package contract_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 227 contract (SQLite + PostgreSQL): seeded 伪装域名 candidates, the
// 自动（最快） pick for new nodes only, 屏蔽, pool merge and pins.
func TestLandingRealityDomainPickContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			rows, err := r.ListLandingRealityDomains()
			if err != nil {
				t.Fatal(err)
			}
			usable, blocked := 0, 0
			for _, d := range rows {
				if d.Source != "builtin" {
					t.Fatalf("seed %+v", d)
				}
				if d.Blocked {
					blocked++
				} else {
					usable++
				}
			}
			if usable != 10 || blocked != 8 || rows[0].Domain != "www.lovelive-anime.jp" {
				t.Fatalf("usable=%d blocked=%d first=%s", usable, blocked, rows[0].Domain)
			}
			now := time.Now().UnixMilli()
			db := r.DB()
			if err := db.Create(&model.Node{ID: 2, Name: "n2", Secret: "s2", ServerIP: "203.0.113.2", Port: "1000-2000", CreatedTime: now, Status: 1, LandingCapable: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.LandingServer{ID: 1, Source: "native", NodeID: 2, Name: "s", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
				Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			s, _ := r.GetLandingServer(1)
			if s.RealityMode != "auto" {
				t.Fatalf("default mode %q", s.RealityMode)
			}
			old, err := r.CreateLandingNode(1, 1, "old", "vless_reality_vision", true)
			if err != nil {
				t.Fatal(err)
			}
			results := []repo.RealityProbeResult{
				{Target: "www.microsoft.com:443", Feasible: true, LatencyMs: 2, ServerNames: []string{"www.microsoft.com"}},
				{Target: "www.sony.com:443", Feasible: true, LatencyMs: 8, BestIP: "203.0.113.8", ServerNames: []string{"www.sony.com", "sony.com", "*.sony.com"}},
				{Target: "www.dell.com:443", Feasible: true, LatencyMs: 15},
				{Target: "www.amd.com:443", Feasible: false, Reason: "不支持 TLS 1.3"},
			}
			rec, err := r.RecordLandingRealityProbe(1, results, "")
			if err != nil || rec.Picked != "www.sony.com:443" {
				t.Fatalf("pick %+v %v", rec, err)
			}
			s, _ = r.GetLandingServer(1)
			if s.RealityDest != "www.sony.com:443" || s.RealityServerNames != `["www.sony.com","sony.com"]` || s.RealityProbedAt == 0 || len(repo.LandingRealityProbeResults(s)) != 4 {
				t.Fatalf("server %s %s %d", s.RealityDest, s.RealityServerNames, s.RealityProbedAt)
			}
			var oldNow model.LandingNode
			if err := db.First(&oldNow, old.ID).Error; err != nil || oldNow.Dest != "www.lovelive-anime.jp:443" || oldNow.ServerNames != old.ServerNames {
				t.Fatalf("existing node changed: %s %s", oldNow.Dest, oldNow.ServerNames)
			}
			fresh, err := r.CreateLandingNode(1, 1, "fresh", "vless_reality_vision", true)
			if err != nil || fresh.Dest != "www.sony.com:443" {
				t.Fatalf("new node %+v %v", fresh, err)
			}
			// Block the winner → the next fastest; a failed 测速 keeps the table.
			var sony model.LandingRealityDomain
			db.Where("domain = ?", "www.sony.com").First(&sony)
			if _, err := r.SetLandingRealityDomainBlocked(sony.ID, true); err != nil {
				t.Fatal(err)
			}
			if rec, _ := r.RecordLandingRealityProbe(1, results, ""); rec.Picked != "www.dell.com:443" {
				t.Fatalf("after block %+v", rec)
			}
			if _, err := r.RecordLandingRealityProbe(1, nil, "节点离线"); err != nil {
				t.Fatal(err)
			}
			s, _ = r.GetLandingServer(1)
			if s.RealityProbeError != "节点离线" || len(repo.LandingRealityProbeResults(s)) != 4 || s.RealityDest != "www.dell.com:443" {
				t.Fatalf("failed probe %q %s", s.RealityProbeError, s.RealityDest)
			}
			// Manual: unchanged.
			db.Model(&model.LandingServer{}).Where("id = 1").Update("reality_mode", "manual")
			if rec, _ := r.RecordLandingRealityProbe(1, []repo.RealityProbeResult{{Target: "www.tesla.com:443", Feasible: true, LatencyMs: 1}}, ""); rec.Picked != "" {
				t.Fatalf("manual picked %+v", rec)
			}
			// Pool merge + pins.
			if added, err := r.ReplaceLandingRealityPool([]string{"www.tesla.com", "www.example.net", "gateway.icloud.com", "BAD DOMAIN"}); err != nil || added != 2 {
				t.Fatalf("pool added=%d %v", added, err)
			}
			targets, _ := r.RealityProbeTargets()
			joined := strings.Join(targets, ",")
			if !strings.Contains(joined, "www.example.net:443") || strings.Contains(joined, "icloud") || strings.Contains(joined, "www.sony.com") {
				t.Fatalf("targets %s", joined)
			}
			if err := r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 3, CheckedAt: now}}); err != nil {
				t.Fatal(err)
			}
			s, _ = r.GetLandingServer(1)
			at := s.RealityPinsAt
			if pins := repo.LandingRealityPins(s); len(pins) != 1 || pins[0].PinnedIP != "203.0.113.15" || at == 0 {
				t.Fatalf("pins %+v", pins)
			}
			// Same pins again: no write.
			time.Sleep(5 * time.Millisecond)
			_ = r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 3, CheckedAt: now}})
			if s, _ = r.GetLandingServer(1); s.RealityPinsAt != at {
				t.Fatal("unchanged pins rewritten")
			}
			due, err := r.RealityAutoDueServers(time.Hour)
			if err != nil || len(due) != 0 {
				t.Fatalf("manual server due: %v %v", due, err)
			}
		})
	}
}

// TestLandingRealityMigrationKeepsDest: an existing database (no plan-227
// table/columns) gets the seeds and 自动 mode; every server and node keeps
// its dest byte for byte.
func TestLandingRealityMigrationKeepsDest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	db := r.DB()
	db.Create(&model.Node{ID: 2, Name: "n2", Secret: "s2", ServerIP: "203.0.113.2", Port: "1000-2000", CreatedTime: now, Status: 1, LandingCapable: true})
	if err := db.Create(&model.LandingServer{ID: 1, Source: "native", NodeID: 2, Name: "s", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
		Protocols: `["vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	n, err := r.CreateLandingNode(1, 1, "usb2-like", "vless_reality_vision", true)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.LandingNodeLinks(*n)
	for _, stmt := range []string{"DROP TABLE landing_reality_domain", "DELETE FROM vite_config WHERE name = 'landing_reality_domains_seed_v1'",
		"ALTER TABLE landing_server DROP COLUMN reality_mode", "ALTER TABLE landing_server DROP COLUMN reality_probe", "ALTER TABLE landing_server DROP COLUMN reality_probed_at",
		"ALTER TABLE landing_server DROP COLUMN reality_probe_error", "ALTER TABLE landing_server DROP COLUMN reality_pins", "ALTER TABLE landing_server DROP COLUMN reality_pins_at"} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if db.Migrator().HasColumn(&model.LandingServer{}, "reality_mode") {
		t.Fatal("column not dropped")
	}
	r.Close()
	r, err = repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, _ := r.GetLandingServer(1)
	if s.RealityMode != "auto" || s.RealityDest != "www.lovelive-anime.jp:443" || s.RealityServerNames != `["www.lovelive-anime.jp"]` {
		t.Fatalf("migrated server %q %s %s", s.RealityMode, s.RealityDest, s.RealityServerNames)
	}
	var reloaded model.LandingNode
	if err := r.DB().First(&reloaded, n.ID).Error; err != nil {
		t.Fatal(err)
	}
	after, _ := r.LandingNodeLinks(reloaded)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("link changed:\n%v\n%v", before, after)
	}
	rows, _ := r.ListLandingRealityDomains()
	if len(rows) != 18 {
		t.Fatalf("seeded %d", len(rows))
	}
	_ = os.Remove(path)
}
