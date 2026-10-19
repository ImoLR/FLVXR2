package repo

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

func grantedServerIDs(t *testing.T, r *Repository, userID int64) []int64 {
	t.Helper()
	ss, err := r.ListLandingServers(userID, false)
	if err != nil {
		t.Fatal(err)
	}
	out := []int64{}
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Fixture users 501/502 have role 0; the grant tests need real non-admin users.
func seedGrantUsers(t *testing.T, r *Repository) {
	t.Helper()
	future := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	orphanSeed(t, r,
		&model.User{ID: 511, User: "group-only", Status: 1, RoleID: 1, ExpTime: future, Flow: 99999, FlowResetTime: 1},
		&model.User{ID: 512, User: "ungranted", Status: 1, RoleID: 1, ExpTime: future, Flow: 99999, FlowResetTime: 1},
		&model.User{ID: 513, User: "direct-only", Status: 1, RoleID: 1, ExpTime: future, Flow: 99999, FlowResetTime: 1},
		&model.User{ID: 514, User: "second-admin", Status: 1, RoleID: 0, ExpTime: future, Flow: 99999, FlowResetTime: 1},
	)
}

// Plan 110: visibility and node creation follow one rule — direct grant OR an
// enabled user-group grant; admins see everything; the owner's access decides.
func TestLandingDirectGrantVisibility(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect) // group 701 is granted on s
			seedGrantUsers(t, r)
			orphanSeed(t, r, &model.UserGroupUser{UserGroupID: 701, UserID: 511})
			if !r.db.Migrator().HasTable(&model.LandingServerUser{}) || !r.db.Migrator().HasIndex(&model.LandingServerUser{}, "idx_landing_server_user_user") {
				t.Fatal("landing_server_user table/index missing")
			}
			shared := model.LandingServer{Source: "shared", RemoteServerID: 9, Name: "shared", PublicHost: "198.51.100.9", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022"]`, Status: "active", CreatedTime: 1, UpdatedTime: 1, SharedCaps: `{"server_id":9,"status":"active","port_min":30000,"port_max":30010,"protocols":["ss2022"]}`}
			orphanSeed(t, r, &shared)

			// group only
			if got := grantedServerIDs(t, r, 511); !sameIDs(got, []int64{s.ID}) {
				t.Fatalf("group member sees %v", got)
			}
			// none
			if got := grantedServerIDs(t, r, 512); len(got) != 0 {
				t.Fatalf("ungranted user sees %v", got)
			}
			if _, err := r.CreateLandingNode(512, s.ID, "x", "ss2022", false); err == nil || err.Error() != "无权使用该落地服务器" {
				t.Fatalf("ungranted create: %v", err)
			}
			// direct only (via the user settings save)
			ids := []int64{shared.ID}
			if err := r.SaveLandingUserSettingWithGrants(513, 2, nil, 0, &ids); err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 513); !sameIDs(got, []int64{shared.ID}) {
				t.Fatalf("direct grant sees %v", got)
			}
			if got, _ := r.LandingUserServerIDs(513); !sameIDs(got, []int64{shared.ID}) {
				t.Fatalf("direct grant ids %v", got)
			}
			if ok, err := r.UserCanUseLandingServer(513, shared.ID); err != nil || !ok {
				t.Fatalf("direct permission %v %v", ok, err)
			}
			if ok, _ := r.UserCanUseLandingServer(513, s.ID); ok {
				t.Fatal("direct grant leaked to another server")
			}
			if _, err := r.CreateLandingNode(513, shared.ID, "direct", "ss2022", false); err != nil {
				t.Fatalf("direct grant create: %v", err)
			}
			// admin sees all and needs no grant
			all, err := r.ListLandingServers(514, true)
			if err != nil || len(all) != 2 {
				t.Fatalf("admin list %d %v", len(all), err)
			}
			if _, err := r.CreateLandingNode(514, shared.ID, "admin own", "ss2022", true); err != nil {
				t.Fatalf("admin create: %v", err)
			}
			// a disabled group no longer grants; a direct grant is independent of groups
			if err := r.db.Model(&model.UserGroup{}).Where("id = ?", 701).Update("status", 0).Error; err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 511); len(got) != 0 {
				t.Fatalf("disabled group still grants %v", got)
			}
			// disabled servers stay hidden from users even with a grant
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", shared.ID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 513); len(got) != 0 {
				t.Fatalf("disabled server visible %v", got)
			}
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", shared.ID).Update("status", "active").Error; err != nil {
				t.Fatal(err)
			}
			// revoke = empty selection
			empty := []int64{}
			if err := r.SaveLandingUserSettingWithGrants(513, 2, nil, 0, &empty); err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 513); len(got) != 0 {
				t.Fatalf("revoked user sees %v", got)
			}
			// unknown server id is rejected without partial writes
			bad := []int64{shared.ID, 999999}
			if err := r.SaveLandingUserSettingWithGrants(513, 2, nil, 0, &bad); err == nil {
				t.Fatal("unknown server granted")
			}
			if got, _ := r.LandingUserServerIDs(513); len(got) != 0 {
				t.Fatalf("failed save left grants %v", got)
			}
		})
	}
}

// An admin creating for another user is checked against that user's grant and
// limits (the lifecycle job would disable an ungranted node immediately).
func TestLandingCreateForUserUsesOwnerAccess(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			seedGrantUsers(t, r)
			_, err := r.CreateLandingNode(512, s.ID, "for 512", "ss2022", true)
			if err == nil || !strings.Contains(err.Error(), "该用户无权使用此落地服务器") || !strings.Contains(err.Error(), "可用落地服务器") {
				t.Fatalf("admin create for ungranted user: %v", err)
			}
			ids := []int64{s.ID}
			if err := r.SaveLandingUserSettingWithGrants(512, 1, nil, 0, &ids); err != nil {
				t.Fatal(err)
			}
			n, err := r.CreateLandingNode(512, s.ID, "for 512", "ss2022", true)
			if err != nil || n.UserID != 512 {
				t.Fatalf("admin create for granted user: %+v %v", n, err)
			}
			if _, err := r.CreateLandingNode(512, s.ID, "over limit", "ss2022", true); err == nil || !strings.Contains(err.Error(), "用户落地节点上限") {
				t.Fatalf("owner max nodes not applied: %v", err)
			}
			// lifecycle keeps a directly granted node active
			if _, err := r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			if got, _ := r.GetLandingNode(n.ID); got.Status != "active" || got.AutoDisabled {
				t.Fatalf("direct grant node disabled by lifecycle: %+v", got)
			}
			// revoking disables it automatically; re-granting restores it
			empty := []int64{}
			if err := r.SaveLandingUserSettingWithGrants(512, 1, nil, 0, &empty); err != nil {
				t.Fatal(err)
			}
			if _, err := r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			if got, _ := r.GetLandingNode(n.ID); got.Status != "disabled" || !got.AutoDisabled {
				t.Fatalf("revoked node not auto-disabled: %+v", got)
			}
			if err := r.SaveLandingUserSettingWithGrants(512, 1, nil, 0, &ids); err != nil {
				t.Fatal(err)
			}
			if _, err := r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			if got, _ := r.GetLandingNode(n.ID); got.Status != "active" {
				t.Fatalf("re-granted node not restored: %+v", got)
			}
			// user delete cascades the grant
			if err := r.DeleteUserCascade(512); err != nil {
				t.Fatal(err)
			}
			var count int64
			r.db.Model(&model.LandingServerUser{}).Where("user_id = ?", 512).Count(&count)
			if count != 0 {
				t.Fatal("user delete left grants")
			}
		})
	}
}

// Adding a landing-kind server quota grants the server once; a later revoke
// keeps the quota row and is not undone by saving the same quota again.
func TestLandingQuotaAutoGrant(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := landingRepoFixture(t, dialect)
			seedGrantUsers(t, r)
			share := landingcustom.Share{ID: 5, Name: "Boil Hinet", Hosts: landingcustom.Hosts{IPv4: "198.51.100.5"}, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022"}}
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			var shared model.LandingServer
			if err := r.db.Where("source = 'shared'").First(&shared).Error; err != nil {
				t.Fatal(err)
			}
			quota := []model.UserServerQuota{{ServerKind: "landing", ServerRefID: shared.ID, LimitBytes: 1000 << 30}}
			if err := r.SaveLandingUserSetting(512, 2, quota, 0); err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 512); !sameIDs(got, []int64{shared.ID}) {
				t.Fatalf("quota row did not grant: %v", got)
			}
			// explicit revoke keeps the quota row
			empty := []int64{}
			if err := r.SaveLandingUserSettingWithGrants(512, 2, quota, 0, &empty); err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 512); len(got) != 0 {
				t.Fatalf("revoke with existing quota re-granted: %v", got)
			}
			var rows int64
			r.db.Model(&model.UserServerQuota{}).Where("user_id = ? AND server_kind = 'landing'", 512).Count(&rows)
			if rows != 1 {
				t.Fatalf("quota row removed by revoke: %d", rows)
			}
			if err := r.SaveLandingUserSetting(512, 2, quota, 0); err != nil {
				t.Fatal(err)
			}
			if got := grantedServerIDs(t, r, 512); len(got) != 0 {
				t.Fatalf("re-saving an existing quota re-granted: %v", got)
			}
			if err := r.SaveLandingUserSetting(999999, 0, nil, 0); err == nil {
				t.Fatal("unknown user accepted")
			}
		})
	}
}

// One-time backfill turns existing landing-kind quotas into grants; it ignores
// rows whose user/server is gone and never runs again after a revoke.
func TestLandingGrantBackfill(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			seedGrantUsers(t, r)
			now := time.Now().UnixMilli()
			orphanSeed(t, r,
				&model.UserServerQuota{UserID: 512, ServerKind: "landing", ServerRefID: s.ID, LimitBytes: 1, LimitOverride: true, CreatedTime: now, UpdatedTime: now},
				&model.UserServerQuota{UserID: 512, ServerKind: "node", ServerRefID: 601, CreatedTime: now, UpdatedTime: now},
				&model.UserServerQuota{UserID: 512, ServerKind: "landing", ServerRefID: 424242, CreatedTime: now, UpdatedTime: now},
				&model.UserServerQuota{UserID: 777777, ServerKind: "landing", ServerRefID: s.ID, CreatedTime: now, UpdatedTime: now},
			)
			// The repository already ran the backfill at open time on an empty DB.
			if err := r.db.Where("name = ?", landingServerUserBackfillKey).Delete(&model.ViteConfig{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := backfillLandingServerUsers(r.db); err != nil {
				t.Fatal(err)
			}
			var grants []model.LandingServerUser
			r.db.Order("user_id").Find(&grants)
			if len(grants) != 1 || grants[0].UserID != 512 || grants[0].LandingServerID != s.ID || grants[0].CreatedTime == 0 {
				t.Fatalf("backfill grants %+v", grants)
			}
			if err := backfillLandingServerUsers(r.db); err != nil {
				t.Fatal(err)
			}
			empty := []int64{}
			if err := r.SaveLandingUserSettingWithGrants(512, 5, nil, 0, &empty); err != nil {
				t.Fatal(err)
			}
			if err := backfillLandingServerUsers(r.db); err != nil {
				t.Fatal(err)
			}
			var count int64
			r.db.Model(&model.LandingServerUser{}).Count(&count)
			if count != 0 {
				t.Fatal("backfill re-ran after revoke")
			}
		})
	}
}

// Native server removal (node delete) cascades direct grants.
func TestLandingServerDeleteCascadesGrants(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			seedGrantUsers(t, r)
			ids := []int64{s.ID}
			if err := r.SaveLandingUserSettingWithGrants(512, 5, nil, 0, &ids); err != nil {
				t.Fatal(err)
			}
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteNodeCascade(601); err != nil {
				t.Fatal(err)
			}
			var count int64
			r.db.Model(&model.LandingServerUser{}).Count(&count)
			if count != 0 {
				t.Fatal("server delete left grants")
			}
		})
	}
}

// The shared public host follows Custom only while it is still the value Custom
// picked; a DDNS IP switches to the domain once Custom sends one, and an admin's
// manual edit is kept.
func TestSharedLandingPublicHostFollowsDomain(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			base := func(id int64, hosts landingcustom.Hosts) landingcustom.Share {
				return landingcustom.Share{ID: id, Name: "s", Hosts: hosts, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022"}}
			}
			host := func(remote int64) string {
				var s model.LandingServer
				if err := r.db.Where("source = 'shared' AND remote_server_id = ?", remote).First(&s).Error; err != nil {
					t.Fatal(err)
				}
				return s.PublicHost
			}
			sync := func(shares ...landingcustom.Share) {
				t.Helper()
				if err := r.SyncSharedLandings(shares, now); err != nil {
					t.Fatal(err)
				}
			}
			// 5: auto IP; 7: admin edits to their own domain
			sync(base(5, landingcustom.Hosts{IPv4: "114.41.205.96", IPv6: "2001:db8::5"}), base(7, landingcustom.Hosts{IPv4: "143.20.196.111"}))
			if host(5) != "114.41.205.96" || host(7) != "143.20.196.111" {
				t.Fatalf("initial hosts %s %s", host(5), host(7))
			}
			if err := r.db.Model(&model.LandingServer{}).Where("remote_server_id = ?", 7).Update("public_host", "boilhinet2.imgamer.top").Error; err != nil {
				t.Fatal(err)
			}
			// DDNS IP changes, still no domain: the auto host follows the IP
			sync(base(5, landingcustom.Hosts{IPv4: "114.41.205.97", IPv6: "2001:db8::5"}), base(7, landingcustom.Hosts{IPv4: "143.20.196.112"}))
			if host(5) != "114.41.205.97" {
				t.Fatalf("auto IP not followed: %s", host(5))
			}
			if host(7) != "boilhinet2.imgamer.top" {
				t.Fatalf("manual edit overwritten by IP: %s", host(7))
			}
			// Custom starts sending the domain: auto IP switches, manual stays
			sync(base(5, landingcustom.Hosts{IPv4: "114.41.205.98", Domain: "BoilHinet2.imgamer.top"}), base(7, landingcustom.Hosts{IPv4: "143.20.196.112", Domain: "us.example.net"}))
			if host(5) != "BoilHinet2.imgamer.top" {
				t.Fatalf("auto IP did not switch to domain: %s", host(5))
			}
			if host(7) != "boilhinet2.imgamer.top" {
				t.Fatalf("manual edit overwritten by domain: %s", host(7))
			}
			// domain case/whitespace differences still count as the auto value
			if err := r.db.Model(&model.LandingServer{}).Where("remote_server_id = ?", 5).Update("public_host", "boilhinet2.imgamer.top").Error; err != nil {
				t.Fatal(err)
			}
			sync(base(5, landingcustom.Hosts{IPv4: "114.41.205.98", Domain: "hinet-new.imgamer.top"}), base(7, landingcustom.Hosts{IPv4: "143.20.196.112", Domain: "us.example.net"}))
			if host(5) != "hinet-new.imgamer.top" {
				t.Fatalf("normalized auto domain not followed: %s", host(5))
			}
		})
	}
}
