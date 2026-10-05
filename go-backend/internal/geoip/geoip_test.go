package geoip

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestLookup(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"8.8.8.8", "US"},
		{"114.114.114.114", "CN"},
		{"168.95.1.1", "TW"},
		{"2400:3200::1", "CN"},
		{"2001:b000:168::1", "TW"},
		{"::ffff:8.8.8.8", "US"},
		{"10.0.0.1", ""}, {"172.16.0.1", ""}, {"192.168.1.1", ""},
		{"127.0.0.1", ""}, {"100.64.0.1", ""}, {"100.127.255.254", ""},
		{"169.254.1.1", ""}, {"0.0.0.0", ""}, {"255.255.255.255", ""},
		{"224.0.0.1", ""}, {"192.0.2.1", ""}, {"198.18.0.1", ""},
		{"::1", ""}, {"::", ""}, {"fc00::1", ""}, {"fe80::1", ""},
		{"2001:db8::1", ""}, {"ff02::1", ""},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := Lookup(netip.MustParseAddr(tc.ip)); got != tc.want {
				t.Errorf("Lookup(%q) = %q, want %q", tc.ip, got, tc.want)
			}
		})
	}
	if got := Lookup(netip.Addr{}); got != "" {
		t.Fatalf("invalid IP returned %q", got)
	}
}

type fakeResolver struct {
	ips []netip.Addr
	err error
	t   *testing.T
}

func (r fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 3*time.Second {
		r.t.Fatal("DNS lookup must have a deadline of at most 3 seconds")
	}
	return r.ips, r.err
}

func TestDetectDomain(t *testing.T) {
	r := fakeResolver{t: t, ips: []netip.Addr{
		netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("100.64.0.1"),
		netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("168.95.1.1"),
	}}
	if got := detect(" node.example ", r); got != "US" {
		t.Fatalf("first public DNS result: got %q", got)
	}
	r.err = errors.New("DNS failed")
	if got := detect("unresolvable.invalid", r); got != "" {
		t.Fatalf("failed DNS returned %q", got)
	}
	r.err = nil
	r.ips = r.ips[:2]
	if got := detect("private.example", r); got != "" {
		t.Fatalf("private DNS returned %q", got)
	}
	if got := DetectNodeRegion("this-node-does-not-exist.invalid"); got != "" {
		t.Fatalf("unresolvable domain returned %q", got)
	}
}

func TestDetectNodeAddresses(t *testing.T) {
	for _, tc := range []struct{ v4, general, v6, want string }{
		{"168.95.1.1", "8.8.8.8", "2001:4860:4860::8888", "TW"},
		{"192.168.1.1", "8.8.8.8", "2001:4860:4860::8888", "US"},
		{"", "", "2001:b000:168::1", "TW"},
		{"127.0.0.1", "127.0.0.1", "::1", ""},
	} {
		if got := DetectNodeAddresses(tc.v4, tc.general, tc.v6); got != tc.want {
			t.Errorf("DetectNodeAddresses(%q, %q, %q) = %q, want %q", tc.v4, tc.general, tc.v6, got, tc.want)
		}
	}
}

func TestEmbeddedDatabaseValid(t *testing.T) {
	loadOnce.Do(load)
	if len(v4Table) == 0 || len(v6Table) == 0 {
		t.Fatal("both IPv4 and IPv6 country tables must load")
	}
	if len(countryData) > 10_000_000 {
		t.Fatal("embedded data exceeds binary growth budget")
	}
	if _, _, err := decode([]byte("bad data")); err == nil {
		t.Fatal("invalid data accepted")
	}
}
