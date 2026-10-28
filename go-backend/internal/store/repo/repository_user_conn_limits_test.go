package repo

import (
	"encoding/json"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 158: per-server 入站连接数 save rules, the coordinator inputs (limited
// users, caps, capable landing slots, relay nodes) and the forward-record caps.
func TestUserServerConnCapsSaveAndPlanInputs(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			// A node of user 501 on the native server (node 601).
			n, err := r.CreateLandingNode(501, s.ID, "own", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			// Invalid values are refused.
			for _, bad := range []int{-1, MaxUserServerConnections + 1} {
				if err := r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "node", ServerRefID: 601, MaxInboundConnections: bad}}, 0); err == nil {
					t.Fatalf("入站连接数 %d accepted", bad)
				}
			}
			if err := r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "node", ServerRefID: 601, LimitBytes: 5 << 30, MaxInboundConnections: 7}}, 0); err != nil {
				t.Fatal(err)
			}
			caps, err := r.UserServerConnCaps(501)
			if err != nil || caps[ServerQuotaKey{UserID: 501, Kind: "node", RefID: 601}] != 7 {
				t.Fatalf("caps %+v %v", caps, err)
			}
			// followDefault: only 入站连接数 changes, the row keeps following the default.
			orphanSeed(t, r, &model.Node{ID: 603, Name: "entry", ServerIP: "198.51.100.3", Port: "20000-20010", Status: 1})
			if err := r.db.Create(&model.UserServerQuota{UserID: 501, ServerKind: "node", ServerRefID: 603, LimitBytes: 9, CreatedTime: 1, UpdatedTime: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "node", ServerRefID: 603, LimitBytes: 0, MaxInboundConnections: 3, FollowDefault: true}}, 0); err != nil {
				t.Fatal(err)
			}
			var row model.UserServerQuota
			if err := r.db.Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", 501, "node", 603).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.LimitOverride || row.LimitBytes != 9 || row.MaxInboundConnections != 3 {
				t.Fatalf("followDefault row = %+v, want default kept and 3 connections", row)
			}

			// No capable agent yet: no landing slot, but user 501 is limited (caps).
			ex, err := r.LoadQuotaPlanExtras()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := ex.Users[501]; !ok || len(ex.Landing) != 0 || ex.Caps[ServerQuotaKey{UserID: 501, Kind: "node", RefID: 603}] != 3 {
				t.Fatalf("extras before capability: %+v", ex)
			}
			if _, ok := ex.Users[502]; ok {
				t.Fatal("unlimited user listed")
			}
			// Capable agent + a linked rule dialled by two nodes (entries of a port forward).
			if err := r.db.Model(&model.Node{}).Where("id = ?", 601).Update("landing_user_limits", LandingUserLimitsVersion).Error; err != nil {
				t.Fatal(err)
			}
			orphanSeed(t, r,
				&model.Tunnel{ID: 911, Name: "pf", Type: 1, Status: 1, Flow: 1, TrafficRatio: 1, CreatedTime: 1, UpdatedTime: 1},
				&model.Forward{ID: 912, UserID: 501, UserName: "landing-owner", Name: "to-own", TunnelID: 911, RemoteAddr: "203.0.113.1:1", Strategy: "fifo", Status: 1, LandingNodeID: n.ID, CreatedTime: 1, UpdatedTime: 1},
				&model.ForwardPort{ForwardID: 912, NodeID: 603, Port: 20001},
				&model.ForwardPort{ForwardID: 912, NodeID: 601, Port: 10001},
			)
			ex, err = r.LoadQuotaPlanExtras()
			if err != nil {
				t.Fatal(err)
			}
			if len(ex.Landing) != 1 || ex.Landing[0].UserID != 501 || ex.Landing[0].NodeID != 601 || ex.Landing[0].Shared || ex.Landing[0].RelayNodes != 2 {
				t.Fatalf("native slot = %+v, want user 501 on node 601 with 2 relay nodes", ex.Landing)
			}
			// A paused node is no slot.
			if err := r.db.Model(&model.LandingNode{}).Where("id = ?", n.ID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if ex, _ = r.LoadQuotaPlanExtras(); len(ex.Landing) != 0 {
				t.Fatalf("disabled node gave a slot: %+v", ex.Landing)
			}
			if err := r.db.Model(&model.LandingNode{}).Where("id = ?", n.ID).Update("status", "active").Error; err != nil {
				t.Fatal(err)
			}

			// Shared server: a slot only with user_limits v1.
			caps1, _ := json.Marshal(landingcustom.Share{ID: 9, Status: "active"})
			shared := model.LandingServer{Source: "shared", RemoteServerID: 9, Name: "share", PublicHost: "203.0.113.50", PortMin: 30000, PortMax: 30010, Status: "active", SharedCaps: string(caps1), CreatedTime: 1, UpdatedTime: 1}
			if err := r.db.Create(&shared).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Create(&model.LandingNode{UserID: 501, LandingServerID: shared.ID, Name: "s", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-77", Status: "active", CreatedTime: 1, UpdatedTime: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if ex, _ = r.LoadQuotaPlanExtras(); len(ex.Landing) != 1 {
				t.Fatalf("share without the capability gave a slot: %+v", ex.Landing)
			}
			caps2, _ := json.Marshal(landingcustom.Share{ID: 9, Status: "active", UserLimits: &landingcustom.ShareUserLimits{Versions: []int{1}}})
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", shared.ID).Update("shared_caps", string(caps2)).Error; err != nil {
				t.Fatal(err)
			}
			ex, _ = r.LoadQuotaPlanExtras()
			if len(ex.Landing) != 2 || !ex.Landing[1].Shared || ex.Landing[1].ServerID != shared.ID {
				t.Fatalf("shared slot = %+v", ex.Landing)
			}

			// Forward records carry the entry caps (one query per batch).
			fr, err := r.GetForwardRecord(912)
			if err != nil || fr == nil || fr.UserServerConnCaps[603] != 3 || fr.UserServerConnCaps[601] != 7 {
				t.Fatalf("forward caps = %+v %v", fr, err)
			}
			if got := LandingServerQuotaKey(501, &shared); got != (ServerQuotaKey{UserID: 501, Kind: "landing", RefID: shared.ID}) {
				t.Fatalf("shared quota key %+v", got)
			}
			if got := LandingServerQuotaKey(501, s); got != (ServerQuotaKey{UserID: 501, Kind: "node", RefID: 601}) {
				t.Fatalf("native quota key %+v", got)
			}
		})
	}
}
