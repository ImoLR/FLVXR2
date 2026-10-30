package landing

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Plan 161: desired-state validation and the derived kernel state.

func TestUserRatesValidate(t *testing.T) {
	n1, n2 := testNode(t, 1), testNode(t, 2)
	n1.RateGroup = "landing-user-7"
	ok := Desired{Revision: 1, Enabled: true, Nodes: []Node{n1, n2}, UserRates: &UserRates{Version: 1, Groups: []RateGroup{{Group: "landing-user-7", RateMbps: 20}}}}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(d *Desired){
		func(d *Desired) { d.UserRates.Version = 2 },
		func(d *Desired) { d.UserRates.Groups[0].RateMbps = 0 },
		func(d *Desired) { d.UserRates.Groups[0].RateMbps = maxRateMbps + 1 },
		func(d *Desired) { d.UserRates.Groups[0].Group = "user-7" },
		func(d *Desired) { d.UserRates.Groups[0].Group = "landing-user-07" },
		func(d *Desired) {
			d.UserRates.Groups = append(d.UserRates.Groups, RateGroup{Group: "landing-user-7", RateMbps: 5})
		},
		func(d *Desired) { d.Nodes[0].RateGroup = "landing-user-8" }, // not in groups
		func(d *Desired) { d.UserRates = nil },                       // node tagged without rates
		func(d *Desired) { d.Nodes[0].Listen = ChainListen },         // never on a loopback node
	}
	for i, mut := range bad {
		raw, _ := json.Marshal(ok)
		var d Desired
		_ = json.Unmarshal(raw, &d)
		mut(&d)
		if err := d.validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestUserRatesWant(t *testing.T) {
	ss := func(id int64, port int, group string) Node {
		n := testNode(t, id)
		n.Port, n.RateGroup = port, group
		return n
	}
	vless := ss(4, 31004, "landing-user-7")
	vless.Protocol, vless.SS2022 = "vless_reality_vision", nil
	disabled := ss(5, 31005, "landing-user-7")
	disabled.Enabled = false
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{
		ss(1, 31001, "landing-user-7"), ss(2, 31002, "landing-user-7"), ss(3, 31003, ""), vless, disabled,
		ss(6, 31006, "landing-user-5000"), ss(7, 31007, "landing-user-4095"), ss(8, 31008, "landing-user-9999"),
	}, UserRates: &UserRates{Version: 1, Groups: []RateGroup{
		{Group: "landing-user-7", RateMbps: 20}, {Group: "landing-user-5000", RateMbps: 30},
		{Group: "landing-user-4095", RateMbps: 40}, {Group: "landing-user-9999", RateMbps: 50},
		{Group: "landing-user-12", RateMbps: 60}, // no node here: no class
	}}}
	w := d.rateWant()
	if len(w.groups) != 4 {
		t.Fatalf("groups %+v", w.groups)
	}
	g7 := w.groups[0]
	if g7.group != "landing-user-7" || g7.slot != 7 || g7.bytesPerSec != 20*RateBytesPerMbps ||
		len(g7.tcp) != 3 || g7.tcp[2] != 31004 || len(g7.udp) != 2 || g7.udp[1] != 31002 {
		t.Fatalf("user 7 %+v (VLESS = TCP only, disabled node skipped)", g7)
	}
	slots := map[string]uint16{}
	for _, g := range w.groups {
		slots[g.group] = g.slot
	}
	// Ids above the slot range get the highest free slots, in id order.
	if slots["landing-user-4095"] != 4095 || slots["landing-user-5000"] != 4094 || slots["landing-user-9999"] != 4093 {
		t.Fatalf("slots %v", slots)
	}
	if w.ports() != 3+2+2+2+2 {
		t.Fatalf("ports %d", w.ports())
	}
	d.Enabled = false
	if d.rateWant().active() {
		t.Fatal("a disabled server shapes nothing")
	}
}

type fakeRateEngine struct {
	calls []rateWant
	state string // "error" = the kernel refused (plan 168 tests)
}

func (f *fakeRateEngine) release() RateStatus {
	f.calls = append(f.calls, rateWant{})
	return RateStatus{State: "off"}
}

func (f *fakeRateEngine) ensure(want rateWant, _ time.Time) RateStatus {
	f.calls = append(f.calls, want)
	if !want.active() {
		return RateStatus{State: "off"}
	}
	if f.state == "error" {
		return RateStatus{State: "error", Error: "内核限速失败：test", Groups: len(want.groups), Ports: want.ports()}
	}
	return RateStatus{State: "on", Groups: len(want.groups), Ports: want.ports()}
}

// Status always carries the rate state (the panel tells capable agents from
// older ones by it); applyRates runs without a desired state too (cleanup).
func TestUserRatesStatusAndApply(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	fake := &fakeRateEngine{}
	m.rates = fake
	m.mu.Lock()
	m.applyRates()
	st := m.statusLocked()
	m.mu.Unlock()
	if len(fake.calls) != 1 || fake.calls[0].active() || st.UserRate == nil || st.UserRate.State != "off" {
		t.Fatalf("no desired state: calls %d status %+v", len(fake.calls), st.UserRate)
	}
	n := testNode(t, 1)
	n.RateGroup = "landing-user-3"
	m.mu.Lock()
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{n}, UserRates: &UserRates{Version: 1, Groups: []RateGroup{{Group: "landing-user-3", RateMbps: 10}}}}
	m.loaded = true
	m.applyRates()
	st = m.statusLocked()
	m.mu.Unlock()
	if st.UserRate.State != "on" || st.UserRate.Groups != 1 || st.UserRate.Ports != 2 {
		t.Fatalf("status %+v", st.UserRate)
	}
	raw, _ := json.Marshal(st)
	if !strings.Contains(string(raw), `"user_rate":{"state":"on","groups":1,"ports":2}`) {
		t.Fatalf("wire %s", raw)
	}
}

// A graceful agent stop removes the kernel objects and no later tick (or
// Apply arriving while stopping) builds them again.
func TestReleaseRatesOnStop(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	fake := &fakeRateEngine{}
	m.rates = fake
	n := testNode(t, 1)
	n.RateGroup = "landing-user-3"
	m.mu.Lock()
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{n}, UserRates: &UserRates{Version: 1, Groups: []RateGroup{{Group: "landing-user-3", RateMbps: 10}}}}
	m.loaded = true
	m.applyRates()
	m.mu.Unlock()
	m.ReleaseRates(time.Second)
	if len(fake.calls) != 2 || fake.calls[1].active() {
		t.Fatalf("release did not remove: %d calls", len(fake.calls))
	}
	m.mu.Lock()
	m.applyRates()
	m.mu.Unlock()
	if len(fake.calls) != 2 {
		t.Fatal("rates rebuilt while stopping")
	}
	// A reconcile holding the lock longer than the timeout: release gives up.
	m2 := NewManager(t.TempDir(), true)
	m2.rates = &fakeRateEngine{}
	m2.mu.Lock()
	start := time.Now()
	m2.ReleaseRates(100 * time.Millisecond)
	m2.mu.Unlock()
	if time.Since(start) > time.Second || m2.ratesReleased {
		t.Fatal("release must not wait for a long reconcile")
	}
}
