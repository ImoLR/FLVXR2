package handler

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func (h *Handler) registerLanding(mux *http.ServeMux) {
	h.registerLandingEgress(mux)
	for path, fn := range map[string]http.HandlerFunc{"integration/get": h.landingIntegrationGet, "integration/save": h.landingIntegrationSave, "integration/test": h.landingIntegrationTest, "integration/sync": h.landingIntegrationSync, "server/enable": h.landingServerEnable, "server/list": h.landingServerList, "server/save": h.landingServerSave, "node/list": h.landingNodeList, "node/create": h.landingNodeCreate, "node/update": h.landingNodeUpdate, "node/delete": h.landingNodeDelete, "node/links": h.landingNodeLinks, "user/get": h.landingUserGet, "user/save": h.landingUserSave, "monitor": h.landingMonitor, "monitor/ips": h.landingMonitorIPs} {
		mux.HandleFunc("/api/v1/landing/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if _, _, err := userRoleFromRequest(r); err != nil {
				response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
				return
			}
			fn(w, r)
		})
	}
}
func landingError(w http.ResponseWriter, err error) {
	response.WriteJSON(w, response.ErrDefault(err.Error()))
}
func landingDecode(r *http.Request, v interface{}) error { return leiKwanDecode(r, v) }
func landingMap(v interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	out := map[string]interface{}{}
	_ = json.Unmarshal(b, &out)
	return out
}
func jsonStrings(v string) []string {
	out := []string{}
	_ = json.Unmarshal([]byte(v), &out)
	return out
}
func (h *Handler) landingServerView(s model.LandingServer, admin bool, catalogs ...landingEgressCatalog) map[string]interface{} {
	var catalog landingEgressCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	} else {
		catalog = h.landingEgressCatalog()
	}
	v := landingMap(s)
	v["protocols"] = jsonStrings(s.Protocols)
	v["realityServerNames"] = jsonStrings(s.RealityServerNames)
	groups, _ := h.repo.LandingServerGroupIDs(s.ID)
	if admin {
		v["userGroupIds"] = groups
		direct, _ := h.repo.LandingServerDirectUserCount(s.ID)
		v["directUserCount"] = direct
	}
	if s.Source == "shared" {
		caps := repo.SharedLandingCaps(&s)
		v["sharedCaps"] = caps
		if cfg, e := h.repo.GetLandingIntegration(); e == nil && cfg.SyncError != "" {
			s.SharedSyncError = cfg.SyncError
			v["sharedSyncError"] = cfg.SyncError
		}
		v["online"] = caps.Core.State == "running" && s.SharedSyncError == "" && s.SharedLastSyncAt > time.Now().Add(-2*time.Minute).UnixMilli()
		v["landingSupported"] = true
		v["sharedAvailable"] = repo.SharedLandingAvailable(&s)
		if admin {
			v["egress"] = h.landingServerEgressView(s, 0, catalog)
		}
	} else {
		node, _ := h.repo.GetNodeByID(s.NodeID)
		v["online"] = node != nil && node.Status == 1
		v["landingSupported"] = node != nil && node.LandingCapable
		if admin {
			nodeEgress := 0
			if node != nil {
				nodeEgress = node.LandingEgress
			}
			v["egress"] = h.landingServerEgressView(s, nodeEgress, catalog)
		}
	}
	// Users only see the read-only summary, never profile internals.
	v["egressLabel"] = catalog.label(s.RouteProfileID)
	if !admin {
		for _, key := range []string{"routeProfileId", "egressRestartOk", "egressState", "egressError", "egressReady", "egressCheckedAt"} {
			delete(v, key)
		}
	}
	all, _ := h.repo.ListLandingNodes(0, true)
	count, connections := 0, 0
	billed := int64(0)
	enabled := false
	for _, n := range all {
		if n.LandingServerID == s.ID {
			count++
			connections += n.Connections
			billed += n.BilledIn + n.BilledOut
			enabled = enabled || (n.Status == "active" && !n.DeletePending)
		}
	}
	if s.Source == "shared" {
		v["sharedNotice"] = sharedCoreNotice(&s, enabled)
	}
	v["nodeCount"] = count
	v["connections"] = connections
	v["billedBytes"] = billed
	v["usedBytes"] = billed
	return v
}
func (h *Handler) landingNodeView(n model.LandingNode, catalogs ...landingEgressCatalog) map[string]interface{} {
	var catalog landingEgressCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	} else {
		catalog = h.landingEgressCatalog()
	}
	v := landingMap(n)
	s, _ := h.repo.GetLandingServer(n.LandingServerID)
	u, _ := h.repo.GetUserByID(n.UserID)
	flow, ratio := 2, 1.0
	expires := int64(0)
	minExpiry := func(t int64) {
		if t > 0 && (expires == 0 || t < expires) {
			expires = t
		}
	}
	if s != nil {
		if s.Source == "shared" {
			if current, err := h.repo.GetLandingNode(n.ID); err == nil && current != nil {
				n = *current
				v = landingMap(n)
			}
			v["sharedSyncError"] = s.SharedSyncError
			if cfg, e := h.repo.GetLandingIntegration(); e == nil && cfg.SyncError != "" {
				v["sharedSyncError"] = cfg.SyncError
			}
			if status, reason := landingNodeDisplayStatus(&n, s); status != n.Status {
				v["status"] = status
				if reason != "" {
					v["pendingReason"] = reason
				}
			}
			if caps := repo.SharedLandingCaps(s); caps.ShareUntil != nil {
				minExpiry(*caps.ShareUntil)
			}
		}
		flow = s.FlowType
		ratio = s.TrafficRatio
		v["serverName"] = s.Name
		if s.AvailableUntil != nil {
			minExpiry(*s.AvailableUntil)
		}
	}
	if n.FlowType != nil {
		flow = *n.FlowType
	}
	if n.TrafficRatio != nil {
		ratio = *n.TrafficRatio
	}
	if u != nil {
		v["userName"] = u.User
		minExpiry(u.ExpTime)
	}
	if n.ExpiryMode == "fixed" && n.ExpiresAt != nil {
		minExpiry(*n.ExpiresAt)
	}
	serverProfile := int64(0)
	if s != nil {
		serverProfile = s.RouteProfileID
	}
	effectiveProfile := landingegress.EffectiveProfile(serverProfile, n.RouteProfileID)
	v["egressLabel"] = catalog.label(effectiveProfile)
	v["effectiveRouteProfileId"] = effectiveProfile
	v["effectiveRouteProfileName"] = catalog.profileName(effectiveProfile)
	v["effectiveFlowType"] = flow
	v["effectiveTrafficRatio"] = ratio
	v["effectiveExpiresAt"] = expires
	v["expiringSoon"] = expires > time.Now().UnixMilli() && expires <= time.Now().Add(72*time.Hour).UnixMilli()
	v["serverNames"] = jsonStrings(n.ServerNames)
	return v
}
func (h *Handler) landingServerEnable(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.checkSharedNativeConflict(r.Context(), req.NodeID); err != nil {
		landingError(w, err)
		return
	}
	s, err := h.repo.EnableLandingServer(req.NodeID)
	if err != nil {
		landingError(w, err)
		return
	}
	h.syncLandingServer(s.ID)
	response.WriteJSON(w, response.OK(h.landingServerView(*s, true)))
}
func (h *Handler) landingServerList(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	ss, err := h.repo.ListLandingServers(uid, role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	out := []map[string]interface{}{}
	catalog := h.landingEgressCatalog()
	for _, s := range ss {
		out = append(out, h.landingServerView(s, role == 0, catalog))
	}
	response.WriteJSON(w, response.OK(out))
}
func (h *Handler) landingServerSave(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		model.LandingServer
		Protocols          []string `json:"protocols"`
		RealityServerNames []string `json:"realityServerNames"`
		UserGroupIDs       []int64  `json:"userGroupIds"`
		Online             bool     `json:"online"`
		LandingSupported   bool     `json:"landingSupported"`
		NodeCount          int      `json:"nodeCount"`
		Connections        int      `json:"connections"`
		BilledBytes        int64    `json:"billedBytes"`
		UsedBytes          int64    `json:"usedBytes"`
		EgressConfirmed    bool     `json:"egressRestartConfirmed"`
		// Shadows the embedded field: absent (older frontends) keeps the
		// current assignment instead of resetting it to the default.
		ProfileID *int64 `json:"routeProfileId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	old, err := h.repo.GetLandingServer(req.ID)
	if err != nil || old == nil {
		landingError(w, errors.New("落地服务器不存在"))
		return
	}
	// Validate the outbound/routing assignment before saving anything else.
	profileID := old.RouteProfileID
	if req.ProfileID != nil {
		profileID = *req.ProfileID
	}
	needRestart, err := h.validateServerEgressAssignment(*old, profileID, req.EgressConfirmed)
	if err != nil {
		landingError(w, err)
		return
	}
	req.Source = old.Source
	req.NodeID = old.NodeID
	if old.Source == "native" && old.Status != "active" && req.Status == "active" {
		if err := h.checkSharedNativeConflict(r.Context(), old.NodeID); err != nil {
			landingError(w, err)
			return
		}
	}
	b, _ := json.Marshal(req.Protocols)
	req.LandingServer.Protocols = string(b)
	b, _ = json.Marshal(req.RealityServerNames)
	req.LandingServer.RealityServerNames = string(b)
	if err := h.repo.SaveLandingServer(&req.LandingServer, req.UserGroupIDs); err != nil {
		landingError(w, err)
		return
	}
	var warnings []string
	if profileID != old.RouteProfileID {
		if err := h.repo.SetLandingServerEgress(req.ID, profileID, needRestart); err != nil {
			landingError(w, err)
			return
		}
		warnings = h.ensureLandingWarp([]int64{req.ID}, false, 20*time.Second)
	}
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(req.ID)
	saved, _ := h.repo.GetLandingServer(req.ID)
	if saved == nil {
		saved = &req.LandingServer
	}
	view := h.landingServerView(*saved, true)
	if len(warnings) > 0 {
		view["egressWarnings"] = warnings
	}
	response.WriteJSON(w, response.OK(view))
}
func (h *Handler) landingNodeList(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	nodes, err := h.repo.ListLandingNodes(uid, role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	out := []map[string]interface{}{}
	catalog := h.landingEgressCatalog()
	for _, n := range nodes {
		view := h.landingNodeView(n, catalog)
		if role != 0 {
			hideLandingNodeEgressInternals(view)
		}
		out = append(out, view)
	}
	response.WriteJSON(w, response.OK(out))
}
func (h *Handler) landingNodeCreate(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	var req struct {
		LandingServerID int64  `json:"landingServerId"`
		UserID          int64  `json:"userId"`
		Name            string `json:"name"`
		Protocol        string `json:"protocol"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if role == 0 && req.UserID > 0 {
		uid = req.UserID
	}
	n, err := h.repo.CreateLandingNode(uid, req.LandingServerID, req.Name, req.Protocol, role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(n.LandingServerID)
	view := h.landingNodeView(*n)
	if role != 0 {
		hideLandingNodeEgressInternals(view)
	}
	response.WriteJSON(w, response.OK(view))
}

// hideLandingNodeEgressInternals keeps route profile ids/names admin-only;
// users only get the read-only egressLabel summary.
func hideLandingNodeEgressInternals(view map[string]interface{}) {
	delete(view, "routeProfileId")
	delete(view, "effectiveRouteProfileId")
	delete(view, "effectiveRouteProfileName")
}
func (h *Handler) landingOwnedNode(r *http.Request, id int64) (*model.LandingNode, error) {
	uid, role, err := userRoleFromRequest(r)
	if err != nil {
		return nil, err
	}
	n, err := h.repo.GetLandingNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, errors.New("落地节点不存在")
	}
	if role != 0 && n.UserID != uid {
		return nil, errors.New("无权访问该落地节点")
	}
	return n, nil
}
func (h *Handler) landingNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req map[string]json.RawMessage
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	var id int64
	_ = json.Unmarshal(req["id"], &id)
	n, err := h.landingOwnedNode(r, id)
	if err != nil {
		landingError(w, err)
		return
	}
	allowed := map[string]interface{}{"name": &n.Name, "maxConnections": &n.MaxConnections, "maxOnlineIps": &n.MaxOnlineIPs, "trafficLimitBytes": &n.TrafficLimitBytes, "flowType": &n.FlowType, "trafficRatio": &n.TrafficRatio, "expiryMode": &n.ExpiryMode, "expiresAt": &n.ExpiresAt, "status": &n.Status}
	profileChanged := false
	var profileID *int64
	if raw, ok := req["routeProfileId"]; ok {
		if err := json.Unmarshal(raw, &profileID); err != nil {
			landingError(w, errors.New("请求参数错误"))
			return
		}
		if profileID != nil && *profileID == 0 {
			// 0 is the built-in default; an explicit override to it is kept so
			// a node can opt out of its server's profile.
			zero := int64(0)
			profileID = &zero
		}
		current := n.RouteProfileID
		profileChanged = (current == nil) != (profileID == nil) || (current != nil && profileID != nil && *current != *profileID)
		if profileChanged {
			if err := h.validateNodeEgressOverride(n, profileID); err != nil {
				landingError(w, err)
				return
			}
		}
	}
	for key, value := range req {
		if key == "id" || key == "routeProfileId" {
			continue
		}
		target, ok := allowed[key]
		if !ok {
			landingError(w, errors.New("不支持修改该节点字段"))
			return
		}
		if err := json.Unmarshal(value, target); err != nil {
			landingError(w, errors.New("请求参数错误"))
			return
		}
	}
	if _, ok := req["status"]; ok {
		n.AutoDisabled = false
	}
	if err := h.repo.UpdateLandingNode(n); err != nil {
		landingError(w, err)
		return
	}
	if profileChanged {
		if err := h.repo.SetLandingNodeRouteProfile(n.ID, profileID); err != nil {
			landingError(w, err)
			return
		}
		n.RouteProfileID = profileID
		h.ensureLandingWarp([]int64{n.LandingServerID}, false, 20*time.Second)
	}
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(n.LandingServerID)
	response.WriteJSON(w, response.OK(h.landingNodeView(*n)))
}
func (h *Handler) landingNodeDelete(w http.ResponseWriter, r *http.Request) {
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
	if server, e := h.repo.GetLandingServer(n.LandingServerID); e == nil && server != nil && server.Source == "shared" {
		h.deleteSharedLandingNode(w, r, n)
		return
	}
	n, err = h.repo.PrepareLandingNodeDeletion(n.ID)
	if err != nil {
		landingError(w, err)
		return
	}
	snapshot, err := h.syncLandingServer(n.LandingServerID)
	if err != nil {
		landingError(w, errors.New("已保留节点及端口并标记停用；"+err.Error()))
		return
	}
	// ApplyLandingDesired collects after blocking and removing the inbound.
	// A missing/stale snapshot must never release the accounting identity.
	var final []repo.LandingCounter
	for _, counter := range snapshot.Nodes {
		if counter.ID == n.ID && counter.InboundTag == n.InboundTag && counter.Epoch != "" && counter.Connections == 0 {
			final = append(final, counter)
		}
	}
	if len(final) != 1 {
		landingError(w, errors.New("节点尚未确认最终流量及连接关闭，已保留节点和端口，请稍后重试"))
		return
	}
	enforce, err := h.commitFlowWithLanding(snapshot.NodeID, nil, final)
	if err != nil {
		landingError(w, errors.New("最终落地流量入账失败，已保留节点和端口，请稍后重试"))
		return
	}
	if err := h.repo.DeleteLandingNode(n.ID, true); err != nil {
		landingError(w, err)
		return
	}
	h.landingRateTracker().forget(n.ID)
	h.syncLandingServer(n.LandingServerID)
	// No sync/policy mutex is held here; enforcement may reconcile other nodes.
	if enforce != nil {
		enforce()
	}
	response.WriteJSON(w, response.OKEmpty())
}
func (h *Handler) landingNodeLinks(w http.ResponseWriter, r *http.Request) {
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
	links, err := h.repo.LandingNodeLinks(*n)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(links))
}
func (h *Handler) landingUserGet(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		UserID int64 `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	s, q, err := h.repo.GetLandingUserSetting(req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	nodes, _ := h.repo.ListLandingNodes(req.UserID, false)
	fixed := 0
	for _, n := range nodes {
		if n.ExpiryMode == "fixed" && n.ExpiresAt != nil {
			fixed++
		}
	}
	serverIDs, err := h.repo.LandingUserServerIDs(req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"userId": req.UserID, "maxNodes": s.MaxNodes, "quotas": q, "fixedNodeCount": fixed, "serverIds": serverIDs}))
}
func (h *Handler) landingUserSave(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		UserID          int64                   `json:"userId"`
		MaxNodes        int                     `json:"maxNodes"`
		Quotas          []model.UserServerQuota `json:"quotas"`
		ExtendFixedByMs int64                   `json:"extendFixedByMs"`
		FixedNodeCount  int                     `json:"fixedNodeCount"`
		ServerIDs       *[]int64                `json:"serverIds"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.SaveLandingUserSettingWithGrants(req.UserID, req.MaxNodes, req.Quotas, req.ExtendFixedByMs, req.ServerIDs); err != nil {
		landingError(w, err)
		return
	}
	// A revoke auto-disables the user's nodes there; a grant restores them.
	h.reconcileLandingPolicy(time.Now())
	nodes, _ := h.repo.ListLandingNodes(req.UserID, false)
	seen := map[int64]bool{}
	for _, n := range nodes {
		if !seen[n.LandingServerID] {
			h.syncLandingServer(n.LandingServerID)
			seen[n.LandingServerID] = true
		}
	}
	response.WriteJSON(w, response.OKEmpty())
}

// resolveNativeLandingTarget leaves normal target strings untouched.
func (h *Handler) resolveNativeLandingTarget(req map[string]interface{}, userID, tunnelID int64, remoteAddr string, admin bool) (string, int64, int64, error) {
	landingID, serverID := asInt64(req["landingNodeId"], 0), asInt64(req["landingServerId"], 0)
	createdID := int64(0)
	if landingID > 0 && serverID > 0 {
		return "", 0, 0, errors.New("请选择一种落地目标")
	}
	if serverID > 0 {
		n, err := h.repo.CreateLandingNode(userID, serverID, defaultString(asString(req["name"]), "规则落地"), defaultString(asString(req["landingProtocol"]), "ss2022"), admin)
		if err != nil {
			return "", 0, 0, err
		}
		landingID = n.ID
		createdID = n.ID
	}
	if landingID > 0 {
		n, err := h.repo.GetLandingNode(landingID)
		if err != nil || n == nil {
			return "", 0, createdID, errors.New("落地节点不存在")
		}
		if n.UserID != userID {
			return "", 0, createdID, errors.New("只能使用本用户的落地节点")
		}
		if !admin {
			ok, err := h.repo.UserCanUseLandingServer(userID, n.LandingServerID)
			if err != nil || !ok {
				return "", 0, createdID, errors.New("无权使用该落地服务器")
			}
		}
		s, err := h.repo.GetLandingServer(n.LandingServerID)
		if err != nil || s == nil {
			return "", 0, createdID, errors.New("落地服务器不存在")
		}
		remoteAddr = net.JoinHostPort(strings.Trim(s.PublicHost, "[]"), strconv.Itoa(n.Port))
		return remoteAddr, landingID, createdID, nil
	}
	id, err := h.repo.MatchLandingTarget(userID, tunnelID, remoteAddr)
	if err == nil && id > 0 && !admin {
		n, nodeErr := h.repo.GetLandingNode(id)
		if nodeErr != nil || n == nil {
			return "", 0, createdID, errors.New("落地节点不存在")
		}
		allowed, permissionErr := h.repo.UserCanUseLandingServer(userID, n.LandingServerID)
		if permissionErr != nil || !allowed {
			return "", 0, createdID, errors.New("无权使用该落地服务器")
		}
	}
	return remoteAddr, id, createdID, err
}
