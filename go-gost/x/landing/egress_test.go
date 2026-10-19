package landing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	mrand "math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Fixed keys keep the golden file stable.
const (
	goldenWGPrivate = "YFYOAdbw1bKTHlNNi+aEjBM3BO7unuFC5rOkMRAz9XY="
	goldenWGPeer    = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
	goldenSSKey     = "AAAAAAAAAAAAAAAAAAAAAA=="
	goldenPBK       = "Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws"
)

func goldenPlan() *Egress {
	return &Egress{Version: 1, Outbounds: []EgressOutbound{
		{Tag: "flvxo-direct4", Type: "direct", IPVersion: "force_ipv4"},
		{Tag: "flvxo-direct6", Type: "direct", IPVersion: "force_ipv6"},
		{Tag: "flvxo-pref4", Type: "direct", IPVersion: "prefer_ipv4"},
		{Tag: "flvxo-warp", Type: "wireguard", IPVersion: "force_ipv4", WireGuard: &EgressWireGuard{PrivateKey: goldenWGPrivate, PeerPublicKey: goldenWGPeer, AddressV4: "172.16.0.2", AddressV6: "2606:4700:110:8a36::2", Reserved: []int{1, 2, 3}}},
		{Tag: "flvxo-socks", Type: "socks", IPVersion: "prefer_ipv6", Server: "proxy.example.com", Port: 1080, Username: "u", Password: "p"},
		{Tag: "flvxo-http", Type: "http", IPVersion: "system", Server: "203.0.113.10", Port: 8080},
		{Tag: "flvxo-ss", Type: "shadowsocks", IPVersion: "force_ipv6", Server: "2001:db8::10", Port: 8388, Method: "2022-blake3-aes-128-gcm", Password: goldenSSKey + ":" + goldenSSKey},
		{Tag: "flvxo-vless", Type: "vless", IPVersion: "force_ipv4", Server: "198.51.100.7", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-vision", Reality: &EgressReality{ServerName: "www.example.com", PublicKey: goldenPBK, ShortID: "0a1b"}},
	}, Rules: []EgressRule{
		{Name: "AI", InboundTags: []string{"flvxl-2", "flvxl-1"}, Domain: []string{"geosite:openai", "domain:claude.ai", "full:x.ai", "keyword:gemini", "regexp:^chat\\."}, Outbound: "flvxo-warp"},
		{Name: "IP", InboundTags: []string{"flvxl-1"}, IP: []string{"geoip:cn", "203.0.113.0/24", "2001:db8::/32"}, Port: "443,8000-9000", Network: "tcp", Outbound: "block"},
		{Name: "BT", InboundTags: []string{"flvxl-1"}, Protocol: []string{"bittorrent"}, SourcePort: "1024-65535", Outbound: "block"},
		{Name: "Src", InboundTags: []string{"flvxl-1"}, SourceIP: []string{"198.51.100.0/24"}, Outbound: "flvxo-direct4"},
		{Name: "Default", InboundTags: []string{"flvxl-1", "flvxl-2"}, Outbound: "flvxo-socks"},
	}}
}

func TestEgressValidation(t *testing.T) {
	tags := map[string]bool{"flvxl-1": true, "flvxl-2": true}
	if err := goldenPlan().validate(tags); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(e *Egress){
		"version":              func(e *Egress) { e.Version = 2 },
		"bad tag":              func(e *Egress) { e.Outbounds[0].Tag = "direct" },
		"dup tag":              func(e *Egress) { e.Outbounds[1].Tag = e.Outbounds[0].Tag },
		"ip version":           func(e *Egress) { e.Outbounds[0].IPVersion = "ipv4" },
		"private upstream":     func(e *Egress) { e.Outbounds[5].Server = "10.0.0.5" },
		"metadata upstream":    func(e *Egress) { e.Outbounds[5].Server = "169.254.169.254" },
		"loopback upstream":    func(e *Egress) { e.Outbounds[5].Server = "::1" },
		"mapped private":       func(e *Egress) { e.Outbounds[5].Server = "::ffff:192.168.1.1" },
		"bare hostname":        func(e *Egress) { e.Outbounds[4].Server = "localhost" },
		"forced v4 v6 literal": func(e *Egress) { e.Outbounds[7].Server = "2001:db8::7" },
		"forced v6 v4 literal": func(e *Egress) { e.Outbounds[6].Server = "203.0.113.9" },
		"ss2022 key":           func(e *Egress) { e.Outbounds[6].Password = "short" },
		"ss method":            func(e *Egress) { e.Outbounds[6].Method = "rc4-md5" },
		"vless uuid":           func(e *Egress) { e.Outbounds[7].UUID = "nope" },
		"reality key":          func(e *Egress) { e.Outbounds[7].Reality.PublicKey = "abc" },
		"reality sid":          func(e *Egress) { e.Outbounds[7].Reality.ShortID = "xyz" },
		"wg family":            func(e *Egress) { e.Outbounds[3].WireGuard.AddressV4 = "" },
		"wg reserved":          func(e *Egress) { e.Outbounds[3].WireGuard.Reserved = []int{1, 2} },
		"wg private endpoint":  func(e *Egress) { e.Outbounds[3].WireGuard.Endpoint = "192.168.0.1:2408" },
		"wg mtu":               func(e *Egress) { e.Outbounds[3].WireGuard.MTU = 9000 },
		"unknown type":         func(e *Egress) { e.Outbounds[0].Type = "vmess" },
		"rule outbound":        func(e *Egress) { e.Rules[0].Outbound = "flvxo-missing" },
		"rule scope":           func(e *Egress) { e.Rules[0].InboundTags = nil },
		"rule foreign node":    func(e *Egress) { e.Rules[0].InboundTags = []string{"flvxl-9"} },
		"ext domain":           func(e *Egress) { e.Rules[0].Domain = []string{"ext:evil.dat:x"} },
		"bare domain":          func(e *Egress) { e.Rules[0].Domain = []string{"example.com"} },
		"bad regexp":           func(e *Egress) { e.Rules[0].Domain = []string{"regexp:("} },
		"ext ip":               func(e *Egress) { e.Rules[1].IP = []string{"ext:evil.dat:x"} },
		"bad cidr":             func(e *Egress) { e.Rules[1].IP = []string{"1.2.3.4/40"} },
		"bad port":             func(e *Egress) { e.Rules[1].Port = "0-70000" },
		"bad network":          func(e *Egress) { e.Rules[1].Network = "icmp" },
		"bad protocol":         func(e *Egress) { e.Rules[2].Protocol = []string{"ssh"} },
		"bad exempt":           func(e *Egress) { e.ExemptSources = []string{"nope"} },
		"too many outbounds": func(e *Egress) {
			for i := 0; i < 8; i++ {
				e.Outbounds = append(e.Outbounds, EgressOutbound{Tag: fmt.Sprintf("flvxo-x%d", i), Type: "direct", IPVersion: "system"})
			}
		},
		"too many rules": func(e *Egress) {
			for i := 0; i < maxEgressRules; i++ {
				e.Rules = append(e.Rules, e.Rules[0])
			}
		},
	}
	for name, mutate := range cases {
		e := goldenPlan()
		mutate(e)
		if err := e.validate(tags); err == nil {
			t.Errorf("%s: invalid plan accepted", name)
		}
	}
	// Desired.validate wires the node-tag scope.
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}, Egress: goldenPlan()}
	if err := d.validate(); err == nil {
		t.Fatal("rule scoped to a node absent from the desired state accepted")
	}
	d.Nodes = append(d.Nodes, testNode(t, 2))
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
}

var goldenLocal = localAddrs{v4: netip.MustParseAddr("203.0.113.1"), v6: netip.MustParseAddr("2001:db8:1::1")}

func renderGolden(local localAddrs) map[string]any {
	out := map[string]any{}
	for _, o := range goldenPlan().Outbounds {
		cfg, warn := o.render(local)
		out[o.Tag] = map[string]any{"config": cfg, "warning": warn}
	}
	return out
}

func TestEgressRenderGolden(t *testing.T) {
	plan := goldenPlan()
	rules, err := renderRules(plan, func(string) ([]netip.Prefix, error) { return nil, fmt.Errorf("no geo data needed") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{"outbounds": renderGolden(goldenLocal), "outbounds_no_local": renderGolden(localAddrs{}), "rules": rules}
	raw, _ := json.MarshalIndent(got, "", "  ")
	path := filepath.Join("testdata", "egress_golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(raw)) {
		t.Fatalf("rendered egress differs from %s; rerun with UPDATE_GOLDEN=1 after review\n%s", path, raw)
	}
}

// The forced-family guarantees, checked field by field (the golden file pins
// the full JSON).
func TestEgressForcedFamilies(t *testing.T) {
	plan := goldenPlan()
	byTag := map[string]EgressOutbound{}
	for _, o := range plan.Outbounds {
		byTag[o.Tag] = o
	}
	direct4, _ := byTag["flvxo-direct4"].render(goldenLocal)
	if direct4["sendThrough"] != "203.0.113.1" || direct4["settings"].(map[string]any)["domainStrategy"] != "ForceIPv4" {
		t.Fatalf("forced IPv4 direct not bound: %v", direct4)
	}
	sockopt := direct4["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if custom := sockopt["customSockopt"].([]any)[0].(map[string]any); custom["opt"] != "24" || custom["level"] != "0" {
		t.Fatal("IP_BIND_ADDRESS_NO_PORT missing on bound direct outbound")
	}
	direct6, _ := byTag["flvxo-direct6"].render(goldenLocal)
	if direct6["sendThrough"] != "2001:db8:1::1" || direct6["settings"].(map[string]any)["domainStrategy"] != "ForceIPv6" {
		t.Fatalf("forced IPv6 direct not bound: %v", direct6)
	}
	// No address of the forced family: the outbound refuses everything.
	none, warn := byTag["flvxo-direct6"].render(localAddrs{v4: goldenLocal.v4})
	if none["protocol"] != "blackhole" || warn == "" {
		t.Fatal("forced IPv6 without an IPv6 address must be a blackhole")
	}
	pref, _ := byTag["flvxo-pref4"].render(goldenLocal)
	if _, bound := pref["sendThrough"]; bound || pref["settings"].(map[string]any)["domainStrategy"] != "UseIPv4v6" {
		t.Fatal("prefer mode must not bind")
	}
	warp, _ := byTag["flvxo-warp"].render(goldenLocal)
	ws := warp["settings"].(map[string]any)
	if addr := ws["address"].([]string); len(addr) != 1 || addr[0] != "172.16.0.2/32" || ws["domainStrategy"] != "ForceIPv4" || ws["noKernelTun"] != true {
		t.Fatalf("forced IPv4 WARP must carry only the IPv4 tunnel address: %v", ws)
	}
	if peer := ws["peers"].([]any)[0].(map[string]any); peer["endpoint"] != warpEndpointV4 {
		t.Fatal("WARP endpoint not chosen by server connectivity")
	}
	withEndpoints := byTag["flvxo-warp"]
	wg := *withEndpoints.WireGuard
	wg.EndpointV4, wg.EndpointV6 = "162.159.192.6:2408", "[2606:4700:d0::a29f:c006]:2408"
	withEndpoints.WireGuard = &wg
	if err := withEndpoints.validate(); err != nil {
		t.Fatal(err)
	}
	accountEP, _ := withEndpoints.render(localAddrs{v6: goldenLocal.v6})
	if peer := accountEP["settings"].(map[string]any)["peers"].([]any)[0].(map[string]any); peer["endpoint"] != "[2606:4700:d0::a29f:c006]:2408" {
		t.Fatal("registration endpoints not used")
	}
	wg.EndpointV4 = "[2606:4700:d0::a29f:c006]:2408"
	if err := withEndpoints.validate(); err == nil {
		t.Fatal("endpoint_v4 of the wrong family accepted")
	}
	v6only, _ := byTag["flvxo-warp"].render(localAddrs{v6: goldenLocal.v6})
	if peer := v6only["settings"].(map[string]any)["peers"].([]any)[0].(map[string]any); peer["endpoint"] != warpEndpointV6 {
		t.Fatal("IPv6-only server must use the IPv6 WARP endpoint")
	}
	ss, _ := byTag["flvxo-ss"].render(goldenLocal)
	if ss["sendThrough"] != "2001:db8:1::1" || ss["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["domainStrategy"] != "ForceIPv6" {
		t.Fatalf("forced IPv6 upstream not bound: %v", ss)
	}
	socks, _ := byTag["flvxo-socks"].render(goldenLocal)
	if _, bound := socks["sendThrough"]; bound || socks["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["domainStrategy"] != "UseIPv6v4" {
		t.Fatal("prefer IPv6 upstream misrendered")
	}
	http, _ := byTag["flvxo-http"].render(goldenLocal)
	if _, ok := http["streamSettings"]; ok {
		t.Fatal("system mode must not add socket options")
	}
}

func TestEgressRulesSafetyFirstAndExemption(t *testing.T) {
	cn := []netip.Prefix{netip.MustParsePrefix("1.0.1.0/24"), netip.MustParsePrefix("1.0.2.0/23"), netip.MustParsePrefix("240e::/20")}
	read := func(code string) ([]netip.Prefix, error) {
		if code != "cn" {
			return nil, fmt.Errorf("unknown %s", code)
		}
		return cn, nil
	}
	e := &Egress{Version: 1, Rules: []EgressRule{
		{InboundTags: []string{"flvxl-1"}, IP: []string{"geoip:cn"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-1"}, Domain: []string{"geosite:cn"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-1"}, SourceIP: []string{"geoip:cn"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-1"}, SourceIP: []string{"1.0.1.7/32"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-1"}, SourceIP: []string{"geoip:!cn"}, Outbound: "direct"},
	}, ExemptSources: []string{"1.0.1.7", "240e::1/128", "8.8.4.4"}}
	rules, err := renderRules(e, read, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := rules[0].(map[string]any)
	if first["ruleTag"] != "flvx-safety" || first["outboundTag"] != "block" || len(first["ip"].([]string)) != len(blockedDestinations) {
		t.Fatal("safety rule must be first")
	}
	if rules[1].(map[string]any)["ip"].([]string)[0] != "geoip:cn" || rules[2].(map[string]any)["domain"].([]string)[0] != "geosite:cn" {
		t.Fatal("target matchers must pass through to the Core")
	}
	src := rules[3].(map[string]any)["sourceIP"].([]string)
	set := map[string]bool{}
	for _, c := range src {
		set[c] = true
	}
	for _, exempt := range []string{"1.0.1.7/32", "240e::1/128", "8.8.4.4/32"} {
		if set[exempt] {
			t.Fatal("exempt source still blocked")
		}
	}
	got := newDualSet(mustPrefixes(t, src))
	want := newDualSet(cn).subtract(newDualSet(mustPrefixes(t, e.ExemptSources)))
	if fmt.Sprint(got.cidrs()) != fmt.Sprint(want.cidrs()) {
		t.Fatalf("CN minus exempt mismatch:\n%v\n%v", got.cidrs(), want.cidrs())
	}
	// 1.0.1.7/32 is wholly exempt: the rule must disappear, never become an
	// unconditional rule.
	if len(rules) != 5 {
		t.Fatalf("fully exempt source rule not dropped: %d rules", len(rules))
	}
	reverse := rules[4].(map[string]any)["sourceIP"].([]string)
	rs := newDualSet(mustPrefixes(t, reverse))
	if rs.intersects(newDualSet(cn)) || rs.intersects(newDualSet(mustPrefixes(t, e.ExemptSources))) {
		t.Fatal("reverse geoip source overlaps CN or exempt sources")
	}
	if !rs.intersects(newDualSet(mustPrefixes(t, []string{"8.8.8.8"}))) {
		t.Fatal("reverse geoip lost non-CN space")
	}
	// Without an overlap the original geoip entry is kept (smaller payload).
	e.ExemptSources = []string{"8.8.8.8"}
	rules, _ = renderRules(e, read, nil)
	if s := rules[3].(map[string]any)["sourceIP"].([]string); len(s) != 1 || s[0] != "geoip:cn" {
		t.Fatalf("non-overlapping exemption rewrote the source list: %v", s)
	}
}

func mustPrefixes(t *testing.T, in []string) []netip.Prefix {
	t.Helper()
	out := []netip.Prefix{}
	for _, s := range in {
		p, err := parsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// Brute force over a /20 checks subtract + CIDR cover exactness.
func TestCIDRSetBruteForce(t *testing.T) {
	r := mrand.New(mrand.NewSource(7))
	base := netip.MustParseAddr("10.20.0.0").As4()
	addr := func(i int) netip.Addr {
		b := base
		b[2] += byte(i >> 8)
		b[3] = byte(i)
		return netip.AddrFrom4(b)
	}
	for round := 0; round < 200; round++ {
		in := map[int]bool{}
		var a, b []netip.Prefix
		for i := 0; i < 6; i++ {
			bitsLen := 20 + r.Intn(13)
			p := netip.PrefixFrom(addr(r.Intn(4096)), bitsLen).Masked()
			a = append(a, p)
		}
		for i := 0; i < 6; i++ {
			bitsLen := 22 + r.Intn(11)
			b = append(b, netip.PrefixFrom(addr(r.Intn(4096)), bitsLen).Masked())
		}
		for i := 0; i < 4096; i++ {
			x := addr(i)
			inA, inB := false, false
			for _, p := range a {
				inA = inA || p.Contains(x)
			}
			for _, p := range b {
				inB = inB || p.Contains(x)
			}
			in[i] = inA && !inB
		}
		got := newDualSet(a).subtract(newDualSet(b))
		cover := mustPrefixes(t, got.cidrs())
		for i := 0; i < 4096; i++ {
			x := addr(i)
			hit := false
			for _, p := range cover {
				hit = hit || p.Contains(x)
			}
			if hit != in[i] {
				t.Fatalf("round %d: %s membership %v want %v (a=%v b=%v cover=%v)", round, x, hit, in[i], a, b, cover)
			}
		}
		// Minimality: a re-normalized cover must not shrink.
		if again := newDualSet(cover).cidrs(); len(again) != len(cover) {
			t.Fatalf("non-minimal cover %v vs %v", cover, again)
		}
	}
	full := fullSet(128)
	if c := full.cidrs(); len(c) != 1 || c[0] != "::/0" {
		t.Fatalf("full IPv6 space cover = %v", c)
	}
	if c := fullSet(32).subtract(newIPSet(32, []ipRange{prefixRangeMust("255.255.255.255/32")})).cidrs(); len(c) != 32 || c[0] != "0.0.0.0/1" {
		t.Fatalf("top-of-space subtraction = %v", c)
	}
}

func prefixRangeMust(s string) ipRange {
	r, _ := prefixRange(netip.MustParsePrefix(s))
	return r
}

func writeVarint(buf *bytes.Buffer, v uint64) {
	for v >= 0x80 {
		buf.WriteByte(byte(v) | 0x80)
		v >>= 7
	}
	buf.WriteByte(byte(v))
}

func geoEntry(code string, prefixes []netip.Prefix) []byte {
	var body bytes.Buffer
	body.WriteByte(0x0a)
	writeVarint(&body, uint64(len(code)))
	body.WriteString(code)
	for _, p := range prefixes {
		var c bytes.Buffer
		ip := p.Addr().AsSlice()
		c.WriteByte(0x0a)
		writeVarint(&c, uint64(len(ip)))
		c.Write(ip)
		c.WriteByte(0x10)
		writeVarint(&c, uint64(p.Bits()))
		body.WriteByte(0x12)
		writeVarint(&body, uint64(c.Len()))
		body.Write(c.Bytes())
	}
	var out bytes.Buffer
	out.WriteByte(0x0a)
	writeVarint(&out, uint64(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func TestReadGeoIP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geoip.dat")
	data := append(geoEntry("US", []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}), geoEntry("CN", []netip.Prefix{netip.MustParsePrefix("1.0.1.0/24"), netip.MustParsePrefix("240e::/20")})...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readGeoIP(path, "cn")
	if err != nil || len(got) != 2 || got[1].String() != "240e::/20" {
		t.Fatalf("readGeoIP = %v %v", got, err)
	}
	if _, err := readGeoIP(path, "jp"); err == nil {
		t.Fatal("missing category accepted")
	}
	// The pinned release, when present, ships CN in both families.
	if real := os.Getenv("FLVX_LANDING_GEOIP"); real != "" {
		cn, err := readGeoIP(real, "cn")
		if err != nil || len(cn) < 1000 {
			t.Fatalf("pinned geoip.dat cn: %d %v", len(cn), err)
		}
		set := newDualSet(cn)
		if len(set.v4.ranges) == 0 || len(set.v6.ranges) == 0 {
			t.Fatal("pinned geoip:cn lacks a family")
		}
		t.Logf("pinned geoip:cn = %d CIDRs (%d IPv4 ranges, %d IPv6 ranges)", len(cn), len(set.v4.ranges), len(set.v6.ranges))
	}
}

func TestWithheldTagsFailClosed(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	m.desired = Desired{Egress: &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{"flvxl-1", "flvxl-2"}, Outbound: "block"}}}}
	m.egress.State = "applied"
	if len(m.withheldTags()) != 0 {
		t.Fatal("successful apply withheld nodes")
	}
	m.egress.State = "error"
	m.applied.EgressTags = []string{"flvxl-1"}
	got := m.withheldTags()
	if len(got) != 1 || !got["flvxl-2"] {
		t.Fatalf("withheld = %v; want only the newly covered node", got)
	}
}

// fakeCore records API calls in order and answers like the pinned CLI.
func fakeCore(t *testing.T, dir string, routing bool) string {
	t.Helper()
	log := filepath.Join(dir, "calls.log")
	lsrules := `{"rules":[]}`
	exitRules := "0"
	if !routing {
		exitRules = "1"
	}
	script := fmt.Sprintf(`#!/bin/sh
cmd="$2"
shift 2
echo "$cmd $*" >> %q
case "$cmd" in
lsrules) echo '%s'; exit %s ;;
lso) cat %q 2>/dev/null || echo '{"outbounds":[{"tag":"direct"},{"tag":"block"},{"tag":"flvxo-old-00000000"}]}' ;;
ado) for f in "$@"; do case "$f" in --*) ;; *) cp "$f" %q ;; esac; done ;;
adrules) for f in "$@"; do case "$f" in --*) ;; *) cp "$f" %q ;; esac; done ;;
esac
exit 0
`, log, lsrules, exitRules, filepath.Join(dir, "lso.json"), filepath.Join(dir, "last-ado.json"), filepath.Join(dir, "last-rules.json"))
	path := filepath.Join(dir, "flvx-landing-core")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestApplyEgressOrderAndIdempotence(t *testing.T) {
	dir := t.TempDir()
	log := fakeCore(t, dir, true)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m.detectLocal = func() localAddrs { return goldenLocal }
	plan := &Egress{Version: 1, Outbounds: []EgressOutbound{{Tag: "flvxo-a", Type: "direct", IPVersion: "force_ipv4"}}, Rules: []EgressRule{{InboundTags: []string{"flvxl-1"}, Port: "25", Outbound: "flvxo-a"}}}
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}, Egress: plan}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	var order []string
	for _, l := range lines {
		order = append(order, strings.Fields(l)[0])
	}
	if strings.Join(order, ",") != "lsrules,lso,ado,adrules,rmo" {
		t.Fatalf("apply order = %v; want add → replace rules → remove", order)
	}
	if !strings.Contains(lines[len(lines)-1], "flvxo-old-00000000") {
		t.Fatal("stale outbound not removed")
	}
	rulesRaw, _ := os.ReadFile(filepath.Join(dir, "last-rules.json"))
	var rules struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(rulesRaw, &rules); err != nil || rules.Routing.Rules[0]["ruleTag"] != "flvx-safety" {
		t.Fatalf("replacement rules missing safety rule first: %s", rulesRaw)
	}
	coreTag := rules.Routing.Rules[1]["outboundTag"].(string)
	if !strings.HasPrefix(coreTag, "flvxo-a-") {
		t.Fatalf("rule must reference the content-addressed core tag, got %s", coreTag)
	}
	if m.egress.State != "applied" || len(m.applied.EgressTags) != 1 {
		t.Fatalf("status %+v applied %+v", m.egress, m.applied)
	}
	// Steady state: no CLI calls at all.
	_ = os.Remove(log)
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err == nil {
		calls, _ := os.ReadFile(log)
		t.Fatalf("idempotent apply invoked the Core: %s", calls)
	}
	// A new local address changes the bound outbound: new tag, same order.
	m.detectLocal = func() localAddrs { return localAddrs{v4: netip.MustParseAddr("203.0.113.99")} }
	m.egressCache = egressCache{}
	present := map[string]any{"outbounds": []any{map[string]any{"tag": "direct"}, map[string]any{"tag": "block"}, map[string]any{"tag": coreTag}}}
	b, _ := json.Marshal(present)
	_ = os.WriteFile(filepath.Join(dir, "lso.json"), b, 0644)
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	swapped := false
	for _, l := range strings.Split(string(calls), "\n") {
		swapped = swapped || (strings.HasPrefix(l, "rmo ") && strings.Contains(l, coreTag))
	}
	if !swapped || !strings.Contains(string(calls), "ado ") {
		t.Fatalf("address change not hot-swapped: %s", calls)
	}
	// Back to the default plan: rules reset to the safety rule, outbounds removed.
	m.desired.Revision++
	m.desired.Egress = nil
	_ = os.Remove(log)
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	rulesRaw, _ = os.ReadFile(filepath.Join(dir, "last-rules.json"))
	_ = json.Unmarshal(rulesRaw, &rules)
	if len(rules.Routing.Rules) != 1 || m.applied.Egress != "" || m.egress.State != "none" {
		t.Fatalf("default plan did not reset rules: %s %+v", rulesRaw, m.egress)
	}
}

func TestApplyEgressRestartRequiredAndWithheld(t *testing.T) {
	dir := t.TempDir()
	fakeCore(t, dir, false)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	plan := &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{"flvxl-1"}, Port: "25", Outbound: "block"}}}
	m.desired = Desired{Revision: 3, Enabled: true, Nodes: []Node{testNode(t, 1), testNode(t, 2)}, Egress: plan}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != errRestartRequired {
		t.Fatalf("old Core without RoutingService: err=%v", err)
	}
	if m.egress.State != "restart_required" || m.egress.Ready {
		t.Fatalf("status %+v", m.egress)
	}
	w := m.withheldTags()
	if !w["flvxl-1"] || w["flvxl-2"] {
		t.Fatalf("withheld %v; only the node covered by the unapplied plan stays down", w)
	}
	// The default plan on an old Core needs nothing.
	m.desired.Egress = nil
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil || m.egress.State != "none" {
		t.Fatalf("default plan on old Core: %v %+v", err, m.egress)
	}
	// Allowed restart in direct mode without a managed process reports an error
	// and never loops.
	m.desired.Egress = &Egress{Version: 1, AllowCoreRestart: true, Rules: plan.Rules}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err == nil || m.restartedRevision != 3 {
		t.Fatalf("restart attempt not recorded: %v", err)
	}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != errRestartRequired {
		t.Fatalf("second attempt in the same revision must not restart again: %v", err)
	}
}

func TestGeoDataOnlyWhenReferencedAndVerified(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, true)
	var fetched []string
	m.geoDownload = func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error {
		fetched = append(fetched, filepath.Base(path))
		return fmt.Errorf("offline")
	}
	if files := (&Egress{Rules: []EgressRule{{Port: "25"}}}).geoFiles(); len(files) != 0 {
		t.Fatal("geo data requested without geo matchers")
	}
	files := (&Egress{Rules: []EgressRule{{Domain: []string{"geosite:cn"}}, {SourceIP: []string{"geoip:cn"}}}}).geoFiles()
	sort.Strings(files)
	if strings.Join(files, ",") != "geoip.dat,geosite.dat" {
		t.Fatalf("geo files = %v", files)
	}
	if err := m.ensureGeoData(context.Background(), files); err == nil || len(fetched) == 0 {
		t.Fatalf("download failure not reported: %v %v", err, fetched)
	}
	// A wrong local file is never trusted.
	_ = os.WriteFile(filepath.Join(dir, "geoip.dat"), []byte("tampered"), 0644)
	if m.geoVerified("geoip.dat") {
		t.Fatal("tampered geo data accepted")
	}
}
