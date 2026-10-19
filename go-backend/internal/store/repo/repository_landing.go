package repo

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) GetLandingServer(id int64) (*model.LandingServer, error) {
	var v model.LandingServer
	err := r.db.First(&v, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}
func (r *Repository) GetLandingServerForNode(id int64) (*model.LandingServer, error) {
	var v model.LandingServer
	err := r.db.Where("source = 'native' AND node_id = ?", id).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}
func (r *Repository) GetLandingNode(id int64) (*model.LandingNode, error) {
	var v model.LandingNode
	err := r.db.First(&v, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}
func landingPermission(db *gorm.DB, userID, serverID int64) (bool, error) {
	var count int64
	err := db.Table("landing_server_user_group AS lg").Joins("JOIN user_group_user AS gu ON gu.user_group_id = lg.user_group_id").Joins("JOIN user_group AS ug ON ug.id = lg.user_group_id AND ug.status = 1").Where("lg.landing_server_id = ? AND gu.user_id = ?", serverID, userID).Count(&count).Error
	return count > 0, err
}
func (r *Repository) UserCanUseLandingServer(userID, serverID int64) (bool, error) {
	return landingPermission(r.db, userID, serverID)
}
func (r *Repository) ListLandingServers(userID int64, admin bool) ([]model.LandingServer, error) {
	out := []model.LandingServer{}
	q := r.db.Model(&model.LandingServer{})
	if !admin {
		q = q.Where("status = ? AND id IN (?)", "active", r.db.Table("landing_server_user_group AS lg").Select("lg.landing_server_id").Joins("JOIN user_group_user AS gu ON gu.user_group_id = lg.user_group_id").Joins("JOIN user_group AS ug ON ug.id = lg.user_group_id AND ug.status = 1").Where("gu.user_id = ?", userID))
	}
	err := q.Order("id ASC").Find(&out).Error
	return out, err
}
func (r *Repository) ListLandingNodes(userID int64, admin bool) ([]model.LandingNode, error) {
	out := []model.LandingNode{}
	q := r.db
	if !admin {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Order("id ASC").Find(&out).Error
	return out, err
}
func (r *Repository) LandingServerGroupIDs(id int64) ([]int64, error) {
	out := []int64{}
	err := r.db.Model(&model.LandingServerUserGroup{}).Where("landing_server_id = ?", id).Order("user_group_id").Pluck("user_group_id", &out).Error
	return out, err
}

func (r *Repository) EnableLandingServer(nodeID int64) (*model.LandingServer, error) {
	// This production host must never run a FLVX landing core, regardless of privileges.
	if nodeID == 36 {
		return nil, errors.New("节点 36 是妙妙屋生产主机，禁止启用落地")
	}
	var s model.LandingServer
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var n model.Node
		if err := tx.First(&n, nodeID).Error; err != nil {
			return errors.New("节点不存在")
		}
		if !n.LandingCapable {
			return errors.New("节点程序需升级")
		}
		if n.IsRemote != 0 {
			return errors.New("仅支持本地 FLVX 节点")
		}
		var existing model.LandingServer
		if err := tx.Where("node_id = ?", nodeID).First(&existing).Error; err == nil {
			s = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UnixMilli()
		host := strings.TrimSpace(n.ServerIP)
		if n.ServerIPV4.Valid && n.ServerIPV4.String != "" {
			host = n.ServerIPV4.String
		}
		s = model.LandingServer{Source: "native", NodeID: nodeID, Name: n.Name, PublicHost: host, PortMin: 30000, PortMax: 39999, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", Revision: 1, CreatedTime: now, UpdatedTime: now}
		return tx.Create(&s).Error
	})
	return &s, err
}
func ValidateLandingServer(s *model.LandingServer) error {
	if s.NodeID == 36 {
		return errors.New("节点 36 是妙妙屋生产主机，禁止启用落地")
	}
	if (s.Source != "native" || s.NodeID <= 0) && (s.Source != "shared" || s.NodeID != 0 || s.RemoteServerID <= 0) {
		return errors.New("落地服务器来源无效")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 100 || strings.TrimSpace(s.PublicHost) == "" || strings.ContainsAny(s.PublicHost, " /?#@\r\n\t") {
		return errors.New("落地名称或公网地址无效")
	}
	if strings.Contains(s.PublicHost, ":") && net.ParseIP(strings.Trim(s.PublicHost, "[]")) == nil {
		return errors.New("公网地址只填写域名或 IP，不包含端口")
	}
	if s.PortMin < 1024 || s.PortMax > 65535 || s.PortMax < s.PortMin {
		return errors.New("落地端口段必须在 1024–65535 内")
	}
	if (s.FlowType != 1 && s.FlowType != 2) || math.IsNaN(s.TrafficRatio) || math.IsInf(s.TrafficRatio, 0) || s.TrafficRatio <= 0 || s.TrafficRatio > 1000 {
		return errors.New("流量计算或倍率无效")
	}
	if s.MaxConnections < 0 || s.DefaultMaxConnections < 0 || s.DefaultMaxOnlineIPs < 0 || s.DefaultTrafficLimitBytes < 0 || s.DefaultUserQuotaBytes < 0 || s.MaxNodesPerUser < 1 || s.CoreMemoryMB < 64 || s.CoreCPUPercent < 1 {
		return errors.New("落地限制无效")
	}
	if s.Status != "active" && s.Status != "disabled" {
		return errors.New("落地状态无效")
	}
	switch s.DefaultExpiryMode {
	case "follow_user", "permanent":
	case "days":
		if s.DefaultExpiryDays < 1 {
			return errors.New("有效期天数无效")
		}
	case "fixed":
		if s.DefaultExpiresAt == nil || *s.DefaultExpiresAt <= 0 {
			return errors.New("有效期无效")
		}
	default:
		return errors.New("有效期模式无效")
	}
	var protocols []string
	if json.Unmarshal([]byte(s.Protocols), &protocols) != nil || len(protocols) == 0 {
		return errors.New("请选择协议模板")
	}
	for _, p := range protocols {
		if p != "ss2022" && p != "vless_reality_vision" {
			return errors.New("协议模板无效")
		}
	}
	var names []string
	if json.Unmarshal([]byte(s.RealityServerNames), &names) != nil || len(names) == 0 {
		return errors.New("Reality serverNames 无效")
	}
	for _, n := range names {
		if strings.TrimSpace(n) == "" || strings.ContainsAny(n, " /:\r\n") {
			return errors.New("Reality serverNames 无效")
		}
	}
	if _, _, err := net.SplitHostPort(s.RealityDest); err != nil {
		return errors.New("Reality dest 必须是 host:port")
	}
	return ValidateSharedLandingSettings(s)
}
func (r *Repository) SaveLandingServer(s *model.LandingServer, groups []int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var old model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, s.ID).Error; err != nil {
			return err
		}
		if old.NodeID != s.NodeID || old.Source != s.Source {
			return errors.New("不能更换落地服务器来源")
		}
		s.RemoteServerID = old.RemoteServerID
		s.SharedCaps, s.SharedLastSyncAt, s.SharedSyncError = old.SharedCaps, old.SharedLastSyncAt, old.SharedSyncError
		s.SharedDesiredHash, s.SharedPushedRevision, s.SharedAppliedRevision = old.SharedDesiredHash, old.SharedPushedRevision, old.SharedAppliedRevision
		s.SharedLastPushAt, s.SharedCollectedAt = old.SharedLastPushAt, old.SharedCollectedAt
		// Egress assignment and reported state change only through their own
		// validated paths (SetLandingServerEgress / RecordLandingEgressStatus).
		s.RouteProfileID, s.EgressHash, s.EgressRestartOK = old.RouteProfileID, old.EgressHash, old.EgressRestartOK
		s.EgressState, s.EgressError, s.EgressReady, s.EgressCheckedAt, s.EgressWarnings = old.EgressState, old.EgressError, old.EgressReady, old.EgressCheckedAt, old.EgressWarnings
		if err := ValidateLandingServer(s); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.LandingNode{}).Where("landing_server_id = ? AND (port < ? OR port > ?)", s.ID, s.PortMin, s.PortMax).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("新端口段不能排除已有落地节点")
		}
		for _, id := range groups {
			if err := tx.Model(&model.UserGroup{}).Where("id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return errors.New("用户组不存在")
			}
		}
		s.CreatedTime = old.CreatedTime
		s.UpdatedTime = time.Now().UnixMilli()
		s.Revision = old.Revision + 1
		if err := tx.Save(s).Error; err != nil {
			return err
		}
		if err := tx.Where("landing_server_id = ?", s.ID).Delete(&model.LandingServerUserGroup{}).Error; err != nil {
			return err
		}
		for _, id := range groups {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.LandingServerUserGroup{LandingServerID: s.ID, UserGroupID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *Repository) GetLandingUserSetting(userID int64) (model.LandingUserSetting, []model.UserServerQuota, error) {
	s := model.LandingUserSetting{UserID: userID, MaxNodes: 5}
	err := r.db.First(&s, "user_id = ?", userID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return s, nil, err
	}
	qs := []model.UserServerQuota{}
	err = r.db.Where("user_id = ?", userID).Order("server_kind, server_ref_id").Find(&qs).Error
	return s, qs, err
}
func (r *Repository) SaveLandingUserSetting(userID int64, maxNodes int, quotas []model.UserServerQuota, extendFixedByMs int64) error {
	if maxNodes < 0 || maxNodes > 10000 || extendFixedByMs < 0 {
		return errors.New("落地设置无效")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.First(&user, userID).Error; err != nil {
			return errors.New("用户不存在")
		}
		// Flow ingestion locks landing nodes before per-server quota rows. Keep the
		// same order when settings and fixed-expiry renewal share one transaction.
		var lockedNodes []model.LandingNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Order("id ASC").Find(&lockedNodes).Error; err != nil {
			return err
		}
		setting := model.LandingUserSetting{UserID: userID, MaxNodes: maxNodes}
		if err := tx.Save(&setting).Error; err != nil {
			return err
		}
		if err := tx.Model(&setting).Update("max_nodes", maxNodes).Error; err != nil {
			return err
		}
		for _, q := range quotas {
			if (q.ServerKind != "node" && q.ServerKind != "landing") || q.ServerRefID <= 0 || q.LimitBytes < 0 {
				return errors.New("服务器流量限额无效")
			}
			if q.ServerKind == "node" {
				var node model.Node
				if err := tx.First(&node, q.ServerRefID).Error; err != nil {
					return errors.New("服务器不存在")
				}
			} else {
				var server model.LandingServer
				if err := tx.First(&server, q.ServerRefID).Error; err != nil || server.Source != "shared" {
					return errors.New("共享服务器不存在")
				}
				if capExceeded(q.LimitBytes, SharedLandingCaps(&server).TrafficLimit) {
					return errors.New("服务器流量限额不能超过妙妙屋上限")
				}
			}
			now := time.Now().UnixMilli()
			q.ID = 0
			q.UserID = userID
			q.LimitOverride = true
			q.UsedIn, q.UsedOut, q.PeriodKey, q.Paused = 0, 0, 0, false
			q.CreatedTime = now
			q.UpdatedTime = now
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "server_kind"}, {Name: "server_ref_id"}}, DoUpdates: clause.Assignments(map[string]interface{}{"limit_bytes": q.LimitBytes, "limit_override": true, "updated_time": now})}).Create(&q).Error; err != nil {
				return err
			}
		}
		if extendFixedByMs > 0 {
			var serverIDs []int64
			if err := tx.Model(&model.LandingNode{}).Where("user_id = ? AND expiry_mode = ? AND expires_at > 0", userID, "fixed").Distinct().Pluck("landing_server_id", &serverIDs).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.LandingNode{}).Where("user_id = ? AND expiry_mode = ? AND expires_at > 0", userID, "fixed").Updates(map[string]interface{}{"expires_at": gorm.Expr("expires_at + ?", extendFixedByMs), "updated_time": time.Now().UnixMilli()}).Error; err != nil {
				return err
			}
			if len(serverIDs) > 0 {
				return tx.Model(&model.LandingServer{}).Where("id IN ?", serverIDs).Update("revision", gorm.Expr("revision + 1")).Error
			}
		}
		return nil
	})
}
func (r *Repository) SetNodeLandingCapability(id int64, capable bool) error {
	return r.db.Model(&model.Node{}).Where("id = ? AND landing_capable <> ?", id, capable).Update("landing_capable", capable).Error
}
func (r *Repository) SetNodeDefaultUserQuota(id, limit int64) error {
	if limit < 0 {
		return errors.New("每个用户默认限额不能为负数")
	}
	return r.db.Model(&model.Node{}).Where("id = ?", id).Update("default_user_quota_bytes", limit).Error
}

func newLandingCredentials(n *model.LandingNode) error {
	if n.Protocol == "ss2022" {
		a, b := make([]byte, 16), make([]byte, 16)
		if _, err := rand.Read(a); err != nil {
			return err
		}
		if _, err := rand.Read(b); err != nil {
			return err
		}
		n.Method = "2022-blake3-aes-128-gcm"
		n.ServerKey = base64.StdEncoding.EncodeToString(a)
		n.UserKey = base64.StdEncoding.EncodeToString(b)
		return nil
	}
	if n.Protocol != "vless_reality_vision" {
		return errors.New("协议模板无效")
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	n.PrivateKey = base64.RawURLEncoding.EncodeToString(key.Bytes())
	n.PublicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	n.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	n.Flow = "xtls-rprx-vision"
	return nil
}
func (r *Repository) CreateLandingNode(userID, serverID int64, name, protocol string, admin bool) (*model.LandingNode, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, errors.New("请填写节点名称（最多 100 字节）")
	}
	var n model.LandingNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Lock the owner and server to serialize both total and per-server node limits.
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, userID).Error; err != nil {
			return errors.New("用户不存在")
		}
		now := time.Now().UnixMilli()
		if u.Status != 1 || (u.ExpTime > 0 && u.ExpTime <= now) {
			return errors.New("用户已停用或到期")
		}
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return errors.New("落地服务器不存在")
		}
		if s.NodeID == 36 {
			return errors.New("节点 36 禁止启用落地")
		}
		if s.Source == "native" {
			var native model.Node
			if err := tx.First(&native, s.NodeID).Error; err != nil {
				return errors.New("落地服务器节点不存在")
			}
			if !native.LandingCapable {
				return errors.New("节点程序需升级")
			}
		} else if !SharedLandingAvailable(&s) {
			return errors.New("妙妙屋共享已暂停、撤销或额度用完")
		}

		if s.Status != "active" || (s.AvailableUntil != nil && *s.AvailableUntil <= now) {
			return errors.New("落地服务器不可用")
		}
		if !admin {
			ok, err := landingPermission(tx, userID, serverID)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("无权使用该落地服务器")
			}
		}
		var protocols []string
		_ = json.Unmarshal([]byte(s.Protocols), &protocols)
		allowed := false
		for _, p := range protocols {
			if p == protocol {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("该服务器未启用此协议")
		}
		setting := model.LandingUserSetting{MaxNodes: 5}
		err := tx.First(&setting, "user_id = ?", userID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&model.LandingNode{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(setting.MaxNodes) {
			return errors.New("已达到用户落地节点上限")
		}
		if err := tx.Model(&model.LandingNode{}).Where("user_id = ? AND landing_server_id = ?", userID, serverID).Count(&count).Error; err != nil {
			return err
		}
		if s.Source == "shared" {
			var total int64
			if err := tx.Model(&model.LandingNode{}).Where("landing_server_id = ?", serverID).Count(&total).Error; err != nil {
				return err
			}
			if total >= 1000 {
				return errors.New("妙妙屋共享服务器最多支持 1000 个节点")
			}
		}
		if count >= int64(s.MaxNodesPerUser) {
			return errors.New("已达到该服务器每人节点上限")
		}
		port, err := landingFreePort(tx, s)
		if err != nil {
			return err
		}
		n = model.LandingNode{UserID: userID, LandingServerID: serverID, Name: name, Protocol: protocol, Port: port, MaxConnections: s.DefaultMaxConnections, MaxOnlineIPs: s.DefaultMaxOnlineIPs, TrafficLimitBytes: s.DefaultTrafficLimitBytes, ExpiryMode: "follow_user", Status: "active", Dest: s.RealityDest, ServerNames: s.RealityServerNames, CreatedTime: now, UpdatedTime: now}
		switch s.DefaultExpiryMode {
		case "days":
			v := now + int64(s.DefaultExpiryDays)*86400000
			n.ExpiryMode = "fixed"
			n.ExpiresAt = &v
		case "fixed":
			n.ExpiryMode = "fixed"
			n.ExpiresAt = s.DefaultExpiresAt
		case "permanent":
			n.ExpiryMode = "fixed"
		}
		if s.Source == "shared" {
			if expiry := EffectiveLandingExpiry(&n, &s, &u); n.ExpiresAt != nil && expiry > 0 && *n.ExpiresAt > expiry {
				n.ExpiresAt = &expiry
			}
			if err := validateSharedLandingNode(&n, &s); err != nil {
				return err
			}
		}
		if err := newLandingCredentials(&n); err != nil {
			return err
		}
		if err := tx.Create(&n).Error; err != nil {
			return err
		}
		n.InboundTag = fmt.Sprintf("flvxl-%d", n.ID)
		if err := tx.Model(&n).Update("inbound_tag", n.InboundTag).Error; err != nil {
			return err
		}
		return tx.Model(&s).Update("revision", gorm.Expr("revision + 1")).Error
	})
	return &n, err
}
func landingFreePort(tx *gorm.DB, s model.LandingServer) (int, error) {
	var node model.Node
	if s.Source == "native" {
		if err := tx.First(&node, s.NodeID).Error; err != nil {
			return 0, err
		}
	}
	used := map[int]bool{}
	for _, p := range parsePortRangeSpec(node.Port) {
		used[p] = true
	}
	for _, q := range []struct {
		m      interface{}
		column string
		where  string
		id     int64
	}{{&model.LandingNode{}, "port", "landing_server_id = ?", s.ID}, {&model.ForwardPort{}, "port", "node_id = ?", s.NodeID}, {&model.ChainTunnel{}, "port", "node_id = ? AND port IS NOT NULL", s.NodeID}, {&model.NodeDeployedInbound{}, "listen_port", "node_id = ?", s.NodeID}, {&model.NodeRuntimeResource{}, "port", "node_id = ? AND status <> 'released'", s.NodeID}} {
		if s.Source == "shared" && q.id != s.ID {
			continue
		}
		if s.Source == "shared" && q.column != "port" {
			continue
		}
		var ports []int
		if err := tx.Model(q.m).Where(q.where, q.id).Pluck(q.column, &ports).Error; err != nil {
			return 0, err
		}
		for _, p := range ports {
			used[p] = true
		}
	}
	for p := s.PortMin; p <= s.PortMax; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, errors.New("落地端口段无可用端口，请检查与节点规则端口段及隧道监听端口的冲突")
}
func (r *Repository) UpdateLandingNode(n *model.LandingNode) error {
	if n.DeletePending {
		return errors.New("节点正在等待妙妙屋确认删除，请稍后重试删除")
	}
	if strings.TrimSpace(n.Name) == "" || len(n.Name) > 100 || n.MaxConnections < 0 || n.MaxOnlineIPs < 0 || n.TrafficLimitBytes < 0 {
		return errors.New("节点限制无效")
	}
	if n.FlowType != nil && *n.FlowType != 1 && *n.FlowType != 2 {
		return errors.New("流量计算无效")
	}
	if n.TrafficRatio != nil && (math.IsNaN(*n.TrafficRatio) || math.IsInf(*n.TrafficRatio, 0) || *n.TrafficRatio <= 0 || *n.TrafficRatio > 1000) {
		return errors.New("倍率无效")
	}
	if n.ExpiryMode != "follow_user" && n.ExpiryMode != "fixed" {
		return errors.New("有效期模式无效")
	}
	if n.Status != "active" && n.Status != "disabled" && n.Status != "paused_quota" && n.Status != "paused_server_quota" && n.Status != "paused_expired" {
		return errors.New("状态无效")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var server model.LandingServer
		if err := tx.First(&server, n.LandingServerID).Error; err != nil {
			return err
		}
		if err := validateSharedLandingNode(n, &server); err != nil {
			return err
		}
		n.UpdatedTime = time.Now().UnixMilli()
		if err := tx.Model(&model.LandingNode{}).Where("id = ?", n.ID).Updates(map[string]interface{}{"name": n.Name, "max_connections": n.MaxConnections, "max_online_ips": n.MaxOnlineIPs, "traffic_limit_bytes": n.TrafficLimitBytes, "flow_type": n.FlowType, "traffic_ratio": n.TrafficRatio, "expiry_mode": n.ExpiryMode, "expires_at": n.ExpiresAt, "status": n.Status, "auto_disabled": n.AutoDisabled, "updated_time": n.UpdatedTime}).Error; err != nil {
			return err
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", n.LandingServerID).Update("revision", gorm.Expr("revision + 1")).Error
	})
}
func (r *Repository) DeleteLandingNode(id int64, requireDisabled ...bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var n model.LandingNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, id).Error; err != nil {
			return err
		}
		if len(requireDisabled) > 0 && requireDisabled[0] && (n.Status != "disabled" || n.AutoDisabled) {
			return errors.New("节点设置已变化，请重试删除")
		}
		var count int64
		if err := tx.Model(&model.Forward{}).Where("landing_node_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("该节点仍被规则引用，请先删除或修改规则")
		}
		if err := tx.Delete(&n).Error; err != nil {
			return err
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", n.LandingServerID).Update("revision", gorm.Expr("revision + 1")).Error
	})
}

// PrepareLandingNodeDeletion keeps the identity and counter cursor until the
// agent has blocked existing connections and acknowledged its final counters.
func (r *Repository) PrepareLandingNodeDeletion(id int64) (*model.LandingNode, error) {
	var n model.LandingNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.Forward{}).Where("landing_node_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("该节点仍被规则引用，请先删除或修改规则")
		}
		n.Status, n.AutoDisabled, n.UpdatedTime = "disabled", false, time.Now().UnixMilli()
		if err := tx.Model(&n).Updates(map[string]interface{}{"status": n.Status, "auto_disabled": false, "updated_time": n.UpdatedTime}).Error; err != nil {
			return err
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", n.LandingServerID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error
	})
	return &n, err
}
