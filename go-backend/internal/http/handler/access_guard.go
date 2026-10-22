package handler

import (
	"net/http"

	"go-backend/internal/http/response"
)

// ensureSelfOrAdmin lets admins act on any user and normal users only on
// themselves; it writes the refusal and returns false otherwise.
func (h *Handler) ensureSelfOrAdmin(w http.ResponseWriter, r *http.Request, userID int64) bool {
	actorID, roleID, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return false
	}
	if roleID != 0 && actorID != userID {
		response.WriteJSON(w, response.Err(403, "权限不足，只能操作自己的账号"))
		return false
	}
	return true
}
