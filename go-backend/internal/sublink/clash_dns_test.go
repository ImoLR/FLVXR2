package sublink

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Plan 220: from mainland China the foreign DoH resolvers only work through
// the proxy, and a domain outside geosite:cn must not be resolved locally
// just to test GEOIP,CN (5 s stall per new connection on fork.117).
func TestClashDNSPlan220(t *testing.T) {
	nodes := goldenNodes(t)
	type dnsCfg struct {
		Nameserver   []string `yaml:"nameserver"`
		Direct       []string `yaml:"direct-nameserver"`
		ProxyServer  []string `yaml:"proxy-server-nameserver"`
		EnhancedMode string   `yaml:"enhanced-mode"`
		FakeIPFilter []string `yaml:"fake-ip-filter"`
	}
	type doc struct {
		DNS   dnsCfg   `yaml:"dns"`
		Rules []string `yaml:"rules"`
	}
	cn := []string{"https://223.5.5.5/dns-query", "https://120.53.53.53/dns-query"}
	for _, c := range []struct {
		name  string
		nodes []Node
		mode  string
		stash bool
	}{
		{"clash-cn", nodes, ModeCNDirect, false},
		{"clash-global", nodes, ModeGlobal, false},
		{"clash-empty", nil, ModeCNDirect, false},
		{"clash-empty-global", nil, ModeGlobal, false},
		{"stash-cn", nodes, ModeCNDirect, true},
		{"stash-global", nodes, ModeGlobal, true},
	} {
		body, _ := Clash(c.nodes, c.mode, c.stash)
		var d doc
		if err := yaml.Unmarshal(body, &d); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		// Stash has no "#group" detour; without nodes there is nothing to
		// detour through: both resolve with the CN pair.
		if c.stash || len(c.nodes) == 0 {
			if strings.Join(d.DNS.Nameserver, ",") != strings.Join(cn, ",") {
				t.Errorf("%s: nameserver %v", c.name, d.DNS.Nameserver)
			}
		} else {
			if len(d.DNS.Nameserver) != 2 {
				t.Errorf("%s: nameserver %v", c.name, d.DNS.Nameserver)
			}
			for _, ns := range d.DNS.Nameserver {
				if !strings.HasSuffix(ns, "#"+GroupSelect) {
					t.Errorf("%s: nameserver %q not via %s", c.name, ns, GroupSelect)
				}
			}
		}
		if c.stash {
			if d.DNS.Direct != nil {
				t.Errorf("%s: Stash got direct-nameserver %v", c.name, d.DNS.Direct)
			}
		} else if strings.Join(d.DNS.Direct, ",") != strings.Join(cn, ",") {
			t.Errorf("%s: direct-nameserver %v", c.name, d.DNS.Direct)
		}
		if strings.Join(d.DNS.ProxyServer, ",") != strings.Join(cn, ",") || d.DNS.EnhancedMode != "fake-ip" {
			t.Errorf("%s: proxy-server-nameserver %v mode %q", c.name, d.DNS.ProxyServer, d.DNS.EnhancedMode)
		}
		if strings.Join(d.DNS.FakeIPFilter, ",") != strings.Join(fakeIPFilter, ",") {
			t.Errorf("%s: fake-ip-filter %v", c.name, d.DNS.FakeIPFilter)
		}
		for _, r := range d.Rules {
			if (strings.HasPrefix(r, "GEOIP,") || strings.HasPrefix(r, "IP-CIDR")) && !strings.HasSuffix(r, ",no-resolve") {
				t.Errorf("%s: rule %q resolves locally", c.name, r)
			}
		}
		if hasCN := strings.Contains(string(body), "\n  - GEOIP,CN,DIRECT,no-resolve\n"); hasCN != (c.mode == ModeCNDirect) {
			t.Errorf("%s: GEOIP,CN no-resolve present=%v", c.name, hasCN)
		}
		// The fast writer must keep handling the document (no yaml.v3
		// fallback on every pull).
		doc, _ := clashDoc(c.nodes, c.mode, c.stash)
		if _, ok := writeBlockYAML(doc, 1024); !ok {
			t.Errorf("%s: block writer refused the document", c.name)
		}
	}
}

// sing-box: the remote resolver goes through the select group; a config
// without nodes (select = direct only) resolves with CN DoH and has no rule
// sets to download from GitHub directly.
func TestSingBoxDNSPlan220(t *testing.T) {
	type server struct {
		Tag    string `json:"tag"`
		Server string `json:"server"`
		Detour string `json:"detour"`
	}
	type doc struct {
		DNS struct {
			Servers []server `json:"servers"`
			Final   string   `json:"final"`
		} `json:"dns"`
		Route struct {
			RuleSet               []json.RawMessage `json:"rule_set"`
			DefaultDomainResolver string            `json:"default_domain_resolver"`
		} `json:"route"`
	}
	for _, c := range []struct {
		name    string
		nodes   []Node
		mode    string
		ruleSet bool
	}{
		{"cn", goldenNodes(t), ModeCNDirect, true},
		{"global", goldenNodes(t), ModeGlobal, false},
		{"empty", nil, ModeCNDirect, false},
		{"empty-global", nil, ModeGlobal, false},
	} {
		body, _ := SingBox(c.nodes, c.mode, testOrigin)
		var d doc
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d.Route.DefaultDomainResolver != "dns-local" {
			t.Errorf("%s: default_domain_resolver %q", c.name, d.Route.DefaultDomainResolver)
		}
		if (len(d.Route.RuleSet) > 0) != c.ruleSet {
			t.Errorf("%s: rule_set %d", c.name, len(d.Route.RuleSet))
		}
		for _, s := range d.DNS.Servers {
			if s.Server != "223.5.5.5" && s.Detour != GroupSelect {
				t.Errorf("%s: foreign resolver %q dialled directly", c.name, s.Server)
			}
		}
		want := "dns-remote"
		if len(c.nodes) == 0 {
			want = "dns-local"
		}
		if d.DNS.Final != want {
			t.Errorf("%s: dns final %q want %q", c.name, d.DNS.Final, want)
		}
	}
}

// Surge (as mmwx's Surge profile) gets the UDP / IPv6 / real-IP lines;
// Surfboard keeps only what Surfboard documents.
func TestSurgeGeneralPlan220(t *testing.T) {
	lines := []string{
		"ipv6 = false\n",
		"hijack-dns = 8.8.8.8:53, 8.8.4.4:53\n",
		"udp-policy-not-supported-behaviour = REJECT\n",
		"always-real-ip = ",
	}
	nodes := goldenNodes(t)
	surge, _ := Surge(nodes, ModeCNDirect, false)
	board, _ := Surge(nodes, ModeCNDirect, true)
	general := func(b []byte) string { return strings.SplitN(string(b), "\n[Proxy]\n", 2)[0] }
	for _, l := range lines {
		if !strings.Contains(general(surge), l) {
			t.Errorf("surge [General] lacks %q", l)
		}
		if strings.Contains(string(board), l) {
			t.Errorf("surfboard got %q", l)
		}
	}
	if !strings.Contains(general(surge), "dns-server = system, 223.5.5.5, 119.29.29.29\n") {
		t.Errorf("surge dns-server changed")
	}
}

// Plan 226: with the node dead or 节点选择 = DIRECT, the fake-ip-filter names
// still resolve (CN DoH) and the Windows network check goes DIRECT; the
// sing-box rule sets come from the panel DIRECT.
func TestNodeDownPlan226(t *testing.T) {
	type doc struct {
		DNS struct {
			Policy map[string][]string `yaml:"nameserver-policy"`
		} `yaml:"dns"`
		Rules []string `yaml:"rules"`
	}
	covered := map[string]bool{}
	for _, k := range cnResolved {
		for _, d := range strings.Split(k, ",") {
			covered[d] = true
		}
	}
	for _, f := range fakeIPFilter {
		keep := f == "+.lan" || f == "+.local" || f == "localhost.ptlogin2.qq.com" || f == "lens.l.google.com"
		if covered[f] == keep {
			t.Errorf("fake-ip-filter %q: CN DoH policy %v", f, covered[f])
		}
	}
	for _, c := range []struct {
		name  string
		nodes []Node
		mode  string
		stash bool
	}{
		{"clash-cn", goldenNodes(t), ModeCNDirect, false},
		{"stash-cn", goldenNodes(t), ModeCNDirect, true},
		{"clash-empty", nil, ModeCNDirect, false},
		{"clash-global", goldenNodes(t), ModeGlobal, false},
	} {
		body, _ := Clash(c.nodes, c.mode, c.stash)
		var d doc
		if err := yaml.Unmarshal(body, &d); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		cn := c.mode == ModeCNDirect && !c.stash
		for _, k := range cnResolved {
			if got := strings.Join(d.DNS.Policy[k], ","); (got == strings.Join(cnDoH, ",")) != cn {
				t.Errorf("%s: policy %q = %q", c.name, k, got)
			}
		}
		rules := strings.Join(d.Rules, "\n")
		if strings.Contains(rules, ncsiDirect[0]+"\n"+ncsiDirect[1]+"\nGEOSITE,cn,DIRECT") != cn || (!cn && strings.Contains(rules, "msft")) {
			t.Errorf("%s: NCSI rules: %v", c.name, d.Rules)
		}
	}
	type sbDoc struct {
		DNS struct {
			Rules []map[string]any `json:"rules"`
		} `json:"dns"`
		Route struct {
			Rules   []map[string]any    `json:"rules"`
			RuleSet []map[string]string `json:"rule_set"`
		} `json:"route"`
	}
	for _, origin := range []string{testOrigin, ""} {
		body, _ := SingBox(goldenNodes(t), ModeCNDirect, origin)
		var d sbDoc
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		if len(d.Route.RuleSet) != 2 {
			t.Fatalf("rule sets %v", d.Route.RuleSet)
		}
		for _, rs := range d.Route.RuleSet {
			wantURL, wantDetour := RuleSetURL(testOrigin, rs["tag"]), "direct"
			if origin == "" {
				wantURL, wantDetour = ruleSetUpstream[rs["tag"]], GroupSelect
			}
			if rs["url"] != wantURL || rs["download_detour"] != wantDetour {
				t.Errorf("origin %q: rule set %v", origin, rs)
			}
		}
		ncsi := func(rules []map[string]any, key, want string) bool {
			for _, r := range rules {
				if v, _ := json.Marshal(r["domain_suffix"]); string(v) == `["msftconnecttest.com","msftncsi.com"]` {
					return r[key] == want
				}
			}
			return false
		}
		if !ncsi(d.DNS.Rules, "server", "dns-local") || !ncsi(d.Route.Rules, "outbound", "direct") {
			t.Errorf("sing-box NCSI rules: %v / %v", d.DNS.Rules, d.Route.Rules)
		}
	}
	if body, _ := SingBox(goldenNodes(t), ModeGlobal, testOrigin); strings.Contains(string(body), "msft") || strings.Contains(string(body), "/s/rs/") {
		t.Error("全部走代理 got 国内直连 rules")
	}
}
