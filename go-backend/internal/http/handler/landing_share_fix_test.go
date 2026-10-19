package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// fakeCustom serves one share (id 7). desiredCode != 0 makes PUT desired fail
// with that status and error code.
type fakeCustom struct {
	core        string
	collectedAt int64
	desiredCode int
	errCode     string
	pushes      int
	last        landingcustom.Desired
}

func (f *fakeCustom) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case landingcustom.Prefix + "/shares":
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{{ID: 7, Name: "Boil Hinet", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}, Core: landingcustom.Core{State: f.core}}}})
		case landingcustom.Prefix + "/landing-hosts":
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			f.pushes++
			if f.desiredCode != 0 {
				w.WriteHeader(f.desiredCode)
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": f.errCode, "message": "remote text must not leak"}})
				return
			}
			json.NewDecoder(r.Body).Decode(&f.last)
			json.NewEncoder(w).Encode(map[string]any{"accepted_revision": f.last.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			json.NewEncoder(w).Encode(map[string]any{"applied_revision": 0, "collected_at": f.collectedAt, "core": map[string]any{"state": f.core}, "nodes": []any{}, "share_traffic_used_bytes": 0})
		default:
			t.Errorf("unexpected Custom path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}
}

func sharedServer(t *testing.T, e *flowTestEnv) model.LandingServer {
	t.Helper()
	var s model.LandingServer
	if err := e.r.DB().Where("source = 'shared'").First(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

// Plan 110: a background failure stores the specific cause; while backing off
// the cause is kept (not a bare "正在退避重试"); an explicit action (node
// create/update, 立即同步) makes one real attempt despite the backoff.
func TestSharedSyncSpecificErrorsAndManualBypass(t *testing.T) {
	e := newFlowTestEnv(t)
	f := &fakeCustom{core: "running", collectedAt: time.Now().UnixMilli(), desiredCode: 409, errCode: "host_resolution"}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	e.h.syncSharedIntegration(context.Background(), false)
	s := sharedServer(t, e)
	if f.pushes != 1 || !strings.Contains(s.SharedSyncError, "DNS") || strings.Contains(s.SharedSyncError, "remote text") {
		t.Fatalf("first failure: pushes=%d err=%q", f.pushes, s.SharedSyncError)
	}
	// background tick inside the backoff: no request, real cause retained
	e.h.syncSharedIntegration(context.Background(), false)
	s = sharedServer(t, e)
	if f.pushes != 1 {
		t.Fatalf("backoff not honoured by background tick: %d", f.pushes)
	}
	if !strings.Contains(s.SharedSyncError, "DNS") || !strings.Contains(s.SharedSyncError, "自动重试中") || strings.HasPrefix(s.SharedSyncError, "同步异常，正在退避重试") {
		t.Fatalf("backoff hid the cause: %q", s.SharedSyncError)
	}
	// Custom recovers; an explicit action pushes immediately despite the backoff
	f.desiredCode = 0
	if err := e.h.syncSharedLandingServer(s.ID); err != nil {
		t.Fatalf("manual push in backoff: %v", err)
	}
	s = sharedServer(t, e)
	if f.pushes != 2 || s.SharedPushedRevision != s.Revision || s.SharedSyncError != "" {
		t.Fatalf("manual push: pushes=%d pushed=%d rev=%d err=%q", f.pushes, s.SharedPushedRevision, s.Revision, s.SharedSyncError)
	}
	// 立即同步 (force) also bypasses a fresh backoff
	f.desiredCode, f.errCode = 409, "official_ports_unknown"
	e.r.DB().Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("shared_last_push_at", time.Now().Add(-11*time.Minute).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if f.pushes != 3 {
		t.Fatalf("refresh push not attempted: %d", f.pushes)
	}
	f.desiredCode = 0
	e.h.syncSharedIntegration(context.Background(), true)
	cfg, _ := e.r.GetLandingIntegration()
	if f.pushes != 4 || cfg.SyncError != "" {
		t.Fatalf("立即同步 in backoff: pushes=%d err=%q", f.pushes, cfg.SyncError)
	}
	// A transport-level failure trips the shared breaker: explicit actions do
	// not hammer an unavailable Custom, but report the real cause.
	f.desiredCode, f.errCode = 503, "unavailable"
	e.r.DB().Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("shared_last_push_at", time.Now().Add(-11*time.Minute).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if f.pushes != 5 {
		t.Fatalf("503 push not attempted: %d", f.pushes)
	}
	err := e.h.syncSharedLandingServer(s.ID)
	if f.pushes != 5 || err == nil || !strings.Contains(err.Error(), "暂不可用") || !strings.Contains(err.Error(), "自动重试中") {
		t.Fatalf("breaker: pushes=%d err=%v", f.pushes, err)
	}
}

// Plan 110: a share whose mmwx side is not ready explains why on the server
// card and on pending node rows; unknown states are shown, not rejected.
func TestSharedNotReadyReasons(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	f := &fakeCustom{core: "stopped", collectedAt: 0}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	e.h.syncSharedIntegration(context.Background(), true)
	s := sharedServer(t, e)
	if v := e.h.landingServerView(s, true); v["sharedNotice"] != "" {
		t.Fatalf("idle stopped share flagged: %v", v["sharedNotice"])
	}
	n, err := e.r.CreateLandingNode(2, s.ID, "pending", "ss2022", true)
	if err == nil {
		t.Fatal("ungranted owner accepted")
	}
	grant := []int64{s.ID}
	if err := e.r.SaveLandingUserSettingWithGrants(2, 5, nil, 0, &grant); err != nil {
		t.Fatal(err)
	}
	if n, err = e.r.CreateLandingNode(2, s.ID, "pending", "ss2022", true); err != nil {
		t.Fatal(err)
	}
	e.h.syncSharedIntegration(context.Background(), true)
	s = sharedServer(t, e)
	v := e.h.landingServerView(s, true)
	if notice, _ := v["sharedNotice"].(string); !strings.Contains(notice, "核心未运行") || !strings.Contains(notice, "Helper") {
		t.Fatalf("never-reported core: %v", v["sharedNotice"])
	}
	nv := e.h.landingNodeView(*n)
	if nv["status"] != "pending" || !strings.Contains(nv["pendingReason"].(string), "核心未运行") {
		t.Fatalf("node row reason: %v %v", nv["status"], nv["pendingReason"])
	}
	for state, want := range map[string]string{"helper_outdated": "Helper 需升级", "unsupported": "Helper 需升级", "brand_new": "brand_new", "error": "核心异常"} {
		f.core = state
		f.collectedAt = time.Now().UnixMilli()
		e.h.syncSharedIntegration(context.Background(), true)
		s = sharedServer(t, e)
		if notice, _ := e.h.landingServerView(s, true)["sharedNotice"].(string); !strings.Contains(notice, want) {
			t.Fatalf("state %s → %q", state, notice)
		}
	}
	f.core = "running"
	e.h.syncSharedIntegration(context.Background(), true)
	s = sharedServer(t, e)
	if v := e.h.landingServerView(s, true); v["sharedNotice"] != "" {
		t.Fatalf("running core flagged: %v", v["sharedNotice"])
	}
	if nv := e.h.landingNodeView(*n); nv["pendingReason"] != nil {
		t.Fatalf("running core still explains pending: %v", nv["pendingReason"])
	}
}
