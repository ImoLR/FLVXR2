package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

func TestSharedLandingCadenceAndRevisionRetry(t *testing.T) {
	e := newFlowTestEnv(t)
	pushes, polls, lists, hosts := 0, 0, 0, 0
	down := false
	var last landingcustom.Desired
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if down {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case landingcustom.Prefix + "/shares":
			lists++
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{{ID: 7, Name: "shared", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}, Core: landingcustom.Core{State: "running"}}}})
		case landingcustom.Prefix + "/landing-hosts":
			hosts++
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			pushes++
			json.NewDecoder(r.Body).Decode(&last)
			json.NewEncoder(w).Encode(map[string]any{"accepted_revision": last.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			polls++
			json.NewEncoder(w).Encode(landingcustom.Status{Core: landingcustom.Core{State: "running"}, CollectedAt: time.Now().UnixMilli()})
		default:
			t.Errorf("unexpected Custom path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	e.h.syncSharedIntegration(context.Background(), false)
	if pushes != 1 || polls != 1 || hosts != 1 || lists != 1 {
		t.Fatalf("initial cadence %d %d %d %d", pushes, polls, hosts, lists)
	}
	original := last.Revision
	for i := 0; i < 4; i++ {
		e.h.syncSharedIntegration(context.Background(), false)
	}
	if pushes != 1 || polls != 1 || hosts != 1 || lists != 1 {
		t.Fatal("unchanged state polled/pushed too often")
	}
	e.r.DB().Model(&model.LandingServer{}).Where("source='shared'").Update("shared_last_sync_at", time.Now().Add(-61*time.Second).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if polls != 2 || pushes != 1 {
		t.Fatal("60 second poll")
	}
	e.r.DB().Model(&model.LandingServer{}).Where("source='shared'").Update("shared_last_push_at", time.Now().Add(-11*time.Minute).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if pushes != 2 || last.Revision != original {
		t.Fatal("10 minute identical full re-push changed revision")
	}
	down = true
	e.h.sharedLanding.lastList = time.Time{}
	e.h.syncSharedIntegration(context.Background(), false)
	retry := e.h.sharedLanding.offline
	if retry.failures != 1 || time.Until(retry.next) > 15*time.Second {
		t.Fatal("first retry backoff")
	}
	e.h.syncSharedIntegration(context.Background(), false)
	if e.h.sharedLanding.offline.failures != 1 {
		t.Fatal("backoff did not suppress network retry")
	}
	if sharedBackoff(20) != 10*time.Minute {
		t.Fatal("unbounded retry")
	}
	down = false
	e.h.syncSharedIntegration(context.Background(), true)
	if e.h.sharedLanding.offline.failures != 0 {
		t.Fatal("recovery did not reset backoff")
	}
}

func TestSharedLandingRuleAutoCreation(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	share := landingcustom.Share{ID: 7, Name: "shared-rule", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022", "vless_reality_vision"}}
	if err := e.r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	servers, _ := e.r.ListLandingServers(0, true)
	target, id, created, err := e.h.resolveNativeLandingTarget(map[string]any{"landingServerId": servers[0].ID, "landingProtocol": "vless_reality_vision", "name": "rule landing"}, 2, 1, "", true)
	if err != nil || id == 0 || created != id || target != "203.0.113.7:30000" {
		t.Fatalf("rule target %s %d %d %v", target, id, created, err)
	}
	n, _ := e.r.GetLandingNode(id)
	if n.Protocol != "vless_reality_vision" {
		t.Fatal("auto-created wrong protocol")
	}
	target2, id2, created2, err := e.h.resolveNativeLandingTarget(map[string]any{"landingNodeId": id}, 2, 1, "", true)
	if err != nil || target2 != target || id2 != id || created2 != 0 {
		t.Fatal("reuse own shared node")
	}
}
