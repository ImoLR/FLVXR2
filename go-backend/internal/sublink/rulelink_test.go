package sublink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Plan 261 「规则集链接」.

func mustList(t *testing.T, text string) *LinkRules {
	t.Helper()
	r, err := ParseRuleList([]byte(text))
	if err != nil {
		t.Fatalf("ParseRuleList: %v\n%s", err, text)
	}
	return r
}

func linkLines(r *LinkRules) string { return string(LinkText(r.Rules)) }

func TestParseRuleListShapes(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		counts     LinkCounts
	}{
		{"clash classical", "# comment\nDOMAIN-SUFFIX,google.com,Proxy\nDOMAIN,www.example.com\nDOMAIN-KEYWORD,openai\nIP-CIDR,91.108.4.0/22,Proxy,no-resolve\nIP-CIDR6,2001:b28:f23d::/48,no-resolve\n",
			"DOMAIN-SUFFIX,google.com\nDOMAIN,www.example.com\nDOMAIN-KEYWORD,openai\nIP-CIDR,91.108.4.0/22\nIP-CIDR6,2001:b28:f23d::/48\n",
			LinkCounts{Domain: 1, Suffix: 1, Keyword: 1, IP: 2}},
		{"surge list with policy + options", "DOMAIN-SUFFIX,apple.com,DIRECT,extended-matching\nIP-CIDR,17.0.0.0/8,DIRECT,no-resolve\nUSER-AGENT,*Music*\nPROCESS-NAME,Telegram\n",
			"DOMAIN-SUFFIX,apple.com\nIP-CIDR,17.0.0.0/8\n", LinkCounts{Suffix: 1, IP: 1, Ignored: 2}},
		{"quantumult x aliases", "host-suffix, youtube.com, proxy\nhost, m.youtube.com, proxy\nhost-keyword, ytimg, proxy\nip6-cidr, 2404:6800::/32, proxy\nip-cidr, 8.8.8.0/24, proxy\nuser-agent, x*, reject\n",
			"DOMAIN-SUFFIX,youtube.com\nDOMAIN,m.youtube.com\nDOMAIN-KEYWORD,ytimg\nIP-CIDR6,2404:6800::/32\nIP-CIDR,8.8.8.0/24\n", LinkCounts{Domain: 1, Suffix: 1, Keyword: 1, IP: 2, Ignored: 1}},
		{"yaml payload classical", "payload:\n  - DOMAIN-SUFFIX,github.com\n  - 'DOMAIN,api.github.com'\n  - \"IP-CIDR,140.82.112.0/20,no-resolve\"\n  # comment\n  - PROCESS-NAME,git\n",
			"DOMAIN-SUFFIX,github.com\nDOMAIN,api.github.com\nIP-CIDR,140.82.112.0/20\n", LinkCounts{Domain: 1, Suffix: 1, IP: 1, Ignored: 1}},
		{"yaml domain behavior", "payload:\n  - '+.netflix.com'\n  - '.nflxvideo.net'\n  - 'fast.com'\n  - '*.nflximg.com'\n  - '+.stun.*.*'\n  - 'a*b.com'\n",
			"DOMAIN-SUFFIX,netflix.com\nDOMAIN-SUFFIX,nflxvideo.net\nDOMAIN,fast.com\nDOMAIN-SUFFIX,nflximg.com\n", LinkCounts{Domain: 1, Suffix: 3, Ignored: 2}},
		{"yaml ipcidr behavior", "payload:\n  - '1.0.1.0/24'\n  - '2001:250::/30'\n  - '1.2.3.4'\n", "IP-CIDR,1.0.1.0/24\nIP-CIDR6,2001:250::/30\nIP-CIDR,1.2.3.4/32\n", LinkCounts{IP: 3}},
		{"plain list", "; comment\n// comment\n! adblock comment\n+.example.org\nexample.net\n10.0.0.0/8\n::ffff:1.2.3.0/120\n", "DOMAIN-SUFFIX,example.org\nDOMAIN,example.net\nIP-CIDR,10.0.0.0/8\nIP-CIDR,1.2.3.0/24\n", LinkCounts{Domain: 1, Suffix: 1, IP: 2}},
		{"crlf + bom + trailing comments + sections", "\ufeff[Rule]\r\nDOMAIN-SUFFIX,Example.COM # mine\r\nDOMAIN,a.example.com // x\r\n\r\n", "DOMAIN-SUFFIX,example.com\nDOMAIN,a.example.com\n", LinkCounts{Domain: 1, Suffix: 1}},
		{"dropped rule types", "DOMAIN-REGEX,^ad\\.\nURL-REGEX,^http://x\nGEOIP,CN\nGEOSITE,cn\nRULE-SET,https://x/y.list,DIRECT\nAND,((DOMAIN,x.com),(DST-PORT,443)),REJECT\nSCRIPT,x\nDST-PORT,443\nSRC-IP-CIDR,1.0.0.0/8\nDOMAIN-WILDCARD,*.x.com\nMATCH,DIRECT\nFINAL,DIRECT\nDOMAIN-SUFFIX,kept.example\n",
			"DOMAIN-SUFFIX,kept.example\n", LinkCounts{Suffix: 1, Ignored: 12}},
		{"dedupe + single-label suffix", "DOMAIN-SUFFIX,cn\nDOMAIN-SUFFIX,cn,DIRECT\n+.cn\nDOMAIN,cn\n", "DOMAIN-SUFFIX,cn\nDOMAIN,cn\n", LinkCounts{Domain: 1, Suffix: 1}},
		{"idn", "DOMAIN-SUFFIX,例子.测试\nDOMAIN-SUFFIX,xn--fsqu00a.xn--0zwm56d\nＤＯＭＡＩＮ,x.com\n", "DOMAIN-SUFFIX,xn--fsqu00a.xn--0zwm56d\n", LinkCounts{Suffix: 1, Ignored: 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := mustList(t, c.in)
			if got := linkLines(r); got != c.want {
				t.Fatalf("rules\n%s\nwant\n%s", got, c.want)
			}
			if r.Counts != c.counts {
				t.Fatalf("counts %+v want %+v", r.Counts, c.counts)
			}
		})
	}
}

// Nothing a list holds can break out of the generated YAML / JSON / conf:
// every kept value passed the 自己写 validators.
func TestParseRuleListInjection(t *testing.T) {
	bad := []string{
		"DOMAIN,evil.com\"\nMATCH,DIRECT",
		"DOMAIN-SUFFIX,x.com,DIRECT\"]}",
		"DOMAIN,a]b.com", "DOMAIN,a}b.com", "DOMAIN,a{b.com", "DOMAIN,a'b.com",
		"- 'a: b'", "- 'x.com: y'", "DOMAIN,x.com: y",
		"DOMAIN-KEYWORD,a b", "DOMAIN-KEYWORD,a\tb", "DOMAIN-KEYWORD,#x",
		"DOMAIN,x.com\u2028y", "DOMAIN,x.com\rMATCH", "DOMAIN-SUFFIX,..com", "DOMAIN-SUFFIX,-",
		"IP-CIDR,1.2.3.4/33", "IP-CIDR,1.2.3.4/24\"", "IP-CIDR,fe80::1%eth0/64",
		"DOMAIN-SUFFIX," + strings.Repeat("a", 254),
		"DOMAIN,x.com\x00",
	}
	for _, line := range bad {
		r, err := ParseRuleList([]byte(line + "\nDOMAIN,ok.example\n"))
		if errors.Is(err, ErrLinkBinary) && strings.Contains(line, "\x00") {
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		for _, rule := range r.Rules {
			if strings.ContainsAny(rule.Value, "\"'[]{}: \t\r\n#,\u2028") || !validDomain(rule.Value) {
				t.Fatalf("%q kept %+v", line, rule)
			}
		}
		text := string(LinkText(r.Rules))
		if strings.Contains(text, "MATCH") || strings.Contains(text, "evil") {
			t.Fatalf("%q leaked: %q", line, text)
		}
	}
}

func TestParseRuleListRejects(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
		want error
	}{
		{"nul", []byte("DOMAIN,x.com\n\x00\x01"), ErrLinkBinary},
		{"invalid utf8", []byte("DOMAIN,x.com\n\xff\xfe"), ErrLinkBinary},
		{"mrs (zstd)", append([]byte{0x28, 0xB5, 0x2F, 0xFD}, []byte("DOMAIN,x.com")...), ErrLinkBinary},
		{"srs", append([]byte("SRS\x03"), []byte("DOMAIN,x.com")...), ErrLinkBinary},
		{"gzip", []byte{0x1f, 0x8b, 0x08, 0x00, 'x'}, ErrLinkBinary},
		{"html", []byte("  <!DOCTYPE html><html><body>DOMAIN,x.com</body></html>"), ErrLinkHTML},
		{"empty", []byte(""), ErrLinkEmpty},
		{"comments only", []byte("# a\n// b\npayload:\n"), ErrLinkEmpty},
		{"unsupported only", []byte("GEOIP,CN\nPROCESS-NAME,x\n"), ErrLinkEmpty},
		{"too big", bytes.Repeat([]byte("#\n"), MaxLinkBytes/2+1), ErrLinkTooBig},
	} {
		if _, err := ParseRuleList(c.in); !errors.Is(err, c.want) {
			t.Fatalf("%s: err %v want %v", c.name, err, c.want)
		}
	}
	// "SRS" as the start of a text line is not the binary magic.
	if _, err := ParseRuleList([]byte("SRS.example.com\n")); err != nil {
		t.Fatalf("text starting with SRS: %v", err)
	}
}

func TestParseRuleListRuleCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxLinkRules; i++ {
		fmt.Fprintf(&b, "DOMAIN-SUFFIX,d%d.example\n", i)
	}
	b.WriteString("DOMAIN-SUFFIX,d0.example\n") // a duplicate does not count
	r, err := ParseRuleList([]byte(b.String()))
	if err != nil || len(r.Rules) != MaxLinkRules {
		t.Fatalf("at the cap: %v %d", err, len(r.Rules))
	}
	b.WriteString("DOMAIN-SUFFIX,one-more.example\n")
	if _, err := ParseRuleList([]byte(b.String())); !errors.Is(err, ErrLinkTooMany) {
		t.Fatalf("over the cap: %v", err)
	}
}

func TestLinkTextRoundTrip(t *testing.T) {
	r := mustList(t, "DOMAIN-SUFFIX,google.com\nDOMAIN,a.b.c\nDOMAIN-KEYWORD,ab.c\nIP-CIDR,1.2.3.0/24\nIP-CIDR6,2001:db8::/32\n")
	text := LinkText(r.Rules)
	back := ParseLinkText(string(text))
	if !bytes.Equal(LinkText(back), text) {
		t.Fatalf("round trip\n%s\n%s", LinkText(back), text)
	}
	// A damaged stored row never emits anything but validated lines.
	dirty := string(text) + "MATCH,DIRECT\nDOMAIN,evil\"\nRULE-SET,x,y\nDOMAIN-SUFFIX\n"
	if got := LinkText(ParseLinkText(dirty)); !bytes.Equal(got, text) {
		t.Fatalf("dirty row: %s", got)
	}
	var js struct {
		Version int `json:"version"`
		Rules   []struct {
			Domain        []string `json:"domain"`
			DomainSuffix  []string `json:"domain_suffix"`
			DomainKeyword []string `json:"domain_keyword"`
			IPCIDR        []string `json:"ip_cidr"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(LinkSingBoxJSON(back), &js); err != nil {
		t.Fatal(err)
	}
	if js.Version != LinkSourceVersion || len(js.Rules) != 1 || js.Rules[0].DomainSuffix[0] != "google.com" || js.Rules[0].Domain[0] != "a.b.c" ||
		js.Rules[0].DomainKeyword[0] != "ab.c" || len(js.Rules[0].IPCIDR) != 2 {
		t.Fatalf("json %+v", js)
	}
	if LinkHash(text) == LinkHash(append(text, 'x')) || len(LinkHash(text)) != 32 {
		t.Fatal("hash")
	}
}

func TestValidLinkID(t *testing.T) {
	for s, ok := range map[string]bool{
		"0123456789abcdef0123456789abcdef": true, "0123456789ABCDEF0123456789abcdef": false,
		"0123456789abcdef0123456789abcde": false, "../../etc/passwd0123456789abcdef": false, "": false,
	} {
		if ValidLinkID(s) != ok {
			t.Fatalf("%q", s)
		}
	}
}

// ------------------------------------------------------------ routing

const testLinkID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"

func linkRouting(nodeKey string) []RouteEntry {
	r := testRouting(nodeKey)
	link := []RouteEntry{
		{Type: RouteLink, Name: "流媒体合集", Ref: 7, Exit: ExitNodePrefix + nodeKey, Link: &LinkRef{PublicID: testLinkID}},
		{Type: RouteLink, Name: "拦截表", Ref: 8, Exit: ExitReject, Link: &LinkRef{PublicID: "ffffffffffffffffffffffffffffffff"}},
		// Without its stored set (deleted, other user's): left out.
		{Type: RouteLink, Name: "失效", Ref: 9, Exit: ExitDirect},
	}
	return append(r[:1], append(link, r[1:]...)...)
}

func TestGoldenLinkRouting(t *testing.T) {
	nodes := keyedNodes(t)
	routing := linkRouting(nodes[0].Key)
	for _, c := range []struct {
		name string
		fn   func() ([]byte, int)
	}{
		{"link-clash-cn.yaml", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
		{"link-stash-cn.yaml", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, true, routing, testOrigin) }},
		{"link-singbox-cn.json", func() ([]byte, int) { return SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing) }},
		{"link-surge-cn.conf", func() ([]byte, int) { return SurgeRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
		{"link-surfboard-cn.conf", func() ([]byte, int) { return SurgeRouted(nodes, ModeCNDirect, true, routing, testOrigin) }},
	} {
		body, _ := c.fn()
		checkGolden(t, c.name, body)
	}
}

func TestLinkRoutingRender(t *testing.T) {
	nodes := keyedNodes(t)
	routing := linkRouting(nodes[0].Key)
	url := testOrigin + "/s/rs/u/" + testLinkID
	// Clash: classical text provider DIRECT from the panel + RULE-SET line
	// with no-resolve, right after 公司 and before 巴哈姆特.
	d := parseClash(t, func() []byte { b, _ := ClashRouted(nodes, ModeCNDirect, false, routing, testOrigin); return b }())
	p := d.Providers["link-"+testLinkID]
	if p["behavior"] != "classical" || p["format"] != "text" || p["url"] != url+".list" || p["proxy"] != "DIRECT" || p["interval"] != RuleSetInterval {
		t.Fatalf("provider %v", p)
	}
	idx := map[string]int{}
	for i, r := range d.Rules {
		idx[r] = i
	}
	rs := "RULE-SET,link-" + testLinkID + ",📌 流媒体合集,no-resolve"
	i, ok := idx[rs]
	if !ok || i < idx["DOMAIN-SUFFIX,mycompany.com,📌 公司"] || i > idx["DOMAIN-SUFFIX,bahamut.com.tw,📌 巴哈姆特"] {
		t.Fatalf("rule order %v", d.Rules)
	}
	for _, r := range d.Rules {
		if strings.Contains(r, "失效") {
			t.Fatalf("entry without a set rendered: %s", r)
		}
	}
	found := false
	for _, g := range d.Groups {
		if g.Name == "📌 流媒体合集" {
			found = g.Proxies[0] == nodes[0].Name
		}
		if g.Name == "📌 失效" {
			t.Fatal("group without a set")
		}
	}
	if !found {
		t.Fatal("link group / node exit")
	}
	// Stash: no proxy key.
	ds := parseClash(t, func() []byte { b, _ := ClashRouted(nodes, ModeCNDirect, true, routing, testOrigin); return b }())
	if _, has := ds.Providers["link-"+testLinkID]["proxy"]; has {
		t.Fatal("stash provider proxy")
	}
	// sing-box: source rule set from the panel, direct.
	sb, _ := SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing)
	if !bytes.Contains(sb, []byte(`"tag": "link-`+testLinkID+`",
        "format": "source",
        "url": "`+url+`.json",
        "download_detour": "direct"`)) {
		t.Fatalf("sing-box rule set:\n%s", sb)
	}
	var cfg map[string]any
	if err := json.Unmarshal(sb, &cfg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(sb, []byte(`"rule_set": [
          "link-ffffffffffffffffffffffffffffffff"
        ],
        "action": "reject"`)) {
		t.Fatalf("reject link:\n%s", sb)
	}
	// Surge: RULE-SET URL with no-resolve; Surfboard without.
	sg, _ := SurgeRouted(nodes, ModeCNDirect, false, routing, testOrigin)
	if !bytes.Contains(sg, []byte("RULE-SET,"+url+".list,📌 流媒体合集,no-resolve\n")) {
		t.Fatalf("surge:\n%s", sg)
	}
	sf, _ := SurgeRouted(nodes, ModeCNDirect, true, routing, testOrigin)
	if !bytes.Contains(sf, []byte("RULE-SET,"+url+".list,📌 流媒体合集\n")) {
		t.Fatalf("surfboard:\n%s", sf)
	}
	// Loon / QX / base64 are node lists: unchanged (like 自己写).
	// No origin: link entries are left out (nothing to download from).
	noOrigin, _ := ClashRouted(nodes, ModeCNDirect, false, routing, "")
	if bytes.Contains(noOrigin, []byte("link-")) {
		t.Fatal("link without origin")
	}
}

func TestCleanRoutingLinks(t *testing.T) {
	owned := map[int64]bool{7: true, 8: true, 9: true, 10: true, 11: true, 12: true}
	linkOK := func(ref int64) bool { return owned[ref] }
	inSub := func(string) bool { return false }
	in := []RouteEntry{
		{Type: RouteBuiltin, Key: "ai"},
		{Type: RouteLink, Name: " 合集 ", Ref: 7, Exit: "", Link: &LinkRef{PublicID: testLinkID}, Lines: []string{"x.com"}},
		{Type: RouteCustom, Name: "公司", Lines: []string{"a.com"}, Exit: ExitDirect},
	}
	out, err := CleanRoutingLinks(in, inSub, nil, linkOK)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Type != RouteLink || out[0].Name != "合集" || out[0].Ref != 7 || out[0].Exit != ExitSelect || out[0].Link != nil || out[0].Lines != nil || out[1].Type != RouteCustom {
		t.Fatalf("clean %+v", out)
	}
	js := RoutingJSON(out)
	if !strings.Contains(js, `{"type":"link","name":"合集","exit":"select","ref":7}`) {
		t.Fatalf("json %s", js)
	}
	back := ParseRouting(&js)
	if back[0].Ref != 7 || back[0].Type != RouteLink {
		t.Fatalf("parse %+v", back)
	}
	for _, c := range []struct {
		in   []RouteEntry
		want string
	}{
		{[]RouteEntry{{Type: RouteLink, Name: "a", Ref: 99}}, "已失效"},
		{[]RouteEntry{{Type: RouteLink, Name: "a", Ref: 0}}, "已失效"},
		{[]RouteEntry{{Type: RouteLink, Name: "a", Ref: 7}, {Type: RouteLink, Name: "b", Ref: 7}}, "已经在列表里"},
		{[]RouteEntry{{Type: RouteLink, Name: "a", Ref: 7}, {Type: RouteCustom, Name: "a", Lines: []string{"x.com"}}}, "已经用过"},
		{[]RouteEntry{{Type: RouteLink, Name: "节点选择", Ref: 7}}, "内置"},
		{[]RouteEntry{{Type: RouteLink, Name: "", Ref: 7}}, "名字"},
		{[]RouteEntry{{Type: RouteLink, Name: "a", Ref: 7}, {Type: RouteLink, Name: "b", Ref: 8}, {Type: RouteLink, Name: "c", Ref: 9},
			{Type: RouteLink, Name: "d", Ref: 10}, {Type: RouteLink, Name: "e", Ref: 11}, {Type: RouteLink, Name: "f", Ref: 12}}, "最多 5 个规则集链接"},
	} {
		if _, err := CleanRoutingLinks(c.in, inSub, nil, linkOK); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: %v", c.in, err)
		}
	}
	// The plain CleanRouting (no link check) refuses link entries.
	if _, err := CleanRouting([]RouteEntry{{Type: RouteLink, Name: "a", Ref: 7}}, inSub, nil); err == nil {
		t.Fatal("CleanRouting accepted a link")
	}
	// Name collision with a node.
	if _, err := CleanRoutingLinks([]RouteEntry{{Type: RouteLink, Name: "香港 01", Ref: 7}}, inSub, map[string]bool{"香港 01": true}, linkOK); err == nil {
		t.Fatal("node name")
	}
	// Link entries count toward the 30 entries, not toward the 300 lines.
	var many []RouteEntry
	for i := 0; i < 3; i++ {
		lines := make([]string, 100)
		for j := range lines {
			lines[j] = fmt.Sprintf("d%d-%d.example", i, j)
		}
		many = append(many, RouteEntry{Type: RouteCustom, Name: fmt.Sprintf("c%d", i), Lines: lines})
	}
	many = append(many, RouteEntry{Type: RouteLink, Name: "l", Ref: 7})
	if _, err := CleanRoutingLinks(many, inSub, nil, linkOK); err != nil {
		t.Fatalf("300 lines + a link: %v", err)
	}
	var full []RouteEntry
	for i := 0; i < MaxRouteEntries; i++ {
		full = append(full, RouteEntry{Type: RouteCustom, Name: fmt.Sprintf("c%d", i), Lines: []string{"a.com"}})
	}
	full = append(full, RouteEntry{Type: RouteLink, Name: "l", Ref: 7})
	if _, err := CleanRoutingLinks(full, inSub, nil, linkOK); err == nil {
		t.Fatal("31 entries")
	}
}

// Old stored lists (no link entries) re-serialize byte-identically.
func TestRoutingJSONOldUnchanged(t *testing.T) {
	old := `[{"type":"custom","name":"公司","lines":["mycompany.com"],"exit":"direct"},{"type":"builtin","key":"ai","exit":"select"}]`
	if got := RoutingJSON(ParseRouting(&old)); got != old {
		t.Fatalf("%s", got)
	}
}
