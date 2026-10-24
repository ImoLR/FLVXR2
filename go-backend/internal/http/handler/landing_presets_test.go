package handler

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingegress"
)

// Plan 140: the global switches of the default rules reach the agents via the
// normal debounced re-sync; the admin view and the node view show them.
func TestLandingBuiltinSwitchSync(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=1 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	agent := egressAgent(t, e, true)
	e.h.SetLandingLayerApplyDelay(10 * time.Millisecond)
	lastPlan := func() *landingegress.Plan {
		t.Helper()
		plan, _ := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
		return plan
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if p := lastPlan(); p == nil || len(p.Rules) != 2 {
		t.Fatalf("defaults before: %+v", p)
	}
	base := len(agent.payloads)

	if out := egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false, "bt": true}, 1); out.Code != 403 {
		t.Fatalf("normal user switched builtins: %+v", out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false}, 0); out.Code == 0 {
		t.Fatal("half request accepted")
	}
	out := egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false, "bt": true}, 0)
	if out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	b := dataMap(t, out)["builtin"].(map[string]any)
	if b["smtp"] != false || b["bt"] != true || len(b["safety"].([]any)) != len(landingegress.BlockedCIDRs) {
		t.Fatalf("builtin view %+v", b)
	}
	e.h.WaitEgressSync()
	if len(agent.payloads) != base+1 {
		t.Fatalf("pushes after one switch save: %d", len(agent.payloads)-base)
	}
	if p := lastPlan(); p == nil || len(p.Rules) != 1 || p.Rules[0].Name != "默认：屏蔽 BT" {
		t.Fatalf("plan after SMTP off: %+v", p)
	}
	// Same values again: no push.
	egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false, "bt": true}, 0)
	e.h.WaitEgressSync()
	if len(agent.payloads) != base+1 {
		t.Fatal("unchanged switches pushed again")
	}
	s, _ := e.r.GetLandingServer(1)
	view := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())
	if view["abuseDefaults"] != "active" || len(view["abuseRules"].([]string)) != 1 {
		t.Fatalf("server view %+v %+v", view["abuseDefaults"], view["abuseRules"])
	}
	n, _ := e.r.GetLandingNode(1)
	nv, err := e.h.landingNodeRulesView(n, false)
	if err != nil {
		t.Fatal(err)
	}
	if nb := nv["builtin"].(map[string]any); nb["smtp"] != false || nb["bt"] != true {
		t.Fatalf("node builtin view %+v", nb)
	}

	// Both off: no default rule, nothing to send (plan inactive).
	egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": false, "bt": false}, 0)
	e.h.WaitEgressSync()
	if p := lastPlan(); p != nil && len(p.Rules) != 0 {
		t.Fatalf("plan with both off: %+v", p)
	}
	s, _ = e.r.GetLandingServer(1)
	if got := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())["abuseDefaults"]; got != "global_off" {
		t.Fatalf("server state %v", got)
	}
	// Back on: today's plan again.
	egressCall(t, e.h, "/api/v1/landing/egress/builtin/save", map[string]any{"smtp": true, "bt": true}, 0)
	e.h.WaitEgressSync()
	if p := lastPlan(); p == nil || len(p.Rules) != 2 {
		t.Fatalf("plan with both on again: %+v", p)
	}
}

// Plan 149: the seeded 全局·强制 rules are ENABLED on a fresh install: the
// global view lists them, the pushed plan carries the three 大陆 rules (禁止
// 大陆直连 only on SS nodes), and the owner sees them as locked rows.
func TestLandingSeededForcedSent(t *testing.T) {
	e := newSeededFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=2 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	agent := egressAgent(t, e, true)
	view, err := e.h.landingGlobalView()
	if err != nil {
		t.Fatal(err)
	}
	forced := view["forced"].([]landingegress.Rule)
	if len(forced) != 3 || !forced[0].Enabled || !forced[2].Enabled || forced[2].ID != landingegress.SeedCNDirect {
		t.Fatalf("seeded forced %+v", forced)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	p, _ := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	if p == nil || len(p.Rules) != 5 {
		t.Fatalf("plan with the seeds: %+v", p)
	}
	names := []string{}
	for _, r := range p.Rules {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "默认：屏蔽 SMTP 25,默认：屏蔽 BT,屏蔽大陆 IP,屏蔽大陆网站,禁止大陆直连" {
		t.Fatalf("rules %v", names)
	}
	if len(p.ExemptSources) == 0 {
		t.Fatal("source rule without exempt list")
	}
	n, _ := e.r.GetLandingNode(1)
	nv, err := e.h.landingNodeRulesView(n, false)
	if err != nil {
		t.Fatal(err)
	}
	if g := nv["global"].(map[string]any); len(g["forced"].([]map[string]any)) != 3 {
		t.Fatalf("owner view of the seeds: %+v", g["forced"])
	}
}
