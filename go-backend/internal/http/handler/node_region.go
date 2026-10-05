package handler

import (
	"context"
	"log"
	"net/http"
	"strings"

	"go-backend/internal/geoip"
	"go-backend/internal/http/response"
)

func (h *Handler) nodeDetectRegion(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		IP         string `json:"ip"`
		ServerIPV4 string `json:"serverIpV4"`
		ServerIP   string `json:"serverIp"`
		ServerIPV6 string `json:"serverIpV6"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if strings.TrimSpace(req.ServerIP) == "" {
		req.ServerIP = req.IP
	}
	response.WriteJSON(w, response.OK(geoip.DetectNodeAddresses(req.ServerIPV4, req.ServerIP, req.ServerIPV6)))
}

func (h *Handler) runNodeRegionBackfill(ctx context.Context) {
	defer h.jobsWG.Done()
	if err := h.repo.BackfillNodeRegions(ctx, geoip.DetectNodeAddresses); err != nil && ctx.Err() == nil {
		log.Printf("[node-region] backfill failed: %v", err)
	}
}
