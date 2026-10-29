package repo

import (
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 161: per-user 速率 on FLVX-native landing servers — the rates the
// landing sync sends (row of the server's node, else the user default), the
// agent capability and the kernel error shown on the user's row.

func TestNativeLandingUserRates(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			// Native landing server 8 on node 5 (US-landing).
			orphanSeed(t, r, &model.LandingServer{ID: 8, Source: "native", NodeID: 5, Name: "US", Status: "active"})
			for _, n := range []model.LandingNode{
				{ID: 41, LandingServerID: 8, UserID: 2, Name: "a1", Protocol: "ss2022", Port: 31001, Status: "active"},
				{ID: 42, LandingServerID: 8, UserID: 2, Name: "a2", Protocol: "vless_reality_vision", Port: 31002, Status: "active"},
				{ID: 43, LandingServerID: 8, UserID: 3, Name: "b1", Protocol: "ss2022", Port: 31003, Status: "active"},
				{ID: 44, LandingServerID: 8, UserID: 9, Name: "gone", Protocol: "ss2022", Port: 31004, Status: "active", DeletePending: true},
			} {
				n.CreatedTime, n.UpdatedTime = 1, 1
				orphanSeed(t, r, &n)
			}
			save := func(userID int64, quotas ...model.UserServerQuota) {
				t.Helper()
				if err := r.SaveLandingUserSettingFull(userID, LandingMaxNodesFollowGlobal, quotas, 0, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			row := func(ref int64, override bool, rate int) model.UserServerQuota {
				return model.UserServerQuota{ServerKind: "node", ServerRefID: ref, FollowDefault: true, RateOverride: override, RateMbps: rate}
			}
			rates := func() map[int64]int {
				t.Helper()
				v, err := r.NativeLandingUserRates(8, 5)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			// Upgrade: nothing set → nothing sent.
			if v := rates(); len(v) != 0 {
				t.Fatalf("fresh %v", v)
			}
			// Default of alice, override of bob on this node; a row of another
			// node and the default of a user whose only node is being deleted
			// change nothing.
			if err := r.SetUserServerRate(2, 20); err != nil {
				t.Fatal(err)
			}
			if err := r.SetUserServerRate(9, 70); err != nil {
				t.Fatal(err)
			}
			save(3, row(5, true, 50), row(1, true, 5))
			if v := rates(); len(v) != 2 || v[2] != 20 || v[3] != 50 {
				t.Fatalf("default + override %v", v)
			}
			// The same row is the user's rate on that machine for forward entries.
			if v, _ := r.UserServerRateMbps(3, 5); v != 50 {
				t.Fatalf("row value %d", v)
			}
			// An explicit 0 on the row = 不限 even with a default.
			save(2, row(5, true, 0))
			if v := rates(); len(v) != 1 || v[3] != 50 {
				t.Fatalf("explicit unlimited %v", v)
			}
			// Back to following the default.
			save(2, row(5, false, 0))
			if v := rates(); v[2] != 20 {
				t.Fatalf("follow default %v", v)
			}
			// Wrong server / node never mixes.
			if v, _ := r.NativeLandingUserRates(8, 1); v[3] != 5 || v[2] != 20 {
				t.Fatalf("other node's rows %v", v)
			}
			if v, _ := r.NativeLandingUserRates(99, 5); len(v) != 0 {
				t.Fatalf("unknown server %v", v)
			}
		})
	}
}

func TestLandingRateCapabilityAndRow(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			orphanSeed(t, r, &model.LandingServer{ID: 8, Source: "native", NodeID: 5, Name: "US", Status: "active"})
			orphanSeed(t, r, &model.LandingNode{ID: 41, LandingServerID: 8, UserID: 2, Name: "a1", Protocol: "ss2022", Port: 31001, Status: "active", CreatedTime: 1, UpdatedTime: 1})
			row := func() RelatedServer {
				t.Helper()
				list, err := r.UserRelatedServers(2)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range list {
					if v.ServerKind == "node" && v.ServerRefID == 5 {
						if !v.Landing || v.Entry {
							t.Fatalf("roles %+v", v)
						}
						return v
					}
				}
				t.Fatalf("native landing row missing in %+v", list)
				return RelatedServer{}
			}
			if v := row(); !v.LandingRateOldAgent || v.LandingRateError != "" {
				t.Fatalf("old agent %+v", v)
			}
			if err := r.SetNodeLandingRateCapability(5, LandingRateVersion); err != nil {
				t.Fatal(err)
			}
			if v := row(); v.LandingRateOldAgent {
				t.Fatalf("capable %+v", v)
			}
			long := strings.Repeat("内核", 400)
			if err := r.RecordLandingRateError(8, long); err != nil {
				t.Fatal(err)
			}
			v := row()
			if v.LandingRateOldAgent || len([]rune(v.LandingRateError)) != 300 {
				t.Fatalf("error %d runes", len([]rune(v.LandingRateError)))
			}
			// An older agent again (rollback): its stale error is not shown.
			if err := r.SetNodeLandingRateCapability(5, 0); err != nil {
				t.Fatal(err)
			}
			if v := row(); !v.LandingRateOldAgent || v.LandingRateError != "" {
				t.Fatalf("rolled back %+v", v)
			}
			if err := r.RecordLandingRateError(8, ""); err != nil {
				t.Fatal(err)
			}
			var s model.LandingServer
			if err := r.db.Select("id", "rate_error").First(&s, 8).Error; err != nil || s.RateError != "" {
				t.Fatalf("cleared %q %v", s.RateError, err)
			}
			var n model.Node
			if err := r.db.Select("id", "landing_rate").First(&n, 5).Error; err != nil || n.LandingRate != 0 {
				t.Fatalf("capability %d %v", n.LandingRate, err)
			}
		})
	}
}
