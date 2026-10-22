package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 131: global default for the per-user total landing-node cap and the
// admin owner change of an existing landing node.

// landingUserSaveMaxNodes maps the user dialog's cap choice onto the repo
// value. "global" follows the global default; a numeric maxNodes (with
// "custom" or, from older clients, no source) sets an own value; neither
// keeps what the user has.
func (h *Handler) landingUserSaveMaxNodes(userID int64, source string, maxNodes *int) (int, error) {
	switch source {
	case "global":
		return repo.LandingMaxNodesFollowGlobal, nil
	case "", "custom":
	default:
		return 0, errors.New("落地设置无效")
	}
	if maxNodes != nil {
		if *maxNodes < 0 {
			return 0, errors.New("落地设置无效")
		}
		return *maxNodes, nil
	}
	if source == "custom" {
		return 0, errors.New("请填写可建落地节点总数")
	}
	current, _, _, err := h.repo.GetLandingUserSetting(userID)
	if err != nil {
		return 0, err
	}
	if !current.MaxNodesCustom {
		return repo.LandingMaxNodesFollowGlobal, nil
	}
	return current.MaxNodes, nil
}

func (h *Handler) landingSettingsView() (map[string]interface{}, error) {
	n, err := h.repo.LandingDefaultMaxNodes()
	if err != nil {
		return nil, err
	}
	custom, err := h.repo.LandingCustomMaxNodesUserCount()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"defaultMaxNodes": n, "customUserCount": custom}, nil
}

func (h *Handler) landingSettingsGet(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	view, err := h.landingSettingsView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

func (h *Handler) landingSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		DefaultMaxNodes *int `json:"defaultMaxNodes"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.DefaultMaxNodes == nil {
		landingError(w, errors.New("请填写每个用户默认可建落地节点总数"))
		return
	}
	if err := h.repo.SetLandingDefaultMaxNodes(*req.DefaultMaxNodes); err != nil {
		landingError(w, err)
		return
	}
	view, err := h.landingSettingsView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// landingNodeTransfer changes the owner of a landing node (admin only).
// userId <= 0 means the calling admin.
func (h *Handler) landingNodeTransfer(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	adminID, _, _ := userRoleFromRequest(r)
	var req struct {
		ID     int64 `json:"id"`
		UserID int64 `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.UserID <= 0 {
		req.UserID = adminID
	}
	n, err := h.repo.GetLandingNode(req.ID)
	if err != nil {
		landingError(w, err)
		return
	}
	if n == nil {
		landingError(w, errors.New("落地节点不存在"))
		return
	}
	if n.UserID == req.UserID {
		landingError(w, errors.New("节点已属于该用户"))
		return
	}
	// Bill what the node carried so far to the old owner before the owner
	// changes; flow ingestion attributes later counters to the new owner.
	enforceBefore := h.flushLandingCountersBeforeTransfer(r.Context(), n)
	result, err := h.repo.TransferLandingNode(req.ID, req.UserID)
	if enforceBefore != nil {
		enforceBefore()
	}
	if err != nil {
		landingError(w, err)
		return
	}
	log.Printf("[landing-transfer] node=%d server=%d from=%d(%s) to=%d(%s) by=%d", result.Node.ID, result.Node.LandingServerID, result.FromUserID, result.FromUser, result.Node.UserID, result.ToUser, adminID)
	// The new owner's grants, expiry and quotas decide the node's state; the
	// shared desired state carries the owner, so push it at once.
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(result.Node.LandingServerID)
	saved, err := h.repo.GetLandingNode(result.Node.ID)
	if err != nil || saved == nil {
		saved = &result.Node
	}
	response.WriteJSON(w, response.OK(h.landingNodeView(*saved)))
}

// flushLandingCountersBeforeTransfer collects the node's current counters
// (best effort: an offline node or an unreachable Custom just skips it; the
// next report is then attributed to the new owner). Returns the quota
// enforcement to run once no lock is held.
func (h *Handler) flushLandingCountersBeforeTransfer(ctx context.Context, n *model.LandingNode) func() {
	s, err := h.repo.GetLandingServer(n.LandingServerID)
	if err != nil || s == nil || s.ChainOnly || n.DeletePending {
		return nil
	}
	if s.Source == "shared" {
		cfg, err := h.repo.GetLandingIntegration()
		if err != nil || cfg.Token == "" {
			return nil
		}
		client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
		if err != nil {
			return nil
		}
		h.sharedLandingMu.Lock()
		defer h.sharedLandingMu.Unlock()
		enforce, err := h.pollSharedLanding(ctx, client, *s)
		if err != nil {
			log.Printf("[landing-transfer] node=%d pre-transfer shared counters skipped: %v", n.ID, err)
		}
		return enforce
	}
	snapshot, err := h.syncLandingServer(s.ID)
	if err != nil || snapshot == nil {
		log.Printf("[landing-transfer] node=%d pre-transfer counters skipped: %v", n.ID, err)
		return nil
	}
	for _, c := range snapshot.Nodes {
		if c.ID == n.ID && c.InboundTag == n.InboundTag && c.Epoch != "" {
			enforce, err := h.commitFlowWithLanding(snapshot.NodeID, nil, []repo.LandingCounter{c})
			if err != nil {
				log.Printf("[landing-transfer] node=%d pre-transfer counters not billed: %v", n.ID, err)
				return nil
			}
			return enforce
		}
	}
	return nil
}
