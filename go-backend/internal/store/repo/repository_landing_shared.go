package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) GetLandingIntegration() (model.LandingIntegration, error) {
	v := model.LandingIntegration{ID: 1, BaseURL: landingcustom.DefaultBaseURL}
	err := r.db.First(&v, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return v, err
}
func (r *Repository) SaveLandingIntegration(base, token string) error {
	base, err := landingcustom.NormalizeBaseURL(base)
	if err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var v model.LandingIntegration
		err := tx.First(&v, 1).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if token == "" {
			token = v.Token
		}
		if _, err = landingcustom.New(base, token); err != nil {
			return err
		}
		// Moving an integration to a different Custom must not silently rebind IDs.
		if v.BaseURL != "" && base != v.BaseURL {
			var count int64
			if err = tx.Model(&model.LandingServer{}).Where("source = ?", "shared").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errors.New("已有共享服务器时不能更换妙妙屋地址")
			}
		}
		v.ID, v.BaseURL, v.Token, v.UpdatedTime = 1, base, token, time.Now().UnixMilli()
		return tx.Save(&v).Error
	})
}
func (r *Repository) SetLandingIntegrationResult(errText string, now int64) error {
	values := map[string]any{"sync_error": errText}
	if errText == "" {
		values["last_sync_at"] = now
	}
	return r.db.Model(&model.LandingIntegration{}).Where("id = 1").Updates(values).Error
}

func SharedLandingCaps(s *model.LandingServer) landingcustom.Share {
	var c landingcustom.Share
	_ = json.Unmarshal([]byte(s.SharedCaps), &c)
	return c
}
func SharedLandingAvailable(s *model.LandingServer) bool {
	if s.Source != "shared" {
		return true
	}
	c := SharedLandingCaps(s)
	return c.Status == "active" && (c.TrafficLimit == 0 || c.TrafficUsed < c.TrafficLimit)
}

// sharedAutoHost reports whether current is one of the addresses Custom sent
// last time, i.e. the public host was auto-picked rather than set by an admin.
func sharedAutoHost(current string, old landingcustom.Hosts) bool {
	norm := func(v string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(v), "[]")) }
	c := norm(current)
	if c == "" {
		return true
	}
	for _, h := range []string{old.IPv4, old.IPv6, old.Domain} {
		if h = norm(h); h != "" && h == c {
			return true
		}
	}
	return false
}
func capExceeded(value, cap int64) bool { return cap > 0 && (value == 0 || value > cap) }
func ValidateSharedLandingSettings(s *model.LandingServer) error {
	if s.Source != "shared" {
		return nil
	}
	c := SharedLandingCaps(s)
	if c.ID <= 0 || s.RemoteServerID != c.ID {
		return errors.New("妙妙屋共享上限尚未同步")
	}
	if s.PortMin < c.PortMin || s.PortMax > c.PortMax {
		return errors.New("FLVX 端口段不能超出妙妙屋共享端口段")
	}
	// v1 has no server-limit field in desired. The server total is owned by Custom.
	if s.MaxConnections != c.MaxConnections {
		return errors.New("共享服务器总连接数由妙妙屋管理")
	}
	if capExceeded(int64(s.DefaultMaxConnections), int64(c.MaxConnections)) || capExceeded(s.DefaultTrafficLimitBytes, c.TrafficLimit) || capExceeded(s.DefaultUserQuotaBytes, c.TrafficLimit) {
		return errors.New("FLVX 默认连接数或流量限额不能超过妙妙屋上限（0 表示不限）")
	}
	if c.ShareUntil != nil && ((s.DefaultExpiryMode == "fixed" && s.DefaultExpiresAt != nil && *s.DefaultExpiresAt > *c.ShareUntil) || (s.DefaultExpiryMode == "days" && int64(s.DefaultExpiryDays) > (*c.ShareUntil-time.Now().UnixMilli())/86400000)) {
		return errors.New("新节点默认有效期不能超过妙妙屋共享期限")
	}
	if c.ShareUntil != nil && (s.AvailableUntil == nil || *s.AvailableUntil > *c.ShareUntil) {
		return errors.New("FLVX 可用期限不能超过妙妙屋共享期限")
	}
	var ps []string
	_ = json.Unmarshal([]byte(s.Protocols), &ps)
	for _, p := range ps {
		found := false
		for _, cp := range c.Protocols {
			if p == cp {
				found = true
			}
		}
		if !found {
			return errors.New("协议不在妙妙屋允许列表内")
		}
	}
	if s.MaxNodesPerUser > c.PortMax-c.PortMin+1 || s.MaxNodesPerUser > 1000 {
		return errors.New("每人节点数不能超过共享端口数量或妙妙屋节点上限")
	}
	return nil
}
func validateSharedLandingNode(n *model.LandingNode, s *model.LandingServer) error {
	if s.Source != "shared" {
		return nil
	}
	c := SharedLandingCaps(s)
	if capExceeded(int64(n.MaxConnections), int64(c.MaxConnections)) || capExceeded(n.TrafficLimitBytes, c.TrafficLimit) {
		return errors.New("节点连接数或流量限额不能超过妙妙屋上限")
	}
	// follow_user/permanent remain useful defaults; effective expiry is capped.
	if n.ExpiresAt != nil && c.ShareUntil != nil && *n.ExpiresAt > *c.ShareUntil {
		return errors.New("节点有效期不能超过妙妙屋共享期限")
	}
	return nil
}
func capValue(v, limit int64) int64 {
	if capExceeded(v, limit) {
		return limit
	}
	return v
}

// SyncSharedLandings validates a complete response before applying anything.
// Only an explicit revoked state revokes a share; omissions and transport errors
// retain user data and the last good desired state.
func (r *Repository) SyncSharedLandings(shares []landingcustom.Share, now int64) error {
	seen := map[int64]bool{}
	for _, c := range shares {
		if c.ID <= 0 || seen[c.ID] || c.PortMin < 1024 || c.PortMax < c.PortMin || c.PortMax > 65535 || c.TrafficLimit < 0 || c.TrafficUsed < 0 || c.MaxConnections < 0 || (c.Status != "active" && c.Status != "paused" && c.Status != "revoked") || len(c.Protocols) == 0 {
			return errors.New("妙妙屋共享列表包含无效上限")
		}
		if c.Hosts.IPv4 == "" && c.Hosts.IPv6 == "" && c.Hosts.Domain == "" {
			return errors.New("妙妙屋共享地址缺失")
		}
		seen[c.ID] = true
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Match policy/flow lock order: nodes before their server rows.
		var locked []model.LandingNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("landing_server_id IN (SELECT id FROM landing_server WHERE source = 'shared')").Order("id").Find(&locked).Error; err != nil {
			return err
		}
		for _, c := range shares {
			var s model.LandingServer
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source = ? AND remote_server_id = ?", "shared", c.ID).First(&s).Error
			fresh := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !fresh {
				return err
			}
			host := c.Hosts.Domain
			if host == "" {
				host = c.Hosts.IPv4
			}
			if host == "" {
				host = c.Hosts.IPv6
			}
			if fresh {
				s = model.LandingServer{Source: "shared", RemoteServerID: c.ID, Name: c.Name, PublicHost: host, PortMin: c.PortMin, PortMax: c.PortMax, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: min(3, c.PortMax-c.PortMin+1), RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", Revision: 1, CreatedTime: now}
				b, _ := json.Marshal(c.Protocols)
				s.Protocols = string(b)
			} else {
				old := SharedLandingCaps(&s)
				if old.ShareUntil != nil && s.AvailableUntil != nil && *old.ShareUntil == *s.AvailableUntil {
					s.AvailableUntil = c.ShareUntil
				}
				// A terminal revocation cannot be resurrected by a stale list response.
				if old.Status == "revoked" {
					c.Status = "revoked"
				}
				// Follow Custom only while the address is still the one it picked
				// (e.g. a DDNS IP becomes the domain); keep an admin's own edit.
				if sharedAutoHost(s.PublicHost, old.Hosts) {
					s.PublicHost = host
				}
				s.PortMin = max(s.PortMin, c.PortMin)
				s.PortMax = min(s.PortMax, c.PortMax)
				if s.PortMin > s.PortMax {
					s.PortMin = c.PortMin
					s.PortMax = c.PortMax
				}
				var ps []string
				_ = json.Unmarshal([]byte(s.Protocols), &ps)
				allowed := []string{}
				for _, p := range ps {
					for _, cp := range c.Protocols {
						if p == cp {
							allowed = append(allowed, p)
							break
						}
					}
				}
				b, _ := json.Marshal(allowed)
				s.Protocols = string(b)
			}
			s.MaxConnections = c.MaxConnections
			s.DefaultMaxConnections = int(capValue(int64(s.DefaultMaxConnections), int64(c.MaxConnections)))
			s.DefaultTrafficLimitBytes = capValue(s.DefaultTrafficLimitBytes, c.TrafficLimit)
			s.DefaultUserQuotaBytes = capValue(s.DefaultUserQuotaBytes, c.TrafficLimit)
			s.MaxNodesPerUser = min(s.MaxNodesPerUser, c.PortMax-c.PortMin+1, 1000)
			if c.ShareUntil != nil && (s.AvailableUntil == nil || *s.AvailableUntil > *c.ShareUntil) {
				s.AvailableUntil = c.ShareUntil
			}
			raw, _ := json.Marshal(c)
			s.SharedCaps = string(raw)
			s.UpdatedTime = now
			if fresh {
				if err = tx.Create(&s).Error; err != nil {
					return err
				}
			} else {
				if err = tx.Save(&s).Error; err != nil {
					return err
				}
			}
			// Existing overrides remain bounded if the owner tightens their share.
			if c.MaxConnections > 0 {
				if err = tx.Model(&model.LandingNode{}).Where("landing_server_id = ? AND (max_connections = 0 OR max_connections > ?)", s.ID, c.MaxConnections).Update("max_connections", c.MaxConnections).Error; err != nil {
					return err
				}
			}
			if c.TrafficLimit > 0 {
				if err = tx.Model(&model.LandingNode{}).Where("landing_server_id = ? AND (traffic_limit_bytes = 0 OR traffic_limit_bytes > ?)", s.ID, c.TrafficLimit).Update("traffic_limit_bytes", c.TrafficLimit).Error; err != nil {
					return err
				}
				if err = tx.Model(&model.UserServerQuota{}).Where("server_kind = 'landing' AND server_ref_id = ? AND (limit_bytes = 0 OR limit_bytes > ?)", s.ID, c.TrafficLimit).Update("limit_bytes", c.TrafficLimit).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SharedLandingDesired persists a content hash before sending: retrying a timeout
// reuses the exact revision; owner/expiry changes also advance it without relying
// on every caller to bump the server revision.
func (r *Repository) SharedLandingDesired(id int64) (landingcustom.Desired, error) {
	out := landingcustom.Desired{Nodes: []landingcustom.Node{}}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, id).Error; err != nil {
			return err
		}
		if s.Source != "shared" {
			return errors.New("不是共享服务器")
		}
		var ns []model.LandingNode
		if err := tx.Where("landing_server_id = ?", id).Order("id").Find(&ns).Error; err != nil {
			return err
		}
		if len(ns) > 1000 {
			return errors.New("妙妙屋共享服务器最多支持 1000 个节点")
		}
		for _, n := range ns {
			if n.DeletePending {
				continue
			}
			var u model.User
			if err := tx.First(&u, n.UserID).Error; err != nil {
				return err
			}
			d := landingcustom.Node{ID: strconv.FormatInt(n.ID, 10), Owner: landingcustom.Owner{ID: strconv.FormatInt(n.UserID, 10), Name: u.User}, Name: n.Name, Protocol: n.Protocol, Port: n.Port, Enabled: n.Status == "active" && s.Status == "active" && SharedLandingAvailable(&s), MaxConnections: n.MaxConnections, MaxOnlineIPs: n.MaxOnlineIPs}
			if expiry := EffectiveLandingExpiry(&n, &s, &u); expiry > 0 {
				d.ExpiresAt = &expiry
			}
			if n.Protocol == "ss2022" {
				d.SS2022 = &landingcustom.SS2022{Method: n.Method, ServerKey: n.ServerKey, UserKey: n.UserKey}
			}
			if n.Protocol == "vless_reality_vision" {
				names := []string{}
				_ = json.Unmarshal([]byte(n.ServerNames), &names)
				d.VLESS = &landingcustom.VLESS{UUID: n.UUID, Flow: n.Flow, Reality: landingcustom.Reality{Dest: n.Dest, ServerNames: names, PrivateKey: n.PrivateKey, ShortIDs: []string{n.ShortID}}}
			}
			out.Nodes = append(out.Nodes, d)
		}
		raw, _ := json.Marshal(out.Nodes)
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		if s.SharedDesiredHash != hash {
			s.Revision = max(s.Revision+1, s.SharedPushedRevision+1)
			if s.Revision > 9007199254740991 {
				return errors.New("妙妙屋期望状态版本超出范围")
			}
			if err := tx.Model(&s).Updates(map[string]any{"shared_desired_hash": hash, "revision": s.Revision}).Error; err != nil {
				return err
			}
		}
		out.Revision = s.Revision
		return nil
	})
	return out, err
}
func (r *Repository) SetSharedPushResult(id, revision, now int64, problem error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		v := map[string]any{}
		if problem == nil {
			v["shared_pushed_revision"] = revision
			v["shared_last_push_at"] = now
			v["shared_sync_error"] = ""
		} else {
			v["shared_sync_error"] = problem.Error()
			var e *landingcustom.APIError
			if errors.As(problem, &e) && e.Status == 422 {
				for _, p := range e.Problems {
					q := tx.Model(&model.LandingNode{}).Where("landing_server_id = ?", id)
					if p.NodeID != "" {
						nodeID, err := strconv.ParseInt(p.NodeID, 10, 64)
						if err != nil {
							continue
						}
						q = q.Where("id = ?", nodeID)
					}
					if err := q.Updates(map[string]any{"remote_state": "error", "sync_error": p.Message, "rejected_revision": revision}).Error; err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", id).Updates(v).Error
	})
}
func (r *Repository) SetSharedSyncError(id int64, message string) error {
	return r.db.Model(&model.LandingServer{}).Where("id = ?", id).Update("shared_sync_error", message).Error
}
func (r *Repository) RecordSharedStatus(id int64, status landingcustom.Status, now int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.First(&s, id).Error; err != nil {
			return err
		}
		if status.CollectedAt < s.SharedCollectedAt {
			return nil
		}
		if s.SharedPushedRevision >= s.Revision {
			if err := tx.Model(&s).Update("shared_sync_error", "").Error; err != nil {
				return err
			}
		}
		c := SharedLandingCaps(&s)
		c.Core = status.Core
		c.TrafficUsed = status.TrafficUsed
		raw, _ := json.Marshal(c)
		if err := tx.Model(&s).Updates(map[string]any{"shared_caps": string(raw), "shared_applied_revision": status.AppliedRevision, "shared_collected_at": status.CollectedAt, "shared_last_sync_at": now}).Error; err != nil {
			return err
		}
		for _, n := range status.Nodes {
			nodeID, err := strconv.ParseInt(n.ID, 10, 64)
			if err != nil {
				continue
			}
			if n.State != "active" && n.State != "error" && n.State != "removed" {
				continue
			}
			message := ""
			if n.State == "error" {
				message = "妙妙屋节点应用异常，请检查共享配置及 Helper 状态"
			}
			if err := tx.Model(&model.LandingNode{}).Where("id = ? AND landing_server_id = ? AND rejected_revision <= ?", nodeID, id, status.AppliedRevision).Updates(map[string]any{"remote_state": n.State, "sync_error": message, "rejected_revision": 0}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Host inventories are deliberately credentials-free.
func (r *Repository) LandingHostInventory() (landingcustom.LandingHosts, error) {
	out := landingcustom.LandingHosts{Native: []string{}, Nodes: []string{}}
	var nodes []model.Node
	if err := r.db.Where("is_remote = 0").Find(&nodes).Error; err != nil {
		return out, err
	}
	var servers []model.LandingServer
	if err := r.db.Where("source = 'native'").Find(&servers).Error; err != nil {
		return out, err
	}
	byNode := map[int64]model.LandingServer{}
	for _, s := range servers {
		byNode[s.NodeID] = s
	}
	for _, n := range nodes {
		s, isNative := byNode[n.ID]
		for host := range landingNodeHosts(s, n) {
			if strings.TrimSpace(host) == "" {
				continue
			}
			out.Nodes = append(out.Nodes, host)
			// Retain disabled native hosts too: their managed Core still belongs to FLVX.
			if isNative {
				out.Native = append(out.Native, host)
			}
		}
	}
	return out, nil
}

func (r *Repository) PrepareSharedLandingDeletion(id int64) (*model.LandingNode, error) {
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
		// Custom's 200 is acceptance, not an applied acknowledgement. Retrying must
		// keep the same omission/revision so an asynchronous Helper can catch up.
		if n.DeletePending {
			return nil
		}
		n.Status, n.AutoDisabled, n.DeletePending, n.UpdatedTime = "disabled", false, true, time.Now().UnixMilli()
		if err := tx.Model(&n).Updates(map[string]any{"status": "disabled", "auto_disabled": false, "delete_pending": true, "updated_time": n.UpdatedTime}).Error; err != nil {
			return err
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", n.LandingServerID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error
	})
	return &n, err
}

// FinalizeSharedLandingDeletions runs only after a successful status poll has
// booked its counters. Pending nodes already disappeared from desired, so deleting
// their local identities must not advance the revision again.
func (r *Repository) FinalizeSharedLandingDeletions(serverID int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var nodes []model.LandingNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("landing_server_id = ? AND delete_pending = ?", serverID, true).Order("id").Find(&nodes).Error; err != nil {
			return err
		}
		if len(nodes) == 0 {
			return nil
		}
		var server model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&server, serverID).Error; err != nil {
			return err
		}
		caps := SharedLandingCaps(&server)
		if server.Source != "shared" || caps.Status == "revoked" {
			return nil
		}
		// The background poll can run before a failed omission push is retried.
		if caps.Status == "active" && server.SharedPushedRevision < server.Revision {
			return nil
		}
		for _, n := range nodes {
			if n.Status != "disabled" || n.AutoDisabled {
				continue
			}
			confirmed := n.RemoteState == "removed" && n.Connections == 0
			if caps.Status == "active" {
				confirmed = confirmed && server.SharedAppliedRevision >= server.SharedPushedRevision
			}
			// Never-applied nodes have no final counters; retain the existing ACK rule.
			if n.LastCounterAt == 0 && caps.Status == "active" && server.SharedAppliedRevision >= server.SharedPushedRevision && server.SharedPushedRevision > 0 {
				confirmed = true
			}
			if !confirmed {
				continue
			}
			var count int64
			if err := tx.Model(&model.Forward{}).Where("landing_node_id = ?", n.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Delete(&n).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
