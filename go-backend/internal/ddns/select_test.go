package ddns

import (
	"strings"
	"testing"
	"time"
)

var node48 = AgentAddrs{
	V4: []string{},
	V6: []string{"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"},
}

func TestSelectAddressByCarrierPrefixAndFamily(t *testing.T) {
	for _, tc := range []struct {
		typ, source, prefix, panel string
		addrs                      AgentAddrs
		want                       string
		ok                         bool
	}{
		{"AAAA", SourceAuto, "", "", node48, "240e:b8f:72d:5c04:ca:8aff:fec0:2813", true},
		{"AAAA", SourceCT, "", "", node48, "240e:b8f:72d:5c04:ca:8aff:fec0:2813", true},
		{"AAAA", SourceCU, "", "", node48, "2408:820c:750b:dc64:ca:8aff:fec0:2813", true},
		{"AAAA", SourceCM, "", "", node48, "", false},
		{"AAAA", SourcePrefix, "2408:820c", "", node48, "2408:820c:750b:dc64:ca:8aff:fec0:2813", true},
		{"AAAA", SourcePrefix, "2408:0820c", "", node48, "", false}, // no such group
		{"AAAA", SourcePrefix, "240e:b8f::/32", "", node48, "240e:b8f:72d:5c04:ca:8aff:fec0:2813", true},
		{"AAAA", SourcePrefix, "240e:b8f:a30::/48", "", node48, "", false},
		{"AAAA", SourcePrefix, "240e:b8f:72d:5c04:", "", node48, "240e:b8f:72d:5c04:ca:8aff:fec0:2813", true},
		{"A", SourcePanel, "", "123.204.3.9", node48, "123.204.3.9", true},
		{"A", SourcePanel, "", "2001:b011::1", node48, "", false}, // session over IPv6
		{"A", SourcePanel, "", "10.3.100.20", node48, "", false},  // private
		{"A", SourceIface, "", "", AgentAddrs{V4: []string{"8.8.4.4"}}, "8.8.4.4", true},
		{"A", SourceIface, "", "", node48, "", false},
		{"AAAA", SourceAuto, "", "", AgentAddrs{}, "", false},
	} {
		got, ok := SelectAddress(tc.typ, tc.source, tc.prefix, tc.addrs, tc.panel)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s %s %q: got %q %v, want %q %v", tc.typ, tc.source, tc.prefix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNormalizeReportFiltersAndDetectsChange(t *testing.T) {
	got := NormalizeReport(
		[]string{"10.3.100.20", "100.64.0.1", "8.8.8.8", "8.8.8.8", "bad"},
		[]string{"fe80::1", "fd12::1", "2001:db8::1", "2002::1", "[240E:B8F:72D:5C04:CA:8AFF:FEC0:2813]", "::ffff:1.2.3.4", "2408:820c:750b:dc64:ca:8aff:fec0:2813"},
	)
	want := AgentAddrs{V4: []string{"8.8.8.8"}, V6: []string{"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"}}
	if !got.Equal(want) {
		t.Fatalf("NormalizeReport = %+v", got)
	}
	rotated := NormalizeReport(nil, []string{"240e:b8f:a30:1904:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"})
	if rotated.Equal(want) {
		t.Fatal("prefix rotation must be a change")
	}
	if !NormalizeReport(nil, want.V6).Equal(AgentAddrs{V4: []string{}, V6: want.V6}) {
		t.Fatal("same set must compare equal")
	}
}

func TestValidateSourceAndDomain(t *testing.T) {
	if _, err := ValidateSource("AAAA", SourcePanel, ""); err == nil {
		t.Fatal("AAAA from panel IPv4 accepted")
	}
	if _, err := ValidateSource("A", SourceCT, ""); err == nil {
		t.Fatal("A from carrier prefix accepted")
	}
	if p, err := ValidateSource("AAAA", SourcePrefix, " 2408:820C::/32 "); err != nil || p != "2408:820c::/32" {
		t.Fatalf("CIDR prefix = %q %v", p, err)
	}
	if _, err := ValidateSource("AAAA", SourcePrefix, "10.0.0.0/8"); err == nil {
		t.Fatal("IPv4 prefix accepted for AAAA")
	}
	if _, err := ValidateSource("AAAA", SourcePrefix, "xyz"); err == nil {
		t.Fatal("garbage prefix accepted")
	}
	for raw, want := range map[string]string{"Leikwan-Hutai-CT.imgamer.top.": "leikwan-hutai-ct.imgamer.top"} {
		if got, err := NormalizeDomain(raw); err != nil || got != want {
			t.Fatalf("NormalizeDomain(%q) = %q %v", raw, got, err)
		}
	}
	for _, bad := range []string{"", "localhost", "*.imgamer.top", "a_b.imgamer.top", "240e::1", "1.2.3.4", "-x.imgamer.top", "x.123"} {
		if _, err := NormalizeDomain(bad); err == nil {
			t.Errorf("NormalizeDomain(%q) accepted", bad)
		}
	}
}

func TestProtectedDomains(t *testing.T) {
	for _, d := range []string{"flvxx.imgamer.top", "flvxxht.imgamer.top", "flvx.imgamer.top", "flvxht.imgamer.top", "mmwx.imgamer.top", "mmwxc.imgamer.top", "a.mmwx-node.imgamer.top", "imgamer.top"} {
		if !ProtectedDomain(d, "imgamer.top", nil) {
			t.Errorf("%s not protected", d)
		}
	}
	if !ProtectedDomain("panel.example.com", "example.com", []string{"PANEL.example.com"}) {
		t.Error("panel host not protected")
	}
	for _, d := range []string{"leikwan-hutai-ct.imgamer.top", "flvxx-node.imgamer.top"} {
		if ProtectedDomain(d, "imgamer.top", []string{"flvxxht.imgamer.top"}) {
			t.Errorf("%s protected", d)
		}
	}
}

func TestBackoffDoublesToTenMinutes(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := Backoff(i + 1); got != w {
			t.Fatalf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	if Backoff(0) != 30*time.Second || Backoff(100) != 10*time.Minute {
		t.Fatal("Backoff bounds")
	}
}

func TestZoneForPicksLongestSuffix(t *testing.T) {
	zones := []Zone{{ID: "1", Name: "imgamer.top"}, {ID: "2", Name: "hk.imgamer.top"}}
	if z, ok := ZoneFor("a.hk.imgamer.top", zones); !ok || z.ID != "2" {
		t.Fatalf("zone %+v %v", z, ok)
	}
	if z, ok := ZoneFor("leikwan-hutai-ct.imgamer.top", zones); !ok || z.ID != "1" {
		t.Fatalf("zone %+v %v", z, ok)
	}
	if _, ok := ZoneFor("x.example.com", zones); ok {
		t.Fatal("foreign zone matched")
	}
	if _, ok := ZoneFor("evilimgamer.top", zones); ok {
		t.Fatal("suffix without dot matched")
	}
	if !strings.Contains((&APIError{Status: 403, Code: 9109, Message: "Invalid access token"}).Error(), "9109") {
		t.Fatal("api error text")
	}
}
