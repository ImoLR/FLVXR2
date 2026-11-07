package contract_test

import (
	"encoding/json"
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
			rec, err := r.RecordLandingRealityProbe(1, results, "", nil)
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
			if rec, _ := r.RecordLandingRealityProbe(1, results, "", nil); rec.Picked != "www.dell.com:443" {
				t.Fatalf("after block %+v", rec)
			}
			if _, err := r.RecordLandingRealityProbe(1, nil, "节点离线", nil); err != nil {
				t.Fatal(err)
			}
			s, _ = r.GetLandingServer(1)
			if s.RealityProbeError != "节点离线" || len(repo.LandingRealityProbeResults(s)) != 4 || s.RealityDest != "www.dell.com:443" {
				t.Fatalf("failed probe %q %s", s.RealityProbeError, s.RealityDest)
			}
			// Manual: unchanged.
			db.Model(&model.LandingServer{}).Where("id = 1").Update("reality_mode", "manual")
			if rec, _ := r.RecordLandingRealityProbe(1, []repo.RealityProbeResult{{Target: "www.tesla.com:443", Feasible: true, LatencyMs: 1}}, "", nil); rec.Picked != "" {
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
			if moves, err := r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 3, CheckedAt: now}}); err != nil || len(moves) != 0 {
				t.Fatalf("first pin moves=%v %v", moves, err)
			}
			s, _ = r.GetLandingServer(1)
			at := s.RealityPinsAt
			if pins := repo.LandingRealityPins(s); len(pins) != 1 || pins[0].PinnedIP != "203.0.113.15" || at == 0 {
				t.Fatalf("pins %+v", pins)
			}
			// Same pins again: no write.
			time.Sleep(5 * time.Millisecond)
			_, _ = r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 3, CheckedAt: now}})
			if s, _ = r.GetLandingServer(1); s.RealityPinsAt != at {
				t.Fatal("unchanged pins rewritten")
			}
			// Plan 234: a re-check (new time) on the same IP is no move; another IP is one, with its reason.
			if moves, err := r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 4, CheckedAt: now + 1}}); err != nil || len(moves) != 0 {
				t.Fatalf("re-check moves=%v %v", moves, err)
			}
			moves, err := r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.16", PinnedMs: 2, CheckedAt: now + 2, MoveReason: repo.RealityPinMoveFailover}})
			if err != nil || len(moves) != 1 || !moves[0].Failover || moves[0].Kind != repo.AutoSwitchRealityIP || moves[0].Unit != 1 {
				t.Fatalf("move %+v %v", moves, err)
			}
			if moves, _ := r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.16", PinnedMs: 2, CheckedAt: now + 2, MoveReason: repo.RealityPinMoveFailover}}); len(moves) != 0 {
				t.Fatalf("same report counted twice: %+v", moves)
			}
			// Restore the pin the rest of the test expects.
			_, _ = r.RecordLandingRealityPins(1, []repo.RealityPin{{Host: "www.dell.com", PinnedIP: "203.0.113.15", PinnedMs: 3, CheckedAt: now}})
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

// Plan 229 contract (SQLite + PostgreSQL): merged re-samples rank by the worst
// sample, the per-server history persists in reality_samples (bounded) and a
// 妙妙屋池 small site is auto-picked only once its history is stable.
func TestLandingRealityTrialHistoryContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			if err := db.Create(&model.Node{ID: 2, Name: "n2", Secret: "s2", ServerIP: "203.0.113.2", Port: "1000-2000", CreatedTime: now, Status: 1, LandingCapable: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.LandingServer{ID: 1, Source: "native", NodeID: 2, Name: "s", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
				Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := r.ReplaceLandingRealityPool([]string{"www.ebay.com", "portal.citygrainla.com"}); err != nil {
				t.Fatal(err)
			}
			pass1 := []repo.RealityProbeResult{
				{Target: "www.ebay.com:443", Feasible: true, LatencyMs: 3, ServerNames: []string{"www.ebay.com"}},
				{Target: "www.tesla.com:443", Feasible: true, LatencyMs: 4, ServerNames: []string{"www.tesla.com"}},
				{Target: "www.sony.com:443", Feasible: true, LatencyMs: 8, ServerNames: []string{"www.sony.com"}},
			}
			info, err := r.RealityPickInfo(1)
			if err != nil {
				t.Fatal(err)
			}
			if set := strings.Join(repo.RealityResampleSet(pass1, info), ","); set != "www.tesla.com:443,www.sony.com:443,www.ebay.com:443" {
				t.Fatalf("re-sample set %s", set)
			}
			re := []repo.RealityProbeResult{{Target: "www.ebay.com:443", Feasible: true, LatencyMs: 3}, {Target: "www.tesla.com:443", Feasible: true, LatencyMs: 30}, {Target: "www.sony.com:443", Feasible: true, LatencyMs: 8}}
			merged := repo.MergeRealitySamples(pass1, [][]repo.RealityProbeResult{re, re})
			stats := &repo.RealityProbeStats{Passes: 3, Resampled: 3, Sockets: 12, MaxOpen: 2, Linger0: true, Measured: true}
			rec, err := r.RecordLandingRealityProbe(1, merged, "", stats)
			if err != nil || rec.Picked != "www.sony.com:443" {
				t.Fatalf("picked %+v %v", rec, err)
			}
			s, _ := r.GetLandingServer(1)
			hist := repo.LandingRealityHistory(s)
			if len(hist.D["www.ebay.com"]) != 3 || hist.Stats == nil || hist.Stats.Sockets != 12 || !hist.Stats.Linger0 {
				t.Fatalf("history %+v", hist)
			}
			for _, res := range repo.LandingRealityProbeResults(s) {
				if res.Target == "www.ebay.com:443" && (!res.Trial || res.TrialOK || res.TrialReason != "6 小时后再测一次" || !res.TrialPending) {
					t.Fatalf("ebay %+v", res)
				}
			}
			// Same samples 7 h later: ebay is stable → picked.
			for d, list := range hist.D {
				for i := range list {
					list[i].At -= 7 * 3600
				}
				hist.D[d] = list
			}
			raw, _ := json.Marshal(hist)
			if err := db.Model(&model.LandingServer{}).Where("id = ?", 1).Update("reality_samples", string(raw)).Error; err != nil {
				t.Fatal(err)
			}
			merged = repo.MergeRealitySamples(pass1, [][]repo.RealityProbeResult{re, re})
			if rec, err := r.RecordLandingRealityProbe(1, merged, "", nil); err != nil || rec.Picked != "www.ebay.com:443" {
				t.Fatalf("stable small site %+v %v", rec, err)
			}
			for i := 0; i < 3; i++ {
				if _, err := r.RecordLandingRealityProbe(1, repo.MergeRealitySamples(pass1, [][]repo.RealityProbeResult{re, re}), "", nil); err != nil {
					t.Fatal(err)
				}
			}
			s, _ = r.GetLandingServer(1)
			for d, list := range repo.LandingRealityHistory(s).D {
				if len(list) != repo.RealitySampleKeep {
					t.Fatalf("%s: %d samples", d, len(list))
				}
			}
			// A failed 测速 keeps table and history.
			before := s.RealitySamples
			if _, err := r.RecordLandingRealityProbe(1, nil, "节点离线", nil); err != nil {
				t.Fatal(err)
			}
			if s, _ = r.GetLandingServer(1); s.RealitySamples != before || s.RealityProbeError != "节点离线" {
				t.Fatal("failed 测速 touched the history")
			}
		})
	}
}

// Plan 233 contract (SQLite + PostgreSQL): in 自动 mode the stored default
// stays while a challenger is < 5 ms faster, moves at ≥ 5 ms, and at once
// when the current one fails; existing nodes keep their dest.
func TestLandingRealityPickMarginContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			if err := db.Create(&model.Node{ID: 2, Name: "n2", Secret: "s2", ServerIP: "203.0.113.2", Port: "1000-2000", CreatedTime: now, Status: 1, LandingCapable: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.LandingServer{ID: 1, Source: "native", NodeID: 2, Name: "s", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
				Protocols: `["vless_reality_vision"]`, RealityDest: "www.tesla.com:443", RealityServerNames: `["www.tesla.com"]`, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			old, err := r.CreateLandingNode(1, 1, "old", "vless_reality_vision", true)
			if err != nil {
				t.Fatal(err)
			}
			row := func(target string, ms ...int) repo.RealityProbeResult {
				pass1 := []repo.RealityProbeResult{{Target: target, Feasible: true, LatencyMs: ms[0], ServerNames: []string{strings.Split(target, ":")[0]}}}
				var passes [][]repo.RealityProbeResult
				for _, m := range ms[1:] {
					passes = append(passes, []repo.RealityProbeResult{{Target: target, Feasible: m > 0, LatencyMs: m}})
				}
				return repo.MergeRealitySamples(pass1, passes)[0]
			}
			dest := func() string { s, _ := r.GetLandingServer(1); return s.RealityDest }
			// Bestvm US shape: 1 ms apart, order flips → tesla stays.
			for _, rows := range [][]repo.RealityProbeResult{
				{row("www.sony.com:443", 6, 6, 6), row("www.tesla.com:443", 7, 7, 7)},
				{row("www.dell.com:443", 6, 6, 6), row("www.sony.com:443", 7, 7, 7), row("www.tesla.com:443", 7, 7, 7)},
			} {
				rec, err := r.RecordLandingRealityProbe(1, rows, "", nil)
				if err != nil || rec.Picked != "" || dest() != "www.tesla.com:443" {
					t.Fatalf("held pick moved: %+v %v %s", rec, err, dest())
				}
			}
			// ≥ 5 ms faster → switch.
			if rec, _ := r.RecordLandingRealityProbe(1, []repo.RealityProbeResult{row("www.sony.com:443", 2, 2, 2), row("www.tesla.com:443", 7, 7, 7)}, "", nil); rec.Picked != "www.sony.com:443" || dest() != "www.sony.com:443" {
				t.Fatalf("margin reached, not switched: %+v %s", rec, dest())
			}
			// Current fails a re-sample → at once, even to a 1 ms slower site.
			if rec, _ := r.RecordLandingRealityProbe(1, []repo.RealityProbeResult{row("www.tesla.com:443", 3, 3, 3), row("www.sony.com:443", 2, 2, 0)}, "", nil); rec.Picked != "www.tesla.com:443" {
				t.Fatalf("failed current kept: %+v %s", rec, dest())
			}
			var n model.LandingNode
			if err := db.First(&n, old.ID).Error; err != nil || n.Dest != "www.tesla.com:443" || n.ServerNames != old.ServerNames {
				t.Fatalf("existing node changed: %s %s", n.Dest, n.ServerNames)
			}
		})
	}
}
