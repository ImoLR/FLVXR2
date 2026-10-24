package repo

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 144 fixture:
//
//	tunnel 10 entries 11 深圳 + 12 广州; group 101 grants entry 11, group 103 entry 12
//	tunnel 20 entry 13; groups 103 and 104 (104 = 外部组, never granted to users)
//	tunnel 30 entry 11; no group
//	tunnel 40 no entry nodes; group 101, no entry rows
//	tunnel 50 entry 11; only in disabled group 102
//	tunnel 60 entry 14; group 101 member without entry rows (legacy)
func pickerGroupFixture(t *testing.T, dialect string) *Repository {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	tg := func(id int64, name string, inx, status int) *model.TunnelGroup {
		return &model.TunnelGroup{ID: id, Name: name, Color: "#10b981", Inx: inx, Status: status, CreatedTime: 1, UpdatedTime: 1}
	}
	orphanSeed(t, r,
		&model.User{ID: 2, User: "two-groups", RoleID: 1},
		&model.User{ID: 3, User: "direct-first", RoleID: 1},
		&model.User{ID: 4, User: "package-user", RoleID: 1},
		&model.Node{ID: 11, Name: "entry-sz", RegionCity: "深圳"},
		&model.Node{ID: 12, Name: "entry-gz", RegionCity: "广州"},
		&model.Node{ID: 13, Name: "entry-sh", RegionCity: "上海"},
		&model.Node{ID: 14, Name: "entry-bj", RegionCity: "北京"},
		&model.Tunnel{ID: 10, Name: "t10", Type: 1, Status: 1, Inx: 1},
		&model.Tunnel{ID: 20, Name: "t20", Type: 1, Status: 1, Inx: 2},
		&model.Tunnel{ID: 30, Name: "t30", Type: 1, Status: 1, Inx: 3},
		&model.Tunnel{ID: 40, Name: "t40", Type: 1, Status: 1, Inx: 4},
		&model.Tunnel{ID: 50, Name: "t50", Type: 1, Status: 1, Inx: 5},
		&model.Tunnel{ID: 60, Name: "t60", Type: 1, Status: 1, Inx: 6},
		&model.ChainTunnel{TunnelID: 10, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 10, ChainType: "1", NodeID: 12},
		&model.ChainTunnel{TunnelID: 20, ChainType: "1", NodeID: 13},
		&model.ChainTunnel{TunnelID: 30, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 50, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 60, ChainType: "1", NodeID: 14},
		tg(101, "沪日440", 2, 1), tg(102, "禁用组", 0, 0), tg(103, "深港", 1, 1), tg(104, "外部组", 0, 1),
		&model.TunnelGroupTunnel{TunnelGroupID: 101, TunnelID: 10, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 103, TunnelID: 10, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 103, TunnelID: 20, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 104, TunnelID: 20, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 101, TunnelID: 40, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 102, TunnelID: 50, CreatedTime: 1},
		&model.TunnelGroupTunnel{TunnelGroupID: 101, TunnelID: 60, CreatedTime: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 101, TunnelID: 10, NodeID: 11, CreatedTime: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 103, TunnelID: 10, NodeID: 12, CreatedTime: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 103, TunnelID: 20, NodeID: 13, CreatedTime: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 104, TunnelID: 20, NodeID: 13, CreatedTime: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 102, TunnelID: 50, NodeID: 11, CreatedTime: 1},
		// user 2: 沪日440 + 深港 (+ disabled group) through user groups, 30 direct.
		&model.UserTunnel{ID: 210, UserID: 2, TunnelID: 10, Status: 1},
		&model.UserTunnel{ID: 220, UserID: 2, TunnelID: 20, Status: 1},
		&model.UserTunnel{ID: 230, UserID: 2, TunnelID: 30, Status: 1},
		&model.UserTunnel{ID: 240, UserID: 2, TunnelID: 40, Status: 1},
		&model.UserTunnel{ID: 250, UserID: 2, TunnelID: 50, Status: 1},
		&model.GroupPermissionGrant{UserGroupID: 9, TunnelGroupID: 101, UserTunnelID: 210, CreatedByGroup: 1, CreatedTime: 1},
		&model.GroupPermissionGrant{UserGroupID: 9, TunnelGroupID: 103, UserTunnelID: 210, CreatedByGroup: 1, CreatedTime: 1},
		&model.GroupPermissionGrant{UserGroupID: 9, TunnelGroupID: 103, UserTunnelID: 220, CreatedByGroup: 1, CreatedTime: 1},
		&model.GroupPermissionGrant{UserGroupID: 9, TunnelGroupID: 101, UserTunnelID: 240, CreatedByGroup: 1, CreatedTime: 1},
		&model.GroupPermissionGrant{UserGroupID: 9, TunnelGroupID: 102, UserTunnelID: 250, CreatedByGroup: 1, CreatedTime: 1},
		// user 3: direct grants first, then 沪日440 attached (created_by_group 0) → unrestricted.
		&model.UserTunnel{ID: 310, UserID: 3, TunnelID: 10, Status: 1},
		&model.UserTunnel{ID: 360, UserID: 3, TunnelID: 60, Status: 1},
		&model.GroupPermissionGrant{UserGroupID: 8, TunnelGroupID: 101, UserTunnelID: 310, CreatedByGroup: 0, CreatedTime: 1},
		&model.GroupPermissionGrant{UserGroupID: 8, TunnelGroupID: 101, UserTunnelID: 360, CreatedByGroup: 0, CreatedTime: 1},
		// user 4: active package with 深港, an old inactive package with 沪日440.
		&model.UserTunnel{ID: 410, UserID: 4, TunnelID: 10, Status: 1},
		&model.UserTunnel{ID: 420, UserID: 4, TunnelID: 20, Status: 1},
		&model.PackageSubscription{ID: 1, UserID: 4, PackageID: 7, Status: 1, StartAt: 1, ExpireAt: 2, CreatedAt: 1, UpdatedAt: 1},
		&model.PackageSubscription{ID: 2, UserID: 4, PackageID: 8, Status: 0, StartAt: 1, ExpireAt: 2, CreatedAt: 1, UpdatedAt: 1},
		&model.SubscriptionPackageTunnelGroup{PackageID: 7, TunnelGroupID: 103},
		&model.SubscriptionPackageTunnelGroup{PackageID: 8, TunnelGroupID: 101},
	)
	// status has default:1, so a zero value is not inserted by Create.
	if err := r.db.Model(&model.PackageSubscription{}).Where("id = ?", 2).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	var disabled int64
	r.db.Model(&model.TunnelGroup{}).Where("status = 0").Count(&disabled)
	if disabled != 1 {
		t.Fatalf("disabled groups=%d", disabled)
	}
	return r
}

// pickerSummary renders items as "tunnel:group=keys" with node/entry keys
// replaced by labels, e.g. "10:深港=广州".
func pickerSummary(t *testing.T, items []map[string]interface{}) string {
	t.Helper()
	parts := []string{}
	for _, item := range items {
		labels := map[string]string{}
		for _, g := range item["entryGroups"].([]tunnelEntryGroup) {
			labels[g.Key] = g.Label
		}
		groups, ok := item["groups"].([]TunnelPickerGroup)
		if !ok {
			t.Fatalf("item %v has no groups slice", item["id"])
		}
		names := []string{}
		for _, g := range groups {
			keys := []string{}
			for _, k := range g.EntryKeys {
				if labels[k] == "" {
					t.Fatalf("group %s key %s not in entryGroups", g.Name, k)
				}
				keys = append(keys, labels[k])
			}
			names = append(names, g.Name+"="+strings.Join(keys, "+"))
		}
		parts = append(parts, fmt.Sprintf("%v:%s", item["id"], strings.Join(names, ",")))
	}
	return strings.Join(parts, " ")
}

func stripGroups(items []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		c := map[string]interface{}{}
		for k, v := range item {
			if k != "groups" {
				c[k] = v
			}
		}
		out = append(out, c)
	}
	return out
}

func TestTunnelPickerGroups(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := pickerGroupFixture(t, dialect)

			admin, err := r.ListEnabledTunnelSummariesWithGroups()
			if err != nil {
				t.Fatal(err)
			}
			// Per-entry rows, multi-group tunnels, inx order, disabled 102 hidden,
			// 40 (no entries) covered with [] = all, 60 member without rows = all.
			want := "10:深港=entry-gz,沪日440=entry-sz 20:外部组=entry-sh,深港=entry-sh 30: 40:沪日440= 50: 60:沪日440=entry-bj"
			if got := pickerSummary(t, admin); got != want {
				t.Fatalf("admin\n got %s\nwant %s", got, want)
			}
			plain, err := r.ListEnabledTunnelSummaries()
			if err != nil || !reflect.DeepEqual(plain, stripGroups(admin)) {
				t.Fatalf("admin list changed beyond groups: %v", err)
			}
			if _, ok := plain[0]["groups"]; ok {
				t.Fatal("plain admin list must not carry groups")
			}

			// Two groups; 30 direct → 其他; 外部组 never shown; disabled group's
			// tunnel 50 stays visible but ungrouped.
			user, err := r.ListUserAccessibleTunnelsWithGroups(2)
			if err != nil {
				t.Fatal(err)
			}
			want = "10:深港=广州,沪日440=深圳 20:深港=上海 30: 40:沪日440= 50:"
			if got := pickerSummary(t, user); got != want {
				t.Fatalf("user 2\n got %s\nwant %s", got, want)
			}
			raw, _ := json.Marshal(user)
			if strings.Contains(string(raw), "外部组") || strings.Contains(string(raw), "禁用组") {
				t.Fatalf("foreign/disabled group leaked: %s", raw)
			}
			plainUser, err := r.ListUserAccessibleTunnels(2)
			if err != nil || !reflect.DeepEqual(plainUser, stripGroups(user)) {
				t.Fatalf("user list changed beyond groups: %v", err)
			}

			// Direct grant first: all entries visible, the group covers only its
			// entry row (广州 → 其他); legacy member without rows covers all.
			user3, err := r.ListUserAccessibleTunnelsWithGroups(3)
			if err != nil {
				t.Fatal(err)
			}
			want = "10:沪日440=深圳 60:沪日440=北京"
			if got := pickerSummary(t, user3); got != want {
				t.Fatalf("user 3\n got %s\nwant %s", got, want)
			}

			// Active package only.
			user4, err := r.ListUserAccessibleTunnelsWithGroups(4)
			if err != nil {
				t.Fatal(err)
			}
			want = "10:深港=广州 20:深港=上海"
			if got := pickerSummary(t, user4); got != want {
				t.Fatalf("user 4\n got %s\nwant %s", got, want)
			}

			// Revoking the 沪日440 grant on tunnel 10 moves 深圳 out of the list
			// (access) and out of the group, consistently.
			if err := r.db.Where("tunnel_group_id = 101 AND user_tunnel_id = 210").Delete(&model.GroupPermissionGrant{}).Error; err != nil {
				t.Fatal(err)
			}
			user, err = r.ListUserAccessibleTunnelsWithGroups(2)
			if err != nil {
				t.Fatal(err)
			}
			if got := pickerSummary(t, user); !strings.HasPrefix(got, "10:深港=广州 20:") {
				t.Fatalf("after revoke: %s", got)
			}
		})
	}
}
