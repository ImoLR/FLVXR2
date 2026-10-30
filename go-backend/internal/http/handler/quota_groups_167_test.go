package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/repo"
)

// Plan 167 — push policy tests (fake clock, no background reconciles).

type fakeQuotaClock struct{ t time.Time }

func (f *fakeQuotaClock) now() time.Time      { return f.t }
func (f *fakeQuotaClock) add(d time.Duration) { f.t = f.t.Add(d) }
func newFakeQuotaClock() *fakeQuotaClock      { return &fakeQuotaClock{t: time.Unix(1_800_000_000, 0)} }

// recordingSender keeps the last pushed budgets and the push count per node.
type recordingSender struct {
	mu     sync.Mutex
	pushes map[int64]int
	last   map[int64][]quotaGroupBudget
}

func newRecordingSender() *recordingSender {
	return &recordingSender{pushes: map[int64]int{}, last: map[int64][]quotaGroupBudget{}}
}

func (r *recordingSender) send(nodeID int64, budgets []quotaGroupBudget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushes[nodeID]++
	r.last[nodeID] = append([]quotaGroupBudget(nil), budgets...)
	return nil
}

func (r *recordingSender) budget(t *testing.T, nodeID int64, group string) quotaGroupBudget {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.last[nodeID] {
		if b.Group == group {
			return b
		}
	}
	t.Fatalf("node %d never received %s: %+v", nodeID, group, r.last[nodeID])
	return quotaGroupBudget{}
}

func (r *recordingSender) count(nodeID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pushes[nodeID]
}

// newTestCoordinator builds a coordinator whose Trigger never starts a
// background reconcile (running = true): the test drives reconcileOnce.
func newTestCoordinator(source quotaTargetSource, sender *recordingSender, clock *fakeQuotaClock) *quotaCoordinator {
	c := newQuotaCoordinator(source, sender.send)
	c.now = clock.now
	c.running = true
	return c
}

func setQuotaUsage(c *quotaCoordinator, nodeID, userID int64, connections int, ips ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.usageByNode[nodeID] == nil {
		c.usageByNode[nodeID] = map[quotaUsageKey]quotaNodeUsage{}
	}
	c.usageByNode[nodeID][quotaUsageKey{UserID: userID}] = quotaUsage(connections, ips...)
}

func limitedTargets(userID int64, maxConns, maxIPs int, nodeIDs ...int64) staticQuotaTargets {
	var targets []repo.QuotaGroupTarget
	for _, nodeID := range nodeIDs {
		targets = append(targets, repo.QuotaGroupTarget{UserID: userID, NodeID: nodeID, MaxConnections: maxConns, MaxClientIps: maxIPs, NodeStatus: 1, NodeVersion: "3.0.27-fork.97"})
	}
	return staticQuotaTargets{targets: targets}
}

func TestQuotaLimitUrgency(t *testing.T) {
	cases := []struct {
		name                string
		sent, current, used int
		want                quotaPushUrgency
	}{
		{"same", 300, 300, 10, quotaPushNone},
		{"unlimited to limited", -1, 300, 10, quotaPushNow},
		{"limited to unlimited", 300, -1, 10, quotaPushNow},
		{"small tighten far from the limit waits", 310, 300, 10, quotaPushLater},
		{"tighten by a quarter of the room is urgent", 300, 232, 10, quotaPushNow},
		{"tighten to the limit is urgent", 12, 10, 10, quotaPushNow},
		{"over the limit is urgent", 5, 0, 7, quotaPushNow},
		{"tighten near the limit by one is urgent", 13, 12, 9, quotaPushNow},
		{"loosen while refusing is urgent", 10, 30, 10, quotaPushNow},
		{"loosen beyond the band is soon", 100, 140, 10, quotaPushSoon},
		{"small loosen far from the limit waits", 300, 310, 10, quotaPushLater},
		{"ip tighten by one with small room is urgent", 3, 2, 0, quotaPushNow},
	}
	for _, tc := range cases {
		slack := quotaPushConnSlack
		if strings.HasPrefix(tc.name, "ip") {
			slack = quotaPushIPSlack
		}
		if got := quotaLimitUrgency(tc.sent, tc.current, tc.used, slack); got != tc.want {
			t.Errorf("%s: quotaLimitUrgency(%d, %d, %d) = %d, want %d", tc.name, tc.sent, tc.current, tc.used, got, tc.want)
		}
	}
}

func TestQuotaPushClassStructureAndSharedIPs(t *testing.T) {
	base := []quotaGroupBudget{{Group: "user-8", MaxConnections: 300, MaxClientIps: 3, SharedIps: []string{"a", "b"}}}
	usage := map[string]quotaBudgetUsage{"user-8": {Connections: 5, ClientIps: 0}}
	clone := func(mut func(*quotaGroupBudget)) []quotaGroupBudget {
		b := base[0]
		b.SharedIps = append([]string(nil), base[0].SharedIps...)
		mut(&b)
		return []quotaGroupBudget{b}
	}
	if got := quotaPushClass(base, append(clone(func(*quotaGroupBudget) {}), quotaGroupBudget{Group: "user-9", MaxConnections: 5, MaxClientIps: -1}), usage); got != quotaPushNow {
		t.Fatalf("added group = %d, want now", got)
	}
	if got := quotaPushClass(base, []quotaGroupBudget{{Group: "user-9", MaxConnections: 300, MaxClientIps: 3}}, usage); got != quotaPushNow {
		t.Fatalf("replaced group = %d, want now", got)
	}
	// An IP left elsewhere and the IP budget grew by one: neutral, may wait.
	if got := quotaPushClass(base, clone(func(b *quotaGroupBudget) { b.SharedIps = []string{"a"}; b.MaxClientIps = 4 }), usage); got != quotaPushSoon {
		t.Fatalf("compensated removal = %d, want soon", got)
	}
	// An IP left the list without a larger budget: it would stay free here.
	if got := quotaPushClass(base, clone(func(b *quotaGroupBudget) { b.SharedIps = []string{"a", "c"} }), usage); got != quotaPushNow {
		t.Fatalf("swapped shared IP = %d, want now", got)
	}
	// A new IP elsewhere also costs a slot here: the budget tightens → now.
	if got := quotaPushClass(base, clone(func(b *quotaGroupBudget) { b.SharedIps = []string{"a", "b", "c"}; b.MaxClientIps = 2 }), usage); got != quotaPushNow {
		t.Fatalf("new IP elsewhere = %d, want now", got)
	}
	server := 20
	landing := []quotaGroupBudget{{Group: "landing-user-8", MaxConnections: 50, MaxClientIps: -1, ServerMaxConnections: &server, RelayIps: 1}}
	moreRelays := []quotaGroupBudget{{Group: "landing-user-8", MaxConnections: 50, MaxClientIps: -1, ServerMaxConnections: &server, RelayIps: 2}}
	if got := quotaPushClass(landing, moreRelays, nil); got != quotaPushNow {
		t.Fatalf("relay IPs change = %d, want now", got)
	}
	noServer := []quotaGroupBudget{{Group: "landing-user-8", MaxConnections: 50, MaxClientIps: -1, RelayIps: 1}}
	if got := quotaPushClass(landing, noServer, nil); got != quotaPushNow {
		t.Fatalf("server cap removed = %d, want now", got)
	}
	tighterServer := 2
	serverAtLimit := []quotaGroupBudget{{Group: "landing-user-8", MaxConnections: 50, MaxClientIps: -1, ServerMaxConnections: &tighterServer, RelayIps: 1}}
	if got := quotaPushClass(landing, serverAtLimit, map[string]quotaBudgetUsage{"landing-user-8": {Connections: 1, Server: 2}}); got != quotaPushNow {
		t.Fatalf("server cap at the limit = %d, want now", got)
	}
}

// The old bound: a user who reaches the total on other nodes gets 0 on this
// node in the very next reconcile (≤ quotaGroupPushInterval after the last
// push), and at the limit every ±1 move is pushed at once.
func TestQuotaCoordinatorOverLimitUserBlockedWithinOldBound(t *testing.T) {
	clock := newFakeQuotaClock()
	sender := newRecordingSender()
	c := newTestCoordinator(limitedTargets(9, 10, 0, 1, 2), sender, clock)

	setQuotaUsage(c, 1, 9, 3, "198.51.100.1")
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-9"); b.MaxConnections != 7 {
		t.Fatalf("initial node 2 budget = %d, want 7", b.MaxConnections)
	}
	// Far from the limit a one-connection move waits.
	clock.add(time.Second)
	setQuotaUsage(c, 1, 9, 4, "198.51.100.1")
	c.reconcileOnce()
	if n := sender.count(2); n != 1 {
		t.Fatalf("small move far from the limit pushed (%d pushes), want it deferred", n)
	}
	// The user fills the total on node 1: node 2 must refuse at once.
	clock.add(time.Second)
	setQuotaUsage(c, 1, 9, 10, "198.51.100.1")
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-9"); b.MaxConnections != 0 {
		t.Fatalf("node 2 budget after the user hit the limit = %d, want 0 at once", b.MaxConnections)
	}
	// Node 2 itself is at its (now zero) budget; node 1 keeps 10 room for its own 10.
	if b := sender.budget(t, 1, "user-9"); b.MaxConnections != 10 {
		t.Fatalf("node 1 budget = %d, want 10", b.MaxConnections)
	}
	// At the limit every move is urgent (pushed within the old 1 s interval).
	for i, used := range []int{9, 10, 8, 10, 9} {
		clock.add(time.Second)
		setQuotaUsage(c, 1, 9, used, "198.51.100.1")
		c.reconcileOnce()
		if b := sender.budget(t, 2, "user-9"); b.MaxConnections != 10-used {
			t.Fatalf("step %d: node 2 budget = %d, want %d pushed at once", i, b.MaxConnections, 10-used)
		}
	}
	// Over the limit (reports lag behind): still 0.
	clock.add(time.Second)
	setQuotaUsage(c, 1, 9, 12, "198.51.100.1")
	setQuotaUsage(c, 2, 9, 1, "198.51.100.2")
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-9"); b.MaxConnections != 0 {
		t.Fatalf("over-limit node 2 budget = %d, want 0", b.MaxConnections)
	}
	// The pushes of one second never exceed one per node.
	if n := sender.count(2); n > 9 {
		t.Fatalf("node 2 pushes = %d, want at most one per reconcile", n)
	}
}

// A user with a 5-IP total gets a new IP elsewhere while this node is at its
// IP budget: the tighter IP budget goes out at once.
func TestQuotaCoordinatorIPLimitTightensAtOnce(t *testing.T) {
	clock := newFakeQuotaClock()
	sender := newRecordingSender()
	c := newTestCoordinator(limitedTargets(8, 400, 3, 1, 2), sender, clock)
	setQuotaUsage(c, 1, 8, 10, "10.0.0.1")
	setQuotaUsage(c, 2, 8, 2, "10.0.0.2")
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-8"); b.MaxClientIps != 2 || len(b.SharedIps) != 1 {
		t.Fatalf("initial node 2 budget = %+v, want 2 IPs + 1 shared", b)
	}
	clock.add(time.Second)
	setQuotaUsage(c, 1, 8, 11, "10.0.0.1", "10.0.0.3")
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-8"); b.MaxClientIps != 1 || len(b.SharedIps) != 2 {
		t.Fatalf("node 2 budget after a new IP elsewhere = %+v, want 1 IP + 2 shared at once", b)
	}
}

// Prod node 32 pattern (10-30): user 8 (400 conns / 5 IPs) sits at ~80–115
// connections on node 23 and 0–15 on node 48, moving every second; node 32
// received one SetQuotaGroups per changed second. The new policy must cut the
// steady-state pushes by ≥ 90 % and still block the user once the total is
// reached.
func TestQuotaCoordinatorSteadyChurnCutsPushes(t *testing.T) {
	clock := newFakeQuotaClock()
	sender := newRecordingSender()
	c := newTestCoordinator(limitedTargets(8, 400, 5, 23, 32, 48), sender, clock)

	seed := uint32(167)
	next := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>16) % n }
	conns23, conns48 := 95, 5
	oldPushes := map[int64]int{}
	lastJSON := map[int64]string{}
	const hour = 3600
	for sec := 0; sec < hour; sec++ {
		conns23 = min(max(conns23+next(9)-4, 80), 115)
		conns48 = min(max(conns48+next(5)-2, 0), 15)
		ips48 := []string{}
		if conns48 > 0 {
			ips48 = []string{"203.0.113.9"}
		}
		setQuotaUsage(c, 23, 8, conns23, "203.0.113.7", "203.0.113.8")
		setQuotaUsage(c, 48, 8, conns48, ips48...)
		// Old policy: every changed plan was pushed (1 s apart).
		c.mu.Lock()
		usage := cloneQuotaUsage(c.usageByNode)
		c.mu.Unlock()
		plan := computeQuotaPlan(limitedTargets(8, 400, 5, 23, 32, 48).targets, repo.QuotaPlanExtras{}, usage, nil)
		for nodeID, budgets := range plan.Budgets {
			encoded, _ := json.Marshal(budgets)
			if lastJSON[nodeID] != string(encoded) {
				lastJSON[nodeID] = string(encoded)
				oldPushes[nodeID]++
			}
		}
		c.reconcileOnce()
		// Safety: whatever node 32 holds never exceeds its true budget by a
		// quarter of the room or more (the excess it could over-admit).
		truth := budgetForUser(t, plan, 32, 8).MaxConnections
		held := sender.budget(t, 32, "user-8").MaxConnections
		if room := truth; held-truth > 0 && (held-truth)*quotaPushBandDivisor >= room {
			t.Fatalf("second %d: node 32 holds %d, truth %d (room %d): excess not bounded", sec, held, truth, room)
		}
		clock.add(time.Second)
	}
	for _, nodeID := range []int64{32, 48} {
		before, after := oldPushes[nodeID], sender.count(nodeID)
		t.Logf("node %d: old policy %d pushes/h, new %d pushes/h (%.1f %% fewer)", nodeID, before, after, 100*float64(before-after)/float64(before))
		if before < 1000 {
			t.Fatalf("node %d: simulation too calm (%d old pushes)", nodeID, before)
		}
		if after*10 > before {
			t.Fatalf("node %d: %d pushes/h vs %d before, want ≥ 90 %% fewer", nodeID, after, before)
		}
	}

	// The user then ramps up on node 23 until the total is reached: node 32
	// must hold the exact (zero) budget as soon as the panel sees it.
	for conns23 < 400-conns48 {
		conns23 = min(conns23+25, 400-conns48)
		setQuotaUsage(c, 23, 8, conns23, "203.0.113.7", "203.0.113.8")
		c.reconcileOnce()
		truth := 400 - conns23 - conns48
		held := sender.budget(t, 32, "user-8").MaxConnections
		if held-truth > 0 && ((held-truth)*quotaPushBandDivisor >= truth || truth <= quotaPushConnSlack) {
			t.Fatalf("ramp at %d: node 32 holds %d, truth %d", conns23, held, truth)
		}
		clock.add(time.Second)
	}
	if held := sender.budget(t, 32, "user-8").MaxConnections; held != 0 {
		t.Fatalf("node 32 budget at the total = %d, want 0", held)
	}
}

// Limit edits (InvalidateInputs) are pushed at once even when they loosen far
// from the limit; deferred classes go out after their interval.
func TestQuotaCoordinatorLimitEditAtOnceAndDeferredDelivery(t *testing.T) {
	clock := newFakeQuotaClock()
	sender := newRecordingSender()
	source := &extrasQuotaTargets{
		targets: []repo.QuotaGroupTarget{forwardTarget(5, 1), forwardTarget(5, 2)},
		extras:  repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{5: {MaxConnections: 100}}},
	}
	c := newTestCoordinator(source, sender, clock)
	setQuotaUsage(c, 1, 5, 10)
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-5"); b.MaxConnections != 90 {
		t.Fatalf("initial budget = %d, want 90", b.MaxConnections)
	}

	// Admin raises the limit: pushed in the next reconcile, not after 15 s.
	source.mu.Lock()
	source.extras = repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{5: {MaxConnections: 200}}}
	source.mu.Unlock()
	clock.add(time.Second)
	c.InvalidateInputs()
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-5"); b.MaxConnections != 190 {
		t.Fatalf("budget after the limit edit = %d, want 190 at once", b.MaxConnections)
	}

	// Loosening beyond the band (node 1 drops 10 → 0 and node 2 used 150):
	// waits quotaPushSoonInterval.
	setQuotaUsage(c, 2, 5, 150)
	clock.add(time.Second)
	c.reconcileOnce() // node 1's budget tightens 200-0 → 50: urgent for node 1
	pushes2 := sender.count(2)
	setQuotaUsage(c, 1, 5, 0)
	clock.add(time.Second)
	c.reconcileOnce()
	if sender.count(2) != pushes2 {
		t.Fatalf("loosening pushed before %s", quotaPushSoonInterval)
	}
	clock.add(quotaPushSoonInterval)
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-5"); b.MaxConnections != 200 {
		t.Fatalf("loosened budget after %s = %d, want 200", quotaPushSoonInterval, b.MaxConnections)
	}

	// A tiny drift is refreshed within quotaPushRefreshInterval.
	setQuotaUsage(c, 1, 5, 2)
	clock.add(time.Second)
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-5"); b.MaxConnections != 200 {
		t.Fatalf("tiny drift pushed at once (%d)", b.MaxConnections)
	}
	clock.add(quotaPushRefreshInterval)
	c.reconcileOnce()
	if b := sender.budget(t, 2, "user-5"); b.MaxConnections != 198 {
		t.Fatalf("tiny drift after %s = %d, want 198", quotaPushRefreshInterval, b.MaxConnections)
	}

	// A node reconnect re-sends at once.
	c.mu.Lock()
	delete(c.lastSent, 2)
	delete(c.sentBudgets, 2)
	delete(c.lastPush, 2)
	c.mu.Unlock()
	pushes2 = sender.count(2)
	c.reconcileOnce()
	if sender.count(2) != pushes2+1 {
		t.Fatal("budgets not re-sent after the node's state was reset")
	}
}

// One summary line per hour, only when pushes happened.
func TestQuotaCoordinatorPushStatsLine(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) }()

	clock := newFakeQuotaClock()
	sender := newRecordingSender()
	c := newTestCoordinator(limitedTargets(9, 10, 0, 1, 2), sender, clock)
	setQuotaUsage(c, 1, 9, 3)
	c.reconcileOnce()
	clock.add(30 * time.Minute)
	c.reconcileOnce()
	if buf.Len() != 0 {
		t.Fatalf("stats logged before an hour: %q", buf.String())
	}
	clock.add(31 * time.Minute)
	c.reconcileOnce()
	if out := buf.String(); strings.Count(out, "\n") != 1 || !strings.Contains(out, "2 SetQuotaGroups pushes") || !strings.Contains(out, "now 2") {
		t.Fatalf("stats line = %q", out)
	}
	buf.Reset()
	clock.add(2 * time.Hour)
	c.reconcileOnce()
	if buf.Len() != 0 {
		t.Fatalf("idle hour logged: %q", buf.String())
	}
}
