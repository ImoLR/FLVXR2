package repo

import (
	"go-backend/internal/store/model"
)

// Tunnel relay auth (plan 225).

// TunnelRelayAuthState is one tunnel's relay auth flag.
type TunnelRelayAuthState struct {
	ID        int64  `gorm:"column:id"`
	Name      string `gorm:"column:name"`
	Type      int    `gorm:"column:type"`
	Status    int    `gorm:"column:status"`
	RelayAuth int    `gorm:"column:relay_auth"`
}

// ListTunnelRelayAuth lists the relay auth flag of every tunnel (ids empty) or
// of the given tunnels, ordered by id.
func (r *Repository) ListTunnelRelayAuth(ids []int64) ([]TunnelRelayAuthState, error) {
	var rows []TunnelRelayAuthState
	q := r.db.Model(&model.Tunnel{}).Select("id, name, type, status, relay_auth").Order("id ASC")
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	err := q.Scan(&rows).Error
	return rows, err
}

// SetTunnelRelayAuth stores the relay auth flag (0/1) of one tunnel.
func (r *Repository) SetTunnelRelayAuth(tunnelID int64, value int) error {
	return r.db.Model(&model.Tunnel{}).Where("id = ?", tunnelID).Update("relay_auth", value).Error
}

// NodeVersions returns node.version for the given node ids.
func (r *Repository) NodeVersions(ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID      int64  `gorm:"column:id"`
		Version string `gorm:"column:version"`
	}
	if err := r.db.Model(&model.Node{}).Select("id, version").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Version
	}
	return out, nil
}
