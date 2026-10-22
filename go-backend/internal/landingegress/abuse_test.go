package landingegress

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

func boolPtr(v bool) *bool { return &v }

// goldenPlan compares the compiled plan (and warnings) with testdata/<name>.json.
func goldenPlan(t *testing.T, name string, plan *Plan, warnings []string) {
	t.Helper()
	got, err := json.MarshalIndent(map[string]any{"plan": plan, "active": plan.Active(), "warnings": warnings}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name+".json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs from golden:\n%s", name, got)
	}
}

func abuseInput(profiles map[int64]Profile, serverProfile int64, nodes ...NodeRef) CompileInput {
	return CompileInput{
		ServerProfileID: serverProfile,
		Nodes:           nodes,
		Profiles:        profiles,
		Resolve: func(id int64) (PlanOutbound, error) {
			if id == 5 {
				return PlanOutbound{Type: "socks", IPVersion: "system", Server: "203.0.113.9", Port: 1080}, nil
			}
			return PlanOutbound{}, errors.New("missing")
		},
		Exempt:  func() ([]string, error) { return nil, nil },
		GeoIP:   true,
		GeoSite: true,
		Abuse:   true,
	}
}

func compileOK(t *testing.T, in CompileInput) (*Plan, []string) {
	t.Helper()
	plan, warnings, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	return plan, warnings
}

func TestAbuseDefaultsBuiltinProfile(t *testing.T) {
	in := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 12, Tag: "flvxl-12"}, NodeRef{ID: 11, Tag: "flvxl-11"})
	plan, warnings := compileOK(t, in)
	goldenPlan(t, "abuse-builtin-default", plan, warnings)
	if !plan.Active() || len(plan.Rules) != 2 || len(plan.Outbounds) != 0 {
		t.Fatalf("built-in default must carry exactly the two default rules: %+v", plan)
	}
	// Idempotent: compiling again gives the same bytes (no reordering, no dup).
	again, _ := compileOK(t, in)
	a, _ := json.Marshal(plan)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("re-compile changed the plan")
	}
	// Not ready (native Core without RoutingService): today's payload.
	in.Abuse = false
	plan, _ = compileOK(t, in)
	if plan.Active() {
		t.Fatal("defaults sent to a server that cannot take plan rules yet")
	}
	// No nodes: nothing to send.
	in = abuseInput(map[int64]Profile{}, 0)
	plan, _ = compileOK(t, in)
	if plan.Active() {
		t.Fatal("server without nodes produced an active plan")
	}
}

func TestAbuseDefaultsCustomProfile(t *testing.T) {
	profiles := map[int64]Profile{
		1: {ID: 1, Name: "A", DefaultAction: "outbound:5", Rules: []Rule{
			{ID: "ai", Name: "AI", Enabled: true, Domains: []string{"geosite:openai"}, Action: ActionBlock},
			{ID: "p8", Name: "8081", Enabled: true, Ports: "8081", Action: ActionBlock},
		}},
	}
	in := abuseInput(profiles, 1, NodeRef{ID: 11, Tag: "flvxl-11"}, NodeRef{ID: 12, Tag: "flvxl-12", Override: ptr(0)})
	plan, warnings := compileOK(t, in)
	goldenPlan(t, "abuse-custom-profile", plan, warnings)
	if plan.Rules[0].Port != "25" || plan.Rules[1].Protocol[0] != "bittorrent" {
		t.Fatalf("default rules must come first: %+v", plan.Rules)
	}
	for _, r := range plan.Rules[:2] {
		if strings.Join(r.InboundTags, ",") != "flvxl-11,flvxl-12" || r.Builtin != "" {
			t.Fatalf("default rule must cover profile + built-in nodes without builtin marker: %+v", r)
		}
	}
	// Existing user rules keep their order after the defaults.
	if plan.Rules[2].Name != "AI" || plan.Rules[3].Name != "8081" || plan.Rules[4].Name != "默认出站" {
		t.Fatalf("profile rule order changed: %+v", plan.Rules)
	}
}

func TestAbuseDefaultsDedupeBTPreset(t *testing.T) {
	// 「屏蔽 BT」 preset as the frontend stores it, plus an identical TCP 25
	// rule and a broader UDP+TCP 25 rule that must stay.
	profiles := map[int64]Profile{
		1: {ID: 1, Name: "A", DefaultAction: ActionDirect, Rules: []Rule{
			{ID: "bt", Name: "屏蔽 BT", Enabled: true, Protocols: []string{"bittorrent"}, Action: ActionBlock, Preset: "block-bt"},
			{ID: "smtp", Name: "封 25", Enabled: true, Ports: "25", Network: "tcp", LandingNodeIDs: []int64{11}, Action: ActionBlock},
			{ID: "smtp-all", Name: "封 25 全部", Enabled: true, Ports: "25", Action: ActionBlock},
			{ID: "bt-direct", Name: "BT 直连", Enabled: true, Protocols: []string{"bittorrent"}, Action: ActionDirect},
		}},
	}
	in := abuseInput(profiles, 1, NodeRef{ID: 11, Tag: "flvxl-11"})
	plan, warnings := compileOK(t, in)
	goldenPlan(t, "abuse-bt-preset-dedupe", plan, warnings)
	names := []string{}
	for _, r := range plan.Rules {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, "|"); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|封 25 全部|BT 直连" {
		t.Fatalf("dedupe wrong: %s", got)
	}
	// With the defaults switched off the preset rule is kept as before.
	p := profiles[1]
	p.AbuseDefaults = boolPtr(false)
	profiles[1] = p
	plan, _ = compileOK(t, in)
	if len(plan.Rules) != 4 || plan.Rules[0].Name != "屏蔽 BT" {
		t.Fatalf("profile without defaults must keep its own rules unchanged: %+v", plan.Rules)
	}
}

func TestAbuseDefaultsToggleOff(t *testing.T) {
	profiles := map[int64]Profile{
		1: {ID: 1, Name: "Off", DefaultAction: ActionDirect, AbuseDefaults: boolPtr(false)},
		2: {ID: 2, Name: "On", DefaultAction: ActionDirect, AbuseDefaults: boolPtr(true), Rules: []Rule{{ID: "x", Name: "x", Enabled: true, Ports: "8081", Action: ActionBlock}}},
	}
	in := abuseInput(profiles, 1,
		NodeRef{ID: 11, Tag: "flvxl-11"},                   // server profile 1 (off)
		NodeRef{ID: 12, Tag: "flvxl-12", Override: ptr(2)}, // on
		NodeRef{ID: 13, Tag: "flvxl-13", Override: ptr(0)}, // built-in default (always on)
	)
	plan, warnings := compileOK(t, in)
	goldenPlan(t, "abuse-toggle-off", plan, warnings)
	for _, r := range plan.Rules[:2] {
		if strings.Join(r.InboundTags, ",") != "flvxl-12,flvxl-13" {
			t.Fatalf("switched-off profile still covered: %+v", r)
		}
	}
	// Every node off → the plan is empty again (today's payload).
	in.Nodes = in.Nodes[:1]
	plan, _ = compileOK(t, in)
	if plan.Active() {
		t.Fatal("all-off server produced an active plan")
	}
}

func TestAbuseDefaultsLineChain(t *testing.T) {
	chain := fmt.Sprintf("flvxl-%d", int64(1<<40)+7)
	in := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 11, Tag: "flvxl-11"})
	in.ChainTags = []string{chain}
	// Old agent: chain copies are not covered, no marker.
	plan, _ := compileOK(t, in)
	for _, r := range plan.Rules {
		if r.Builtin != "" || strings.Contains(strings.Join(r.InboundTags, ","), chain) {
			t.Fatalf("old agent got builtin marker or chain tag: %+v", r)
		}
	}
	// Agent with landing_guard: chain copies covered and marked.
	in.Builtin = true
	plan, warnings := compileOK(t, in)
	goldenPlan(t, "abuse-line-chain", plan, warnings)
	for _, r := range plan.Rules {
		if r.Builtin != BuiltinAbuse || strings.Join(r.InboundTags, ",") != chain+",flvxl-11" {
			t.Fatalf("chain coverage wrong: %+v", r)
		}
	}
	// Chain-only server (no landing nodes) still gets the defaults.
	in.Nodes = nil
	plan, _ = compileOK(t, in)
	if len(plan.Rules) != 2 || strings.Join(plan.Rules[0].InboundTags, ",") != chain {
		t.Fatalf("chain-only server: %+v", plan.Rules)
	}
	// Server profile with the defaults off also switches them off for chains.
	in.ServerProfileID = 3
	in.Profiles = map[int64]Profile{3: {ID: 3, Name: "Off", DefaultAction: ActionDirect, AbuseDefaults: boolPtr(false)}}
	plan, _ = compileOK(t, in)
	if plan.Active() {
		t.Fatalf("chain defaults ignored the server switch: %+v", plan.Rules)
	}
}

func TestAbuseDefaultsNeverBreakFullPlan(t *testing.T) {
	rules := []Rule{}
	for i := 0; i < MaxProfileRules; i++ {
		rules = append(rules, Rule{ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("r%d", i), Enabled: true, Ports: fmt.Sprintf("%d", 1000+i), Action: ActionBlock})
	}
	other := append([]Rule(nil), rules...)
	for i := range other {
		other[i].Ports = fmt.Sprintf("%d", 3000+i)
	}
	profiles := map[int64]Profile{1: {ID: 1, Name: "A", DefaultAction: ActionDirect, Rules: rules}, 2: {ID: 2, Name: "B", DefaultAction: ActionDirect, Rules: other}}
	in := abuseInput(profiles, 1, NodeRef{ID: 11, Tag: "flvxl-11"}, NodeRef{ID: 12, Tag: "flvxl-12", Override: ptr(2)})
	plan, warnings, err := Compile(in)
	if err != nil {
		t.Fatalf("a plan valid without defaults must stay valid: %v", err)
	}
	if len(plan.Rules) != MaxPlanRules || len(warnings) != 1 || plan.Rules[0].Name == "默认：屏蔽 SMTP 25" {
		t.Fatalf("full plan: %d rules, warnings %v", len(plan.Rules), warnings)
	}
}

func TestIsAbuseDuplicate(t *testing.T) {
	cases := []struct {
		r    Rule
		want bool
	}{
		{Rule{Protocols: []string{"bittorrent"}, Action: ActionBlock}, true},
		{Rule{Protocols: []string{"bittorrent"}, Action: ActionBlock, LandingNodeIDs: []int64{3}}, true},
		{Rule{Ports: "25", Network: "tcp", Action: ActionBlock}, true},
		{Rule{Ports: "25", Action: ActionBlock}, false},
		{Rule{Protocols: []string{"bittorrent", "quic"}, Action: ActionBlock}, false},
		{Rule{Protocols: []string{"bittorrent"}, Action: ActionDirect}, false},
		{Rule{Protocols: []string{"bittorrent"}, SourceIPs: []string{"geoip:cn"}, Action: ActionBlock}, false},
		{Rule{Ports: "25", Network: "tcp", Domains: []string{"domain:a.com"}, Action: ActionBlock}, false},
	}
	for i, c := range cases {
		if got := IsAbuseDuplicate(c.r); got != c.want {
			t.Fatalf("case %d: %v want %v", i, got, c.want)
		}
	}
}

// TestAbusePlanSize reports the compiled plan size before/after plan 127 for a
// prod-like server (1 node on the built-in default) and a busy one (50 nodes,
// half on a 10-rule profile). Run with -v to see the numbers.
func TestAbusePlanSize(t *testing.T) {
	size := func(in CompileInput) (int, int) {
		plan, _, err := Compile(in)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Active() {
			return 0, 0
		}
		b, _ := json.Marshal(plan)
		return len(plan.Rules), len(b)
	}
	one := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 10, Tag: "flvxl-10"})
	one.Abuse = false
	r0, b0 := size(one)
	one.Abuse = true
	r1, b1 := size(one)
	rules := []Rule{}
	for i := 0; i < 10; i++ {
		rules = append(rules, Rule{ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("规则 %d", i), Enabled: true, Ports: fmt.Sprintf("%d", 8000+i), Action: ActionBlock})
	}
	profiles := map[int64]Profile{1: {ID: 1, Name: "A", DefaultAction: ActionDirect, Rules: rules}}
	var nodes []NodeRef
	for i := int64(1); i <= 50; i++ {
		n := NodeRef{ID: i, Tag: fmt.Sprintf("flvxl-%d", i)}
		if i%2 == 0 {
			n.Override = ptr(0)
		}
		nodes = append(nodes, n)
	}
	busy := abuseInput(profiles, 1, nodes...)
	busy.Abuse = false
	r2, b2 := size(busy)
	busy.Abuse = true
	r3, b3 := size(busy)
	t.Logf("prod-like (1 node, built-in default): before %d rules / %d B, after %d rules / %d B", r0, b0, r1, b1)
	t.Logf("busy (50 nodes, 25 on a 10-rule profile): before %d rules / %d B, after %d rules / %d B", r2, b2, r3, b3)
	if r1 != 2 || r3 != r2+2 {
		t.Fatalf("unexpected rule counts %d %d %d", r1, r2, r3)
	}
}
