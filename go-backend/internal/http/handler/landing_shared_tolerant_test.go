package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// fakeTolerantCustom serves a share list the test edits between rounds, and
// answers 404 not_found for the status / desired of shares listed in gone.
type fakeTolerantCustom struct {
	mu     sync.Mutex
	shares []landingcustom.Share
	gone   map[int64]bool
	polls  map[int64]int
}

func (f *fakeTolerantCustom) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(req.URL.Path, landingcustom.Prefix)
	switch {
	case path == "/shares":
		json.NewEncoder(w).Encode(map[string]any{"shares": f.shares})
	case path == "/landing-hosts":
		w.Write([]byte("{}"))
	case strings.HasPrefix(path, "/shares/"):
		var id int64
		var tail string
		fmt.Sscanf(strings.TrimPrefix(path, "/shares/"), "%d/%s", &id, &tail)
		if f.gone[id] {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"not_found","message":"share or server not found"}}`))
			return
		}
		switch tail {
		case "status":
			f.polls[id]++
			json.NewEncoder(w).Encode(landingcustom.Status{CollectedAt: time.Now().UnixMilli(), Core: landingcustom.Core{State: "running"}})
		case "desired":
			var d landingcustom.Desired
			json.NewDecoder(req.Body).Decode(&d)
			json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": d.Revision})
		default:
			w.WriteHeader(404)
		}
	default:
		w.WriteHeader(404)
	}
}

func tolerantShare(id int64) landingcustom.Share {
	return landingcustom.Share{ID: id, Name: fmt.Sprintf("share-%d", id), Hosts: landingcustom.Hosts{IPv4: fmt.Sprintf("203.0.113.%d", id)}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}, Core: landingcustom.Core{State: "running"}}
}

func tolerantRowByRemote(t *testing.T, r *repo.Repository, remote int64) model.LandingServer {
	t.Helper()
	var s model.LandingServer
	if err := r.DB().Where("source = ? AND remote_server_id = ?", "shared", remote).First(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

// The 11-09 incident end to end: 4 business shares + one whose server was
// deleted in 妙妙屋 (hostless list item, status not_found). The 4 keep syncing
// with no error, the integration stays healthy, the breaker stays closed, and
// only the broken share shows its problem.
func TestSharedSyncOneBadShareDoesNotStopOthers(t *testing.T) {
	e := newFlowTestEnv(t)
	f := &fakeTolerantCustom{gone: map[int64]bool{}, polls: map[int64]int{}}
	for _, id := range []int64{5, 7, 10, 12, 16} {
		f.shares = append(f.shares, tolerantShare(id))
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.h.syncSharedIntegration(ctx, true)
	// 妙妙屋 deletes server 7 but keeps the share row (the Custom bug).
	f.mu.Lock()
	f.shares[1].Hosts, f.shares[1].Name = landingcustom.Hosts{}, ""
	f.gone[7] = true
	f.shares[0].TrafficLimit = 1 << 40 // a change the good shares must still take
	f.polls = map[int64]int{}
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	cfg, _ := e.r.GetLandingIntegration()
	if cfg.SyncError != "" {
		t.Fatalf("integration error set by one share: %q", cfg.SyncError)
	}
	if e.h.sharedLanding.offline.failures != 0 {
		t.Fatal("per-share not_found tripped the transport breaker")
	}
	for _, id := range []int64{5, 10, 12, 16} {
		s := tolerantRowByRemote(t, e.r, id)
		if s.SharedSyncError != "" || f.polls[id] != 1 {
			t.Fatalf("share %d: err %q polls %d", id, s.SharedSyncError, f.polls[id])
		}
	}
	if s := tolerantRowByRemote(t, e.r, 5); repo.SharedLandingCaps(&s).TrafficLimit != 1<<40 {
		t.Fatal("good share's change not applied")
	}
	bad := tolerantRowByRemote(t, e.r, 7)
	if !strings.Contains(bad.SharedSyncError, "地址缺失") || repo.SharedLandingCaps(&bad).Status != "active" || bad.PublicHost != "203.0.113.7" {
		t.Fatalf("bad share: err %q caps %s host %s", bad.SharedSyncError, bad.SharedCaps, bad.PublicHost)
	}
	if e.h.sharedLanding.vanish.NotFoundCount(bad.ID) != 1 {
		t.Fatalf("not_found not counted: %d", e.h.sharedLanding.vanish.NotFoundCount(bad.ID))
	}
	// The views: no ⚠ for a single bad share, others online.
	if v := e.h.landingServerView(tolerantRowByRemote(t, e.r, 10)); v["online"] != true {
		t.Fatalf("good share shown offline: %v", v["online"])
	}
	// Fixed in 妙妙屋: the item is valid again → its list problem clears.
	f.mu.Lock()
	f.shares[1] = tolerantShare(7)
	delete(f.gone, 7)
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	if s := tolerantRowByRemote(t, e.r, 7); s.SharedSyncError != "" {
		t.Fatalf("fixed share keeps its error: %q", s.SharedSyncError)
	}
	if e.h.sharedLanding.vanish.NotFoundCount(bad.ID) != 0 {
		t.Fatal("successful poll did not reset the not_found streak")
	}
}

// Empty list / mass vanish: nothing revoked, held shares get the error and the
// admin ⚠ field; a revoked share Custom forgot is not polled again.
func TestSharedSyncMassHoldAndGoneRevoked(t *testing.T) {
	e := newFlowTestEnv(t)
	f := &fakeTolerantCustom{gone: map[int64]bool{}, polls: map[int64]int{}}
	for _, id := range []int64{5, 10, 12, 16} {
		f.shares = append(f.shares, tolerantShare(id))
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.h.syncSharedIntegration(ctx, true)
	f.mu.Lock()
	all := f.shares
	f.shares = []landingcustom.Share{}
	f.mu.Unlock()
	for i := 0; i < 5; i++ {
		e.h.syncSharedIntegration(ctx, true)
	}
	for _, id := range []int64{5, 10, 12, 16} {
		s := tolerantRowByRemote(t, e.r, id)
		if repo.SharedLandingCaps(&s).Status != "active" || !strings.Contains(s.SharedSyncError, "为防误撤销") {
			t.Fatalf("share %d: %s %q", id, repo.SharedLandingCaps(&s).Status, s.SharedSyncError)
		}
		v := e.h.landingServerView(s)
		if hold, _ := v["sharedHold"].(string); !strings.Contains(hold, "为防误撤销") {
			t.Fatalf("share %d: no ⚠ field: %v", id, v["sharedHold"])
		}
		if uv := e.h.landingServerUserView(s, landingEgressCatalog{}, &repo.LandingUserNodeCounts{PerServer: map[int64]int64{}}); uv["sharedHold"] != nil {
			t.Fatal("hold leaked to the user view")
		}
	}
	// Custom recovers: holds end, errors clear.
	f.mu.Lock()
	f.shares = all
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	for _, id := range []int64{5, 10, 12, 16} {
		s := tolerantRowByRemote(t, e.r, id)
		if s.SharedSyncError != "" || e.h.sharedHoldMessage(s.ID) != "" {
			t.Fatalf("share %d after recovery: %q", id, s.SharedSyncError)
		}
	}
	// An explicitly revoked share that Custom then forgets (not_found): no
	// error, no integration error, and no more polls.
	f.mu.Lock()
	f.shares[0].Status = "revoked"
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	f.mu.Lock()
	f.shares = f.shares[1:]
	f.gone[5] = true
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	s := tolerantRowByRemote(t, e.r, 5)
	if s.SharedSyncError != "" || !e.h.sharedLanding.gone[s.ID] {
		t.Fatalf("revoked + not_found: err %q gone %v", s.SharedSyncError, e.h.sharedLanding.gone[s.ID])
	}
	if cfg, _ := e.r.GetLandingIntegration(); cfg.SyncError != "" {
		t.Fatalf("integration error: %q", cfg.SyncError)
	}
	f.mu.Lock()
	f.gone[5] = false
	f.polls = map[int64]int{}
	f.mu.Unlock()
	e.h.syncSharedIntegration(ctx, true)
	if f.polls[5] != 0 || f.polls[10] != 1 {
		t.Fatalf("gone share polled again: %v", f.polls)
	}
}
