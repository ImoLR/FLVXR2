package landingegress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

const testKey32 = "YFYOAdbw1bKTHlNNi+aEjBM3BO7unuFC5rOkMRAz9XY="
const testPBK = "Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws"

func TestNormalizeOutbound(t *testing.T) {
	good := []Outbound{
		{Name: "直连 v4", Type: TypeDirect, IPVersion: "force_ipv4", Settings: Settings{Server: "ignored"}},
		{Name: "WARP", Type: TypeWarp, Settings: Settings{PrivateKey: "dropped in auto mode"}},
		{Name: "WARP+", Type: TypeWarp, Settings: Settings{WarpLicense: "2Ia7f3S4-r56H8aT3-0Jz4F9i2"}},
		{Name: "WARP 手动", Type: TypeWarp, IPVersion: "force_ipv6", Settings: Settings{WarpMode: "manual", PrivateKey: testKey32, PeerPublicKey: testKey32, AddressV6: "2606:4700:110:8a36::2/128", Reserved: []int{1, 2, 3}, Endpoint: "162.159.192.1:2408"}},
		{Name: "socks", Type: TypeProxy, Settings: Settings{Protocol: "socks", Server: "Proxy.Example.COM", Port: 1080, Username: "u", Password: "p"}},
		{Name: "http", Type: TypeProxy, IPVersion: "force_ipv6", Settings: Settings{Protocol: "http", Server: "[2001:db8::5]", Port: 3128}},
		{Name: "ss2022", Type: TypeProxy, Settings: Settings{Protocol: "shadowsocks", Server: "203.0.113.5", Port: 8388, Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA==:AAAAAAAAAAAAAAAAAAAAAA=="}},
		{Name: "aead", Type: TypeProxy, Settings: Settings{Protocol: "shadowsocks", Server: "203.0.113.5", Port: 8388, Method: "chacha20-ietf-poly1305", Password: "secret"}},
		{Name: "vless", Type: TypeProxy, IPVersion: "force_ipv4", Settings: Settings{Protocol: "vless", Server: "198.51.100.7", Port: 443, UUID: "D77C8B43-40ED-42CD-9150-F3F47D0F534D", Flow: "xtls-rprx-vision", RealityServerName: "www.example.com", RealityPublicKey: testPBK, RealityShortID: "0a1b"}},
		{Name: "上游", Type: TypeLandingNode, Settings: Settings{LandingNodeID: 3, ViaForwardID: 9}},
	}
	for _, o := range good {
		o := o
		if err := NormalizeOutbound(&o); err != nil {
			t.Errorf("%s: %v", o.Name, err)
		}
	}
	d := good[0]
	_ = NormalizeOutbound(&d)
	if d.Settings.Server != "" {
		t.Fatal("direct outbound kept foreign fields")
	}
	w := good[1]
	_ = NormalizeOutbound(&w)
	if w.Settings.WarpMode != "auto" || w.Settings.PrivateKey != "" {
		t.Fatal("auto WARP kept manual keys")
	}
	s := good[4]
	_ = NormalizeOutbound(&s)
	if s.Settings.Server != "proxy.example.com" || s.IPVersion != "system" {
		t.Fatalf("host/ip version not normalized: %+v", s)
	}
	v := good[8]
	_ = NormalizeOutbound(&v)
	if v.Settings.UUID != "d77c8b43-40ed-42cd-9150-f3f47d0f534d" || v.Settings.RealityFingerprint != "chrome" {
		t.Fatalf("vless not normalized: %+v", v.Settings)
	}
	bad := map[string]Outbound{
		"empty name":            {Type: TypeDirect},
		"bad type":              {Name: "x", Type: "vmess"},
		"bad ip version":        {Name: "x", Type: TypeDirect, IPVersion: "v4"},
		"private upstream":      {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "socks", Server: "192.168.1.1", Port: 1080}},
		"metadata upstream":     {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "169.254.169.254", Port: 80}},
		"loopback upstream":     {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "127.0.0.1", Port: 80}},
		"cgnat upstream":        {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "100.64.1.1", Port: 80}},
		"ula upstream":          {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "fd00::1", Port: 80}},
		"bare host":             {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "localhost", Port: 80}},
		"bad port":              {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "http", Server: "203.0.113.5", Port: 70000}},
		"forced family literal": {Name: "x", Type: TypeProxy, IPVersion: "force_ipv4", Settings: Settings{Protocol: "http", Server: "2001:db8::1", Port: 80}},
		"ss2022 key length":     {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "shadowsocks", Server: "203.0.113.5", Port: 1, Method: "2022-blake3-aes-256-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA=="}},
		"ss bad method":         {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "shadowsocks", Server: "203.0.113.5", Port: 1, Method: "rc4-md5", Password: "x"}},
		"vless no reality":      {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "vless", Server: "203.0.113.5", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d"}},
		"vless bad flow":        {Name: "x", Type: TypeProxy, Settings: Settings{Protocol: "vless", Server: "203.0.113.5", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-direct", RealityServerName: "a.com", RealityPublicKey: testPBK}},
		"warp bad license":      {Name: "x", Type: TypeWarp, Settings: Settings{WarpLicense: "nope"}},
		"warp manual key":       {Name: "x", Type: TypeWarp, Settings: Settings{WarpMode: "manual", PrivateKey: "short", PeerPublicKey: testKey32, AddressV4: "172.16.0.2"}},
		"warp manual family":    {Name: "x", Type: TypeWarp, IPVersion: "force_ipv4", Settings: Settings{WarpMode: "manual", PrivateKey: testKey32, PeerPublicKey: testKey32, AddressV6: "2606::2"}},
		"warp private endpoint": {Name: "x", Type: TypeWarp, Settings: Settings{WarpMode: "manual", PrivateKey: testKey32, PeerPublicKey: testKey32, AddressV4: "172.16.0.2", Endpoint: "10.0.0.1:2408"}},
		"landing node missing":  {Name: "x", Type: TypeLandingNode},
	}
	for name, o := range bad {
		o := o
		if err := NormalizeOutbound(&o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNormalizeProfile(t *testing.T) {
	outs := map[int64]bool{7: true}
	p := Profile{Name: "AI", DefaultAction: "outbound:7", Rules: []Rule{
		{Name: "AI 解锁", Enabled: true, Domains: []string{"geosite:OpenAI", "Claude.AI", "geosite:openai", "full:x.ai", "*.Gemini.Google.com", "domain:*.claude.ai"}, Action: "outbound:7"},
		{Enabled: true, IPs: []string{"1.2.3.4", "10.0.0.0/8", "::ffff:8.8.8.0/120", "GeoIP:CN"}, Ports: " 443, 8000-9000 ", Network: "tcp,udp", Protocols: []string{"tls", "http", "tls"}, Action: "block"},
		{Enabled: true, SourceIPs: []string{"geoip:!cn"}, LandingNodeIDs: []int64{5, 3, 5}, Action: "direct"},
	}}
	if err := NormalizeProfile(&p, outs); err != nil {
		t.Fatal(err)
	}
	r0, r1, r2 := p.Rules[0], p.Rules[1], p.Rules[2]
	if strings.Join(r0.Domains, ",") != "geosite:openai,domain:claude.ai,full:x.ai,domain:gemini.google.com" || r0.ID == "" {
		t.Fatalf("domains = %v", r0.Domains)
	}
	if strings.Join(r1.IPs, ",") != "1.2.3.4,10.0.0.0/8,8.8.8.0/24,geoip:cn" || r1.Ports != "443,8000-9000" || r1.Network != "" || strings.Join(r1.Protocols, ",") != "http,tls" || r1.Name != "规则 2" {
		t.Fatalf("rule 2 = %+v", r1)
	}
	if strings.Join(r2.SourceIPs, ",") != "geoip:!cn" || len(r2.LandingNodeIDs) != 2 || r2.LandingNodeIDs[0] != 3 {
		t.Fatalf("rule 3 = %+v", r2)
	}
	bad := map[string]Profile{
		"unknown geosite":   {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"geosite:not-a-real-list"}, Action: "block"}}},
		"unknown geoip":     {Name: "x", Rules: []Rule{{Enabled: true, IPs: []string{"geoip:zz-nope"}, Action: "block"}}},
		"too large geosite": {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"geosite:category-ads-all"}, Action: "block"}}},
		"no matcher":        {Name: "x", Rules: []Rule{{Enabled: true, Action: "block"}}},
		"bad action":        {Name: "x", Rules: []Rule{{Enabled: true, Ports: "25", Action: "outbound:99"}}},
		"bad default":       {Name: "x", DefaultAction: "outbound:99"},
		"bad port":          {Name: "x", Rules: []Rule{{Enabled: true, Ports: "0", Action: "block"}}},
		"bad network":       {Name: "x", Rules: []Rule{{Enabled: true, Network: "icmp", Ports: "1", Action: "block"}}},
		"bad protocol":      {Name: "x", Rules: []Rule{{Enabled: true, Protocols: []string{"ssh"}, Action: "block"}}},
		"ext file":          {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"ext:evil.dat:x"}, Action: "block"}}},
		"bad regexp":        {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"regexp:("}, Action: "block"}}},
		"inner wildcard":    {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"a.*.example.com"}, Action: "block"}}},
		"full wildcard":     {Name: "x", Rules: []Rule{{Enabled: true, Domains: []string{"full:*.example.com"}, Action: "block"}}},
		"dup rule id":       {Name: "x", Rules: []Rule{{ID: "a", Enabled: true, Ports: "1", Action: "block"}, {ID: "a", Enabled: true, Ports: "2", Action: "block"}}},
	}
	for name, p := range bad {
		p := p
		if err := NormalizeProfile(&p, outs); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	many := Profile{Name: "many"}
	for i := 0; i <= MaxProfileRules; i++ {
		many.Rules = append(many.Rules, Rule{Enabled: true, Ports: "25", Action: "block"})
	}
	if err := NormalizeProfile(&many, outs); err == nil {
		t.Fatal("rule limit not enforced")
	}
	// geosite:cn (≈110k domains, 1.96 MB) fits the per-rule budget.
	cn := Profile{Name: "cn", Rules: []Rule{{Enabled: true, Domains: []string{"geosite:cn"}, Action: "block"}}}
	if err := NormalizeProfile(&cn, outs); err != nil {
		t.Fatal(err)
	}
}

func ptr(v int64) *int64 { return &v }

func cnRules() []Rule {
	return []Rule{
		{ID: "cn-ip", Name: "屏蔽大陆 IP", Enabled: true, IPs: []string{"geoip:cn"}, Action: "block"},
		{ID: "cn-site", Name: "屏蔽大陆网站", Enabled: true, Domains: []string{"geosite:cn"}, Action: "block"},
		{ID: "cn-src", Name: "禁止大陆直连", Enabled: true, SourceIPs: []string{"geoip:cn"}, Action: "block"},
	}
}

func TestCompileGroupsMergesAndScopes(t *testing.T) {
	profiles := map[int64]Profile{
		1: {ID: 1, Name: "A", DefaultAction: ActionDirect, Rules: append(cnRules(), Rule{ID: "a1", Enabled: true, Ports: "25", LandingNodeIDs: []int64{14, 99}, Action: "block"}, Rule{ID: "off", Enabled: false, Ports: "1", Action: "block"})},
		2: {ID: 2, Name: "B", DefaultAction: "outbound:5", Rules: append(cnRules(), Rule{ID: "ai", Enabled: true, Domains: []string{"geosite:openai"}, Action: "outbound:6"})},
	}
	resolved := map[int64]PlanOutbound{5: {Type: "socks", IPVersion: "system", Server: "203.0.113.9", Port: 1080}}
	exemptCalls := 0
	in := CompileInput{
		ServerProfileID: 1,
		Nodes:           []NodeRef{{ID: 11, Tag: "flvxl-11"}, {ID: 12, Tag: "flvxl-12", Override: ptr(2)}, {ID: 13, Tag: "flvxl-13", Override: ptr(0)}, {ID: 14, Tag: "flvxl-14"}},
		Profiles:        profiles,
		Resolve: func(id int64) (PlanOutbound, error) {
			if o, ok := resolved[id]; ok {
				return o, nil
			}
			return PlanOutbound{}, errors.New("WARP 尚未注册")
		},
		Exempt:  func() ([]string, error) { exemptCalls++; return []string{"203.0.113.1/32"}, nil },
		GeoIP:   true,
		GeoSite: true,
	}
	plan, warnings, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rules) != 6 {
		b, _ := json.MarshalIndent(plan.Rules, "", " ")
		t.Fatalf("rules = %s", b)
	}
	for i := 0; i < 3; i++ {
		if got := strings.Join(plan.Rules[i].InboundTags, ","); got != "flvxl-11,flvxl-12,flvxl-14" {
			t.Fatalf("shared CN rule %d not merged across profiles: %s", i, got)
		}
	}
	for _, r := range plan.Rules {
		for _, tag := range r.InboundTags {
			if tag == "flvxl-13" {
				t.Fatal("node on the built-in default got a rule")
			}
		}
		if r.Outbound == ActionDirect && r.Name == "默认出站" {
			t.Fatal("direct default emitted a redundant rule")
		}
	}
	var scoped, ai, def *PlanRule
	for i := range plan.Rules {
		r := &plan.Rules[i]
		switch {
		case r.Port == "25":
			scoped = r
		case len(r.Domain) == 1 && r.Domain[0] == "geosite:openai":
			ai = r
		case r.Name == "默认出站":
			def = r
		}
	}
	if scoped == nil || strings.Join(scoped.InboundTags, ",") != "flvxl-14" {
		t.Fatalf("适用落地节点 scope wrong: %+v", scoped)
	}
	if ai == nil || ai.Outbound != ActionBlock || len(warnings) != 1 {
		t.Fatalf("unresolvable outbound must fail closed: %+v %v", ai, warnings)
	}
	if def == nil || def.Outbound != "flvxo-5" || strings.Join(def.InboundTags, ",") != "flvxl-12" {
		t.Fatalf("profile default rule: %+v", def)
	}
	if len(plan.Outbounds) != 1 || plan.Outbounds[0].Tag != "flvxo-5" {
		t.Fatalf("outbounds = %+v", plan.Outbounds)
	}
	if exemptCalls != 1 || len(plan.ExemptSources) != 1 {
		t.Fatal("exempt sources missing although a source rule exists")
	}
	// Geo capability gating (shared servers without geo data).
	in.GeoSite = false
	if _, _, err := Compile(in); err == nil {
		t.Fatal("geosite rule accepted without geosite data")
	}
	in.GeoSite = true
	// No source rule → no exemption lookup.
	profiles[1] = Profile{ID: 1, Name: "A", DefaultAction: ActionDirect, Rules: []Rule{{ID: "p", Enabled: true, Ports: "25", Action: "block"}}}
	profiles[2] = Profile{ID: 2, Name: "B", DefaultAction: ActionDirect}
	exemptCalls = 0
	plan, _, err = Compile(in)
	if err != nil || exemptCalls != 0 || len(plan.ExemptSources) != 0 {
		t.Fatalf("exemption fetched without source rules: %v %d", err, exemptCalls)
	}
	// Everything on the default → inactive plan.
	in.ServerProfileID = 0
	in.Nodes = []NodeRef{{ID: 11, Tag: "flvxl-11"}}
	plan, _, _ = Compile(in)
	if plan.Active() {
		t.Fatal("default profile produced an active plan")
	}
}

func TestCompileLimits(t *testing.T) {
	profiles := map[int64]Profile{1: {ID: 1, Name: "many", DefaultAction: ActionDirect}}
	p := profiles[1]
	for i := int64(1); i <= 9; i++ {
		p.Rules = append(p.Rules, Rule{ID: "r" + strings.Repeat("x", int(i)), Enabled: true, Ports: "25", Action: "outbound:" + string(rune('0'+i))})
	}
	profiles[1] = p
	in := CompileInput{ServerProfileID: 1, Nodes: []NodeRef{{ID: 1, Tag: "flvxl-1"}}, Profiles: profiles, Resolve: func(id int64) (PlanOutbound, error) { return PlanOutbound{Type: "direct", IPVersion: "system"}, nil }}
	if _, _, err := Compile(in); err == nil || !strings.Contains(err.Error(), "出站") {
		t.Fatalf("outbound limit: %v", err)
	}
	// Two different profiles each with geosite:cn exceed the per-server budget.
	profiles = map[int64]Profile{
		1: {ID: 1, Name: "a", DefaultAction: ActionDirect, Rules: []Rule{{ID: "x", Enabled: true, Domains: []string{"geosite:cn"}, Ports: "80", Action: "block"}}},
		2: {ID: 2, Name: "b", DefaultAction: ActionDirect, Rules: []Rule{{ID: "y", Enabled: true, Domains: []string{"geosite:cn"}, Ports: "443", Action: "block"}}},
	}
	in = CompileInput{ServerProfileID: 1, Nodes: []NodeRef{{ID: 1, Tag: "flvxl-1"}, {ID: 2, Tag: "flvxl-2", Override: ptr(2)}}, Profiles: profiles, Resolve: in.Resolve, GeoSite: true}
	if _, _, err := Compile(in); err == nil {
		t.Fatal("geo budget not enforced")
	}
	// Identical leading rules merge, so the same preset in both fits.
	profiles[2] = Profile{ID: 2, Name: "b", DefaultAction: ActionBlock, Rules: profiles[1].Rules}
	if plan, _, err := Compile(in); err != nil || len(plan.Rules) != 2 {
		t.Fatalf("merged preset should fit: %v %+v", err, plan)
	}
}

func TestLabels(t *testing.T) {
	outs := map[int64]Outbound{1: {ID: 1, Type: TypeWarp}, 2: {ID: 2, Type: TypeDirect, IPVersion: "force_ipv6"}, 3: {ID: 3, Type: TypeProxy}, 4: {ID: 4, Type: TypeLandingNode}}
	profiles := map[int64]Profile{
		10: {DefaultAction: "outbound:1"}, 11: {DefaultAction: "outbound:2"}, 12: {DefaultAction: "outbound:3"}, 13: {DefaultAction: "outbound:4"},
		14: {DefaultAction: ActionBlock}, 15: {DefaultAction: ActionDirect, Rules: []Rule{{Enabled: true}}}, 16: {DefaultAction: "outbound:1", Rules: []Rule{{Enabled: false}}},
	}
	want := map[int64]string{0: "直连", 10: "WARP", 11: "强制 IPv6", 12: "代理", 13: "FLVX 落地", 14: "封锁", 15: "按规则分流", 16: "WARP", 99: "直连"}
	for id, label := range want {
		if got := Label(id, profiles, outs); got != label {
			t.Errorf("profile %d label %q want %q", id, got, label)
		}
	}
}

func TestParseLink(t *testing.T) {
	// The exact format FLVX generates for its own SS2022 landing links.
	settings, name, err := ParseLink("ss://2022-blake3-aes-128-gcm:AAAAAAAAAAAAAAAAAAAAAA%3D%3D%3ABBBBBBBBBBBBBBBBBBBBBB%3D%3D@203.0.113.5:30001#%E6%97%A5%E6%9C%AC")
	if err != nil || settings.Method != "2022-blake3-aes-128-gcm" || settings.Password != "AAAAAAAAAAAAAAAAAAAAAA==:BBBBBBBBBBBBBBBBBBBBBB==" || settings.Port != 30001 || name != "日本" {
		t.Fatalf("SIP022: %+v %q %v", settings, name, err)
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pa:ss"))
	settings, _, err = ParseLink("ss://" + userinfo + "@[2001:db8::7]:8388/?group=x#n")
	if err != nil || settings.Method != "aes-256-gcm" || settings.Password != "pa:ss" || settings.Server != "2001:db8::7" {
		t.Fatalf("SIP002 base64: %+v %v", settings, err)
	}
	legacy := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@example.com:8389"))
	settings, _, err = ParseLink("ss://" + legacy + "#legacy")
	if err != nil || settings.Server != "example.com" || settings.Port != 8389 {
		t.Fatalf("legacy: %+v %v", settings, err)
	}
	if _, _, err := ParseLink("ss://" + userinfo + "@1.2.3.4:1?plugin=obfs-local"); err == nil {
		t.Fatal("plugin link accepted")
	}
	settings, name, err = ParseLink("vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@198.51.100.7:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=" + testPBK + "&sid=0a1b&type=tcp#hk")
	if err != nil || settings.Protocol != "vless" || settings.RealityPublicKey != testPBK || settings.RealityShortID != "0a1b" || settings.Flow != "xtls-rprx-vision" || name != "hk" {
		t.Fatalf("vless: %+v %v", settings, err)
	}
	o := Outbound{Name: name, Type: TypeProxy, Settings: settings}
	if err := NormalizeOutbound(&o); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@1.2.3.4:443?security=tls&type=tcp", "vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@1.2.3.4:443?security=reality&type=ws", "vmess://abc", "trojan://x@y:1"} {
		if _, _, err := ParseLink(link); err == nil {
			t.Errorf("%s accepted", link)
		}
	}
}

func TestExemptSources(t *testing.T) {
	resolve := func(host string) []netip.Addr {
		if host == "node.example.com" {
			return []netip.Addr{netip.MustParseAddr("198.51.100.20"), netip.MustParseAddr("2001:db8::20")}
		}
		return nil
	}
	got := ExemptSources([]string{"203.0.113.1", "Node.Example.COM", "", "203.0.113.1", "[2001:db8::1]", "203.0.113.7, 203.0.113.8", "unresolvable.example.org"}, []string{"192.0.2.0/24", "bad", "2001:db8:5::7"}, resolve)
	// Plan 149: node IPv6 addresses (literal or resolved) are exempt as their
	// /64; whitelist entries stay exact.
	want := "192.0.2.0/24,198.51.100.20/32,2001:db8:5::7/128,2001:db8::/64,203.0.113.1/32,203.0.113.7/32,203.0.113.8/32"
	if strings.Join(got, ",") != want {
		t.Fatalf("exempt = %v", got)
	}
	if list, err := NormalizeWhitelist([]string{"1.2.3.4", "10.0.0.0/8", "10.0.0.0/8", " "}); err != nil || strings.Join(list, ",") != "1.2.3.4/32,10.0.0.0/8" {
		t.Fatalf("whitelist = %v %v", list, err)
	}
	if _, err := NormalizeWhitelist([]string{"0.0.0.0/0"}); err == nil {
		t.Fatal("whole-internet whitelist accepted")
	}
}

// Mock Cloudflare client API: registration, WARP+ license and deletion.
func TestRegisterWarpMock(t *testing.T) {
	var licensed, deleted bool
	failLicense := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Client-Version") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "1.1.1.1/") {
			w.WriteHeader(403)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/reg":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			key, _ := base64.StdEncoding.DecodeString(body["key"].(string))
			if len(key) != 32 || body["tos"] == "" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"id":"dev-1","token":"tok-1","account":{"account_type":"free"},"config":{"client_id":"D152","peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=","endpoint":{"v4":"162.159.192.6:0","v6":"[2606:4700:d0::a29f:c006]:0","host":"engage.cloudflareclient.com:2408"}}],"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:8414::2"}}}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/reg/dev-1/account":
			if r.Header.Get("Authorization") != "Bearer tok-1" || failLicense {
				w.WriteHeader(403)
				return
			}
			licensed = true
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/reg/dev-1":
			_, _ = w.Write([]byte(`{"id":"dev-1","account":{"account_type":"limited"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/reg/dev-1":
			deleted = r.Header.Get("Authorization") == "Bearer tok-1"
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := WarpAPIBase
	WarpAPIBase = srv.URL
	defer func() { WarpAPIBase = old }()
	acct, err, licErr := RegisterWarp(context.Background(), srv.Client(), "2Ia7f3S4-r56H8aT3-0Jz4F9i2")
	if err != nil || licErr != nil || !licensed || acct.AccountType != "limited" {
		t.Fatalf("register: %+v %v %v", acct, err, licErr)
	}
	reserved, _ := base64.StdEncoding.DecodeString("D152")
	if len(acct.Reserved) != 3 || acct.Reserved[0] != int(reserved[0]) || acct.EndpointV4 != "162.159.192.6:2408" || acct.EndpointV6 != "[2606:4700:d0::a29f:c006]:2408" || acct.AddressV4 != "172.16.0.2" {
		t.Fatalf("account fields: %+v", acct)
	}
	priv, _ := base64.StdEncoding.DecodeString(acct.PrivateKey)
	if len(priv) != 32 || acct.Token != "tok-1" || acct.DeviceID != "dev-1" {
		t.Fatal("device credentials missing")
	}
	failLicense = true
	acct, err, licErr = RegisterWarp(context.Background(), srv.Client(), "2Ia7f3S4-r56H8aT3-0Jz4F9i2")
	if err != nil || licErr == nil || acct.PrivateKey == "" {
		t.Fatalf("license failure must keep the free account: %v %v", err, licErr)
	}
	if err := DeleteWarp(context.Background(), srv.Client(), "dev-1", "tok-1"); err != nil || !deleted {
		t.Fatalf("delete: %v", err)
	}
	WarpAPIBase = srv.URL + "/unreachable"
	if _, err, _ := RegisterWarp(context.Background(), srv.Client(), ""); err == nil {
		t.Fatal("API error not reported")
	}
}

func TestBlockPlanAndGeoTable(t *testing.T) {
	if BlockPlan(nil, false) != nil {
		t.Fatal("empty block plan")
	}
	p := BlockPlan([]string{"flvxl-2", "flvxl-1"}, true)
	if !p.Active() || p.Rules[0].Outbound != ActionBlock || strings.Join(p.Rules[0].InboundTags, ",") != "flvxl-1,flvxl-2" || !p.AllowCoreRestart {
		t.Fatalf("block plan = %+v", p)
	}
	for _, code := range []string{"geosite:cn", "geosite:openai", "geosite:category-ads", "geosite:netflix", "geosite:speedtest", "geosite:tiktok", "geosite:google", "geoip:cn", "geoip:!cn", "geoip:private"} {
		if ok, bytes, _ := GeoEntry(code); !ok || bytes <= 0 {
			t.Errorf("%s missing from the pinned geo table", code)
		}
	}
	if ok, _, _ := GeoEntry("geosite:nope-nope"); ok {
		t.Fatal("unknown geosite reported as known")
	}
}
