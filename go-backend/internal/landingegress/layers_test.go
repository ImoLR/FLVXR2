package landingegress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func planHashOf(t *testing.T, p *Plan) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// layersInput is a prod-shaped server: two profiles (one with node-scoped
// rules and a WARP default), node overrides, chain copies, builtin agent.
func layersInput() CompileInput {
	profiles := map[int64]Profile{
		1: {ID: 1, Name: "解锁", DefaultAction: "outbound:5", Rules: []Rule{
			{ID: "ai", Name: "AI", Enabled: true, Domains: []string{"geosite:openai"}, Action: "outbound:5"},
			{ID: "cn", Name: "屏蔽大陆", Enabled: true, IPs: []string{"geoip:cn"}, Action: ActionBlock},
			{ID: "only12", Name: "仅 12", Enabled: true, Ports: "8443", LandingNodeIDs: []int64{12}, Action: ActionBlock},
			{ID: "off", Name: "停用", Enabled: false, Ports: "1", Action: ActionBlock},
		}},
		2: {ID: 2, Name: "封锁默认", DefaultAction: ActionBlock, AbuseDefaults: boolPtr(false), Rules: []Rule{
			{ID: "ai", Name: "AI", Enabled: true, Domains: []string{"geosite:openai"}, Action: "outbound:5"},
		}},
	}
	in := abuseInput(profiles, 1,
		NodeRef{ID: 11, Tag: "flvxl-11"},
		NodeRef{ID: 12, Tag: "flvxl-12"},
		NodeRef{ID: 13, Tag: "flvxl-13", Override: ptr(2)},
		NodeRef{ID: 14, Tag: "flvxl-14", Override: ptr(0)},
	)
	in.Builtin, in.ChainTags = true, []string{"flvxl-1099511627790"}
	in.Outbounds = map[int64]Outbound{
		5: {ID: 5, Name: "socks", Type: TypeProxy, IPVersion: "system"},
		6: {ID: 6, Name: "直连 v4", Type: TypeDirect, IPVersion: "force_ipv4"},
		7: {ID: 7, Name: "直连 v6", Type: TypeDirect, IPVersion: "force_ipv6"},
		9: {ID: 9, Name: "WARP", Type: TypeWarp, IPVersion: "system"},
	}
	in.Resolve = func(id int64) (PlanOutbound, error) {
		switch id {
		case 5:
			return PlanOutbound{Type: "socks", IPVersion: "system", Server: "203.0.113.9", Port: 1080}, nil
		case 6:
			return PlanOutbound{Type: "direct", IPVersion: "force_ipv4"}, nil
		case 7:
			return PlanOutbound{Type: "direct", IPVersion: "force_ipv6"}, nil
		case 9:
			return PlanOutbound{}, errors.New("WARP「WARP」尚未在本服务器注册成功，使用它的流量暂时封锁")
		}
		return PlanOutbound{}, errors.New("missing")
	}
	return in
}

// Empty global group + no node rules ⇒ byte-identical plans (zero behaviour
// change on upgrade), across every pre-134 fixture shape.
func TestLayersEmptyGlobalIsIdentical(t *testing.T) {
	cases := map[string]CompileInput{"prod-shaped": layersInput()}
	builtin := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 12, Tag: "flvxl-12"}, NodeRef{ID: 11, Tag: "flvxl-11"})
	cases["builtin-default"] = builtin
	notReady := layersInput()
	notReady.Abuse = false
	cases["not-ready"] = notReady
	oldAgent := layersInput()
	oldAgent.Builtin, oldAgent.ChainTags = false, nil
	cases["old-agent"] = oldAgent
	shared := layersInput()
	shared.Builtin, shared.ChainTags, shared.GeoSite = false, nil, true
	cases["shared"] = shared
	for name, in := range cases {
		before, warnBefore := compileOK(t, in)
		layered := in
		layered.Global = &Global{DefaultAction: ActionDirect, Forced: []Rule{{ID: "x", Name: "停用的强制", Enabled: false, Ports: "1", Action: ActionBlock}}}
		layered.Layers, layered.NodeRules, layered.Markers = true, true, in.Builtin
		after, warnAfter := compileOK(t, layered)
		if planHashOf(t, before) != planHashOf(t, after) || strings.Join(warnBefore, "|") != strings.Join(warnAfter, "|") {
			t.Fatalf("%s: empty global changed the plan:\n%s\n%s", name, mustJSON(before), mustJSON(after))
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

func layeredGlobal() *Global {
	return &Global{
		Forced:        []Rule{{ID: "f1", Name: "强制屏蔽", Enabled: true, Domains: []string{"domain:blocked.example"}, Action: ActionBlock}},
		Default:       []Rule{{ID: "d1", Name: "默认屏蔽 9999", Enabled: true, Ports: "9999", Action: ActionBlock}},
		DefaultAction: "outbound:5",
	}
}

// Order per node: 内置 → 全局·强制 → 节点 → 服务器 → 全局·默认 → 默认出站.
func TestLayersOrder(t *testing.T) {
	in := layersInput()
	in.Global, in.Layers, in.NodeRules, in.Markers = layeredGlobal(), true, true, true
	in.Nodes[0].Rules = []Rule{{ID: "n1", Name: "我的封锁", Enabled: true, Domains: []string{"domain:mine.example"}, Action: ActionBlock}}
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	goldenPlan(t, "layers-order", plan, rep.Warnings)
	var names []string
	for _, r := range plan.Rules {
		names = append(names, r.Name)
	}
	want := "默认：屏蔽 SMTP 25|默认：屏蔽 BT|强制屏蔽|我的封锁|AI|屏蔽大陆|仅 12|默认屏蔽 9999|默认出站|默认出站|全局默认出站"
	if got := strings.Join(names, "|"); got != want {
		t.Fatalf("order:\n got %s\nwant %s", got, want)
	}
	byName := func(name string) PlanRule {
		for _, r := range plan.Rules {
			if r.Name == name {
				return r
			}
		}
		t.Fatalf("rule %s missing", name)
		return PlanRule{}
	}
	if f := byName("强制屏蔽"); f.Builtin != BuiltinForced || strings.Join(f.InboundTags, ",") != "flvxl-1099511627790,flvxl-11,flvxl-12,flvxl-13,flvxl-14" {
		t.Fatalf("forced rule must cover every node + chain copies: %+v", f)
	}
	if n := byName("我的封锁"); n.Builtin != "" || strings.Join(n.InboundTags, ",") != "flvxl-11" {
		t.Fatalf("node rule must be scoped to its own node: %+v", n)
	}
	if d := byName("默认屏蔽 9999"); d.Builtin != BuiltinGlobal || strings.Join(d.InboundTags, ",") != "flvxl-11,flvxl-12,flvxl-13,flvxl-14" {
		t.Fatalf("global default must skip chain copies: %+v", d)
	}
	// Node 14 opted out of the server profile: the global default outbound.
	if g := byName("全局默认出站"); g.Builtin != BuiltinGlobal || strings.Join(g.InboundTags, ",") != "flvxl-14" || g.Outbound != "flvxo-5" {
		t.Fatalf("global default outbound: %+v", g)
	}
}

// Line-chain copies get only 内置 + 全局·强制; older agents (no markers)
// cannot order forced rules before chain rules, so chain copies are left out.
func TestLayersLineChain(t *testing.T) {
	in := layersInput()
	in.Global, in.Layers, in.NodeRules, in.Markers = layeredGlobal(), true, true, true
	plan, _ := compileOK(t, in)
	chain := "flvxl-1099511627790"
	for _, r := range plan.Rules {
		has := strings.Contains(strings.Join(r.InboundTags, ","), chain)
		switch {
		case r.Builtin == BuiltinAbuse || r.Builtin == BuiltinForced:
			if !has {
				t.Fatalf("chain copy missing from %s", r.Name)
			}
		case has:
			t.Fatalf("chain copy in non-forced rule %s", r.Name)
		}
	}
	in.Markers = false
	plan, _ = compileOK(t, in)
	for _, r := range plan.Rules {
		if r.Name == "强制屏蔽" && (r.Builtin != "" || strings.Contains(strings.Join(r.InboundTags, ","), chain)) {
			t.Fatalf("old agent: forced rule must be plain and skip chain copies: %+v", r)
		}
		if r.Builtin == BuiltinGlobal || r.Builtin == BuiltinForced {
			t.Fatalf("marker sent to an old agent: %+v", r)
		}
	}
}

// Node rules: own tag only; identical leading rules of different nodes merge
// into one rule (one copy of the category in the Core).
func TestLayersNodeRulesScopeAndMerge(t *testing.T) {
	in := layersInput()
	in.NodeRules = true
	shared := Rule{ID: "s", Name: "奈飞", Enabled: true, Domains: []string{"geosite:netflix"}, Action: ActionBlock}
	in.Nodes[0].Rules = []Rule{shared, {ID: "a", Name: "A 专属", Enabled: true, Ports: "7000", Action: ActionBlock}}
	in.Nodes[1].Rules = []Rule{shared}
	in.Nodes[2].Rules = []Rule{{ID: "off", Name: "停用", Enabled: false, Ports: "1", Action: ActionBlock}}
	plan, rep := compileOK(t, in)
	goldenPlan(t, "layers-node-rules", plan, rep)
	count := 0
	for _, r := range plan.Rules {
		switch r.Name {
		case "奈飞":
			count++
			if strings.Join(r.InboundTags, ",") != "flvxl-11,flvxl-12" {
				t.Fatalf("identical node rules must merge: %+v", r)
			}
		case "A 专属":
			if strings.Join(r.InboundTags, ",") != "flvxl-11" {
				t.Fatalf("node rule leaked: %+v", r)
			}
		case "停用":
			t.Fatal("disabled node rule sent")
		}
	}
	if count != 1 {
		t.Fatalf("netflix rule emitted %d times", count)
	}
	// Queued (Core cannot hot-apply yet): no node rules at all.
	in.NodeRules = false
	queued, _ := compileOK(t, in)
	base := layersInput()
	want, _ := compileOK(t, base)
	if planHashOf(t, queued) != planHashOf(t, want) {
		t.Fatal("queued node rules changed the plan")
	}
}

// 默认出站: node owner > admin override profile > server profile > global.
func TestLayersDefaultPrecedence(t *testing.T) {
	in := layersInput()
	in.Global = &Global{DefaultAction: "outbound:6"}
	in.Layers, in.NodeRules = true, true
	in.Nodes[1].Default = ActionBlock // node 12: owner choice beats the server profile
	plan, _ := compileOK(t, in)
	defaults := map[string]string{}
	for _, r := range plan.Rules {
		if strings.Contains(r.Name, "默认出站") {
			for _, tag := range r.InboundTags {
				defaults[tag] = r.Name + "→" + r.Outbound
			}
		}
	}
	want := map[string]string{
		"flvxl-11": "默认出站→flvxo-5",   // server profile 1
		"flvxl-12": "节点默认出站→block",   // owner
		"flvxl-13": "默认出站→block",     // admin override profile 2
		"flvxl-14": "全局默认出站→flvxo-6", // override 0 → global
	}
	if fmt.Sprint(defaults) != fmt.Sprint(want) {
		t.Fatalf("defaults = %v", defaults)
	}
	// A server profile with a direct default keeps direct (no global default).
	in.Profiles[3] = Profile{ID: 3, Name: "直连方案", DefaultAction: ActionDirect}
	in.ServerProfileID = 3
	in.Nodes = []NodeRef{{ID: 21, Tag: "flvxl-21"}}
	plan, _ = compileOK(t, in)
	for _, r := range plan.Rules {
		if strings.Contains(r.Name, "默认出站") {
			t.Fatalf("server profile direct default must win over the global default: %+v", r)
		}
	}
}

// IP version safety: a forced family the server lacks skips that part of the
// global / node layers (never black-holes the server) and says why.
func TestLayersForcedFamilySkipped(t *testing.T) {
	in := layersInput()
	in.NoIPv4 = true // IPv6-only server
	in.Global = &Global{
		Forced:        []Rule{{ID: "v4", Name: "走 IPv4", Enabled: true, Domains: []string{"domain:v4only.example"}, Action: "outbound:6"}, {ID: "v6", Name: "走 IPv6", Enabled: true, Domains: []string{"domain:v6.example"}, Action: "outbound:7"}},
		DefaultAction: "outbound:6",
	}
	in.Layers, in.NodeRules = true, true
	in.Nodes[0].Rules = []Rule{{ID: "n", Name: "我的 v4", Enabled: true, Ports: "443", Action: "outbound:6"}}
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	goldenPlan(t, "layers-ipv4-on-ipv6-only", plan, rep.Warnings)
	for _, r := range plan.Rules {
		if r.Outbound == "flvxo-6" {
			t.Fatalf("forced IPv4 outbound used on an IPv6-only server: %+v", r)
		}
	}
	found := false
	for _, r := range plan.Rules {
		found = found || r.Name == "走 IPv6"
	}
	if !found || len(rep.GlobalNotes) != 2 || len(rep.NodeNotes[11]) != 1 {
		t.Fatalf("notes: %+v / %+v", rep.GlobalNotes, rep.NodeNotes)
	}
}

// WARP that failed to register: global layers fall back to direct, server
// profile rules keep failing closed (block), both are reported.
func TestLayersWarpFallback(t *testing.T) {
	in := layersInput()
	in.Global = &Global{Forced: []Rule{{ID: "w", Name: "全局 WARP", Enabled: true, Domains: []string{"geosite:google"}, Action: "outbound:9"}}, DefaultAction: "outbound:9"}
	in.Layers = true
	in.Profiles[1] = Profile{ID: 1, Name: "解锁", DefaultAction: ActionDirect, Rules: []Rule{{ID: "w2", Name: "方案 WARP", Enabled: true, Domains: []string{"geosite:openai"}, Action: "outbound:9"}}}
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	goldenPlan(t, "layers-warp-fallback", plan, rep.Warnings)
	for _, r := range plan.Rules {
		switch r.Name {
		case "全局 WARP", "全局默认出站":
			if r.Outbound != ActionDirect {
				t.Fatalf("global WARP failure must fall back to direct: %+v", r)
			}
		case "方案 WARP":
			if r.Outbound != ActionBlock {
				t.Fatalf("profile WARP failure must stay blocked: %+v", r)
			}
		}
	}
	if len(rep.Warnings) != 2 {
		t.Fatalf("warnings: %v", rep.Warnings)
	}
}

// Node rules beyond the plan budget are dropped per node (highest node ID
// first) instead of failing the server; the anti-abuse defaults stay.
func TestLayersNodeRulesCapacity(t *testing.T) {
	in := abuseInput(map[int64]Profile{}, 0)
	for id := int64(1); id <= 30; id++ {
		n := NodeRef{ID: id, Tag: fmt.Sprintf("flvxl-%d", id)}
		for j := 0; j < 5; j++ {
			n.Rules = append(n.Rules, Rule{ID: fmt.Sprintf("r%d", j), Name: fmt.Sprintf("n%d-%d", id, j), Enabled: true, Ports: fmt.Sprint(1000 + int(id)*10 + j), Action: ActionBlock})
		}
		in.Nodes = append(in.Nodes, n)
	}
	in.NodeRules = true
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rules) > MaxPlanRules || plan.Rules[0].Name != "默认：屏蔽 SMTP 25" {
		t.Fatalf("plan rules %d, first %q", len(plan.Rules), plan.Rules[0].Name)
	}
	// 2 defaults + 25 nodes × 5 = 127 rules; nodes 26–30 dropped.
	if len(plan.Rules) != 127 || len(rep.NodeNotes) != 5 || rep.NodeNotes[30] == nil || rep.NodeNotes[25] != nil {
		t.Fatalf("rules %d notes %v", len(plan.Rules), rep.NodeNotes)
	}
}

// Readiness gates: global layers wait like the abuse defaults.
func TestLayersNotReady(t *testing.T) {
	in := layersInput()
	in.Global, in.Layers = layeredGlobal(), false
	plan, _ := compileOK(t, in)
	want, _ := compileOK(t, layersInput())
	if planHashOf(t, plan) != planHashOf(t, want) {
		t.Fatal("global layers sent to a server that cannot take them")
	}
}

func TestNormalizeGlobalAndNodeRules(t *testing.T) {
	outs := map[int64]bool{5: true}
	g := Global{Forced: []Rule{{Enabled: true, Domains: []string{"Blocked.Example"}, Action: ActionBlock}}, Default: []Rule{{Enabled: true, SourceIPs: []string{"geoip:cn"}, Action: ActionBlock}}}
	if err := NormalizeGlobal(&g, outs); err != nil || g.DefaultAction != ActionDirect || g.Forced[0].Domains[0] != "domain:blocked.example" {
		t.Fatalf("%v %+v", err, g)
	}
	bad := Global{Forced: []Rule{{Enabled: true, Ports: "1", LandingNodeIDs: []int64{3}, Action: ActionBlock}}}
	if NormalizeGlobal(&bad, outs) == nil {
		t.Fatal("global rule with node scope accepted")
	}
	bad = Global{DefaultAction: "outbound:8"}
	if NormalizeGlobal(&bad, outs) == nil {
		t.Fatal("unknown global default accepted")
	}
	rules, def, err := NormalizeNodeRules([]Rule{{Enabled: true, IPs: []string{"10.0.0.0/8"}, Action: ActionDirect}}, "", map[int64]bool{})
	if err != nil || def != "" || len(rules) != 1 {
		t.Fatalf("private CIDR rule (still blocked by the safety rule) must be accepted: %v", err)
	}
	for name, r := range map[string]Rule{
		"source ip":   {Enabled: true, SourceIPs: []string{"1.2.3.4"}, Action: ActionBlock},
		"source port": {Enabled: true, SourcePorts: "1000", Ports: "1", Action: ActionBlock},
		"node scope":  {Enabled: true, Ports: "1", LandingNodeIDs: []int64{1}, Action: ActionBlock},
		"unpublished": {Enabled: true, Ports: "1", Action: "outbound:5"},
	} {
		if _, _, err := NormalizeNodeRules([]Rule{r}, "", map[int64]bool{}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, _, err := NormalizeNodeRules(nil, "outbound:5", map[int64]bool{}); err == nil {
		t.Fatal("unpublished default accepted")
	}
	if _, def, err := NormalizeNodeRules(nil, "outbound:5", outs); err != nil || def != "outbound:5" {
		t.Fatalf("published default refused: %v", err)
	}
}
