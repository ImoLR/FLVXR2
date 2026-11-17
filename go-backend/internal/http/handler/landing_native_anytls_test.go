package handler

import (
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 274: native AnyTLS landing nodes end to end through the handlers.
func TestNativeAnyTLSSyncFilterAndLineMachine(t *testing.T) {
	e, s := landingTargetHandlerEnv(t)
	a := newChainAgent()
	e.h.nodeCommandSender = a.send
	srv, _ := e.r.GetLandingServer(s.ID)
	srv.Protocols = `["ss2022","vless_reality_vision","anytls"]`
	if err := e.r.SaveLandingServer(srv, nil); err == nil || !strings.Contains(err.Error(), "版本过低") {
		t.Fatalf("switch on old agent: %v", err)
	}
	view := e.h.landingServerView(*srv)
	if view["anytlsAvailable"] != false || asString(view["anytlsReason"]) != repo.AnyTLSAgentReason {
		t.Fatalf("server view %v %v", view["anytlsAvailable"], view["anytlsReason"])
	}
	e.exec("UPDATE node SET landing_anytls = 1 WHERE id = 3")
	if err := e.r.SaveLandingServer(srv, nil); err != nil {
		t.Fatal(err)
	}
	if view = e.h.landingServerView(*srv); view["anytlsAvailable"] != true || view["anytlsReason"] != nil {
		t.Fatalf("server view after OTA %v", view)
	}
	n, err := e.r.CreateLandingNode(7, s.ID, "at", landingcustom.ProtocolAnyTLS, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := e.h.landingNodeView(*n); v["protocolOpts"] == nil || v["password"] != nil {
		t.Fatalf("node view leaks or lacks opts: %v", v)
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
	if d == nil || d["protocol"] != "anytls" || d["vless"] != nil || d["ss2022"] != nil || d["guard_port"] != nil || d["listen"] != nil {
		t.Fatalf("native desired %v", d)
	}
	obj, ok := d["anytls"].(map[string]interface{})
	if !ok || obj["password"] != n.Password || obj["certificate_pem"] != n.TLSCertPEM || obj["key_pem"] != n.TLSKeyPEM {
		t.Fatalf("anytls object %#v", d["anytls"])
	}
	cur, _ := e.r.GetLandingServer(s.ID)
	// Agent rolled back: the node is withheld, the revision moves on.
	e.exec("UPDATE node SET landing_anytls = 0 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if find() != nil {
		t.Fatal("AnyTLS node sent to an old agent")
	}
	old, _ := e.r.GetLandingServer(s.ID)
	if old.Revision == cur.Revision {
		t.Fatal("withholding the AnyTLS node kept the revision")
	}
	e.exec("UPDATE node SET landing_anytls = 1 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.r.GetLandingServer(s.ID); find() == nil || again.Revision == old.Revision {
		t.Fatal("agent upgrade did not resend the AnyTLS node under a new revision")
	}

	// 屏蔽 TLS tunnel (normal tunnel 30): AnyTLS refused (TLS feature).
	e.exec("UPDATE tunnel SET tls = 1, http = 0 WHERE id = 30")
	if err := e.h.landingFilterTargetError(30, "gost", n.ID); err == nil {
		t.Fatal("AnyTLS accepted on a TLS-blocking tunnel")
	}
	if err := e.h.landingFilterPreCheck(30, "gost", map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "anytls"}); err == nil {
		t.Fatal("new AnyTLS accepted on a TLS-blocking tunnel")
	}
	e.exec("UPDATE tunnel SET tls = 0, http = 0 WHERE id = 30")
	// 线路机 (tunnel 10): AnyTLS allowed, existing node or new.
	if err := e.h.lineMachineTargetError(10, "gost", n.ID, true); err != nil {
		t.Fatalf("line machine existing AnyTLS: %v", err)
	}
	if _, id, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "anytls"}, 7, 10, "", false); err != nil || id == 0 {
		t.Fatalf("line machine new AnyTLS: id=%d err=%v", id, err)
	}
	if !forwardLineCompliant(model.Forward{LandingNodeID: n.ID}, false, map[int64]string{n.ID: "anytls"}) {
		t.Fatal("AnyTLS rule not compliant on 线路机")
	}
}
