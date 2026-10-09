package repo

import (
	"reflect"
	"testing"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func entryPermissionFixture(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	for _, row := range []interface{}{
		&model.User{ID: 2, User: "group-user", RoleID: 1},
		&model.User{ID: 3, User: "direct-user", RoleID: 1},
		&model.Tunnel{ID: 10, Name: "two entries", Type: 2, Status: 1},
		&model.Node{ID: 11, Name: "entry one", RegionCity: "深圳"},
		&model.Node{ID: 12, Name: "entry two", RegionCity: "广州"},
		&model.ChainTunnel{TunnelID: 10, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 10, ChainType: "1", NodeID: 12},
		&model.TunnelGroupTunnel{TunnelGroupID: 20, TunnelID: 10},
		&model.UserGroupUser{UserGroupID: 30, UserID: 2},
		&model.GroupPermission{UserGroupID: 30, TunnelGroupID: 20},
		&model.UserTunnel{ID: 40, UserID: 2, TunnelID: 10, Status: 1},
		&model.UserTunnel{ID: 41, UserID: 3, TunnelID: 10, Status: 1},
		&model.GroupPermissionGrant{UserGroupID: 30, TunnelGroupID: 20, UserTunnelID: 40, CreatedByGroup: 1},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func requireEntries(t *testing.T, r *Repository, user int64, want ...int64) {
	t.Helper()
	got, err := r.EffectiveTunnelEntryNodeIDs(user, 10)
	if err != nil || !reflect.DeepEqual(append([]int64{}, got...), append([]int64{}, want...)) {
		t.Fatalf("user %d entries=%v, err=%v, want=%v", user, got, err, want)
	}
}

func TestTunnelEntryBackfillIdempotent(t *testing.T) {
	r := entryPermissionFixture(t)
	if err := r.db.Where("name = ?", tunnelEntryBackfillKey).Delete(&model.ViteConfig{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTunnelGroupEntries(r.db); err != nil {
		t.Fatal(err)
	}
	requireEntries(t, r, 2, 11, 12)
	var count int64
	r.db.Model(&model.TunnelGroupTunnelEntry{}).Count(&count)
	if count != 2 {
		t.Fatalf("backfill rows=%d", count)
	}
	// A subsequent tunnel entry is not implicitly granted, including on restart.
	if err := r.db.Create(&model.ChainTunnel{TunnelID: 10, ChainType: "1", NodeID: 13}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTunnelGroupEntries(r.db); err != nil {
		t.Fatal(err)
	}
	requireEntries(t, r, 2, 11, 12)
	requireEntries(t, r, 1, 11, 12, 13)
	requireEntries(t, r, 3, 11, 12, 13)
	r.db.Model(&model.TunnelGroupTunnelEntry{}).Count(&count)
	if count != 2 {
		t.Fatalf("repeat backfill rows=%d", count)
	}
}

func TestTunnelEntryPermissionUnionAndOldAssignment(t *testing.T) {
	r := entryPermissionFixture(t)
	assign := func(group int64, selection ...map[int64][]int64) {
		t.Helper()
		if err := r.db.Transaction(func(tx *gorm.DB) error { return r.ReplaceTunnelGroupMembersTx(tx, group, []int64{10}, 1, selection...) }); err != nil {
			t.Fatal(err)
		}
	}
	assign(20, map[int64][]int64{10: {11}})
	requireEntries(t, r, 2, 11)
	items, err := r.ListUserAccessibleTunnels(2)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	groups := items[0]["entryGroups"].([]tunnelEntryGroup)
	if len(groups) != 1 || groups[0].Label != "深圳" {
		t.Fatalf("groups=%v", groups)
	}
	assign(21, map[int64][]int64{10: {12}})
	if err := r.InsertGroupPermission(30, 21, 1); err != nil {
		t.Fatal(err)
	}
	id, derived, err := r.EnsureUserTunnelGrant(2, 10)
	if err != nil || !derived {
		t.Fatalf("provenance lost: %v %v", derived, err)
	}
	r.InsertGroupPermissionGrant(30, 21, id, 1, 1)
	requireEntries(t, r, 2, 11, 12)
	assign(20, map[int64][]int64{10: {}})
	requireEntries(t, r, 2, 12)
	assign(21, map[int64][]int64{10: {}})
	requireEntries(t, r, 2)
	items, err = r.ListUserAccessibleTunnels(2)
	if err != nil || len(items) != 0 {
		t.Fatalf("empty entries still visible: %v %v", items, err)
	}
	assign(20) // Omitted entry selection is the original all-entry API.
	requireEntries(t, r, 2, 11, 12)
	// A real direct grant remains unrestricted even when groups are attached.
	r.InsertGroupPermissionGrant(30, 20, 41, 0, 1)
	requireEntries(t, r, 3, 11, 12)
}

func TestTunnelEntryRestrictionLegacyFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"missing user", "DELETE FROM user WHERE id = 2"},
		{"missing user tunnel", "DELETE FROM user_tunnel WHERE id = 40"},
		{"direct grant", "UPDATE group_permission_grant SET created_by_group = 0"},
		{"no chain entries", "DELETE FROM chain_tunnel WHERE tunnel_id = 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := entryPermissionFixture(t)
			if err := r.db.Exec(tc.sql).Error; err != nil {
				t.Fatal(err)
			}
			ids, restricted, err := r.TunnelEntryRestriction(2, 10, false)
			if err != nil || restricted || ids != nil {
				t.Fatalf("legacy grant restricted: ids=%v restricted=%v err=%v", ids, restricted, err)
			}
			if tc.name == "no chain entries" {
				items, err := r.ListUserAccessibleTunnels(2)
				if err != nil || len(items) != 1 {
					t.Fatalf("legacy tunnel hidden: items=%v err=%v", items, err)
				}
			}
		})
	}
	// The JWT role is sufficient, even with no database available for a user lookup.
	r := &Repository{}
	if ids, restricted, err := r.TunnelEntryRestrictionTx(nil, 900, 10, true); err != nil || restricted || ids != nil {
		t.Fatalf("admin JWT restricted: ids=%v restricted=%v err=%v", ids, restricted, err)
	}
	r = entryPermissionFixture(t)
	if ids, restricted, err := r.TunnelEntryRestriction(2, 10, false); err != nil || !restricted || len(ids) != 0 {
		t.Fatalf("empty group union not restricted: ids=%v restricted=%v err=%v", ids, restricted, err)
	}
}

func TestTunnelEntryRestrictionPreservesPersistedLegacyGrants(t *testing.T) {
	r := entryPermissionFixture(t)
	if err := r.db.Transaction(func(tx *gorm.DB) error {
		return r.ReplaceTunnelGroupMembersTx(tx, 20, []int64{10}, 1)
	}); err != nil {
		t.Fatal(err)
	}
	// Older panels could leave a valid user_tunnel and its grant after removing
	// group membership. Entry filtering must not retroactively revoke that access.
	for _, sql := range []string{"DELETE FROM user_group_user WHERE user_id = 2", "DELETE FROM group_permission WHERE user_group_id = 30"} {
		if err := r.db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
		requireEntries(t, r, 2, 11, 12)
		items, err := r.ListUserAccessibleTunnels(2)
		if err != nil || len(items) != 1 {
			t.Fatalf("persisted grant hidden: items=%v err=%v", items, err)
		}
	}
}
