package handler

import (
	"context"
	"errors"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 294 / 297 (im.gamer App): 复制链接 and the 利群汇聚 subscription can
// point at 手机本地 — 127.0.0.1 = the LeiKwan client on the device itself,
// same vendor port (no offset). Nothing is stored on the panel: picking
// 手机本地 is a choice kept in the App (bridge setEntry), the panel only
// renders on request — the link's `local` flag or the subscription URL's
// ?lk=local (「手机专用订阅」). Plan 297 dropped round 1's arbitrary entry
// (entryNodeId / ?lk=<node>): switching to another server entry is the normal
// 换主入口 (grant/save), as in the browser.

const (
	lkLocalHost = "127.0.0.1"
	lkLocalName = "手机本地"
	// lkSubEntryQuery is the subscription URL parameter (only "local").
	lkSubEntryQuery = "lk"
	lkSubEntryLocal = "local"
)

// parseLKLocal: ?lk=local (anything else = the owner's 主入口).
func parseLKLocal(v string) bool { return strings.TrimSpace(v) == lkSubEntryLocal }

// lkLocalEntry: 手机本地 as an entry of the 利群 nodes (never locked by
// 「只开主入口」: that is the hosts' nft, the phone dials its own loopback).
func lkLocalEntry() *lkEntry { return &lkEntry{Host: lkLocalHost, Name: lkLocalName} }

type lkEntryCtxKey struct{}

// withLKEntry carries a subscription's ?lk=local entry into subLoad.
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
}

// lkPhoneCaps (plan 297): 手机本地 in the plan-295 capability schema. The
// App's LeiKwan client has its own 预热池 and none of the hosts' designs:
// no 首包过滤, no 日志快切 / 快切触发, no 手机备线 port.
func lkPhoneCaps(p lkPhoneReq) lkEntryInfo {
	build := "im.gamer App"
	if v := strings.TrimSpace(p.App); v != "" && len(v) <= 40 {
		build += " " + v
	}
	c := lkEntryCaps{Name: lkLocalName, Host: lkLocalHost, Online: true, Known: true, FrontBuild: build,
		Client: "手机 App", Warm: true, Missing: []string{"首包过滤", "日志快切", "快切触发", "手机备线"}}
	return lkEntryInfo{caps: c}
}

// lkPhoneRuleDiff: a rule on the current 主入口 (from) vs on 手机本地 (the
// App's 预热池 on/off; nothing else of the hosts' designs). compare=false: no
// usable current values, only list what 手机本地 runs.
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
	if from.SSFilter != "" && from.SSFilter != "off" {
		changes = append(changes, lkSwitchChange{Key: lkFileSSFilter, Label: "首包过滤", From: lkFilterText(from.SSFilter), To: lkFilterText("off"), Lost: true, Why: to.why(lkFileSSFilter)})
	}
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
	to := lkPhoneCaps(p).caps
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
		sr.Changes, sr.Kept = lkPhoneRuleDiff(from, to, p.Warm, compare)
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

// leiKwanRulePhoneLimits (plan 297, admin list with `phone`): per own rule of
// the caller what 手机本地 cannot run — the plan-295 entryLimits, computed
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
		if x.UserID != uid {
			continue
		}
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
