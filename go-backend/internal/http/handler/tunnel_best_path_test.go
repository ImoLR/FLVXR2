package handler

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// bpNet is a synthetic tunnel for the 最优 engine: groups of node ids
// ([entries, hops…, exits]) with per-group strategy, and edge latencies
// (a missing edge = failed in the round).
type bpNet struct {
	tunnelID int64
	groups   [][]int64
	strategy []string
	lat      map[[2]int64]float64
	inactive bool
	pipe     bool
	remote   map[int64]bool
}

func (n bpNet) build() (pathLatencyPlan, map[pathProbeKey]pathProbeResult) {
	plan := pathLatencyPlan{TunnelID: n.tunnelID, Active: !n.inactive, Pipe: n.pipe, Remote: n.remote}
	results := map[pathProbeKey]pathProbeResult{}
	for g, ids := range n.groups {
		rows := make([]chainNodeRecord, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, chainNodeRecord{NodeID: id, Strategy: n.strategy[g]})
		}
		plan.Groups = append(plan.Groups, rows)
	}
	plan.Entries = plan.Groups[0]
	for g := 1; g < len(n.groups); g++ {
		var edges []pathLatencyEdge
		for _, from := range n.groups[g-1] {
			for _, to := range n.groups[g] {
				key := pathProbeKey{From: from, Host: fmt.Sprintf("n%d", to), Port: 1000 + int(n.tunnelID)}
				edges = append(edges, pathLatencyEdge{From: from, To: to, Key: key})
				if v, ok := n.lat[[2]int64{from, to}]; ok {
					results[key] = pathProbeResult{LatencyMS: v, OK: true}
				} else {
					results[key] = pathProbeResult{}
				}
			}
		}
		plan.Layers = append(plan.Layers, edges)
	}
	return plan, results
}

func bpAllCapable(int64) (bool, bool) { return true, true }

func bpRound(m *bestPathManager, n bpNet, owner func(int64) (bool, bool), at time.Time) []bestPathSwitch {
	plan, results := n.build()
	return m.evaluate([]pathLatencyPlan{plan}, results, owner, at)
}

// apply records a switch as the push would on success.
func bpApply(m *bestPathManager, sws []bestPathSwitch, at time.Time) {
	for _, sw := range sws {
		m.setOrder(sw.Key, sw.Order)
		m.markSwitched(sw.Key, at)
	}
}

// The brief's example: entry → 中转 {A, B} (最优) → exit. Edges E→A 10 ms,
// A→X 100 ms, E→B 30 ms, B→X 20 ms. Per hop A looks better (10 < 30) but the
// whole path via B is 50 ms against 110 ms → B.
func bpExample() bpNet {
	return bpNet{
		tunnelID: 7,
		groups:   [][]int64{{1}, {2, 3}, {9}},
		strategy: []string{"round", "best", "round"},
		lat:      map[[2]int64]float64{{1, 2}: 10, {2, 9}: 100, {1, 3}: 30, {3, 9}: 20},
	}
}

func TestBestPathRanksWholePathNotFirstHop(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	if sws := bpRound(m, bpExample(), bpAllCapable, now); len(sws) != 0 {
		t.Fatalf("round 1 must only start the confirmation, got %+v", sws)
	}
	st, _ := m.snapshot(bestPathKey{7, 1})
	if st.Ranked[0].NodeID != 3 || st.Ranked[0].CostMS != 50 || st.Ranked[1].NodeID != 2 || st.Ranked[1].CostMS != 110 {
		t.Fatalf("ranking = %+v, want B(3) 50 ms then A(2) 110 ms", st.Ranked)
	}
	sws := bpRound(m, bpExample(), bpAllCapable, now.Add(time.Minute))
	if len(sws) != 1 || sws[0].ToID != 3 || sws[0].FromID != 2 || sws[0].Failover || sws[0].Gain != 60 {
		t.Fatalf("round 2 switch = %+v, want 2 → 3 gain 60", sws)
	}
	if got := fmt.Sprint(sws[0].Order); got != "[3 2]" {
		t.Fatalf("new order = %s, want [3 2]", got)
	}
}

func TestBestPathMarginIs15ms(t *testing.T) {
	for _, tc := range []struct {
		gain    float64
		switch_ bool
	}{{14.9, false}, {15, true}, {40, true}} {
		m := newBestPathManager()
		n := bpExample()
		// via A: 10 + 50 = 60; via B: 60 - gain.
		n.lat = map[[2]int64]float64{{1, 2}: 10, {2, 9}: 50, {1, 3}: 30, {3, 9}: 30 - tc.gain}
		now := time.Unix(1_800_000_000, 0)
		var got []bestPathSwitch
		for i := 0; i < 4; i++ {
			got = append(got, bpRound(m, n, bpAllCapable, now.Add(time.Duration(i)*time.Minute))...)
			bpApply(m, got, now)
		}
		if (len(got) > 0) != tc.switch_ {
			t.Fatalf("gain %.1f ms: switches %+v, want switch=%v", tc.gain, got, tc.switch_)
		}
	}
}

func TestBestPathNeedsTwoConsecutiveRounds(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	good, spike := bpExample(), bpExample()
	spike.lat = map[[2]int64]float64{{1, 2}: 10, {2, 9}: 100, {1, 3}: 30, {3, 9}: 75} // B 105: gain 5
	steps := []struct {
		n    bpNet
		want int
	}{{good, 0}, {spike, 0}, {good, 0}, {good, 1}}
	for i, step := range steps {
		sws := bpRound(m, step.n, bpAllCapable, now.Add(time.Duration(i)*time.Minute))
		if len(sws) != step.want {
			t.Fatalf("round %d: %d switches %+v, want %d", i+1, len(sws), sws, step.want)
		}
	}
}

func TestBestPathFailoverIsImmediate(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpExample()
	for i := 0; i < 2; i++ {
		bpApply(m, bpRound(m, n, bpAllCapable, now.Add(time.Duration(i)*time.Minute)), now)
	}
	if got := m.order(bestPathKey{7, 1}); fmt.Sprint(got) != "[3 2]" {
		t.Fatalf("setup: order %v", got)
	}
	// B's downstream dies (B→exit failed): B is unreachable → back to A at once.
	n.lat = map[[2]int64]float64{{1, 2}: 10, {2, 9}: 100, {1, 3}: 30}
	sws := bpRound(m, n, bpAllCapable, now.Add(3*time.Minute))
	if len(sws) != 1 || !sws[0].Failover || sws[0].ToID != 2 || sws[0].FromCost != -1 {
		t.Fatalf("downstream failure: %+v, want immediate failover to 2", sws)
	}
	bpApply(m, sws, now)
	// Now A's own edge fails: immediate switch to B (reachable again).
	n.lat = map[[2]int64]float64{{2, 9}: 100, {1, 3}: 30, {3, 9}: 20}
	sws = bpRound(m, n, bpAllCapable, now.Add(4*time.Minute))
	if len(sws) != 1 || !sws[0].Failover || sws[0].ToID != 3 {
		t.Fatalf("edge failure: %+v, want immediate failover to 3", sws)
	}
	// Everything down: nothing to switch to, order kept.
	bpApply(m, sws, now)
	n.lat = map[[2]int64]float64{}
	if sws := bpRound(m, n, bpAllCapable, now.Add(5*time.Minute)); len(sws) != 0 {
		t.Fatalf("all unreachable must not switch: %+v", sws)
	}
	if got := m.order(bestPathKey{7, 1}); fmt.Sprint(got) != "[3 2]" {
		t.Fatalf("order changed while all unreachable: %v", got)
	}
}

func TestBestPathAlternatingChallengersStillSwitch(t *testing.T) {
	// Current first A is 60 ms worse than both B and C, which trade places by
	// a few ms each round: after 2 rounds the round's best takes over.
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpNet{tunnelID: 8, groups: [][]int64{{1}, {2, 3, 4}}, strategy: []string{"round", "best"}}
	n.lat = map[[2]int64]float64{{1, 2}: 100, {1, 3}: 40, {1, 4}: 42}
	if sws := bpRound(m, n, bpAllCapable, now); len(sws) != 0 {
		t.Fatalf("round 1: %+v", sws)
	}
	n.lat = map[[2]int64]float64{{1, 2}: 100, {1, 3}: 43, {1, 4}: 41}
	sws := bpRound(m, n, bpAllCapable, now.Add(time.Minute))
	if len(sws) != 1 || sws[0].ToID != 4 || fmt.Sprint(sws[0].Order) != "[4 3 2]" {
		t.Fatalf("round 2: %+v, want switch to 4 order [4 3 2]", sws)
	}
}

func TestBestPathOnlyFirstPlaceChangesPush(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpNet{tunnelID: 9, groups: [][]int64{{1}, {2, 3, 4}}, strategy: []string{"round", "best"}}
	n.lat = map[[2]int64]float64{{1, 2}: 10, {1, 3}: 50, {1, 4}: 90}
	for i := 0; i < 3; i++ {
		if sws := bpRound(m, n, bpAllCapable, now.Add(time.Duration(i)*time.Minute)); len(sws) != 0 {
			t.Fatalf("first already best: %+v", sws)
		}
	}
	// Backups swap places (4 now faster than 3), first stays: no push.
	n.lat = map[[2]int64]float64{{1, 2}: 10, {1, 3}: 90, {1, 4}: 50}
	for i := 3; i < 6; i++ {
		if sws := bpRound(m, n, bpAllCapable, now.Add(time.Duration(i)*time.Minute)); len(sws) != 0 {
			t.Fatalf("backup reorder must not push: %+v", sws)
		}
	}
}

func TestBestPathHopAndExitLayersPerOwner(t *testing.T) {
	// entry 1 → 中转 {2,3} 最优 → 出口 {4,5} 最优: owners 1 (hop layer), 2 and 3
	// (exit layer) are decided independently.
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpNet{tunnelID: 10, groups: [][]int64{{1}, {2, 3}, {4, 5}}, strategy: []string{"round", "best", "best"}}
	n.lat = map[[2]int64]float64{{1, 2}: 10, {1, 3}: 12, {2, 4}: 90, {2, 5}: 30, {3, 4}: 5, {3, 5}: 6}
	bpRound(m, n, bpAllCapable, now)
	sws := bpRound(m, n, bpAllCapable, now.Add(time.Minute))
	got := map[int64]int64{}
	for _, sw := range sws {
		got[sw.Key.OwnerNodeID] = sw.ToID
	}
	// owner 1: via 2 = 10+30 = 40, via 3 = 12+5 = 17 → 3; owner 2: 5 (30) beats 4 (90);
	// owner 3: 4 (5) already first.
	if len(sws) != 2 || got[1] != 3 || got[2] != 5 {
		t.Fatalf("switches %+v, want owner1→3 and owner2→5", sws)
	}
	if m.size() != 3 {
		t.Fatalf("states = %d, want 3 owners", m.size())
	}
}

func TestBestPathStaticOwnersAndCleanup(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpExample()
	bpRound(m, n, bpAllCapable, now)
	if m.size() != 1 {
		t.Fatalf("setup: %d states", m.size())
	}
	// Old agent (online, no chainSwap): no state, never a switch.
	oldAgent := func(int64) (bool, bool) { return true, false }
	for i := 1; i < 4; i++ {
		if sws := bpRound(m, n, oldAgent, now.Add(time.Duration(i)*time.Minute)); len(sws) != 0 {
			t.Fatalf("old agent switched: %+v", sws)
		}
	}
	if m.size() != 0 {
		t.Fatalf("old-agent owner kept %d states", m.size())
	}
	// Offline owner keeps its decision (the reconnect pushes it), no switch.
	bpApply(m, append(bpRound(m, n, bpAllCapable, now), bpRound(m, n, bpAllCapable, now.Add(time.Minute))...), now)
	offline := func(int64) (bool, bool) { return false, false }
	n2 := n
	n2.lat = map[[2]int64]float64{}
	if sws := bpRound(m, n2, offline, now.Add(2*time.Minute)); len(sws) != 0 || fmt.Sprint(m.order(bestPathKey{7, 1})) != "[3 2]" {
		t.Fatalf("offline owner: switches %+v order %v", sws, m.order(bestPathKey{7, 1}))
	}
	// Remote owner / pipe entry / disabled tunnel / not 最优 any more / tunnel gone → dropped.
	for name, mutate := range map[string]func(*bpNet){
		"remote":   func(x *bpNet) { x.remote = map[int64]bool{1: true} },
		"pipe":     func(x *bpNet) { x.pipe = true },
		"disabled": func(x *bpNet) { x.inactive = true },
		"fifo":     func(x *bpNet) { x.strategy = []string{"round", "fifo", "round"} },
		"single":   func(x *bpNet) { x.groups = [][]int64{{1}, {3}, {9}} },
	} {
		bpRound(m, n, bpAllCapable, now)
		x := n
		mutate(&x)
		if sws := bpRound(m, x, bpAllCapable, now.Add(time.Minute)); len(sws) != 0 || m.size() != 0 {
			t.Fatalf("%s: switches %+v, %d states left", name, sws, m.size())
		}
	}
	bpRound(m, n, bpAllCapable, now)
	m.evaluate(nil, nil, bpAllCapable, now) // tunnel deleted: not in any plan
	if m.size() != 0 {
		t.Fatalf("deleted tunnel left %d states", m.size())
	}
}

func TestBestPathStormGuard(t *testing.T) {
	var sws []bestPathSwitch
	for i := int64(0); i < 60; i++ {
		owner := int64(1)
		if i >= 30 {
			owner = 100 + i // 30 distinct owners
		}
		sws = append(sws, bestPathSwitch{Key: bestPathKey{TunnelID: i + 1, OwnerNodeID: owner}})
	}
	pushes := map[int64]int{}
	applied, deferred, failed := applyBestPathSwitches(sws, time.Now, func(sw bestPathSwitch) error {
		pushes[sw.Key.OwnerNodeID]++
		return nil
	})
	if pushes[1] != bestPathMaxPushNode || applied != bestPathMaxPushNode+30 || deferred != 20 || failed != 0 {
		t.Fatalf("applied=%d deferred=%d failed=%d node1=%d", applied, deferred, failed, pushes[1])
	}
	// Global cap.
	sws = sws[:0]
	for i := int64(0); i < 100; i++ {
		sws = append(sws, bestPathSwitch{Key: bestPathKey{TunnelID: i + 1, OwnerNodeID: 1000 + i}})
	}
	applied, deferred, _ = applyBestPathSwitches(sws, time.Now, func(bestPathSwitch) error { return nil })
	if applied != bestPathMaxPushRound || deferred != 100-bestPathMaxPushRound {
		t.Fatalf("global cap: applied=%d deferred=%d", applied, deferred)
	}
	// A failing node gets no second push this round; busy tunnels are deferred.
	calls := 0
	applied, deferred, failed = applyBestPathSwitches([]bestPathSwitch{
		{Key: bestPathKey{1, 5}}, {Key: bestPathKey{2, 5}}, {Key: bestPathKey{3, 6}},
	}, time.Now, func(sw bestPathSwitch) error {
		calls++
		if sw.Key.OwnerNodeID == 5 {
			return errors.New("等待节点响应超时")
		}
		return errBestPathBusy
	})
	if calls != 2 || failed != 1 || deferred != 2 || applied != 0 {
		t.Fatalf("calls=%d applied=%d deferred=%d failed=%d", calls, applied, deferred, failed)
	}
	// Time budget.
	clock := time.Unix(0, 0)
	applied, deferred, _ = applyBestPathSwitches([]bestPathSwitch{{Key: bestPathKey{1, 1}}, {Key: bestPathKey{2, 2}}, {Key: bestPathKey{3, 3}}},
		func() time.Time { return clock }, func(bestPathSwitch) error { clock = clock.Add(15 * time.Second); return nil })
	if applied != 2 || deferred != 1 {
		t.Fatalf("budget: applied=%d deferred=%d", applied, deferred)
	}
}

func TestEffectiveOrderAndOrderedTargets(t *testing.T) {
	if got := fmt.Sprint(effectiveOrder([]int64{1, 2, 3, 4}, []int64{3, 9, 1})); got != "[3 1 2 4]" {
		t.Fatalf("effectiveOrder = %s", got)
	}
	h := &Handler{bestPath: newBestPathManager(), chainSwapCapableFn: func(int64) bool { return true }}
	h.bestPath.states[bestPathKey{5, 1}] = &bestPathState{Layer: 1, Order: []int64{3, 2}}
	targets := []tunnelRuntimeNode{{NodeID: 2, Strategy: "best"}, {NodeID: 3, Strategy: "best"}}
	if got := h.bestOrderedTargets(5, 1, targets); got[0].NodeID != 3 {
		t.Fatalf("capable owner not reordered: %+v", got)
	}
	h.chainSwapCapableFn = func(int64) bool { return false }
	if got := h.bestOrderedTargets(5, 1, targets); got[0].NodeID != 2 {
		t.Fatalf("old agent must keep DB order: %+v", got)
	}
	h.chainSwapCapableFn = func(int64) bool { return true }
	mixed := []tunnelRuntimeNode{{NodeID: 2, Strategy: "best"}, {NodeID: 3, Strategy: "fifo"}}
	if got := h.bestOrderedTargets(5, 1, mixed); got[0].NodeID != 2 {
		t.Fatalf("non-最优 layer reordered: %+v", got)
	}
	if runtimeTunnelStrategy("best") != "fifo" || runtimeTunnelStrategy("round") != "round" {
		t.Fatalf("runtime strategy mapping")
	}
}

// Seeded round: 200 tunnels × (1 entry + 2 中转 groups of 3 + 3 exits), all
// 最优, every edge answered — the CPU the decision step adds per round.
func benchBestPathPlans() ([]pathLatencyPlan, map[pathProbeKey]pathProbeResult) {
	var plans []pathLatencyPlan
	results := map[pathProbeKey]pathProbeResult{}
	for t := int64(1); t <= 200; t++ {
		base := t * 100
		n := bpNet{tunnelID: t, strategy: []string{"round", "best", "best", "best"},
			groups: [][]int64{{base}, {base + 1, base + 2, base + 3}, {base + 4, base + 5, base + 6}, {base + 7, base + 8, base + 9}},
			lat:    map[[2]int64]float64{}}
		for g := 1; g < len(n.groups); g++ {
			for _, from := range n.groups[g-1] {
				for i, to := range n.groups[g] {
					n.lat[[2]int64{from, to}] = float64(10 + (int(from+to)*7+i*13)%90)
				}
			}
		}
		plan, res := n.build()
		plans = append(plans, plan)
		for k, v := range res {
			results[k] = v
		}
	}
	return plans, results
}

func BenchmarkBestPathRound200x3x3(b *testing.B) {
	plans, results := benchBestPathPlans()
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.evaluate(plans, results, bpAllCapable, now.Add(time.Duration(i)*time.Minute))
	}
}

func TestBestPathDisplaySummaries(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	// Two entries pick different exits.
	n := bpNet{tunnelID: 11, groups: [][]int64{{1, 2}, {4, 5}}, strategy: []string{"round", "best"},
		lat: map[[2]int64]float64{{1, 4}: 10, {1, 5}: 80, {2, 4}: 90, {2, 5}: 12}}
	bpApply(m, bpRound(m, n, bpAllCapable, now), now)
	bpApply(m, bpRound(m, n, bpAllCapable, now.Add(time.Minute)), now)
	row := func(id int64) map[string]interface{} {
		return map[string]interface{}{"nodeId": id, "strategy": "best"}
	}
	tunnel := map[string]interface{}{"id": int64(11), "type": 2, "status": 1,
		"inNodeId":  []map[string]interface{}{{"nodeId": int64(1)}, {"nodeId": int64(2)}},
		"outNodeId": []map[string]interface{}{row(4), row(5)}}
	info := func(id int64) bestPathNodeInfo {
		return bestPathNodeInfo{Name: fmt.Sprintf("N%d", id), Online: true, Capable: true, Remote: id == 7}
	}
	state, ok := buildBestPathDisplayState(tunnel, m, info, now.Add(time.Minute))
	if !ok || len(state.Layers) != 1 {
		t.Fatalf("state %+v", state)
	}
	l := state.Layers[0]
	if l.Status != "applied" || l.Summary != "2 个节点" || l.MinMs != 10 || l.MaxMs != 12 || l.Label != "出口" {
		t.Fatalf("layer %+v", l)
	}
	// Stale (no round for > 150 s) → 等待探测.
	if state, _ := buildBestPathDisplayState(tunnel, m, info, now.Add(5*time.Minute)); state.Layers[0].Status != "waiting" {
		t.Fatalf("stale: %+v", state.Layers[0])
	}
	// Everything unreachable for both owners.
	n.lat = map[[2]int64]float64{}
	bpRound(m, n, bpAllCapable, now.Add(2*time.Minute))
	if state, _ := buildBestPathDisplayState(tunnel, m, info, now.Add(2*time.Minute)); state.Layers[0].Status != "unreachable" || state.Layers[0].Summary != "全部不可达" {
		t.Fatalf("unreachable: %+v", state.Layers[0])
	}
	// Remote owner, pipe entry, disabled tunnel, single exit.
	remote := map[string]interface{}{"id": int64(12), "type": 2, "status": 1,
		"inNodeId": []map[string]interface{}{{"nodeId": int64(7)}}, "outNodeId": []map[string]interface{}{row(4), row(5)}}
	if state, _ := buildBestPathDisplayState(remote, m, info, now); state.Layers[0].Status != "remote" {
		t.Fatalf("remote: %+v", state.Layers[0])
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"disabled": func(x map[string]interface{}) { x["status"] = 0 },
		"single":   func(x map[string]interface{}) { x["outNodeId"] = []map[string]interface{}{row(4)} },
		"type1":    func(x map[string]interface{}) { x["type"] = 1 },
	} {
		x := map[string]interface{}{}
		for k, v := range tunnel {
			x[k] = v
		}
		mutate(x)
		if _, ok := buildBestPathDisplayState(x, m, info, now); ok {
			t.Fatalf("%s: display state present", name)
		}
	}
	pipe := map[string]interface{}{"id": int64(13), "type": 2, "status": 1, "leikwanPipeId": int64(1),
		"inNodeId": []map[string]interface{}{{"nodeId": int64(1)}}, "outNodeId": []map[string]interface{}{row(4), row(5)}}
	if _, ok := buildBestPathDisplayState(pipe, m, info, now); ok {
		t.Fatalf("pipe entry layer must stay static")
	}
}
