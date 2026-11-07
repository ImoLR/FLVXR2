package handler

import (
	"errors"
	"log"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/repo"
)

// Plan 234 「自动切换门槛」 (网站配置 card, admin only): the three automatic
// switch margins and how often each switched in the last 7 days.
//
//   - 最优线路 (+ 规则最优): read by every 60 s path round from the repo cache;
//   - Reality 伪装域名: read by every 测速 / server view;
//   - Reality 目标 IP: sent to native agents (landing_reality ≥ 3) with the
//     landing desired state by the 1-minute reconcile push, and to shares
//     whose Helper takes it (reality_pin v2) with the shared desired state.
//
// Changes apply without a restart.

func (h *Handler) registerSwitchMargins(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/config/switch-margins", h.switchMarginsGet)
	mux.HandleFunc("/api/v1/config/switch-margins/save", h.switchMarginsSave)
}

// bestSwitchMargin is the 最优 / 规则最优 margin of a round.
func (h *Handler) bestSwitchMargin() float64 {
	if h == nil || h.repo == nil {
		return bestPathSwitchMarginMS
	}
	return float64(h.repo.SwitchMargins().BestMs)
}

// recordAutoSwitch counts rare switches (测速 picks, IP pins) and persists.
func (h *Handler) recordAutoSwitch(events ...repo.AutoSwitchEvent) {
	if h == nil || h.repo == nil || len(events) == 0 {
		return
	}
	if err := h.repo.RecordAutoSwitch(events...); err != nil {
		log.Printf("[switch-margins] counts not saved: %v", err)
	}
}

// flushAutoSwitches persists the switches the path round noted (no-op when none).
func (h *Handler) flushAutoSwitches() {
	if h == nil || h.repo == nil {
		return
	}
	if err := h.repo.FlushAutoSwitches(); err != nil {
		log.Printf("[switch-margins] counts not saved: %v", err)
	}
}

type switchMarginRowView struct {
	Key       string                 `json:"key"`
	ValueMs   int                    `json:"valueMs"`
	DefaultMs int                    `json:"defaultMs"`
	Counts    repo.AutoSwitchSummary `json:"counts"`
	BusyName  string                 `json:"busyName,omitempty"`
}

func (h *Handler) switchMarginsView(now time.Time) map[string]any {
	m := h.repo.SwitchMargins()
	d := repo.DefaultSwitchMargins()
	counts := h.repo.AutoSwitchSummaries(now)
	rows := []switchMarginRowView{
		{Key: "best", ValueMs: m.BestMs, DefaultMs: d.BestMs, Counts: counts[repo.AutoSwitchBest]},
		{Key: "realityDomain", ValueMs: m.RealityDomainMs, DefaultMs: d.RealityDomainMs, Counts: counts[repo.AutoSwitchRealityDomain]},
		{Key: "realityIp", ValueMs: m.RealityIPMs, DefaultMs: d.RealityIPMs, Counts: counts[repo.AutoSwitchRealityIP]},
	}
	for i := range rows {
		id := rows[i].Counts.BusyUnit
		if id <= 0 {
			continue
		}
		if rows[i].Key == "best" {
			if name, err := h.repo.GetTunnelName(id); err == nil && name != "" {
				rows[i].BusyName = h.repo.RenderTunnelNamesAdmin(map[int64]string{id: name})[id]
			}
		} else if s, err := h.repo.GetLandingServer(id); err == nil && s != nil {
			rows[i].BusyName = s.Name
		}
	}
	return map[string]any{
		"rows":          rows,
		"ipSupport":     h.realityPinMarginSupport(),
		"maxMs":         repo.SwitchMarginMaxMs,
		"confirmRounds": bestPathConfirmRounds,
		"windowDays":    repo.AutoSwitchWindowDays,
		"busyPerWindow": repo.AutoSwitchBusyPerWindow,
	}
}

// realityPinMarginSupport counts the landing servers that take the 目标 IP
// margin (native agent landing_reality ≥ 3, share reality_pin v2) and the
// ones still on the built-in 20 ms (older agent / Helper). Stopped servers
// are left out.
func (h *Handler) realityPinMarginSupport() map[string]int {
	out := map[string]int{"native": 0, "nativeOld": 0, "shared": 0, "sharedOld": 0}
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		return out
	}
	for i := range servers {
		s := &servers[i]
		if s.Status != "active" {
			continue
		}
		if s.Source == "shared" {
			if repo.SharedLandingCaps(s).RealityPin.Supports(landingcustom.RealityPinMarginVersion) {
				out["shared"]++
			} else {
				out["sharedOld"]++
			}
			continue
		}
		if node, err := h.repo.GetNodeByID(s.NodeID); err == nil && node != nil && node.LandingReality >= repo.LandingRealityPinMarginVersion {
			out["native"]++
		} else {
			out["nativeOld"]++
		}
	}
	return out
}

func (h *Handler) switchMarginsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	response.WriteJSON(w, response.OK(h.switchMarginsView(time.Now())))
}

func (h *Handler) switchMarginsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		BestMs          *int `json:"bestMs"`
		RealityDomainMs *int `json:"realityDomainMs"`
		RealityIPMs     *int `json:"realityIpMs"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if req.BestMs == nil || req.RealityDomainMs == nil || req.RealityIPMs == nil {
		response.WriteJSON(w, response.ErrDefault("请填写三个门槛"))
		return
	}
	prev := h.repo.SwitchMargins()
	next := repo.SwitchMargins{BestMs: *req.BestMs, RealityDomainMs: *req.RealityDomainMs, RealityIPMs: *req.RealityIPMs}
	if err := next.Validate(); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	if err := h.repo.SetSwitchMargins(next); err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if prev != next {
		log.Printf("[switch-margins] saved best=%d ms (was %d) reality_domain=%d ms (was %d) reality_ip=%d ms (was %d)",
			next.BestMs, prev.BestMs, next.RealityDomainMs, prev.RealityDomainMs, next.RealityIPMs, prev.RealityIPMs)
	}
	response.WriteJSON(w, response.OK(h.switchMarginsView(time.Now())))
}

var errAutoSwitchCountsKey = errors.New("该配置由面板自动维护，不能直接修改")
