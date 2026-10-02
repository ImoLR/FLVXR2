package repo

import (
	"errors"

	"go-backend/internal/store/model"
)

// ListForwardsForCNCheck returns every forward because inactive rules must also
// be flagged, while only active rules are automatically paused.
func (r *Repository) ListForwardsForCNCheck() ([]model.ForwardRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var forwards []model.Forward
	if err := r.db.Order("id ASC").Find(&forwards).Error; err != nil {
		return nil, err
	}
	rows := make([]model.ForwardRecord, 0, len(forwards))
	for _, f := range forwards {
		rows = append(rows, model.ForwardRecord{
			ID:                  f.ID,
			UserID:              f.UserID,
			UserName:            f.UserName,
			Name:                f.Name,
			TunnelID:            f.TunnelID,
			RemoteAddr:          f.RemoteAddr,
			Strategy:            f.Strategy,
			Status:              f.Status,
			SpeedID:             f.SpeedID,
			MaxConnections:      f.MaxConnections,
			MaxClientIps:        f.MaxClientIps,
			TrafficLimit:        f.TrafficLimit,
			ExpiryTime:          f.ExpiryTime,
			SpeedLimitEnabled:   f.SpeedLimitEnabled,
			SpeedLimit:          f.SpeedLimit,
			UploadSpeed:         f.UploadSpeed,
			DownloadSpeed:       f.DownloadSpeed,
			Mode:                f.Mode,
			InFlow:              f.InFlow,
			OutFlow:             f.OutFlow,
			WGPathID:            f.WGPathID,
			WGRuleType:          f.WGRuleType,
			SourceCIDR:          f.SourceCIDR,
			TargetCIDR:          f.TargetCIDR,
			SNATEnabled:         f.SNATEnabled,
			CNBlocked:           f.CNBlocked,
			CNBlockedReason:     f.CNBlockedReason,
			CNBlockedAutoPaused: f.CNBlockedAutoPaused,
		})
	}
	attachForwardUserLimits(r.db, rows)
	return rows, nil
}

// SetForwardCNBlockState updates the policy flag and the effective status in a
// single database operation. This lets the scanner record a pause even when a
// node is offline.
func (r *Repository) SetForwardCNBlockState(forwardID int64, blocked bool, reason string, autoPaused bool, status int, now int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Forward{}).Where("id = ?", forwardID).Updates(map[string]interface{}{
		"cn_blocked":             blocked,
		"cn_blocked_reason":      reason,
		"cn_blocked_auto_paused": autoPaused,
		"status":                 status,
		"updated_time":           now,
	}).Error
}
