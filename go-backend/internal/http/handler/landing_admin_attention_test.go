package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// TestLandingServerListAdminAttention (plan 218): the admin server list carries
// each server's 禁止端口扫描 state (same as the 内置 per-server row) and the
// count of nodes withheld because the routing plan did not take effect; users
// get neither.
func TestLandingServerListAdminAttention(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2}).Error; err != nil {
		t.Fatal(err)
	}
	e.h.nodeCommandSender = nil
	first := func() map[string]any {
		t.Helper()
		out := egressCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 0)
		raw, _ := json.Marshal(out.Data)
		var list []map[string]any
		_ = json.Unmarshal(raw, &list)
		if len(list) != 1 {
			t.Fatalf("servers: %+v", out)
		}
		return list[0]
	}
	scan := func(v map[string]any) string {
		t.Helper()
		sg, _ := v["scanGuard"].(map[string]any)
		state, _ := sg["state"].(string)
		if state == "" || sg["label"] == "" {
			t.Fatalf("scanGuard %v", v["scanGuard"])
		}
		return state
	}
	// Old agent → 需要升级节点; current agent with an old Core → core.
	if got := scan(first()); got != "agent" {
		t.Fatalf("old agent: %q", got)
	}
	e.exec("UPDATE node SET landing_scan_guard = ? WHERE id = 2", repo.LandingScanGuardVersion)
	e.exec("UPDATE landing_server SET scan_state = 'core_update' WHERE id = 1")
	if got := scan(first()); got != "core" {
		t.Fatalf("core_update: %q", got)
	}
	e.exec("UPDATE landing_server SET scan_state = 'error' WHERE id = 1")
	if got := scan(first()); got != "error" {
		t.Fatalf("error: %q", got)
	}
	e.exec("UPDATE landing_server SET scan_state = 'on' WHERE id = 1")
	if got := scan(first()); got != "on" {
		t.Fatalf("on: %q", got)
	}
	// Same state as the 内置 per-server row.
	view, err := e.h.landingScanAdminView()
	if err != nil {
		t.Fatal(err)
	}
	if rows := view["servers"].([]map[string]any); len(rows) != 1 || rows[0]["state"] != "on" {
		t.Fatalf("内置 rows: %v", view["servers"])
	}

	// Withheld nodes come from the stored warning (same format as the producer).
	egress := func() map[string]any { t.Helper(); m, _ := first()["egress"].(map[string]any); return m }
	if n := egress()["withheldNodes"]; n != float64(0) {
		t.Fatalf("withheld without warnings: %v", n)
	}
	warnings, _ := json.Marshal([]string{"WARP 设备正在注册", strings.Replace(landingEgressWithheldFormat, "%d", "2", 1)})
	e.exec("UPDATE landing_server SET egress_warnings = ? WHERE id = 1", string(warnings))
	if n := egress()["withheldNodes"]; n != float64(2) {
		t.Fatalf("withheld: %v", n)
	}

	// Users see neither field.
	user := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 2, 1)
	raw, _ := json.Marshal(user.Data)
	if !strings.Contains(string(raw), `"id":1`) || strings.Contains(string(raw), "scanGuard") || strings.Contains(string(raw), "withheldNodes") {
		t.Fatalf("user view: %s", raw)
	}
}
