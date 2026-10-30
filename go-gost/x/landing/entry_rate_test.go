package landing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Plan 168: entry rate tree — validation, mapping onto HTB classes / filters
// next to the landing state, stable slots, the Manager's apply / persist /
// status (kernel side: entry_rate_kernel_test.go).

func entryTestTree() EntryRates {
	return EntryRates{Version: 1,
		Classes: []EntryRateClass{
			{ID: "u7", Group: true, RateMbps: 20},
			{ID: "r11", Parent: "u7", RateMbps: 5},
			{ID: "r12", RateMbps: 10},
			{ID: "a3", RateMbps: 100},
		},
		Ports: []EntryRatePort{
			{Port: 10001, TCP: true, UDP: true, Class: "u7"},
			{Port: 10002, TCP: true, Class: "r11"},
			{Port: 10003, UDP: true, Class: "r12"},
			{Port: 10004, TCP: true, UDP: true, Class: "a3"},
			{Port: 10005, TCP: true, Class: "a3"},
		}}
}

func TestEntryRatesValidate(t *testing.T) {
	if err := entryTestTree().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (EntryRates{}).Validate(); err != nil {
		t.Fatalf("empty tree must be valid (= remove all): %v", err)
	}
	bad := map[string]func(*EntryRates){
		"version":         func(r *EntryRates) { r.Version = 2 },
		"rate 0":          func(r *EntryRates) { r.Classes[2].RateMbps = 0 },
		"rate too high":   func(r *EntryRates) { r.Classes[2].RateMbps = maxRateMbps + 1 },
		"bad id":          func(r *EntryRates) { r.Classes[2].ID = "R 12" },
		"duplicate id":    func(r *EntryRates) { r.Classes[3].ID = "r12" },
		"parent no group": func(r *EntryRates) { r.Classes[1].Parent = "r12" },
		"parent missing":  func(r *EntryRates) { r.Classes[1].Parent = "u9" },
		"nested group":    func(r *EntryRates) { r.Classes[0].Parent = "a3" },
		"port 0":          func(r *EntryRates) { r.Ports[0].Port = 0 },
		"port twice":      func(r *EntryRates) { r.Ports[1].Port = 10001 },
		"no protocol":     func(r *EntryRates) { r.Ports[1].TCP = false },
		"unknown class":   func(r *EntryRates) { r.Ports[1].Class = "r99" },
	}
	for name, mutate := range bad {
		r := entryTestTree()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func shapeBySlot(s *entryShape) map[uint16]shapeClass {
	out := map[uint16]shapeClass{}
	for _, c := range s.classes {
		out[c.slot] = c
	}
	return out
}

func TestEntryShapeTree(t *testing.T) {
	r := entryTestTree()
	var slots entrySlots
	now := time.Unix(1_800_000_000, 0)
	s := buildEntryShape(&r, rateWant{}, &slots, now)
	if !s.active() || len(s.errs) != 0 || s.nClasses != 4 || s.nPorts != 5 {
		t.Fatalf("shape: %+v", s)
	}
	// slots from 2048 up in key order: ">a3", ">r12", ">u7", "u7>/", "u7>r11"
	want := map[string]uint16{">a3": 2048, ">r12": 2049, ">u7": 2050, "u7>/": 2051, "u7>r11": 2052}
	for k, slot := range want {
		if slots.byKey[k] != slot {
			t.Fatalf("slot %s = %d, want %d (%v)", k, slots.byKey[k], slot, slots.byKey)
		}
	}
	by := shapeBySlot(s)
	u20, r5, r10, a100 := mbpsWire(20), mbpsWire(5), mbpsWire(10), mbpsWire(100)
	expect := map[uint16]shapeClass{
		2050: {slot: 2050, rate: u20, ceil: u20},                               // user group (inner)
		2051: {slot: 2051, parent: 2050, rate: u20 / 2, ceil: u20, leaf: true}, // default leaf: share = 20/2
		2052: {slot: 2052, parent: 2050, rate: r5, ceil: r5, leaf: true},       // own 5 under the user (share 10 > 5)
		2049: {slot: 2049, rate: r10, ceil: r10, leaf: true},                   // rule without user rate
		2048: {slot: 2048, rate: a100, ceil: a100, leaf: true},                 // arena
	}
	if len(by) != len(expect) {
		t.Fatalf("classes %v", by)
	}
	for slot, c := range expect {
		if by[slot] != c {
			t.Errorf("class %d = %+v, want %+v", slot, by[slot], c)
		}
	}
	// parents before children
	if s.classes[len(s.classes)-1].parent == 0 && s.classes[0].parent != 0 {
		t.Fatalf("order: %+v", s.classes)
	}
	// filters: 10001 tcp+udp ×4, 10002 tcp ×4, 10003 udp ×4, 10004 ×8, 10005 ×4
	if len(s.filters) != 8+4+4+8+4 {
		t.Fatalf("filters %d", len(s.filters))
	}
	checks := map[rateKey]uint16{
		{port: 10001}:                         2051, // group port → default leaf
		{port: 10001, udp: true, v6: true}:    2051,
		{port: 10002, egress: true}:           2052,
		{port: 10003, udp: true}:              2049,
		{port: 10005, v6: true, egress: true}: 2048,
	}
	for k, slot := range checks {
		if s.filters[k] != slot {
			t.Errorf("filter %+v → %d, want %d", k, s.filters[k], slot)
		}
	}
	if _, ok := s.filters[rateKey{port: 10002, udp: true}]; ok {
		t.Fatal("tcp-only port got a udp filter")
	}
	// own rate above the user's: capped at the user's
	r.Classes[1].RateMbps = 50
	s = buildEntryShape(&r, rateWant{}, &slots, now)
	if c := shapeBySlot(s)[2052]; c.ceil != u20 || c.rate != u20/2 {
		t.Fatalf("capped child %+v", c)
	}
}

func TestEntryShapeSlotsStableAndLandingFirst(t *testing.T) {
	r := entryTestTree()
	var slots entrySlots
	now := time.Unix(1_800_000_000, 0)
	buildEntryShape(&r, rateWant{}, &slots, now)
	before := map[string]uint16{}
	for k, v := range slots.byKey {
		before[k] = v
	}
	// a new rule class: existing slots stay
	r.Classes = append(r.Classes, EntryRateClass{ID: "r13", RateMbps: 7})
	r.Ports = append(r.Ports, EntryRatePort{Port: 10006, TCP: true, Class: "r13"})
	s := buildEntryShape(&r, rateWant{}, &slots, now.Add(time.Second))
	for k, v := range before {
		if slots.byKey[k] != v {
			t.Fatalf("slot of %s moved %d → %d", k, v, slots.byKey[k])
		}
	}
	if slots.byKey[">r13"] != 2053 {
		t.Fatalf("new slot %d", slots.byKey[">r13"])
	}
	// the rule's own limit goes: its old class is freed and not reused for
	// the reuse period; a reparent (r13 under u7) is a new identity
	r.Classes[4].Parent = "u7"
	s = buildEntryShape(&r, rateWant{}, &slots, now.Add(2*time.Second))
	if _, ok := slots.byKey[">r13"]; ok || slots.byKey["u7>r13"] != 2054 {
		t.Fatalf("reparent: %v", slots.byKey)
	}
	if _, ok := slots.freed[2053]; !ok {
		t.Fatal("old slot not held as freed")
	}
	if c := shapeBySlot(s)[2051]; c.rate != mbpsWire(20)/3 {
		t.Fatalf("shares not recomputed for 3 children: %+v", c)
	}
	// landing: user 2049 takes slot 2049 (plan-161 rule: user id) and port
	// 10003; the entry class moves, the clashing port is reported
	landing := rateWant{groups: []rateGroupWant{{group: "landing-user-2049", slot: 2049, bytesPerSec: 30 * RateBytesPerMbps, udp: []uint16{10003}, tcp: []uint16{10003}}}}
	s = buildEntryShape(&r, landing, &slots, now.Add(3*time.Second))
	if slots.byKey[">r12"] == 2049 || slots.byKey[">r12"] == 0 {
		t.Fatalf("entry kept the landing slot: %v", slots.byKey)
	}
	for _, c := range s.classes {
		if c.slot == 2049 {
			t.Fatal("entry class on a landing slot")
		}
	}
	if len(s.errs) != 1 || !strings.Contains(s.errs[0], "10003") {
		t.Fatalf("clash not reported: %v", s.errs)
	}
	for k := range s.filters {
		if k.port == 10003 {
			t.Fatal("entry filter on a landing port")
		}
	}
	filters, classes := rateWant{groups: landing.groups, entry: s}.shape()
	if classes[2049].ceil != rateWire(30*RateBytesPerMbps) || filters[rateKey{port: 10003, udp: true}] != 2049 {
		t.Fatal("landing not first in the merged shape")
	}
	// after the reuse period the freed slot can be handed out again
	r.Classes = append(r.Classes, EntryRateClass{ID: "r14", RateMbps: 3})
	buildEntryShape(&r, rateWant{}, &slots, now.Add(3*time.Second+entrySlotReuse))
	if slots.byKey[">r14"] != 2049 && slots.byKey[">r14"] != 2053 {
		t.Fatalf("freed slot not reused after the reuse period: %v", slots.byKey)
	}
	// empty tree frees everything
	if s := buildEntryShape(&EntryRates{}, rateWant{}, &slots, now.Add(time.Hour)); s != nil || len(slots.byKey) != 0 {
		t.Fatalf("empty tree: %+v %v", s, slots.byKey)
	}
}

func TestEntryRatesApplyPersistStatus(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false)
	fake := &fakeRateEngine{}
	m.rates = fake
	r := entryTestTree()
	r.Rev = "rev-1"
	st, err := m.ApplyEntryRates(r)
	if err != nil || st.State != "on" || st.Classes != 4 || st.Ports != 5 {
		t.Fatalf("apply: %+v %v", st, err)
	}
	if len(fake.calls) != 1 || fake.calls[0].entry == nil || len(fake.calls[0].groups) != 0 {
		t.Fatalf("engine calls %+v", fake.calls)
	}
	if m.EntryRatesRev() != "rev-1" {
		t.Fatalf("rev %q", m.EntryRatesRev())
	}
	if rs := m.Status().UserRate; rs.State != "off" {
		t.Fatalf("landing status must stay off with entry limits only: %+v", rs)
	}
	st1, seq1 := m.EntryRateReport()
	if st1.State != "on" || seq1 == 0 {
		t.Fatalf("report %+v %d", st1, seq1)
	}
	if _, err := os.Stat(filepath.Join(dir, entryRatesFile)); err != nil {
		t.Fatal(err)
	}
	// a new process loads it from the file before its first tick
	m2 := NewManager(dir, false)
	fake2 := &fakeRateEngine{}
	m2.rates = fake2
	m2.mu.Lock()
	m2.applyRates()
	m2.mu.Unlock()
	if len(fake2.calls) != 1 || fake2.calls[0].entry == nil || m2.EntryRatesRev() != "rev-1" {
		t.Fatalf("reload: %+v rev %q", fake2.calls, m2.EntryRatesRev())
	}
	// same status → same seq; an engine error → error state, new seq
	m.mu.Lock()
	m.applyRates()
	m.mu.Unlock()
	if _, seq := m.EntryRateReport(); seq != seq1 {
		t.Fatalf("seq moved without a change: %d → %d", seq1, seq)
	}
	fake.state = "error"
	m.mu.Lock()
	m.applyRates()
	m.mu.Unlock()
	st2, seq2 := m.EntryRateReport()
	if st2.State != "error" || st2.Error == "" || seq2 == seq1 {
		t.Fatalf("error report %+v %d", st2, seq2)
	}
	fake.state = ""
	// invalid payload: rejected, nothing changes
	bad := entryTestTree()
	bad.Ports[0].Class = "nope"
	if _, err := m.ApplyEntryRates(bad); err == nil {
		t.Fatal("invalid tree accepted")
	}
	if m.EntryRatesRev() != "rev-1" {
		t.Fatal("invalid tree replaced the applied one")
	}
	// empty = remove: file gone, status off, no revision
	if st, err := m.ApplyEntryRates(EntryRates{}); err != nil || st.State != "off" {
		t.Fatalf("remove: %+v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, entryRatesFile)); !os.IsNotExist(err) {
		t.Fatal("file kept")
	}
	if m.EntryRatesRev() != "" {
		t.Fatal("revision kept")
	}
	// stopping: further applies are refused
	m.ReleaseRates(time.Second)
	if _, err := m.ApplyEntryRates(r); err == nil {
		t.Fatal("apply after release accepted")
	}
}
