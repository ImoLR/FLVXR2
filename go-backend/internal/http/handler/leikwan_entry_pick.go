package handler

import (
	"context"
	"errors"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 294 / 297 (im.gamer App): 复制链接 and subscriptions can point at
// 手机本地 — 127.0.0.1 = the LeiKwan client on the device itself, same vendor
// port (no offset). Which entry a phone uses is the App's own choice (bridge
// setEntry), never written to the server grant. The panel renders 手机本地 on
// request only: the link's `local` flag, and a subscription whose entry_mode
// is "local" (made / updated in the App while its entry was 手机本地). Admin
// only this round: normal users keep their grant's 主入口.

const (
	lkLocalHost = "127.0.0.1"
	lkLocalName = "手机本地"
)

// errLKLocalAdminOnly: 手机本地 is admin-only this round (plan 297).
var errLKLocalAdminOnly = errors.New("手机本地现在只给管理员用")

// lkLocalEntry: 手机本地 as an entry of the 利群 nodes (never locked by
// 「只开主入口」: that is the hosts' nft, the phone dials its own loopback).
func lkLocalEntry() *lkEntry { return &lkEntry{Host: lkLocalHost, Name: lkLocalName} }

type lkEntryCtxKey struct{}

// withLKEntry carries a 手机本地 subscription's entry into subLoad.
func withLKEntry(ctx context.Context, e *lkEntry) context.Context {
	if e == nil {
		return ctx
	}
	return context.WithValue(ctx, lkEntryCtxKey{}, e)
}

func lkEntryFrom(ctx context.Context) *lkEntry {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(lkEntryCtxKey{}).(*lkEntry)
	return e
}

// lkPhoneReq: 手机本地 as the App reports it (bridge PhoneState → the page →
// grant/preview and rule/list). Only what the capability schema needs.
type lkPhoneReq struct {
	// Warm: the App's 预热池 switch is on.
	Warm bool `json:"warm"`
	// App: the App's version (shown as the client build).
	App string `json:"app"`
	// Features (plan 301, AppInfo.features): 1.0.10-panel runs 利群原版's
	// protection engine — "logswitch", "triggers", "backup" (the phone dials
	// the 备线 forward itself). Empty = 1.0.9-panel (预热池 only).
	Features []string `json:"features,omitempty"`
}

func (p lkPhoneReq) has(f string) bool {
	for _, x := range p.Features {
		if x == f {
			return true
		}
	}
	return false
}

// full: the App runs the VPS stack (日志快切 + 快切触发 + 备线 + 预热池).
func (p lkPhoneReq) full() bool { return p.has("logswitch") && p.has("triggers") && p.has("backup") }

// lkPhoneCaps (plan 297 / 301): 手机本地 in the plan-295 capability schema.
// 1.0.9-panel: only its own 预热池. 1.0.10-panel: the same parts as 百度云广州
// (日志快切 with its triggers, 备线 dialled by the phone itself, 预热池).
// 首包过滤 is never listed (plan 301).
func lkPhoneCaps(p lkPhoneReq) lkEntryInfo {
	build := "im.gamer App"
	if v := strings.TrimSpace(p.App); v != "" && len(v) <= 40 {
		build += " " + v
	}
	c := lkEntryCaps{Name: lkLocalName, Host: lkLocalHost, Online: true, Known: true, FrontBuild: build,
		Client: "手机 App", Warm: true, Missing: []string{"日志快切", "快切触发", "手机备线"}}
	info := lkEntryInfo{caps: c}
	if p.full() {
		info.caps.Logswitch, info.caps.Triggers, info.caps.Phone = true, true, true
		info.caps.Missing = []string{}
		info.phoneDirect = true
	}
	return info
}

// lkPhoneDirectText: the 手机本地 备线 (the phone dials the 备线 forward itself).
const lkPhoneDirectText = "手机直连备线"

// lkPhoneOnHost (plan 301): what 1.0.10-panel runs for a rule — the values
// the panel hands the phone (lkPhonePlan), with the App's 预热池 switch.
func (g *lkGen) lkPhoneOnHost(x model.LeiKwanRule, ref lkEntryInfo, warmOn bool) lkOnHost {
	f := g.phoneForward(x, ref)
	v := lkOnHost{Known: true, SSFilter: "off", Logswitch: f.Logswitch, Triggers: f.Logswitch, ReturnCheck: f.ReturnCheck}
	if warmOn {
		v.Warm = f.Warm
	}
	if f.Backup != "" {
		v.Phone = lkPhoneDirectText
	}
	return v
}

// lkPhoneRuleDiff: a rule on the current 主入口 (from) vs on 手机本地 (the
// App's 预热池 on/off; nothing else of the hosts' designs — 1.0.9-panel).
// compare=false: no usable current values, only list what 手机本地 runs.
func lkPhoneRuleDiff(from lkOnHost, to lkEntryCaps, warmOn, compare bool) ([]lkSwitchChange, []string) {
	changes, kept := []lkSwitchChange{}, []string{}
	if !compare {
		if warmOn {
			kept = append(kept, "手机预热池")
		}
		return changes, kept
	}
	switch wasOn := from.Warm > 0; {
	case wasOn && warmOn:
		kept = append(kept, "预热（手机预热池）")
	case wasOn:
		changes = append(changes, lkSwitchChange{Key: lkFileWarm, Label: "预热", From: lkWarmText(from.Warm), To: "不预热", Lost: true,
			Why: "手机本地的预热池关着（手机本地详情里打开）"})
	case warmOn:
		changes = append(changes, lkSwitchChange{Key: lkFileWarm, Label: "预热", From: "不预热", To: "手机预热池", Gain: true})
	}
	// Plan 301: 首包过滤 is not compared / listed (hidden from the UI).
	if from.Logswitch {
		changes = append(changes, lkSwitchChange{Key: lkFileLogswitch, Label: "日志快切", From: lkOnOffText("日志快切", true), To: lkOnOffText("日志快切", false), Lost: true,
			Why: to.why(lkFileLogswitch) + "：断线时不会自动切到备线"})
	}
	if from.Phone != "" {
		changes = append(changes, lkSwitchChange{Key: lkFilePhone, Label: "手机备线", From: lkPhoneText(from.Phone), To: lkPhoneText(""), Lost: true, Why: to.why(lkFilePhone)})
	}
	return changes, kept
}

// leiKwanGrantPreviewPhone (plan 297, read-only): the 换主入口 preview for
// 手机本地 — what each of the user's 利群 forwards keeps / loses on the App's
// own LeiKwan client compared with the current 主入口. Nothing is saved: the
// choice lives in the App and the server 主入口 stays (other devices keep it).
func (h *Handler) leiKwanGrantPreviewPhone(userID int64, p lkPhoneReq) (map[string]interface{}, error) {
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil || setting.EntryGroupID <= 0 {
		return nil, errors.New("请先设置节点分组「利群入口」")
	}
	infos := h.leiKwanEntryInfos(h.leiKwanEntryNodes(setting.EntryGroupID))
	g, err := h.newLKGen()
	if err != nil {
		return nil, errors.New("读取利群数据失败")
	}
	phone := lkPhoneCaps(p)
	to := phone.caps
	ref := h.lkPhoneRef(g, infos, userID)
	out := map[string]interface{}{"userId": userID, "to": to, "local": true}
	notes := []string{"手机本地只在这台手机上生效：面板里的主入口不变，这个用户别的设备照常走它。"}
	cur := g.mainEntry[userID]
	fromInfo, hasFrom := infos[cur]
	if hasFrom {
		out["from"] = fromInfo.caps
	}
	compare := hasFrom && fromInfo.caps.Known
	if !compare {
		notes = append(notes, "现在的主入口不在「利群入口」组里或还没采用主机现值，没法逐项对比；下面是手机本地会跑的")
	}
	rules := []lkSwitchRule{}
	lost, gain := 0, 0
	for _, x := range g.rules {
		if x.UserID != userID {
			continue
		}
		sr := lkSwitchRule{ID: x.ID, Name: x.Name, ForwardID: x.ForwardID, LineName: g.lines[x.LineID].Name}
		from := lkOnHost{SSFilter: "off"}
		if compare {
			from = g.onHost(x, fromInfo)
		}
		if phone.phoneDirect {
			// Plan 301: 1.0.10-panel runs the VPS stack with the panel's values.
			sr.Changes, sr.Kept = lkSwitchRuleDiff(from, g.lkPhoneOnHost(x, ref, p.Warm), fromCaps(fromInfo, hasFrom), to, compare)
		} else {
			sr.Changes, sr.Kept = lkPhoneRuleDiff(from, to, p.Warm, compare)
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
	out["rules"], out["lostRules"], out["gainRules"], out["notes"], out["same"] = rules, lost, gain, notes, false
	return out, nil
}

// leiKwanRulePhoneLimits (plan 297, admin list with `phone`): per rule (plan
// 301: every row, they all follow the chosen entry) what 手机本地 cannot run — the plan-295 entryLimits, computed
// for the App's client (same ruleLimits, same chips).
func (h *Handler) leiKwanRulePhoneLimits(rules []model.LeiKwanRule, uid int64, p lkPhoneReq, c *leiKwanRuleCtx) map[int64][]lkCapLimit {
	if len(rules) == 0 {
		return nil
	}
	g, err := h.newLKGen()
	if err != nil {
		return nil
	}
	infos := h.leiKwanEntryInfos(c.entries)
	phone := lkPhoneCaps(p)
	out := map[int64][]lkCapLimit{}
	for _, x := range rules {
		// Plan 301: every row follows the chosen entry (not only own rules).
		somewhere := false
		for _, info := range infos {
			if lkPhonePort(info.prof, x) != "" {
				somewhere = true
				break
			}
		}
		out[x.ID] = g.ruleLimits(x, phone, somewhere)
	}
	return out
}

func fromCaps(info lkEntryInfo, ok bool) lkEntryCaps {
	if !ok {
		return lkEntryCaps{}
	}
	return info.caps
}
