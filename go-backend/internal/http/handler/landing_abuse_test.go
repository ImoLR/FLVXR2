package handler

import (
	"strings"
	"testing"

	"go-backend/internal/landingegress"
)

// TestLandingAbuseDefaultsNativeGate: the default anti-abuse rules (plan 127)
// reach a native agent only when its Core can take plan rules, so a Core still
// running a pre-plan-107 config never gets a plan that would pause its nodes.
func TestLandingAbuseDefaultsNativeGate(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1 WHERE id=2", true)
	agent := egressAgent(t, e, false)

	// Not ready, nothing confirmed: today's payload, no revision churn.
	before, _ := e.r.GetLandingServer(1)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := agent.payloads[len(agent.payloads)-1]["egress"]; ok {
		t.Fatal("defaults sent to a Core without the routing API")
	}
	if after, _ := e.r.GetLandingServer(1); after.Revision != before.Revision {
		t.Fatalf("revision changed %d→%d", before.Revision, after.Revision)
	}
	s, _ := e.r.GetLandingServer(1)
	if got := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())["abuseDefaults"]; got != "restart" {
		t.Fatalf("abuse state %v, want restart", got)
	}

	// The admin confirms the one-time restart without changing the profile.
	out := egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, 0, true), 0)
	if out.Code != 0 {
		t.Fatalf("confirm-only save: %+v", out)
	}
	plan, ok := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	if !ok || !plan.AllowCoreRestart || len(plan.Rules) != 2 || plan.Rules[0].Builtin != "" {
		t.Fatalf("confirmed restart plan: %+v", agent.payloads[len(agent.payloads)-1]["egress"])
	}

	// Ready agent (Core restarted), no confirmation pending: defaults stay.
	e.exec("UPDATE landing_server SET egress_restart_ok=?, egress_ready=? WHERE id=1", false, true)
	e.exec("UPDATE node SET landing_guard=1 WHERE id=2")
	agent = egressAgent(t, e, true)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	plan, ok = agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	if !ok || plan.AllowCoreRestart || len(plan.Rules) != 2 || plan.Rules[1].Builtin != landingegress.BuiltinAbuse || strings.Join(plan.Rules[1].InboundTags, ",") != "flvxl-1" {
		t.Fatalf("ready plan: %+v", agent.payloads[len(agent.payloads)-1]["egress"])
	}
	s, _ = e.r.GetLandingServer(1)
	if got := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())["abuseDefaults"]; got != "active" {
		t.Fatalf("abuse state %v, want active", got)
	}
	// Re-sync with nothing changed: same revision (idempotent compile).
	rev := s.Revision
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if after, _ := e.r.GetLandingServer(1); after.Revision != rev {
		t.Fatalf("idempotent sync bumped revision %d→%d", rev, after.Revision)
	}
}

// TestLandingAbuseDefaultsProfileSwitch: the per-profile switch is admin-only,
// defaults to on, survives saves from clients that do not send it, and turns
// the defaults off for the profile's nodes.
func TestLandingAbuseDefaultsProfileSwitch(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	agent := egressAgent(t, e, true)

	if out := egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"name": "p", "abuseDefaults": false}, 1); out.Code != 403 {
		t.Fatalf("normal user saved a profile: %+v", out)
	}
	out := egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"name": "p", "defaultAction": "direct", "rules": []any{map[string]any{"name": "x", "enabled": true, "ports": "8081", "action": "block"}}}, 0)
	if out.Code != 0 || dataMap(t, out)["abuseDefaults"] != true {
		t.Fatalf("new profile must default to on: %+v", out)
	}
	id := dataMap(t, out)["id"].(float64)
	out = egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"id": id, "name": "p", "defaultAction": "direct", "abuseDefaults": false, "rules": []any{map[string]any{"name": "x", "enabled": true, "ports": "8081", "action": "block"}}}, 0)
	if out.Code != 0 || dataMap(t, out)["abuseDefaults"] != false {
		t.Fatalf("switch off: %+v", out)
	}
	// A save without the field keeps the stored value.
	out = egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"id": id, "name": "p2", "defaultAction": "direct", "rules": []any{map[string]any{"name": "x", "enabled": true, "ports": "8081", "action": "block"}}}, 0)
	if out.Code != 0 || dataMap(t, out)["abuseDefaults"] != false {
		t.Fatalf("legacy save reset the switch: %+v", out)
	}
	overview := dataMap(t, egressCall(t, e.h, "/api/v1/landing/egress/overview", map[string]any{}, 0))
	if p := overview["profiles"].([]any)[0].(map[string]any); p["abuseDefaults"] != false {
		t.Fatalf("overview: %+v", p)
	}
	// Assign it: only the profile's own rule is sent, no defaults.
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, int64(id), false), 0); out.Code != 0 {
		t.Fatalf("assign: %+v", out)
	}
	plan, ok := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	if !ok || len(plan.Rules) != 1 || plan.Rules[0].Port != "8081" {
		t.Fatalf("switched-off plan: %+v", agent.payloads[len(agent.payloads)-1]["egress"])
	}
	s, _ := e.r.GetLandingServer(1)
	if got := e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog())["abuseDefaults"]; got != "off" {
		t.Fatalf("abuse state %v, want off", got)
	}
	e.h.egressSyncWG.Wait()
}
