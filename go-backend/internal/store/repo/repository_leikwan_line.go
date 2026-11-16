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
			return errors.New("专线上还有利群规则，不能删除")
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
			return errors.New("该用户还有利群规则，请先删除规则再取消授权")
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

func (r *Repository) CountLeiKwanRules(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.LeiKwanRule{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// CountLeiKwanRulesByBackup: rules that use a backup forward (shared backups,
// e.g. one forward for 280 and 282, are only deleted with the last rule).
func (r *Repository) CountLeiKwanRulesByBackup(forwardID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.LeiKwanRule{}).Where("backup_forward_id = ?", forwardID).Count(&n).Error
	return n, err
}

func (r *Repository) SaveLeiKwanRule(x *model.LeiKwanRule) error {
	now := time.Now().UnixMilli()
	if x.ID == 0 {
		x.CreatedTime = now
	}
	x.UpdatedTime = now
	return r.db.Save(x).Error
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

// LeiKwanTunnelBrief: the port-forward tunnels offered as 备线隧道.
type LeiKwanTunnelBrief struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	EntryNames string `json:"entryNames"`
}

func (r *Repository) ListLeiKwanBackupTunnels() ([]LeiKwanTunnelBrief, error) {
	var tunnels []model.Tunnel
	if err := r.db.Select("id, name, type").Where("status = 1").Order("inx, id").Find(&tunnels).Error; err != nil {
		return nil, err
	}
	var chains []model.ChainTunnel
	if err := r.db.Select("tunnel_id, node_id").Where("chain_type = ?", "1").Find(&chains).Error; err != nil {
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
	entries := map[int64][]string{}
	for _, c := range chains {
		entries[c.TunnelID] = append(entries[c.TunnelID], nodeName[c.NodeID])
	}
	out := make([]LeiKwanTunnelBrief, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, LeiKwanTunnelBrief{ID: t.ID, Name: t.Name, Type: t.Type, EntryNames: strings.Join(entries[t.ID], "、")})
	}
	return out, nil
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
