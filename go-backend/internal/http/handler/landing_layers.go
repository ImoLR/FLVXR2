package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 134: layered landing routing — the global rule group (admin), node
// rules (node owner), the debounced push after global saves and the status
// views. Order per node: 内置 → 全局·强制 → 节点规则 → 服务器 → 全局·默认 →
// 默认出站 (see landingegress.CompileLayers).

func mergeIDs(a, b []int64) []int64 {
	set := map[int64]bool{}
	out := []int64{}
	for _, id := range append(append([]int64{}, a...), b...) {
		if !set[id] {
			set[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// landingUsedActions lists the actions a server's plan actually references
// (profiles always; global layers and node rules only where they are sent).
func landingUsedActions(d *repo.LandingEgressData) []string {
	out := []string{}
	for _, n := range d.Nodes {
		pid := landingegress.EffectiveProfile(d.Server.RouteProfileID, n.RouteProfileID)
		if p, ok := d.Profiles[pid]; pid != 0 && ok {
			out = append(out, landingegress.Actions(p.Rules, p.DefaultAction)...)
		}
		if landingNodeRulesReady(d) {
			rules, def := repo.NodeRulesFromModel(n)
			out = append(out, landingegress.Actions(rules, def)...)
		}
	}
	if landingAbuseReady(d) && d.Global.Active() {
		out = append(out, landingegress.Actions(append(append([]landingegress.Rule{}, d.Global.Forced...), d.Global.Default...), d.Global.DefaultAction)...)
	}
	return out
}

// compileLandingReport compiles a server for status views and save checks.
// mutate applies a candidate change first.
func (h *Handler) compileLandingReport(d *repo.LandingEgressData, mutate func(*repo.LandingEgressData), forceNodeRules bool) (*landingegress.Plan, landingegress.Report, error) {
	if mutate != nil {
		mutate(d)
	}
	_, geoIP, geoSite, _ := landingEgressSupport(d)
	in := landingEgressInput(d)
	in.GeoIP, in.GeoSite = geoIP, geoSite
	in.Exempt = h.landingExemptSources // cached; status hashes must match the pushed plan
	if forceNodeRules {
		// Save checks: judge capacity as if the Core could take them now.
		in.NodeRules = true
	}
	return landingegress.CompileLayers(in)
}

// ---- Debounced push after global saves --------------------------------

// layerApplyState pushes every landing server once after (a burst of) global
// rule saves: saves within the delay push the timer out, a save during a run
// queues exactly one more run. No goroutine exists while idle.
type layerApplyState struct {
	mu       sync.Mutex
	timer    *time.Timer
	running  bool
	pending  map[int64]bool
	inflight map[int64]bool   // taken by the current run, not pushed yet
	result   map[int64]string // last push error per server ("" = ok)
	delay    time.Duration    // tests shorten it
}

const layerApplyDelay = 3 * time.Second

func (h *Handler) scheduleLandingLayerApply(ids []int64) {
	st := &h.layerApply
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pending == nil {
		st.pending, st.result = map[int64]bool{}, map[int64]string{}
	}
	for _, id := range ids {
		st.pending[id] = true
	}
	if st.running {
		return // the run re-arms itself for the new marks
	}
	delay := st.delay
	if delay <= 0 {
		delay = layerApplyDelay
	}
	if st.timer != nil {
		if st.timer.Stop() {
			st.timer.Reset(delay)
		}
		// Already fired: the run is about to take the marks.
		return
	}
	h.egressSyncWG.Add(1)
	st.timer = time.AfterFunc(delay, h.runLandingLayerApply)
}

func (h *Handler) runLandingLayerApply() {
	defer h.egressSyncWG.Done()
	st := &h.layerApply
	st.mu.Lock()
	st.timer, st.running = nil, true
	ids := make([]int64, 0, len(st.pending))
	for id := range st.pending {
		ids = append(ids, id)
	}
	st.pending = map[int64]bool{}
	st.inflight = map[int64]bool{}
	for _, id := range ids {
		st.inflight[id] = true
	}
	st.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		_, err := h.syncLandingServer(id)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		st.mu.Lock()
		st.result[id] = msg
		delete(st.inflight, id)
		st.mu.Unlock()
	}
	st.mu.Lock()
	st.running = false
	again := len(st.pending) > 0
	st.mu.Unlock()
	if again {
		h.scheduleLandingLayerApply(nil)
	}
}

func (h *Handler) landingLayerApplyState(id int64) (pending bool, lastErr string) {
	st := &h.layerApply
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.pending[id] || st.inflight[id], st.result[id]
}

// ---- Status --------------------------------------------------------------

// landingLayerServerStatus is one server's row in the 全局规则 section:
// state synced | syncing | failed | restart | unsupported, plus yellow notes
// (global parts skipped on this server, node rules not applied).
func (h *Handler) landingLayerServerStatus(s model.LandingServer) map[string]any {
	out := map[string]any{"serverId": s.ID, "serverName": s.Name, "source": s.Source}
	d, err := h.repo.LandingEgressData(s.ID)
	if err != nil || d == nil {
		out["state"], out["reason"] = "failed", "读取落地服务器失败"
		return out
	}
	supported, _, _, reason := landingEgressSupport(d)
	if !supported {
		out["state"], out["reason"] = "unsupported", reason
		return out
	}
	notes := []string{}
	if _, rep, err := h.compileLandingReport(d, nil, false); err == nil {
		notes = append(notes, rep.GlobalNotes...)
		if len(rep.NodeNotes) > 0 {
			notes = append(notes, fmt.Sprintf("%d 个节点的节点规则未完全生效", len(rep.NodeNotes)))
		}
	} else {
		notes = append(notes, err.Error())
	}
	if d.Global.Active() && d.Server.Source == "native" && d.NodeGuard < landingegress.LayersVersion && len(d.ChainTags) > 0 && len(d.Global.Forced) > 0 {
		notes = append(notes, "线路机节点需要升级节点程序后才应用全局·强制规则")
	}
	out["notes"] = notes
	pending, lastErr := h.landingLayerApplyState(s.ID)
	switch {
	case s.Source == "native" && !landingAbuseReady(d):
		out["state"], out["reason"] = "restart", "该服务器的落地 Core 需要在服务器设置中确认重启一次后才能应用"
	case pending:
		out["state"] = "syncing"
	case lastErr != "":
		out["state"], out["reason"] = "failed", lastErr
	case s.EgressState == "error":
		out["state"], out["reason"] = "failed", s.EgressError
	case s.Source == "shared" && s.SharedSyncError != "":
		out["state"], out["reason"] = "failed", s.SharedSyncError
	case s.Source == "shared" && s.SharedPushedRevision != s.SharedAppliedRevision:
		out["state"] = "syncing"
	default:
		out["state"] = "synced"
	}
	if s.Source == "native" {
		if n, err := h.repo.GetNodeByID(s.NodeID); err == nil && n != nil && n.Status != 1 && out["state"] == "synced" {
			out["state"], out["reason"] = "failed", "节点离线，恢复在线后自动同步"
		}
	}
	return out
}

func ruleSummaries(rules []landingegress.Rule, outbounds map[int64]landingegress.Outbound, admin bool) []map[string]any {
	out := []map[string]any{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		label := landingegress.ActionLabel(r.Action, outbounds)
		if admin {
			if id, ok := landingegress.ActionOutbound(r.Action); ok {
				if o, ok := outbounds[id]; ok {
					label = o.Name
				}
			}
		}
		out = append(out, map[string]any{"id": r.ID, "name": r.Name, "action": r.Action, "actionLabel": label, "domains": len(r.Domains), "ips": len(r.IPs), "ports": r.Ports, "sourceIps": len(r.SourceIPs), "network": r.Network, "protocols": r.Protocols})
	}
	return out
}

// landingGlobalView is the admin 全局规则 section.
func (h *Handler) landingGlobalView() (map[string]any, error) {
	g, abuse, updated, err := h.repo.GetLandingGlobalSettings()
	if err != nil {
		return nil, err
	}
	builtin, err := h.landingBuiltinAdminView(abuse)
	if err != nil {
		return nil, err
	}
	perNode, total, err := h.repo.LandingRuleCapDefaults()
	if err != nil {
		return nil, err
	}
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		return nil, err
	}
	status := []map[string]any{}
	for _, s := range servers {
		if s.ChainOnly {
			continue
		}
		status = append(status, h.landingLayerServerStatus(s))
	}
	withRules, _ := h.repo.LandingNodeRuleNodes()
	return map[string]any{
		"forced": g.Forced, "default": g.Default, "defaultAction": g.DefaultAction, "updatedTime": updated,
		"userCaps": map[string]any{"perNode": perNode, "total": total}, "nodesWithRules": withRules, "servers": status,
		"builtin": builtin,
	}, nil
}

func (h *Handler) landingEgressGlobalGet(w http.ResponseWriter, r *http.Request) {
	view, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// landingEgressGlobalSave validates the global group against every server
// (hard errors refuse, skipped parts are returned as warnings), stores it and
// pushes every server once (debounced).
func (h *Handler) landingEgressGlobalSave(w http.ResponseWriter, r *http.Request) {
	var g landingegress.Global
	if err := landingDecode(r, &g); err != nil {
		landingError(w, err)
		return
	}
	outs, _, err := h.repo.LandingEgressCatalog()
	if err != nil {
		landingError(w, err)
		return
	}
	ids := map[int64]bool{}
	for id := range outs {
		ids[id] = true
	}
	candidate := g
	candidate.Forced = append([]landingegress.Rule(nil), g.Forced...)
	candidate.Default = append([]landingegress.Rule(nil), g.Default...)
	if err := landingegress.NormalizeGlobal(&candidate, ids); err != nil {
		landingError(w, err)
		return
	}
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		landingError(w, err)
		return
	}
	warnings := []string{}
	all := []int64{}
	for _, s := range servers {
		all = append(all, s.ID)
		d, err := h.repo.LandingEgressData(s.ID)
		if err != nil || d == nil {
			continue
		}
		if ok, _, _, _ := landingEgressSupport(d); !ok {
			continue
		}
		before := 0
		if _, rep, err := h.compileLandingReport(d, nil, false); err == nil {
			before = len(rep.NodeNotes)
		}
		_, rep, err := h.compileLandingReport(d, func(d *repo.LandingEgressData) { d.Global = candidate }, false)
		if err != nil {
			landingError(w, fmt.Errorf("落地服务器「%s」：%w", s.Name, err))
			return
		}
		for _, note := range rep.GlobalNotes {
			if strings.HasPrefix(note, "全局规则未下发：") {
				landingError(w, fmt.Errorf("落地服务器「%s」放不下这些全局规则（%s），请减少规则或匹配条目", s.Name, strings.TrimPrefix(note, "全局规则未下发：")))
				return
			}
			warnings = append(warnings, fmt.Sprintf("%s：%s", s.Name, note))
		}
		if n := len(rep.NodeNotes); n > before {
			warnings = append(warnings, fmt.Sprintf("%s：服务器规则容量不足，%d 个节点的节点规则将不生效", s.Name, n-before))
		}
	}
	edges, err := h.repo.LandingUpstreamEdgesWith(repo.EdgeOverrides{Global: &candidate})
	if err != nil {
		landingError(w, err)
		return
	}
	if err := checkLandingLoops(edges); err != nil {
		landingError(w, err)
		return
	}
	if _, err := h.repo.SaveLandingGlobal(&g); err != nil {
		landingError(w, err)
		return
	}
	log.Printf("[landing-global] saved forced=%d default=%d action=%s servers=%d", len(g.Forced), len(g.Default), g.DefaultAction, len(all))
	warnings = append(warnings, h.ensureLandingWarp(nil, false, 20*time.Second)...)
	h.scheduleLandingLayerApply(all)
	view, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	view["warnings"] = warnings
	response.WriteJSON(w, response.OK(view))
}

// landingEgressUserCapsSave stores the global node-rule caps (no recompile:
// caps only gate later saves).
func (h *Handler) landingEgressUserCapsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PerNode *int `json:"perNode"`
		Total   *int `json:"total"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.PerNode == nil || req.Total == nil {
		landingError(w, errors.New("请填写每个节点和每个用户的节点规则上限"))
		return
	}
	if err := h.repo.SetLandingRuleCapDefaults(*req.PerNode, *req.Total); err != nil {
		landingError(w, err)
		return
	}
	perNode, total, _ := h.repo.LandingRuleCapDefaults()
	response.WriteJSON(w, response.OK(map[string]any{"perNode": perNode, "total": total}))
}

// ---- Node rules ------------------------------------------------------------

// landingNodeRulesView is the 路由规则 dialog of one node.
func (h *Handler) landingNodeRulesView(n *model.LandingNode, admin bool) (map[string]any, error) {
	d, err := h.repo.LandingEgressData(n.LandingServerID)
	if err != nil || d == nil {
		return nil, errors.New("读取落地服务器失败")
	}
	if d.Server.ChainOnly {
		return nil, errors.New("线路机专用节点不能设置节点规则")
	}
	rules, def := repo.NodeRulesFromModel(*n)
	caps, err := h.repo.EffectiveLandingRuleCaps(n.UserID)
	if err != nil {
		return nil, err
	}
	used, err := h.repo.LandingUserRuleTotal(n.UserID)
	if err != nil {
		return nil, err
	}
	outboundChoices := []map[string]any{}
	ids := make([]int64, 0, len(d.Outbounds))
	for id := range d.Outbounds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	referenced := map[string]bool{def: true}
	for _, rule := range rules {
		referenced[rule.Action] = true
	}
	for _, id := range ids {
		o := d.Outbounds[id]
		ref := fmt.Sprintf("outbound:%d", id)
		if !admin && !o.UserAllowed && !referenced[ref] {
			continue
		}
		outboundChoices = append(outboundChoices, map[string]any{"id": id, "name": o.Name, "type": o.Type, "ipVersion": o.IPVersion, "allowed": admin || o.UserAllowed})
	}
	supported, _, _, reason := landingEgressSupport(d)
	// What 「跟随上级」 resolves to for this node.
	pid := landingegress.EffectiveProfile(d.Server.RouteProfileID, n.RouteProfileID)
	inherited, inheritedFrom := d.Global.DefaultAction, "global"
	if p, ok := d.Profiles[pid]; pid != 0 && ok {
		inherited, inheritedFrom = p.DefaultAction, "server"
	}
	if inheritedFrom == "global" && !landingAbuseReady(d) {
		inherited = landingegress.ActionDirect
	}
	if inherited == "" {
		inherited = landingegress.ActionDirect
	}
	labelOf := func(action string) string {
		if admin {
			if id, ok := landingegress.ActionOutbound(action); ok {
				if o, ok := d.Outbounds[id]; ok {
					return o.Name
				}
			}
		}
		return landingegress.ActionLabel(action, d.Outbounds)
	}
	server := map[string]any{"label": landingegress.Label(pid, d.Profiles, d.Outbounds)}
	if p, ok := d.Profiles[pid]; pid != 0 && ok {
		if admin {
			server["profileName"] = p.Name
			server["rules"] = ruleSummaries(p.Rules, d.Outbounds, true)
		} else {
			server["ruleCount"] = len(ruleSummaries(p.Rules, d.Outbounds, false))
		}
		server["defaultLabel"] = labelOf(p.DefaultAction)
	}
	globalOn := landingAbuseReady(d) && supported
	state, stateReason := "applied", ""
	notes := []string{}
	switch {
	case !supported:
		state, stateReason = "unsupported", reason
	case !landingNodeRulesReady(d):
		state, stateReason = "queued", "该服务器的落地 Core 还不能热更新规则，节点规则下次重启生效（不会为此重启服务器）"
	default:
		plan, rep, err := h.compileLandingReport(d, nil, false)
		if err != nil {
			state, stateReason = "failed", err.Error()
			break
		}
		notes = rep.NodeNotes[n.ID]
		if d.Server.Source == "native" {
			if !plan.Active() {
				plan = nil
			}
			if planHash(plan) != d.Server.EgressHash {
				state = "syncing"
			}
			if node, err := h.repo.GetNodeByID(d.Server.NodeID); err == nil && node != nil && node.Status != 1 {
				state, stateReason = "failed", "落地服务器离线，恢复在线后自动同步"
			}
		} else if d.Server.SharedPushedRevision != d.Server.SharedAppliedRevision {
			state = "syncing"
		}
		if state == "applied" && d.Server.EgressState == "error" {
			state, stateReason = "failed", d.Server.EgressError
		}
	}
	view := map[string]any{
		"nodeId": n.ID, "nodeName": n.Name, "serverId": d.Server.ID, "serverName": d.Server.Name,
		"rules": rules, "defaultAction": def, "inheritedAction": inherited, "inheritedLabel": labelOf(inherited), "inheritedFrom": inheritedFrom,
		"outbounds": outboundChoices, "supported": supported, "state": state, "stateReason": stateReason, "notes": notes,
		"global":  map[string]any{"active": globalOn, "forced": ruleSummaries(d.Global.Forced, d.Outbounds, admin), "default": ruleSummaries(d.Global.Default, d.Outbounds, admin), "defaultLabel": labelOf(d.Global.DefaultAction)},
		"builtin": landingBuiltinNodeView(d, pid, globalOn),
		"server":  server,
		"caps":    map[string]any{"perNode": caps.PerNode, "total": caps.Total, "used": used, "unlimited": caps.Unlimited || admin, "perNodeSource": caps.PerNodeSource, "totalSource": caps.TotalSource},
		"admin":   admin,
	}
	if !landingAbuseReady(d) {
		view["globalPending"] = "该服务器的落地 Core 确认重启一次后才应用全局规则"
	}
	return view, nil
}

func (h *Handler) landingNodeRulesGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	n, err := h.landingOwnedNode(r, req.ID)
	if err != nil {
		landingError(w, err)
		return
	}
	_, role, _ := userRoleFromRequest(r)
	view, err := h.landingNodeRulesView(n, role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// landingNodeRulesSave stores a node's own rules (owner or admin). The
// server is compiled with the candidate first: a node whose rules would not
// fit is refused instead of being dropped silently; then the caps are
// enforced in one locked transaction and the server is pushed (hot apply).
func (h *Handler) landingNodeRulesSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID            int64                `json:"id"`
		Rules         []landingegress.Rule `json:"rules"`
		DefaultAction string               `json:"defaultAction"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	uid, role, _ := userRoleFromRequest(r)
	admin := role == 0
	n, err := h.landingOwnedNode(r, req.ID)
	if err != nil {
		landingError(w, err)
		return
	}
	if req.Rules == nil {
		req.Rules = []landingegress.Rule{}
	}
	d, err := h.repo.LandingEgressData(n.LandingServerID)
	if err != nil || d == nil {
		landingError(w, errors.New("读取落地服务器失败"))
		return
	}
	if d.Server.ChainOnly {
		landingError(w, errors.New("线路机专用节点不能设置节点规则"))
		return
	}
	supported, _, _, reason := landingEgressSupport(d)
	if !supported && (len(req.Rules) > 0 || req.DefaultAction != "") {
		landingError(w, errors.New(reason))
		return
	}
	all := map[int64]bool{}
	for id := range d.Outbounds {
		all[id] = true
	}
	rules, def, err := landingegress.NormalizeNodeRules(append([]landingegress.Rule(nil), req.Rules...), req.DefaultAction, all)
	if err != nil {
		landingError(w, err)
		return
	}
	if supported {
		raw := landingRulesJSON(rules)
		_, rep, err := h.compileLandingReport(d, func(d *repo.LandingEgressData) {
			for i := range d.Nodes {
				if d.Nodes[i].ID == n.ID {
					d.Nodes[i].RouteRules, d.Nodes[i].RouteRuleCount, d.Nodes[i].RouteDefault = raw, len(rules), def
				}
			}
		}, true)
		if err != nil {
			landingError(w, err)
			return
		}
		for _, note := range rep.NodeNotes[n.ID] {
			if strings.HasPrefix(note, "服务器规则容量已满") {
				landingError(w, errors.New("该落地服务器的规则容量已满，无法再添加节点规则，请减少规则或匹配条目"))
				return
			}
			if strings.Contains(note, "geo 数据") {
				landingError(w, errors.New("该落地服务器没有 geo 数据，节点规则不能使用 geoip / geosite"))
				return
			}
		}
		edges, err := h.repo.LandingUpstreamEdgesWith(repo.EdgeOverrides{NodeRules: map[int64][]string{n.ID: landingegress.Actions(rules, def)}})
		if err != nil {
			landingError(w, err)
			return
		}
		if err := checkLandingLoops(edges); err != nil {
			landingError(w, err)
			return
		}
	}
	saved, err := h.repo.SaveLandingNodeRules(repo.NodeRulesSave{NodeID: n.ID, Rules: req.Rules, Default: req.DefaultAction, Admin: admin, UserID: uid})
	if err != nil {
		landingError(w, err)
		return
	}
	warnings := []string{}
	if supported {
		warnings = append(warnings, h.ensureLandingWarp([]int64{n.LandingServerID}, false, 20*time.Second)...)
		h.landingEgressSaveAsync([]int64{n.LandingServerID})
	}
	view, err := h.landingNodeRulesView(saved, admin)
	if err != nil {
		landingError(w, err)
		return
	}
	if supported && view["state"] == "applied" && landingNodeRulesReady(d) {
		view["state"] = "syncing"
	}
	view["warnings"] = warnings
	response.WriteJSON(w, response.OK(view))
}

func landingRulesJSON(rules []landingegress.Rule) string {
	if rules == nil {
		rules = []landingegress.Rule{}
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

// SetLandingLayerApplyDelay shortens the debounce of the push after global
// rule saves (tests).
func (h *Handler) SetLandingLayerApplyDelay(d time.Duration) {
	h.layerApply.mu.Lock()
	h.layerApply.delay = d
	h.layerApply.mu.Unlock()
}

// WaitEgressSync waits for background pushes after egress edits (tests).
func (h *Handler) WaitEgressSync() { h.egressSyncWG.Wait() }
