package handler

import (
	"log"
	"net/http"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
)

// Plan 318: 「VPS 厂商」 belongs to the node. node/create requires it (the UI
// and the admin API); node/update requires a non-empty pick whenever the
// request carries `vendors` / `vendorOther` (the UI always does; absent =
// keep, for older API callers). Internal paths (federation remote nodes,
// backup import) create nodes without a vendor, which shows an admin ⚠.

// parseNodeVendorRequest reads `vendors` (array of keys, or a comma list)
// and `vendorOther` (the 「其他（列表里没有）」 name). present = either key is
// in the request.
func parseNodeVendorRequest(req map[string]interface{}) (keys []string, other string, present bool) {
	raw, hasKeys := req["vendors"]
	otherRaw, hasOther := req["vendorOther"]
	if !hasKeys && !hasOther {
		return nil, "", false
	}
	switch v := raw.(type) {
	case []interface{}:
		for _, x := range v {
			if s, ok := x.(string); ok {
				keys = append(keys, s)
			}
		}
	case string:
		keys = strings.Split(v, ",")
	}
	if s, ok := otherRaw.(string); ok {
		other = s
	}
	return keys, other, true
}

// nodeVendorPickFromRequest validates the request's pick; required = an
// absent pick is refused too (node/create).
func (h *Handler) nodeVendorPickFromRequest(req map[string]interface{}, required bool) (pick repo.NodeVendorPick, present bool, err error) {
	keys, other, present := parseNodeVendorRequest(req)
	if !present && !required {
		return repo.NodeVendorPick{}, false, nil
	}
	pick, err = h.repo.NormalizeNodeVendorPick(keys, other)
	return pick, true, err
}

// saveNodeVendors stores the pick and re-syncs the node's native landing when
// it changed (the rule follows the node at once).
func (h *Handler) saveNodeVendors(nodeID int64, pick repo.NodeVendorPick) error {
	serverID, changed, err := h.repo.SetNodeVendors(nodeID, pick)
	if err != nil || !changed {
		return err
	}
	log.Printf("[landing-vendor] node=%d vendors=%q other=%q (landing server %d)", nodeID, strings.Join(pick.Keys, ","), pick.Other, serverID)
	if serverID > 0 {
		h.scheduleLandingLayerApply([]int64{serverID})
	}
	return nil
}

// nodeVendorOptions (admin, node page): the vendor list for the node
// dialog's 「VPS 厂商」 select (names + domains, read-only there).
func (h *Handler) nodeVendorOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	vs, err := h.repo.GetLandingVendorSettings()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	opts := make([]map[string]any, 0, len(vs.Vendors))
	for _, v := range vs.Vendors {
		opts = append(opts, map[string]any{"key": v.Key, "name": v.Name, "domains": v.Domains, "remark": v.Remark})
	}
	response.WriteJSON(w, response.OK(map[string]any{"on": vs.On, "vendors": opts}))
}

// nodeVendorSet (admin): only the node's 「VPS 厂商」 — the same validation
// as the node dialog (required), no other node field is touched (the plan
// 318 backfill of old nodes uses it: one node at a time, hot landing update).
func (h *Handler) nodeVendorSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req map[string]interface{}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	id := asInt64(req["id"], 0)
	node, err := h.repo.GetNodeByID(id)
	if err != nil || node == nil {
		response.WriteJSON(w, response.ErrDefault("节点不存在"))
		return
	}
	pick, _, err := h.nodeVendorPickFromRequest(req, true)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	serverID, changed, err := h.repo.SetNodeVendors(id, pick)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if changed {
		log.Printf("[landing-vendor] node=%d vendors=%q other=%q (landing server %d)", id, strings.Join(pick.Keys, ","), pick.Other, serverID)
		if serverID > 0 {
			h.scheduleLandingLayerApply([]int64{serverID})
		}
	}
	response.WriteJSON(w, response.OK(map[string]any{"id": id, "vendors": pick.Keys, "vendorOther": pick.Other, "changed": changed, "landingServerId": serverID}))
}
