package repo

import (
	"errors"
	"sort"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 304: before the picker hands out a native landing port, the panel
// checks the next candidate(s) on the machine (handler probe, one loopback
// TCP connect each through the agent's TcpPing). Ports found held by another
// program go into landing_server.native_busy_ports — the plan-147 list the
// picker already skips — so the pick in the create / move transaction lands
// on the first port the machine really has free. The list keeps its 24 h
// lifetime: a port that comes up as a candidate again is simply probed again.

// NativeLandingPortProbe is one server's next candidate ports, in the order
// landingFreePort hands them out, and the node whose agent can check them.
type NativeLandingPortProbe struct {
	NodeID int64
	Ports  []int
}

// NativeLandingPortCandidates returns the first n ports the native picker
// would hand out next (panel DB view, read-only). nil when there is nothing
// to check: shared / chain-only / paused / unknown server, or a node that is
// offline, remote or not landing-capable (creation then fails or picks as
// before).
func (r *Repository) NativeLandingPortCandidates(serverID int64, n int) (*NativeLandingPortProbe, error) {
	if r == nil || r.db == nil || serverID <= 0 || n <= 0 {
		return nil, nil
	}
	var s model.LandingServer
	if err := r.db.First(&s, serverID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if s.Source != "native" || s.ChainOnly || s.Status != "active" || s.NodeID <= 0 || s.NodeID == 36 {
		return nil, nil
	}
	used, node, _, err := landingUsedPorts(r.db, s)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if node.Status != 1 || node.IsRemote == 1 || !node.LandingCapable {
		return nil, nil
	}
	out := &NativeLandingPortProbe{NodeID: s.NodeID}
	for p := s.PortMin; p <= s.PortMax && len(out.Ports) < n; p++ {
		if !used[p] {
			out.Ports = append(out.Ports, p)
		}
	}
	if len(out.Ports) == 0 {
		return nil, nil
	}
	return out, nil
}

// NoteNativeBusyPorts remembers ports the probe found held by another
// program on the machine: same list, lifetime and cap as the agent's
// port_in_use reports. Writes only on change.
func (r *Repository) NoteNativeBusyPorts(serverID int64, ports []int, now time.Time) error {
	if r == nil || r.db == nil || len(ports) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return err
		}
		if s.Source != "native" {
			return nil
		}
		busy := nativeBusyPorts(&s)
		if !mergeNativeBusyPorts(busy, ports, now) {
			return nil
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("native_busy_ports", formatNativeBusyPorts(busy, now)).Error
	})
}

// NativeLandingMovableCount: how many nodes ApplyNativeLandingNodeErrors
// would move off a busy port for these reports right now (read-only), so
// the handler probes the move targets first. 0 (no query) without a
// port_in_use report.
func (r *Repository) NativeLandingMovableCount(serverID int64, reports []LandingNodeReport, now time.Time) (int, error) {
	byID := map[int64]LandingNodeReport{}
	for _, rep := range reports {
		if rep.Code == LandingNodeErrPortInUse && rep.Port > 0 && rep.ID > 0 {
			byID[rep.ID] = rep
		}
	}
	if len(byID) == 0 || r == nil || r.db == nil {
		return 0, nil
	}
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var nodes []model.LandingNode
	if err := r.db.Where("landing_server_id = ? AND id IN ?", serverID, ids).Find(&nodes).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range nodes {
		n := &nodes[i]
		if byID[n.ID].Port != n.Port {
			continue // stale report: the node already moved
		}
		movable, err := landingNodeMovable(r.db, n, now)
		if err != nil {
			return 0, err
		}
		if movable {
			count++
		}
	}
	return count, nil
}
