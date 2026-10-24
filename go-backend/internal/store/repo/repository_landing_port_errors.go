package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 147: a native landing node whose port is taken by another program on
// the machine. Agents from plan 147 on keep adding the server's other inbounds
// and report each failed one ({id, port, code, reason}); older agents fail the
// whole apply and the panel behaves as before (no per-node report).
//
//   - The node shows a short Chinese reason (landing_node.sync_error, the field
//     the 落地 page and monitor already display for admin and owner).
//   - A node that was just created and never carried traffic moves to the next
//     free port and is redeployed, at most landingPortMovesMax times; after
//     that, or for a node with traffic / rules, only the error is shown.
//   - Busy ports are remembered for landingBusyPortTTL so the port picker
//     skips them for later nodes.

// LandingNodeReport is one agent-reported node error (agent NodeError).
type LandingNodeReport struct {
	ID     int64  `json:"id"`
	Port   int    `json:"port"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Since  int64  `json:"since"`
}

const (
	LandingNodeErrPortInUse = "port_in_use"
	landingPortMovesMax     = 3
	landingFreshNodeWindow  = time.Hour
	landingBusyPortTTL      = 24 * time.Hour
	landingBusyPortRefresh  = time.Hour
	landingBusyPortsMax     = 64
)

// LandingPortErrorText is the node's short message (admin + owner).
func LandingPortErrorText(rep LandingNodeReport, movesExhausted bool) string {
	reason := rep.Reason
	if r := []rune(reason); len(r) > 60 {
		reason = string(r[:60])
	}
	var text string
	if rep.Code == LandingNodeErrPortInUse {
		text = fmt.Sprintf("端口 %d 被这台机器上的其他程序占用（%s）", rep.Port, reason)
	} else {
		text = fmt.Sprintf("端口 %d 的入站启动失败（%s）", rep.Port, reason)
	}
	if movesExhausted {
		text += fmt.Sprintf("，已自动换端口 %d 次仍失败", landingPortMovesMax)
	}
	return text
}

// nativeBusyPorts parses landing_server.native_busy_ports ({"port": ms}).
func nativeBusyPorts(s *model.LandingServer) map[int]int64 {
	out := map[int]int64{}
	if s == nil || s.NativeBusyPorts == "" {
		return out
	}
	raw := map[string]int64{}
	if json.Unmarshal([]byte(s.NativeBusyPorts), &raw) != nil {
		return out
	}
	for k, at := range raw {
		if p, err := strconv.Atoi(k); err == nil && p > 0 && p <= 65535 {
			out[p] = at
		}
	}
	return out
}

func formatNativeBusyPorts(busy map[int]int64, now time.Time) string {
	type entry struct {
		port int
		at   int64
	}
	list := make([]entry, 0, len(busy))
	for p, at := range busy {
		if now.UnixMilli()-at < landingBusyPortTTL.Milliseconds() {
			list = append(list, entry{p, at})
		}
	}
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at > list[j].at })
	if len(list) > landingBusyPortsMax {
		list = list[:landingBusyPortsMax]
	}
	raw := map[string]int64{}
	for _, e := range list {
		raw[strconv.Itoa(e.port)] = e.at
	}
	b, _ := json.Marshal(raw)
	return string(b)
}

// nativeBusyPortsFresh are the reported busy ports the picker skips now.
func nativeBusyPortsFresh(s *model.LandingServer, now time.Time) map[int]bool {
	out := map[int]bool{}
	for p, at := range nativeBusyPorts(s) {
		if now.UnixMilli()-at < landingBusyPortTTL.Milliseconds() {
			out[p] = true
		}
	}
	return out
}

// landingNodeMovable: just created, never carried traffic, no rule uses it,
// moves left.
func landingNodeMovable(tx *gorm.DB, n *model.LandingNode, now time.Time) (bool, error) {
	if n.Status != "active" || n.PortMoves >= landingPortMovesMax || n.UpBytes != 0 || n.DownBytes != 0 || n.LastSeenUp != 0 || n.LastSeenDown != 0 {
		return false, nil
	}
	if now.UnixMilli()-n.CreatedTime > landingFreshNodeWindow.Milliseconds() {
		return false, nil
	}
	var refs int64
	if err := tx.Model(&model.Forward{}).Where("landing_node_id = ?", n.ID).Count(&refs).Error; err != nil {
		return false, err
	}
	return refs == 0, nil
}

// ApplyNativeLandingNodeErrors reconciles one native server's nodes with the
// agent's current error list (nil / empty = every inbound runs) and returns
// the nodes it moved to a new port (the caller re-pushes the desired state).
// Writes only on change: a steady state costs one small read.
func (r *Repository) ApplyNativeLandingNodeErrors(serverID int64, reports []LandingNodeReport, now time.Time) ([]int64, error) {
	if len(reports) == 0 {
		// Common case (every minute per native server): nothing failed and
		// nothing to clear → one indexed count, no transaction.
		var stale int64
		if err := r.db.Model(&model.LandingNode{}).Where("landing_server_id = ? AND sync_error <> ''", serverID).Count(&stale).Error; err != nil || stale == 0 {
			return nil, err
		}
	}
	var moved []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.First(&s, serverID).Error; err != nil {
			return err
		}
		if s.Source != "native" {
			return nil
		}
		var nodes []model.LandingNode
		if err := tx.Where("landing_server_id = ?", serverID).Order("id").Find(&nodes).Error; err != nil {
			return err
		}
		byID := map[int64]LandingNodeReport{}
		for _, rep := range reports {
			byID[rep.ID] = rep
		}
		busy := nativeBusyPorts(&s)
		busyChanged := false
		for _, rep := range reports {
			if rep.Code != LandingNodeErrPortInUse || rep.Port <= 0 {
				continue
			}
			if at, ok := busy[rep.Port]; !ok || now.UnixMilli()-at >= landingBusyPortRefresh.Milliseconds() {
				busy[rep.Port] = now.UnixMilli()
				busyChanged = true
			}
		}
		if busyChanged {
			s.NativeBusyPorts = formatNativeBusyPorts(busy, now)
			if err := tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("native_busy_ports", s.NativeBusyPorts).Error; err != nil {
				return err
			}
		}
		for i := range nodes {
			n := &nodes[i]
			want := ""
			rep, failed := byID[n.ID]
			// A report about a port the node no longer has (moved meanwhile) is stale.
			if failed && rep.Port == n.Port {
				if rep.Code == LandingNodeErrPortInUse {
					movable, err := landingNodeMovable(tx, n, now)
					if err != nil {
						return err
					}
					if movable {
						port, err := landingFreePort(tx, s)
						if err == nil && port != n.Port {
							if err := tx.Model(&model.LandingNode{}).Where("id = ? AND port = ?", n.ID, n.Port).Updates(map[string]any{"port": port, "port_moves": n.PortMoves + 1, "sync_error": "", "updated_time": now.UnixMilli()}).Error; err != nil {
								return err
							}
							moved = append(moved, n.ID)
							continue
						}
					}
				}
				want = LandingPortErrorText(rep, rep.Code == LandingNodeErrPortInUse && n.PortMoves >= landingPortMovesMax)
			}
			if n.SyncError != want {
				if err := tx.Model(&model.LandingNode{}).Where("id = ?", n.ID).Update("sync_error", want).Error; err != nil {
					return err
				}
			}
		}
		if len(moved) > 0 {
			return tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("revision", gorm.Expr("revision + 1")).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}
