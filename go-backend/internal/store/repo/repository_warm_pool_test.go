package repo

import (
	"database/sql"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 244: tunnel.warm_pool on both dialects — on for new rows and for every
// existing row after the migration, INSERTs of an older panel (rollback to
// fork.128) still work, the switch can be stored off; the owner queries count
// only active gost rules of enabled tunnels with the switch on.
func TestWarmPoolColumnAndOwners(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			if err := r.db.Exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx, ip_preference, http, tls, socks, block_other)
				VALUES(5, 'old-panel', 1, 2, 'tls', 1, 1, 1, 1, 0, '', 0, 0, 0, 0)`).Error; err != nil {
				t.Fatalf("insert without warm_pool: %v", err)
			}
			if tr, err := r.GetTunnelRecord(5); err != nil || tr == nil || tr.WarmPool != 1 {
				t.Fatalf("old-panel row warm_pool = %+v %v, want 1", tr, err)
			}
			tn := model.Tunnel{Name: "gorm", Type: 1, Protocol: "tls", Flow: 1, Status: 1}
			if err := r.db.Create(&tn).Error; err != nil {
				t.Fatal(err)
			}
			if tr, _ := r.GetTunnelRecord(tn.ID); tr.WarmPool != 1 {
				t.Fatalf("gorm create warm_pool = %d", tr.WarmPool)
			}
			if err := r.SetTunnelWarmPoolTx(nil, tn.ID, 0); err != nil {
				t.Fatal(err)
			}
			if tr, _ := r.GetTunnelRecord(tn.ID); tr.WarmPool != 0 {
				t.Fatalf("stored off = %d", tr.WarmPool)
			}
			if err := r.SetTunnelWarmPoolTx(nil, tn.ID, 1); err != nil {
				t.Fatal(err)
			}

			// Migration of a fork.128 database: column added, all tunnels on.
			if err := r.db.Exec(`ALTER TABLE tunnel DROP COLUMN warm_pool`).Error; err != nil {
				t.Fatalf("drop column: %v", err)
			}
			if err := r.db.Exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx, ip_preference, http, tls, socks, block_other)
				VALUES(50, 'pre-244', 1, 2, 'tls', 1, 1, 1, 1, 0, '', 0, 0, 0, 0)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			var on, all int64
			r.db.Raw(`SELECT COUNT(*) FROM tunnel WHERE warm_pool = 1`).Scan(&on)
			r.db.Raw(`SELECT COUNT(*) FROM tunnel`).Scan(&all)
			if on != all || all != 3 {
				t.Fatalf("after migration %d of %d tunnels on", on, all)
			}

			// Owners: tunnel 50 (type 2) entry 7 → middle 8 → exit 9; tunnel
			// tn (type 1) entry 7.
			for _, ct := range []model.ChainTunnel{
				{TunnelID: 50, ChainType: "1", NodeID: 7},
				{TunnelID: 50, ChainType: "2", NodeID: 8, Port: sql.NullInt64{Int64: 3001, Valid: true}},
				{TunnelID: 50, ChainType: "3", NodeID: 9, Port: sql.NullInt64{Int64: 3002, Valid: true}},
				{TunnelID: tn.ID, ChainType: "1", NodeID: 7},
			} {
				ct := ct
				if err := r.db.Create(&ct).Error; err != nil {
					t.Fatal(err)
				}
			}
			mkRule := func(id, tunnel int64, status int, mode string) {
				t.Helper()
				f := model.Forward{ID: id, UserID: 1, UserName: "u", Name: "r", TunnelID: tunnel, RemoteAddr: "198.51.100.1:443", Strategy: "fifo", Status: status, Mode: mode, CreatedTime: 1, UpdatedTime: 1}
				if err := r.db.Create(&f).Error; err != nil {
					t.Fatal(err)
				}
				if err := r.db.Create(&model.ForwardPort{ForwardID: id, NodeID: 7, Port: int(20000 + id)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			owners := func() ([]WarmChainOwner, []WarmForwardOwner) {
				t.Helper()
				c, err := r.ListWarmChainOwners()
				if err != nil {
					t.Fatal(err)
				}
				f, err := r.ListWarmForwardOwners()
				if err != nil {
					t.Fatal(err)
				}
				return c, f
			}
			if c, f := owners(); len(c) != 0 || len(f) != 0 {
				t.Fatalf("owners without rules: %v %v", c, f)
			}
			mkRule(101, 50, 0, "gost")     // paused
			mkRule(102, 50, 1, "nftables") // kernel forward: no chain dial
			mkRule(103, tn.ID, 0, "gost")  // paused
			if c, f := owners(); len(c) != 0 || len(f) != 0 {
				t.Fatalf("paused / nftables rules counted: %v %v", c, f)
			}
			mkRule(104, 50, 1, "gost")
			mkRule(105, tn.ID, 1, "gost")
			c, f := owners()
			if len(c) != 2 || c[0] != (WarmChainOwner{NodeID: 7, TunnelID: 50}) || c[1] != (WarmChainOwner{NodeID: 8, TunnelID: 50}) {
				t.Fatalf("chain owners = %v (want entry 7 + middle 8, not the exit)", c)
			}
			if len(f) != 1 || f[0] != (WarmForwardOwner{NodeID: 7, ForwardID: 105, TunnelID: tn.ID}) {
				t.Fatalf("forward owners = %v", f)
			}
			counts, err := r.CountActiveGostForwardsByTunnel()
			if err != nil || counts[50] != 1 || counts[tn.ID] != 1 {
				t.Fatalf("active counts %v %v", counts, err)
			}
			// Switch off → no owners; tunnel disabled → none.
			_ = r.SetTunnelWarmPoolTx(nil, 50, 0)
			_ = r.SetTunnelWarmPoolTx(nil, tn.ID, 0)
			if c, f := owners(); len(c) != 0 || len(f) != 0 {
				t.Fatalf("owners with the switch off: %v %v", c, f)
			}
			_ = r.SetTunnelWarmPoolTx(nil, 50, 1)
			r.db.Exec(`UPDATE tunnel SET status = 0 WHERE id = 50`)
			if c, _ := owners(); len(c) != 0 {
				t.Fatalf("owners of a disabled tunnel: %v", c)
			}
		})
	}
}
