package handler

import (
	"net/http"

	"go-backend/internal/http/response"
)

// Plan 184 「显示地区」: POST /api/v1/tunnel/region {id, regionOverride} —
// admin only. Sets the region users see the tunnel under (” = automatic,
// exit-node region; entry region for 端口转发). Display only: one column
// write, no runtime push — /tunnel/update would redeploy the tunnel and every
// rule on it (live connections drop, see plan 155), so a region-only change
// from the edit form and the release data step come here.
func (h *Handler) tunnelRegionUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req map[string]interface{}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	id := asInt64(req["id"], 0)
	if id <= 0 {
		response.WriteJSON(w, response.ErrDefault("隧道ID不能为空"))
		return
	}
	if _, ok := req["regionOverride"]; !ok {
		response.WriteJSON(w, response.ErrDefault("缺少显示地区（自动请传空字符串）"))
		return
	}
	code, err := tunnelRegionOverrideFromRequest(req, "")
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	found, err := h.repo.SetTunnelRegionOverrideTx(nil, id, code)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if !found {
		// RowsAffected is 0 for an unchanged value on some drivers: confirm the tunnel exists.
		if tr, getErr := h.repo.GetTunnelRecord(id); getErr != nil || tr == nil {
			response.WriteJSON(w, response.ErrDefault("隧道不存在"))
			return
		}
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": id, "regionOverride": code}))
}
