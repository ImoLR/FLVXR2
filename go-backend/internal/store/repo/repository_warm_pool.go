package repo

import (
	"errors"
	"sort"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// WarmChainOwner (plan 244 预热池): a node running chains_<TunnelID> that
// should keep warm conns to its next hop.
type WarmChainOwner struct {
	NodeID   int64 `gorm:"column:node_id"`
	TunnelID int64 `gorm:"column:tunnel_id"`
}

// ListWarmChainOwners returns, for every enabled 隧道转发 tunnel with the
// 预热池 switch on, the entry nodes that serve ≥ 1 active rule of it (status 1,
// gost mode — nftables / WG rules do not dial through the chain) and, when
// any such entry exists, all of the tunnel's middle nodes. Paused, expired
// and deleted rules do not count; LeiKwan pipe tunnels never warm.
func (r *Repository) ListWarmChainOwners() ([]WarmChainOwner, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	entries := make([]WarmChainOwner, 0)
	err := r.db.Table("forward_port AS fp").
		Select("DISTINCT fp.node_id AS node_id, f.tunnel_id AS tunnel_id").
		Joins("JOIN forward f ON f.id = fp.forward_id").
		Joins("JOIN tunnel t ON t.id = f.tunnel_id").
		Where("f.status = 1 AND (f.mode = '' OR f.mode = 'gost') AND t.type = 2 AND t.status = 1 AND t.warm_pool = 1").
		// A LeiKwan pipe tunnel's entry dials the local LeiKwan client, which
		// has its own pool and LeiKwan's connection limit: never warm it.
		Where("t.id NOT IN (SELECT tunnel_id FROM leikwan_pipe WHERE tunnel_id IS NOT NULL)").
		Scan(&entries).Error
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return entries, nil
	}
	seen := map[int64]struct{}{}
	tunnelIDs := make([]int64, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.TunnelID]; !ok {
			seen[e.TunnelID] = struct{}{}
			tunnelIDs = append(tunnelIDs, e.TunnelID)
		}
	}
	mids := make([]WarmChainOwner, 0)
	if err := r.db.Model(&model.ChainTunnel{}).
		Select("DISTINCT node_id, tunnel_id").
		Where("chain_type = ? AND tunnel_id IN ?", "2", tunnelIDs).
		Scan(&mids).Error; err != nil {
		return nil, err
	}
	out := append(entries, mids...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].TunnelID < out[j].TunnelID
	})
	return out, nil
}

// WarmForwardOwner (plan 244): a node whose rule service (端口转发) should
// keep warm conns to the rule's target.
type WarmForwardOwner struct {
	NodeID    int64 `gorm:"column:node_id"`
	ForwardID int64 `gorm:"column:forward_id"`
	TunnelID  int64 `gorm:"column:tunnel_id"`
}

// ListWarmForwardOwners: active gost rules (status 1) of enabled 端口转发
// tunnels with the 预热池 switch on, per entry node.
func (r *Repository) ListWarmForwardOwners() ([]WarmForwardOwner, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	out := make([]WarmForwardOwner, 0)
	err := r.db.Table("forward_port AS fp").
		Select("DISTINCT fp.node_id AS node_id, f.id AS forward_id, f.tunnel_id AS tunnel_id").
		Joins("JOIN forward f ON f.id = fp.forward_id").
		Joins("JOIN tunnel t ON t.id = f.tunnel_id").
		Where("f.status = 1 AND (f.mode = '' OR f.mode = 'gost') AND t.type = 1 AND t.status = 1 AND t.warm_pool = 1").
		Order("fp.node_id ASC, f.id ASC").
		Scan(&out).Error
	return out, err
}

// SetTunnelWarmPoolTx sets the 预热池 switch (plan 244); GORM skips a 0 on
// create because the column has a default, so 0 is always written here.
func (r *Repository) SetTunnelWarmPoolTx(tx *gorm.DB, tunnelID int64, value int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if tx == nil {
		tx = r.db
	}
	if value != 0 {
		value = 1
	}
	return tx.Model(&model.Tunnel{}).Where("id = ?", tunnelID).Update("warm_pool", value).Error
}

// WarmPoolTunnel is what the 预热池 tag needs of a tunnel.
type WarmPoolTunnel struct {
	ID       int64 `gorm:"column:id"`
	Type     int   `gorm:"column:type"`
	Status   int   `gorm:"column:status"`
	WarmPool int   `gorm:"column:warm_pool"`
}

// ListWarmPoolTunnels: every tunnel's switch and type.
func (r *Repository) ListWarmPoolTunnels() ([]WarmPoolTunnel, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	out := make([]WarmPoolTunnel, 0)
	err := r.db.Model(&model.Tunnel{}).Select("id, type, status, warm_pool").Scan(&out).Error
	return out, err
}

// CountActiveGostForwardsByTunnel: active gost rules per tunnel (the pool
// exists only while a tunnel has ≥ 1).
func (r *Repository) CountActiveGostForwardsByTunnel() (map[int64]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []struct {
		TunnelID int64 `gorm:"column:tunnel_id"`
		N        int64 `gorm:"column:n"`
	}
	err := r.db.Model(&model.Forward{}).
		Select("tunnel_id, COUNT(*) AS n").
		Where("status = 1 AND (mode = '' OR mode = 'gost')").
		Group("tunnel_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.TunnelID] = row.N
	}
	return out, nil
}
