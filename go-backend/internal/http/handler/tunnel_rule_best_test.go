package handler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/repo"
)

type rbForward struct {
	id, tunnel int64
	addr, mode string
	status     int
}

func rbForwards(in []rbForward) []repo.RuleBestForward {
	out := make([]repo.RuleBestForward, 0, len(in))
	for _, f := range in {
		out = append(out, repo.RuleBestForward{ID: f.id, TunnelID: f.tunnel, RemoteAddr: f.addr, Mode: f.mode, Status: f.status})
	}
	return out
}

// ---- pure decision tests (plan 180) -------------------------------------

// rbPlan builds a tunnel plan: groups[0] = entries, then hops…, last = exits;
// a group with ≥ 2 nodes is 最优. Edge probe keys are synthetic.
func rbPlan(tunnelID int64, groups [][]int64) pathLatencyPlan {
	plan := pathLatencyPlan{TunnelID: tunnelID, Active: true, RuleBestOn: true}
	for g, ids := range groups {
		rows := make([]chainNodeRecord, 0, len(ids))
		for _, id := range ids {
			strategy := "round"
			if g > 0 && len(ids) >= 2 {
				strategy = "best"
			}
			rows = append(rows, chainNodeRecord{NodeID: id, NodeName: fmt.Sprintf("node-%d", id), Strategy: strategy})
		}
		plan.Groups = append(plan.Groups, rows)
	}
	plan.Entries = plan.Groups[0]
	for g := 1; g < len(plan.Groups); g++ {
		var edges []pathLatencyEdge
		for _, from := range plan.Groups[g-1] {
			for _, to := range plan.Groups[g] {
				edges = append(edges, pathLatencyEdge{From: from.NodeID, To: to.NodeID, Key: pathProbeKey{From: from.NodeID, Host: fmt.Sprintf("n%d", to.NodeID), Port: 1000 + int(to.NodeID)}})
			}
		}
		plan.Layers = append(plan.Layers, edges)
	}
	return plan
}

// rbDest adds a destination probed from every exit of the plan.
func rbDest(plan *pathLatencyPlan, key string) {
	if plan.RuleBest == nil {
		plan.RuleBest = &ruleBestPlan{Forwards: map[int64][]string{}}
	}
	target, _ := splitDestTarget(key)
	d := ruleBestPlanDest{Key: key, Label: key, Probe: target, Exits: map[int64]pathProbeKey{}}
	for _, exit := range plan.Groups[len(plan.Groups)-1] {
		d.Exits[exit.NodeID] = pathProbeKey{From: exit.NodeID, Host: target.Host, Port: target.Port}
	}
	plan.RuleBest.Dests = append(plan.RuleBest.Dests, d)
}

// rbResults: edge latencies {from,to} and destination latencies dest→exit→ms
// (missing = failed in the round).
func rbResults(plans []pathLatencyPlan, edges map[[2]int64]float64, dests map[string]map[int64]float64) map[pathProbeKey]pathProbeResult {
	out := map[pathProbeKey]pathProbeResult{}
	for _, plan := range plans {
		for _, layer := range plan.Layers {
			for _, e := range layer {
				if v, ok := edges[[2]int64{e.From, e.To}]; ok {
					out[e.Key] = pathProbeResult{LatencyMS: v, OK: true}
				} else {
					out[e.Key] = pathProbeResult{}
				}
			}
		}
		if plan.RuleBest == nil {
			continue
		}
		for _, d := range plan.RuleBest.Dests {
			for exit, key := range d.Exits {
				if v, ok := dests[d.Key][exit]; ok {
					out[key] = pathProbeResult{LatencyMS: v, OK: true}
				} else {
					out[key] = pathProbeResult{}
				}
			}
		}
	}
	return out
}

func rbAllCapable(int64) (bool, bool) { return true, true }

func rbNoTunnelOrder(bestPathKey) []int64 { return nil }

func rbEval(m *ruleBestManager, plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, at time.Time) []ruleBestPush {
	dead, recovered, _ := m.updateDead(plans, results, at)
	return m.evaluate(plans, results, dead, recovered, rbNoTunnelOrder, rbAllCapable, at)
}

// rbApply marks every push applied (what a successful UpdateChains does).
func rbApply(m *ruleBestManager, pushes []ruleBestPush, at time.Time) {
	for _, p := range pushes {
		m.setOrders(p)
		m.markPushed(p, at)
	}
}

// Brief example: exit A = 10 ms hop + 80 ms to the target, exit B = 30 + 20
// ⇒ B, after 2 rounds; plan 165 alone (entry→exit) would keep A.
func TestRuleBestCostIncludesExitToTarget(t *testing.T) {
	plan := rbPlan(7, [][]int64{{1}, {4, 5}})
	rbDest(&plan, "target.example:443")
	plans := []pathLatencyPlan{plan}
	res := rbResults(plans, map[[2]int64]float64{{1, 4}: 10, {1, 5}: 30}, map[string]map[int64]float64{"target.example:443": {4: 80, 5: 20}})
	m := newRuleBestManager()
	now := time.Unix(1_800_000_000, 0)
	if p := rbEval(m, plans, res, now); len(p) != 0 {
		t.Fatalf("round 1 must only count: %+v", p)
	}
	st, _ := m.snapshot(ruleBestKey{7, 1, "target.example:443"})
	if st.PendingN != 1 || st.Ranked[0].NodeID != 5 || st.Ranked[0].CostMS != 50 || st.Ranked[1].CostMS != 90 {
		t.Fatalf("ranking %+v", st)
	}
	p := rbEval(m, plans, res, now.Add(time.Minute))
	if len(p) != 1 || p[0].Key != (bestPathKey{7, 1}) || len(p[0].Switches) != 1 {
		t.Fatalf("round 2 pushes %+v", p)
	}
	sw := p[0].Switches[0]
	if sw.FromID != 4 || sw.ToID != 5 || sw.FromCost != 90 || sw.ToCost != 50 || sw.Failover || !reflect.DeepEqual(sw.Order, []int64{5, 4}) {
		t.Fatalf("switch %+v", sw)
	}
	// Plan 165 on the same round keeps A (10 < 30): no tunnel switch.
	bp := newBestPathManager()
	for i := 0; i < 3; i++ {
		if sws := bp.evaluate(plans, res, rbAllCapable, now.Add(time.Duration(i)*time.Minute)); len(sws) != 0 {
			t.Fatalf("tunnel decision must stay on A: %+v", sws)
		}
	}
}

func TestRuleBestMarginTwoRoundsAndFailover(t *testing.T) {
	key := "t.example:80"
	plan := rbPlan(8, [][]int64{{1}, {4, 5}})
	rbDest(&plan, key)
	plans := []pathLatencyPlan{plan}
	edges := map[[2]int64]float64{{1, 4}: 10, {1, 5}: 10}
	now := time.Unix(1_800_000_000, 0)
	m := newRuleBestManager()
	// 4 ms faster: never switches.
	for i := 0; i < 4; i++ {
		res := rbResults(plans, edges, map[string]map[int64]float64{key: {4: 24, 5: 20}})
		if p := rbEval(m, plans, res, now.Add(time.Duration(i)*time.Minute)); len(p) != 0 {
			t.Fatalf("4 ms gap switched in round %d: %+v", i, p)
		}
	}
	// ≥ 5 ms once, then gone, then twice: the streak resets in between.
	seq := []float64{25, 22, 25, 26}
	var pushes []ruleBestPush
	for i, a := range seq {
		res := rbResults(plans, edges, map[string]map[int64]float64{key: {4: a, 5: 20}})
		pushes = rbEval(m, plans, res, now.Add(time.Duration(10+i)*time.Minute))
		if i < 3 && len(pushes) != 0 {
			t.Fatalf("round %d switched early: %+v", i, pushes)
		}
	}
	if len(pushes) != 1 || pushes[0].Switches[0].ToID != 5 || pushes[0].Switches[0].Gain != 6 {
		t.Fatalf("second consecutive ≥5 ms round must switch: %+v", pushes)
	}
	rbApply(m, pushes, now.Add(14*time.Minute))
	// Current pick (5) can no longer reach the target, 4 can: at once.
	res := rbResults(plans, edges, map[string]map[int64]float64{key: {4: 200}})
	pushes = rbEval(m, plans, res, now.Add(15*time.Minute))
	if len(pushes) != 1 || !pushes[0].Failover || pushes[0].Switches[0].FromCost != -1 || pushes[0].Switches[0].ToID != 4 {
		t.Fatalf("failover not immediate: %+v", pushes)
	}
	// The hop to the current exit fails: also at once.
	rbApply(m, pushes, now.Add(15*time.Minute))
	res = rbResults(plans, map[[2]int64]float64{{1, 5}: 10}, map[string]map[int64]float64{key: {4: 1, 5: 30}})
	pushes = rbEval(m, plans, res, now.Add(16*time.Minute))
	if len(pushes) != 1 || !pushes[0].Failover || pushes[0].Switches[0].ToID != 5 {
		t.Fatalf("edge failure failover: %+v", pushes)
	}
}

// A target down from every exit: no switch, probed only at the retry points
// (round 2), the destination falls back to the tunnel order (its entry is
// removed once).
func TestRuleBestDeadTargetBacksOffAndUsesTunnelOrder(t *testing.T) {
	key := "dead.example:443"
	plan := rbPlan(9, [][]int64{{1}, {4, 5}})
	rbDest(&plan, key)
	plans := []pathLatencyPlan{plan}
	edges := map[[2]int64]float64{{1, 4}: 10, {1, 5}: 30}
	now := time.Unix(1_800_000_000, 0)
	m := newRuleBestManager()
	good := rbResults(plans, edges, map[string]map[int64]float64{key: {4: 80, 5: 20}})
	rbEval(m, plans, good, now)
	rbApply(m, rbEval(m, plans, good, now.Add(time.Minute)), now.Add(time.Minute))
	if o := m.ownerOrders(bestPathKey{9, 1}); !reflect.DeepEqual(o[key], []int64{5, 4}) {
		t.Fatalf("decision %v", o)
	}
	// Down everywhere: no switch to anything; the decision is dropped and
	// one cleanup push removes the entry.
	down := rbResults(plans, edges, nil)
	pushes := rbEval(m, plans, down, now.Add(2*time.Minute))
	if len(pushes) != 1 || !pushes[0].Cleanup || len(pushes[0].Switches) != 0 {
		t.Fatalf("dead target pushes %+v", pushes)
	}
	rbApply(m, pushes, now.Add(2*time.Minute))
	if o := m.ownerOrders(bestPathKey{9, 1}); len(o) != 0 {
		t.Fatalf("dead target kept a decision: %v", o)
	}
	// Round 2: the next probe is the +1 min retry point (nothing before it).
	dk := ruleBestDeadKey{TunnelID: 9, Dest: key}
	if m.probeDue(dk, now.Add(2*time.Minute+20*time.Second)) {
		t.Fatalf("dead target probed again before the +1 min point")
	}
	if !m.probeDue(dk, now.Add(3*time.Minute)) {
		t.Fatalf("dead target not probed at the +1 min point")
	}
	// Back-off rounds: the planner marks the dest; nothing is evaluated.
	plan.RuleBest.Dests[0].BackedOff = true
	plan.RuleBest.Dests[0].Exits = nil
	if p := rbEval(m, []pathLatencyPlan{plan}, rbResults([]pathLatencyPlan{plan}, edges, nil), now.Add(3*time.Minute)); len(p) != 0 {
		t.Fatalf("backed-off round pushed %+v", p)
	}
	if states, dead, _, _ := m.size(); states != 0 || dead != 1 {
		t.Fatalf("states=%d dead=%d", states, dead)
	}
	// Up again at a retry point: still the tunnel order (the answer is
	// confirmed by the next round), then the best decision at once — no
	// 2-round wait (round 2).
	revived := rbPlan(9, [][]int64{{1}, {4, 5}})
	rbDest(&revived, key)
	plans = []pathLatencyPlan{revived}
	good = rbResults(plans, edges, map[string]map[int64]float64{key: {4: 80, 5: 20}})
	if p := rbEval(m, plans, good, now.Add(3*time.Minute)); len(p) != 0 {
		t.Fatalf("revived target switched before the confirmation: %+v", p)
	}
	p := rbEval(m, plans, good, now.Add(4*time.Minute))
	if len(p) != 1 || p[0].Switches[0].ToID != 5 || !p[0].Switches[0].Recovered {
		t.Fatalf("confirmed target did not switch at once: %+v", p)
	}
	if _, dead, _, _ := m.size(); dead != 1 {
		t.Fatalf("schedule forgotten before 1 h healthy (dead=%d)", dead)
	}
}

// Rule (destination) removed, tunnel removed, owner without destOrder.
func TestRuleBestCleanupAndCapabilityGating(t *testing.T) {
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
	// The rule is deleted: destination gone ⇒ one cleanup push, then nothing.
	plan.RuleBest = &ruleBestPlan{}
	plans = []pathLatencyPlan{plan}
	pushes := rbEval(m, plans, rbResults(plans, edges, nil), now.Add(2*time.Minute))
	if len(pushes) != 1 || !pushes[0].Cleanup {
		t.Fatalf("cleanup push %+v", pushes)
	}
	rbApply(m, pushes, now)
	if p := rbEval(m, plans, rbResults(plans, edges, nil), now.Add(3*time.Minute)); len(p) != 0 {
		t.Fatalf("cleanup repeated: %+v", p)
	}
	// Decided again, then the tunnel disappears: states dropped, no push.
	rbDest(&plan, key)
	plans = []pathLatencyPlan{plan}
	rbEval(m, plans, res, now.Add(4*time.Minute))
	rbApply(m, rbEval(m, plans, res, now.Add(5*time.Minute)), now)
	if p := rbEval(m, nil, nil, now.Add(6*time.Minute)); len(p) != 0 {
		t.Fatalf("deleted tunnel pushed %+v", p)
	}
	if states, _, dirty, _ := m.size(); states != 0 || dirty != 0 {
		t.Fatalf("deleted tunnel left states=%d dirty=%d", states, dirty)
	}
	// Owner online without destOrder: no state, never a push.
	old := func(int64) (bool, bool) { return true, false }
	for i := 0; i < 3; i++ {
		dead, recovered, _ := m.updateDead(plans, res, now)
		if p := m.evaluate(plans, res, dead, recovered, rbNoTunnelOrder, old, now.Add(time.Duration(10+i)*time.Minute)); len(p) != 0 {
			t.Fatalf("old agent got a push: %+v", p)
		}
	}
	if states, _, _, _ := m.size(); states != 0 {
		t.Fatalf("old agent kept %d states", states)
	}
}

// Two targets of one rule: each its own decision; same-as-tunnel = no entry.
// Two rules with the same target share one decision.
func TestRuleBestMultiTargetAndSharedDestination(t *testing.T) {
	plan := rbPlan(11, [][]int64{{1}, {2, 3}, {4, 5}})
	rbDest(&plan, "a.example:443")
	rbDest(&plan, "b.example:443")
	plans := []pathLatencyPlan{plan}
	edges := map[[2]int64]float64{{1, 2}: 10, {1, 3}: 30, {2, 4}: 100, {2, 5}: 120, {3, 4}: 20, {3, 5}: 25}
	dests := map[string]map[int64]float64{"a.example:443": {4: 5, 5: 60}, "b.example:443": {4: 90, 5: 10}}
	res := rbResults(plans, edges, dests)
	now := time.Unix(1_800_000_000, 0)
	m := newRuleBestManager()
	tunnelOrder := func(k bestPathKey) []int64 {
		if k.OwnerNodeID == 1 {
			return []int64{3, 2} // plan 165 already picked 中转 3
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		dead, recovered, _ := m.updateDead(plans, res, now)
		pushes := m.evaluate(plans, res, dead, recovered, tunnelOrder, rbAllCapable, now.Add(time.Duration(i)*time.Minute))
		if i == 0 {
			continue
		}
		got := map[int64][]string{}
		for _, p := range pushes {
			for _, sw := range p.Switches {
				got[p.Key.OwnerNodeID] = append(got[p.Key.OwnerNodeID], fmt.Sprintf("%s:%d>%d", sw.Key.Dest, sw.FromID, sw.ToID))
			}
		}
		want := map[int64][]string{2: {"b.example:443:4>5"}, 3: {"b.example:443:4>5"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round 2 switches %v, want %v", got, want)
		}
	}
	if states, _, _, _ := m.size(); states != 6 { // 3 owners × 2 destinations
		t.Fatalf("states %d", states)
	}
	// Dedupe of the planner: one probe per (exit, target) — see the env test.
}

// ---- chain config: destOrder materialisation ----------------------------

func rbRuntimeTargets(ids ...int64) []tunnelRuntimeNode {
	out := make([]tunnelRuntimeNode, 0, len(ids))
	for _, id := range ids {
		out = append(out, tunnelRuntimeNode{NodeID: id, Port: 1000 + int(id), Strategy: "best", Protocol: "tls"})
	}
	return out
}

func TestRuleBestDestOrderMaterialisation(t *testing.T) {
	h := &Handler{bestPath: newBestPathManager(), ruleBest: newRuleBestManager()}
	h.chainSwapCapableFn = func(int64) bool { return true }
	h.destOrderCapableFn = func(int64) bool { return true }
	nodes := map[int64]*nodeRecord{}
	for id := int64(1); id <= 5; id++ {
		nodes[id] = &nodeRecord{ID: id, ServerIPv4: fmt.Sprintf("192.0.2.%d", id)}
	}
	state := &tunnelCreateState{TunnelID: 12, Type: 2, RuleBest: true, Nodes: nodes}
	h.ruleBest.states[ruleBestKey{12, 1, "b.example:443"}] = &ruleBestState{Layer: 1, Order: []int64{5, 4}, Candidates: []int64{4, 5}}
	h.ruleBest.states[ruleBestKey{12, 1, "a.example:443"}] = &ruleBestState{Layer: 1, Order: []int64{4, 5}, Candidates: []int64{4, 5}}
	h.ruleBest.states[ruleBestKey{12, 1, "c.example:443"}] = &ruleBestState{Layer: 1, Candidates: []int64{4, 5}} // follows the tunnel
	cfg, err := h.tunnelChainConfigFor(state, 1, rbRuntimeTargets(4, 5))
	if err != nil {
		t.Fatal(err)
	}
	hop := cfg["hops"].([]map[string]interface{})[0]
	got := hop["metadata"].(map[string]interface{})[destOrderMetaKey].(map[string][]string)
	want := map[string][]string{"b.example:443": {"192.0.2.5:1005", "192.0.2.4:1004"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("destOrder %v, want %v", got, want)
	}
	// The tunnel's own first place moves to 5: now only a.example differs.
	h.bestPath.states[bestPathKey{12, 1}] = &bestPathState{Layer: 1, Order: []int64{5, 4}}
	cfg, _ = h.tunnelChainConfigFor(state, 1, rbRuntimeTargets(4, 5))
	hop = cfg["hops"].([]map[string]interface{})[0]
	if addrs := chainHopAddrs(cfg); addrs[0] != "192.0.2.5:1005" {
		t.Fatalf("tunnel order not applied: %v", addrs)
	}
	got = hop["metadata"].(map[string]interface{})[destOrderMetaKey].(map[string][]string)
	want = map[string][]string{"a.example:443": {"192.0.2.4:1004", "192.0.2.5:1005"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after tunnel switch destOrder %v, want %v", got, want)
	}
	// Agent without destOrder, switch off, non-最优 layer: no metadata at all.
	for name, mutate := range map[string]func(){
		"old agent":  func() { h.destOrderCapableFn = func(int64) bool { return false } },
		"switch off": func() { state.RuleBest = false },
	} {
		h.destOrderCapableFn = func(int64) bool { return true }
		state.RuleBest = true
		mutate()
		cfg, _ = h.tunnelChainConfigFor(state, 1, rbRuntimeTargets(4, 5))
		if _, ok := cfg["hops"].([]map[string]interface{})[0]["metadata"]; ok {
			t.Fatalf("%s: metadata present: %v", name, cfg)
		}
	}
	state.RuleBest = true
	h.destOrderCapableFn = func(int64) bool { return true }
	fixed := []tunnelRuntimeNode{{NodeID: 4, Port: 1004, Strategy: "round"}, {NodeID: 5, Port: 1005, Strategy: "round"}}
	cfg, _ = h.tunnelChainConfigFor(state, 1, fixed)
	if _, ok := cfg["hops"].([]map[string]interface{})[0]["metadata"]; ok {
		t.Fatalf("non-最优 layer got destOrder")
	}
}

// Switch off (and on without a differing decision) = the chain config of
// fork.102 byte for byte, for every owner of every prod-shaped tunnel.
// Shapes: the 31 tunnel-type tunnels of prod on 2026-10-31 (node ids only;
// addresses are synthetic). 「*」 = best.
var rbProdShapes = []string{
	"2|42*,15*", "2|47*,15*", "2|42*,15*|12*,26*", "2|42*,15*|17", "2|42*,15*|18*,21*", "2|42*,15*|39",
	"2|42*,15*,18*,21*|17*,30*", "2|15", "23|18*,21*", "23|18*,21*|26*,12*", "23|18*,21*|15*,42*", "23|18*,21*|39",
	"23|18*,21*,26*,12*|45", "23|18*,21*|17*,30*", "32|42*,15*", "32|42*,15*|39", "32|42*,15*|12*,26*", "32|47*,15*",
	"32|42*,15*|18*,21*", "32|42*,15*,18*,21*|17*,30*", "32|42*,15*|17", "49|47", "52|47", "51|18*,21*", "51|18|26*,12*",
	"51|18|15", "51|18|39", "51|18,26*,12*|45", "51|21|45", "51|18*,21*|17*,30*", "32|15",
}

func rbParseShape(shape string) [][]tunnelRuntimeNode {
	var groups [][]tunnelRuntimeNode
	for _, part := range strings.Split(shape, "|") {
		var g []tunnelRuntimeNode
		for _, raw := range strings.Split(part, ",") {
			best := strings.HasSuffix(raw, "*")
			var id int64
			fmt.Sscanf(strings.TrimSuffix(raw, "*"), "%d", &id)
			strategy := "fifo"
			if best {
				strategy = "best"
			}
			g = append(g, tunnelRuntimeNode{NodeID: id, Port: 20000 + int(id), Strategy: strategy, Protocol: "tls"})
		}
		groups = append(groups, g)
	}
	return groups
}

func TestRuleBestSwitchOffChainConfigIdenticalToFork102(t *testing.T) {
	h := &Handler{bestPath: newBestPathManager(), ruleBest: newRuleBestManager()}
	h.chainSwapCapableFn = func(int64) bool { return true }
	h.destOrderCapableFn = func(int64) bool { return true }
	nodes := map[int64]*nodeRecord{}
	for id := int64(1); id <= 60; id++ {
		nodes[id] = &nodeRecord{ID: id, ServerIPv4: fmt.Sprintf("198.51.100.%d", id), ServerIPv6: fmt.Sprintf("2001:db8::%x", id)}
	}
	compared := 0
	for i, shape := range rbProdShapes {
		tid := int64(100 + i)
		groups := rbParseShape(shape)
		for g := 0; g+1 < len(groups); g++ {
			next := groups[g+1]
			for _, owner := range groups[g] {
				if isBestRuntimeLayer(next) {
					// A plan-165 decision (reverse order) and rule-level decisions
					// that differ from it: present in memory, must not leak.
					rev := make([]int64, 0, len(next))
					for j := len(next) - 1; j >= 0; j-- {
						rev = append(rev, next[j].NodeID)
					}
					h.bestPath.states[bestPathKey{tid, owner.NodeID}] = &bestPathState{Layer: g + 1, Order: rev}
					h.ruleBest.states[ruleBestKey{tid, owner.NodeID, "x.example:443"}] = &ruleBestState{Layer: g + 1, Order: []int64{next[0].NodeID}}
				}
				for _, pref := range []string{"", "v4", "v6"} {
					state := &tunnelCreateState{TunnelID: tid, Type: 2, IPPreference: pref, Nodes: nodes}
					// fork.102's tunnelChainConfigFor.
					want, err := buildTunnelChainConfig(tid, owner.NodeID, h.bestOrderedTargets(tid, owner.NodeID, next), nodes, pref)
					if err != nil {
						t.Fatal(err)
					}
					wantJSON, _ := json.Marshal(want)
					got, err := h.tunnelChainConfigFor(state, owner.NodeID, next)
					if err != nil {
						t.Fatal(err)
					}
					gotJSON, _ := json.Marshal(got)
					if string(gotJSON) != string(wantJSON) {
						t.Fatalf("shape %s owner %d pref %q: switch off differs\n got %s\nwant %s", shape, owner.NodeID, pref, gotJSON, wantJSON)
					}
					// Switch on, but the rule decision equals the hop's first: identical too.
					if isBestRuntimeLayer(next) {
						first := h.bestOrderedTargets(tid, owner.NodeID, next)[0].NodeID
						h.ruleBest.states[ruleBestKey{tid, owner.NodeID, "x.example:443"}].Order = []int64{first}
						state.RuleBest = true
						on, _ := h.tunnelChainConfigFor(state, owner.NodeID, next)
						onJSON, _ := json.Marshal(on)
						if string(onJSON) != string(wantJSON) {
							t.Fatalf("shape %s owner %d: same-as-tunnel decision changed the config\n%s", shape, owner.NodeID, onJSON)
						}
						h.ruleBest.states[ruleBestKey{tid, owner.NodeID, "x.example:443"}].Order = []int64{next[0].NodeID}
					}
					compared++
				}
			}
		}
	}
	if compared < 100 {
		t.Fatalf("only %d configs compared", compared)
	}
	t.Logf("compared %d chain configs (31 prod-shaped tunnels × owners × 3 IP preferences)", compared)
}

// ---- destinations ----------------------------------------------------------

func TestRuleBestDestinationsSkipAndDedupe(t *testing.T) {
	forwards := []rbForward{
		{1, 7, "Jp.Okonfu.com:6666", "gost", 1},
		{2, 7, "jp.okonfu.com:6666,[2001:DB8::9]:443", "", 1}, // same target + an IPv6 one
		{3, 7, "203.0.113.1:22", "nftables", 1},               // kernel: no chain on the entry
		{4, 7, "203.0.113.2:22", "wg_path", 1},
		{5, 7, "203.0.113.3:22", "gost", 0},  // paused
		{6, 7, "127.0.0.1:30001", "gost", 1}, // chain rule → its upstream
		{7, 7, "127.0.0.1:30002", "gost", 1}, // loopback without a chain: skipped
		{8, 7, "bad-target-without-port", "gost", 1},
	}
	dests, perForward := ruleBestDestinations(rbForwards(forwards), map[int64]string{6: "user-node.example:8443"})
	keys := make([]string, 0, len(dests))
	for _, d := range dests {
		keys = append(keys, d.Key+"="+d.Probe.String())
	}
	sort.Strings(keys)
	want := []string{"127.0.0.1:30001=user-node.example:8443", "[2001:db8::9]:443=[2001:DB8::9]:443", "jp.okonfu.com:6666=Jp.Okonfu.com:6666"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("dests %v, want %v", keys, want)
	}
	if len(perForward[1]) != 1 || len(perForward[2]) != 2 || perForward[3] != nil || perForward[7] != nil {
		t.Fatalf("per forward %v", perForward)
	}
}
