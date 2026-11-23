package repo

// Plan 266: 利群专线 / 授权 / 规则 / 主机状态 data access. Nothing here touches
// tunnel, forward or forward_port rows except the read-only lookups at the end.

import (
	"errors"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func (r *Repository) GetLeiKwanSetting() (*model.LeiKwanSetting, error) {
	var s model.LeiKwanSetting
	err := r.db.First(&s, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.LeiKwanSetting{ID: 1, GroupName: "利群汇聚"}, nil
	}
	return &s, err
}

func (r *Repository) SaveLeiKwanSetting(s *model.LeiKwanSetting) error {
	s.ID = 1
	s.UpdatedTime = time.Now().UnixMilli()
	if strings.TrimSpace(s.GroupName) == "" {
		s.GroupName = "利群汇聚"
	}
	return r.db.Save(s).Error
}

func (r *Repository) ListLeiKwanLines() ([]model.LeiKwanLine, error) {
	out := []model.LeiKwanLine{}
	err := r.db.Order("inx, id").Find(&out).Error
	return out, err
}

func (r *Repository) GetLeiKwanLine(id int64) (*model.LeiKwanLine, error) {
	var l model.LeiKwanLine
	err := r.db.First(&l, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &l, err
}

func (r *Repository) GetLeiKwanLineByRoute(accountID, routeID int64) (*model.LeiKwanLine, error) {
	var l model.LeiKwanLine
	err := r.db.Where("account_id = ? AND route_id = ?", accountID, routeID).First(&l).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &l, err
}

func (r *Repository) SaveLeiKwanLine(l *model.LeiKwanLine) error {
	now := time.Now().UnixMilli()
	if l.ID == 0 {
		l.CreatedTime = now
	}
	l.UpdatedTime = now
	return r.db.Save(l).Error
}

func (r *Repository) DeleteLeiKwanLine(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.LeiKwanRule{}).Where("line_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("专线上还有利群转发，不能删除")
		}
		return tx.Delete(&model.LeiKwanLine{}, id).Error
	})
}

func (r *Repository) ListLeiKwanGrants() ([]model.LeiKwanGrant, error) {
	out := []model.LeiKwanGrant{}
	err := r.db.Order("id").Find(&out).Error
	return out, err
}

func (r *Repository) GetLeiKwanGrant(userID int64) (*model.LeiKwanGrant, error) {
	var g model.LeiKwanGrant
	err := r.db.Where("user_id = ?", userID).First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &g, err
}

func (r *Repository) SaveLeiKwanGrant(g *model.LeiKwanGrant) error {
	now := time.Now().UnixMilli()
	if g.ID == 0 {
		g.CreatedTime = now
	}
	g.UpdatedTime = now
	return r.db.Save(g).Error
}

func (r *Repository) DeleteLeiKwanGrant(userID int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.LeiKwanRule{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("该用户还有利群转发，请先删除这些转发再取消授权")
		}
		return tx.Where("user_id = ?", userID).Delete(&model.LeiKwanGrant{}).Error
	})
}

// CountLeiKwanGrantsByEntry: how many grants use each node as main entry.
func (r *Repository) CountLeiKwanGrantsByEntry() (map[int64]int, error) {
	var rows []struct {
		MainEntryNodeID int64
		N               int
	}
	err := r.db.Model(&model.LeiKwanGrant{}).Select("main_entry_node_id, count(*) as n").Group("main_entry_node_id").Scan(&rows).Error
	out := map[int64]int{}
	for _, row := range rows {
		out[row.MainEntryNodeID] = row.N
	}
	return out, err
}

// ListLeiKwanRules lists one user's rules (userID > 0) or all of them.
func (r *Repository) ListLeiKwanRules(userID int64) ([]model.LeiKwanRule, error) {
	out := []model.LeiKwanRule{}
	q := r.db.Order("line_id, id")
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Find(&out).Error
	return out, err
}

func (r *Repository) GetLeiKwanRule(id int64) (*model.LeiKwanRule, error) {
	var x model.LeiKwanRule
	err := r.db.First(&x, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &x, err
}

func (r *Repository) GetLeiKwanRuleByForward(forwardID int64) (*model.LeiKwanRule, error) {
	var x model.LeiKwanRule
	err := r.db.Where("forward_id = ?", forwardID).First(&x).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &x, err
}

// CountLeiKwanRulesByUser (plan 276): 利群 rules per owner (user list, the
// delete dialog says they go with the user).
func (r *Repository) CountLeiKwanRulesByUser() (map[int64]int64, error) {
	var rows []struct {
		UserID int64
		N      int64
	}
	err := r.db.Model(&model.LeiKwanRule{}).Select("user_id, count(*) as n").Group("user_id").Scan(&rows).Error
	out := map[int64]int64{}
	for _, row := range rows {
		out[row.UserID] = row.N
	}
	return out, err
}

func (r *Repository) CountLeiKwanRules(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.LeiKwanRule{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// CountTunnelForwards (plan 270): rules on a tunnel — the 「旧管道」 card of
// the 利群汇聚 page shows how many rules still use a pipe-mode tunnel.
func (r *Repository) CountTunnelForwards(tunnelID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.Forward{}).Where("tunnel_id = ?", tunnelID).Count(&n).Error
	return n, err
}

// CountLeiKwanRulesByBackup: rules that use a backup forward (shared backups,
// e.g. one forward for 280 and 282, are only deleted with the last rule).
func (r *Repository) CountLeiKwanRulesByBackup(forwardID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.LeiKwanRule{}).Where("backup_forward_id = ?", forwardID).Count(&n).Error
	return n, err
}

// leiKwanRuleCounters: the billed counters only the flow batch and the user
// reset write (plan 271) — a handler's read-modify-write of a rule must not
// put back the bytes it read.
var leiKwanRuleCounters = []string{"in_flow", "out_flow", "backup_in_flow", "backup_out_flow"}

func (r *Repository) SaveLeiKwanRule(x *model.LeiKwanRule) error {
	now := time.Now().UnixMilli()
	x.UpdatedTime = now
	if x.ID == 0 {
		x.CreatedTime = now
		return r.db.Create(x).Error
	}
	// all_entries (plan 272) has its own admin switch (SetLeiKwanRuleAllEntries).
	res := r.db.Model(x).Select("*").Omit(append([]string{"id", "all_entries"}, leiKwanRuleCounters...)...).Updates(x)
	if res.Error == nil && res.RowsAffected == 0 {
		return r.db.Create(x).Error // like Save: a preset id that is not there yet
	}
	return res.Error
}

// LeiKwanBackupRef: one 利群 rule's link to its 备线 forward (plan 271).
type LeiKwanBackupRef struct {
	RuleID          int64
	UserID          int64
	ForwardID       int64 // the 利群 forward id (LeiKwan side)
	Name            string
	BackupForwardID int64
	LandingNodeID   int64
	BackupInFlow    int64
	BackupOutFlow   int64
}

// ListLeiKwanBackupRefs: every rule that has a 备线 forward.
func (r *Repository) ListLeiKwanBackupRefs() ([]LeiKwanBackupRef, error) {
	out := []LeiKwanBackupRef{}
	err := r.db.Model(&model.LeiKwanRule{}).
		Select("id AS rule_id, user_id, forward_id, name, backup_forward_id, landing_node_id, backup_in_flow, backup_out_flow").
		Where("backup_forward_id > 0").Order("id").Scan(&out).Error
	return out, err
}

func (r *Repository) DeleteLeiKwanRule(id int64) error {
	return r.db.Delete(&model.LeiKwanRule{}, id).Error
}

func (r *Repository) ListLeiKwanHostStates() ([]model.LeiKwanHostState, error) {
	out := []model.LeiKwanHostState{}
	err := r.db.Order("node_id").Find(&out).Error
	return out, err
}

func (r *Repository) SaveLeiKwanHostState(nodeID int64, data, source string, capturedAt int64) error {
	var cur model.LeiKwanHostState
	err := r.db.Where("node_id = ?", nodeID).First(&cur).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	cur.NodeID, cur.Data, cur.Source, cur.CapturedAt = nodeID, data, source, capturedAt
	return r.db.Save(&cur).Error
}

// FirstAdminUserID: the owner of automatic 备线 forwards (like the existing
// hand-made 備線·NNN rules). 0 if the panel has no admin.
func (r *Repository) FirstAdminUserID() int64 {
	var u model.User
	if err := r.db.Select("id").Where("role_id = 0").Order("id").First(&u).Error; err != nil {
		return 0
	}
	return u.ID
}

// AdminUserIDs (plan 297): every admin account (role 0). Their 利群 rules
// are never held to one entry by 「只开主入口」.
func (r *Repository) AdminUserIDs() (map[int64]bool, error) {
	var ids []int64
	if err := r.db.Model(&model.User{}).Where("role_id = 0").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// LeiKwanBackupForward is the read-only view of a 备线 forward.
type LeiKwanBackupForward struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TunnelID   int64  `json:"tunnelId"`
	TunnelName string `json:"tunnelName"`
	RemoteAddr string `json:"remoteAddr"`
	Status     int    `json:"status"`
	InFlow     int64  `json:"inFlow"`
	OutFlow    int64  `json:"outFlow"`
	EntryHost  string `json:"entryHost"`
	EntryPort  int    `json:"entryPort"`
}

func (r *Repository) LeiKwanBackupForwards(ids []int64) (map[int64]LeiKwanBackupForward, error) {
	out := map[int64]LeiKwanBackupForward{}
	if len(ids) == 0 {
		return out, nil
	}
	var forwards []model.Forward
	if err := r.db.Select("id, name, tunnel_id, remote_addr, status, in_flow, out_flow").Where("id IN ?", ids).Find(&forwards).Error; err != nil {
		return nil, err
	}
	tunnelIDs := make([]int64, 0, len(forwards))
	for _, f := range forwards {
		tunnelIDs = append(tunnelIDs, f.TunnelID)
	}
	names := map[int64]string{}
	if len(tunnelIDs) > 0 {
		var tunnels []model.Tunnel
		if err := r.db.Select("id, name").Where("id IN ?", tunnelIDs).Find(&tunnels).Error; err != nil {
			return nil, err
		}
		for _, t := range tunnels {
			names[t.ID] = t.Name
		}
	}
	var ports []model.ForwardPort
	if err := r.db.Where("forward_id IN ?", ids).Order("id").Find(&ports).Error; err != nil {
		return nil, err
	}
	nodeIDs := make([]int64, 0, len(ports))
	for _, p := range ports {
		nodeIDs = append(nodeIDs, p.NodeID)
	}
	hosts := map[int64]string{}
	if len(nodeIDs) > 0 {
		var nodes []model.Node
		if err := r.db.Select("id, server_ip").Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
			return nil, err
		}
		for _, n := range nodes {
			hosts[n.ID] = n.ServerIP
		}
	}
	for _, f := range forwards {
		v := LeiKwanBackupForward{ID: f.ID, Name: f.Name, TunnelID: f.TunnelID, TunnelName: names[f.TunnelID], RemoteAddr: f.RemoteAddr, Status: f.Status, InFlow: f.InFlow, OutFlow: f.OutFlow}
		for _, p := range ports {
			if p.ForwardID == f.ID {
				v.EntryHost, v.EntryPort = hosts[p.NodeID], p.Port
				if p.InIP.Valid && strings.TrimSpace(p.InIP.String) != "" {
					v.EntryHost = strings.TrimSpace(p.InIP.String)
				}
				break
			}
		}
		out[f.ID] = v
	}
	return out, nil
}

// FindForwardByEntryAddr finds the FLVX forward listening on host:port (the
// backup target a host's 日志快切 line points at). Read-only, import only.
func (r *Repository) FindForwardByEntryAddr(host string, port int) (int64, error) {
	var nodes []model.Node
	if err := r.db.Select("id").Where("server_ip = ? OR server_ip_v4 = ?", host, host).Find(&nodes).Error; err != nil {
		return 0, err
	}
	for _, n := range nodes {
		var fp model.ForwardPort
		err := r.db.Where("node_id = ? AND port = ?", n.ID, port).Order("id").First(&fp).Error
		if err == nil {
			return fp.ForwardID, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}
	return 0, nil
}

// LeiKwanTunnelBrief: a tunnel offered as 备线隧道 (plan 268: any tunnel),
// with its path (entry → middle hops → exits) and 线路分组.
type LeiKwanTunnelBrief struct {
	ID           int64                   `json:"id"`
	Name         string                  `json:"name"`
	Type         int                     `json:"type"`
	EntryNames   string                  `json:"entryNames"`
	EntryNodeIDs []int64                 `json:"entryNodeIds"`
	Hops         []string                `json:"hops"`
	Exits        []string                `json:"exits"`
	Groups       []LeiKwanTunnelGroupRef `json:"groups"`
}

func (r *Repository) ListLeiKwanBackupTunnels() ([]LeiKwanTunnelBrief, error) {
	var tunnels []model.Tunnel
	if err := r.db.Select("id, name, type").Where("status = 1").Order("inx, id").Find(&tunnels).Error; err != nil {
		return nil, err
	}
	var chains []model.ChainTunnel
	if err := r.db.Select("tunnel_id, chain_type, node_id, inx").Order("tunnel_id, chain_type, inx, id").Find(&chains).Error; err != nil {
		return nil, err
	}
	var nodes []model.Node
	if err := r.db.Select("id, name").Find(&nodes).Error; err != nil {
		return nil, err
	}
	nodeName := map[int64]string{}
	for _, n := range nodes {
		nodeName[n.ID] = n.Name
	}
	entries, entryIDs, hops, exits := map[int64][]string{}, map[int64][]int64{}, map[int64][]string{}, map[int64][]string{}
	for _, c := range chains {
		switch c.ChainType {
		case "1":
			entries[c.TunnelID] = append(entries[c.TunnelID], nodeName[c.NodeID])
			entryIDs[c.TunnelID] = append(entryIDs[c.TunnelID], c.NodeID)
		case "2":
			hops[c.TunnelID] = append(hops[c.TunnelID], nodeName[c.NodeID])
		case "3":
			exits[c.TunnelID] = append(exits[c.TunnelID], nodeName[c.NodeID])
		}
	}
	groups, err := r.LeiKwanTunnelGroups()
	if err != nil {
		return nil, err
	}
	out := make([]LeiKwanTunnelBrief, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, LeiKwanTunnelBrief{ID: t.ID, Name: t.Name, Type: t.Type, EntryNames: strings.Join(entries[t.ID], "、"),
			EntryNodeIDs: nonNilIDs(entryIDs[t.ID]), Hops: nonNilNames(hops[t.ID]), Exits: nonNilNames(exits[t.ID]), Groups: nonNilGroups(groups[t.ID])})
	}
	return out, nil
}

func nonNilIDs(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

func nonNilNames(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilGroups(v []LeiKwanTunnelGroupRef) []LeiKwanTunnelGroupRef {
	if v == nil {
		return []LeiKwanTunnelGroupRef{}
	}
	return v
}

// LeiKwanLandingTarget resolves a landing node to its public host:port.
func (r *Repository) LeiKwanLandingTarget(landingNodeID int64) (*model.LandingNode, *model.LandingServer, error) {
	n, err := r.GetLandingNode(landingNodeID)
	if err != nil || n == nil {
		return nil, nil, errors.New("落地节点不存在")
	}
	s, err := r.GetLandingServer(n.LandingServerID)
	if err != nil || s == nil {
		return nil, nil, errors.New("落地服务器不存在")
	}
	return n, s, nil
}

// LeiKwanLandingServerByHost: the panel landing server whose public host is
// host (hint 「可换成面板落地节点」 for imported manual targets).
func (r *Repository) LeiKwanLandingServerByHost(host string) (*model.LandingServer, error) {
	var s model.LandingServer
	err := r.db.Where("public_host = ?", host).Order("id").First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

// LeiKwanUserNames maps user ids to names for the admin views.
func (r *Repository) LeiKwanUserNames(ids []int64) map[int64]string {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out
	}
	var users []model.User
	if err := r.db.Select("id, user").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return out
	}
	for _, u := range users {
		out[u.ID] = u.User
	}
	return out
}

// LeiKwanUsersExpired (plan 268): the users that are expired at nowMs — their
// 利群 rules show 已到期 like normal rules and no new ones can be created.
func (r *Repository) LeiKwanUsersExpired(ids []int64, nowMs int64) map[int64]bool {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out
	}
	var users []model.User
	if err := r.db.Select("id, exp_time").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return out
	}
	for _, u := range users {
		if u.ExpTime > 0 && u.ExpTime <= nowMs {
			out[u.ID] = true
		}
	}
	return out
}

// LeiKwanTunnelGroups (plan 268): the 线路分组 of each tunnel, ordered like
// the rule picker (inx, id), for the 备线隧道 picker.
func (r *Repository) LeiKwanTunnelGroups() (map[int64][]LeiKwanTunnelGroupRef, error) {
	var groups []model.TunnelGroup
	if err := r.db.Select("id, name, color, inx").Where("status = 1").Order("inx, id").Find(&groups).Error; err != nil {
		return nil, err
	}
	var links []model.TunnelGroupTunnel
	if err := r.db.Select("tunnel_group_id, tunnel_id").Find(&links).Error; err != nil {
		return nil, err
	}
	byGroup := map[int64][]int64{}
	for _, l := range links {
		byGroup[l.TunnelGroupID] = append(byGroup[l.TunnelGroupID], l.TunnelID)
	}
	out := map[int64][]LeiKwanTunnelGroupRef{}
	for i, g := range groups {
		for _, t := range byGroup[g.ID] {
			out[t] = append(out[t], LeiKwanTunnelGroupRef{ID: g.ID, Name: g.Name, Color: g.Color, Order: i})
		}
	}
	return out, nil
}

// LeiKwanTunnelGroupRef is one 线路分组 a tunnel belongs to.
type LeiKwanTunnelGroupRef struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Order int    `json:"order"`
}

// FindNodeIDByServerIP: the FLVX node with this public address (names only).
func (r *Repository) FindNodeIDByServerIP(host string) (int64, error) {
	var n model.Node
	err := r.db.Select("id").Where("server_ip = ? OR server_ip_v4 = ?", host, host).Order("id").First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return n.ID, err
}

// GetNodeGroupIDForNode: the node's 节点分组 (0 = none).
func (r *Repository) GetNodeGroupIDForNode(nodeID int64) (int64, error) {
	var n model.Node
	if err := r.db.Select("id, group_id").First(&n, nodeID).Error; err != nil {
		return 0, err
	}
	if !n.GroupID.Valid {
		return 0, nil
	}
	return n.GroupID.Int64, nil
}

// ListLeiKwanDetectedNodeIDs: nodes whose agent reports a running LeiKwan client.
func (r *Repository) ListLeiKwanDetectedNodeIDs() ([]int64, error) {
	var ids []int64
	err := r.db.Model(&model.Node{}).Where("leikwan_status = ?", "running").Order("id").Pluck("id", &ids).Error
	return ids, err
}

// SetLeiKwanRuleAllEntries switches 「所有利群入口都可用」 (plan 272).
func (r *Repository) SetLeiKwanRuleAllEntries(id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res := r.db.Model(&model.LeiKwanRule{}).Where("id = ?", id).UpdateColumn("all_entries", v)
	if res.Error == nil && res.RowsAffected == 0 {
		return errors.New("利群转发不存在")
	}
	return res.Error
}

// EndLeiKwanGrantGrace ends the 换主入口 grace of a user whose subscription
// was pulled at pulledAt (after the change); true when one ended (plan 272).
func (r *Repository) EndLeiKwanGrantGrace(userID, pulledAt int64) (bool, error) {
	res := r.db.Model(&model.LeiKwanGrant{}).Where("user_id = ? AND prev_entry_node_id > 0 AND entry_changed_at < ?", userID, pulledAt).
		UpdateColumn("prev_entry_node_id", 0)
	return res.RowsAffected > 0, res.Error
}

// ExpireLeiKwanGrantGrace ends every grace that started before cutoff.
func (r *Repository) ExpireLeiKwanGrantGrace(cutoff int64) (int64, error) {
	res := r.db.Model(&model.LeiKwanGrant{}).Where("prev_entry_node_id > 0 AND entry_changed_at < ?", cutoff).UpdateColumn("prev_entry_node_id", 0)
	return res.RowsAffected, res.Error
}
