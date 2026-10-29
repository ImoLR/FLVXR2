package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 155: admin maintenance endpoints that move one entry of a multi-entry
// tunnel into its own one-entry tunnel ("twin") and back.
//
// Why not /tunnel/update: removing an entry or renaming through it runs
// cleanupTunnelRuntime (DeleteChains on every entry, DeleteService/DeleteChains
// on every hop and exit), re-adds everything and re-sends UpdateService for
// every rule of the tunnel — the agent restarts a listener on every add or
// update, so all live connections of the tunnel would drop. Here the twin goes
// through the normal /tunnel/create path (new objects only) and the original
// only loses the moved entry's chain object (one DeleteChains on that node);
// its other entries, hops, exits and rules are not pushed at all.
//
// The regular multi-entry create/update/delete logic is not changed.

type tunnelSplitRequest struct {
	TunnelID    int64  `json:"tunnelId"`
	EntryNodeID int64  `json:"entryNodeId"`
	Name        string `json:"name"`
	TwinName    string `json:"twinName"`
	DryRun      *bool  `json:"dryRun"` // absent = dry run
}

type tunnelSplitUserPlan struct {
	UserID           int64   `json:"userId"`
	UserTunnelID     int64   `json:"userTunnelId"`
	EffectiveEntries []int64 `json:"effectiveEntries"`
	// Twin: "none" (cannot see the moved entry), "group" (gets the twin
	// through a tunnel group the twin joins) or "direct".
	Twin          string `json:"twin"`
	Num           int    `json:"num"`
	Flow          int64  `json:"flow"`
	ExpTime       int64  `json:"expTime"`
	FlowResetTime int64  `json:"flowResetTime"`
	SpeedID       *int64 `json:"speedId"`
	Status        int    `json:"status"`
	TwinUserTunID int64  `json:"twinUserTunnelId,omitempty"`
}

type tunnelSplitGroupPlan struct {
	repo.TunnelSplitGroup
	TwinJoins      bool `json:"twinJoins"`
	OriginalLeaves bool `json:"originalLeaves"`
}

type tunnelSplitPlan struct {
	DryRun         bool                   `json:"dryRun"`
	TunnelID       int64                  `json:"tunnelId"`
	EntryNodeID    int64                  `json:"entryNodeId"`
	Type           int                    `json:"type"`
	Inx            int                    `json:"inx"`
	NameBefore     string                 `json:"nameBefore"`
	NameAfter      string                 `json:"nameAfter"`
	InIPBefore     string                 `json:"inIpBefore"`
	EntriesBefore  []int64                `json:"entriesBefore"`
	EntriesAfter   []int64                `json:"entriesAfter"`
	TwinName       string                 `json:"twinName"`
	TwinRequest    map[string]interface{} `json:"twinRequest"`
	Groups         []tunnelSplitGroupPlan `json:"groups"`
	Folders        []int64                `json:"folders"`
	Lists          int                    `json:"lists"`
	Users          []tunnelSplitUserPlan  `json:"users"`
	Forwards       int64                  `json:"forwards"`
	ExpectedPushes map[string][]string    `json:"expectedPushes"`
	// Filled by apply.
	TwinID   int64                   `json:"twinId,omitempty"`
	Result   *repo.TunnelSplitResult `json:"result,omitempty"`
	Warnings []string                `json:"warnings,omitempty"`

	state *repo.TunnelSplitState
}

func requireAdminRequest(w http.ResponseWriter, r *http.Request) bool {
	_, roleID, err := userRoleFromRequest(r)
	if err != nil || roleID != 0 {
		response.WriteJSON(w, response.Err(403, "权限不足，仅管理员可操作"))
		return false
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return false
	}
	return true
}

func validSplitTunnelName(name string) error {
	if name == "" {
		return errors.New("隧道名称不能为空")
	}
	if utf8.RuneCountInString(name) > 100 {
		return errors.New("隧道名称不能超过100个字符")
	}
	if repo.HasTunnelEntryToken(name) {
		return errors.New("拆分后的隧道名称不能包含 {入口}")
	}
	return nil
}

func nodeKey(id int64) string { return fmt.Sprintf("%d", id) }

// planTunnelSplit validates the request and computes everything apply does.
func (h *Handler) planTunnelSplit(req tunnelSplitRequest) (*tunnelSplitPlan, error) {
	req.Name, req.TwinName = strings.TrimSpace(req.Name), strings.TrimSpace(req.TwinName)
	if req.TunnelID <= 0 || req.EntryNodeID <= 0 {
		return nil, errors.New("请求参数错误")
	}
	s, err := h.repo.LoadTunnelSplitState(req.TunnelID, req.EntryNodeID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("隧道不存在")
	}
	var problems []string
	t := s.Tunnel
	if t.Type != 1 && t.Type != 2 {
		problems = append(problems, fmt.Sprintf("不支持的隧道类型 %d", t.Type))
	}
	entries := s.EntryNodeIDs()
	entryRow, isEntry := s.EntryRow(req.EntryNodeID)
	if !isEntry {
		problems = append(problems, fmt.Sprintf("节点 %d 不是该隧道的入口", req.EntryNodeID))
	} else if len(entries) < 2 {
		problems = append(problems, "隧道只有一个入口，不需要拆分")
	}
	if s.ForwardsOnNode > 0 {
		problems = append(problems, fmt.Sprintf("有 %d 条规则在该入口上监听（规则不会被迁移）", s.ForwardsOnNode))
	}
	if s.BoundToNode > 0 {
		problems = append(problems, fmt.Sprintf("有 %d 条规则绑定了该入口", s.BoundToNode))
	}
	if s.HasLeiKwanPipe {
		problems = append(problems, "利群管道隧道不支持拆分")
	}
	if s.Federation > 0 {
		problems = append(problems, "联邦隧道不支持拆分")
	}
	if err := validSplitTunnelName(req.Name); err != nil {
		problems = append(problems, "原隧道新名称："+err.Error())
	}
	if err := validSplitTunnelName(req.TwinName); err != nil {
		problems = append(problems, "新隧道名称："+err.Error())
	}
	if req.Name != "" && req.Name == req.TwinName {
		problems = append(problems, "两个隧道名称不能相同")
	}
	if req.Name != "" {
		if taken, err := h.repo.TunnelNameTakenByOther(req.Name, t.ID); err != nil {
			return nil, err
		} else if taken {
			problems = append(problems, "原隧道新名称已被其他隧道使用")
		}
	}
	if req.TwinName != "" {
		if taken, err := h.repo.TunnelNameTakenByOther(req.TwinName, t.ID); err != nil {
			return nil, err
		} else if taken {
			problems = append(problems, "新隧道名称已被其他隧道使用")
		}
	}

	plan := &tunnelSplitPlan{
		DryRun: true, TunnelID: t.ID, EntryNodeID: req.EntryNodeID, Type: t.Type, Inx: t.Inx,
		NameBefore: t.Name, NameAfter: req.Name, InIPBefore: t.InIP.String,
		EntriesBefore: entries, EntriesAfter: []int64{}, TwinName: req.TwinName,
		Folders: s.Folders, Lists: len(s.Lists), Forwards: s.Forwards,
		Groups: []tunnelSplitGroupPlan{}, Users: []tunnelSplitUserPlan{},
		ExpectedPushes: map[string][]string{}, state: s,
	}
	for _, id := range entries {
		if id != req.EntryNodeID {
			plan.EntriesAfter = append(plan.EntriesAfter, id)
		}
	}

	// Twin = the same hop chain behind the moved entry; hop ports left out so
	// /tunnel/create picks fresh ones from each hop node's range.
	in := []map[string]interface{}{{
		"nodeId": req.EntryNodeID, "protocol": defaultString(entryRow.Protocol.String, "tls"),
		"strategy": defaultString(entryRow.Strategy.String, "round"), "connectIpType": entryRow.ConnectIPType.String,
	}}
	hops := map[int64][]map[string]interface{}{}
	hopOrder := []int64{}
	out := []map[string]interface{}{}
	twinNodes := []int64{req.EntryNodeID}
	for _, c := range s.Chains {
		item := map[string]interface{}{
			"nodeId": c.NodeID, "protocol": defaultString(c.Protocol.String, "tls"), "strategy": defaultString(c.Strategy.String, "round"),
			"connectIp": c.ConnectIP.String, "connectIpType": c.ConnectIPType.String,
		}
		switch c.ChainType {
		case "2":
			if _, ok := hops[c.Inx.Int64]; !ok {
				hopOrder = append(hopOrder, c.Inx.Int64)
			}
			hops[c.Inx.Int64] = append(hops[c.Inx.Int64], item)
			twinNodes = append(twinNodes, c.NodeID)
		case "3":
			out = append(out, item)
			twinNodes = append(twinNodes, c.NodeID)
		}
	}
	sort.Slice(hopOrder, func(i, j int) bool { return hopOrder[i] < hopOrder[j] })
	chainNodes := make([]interface{}, 0, len(hopOrder))
	for _, inx := range hopOrder {
		chainNodes = append(chainNodes, hops[inx])
	}
	twinReq := map[string]interface{}{
		"name": req.TwinName, "type": t.Type, "flow": t.Flow, "trafficRatio": t.TrafficRatio, "status": t.Status,
		"ipPreference": t.IPPreference, "http": t.HTTP, "tls": t.TLS, "socks": t.Socks, "blockOther": t.BlockOther,
		"inNodeId": in, "chainNodes": chainNodes, "outNodeId": out,
	}
	if t.SpeedID.Valid && t.SpeedID.Int64 > 0 {
		twinReq["speedId"] = t.SpeedID.Int64
	}
	if t.ListID.Valid && t.ListID.Int64 > 0 {
		twinReq["listId"] = t.ListID.Int64
	}
	if t.TunnelGroupID.Valid && t.TunnelGroupID.Int64 > 0 {
		twinReq["tunnelGroupId"] = float64(t.TunnelGroupID.Int64) // tunnelCreate reads a JSON number
	}
	if t.Remark.Valid {
		twinReq["remark"] = t.Remark.String
	}
	plan.TwinRequest = twinReq

	for _, id := range twinNodes {
		node, err := h.getNodeRecord(id)
		if err != nil {
			problems = append(problems, fmt.Sprintf("节点 %d: %v", id, err))
			continue
		}
		if node.IsRemote == 1 {
			problems = append(problems, fmt.Sprintf("节点 %s 是联邦远程节点，不支持拆分", nodeDisplayName(node)))
		} else if node.Status != 1 {
			problems = append(problems, fmt.Sprintf("节点 %s 不在线（新隧道创建需要所有节点在线）", nodeDisplayName(node)))
		}
	}
	for _, id := range plan.EntriesAfter {
		if node, err := h.getNodeRecord(id); err == nil && node.IsRemote == 1 {
			problems = append(problems, fmt.Sprintf("入口 %s 是联邦远程节点，不支持拆分", nodeDisplayName(node)))
		}
	}

	joins := map[int64]bool{}
	for _, g := range s.Groups {
		gp := tunnelSplitGroupPlan{TunnelSplitGroup: g}
		for _, id := range g.Entries {
			gp.TwinJoins = gp.TwinJoins || id == req.EntryNodeID
		}
		gp.OriginalLeaves = gp.TwinJoins && len(g.Entries) == 1
		if gp.TwinJoins {
			joins[g.GroupID] = true
		}
		plan.Groups = append(plan.Groups, gp)
	}

	for _, ut := range s.Users {
		eff, err := h.repo.EffectiveTunnelEntryNodeIDs(ut.UserID, t.ID)
		if err != nil {
			return nil, err
		}
		sees, keeps := false, false
		for _, id := range eff {
			if id == req.EntryNodeID {
				sees = true
			} else {
				keeps = true
			}
		}
		up := tunnelSplitUserPlan{UserID: ut.UserID, UserTunnelID: ut.ID, EffectiveEntries: eff, Twin: "none",
			Num: ut.Num, Flow: ut.Flow, ExpTime: ut.ExpTime, FlowResetTime: ut.FlowResetTime, Status: ut.Status}
		if ut.SpeedID.Valid {
			v := ut.SpeedID.Int64
			up.SpeedID = &v
		}
		if sees && !keeps {
			problems = append(problems, fmt.Sprintf("用户 %d 只能使用该入口，拆分后会失去原隧道", ut.UserID))
		}
		if sees {
			up.Twin = "direct"
			if granted, err := h.userGetsTwinThroughGroups(ut.UserID, joins); err != nil {
				return nil, err
			} else if granted {
				up.Twin = "group"
			}
		}
		plan.Users = append(plan.Users, up)
	}

	n := nodeKey(req.EntryNodeID)
	if t.Type == 2 {
		plan.ExpectedPushes[n] = append(plan.ExpectedPushes[n], "AddChains chains_<twin>")
		for _, hop := range chainNodes {
			for _, item := range hop.([]map[string]interface{}) {
				k := nodeKey(asInt64(item["nodeId"], 0))
				plan.ExpectedPushes[k] = append(plan.ExpectedPushes[k], "AddChains chains_<twin>", "AddService <twin>_tls")
			}
		}
		for _, item := range out {
			k := nodeKey(asInt64(item["nodeId"], 0))
			plan.ExpectedPushes[k] = append(plan.ExpectedPushes[k], "AddService <twin>_tls")
		}
	}
	plan.ExpectedPushes[n] = append(plan.ExpectedPushes[n], "SetProtocol (legacy node-global, same values)")
	if t.Type == 2 {
		plan.ExpectedPushes[n] = append(plan.ExpectedPushes[n], fmt.Sprintf("DeleteChains chains_%d", t.ID))
	}

	if len(problems) > 0 {
		return plan, errors.New(strings.Join(problems, "；"))
	}
	return plan, nil
}

// userGetsTwinThroughGroups: the user is in a user group permitted on one of
// the tunnel groups the twin joins (group sync then creates the grant).
func (h *Handler) userGetsTwinThroughGroups(userID int64, groups map[int64]bool) (bool, error) {
	for groupID := range groups {
		pairs, err := h.repo.ListGroupPermissionPairsByTunnelGroup(groupID)
		if err != nil {
			return false, err
		}
		for _, p := range pairs {
			ids, err := h.repo.ListUserIDsByUserGroup(p[0])
			if err != nil {
				return false, err
			}
			for _, id := range ids {
				if id == userID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// callJSONHandler runs a handler in-process with the caller's context (claims).
func callJSONHandler(parent *http.Request, fn http.HandlerFunc, path string, payload interface{}) (response.R, error) {
	var out response.R
	raw, err := json.Marshal(payload)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(parent.Context(), http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	w := &captureWriter{}
	fn(w, req)
	if err := json.Unmarshal(w.body.Bytes(), &out); err != nil {
		return out, fmt.Errorf("decode %s response: %w", path, err)
	}
	return out, nil
}

// POST /api/v1/tunnel/split-entry
func (h *Handler) tunnelSplitEntry(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req tunnelSplitRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	plan, err := h.planTunnelSplit(req)
	if err != nil {
		if plan != nil {
			response.WriteJSON(w, response.R{Code: -1, Msg: err.Error(), TS: time.Now().UnixMilli(), Data: plan})
			return
		}
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	if req.DryRun == nil || *req.DryRun {
		response.WriteJSON(w, response.OK(plan))
		return
	}
	plan.DryRun = false
	if err := h.applyTunnelSplit(r, plan); err != nil {
		response.WriteJSON(w, response.R{Code: -1, Msg: err.Error(), TS: time.Now().UnixMilli(), Data: plan})
		return
	}
	response.WriteJSON(w, response.OK(plan))
}

func (h *Handler) applyTunnelSplit(r *http.Request, plan *tunnelSplitPlan) error {
	t := plan.state.Tunnel
	unlock := h.lockTunnelRuntime(t.ID)
	defer unlock()

	// 1. Twin through the normal create path (new runtime objects only).
	created, err := callJSONHandler(r, h.tunnelCreate, "/api/v1/tunnel/create", plan.TwinRequest)
	if err != nil {
		return err
	}
	if created.Code != 0 {
		return fmt.Errorf("创建新隧道失败: %s", created.Msg)
	}
	if data, ok := created.Data.(map[string]interface{}); ok {
		if warnings, ok := data["warnings"].([]interface{}); ok {
			for _, item := range warnings {
				plan.Warnings = append(plan.Warnings, fmt.Sprint(item))
			}
		}
	}
	twinID, err := h.repo.TunnelIDByName(plan.TwinName)
	if err != nil || twinID <= 0 {
		return fmt.Errorf("新隧道已创建但找不到它: %v", err)
	}
	plan.TwinID = twinID

	// 2. Original loses the entry; twin gets order, folders, lists, groups.
	tx := h.repo.BeginTx()
	if tx == nil || tx.Error != nil {
		h.rollbackSplitTwin(twinID)
		return errors.New("database unavailable")
	}
	res, err := h.repo.ApplyTunnelEntrySplitTx(tx, t.ID, twinID, plan.EntryNodeID, plan.NameAfter, time.Now().UnixMilli(), rebuildNodeEntryInIP)
	if err == nil {
		err = tx.Commit().Error
	}
	if err != nil {
		tx.Rollback()
		h.rollbackSplitTwin(twinID)
		plan.TwinID = 0
		return fmt.Errorf("拆分失败，新隧道已删除: %w", err)
	}
	plan.Result = res

	// 3. Grants: what /group/tunnel/assign does after a membership change.
	for _, pair := range res.Revoked {
		h.cleanupForwardsForUserTunnel(pair.UserID, pair.TunnelID)
	}
	for _, groupID := range res.Joined {
		_ = h.syncPermissionsByTunnelGroup(groupID)
	}
	for i := range plan.Users {
		u := &plan.Users[i]
		if u.Twin == "none" {
			continue
		}
		from := model.UserTunnel{Num: u.Num, Flow: u.Flow, ExpTime: u.ExpTime, FlowResetTime: u.FlowResetTime, Status: u.Status}
		if u.SpeedID != nil {
			from.SpeedID.Int64, from.SpeedID.Valid = *u.SpeedID, true
		}
		id, _, _, _, _, _, _, lookupErr := h.repo.GetExistingUserTunnel(u.UserID, twinID)
		if lookupErr != nil {
			grant := map[string]interface{}{"userId": u.UserID, "tunnelId": twinID, "flow": u.Flow, "num": u.Num,
				"expTime": u.ExpTime, "flowResetTime": u.FlowResetTime, "status": u.Status}
			if u.SpeedID != nil {
				grant["speedId"] = *u.SpeedID
			}
			if err := h.upsertUserTunnel(grant); err != nil {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("用户 %d 新隧道授权失败: %v", u.UserID, err))
				continue
			}
			id, _, _, _, _, _, _, _ = h.repo.GetExistingUserTunnel(u.UserID, twinID)
		} else if err := h.repo.CopyUserTunnelLimits(from, id); err != nil {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("用户 %d 新隧道限额复制失败: %v", u.UserID, err))
		}
		u.TwinUserTunID = id
	}

	// 4. Runtime of the original: only the moved entry's chain object.
	if t.Type == 2 {
		if _, err := h.sendNodeCommand(plan.EntryNodeID, "DeleteChains", map[string]interface{}{"chain": fmt.Sprintf("chains_%d", t.ID)}, false, true); err != nil {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("入口 %d 删除旧转发链失败（节点重连后会清理）: %v", plan.EntryNodeID, err))
		}
	}
	plan.Warnings = append(plan.Warnings, h.reconcileTunnelEntryPermissions(0, t.ID)...)
	log.Printf("[tunnel-split] tunnel=%d entry=%d → twin=%d %q / %q", t.ID, plan.EntryNodeID, twinID, plan.NameAfter, plan.TwinName)
	return nil
}

// rollbackSplitTwin removes a twin created by a split that could not finish.
func (h *Handler) rollbackSplitTwin(twinID int64) {
	unlock := h.lockTunnelRuntime(twinID)
	defer unlock()
	h.cleanupTunnelRuntime(twinID)
	if err := h.deleteTunnelByID(twinID); err != nil {
		log.Printf("[tunnel-split] rollback twin %d: %v", twinID, err)
	}
}

type tunnelMergeRequest struct {
	TunnelID int64   `json:"tunnelId"`
	TwinID   int64   `json:"twinId"`
	Name     string  `json:"name"`
	InIP     *string `json:"inIp"`
	DryRun   *bool   `json:"dryRun"`
}

type tunnelMergePlan struct {
	DryRun         bool                    `json:"dryRun"`
	TunnelID       int64                   `json:"tunnelId"`
	TwinID         int64                   `json:"twinId"`
	EntryNodeID    int64                   `json:"entryNodeId"`
	NameBefore     string                  `json:"nameBefore"`
	NameAfter      string                  `json:"nameAfter"`
	TwinName       string                  `json:"twinName"`
	TwinUsers      int                     `json:"twinUsers"`
	ExpectedPushes map[string][]string     `json:"expectedPushes"`
	Result         *repo.TunnelMergeResult `json:"result,omitempty"`
	Warnings       []string                `json:"warnings,omitempty"`
	orig, twin     *repo.TunnelSplitState
}

func sameHopChain(a, b []model.ChainTunnel) bool {
	key := func(rows []model.ChainTunnel) []string {
		out := []string{}
		for _, c := range rows {
			if c.ChainType == "1" {
				continue
			}
			out = append(out, fmt.Sprintf("%s/%d/%d/%s/%s/%s/%s", c.ChainType, c.Inx.Int64, c.NodeID, c.Protocol.String, c.Strategy.String, c.ConnectIP.String, c.ConnectIPType.String))
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

func (h *Handler) planTunnelMerge(req tunnelMergeRequest) (*tunnelMergePlan, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.TunnelID <= 0 || req.TwinID <= 0 || req.TunnelID == req.TwinID {
		return nil, errors.New("请求参数错误")
	}
	orig, err := h.repo.LoadTunnelSplitState(req.TunnelID, 0)
	if err != nil {
		return nil, err
	}
	twin, err := h.repo.LoadTunnelSplitState(req.TwinID, 0)
	if err != nil {
		return nil, err
	}
	if orig == nil || twin == nil {
		return nil, errors.New("隧道不存在")
	}
	plan := &tunnelMergePlan{DryRun: true, TunnelID: orig.Tunnel.ID, TwinID: twin.Tunnel.ID, NameBefore: orig.Tunnel.Name,
		NameAfter: req.Name, TwinName: twin.Tunnel.Name, TwinUsers: len(twin.Users), ExpectedPushes: map[string][]string{}, orig: orig, twin: twin}
	var problems []string
	entries := twin.EntryNodeIDs()
	if len(entries) != 1 {
		problems = append(problems, "拆分出的隧道必须正好有一个入口")
	} else {
		plan.EntryNodeID = entries[0]
		if _, ok := orig.EntryRow(entries[0]); ok {
			problems = append(problems, "原隧道已经有这个入口")
		}
	}
	if orig.Tunnel.Type != twin.Tunnel.Type {
		problems = append(problems, "两个隧道类型不同")
	}
	if !sameHopChain(orig.Chains, twin.Chains) {
		problems = append(problems, "两个隧道的中转/出口不一致")
	}
	if twin.Forwards > 0 {
		problems = append(problems, fmt.Sprintf("拆分出的隧道上有 %d 条规则，请先删除或迁移", twin.Forwards))
	}
	if orig.HasLeiKwanPipe || twin.HasLeiKwanPipe || orig.Federation > 0 || twin.Federation > 0 {
		problems = append(problems, "利群管道 / 联邦隧道不支持合并")
	}
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 100 {
		problems = append(problems, "隧道名称不能为空且不能超过100个字符")
	} else if taken, err := h.repo.TunnelNameTakenByOther(req.Name, orig.Tunnel.ID); err != nil {
		return nil, err
	} else if taken && req.Name != twin.Tunnel.Name {
		problems = append(problems, "隧道名称已被其他隧道使用")
	}
	if plan.EntryNodeID > 0 {
		n := nodeKey(plan.EntryNodeID)
		if orig.Tunnel.Type == 2 {
			plan.ExpectedPushes[n] = append(plan.ExpectedPushes[n], fmt.Sprintf("AddChains chains_%d", orig.Tunnel.ID))
		}
		plan.ExpectedPushes[n] = append(plan.ExpectedPushes[n], fmt.Sprintf("DeleteChains chains_%d (twin delete)", twin.Tunnel.ID))
		for _, c := range twin.Chains {
			k := nodeKey(c.NodeID)
			switch c.ChainType {
			case "2":
				plan.ExpectedPushes[k] = append(plan.ExpectedPushes[k], fmt.Sprintf("DeleteChains chains_%d", twin.Tunnel.ID), fmt.Sprintf("DeleteService %d_tls", twin.Tunnel.ID))
			case "3":
				plan.ExpectedPushes[k] = append(plan.ExpectedPushes[k], fmt.Sprintf("DeleteService %d_tls", twin.Tunnel.ID))
			}
		}
	}
	if len(problems) > 0 {
		return plan, errors.New(strings.Join(problems, "；"))
	}
	return plan, nil
}

// POST /api/v1/tunnel/merge-entry — exact undo of split-entry.
func (h *Handler) tunnelMergeEntry(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req tunnelMergeRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	plan, err := h.planTunnelMerge(req)
	if err != nil {
		if plan != nil {
			response.WriteJSON(w, response.R{Code: -1, Msg: err.Error(), TS: time.Now().UnixMilli(), Data: plan})
			return
		}
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	if req.DryRun == nil || *req.DryRun {
		response.WriteJSON(w, response.OK(plan))
		return
	}
	plan.DryRun = false
	if err := h.applyTunnelMerge(plan, req.InIP); err != nil {
		response.WriteJSON(w, response.R{Code: -1, Msg: err.Error(), TS: time.Now().UnixMilli(), Data: plan})
		return
	}
	response.WriteJSON(w, response.OK(plan))
}

func (h *Handler) applyTunnelMerge(plan *tunnelMergePlan, inIP *string) error {
	unlock := h.lockTunnelRuntime(plan.TunnelID)
	defer unlock()
	tx := h.repo.BeginTx()
	if tx == nil || tx.Error != nil {
		return errors.New("database unavailable")
	}
	res, err := h.repo.ApplyTunnelEntryMergeTx(tx, plan.TunnelID, plan.TwinID, plan.NameAfter, inIP, time.Now().UnixMilli(), rebuildNodeEntryInIP)
	if err == nil {
		err = tx.Commit().Error
	}
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("合并失败: %w", err)
	}
	plan.Result = res

	// The original's chain object on the entry again (same builder as create).
	if plan.orig.Tunnel.Type == 2 {
		state, err := h.reconstructTunnelState(plan.TunnelID)
		if err == nil {
			var targets []tunnelRuntimeNode
			if targets, err = leiKwanEntryTargets(state); err == nil {
				var chainData map[string]interface{}
				if chainData, err = h.tunnelChainConfigFor(state, plan.EntryNodeID, targets); err == nil {
					_, err = h.sendNodeCommand(plan.EntryNodeID, "AddChains", chainData, true, false)
				}
			}
		}
		if err != nil {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("入口 %d 下发转发链失败（节点重连后会补上）: %v", plan.EntryNodeID, err))
		}
	}
	for _, groupID := range res.Joined {
		_ = h.syncPermissionsByTunnelGroup(groupID)
	}

	// The twin through the normal delete path (its runtime objects go too).
	unlockTwin := h.lockTunnelRuntime(plan.TwinID)
	h.cleanupTunnelRuntime(plan.TwinID)
	h.cleanupFederationRuntime(plan.TwinID)
	err = h.deleteTunnelByID(plan.TwinID)
	unlockTwin()
	if err != nil {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("删除拆分出的隧道失败: %v", err))
	}
	plan.Warnings = append(plan.Warnings, h.reconcileTunnelEntryPermissions(0, plan.TunnelID)...)
	log.Printf("[tunnel-split] merged twin=%d back into tunnel=%d entry=%d %q", plan.TwinID, plan.TunnelID, plan.EntryNodeID, plan.NameAfter)
	return nil
}
