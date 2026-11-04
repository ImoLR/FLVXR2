package repo

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 212 rollup: traffic_daily must move exactly with the billing counters.

func dailySum(t *testing.T, r *Repository, userID int64, kind string, ref int64, day int64) (int64, int64) {
	t.Helper()
	var row struct{ In, Out int64 }
	q := r.db.Model(&model.TrafficDaily{}).Select("COALESCE(SUM(in_bytes),0) AS \"in\", COALESCE(SUM(out_bytes),0) AS \"out\"").
		Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", userID, kind, ref)
	if day > 0 {
		q = q.Where("day = ?", day)
	}
	if err := q.Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.In, row.Out
}

func dailyRows(t *testing.T, r *Repository) int64 {
	t.Helper()
	var n int64
	if err := r.db.Model(&model.TrafficDaily{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrafficDailyFollowsBillingExactly(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, now := landingFlowFixture(t, dialect)
			orphanSeed(t, r, &model.Node{ID: 7, Name: "entry"}, &model.User{ID: 3, User: "other", Status: 1, RoleID: 1, Flow: 100, FlowResetTime: 1})
			apply := func(b FlowBatch, at time.Time) {
				t.Helper()
				if _, err := r.ApplyFlowBatch(b, at); err != nil {
					t.Fatal(err)
				}
			}
			// The rollup must equal what the counters received since the start.
			check := func(wantUserIn, wantUserOut int64) {
				t.Helper()
				in, out := dailySum(t, r, 2, TrafficKindTotal, 0, 0)
				if in != wantUserIn || out != wantUserOut {
					t.Fatalf("daily total %d/%d want %d/%d", in, out, wantUserIn, wantUserOut)
				}
			}
			entry := ServerQuotaKey{2, "node", 7}
			// 1) a forward flow: user total + entry server.
			apply(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 100, Out: 300}}, QuotaUsage: map[int64]int64{2: 400},
				ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{entry: {In: 100, Out: 300}}}, now)
			assertLandingUser(t, r, 100, 300)
			check(100, 300)
			// 2) landing Core counters (incl. a duplicate snapshot and an epoch restart).
			for _, c := range []LandingCounter{landingSnapshot("100", 100, 200), landingSnapshot("100", 100, 200), landingSnapshot("100", 150, 300), landingSnapshot("101", 10, 20)} {
				apply(FlowBatch{LandingCounters: []LandingCounter{c}}, now)
			}
			// Landing billed: flow type 2 ×1 → (150+10)×2 / (300+20)×2.
			assertLandingUser(t, r, 100+320, 300+640)
			check(420, 940)
			if in, out := dailySum(t, r, 2, "node", 2, 0); in != 320 || out != 640 {
				t.Fatalf("landing server rows %d/%d", in, out)
			}
			var q model.UserServerQuota
			if err := r.db.Where("user_id = 2 AND server_kind = 'node' AND server_ref_id = 2").First(&q).Error; err != nil {
				t.Fatal(err)
			}
			if q.UsedIn != 320 || q.UsedOut != 640 {
				t.Fatalf("quota %+v", q)
			}
			if in, out := dailySum(t, r, 2, "node", 7, 0); in != 100 || out != 300 {
				t.Fatalf("entry rows %d/%d", in, out)
			}
			// 3) a rejected upload (negative server delta) writes nothing, rollup included.
			before := dailyRows(t, r)
			if _, err := r.ApplyFlowBatch(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 5, Out: 5}},
				ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{entry: {In: -1, Out: 0}}}, now); err == nil {
				t.Fatal("negative delta accepted")
			}
			check(420, 940)
			assertLandingUser(t, r, 420, 940)
			if dailyRows(t, r) != before {
				t.Fatal("rollup escaped the rollback")
			}
			// 4) monthly user flow reset + server period roll: counters restart, the rollup keeps
			// adding only the new deltas (no snapshot diff, nothing double counted or lost).
			if err := r.db.Model(&model.User{}).Where("id = 2").Updates(map[string]interface{}{"in_flow": 0, "out_flow": 0}).Error; err != nil {
				t.Fatal(err)
			}
			later := now.AddDate(0, 1, 0)
			apply(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 7, Out: 9}}, ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{entry: {In: 7, Out: 9}}}, later)
			assertLandingUser(t, r, 7, 9)
			check(427, 949)
			if in, out := dailySum(t, r, 2, TrafficKindTotal, 0, TrafficDayKey(later)); in != 7 || out != 9 {
				t.Fatalf("new day %d/%d", in, out)
			}
			q = model.UserServerQuota{}
			if err := r.db.Where("user_id = 2 AND server_kind = 'node' AND server_ref_id = 7").First(&q).Error; err != nil {
				t.Fatal(err)
			}
			if q.UsedIn != 7 || q.UsedOut != 9 {
				t.Fatalf("entry quota after period roll %+v", q)
			}
			// 5) zero / negative / unknown-kind entries are skipped, other users kept apart.
			apply(FlowBatch{Users: map[int64]FlowCounterDelta{3: {In: 1, Out: 2}, 0: {In: 5, Out: 5}, 4: {}},
				ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{{3, "node", 7}: {In: 1, Out: 2}}}, later)
			// (applyServerQuotasTx refuses unknown kinds; the rollup skips them on its own too.)
			if err := addTrafficDailyTx(r.db, nil, map[ServerQuotaKey]FlowCounterDelta{{3, "bogus", 7}: {In: 1, Out: 1}, {3, "node", 0}: {In: 1}}, later); err != nil {
				t.Fatal(err)
			}
			if in, out := dailySum(t, r, 3, TrafficKindTotal, 0, 0); in != 1 || out != 2 {
				t.Fatalf("user 3 %d/%d", in, out)
			}
			var bogus int64
			r.db.Model(&model.TrafficDaily{}).Where("server_kind = 'bogus' OR user_id IN (0, 4) OR (server_kind = 'node' AND server_ref_id = 0)").Count(&bogus)
			if bogus != 0 {
				t.Fatalf("bogus rows %d", bogus)
			}
			check(427, 949)
		})
	}
}

func TestTrafficDailyDayBoundaryAndRetention(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := landingFlowFixture(t, dialect)
			// 23:59:59 and 00:00:01 UTC+8 (= 15:59:59 / 16:00:01 UTC) are different days.
			late := time.Date(2026, 11, 5, 23, 59, 59, 0, TrafficDayZone)
			early := late.Add(2 * time.Second)
			for _, at := range []time.Time{late.UTC(), early.UTC(), early.Add(time.Hour)} {
				if _, err := r.ApplyFlowBatch(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 10, Out: 1}}}, at); err != nil {
					t.Fatal(err)
				}
			}
			if in, _ := dailySum(t, r, 2, TrafficKindTotal, 0, 20261105); in != 10 {
				t.Fatalf("11-05 = %d", in)
			}
			if in, _ := dailySum(t, r, 2, TrafficKindTotal, 0, 20261106); in != 20 {
				t.Fatalf("11-06 = %d", in)
			}
			// Retention: rows older than 400 days go, the cutoff day stays.
			cutoff := trafficDayAdd(20261106, -TrafficDailyRetentionDays)
			orphanSeed(t, r,
				&model.TrafficDaily{Day: trafficDayAdd(cutoff, -1), UserID: 2, ServerKind: TrafficKindTotal, InBytes: 1},
				&model.TrafficDaily{Day: cutoff, UserID: 2, ServerKind: TrafficKindTotal, InBytes: 1})
			n, err := r.PurgeTrafficDaily(early)
			if err != nil || n != 1 {
				t.Fatalf("purged %d err %v", n, err)
			}
			if got := dailyRows(t, r); got != 3 {
				t.Fatalf("rows after purge %d", got)
			}
		})
	}
}

// A panel restart neither resets the since marker nor loses / repeats rows.
func TestTrafficDailySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	orphanSeed(t, r, &model.User{ID: 2, User: "u", Status: 1, RoleID: 1, Flow: 1})
	since := r.TrafficDailySince()
	if since <= 0 {
		t.Fatal("no since marker")
	}
	now := time.Now()
	if _, err := r.ApplyFlowBatch(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 3, Out: 4}}}, now); err != nil {
		t.Fatal(err)
	}
	r.Close()
	time.Sleep(5 * time.Millisecond)
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := r.TrafficDailySince(); got != since {
		t.Fatalf("since changed %d → %d", since, got)
	}
	if _, err := r.ApplyFlowBatch(FlowBatch{Users: map[int64]FlowCounterDelta{2: {In: 3, Out: 4}}}, now); err != nil {
		t.Fatal(err)
	}
	if in, out := dailySum(t, r, 2, TrafficKindTotal, 0, 0); in != 6 || out != 8 {
		t.Fatalf("after restart %d/%d", in, out)
	}
	u, _ := r.GetUserByID(2)
	if u.InFlow != 6 || u.OutFlow != 8 {
		t.Fatalf("user %d/%d", u.InFlow, u.OutFlow)
	}
}

// The user card numbers are the enforcement numbers: same period, same limit.
func TestUserServerTrafficMatchesQuota(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			ms := time.Now().UnixMilli()
			exec := func(sql string, args ...interface{}) {
				t.Helper()
				if err := r.db.Exec(sql, args...).Error; err != nil {
					t.Fatalf("%s: %v", sql, err)
				}
			}
			exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx) VALUES (1,'t1',1,2,'tls',1,?,?,1,0),(2,'t2',1,1,'tls',1,?,?,1,0)`, ms, ms, ms, ms)
			exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx) VALUES (1,'1',1,0),(1,'2',2,1),(1,'3',3,0),(2,'1',4,0)`)
			exec(`INSERT INTO user_tunnel(id, user_id, tunnel_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status) VALUES (10,2,1,10,99999,0,0,1,?,1),(11,3,2,10,99999,0,0,1,?,1)`, ms+86400000, ms+86400000)
			orphanSeed(t, r,
				&model.LandingServer{ID: 5, NodeID: 5, Source: "native", Name: "US-native", Status: "active"},
				&model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "PQS-TW", Status: "active", DefaultUserQuotaBytes: 1000},
				&model.LandingServerUser{LandingServerID: 5, UserID: 2, CreatedTime: ms},
				&model.LandingServerUser{LandingServerID: 7, UserID: 2, CreatedTime: ms})
			// Node 1 default 500; user 2 reset day 1.
			exec(`UPDATE node SET default_user_quota_bytes = 500 WHERE id = 1`)
			now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)
			apply := func(key ServerQuotaKey, in, out int64, at time.Time) {
				t.Helper()
				if _, err := r.ApplyFlowBatch(FlowBatch{ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{key: {In: in, Out: out}}}, at); err != nil {
					t.Fatal(err)
				}
			}
			apply(ServerQuotaKey{2, "node", 1}, 300, 250, now)   // entry: 550 of default 500 → exhausted
			apply(ServerQuotaKey{2, "node", 2}, 999, 999, now)   // relay: not a granted server, not shown
			apply(ServerQuotaKey{2, "landing", 7}, 100, 50, now) // shared landing, default 1000
			apply(ServerQuotaKey{3, "node", 4}, 1, 1, now)       // other user
			// Native landing 5: an old period row must read 0.
			apply(ServerQuotaKey{2, "node", 5}, 40, 2, now.AddDate(0, -1, 0))
			view, err := r.UserServerTraffic(2, now)
			if err != nil {
				t.Fatal(err)
			}
			if view.PeriodStart != "2026-11-01" || view.NextReset != "2026-12-01" || view.ResetDay != 1 {
				t.Fatalf("period %+v", view)
			}
			type want struct {
				used, limit int64
				exhausted   bool
				entry, land bool
			}
			wants := map[string]want{"node:1": {550, 500, true, true, false}, "node:5": {0, 0, false, false, true}, "landing:7": {150, 1000, false, false, true}}
			if len(view.Servers) != len(wants) {
				t.Fatalf("servers %+v", view.Servers)
			}
			for _, s := range view.Servers {
				w, ok := wants[s.ServerKind+":"+i64s(s.ServerRefID)]
				if !ok || s.UsedIn+s.UsedOut != w.used || s.LimitBytes != w.limit || s.Exhausted != w.exhausted || s.Entry != w.entry || s.Landing != w.land {
					t.Fatalf("row %+v want %+v", s, w)
				}
				// Cross-check against the enforcement row.
				var q model.UserServerQuota
				if r.db.Where("user_id = 2 AND server_kind = ? AND server_ref_id = ?", s.ServerKind, s.ServerRefID).First(&q).Error == nil &&
					q.PeriodKey == LandingPeriodKey(1, now) && (q.UsedIn != s.UsedIn || q.UsedOut != s.UsedOut || q.LimitBytes != s.LimitBytes) {
					t.Fatalf("card %+v != quota %+v", s, q)
				}
			}
			// An admin override wins over the default; a raised default follows at once.
			exec(`UPDATE user_server_quota SET limit_override = ?, limit_bytes = 2000 WHERE user_id = 2 AND server_kind = 'landing'`, true)
			exec(`UPDATE node SET default_user_quota_bytes = 0 WHERE id = 1`)
			view, _ = r.UserServerTraffic(2, now)
			for _, s := range view.Servers {
				if s.ServerKind == "landing" && s.LimitBytes != 2000 || s.ServerKind == "node" && s.ServerRefID == 1 && (s.LimitBytes != 0 || s.Exhausted) {
					t.Fatalf("after override %+v", s)
				}
			}
			// The other user only ever sees their own entry.
			view, _ = r.UserServerTraffic(3, now)
			if len(view.Servers) != 1 || view.Servers[0].ServerRefID != 4 || view.Servers[0].UsedIn != 1 {
				t.Fatalf("user 3 %+v", view.Servers)
			}
			// Admin: no card rows. No reset day: no period dates.
			if view, _ = r.UserServerTraffic(9, now); len(view.Servers) != 0 || view.PeriodStart != "" {
				t.Fatalf("admin %+v", view)
			}
		})
	}
}

func i64s(v int64) string { return strconv.FormatInt(v, 10) }

func TestTrafficRankingWindows(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			exec := func(sql string, args ...interface{}) {
				t.Helper()
				if err := r.db.Exec(sql, args...).Error; err != nil {
					t.Fatalf("%s: %v", sql, err)
				}
			}
			exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx) VALUES (1,'1',1,0),(1,'2',2,1),(1,'3',3,0),(2,'1',3,0)`)
			orphanSeed(t, r, &model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "PQS-TW", Status: "active"})
			now := time.Date(2026, 11, 3, 10, 0, 0, 0, TrafficDayZone) // Tue; week = 10-28..11-03, month = 11-01..11-03
			row := func(day, user int64, kind string, ref, in, out int64) *model.TrafficDaily {
				return &model.TrafficDaily{Day: day, UserID: user, ServerKind: kind, ServerRefID: ref, InBytes: in, OutBytes: out}
			}
			orphanSeed(t, r,
				row(20261103, 2, TrafficKindTotal, 0, 10, 10), row(20261103, 2, "node", 1, 10, 10), row(20261103, 2, "node", 3, 10, 10),
				row(20261101, 3, TrafficKindTotal, 0, 50, 0), row(20261101, 3, "landing", 7, 50, 0),
				row(20261030, 2, TrafficKindTotal, 0, 100, 100), row(20261030, 2, "node", 1, 100, 100),
				row(20261027, 3, TrafficKindTotal, 0, 1000, 0), // before the week and the month
				row(20261103, 77, TrafficKindTotal, 0, 5, 5),    // deleted user: skipped
				row(20261103, 2, "node", 6, 0, 0),               // zero row: not active
			)
			exec(`INSERT INTO vite_config(name, value, time) VALUES ('traffic_daily_since', ?, 0) ON CONFLICT(name) DO UPDATE SET value = excluded.value`,
				i64s(time.Date(2026, 10, 29, 14, 0, 0, 0, TrafficDayZone).UnixMilli()))
			view, err := r.TrafficRanking(now)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Windows) != 3 {
				t.Fatalf("windows %+v", view.Windows)
			}
			get := func(key string) TrafficWindow {
				for _, w := range view.Windows {
					if w.Key == key {
						return w
					}
				}
				t.Fatalf("no window %s", key)
				return TrafficWindow{}
			}
			today, week, month := get("today"), get("week"), get("month")
			if today.From != "2026-11-03" || week.From != "2026-10-28" || month.From != "2026-11-01" || month.To != "2026-11-03" {
				t.Fatalf("ranges %s %s %s", today.From, week.From, month.From)
			}
			if today.Partial || month.Partial || !week.Partial {
				t.Fatalf("partial today=%v week=%v month=%v", today.Partial, week.Partial, month.Partial)
			}
			if len(today.Users) != 1 || today.Users[0].UserID != 2 || today.Users[0].In != 10 || today.Users[0].User != "alice" {
				t.Fatalf("today users %+v", today.Users)
			}
			if len(week.Users) != 2 || week.Users[0].UserID != 2 || week.Users[0].In+week.Users[0].Out != 220 || week.Users[1].In != 50 {
				t.Fatalf("week users %+v", week.Users)
			}
			if len(month.Users) != 2 || month.Users[0].UserID != 3 || month.Users[1].In+month.Users[1].Out != 20 {
				t.Fatalf("month users %+v", month.Users)
			}
			// Servers: names + roles (node 3 = exit of t1 and entry of t2).
			if len(week.Servers) != 3 || week.Servers[0].ServerRefID != 1 || week.Servers[0].In != 110 || week.Servers[0].Name != "HK-entry" {
				t.Fatalf("week servers %+v", week.Servers)
			}
			for _, s := range week.Servers {
				switch {
				case s.ServerKind == "node" && s.ServerRefID == 3:
					if len(s.Roles) != 2 || s.Roles[0] != "entry" || s.Roles[1] != "exit" {
						t.Fatalf("node 3 roles %v", s.Roles)
					}
				case s.ServerKind == "landing":
					if s.Name != "PQS-TW" || len(s.Roles) != 1 || s.Roles[0] != "landing" {
						t.Fatalf("landing %+v", s)
					}
				}
			}
			// Drill-down: one user's split for a window; bad window / missing user refused.
			split, err := r.UserTrafficSplit(2, "week", now)
			if err != nil || split.In != 110 || split.Out != 110 || len(split.Servers) != 2 || split.Servers[0].ServerRefID != 1 || split.Servers[0].In != 110 {
				t.Fatalf("split %+v err %v", split, err)
			}
			if _, err := r.UserTrafficSplit(2, "year", now); err == nil {
				t.Fatal("bad window accepted")
			}
			if _, err := r.UserTrafficSplit(77, "today", now); err == nil {
				t.Fatal("missing user accepted")
			}
			// User delete removes the user's rows.
			if err := r.DeleteUserCascade(3); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "traffic_daily", "user_id = 3", 0)
		})
	}
}
