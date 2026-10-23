package landingegress

import (
	"encoding/json"
	"strings"
	"testing"
)

// Plan 140: global switches of the default rules, seeds, preset library and
// the sync scope of every layer.

func ruleNames(p *Plan) string {
	var names []string
	for _, r := range p.Rules {
		names = append(names, r.Name)
	}
	return strings.Join(names, "|")
}

func findRule(p *Plan, name string) (PlanRule, bool) {
	for _, r := range p.Rules {
		if r.Name == name {
			return r, true
		}
	}
	return PlanRule{}, false
}

// Global switch off → that default rule is absent everywhere; the profile's
// own toggle still wins when the switch is on.
func TestAbuseSwitches(t *testing.T) {
	in := layersInput()
	plan, _ := compileOK(t, in)
	if _, ok := findRule(plan, "默认：屏蔽 SMTP 25"); !ok {
		t.Fatal("default SMTP rule missing with switches on")
	}
	// Zero switches = pre-140 output, byte for byte.
	zero := in
	zero.AbuseOff = AbuseSwitches{}
	if p2, _ := compileOK(t, zero); planHashOf(t, p2) != planHashOf(t, plan) {
		t.Fatal("zero switches changed the plan")
	}

	in.AbuseOff = AbuseSwitches{SMTPOff: true}
	plan, _ = compileOK(t, in)
	if _, ok := findRule(plan, "默认：屏蔽 SMTP 25"); ok {
		t.Fatalf("SMTP rule present with the global switch off: %s", ruleNames(plan))
	}
	bt, ok := findRule(plan, "默认：屏蔽 BT")
	if !ok || bt.Builtin != BuiltinAbuse {
		t.Fatalf("BT rule must stay: %s", ruleNames(plan))
	}
	// Node 13 is on profile 2 (defaults off): still excluded, chain copy included.
	if tags := strings.Join(bt.InboundTags, ","); tags != "flvxl-1099511627790,flvxl-11,flvxl-12,flvxl-14" {
		t.Fatalf("BT tags %s", tags)
	}

	in.AbuseOff = AbuseSwitches{SMTPOff: true, BTOff: true}
	plan, _ = compileOK(t, in)
	for _, r := range plan.Rules {
		if r.Builtin == BuiltinAbuse {
			t.Fatalf("default rule present with both switches off: %+v", r)
		}
	}
}

// A profile rule repeating a default rule is dropped only while that default
// rule is sent; with the global switch off it stays in the plan.
func TestAbuseDuplicateFollowsSwitch(t *testing.T) {
	profiles := map[int64]Profile{1: {ID: 1, Name: "p", DefaultAction: ActionDirect, Rules: []Rule{
		{ID: "smtp", Name: "我的 25", Enabled: true, Ports: "25", Network: "tcp", Action: ActionBlock},
		{ID: "bt", Name: "我的 BT", Enabled: true, Protocols: []string{"bittorrent"}, Action: ActionBlock},
	}}}
	in := abuseInput(profiles, 1, NodeRef{ID: 1, Tag: "flvxl-1"})
	plan, _ := compileOK(t, in)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT" {
		t.Fatalf("duplicates not merged into the defaults: %s", got)
	}
	in.AbuseOff = AbuseSwitches{SMTPOff: true}
	plan, _ = compileOK(t, in)
	if got := ruleNames(plan); got != "默认：屏蔽 BT|我的 25" {
		t.Fatalf("SMTP off: profile rule must stay: %s", got)
	}
	if AbuseDuplicateOf(profiles[1].Rules[0]) != AbuseSMTP || AbuseDuplicateOf(profiles[1].Rules[1]) != AbuseBT || !IsAbuseDuplicate(profiles[1].Rules[1]) {
		t.Fatal("AbuseDuplicateOf keys")
	}
}

// The 全局·强制 seeds are disabled, valid, and never change a plan.
func TestForcedSeedsDisabledAndIdentical(t *testing.T) {
	seeds := ForcedSeedRules()
	// Plan 143: the 防滥用 seeds live in 内置 now; only the 大陆 rules remain.
	if len(seeds) != 2 {
		t.Fatalf("seeds %d", len(seeds))
	}
	want := []string{"屏蔽大陆 IP", "屏蔽大陆网站"}
	for i, r := range seeds {
		if r.Enabled || r.Action != ActionBlock || r.Name != want[i] {
			t.Fatalf("seed %d: %+v", i, r)
		}
	}
	g := Global{Forced: AppendSeedRules(nil, seeds), Default: []Rule{}, DefaultAction: ActionDirect}
	before, _ := json.Marshal(g.Forced)
	if err := NormalizeGlobal(&g, map[int64]bool{}); err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(g.Forced); string(after) != string(before) {
		t.Fatalf("seeds are not in normalized form:\n%s\n%s", before, after)
	}
	if g.Active() {
		t.Fatal("disabled seeds made the global group active")
	}
	for name, in := range map[string]CompileInput{"prod-shaped": layersInput(), "shared": func() CompileInput {
		in := layersInput()
		in.Builtin, in.ChainTags = false, nil
		return in
	}()} {
		empty := in
		empty.Global, empty.Layers, empty.NodeRules, empty.Markers = &Global{DefaultAction: ActionDirect}, true, true, in.Builtin
		seeded := empty
		seeded.Global = &g
		a, wa := compileOK(t, empty)
		b, wb := compileOK(t, seeded)
		if planHashOf(t, a) != planHashOf(t, b) || strings.Join(wa, "|") != strings.Join(wb, "|") {
			t.Fatalf("%s: disabled seeds changed the plan", name)
		}
	}
	// Appending twice never duplicates; an equal existing rule is kept as is.
	existing := []Rule{{ID: "mine", Name: "我的大陆", Enabled: true, IPs: []string{"geoip:cn"}, Action: ActionBlock}}
	out := AppendSeedRules(AppendSeedRules(existing, seeds), seeds)
	if len(out) != 2 || out[0].ID != "mine" || !out[0].Enabled {
		t.Fatalf("append: %+v", out)
	}
}

func TestSeedPresetsNormalized(t *testing.T) {
	seeds := SeedPresets()
	if len(seeds) != 10 {
		t.Fatalf("seed presets %d", len(seeds))
	}
	keys := map[string]bool{}
	for _, p := range seeds {
		if keys[p.SeedKey] {
			t.Fatalf("duplicate seed key %s", p.SeedKey)
		}
		keys[p.SeedKey] = true
		before, _ := json.Marshal(p.Rules)
		c := p
		c.Rules = append([]Rule(nil), p.Rules...)
		if err := NormalizePreset(&c, map[int64]bool{}); err != nil {
			t.Fatalf("%s: %v", p.SeedKey, err)
		}
		if after, _ := json.Marshal(c.Rules); string(after) != string(before) {
			t.Fatalf("%s not normalized:\n%s\n%s", p.SeedKey, before, after)
		}
	}
	if !keys["block-private"] {
		t.Fatal("禁止访问内网 missing")
	}
	private := seeds[7]
	if private.SeedKey != "block-private" || private.Rules[0].IPs[0] != "geoip:private" || private.Rules[0].Action != ActionBlock {
		t.Fatalf("block-private: %+v", private)
	}
}

func TestNormalizePresetRules(t *testing.T) {
	outs := map[int64]bool{5: true}
	ask := Preset{Name: "解锁", AskOutbound: true, Rules: []Rule{{ID: "x", Name: "a", Enabled: true, Domains: []string{"geosite:openai"}, Action: ActionAsk, LandingNodeIDs: []int64{3}}}}
	if err := NormalizePreset(&ask, outs); err != nil {
		t.Fatal(err)
	}
	if r := ask.Rules[0]; r.ID != "" || len(r.LandingNodeIDs) != 0 || r.Action != ActionAsk {
		t.Fatalf("ask rule not normalized: %+v", r)
	}
	for name, p := range map[string]Preset{
		"ask without flag":     {Name: "x", Rules: []Rule{{Name: "a", Enabled: true, Ports: "1", Action: ActionAsk}}},
		"flag without ask":     {Name: "x", AskOutbound: true, Rules: []Rule{{Name: "a", Enabled: true, Ports: "1", Action: ActionBlock}}},
		"no rules":             {Name: "x"},
		"no name":              {Rules: []Rule{{Name: "a", Enabled: true, Ports: "1", Action: ActionBlock}}},
		"deleted outbound":     {Name: "x", Rules: []Rule{{Name: "a", Enabled: true, Ports: "1", Action: "outbound:9"}}},
		"no matcher":           {Name: "x", Rules: []Rule{{Name: "a", Enabled: true, Action: ActionBlock}}},
		"description too long": {Name: "x", Description: strings.Repeat("长", MaxPresetDescRunes+1), Rules: []Rule{{Name: "a", Enabled: true, Ports: "1", Action: ActionBlock}}},
	} {
		if err := NormalizePreset(&p, outs); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestPresetUsableByUser(t *testing.T) {
	allowed := map[int64]bool{5: true}
	cases := []struct {
		rule Rule
		want bool
	}{
		{Rule{Ports: "1", Action: ActionBlock}, true},
		{Rule{Domains: []string{"geosite:openai"}, Action: ActionAsk}, true},
		{Rule{Domains: []string{"geosite:openai"}, Action: "outbound:5"}, true},
		{Rule{Domains: []string{"geosite:openai"}, Action: "outbound:6"}, false},
		{Rule{SourceIPs: []string{"geoip:cn"}, Action: ActionBlock}, false},
		{Rule{SourcePorts: "1000", Action: ActionBlock}, false},
	}
	for i, c := range cases {
		if got := PresetUsableByUser(Preset{Rules: []Rule{c.rule}}, allowed); got != c.want {
			t.Fatalf("case %d: got %v", i, got)
		}
	}
	if PresetUsableByUser(SeedPresets()[0], allowed) {
		t.Fatal("屏蔽中国大陆 (来源 IP rule) offered to users")
	}
}

// §3 sync scope: 内置 (default rules) and 全局·强制 reach every node incl.
// line-chain copies (agents with the markers); 全局·默认 never reaches chain
// copies; a chain-only server (no landing nodes) gets 内置 + 强制 only;
// mmwx-shared servers (no markers, no chain copies) get every layer.
func TestLayerSyncScope(t *testing.T) {
	g := &Global{
		Forced:        []Rule{{ID: "f", Name: "强制", Enabled: true, Ports: "6666", Action: ActionBlock}},
		Default:       []Rule{{ID: "d", Name: "默认", Enabled: true, Ports: "7777", Action: ActionBlock}},
		DefaultAction: ActionDirect,
	}
	chain := "flvxl-1099511627790"
	native := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 1, Tag: "flvxl-1"})
	native.Builtin, native.Markers, native.ChainTags = true, true, []string{chain}
	native.Global, native.Layers, native.NodeRules = g, true, true
	plan, _ := compileOK(t, native)
	for _, r := range plan.Rules {
		tags := strings.Join(r.InboundTags, ",")
		switch r.Name {
		case "默认：屏蔽 SMTP 25", "默认：屏蔽 BT", "强制":
			if tags != "flvxl-1,"+chain {
				t.Fatalf("native %s tags %s", r.Name, tags)
			}
		case "默认":
			if tags != "flvxl-1" {
				t.Fatalf("native default rule reached the chain copy: %s", tags)
			}
		}
	}
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|强制|默认" {
		t.Fatalf("native order %s", got)
	}

	chainOnly := native
	chainOnly.Nodes = nil
	plan, _ = compileOK(t, chainOnly)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|强制" {
		t.Fatalf("chain-only server: %s", got)
	}

	shared := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 2, Tag: "flvxs-2"})
	shared.Global, shared.Layers, shared.NodeRules = g, true, true
	plan, _ = compileOK(t, shared)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|强制|默认" {
		t.Fatalf("shared: %s", got)
	}
	for _, r := range plan.Rules {
		if r.Builtin != "" || strings.Join(r.InboundTags, ",") != "flvxs-2" {
			t.Fatalf("shared rule %+v", r)
		}
	}
}
