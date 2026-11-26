package handler

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/repo"
)

// Plan 316: 屏蔽本机厂商官网 — assign a vendor → the server's plan gets the
// vendor rule first (only that vendor's names, v2 agent = abuse marker, v3 =
// vendor marker); the switch removes it; views show it; a used vendor
// cannot be deleted.
func TestLandingVendorBlock(t *testing.T) {
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
	before := names(lastPlan())
	if before != "默认：屏蔽 SMTP 25|默认：屏蔽 BT" {
		t.Fatalf("fresh plan %s", before)
	}
	// Unassigned: admin row lists the server with a suggestion, node row ✗ unset.
	view, err := e.h.landingGlobalView()
	if err != nil {
		t.Fatal(err)
	}
	vv := view["builtin"].(map[string]any)["vendor"].(map[string]any)
	if vv["on"] != true || vv["unset"].(int) != 1 || len(vv["vendors"].([]landingegress.Vendor)) != len(landingegress.SeedVendors()) {
		t.Fatalf("admin vendor view %+v", vv)
	}
	n, _ := e.r.GetLandingNode(1)
	nv, _ := e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != false || nb["offReason"] != "unset" {
		t.Fatalf("node view unset %+v", nb)
	}
	// Assign DMIT.
	out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/assign", map[string]any{"serverId": 1, "vendor": "dmit"}, 0)
	if out.Code != 0 {
		t.Fatalf("assign %+v", out)
	}
	e.h.WaitEgressSync()
	p := lastPlan()
	if names(p) != "内置：屏蔽本机厂商官网（DMIT）|"+before {
		t.Fatalf("plan after assign %s", names(p))
	}
	r0 := p.Rules[0]
	if strings.Join(r0.Domain, ",") != "domain:dmit.io,domain:dmithost.net" || r0.Outbound != "block" || r0.Builtin != landingegress.BuiltinAbuse {
		t.Fatalf("vendor rule %+v", r0)
	}
	nv, _ = e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != true || nb["name"] != "DMIT" || nb["sni"] != false {
		t.Fatalf("node view on %+v", nb)
	}
	// A plan-316 agent gets the vendor marker (SNI / Host match).
	e.exec("UPDATE node SET landing_guard=3 WHERE id=2")
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if got := lastPlan().Rules[0].Builtin; got != landingegress.BuiltinVendor {
		t.Fatalf("v3 marker %q", got)
	}
	// The vendor in use cannot be deleted.
	vs, _ := e.r.GetLandingVendorSettings()
	var rest []landingegress.Vendor
	for _, v := range vs.Vendors {
		if v.Key != "dmit" {
			rest = append(rest, v)
		}
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"vendors": rest}, 0); out.Code == 0 {
		t.Fatal("deleting a used vendor was accepted")
	}
	// Edit the vendor's names: the plan follows.
	for i := range vs.Vendors {
		if vs.Vendors[i].Key == "dmit" {
			vs.Vendors[i].Domains = []string{"dmit.io"}
		}
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"vendors": vs.Vendors}, 0); out.Code != 0 {
		t.Fatalf("save list %+v", out)
	}
	e.h.WaitEgressSync()
	if got := strings.Join(lastPlan().Rules[0].Domain, ","); got != "domain:dmit.io" {
		t.Fatalf("edited names %s", got)
	}
	// Global switch off: the rule is gone, the rest unchanged.
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"on": false}, 0); out.Code != 0 {
		t.Fatalf("switch %+v", out)
	}
	e.h.WaitEgressSync()
	if names(lastPlan()) != before {
		t.Fatalf("plan with switch off %s", names(lastPlan()))
	}
	nv, _ = e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != false || nb["offReason"] != "global" {
		t.Fatalf("node view off %+v", nb)
	}
	// Server card fields (admin).
	s, _ := e.r.GetLandingServer(1)
	sv := e.h.landingServerView(*s)
	if sv["vendor"] != "dmit" || sv["vendorName"] != "DMIT" || sv["vendorOn"] != false {
		t.Fatalf("server view %+v %+v %+v", sv["vendor"], sv["vendorName"], sv["vendorOn"])
	}
	if uv := e.h.landingServerUserView(*s, e.h.landingEgressCatalog(), &repo.LandingUserNodeCounts{}); uv["vendor"] != nil {
		t.Fatal("user server view carries the vendor key")
	}
	// Unknown key refused.
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/assign", map[string]any{"serverId": 1, "vendor": "nope"}, 0); out.Code == 0 {
		t.Fatal("unknown vendor accepted")
	}
}
