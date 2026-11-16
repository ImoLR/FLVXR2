package repo

// Plan 269 利群主机接管: per-host management rows, block state and the data
// the desired-content generator needs.

import (
	"errors"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

func (r *Repository) GetLeiKwanHost(nodeID int64) (*model.LeiKwanHost, error) {
	var h model.LeiKwanHost
	err := r.db.Where("node_id = ?", nodeID).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &h, err
}

// EnsureLeiKwanHost returns the row of a node, creating an empty one.
func (r *Repository) EnsureLeiKwanHost(nodeID int64) (*model.LeiKwanHost, error) {
	if h, err := r.GetLeiKwanHost(nodeID); err != nil || h != nil {
		return h, err
	}
	h := &model.LeiKwanHost{NodeID: nodeID, UpdatedTime: time.Now().UnixMilli()}
	if err := r.db.Create(h).Error; err != nil {
		if again, err2 := r.GetLeiKwanHost(nodeID); err2 == nil && again != nil {
			return again, nil
		}
		return nil, err
	}
	return h, nil
}

func (r *Repository) ListLeiKwanHosts() ([]model.LeiKwanHost, error) {
	out := []model.LeiKwanHost{}
	err := r.db.Order("node_id").Find(&out).Error
	return out, err
}

// SaveLeiKwanHostConfig writes the admin/generator columns (never the report
// or the counter baseline, which have their own writers).
func (r *Repository) SaveLeiKwanHostConfig(h *model.LeiKwanHost) error {
	h.UpdatedTime = time.Now().UnixMilli()
	return r.db.Model(&model.LeiKwanHost{}).Where("id = ?", h.ID).Updates(map[string]interface{}{
		"manage": h.Manage, "traffic": h.Traffic, "profile": h.Profile, "learned_at": h.LearnedAt,
		"rev": h.Rev, "force": h.Force, "desired_hash": h.DesiredHash, "updated_time": h.UpdatedTime,
	}).Error
}

func (r *Repository) SaveLeiKwanHostReport(nodeID int64, report string, at int64) error {
	return r.db.Model(&model.LeiKwanHost{}).Where("node_id = ?", nodeID).
		UpdateColumns(map[string]interface{}{"report": report, "reported_at": at}).Error
}

func (r *Repository) SaveLeiKwanHostSync(nodeID int64, at int64, syncErr string) error {
	return r.db.Model(&model.LeiKwanHost{}).Where("node_id = ?", nodeID).
		UpdateColumns(map[string]interface{}{"synced_at": at, "sync_error": syncErr}).Error
}

// SetLeiKwanRuleBlock stores the current block reason of a 利群 rule.
func (r *Repository) SetLeiKwanRuleBlock(id int64, reason string) error {
	return r.db.Model(&model.LeiKwanRule{}).Where("id = ?", id).UpdateColumn("block_reason", reason).Error
}

// SetLeiKwanRulePending stores (0 = clears) the prepared landing node.
func (r *Repository) SetLeiKwanRulePending(id, landingNodeID int64) error {
	return r.db.Model(&model.LeiKwanRule{}).Where("id = ?", id).UpdateColumn("pending_landing_node_id", landingNodeID).Error
}

// LeiKwanPausedServerQuotas: per user, the node ids (kind node) whose
// per-server quota is used up in the user's current period.
func (r *Repository) LeiKwanPausedServerQuotas(userIDs, nodeIDs []int64, now time.Time) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(userIDs) == 0 || len(nodeIDs) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id, flow_reset_time").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	period := map[int64]int64{}
	for _, u := range users {
		period[u.ID] = LandingPeriodKey(u.FlowResetTime, now)
	}
	var rows []model.UserServerQuota
	if err := r.db.Where("user_id IN ? AND server_kind = ? AND server_ref_id IN ? AND paused = ?", userIDs, "node", nodeIDs, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, q := range rows {
		if q.PeriodKey == period[q.UserID] && q.LimitBytes > 0 && q.UsedIn+q.UsedOut >= q.LimitBytes {
			out[q.UserID] = append(out[q.UserID], q.ServerRefID)
		}
	}
	return out, nil
}

// LeiKwanUsersState: status, expiry and flow of the given users.
func (r *Repository) LeiKwanUsersState(ids []int64) (map[int64]model.User, error) {
	out := map[int64]model.User{}
	if len(ids) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id, user, role_id, status, exp_time, flow, in_flow, out_flow, flow_reset_time").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}
