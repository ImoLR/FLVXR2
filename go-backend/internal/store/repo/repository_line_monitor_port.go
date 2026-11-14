package repo

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 255 part 2 「检测端口」: check ports the panel opens on its own nodes
// through their agents. A line_monitor_port row reserves (node, port) in every
// port allocator; it is referenced by the auto-mode lines with that target
// node and port (ref-count). Lines and rows always change in one
// transaction: a row nobody references any more turns `closing` (close
// intent, still reserved) and is deleted only after the agent acknowledged a
// full sync without it.

// LineMonitorMaxPortsPerNode = the agent's own cap (monitorport.MaxPorts).
const LineMonitorMaxPortsPerNode = 8

// lineMonitorPortsOnNodeTx: ports reserved for check ports (open or closing).
func lineMonitorPortsOnNodeTx(tx *gorm.DB, nodeID int64) ([]int, error) {
	var ports []int
	err := tx.Model(&model.LineMonitorPort{}).Where("node_id = ?", nodeID).Pluck("port", &ports).Error
	return ports, err
}

// LineMonitorPortReservedTx reports whether port is a check port of the node.
func (r *Repository) LineMonitorPortReservedTx(tx *gorm.DB, nodeID int64, port int) (bool, error) {
	if tx == nil {
		return false, errors.New("database unavailable")
	}
	var n int64
	err := tx.Model(&model.LineMonitorPort{}).Where("node_id = ? AND port = ?", nodeID, port).Count(&n).Error
	return n > 0, err
}

// LineMonitorPortReserved: same outside a transaction.
func (r *Repository) LineMonitorPortReserved(nodeID int64, port int) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	return r.LineMonitorPortReservedTx(r.db, nodeID, port)
}

// lineMonitorOccupiedPortsTx: every port in use or reserved on the node by
// anything the panel manages — rules, tunnels, landing nodes (incl. guard and
// shared landing ports), chain listeners, deployed inbounds, runtime
// resources, federation bindings, panel shares, nftables rules and check
// ports.
func lineMonitorOccupiedPortsTx(tx *gorm.DB, nodeID int64) (map[int]bool, error) {
	used := map[int]bool{}
	for _, q := range []struct {
		m      interface{}
		column string
		where  string
	}{
		{&model.ForwardPort{}, "port", "node_id = ?"},
		{&model.ChainTunnel{}, "port", "node_id = ? AND port > 0"},
		{&model.ForwardChainNode{}, "port", "node_id = ?"},
		{&model.NodeDeployedInbound{}, "listen_port", "node_id = ?"},
		{&model.NodeRuntimeResource{}, "port", "node_id = ? AND status <> 'released'"},
		{&model.FederationTunnelBinding{}, "allocated_port", "node_id = ? AND status = 1"},
		{&model.PeerShareRuntime{}, "port", "node_id = ? AND status = 1"},
		{&model.NftablesRule{}, "port", "node_id = ?"},
		{&model.LineMonitorPort{}, "port", "node_id = ?"},
	} {
		var ports []int
		if err := tx.Model(q.m).Where(q.where, nodeID).Pluck(q.column, &ports).Error; err != nil {
			return nil, err
		}
		for _, p := range ports {
			used[p] = true
		}
	}
	landing, err := landingPortsOnNode(tx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, p := range landing {
		used[p] = true
	}
	return used, nil
}

// pickLineMonitorPortTx picks a random free port of the node's rule range
// (node.port — what the vendor NAT / security group forwards; landing ranges
// are never used), never 22 or below 1024, not occupied, not in exclude.
func pickLineMonitorPortTx(tx *gorm.DB, nodeID int64, exclude map[int]bool) (int, error) {
	var node model.Node
	if err := tx.Select("id", "name", "port").First(&node, nodeID).Error; err != nil {
		return 0, errors.New("目标节点不存在")
	}
	used, err := lineMonitorOccupiedPortsTx(tx, nodeID)
	if err != nil {
		return 0, err
	}
	var free []int
	for _, p := range parsePortRangeSpec(node.Port) {
		if p < 1024 || p > 65535 || p == 22 || used[p] || exclude[p] {
			continue
		}
		free = append(free, p)
	}
	if len(free) == 0 {
		return 0, fmt.Errorf("节点「%s」的端口段 %s 里没有空闲端口可用作检测端口", node.Name, node.Port)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(free))))
	if err != nil {
		return free[0], nil
	}
	return free[n.Int64()], nil
}

// reconcileLineMonitorPortsTx makes the check-port rows of the nodes match
// their auto lines: wanted ports exist as `open`, the rest turn `closing`.
func reconcileLineMonitorPortsTx(tx *gorm.DB, nodeIDs []int64, now int64) error {
	for _, nodeID := range uniqueIDs(nodeIDs) {
		if nodeID <= 0 {
			continue
		}
		var wanted []int
		if err := tx.Model(&model.LineMonitorLine{}).
			Where("port_mode = ? AND target_node_id = ? AND port > 0", model.LinePortAuto, nodeID).
			Distinct("port").Pluck("port", &wanted).Error; err != nil {
			return err
		}
		want := map[int]bool{}
		for _, p := range wanted {
			want[p] = true
		}
		var rows []model.LineMonitorPort
		if err := tx.Where("node_id = ?", nodeID).Find(&rows).Error; err != nil {
			return err
		}
		have := map[int]bool{}
		for _, row := range rows {
			have[row.Port] = true
			switch {
			case want[row.Port] && row.State != model.LineMonitorPortOpen:
				if err := tx.Model(&model.LineMonitorPort{}).Where("id = ?", row.ID).
					Updates(map[string]interface{}{"state": model.LineMonitorPortOpen, "updated_time": now}).Error; err != nil {
					return err
				}
			case !want[row.Port] && row.State != model.LineMonitorPortClosing:
				if err := tx.Model(&model.LineMonitorPort{}).Where("id = ?", row.ID).
					Updates(map[string]interface{}{"state": model.LineMonitorPortClosing, "updated_time": now}).Error; err != nil {
					return err
				}
			}
		}
		for _, p := range wanted {
			if have[p] {
				continue
			}
			row := model.LineMonitorPort{NodeID: nodeID, Port: p, State: model.LineMonitorPortOpen, CreatedTime: now, UpdatedTime: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// autoPortPicker assigns the check port of auto lines inside one save:
// a line keeps its port while target and mode stay; otherwise it shares the
// port another auto line already uses on that node, else a new one is picked.
type autoPortPicker struct {
	tx       *gorm.DB
	busy     map[int64]map[int]bool // ports agents reported busy (memory)
	assigned map[int64]int          // node → port given to a line in this save
}

func (p *autoPortPicker) pick(nodeID int64) (int, error) {
	if port := p.assigned[nodeID]; port > 0 {
		return port, nil
	}
	var open []model.LineMonitorPort
	if err := p.tx.Where("node_id = ? AND state = ? AND agent_state NOT IN ?", nodeID, model.LineMonitorPortOpen, []string{"busy", "refused"}).
		Order("id ASC").Find(&open).Error; err != nil {
		return 0, err
	}
	for _, row := range open {
		if !p.busy[nodeID][row.Port] {
			p.assigned[nodeID] = row.Port
			return row.Port, nil
		}
	}
	var openCount int64
	if err := p.tx.Model(&model.LineMonitorPort{}).Where("node_id = ? AND state = ?", nodeID, model.LineMonitorPortOpen).Count(&openCount).Error; err != nil {
		return 0, err
	}
	if openCount >= LineMonitorMaxPortsPerNode {
		return 0, fmt.Errorf("目标节点的检测端口已达 %d 个上限", LineMonitorMaxPortsPerNode)
	}
	port, err := pickLineMonitorPortTx(p.tx, nodeID, p.busy[nodeID])
	if err != nil {
		return 0, err
	}
	p.assigned[nodeID] = port
	return port, nil
}

// lineCheckReset: the check fields of a line whose check port changed.
func lineCheckReset(now int64) map[string]interface{} {
	return map[string]interface{}{"check_state": "", "check_msg": "", "check_at": now}
}

// LineMonitorLineRef is one line of a node delete (samples to drop, ports to push).
type LineMonitorLineRef struct {
	ID           int64
	SourceNodeID int64
	TargetNodeID int64
	PortMode     string
}

// LineMonitorLinesOfNode lists the lines that start or end at the node (they
// go with it when the node is deleted).
func (r *Repository) LineMonitorLinesOfNode(nodeID int64) ([]LineMonitorLineRef, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []LineMonitorLineRef
	err := r.db.Model(&model.LineMonitorLine{}).Select("id", "source_node_id", "target_node_id", "port_mode").
		Where("source_node_id = ? OR target_node_id = ?", nodeID, nodeID).Scan(&out).Error
	return out, err
}

// deleteLineMonitorForNodeTx (node delete, same transaction): the node's
// lines (as source or target) and its check-port reservations go; the check
// ports other targets kept for those lines turn closing.
func deleteLineMonitorForNodeTx(tx *gorm.DB, nodeID int64) error {
	var lines []model.LineMonitorLine
	if err := tx.Where("source_node_id = ? OR target_node_id = ?", nodeID, nodeID).Find(&lines).Error; err != nil {
		return err
	}
	var targets []int64
	ids := make([]int64, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ID)
		if l.PortMode == model.LinePortAuto && l.TargetNodeID != nodeID {
			targets = append(targets, l.TargetNodeID)
		}
	}
	if len(ids) > 0 {
		if err := tx.Where("id IN ?", ids).Delete(&model.LineMonitorLine{}).Error; err != nil {
			return err
		}
	}
	// The target node goes away: nothing to close there, the reservation is dropped.
	if err := tx.Where("node_id = ?", nodeID).Delete(&model.LineMonitorPort{}).Error; err != nil {
		return err
	}
	return reconcileLineMonitorPortsTx(tx, targets, time.Now().UnixMilli())
}

// ListLineMonitorPorts returns the check-port rows (nodeID 0 = all).
func (r *Repository) ListLineMonitorPorts(nodeID int64) ([]model.LineMonitorPort, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []model.LineMonitorPort
	q := r.db.Order("node_id ASC, port ASC")
	if nodeID > 0 {
		q = q.Where("node_id = ?", nodeID)
	}
	err := q.Find(&rows).Error
	return rows, err
}

// LineMonitorPortResult is the agent's answer for one port of a sync.
type LineMonitorPortResult struct {
	Port  int
	State string // open | busy | refused | error | closed
	Error string
}

// ApplyLineMonitorPortSync stores the agent's answer to a full sync: the
// closing rows that were closing when the sync was sent are gone on the
// agent (it closes every port the sync did not list) and are deleted; open
// rows get the agent's state.
func (r *Repository) ApplyLineMonitorPortSync(nodeID int64, closingIDs []int64, results []LineMonitorPortResult) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	now := time.Now().UnixMilli()
	return r.db.Transaction(func(tx *gorm.DB) error {
		if len(closingIDs) > 0 {
			if err := tx.Where("node_id = ? AND id IN ? AND state = ?", nodeID, closingIDs, model.LineMonitorPortClosing).
				Delete(&model.LineMonitorPort{}).Error; err != nil {
				return err
			}
		}
		for _, res := range results {
			msg := res.Error
			if len([]rune(msg)) > 200 {
				msg = string([]rune(msg)[:200])
			}
			if err := tx.Model(&model.LineMonitorPort{}).Where("node_id = ? AND port = ? AND state = ?", nodeID, res.Port, model.LineMonitorPortOpen).
				Updates(map[string]interface{}{"agent_state": res.State, "agent_msg": msg, "updated_time": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceLineMonitorPort moves every auto line on (node, port) to a new check
// port (the agent reported the old one busy, or 换一个端口). exclude = ports
// not to pick. The old row turns closing (or is deleted when dropOld: the
// agent never opened it). Returns the moved line IDs and the new port.
func (r *Repository) ReplaceLineMonitorPort(nodeID int64, port int, lineIDs []int64, exclude map[int]bool, dropOld bool) ([]int64, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("repository not initialized")
	}
	var moved []int64
	newPort := 0
	now := time.Now().UnixMilli()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&model.LineMonitorLine{}).Where("port_mode = ? AND target_node_id = ? AND port = ?", model.LinePortAuto, nodeID, port)
		if len(lineIDs) > 0 {
			q = q.Where("id IN ?", lineIDs)
		}
		if err := q.Pluck("id", &moved).Error; err != nil {
			return err
		}
		if len(moved) == 0 {
			return errors.New("该检测端口已没有线路使用")
		}
		if dropOld {
			if err := tx.Where("node_id = ? AND port = ?", nodeID, port).Delete(&model.LineMonitorPort{}).Error; err != nil {
				return err
			}
		}
		ex := map[int]bool{port: true}
		for p := range exclude {
			ex[p] = true
		}
		p, err := pickLineMonitorPortTx(tx, nodeID, ex)
		if err != nil {
			return err
		}
		newPort = p
		upd := lineCheckReset(now)
		upd["port"], upd["updated_time"] = newPort, now
		if err := tx.Model(&model.LineMonitorLine{}).Where("id IN ?", moved).Updates(upd).Error; err != nil {
			return err
		}
		return reconcileLineMonitorPortsTx(tx, []int64{nodeID}, now)
	})
	if err != nil {
		return nil, 0, err
	}
	return moved, newPort, nil
}

// SetLineMonitorLineCheck stores a line's last check-port verify.
func (r *Repository) SetLineMonitorLineCheck(lineID int64, state, msg string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if len([]rune(msg)) > 200 {
		msg = string([]rune(msg)[:200])
	}
	return r.db.Model(&model.LineMonitorLine{}).Where("id = ?", lineID).
		Updates(map[string]interface{}{"check_state": state, "check_msg": msg, "check_at": time.Now().UnixMilli()}).Error
}

// GetLineMonitorLine returns one line (nil when missing).
func (r *Repository) GetLineMonitorLine(id int64) (*model.LineMonitorLine, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var l model.LineMonitorLine
	err := r.db.First(&l, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// LineMonitorCustomPorts lists the ports other custom lines use on host (the
// suggestion must not collide).
func (r *Repository) LineMonitorCustomPorts(host string) (map[int]bool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var ports []int
	if err := r.db.Model(&model.LineMonitorLine{}).Where("target_node_id = 0 AND LOWER(host) = LOWER(?)", host).Pluck("port", &ports).Error; err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, p := range ports {
		out[p] = true
	}
	return out, nil
}

// LineMonitorNodeHasDDNS reports nodes with a DDNS record (dynamic address).
func (r *Repository) LineMonitorNodeHasDDNS(nodeIDs []int64) (map[int64]bool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	out := map[int64]bool{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	var ids []int64
	if err := r.db.Model(&model.NodeDDNSRecord{}).Where("node_id IN ?", nodeIDs).Distinct("node_id").Pluck("node_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
