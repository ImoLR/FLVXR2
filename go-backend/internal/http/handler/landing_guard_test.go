package handler

import (
	"fmt"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func guardSeed(t *testing.T, e *flowTestEnv) {
	t.Helper()
	e.addUser(2)
	e.addNode(2, "landing-secret")
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_chain=1, landing_guard=1 WHERE id=2", true)
	reality := func(id int64, port int, dest, names string) *model.LandingNode {
		return &model.LandingNode{ID: id, UserID: 2, LandingServerID: 1, Name: fmt.Sprint("r", id), Protocol: "vless_reality_vision", Port: port, InboundTag: fmt.Sprint("flvxl-", id), Status: "active",
			UUID: "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f", Flow: "xtls-rprx-vision", Dest: dest, ServerNames: names, PrivateKey: "YFYOAdbw1bKTHlNNi-aEjBM3BO7unuFC5rOkMRAz9XY", ShortID: "a1b2"}
	}
	for _, v := range []interface{}{
		&model.LandingServer{ID: 1, NodeID: 2, Source: "native", Name: "landing", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active",
			Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, EgressReady: true},
		reality(1, 30001, "www.lovelive-anime.jp:443", `["www.lovelive-anime.jp"]`),
		reality(2, 30002, "www.lovelive-anime.jp:443", `["WWW.lovelive-anime.jp"]`), // same group (case)
		reality(3, 30003, "www.example.com:443", `["www.example.com"]`),
		&model.LandingNode{ID: 4, UserID: 2, LandingServerID: 1, Name: "ss", Protocol: "ss2022", Port: 30004, InboundTag: "flvxl-4", Status: "active"},
		&model.ForwardChain{ID: 1, UserID: 2, State: model.ChainStateActive, Port: 30005, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", PrivateKey: "YFYOAdbw1bKTHlNNi-aEjBM3BO7unuFC5rOkMRAz9XY", ShortID: "0a", Dest: "www.lovelive-anime.jp:443", ServerNames: `["www.lovelive-anime.jp"]`, Upstream: `{"type":"socks","server":"203.0.113.11","port":1080}`},
		&model.ForwardChainNode{ID: 1, ChainID: 1, LandingServerID: 1, NodeID: 2, Port: 30005, State: "active"},
	} {
		if err := e.r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func desiredGuardPorts(payload map[string]interface{}) map[string]int {
	out := map[string]int{}
	for _, n := range payload["nodes"].([]map[string]interface{}) {
		if p, ok := n["guard_port"].(int); ok {
			out[n["inbound_tag"].(string)] = p
		}
	}
	return out
}

// TestLandingRealityGuardPorts: guard ports are allocated once per (dest,
// serverNames) group from the top of the landing range, stay stable, are
// counted by every port check, freed with the node and released when the
// switch is off; older agents get today's payload.
func TestLandingRealityGuardPorts(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	agent := egressAgent(t, e, true)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	chainTag := fmt.Sprintf("flvxl-%d", model.ChainAgentIDBase+1)
	got := desiredGuardPorts(agent.payloads[len(agent.payloads)-1])
	if got["flvxl-1"] != 30010 || got["flvxl-2"] != 30010 || got[chainTag] != 30010 || got["flvxl-3"] != 30009 || len(got) != 4 {
		t.Fatalf("guard ports = %v", got)
	}
	// Steady: the next sync keeps them (same revision, same payload).
	rev, _ := e.r.GetLandingServer(1)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if again := desiredGuardPorts(agent.payloads[len(agent.payloads)-1]); fmt.Sprint(again) != fmt.Sprint(got) {
		t.Fatalf("ports moved: %v → %v", got, again)
	}
	if after, _ := e.r.GetLandingServer(1); after.Revision != rev.Revision {
		t.Fatalf("steady sync bumped revision %d→%d", rev.Revision, after.Revision)
	}
	// Every port check counts guard ports.
	for _, p := range []int{30009, 30010} {
		if used, err := e.r.LandingPortInUse(2, p); err != nil || !used {
			t.Fatalf("LandingPortInUse(%d) = %v %v", p, used, err)
		}
	}
	if err := e.r.ValidateNodeLandingPortRange(2, "30009-30010"); err == nil {
		t.Fatal("rule range over guard ports accepted")
	}
	n, err := e.r.CreateLandingNode(1, 1, "new", "ss2022", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.Port == 30009 || n.Port == 30010 {
		t.Fatalf("new landing node got a guard port %d", n.Port)
	}
	// Deleting the only node of a group frees its port.
	if err := e.r.DeleteLandingNode(3); err != nil {
		t.Fatal(err)
	}
	if used, _ := e.r.LandingPortInUse(2, 30009); used {
		t.Fatal("guard port of a deleted node still reserved")
	}
	// Switch off: ports released, payload without guard_port.
	body := serverSaveBody(t, e, 0, false)
	body["realityGuard"] = false
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatalf("switch off: %+v", out)
	}
	if got := desiredGuardPorts(agent.payloads[len(agent.payloads)-1]); len(got) != 0 {
		t.Fatalf("guard still sent after switch off: %v", got)
	}
	if used, _ := e.r.LandingPortInUse(2, 30010); used {
		t.Fatal("guard port kept after switch off")
	}
	s, _ := e.r.GetLandingServer(1)
	if v := landingGuardView(*s, 1); v["enabled"] != false || v["state"] != "off" {
		t.Fatalf("view %v", v)
	}
	// A save without the field keeps the switch.
	body = serverSaveBody(t, e, 0, false)
	body["name"] = "renamed"
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatal(out)
	}
	if s, _ := e.r.GetLandingServer(1); !s.RealityGuardOff || s.Name != "renamed" {
		t.Fatalf("legacy save changed the guard switch: %+v", s.RealityGuardOff)
	}
	// Normal users never see it.
	for _, key := range []string{"realityGuardOff", "guardState", "guardNodes"} {
		if _, ok := landingMap(*s)[key]; ok {
			t.Fatalf("server JSON exposes %s", key)
		}
	}
	// Back on, with an old agent: nothing allocated, nothing sent.
	body["realityGuard"] = true
	e.exec("UPDATE node SET landing_guard=0 WHERE id=2")
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatal(out)
	}
	if got := desiredGuardPorts(agent.payloads[len(agent.payloads)-1]); len(got) != 0 {
		t.Fatalf("old agent got guard ports: %v", got)
	}
	s, _ = e.r.GetLandingServer(1)
	if v := landingGuardView(*s, 0); v["state"] != "agent_old" {
		t.Fatalf("view %v", v)
	}
	e.h.egressSyncWG.Wait()
}

// TestLandingRealityGuardRangeFull: no free port → the node stays unguarded
// with a warning instead of failing the sync.
func TestLandingRealityGuardRangeFull(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	e.exec("UPDATE landing_server SET port_max=30005 WHERE id=1")
	agent := egressAgent(t, e, true)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	payload := agent.payloads[len(agent.payloads)-1]
	if got := desiredGuardPorts(payload); len(got) != 3 || got["flvxl-1"] != 30000 {
		// 30000 is the only free port: the first group gets it, the second
		// (www.example.com) stays unguarded.
		t.Fatalf("guard ports = %v", got)
	}
	s, _ := e.r.GetLandingServer(1)
	if !strings.Contains(s.EgressWarnings, "Reality 防盗") {
		t.Fatalf("no warning recorded: %q", s.EgressWarnings)
	}
}
