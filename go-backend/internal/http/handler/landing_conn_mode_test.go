package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func connModeNodes(payload map[string]interface{}) map[float64]map[string]interface{} {
	out := map[float64]map[string]interface{}{}
	raw, _ := json.Marshal(payload["nodes"])
	var nodes []map[string]interface{}
	_ = json.Unmarshal(raw, &nodes)
	for _, n := range nodes {
		out[n["id"].(float64)] = n
	}
	return out
}

// Native payload (plan 141, retired choice in plan 158): agents with
// landing_conn_mode >= 1 get conn_mode "inbound" on every node; a total still
// stored as 仅入站 keeps max_conn_mode until the server is saved again; an old
// agent gets today's payload byte for byte. Plan 158 user limits (quota_group
// + user_limits) go only to agents reporting landing_user_limits. Each content
// change advances the revision exactly once.
func TestNativeLandingPayloadConnMode(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0, landing_conn_mode=0 WHERE id=2", true)
	if err := e.r.DB().Create(&model.LandingNode{ID: 2, UserID: 2, LandingServerID: 1, Name: "two", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active", MaxConnections: 3}).Error; err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE landing_node SET max_connections = 100 WHERE id = 1")
	c := egressAgent(t, e, false)
	sync := func() (map[string]interface{}, []byte) {
		t.Helper()
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(c.payloads[len(c.payloads)-1])
		p := map[string]interface{}{}
		_ = json.Unmarshal(raw, &p)
		return p, raw
	}
	_, baseRaw := sync()

	// A legacy 仅入站 total + an own node mode — old agent: unchanged payload.
	e.exec("UPDATE landing_node SET conn_mode = 'total' WHERE id = 2")
	e.exec("UPDATE landing_server SET max_conn_mode = 'inbound', max_connections = 500 WHERE id = 1")
	old, oldRaw := sync()
	if strings.Contains(string(oldRaw), "conn_mode") || strings.Contains(string(oldRaw), "quota_group") || strings.Contains(string(oldRaw), "user_limits") {
		t.Fatalf("old agent got new fields: %s", oldRaw)
	}
	if old["max_connections"].(float64) != 500 || connModeNodes(old)[2]["max_connections"].(float64) != 3 {
		t.Fatalf("old agent limits: %s", oldRaw)
	}
	t.Logf("native payload, 2 nodes: before %d bytes", len(baseRaw))

	// Agent upgraded: every node counts 入站; the legacy 仅入站 total stays.
	e.exec("UPDATE node SET landing_conn_mode = 1 WHERE id = 2")
	newer, newRaw := sync()
	nodes := connModeNodes(newer)
	if nodes[1]["conn_mode"] != "inbound" || nodes[2]["conn_mode"] != "inbound" || newer["max_conn_mode"] != "inbound" {
		t.Fatalf("capable agent payload: %s", newRaw)
	}
	if newer["revision"].(float64) <= old["revision"].(float64) {
		t.Fatalf("revision not advanced: %v → %v", old["revision"], newer["revision"])
	}
	again, _ := sync()
	if again["revision"] != newer["revision"] {
		t.Fatal("identical content advanced the revision")
	}
	// The total saved as 入站+出站: max_conn_mode disappears, nodes stay 入站.
	e.exec("UPDATE landing_server SET max_conn_mode = 'total' WHERE id = 1")
	total, totalRaw := sync()
	if total["max_conn_mode"] != nil || connModeNodes(total)[1]["conn_mode"] != "inbound" || total["revision"] == again["revision"] {
		t.Fatalf("total back to 入站+出站: %s", totalRaw)
	}

	// Plan 158: a limited owner (总连接数 4) on an agent with landing_user_limits.
	e.exec("UPDATE \"user\" SET max_connections = 4, max_client_ips = 0 WHERE id = 2")
	plain, plainRaw := sync()
	if strings.Contains(string(plainRaw), "quota_group") || plain["revision"] != total["revision"] {
		t.Fatalf("agent without landing_user_limits got user limits: %s", plainRaw)
	}
	e.exec("UPDATE node SET landing_user_limits = 1 WHERE id = 2")
	ul, ulRaw := sync()
	nodes = connModeNodes(ul)
	if nodes[1]["quota_group"] != "landing-user-2" || nodes[2]["quota_group"] != "landing-user-2" || ul["revision"] == plain["revision"] {
		t.Fatalf("user-limits payload nodes: %s", ulRaw)
	}
	limits, _ := ul["user_limits"].(map[string]interface{})
	groups, _ := limits["groups"].([]interface{})
	relays, _ := limits["relay_sources"].([]interface{})
	if limits["version"].(float64) != 1 || len(groups) != 1 || groups[0].(map[string]interface{})["max_connections"].(float64) != 4 || groups[0].(map[string]interface{})["server_max_connections"].(float64) != -1 {
		t.Fatalf("user_limits: %s", ulRaw)
	}
	hasLoop := false
	for _, ip := range relays {
		hasLoop = hasLoop || ip == "127.0.0.1"
	}
	if !hasLoop {
		t.Fatalf("relay sources without loopback: %v", relays)
	}
	t.Logf("native payload, 2 nodes with user limits: %d bytes (+%d)", len(ulRaw), len(ulRaw)-len(totalRaw))
	if same, _ := sync(); same["revision"] != ul["revision"] {
		t.Fatal("identical user-limits content advanced the revision")
	}
	// A per-server 入站连接数 alone (no totals) still maps the owner's nodes.
	e.exec("UPDATE \"user\" SET max_connections = 0 WHERE id = 2")
	if err := e.r.DB().Create(&model.UserServerQuota{UserID: 2, ServerKind: "node", ServerRefID: 2, MaxInboundConnections: 6, CreatedTime: 1, UpdatedTime: 1}).Error; err != nil {
		t.Fatal(err)
	}
	capped, cappedRaw := sync()
	cg := capped["user_limits"].(map[string]interface{})["groups"].([]interface{})
	if len(cg) != 1 || cg[0].(map[string]interface{})["server_max_connections"].(float64) != 6 || cg[0].(map[string]interface{})["max_connections"].(float64) != -1 {
		t.Fatalf("per-server cap payload: %s", cappedRaw)
	}
	// Owner no longer limited: no quota_group, empty group list.
	e.exec("UPDATE user_server_quota SET max_inbound_connections = 0")
	free, freeRaw := sync()
	if strings.Contains(string(freeRaw), "quota_group") || len(free["user_limits"].(map[string]interface{})["groups"].([]interface{})) != 0 {
		t.Fatalf("unlimited owner payload: %s", freeRaw)
	}
}

// Views: counts follow the effective mode, labels and the pending hint are
// admin data, users only get the effective label.
func TestLandingViewsConnMode(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_conn_mode=0 WHERE id=2", true)
	e.exec("UPDATE landing_node SET connections = 9, inbound_connections = 4, max_connections = 100, conn_mode = 'inbound' WHERE id = 1")
	e.h.nodeCommandSender = nil
	nodeView := func(role int) map[string]any {
		t.Helper()
		out := egressCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, role)
		raw, _ := json.Marshal(out.Data)
		var list []map[string]any
		_ = json.Unmarshal(raw, &list)
		if len(list) == 0 {
			t.Fatalf("no nodes: %+v", out)
		}
		return list[0]
	}
	v := nodeView(0)
	if v["connections"].(float64) != 9 || v["connModeEffective"] != "total" || v["connModeConfigured"] != "inbound" || v["connModePending"] != true {
		t.Fatalf("old agent view: %+v", v)
	}
	e.exec("UPDATE node SET landing_conn_mode = 1 WHERE id = 2")
	v = nodeView(0)
	if v["connections"].(float64) != 4 || v["connModeEffective"] != "inbound" || v["connModePending"] != false {
		t.Fatalf("capable view: %+v", v)
	}
	servers := egressCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 0)
	raw, _ := json.Marshal(servers.Data)
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	if len(list) != 1 || list[0]["connections"].(float64) != 4 || list[0]["connectionsMode"] != "inbound" || list[0]["connModeSupported"] != true || list[0]["maxConnectionsMode"] != "total" {
		t.Fatalf("server view: %+v", list)
	}
	data := monitorFor(t, e, true)
	if data.Servers[0].ConnectionsMode != "inbound" || data.Servers[0].Connections != 4 || data.Servers[0].Nodes[0].ConnMode != "inbound" || data.Servers[0].Nodes[0].Connections != 4 {
		t.Fatalf("monitor: %+v", data.Servers[0])
	}
	e.exec("UPDATE node SET landing_conn_mode = 0 WHERE id = 2")
	data = monitorFor(t, e, true)
	if data.Servers[0].ConnectionsMode != "total" || data.Servers[0].Nodes[0].Connections != 9 {
		t.Fatalf("monitor old agent: %+v", data.Servers[0])
	}
}

func monitorFor(t *testing.T, e *flowTestEnv, admin bool) landingMonitorView {
	t.Helper()
	snap, err := e.r.LandingMonitorSnapshot(1, admin, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return buildLandingMonitorView(snap, admin, e.h.landingRateTracker(), time.Now())
}

// User node views drop the admin-only conn-mode internals.
func TestLandingUserNodeViewHidesConnModeInternals(t *testing.T) {
	v := map[string]interface{}{"connMode": "inbound", "connModeConfigured": "inbound", "connModePending": true, "inboundConnections": 3, "connModeEffective": "total", "connections": 9}
	hideLandingNodeEgressInternals(v)
	for _, k := range []string{"connMode", "connModeConfigured", "connModePending", "inboundConnections"} {
		if _, ok := v[k]; ok {
			t.Fatalf("%s leaked to users", k)
		}
	}
	if v["connModeEffective"] != "total" || v["connections"] != 9 {
		t.Fatal("label / count removed")
	}
	if repo.LandingConnInbound != "inbound" {
		t.Fatal("wire value")
	}
}
