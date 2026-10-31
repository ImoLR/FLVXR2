package landingegress

import (
	"fmt"
	"strings"
	"testing"
)

// Plan 177 「链式代理」 compile semantics.

func chainOut(server string) *PlanOutbound {
	return &PlanOutbound{Type: "vless", IPVersion: "system", Server: server, Port: 443, UUID: "4b2f1c84-1f7e-4f3b-9d4c-2f0f6b6a1c11", Flow: "xtls-rprx-vision", Network: "tcp", Security: "reality",
		Reality: &PlanReality{ServerName: "www.example.com", PublicKey: "x", ShortID: "ab", Fingerprint: "chrome"}}
}

// rulesFor lists (name, outbound) of the rules covering one inbound tag, in
// order (what the Core evaluates for that node; first match wins).
func rulesFor(p *Plan, tag string) []string {
	var out []string
	for _, r := range p.Rules {
		for _, t := range r.InboundTags {
			if t == tag {
				out = append(out, r.Name+"→"+r.Outbound)
			}
		}
	}
	return out
}

// Block rules of every layer keep applying first; every non-block route
// (node / server / global) is replaced by the chain, which is the last rule.
func TestChainReplacesNonBlockRoutesBlocksFirst(t *testing.T) {
	in := layersInput()
	in.Layers, in.NodeRules, in.Markers = true, true, true
	in.Global = &Global{
		Forced: []Rule{
			{ID: "f1", Name: "强制封锁 25", Enabled: true, Ports: "25", Action: ActionBlock},
			{ID: "f2", Name: "强制 AI", Enabled: true, Domains: []string{"geosite:openai"}, Action: "outbound:5"},
		},
		Default: []Rule{
			{ID: "d1", Name: "默认流媒体", Enabled: true, Domains: []string{"geosite:netflix"}, Action: "outbound:5"},
			{ID: "d2", Name: "默认封锁 bt", Enabled: true, Protocols: []string{"bittorrent"}, Action: ActionBlock},
		},
		DefaultAction: "outbound:5",
	}
	nodeRules := []Rule{
		{ID: "n1", Name: "我的封锁", Enabled: true, Domains: []string{"domain:bad.example"}, Action: ActionBlock},
		{ID: "n2", Name: "我的分流", Enabled: true, Domains: []string{"domain:good.example"}, Action: "outbound:5"},
	}
	for i := range in.Nodes {
		if in.Nodes[i].ID == 11 {
			in.Nodes[i].Rules, in.Nodes[i].Default = nodeRules, "outbound:5"
			in.Nodes[i].Chain = chainOut("198.51.124.2")
		}
		if in.Nodes[i].ID == 12 {
			in.Nodes[i].Rules = nodeRules
		}
	}
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	chained := rulesFor(plan, "flvxl-11")
	if len(chained) == 0 {
		t.Fatal("chained node has no rules")
	}
	last := chained[len(chained)-1]
	tag := ChainOutboundTag(*chainOut("198.51.124.2"))
	if last != ChainRuleName+"→"+tag {
		t.Fatalf("last rule of the chained node = %q, want the chain catch-all %s", last, tag)
	}
	for _, r := range chained[:len(chained)-1] {
		if !strings.HasSuffix(r, "→block") {
			t.Fatalf("non-block rule %q still covers the chained node: %v", r, chained)
		}
	}
	for _, want := range []string{"强制封锁 25→block", "我的封锁→block", "屏蔽大陆→block", "默认封锁 bt→block"} {
		if !contains(chained, want) {
			t.Fatalf("block rule %q missing for the chained node: %v", want, chained)
		}
	}
	// The unchained neighbour keeps every layer.
	other := rulesFor(plan, "flvxl-12")
	for _, want := range []string{"强制 AI→flvxo-5", "我的分流→flvxo-5", "AI→flvxo-5", "默认流媒体→flvxo-5"} {
		if !contains(other, want) {
			t.Fatalf("unchained node lost %q: %v", want, other)
		}
	}
	if contains(other, ChainRuleName+"→"+tag) {
		t.Fatal("chain rule leaked to another node")
	}
	found := false
	for _, o := range plan.Outbounds {
		if o.Tag == tag {
			found = o.Security == "reality" && o.Network == "tcp" && o.Server == "198.51.124.2"
		}
	}
	if !found {
		t.Fatalf("chain outbound %s missing or altered: %+v", tag, plan.Outbounds)
	}
	if len(rep.ChainNotes) != 0 {
		t.Fatalf("unexpected chain notes %v", rep.ChainNotes)
	}
	if got := in.EffectiveLabel(&in.Nodes[0]); got != ChainRuleName {
		t.Fatalf("label = %q", got)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// A node on the built-in default with no rules gets exactly the chain rule
// (after the default anti-abuse rules); identical external nodes share one
// outbound; an unusable chain blocks (fail closed) and is reported.
func TestChainDedupeAndFailClosed(t *testing.T) {
	in := abuseInput(map[int64]Profile{}, 0,
		NodeRef{ID: 1, Tag: "flvxl-1", Chain: chainOut("198.51.124.2")},
		NodeRef{ID: 2, Tag: "flvxl-2", Chain: chainOut("198.51.124.2")},
		NodeRef{ID: 3, Tag: "flvxl-3", ChainErr: "此服务器不支持该协议"},
		NodeRef{ID: 4, Tag: "flvxl-4"},
	)
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outbounds) != 1 {
		t.Fatalf("outbounds = %+v, want one shared chain outbound", plan.Outbounds)
	}
	tag := plan.Outbounds[0].Tag
	if !strings.HasPrefix(tag, "flvxo-x") || len(tag) > 6+32 {
		t.Fatalf("bad chain tag %q", tag)
	}
	n := len(plan.Rules)
	if plan.Rules[n-2].Outbound != tag || strings.Join(plan.Rules[n-2].InboundTags, ",") != "flvxl-1,flvxl-2" {
		t.Fatalf("chain rule = %+v", plan.Rules[n-2])
	}
	if plan.Rules[n-1].Outbound != ActionBlock || strings.Join(plan.Rules[n-1].InboundTags, ",") != "flvxl-3" || plan.Rules[n-1].Builtin != "" {
		t.Fatalf("fail-closed rule = %+v", plan.Rules[n-1])
	}
	if rep.ChainNotes[3] != "此服务器不支持该协议" || len(rep.ChainNotes) != 1 {
		t.Fatalf("notes = %v", rep.ChainNotes)
	}
	for _, r := range plan.Rules {
		if r.Builtin != "" && contains(r.InboundTags, "flvxl-4") && r.Outbound != ActionBlock {
			t.Fatalf("unexpected rule %+v", r)
		}
	}
	if got := rulesFor(plan, "flvxl-4"); len(got) != len(AbuseRules(in.AbuseOff)) {
		t.Fatalf("plain node rules = %v", got)
	}
}

// More distinct external nodes than the server's outbound room: the earliest
// nodes get theirs, the rest are blocked with a note (never direct).
func TestChainOverflowBlocks(t *testing.T) {
	var nodes []NodeRef
	for i := 1; i <= MaxPlanOutbounds+2; i++ {
		nodes = append(nodes, NodeRef{ID: int64(i), Tag: fmt.Sprintf("flvxl-%d", i), Chain: chainOut(fmt.Sprintf("198.51.124.%d", i))})
	}
	profiles := map[int64]Profile{1: {ID: 1, Name: "socks", DefaultAction: "outbound:5"}}
	in := abuseInput(profiles, 0, nodes...)
	in.Nodes = append(in.Nodes, NodeRef{ID: 100, Tag: "flvxl-100", Override: ptr(1)})
	plan, rep, err := CompileLayers(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outbounds) != MaxPlanOutbounds {
		t.Fatalf("outbounds = %d", len(plan.Outbounds))
	}
	if plan.Outbounds[0].Tag != "flvxo-5" {
		t.Fatalf("template outbound must keep its room first: %+v", plan.Outbounds[0])
	}
	// 1 template + 7 chains; nodes 8, 9, 10 blocked.
	if len(rep.ChainNotes) != 3 || rep.ChainNotes[8] == "" || rep.ChainNotes[10] == "" || rep.ChainNotes[7] != "" {
		t.Fatalf("notes = %v", rep.ChainNotes)
	}
	last := plan.Rules[len(plan.Rules)-1]
	if last.Outbound != ActionBlock || strings.Join(last.InboundTags, ",") != "flvxl-10,flvxl-8,flvxl-9" {
		t.Fatalf("overflow rule = %+v", last)
	}
}

// Without any chained node the compiler output is unchanged (the existing
// golden tests pin the bytes); a chained node never changes others' rules.
func TestChainLeavesOtherNodesIdentical(t *testing.T) {
	base := layersInput()
	plain, _, err := CompileLayers(base)
	if err != nil {
		t.Fatal(err)
	}
	withChain := layersInput()
	withChain.Nodes = append(withChain.Nodes, NodeRef{ID: 15, Tag: "flvxl-15", Chain: chainOut("198.51.124.2")})
	chained, _, err := CompileLayers(withChain)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"flvxl-11", "flvxl-12", "flvxl-13", "flvxl-14"} {
		a, b := strings.Join(rulesFor(plain, tag), "|"), strings.Join(rulesFor(chained, tag), "|")
		if a != b {
			t.Fatalf("%s changed:\n%s\n%s", tag, a, b)
		}
	}
}
