package repo

import (
	"errors"
	"fmt"
	"log"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// Plan 159: per user per server 速率.
//
// The rate of user U on FLVX node N is one limit shared by all of U's rules
// entering N (the agent runs one limiter per user per machine). The per-server
// row (user_server_quota, kind "node") may set its own value (RateOverride,
// 0 = 不限); otherwise the user's default (user.server_rate_mbps) applies.
// Relay / exit hops and landing Cores are not limited by it.

// MaxServerRateMbps bounds the stored rates (100 Gbps).
const MaxServerRateMbps = 100000

// UserRateVersion is the agent capability (handshake `userRate=1`): forward
// services carry `metadata.userLimiter` and the agent applies the rule's own
// limiter and the user's limiter together.
const UserRateVersion = 1

// ValidServerRate checks an admin-entered rate (0 = 不限).
func ValidServerRate(rate int) error {
	if rate < 0 || rate > MaxServerRateMbps {
		return fmt.Errorf("速率须为 0（不限）到 %d Mbps", MaxServerRateMbps)
	}
	return nil
}

type userRateRow struct {
	UserID      int64
	ServerRefID int64
	RateMbps    int
}

// effectiveRate: the row's own value when it overrides, else the default.
func effectiveRate(def int, own map[int64]int, nodeID int64) int {
	if v, ok := own[nodeID]; ok {
		return v
	}
	return def
}

// UserServerRateMbps is the rate of the user on one FLVX node (0 = 不限).
func (r *Repository) UserServerRateMbps(userID, nodeID int64) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var u model.User
	if err := r.db.Select("id", "server_rate_mbps").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	var rows []model.UserServerQuota
	if err := r.db.Select("rate_mbps").Where("user_id = ? AND server_kind = ? AND server_ref_id = ? AND rate_override = ?", userID, "node", nodeID, true).Limit(1).Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) > 0 {
		return rows[0].RateMbps, nil
	}
	return u.ServerRateMbps, nil
}

// UserEntryRates maps every FLVX node the user's rules enter to the user's
// rate there (0 = 不限), for comparing before / after a settings save.
func (r *Repository) UserEntryRates(userID int64) (map[int64]int, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var nodeIDs []int64
	if err := r.db.Table("forward_port AS fp").Distinct().
		Joins("JOIN forward AS f ON f.id = fp.forward_id").
		Where("f.user_id = ?", userID).Pluck("fp.node_id", &nodeIDs).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return out, nil
	}
	var u model.User
	if err := r.db.Select("id", "server_rate_mbps").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}
	own := map[int64]int{}
	var rows []userRateRow
	if err := r.db.Model(&model.UserServerQuota{}).Select("user_id", "server_ref_id", "rate_mbps").
		Where("user_id = ? AND server_kind = ? AND rate_override = ?", userID, "node", true).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		own[row.ServerRefID] = row.RateMbps
	}
	for _, id := range nodeIDs {
		out[id] = effectiveRate(u.ServerRateMbps, own, id)
	}
	return out, nil
}

// NodeUserRates returns, for one FLVX node, the users whose rules enter it and
// who have a rate there (> 0 only). Three queries, independent of the number
// of rules.
func (r *Repository) NodeUserRates(nodeID int64) (map[int64]int, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var userIDs []int64
	if err := r.db.Table("forward_port AS fp").Distinct().
		Joins("JOIN forward AS f ON f.id = fp.forward_id").
		Where("fp.node_id = ?", nodeID).Pluck("f.user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	out := map[int64]int{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id", "server_rate_mbps").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	var rows []userRateRow
	if err := r.db.Model(&model.UserServerQuota{}).Select("user_id", "server_ref_id", "rate_mbps").
		Where("server_kind = ? AND server_ref_id = ? AND rate_override = ? AND user_id IN ?", "node", nodeID, true, userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	own := map[int64]int{}
	for _, row := range rows {
		own[row.UserID] = row.RateMbps
	}
	for _, u := range users {
		rate := u.ServerRateMbps
		if v, ok := own[u.ID]; ok {
			rate = v
		}
		if rate > 0 {
			out[u.ID] = rate
		}
	}
	return out, nil
}

// SetUserServerRate stores the user's default 速率. A default replaces the
// legacy 「总限速规则」 (user.speed_limit_id, only ever copied into new rules),
// so setting one clears it.
func (r *Repository) SetUserServerRate(userID int64, rate int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if err := ValidServerRate(rate); err != nil {
		return err
	}
	values := map[string]interface{}{"server_rate_mbps": rate}
	if rate > 0 {
		values["speed_limit_id"] = nil
	}
	return r.db.Model(&model.User{}).Where("id = ?", userID).Updates(values).Error
}

// SetNodeUserRateCapability records the agent capability reported in the
// handshake (written only when it changed).
func (r *Repository) SetNodeUserRateCapability(nodeID int64, version int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Node{}).Where("id = ? AND user_rate <> ?", nodeID, version).Update("user_rate", version).Error
}

// NodeUserRateCapabilities maps node ID → capability for the given nodes.
func (r *Repository) NodeUserRateCapabilities(nodeIDs []int64) (map[int64]int, error) {
	out := map[int64]int{}
	if r == nil || r.db == nil || len(nodeIDs) == 0 {
		return out, nil
	}
	var rows []model.Node
	if err := r.db.Select("id", "user_rate").Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, n := range rows {
		out[n.ID] = n.UserRate
	}
	return out, nil
}

// deleteSharedServerRowsTx removes the per-server rows of one shared server
// (traffic quota / 入站连接数 / 速率 row and pool membership). Used when 妙妙屋
// revokes the share (the terminal "deleted" state of a shared server; FLVX
// never deletes the server row itself). Pools left without members stay.
func deleteSharedServerRowsTx(tx *gorm.DB, serverID int64) error {
	if err := tx.Where("server_kind = ? AND server_ref_id = ?", "landing", serverID).Delete(&model.UserServerQuota{}).Error; err != nil {
		return err
	}
	return tx.Where("server_kind = ? AND server_ref_id = ?", "landing", serverID).Delete(&model.UserTrafficPoolMember{}).Error
}

// ServerRowCleanup is what cleanupServerRowOrphans removed.
type ServerRowCleanup struct {
	Quotas  int64
	Members int64
}

// CleanupServerRowOrphans removes per-server rows (user_server_quota and
// user_traffic_pool_member) whose server no longer exists: kind "node" with a
// deleted node, kind "landing" with a missing or revoked shared server, and
// rows of deleted users. Idempotent; runs at every startup.
func (r *Repository) CleanupServerRowOrphans() (ServerRowCleanup, error) {
	if r == nil || r.db == nil {
		return ServerRowCleanup{}, errors.New("repository not initialized")
	}
	return cleanupServerRowOrphans(r.db)
}

func cleanupServerRowOrphans(db *gorm.DB) (ServerRowCleanup, error) {
	var out ServerRowCleanup
	err := db.Transaction(func(tx *gorm.DB) error {
		var shared []model.LandingServer
		if err := tx.Select("id", "source", "shared_caps").Where("source = ?", "shared").Find(&shared).Error; err != nil {
			return err
		}
		live := []int64{0} // never empty: "NOT IN ()" is invalid SQL
		for i := range shared {
			if SharedLandingCaps(&shared[i]).Status != "revoked" {
				live = append(live, shared[i].ID)
			}
		}
		for _, m := range []struct {
			model interface{}
			count *int64
		}{{&model.UserServerQuota{}, &out.Quotas}, {&model.UserTrafficPoolMember{}, &out.Members}} {
			res := tx.Where("server_kind = ? AND server_ref_id NOT IN (?)", "node", tx.Model(&model.Node{}).Select("id")).Delete(m.model)
			if res.Error != nil {
				return res.Error
			}
			*m.count += res.RowsAffected
			res = tx.Where("server_kind = ? AND server_ref_id NOT IN ?", "landing", live).Delete(m.model)
			if res.Error != nil {
				return res.Error
			}
			*m.count += res.RowsAffected
			res = tx.Where("user_id NOT IN (?)", tx.Model(&model.User{}).Select("id")).Delete(m.model)
			if res.Error != nil {
				return res.Error
			}
			*m.count += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return ServerRowCleanup{}, err
	}
	log.Printf("[server-row-cleanup] removed orphaned per-server rows: user_server_quota=%d user_traffic_pool_member=%d", out.Quotas, out.Members)
	return out, nil
}

// SharedUserRates returns the 速率 (> 0 only) of the owners of nodes on one
// shared server: their row for the server (kind "landing") when it overrides,
// else their default. Sent to 妙妙屋 shares that apply it (contract §7).
func (r *Repository) SharedUserRates(serverID int64) (map[int64]int, error) {
	return r.landingOwnerRates(serverID, "landing", serverID)
}

// NativeLandingUserRates (plan 161) returns the 速率 (> 0 only) of the owners
// of nodes on one FLVX-native landing server: their row for the server's node
// (kind "node", the same row that limits their forward entries there) when it
// overrides, else their default. The agent shapes all of an owner's node
// ports on that machine together.
func (r *Repository) NativeLandingUserRates(serverID, nodeID int64) (map[int64]int, error) {
	return r.landingOwnerRates(serverID, "node", nodeID)
}

// landingOwnerRates: three queries, independent of the number of nodes.
func (r *Repository) landingOwnerRates(serverID int64, kind string, ref int64) (map[int64]int, error) {
	out := map[int64]int{}
	if r == nil || r.db == nil || serverID <= 0 || ref <= 0 {
		return out, nil
	}
	var userIDs []int64
	if err := r.db.Model(&model.LandingNode{}).Distinct().Where("landing_server_id = ? AND delete_pending = ?", serverID, false).Pluck("user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id", "server_rate_mbps").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	var rows []userRateRow
	if err := r.db.Model(&model.UserServerQuota{}).Select("user_id", "server_ref_id", "rate_mbps").
		Where("server_kind = ? AND server_ref_id = ? AND rate_override = ? AND user_id IN ?", kind, ref, true, userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	own := map[int64]int{}
	for _, row := range rows {
		own[row.UserID] = row.RateMbps
	}
	for _, u := range users {
		rate := u.ServerRateMbps
		if v, ok := own[u.ID]; ok {
			rate = v
		}
		if rate > 0 {
			out[u.ID] = rate
		}
	}
	return out, nil
}

// LandingRateVersion (plan 161) is the agent capability (handshake
// `landingRate=1`): the agent shapes each user's landing node ports in the
// kernel from user_rates / rate_group of the desired state.
const LandingRateVersion = 1

// SetNodeLandingRateCapability records the handshake capability (written only
// when it changed).
func (r *Repository) SetNodeLandingRateCapability(nodeID int64, version int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Node{}).Where("id = ? AND landing_rate <> ?", nodeID, version).Update("landing_rate", version).Error
}

// RecordLandingRateError stores why the agent could not apply the rates in the
// kernel ("" clears); one conditional statement.
func (r *Repository) RecordLandingRateError(serverID int64, text string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if rs := []rune(text); len(rs) > 300 {
		text = string(rs[:300])
	}
	return r.db.Model(&model.LandingServer{}).Where("id = ? AND rate_error <> ?", serverID, text).Update("rate_error", text).Error
}

// NativeLandingServerIDForNode is the FLVX-native landing server running on
// the node (0 = none; at most one per node).
func (r *Repository) NativeLandingServerIDForNode(nodeID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var ids []int64
	if err := r.db.Model(&model.LandingServer{}).Where("node_id = ? AND source = ?", nodeID, "native").Limit(1).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return ids[0], nil
}

// SetNodeEntryRateCapability stores the plan-168 entry rate mode of the node's
// agent (0 old / 1 tc / 2 kernel cannot) and the probe / shaper error ("" =
// fine); one conditional statement, nothing written when unchanged.
func (r *Repository) SetNodeEntryRateCapability(nodeID int64, mode int, text string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if rs := []rune(text); len(rs) > 300 {
		text = string(rs[:300])
	}
	return r.db.Model(&model.Node{}).Where("id = ? AND (entry_rate_tc <> ? OR entry_rate_error <> ?)", nodeID, mode, text).
		Updates(map[string]interface{}{"entry_rate_tc": mode, "entry_rate_error": text}).Error
}

// NodeEntryRateModes maps node ID → (mode, error) for the given nodes (admin view).
func (r *Repository) NodeEntryRateModes(nodeIDs []int64) (map[int64]model.Node, error) {
	out := map[int64]model.Node{}
	if r == nil || r.db == nil || len(nodeIDs) == 0 {
		return out, nil
	}
	var rows []model.Node
	if err := r.db.Select("id", "name", "is_remote", "entry_rate_tc", "entry_rate_error").Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, n := range rows {
		out[n.ID] = n
	}
	return out, nil
}

// EntryRateRuleRow is one active forward entering the node, with everything
// its entry speed limits depend on (plan 168).
//
// Plan 170: rules have no own limit any more — EntryRateRules leaves the
// SpeedLimit* / Limit* fields empty (the stale forward.speed_id /
// speed_limit_enabled / speed_limit and user_tunnel.speed_id are not read), so
// the tree only gets the users' 速率 classes. The fields stay for the plan-168
// tree builder (and its tests).
type EntryRateRuleRow struct {
	ForwardID         int64  `gorm:"column:forward_id"`
	UserID            int64  `gorm:"column:user_id"`
	TunnelID          int64  `gorm:"column:tunnel_id"`
	Mode              string `gorm:"column:mode"`
	Port              int    `gorm:"column:port"`
	SpeedLimitEnabled bool   `gorm:"-"`
	SpeedLimit        int    `gorm:"-"`
	LimitID           int64  `gorm:"-"`
	LimitSpeed        int    `gorm:"-"`
	LimitArena        bool   `gorm:"-"`
}

// EntryRateRules lists the active forwards entering the node (one query).
func (r *Repository) EntryRateRules(nodeID int64) ([]EntryRateRuleRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []EntryRateRuleRow
	if err := r.db.Table("forward_port AS fp").
		Select("f.id AS forward_id, f.user_id, f.tunnel_id, f.mode, fp.port").
		Joins("JOIN forward AS f ON f.id = fp.forward_id").
		Where("fp.node_id = ? AND f.status = ?", nodeID, 1).
		Order("f.id ASC, fp.port ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows, nil
}

// ListNodeIDsByEntryRateMode lists the nodes whose agent reported the given
// plan-168 entry rate mode.
func (r *Repository) ListNodeIDsByEntryRateMode(mode int) ([]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var ids []int64
	err := r.db.Model(&model.Node{}).Where("entry_rate_tc = ?", mode).Order("id ASC").Pluck("id", &ids).Error
	return ids, err
}
