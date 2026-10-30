package repo

import (
	"errors"
	"math"
)

// Plan 172: server-side bounds for the tunnel billing multiplier.
//
// Billed bytes = traffic × traffic_ratio × flow (see billTunnelFlow), so both
// fields are multipliers. The ranges match what the tunnel / WG path forms allow:
// 流量计算 select 1 (单向) or 2 (双向), 流量倍率 input 0.01..100.
const (
	TunnelFlowOneWay         int64   = 1
	TunnelFlowTwoWay         int64   = 2
	TunnelTrafficRatioMin    float64 = 0.01
	TunnelTrafficRatioMax    float64 = 100
	errTunnelFlowModeMsg             = "流量计算方式无效，只能是单向(1)或双向(2)"
	errTunnelTrafficRatioMsg         = "流量倍率必须在 0.01 到 100 之间"
)

// ValidateTunnelFlowMode accepts only 1 (单向) and 2 (双向).
func ValidateTunnelFlowMode(flow int64) error {
	if flow != TunnelFlowOneWay && flow != TunnelFlowTwoWay {
		return errors.New(errTunnelFlowModeMsg)
	}
	return nil
}

// ValidateTunnelTrafficRatio accepts a finite ratio in [0.01, 100].
func ValidateTunnelTrafficRatio(ratio float64) error {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < TunnelTrafficRatioMin || ratio > TunnelTrafficRatioMax {
		return errors.New(errTunnelTrafficRatioMsg)
	}
	return nil
}

// validateImportedTunnelBilling is the backup-import variant: 0 is accepted as a
// legacy "unset" value (billing already treats it as 1), anything else must be
// inside the normal range.
func validateImportedTunnelBilling(flow int64, ratio float64) error {
	if flow != 0 {
		if err := ValidateTunnelFlowMode(flow); err != nil {
			return err
		}
	}
	if ratio != 0 {
		if err := ValidateTunnelTrafficRatio(ratio); err != nil {
			return err
		}
	}
	return nil
}
