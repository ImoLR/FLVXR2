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

// Plan 141 native payload: 仅入站 nodes carry conn_mode only for agents with
// landing_conn_mode >= 1; an old agent gets today's payload byte for byte.
// The 仅入站 signature advances the revision exactly when the content changes.
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
	base, baseRaw := sync()
	baseRev := base["revision"]

	// Configure 仅入站 (node 2 own, server total) — old agent: unchanged payload.
	e.exec("UPDATE landing_node SET conn_mode = 'inbound' WHERE id = 2")
	e.exec("UPDATE landing_server SET max_conn_mode = 'inbound', max_connections = 500 WHERE id = 1")
	old, oldRaw := sync()
	if strings.Contains(string(oldRaw), "conn_mode") {
		t.Fatalf("old agent got conn_mode: %s", oldRaw)
	}
	if old["max_connections"].(float64) != 500 || connModeNodes(old)[2]["max_connections"].(float64) != 3 {
		t.Fatalf("old agent limits: %s", oldRaw)
	}
	t.Logf("native payload, 2 nodes: before %d bytes", len(baseRaw))

	// Agent upgraded: conn_mode for node 2 + max_conn_mode, revision bumped.
	e.exec("UPDATE node SET landing_conn_mode = 1 WHERE id = 2")
	newer, newRaw := sync()
	nodes := connModeNodes(newer)
	if nodes[2]["conn_mode"] != "inbound" || nodes[1]["conn_mode"] != nil || newer["max_conn_mode"] != "inbound" {
		t.Fatalf("capable agent payload: %s", newRaw)
	}
	if newer["revision"].(float64) <= old["revision"].(float64) {
		t.Fatalf("revision not advanced: %v → %v", old["revision"], newer["revision"])
	}
	t.Logf("native payload, 2 nodes, 1 仅入站 + 仅入站 total: %d bytes (+%d vs old agent)", len(newRaw), len(newRaw)-len(oldRaw))
	again, _ := sync()
	if again["revision"] != newer["revision"] {
		t.Fatal("identical content advanced the revision")
	}
	// Server follows: node 1 (NULL) follows a 仅入站 server.
	e.exec("UPDATE landing_server SET conn_mode = 'inbound' WHERE id = 1")
	follow, _ := sync()
	if connModeNodes(follow)[1]["conn_mode"] != "inbound" || follow["revision"] == again["revision"] {
		t.Fatalf("following node: %v rev %v", connModeNodes(follow)[1], follow["revision"])
	}
	// Back to all total: payload equals the pre-141 shape again (modulo revision).
	e.exec("UPDATE landing_server SET conn_mode = 'total', max_conn_mode = 'total' WHERE id = 1")
	e.exec("UPDATE landing_node SET conn_mode = NULL")
	back, backRaw := sync()
	if strings.Contains(string(backRaw), "conn_mode") || back["revision"] == follow["revision"] {
		t.Fatalf("back to total: %s", backRaw)
	}
	_ = baseRev
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
