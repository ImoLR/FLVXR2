package repo

import (
	"database/sql"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func landingFlowFixture(t *testing.T, dialect string) (*Repository, time.Time) {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	orphanSeed(t, r, &model.User{ID: 2, User: "landing-flow", Status: 1, RoleID: 0, Flow: 100, FlowResetTime: 15},
		&model.Node{ID: 2, Name: "landing-host", DefaultUserQuotaBytes: 1000, LandingCapable: true},
		&model.LandingServer{ID: 1, NodeID: 2, Source: "native", Name: "landing", PublicHost: "203.0.113.2", FlowType: 2, TrafficRatio: 1, Status: "active"},
		&model.LandingNode{ID: 1, UserID: 2, LandingServerID: 1, Name: "one", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"})
	return r, now
}
func landingSnapshot(epoch string, up, down int64) LandingCounter {
	return LandingCounter{ID: 1, NodeID: 2, InboundTag: "flvxl-1", Epoch: epoch, UpBytes: up, DownBytes: down}
}
func assertLandingUser(t *testing.T, r *Repository, in, out int64) {
	t.Helper()
	u, e := r.GetUserByID(2)
	if e != nil || u.InFlow != in || u.OutFlow != out {
		t.Fatalf("user=%+v err=%v want %d/%d", u, e, in, out)
	}
}

func TestLandingCumulativeFlowAtomicEpoch(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, now := landingFlowFixture(t, dialect)
			ingest := func(c LandingCounter) {
				t.Helper()
				if _, e := r.ApplyFlowBatch(FlowBatch{LandingCounters: []LandingCounter{c}}, now); e != nil {
					t.Fatal(e)
				}
			}
			ingest(landingSnapshot("100", 100, 200))
			assertLandingUser(t, r, 200, 400)
			ingest(landingSnapshot("100", 100, 200))
			assertLandingUser(t, r, 200, 400)
			ingest(landingSnapshot("100", 90, 199))
			assertLandingUser(t, r, 200, 400)
			ingest(landingSnapshot("100", 150, 300))
			assertLandingUser(t, r, 300, 600)
			ingest(landingSnapshot("101", 10, 20))
			assertLandingUser(t, r, 320, 640)
			ingest(landingSnapshot("100", 170, 400))
			assertLandingUser(t, r, 320, 640)
			// A forged final snapshot rolls the complete batch back, including valid earlier rows.
			forged := landingSnapshot("101", 50, 50)
			forged.NodeID = 3
			if _, e := r.ApplyFlowBatch(FlowBatch{LandingCounters: []LandingCounter{landingSnapshot("101", 20, 30), forged}}, now); e == nil {
				t.Fatal("forged counter accepted")
			}
			assertLandingUser(t, r, 320, 640)
			n, _ := r.GetLandingNode(1)
			if n.LastSeenUp != 10 || n.LastSeenDown != 20 {
				t.Fatal("counter cursor escaped rollback")
			}
			var q model.UserServerQuota
			if e := r.db.Where("user_id = 2 AND server_ref_id = 2").First(&q).Error; e != nil {
				t.Fatal(e)
			}
			if q.UsedIn != 320 || q.UsedOut != 640 || q.LimitBytes != 1000 || q.Paused {
				t.Fatalf("quota %+v", q)
			}
			ingest(landingSnapshot("101", 30, 30))
			if e := r.db.First(&q, q.ID).Error; e != nil {
				t.Fatal(e)
			}
			if !q.Paused {
				t.Fatal("server quota not paused")
			}
		})
	}
}

func TestLandingForwardServerAttribution(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := landingFlowFixture(t, dialect)
			for _, c := range []model.ChainTunnel{{TunnelID: 10, ChainType: "1", NodeID: 3}, {TunnelID: 10, ChainType: "1", NodeID: 4}, {TunnelID: 10, ChainType: "2", NodeID: 5, Inx: sql.NullInt64{Int64: 0, Valid: true}}, {TunnelID: 10, ChainType: "2", NodeID: 6, Inx: sql.NullInt64{Int64: 0, Valid: true}}, {TunnelID: 10, ChainType: "2", NodeID: 7, Inx: sql.NullInt64{Int64: 1, Valid: true}}, {TunnelID: 10, ChainType: "3", NodeID: 8}, {TunnelID: 10, ChainType: "3", NodeID: 9}} {
				v := c
				orphanSeed(t, r, &v)
			}
			w, e := r.ForwardServerWeights(10, 4)
			if e != nil {
				t.Fatal(e)
			}
			want := map[int64]float64{4: 1, 5: .5, 6: .5, 7: 1, 8: .5, 9: .5}
			if len(w) != len(want) {
				t.Fatalf("weights %v", w)
			}
			for id, v := range want {
				if w[id] != v {
					t.Fatalf("weights %v", w)
				}
			}
		})
	}
}

func TestLandingQuotaExpiryLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, now := landingFlowFixture(t, dialect)
			reconcile := func(at time.Time) {
				t.Helper()
				if _, e := r.ReconcileLandingPolicy(at); e != nil {
					t.Fatal(e)
				}
			}
			status := func(want string) {
				t.Helper()
				n, _ := r.GetLandingNode(1)
				if n == nil || n.Status != want {
					t.Fatalf("node %+v want %s", n, want)
				}
			}
			if _, e := r.ApplyFlowBatch(FlowBatch{LandingCounters: []LandingCounter{landingSnapshot("100", 300, 300)}}, now); e != nil {
				t.Fatal(e)
			}
			reconcile(now)
			status("paused_server_quota")
			r.db.Model(&model.UserServerQuota{}).Where("user_id = 2").Updates(map[string]interface{}{"limit_override": true, "limit_bytes": 2000})
			reconcile(now)
			status("active")
			r.db.Model(&model.LandingNode{}).Where("id = 1").Update("traffic_limit_bytes", 1000)
			reconcile(now)
			status("paused_quota")
			reconcile(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
			status("active")
			n, _ := r.GetLandingNode(1)
			if n.BilledIn+n.BilledOut != 0 || n.LastSeenUp != 300 || n.UpBytes != 300 {
				t.Fatal("period reset lost cumulative cursor or lifetime total")
			}
			r.db.Model(&model.User{}).Where("id = 2").Update("status", 0)
			reconcile(now)
			status("disabled")
			r.db.Model(&model.User{}).Where("id = 2").Update("status", 1)
			reconcile(now)
			status("active")
			exp := now.Add(time.Hour).UnixMilli()
			r.db.Model(&model.User{}).Where("id = 2").Update("exp_time", exp)
			reconcile(now.Add(2 * time.Hour))
			status("paused_expired")
			n, _ = r.GetLandingNode(1)
			oldPort, oldTag := n.Port, n.InboundTag
			r.db.Model(&model.User{}).Where("id = 2").Update("exp_time", now.Add(30*24*time.Hour).UnixMilli())
			reconcile(now.Add(24 * time.Hour))
			status("active")
			n, _ = r.GetLandingNode(1)
			if n.Port != oldPort || n.InboundTag != oldTag || n.ExpiredAt != nil {
				t.Fatal("renewal replaced link")
			}
			fixed := now.Add(2 * time.Hour).UnixMilli()
			r.db.Model(&model.LandingNode{}).Where("id = 1").Updates(map[string]interface{}{"expiry_mode": "fixed", "expires_at": fixed})
			reconcile(now.Add(3 * time.Hour))
			status("paused_expired")
			reconcile(now.Add(8 * 24 * time.Hour))
			n, _ = r.GetLandingNode(1)
			if n != nil {
				t.Fatal("grace-expired node not deleted")
			}
		})
	}
}

func TestLandingEffectiveExpiryAndResetDay(t *testing.T) {
	user := model.User{ExpTime: 100}
	node := model.LandingNode{ExpiryMode: "fixed"}
	server := model.LandingServer{}
	fixed, available := int64(90), int64(80)
	node.ExpiresAt = &fixed
	server.AvailableUntil = &available
	if EffectiveLandingExpiry(&node, &server, &user) != 80 {
		t.Fatal("expiry min")
	}
	node.ExpiryMode = "follow_user"
	server.AvailableUntil = nil
	if EffectiveLandingExpiry(&node, &server, &user) != 100 {
		t.Fatal("follow_user")
	}
	for _, c := range []struct {
		date     string
		day, key int64
	}{{"2026-02-27", 31, 20260131}, {"2026-02-28", 31, 20260228}, {"2026-03-01", 31, 20260228}, {"2026-10-14", 15, 20260915}, {"2026-10-15", 15, 20261015}, {"2026-10-15", 0, 1}} {
		at, _ := time.Parse("2006-01-02", c.date)
		if got := LandingPeriodKey(c.day, at); got != c.key {
			t.Fatalf("%+v got %d", c, got)
		}
	}
}
