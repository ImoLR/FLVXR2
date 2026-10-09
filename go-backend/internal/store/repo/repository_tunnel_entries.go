package repo

import (
	"errors"
	"fmt"
	"log"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tunnelEntryBackfillKey = "tunnel_group_entry_backfill_v1"

// Run before serving requests. The rows and marker commit together, so a
// restart can never refill entries deliberately unticked by an administrator.
func backfillTunnelGroupEntries(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var cfg model.ViteConfig
		err := tx.Where("name = ?", tunnelEntryBackfillKey).First(&cfg).Error
		if err == nil && cfg.Value == "done" {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var rows []model.TunnelGroupTunnelEntry
		if err := tx.Table("tunnel_group_tunnel AS g").
			Select("DISTINCT g.tunnel_group_id, g.tunnel_id, c.node_id, g.created_time").
			Joins("JOIN chain_tunnel AS c ON c.tunnel_id = g.tunnel_id AND c.chain_type = '1'").
			Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		cfg.Name, cfg.Value = tunnelEntryBackfillKey, "done"
		cfg.Time = time.Now().UnixMilli()
		if err := tx.Save(&cfg).Error; err != nil {
			return err
		}
		log.Printf("[tunnel-entries] backfilled %d group entry rows", len(rows))
		return nil
	})
}

// EffectiveTunnelEntryNodeIDs keeps chain order. Grant provenance belongs to
// user_tunnel: legacy additional group grants may carry a zero marker even
// though the original grant was group-created.
func (r *Repository) EffectiveTunnelEntryNodeIDs(userID, tunnelID int64) ([]int64, error) {
	return r.EffectiveTunnelEntryNodeIDsTx(r.db, userID, tunnelID)
}

func (r *Repository) EffectiveTunnelEntryNodeIDsTx(tx *gorm.DB, userID, tunnelID int64) ([]int64, error) {
	ids, restricted, err := r.TunnelEntryRestrictionTx(tx, userID, tunnelID, false)
	if err != nil || restricted {
		return ids, err
	}
	ids = make([]int64, 0)
	err = tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = '1'", tunnelID).Order("inx ASC, id ASC").Pluck("node_id", &ids).Error
	return ids, err
}

// Entry permissions only narrow group-derived grants. Missing legacy rows and
// tunnels without chain entries must retain their original behaviour. Callers
// with an admin JWT bypass the lookup; background operations use the owner's role.
func (r *Repository) TunnelEntryRestriction(userID, tunnelID int64, admin bool) ([]int64, bool, error) {
	return r.TunnelEntryRestrictionTx(r.db, userID, tunnelID, admin)
}

func (r *Repository) TunnelEntryRestrictionTx(tx *gorm.DB, userID, tunnelID int64, admin bool) ([]int64, bool, error) {
	if admin {
		return nil, false, nil
	}
	var ut model.UserTunnel
	err := tx.Where("user_id = ? AND tunnel_id = ?", userID, tunnelID).First(&ut).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var count int64
	if err := tx.Model(&model.GroupPermissionGrant{}).Where("user_tunnel_id = ? AND created_by_group = 1", ut.ID).Count(&count).Error; err != nil {
		return nil, false, err
	}
	if count == 0 {
		return nil, false, nil
	}
	var user model.User
	err = tx.Select("id, role_id").Where("id = ?", userID).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && user.RoleID == 0 {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	q := tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = '1'", tunnelID).Order("inx ASC, id ASC")
	if err := q.Count(&count).Error; err != nil {
		return nil, false, err
	}
	if count == 0 {
		return nil, false, nil
	}
	// Persisted grants are authoritative, including legacy grants whose group
	// membership has since changed. Explicit revocation removes those grants.
	entries := tx.Table("tunnel_group_tunnel_entry AS e").Select("e.node_id").
		Joins("JOIN group_permission_grant AS g ON g.tunnel_group_id = e.tunnel_group_id").
		Where("g.user_tunnel_id = ? AND e.tunnel_id = ?", ut.ID, tunnelID)
	ids := make([]int64, 0)
	if err := q.Where("node_id IN (?)", entries).Pluck("node_id", &ids).Error; err != nil {
		return nil, true, err
	}
	if len(ids) == 0 {
		log.Printf("[tunnel-entries] user=%d tunnel=%d group grant has no allowed entries", userID, tunnelID)
	}
	return ids, true, nil
}

func (r *Repository) replaceTunnelGroupEntriesTx(tx *gorm.DB, groupID int64, tunnelIDs []int64, selection map[int64][]int64, now int64) error {
	if err := tx.Where("tunnel_group_id = ?", groupID).Delete(&model.TunnelGroupTunnelEntry{}).Error; err != nil {
		return err
	}
	var rows []model.TunnelGroupTunnelEntry
	for _, tunnelID := range tunnelIDs {
		var current []int64
		if err := tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = '1'", tunnelID).Pluck("node_id", &current).Error; err != nil {
			return err
		}
		selected, explicit := selection[tunnelID]
		if !explicit {
			selected = current // Older assign payloads retain their all-entry meaning.
		}
		for _, nodeID := range selected {
			valid := false
			for _, id := range current {
				valid = valid || id == nodeID
			}
			if !valid {
				return fmt.Errorf("隧道 %d 不包含入口 %d", tunnelID, nodeID)
			}
			rows = append(rows, model.TunnelGroupTunnelEntry{TunnelGroupID: groupID, TunnelID: tunnelID, NodeID: nodeID, CreatedTime: now})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func (r *Repository) TunnelGroupEntries(groupID int64) (map[int64][]int64, error) {
	ids, err := r.ListTunnelIDsByTunnelGroup(groupID)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		result[id] = []int64{}
	}
	var rows []model.TunnelGroupTunnelEntry
	if err := r.db.Where("tunnel_group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TunnelID] = append(result[row.TunnelID], row.NodeID)
	}
	return result, nil
}

func (r *Repository) DeleteRemovedTunnelEntriesTx(tx *gorm.DB, tunnelID int64) error {
	current := tx.Model(&model.ChainTunnel{}).Select("node_id").Where("tunnel_id = ? AND chain_type = '1'", tunnelID)
	return tx.Where("tunnel_id = ? AND node_id NOT IN (?)", tunnelID, current).Delete(&model.TunnelGroupTunnelEntry{}).Error
}

func (r *Repository) ListForwardUserTunnelPairs() ([]RevokedUserTunnelPair, error) {
	var pairs []RevokedUserTunnelPair
	err := r.db.Model(&model.Forward{}).Distinct("user_id", "tunnel_id").Where("tunnel_id > 0").Find(&pairs).Error
	return pairs, err
}

// ReconcileForwardEntryPorts leaves retained rows (including their IDs and
// custom bind addresses) untouched.
func (r *Repository) ReconcileForwardEntryPorts(forwardID int64, entries []model.ForwardPortRecord) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		ids := make([]int64, 0, len(entries))
		for _, entry := range entries {
			ids = append(ids, entry.NodeID)
		}
		q := tx.Where("forward_id = ?", forwardID)
		if len(ids) > 0 {
			q = q.Where("node_id NOT IN ?", ids)
		}
		if err := q.Delete(&model.ForwardPort{}).Error; err != nil {
			return err
		}
		for _, entry := range entries {
			var count int64
			if err := tx.Model(&model.ForwardPort{}).Where("forward_id = ? AND node_id = ?", forwardID, entry.NodeID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&model.ForwardPort{ForwardID: forwardID, NodeID: entry.NodeID, Port: entry.Port}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
