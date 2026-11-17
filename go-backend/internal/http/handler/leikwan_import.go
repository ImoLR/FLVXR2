package handler

// Plan 266 「导入为规则」: registers the existing LeiKwan forwards as 利群 rules
// under the admin, without changing anything on the LeiKwan side (no target
// change, no write) or on any host. Backups link the existing FLVX 備線
// forwards found through the hosts' 日志快切 lines. Idempotent: a forward that
// is already a rule or a pipe is skipped. Dry-run preview first.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/store/model"
)

type leiKwanImportItem struct {
	ForwardID      int64                  `json:"forwardId"`
	ListenPort     int                    `json:"listenPort"`
	RouteID        int64                  `json:"routeId"`
	RouteLabel     string                 `json:"routeLabel"`
	Target         string                 `json:"target"`
	Action         string                 `json:"action"` // import | skip | registered
	Reason         string                 `json:"reason,omitempty"`
	LineName       string                 `json:"lineName,omitempty"`
	RuleName       string                 `json:"ruleName,omitempty"`
	BackupForward  int64                  `json:"backupForwardId,omitempty"`
	BackupName     string                 `json:"backupName,omitempty"`
	BackupShared   []int64                `json:"backupSharedWith,omitempty"`
	BackupAddr     string                 `json:"backupAddr,omitempty"`
	Accel          model.LeiKwanRuleAccel `json:"accel"`
	LandingHint    string                 `json:"landingHint,omitempty"`
	CurrentOnHosts []leiKwanHostValue     `json:"hostValues,omitempty"`
}

type leiKwanImportLine struct {
	Exists         bool               `json:"exists"`
	ID             int64              `json:"id,omitempty"`
	Name           string             `json:"name"`
	RouteID        int64              `json:"routeId"`
	RouteLabel     string             `json:"routeLabel"`
	Accel          model.LeiKwanAccel `json:"accel"`
	BackupTunnelID int64              `json:"backupTunnelId"`
	BackupTunnel   string             `json:"backupTunnelName"`
	ForwardIDs     []int64            `json:"forwardIds"`
}

type leiKwanImportPlan struct {
	Items        []leiKwanImportItem      `json:"items"`
	Lines        []leiKwanImportLine      `json:"lines"`
	EntryGroup   map[string]interface{}   `json:"entryGroup"`
	AdminGrant   map[string]interface{}   `json:"adminGrant"`
	Global       model.LeiKwanGlobalAccel `json:"global"`
	Quota        leiKwanQuota             `json:"quota"`
	ReferenceHub string                   `json:"referenceHost"`
	Notes        []string                 `json:"notes"`
}

const leiKwanEntryGroupName = "利群入口"

// leiKwanImportBuild computes the plan; it reads the LeiKwan forward list only
// through the bounded, cached client (one GET pair per 15 s at most).
func (h *Handler) leiKwanImportBuild(r *http.Request) (*leiKwanImportPlan, *model.LeiKwanAccount, error) {
	setting, a, err := h.leiKwanSettingAccount()
	if err != nil || a == nil {
		return nil, nil, errors.New("请先在利群汇聚页保存利群账号")
	}
	view, err := h.leikwan.Overview(r.Context(), a.Token, a.ServiceID)
	if err != nil {
		return nil, nil, err
	}
	plan := &leiKwanImportPlan{Quota: leiKwanQuota{Limit: view.Options.ForwardLimit, Active: view.Options.ActiveForwardCount}, Notes: []string{}, Items: []leiKwanImportItem{}, Lines: []leiKwanImportLine{}}
	pipes, _ := h.repo.ListLeiKwanPipes(a.ID)
	pipeTunnel := map[int64]string{}
	for _, p := range pipes {
		name := "管道"
		if p.TunnelID != nil {
			name = h.repo.LeiKwanTunnelName(*p.TunnelID)
		}
		pipeTunnel[p.ForwardID] = name
	}
	hosts := h.leiKwanHostContexts()
	// Reference host = the one with the most wired 日志快切 lines (the user's
	// main test front); its files give the 利群加速 values to register.
	var ref *leiKwanHostCtx
	for i := range hosts {
		if ref == nil || len(hosts[i].snap.Lines) > len(ref.snap.Lines) {
			ref = &hosts[i]
		}
	}
	if ref != nil {
		plan.ReferenceHub = ref.name
	} else {
		plan.Notes = append(plan.Notes, "还没有入口机状态快照：预热 / 日志快切按默认值登记，备线无法自动链接")
	}

	// Entry node group 「利群入口」: existing one, or the nodes that run the
	// LeiKwan client (detected by the agent, or present in a host snapshot).
	groupID := setting.EntryGroupID
	if groupID == 0 {
		if gs, err := h.repo.ListNodeGroups(); err == nil {
			for _, g := range gs {
				if g.Name == leiKwanEntryGroupName {
					groupID = g.ID
				}
			}
		}
	}
	members := []map[string]interface{}{}
	if groupID > 0 {
		for _, e := range h.leiKwanEntryNodes(groupID) {
			members = append(members, map[string]interface{}{"id": e.ID, "name": e.Name, "host": e.Host, "inGroup": true})
		}
	}
	if len(members) == 0 {
		seen := map[int64]bool{}
		candidates := []int64{}
		if ids, err := h.repo.ListLeiKwanDetectedNodeIDs(); err == nil {
			candidates = append(candidates, ids...)
		}
		for _, hc := range hosts {
			candidates = append(candidates, hc.nodeID)
		}
		for _, id := range candidates {
			if n, err := h.repo.GetNodeRecord(id); err == nil && n != nil && !seen[n.ID] {
				seen[n.ID] = true
				members = append(members, map[string]interface{}{"id": n.ID, "name": n.Name, "host": n.ServerIP, "inGroup": false})
			}
		}
	}
	plan.EntryGroup = map[string]interface{}{"id": groupID, "name": leiKwanEntryGroupName, "nodes": members}

	admin := h.repo.FirstAdminUserID()
	if g, _ := h.repo.GetLeiKwanGrant(admin); g != nil {
		plan.AdminGrant = map[string]interface{}{"exists": true, "mainEntryNodeId": g.MainEntryNodeID}
	} else {
		var pick int64
		if ref != nil {
			pick = ref.nodeID
		} else if len(members) > 0 {
			pick = members[0]["id"].(int64)
		}
		name := ""
		if n, err := h.repo.GetNodeRecord(pick); err == nil && n != nil {
			name = n.Name
		}
		plan.AdminGrant = map[string]interface{}{"exists": false, "userId": admin, "userName": h.repo.LeiKwanUserNames([]int64{admin})[admin], "mainEntryNodeId": pick, "mainEntryName": name, "backupLine": true}
	}

	global := parseLeiKwanGlobal(setting.Global)
	if ref != nil {
		if ref.snap.HoldS > 0 {
			global.HoldS = ref.snap.HoldS
		}
		if ref.snap.LeaseSettleS > 0 {
			global.LeaseSettleS = ref.snap.LeaseSettleS
		}
		if ref.snap.Grace != "" {
			global.Grace = ref.snap.Grace
		}
		if ref.snap.ReturnCheckS > 0 {
			global.ReturnCheckS = ref.snap.ReturnCheckS
		}
		if v := ref.snap.Triggers["io_error"]; v != "" {
			global.IOError = v
		}
	}
	for _, hc := range hosts {
		if hc.snap.Coalesce != "" && leiKwanCoalescePattern.MatchString(hc.snap.Coalesce) {
			global.CoalesceByNode[strconv.FormatInt(hc.nodeID, 10)] = hc.snap.Coalesce
		}
	}
	plan.Global = global

	forwards := append([]leikwan.Forward(nil), view.Forwards...)
	sort.Slice(forwards, func(i, j int) bool { return forwards[i].ID < forwards[j].ID })
	lines := map[int64]*leiKwanImportLine{}
	lineOrder := []int64{}
	backupUse := map[int64][]int64{}
	for _, f := range forwards {
		item := leiKwanImportItem{ForwardID: f.ID, ListenPort: f.ListenPort, RouteID: f.Route.ID, RouteLabel: f.Route.Label, Target: net.JoinHostPort(f.Target.Host, strconv.Itoa(f.Target.Port))}
		if name, ok := pipeTunnel[f.ID]; ok {
			item.Action, item.Reason = "skip", fmt.Sprintf("管道模式隧道「%s」，保持原样（不进利群汇聚组）", name)
			plan.Items = append(plan.Items, item)
			continue
		}
		if x, _ := h.repo.GetLeiKwanRuleByForward(f.ID); x != nil {
			item.Action, item.Reason, item.RuleName = "registered", "已经是利群规则", x.Name
			plan.Items = append(plan.Items, item)
			continue
		}
		if f.State != "active" {
			item.Action, item.Reason = "skip", "利群侧状态不是 active"
			plan.Items = append(plan.Items, item)
			continue
		}
		item.Action = "import"
		line := lines[f.Route.ID]
		if line == nil {
			line = &leiKwanImportLine{RouteID: f.Route.ID, RouteLabel: f.Route.Label, Accel: defaultLeiKwanAccel()}
			if l, _ := h.repo.GetLeiKwanLineByRoute(a.ID, f.Route.ID); l != nil {
				line.Exists, line.ID, line.Name, line.Accel, line.BackupTunnelID = true, l.ID, l.Name, parseLeiKwanAccel(l.Accel), l.BackupTunnelID
			} else if n, ok := leiKwanRouteNames[f.Route.ID]; ok {
				line.Name = n
			} else {
				line.Name = "利群·" + f.Route.Label
			}
			lines[f.Route.ID] = line
			lineOrder = append(lineOrder, f.Route.ID)
		}
		line.ForwardIDs = append(line.ForwardIDs, f.ID)
		item.LineName = line.Name
		item.RuleName = h.leiKwanImportRuleName(f)
		if s, _ := h.repo.LeiKwanLandingServerByHost(f.Target.Host); s != nil {
			item.LandingHint = fmt.Sprintf("落地机「%s」已是面板落地服务器，第二步可换成面板落地节点（链接会变）", s.Name)
		}
		if ref != nil {
			for _, l := range ref.snap.Lines {
				if l.ForwardID != f.ID || l.Backup == "" {
					continue
				}
				item.BackupAddr = l.Backup
				host, portRaw, err := net.SplitHostPort(l.Backup)
				port, _ := strconv.Atoi(portRaw)
				if err == nil {
					if id, _ := h.repo.FindForwardByEntryAddr(host, port); id > 0 {
						item.BackupForward = id
						backupUse[id] = append(backupUse[id], f.ID)
					}
				}
			}
			item.CurrentOnHosts = hostValuesFor(hosts, f.ID)
		}
		plan.Items = append(plan.Items, item)
	}
	// Line defaults from the reference host: the biggest pool / the common
	// switch value of the line's forwards; rules that differ get an override.
	tunnelOf := map[int64]int64{}
	if ids := keysOf(backupUse); len(ids) > 0 {
		views, _ := h.repo.LeiKwanBackupForwards(ids)
		for id, v := range views {
			tunnelOf[id] = v.TunnelID
		}
		for i := range plan.Items {
			it := &plan.Items[i]
			if v, ok := views[it.BackupForward]; ok {
				it.BackupName = v.Name
				for _, other := range backupUse[it.BackupForward] {
					if other != it.ForwardID {
						it.BackupShared = append(it.BackupShared, other)
					}
				}
			}
		}
	}
	for _, rid := range lineOrder {
		line := lines[rid]
		if !line.Exists && ref != nil {
			line.Accel = h.leiKwanLineAccelFromHost(ref, hosts, line.ForwardIDs)
			counts := map[int64]int{}
			for _, it := range plan.Items {
				if it.RouteID == rid && it.BackupForward > 0 {
					counts[tunnelOf[it.BackupForward]]++
				}
			}
			best, bestN := int64(0), 0
			for t, n := range counts {
				if n > bestN || n == bestN && t < best {
					best, bestN = t, n
				}
			}
			line.BackupTunnelID = best
		}
		if line.BackupTunnelID > 0 {
			line.BackupTunnel = h.repo.LeiKwanTunnelName(line.BackupTunnelID)
		}
		plan.Lines = append(plan.Lines, *line)
	}
	for i := range plan.Items {
		it := &plan.Items[i]
		if it.Action != "import" || ref == nil {
			continue
		}
		it.Accel = leiKwanRuleOverrideFromHost(ref, hosts, lines[it.RouteID].Accel, it.ForwardID)
	}
	return plan, a, nil
}

func keysOf(m map[int64][]int64) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (h *Handler) leiKwanImportRuleName(f leikwan.Forward) string {
	host := f.Target.Host
	if s, _ := h.repo.LeiKwanLandingServerByHost(host); s != nil {
		host = s.Name
	} else if id, _ := h.repo.FindNodeIDByServerIP(host); id > 0 {
		if n, err := h.repo.GetNodeRecord(id); err == nil && n != nil {
			host = n.Name
		}
	} else if i := strings.Index(host, "."); i > 0 && net.ParseIP(host) == nil {
		host = host[:i]
	}
	return fmt.Sprintf("%d %s :%d", f.ID, host, f.Target.Port)
}

// A forward whose 利群加速 entry exists on only some of the hosts that run
// the line's other forwards was added by hand for a test (e.g. 540 on
// 百度云广州 only): it is registered without a warm pool.
func leiKwanTestOnly(hosts []leiKwanHostCtx, forwardID int64) (bool, []string) {
	key := strconv.FormatInt(forwardID, 10)
	with, without := []string{}, 0
	for _, hc := range hosts {
		if len(hc.snap.Warm) == 0 {
			continue // no warm pool on this host at all (stable front)
		}
		if _, ok := hc.snap.Warm[key]; ok {
			with = append(with, hc.name)
		} else {
			without++
		}
	}
	return len(with) > 0 && without > 0, with
}

func (h *Handler) leiKwanLineAccelFromHost(ref *leiKwanHostCtx, hosts []leiKwanHostCtx, forwardIDs []int64) model.LeiKwanAccel {
	a := defaultLeiKwanAccel()
	s := ref.snap
	a.Warm.Main, a.Warm.Backup = 0, 0
	enforce, wired, rcOn, rcAll := 0, 0, 0, 0
	for _, fid := range forwardIDs {
		key := strconv.FormatInt(fid, 10)
		if test, _ := leiKwanTestOnly(hosts, fid); !test {
			if n := s.Warm[key]; n > a.Warm.Main {
				a.Warm.Main = n
			}
			if n := s.Warm["backup:"+key]; n > a.Warm.Backup {
				a.Warm.Backup = n
			}
		}
		if s.SSFilter[key] == "enforce" {
			enforce++
		}
		for _, l := range s.Lines {
			if l.ForwardID == fid {
				wired++
				rcAll++
				rc := s.Triggers[key+":return_check"]
				if rc == "" {
					rc = s.Triggers["return_check"]
				}
				if rc == "on" {
					rcOn++
				}
			}
		}
	}
	if n, ok := s.Warm["backup_standby"]; ok {
		a.Warm.Standby = n
	}
	if n, ok := s.Warm["pace_ms"]; ok {
		a.Warm.PaceMs = n
	}
	if n, ok := s.Warm["headroom"]; ok {
		a.Warm.Burst = strconv.Itoa(n)
	} else {
		a.Warm.Burst = "测试中"
	}
	a.Warm.Flush = strings.Contains(s.FrontBuild, "warmflush") || strings.Contains(s.FrontBuild, "flush")
	a.SSFilter = map[bool]string{true: "enforce", false: "off"}[enforce*2 >= len(forwardIDs) && enforce > 0]
	a.Logswitch = wired > 0
	a.ReturnCheck = rcAll > 0 && rcOn*2 >= rcAll
	return a
}

func leiKwanRuleOverrideFromHost(ref *leiKwanHostCtx, hosts []leiKwanHostCtx, line model.LeiKwanAccel, forwardID int64) model.LeiKwanRuleAccel {
	s := ref.snap
	key := strconv.FormatInt(forwardID, 10)
	var o model.LeiKwanRuleAccel
	main := s.Warm[key]
	if test, on := leiKwanTestOnly(hosts, forwardID); test {
		main = 0
		o.Note = "测试规则：预热只在 " + strings.Join(on, "、") + " 上手动加过，按测试规则登记为不预热"
	}
	if main != line.Warm.Main {
		o.WarmMain = &main
		if o.Note == "" && main == 0 {
			o.Note = "主机上没开预热（落地空闲时间未测）"
		}
	}
	if f := s.SSFilter[key]; f != "" && f != line.SSFilter {
		o.SSFilter = &f
	} else if f == "" && line.SSFilter == "enforce" {
		off := "off"
		o.SSFilter = &off
	}
	wired := false
	for _, l := range s.Lines {
		wired = wired || l.ForwardID == forwardID
	}
	if wired != line.Logswitch {
		o.Logswitch = &wired
	}
	if wired {
		rc := s.Triggers[key+":return_check"]
		if rc == "" {
			rc = s.Triggers["return_check"]
		}
		on := rc == "on"
		if on != line.ReturnCheck {
			o.ReturnCheck = &on
		}
	}
	return o
}

func (h *Handler) leikwanImportPreview(w http.ResponseWriter, r *http.Request) {
	plan, _, err := h.leiKwanImportBuild(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(plan))
}

func (h *Handler) leikwanImportApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirmed  bool    `json:"confirmed"`
		ForwardIDs []int64 `json:"forwardIds"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if !req.Confirmed {
		leiKwanError(w, errors.New("请先看预览再确认导入"))
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	plan, a, err := h.leiKwanImportBuild(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	want := map[int64]bool{}
	for _, id := range req.ForwardIDs {
		want[id] = true
	}
	done := map[string]interface{}{"groupCreated": false, "nodesAdded": []string{}, "linesCreated": []string{}, "rulesImported": []int64{}, "grantCreated": false, "skipped": []string{}}
	setting, _ := h.repo.GetLeiKwanSetting()
	setting.AccountID = a.ID
	// 1. node group 「利群入口」
	groupID, _ := plan.EntryGroup["id"].(int64)
	if groupID == 0 {
		g, err := h.repo.CreateNodeGroup(leiKwanEntryGroupName, "利群客户端入口机（plan 266）", "#0d9488", 0)
		if err != nil {
			leiKwanError(w, errors.New("创建节点分组「利群入口」失败"))
			return
		}
		groupID, done["groupCreated"] = g.ID, true
	}
	added := []string{}
	for _, m := range plan.EntryGroup["nodes"].([]map[string]interface{}) {
		if in, _ := m["inGroup"].(bool); in {
			continue
		}
		id := m["id"].(int64)
		n, err := h.repo.GetNodeRecord(id)
		if err != nil || n == nil {
			continue
		}
		if cur, _ := h.repo.GetNodeGroupIDForNode(id); cur > 0 && cur != groupID {
			done["skipped"] = append(done["skipped"].([]string), fmt.Sprintf("%s 已在别的节点分组，未移动", n.Name))
			continue
		}
		gid := groupID
		if err := h.repo.AssignNodeToGroup(&id, &gid); err == nil {
			added = append(added, n.Name)
		}
	}
	done["nodesAdded"] = added
	setting.EntryGroupID = groupID
	raw, _ := json.Marshal(plan.Global)
	setting.Global = string(raw)
	if err := h.repo.SaveLeiKwanSetting(setting); err != nil {
		leiKwanError(w, errors.New("保存利群设置失败"))
		return
	}
	// 2. lines
	lineIDs := map[int64]int64{}
	created := []string{}
	for i, l := range plan.Lines {
		if l.Exists {
			lineIDs[l.RouteID] = l.ID
			continue
		}
		accel, _ := json.Marshal(l.Accel)
		line := &model.LeiKwanLine{AccountID: a.ID, RouteID: l.RouteID, Name: l.Name, RouteLabel: l.RouteLabel, Transport: "tcp+udp", BackupTunnelID: l.BackupTunnelID, Accel: string(accel), Inx: i, Status: 1}
		if err := h.repo.SaveLeiKwanLine(line); err != nil {
			leiKwanError(w, fmt.Errorf("创建专线「%s」失败", l.Name))
			return
		}
		lineIDs[l.RouteID] = line.ID
		created = append(created, l.Name)
	}
	done["linesCreated"] = created
	// 3. the admin's grant (主入口), so imported rules have an entry address
	admin := h.repo.FirstAdminUserID()
	if exists, _ := plan.AdminGrant["exists"].(bool); !exists && admin > 0 {
		pick, _ := plan.AdminGrant["mainEntryNodeId"].(int64)
		g := &model.LeiKwanGrant{UserID: admin, MainEntryNodeID: pick, BackupLine: 1, Status: 1}
		if err := h.repo.SaveLeiKwanGrant(g); err == nil {
			done["grantCreated"] = true
		}
	}
	// 4. rules (link existing backups; no LeiKwan write, no new 备线)
	imported := []int64{}
	for _, it := range plan.Items {
		if it.Action != "import" || (len(want) > 0 && !want[it.ForwardID]) {
			continue
		}
		host, portRaw, _ := net.SplitHostPort(it.Target)
		port, _ := strconv.Atoi(portRaw)
		accel, _ := json.Marshal(it.Accel)
		x := &model.LeiKwanRule{UserID: admin, LineID: lineIDs[it.RouteID], AccountID: a.ID, ForwardID: it.ForwardID, Name: it.RuleName, ListenPort: it.ListenPort,
			Transport: h.leiKwanForwardTransport(r, a, it.ForwardID), TargetHost: host, TargetPort: port, Accel: string(accel), Source: "import", Status: 1}
		if it.BackupForward > 0 {
			x.BackupForwardID, x.BackupMode = it.BackupForward, "linked"
		}
		if err := h.saveLeiKwanRule(x); err != nil {
			done["skipped"] = append(done["skipped"].([]string), fmt.Sprintf("#%d 登记失败", it.ForwardID))
			continue
		}
		imported = append(imported, it.ForwardID)
	}
	done["rulesImported"] = imported
	response.WriteJSON(w, response.OK(done))
}

func (h *Handler) leiKwanForwardTransport(r *http.Request, a *model.LeiKwanAccount, id int64) string {
	if view, err := h.leikwan.Overview(r.Context(), a.Token, a.ServiceID); err == nil {
		for _, f := range view.Forwards {
			if f.ID == id {
				return f.Transport
			}
		}
	}
	return "tcp+udp"
}
