package repo

import "go-backend/internal/store/model"

func (r *Repository) ListActiveDialTargetTunnelIDsByNode(nodeID int64) ([]int64, error) {
	var ids []int64
	err := r.db.Model(&model.ChainTunnel{}).
		Joins("JOIN tunnel ON tunnel.id = chain_tunnel.tunnel_id").
		Where("chain_tunnel.node_id = ? AND chain_tunnel.chain_type IN ? AND tunnel.status = 1", nodeID, []string{"2", "3"}).
		Distinct().Order("chain_tunnel.tunnel_id ASC").Pluck("chain_tunnel.tunnel_id", &ids).Error
	return ids, err
}

func (r *Repository) ListActiveWGPathsByNode(nodeID int64) ([]model.PathTunnel, error) {
	var paths []model.PathTunnel
	err := r.db.Model(&model.PathTunnel{}).
		Joins("JOIN path_segment ON path_segment.path_id = path_tunnel.id").
		Where("(path_segment.from_node_id = ? OR path_segment.to_node_id = ?) AND path_tunnel.status = ?", nodeID, nodeID, "active").
		Distinct("path_tunnel.*").Order("path_tunnel.id ASC").Find(&paths).Error
	return paths, err
}
