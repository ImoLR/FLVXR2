package handler

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 301: 手机本地 runs what the VPS entries run. The im.gamer App
// (1.0.10-panel) carries 利群原版's protection engine (日志快切 v12 = the
// Baidu port, 备线, 预热池) and takes its values from this read-only phone
// plan: the values lkGen renders into the reference host's files (the
// owner's 主入口 when it runs the whole stack, else the first full 利群入口 —
// on prod 百度云广州), per forward as if the phone were the owner's 主入口.
// The 备线 is the 备线 forward's own entry (the phone dials it directly; IP
// literals only — the App never resolves a name). The page hands the plan
// to the App over the bridge (setPlan); nothing is written anywhere.

type lkPhoneFwd struct {
	ForwardID   int64  `json:"forwardId"`
	Port        int    `json:"port"`
	Logswitch   bool   `json:"logswitch"`
	Backup      string `json:"backup"`
	Warm        int    `json:"warm"`
	BackupWarm  int    `json:"backupWarm"`
	ReturnCheck bool   `json:"returnCheck"`
}

type lkPhonePlanWarm struct {
	Standby int  `json:"standby"`
	PaceMs  int  `json:"paceMs"`
	Bytes4  bool `json:"bytes4"`
	ZeroFin bool `json:"zeroFin"`
	Burst   bool `json:"burst"`
}

type lkPhonePlanGlobal struct {
	Triggers     map[string]string `json:"triggers"`
	HoldS        int               `json:"holdS"`
	LeaseSettleS int               `json:"leaseSettleS"`
	ReturnCheckS float64           `json:"returnCheckS"`
	Warm         lkPhonePlanWarm   `json:"warm"`
	Coalesce     bool              `json:"coalesce"`
}

type lkPhonePlanEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

type lkPhonePlan struct {
	V         int                `json:"v"`
	Ref       string             `json:"ref"`
	UpdatedAt int64              `json:"updatedAt"`
	Global    lkPhonePlanGlobal  `json:"global"`
	Forwards  []lkPhoneFwd       `json:"forwards"`
	Entries   []lkPhonePlanEntry `json:"entries"`
}

// lkPhoneTriggers: the App's LogSwitch.TRIGGERS (others are ignored there).
var lkPhoneTriggers = map[string]bool{"lapsed": true, "grace": true, "no_candidate": true, "reset2s": true, "reset1s": true,
	"zero_close": true, "eof1s": true, "timeout": true, "warm_death": true, "bytes4": true, "io_error": true}

var lkTriggerMode = regexp.MustCompile(`^(switch|record|off)$`)

// lkFullStack: a host that runs every part the phone copies.
func lkFullStack(p *lkHostProfile) bool {
	return p != nil && p.Files[lkFileWarm] && p.Files[lkFileLogswitch] && p.Files[lkFileTriggers]
}

// lkPhoneRef: the 利群入口 whose values the phone copies.
func (h *Handler) lkPhoneRef(g *lkGen, infos map[int64]lkEntryInfo, userID int64) lkEntryInfo {
	if info, ok := infos[g.mainEntry[userID]]; ok && lkFullStack(info.prof) {
		return info
	}
	ids := make([]int64, 0, len(infos))
	for id := range infos {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		if lkFullStack(infos[id].prof) {
			return infos[id]
		}
	}
	return lkEntryInfo{}
}

// lkIPPort: "ip:port" with an IP literal (v4 or v6) and a valid port.
func lkIPPort(addr string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 65536
}

func lkRecordOnly(p *lkHostProfile, fid string) bool {
	if p == nil {
		return false
	}
	for _, r := range p.RecordOnly {
		if r == fid || r == "*" {
			return true
		}
	}
	return false
}

// phoneForward: one rule as the phone runs it (the reference host's
// effective values with the phone as the owner's 主入口).
func (g *lkGen) phoneForward(x model.LeiKwanRule, ref lkEntryInfo) lkPhoneFwd {
	node := ref.caps.NodeID
	fid := strconv.FormatInt(x.ForwardID, 10)
	var o lkRuleOverride
	if ref.prof != nil {
		o = ref.prof.Rules[fid]
	}
	saved, had := g.mainEntry[x.UserID]
	g.mainEntry[x.UserID] = node
	e := g.effective(x, o, node)
	if had {
		g.mainEntry[x.UserID] = saved
	} else {
		delete(g.mainEntry, x.UserID)
	}
	f := lkPhoneFwd{ForwardID: x.ForwardID, Port: x.ListenPort, Warm: e.WarmMain}
	if e.Logswitch && x.ListenPort > 0 && lkIPPort(e.Backup) && !lkRecordOnly(ref.prof, fid) {
		f.Logswitch, f.Backup, f.BackupWarm, f.ReturnCheck = true, strings.TrimSpace(e.Backup), e.WarmBackup, e.ReturnCheck
	}
	return f
}

func lkAtoi(v string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return n
	}
	return def
}

// leiKwanPhonePlan builds the plan for userID's phone (admin only).
func (h *Handler) leiKwanPhonePlan(userID int64) (*lkPhonePlan, error) {
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil || setting.EntryGroupID <= 0 {
		return nil, errors.New("请先设置节点分组「利群入口」")
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	infos := h.leiKwanEntryInfos(entries)
	g, err := h.newLKGen()
	if err != nil {
		return nil, errors.New("读取利群数据失败")
	}
	ref := h.lkPhoneRef(g, infos, userID)
	plan := &lkPhonePlan{V: 1, Ref: ref.caps.Name, UpdatedAt: time.Now().UnixMilli(), Forwards: []lkPhoneFwd{}, Entries: []lkPhonePlanEntry{}}
	gl := lkPhonePlanGlobal{Triggers: map[string]string{}, HoldS: g.global.HoldS, LeaseSettleS: g.global.LeaseSettleS, ReturnCheckS: g.global.ReturnCheckS,
		Warm: lkPhonePlanWarm{Standby: 2, PaceMs: 100}}
	if lkTriggerMode.MatchString(g.global.Grace) {
		gl.Triggers["grace"] = g.global.Grace
	}
	if lkTriggerMode.MatchString(g.global.IOError) {
		gl.Triggers["io_error"] = g.global.IOError
	}
	if p := ref.prof; p != nil {
		trig := lkCopy(p.TrigGlobal)
		for k, v := range p.Unmanaged.Triggers {
			trig[k] = v
		}
		for k, v := range trig {
			if v = strings.TrimSpace(v); lkPhoneTriggers[k] && lkTriggerMode.MatchString(v) {
				gl.Triggers[k] = v
			}
		}
		warm := lkCopy(p.WarmExtra)
		for k, v := range p.Unmanaged.Warm {
			warm[k] = v
		}
		gl.Warm.Standby = lkAtoi(warm["backup_standby"], gl.Warm.Standby)
		gl.Warm.PaceMs = lkAtoi(warm["pace_ms"], gl.Warm.PaceMs)
		gl.Warm.Bytes4 = strings.TrimSpace(warm["bytes4"]) == "on"
		gl.Warm.ZeroFin = strings.TrimSpace(warm["zero_fin"]) == "on"
		if p.Files[lkFileCoalesce] {
			gl.Coalesce = strings.HasPrefix(g.coalesceFor(ref.caps.NodeID), "on")
		}
	}
	// Burst headroom stays off on every front (user 11-19) and is not ported.
	gl.Warm.Burst = false
	plan.Global = gl
	for _, x := range g.rules {
		if x.ListenPort <= 0 {
			continue
		}
		plan.Forwards = append(plan.Forwards, g.phoneForward(x, ref))
	}
	sort.Slice(plan.Forwards, func(a, b int) bool { return plan.Forwards[a].ForwardID < plan.Forwards[b].ForwardID })
	for _, e := range entries {
		if _, err := netip.ParseAddr(strings.Trim(e.Host, "[]")); err == nil {
			plan.Entries = append(plan.Entries, lkPhonePlanEntry{Name: e.Name, Host: strings.Trim(e.Host, "[]")})
		}
	}
	return plan, nil
}

// leikwan/phone/plan (plan 301, admin, read-only): the 手机本地 phone plan.
func (h *Handler) leikwanPhonePlan(w http.ResponseWriter, r *http.Request) {
	uid, _, err := userRoleFromRequest(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	plan, err := h.leiKwanPhonePlan(uid)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(plan))
}
