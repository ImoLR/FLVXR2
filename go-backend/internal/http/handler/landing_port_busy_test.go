package handler

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// portBusyAgent answers ApplyLandingDesired like a plan-147 agent: every node
// whose port is in busy is reported with port + reason; legacy omits the
// node_errors field (older agent).
type portBusyAgent struct {
	busy     map[int]bool
	allBusy  bool
	legacy   bool
	calls    int
	payloads []map[string]interface{}
}

func (a *portBusyAgent) send(_ int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
	if method != "ApplyLandingDesired" {
		return ws.CommandResult{Success: true}, nil
	}
	a.calls++
	payload := data.(map[string]interface{})
	a.payloads = append(a.payloads, payload)
	errs := []map[string]interface{}{}
	for _, n := range payload["nodes"].([]map[string]interface{}) {
		port := n["port"].(int)
		if a.allBusy || a.busy[port] {
			errs = append(errs, map[string]interface{}{"id": n["id"], "port": port, "code": "port_in_use", "reason": "TCP: address already in use", "since": 1})
		}
	}
	out := map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running", "nodes": []repo.LandingCounter{}}
	if !a.legacy {
		out["node_errors"] = errs
	}
	return ws.CommandResult{Success: true, Data: out}, nil
}

func portBusySeed(t *testing.T, e *flowTestEnv) {
	t.Helper()
	e.addUser(2)
	e.addNode(2, "landing-secret")
	e.exec("UPDATE node SET landing_capable=?, port='10000-29999' WHERE id=2", true)
	if err := e.r.DB().Create(&model.LandingServer{ID: 1, NodeID: 2, Source: "native", Name: "landing", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active", MaxNodesPerUser: 10,
		Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, Revision: 1, CreatedTime: e.now, UpdatedTime: e.now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2, CreatedTime: e.now}).Error; err != nil {
		t.Fatal(err)
	}
}

func landingNode(t *testing.T, e *flowTestEnv, id int64) *model.LandingNode {
	t.Helper()
	n, err := e.r.GetLandingNode(id)
	if err != nil || n == nil {
		t.Fatalf("node %d: %v", id, err)
	}
	return n
}

// TestLandingPortBusyNodeErrors (plan 147): the agent reports a node whose port
// another program holds. A just-created, never-used node moves to the next
// free port and is redeployed (the busy port is remembered and avoided); a
// node with traffic keeps its port and shows a short reason; the moves are
// bounded (3); an older agent changes nothing; a later clean report clears
// the reason.
func TestLandingPortBusyNodeErrors(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	agent := &portBusyAgent{busy: map[int]bool{30000: true}}
	e.h.nodeCommandSender = agent.send

	fresh, err := e.r.CreateLandingNode(2, 1, "fresh", "ss2022", true)
	if err != nil || fresh.Port != 30000 {
		t.Fatalf("fresh node: %+v %v", fresh, err)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	n := landingNode(t, e, fresh.ID)
	if n.Port != 30001 || n.PortMoves != 1 || n.SyncError != "" {
		t.Fatalf("fresh node not moved: port %d moves %d err %q", n.Port, n.PortMoves, n.SyncError)
	}
	if agent.calls != 2 {
		t.Fatalf("expected one re-push after the move, agent calls %d", agent.calls)
	}
	last := agent.payloads[len(agent.payloads)-1]
	if last["nodes"].([]map[string]interface{})[0]["port"].(int) != 30001 || last["revision"].(int64) <= agent.payloads[0]["revision"].(int64) {
		t.Fatalf("re-push %v", last)
	}
	// The busy port is skipped for later nodes too.
	next, err := e.r.CreateLandingNode(2, 1, "next", "ss2022", true)
	if err != nil || next.Port != 30002 {
		t.Fatalf("busy port not avoided: %+v %v", next, err)
	}

	// A node with traffic keeps its port and shows the reason (admin + owner).
	e.exec("UPDATE landing_node SET up_bytes=100 WHERE id=?", next.ID)
	agent.busy[30002] = true
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	n = landingNode(t, e, next.ID)
	want := "端口 30002 被这台机器上的其他程序占用（TCP: address already in use）"
	if n.Port != 30002 || n.SyncError != want {
		t.Fatalf("used node: port %d err %q", n.Port, n.SyncError)
	}
	s, _ := e.r.GetLandingServer(1)
	view := landingNodeViewData(*n, s, nil, e.h.landingEgressCatalog(), "", false)
	if view["status"] != "error" || view["syncError"] != want {
		t.Fatalf("view %v %v", view["status"], view["syncError"])
	}
	// Steady state: the same report keeps the reason and the port.
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if n = landingNode(t, e, next.ID); n.SyncError != want || n.Port != 30002 {
		t.Fatalf("steady state changed the node: %q", n.SyncError)
	}

	// The program goes away: the clean report clears the reason.
	delete(agent.busy, 30002)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if n = landingNode(t, e, next.ID); n.SyncError != "" {
		t.Fatalf("reason kept: %q", n.SyncError)
	}

	// Every port busy: at most 3 moves, then the error with the note.
	third, err := e.r.CreateLandingNode(2, 1, "third", "ss2022", true)
	if err != nil {
		t.Fatal(err)
	}
	agent.allBusy, agent.calls = true, 0
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	n = landingNode(t, e, third.ID)
	if n.PortMoves != 3 || !strings.Contains(n.SyncError, "已自动换端口 3 次仍失败") {
		t.Fatalf("bounded moves: moves %d err %q", n.PortMoves, n.SyncError)
	}
	if agent.calls > 4 {
		t.Fatalf("unbounded re-push: %d agent calls", agent.calls)
	}
	agent.calls = 0
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if agent.calls != 1 || landingNode(t, e, third.ID).PortMoves != 3 {
		t.Fatalf("exhausted node moved again: calls %d", agent.calls)
	}

	// Older agent (no node_errors): nothing recorded, nothing moved.
	agent.allBusy, agent.legacy = false, true
	agent.busy = map[int]bool{}
	legacyNode, err := e.r.CreateLandingNode(2, 1, "legacy", "ss2022", true)
	if err != nil {
		t.Fatal(err)
	}
	agent.busy[legacyNode.Port] = true
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if n = landingNode(t, e, legacyNode.ID); n.PortMoves != 0 || n.SyncError != "" || n.Port != legacyNode.Port {
		t.Fatalf("old agent changed the node: %+v", n)
	}
}

// A node a rule targets is never moved, even when fresh.
func TestLandingPortBusyKeepsReferencedNode(t *testing.T) {
	e := newFlowTestEnv(t)
	portBusySeed(t, e)
	agent := &portBusyAgent{busy: map[int]bool{30000: true}}
	e.h.nodeCommandSender = agent.send
	n, err := e.r.CreateLandingNode(2, 1, "ruled", "ss2022", true)
	if err != nil {
		t.Fatal(err)
	}
	e.addTunnel(1, 2, 1)
	e.addForward(1, 2, 1)
	e.exec("UPDATE forward SET landing_node_id=? WHERE id=1", n.ID)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	got := landingNode(t, e, n.ID)
	if got.Port != 30000 || got.PortMoves != 0 || !strings.Contains(got.SyncError, "端口 30000") {
		t.Fatalf("referenced node: port %d moves %d err %q", got.Port, got.PortMoves, got.SyncError)
	}
}
