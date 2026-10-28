package repo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 159: per user per server 速率 (row override / user default), the
// rates the runtime reads, and per-server row cleanup when servers go away.

func rateForward(t *testing.T, r *Repository, id, userID int64, nodes ...int64) {
	t.Helper()
	now := time.Now().UnixMilli()
	orphanSeed(t, r, &model.Forward{ID: id, UserID: userID, UserName: "u", Name: "f", TunnelID: 1, RemoteAddr: "1.1.1.1:80", Strategy: "fifo", CreatedTime: now, Status: 1})
	for i, n := range nodes {
		orphanSeed(t, r, &model.ForwardPort{ForwardID: id, NodeID: n, Port: int(1000 + id*10 + int64(i))})
	}
}

func TestUserServerRates(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			caps, _ := json.Marshal(landingcustom.Share{ID: 16, Status: "active"})
			orphanSeed(t, r, &model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "PQS-TW", Status: "active", SharedCaps: string(caps)})
			rateForward(t, r, 10, 2, 1, 4) // alice enters HK + TW
			rateForward(t, r, 11, 3, 1)    // bob enters HK
			save := func(userID int64, quotas ...model.UserServerQuota) error {
				return r.SaveLandingUserSettingFull(userID, LandingMaxNodesFollowGlobal, quotas, 0, nil, nil)
			}
			row := func(kind string, ref int64, override bool, rate int) model.UserServerQuota {
				return model.UserServerQuota{ServerKind: kind, ServerRefID: ref, FollowDefault: true, RateOverride: override, RateMbps: rate}
			}
			// Nothing set: no rates anywhere (upgrade = no change).
			if rates, err := r.UserEntryRates(2); err != nil || rates[1] != 0 || rates[4] != 0 || len(rates) != 2 {
				t.Fatalf("fresh rates %v %v", rates, err)
			}
			if rates, err := r.NodeUserRates(1); err != nil || len(rates) != 0 {
				t.Fatalf("fresh node rates %v %v", rates, err)
			}
			// Row override on HK, default for the rest.
			if err := save(2, row("node", 1, true, 50)); err != nil {
				t.Fatal(err)
			}
			if err := r.SetUserServerRate(2, 20); err != nil {
				t.Fatal(err)
			}
			if v, _ := r.UserServerRateMbps(2, 1); v != 50 {
				t.Fatalf("HK override = %d, want 50", v)
			}
			if v, _ := r.UserServerRateMbps(2, 4); v != 20 {
				t.Fatalf("TW default = %d, want 20", v)
			}
			if rates, _ := r.UserEntryRates(2); rates[1] != 50 || rates[4] != 20 {
				t.Fatalf("entry rates %v", rates)
			}
			if err := r.SetUserServerRate(3, 10); err != nil {
				t.Fatal(err)
			}
			if rates, _ := r.NodeUserRates(1); rates[2] != 50 || rates[3] != 10 || len(rates) != 2 {
				t.Fatalf("HK node rates %v", rates)
			}
			// The traffic quota keeps following the server default.
			var q model.UserServerQuota
			if err := r.db.Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", 2, "node", 1).First(&q).Error; err != nil || q.LimitOverride || !q.RateOverride || q.RateMbps != 50 {
				t.Fatalf("row %+v %v", q, err)
			}
			// An explicit 0 = 不限 on TW even with a default.
			if err := save(2, row("node", 4, true, 0)); err != nil {
				t.Fatal(err)
			}
			if rates, _ := r.UserEntryRates(2); rates[1] != 50 || rates[4] != 0 {
				t.Fatalf("explicit unlimited %v", rates)
			}
			// An older client (no rate fields) keeps the stored rate.
			old := model.UserServerQuota{ServerKind: "node", ServerRefID: 1, FollowDefault: true, MaxInboundConnections: 5, RateKeep: true}
			if err := save(2, old); err != nil {
				t.Fatal(err)
			}
			if v, _ := r.UserServerRateMbps(2, 1); v != 50 {
				t.Fatalf("old client cleared the rate: %d", v)
			}
			// Back to following the default.
			if err := save(2, row("node", 1, false, 999)); err != nil {
				t.Fatal(err)
			}
			if v, _ := r.UserServerRateMbps(2, 1); v != 20 {
				t.Fatalf("follow default = %d, want 20", v)
			}
			for _, bad := range []int{-1, MaxServerRateMbps + 1} {
				if err := save(2, row("node", 1, true, bad)); err == nil || !strings.Contains(err.Error(), "速率") {
					t.Fatalf("rate %d accepted: %v", bad, err)
				}
				if err := r.SetUserServerRate(2, bad); err == nil {
					t.Fatalf("default %d accepted", bad)
				}
			}
			// A default replaces the legacy 总限速规则 (speed_limit_id).
			if err := r.db.Model(&model.User{}).Where("id = ?", 3).Update("speed_limit_id", 4).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SetUserServerRate(3, 0); err != nil {
				t.Fatal(err)
			}
			var bob model.User
			_ = r.db.First(&bob, 3).Error
			if !bob.SpeedLimitID.Valid {
				t.Fatal("rate 0 must keep the legacy binding")
			}
			if err := r.SetUserServerRate(3, 30); err != nil {
				t.Fatal(err)
			}
			bob = model.User{}
			_ = r.db.First(&bob, 3).Error
			if bob.SpeedLimitID.Valid || bob.ServerRateMbps != 30 {
				t.Fatalf("legacy binding not cleared: %+v", bob)
			}
			// Shared server: owners of nodes there, row (kind landing) or default.
			orphanSeed(t, r, &model.LandingNode{ID: 40, LandingServerID: 7, UserID: 2, Name: "n", Protocol: "ss2022", Port: 30001, Status: "active", CreatedTime: 1, UpdatedTime: 1})
			if rates, err := r.SharedUserRates(7); err != nil || rates[2] != 20 || len(rates) != 1 {
				t.Fatalf("shared default %v %v", rates, err)
			}
			if err := save(2, row("landing", 7, true, 30)); err != nil {
				t.Fatal(err)
			}
			if rates, _ := r.SharedUserRates(7); rates[2] != 30 {
				t.Fatalf("shared override %v", rates)
			}
		})
	}
}

func TestServerRowOrphanCleanup(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			live, _ := json.Marshal(landingcustom.Share{ID: 16, Status: "active"})
			gone, _ := json.Marshal(landingcustom.Share{ID: 17, Status: "revoked"})
			orphanSeed(t, r,
				&model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "live", Status: "active", SharedCaps: string(live)},
				&model.LandingServer{ID: 8, Source: "shared", RemoteServerID: 17, Name: "revoked", Status: "active", SharedCaps: string(gone)},
			)
			now := time.Now().UnixMilli()
			for _, q := range []model.UserServerQuota{
				{UserID: 2, ServerKind: "node", ServerRefID: 1},     // keep
				{UserID: 2, ServerKind: "node", ServerRefID: 99},    // node deleted
				{UserID: 2, ServerKind: "landing", ServerRefID: 7},  // keep
				{UserID: 2, ServerKind: "landing", ServerRefID: 8},  // share revoked
				{UserID: 2, ServerKind: "landing", ServerRefID: 77}, // server gone
				{UserID: 55, ServerKind: "node", ServerRefID: 1},    // user deleted
			} {
				q.CreatedTime, q.UpdatedTime = now, now
				orphanSeed(t, r, &q)
			}
			pool := model.UserTrafficPool{UserID: 2, Name: "p", LimitBytes: 1 << 30, CreatedTime: now, UpdatedTime: now}
			orphanSeed(t, r, &pool)
			orphanSeed(t, r,
				&model.UserTrafficPoolMember{PoolID: pool.ID, UserID: 2, ServerKind: "node", ServerRefID: 99},
				&model.UserTrafficPoolMember{PoolID: pool.ID, UserID: 2, ServerKind: "landing", ServerRefID: 8},
			)
			got, err := r.CleanupServerRowOrphans()
			if err != nil || got.Quotas != 4 || got.Members != 2 {
				t.Fatalf("cleanup = %+v %v, want 4 rows + 2 members", got, err)
			}
			orphanCount(t, r, "user_server_quota", "1 = 1", 2)
			orphanCount(t, r, "user_server_quota", "user_id = ? AND server_kind = ? AND server_ref_id = ?", 1, 2, "node", 1)
			orphanCount(t, r, "user_server_quota", "user_id = ? AND server_kind = ? AND server_ref_id = ?", 1, 2, "landing", 7)
			orphanCount(t, r, "user_traffic_pool_member", "1 = 1", 0)
			// The emptied pool stays (shown empty).
			orphanCount(t, r, "user_traffic_pool", "id = ?", 1, pool.ID)
			// Idempotent.
			if again, err := r.CleanupServerRowOrphans(); err != nil || again.Quotas != 0 || again.Members != 0 {
				t.Fatalf("second cleanup = %+v %v", again, err)
			}
		})
	}
}

// A share revoked by 妙妙屋 loses its per-server rows in the sync transaction;
// other servers' rows and the (now empty) pool stay.
func TestSharedRevokeDeletesServerRows(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			share := landingcustom.Share{ID: 5, Name: "Boil Hinet", Hosts: landingcustom.Hosts{IPv4: "198.51.100.5"}, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022"}}
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			var s model.LandingServer
			if err := r.db.Where("source = ? AND remote_server_id = ?", "shared", 5).First(&s).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingUserSettingFull(2, LandingMaxNodesFollowGlobal, []model.UserServerQuota{
				{ServerKind: "landing", ServerRefID: s.ID, FollowDefault: true, RateOverride: true, RateMbps: 40},
				{ServerKind: "node", ServerRefID: 1, FollowDefault: true, RateOverride: true, RateMbps: 10},
			}, 0, nil, &[]TrafficPoolInput{{Name: "p", LimitBytes: 1 << 30, Members: []TrafficPoolServer{{ServerKind: "landing", ServerRefID: s.ID}}}}); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 1, "landing", s.ID)
			orphanCount(t, r, "user_traffic_pool_member", "server_kind = ? AND server_ref_id = ?", 1, "landing", s.ID)
			share.Status = "revoked"
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			orphanCount(t, r, "user_traffic_pool_member", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 1, "node", 1)
			orphanCount(t, r, "user_traffic_pool", "user_id = ?", 1, 2)
			// A dialog opened before the revoke cannot bring the rows back, and
			// the revoked share is no longer offered.
			if err := r.SaveLandingUserSettingFull(2, LandingMaxNodesFollowGlobal, []model.UserServerQuota{
				{ServerKind: "landing", ServerRefID: s.ID, FollowDefault: true, RateOverride: true, RateMbps: 40},
			}, 0, nil, &[]TrafficPoolInput{{Name: "p", LimitBytes: 1 << 30, Members: []TrafficPoolServer{{ServerKind: "landing", ServerRefID: s.ID}}}}); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			orphanCount(t, r, "user_traffic_pool_member", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			related, err := r.UserRelatedServers(2)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range related {
				if v.ServerKind == "landing" && v.ServerRefID == s.ID {
					t.Fatalf("revoked share still related: %+v", v)
				}
			}
			// Node delete removes the node's rows in its transaction (plan 157).
			if err := r.DeleteNodeCascade(1); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 0, "node", 1)
		})
	}
}
