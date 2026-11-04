package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 211, native payload: an agent with landing_conn_mode 1 gets a node's
// 节点总连接数 the old way (max_connections without conn_mode) when the node
// has no 入站 limit, and the 入站 limit alone otherwise (total pending); an
// agent with landing_conn_mode 2 gets both fields; nodes without a total keep
// today's item. Every change advances the revision once.
func TestNativeLandingPayloadNodeTotal(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0, landing_conn_mode=1 WHERE id=2", true)
	if err := e.r.DB().Create(&model.LandingNode{ID: 2, UserID: 2, LandingServerID: 1, Name: "two", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active", MaxConnections: 3}).Error; err != nil {
		t.Fatal(err)
	}
	c := egressAgent(t, e, false)
	sync := func() (map[string]interface{}, string) {
		t.Helper()
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(c.payloads[len(c.payloads)-1])
		p := map[string]interface{}{}
		_ = json.Unmarshal(raw, &p)
		return p, string(raw)
	}
	base, baseRaw := sync()
	if strings.Contains(baseRaw, "max_total_connections") {
		t.Fatalf("no totals yet, payload carries the key: %s", baseRaw)
	}

	e.exec("UPDATE landing_node SET max_total_connections = 400")
	v1, v1Raw := sync()
	nodes := connModeNodes(v1)
	if nodes[1]["max_connections"].(float64) != 400 || nodes[1]["conn_mode"] != nil || nodes[1]["max_total_connections"] != nil {
		t.Fatalf("v1 agent, total only: %s", v1Raw)
	}
	if nodes[2]["max_connections"].(float64) != 3 || nodes[2]["conn_mode"] != "inbound" || nodes[2]["max_total_connections"] != nil {
		t.Fatalf("v1 agent, both (total pending): %s", v1Raw)
	}
	if v1["revision"].(float64) <= base["revision"].(float64) {
		t.Fatal("revision not advanced by the totals")
	}
	if same, _ := sync(); same["revision"] != v1["revision"] {
		t.Fatal("identical content advanced the revision")
	}
	view := e.h.landingNodeView(model.LandingNode{ID: 2, UserID: 2, LandingServerID: 1, Name: "two", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active", MaxConnections: 3, MaxTotalConnections: 400})
	if view["nodeTotalPending"] != true {
		t.Fatalf("pending not shown: %v", view["nodeTotalPending"])
	}

	// Agent OTA → landing_conn_mode 2: both limits, revision advances.
	e.exec("UPDATE node SET landing_conn_mode = 2 WHERE id = 2")
	v2, v2Raw := sync()
	nodes = connModeNodes(v2)
	if nodes[1]["max_connections"].(float64) != 0 || nodes[1]["conn_mode"] != "inbound" || nodes[1]["max_total_connections"].(float64) != 400 {
		t.Fatalf("v2 agent, total only: %s", v2Raw)
	}
	if nodes[2]["max_connections"].(float64) != 3 || nodes[2]["conn_mode"] != "inbound" || nodes[2]["max_total_connections"].(float64) != 400 {
		t.Fatalf("v2 agent, both: %s", v2Raw)
	}
	if v2["revision"] == v1["revision"] {
		t.Fatal("agent upgrade did not advance the revision")
	}
	view = e.h.landingNodeView(model.LandingNode{ID: 2, UserID: 2, LandingServerID: 1, Name: "two", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active", MaxConnections: 3, MaxTotalConnections: 400})
	if view["nodeTotalPending"] != false {
		t.Fatalf("pending on a capable agent: %v", view["nodeTotalPending"])
	}
	// A changed total advances the revision too.
	e.exec("UPDATE landing_node SET max_total_connections = 300 WHERE id = 2")
	v3, _ := sync()
	if v3["revision"] == v2["revision"] || connModeNodes(v3)[2]["max_total_connections"].(float64) != 300 {
		t.Fatal("changed total not sent with a new revision")
	}
	// Agent without 仅入站 (landing_conn_mode 0): every limit is 入站+出站 →
	// the smaller of the two.
	e.exec("UPDATE node SET landing_conn_mode = 0 WHERE id = 2")
	old, oldRaw := sync()
	nodes = connModeNodes(old)
	if nodes[2]["max_connections"].(float64) != 3 || nodes[1]["max_connections"].(float64) != 400 || strings.Contains(oldRaw, "conn_mode") || strings.Contains(oldRaw, "max_total_connections") {
		t.Fatalf("old agent: %s", oldRaw)
	}
	t.Logf("native payload, 2 nodes: no totals %d bytes, v1 %d, v2 %d", len(baseRaw), len(v1Raw), len(v2Raw))
}

// Plan 211: the monitor shows 入站 / 总 / 上限 per node, the sums per server
// and the socket breakdown from the tracker (only when it adds up).
func TestLandingMonitorNodeTotal(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_conn_mode=2 WHERE id=2", true)
	e.exec("UPDATE landing_node SET max_total_connections = 400, max_connections = 0, connections = 87, inbound_connections = 12 WHERE id = 1")
	now := time.Now().UnixMilli()
	tcp := &landingcustom.TCPCounts{Inbound: 14, Outbound: 73, Established: 20, TimeWait: 60, CloseWait: 5}
	c := nativeCounter(1, 1, 0, 0, 87)
	c.TCP = tcp
	e.h.landingRateTracker().observe([]repo.LandingCounter{c}, now)
	view := monitorFor(t, e, true)
	if len(view.Servers) != 1 || len(view.Servers[0].Nodes) != 1 {
		t.Fatalf("monitor %+v", view)
	}
	s, n := view.Servers[0], view.Servers[0].Nodes[0]
	if n.InboundConnections != 12 || n.TotalConnections != 87 || n.MaxTotalConnections != 400 || !n.InboundKnown || n.TotalPending || n.TCP == nil || *n.TCP != *tcp {
		t.Fatalf("node %+v tcp %+v", n, n.TCP)
	}
	if s.InboundConnections != 12 || s.TotalConnections != 87 || !s.InboundKnown {
		t.Fatalf("server sums %+v", s)
	}
	raw, _ := json.Marshal(view)
	t.Logf("monitor payload, 1 server 1 node with breakdown: %d bytes", len(raw))
	// A breakdown that does not add up to the total is dropped.
	bad := nativeCounter(1, 1, 0, 0, 90)
	bad.TCP = tcp
	e.h.landingRateTracker().observe([]repo.LandingCounter{bad}, now+2000)
	if n := monitorFor(t, e, true).Servers[0].Nodes[0]; n.TCP != nil {
		t.Fatalf("mismatching breakdown kept: %+v", n.TCP)
	}
	// Users see their own node's numbers too (no owner fields).
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2, CreatedTime: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.r.InsertMonitorPermission(2, now); err != nil {
		t.Fatal(err)
	}
	snap, err := e.r.LandingMonitorSnapshot(2, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u := buildLandingMonitorView(snap, false, e.h.landingRateTracker(), time.Now()); len(u.Servers) != 1 || u.Servers[0].Nodes[0].TotalConnections != 87 || u.Servers[0].Nodes[0].UserName != "" {
		t.Fatalf("user monitor %+v", u)
	}
}
