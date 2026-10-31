package repo

import (
	"testing"

	"go-backend/internal/store/model"
)

// Plan 180: tunnel.rule_best on both dialects — default 1 for new and for
// existing rows (migration), INSERTs of an older panel without the column
// still work (rollback to fork.102), the switch can be stored off, and the
// round's two light reads.
func TestRuleBestColumnAndReads(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			// A row as an older panel writes it (no rule_best in the INSERT).
			if err := r.db.Exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx, ip_preference, http, tls, socks, block_other)
				VALUES(5, 'old-panel', 1, 2, 'tls', 1, 1, 1, 1, 0, '', 0, 0, 0, 0)`).Error; err != nil {
				t.Fatalf("insert without rule_best: %v", err)
			}
			tr, err := r.GetTunnelRecord(5)
			if err != nil || tr == nil || tr.RuleBest != 1 {
				t.Fatalf("old-panel row rule_best = %+v %v, want 1", tr, err)
			}
			// GORM create with the zero value keeps the column default (on).
			tn := model.Tunnel{Name: "gorm", Type: 2, Protocol: "tls", Flow: 1, Status: 1}
			if err := r.db.Create(&tn).Error; err != nil {
				t.Fatal(err)
			}
			if tr, _ := r.GetTunnelRecord(tn.ID); tr.RuleBest != 1 {
				t.Fatalf("gorm create rule_best = %d", tr.RuleBest)
			}
			if err := r.SetTunnelRuleBestTx(nil, tn.ID, 0); err != nil {
				t.Fatal(err)
			}
			if tr, _ := r.GetTunnelRecord(tn.ID); tr.RuleBest != 0 {
				t.Fatalf("stored off = %d", tr.RuleBest)
			}
			// Migration of a database from before plan 180: the column is added
			// and every existing tunnel is on.
			if err := r.db.Exec(`ALTER TABLE tunnel DROP COLUMN rule_best`).Error; err != nil {
				t.Fatalf("drop column: %v", err)
			}
			if err := r.db.Exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx, ip_preference, http, tls, socks, block_other)
				VALUES(50, 'pre-180', 1, 2, 'tls', 1, 1, 1, 1, 0, '', 0, 0, 0, 0)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			var on int64
			r.db.Raw(`SELECT COUNT(*) FROM tunnel WHERE rule_best = 1`).Scan(&on)
			var all int64
			r.db.Raw(`SELECT COUNT(*) FROM tunnel`).Scan(&all)
			if on != all || all != 3 {
				t.Fatalf("after migration %d of %d tunnels on", on, all)
			}

			// The round's reads.
			for _, f := range []model.Forward{
				{ID: 11, UserID: 1, UserName: "u", Name: "a", TunnelID: 5, RemoteAddr: "a.example:443", Strategy: "fifo", Status: 1, Mode: "gost"},
				{ID: 12, UserID: 1, UserName: "u", Name: "b", TunnelID: 5, RemoteAddr: "b.example:443", Strategy: "fifo", Status: 0},
			} {
				if err := r.db.Create(&f).Error; err != nil {
					t.Fatal(err)
				}
			}
			rows, err := r.ListRuleBestForwards()
			if err != nil || len(rows) != 2 || rows[0].ID != 11 || rows[0].Mode != "gost" || rows[1].Status != 0 || rows[1].TunnelID != 5 {
				t.Fatalf("forwards %+v %v", rows, err)
			}
			for _, c := range []model.ForwardChain{
				{UserID: 1, TunnelID: 5, ForwardID: 11, State: model.ChainStateActive, UpstreamHost: "2001:db8::7", UpstreamPort: 8443},
				{UserID: 1, TunnelID: 5, ForwardID: 12, State: "creating", UpstreamHost: "x.example", UpstreamPort: 1},
			} {
				if err := r.db.Create(&c).Error; err != nil {
					t.Fatal(err)
				}
			}
			ups, err := r.ListActiveChainUpstreams()
			if err != nil || len(ups) != 1 || ups[11] != "[2001:db8::7]:8443" {
				t.Fatalf("chain upstreams %v %v", ups, err)
			}
		})
	}
}
