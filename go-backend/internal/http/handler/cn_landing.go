package handler

import (
	"context"
	"fmt"
	"net"
	"strconv"
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
	} else if strings.TrimSpace(targetCIDR) != "" {
		remoteAddr = ""
	}
	return checker.Check(ctx, remoteAddr, targetCIDR)
}

func (h *Handler) checkForwardRecordLanding(ctx context.Context, forward *forwardRecord) error {
	if forward == nil {
		return nil
	}
	// A chain rule (plan 108) really lands on the user's own node, not on its
	// 127.0.0.1 target: the mainland-China policy applies to that node.
	if h.repo != nil {
		if chain, err := h.repo.GetForwardChainByForward(forward.ID); err == nil && chain != nil && chain.UpstreamHost != "" {
			return h.checkForwardLanding(ctx, net.JoinHostPort(chain.UpstreamHost, strconv.Itoa(chain.UpstreamPort)), "gost", "")
		}
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

// checkForwardImportLineMachine applies the 线路机 rules to imported rules:
// a rule on a 线路机 tunnel must target a VLESS Reality Vision landing node.
// Chains are not part of backups, so an exported chain rule is refused too.
func (h *Handler) checkForwardImportLineMachine(raw map[string]interface{}, types []string) error {
	if !stringSliceContains(types, "forward") {
		return nil
	}
	items, ok := raw["forward"].([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}
	lines, err := h.repo.LineMachineTunnelIDs()
	if err != nil || len(lines) == 0 {
		return err
	}
	for _, item := range items {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		tunnelID := asInt64(row["tunnel_id"], asInt64(row["tunnelId"], 0))
		if !lines[tunnelID] || strings.EqualFold(asString(row["mode"]), "wg_path") {
			continue
		}
		landingID := asInt64(row["landing_node_id"], asInt64(row["landingNodeId"], 0))
		if err := h.lineMachineTargetError(tunnelID, asString(row["mode"]), landingID, landingID > 0); err != nil {
			return fmt.Errorf("规则「%s」：%s", asString(row["name"]), err.Error())
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
