package handler

import (
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Plan 249: the 「预热」 tag on the rules page (forward list), built from the
// same in-memory window as the admin tunnel page — no extra request, no
// probe, at most one tunnel query and one chain query per warm tunnel per
// list call (nothing at all while no agent reports a pool).
//
//   - 隧道转发 rule: the path it really uses — its own entry nodes (the
//     forward ports: the bound entry of an entry-bound rule (plan 152), else
//     every entry of the tunnel = the tunnel's weighted value);
//   - 端口转发 rule: its own pool (rule service × target, on its entries).
//
// Admins get the full view (same states as the tunnel page: faster / slower /
// unsupported / warming, per-hop detail with node names). Users only get a
// rule that really got faster — status + ms, no hops, node names, targets or
// counts (the tunnel stats also hold other users' streams).

// warmPoolUserView is item["warmPoolState"] for a non-admin viewer.
type warmPoolUserView struct {
	Status    string            `json:"status"` // always faster
	SavedMs   float64           `json:"savedMs"`
	Kind      string            `json:"kind"`
	Hops      []warmPoolHopView `json:"hops"` // always empty
	WindowMin int               `json:"windowMin"`
}

// warmUserMinSavedMs: below 1 ms a user would see 「预热 −0 ms」.
const warmUserMinSavedMs = 1.0

func (h *Handler) attachForwardWarmPoolStatesOrLog(items []map[string]interface{}, entryNodes map[int64][]int64, admin bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[warm-pool] attach rule view failed: %v", recovered)
		}
	}()
	h.attachForwardWarmPoolStates(items, entryNodes, admin)
}

func (h *Handler) attachForwardWarmPoolStates(items []map[string]interface{}, entryNodes map[int64][]int64, admin bool) {
	for _, item := range items {
		delete(item, "warmPoolState")
	}
	m := h.warmPools()
	if m == nil || len(items) == 0 {
		return
	}
	chainKeys := m.snapshot("c")
	ruleKeys := m.snapshot("s")
	if len(chainKeys) == 0 && len(ruleKeys) == 0 {
		return
	}
	// chain keys by tunnel → node; rule keys by rule id.
	chainsByTunnel := map[int64]map[int64][]warmKeyStat{}
	for _, k := range chainKeys {
		tid, ok := warmChainTunnelID(k.key.Owner)
		if !ok {
			continue
		}
		byNode := chainsByTunnel[tid]
		if byNode == nil {
			byNode = map[int64][]warmKeyStat{}
			chainsByTunnel[tid] = byNode
		}
		byNode[k.key.NodeID] = append(byNode[k.key.NodeID], k)
	}
	rulesByID := map[int64][]warmKeyStat{}
	for _, k := range ruleKeys {
		if fid := forwardIDOfService(k.key.Owner); fid > 0 {
			rulesByID[fid] = append(rulesByID[fid], k)
		}
	}

	var tunnels map[int64]struct{ typ, status, warm int }
	tunnelInfo := func(id int64) (typ int, ok bool) {
		if tunnels == nil {
			tunnels = map[int64]struct{ typ, status, warm int }{}
			list, err := h.repo.ListWarmPoolTunnels()
			if err != nil {
				return 0, false
			}
			for _, t := range list {
				tunnels[t.ID] = struct{ typ, status, warm int }{t.Type, t.Status, t.WarmPool}
			}
		}
		t, found := tunnels[id]
		if !found || t.status != 1 || t.warm == 0 {
			return 0, false
		}
		return t.typ, true
	}
	chainRows := map[int64][]chainNodeRecord{}
	rowsOf := func(tid int64) []chainNodeRecord {
		if rows, ok := chainRows[tid]; ok {
			return rows
		}
		rows, err := h.repo.ListChainNodesForTunnel(tid)
		if err != nil {
			rows = nil
		}
		chainRows[tid] = rows
		return rows
	}
	names := map[int64]string{}
	nodeName := func(id int64) string {
		if n, ok := names[id]; ok {
			return n
		}
		n := fmt.Sprintf("节点 %d", id)
		if node, err := h.getNodeRecord(id); err == nil && node != nil {
			n = nodeDisplayName(node)
		}
		names[id] = n
		return n
	}

	for _, item := range items {
		fid := asInt64(item["id"], 0)
		tid := asInt64(item["tunnelId"], 0)
		if fid <= 0 || tid <= 0 || asInt(item["status"], 0) != 1 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(asString(item["mode"]))) {
		case "", "gost":
		default:
			continue // nftables / WG rules never use a pool
		}
		byNode := chainsByTunnel[tid]
		ruleKs := rulesByID[fid]
		if len(byNode) == 0 && len(ruleKs) == 0 {
			continue
		}
		typ, ok := tunnelInfo(tid)
		if !ok {
			continue
		}
		var entries map[int64]bool
		if ids := entryNodes[fid]; len(ids) > 0 {
			entries = make(map[int64]bool, len(ids))
			for _, id := range ids {
				entries[id] = true
			}
		}
		var view *warmPoolView
		switch typ {
		case 2:
			if len(byNode) == 0 || entries == nil {
				continue
			}
			rows := rowsOf(tid)
			if len(rows) == 0 {
				continue
			}
			view = h.tunnelWarmViewOf(rows, byNode, entries, nodeName)
		case 1:
			view = ruleWarmView(ruleKs, entries, nodeName)
		}
		if view == nil {
			continue
		}
		if admin {
			item["warmPoolState"] = view
			continue
		}
		if view.Status == "faster" && view.SavedMs >= warmUserMinSavedMs {
			item["warmPoolState"] = warmPoolUserView{
				Status:    view.Status,
				SavedMs:   view.SavedMs,
				Kind:      view.Kind,
				Hops:      []warmPoolHopView{},
				WindowMin: view.WindowMin,
			}
		}
	}
}

// ruleWarmView: 端口转发 — one path per (entry node, target) key of the rule.
func ruleWarmView(keys []warmKeyStat, entries map[int64]bool, nodeName func(int64) string) *warmPoolView {
	var paths [][]warmPoolHopView
	var weights []int64
	for i := range keys {
		k := &keys[i]
		if entries != nil && !entries[k.key.NodeID] {
			continue
		}
		hv := hopFromKey(k)
		hv.FromNodeID = k.key.NodeID
		hv.From = nodeName(k.key.NodeID)
		hv.To = k.key.Addr
		paths = append(paths, []warmPoolHopView{hv})
		weights = append(weights, hv.WarmN)
	}
	if len(paths) == 0 {
		return nil
	}
	return summarizeWarmPaths("forward", paths, weights)
}

// warmChainTunnelID parses "chains_<tunnel>".
func warmChainTunnelID(owner string) (int64, bool) {
	rest, ok := strings.CutPrefix(owner, "chains_")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
