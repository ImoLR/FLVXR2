package repo

import (
	"fmt"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// Plan 111 fixture: native server s (group 701: users 501 + 511) and a shared
// server granted directly to 513. 512 has no grant at all.
func landingMonitorFixture(t *testing.T, dialect string) (*Repository, *model.LandingServer, *model.LandingServer) {
	t.Helper()
	r, s := landingRepoFixture(t, dialect)
	seedGrantUsers(t, r)
	orphanSeed(t, r, &model.UserGroupUser{UserGroupID: 701, UserID: 511})
	shared := &model.LandingServer{Source: "shared", RemoteServerID: 9, Name: "妙妙屋 US", PublicHost: "198.51.100.9", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022"]`, Status: "active", DefaultUserQuotaBytes: 50 << 30, DefaultMaxConnections: 100, DefaultTrafficLimitBytes: 10 << 30, CreatedTime: 1, UpdatedTime: 1, SharedCaps: `{"server_id":9,"status":"active","port_min":30000,"port_max":30010,"protocols":["ss2022"],"max_connections":500,"traffic_limit_bytes":107374182400,"traffic_used_bytes":1073741824,"core":{"state":"running"}}`}
	orphanSeed(t, r, shared)
	ids := []int64{shared.ID}
	if err := r.SaveLandingUserSettingWithGrants(513, 3, nil, 0, &ids); err != nil {
		t.Fatal(err)
	}
	return r, s, shared
}

func monitorNodeIDs(d *LandingMonitorData) []int64 {
	out := []int64{}
	for _, n := range d.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func monitorServerIDs(d *LandingMonitorData) []int64 {
	out := []int64{}
	for _, s := range d.Servers {
		out = append(out, s.ID)
	}
	return out
}

func TestLandingMonitorSnapshotVisibility(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s, shared := landingMonitorFixture(t, dialect)
			now := time.Now()
			mine, err := r.CreateLandingNode(511, s.ID, "mine", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			other, err := r.CreateLandingNode(501, s.ID, "other user", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			onShared, err := r.CreateLandingNode(513, shared.ID, "shared", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			// 511 still owns a node on a server it can no longer use (revoked grant).
			stray := &model.LandingNode{UserID: 511, LandingServerID: shared.ID, Name: "revoked", Protocol: "ss2022", Port: 30009, InboundTag: "flvxl-stray", Status: "active", CreatedTime: 1, UpdatedTime: 1}
			orphanSeed(t, r, stray)

			admin, err := r.LandingMonitorSnapshot(514, true, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := monitorServerIDs(admin); !sameIDs(got, []int64{s.ID, shared.ID}) {
				t.Fatalf("admin servers %v", got)
			}
			if got := monitorNodeIDs(admin); !sameIDs(got, []int64{mine.ID, other.ID, onShared.ID, stray.ID}) {
				t.Fatalf("admin nodes %v", got)
			}
			if admin.OwnerNames[511] != "group-only" || admin.OwnerNames[501] != "landing-owner" || len(admin.Quotas) != 0 {
				t.Fatalf("admin owners %v quotas %v", admin.OwnerNames, admin.Quotas)
			}
			if !admin.NativeOnline[s.NodeID] {
				t.Fatalf("native node state %v", admin.NativeOnline)
			}

			user, err := r.LandingMonitorSnapshot(511, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := monitorServerIDs(user); !sameIDs(got, []int64{s.ID}) {
				t.Fatalf("group user servers %v", got)
			}
			if got := monitorNodeIDs(user); !sameIDs(got, []int64{mine.ID}) {
				t.Fatalf("group user nodes %v (other users' or revoked nodes leaked)", got)
			}
			if len(user.OwnerNames) != 0 {
				t.Fatalf("owner names leaked to user: %v", user.OwnerNames)
			}

			direct, err := r.LandingMonitorSnapshot(513, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if !sameIDs(monitorServerIDs(direct), []int64{shared.ID}) || !sameIDs(monitorNodeIDs(direct), []int64{onShared.ID}) {
				t.Fatalf("direct grant user %v %v", monitorServerIDs(direct), monitorNodeIDs(direct))
			}

			none, err := r.LandingMonitorSnapshot(512, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(none.Servers) != 0 || len(none.Nodes) != 0 {
				t.Fatalf("ungranted user sees %v %v", monitorServerIDs(none), monitorNodeIDs(none))
			}

			// A disabled server disappears for users but stays for admins.
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if user, _ = r.LandingMonitorSnapshot(511, false, now); len(user.Servers) != 0 || len(user.Nodes) != 0 {
				t.Fatalf("disabled server visible to user: %v", monitorServerIDs(user))
			}
			if admin, _ = r.LandingMonitorSnapshot(514, true, now); len(admin.Servers) != 2 {
				t.Fatalf("admin lost disabled server")
			}

			ids, err := r.ListLandingNodeIDs()
			if err != nil || !sameIDs(ids, []int64{mine.ID, other.ID, onShared.ID, stray.ID}) {
				t.Fatalf("node ids %v %v", ids, err)
			}
		})
	}
}

func TestLandingMonitorSnapshotUserQuota(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s, shared := landingMonitorFixture(t, dialect)
			now := time.Now()
			if err := r.SetNodeDefaultUserQuota(s.NodeID, 20<<30); err != nil {
				t.Fatal(err)
			}
			// 513 is granted the shared server: no row yet → server default, nothing used.
			d, err := r.LandingMonitorSnapshot(513, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if q := d.Quotas[shared.ID]; q.LimitBytes != 50<<30 || q.UsedBytes != 0 {
				t.Fatalf("default shared quota %+v", q)
			}
			period := LandingPeriodKey(1, now)
			orphanSeed(t, r,
				&model.UserServerQuota{UserID: 513, ServerKind: "landing", ServerRefID: shared.ID, LimitBytes: 10 << 30, LimitOverride: true, UsedIn: 1 << 30, UsedOut: 2 << 30, PeriodKey: period, Paused: false, CreatedTime: 1, UpdatedTime: 1},
				&model.UserServerQuota{UserID: 511, ServerKind: "node", ServerRefID: s.NodeID, LimitBytes: 1, LimitOverride: false, UsedIn: 5 << 30, UsedOut: 5 << 30, PeriodKey: period - 1, Paused: true, CreatedTime: 1, UpdatedTime: 1},
			)
			if d, err = r.LandingMonitorSnapshot(513, false, now); err != nil {
				t.Fatal(err)
			}
			if q := d.Quotas[shared.ID]; q.LimitBytes != 10<<30 || q.UsedBytes != 3<<30 || q.Paused {
				t.Fatalf("override shared quota %+v", q)
			}
			// Old-period row: nothing used this period; limit follows the node default.
			if d, err = r.LandingMonitorSnapshot(511, false, now); err != nil {
				t.Fatal(err)
			}
			if q := d.Quotas[s.ID]; q.LimitBytes != 20<<30 || q.UsedBytes != 0 || q.Paused {
				t.Fatalf("old period native quota %+v", q)
			}
		})
	}
}

// The snapshot must cost the same number of queries for 3 or 60 nodes (no N+1).
func TestLandingMonitorSnapshotQueryCountIsFixed(t *testing.T) {
	r, s, shared := landingMonitorFixture(t, "sqlite")
	count := 0
	if err := r.db.Callback().Query().After("gorm:query").Register("landing_monitor_count", func(*gorm.DB) { count++ }); err != nil {
		t.Fatal(err)
	}
	measure := func(userID int64, admin bool) int {
		count = 0
		if _, err := r.LandingMonitorSnapshot(userID, admin, time.Now()); err != nil {
			t.Fatal(err)
		}
		return count
	}
	seq := 0
	seed := func(n int) {
		for i := 0; i < n; i++ {
			for _, row := range []*model.LandingNode{
				{UserID: 511, LandingServerID: s.ID, Name: "n", Protocol: "ss2022", Port: 0, Status: "active", CreatedTime: 1, UpdatedTime: 1},
				{UserID: 513, LandingServerID: shared.ID, Name: "n", Protocol: "ss2022", Port: 0, Status: "active", CreatedTime: 1, UpdatedTime: 1},
			} {
				seq++
				row.Port = 40000 + seq
				row.InboundTag = fmt.Sprintf("flvxl-seed-%d", seq)
				orphanSeed(t, r, row)
			}
		}
	}
	seed(2)
	adminFew, userFew := measure(514, true), measure(511, false)
	seed(30)
	adminMany, userMany := measure(514, true), measure(511, false)
	if adminFew != adminMany || userFew != userMany || adminMany > 6 || userMany > 6 {
		t.Fatalf("query count grows with nodes: admin %d→%d user %d→%d", adminFew, adminMany, userFew, userMany)
	}
}
