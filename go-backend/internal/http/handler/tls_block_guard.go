package handler

import (
	"errors"
	"net/http"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
)

// Plan 224: a tunnel with 屏蔽 TLS / 屏蔽 HTTP closes every connection whose
// first packet shows that feature (go-gost entry protocol filter), so a rule
// from it to a landing node with that feature could never connect. Rule
// targets are checked on every path that picks a landing node; typed
// addresses are not (unless they match a panel landing node). Unchanged
// targets stay editable and nothing existing is changed or removed.

// filterInspected: only gost-mode rules run through the entry protocol
// filter (nftables / WG rules are forwarded in the kernel).
func filterInspected(mode string) bool {
	return !strings.EqualFold(mode, "nftables") && !strings.EqualFold(mode, "wg_path")
}

// landingFilterPreCheck refuses a 「新建/复用」 landing target (landingServerId
// + landingProtocol) before a node is created for it.
func (h *Handler) landingFilterPreCheck(tunnelID int64, mode string, req map[string]interface{}) error {
	serverID := asInt64(req["landingServerId"], 0)
	if h == nil || h.repo == nil || serverID <= 0 || tunnelID <= 0 || !filterInspected(mode) {
		return nil
	}
	t, err := h.getTunnelRecord(tunnelID)
	if err != nil || (t.TLS != 1 && t.HTTP != 1) {
		return nil
	}
	s, err := h.repo.GetLandingServer(serverID)
	if err != nil || s == nil {
		return nil // resolveNativeLandingTarget reports it
	}
	protocol := defaultString(asString(req["landingProtocol"]), "ss2022")
	if h.isLineMachineTunnel(tunnelID) {
		protocol = "vless_reality_vision"
	}
	if f := repo.TunnelBlockedFeature(t.TLS, t.HTTP, repo.LandingServerProtocolFilterFeatures(s, protocol)); f != "" {
		return errors.New(repo.LandingFeatureBlockedMsg(f))
	}
	return nil
}

// landingFilterTargetError validates a resolved landing target (landingID
// = the rule's landing node, 0 = plain address) on tunnelID.
func (h *Handler) landingFilterTargetError(tunnelID int64, mode string, landingID int64) error {
	if h == nil || h.repo == nil || landingID <= 0 || tunnelID <= 0 || !filterInspected(mode) {
		return nil
	}
	t, err := h.getTunnelRecord(tunnelID)
	if err != nil || (t.TLS != 1 && t.HTTP != 1) {
		return nil
	}
	n, err := h.repo.GetLandingNode(landingID)
	if err != nil || n == nil {
		return nil
	}
	if f := repo.TunnelBlockedFeature(t.TLS, t.HTTP, repo.LandingNodeFilterFeatures(n)); f != "" {
		return errors.New(repo.LandingFeatureBlockedMsg(f))
	}
	return nil
}

// chainFilterError: 「我自己的节点」 reaches the exit with VLESS Reality.
func (h *Handler) chainFilterError(tunnelID int64) error {
	if h == nil || h.repo == nil || tunnelID <= 0 {
		return nil
	}
	if t, err := h.getTunnelRecord(tunnelID); err == nil && t.TLS == 1 {
		return errors.New(repo.ChainTLSBlockedMsg)
	}
	return nil
}

// tunnelFilterAffected (admin, plan 224): the rules of a tunnel that would
// stop working with the given 屏蔽 TLS / 屏蔽 HTTP flags — the tunnel edit
// dialog warns before saving; saving is still allowed and changes no rule.
func (h *Handler) tunnelFilterAffected(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		ID   int64 `json:"id"`
		TLS  int   `json:"tls"`
		HTTP int   `json:"http"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	rules, err := h.repo.TunnelFilterAffectedRules(req.ID, req.TLS, req.HTTP)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"count": len(rules), "rules": rules}))
}
