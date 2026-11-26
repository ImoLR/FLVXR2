package landing

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// blockedByVendor is the Core's "domain:" semantics over the lowercase name.
func blockedByVendor(s string, hosts []string) bool {
	for _, h := range hosts {
		if s == h || strings.HasSuffix(s, "."+h) {
			return true
		}
	}
	return false
}

// The exclusion must match exactly the names the vendor rule does NOT block:
// an excluded name never overrides routing, every other one is blocked.
func TestSniffExcludeRegexpExact(t *testing.T) {
	sets := [][]string{
		{"dmit.io"},
		{"dmit.io", "dmithost.net"},
		{"dmit.io", "xdmit.io", "mit.io.cn"},
		{"aws.amazon.com", "signin.aws"},
		{"bestvm.cloud"},
		{"a.b", "b", "c.b"}, // covered names dedup to "b"
		{"cloud.baidu.com", "bce.baidu.com"},
		{"x-y.example", "y.example"},
	}
	rng := rand.New(rand.NewSource(316))
	for _, hosts := range sets {
		raw := sniffExcludeRegexp(hosts)
		re, err := regexp.Compile(raw)
		if err != nil {
			t.Fatalf("%v: %v (%s)", hosts, err, raw)
		}
		// The Core lowercases nothing in the pattern we must rely on, but
		// make sure a lowercased copy is the same expression.
		if strings.ToLower(raw) != raw {
			t.Fatalf("pattern not lowercase: %s", raw)
		}
		alphabet := []byte("abcdeimostwxyzn.-_1\n")
		var cands []string
		for _, h := range hosts {
			cands = append(cands, h, "www."+h, "a.b."+h, "x"+h, h+".", "."+h, h[1:], h+"x", "-"+h, strings.ToUpper(h)[:1]+h[1:], "\n."+h, h+"\n")
		}
		cands = append(cands, "", ".", "google.com", "amazon.com", "baidu.com", "io", "dmit.iox", "dmitio")
		for i := 0; i < 20000; i++ {
			n := rng.Intn(14)
			b := make([]byte, n)
			for j := range b {
				b[j] = alphabet[rng.Intn(len(alphabet))]
			}
			s := string(b)
			if rng.Intn(3) == 0 && len(hosts) > 0 {
				s += hosts[rng.Intn(len(hosts))]
			}
			cands = append(cands, s)
		}
		for _, s := range cands {
			excluded := re.MatchString(s)
			if excluded == blockedByVendor(s, minimalHosts(hostSet(hosts))) {
				t.Fatalf("hosts %v name %q: excluded=%v blocked=%v (%s)", hosts, s, excluded, !excluded, raw)
			}
		}
	}
}

func vendorRule(tags []string, hosts ...string) EgressRule {
	r := EgressRule{Name: "内置：屏蔽本机厂商官网（DMIT）", InboundTags: tags, Outbound: "block", Builtin: builtinVendor}
	for _, h := range hosts {
		r.Domain = append(r.Domain, "domain:"+h)
	}
	return r
}

func TestVendorRuleValidate(t *testing.T) {
	ok := &Egress{Version: 1, Rules: []EgressRule{vendorRule([]string{"flvxl-1"}, "dmit.io", "dmithost.net")}}
	if err := ok.validate(map[string]bool{"flvxl-1": true}); err != nil {
		t.Fatal(err)
	}
	bad := []EgressRule{
		func() EgressRule { r := vendorRule([]string{"flvxl-1"}, "dmit.io"); r.Outbound = "direct"; return r }(),
		func() EgressRule { r := vendorRule([]string{"flvxl-1"}, "dmit.io"); r.Port = "443"; return r }(),
		func() EgressRule {
			r := vendorRule([]string{"flvxl-1"})
			r.Domain = []string{"geosite:cn"}
			return r
		}(),
		func() EgressRule {
			r := vendorRule([]string{"flvxl-1"})
			r.Domain = []string{"regexp:.*"}
			return r
		}(),
		vendorRule([]string{"flvxl-1"}),
		vendorRule([]string{"flvxl-1"}, "localhost"),
	}
	for i, r := range bad {
		e := &Egress{Version: 1, Rules: []EgressRule{r}}
		if err := e.validate(map[string]bool{"flvxl-1": true}); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	if !vendorRule(nil, "a.b").beforeChains() {
		t.Fatal("vendor rule must go before chain rules")
	}
	if len((&Egress{Version: 1, Rules: []EgressRule{vendorRule([]string{"flvxl-1"}, "dmit.io")}}).coveredTags()) != 0 {
		t.Fatal("vendor rule must never withhold a node")
	}
}

// Rendered right after the safety rule even when the plan lists it later.
func TestVendorRuleRenderedFirst(t *testing.T) {
	e := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 BT", InboundTags: []string{"flvxl-1"}, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse},
		{Name: "放行", InboundTags: []string{"flvxl-1"}, Domain: []string{"domain:dmit.io"}, Outbound: "direct"},
		vendorRule([]string{"flvxl-1"}, "dmit.io"),
	}}
	rules, err := renderRules(e, nil, &sourceMemo{})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range rules {
		got = append(got, r.(map[string]any)["ruleTag"].(string)+"→"+r.(map[string]any)["outboundTag"].(string))
	}
	if strings.Join(got, ",") != "flvx-safety→block,flvxr-3→block,flvxr-1→block,flvxr-2→direct" {
		t.Fatalf("order %v", got)
	}
}

func TestVendorSniffInbound(t *testing.T) {
	n := testNode(t, 1)
	cfg, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	if got := withVendorSniff(cfg, nil); !reflect.DeepEqual(got, cfg) {
		t.Fatal("no names must keep the inbound")
	}
	out := withVendorSniff(cfg, []string{"dmit.io"})
	sniff := out["sniffing"].(map[string]any)
	ex := sniff["domainsExcluded"].([]string)
	if len(ex) != 1 || !strings.HasPrefix(ex[0], "regexp:") || sniff["routeOnly"] != true {
		t.Fatalf("sniffing %+v", sniff)
	}
	// The original map is untouched (it is the canonical desired config).
	if cfg["sniffing"].(map[string]any)["domainsExcluded"].([]string)[0] != "regexp:.*" {
		t.Fatal("original inbound modified")
	}
	if got := sniffOverride([]string{"dmit.io", "dmithost.net"}, []string{"dmit.io"}); !reflect.DeepEqual(got, []string{"dmit.io"}) {
		t.Fatalf("override = %v", got)
	}
	if got := sniffOverride([]string{"www.dmit.io"}, []string{"dmit.io"}); len(got) != 1 {
		t.Fatalf("sub-name override = %v", got)
	}
}

// Routing keeps blocking what applied inbounds still override; the applied
// record follows the rules the Core holds.
func TestVendorApplyAndTransition(t *testing.T) {
	dir := t.TempDir()
	fakeCore(t, dir, true)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m.detectLocal = func() localAddrs { return goldenLocal }
	plan := &Egress{Version: 1, Rules: []EgressRule{vendorRule([]string{"flvxl-1"}, "dmit.io", "dmithost.net")}}
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}, Egress: plan}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if got := m.applied.VendorRules["flvxl-1"]; !reflect.DeepEqual(got, []string{"dmit.io", "dmithost.net"}) {
		t.Fatalf("applied vendor rules %v", got)
	}
	rules := lastRules(t, dir)
	if rules[1]["ruleTag"] != "flvxr-1" || rules[1]["outboundTag"] != "block" || len(rules[1]["domain"].([]any)) != 2 {
		t.Fatalf("rendered %v", rules)
	}
	// The inbound overrode both names; the vendor is removed from the plan:
	// the routing still blocks them for flvxl-1 until the inbound is re-added.
	m.applied.VendorSniff = map[string][]string{"flvxl-1": {"dmit.io", "dmithost.net"}}
	m.desired.Egress = &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{"flvxl-1"}, Port: "25", Outbound: "block", Builtin: builtinAbuse}}}
	m.desired.Revision = 2
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if got := m.applied.VendorRules["flvxl-1"]; !reflect.DeepEqual(got, []string{"dmit.io", "dmithost.net"}) {
		t.Fatalf("transition rules %v", got)
	}
	rules = lastRules(t, dir)
	if rules[1]["outboundTag"] != "block" || len(rules[1]["domain"].([]any)) != 2 || len(rules) != 3 {
		t.Fatalf("transition rendered %v", rules)
	}
	// Once the inbound no longer overrides, the next pass drops them.
	m.applied.VendorSniff = nil
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if len(m.applied.VendorRules) != 0 || len(lastRules(t, dir)) != 2 {
		t.Fatalf("after transition %v %v", m.applied.VendorRules, lastRules(t, dir))
	}
}

func lastRules(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "last-rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Routing.Rules
}
