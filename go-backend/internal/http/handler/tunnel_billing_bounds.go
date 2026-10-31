package handler

import (
	"fmt"
	"strconv"

	"go-backend/internal/store/repo"
)

// validateTunnelBillingRequest checks the billing multiplier fields of a
// /tunnel/create or /tunnel/update body (plan 172). A missing or null field keeps
// the existing default (1 / 1.0); a present field must parse and be in range.
// Pure validation on the decoded body: no DB access.
func validateTunnelBillingRequest(req map[string]interface{}) error {
	if v, ok := req["flow"]; ok && v != nil {
		if s := asString(v); s != "" {
			flow, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return repo.ValidateTunnelFlowMode(0)
			}
			if err := repo.ValidateTunnelFlowMode(flow); err != nil {
				return err
			}
		}
	}
	if v, ok := req["trafficRatio"]; ok && v != nil {
		if s := asString(v); s != "" {
			ratio, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return repo.ValidateTunnelTrafficRatio(0)
			}
			if err := repo.ValidateTunnelTrafficRatio(ratio); err != nil {
				return err
			}
		}
	}
	return nil
}

// validatePathBilling checks a WG path body after the existing "<= 0 means 1"
// defaulting (the body is a struct, so an absent field decodes as 0). Negative
// values are rejected before that defaulting.
func validatePathBilling(flow int, ratio float64) error {
	if flow < 0 {
		return repo.ValidateTunnelFlowMode(int64(flow))
	}
	if ratio < 0 {
		return repo.ValidateTunnelTrafficRatio(ratio)
	}
	if flow == 0 {
		flow = 1
	}
	if ratio == 0 {
		ratio = 1
	}
	if err := repo.ValidateTunnelFlowMode(int64(flow)); err != nil {
		return err
	}
	if err := repo.ValidateTunnelTrafficRatio(ratio); err != nil {
		return err
	}
	return nil
}

// checkTunnelImportBilling rejects a backup whose tunnel / WG path rows carry an
// out-of-range billing multiplier (plan 172). 0 / missing is accepted as the
// legacy "unset" value (billing already treats it as 1). Scans the uploaded
// JSON only: no DB access.
func checkTunnelImportBilling(raw map[string]interface{}, types []string) error {
	for _, table := range []string{"tunnel", "path_tunnel"} {
		if !stringSliceContains(types, table) {
			continue
		}
		items, _ := raw[table].([]interface{})
		for _, item := range items {
			row, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if err := importedBillingError(row); err != nil {
				return fmt.Errorf("备份中的隧道「%s」（ID %s）：%s", asString(row["name"]), asString(row["id"]), err.Error())
			}
		}
	}
	return nil
}

func importedBillingError(row map[string]interface{}) error {
	if s := asString(row["flow"]); s != "" && s != "0" {
		flow, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return repo.ValidateTunnelFlowMode(0)
		}
		if err := repo.ValidateTunnelFlowMode(flow); err != nil {
			return err
		}
	}
	if s := asString(row["traffic_ratio"]); s != "" && s != "0" {
		ratio, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return repo.ValidateTunnelTrafficRatio(0)
		}
		if err := repo.ValidateTunnelTrafficRatio(ratio); err != nil {
			return err
		}
	}
	return nil
}
