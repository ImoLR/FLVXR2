package repo

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeNodeExtraIPs(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		primary   []string
		want, err string
	}{
		{name: "empty", raw: "  ", want: ""},
		{name: "trim dedupe canonical", raw: " 2001:DB8::2\n2001:db8:0::2, [2001:db8::3]，203.0.113.9 ;203.0.113.9",
			want: `["2001:db8::2","2001:db8::3","203.0.113.9"]`},
		{name: "json array", raw: `["2001:db8::2","10.0.0.5"]`, want: `["2001:db8::2","10.0.0.5"]`},
		{name: "primary v6 duplicate", raw: "2001:db8::1", primary: []string{"192.0.2.1", "192.0.2.1", "2001:DB8::1"}, err: "与节点主 IP 重复"},
		{name: "primary server ip duplicate", raw: "[192.0.2.1]", primary: []string{"192.0.2.1"}, err: "与节点主 IP 重复"},
		{name: "hostname", raw: "hk.example.com", err: "不是有效的 IPv4 或 IPv6 地址"},
		{name: "with port", raw: "192.0.2.5:443", err: "不是有效的"},
		{name: "loopback", raw: "::1", err: "回环"},
		{name: "unspecified", raw: "0.0.0.0", err: "未指定"},
		{name: "link local", raw: "fe80::1", err: "链路本地"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeNodeExtraIPs(tc.raw, tc.primary...)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestNormalizeNodeExtraIPsCap(t *testing.T) {
	ips := make([]string, 0, MaxNodeExtraIPs+1)
	for i := 0; i <= MaxNodeExtraIPs; i++ {
		ips = append(ips, fmt.Sprintf("2001:db8::%x", i+1))
	}
	if _, err := NormalizeNodeExtraIPs(strings.Join(ips[:MaxNodeExtraIPs], ",")); err != nil {
		t.Fatalf("cap of %d rejected: %v", MaxNodeExtraIPs, err)
	}
	if _, err := NormalizeNodeExtraIPs(strings.Join(ips, ",")); err == nil || !strings.Contains(err.Error(), "最多 16 个") {
		t.Fatalf("over cap accepted: %v", err)
	}
}

func TestParseNodeExtraIPsAcceptsLegacyLists(t *testing.T) {
	for raw, want := range map[string][]string{
		"":                                {},
		`["2001:db8::2","192.0.2.9"]`:     {"2001:db8::2", "192.0.2.9"},
		"192.0.2.9, [2001:db8::2] bad.ip": {"192.0.2.9", "2001:db8::2"},
		"10.0.0.5\n10.0.0.5":              {"10.0.0.5"},
	} {
		if got := ParseNodeExtraIPs(raw); !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseNodeExtraIPs(%q) = %v, want %v", raw, got, want)
		}
	}
}
