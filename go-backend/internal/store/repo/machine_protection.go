package repo

import (
	"errors"
	"fmt"

	"go-backend/internal/store/model"
)

// MachineProtection is a node's whole-machine connection protection (plan 118):
// cap on all TCP+UDP sockets (drop NEW connections to FLVX ports at or above it),
// kernel TIME_WAIT bucket limit, CLOSE_WAIT timeout of the node's landing Core.
// A zero number turns that part off. The agent enforces the same ranges.
type MachineProtection struct {
	Enabled          bool  `json:"enabled"`
	MaxTotal         int64 `json:"maxTotal"`
	TWBuckets        int64 `json:"twBuckets"`
	CloseWaitSeconds int64 `json:"closeWaitSeconds"`
}

// User-approved defaults 2026-10-20: cap 1000 (was 1500), TIME_WAIT 500, CLOSE_WAIT
// 10 s (was 30 s; not 5 s: a CLOSE_WAIT socket may still be flushing the tail of a
// download to a slow client). The agent enforces the same ranges.
const (
	DefaultMachineMaxTotal         int64 = 1000
	DefaultMachineTWBuckets        int64 = 500
	DefaultMachineCloseWaitSeconds int64 = 10

	MinMachineMaxTotal         int64 = 100
	MaxMachineMaxTotal         int64 = 1_000_000
	MinMachineTWBuckets        int64 = 100
	MaxMachineTWBuckets        int64 = 2_000_000
	MinMachineCloseWaitSeconds int64 = 1
	MaxMachineCloseWaitSeconds int64 = 3600
)

func DefaultMachineProtection() MachineProtection {
	return MachineProtection{Enabled: true, MaxTotal: DefaultMachineMaxTotal, TWBuckets: DefaultMachineTWBuckets, CloseWaitSeconds: DefaultMachineCloseWaitSeconds}
}

func (m MachineProtection) Validate() error {
	if m.MaxTotal != 0 && (m.MaxTotal < MinMachineMaxTotal || m.MaxTotal > MaxMachineMaxTotal) {
		return fmt.Errorf("整机连接上限须为 0（不限）或 %d–%d", MinMachineMaxTotal, MaxMachineMaxTotal)
	}
	if m.TWBuckets != 0 && (m.TWBuckets < MinMachineTWBuckets || m.TWBuckets > MaxMachineTWBuckets) {
		return fmt.Errorf("TIME_WAIT 上限须为 0（不修改）或 %d–%d", MinMachineTWBuckets, MaxMachineTWBuckets)
	}
	if m.CloseWaitSeconds != 0 && (m.CloseWaitSeconds < MinMachineCloseWaitSeconds || m.CloseWaitSeconds > MaxMachineCloseWaitSeconds) {
		return fmt.Errorf("CLOSE_WAIT 超时须为 0（不设置）或 %d–%d 秒", MinMachineCloseWaitSeconds, MaxMachineCloseWaitSeconds)
	}
	return nil
}

// NodeMachineProtection reads the setting stored on a node row.
func NodeMachineProtection(n *model.Node) MachineProtection {
	if n == nil {
		return DefaultMachineProtection()
	}
	return MachineProtection{Enabled: n.MachineProtectEnabled, MaxTotal: n.MachineMaxTotal, TWBuckets: n.MachineTWBuckets, CloseWaitSeconds: n.MachineCloseWaitSeconds}
}

// GetNodeMachineProtection loads one node's setting.
func (r *Repository) GetNodeMachineProtection(nodeID int64) (MachineProtection, error) {
	if r == nil || r.db == nil {
		return MachineProtection{}, errors.New("repository not initialized")
	}
	var n model.Node
	if err := r.db.Select("id", "machine_protect_enabled", "machine_max_total", "machine_tw_buckets", "machine_close_wait_seconds").
		Where("id = ?", nodeID).Take(&n).Error; err != nil {
		return MachineProtection{}, err
	}
	return NodeMachineProtection(&n), nil
}

// SetNodeMachineProtection stores a validated setting. A map update also writes
// false/0 (GORM skips zero values of struct fields that have a default).
func (r *Repository) SetNodeMachineProtection(nodeID int64, m MachineProtection) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]interface{}{
		"machine_protect_enabled":    m.Enabled,
		"machine_max_total":          m.MaxTotal,
		"machine_tw_buckets":         m.TWBuckets,
		"machine_close_wait_seconds": m.CloseWaitSeconds,
	}).Error
}
