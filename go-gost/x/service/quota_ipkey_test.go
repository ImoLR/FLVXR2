package service

import (
	"reflect"
	"sort"
	"testing"
)

func sortedIPs(ips ...string) []string {
	out := append([]string(nil), ips...)
	sort.Strings(out)
	return out
}

// Plan 231: one IPv6 /64 is one client IP (like the 妙妙屋 Core).
const (
	v6a1 = "2409:8a4c:5c4f:26e1:1111:2222:3333:4444"
	v6a2 = "2409:8a4c:5c4f:26e1:aaaa:bbbb:cccc:dddd"
	v6a3 = "2409:8a4c:5c4f:26e1::9"
	v6b1 = "2409:8a4c:5c4f:26e2::1" // another /64
	v6a  = "2409:8a4c:5c4f:26e1::/64"
)

func TestRuleClientIPLimitCountsIPv6Slash64(t *testing.T) {
	s := &defaultService{}
	s.SetMaxClientIPs(1)
	r1 := mustAcquire(t, s, v6a1)
	r2 := mustAcquire(t, s, v6a2)
	r3 := mustAcquire(t, s, v6a3) // three addresses in one /64 = one slot
	if release, reason, _, ok := s.acquireConnection(v6b1); ok {
		release()
		t.Fatal("second /64 admitted with a 1-IP rule limit")
	} else if reason != "rule client IPs" {
		t.Fatalf("reason %q", reason)
	}
	if got := s.ClientIPCounts(); !reflect.DeepEqual(got, map[string]int{v6a: 3}) {
		t.Fatalf("ClientIPCounts = %v", got)
	}
	r1()
	r2()
	if release, _, _, ok := s.acquireConnection(v6b1); ok {
		release()
		t.Fatal("/64 slot freed before its last connection closed")
	}
	r3()
	mustAcquire(t, s, v6b1)()
	if len(s.ClientIPCounts()) != 0 {
		t.Fatalf("counts not empty: %v", s.ClientIPCounts())
	}
}

func TestRuleClientIPLimitIPv4AndMappedUnchanged(t *testing.T) {
	s := &defaultService{}
	s.SetMaxClientIPs(2)
	r1 := mustAcquire(t, s, "192.0.2.1")
	r2 := mustAcquire(t, s, "::ffff:192.0.2.1") // mapped = the same IPv4
	r3 := mustAcquire(t, s, "192.0.2.2")
	if release, _, _, ok := s.acquireConnection("192.0.2.3"); ok {
		release()
		t.Fatal("third IPv4 admitted with a 2-IP limit")
	}
	if got := s.ClientIPCounts(); !reflect.DeepEqual(got, map[string]int{"192.0.2.1": 2, "192.0.2.2": 1}) {
		t.Fatalf("ClientIPCounts = %v", got)
	}
	r1()
	r2()
	r3()
}

func TestQuotaGroupCountsIPv6Slash64AndReportsRaw(t *testing.T) {
	const group = "test-ipkey-group"
	s := &defaultService{}
	s.SetQuotaGroup(group, UnlimitedQuota, 2)
	r1 := mustAcquire(t, s, v6a1)
	r2 := mustAcquire(t, s, v6a2)
	r3 := mustAcquire(t, s, v6a3)
	r4 := mustAcquire(t, s, "192.0.2.10")
	// 3 addresses in one /64 + 1 IPv4 = 2 slots: a 2nd /64 is a 3rd IP.
	if release, reason, _, ok := s.acquireConnection(v6b1); ok {
		release()
		t.Fatal("2nd /64 admitted at a 2-IP group budget")
	} else if reason != "group client IPs" {
		t.Fatalf("reason %q", reason)
	}
	state, _ := GetQuotaGroupState(group)
	if state.CountedIPs != 2 || !reflect.DeepEqual(state.ClientIPs, map[string]struct{}{v6a: {}, "192.0.2.10": {}}) {
		t.Fatalf("state = %+v", state)
	}
	// The panel still gets the raw addresses (old-agent shared lists, rollback).
	if got := findQuotaUsage(t, group).ClientIPs; !reflect.DeepEqual(got, sortedIPs("192.0.2.10", v6a3, v6a1, v6a2)) {
		t.Fatalf("reported = %v", got)
	}
	r1()
	r3()
	if got := findQuotaUsage(t, group).ClientIPs; !reflect.DeepEqual(got, sortedIPs("192.0.2.10", v6a2)) {
		t.Fatalf("reported after release = %v", got)
	}
	r2()
	r4()
	g := s.quotaGroup
	g.mu.Lock()
	leftover := len(g.clientIPs) + len(g.addrs)
	g.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("group maps not empty after all releases: %d", leftover)
	}
}

func TestQuotaGroupSharedIPsKeyedRawAndPrefix(t *testing.T) {
	const group = "test-ipkey-shared"
	s := &defaultService{}
	s.SetQuotaGroup(group, UnlimitedQuota, 0)
	// An old panel sends raw addresses, a new one "<net>/64" keys: both cover the /64.
	for _, shared := range [][]string{{v6a1}, {v6a}} {
		if err := SetQuotaGroupBudgets([]QuotaGroupBudget{{Group: group, MaxConnections: -1, MaxClientIPs: 0, SharedIPs: shared}}); err != nil {
			t.Fatal(err)
		}
		r := mustAcquire(t, s, v6a2) // another address in the shared /64: no slot used
		if release, _, _, ok := s.acquireConnection(v6b1); ok {
			release()
			t.Fatalf("shared %v admitted a different /64 at budget 0", shared)
		}
		r()
	}
	if err := SetQuotaGroupBudgets([]QuotaGroupBudget{{Group: group, MaxConnections: -1, MaxClientIPs: 0, SharedIPs: []string{"::ffff:192.0.2.5"}}}); err != nil {
		t.Fatal(err)
	}
	mustAcquire(t, s, "192.0.2.5")()
}

func TestNftUsageKeysMergeWithGost(t *testing.T) {
	const group = "test-ipkey-nft"
	s := &defaultService{}
	s.SetQuotaGroup(group, UnlimitedQuota, 1)
	handle := AttachNftQuotaGroup(group, UnlimitedQuota, 1)
	defer handle.Detach()
	SetNftQuotaGroupUsage(group, 2, map[string]int{v6a: 2}, map[string]int{v6a1: 1, v6a3: 1})
	defer SetNftQuotaGroupUsage(group, 0, nil, nil)
	// gost: another address of the same /64 is not a new IP.
	r := mustAcquire(t, s, v6a2)
	if release, _, _, ok := s.acquireConnection("192.0.2.1"); ok {
		release()
		t.Fatal("new IPv4 admitted while the /64 fills the 1-IP budget")
	}
	state, _ := GetQuotaGroupState(group)
	if state.CountedIPs != 1 || state.Connections != 3 {
		t.Fatalf("state = %+v", state)
	}
	if got := findQuotaUsage(t, group).ClientIPs; !reflect.DeepEqual(got, sortedIPs(v6a3, v6a1, v6a2)) {
		t.Fatalf("reported = %v", got)
	}
	r()
}
