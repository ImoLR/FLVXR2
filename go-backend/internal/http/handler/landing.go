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
	"go-backend/internal/landingcustom"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func (h *Handler) registerLanding(mux *http.ServeMux) {
	h.registerLandingEgress(mux)
	h.registerLandingExternal(mux)
	for path, fn := range map[string]http.HandlerFunc{"integration/get": h.landingIntegrationGet, "integration/save": h.landingIntegrationSave, "integration/test": h.landingIntegrationTest, "integration/sync": h.landingIntegrationSync, "server/enable": h.landingServerEnable, "server/list": h.landingServerList, "server/save": h.landingServerSave, "node/list": h.landingNodeList, "node/create": h.limitMutation(mutationLanding, h.landingNodeCreate), "node/update": h.limitMutation(mutationLanding, h.landingNodeUpdate), "node/delete": h.limitMutation(mutationLanding, h.landingNodeDelete), "node/links": h.landingNodeLinks, "user/get": h.landingUserGet, "user/save": h.landingUserSave, "settings/get": h.landingSettingsGet, "settings/save": h.landingSettingsSave, "node/transfer": h.limitMutation(mutationLanding, h.landingNodeTransfer), "monitor": h.landingMonitor, "monitor/ips": h.landingMonitorIPs, "node/rules/get": h.landingNodeRulesGet, "node/rules/save": h.limitMutation(mutationLanding, h.landingNodeRulesSave), "preset/list": h.landingPresetList} {
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

// landingServerStats are the server-wide node aggregates of the admin view.
type landingServerStats struct {
	count, connections int
	billed             int64
	enabled            bool
	// Plan 141: the node sum in each node's configured 计算方式 (used when the
	// server's runtime applies 仅入站) and how many nodes count each way;
	// connections stays the 入站+出站 sum (old agent / Helper).
	modeConnections, inboundNodes, totalNodes int
}

// landingConnSumMode labels a sum of node connection counts: "inbound" or
// "total" when every node counts the same way, "mixed" otherwise.
func landingConnSumMode(inbound, total int) string {
	switch {
	case inbound > 0 && total > 0:
		return "mixed"
	case inbound > 0:
		return repo.LandingConnInbound
	default:
		return repo.LandingConnTotal
	}
}

// landingServerStatsByID aggregates all landing nodes per server in one pass;
// servers resolve each node's configured 计算方式 (plan 141).
func landingServerStatsByID(all []model.LandingNode, servers map[int64]*model.LandingServer) map[int64]*landingServerStats {
	out := map[int64]*landingServerStats{}
	for _, n := range all {
		st := out[n.LandingServerID]
		if st == nil {
			st = &landingServerStats{}
			out[n.LandingServerID] = st
		}
		mode := repo.LandingNodeConnMode(&n, servers[n.LandingServerID])
		st.count++
		st.connections += repo.LandingLiveConnections(&n)
		st.modeConnections += repo.LandingModeConnections(&n, mode)
		if mode == repo.LandingConnInbound {
			st.inboundNodes++
		} else {
			st.totalNodes++
		}
		st.billed += n.BilledIn + n.BilledOut
		st.enabled = st.enabled || (n.Status == "active" && !n.DeletePending)
	}
	return out
}

// landingServerUserHidden are server fields a non-admin never receives:
// admin limits and defaults, core sizing, and 妙妙屋 sync internals. Server-wide
// aggregates (connections, traffic, other users' nodes) are not added at all.
var landingServerUserHidden = []string{"chainOnly", "routeProfileId", "egressRestartOk", "egressState", "egressError", "egressReady", "egressCheckedAt", "remoteServerId", "maxConnections", "defaultMaxConnections", "connMode", "maxConnMode", "defaultMaxOnlineIps", "defaultTrafficLimitBytes", "defaultUserQuotaBytes", "defaultExpiryMode", "defaultExpiryDays", "defaultExpiresAt", "coreMemoryMb", "coreCpuPercent", "revision", "sharedLastSyncAt", "sharedSyncError", "sharedPushedRevision", "sharedAppliedRevision", "createdTime", "updatedTime"}

// landingServerView renders one server for an admin, with server-wide
// aggregates. Lists use landingServerAdminView / landingServerUserView with
// aggregates computed once per request.
func (h *Handler) landingServerView(s model.LandingServer, catalogs ...landingEgressCatalog) map[string]interface{} {
	var catalog landingEgressCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	} else {
		catalog = h.landingEgressCatalog()
	}
	all, _ := h.repo.ListLandingNodes(0, true)
	return h.landingServerAdminView(s, catalog, landingServerStatsByID(all, map[int64]*model.LandingServer{s.ID: &s})[s.ID])
}

func (h *Handler) landingServerAdminView(s model.LandingServer, catalog landingEgressCatalog, st *landingServerStats) map[string]interface{} {
	return h.landingServerViewWith(s, true, catalog, st, nil)
}

// landingServerUserView is a non-admin's view: own node count on the server and
// how many more they may create, without limits or other users' data.
func (h *Handler) landingServerUserView(s model.LandingServer, catalog landingEgressCatalog, counts *repo.LandingUserNodeCounts) map[string]interface{} {
	return h.landingServerViewWith(s, false, catalog, nil, counts)
}

func (h *Handler) landingServerViewWith(s model.LandingServer, admin bool, catalog landingEgressCatalog, st *landingServerStats, counts *repo.LandingUserNodeCounts) map[string]interface{} {
	v := landingMap(s)
	connAgent := 0 // plan 141: the native agent's landing_conn_mode
	v["protocols"] = jsonStrings(s.Protocols)
	v["realityServerNames"] = jsonStrings(s.RealityServerNames)
	if admin {
		groups, _ := h.repo.LandingServerGroupIDs(s.ID)
		v["userGroupIds"] = groups
		direct, _ := h.repo.LandingServerDirectUserCount(s.ID)
		v["directUserCount"] = direct
		chainNodes, _ := h.repo.ChainCopyCountForServer(s.ID)
		v["chainNodeCount"] = chainNodes
	}
	if s.Source == "shared" {
		caps := repo.SharedLandingCaps(&s)
		if admin {
			// The busy-port list only feeds port picking; the view carries its size.
			v["busyPortCount"] = len(repo.SharedBusyPortList(&s))
			v["sharedCaps"] = caps
		} else {
			// Users only need the extra-protocol capabilities (create dialog:
			// creatable protocols, UDP hints), not the share's limits or usage.
			v["sharedCaps"] = map[string]interface{}{"protocol_caps": caps.ProtocolCaps}
		}
		if cfg, e := h.repo.GetLandingIntegration(); e == nil && cfg.SyncError != "" {
			s.SharedSyncError = cfg.SyncError
			if admin {
				v["sharedSyncError"] = cfg.SyncError
			}
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
		if node != nil {
			connAgent = node.LandingConnMode
		}
		if admin {
			nodeEgress, nodeGuard := 0, 0
			if node != nil {
				nodeEgress, nodeGuard = node.LandingEgress, node.LandingGuard
			}
			v["egress"] = h.landingServerEgressView(s, nodeEgress, catalog, nodeGuard)
		}
	}
	// Users only see the read-only summary, never profile internals.
	v["egressLabel"] = catalog.serverLabel(s)
	if !admin {
		for _, key := range landingServerUserHidden {
			delete(v, key)
		}
		// The caller's own nodes, counted exactly like node creation counts.
		v["nodeCount"] = counts.PerServer[s.ID]
		v["myRemaining"] = counts.Remaining(s.ID, s.MaxNodesPerUser)
		return v
	}
	if st == nil {
		st = &landingServerStats{}
	}
	if s.Source == "shared" {
		v["sharedNotice"] = sharedCoreNotice(&s, st.enabled)
	}
	v["nodeCount"] = st.count
	v["connections"] = st.connections
	// Plan 141: whether the runtime applies 仅入站 (false = old agent / 妙妙屋
	// Helper: 仅入站 settings wait, every limit stays 入站+出站); the node sum
	// then counts each node in its own mode, labelled by connectionsMode.
	supported := repo.LandingConnModeSupported(&s, connAgent)
	v["connModeSupported"] = supported
	v["connModeFixed"] = repo.LandingConnModeFixed(&s)
	v["maxConnectionsMode"] = repo.LandingServerTotalConnMode(&s, supported)
	v["connectionsMode"] = repo.LandingConnTotal
	if supported {
		v["connections"] = st.modeConnections
		v["connectionsMode"] = landingConnSumMode(st.inboundNodes, st.totalNodes)
	}
	v["billedBytes"] = st.billed
	v["usedBytes"] = st.billed
	// Plan 158: the whole-machine protection cap shown next to the server total
	// (a different, wider limit: whole machine TCP+UDP, all ports, all states).
	// 0 / "" = unknown (offline, protection off, old agent, no 妙妙屋 sample).
	cap, owner := h.landingMachineCap(s)
	v["machineMaxTotal"], v["machineCapOwner"] = cap, owner
	// A native total stored as 仅入站 (plan 141) keeps working until the server
	// is saved again; the dialog says so.
	v["maxConnModeLegacy"] = s.Source != "shared" && s.MaxConnections > 0 && repo.NormalizeLandingConnMode(s.MaxConnMode) == repo.LandingConnInbound
	return v
}

// landingMachineCap is the whole-machine connection cap of the server's
// machine: the live report (agent or 妙妙屋 Helper, incl. 由妙妙屋负责 machines),
// else the node setting; 0 = unknown or off.
func (h *Handler) landingMachineCap(s model.LandingServer) (int64, string) {
	var view *ws.MachineView
	if s.Source == "shared" {
		view = h.sharedMachineView(s.ID, true, time.Now())
	} else {
		view = h.machineView(s.NodeID, true)
	}
	if view != nil && view.Protect != nil {
		if view.Protect.Enabled && view.Protect.MaxTotal != nil && *view.Protect.MaxTotal > 0 {
			return *view.Protect.MaxTotal, view.Protect.Owner
		}
		return 0, view.Protect.Owner
	}
	if s.Source == "shared" {
		return 0, ""
	}
	if m, err := h.repo.GetNodeMachineProtection(s.NodeID); err == nil && m.Enabled && m.MaxTotal > 0 {
		return m.MaxTotal, "flvx"
	}
	return 0, ""
}
func (h *Handler) landingNodeView(n model.LandingNode, catalogs ...landingEgressCatalog) map[string]interface{} {
	var catalog landingEgressCatalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	} else {
		catalog = h.landingEgressCatalog()
	}
	s, _ := h.repo.GetLandingServer(n.LandingServerID)
	u, _ := h.repo.GetUserByID(n.UserID)
	integrationError := ""
	if s != nil && s.Source == "shared" {
		if current, err := h.repo.GetLandingNode(n.ID); err == nil && current != nil {
			n = *current
		}
		if cfg, err := h.repo.GetLandingIntegration(); err == nil {
			integrationError = cfg.SyncError
		}
	}
	supported := false
	if s != nil {
		agent := 0
		if s.Source == "native" {
			if node, err := h.repo.GetNodeByID(s.NodeID); err == nil && node != nil {
				agent = node.LandingConnMode
			}
		}
		supported = repo.LandingConnModeSupported(s, agent)
	}
	view := landingNodeViewData(n, s, u, catalog, integrationError, supported)
	if s != nil {
		h.landingChainViews([]map[string]interface{}{view}, []model.LandingNode{n}, map[int64]*model.LandingServer{s.ID: s}, catalog)
	}
	return view
}

// landingNodeViewData renders one node; connSupported says whether the
// server's runtime applies 仅入站 (plan 141).
func landingNodeViewData(n model.LandingNode, s *model.LandingServer, u *model.User, catalog landingEgressCatalog, integrationError string, connSupported bool) map[string]interface{} {
	v := landingMap(n)
	// Plan 141: the count shown next to the limit counts like the limit does.
	configured := repo.LandingNodeConnMode(&n, s)
	effective := repo.LandingEffectiveConnMode(&n, s, connSupported)
	v["connections"] = repo.LandingModeConnections(&n, effective)
	v["connModeConfigured"] = configured
	v["connModeEffective"] = effective
	v["connModePending"] = configured != effective
	// Plan 147: no 计算方式 choice on a shared server (fixed 入站+出站).
	v["connModeFixed"] = repo.LandingConnModeFixed(s)
	flow, ratio := 2, 1.0
	expires := int64(0)
	minExpiry := func(t int64) {
		if t > 0 && (expires == 0 || t < expires) {
			expires = t
		}
	}
	if s != nil {
		if s.Source == "shared" {
			v["sharedSyncError"] = s.SharedSyncError
			if integrationError != "" {
				v["sharedSyncError"] = integrationError
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
		} else if status, _ := landingNodeDisplayStatus(&n, s); status != n.Status {
			v["status"] = status
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
	if s != nil {
		v["egressLabel"] = catalog.nodeLabel(*s, n)
	} else {
		v["egressLabel"] = catalog.label(effectiveProfile)
	}
	v["effectiveRouteProfileId"] = effectiveProfile
	v["effectiveRouteProfileName"] = catalog.profileName(effectiveProfile, s)
	v["effectiveFlowType"] = flow
	v["effectiveTrafficRatio"] = ratio
	v["effectiveExpiresAt"] = expires
	v["expiringSoon"] = expires > time.Now().UnixMilli() && expires <= time.Now().Add(72*time.Hour).UnixMilli()
	v["serverNames"] = jsonStrings(n.ServerNames)
	if s != nil {
		if reason := landingNodeSyncError(&n, s); reason != n.SyncError {
			v["syncError"] = reason
		}
	}
	if landingcustom.IsExtraProtocol(n.Protocol) {
		// Non-secret options only; credentials stay in the links endpoint.
		v["protocolOpts"] = repo.LandingNodeOpts(&n)
	}
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
	response.WriteJSON(w, response.OK(h.landingServerView(*s)))
}
func (h *Handler) landingServerList(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	ownerAdmin := role == 0
	var req struct {
		UserID int64 `json:"userId"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := landingDecode(r, &req); err != nil {
			landingError(w, err)
			return
		}
	}
	if role == 0 && req.UserID > 0 {
		u, err := h.repo.GetUserByID(req.UserID)
		if err != nil || u == nil {
			landingError(w, errors.New("用户不存在"))
			return
		}
		uid, ownerAdmin = u.ID, u.RoleID == 0
	}
	ss, err := h.repo.ListLandingServers(uid, ownerAdmin)
	if err != nil {
		landingError(w, err)
		return
	}
	out := []map[string]interface{}{}
	catalog := h.landingEgressCatalog()
	// Per-request aggregates: one node query for the whole list, not per server.
	if role == 0 {
		all, err := h.repo.ListLandingNodes(0, true)
		if err != nil {
			landingError(w, err)
			return
		}
		byID := make(map[int64]*model.LandingServer, len(ss))
		for i := range ss {
			byID[ss[i].ID] = &ss[i]
		}
		stats := landingServerStatsByID(all, byID)
		for _, s := range ss {
			out = append(out, h.landingServerAdminView(s, catalog, stats[s.ID]))
		}
	} else {
		counts, err := h.repo.LandingUserNodeCounts(uid)
		if err != nil {
			landingError(w, err)
			return
		}
		for _, s := range ss {
			out = append(out, h.landingServerUserView(s, catalog, &counts))
		}
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
		// Plan 121 defaults, shadowed the same way (absent = keep).
		AnyTLSServerName *string `json:"anytlsServerName"`
		SnellVersion     *int    `json:"snellVersion"`
		SnellObfs        *string `json:"snellObfs"`
		SnellObfsHost    *string `json:"snellObfsHost"`
		MieruTransport   *string `json:"mieruTransport"`
		// Reality guard switch (plan 127); absent = keep.
		RealityGuard *bool `json:"realityGuard"`
		// Plan 129: one restart of an outdated native Core onto the pinned one.
		CoreUpgradeConfirmed bool `json:"coreUpgradeConfirmed"`
		// Plan 141 计算方式, shadowed (absent = keep), and 「应用到现有节点」.
		ConnMode             *string `json:"connMode"`
		MaxConnMode          *string `json:"maxConnMode"`
		ApplyConnModeToNodes bool    `json:"applyConnModeToNodes"`
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
	req.LandingServer.AnyTLSServerName, req.LandingServer.SnellVersion, req.LandingServer.SnellObfs = old.AnyTLSServerName, old.SnellVersion, old.SnellObfs
	req.LandingServer.SnellObfsHost, req.LandingServer.MieruTransport = old.SnellObfsHost, old.MieruTransport
	if req.AnyTLSServerName != nil {
		req.LandingServer.AnyTLSServerName = *req.AnyTLSServerName
	}
	if req.SnellVersion != nil {
		req.LandingServer.SnellVersion = *req.SnellVersion
	}
	if req.SnellObfs != nil {
		req.LandingServer.SnellObfs = *req.SnellObfs
	}
	if req.SnellObfsHost != nil {
		req.LandingServer.SnellObfsHost = *req.SnellObfsHost
	}
	if req.MieruTransport != nil {
		req.LandingServer.MieruTransport = *req.MieruTransport
	}
	req.LandingServer.ConnMode, req.LandingServer.MaxConnMode = old.ConnMode, old.MaxConnMode
	if req.ConnMode != nil {
		req.LandingServer.ConnMode = *req.ConnMode
	}
	if req.MaxConnMode != nil {
		req.LandingServer.MaxConnMode = *req.MaxConnMode
	}
	b, _ := json.Marshal(req.Protocols)
	req.LandingServer.Protocols = string(b)
	b, _ = json.Marshal(req.RealityServerNames)
	req.LandingServer.RealityServerNames = string(b)
	if err := h.repo.SaveLandingServerOpts(&req.LandingServer, req.UserGroupIDs, repo.LandingServerSaveOptions{ApplyConnModeToNodes: req.ApplyConnModeToNodes}); err != nil {
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
	} else if req.EgressConfirmed && old.Source == "native" && !old.EgressReady && !old.EgressRestartOK {
		// Plan 127: the default anti-abuse rules (and the Reality guard) need
		// the routing API too; the admin may confirm the one-time Core restart
		// without changing the profile.
		if err := h.repo.SetLandingServerEgress(req.ID, profileID, true); err != nil {
			landingError(w, err)
			return
		}
	}
	// Plan 129: shared servers too, once their Helper renders the guard.
	if req.RealityGuard != nil && (old.Source == "native" || repo.SharedRealityGuardSupported(old)) && *req.RealityGuard == old.RealityGuardOff {
		if err := h.repo.SetLandingRealityGuard(req.ID, *req.RealityGuard); err != nil {
			landingError(w, err)
			return
		}
	}
	if req.CoreUpgradeConfirmed && old.Source == "native" && old.CoreStaleEpoch != "" {
		if err := h.repo.SetLandingCoreUpgrade(req.ID); err != nil {
			landingError(w, err)
			return
		}
	}
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(req.ID)
	saved, _ := h.repo.GetLandingServer(req.ID)
	if saved == nil {
		saved = &req.LandingServer
	}
	view := h.landingServerView(*saved)
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
	data, err := h.repo.LandingNodeListData(nodes)
	if err != nil {
		landingError(w, err)
		return
	}
	out := []map[string]interface{}{}
	catalog := h.landingEgressCatalog()
	for _, n := range nodes {
		s := data.Servers[n.LandingServerID]
		view := landingNodeViewData(n, s, data.Users[n.UserID], catalog, data.IntegrationError, data.ConnSupport.Supported(s))
		view["forwardCount"] = data.ForwardCounts[n.ID]
		view["serverAvailable"] = data.Allowed[n.ID] && s != nil && !s.ChainOnly && s.Status == "active" && (s.AvailableUntil == nil || *s.AvailableUntil > time.Now().UnixMilli()) && repo.SharedLandingAvailable(s)
		if role != 0 {
			hideLandingNodeEgressInternals(view)
		}
		out = append(out, view)
	}
	h.landingChainViews(out, nodes, data.Servers, catalog)
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
	// Plan 147: the sync may have moved a never-used node off a busy port.
	if cur, err := h.repo.GetLandingNode(n.ID); err == nil && cur != nil {
		n = cur
	}
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
	// Plan 141: users only get the label of how their limit counts.
	delete(view, "connMode")
	delete(view, "connModeConfigured")
	delete(view, "connModePending")
	delete(view, "connModeFixed")
	delete(view, "inboundConnections")
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
	allowed := map[string]interface{}{"name": &n.Name, "maxConnections": &n.MaxConnections, "maxOnlineIps": &n.MaxOnlineIPs, "trafficLimitBytes": &n.TrafficLimitBytes, "flowType": &n.FlowType, "trafficRatio": &n.TrafficRatio, "connMode": &n.ConnMode, "expiryMode": &n.ExpiryMode, "expiresAt": &n.ExpiresAt, "status": &n.Status}
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
			if key == "protocol" || key == "port" {
				landingError(w, errors.New("落地节点的协议和端口不可修改，请新建节点后修改规则引用"))
				return
			}
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
	s, limit, q, err := h.repo.GetLandingUserSetting(req.UserID)
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
	// maxNodes is the cap in force; maxNodesSource says whether it is the
	// user's own value or the global default (plan 131).
	view := map[string]interface{}{"userId": req.UserID, "maxNodes": limit.Effective, "maxNodesSource": limit.Source, "globalMaxNodes": limit.Global, "quotas": q, "fixedNodeCount": fixed, "serverIds": serverIDs}
	// Plan 159: the user's 默认速率 (rows without their own 速率 follow it).
	if u, err := h.repo.GetUserByID(req.UserID); err == nil && u != nil {
		view["serverRateMbps"] = u.ServerRateMbps
	}
	// Plan 157: 共享流量池 + the servers related to this user (pickers).
	pools, err := h.repo.UserTrafficPools(req.UserID, true, time.Now())
	if err != nil {
		landingError(w, err)
		return
	}
	related, err := h.repo.UserRelatedServers(req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	view["pools"], view["relatedServers"] = pools, related
	if s.MaxNodesCustom {
		view["customMaxNodes"] = s.MaxNodes
	}
	// Plan 134 node-rule caps: in force, source (global | custom | admin), the
	// global defaults and how many node rules the user has.
	caps, err := h.repo.EffectiveLandingRuleCaps(req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	used, _ := h.repo.LandingUserRuleTotal(req.UserID)
	view["ruleMaxNode"], view["ruleMaxNodeSource"], view["globalRuleMaxNode"] = caps.PerNode, caps.PerNodeSource, caps.GlobalPerNode
	view["ruleMaxTotal"], view["ruleMaxTotalSource"], view["globalRuleMaxTotal"] = caps.Total, caps.TotalSource, caps.GlobalTotal
	view["ruleUsed"] = used
	response.WriteJSON(w, response.OK(view))
}
func (h *Handler) landingUserSave(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		UserID   int64 `json:"userId"`
		MaxNodes *int  `json:"maxNodes"`
		// MaxNodesSource "global" follows the global default; "custom" or
		// absent with a numeric maxNodes (older clients) sets an own value.
		MaxNodesSource  string                `json:"maxNodesSource"`
		Quotas          []userServerQuotaInput `json:"quotas"`
		ExtendFixedByMs int64                 `json:"extendFixedByMs"`
		FixedNodeCount  int                   `json:"fixedNodeCount"`
		ServerIDs       *[]int64              `json:"serverIds"`
		// Plan 134 node-rule caps: source "global" follows the global
		// default, "custom" uses the number; absent keeps the current choice.
		RuleMaxNodeSource  string `json:"ruleMaxNodeSource"`
		RuleMaxNode        *int   `json:"ruleMaxNode"`
		RuleMaxTotalSource string `json:"ruleMaxTotalSource"`
		RuleMaxTotal       *int   `json:"ruleMaxTotal"`
		// Plan 157: the user's 共享流量池; absent keeps them (older clients).
		Pools *[]repo.TrafficPoolInput `json:"pools"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	maxNodes, err := h.landingUserSaveMaxNodes(req.UserID, req.MaxNodesSource, req.MaxNodes)
	if err != nil {
		landingError(w, err)
		return
	}
	ruleNode, err := landingRuleCapChoice(req.RuleMaxNodeSource, req.RuleMaxNode, "每个节点最多节点规则数")
	if err != nil {
		landingError(w, err)
		return
	}
	ruleTotal, err := landingRuleCapChoice(req.RuleMaxTotalSource, req.RuleMaxTotal, "节点规则总数")
	if err != nil {
		landingError(w, err)
		return
	}
	// Plan 158: per-server 入站连接数 before the save, to re-push only the
	// forwards whose entry joins or leaves the user's quota group.
	capsBefore, capsErr := h.repo.UserServerConnCaps(req.UserID)
	// Plan 159: the user's 速率 per entry machine before the save.
	rateBefore := h.userRateBefore(req.UserID)
	quotas := make([]model.UserServerQuota, len(req.Quotas))
	for i, q := range req.Quotas {
		quotas[i] = q.UserServerQuota
		quotas[i].RateKeep = !q.hasRate
	}
	if err := h.repo.SaveLandingUserSettingFull(req.UserID, maxNodes, quotas, req.ExtendFixedByMs, req.ServerIDs, req.Pools); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.SetLandingUserRuleCaps(req.UserID, ruleNode, ruleTotal); err != nil {
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
	warnings := []string{}
	if capsAfter, err := h.repo.UserServerConnCaps(req.UserID); err == nil && capsErr == nil {
		if u, uErr := h.repo.GetUserByID(req.UserID); uErr == nil && u != nil {
			warnings = h.resyncForwardsForCapToggle(req.UserID, repo.QuotaUserLimits{MaxConnections: u.MaxConnections, MaxClientIps: u.MaxClientIps}, capsBefore, capsAfter)
		}
	}
	if h.quotaGroups != nil {
		h.quotaGroups.InvalidateInputs()
	}
	warnings = append(warnings, h.userRateAfter(req.UserID, rateBefore)...)
	if len(warnings) > 0 {
		response.WriteJSON(w, response.OK(map[string]interface{}{"warnings": warnings}))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

// userServerQuotaInput is one per-server row of a save; hasRate tells whether
// the client sent the plan-159 速率 fields (older frontends do not: their
// save keeps the stored rate).
type userServerQuotaInput struct {
	model.UserServerQuota
	hasRate bool
}

func (q *userServerQuotaInput) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &q.UserServerQuota); err != nil {
		return err
	}
	var probe struct {
		RateOverride *bool `json:"rateOverride"`
	}
	_ = json.Unmarshal(b, &probe)
	q.hasRate = probe.RateOverride != nil
	return nil
}

// landingRuleCapChoice maps a 跟随全局 / 自定义 choice onto the repo value
// (nil = keep, LandingRuleCapFollowGlobal = follow the global default).
func landingRuleCapChoice(source string, value *int, label string) (*int, error) {
	switch source {
	case "":
		return nil, nil
	case "global":
		v := repo.LandingRuleCapFollowGlobal
		return &v, nil
	case "custom":
		if value == nil || *value < 0 {
			return nil, errors.New("请填写" + label)
		}
		return value, nil
	}
	return nil, errors.New("落地设置无效")
}

// resolveNativeLandingTarget leaves normal target strings untouched.
func (h *Handler) resolveNativeLandingTarget(req map[string]interface{}, userID, tunnelID int64, remoteAddr string, admin bool) (string, int64, int64, error) {
	landingID, serverID := asInt64(req["landingNodeId"], 0), asInt64(req["landingServerId"], 0)
	createdID := int64(0)
	if landingID > 0 && serverID > 0 {
		return "", 0, 0, errors.New("请选择一种落地目标")
	}
	if serverID > 0 {
		protocol := defaultString(asString(req["landingProtocol"]), "ss2022")
		if h.isLineMachineTunnel(tunnelID) {
			protocol = "vless_reality_vision"
		}
		n, created, err := h.repo.ReuseOrCreateLandingNode(userID, serverID, defaultString(asString(req["name"]), "规则落地"), protocol, admin)
		if err != nil {
			return "", 0, 0, err
		}
		landingID = n.ID
		if created {
			createdID = n.ID
		}
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
