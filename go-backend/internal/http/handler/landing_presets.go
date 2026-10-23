package handler

import (
	"errors"
	"log"
	"net/http"

	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/repo"
)

// Plan 140: the 内置 list (safety rule, global switches of the default
// anti-abuse rules, source whitelist) and the routing-rule preset library.
// Plan 143: four 防滥用 rows (SMTP 25, 发信端口, Telnet/SMB, BT).

// landingBuiltinAdminView is the 内置 section of the admin 全局规则 card.
func (h *Handler) landingBuiltinAdminView(abuse landingegress.AbuseSwitches) (map[string]any, error) {
	off, err := h.repo.LandingProfilesAbuseOff()
	if err != nil {
		return nil, err
	}
	profiles := []map[string]any{}
	for _, p := range off {
		profiles = append(profiles, map[string]any{"id": p.ID, "name": p.Name})
	}
	view := map[string]any{"safety": landingegress.BlockedCIDRs, "profilesOff": profiles}
	for _, k := range landingegress.AbuseKeys {
		view[k] = abuse.On(k)
	}
	return view, nil
}

// landingBuiltinNodeView is the read-only 内置 list of one node: whether each
// default rule actually applies to it (server ready, global switch, the
// node's routing profile toggle). No addresses, no switches.
func landingBuiltinNodeView(d *repo.LandingEgressData, profileID int64, ready bool) map[string]any {
	profileOn := landingegress.AbuseOn(profileID, d.Profiles)
	on := ready && profileOn
	view := map[string]any{"safety": landingegress.BlockedCIDRs}
	// Why a row is not in force (the ✗ tooltip): "global" (switched off in
	// 全局规则), "profile" (the node's routing profile), "pending" (the
	// server cannot apply rules yet).
	reasons := map[string]string{}
	for _, k := range landingegress.AbuseKeys {
		view[k] = on && d.Abuse.On(k)
		switch {
		case !d.Abuse.On(k):
			reasons[k] = "global"
		case !profileOn:
			reasons[k] = "profile"
		case !ready:
			reasons[k] = "pending"
		}
	}
	view["offReasons"] = reasons
	return view
}

// landingEgressBuiltinSave stores the global switches of the default rules
// and pushes every landing server once (the same debounced path as a global
// group save).
func (h *Handler) landingEgressBuiltinSave(w http.ResponseWriter, r *http.Request) {
	// mail / telnet (plan 143) are optional: a client without them keeps
	// their current state.
	var req struct {
		SMTP   *bool `json:"smtp"`
		BT     *bool `json:"bt"`
		Mail   *bool `json:"mail"`
		Telnet *bool `json:"telnet"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.SMTP == nil || req.BT == nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	before, err := h.repo.GetLandingAbuseSwitches()
	if err != nil {
		landingError(w, err)
		return
	}
	after := before
	for key, v := range map[string]*bool{landingegress.AbuseSMTP: req.SMTP, landingegress.AbuseBT: req.BT, landingegress.AbuseMail: req.Mail, landingegress.AbuseTelnetSMB: req.Telnet} {
		if v != nil {
			after.Set(key, *v)
		}
	}
	if err := h.repo.SaveLandingAbuseSwitches(after); err != nil {
		landingError(w, err)
		return
	}
	if after != before {
		servers, err := h.repo.ListLandingServers(0, true)
		if err != nil {
			landingError(w, err)
			return
		}
		all := make([]int64, 0, len(servers))
		for _, s := range servers {
			all = append(all, s.ID)
		}
		log.Printf("[landing-builtin] smtp=%t mail=%t telnet=%t bt=%t servers=%d", after.On(landingegress.AbuseSMTP), after.On(landingegress.AbuseMail), after.On(landingegress.AbuseTelnetSMB), after.On(landingegress.AbuseBT), len(all))
		h.scheduleLandingLayerApply(all)
	}
	view, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	view["warnings"] = []string{}
	response.WriteJSON(w, response.OK(view))
}

// resyncLandingAfterMigration: the plan-143 startup migration moved enabled
// 防滥用 rules from 全局·强制 into 内置, so every plan changed (same blocked
// traffic, new order). Push every landing server once through the normal
// debounced path (native servers would also get it from the reconcile loop;
// shared ones only on their next change otherwise).
func (h *Handler) resyncLandingAfterMigration() {
	if !h.repo.TakeLandingStartupResync() {
		return
	}
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		log.Printf("[landing-builtin] startup resync skipped: %v", err)
		return
	}
	all := make([]int64, 0, len(servers))
	for _, s := range servers {
		all = append(all, s.ID)
	}
	log.Printf("[landing-builtin] 防滥用 moved to 内置, re-syncing %d landing servers once", len(all))
	h.scheduleLandingLayerApply(all)
}

// ---- Preset library ------------------------------------------------------

func presetView(p landingegress.Preset) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description, "rules": p.Rules,
		"askOutbound": p.AskOutbound, "insertTop": p.InsertTop, "sortOrder": p.SortOrder, "seedKey": p.SeedKey,
	}
}

func (h *Handler) landingPresetViews(admin bool) ([]map[string]any, error) {
	presets, err := h.repo.ListLandingEgressPresets()
	if err != nil {
		return nil, err
	}
	outs, err := h.repo.ListLandingOutbounds()
	if err != nil {
		return nil, err
	}
	exists, allowed := map[int64]bool{}, map[int64]bool{}
	for _, m := range outs {
		exists[m.ID] = true
		allowed[m.ID] = m.UserAllowed
	}
	out := []map[string]any{}
	for _, p := range presets {
		if admin {
			v := presetView(p)
			v["missingOutbound"] = landingegress.PresetMissingOutbound(p, exists)
			out = append(out, v)
			continue
		}
		if landingegress.PresetUsableByUser(p, allowed) {
			out = append(out, presetView(p))
		}
	}
	return out, nil
}

// landingPresetList: every logged-in user. Admins get the whole library;
// users only presets they can insert into their node rules (no 来源 IP /
// 来源端口, fixed outbounds 允许用户使用).
func (h *Handler) landingPresetList(w http.ResponseWriter, r *http.Request) {
	_, role, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return
	}
	views, err := h.landingPresetViews(role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"presets": views, "admin": role == 0}))
}

func (h *Handler) landingPresetSave(w http.ResponseWriter, r *http.Request) {
	var p landingegress.Preset
	if err := landingDecode(r, &p); err != nil {
		landingError(w, err)
		return
	}
	saved, err := h.repo.SaveLandingEgressPreset(&p)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(presetView(*saved)))
}

func (h *Handler) landingPresetDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.DeleteLandingEgressPreset(req.ID); err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(nil))
}

func (h *Handler) landingPresetReorder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.ReorderLandingEgressPresets(req.IDs); err != nil {
		landingError(w, err)
		return
	}
	views, err := h.landingPresetViews(true)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"presets": views, "admin": true}))
}

func (h *Handler) landingPresetRestore(w http.ResponseWriter, r *http.Request) {
	added, err := h.repo.RestoreLandingEgressPresets()
	if err != nil {
		landingError(w, err)
		return
	}
	views, err := h.landingPresetViews(true)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"presets": views, "admin": true, "added": added}))
}
