package repo

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxChainCopiesPerServer bounds chain nodes on one exit; each is one loopback
// listener plus one routing rule in the landing Core.
const MaxChainCopiesPerServer = 500

var (
	// ErrChainInFlight: the user already has a chain operation running.
	ErrChainInFlight = errors.New("正在创建「我自己的节点」规则，请稍候")
	// ErrChainIdempotent wraps a repeated submit; the existing chain is returned.
	ErrChainIdempotent = errors.New("重复提交")
)

// ChainExit is one exit node of a tunnel as seen by chain rules.
type ChainExit struct {
	NodeID         int64  `json:"nodeId"`
	Name           string `json:"name"`
	Online         bool   `json:"online"`
	LineMachine    bool   `json:"lineMachine"`
	LandingCapable bool   `json:"landingCapable"`
	LandingChain   int    `json:"landingChain"`
	IsRemote       bool   `json:"isRemote"`
}

// TunnelLineInfo describes a tunnel for 线路机 decisions: it is a 线路机 tunnel
// iff any entry node is a 线路机; chain rules run on its exit tier.
type TunnelLineInfo struct {
	TunnelID     int64
	Type         int
	Status       int
	LineMachine  bool
	EntryNodeIDs []int64
	Exits        []ChainExit
}

func (r *Repository) TunnelLineInfo(tunnelID int64) (*TunnelLineInfo, error) {
	infos, err := r.TunnelLineInfos([]int64{tunnelID})
	if err != nil {
		return nil, err
	}
	info, ok := infos[tunnelID]
	if !ok {
		return nil, errors.New("隧道不存在")
	}
	return info, nil
}

// TunnelLineInfos loads several tunnels with three queries (list views).
func (r *Repository) TunnelLineInfos(tunnelIDs []int64) (map[int64]*TunnelLineInfo, error) {
	out := map[int64]*TunnelLineInfo{}
	if len(tunnelIDs) == 0 {
		return out, nil
	}
	var tunnels []model.Tunnel
	if err := r.db.Select("id", "type", "status").Where("id IN ?", tunnelIDs).Find(&tunnels).Error; err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		out[t.ID] = &TunnelLineInfo{TunnelID: t.ID, Type: t.Type, Status: t.Status}
	}
	var rows []model.ChainTunnel
	if err := r.db.Where("tunnel_id IN ?", tunnelIDs).Order("tunnel_id ASC, chain_type ASC, inx ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, c := range rows {
		ids = append(ids, c.NodeID)
	}
	nodes := map[int64]model.Node{}
	if len(ids) > 0 {
		var list []model.Node
		if err := r.db.Select("id", "name", "status", "line_machine", "landing_capable", "landing_chain", "is_remote").Where("id IN ?", ids).Find(&list).Error; err != nil {
			return nil, err
		}
		for _, n := range list {
			nodes[n.ID] = n
		}
	}
	seenExit := map[[2]int64]bool{}
	for _, c := range rows {
		info := out[c.TunnelID]
		if info == nil {
			continue
		}
		n := nodes[c.NodeID]
		switch c.ChainType {
		case "1":
			info.EntryNodeIDs = append(info.EntryNodeIDs, c.NodeID)
			if n.LineMachine {
				info.LineMachine = true
			}
		case "3":
			key := [2]int64{c.TunnelID, c.NodeID}
			if seenExit[key] {
				continue
			}
			seenExit[key] = true
			info.Exits = append(info.Exits, ChainExit{NodeID: c.NodeID, Name: n.Name, Online: n.Status == 1, LineMachine: n.LineMachine, LandingCapable: n.LandingCapable, LandingChain: n.LandingChain, IsRemote: n.IsRemote != 0})
		}
	}
	return out, nil
}

// LineMachineTunnelIDs lists every 线路机 tunnel in one query (list views).
func (r *Repository) LineMachineTunnelIDs() (map[int64]bool, error) {
	var ids []int64
	err := r.db.Table("chain_tunnel AS ct").Joins("JOIN node AS n ON n.id = ct.node_id").
		Where("ct.chain_type = ? AND n.line_machine = ?", "1", true).Distinct().Pluck("ct.tunnel_id", &ids).Error
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

func (r *Repository) SetNodeLineMachine(id int64, v bool) error {
	return r.db.Model(&model.Node{}).Where("id = ?", id).Update("line_machine", v).Error
}

// NodeChainCopyCount counts chain nodes hosted on a FLVX node.
func (r *Repository) NodeChainCopyCount(nodeID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.ForwardChainNode{}).Where("node_id = ?", nodeID).Count(&n).Error
	return n, err
}

func (r *Repository) GetForwardChain(id int64) (*model.ForwardChain, error) {
	var c model.ForwardChain
	err := r.db.First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

func (r *Repository) GetForwardChainByForward(forwardID int64) (*model.ForwardChain, error) {
	if forwardID <= 0 {
		return nil, nil
	}
	var c model.ForwardChain
	err := r.db.Where("forward_id = ?", forwardID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// ForwardChainsByForwardIDs loads the chains of many rules in one query.
func (r *Repository) ForwardChainsByForwardIDs(ids []int64) (map[int64]model.ForwardChain, error) {
	out := map[int64]model.ForwardChain{}
	if len(ids) == 0 {
		return out, nil
	}
	var list []model.ForwardChain
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		var part []model.ForwardChain
		if err := r.db.Where("forward_id IN ?", ids[start:end]).Find(&part).Error; err != nil {
			return nil, err
		}
		list = append(list, part...)
	}
	for _, c := range list {
		out[c.ForwardID] = c
	}
	return out, nil
}

func (r *Repository) ListForwardChains(states ...string) ([]model.ForwardChain, error) {
	var out []model.ForwardChain
	q := r.db.Order("id ASC")
	if len(states) > 0 {
		q = q.Where("state IN ?", states)
	}
	err := q.Find(&out).Error
	return out, err
}

func (r *Repository) ListForwardChainNodes(chainID int64) ([]model.ForwardChainNode, error) {
	var out []model.ForwardChainNode
	err := r.db.Where("chain_id = ?", chainID).Order("id ASC").Find(&out).Error
	return out, err
}

// ChainCopy is a chain node to put into one landing server's desired state.
type ChainCopy struct {
	Node  model.ForwardChainNode
	Chain model.ForwardChain
}

// ChainCopiesForServer returns the chain nodes a server must run: copies of
// creating and active chains (pending copies of a running create or tunnel
// change included, so they are applied before the rule switches to them).
func (r *Repository) ChainCopiesForServer(serverID int64) ([]ChainCopy, error) {
	var nodes []model.ForwardChainNode
	if err := r.db.Where("landing_server_id = ?", serverID).Order("id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	ids := []int64{}
	for _, n := range nodes {
		ids = append(ids, n.ChainID)
	}
	var chains []model.ForwardChain
	if err := r.db.Where("id IN ? AND state IN ?", ids, []string{model.ChainStateCreating, model.ChainStateActive}).Find(&chains).Error; err != nil {
		return nil, err
	}
	byID := map[int64]model.ForwardChain{}
	for _, c := range chains {
		byID[c.ID] = c
	}
	out := []ChainCopy{}
	for _, n := range nodes {
		if c, ok := byID[n.ChainID]; ok && c.Upstream != "" {
			out = append(out, ChainCopy{Node: n, Chain: c})
		}
	}
	return out, nil
}

// ChainServerIDs lists landing servers hosting copies of the given chains.
func (r *Repository) ChainServerIDs(chainIDs []int64) ([]int64, error) {
	out := []int64{}
	if len(chainIDs) == 0 {
		return out, nil
	}
	err := r.db.Model(&model.ForwardChainNode{}).Where("chain_id IN ?", chainIDs).Distinct().Pluck("landing_server_id", &out).Error
	return out, err
}

// ChainServerIDsByForwards/User/Tunnel collect the servers to resync before a
// cascade deletes the chains of those rules.
func (r *Repository) ChainServerIDsByForwards(forwardIDs []int64) ([]int64, error) {
	out := []int64{}
	if len(forwardIDs) == 0 {
		return out, nil
	}
	err := r.db.Model(&model.ForwardChainNode{}).Where("chain_id IN (?)", r.db.Model(&model.ForwardChain{}).Select("id").Where("forward_id IN ?", forwardIDs)).Distinct().Pluck("landing_server_id", &out).Error
	return out, err
}

func (r *Repository) ChainServerIDsByUser(userID int64) ([]int64, error) {
	out := []int64{}
	err := r.db.Model(&model.ForwardChainNode{}).Where("chain_id IN (?)", r.db.Model(&model.ForwardChain{}).Select("id").Where("user_id = ?", userID)).Distinct().Pluck("landing_server_id", &out).Error
	return out, err
}

func (r *Repository) ChainServerIDsByTunnel(tunnelID int64) ([]int64, error) {
	out := []int64{}
	err := r.db.Model(&model.ForwardChainNode{}).Where("chain_id IN (?)", r.db.Model(&model.ForwardChain{}).Select("id").Where("tunnel_id = ? OR forward_id IN (?)", tunnelID, r.db.Model(&model.Forward{}).Select("id").Where("tunnel_id = ?", tunnelID))).Distinct().Pluck("landing_server_id", &out).Error
	return out, err
}

func bumpServers(tx *gorm.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return tx.Model(&model.LandingServer{}).Where("id IN ?", ids).UpdateColumn("revision", gorm.Expr("revision + 1")).Error
}

// deleteChainsTx removes chains selected by where (and every copy), bumping
// the revision of each server that hosted a copy. Used by every cascade.
func deleteChainsTx(tx *gorm.DB, where string, args ...interface{}) error {
	var ids []int64
	if err := tx.Model(&model.ForwardChain{}).Where(where, args...).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var servers []int64
	if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id IN ?", ids).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
		return err
	}
	if err := tx.Where("chain_id IN ?", ids).Delete(&model.ForwardChainNode{}).Error; err != nil {
		return err
	}
	if err := tx.Where("id IN ?", ids).Delete(&model.ForwardChain{}).Error; err != nil {
		return err
	}
	return bumpServers(tx, servers)
}

// ensureChainHostTx returns the exit node's landing server, creating a
// chain-only one when the node has none (not public, never shown to users).
func ensureChainHostTx(tx *gorm.DB, nodeID int64) (*model.LandingServer, error) {
	if nodeID == 36 {
		return nil, errors.New("节点 36 是妙妙屋生产主机，禁止承载落地")
	}
	var n model.Node
	if err := tx.First(&n, nodeID).Error; err != nil {
		return nil, errors.New("出口节点不存在")
	}
	if n.IsRemote != 0 {
		return nil, errors.New("出口节点「" + n.Name + "」是远程（联邦）节点，不能承载「我自己的节点」")
	}
	if !n.LandingCapable || n.LandingChain < 1 {
		return nil, errors.New("出口节点「" + n.Name + "」的节点程序版本过低，不支持「我自己的节点」，请先升级该节点")
	}
	var s model.LandingServer
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source = 'native' AND node_id = ?", nodeID).First(&s).Error
	if err == nil {
		if s.Status != "active" {
			return nil, errors.New("出口节点「" + n.Name + "」上的落地已停用，不能承载「我自己的节点」")
		}
		return &s, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	now := time.Now().UnixMilli()
	host := strings.TrimSpace(n.ServerIP)
	if n.ServerIPV4.Valid && strings.TrimSpace(n.ServerIPV4.String) != "" {
		host = strings.TrimSpace(n.ServerIPV4.String)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	s = model.LandingServer{Source: "native", NodeID: nodeID, Name: n.Name, PublicHost: host, PortMin: 30000, PortMax: 39999, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", Revision: 1, ChainOnly: true, CreatedTime: now, UpdatedTime: now}
	if err := tx.Create(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// usedPortsTx: every port a chain listener on this server must avoid,
// including the node's whole rule range (forwards may claim it later).
func usedPortsTx(tx *gorm.DB, s model.LandingServer, skipChain int64) (map[int]bool, error) {
	var node model.Node
	if err := tx.Select("id", "port").First(&node, s.NodeID).Error; err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, p := range parsePortRangeSpec(node.Port) {
		used[p] = true
	}
	queries := []struct {
		m      interface{}
		column string
		where  string
		args   []interface{}
	}{
		{&model.LandingNode{}, "port", "landing_server_id = ?", []interface{}{s.ID}},
		{&model.ForwardChainNode{}, "port", "landing_server_id = ? AND chain_id <> ?", []interface{}{s.ID, skipChain}},
		{&model.ForwardPort{}, "port", "node_id = ?", []interface{}{s.NodeID}},
		{&model.ChainTunnel{}, "port", "node_id = ? AND port IS NOT NULL", []interface{}{s.NodeID}},
		{&model.NodeDeployedInbound{}, "listen_port", "node_id = ?", []interface{}{s.NodeID}},
		{&model.NodeRuntimeResource{}, "port", "node_id = ? AND status <> 'released'", []interface{}{s.NodeID}},
	}
	for _, q := range queries {
		var ports []int
		if err := tx.Model(q.m).Where(q.where, q.args...).Pluck(q.column, &ports).Error; err != nil {
			return nil, err
		}
		for _, p := range ports {
			used[p] = true
		}
	}
	return used, nil
}

// chainFreePortTx picks one port free on every server. Public landing servers
// keep chain ports inside their planned landing range; chain-only servers
// only avoid everything in use (the listener is loopback-only).
func chainFreePortTx(tx *gorm.DB, servers []model.LandingServer, skipChain int64) (int, error) {
	used := make([]map[int]bool, len(servers))
	lo, hi := 1024, 65535
	public := false
	for i, s := range servers {
		u, err := usedPortsTx(tx, s, skipChain)
		if err != nil {
			return 0, err
		}
		used[i] = u
		if !s.ChainOnly && s.Source == "native" {
			public = true
			if s.PortMin > lo {
				lo = s.PortMin
			}
			if s.PortMax < hi {
				hi = s.PortMax
			}
		}
	}
	free := func(p int) bool {
		for _, u := range used {
			if u[p] {
				return false
			}
		}
		return true
	}
	if public {
		for p := lo; p <= hi; p++ {
			if free(p) {
				return p, nil
			}
		}
		return 0, errors.New("出口落地服务器的端口段内没有共同的空闲端口，请联系管理员调整落地端口段")
	}
	for _, w := range [][2]int{{30000, 39999}, {40000, 49999}, {20000, 29999}, {50000, 60999}, {10000, 19999}, {1024, 9999}} {
		for p := w[0]; p <= w[1]; p++ {
			if free(p) {
				return p, nil
			}
		}
	}
	return 0, errors.New("出口节点没有可用的空闲端口")
}

func newChainCredentials(c *model.ForwardChain) error {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	c.PrivateKey = base64.RawURLEncoding.EncodeToString(key.Bytes())
	c.PublicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	c.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	sid := make([]byte, 4)
	if _, err := rand.Read(sid); err != nil {
		return err
	}
	c.ShortID = hex.EncodeToString(sid)
	return nil
}

// ChainCreateInput is a validated chain request.
type ChainCreateInput struct {
	UserID      int64
	TunnelID    int64
	IdemKey     string
	Upstream    linechain.Upstream
	ExitNodeIDs []int64
	// InFlightAfter: a creating chain of this user newer than this (ms) blocks
	// a new one; older ones are left to the reconciler.
	InFlightAfter int64
	// AttachForwardID converts an existing rule into a chain rule.
	AttachForwardID int64
}

// CreateForwardChain persists a pending chain and one pending copy per exit,
// all on the same free port, in one transaction. A repeated idempotency key
// returns the existing chain with ErrChainIdempotent.
func (r *Repository) CreateForwardChain(in ChainCreateInput) (*model.ForwardChain, []model.ForwardChainNode, error) {
	if len(in.ExitNodeIDs) == 0 {
		return nil, nil, errors.New("该隧道没有出口节点")
	}
	var chain model.ForwardChain
	var copies []model.ForwardChainNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, in.UserID).Error; err != nil {
			return errors.New("用户不存在")
		}
		if in.IdemKey != "" {
			var existing model.ForwardChain
			if err := tx.Where("user_id = ? AND idem_key = ?", in.UserID, in.IdemKey).First(&existing).Error; err == nil {
				chain = existing
				return ErrChainIdempotent
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var count int64
		if err := tx.Model(&model.ForwardChain{}).Where("user_id = ? AND state = ? AND op_started_at > ?", in.UserID, model.ChainStateCreating, in.InFlightAfter).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrChainInFlight
		}
		hash := in.Upstream.Hash()
		var dup model.ForwardChain
		if err := tx.Where("user_id = ? AND tunnel_id = ? AND upstream_hash = ? AND state IN ?", in.UserID, in.TunnelID, hash, []string{model.ChainStateCreating, model.ChainStateActive}).First(&dup).Error; err == nil {
			name := ""
			if dup.ForwardID > 0 {
				var f model.Forward
				if tx.Select("name").First(&f, dup.ForwardID).Error == nil {
					name = f.Name
				}
			}
			if name == "" {
				return errors.New("这个节点已经在本隧道上建过「我自己的节点」规则（正在创建中），请勿重复创建")
			}
			return fmt.Errorf("这个节点已经在本隧道上建过「我自己的节点」规则「%s」，请直接复制那条规则的链接", name)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		servers := []model.LandingServer{}
		seen := map[int64]bool{}
		for _, nodeID := range in.ExitNodeIDs {
			if seen[nodeID] {
				continue
			}
			seen[nodeID] = true
			s, err := ensureChainHostTx(tx, nodeID)
			if err != nil {
				return err
			}
			var n int64
			if err := tx.Model(&model.ForwardChainNode{}).Where("landing_server_id = ?", s.ID).Count(&n).Error; err != nil {
				return err
			}
			if n >= MaxChainCopiesPerServer {
				return errors.New("出口节点上的「我自己的节点」规则已达上限，请联系管理员")
			}
			servers = append(servers, *s)
		}
		port, err := chainFreePortTx(tx, servers, 0)
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		dest, names := "www.lovelive-anime.jp:443", `["www.lovelive-anime.jp"]`
		for _, s := range servers {
			if !s.ChainOnly && s.RealityDest != "" {
				dest, names = s.RealityDest, s.RealityServerNames
				break
			}
		}
		op := "create"
		if in.AttachForwardID > 0 {
			op = "attach"
			var existing int64
			if err := tx.Model(&model.ForwardChain{}).Where("forward_id = ?", in.AttachForwardID).Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				return errors.New("该规则已经是「我自己的节点」规则")
			}
		}
		chain = model.ForwardChain{UserID: in.UserID, TunnelID: in.TunnelID, ForwardID: in.AttachForwardID, State: model.ChainStateCreating, Progress: "正在准备出口…", Port: port, Dest: dest, ServerNames: names,
			Upstream: in.Upstream.JSON(), UpstreamProtocol: in.Upstream.ProtocolLabel(), UpstreamHost: in.Upstream.Server, UpstreamPort: in.Upstream.Port, UpstreamHash: hash,
			IdemKey: in.IdemKey, Op: op, OpStartedAt: now, CreatedTime: now, UpdatedTime: now}
		if err := newChainCredentials(&chain); err != nil {
			return err
		}
		if err := tx.Create(&chain).Error; err != nil {
			return err
		}
		ids := []int64{}
		for _, s := range servers {
			c := model.ForwardChainNode{ChainID: chain.ID, LandingServerID: s.ID, NodeID: s.NodeID, Port: port, State: model.ChainNodePending, CreatedTime: now}
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
			copies = append(copies, c)
			ids = append(ids, s.ID)
		}
		return bumpServers(tx, ids)
	})
	if errors.Is(err, ErrChainIdempotent) {
		return &chain, nil, err
	}
	if err != nil {
		return nil, nil, err
	}
	return &chain, copies, nil
}

func (r *Repository) SetForwardChainProgress(id int64, progress string) error {
	return r.db.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"progress": progress, "updated_time": time.Now().UnixMilli()}).Error
}

// ActivateForwardChain links the created rule and marks every copy active.
func (r *Repository) ActivateForwardChain(id, forwardID int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Update("state", model.ChainNodeActive).Error; err != nil {
			return err
		}
		return tx.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"state": model.ChainStateActive, "forward_id": forwardID, "progress": "", "last_error": "", "op": "", "last_op_at": gorm.Expr("op_started_at"), "op_started_at": 0, "updated_time": time.Now().UnixMilli()}).Error
	})
}

// StartChainOp marks an operation on an idle chain (compare-and-set).
func (r *Repository) StartChainOp(id int64, op string) (int64, error) {
	now := time.Now().UnixMilli()
	res := r.db.Model(&model.ForwardChain{}).Where("id = ? AND op_started_at = 0 AND state = ?", id, model.ChainStateActive).Updates(map[string]interface{}{"op": op, "op_started_at": now, "progress": "", "last_error": "", "updated_time": now})
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, ErrChainInFlight
	}
	return now, nil
}

// FinishChainOp ends the running operation of an active chain.
func (r *Repository) FinishChainOp(id int64, lastError string) error {
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	return r.db.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"op": "", "last_op_at": gorm.Expr("op_started_at"), "op_started_at": 0, "progress": "", "last_error": lastError, "updated_time": time.Now().UnixMilli()}).Error
}

// FailForwardChain rolls a create back: copies are removed (their servers are
// returned for a resync), secrets are erased, and the row stays briefly as a
// failed result for the idempotency key.
func (r *Repository) FailForwardChain(id int64, reason string) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		if err := tx.Where("chain_id = ?", id).Delete(&model.ForwardChainNode{}).Error; err != nil {
			return err
		}
		if len(reason) > 500 {
			reason = reason[:500]
		}
		if err := tx.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"state": model.ChainStateFailed, "forward_id": 0, "progress": "", "last_error": reason, "upstream": "", "prev_upstream": "", "private_key": "", "uuid": "", "op": "", "last_op_at": gorm.Expr("op_started_at"), "op_started_at": 0, "updated_time": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}

// DeleteForwardChain removes a chain and every copy.
func (r *Repository) DeleteForwardChain(id int64) ([]int64, error) {
	servers, err := r.ChainServerIDs([]int64{id})
	if err != nil {
		return nil, err
	}
	err = r.db.Transaction(func(tx *gorm.DB) error { return deleteChainsTx(tx, "id = ?", id) })
	return servers, err
}

// DeleteForwardChainsForForward removes the chain of a rule (if any).
func (r *Repository) DeleteForwardChainsForForward(forwardID int64) ([]int64, error) {
	servers, err := r.ChainServerIDsByForwards([]int64{forwardID})
	if err != nil {
		return nil, err
	}
	err = r.db.Transaction(func(tx *gorm.DB) error { return deleteChainsTx(tx, "forward_id = ?", forwardID) })
	return servers, err
}

// BeginChainUpstreamChange swaps in a new upstream and keeps the old one as
// the rollback point until Commit/Revert. The user's link does not change.
func (r *Repository) BeginChainUpstreamChange(id int64, u linechain.Upstream) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var c model.ForwardChain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, id).Error; err != nil {
			return err
		}
		if c.State != model.ChainStateActive {
			return errors.New("链式规则正在处理中，请稍后再试")
		}
		if c.PrevUpstream != "" {
			return ErrChainInFlight
		}
		var dup int64
		if err := tx.Model(&model.ForwardChain{}).Where("id <> ? AND user_id = ? AND tunnel_id = ? AND upstream_hash = ? AND state IN ?", id, c.UserID, c.TunnelID, u.Hash(), []string{model.ChainStateCreating, model.ChainStateActive}).Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 {
			return errors.New("这个节点已经在本隧道上建过另一条「我自己的节点」规则")
		}
		now := time.Now().UnixMilli()
		if err := tx.Model(&c).Updates(map[string]interface{}{"prev_upstream": c.Upstream, "upstream": u.JSON(), "upstream_protocol": u.ProtocolLabel(), "upstream_host": u.Server, "upstream_port": u.Port, "upstream_hash": u.Hash(), "updated_time": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}

func (r *Repository) CommitChainUpstreamChange(id int64) error {
	return r.db.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"prev_upstream": "", "updated_time": time.Now().UnixMilli()}).Error
}

// RevertChainUpstreamChange restores the previous upstream.
func (r *Repository) RevertChainUpstreamChange(id int64) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var c model.ForwardChain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, id).Error; err != nil {
			return err
		}
		if c.PrevUpstream == "" {
			return nil
		}
		prev, err := linechain.FromJSON(c.PrevUpstream)
		if err != nil {
			return err
		}
		if err := tx.Model(&c).Updates(map[string]interface{}{"upstream": c.PrevUpstream, "prev_upstream": "", "upstream_protocol": prev.ProtocolLabel(), "upstream_host": prev.Server, "upstream_port": prev.Port, "upstream_hash": prev.Hash(), "updated_time": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}

// ChainRetarget is a prepared move of a chain onto a (new) set of exits.
type ChainRetarget struct {
	ChainID          int64
	OldPort, NewPort int
	AddedServerIDs   []int64
	RemovedServerIDs []int64
}

// PrepareChainRetarget adds pending copies on the given exits. The current
// port is kept when it is free on every new exit; otherwise all exits move to
// a new common port (the rule's target changes at commit). Old copies keep
// serving until CommitChainRetarget.
func (r *Repository) PrepareChainRetarget(chainID int64, exitNodeIDs []int64) (*ChainRetarget, error) {
	if len(exitNodeIDs) == 0 {
		return nil, errors.New("该隧道没有出口节点")
	}
	plan := &ChainRetarget{ChainID: chainID}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var c model.ForwardChain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, chainID).Error; err != nil {
			return err
		}
		if c.State != model.ChainStateActive {
			return errors.New("链式规则正在处理中，请稍后再试")
		}
		if err := tx.Where("chain_id = ? AND state = ?", chainID, model.ChainNodePending).Delete(&model.ForwardChainNode{}).Error; err != nil {
			return err
		}
		var active []model.ForwardChainNode
		if err := tx.Where("chain_id = ?", chainID).Find(&active).Error; err != nil {
			return err
		}
		plan.OldPort, plan.NewPort = c.Port, c.Port
		servers := []model.LandingServer{}
		seen := map[int64]bool{}
		for _, nodeID := range exitNodeIDs {
			if seen[nodeID] {
				continue
			}
			seen[nodeID] = true
			s, err := ensureChainHostTx(tx, nodeID)
			if err != nil {
				return err
			}
			servers = append(servers, *s)
		}
		hosted := map[int64]bool{}
		for _, a := range active {
			if a.Port == c.Port {
				hosted[a.LandingServerID] = true
			}
		}
		keep := true
		for _, s := range servers {
			if hosted[s.ID] {
				continue
			}
			used, err := usedPortsTx(tx, s, chainID)
			if err != nil {
				return err
			}
			if used[c.Port] || (!s.ChainOnly && (c.Port < s.PortMin || c.Port > s.PortMax)) {
				keep = false
				break
			}
		}
		if !keep {
			p, err := chainFreePortTx(tx, servers, chainID)
			if err != nil {
				return err
			}
			plan.NewPort = p
		}
		now := time.Now().UnixMilli()
		wanted := map[int64]bool{}
		for _, s := range servers {
			wanted[s.ID] = true
			if plan.NewPort == c.Port && hosted[s.ID] {
				continue
			}
			n := model.ForwardChainNode{ChainID: chainID, LandingServerID: s.ID, NodeID: s.NodeID, Port: plan.NewPort, State: model.ChainNodePending, CreatedTime: now}
			if err := tx.Create(&n).Error; err != nil {
				return err
			}
			plan.AddedServerIDs = append(plan.AddedServerIDs, s.ID)
		}
		for _, a := range active {
			if !wanted[a.LandingServerID] || a.Port != plan.NewPort {
				plan.RemovedServerIDs = append(plan.RemovedServerIDs, a.LandingServerID)
			}
		}
		if err := tx.Model(&c).Update("updated_time", now).Error; err != nil {
			return err
		}
		return bumpServers(tx, plan.AddedServerIDs)
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// CommitChainRetarget switches the chain to its pending copies.
func (r *Repository) CommitChainRetarget(plan *ChainRetarget, tunnelID int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chain_id = ? AND state = ? AND (port <> ? OR landing_server_id IN ?)", plan.ChainID, model.ChainNodeActive, plan.NewPort, append([]int64{0}, plan.RemovedServerIDs...)).Delete(&model.ForwardChainNode{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ? AND state = ?", plan.ChainID, model.ChainNodePending).Update("state", model.ChainNodeActive).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ForwardChain{}).Where("id = ?", plan.ChainID).Updates(map[string]interface{}{"tunnel_id": tunnelID, "port": plan.NewPort, "updated_time": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		return bumpServers(tx, append(append([]int64{}, plan.RemovedServerIDs...), plan.AddedServerIDs...))
	})
}

// AbortChainRetarget drops the pending copies; the old ones keep serving.
func (r *Repository) AbortChainRetarget(chainID int64) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ? AND state = ?", chainID, model.ChainNodePending).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		if err := tx.Where("chain_id = ? AND state = ?", chainID, model.ChainNodePending).Delete(&model.ForwardChainNode{}).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}

func (r *Repository) SetForwardChainError(id int64, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return r.db.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"last_error": reason, "updated_time": time.Now().UnixMilli()}).Error
}

// ChainOrphan is a chain row or copy without a valid owner.
type ChainOrphan struct {
	Kind     string `json:"kind"` // chain | chainNode
	ID       int64  `json:"id"`
	ChainID  int64  `json:"chainId,omitempty"`
	NodeID   int64  `json:"nodeId,omitempty"`
	ServerID int64  `json:"serverId,omitempty"`
	Reason   string `json:"reason"`
	Deleted  bool   `json:"deleted,omitempty"`
	Error    string `json:"error,omitempty"`
}

// FindChainOrphans: copies whose chain is gone or failed, active chains whose
// rule is gone or no longer targets the chain port, and copies on servers
// that no longer exist. Creating chains are left to the reconciler.
func (r *Repository) FindChainOrphans() ([]ChainOrphan, error) {
	out := []ChainOrphan{}
	var chains []model.ForwardChain
	if err := r.db.Find(&chains).Error; err != nil {
		return nil, err
	}
	byID := map[int64]model.ForwardChain{}
	forwardIDs := []int64{}
	for _, c := range chains {
		byID[c.ID] = c
		if c.ForwardID > 0 {
			forwardIDs = append(forwardIDs, c.ForwardID)
		}
	}
	forwards := map[int64]model.Forward{}
	if len(forwardIDs) > 0 {
		var list []model.Forward
		if err := r.db.Select("id", "remote_addr", "tunnel_id").Where("id IN ?", forwardIDs).Find(&list).Error; err != nil {
			return nil, err
		}
		for _, f := range list {
			forwards[f.ID] = f
		}
	}
	for _, c := range chains {
		if c.State != model.ChainStateActive {
			continue
		}
		f, ok := forwards[c.ForwardID]
		switch {
		case !ok:
			out = append(out, ChainOrphan{Kind: "chain", ID: c.ID, Reason: "规则已删除"})
		case !strings.Contains(f.RemoteAddr, fmt.Sprintf("127.0.0.1:%d", c.Port)) && c.OpStartedAt == 0:
			out = append(out, ChainOrphan{Kind: "chain", ID: c.ID, Reason: "规则目标已不是该链式节点"})
		}
	}
	var nodes []model.ForwardChainNode
	if err := r.db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	var serverIDs []int64
	if err := r.db.Model(&model.LandingServer{}).Pluck("id", &serverIDs).Error; err != nil {
		return nil, err
	}
	servers := map[int64]bool{}
	for _, id := range serverIDs {
		servers[id] = true
	}
	for _, n := range nodes {
		c, ok := byID[n.ChainID]
		switch {
		case !ok:
			out = append(out, ChainOrphan{Kind: "chainNode", ID: n.ID, ChainID: n.ChainID, NodeID: n.NodeID, ServerID: n.LandingServerID, Reason: "链式规则已不存在"})
		case c.State == model.ChainStateFailed:
			out = append(out, ChainOrphan{Kind: "chainNode", ID: n.ID, ChainID: n.ChainID, NodeID: n.NodeID, ServerID: n.LandingServerID, Reason: "链式规则创建失败"})
		case !servers[n.LandingServerID]:
			out = append(out, ChainOrphan{Kind: "chainNode", ID: n.ID, ChainID: n.ChainID, NodeID: n.NodeID, ServerID: n.LandingServerID, Reason: "落地服务器已不存在"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DeleteChainOrphan removes one orphan after re-checking it is still one.
func (r *Repository) DeleteChainOrphan(o ChainOrphan) ([]int64, error) {
	current, err := r.FindChainOrphans()
	if err != nil {
		return nil, err
	}
	still := false
	for _, c := range current {
		if c.Kind == o.Kind && c.ID == o.ID {
			still = true
		}
	}
	if !still {
		return nil, errors.New("ownership changed; retained")
	}
	if o.Kind == "chain" {
		return r.DeleteForwardChain(o.ID)
	}
	var n model.ForwardChainNode
	if err := r.db.First(&n, o.ID).Error; err != nil {
		return nil, err
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&n).Error; err != nil {
			return err
		}
		return bumpServers(tx, []int64{n.LandingServerID})
	})
	return []int64{n.LandingServerID}, err
}

// PurgeFailedForwardChains drops failed results older than the cutoff.
func (r *Repository) PurgeFailedForwardChains(before int64) (int64, error) {
	res := r.db.Where("state = ? AND updated_time < ?", model.ChainStateFailed, before).Delete(&model.ForwardChain{})
	return res.RowsAffected, res.Error
}

// ChainServerNames decodes stored Reality server names.
func ChainServerNames(c model.ForwardChain) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(c.ServerNames), &out)
	return out
}

// ChainCopyCounts maps landing server id -> number of chain copies.
func (r *Repository) ChainCopyCounts() (map[int64]int, error) {
	type row struct {
		LandingServerID int64
		N               int
	}
	var rows []row
	err := r.db.Model(&model.ForwardChainNode{}).Select("landing_server_id, COUNT(*) AS n").Group("landing_server_id").Scan(&rows).Error
	out := map[int64]int{}
	for _, x := range rows {
		out[x.LandingServerID] = x.N
	}
	return out, err
}

// FindForwardChainByIdem returns the chain created by a submit key.
func (r *Repository) FindForwardChainByIdem(userID int64, key string) (*model.ForwardChain, error) {
	if key == "" {
		return nil, nil
	}
	var c model.ForwardChain
	err := r.db.Where("user_id = ? AND idem_key = ?", userID, key).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}

// FindChainTargetForward finds a rule already pointing at a chain's loopback
// target (crash recovery: the rule was created but the chain not activated).
func (r *Repository) FindChainTargetForward(userID, tunnelID int64, target string) (int64, error) {
	var ids []int64
	err := r.db.Model(&model.Forward{}).Where("user_id = ? AND tunnel_id = ? AND remote_addr = ?", userID, tunnelID, target).Order("id DESC").Limit(1).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// SetForwardRemoteAddr points a chain rule at its (moved) loopback target.
func (r *Repository) SetForwardRemoteAddr(id int64, addr string) error {
	return r.db.Model(&model.Forward{}).Where("id = ?", id).Updates(map[string]interface{}{"remote_addr": addr, "updated_time": time.Now().UnixMilli()}).Error
}

// CountUserForwards counts a user's rules (chain rules included).
func (r *Repository) CountUserForwards(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.Forward{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// CountCreatingChains counts a user's chain creates that have no rule yet.
func (r *Repository) CountCreatingChains(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.ForwardChain{}).Where("user_id = ? AND state = ? AND op = ?", userID, model.ChainStateCreating, "create").Count(&n).Error
	return n, err
}

// ForwardEntryAddress is one entry address of a rule (the 转发版 address).
type ForwardEntryAddress struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	NodeID   int64  `json:"nodeId"`
	NodeName string `json:"nodeName"`
}

// ForwardEntryAddresses lists a rule's entry addresses, one per entry node,
// with the same tunnel/port overrides as the landing 转发版 links.
func (r *Repository) ForwardEntryAddresses(forwardID int64) ([]ForwardEntryAddress, error) {
	var f model.Forward
	if err := r.db.First(&f, forwardID).Error; err != nil {
		return nil, err
	}
	entries, err := r.forwardEntries(f)
	if err != nil {
		return nil, err
	}
	out := make([]ForwardEntryAddress, 0, len(entries))
	for _, e := range entries {
		out = append(out, ForwardEntryAddress{Host: e.Host, Port: e.Port, NodeID: e.NodeID, NodeName: e.NodeName})
	}
	return out, nil
}

// ChainLinkURL is the user's VLESS Reality Vision link through one entry.
func ChainLinkURL(c model.ForwardChain, host string, port int, name string) string {
	n := model.LandingNode{Protocol: "vless_reality_vision", UUID: c.UUID, PublicKey: c.PublicKey, ShortID: c.ShortID, ServerNames: c.ServerNames, Name: name}
	return LandingURL(n, host, port)
}

// LandingNodeProtocols maps landing node ids to protocols in one query.
func (r *Repository) LandingNodeProtocols(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.LandingNode
	if err := r.db.Select("id", "protocol").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, n := range rows {
		out[n.ID] = n.Protocol
	}
	return out, nil
}

// ChainCopyCountForServer counts chain nodes on one landing server.
func (r *Repository) ChainCopyCountForServer(serverID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.ForwardChainNode{}).Where("landing_server_id = ?", serverID).Count(&n).Error
	return n, err
}
