package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

const mb = int64(1 << 20)

func nativeCounter(id, epoch, up, down int64, conns int) repo.LandingCounter {
	return repo.LandingCounter{ID: id, InboundTag: fmt.Sprintf("flvxl-%d", id), Epoch: fmt.Sprint(epoch), UpBytes: up, DownBytes: down, Connections: conns, NodeID: 2}
}

func sharedCounter(id, collectedAt, up, down int64, conns int) repo.LandingCounter {
	return repo.LandingCounter{ID: id, InboundTag: fmt.Sprintf("flvxl-%d", id), Epoch: "1", UpBytes: up, DownBytes: down, Connections: conns, ServerID: 7, CollectedAt: collectedAt}
}

func wantRate(t *testing.T, got landingRate, ok bool, up, down float64) {
	t.Helper()
	if got.RateOK != ok || (ok && (got.UpRate != up || got.DownRate != down)) {
		t.Fatalf("rate = %+v, want ok=%v up=%v down=%v", got, ok, up, down)
	}
	if got.UpRate < 0 || got.DownRate < 0 {
		t.Fatalf("negative rate %+v", got)
	}
}

// Native counters: rate over the two newest samples; a Core restart (new epoch)
// counts the bytes since the restart; an older epoch is ignored; a decrease
// inside one epoch never yields a rate (and never a negative one).
func TestLandingRateTrackerNativeCounters(t *testing.T) {
	tr := newLandingRateTracker(10)
	base := int64(1_700_000_000_000)
	tr.observe([]repo.LandingCounter{nativeCounter(1, 100, 0, 0, 1)}, base)
	if r := tr.snapshot(1, base); r.RateOK || r.SampleAt != base || r.Connections != 1 {
		t.Fatalf("first sample has a rate: %+v", r)
	}
	tr.observe([]repo.LandingCounter{nativeCounter(1, 100, 5*mb, 10*mb, 4)}, base+5000)
	wantRate(t, tr.snapshot(1, base+5000), true, float64(mb), float64(2*mb))

	// Core restarted: counters dropped to near zero under a new epoch.
	tr.observe([]repo.LandingCounter{nativeCounter(1, 200, 1000, 2000, 2)}, base+10000)
	wantRate(t, tr.snapshot(1, base+10000), true, 200, 400)

	// A late report from the old Core is ignored entirely.
	tr.observe([]repo.LandingCounter{nativeCounter(1, 100, 99*mb, 99*mb, 9)}, base+15000)
	if r := tr.snapshot(1, base+15000); r.SampleAt != base+10000 || r.Connections != 2 {
		t.Fatalf("old epoch sample accepted: %+v", r)
	}

	// Same epoch but lower counters: not trusted, no rate.
	tr.observe([]repo.LandingCounter{nativeCounter(1, 200, 500, 3000, 2)}, base+20000)
	wantRate(t, tr.snapshot(1, base+20000), false, 0, 0)

	// Invalid epochs are skipped.
	tr.observe([]repo.LandingCounter{nativeCounter(2, 0, 1, 1, 1), {ID: 3, Epoch: "x", NodeID: 2}}, base)
	if tr.size() != 1 {
		t.Fatalf("invalid epoch recorded, size %d", tr.size())
	}
}

// Shared lifetime counters are timed by the Helper's collection time. A
// decrease of one direction is a reset of that direction; repeated or older
// collections are ignored.
func TestLandingRateTrackerSharedCounters(t *testing.T) {
	tr := newLandingRateTracker(10)
	base := int64(1_700_000_000_000)
	now := base + 90_000
	tr.observe([]repo.LandingCounter{sharedCounter(5, base, 100*mb, 200*mb, 3)}, now)
	tr.observe([]repo.LandingCounter{sharedCounter(5, base+60_000, 160*mb, 230*mb, 5)}, now)
	wantRate(t, tr.snapshot(5, now), true, float64(mb), float64(mb)/2)
	if r := tr.snapshot(5, now); r.SampleAt != base+60_000 || r.Connections != 5 {
		t.Fatalf("shared sample time/conns %+v", r)
	}
	// The same collection polled again changes nothing.
	tr.observe([]repo.LandingCounter{sharedCounter(5, base+60_000, 999*mb, 999*mb, 9)}, now+60_000)
	wantRate(t, tr.snapshot(5, now), true, float64(mb), float64(mb)/2)
	// Upload counter reset on the Helper side: the new value is the delta.
	tr.observe([]repo.LandingCounter{sharedCounter(5, base+120_000, 6*mb, 290*mb, 5)}, now+60_000)
	wantRate(t, tr.snapshot(5, base+120_000), true, float64(6*mb)/60, float64(mb))
	if r := tr.snapshot(5, base+120_000); r.UpRate < 0 || r.DownRate < 0 {
		t.Fatalf("negative shared rate %+v", r)
	}
}

// A near-duplicate report (< 1 s) keeps the older base, so a tiny interval
// cannot produce a spike; old samples lose their rate but keep their age.
func TestLandingRateTrackerMinGapAndStaleness(t *testing.T) {
	tr := newLandingRateTracker(10)
	base := int64(1_700_000_000_000)
	tr.observe([]repo.LandingCounter{nativeCounter(1, 1, 0, 0, 0)}, base)
	tr.observe([]repo.LandingCounter{nativeCounter(1, 1, 10*mb, 10*mb, 0)}, base+10_000)
	tr.observe([]repo.LandingCounter{nativeCounter(1, 1, 11*mb, 11*mb, 0)}, base+10_050)
	// base stays at t=0: 11 MiB over 10.05 s, not 1 MiB over 50 ms
	r := tr.snapshot(1, base+10_050)
	if !r.RateOK || r.UpRate > float64(2*mb) || r.SampleAt != base+10_050 {
		t.Fatalf("min gap spike: %+v", r)
	}
	stale := tr.snapshot(1, base+10_050+landingRateStaleAfterMs+1)
	if stale.RateOK || stale.SampleAt != base+10_050 {
		t.Fatalf("stale sample still has a rate: %+v", stale)
	}
}

func TestLandingRateTrackerEviction(t *testing.T) {
	tr := newLandingRateTracker(3)
	base := int64(1_700_000_000_000)
	for i := int64(1); i <= 3; i++ {
		tr.observe([]repo.LandingCounter{nativeCounter(i, 1, 0, 0, 0)}, base+i)
	}
	// Full: the entry with the oldest sample (1) makes room for 4.
	tr.observe([]repo.LandingCounter{nativeCounter(4, 1, 0, 0, 0)}, base+10)
	if tr.size() != 3 || tr.snapshot(1, base).SampleAt != 0 || tr.snapshot(4, base).SampleAt == 0 {
		t.Fatalf("cap eviction wrong, size %d", tr.size())
	}
	tr.forget(2)
	if tr.size() != 2 || tr.snapshot(2, base).SampleAt != 0 {
		t.Fatal("forget kept the entry")
	}
	// retain drops deleted nodes and entries older than the TTL.
	tr.observe([]repo.LandingCounter{nativeCounter(5, 1, 0, 0, 0)}, base+landingRateTTLMs)
	tr.retain([]int64{3, 4, 5}, base+landingRateTTLMs+11)
	if tr.size() != 1 || tr.snapshot(5, base).SampleAt == 0 {
		t.Fatalf("retain kept stale/deleted entries, size %d", tr.size())
	}
	tr.retain(nil, base)
	if tr.size() != 0 {
		t.Fatal("retain(nil) kept entries")
	}
	var nilTracker *landingRateTracker
	nilTracker.observe([]repo.LandingCounter{nativeCounter(1, 1, 0, 0, 0)}, base)
	nilTracker.forget(1)
	nilTracker.retain(nil, base)
	if nilTracker.size() != 0 || len(nilTracker.snapshotMany([]int64{1}, base)) != 0 {
		t.Fatal("nil tracker misbehaves")
	}
}

// The flow commit is the only feed: a successful native counter commit lands in
// the tracker without any extra DB write; the reconcile sweep forgets deleted nodes.
func TestLandingRateTrackerFedByFlowCommit(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	commit := func(up, down int64, conns int) {
		t.Helper()
		if _, err := e.h.commitFlowWithLanding(2, nil, []repo.LandingCounter{{ID: 1, InboundTag: "flvxl-1", Epoch: "1000", UpBytes: up, DownBytes: down, Connections: conns}}); err != nil {
			t.Fatal(err)
		}
	}
	commit(0, 0, 1)
	if r := e.h.landingRateTracker().snapshot(1, time.Now().UnixMilli()); r.SampleAt == 0 || r.Connections != 1 || r.RateOK {
		t.Fatalf("first commit not tracked: %+v", r)
	}
	time.Sleep(1100 * time.Millisecond)
	commit(2*mb, 4*mb, 3)
	r := e.h.landingRateTracker().snapshot(1, time.Now().UnixMilli())
	if !r.RateOK || r.UpRate <= 0 || r.DownRate <= r.UpRate || r.Connections != 3 {
		t.Fatalf("second commit rate: %+v", r)
	}
	// A failed commit (ownership mismatch) is not observed.
	if _, err := e.h.commitFlowWithLanding(99, nil, []repo.LandingCounter{{ID: 1, InboundTag: "flvxl-1", Epoch: "2000", UpBytes: 1, DownBytes: 1}}); err == nil {
		t.Fatal("foreign agent counter accepted")
	}
	if got := e.h.landingRateTracker().snapshot(1, time.Now().UnixMilli()); got.Connections != 3 {
		t.Fatalf("failed commit observed: %+v", got)
	}
	e.exec("DELETE FROM landing_node WHERE id = 1")
	ids, err := e.r.ListLandingNodeIDs()
	if err != nil {
		t.Fatal(err)
	}
	e.h.landingRateTracker().retain(ids, time.Now().UnixMilli())
	if e.h.landingRateTracker().size() != 0 {
		t.Fatal("deleted node still tracked")
	}
}

// landingMonitorRepo opens SQLite or (with FLVX_POSTGRES_TEST_DSN) a fresh
// PostgreSQL schema.
func landingMonitorRepo(t *testing.T, dialect string) *repo.Repository {
	t.Helper()
	if dialect != "postgres" {
		r, err := repo.Open(filepath.Join(t.TempDir(), "monitor.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		return r
	}
	dsn := os.Getenv("FLVX_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run postgres tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("landing_monitor_%d", time.Now().UnixNano())
	if _, err = admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	r, err := repo.OpenPostgres(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func callLandingMonitor(t *testing.T, h *Handler, userID int64, role int) (int, landingMonitorView, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/landing/monitor", strings.NewReader(`{}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: fmt.Sprint(userID), RoleID: role}))
	w := httptest.NewRecorder()
	h.landingMonitor(w, req)
	var env struct {
		Code int                `json:"code"`
		Msg  string             `json:"msg"`
		Data landingMonitorView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Code, env.Data, w.Body.String()
}

func TestLandingMonitorEndpointVisibilityAndRates(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingMonitorRepo(t, dialect)
			h := &Handler{repo: r}
			now := time.Now()
			future := now.Add(30 * 24 * time.Hour).UnixMilli()
			seed := []interface{}{
				&model.User{ID: 21, User: "alice", RoleID: 1, Status: 1, ExpTime: future, FlowResetTime: 1},
				&model.User{ID: 22, User: "bob", RoleID: 1, Status: 1, ExpTime: future, FlowResetTime: 1},
				&model.User{ID: 23, User: "carol-ungranted", RoleID: 1, Status: 1, ExpTime: future, FlowResetTime: 1},
				&model.User{ID: 24, User: "dave-no-monitor", RoleID: 1, Status: 1, ExpTime: future, FlowResetTime: 1},
				&model.Node{ID: 601, Name: "JP", ServerIP: "203.0.113.1", Port: "10000-19999", LandingCapable: true, Status: 1},
				&model.UserGroup{ID: 701, Name: "landing", Status: 1},
				&model.UserGroupUser{UserGroupID: 701, UserID: 21},
				&model.UserGroupUser{UserGroupID: 701, UserID: 22},
				&model.UserGroupUser{UserGroupID: 701, UserID: 24},
			}
			for _, row := range seed {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			native, err := r.EnableLandingServer(601)
			if err != nil {
				t.Fatal(err)
			}
			native.MaxConnections = 300
			if err = r.SaveLandingServer(native, []int64{701}); err != nil {
				t.Fatal(err)
			}
			shared := &model.LandingServer{Source: "shared", RemoteServerID: 7, Name: "妙妙屋 US", PublicHost: "198.51.100.7", PortMin: 30000, PortMax: 30100, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022"]`, Status: "active", SharedLastSyncAt: now.UnixMilli(), SharedCollectedAt: now.Add(-90 * time.Second).UnixMilli(), DefaultMaxConnections: 100, DefaultTrafficLimitBytes: 10 << 30, CreatedTime: 1, UpdatedTime: 1, SharedCaps: `{"server_id":7,"status":"active","port_min":30000,"port_max":30100,"protocols":["ss2022"],"max_connections":500,"traffic_limit_bytes":107374182400,"traffic_used_bytes":1073741824,"core":{"state":"running"}}`}
			if err = r.DB().Create(shared).Error; err != nil {
				t.Fatal(err)
			}
			if err = r.DB().Create(&model.LandingServerUser{LandingServerID: shared.ID, UserID: 21, CreatedTime: 1}).Error; err != nil {
				t.Fatal(err)
			}
			for _, uid := range []int64{21, 22, 23} {
				if err = r.InsertMonitorPermission(uid, now.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			alice, err := r.CreateLandingNode(21, native.ID, "alice-jp", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			bob, err := r.CreateLandingNode(22, native.ID, "bob-jp", "vless_reality_vision", false)
			if err != nil {
				t.Fatal(err)
			}
			aliceShared, err := r.CreateLandingNode(21, shared.ID, "alice-us", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.DB().Model(&model.LandingNode{}).Where("id = ?", alice.ID).Updates(map[string]any{"connections": 4, "billed_in": 1 << 30, "billed_out": 1 << 30}).Error; err != nil {
				t.Fatal(err)
			}
			if err = r.DB().Model(&model.LandingNode{}).Where("id = ?", bob.ID).Update("connections", 7).Error; err != nil {
				t.Fatal(err)
			}
			if err = r.DB().Model(&model.LandingNode{}).Where("id = ?", aliceShared.ID).Updates(map[string]any{"connections": 2, "remote_state": "active"}).Error; err != nil {
				t.Fatal(err)
			}
			ms := now.UnixMilli()
			tr := h.landingRateTracker()
			tr.observe([]repo.LandingCounter{nativeCounter(alice.ID, 1, 0, 0, 4), nativeCounter(bob.ID, 1, 0, 0, 7)}, ms-10_000)
			tr.observe([]repo.LandingCounter{nativeCounter(alice.ID, 1, 10*mb, 20*mb, 4), nativeCounter(bob.ID, 1, 50*mb, 50*mb, 7)}, ms-5_000)
			tr.observe([]repo.LandingCounter{sharedCounter(aliceShared.ID, ms-150_000, 0, 0, 2)}, ms)
			tr.observe([]repo.LandingCounter{sharedCounter(aliceShared.ID, ms-90_000, 60*mb, 6*mb, 2)}, ms)

			// Admin: everything, owners, caps, server sums over all users.
			code, admin, raw := callLandingMonitor(t, h, 1, 0)
			if code != 0 || !admin.Admin || len(admin.Servers) != 2 {
				t.Fatalf("admin response %s", raw)
			}
			jp := admin.Servers[0]
			if jp.ID != native.ID || len(jp.Nodes) != 2 || jp.Connections != 11 || jp.MaxConnections != 300 || !jp.Online || jp.UpRate == nil || *jp.UpRate != float64(12*mb) || *jp.DownRate != float64(14*mb) {
				t.Fatalf("admin native server %+v", jp)
			}
			if jp.Nodes[0].UserName != "alice" || jp.Nodes[1].UserName != "bob" || jp.Nodes[0].UpRate == nil || *jp.Nodes[0].UpRate != float64(2*mb) {
				t.Fatalf("admin nodes %+v", jp.Nodes)
			}
			us := admin.Servers[1]
			if us.Source != "shared" || us.MaxConnections != 500 || us.ShareLimitBytes != 100<<30 || us.ShareUsedBytes != 1<<30 || !us.Online || us.CoreState != "running" || us.SampleAt != ms-90_000 {
				t.Fatalf("admin shared server %+v", us)
			}
			if us.Nodes[0].UpRate == nil || *us.Nodes[0].UpRate != float64(mb) || us.Nodes[0].SampleAt != ms-90_000 {
				t.Fatalf("shared node rate %+v", us.Nodes[0])
			}

			// Alice (group + direct grant): both servers, only her nodes, sums of hers only.
			code, view, raw := callLandingMonitor(t, h, 21, 1)
			if code != 0 || view.Admin || len(view.Servers) != 2 {
				t.Fatalf("alice response %s", raw)
			}
			jp = view.Servers[0]
			if len(jp.Nodes) != 1 || jp.Nodes[0].ID != alice.ID || jp.Connections != 4 || *jp.UpRate != float64(2*mb) || jp.MaxConnections != 0 || jp.BilledBytes != 2<<30 {
				t.Fatalf("alice native view %+v", jp)
			}
			if strings.Contains(raw, "bob") || strings.Contains(raw, `"userName"`) || strings.Contains(raw, "shareLimitBytes") {
				t.Fatalf("other users' data leaked to alice: %s", raw)
			}
			if view.Servers[1].Nodes[0].ID != aliceShared.ID || view.Servers[1].MaxConnections != 0 {
				t.Fatalf("alice shared view %+v", view.Servers[1])
			}

			// Bob: group only → native server only, his node only.
			_, view, raw = callLandingMonitor(t, h, 22, 1)
			if len(view.Servers) != 1 || len(view.Servers[0].Nodes) != 1 || view.Servers[0].Nodes[0].ID != bob.ID || view.Servers[0].Connections != 7 || strings.Contains(raw, "alice") {
				t.Fatalf("bob view %s", raw)
			}

			// Carol: monitor permission but no landing grant → nothing.
			code, view, raw = callLandingMonitor(t, h, 23, 1)
			if code != 0 || len(view.Servers) != 0 {
				t.Fatalf("ungranted view %s", raw)
			}

			// Dave: landing grant but no monitor permission → 403 like the other monitor endpoints.
			if code, _, raw = callLandingMonitor(t, h, 24, 1); code != 403 {
				t.Fatalf("no monitor permission: %s", raw)
			}
		})
	}
}
