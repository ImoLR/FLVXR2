package handler

import (
	"strconv"
	"testing"

	"go-backend/internal/landingegress"
)

// Plan 139: server and node labels follow the 全局默认出站 (prod: 「直连-强制ipv4出站」),
// for admins and users, and fall back to 直连 when the server lacks IPv4.
func TestLandingLabelsFollowGlobalDefault(t *testing.T) {
	e, _ := layersEnv(t)
	o, err := e.r.SaveLandingOutbound(&landingegress.Outbound{Name: "直连-强制ipv4出站", Type: landingegress.TypeDirect, IPVersion: "force_ipv4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.SaveLandingGlobal(&landingegress.Global{DefaultAction: "outbound:" + strconv.FormatInt(o.ID, 10)}); err != nil {
		t.Fatal(err)
	}
	check := func(want string, uid int64, role int) {
		t.Helper()
		out := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, uid, role)
		list, _ := out.Data.([]any)
		if out.Code != 0 || len(list) != 1 || list[0].(map[string]any)["egressLabel"] != want {
			t.Fatalf("server label role %d: %+v", role, out)
		}
		out = layerCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, uid, role)
		nodes, _ := out.Data.([]any)
		if out.Code != 0 || len(nodes) == 0 {
			t.Fatalf("node list: %+v", out)
		}
		for _, n := range nodes {
			if n.(map[string]any)["egressLabel"] != want {
				t.Fatalf("node label role %d: %+v", role, n)
			}
		}
	}
	check("强制 IPv4", 1, 0)
	e.exec("INSERT INTO landing_server_user (landing_server_id, user_id) VALUES (1, 2)")
	check("强制 IPv4", 2, 1)
	out := layerCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, 1, 0)
	if name := out.Data.([]any)[0].(map[string]any)["effectiveRouteProfileName"]; name != "默认 · 强制 IPv4" {
		t.Fatalf("default profile name %v", name)
	}
	// An IPv6-only server cannot force IPv4: the plan falls back to direct, so does the label.
	e.exec("UPDATE node SET egress_detected = 'v6' WHERE id = 2")
	check("直连", 1, 0)
}
