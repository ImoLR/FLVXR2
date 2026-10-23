package handler

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingegress"
)

// Plan 143: the two new 内置 rows (发信端口, Telnet/SMB) — switches, one
// re-sync per change, views with off reasons, older clients keep them.
func TestLandingBuiltinNewRows(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=2 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	agent := egressAgent(t, e, true)
	e.h.SetLandingLayerApplyDelay(10 * time.Millisecond)
	lastPlan := func() *landingegress.Plan {
		t.Helper()
		plan, _ := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
		return plan
	}
	names := func(p *landingegress.Plan) string {
		var out []string
		for _, r := range p.Rules {
			out = append(out, r.Name)
		}
		return strings.Join(out, "|")
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	// Fresh install: the new rows are off.
	if p := lastPlan(); p == nil || names(p) != "默认：屏蔽 SMTP 25|默认：屏蔽 BT" {
		t.Fatalf("fresh plan %+v", p)
	}
	view, _ := e.h.landingGlobalView()
	if b := view["builtin"].(map[string]any); b["mail"] != false || b["telnet"] != false || b["smtp"] != true || b["bt"] != true {
		t.Fatalf("fresh admin view %+v", b)
	}
	base := len(agent.payloads)
	out := egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": true, "bt": true, "mail": true, "telnet": true}, 0)
	if out.Code != 0 {
		t.Fatalf("save %+v", out)
	}
	if b := dataMap(t, out)["builtin"].(map[string]any); b["mail"] != true || b["telnet"] != true {
		t.Fatalf("saved view %+v", b)
	}
	e.h.WaitEgressSync()
	if len(agent.payloads) != base+1 {
		t.Fatalf("pushes after one save: %d", len(agent.payloads)-base)
	}
	if p := lastPlan(); names(p) != "默认：屏蔽 SMTP 25|默认：屏蔽发信端口|默认：屏蔽 Telnet/SMB|默认：屏蔽 BT" {
		t.Fatalf("plan with 4 rows: %s", names(p))
	}
	// An older client (smtp/bt only) keeps the new rows as they are.
	egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false, "bt": true}, 0)
	e.h.WaitEgressSync()
	if p := lastPlan(); names(p) != "默认：屏蔽发信端口|默认：屏蔽 Telnet/SMB|默认：屏蔽 BT" {
		t.Fatalf("plan after old-client save: %s", names(p))
	}
	s, _ := e.r.GetLandingServer(1)
	if got := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())["abuseRules"].([]string); strings.Join(got, ",") != "发信端口 465/587/2525,Telnet/SMB,BT" {
		t.Fatalf("server abuseRules %v", got)
	}
	// Node view: in force per row, with the reason for the off ones.
	n, _ := e.r.GetLandingNode(1)
	nv, err := e.h.landingNodeRulesView(n, false)
	if err != nil {
		t.Fatal(err)
	}
	nb := nv["builtin"].(map[string]any)
	reasons := nb["offReasons"].(map[string]string)
	if nb["smtp"] != false || nb["mail"] != true || nb["telnet"] != true || nb["bt"] != true || reasons["smtp"] != "global" || len(reasons) != 1 {
		t.Fatalf("node view %+v", nb)
	}
	// The node's profile switches the defaults off: every row off, reason profile.
	off := false
	prof, _, err := e.r.SaveLandingRouteProfile(&landingegress.Profile{Name: "无防滥用", DefaultAction: landingegress.ActionDirect, AbuseDefaults: &off, Rules: []landingegress.Rule{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.SetLandingNodeRouteProfile(1, &prof.ID); err != nil {
		t.Fatal(err)
	}
	n, _ = e.r.GetLandingNode(1)
	nv, _ = e.h.landingNodeRulesView(n, false)
	nb = nv["builtin"].(map[string]any)
	reasons = nb["offReasons"].(map[string]string)
	if nb["mail"] != false || reasons["mail"] != "profile" || reasons["smtp"] != "global" {
		t.Fatalf("profile-off node view %+v", nb)
	}
}
