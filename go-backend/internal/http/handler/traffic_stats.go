package handler

import (
	"net/http"
	"time"

	"go-backend/internal/http/response"
)

// Plan 212: 主页 traffic cards. Every request is one small read; the user card
// only ever answers for the caller's own id (taken from the token, no input).

// trafficServers = the caller's 「服务器流量」 card (granted servers, 本期已用 /
// 限额 exactly as enforcement computes them). Admins get an empty list.
func (h *Handler) trafficServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	userID, _, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "未登录或token已过期"))
		return
	}
	view, err := h.repo.UserServerTraffic(userID, time.Now())
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// trafficRanking = the admin 「流量排行」 card: 今日 / 本周 / 本月 × 用户 / 服务器.
func (h *Handler) trafficRanking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	view, err := h.repo.TrafficRanking(time.Now())
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// trafficRankingUser = one user's per-server split for one ranking window.
func (h *Handler) trafficRankingUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		UserID int64  `json:"userId"`
		Window string `json:"window"`
	}
	if err := landingDecode(r, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	view, err := h.repo.UserTrafficSplit(req.UserID, req.Window, time.Now())
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(view))
}
