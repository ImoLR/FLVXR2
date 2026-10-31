package handler

import (
	"fmt"
	"log"
	"math"
	"strings"
	"time"
)

// Plan 180 part 1: the rule list shows, under each rule on a tunnel with a
// 最优 layer, the node the 最优 layers currently use (中转 X → 出口 Y · 38 ms).
// Built from memory only (the latest 60 s round and the decisions), no DB
// read, no probe; it rides the rule list's own polling. The viewer only gets
// their own rules (the list is already filtered) and node names — never node
// ids, IPs or other users' data. Source per owner: the rule-level decision
// when 「规则最优」 is active for it (ms then includes exit→target: 含落地), else
// the tunnel decision (ms entry→exit, as on the admin tunnel card).

type rulePickHop struct {
	Label string `json:"label"`
	Name  string `json:"name"`
}

type rulePickItem struct {
	Entry      string        `json:"entry,omitempty"`
	Target     string        `json:"target,omitempty"`
	Status     string        `json:"status"`
	Summary    string        `json:"summary,omitempty"`
	Hops       []rulePickHop `json:"hops,omitempty"`
	Ms         float64       `json:"ms,omitempty"`
	WithTarget bool          `json:"withTarget,omitempty"`
}

type rulePickLayer struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	Count int    `json:"count"` // distinct nodes over the items (>1 ⇒ Name = 「N 个节点」)
}

type rulePick struct {
	Status     string          `json:"status"`
	Summary    string          `json:"summary"`
	Layers     []rulePickLayer `json:"layers,omitempty"`
	MinMs      float64         `json:"minMs,omitempty"`
	MaxMs      float64         `json:"maxMs,omitempty"`
	WithTarget bool            `json:"withTarget,omitempty"`
	Multi      bool            `json:"multi,omitempty"`
	Items      []rulePickItem  `json:"items,omitempty"`
	UpdatedAt  int64           `json:"updatedAt,omitempty"`
}

// rulePickOwner is what the display needs to know about an owner.
type rulePickOwner struct {
	Online, ChainSwap, DestOrder bool
}

func (h *Handler) attachRuleBestPicksOrLog(items []map[string]interface{}, entryNodes map[int64][]int64) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[rule-best] attach rule picks failed: %v", recovered)
		}
	}()
	h.attachRuleBestPicks(items, entryNodes, time.Now())
}

func (h *Handler) attachRuleBestPicks(items []map[string]interface{}, entryNodes map[int64][]int64, now time.Time) {
	if h == nil || h.ruleBest == nil || len(items) == 0 {
		return
	}
	owners := map[int64]rulePickOwner{}
	owner := func(id int64) rulePickOwner {
		if v, ok := owners[id]; ok {
			return v
		}
		var v rulePickOwner
		v.Online = h.nodeAgentOnline(id)
		v.ChainSwap = v.Online && h.chainSwapCapable(id)
		v.DestOrder = v.ChainSwap && h.destOrderCapable(id)
		owners[id] = v
		return v
	}
	for _, item := range items {
		delete(item, "bestPick")
		forwardID := asInt64(item["id"], 0)
		view := h.ruleBest.view(asInt64(item["tunnelId"], 0))
		if forwardID <= 0 || view == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(asString(item["mode"]))) {
		case "", "gost":
		default:
			continue // nftables / wg_path rules do not use the chain on the entry
		}
		if pick := h.buildRulePick(asInt64(item["tunnelId"], 0), view, entryNodes[forwardID], asString(item["remoteAddr"]), owner, now); pick != nil {
			item["bestPick"] = pick
		}
	}
}

func rulePickLayerLabel(g, last, hopGroups int) string {
	if g == last {
		return "出口"
	}
	if hopGroups > 1 {
		return fmt.Sprintf("中转%d", g)
	}
	return "中转"
}

func (h *Handler) buildRulePick(tunnelID int64, view *ruleBestView, entryIDs []int64, remoteAddr string, owner func(int64) rulePickOwner, now time.Time) *rulePick {
	if len(view.Groups) < 2 {
		return nil
	}
	entryNames := make(map[int64]string, len(view.Groups[0]))
	for _, e := range view.Groups[0] {
		entryNames[e.NodeID] = e.NodeName
	}
	entries := make([]int64, 0, len(entryIDs))
	for _, id := range uniqueInt64s(entryIDs) {
		if _, ok := entryNames[id]; ok {
			entries = append(entries, id)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	// Destinations: with the switch on, each target of the rule; off (or no
	// probe data for it) = the tunnel decision, which does not depend on it.
	dests := []string{""}
	if view.On && len(view.Dests) > 0 {
		keys := make([]string, 0)
		for _, raw := range splitRemoteTargets(remoteAddr) {
			if key := normalizeDestKey(raw); key != "" {
				keys = appendUniqueString(keys, key)
			}
		}
		if len(keys) > 0 {
			dests = keys
		}
	}
	fresh := view.UpdatedAt > 0 && now.Sub(time.UnixMilli(view.UpdatedAt)) <= pathLatencyMaxAge
	pick := &rulePick{UpdatedAt: view.UpdatedAt}
	for _, entryID := range entries {
		for _, dest := range dests {
			item := h.walkRulePick(tunnelID, view, entryID, dest, owner, fresh)
			if len(entries) > 1 {
				item.Entry = entryNames[entryID]
			}
			if len(dests) > 1 {
				if vd, ok := view.Dests[dest]; ok && vd.Label != "" {
					item.Target = vd.Label
				} else {
					item.Target = dest
				}
			}
			pick.Items = append(pick.Items, item)
		}
	}
	summarizeRulePick(pick)
	if !pick.Multi {
		pick.Items = nil
	}
	return pick
}

// walkRulePick follows the rule's path from one entry: at every 最优 layer the
// pick of the current owner, at other layers their first node.
func (h *Handler) walkRulePick(tunnelID int64, view *ruleBestView, entryID int64, dest string, owner func(int64) rulePickOwner, fresh bool) rulePickItem {
	item := rulePickItem{Status: bestPathStatusApplied}
	if !fresh {
		item.Status = bestPathStatusWaiting
		item.Summary = bestPathStatusSummary[item.Status]
		return item
	}
	last := len(view.Groups) - 1
	hopGroups := last - 1
	names := map[int64]string{}
	for _, group := range view.Groups {
		for _, row := range group {
			names[row.NodeID] = row.NodeName
		}
	}
	name := func(id int64) string {
		if n := strings.TrimSpace(names[id]); n != "" {
			return n
		}
		return "节点"
	}
	vd, hasDest := view.Dests[dest]
	if hasDest && (vd.BackedOff || vd.Dead) {
		hasDest = false // tunnel decision meanwhile
	}
	allRule := hasDest
	cur := entryID
	ms := 0.0
	for g := 1; g <= last; g++ {
		layer := view.Groups[g]
		if !isBestLayer(layer) || (view.Pipe && g == 1) {
			next := layer[0].NodeID
			if len(layer) > 1 {
				best := math.Inf(1)
				for _, row := range layer {
					if c, ok := view.Edges[bestPathEdge{cur, row.NodeID}]; ok && c < best {
						best, next = c, row.NodeID
					}
				}
			}
			ms += view.Edges[bestPathEdge{cur, next}]
			cur = next
			continue
		}
		pickID, status, fromRule := h.rulePickAt(tunnelID, view, cur, g, layer, dest, hasDest, owner)
		if status != bestPathStatusApplied {
			item.Status, item.Summary = status, bestPathStatusSummary[status]
			item.Hops = nil
			return item
		}
		allRule = allRule && fromRule
		edge, ok := view.Edges[bestPathEdge{cur, pickID}]
		if !ok {
			item.Status, item.Summary = bestPathStatusUnreachable, bestPathStatusSummary[bestPathStatusUnreachable]
			item.Hops = nil
			return item
		}
		item.Hops = append(item.Hops, rulePickHop{Label: rulePickLayerLabel(g, last, hopGroups), Name: name(pickID)})
		ms += edge
		cur = pickID
	}
	if allRule {
		if c, ok := vd.Exit[cur]; ok {
			ms += c
			item.WithTarget = true
		}
	}
	item.Ms = roundMS(ms)
	return item
}

// rulePickAt is the node an owner's 最优 layer uses now: the rule-level
// decision when active, else the tunnel decision.
func (h *Handler) rulePickAt(tunnelID int64, view *ruleBestView, ownerID int64, g int, layer []chainNodeRecord, dest string, hasDest bool, owner func(int64) rulePickOwner) (int64, string, bool) {
	info := owner(ownerID)
	switch {
	case view.Remote[ownerID]:
		return 0, bestPathStatusRemote, false
	case !info.Online:
		return 0, bestPathStatusOffline, false
	case !info.ChainSwap:
		return 0, bestPathStatusUpgrade, false
	}
	dbOrder := make([]int64, 0, len(layer))
	for _, row := range layer {
		dbOrder = append(dbOrder, row.NodeID)
	}
	tunnelKey := bestPathKey{TunnelID: tunnelID, OwnerNodeID: ownerID}
	reachable := func(ranked []bestPathCandidate, id int64) bool {
		for _, c := range ranked {
			if c.NodeID == id {
				return c.Reachable
			}
		}
		return false
	}
	if hasDest && info.DestOrder {
		if st, ok := h.ruleBest.snapshot(ruleBestKey{TunnelID: tunnelID, OwnerNodeID: ownerID, Dest: dest}); ok && st.Layer == g && st.EvaluatedAt > 0 && len(st.Ranked) > 0 {
			order := st.Order
			if len(order) == 0 {
				order = h.bestPath.order(tunnelKey)
			}
			id := effectiveOrder(dbOrder, order)[0]
			if !reachable(st.Ranked, id) {
				return id, bestPathStatusUnreachable, true
			}
			return id, bestPathStatusApplied, true
		}
	}
	st, ok := h.bestPath.snapshot(tunnelKey)
	if !ok || st.Layer != g || st.EvaluatedAt <= 0 || len(st.Ranked) == 0 {
		return 0, bestPathStatusWaiting, false
	}
	id := effectiveOrder(dbOrder, st.Order)[0]
	if !reachable(st.Ranked, id) {
		return id, bestPathStatusUnreachable, false
	}
	return id, bestPathStatusApplied, false
}

// summarizeRulePick: the applied items decide the line (per 最优 layer one
// name or 「N 个节点」, ms range); without any, the most telling status.
func summarizeRulePick(pick *rulePick) {
	var applied []rulePickItem
	statuses := map[string]bool{}
	for _, it := range pick.Items {
		statuses[it.Status] = true
		if it.Status == bestPathStatusApplied {
			applied = append(applied, it)
		}
	}
	pick.Multi = len(pick.Items) > 1 && len(statuses) > 1
	if len(applied) == 0 {
		for _, status := range []string{bestPathStatusUnreachable, bestPathStatusWaiting, bestPathStatusOffline, bestPathStatusUpgrade, bestPathStatusRemote} {
			if statuses[status] {
				pick.Status, pick.Summary = status, bestPathStatusSummary[status]
				return
			}
		}
		pick.Status, pick.Summary = bestPathStatusWaiting, bestPathStatusSummary[bestPathStatusWaiting]
		return
	}
	pick.Status = bestPathStatusApplied
	pick.WithTarget = true
	first := true
	for _, it := range applied {
		if first || it.Ms < pick.MinMs {
			pick.MinMs = it.Ms
		}
		if first || it.Ms > pick.MaxMs {
			pick.MaxMs = it.Ms
		}
		first = false
		pick.WithTarget = pick.WithTarget && it.WithTarget
	}
	if pick.MaxMs == pick.MinMs {
		pick.MaxMs = 0
	}
	// Layers: the applied items share the same layer labels (same tunnel).
	parts := make([]string, 0, len(applied[0].Hops))
	for i, hop := range applied[0].Hops {
		distinct := map[string]bool{}
		for _, it := range applied {
			if i < len(it.Hops) {
				distinct[it.Hops[i].Name] = true
			}
		}
		layer := rulePickLayer{Label: hop.Label, Name: hop.Name, Count: len(distinct)}
		if len(distinct) > 1 {
			layer.Name = fmt.Sprintf("%d 个节点", len(distinct))
			pick.Multi = true
		}
		pick.Layers = append(pick.Layers, layer)
		parts = append(parts, layer.Label+" "+layer.Name)
	}
	pick.Summary = strings.Join(parts, " → ")
}
