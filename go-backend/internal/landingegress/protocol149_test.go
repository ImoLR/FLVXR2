package landingegress

import (
	"strings"
	"testing"
)

// Plan 149: the 「节点协议」 matcher (inboundProtocols) limits a rule to the
// inbound tags of nodes with those protocols; empty = every node (unchanged).

func protocolInput() CompileInput {
	in := layersInput()
	in.Nodes[0].Protocol = "ss2022"               // 11, server profile 1
	in.Nodes[1].Protocol = "vless_reality_vision" // 12, server profile 1
	in.Nodes[2].Protocol = "ss2022"               // 13, override 2
	in.Nodes[3].Protocol = "anytls"               // 14, built-in default
	in.Global, in.Layers, in.NodeRules, in.Markers = &Global{DefaultAction: ActionDirect}, true, true, true
	in.GeoIP, in.GeoSite = true, true
	return in
}

func TestInboundProtocolScope(t *testing.T) {
	in := protocolInput()
	in.Global.Forced = ForcedSeedRules()
	in.Global.Default = []Rule{{ID: "d", Name: "SS 走 WARP", Enabled: true, Domains: []string{"geosite:openai"}, InboundProtocols: []string{"ss2022"}, Action: "outbound:9"}}
	plan, _ := compileOK(t, in)
	all := "flvxl-1099511627790,flvxl-11,flvxl-12,flvxl-13,flvxl-14"
	for name, want := range map[string]string{
		"屏蔽大陆 IP":   all,                 // unscoped: every node + chain copy, as before
		"屏蔽大陆网站":    all,                 //
		"禁止大陆直连":    "flvxl-11,flvxl-13", // SS nodes only, never chain copies
		"SS 走 WARP": "flvxl-11,flvxl-13", // 全局·默认 too
	} {
		r, ok := findRule(plan, name)
		if !ok {
			t.Fatalf("%s missing: %s", name, ruleNames(plan))
		}
		if got := strings.Join(r.InboundTags, ","); got != want {
			t.Fatalf("%s tags = %s, want %s", name, got, want)
		}
	}
	// Source rules bring the exemption list along.
	in.Exempt = func() ([]string, error) { return []string{"203.0.113.1/32"}, nil }
	plan, _ = compileOK(t, in)
	if strings.Join(plan.ExemptSources, ",") != "203.0.113.1/32" {
		t.Fatalf("exempt %v", plan.ExemptSources)
	}

	// No node with that protocol: the rule is not emitted at all.
	in = protocolInput()
	in.Global.Forced = []Rule{{ID: "m", Name: "Mieru only", Enabled: true, Ports: "25", InboundProtocols: []string{"mieru"}, Action: ActionBlock}}
	plan, _ = compileOK(t, in)
	if _, ok := findRule(plan, "Mieru only"); ok {
		t.Fatal("rule emitted without a matching node")
	}

	// Empty scope = byte-identical to a rule without the field.
	a, b := protocolInput(), protocolInput()
	a.Global.Forced = []Rule{{ID: "x", Name: "x", Enabled: true, Ports: "8080", Action: ActionBlock}}
	b.Global.Forced = []Rule{{ID: "x", Name: "x", Enabled: true, Ports: "8080", InboundProtocols: []string{}, Action: ActionBlock}}
	pa, _ := compileOK(t, a)
	pb, _ := compileOK(t, b)
	if planHashOf(t, pa) != planHashOf(t, pb) {
		t.Fatal("empty scope changed the plan")
	}
	// Nodes without a known protocol (pre-149 callers) are not covered by
	// scoped rules but still by unscoped ones.
	c := layersInput()
	c.Global, c.Layers, c.Markers = &Global{DefaultAction: ActionDirect, Forced: ForcedSeedRules()}, true, true
	c.GeoIP, c.GeoSite = true, true
	pc, _ := compileOK(t, c)
	if _, ok := findRule(pc, "禁止大陆直连"); ok {
		t.Fatal("scoped rule on nodes without protocol")
	}
	if _, ok := findRule(pc, "屏蔽大陆 IP"); !ok {
		t.Fatal("unscoped rule lost")
	}
}

// Profile rules: the scope narrows the profile's own node group (and a
// 适用落地节点 list).
func TestInboundProtocolProfileScope(t *testing.T) {
	in := protocolInput()
	p := in.Profiles[1]
	p.Rules = append([]Rule{{ID: "ssonly", Name: "SS 封 8443", Enabled: true, Ports: "9443", InboundProtocols: []string{"ss2022"}, Action: ActionBlock}}, p.Rules...)
	in.Profiles[1] = p
	plan, _ := compileOK(t, in)
	r, ok := findRule(plan, "SS 封 8443")
	if !ok || strings.Join(r.InboundTags, ",") != "flvxl-11" {
		t.Fatalf("profile scope: %+v %v", r, ok)
	}
	// Scope ∩ node list empty → skipped.
	p.Rules[0].LandingNodeIDs = []int64{12}
	in.Profiles[1] = p
	plan, _ = compileOK(t, in)
	if _, ok := findRule(plan, "SS 封 8443"); ok {
		t.Fatal("empty intersection emitted")
	}
}

func TestInboundProtocolValidation(t *testing.T) {
	g := Global{Forced: []Rule{{Name: "a", Enabled: true, SourceIPs: []string{"geoip:cn"}, InboundProtocols: []string{"ss2022", "ss2022", "anytls"}, Action: ActionBlock}}, DefaultAction: ActionDirect}
	if err := NormalizeGlobal(&g, map[int64]bool{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(g.Forced[0].InboundProtocols, ",") != "anytls,ss2022" {
		t.Fatalf("normalized %v", g.Forced[0].InboundProtocols)
	}
	bad := Global{Forced: []Rule{{Name: "a", Enabled: true, Ports: "1", InboundProtocols: []string{"vmess"}, Action: ActionBlock}}, DefaultAction: ActionDirect}
	if err := NormalizeGlobal(&bad, map[int64]bool{}); err == nil || !strings.Contains(err.Error(), "节点协议无效") {
		t.Fatalf("unknown protocol: %v", err)
	}
	// 节点协议 alone is a valid matcher (all traffic of those nodes).
	only := Global{Default: []Rule{{Name: "SS 全走 WARP", Enabled: true, InboundProtocols: []string{"ss2022"}, Action: ActionDirect}}, DefaultAction: ActionDirect}
	if err := NormalizeGlobal(&only, map[int64]bool{}); err != nil {
		t.Fatal(err)
	}
	// Node rules (one node = one protocol) refuse it.
	if _, _, err := NormalizeNodeRules([]Rule{{Name: "a", Enabled: true, Ports: "1", InboundProtocols: []string{"ss2022"}, Action: ActionBlock}}, "", map[int64]bool{}); err == nil {
		t.Fatal("node rule with 节点协议 accepted")
	}
	// Presets with a scope are admin-only.
	if PresetUsableByUser(Preset{Rules: []Rule{{Ports: "1", InboundProtocols: []string{"ss2022"}, Action: ActionBlock}}}, nil) {
		t.Fatal("scoped preset usable by users")
	}
	// The scope never reaches the plan JSON beyond the inbound tags.
	if strings.Contains(ruleKey(toPlanRule(ForcedSeedRules()[2])), "ss2022") {
		t.Fatal("protocol scope leaked into the plan rule")
	}
}

// Labels: a VLESS node is not 「按规则分流」 because of an SS-only routing rule.
func TestInboundProtocolLabel(t *testing.T) {
	in := protocolInput()
	in.Global.Default = []Rule{{ID: "d", Name: "SS 走 WARP", Enabled: true, Domains: []string{"geosite:openai"}, InboundProtocols: []string{"ss2022"}, Action: "outbound:9"}}
	in.ServerProfileID = 0
	ss := NodeRef{ID: 20, Tag: "flvxl-20", Protocol: "ss2022"}
	vless := NodeRef{ID: 21, Tag: "flvxl-21", Protocol: "vless_reality_vision"}
	if got := in.EffectiveLabel(&ss); got != "按规则分流" {
		t.Fatalf("ss label %s", got)
	}
	if got := in.EffectiveLabel(&vless); got != "直连" {
		t.Fatalf("vless label %s", got)
	}
	if got := in.EffectiveLabel(nil); got != "按规则分流" {
		t.Fatalf("server label %s", got)
	}
}
