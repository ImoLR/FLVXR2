package repo

import (
	"errors"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 252: a chain-only exit landing (auto-created for 「我自己的节点」, plan
// 108) whose last chain copy is gone is removed together with its Core.

// ChainHostRemoveVersion: exit agents reporting landing_chain ≥ this accept
// RemoveLandingCore; older agents keep the server and its Core as before.
const ChainHostRemoveVersion = 3

// IdleChainOnlyServers lists chain-only native landing servers without any
// chain copy (one query; the copy index leads with landing_server_id).
func (r *Repository) IdleChainOnlyServers() ([]model.LandingServer, error) {
	var out []model.LandingServer
	err := r.db.Where("source = ? AND chain_only = ? AND NOT EXISTS (SELECT 1 FROM forward_chain_node c WHERE c.landing_server_id = landing_server.id)", "native", true).
		Order("id ASC").Find(&out).Error
	return out, err
}

// ChainHostKeepReason says why a chain-only server must stay ("" = it may be
// removed): anything still using it, in any state, keeps it.
func (r *Repository) ChainHostKeepReason(serverID int64) (string, error) {
	var s model.LandingServer
	if err := r.db.First(&s, serverID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "落地服务器已不存在", nil
		}
		return "", err
	}
	return chainHostKeepReasonTx(r.db, s)
}

func chainHostKeepReasonTx(tx *gorm.DB, s model.LandingServer) (string, error) {
	switch {
	case s.Source != "native" || !s.ChainOnly:
		return "不是仅链式落地服务器", nil
	case s.NodeID <= 0 || s.NodeID == 36:
		return "节点不可承载落地", nil
	case s.Status != "active":
		return "已停用（管理员设置）", nil
	case s.RouteProfileID > 0:
		return "设置了出站路由方案", nil
	}
	refs := []struct {
		m      interface{}
		where  string
		args   []interface{}
		reason string
	}{
		{&model.ForwardChainNode{}, "landing_server_id = ?", []interface{}{s.ID}, "仍有「我自己的节点」规则"},
		{&model.LandingNode{}, "landing_server_id = ?", []interface{}{s.ID}, "有落地节点"},
		{&model.LandingServerUser{}, "landing_server_id = ?", []interface{}{s.ID}, "有用户授权"},
		{&model.LandingServerUserGroup{}, "landing_server_id = ?", []interface{}{s.ID}, "有用户组授权"},
		{&model.LandingWarpAccount{}, "landing_server_id = ?", []interface{}{s.ID}, "有 WARP 设备"},
		{&model.LandingScanEvent{}, "landing_server_id = ?", []interface{}{s.ID}, "有扫描记录"},
		{&model.UserServerQuota{}, "server_kind = ? AND server_ref_id = ?", []interface{}{"landing", s.ID}, "有用户限额"},
		{&model.UserTrafficPoolMember{}, "server_kind = ? AND server_ref_id = ?", []interface{}{"landing", s.ID}, "在用户流量池中"},
	}
	for _, ref := range refs {
		var n int64
		if err := tx.Model(ref.m).Where(ref.where, ref.args...).Count(&n).Error; err != nil {
			return "", err
		}
		if n > 0 {
			return ref.reason, nil
		}
	}
	return "", nil
}

// DeleteIdleChainOnlyServer removes the row once its Core is gone, after
// re-checking under a row lock that nothing started using it meanwhile (a
// racing chain create either commits first and keeps the server, or finds no
// row afterwards and creates a fresh one). Returns the keep reason when kept.
func (r *Repository) DeleteIdleChainOnlyServer(serverID int64) (bool, string, error) {
	removed, reason := false, ""
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				removed = true // already gone (repeat)
				return nil
			}
			return err
		}
		why, err := chainHostKeepReasonTx(tx, s)
		if err != nil {
			return err
		}
		if why != "" {
			reason = why
			return nil
		}
		// Same leftovers as a node delete removes with its landing (all
		// checked empty above; kept for symmetry and safety).
		for _, row := range []interface{}{&model.LandingServerUserGroup{}, &model.LandingServerUser{}, &model.LandingWarpAccount{}} {
			if err := tx.Where("landing_server_id = ?", s.ID).Delete(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(&model.LandingServer{}, s.ID).Error; err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, reason, err
}
