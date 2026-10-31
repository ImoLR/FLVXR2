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

// Plan 180 r2: forward.rule_best_off_* on both dialects — added by the
// migration with defaults (existing rules on), INSERTs of an older panel
// without them still work (rollback to fork.102), set / list / clear, and the
// signature: an auto-off only counts for the tunnel + targets it was given for.
func TestRuleBestOffColumns(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			insertOld := func(id int64, addr string) {
				t.Helper()
				if err := r.db.Exec(`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
					VALUES(?, 1, 'u', ?, 5, ?, 'fifo', 0, 0, 1, 1, 1, 0)`, id, "old-"+addr, addr).Error; err != nil {
					t.Fatalf("insert without the r2 columns: %v", err)
				}
			}
			insertOld(1, "a.example:443")
			// Migration of a database from before r2.
			for _, col := range []string{"rule_best_off_sig", "rule_best_off_target", "rule_best_off_time"} {
				if err := r.db.Exec(`ALTER TABLE forward DROP COLUMN ` + col).Error; err != nil {
					t.Fatalf("drop %s: %v", col, err)
				}
			}
			insertOld(2, "b.example:443")
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			insertOld(3, "c.example:443,d.example:443")
			var marked int64
			r.db.Raw(`SELECT COUNT(*) FROM forward WHERE rule_best_off_sig <> '' OR rule_best_off_target <> '' OR rule_best_off_time <> 0`).Scan(&marked)
			if marked != 0 {
				t.Fatalf("%d rules off after migration", marked)
			}
			if offs, err := r.ForwardRuleBestOffs([]int64{1, 2, 3}); err != nil || len(offs) != 0 {
				t.Fatalf("offs %v %v", offs, err)
			}
			if ok, err := r.SetForwardRuleBestOff(3, 5, "c.example:443,d.example:443", "d.example:443", 1234); err != nil || !ok {
				t.Fatalf("set: %v %v", ok, err)
			}
			if ok, err := r.SetForwardRuleBestOff(2, 5, "stale.example:1", "stale.example:1", 1); err != nil || ok {
				t.Fatalf("stale set changed the row: %v %v", ok, err)
			}
			rows, err := r.ListRuleBestForwards()
			if err != nil || len(rows) != 3 || rows[2].Name != "old-c.example:443,d.example:443" || !rows[2].RuleBestOff() || rows[0].RuleBestOff() {
				t.Fatalf("round read %+v %v", rows, err)
			}
			offs, err := r.ForwardRuleBestOffs([]int64{1, 2, 3})
			if err != nil || len(offs) != 1 || offs[3].Target != "d.example:443" || offs[3].Time != 1234 {
				t.Fatalf("offs %+v %v", offs, err)
			}
			// Case / spacing of the same targets keep it; another tunnel does not.
			if RuleBestOffSig(5, "C.example:443, d.example:443") != RuleBestOffSig(5, "c.example:443,d.example:443") || RuleBestOffSig(6, "c.example:443,d.example:443") == RuleBestOffSig(5, "c.example:443,d.example:443") {
				t.Fatalf("signature")
			}
			if err := r.db.Exec(`UPDATE forward SET tunnel_id = 6 WHERE id = 3`).Error; err != nil {
				t.Fatal(err)
			}
			if offs, _ := r.ForwardRuleBestOffs([]int64{3}); len(offs) != 0 {
				t.Fatalf("moved rule keeps its auto-off")
			}
			r.db.Exec(`UPDATE forward SET tunnel_id = 5 WHERE id = 3`)
			if cleared, err := r.ClearForwardRuleBestOff(3); err != nil || !cleared {
				t.Fatalf("clear: %v %v", cleared, err)
			}
			if cleared, err := r.ClearForwardRuleBestOff(3); err != nil || cleared {
				t.Fatalf("second clear: %v %v", cleared, err)
			}
			r.db.Raw(`SELECT COUNT(*) FROM forward WHERE rule_best_off_sig <> '' OR rule_best_off_target <> '' OR rule_best_off_time <> 0`).Scan(&marked)
			if marked != 0 {
				t.Fatalf("clear left %d marks", marked)
			}
		})
	}
}
