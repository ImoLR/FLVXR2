package repo

import (
	"testing"
	"time"
)

// Plan 231: landings count an IPv6 source per /64, so a relay node whose IPv6
// rotates inside one /64 keeps one history entry (its newest address) and
// needs one relay slot, not one per rotation.
func TestEgressHistoryOneIPv6PerSlash64(t *testing.T) {
	now := time.Unix(1_793_980_000, 0)
	raw := ""
	for i, ip := range []string{"240e:b8f:737:1204::a", "240e:b8f:737:1204::b", "240e:b8f:737:1204::c", "240e:b8f:737:1205::1"} {
		raw, _ = mergeEgressHistory(raw, []string{ip, "114.35.6.24"}, now.Add(time.Duration(i)*time.Minute))
	}
	got := map[string]bool{}
	for _, e := range parseEgressHistory(raw) {
		got[e.IP] = true
	}
	want := map[string]bool{"240e:b8f:737:1204::c": true, "240e:b8f:737:1205::1": true, "114.35.6.24": true}
	if len(got) != len(want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for ip := range want {
		if !got[ip] {
			t.Fatalf("history = %v, want %v", got, want)
		}
	}
	if s := relaySlotsFor([]string{"2001:db8::1", "2001:db8::2", "2001:db8:0:1::1", "192.0.2.1"}); s != 2 {
		t.Fatalf("relay slots = %d, want 2 (two /64s)", s)
	}
	if s := relaySlotsFor([]string{"2001:db8::1", "2001:db8::2", "2001:db8::3"}); s != 1 {
		t.Fatalf("relay slots = %d, want 1 (one /64)", s)
	}
}
