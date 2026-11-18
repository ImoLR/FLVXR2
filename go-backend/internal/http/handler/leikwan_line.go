package handler

// Plan 266 利群专线入面板 (step 1): 利群专线 (one LeiKwan route = one tunnel of
// the type 「利群专线」, all in the group 「利群汇聚」), grants with the user's
// 主入口 + 备线 switch, rules = LeiKwan forwards, automatic 备线 forwards,
// 「导入为规则」 and read-only entry-host status. Nothing here deploys to a node
// or touches a host: LeiKwan forwards go through the bounded client, 备线 are
// ordinary FLVX rules created through the normal rule path.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// One lock for every 利群 rule / grant write: quota checks (8 slots for the
// whole account) and backup bookkeeping stay consistent. Writes are rare.
var leiKwanRuleMu sync.Mutex

// Route id → default line name (the routes of service 39).
var leiKwanRouteNames = map[int64]string{21: "利群·广港", 24: "利群·沪日", 29: "利群·广台", 23: "利群·福台", 25: "利群·京德"}

func (h *Handler) registerLeiKwanLines(mux *http.ServeMux) {
	admin := map[string]http.HandlerFunc{
		"line/list": h.leikwanLineList, "line/save": h.leikwanLineSave, "line/delete": h.leikwanLineDelete,
		"setting/save": h.leikwanSettingSave, "grant/list": h.leikwanGrantList, "grant/save": h.leikwanGrantSave,
		"grant/delete": h.leikwanGrantDelete, "import/preview": h.leikwanImportPreview, "import/apply": h.leikwanImportApply,
		"host/list": h.leikwanHostList, "host/import": h.leikwanHostImport, "rule/backup-retry": h.leikwanRuleBackupRetry,
		"line/backup-rebuild": h.leikwanLineBackupRebuild,
		// Plan 269 利群主机接管 / 流量 / 99U 落地准备.
		"host/learn": h.leikwanHostLearn, "host/unlearn": h.leikwanHostUnlearn, "host/manage": h.leikwanHostManage,
		"host/force": h.leikwanHostForce, "host/traffic": h.leikwanHostTraffic, "host/sync": h.leikwanHostSync,
		"rule/prepare-landing": h.leikwanRulePrepareLanding,
		// Plan 272 「所有利群入口都可用」.
		"rule/all-entries": h.leikwanRuleAllEntries,
		// Plan 273 套餐流量 + 利群在线设备 (last snapshots, no vendor call).
		"usage": h.leikwanUsage,
	}
	for path, fn := range admin {
		path, fn := path, fn
		mux.HandleFunc("/api/v1/leikwan/"+path, func(w http.ResponseWriter, r *http.Request) {
			if _, role, err := userRoleFromRequest(r); err == nil && role != 0 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
			}
			if !h.ensureAdminAccess(w, r) {
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			fn(w, r)
			if lkMutatingPath(path) {
				h.leiKwanHostKick() // plan 269: hosts follow the panel data
			}
		})
	}
	// Rules: users (own rules, their grant) and admins.
	for path, fn := range map[string]http.HandlerFunc{
		"list": h.leikwanRuleList, "options": h.leikwanRuleOptions, "create": h.leikwanRuleCreate,
		"update": h.leikwanRuleUpdate, "delete": h.leikwanRuleDelete, "link": h.leikwanRuleLink,
		// Plan 270: the cheap 「利群汇聚」 nav flag (one indexed read).
		"access": h.leikwanRuleAccess,
		// Plan 272: the automatic 利群汇聚 subscription and 诊断.
		"sub": h.leikwanRuleSub, "diagnose": h.leikwanRuleDiagnose, "diagnose/stream": h.leikwanRuleDiagnoseStream,
	} {
		path, fn := path, fn
		mux.HandleFunc("/api/v1/leikwan-rule/"+path, func(w http.ResponseWriter, r *http.Request) {
			if _, _, err := userRoleFromRequest(r); err != nil {
				response.WriteJSON(w, response.Err(401, "未登录或token已过期"))
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			fn(w, r)
			if lkMutatingPath(path) {
				h.leiKwanHostKick()
			}
		})
	}
}

// lkMutatingPath: 利群 API calls that may change what the entry hosts get.
func lkMutatingPath(path string) bool {
	if strings.HasPrefix(path, "host/") {
		return false // host/* push themselves
	}
	for _, s := range []string{"save", "delete", "create", "update", "apply", "rebuild", "retry", "prepare"} {
		if strings.Contains(path, s) {
			return true
		}
	}
	return false
}

// ---- 利群加速 defaults / parsing ----

func defaultLeiKwanAccel() model.LeiKwanAccel {
	return model.LeiKwanAccel{Warm: model.LeiKwanWarm{Main: 4, Backup: 4, Standby: 2, PaceMs: 100, Flush: true}, SSFilter: "enforce", Logswitch: true, ReturnCheck: true}
}

func defaultLeiKwanGlobal() model.LeiKwanGlobalAccel {
	return model.LeiKwanGlobalAccel{HoldS: 30, LeaseSettleS: 30, Grace: "switch", ReturnCheckS: 2.5, IOError: "record", Coalesce: "on budget_ms=2 min=59", CoalesceByNode: map[string]string{}}
}

func parseLeiKwanAccel(raw string) model.LeiKwanAccel {
	a := defaultLeiKwanAccel()
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return a
}

func parseLeiKwanRuleAccel(raw string) model.LeiKwanRuleAccel {
	var a model.LeiKwanRuleAccel
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &a)
	}
	return a
}

func parseLeiKwanGlobal(raw string) model.LeiKwanGlobalAccel {
	g := defaultLeiKwanGlobal()
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &g)
	}
	if g.CoalesceByNode == nil {
		g.CoalesceByNode = map[string]string{}
	}
	return g
}

var leiKwanCoalescePattern = regexp.MustCompile(`^(off|on budget_ms=[0-9]{1,2} min=[0-9]{1,3})$`)

func validLeiKwanAccel(a model.LeiKwanAccel) error {
	w := a.Warm
	if w.Main < 0 || w.Main > 16 || w.Backup < 0 || w.Backup > 16 || w.Standby < 0 || w.Standby > 16 || w.PaceMs < 0 || w.PaceMs > 1000 || len(w.Burst) > 40 {
		return errors.New("预热池数值无效（每条线路 0–16 条，间隔 0–1000 ms）")
	}
	if a.SSFilter != "enforce" && a.SSFilter != "off" {
		return errors.New("首包过滤只能是 enforce 或 off")
	}
	return nil
}

// leiKwanTriggers is the global 日志快切 trigger set, read-only in step 1;
// each trigger says where its signal comes from (日志 / 前端信号 / 状态文件).
var leiKwanTriggers = []map[string]string{
	{"key": "lapsed", "name": "授权中断（policy lapsed）", "source": "日志", "recover": "状态文件", "note": "恢复：/run/ultra/forward-N.status.json 回到 ready + active"},
	{"key": "no_candidate", "name": "没有可用候选（no healthy authorized candidate）", "source": "日志"},
	{"key": "reset2s", "name": "2 秒 0 字节重置", "source": "日志"},
	{"key": "reset1s", "name": "1 秒 0 字节重置", "source": "日志"},
	{"key": "zero_close", "name": "0 ms 0 字节关闭", "source": "日志"},
	{"key": "eof1s", "name": "1 秒 0 字节 EOF", "source": "日志", "note": "只在首包过滤 enforce 的线路生效（读前端 ssfilter）"},
	{"key": "grace", "name": "厂商网络宽限（grace / 续不上）", "source": "日志", "note": "新版厂商 agent 才有"},
	{"key": "timeout", "name": "候选连接超时（第一次就切）", "source": "日志"},
	{"key": "warm_death", "name": "预热池连接被断（第一次就切）", "source": "前端信号"},
	{"key": "io_error", "name": "候选 io_error", "source": "日志", "note": "只记录，不切"},
	{"key": "return_check", "name": "切回前复查（最后 2.5 s 一条新主线连接要活着）", "source": "前端信号", "note": "每条线路可开关"},
}

// ---- host snapshot (step 1: imported, secret-free) ----

type leiKwanHostLine struct {
	ForwardID  int64  `json:"forwardId"`
	ListenPort int    `json:"listenPort"`
	Backup     string `json:"backup"`
}
type leiKwanHostForward struct {
	ForwardID  int64  `json:"forwardId"`
	ListenPort int    `json:"listenPort"`
	Target     string `json:"target"`
	Transport  string `json:"transport"`
	State      string `json:"state"`
}
type leiKwanHostPhone struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
	Note   string `json:"note"`
}
type leiKwanHostSnapshot struct {
	Host                string               `json:"host"`
	CapturedAt          string               `json:"capturedAt"`
	AgentVersion        string               `json:"agentVersion"`
	CoreVersion         string               `json:"coreVersion"`
	UpdateLock          bool                 `json:"updateLock"`
	NativeUDP           bool                 `json:"nativeUdp"`
	SelectionMode       string               `json:"selectionMode"`
	ForwardsReady       string               `json:"forwardsReady"`
	FrontChannel        string               `json:"frontChannel"`
	FrontBuild          string               `json:"frontBuild"`
	FastFailover        string               `json:"fastFailover"`
	Logswitch           string               `json:"logswitch"`
	LogswitchRecordOnly []string             `json:"logswitchRecordOnly"`
	HoldS               int                  `json:"holdS"`
	LeaseSettleS        int                  `json:"leaseSettleS"`
	Grace               string               `json:"grace"`
	ReturnCheckS        float64              `json:"returnCheckS"`
	Coalesce            string               `json:"coalesce"`
	Warm                map[string]int       `json:"warm"`
	SSFilter            map[string]string    `json:"ssFilter"`
	Triggers            map[string]string    `json:"triggers"`
	Lines               []leiKwanHostLine    `json:"lines"`
	Forwards            []leiKwanHostForward `json:"forwards"`
	PhoneBackup         []leiKwanHostPhone   `json:"phoneBackup"`
	DeviceQuota         string               `json:"deviceQuota"`
	Extra               []string             `json:"extra"`
}

var leiKwanSecretRun = regexp.MustCompile(`[A-Za-z0-9+/=_]{24,}`)

// leiKwanSecretLike: a long base64/hex-like run with letters and digits
// (SS2022 keys, tokens, full hashes). Hyphenated build names don't match.
func leiKwanSecretLike(raw []byte) bool {
	for _, m := range leiKwanSecretRun.FindAll(raw, -1) {
		if bytes.ContainsAny(m, "0123456789") && bytes.IndexFunc(m, func(r rune) bool { return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }) >= 0 {
			return true
		}
	}
	return false
}

func parseLeiKwanHostSnapshot(raw string) *leiKwanHostSnapshot {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var s leiKwanHostSnapshot
	if json.Unmarshal([]byte(raw), &s) != nil {
		return nil
	}
	return &s
}

// ---- small helpers ----

func leiKwanDecodeLarge(r *http.Request, v interface{}, limit int64) error {
	d := json.NewDecoder(io.LimitReader(r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("请求参数错误")
	}
	return nil
}

func leiKwanBadHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\@?#[]") {
		return true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback()
	}
	return !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`).MatchString(host) || !strings.Contains(host, ".")
}

// leikwanAdminCall replays a normal rule handler in-process as an admin (the
// owner of automatic 备线 forwards), so 备线 get every check and side effect
// of an ordinary rule (port pick incl. LeiKwan ports skipped, runtime push).
func (h *Handler) leikwanAdminCall(fn http.HandlerFunc, adminID int64, body map[string]interface{}) (int, string, json.RawMessage) {
	raw, _ := json.Marshal(body)
	claims := auth.Claims{Sub: strconv.FormatInt(adminID, 10), RoleID: 0, User: "leikwan-backup", Name: "leikwan-backup"}
	ctx := context.WithValue(context.Background(), middleware.ClaimsContextKey, claims)
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/forward/internal", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	cw := &captureWriter{}
	fn(cw, r)
	return cw.envelope()
}

func (h *Handler) leiKwanSettingAccount() (*model.LeiKwanSetting, *model.LeiKwanAccount, error) {
	s, err := h.repo.GetLeiKwanSetting()
	if err != nil {
		return nil, nil, errors.New("读取利群设置失败")
	}
	if s.AccountID == 0 {
		as, err := h.repo.ListLeiKwanAccounts()
		if err != nil || len(as) == 0 {
			return s, nil, errors.New("请先在利群汇聚页保存利群账号")
		}
		s.AccountID = as[0].ID
	}
	a, err := h.leiKwanAccount(s.AccountID)
	return s, a, err
}

type leiKwanEntryNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Online   bool   `json:"online"`
	LeiKwan  string `json:"leikwanStatus"`
	Forwards int    `json:"leikwanForwards"`
}

func (h *Handler) leiKwanEntryNodes(groupID int64) []leiKwanEntryNode {
	out := []leiKwanEntryNode{}
	if groupID <= 0 {
		return out
	}
	nodes, err := h.repo.GetNodesByGroupID(groupID)
	if err != nil {
		return out
	}
	for _, n := range nodes {
		var fw []model.LeiKwanDetectedForward
		_ = json.Unmarshal([]byte(n.LeiKwanForwards), &fw)
		out = append(out, leiKwanEntryNode{ID: n.ID, Name: n.Name, Host: n.ServerIP, Online: n.Status == 1, LeiKwan: n.LeiKwanStatus, Forwards: len(fw)})
	}
	return out
}

func leiKwanEntryByID(nodes []leiKwanEntryNode, id int64) *leiKwanEntryNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

type leiKwanQuota struct {
	Limit  int    `json:"limit"`
	Active int    `json:"active"`
	Error  string `json:"error,omitempty"`
}

func (h *Handler) leiKwanQuota(ctx context.Context, a *model.LeiKwanAccount) (leiKwanQuota, *leikwan.Overview) {
	if a == nil {
		return leiKwanQuota{Error: "未配置利群账号"}, nil
	}
	view, err := h.leikwan.Overview(ctx, a.Token, a.ServiceID)
	if err != nil {
		return leiKwanQuota{Error: err.Error()}, nil
	}
	return leiKwanQuota{Limit: view.Options.ForwardLimit, Active: view.Options.ActiveForwardCount}, &view
}

// ---- 利群专线 (tunnels) ----

type leiKwanHostValue struct {
	NodeID      int64  `json:"nodeId"`
	NodeName    string `json:"nodeName"`
	WarmMain    *int   `json:"warmMain"`
	WarmBackup  *int   `json:"warmBackup"`
	SSFilter    string `json:"ssFilter"`
	Logswitch   bool   `json:"logswitch"`
	Backup      string `json:"backup"`
	ReturnCheck string `json:"returnCheck"`
}

type leiKwanHostCtx struct {
	nodeID int64
	name   string
	snap   *leiKwanHostSnapshot
}

func (h *Handler) leiKwanHostContexts() []leiKwanHostCtx {
	states, err := h.repo.ListLeiKwanHostStates()
	if err != nil {
		return nil
	}
	ids := make([]int64, 0, len(states))
	for _, s := range states {
		ids = append(ids, s.NodeID)
	}
	names := h.leiKwanNodeNames(ids)
	out := make([]leiKwanHostCtx, 0, len(states))
	for _, s := range states {
		if snap := parseLeiKwanHostSnapshot(s.Data); snap != nil {
			out = append(out, leiKwanHostCtx{nodeID: s.NodeID, name: names[s.NodeID], snap: snap})
		}
	}
	return out
}

func (h *Handler) leiKwanNodeNames(ids []int64) map[int64]string {
	out := map[int64]string{}
	for _, id := range ids {
		if n, err := h.repo.GetNodeRecord(id); err == nil && n != nil {
			out[id] = n.Name
		}
	}
	return out
}

// hostValuesFor: what each entry host runs today for one LeiKwan forward.
func hostValuesFor(hosts []leiKwanHostCtx, forwardID int64) []leiKwanHostValue {
	key := strconv.FormatInt(forwardID, 10)
	out := make([]leiKwanHostValue, 0, len(hosts))
	for _, hc := range hosts {
		v := leiKwanHostValue{NodeID: hc.nodeID, NodeName: hc.name}
		if n, ok := hc.snap.Warm[key]; ok {
			n := n
			v.WarmMain = &n
		}
		if n, ok := hc.snap.Warm["backup:"+key]; ok {
			n := n
			v.WarmBackup = &n
		}
		v.SSFilter = hc.snap.SSFilter[key]
		for _, l := range hc.snap.Lines {
			if l.ForwardID == forwardID {
				v.Logswitch, v.Backup = true, l.Backup
			}
		}
		if v.Logswitch {
			v.ReturnCheck = hc.snap.Triggers[key+":return_check"]
			if v.ReturnCheck == "" {
				v.ReturnCheck = hc.snap.Triggers["return_check"]
			}
		}
		out = append(out, v)
	}
	return out
}

func (h *Handler) leikwanLineList(w http.ResponseWriter, r *http.Request) {
	setting, account, accErr := h.leiKwanSettingAccount()
	if setting == nil {
		leiKwanError(w, accErr)
		return
	}
	lines, err := h.repo.ListLeiKwanLines()
	if err != nil {
		leiKwanError(w, errors.New("读取利群专线失败"))
		return
	}
	rules, err := h.repo.ListLeiKwanRules(0)
	if err != nil {
		leiKwanError(w, errors.New("读取利群转发失败"))
		return
	}
	tunnels, _ := h.repo.ListLeiKwanBackupTunnels()
	tunnelName := map[int64]string{}
	for _, t := range tunnels {
		tunnelName[t.ID] = t.Name
	}
	hosts := h.leiKwanHostContexts()
	backupStats := h.leiKwanLineBackupStats(lines, rules)
	byLine := map[int64][]model.LeiKwanRule{}
	for _, x := range rules {
		byLine[x.LineID] = append(byLine[x.LineID], x)
	}
	out := make([]map[string]interface{}, 0, len(lines))
	for _, l := range lines {
		forwardIDs := []int64{}
		hostRows := []map[string]interface{}{}
		for _, x := range byLine[l.ID] {
			forwardIDs = append(forwardIDs, x.ForwardID)
			hostRows = append(hostRows, map[string]interface{}{"forwardId": x.ForwardID, "ruleName": x.Name, "values": hostValuesFor(hosts, x.ForwardID)})
		}
		out = append(out, map[string]interface{}{
			"id": l.ID, "name": l.Name, "routeId": l.RouteID, "routeLabel": l.RouteLabel, "transport": l.Transport,
			"backupTunnelId": l.BackupTunnelID, "backupTunnelName": tunnelName[l.BackupTunnelID], "accel": parseLeiKwanAccel(l.Accel),
			"flowMode": l.FlowMode, "trafficRatio": l.TrafficRatio, // plan 269
			"inx": l.Inx, "status": l.Status, "ruleCount": len(byLine[l.ID]), "forwardIds": forwardIDs, "hostValues": hostRows,
			"backupStats": backupStats[l.ID],
		})
	}
	// The pipe-mode tunnels (plan 094) are shown beside the group, not in it.
	pipes := []map[string]interface{}{}
	if account != nil {
		ps, _ := h.repo.ListLeiKwanPipes(account.ID)
		for _, p := range ps {
			// Plan 270: id + etag (delete an unbound pipe) and the tunnel's rule count.
			item := map[string]interface{}{"id": p.ID, "etag": p.ETag, "forwardId": p.ForwardID, "listenPort": p.ListenPort, "routeLabel": p.RouteLabel, "tunnelId": p.TunnelID}
			if p.TunnelID != nil {
				item["tunnelName"] = h.repo.LeiKwanTunnelName(*p.TunnelID)
				n, _ := h.repo.CountTunnelForwards(*p.TunnelID)
				item["tunnelRules"] = n
			}
			pipes = append(pipes, item)
		}
	}
	quota, view := h.leiKwanQuota(r.Context(), account)
	routes := []map[string]interface{}{}
	if view != nil {
		for _, rt := range view.Options.Routes {
			avail := false
			for _, t := range rt.Transports {
				avail = avail || t.Available
			}
			routes = append(routes, map[string]interface{}{"id": rt.ID, "label": rt.Label, "available": avail})
		}
	}
	group, _ := h.repo.GetNodeGroupByID(setting.EntryGroupID)
	groupName := ""
	if group != nil {
		groupName = group.Name
	}
	global := parseLeiKwanGlobal(setting.Global)
	entryNodes := h.leiKwanEntryNodes(setting.EntryGroupID)
	fronts := h.leiKwanFronts(entryNodes)
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"setting":       map[string]interface{}{"accountId": setting.AccountID, "entryGroupId": setting.EntryGroupID, "entryGroupName": groupName, "groupName": setting.GroupName, "global": global},
		"entryNodes":    entryNodes,
		"fronts":        fronts,
		"frontFarMs":    leiKwanFrontFarMs,
		"lines":         out,
		"pipes":         pipes,
		"quota":         quota,
		"routes":        routes,
		"backupTunnels": h.leiKwanBackupTunnelViews(tunnels, fronts),
		"triggers":      leiKwanTriggers,
		"accountReady":  account != nil,
	}))
}

func (h *Handler) leikwanLineSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID             int64              `json:"id"`
		Name           string             `json:"name"`
		RouteID        int64              `json:"routeId"`
		RouteLabel     string             `json:"routeLabel"`
		Transport      string             `json:"transport"`
		BackupTunnelID int64              `json:"backupTunnelId"`
		Accel          model.LeiKwanAccel `json:"accel"`
		Inx            int                `json:"inx"`
		Status         *int               `json:"status"`
		// Plan 269: billing of the 利群 bytes (nil = unchanged / default 单向 ×1).
		FlowMode     *int     `json:"flowMode"`
		TrafficRatio *float64 `json:"trafficRatio"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 || req.RouteID <= 0 || (req.Transport != "tcp" && req.Transport != "tcp+udp") {
		leiKwanError(w, errors.New("请填写隧道名称并选择利群路线"))
		return
	}
	if err := validLeiKwanAccel(req.Accel); err != nil {
		leiKwanError(w, err)
		return
	}
	setting, account, err := h.leiKwanSettingAccount()
	if err != nil || account == nil {
		leiKwanError(w, errors.New("请先在利群汇聚页保存利群账号"))
		return
	}
	if req.BackupTunnelID > 0 {
		// Plan 268: any existing tunnel (the admin's own pick, like the rule
		// picker); the backup's exit is always the rule's landing.
		t, err := h.getTunnelRecord(req.BackupTunnelID)
		if err != nil || t == nil {
			leiKwanError(w, errors.New("备线隧道不存在"))
			return
		}
	}
	line := &model.LeiKwanLine{AccountID: account.ID, Status: 1}
	if req.ID > 0 {
		old, err := h.repo.GetLeiKwanLine(req.ID)
		if err != nil || old == nil {
			leiKwanError(w, errors.New("利群专线不存在"))
			return
		}
		if old.RouteID != req.RouteID {
			if rules, _ := h.repo.ListLeiKwanRules(0); leiKwanLineHasRules(rules, old.ID) {
				leiKwanError(w, errors.New("专线上已有转发，不能更换利群路线"))
				return
			}
		}
		line = old
	} else if dup, _ := h.repo.GetLeiKwanLineByRoute(account.ID, req.RouteID); dup != nil {
		leiKwanError(w, fmt.Errorf("这条利群路线已经是专线「%s」", dup.Name))
		return
	}
	label := strings.TrimSpace(req.RouteLabel)
	if label == "" {
		label = line.RouteLabel
	}
	if view, err := h.leikwan.Overview(r.Context(), account.Token, account.ServiceID); err == nil {
		for _, rt := range view.Options.Routes {
			if rt.ID == req.RouteID {
				label = rt.Label
			}
		}
	}
	raw, _ := json.Marshal(req.Accel)
	line.Name, line.RouteID, line.RouteLabel, line.Transport, line.BackupTunnelID, line.Accel, line.Inx = req.Name, req.RouteID, label, req.Transport, req.BackupTunnelID, string(raw), req.Inx
	if req.Status != nil {
		line.Status = map[bool]int{true: 1, false: 0}[*req.Status == 1]
	}
	if line.FlowMode <= 0 {
		line.FlowMode = 1
	}
	if line.TrafficRatio <= 0 {
		line.TrafficRatio = 1
	}
	if req.FlowMode != nil {
		if *req.FlowMode != 1 && *req.FlowMode != 2 {
			leiKwanError(w, errors.New("计费方式只能是单向或双向"))
			return
		}
		line.FlowMode = *req.FlowMode
	}
	if req.TrafficRatio != nil {
		if *req.TrafficRatio <= 0 || *req.TrafficRatio > 100 {
			leiKwanError(w, errors.New("流量倍率要在 0–100 之间"))
			return
		}
		line.TrafficRatio = *req.TrafficRatio
	}
	if err := h.repo.SaveLeiKwanLine(line); err != nil {
		leiKwanError(w, errors.New("保存利群专线失败"))
		return
	}
	if setting.AccountID == 0 {
		setting.AccountID = account.ID
		_ = h.repo.SaveLeiKwanSetting(setting)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": line.ID}))
}

func leiKwanLineHasRules(rules []model.LeiKwanRule, lineID int64) bool {
	for _, x := range rules {
		if x.LineID == lineID {
			return true
		}
	}
	return false
}

func (h *Handler) leikwanLineDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if err := h.repo.DeleteLeiKwanLine(req.ID); err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) leikwanSettingSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EntryGroupID int64                     `json:"entryGroupId"`
		Global       *model.LeiKwanGlobalAccel `json:"global"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	s, _, _ := h.leiKwanSettingAccount()
	if s == nil {
		leiKwanError(w, errors.New("读取利群设置失败"))
		return
	}
	if req.EntryGroupID > 0 {
		if g, err := h.repo.GetNodeGroupByID(req.EntryGroupID); err != nil || g == nil {
			leiKwanError(w, errors.New("节点分组不存在"))
			return
		}
		s.EntryGroupID = req.EntryGroupID
	}
	if req.Global != nil {
		g := *req.Global
		if !leiKwanCoalescePattern.MatchString(g.Coalesce) {
			leiKwanError(w, errors.New("首包合并格式：off 或 on budget_ms=2 min=59"))
			return
		}
		for _, v := range g.CoalesceByNode {
			if v != "" && !leiKwanCoalescePattern.MatchString(v) {
				leiKwanError(w, errors.New("首包合并格式：off 或 on budget_ms=2 min=59"))
				return
			}
		}
		if g.HoldS < 5 || g.HoldS > 600 || g.LeaseSettleS < 0 || g.LeaseSettleS > 600 || g.ReturnCheckS < 0 || g.ReturnCheckS > 30 {
			leiKwanError(w, errors.New("日志快切时间数值无效"))
			return
		}
		raw, _ := json.Marshal(g)
		s.Global = string(raw)
	}
	if err := h.repo.SaveLeiKwanSetting(s); err != nil {
		leiKwanError(w, errors.New("保存利群设置失败"))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

// ---- grants (授权：主入口 + 备线) ----

func (h *Handler) leiKwanGrantView(g model.LeiKwanGrant, entries []leiKwanEntryNode, ruleCount int64, userName string) map[string]interface{} {
	// Plan 268: a grant is only 主入口 + 备线. 规则数 / 到期 come from the user
	// (总规则数, user expiry); the stored num / flow_gb / exp_time are ignored.
	v := map[string]interface{}{"id": g.ID, "userId": g.UserID, "userName": userName, "mainEntryNodeId": g.MainEntryNodeID, "backupLine": g.BackupLine == 1,
		"status": g.Status, "ruleCount": ruleCount, "updatedTime": g.UpdatedTime}
	if e := leiKwanEntryByID(entries, g.MainEntryNodeID); e != nil {
		v["mainEntryName"], v["mainEntryHost"] = e.Name, e.Host
	} else if n, err := h.repo.GetNodeRecord(g.MainEntryNodeID); err == nil && n != nil {
		v["mainEntryName"], v["mainEntryHost"], v["mainEntryOutsideGroup"] = n.Name, n.ServerIP, true
	}
	v["grace"] = nil
	if until := g.EntryChangedAt + lkEntryGrace.Milliseconds(); g.PrevEntryNodeID > 0 && until > time.Now().UnixMilli() {
		name := ""
		if e := leiKwanEntryByID(entries, g.PrevEntryNodeID); e != nil {
			name = e.Name
		} else if n, err := h.repo.GetNodeRecord(g.PrevEntryNodeID); err == nil && n != nil {
			name = n.Name
		}
		v["grace"] = map[string]interface{}{"prevEntryNodeId": g.PrevEntryNodeID, "prevEntryName": name, "changedAt": g.EntryChangedAt, "until": until, "waitingSub": true}
	}
	return v
}

func (h *Handler) leikwanGrantList(w http.ResponseWriter, r *http.Request) {
	setting, _, _ := h.leiKwanSettingAccount()
	if setting == nil {
		leiKwanError(w, errors.New("读取利群设置失败"))
		return
	}
	grants, err := h.repo.ListLeiKwanGrants()
	if err != nil {
		leiKwanError(w, errors.New("读取授权失败"))
		return
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	ids := []int64{}
	for _, g := range grants {
		ids = append(ids, g.UserID)
	}
	names := h.repo.LeiKwanUserNames(ids)
	refs, _ := h.repo.ListLeiKwanBackupRefs()
	out := make([]map[string]interface{}, 0, len(grants))
	for _, g := range grants {
		n, _ := h.repo.CountLeiKwanRules(g.UserID)
		gv := h.leiKwanGrantView(g, entries, n, names[g.UserID])
		gv["backupFlow"] = leiKwanUserBackupFlow(refs, g.UserID) // plan 271 备线流量
		out = append(out, gv)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"grants": out, "entryNodes": entries, "entryGroupId": setting.EntryGroupID,
		"entryLock": parseLeiKwanGlobal(setting.Global).EntryLock}))
}

func (h *Handler) leikwanGrantSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID          int64 `json:"userId"`
		MainEntryNodeID int64 `json:"mainEntryNodeId"`
		BackupLine      bool  `json:"backupLine"`
		// Plan 268: accepted for old (cached) clients and ignored — the
		// grant no longer carries 规则数 / 流量 / 到期.
		Num     json.RawMessage `json:"num"`
		FlowGB  json.RawMessage `json:"flowGb"`
		ExpTime json.RawMessage `json:"expTime"`
		Status  *int            `json:"status"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if u, err := h.repo.GetUserByID(req.UserID); err != nil || u == nil {
		leiKwanError(w, errors.New("用户不存在"))
		return
	}
	setting, _, _ := h.leiKwanSettingAccount()
	if setting == nil || setting.EntryGroupID <= 0 {
		leiKwanError(w, errors.New("请先设置节点分组「利群入口」"))
		return
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	if leiKwanEntryByID(entries, req.MainEntryNodeID) == nil {
		leiKwanError(w, errors.New("主入口必须从节点分组「利群入口」里选"))
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	old, err := h.repo.GetLeiKwanGrant(req.UserID)
	if err != nil {
		leiKwanError(w, errors.New("读取授权失败"))
		return
	}
	g := &model.LeiKwanGrant{UserID: req.UserID, Status: 1}
	if old != nil {
		g = old
	}
	prevEntry, prevBackup := g.MainEntryNodeID, g.BackupLine
	g.MainEntryNodeID, g.BackupLine = req.MainEntryNodeID, map[bool]int{true: 1, false: 0}[req.BackupLine]
	if old != nil && prevEntry != g.MainEntryNodeID {
		// Plan 272: the old 主入口 stays open until the user refreshes a
		// subscription (or 24 h); only the entry right before this change.
		g.PrevEntryNodeID, g.EntryChangedAt = prevEntry, time.Now().UnixMilli()
		if prevEntry == g.MainEntryNodeID || prevEntry <= 0 {
			g.PrevEntryNodeID = 0
		}
	}
	if req.Status != nil {
		g.Status = map[bool]int{true: 1, false: 0}[*req.Status == 1]
	}
	if err := h.repo.SaveLeiKwanGrant(g); err != nil {
		leiKwanError(w, errors.New("保存授权失败"))
		return
	}
	rules, _ := h.repo.ListLeiKwanRules(g.UserID)
	moved := 0
	if old != nil && prevEntry != g.MainEntryNodeID {
		moved = len(rules)
	}
	warnings := []string{}
	if g.BackupLine == 1 && (old == nil || prevBackup != 1) {
		warnings = append(warnings, h.leiKwanBackupsOn(rules)...)
	} else if g.BackupLine == 0 && old != nil && prevBackup == 1 {
		warnings = append(warnings, h.leiKwanBackupsOff(rules)...)
	}
	if moved > 0 || (old != nil && prevEntry != g.MainEntryNodeID) {
		h.leiKwanSubsChanged(g.UserID) // plan 272: subs carry the new 主入口 at once
	}
	n, _ := h.repo.CountLeiKwanRules(g.UserID)
	names := h.repo.LeiKwanUserNames([]int64{g.UserID})
	response.WriteJSON(w, response.OK(map[string]interface{}{"grant": h.leiKwanGrantView(*g, entries, n, names[g.UserID]), "movedRules": moved, "warnings": warnings}))
}

func (h *Handler) leikwanGrantDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"userId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if err := h.repo.DeleteLeiKwanGrant(req.UserID); err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

// ---- 备线 (automatic backup forwards) ----

func leiKwanTargetAddr(x model.LeiKwanRule) string {
	return net.JoinHostPort(strings.Trim(x.TargetHost, "[]"), strconv.Itoa(x.TargetPort))
}

// leiKwanCreateBackup creates the rule's automatic 备线: an ordinary FLVX rule
// on the line's 备线隧道 to the same landing, owned by the admin (like the
// hand-made 備線·NNN rules), so it is no extra user rule and not billed twice.
func (h *Handler) leiKwanCreateBackup(x *model.LeiKwanRule) error {
	line, err := h.repo.GetLeiKwanLine(x.LineID)
	if err != nil || line == nil {
		return errors.New("利群专线不存在")
	}
	if line.BackupTunnelID <= 0 {
		return errors.New("专线没有设置备线隧道")
	}
	admin := h.repo.FirstAdminUserID()
	if admin <= 0 {
		return errors.New("面板没有管理员账号")
	}
	code, msg, data := h.leikwanAdminCall(h.forwardCreate, admin, map[string]interface{}{
		"name": fmt.Sprintf("備線·%d", x.ForwardID), "tunnelId": line.BackupTunnelID, "remoteAddr": leiKwanTargetAddr(*x), "userId": admin, "strategy": "fifo",
	})
	if code != 0 {
		return errors.New(msg)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(data, &created) != nil || created.ID <= 0 {
		return errors.New("备线转发创建结果无法解析")
	}
	x.BackupForwardID, x.BackupMode, x.BackupError = created.ID, "auto", ""
	return nil
}

// leiKwanDropBackup removes an automatic 备线 (linked, pre-existing ones are
// only detached: hosts' 日志快切 and phone backup ports may still use them).
func (h *Handler) leiKwanDropBackup(x *model.LeiKwanRule) error {
	if x.BackupForwardID <= 0 {
		x.BackupMode, x.BackupError = "", ""
		return nil
	}
	if x.BackupMode == "auto" {
		if n, _ := h.repo.CountLeiKwanRulesByBackup(x.BackupForwardID); n <= 1 {
			admin := h.repo.FirstAdminUserID()
			code, msg, _ := h.leikwanAdminCall(h.forwardDelete, admin, map[string]interface{}{"id": x.BackupForwardID})
			if code != 0 && msg != "转发不存在" {
				// An offline entry keeps the normal delete from cleaning up;
				// force delete like the rules page does (the agent's own
				// orphan sweep removes the service when it reconnects).
				code, msg, _ = h.leikwanAdminCall(h.forwardForceDelete, admin, map[string]interface{}{"id": x.BackupForwardID})
				if code != 0 && msg != "转发不存在" {
					return errors.New("删除备线失败：" + msg)
				}
			}
		}
	}
	x.BackupForwardID, x.BackupMode, x.BackupError = 0, "", ""
	return nil
}

func (h *Handler) leiKwanRetargetBackup(x *model.LeiKwanRule) error {
	if x.BackupForwardID <= 0 || x.BackupMode != "auto" {
		return nil
	}
	views, err := h.repo.LeiKwanBackupForwards([]int64{x.BackupForwardID})
	if err != nil {
		return err
	}
	b, ok := views[x.BackupForwardID]
	if !ok {
		return h.leiKwanCreateBackup(x)
	}
	admin := h.repo.FirstAdminUserID()
	code, msg, _ := h.leikwanAdminCall(h.forwardUpdate, admin, map[string]interface{}{
		"id": b.ID, "name": b.Name, "tunnelId": b.TunnelID, "remoteAddr": leiKwanTargetAddr(*x), "inPort": b.EntryPort, "strategy": "fifo",
	})
	if code != 0 {
		return errors.New(msg)
	}
	x.BackupError = "" // plan 276 (F02): the 备线 follows the landing again
	return nil
}

// lkBackupStale starts the 备线 error of a rule whose 备线 could not follow a
// 换落地 (plan 276, F02): it still points at the old landing.
const lkBackupStale = "备线还指向旧落地"

func lkBackupStaleError(err error) string {
	return lkBackupStale + "：" + err.Error()
}

// leiKwanRepointBackup (plan 276, F02 「重试备线」 on an existing 备线): point it
// at the rule's current target. An automatic 备线 that cannot be changed is
// deleted and created again; a linked one is only changed when no other rule
// shares it. Success clears the error.
func (h *Handler) leiKwanRepointBackup(x *model.LeiKwanRule) error {
	if x.BackupMode == "linked" {
		if n, _ := h.repo.CountLeiKwanRulesByBackup(x.BackupForwardID); n > 1 {
			views, err := h.repo.LeiKwanBackupForwards([]int64{x.BackupForwardID})
			if err != nil {
				return err
			}
			if b, ok := views[x.BackupForwardID]; ok && b.RemoteAddr == leiKwanTargetAddr(*x) {
				x.BackupError = ""
				return nil
			}
			return errors.New("这条备线和其他利群转发共用、目标不同，请在授权里关掉再打开备线")
		}
		if err := h.leiKwanRetargetForward(x.BackupForwardID, leiKwanTargetAddr(*x)); err != nil {
			return err
		}
		x.BackupError = ""
		return nil
	}
	err := h.leiKwanRetargetBackup(x)
	if err == nil {
		x.BackupError = ""
		return nil
	}
	// Recreate: the new 备线 first, the old one goes only once it exists (a
	// target that cannot be reached keeps the old 备线 and the error).
	log.Printf("利群规则 %s：备线改目标失败（%v），重建", x.Name, err)
	fresh := *x
	if cerr := h.leiKwanCreateBackup(&fresh); cerr != nil {
		return cerr
	}
	old := *x
	if derr := h.leiKwanDropBackup(&old); derr != nil {
		log.Printf("利群规则 %s：旧备线 %d 没删掉: %v", x.Name, x.BackupForwardID, derr)
	}
	x.BackupForwardID, x.BackupMode, x.BackupError = fresh.BackupForwardID, "auto", ""
	return nil
}

func (h *Handler) leiKwanBackupsOn(rules []model.LeiKwanRule) []string {
	warnings := []string{}
	for i := range rules {
		x := rules[i]
		if x.BackupForwardID > 0 {
			continue
		}
		if err := h.leiKwanCreateBackup(&x); err != nil {
			x.BackupError = err.Error()
			warnings = append(warnings, fmt.Sprintf("%s：备线未建好（%s）", x.Name, err.Error()))
		}
		_ = h.saveLeiKwanRule(&x)
	}
	return warnings
}

func (h *Handler) leiKwanBackupsOff(rules []model.LeiKwanRule) []string {
	warnings := []string{}
	for i := range rules {
		x := rules[i]
		if x.BackupMode != "auto" {
			if x.BackupError != "" {
				x.BackupError = ""
				_ = h.saveLeiKwanRule(&x)
			}
			continue
		}
		if err := h.leiKwanDropBackup(&x); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s：%s", x.Name, err.Error()))
			continue
		}
		_ = h.saveLeiKwanRule(&x)
	}
	return warnings
}

func (h *Handler) leikwanRuleBackupRetry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	x, err := h.repo.GetLeiKwanRule(req.ID)
	if err != nil || x == nil {
		leiKwanError(w, errors.New("利群转发不存在"))
		return
	}
	if x.BackupForwardID > 0 {
		before := x.BackupForwardID
		if err := h.leiKwanRepointBackup(x); err != nil {
			if x.BackupForwardID > 0 {
				x.BackupError = lkBackupStaleError(err)
			} else {
				x.BackupError = err.Error()
			}
			_ = h.saveLeiKwanRule(x)
			leiKwanError(w, errors.New("备线未改好："+err.Error()))
			return
		}
		_ = h.saveLeiKwanRule(x)
		if x.BackupForwardID != before {
			h.leiKwanHostPushAllAsync() // 日志快切 lines name the new 备线 entry
		}
		response.WriteJSON(w, response.OK(map[string]interface{}{"backupForwardId": x.BackupForwardID}))
		return
	}
	if err := h.leiKwanCreateBackup(x); err != nil {
		x.BackupError = err.Error()
		_ = h.saveLeiKwanRule(x)
		leiKwanError(w, errors.New("备线未建好："+err.Error()))
		return
	}
	_ = h.saveLeiKwanRule(x)
	response.WriteJSON(w, response.OK(map[string]interface{}{"backupForwardId": x.BackupForwardID}))
}

// ---- rules ----

type leiKwanRuleCtx struct {
	lines    map[int64]model.LeiKwanLine
	grants   map[int64]model.LeiKwanGrant
	entries  []leiKwanEntryNode
	backups  map[int64]repo.LeiKwanBackupForward
	users    map[int64]string
	landings map[int64]map[string]interface{}
	hosts    []leiKwanHostCtx
	shared   map[int64][]int64
	expired  map[int64]bool
	// Plan 271: every rule with a 备线 (all users), by 备线 forward id.
	backupRefs map[int64][]repo.LeiKwanBackupRef
	allRefs    []repo.LeiKwanBackupRef
	// Plan 272: 外部节点 landings (name + protocol label).
	externals map[int64]map[string]interface{}
	// Plan 273: the vendor's current-cycle report per 利群 account.
	vendor         map[int64]lkVendorReport
	settingAccount int64
}

func (h *Handler) leiKwanRuleContext(rules []model.LeiKwanRule, admin bool) *leiKwanRuleCtx {
	c := &leiKwanRuleCtx{lines: map[int64]model.LeiKwanLine{}, grants: map[int64]model.LeiKwanGrant{}, landings: map[int64]map[string]interface{}{}, shared: map[int64][]int64{},
		externals: map[int64]map[string]interface{}{}}
	lines, _ := h.repo.ListLeiKwanLines()
	for _, l := range lines {
		c.lines[l.ID] = l
	}
	grants, _ := h.repo.ListLeiKwanGrants()
	for _, g := range grants {
		c.grants[g.UserID] = g
	}
	setting, _ := h.repo.GetLeiKwanSetting()
	if setting != nil {
		c.entries = h.leiKwanEntryNodes(setting.EntryGroupID)
		c.settingAccount = setting.AccountID
	}
	c.vendor = h.leiKwanVendorReports(rules, c.settingAccount)
	ids, users := []int64{}, []int64{}
	for _, x := range rules {
		if x.BackupForwardID > 0 {
			ids = append(ids, x.BackupForwardID)
			c.shared[x.BackupForwardID] = append(c.shared[x.BackupForwardID], x.ForwardID)
		}
		users = append(users, x.UserID)
		if x.ExternalNodeID > 0 {
			if _, ok := c.externals[x.ExternalNodeID]; !ok {
				if rows, err := h.repo.ExternalNodesByIDs(x.UserID, []int64{x.ExternalNodeID}); err == nil && len(rows) > 0 {
					e := rows[0]
					label := e.Protocol
					if up, err := linechain.FromJSON(e.Upstream); err == nil {
						label = up.ProtocolLabel()
					}
					c.externals[x.ExternalNodeID] = map[string]interface{}{"name": e.Name, "protocol": label}
				}
			}
		}
		if x.LandingNodeID > 0 {
			if _, ok := c.landings[x.LandingNodeID]; !ok {
				if n, s, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID); err == nil {
					c.landings[x.LandingNodeID] = map[string]interface{}{"id": n.ID, "name": n.Name, "protocol": n.Protocol, "serverName": s.Name, "status": n.Status}
				}
			}
		}
	}
	c.backups, _ = h.repo.LeiKwanBackupForwards(ids)
	c.backupRefs = map[int64][]repo.LeiKwanBackupRef{}
	if len(ids) > 0 {
		c.allRefs, _ = h.repo.ListLeiKwanBackupRefs()
		for _, r := range c.allRefs {
			c.backupRefs[r.BackupForwardID] = append(c.backupRefs[r.BackupForwardID], r)
		}
	}
	c.users = h.repo.LeiKwanUserNames(users)
	c.expired = h.repo.LeiKwanUsersExpired(users, time.Now().UnixMilli())
	if admin {
		c.hosts = h.leiKwanHostContexts()
	}
	return c
}

func (c *leiKwanRuleCtx) entryFor(userID int64) *leiKwanEntryNode {
	g, ok := c.grants[userID]
	if !ok {
		return nil
	}
	return leiKwanEntryByID(c.entries, g.MainEntryNodeID)
}

func (h *Handler) leiKwanRuleView(x model.LeiKwanRule, c *leiKwanRuleCtx, admin bool) map[string]interface{} {
	line := c.lines[x.LineID]
	v := map[string]interface{}{
		"id": x.ID, "name": x.Name, "lineId": x.LineID, "lineName": line.Name, "routeLabel": line.RouteLabel,
		"listenPort": x.ListenPort, "transport": x.Transport, "status": x.Status, "createdTime": x.CreatedTime,
		"backupOn": x.BackupForwardID > 0, "backupError": x.BackupError,
		// Plan 268: the owner's 到期 (shown 已到期 like normal rules).
		"userExpired": c.expired[x.UserID],
		// Plan 269: 利群流量 (billed, since the owner's reset) and the block state.
		"inFlow": x.InFlow, "outFlow": x.OutFlow, "blockReason": x.BlockReason,
	}
	// Plan 270: users never see the internal 备线 error (it can name forwards).
	if !admin && x.BackupError != "" {
		v["backupError"] = "备线还没建好，管理员处理中"
		if x.BackupForwardID > 0 && strings.HasPrefix(x.BackupError, lkBackupStale) {
			v["backupError"] = "备线还指向旧落地，管理员处理中"
		}
	}
	// Plan 276 (F02): the 备线 exists but could not follow the last 换落地.
	v["backupStale"] = x.BackupForwardID > 0 && strings.HasPrefix(x.BackupError, lkBackupStale)
	// Plan 271: 备线流量 (billed, since the owner's reset). A 备线 shared with
	// other rules shows its full bytes on each (no split): backupSharedCount
	// = the other rules, backupSharedWith = their names (users: own rules only).
	if x.BackupForwardID > 0 {
		v["backupInFlow"], v["backupOutFlow"] = x.BackupInFlow, x.BackupOutFlow
		names := []string{}
		others := 0
		for _, r := range c.backupRefs[x.BackupForwardID] {
			if r.RuleID == x.ID {
				continue
			}
			others++
			if admin || r.UserID == x.UserID {
				names = append(names, r.Name)
			}
		}
		if others > 0 {
			v["backupSharedCount"], v["backupSharedWith"] = others, names
		}
	}
	if x.PendingLandingNodeID > 0 {
		p := map[string]interface{}{"landingNodeId": x.PendingLandingNodeID}
		if n, s, err := h.repo.LeiKwanLandingTarget(x.PendingLandingNodeID); err == nil && n != nil {
			p["name"], p["protocol"], p["port"] = n.Name, n.Protocol, n.Port
			if s != nil {
				p["serverName"], p["host"] = s.Name, s.PublicHost
			}
		}
		v["pendingLanding"] = p
	}
	landing := map[string]interface{}{"kind": "manual", "host": x.TargetHost, "port": x.TargetPort}
	if x.ExternalNodeID > 0 {
		landing = map[string]interface{}{"kind": "external", "externalNodeId": x.ExternalNodeID, "host": x.TargetHost, "port": x.TargetPort}
		for k, val := range c.externals[x.ExternalNodeID] {
			landing[k] = val
		}
	}
	v["linkKind"] = leiKwanLinkKind(x)
	if x.LandingNodeID > 0 {
		landing = map[string]interface{}{"kind": "node", "landingNodeId": x.LandingNodeID, "host": x.TargetHost, "port": x.TargetPort}
		for k, val := range c.landings[x.LandingNodeID] {
			if k != "id" {
				landing[k] = val
			}
		}
	}
	v["landing"] = landing
	// Plan 273: 利群计费流量 = the vendor's own bytes of this forward in the
	// current cycle (separate from our nft 利群流量 and the 备线 bytes).
	accountID := x.AccountID
	if accountID == 0 {
		accountID = c.settingAccount
	}
	if rep, ok := c.vendor[accountID]; ok {
		if vf := lkRuleVendorFlow(rep.usage, rep.snap, x.ForwardID, admin, time.Now()); vf != nil {
			v["vendorFlow"] = vf
		}
	}
	if e := c.entryFor(x.UserID); e != nil {
		v["entry"] = map[string]interface{}{"nodeId": e.ID, "nodeName": e.Name, "host": e.Host, "address": net.JoinHostPort(e.Host, strconv.Itoa(x.ListenPort))}
	}
	if !admin {
		return v
	}
	v["userId"], v["userName"], v["forwardId"], v["source"] = x.UserID, c.users[x.UserID], x.ForwardID, x.Source
	v["allEntries"] = x.AllEntries == 1
	accel := parseLeiKwanAccel(line.Accel)
	over := parseLeiKwanRuleAccel(x.Accel)
	eff := map[string]interface{}{"warmMain": accel.Warm.Main, "warmBackup": accel.Warm.Backup, "ssFilter": accel.SSFilter, "logswitch": accel.Logswitch, "returnCheck": accel.ReturnCheck}
	if over.WarmMain != nil {
		eff["warmMain"] = *over.WarmMain
	}
	if over.SSFilter != nil {
		eff["ssFilter"] = *over.SSFilter
	}
	if over.Logswitch != nil {
		eff["logswitch"] = *over.Logswitch
	}
	if over.ReturnCheck != nil {
		eff["returnCheck"] = *over.ReturnCheck
	}
	v["accel"], v["accelOverride"] = eff, over
	if x.BackupForwardID > 0 {
		b := map[string]interface{}{"forwardId": x.BackupForwardID, "mode": x.BackupMode}
		if bv, ok := c.backups[x.BackupForwardID]; ok {
			b["name"], b["tunnelName"], b["remoteAddr"], b["inFlow"], b["outFlow"], b["status"] = bv.Name, bv.TunnelName, bv.RemoteAddr, bv.InFlow, bv.OutFlow, bv.Status
			b["entry"] = net.JoinHostPort(bv.EntryHost, strconv.Itoa(bv.EntryPort))
		} else {
			b["missing"] = true
		}
		others := []int64{}
		for _, fid := range c.shared[x.BackupForwardID] {
			if fid != x.ForwardID {
				others = append(others, fid)
			}
		}
		b["sharedWith"] = others
		v["backup"] = b
	}
	if x.LandingNodeID == 0 {
		if s, _ := h.repo.LeiKwanLandingServerByHost(x.TargetHost); s != nil {
			v["landingHint"] = fmt.Sprintf("落地机「%s」已是面板落地服务器，第二步可换成面板落地节点（链接会变）", s.Name)
		}
	}
	if c.hosts != nil {
		v["hostValues"] = hostValuesFor(c.hosts, x.ForwardID)
	}
	return v
}

func (h *Handler) leikwanRuleList(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	var req struct {
		UserID int64 `json:"userId"`
	}
	_ = leiKwanDecode(r, &req)
	admin := role == 0
	owner := uid
	if admin {
		owner = req.UserID // 0 = every user's rules
	}
	// Plan 270: a user without an active grant sees no 利群 rules or lines
	// (the page and the nav item are only for granted users).
	if !admin && !h.leiKwanUserGranted(uid) {
		response.WriteJSON(w, response.OK(map[string]interface{}{"rules": []interface{}{}, "admin": false, "granted": false}))
		return
	}
	rules, err := h.repo.ListLeiKwanRules(owner)
	if err != nil {
		leiKwanError(w, errors.New("读取利群转发失败"))
		return
	}
	c := h.leiKwanRuleContext(rules, admin)
	out := make([]map[string]interface{}, 0, len(rules))
	for _, x := range rules {
		out = append(out, h.leiKwanRuleView(x, c, admin))
	}
	result := map[string]interface{}{"rules": out, "admin": admin, "granted": true, "subEnabled": h.subLinkEnabled()}
	if setting, _ := h.repo.GetLeiKwanSetting(); setting != nil {
		result["entryLock"] = parseLeiKwanGlobal(setting.Global).EntryLock
	}
	if g, ok := c.grants[uid]; ok {
		gv := h.leiKwanGrantView(g, c.entries, int64(len(leiKwanOwnRules(rules, uid))), "")
		gv["backupFlow"] = leiKwanUserBackupFlow(c.allRefs, uid) // plan 271
		result["grant"] = gv
	}
	// Plan 280: the user's 利群转发数 — the page shows 已用 x / N and greys
	// 「添加转发」 at the limit (the create itself is checked again).
	if !admin {
		if limit, used, err := h.repo.UserLeiKwanRuleLimit(uid); err == nil {
			unlimited := repo.RuleLimitUnlimited(limit)
			v := map[string]interface{}{"limit": limit, "used": used, "unlimited": unlimited, "full": !unlimited && used >= int64(limit)}
			if !unlimited && used >= int64(limit) {
				v["error"] = (&repo.LeiKwanRuleLimitError{Limit: limit}).Error()
			}
			result["ruleLimit"] = v
		}
	}
	// Admins get every line; granted users the active ones (「可用专线」) —
	// name, route and transport only.
	ls := []map[string]interface{}{}
	for _, l := range c.lines {
		if !admin && l.Status != 1 {
			continue
		}
		ls = append(ls, map[string]interface{}{"id": l.ID, "name": l.Name, "routeLabel": l.RouteLabel, "transport": l.Transport, "status": l.Status, "inx": l.Inx})
	}
	sort.Slice(ls, func(i, j int) bool {
		return ls[i]["inx"].(int) < ls[j]["inx"].(int) || ls[i]["inx"].(int) == ls[j]["inx"].(int) && ls[i]["id"].(int64) < ls[j]["id"].(int64)
	})
	result["lines"] = ls
	response.WriteJSON(w, response.OK(result))
}

// leiKwanUserGranted: the user holds an active 利群 grant (status 1).
func (h *Handler) leiKwanUserGranted(uid int64) bool {
	g, err := h.repo.GetLeiKwanGrant(uid)
	return err == nil && g != nil && g.Status == 1
}

// leikwanRuleAccess (plan 270): whether the 「利群汇聚」 nav item / page is
// open for the caller — admins always, users with an active grant.
func (h *Handler) leikwanRuleAccess(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	admin := role == 0
	response.WriteJSON(w, response.OK(map[string]interface{}{"admin": admin, "granted": admin || h.leiKwanUserGranted(uid)}))
}

func leiKwanOwnRules(rules []model.LeiKwanRule, uid int64) []model.LeiKwanRule {
	out := []model.LeiKwanRule{}
	for _, x := range rules {
		if x.UserID == uid {
			out = append(out, x)
		}
	}
	return out
}

func leiKwanGrantUsable(g *model.LeiKwanGrant) error {
	if g == nil {
		return errors.New("没有利群汇聚授权，请联系管理员")
	}
	if g.Status != 1 {
		return errors.New("利群汇聚授权已停用")
	}
	// Plan 268: no grant-level expiry; the user's own 到期 applies.
	return nil
}

func (h *Handler) leikwanRuleOptions(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	var req struct {
		UserID int64 `json:"userId"`
	}
	_ = leiKwanDecode(r, &req)
	owner := uid
	if role == 0 && req.UserID > 0 {
		owner = req.UserID
	}
	// Plan 270: no lines, landings or quota (vendor read) without an active grant.
	if role != 0 && !h.leiKwanUserGranted(uid) {
		g, _ := h.repo.GetLeiKwanGrant(uid)
		msg := "没有利群汇聚授权，请联系管理员"
		if err := leiKwanGrantUsable(g); err != nil {
			msg = err.Error()
		}
		response.WriteJSON(w, response.OK(map[string]interface{}{"admin": false, "ownerId": owner, "grantError": msg,
			"lines": []interface{}{}, "landings": []interface{}{}, "quota": map[string]interface{}{"limit": 0, "active": 0}}))
		return
	}
	setting, account, _ := h.leiKwanSettingAccount()
	g, _ := h.repo.GetLeiKwanGrant(owner)
	out := map[string]interface{}{"admin": role == 0, "ownerId": owner}
	if setting != nil {
		entries := h.leiKwanEntryNodes(setting.EntryGroupID)
		if g != nil {
			n, _ := h.repo.CountLeiKwanRules(owner)
			out["grant"] = h.leiKwanGrantView(*g, entries, n, "")
		}
	}
	if err := leiKwanGrantUsable(g); err != nil {
		out["grantError"] = err.Error()
	} else if err := h.repo.CheckLeiKwanRuleLimit(owner, time.Now().UnixMilli(), role != 0); err != nil {
		// Plan 268/280: the user's 到期 / 利群转发数 block a new 利群 rule up front.
		out["grantError"] = err.Error()
		out["userBlocked"] = true
	}
	// Plan 280: 已用 x / N of the owner's 利群转发数 (limit 0 / ≥ 99999 = 不限).
	if limit, used, err := h.repo.UserLeiKwanRuleLimit(owner); err == nil {
		out["ruleLimit"] = map[string]interface{}{"limit": limit, "used": used, "unlimited": repo.RuleLimitUnlimited(limit)}
	}
	lines, _ := h.repo.ListLeiKwanLines()
	ls := []map[string]interface{}{}
	for _, l := range lines {
		if l.Status == 1 {
			ls = append(ls, map[string]interface{}{"id": l.ID, "name": l.Name, "routeLabel": l.RouteLabel, "backup": l.BackupTunnelID > 0})
		}
	}
	out["lines"] = ls
	nodes, _ := h.repo.ListLandingNodes(owner, false)
	landings := []map[string]interface{}{}
	for _, n := range nodes {
		if n.UserID != owner || n.Status != "active" {
			continue
		}
		s, err := h.repo.GetLandingServer(n.LandingServerID)
		if err != nil || s == nil {
			continue
		}
		landings = append(landings, map[string]interface{}{"id": n.ID, "name": n.Name, "protocol": n.Protocol, "serverName": s.Name, "host": s.PublicHost, "port": n.Port})
	}
	out["landings"] = landings
	// Plan 272: the owner's 外部节点 can be a 利群 landing (full link / sub node).
	externals := []map[string]interface{}{}
	if rows, err := h.repo.ExternalNodesByIDs(owner, nil); err == nil {
		for _, e := range rows {
			up, ok, reason := leiKwanExternalSupported(e)
			v := map[string]interface{}{"id": e.ID, "name": e.Name, "protocol": up.ProtocolLabel(), "host": e.Host, "port": e.Port, "supported": ok}
			if !ok {
				v["reason"] = reason
			}
			externals = append(externals, v)
		}
	}
	out["externals"] = externals
	q, _ := h.leiKwanQuota(r.Context(), account)
	out["quota"] = q
	response.WriteJSON(w, response.OK(out))
}

var leiKwanRuleKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,48}$`)

type leiKwanRuleTargetReq struct {
	LandingNodeID int64  `json:"landingNodeId"`
	TargetHost    string `json:"targetHost"`
	TargetPort    int    `json:"targetPort"`
	// Plan 272: one of the owner's 外部节点 (target = its server:port).
	ExternalNodeID int64 `json:"externalNodeId"`
}

// leiKwanResolveExternal: the owner's 外部节点 as a 利群 target.
func (h *Handler) leiKwanResolveExternal(id, owner int64) (string, int, error) {
	rows, err := h.repo.ExternalNodesByIDs(owner, []int64{id})
	if err != nil || len(rows) == 0 {
		return "", 0, errors.New("外部节点不存在（只能选转发主人自己的外部节点）")
	}
	up, ok, reason := leiKwanExternalSupported(rows[0])
	if !ok {
		return "", 0, errors.New("这个外部节点不能用作利群落地：" + reason)
	}
	host := strings.Trim(strings.TrimSpace(up.Server), "[]")
	if leiKwanBadHost(host) || up.Port < 1 || up.Port > 65535 {
		return "", 0, errors.New("外部节点的地址不能用作利群落地（需要公网地址或域名）")
	}
	return host, up.Port, nil
}

// leiKwanResolveTarget: the user's own panel landing node, or (admin) manual.
// The 外部节点 case is resolved by the callers (leiKwanResolveExternal).
func (h *Handler) leiKwanResolveTarget(req leiKwanRuleTargetReq, owner int64, admin bool) (int64, string, int, error) {
	if req.LandingNodeID > 0 {
		n, s, err := h.repo.LeiKwanLandingTarget(req.LandingNodeID)
		if err != nil {
			return 0, "", 0, err
		}
		if n.UserID != owner {
			return 0, "", 0, errors.New("只能选自己的落地节点")
		}
		if n.Status != "active" {
			return 0, "", 0, errors.New("落地节点未启用")
		}
		return n.ID, strings.TrimSpace(s.PublicHost), n.Port, nil
	}
	if !admin {
		return 0, "", 0, errors.New("请选择落地节点")
	}
	host := strings.TrimSpace(req.TargetHost)
	if leiKwanBadHost(host) || req.TargetPort < 1 || req.TargetPort > 65535 {
		return 0, "", 0, errors.New("手动落地需要公网地址或域名和 1–65535 端口")
	}
	return 0, host, req.TargetPort, nil
}

func (h *Handler) leikwanRuleCreate(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	var req struct {
		leiKwanRuleTargetReq
		LineID    int64  `json:"lineId"`
		Name      string `json:"name"`
		UserID    int64  `json:"userId"`
		RequestID string `json:"requestId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	admin := role == 0
	owner := uid
	if admin && req.UserID > 0 {
		owner = req.UserID
	}
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case req.Name == "":
		leiKwanError(w, errors.New("请填写转发名称"))
		return
	case len(req.Name) > 100:
		leiKwanError(w, errors.New("转发名称太长（最多 100 字节，约 33 个汉字）"))
		return
	case !leiKwanRuleKeyPattern.MatchString(req.RequestID):
		leiKwanError(w, errors.New("请求标识无效，请关闭弹窗后重试"))
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	g, err := h.repo.GetLeiKwanGrant(owner)
	if err != nil {
		leiKwanError(w, errors.New("读取授权失败"))
		return
	}
	if err := leiKwanGrantUsable(g); err != nil {
		leiKwanError(w, err)
		return
	}
	// Plan 268/280: the user's status / 到期, then 利群转发数 (user.leikwan_num,
	// 利群 rules only); an admin actor is exempt from the count.
	if err := h.repo.CheckLeiKwanRuleLimit(owner, time.Now().UnixMilli(), ruleLimitApplies(role)); err != nil {
		leiKwanError(w, err)
		return
	}
	line, err := h.repo.GetLeiKwanLine(req.LineID)
	if err != nil || line == nil || line.Status != 1 {
		leiKwanError(w, errors.New("利群专线不存在或已停用"))
		return
	}
	a, err := h.leiKwanAccount(line.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	var landingID int64
	var host string
	var port int
	if req.ExternalNodeID > 0 {
		host, port, err = h.leiKwanResolveExternal(req.ExternalNodeID, owner)
	} else {
		landingID, host, port, err = h.leiKwanResolveTarget(req.leiKwanRuleTargetReq, owner, admin)
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	target := leikwan.Target{Source: "manual", Host: host, Port: port}
	body := leikwan.CreateRequest{RouteID: line.RouteID, Transport: line.Transport, Target: target}
	raw, _ := json.Marshal(body)
	requestID := "rule:" + strconv.FormatInt(owner, 10) + ":" + req.RequestID
	create := &model.LeiKwanCreate{AccountID: a.ID, RequestID: requestID, Payload: string(raw)}
	previous, err := h.repo.GetLeiKwanCreate(a.ID, requestID)
	if err != nil {
		leiKwanError(w, errors.New("读取创建记录失败"))
		return
	}
	if previous == nil {
		view, err := h.leikwan.Overview(r.Context(), a.Token, a.ServiceID)
		if err != nil {
			leiKwanError(w, err)
			return
		}
		available := false
		for _, rt := range view.Options.Routes {
			if rt.ID == line.RouteID && rt.Transports[line.Transport].Available {
				available = true
			}
		}
		if !view.Options.Capabilities.AllowManage || !available {
			leiKwanError(w, errors.New("利群服务不允许创建转发或这条路线暂不可用"))
			return
		}
		if view.Options.ActiveForwardCount >= view.Options.ForwardLimit {
			leiKwanError(w, fmt.Errorf("利群名额已满（%d / %d），名额提升前不能再建", view.Options.ActiveForwardCount, view.Options.ForwardLimit))
			return
		}
	}
	if err := h.repo.PrepareLeiKwanCreate(create); err != nil {
		leiKwanError(w, err)
		return
	}
	var f leikwan.Forward
	tag := ""
	if create.ForwardID > 0 {
		if existing, _ := h.repo.GetLeiKwanRuleByForward(create.ForwardID); existing != nil {
			c := h.leiKwanRuleContext([]model.LeiKwanRule{*existing}, admin)
			response.WriteJSON(w, response.OK(map[string]interface{}{"rule": h.leiKwanRuleView(*existing, c, admin)}))
			return
		}
		f, tag, err = h.leikwan.GetForward(r.Context(), a.Token, create.ForwardID)
	} else {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", a.ID, requestID)))
		f, tag, err = h.leikwan.CreateForward(r.Context(), a.Token, a.ServiceID, hex.EncodeToString(sum[:]), body)
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if f.ServiceID != a.ServiceID || f.Route.ID != line.RouteID || !sameLeiKwanTarget(f.Target, target) {
		leiKwanError(w, errors.New("利群创建结果与请求不一致，请保留请求标识并检查利群侧"))
		return
	}
	x := &model.LeiKwanRule{UserID: owner, LineID: line.ID, AccountID: a.ID, ForwardID: f.ID, Name: req.Name, ListenPort: f.ListenPort, Transport: f.Transport,
		LandingNodeID: landingID, TargetHost: host, TargetPort: port, ETag: tag, Source: "panel", Status: 1, ExternalNodeID: req.ExternalNodeID}
	if err := h.saveLeiKwanRule(x); err != nil {
		leiKwanError(w, errors.New("利群侧已建好，请重试原请求完成登记"))
		return
	}
	_ = h.repo.CompleteLeiKwanCreate(create.ID, f.ID)
	warnings := []string{}
	if g.BackupLine == 1 {
		if err := h.leiKwanCreateBackup(x); err != nil {
			x.BackupError = err.Error()
			warnings = append(warnings, "备线未建好："+err.Error())
		}
		_ = h.saveLeiKwanRule(x)
	}
	h.leiKwanSubsChanged(owner) // plan 272: subscriptions show the new rule at once
	c := h.leiKwanRuleContext([]model.LeiKwanRule{*x}, admin)
	response.WriteJSON(w, response.OK(map[string]interface{}{"rule": h.leiKwanRuleView(*x, c, admin), "warnings": warnings}))
}

// leiKwanRuleAccess loads a rule its caller may change.
func (h *Handler) leiKwanRuleAccess(r *http.Request, id int64) (*model.LeiKwanRule, bool, error) {
	uid, role, _ := userRoleFromRequest(r)
	x, err := h.repo.GetLeiKwanRule(id)
	if err != nil || x == nil || (role != 0 && x.UserID != uid) {
		return nil, false, errors.New("利群转发不存在")
	}
	return x, role == 0, nil
}

// leiKwanFreshForward reads the LeiKwan forward and checks it still has the
// target the panel knows (someone may have changed it on the LeiKwan side).
func (h *Handler) leiKwanFreshForward(ctx context.Context, x *model.LeiKwanRule) (*model.LeiKwanAccount, leikwan.Forward, string, error) {
	a, err := h.leiKwanAccount(x.AccountID)
	if err != nil {
		return nil, leikwan.Forward{}, "", err
	}
	f, tag, err := h.leikwan.GetForward(ctx, a.Token, x.ForwardID)
	if err != nil {
		return a, f, "", err
	}
	if f.Target.Host != x.TargetHost || f.Target.Port != x.TargetPort {
		return a, f, "", &leikwan.APIError{Status: 412, Code: "forward_version_conflict"}
	}
	return a, f, tag, nil
}

func (h *Handler) leikwanRuleUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		leiKwanRuleTargetReq
		ID           int64                   `json:"id"`
		Name         string                  `json:"name"`
		ChangeTarget bool                    `json:"changeTarget"`
		Accel        *model.LeiKwanRuleAccel `json:"accel"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	x, admin, err := h.leiKwanRuleAccess(r, req.ID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		if len(name) > 100 {
			leiKwanError(w, errors.New("转发名称太长（最多 100 字节，约 33 个汉字）"))
			return
		}
		x.Name = name
	}
	if req.Accel != nil {
		if !admin {
			leiKwanError(w, errors.New("利群加速仅管理员可改"))
			return
		}
		if (req.Accel.WarmMain != nil && (*req.Accel.WarmMain < 0 || *req.Accel.WarmMain > 16)) || (req.Accel.SSFilter != nil && *req.Accel.SSFilter != "enforce" && *req.Accel.SSFilter != "off") || len(req.Accel.Note) > 200 {
			leiKwanError(w, errors.New("利群加速数值无效"))
			return
		}
		raw, _ := json.Marshal(req.Accel)
		x.Accel = string(raw)
	}
	warnings := []string{}
	if req.ChangeTarget {
		var landingID int64
		var host string
		var port int
		var err error
		if req.ExternalNodeID > 0 {
			host, port, err = h.leiKwanResolveExternal(req.ExternalNodeID, x.UserID)
		} else {
			landingID, host, port, err = h.leiKwanResolveTarget(req.leiKwanRuleTargetReq, x.UserID, admin)
		}
		if err != nil {
			leiKwanError(w, err)
			return
		}
		if err := h.leiKwanFilterKeyGuard(x, landingID); err != nil {
			leiKwanError(w, err)
			return
		}
		if host != x.TargetHost || port != x.TargetPort {
			a, _, tag, err := h.leiKwanFreshForward(r.Context(), x)
			if err != nil {
				leiKwanError(w, err)
				return
			}
			f, newTag, err := h.leikwan.PatchTarget(r.Context(), a.Token, x.ForwardID, tag, leikwan.Target{Source: "manual", Host: host, Port: port})
			if err != nil {
				leiKwanError(w, err)
				return
			}
			h.leikwan.Invalidate(a.Token, a.ServiceID)
			x.TargetHost, x.TargetPort, x.ETag = f.Target.Host, f.Target.Port, newTag
			if x.BackupMode == "linked" {
				if n, _ := h.repo.CountLeiKwanRulesByBackup(x.BackupForwardID); n <= 1 {
					// Plan 269: a linked backup used only by this rule moves with it
					// (same entry port: hosts' 日志快切 and phone backup ports keep working).
					if err := h.leiKwanRetargetForward(x.BackupForwardID, leiKwanTargetAddr(*x)); err != nil {
						x.BackupError = lkBackupStaleError(err)
						warnings = append(warnings, "备线目标未改好："+err.Error())
					} else {
						x.BackupError = "" // plan 276 (F02)
					}
				} else {
					// Shared by other rules (e.g. 280/282): keep it for them; the
					// rule gets its own automatic 备线.
					x.BackupForwardID, x.BackupMode = 0, ""
					if g, _ := h.repo.GetLeiKwanGrant(x.UserID); g != nil && g.BackupLine == 1 {
						if err := h.leiKwanCreateBackup(x); err != nil {
							x.BackupError = err.Error()
							warnings = append(warnings, "备线未建好："+err.Error())
						}
					}
				}
			} else if err := h.leiKwanRetargetBackup(x); err != nil {
				x.BackupError = lkBackupStaleError(err)
				warnings = append(warnings, "备线目标未改好："+err.Error())
			}
		}
		x.LandingNodeID, x.ExternalNodeID = landingID, req.ExternalNodeID
		if x.PendingLandingNodeID > 0 && x.PendingLandingNodeID == landingID {
			x.PendingLandingNodeID = 0 // 待切换 done
		}
	}
	if err := h.saveLeiKwanRule(x); err != nil {
		leiKwanError(w, errors.New("保存利群转发失败"))
		return
	}
	if req.ChangeTarget {
		// The first-packet filter key follows at once (not after the 2 s debounce).
		h.leiKwanHostPushAllAsync()
	}
	h.leiKwanSubsChanged(x.UserID)
	c := h.leiKwanRuleContext([]model.LeiKwanRule{*x}, admin)
	response.WriteJSON(w, response.OK(map[string]interface{}{"rule": h.leiKwanRuleView(*x, c, admin), "warnings": warnings}))
}

func (h *Handler) leikwanRuleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          int64 `json:"id"`
		Confirmed   bool  `json:"confirmed"`
		KeepLeiKwan bool  `json:"keepLeiKwan"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if !req.Confirmed {
		leiKwanError(w, errors.New("请确认删除"))
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	x, admin, err := h.leiKwanRuleAccess(r, req.ID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if req.KeepLeiKwan && !admin {
		leiKwanError(w, errors.New("仅管理员可只解除登记"))
		return
	}
	if !req.KeepLeiKwan {
		if err := h.leiKwanDeleteForward(r.Context(), x); err != nil {
			leiKwanError(w, err)
			return
		}
	}
	backupNote := ""
	if x.BackupMode == "linked" && x.BackupForwardID > 0 {
		backupNote = fmt.Sprintf("导入时链接的备线（转发 #%d）保留为普通转发", x.BackupForwardID)
	}
	if req.KeepLeiKwan {
		// Plan 269: the LeiKwan forward stays on the hosts — keep its 利群加速
		// there as it is (unmanaged values in each learned host profile).
		h.leiKwanFreezeHostValues(*x)
	}
	if err := h.leiKwanDropBackup(x); err != nil {
		backupNote = err.Error()
	}
	if err := h.deleteLeiKwanRule(x.ID); err != nil {
		leiKwanError(w, errors.New("删除利群转发记录失败"))
		return
	}
	h.leiKwanSubsChanged(x.UserID)
	response.WriteJSON(w, response.OK(map[string]interface{}{"backupNote": backupNote}))
}

// leiKwanDeleteForward deletes the rule's LeiKwan forward (already gone = ok).
func (h *Handler) leiKwanDeleteForward(ctx context.Context, x *model.LeiKwanRule) error {
	a, _, tag, err := h.leiKwanFreshForward(ctx, x)
	var apiErr *leikwan.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == 404:
		return nil // already gone on the LeiKwan side
	case err != nil:
		return err
	}
	if err := h.leikwan.DeleteForward(ctx, a.Token, x.ForwardID, tag); err != nil {
		return err
	}
	h.leikwan.Invalidate(a.Token, a.ServiceID)
	return nil
}

func (h *Handler) leikwanRuleLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	x, _, err := h.leiKwanRuleAccess(r, req.ID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	g, _ := h.repo.GetLeiKwanGrant(x.UserID)
	if g == nil {
		leiKwanError(w, errors.New("没有利群汇聚授权，入口未定"))
		return
	}
	if g.Status != 1 {
		leiKwanError(w, errors.New("利群汇聚授权已停用，没有链接")) // plan 276 (F12)
		return
	}
	n, err := h.repo.GetNodeRecord(g.MainEntryNodeID)
	if err != nil || n == nil {
		leiKwanError(w, errors.New("主入口节点不存在"))
		return
	}
	addr := net.JoinHostPort(n.ServerIP, strconv.Itoa(x.ListenPort))
	out := map[string]interface{}{"address": addr}
	if x.LandingNodeID > 0 {
		if ln, _, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID); err == nil {
			cp := *ln
			cp.Name = x.Name
			out["url"] = repo.LandingURL(cp, n.ServerIP, x.ListenPort)
		}
	} else if x.ExternalNodeID > 0 {
		if node, err := h.leiKwanRuleNode(*x, strings.Trim(n.ServerIP, "[]"), x.Name); err == nil {
			out["url"] = sublink.ShareURI(node)
		} else {
			out["note"] = err.Error()
		}
	} else {
		out["note"] = "手动落地没有节点链接，只有入口地址"
	}
	response.WriteJSON(w, response.OK(out))
}

// ---- entry-host status (read-only) ----

func (h *Handler) leikwanHostList(w http.ResponseWriter, r *http.Request) {
	setting, _, _ := h.leiKwanSettingAccount()
	if setting == nil {
		leiKwanError(w, errors.New("读取利群设置失败"))
		return
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	states, _ := h.repo.ListLeiKwanHostStates()
	byNode := map[int64]model.LeiKwanHostState{}
	for _, s := range states {
		byNode[s.NodeID] = s
	}
	counts, _ := h.repo.CountLeiKwanGrantsByEntry()
	out := make([]map[string]interface{}, 0, len(entries))
	g, _ := h.newLKGen()
	attention := 0
	for _, e := range entries {
		item := map[string]interface{}{"node": e, "mainEntryUsers": counts[e.ID]}
		if s, ok := byNode[e.ID]; ok {
			item["snapshot"], item["source"], item["capturedAt"] = parseLeiKwanHostSnapshot(s.Data), s.Source, s.CapturedAt
		}
		// Plan 269: 接管 / drift / traffic switch of this host.
		mv := h.leiKwanHostManageView(e.ID, g)
		if a, ok := mv["attention"].([]string); ok && len(a) > 0 {
			attention++
		}
		item["manage"] = mv
		out = append(out, item)
	}
	group, _ := h.repo.GetNodeGroupByID(setting.EntryGroupID)
	groupName := ""
	if group != nil {
		groupName = group.Name
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"entryGroupId": setting.EntryGroupID, "entryGroupName": groupName, "hosts": out, "global": parseLeiKwanGlobal(setting.Global), "triggers": leiKwanTriggers, "attention": attention}))
}

// leikwanHostImport stores a secret-free host snapshot (step 1: taken with a
// read-only SSH script; step 2: reported by the entry agent). Unknown fields
// are rejected and anything that looks like a key or token is refused.
func (h *Handler) leikwanHostImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID   int64               `json:"nodeId"`
		Source   string              `json:"source"`
		Snapshot leiKwanHostSnapshot `json:"snapshot"`
	}
	if err := leiKwanDecodeLarge(r, &req, 64<<10); err != nil {
		leiKwanError(w, err)
		return
	}
	if n, err := h.repo.GetNodeRecord(req.NodeID); err != nil || n == nil {
		leiKwanError(w, errors.New("节点不存在"))
		return
	}
	raw, _ := json.Marshal(req.Snapshot)
	if leiKwanSecretLike(raw) {
		leiKwanError(w, errors.New("快照里有疑似密钥的长字符串，已拒绝"))
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" || len(source) > 20 {
		source = "ssh-readonly"
	}
	if err := h.repo.SaveLeiKwanHostState(req.NodeID, string(raw), source, time.Now().UnixMilli()); err != nil {
		leiKwanError(w, errors.New("保存主机状态失败"))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}
