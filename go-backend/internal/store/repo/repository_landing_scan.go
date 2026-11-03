package repo

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 202 「禁止端口扫描」 (native landing nodes): the global switch and
// thresholds, the agent's hits (recorded once per server + seq, the node
// disabled in the same transaction), 解除禁用 and the event list.

// LandingScanGuardVersion is the agent capability (landing_scan_guard) that
// applies Desired.scan_guard and reports scan hits.
const LandingScanGuardVersion = 1

// Event retention: 30 days AND at most 5000 rows.
const (
	landingScanEventKeep    = 30 * 24 * time.Hour
	landingScanEventMaxRows = 5000
)

// LandingScanHit is one Core hit as reported by the agent (Status.scan_guard).
type LandingScanHit struct {
	ID         int64     `json:"id"`
	InboundTag string    `json:"inbound_tag"`
	Type       string    `json:"type"`
	Count      int       `json:"count"`
	Port       int       `json:"port"`
	Host       string    `json:"host"`
	WindowS    int       `json:"window_s"`
	At         time.Time `json:"at"`
	Seq        int64     `json:"seq"`
}

// LandingScanBlock is the reason stored on a disabled node (ScanBlock JSON).
type LandingScanBlock struct {
	Type    string `json:"type"`
	Count   int    `json:"count"`
	Port    int    `json:"port,omitempty"`
	Host    string `json:"host,omitempty"`
	WindowS int    `json:"windowS"`
	At      int64  `json:"at"`
	Seq     int64  `json:"seq"`
}

// Reason is the short text of the block (see landingegress.ScanReason).
func (b LandingScanBlock) Reason() string {
	return landingegress.ScanReason(b.Type, b.Count, b.Port, b.Host, b.WindowS)
}

// LandingScanBlockOf decodes a node's reason (nil = not scan-blocked).
func LandingScanBlockOf(n *model.LandingNode) *LandingScanBlock {
	if n == nil || n.ScanBlockedAt <= 0 {
		return nil
	}
	var b LandingScanBlock
	if json.Unmarshal([]byte(n.ScanBlock), &b) != nil || b.Type == "" {
		b = LandingScanBlock{At: n.ScanBlockedAt}
	}
	if b.At == 0 {
		b.At = n.ScanBlockedAt
	}
	return &b
}

// LandingScanNotice is one node disabled by a scan (for the admin notice).
type LandingScanNotice struct {
	ServerID   int64
	ServerName string
	NodeID     int64
	NodeName   string
	UserID     int64
	Block      LandingScanBlock
}

func scanGuardFromSetting(s model.LandingEgressSetting) landingegress.ScanGuard {
	g := landingegress.ScanGuardDefaults()
	g.On = s.BuiltinScanGuard == nil || *s.BuiltinScanGuard
	for _, f := range []struct {
		v   int
		dst *int
	}{{s.ScanHorizontal, &g.Horizontal}, {s.ScanHorizontalWeb, &g.HorizontalWeb}, {s.ScanVertical, &g.Vertical}, {s.ScanTotal, &g.Total}} {
		if f.v > 0 {
			*f.dst = f.v
		}
	}
	return g
}

// GetLandingScanGuard returns the global setting (defaults while unset).
func (r *Repository) GetLandingScanGuard() (landingegress.ScanGuard, error) {
	var rows []model.LandingEgressSetting
	if err := r.db.Where("id = ?", 1).Limit(1).Find(&rows).Error; err != nil {
		return landingegress.ScanGuardDefaults(), err
	}
	if len(rows) == 0 {
		return landingegress.ScanGuardDefaults(), nil
	}
	return scanGuardFromSetting(rows[0]), nil
}

// SaveLandingScanGuard stores the switch and thresholds (only these columns).
func (r *Repository) SaveLandingScanGuard(g landingegress.ScanGuard) error {
	if err := g.Validate(); err != nil {
		return err
	}
	on := g.On
	row := model.LandingEgressSetting{ID: 1, SourceWhitelist: "[]", GlobalForced: "[]", GlobalDefault: "[]", GlobalAction: landingegress.ActionDirect,
		BuiltinScanGuard: &on, ScanHorizontal: g.Horizontal, ScanHorizontalWeb: g.HorizontalWeb, ScanVertical: g.Vertical, ScanTotal: g.Total,
		GlobalSeedVersion: landingGlobalSeedVersion, PresetSeedVersion: landingPresetSeedVersion, UpdatedTime: time.Now().UnixMilli()}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"builtin_scan_guard", "scan_horizontal", "scan_horizontal_web", "scan_vertical", "scan_total"})}).Create(&row).Error
}

// RecordLandingScanStatus stores the agent's guard state and records the
// hits the panel has not seen yet (seq > server.ScanSeenSeq) in one
// transaction: one event per hit (unique per server + seq), the hit's node
// disabled with its reason unless it already is, and the high-water mark the
// next push sends back as scan_guard.seen. It returns the nodes it disabled
// and whether the high-water mark moved (the agent then needs a push).
func (r *Repository) RecordLandingScanStatus(serverID int64, state string, hits []LandingScanHit, now time.Time) ([]LandingScanNotice, bool, error) {
	var notices []LandingScanNotice
	advanced := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Select("id", "name", "source", "scan_seen_seq", "scan_state").First(&s, serverID).Error; err != nil {
			return err
		}
		updates := map[string]any{}
		if state != s.ScanState {
			updates["scan_state"] = state
		}
		fresh := []LandingScanHit{}
		top := s.ScanSeenSeq
		for _, h := range hits {
			if h.Seq > s.ScanSeenSeq && h.InboundTag != "" {
				fresh = append(fresh, h)
				if h.Seq > top {
					top = h.Seq
				}
			}
		}
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].Seq < fresh[j].Seq })
		if len(fresh) > 0 && s.Source == "native" {
			tags := make([]string, 0, len(fresh))
			for _, h := range fresh {
				tags = append(tags, h.InboundTag)
			}
			var nodes []model.LandingNode
			if err := tx.Where("landing_server_id = ? AND inbound_tag IN ?", serverID, tags).Find(&nodes).Error; err != nil {
				return err
			}
			byTag := map[string]*model.LandingNode{}
			for i := range nodes {
				byTag[nodes[i].InboundTag] = &nodes[i]
			}
			nowMs := now.UnixMilli()
			events := []model.LandingScanEvent{}
			for _, h := range fresh {
				n := byTag[h.InboundTag]
				if n == nil {
					continue // node deleted meanwhile: nothing to disable
				}
				at := h.At.UnixMilli()
				if h.At.IsZero() {
					at = nowMs
				}
				host := h.Host
				if len(host) > 64 {
					host = host[:64]
				}
				kind := h.Type
				if len(kind) > 16 {
					kind = kind[:16]
				}
				block := LandingScanBlock{Type: kind, Count: h.Count, Port: h.Port, Host: host, WindowS: h.WindowS, At: at, Seq: h.Seq}
				ev := model.LandingScanEvent{LandingServerID: serverID, Seq: h.Seq, Type: kind, Count: h.Count, Port: h.Port, Host: host, WindowS: h.WindowS, HitAt: at, CreatedTime: nowMs}
				ev.LandingNodeID, ev.UserID, ev.NodeName = n.ID, n.UserID, n.Name
				// A node already disabled by a scan keeps its first reason.
				if n.ScanBlockedAt == 0 && !n.DeletePending {
					raw, _ := json.Marshal(block)
					if err := tx.Model(&model.LandingNode{}).Where("id = ?", n.ID).Updates(map[string]any{"status": "disabled", "auto_disabled": false, "scan_blocked_at": nowMs, "scan_block": string(raw), "updated_time": nowMs}).Error; err != nil {
						return err
					}
					n.ScanBlockedAt = nowMs
					ev.Blocked = true
					notices = append(notices, LandingScanNotice{ServerID: serverID, ServerName: s.Name, NodeID: n.ID, NodeName: n.Name, UserID: n.UserID, Block: block})
				}
				events = append(events, ev)
			}
			if len(events) > 0 {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&events, 100).Error; err != nil {
					return err
				}
				if err := pruneLandingScanEvents(tx, now); err != nil {
					return err
				}
			}
		}
		if top != s.ScanSeenSeq {
			updates["scan_seen_seq"] = top
			advanced = true
		}
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", serverID).Updates(updates).Error
	})
	if err != nil {
		return nil, false, err
	}
	return notices, advanced, nil
}

// pruneLandingScanEvents keeps 30 days and at most 5000 rows (runs only
// when hits were recorded, i.e. rarely).
func pruneLandingScanEvents(tx *gorm.DB, now time.Time) error {
	if err := tx.Where("created_time < ?", now.Add(-landingScanEventKeep).UnixMilli()).Delete(&model.LandingScanEvent{}).Error; err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.LandingScanEvent{}).Count(&count).Error; err != nil {
		return err
	}
	if count <= landingScanEventMaxRows {
		return nil
	}
	var cut []int64
	if err := tx.Model(&model.LandingScanEvent{}).Order("id DESC").Offset(landingScanEventMaxRows).Limit(1).Pluck("id", &cut).Error; err != nil || len(cut) == 0 {
		return err
	}
	return tx.Where("id <= ?", cut[0]).Delete(&model.LandingScanEvent{}).Error
}

// ErrNotScanBlocked: 解除禁用 on a node that is not disabled by a scan.
var ErrNotScanBlocked = errors.New("该节点没有因端口扫描被禁用")

// LiftLandingScanBlock is 解除禁用: clears the node's scan block and records
// who lifted it on its open event(s). activate also sets the node active (the
// 「解除禁用」 button); the admin edit path sets the status itself.
func (r *Repository) LiftLandingScanBlock(nodeID int64, by string, activate bool, now time.Time) (*model.LandingNode, error) {
	var out model.LandingNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&out, nodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("落地节点不存在")
			}
			return err
		}
		if out.ScanBlockedAt <= 0 {
			return ErrNotScanBlocked
		}
		nowMs := now.UnixMilli()
		updates := map[string]any{"scan_blocked_at": 0, "scan_block": "", "updated_time": nowMs}
		if activate && out.Status == "disabled" {
			updates["status"], updates["auto_disabled"] = "active", false
			out.Status, out.AutoDisabled = "active", false
		}
		if err := tx.Model(&model.LandingNode{}).Where("id = ?", nodeID).Updates(updates).Error; err != nil {
			return err
		}
		out.ScanBlockedAt, out.ScanBlock, out.UpdatedTime = 0, "", nowMs
		if len(by) > 64 {
			by = by[:64]
		}
		return tx.Model(&model.LandingScanEvent{}).Where("landing_node_id = ? AND blocked = ? AND unblocked_at = 0", nodeID, true).
			Updates(map[string]any{"unblocked_at": nowMs, "unblocked_by": by}).Error
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LandingScanEventView is one row of the admin event list.
type LandingScanEventView struct {
	model.LandingScanEvent
	ServerName string `json:"serverName"`
	UserName   string `json:"userName"`
	Reason     string `json:"reason"`
	TypeLabel  string `json:"typeLabel"`
	// NodeBlocked: the node is still disabled by a scan right now.
	NodeBlocked bool `json:"nodeBlocked"`
}

// ListLandingScanEvents returns the newest events (optionally of one node).
func (r *Repository) ListLandingScanEvents(nodeID int64, limit int) ([]LandingScanEventView, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := r.db.Model(&model.LandingScanEvent{})
	if nodeID > 0 {
		q = q.Where("landing_node_id = ?", nodeID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.LandingScanEvent
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	serverIDs, userIDs, nodeIDs := []int64{}, []int64{}, []int64{}
	for _, e := range rows {
		serverIDs = append(serverIDs, e.LandingServerID)
		userIDs = append(userIDs, e.UserID)
		nodeIDs = append(nodeIDs, e.LandingNodeID)
	}
	servers := map[int64]string{}
	if len(serverIDs) > 0 {
		var ss []model.LandingServer
		if err := r.db.Select("id", "name").Where("id IN ?", serverIDs).Find(&ss).Error; err != nil {
			return nil, 0, err
		}
		for _, s := range ss {
			servers[s.ID] = s.Name
		}
	}
	names, err := r.UserNamesByIDs(userIDs)
	if err != nil {
		return nil, 0, err
	}
	blocked := map[int64]bool{}
	if len(nodeIDs) > 0 {
		var ids []int64
		if err := r.db.Model(&model.LandingNode{}).Where("id IN ? AND scan_blocked_at > 0", nodeIDs).Pluck("id", &ids).Error; err != nil {
			return nil, 0, err
		}
		for _, id := range ids {
			blocked[id] = true
		}
	}
	out := make([]LandingScanEventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, LandingScanEventView{LandingScanEvent: e, ServerName: servers[e.LandingServerID], UserName: names[e.UserID],
			Reason: landingegress.ScanReason(e.Type, e.Count, e.Port, e.Host, e.WindowS), TypeLabel: landingegress.ScanTypeLabel(e.Type), NodeBlocked: blocked[e.LandingNodeID]})
	}
	return out, total, nil
}

// LandingScanCounts: nodes disabled by a scan right now and stored events.
func (r *Repository) LandingScanCounts() (blocked int64, events int64, err error) {
	if err = r.db.Model(&model.LandingNode{}).Where("scan_blocked_at > 0").Count(&blocked).Error; err != nil {
		return
	}
	err = r.db.Model(&model.LandingScanEvent{}).Count(&events).Error
	return
}

// NativeLandingScanTargets lists the inbound tags of a native server's
// SS2022 / VLESS nodes (the identities the agent guards).
func NativeLandingScanTargets(nodes []model.LandingNode, serverID int64, isExtra func(string) bool) []string {
	tags := []string{}
	for i := range nodes {
		n := &nodes[i]
		if n.LandingServerID == serverID && !isExtra(n.Protocol) && strings.HasPrefix(n.InboundTag, "flvxl-") {
			tags = append(tags, n.InboundTag)
		}
	}
	sort.Strings(tags)
	return tags
}
