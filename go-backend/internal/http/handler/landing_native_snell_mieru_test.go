package handler

import (
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 317: native Snell / Mieru landing nodes end to end through the
// handlers: old-agent refusal, view flags, desired objects, withheld on an
// agent rollback, no revision change for servers without such nodes, 线路机
// refusal and the 屏蔽 TLS / HTTP tunnel features.
func TestNativeSnellMieruSyncAndTunnels(t *testing.T) {
	e, s := landingTargetHandlerEnv(t)
	a := newChainAgent()
	e.h.nodeCommandSender = a.send
	srv, _ := e.r.GetLandingServer(s.ID)
	for _, tc := range []struct{ protocol, reason string }{{"snell", repo.SnellAgentReason}, {"mieru", repo.MieruAgentReason}} {
		x := *srv
		x.Protocols = `["ss2022","vless_reality_vision","` + tc.protocol + `"]`
		if err := e.r.SaveLandingServer(&x, nil); err == nil || !strings.Contains(err.Error(), tc.reason) || !strings.Contains(err.Error(), "版本过低") {
			t.Fatalf("%s switch on old agent: %v", tc.protocol, err)
		}
	}
	view := e.h.landingServerView(*srv)
	if view["snellAvailable"] != false || asString(view["snellReason"]) != repo.SnellAgentReason || view["mieruAvailable"] != false || asString(view["mieruReason"]) != repo.MieruAgentReason {
		t.Fatalf("server view %v %v %v %v", view["snellAvailable"], view["snellReason"], view["mieruAvailable"], view["mieruReason"])
	}
	// A server without Snell / Mieru nodes: the agent reporting the new
	// capabilities changes nothing it is sent (same revision).
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := e.r.GetLandingServer(s.ID)
	e.exec("UPDATE node SET landing_snell = 1, landing_mieru = 1 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := e.r.GetLandingServer(s.ID); after.Revision != before.Revision {
		t.Fatalf("agent upgrade alone moved the revision %d → %d", before.Revision, after.Revision)
	}
	if view = e.h.landingServerView(*srv); view["snellAvailable"] != true || view["snellReason"] != nil || view["mieruAvailable"] != true || view["mieruReason"] != nil {
		t.Fatalf("server view after OTA %v", view)
	}
	srv, _ = e.r.GetLandingServer(s.ID)
	srv.Protocols = `["ss2022","vless_reality_vision","snell","mieru"]`
	srv.SnellVersion, srv.SnellObfs, srv.SnellObfsHost, srv.MieruTransport = 5, "http", "www.bing.com", "UDP"
	if err := e.r.SaveLandingServer(srv, nil); err != nil {
		t.Fatal(err)
	}
	sn, err := e.r.CreateLandingNode(7, s.ID, "sn", landingcustom.ProtocolSnell, false)
	if err != nil {
		t.Fatal(err)
	}
	mi, err := e.r.CreateLandingNode(7, s.ID, "mi", landingcustom.ProtocolMieru, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []*model.LandingNode{sn, mi} {
		if v := e.h.landingNodeView(*n); v["protocolOpts"] == nil || v["password"] != nil || v["username"] != nil {
			t.Fatalf("node view leaks or lacks opts: %v", v)
		}
	}
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	find := func(id int64) map[string]interface{} {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, d := range a.desired[3] {
			if asInt64(d["id"], 0) == id {
				return d
			}
		}
		return nil
	}
	d := find(sn.ID)
	if d == nil || d["protocol"] != "snell" || d["ss2022"] != nil || d["vless"] != nil || d["mieru"] != nil || d["guard_port"] != nil || d["listen"] != nil {
		t.Fatalf("snell desired %v", d)
	}
	if obj, ok := d["snell"].(map[string]interface{}); !ok || obj["psk"] != sn.Password || asInt64(obj["version"], 0) != 5 || obj["obfs"] != "http" || obj["obfs_host"] != "www.bing.com" || len(obj) != 4 {
		t.Fatalf("snell object %#v", d["snell"])
	}
	d = find(mi.ID)
	if d == nil || d["protocol"] != "mieru" || d["snell"] != nil {
		t.Fatalf("mieru desired %v", d)
	}
	if obj, ok := d["mieru"].(map[string]interface{}); !ok || obj["username"] != mi.Username || obj["password"] != mi.Password || len(obj) != 2 {
		t.Fatalf("mieru object %#v", d["mieru"])
	}
	cur, _ := e.r.GetLandingServer(s.ID)
	// Agent rolled back: both nodes withheld, the revision moves on.
	e.exec("UPDATE node SET landing_snell = 0, landing_mieru = 0 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if find(sn.ID) != nil || find(mi.ID) != nil {
		t.Fatal("Snell / Mieru node sent to an old agent")
	}
	old, _ := e.r.GetLandingServer(s.ID)
	if old.Revision == cur.Revision {
		t.Fatal("withholding kept the revision")
	}
	// Old agent: new nodes refused, unrelated saves still work.
	if _, err := e.r.CreateLandingNode(7, s.ID, "sn2", landingcustom.ProtocolSnell, false); err == nil || !strings.Contains(err.Error(), repo.SnellAgentReason) {
		t.Fatalf("create on old agent: %v", err)
	}
	w, _ := e.r.GetLandingServer(s.ID)
	w.MaxNodesPerUser = 9
	if err := e.r.SaveLandingServer(w, nil); err != nil {
		t.Fatalf("unrelated save after downgrade: %v", err)
	}
	e.exec("UPDATE node SET landing_snell = 1, landing_mieru = 1 WHERE id = 3")
	if _, err := e.h.syncLandingServer(s.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.r.GetLandingServer(s.ID); find(sn.ID) == nil || find(mi.ID) == nil || again.Revision == old.Revision {
		t.Fatal("agent upgrade did not resend the nodes under a new revision")
	}

	// 线路机 (tunnel 10): Snell / Mieru refused (Reality + AnyTLS only).
	for _, n := range []*model.LandingNode{sn, mi} {
		if err := e.h.lineMachineTargetError(10, "gost", n.ID, true); err == nil {
			t.Fatalf("line machine accepted %s", n.Protocol)
		}
		if _, _, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID, "landingProtocol": n.Protocol}, 7, 10, "", false); err == nil {
			t.Fatalf("line machine new %s accepted", n.Protocol)
		}
	}
	// 屏蔽 HTTP tunnel: Snell obfs=http refused, Mieru passes; 屏蔽 TLS: both pass.
	e.exec("UPDATE tunnel SET tls = 0, http = 1 WHERE id = 30")
	if err := e.h.landingFilterTargetError(30, "gost", sn.ID); err == nil {
		t.Fatal("Snell obfs=http accepted on an HTTP-blocking tunnel")
	}
	if err := e.h.landingFilterTargetError(30, "gost", mi.ID); err != nil {
		t.Fatalf("Mieru refused on an HTTP-blocking tunnel: %v", err)
	}
	e.exec("UPDATE tunnel SET tls = 1, http = 0 WHERE id = 30")
	for _, n := range []*model.LandingNode{sn, mi} {
		if err := e.h.landingFilterTargetError(30, "gost", n.ID); err != nil {
			t.Fatalf("%s refused on a TLS-blocking tunnel: %v", n.Protocol, err)
		}
	}
	e.exec("UPDATE tunnel SET tls = 0, http = 0 WHERE id = 30")
}
