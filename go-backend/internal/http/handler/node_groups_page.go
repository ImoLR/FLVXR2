package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/response"
)

// Plan 324: the 节点 page groups. One group set = the 探针 groups
// (vite_config probe_groups, edited here or in the probe settings card) plus
// the 利群入口 node_group (leikwan_setting.entry_group_id, admin only, never on
// the probe: it is not stored in probe_groups). A node-page save writes only
// probe_groups (each group's 救命版 members kept as stored), the 利群 group's
// name / members and its position among the groups; the other probe settings
// stay untouched.

// nodePageLeiKwanPosKey: index of the 利群入口 group among the probe groups on
// the node page (missing → last). Kept outside probe_groups on purpose.
const nodePageLeiKwanPosKey = "node_page_leikwan_pos"

type nodePageGroup struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Nodes []int64 `json:"nodes"`
}

// nodePageLeiKwan: the 利群入口 group. Uses = what of 利群汇聚 needs a member
// (removing it is refused); Notes = information only (shown in the confirm).
type nodePageLeiKwan struct {
	ID    int64              `json:"id"`
	Name  string             `json:"name"`
	Nodes []int64            `json:"nodes"`
	Pos   int                `json:"pos"`
	Uses  map[int64][]string `json:"uses"`
	Notes map[int64][]string `json:"notes"`
}

type nodePageGroups struct {
	Groups  []nodePageGroup  `json:"groups"`
	LeiKwan *nodePageLeiKwan `json:"leikwan"`
}

type nodePageGroupsSave struct {
	Groups  []nodePageGroup `json:"groups"`
	LeiKwan *struct {
		Name  string  `json:"name"`
		Nodes []int64 `json:"nodes"`
		Pos   int     `json:"pos"`
	} `json:"leikwan"`
}

// errLeiKwanGroupDelete: the 利群入口 group cannot be deleted (UI + API).
var errLeiKwanGroupDelete = errors.New("利群汇聚正在用这个分组当入口机，不能删除；可以改名、加减机器")

// storedProbeGroups reads probe_groups fresh (not the 30 s probe cache).
func (h *Handler) storedProbeGroups() ([]probeGroup, string, error) {
	cfg, err := h.repo.GetConfigsByNames([]string{probeGroupsKey, nodePageLeiKwanPosKey})
	if err != nil {
		return nil, "", err
	}
	var groups []probeGroup
	if raw := strings.TrimSpace(cfg[probeGroupsKey]); raw != "" {
		if groups, err = parseProbeGroups(raw); err != nil {
			// Same as the probe: a row that no longer validates counts as none.
			groups = nil
		}
	}
	return groups, cfg[nodePageLeiKwanPosKey], nil
}

func clampLeiKwanPos(raw string, n int) int {
	pos, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || pos < 0 || pos > n {
		return n
	}
	return pos
}

// nodePageKnown: FLVX nodes that may be in a probe group (same set as the
// probe settings card: not remote).
func (h *Handler) nodePageKnown() (map[int64]bool, map[int64]string, error) {
	rows, err := h.repo.ListProbeNodes()
	if err != nil {
		return nil, nil, err
	}
	known := make(map[int64]bool, len(rows))
	names := make(map[int64]string, len(rows))
	for _, n := range rows {
		known[n.ID] = true
		names[n.ID] = n.Name
	}
	return known, names, nil
}

func (h *Handler) nodePageGroupsView() (*nodePageGroups, error) {
	known, _, err := h.nodePageKnown()
	if err != nil {
		return nil, err
	}
	stored, posRaw, err := h.storedProbeGroups()
	if err != nil {
		return nil, err
	}
	out := &nodePageGroups{Groups: make([]nodePageGroup, 0, len(stored))}
	for _, g := range stored {
		members := make([]int64, 0, len(g.Nodes))
		for _, id := range g.Nodes {
			if known[id] {
				members = append(members, id)
			}
		}
		out.Groups = append(out.Groups, nodePageGroup{ID: g.ID, Name: g.Name, Nodes: members})
	}
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil || setting.EntryGroupID <= 0 {
		return out, nil
	}
	group, err := h.repo.GetNodeGroupByID(setting.EntryGroupID)
	if err != nil || group == nil {
		return out, nil
	}
	nodes, err := h.repo.GetNodesByGroupID(group.ID)
	if err != nil {
		return nil, err
	}
	lk := &nodePageLeiKwan{ID: group.ID, Name: group.Name, Nodes: make([]int64, 0, len(nodes)), Pos: clampLeiKwanPos(posRaw, len(out.Groups))}
	ids := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		lk.Nodes = append(lk.Nodes, n.ID)
		ids[n.ID] = true
	}
	lk.Uses, lk.Notes = h.leiKwanEntryUses(ids)
	out.LeiKwan = lk
	return out, nil
}

// leiKwanEntryUses: per member of the 利群入口 group, what of 利群汇聚 needs it
// as an entry machine (uses: removing it would break that) and what only
// mentions it (notes). Reads grants / rules / lines / tunnels once each.
func (h *Handler) leiKwanEntryUses(ids map[int64]bool) (map[int64][]string, map[int64][]string) {
	uses, notes := map[int64][]string{}, map[int64][]string{}
	if len(ids) == 0 {
		return uses, notes
	}
	grants, _ := h.repo.ListLeiKwanGrants()
	lines, _ := h.repo.ListLeiKwanLines()
	rules, _ := h.repo.ListLeiKwanRules(0)
	lineName := map[int64]string{}
	lineOrder := map[int64]int{}
	for i, l := range lines {
		lineName[l.ID] = l.Name
		lineOrder[l.ID] = i
	}
	userLines := map[int64][]int64{}
	allEntries := 0
	for _, x := range rules {
		if !lkContains(userLines[x.UserID], x.LineID) {
			userLines[x.UserID] = append(userLines[x.UserID], x.LineID)
		}
		if x.AllEntries == 1 {
			allEntries++
		}
	}
	userIDs := make([]int64, 0, len(grants))
	for _, g := range grants {
		userIDs = append(userIDs, g.UserID)
	}
	users, _ := h.repo.UserNamesByIDs(userIDs)
	userLabel := func(id int64) string {
		if n := strings.TrimSpace(users[id]); n != "" {
			return "用户 " + n
		}
		return fmt.Sprintf("用户 %d", id)
	}
	linesOf := func(userID int64) string {
		ls := append([]int64(nil), userLines[userID]...)
		sort.Slice(ls, func(i, j int) bool { return lineOrder[ls[i]] < lineOrder[ls[j]] })
		names := make([]string, 0, len(ls))
		for _, id := range ls {
			if n := lineName[id]; n != "" {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return ""
		}
		return "（专线 " + strings.Join(names, "、") + "）"
	}
	graceFrom := time.Now().Add(-lkEntryGrace).UnixMilli()
	for _, g := range grants {
		if ids[g.MainEntryNodeID] {
			uses[g.MainEntryNodeID] = append(uses[g.MainEntryNodeID], userLabel(g.UserID)+linesOf(g.UserID)+"的主入口")
		}
		if g.PrevEntryNodeID > 0 && g.PrevEntryNodeID != g.MainEntryNodeID && ids[g.PrevEntryNodeID] && g.EntryChangedAt >= graceFrom {
			until := time.UnixMilli(g.EntryChangedAt + lkEntryGrace.Milliseconds()).In(time.FixedZone("UTC+8", 8*3600)).Format("01-02 15:04")
			uses[g.PrevEntryNodeID] = append(uses[g.PrevEntryNodeID], userLabel(g.UserID)+"换主入口后的旧入口（宽限到 "+until+"）")
		}
		if g.BackupEntryNodeID > 0 && ids[g.BackupEntryNodeID] {
			uses[g.BackupEntryNodeID] = append(uses[g.BackupEntryNodeID], userLabel(g.UserID)+"的备用入口")
		}
	}
	// The 备线 starts from the 前置 (the 主入口); the 备线隧道's own entry does
	// not depend on this group, so it is information only.
	backupLines := map[int64][]string{}
	for _, l := range lines {
		if l.BackupTunnelID > 0 {
			backupLines[l.BackupTunnelID] = append(backupLines[l.BackupTunnelID], l.Name)
		}
	}
	if len(backupLines) > 0 {
		if tunnels, err := h.repo.ListLeiKwanBackupTunnels(); err == nil {
			for _, t := range tunnels {
				names := backupLines[t.ID]
				if len(names) == 0 {
					continue
				}
				for _, id := range t.EntryNodeIDs {
					if ids[id] {
						notes[id] = append(notes[id], "专线 "+strings.Join(names, "、")+" 的备线隧道「"+t.Name+"」入口（不受这个分组影响）")
					}
				}
			}
		}
	}
	if allEntries > 0 {
		for id := range ids {
			notes[id] = append(notes[id], fmt.Sprintf("%d 条「所有利群入口都可用」的利群转发会少这个入口", allEntries))
		}
	}
	return uses, notes
}

// leiKwanRemoveRefusal: the message for members that cannot leave the group.
func leiKwanRemoveRefusal(removed []int64, uses map[int64][]string, names map[int64]string) error {
	parts := []string{}
	grace := false
	for _, id := range removed {
		u := uses[id]
		if len(u) == 0 {
			continue
		}
		for _, s := range u {
			if strings.Contains(s, "宽限") {
				grace = true
			}
		}
		name := strings.TrimSpace(names[id])
		if name == "" {
			name = fmt.Sprintf("节点 %d", id)
		}
		parts = append(parts, name+" 正在被利群汇聚用作 "+strings.Join(u, "、"))
	}
	if len(parts) == 0 {
		return nil
	}
	msg := strings.Join(parts, "；") + "，请先到利群汇聚换主入口"
	if grace {
		msg += "（宽限中的旧入口等宽限结束后再移出）"
	}
	return errors.New(msg)
}

func (h *Handler) nodePageGroupsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	out, err := h.nodePageGroupsView()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取分组失败"))
		return
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) nodePageGroupsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req nodePageGroupsSave
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	known, names, err := h.nodePageKnown()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取节点失败"))
		return
	}
	stored, _, err := h.storedProbeGroups()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取分组失败"))
		return
	}
	oldOf := make(map[int64][]int64, len(stored))
	for _, g := range stored {
		oldOf[g.ID] = g.Old
	}
	// The probe groups: FLVX ids only; each kept group carries its stored
	// 救命版 members (the node page does not know them).
	groups := make([]probeGroup, 0, len(req.Groups))
	for _, g := range req.Groups {
		pg := probeGroup{ID: g.ID, Name: g.Name, Nodes: g.Nodes}
		if g.ID > 0 {
			pg.Old = oldOf[g.ID]
		}
		groups = append(groups, pg)
	}
	normalized, err := normalizeProbeGroups(groups, known, nil)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}

	// The 利群入口 group (only when one is configured). Omitted = unchanged:
	// this endpoint can never drop it.
	var lkID int64
	var lkName string
	var lkAdd, lkRemove []int64
	lkPos := -1
	membersChanged := false
	if setting, _ := h.repo.GetLeiKwanSetting(); setting != nil && setting.EntryGroupID > 0 && req.LeiKwan != nil {
		group, gErr := h.repo.GetNodeGroupByID(setting.EntryGroupID)
		if gErr == nil && group != nil {
			lkID = group.ID
			name, err := cleanProbeGroupName(req.LeiKwan.Name)
			if err != nil {
				response.WriteJSON(w, response.ErrDefault(err.Error()))
				return
			}
			lower := strings.ToLower(name)
			for _, g := range normalized {
				if strings.ToLower(g.Name) == lower {
					response.WriteJSON(w, response.ErrDefault("分组名「"+name+"」重复了"))
					return
				}
			}
			if name != group.Name {
				all, err := h.repo.ListNodeGroups()
				if err != nil {
					response.WriteJSON(w, response.Err(-2, "读取分组失败"))
					return
				}
				for _, g := range all {
					if g.ID != group.ID && strings.ToLower(strings.TrimSpace(g.Name)) == lower {
						response.WriteJSON(w, response.ErrDefault("「"+name+"」和一个旧节点分组同名（旧分组已不在节点页显示），请换个名字"))
						return
					}
				}
				lkName = name
			}
			cur, err := h.repo.GetNodesByGroupID(group.ID)
			if err != nil {
				response.WriteJSON(w, response.Err(-2, "读取分组失败"))
				return
			}
			curSet := make(map[int64]bool, len(cur))
			for _, n := range cur {
				curSet[n.ID] = true
				if names[n.ID] == "" {
					names[n.ID] = n.Name
				}
			}
			target := make(map[int64]bool, len(req.LeiKwan.Nodes))
			for _, id := range req.LeiKwan.Nodes {
				if id <= 0 || (!known[id] && !curSet[id]) {
					response.WriteJSON(w, response.ErrDefault("分组「"+name+"」里有不存在的服务器，请刷新后再试"))
					return
				}
				if !target[id] && !curSet[id] {
					lkAdd = append(lkAdd, id)
				}
				target[id] = true
			}
			for _, n := range cur {
				if !target[n.ID] {
					lkRemove = append(lkRemove, n.ID)
				}
			}
			if len(lkRemove) > 0 {
				ids := make(map[int64]bool, len(lkRemove))
				for _, id := range lkRemove {
					ids[id] = true
				}
				uses, _ := h.leiKwanEntryUses(ids)
				if err := leiKwanRemoveRefusal(lkRemove, uses, names); err != nil {
					response.WriteJSON(w, response.ErrDefault(err.Error()))
					return
				}
			}
			membersChanged = len(lkAdd) > 0 || len(lkRemove) > 0
			lkPos = req.LeiKwan.Pos
			if lkPos < 0 || lkPos > len(normalized) {
				lkPos = len(normalized)
			}
		}
	}

	// Everything validated: write.
	if lkID > 0 && (lkName != "" || membersChanged) {
		if err := h.repo.SetNodeGroupMembers(lkID, lkName, lkAdd, lkRemove); err != nil {
			response.WriteJSON(w, response.Err(-2, "保存利群入口分组失败"))
			return
		}
	}
	now := time.Now().UnixMilli()
	groupsJSON, _ := json.Marshal(normalized)
	if err := h.repo.UpsertConfig(probeGroupsKey, string(groupsJSON), now); err != nil {
		response.WriteJSON(w, response.Err(-2, "保存失败"))
		return
	}
	if lkPos >= 0 {
		if err := h.repo.UpsertConfig(nodePageLeiKwanPosKey, strconv.Itoa(lkPos), now); err != nil {
			response.WriteJSON(w, response.Err(-2, "保存失败"))
			return
		}
	}
	h.resetProbeSettings()
	if membersChanged {
		// The 利群 host loop follows the new entry set (same as its 5-min pass).
		h.leiKwanHostKick()
	}
	out, err := h.nodePageGroupsView()
	if err != nil {
		response.WriteJSON(w, response.OKEmpty())
		return
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) registerNodePageGroups(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/node/groups", h.nodePageGroupsGet)
	mux.HandleFunc("/api/v1/node/groups/save", h.nodePageGroupsSave)
}
