package landing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func realityNode(id int64, guardPort int, dest string, names ...string) Node {
	if len(names) == 0 {
		names = []string{"www.lovelive-anime.jp"}
	}
	return Node{ID: id, InboundTag: "flvxl-" + strconv.FormatInt(id, 10), Protocol: "vless_reality_vision", Port: 31000 + int(id), Enabled: true, GuardPort: guardPort,
		VLESS: &VLESS{UUID: "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f", Flow: "xtls-rprx-vision", Reality: Reality{Dest: dest, ServerNames: names, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
}

func TestGuardGroups(t *testing.T) {
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{
		realityNode(1, 39999, "www.lovelive-anime.jp:443"),
		realityNode(2, 39999, "www.lovelive-anime.jp:443"),                            // same group
		realityNode(3, 39998, "[2001:db8::1]:8443", "B.example.com", "a.example.com"), // own group
		realityNode(4, 39999, "other.example.com:443"),                                // conflicts with group 39999
		realityNode(5, 31001, "www.lovelive-anime.jp:443"),                            // collides with node 1's public port
		realityNode(6, 0, "www.lovelive-anime.jp:443"),                                // no guard
		testNode(t, 7),
	}}
	d.Nodes[6].GuardPort = 39997 // SS2022 cannot be guarded
	off := realityNode(8, 39996, "www.lovelive-anime.jp:443")
	off.Enabled = false
	d.Nodes = append(d.Nodes, off)
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	groups, byTag, warnings := d.guardGroups()
	if len(groups) != 2 || groups[39998].Host != "2001:db8::1" || groups[39998].DestPort != 8443 || strings.Join(groups[39998].Names, ",") != "a.example.com,b.example.com" {
		t.Fatalf("groups = %+v", groups)
	}
	if byTag["flvxl-1"] != 39999 || byTag["flvxl-2"] != 39999 || byTag["flvxl-3"] != 39998 || len(byTag) != 3 {
		t.Fatalf("byTag = %v", byTag)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v", warnings)
	}
	// A disabled server guards nothing.
	d.Enabled = false
	if g, _, _ := d.guardGroups(); len(g) != 0 {
		t.Fatal("disabled server got guards")
	}
	// Rules: allow exact serverNames to direct, block the rest; only the helper inbound.
	rules := guardRules(groups)
	b, _ := json.Marshal(rules)
	want := `[{"domain":["full:www.lovelive-anime.jp"],"inboundTag":["flvxg-39999"],"outboundTag":"direct","protocol":["tls"],"ruleTag":"flvxg-39999-allow","type":"field"},{"inboundTag":["flvxg-39999"],"outboundTag":"block","ruleTag":"flvxg-39999-block","type":"field"},{"domain":["full:a.example.com","full:b.example.com"],"inboundTag":["flvxg-39998"],"outboundTag":"direct","protocol":["tls"],"ruleTag":"flvxg-39998-allow","type":"field"},{"inboundTag":["flvxg-39998"],"outboundTag":"block","ruleTag":"flvxg-39998-block","type":"field"}]`
	if string(b) == want {
		t.Fatal("rules must be in port order")
	}
	if !strings.HasPrefix(string(b), `[{"domain":["full:a.example.com","full:b.example.com"],"inboundTag":["flvxg-39998"]`) {
		t.Fatalf("rules = %s", b)
	}
	in := groups[39999].inbound()
	ib, _ := json.Marshal(in)
	if string(ib) != `{"listen":"127.0.0.1","port":39999,"protocol":"dokodemo-door","settings":{"address":"www.lovelive-anime.jp","network":"tcp","port":443},"sniffing":{"destOverride":["tls"],"enabled":true,"routeOnly":true},"tag":"flvxg-39999"}` {
		t.Fatalf("guard inbound = %s", ib)
	}
	cfg, err := d.Nodes[0].inbound()
	if err != nil {
		t.Fatal(err)
	}
	guarded := withGuardDest(cfg, 39999)
	gb, _ := json.Marshal(guarded)
	ob, _ := json.Marshal(cfg)
	if !strings.Contains(string(gb), `"dest":"127.0.0.1:39999"`) || !strings.Contains(string(ob), `"dest":"www.lovelive-anime.jp:443"`) {
		t.Fatalf("dest rewrite must not touch the original: %s / %s", gb, ob)
	}
	// Nothing user-visible changes: keys, serverNames, shortIds, port, listen.
	if !strings.Contains(string(gb), `"sockopt":{"customSockopt":[{"level":"6","network":"tcp","opt":"9","system":"linux","type":"int","value":"5"}]}`) {
		t.Fatalf("guarded inbound must defer accept until the ClientHello: %s", gb)
	}
	strip := func(s string) string {
		s = strings.Replace(s, `,"sockopt":{"customSockopt":[{"level":"6","network":"tcp","opt":"9","system":"linux","type":"int","value":"5"}]}`, "", 1)
		return strings.Replace(strings.Replace(s, `"dest":"127.0.0.1:39999"`, "", 1), `"dest":"www.lovelive-anime.jp:443"`, "", 1)
	}
	if strip(string(gb)) != strip(string(ob)) {
		t.Fatalf("guard changed more than dest and the accept option:\n%s\n%s", gb, ob)
	}
}

func TestApplyEgressGuardsOnly(t *testing.T) {
	dir := t.TempDir()
	log := fakeCore(t, dir, true)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{realityNode(1, 39999, "www.lovelive-anime.jp:443")}}
	m.guardWant, m.guardByTag, _ = m.desired.guardGroups()
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	rulesRaw, _ := os.ReadFile(filepath.Join(dir, "last-rules.json"))
	var rules struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(rulesRaw, &rules); err != nil || len(rules.Routing.Rules) != 3 || rules.Routing.Rules[0]["ruleTag"] != "flvxg-39999-allow" || rules.Routing.Rules[2]["ruleTag"] != "flvx-safety" {
		t.Fatalf("guard rules must precede the safety rule: %s", rulesRaw)
	}
	if !m.guardOn[39999] || m.egress.State != "none" || m.egress.Guard != "on" || m.egress.GuardNodes != 1 || m.applied.Egress == "" {
		t.Fatalf("guard state %+v on=%v applied=%q", m.egress, m.guardOn, m.applied.Egress)
	}
	// Steady state: no CLI calls.
	_ = os.Remove(log)
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil || !m.guardOn[39999] {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err == nil {
		calls, _ := os.ReadFile(log)
		t.Fatalf("idempotent guard apply invoked the Core: %s", calls)
	}
	// Guard switched off: rules back to the safety rule only.
	m.desired.Nodes[0].GuardPort = 0
	m.desired.Revision++
	m.guardWant, m.guardByTag, _ = m.desired.guardGroups()
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	rulesRaw, _ = os.ReadFile(filepath.Join(dir, "last-rules.json"))
	_ = json.Unmarshal(rulesRaw, &rules)
	if len(rules.Routing.Rules) != 1 || m.applied.Egress != "" || len(m.guardOn) != 0 || m.egress.Guard != "" {
		t.Fatalf("guard removal: %s %+v", rulesRaw, m.egress)
	}
}

func TestGuardFailOpenWithoutRouting(t *testing.T) {
	dir := t.TempDir()
	fakeCore(t, dir, false)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{realityNode(1, 39999, "www.lovelive-anime.jp:443")}}
	m.guardWant, m.guardByTag, _ = m.desired.guardGroups()
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatalf("guard alone must never fail the apply: %v", err)
	}
	if len(m.guardOn) != 0 || m.egress.Guard != "restart_required" || len(m.withheldTags()) != 0 {
		t.Fatalf("old Core: %+v on=%v", m.egress, m.guardOn)
	}
}

func TestBuiltinRulesOrderAndFailOpen(t *testing.T) {
	upstream := EgressOutbound{Type: "socks", Server: "203.0.113.11", Port: 1080}
	chain := chainNode(int64(1<<40+1), upstream)
	plain := testNode(t, 2)
	plan := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 SMTP 25", InboundTags: []string{chain.InboundTag, plain.InboundTag}, Port: "25", Network: "tcp", Outbound: "block", Builtin: "abuse"},
		{Name: "默认：屏蔽 BT", InboundTags: []string{chain.InboundTag, plain.InboundTag}, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: "abuse"},
	}}
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{chain, plain}, Egress: plan}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), true)
	m.desired = d
	merged := m.effectiveEgress()
	var names []string
	for _, r := range merged.Rules {
		names = append(names, r.Name)
	}
	if strings.Join(names, "|") != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|chain" {
		t.Fatalf("builtin rules must precede chain rules: %v", names)
	}
	// Builtin-only coverage never withholds a node, even on an old Core.
	if c := plan.coveredTags(); len(c) != 0 {
		t.Fatalf("builtin rules counted as coverage: %v", c)
	}
	dir := t.TempDir()
	fakeCore(t, dir, false)
	m2 := NewManager(dir, true)
	m2.apiPort, m2.epoch = 1, "e1"
	m2.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m2.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{plain}, Egress: &Egress{Version: 1, Rules: plan.Rules}}
	m2.desired.Egress.Rules = []EgressRule{plan.Rules[0], plan.Rules[1]}
	for i := range m2.desired.Egress.Rules {
		m2.desired.Egress.Rules[i].InboundTags = []string{plain.InboundTag}
	}
	if _, err := m2.applyEgress(context.Background(), coreSnapshot{}); err != errRestartRequired {
		t.Fatalf("err = %v", err)
	}
	if w := m2.withheldTags(); len(w) != 0 {
		t.Fatalf("builtin-only plan withheld nodes on an old Core: %v", w)
	}
	// A builtin rule may only block.
	bad := Desired{Revision: 1, Enabled: true, Nodes: []Node{plain}, Egress: &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{plain.InboundTag}, Port: "25", Outbound: "direct", Builtin: "abuse"}}}}
	if err := bad.validate(); err == nil {
		t.Fatal("builtin rule routing to direct accepted")
	}
	bad.Egress.Rules[0].Outbound, bad.Egress.Rules[0].Builtin = "block", "other"
	if err := bad.validate(); err == nil {
		t.Fatal("unknown builtin kind accepted")
	}
}

// Plan 134: global layer markers. 全局·强制 ("forced") goes before chain rules
// like the abuse defaults; 全局·默认 ("global") stays in place; neither counts
// as coverage, so a failed global change never withholds a node.
func TestGlobalLayerMarkersOrderAndFailOpen(t *testing.T) {
	upstream := EgressOutbound{Type: "socks", Server: "203.0.113.11", Port: 1080}
	chain := chainNode(int64(1<<40+1), upstream)
	plain := testNode(t, 2)
	both := []string{chain.InboundTag, plain.InboundTag}
	only := []string{plain.InboundTag}
	plan := &Egress{Version: 1, Outbounds: []EgressOutbound{{Tag: "flvxo-7", Type: "direct", IPVersion: "force_ipv4"}}, Rules: []EgressRule{
		{Name: "默认：屏蔽 BT", InboundTags: both, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: "abuse"},
		{Name: "强制", InboundTags: both, Domain: []string{"domain:blocked.example"}, Outbound: "block", Builtin: "forced"},
		{Name: "强制 v4", InboundTags: both, Port: "8443", Outbound: "flvxo-7", Builtin: "forced"},
		{Name: "节点", InboundTags: only, Port: "9000", Outbound: "block"},
		{Name: "全局默认", InboundTags: only, Port: "53", Outbound: "flvxo-7", Builtin: "global"},
		{Name: "全局默认出站", InboundTags: only, Outbound: "direct", Builtin: "global"},
	}}
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{chain, plain}, Egress: plan}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), true)
	m.desired = d
	var names []string
	for _, r := range m.effectiveEgress().Rules {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, "|"); got != "默认：屏蔽 BT|强制|强制 v4|chain|节点|全局默认|全局默认出站" {
		t.Fatalf("layer order = %s", got)
	}
	c := plan.coveredTags()
	if len(c) != 1 || !c[plain.InboundTag] {
		t.Fatalf("only the node rule may cover: %v", c)
	}
	// Global-only plan on an old Core: nothing withheld.
	dir := t.TempDir()
	fakeCore(t, dir, false)
	m2 := NewManager(dir, true)
	m2.apiPort, m2.epoch = 1, "e1"
	m2.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m2.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{plain}, Egress: &Egress{Version: 1, Outbounds: plan.Outbounds, Rules: []EgressRule{
		{Name: "强制", InboundTags: only, Domain: []string{"domain:blocked.example"}, Outbound: "block", Builtin: "forced"},
		{Name: "全局默认出站", InboundTags: only, Outbound: "flvxo-7", Builtin: "global"},
	}}}
	if _, err := m2.applyEgress(context.Background(), coreSnapshot{}); err != errRestartRequired {
		t.Fatalf("err = %v", err)
	}
	if w := m2.withheldTags(); len(w) != 0 {
		t.Fatalf("global-only plan withheld nodes: %v", w)
	}
	bad := Desired{Revision: 1, Enabled: true, Nodes: []Node{plain}, Egress: &Egress{Version: 1, Rules: []EgressRule{{InboundTags: only, Port: "25", Outbound: "block", Builtin: "unknown"}}}}
	if err := bad.validate(); err == nil {
		t.Fatal("unknown builtin kind accepted")
	}
}
