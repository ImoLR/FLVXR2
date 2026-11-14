package sublink

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Plan 256 「分流」.

// keyedNodes: goldenNodes with item keys (as the handler's resolve sets).
func keyedNodes(t *testing.T) []Node {
	nodes := goldenNodes(t)
	for i := range nodes {
		nodes[i].Key = fmt.Sprintf("e:%d", 100+i)
	}
	return nodes
}

// testRouting: the default 8 + two custom entries + a node exit + ads.
func testRouting(nodeKey string) []RouteEntry {
	r := []RouteEntry{
		{Type: RouteCustom, Name: "公司", Lines: []string{"mycompany.com", "full:vpn.corp.example.net", "intranet", "10.8.0.0/16", "2001:db8::/32"}, Exit: ExitDirect},
		{Type: RouteCustom, Name: "巴哈姆特", Lines: []string{"bahamut.com.tw", "gamer.com.tw"}, Exit: ExitNodePrefix + nodeKey},
		{Type: RouteBuiltin, Key: "ads", Exit: ExitReject},
	}
	for _, e := range DefaultRouting() {
		if e.Key == "ai" {
			e.Exit = ExitAuto
		}
		r = append(r, e)
	}
	return r
}

func TestRoutingEmptyIsByteIdentical(t *testing.T) {
	nodes := keyedNodes(t)
	for _, routing := range [][]RouteEntry{nil, {}} {
		for _, mode := range []string{ModeCNDirect, ModeGlobal} {
			for _, stash := range []bool{false, true} {
				a, _ := Clash(nodes, mode, stash)
				b, _ := ClashRouted(nodes, mode, stash, routing, testOrigin)
				if !bytes.Equal(a, b) {
					t.Fatalf("clash %s stash=%v differs", mode, stash)
				}
			}
			a, _ := SingBox(nodes, mode, testOrigin)
			b, _ := SingBoxRouted(nodes, mode, testOrigin, routing)
			if !bytes.Equal(a, b) {
				t.Fatalf("sing-box %s differs", mode)
			}
			for _, sb := range []bool{false, true} {
				a, _ := Surge(nodes, mode, sb)
				b, _ := SurgeRouted(nodes, mode, sb, routing, testOrigin)
				if !bytes.Equal(a, b) {
					t.Fatalf("surge %s sb=%v differs", mode, sb)
				}
			}
		}
	}
}

// TestGoldenRouting locks the routed output of every format (written at
// plan 256 with -update-golden).
func TestGoldenRouting(t *testing.T) {
	nodes := keyedNodes(t)
	routing := testRouting(nodes[0].Key)
	for _, c := range []struct {
		name string
		fn   func() ([]byte, int)
	}{
		{"routing-clash-cn.yaml", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
		{"routing-clash-global.yaml", func() ([]byte, int) { return ClashRouted(nodes, ModeGlobal, false, routing, testOrigin) }},
		{"routing-stash-cn.yaml", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, true, routing, testOrigin) }},
		{"routing-singbox-cn.json", func() ([]byte, int) { return SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing) }},
		{"routing-singbox-global.json", func() ([]byte, int) { return SingBoxRouted(nodes, ModeGlobal, testOrigin, routing) }},
		{"routing-surge-cn.conf", func() ([]byte, int) { return SurgeRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
		{"routing-surfboard-cn.conf", func() ([]byte, int) { return SurgeRouted(nodes, ModeCNDirect, true, routing, testOrigin) }},
		{"routing-clash-empty.yaml", func() ([]byte, int) { return ClashRouted(nil, ModeCNDirect, false, routing, testOrigin) }},
	} {
		body, _ := c.fn()
		checkGolden(t, c.name, body)
	}
}

type clashTestDoc struct {
	Proxies []struct {
		Name string `yaml:"name"`
	} `yaml:"proxies"`
	Groups []struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	} `yaml:"proxy-groups"`
	Providers map[string]map[string]any `yaml:"rule-providers"`
	Rules     []string                  `yaml:"rules"`
}

func parseClash(t *testing.T, b []byte) clashTestDoc {
	t.Helper()
	var d clashTestDoc
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatalf("yaml: %v\n%s", err, b)
	}
	return d
}

func TestRoutingClash(t *testing.T) {
	nodes := keyedNodes(t)
	routing := testRouting(nodes[0].Key)
	for _, stash := range []bool{false, true} {
		out, n := ClashRouted(nodes, ModeCNDirect, stash, routing, testOrigin)
		d := parseClash(t, out)
		if n != len(d.Proxies) || n == 0 {
			t.Fatalf("nodes %d vs %d", n, len(d.Proxies))
		}
		want := []string{GroupSelect, GroupAuto, "📌 公司", "📌 巴哈姆特", "🛑 广告拦截", "🤖 AI", "📹 YouTube", "🎬 流媒体", "📲 Telegram", "🐱 GitHub", "🔍 谷歌", "Ⓜ️ 微软", "🍎 苹果"}
		if len(d.Groups) != len(want) {
			t.Fatalf("groups %d, want %d", len(d.Groups), len(want))
		}
		first := map[string]string{}
		for i, g := range d.Groups {
			if g.Name != want[i] {
				t.Fatalf("group %d = %q, want %q", i, g.Name, want[i])
			}
			if i >= 2 {
				first[g.Name] = g.Proxies[0]
				if g.Type != "select" || len(g.Proxies) != len(d.Proxies)+3+boolInt(g.Proxies[0] == "REJECT") {
					t.Fatalf("%s: %s %d members", g.Name, g.Type, len(g.Proxies))
				}
				seen := map[string]bool{}
				for _, m := range g.Proxies {
					if seen[m] {
						t.Fatalf("%s: duplicate member %q", g.Name, m)
					}
					seen[m] = true
				}
			}
		}
		if first["📌 公司"] != "DIRECT" || first["📌 巴哈姆特"] != d.Proxies[0].Name || first["🛑 广告拦截"] != "REJECT" ||
			first["🤖 AI"] != GroupAuto || first["📹 YouTube"] != GroupSelect || first["Ⓜ️ 微软"] != "DIRECT" {
			t.Fatalf("exits: %v", first)
		}
		// Providers: every referenced set once, from the panel, DIRECT.
		if len(d.Providers) != 15 {
			t.Fatalf("providers %d", len(d.Providers))
		}
		for id, p := range d.Providers {
			ext, format := ".mrs", "mrs"
			if stash {
				ext, format = ".txt", "text"
			}
			if p["url"] != testOrigin+"/s/rs/"+id+ext || p["format"] != format || p["type"] != "http" || p["interval"] != RuleSetInterval {
				t.Fatalf("provider %s: %v", id, p)
			}
			if _, has := p["proxy"]; has == stash || !stash && p["proxy"] != "DIRECT" {
				t.Fatalf("provider %s proxy: %v", id, p)
			}
			wantB := "domain"
			if strings.HasPrefix(id, "geoip-") {
				wantB = "ipcidr"
			}
			if p["behavior"] != wantB {
				t.Fatalf("provider %s behavior %v", id, p["behavior"])
			}
		}
		if strings.Contains(string(out), "githubusercontent") || strings.Contains(string(out), "jsdelivr") {
			t.Fatal("third-party rule host")
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The rule order is fixed: private/LAN → custom → catalog order → CN → MATCH.
func TestRoutingRuleOrder(t *testing.T) {
	nodes := keyedNodes(t)
	all := []RouteEntry{}
	for i := len(Catalog) - 1; i >= 0; i-- { // stored in reverse: render still uses rule order
		all = append(all, RouteEntry{Type: RouteBuiltin, Key: Catalog[i].Key, Exit: Catalog[i].Exit})
	}
	all = append(all, RouteEntry{Type: RouteCustom, Name: "x", Lines: []string{"x.example"}, Exit: ExitSelect})
	out, _ := ClashRouted(nodes, ModeCNDirect, false, all, testOrigin)
	d := parseClash(t, out)
	var got []string
	for _, r := range d.Rules {
		p := strings.Split(r, ",")
		switch p[0] {
		case "RULE-SET":
			if len(got) == 0 || got[len(got)-1] != p[2] {
				got = append(got, p[2])
			}
		case "GEOSITE", "GEOIP", "MATCH", "DOMAIN-SUFFIX":
			got = append(got, p[0]+":"+p[1])
		}
	}
	want := "GEOSITE:private GEOIP:private DOMAIN-SUFFIX:msftconnecttest.com DOMAIN-SUFFIX:msftncsi.com DOMAIN-SUFFIX:x.example " +
		"🛑 广告拦截 🤖 AI 📹 YouTube 🎬 流媒体 🎵 TikTok 🎧 Spotify 🕹️ Discord 💬 社交 📲 Telegram 🐱 GitHub 🔍 谷歌 Ⓜ️ 微软 🍎 苹果 🎮 游戏 " +
		"GEOSITE:cn GEOIP:CN MATCH:" + GroupSelect
	if strings.Join(got, " ") != want {
		t.Fatalf("rule order:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	if d.Rules[len(d.Rules)-1] != "MATCH,"+GroupSelect {
		t.Fatal("MATCH not last")
	}
	// global: private CIDRs first, no CN rules.
	out, _ = ClashRouted(nodes, ModeGlobal, false, all, testOrigin)
	g := parseClash(t, out).Rules
	if !strings.HasPrefix(g[0], "IP-CIDR,127.0.0.0/8") || g[len(privateCIDRs)] != "DOMAIN-SUFFIX,x.example,📌 x" || strings.Contains(string(out), "GEOSITE,cn") {
		t.Fatalf("global rules: %v", g[:len(privateCIDRs)+1])
	}
}

// clashMatcher evaluates Clash rules against a domain with the embedded
// catalog text lists (what the client's providers hold).
func clashMatcher(t *testing.T, rules []string) func(string) string {
	t.Helper()
	lists := map[string][]string{}
	for id := range catalogSets {
		b, ok := embeddedRuleSet(id, ExtText)
		if !ok {
			t.Fatalf("no embedded %s", id)
		}
		lists[id] = strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	suffix := func(d, s string) bool { return d == s || strings.HasSuffix(d, "."+s) }
	return func(domain string) string {
		for _, r := range rules {
			p := strings.Split(r, ",")
			switch p[0] {
			case "DOMAIN-SUFFIX":
				if suffix(domain, p[1]) {
					return p[2]
				}
			case "DOMAIN":
				if domain == p[1] {
					return p[2]
				}
			case "DOMAIN-KEYWORD":
				if strings.Contains(domain, p[1]) {
					return p[2]
				}
			case "RULE-SET":
				for _, l := range lists[p[1]] {
					if s, ok := strings.CutPrefix(l, "+."); ok && suffix(domain, s) || l == domain {
						return p[2]
					}
				}
			case "MATCH":
				return p[1]
			}
		}
		return ""
	}
}

// Gemini / YouTube must land in AI / YouTube, not 谷歌 (rule order).
func TestRoutingDomainsHitTheirGroup(t *testing.T) {
	all := []RouteEntry{}
	for _, c := range Catalog {
		all = append(all, RouteEntry{Type: RouteBuiltin, Key: c.Key, Exit: c.Exit})
	}
	out, _ := ClashRouted(keyedNodes(t), ModeGlobal, false, all, testOrigin)
	match := clashMatcher(t, parseClash(t, out).Rules)
	for domain, want := range map[string]string{
		"gemini.google.com": "🤖 AI", "bard.google.com": "🤖 AI", "chatgpt.com": "🤖 AI", "claude.ai": "🤖 AI", "api.openai.com": "🤖 AI",
		"www.youtube.com": "📹 YouTube", "youtu.be": "📹 YouTube", "i.ytimg.com": "📹 YouTube", "yt3.googleusercontent.com": "📹 YouTube",
		"www.google.com": "🔍 谷歌", "mail.google.com": "🔍 谷歌", "lh3.googleusercontent.com": "🔍 谷歌",
		"www.netflix.com": "🎬 流媒体", "www.disneyplus.com": "🎬 流媒体",
		"api.telegram.org": "📲 Telegram", "t.me": "📲 Telegram",
		"github.com": "🐱 GitHub", "raw.githubusercontent.com": "🐱 GitHub",
		"www.microsoft.com": "Ⓜ️ 微软", "www.apple.com": "🍎 苹果",
		"store.steampowered.com": "🎮 游戏", "x.com": "💬 社交", "discord.com": "🕹️ Discord", "open.spotify.com": "🎧 Spotify", "www.tiktok.com": "🎵 TikTok",
		"www.baidu.com": GroupSelect,
	} {
		if got := match(domain); got != want {
			t.Errorf("%s → %q, want %q", domain, got, want)
		}
	}
}

func TestRoutingSingBox(t *testing.T) {
	nodes := keyedNodes(t)
	routing := testRouting(nodes[0].Key)
	out, _ := SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing)
	var d struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     struct {
			Rules   []map[string]any    `json:"rules"`
			RuleSet []map[string]string `json:"rule_set"`
		} `json:"route"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	sel := map[string][]any{}
	for _, o := range d.Outbounds {
		if o["type"] == "selector" {
			sel[o["tag"].(string)] = o["outbounds"].([]any)
			if o["default"] != o["outbounds"].([]any)[0] {
				t.Fatalf("%v default", o["tag"])
			}
		}
		if o["type"] == "block" {
			t.Fatal("block outbound (removed in sing-box 1.13)")
		}
	}
	if _, ok := sel["🛑 广告拦截"]; ok {
		t.Fatal("reject entry got a selector")
	}
	if sel["📌 公司"][0] != "direct" || sel["🤖 AI"][0] != GroupAuto || sel["📹 YouTube"][0] != GroupSelect {
		t.Fatalf("selectors: %v", sel)
	}
	var tags []string
	for _, r := range d.Route.Rules {
		switch {
		case r["action"] == "reject":
			tags = append(tags, "reject")
		case r["outbound"] != nil:
			tags = append(tags, r["outbound"].(string))
		}
	}
	want := "direct direct 📌 公司 📌 巴哈姆特 reject 🤖 AI 📹 YouTube 🎬 流媒体 📲 Telegram 🐱 GitHub 🔍 谷歌 Ⓜ️ 微软 🍎 苹果 direct"
	if strings.Join(tags, " ") != want {
		t.Fatalf("route rules:\n%s\nwant\n%s", strings.Join(tags, " "), want)
	}
	company := d.Route.Rules[4]
	if fmt.Sprint(company["domain_suffix"]) != "[mycompany.com]" || fmt.Sprint(company["domain"]) != "[vpn.corp.example.net]" ||
		fmt.Sprint(company["domain_keyword"]) != "[intranet]" || fmt.Sprint(company["ip_cidr"]) != "[10.8.0.0/16 2001:db8::/32]" {
		t.Fatalf("custom rule: %v", company)
	}
	if len(d.Route.RuleSet) != 2+15 {
		t.Fatalf("rule sets %d", len(d.Route.RuleSet))
	}
	for _, rs := range d.Route.RuleSet {
		if rs["url"] != RuleSetURL(testOrigin, rs["tag"]) || rs["download_detour"] != "direct" || rs["format"] != "binary" {
			t.Fatalf("rule set %v", rs)
		}
	}
}

func TestRoutingSurge(t *testing.T) {
	nodes := keyedNodes(t)
	routing := testRouting(nodes[0].Key)
	for _, sb := range []bool{false, true} {
		out, _ := SurgeRouted(nodes, ModeCNDirect, sb, routing, testOrigin)
		s := string(out)
		groups := s[strings.Index(s, "[Proxy Group]"):strings.Index(s, "[Rule]")]
		for _, want := range []string{"\n📌 公司 = select, DIRECT, 节点选择, 自动选择, ", "\n🛑 广告拦截 = select, REJECT, 节点选择, ", "\n🤖 AI = select, 自动选择, 节点选择, "} {
			if !strings.Contains(groups, want) {
				t.Fatalf("sb=%v missing %q in\n%s", sb, want, groups)
			}
		}
		nr := ",no-resolve"
		if sb {
			nr = ""
		}
		for _, want := range []string{
			"\nIP-CIDR,10.8.0.0/16,📌 公司" + nr + "\n",
			"\nRULE-SET," + testOrigin + "/s/rs/geosite-ai.list,🤖 AI\n",
			"\nRULE-SET," + testOrigin + "/s/rs/geoip-telegram.list,📲 Telegram" + nr + "\n",
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("sb=%v missing rule %q", sb, want)
			}
		}
		if sb && strings.Contains(s, "no-resolve") {
			t.Fatal("Surfboard got no-resolve")
		}
		rules := s[strings.Index(s, "[Rule]"):]
		if strings.Index(rules, "📌 公司") > strings.Index(rules, "geosite-ads") || strings.Index(rules, "geosite-apple") > strings.Index(rules, "GEOIP,CN") {
			t.Fatal("surge rule order")
		}
	}
}

// A node: exit whose node is not in the rendered list falls back to
// 节点选择 (deleted node, or a protocol the format cannot express).
func TestRoutingNodeExitFallback(t *testing.T) {
	nodes := keyedNodes(t)
	var snell string
	for _, n := range nodes {
		if n.Type() == "snell" {
			snell = n.Key
		}
	}
	if snell == "" {
		t.Fatal("no snell sample")
	}
	for _, key := range []string{"e:999", snell} {
		routing := []RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: ExitNodePrefix + key}}
		out, _ := SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing) // sing-box has no Snell
		if !strings.Contains(string(out), "\"tag\": \"🤖 AI\",\n      \"outbounds\": [\n        \""+GroupSelect+"\"") {
			t.Fatalf("%s: no fallback to 节点选择", key)
		}
		c, _ := ClashRouted(nil, ModeCNDirect, false, routing, testOrigin)
		d := parseClash(t, c)
		if d.Groups[1].Name != "🤖 AI" || strings.Join(d.Groups[1].Proxies, ",") != GroupSelect+",DIRECT" {
			t.Fatalf("no nodes: %v", d.Groups)
		}
	}
	// Clash does have Snell: the exit is that node.
	out, _ := ClashRouted(nodes, ModeCNDirect, false, []RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: ExitNodePrefix + snell}}, testOrigin)
	d := parseClash(t, out)
	if d.Groups[2].Proxies[0] == GroupSelect {
		t.Fatal("snell node exit fell back in Clash")
	}
	// auto without nodes → 节点选择.
	c, _ := ClashRouted(nil, ModeCNDirect, false, []RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: ExitAuto}}, testOrigin)
	if g := parseClash(t, c).Groups; g[1].Proxies[0] != GroupSelect {
		t.Fatalf("auto without nodes: %v", g)
	}
}

// A node may not share a name with a group.
func TestRoutingNodeNameCollision(t *testing.T) {
	nodes := keyedNodes(t)
	nodes[1].Name = "🤖 AI"
	routing := []RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: ExitNodePrefix + nodes[1].Key}}
	d := parseClash(t, func() []byte { b, _ := ClashRouted(nodes, ModeCNDirect, false, routing, testOrigin); return b }())
	if d.Proxies[1].Name != "🤖 AI · 节点" || d.Groups[2].Proxies[0] != "🤖 AI · 节点" {
		t.Fatalf("collision: %q %v", d.Proxies[1].Name, d.Groups[2].Proxies[:2])
	}
	if nodes[1].Name != "🤖 AI" {
		t.Fatal("caller's nodes changed")
	}
	s, _ := SurgeRouted(nodes, ModeCNDirect, false, routing, testOrigin)
	if !strings.Contains(string(s), "\n🤖 AI · 节点 = ") || !strings.Contains(string(s), "\n🤖 AI = select, 🤖 AI · 节点, ") {
		t.Fatal("surge collision")
	}
}

func TestParseRouteLine(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":                 "suffix example.com",
		"Example.COM":                 "suffix example.com",
		"+.example.com":               "suffix example.com",
		"*.example.com":               "suffix example.com",
		".example.com":                "suffix example.com",
		"domain:example.com":          "suffix example.com",
		"full:a.example.com":          "full a.example.com",
		"keyword:google":              "keyword google",
		"keyword:a.b":                 "keyword a.b",
		"google":                      "keyword google",
		"https://www.example.com/x?y": "suffix www.example.com",
		"10.8.0.0/16":                 "ip4 10.8.0.0/16",
		"10.8.3.4/16":                 "ip4 10.8.0.0/16",
		"1.2.3.4":                     "ip4 1.2.3.4/32",
		"ip:1.2.3.4":                  "ip4 1.2.3.4/32",
		"2001:db8::1":                 "ip6 2001:db8::1/128",
		"[2001:db8::1]":               "ip6 2001:db8::1/128",
		"2001:db8::/32":               "ip6 2001:db8::/32",
		"::ffff:1.2.3.0/120":          "ip4 1.2.3.0/24",
	} {
		r, why := ParseRouteLine(in)
		if why != "" || r.Kind+" "+r.Value != want {
			t.Errorf("%q → %q %q, want %q", in, r.Kind+" "+r.Value, why, want)
		}
		// The stored form parses back to the same rule.
		if again, _ := ParseRouteLine(r.Line()); again != r {
			t.Errorf("%q: line %q not stable", in, r.Line())
		}
	}
	for _, bad := range []string{"a b.com", "a,b.com", "例子.中国", "foo..com", "-", "a", "keyword:-", "ip:example.com", `a"b`, "a;b.com", strings.Repeat("a", 254), "exa mple"} {
		if r, why := ParseRouteLine(bad); why == "" {
			t.Errorf("%q accepted as %v", bad, r)
		}
	}
}

func TestCleanRouting(t *testing.T) {
	inSub := func(k string) bool { return k == "e:1" }
	ok := func(in []RouteEntry) []RouteEntry {
		t.Helper()
		out, err := CleanRouting(in, inSub, map[string]bool{"香港 01": true})
		if err != nil {
			t.Fatalf("%v", err)
		}
		return out
	}
	bad := func(in []RouteEntry, want string) {
		t.Helper()
		_, err := CleanRouting(in, inSub, map[string]bool{"香港 01": true})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want …%s…", err, want)
		}
	}
	// Normalized: custom first, builtins in rule order, defaults filled, dups dropped.
	out := ok([]RouteEntry{
		{Type: RouteBuiltin, Key: "apple"}, {Type: RouteBuiltin, Key: "ai", Exit: ExitNodePrefix + "e:1"},
		{Type: RouteCustom, Name: " 公司 ", Lines: []string{"A.com\n\n# note\nb.com", "a.com", "1.2.3.4"}},
		{Type: RouteBuiltin, Key: "ads"}, {Type: RouteBuiltin, Key: "apple", Exit: ExitSelect},
		{Type: RouteBuiltin, Key: "google", Exit: ExitNodePrefix + "e:2"},
	})
	got, _ := json.Marshal(out)
	want := `[{"type":"custom","name":"公司","lines":["a.com","b.com","1.2.3.4"],"exit":"select"},{"type":"builtin","key":"ads","exit":"reject"},{"type":"builtin","key":"ai","exit":"node:e:1"},{"type":"builtin","key":"google","exit":"select"},{"type":"builtin","key":"apple","exit":"direct"}]`
	if string(got) != want {
		t.Fatalf("clean:\n%s\nwant\n%s", got, want)
	}
	if len(ok(nil)) != 0 || RoutingJSON(ok([]RouteEntry{})) != "[]" {
		t.Fatal("empty list")
	}
	many := make([]RouteEntry, MaxRouteEntries+1)
	for i := range many {
		many[i] = RouteEntry{Type: RouteCustom, Name: fmt.Sprint("n", i), Lines: []string{"a.com"}}
	}
	bad(many, "最多 30 个分流")
	lines := make([]string, 0, MaxRouteLines+1)
	for i := 0; i <= MaxRouteLines; i++ {
		lines = append(lines, fmt.Sprintf("h%d.example.com", i))
	}
	bad([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: lines[:200]}, {Type: RouteCustom, Name: "b", Lines: lines[200:]}}, "合计最多 300 行")
	ok([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: lines[:MaxRouteLines]}})
	bad([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: []string{"ok.com", "", "bad line"}}}, "「a」第 3 行「bad line」")
	bad([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: []string{strings.Repeat("a", 250) + ".com"}}}, "太长了")
	bad([]RouteEntry{{Type: RouteCustom, Name: strings.Repeat("长", 21), Lines: []string{"a.com"}}}, "最多 20 个字")
	for _, name := range []string{"a,b", "a，b", `a"b`, "a\nb", "a\tb", "a\\b", "a\u2028b"} {
		bad([]RouteEntry{{Type: RouteCustom, Name: name, Lines: []string{"a.com"}}}, "不能有逗号")
	}
	for _, name := range []string{"节点选择", "DIRECT", "reject", "AI", "🤖 AI", "谷歌", "Proxy"} {
		bad([]RouteEntry{{Type: RouteCustom, Name: name, Lines: []string{"a.com"}}}, "内置的名字")
	}
	bad([]RouteEntry{{Type: RouteCustom, Name: "香港 01", Lines: []string{"a.com"}}}, "和订阅里的节点同名")
	bad([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: []string{"a.com"}}, {Type: RouteCustom, Name: "a", Lines: []string{"b.com"}}}, "已经用过了")
	bad([]RouteEntry{{Type: RouteCustom, Name: "a", Lines: []string{"# only a comment"}}}, "还没有写")
	bad([]RouteEntry{{Type: RouteCustom, Name: "", Lines: []string{"a.com"}}}, "起个名字")
	bad([]RouteEntry{{Type: RouteBuiltin, Key: "nope"}}, "无效")
	bad([]RouteEntry{{Type: "x"}}, "无效")
	bad([]RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: "proxy"}}, "走哪里")
	bad([]RouteEntry{{Type: RouteBuiltin, Key: "ai", Exit: "node:"}}, "走哪里")
	ok([]RouteEntry{{Type: RouteCustom, Name: "#1 ü 🎮", Lines: []string{"a.com"}}})
}

// Hostile names / lines (stored JSON bypassing CleanRouting) never break
// the document structure of any format.
func TestRoutingInjection(t *testing.T) {
	nodes := keyedNodes(t)
	hostile := []RouteEntry{
		{Type: RouteCustom, Name: "x\nproxies: []\n#", Lines: []string{"a.com"}, Exit: ExitDirect},
		{Type: RouteCustom, Name: "y,DIRECT", Lines: []string{"b.com"}, Exit: ExitDirect},
		{Type: RouteCustom, Name: `z"}]`, Lines: []string{"c.com"}, Exit: ExitDirect},
		{Type: RouteCustom, Name: "ok #1 ü", Lines: []string{"d.com\nMATCH,DIRECT", "e.com,DIRECT", "f.com\r\n[Rule]", "g.com"}, Exit: ExitDirect},
		{Type: RouteBuiltin, Key: "ai\nx", Exit: ExitDirect},
		{Type: RouteBuiltin, Key: "google", Exit: "node:\n[Proxy]"},
	}
	out, _ := ClashRouted(nodes, ModeCNDirect, false, hostile, testOrigin)
	d := parseClash(t, out)
	if len(d.Proxies) != len(parseClash(t, func() []byte { b, _ := Clash(nodes, ModeCNDirect, false); return b }()).Proxies) {
		t.Fatal("proxies changed")
	}
	var names []string
	for _, g := range d.Groups[2:] {
		names = append(names, g.Name)
	}
	if strings.Join(names, "|") != "📌 ok #1 ü|🔍 谷歌" {
		t.Fatalf("groups: %v", names)
	}
	for _, r := range d.Rules {
		if strings.Count(r, ",") > 3 || strings.ContainsAny(r, "\r\n") {
			t.Fatalf("rule %q", r)
		}
	}
	if d.Rules[len(d.Rules)-1] != "MATCH,"+GroupSelect || strings.Count(strings.Join(d.Rules, "\n"), "MATCH") != 1 {
		t.Fatal("MATCH injected")
	}
	for _, r := range d.Rules {
		if strings.HasPrefix(r, "DOMAIN-SUFFIX,") && strings.HasSuffix(r, "📌 ok #1 ü") {
			v := strings.Split(r, ",")[1]
			if v != "g.com" {
				t.Fatalf("bad line kept: %q", r)
			}
		}
	}
	var js any
	sb, _ := SingBoxRouted(nodes, ModeCNDirect, testOrigin, hostile)
	if err := json.Unmarshal(sb, &js); err != nil {
		t.Fatal(err)
	}
	for _, sbf := range []bool{false, true} {
		s, _ := SurgeRouted(nodes, ModeCNDirect, sbf, hostile, testOrigin)
		if strings.Count(string(s), "[Proxy]") != 1 || strings.Count(string(s), "[Rule]") != 1 || strings.Count(string(s), "FINAL,") != 1 {
			t.Fatal("surge sections injected")
		}
		if !strings.Contains(string(s), "\n📌 ok ＃1 ü = select, DIRECT,") || !strings.Contains(string(s), "\nDOMAIN-SUFFIX,g.com,📌 ok ＃1 ü\n") {
			t.Fatalf("surge escaped name missing:\n%s", s)
		}
	}
}

var fetchRuleSets = flag.Bool("fetch-rulesets", false, "download missing catalog snapshot files into ruleset/ (one at a time, 3 s apart)")

// Every catalog set exists in every served format (embedded), and the
// upstream names are what MetaCubeX publishes. A new Catalog row: run
// go test ./internal/sublink -run TestRuleSetSnapshot -fetch-rulesets, then
// rebuild (the files are embedded).
func TestRuleSetSnapshot(t *testing.T) {
	if *fetchRuleSets {
		for id := range catalogSets {
			for _, ext := range []string{ExtSRS, ExtMRS, ExtText} {
				path := filepath.Join("ruleset", id+"."+ext)
				if _, err := os.Stat(path); err == nil {
					continue
				}
				b, err := fetchRuleSet(context.Background(), upstreamURL(id, ext))
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				if err := os.WriteFile(path, b, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("fetched %s (%d bytes)", path, len(b))
				time.Sleep(3 * time.Second)
			}
		}
		t.Skip("snapshot fetched; rebuild to embed it")
	}
	var s RuleSets
	for id, rs := range catalogSets {
		for _, ext := range []string{ExtSRS, ExtMRS, ExtText, ExtList} {
			b, etag, ok := s.Get(id, ext, time.Time{}) // zero time: no refresh queued
			if !ok || len(b) == 0 || etag == "" {
				t.Fatalf("%s.%s not served", id, ext)
			}
			switch ext {
			case ExtSRS:
				ok = validRuleSet(b)
			case ExtMRS:
				ok = validMRS(b)
			case ExtText:
				_, ok = cleanTextList(b, rs.IP)
			case ExtList:
				ok = bytes.HasPrefix(b, []byte("DOMAIN")) || bytes.HasPrefix(b, []byte("IP-CIDR"))
			}
			if !ok {
				t.Fatalf("%s.%s invalid", id, ext)
			}
		}
		u := upstreamURL(id, ExtMRS)
		kind := "geosite/"
		if rs.IP {
			kind = "geoip/"
		}
		if u != metaRulesBase+"meta/geo/"+kind+rs.Upstream+".mrs" {
			t.Fatalf("%s upstream %s", id, u)
		}
	}
	if s.Known("geosite-cn", ExtMRS) || !s.Known("geosite-cn", ExtSRS) || s.Known("nope", ExtSRS) || s.Known("geosite-ai", "yaml") {
		t.Fatal("Known")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) != 0 || s.running {
		t.Fatal("refresh queued for a fresh copy")
	}
}

func TestSurgeListAndTextClean(t *testing.T) {
	// One bad line among many is dropped (more than 2 % rejects the file).
	txt, ok := cleanTextList([]byte("# c\r\n+.a.com\r\nb.com\n.c.com\n*.d.com\nbad domain\n"+strings.Repeat("+.z.com\n", 60)), false)
	if !ok || !strings.HasPrefix(string(txt), "+.a.com\nb.com\n.c.com\n*.d.com\n+.z.com\n") || strings.Contains(string(txt), "bad") {
		t.Fatalf("clean %q %v", txt, ok)
	}
	txt = txt[:strings.Index(string(txt), "+.z.com")]
	if got := string(surgeList(txt, false)); got != "DOMAIN-SUFFIX,a.com\nDOMAIN,b.com\nDOMAIN-SUFFIX,c.com\n" {
		t.Fatalf("surge %q", got)
	}
	ip, ok := cleanTextList([]byte("1.2.3.4/24\n2001:db8::1/32\n"), true)
	if !ok || string(surgeList(ip, true)) != "IP-CIDR,1.2.3.0/24\nIP-CIDR6,2001:db8::/32\n" {
		t.Fatalf("ip %q", surgeList(ip, true))
	}
	for _, b := range []string{"", "<html>blocked</html>", "\xff\xfe", strings.Repeat("ok.com\n", 10) + "a b\nc d\n"} {
		if _, ok := cleanTextList([]byte(b), false); ok {
			t.Fatalf("accepted %q", b)
		}
	}
	if _, ok := cleanTextList([]byte("1.2.3.4/24\nexample.com\n"), true); ok {
		t.Fatal("domain in ip list")
	}
}
