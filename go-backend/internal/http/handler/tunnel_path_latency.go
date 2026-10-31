package handler

import (
	"context"
	"log"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go-backend/internal/http/client"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

const (
	pathLatencyInterval    = 60 * time.Second
	pathLatencyMaxAge      = 150 * time.Second
	pathLatencyRetryDelay  = 10 * time.Second
	pathLatencyConcurrency = 6
)

type pathProbeKey struct {
	PipeTunnelID int64
	From         int64
	Host         string
	Port         int
}

type pathProbeResult struct {
	LatencyMS float64
	OK        bool
}

type pathLatencyEdge struct {
	From, To int64
	Key      pathProbeKey
}

type pathLatencyPlan struct {
	TunnelID int64
	Entries  []chainNodeRecord
	Layers   [][]pathLatencyEdge
	// Plan 165 (最优): all groups [entries, hops…, exits] in DB order, whether
	// the tunnel runs (status 1), has a LeiKwan pipe entry, and its remote
	// (federation) nodes.
	Groups [][]chainNodeRecord
	Active bool
	Pipe   bool
	Remote map[int64]bool
	// Plan 180 「规则最优」: the tunnel's switch, and (filled by
	// attachRuleBestProbes) its destinations with their exit probes.
	RuleBestOn bool
	RuleBest   *ruleBestPlan
}

type pathEntryLatency struct {
	NodeID    int64  `json:"-"`
	EntryName string `json:"entryName,omitempty"`
	// EntryKey is the viewer's picker key of the entry (admin node-<id>, user
	// hashed label key): one value per picker option (plan 152 part C).
	EntryKey  string  `json:"entryKey,omitempty"`
	LatencyMS float64 `json:"latencyMs"`
	Status    string  `json:"status"`
}

type tunnelPathLatency struct {
	TunnelID  int64              `json:"tunnelId"`
	Entries   []pathEntryLatency `json:"entries"`
	UpdatedAt int64              `json:"updatedAt"`
}

func (h *Handler) runPathLatencyLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	ticker := time.NewTicker(pathLatencyInterval)
	defer ticker.Stop()
	for {
		h.runPathLatencyRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) planPathLatencyRound() ([]pathLatencyPlan, map[pathProbeKey]*nodeRecord, int, error) {
	tunnels, err := h.repo.ListTunnels()
	if err != nil {
		return nil, nil, 0, err
	}
	nodes := make(map[int64]*nodeRecord)
	load := func(id int64) *nodeRecord {
		if n, ok := nodes[id]; ok {
			return n
		}
		n, _ := h.getNodeRecord(id)
		nodes[id] = n
		return n
	}
	plans := make([]pathLatencyPlan, 0, len(tunnels))
	pairs := make(map[pathProbeKey]*nodeRecord)
	edgeCount := 0
	for _, tunnel := range tunnels {
		if asInt(tunnel["type"], 0) != 2 {
			continue
		}
		id := asInt64(tunnel["id"], 0)
		rows, err := h.listChainNodesForTunnel(id)
		if err != nil {
			return nil, nil, 0, err
		}
		pipe, hasPipe := tunnel["leikwanPipe"].(model.LeiKwanPipe)
		entries, hops, exits := splitChainNodeGroups(rows)
		plan := pathLatencyPlan{TunnelID: id, Entries: entries, Active: asInt(tunnel["status"], 0) == 1, Pipe: hasPipe, RuleBestOn: asInt(tunnel["ruleBest"], 1) == 1}
		groups := append([][]chainNodeRecord{entries}, hops...)
		groups = append(groups, exits)
		plan.Groups = groups
		for _, group := range groups {
			for _, row := range group {
				if n := load(row.NodeID); n != nil && n.IsRemote == 1 {
					if plan.Remote == nil {
						plan.Remote = make(map[int64]bool)
					}
					plan.Remote[row.NodeID] = true
				}
			}
		}
		for i := 1; i < len(groups); i++ {
			edges := make([]pathLatencyEdge, 0)
			for _, from := range groups[i-1] {
				for _, to := range groups[i] {
					edgeCount++
					fromNode, toNode := load(from.NodeID), load(to.NodeID)
					if fromNode == nil || toNode == nil || fromNode.Status != 1 || toNode.Status != 1 {
						continue
					}
					host, port, err := resolveChainProbeTarget(fromNode, toNode, to.Port, asString(tunnel["ipPreference"]), to.ConnectIPType, to.ConnectIP)
					if err != nil {
						continue
					}
					key := pathProbeKey{From: from.NodeID, Host: host, Port: port}
					if hasPipe && i == 1 {
						if len(groups[i]) != 1 || to.NodeID != pipe.LandingNodeID || to.Port != pipe.LandingPort {
							continue
						}
						key.Host, key.Port, key.PipeTunnelID = "127.0.0.1", pipe.ListenPort, id
					}
					edges = append(edges, pathLatencyEdge{From: from.NodeID, To: to.NodeID, Key: key})
					pairs[key] = fromNode
				}
			}
			plan.Layers = append(plan.Layers, edges)
		}
		plans = append(plans, plan)
	}
	return plans, pairs, edgeCount, nil
}

func (h *Handler) probePathPair(key pathProbeKey, from *nodeRecord) pathProbeResult {
	options := diagnosisExecOptions{pingCount: 2, pingTimeoutMS: 1000, commandTimeout: 4 * time.Second}
	var data map[string]interface{}
	var err error
	if key.PipeTunnelID > 0 {
		data, err = h.probeLeiKwanPipe(key.From, key.PipeTunnelID, key.Port, 3*time.Second)
	} else if from.IsRemote == 1 {
		// Same federation target and authentication as diagnose, with this job's
		// small probe count. Diagnose's own settings remain unchanged.
		fc := client.NewFederationClientWithTimeout(options.commandTimeout)
		data, err = fc.Diagnose(from.RemoteURL, from.RemoteToken, h.federationLocalDomain(), client.RuntimeDiagnoseRequest{IP: key.Host, Port: key.Port, Count: 2, Timeout: options.pingTimeoutMS})
	} else {
		data, err = h.tcpPingViaNode(key.From, key.Host, key.Port, options)
	}
	latency := asFloat(data["averageTime"], -1)
	return pathProbeResult{LatencyMS: latency, OK: err == nil && asBool(data["success"], false) && latency >= 0 && !math.IsNaN(latency) && !math.IsInf(latency, 0)}
}

func waitPathRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// A failed pair releases its worker slot during its single retry delay. Other
// pairs proceed immediately, and successes are never repeated in this round.
func probePathPairs(ctx context.Context, pairs map[pathProbeKey]*nodeRecord, probe func(pathProbeKey, *nodeRecord) pathProbeResult, wait func(context.Context, time.Duration) bool) (map[pathProbeKey]pathProbeResult, int) {
	results := make(map[pathProbeKey]pathProbeResult, len(pairs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, pathLatencyConcurrency)
	retries := 0
	for key, node := range pairs {
		wg.Add(1)
		go func(key pathProbeKey, node *nodeRecord) {
			defer wg.Done()
			ping := func() pathProbeResult {
				select {
				case <-ctx.Done():
					return pathProbeResult{}
				case slots <- struct{}{}:
				}
				defer func() { <-slots }()
				if ctx.Err() != nil {
					return pathProbeResult{}
				}
				return probe(key, node)
			}
			result := ping()
			if !result.OK && ctx.Err() == nil && wait(ctx, pathLatencyRetryDelay) {
				mu.Lock()
				retries++
				mu.Unlock()
				result = ping()
			}
			mu.Lock()
			results[key] = result
			mu.Unlock()
		}(key, node)
	}
	wg.Wait()
	return results, retries
}

func calculatePathLatency(plan pathLatencyPlan, results map[pathProbeKey]pathProbeResult, now time.Time) tunnelPathLatency {
	snapshot := tunnelPathLatency{TunnelID: plan.TunnelID, UpdatedAt: now.UnixMilli(), Entries: make([]pathEntryLatency, 0, len(plan.Entries))}
	for _, entry := range plan.Entries {
		distances := map[int64]float64{entry.NodeID: 0}
		for _, edges := range plan.Layers {
			next := make(map[int64]float64)
			for _, edge := range edges {
				prior, reachable := distances[edge.From]
				result := results[edge.Key]
				if !reachable || !result.OK {
					continue
				}
				cost := prior + result.LatencyMS
				if previous, exists := next[edge.To]; !exists || cost < previous {
					next[edge.To] = cost
				}
			}
			distances = next
		}
		value := pathEntryLatency{NodeID: entry.NodeID, EntryName: entry.NodeName, Status: "timeout"}
		best := math.Inf(1)
		if len(plan.Layers) > 0 {
			for _, cost := range distances {
				best = math.Min(best, cost)
			}
		}
		if !math.IsInf(best, 1) {
			value.LatencyMS, value.Status = best, "ok"
		}
		snapshot.Entries = append(snapshot.Entries, value)
	}
	return snapshot
}

func (h *Handler) runPathLatencyRound(ctx context.Context) {
	start := time.Now()
	plans, pairs, edges, err := h.planPathLatencyRound()
	if err != nil {
		log.Printf("[path-latency] plan failed: %v", err)
		return
	}
	// Plan 180: exit→destination probes of the 「规则最优」 tunnels, deduped
	// with each other and the edges, in the same round.
	ruleBestProbes := h.attachRuleBestProbes(plans, pairs, start)
	results, retries := probePathPairs(ctx, pairs, h.probePathPair, waitPathRetry)
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	snapshots := make(map[int64]tunnelPathLatency, len(plans))
	for _, plan := range plans {
		snapshots[plan.TunnelID] = calculatePathLatency(plan, results, now)
	}
	h.pathLatencyMu.Lock()
	h.pathLatency = snapshots
	h.pathLatencyMu.Unlock()
	// Plan 165: 最优 decisions from this round's edges (no probes of its own).
	bestStart := time.Now()
	guard := newBestPathPushGuard(time.Now)
	h.runBestPathRound(plans, results, guard, now)
	// Plan 180: rule-level decisions after the tunnel ones, same storm guard.
	h.runRuleBestRound(plans, results, guard, now)
	h.ruleBest.count(0, 0, ruleBestProbes)
	bestDuration := time.Since(bestStart)
	// One summary line per hour (was one per minute, ~1.4k lines/day).
	if last := pathLatencyLoggedAt.Load(); now.Unix()-last >= 3600 {
		pathLatencyLoggedAt.Store(now.Unix())
		owners, switches, deferred, failures := h.bestPath.takeCounters()
		rbStates, rbDests, rbDead, rbSwitches, rbDeferred, rbFailures, rbProbes, rbDeadRounds := h.ruleBest.takeCounters()
		log.Printf("[path-latency] round tunnels=%d edges=%d pairs=%d retries=%d duration=%s best_owners=%d best_switches=%d best_deferred=%d best_push_fail=%d best_round=%s rule_best_dests=%d rule_best_states=%d rule_best_probes=%d rule_best_dead=%d rule_best_dead_rounds=%d rule_best_switches=%d rule_best_deferred=%d rule_best_push_fail=%d (logged hourly)", len(plans), edges, len(pairs), retries, time.Since(start).Round(time.Millisecond), owners, switches, deferred, failures, bestDuration.Round(time.Microsecond), rbDests, rbStates, rbProbes, rbDead, rbDeadRounds, rbSwitches, rbDeferred, rbFailures)
	}
}

var pathLatencyLoggedAt atomic.Int64

func (h *Handler) userTunnelLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	userID, roleID, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return
	}
	var items []map[string]interface{}
	if roleID == 0 {
		items, err = h.repo.ListTunnels()
	} else {
		items, err = h.repo.ListUserAccessibleTunnels(userID)
	}
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	out := make([]tunnelPathLatency, 0, len(items))
	for _, item := range items {
		id := asInt64(item["id"], 0)
		h.pathLatencyMu.RLock()
		snapshot, exists := h.pathLatency[id]
		h.pathLatencyMu.RUnlock()
		if !exists || time.Since(time.UnixMilli(snapshot.UpdatedAt)) > pathLatencyMaxAge {
			continue
		}
		var allowed []int64
		if roleID == 0 {
			allowed, err = h.tunnelEntryNodeIDs(id)
		} else {
			allowed, err = h.repo.EffectiveTunnelEntryNodeIDs(userID, id)
		}
		if err != nil {
			response.WriteJSON(w, response.Err(-2, err.Error()))
			return
		}
		visible := tunnelPathLatency{TunnelID: id, UpdatedAt: snapshot.UpdatedAt, Entries: make([]pathEntryLatency, 0, len(allowed))}
		for _, nodeID := range allowed {
			for _, entry := range snapshot.Entries {
				if entry.NodeID != nodeID {
					continue
				}
				if roleID != 0 {
					entry.EntryName = ""
				}
				visible.Entries = append(visible.Entries, entry)
				break
			}
		}
		if len(visible.Entries) > 0 && len(visible.Entries) == len(allowed) {
			out = append(out, visible)
		}
	}
	// One batched lookup for every entry shown; never node ids/names to users.
	nodeIDs := make([]int64, 0)
	for _, item := range out {
		for _, entry := range item.Entries {
			nodeIDs = append(nodeIDs, entry.NodeID)
		}
	}
	if keys, keyErr := h.repo.EntryKeysForNodes(uniqueInt64s(nodeIDs), roleID == 0); keyErr == nil {
		for i := range out {
			for j := range out[i].Entries {
				out[i].Entries[j].EntryKey = keys[out[i].Entries[j].NodeID]
			}
		}
	}
	response.WriteJSON(w, response.OK(out))
}
