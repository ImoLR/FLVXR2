package repo

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

const groupOrphanCleanupKey = "tunnel_group_orphan_cleanup_v1"

// Only missing parent records count as orphans. In particular, valid grant
// provenance is retained even when group membership or permissions have changed.
// Frozen tunnel_quality and traffic/reset/configuration history are excluded.
var groupOrphanCleanupRules = []struct {
	table   string
	missing string
}{
	{"tunnel_group_tunnel", `NOT EXISTS (SELECT 1 FROM tunnel WHERE tunnel.id = tunnel_group_tunnel.tunnel_id) OR NOT EXISTS (SELECT 1 FROM tunnel_group WHERE tunnel_group.id = tunnel_group_tunnel.tunnel_group_id)`},
	{"tunnel_group_tunnel_entry", `NOT EXISTS (SELECT 1 FROM tunnel WHERE tunnel.id = tunnel_group_tunnel_entry.tunnel_id) OR NOT EXISTS (SELECT 1 FROM node WHERE node.id = tunnel_group_tunnel_entry.node_id) OR NOT EXISTS (SELECT 1 FROM tunnel_group WHERE tunnel_group.id = tunnel_group_tunnel_entry.tunnel_group_id)`},
	{"tunnel_group_tunnel_new", `NOT EXISTS (SELECT 1 FROM tunnel WHERE tunnel.id = tunnel_group_tunnel_new.tunnel_id) OR NOT EXISTS (SELECT 1 FROM tunnel_group_new WHERE tunnel_group_new.id = tunnel_group_tunnel_new.tunnel_group_id)`},
	{"tunnel_list_tunnel", `NOT EXISTS (SELECT 1 FROM tunnel WHERE tunnel.id = tunnel_list_tunnel.tunnel_id) OR NOT EXISTS (SELECT 1 FROM tunnel_list WHERE tunnel_list.id = tunnel_list_tunnel.tunnel_list_id)`},
	{"group_permission_grant", `NOT EXISTS (SELECT 1 FROM user_tunnel WHERE user_tunnel.id = group_permission_grant.user_tunnel_id) OR NOT EXISTS (SELECT 1 FROM user_group WHERE user_group.id = group_permission_grant.user_group_id) OR NOT EXISTS (SELECT 1 FROM tunnel_group WHERE tunnel_group.id = group_permission_grant.tunnel_group_id)`},
	{"group_permission", `NOT EXISTS (SELECT 1 FROM user_group WHERE user_group.id = group_permission.user_group_id) OR NOT EXISTS (SELECT 1 FROM tunnel_group WHERE tunnel_group.id = group_permission.tunnel_group_id)`},
	{"user_group_user", `NOT EXISTS (SELECT 1 FROM "user" WHERE "user".id = user_group_user.user_id) OR NOT EXISTS (SELECT 1 FROM user_group WHERE user_group.id = user_group_user.user_group_id)`},
	{"tunnel_metric", `NOT EXISTS (SELECT 1 FROM tunnel WHERE tunnel.id = tunnel_metric.tunnel_id) OR NOT EXISTS (SELECT 1 FROM node WHERE node.id = tunnel_metric.node_id)`},
	{"node_metric", `NOT EXISTS (SELECT 1 FROM node WHERE node.id = node_metric.node_id)`},
	{"node_identity", `NOT EXISTS (SELECT 1 FROM node WHERE node.id = node_identity.node_id)`},
	{"wg_node_identity", `NOT EXISTS (SELECT 1 FROM node WHERE node.id = wg_node_identity.node_id)`},
	{"node_tag_node", `NOT EXISTS (SELECT 1 FROM node WHERE node.id = node_tag_node.node_id) OR NOT EXISTS (SELECT 1 FROM node_tag WHERE node_tag.id = node_tag_node.tag_id)`},
}

// Run before serving requests, after entry backfill. All deletions and the
// marker commit together; a failed migration remains retryable on next startup.
func cleanupGroupOrphans(db *gorm.DB) error {
	var counts []string
	err := db.Transaction(func(tx *gorm.DB) error {
		var cfg model.ViteConfig
		err := tx.Where("name = ?", groupOrphanCleanupKey).First(&cfg).Error
		if err == nil && cfg.Value == "done" {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		for _, rule := range groupOrphanCleanupRules {
			result := tx.Exec("DELETE FROM " + rule.table + " WHERE " + rule.missing)
			if result.Error != nil {
				return result.Error
			}
			counts = append(counts, fmt.Sprintf("%s=%d", rule.table, result.RowsAffected))
		}
		cfg.Name, cfg.Value, cfg.Time = groupOrphanCleanupKey, "done", time.Now().UnixMilli()
		return tx.Save(&cfg).Error
	})
	if err == nil && len(counts) > 0 {
		log.Printf("[group-orphan-cleanup] %s", strings.Join(counts, " "))
	}
	return err
}
