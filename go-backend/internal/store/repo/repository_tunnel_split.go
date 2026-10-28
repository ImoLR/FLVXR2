package repo

import (
	"database/sql"
	"errors"
	"sort"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 155: move one entry of a multi-entry tunnel into its own one-entry
// tunnel ("twin") without touching the original's other entries, hops or
// rules, and the exact reverse. Additive maintenance code: the regular tunnel
// create/update/delete paths are not changed.

// TunnelSplitGroup is one tunnel_group (线路分组) membership of a tunnel with
// the entry nodes the group selects (tunnel_group_tunnel_entry).
type TunnelSplitGroup struct {
	GroupID     int64   `json:"groupId"`
	GroupName   string  `json:"groupName"`
	CreatedTime int64   `json:"createdTime"`
	Entries     []int64 `json:"entries"`
}

// TunnelSplitState is everything the split/merge reads about one tunnel.
type TunnelSplitState struct {
	Tunnel  model.Tunnel             `json:"-"`
	Chains  []model.ChainTunnel      `json:"-"`
	Groups  []TunnelSplitGroup       `json:"groups"`
	Folders []int64                  `json:"folders"` // tunnel_group_new ids (tunnel page folders)
	Lists   []model.TunnelListTunnel `json:"lists"`
	Users   []model.UserTunnel       `json:"-"`
	// Rules of the tunnel: total, with a forward_port on the given node, and
	// bound (plan 152) to the given node.
	Forwards         int64 `json:"forwards"`
	ForwardsOnNode   int64 `json:"forwardsOnNode"`
	BoundToNode      int64 `json:"boundToNode"`
	Federation       int64 `json:"federationBindings"`
	HasLeiKwanPipe   bool  `json:"hasLeiKwanPipe"`
	ForwardChainRows int64 `json:"forwardChainRows"`
}

// EntryNodeIDs lists the entry nodes in chain order.
func (s *TunnelSplitState) EntryNodeIDs() []int64 {
	out := make([]int64, 0, 2)
	for _, c := range s.Chains {
		if c.ChainType == "1" {
			out = append(out, c.NodeID)
		}
	}
	return out
}

// EntryRow returns the chain row of an entry node.
func (s *TunnelSplitState) EntryRow(nodeID int64) (model.ChainTunnel, bool) {
	for _, c := range s.Chains {
		if c.ChainType == "1" && c.NodeID == nodeID {
			return c, true
		}
	}
	return model.ChainTunnel{}, false
}

// LoadTunnelSplitState reads one tunnel (nil when it does not exist); nodeID
// is the entry the forward_port/binding counters look at (0 = none).
func (r *Repository) LoadTunnelSplitState(tunnelID, nodeID int64) (*TunnelSplitState, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	return r.loadTunnelSplitStateTx(r.db, tunnelID, nodeID)
}

func (r *Repository) loadTunnelSplitStateTx(tx *gorm.DB, tunnelID, nodeID int64) (*TunnelSplitState, error) {
	s := &TunnelSplitState{}
	if err := tx.Where("id = ?", tunnelID).Limit(1).Find(&s.Tunnel).Error; err != nil {
		return nil, err
	}
	if s.Tunnel.ID == 0 {
		return nil, nil
	}
	if err := tx.Where("tunnel_id = ?", tunnelID).Order("chain_type ASC, inx ASC, id ASC").Find(&s.Chains).Error; err != nil {
		return nil, err
	}
	var members []model.TunnelGroupTunnel
	if err := tx.Where("tunnel_id = ?", tunnelID).Order("tunnel_group_id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	var entryRows []model.TunnelGroupTunnelEntry
	if err := tx.Where("tunnel_id = ?", tunnelID).Order("id ASC").Find(&entryRows).Error; err != nil {
		return nil, err
	}
	groupIDs := make([]int64, 0, len(members))
	for _, m := range members {
		groupIDs = append(groupIDs, m.TunnelGroupID)
	}
	names := map[int64]string{}
	if len(groupIDs) > 0 {
		var groups []model.TunnelGroup
		if err := tx.Select("id, name").Where("id IN ?", groupIDs).Find(&groups).Error; err != nil {
			return nil, err
		}
		for _, g := range groups {
			names[g.ID] = g.Name
		}
	}
	for _, m := range members {
		g := TunnelSplitGroup{GroupID: m.TunnelGroupID, GroupName: names[m.TunnelGroupID], CreatedTime: m.CreatedTime, Entries: []int64{}}
		for _, e := range entryRows {
			if e.TunnelGroupID == m.TunnelGroupID {
				g.Entries = append(g.Entries, e.NodeID)
			}
		}
		s.Groups = append(s.Groups, g)
	}
	if err := tx.Model(&model.TunnelGroupTunnelNew{}).Where("tunnel_id = ?", tunnelID).Order("tunnel_group_id ASC").Pluck("tunnel_group_id", &s.Folders).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("tunnel_id = ?", tunnelID).Order("tunnel_list_id ASC").Find(&s.Lists).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("tunnel_id = ?", tunnelID).Order("id ASC").Find(&s.Users).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&model.Forward{}).Where("tunnel_id = ?", tunnelID).Count(&s.Forwards).Error; err != nil {
		return nil, err
	}
	if nodeID > 0 {
		if err := tx.Model(&model.ForwardPort{}).
			Joins("JOIN forward ON forward.id = forward_port.forward_id").
			Where("forward.tunnel_id = ? AND forward_port.node_id = ?", tunnelID, nodeID).
			Count(&s.ForwardsOnNode).Error; err != nil {
			return nil, err
		}
		var bindings []string
		if err := tx.Model(&model.Forward{}).Where("tunnel_id = ? AND entry_node_ids <> ''", tunnelID).Pluck("entry_node_ids", &bindings).Error; err != nil {
			return nil, err
		}
		for _, b := range bindings {
			for _, id := range ParseEntryNodeIDs(b) {
				if id == nodeID {
					s.BoundToNode++
				}
			}
		}
	}
	if err := tx.Model(&model.FederationTunnelBinding{}).Where("tunnel_id = ?", tunnelID).Count(&s.Federation).Error; err != nil {
		return nil, err
	}
	var pipes int64
	if err := tx.Model(&model.LeiKwanPipe{}).Where("tunnel_id = ?", tunnelID).Count(&pipes).Error; err != nil {
		return nil, err
	}
	s.HasLeiKwanPipe = pipes > 0
	if err := tx.Model(&model.ForwardChain{}).Where("tunnel_id = ?", tunnelID).Count(&s.ForwardChainRows).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// TunnelIDByName returns the id of the tunnel with exactly this name (0 when
// none; the lowest id when several).
func (r *Repository) TunnelIDByName(name string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var ids []int64
	if err := r.db.Model(&model.Tunnel{}).Where("name = ?", name).Order("id ASC").Limit(1).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}

// TunnelNameTakenByOther reports whether another tunnel already has name.
func (r *Repository) TunnelNameTakenByOther(name string, tunnelID int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	var cnt int64
	err := r.db.Model(&model.Tunnel{}).Where("name = ? AND id <> ?", name, tunnelID).Count(&cnt).Error
	return cnt > 0, err
}

// TunnelSplitResult tells the handler what the transaction changed.
type TunnelSplitResult struct {
	InIP      string                  `json:"inIp"`
	TwinInx   int                     `json:"twinInx"`
	Shifted   int64                   `json:"shiftedTunnels"`
	Joined    []int64                 `json:"twinJoinedGroups"`
	Left      []int64                 `json:"originalLeftGroups"`
	Revoked   []RevokedUserTunnelPair `json:"revoked"`
	Folders   int                     `json:"folders"`
	ListsCopy int                     `json:"lists"`
}

// ApplyTunnelEntrySplitTx moves entry nodeID of tunnelID to twinID (already
// created with that entry only): the original loses the entry row and gets
// name + in_ip (TunnelEntryInIPTx), the twin is ordered right after the
// original (later tunnels shift by one) and inherits folder/list rows and the
// moved entry's tunnel-group membership. Grants of groups the original left
// are revoked like /group/tunnel/assign does.
func (r *Repository) ApplyTunnelEntrySplitTx(tx *gorm.DB, tunnelID, twinID, nodeID int64, name string, now int64, build func([]model.Node, string) string) (*TunnelSplitResult, error) {
	if tx == nil {
		return nil, errors.New("database unavailable")
	}
	s, err := r.loadTunnelSplitStateTx(tx, tunnelID, nodeID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("隧道不存在")
	}
	if _, ok := s.EntryRow(nodeID); !ok {
		return nil, errors.New("入口不属于该隧道")
	}
	remaining := make([]int64, 0, 2)
	for _, id := range s.EntryNodeIDs() {
		if id != nodeID {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) == 0 {
		return nil, errors.New("隧道只剩这一个入口，不能拆分")
	}
	res := &TunnelSplitResult{Joined: []int64{}, Left: []int64{}, Revoked: []RevokedUserTunnelPair{}}
	// Before the chain rows change (TunnelEntryInIPTx compares old/new sets).
	if res.InIP, err = r.TunnelEntryInIPTx(tx, tunnelID, nil, remaining, s.Tunnel.IPPreference, build); err != nil {
		return nil, err
	}
	if err := tx.Where("tunnel_id = ? AND chain_type = '1' AND node_id = ?", tunnelID, nodeID).Delete(&model.ChainTunnel{}).Error; err != nil {
		return nil, err
	}
	inIP := sql.NullString{String: res.InIP, Valid: res.InIP != ""}
	if err := tx.Model(&model.Tunnel{}).Where("id = ?", tunnelID).Updates(map[string]interface{}{"name": name, "in_ip": inIP, "updated_time": now}).Error; err != nil {
		return nil, err
	}
	shift := tx.Model(&model.Tunnel{}).Where("inx > ? AND id <> ? AND id <> ?", s.Tunnel.Inx, tunnelID, twinID).Update("inx", gorm.Expr("inx + 1"))
	if shift.Error != nil {
		return nil, shift.Error
	}
	res.Shifted = shift.RowsAffected
	res.TwinInx = s.Tunnel.Inx + 1
	if err := tx.Model(&model.Tunnel{}).Where("id = ?", twinID).Update("inx", res.TwinInx).Error; err != nil {
		return nil, err
	}
	for _, folder := range s.Folders {
		row := model.TunnelGroupTunnelNew{TunnelGroupID: folder, TunnelID: twinID, CreatedTime: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return nil, err
		}
		res.Folders++
	}
	for _, list := range s.Lists {
		row := model.TunnelListTunnel{TunnelListID: list.TunnelListID, TunnelID: twinID, Inx: list.Inx, CreatedTime: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return nil, err
		}
		res.ListsCopy++
	}
	for _, g := range s.Groups {
		has := false
		for _, id := range g.Entries {
			has = has || id == nodeID
		}
		if !has {
			continue // the group never selected the moved entry
		}
		member := model.TunnelGroupTunnel{TunnelGroupID: g.GroupID, TunnelID: twinID, CreatedTime: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
			return nil, err
		}
		entry := model.TunnelGroupTunnelEntry{TunnelGroupID: g.GroupID, TunnelID: twinID, NodeID: nodeID, CreatedTime: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
			return nil, err
		}
		res.Joined = append(res.Joined, g.GroupID)
		if err := tx.Where("tunnel_group_id = ? AND tunnel_id = ? AND node_id = ?", g.GroupID, tunnelID, nodeID).Delete(&model.TunnelGroupTunnelEntry{}).Error; err != nil {
			return nil, err
		}
		if len(g.Entries) > 1 {
			continue
		}
		// The moved entry was the group's only selection: the original leaves.
		if err := tx.Where("tunnel_group_id = ? AND tunnel_id = ?", g.GroupID, tunnelID).Delete(&model.TunnelGroupTunnel{}).Error; err != nil {
			return nil, err
		}
		res.Left = append(res.Left, g.GroupID)
		var current []int64
		if err := tx.Model(&model.TunnelGroupTunnel{}).Where("tunnel_group_id = ?", g.GroupID).Pluck("tunnel_id", &current).Error; err != nil {
			return nil, err
		}
		revoked, err := r.RevokeStaleTunnelGroupGrantsTx(tx, g.GroupID, current)
		if err != nil {
			return nil, err
		}
		res.Revoked = append(res.Revoked, revoked...)
	}
	return res, nil
}

// TunnelMergeResult tells the handler what the merge transaction changed.
type TunnelMergeResult struct {
	InIP    string  `json:"inIp"`
	Joined  []int64 `json:"originalJoinedGroups"`
	Shifted int64   `json:"shiftedTunnels"`
}

// ApplyTunnelEntryMergeTx is the reverse of ApplyTunnelEntrySplitTx: the
// twin's only entry row is added back to the original (last entry), name and
// in_ip restored (inIP nil = recompute), the twin's tunnel-group selections
// are given back to the original and later tunnels shift back by one. The twin
// itself is deleted by the caller through the normal tunnel delete path.
func (r *Repository) ApplyTunnelEntryMergeTx(tx *gorm.DB, tunnelID, twinID int64, name string, inIP *string, now int64, build func([]model.Node, string) string) (*TunnelMergeResult, error) {
	if tx == nil {
		return nil, errors.New("database unavailable")
	}
	orig, err := r.loadTunnelSplitStateTx(tx, tunnelID, 0)
	if err != nil {
		return nil, err
	}
	twin, err := r.loadTunnelSplitStateTx(tx, twinID, 0)
	if err != nil {
		return nil, err
	}
	if orig == nil || twin == nil {
		return nil, errors.New("隧道不存在")
	}
	twinEntries := twin.EntryNodeIDs()
	if len(twinEntries) != 1 {
		return nil, errors.New("拆分出的隧道必须正好有一个入口")
	}
	nodeID := twinEntries[0]
	if _, ok := orig.EntryRow(nodeID); ok {
		return nil, errors.New("原隧道已经有这个入口")
	}
	row, _ := twin.EntryRow(nodeID)
	entries := append(orig.EntryNodeIDs(), nodeID)
	res := &TunnelMergeResult{Joined: []int64{}}
	if inIP != nil {
		res.InIP = *inIP
	} else if res.InIP, err = r.TunnelEntryInIPTx(tx, tunnelID, nil, entries, orig.Tunnel.IPPreference, build); err != nil {
		return nil, err
	}
	maxInx := int64(0)
	for _, c := range orig.Chains {
		if c.ChainType == "1" && c.Inx.Valid && c.Inx.Int64 > maxInx {
			maxInx = c.Inx.Int64
		}
	}
	back := model.ChainTunnel{
		TunnelID: tunnelID, ChainType: "1", NodeID: nodeID,
		Strategy: row.Strategy, Inx: sql.NullInt64{Int64: maxInx + 1, Valid: true},
		Protocol: row.Protocol, ConnectIP: row.ConnectIP, ConnectIPType: row.ConnectIPType,
	}
	if err := tx.Create(&back).Error; err != nil {
		return nil, err
	}
	value := sql.NullString{String: res.InIP, Valid: res.InIP != ""}
	if err := tx.Model(&model.Tunnel{}).Where("id = ?", tunnelID).Updates(map[string]interface{}{"name": name, "in_ip": value, "updated_time": now}).Error; err != nil {
		return nil, err
	}
	member := map[int64]bool{}
	for _, g := range orig.Groups {
		member[g.GroupID] = true
	}
	for _, g := range twin.Groups {
		if !member[g.GroupID] {
			created := g.CreatedTime
			if created <= 0 {
				created = now
			}
			m := model.TunnelGroupTunnel{TunnelGroupID: g.GroupID, TunnelID: tunnelID, CreatedTime: created}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&m).Error; err != nil {
				return nil, err
			}
			res.Joined = append(res.Joined, g.GroupID)
		}
		for _, id := range g.Entries {
			e := model.TunnelGroupTunnelEntry{TunnelGroupID: g.GroupID, TunnelID: tunnelID, NodeID: id, CreatedTime: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error; err != nil {
				return nil, err
			}
		}
	}
	shift := tx.Model(&model.Tunnel{}).Where("inx > ? AND id <> ?", twin.Tunnel.Inx, twinID).Update("inx", gorm.Expr("inx - 1"))
	if shift.Error != nil {
		return nil, shift.Error
	}
	res.Shifted = shift.RowsAffected
	sort.Slice(res.Joined, func(i, j int) bool { return res.Joined[i] < res.Joined[j] })
	return res, nil
}

// CopyUserTunnelLimits gives the twin's user_tunnel the original's limits
// (quota, rule count, expiry, reset day, speed limit, status); counters stay.
func (r *Repository) CopyUserTunnelLimits(from model.UserTunnel, toID int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.UserTunnel{}).Where("id = ?", toID).Updates(map[string]interface{}{
		"num": from.Num, "flow": from.Flow, "exp_time": from.ExpTime, "flow_reset_time": from.FlowResetTime,
		"speed_id": from.SpeedID, "status": from.Status,
	}).Error
}
