package handler

import (
	"net/http"
	"sync"
	"time"

	"go-backend/internal/http/response"
)

type tunnelQualityPingFunc func(int64, string, int, diagnosisExecOptions) (float64, float64, error)

type tunnelQualityPingKey struct {
	nodeID int64
	ip     string
	port   int
}

type tunnelQualityPingResult struct {
	done    chan struct{}
	latency float64
	loss    float64
	err     error
}

// A round owns its cache, including in-flight requests shared by concurrent workers.
type tunnelQualityProbeRound struct {
	results sync.Map
	execute tunnelQualityPingFunc
}

func newTunnelQualityProbeRound(ping tunnelQualityPingFunc) *tunnelQualityProbeRound {
	return &tunnelQualityProbeRound{execute: ping}
}

func (r *tunnelQualityProbeRound) ping(nodeID int64, ip string, port int, options diagnosisExecOptions) (float64, float64, error) {
	result := &tunnelQualityPingResult{done: make(chan struct{})}
	value, loaded := r.results.LoadOrStore(tunnelQualityPingKey{nodeID, ip, port}, result)
	if loaded {
		result = value.(*tunnelQualityPingResult)
		<-result.done
	} else {
		result.latency, result.loss, result.err = r.execute(nodeID, ip, port, options)
		close(result.done)
	}
	return result.latency, result.loss, result.err
}

// Select one representative per group in chain order, retaining the first node
// as the fallback when the whole group is offline. Public exit tests are separate.
func probeTunnelPath(rows []chainNodeRecord, ipPreference string, lookup func(int64) (*nodeRecord, error), ping tunnelQualityPingFunc, options diagnosisExecOptions) (float64, float64, string) {
	entries, hops, exits := splitChainNodeGroups(rows)
	groups := append([][]chainNodeRecord{entries}, hops...)
	groups = append(groups, exits)
	path := make([]chainNodeRecord, 0, len(groups))
	nodes := make([]*nodeRecord, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			return 0, 100, "timeout"
		}
		chosen := group[0]
		var node *nodeRecord
		for _, candidate := range group {
			current, err := lookup(candidate.NodeID)
			if err == nil && current != nil && current.Status == 1 {
				chosen, node = candidate, current
				break
			}
		}
		if node == nil {
			return 0, 100, "timeout"
		}
		path = append(path, chosen)
		nodes = append(nodes, node)
	}
	var latency, loss float64
	for i := 1; i < len(path); i++ {
		ip, port, err := resolveChainProbeTarget(nodes[i-1], nodes[i], path[i].Port, ipPreference, path[i].ConnectIPType)
		if err != nil {
			return 0, 100, "timeout"
		}
		segmentLatency, segmentLoss, err := ping(path[i-1].NodeID, ip, port, options)
		if err != nil || segmentLoss >= 100 || segmentLatency < 0 {
			return 0, 100, "timeout"
		}
		latency += segmentLatency
		loss = combineLossPercent(loss, segmentLoss)
	}
	return latency, loss, "ok"
}

type userTunnelLatency struct {
	TunnelID  int64   `json:"tunnelId"`
	LatencyMS float64 `json:"latencyMs"`
	Status    string  `json:"status"`
	UpdatedAt int64   `json:"updatedAt"`
}

func (h *Handler) userTunnelLatencyList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	userID, roleID, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return
	}
	ids, err := h.repo.ListUserTunnelLatencyIDs(userID, roleID == 0)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	items := make([]userTunnelLatency, 0, len(ids))
	cutoff := time.Now().Add(-60 * time.Second).UnixMilli()
	if h.qualityProber != nil {
		for _, id := range ids {
			value, ok := h.qualityProber.cache.Load(id)
			if !ok {
				continue
			}
			snap := value.(*tunnelQualitySnapshot)
			if snap.PathUpdatedAt < cutoff || (snap.PathStatus != "ok" && snap.PathStatus != "timeout") {
				continue
			}
			items = append(items, userTunnelLatency{id, snap.PathLatency, snap.PathStatus, snap.PathUpdatedAt})
		}
	}
	response.WriteJSON(w, response.OK(items))
}
