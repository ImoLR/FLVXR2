package repo

import (
	"errors"
	"fmt"
	"sort"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 321 (user 11-28): a user's 专属域名 on a node and his per-server row
// (user_server_quota, 用户设置 → 服务器) exist only while something of his
// still needs that node. Removing the last grant / forward / landing used to
// leave both behind (wings: node 48 after both 沪台 grants were removed).
// These methods compute "still needs" from the stored facts only, so every
// path that removes access (grant delete, batch edits, user-group or group
// changes, tunnel delete or entry change, landing revoke) is covered by the
// same check, and running it twice changes nothing.

// UserNodeGrant is one grant (user_tunnel) whose tunnel uses a node.
type UserNodeGrant struct {
	UserTunnelID int64  `json:"userTunnelId"`
	TunnelID     int64  `json:"tunnelId"`
	TunnelName   string `json:"tunnelName"`
	Status       int    `json:"status"`
	// Entry: the node is one of the tunnel's entries (else a relay / exit hop).
	Entry bool `json:"entry"`
}

type userNodePair struct {
	UserID int64
	NodeID int64
}

func addUserNode(out map[int64]map[int64]bool, nodeID, userID int64) {
	if nodeID <= 0 || userID <= 0 {
		return
	}
	if out[nodeID] == nil {
		out[nodeID] = map[int64]bool{}
	}
	out[nodeID][userID] = true
}

// UserSubdomainAccess maps node id → users who still need an entry name
// there: a grant (any status — 停用 / 到期 grants stay renewable) whose tunnel
// enters on the node, a forward with a port on it (kept ones too), a native
// landing node or landing access on the node's server, a 利群 grant entry.
// Group-granted tunnels restricted to other entries still count (keeping a
// name a little longer is harmless; retiring one wrongly is not). 4 queries.
func (r *Repository) UserSubdomainAccess(nodeIDs []int64) (map[int64]map[int64]bool, error) {
	out := map[int64]map[int64]bool{}
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	var pairs []userNodePair
	if err := r.db.Table("user_tunnel AS ut").Distinct("ut.user_id AS user_id", "c.node_id AS node_id").
		Joins("JOIN chain_tunnel AS c ON c.tunnel_id = ut.tunnel_id AND c.chain_type = '1'").
		Where("c.node_id IN ?", nodeIDs).Scan(&pairs).Error; err != nil {
		return nil, err
	}
	for _, p := range pairs {
		addUserNode(out, p.NodeID, p.UserID)
	}
	pairs = nil
	if err := r.db.Table("forward_port AS fp").Distinct("f.user_id AS user_id", "fp.node_id AS node_id").
		Joins("JOIN forward AS f ON f.id = fp.forward_id").
		Where("fp.node_id IN ?", nodeIDs).Scan(&pairs).Error; err != nil {
		return nil, err
	}
	for _, p := range pairs {
		addUserNode(out, p.NodeID, p.UserID)
	}
	land, err := r.nativeLandingUsers(nodeIDs)
	if err != nil {
		return nil, err
	}
	for nodeID, users := range land {
		for uid := range users {
			addUserNode(out, nodeID, uid)
		}
	}
	var grants []model.LeiKwanGrant
	if err := r.db.Select("user_id", "main_entry_node_id", "backup_entry_node_id", "prev_entry_node_id").
		Where("main_entry_node_id IN ? OR backup_entry_node_id IN ? OR prev_entry_node_id IN ?", nodeIDs, nodeIDs, nodeIDs).Find(&grants).Error; err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range nodeIDs {
		want[id] = true
	}
	for _, g := range grants {
		for _, id := range []int64{g.MainEntryNodeID, g.BackupEntryNodeID, g.PrevEntryNodeID} {
			if want[id] {
				addUserNode(out, id, g.UserID)
			}
		}
	}
	return out, nil
}

// nativeLandingUsers: node id → users with a landing node (pending deletes
// too) or landing access (direct or through an enabled user group) on a
// native landing server of the node. 2 queries.
func (r *Repository) nativeLandingUsers(nodeIDs []int64) (map[int64]map[int64]bool, error) {
	out := map[int64]map[int64]bool{}
	var pairs []userNodePair
	// Same rows as UserSubdomainUsage reads (any source, pending deletes too),
	// so every name the planner would create is also "still needed" — a name
	// can never be created and retired in a loop.
	if err := r.db.Table("landing_node AS ln").Distinct("ln.user_id AS user_id", "s.node_id AS node_id").
		Joins("JOIN landing_server AS s ON s.id = ln.landing_server_id").
		Where("s.node_id IN ?", nodeIDs).Scan(&pairs).Error; err != nil {
		return nil, err
	}
	for _, p := range pairs {
		addUserNode(out, p.NodeID, p.UserID)
	}
	pairs = nil
	if err := r.db.Raw(`SELECT DISTINCT lu.user_id AS user_id, s.node_id AS node_id FROM landing_server_user AS lu
JOIN landing_server AS s ON s.id = lu.landing_server_id WHERE s.source = 'native' AND s.node_id IN ?
UNION SELECT DISTINCT gu.user_id AS user_id, s.node_id AS node_id FROM landing_server_user_group AS lg
JOIN user_group_user AS gu ON gu.user_group_id = lg.user_group_id
JOIN user_group AS ug ON ug.id = lg.user_group_id AND ug.status = 1
JOIN landing_server AS s ON s.id = lg.landing_server_id WHERE s.source = 'native' AND s.node_id IN ?`, nodeIDs, nodeIDs).
		Scan(&pairs).Error; err != nil {
		return nil, err
	}
	for _, p := range pairs {
		addUserNode(out, p.NodeID, p.UserID)
	}
	return out, nil
}

// UserNodeGrants: per (user, node) the grants whose tunnel uses the node
// (entryOnly: as an entry), tunnel name order. 1 query.
func (r *Repository) UserNodeGrants(userIDs, nodeIDs []int64, entryOnly bool) (map[[2]int64][]UserNodeGrant, error) {
	out := map[[2]int64][]UserNodeGrant{}
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	if len(userIDs) == 0 || len(nodeIDs) == 0 {
		return out, nil
	}
	type row struct {
		UserID       int64
		NodeID       int64
		UserTunnelID int64
		TunnelID     int64
		TunnelName   string
		Status       int
		ChainType    string
	}
	q := r.db.Table("user_tunnel AS ut").
		Select("ut.user_id AS user_id, c.node_id AS node_id, ut.id AS user_tunnel_id, ut.tunnel_id AS tunnel_id, t.name AS tunnel_name, ut.status AS status, c.chain_type AS chain_type").
		Joins("JOIN chain_tunnel AS c ON c.tunnel_id = ut.tunnel_id").
		Joins("JOIN tunnel AS t ON t.id = ut.tunnel_id").
		Where("ut.user_id IN ? AND c.node_id IN ?", userIDs, nodeIDs)
	if entryOnly {
		q = q.Where("c.chain_type = '1'")
	}
	var rows []row
	if err := q.Order("t.inx ASC, t.id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	idx := map[[3]int64]int{}
	for _, x := range rows {
		k := [2]int64{x.UserID, x.NodeID}
		key := [3]int64{x.UserID, x.NodeID, x.UserTunnelID}
		if i, ok := idx[key]; ok {
			if x.ChainType == "1" {
				out[k][i].Entry = true
			}
			continue
		}
		idx[key] = len(out[k])
		out[k] = append(out[k], UserNodeGrant{UserTunnelID: x.UserTunnelID, TunnelID: x.TunnelID, TunnelName: x.TunnelName, Status: x.Status, Entry: x.ChainType == "1"})
	}
	return out, nil
}

// UserServerQuotaOrphan is a per-server row (用户设置 → 服务器) of a user who
// no longer needs that server.
type UserServerQuotaOrphan struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"userId"`
	UserName      string `json:"userName"`
	ServerKind    string `json:"serverKind"`
	ServerRefID   int64  `json:"serverRefId"`
	ServerName    string `json:"serverName"`
	UsedIn        int64  `json:"usedIn"`
	UsedOut       int64  `json:"usedOut"`
	PeriodKey     int64  `json:"periodKey"`
	LimitBytes    int64  `json:"limitBytes"`
	LimitOverride bool   `json:"limitOverride"`
	RateOverride  bool   `json:"rateOverride"`
	MaxInbound    int    `json:"maxInboundConnections"`
}

// userServerNeeds: per user, the node ids and shared landing server ids he
// still needs. A node is needed by: any hop (entry / relay / exit) of a
// granted tunnel (any status); a port, a tunnel hop or the target landing of
// one of his forwards; a native landing node / landing access on it. A shared server: landing access or a landing node on it.
// Users in skip are never pruned (see below).
func userServerNeeds(db *gorm.DB, userIDs []int64) (nodes map[int64]map[int64]bool, shared map[int64]map[int64]bool, skip map[int64]bool, err error) {
	nodes, shared, skip = map[int64]map[int64]bool{}, map[int64]map[int64]bool{}, map[int64]bool{}
	add := func(m map[int64]map[int64]bool, uid, id int64) {
		if uid <= 0 || id <= 0 {
			return
		}
		if m[uid] == nil {
			m[uid] = map[int64]bool{}
		}
		m[uid][id] = true
	}
	var pairs []userNodePair
	scan := func(q *gorm.DB, m map[int64]map[int64]bool) error {
		pairs = nil
		if err := q.Scan(&pairs).Error; err != nil {
			return err
		}
		for _, p := range pairs {
			add(m, p.UserID, p.NodeID)
		}
		return nil
	}
	if err = scan(db.Table("user_tunnel AS ut").Distinct("ut.user_id AS user_id", "c.node_id AS node_id").
		Joins("JOIN chain_tunnel AS c ON c.tunnel_id = ut.tunnel_id").Where("ut.user_id IN ?", userIDs), nodes); err != nil {
		return
	}
	if err = scan(db.Table("forward_port AS fp").Distinct("f.user_id AS user_id", "fp.node_id AS node_id").
		Joins("JOIN forward AS f ON f.id = fp.forward_id").Where("f.user_id IN ?", userIDs), nodes); err != nil {
		return
	}
	if err = scan(db.Table("forward AS f").Distinct("f.user_id AS user_id", "c.node_id AS node_id").
		Joins("JOIN chain_tunnel AS c ON c.tunnel_id = f.tunnel_id").Where("f.user_id IN ?", userIDs), nodes); err != nil {
		return
	}
	// A forward into a native landing node: the landing host counts it.
	if err = scan(db.Table("forward AS f").Distinct("f.user_id AS user_id", "s.node_id AS node_id").
		Joins("JOIN landing_node AS ln ON ln.id = f.landing_node_id").
		Joins("JOIN landing_server AS s ON s.id = ln.landing_server_id").
		Where("f.user_id IN ? AND f.landing_node_id > 0 AND s.source = 'native'", userIDs), nodes); err != nil {
		return
	}
	if err = scan(db.Table("forward AS f").Distinct("f.user_id AS user_id", "s.id AS node_id").
		Joins("JOIN landing_node AS ln ON ln.id = f.landing_node_id").
		Joins("JOIN landing_server AS s ON s.id = ln.landing_server_id").
		Where("f.user_id IN ? AND f.landing_node_id > 0 AND s.source <> 'native'", userIDs), shared); err != nil {
		return
	}
	// Landing nodes and landing access: native → its node, others → the server.
	type srow struct {
		UserID   int64
		ServerID int64
		NodeID   int64
		Source   string
	}
	var srows []srow
	if err = db.Raw(`SELECT ln.user_id AS user_id, s.id AS server_id, s.node_id AS node_id, s.source AS source FROM landing_node AS ln
JOIN landing_server AS s ON s.id = ln.landing_server_id WHERE ln.user_id IN ?
UNION SELECT lu.user_id, s.id, s.node_id, s.source FROM landing_server_user AS lu
JOIN landing_server AS s ON s.id = lu.landing_server_id WHERE lu.user_id IN ?
UNION SELECT gu.user_id, s.id, s.node_id, s.source FROM landing_server_user_group AS lg
JOIN user_group_user AS gu ON gu.user_group_id = lg.user_group_id
JOIN user_group AS ug ON ug.id = lg.user_group_id AND ug.status = 1
JOIN landing_server AS s ON s.id = lg.landing_server_id WHERE gu.user_id IN ?`, userIDs, userIDs, userIDs).Scan(&srows).Error; err != nil {
		return
	}
	for _, s := range srows {
		if s.Source == "native" {
			add(nodes, s.UserID, s.NodeID)
		} else {
			add(shared, s.UserID, s.ServerID)
		}
	}
	// 利群 users (grant or rules: entry switches, 备线, 手机本地 move their
	// counted node around) and WG-path users (hops outside chain_tunnel) are
	// never pruned — their rows stay as before.
	for _, q := range []*gorm.DB{
		db.Model(&model.Forward{}).Where("user_id IN ? AND wg_path_id > 0", userIDs),
		db.Model(&model.LeiKwanGrant{}).Where("user_id IN ?", userIDs),
		db.Model(&model.LeiKwanRule{}).Where("user_id IN ?", userIDs),
	} {
		var ids []int64
		if err = q.Distinct().Pluck("user_id", &ids).Error; err != nil {
			return
		}
		for _, uid := range ids {
			skip[uid] = true
		}
	}
	return
}

// userServerQuotaOrphansTx lists the per-server rows of non-admin users that
// no server need backs any more (userIDs nil = every user).
func userServerQuotaOrphansTx(db *gorm.DB, userIDs []int64) ([]model.UserServerQuota, error) {
	q := db.Model(&model.UserServerQuota{}).Where("user_id IN (?)", db.Model(&model.User{}).Select("id").Where("role_id <> 0"))
	if userIDs != nil {
		if len(userIDs) == 0 {
			return nil, nil
		}
		q = q.Where("user_id IN ?", userIDs)
	}
	var rows []model.UserServerQuota
	if err := q.Order("user_id ASC, server_kind ASC, server_ref_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	users := map[int64]bool{}
	for _, row := range rows {
		users[row.UserID] = true
	}
	ids := make([]int64, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nodes, shared, skip, err := userServerNeeds(db, ids)
	if err != nil {
		return nil, err
	}
	// Shared servers that still exist (a row of a deleted server is left to
	// the server's own cleanup).
	var sharedIDs []int64
	if err := db.Model(&model.LandingServer{}).Where("source = ?", "shared").Pluck("id", &sharedIDs).Error; err != nil {
		return nil, err
	}
	isShared := map[int64]bool{}
	for _, id := range sharedIDs {
		isShared[id] = true
	}
	out := []model.UserServerQuota{}
	for _, row := range rows {
		if skip[row.UserID] {
			continue
		}
		switch row.ServerKind {
		case "node":
			if !nodes[row.UserID][row.ServerRefID] {
				out = append(out, row)
			}
		case "landing":
			if isShared[row.ServerRefID] && !shared[row.UserID][row.ServerRefID] {
				out = append(out, row)
			}
		}
	}
	return out, nil
}

// UserServerQuotaOrphans lists the per-server rows nothing backs any more
// (userIDs nil = all users), with user and server names (admin dry run).
func (r *Repository) UserServerQuotaOrphans(userIDs []int64) ([]UserServerQuotaOrphan, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	rows, err := userServerQuotaOrphansTx(r.db, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]UserServerQuotaOrphan, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	uids := map[int64]bool{}
	keys := make([]TrafficPoolServer, 0, len(rows))
	for _, row := range rows {
		uids[row.UserID] = true
		keys = append(keys, TrafficPoolServer{ServerKind: row.ServerKind, ServerRefID: row.ServerRefID})
	}
	ids := make([]int64, 0, len(uids))
	for id := range uids {
		ids = append(ids, id)
	}
	userNames, err := r.UserNames(ids)
	if err != nil {
		return nil, err
	}
	names, err := serverNames(r.db, keys)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, UserServerQuotaOrphan{ID: row.ID, UserID: row.UserID, UserName: userNames[row.UserID], ServerKind: row.ServerKind,
			ServerRefID: row.ServerRefID, ServerName: names[fmt.Sprintf("%s:%d", row.ServerKind, row.ServerRefID)], UsedIn: row.UsedIn, UsedOut: row.UsedOut,
			PeriodKey: row.PeriodKey, LimitBytes: row.LimitBytes, LimitOverride: row.LimitOverride, RateOverride: row.RateOverride,
			MaxInbound: row.MaxInboundConnections})
	}
	return out, nil
}

// PruneUserServerQuotaOrphans deletes the per-server rows nothing backs any
// more (userIDs nil = all users) in one transaction (re-checked inside it).
// Idempotent; returns the deleted rows. Traffic pools are left as they are
// (admin configuration).
func (r *Repository) PruneUserServerQuotaOrphans(userIDs []int64) ([]model.UserServerQuota, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var deleted []model.UserServerQuota
	err := r.db.Transaction(func(tx *gorm.DB) error {
		rows, err := userServerQuotaOrphansTx(tx, userIDs)
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if err := tx.Where("id IN ?", ids).Delete(&model.UserServerQuota{}).Error; err != nil {
			return err
		}
		deleted = rows
		return nil
	})
	return deleted, err
}

// AnyUserServerQuotaRows: cheap guard for the per-mutation prune.
func (r *Repository) AnyUserServerQuotaRows() (bool, error) {
	if r == nil || r.db == nil {
		return false, nil
	}
	var n int64
	err := r.db.Model(&model.UserServerQuota{}).Limit(1).Count(&n).Error
	return n > 0, err
}
