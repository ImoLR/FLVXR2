package landingegress

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Plan 143: the 防滥用 group in 内置 (SMTP 25, 发信端口, Telnet/SMB, BT) and the
// one-time move of the plan-140 全局·强制 seeds.

// The zero switches keep the pre-143 output (SMTP + BT only); the two new
// rows are emitted only when switched on, with the seed matchers, in 内置
// order, over the same scope as SMTP / BT (profile toggle wins, chain copies
// included for builtin agents).
func TestAbuseNewRows(t *testing.T) {
	in := layersInput()
	plan, _ := compileOK(t, in)
	for _, n := range []string{"默认：屏蔽发信端口", "默认：屏蔽 Telnet/SMB"} {
		if _, ok := findRule(plan, n); ok {
			t.Fatalf("%s present with the zero switches", n)
		}
	}
	in.AbuseOff = AbuseSwitches{MailOn: true, TelnetSMBOn: true}
	plan, _ = compileOK(t, in)
	if got := ruleNames(plan); !strings.HasPrefix(got, "默认：屏蔽 SMTP 25|默认：屏蔽发信端口|默认：屏蔽 Telnet/SMB|默认：屏蔽 BT|") {
		t.Fatalf("order %s", got)
	}
	mail, _ := findRule(plan, "默认：屏蔽发信端口")
	telnet, _ := findRule(plan, "默认：屏蔽 Telnet/SMB")
	if mail.Port != "465,587,2525" || mail.Network != "tcp" || mail.Outbound != ActionBlock || mail.Builtin != BuiltinAbuse {
		t.Fatalf("mail %+v", mail)
	}
	if telnet.Port != "23,135-139,445" || telnet.Network != "" || telnet.Outbound != ActionBlock {
		t.Fatalf("telnet must cover TCP+UDP: %+v", telnet)
	}
	// Node 13 (profile 2, defaults off) excluded; the chain copy included.
	for _, r := range []PlanRule{mail, telnet} {
		if tags := strings.Join(r.InboundTags, ","); tags != "flvxl-1099511627790,flvxl-11,flvxl-12,flvxl-14" {
			t.Fatalf("%s tags %s", r.Name, tags)
		}
	}
	// Only the new rows on, SMTP / BT off.
	in.AbuseOff = AbuseSwitches{SMTPOff: true, BTOff: true, TelnetSMBOn: true}
	plan, _ = compileOK(t, in)
	if got := ruleNames(plan); !strings.HasPrefix(got, "默认：屏蔽 Telnet/SMB|") || strings.Contains(got, "SMTP") || strings.Contains(got, "发信") {
		t.Fatalf("only telnet: %s", got)
	}
	// Shared server (mmwx Custom): no builtin marker, node tags only.
	shared := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 2, Tag: "flvxs-2"})
	shared.AbuseOff = AbuseSwitches{MailOn: true, TelnetSMBOn: true}
	plan, _ = compileOK(t, shared)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽发信端口|默认：屏蔽 Telnet/SMB|默认：屏蔽 BT" {
		t.Fatalf("shared %s", got)
	}
	for _, r := range plan.Rules {
		if r.Builtin != "" || strings.Join(r.InboundTags, ",") != "flvxs-2" {
			t.Fatalf("shared rule %+v", r)
		}
	}
	// A profile rule repeating a new row (the 「防滥用」 preset) is merged into
	// it while the row is on, kept while it is off.
	profiles := map[int64]Profile{1: {ID: 1, Name: "p", DefaultAction: ActionDirect, Rules: []Rule{
		{ID: "m", Name: "我的发信", Enabled: true, Ports: "465,587,2525", Network: "tcp", Action: ActionBlock},
		{ID: "t", Name: "我的 SMB", Enabled: true, Ports: "23,135-139,445", Action: ActionBlock},
	}}}
	dup := abuseInput(profiles, 1, NodeRef{ID: 1, Tag: "flvxl-1"})
	plan, _ = compileOK(t, dup)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽 BT|我的发信|我的 SMB" {
		t.Fatalf("rows off: %s", got)
	}
	dup.AbuseOff = AbuseSwitches{MailOn: true, TelnetSMBOn: true}
	plan, _ = compileOK(t, dup)
	if got := ruleNames(plan); got != "默认：屏蔽 SMTP 25|默认：屏蔽发信端口|默认：屏蔽 Telnet/SMB|默认：屏蔽 BT" {
		t.Fatalf("rows on: %s", got)
	}
	if AbuseDuplicateOf(profiles[1].Rules[0]) != AbuseMail || AbuseDuplicateOf(profiles[1].Rules[1]) != AbuseTelnetSMB {
		t.Fatal("AbuseDuplicateOf keys")
	}
}

// The plan-143 rows use exactly the matchers of the plan-140 seeds and of
// the 「防滥用」 library preset.
func TestAbuseRowsMatchSeedsAndPreset(t *testing.T) {
	rows := map[string]PlanRule{}
	for i, r := range AbuseDefaultRules() {
		rows[AbuseKeys[i]] = r
	}
	for key, seed := range AbuseSeedRules() {
		r := rows[key]
		r.Name, r.Outbound = "", ""
		want := toPlanRule(seed)
		want.Name = ""
		if ruleKey(r) != ruleKey(want) || seed.Action != ActionBlock {
			t.Fatalf("%s: row %s seed %s", key, ruleKey(r), ruleKey(want))
		}
	}
	for _, p := range SeedPresets() {
		if p.SeedKey != "anti-abuse" {
			continue
		}
		for _, r := range p.Rules {
			if AbuseDuplicateOf(r) == "" {
				t.Fatalf("preset rule %s is not a 内置 row", r.Name)
			}
		}
		return
	}
	t.Fatal("anti-abuse preset missing")
}

func TestMoveAbuseSeeds(t *testing.T) {
	seeds := AbuseSeedRules()
	mail, telnet := seeds[AbuseMail], seeds[AbuseTelnetSMB]
	cn := ForcedSeedRules()
	on := func(r Rule) Rule { r.Enabled = true; return r }
	renamed := on(mail)
	renamed.ID, renamed.Name, renamed.Preset = "abc123", "我的发信封锁", ""
	edited := on(telnet)
	edited.Ports = "23,445"
	other := Rule{ID: "x", Name: "x", Enabled: true, Ports: "465,587,2525", Network: "tcp", Action: ActionDirect}

	cases := []struct {
		name        string
		in          []Rule
		rest        []string
		foundM, onM bool
		foundT, onT bool
		kept        []string
	}{
		{"prod: all enabled", []Rule{on(mail), on(telnet), on(cn[0]), on(cn[1])}, []string{"seed-cn-ip", "seed-cn-site"}, true, true, true, true, nil},
		{"seeds disabled", []Rule{mail, telnet, cn[0]}, []string{"seed-cn-ip"}, true, false, true, false, nil},
		{"seeds deleted", []Rule{cn[0], cn[1]}, []string{"seed-cn-ip", "seed-cn-site"}, false, false, false, false, nil},
		{"renamed, other id", []Rule{renamed, cn[0]}, []string{"seed-cn-ip"}, true, true, false, false, nil},
		{"edited matchers stay", []Rule{edited, cn[0]}, []string{"seed-telnet-smb", "seed-cn-ip"}, false, false, false, false, []string{"seed-telnet-smb"}},
		{"duplicates: any enabled", []Rule{mail, renamed}, []string{}, true, true, false, false, nil},
		{"direct action stays", []Rule{other}, []string{"x"}, false, false, false, false, nil},
		{"empty", nil, []string{}, false, false, false, false, nil},
	}
	for _, c := range cases {
		rest, mv := MoveAbuseSeeds(c.in)
		ids := []string{}
		for _, r := range rest {
			ids = append(ids, r.ID)
		}
		if fmt.Sprint(ids) != fmt.Sprint(c.rest) || mv.Found[AbuseMail] != c.foundM || mv.On[AbuseMail] != c.onM ||
			mv.Found[AbuseTelnetSMB] != c.foundT || mv.On[AbuseTelnetSMB] != c.onT || fmt.Sprint(mv.Kept) != fmt.Sprint(c.kept) {
			t.Fatalf("%s: rest %v move %+v", c.name, ids, mv)
		}
		// Idempotent: a second pass finds nothing more.
		again, mv2 := MoveAbuseSeeds(rest)
		if len(again) != len(rest) || len(mv2.Found) != 0 {
			t.Fatalf("%s: second pass moved %+v", c.name, mv2)
		}
	}
}

// blockedSet: every (inbound tag, matchers) the plan blocks, order-free.
func blockedSet(p *Plan) []string {
	set := map[string]bool{}
	for _, r := range p.Rules {
		if r.Outbound != ActionBlock {
			continue
		}
		k := ruleKey(PlanRule{Domain: r.Domain, IP: r.IP, Port: r.Port, SourceIP: r.SourceIP, SourcePort: r.SourcePort, Network: r.Network, Protocol: r.Protocol})
		for _, tag := range r.InboundTags {
			set[tag+" "+k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Behaviour equivalence of the move on every server shape: prod-shaped
// global (all four plan-140 seeds enabled), every node on the built-in
// default. Before: 内置 SMTP/BT + 强制 seeds. After: 内置 4 rows + 强制 大陆.
// The blocked set per inbound tag is identical; only the position changes
// (the moved rules now come right after SMTP 25, still ahead of every
// non-block rule).
func TestAbuseMoveEquivalence(t *testing.T) {
	seeds := AbuseSeedRules()
	forced := []Rule{seeds[AbuseMail], seeds[AbuseTelnetSMB]}
	forced = append(forced, ForcedSeedRules()...)
	for i := range forced {
		forced[i].Enabled = true
	}
	chain := "flvxl-1099511627790"
	type shape struct {
		in          CompileInput
		chainGained bool // agent < LayersVersion: 强制 never covered chain copies, 内置 does
	}
	mk := func(builtin, markers bool, chainTags []string, nodes ...NodeRef) CompileInput {
		in := abuseInput(map[int64]Profile{}, 0, nodes...)
		in.Builtin, in.Markers, in.ChainTags = builtin, markers, chainTags
		in.Layers, in.NodeRules = true, true
		in.Outbounds = map[int64]Outbound{}
		return in
	}
	shapes := map[string]shape{
		"native guard2 + chain": {in: mk(true, true, []string{chain}, NodeRef{ID: 1, Tag: "flvxl-1"}, NodeRef{ID: 2, Tag: "flvxl-2", Rules: []Rule{{ID: "u", Name: "用户直连", Enabled: true, Ports: "587", Action: ActionDirect}}})},
		"chain-only guard2":     {in: mk(true, true, []string{chain})},
		"native guard1 + chain": {in: mk(true, false, []string{chain}, NodeRef{ID: 1, Tag: "flvxl-1"}), chainGained: true},
		"native guard0":         {in: mk(false, false, nil, NodeRef{ID: 1, Tag: "flvxl-1"})},
		"shared":                {in: mk(false, false, nil, NodeRef{ID: 3, Tag: "flvxs-3"}, NodeRef{ID: 4, Tag: "flvxs-4"})},
	}
	for name, sh := range shapes {
		before := sh.in
		before.Global = &Global{Forced: forced, Default: []Rule{}, DefaultAction: ActionDirect}
		rest, mv := MoveAbuseSeeds(forced)
		after := sh.in
		after.Global = &Global{Forced: rest, Default: []Rule{}, DefaultAction: ActionDirect}
		after.AbuseOff = AbuseSwitches{MailOn: mv.On[AbuseMail], TelnetSMBOn: mv.On[AbuseTelnetSMB]}
		pb, _ := compileOK(t, before)
		pa, _ := compileOK(t, after)
		b, a := blockedSet(pb), blockedSet(pa)
		if sh.chainGained {
			gained := []string{}
			have := map[string]bool{}
			for _, k := range b {
				have[k] = true
			}
			for _, k := range a {
				if !have[k] {
					gained = append(gained, k)
				}
			}
			if len(a) != len(b)+2 || len(gained) != 2 || !strings.HasPrefix(gained[0], chain+" ") || !strings.HasPrefix(gained[1], chain+" ") {
				t.Fatalf("%s: expected only the chain copy to gain the 2 rows:\nbefore %v\nafter %v", name, b, a)
			}
			continue
		}
		if strings.Join(a, "\n") != strings.Join(b, "\n") {
			t.Fatalf("%s: blocked set changed:\nbefore %v\nafter %v", name, b, a)
		}
		// The user's 587 direct rule (node 2) still loses to the block.
		if name == "native guard2 + chain" {
			idxBlock, idxUser := -1, -1
			for i, r := range pa.Rules {
				switch r.Name {
				case "默认：屏蔽发信端口":
					idxBlock = i
				case "用户直连":
					idxUser = i
				}
			}
			if idxBlock < 0 || idxUser < 0 || idxBlock > idxUser {
				t.Fatalf("order: %s", ruleNames(pa))
			}
		}
	}
}
