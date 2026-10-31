package repo

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"go-backend/internal/store/model"
)

// Plan 180 「规则最优」: the tunnel switch and the light per-round reads of the
// rules' targets (two queries per 60 s round, no writes), plus the per-rule
// auto-off of round 2 (written only on the off / re-enable transition).

// RuleBestForward is one rule as the rule-best round needs it.
type RuleBestForward struct {
	ID         int64  `gorm:"column:id"`
	Name       string `gorm:"column:name"`
	TunnelID   int64  `gorm:"column:tunnel_id"`
	RemoteAddr string `gorm:"column:remote_addr"`
	Mode       string `gorm:"column:mode"`
	Status     int    `gorm:"column:status"`
	OffSig     string `gorm:"column:rule_best_off_sig"`
}

// RuleBestOff: 「规则最优」 is auto-off for this rule as it is now (plan 180 r2).
func (f RuleBestForward) RuleBestOff() bool {
	return f.OffSig != "" && f.OffSig == RuleBestOffSig(f.TunnelID, f.RemoteAddr)
}

const ruleBestOffSigVersion = "rb1:"

// RuleBestOffSig is the signature of a rule's tunnel + targets (order kept,
// case-insensitive) a 「规则最优」 auto-off was given for (plan 180 r2): it
// only counts while the rule still has them, whichever path changed them.
func RuleBestOffSig(tunnelID int64, remoteAddr string) string {
	sig := LineComplianceAckSig(tunnelID, 0, remoteAddr)
	return ruleBestOffSigVersion + strings.TrimPrefix(sig, lineAckSigVersion)
}

// ruleBestOffKeepExpr keeps an auto-off only when the UPDATE leaves the
// tunnel and targets as they were (the CASE sees the old row).
func ruleBestOffKeepExpr(tunnelID int64, remoteAddr string) interface{} {
	return gorm.Expr("CASE WHEN tunnel_id = ? AND remote_addr = ? THEN rule_best_off_sig ELSE '' END", tunnelID, remoteAddr)
}

// RuleBestOffInfo is a valid auto-off of one rule (admin notice).
type RuleBestOffInfo struct {
	Target string
	Time   int64
}

// SetForwardRuleBestOff turns 「规则最优」 off for a rule, but only while it
// still has the tunnel + targets the round saw (a concurrent edit wins). It
// reports whether the row changed (false: edited meanwhile or already off).
func (r *Repository) SetForwardRuleBestOff(forwardID, tunnelID int64, remoteAddr, target string, at int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	sig := RuleBestOffSig(tunnelID, remoteAddr)
	if len(target) > 255 {
		target = target[:255]
	}
	res := r.db.Model(&model.Forward{}).
		Where("id = ? AND tunnel_id = ? AND remote_addr = ? AND rule_best_off_sig <> ?", forwardID, tunnelID, remoteAddr, sig).
		Updates(map[string]interface{}{"rule_best_off_sig": sig, "rule_best_off_target": target, "rule_best_off_time": at})
	return res.RowsAffected > 0, res.Error
}

// ClearForwardRuleBestOff turns 「规则最优」 back on for a rule (admin). It
// reports whether a valid auto-off was cleared.
func (r *Repository) ClearForwardRuleBestOff(forwardID int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	var row RuleBestForward
	if err := r.db.Model(&model.Forward{}).Select("id, tunnel_id, remote_addr, rule_best_off_sig").
		Where("id = ?", forwardID).Take(&row).Error; err != nil {
		return false, err
	}
	valid := row.RuleBestOff()
	if row.OffSig == "" {
		return false, nil
	}
	err := r.db.Model(&model.Forward{}).Where("id = ?", forwardID).
		Updates(map[string]interface{}{"rule_best_off_sig": "", "rule_best_off_target": "", "rule_best_off_time": 0}).Error
	return valid && err == nil, err
}

// ForwardRuleBestOffs returns the valid auto-offs among the given rules
// (one query; only rows with a mark are read).
func (r *Repository) ForwardRuleBestOffs(ids []int64) (map[int64]RuleBestOffInfo, error) {
	out := make(map[int64]RuleBestOffInfo)
	if r == nil || r.db == nil || len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID         int64
		TunnelID   int64
		RemoteAddr string
		Sig        string `gorm:"column:rule_best_off_sig"`
		Target     string `gorm:"column:rule_best_off_target"`
		Time       int64  `gorm:"column:rule_best_off_time"`
	}
	if err := r.db.Model(&model.Forward{}).
		Select("id", "tunnel_id", "remote_addr", "rule_best_off_sig", "rule_best_off_target", "rule_best_off_time").
		Where("id IN ? AND rule_best_off_sig <> ''", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Sig == RuleBestOffSig(row.TunnelID, row.RemoteAddr) {
			out[row.ID] = RuleBestOffInfo{Target: row.Target, Time: row.Time}
		}
	}
	return out, nil
}

// SetTunnelRuleBestTx stores the switch (1 on, 0 off).
func (r *Repository) SetTunnelRuleBestTx(tx *gorm.DB, tunnelID int64, value int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if tx == nil {
		tx = r.db
	}
	if value != 0 {
		value = 1
	}
	return tx.Model(&model.Tunnel{}).Where("id = ?", tunnelID).Update("rule_best", value).Error
}

// ListRuleBestForwards returns every rule's tunnel, targets, mode and status.
func (r *Repository) ListRuleBestForwards() ([]RuleBestForward, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []RuleBestForward
	err := r.db.Model(&model.Forward{}).
		Select("id, name, tunnel_id, remote_addr, COALESCE(mode, '') AS mode, status, rule_best_off_sig").
		Where("tunnel_id > 0").
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// ListActiveChainUpstreams maps the forward id of every active chain rule
// (plan 108) to its upstream host:port — the address its exit really reaches.
func (r *Repository) ListActiveChainUpstreams() (map[int64]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []model.ForwardChain
	if err := r.db.Select("forward_id", "upstream_host", "upstream_port").
		Where("state = ? AND forward_id > 0", model.ChainStateActive).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(rows))
	for _, c := range rows {
		host := strings.TrimSpace(c.UpstreamHost)
		if host == "" || c.UpstreamPort <= 0 {
			continue
		}
		out[c.ForwardID] = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(c.UpstreamPort))
	}
	return out, nil
}
