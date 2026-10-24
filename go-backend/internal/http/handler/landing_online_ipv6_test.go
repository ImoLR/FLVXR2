package handler

import (
	"fmt"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/repo"
)

// TestLandingOnlineIPsIPv6Prefix (plan 129): the Core merges IPv6 clients per
// /64 (native and shared alike) and reports "x::/64"; the list keeps those
// entries canonically and the count stays the Core's (one /64 = one IP).
func TestLandingOnlineIPsIPv6Prefix(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			tr := newLandingRateTracker(4)
			at := int64(1_700_000_000_000)
			c := nativeCounter(1, 1, 0, 0, 3)
			if shared {
				c = sharedCounter(1, at, 0, 0, 3)
			}
			ips := []landingcustom.OnlineIP{
				{IP: "2408:8748:a100:59::/64", Connections: 2},
				{IP: "2408:8748:A100:0059:0000::/64", Connections: 0}, // non-canonical spelling
				{IP: "198.51.100.7", Connections: 1},
				{IP: "2001:db8::1", Connections: 1},
				// Rejected: unmasked, IPv4 prefix, IPv4-mapped prefix, zone, /0, junk.
				{IP: "2408:8748:a100:59::1/64", Connections: 9},
				{IP: "198.51.100.0/24", Connections: 9},
				{IP: "::ffff:198.51.100.0/120", Connections: 9},
				{IP: "fe80::1%eth0", Connections: 9},
				{IP: "::/0", Connections: 9},
				{IP: "2408:8748:a100:59::/64x", Connections: 9},
			}
			tr.observe([]repo.LandingCounter{onlineIPCounter(c, 3, ips...)}, at)
			got := tr.onlineIPs(1, at)
			if got.OnlineIPs == nil || *got.OnlineIPs != 3 {
				t.Fatalf("count must stay the Core's: %+v", got.OnlineIPs)
			}
			want := []landingcustom.OnlineIP{
				{IP: "2408:8748:a100:59::/64", Connections: 2},
				{IP: "198.51.100.7", Connections: 1},
				{IP: "2001:db8::1", Connections: 1},
				{IP: "2408:8748:a100:59::/64", Connections: 0},
			}
			if len(got.IPs) != len(want) {
				t.Fatalf("list %+v", got.IPs)
			}
			for i := range want {
				if got.IPs[i] != want[i] {
					t.Fatalf("entry %d = %+v, want %+v (list %+v)", i, got.IPs[i], want[i], got.IPs)
				}
			}
		})
	}
	for raw, want := range map[string]string{"2408:8748:a100:59::/64": "2408:8748:a100:59::/64", "2001:db8::/48": "2001:db8::/48", "2001:db8::5/128": "2001:db8::5/128", "10.0.0.1": "10.0.0.1"} {
		if got, ok := onlineIPText(raw); !ok || got != want {
			t.Fatalf("%s → %q %v", raw, got, ok)
		}
	}
}
