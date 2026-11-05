package repo

import (
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 223: shared landing servers keep Core memory / CPU at 0 / 0 = 不限制
// (decided by 妙妙屋, never sent from FLVX); native servers are unchanged.
func TestSharedCoreUnlimited(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			// A fresh share is 不限制 in the database, despite the column
			// defaults (256 / 50) that Create would apply to zero values.
			if cur := reloadServer(t, r, s.ID); cur.CoreMemoryMB != 0 || cur.CoreCPUPercent != 0 {
				t.Fatalf("fresh share core limits %d / %d, want 0 / 0", cur.CoreMemoryMB, cur.CoreCPUPercent)
			}
			// Saving the settings keeps 0 / 0 whatever the client sends.
			cur := reloadServer(t, r, s.ID)
			if err := r.SaveLandingServer(cur, nil); err != nil {
				t.Fatalf("save shared with 0 / 0: %v", err)
			}
			cur = reloadServer(t, r, s.ID)
			cur.CoreMemoryMB, cur.CoreCPUPercent = 512, 80
			if err := r.SaveLandingServer(cur, nil); err != nil {
				t.Fatalf("save shared with stale limits: %v", err)
			}
			if cur = reloadServer(t, r, s.ID); cur.CoreMemoryMB != 0 || cur.CoreCPUPercent != 0 {
				t.Fatalf("saved shared core limits %d / %d, want 0 / 0", cur.CoreMemoryMB, cur.CoreCPUPercent)
			}
			// A later share sync of an existing row with old values → 0 / 0.
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", s.ID).Updates(map[string]any{"core_memory_mb": 256, "core_cpu_percent": 50}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SyncSharedLandings([]landingcustom.Share{extraProtocolShare(true)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if cur = reloadServer(t, r, s.ID); cur.CoreMemoryMB != 0 || cur.CoreCPUPercent != 0 {
				t.Fatalf("re-synced shared core limits %d / %d, want 0 / 0", cur.CoreMemoryMB, cur.CoreCPUPercent)
			}
			// Nothing about Core limits travels to 妙妙屋.
			if _, err := r.SharedLandingDesired(s.ID, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLandingCoreLimitValidation(t *testing.T) {
	for _, tt := range []struct {
		source   string
		mem, cpu int
		ok       bool
	}{
		{"native", 256, 50, true},
		{"native", 64, 1, true},
		{"native", 0, 0, false},
		{"native", 63, 50, false},
		{"native", 256, 0, false},
		{"shared", 0, 0, true},
		{"shared", 256, 50, true},
		{"shared", -1, 0, false},
		{"shared", 0, -1, false},
	} {
		s := &model.LandingServer{Source: tt.source, CoreMemoryMB: tt.mem, CoreCPUPercent: tt.cpu}
		if got := validLandingCoreLimits(s); got != tt.ok {
			t.Fatalf("%s %d/%d: got %v want %v", tt.source, tt.mem, tt.cpu, got, tt.ok)
		}
	}
}

// The startup data step: shared rows → 0 / 0, native rows untouched, a
// second start changes nothing.
func TestSharedCoreUnlimitedMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			servers := []model.LandingServer{
				{ID: 1, Source: "native", NodeID: 21, Name: "Breadcloud JP", PublicHost: "87.76.190.35", CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", CreatedTime: now, UpdatedTime: now},
				{ID: 2, Source: "native", NodeID: 55, Name: "NexKR", PublicHost: "203.0.113.55", CoreMemoryMB: 384, CoreCPUPercent: 70, Status: "active", CreatedTime: now, UpdatedTime: now},
				{ID: 4, Source: "shared", RemoteServerID: 4, Name: "Dmit T1", PublicHost: "dmit.example", CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", CreatedTime: now, UpdatedTime: now},
				{ID: 6, Source: "shared", RemoteServerID: 6, Name: "PQS TW", PublicHost: "pqs.example", CoreMemoryMB: 128, CoreCPUPercent: 50, Status: "active", CreatedTime: now, UpdatedTime: now},
			}
			for i := range servers {
				if err := r.db.Create(&servers[i]).Error; err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := sharedCoreUnlimitedMigration(r.db); err != nil {
					t.Fatal(err)
				}
				want := map[int64][2]int{1: {256, 50}, 2: {384, 70}, 4: {0, 0}, 6: {0, 0}}
				for id, w := range want {
					cur := reloadServer(t, r, id)
					if cur.CoreMemoryMB != w[0] || cur.CoreCPUPercent != w[1] {
						t.Fatalf("run %d server %d: %d / %d, want %d / %d", i+1, id, cur.CoreMemoryMB, cur.CoreCPUPercent, w[0], w[1])
					}
				}
			}
		})
	}
}
