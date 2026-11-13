package handler

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/vlessenc"
)

// Plan 253: VLESS Encryption landing nodes end to end through the handlers.

func vlessEncLink(t *testing.T, host string, port int) string {
	t.Helper()
	k, err := vlessenc.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	return "vless://8f7a1f8e-7c2a-4c1e-9d1b-6c0a5b3c2d10@" + host + ":" + strconv.Itoa(port) + "?encryption=" + vlessenc.Encryption("random", k.PublicKey) + "&flow=xtls-rprx-vision&security=none&type=tcp#my-ve"
}

func TestVLESSEncNativeSyncFilterAndLineMachine(t *testing.T) {
	e, s := landingTargetHandlerEnv(t)
	a := newChainAgent()
	e.h.nodeCommandSender = a.send
	// The 协议模板 switch needs the agent.
	srv, _ := e.r.GetLandingServer(s.ID)
	srv.Protocols = `["ss2022","vless_reality_vision","vless_enc"]`
	if err := e.r.SaveLandingServer(srv, nil); err == nil || !strings.Contains(err.Error(), "版本过低") {
		t.Fatalf("switch on old agent: %v", err)
	}
	view := e.h.landingServerView(*srv)
	if view["vlessEncAvailable"] != false || !strings.Contains(asString(view["vlessEncReason"]), "版本过低") {
		t.Fatalf("server view %v %v", view["vlessEncAvailable"], view["vlessEncReason"])
	}
	e.exec("UPDATE node SET landing_vless_enc = 1 WHERE id = 3")
	if err := e.r.SaveLandingServer(srv, nil); err != nil {
		t.Fatal(err)
	}
	if view = e.h.landingServerView(*srv); view["vlessEncAvailable"] != true || view["vlessEncReason"] != nil {
		t.Fatalf("server view after OTA %v", view)
	}
	n, err := e.r.CreateLandingNode(7, s.ID, "ve", "vless_enc", false)
	if err != nil {
		t.Fatal(err)
	}
	reality, err := e.r.CreateLandingNode(7, s.ID, "rv", "vless_reality_vision", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	find := func() map[string]interface{} {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, d := range a.desired[3] {
			if asInt64(d["id"], 0) == n.ID {
				return d
			}
		}
		return nil
	}
	d := find()
	if d == nil || d["protocol"] != "vless_enc" || d["vless"] != nil || d["ss2022"] != nil || d["guard_port"] != nil {
		t.Fatalf("native desired %v", d)
	}
	if ve, ok := d["vless_enc"].(*landingcustom.VLESSEnc); !ok || *ve != *repo.VLESSEncDesired(*n) || ve.Flow != "xtls-rprx-vision" {
		t.Fatalf("vless_enc object %#v", d["vless_enc"])
	}
	// Agent rolled back: the node is withheld, the rest still syncs.
	e.exec("UPDATE node SET landing_vless_enc = 0 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if find() != nil {
		t.Fatal("vless_enc node sent to an old agent")
	}
	a.mu.Lock()
	sent := len(a.desired[3])
	a.mu.Unlock()
	if sent != 1 {
		t.Fatalf("old agent desired %d nodes", sent)
	}
	e.exec("UPDATE node SET landing_vless_enc = 1 WHERE id = 3")

	// 屏蔽 TLS + 屏蔽 HTTP tunnel (normal tunnel 30): VLESS Encryption passes,
	// Reality does not.
	e.exec("UPDATE tunnel SET tls = 1, http = 1 WHERE id = 30")
	if err := e.h.landingFilterTargetError(30, "gost", n.ID); err != nil {
		t.Fatalf("vless_enc refused on a TLS/HTTP-blocking tunnel: %v", err)
	}
	if err := e.h.landingFilterPreCheck(30, "gost", map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "vless_enc"}); err != nil {
		t.Fatalf("new vless_enc refused on a blocking tunnel: %v", err)
	}
	if err := e.h.landingFilterTargetError(30, "gost", reality.ID); err == nil {
		t.Fatal("Reality accepted on a TLS-blocking tunnel")
	}
	_, id, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "vless_enc"}, 7, 30, "", false)
	if err != nil || id != n.ID {
		t.Fatalf("normal tunnel target: id=%d err=%v", id, err)
	}
	// 线路机 (tunnel 10): refused for users and admins, existing node or new.
	for _, admin := range []bool{false, true} {
		if _, id, created, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "vless_enc"}, 7, 10, "", admin); err == nil || id != 0 || created != 0 || err.Error() != lineMachineRealityMsg {
			t.Fatalf("line machine new vless_enc (admin=%v): %d %d %v", admin, id, created, err)
		}
		if err := e.h.lineMachineTargetError(10, "gost", n.ID, true); err == nil || err.Error() != lineMachineRealityMsg {
			t.Fatalf("line machine existing vless_enc (admin=%v): %v", admin, err)
		}
	}
	if forwardLineCompliant(model.Forward{LandingNodeID: n.ID}, false, map[int64]string{n.ID: "vless_enc"}) {
		t.Fatal("vless_enc rule compliant on 线路机")
	}
}

func TestChainVLESSEncUpstream(t *testing.T) {
	e, a := newChainEnv(t) // exits 3 / 4 without landing_vless_enc
	link := vlessEncLink(t, "8.8.8.8", 443)
	code, msg, data := callAs(t, e.h.forwardChainParse, 7, 1, map[string]interface{}{"link": link})
	if code != 0 || data["protocolLabel"] != "VLESS Encryption" || data["protocol"] != "vless" {
		t.Fatalf("parse: %d %s %v", code, msg, data)
	}
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "ve-old", link, "key-vlessenc-001")); code == 0 || !strings.Contains(msg, "VLESS Encryption") || !strings.Contains(msg, "AkariHK") {
		t.Fatalf("encrypted upstream on old exits: %d %s", code, msg)
	}
	e.exec("UPDATE node SET landing_vless_enc = 1 WHERE id IN (3, 4)")
	c := createChain(t, e, 10, "ve", link, "key-vlessenc-002")
	if !repo.ChainUpstreamEncrypted(*c) || c.UpstreamProtocol != "VLESS Encryption" {
		t.Fatalf("chain %+v", c)
	}
	for _, nodeID := range []int64{3, 4} {
		nodes := a.chainNodes(nodeID)
		if len(nodes) != 1 {
			t.Fatalf("exit %d copies %v", nodeID, nodes)
		}
		raw, _ := json.Marshal(nodes[0]["chain"])
		up := string(raw)
		if nodes[0]["protocol"] != "vless_reality_vision" || !strings.Contains(up, `"encryption":"mlkem768x25519plus.random.0rtt.`) || !strings.Contains(up, `"flow":"xtls-rprx-vision"`) || !strings.Contains(up, `"security":"none"`) {
			t.Fatalf("exit %d copy %v / %s", nodeID, nodes[0], up)
		}
	}
	// An exit rolled back to an old agent: the encrypted copy is withheld
	// there (never dialled unencrypted); the other exit keeps it.
	e.exec("UPDATE node SET landing_vless_enc = 0 WHERE id = 4")
	servers, _ := e.r.ChainServerIDs([]int64{c.ID})
	for _, sid := range servers {
		if _, err := e.h.syncLandingServer(sid); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.chainNodes(3)) != 1 || len(a.chainNodes(4)) != 0 {
		t.Fatalf("copies after rollback: %v / %v", a.chainNodes(3), a.chainNodes(4))
	}
	// A plain (unencrypted) chain is unaffected by the capability.
	if ok, _ := chainUpstreamAvailability(&repo.TunnelLineInfo{Exits: []repo.ChainExit{{Name: "old"}}}, mustUpstream(t, testSSLink)); !ok {
		t.Fatal("plain upstream gated")
	}
}
