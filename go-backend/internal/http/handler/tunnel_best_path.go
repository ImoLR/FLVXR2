package handler

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plan 165: 「最优」 (strategy `best`) on a tunnel layer with ≥ 2 nodes. For each
// node of the layer before it (the owner) the candidates are ranked by the
// whole path owner → candidate → … → exits, from the per-edge results of the
// existing 60 s path-latency round (no probes of its own). The owner's chain
// is a fifo hop in that order (best first, the rest as backups with the
// usual maxFails=1/failTimeout=600 s failover).
//
// The first place changes only when a challenger is ≥ bestPathSwitchMarginMS
// faster in bestPathConfirmRounds consecutive rounds, or at once when the
// current first is unreachable. Only a first-place change is pushed, with
// UpdateChains, to that owner only, and only to agents that swap chains
// without a gap (handshake chainSwap). Decisions live in memory: a panel
// restart starts from the DB order again; nothing is written per round.
const (
	tunnelStrategyBest      = "best"
	bestRuntimeStrategy     = "fifo"
	bestPathSwitchMarginMS  = 5.0
	bestPathConfirmRounds   = 2
	bestPathMaxPushRound    = 40
	bestPathMaxPushNode     = 10
	bestPathPushTimeout     = 5 * time.Second
	bestPathRoundPushBudget = 20 * time.Second
)

var (
	errBestPathBusy  = errors.New("tunnel runtime busy")
	errBestPathStale = errors.New("tunnel changed since the round")
)

func isBestTunnelStrategy(strategy string) bool {
	return strings.EqualFold(strings.TrimSpace(strategy), tunnelStrategyBest)
}

// runtimeTunnelStrategy is the selector strategy the agent runs: `best` is a
// fifo hop whose order the panel maintains (agents map unknown strategies to
// round-robin, so `best` must never reach them as is).
func runtimeTunnelStrategy(strategy string) string {
	if isBestTunnelStrategy(strategy) {
		return bestRuntimeStrategy
	}
	return strategy
}

func isBestLayer(rows []chainNodeRecord) bool {
	if len(rows) < 2 {
		return false
	}
	for _, row := range rows {
		if !isBestTunnelStrategy(row.Strategy) {
			return false
		}
	}
	return true
}

func isBestRuntimeLayer(targets []tunnelRuntimeNode) bool {
	if len(targets) < 2 {
		return false
	}
	for _, t := range targets {
		if t.LeiKwanPipeHop || !isBestTunnelStrategy(t.Strategy) {
			return false
		}
	}
	return true
}

func orderRuntimeTargetsByNodeID(targets []tunnelRuntimeNode, orderedIDs []int64) []tunnelRuntimeNode {
	out := append([]tunnelRuntimeNode(nil), targets...)
	if len(out) <= 1 || len(orderedIDs) == 0 {
		return out
	}
	positions := make(map[int64]int, len(orderedIDs))
	for i, id := range orderedIDs {
		if _, ok := positions[id]; !ok {
			positions[id] = i
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, iok := positions[out[i].NodeID]
		pj, jok := positions[out[j].NodeID]
		if iok != jok {
			return iok
		}
		if iok && jok && pi != pj {
			return pi < pj
		}
		return false
	})
	return out
}

// effectiveOrder is the order a chain built now uses: the decided order for
// the candidates it knows, then any others in DB order.
func effectiveOrder(dbOrder []int64, decided []int64) []int64 {
	if len(decided) == 0 {
		return append([]int64(nil), dbOrder...)
	}
	in := make(map[int64]bool, len(dbOrder))
	for _, id := range dbOrder {
		in[id] = true
	}
	out := make([]int64, 0, len(dbOrder))
	used := make(map[int64]bool, len(dbOrder))
	for _, id := range decided {
		if in[id] && !used[id] {
			out = append(out, id)
			used[id] = true
		}
	}
	for _, id := range dbOrder {
		if !used[id] {
			out = append(out, id)
		}
	}
	return out
}

type bestPathKey struct {
	TunnelID    int64
	OwnerNodeID int64
}

type bestPathCandidate struct {
	NodeID    int64
	EdgeOK    bool
	EdgeMS    float64
	CostMS    float64 // edge + best downstream path to an exit
	Reachable bool    // edge OK and an exit reachable behind the candidate
}

type bestPathState struct {
	Layer       int     // index of the 最优 group in [entries, hops…, exits]
	Order       []int64 // applied order (nil = DB order)
	PendingN    int     // consecutive rounds the current first was beaten by ≥ margin
	Ranked      []bestPathCandidate
	EvaluatedAt int64 // unix ms of the latest evaluation (0 = waiting)
	SwitchedAt  int64
}

type bestPathSwitch struct {
	Key        bestPathKey
	Layer      int
	FromID     int64
	ToID       int64
	FromCost   float64 // -1 = unreachable
	ToCost     float64
	Order      []int64
	Candidates []int64 // the layer in DB order when decided
	Failover   bool
	Gain       float64
}

type bestPathManager struct {
	mu     sync.Mutex
	states map[bestPathKey]*bestPathState
	// Since the last hourly summary.
	switches, deferred, pushFailures int64
}

func newBestPathManager() *bestPathManager {
	return &bestPathManager{states: make(map[bestPathKey]*bestPathState)}
}

// order returns a copy of the applied order of one owner (nil = DB order).
func (m *bestPathManager) order(key bestPathKey) []int64 {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.states[key]; st != nil {
		return append([]int64(nil), st.Order...)
	}
	return nil
}

func (m *bestPathManager) setOrder(key bestPathKey, order []int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[key]
	if st == nil {
		return nil
	}
	prev := st.Order
	st.Order = append([]int64(nil), order...)
	return prev
}

func (m *bestPathManager) restoreOrder(key bestPathKey, prev []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.states[key]; st != nil {
		st.Order = prev
	}
}

func (m *bestPathManager) markSwitched(key bestPathKey, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.states[key]; st != nil {
		st.PendingN = 0
		st.SwitchedAt = at.UnixMilli()
	}
	m.switches++
}

func (m *bestPathManager) snapshot(key bestPathKey) (bestPathState, bool) {
	if m == nil {
		return bestPathState{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[key]
	if st == nil {
		return bestPathState{}, false
	}
	out := *st
	out.Order = append([]int64(nil), st.Order...)
	out.Ranked = append([]bestPathCandidate(nil), st.Ranked...)
	return out, true
}

func (m *bestPathManager) size() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.states)
}

// takeCounters returns and resets the counters of the hourly summary.
func (m *bestPathManager) takeCounters() (owners int, switches, deferred, failures int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owners, switches, deferred, failures = len(m.states), m.switches, m.deferred, m.pushFailures
	m.switches, m.deferred, m.pushFailures = 0, 0, 0
	return
}

func (m *bestPathManager) count(deferred, failures int) {
	m.mu.Lock()
	m.deferred += int64(deferred)
	m.pushFailures += int64(failures)
	m.mu.Unlock()
}

type bestPathEdge struct{ From, To int64 }

// bestPathCosts returns, per group of the plan, the cheapest latency from each
// node to any exit over edges that answered in this round (exits cost 0), and
// the edge results per layer.
func bestPathCosts(plan pathLatencyPlan, results map[pathProbeKey]pathProbeResult) ([]map[int64]float64, []map[bestPathEdge]pathProbeResult) {
	groups := plan.Groups
	costs := make([]map[int64]float64, len(groups))
	edges := make([]map[bestPathEdge]pathProbeResult, len(plan.Layers))
	for i, layer := range plan.Layers {
		edges[i] = make(map[bestPathEdge]pathProbeResult, len(layer))
		for _, e := range layer {
			edges[i][bestPathEdge{e.From, e.To}] = results[e.Key]
		}
	}
	if len(groups) == 0 {
		return costs, edges
	}
	last := len(groups) - 1
	costs[last] = make(map[int64]float64, len(groups[last]))
	for _, exit := range groups[last] {
		costs[last][exit.NodeID] = 0
	}
	for g := last - 1; g >= 0; g-- {
		costs[g] = make(map[int64]float64, len(groups[g]))
		if g >= len(plan.Layers) {
			continue
		}
		for _, e := range plan.Layers[g] {
			res := results[e.Key]
			down, ok := costs[g+1][e.To]
			if !ok || !res.OK {
				continue
			}
			c := res.LatencyMS + down
			if prev, seen := costs[g][e.From]; !seen || c < prev {
				costs[g][e.From] = c
			}
		}
	}
	return costs, edges
}

// rankBestCandidates: reachable by path cost (ties keep DB order), then the
// unreachable ones in DB order.
func rankBestCandidates(ownerID int64, layer []chainNodeRecord, edges map[bestPathEdge]pathProbeResult, downstream map[int64]float64) []bestPathCandidate {
	out := make([]bestPathCandidate, 0, len(layer))
	for _, row := range layer {
		c := bestPathCandidate{NodeID: row.NodeID}
		if res, ok := edges[bestPathEdge{ownerID, row.NodeID}]; ok && res.OK {
			c.EdgeOK, c.EdgeMS = true, res.LatencyMS
			if down, ok := downstream[row.NodeID]; ok {
				c.CostMS, c.Reachable = res.LatencyMS+down, true
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Reachable != out[j].Reachable {
			return out[i].Reachable
		}
		return out[i].Reachable && out[i].CostMS < out[j].CostMS
	})
	return out
}

// evaluate runs one decision round. owner reports whether a node has a live
// agent session and whether that agent swaps chains without a gap. It
// returns the confirmed first-place changes (not yet applied) and drops the
// state of every (tunnel, owner) that is no longer a 最优 owner.
func (m *bestPathManager) evaluate(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, owner func(int64) (online, capable bool), now time.Time) []bestPathSwitch {
	m.mu.Lock()
	defer m.mu.Unlock()
	visited := make(map[bestPathKey]bool)
	var switches []bestPathSwitch
	for _, plan := range plans {
		if !plan.Active || len(plan.Groups) < 2 {
			continue
		}
		hasBest := false
		for g := 1; g < len(plan.Groups); g++ {
			if isBestLayer(plan.Groups[g]) {
				hasBest = true
				break
			}
		}
		if !hasBest {
			continue
		}
		// A node in two groups of one tunnel runs one role only; keep it static.
		seen := make(map[int64]int)
		for _, group := range plan.Groups {
			for _, row := range group {
				seen[row.NodeID]++
			}
		}
		costs, edges := bestPathCosts(plan, results)
		for g := 1; g < len(plan.Groups); g++ {
			layer := plan.Groups[g]
			if !isBestLayer(layer) || (plan.Pipe && g == 1) {
				continue
			}
			dbOrder := make([]int64, 0, len(layer))
			for _, row := range layer {
				dbOrder = append(dbOrder, row.NodeID)
			}
			for _, ownerRow := range plan.Groups[g-1] {
				ownerID := ownerRow.NodeID
				if plan.Remote[ownerID] || seen[ownerID] > 1 {
					continue
				}
				online, capable := owner(ownerID)
				if online && !capable {
					continue // old agent: static DB order, state dropped below
				}
				key := bestPathKey{TunnelID: plan.TunnelID, OwnerNodeID: ownerID}
				visited[key] = true
				st := m.states[key]
				if st == nil || st.Layer != g {
					st = &bestPathState{Layer: g}
					m.states[key] = st
				}
				if !online {
					continue // keep the decision; the reconnect pushes it
				}
				ranked := rankBestCandidates(ownerID, layer, edges[g-1], costs[g])
				st.Ranked = ranked
				st.EvaluatedAt = now.UnixMilli()
				current := effectiveOrder(dbOrder, st.Order)
				curID := current[0]
				var cur bestPathCandidate
				for _, c := range ranked {
					if c.NodeID == curID {
						cur = c
						break
					}
				}
				best := ranked[0]
				sw := bestPathSwitch{Key: key, Layer: g, FromID: curID, ToID: best.NodeID, FromCost: -1, ToCost: best.CostMS, Candidates: dbOrder}
				if cur.Reachable {
					sw.FromCost = cur.CostMS
				}
				switch {
				case !best.Reachable || best.NodeID == curID:
					st.PendingN = 0
					continue
				case !cur.Reachable:
					sw.Failover = true
				case cur.CostMS-best.CostMS >= bestPathSwitchMarginMS:
					st.PendingN++
					if st.PendingN < bestPathConfirmRounds {
						continue
					}
					sw.Gain = cur.CostMS - best.CostMS
				default:
					st.PendingN = 0
					continue
				}
				sw.Order = make([]int64, 0, len(ranked))
				for _, c := range ranked {
					sw.Order = append(sw.Order, c.NodeID)
				}
				switches = append(switches, sw)
			}
		}
	}
	for key := range m.states {
		if !visited[key] {
			delete(m.states, key)
		}
	}
	// Failovers first, then the biggest gains (the storm guard defers the rest).
	sort.SliceStable(switches, func(i, j int) bool {
		if switches[i].Failover != switches[j].Failover {
			return switches[i].Failover
		}
		return switches[i].Gain > switches[j].Gain
	})
	return switches
}

func (h *Handler) chainSwapCapable(nodeID int64) bool {
	if h.chainSwapCapableFn != nil {
		return h.chainSwapCapableFn(nodeID)
	}
	return h.wsServer.ChainSwapCapable(nodeID)
}

func (h *Handler) nodeAgentOnline(nodeID int64) bool {
	if h.nodeOnlineFn != nil {
		return h.nodeOnlineFn(nodeID)
	}
	return h.wsServer.NodeOnline(nodeID)
}

// bestOrderedTargets is the order the owner's chain uses: the current 最优
// decision for agents that swap chains without a gap, otherwise DB order.
func (h *Handler) bestOrderedTargets(tunnelID, ownerID int64, targets []tunnelRuntimeNode) []tunnelRuntimeNode {
	if h == nil || h.bestPath == nil || !isBestRuntimeLayer(targets) {
		return targets
	}
	order := h.bestPath.order(bestPathKey{TunnelID: tunnelID, OwnerNodeID: ownerID})
	if len(order) == 0 || !h.chainSwapCapable(ownerID) {
		return targets
	}
	return orderRuntimeTargetsByNodeID(targets, order)
}

// tunnelChainConfigFor builds the owner's chain object; every chain push
// (create, edit, redeploy, reconnect, entry split/merge, 最优 switch) goes
// through it so none of them reverts a 最优 decision to DB order.
func (h *Handler) tunnelChainConfigFor(state *tunnelCreateState, ownerID int64, targets []tunnelRuntimeNode) (map[string]interface{}, error) {
	return buildTunnelChainConfig(state.TunnelID, ownerID, h.bestOrderedTargets(state.TunnelID, ownerID, targets), state.Nodes, state.IPPreference)
}

// runBestPathRound decides and applies after a path-latency round.
func (h *Handler) runBestPathRound(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, now time.Time) {
	if h == nil || h.bestPath == nil {
		return
	}
	switches := h.bestPath.evaluate(plans, results, func(id int64) (bool, bool) {
		if !h.nodeAgentOnline(id) {
			return false, false
		}
		return true, h.chainSwapCapable(id)
	}, now)
	if len(switches) == 0 {
		return
	}
	_, deferred, failed := applyBestPathSwitches(switches, time.Now, h.applyBestPathSwitch)
	h.bestPath.count(deferred, failed)
}

// applyBestPathSwitches applies confirmed switches under the storm guard: at
// most bestPathMaxPushNode pushes per owner node and bestPathMaxPushRound per
// round, within bestPathRoundPushBudget; a node whose push failed gets no
// more pushes this round. Deferred switches stay confirmed for the next round.
func applyBestPathSwitches(switches []bestPathSwitch, clock func() time.Time, apply func(bestPathSwitch) error) (applied, deferred, failed int) {
	start := clock()
	perNode := make(map[int64]int)
	failedNode := make(map[int64]bool)
	for _, sw := range switches {
		owner := sw.Key.OwnerNodeID
		if applied >= bestPathMaxPushRound || perNode[owner] >= bestPathMaxPushNode || failedNode[owner] || clock().Sub(start) > bestPathRoundPushBudget {
			deferred++
			continue
		}
		err := apply(sw)
		switch {
		case err == nil:
			applied++
			perNode[owner]++
		case errors.Is(err, errBestPathBusy):
			deferred++
		case errors.Is(err, errBestPathStale):
		default:
			failed++
			failedNode[owner] = true
		}
	}
	return applied, deferred, failed
}

func sameInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyBestPathSwitch pushes the new order to the owner only. The decision
// is recorded first (under the tunnel lock every other builder of this
// tunnel waits for) and rolled back when the push fails, so memory always
// holds what the agent was last told; the next round retries.
func (h *Handler) applyBestPathSwitch(sw bestPathSwitch) error {
	unlock, ok := h.tryLockTunnelRuntime(sw.Key.TunnelID)
	if !ok {
		return errBestPathBusy
	}
	defer unlock()
	owner := sw.Key.OwnerNodeID
	if !h.chainSwapCapable(owner) {
		return errBestPathStale
	}
	tunnel, err := h.getTunnelRecord(sw.Key.TunnelID)
	if err != nil || tunnel == nil || tunnel.Type != 2 || tunnel.Status != 1 {
		return errBestPathStale
	}
	state, err := h.reconstructTunnelState(sw.Key.TunnelID)
	if err != nil {
		return err
	}
	if node := state.Nodes[owner]; node == nil || node.IsRemote == 1 {
		return errBestPathStale
	}
	if err := h.restoreFederationRuntimePorts(state); err != nil {
		return err
	}
	prev := h.bestPath.setOrder(sw.Key, sw.Order)
	plan, err := h.buildTunnelRuntimePlan(state, owner)
	if err == nil && (plan.chainData == nil || plan.federation || !isBestRuntimeLayer(plan.targets)) {
		err = errBestPathStale
	}
	if err == nil {
		ids := make([]int64, 0, len(plan.targets))
		for _, t := range plan.targets {
			ids = append(ids, t.NodeID)
		}
		if !sameInt64s(ids, sw.Candidates) {
			err = errBestPathStale
		}
	}
	if err == nil {
		_, err = h.sendNodeCommandWithTimeout(owner, "UpdateChains", map[string]interface{}{
			"chain": fmt.Sprintf("chains_%d", sw.Key.TunnelID),
			"data":  plan.chainData,
		}, bestPathPushTimeout, false, false)
	}
	if err != nil {
		h.bestPath.restoreOrder(sw.Key, prev)
		return err
	}
	h.bestPath.markSwitched(sw.Key, time.Now())
	reason := "faster"
	from := fmt.Sprintf("%.0fms", sw.FromCost)
	if sw.Failover {
		reason, from = "failover", "unreachable"
	}
	log.Printf("[best-path] switch tunnel=%d owner=%s(%d) layer=%d %s(%d) %s -> %s(%d) %.0fms reason=%s",
		sw.Key.TunnelID, nodeDisplayName(state.Nodes[owner]), owner, sw.Layer,
		nodeDisplayName(state.Nodes[sw.FromID]), sw.FromID, from,
		nodeDisplayName(state.Nodes[sw.ToID]), sw.ToID, sw.ToCost, reason)
	return nil
}

// roundMS rounds a latency for display.
func roundMS(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}
