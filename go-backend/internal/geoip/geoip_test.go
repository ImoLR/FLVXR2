package geoip

import (
	"context"
	"errors"
	"net/netip"
	"strings"
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
		// DB-IP assigns XK to these ranges, outside the fixed ISO country list.
		{"5.206.232.1", ""}, {"2001:470:7433::1", ""},
		{"10.0.0.1", ""}, {"172.16.0.1", ""}, {"192.168.1.1", ""},
		{"127.0.0.1", ""}, {"100.64.0.1", ""}, {"100.127.255.254", ""},
		{"169.254.1.1", ""}, {"0.0.0.0", ""}, {"0.1.2.3", ""}, {"240.0.0.1", ""}, {"255.255.255.255", ""},
		{"224.0.0.1", ""}, {"192.0.0.1", ""}, {"192.0.2.1", ""}, {"198.18.0.1", ""},
		{"198.51.100.1", ""}, {"203.0.113.1", ""}, {"::ffff:192.168.1.1", ""},
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
	answers map[string][]netip.Addr
	err     error
	calls   []string
	t       *testing.T
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 3*time.Second {
		r.t.Fatal("DNS lookup must have a deadline of at most 3 seconds")
	}
	if network != "ip4" && network != "ip6" {
		r.t.Fatalf("DNS lookup must select one address family, got %q", network)
	}
	r.calls = append(r.calls, network+":"+host)
	return r.answers[network+":"+host], r.err
}

func TestDetectNodeAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, v4, general, v6 string
		answers               map[string][]netip.Addr
		dnsErr                error
		want                  DetectionResult
		calls                 string
	}{
		{
			name: "IPv4 literal first", v4: "168.95.1.1", general: "8.8.8.8", v6: "2001:4860:4860::8888",
			want: DetectionResult{Region: "TW", IP: "168.95.1.1", Family: "v4", Source: "server_ip_v4"},
		},
		{
			name: "general IPv4 before domain", v4: "node.example", general: "8.8.8.8",
			want: DetectionResult{Region: "US", IP: "8.8.8.8", Family: "v4", Source: "server_ip"},
		},
		{
			name: "dual stack domain uses A", general: " node.example ", v6: "2001:b000:168::1",
			answers: map[string][]netip.Addr{
				"ip4:node.example": {netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8")},
				"ip6:node.example": {netip.MustParseAddr("2001:b000:168::1")},
			},
			want: DetectionResult{Region: "US", IP: "8.8.8.8", Family: "v4", Source: "dns_a"}, calls: "ip4:node.example",
		},
		{
			name: "private IPv4 falls back", v4: "192.168.1.1", v6: "2001:b000:168::1",
			want: DetectionResult{Region: "TW", IP: "2001:b000:168::1", Family: "v6", Source: "server_ip_v6", Reason: "IPv4 为内网地址，已改用 IPv6 识别"},
		},
		{
			name: "both private", v4: "10.0.0.1", v6: "fd00::1",
			want: DetectionResult{Reason: "IPv4/IPv6 均为内网地址，请手动选择地区"},
		},
		{
			name: "private A falls back to AAAA", v4: "node.example",
			answers: map[string][]netip.Addr{
				"ip4:node.example": {netip.MustParseAddr("10.0.0.1")},
				"ip6:node.example": {netip.MustParseAddr("2001:b000:168::1")},
			},
			want: DetectionResult{Region: "TW", IP: "2001:b000:168::1", Family: "v6", Source: "dns_aaaa", Reason: "IPv4 为内网地址，已改用 IPv6 识别"}, calls: "ip4:node.example,ip6:node.example",
		},
		{
			name: "DNS failure", general: "unresolvable.invalid", dnsErr: errors.New("DNS failed"),
			want: DetectionResult{Reason: "域名解析失败"}, calls: "ip4:unresolvable.invalid,ip6:unresolvable.invalid",
		},
		{
			name: "unknown public IPv4 does not fall back", v4: "5.206.232.1", v6: "2001:b000:168::1",
			want: DetectionResult{IP: "5.206.232.1", Family: "v4", Source: "server_ip_v4", Reason: "未查到该 IP 的地区"},
		},
		{
			name: "mapped private IPv4 falls back", v4: "::ffff:10.0.0.1", general: "2001:b000:168::1",
			want: DetectionResult{Region: "TW", IP: "2001:b000:168::1", Family: "v6", Source: "server_ip", Reason: "IPv4 为内网地址，已改用 IPv6 识别"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeResolver{t: t, answers: tc.answers, err: tc.dnsErr}
			if got := detectNodeAddresses(tc.v4, tc.general, tc.v6, r); got != tc.want {
				t.Fatalf("DetectNodeAddresses = %+v, want %+v", got, tc.want)
			}
			if got := strings.Join(r.calls, ","); got != tc.calls {
				t.Fatalf("DNS queries = %q, want %q", got, tc.calls)
			}
		})
	}
}

func TestEmbeddedKnownIPs(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"168.95.1.1", "TW"}, {"8.8.8.8", "US"}, {"223.5.5.5", "CN"}, {"1.1.1.1", ""},
	} {
		got := Lookup(netip.MustParseAddr(tc.ip))
		t.Logf("DB-IP %s -> %s", tc.ip, got)
		if got == "" || (tc.want != "" && got != tc.want) {
			t.Errorf("Lookup(%q) = %q, want %q (non-empty for 1.1.1.1)", tc.ip, got, tc.want)
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
