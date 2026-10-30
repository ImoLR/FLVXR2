//go:build linux

package landing

import (
	"fmt"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

// Plan 168: the entry tree on the real kernel (throw-away netns, root only):
// inner user classes with a default leaf + children, top-level rule / arena
// leaves, landing next to entry in ONE set of objects (landing handles and
// filters unchanged), hot rate change, a rule moving between classes (old
// class drains, then goes), group removal, restart → one set, the probe, and
// 500 limited ports (count + apply time).

type classView struct {
	parent     uint32
	rate, ceil uint64 // bytes/s as the kernel reports
}

func (r *rateNS) classTree(ifb string) map[uint32]classView {
	r.t.Helper()
	l, err := r.h.LinkByName(ifb)
	if err != nil {
		r.t.Fatal(err)
	}
	cs, err := r.h.ClassList(l, netlink.MakeHandle(rateHTBMajor, 0))
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[uint32]classView{}
	for _, c := range cs {
		if h, ok := c.(*netlink.HtbClass); ok && h.Handle>>16 == rateHTBMajor {
			out[h.Handle] = classView{parent: h.Parent, rate: h.Rate, ceil: h.Ceil}
		}
	}
	return out
}

func entryWant(t *testing.T, r *EntryRates, landing rateWant, slots *entrySlots, now time.Time) rateWant {
	t.Helper()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	w := landing
	w.entry = buildEntryShape(r, landing, slots, now)
	return w
}

func TestEntryRateKernelNetns(t *testing.T) {
	r := newRateNS(t)
	r.dummy("eth0", "198.51.100.10/24", "2001:db8:168::10/64")
	e := newRateEngineAt(r.h)
	var slots entrySlots
	now := time.Unix(1_800_000_000, 0)
	// landing user 7 on port 31001 (plan 161) next to the entry tree
	landing := rateTestWant(rateGroupWant{group: "landing-user-7", bytesPerSec: 30 * RateBytesPerMbps, tcp: []uint16{31001}, udp: []uint16{31001}})
	tree := entryTestTree()
	want := entryWant(t, &tree, landing, &slots, now)
	if st := e.ensure(want, now); st.State != "on" {
		t.Fatalf("ensure: %+v", st)
	}
	up, okUp := r.ifb(rateIfbUp)
	dn, okDn := r.ifb(rateIfbDown)
	if !okUp || !okDn {
		t.Fatal("IFBs missing")
	}
	root := uint32(netlink.HANDLE_ROOT) // the kernel reports top-level classes so
	for _, ifb := range []string{rateIfbUp, rateIfbDown} {
		got := r.classTree(ifb)
		expect := map[uint32]classView{
			rateClassHandle(7):    {root, rateWire(30*RateBytesPerMbps) / 8, rateWire(30*RateBytesPerMbps) / 8}, // landing, unchanged
			rateClassHandle(2050): {root, mbpsWire(20) / 8, mbpsWire(20) / 8},                                   // user group
			rateClassHandle(2051): {rateClassHandle(2050), mbpsWire(20) / 2 / 8, mbpsWire(20) / 8},              // its default leaf
			rateClassHandle(2052): {rateClassHandle(2050), mbpsWire(5) / 8, mbpsWire(5) / 8},                    // rule 5 under the user
			rateClassHandle(2049): {root, mbpsWire(10) / 8, mbpsWire(10) / 8},                                   // rule 10
			rateClassHandle(2048): {root, mbpsWire(100) / 8, mbpsWire(100) / 8},                                 // arena
		}
		if len(got) != len(expect) {
			t.Fatalf("%s classes %v", ifb, got)
		}
		for h, c := range expect {
			if got[h] != c {
				t.Errorf("%s class %x = %+v, want %+v", ifb, h, got[h], c)
			}
		}
		leaves := r.leaves(ifb)
		for _, h := range []uint32{rateClassHandle(7), rateClassHandle(2051), rateClassHandle(2052), rateClassHandle(2049), rateClassHandle(2048)} {
			if leaves[h] != "fq_codel" {
				t.Errorf("%s leaf of %x = %q", ifb, h, leaves[h])
			}
		}
		if _, ok := leaves[rateClassHandle(2050)]; ok {
			t.Errorf("%s: the inner user class has a leaf qdisc", ifb)
		}
	}
	// filters: landing 31001 tcp+udp (8) + entry 28 = 36, one set, right targets
	fs := r.ours("eth0")
	if len(fs) != 36 {
		t.Fatalf("filters %d", len(fs))
	}
	for _, f := range fs {
		if (f.egress && f.mirredTo != dn) || (!f.egress && f.mirredTo != up) {
			t.Fatalf("filter %+v redirects to %d", f, f.mirredTo)
		}
	}
	check := func(port uint16, udp, egress, v6 bool, slot uint16) {
		t.Helper()
		byHandle := map[[2]uint32]rateFilterView{}
		for _, f := range r.ours("eth0") {
			key := [2]uint32{f.handle, uint32(f.pref)}
			if f.egress {
				key[1] |= 1 << 31
			}
			byHandle[key] = f
		}
		k := rateKey{port: port, udp: udp, egress: egress, v6: v6}
		pref := uint32(ratePrefV4)
		if v6 {
			pref = ratePrefV6
		}
		key := [2]uint32{k.handle(), pref}
		if egress {
			key[1] |= 1 << 31
		}
		f, ok := byHandle[key]
		if !ok || f.priority != rateClassHandle(slot) {
			t.Fatalf("filter %+v: %+v (ok %v), want class %x", k, f, ok, rateClassHandle(slot))
		}
	}
	check(31001, true, true, true, 7)
	check(10001, false, false, false, 2051)
	check(10001, true, true, true, 2051)
	check(10002, false, true, false, 2052)
	check(10003, true, false, true, 2049)
	check(10004, false, true, false, 2048)
	check(10005, false, false, true, 2048)
	// steady state: no change
	if st := e.ensure(want, now.Add(10*time.Second)); st.State != "on" || len(r.ours("eth0")) != 36 {
		t.Fatalf("steady: %+v", st)
	}
	// hot change: user 20 → 40, rule 5 → 8 (in place, leaves kept)
	tree.Classes[0].RateMbps, tree.Classes[1].RateMbps = 40, 8
	want = entryWant(t, &tree, landing, &slots, now.Add(20*time.Second))
	if st := e.ensure(want, now.Add(20*time.Second)); st.State != "on" {
		t.Fatalf("change: %+v", st)
	}
	got := r.classTree(rateIfbDown)
	if got[rateClassHandle(2050)].ceil != mbpsWire(40)/8 || got[rateClassHandle(2052)].ceil != mbpsWire(8)/8 || got[rateClassHandle(2051)].rate != mbpsWire(40)/2/8 {
		t.Fatalf("after change %v", got)
	}
	// the rule's own limit goes: its port moves to the user's default leaf at
	// once, the old class drains (unlimited ceil under the user) and goes
	tree.Classes = append(tree.Classes[:1], tree.Classes[2:]...)
	tree.Ports[1].Class = "u7"
	t0 := now.Add(30 * time.Second)
	want = entryWant(t, &tree, landing, &slots, t0)
	if st := e.ensure(want, t0); st.State != "on" {
		t.Fatalf("move: %+v", st)
	}
	check(10002, false, true, false, 2051)
	if c, ok := r.classTree(rateIfbUp)[rateClassHandle(2052)]; !ok || c.ceil != rateUnlimited/8 || c.parent != rateClassHandle(2050) {
		t.Fatalf("released child %+v %v", c, ok)
	}
	if st := e.ensure(want, t0.Add(10*time.Second)); st.State != "on" {
		t.Fatalf("drain: %+v", st)
	}
	if _, ok := r.classTree(rateIfbUp)[rateClassHandle(2052)]; ok {
		t.Fatal("released child not removed after the drain")
	}
	if c := r.classTree(rateIfbUp)[rateClassHandle(2051)]; c.rate != mbpsWire(40)/8 {
		t.Fatalf("default leaf share with 1 child: %+v", c)
	}
	// crash loop / restart: exactly one set
	for i := 0; i < 3; i++ {
		e = newRateEngineAt(r.h)
		var fresh entrySlots
		w := entryWant(t, &tree, landing, &fresh, t0.Add(time.Duration(20+i)*time.Second))
		if st := e.ensure(w, t0.Add(time.Duration(20+i)*time.Second)); st.State != "on" {
			t.Fatalf("restart %d: %+v", i, st)
		}
		slots = fresh
	}
	links, _ := r.h.LinkList()
	ifbs := 0
	for _, l := range links {
		if l.Type() == "ifb" {
			ifbs++
		}
	}
	if ifbs != 2 || len(r.ours("eth0")) != 36 {
		t.Fatalf("after restarts: %d IFBs, %d filters", ifbs, len(r.ours("eth0")))
	}
	// the whole user group goes (row removed): released, children first, then
	// the group; landing untouched, IFBs stay for landing
	tree.Classes = tree.Classes[1:]
	tree.Ports = tree.Ports[2:]
	t1 := t0.Add(time.Minute)
	want = entryWant(t, &tree, landing, &slots, t1)
	e.ensure(want, t1)
	if c := r.classTree(rateIfbDown)[rateClassHandle(2050)]; c.ceil != rateUnlimited/8 {
		t.Fatalf("released group %+v", c)
	}
	// one tick later: children and the group are gone in the same pass
	e.ensure(want, t1.Add(10*time.Second))
	got = r.classTree(rateIfbDown)
	if _, ok := got[rateClassHandle(2050)]; ok || len(got) != 3 {
		t.Fatalf("group not cleaned up: %v", got)
	}
	if got[rateClassHandle(7)].ceil != rateWire(30*RateBytesPerMbps)/8 || len(r.ours("eth0")) != 8+16 {
		t.Fatalf("landing / remaining entry changed: %v %d", got, len(r.ours("eth0")))
	}
	// everything goes: entry first, then landing → nothing left
	want = entryWant(t, &EntryRates{}, rateWant{}, &slots, t1.Add(time.Minute))
	for i := 0; i < 4; i++ {
		e.ensure(want, t1.Add(time.Minute+time.Duration(i)*10*time.Second))
	}
	if _, ok := r.ifb(rateIfbUp); ok || len(r.ours("eth0")) != 0 || r.hasClsact("eth0") {
		t.Fatal("objects left after the last limit")
	}
}

func TestEntryRateProbeNetns(t *testing.T) {
	r := newRateNS(t)
	if reason := probeEntryRateWith(r.h); reason != "" {
		t.Fatalf("probe: %s", reason)
	}
	if _, err := r.h.LinkByName(rateProbeDev); err == nil {
		t.Fatal("probe device left behind")
	}
}

// 500 limited rules (each tcp+udp) under 50 users: filter count, apply time,
// steady-state cost of a tick.
func TestEntryRateKernelScaleNetns(t *testing.T) {
	r := newRateNS(t)
	r.dummy("eth0", "198.51.100.10/24", "2001:db8:168::10/64")
	e := newRateEngineAt(r.h)
	tree := EntryRates{Version: 1}
	for u := 1; u <= 50; u++ {
		tree.Classes = append(tree.Classes, EntryRateClass{ID: fmt.Sprintf("u%d", u), Group: true, RateMbps: 20})
	}
	for i := 0; i < 500; i++ {
		class := fmt.Sprintf("u%d", i%50+1)
		if i%5 == 0 {
			id := fmt.Sprintf("r%d", i)
			tree.Classes = append(tree.Classes, EntryRateClass{ID: id, Parent: class, RateMbps: 5})
			class = id
		}
		tree.Ports = append(tree.Ports, EntryRatePort{Port: 20000 + i, TCP: true, UDP: true, Class: class})
	}
	var slots entrySlots
	now := time.Unix(1_800_000_000, 0)
	want := entryWant(t, &tree, rateWant{}, &slots, now)
	start := time.Now()
	if st := e.ensure(want, now); st.State != "on" {
		t.Fatalf("ensure: %+v", st)
	}
	first := time.Since(start)
	if n := len(r.ours("eth0")); n != 500*2*4 {
		t.Fatalf("filters %d", n)
	}
	if n := len(r.classTree(rateIfbUp)); n != 50*2+100 {
		t.Fatalf("classes %d", n)
	}
	start = time.Now()
	e.ensure(want, now.Add(10*time.Second))
	tick := time.Since(start)
	start = time.Now()
	e.ensure(want, now.Add(70*time.Second)) // full verify
	full := time.Since(start)
	// one rate change of one user: one class change per IFB + its children
	tree.Classes[0].RateMbps = 30
	want = entryWant(t, &tree, rateWant{}, &slots, now.Add(80*time.Second))
	start = time.Now()
	e.ensure(want, now.Add(80*time.Second))
	change := time.Since(start)
	t.Logf("500 ports / 4000 filters / 200 classes: first apply %v, 10 s tick %v, 60 s full verify %v, one user rate change %v", first, tick, full, change)
	e.release()
	if _, ok := r.ifb(rateIfbUp); ok || len(r.ours("eth0")) != 0 {
		t.Fatal("objects left after release")
	}
}
