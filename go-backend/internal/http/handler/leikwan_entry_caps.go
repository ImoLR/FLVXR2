package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 295: 利群入口 state and what each entry host can run.
//
//   - An offline 利群入口 is one attention reason 「节点离线」 (error level) in
//     the host's manage view, so the card ⚠, 「N 台需要处理」 and the H5 tab
//     count (all from leiKwanHostManageView) agree.
//   - What a host can run = the files of its learned profile (the p.Files lkGen
//     writes): 预热池 / 首包过滤 / 日志快切 / 快切触发 / 手机备线. A rule row
//     shows what its owner's 主入口 cannot run; the grant dialog previews a
//     换主入口 with lkGen's own effective values on the old and the new host.

// lkAttnOffline is the attention reason of an offline 利群入口 (the UI adds
// the since-when from the view's offlineSince).
const lkAttnOffline = "节点离线"

// lkEntryCaps: one 利群入口 for the UI (host list, grant dialog, preview).
type lkEntryCaps struct {
	NodeID       int64  `json:"nodeId"`
	Name         string `json:"name"`
	Host         string `json:"host,omitempty"`
	Online       bool   `json:"online"`
	OfflineSince int64  `json:"offlineSince,omitempty"`
	// Known: the host profile is learned (else the panel does not know what
	// the host runs and claims nothing).
	Known   bool `json:"known"`
	Managed bool `json:"managed"`
	// Front: the LeiKwan front channel the host reported (stable / test).
	Front      string   `json:"front,omitempty"`
	FrontBuild string   `json:"frontBuild,omitempty"`
	Client     string   `json:"client"`
	Warm       bool     `json:"warm"`
	SSFilter   bool     `json:"ssFilter"`
	Logswitch  bool     `json:"logswitch"`
	Triggers   bool     `json:"triggers"`
	Phone      bool     `json:"phone"`
	Missing    []string `json:"missing"`
}

// lkEntryInfo: caps plus the learned profile (phone ports, per-rule values).
type lkEntryInfo struct {
	caps lkEntryCaps
	prof *lkHostProfile
}

func lkClientLabel(front string) string {
	switch front {
	case "stable":
		return "稳定版"
	case "test":
		return "测试版"
	case "":
		return "未知"
	}
	return front
}

func lkCapsOf(e leiKwanEntryNode, host *model.LeiKwanHost) lkEntryInfo {
	c := lkEntryCaps{NodeID: e.ID, Name: e.Name, Host: e.Host, Online: e.Online, OfflineSince: e.OfflineSince, Missing: []string{}}
	info := lkEntryInfo{}
	if host != nil {
		c.Managed = host.Manage == 1
		if rep := parseLKReport(host.Report); rep != nil {
			c.Front, c.FrontBuild = rep.State.FrontChannel, rep.State.FrontBuild
		}
		if p := parseLKProfile(host.Profile); p != nil {
			info.prof = p
			c.Known = true
			c.Warm, c.SSFilter, c.Logswitch = p.Files[lkFileWarm], p.Files[lkFileSSFilter], p.Files[lkFileLogswitch]
			c.Triggers, c.Phone = p.Files[lkFileTriggers], p.Files[lkFilePhone]
			for _, f := range []struct {
				ok   bool
				name string
			}{{c.Warm, "预热池"}, {c.SSFilter, "首包过滤"}, {c.Logswitch, "日志快切"}, {c.Triggers, "快切触发"}, {c.Phone, "手机备线"}} {
				if !f.ok {
					c.Missing = append(c.Missing, f.name)
				}
			}
		}
	}
	c.Client = lkClientLabel(c.Front)
	info.caps = c
	return info
}

// why: one sentence naming the host and why it cannot run a feature.
func (c lkEntryCaps) why(key string) string { return c.whyAs("入口", key) }

// whyAs: why with the host called role (「入口」 / 「主入口」).
func (c lkEntryCaps) whyAs(role, key string) string {
	who := role + " " + c.Name + " "
	client := "的利群客户端"
	switch {
	case c.Front == "stable":
		client += "是稳定版，"
	case c.FrontBuild != "":
		client += "（" + c.FrontBuild + "）"
	default:
		client = "上"
	}
	switch key {
	case lkFileWarm:
		return who + client + "没有预热池"
	case lkFileSSFilter:
		return who + client + "没有首包过滤"
	case lkFileLogswitch:
		return who + "上没有日志快切"
	case lkFileTriggers:
		return who + "的日志快切没有触发条件文件：切回复查等条件用不了，按客户端自带的条件切"
	case lkFilePhone:
		return who + "上没有这条转发的手机备线端口"
	}
	return ""
}

// leiKwanEntryInfos: every 利群入口 (one host-table read).
func (h *Handler) leiKwanEntryInfos(entries []leiKwanEntryNode) map[int64]lkEntryInfo {
	out := map[int64]lkEntryInfo{}
	hosts := map[int64]*model.LeiKwanHost{}
	if list, err := h.repo.ListLeiKwanHosts(); err == nil {
		for i := range list {
			hosts[list[i].NodeID] = &list[i]
		}
	}
	for _, e := range entries {
		out[e.ID] = lkCapsOf(e, hosts[e.ID])
	}
	return out
}

// lkPhonePort: the host's 手机备线 port of a rule (empty = none).
func lkPhonePort(p *lkHostProfile, x model.LeiKwanRule) string {
	if p == nil || !p.Files[lkFilePhone] || x.BackupForwardID <= 0 {
		return ""
	}
	best := ""
	for port, ref := range p.Phone {
		if ref.ForwardID == x.BackupForwardID && (best == "" || port < best) {
			best = port
		}
	}
	return best
}

// lkCapLimit: something a rule's config wants that its 主入口 cannot run.
type lkCapLimit struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Why   string `json:"why"`
}

// ruleLimits (plan 295): what the rule wants (line + rule config, the same
// generic values lkGen starts from) that the owner's 主入口 cannot run.
// phoneSomewhere = the rule has a 手机备线 port on some 利群入口.
func (g *lkGen) ruleLimits(x model.LeiKwanRule, info lkEntryInfo, phoneSomewhere bool) []lkCapLimit {
	out := []lkCapLimit{}
	c := info.caps
	if !c.Known {
		return out
	}
	e := g.generic(x, c.NodeID)
	if g.mainEntry[x.UserID] != c.NodeID {
		// generic zeroes the warm pool off the 主入口; the config is what counts.
		saved := g.mainEntry[x.UserID]
		g.mainEntry[x.UserID] = c.NodeID
		e = g.generic(x, c.NodeID)
		g.mainEntry[x.UserID] = saved
	}
	if e.WarmMain > 0 && !c.Warm {
		out = append(out, lkCapLimit{Key: lkFileWarm, Label: "预热", Why: c.whyAs("主入口", lkFileWarm)})
	}
	if e.SSFilter != "off" && !c.SSFilter {
		out = append(out, lkCapLimit{Key: lkFileSSFilter, Label: "首包过滤", Why: c.whyAs("主入口", lkFileSSFilter)})
	}
	if e.Logswitch && x.ListenPort > 0 {
		if !c.Logswitch {
			out = append(out, lkCapLimit{Key: lkFileLogswitch, Label: "日志快切", Why: c.whyAs("主入口", lkFileLogswitch)})
		} else if !c.Triggers {
			out = append(out, lkCapLimit{Key: lkFileTriggers, Label: "快切触发", Why: c.whyAs("主入口", lkFileTriggers)})
		}
	}
	if phoneSomewhere && lkPhonePort(info.prof, x) == "" {
		out = append(out, lkCapLimit{Key: lkFilePhone, Label: "手机备线", Why: c.whyAs("主入口", lkFilePhone)})
	}
	return out
}

// lkOnHost: what one host runs for a rule when it is the owner's 主入口
// (lkGen effective + the host's files), for the 换主入口 preview.
type lkOnHost struct {
	Known       bool
	Warm        int
	SSFilter    string
	Logswitch   bool
	Triggers    bool
	ReturnCheck bool
	Phone       string
	// Pinned: the host profile holds a per-rule value for this file (learned
	// from the host, 「采用主机现值」), not the 专线 / 转发 setting.
	Pinned map[string]bool
}

func (g *lkGen) onHost(x model.LeiKwanRule, info lkEntryInfo) lkOnHost {
	p := info.prof
	if p == nil {
		return lkOnHost{SSFilter: "off"}
	}
	node := info.caps.NodeID
	saved, had := g.mainEntry[x.UserID]
	g.mainEntry[x.UserID] = node
	o := p.Rules[strconv.FormatInt(x.ForwardID, 10)]
	e := g.effective(x, o, node)
	if had {
		g.mainEntry[x.UserID] = saved
	} else {
		delete(g.mainEntry, x.UserID)
	}
	v := lkOnHost{Known: true, SSFilter: "off", Pinned: map[string]bool{
		lkFileWarm: o.WarmMain != nil, lkFileSSFilter: o.SSFilter != nil, lkFileLogswitch: o.Logswitch != nil || o.Backup != nil, lkFileTriggers: o.ReturnCheck != nil,
	}}
	if p.Files[lkFileWarm] {
		v.Warm = e.WarmMain
	}
	if p.Files[lkFileSSFilter] && e.SSFilter != "" {
		v.SSFilter = e.SSFilter
	}
	v.Logswitch = p.Files[lkFileLogswitch] && e.Logswitch && x.ListenPort > 0
	v.Triggers = v.Logswitch && p.Files[lkFileTriggers]
	v.ReturnCheck = v.Triggers && e.ReturnCheck
	v.Phone = lkPhonePort(p, x)
	return v
}

func lkWarmText(n int) string {
	if n > 0 {
		return fmt.Sprintf("预热 %d", n)
	}
	return "不预热"
}

func lkFilterText(m string) string {
	switch m {
	case "off", "":
		return "首包过滤 关"
	case "enforce":
		return "首包过滤 强制"
	}
	return "首包过滤 " + m
}

func lkOnOffText(label string, on bool) string {
	if on {
		return label + " 开"
	}
	return label + " 关"
}

func lkTriggerText(v lkOnHost) string {
	if !v.Triggers {
		return "按客户端自带条件"
	}
	return lkOnOffText("切回复查", v.ReturnCheck)
}

func lkPhoneText(port string) string {
	if port == "" {
		return "没有手机备线"
	}
	return "手机备线 :" + port
}

// lkSwitchChange: one feature of a rule that differs on the new 主入口.
type lkSwitchChange struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	From  string `json:"from"`
	To    string `json:"to"`
	Lost  bool   `json:"lost,omitempty"`
	Gain  bool   `json:"gain,omitempty"`
	Why   string `json:"why,omitempty"`
}

type lkSwitchRule struct {
	ID        int64            `json:"id"`
	Name      string           `json:"name"`
	ForwardID int64            `json:"forwardId"`
	LineName  string           `json:"lineName"`
	Changes   []lkSwitchChange `json:"changes"`
	Kept      []string         `json:"kept"`
}

// lkSwitchRuleDiff compares a rule on the current and on the new 主入口
// (compare=false: no usable current values, only list what the new one runs).
func lkSwitchRuleDiff(from, to lkOnHost, fromCaps, toCaps lkEntryCaps, compare bool) ([]lkSwitchChange, []string) {
	changes, kept := []lkSwitchChange{}, []string{}
	add := func(key, label, a, b string, wasOn, isOn, fileOK bool) {
		if !compare || a == b {
			if isOn {
				kept = append(kept, b)
			}
			return
		}
		c := lkSwitchChange{Key: key, Label: label, From: a, To: b, Lost: wasOn && !isOn, Gain: !wasOn && isOn}
		if !c.Gain {
			switch {
			case !fileOK || key == lkFilePhone:
				c.Why = toCaps.why(key)
			case to.Pinned[key]:
				c.Why = "入口 " + toCaps.Name + " 上这条转发单独登记了这个值（采用主机现值时记下的）"
			case from.Pinned[key]:
				c.Why = "现在的值是入口 " + fromCaps.Name + " 上单独登记的；换过去按专线 / 转发的设置"
			}
		}
		changes = append(changes, c)
	}
	p := toCaps
	add(lkFileWarm, "预热", lkWarmText(from.Warm), lkWarmText(to.Warm), from.Warm > 0, to.Warm > 0, p.Warm)
	add(lkFileSSFilter, "首包过滤", lkFilterText(from.SSFilter), lkFilterText(to.SSFilter), from.SSFilter != "off", to.SSFilter != "off", p.SSFilter)
	add(lkFileLogswitch, "日志快切", lkOnOffText("日志快切", from.Logswitch), lkOnOffText("日志快切", to.Logswitch), from.Logswitch, to.Logswitch, p.Logswitch)
	if (from.Logswitch || !compare) && to.Logswitch {
		add(lkFileTriggers, "快切触发", lkTriggerText(from), lkTriggerText(to), from.Triggers, to.Triggers, p.Triggers)
	}
	add(lkFilePhone, "手机备线", lkPhoneText(from.Phone), lkPhoneText(to.Phone), from.Phone != "", to.Phone != "", p.Phone)
	return changes, kept
}

// leikwan/grant/preview (plan 295, read-only): what each of the user's 利群
// forwards keeps / loses when the 主入口 moves to mainEntryNodeId.
func (h *Handler) leikwanGrantPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID          int64 `json:"userId"`
		MainEntryNodeID int64 `json:"mainEntryNodeId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	out, err := h.leiKwanGrantPreview(req.UserID, req.MainEntryNodeID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) leiKwanGrantPreview(userID, entryID int64) (map[string]interface{}, error) {
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil || setting.EntryGroupID <= 0 {
		return nil, errors.New("请先设置节点分组「利群入口」")
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	if leiKwanEntryByID(entries, entryID) == nil {
		return nil, errors.New("主入口必须从节点分组「利群入口」里选")
	}
	infos := h.leiKwanEntryInfos(entries)
	g, err := h.newLKGen()
	if err != nil {
		return nil, errors.New("读取利群数据失败")
	}
	toInfo := infos[entryID]
	out := map[string]interface{}{"userId": userID, "to": toInfo.caps}
	notes := []string{}
	if !toInfo.caps.Online {
		notes = append(notes, "入口 "+toInfo.caps.Name+" 离线，不能换过去")
	}
	if !toInfo.caps.Known {
		notes = append(notes, "入口 "+toInfo.caps.Name+" 还没采用主机现值，面板不知道它能跑什么")
	} else if !toInfo.caps.Managed {
		notes = append(notes, "入口 "+toInfo.caps.Name+" 的面板接管没开：面板不改它的文件，换过去后按主机现在的设置跑")
	}
	cur := g.mainEntry[userID]
	fromInfo, hasFrom := infos[cur]
	if hasFrom {
		out["from"] = fromInfo.caps
	}
	compare := hasFrom && fromInfo.caps.Known
	if cur > 0 && cur != entryID && !compare {
		notes = append(notes, "现在的主入口不在「利群入口」组里或还没采用主机现值，没法逐项对比；下面是新入口会跑的")
	}
	rules := []lkSwitchRule{}
	lost, gain := 0, 0
	for _, x := range g.rules {
		if x.UserID != userID {
			continue
		}
		sr := lkSwitchRule{ID: x.ID, Name: x.Name, ForwardID: x.ForwardID, LineName: g.lines[x.LineID].Name, Changes: []lkSwitchChange{}, Kept: []string{}}
		if cur != entryID && toInfo.caps.Known {
			from := lkOnHost{SSFilter: "off"}
			if compare {
				from = g.onHost(x, fromInfo)
			}
			sr.Changes, sr.Kept = lkSwitchRuleDiff(from, g.onHost(x, toInfo), fromInfo.caps, toInfo.caps, compare)
		}
		anyLost, anyGain := false, false
		for _, c := range sr.Changes {
			anyLost = anyLost || c.Lost
			anyGain = anyGain || c.Gain
		}
		if anyLost {
			lost++
		}
		if anyGain {
			gain++
		}
		rules = append(rules, sr)
	}
	out["rules"], out["lostRules"], out["gainRules"], out["notes"], out["same"] = rules, lost, gain, notes, cur == entryID
	return out, nil
}

// leiKwanRuleEntryLimits: per rule (admin list) what the owner's 主入口
// cannot run. nil when there is nothing to check.
func (h *Handler) leiKwanRuleEntryLimits(rules []model.LeiKwanRule, c *leiKwanRuleCtx) map[int64][]lkCapLimit {
	if len(rules) == 0 || len(c.entries) == 0 {
		return nil
	}
	g, err := h.newLKGen()
	if err != nil {
		return nil
	}
	infos := h.leiKwanEntryInfos(c.entries)
	phone := map[int64]bool{}
	for _, info := range infos {
		for _, x := range rules {
			if lkPhonePort(info.prof, x) != "" {
				phone[x.ID] = true
			}
		}
	}
	out := map[int64][]lkCapLimit{}
	for _, x := range rules {
		gr, ok := c.grants[x.UserID]
		if !ok {
			continue
		}
		info, ok := infos[gr.MainEntryNodeID]
		if !ok {
			continue
		}
		if l := g.ruleLimits(x, info, phone[x.ID]); len(l) > 0 {
			out[x.ID] = l
		}
	}
	return out
}
