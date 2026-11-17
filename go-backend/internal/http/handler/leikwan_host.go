package handler

// Plan 269 利群主机接管 (step 2), panel side. The entry agent reports what the
// LeiKwan host files mean (secret-free) and, with the per-host 「面板接管」
// switch on, writes the panel's content when it differs. The panel's content
// per host = 专线默认 + 规则覆盖 + 全局 + the host profile learned from the host
// (only values that differ from the generic ones), so switching 接管 on changes
// nothing (semantic diff 0) and later panel changes flow to every host that
// does not pin the value.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

const (
	lkFileWarm      = "warm"
	lkFileSSFilter  = "ssfilter"
	lkFileSSKeys    = "sskeys"
	lkFileTriggers  = "triggers"
	lkFileLogswitch = "logswitch"
	lkFileCoalesce  = "coalesce"
	lkFilePhone     = "phone"
	lkSyncTimeout   = 25 * time.Second
	lkLoopEvery     = 5 * time.Minute
	lkBlockEvery    = time.Minute
)

var lkFileOrder = []string{lkFileWarm, lkFileSSFilter, lkFileSSKeys, lkFileTriggers, lkFileLogswitch, lkFileCoalesce, lkFilePhone}

var lkFileNames = map[string]string{
	lkFileWarm: "预热池", lkFileSSFilter: "首包过滤", lkFileSSKeys: "首包过滤密钥", lkFileTriggers: "日志快切触发条件",
	lkFileLogswitch: "日志快切线路", lkFileCoalesce: "首包合并", lkFilePhone: "手机备线端口",
}

// ---- agent contract (same JSON as go-gost/x/socket/leikwan_host.go) ----

type lkTokensView struct {
	Path    string            `json:"path,omitempty"`
	Present bool              `json:"present"`
	Tokens  map[string]string `json:"tokens"`
	Ignored []string          `json:"ignored,omitempty"`
}
type lkKeysView struct {
	Path    string            `json:"path,omitempty"`
	Present bool              `json:"present"`
	FP      map[string]string `json:"fp"`
	Error   string            `json:"error,omitempty"`
}
type lkLogswitchRun struct {
	Lines      []string `json:"lines"`
	RecordOnly []string `json:"recordOnly"`
}
type lkLogswitchView struct {
	Unit         string          `json:"unit,omitempty"`
	DropIn       string          `json:"dropIn,omitempty"`
	Present      bool            `json:"present"`
	Lines        []string        `json:"lines"`
	RecordOnly   []string        `json:"recordOnly"`
	Running      *lkLogswitchRun `json:"running,omitempty"`
	Build        string          `json:"build,omitempty"`
	TriggersFile string          `json:"triggersFile,omitempty"`
	Hold         string          `json:"hold,omitempty"`
	Grace        string          `json:"grace,omitempty"`
	LeaseSettle  string          `json:"leaseSettle,omitempty"`
	ReturnCheck  string          `json:"returnCheck,omitempty"`
}
type lkValueView struct {
	Path    string `json:"path,omitempty"`
	Present bool   `json:"present"`
	Value   string `json:"value"`
}
type lkPhoneView struct {
	Path     string            `json:"path,omitempty"`
	Present  bool              `json:"present"`
	Rules    map[string]string `json:"rules"`
	Comments map[string]string `json:"comments,omitempty"`
}
type lkFilesView struct {
	Warm      *lkTokensView    `json:"warm,omitempty"`
	SSFilter  *lkTokensView    `json:"ssfilter,omitempty"`
	SSKeys    *lkKeysView      `json:"sskeys,omitempty"`
	Triggers  *lkTokensView    `json:"triggers,omitempty"`
	Logswitch *lkLogswitchView `json:"logswitch,omitempty"`
	Coalesce  *lkValueView     `json:"coalesce,omitempty"`
	Phone     *lkPhoneView     `json:"phone,omitempty"`
}
type lkForwardView struct {
	ForwardID  int64  `json:"forwardId"`
	ListenPort int    `json:"listenPort"`
	Target     string `json:"target,omitempty"`
	Transport  string `json:"transport,omitempty"`
	State      string `json:"state,omitempty"`
	Policy     string `json:"policy,omitempty"`
	UDP        string `json:"udp,omitempty"`
	Running    bool   `json:"running"`
}
type lkStateView struct {
	Host          string          `json:"host,omitempty"`
	AgentVersion  string          `json:"agentVersion,omitempty"`
	CoreVersion   string          `json:"coreVersion,omitempty"`
	AgentRunning  bool            `json:"agentRunning"`
	UpdateLock    bool            `json:"updateLock"`
	AutoUpdateOff bool            `json:"autoUpdateOff"`
	SelectionMode string          `json:"selectionMode,omitempty"`
	NativeUDP     string          `json:"nativeUdp,omitempty"`
	FrontChannel  string          `json:"frontChannel,omitempty"`
	FrontBuild    string          `json:"frontBuild,omitempty"`
	FastFailover  string          `json:"fastFailover,omitempty"`
	Logswitch     string          `json:"logswitch,omitempty"`
	Coalesce      string          `json:"coalesce,omitempty"`
	PhonePorts    []int           `json:"phonePorts,omitempty"`
	Forwards      []lkForwardView `json:"forwards"`
}
type lkManageView struct {
	Manage  bool              `json:"manage"`
	Rev     int64             `json:"rev"`
	Force   int64             `json:"force"`
	Applied map[string]int64  `json:"applied,omitempty"`
	Drift   []string          `json:"drift,omitempty"`
	Pending []string          `json:"pending,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
	Written map[string]int64  `json:"written,omitempty"`
	Backup  string            `json:"backup,omitempty"`
}
type lkTrafficView struct {
	Epoch   string              `json:"epoch"`
	Ports   map[string][2]int64 `json:"ports"`
	Blocked []int               `json:"blocked,omitempty"`
	Pending bool                `json:"pending,omitempty"`
	Error   string              `json:"error,omitempty"`
}
type lkHostReport struct {
	V       int            `json:"v"`
	At      int64          `json:"at"`
	State   lkStateView    `json:"state"`
	Files   lkFilesView    `json:"files"`
	Manage  lkManageView   `json:"manage"`
	Traffic *lkTrafficView `json:"traffic,omitempty"`
}

type lkKeyEntry struct {
	Method string   `json:"method"`
	PSK    string   `json:"psk"`
	Users  []string `json:"users,omitempty"`
}
type lkWantTokens struct {
	Tokens map[string]string `json:"tokens"`
}
type lkWantKeys struct {
	Set    map[string]lkKeyEntry `json:"set,omitempty"`
	Remove []string              `json:"remove,omitempty"`
}
type lkWantLogswitch struct {
	Lines      []string `json:"lines"`
	RecordOnly []string `json:"recordOnly"`
}
type lkWantValue struct {
	Value string `json:"value"`
}
type lkWantPhone struct {
	Rules    map[string]string `json:"rules"`
	Comments map[string]string `json:"comments,omitempty"`
}
type lkDesired struct {
	Warm      *lkWantTokens    `json:"warm,omitempty"`
	SSFilter  *lkWantTokens    `json:"ssfilter,omitempty"`
	SSKeys    *lkWantKeys      `json:"sskeys,omitempty"`
	Triggers  *lkWantTokens    `json:"triggers,omitempty"`
	Logswitch *lkWantLogswitch `json:"logswitch,omitempty"`
	Coalesce  *lkWantValue     `json:"coalesce,omitempty"`
	Phone     *lkWantPhone     `json:"phone,omitempty"`
}
type lkSync struct {
	V       int        `json:"v"`
	Manage  bool       `json:"manage"`
	Rev     int64      `json:"rev"`
	Force   int64      `json:"force"`
	Desired *lkDesired `json:"desired,omitempty"`
	Traffic bool       `json:"traffic"`
	Ports   []int      `json:"ports"`
	Block   []int      `json:"block"`
	// NightOnly (global 利群 setting nightOnly): restarts / nft changes on the
	// entry hosts only 02:00–06:00 TW. Off by default (user 11-17: the 利群入口
	// machines carry no business yet).
	NightOnly bool `json:"nightOnly,omitempty"`
}

// ---- normalization (identical to the agent) ----

var (
	lkPoolKey   = regexp.MustCompile(`^(backup:)?[1-9][0-9]*$`)
	lkForwardID = regexp.MustCompile(`^[1-9][0-9]*$`)
)

func lkNormWarm(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		if lkPoolKey.MatchString(k) && v == "0" {
			continue
		}
		out[k] = v
	}
	return out
}

func lkNormSSFilter(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		if v != "off" && v != "" {
			out[k] = v
		}
	}
	return out
}

func lkNormTriggers(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		out[strings.TrimPrefix(k, "*:")] = v
	}
	return out
}

func lkNormValue(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return "off"
	}
	return v
}

func lkLineForward(item string) int64 {
	n, _ := strconv.ParseInt(strings.SplitN(item, ":", 2)[0], 10, 64)
	return n
}

func lkSortLines(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := lkLineForward(out[i]), lkLineForward(out[j])
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

func lkSortUnique(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func lkKeyFingerprint(e lkKeyEntry) string {
	sum := sha256.Sum256([]byte(e.Method + "|" + e.PSK + "|" + strings.Join(e.Users, ",")))
	return hex.EncodeToString(sum[:8])
}

func lkCopy(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---- host profile (learned) ----

type lkRuleOverride struct {
	WarmMain    *int    `json:"warmMain,omitempty"`
	WarmBackup  *int    `json:"warmBackup,omitempty"`
	SSFilter    *string `json:"ssFilter,omitempty"`
	Logswitch   *bool   `json:"logswitch,omitempty"`
	ReturnCheck *bool   `json:"returnCheck,omitempty"`
	Backup      *string `json:"backup,omitempty"`
}

func (o lkRuleOverride) empty() bool {
	return o.WarmMain == nil && o.WarmBackup == nil && o.SSFilter == nil && o.Logswitch == nil && o.ReturnCheck == nil && o.Backup == nil
}

type lkUnmanaged struct {
	Warm     map[string]string `json:"warm,omitempty"`
	SSFilter map[string]string `json:"ssfilter,omitempty"`
	Triggers map[string]string `json:"triggers,omitempty"`
	Lines    []string          `json:"lines,omitempty"`
}

type lkPhoneRef struct {
	ForwardID int64  `json:"forwardId,omitempty"`
	Target    string `json:"target,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

// lkHostProfile: what is special about one host. Files = which managed files
// this host has (a stable front has no warm / filter files, v9 logswitch no
// triggers file, …); the rest are host-wide values and per-rule exceptions.
type lkHostProfile struct {
	Files      map[string]bool           `json:"files"`
	WarmExtra  map[string]string         `json:"warmExtra,omitempty"`
	TrigGlobal map[string]string         `json:"trigGlobal,omitempty"`
	RecordOnly []string                  `json:"recordOnly,omitempty"`
	Rules      map[string]lkRuleOverride `json:"rules,omitempty"`
	Unmanaged  lkUnmanaged               `json:"unmanaged"`
	Phone      map[string]lkPhoneRef     `json:"phone,omitempty"`
}

func parseLKProfile(raw string) *lkHostProfile {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var p lkHostProfile
	if json.Unmarshal([]byte(raw), &p) != nil || p.Files == nil {
		return nil
	}
	return &p
}

// ---- generator ----

type lkRuleEff struct {
	WarmMain, WarmBackup int
	SSFilter             string
	Logswitch            bool
	ReturnCheck          bool
	Backup               string
	Key                  *lkKeyEntry
}

type lkGen struct {
	lines    map[int64]model.LeiKwanLine
	rules    []model.LeiKwanRule
	backups  map[int64]string // backup forward id → entry host:port
	byAddr   map[string]int64 // entry host:port → backup forward id
	keys     map[int64]*lkKeyEntry
	protoOK  map[int64]bool // landing node is SS2022 (first-packet filter applies)
	setting  *model.LeiKwanSetting
	global   model.LeiKwanGlobalAccel
	blocked  map[int64]bool
	allPorts []int
	// mainEntry: user → 主入口 node. The warm pool (pre-opened streams) of a
	// rule runs only on its owner's 主入口 (the cheap filter / logswitch run on
	// every entry host); learned host values pin what the hosts run today.
	mainEntry map[int64]int64
	// Plan 272 「只开主入口」: user → the old 主入口 still open in its grace.
	graceEntry map[int64]int64
}

func (h *Handler) newLKGen() (*lkGen, error) {
	g := &lkGen{lines: map[int64]model.LeiKwanLine{}, backups: map[int64]string{}, byAddr: map[string]int64{}, keys: map[int64]*lkKeyEntry{}, protoOK: map[int64]bool{}, blocked: map[int64]bool{}, mainEntry: map[int64]int64{}, graceEntry: map[int64]int64{}}
	if grants, err := h.repo.ListLeiKwanGrants(); err == nil {
		cutoff := time.Now().Add(-lkEntryGrace).UnixMilli()
		for _, gr := range grants {
			g.mainEntry[gr.UserID] = gr.MainEntryNodeID
			if gr.PrevEntryNodeID > 0 && gr.EntryChangedAt >= cutoff {
				g.graceEntry[gr.UserID] = gr.PrevEntryNodeID
			}
		}
	}
	lines, err := h.repo.ListLeiKwanLines()
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		g.lines[l.ID] = l
	}
	if g.rules, err = h.repo.ListLeiKwanRules(0); err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, x := range g.rules {
		if x.BackupForwardID > 0 {
			ids = append(ids, x.BackupForwardID)
		}
		if x.ListenPort > 0 {
			g.allPorts = append(g.allPorts, x.ListenPort)
		}
		if x.BlockReason != "" {
			g.blocked[x.ID] = true
		}
		if x.LandingNodeID > 0 {
			if _, ok := g.keys[x.LandingNodeID]; !ok {
				g.keys[x.LandingNodeID] = nil
				if n, _, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID); err == nil && n != nil {
					if n.Protocol == "ss2022" && n.ServerKey != "" {
						e := &lkKeyEntry{Method: n.Method, PSK: n.ServerKey}
						if n.UserKey != "" {
							e.Users = []string{n.UserKey}
						}
						g.keys[x.LandingNodeID] = e
					}
					g.protoOK[x.LandingNodeID] = n.Protocol == "ss2022"
				}
			}
		}
	}
	sort.Ints(g.allPorts)
	views, err := h.repo.LeiKwanBackupForwards(ids)
	if err != nil {
		return nil, err
	}
	for id, v := range views {
		if v.EntryHost != "" && v.EntryPort > 0 {
			addr := net.JoinHostPort(v.EntryHost, strconv.Itoa(v.EntryPort))
			g.backups[id] = addr
			g.byAddr[addr] = id
		}
	}
	if g.setting, err = h.repo.GetLeiKwanSetting(); err != nil {
		return nil, err
	}
	if g.setting != nil {
		g.global = parseLeiKwanGlobal(g.setting.Global)
	}
	return g, nil
}

// generic: the rule's values from panel data only (line default + rule override).
func (g *lkGen) generic(x model.LeiKwanRule, nodeID int64) lkRuleEff {
	line := g.lines[x.LineID]
	a := parseLeiKwanAccel(line.Accel)
	o := parseLeiKwanRuleAccel(x.Accel)
	e := lkRuleEff{WarmMain: a.Warm.Main, SSFilter: a.SSFilter, Backup: g.backups[x.BackupForwardID]}
	if o.WarmMain != nil {
		e.WarmMain = *o.WarmMain
	}
	if g.mainEntry[x.UserID] != nodeID {
		e.WarmMain = 0 // not the owner's 主入口: no pre-opened streams here
	}
	if o.SSFilter != nil {
		e.SSFilter = *o.SSFilter
	}
	if e.SSFilter == "" {
		e.SSFilter = "off"
	}
	ls := a.Logswitch
	if o.Logswitch != nil {
		ls = *o.Logswitch
	}
	e.Logswitch = ls && e.Backup != ""
	rc := a.ReturnCheck
	if o.ReturnCheck != nil {
		rc = *o.ReturnCheck
	}
	e.ReturnCheck = rc && e.Logswitch
	if e.WarmMain > 0 && e.Logswitch {
		e.WarmBackup = a.Warm.Backup
	}
	if x.LandingNodeID > 0 {
		e.Key = g.keys[x.LandingNodeID]
		if ok, known := g.protoOK[x.LandingNodeID]; known && !ok {
			// VLESS / AnyTLS / … clients would look like junk to the SS2022 filter.
			e.SSFilter = "off"
		}
	}
	if x.ExternalNodeID > 0 {
		// Plan 272: a 外部节点 landing (keys not on the panel's filter list).
		e.SSFilter = "off"
	}
	return e
}

func (g *lkGen) effective(x model.LeiKwanRule, o lkRuleOverride, nodeID int64) lkRuleEff {
	e := g.generic(x, nodeID)
	line := g.lines[x.LineID]
	a := parseLeiKwanAccel(line.Accel)
	ro := parseLeiKwanRuleAccel(x.Accel)
	if o.Backup != nil {
		e.Backup = *o.Backup
	}
	if o.Logswitch != nil {
		e.Logswitch = *o.Logswitch && e.Backup != ""
	}
	rc := a.ReturnCheck
	if ro.ReturnCheck != nil {
		rc = *ro.ReturnCheck
	}
	if o.ReturnCheck != nil {
		rc = *o.ReturnCheck
	}
	e.ReturnCheck = rc && e.Logswitch
	if o.WarmMain != nil {
		e.WarmMain = *o.WarmMain
	}
	e.WarmBackup = 0
	if e.WarmMain > 0 && e.Logswitch {
		e.WarmBackup = a.Warm.Backup
	}
	if o.WarmBackup != nil {
		e.WarmBackup = *o.WarmBackup
	}
	if o.SSFilter != nil {
		e.SSFilter = *o.SSFilter
	}
	return e
}

func (g *lkGen) coalesceFor(nodeID int64) string {
	if v := strings.TrimSpace(g.global.CoalesceByNode[strconv.FormatInt(nodeID, 10)]); v != "" {
		return lkNormValue(v)
	}
	return lkNormValue(g.global.Coalesce)
}

// desired renders the panel's content for one host (nil profile = nothing).
func (g *lkGen) desired(nodeID int64, p *lkHostProfile) *lkDesired {
	if p == nil {
		return nil
	}
	d := &lkDesired{}
	effs := make([]lkRuleEff, len(g.rules))
	for i, x := range g.rules {
		effs[i] = g.effective(x, p.Rules[strconv.FormatInt(x.ForwardID, 10)], nodeID)
	}
	if p.Files[lkFileWarm] {
		t := lkCopy(p.WarmExtra)
		for k, v := range p.Unmanaged.Warm {
			t[k] = v
		}
		for i, x := range g.rules {
			fid := strconv.FormatInt(x.ForwardID, 10)
			if effs[i].WarmMain > 0 {
				t[fid] = strconv.Itoa(effs[i].WarmMain)
			}
			if effs[i].WarmBackup > 0 {
				t["backup:"+fid] = strconv.Itoa(effs[i].WarmBackup)
			}
		}
		d.Warm = &lkWantTokens{Tokens: t}
	}
	if p.Files[lkFileSSFilter] {
		t := lkCopy(p.Unmanaged.SSFilter)
		for i, x := range g.rules {
			if m := effs[i].SSFilter; m != "" && m != "off" {
				t[strconv.FormatInt(x.ForwardID, 10)] = m
			}
		}
		d.SSFilter = &lkWantTokens{Tokens: t}
	}
	if p.Files[lkFileSSKeys] {
		k := &lkWantKeys{}
		for i, x := range g.rules {
			if effs[i].Key != nil && effs[i].SSFilter != "off" {
				if k.Set == nil {
					k.Set = map[string]lkKeyEntry{}
				}
				k.Set[strconv.FormatInt(x.ForwardID, 10)] = *effs[i].Key
			}
		}
		d.SSKeys = k
	}
	if p.Files[lkFileTriggers] {
		t := lkCopy(p.TrigGlobal)
		for k, v := range p.Unmanaged.Triggers {
			t[k] = v
		}
		globalOn := t["return_check"] == "on"
		for i, x := range g.rules {
			if !effs[i].Logswitch {
				continue
			}
			key := strconv.FormatInt(x.ForwardID, 10) + ":return_check"
			if effs[i].ReturnCheck && !globalOn {
				t[key] = "on"
			} else if !effs[i].ReturnCheck && globalOn {
				t[key] = "off"
			}
		}
		d.Triggers = &lkWantTokens{Tokens: t}
	}
	if p.Files[lkFileLogswitch] {
		lines := append([]string{}, p.Unmanaged.Lines...)
		for i, x := range g.rules {
			if effs[i].Logswitch && x.ListenPort > 0 {
				lines = append(lines, fmt.Sprintf("%d:%d>%s", x.ForwardID, x.ListenPort, effs[i].Backup))
			}
		}
		d.Logswitch = &lkWantLogswitch{Lines: lkSortLines(lines), RecordOnly: lkSortUnique(p.RecordOnly)}
	}
	if p.Files[lkFileCoalesce] {
		d.Coalesce = &lkWantValue{Value: g.coalesceFor(nodeID)}
	}
	if p.Files[lkFilePhone] {
		ph := &lkWantPhone{Rules: map[string]string{}, Comments: map[string]string{}}
		for port, ref := range p.Phone {
			target := ref.Target
			if ref.ForwardID > 0 {
				if a := g.backups[ref.ForwardID]; a != "" {
					target = a
				}
			}
			if target == "" {
				continue
			}
			ph.Rules[port] = target
			if ref.Comment != "" {
				ph.Comments[port] = ref.Comment
			}
		}
		d.Phone = ph
	}
	return d
}

// desiredFor (plan 276, F08): desired plus the first-packet filter keys the
// host still has but no rule wants any more (sskeys "remove"):
//   - a panel rule whose key the panel supplies (panel landing node / 外部节点)
//     but whose filter is off on this host (e.g. switched to a 外部节点);
//   - a forward no panel rule has, not kept as unmanaged (its ssfilter line),
//     and no longer configured on the host (deleted on the LeiKwan side).
//
// Keys of manual-landing rules (only the host has them) and of forwards still
// on the host are never removed. learn() keeps using desired (no removals).
func (g *lkGen) desiredFor(nodeID int64, p *lkHostProfile, rep *lkHostReport) *lkDesired {
	d := g.desired(nodeID, p)
	if d == nil || d.SSKeys == nil || rep == nil || rep.Files.SSKeys == nil || len(rep.Files.SSKeys.FP) == 0 {
		return d
	}
	byFid := map[string]int{}
	for i, x := range g.rules {
		byFid[strconv.FormatInt(x.ForwardID, 10)] = i
	}
	onHost := map[string]bool{}
	for _, f := range rep.State.Forwards {
		onHost[strconv.FormatInt(f.ForwardID, 10)] = true
	}
	// The agent lists at most 64 forwards; an empty or full list says nothing.
	complete := len(rep.State.Forwards) > 0 && len(rep.State.Forwards) < 64
	remove := []string{}
	for fid := range rep.Files.SSKeys.FP {
		if !lkForwardID.MatchString(fid) {
			continue
		}
		if _, wanted := d.SSKeys.Set[fid]; wanted {
			continue
		}
		if i, ok := byFid[fid]; ok {
			x := g.rules[i]
			if x.LandingNodeID <= 0 && x.ExternalNodeID <= 0 {
				continue // manual landing: the host's key is the only copy
			}
			if e := g.effective(x, p.Rules[fid], nodeID); e.SSFilter == "" || e.SSFilter == "off" {
				remove = append(remove, fid)
			}
			continue
		}
		if _, kept := p.Unmanaged.SSFilter[fid]; kept {
			continue
		}
		if complete && !onHost[fid] {
			remove = append(remove, fid)
		}
	}
	sort.Slice(remove, func(i, j int) bool {
		a, _ := strconv.ParseInt(remove[i], 10, 64)
		b, _ := strconv.ParseInt(remove[j], 10, 64)
		return a < b
	})
	if len(remove) > 0 {
		d.SSKeys.Remove = remove
	}
	return d
}

// learn builds the host profile from a report: host-wide values verbatim,
// per-rule values only where the host differs from the generic ones. The
// result must render back to exactly what the host has.
func (g *lkGen) learn(nodeID int64, rep *lkHostReport) (*lkHostProfile, string, []lkFileDiff, error) {
	f := rep.Files
	p := &lkHostProfile{Files: map[string]bool{}, WarmExtra: map[string]string{}, TrigGlobal: map[string]string{}, Rules: map[string]lkRuleOverride{},
		Unmanaged: lkUnmanaged{Warm: map[string]string{}, SSFilter: map[string]string{}, Triggers: map[string]string{}}, Phone: map[string]lkPhoneRef{}}
	byFid := map[string]model.LeiKwanRule{}
	for _, x := range g.rules {
		byFid[strconv.FormatInt(x.ForwardID, 10)] = x
	}
	warm, ssf, trig := map[string]string{}, map[string]string{}, map[string]string{}
	if f.Warm != nil {
		p.Files[lkFileWarm] = true
		warm = lkNormWarm(f.Warm.Tokens)
		for k, v := range warm {
			if lkPoolKey.MatchString(k) {
				if _, ok := byFid[strings.TrimPrefix(k, "backup:")]; !ok {
					p.Unmanaged.Warm[k] = v
				}
				continue
			}
			p.WarmExtra[k] = v
		}
	}
	if f.SSFilter != nil {
		p.Files[lkFileSSFilter] = true
		ssf = lkNormSSFilter(f.SSFilter.Tokens)
		for k, v := range ssf {
			if _, ok := byFid[k]; !ok {
				p.Unmanaged.SSFilter[k] = v
			}
		}
	}
	if f.SSKeys != nil {
		p.Files[lkFileSSKeys] = true
	}
	hostLines := map[string]string{}
	if f.Logswitch != nil && f.Logswitch.Present {
		p.Files[lkFileLogswitch] = true
		p.RecordOnly = lkSortUnique(f.Logswitch.RecordOnly)
		for _, item := range f.Logswitch.Lines {
			fid := strconv.FormatInt(lkLineForward(item), 10)
			_, backup, _ := strings.Cut(item, ">")
			if x, ok := byFid[fid]; ok && strings.HasPrefix(item, fmt.Sprintf("%d:%d>", x.ForwardID, x.ListenPort)) {
				hostLines[fid] = backup
				continue
			}
			p.Unmanaged.Lines = append(p.Unmanaged.Lines, item)
		}
	}
	if f.Triggers != nil {
		p.Files[lkFileTriggers] = true
		trig = lkNormTriggers(f.Triggers.Tokens)
		for k, v := range trig {
			fid, key, per := strings.Cut(k, ":")
			if !per {
				p.TrigGlobal[k] = v
				continue
			}
			if _, ok := byFid[fid]; ok && key == "return_check" {
				if _, wired := hostLines[fid]; wired {
					continue
				}
			}
			p.Unmanaged.Triggers[k] = v
		}
	}
	coalesce := ""
	if f.Coalesce != nil {
		p.Files[lkFileCoalesce] = true
		coalesce = lkNormValue(f.Coalesce.Value)
	}
	if f.Phone != nil && f.Phone.Present {
		p.Files[lkFilePhone] = true
		for port, target := range f.Phone.Rules {
			ref := lkPhoneRef{Comment: f.Phone.Comments[port]}
			if id, ok := g.byAddr[target]; ok {
				ref.ForwardID = id
			} else {
				ref.Target = target
			}
			p.Phone[port] = ref
		}
	}
	globalOn := trig["return_check"] == "on"
	for _, x := range g.rules {
		fid := strconv.FormatInt(x.ForwardID, 10)
		var o lkRuleOverride
		e := g.effective(x, o, nodeID)
		if p.Files[lkFileLogswitch] {
			backup, wired := hostLines[fid]
			if wired != e.Logswitch || (wired && backup != e.Backup) {
				w := wired
				o.Logswitch = &w
				if wired && backup != e.Backup {
					b := backup
					o.Backup = &b
				}
				e = g.effective(x, o, nodeID)
			}
		}
		if p.Files[lkFileWarm] {
			hw, _ := strconv.Atoi(warm[fid])
			if hw != e.WarmMain {
				o.WarmMain = &hw
				e = g.effective(x, o, nodeID)
			}
			hb, _ := strconv.Atoi(warm["backup:"+fid])
			if hb != e.WarmBackup {
				o.WarmBackup = &hb
				e = g.effective(x, o, nodeID)
			}
		}
		if p.Files[lkFileSSFilter] {
			hs := ssf[fid]
			if hs == "" {
				hs = "off"
			}
			es := e.SSFilter
			if es == "" {
				es = "off"
			}
			if hs != es {
				o.SSFilter = &hs
				e = g.effective(x, o, nodeID)
			}
		}
		if p.Files[lkFileTriggers] && e.Logswitch {
			hrc := globalOn
			if v, ok := trig[fid+":return_check"]; ok {
				hrc = v == "on"
			}
			if hrc != e.ReturnCheck {
				o.ReturnCheck = &hrc
				e = g.effective(x, o, nodeID)
			}
		}
		if !o.empty() {
			p.Rules[fid] = o
		}
	}
	// The generated content must equal the host (semantic diff 0).
	saved := g.global
	if p.Files[lkFileCoalesce] {
		if g.global.CoalesceByNode == nil {
			g.global.CoalesceByNode = map[string]string{}
		} else {
			g.global.CoalesceByNode = lkCopy(g.global.CoalesceByNode)
		}
		g.global.CoalesceByNode[strconv.FormatInt(nodeID, 10)] = coalesce
	}
	diffs := lkDiffFiles(f, g.desired(nodeID, p))
	g.global = saved
	for _, d := range diffs {
		if !d.Same {
			return nil, "", diffs, fmt.Errorf("学习后「%s」仍和主机不同（面板逻辑没覆盖到），没有保存", lkFileNames[d.File])
		}
	}
	return p, coalesce, diffs, nil
}

// ---- diff ----

type lkFileDiff struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	Path    string   `json:"path,omitempty"`
	Managed bool     `json:"managed"`
	Same    bool     `json:"same"`
	Lines   []string `json:"lines,omitempty"` // "- host" / "+ panel"
	Restart bool     `json:"restart,omitempty"`
}

func lkMapDiff(host, want map[string]string) []string {
	keys := map[string]bool{}
	for k := range host {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	sort.Strings(list)
	out := []string{}
	for _, k := range list {
		hv, hok := host[k]
		wv, wok := want[k]
		switch {
		case hok && !wok:
			out = append(out, "- "+k+"="+hv)
		case !hok && wok:
			out = append(out, "+ "+k+"="+wv)
		case hv != wv:
			out = append(out, "- "+k+"="+hv, "+ "+k+"="+wv)
		}
	}
	return out
}

func lkListDiff(host, want []string) []string {
	hs, ws := map[string]bool{}, map[string]bool{}
	for _, s := range host {
		hs[s] = true
	}
	for _, s := range want {
		ws[s] = true
	}
	out := []string{}
	for _, s := range host {
		if !ws[s] {
			out = append(out, "- "+s)
		}
	}
	for _, s := range want {
		if !hs[s] {
			out = append(out, "+ "+s)
		}
	}
	return out
}

// lkDiffFiles compares the host's files with the panel's content (nil
// desired = nothing managed).
func lkDiffFiles(f lkFilesView, d *lkDesired) []lkFileDiff {
	out := []lkFileDiff{}
	if d == nil {
		return out
	}
	add := func(file, path string, lines []string, restart bool) {
		out = append(out, lkFileDiff{File: file, Name: lkFileNames[file], Path: path, Managed: true, Same: len(lines) == 0, Lines: lines, Restart: restart && len(lines) > 0})
	}
	tokens := func(file string, h *lkTokensView, w *lkWantTokens, norm func(map[string]string) map[string]string) {
		if w == nil {
			return
		}
		if h == nil {
			add(file, "", []string{"主机上没有这个文件"}, false)
			return
		}
		lines := lkMapDiff(norm(h.Tokens), norm(w.Tokens))
		if !h.Present && len(norm(w.Tokens)) > 0 {
			lines = append([]string{"主机文件不存在，会新建"}, lines...)
		}
		add(file, h.Path, lines, false)
	}
	tokens(lkFileWarm, f.Warm, d.Warm, lkNormWarm)
	tokens(lkFileSSFilter, f.SSFilter, d.SSFilter, lkNormSSFilter)
	if d.SSKeys != nil {
		if f.SSKeys == nil {
			add(lkFileSSKeys, "", []string{"主机上没有密钥文件"}, false)
		} else {
			lines := []string{}
			ids := make([]string, 0, len(d.SSKeys.Set))
			for fid := range d.SSKeys.Set {
				ids = append(ids, fid)
			}
			sort.Strings(ids)
			for _, fid := range ids {
				want := lkKeyFingerprint(d.SSKeys.Set[fid])
				if have := f.SSKeys.FP[fid]; have != want {
					if have == "" {
						have = "无"
					}
					lines = append(lines, fmt.Sprintf("~ %s 密钥 主机 %s → 面板 %s（只比指纹）", fid, have, want))
				}
			}
			for _, fid := range d.SSKeys.Remove {
				if _, ok := f.SSKeys.FP[fid]; ok {
					lines = append(lines, "- "+fid+" 密钥")
				}
			}
			add(lkFileSSKeys, f.SSKeys.Path, lines, false)
		}
	}
	tokens(lkFileTriggers, f.Triggers, d.Triggers, lkNormTriggers)
	if d.Logswitch != nil {
		if f.Logswitch == nil || !f.Logswitch.Present {
			add(lkFileLogswitch, "", []string{"主机上没有日志快切"}, true)
		} else {
			lines := lkListDiff(lkSortLines(f.Logswitch.Lines), lkSortLines(d.Logswitch.Lines))
			for _, s := range lkListDiff(lkSortUnique(f.Logswitch.RecordOnly), lkSortUnique(d.Logswitch.RecordOnly)) {
				lines = append(lines, s[:2]+"只记录 "+s[2:])
			}
			add(lkFileLogswitch, f.Logswitch.Unit, lines, true)
		}
	}
	if d.Coalesce != nil {
		if f.Coalesce == nil {
			add(lkFileCoalesce, "", []string{"主机前端不读首包合并文件"}, false)
		} else if lkNormValue(f.Coalesce.Value) != lkNormValue(d.Coalesce.Value) {
			add(lkFileCoalesce, f.Coalesce.Path, []string{"- " + lkNormValue(f.Coalesce.Value), "+ " + lkNormValue(d.Coalesce.Value)}, false)
		} else {
			add(lkFileCoalesce, f.Coalesce.Path, nil, false)
		}
	}
	if d.Phone != nil {
		if f.Phone == nil || !f.Phone.Present {
			lines := []string{}
			if len(d.Phone.Rules) > 0 {
				lines = []string{"主机上没有手机备线文件"}
			}
			add(lkFilePhone, "", lines, true)
		} else {
			add(lkFilePhone, f.Phone.Path, lkMapDiff(f.Phone.Rules, d.Phone.Rules), true)
		}
	}
	return out
}

// ---- push / ingest ----

var lkHostLocks sync.Map // node id → *sync.Mutex

func lkHostLock(nodeID int64) *sync.Mutex {
	v, _ := lkHostLocks.LoadOrStore(nodeID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func lkHash(v interface{}) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// leiKwanEntryNodeIDs: the nodes of the 「利群入口」 group.
func (h *Handler) leiKwanEntryNodeIDs() []int64 {
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil || setting.EntryGroupID <= 0 {
		return nil
	}
	out := []int64{}
	for _, e := range h.leiKwanEntryNodes(setting.EntryGroupID) {
		out = append(out, e.ID)
	}
	return out
}

func lkContains(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// leiKwanHostSyncPayload refreshes the host's revision (the desired content
// changed → rev+1) and returns what the agent gets.
func (h *Handler) leiKwanHostSyncPayload(host *model.LeiKwanHost, g *lkGen) (lkSync, *lkDesired, error) {
	prof := parseLKProfile(host.Profile)
	d := g.desiredFor(host.NodeID, prof, parseLKReport(host.Report))
	if hash := lkHash(d); hash != host.DesiredHash {
		host.DesiredHash = hash
		host.Rev++
		if err := h.repo.SaveLeiKwanHostConfig(host); err != nil {
			return lkSync{}, nil, err
		}
	}
	s := lkSync{V: 1, Manage: host.Manage == 1 && d != nil, Rev: host.Rev, Force: host.Force, Traffic: host.Traffic == 1, Ports: g.allPorts, Block: []int{}, NightOnly: g.global.NightOnly}
	if s.Ports == nil {
		s.Ports = []int{}
	}
	if s.Manage {
		s.Desired = d
	}
	s.Block = g.blockFor(host.NodeID)
	return s, d, nil
}

// blockFor: the 利群 ports this entry host drops (quota / expiry blocks on
// every host; plan 272 「只开主入口」 per host).
func (g *lkGen) blockFor(nodeID int64) []int {
	out := []int{}
	for _, x := range g.rules {
		if (g.blocked[x.ID] || g.entryLocked(x, nodeID)) && x.ListenPort > 0 {
			out = append(out, x.ListenPort)
		}
	}
	sort.Ints(out)
	return out
}

// LeiKwanHostBlockPorts: what LeiKwanHostSync would block on this host now
// (tests, diagnostics; no push, no rev change).
func (h *Handler) LeiKwanHostBlockPorts(nodeID int64) ([]int, error) {
	g, err := h.newLKGen()
	if err != nil {
		return nil, err
	}
	return g.blockFor(nodeID), nil
}

// lkEntryGrace: how long the old 主入口 stays open after a change when the
// owner does not refresh a subscription earlier (plan 272).
const lkEntryGrace = 24 * time.Hour

// entryLocked (plan 272 「只开主入口」): the rule's port is closed on this
// 利群入口 host because it is not the owner's 主入口. Open: lock off, rule
// exempt (「所有利群入口都可用」), owner without a 主入口, the 主入口 itself,
// or the previous 主入口 during its grace.
func (g *lkGen) entryLocked(x model.LeiKwanRule, nodeID int64) bool {
	if !g.global.EntryLock || x.AllEntries == 1 {
		return false
	}
	main := g.mainEntry[x.UserID]
	if main <= 0 || main == nodeID {
		return false
	}
	return g.graceEntry[x.UserID] != nodeID
}

// leiKwanHostPush sends one host its LeiKwanHostSync and ingests the answer.
func (h *Handler) leiKwanHostPush(nodeID int64) error {
	if h == nil || h.repo == nil || h.wsServer == nil || nodeID <= 0 {
		return nil
	}
	mu := lkHostLock(nodeID)
	mu.Lock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err != nil {
		mu.Unlock()
		return err
	}
	g, err := h.newLKGen()
	if err != nil {
		mu.Unlock()
		return err
	}
	s, _, err := h.leiKwanHostSyncPayload(host, g)
	mu.Unlock()
	if err != nil {
		return err
	}
	if !h.wsServer.NodeOnline(nodeID) {
		return errors.New("节点不在线，上线后自动同步")
	}
	if host.ReportedAt <= 0 {
		// Old agents do not know LeiKwanHostSync: wait for the first report.
		return errors.New("agent 还没上报利群主机状态（需要新版 agent）")
	}
	res, err := h.wsServer.SendCommand(nodeID, "LeiKwanHostSync", s, lkSyncTimeout)
	now := time.Now().UnixMilli()
	if err != nil {
		_ = h.repo.SaveLeiKwanHostSync(nodeID, now, err.Error())
		return err
	}
	if !res.Success {
		msg := res.Message
		if msg == "" {
			msg = "agent 没有接受同步"
		}
		_ = h.repo.SaveLeiKwanHostSync(nodeID, now, msg)
		return errors.New(msg)
	}
	_ = h.repo.SaveLeiKwanHostSync(nodeID, now, "")
	if len(res.Data) > 0 {
		raw, _ := json.Marshal(res.Data)
		h.leiKwanHostIngest(nodeID, raw)
	}
	return nil
}

// IngestLeiKwanHostReport is the ws hook's entry point (also used by tests).
func (h *Handler) IngestLeiKwanHostReport(nodeID int64, raw json.RawMessage) {
	h.leiKwanHostIngest(nodeID, raw)
}

// ReconcileLeiKwanBlocks recomputes the 利群 block state now (tests, jobs).
func (h *Handler) ReconcileLeiKwanBlocks() { h.leiKwanReconcileBlocks() }

// leiKwanHostIngest stores an agent report (system info or sync answer),
// refreshes the step-1 host status and bills the traffic counters.
func (h *Handler) leiKwanHostIngest(nodeID int64, raw json.RawMessage) {
	if h == nil || h.repo == nil || len(raw) == 0 {
		return
	}
	var rep lkHostReport
	if err := json.Unmarshal(raw, &rep); err != nil || rep.V != 1 {
		return
	}
	check := rep
	check.State.Host = ""
	if probe, _ := json.Marshal(check); leiKwanSecretLike(probe) {
		log.Printf("node %d: 利群主机上报里有疑似密钥的字符串，已丢弃", nodeID)
		return
	}
	mu := lkHostLock(nodeID)
	mu.Lock()
	defer mu.Unlock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	trimmed := rep
	if rep.Traffic != nil {
		// Keep the table state for the UI; the counters live in counters/epoch.
		trimmed.Traffic = &lkTrafficView{Epoch: rep.Traffic.Epoch, Blocked: rep.Traffic.Blocked, Pending: rep.Traffic.Pending, Error: rep.Traffic.Error, Ports: map[string][2]int64{}}
	}
	stored, _ := json.Marshal(trimmed)
	_ = h.repo.SaveLeiKwanHostReport(nodeID, string(stored), now)
	if snap, err := json.Marshal(lkSnapshotFromReport(&rep, now)); err == nil {
		_ = h.repo.SaveLeiKwanHostState(nodeID, string(snap), "agent", now)
	}
	if rep.Traffic != nil && rep.Traffic.Epoch != "" {
		if err := h.leiKwanBillTraffic(host, rep.Traffic); err != nil {
			log.Printf("node %d: 利群流量入账失败: %v", nodeID, err)
		}
	}
	// A (re)started agent has no sync yet (or an old one): send it now, at
	// most every 30 s per node.
	if rep.Manage.Rev != host.Rev || rep.Manage.Force != host.Force || rep.Manage.Manage != (host.Manage == 1 && host.Profile != "") {
		if last, ok := lkResync.Load(nodeID); !ok || time.Since(last.(time.Time)) > 30*time.Second {
			lkResync.Store(nodeID, time.Now())
			go func() {
				if err := h.leiKwanHostPush(nodeID); err != nil {
					log.Printf("node %d: 利群主机同步失败: %v", nodeID, err)
				}
			}()
		}
	}
}

var lkResync sync.Map // node id → time of the last report-triggered push

// lkSnapshotFromReport maps the agent report onto the step-1 snapshot shape
// (the 利群入口 tab and the rules' 「主机现值」 keep working unchanged).
func lkSnapshotFromReport(rep *lkHostReport, now int64) leiKwanHostSnapshot {
	st, f := rep.State, rep.Files
	s := leiKwanHostSnapshot{Host: st.Host, CapturedAt: time.UnixMilli(now).UTC().Add(8 * time.Hour).Format("2006-01-02T15:04:05+08:00"),
		AgentVersion: st.AgentVersion, CoreVersion: st.CoreVersion, UpdateLock: st.UpdateLock, NativeUDP: st.NativeUDP == "all",
		SelectionMode: st.SelectionMode, FrontChannel: st.FrontChannel, FrontBuild: st.FrontBuild, FastFailover: st.FastFailover,
		Logswitch: st.Logswitch, Coalesce: st.Coalesce, Warm: map[string]int{}, SSFilter: map[string]string{}, Triggers: map[string]string{},
		Lines: []leiKwanHostLine{}, Forwards: []leiKwanHostForward{}, PhoneBackup: []leiKwanHostPhone{}, Extra: []string{}}
	ready := 0
	for _, fw := range st.Forwards {
		state := fw.State
		if !fw.Running {
			state = "stopped"
		}
		if fw.State == "ready" && fw.Running {
			ready++
		}
		s.Forwards = append(s.Forwards, leiKwanHostForward{ForwardID: fw.ForwardID, ListenPort: fw.ListenPort, Target: fw.Target, Transport: fw.Transport, State: state})
	}
	s.ForwardsReady = fmt.Sprintf("%d/%d ready", ready, len(st.Forwards))
	if f.Warm != nil {
		for k, v := range f.Warm.Tokens {
			if n, err := strconv.Atoi(v); err == nil {
				s.Warm[k] = n
			}
		}
	}
	if f.SSFilter != nil {
		s.SSFilter = lkCopy(f.SSFilter.Tokens)
	}
	if f.Triggers != nil {
		s.Triggers = lkCopy(f.Triggers.Tokens)
	}
	if ls := f.Logswitch; ls != nil {
		s.LogswitchRecordOnly = ls.RecordOnly
		s.HoldS, _ = strconv.Atoi(ls.Hold)
		s.LeaseSettleS, _ = strconv.Atoi(ls.LeaseSettle)
		s.Grace = ls.Grace
		s.ReturnCheckS, _ = strconv.ParseFloat(ls.ReturnCheck, 64)
		for _, item := range ls.Lines {
			head, backup, _ := strings.Cut(item, ">")
			fid, port, _ := strings.Cut(head, ":")
			id, _ := strconv.ParseInt(fid, 10, 64)
			p, _ := strconv.Atoi(port)
			s.Lines = append(s.Lines, leiKwanHostLine{ForwardID: id, ListenPort: p, Backup: backup})
		}
		if ls.Running != nil {
			s.Extra = append(s.Extra, "日志快切 unit 改过但还没重启（运行中的仍是旧线路）")
		}
	}
	if f.Phone != nil {
		ports := make([]string, 0, len(f.Phone.Rules))
		for p := range f.Phone.Rules {
			ports = append(ports, p)
		}
		sort.Strings(ports)
		for _, p := range ports {
			n, _ := strconv.Atoi(p)
			s.PhoneBackup = append(s.PhoneBackup, leiKwanHostPhone{Port: n, Target: f.Phone.Rules[p], Note: f.Phone.Comments[p]})
		}
	}
	if !st.AgentRunning {
		s.Extra = append(s.Extra, "厂商 agent 没在运行")
	}
	if st.AutoUpdateOff {
		s.Extra = append(s.Extra, "厂商自动更新已关（disable_auto_update）")
	}
	return s
}

// ---- background ----

func (h *Handler) leiKwanHostKick() {
	if h == nil {
		return
	}
	select {
	case h.lkKick <- struct{}{}:
	default:
	}
}

// runLeiKwanHostLoop: every 5 min (or on a kick) reconcile the 利群 blocks
// and push every 利群入口 host its sync. Idle panels without 利群入口 do nothing.
func (h *Handler) runLeiKwanHostLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	// Plan 276 (F06/F07): a light block pass every minute (DB reads only; a
	// changed block state pushes the hosts, a 备线 out of step is retried) —
	// renewals, orders and jobs that do not kick still show within a minute.
	light := time.NewTicker(lkBlockEvery)
	defer light.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-light.C:
			h.leiKwanReconcileBlocks()
			continue
		case <-timer.C:
			timer.Reset(lkLoopEvery)
		case <-h.lkKick:
			time.Sleep(2 * time.Second) // let a burst of edits settle
		}
		h.leiKwanReconcileBlocks()
		// Plan 272: graces older than 24 h end here (a sub pull ends them sooner).
		if n, err := h.repo.ExpireLeiKwanGrantGrace(time.Now().Add(-lkEntryGrace).UnixMilli()); err == nil && n > 0 {
			log.Printf("利群：%d 个用户的旧主入口宽限已满 24 小时，旧入口停用", n)
		}
		for _, id := range h.leiKwanEntryNodeIDs() {
			if h.wsServer != nil && h.wsServer.NodeOnline(id) {
				if err := h.leiKwanHostPush(id); err != nil {
					log.Printf("node %d: 利群主机同步失败: %v", id, err)
				}
			}
		}
	}
}

func (h *Handler) leiKwanHostNodeOnline(nodeID int64) {
	if !lkContains(h.leiKwanEntryNodeIDs(), nodeID) {
		return
	}
	go func() {
		time.Sleep(3 * time.Second)
		if err := h.leiKwanHostPush(nodeID); err != nil {
			log.Printf("node %d: 利群主机同步失败: %v", nodeID, err)
		}
	}()
}

// ---- admin API ----

func parseLKReport(raw string) *lkHostReport {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var r lkHostReport
	if json.Unmarshal([]byte(raw), &r) != nil || r.V != 1 {
		return nil
	}
	return &r
}

// leiKwanHostManageView: the 接管 part of one host in host/list.
func (h *Handler) leiKwanHostManageView(nodeID int64, g *lkGen) map[string]interface{} {
	host, _ := h.repo.GetLeiKwanHost(nodeID)
	v := map[string]interface{}{"manage": false, "traffic": false, "learned": false, "reported": false}
	if host == nil {
		return v
	}
	v["manage"], v["traffic"], v["learned"] = host.Manage == 1, host.Traffic == 1, host.Profile != ""
	v["rev"], v["force"], v["learnedAt"], v["reportedAt"], v["syncedAt"], v["syncError"] = host.Rev, host.Force, host.LearnedAt, host.ReportedAt, host.SyncedAt, host.SyncError
	rep := parseLKReport(host.Report)
	if rep == nil {
		return v
	}
	v["reported"] = true
	v["state"] = rep.State
	v["agent"] = rep.Manage
	if rep.Traffic != nil {
		v["trafficState"] = map[string]interface{}{"counting": rep.Traffic.Epoch != "" && host.Traffic == 1 && !rep.Traffic.Pending, "pending": rep.Traffic.Pending, "blocked": rep.Traffic.Blocked, "error": rep.Traffic.Error}
	}
	prof := parseLKProfile(host.Profile)
	attention := []string{}
	if prof != nil && g != nil {
		diffs := lkDiffFiles(rep.Files, g.desiredFor(nodeID, prof, rep))
		v["diff"] = diffs
		changed := 0
		for _, d := range diffs {
			if !d.Same {
				changed++
			}
		}
		v["diffCount"] = changed
		v["overrides"] = prof.Rules
		v["profile"] = prof
		if host.Manage == 1 && changed > 0 && len(rep.Manage.Drift) == 0 && len(rep.Manage.Pending) == 0 && len(rep.Manage.Errors) == 0 {
			v["applying"] = true
		}
	}
	for _, f := range rep.Manage.Drift {
		attention = append(attention, lkFileNames[f]+"被手改（漂移）")
	}
	for f, e := range rep.Manage.Errors {
		attention = append(attention, lkFileNames[f]+"写入失败："+e)
	}
	if host.SyncError != "" && time.Now().UnixMilli()-host.SyncedAt < 15*60*1000 {
		attention = append(attention, "同步失败："+host.SyncError)
	}
	if ls := rep.Files.Logswitch; ls != nil && ls.Running != nil {
		attention = append(attention, "日志快切 unit 改了还没重启")
	}
	sort.Strings(attention)
	v["attention"] = attention
	if len(rep.Manage.Pending) > 0 {
		names := []string{}
		for _, f := range rep.Manage.Pending {
			names = append(names, lkFileNames[f])
		}
		v["pending"] = names
	}
	return v
}

func (h *Handler) lkHostReq(r *http.Request) (int64, map[string]interface{}, error) {
	var req map[string]interface{}
	if err := leiKwanDecode(r, &req); err != nil {
		return 0, nil, err
	}
	id, _ := req["nodeId"].(float64)
	nodeID := int64(id)
	if nodeID <= 0 || !lkContains(h.leiKwanEntryNodeIDs(), nodeID) {
		return 0, nil, errors.New("不是利群入口节点")
	}
	return nodeID, req, nil
}

func (h *Handler) lkHostReply(w http.ResponseWriter, nodeID int64, extra map[string]interface{}) {
	g, _ := h.newLKGen()
	out := h.leiKwanHostManageView(nodeID, g)
	for k, v := range extra {
		out[k] = v
	}
	response.WriteJSON(w, response.OK(out))
}

// host/learn: adopt the host's current values into the panel (host profile +
// per-host 首包合并). Refused when the result would not equal the host.
func (h *Handler) leikwanHostLearn(w http.ResponseWriter, r *http.Request) {
	nodeID, _, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	diffs, err := h.leiKwanHostLearnNode(nodeID)
	if err != nil {
		response.WriteJSON(w, response.Err(-1, err.Error()))
		return
	}
	h.leiKwanHostKick()
	h.lkHostReply(w, nodeID, map[string]interface{}{"learnedDiff": diffs})
}

func (h *Handler) leiKwanHostLearnNode(nodeID int64) ([]lkFileDiff, error) {
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	mu := lkHostLock(nodeID)
	mu.Lock()
	defer mu.Unlock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err != nil {
		return nil, errors.New("读取主机失败")
	}
	rep := parseLKReport(host.Report)
	if rep == nil {
		return nil, errors.New("这台主机还没有 agent 上报（需要新版 agent）")
	}
	if time.Now().UnixMilli()-host.ReportedAt > 15*60*1000 {
		return nil, errors.New("主机上报已超过 15 分钟，先等 agent 重新上报")
	}
	g, err := h.newLKGen()
	if err != nil {
		return nil, errors.New("读取利群数据失败")
	}
	prof, coalesce, diffs, err := g.learn(nodeID, rep)
	if err != nil {
		return diffs, err
	}
	if prof.Files[lkFileCoalesce] && g.setting != nil {
		gl := parseLeiKwanGlobal(g.setting.Global)
		if gl.CoalesceByNode == nil {
			gl.CoalesceByNode = map[string]string{}
		}
		key := strconv.FormatInt(nodeID, 10)
		if gl.CoalesceByNode[key] != coalesce {
			gl.CoalesceByNode[key] = coalesce
			raw, _ := json.Marshal(gl)
			g.setting.Global = string(raw)
			g.setting.UpdatedTime = time.Now().UnixMilli()
			if err := h.repo.SaveLeiKwanSetting(g.setting); err != nil {
				return nil, errors.New("保存首包合并失败")
			}
		}
	}
	raw, _ := json.Marshal(prof)
	host.Profile, host.LearnedAt = string(raw), time.Now().UnixMilli()
	if err := h.repo.SaveLeiKwanHostConfig(host); err != nil {
		return nil, errors.New("保存主机配置失败")
	}
	return diffs, nil
}

// host/manage {nodeId, manage}: 接管 on bumps the revision, so whatever the
// admin saw in the diff is written; off = the agent only reads.
func (h *Handler) leikwanHostManage(w http.ResponseWriter, r *http.Request) {
	nodeID, req, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	on, _ := req["manage"].(bool)
	mu := lkHostLock(nodeID)
	mu.Lock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err == nil && on && host.Profile == "" {
		err = errors.New("先「采用主机现值」，看过差异再接管")
	}
	if err != nil {
		mu.Unlock()
		leiKwanError(w, err)
		return
	}
	if on && host.Manage != 1 {
		host.Rev++
	}
	host.Manage = 0
	if on {
		host.Manage = 1
	}
	err = h.repo.SaveLeiKwanHostConfig(host)
	mu.Unlock()
	if err != nil {
		leiKwanError(w, errors.New("保存失败"))
		return
	}
	pushErr := h.leiKwanHostPush(nodeID)
	extra := map[string]interface{}{}
	if pushErr != nil {
		extra["warning"] = "已保存，但这次没同步到主机：" + pushErr.Error()
	}
	h.lkHostReply(w, nodeID, extra)
}

// host/force: 「按面板覆盖」 — the agent rewrites drifted files once.
func (h *Handler) leikwanHostForce(w http.ResponseWriter, r *http.Request) {
	nodeID, _, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	mu := lkHostLock(nodeID)
	mu.Lock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err == nil && host.Manage != 1 {
		err = errors.New("没有接管的主机不会被覆盖")
	}
	if err == nil {
		host.Force++
		err = h.repo.SaveLeiKwanHostConfig(host)
	}
	mu.Unlock()
	if err != nil {
		leiKwanError(w, err)
		return
	}
	extra := map[string]interface{}{}
	if err := h.leiKwanHostPush(nodeID); err != nil {
		extra["warning"] = "已记下，但这次没同步到主机：" + err.Error()
	}
	h.lkHostReply(w, nodeID, extra)
}

// host/traffic {nodeId, traffic}: 利群流量统计 on this host (the agent creates
// its counter table only inside the 02:00–06:00 window).
func (h *Handler) leikwanHostTraffic(w http.ResponseWriter, r *http.Request) {
	nodeID, req, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	on, _ := req["traffic"].(bool)
	mu := lkHostLock(nodeID)
	mu.Lock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err == nil {
		host.Traffic = 0
		if on {
			host.Traffic = 1
		}
		err = h.repo.SaveLeiKwanHostConfig(host)
	}
	mu.Unlock()
	if err != nil {
		leiKwanError(w, err)
		return
	}
	extra := map[string]interface{}{}
	if err := h.leiKwanHostPush(nodeID); err != nil {
		extra["warning"] = "已保存，但这次没同步到主机：" + err.Error()
	}
	h.lkHostReply(w, nodeID, extra)
}

// host/sync: push now (refresh the report).
func (h *Handler) leikwanHostSync(w http.ResponseWriter, r *http.Request) {
	nodeID, _, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	extra := map[string]interface{}{}
	if err := h.leiKwanHostPush(nodeID); err != nil {
		extra["warning"] = "同步失败：" + err.Error()
	}
	h.lkHostReply(w, nodeID, extra)
}

// host/unlearn: forget the learned profile (only with 接管 off).
func (h *Handler) leikwanHostUnlearn(w http.ResponseWriter, r *http.Request) {
	nodeID, _, err := h.lkHostReq(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	mu := lkHostLock(nodeID)
	mu.Lock()
	host, err := h.repo.EnsureLeiKwanHost(nodeID)
	if err == nil && host.Manage == 1 {
		err = errors.New("先关掉接管")
	}
	if err == nil {
		host.Profile, host.LearnedAt = "", 0
		err = h.repo.SaveLeiKwanHostConfig(host)
	}
	mu.Unlock()
	if err != nil {
		leiKwanError(w, err)
		return
	}
	h.lkHostReply(w, nodeID, nil)
}

// ---- 换落地 (plan 269 item 4) ----

func (h *Handler) leiKwanHostPushAllAsync() {
	for _, id := range h.leiKwanEntryNodeIDs() {
		if h.wsServer != nil && h.wsServer.NodeOnline(id) {
			go func(id int64) {
				if err := h.leiKwanHostPush(id); err != nil {
					log.Printf("node %d: 利群主机同步失败: %v", id, err)
				}
			}(id)
		}
	}
}

// leiKwanFilterKeyGuard refuses a 换落地 to a panel SS2022 node while an entry
// host that filters this rule (enforce) is not under 接管: its key file would
// keep the old key and the filter would close every new client as junk.
func (h *Handler) leiKwanFilterKeyGuard(x *model.LeiKwanRule, landingID int64) error {
	if landingID <= 0 || landingID == x.LandingNodeID {
		return nil
	}
	n, _, err := h.repo.LeiKwanLandingTarget(landingID)
	if err != nil || n == nil || n.Protocol != "ss2022" {
		return nil
	}
	fid := strconv.FormatInt(x.ForwardID, 10)
	names := h.leiKwanNodeNames(h.leiKwanEntryNodeIDs())
	blocking := []string{}
	for _, id := range h.leiKwanEntryNodeIDs() {
		host, _ := h.repo.GetLeiKwanHost(id)
		if host != nil && host.Manage == 1 {
			continue
		}
		var rep *lkHostReport
		if host != nil {
			rep = parseLKReport(host.Report)
		}
		if rep == nil {
			states, _ := h.repo.ListLeiKwanHostStates()
			for _, s := range states {
				if s.NodeID == id {
					if snap := parseLeiKwanHostSnapshot(s.Data); snap != nil && snap.SSFilter[fid] == "enforce" {
						blocking = append(blocking, names[id])
					}
				}
			}
			continue
		}
		if f := rep.Files.SSFilter; f != nil && f.Tokens[fid] == "enforce" {
			blocking = append(blocking, names[id])
		}
	}
	if len(blocking) > 0 {
		sort.Strings(blocking)
		return fmt.Errorf("入口机 %s 还没接管：首包过滤会挡掉新落地的密钥。先接管这些入口机再换落地", strings.Join(blocking, "、"))
	}
	return nil
}

// leiKwanRetargetForward points an existing FLVX forward (a linked 备线) at
// a new target; its entry (tunnel + port) stays.
func (h *Handler) leiKwanRetargetForward(forwardID int64, target string) error {
	views, err := h.repo.LeiKwanBackupForwards([]int64{forwardID})
	if err != nil {
		return err
	}
	b, ok := views[forwardID]
	if !ok {
		return errors.New("备线规则不存在")
	}
	admin := h.repo.FirstAdminUserID()
	code, msg, _ := h.leikwanAdminCall(h.forwardUpdate, admin, map[string]interface{}{
		"id": b.ID, "name": b.Name, "tunnelId": b.TunnelID, "remoteAddr": target, "inPort": b.EntryPort, "strategy": "fifo",
	})
	if code != 0 {
		return errors.New(msg)
	}
	return nil
}

// rule/prepare-landing {id, landingNodeId}: 「待切换」 — remember the panel
// landing node the rule should move to; nothing changes until 换落地.
func (h *Handler) leikwanRulePrepareLanding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID            int64 `json:"id"`
		LandingNodeID int64 `json:"landingNodeId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	x, err := h.repo.GetLeiKwanRule(req.ID)
	if err != nil || x == nil {
		leiKwanError(w, errors.New("利群规则不存在"))
		return
	}
	if req.LandingNodeID > 0 {
		n, _, err := h.repo.LeiKwanLandingTarget(req.LandingNodeID)
		if err != nil || n == nil {
			leiKwanError(w, errors.New("落地节点不存在"))
			return
		}
		if n.UserID != x.UserID {
			leiKwanError(w, errors.New("落地节点不属于规则的用户"))
			return
		}
		if n.ID == x.LandingNodeID {
			leiKwanError(w, errors.New("规则已经在这个落地上"))
			return
		}
	}
	if err := h.repo.SetLeiKwanRulePending(x.ID, req.LandingNodeID); err != nil {
		leiKwanError(w, errors.New("保存失败"))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

// leiKwanFreezeHostValues: a rule is unregistered but its LeiKwan forward
// stays — copy what each learned host runs for it into the host's unmanaged
// values, so the hosts keep their behaviour. An automatic 备线 is deleted with
// the rule, so its logswitch line / backup pool / return check are not kept.
func (h *Handler) leiKwanFreezeHostValues(x model.LeiKwanRule) {
	g, err := h.newLKGen()
	if err != nil {
		return
	}
	hosts, err := h.repo.ListLeiKwanHosts()
	if err != nil {
		return
	}
	fid := strconv.FormatInt(x.ForwardID, 10)
	for i := range hosts {
		host := hosts[i]
		p := parseLKProfile(host.Profile)
		if p == nil {
			continue
		}
		e := g.effective(x, p.Rules[fid], host.NodeID)
		keepLine := e.Logswitch && x.BackupMode != "auto"
		if p.Unmanaged.Warm == nil {
			p.Unmanaged.Warm = map[string]string{}
		}
		if p.Unmanaged.SSFilter == nil {
			p.Unmanaged.SSFilter = map[string]string{}
		}
		if p.Unmanaged.Triggers == nil {
			p.Unmanaged.Triggers = map[string]string{}
		}
		if p.Files[lkFileWarm] && e.WarmMain > 0 {
			p.Unmanaged.Warm[fid] = strconv.Itoa(e.WarmMain)
			if keepLine && e.WarmBackup > 0 {
				p.Unmanaged.Warm["backup:"+fid] = strconv.Itoa(e.WarmBackup)
			}
		}
		if p.Files[lkFileSSFilter] && e.SSFilter != "" && e.SSFilter != "off" {
			p.Unmanaged.SSFilter[fid] = e.SSFilter
		}
		if p.Files[lkFileLogswitch] && keepLine && x.ListenPort > 0 {
			p.Unmanaged.Lines = append(p.Unmanaged.Lines, fmt.Sprintf("%d:%d>%s", x.ForwardID, x.ListenPort, e.Backup))
			if p.Files[lkFileTriggers] {
				globalOn := p.TrigGlobal["return_check"] == "on"
				if e.ReturnCheck && !globalOn {
					p.Unmanaged.Triggers[fid+":return_check"] = "on"
				} else if !e.ReturnCheck && globalOn {
					p.Unmanaged.Triggers[fid+":return_check"] = "off"
				}
			}
		}
		delete(p.Rules, fid)
		raw, _ := json.Marshal(p)
		mu := lkHostLock(host.NodeID)
		mu.Lock()
		if cur, err := h.repo.GetLeiKwanHost(host.NodeID); err == nil && cur != nil && cur.Profile == host.Profile {
			cur.Profile = string(raw)
			_ = h.repo.SaveLeiKwanHostConfig(cur)
		}
		mu.Unlock()
	}
}
