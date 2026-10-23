package repo

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func orphanTestRepo(t *testing.T, dialect string) *Repository {
	t.Helper()
	var r *Repository
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("FLVX_POSTGRES_TEST_DSN")
		if dsn == "" {
			t.Skip("set FLVX_POSTGRES_TEST_DSN to run postgres tests")
		}
		admin, e := sql.Open("pgx", dsn)
		if e != nil {
			t.Fatal(e)
		}
		schema := fmt.Sprintf("orphan_%d", time.Now().UnixNano())
		if _, e = admin.Exec(`CREATE SCHEMA "` + schema + `"`); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		r, err = OpenPostgres(u.String())
	} else {
		r, err = Open(":memory:")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func orphanSeed(t *testing.T, r *Repository, rows ...interface{}) {
	t.Helper()
	for _, row := range rows {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func orphanCount(t *testing.T, r *Repository, table, where string, want int64, args ...interface{}) {
	t.Helper()
	var n int64
	if err := r.db.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s WHERE %s: got %d, want %d", table, where, n, want)
	}
}

func orphanFixture(t *testing.T, r *Repository) {
	orphanSeed(t, r,
		&model.User{ID: 2, User: "orphan-test-user", RoleID: 1},
		&model.Tunnel{ID: 10, Name: "orphan-test-tunnel"},
		&model.Node{ID: 11, Name: "orphan-test-entry"},
		&model.TunnelGroup{ID: 20, Name: "orphan-test-group"},
		&model.TunnelGroupNew{ID: 21, Name: "orphan-test-display-group"},
		&model.TunnelList{ID: 22, Name: "orphan-test-list"},
		&model.UserGroup{ID: 30, Name: "orphan-test-users"},
		&model.UserTunnel{ID: 40, UserID: 2, TunnelID: 10},
		&model.NodeTag{ID: 50, Name: "orphan-test-tag"},
		&model.TunnelGroupTunnel{ID: 100, TunnelGroupID: 20, TunnelID: 10},
		&model.TunnelGroupTunnelEntry{ID: 100, TunnelGroupID: 20, TunnelID: 10, NodeID: 11},
		&model.TunnelGroupTunnelNew{ID: 100, TunnelGroupID: 21, TunnelID: 10},
		&model.TunnelListTunnel{ID: 100, TunnelListID: 22, TunnelID: 10},
		&model.UserGroupUser{ID: 100, UserGroupID: 30, UserID: 2},
		&model.GroupPermission{ID: 100, UserGroupID: 30, TunnelGroupID: 20},
		&model.GroupPermissionGrant{ID: 100, UserGroupID: 30, TunnelGroupID: 20, UserTunnelID: 40, CreatedByGroup: 1},
		&model.TunnelMetric{ID: 100, TunnelID: 10, NodeID: 11, Timestamp: 1},
		&model.NodeMetric{ID: 100, NodeID: 11, Timestamp: 1},
		&model.NodeIdentity{NodeID: 11},
		&model.WGNodeIdentity{ID: 100, NodeID: 11},
		&model.NodeTagNode{ID: 100, NodeID: 11, TagID: 50},
		&model.TunnelQuality{ID: 100, TunnelID: 10},
		&model.NodeTrafficResetLog{ID: 100, NodeID: 11},
		&model.NodeConfigRevision{ID: 100, NodeID: 11},
		&model.NodeDeployLog{ID: 100, NodeID: 11, RevisionID: 100},
	)
}

func TestGroupOrphanCleanup(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			orphanFixture(t, r)
			before := map[string][]map[string]interface{}{}
			for _, rule := range groupOrphanCleanupRules {
				var rows []map[string]interface{}
				if err := r.db.Table(rule.table).Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				before[rule.table] = rows
				// One orphan for every referenced parent, plus a row missing all parents.
				fields := []string{}
				for _, field := range []string{"tunnel_id", "node_id", "tunnel_group_id", "tunnel_list_id", "user_group_id", "user_tunnel_id", "user_id", "tag_id"} {
					if strings.Contains(rule.missing, rule.table+"."+field+")") {
						fields = append(fields, field)
					}
				}
				for i := 0; i <= len(fields); i++ {
					row := map[string]interface{}{}
					for k, v := range rows[0] {
						row[k] = v
					}
					if _, ok := row["id"]; ok {
						row["id"] = 200 + i
					}
					if i == len(fields) {
						for _, f := range fields {
							row[f] = 999 + i
						}
					} else {
						row[fields[i]] = 999 + i
					}
					if err := r.db.Table(rule.table).Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := r.db.Where("name = ?", groupOrphanCleanupKey).Delete(&model.ViteConfig{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := cleanupGroupOrphans(r.db); err != nil {
				t.Fatal(err)
			}
			for _, rule := range groupOrphanCleanupRules {
				var after []map[string]interface{}
				if err := r.db.Table(rule.table).Find(&after).Error; err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before[rule.table], after) {
					t.Fatalf("valid rows changed in %s: %v -> %v", rule.table, before[rule.table], after)
				}
			}
			// Existing references, even with no membership or permission pair, survive.
			if err := r.db.Exec("DELETE FROM group_permission").Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Exec("DELETE FROM tunnel_group_tunnel").Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Where("name = ?", groupOrphanCleanupKey).Delete(&model.ViteConfig{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := cleanupGroupOrphans(r.db); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "group_permission_grant", "id = 100", 1)
			orphanCount(t, r, "tunnel_group_tunnel_entry", "id = 100", 1)
			orphanSeed(t, r, &model.GroupPermissionGrant{UserGroupID: 30, TunnelGroupID: 20, UserTunnelID: 999})
			if err := cleanupGroupOrphans(r.db); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "group_permission_grant", "user_tunnel_id = 999", 1) // marker skips repeated work
			for _, table := range []string{"tunnel_quality", "node_traffic_reset_log", "node_config_revision", "node_deploy_log"} {
				orphanCount(t, r, table, "id = 100", 1)
			}
			orphanCount(t, r, "user_tunnel", "id = 40", 1)
		})
	}
}

func TestGroupOrphanCascades(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, path := range []string{"tunnel", "user", "revoke", "removed-user", "stale-tunnel", "permission-pair", "tunnel-group", "user-group", "node"} {
			t.Run(dialect+"/"+path, func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				orphanFixture(t, r)
				var err error
				switch path {
				case "tunnel":
					err = r.DeleteTunnelCascade(10)
				case "user":
					err = r.DeleteUserCascade(2)
				case "revoke":
					err = r.DeleteUserTunnel(40)
				case "removed-user":
					err = r.db.Transaction(func(tx *gorm.DB) error {
						_, e := r.RevokeGroupGrantsForRemovedUsersTx(tx, 30, []int64{2}, nil)
						return e
					})
				case "stale-tunnel":
					err = r.db.Transaction(func(tx *gorm.DB) error { _, e := r.RevokeStaleTunnelGroupGrantsTx(tx, 20, nil); return e })
				case "permission-pair":
					err = r.db.Transaction(func(tx *gorm.DB) error { _, e := r.RevokeGroupPermissionPairTx(tx, 30, 20); return e })
				case "tunnel-group":
					_, err = r.GroupDeleteCascade("tunnel_group", 20)
				case "user-group":
					_, err = r.GroupDeleteCascade("user_group", 30)
				case "node":
					err = r.DeleteNodeCascade(11)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, rule := range groupOrphanCleanupRules {
					orphanCount(t, r, rule.table, rule.missing, 0)
				}
				if path != "node" {
					orphanCount(t, r, "user_tunnel", "id = 40", 0)
					orphanCount(t, r, "group_permission_grant", "user_tunnel_id = 40", 0)
				} else {
					orphanCount(t, r, "user_tunnel", "id = 40", 1)
				}
				for _, table := range []string{"tunnel_quality", "node_traffic_reset_log", "node_config_revision", "node_deploy_log"} {
					orphanCount(t, r, table, "id = 100", 1)
				}
			})
		}
	}
}

func TestNodeDeleteRefusesReferences(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, row := range []interface{}{
			&model.ChainTunnel{TunnelID: 10, NodeID: 11, ChainType: "1"},
			&model.ChainTunnel{TunnelID: 10, NodeID: 11, ChainType: "2"},
			&model.ChainTunnel{TunnelID: 10, NodeID: 11, ChainType: "3"},
			&model.ForwardPort{ForwardID: 1, NodeID: 11},
			&model.FederationTunnelBinding{TunnelID: 10, NodeID: 11, Status: 1},
			&model.PathSegment{PathID: 1, FromNodeID: 11, ToNodeID: 12},
			&model.PathSegment{PathID: 1, FromNodeID: 12, ToNodeID: 11},
			&model.PeerShare{NodeID: 11}, &model.ServiceMonitor{NodeID: 11},
			&model.PeerShareRuntime{NodeID: 11, Status: 1},
			&model.NodeDeployedInbound{NodeID: 11}, &model.NodeRuntimeResource{NodeID: 11}, &model.NftablesRule{NodeID: 11},
		} {
			t.Run(fmt.Sprintf("%s/%T", dialect, row), func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				orphanFixture(t, r)
				orphanSeed(t, r, row)
				if err := r.DeleteNodeCascade(11); err == nil {
					t.Fatal("referenced node deleted")
				}
				orphanCount(t, r, "node", "id = 11", 1)
				orphanCount(t, r, "tunnel_group_tunnel_entry", "id = 100", 1)
				orphanCount(t, r, "node_identity", "node_id = 11", 1)
			})
		}
	}
}

func TestGroupOrphanCleanupStartupAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	orphanSeed(t, r, &model.GroupPermissionGrant{UserGroupID: 999, TunnelGroupID: 999, UserTunnelID: 999}, &model.TunnelGroupTunnel{TunnelGroupID: 999, TunnelID: 999})
	if err = r.db.Where("name = ?", groupOrphanCleanupKey).Delete(&model.ViteConfig{}).Error; err != nil {
		t.Fatal(err)
	}
	if err = r.db.Exec(`CREATE TRIGGER fail_orphan_delete BEFORE DELETE ON group_permission_grant BEGIN SELECT RAISE(ABORT, 'test rollback'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err = cleanupGroupOrphans(r.db); err == nil {
		t.Fatal("expected cleanup failure")
	}
	orphanCount(t, r, "tunnel_group_tunnel", "tunnel_id = 999", 1)
	orphanCount(t, r, "vite_config", "name = ?", 0, groupOrphanCleanupKey)
	if err = r.db.Exec("DROP TRIGGER fail_orphan_delete").Error; err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	orphanCount(t, r, "group_permission_grant", "user_tunnel_id = 999", 0)
	orphanCount(t, r, "tunnel_group_tunnel", "tunnel_id = 999", 0)
	orphanCount(t, r, "vite_config", "name = ? AND value = 'done'", 1, groupOrphanCleanupKey)
}

func TestGroupOrphanSharedGrantPreserved(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, path := range []string{"removed-user", "stale-tunnel", "permission-pair"} {
			t.Run(dialect+"/"+path, func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				orphanFixture(t, r)
				orphanSeed(t, r, &model.UserGroup{ID: 31, Name: "other-users"}, &model.TunnelGroup{ID: 23, Name: "other-tunnels"},
					&model.GroupPermissionGrant{UserGroupID: 31, TunnelGroupID: 23, UserTunnelID: 40, CreatedByGroup: 0})
				var revoked []RevokedUserTunnelPair
				err := r.db.Transaction(func(tx *gorm.DB) error {
					var err error
					switch path {
					case "removed-user":
						revoked, err = r.RevokeGroupGrantsForRemovedUsersTx(tx, 30, []int64{2}, nil)
					case "stale-tunnel":
						revoked, err = r.RevokeStaleTunnelGroupGrantsTx(tx, 20, nil)
					case "permission-pair":
						revoked, err = r.RevokeGroupPermissionPairTx(tx, 30, 20)
					}
					return err
				})
				if err != nil || len(revoked) != 0 {
					t.Fatalf("shared permission revoked: %v %v", revoked, err)
				}
				orphanCount(t, r, "user_tunnel", "id = 40", 1)
				orphanCount(t, r, "group_permission_grant", "user_tunnel_id = 40 AND user_group_id = 31 AND created_by_group = 1", 1)
			})
		}
	}
}

func TestGroupOrphanCascadeRollback(t *testing.T) {
	for _, path := range []string{"tunnel", "user", "revoke", "node"} {
		t.Run(path, func(t *testing.T) {
			r := orphanTestRepo(t, "sqlite")
			orphanFixture(t, r)
			parent := map[string]string{"tunnel": "tunnel", "user": "user", "revoke": "user_tunnel", "node": "node"}[path]
			if err := r.db.Exec(`CREATE TRIGGER fail_parent_delete BEFORE DELETE ON "` + parent + `" BEGIN SELECT RAISE(ABORT, 'test rollback'); END`).Error; err != nil {
				t.Fatal(err)
			}
			var err error
			switch path {
			case "tunnel":
				err = r.DeleteTunnelCascade(10)
			case "user":
				err = r.DeleteUserCascade(2)
			case "revoke":
				err = r.DeleteUserTunnel(40)
			case "node":
				orphanSeed(t, r, &model.NodeRuntimeResource{NodeID: 11, Status: "released"})
				err = r.DeleteNodeCascade(11)
				orphanCount(t, r, "node_runtime_resource", "node_id = 11 AND status = 'released'", 1)
			}
			if err == nil {
				t.Fatal("expected parent delete failure")
			}
			for _, rule := range groupOrphanCleanupRules {
				orphanCount(t, r, rule.table, "1=1", 1)
			}
		})
	}
}

func TestNodeDeleteRuntimeResources(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, active := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/active=%t", dialect, active), func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				orphanFixture(t, r)
				orphanSeed(t, r,
					&model.Node{ID: 12, Name: "other-node"},
					&model.NodeRuntimeResource{ID: 1, NodeID: 11, Status: "released"},
					&model.NodeRuntimeResource{ID: 2, NodeID: 11, Status: "released"},
					&model.NodeRuntimeResource{ID: 3, NodeID: 12, Status: "released"},
				)
				if active {
					orphanSeed(t, r, &model.NodeRuntimeResource{ID: 4, NodeID: 11, Status: "active"})
				}
				before := map[string][]map[string]interface{}{}
				for _, table := range []string{"node", "node_runtime_resource", "tunnel_group_tunnel_entry", "node_tag_node", "node_identity", "wg_node_identity", "node_metric", "tunnel_metric"} {
					var rows []map[string]interface{}
					if err := r.db.Table(table).Order("1").Find(&rows).Error; err != nil {
						t.Fatal(err)
					}
					before[table] = rows
				}
				err := r.DeleteNodeCascade(11)
				if active {
					if err == nil || !strings.Contains(err.Error(), "节点仍被运行中的资源使用") {
						t.Fatalf("expected referenced node refusal, got %v", err)
					}
					for table, rows := range before {
						var after []map[string]interface{}
						if err := r.db.Table(table).Order("1").Find(&after).Error; err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(rows, after) {
							t.Fatalf("refused deletion changed %s: %v -> %v", table, rows, after)
						}
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					orphanCount(t, r, "node", "id = 11", 0)
					orphanCount(t, r, "node_runtime_resource", "node_id = 11", 0)
				}
				var other model.NodeRuntimeResource
				if err := r.db.First(&other, 3).Error; err != nil {
					t.Fatal(err)
				}
				want := model.NodeRuntimeResource{ID: 3, NodeID: 12, Status: "released"}
				if !reflect.DeepEqual(other, want) {
					t.Fatalf("other node's resource changed: %+v", other)
				}
				orphanCount(t, r, "node", "id = 12", 1)
			})
		}
	}
}
