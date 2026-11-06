package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestTunnelRelayCredential(t *testing.T) {
	u1, p1 := tunnelRelayCredential("secret-a", 7)
	u2, p2 := tunnelRelayCredential("secret-a", 7)
	_, p3 := tunnelRelayCredential("secret-a", 8)
	_, p4 := tunnelRelayCredential("secret-b", 7)
	if u1 != "flvx-t7" || u1 != u2 || p1 != p2 {
		t.Fatalf("not deterministic: %s/%s %s/%s", u1, p1, u2, p2)
	}
	if len(p1) != 32 || p1 == p3 || p1 == p4 || strings.Contains(p1, "secret") {
		t.Fatalf("weak credential %q (other tunnel %q, other secret %q)", p1, p3, p4)
	}
}

func TestRelayAuthPayloads(t *testing.T) {
	nodes := map[int64]*nodeRecord{
		1: {ID: 1, Name: "entry", ServerIP: "198.51.100.1", ServerIPv4: "198.51.100.1"},
		2: {ID: 2, Name: "exit", ServerIP: "198.51.100.2", ServerIPv4: "198.51.100.2", TCPListenAddr: "[::]"},
	}
	auth := &relayAuthSpec{user: "flvx-t5", pass: strings.Repeat("a", 32)}
	chain, err := buildTunnelChainConfig(5, 1, []tunnelRuntimeNode{{NodeID: 2, Port: 4000, Protocol: "tls"}}, nodes, "", auth)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chain)
	if !strings.Contains(string(raw), `"auth":{"password":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","username":"flvx-t5"}`) {
		t.Fatalf("connector without auth: %s", raw)
	}
	for _, enforce := range []bool{false, true} {
		auth.enforce = enforce
		for _, ct := range []int{2, 3} {
			svc := buildTunnelChainServiceConfig(5, tunnelRuntimeNode{NodeID: 2, Port: 4000, ChainType: ct}, nodes[2], 1, auth)
			h := svc[0]["handler"].(map[string]interface{})
			_, hasAuth := h["auth"]
			guard, _ := h["metadata"].(map[string]interface{})[relayTargetGuardKey].(bool)
			if hasAuth != enforce || guard != (enforce && ct == 3) {
				t.Fatalf("enforce=%v chainType=%d: auth=%v guard=%v", enforce, ct, hasAuth, guard)
			}
		}
	}
	svc := buildTunnelChainServiceConfig(5, tunnelRuntimeNode{NodeID: 2, Port: 4000, ChainType: 3}, nodes[2], 1, nil)
	if _, ok := svc[0]["handler"].(map[string]interface{})["auth"]; ok {
		t.Fatal("nil auth must not require auth")
	}
}

func TestRelayAuthEnforceable(t *testing.T) {
	h := &Handler{jwtSecret: "s"}
	base := func() *tunnelCreateState {
		return &tunnelCreateState{
			TunnelID: 9, RelayAuth: true,
			InNodes:    []tunnelRuntimeNode{{NodeID: 1, ChainType: 1}},
			OutNodes:   []tunnelRuntimeNode{{NodeID: 2, ChainType: 3, Port: 4000}},
			Nodes:      map[int64]*nodeRecord{1: {ID: 1, Name: "entry"}, 2: {ID: 2, Name: "exit"}},
			NodeIDList: []int64{1, 2},
		}
	}
	if ok, why := h.relayAuthEnforceable(base()); !ok {
		t.Fatalf("plain tunnel not enforceable: %s", why)
	}
	if a := h.relayAuthFor(base()); a == nil || !a.enforce || a.user != "flvx-t9" {
		t.Fatalf("relayAuthFor %+v", a)
	}
	off := base()
	off.RelayAuth = false
	if a := h.relayAuthFor(off); a == nil || a.enforce || a.pass == "" {
		t.Fatalf("flag 0 must send but not enforce: %+v", a)
	}
	remote := base()
	remote.Nodes[2].IsRemote = 1
	if ok, _ := h.relayAuthEnforceable(remote); ok {
		t.Fatal("federation tunnel must not enforce")
	}
	for v, want := range map[string]bool{"3.0.27-fork.103 (debian/amd64)": false, "3.0.27-fork.120 (debian/amd64)": true, "": false, "3.0.27-fork.121": true} {
		pipe := base()
		pipe.LeiKwanPipe = &model.LeiKwanPipe{}
		pipe.nodeVersions = map[int64]string{1: v}
		if ok, _ := h.relayAuthEnforceable(pipe); ok != want {
			t.Fatalf("pipe entry %q: enforceable=%v want %v", v, ok, want)
		}
	}
	if agentForkRevision("3.0.27-fork.95 (debian/amd64)") != 95 || agentForkRevision("v1") != 0 {
		t.Fatal("agentForkRevision")
	}
}
