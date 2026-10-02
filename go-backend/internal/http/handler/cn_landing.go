package handler

import (
	"context"
	"strings"

	"go-backend/internal/cnlanding"
)

func (h *Handler) checkForwardLanding(ctx context.Context, remoteAddr, mode, targetCIDR string) error {
	checker := h.cnLandingChecker
	if checker == nil {
		checker = cnlanding.New(nil)
	}
	if !strings.EqualFold(strings.TrimSpace(mode), "wg_path") {
		targetCIDR = ""
	}
	return checker.Check(ctx, remoteAddr, targetCIDR)
}

func (h *Handler) checkForwardRecordLanding(ctx context.Context, forward *forwardRecord) error {
	if forward == nil {
		return nil
	}
	return h.checkForwardLanding(ctx, forward.RemoteAddr, forward.Mode, forward.TargetCIDR)
}

func (h *Handler) checkForwardImportLanding(ctx context.Context, raw map[string]interface{}, types []string) error {
	if !stringSliceContains(types, "forward") {
		return nil
	}
	items, ok := raw["forward"].([]interface{})
	if !ok {
		return nil
	}
	for _, item := range items {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if err := h.checkForwardLanding(ctx, asString(row["remote_addr"]), asString(row["mode"]), asString(row["target_cidr"])); err != nil {
			return err
		}
	}
	return nil
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
