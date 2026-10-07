package handler

import (
	"context"
	"errors"
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

const (
	tunnelQualityMaxProbes     = 12
	tunnelQualityMaxNodeProbes = 3
)

// A round plans all unique work before executing it. Only actual ping commands
// consume slots, so a shared public fallback never holds a slot while waiting.
type tunnelQualityProbeRound struct {
	results       sync.Map
	publicResults sync.Map
	execute       tunnelQualityPingFunc
	planned       map[tunnelQualityPingKey]diagnosisExecOptions
	targetNodes   map[tunnelQualityPingKey][]int64
	connected     func(int64) bool
	publicTests   map[int64]diagnosisExecOptions
	ctx           context.Context
	global        chan struct{}
	sources       sync.Map
}

func newTunnelQualityProbeRound(ping tunnelQualityPingFunc) *tunnelQualityProbeRound {
	return &tunnelQualityProbeRound{
		execute:     ping,
		planned:     make(map[tunnelQualityPingKey]diagnosisExecOptions),
		targetNodes: make(map[tunnelQualityPingKey][]int64),
		publicTests: make(map[int64]diagnosisExecOptions),
		ctx:         context.Background(),
		global:      make(chan struct{}, tunnelQualityMaxProbes),
	}
}

func (r *tunnelQualityProbeRound) planPing(key tunnelQualityPingKey, options diagnosisExecOptions) {
	r.planned[key] = options
}

// Local destinations are checked again when a queued command is dispatched.
func (r *tunnelQualityProbeRound) planNodePing(key tunnelQualityPingKey, localTargetID int64, options diagnosisExecOptions) {
	r.planPing(key, options)
	if localTargetID == 0 {
		return
	}
	for _, id := range r.targetNodes[key] {
		if id == localTargetID {
			return
		}
	}
	r.targetNodes[key] = append(r.targetNodes[key], localTargetID)
}

func (r *tunnelQualityProbeRound) planExitTest(nodeID int64, options diagnosisExecOptions) {
	r.publicTests[nodeID] = options
}

func (r *tunnelQualityProbeRound) run(ctx context.Context) {
	r.ctx = ctx
	var wg sync.WaitGroup
	for key, options := range r.planned {
		wg.Add(1)
		go func(key tunnelQualityPingKey, options diagnosisExecOptions) {
			defer wg.Done()
			_, _, _ = r.ping(key.nodeID, key.ip, key.port, options)
		}(key, options)
	}
	for nodeID, options := range r.publicTests {
		wg.Add(1)
		go func(nodeID int64, options diagnosisExecOptions) {
			defer wg.Done()
			_, _, _, _ = r.tcpPingExitTest(nodeID, options)
		}(nodeID, options)
	}
	wg.Wait()
}

func (r *tunnelQualityProbeRound) ping(nodeID int64, ip string, port int, options diagnosisExecOptions) (float64, float64, error) {
	result := &tunnelQualityPingResult{done: make(chan struct{})}
	value, loaded := r.results.LoadOrStore(tunnelQualityPingKey{nodeID, ip, port}, result)
	if loaded {
		result = value.(*tunnelQualityPingResult)
		select {
		case <-result.done:
		case <-r.ctx.Done():
			return 0, 100, r.ctx.Err()
		}
	} else {
		result.latency, result.loss, result.err = r.sample(nodeID, ip, port, options)
		close(result.done)
	}
	return result.latency, result.loss, result.err
}

func (r *tunnelQualityProbeRound) sample(nodeID int64, ip string, port int, options diagnosisExecOptions) (float64, float64, error) {
	value, _ := r.sources.LoadOrStore(nodeID, make(chan struct{}, tunnelQualityMaxNodeProbes))
	source := value.(chan struct{})
	// Acquire the source slot first: a busy agent must not reserve global slots.
	select {
	case source <- struct{}{}:
		defer func() { <-source }()
	case <-r.ctx.Done():
		return 0, 100, r.ctx.Err()
	}
	select {
	case r.global <- struct{}{}:
		defer func() { <-r.global }()
	case <-r.ctx.Done():
		return 0, 100, r.ctx.Err()
	}
	if err := r.ctx.Err(); err != nil {
		return 0, 100, err
	}
	if r.connected != nil {
		for _, targetID := range r.targetNodes[tunnelQualityPingKey{nodeID, ip, port}] {
			if !r.connected(targetID) {
				return 0, 100, errors.New("节点不在线")
			}
		}
	}
	return r.execute(nodeID, ip, port, options)
}

// Assembly only reads completed work; it never starts another probe.
func (r *tunnelQualityProbeRound) result(nodeID int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
	value, ok := r.results.Load(tunnelQualityPingKey{nodeID, ip, port})
	if !ok {
		return 0, 100, errors.New("探测结果不存在")
	}
	result := value.(*tunnelQualityPingResult)
	<-result.done
	return result.latency, result.loss, result.err
}

type tunnelPathSegment struct {
	from, to      int
	key           tunnelQualityPingKey
	localTargetID int64
}

type tunnelPathPlan struct {
	widths   []int
	segments [][]tunnelPathSegment
}

// Resolve every online pair between adjacent layers once, retaining the actual
// endpoints so minimum segments cannot be joined through different hop nodes.
func planTunnelPath(rows []chainNodeRecord, ipPreference string, lookup func(int64) (*nodeRecord, error)) tunnelPathPlan {
	entries, hops, exits := splitChainNodeGroups(rows)
	groups := append([][]chainNodeRecord{entries}, hops...)
	groups = append(groups, exits)
	path := tunnelPathPlan{widths: make([]int, len(groups)), segments: make([][]tunnelPathSegment, len(groups)-1)}
	nodes := make([][]*nodeRecord, len(groups))
	for i, group := range groups {
		online := make([]chainNodeRecord, 0, len(group))
		for _, candidate := range group {
			node, err := lookup(candidate.NodeID)
			if err == nil && node != nil && node.Status == 1 {
				online = append(online, candidate)
				nodes[i] = append(nodes[i], node)
			}
		}
		groups[i] = online
		path.widths[i] = len(online)
	}
	for i := 1; i < len(groups); i++ {
		for from, source := range groups[i-1] {
			for to, target := range groups[i] {
				ip, port, err := resolveChainProbeTarget(nodes[i-1][from], nodes[i][to], target.Port, ipPreference, target.ConnectIPType, target.ConnectIP)
				if err == nil {
					localTargetID := target.NodeID
					if nodes[i][to].IsRemote == 1 {
						localTargetID = 0
					}
					path.segments[i-1] = append(path.segments[i-1], tunnelPathSegment{from, to, tunnelQualityPingKey{source.NodeID, ip, port}, localTargetID})
				}
			}
		}
	}
	return path
}

func (path tunnelPathPlan) probe(ping tunnelQualityPingFunc, options diagnosisExecOptions) (float64, float64, string) {
	type bestPath struct {
		latency, loss float64
		ok            bool
	}
	best := make([]bestPath, path.widths[0])
	for i := range best {
		best[i].ok = true
	}
	for i, segments := range path.segments {
		next := make([]bestPath, path.widths[i+1])
		for _, segment := range segments {
			latency, loss, err := ping(segment.key.nodeID, segment.key.ip, segment.key.port, options)
			previous := best[segment.from]
			if !previous.ok || err != nil || loss >= 100 || latency < 0 {
				continue
			}
			latency += previous.latency
			if !next[segment.to].ok || latency < next[segment.to].latency {
				next[segment.to] = bestPath{latency, combineLossPercent(previous.loss, loss), true}
			}
		}
		best = next
	}
	var chosen bestPath
	for _, candidate := range best {
		if candidate.ok && (!chosen.ok || candidate.latency < chosen.latency) {
			chosen = candidate
		}
	}
	if !chosen.ok {
		return 0, 100, "timeout"
	}
	return chosen.latency, chosen.loss, "ok"
}

func probeTunnelPath(rows []chainNodeRecord, ipPreference string, lookup func(int64) (*nodeRecord, error), ping tunnelQualityPingFunc, options diagnosisExecOptions) (float64, float64, string) {
	return planTunnelPath(rows, ipPreference, lookup).probe(ping, options)
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
