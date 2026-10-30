package repo

import (
	"database/sql"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 170: 限速规则 retired — delete clears every reference in one transaction
// and reports the affected rules; the start cleans up dangling references.

func speedRetireFixture(t *testing.T, r *Repository) {
	t.Helper()
	n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	orphanSeed(t, r,
		&model.SpeedLimit{ID: 1, Name: "one", Speed: 50, Status: 1, CreatedTime: 1},
		&model.SpeedLimit{ID: 2, Name: "arena", Speed: 30, ArenaMode: 1, Status: 1, CreatedTime: 1},
		&model.Tunnel{ID: 1, Name: "t1", SpeedID: n(1), Status: 1},
		&model.Tunnel{ID: 2, Name: "t2", SpeedID: n(2), Status: 1},
		&model.UserTunnel{ID: 10, UserID: 2, TunnelID: 1, SpeedID: n(2), Num: 1, Flow: 1, FlowResetTime: 1, ExpTime: 1, Status: 1},
		&model.UserTunnel{ID: 11, UserID: 3, TunnelID: 1, SpeedID: n(1), Num: 1, Flow: 1, FlowResetTime: 1, ExpTime: 1, Status: 1},
	)
	fwd := func(id, user, tunnel int64, speedID sql.NullInt64) *model.Forward {
		return &model.Forward{ID: id, UserID: user, UserName: "u", Name: "f", TunnelID: tunnel, RemoteAddr: "1.1.1.1:1", CreatedTime: 1, UpdatedTime: 1, Status: 1, Mode: "gost", SpeedID: speedID}
	}
	orphanSeed(t, r,
		fwd(100, 2, 1, sql.NullInt64{}), // via user tunnel 10 → limit 2
		fwd(101, 2, 1, n(1)),            // own limit 1 (user tunnel → 2)
		fwd(102, 3, 1, n(2)),            // own limit 2 (user tunnel → 1)
		fwd(103, 3, 2, sql.NullInt64{}), // none
	)
	if err := r.db.Model(&model.Node{}).Where("id = ?", 1).Update("speed_id", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&model.User{}).Where("id = ?", 2).Update("speed_limit_id", 2).Error; err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSpeedLimitAndRefs(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			speedRetireFixture(t, r)
			ids, err := r.DeleteSpeedLimitAndRefs(2)
			if err != nil {
				t.Fatal(err)
			}
			// 100 via its user tunnel, 101 too (same user tunnel), 102 own.
			if len(ids) != 3 || ids[0] != 100 || ids[1] != 101 || ids[2] != 102 {
				t.Fatalf("affected %v", ids)
			}
			orphanCount(t, r, "speed_limit", "id = 2", 0)
			orphanCount(t, r, "speed_limit", "id = 1", 1)
			orphanCount(t, r, "forward", "speed_id = 2", 0)
			orphanCount(t, r, "forward", "speed_id = 1", 1) // 101 keeps limit 1
			orphanCount(t, r, "tunnel", "speed_id = 2", 0)
			orphanCount(t, r, "tunnel", "speed_id = 1", 1)
			orphanCount(t, r, "user_tunnel", "speed_id = 2", 0)
			orphanCount(t, r, "user_tunnel", "speed_id = 1", 1)
			orphanCount(t, r, "node", "speed_id IS NOT NULL", 0)
			var users int64
			if err := r.db.Model(&model.User{}).Where("speed_limit_id IS NOT NULL").Count(&users).Error; err != nil || users != 0 {
				t.Fatalf("user.speed_limit_id left: %d %v", users, err)
			}
			// Unknown id: nothing affected, no error.
			if ids, err := r.DeleteSpeedLimitAndRefs(99); err != nil || len(ids) != 0 {
				t.Fatalf("unknown id: %v %v", ids, err)
			}
		})
	}
}

func TestCleanupSpeedLimitOrphansIdempotent(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			speedRetireFixture(t, r)
			// Limit 2 vanished the pre-plan-170 way (row deleted, refs kept).
			if err := r.db.Exec("DELETE FROM speed_limit WHERE id = 2").Error; err != nil {
				t.Fatal(err)
			}
			out, err := cleanupSpeedLimitOrphans(r.db)
			if err != nil {
				t.Fatal(err)
			}
			want := SpeedRefCleanup{"forward.speed_id": 1, "tunnel.speed_id": 1, "user_tunnel.speed_id": 1, "node.speed_id": 1, "user.speed_limit_id": 1}
			for k, v := range want {
				if out[k] != v {
					t.Fatalf("first run %v, want %v", out, want)
				}
			}
			orphanCount(t, r, "forward", "speed_id = 1", 1) // valid references stay
			orphanCount(t, r, "tunnel", "speed_id = 1", 1)
			orphanCount(t, r, "user_tunnel", "speed_id = 1", 1)
			orphanCount(t, r, "forward", "speed_id = 2", 0)
			again, err := cleanupSpeedLimitOrphans(r.db)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range again {
				if v != 0 {
					t.Fatalf("second run changed %s: %v", k, again)
				}
			}
		})
	}
}
