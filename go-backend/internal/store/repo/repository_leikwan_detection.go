package repo

import (
	"encoding/json"
	"errors"
	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Repeated reports and reconnects preserve the last changed observation, as RA does.
func (r *Repository) UpdateNodeLeiKwan(nodeID int64, status, forwards string, now int64) error {
	if status != "absent" && status != "present" && status != "running" {
		return nil
	}
	return r.db.Model(&model.Node{}).
		Where("id = ? AND (leikwan_status <> ? OR leikwan_forwards <> ?)", nodeID, status, forwards).
		Updates(map[string]interface{}{"leikwan_status": status, "leikwan_forwards": forwards, "leikwan_checked_at": now}).Error
}
func leiKwanForwards(raw string) []model.LeiKwanDetectedForward {
	forwards := []model.LeiKwanDetectedForward{}
	_ = json.Unmarshal([]byte(raw), &forwards)
	return forwards
}

// Plan 265: the panel never hands out a port the node's LeiKwan client
// listens on.

// LeiKwanReservedFromRaw: the listen ports of a node's LeiKwan client forwards.
// Every entry counts, also running=false: a stopped LeiKwan forward comes
// back on the same port. nil when there are none.
func LeiKwanReservedFromRaw(raw string) map[int]bool {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out map[int]bool
	for _, f := range leiKwanForwards(raw) {
		if f.ListenPort < 1 || f.ListenPort > 65535 {
			continue
		}
		if out == nil {
			out = map[int]bool{}
		}
		out[f.ListenPort] = true
	}
	return out
}

// leiKwanReservedPortsTx loads the node's reserved LeiKwan ports with one
// query. The list is only rewritten by agent reports (built from the client's
// config files) and never cleared on disconnect, so an offline node keeps it.
func leiKwanReservedPortsTx(tx *gorm.DB, nodeID int64) (map[int]bool, error) {
	if tx == nil {
		return nil, errors.New("database unavailable")
	}
	if nodeID <= 0 {
		return nil, nil
	}
	var raw []string
	if err := tx.Model(&model.Node{}).Where("id = ?", nodeID).Pluck("leikwan_forwards", &raw).Error; err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return LeiKwanReservedFromRaw(raw[0]), nil
}

// LeiKwanReservedPortsTx: ports the node's LeiKwan client listens on.
func (r *Repository) LeiKwanReservedPortsTx(tx *gorm.DB, nodeID int64) (map[int]bool, error) {
	return leiKwanReservedPortsTx(tx, nodeID)
}

// LeiKwanReservedPorts: same outside a transaction.
func (r *Repository) LeiKwanReservedPorts(nodeID int64) (map[int]bool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	return leiKwanReservedPortsTx(r.db, nodeID)
}

// leiKwanPortBlockedTx: a new or changed use of (node, port) is refused
// because the node's LeiKwan client listens there. holds reports whether the
// caller already has this exact (node, port) — an unchanged existing row
// keeps saving, nothing is moved.
func leiKwanPortBlockedTx(tx *gorm.DB, nodeID int64, port int, holds func() (bool, error)) (bool, error) {
	if nodeID <= 0 || port <= 0 {
		return false, nil
	}
	reserved, err := leiKwanReservedPortsTx(tx, nodeID)
	if err != nil || !reserved[port] {
		return false, err
	}
	if holds == nil {
		return true, nil
	}
	own, err := holds()
	if err != nil {
		return false, err
	}
	return !own, nil
}

// LeiKwanPortBlocksForwardTx reports whether a forward may not use (node,
// port) because of the node's LeiKwan client. A forward (currentForwardID >
// 0) that already holds this port on the node is grandfathered.
func (r *Repository) LeiKwanPortBlocksForwardTx(tx *gorm.DB, nodeID int64, port int, currentForwardID int64) (bool, error) {
	var holds func() (bool, error)
	if currentForwardID > 0 {
		holds = func() (bool, error) {
			var n int64
			err := tx.Model(&model.ForwardPort{}).Where("forward_id = ? AND node_id = ? AND port = ?", currentForwardID, nodeID, port).Count(&n).Error
			return n > 0, err
		}
	}
	return leiKwanPortBlockedTx(tx, nodeID, port, holds)
}

// LeiKwanPortBlocksForward: same outside a transaction.
func (r *Repository) LeiKwanPortBlocksForward(nodeID int64, port int, currentForwardID int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	return r.LeiKwanPortBlocksForwardTx(r.db, nodeID, port, currentForwardID)
}

// LeiKwanPortBlocksTunnelTx: same for a tunnel's out / chain-hop port. A
// tunnel being edited (tunnelID > 0) that already listens there keeps it.
func (r *Repository) LeiKwanPortBlocksTunnelTx(tx *gorm.DB, nodeID int64, port int, tunnelID int64) (bool, error) {
	var holds func() (bool, error)
	if tunnelID > 0 {
		holds = func() (bool, error) {
			var n int64
			err := tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND node_id = ? AND port = ?", tunnelID, nodeID, port).Count(&n).Error
			return n > 0, err
		}
	}
	return leiKwanPortBlockedTx(tx, nodeID, port, holds)
}

// LeiKwanReservedPortsByHost: the reserved LeiKwan ports of every local node
// that has address host (a 线路监测 custom target may be one of our machines).
func (r *Repository) LeiKwanReservedPortsByHost(host string) (map[int]bool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	out := map[int]bool{}
	if host == "" {
		return out, nil
	}
	var nodes []model.Node
	if err := r.db.Select("id", "server_ip", "server_ip_v4", "server_ip_v6", "intranet_ip", "extra_ips", "leikwan_forwards").
		Where("is_remote = 0 AND leikwan_forwards <> '' AND leikwan_forwards <> '[]'").Find(&nodes).Error; err != nil {
		return nil, err
	}
	want := normalizeLandingHost(host)
	for _, n := range nodes {
		if !landingNodeHosts(model.LandingServer{}, n)[want] {
			continue
		}
		for p := range LeiKwanReservedFromRaw(n.LeiKwanForwards) {
			out[p] = true
		}
	}
	return out, nil
}

// LeiKwanPortHolders: of the given LeiKwan ports, the ones the panel already
// uses on the node (rules, tunnels, landing, chains, check ports, …) — the
// admin is told in the log, nothing is moved.
func (r *Repository) LeiKwanPortHolders(nodeID int64, ports []int) ([]int, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	if nodeID <= 0 || len(ports) == 0 {
		return nil, nil
	}
	used, err := lineMonitorOccupiedPortsTx(r.db, nodeID)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, p := range ports {
		if used[p] {
			out = append(out, p)
		}
	}
	return out, nil
}
