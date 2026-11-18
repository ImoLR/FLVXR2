package handler

// Plan 284 「我的外部节点」 as an ordinary forward target: the forward is a
// plain port forward to the owner's external node (same agent payload,
// billing and limits as a 手动填写 forward); the panel only remembers which
// node it is (forward.external_node_id) so it can hand out the node's own
// link re-pointed at the entry address (复制链接 / 订阅).

import (
	"errors"
	"net/http"
	"strings"

	"go-backend/internal/cnlanding"
	"go-backend/internal/http/response"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

const (
	externalTargetLineMsg = "线路机隧道上的「我的外部节点」经隧道出口连接，请在转发弹窗里重新选择"
	externalTargetWGMsg   = "WG 隧道转发不能选「我的外部节点」"
	externalTargetBothMsg = "只能选一种转发目标"
)

// externalNodeTarget validates a 「我的外部节点」 target for a forward of
// ownerID on tunnelID and returns its address (host:port).
func (h *Handler) externalNodeTarget(ownerID, extID, tunnelID int64, mode string, req map[string]interface{}) (string, error) {
	if explicitLandingTarget(req) {
		return "", errors.New(externalTargetBothMsg)
	}
	if strings.EqualFold(mode, "wg_path") {
		return "", errors.New(externalTargetWGMsg)
	}
	if h.isLineMachineTunnel(tunnelID) {
		return "", errors.New(externalTargetLineMsg)
	}
	n, err := h.repo.GetLandingExternalNode(extID, ownerID)
	if err != nil {
		return "", err // ErrExternalNodeNotFound: missing or another user's
	}
	up, ok := repo.ExternalUpstream(*n)
	if !ok {
		return "", errors.New("外部节点数据无效，请重新粘贴")
	}
	if err := h.externalFilterError(tunnelID, mode, up); err != nil {
		return "", err
	}
	return up.Address(), nil
}

// externalFilterError: the tunnel's entry filter would block the node's
// protocol (gost mode only; nftables / WG forward in the kernel).
func (h *Handler) externalFilterError(tunnelID int64, mode string, up linechain.Upstream) error {
	if tunnelID <= 0 || !filterInspected(mode) {
		return nil
	}
	t, err := h.getTunnelRecord(tunnelID)
	if err != nil || t == nil {
		return nil
	}
	if msg := repo.ExternalFilterBlockedMsg(t.TLS, t.HTTP, t.Socks, t.BlockOther, up); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// externalTargetForUpdate: the external node id the updated forward keeps.
// An externalNodeId in the request wins (0 clears it); without it a linked
// forward keeps the link only while its target stays the same. A landing
// target always clears it.
func externalTargetForUpdate(req map[string]interface{}, current int64, oldRemote, newRemote string, landingID int64) int64 {
	if landingID > 0 {
		return 0
	}
	if raw, ok := req["externalNodeId"]; ok && raw != nil {
		return asInt64(raw, 0)
	}
	if current > 0 && strings.TrimSpace(newRemote) != strings.TrimSpace(oldRemote) {
		return 0
	}
	return current
}

// keptExternalFilterError: a forward that keeps its link while moving to
// another tunnel / mode must pass that tunnel's filter too.
func (h *Handler) keptExternalFilterError(ownerID, extID, tunnelID int64, mode string) error {
	if extID <= 0 {
		return nil
	}
	n, err := h.repo.GetLandingExternalNode(extID, ownerID)
	if err != nil || n == nil {
		return nil // gone: the forward stays a plain port forward
	}
	up, ok := repo.ExternalUpstream(*n)
	if !ok {
		return nil
	}
	if !strings.EqualFold(mode, "wg_path") && h.isLineMachineTunnel(tunnelID) {
		return errors.New(externalTargetLineMsg)
	}
	return h.externalFilterError(tunnelID, mode, up)
}

// externalForwardView is the forward list's 「外部节点」 info.
func externalForwardView(x *repo.ForwardExternal) map[string]interface{} {
	v := map[string]interface{}{
		"id": x.Node.ID, "name": x.Node.Name, "protocol": x.Up.Type, "protocolLabel": x.Up.ProtocolLabel(),
		"address": x.Up.Address(), "auto": x.Auto, "mismatch": x.Mismatch,
	}
	if x.Blocked != "" {
		v["blocked"] = x.Blocked
	}
	return v
}

// decorateForwardExternals adds item["externalNode"] to forward list items
// whose target is one of their owners' 外部节点 (three queries).
func (h *Handler) decorateForwardExternals(items []map[string]interface{}) {
	if h == nil || h.repo == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	chained := map[int64]bool{}
	for _, item := range items {
		id := asInt64(item["id"], 0)
		ids = append(ids, id)
		if _, ok := item["chain"]; ok {
			chained[id] = true
		}
	}
	got, err := h.repo.ForwardExternalsByIDs(ids, chained)
	if err != nil {
		return
	}
	for _, item := range items {
		if x := got[asInt64(item["id"], 0)]; x != nil {
			item["externalNode"] = externalForwardView(x)
		}
	}
}

// forwardExternalLinks (POST /api/v1/forward/external/links): the links of a
// 「我的外部节点」 forward, one per entry address.
func (h *Handler) forwardExternalLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.Err(405, "仅支持 POST"))
		return
	}
	var req struct {
		ForwardID int64 `json:"forwardId"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ForwardID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	f, _, _, err := h.resolveForwardAccess(r, req.ForwardID)
	if err != nil || f == nil {
		response.WriteJSON(w, response.ErrDefault("转发不存在"))
		return
	}
	fm := model.Forward{ID: f.ID, Name: f.Name, UserID: f.UserID, TunnelID: f.TunnelID, RemoteAddr: f.RemoteAddr, LandingNodeID: f.LandingNodeID, Mode: f.Mode, ExternalNodeID: f.ExternalNodeID}
	x, err := h.repo.ForwardExternalOf(fm)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if x == nil {
		response.WriteJSON(w, response.ErrDefault("这条转发的目标不是你的外部节点"))
		return
	}
	links, err := h.repo.ForwardExternalLinks(fm, x)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if len(links) == 0 {
		response.WriteJSON(w, response.ErrDefault("这个外部节点的协议不能生成链接"))
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"links": links, "externalNode": externalForwardView(x)}))
}

func (h *Handler) registerForwardExternal(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/forward/external/links", h.forwardExternalLinks)
}

// normalizeForwardImportExternals: a backup restored without its
// landing_external_node rows cannot keep links to them (the ids belong to
// the source panel), so imported forwards become plain port forwards; a
// forward whose target equals one of the owner's 外部节点 here is still
// recognised at read time.
func normalizeForwardImportExternals(raw map[string]interface{}, types []string) {
	if !stringSliceContains(types, "forward") || stringSliceContains(types, "landing_external_node") {
		return
	}
	items, _ := raw["forward"].([]interface{})
	for _, item := range items {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, has := row["external_node_id"]; has {
			row["external_node_id"] = 0
		}
		if _, has := row["externalNodeId"]; has {
			row["externalNodeId"] = 0
		}
	}
}

// SetCNLandingResolver replaces the resolver of the mainland-China target
// check (contract tests / offline demos only; production uses system DNS).
func (h *Handler) SetCNLandingResolver(r cnlanding.Resolver) {
	h.cnLandingChecker = cnlanding.New(r)
}
