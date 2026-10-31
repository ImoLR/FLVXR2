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
// rules' targets (two queries per 60 s round, no writes).

// RuleBestForward is one rule as the rule-best round needs it.
type RuleBestForward struct {
	ID         int64  `gorm:"column:id"`
	TunnelID   int64  `gorm:"column:tunnel_id"`
	RemoteAddr string `gorm:"column:remote_addr"`
	Mode       string `gorm:"column:mode"`
	Status     int    `gorm:"column:status"`
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
		Select("id, tunnel_id, remote_addr, COALESCE(mode, '') AS mode, status").
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
