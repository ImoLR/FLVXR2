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
	"go-backend/internal/store/model"
)

func (h *Handler) registerLanding(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{"server/enable": h.landingServerEnable, "server/list": h.landingServerList, "server/save": h.landingServerSave, "node/list": h.landingNodeList, "node/create": h.landingNodeCreate, "node/update": h.landingNodeUpdate, "node/delete": h.landingNodeDelete, "node/links": h.landingNodeLinks, "user/get": h.landingUserGet, "user/save": h.landingUserSave} {
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
func (h *Handler) landingServerView(s model.LandingServer, admin bool) map[string]interface{} {
	v := landingMap(s)
	v["protocols"] = jsonStrings(s.Protocols)
	v["realityServerNames"] = jsonStrings(s.RealityServerNames)
	groups, _ := h.repo.LandingServerGroupIDs(s.ID)
	if admin {
		v["userGroupIds"] = groups
	}
	node, _ := h.repo.GetNodeByID(s.NodeID)
	v["online"] = node != nil && node.Status == 1
	v["landingSupported"] = node != nil && node.LandingCapable
	all, _ := h.repo.ListLandingNodes(0, true)
	count, connections := 0, 0
	billed := int64(0)
	for _, n := range all {
		if n.LandingServerID == s.ID {
			count++
			connections += n.Connections
			billed += n.BilledIn + n.BilledOut
		}
	}
	v["nodeCount"] = count
	v["connections"] = connections
	v["billedBytes"] = billed
	v["usedBytes"] = billed
	return v
}
func (h *Handler) landingNodeView(n model.LandingNode) map[string]interface{} {
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
	for _, s := range ss {
		out = append(out, h.landingServerView(s, role == 0))
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
	req.Source = old.Source
	req.NodeID = old.NodeID
	b, _ := json.Marshal(req.Protocols)
	req.LandingServer.Protocols = string(b)
	b, _ = json.Marshal(req.RealityServerNames)
	req.LandingServer.RealityServerNames = string(b)
	if err := h.repo.SaveLandingServer(&req.LandingServer, req.UserGroupIDs); err != nil {
		landingError(w, err)
		return
	}
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(req.ID)
	response.WriteJSON(w, response.OK(h.landingServerView(req.LandingServer, true)))
}
func (h *Handler) landingNodeList(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	nodes, err := h.repo.ListLandingNodes(uid, role == 0)
	if err != nil {
		landingError(w, err)
		return
	}
	out := []map[string]interface{}{}
	for _, n := range nodes {
		out = append(out, h.landingNodeView(n))
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
	response.WriteJSON(w, response.OK(h.landingNodeView(*n)))
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
	for key, value := range req {
		if key == "id" {
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
	if err := h.repo.DeleteLandingNode(n.ID); err != nil {
		landingError(w, err)
		return
	}
	h.syncLandingServer(n.LandingServerID)
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
	response.WriteJSON(w, response.OK(map[string]interface{}{"userId": req.UserID, "maxNodes": s.MaxNodes, "quotas": q, "fixedNodeCount": fixed}))
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
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.SaveLandingUserSetting(req.UserID, req.MaxNodes, req.Quotas, req.ExtendFixedByMs); err != nil {
		landingError(w, err)
		return
	}
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
	return remoteAddr, id, createdID, err
}
