package repo

import (
	"errors"
	"sort"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const landingGracePeriod = 7 * 24 * time.Hour

func EffectiveLandingExpiry(n *model.LandingNode, s *model.LandingServer, u *model.User) int64 {
	expiry := u.ExpTime
	take := func(v *int64) {
		if v != nil && *v > 0 && (expiry <= 0 || *v < expiry) {
			expiry = *v
		}
	}
	if n.ExpiryMode == "fixed" {
		take(n.ExpiresAt)
	}
	take(s.AvailableUntil)
	if s.Source == "shared" {
		take(SharedLandingCaps(s).ShareUntil)
	}
	return expiry
}

type LandingReconcileResult struct {
	ServerIDs        []int64
	PauseForwardIDs  []int64
	ResumeForwardIDs []int64
}

// ReconcileLandingPolicy persists policy transitions atomically. Runtime work is
// returned to the handler and retried by the periodic reconcile after failures.
func (r *Repository) ReconcileLandingPolicy(now time.Time) (LandingReconcileResult, error) {
	result := LandingReconcileResult{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var nodes []model.LandingNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Find(&nodes).Error; err != nil {
			return err
		}
		var servers []model.LandingServer
		if err := tx.Find(&servers).Error; err != nil {
			return err
		}
		byServer := map[int64]*model.LandingServer{}
		for i := range servers {
			byServer[servers[i].ID] = &servers[i]
		}
		var users []model.User
		if err := tx.Find(&users).Error; err != nil {
			return err
		}
		byUser := map[int64]*model.User{}
		for i := range users {
			byUser[users[i].ID] = &users[i]
		}
		var quotas []model.UserServerQuota
		if err := tx.Order("user_id, server_kind, server_ref_id").Find(&quotas).Error; err != nil {
			return err
		}
		roll := map[ServerQuotaKey]FlowCounterDelta{}
		var nodeDefaults []model.Node
		if len(quotas) > 0 {
			if err := tx.Select("id", "default_user_quota_bytes").Find(&nodeDefaults).Error; err != nil {
				return err
			}
		}
		byNodeDefault := map[int64]int64{}
		for _, n := range nodeDefaults {
			byNodeDefault[n.ID] = n.DefaultUserQuotaBytes
		}
		for _, q := range quotas {
			u := byUser[q.UserID]
			if u == nil {
				continue
			}
			limit := q.LimitBytes
			if !q.LimitOverride {
				limit = byNodeDefault[q.ServerRefID]
				if q.ServerKind == "landing" {
					limit = 0
					if s := byServer[q.ServerRefID]; s != nil {
						limit = s.DefaultUserQuotaBytes
					}
				}
			}
			// Uploads reconcile all quotas; unchanged rows must not be rewritten.
			paused := limit > 0 && q.UsedIn+q.UsedOut >= limit
			if q.PeriodKey != LandingPeriodKey(u.FlowResetTime, now) || q.LimitBytes != limit || q.Paused != paused {
				roll[ServerQuotaKey{q.UserID, q.ServerKind, q.ServerRefID}] = FlowCounterDelta{}
			}
		}
		if err := applyServerQuotasTx(tx, roll, now); err != nil {
			return err
		}
		if err := tx.Find(&quotas).Error; err != nil {
			return err
		}
		paused := map[ServerQuotaKey]bool{}
		for _, q := range quotas {
			paused[ServerQuotaKey{q.UserID, q.ServerKind, q.ServerRefID}] = q.Paused
		}
		var userQuotas []model.UserQuota
		if err := tx.Find(&userQuotas).Error; err != nil {
			return err
		}
		userQuotaBlocked := map[int64]bool{}
		for _, q := range userQuotas {
			v := normalizeUserQuotaView(cloneUserQuotaView(q), now)
			userQuotaBlocked[q.UserID] = userQuotaExceeded(v)
		}
		userBlocked := func(u *model.User) bool {
			return u == nil || u.Status != 1 || (u.ExpTime > 0 && u.ExpTime <= now.UnixMilli()) || (u.Flow >= 0 && u.InFlow+u.OutFlow >= u.Flow*(1<<30)) || userQuotaBlocked[u.ID]
		}
		changed := map[int64]bool{}
		active := map[int64]bool{}
		landingOwner := map[int64]int64{}
		for i := range nodes {
			n := &nodes[i]
			s := byServer[n.LandingServerID]
			u := byUser[n.UserID]
			if s == nil || u == nil {
				continue
			}
			landingOwner[n.ID] = n.UserID
			expiry := EffectiveLandingExpiry(n, s, u)
			// Revocation never destroys user records. For other shared expiries,
			// wait for the final removed counter snapshot before releasing a port.
			canDelete := s.Source != "shared" || (SharedLandingCaps(s).Status != "revoked" && n.RemoteState == "removed" && n.Connections == 0 && n.LastCounterAt >= expiry && s.SharedSyncError == "")
			if canDelete && n.ExpiredAt != nil && now.UnixMilli()-*n.ExpiredAt >= landingGracePeriod.Milliseconds() {
				if err := tx.Delete(n).Error; err != nil {
					return err
				}
				changed[s.ID] = true
				continue
			}
			oldStatus, oldAuto := n.Status, n.AutoDisabled
			oldExpired := n.ExpiredAt
			period := LandingPeriodKey(u.FlowResetTime, now)
			periodChanged := n.PeriodKey != period
			if periodChanged {
				n.BilledIn = 0
				n.BilledOut = 0
				n.PeriodKey = period
			}
			if expiry > 0 && expiry <= now.UnixMilli() {
				if n.Status != "disabled" || n.AutoDisabled {
					n.Status = "paused_expired"
				}
				if n.ExpiredAt == nil {
					v := expiry
					n.ExpiredAt = &v
				}
			} else {
				n.ExpiredAt = nil
				allowed := u.RoleID == 0
				if !allowed {
					var count int64
					if err := tx.Table("landing_server_user_group AS lg").Joins("JOIN user_group_user AS ug ON ug.user_group_id = lg.user_group_id").Joins("JOIN user_group AS g ON g.id = lg.user_group_id AND g.status = 1").Where("lg.landing_server_id = ? AND ug.user_id = ?", s.ID, u.ID).Count(&count).Error; err != nil {
						return err
					}
					allowed = count > 0
				}
				switch {
				case n.Status == "disabled" && !n.AutoDisabled:
				case u.Status != 1 || s.Status != "active" || !SharedLandingAvailable(s) || !allowed:
					n.Status = "disabled"
					n.AutoDisabled = true
				case userBlocked(u) || (n.TrafficLimitBytes > 0 && n.BilledIn+n.BilledOut >= n.TrafficLimitBytes):
					n.Status = "paused_quota"
					n.AutoDisabled = false
				case paused[landingQuotaKey(u.ID, s)]:
					n.Status = "paused_server_quota"
					n.AutoDisabled = false
				default:
					n.Status = "active"
					n.AutoDisabled = false
				}
			}
			// A job first observing a long-expired node need not keep it for seven more days.
			if canDelete && n.ExpiredAt != nil && now.UnixMilli()-*n.ExpiredAt >= landingGracePeriod.Milliseconds() {
				if err := tx.Delete(n).Error; err != nil {
					return err
				}
				changed[s.ID] = true
				continue
			}
			active[n.ID] = n.Status == "active"
			expiryChanged := (oldExpired == nil) != (n.ExpiredAt == nil)
			if oldStatus != n.Status || oldAuto != n.AutoDisabled || expiryChanged {
				changed[s.ID] = true
				n.UpdatedTime = now.UnixMilli()
			}
			if periodChanged || oldStatus != n.Status || oldAuto != n.AutoDisabled || expiryChanged {
				if err := tx.Model(n).Updates(map[string]interface{}{"status": n.Status, "auto_disabled": n.AutoDisabled, "expired_at": n.ExpiredAt, "period_key": n.PeriodKey, "billed_in": n.BilledIn, "billed_out": n.BilledOut, "updated_time": n.UpdatedTime}).Error; err != nil {
					return err
				}
			}
		}
		// Scan only rules relevant to landing/quota policy. Normal rule behaviour is
		// untouched while no landing or per-server quota exists for their owner.
		var forwards []model.Forward
		if err := tx.Where("landing_node_id > 0 OR user_id IN (SELECT user_id FROM landing_node) OR landing_auto_paused = ? OR user_id IN (SELECT user_id FROM user_server_quota WHERE paused = ?)", true, true).Order("id ASC").Find(&forwards).Error; err != nil {
			return err
		}
		for _, f := range forwards {
			u := byUser[f.UserID]
			blocked := userBlocked(u)
			if f.LandingNodeID > 0 && (landingOwner[f.LandingNodeID] == f.UserID || landingOwner[f.LandingNodeID] == 0) && !active[f.LandingNodeID] {
				blocked = true
			}
			var ids []int64
			if err := tx.Model(&model.ForwardPort{}).Where("forward_id = ?", f.ID).Pluck("node_id", &ids).Error; err != nil {
				return err
			}
			var chainIDs []int64
			if err := tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type IN ?", f.TunnelID, []string{"2", "3"}).Pluck("node_id", &chainIDs).Error; err != nil {
				return err
			}
			for _, id := range append(ids, chainIDs...) {
				if paused[ServerQuotaKey{f.UserID, "node", id}] {
					blocked = true
				}
			}
			if blocked {
				if f.Status == 1 {
					if err := tx.Model(&f).Updates(map[string]interface{}{"status": 0, "landing_auto_paused": true}).Error; err != nil {
						return err
					}
					f.LandingAutoPaused = true
				}
				if f.LandingAutoPaused {
					result.PauseForwardIDs = append(result.PauseForwardIDs, f.ID)
				}
			} else if f.LandingAutoPaused {
				result.ResumeForwardIDs = append(result.ResumeForwardIDs, f.ID)
			}
		}
		for id := range changed {
			if err := tx.Model(&model.LandingServer{}).Where("id = ?", id).UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
				return err
			}
			result.ServerIDs = append(result.ServerIDs, id)
		}
		sort.Slice(result.ServerIDs, func(i, j int) bool { return result.ServerIDs[i] < result.ServerIDs[j] })
		return nil
	})
	return result, err
}

func (r *Repository) CompleteLandingForwardResume(id int64) error {
	return r.db.Model(&model.Forward{}).Where("id = ? AND landing_auto_paused = ?", id, true).Updates(map[string]interface{}{"status": 1, "landing_auto_paused": false}).Error
}

// LandingForwardBlockReason is also used before explicit resume/create paths, so
// saving a rule cannot bypass per-server or landing policy between periodic jobs.
func (r *Repository) LandingForwardBlockReason(forwardID int64) error {
	var f model.Forward
	if err := r.db.First(&f, forwardID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if f.LandingNodeID > 0 {
		n, err := r.GetLandingNode(f.LandingNodeID)
		if err != nil {
			return err
		}
		if n == nil || (n.UserID == f.UserID && n.Status != "active") {
			return errors.New("落地节点已停用")
		}
	}
	var count int64
	err := r.db.Model(&model.UserServerQuota{}).Where("user_id = ? AND server_kind = 'node' AND paused = ? AND (server_ref_id IN (SELECT node_id FROM forward_port WHERE forward_id = ?) OR server_ref_id IN (SELECT node_id FROM chain_tunnel WHERE tunnel_id = ? AND chain_type IN ('2','3')))", f.UserID, true, f.ID, f.TunnelID).Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("服务器流量限额已超额")
	}
	return nil
}

func deleteUserLandingsTx(tx *gorm.DB, userID int64) error {
	ids := tx.Model(&model.LandingNode{}).Select("landing_server_id").Where("user_id = ?", userID)
	if err := tx.Model(&model.LandingServer{}).Where("id IN (?)", ids).UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
		return err
	}
	for _, row := range []interface{}{&model.LandingNode{}, &model.LandingUserSetting{}, &model.UserServerQuota{}} {
		if err := tx.Where("user_id = ?", userID).Delete(row).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetUserLandingUsageTx(tx *gorm.DB, userID, nowMs int64) error {
	var u model.User
	if err := tx.First(&u, userID).Error; err != nil {
		return err
	}
	period := LandingPeriodKey(u.FlowResetTime, time.UnixMilli(nowMs))
	// Counter cursors are lifetime snapshots and must never reset with quotas.
	if err := tx.Model(&model.LandingNode{}).Where("user_id = ?", userID).Updates(map[string]interface{}{"billed_in": 0, "billed_out": 0, "period_key": period}).Error; err != nil {
		return err
	}
	return tx.Model(&model.UserServerQuota{}).Where("user_id = ?", userID).Updates(map[string]interface{}{"used_in": 0, "used_out": 0, "paused": false, "period_key": period}).Error
}
