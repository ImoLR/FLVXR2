package cfnet

import "testing"

func TestContainsEdges(t *testing.T) {
	cases := map[string]bool{
		// IPv4 edges of 104.16.0.0/13 (104.16.0.0 – 104.23.255.255)
		"104.16.0.0":      true,
		"104.23.255.255":  true,
		"104.15.255.255":  false,
		"104.24.0.0":      true, // next range 104.24.0.0/14
		"104.27.255.255":  true,
		"104.28.0.0":      false,
		"162.158.0.0":     true,
		"162.159.255.255": true,
		"162.160.0.0":     false,
		"172.64.0.1":      true,
		"172.71.255.255":  true,
		"172.72.0.0":      false,
		"173.245.63.255":  true,
		"173.245.64.0":    false,
		"131.0.75.255":    true,
		"131.0.76.0":      false,
		"1.1.1.1":         false, // resolver, not the proxy ranges
		"23.1.2.3":        false, // Akamai
		"13.32.0.1":       false, // CloudFront
		// IPv6
		"2606:4700::6810:84e5":                    true,
		"2606:4700:ffff:ffff:ffff:ffff:ffff:ffff": true,
		"2606:4701::1":                            false,
		"2a06:98c0::1":                            true,
		"2a06:98c7:ffff::1":                       true, // /29 upper edge
		"2a06:98c8::1":                            false,
		"2c0f:f248::1":                            true,
		"2001:db8::1":                             false,
		// forms
		"::ffff:104.16.1.1":      true, // IPv4-mapped
		"[2606:4700::1]":         true,
		" 104.16.1.1 ":           true,
		"fe80::1%eth0":           false,
		"2606:4700::1%eth0":      true,
		"":                       false,
		"not-an-ip":              false,
		"104.16.1.1:443":         false, // host:port is not an IP
		"www.cloudflare.com":     false,
		"2400:cb00:2048:1::c629": true,
	}
	for ip, want := range cases {
		if got := Contains(ip); got != want {
			t.Errorf("Contains(%q) = %v, want %v", ip, got, want)
		}
	}
}

func TestFirst(t *testing.T) {
	if got := First([]string{"23.1.2.3", "2001:db8::1", "172.67.1.1", "104.16.1.1"}); got != "172.67.1.1" {
		t.Fatalf("First = %q", got)
	}
	if got := First([]string{"23.1.2.3"}); got != "" {
		t.Fatalf("First non-CF = %q", got)
	}
	if got := First(nil); got != "" {
		t.Fatalf("First nil = %q", got)
	}
}

func TestRangesCount(t *testing.T) {
	if n := len(Ranges()); n != 22 {
		t.Fatalf("ranges = %d, want 15 v4 + 7 v6", n)
	}
}
