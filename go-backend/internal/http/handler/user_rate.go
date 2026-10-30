package handler

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/ws"
)

// Plan 159: per user per server 速率.
//
// Each user with a rate on an entry machine gets one agent limiter there
// (`flvx_user_<id>_rate`, "$ <rate/8>MB <rate/8>MB"). Its service-scope bucket
// is shared by every forward service of that user on the machine:
//   - agents with the userRate capability: every forward service carries
//     metadata.userLimiter; the agent applies the rule's own limiter AND the
//     user limiter (looked up on every call), so a rate change or removal only
//     touches the limiter (no service restart).
//   - older agents: services WITHOUT an own limiter reference the user limiter
//     as their `limiter` (shared bucket); rules with their own limit keep it.
//   - nftables rules: each rule is policed at min(own, rate) (kernel rules
//     cannot share a bucket here).
// The panel never re-creates an unchanged limiter during one agent session
// (userRatePushed): gost services cache a limiter object by its rate, so a
// re-created object with the same rate would split the bucket on old agents.

const userRateLimiterPrefix, userRateLimiterSuffix = "flvx_user_", "_rate"

func userRateLimiterName(userID int64) string {
	return userRateLimiterPrefix + strconv.FormatInt(userID, 10) + userRateLimiterSuffix
}

// userRateLimiterUser parses a limiter name created by userRateLimiterName.
func userRateLimiterUser(name string) (int64, bool) {
	if !strings.HasPrefix(name, userRateLimiterPrefix) || !strings.HasSuffix(name, userRateLimiterSuffix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, userRateLimiterPrefix), userRateLimiterSuffix), 10, 64)
	if err != nil || id <= 0 || userRateLimiterName(id) != name {
		return 0, false
	}
	return id, true
}

// userRateRef is how the forward services of one rule on one machine
// reference the user's limiter.
type userRateRef struct {
	name     string // limiter name; "" = none
	metadata bool   // capable agent: metadata.userLimiter (with the rule's own limiter)
}

func (h *Handler) userRateCapable(nodeID int64) bool {
	if h == nil {
		return false
	}
	if h.userRateCapableFn != nil {
		return h.userRateCapableFn(nodeID)
	}
	return h.wsServer != nil && h.wsServer.UserRateCapable(nodeID)
}

// userRateOnNode is the user's rate on one machine (0 = 不限; remote /
// federated nodes are never limited by it).
func (h *Handler) userRateOnNode(userID int64, node *nodeRecord) int {
	if h == nil || h.repo == nil || userID <= 0 || node == nil || node.IsRemote == 1 {
		return 0
	}
	rate, err := h.repo.UserServerRateMbps(userID, node.ID)
	if err != nil {
		return 0
	}
	return rate
}

// userRateRefFor decides the reference of the gost forward services of one
// rule on one machine (applyUserRateRef leaves a service's own limiter alone
// on older agents).
func (h *Handler) userRateRefFor(userID int64, node *nodeRecord, rate int) userRateRef {
	if userID <= 0 || node == nil || node.IsRemote == 1 {
		return userRateRef{}
	}
	if h.userRateCapable(node.ID) {
		return userRateRef{name: userRateLimiterName(userID), metadata: true}
	}
	if rate > 0 {
		return userRateRef{name: userRateLimiterName(userID)}
	}
	return userRateRef{}
}

// applyUserRateRef adds the reference to the service payloads.
func applyUserRateRef(services []map[string]interface{}, ref userRateRef) {
	if ref.name == "" {
		return
	}
	for _, service := range services {
		if ref.metadata {
			meta, _ := service["metadata"].(map[string]interface{})
			if meta == nil {
				meta = map[string]interface{}{}
				service["metadata"] = meta
			}
			meta["userLimiter"] = ref.name
			continue
		}
		if _, has := service["limiter"]; !has {
			service["limiter"] = ref.name
		}
	}
}

// buildUserRateLimiterPayload is the AddLimiters payload of a user's rate.
func buildUserRateLimiterPayload(userID int64, rate int) map[string]interface{} {
	_, payload := buildNamedLimiterAddPayload(userRateLimiterName(userID), rate)
	return payload
}

// nftRuleSpeed is the per-rule policer of an nftables rule under a user rate.
func nftRuleSpeed(own, rate int) int {
	if rate > 0 && (own <= 0 || own > rate) {
		return rate
	}
	return own
}

// capNftRulesByUserRate polices each nftables rule at min(own, user rate of
// its entry machine).
func (h *Handler) capNftRulesByUserRate(userID int64, rules []NftablesRulePayload) {
	if h == nil || userID <= 0 || len(rules) == 0 {
		return
	}
	rates := map[int64]int{}
	plans := map[int64]*entryRatePlan{}
	for i := range rules {
		nodeID := rules[i].NodeID
		// Plan 168: a tc node shapes the rule in the kernel tree — no
		// policer, except a rule whose arena stays on the agent path (per-rule
		// policer at the arena speed as before; its user's 速率 is in the tree).
		plan, seen := plans[nodeID]
		if !seen {
			plan = h.entryRatePlanFor(nodeID)
			plans[nodeID] = plan
		}
		if plan != nil {
			if plan.tcCovers(rules[i].ForwardID) {
				rules[i].SpeedLimit = 0
			}
			continue
		}
		rate, ok := rates[nodeID]
		if !ok {
			node, err := h.getNodeRecord(nodeID)
			if err == nil {
				rate = h.userRateOnNode(userID, node)
			}
			rates[nodeID] = rate
		}
		rules[i].SpeedLimit = nftRuleSpeed(rules[i].SpeedLimit, rate)
	}
}

// userRatePushLog remembers, per node session, the rate each user limiter was
// last pushed with (memory only, bounded by the users with rules per node).
type userRatePushLog struct {
	mu    sync.Mutex
	nodes map[int64]*userRateNodeLog
}

type userRateNodeLog struct {
	session time.Time
	rates   map[int64]int
}

func (l *userRatePushLog) same(nodeID int64, session time.Time, userID int64, rate int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.nodes[nodeID]
	if n == nil || !n.session.Equal(session) {
		return false
	}
	v, ok := n.rates[userID]
	return ok && v == rate
}

func (l *userRatePushLog) set(nodeID int64, session time.Time, userID int64, rate int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodes == nil {
		l.nodes = map[int64]*userRateNodeLog{}
	}
	n := l.nodes[nodeID]
	if n == nil || !n.session.Equal(session) {
		n = &userRateNodeLog{session: session, rates: map[int64]int{}}
		l.nodes[nodeID] = n
	}
	n.rates[userID] = rate
}

func (l *userRatePushLog) forget(nodeID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.nodes, nodeID)
}

func (h *Handler) nodeSessionStart(nodeID int64) time.Time {
	if h == nil || h.wsServer == nil {
		return time.Time{}
	}
	return h.wsServer.SessionStartedAt(nodeID)
}

// ensureUserRateLimiter makes the user's limiter on the node match rate
// (0 = removed). Unchanged limiters are not pushed again in one session.
func (h *Handler) ensureUserRateLimiter(nodeID, userID int64, rate int) error {
	session := h.nodeSessionStart(nodeID)
	if h.userRatePushed.same(nodeID, session, userID, rate) {
		return nil
	}
	name := userRateLimiterName(userID)
	var err error
	if rate > 0 {
		err = h.upsertNamedLimiterOnNode(nodeID, name, rate)
	} else {
		_, err = h.sendNodeCommand(nodeID, "DeleteLimiters", map[string]interface{}{"limiter": name}, false, true)
	}
	if err != nil {
		return fmt.Errorf("速率下发失败：%w", err)
	}
	h.userRatePushed.set(nodeID, session, userID, rate)
	return nil
}

// applyUserRateChanges pushes the user's new rates (entry machine → rate,
// before and after a settings save). Capable agents only get the limiter;
// older agents and nftables rules get their forwards re-pushed where the
// payload changes. Offline machines are fixed by their reconnect.
func (h *Handler) applyUserRateChanges(userID int64, before, after map[int64]int) []string {
	if h == nil || userID <= 0 {
		return nil
	}
	var warnings []string
	nodes := map[int64]bool{}
	for id := range before {
		nodes[id] = true
	}
	for id := range after {
		nodes[id] = true
	}
	for nodeID := range nodes {
		oldRate, newRate := before[nodeID], after[nodeID]
		if oldRate == newRate {
			continue
		}
		node, err := h.getNodeRecord(nodeID)
		if err != nil || node == nil || node.IsRemote == 1 {
			continue
		}
		if !h.nodeOnline(nodeID) {
			warnings = append(warnings, fmt.Sprintf("服务器 %s 不在线，速率在它上线后自动生效", node.Name))
			continue
		}
		if h.entryRateTc(nodeID) {
			// Plan 168: the user's group in the node's tree (class change,
			// no service or rule push).
			if err := h.syncEntryRates(nodeID); err != nil {
				warnings = append(warnings, fmt.Sprintf("服务器 %s %v", node.Name, err))
			}
			continue
		}
		capable := h.userRateCapable(nodeID)
		if newRate > 0 {
			if err := h.ensureUserRateLimiter(nodeID, userID, newRate); err != nil {
				warnings = append(warnings, fmt.Sprintf("服务器 %s %v", node.Name, err))
				continue
			}
		}
		warnings = append(warnings, h.resyncForwardsForRate(userID, nodeID, oldRate, newRate, capable)...)
		if newRate <= 0 {
			if err := h.ensureUserRateLimiter(nodeID, userID, 0); err != nil {
				warnings = append(warnings, fmt.Sprintf("服务器 %s %v", node.Name, err))
			}
		}
	}
	return warnings
}

func (h *Handler) nodeOnline(nodeID int64) bool {
	if h.nodeCommandSender != nil {
		return true // tests drive commands directly
	}
	return !h.nodeSessionStart(nodeID).IsZero()
}

// resyncForwardsForRate re-pushes the user's forwards on one machine whose
// payload depends on the rate change: nftables rules whose policer changes,
// and (older agents) gost rules without an own limiter when the rate starts
// or stops.
func (h *Handler) resyncForwardsForRate(userID, nodeID int64, oldRate, newRate int, capable bool) []string {
	forwards, err := h.repo.ListActiveForwardsByUser(userID)
	if err != nil {
		return []string{fmt.Sprintf("速率同步失败: %v", err)}
	}
	var warnings []string
	for _, item := range forwards {
		forward, err := h.getForwardRecord(item.ID)
		if err != nil || forward == nil || forward.Status != 1 || strings.EqualFold(forward.Mode, "wg_path") {
			continue
		}
		ports, err := h.listForwardPorts(forward.ID)
		if err != nil {
			continue
		}
		onNode := false
		for _, p := range ports {
			if p.NodeID == nodeID {
				onNode = true
				break
			}
		}
		if !onNode {
			continue
		}
		in, err := h.resolveForwardRuntimeInputs(forward)
		if err != nil {
			continue
		}
		if strings.EqualFold(forward.Mode, "nftables") {
			own := 0
			if forward.SpeedLimitEnabled && forward.SpeedLimit > 0 {
				own = forward.SpeedLimit
			} else if in.speed != nil {
				own = *in.speed
			}
			if nftRuleSpeed(own, oldRate) == nftRuleSpeed(own, newRate) {
				continue
			}
		} else {
			ownLimiter := in.dynamicLimiterName != "" || (in.limiterID != nil && *in.limiterID > 0)
			if capable || ownLimiter || (oldRate > 0) == (newRate > 0) {
				continue
			}
		}
		if err := h.syncForwardServicesOnNode(forward, nodeID); err != nil {
			warnings = append(warnings, fmt.Sprintf("规则 %s 速率下发失败: %v", forward.Name, err))
		}
	}
	return warnings
}

// userRateReconnectPush makes the user limiters of a reconnected node match
// the panel before its forwards are compared / pushed: missing or different
// ones are pushed. inv nil = unknown (full redeploy): push all expected.
func (h *Handler) userRateReconnectPush(nodeID int64, inv *agentRuntimeInventory) (map[int64]int, int, string) {
	h.userRatePushed.forget(nodeID)
	node, err := h.getNodeRecord(nodeID)
	if err != nil || node == nil || node.IsRemote == 1 {
		return nil, 0, ""
	}
	rates, err := h.repo.NodeUserRates(nodeID)
	if err != nil {
		return nil, 0, "user rates not read: " + err.Error()
	}
	if inv != nil {
		inv.userRates, inv.userRatesLoaded = rates, true
	}
	if h.entryRateTc(nodeID) {
		// Plan 168: the 速率 lives in the kernel tree; no user limiter is
		// needed, leftovers are removed by userRateReconnectStale.
		return map[int64]int{}, 0, ""
	}
	session := h.nodeSessionStart(nodeID)
	pushed := 0
	var failed []string
	for userID, rate := range rates {
		if inv != nil && inv.matches(ws.ExpectedRuntimeRevs("AddLimiters", buildUserRateLimiterPayload(userID, rate))) {
			h.userRatePushed.set(nodeID, session, userID, rate)
			continue
		}
		if err := h.ensureUserRateLimiter(nodeID, userID, rate); err != nil {
			failed = append(failed, fmt.Sprintf("user %d: %v", userID, err))
			continue
		}
		pushed++
	}
	if len(failed) > 0 {
		return rates, pushed, "user rate limiters: " + strings.Join(failed, "; ")
	}
	return rates, pushed, ""
}

// userRateReconnectStale deletes user limiters the agent still runs although
// the user has no rate there any more (removed while the node was away).
// Runs after the forwards were pushed (older agents reference it by name).
func (h *Handler) userRateReconnectStale(nodeID int64, inv *agentRuntimeInventory, rates map[int64]int) (int, string) {
	if inv == nil || rates == nil {
		return 0, ""
	}
	removed := 0
	var failed []string
	for _, name := range inv.inv.Limiters {
		userID, ok := userRateLimiterUser(name)
		if !ok || rates[userID] > 0 {
			continue
		}
		if _, err := h.sendNodeCommand(nodeID, "DeleteLimiters", map[string]interface{}{"limiter": name}, false, true); err != nil {
			failed = append(failed, fmt.Sprintf("delete %s: %v", name, err))
			continue
		}
		removed++
	}
	if len(failed) > 0 {
		return removed, "user rate limiters: " + strings.Join(failed, "; ")
	}
	return removed, ""
}

// userRateLimiterInUse: name is a user rate limiter whose user still has a
// rate on the node (kept by the config-report limiter cleanup).
func (h *Handler) userRateLimiterInUse(nodeID int64, name string) bool {
	userID, ok := userRateLimiterUser(name)
	if !ok {
		return false
	}
	node, err := h.getNodeRecord(nodeID)
	if err != nil || node == nil {
		return true // unsure: keep (the reconnect diff removes stale ones)
	}
	return h.userRateOnNode(userID, node) > 0
}

// rateFor is the user's rate on the inventory's node (loaded once per
// reconnect by userRateReconnectPush; -1 = not loaded, 0 = no rate).
func (a *agentRuntimeInventory) rateFor(userID int64) int {
	if a == nil || !a.userRatesLoaded {
		return -1
	}
	return a.userRates[userID]
}

// userRateBefore snapshots the user's entry-machine rates (nil on error, which
// makes the caller skip the propagation instead of guessing).
func (h *Handler) userRateBefore(userID int64) map[int64]int {
	rates, err := h.repo.UserEntryRates(userID)
	if err != nil {
		return nil
	}
	return rates
}

// userRateAfter propagates a settings change of one user.
func (h *Handler) userRateAfter(userID int64, before map[int64]int) []string {
	if before == nil {
		return nil
	}
	after, err := h.repo.UserEntryRates(userID)
	if err != nil {
		return []string{fmt.Sprintf("速率同步失败: %v", err)}
	}
	return h.applyUserRateChanges(userID, before, after)
}

// ruleOwnSpeed is the Mbps of a rule's own limit: the dynamic speed when
// enabled, else its 限速规则 (0 = none).
func (h *Handler) ruleOwnSpeed(dynamicEnabled bool, dynamic int, speedID sql.NullInt64) int {
	if dynamicEnabled && dynamic > 0 {
		return dynamic
	}
	if speedID.Valid && speedID.Int64 > 0 && h != nil && h.repo != nil {
		if limit, err := h.repo.GetSpeedLimit(speedID.Int64); err == nil && limit != nil && limit.Speed > 0 {
			return limit.Speed
		}
	}
	return 0
}

// validateRuleSpeedAgainstRows: an admin-set rule speed (Mbps) may not exceed
// the user's rate on any entry machine of the rule.
func (h *Handler) validateRuleSpeedAgainstRows(userID int64, nodeIDs []int64, speed int) error {
	if h == nil || speed <= 0 || userID <= 0 {
		return nil
	}
	for _, nodeID := range nodeIDs {
		node, err := h.getNodeRecord(nodeID)
		if err != nil || node == nil {
			continue
		}
		if rate := h.userRateOnNode(userID, node); rate > 0 && speed > rate {
			return fmt.Errorf("规则限速 %d Mbps 不能超过该用户在服务器「%s」的速率 %d Mbps", speed, node.Name, rate)
		}
	}
	return nil
}
