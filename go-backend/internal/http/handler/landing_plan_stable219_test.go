package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go-backend/internal/landingegress"
	"go-backend/internal/ws"
)

// Plan 219: no landing plan depends on FLVX node addresses any more. A NAT
// node whose egress flips between two IPs (node 48 沪台), address edits, DNS
// answers and DDNS record updates leave every plan hash — and so the revision
// and the push — unchanged.
func TestLandingPlanIgnoresNodeAddresses219(t *testing.T) {
	e, _ := layersEnv(t)
	raw, _ := json.Marshal(landingegress.ForcedSeedRules())
	e.exec("UPDATE landing_egress_setting SET global_forced = ? WHERE id = 1", string(raw))
	offlineDNS(t, map[string]string{"n3.example.com": "198.51.100.3"})
	e.addNode(3, "entry")
	before, plan := compiledHash(t, e.h, 1)
	direct := findRule(plan, "禁止大陆直连")
	if direct == nil || strings.Join(direct.SourceIP, ",") != "geoip:cn" || direct.Outbound != landingegress.ActionBlock {
		t.Fatalf("禁止大陆直连 must stay plain source geoip:cn → block: %+v", direct)
	}

	same := func(step string) {
		t.Helper()
		if got, _ := compiledHash(t, e.h, 1); got != before {
			t.Fatalf("%s changed the landing plan hash", step)
		}
	}
	// Agent egress reports flipping between two NAT exits (written to
	// reported_ip_v4/v6, still used by the chain-upstream guard).
	for _, ip := range []string{"103.246.219.39", "114.35.6.24", "103.246.219.39"} {
		if _, err := e.r.FillNodePublicIPs(3, ip, "240e:b8f:737:1204::7"); err != nil {
			t.Fatal(err)
		}
		same("reported egress " + ip)
	}
	// Admin address edits, a hostname whose DNS answer changes, extra IPs.
	e.exec("UPDATE node SET server_ip='n3.example.com', server_ip_v4='112.64.1.9', server_ip_v6='2408:8748:a100:59::1', intranet_ip='10.0.0.3', extra_ips='[\"203.0.113.33\"]' WHERE id=3")
	same("node address edit")
	offlineDNS(t, map[string]string{"n3.example.com": "198.51.100.4"})
	same("DNS answer change")
	// DDNS records (created, then the address rotates).
	e.exec(`INSERT INTO node_ddns_record(node_id, domain, record_type, current_ip, status) VALUES(3, 'n3.ddns.example.com', 'AAAA', '240e:b8f:737:1204::7', 'synced')`)
	same("DDNS record")
	e.exec(`UPDATE node_ddns_record SET current_ip='2408:820c:7508:34d4::7' WHERE node_id=3`)
	same("DDNS rotation")
	// A whole new node (no landing on it).
	e.addNode(4, "other")
	same("new node")
}

// Plan 219: the manual 来源白名单 is gone (route removed, nothing reads the
// column); the overview no longer reports exempt node addresses.
func TestLandingWhitelistRemoved219(t *testing.T) {
	e, _ := layersEnv(t)
	mux := http.NewServeMux()
	e.h.registerLanding(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/landing/egress/whitelist/save", strings.NewReader(`{"whitelist":["198.51.100.9"]}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("whitelist/save still served: %d %s", w.Code, w.Body.String())
	}
	view := dataMap(t, egressCall(t, e.h, "/api/v1/landing/egress/overview", map[string]any{}, 0))
	for _, k := range []string{"whitelist", "exemptNodeCount", "exemptPreview"} {
		if _, ok := view[k]; ok {
			t.Fatalf("overview still has %s", k)
		}
	}
}

// Plan 219: the relay-source list of native landings (plan 158, part of the
// revision signature) keeps a node's reported interface addresses while the
// node is offline, so a disconnect + reconnect with the same addresses does
// not bump every native landing twice; a real address change still does.
func TestLandingRelaySourcesSurviveOffline219(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(5, "relay")
	m := e.h.ddnsState()
	report := func(v6 string) {
		e.h.onNodeAddrs(5, ws.NodeAddrs{V6: []string{v6}})
		m.pending.Wait()
	}
	report("2a01:4f8:c0c:1::5")
	first := e.h.landingRelaySources(context.Background())
	if !slices.Contains(first, "2a01:4f8:c0c:1::5") {
		t.Fatalf("reported address missing: %v", first)
	}
	e.exec("UPDATE node SET status=0 WHERE id=5")
	e.h.ddnsNodeOffline(5)
	if got := e.h.landingRelaySources(context.Background()); !slices.Equal(got, first) {
		t.Fatalf("offline node changed the relay sources: %v → %v", first, got)
	}
	e.exec("UPDATE node SET status=1 WHERE id=5")
	report("2a01:4f8:c0c:1::5")
	if got := e.h.landingRelaySources(context.Background()); !slices.Equal(got, first) {
		t.Fatalf("reconnect with the same addresses changed the relay sources: %v", got)
	}
	report("2a01:4f8:c0c:2::5")
	got := e.h.landingRelaySources(context.Background())
	if slices.Contains(got, "2a01:4f8:c0c:1::5") || !slices.Contains(got, "2a01:4f8:c0c:2::5") {
		t.Fatalf("real address change not applied: %v", got)
	}
}
