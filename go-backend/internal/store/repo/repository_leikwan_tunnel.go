package repo

import (
	"errors"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// SaveLeiKwanTunnelPipeTx commits the platform target and the ordinary tunnel
// linkage together. The conditional update protects concurrent attachment.
func (r *Repository) SaveLeiKwanTunnelPipeTx(tx *gorm.DB, pipe *model.LeiKwanPipe, tunnelID int64) error {
	pipe.UpdatedTime = time.Now().UnixMilli()
	result := tx.Model(&model.LeiKwanPipe{}).
		Where("id = ? AND (tunnel_id IS NULL OR tunnel_id = ?)", pipe.ID, tunnelID).
		Updates(map[string]interface{}{
			"tunnel_id": tunnelID, "landing_node_id": pipe.LandingNodeID,
			"landing_port": pipe.LandingPort, "listen_port": pipe.ListenPort,
			"target_host": pipe.TargetHost, "e_tag": pipe.ETag, "updated_time": pipe.UpdatedTime,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("管道已被其他隧道使用")
	}
	pipe.TunnelID = &tunnelID
	return nil
}
