package oldpanel

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseAddrs(t *testing.T) {
	a := ParseAddrs("JP.Example.ORG.", "87.76.190.35", "[2403:18c0:5:429::1]", `["143.203.126.34","leikwan-cu.example.org","10.0.0.5"]`)
	if len(a.IPs) != 3 || a.IPs[0].String() != "87.76.190.35" || a.IPs[1].String() != "2403:18c0:5:429::1" || a.IPs[2].String() != "143.203.126.34" {
		t.Fatalf("ips %v", a.IPs)
	}
	if len(a.Hosts) != 2 || a.Hosts[0] != "jp.example.org" || a.Hosts[1] != "leikwan-cu.example.org" {
		t.Fatalf("hosts %v", a.Hosts)
	}
	// Private / CGNAT / loopback / link-local never identify a machine.
	if p := ParseAddrs("10.0.2.230", "100.64.1.1", "127.0.0.1", "fe80::1", "fd00::1", "192.168.1.1", "0.0.0.0", "::"); len(p.IPs) != 0 || len(p.Hosts) != 0 {
		t.Fatalf("private kept %+v", p)
	}
	if p := ParseAddrs("host.example.org:8443, 1.2.3.4:80", "not a host", "localhost"); len(p.Hosts) != 1 || p.Hosts[0] != "host.example.org" || len(p.IPs) != 1 || p.IPs[0].String() != "1.2.3.4" {
		t.Fatalf("ports / junk %+v", p)
	}
	if p := ParseAddrs("::ffff:8.8.8.8"); len(p.IPs) != 1 || !p.IPs[0].Is4() {
		t.Fatalf("mapped %+v", p)
	}
}

func lookupMap(m map[string][]string, pending ...string) Lookup {
	return func(h string) ([]netip.Addr, bool) {
		for _, p := range pending {
			if p == h {
				return nil, false
			}
		}
		var out []netip.Addr
		for _, s := range m[h] {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, true
	}
}

func TestMatch(t *testing.T) {
	flvx := []Side{
		{ID: 21, Addrs: ParseAddrs("87.76.190.35")},                          // Breadcloud JP
		{ID: 49, Addrs: ParseAddrs("dmitnathan.example.org")},                // same host name both sides
		{ID: 35, Addrs: ParseAddrs("us2.example.org")},                       // resolves to the old literal
		{ID: 51, Addrs: ParseAddrs("240e:96c:7100:1fe::e4", `["10.0.2.230"]`)}, // v6 literal
		{ID: 60, Addrs: ParseAddrs("proxied-a.example.org")},                 // Cloudflare-proxied name
		{ID: 61, Addrs: ParseAddrs("10.7.177.91")},                           // private only
		{ID: 70, Addrs: ParseAddrs("203.0.113.70")},
		{ID: 71, Addrs: ParseAddrs("203.0.113.70")}, // same IP twice: first in order wins
	}
	old := []Side{
		{ID: 13, Addrs: ParseAddrs("jp.example.org")},                         // → 21 via its A record
		{ID: 72, Addrs: ParseAddrs("DmitNathan.example.org")},                 // → 49
		{ID: 21, Addrs: ParseAddrs("143.20.196.111")},                         // → 35 (FLVX host resolves here)
		{ID: 73, Addrs: ParseAddrs("10.0.2.230", "", "240e:96c:7100:1fe::e4")}, // → 51
		{ID: 80, Addrs: ParseAddrs("proxied-b.example.org")},                  // same CF IP as 60: NO match
		{ID: 2, Addrs: ParseAddrs("10.7.177.91")},                             // private equal: NO match
		{ID: 1, Addrs: ParseAddrs("pqstw.example.org")},                       // unique
		{ID: 90, Addrs: ParseAddrs("203.0.113.70")},                           // → 70
		{ID: 91, Addrs: ParseAddrs("")},                                       // no address at all
	}
	dns := map[string][]string{
		"jp.example.org":         {"87.76.190.35"},
		"dmitnathan.example.org": {"154.17.1.1"},
		"us2.example.org":        {"143.20.196.111"},
		"proxied-a.example.org":  {"104.21.1.1", "2606:4700::1"},
		"proxied-b.example.org":  {"104.21.1.1", "2606:4700::1"},
		"pqstw.example.org":      {"45.1.2.3"},
	}
	matched, pending := Match(flvx, old, lookupMap(dns))
	want := map[int64]int64{13: 21, 72: 49, 21: 35, 73: 51, 90: 70}
	if len(matched) != len(want) {
		t.Fatalf("matched %v", matched)
	}
	for o, f := range want {
		if matched[o] != f {
			t.Fatalf("old %d → %d, want %d (all %v)", o, matched[o], f, matched)
		}
	}
	if len(pending) != 0 {
		t.Fatalf("pending %v", pending)
	}

	// While a name is resolving, unmatched machines that could still match
	// wait (no duplicate card flashes); literal matches are final at once.
	matched, pending = Match(flvx, old, lookupMap(dns, "jp.example.org", "us2.example.org"))
	if matched[73] != 51 || matched[90] != 70 || !pending[13] || !pending[21] || !pending[1] || pending[91] {
		t.Fatalf("pending phase: matched %v pending %v", matched, pending)
	}
	// A failed lookup is done: the machine is just unmatched.
	matched, pending = Match(flvx, old, lookupMap(map[string][]string{}))
	if _, ok := matched[13]; ok || pending[13] || matched[72] != 49 {
		t.Fatalf("failed lookups: %v %v", matched, pending)
	}
}

func TestDNSCache(t *testing.T) {
	var calls atomic.Int64
	now := time.Unix(1_800_000_000, 0)
	c := NewDNSCache()
	c.Now = func() time.Time { return now }
	c.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		calls.Add(1)
		switch host {
		case "a.example.org":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("::ffff:1.1.1.1")}, nil
		case "slow.example.org":
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, errors.New("nxdomain")
	}
	c.Timeout = 50 * time.Millisecond

	if _, done := c.Lookup("a.example.org"); done {
		t.Fatal("resolved before Want")
	}
	c.Want([]string{"a.example.org", "b.example.org", "a.example.org", "slow.example.org"})
	c.Want([]string{"a.example.org"}) // batch running: no second batch, but pruned
	c.Wait(2 * time.Second)
	ips, done := c.Lookup("a.example.org")
	if !done || len(ips) != 2 || ips[1].String() != "1.1.1.1" {
		t.Fatalf("a: %v %v (private dropped, mapped unmapped)", ips, done)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls %d", calls.Load())
	}
	c.Want([]string{"a.example.org", "b.example.org", "slow.example.org"})
	c.Wait(2 * time.Second)
	if ips, done := c.Lookup("b.example.org"); !done || len(ips) != 0 {
		t.Fatal("failure must be cached as done, no IPs")
	}
	if _, done := c.Lookup("slow.example.org"); !done {
		t.Fatal("timeout must be cached as done")
	}
	before := calls.Load()
	c.Want([]string{"a.example.org", "b.example.org", "slow.example.org"})
	c.Wait(time.Second)
	if calls.Load() != before {
		t.Fatal("fresh entries re-resolved")
	}
	now = now.Add(3 * time.Minute) // failures expire (2 min), successes not (10 min)
	c.Want([]string{"a.example.org", "b.example.org", "slow.example.org"})
	c.Wait(2 * time.Second)
	if calls.Load() != before+2 {
		t.Fatalf("after 3 min: %d calls, want %d", calls.Load(), before+2)
	}
	now = now.Add(10 * time.Minute)
	c.Want([]string{"b.example.org"}) // a + slow no longer wanted → forgotten
	c.Wait(2 * time.Second)
	if _, done := c.Lookup("a.example.org"); done {
		t.Fatal("unwanted name kept")
	}
}
