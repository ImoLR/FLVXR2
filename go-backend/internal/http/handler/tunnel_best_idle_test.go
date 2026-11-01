package handler

import (
	"testing"
	"time"
)

// Plan 182: tunnels without rules keep their ranking (latency display) but
// take no part in 最优 switching; their first rule starts on the best order.

// bpIdleRound evaluates several synthetic tunnels in one round; idle marks the
// tunnels without rules.
func bpIdleRound(m *bestPathManager, nets []bpNet, idle map[int64]bool, at time.Time) []bestPathSwitch {
	plans := make([]pathLatencyPlan, 0, len(nets))
	results := map[pathProbeKey]pathProbeResult{}
	for _, n := range nets {
		plan, res := n.build()
		plan.Idle = idle[n.tunnelID]
		plans = append(plans, plan)
		for k, v := range res {
			results[k] = v
		}
	}
	return m.evaluate(plans, results, bpAllCapable, at)
}

// Two exits whose latency alternates every two rounds (prod: TW exits 12↔26
// on rule-less tunnels 27/30): entry 1 → exit {2, 3}.
func bpAlternating(tunnelID int64, round int) bpNet {
	fast, slow := 20.0, 32.0
	lat := map[[2]int64]float64{{1, 2}: fast, {1, 3}: slow}
	if (round/2)%2 == 1 {
		lat = map[[2]int64]float64{{1, 2}: slow, {1, 3}: fast}
	}
	return bpNet{tunnelID: tunnelID, groups: [][]int64{{1}, {2, 3}}, strategy: []string{"round", "best"}, lat: lat}
}

func TestBestPathIdleTunnelRankedButNeverSwitched(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	idle := map[int64]bool{30: true}
	pushes := map[int64]int{}
	for r := 0; r < 24; r++ {
		sws := bpIdleRound(m, []bpNet{bpAlternating(4, r), bpAlternating(30, r)}, idle, now.Add(time.Duration(r)*time.Minute))
		for _, sw := range sws {
			pushes[sw.Key.TunnelID]++
		}
		bpApply(m, sws, now)
	}
	if pushes[30] != 0 {
		t.Fatalf("tunnel without rules pushed %d switches", pushes[30])
	}
	if pushes[4] < 4 {
		t.Fatalf("tunnel in use switched only %d times (must keep switching as before)", pushes[4])
	}
	st, ok := m.snapshot(bestPathKey{TunnelID: 30, OwnerNodeID: 1})
	if !ok || !st.Idle || len(st.Ranked) != 2 || st.EvaluatedAt == 0 || len(st.Order) != 0 || st.PendingN != 0 {
		t.Fatalf("idle state %+v", st)
	}
	if got := m.idleTunnels(); got != 1 {
		t.Fatalf("idle tunnels = %d", got)
	}
	// Display: the idle tunnel shows the node its first rule will start on.
	info := func(id int64) bestPathNodeInfo { return bestPathNodeInfo{Name: "n", Online: true, Capable: true} }
	item := bestPathDisplayOwner(30, 1, 1, []int64{2, 3}, m, info, now.Add(23*time.Minute))
	if item.Status != bestPathStatusApplied || item.BestNodeID != st.Ranked[0].NodeID {
		t.Fatalf("idle display %+v (ranked %+v)", item, st.Ranked)
	}
}

// The same switch pattern with and without the tunnel in use: identical
// decisions for the tunnel in use (nothing changes for tunnel 4).
func TestBestPathInUseTunnelUnchanged(t *testing.T) {
	plain, withIdle := newBestPathManager(), newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	for r := 0; r < 16; r++ {
		at := now.Add(time.Duration(r) * time.Minute)
		a := bpIdleRound(plain, []bpNet{bpAlternating(4, r)}, nil, at)
		b := bpIdleRound(withIdle, []bpNet{bpAlternating(4, r), bpAlternating(30, r)}, map[int64]bool{30: true}, at)
		if len(a) != len(b) {
			t.Fatalf("round %d: %d vs %d switches", r, len(a), len(b))
		}
		for i := range a {
			if a[i].ToID != b[i].ToID || a[i].Activate || b[i].Activate {
				t.Fatalf("round %d: %+v vs %+v", r, a[i], b[i])
			}
		}
		bpApply(plain, a, at)
		bpApply(withIdle, b, at)
	}
}

func TestBestPathFirstRuleTakesBestAtOnce(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	// Exit 3 is 3 ms faster than DB-first 2: below the 5 ms margin.
	n := bpNet{tunnelID: 30, groups: [][]int64{{1}, {2, 3}}, strategy: []string{"round", "best"}, lat: map[[2]int64]float64{{1, 2}: 23, {1, 3}: 20}}
	idle := map[int64]bool{30: true}
	for r := 0; r < 3; r++ {
		if sws := bpIdleRound(m, []bpNet{n}, idle, now.Add(time.Duration(r)*time.Minute)); len(sws) != 0 {
			t.Fatalf("idle round %d switched %+v", r, sws)
		}
	}
	// A rule appears (seen by the round): the best at once, no confirm rounds.
	sws := bpIdleRound(m, []bpNet{n}, nil, now.Add(3*time.Minute))
	if len(sws) != 1 || !sws[0].Activate || sws[0].ToID != 3 || sws[0].Order[0] != 3 {
		t.Fatalf("first-rule round %+v", sws)
	}
	bpApply(m, sws, now.Add(3*time.Minute))
	if st, _ := m.snapshot(bestPathKey{TunnelID: 30, OwnerNodeID: 1}); st.Activate || st.Idle {
		t.Fatalf("after apply %+v", st)
	}
	// From now on the normal rule: 5 ms × 2 rounds.
	n.lat = map[[2]int64]float64{{1, 2}: 10, {1, 3}: 20}
	if sws := bpIdleRound(m, []bpNet{n}, nil, now.Add(4*time.Minute)); len(sws) != 0 {
		t.Fatalf("switched after one round: %+v", sws)
	}
	if sws := bpIdleRound(m, []bpNet{n}, nil, now.Add(5*time.Minute)); len(sws) != 1 || sws[0].Activate || sws[0].ToID != 2 {
		t.Fatalf("normal switch %+v", sws)
	}
}

func TestBestPathActivationOnRuleDeploy(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpNet{tunnelID: 30, groups: [][]int64{{1}, {2, 3}}, strategy: []string{"round", "best"}, lat: map[[2]int64]float64{{1, 2}: 30, {1, 3}: 20}}
	idle := map[int64]bool{30: true}
	bpIdleRound(m, []bpNet{n}, idle, now)
	bpIdleRound(m, []bpNet{n}, idle, now.Add(time.Minute))

	// Other tunnels and tunnels in use: nothing.
	if sws := m.activationSwitches(4, bpAllCapable, now.Add(70*time.Second)); len(sws) != 0 {
		t.Fatalf("unknown tunnel %+v", sws)
	}
	sws := m.activationSwitches(30, bpAllCapable, now.Add(70*time.Second))
	if len(sws) != 1 || !sws[0].Activate || sws[0].ToID != 3 || sws[0].FromID != 2 || len(sws[0].Candidates) != 2 {
		t.Fatalf("activation %+v", sws)
	}
	bpApply(m, sws, now.Add(70*time.Second))
	if again := m.activationSwitches(30, bpAllCapable, now.Add(75*time.Second)); len(again) != 0 {
		t.Fatalf("second rule activated again: %+v", again)
	}
	// The next round (the rule is there): already best, nothing to push.
	if sws := bpIdleRound(m, []bpNet{n}, nil, now.Add(2*time.Minute)); len(sws) != 0 {
		t.Fatalf("round after activation pushed %+v", sws)
	}

	// Stale ranking (> 150 s) or offline owner: no push now, the next round
	// does it at once.
	m2 := newBestPathManager()
	bpIdleRound(m2, []bpNet{n}, idle, now)
	if sws := m2.activationSwitches(30, bpAllCapable, now.Add(10*time.Minute)); len(sws) != 0 {
		t.Fatalf("stale ranking pushed %+v", sws)
	}
	if sws := bpIdleRound(m2, []bpNet{n}, nil, now.Add(11*time.Minute)); len(sws) != 1 || !sws[0].Activate {
		t.Fatalf("round after stale activation %+v", sws)
	}
	m3 := newBestPathManager()
	bpIdleRound(m3, []bpNet{n}, idle, now)
	offline := func(int64) (bool, bool) { return false, false }
	if sws := m3.activationSwitches(30, offline, now.Add(10*time.Second)); len(sws) != 0 {
		t.Fatalf("offline owner pushed %+v", sws)
	}
	// Activation already consumed by the hook's "not idle" mark: the round
	// still sees Activate and pushes at once.
	if sws := bpIdleRound(m3, []bpNet{n}, nil, now.Add(time.Minute)); len(sws) != 1 || !sws[0].Activate {
		t.Fatalf("round after offline activation %+v", sws)
	}
}

func TestBestPathIdleAgainStopsWithoutPush(t *testing.T) {
	m := newBestPathManager()
	now := time.Unix(1_800_000_000, 0)
	n := bpNet{tunnelID: 30, groups: [][]int64{{1}, {2, 3}}, strategy: []string{"round", "best"}, lat: map[[2]int64]float64{{1, 2}: 30, {1, 3}: 20}}
	bpIdleRound(m, []bpNet{n}, nil, now)
	sws := bpIdleRound(m, []bpNet{n}, nil, now.Add(time.Minute))
	if len(sws) != 1 {
		t.Fatalf("in use: %+v", sws)
	}
	bpApply(m, sws, now)
	// Last rule deleted; exit 3 then dies (would be a failover in use).
	n.lat = map[[2]int64]float64{{1, 2}: 30}
	idle := map[int64]bool{30: true}
	for r := 2; r < 6; r++ {
		if sws := bpIdleRound(m, []bpNet{n}, idle, now.Add(time.Duration(r)*time.Minute)); len(sws) != 0 {
			t.Fatalf("idle again round %d pushed %+v", r, sws)
		}
	}
	// A rule again: the failover-equivalent best at once.
	sws = bpIdleRound(m, []bpNet{n}, nil, now.Add(6*time.Minute))
	if len(sws) != 1 || sws[0].ToID != 2 {
		t.Fatalf("reuse %+v", sws)
	}
}

// 规则最优: a tunnel whose last rule went away pushes no cleanup while idle;
// the cleanup waits until the tunnel has rules again.
func TestRuleBestIdleTunnelDefersCleanup(t *testing.T) {
	key := "gone.example:443"
	plan := rbPlan(10, [][]int64{{1}, {4, 5}})
	rbDest(&plan, key)
	plans := []pathLatencyPlan{plan}
	edges := map[[2]int64]float64{{1, 4}: 10, {1, 5}: 30}
	res := rbResults(plans, edges, map[string]map[int64]float64{key: {4: 80, 5: 20}})
	now := time.Unix(1_800_000_000, 0)
	m := newRuleBestManager()
	rbEval(m, plans, res, now)
	rbApply(m, rbEval(m, plans, res, now.Add(time.Minute)), now)

	plan.RuleBest = &ruleBestPlan{}
	plan.Idle = true
	plans = []pathLatencyPlan{plan}
	for r := 2; r < 5; r++ {
		if p := rbEval(m, plans, rbResults(plans, edges, nil), now.Add(time.Duration(r)*time.Minute)); len(p) != 0 {
			t.Fatalf("idle tunnel pushed %+v", p)
		}
	}
	if _, _, dirty, _ := m.size(); dirty != 1 {
		t.Fatalf("cleanup not kept pending: dirty=%d", dirty)
	}
	plan.Idle = false
	plans = []pathLatencyPlan{plan}
	pushes := rbEval(m, plans, rbResults(plans, edges, nil), now.Add(6*time.Minute))
	if len(pushes) != 1 || !pushes[0].Cleanup {
		t.Fatalf("cleanup after reuse %+v", pushes)
	}
}
