package repo

import (
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// RuntimeOwnership is a consistent read of ownership, including paused objects
// and inactive federation shares: neither is permission to delete a runtime.
type RuntimeOwnership struct {
	Nodes       []model.Node
	Forwards    []model.Forward
	Ports       []model.ForwardPort
	Tunnels     []model.Tunnel
	Chains      []model.ChainTunnel
	Grants      []model.UserTunnel
	Paths       []model.PathTunnel
	Segments    []model.PathSegment
	SharedNodes map[int64]bool
}

func (r *Repository) ReadRuntimeOwnership() (*RuntimeOwnership, error) {
	s := &RuntimeOwnership{SharedNodes: make(map[int64]bool)}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Select("id", "name", "status", "version", "is_remote", "remote_url", "remote_token").Order("id").Find(&s.Nodes).Error; err != nil {
			return err
		}
		for _, dest := range []interface{}{&s.Forwards, &s.Ports, &s.Tunnels, &s.Chains, &s.Grants, &s.Paths, &s.Segments} {
			if err := tx.Find(dest).Error; err != nil {
				return err
			}
		}
		for _, table := range []string{"peer_share", "peer_share_runtime", "federation_tunnel_binding"} {
			var ids []int64
			if err := tx.Table(table).Distinct("node_id").Pluck("node_id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				s.SharedNodes[id] = true
			}
		}
		return nil
	})
	return s, err
}
