package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 227: Reality 伪装域名 — candidate list (内置 / 自定义 / 妙妙屋域名池,
// 屏蔽), 测速 from the landing machine (native agent RealityProbe, shared
// Helper via Custom), 自动（最快） pick for NEW nodes, daily re-测速, pins.

const (
	realityProbeEvery    = 24 * time.Hour
	realityProbeRetry    = time.Hour
	realityPoolEvery     = 6 * time.Hour
	realityPoolRetry     = time.Hour
	realityProbeTimeout  = 110 * time.Second
	realityProbeTimeoutM = 3000 // ms per candidate TLS handshake

	realityPoolAtKey    = "landing_reality_pool_at"
	realityPoolErrorKey = "landing_reality_pool_error"

	realitySharedUnsupported = "共享服务器需要妙妙屋升级后才能测速"
)

var (
	realityProbeBusy     sync.Map // server id -> struct{}: one 测速 per server at a time
	realityProbeAttempts sync.Map // server id -> time.Time of the last failed automatic 测速
	realityPoolMu        sync.Mutex
	realityPoolTried     time.Time
)

func (h *Handler) registerLandingReality(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{
		"reality/domains":       h.landingRealityDomains,
		"reality/domain/add":    h.landingRealityDomainAdd,
		"reality/domain/delete": h.landingRealityDomainDelete,
		"reality/domain/block":  h.landingRealityDomainBlock,
		"reality/pool/refresh":  h.landingRealityPoolRefresh,
		"server/reality-probe":  h.landingServerRealityProbe,
	} {
		fn := fn
		mux.HandleFunc("/api/v1/landing/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if !h.ensureAdminAccess(w, r) {
				return
			}
			fn(w, r)
		})
	}
}

func (h *Handler) realityConfig(name string) string {
	v, _ := h.repo.GetViteConfigValue(name)
	return v
}

func (h *Handler) realityDomainsView() (map[string]any, error) {
	rows, err := h.repo.ListLandingRealityDomains()
	if err != nil {
		return nil, err
	}
	probed := 0
	for _, d := range rows {
		if !d.Blocked && probed < repo.RealityProbeMaxCandidates {
			probed++
		}
	}
	poolAt, _ := strconv.ParseInt(h.realityConfig(realityPoolAtKey), 10, 64)
	cfg, _ := h.repo.GetLandingIntegration()
	return map[string]any{
		"domains":       rows,
		"probeCount":    probed,
		"probeMax":      repo.RealityProbeMaxCandidates,
		"customMax":     repo.RealityCustomMax,
		"poolFetchedAt": poolAt,
		"poolError":     h.realityConfig(realityPoolErrorKey),
		"poolAvailable": cfg.Token != "",
	}, nil
}

func (h *Handler) landingRealityDomains(w http.ResponseWriter, r *http.Request) {
	view, err := h.realityDomainsView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

func (h *Handler) landingRealityDomainAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if _, err := h.repo.AddLandingRealityDomain(req.Domain); err != nil {
		landingError(w, err)
		return
	}
	h.landingRealityDomains(w, r)
}

func (h *Handler) landingRealityDomainDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.DeleteLandingRealityDomain(req.ID); err != nil {
		landingError(w, err)
		return
	}
	h.landingRealityDomains(w, r)
}

func (h *Handler) landingRealityDomainBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      int64 `json:"id"`
		Blocked bool  `json:"blocked"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if _, err := h.repo.SetLandingRealityDomainBlocked(req.ID, req.Blocked); err != nil {
		landingError(w, err)
		return
	}
	h.landingRealityDomains(w, r)
}

func (h *Handler) landingRealityPoolRefresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := h.refreshRealityPool(ctx, true); err != nil {
		landingError(w, err)
		return
	}
	h.landingRealityDomains(w, r)
}

// refreshRealityPool reads 妙妙屋's 共享域名池 (cached ≥ 6 h; failures retried
// hourly). An unavailable pool never blocks anything: the reason is shown.
func (h *Handler) refreshRealityPool(ctx context.Context, force bool) error {
	realityPoolMu.Lock()
	defer realityPoolMu.Unlock()
	cfg, err := h.repo.GetLandingIntegration()
	if err != nil || cfg.Token == "" {
		if force {
			return errors.New("未配置妙妙屋接入")
		}
		return nil
	}
	if !force {
		at, _ := strconv.ParseInt(h.realityConfig(realityPoolAtKey), 10, 64)
		if time.Since(time.UnixMilli(at)) < realityPoolEvery || time.Since(realityPoolTried) < realityPoolRetry {
			return nil
		}
	}
	realityPoolTried = time.Now()
	client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
	if err != nil {
		return err
	}
	pool, err := client.RealityPool(ctx)
	if err != nil {
		msg := err.Error()
		var apiErr *landingcustom.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusMethodNotAllowed) {
			msg = "妙妙屋版本还不支持域名池，升级后会自动读取"
		}
		_ = h.repo.UpsertConfig(realityPoolErrorKey, msg, time.Now().UnixMilli())
		if force {
			return errors.New(msg)
		}
		return nil
	}
	domains := make([]string, 0, len(pool.Domains))
	for _, d := range pool.Domains {
		domains = append(domains, d.Domain)
	}
	added, err := h.repo.ReplaceLandingRealityPool(domains)
	if err != nil {
		return err
	}
	_ = h.repo.UpsertConfig(realityPoolAtKey, strconv.FormatInt(time.Now().UnixMilli(), 10), time.Now().UnixMilli())
	_ = h.repo.UpsertConfig(realityPoolErrorKey, pool.Reason, time.Now().UnixMilli())
	if added > 0 {
		log.Printf("landing reality pool: %d domains (%d new)", len(domains), added)
	}
	return nil
}

// realityProbeSupport says whether a 测速 can run on the server now and why not.
func (h *Handler) realityProbeSupport(s *model.LandingServer) (bool, string) {
	if s == nil {
		return false, "落地服务器不存在"
	}
	if s.Status != "active" {
		return false, "服务器已停用"
	}
	if s.Source == "shared" {
		caps := repo.SharedLandingCaps(s)
		if caps.RealityProbe.Supports(landingcustom.RealityProbeVersion) {
			return true, ""
		}
		return false, realitySharedUnsupported
	}
	node, err := h.repo.GetNodeByID(s.NodeID)
	if err != nil || node == nil {
		return false, "节点不存在"
	}
	if node.LandingReality < 1 {
		return false, "节点程序版本过旧，升级节点后才能测速"
	}
	if node.Status != 1 || !h.nodeOnline(node.ID) {
		return false, "节点离线，恢复在线后才能测速"
	}
	return true, ""
}

// realityPinSupport: does this server report Reality IP pins?
func (h *Handler) realityPinSupport(s *model.LandingServer) bool {
	if s.Source == "shared" {
		return repo.SharedLandingCaps(s).RealityPin.Supports(landingcustom.RealityPinVersion)
	}
	node, err := h.repo.GetNodeByID(s.NodeID)
	return err == nil && node != nil && node.LandingReality >= 1
}

// realityProbeTargets = the non-blocked candidates, plus the server's
// current dest when it is not in the list (so its numbers show too).
func (h *Handler) realityProbeTargets(s *model.LandingServer) ([]string, error) {
	targets, err := h.repo.RealityProbeTargets()
	if err != nil {
		return nil, err
	}
	cur := s.RealityDest
	seen := false
	for _, t := range targets {
		seen = seen || t == cur
	}
	if !seen && cur != "" && len(targets) < repo.RealityProbeMaxCandidates && !h.repo.RealityDomainBlocked(realityHostOf(cur)) {
		targets = append(targets, cur)
	}
	if len(targets) == 0 {
		return nil, errors.New("没有可测速的伪装域名（全部被屏蔽了）")
	}
	return targets, nil
}

func realityHostOf(target string) string {
	if h, _, err := net.SplitHostPort(target); err == nil {
		return h
	}
	return target
}

// runRealityProbe runs one 测速 on the server's machine and records it
// (auto mode: the fastest usable non-blocked candidate becomes the default
// for new nodes). One 测速 per server at a time.
func (h *Handler) runRealityProbe(ctx context.Context, serverID int64) ([]repo.RealityProbeResult, repo.RealityProbeRecord, error) {
	var rec repo.RealityProbeRecord
	if _, busy := realityProbeBusy.LoadOrStore(serverID, struct{}{}); busy {
		return nil, rec, errors.New("这台服务器正在测速，请稍候")
	}
	defer realityProbeBusy.Delete(serverID)
	s, err := h.repo.GetLandingServer(serverID)
	if err != nil || s == nil {
		return nil, rec, errors.New("落地服务器不存在")
	}
	if ok, why := h.realityProbeSupport(s); !ok {
		return nil, rec, errors.New(why)
	}
	targets, err := h.realityProbeTargets(s)
	if err != nil {
		return nil, rec, err
	}
	var results []repo.RealityProbeResult
	if s.Source == "shared" {
		results, err = h.realityProbeShared(ctx, s, targets)
	} else {
		results, err = h.realityProbeNative(s, targets)
	}
	if err != nil {
		_, _ = h.repo.RecordLandingRealityProbe(serverID, nil, err.Error())
		return nil, rec, err
	}
	rec, err = h.repo.RecordLandingRealityProbe(serverID, results, "")
	if err != nil {
		return nil, rec, err
	}
	if rec.Picked != "" {
		log.Printf("landing reality server=%d auto dest %s → %s (new nodes only)", serverID, rec.Previous, rec.Picked)
	}
	return results, rec, nil
}

func (h *Handler) realityProbeNative(s *model.LandingServer, targets []string) ([]repo.RealityProbeResult, error) {
	res, err := h.sendNodeCommandWithTimeout(s.NodeID, "RealityProbe", map[string]any{"candidates": targets, "timeout_ms": realityProbeTimeoutM}, realityProbeTimeout, false, false)
	if err != nil {
		return nil, errors.New("测速失败：" + err.Error())
	}
	if !res.Success {
		return nil, errors.New("测速失败：" + res.Message)
	}
	raw, _ := json.Marshal(res.Data)
	var out struct {
		Results []repo.RealityProbeResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("测速结果格式错误")
	}
	return out.Results, nil
}

func (h *Handler) realityProbeShared(ctx context.Context, s *model.LandingServer, targets []string) ([]repo.RealityProbeResult, error) {
	cfg, err := h.repo.GetLandingIntegration()
	if err != nil {
		return nil, errors.New("接入设置读取失败")
	}
	client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, realityProbeTimeout)
	defer cancel()
	list, err := client.ProbeReality(pctx, s.RemoteServerID, landingcustom.RealityProbeRequest{Candidates: targets, TimeoutMs: realityProbeTimeoutM})
	if err != nil {
		return nil, errors.New("妙妙屋测速失败：" + err.Error())
	}
	raw, _ := json.Marshal(list)
	var out []repo.RealityProbeResult
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// realityResultsView annotates a 测速 table with each domain's source and
// block state (admin view).
func (h *Handler) realityResultsView(results []repo.RealityProbeResult) []map[string]any {
	rows, _ := h.repo.ListLandingRealityDomains()
	byDomain := map[string]model.LandingRealityDomain{}
	for _, d := range rows {
		byDomain[d.Domain] = d
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		host := realityHostOf(r.Target)
		d, known := byDomain[host]
		source := "current"
		if known {
			source = d.Source
		}
		out = append(out, map[string]any{
			"target": r.Target, "domain": host, "feasible": r.Feasible, "reason": r.Reason, "latencyMs": r.LatencyMs,
			"bestIp": r.BestIP, "ips": r.IPs, "serverNames": repo.RealityServerNamesFor(host, r.ServerNames),
			"source": source, "inPool": known && d.InPool, "blocked": known && d.Blocked,
		})
	}
	return out
}

// landingServerRealityView is the admin part of a server view.
func (h *Handler) landingServerRealityView(s model.LandingServer, v map[string]any) {
	ok, why := h.realityProbeSupport(&s)
	v["realityMode"] = repo.NormalizeRealityMode(s.RealityMode)
	v["realityProbeSupported"] = ok
	v["realityProbeUnsupportedReason"] = why
	v["realityProbedAt"] = s.RealityProbedAt
	v["realityProbeError"] = s.RealityProbeError
	v["realityProbe"] = h.realityResultsView(repo.LandingRealityProbeResults(&s))
	v["realityPinSupported"] = h.realityPinSupport(&s)
	v["realityPins"] = repo.LandingRealityPins(&s)
	v["realityPinsAt"] = s.RealityPinsAt
}

func (h *Handler) landingServerRealityProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	_, rec, err := h.runRealityProbe(r.Context(), req.ID)
	if err != nil {
		landingError(w, err)
		return
	}
	saved, _ := h.repo.GetLandingServer(req.ID)
	if saved == nil {
		landingError(w, errors.New("落地服务器不存在"))
		return
	}
	view := h.landingServerView(*saved)
	view["realityPicked"] = rec.Picked
	response.WriteJSON(w, response.OK(view))
}

// runRealityProbeLoop: daily 测速 of servers in 自动（最快）, one server per
// 10-minute tick (paced; ≤ 64 candidates, ≤ 4 at once on the machine), plus
// the 6-hourly pool refresh.
func (h *Handler) runRealityProbeLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = h.refreshRealityPool(pctx, false)
		cancel()
		h.realityProbeDue(ctx)
		timer.Reset(10 * time.Minute)
	}
}

func (h *Handler) realityProbeDue(ctx context.Context) {
	due, err := h.repo.RealityAutoDueServers(realityProbeEvery)
	if err != nil {
		return
	}
	for i := range due {
		s := due[i]
		if ok, _ := h.realityProbeSupport(&s); !ok {
			continue
		}
		if t, ok := realityProbeAttempts.Load(s.ID); ok && time.Since(t.(time.Time)) < realityProbeRetry {
			continue
		}
		if _, _, err := h.runRealityProbe(ctx, s.ID); err != nil {
			realityProbeAttempts.Store(s.ID, time.Now())
			log.Printf("landing reality server=%d daily probe failed: %v", s.ID, err)
		} else {
			realityProbeAttempts.Delete(s.ID)
		}
		return // one server per tick
	}
}

// recordRealityPins stores pins reported with a native apply / shared status.
func (h *Handler) recordRealityPins(serverID int64, pins []repo.RealityPin) {
	if pins == nil {
		return
	}
	if err := h.repo.RecordLandingRealityPins(serverID, pins); err != nil {
		log.Printf("landing reality pins not recorded server=%d: %v", serverID, err)
	}
}

func sharedRealityPins(p landingcustom.RealityPins) []repo.RealityPin {
	if p == nil {
		return nil
	}
	out := make([]repo.RealityPin, 0, len(p))
	for _, pin := range p {
		out = append(out, repo.RealityPin{Host: pin.Host, Port: pin.Port, PinnedIP: pin.PinnedIP, PinnedMs: pin.PinnedMs, BestIP: pin.BestIP, BestMs: pin.BestMs, CheckedAt: pin.CheckedAt, Error: pin.Error})
	}
	return out
}
