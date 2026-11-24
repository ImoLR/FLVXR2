package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// hostPortAgent is a plan-147 agent on a machine where other programs listen
// on hostBusy (TCP): TcpPing 127.0.0.1:<port> connects exactly there, and a
// landing inbound on such a port fails with port_in_use. offline = every
// command fails; noData = TcpPing answers without data (unusable answer).
type hostPortAgent struct {
	hostBusy map[int]bool
	pings    []int
	applies  int
	offline  bool
	noData   bool
}

func (a *hostPortAgent) send(_ int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
	if a.offline {
		return ws.CommandResult{}, errors.New("节点不在线")
	}
	switch method {
	case "TcpPing":
		m := data.(map[string]interface{})
		ip, port := m["ip"].(string), m["port"].(int)
		if ip != "127.0.0.1" || m["count"].(int) != 1 {
			return ws.CommandResult{}, fmt.Errorf("unexpected probe %v", m)
		}
		a.pings = append(a.pings, port)
		if a.noData {
			return ws.CommandResult{Success: true}, nil
		}
		if a.hostBusy[port] {
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"ip": ip, "port": port, "success": true, "averageTime": 0.1}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"ip": ip, "port": port, "success": false, "errorKind": "refused",
			"errorMessage": "所有TCP连接尝试都失败（连接被拒绝：目标端口没有监听）"}}, nil
	case "ApplyLandingDesired":
		a.applies++
		payload := data.(map[string]interface{})
		errs := []map[string]interface{}{}
		for _, n := range payload["nodes"].([]map[string]interface{}) {
			if port := n["port"].(int); a.hostBusy[port] {
				errs = append(errs, map[string]interface{}{"id": n["id"], "port": port, "code": "port_in_use", "reason": "TCP: address already in use", "since": 1})
			}
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running",
			"nodes": []repo.LandingCounter{}, "node_errors": errs}}, nil
	}
	return ws.CommandResult{Success: true}, nil
}

func probeCreate(t *testing.T, e *flowTestEnv, sub string, role int, body string) (response.R, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/landing/node/create", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: sub, RoleID: role}))
	w := httptest.NewRecorder()
	e.h.landingNodeCreate(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("create as %s: %v %s", sub, err, w.Body.String())
	}
	if out.Code != 0 {
		return out, nil
	}
	return out, dataMap(t, out)
}

func busyPortsOf(t *testing.T, e *flowTestEnv, serverID int64) map[string]int64 {
	t.Helper()
	var raw string
	if err := e.r.DB().Raw("SELECT native_busy_ports FROM landing_server WHERE id = ?", serverID).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("native_busy_ports %q: %v", raw, err)
		}
	}
	return out
}

func samePorts(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestLandingPortProbeSkipsHostUsedPorts (plan 304): before the pick, the
// next ports are checked on the machine (one loopback connect each); ports
// another program listens on are remembered and skipped, so the node starts
// on its first port — no failed start, no move, no extra push. Remembered
// ports are not probed again while fresh.
func TestLandingPortProbeSkipsHostUsedPorts(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	agent := &hostPortAgent{hostBusy: map[int]bool{30000: true, 30001: true}}
	e.h.nodeCommandSender = agent.send

	out, view := probeCreate(t, e, "2", 1, `{"landingServerId":1,"name":"mine","protocol":"ss2022"}`)
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	n := landingNode(t, e, int64(view["id"].(float64)))
	if n.Port != 30002 || n.PortMoves != 0 || n.SyncError != "" {
		t.Fatalf("node: port %d moves %d err %q", n.Port, n.PortMoves, n.SyncError)
	}
	if !samePorts(agent.pings, 30000, 30001, 30002) || agent.applies != 1 {
		t.Fatalf("probes %v, pushes %d", agent.pings, agent.applies)
	}
	busy := busyPortsOf(t, e, 1)
	if len(busy) != 2 || busy["30000"] == 0 || busy["30001"] == 0 {
		t.Fatalf("remembered %v", busy)
	}

	// Next node: the remembered ports are skipped without a new check.
	agent.pings = nil
	out, view = probeCreate(t, e, "1", 0, `{"landingServerId":1,"userId":2,"name":"second","protocol":"ss2022"}`)
	if out.Code != 0 || landingNode(t, e, int64(view["id"].(float64))).Port != 30003 || !samePorts(agent.pings, 30003) {
		t.Fatalf("second: %+v probes %v", out, agent.pings)
	}

	// After the 24 h lifetime a remembered port is checked again, not just
	// handed out: still taken → remembered again (re-check, not forget).
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	e.exec("UPDATE landing_server SET native_busy_ports = ? WHERE id = 1", fmt.Sprintf(`{"30000":%d,"30001":%d}`, old, old))
	e.exec("DELETE FROM landing_node")
	agent.pings = nil
	if out, view = probeCreate(t, e, "2", 1, `{"landingServerId":1,"name":"third","protocol":"ss2022"}`); out.Code != 0 {
		t.Fatalf("third: %+v", out)
	}
	if got := landingNode(t, e, int64(view["id"].(float64))).Port; got != 30002 || !samePorts(agent.pings, 30000, 30001, 30002) {
		t.Fatalf("expired ports not re-checked: port %d probes %v", got, agent.pings)
	}
	if busy = busyPortsOf(t, e, 1); busy["30000"] <= old || busy["30001"] <= old {
		t.Fatalf("re-check did not refresh %v", busy)
	}
}

// A user without access to the server costs the machine nothing: no probe.
func TestLandingPortProbeNeedsAccess(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	e.addUser(3)
	agent := &hostPortAgent{hostBusy: map[int]bool{}}
	e.h.nodeCommandSender = agent.send
	if out, _ := probeCreate(t, e, "3", 1, `{"landingServerId":1,"name":"x","protocol":"ss2022"}`); out.Code == 0 {
		t.Fatal("create without access succeeded")
	}
	if len(agent.pings) != 0 {
		t.Fatalf("probed for a refused user: %v", agent.pings)
	}
}

// The probe never blocks creation: no usable answer, an offline agent or an
// offline node leave the pick as before; plan 147 then moves the fresh node
// off the busy port (today's behaviour).
func TestLandingPortProbeFallsBack(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	agent := &hostPortAgent{hostBusy: map[int]bool{30000: true}, noData: true}
	e.h.nodeCommandSender = agent.send

	out, view := probeCreate(t, e, "1", 0, `{"landingServerId":1,"userId":2,"name":"nodata","protocol":"ss2022"}`)
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	n := landingNode(t, e, int64(view["id"].(float64)))
	if n.Port != 30001 || n.PortMoves != 1 {
		t.Fatalf("fallback (plan 147 move): port %d moves %d", n.Port, n.PortMoves)
	}
	if len(agent.pings) != 2 { // one stopped probe at create, one at the move
		t.Fatalf("probes %v", agent.pings)
	}

	agent.offline, agent.pings = true, nil
	if out, view = probeCreate(t, e, "1", 0, `{"landingServerId":1,"userId":2,"name":"offline","protocol":"ss2022"}`); out.Code != 0 {
		t.Fatalf("offline agent blocked creation: %+v", out)
	}
	if got := landingNode(t, e, int64(view["id"].(float64))).Port; got != 30002 {
		t.Fatalf("offline agent: port %d", got)
	}

	e.exec("UPDATE node SET status = 0 WHERE id = 2")
	agent.offline, agent.noData, agent.pings = false, false, nil
	if out, _ = probeCreate(t, e, "1", 0, `{"landingServerId":1,"userId":2,"name":"node-off","protocol":"ss2022"}`); out.Code != 0 || len(agent.pings) != 0 {
		t.Fatalf("offline node: %+v probes %v", out, agent.pings)
	}
}

// The plan-147 move of a fresh node checks its new port first: one move
// straight to a free port instead of one failed start per busy port.
func TestLandingPortProbeMoveTarget(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	agent := &hostPortAgent{hostBusy: map[int]bool{30000: true, 30001: true}}
	e.h.nodeCommandSender = agent.send
	// Created without the probe (e.g. the agent was offline at the time).
	fresh, err := e.r.CreateLandingNode(2, 1, "fresh", "ss2022", true)
	if err != nil || fresh.Port != 30000 {
		t.Fatalf("fresh: %+v %v", fresh, err)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	n := landingNode(t, e, fresh.ID)
	if n.Port != 30002 || n.PortMoves != 1 || n.SyncError != "" || agent.applies != 2 {
		t.Fatalf("move: port %d moves %d err %q pushes %d", n.Port, n.PortMoves, n.SyncError, agent.applies)
	}
	if !samePorts(agent.pings, 30001, 30002) {
		t.Fatalf("probes %v", agent.pings)
	}
	// Steady state (node runs): no report, no probe.
	agent.pings = nil
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if len(agent.pings) != 0 {
		t.Fatalf("steady state probed %v", agent.pings)
	}
	// A node that cannot move (has traffic) and keeps failing: the error is
	// shown, and the repeated report probes nothing.
	e.exec("UPDATE landing_node SET up_bytes = 1 WHERE id = ?", fresh.ID)
	agent.hostBusy[30002] = true
	for i := 0; i < 2; i++ {
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
	}
	if n = landingNode(t, e, fresh.ID); n.Port != 30002 || !strings.Contains(n.SyncError, "端口 30002 被这台机器上的其他程序占用") || len(agent.pings) != 0 {
		t.Fatalf("stuck node: port %d err %q probes %v", n.Port, n.SyncError, agent.pings)
	}
}

// Bounded: one allocation checks at most need + 3 ports, even when every
// port of the range is taken.
func TestLandingPortProbeBounded(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	all := map[int]bool{}
	for p := 30000; p <= 30010; p++ {
		all[p] = true
	}
	agent := &hostPortAgent{hostBusy: all}
	e.h.nodeCommandSender = agent.send
	if busy := e.h.probeNativeLandingPorts(1, 1); !samePorts(busy, 30000, 30001, 30002, 30003) || len(agent.pings) != 4 {
		t.Fatalf("busy %v probes %v", busy, agent.pings)
	}
	agent.pings = nil
	if busy := e.h.probeNativeLandingPorts(1, 2); len(busy) != 5 || len(agent.pings) != 5 {
		t.Fatalf("need 2: busy %v probes %v", busy, agent.pings)
	}
}

// The real layout of Boil HKT 99u / Fachost seednet 黑五 (11-24 prod): range
// 30000–39999, node rule ranges outside it, FLVX's own node on 30000, mmwx's
// xray on 127.0.0.1:38889. Once every port below 38889 is taken, the next
// pick is 38890 — never 38889.
func TestLandingPortProbeMmwxLayout(t *testing.T) {
	for _, tc := range []struct {
		name, rules string
	}{{"Boil HKT 99u", "40000-50999"}, {"Fachost seednet 黑五", "10000-29999,40000-65535"}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFlowTestEnv(t)
			portBusySeed(t, e)
			e.exec("UPDATE node SET port = ? WHERE id = 2", tc.rules)
			e.exec("UPDATE landing_server SET port_min = 30000, port_max = 39999 WHERE id = 1")
			// FLVX's own node on 30000 (as on prod), every other port below
			// 38889 taken by FLVX listeners (forward ports stand in for them).
			if _, err := e.r.CreateLandingNode(2, 1, "own", "ss2022", true); err != nil {
				t.Fatal(err)
			}
			fill := make([]model.ForwardPort, 0, 8888)
			for p := 30001; p < 38889; p++ {
				fill = append(fill, model.ForwardPort{ForwardID: 1, NodeID: 2, Port: p})
			}
			if err := e.r.DB().CreateInBatches(fill, 500).Error; err != nil {
				t.Fatal(err)
			}
			agent := &hostPortAgent{hostBusy: map[int]bool{38889: true}}
			e.h.nodeCommandSender = agent.send
			out, view := probeCreate(t, e, "1", 0, `{"landingServerId":1,"userId":2,"name":"next","protocol":"ss2022"}`)
			if out.Code != 0 {
				t.Fatalf("create: %+v", out)
			}
			n := landingNode(t, e, int64(view["id"].(float64)))
			if n.Port != 38890 || n.PortMoves != 0 || !samePorts(agent.pings, 38889, 38890) || agent.applies != 1 {
				t.Fatalf("port %d moves %d probes %v pushes %d", n.Port, n.PortMoves, agent.pings, agent.applies)
			}
		})
	}
}
