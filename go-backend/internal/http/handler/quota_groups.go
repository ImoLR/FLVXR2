package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

const quotaGroupPushInterval = time.Second

// Plan 167: a node's budgets move whenever the user's live counts move on
// other nodes, so pushing every difference sent one SetQuotaGroups per second
// to busy entries (node 32: ~1350/h) while the user sat far below the limit.
// Differences are now classed (quotaPushClass): urgent ones still go out at
// once (quotaGroupPushInterval apart), the rest wait.
const (
	// Loosening beyond the band while the node is not about to refuse.
	quotaPushSoonInterval = 15 * time.Second
	// Drift inside the band: harmless either way, refreshed this often.
	quotaPushRefreshInterval = 5 * time.Minute
	// One summary line of push counts at most this often (only when pushes happened).
	quotaPushStatsInterval = time.Hour
	// The band: a change smaller than 1/quotaPushBandDivisor of the node's
	// remaining room (budget minus its live usage) may wait. Near the limit the
	// room shrinks, so every change becomes urgent and enforcement is exact.
	quotaPushBandDivisor = 4
	// Room at or below this is "at the limit": every change is urgent.
	quotaPushConnSlack = 2
	quotaPushIPSlack   = 1
)

// Plan 158: the static inputs (limited users, per-server caps, landing slots)
// are re-read at most this often unless a mutation invalidates them; forward
// targets are still read on every reconcile, as before.
const quotaExtrasTTL = 5 * time.Second

// Shared (妙妙屋) landing usage older than this no longer counts: the share's
// exchange stopped (Custom down, capability withdrawn).
const quotaSharedUsageTTL = 2 * time.Minute

// landingQuotaGroupPrefix names a user's group on a landing Core (plan 158).
// Older panels ignore these usage entries (they only parse "user-<id>").
const landingQuotaGroupPrefix = "landing-"

type quotaGroupBudget struct {
	Group          string `json:"group"`
	MaxConnections int    `json:"maxConnections"`
	MaxClientIps   int    `json:"maxClientIps"`
	// Plan 158, landing-user-* groups only. MaxConnections / MaxClientIps are
	// then the user-total budgets WITHOUT relayed connections / relay IPs (the
	// landing Core adds its live relay count); ServerMaxConnections is the
	// per-server 入站连接数 budget in server scope (relays included), nil =
	// none; RelayIps = our nodes that dial the user's nodes there (each keeps
	// an IP slot). Forward groups never carry them (byte-identical payload).
	ServerMaxConnections *int `json:"serverMaxConnections,omitempty"`
	RelayIps             int  `json:"relayIps,omitempty"`
	// SharedIps (plan 158, forward groups of a user with 总 IP 数): the user's
	// IPs active on other slots; the entry admits them without using one of
	// its IP slots (distinct IPs everywhere). Older agents ignore it.
	SharedIps []string `json:"sharedIps,omitempty"`
}

// maxQuotaSharedIPs bounds one budget's shared-IP list (agents refuse more).
const maxQuotaSharedIPs = 256

type quotaNodeUsage struct {
	Connections int
	ClientIps   map[string]struct{}
	// Relay (landing groups only): connections from our own nodes, excluded
	// from Connections; they still count for the server-scope cap.
	Relay int
}

type quotaLiveUsage struct {
	Connections int
	ClientIps   int
}

type quotaBudgetPlan struct {
	Budgets      map[int64][]quotaGroupBudget
	NodeVersions map[int64]string
	LiveUsage    map[int64]quotaLiveUsage
	// Shares: landing_server.id → per-user budgets for 妙妙屋 (plan 158).
	Shares map[int64][]quotaShareBudget
	// Usage (plan 167): node → group → the node's own live usage that each
	// budget is compared with when deciding how urgent a change is.
	Usage map[int64]map[string]quotaBudgetUsage
}

// quotaBudgetUsage is a node's own live usage of one group: Connections is
// compared with MaxConnections, Server with ServerMaxConnections (landing,
// relays included) and ClientIps (local distinct IPs) with MaxClientIps.
type quotaBudgetUsage struct {
	Connections int
	Server      int
	ClientIps   int
}

// quotaPushUrgency classes the difference between a node's last pushed and
// current budgets (plan 167).
type quotaPushUrgency uint8

const (
	quotaPushNone  quotaPushUrgency = iota // identical
	quotaPushLater                         // drift inside the band: quotaPushRefreshInterval
	quotaPushSoon                          // loosening beyond the band: quotaPushSoonInterval
	quotaPushNow                           // tightening that matters, node about to refuse, structure: at once
)

type quotaPushStats struct {
	since                    time.Time
	now, soon, later, failed int
	perNode                  map[int64]int
}

// quotaShareBudget is one user's budget on a shared server (contract
// landing-user-limits-v1, same meaning as a landing-user-* group budget).
type quotaShareBudget struct {
	UserID               int64
	MaxConnections       int
	ServerMaxConnections *int
	MaxClientIps         int
	RelayIps             int
}

type quotaTargetSource interface {
	ListQuotaGroupTargets() ([]repo.QuotaGroupTarget, error)
}

// quotaExtrasSource is the plan-158 part of the source (the repository).
type quotaExtrasSource interface {
	LoadQuotaPlanExtras() (repo.QuotaPlanExtras, error)
}

type quotaCommandSender func(nodeID int64, budgets []quotaGroupBudget) error

// quotaUsageKey separates a node's forward group from its landing group.
type quotaUsageKey struct {
	UserID  int64
	Landing bool
}

type quotaSharedUsage struct {
	at    time.Time
	users map[int64]quotaNodeUsage
}

type quotaCoordinator struct {
	source   quotaTargetSource
	send     quotaCommandSender
	interval time.Duration
	// Plan 167 push classes (see quotaPushClass).
	soonInterval    time.Duration
	refreshInterval time.Duration
	now             func() time.Time

	mu            sync.Mutex
	usageByNode   map[int64]map[quotaUsageKey]quotaNodeUsage
	usageByShare  map[int64]quotaSharedUsage
	liveUsage     map[int64]quotaLiveUsage
	shareBudgets  map[int64][]quotaShareBudget
	landingPushed map[int64]bool
	lastSent      map[int64]string
	// sentBudgets: the budgets of lastSent, compared field by field.
	sentBudgets map[int64][]quotaGroupBudget
	// forcePush: limits / landing inputs changed — push every changed node now.
	forcePush     bool
	stats         quotaPushStats
	lastPush      map[int64]time.Time
	unsupported   map[int64]bool
	errorLogged   map[int64]bool
	extras        repo.QuotaPlanExtras
	extrasAt      time.Time
	extrasStale   bool
	running       bool
	pending       bool
	timer         *time.Timer
	lastReconcile time.Time
}

func newQuotaCoordinator(source quotaTargetSource, send quotaCommandSender) *quotaCoordinator {
	return &quotaCoordinator{
		source:          source,
		send:            send,
		interval:        quotaGroupPushInterval,
		soonInterval:    quotaPushSoonInterval,
		refreshInterval: quotaPushRefreshInterval,
		now:             time.Now,
		usageByNode:     make(map[int64]map[quotaUsageKey]quotaNodeUsage),
		usageByShare:    make(map[int64]quotaSharedUsage),
		liveUsage:       make(map[int64]quotaLiveUsage),
		shareBudgets:    make(map[int64][]quotaShareBudget),
		landingPushed:   make(map[int64]bool),
		lastSent:        make(map[int64]string),
		sentBudgets:     make(map[int64][]quotaGroupBudget),
		lastPush:        make(map[int64]time.Time),
		unsupported:     make(map[int64]bool),
		errorLogged:     make(map[int64]bool),
		extrasStale:     true,
	}
}

func quotaGroupForUser(userID int64) string {
	return fmt.Sprintf("user-%d", userID)
}

// landingQuotaGroupForUser is the user's group on a landing Core.
func landingQuotaGroupForUser(userID int64) string {
	return landingQuotaGroupPrefix + quotaGroupForUser(userID)
}

func quotaGroupUserID(group string) (int64, bool) {
	var userID int64
	if _, err := fmt.Sscanf(strings.TrimSpace(group), "user-%d", &userID); err != nil || userID <= 0 {
		return 0, false
	}
	if quotaGroupForUser(userID) != strings.TrimSpace(group) {
		return 0, false
	}
	return userID, true
}

// quotaUsageGroupKey parses "user-<id>" (forward) and "landing-user-<id>".
func quotaUsageGroupKey(group string) (quotaUsageKey, bool) {
	group = strings.TrimSpace(group)
	landing := strings.HasPrefix(group, landingQuotaGroupPrefix)
	userID, ok := quotaGroupUserID(strings.TrimPrefix(group, landingQuotaGroupPrefix))
	return quotaUsageKey{UserID: userID, Landing: landing}, ok
}

func quotaUsageFrom(connections, relay int, rawIPs []string) quotaNodeUsage {
	ips := make(map[string]struct{}, len(rawIPs))
	for _, rawIP := range rawIPs {
		if ip := strings.TrimSpace(rawIP); ip != "" {
			ips[ip] = struct{}{}
		}
	}
	return quotaNodeUsage{Connections: max(connections, 0), ClientIps: ips, Relay: max(relay, 0)}
}

func (c *quotaCoordinator) Observe(nodeID int64, usages []ws.QuotaGroupUsage) {
	if c == nil || nodeID <= 0 {
		return
	}
	nodeUsage := make(map[quotaUsageKey]quotaNodeUsage, len(usages))
	for _, item := range usages {
		key, ok := quotaUsageGroupKey(item.Group)
		if !ok {
			continue
		}
		relay := 0
		if key.Landing {
			relay = item.RelayConnections
		}
		nodeUsage[key] = quotaUsageFrom(item.Connections, relay, item.ClientIps)
	}

	c.mu.Lock()
	c.usageByNode[nodeID] = nodeUsage
	c.mu.Unlock()
	c.Trigger()
}

// quotaShareUsage is one user's usage on a shared server as Custom reports it.
type quotaShareUsage struct {
	UserID      int64
	Connections int
	Relay       int
	ClientIps   []string
}

// ObserveShare records the per-user usage a 妙妙屋 share reported (plan 158).
func (c *quotaCoordinator) ObserveShare(serverID int64, usages []quotaShareUsage) {
	if c == nil || serverID <= 0 {
		return
	}
	users := make(map[int64]quotaNodeUsage, len(usages))
	for _, u := range usages {
		if u.UserID > 0 {
			users[u.UserID] = quotaUsageFrom(u.Connections, u.Relay, u.ClientIps)
		}
	}
	c.mu.Lock()
	c.usageByShare[serverID] = quotaSharedUsage{at: c.now(), users: users}
	c.mu.Unlock()
	c.Trigger()
}

// ForgetShare drops a share's usage (exchange no longer possible).
func (c *quotaCoordinator) ForgetShare(serverID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	_, had := c.usageByShare[serverID]
	delete(c.usageByShare, serverID)
	c.mu.Unlock()
	if had {
		c.Trigger()
	}
}

// ShareBudgets returns the latest per-user budgets of a shared server and
// whether the server has a user-limits slot at all.
func (c *quotaCoordinator) ShareBudgets(serverID int64) ([]quotaShareBudget, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	budgets, ok := c.shareBudgets[serverID]
	return append([]quotaShareBudget(nil), budgets...), ok
}

func (c *quotaCoordinator) NodeOnline(nodeID int64) {
	if c == nil || nodeID <= 0 {
		return
	}
	c.mu.Lock()
	delete(c.usageByNode, nodeID)
	delete(c.lastSent, nodeID)
	delete(c.sentBudgets, nodeID)
	delete(c.lastPush, nodeID)
	delete(c.unsupported, nodeID)
	delete(c.errorLogged, nodeID)
	delete(c.landingPushed, nodeID)
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) NodeOffline(nodeID int64) {
	if c == nil || nodeID <= 0 {
		return
	}
	c.mu.Lock()
	delete(c.usageByNode, nodeID)
	delete(c.lastSent, nodeID)
	delete(c.sentBudgets, nodeID)
	delete(c.lastPush, nodeID)
	delete(c.landingPushed, nodeID)
	c.mu.Unlock()
	c.Trigger()
}

// InvalidateNodes forces a fresh push to the nodes (their forwards changed:
// services were re-created and joined their groups with static budgets).
func (c *quotaCoordinator) InvalidateNodes(nodeIDs ...int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	for _, nodeID := range nodeIDs {
		if nodeID > 0 {
			delete(c.lastSent, nodeID)
			delete(c.sentBudgets, nodeID)
		}
	}
	c.extrasStale = true
	c.mu.Unlock()
	c.Trigger()
}

// InvalidateInputs re-reads limited users, per-server caps and landing slots
// on the next reconcile (after a user / per-server limit / landing change);
// every budget that changes because of it is pushed at once (plan 167).
func (c *quotaCoordinator) InvalidateInputs() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.extrasStale = true
	c.forcePush = true
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) Trigger() {
	if c == nil || c.source == nil || c.send == nil {
		return
	}
	c.mu.Lock()
	c.pending = true
	if c.running || c.timer != nil {
		c.mu.Unlock()
		return
	}

	now := c.now()
	delay := time.Duration(0)
	if !c.lastReconcile.IsZero() {
		elapsed := now.Sub(c.lastReconcile)
		if elapsed < c.interval {
			delay = c.interval - elapsed
		}
	}
	if delay > 0 {
		c.timer = time.AfterFunc(delay, c.startScheduledReconcile)
		c.mu.Unlock()
		return
	}
	c.running = true
	c.pending = false
	c.lastReconcile = now
	c.mu.Unlock()
	go c.runReconcile()
}

func (c *quotaCoordinator) startScheduledReconcile() {
	c.mu.Lock()
	c.timer = nil
	if c.running || !c.pending {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.pending = false
	c.lastReconcile = c.now()
	c.mu.Unlock()
	c.runReconcile()
}

func (c *quotaCoordinator) runReconcile() {
	c.reconcileOnce()
	c.mu.Lock()
	c.running = false
	pending := c.pending
	c.mu.Unlock()
	if pending {
		c.Trigger()
	}
}

// loadExtras returns the cached plan-158 inputs, re-read when stale.
func (c *quotaCoordinator) loadExtras() (repo.QuotaPlanExtras, bool) {
	source, ok := c.source.(quotaExtrasSource)
	if !ok {
		return repo.QuotaPlanExtras{}, false
	}
	c.mu.Lock()
	fresh := !c.extrasStale && !c.extrasAt.IsZero() && c.now().Sub(c.extrasAt) < quotaExtrasTTL
	cached := c.extras
	c.mu.Unlock()
	if fresh {
		return cached, true
	}
	extras, err := source.LoadQuotaPlanExtras()
	if err != nil {
		log.Printf("quota groups: load per-server/landing inputs failed: %v", err)
		return cached, !c.extrasAt.IsZero()
	}
	c.mu.Lock()
	c.extras, c.extrasAt, c.extrasStale = extras, c.now(), false
	c.mu.Unlock()
	return extras, true
}

func (c *quotaCoordinator) reconcileOnce() {
	// Read before the inputs: an invalidation racing this reconcile keeps
	// its flag for the next one.
	c.mu.Lock()
	force := c.forcePush
	c.forcePush = false
	c.mu.Unlock()
	targets, err := c.source.ListQuotaGroupTargets()
	if err != nil {
		log.Printf("quota groups: list targets failed: %v", err)
		c.mu.Lock()
		c.forcePush = c.forcePush || force
		c.mu.Unlock()
		return
	}
	extras, hasExtras := c.loadExtras()

	c.mu.Lock()
	now := c.now()
	nodeUsage := cloneQuotaUsage(c.usageByNode)
	shareUsage := make(map[int64]map[int64]quotaNodeUsage, len(c.usageByShare))
	for serverID, s := range c.usageByShare {
		if now.Sub(s.at) > quotaSharedUsageTTL {
			delete(c.usageByShare, serverID)
			continue
		}
		shareUsage[serverID] = s.users
	}
	c.mu.Unlock()
	var plan quotaBudgetPlan
	if hasExtras {
		plan = computeQuotaPlan(targets, extras, nodeUsage, shareUsage)
	} else {
		plan = computeQuotaPlan(targets, repo.QuotaPlanExtras{}, nodeUsage, shareUsage)
	}

	c.mu.Lock()
	c.liveUsage = plan.LiveUsage
	c.shareBudgets = plan.Shares
	// A landing-capable agent that had landing groups and has none now gets
	// one empty list: its landing groups fall back to the static desired.
	resets := map[int64]bool{}
	for nodeID := range c.landingPushed {
		if _, ok := plan.Budgets[nodeID]; !ok {
			plan.Budgets[nodeID] = []quotaGroupBudget{}
			resets[nodeID] = true
		}
	}
	c.mu.Unlock()

	var wg sync.WaitGroup
	for nodeID, budgets := range plan.Budgets {
		encoded, _ := json.Marshal(budgets)
		hash := string(encoded)
		now := c.now()

		c.mu.Lock()
		if c.unsupported[nodeID] || c.lastSent[nodeID] == hash {
			c.mu.Unlock()
			continue
		}
		class := quotaPushNow
		if sent, ok := c.sentBudgets[nodeID]; ok && !force && !resets[nodeID] {
			class = quotaPushClass(sent, budgets, plan.Usage[nodeID])
		}
		if !c.pushDue(class, c.lastPush[nodeID], now) {
			// Not due yet: the next reconcile (every agent report) re-checks;
			// a forced push stays forced until it went out.
			c.forcePush = c.forcePush || force
			c.mu.Unlock()
			continue
		}
		c.lastPush[nodeID] = now
		c.countPush(nodeID, class)
		c.mu.Unlock()

		budgetsCopy := append([]quotaGroupBudget(nil), budgets...)
		version := plan.NodeVersions[nodeID]
		landing := false
		for _, b := range budgets {
			if strings.HasPrefix(b.Group, landingQuotaGroupPrefix) {
				landing = true
				break
			}
		}
		wg.Add(1)
		go func(nodeID int64) {
			defer wg.Done()
			err := c.send(nodeID, budgetsCopy)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err == nil {
				c.lastSent[nodeID] = hash
				c.sentBudgets[nodeID] = budgetsCopy
				delete(c.errorLogged, nodeID)
				if landing {
					c.landingPushed[nodeID] = true
				} else {
					delete(c.landingPushed, nodeID)
				}
				return
			}
			c.stats.failed++
			// An empty reset list only goes to agents known to take landing
			// groups; its failure (e.g. a reconnect) never marks them unsupported.
			if !resets[nodeID] && quotaAgentSupport(version) == quotaAgentUnknown {
				c.unsupported[nodeID] = true
			}
			if !c.errorLogged[nodeID] {
				log.Printf("quota groups: node %d rejected SetQuotaGroups: %v", nodeID, err)
				c.errorLogged[nodeID] = true
			}
		}(nodeID)
	}
	wg.Wait()
	c.logPushStats()
}

// pushDue reports whether a difference of the given class may be pushed now,
// given the node's last push. Caller holds c.mu.
func (c *quotaCoordinator) pushDue(class quotaPushUrgency, last, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	wait := c.interval
	switch class {
	case quotaPushSoon:
		wait = max(c.soonInterval, c.interval)
	case quotaPushLater:
		wait = max(c.refreshInterval, c.interval)
	}
	return now.Sub(last) >= wait
}

// countPush records one push for the hourly summary. Caller holds c.mu.
func (c *quotaCoordinator) countPush(nodeID int64, class quotaPushUrgency) {
	if c.stats.perNode == nil {
		c.stats.perNode = make(map[int64]int)
	}
	if c.stats.since.IsZero() {
		c.stats.since = c.now()
	}
	switch class {
	case quotaPushSoon:
		c.stats.soon++
	case quotaPushLater:
		c.stats.later++
	default:
		c.stats.now++
	}
	c.stats.perNode[nodeID]++
}

// logPushStats writes one line with the push counts at most every
// quotaPushStatsInterval, and nothing in an hour without pushes.
func (c *quotaCoordinator) logPushStats() {
	c.mu.Lock()
	s := c.stats
	if s.since.IsZero() || c.now().Sub(s.since) < quotaPushStatsInterval {
		c.mu.Unlock()
		return
	}
	c.stats = quotaPushStats{}
	c.mu.Unlock()
	total := s.now + s.soon + s.later
	nodes := make([]int64, 0, len(s.perNode))
	for nodeID := range s.perNode {
		nodes = append(nodes, nodeID)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return s.perNode[nodes[i]] > s.perNode[nodes[j]] || (s.perNode[nodes[i]] == s.perNode[nodes[j]] && nodes[i] < nodes[j])
	})
	if len(nodes) > 8 {
		nodes = nodes[:8]
	}
	parts := make([]string, 0, len(nodes))
	for _, nodeID := range nodes {
		parts = append(parts, fmt.Sprintf("%d:%d", nodeID, s.perNode[nodeID]))
	}
	log.Printf("quota groups: %d SetQuotaGroups pushes in %s (now %d, soon %d, refresh %d, failed %d), top nodes %s",
		total, c.now().Sub(s.since).Round(time.Minute), s.now, s.soon, s.later, s.failed, strings.Join(parts, " "))
}

// quotaPushClass classes the difference between the last pushed budgets of a
// node and the current ones (plan 167). Over-admission can only come from a
// stale budget that is too high, and only by its excess; refusals only from a
// stale budget the node's usage has reached. So:
//   - tightening is urgent when the excess is at least 1/quotaPushBandDivisor
//     of the room left under the new budget, or the room is at the limit
//     (≤ slack): an over-limit user gets 0 at once, as before; a smaller
//     excess waits for the refresh (it cannot be used up before the room
//     itself shrinks, which makes it urgent);
//   - loosening is urgent when the node is at (or near) its stale budget; a
//     loosening beyond the band waits quotaPushSoonInterval, a smaller one the
//     refresh;
//   - added / removed groups, unlimited <-> limited, server cap or relay IP
//     changes are urgent.
func quotaPushClass(sent, current []quotaGroupBudget, usage map[string]quotaBudgetUsage) quotaPushUrgency {
	if len(sent) != len(current) {
		return quotaPushNow
	}
	byGroup := make(map[string]quotaGroupBudget, len(sent))
	for _, b := range sent {
		byGroup[b.Group] = b
	}
	worst := quotaPushNone
	for _, b := range current {
		old, ok := byGroup[b.Group]
		if !ok || old.RelayIps != b.RelayIps || (old.ServerMaxConnections == nil) != (b.ServerMaxConnections == nil) {
			return quotaPushNow
		}
		u := usage[b.Group]
		worst = max(worst, quotaLimitUrgency(old.MaxConnections, b.MaxConnections, u.Connections, quotaPushConnSlack))
		worst = max(worst, quotaLimitUrgency(old.MaxClientIps, b.MaxClientIps, u.ClientIps, quotaPushIPSlack))
		if b.ServerMaxConnections != nil {
			worst = max(worst, quotaLimitUrgency(*old.ServerMaxConnections, *b.ServerMaxConnections, u.Server, quotaPushConnSlack))
		}
		worst = max(worst, quotaSharedIPsUrgency(old, b, u.ClientIps))
		if worst == quotaPushNow {
			return worst
		}
	}
	return worst
}

// quotaLimitUrgency classes one budget change (-1 = unlimited) against the
// node's own live usage of it.
func quotaLimitUrgency(sent, current, used, slack int) quotaPushUrgency {
	switch {
	case sent == current:
		return quotaPushNone
	case sent < 0 || current < 0:
		return quotaPushNow
	case current < sent:
		room := current - used
		if room <= slack || (sent-current)*quotaPushBandDivisor >= room {
			return quotaPushNow
		}
		return quotaPushLater
	default:
		room := sent - used
		if room <= slack {
			return quotaPushNow
		}
		if (current-sent)*quotaPushBandDivisor >= room {
			return quotaPushSoon
		}
		return quotaPushLater
	}
}

// quotaSharedIPsUrgency: an IP left off the list stays free on the node until
// the push, which only matters if the IP budget does not grow by as much at
// the same time (the IP then counts again, one slot more). Added IPs only make
// the stale list stricter: urgent when the node is at its IP budget.
func quotaSharedIPsUrgency(sent, current quotaGroupBudget, usedIPs int) quotaPushUrgency {
	if len(sent.SharedIps) == 0 && len(current.SharedIps) == 0 {
		return quotaPushNone
	}
	now := make(map[string]struct{}, len(current.SharedIps))
	for _, ip := range current.SharedIps {
		now[ip] = struct{}{}
	}
	removed := 0
	for _, ip := range sent.SharedIps {
		if _, ok := now[ip]; ok {
			delete(now, ip)
		} else {
			removed++
		}
	}
	added := len(now)
	switch {
	case removed == 0 && added == 0:
		return quotaPushNone
	case removed > 0 && (sent.MaxClientIps < 0 || current.MaxClientIps < 0 || current.MaxClientIps-sent.MaxClientIps < removed):
		return quotaPushNow
	case sent.MaxClientIps >= 0 && sent.MaxClientIps-usedIPs <= quotaPushIPSlack:
		return quotaPushNow
	default:
		return quotaPushSoon
	}
}

func (c *quotaCoordinator) LiveUsage() map[int64]quotaLiveUsage {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int64]quotaLiveUsage, len(c.liveUsage))
	for userID, usage := range c.liveUsage {
		out[userID] = usage
	}
	return out
}

func cloneQuotaUsage(source map[int64]map[quotaUsageKey]quotaNodeUsage) map[int64]map[quotaUsageKey]quotaNodeUsage {
	out := make(map[int64]map[quotaUsageKey]quotaNodeUsage, len(source))
	for nodeID, users := range source {
		out[nodeID] = make(map[quotaUsageKey]quotaNodeUsage, len(users))
		for key, usage := range users {
			ips := make(map[string]struct{}, len(usage.ClientIps))
			for ip := range usage.ClientIps {
				ips[ip] = struct{}{}
			}
			out[nodeID][key] = quotaNodeUsage{Connections: usage.Connections, ClientIps: ips, Relay: usage.Relay}
		}
	}
	return out
}

// computeQuotaBudgetPlan is the forward-only plan (before plan 158), kept for
// callers and tests that only know forward targets.
func computeQuotaBudgetPlan(targets []repo.QuotaGroupTarget, usageByNode map[int64]map[int64]quotaNodeUsage) quotaBudgetPlan {
	usage := make(map[int64]map[quotaUsageKey]quotaNodeUsage, len(usageByNode))
	for nodeID, users := range usageByNode {
		usage[nodeID] = make(map[quotaUsageKey]quotaNodeUsage, len(users))
		for userID, u := range users {
			usage[nodeID][quotaUsageKey{UserID: userID}] = u
		}
	}
	return computeQuotaPlan(targets, repo.QuotaPlanExtras{}, usage, nil)
}

type quotaSlotKind uint8

const (
	quotaSlotForward quotaSlotKind = iota
	quotaSlotLanding               // native landing Core, applied by the node's agent
	quotaSlotShared                // 妙妙屋 shared server, applied by its Helper
)

// quotaSlot is one place where a user's inbound connections are counted and
// limited. Per-server caps apply to all slots with the same server key.
type quotaSlot struct {
	kind        quotaSlotKind
	nodeID      int64 // forward / native landing: the agent node
	serverID    int64 // native / shared landing: landing_server.id
	key         repo.ServerQuotaKey
	relayNodes  int
	pushable    bool
	nodeVersion string
	usage       quotaNodeUsage
}

// serverUsage is what the slot adds to its server's 入站连接数: a landing Core
// counts every inbound it receives, relayed ones included.
func (s *quotaSlot) serverUsage() int { return s.usage.Connections + s.usage.Relay }

type quotaUserPlan struct {
	limits repo.QuotaUserLimits
	slots  []*quotaSlot
}

// computeQuotaPlan splits every limited user's budgets over the user's slots:
// the user total (总连接数 / 总 IP 数, deduped: relayed landing inbound counted
// at the entry only) and each server's 入站连接数 (server scope).
func computeQuotaPlan(targets []repo.QuotaGroupTarget, extras repo.QuotaPlanExtras, usageByNode map[int64]map[quotaUsageKey]quotaNodeUsage, usageByShare map[int64]map[int64]quotaNodeUsage) quotaBudgetPlan {
	plan := quotaBudgetPlan{
		Budgets:      make(map[int64][]quotaGroupBudget),
		NodeVersions: make(map[int64]string),
		LiveUsage:    make(map[int64]quotaLiveUsage),
		Shares:       make(map[int64][]quotaShareBudget),
		Usage:        make(map[int64]map[string]quotaBudgetUsage),
	}
	noteUsage := func(nodeID int64, group string, s *quotaSlot) {
		if plan.Usage[nodeID] == nil {
			plan.Usage[nodeID] = make(map[string]quotaBudgetUsage)
		}
		plan.Usage[nodeID][group] = quotaBudgetUsage{Connections: s.usage.Connections, Server: s.serverUsage(), ClientIps: len(s.usage.ClientIps)}
	}
	users := make(map[int64]*quotaUserPlan)
	user := func(userID int64, limits repo.QuotaUserLimits) *quotaUserPlan {
		u := users[userID]
		if u == nil {
			u = &quotaUserPlan{limits: limits}
			users[userID] = u
		}
		return u
	}
	capUsers := make(map[int64]bool)
	for key := range extras.Caps {
		capUsers[key.UserID] = true
	}
	seenForward := make(map[[2]int64]bool)
	for _, target := range targets {
		if target.UserID <= 0 || target.NodeID <= 0 {
			continue
		}
		limits, known := extras.Users[target.UserID]
		if !known {
			limits = repo.QuotaUserLimits{MaxConnections: max(target.MaxConnections, 0), MaxClientIps: max(target.MaxClientIps, 0)}
		}
		if limits.MaxConnections <= 0 && limits.MaxClientIps <= 0 && !capUsers[target.UserID] {
			continue
		}
		pair := [2]int64{target.UserID, target.NodeID}
		if seenForward[pair] {
			continue
		}
		seenForward[pair] = true
		u := user(target.UserID, limits)
		u.slots = append(u.slots, &quotaSlot{
			kind:        quotaSlotForward,
			nodeID:      target.NodeID,
			key:         repo.ServerQuotaKey{UserID: target.UserID, Kind: "node", RefID: target.NodeID},
			pushable:    target.NodeStatus == 1 && quotaAgentSupport(target.NodeVersion) != quotaAgentUnsupported,
			nodeVersion: target.NodeVersion,
			usage:       usageByNode[target.NodeID][quotaUsageKey{UserID: target.UserID}],
		})
	}
	for _, slot := range extras.Landing {
		limits, known := extras.Users[slot.UserID]
		if !known || (limits.MaxConnections <= 0 && limits.MaxClientIps <= 0 && !capUsers[slot.UserID]) {
			continue
		}
		u := user(slot.UserID, limits)
		if slot.Shared {
			u.slots = append(u.slots, &quotaSlot{
				kind:       quotaSlotShared,
				serverID:   slot.ServerID,
				key:        repo.ServerQuotaKey{UserID: slot.UserID, Kind: "landing", RefID: slot.ServerID},
				relayNodes: slot.RelayNodes,
				pushable:   true,
				usage:      usageByShare[slot.ServerID][slot.UserID],
			})
			continue
		}
		u.slots = append(u.slots, &quotaSlot{
			kind:        quotaSlotLanding,
			nodeID:      slot.NodeID,
			serverID:    slot.ServerID,
			key:         repo.ServerQuotaKey{UserID: slot.UserID, Kind: "node", RefID: slot.NodeID},
			relayNodes:  slot.RelayNodes,
			pushable:    slot.NodeStatus == 1,
			nodeVersion: slot.NodeVersion,
			usage:       usageByNode[slot.NodeID][quotaUsageKey{UserID: slot.UserID, Landing: true}],
		})
	}

	for userID, u := range users {
		total := 0
		allIps := make(map[string]struct{})
		serverTotals := make(map[repo.ServerQuotaKey]int)
		for _, s := range u.slots {
			total += s.usage.Connections
			for ip := range s.usage.ClientIps {
				allIps[ip] = struct{}{}
			}
			serverTotals[s.key] += s.serverUsage()
		}
		plan.LiveUsage[userID] = quotaLiveUsage{Connections: total, ClientIps: len(allIps)}

		for _, s := range u.slots {
			if !s.pushable {
				continue
			}
			serverCap := extras.Caps[s.key]
			if u.limits.MaxConnections <= 0 && u.limits.MaxClientIps <= 0 && serverCap <= 0 {
				// Nothing limits this slot (the user's caps sit on other servers):
				// its services never join the group, so no budget is pushed.
				continue
			}
			totalBudget := -1
			if u.limits.MaxConnections > 0 {
				totalBudget = max(u.limits.MaxConnections-(total-s.usage.Connections), 0)
			}
			serverBudget := -1
			if serverCap > 0 {
				serverBudget = max(serverCap-(serverTotals[s.key]-s.serverUsage()), 0)
			}
			clientIPBudget := -1
			var sharedIPs []string
			if u.limits.MaxClientIps > 0 {
				otherOnlyIps := make(map[string]struct{})
				for _, other := range u.slots {
					if other == s {
						continue
					}
					for ip := range other.usage.ClientIps {
						if _, local := s.usage.ClientIps[ip]; !local {
							otherOnlyIps[ip] = struct{}{}
						}
					}
				}
				clientIPBudget = max(u.limits.MaxClientIps-len(otherOnlyIps), 0)
				if s.kind == quotaSlotForward && len(otherOnlyIps) > 0 {
					sharedIPs = make([]string, 0, len(otherOnlyIps))
					for ip := range otherOnlyIps {
						sharedIPs = append(sharedIPs, ip)
					}
					sort.Strings(sharedIPs)
					if len(sharedIPs) > maxQuotaSharedIPs {
						sharedIPs = sharedIPs[:maxQuotaSharedIPs]
					}
				}
			}
			switch s.kind {
			case quotaSlotForward:
				plan.NodeVersions[s.nodeID] = s.nodeVersion
				noteUsage(s.nodeID, quotaGroupForUser(userID), s)
				plan.Budgets[s.nodeID] = append(plan.Budgets[s.nodeID], quotaGroupBudget{
					Group:          quotaGroupForUser(userID),
					MaxConnections: minQuotaBudget(totalBudget, serverBudget),
					MaxClientIps:   clientIPBudget,
					SharedIps:      sharedIPs,
				})
			case quotaSlotLanding:
				plan.NodeVersions[s.nodeID] = s.nodeVersion
				noteUsage(s.nodeID, landingQuotaGroupForUser(userID), s)
				b := quotaGroupBudget{Group: landingQuotaGroupForUser(userID), MaxConnections: totalBudget, MaxClientIps: clientIPBudget, RelayIps: s.relayNodes}
				if serverBudget >= 0 {
					v := serverBudget
					b.ServerMaxConnections = &v
				}
				plan.Budgets[s.nodeID] = append(plan.Budgets[s.nodeID], b)
			case quotaSlotShared:
				b := quotaShareBudget{UserID: userID, MaxConnections: totalBudget, MaxClientIps: clientIPBudget, RelayIps: s.relayNodes}
				if serverBudget >= 0 {
					v := serverBudget
					b.ServerMaxConnections = &v
				}
				plan.Shares[s.serverID] = append(plan.Shares[s.serverID], b)
			}
		}
	}
	// Every capable share gets an entry (possibly empty) so its exchange runs.
	for _, slot := range extras.Landing {
		if slot.Shared {
			if _, ok := plan.Shares[slot.ServerID]; !ok {
				plan.Shares[slot.ServerID] = []quotaShareBudget{}
			}
		}
	}

	for nodeID := range plan.Budgets {
		sort.Slice(plan.Budgets[nodeID], func(i, j int) bool {
			return plan.Budgets[nodeID][i].Group < plan.Budgets[nodeID][j].Group
		})
	}
	for serverID := range plan.Shares {
		sort.Slice(plan.Shares[serverID], func(i, j int) bool {
			return plan.Shares[serverID][i].UserID < plan.Shares[serverID][j].UserID
		})
	}
	return plan
}

// minQuotaBudget combines two budgets where -1 = unlimited.
func minQuotaBudget(a, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

type quotaAgentSupportLevel int

const (
	quotaAgentUnknown quotaAgentSupportLevel = iota
	quotaAgentUnsupported
	quotaAgentSupported
)

func quotaAgentSupport(version string) quotaAgentSupportLevel {
	fields := strings.Fields(strings.TrimSpace(version))
	if len(fields) == 0 {
		return quotaAgentUnknown
	}
	version = strings.TrimPrefix(fields[0], "v")
	if version == "" || !stableVersionPattern.MatchString(version) {
		return quotaAgentUnknown
	}
	_, fork, revision := splitForkVersion(version)
	if fork && revision >= 13 {
		return quotaAgentSupported
	}
	return quotaAgentUnsupported
}
