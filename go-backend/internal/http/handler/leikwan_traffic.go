package handler

// Plan 269 利群流量: the entry agents count every 利群 rule port (nft,
// conntrack original destination, both directions). The panel bills the
// growth of those counters to the rule, the owner's 总流量 (with the line's
// mode/ratio), the per-server quota of the entry machine the bytes came in on
// and the user's traffic pools — like a forward whose path is just the entry.
// A rule whose landing is the owner's panel landing node is billed once: the
// landing counters already bill the user (same rule as planLandingForwardBilling).
// Limits / expiry on any layer block the rule's port on every 利群入口 host
// (and pause its automatic 备线); the block lifts on reset / renewal.

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func lkParseCounters(raw string) map[string][2]int64 {
	out := map[string][2]int64{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// lkCounterDeltas: growth per port since the billed baseline. A new epoch
// (table re-created) or a smaller value means the counter started again at 0.
func lkCounterDeltas(prevEpoch string, prev map[string][2]int64, epoch string, cur map[string][2]int64) map[string][2]int64 {
	out := map[string][2]int64{}
	for port, v := range cur {
		p := [2]int64{}
		if prevEpoch == epoch {
			p = prev[port]
		}
		d := [2]int64{v[0] - p[0], v[1] - p[1]}
		for i := range d {
			if d[i] < 0 {
				d[i] = v[i]
			}
		}
		if d[0] > 0 || d[1] > 0 {
			out[port] = d
		}
	}
	return out
}

func (h *Handler) leiKwanBillTraffic(host *model.LeiKwanHost, tr *lkTrafficView) error {
	deltas := lkCounterDeltas(host.CounterEpoch, lkParseCounters(host.Counters), tr.Epoch, tr.Ports)
	rawCur, _ := json.Marshal(tr.Ports)
	if string(rawCur) == host.Counters && tr.Epoch == host.CounterEpoch {
		return nil
	}
	rules, err := h.repo.ListLeiKwanRules(0)
	if err != nil {
		return err
	}
	byPort := map[string]model.LeiKwanRule{}
	for _, x := range rules {
		if x.ListenPort > 0 {
			byPort[strconv.Itoa(x.ListenPort)] = x
		}
	}
	lines := map[int64]*model.LeiKwanLine{}
	batch := newFlowIngestBatch()
	batch.write.LeiKwanRules = map[int64]repo.FlowCounterDelta{}
	ports := make([]string, 0, len(deltas))
	for p := range deltas {
		ports = append(ports, p)
	}
	sort.Strings(ports)
	for _, port := range ports {
		x, ok := byPort[port]
		if !ok {
			continue
		}
		line, ok := lines[x.LineID]
		if !ok {
			line, _ = h.repo.GetLeiKwanLine(x.LineID)
			lines[x.LineID] = line
		}
		d := deltas[port]
		if err := h.planLeiKwanRuleBilling(batch, x, line, host.NodeID, d[0], d[1]); err != nil {
			return err
		}
	}
	batch.write.LeiKwanCounters = []repo.LeiKwanCounterBase{{NodeID: host.NodeID, Epoch: tr.Epoch, Counters: string(rawCur)}}
	quotas, err := h.repo.ApplyFlowBatch(batch.write, time.Now())
	if err != nil {
		return err
	}
	host.CounterEpoch, host.Counters = tr.Epoch, string(rawCur)
	if len(batch.write.LeiKwanRules) > 0 {
		go func() {
			h.flowEnforceMu.Lock()
			h.enforceFlowBatch(batch, quotas)
			h.flowEnforceMu.Unlock()
			h.leiKwanReconcileBlocks()
		}()
	}
	return nil
}

// planLeiKwanRuleBilling mirrors planLandingForwardBilling with the entry
// machine as the only path tier.
func (h *Handler) planLeiKwanRuleBilling(b *flowIngestBatch, x model.LeiKwanRule, line *model.LeiKwanLine, entryID, up, down int64) error {
	mode, ratio := int64(1), 1.0
	if line != nil {
		if line.FlowMode > 0 {
			mode = int64(line.FlowMode)
		}
		if line.TrafficRatio > 0 {
			ratio = line.TrafficRatio
		}
	}
	in, out := billTunnelFlow(mode, ratio, up, down)
	addFlowDelta(b.write.LeiKwanRules, x.ID, in, out)
	userIn, userOut, pathIn, pathOut := in, out, in, out
	var landingServerRef int64
	pool := repo.PoolFlow{UserID: x.UserID}
	if x.LandingNodeID > 0 {
		n, err := h.repo.GetLandingNode(x.LandingNodeID)
		if err != nil {
			return err
		}
		if n != nil && n.UserID == x.UserID {
			s, err := h.repo.GetLandingServer(n.LandingServerID)
			if err != nil {
				return err
			}
			if s != nil {
				lm, lr := repo.LandingRate(n, s)
				landingIn, landingOut := billTunnelFlow(lm, lr, up, down)
				userIn = max(int64(0), in-landingIn)
				userOut = max(int64(0), out-landingOut)
				pathIn, pathOut = max(in, landingIn), max(out, landingOut)
				if s.Source == "native" {
					landingServerRef = s.NodeID
				}
				key := repo.ServerQuotaKey{UserID: x.UserID, Kind: "node", RefID: s.NodeID}
				if s.Source != "native" {
					key.Kind, key.RefID = "landing", s.ID
				}
				d := b.write.ServerQuotas[key]
				d.In += userIn
				d.Out += userOut
				b.write.ServerQuotas[key] = d
				pool.Landing, pool.LandingIn, pool.LandingOut = key, userIn, userOut
			}
		}
	}
	addFlowDelta(b.write.Users, x.UserID, userIn, userOut)
	if x.UserID > 0 {
		b.write.QuotaUsage[x.UserID] += userIn + userOut
		if _, ok := b.seenQuotaUsers[x.UserID]; !ok {
			b.seenQuotaUsers[x.UserID] = struct{}{}
			b.quotaUsers = append(b.quotaUsers, x.UserID)
		}
		check := flowPolicyCheck{userID: x.UserID}
		if _, ok := b.seenPolicies[check]; !ok {
			b.seenPolicies[check] = struct{}{}
			b.policyChecks = append(b.policyChecks, check)
		}
	}
	tiers := [][]int64{{entryID}}
	usage := repo.ServerUsageFromTiers(tiers, pathIn, pathOut)
	pool.Tiers, pool.In, pool.Out = tiers, pathIn, pathOut
	b.write.PoolFlows = append(b.write.PoolFlows, pool)
	for nodeID, share := range usage {
		if nodeID == landingServerRef {
			continue
		}
		key := repo.ServerQuotaKey{UserID: x.UserID, Kind: "node", RefID: nodeID}
		d := b.write.ServerQuotas[key]
		d.In += share.In
		d.Out += share.Out
		b.write.ServerQuotas[key] = d
	}
	return nil
}

// ---- block / unblock ----

var lkBlockMu sync.Mutex

// leiKwanBlockReason: '' = the rule may run.
func leiKwanBlockReason(u model.User, ok bool, pausedNodes []int64, names map[int64]string, now int64) string {
	switch {
	case !ok:
		return ""
	case u.Status != 1:
		return "账号已禁用"
	case u.ExpTime > 0 && u.ExpTime <= now:
		return "账号已到期"
	case u.Flow*bytesPerGB < u.InFlow+u.OutFlow:
		return "总流量已用完"
	case len(pausedNodes) > 0:
		list := []string{}
		for _, id := range pausedNodes {
			n := names[id]
			if n == "" {
				n = fmt.Sprintf("节点 %d", id)
			}
			list = append(list, n)
		}
		sort.Strings(list)
		return "单服务器流量已用完（" + strings.Join(list, "、") + "）"
	}
	return ""
}

// leiKwanReconcileBlocks recomputes every 利群 rule's block state; on a
// change it stores it, pauses / resumes the rule's automatic 备线 and pushes
// the new block list to every 利群入口 host.
func (h *Handler) leiKwanReconcileBlocks() {
	if h == nil || h.repo == nil {
		return
	}
	lkBlockMu.Lock()
	defer lkBlockMu.Unlock()
	rules, err := h.repo.ListLeiKwanRules(0)
	if err != nil || len(rules) == 0 {
		return
	}
	userIDs := []int64{}
	seen := map[int64]bool{}
	for _, x := range rules {
		if !seen[x.UserID] {
			seen[x.UserID] = true
			userIDs = append(userIDs, x.UserID)
		}
	}
	users, err := h.repo.LeiKwanUsersState(userIDs)
	if err != nil {
		return
	}
	entries := h.leiKwanEntryNodeIDs()
	paused, err := h.repo.LeiKwanPausedServerQuotas(userIDs, entries, time.Now())
	if err != nil {
		return
	}
	names := h.leiKwanNodeNames(entries)
	now := time.Now().UnixMilli()
	changed := false
	for _, x := range rules {
		u, ok := users[x.UserID]
		reason := leiKwanBlockReason(u, ok, paused[x.UserID], names, now)
		if reason == x.BlockReason {
			continue
		}
		if err := h.repo.SetLeiKwanRuleBlock(x.ID, reason); err != nil {
			continue
		}
		changed = true
		if reason != "" {
			log.Printf("利群规则 %s（转发 %d）屏蔽：%s", x.Name, x.ForwardID, reason)
		} else {
			log.Printf("利群规则 %s（转发 %d）解除屏蔽", x.Name, x.ForwardID)
		}
		if x.BackupMode == "auto" && x.BackupForwardID > 0 {
			admin := h.repo.FirstAdminUserID()
			fn := h.forwardResume
			if reason != "" {
				fn = h.forwardPause
			}
			if code, msg, _ := h.leikwanAdminCall(fn, admin, map[string]interface{}{"id": x.BackupForwardID}); code != 0 {
				log.Printf("利群规则 %s 的自动备线 %d 暂停/恢复失败: %s", x.Name, x.BackupForwardID, msg)
			}
		}
	}
	if changed {
		for _, id := range entries {
			if h.wsServer != nil && h.wsServer.NodeOnline(id) {
				go func(id int64) {
					if err := h.leiKwanHostPush(id); err != nil {
						log.Printf("node %d: 利群屏蔽同步失败: %v", id, err)
					}
				}(id)
			}
		}
	}
}
