package handler

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// Plan 165: admin view of the 最优 decisions — one entry per 最优 layer of a
// tunnel, per owner the current first candidate and its whole-path latency.
// Only the admin tunnel list/get carry it (node names; users never see it).
const (
	bestPathStatusApplied     = "applied"
	bestPathStatusWaiting     = "waiting"
	bestPathStatusUpgrade     = "upgrade" // owner agent without chainSwap: static order
	bestPathStatusRemote      = "remote"  // federation owner: static order
	bestPathStatusOffline     = "offline"
	bestPathStatusUnreachable = "unreachable"
)

var bestPathStatusSummary = map[string]string{
	bestPathStatusWaiting:     "等待探测",
	bestPathStatusUpgrade:     "节点需升级",
	bestPathStatusRemote:      "远程节点按顺序",
	bestPathStatusOffline:     "节点离线",
	bestPathStatusUnreachable: "全部不可达",
}

type bestPathDisplayCandidate struct {
	NodeID    int64   `json:"nodeId"`
	Name      string  `json:"name"`
	CostMs    float64 `json:"costMs,omitempty"`
	Reachable bool    `json:"reachable"`
}

type bestPathDisplayItem struct {
	OwnerNodeID int64                      `json:"ownerNodeId"`
	OwnerName   string                     `json:"ownerName"`
	Status      string                     `json:"status"`
	BestNodeID  int64                      `json:"bestNodeId,omitempty"`
	BestName    string                     `json:"bestName,omitempty"`
	CostMs      float64                    `json:"costMs,omitempty"`
	SwitchedAt  int64                      `json:"switchedAt,omitempty"`
	UpdatedAt   int64                      `json:"updatedAt,omitempty"`
	Candidates  []bestPathDisplayCandidate `json:"candidates,omitempty"`
}

type bestPathDisplayLayer struct {
	Layer   int                   `json:"layer"` // group index: 1 = first group after the entries
	Label   string                `json:"label"` // 出口 / 中转 / 中转N
	Status  string                `json:"status"`
	Summary string                `json:"summary"`
	MinMs   float64               `json:"minMs,omitempty"`
	MaxMs   float64               `json:"maxMs,omitempty"`
	Items   []bestPathDisplayItem `json:"items"`
}

type bestPathDisplayState struct {
	Layers []bestPathDisplayLayer `json:"layers"`
}

type bestPathNodeInfo struct {
	Name    string
	Remote  bool
	Online  bool
	Capable bool
}

func (h *Handler) attachBestPathStates(items []map[string]interface{}) {
	if h == nil || len(items) == 0 {
		return
	}
	cache := map[int64]bestPathNodeInfo{}
	info := func(id int64) bestPathNodeInfo {
		if v, ok := cache[id]; ok {
			return v
		}
		var v bestPathNodeInfo
		if node, err := h.getNodeRecord(id); err == nil && node != nil {
			v.Name = strings.TrimSpace(node.Name)
			v.Remote = node.IsRemote == 1
		}
		v.Online = h.nodeAgentOnline(id)
		v.Capable = v.Online && h.chainSwapCapable(id)
		cache[id] = v
		return v
	}
	now := time.Now()
	for _, item := range items {
		delete(item, "bestExitState")
		if state, ok := buildBestPathDisplayState(item, h.bestPath, info, now); ok {
			item["bestPathState"] = state
		} else {
			delete(item, "bestPathState")
		}
	}
}

func (h *Handler) attachBestPathStatesOrLog(items []map[string]interface{}) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[best-path] attach display state failed: %v", recovered)
		}
	}()
	h.attachBestPathStates(items)
}

func bestPathRowsOf(v interface{}) []map[string]interface{} {
	switch arr := v.(type) {
	case []map[string]interface{}:
		return arr
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func bestPathGroupsOf(v interface{}) [][]map[string]interface{} {
	switch groups := v.(type) {
	case [][]map[string]interface{}:
		return groups
	case []interface{}:
		out := make([][]map[string]interface{}, 0, len(groups))
		for _, group := range groups {
			if items := bestPathRowsOf(group); len(items) > 0 {
				out = append(out, items)
			}
		}
		return out
	default:
		return nil
	}
}

func bestPathRowsAreBest(rows []map[string]interface{}) bool {
	if len(rows) < 2 {
		return false
	}
	for _, row := range rows {
		if !isBestTunnelStrategy(asString(row["strategy"])) {
			return false
		}
	}
	return true
}

func buildBestPathDisplayState(tunnel map[string]interface{}, m *bestPathManager, info func(int64) bestPathNodeInfo, now time.Time) (*bestPathDisplayState, bool) {
	if tunnel == nil || asInt(tunnel["type"], 0) != 2 || asInt(tunnel["status"], 0) != 1 {
		return nil, false
	}
	tunnelID := asInt64(tunnel["id"], 0)
	if tunnelID <= 0 {
		return nil, false
	}
	hops := bestPathGroupsOf(tunnel["chainNodes"])
	groups := append([][]map[string]interface{}{bestPathRowsOf(tunnel["inNodeId"])}, hops...)
	groups = append(groups, bestPathRowsOf(tunnel["outNodeId"]))
	_, pipe := tunnel["leikwanPipeId"]
	state := &bestPathDisplayState{}
	for g := 1; g < len(groups); g++ {
		if !bestPathRowsAreBest(groups[g]) || (pipe && g == 1) {
			continue
		}
		label := "出口"
		if g < len(groups)-1 {
			label = "中转"
			if len(hops) > 1 {
				label = fmt.Sprintf("中转%d", g)
			}
		}
		dbOrder := make([]int64, 0, len(groups[g]))
		for _, row := range groups[g] {
			dbOrder = append(dbOrder, asInt64(row["nodeId"], 0))
		}
		layer := bestPathDisplayLayer{Layer: g, Label: label, Items: make([]bestPathDisplayItem, 0, len(groups[g-1]))}
		for _, ownerRow := range groups[g-1] {
			ownerID := asInt64(ownerRow["nodeId"], 0)
			if ownerID <= 0 {
				continue
			}
			layer.Items = append(layer.Items, bestPathDisplayOwner(tunnelID, ownerID, g, dbOrder, m, info, now))
		}
		if len(layer.Items) == 0 {
			continue
		}
		summarizeBestPathLayer(&layer)
		state.Layers = append(state.Layers, layer)
	}
	return state, len(state.Layers) > 0
}

func bestPathDisplayOwner(tunnelID, ownerID int64, g int, dbOrder []int64, m *bestPathManager, info func(int64) bestPathNodeInfo, now time.Time) bestPathDisplayItem {
	owner := info(ownerID)
	item := bestPathDisplayItem{OwnerNodeID: ownerID, OwnerName: owner.Name, Status: bestPathStatusWaiting}
	if item.OwnerName == "" {
		item.OwnerName = fmt.Sprintf("节点 %d", ownerID)
	}
	switch {
	case owner.Remote:
		item.Status = bestPathStatusRemote
		return item
	case !owner.Online:
		item.Status = bestPathStatusOffline
		return item
	case !owner.Capable:
		item.Status = bestPathStatusUpgrade
		return item
	}
	st, ok := m.snapshot(bestPathKey{TunnelID: tunnelID, OwnerNodeID: ownerID})
	if !ok || st.Layer != g || st.EvaluatedAt <= 0 || now.Sub(time.UnixMilli(st.EvaluatedAt)) > pathLatencyMaxAge || len(st.Ranked) == 0 {
		return item
	}
	item.UpdatedAt, item.SwitchedAt = st.EvaluatedAt, st.SwitchedAt
	byID := make(map[int64]bestPathCandidate, len(st.Ranked))
	for _, c := range st.Ranked {
		byID[c.NodeID] = c
		dc := bestPathDisplayCandidate{NodeID: c.NodeID, Name: info(c.NodeID).Name, Reachable: c.Reachable}
		if dc.Name == "" {
			dc.Name = fmt.Sprintf("节点 %d", c.NodeID)
		}
		if c.Reachable {
			dc.CostMs = roundMS(c.CostMS)
		}
		item.Candidates = append(item.Candidates, dc)
	}
	cur := byID[effectiveOrder(dbOrder, st.Order)[0]]
	item.BestNodeID = cur.NodeID
	item.BestName = info(cur.NodeID).Name
	if item.BestName == "" {
		item.BestName = fmt.Sprintf("节点 %d", cur.NodeID)
	}
	if !cur.Reachable {
		item.Status = bestPathStatusUnreachable
		return item
	}
	item.Status, item.CostMs = bestPathStatusApplied, roundMS(cur.CostMS)
	return item
}

// summarizeBestPathLayer: the applied owners decide the chip (one node name or
// 「N 个节点」, ms range); without any, the most telling other status.
func summarizeBestPathLayer(layer *bestPathDisplayLayer) {
	names := map[int64]string{}
	first := true
	for _, it := range layer.Items {
		if it.Status != bestPathStatusApplied {
			continue
		}
		names[it.BestNodeID] = it.BestName
		if first || it.CostMs < layer.MinMs {
			layer.MinMs = it.CostMs
		}
		if first || it.CostMs > layer.MaxMs {
			layer.MaxMs = it.CostMs
		}
		first = false
	}
	if len(names) > 0 {
		layer.Status = bestPathStatusApplied
		if len(names) == 1 {
			for _, name := range names {
				layer.Summary = name
			}
		} else {
			layer.Summary = fmt.Sprintf("%d 个节点", len(names))
		}
		return
	}
	for _, status := range []string{bestPathStatusUnreachable, bestPathStatusWaiting, bestPathStatusOffline, bestPathStatusUpgrade, bestPathStatusRemote} {
		for _, it := range layer.Items {
			if it.Status == status {
				layer.Status, layer.Summary = status, bestPathStatusSummary[status]
				return
			}
		}
	}
}
