package landingegress

import "testing"

// Plan 139: labels follow CompileLayers' precedence (node default > profile
// default > 全局默认出站, IP-family fallback to direct, rules → 按规则分流).
func TestEffectiveLabelPrecedence(t *testing.T) {
	outbounds := map[int64]Outbound{
		1: {ID: 1, Name: "直连-强制ipv4出站", Type: TypeDirect, IPVersion: "force_ipv4"},
		2: {ID: 2, Name: "代理", Type: TypeProxy},
		3: {ID: 3, Name: "仅 v6", Type: TypeDirect, IPVersion: "force_ipv6"},
		5: {ID: 5, Name: "WARP", Type: TypeWarp},
	}
	profiles := map[int64]Profile{
		7: {ID: 7, Name: "WARP 默认", DefaultAction: "outbound:5"},
		8: {ID: 8, Name: "分流", DefaultAction: ActionDirect, Rules: []Rule{{ID: "r", Name: "r", Enabled: true, Ports: "443", Action: "outbound:5"}}},
	}
	base := func() CompileInput {
		return CompileInput{Profiles: profiles, Outbounds: outbounds, Layers: true, NodeRules: true, Abuse: true, GeoIP: true, GeoSite: true,
			Global: &Global{DefaultAction: "outbound:1"}}
	}
	rule := Rule{ID: "x", Name: "x", Enabled: true, Ports: "25", Action: ActionBlock}
	routed := Rule{ID: "y", Name: "y", Enabled: true, Domains: []string{"example.com"}, Action: "outbound:2"}
	cases := []struct {
		name string
		in   func() CompileInput
		node *NodeRef
		want string
		from string
	}{
		{"global default ipv4", base, nil, "直连（仅 IPv4）", DefaultFromGlobal},
		{"no global layer", func() CompileInput { in := base(); in.Global = &Global{}; return in }, nil, "直连", DefaultFromGlobal},
		{"layers not ready", func() CompileInput { in := base(); in.Layers = false; return in }, nil, "直连", DefaultFromGlobal},
		{"server lacks ipv4 → direct", func() CompileInput { in := base(); in.NoIPv4 = true; return in }, nil, "直连", DefaultFromGlobal},
		{"global proxy", func() CompileInput { in := base(); in.Global.DefaultAction = "outbound:2"; return in }, nil, "代理", DefaultFromGlobal},
		{"profile default wins over global", func() CompileInput { in := base(); in.ServerProfileID = 7; return in }, nil, "WARP", DefaultFromServer},
		{"profile rules", func() CompileInput { in := base(); in.ServerProfileID = 8; return in }, nil, "按规则分流", DefaultFromServer},
		{"global forced block rule only filters", func() CompileInput { in := base(); in.Global.Forced = []Rule{rule}; return in }, nil, "直连（仅 IPv4）", DefaultFromGlobal},
		{"global forced routing rule", func() CompileInput { in := base(); in.Global.Forced = []Rule{routed}; return in }, nil, "按规则分流", DefaultFromGlobal},
		{"global default routing rule", func() CompileInput { in := base(); in.Global.Default = []Rule{routed}; return in }, nil, "按规则分流", DefaultFromGlobal},
		{"profile with block rules only", func() CompileInput {
			in := base()
			in.Profiles = map[int64]Profile{9: {ID: 9, Name: "封锁", DefaultAction: "outbound:5", Rules: []Rule{rule}}}
			in.ServerProfileID = 9
			return in
		}, nil, "WARP", DefaultFromServer},
		{"disabled global rule ignored", func() CompileInput {
			in := base()
			off := rule
			off.Enabled = false
			in.Global.Forced = []Rule{off}
			return in
		}, nil, "直连（仅 IPv4）", DefaultFromGlobal},
		{"abuse switches do not count", func() CompileInput { in := base(); in.AbuseOff = AbuseSwitches{}; return in }, nil, "直连（仅 IPv4）", DefaultFromGlobal},
		{"node follows global", base, &NodeRef{ID: 1}, "直连（仅 IPv4）", DefaultFromGlobal},
		{"node default wins", base, &NodeRef{ID: 1, Default: "outbound:2"}, "代理", DefaultFromNode},
		{"node default ignored when node rules not ready", func() CompileInput { in := base(); in.NodeRules = false; return in }, &NodeRef{ID: 1, Default: "outbound:2"}, "直连（仅 IPv4）", DefaultFromGlobal},
		{"node default family conflict follows upper", func() CompileInput { in := base(); in.NoIPv6 = true; return in }, &NodeRef{ID: 1, Default: "outbound:3"}, "直连（仅 IPv4）", DefaultFromGlobal},
		{"node routing rules", base, &NodeRef{ID: 1, Rules: []Rule{routed}}, "按规则分流", DefaultFromGlobal},
		{"node block rules only", base, &NodeRef{ID: 1, Rules: []Rule{rule}}, "直连（仅 IPv4）", DefaultFromGlobal},
		{"node profile override", base, &NodeRef{ID: 1, Override: ptr(7)}, "WARP", DefaultFromServer},
		{"node override to default follows global", func() CompileInput { in := base(); in.ServerProfileID = 7; return in }, &NodeRef{ID: 1, Override: ptr(0)}, "直连（仅 IPv4）", DefaultFromGlobal},
		{"unknown profile", func() CompileInput { in := base(); in.ServerProfileID = 99; return in }, nil, "直连", ""},
	}
	for _, c := range cases {
		in := c.in()
		if got := in.EffectiveLabel(c.node); got != c.want {
			t.Errorf("%s: label %q want %q", c.name, got, c.want)
		}
		if c.from == "" {
			continue
		}
		if _, from, _ := in.EffectiveDefault(c.node); from != c.from {
			t.Errorf("%s: from %q want %q", c.name, from, c.from)
		}
	}
}

// The label and the compiled plan agree on the global default.
func TestEffectiveDefaultMatchesPlan(t *testing.T) {
	in := CompileInput{Outbounds: map[int64]Outbound{1: {ID: 1, Type: TypeDirect, IPVersion: "force_ipv4"}}, Layers: true, NodeRules: true,
		Global: &Global{DefaultAction: "outbound:1"}, Nodes: []NodeRef{{ID: 7, Tag: "flvxl-7"}},
		Resolve: func(id int64) (PlanOutbound, error) {
			return PlanOutbound{Tag: OutboundTag(id), Type: "direct", IPVersion: "force_ipv4"}, nil
		}}
	plan, _, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range plan.Rules {
		if r.Outbound == OutboundTag(1) && len(r.InboundTags) == 1 && r.InboundTags[0] == "flvxl-7" {
			found = true
		}
	}
	action, _, _ := in.EffectiveDefault(&in.Nodes[0])
	if !found || action != "outbound:1" {
		t.Fatalf("plan rule %v, label action %q", found, action)
	}
	in.NoIPv4 = true
	plan, _, _ = CompileLayers(in)
	if action, _, _ := in.EffectiveDefault(&in.Nodes[0]); action != ActionDirect || (plan != nil && plan.Active()) {
		t.Fatalf("family fallback: action %q plan %+v", action, plan)
	}
}
