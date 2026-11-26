package handler

// Plan 272 利群规则「诊断」, plan 301 v2: the path from the viewer's chosen
// entry (server 主入口, or 手机本地 in the im.gamer App), one-shot on click.
//   - 本机 → 入口 (App, server entry): the phone's own TCP handshakes, sent
//     along by the page (bridge tcpPing); browsers start at the entry.
//   - 入口 → 专线入口: the entry agent times a TCP handshake to each relay
//     ingress the LeiKwan client was offered (agent command LeiKwanDiag) and
//     reads which one the client uses. Users see one row (no relay IPs),
//     admins every ingress. 手机本地: measured on the phone (bridge diag).
//   - 全程: the LeiKwan client's own rtt (entry status file / phone core); it
//     ends at the line's exit (the vendor probes never reach the landing).
//   - 专线出口 → 落地: measured on a native landing — the kernel TCP rtt of
//     the relay exit's inbound connections (agent command LandingPeerRtt).
//   - 专线入口 → 专线出口 = 「端内」 (plan 306 r2, the user's word for the
//     line's own entry → exit delay): 全程 − 入口段, a plain number with the
//     tag 「端内 X ms」 for every user; 到落地 = 入口段 + 端内 + 专线出口 → 落地.
//     Without a landing measurement: 专线入口 → 落地 = 全程 − 入口 → 专线入口
//     (估算, as plan 272).
//   - 出口测试: panel landing on an FLVX node → the existing TcpPing rotation.
//   - 备线: the existing rule diagnosis of the 备线 forward, as one row.
// Only the chosen entry (no 「其他利群入口机」). No new pollers; ≤ 5
// handshakes per ingress; per-command and total timeouts; one diagnose per
// user per 10 s.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/geoip"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

const (
	lkDiagTotalTimeout = 40 * time.Second
	lkDiagCmdTimeout   = 25 * time.Second
	lkDiagCount        = 3
	lkDiagPingMs       = 1500
	lkDiagGap          = 10 * time.Second
)

// leiKwanLabCommand answers node commands in the leikwanlab build (screens
// without agents); nil in release builds.
var leiKwanLabCommand func(nodeID int64, cmd string, data interface{}) (ws.CommandResult, bool)

// lkAgentDiag mirrors the agent's LeiKwanDiagResponse.
type lkAgentDiag struct {
	V         int    `json:"v"`
	ForwardID int64  `json:"forwardId"`
	Found     bool   `json:"found"`
	State     string `json:"state"`
	Ingresses []struct {
		Endpoint string  `json:"endpoint"`
		InUse    string  `json:"inUse"`
		Offered  bool    `json:"offered"`
		OK       bool    `json:"ok"`
		AvgMs    float64 `json:"avgMs"`
		Loss     float64 `json:"loss"`
		Error    string  `json:"error"`
	} `json:"ingresses"`
	TCP *lkAgentClient `json:"tcp"`
	UDP *lkAgentClient `json:"udp"`
}

type lkAgentClient struct {
	RttMs float64 `json:"rttMs"`
	P95Ms float64 `json:"p95Ms"`
	Sent  int     `json:"sent"`
	Recv  int     `json:"recv"`
	AgeS  int64   `json:"ageS"`
}

// lkDiagItem is one result row (see the frontend modal).
type lkDiagItem map[string]interface{}

func lkDiagRow(section, title, sub string) lkDiagItem {
	return lkDiagItem{"section": section, "title": title, "sub": sub, "success": false, "averageTime": 0.0, "packetLoss": 100.0}
}

var circledNum = []string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧"}

func lkCircled(i int) string {
	if i >= 0 && i < len(circledNum) {
		return circledNum[i]
	}
	return strconv.Itoa(i + 1)
}

// ---- relay ingress label (admin only) ----

// lkProviderPrefixes: well-known cloud blocks the LeiKwan relays use
// (best effort; unknown blocks show the country only).
var lkProviderPrefixes = []struct {
	prefix netip.Prefix
	name   string
}{}

func init() {
	add := func(name string, cidrs ...string) {
		for _, c := range cidrs {
			if p, err := netip.ParsePrefix(c); err == nil {
				lkProviderPrefixes = append(lkProviderPrefixes, struct {
					prefix netip.Prefix
					name   string
				}{p, name})
			}
		}
	}
	add("UCloud", "106.75.0.0/16", "117.50.0.0/16", "152.32.128.0/17")
	add("华为云", "121.36.0.0/15", "124.70.0.0/15", "139.9.0.0/16", "139.159.0.0/16", "113.44.0.0/15", "119.3.0.0/16", "122.9.0.0/16", "116.205.0.0/16", "1.94.0.0/15", "60.204.0.0/16", "123.249.0.0/16", "124.71.0.0/16")
	add("腾讯云", "43.136.0.0/13", "106.52.0.0/14", "114.132.0.0/16", "175.178.0.0/16", "119.29.0.0/16", "1.116.0.0/15", "150.158.0.0/16", "129.204.0.0/16", "42.192.0.0/15", "81.68.0.0/14", "49.232.0.0/14", "101.32.0.0/14")
	add("阿里云", "47.92.0.0/14", "47.96.0.0/11", "8.128.0.0/10", "39.96.0.0/13", "120.24.0.0/14", "112.124.0.0/14")
	add("百度云", "106.12.0.0/14", "180.76.0.0/16")
	add("火山引擎", "101.126.0.0/16", "14.103.0.0/16", "101.96.0.0/16")
}

var lkCountryNames = map[string]string{"CN": "中国", "HK": "香港", "TW": "台湾", "JP": "日本", "SG": "新加坡", "US": "美国", "KR": "韩国", "DE": "德国"}

// lkIngressLabel: 「华为云 · 中国」 for a relay ingress address.
func lkIngressLabel(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	parts := []string{}
	for _, p := range lkProviderPrefixes {
		if p.prefix.Contains(addr) {
			parts = append(parts, p.name)
			break
		}
	}
	if cc := geoip.Lookup(addr); cc != "" {
		if name, ok := lkCountryNames[cc]; ok {
			parts = append(parts, name)
		} else {
			parts = append(parts, cc)
		}
	}
	return strings.Join(parts, " · ")
}

// lkEndpointHost: the relay ingress IP without its port (always :5438).
func lkEndpointHost(endpoint string) string {
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return host
	}
	return endpoint
}

// ---- running ----

// lkDiagPhoneHop: TCP handshakes the im.gamer App made (plan 301).
type lkDiagPhoneHop struct {
	OK    bool    `json:"ok"`
	AvgMs float64 `json:"avgMs"`
	Loss  float64 `json:"loss"`
	InUse bool    `json:"inUse"`
}

// lkDiagPhoneTotal: 全程 as the phone's LeiKwan core reports it.
type lkDiagPhoneTotal struct {
	Source string   `json:"source"`
	RttMs  float64  `json:"rttMs"`
	AgeS   int64    `json:"ageS"`
	Loss   *float64 `json:"loss"`
}

// lkDiagPhone: what the page measured through the App (bridge diag / tcpPing).
type lkDiagPhone struct {
	Device  *lkDiagPhoneHop   `json:"device"`  // 本机 → 入口 (server entry)
	Ingress *lkDiagPhoneHop   `json:"ingress"` // 手机 → 专线入口 (手机本地)
	Total   *lkDiagPhoneTotal `json:"total"`   // 手机本地 全程
	Backup  *lkDiagPhoneHop   `json:"backup"`  // 手机 → 备线入口 (手机本地)
	Error   string            `json:"error"`   // why the App could not measure
}

// lkDiagJob: one diagnose run of a rule.
type lkDiagJob struct {
	h       *Handler
	admin   bool
	rule    model.LeiKwanRule
	line    model.LeiKwanLine
	entry   *lkEntry
	local   bool // 手机本地 (the phone is the entry)
	phone   *lkDiagPhone
	landing string // landing display name
	path    map[string]interface{}
	// exitNode: FLVX node of a native landing server (0 = no exit test).
	exitNode int64
	exitSkip string
	// landingPort: the native landing node's port (0 = 专线出口 → 落地 not
	// measurable: manual / external / shared landing).
	landingPort int

	// Row indexes (-1 = none).
	iDevice, iHop1, iVendor, iLanding, iExit, iBackup int

	mu      sync.Mutex
	items   []lkDiagItem
	summary map[string]interface{}
	emit    func(index int, item lkDiagItem)
	// Parts for the composition (-1 = not measured).
	hop1Ms, totalMs, landingMs float64
	totalSource                string
	totalAgeS                  int64
	totalLoss                  *float64
	hop1Why, landingWhy        string
	// Plan 306: the landing's relay-exit sources with their ping, the other
	// 利群 rules on the same landing node (their exits share the port) and
	// how many sources were not pinged.
	exits      []lkExitCand
	exitOthers int
	sharedWith []string
}

func (h *Handler) leiKwanDiagPrepare(r *http.Request) (*lkDiagJob, error) {
	var req struct {
		ID    int64 `json:"id"`
		Local bool  `json:"local"`
		// The App's numbers: read leniently (a newer App may send more fields).
		Phone json.RawMessage `json:"phone"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		return nil, err
	}
	var phone *lkDiagPhone
	if len(req.Phone) > 0 && string(req.Phone) != "null" {
		phone = &lkDiagPhone{}
		if json.Unmarshal(req.Phone, phone) != nil {
			return nil, errors.New("请求参数错误")
		}
	}
	x, admin, err := h.leiKwanRuleAccess(r, req.ID)
	if err != nil {
		return nil, err
	}
	uid, _, _ := userRoleFromRequest(r)
	if req.Local && !admin {
		return nil, errLKLocalAdminOnly
	}
	if !h.lkThrottle.allow("diag:"+strconv.FormatInt(uid, 10), time.Now(), lkDiagGap) {
		return nil, errors.New("诊断太频繁，请 10 秒后再试")
	}
	j := &lkDiagJob{h: h, admin: admin, rule: *x, local: req.Local, phone: phone, hop1Ms: -1, totalMs: -1, landingMs: -1,
		iDevice: -1, iHop1: -1, iVendor: -1, iLanding: -1, iExit: -1, iBackup: -1}
	if l, err := h.repo.GetLeiKwanLine(x.LineID); err == nil && l != nil {
		j.line = *l
	}
	switch {
	case req.Local:
		j.entry = lkLocalEntry()
	default:
		// Plan 301: the viewer's chosen entry (an admin looking at another
		// user's forward diagnoses it from the admin's own 主入口).
		if uid != x.UserID {
			j.entry = h.leiKwanUserEntry(uid)
		}
		if j.entry == nil {
			j.entry = h.leiKwanUserEntry(x.UserID)
		}
	}
	if j.entry == nil {
		return nil, errors.New("还没有主入口，诊断不了")
	}
	entryName := j.entry.Name
	if j.local {
		entryName = "手机"
	}
	path := map[string]interface{}{"entryName": j.entry.Name, "entryAddress": net.JoinHostPort(j.entry.Host, strconv.Itoa(x.ListenPort)),
		"lineName": j.line.Name, "routeLabel": j.line.RouteLabel, "local": j.local}
	switch {
	case x.LandingNodeID > 0:
		path["landingKind"] = "node"
		if n, s, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID); err == nil && n != nil {
			j.landing = n.Name
			sub := strings.ToUpper(n.Protocol)
			if s != nil {
				sub = s.Name + " · " + sub
				if s.Source != "shared" && s.NodeID > 0 {
					j.exitNode = s.NodeID
					if n.Port > 0 {
						j.landingPort = n.Port
					}
				} else {
					j.exitSkip = "共享落地机上不测出口"
					if admin {
						j.exitSkip = "共享落地机上不测出口（妙妙屋那边的机器）"
					}
				}
			}
			path["landingSub"] = sub
		} else {
			j.landing = net.JoinHostPort(x.TargetHost, strconv.Itoa(x.TargetPort))
			j.exitSkip = "落地节点不存在"
		}
	case x.ExternalNodeID > 0:
		path["landingKind"] = "external"
		j.landing = "外部节点"
		path["landingSub"] = "外部节点"
		if rows, err := h.repo.ExternalNodesByIDs(x.UserID, []int64{x.ExternalNodeID}); err == nil && len(rows) > 0 {
			j.landing = rows[0].Name
			if up, _, _ := leiKwanExternalSupported(rows[0]); up.Type != "" {
				path["landingSub"] = "外部节点 · " + up.ProtocolLabel()
			}
		}
	default:
		path["landingKind"] = "manual"
		j.landing = net.JoinHostPort(x.TargetHost, strconv.Itoa(x.TargetPort))
		path["landingSub"] = "手动落地（不在面板）"
	}
	path["landingName"] = j.landing
	if !admin && x.LandingNodeID == 0 && x.ExternalNodeID == 0 {
		path["landingName"] = "手动落地"
	}
	path["landingMeasurable"] = j.landingPort > 0
	if j.landingPort > 0 {
		// Plan 306: rules sharing this landing node send their relay exits to
		// the same port — the diagnosis must not pass another line's exit off
		// as this one's.
		if rows, err := h.repo.LeiKwanRulesOnLanding(x.LandingNodeID, x.ID); err == nil {
			for _, o := range rows {
				j.sharedWith = append(j.sharedWith, o.Name)
			}
		}
	}
	device := !j.local && j.phone != nil && j.phone.Device != nil
	path["device"] = device
	j.path = path
	// Placeholders in display order.
	add := func(it lkDiagItem) int {
		j.items = append(j.items, it)
		return len(j.items) - 1
	}
	if device {
		j.iDevice = add(lkDiagRow("device", "本机 → "+entryName, ""))
	}
	j.iHop1 = add(lkDiagRow("hop1", entryName+" → 利群专线", ""))
	if j.landingPort > 0 {
		j.iVendor = add(lkDiagRow("vendor", "利群专线入口 → 专线出口", ""))
		j.iLanding = add(lkDiagRow("landing", "专线出口 → "+j.landingTitle(), ""))
	} else {
		j.iVendor = add(lkDiagRow("hop2", "利群专线 → "+j.landingTitle(), ""))
	}
	if x.LandingNodeID > 0 {
		j.iExit = add(lkDiagRow("exit", j.landingTitle()+" → 外网", ""))
	}
	if x.BackupForwardID > 0 {
		j.iBackup = add(lkDiagRow("backup", j.backupTitle(""), ""))
	}
	for _, it := range j.items {
		it["diagnosing"] = true
		it["message"] = "诊断中..."
	}
	return j, nil
}

func (j *lkDiagJob) landingTitle() string {
	if !j.admin && j.rule.LandingNodeID == 0 && j.rule.ExternalNodeID == 0 {
		return "手动落地"
	}
	return j.landing
}

// entryTitle: the first node of the path (「手机」 on 手机本地).
func (j *lkDiagJob) entryTitle() string {
	if j.local {
		return "手机"
	}
	return j.entry.Name
}

func (j *lkDiagJob) startPayload() map[string]interface{} {
	items := make([]lkDiagItem, len(j.items))
	copy(items, j.items)
	return map[string]interface{}{"total": len(items), "timestamp": time.Now().UnixMilli(), "admin": j.admin, "ruleName": j.rule.Name, "path": j.path, "items": items}
}

// set replaces (or, index < 0, appends) a row and emits it.
func (j *lkDiagJob) set(index int, it lkDiagItem) {
	j.mu.Lock()
	if index < 0 || index >= len(j.items) {
		index = len(j.items)
		j.items = append(j.items, it)
	} else {
		j.items[index] = it
	}
	emit := j.emit
	j.mu.Unlock()
	if emit != nil {
		emit(index, it)
	}
}

func (j *lkDiagJob) progress() diagnosisProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	p := diagnosisProgress{Total: len(j.items)}
	for _, it := range j.items {
		if it["diagnosing"] == true {
			continue
		}
		p.Completed++
		if it["success"] == true || it["skipped"] == true {
			p.Success++
		} else {
			p.Failed++
		}
	}
	return p
}

// agentDiag runs LeiKwanDiag on one entry host.
func (j *lkDiagJob) agentDiag(nodeID int64) (*lkAgentDiag, string) {
	h := j.h
	if h.wsServer != nil && !h.wsServer.NodeOnline(nodeID) && h.nodeCommandSender == nil && leiKwanLabCommand == nil {
		return nil, "入口机离线"
	}
	res, err := h.sendNodeCommandWithTimeout(nodeID, "LeiKwanDiag", map[string]interface{}{"forwardId": j.rule.ForwardID, "count": lkDiagCount, "timeoutMs": lkDiagPingMs},
		lkDiagCmdTimeout, false, false)
	msg := ""
	if err != nil {
		msg = err.Error()
	} else if !res.Success {
		msg = res.Message
	}
	if msg != "" {
		if lkUnknownCommand(msg) {
			return nil, "入口机 agent 需要升级才能诊断"
		}
		if strings.Contains(msg, "超时") || strings.Contains(strings.ToLower(msg), "timeout") {
			return nil, "入口机没有及时回应（超时）"
		}
		return nil, msg
	}
	raw, _ := json.Marshal(res.Data)
	var d lkAgentDiag
	if json.Unmarshal(raw, &d) != nil || d.V != 1 {
		return nil, "入口机 agent 需要升级才能诊断"
	}
	if !d.Found {
		return nil, "入口机上没有这条利群转发"
	}
	return &d, ""
}

func lkUnknownCommand(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(low, "unknown") || strings.Contains(msg, "未知") || strings.Contains(low, "not support") || strings.Contains(msg, "不支持")
}

// lkDiagMaxClientRtt: a client rtt above this is not a path measurement.
// Seen on prod (fwd 263, 11-17): active_udp.rtt_ms 60000 and 3018 without
// probe counters while UDP probes got no answer; latest_tcp said 64 ms.
const lkDiagMaxClientRtt = 2000.0

func lkDiagClientOK(c *lkAgentClient) bool {
	return c != nil && c.RttMs > 0 && c.RttMs <= lkDiagMaxClientRtt
}

// lkDiagSource: where 全程 comes from — the client's UDP probe when it has
// answered probes (it also gives loss), else its last TCP selection, else a
// UDP value without probe counters.
func lkDiagSource(d *lkAgentDiag) (string, *lkAgentClient) {
	return lkClientPick(d.UDP, d.TCP)
}

// lkClientPick: the one UDP-vs-TCP choice (+ the sanity cap) shared by 诊断
// and the plan-310 rule latency tag, so the two always agree.
func lkClientPick(udp, tcp *lkAgentClient) (string, *lkAgentClient) {
	switch {
	case lkDiagClientOK(udp) && udp.Sent > 0 && udp.Recv > 0:
		return "udp", udp
	case lkDiagClientOK(tcp):
		return "tcp", tcp
	case lkDiagClientOK(udp):
		return "udp", udp
	}
	return "", nil
}

// lkDiagPick: the ingress the client uses for that source (so 全程 = 第一跳
// + the rest on the same path), else any in use, else the fastest reachable.
func lkDiagPick(d *lkAgentDiag, source string) int {
	if source != "" {
		for i, in := range d.Ingresses {
			if strings.Contains(in.InUse, source) {
				return i
			}
		}
	}
	for i, in := range d.Ingresses {
		if in.InUse != "" {
			return i
		}
	}
	best := -1
	for i, in := range d.Ingresses {
		if in.OK && (best < 0 || in.AvgMs < d.Ingresses[best].AvgMs) {
			best = i
		}
	}
	return best
}

func lkRound(v float64) float64 { return math.Round(v*10) / 10 }

// runDevice: 本机 → 入口, measured by the App (server entry).
func (j *lkDiagJob) runDevice() {
	if j.iDevice < 0 {
		return
	}
	d := j.phone.Device
	it := lkDiagRow("device", "本机 → "+j.entry.Name, "在这台手机上测（TCP 握手）")
	it["success"], it["averageTime"], it["packetLoss"] = d.OK, lkRound(d.AvgMs), d.Loss
	if !d.OK {
		it["message"] = "这台手机连不上入口机"
	}
	j.set(j.iDevice, it)
}

// runHop1: 入口 → 专线入口 (+ 全程). 手机本地: from the phone's numbers.
func (j *lkDiagJob) runHop1(ctx context.Context) {
	if j.local {
		j.phoneHop1()
		return
	}
	d, errMsg := j.agentDiag(j.entry.NodeID)
	if d == nil {
		it := lkDiagRow("hop1", j.entry.Name+" → 利群专线", "")
		it["message"] = errMsg
		j.set(j.iHop1, it)
		j.mu.Lock()
		j.hop1Why = "第一跳没测到"
		j.mu.Unlock()
		return
	}
	source, client := lkDiagSource(d)
	pick := lkDiagPick(d, source)
	j.mu.Lock()
	if pick >= 0 && d.Ingresses[pick].OK {
		j.hop1Ms = d.Ingresses[pick].AvgMs
	}
	if client != nil {
		j.totalMs, j.totalSource, j.totalAgeS = client.RttMs, source, client.AgeS
		if source == "udp" && client.Sent > 0 {
			loss := lkRound(float64(client.Sent-client.Recv) * 100 / float64(client.Sent))
			j.totalLoss = &loss
		}
	}
	j.mu.Unlock()
	if j.admin {
		if len(d.Ingresses) == 0 {
			it := lkDiagRow("hop1", j.entry.Name+" → 利群专线", "")
			it["message"] = "利群客户端现在没有候选入口（转发状态：" + d.State + "）"
			j.set(j.iHop1, it)
		}
		for i, in := range d.Ingresses {
			sub := strings.TrimSpace(strings.Trim(lkIngressLabel(in.Endpoint)+" · "+lkEndpointHost(in.Endpoint), " ·"))
			if !in.Offered {
				sub += "（之前选的入口，已不在当前候选里）"
			}
			it := lkDiagRow("hop1", j.entry.Name+" → 利群入口 "+lkCircled(i), sub)
			it["inUse"], it["success"], it["averageTime"], it["packetLoss"] = in.InUse, in.OK, lkRound(in.AvgMs), in.Loss
			if !in.OK || in.Error != "" {
				it["message"] = in.Error
			}
			idx := -1
			if i == 0 {
				idx = j.iHop1
			}
			j.set(idx, it)
		}
		return
	}
	it := lkDiagRow("hop1", j.entry.Name+" → 利群专线", "在你的主入口上测")
	if pick >= 0 {
		in := d.Ingresses[pick]
		it["inUse"], it["success"], it["averageTime"], it["packetLoss"] = in.InUse, in.OK, lkRound(in.AvgMs), in.Loss
		if !in.OK {
			it["message"] = "利群专线入口连不上"
		}
		if in.InUse == "" {
			it["sub"] = "客户端还没选定入口，取最快的一个"
		}
	} else {
		it["message"] = "利群客户端现在没有候选入口"
	}
	j.set(j.iHop1, it)
}

// phoneHop1: 手机 → 专线入口 and 全程 from the App (bridge diag).
func (j *lkDiagJob) phoneHop1() {
	it := lkDiagRow("hop1", "手机 → 利群专线", "在这台手机上测（手机本地）")
	p := j.phone
	if p == nil || p.Ingress == nil {
		msg := "App 没有测到（要 im.gamer 1.0.10 以上，并且手机本地已连接）"
		if p != nil && strings.TrimSpace(p.Error) != "" {
			msg = strings.TrimSpace(p.Error)
		}
		it["message"] = msg
		j.set(j.iHop1, it)
		j.mu.Lock()
		j.hop1Why = "第一跳没测到"
		j.mu.Unlock()
	} else {
		in := p.Ingress
		it["success"], it["averageTime"], it["packetLoss"] = in.OK, lkRound(in.AvgMs), in.Loss
		if in.InUse {
			it["inUse"] = "tcp"
		} else {
			it["sub"] = "客户端还没选定入口，取最快的一个"
		}
		if !in.OK {
			it["message"] = "利群专线入口连不上"
		}
		j.set(j.iHop1, it)
		if in.OK {
			j.mu.Lock()
			j.hop1Ms = in.AvgMs
			j.mu.Unlock()
		}
	}
	if p != nil && p.Total != nil && p.Total.RttMs > 0 && p.Total.RttMs <= lkDiagMaxClientRtt {
		j.mu.Lock()
		j.totalMs, j.totalSource, j.totalAgeS, j.totalLoss = p.Total.RttMs, p.Total.Source, p.Total.AgeS, p.Total.Loss
		if j.totalSource != "udp" && j.totalSource != "tcp" {
			j.totalSource = "tcp"
		}
		j.mu.Unlock()
	}
}

func lkMs(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(lkRound(v), 'f', 1, 64)
}

// lkLandingPeer mirrors the agent's LandingPeerRtt peers.
type lkLandingPeer struct {
	IP       string  `json:"ip"`
	Conns    int     `json:"conns"`
	RttMs    float64 `json:"rttMs"`
	MinRttMs float64 `json:"minRttMs"`
}

// lkDiagNodeIPs: every FLVX node address (IP literals): connections from
// them into the landing are 备线 / other forwards, not the 利群 relay exit.
func (j *lkDiagJob) nodeIPs() map[string]bool {
	out := map[string]bool{}
	hosts, err := j.h.repo.NodeGuardHosts()
	if err != nil {
		return out
	}
	for _, raw := range hosts {
		if a, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]")); err == nil {
			out[a.Unmap().String()] = true
		}
	}
	return out
}

// lkPickExit: the 利群 relay exit among the landing port's peers — public,
// not an FLVX node (备线 tunnel exits, other forwards); the relays sit next to
// the landing (TW / HK / JP …), so a mainland address (a direct client) only
// counts when nothing else is there; then the busiest. others = the rest.
func lkPickExit(peers []lkLandingPeer, skip map[string]bool) (lkLandingPeer, int, bool) {
	list := lkExitCands(peers, skip)
	if len(list) == 0 {
		return lkLandingPeer{}, 0, false
	}
	return list[0], len(list) - 1, true
}

// lkExitCands: every possible relay exit in lkPickExit's order.
func lkExitCands(peers []lkLandingPeer, skip map[string]bool) []lkLandingPeer {
	type cand struct {
		p  lkLandingPeer
		cn bool
	}
	list := []cand{}
	for _, p := range peers {
		a, err := netip.ParseAddr(p.IP)
		if err != nil {
			continue
		}
		a = a.Unmap()
		if skip[a.String()] || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
			continue
		}
		if p.RttMs <= 0 && p.MinRttMs <= 0 {
			continue
		}
		list = append(list, cand{p: p, cn: geoip.Lookup(a) == "CN"})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].cn != list[j].cn {
			return !list[i].cn
		}
		return list[i].p.Conns > list[j].p.Conns
	})
	out := make([]lkLandingPeer, len(list))
	for i, c := range list {
		out[i] = c.p
	}
	return out
}

// ---- plan 306: 端内 ----
//
// Measured 11-25 on all six 利群 lines (plan doc 306): the landing's kernel
// rtt of a relay-exit connection (C) is not the exit → landing network. On
// 广港 / 沪日 / 广台 the TCP is held by a vendor relay right behind the 专线入口
// and carried inside the vendor to the exit IP (a front), so C = 专线 + the
// network; on 福台 the exit is the relay itself (C ≈ ping). The landing's ping
// to the exit IP (n) is the network part; C − n (behind()) is part of the 专线
// — the vendor's own 全程 already ends at the exit front. So:
//   专线出口 → 落地 = n;  端内 (= the whole 专线入口 → 专线出口) = max(全程 −
//   入口段, C − n);  到落地 = 入口段 + 端内 + n  (= max(全程 + n, 入口段 + C),
//   matched the real round trip through the line within ≈ 3 ms; 全程 + C
//   counted the 专线 twice). Round 2 (user 11-25 night): 「端内 X ms」 for every
//   user and any value, never 估算 / ≈.

const (
	lkExitPings       = 3
	lkExitPingGap     = 150 * time.Millisecond
	lkExitMaxPinged   = 4   // sources pinged on a landing shared by several 利群 rules
	lkEndInSlackMs    = 5.0 // a source whose C − n is longer than the line's 端内 + this is another line's
	lkExitAgreeMs     = 3.0 // sources closer than this give one answer
	lkExitPingTimeout = 2   // seconds per echo
)

// lkExitCand: one relay-exit source of the landing port and the landing's
// ping to it.
type lkExitCand struct {
	lkLandingPeer
	PingMs float64 // landing → exit IP (ICMP min), -1 = no answer
}

func (c lkExitCand) connMs() float64 {
	if c.MinRttMs > 0 {
		return c.MinRttMs
	}
	return c.RttMs
}

// behind: the part of the connection rtt inside the vendor (behind the exit
// IP, C − n); a piece of the line's 端内, never added on top.
func (c lkExitCand) behind() float64 { return math.Max(0, c.connMs()-c.PingMs) }

// pingExit: ICMP from the landing node to an exit IP through the agents'
// existing ServiceMonitorCheck (no OTA), lkExitPings echoes one at a time;
// the min. The agent reports whole ms (floored) → +0.5.
func (j *lkDiagJob) pingExit(ctx context.Context, ip string) float64 {
	best := -1.0
	for i := 0; i < lkExitPings; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return best
			case <-time.After(lkExitPingGap):
			}
		}
		res, err := j.h.sendNodeCommandWithTimeout(j.exitNode, "ServiceMonitorCheck",
			map[string]interface{}{"monitorId": 0, "type": "icmp", "target": ip, "timeoutSec": lkExitPingTimeout},
			time.Duration(lkExitPingTimeout+3)*time.Second, false, false)
		if err != nil || !res.Success || res.Data == nil {
			if err != nil && lkUnknownCommand(err.Error()) || lkUnknownCommand(res.Message) {
				return best
			}
			continue
		}
		ok, _ := res.Data["success"].(bool)
		v, _ := res.Data["latencyMs"].(float64)
		if !ok || v < 0 || v > 2000 {
			continue
		}
		if v == math.Trunc(v) {
			v += 0.5
		}
		if best < 0 || v < best {
			best = v
		}
	}
	return best
}

// lkExitPick: what compose() shows for the landing hop.
type lkExitPick struct {
	sel      []lkExitCand // one = unambiguous; several = a range (shared landing)
	dropped  int          // sources dropped on a shared landing (C − n longer than this line's 端内)
	noPing   bool         // the exit did not answer ping → today's numbers (C as 出口 → 落地)
	noneFits bool         // shared landing: no source looks like this line's
	alone    bool         // shared landing with one source only: it may be the other line's
}

// lkPickExitFor chooses among the pinged sources. est = 全程 − 入口段 (< 0
// unknown). Not shared: the busiest. Shared: sources whose C − n fits in this
// line's 端内; one (or all within lkExitAgreeMs) → that, else all of them.
func lkPickExitFor(cands []lkExitCand, shared bool, est float64) lkExitPick {
	if len(cands) == 0 {
		return lkExitPick{}
	}
	if !shared {
		if cands[0].PingMs < 0 {
			return lkExitPick{sel: cands[:1], noPing: true}
		}
		return lkExitPick{sel: cands[:1]}
	}
	fit, answered := []lkExitCand{}, 0
	for _, c := range cands {
		if c.PingMs < 0 {
			continue
		}
		answered++
		if est >= 0 && c.behind() > est+lkEndInSlackMs {
			continue
		}
		fit = append(fit, c)
	}
	switch {
	case answered == 0:
		return lkExitPick{sel: cands[:1], noPing: true}
	case len(fit) == 0:
		return lkExitPick{noneFits: true, dropped: answered}
	}
	minN, maxN, minX, maxX := fit[0].PingMs, fit[0].PingMs, fit[0].behind(), fit[0].behind()
	for _, c := range fit[1:] {
		minN, maxN = math.Min(minN, c.PingMs), math.Max(maxN, c.PingMs)
		minX, maxX = math.Min(minX, c.behind()), math.Max(maxX, c.behind())
	}
	p := lkExitPick{sel: fit, dropped: answered - len(fit), alone: answered == 1}
	if maxN-minN <= lkExitAgreeMs && maxX-minX <= lkExitAgreeMs {
		p.sel = fit[:1]
	}
	return p
}

// lkSpan: [lo, hi] of a value over the picked sources.
type lkSpan struct{ lo, hi float64 }

func lkSpanOf(sel []lkExitCand, f func(lkExitCand) float64) lkSpan {
	s := lkSpan{f(sel[0]), f(sel[0])}
	for _, c := range sel[1:] {
		v := f(c)
		s.lo, s.hi = math.Min(s.lo, v), math.Max(s.hi, v)
	}
	return s
}

// put writes lo (and hi when it shows as a different whole ms) into m.
func (s lkSpan) put(m map[string]interface{}, key, maxKey string) {
	m[key] = lkRound(s.lo)
	if math.Round(s.hi) > math.Round(s.lo) {
		m[maxKey] = lkRound(s.hi)
	}
}

// lkTagMs: 「0.4」 / 「4.7」 under 10 ms (one decimal), 「19」 above.
func lkTagMs(v float64) string {
	if v < 9.95 {
		return lkMs(lkRound(v))
	}
	return strconv.Itoa(int(math.Round(v)))
}

// tagText: a range only when put() writes one (whole ms apart).
func (s lkSpan) tagText() string {
	if math.Round(s.hi) > math.Round(s.lo) {
		return lkTagMs(s.lo) + "–" + lkTagMs(s.hi)
	}
	return lkTagMs(s.lo)
}

// endInTag: 「端内 X ms」 on the 专线入口 → 专线出口 row and the path strip —
// for every user, whatever the value (round 2: no 估算 / ≈ / hiding).
func endInTag(x lkSpan) map[string]interface{} {
	return map[string]interface{}{"label": "端内 " + x.tagText() + " ms", "tone": "amber",
		"title": "端内 = 利群专线入口 → 专线出口（全程减入口段）"}
}

// exitLabel: 「Zenlayer · 台湾 · 103.167.50.69」 (admin only).
func lkExitLabel(ip string) string {
	return strings.Trim(lkIngressLabel(ip)+" · "+ip, " ·")
}

// runLanding: 专线出口 → 落地 on the native landing (kernel TCP rtt).
func (j *lkDiagJob) runLanding(ctx context.Context) {
	if j.iLanding < 0 {
		return
	}
	fail := func(why string) {
		j.mu.Lock()
		j.landingWhy = why
		j.mu.Unlock()
	}
	h := j.h
	if h.wsServer != nil && !h.wsServer.NodeOnline(j.exitNode) && h.nodeCommandSender == nil && leiKwanLabCommand == nil {
		fail("落地机离线，这一段没法实测")
		return
	}
	res, err := h.sendNodeCommandWithTimeout(j.exitNode, "LandingPeerRtt", map[string]interface{}{"port": j.landingPort, "max": 16}, 12*time.Second, false, false)
	msg := ""
	if err != nil {
		msg = err.Error()
	} else if !res.Success {
		msg = res.Message
	}
	if msg != "" {
		if lkUnknownCommand(msg) {
			fail("落地机 agent 还没升级，这一段先用估算")
		} else {
			fail("落地机没测到：" + j.redact(msg))
		}
		return
	}
	raw, _ := json.Marshal(res.Data)
	var d struct {
		V     int             `json:"v"`
		Peers []lkLandingPeer `json:"peers"`
		Total int             `json:"total"`
	}
	if json.Unmarshal(raw, &d) != nil || d.V != 1 {
		fail("落地机 agent 还没升级，这一段先用估算")
		return
	}
	list := lkExitCands(d.Peers, j.nodeIPs())
	if len(list) == 0 {
		fail("落地机上现在没有专线出口的连接，这一段用估算")
		return
	}
	// Plan 306: ping the exit (the busiest source; on a landing shared by
	// several 利群 rules up to lkExitMaxPinged sources), one echo at a time
	// per source. compose() splits 端内 off once 全程 / 入口段 are in.
	n := 1
	if len(j.sharedWith) > 0 {
		n = min(len(list), lkExitMaxPinged)
	}
	cands := make([]lkExitCand, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		cands[i] = lkExitCand{lkLandingPeer: list[i], PingMs: -1}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cands[i].PingMs = j.pingExit(ctx, list[i].IP)
		}(i)
	}
	wg.Wait()
	j.mu.Lock()
	j.exits, j.exitOthers = cands, len(list)-n
	j.landingMs = cands[0].connMs()
	j.mu.Unlock()
	if len(j.sharedWith) == 0 {
		// Final already (the pick needs 全程 only on a shared landing).
		j.landingRows(lkPickExitFor(cands, false, -1), len(list)-n)
	}
}

// shareNote: 「这台落地机还接着「…」」 (admins see the other rules' names).
func (j *lkDiagJob) shareNote() string {
	if len(j.sharedWith) == 0 {
		return ""
	}
	if j.admin {
		return "这台落地机还接着「" + strings.Join(j.sharedWith, "」「") + "」"
	}
	return "这台落地机还接了别的利群线路"
}

func (j *lkDiagJob) noneFitsWhy() string {
	return j.shareNote() + "，现在的来源都不像这条线的出口，这一段用估算"
}

// landingRows writes the 专线出口 → 落地 row(s) for the pick (compose()).
func (j *lkDiagJob) landingRows(p lkExitPick, others int) {
	title := "专线出口 → " + j.landingTitle()
	shareNote := j.shareNote
	if p.noneFits {
		it := lkDiagRow("landing", title, "")
		it["skipped"], it["message"] = true, j.noneFitsWhy()
		j.set(j.iLanding, it)
		return
	}
	c := p.sel[0]
	if p.noPing {
		// Today's numbers: the connection rtt (it may hold part of the 端内).
		sub := "落地机实测（连接延迟；出口不回 ping，可能偏高）"
		if j.admin {
			sub = fmt.Sprintf("利群出口 %s · %d 条连接 · 平滑 %s ms · 出口不回 ping，用的是连接延迟", lkExitLabel(c.IP), c.Conns, lkMs(lkRound(c.RttMs)))
			if others > 0 {
				sub += fmt.Sprintf(" · 另有 %d 个来源", others)
			}
		}
		it := lkDiagRow("landing", title, sub)
		it["success"], it["averageTime"], it["packetLoss"] = true, lkRound(c.connMs()), 0.0
		j.set(j.iLanding, it)
		return
	}
	srcLine := func(c lkExitCand) string {
		return fmt.Sprintf("%s · ping %s ms · 连接 %s ms · %d 条连接", lkExitLabel(c.IP),
			lkMs(lkRound(c.PingMs)), lkMs(lkRound(c.connMs())), c.Conns)
	}
	if len(p.sel) == 1 {
		sub := "落地机实测（ping 专线出口）"
		if p.alone {
			sub = "落地机 ping 专线出口；" + shareNote() + "，不一定是这条线的出口"
		}
		if j.admin {
			sub = "利群出口 " + srcLine(c)
			switch {
			case p.alone:
				sub += " · " + shareNote() + "，现在只有这一个来源，不一定是这条线的出口"
			case p.dropped > 0:
				sub += fmt.Sprintf(" · %s，排除了 %d 个别的线路的来源", shareNote(), p.dropped)
			case len(j.sharedWith) > 0:
				sub += " · " + shareNote() + "，其余来源和这个差不多"
			}
			if others > 0 {
				sub += fmt.Sprintf(" · 另有 %d 个来源没测", others)
			}
		}
		it := lkDiagRow("landing", title, sub)
		it["success"], it["averageTime"], it["packetLoss"] = true, lkRound(c.PingMs), 0.0
		j.set(j.iLanding, it)
		return
	}
	// Shared landing, several plausible sources: show the range honestly.
	n := lkSpanOf(p.sel, func(c lkExitCand) float64 { return c.PingMs })
	sub := shareNote() + "，分不清是哪个出口，显示范围"
	it := lkDiagRow("landing", title, sub)
	it["success"], it["packetLoss"], it["ambiguous"] = true, 0.0, true
	n.put(it, "averageTime", "averageMaxMs")
	j.set(j.iLanding, it)
	if !j.admin {
		return
	}
	for i, c := range p.sel {
		r := lkDiagRow("landing", "专线出口 "+lkCircled(i)+" → "+j.landingTitle(), srcLine(c))
		r["success"], r["averageTime"], r["packetLoss"] = true, lkRound(c.PingMs), 0.0
		j.set(-1, r)
	}
}

// compose fills the estimated hop and the summary once the measured parts
// are in (called after every part finished or the run timed out).
func (j *lkDiagJob) compose() {
	j.mu.Lock()
	hop1, total, land := j.hop1Ms, j.totalMs, j.landingMs
	sum := map[string]interface{}{"hop2Estimate": true, "totalSource": "", "local": j.local}
	if hop1 >= 0 {
		sum["hop1Ms"] = lkRound(hop1)
	}
	if total >= 0 {
		sum["totalMs"], sum["totalSource"], sum["totalAgeS"] = lkRound(total), j.totalSource, j.totalAgeS
		if j.totalLoss != nil {
			sum["totalLoss"] = *j.totalLoss
		}
	}
	if j.iDevice >= 0 && j.phone != nil && j.phone.Device != nil && j.phone.Device.OK {
		sum["deviceMs"] = lkRound(j.phone.Device.AvgMs)
	}
	landingWhy, hop1Why := j.landingWhy, j.hop1Why
	exits, exitOthers := j.exits, j.exitOthers
	j.mu.Unlock()
	noTotal := "利群客户端还没有全程数据，估算不了"
	if hop1Why == "" {
		hop1Why = "第一跳没测到"
	}
	est := -1.0
	if total >= 0 && hop1 >= 0 {
		est = math.Max(0, total-hop1)
	}
	// Plan 306: which source(s) are this line's exit and their ping.
	var pick lkExitPick
	if land >= 0 && len(exits) > 0 {
		pick = lkPickExitFor(exits, len(j.sharedWith) > 0, est)
		j.landingRows(pick, exitOthers)
		if pick.noneFits {
			land = -1 // like no exit connection: the old 专线 → 落地 estimate
			landingWhy = j.noneFitsWhy()
		}
	}
	if land >= 0 {
		sum["landingMeasured"] = true
		// Round 2: this row is 「端内」 — a plain number, not 估算.
		it := lkDiagRow("vendor", "利群专线入口 → 专线出口", "")
		// The parts per picked source: n = 出口 → 落地, x = C − n; the old
		// numbers (n = connection rtt, x = 0) when the exit does not answer
		// ping or there is no source list.
		nOf := func(c lkExitCand) float64 { return c.PingMs }
		xOf := func(c lkExitCand) float64 { return c.behind() }
		sel := pick.sel
		if len(sel) == 0 || pick.noPing {
			c := lkExitCand{lkLandingPeer: lkLandingPeer{MinRttMs: land}, PingMs: land}
			if len(sel) > 0 {
				c = sel[0]
			}
			sel = []lkExitCand{c}
			nOf = func(c lkExitCand) float64 { return c.connMs() }
			xOf = func(lkExitCand) float64 { return 0 }
		} else {
			sum["landingPing"] = true // 出口 → 落地 = the landing's ping to the exit
		}
		lkSpanOf(sel, nOf).put(sum, "landingMs", "landingMaxMs")
		if len(sel) > 1 {
			sum["landingAmbiguous"] = true
		}
		switch {
		case total < 0:
			it["skipped"], it["message"] = true, "利群客户端还没有全程数据，算不出端内"
		case hop1 < 0:
			it["skipped"], it["message"] = true, hop1Why+"，算不出端内"
		default:
			// Prod 11-24 (fork.165): the client's 全程 ends at the line's exit
			// front — its probes never reach the landing. Plan 306 (11-25, real
			// round trips through all six lines): the landing's connection rtt
			// also holds the 专线 (C − n), so 端内 = max(全程 − 入口段, C − n)
			// and 到落地 = 入口段 + 端内 + 出口 → 落地 (not 全程 + C).
			endIn := lkSpanOf(sel, func(c lkExitCand) float64 { return math.Max(est, xOf(c)) })
			e2e := lkSpanOf(sel, func(c lkExitCand) float64 { return hop1 + math.Max(est, xOf(c)) + nOf(c) })
			endIn.put(sum, "vendorMs", "vendorMaxMs")
			endIn.put(sum, "endInMs", "endInMaxMs")
			e2e.put(sum, "endToEndMs", "endToEndMaxMs")
			sum["hop2Estimate"] = false
			it["success"], it["packetLoss"] = true, 0.0
			endIn.put(it, "averageTime", "averageMaxMs")
			it["sub"] = fmt.Sprintf("全程 %s ms − 入口段 %s ms", lkMs(lkRound(total)), lkMs(lkRound(hop1)))
			if endIn.hi > est+0.5 {
				it["sub"] = fmt.Sprintf("全程 %s ms − 入口段 %s ms = %s ms，落地机测到这段更长，按 %s ms 算",
					lkMs(lkRound(total)), lkMs(lkRound(hop1)), lkMs(lkRound(est)), endIn.tagText())
			}
			tag := endInTag(endIn)
			it["endIn"], sum["endIn"] = tag, tag
		}
		j.set(j.iVendor, it)
	} else {
		it := lkDiagRow("hop2", "利群专线 → "+j.landingTitle(), "")
		it["estimate"] = true
		switch {
		case total < 0:
			it["skipped"], it["estimate"], it["message"] = true, false, noTotal
		case hop1 < 0:
			it["skipped"], it["estimate"], it["message"] = true, false, hop1Why+"，估算不了"
		default:
			est := math.Max(0, total-hop1)
			sum["hop2Ms"] = lkRound(est)
			it["success"], it["averageTime"], it["packetLoss"] = true, lkRound(est), 0.0
			it["sub"] = fmt.Sprintf("估算：全程 %s ms − 第一跳 %s ms", lkMs(lkRound(total)), lkMs(lkRound(hop1)))
		}
		j.set(j.iVendor, it)
		if j.iLanding >= 0 {
			why := landingWhy
			if why == "" {
				why = "这一段没测到，用估算"
			}
			lr := lkDiagRow("landing", "专线出口 → "+j.landingTitle(), "")
			lr["skipped"], lr["message"] = true, why
			j.set(j.iLanding, lr)
		}
	}
	j.mu.Lock()
	j.summary = sum
	j.mu.Unlock()
}

func (j *lkDiagJob) runExit(ctx context.Context, idx int) {
	it := lkDiagRow("exit", j.landingTitle()+" → 外网", "落地机实测")
	if j.exitNode <= 0 {
		it["skipped"], it["message"] = true, j.exitSkip
		j.set(idx, it)
		return
	}
	results := []map[string]interface{}{}
	j.h.appendExitTestRotation(&results, j.exitNode, j.landingTitle()+" → 外网", map[string]interface{}{"exitTest": true},
		diagnosisExecOptions{commandTimeout: exitTestCommandTimeout, pingTimeoutMS: 3000, pingCount: exitTestPingCount})
	if len(results) == 0 {
		it["message"] = "出口测试没有结果"
		j.set(idx, it)
		return
	}
	r := results[0]
	target := asString(r["targetIp"])
	if a := asString(r["actualTarget"]); a != "" {
		target = a
	}
	if target != "" {
		it["title"] = j.landingTitle() + " → " + target + ":443"
	}
	it["success"] = asBool(r["success"], false)
	it["averageTime"], it["packetLoss"] = lkRound(asFloat(r["averageTime"], 0)), asFloat(r["packetLoss"], 100)
	if it["success"] != true {
		it["message"] = j.redact(asString(r["message"]))
	}
	j.set(idx, it)
}

// backupTitle: admins see the 备线 tunnel's name; users only 「备线（FLVX 隧道）」
// (plan 276, F09).
func (j *lkDiagJob) backupTitle(tunnelName string) string {
	if !j.admin {
		return j.entryTitle() + " → 备线（FLVX 隧道） → " + j.landingTitle()
	}
	if tunnelName != "" {
		return j.entryTitle() + " → " + tunnelName + " → " + j.landingTitle()
	}
	return j.entryTitle() + " → 备线 → " + j.landingTitle()
}

func (j *lkDiagJob) redact(s string) string {
	if j.admin {
		return s
	}
	return diagnosisAddrPattern.ReplaceAllString(s, "***")
}

func (j *lkDiagJob) runBackup(ctx context.Context, idx int) {
	h := j.h
	it := lkDiagRow("backup", j.backupTitle(""), "")
	if !j.admin {
		it["sub"] = "备线（FLVX 隧道）"
	}
	fwd, err := h.getForwardRecord(j.rule.BackupForwardID)
	if err != nil || fwd == nil {
		it["message"] = "备线转发不存在"
		j.set(idx, it)
		return
	}
	tunnelName, entryHost, entryPort := "", "", 0
	if views, err := h.repo.LeiKwanBackupForwards([]int64{j.rule.BackupForwardID}); err == nil {
		v := views[j.rule.BackupForwardID]
		tunnelName, entryHost, entryPort = v.TunnelName, v.EntryHost, v.EntryPort
	}
	if tunnelName != "" && j.admin {
		it["title"] = j.backupTitle(tunnelName)
		it["sub"] = "FLVX 隧道「" + tunnelName + "」"
	}
	_, work, err := h.prepareForwardDiagnosis(fwd)
	if err != nil || len(work) == 0 {
		it["message"] = "备线诊断准备失败"
		if err != nil {
			it["message"] = j.redact(err.Error())
		}
		j.set(idx, it)
		return
	}
	results := h.runDiagnosisWorkItems(ctx, work, nil)
	ok, sum, loss := true, 0.0, 0.0
	msg := ""
	// The 备线 starts at the chosen entry: the 主入口 (logswitch / phone DNAT)
	// → the 备线 forward's entry port; 手机本地: the phone dials it itself.
	switch {
	case j.local:
		first := map[string]interface{}{"description": "手机 → 备线入口", "success": false, "averageTime": 0.0, "packetLoss": 100.0, "message": "App 没有测到"}
		if j.phone != nil && j.phone.Backup != nil {
			b := j.phone.Backup
			first["success"], first["averageTime"], first["packetLoss"] = b.OK, b.AvgMs, b.Loss
			if !b.OK {
				first["message"] = "手机连不上备线入口"
			} else {
				delete(first, "message")
			}
		}
		results = append([]map[string]interface{}{first}, results...)
	case entryHost != "" && entryPort > 0:
		first := []map[string]interface{}{}
		h.appendPathDiagnosis(&first, map[int64]*nodeRecord{}, j.entry.NodeID, entryHost, entryPort, j.entry.Name+" → 备线入口",
			map[string]interface{}{}, diagnosisExecOptions{commandTimeout: 12 * time.Second, pingTimeoutMS: 2000, pingCount: 3})
		if len(first) > 0 {
			first[0]["description"] = j.entry.Name + " → 备线入口"
			results = append(first, results...)
		}
	}
	for _, r := range results {
		if r == nil || asBool(r["exitTest"], false) {
			continue
		}
		if !asBool(r["success"], false) {
			ok = false
			if msg == "" {
				msg = strings.TrimSpace(asString(r["description"]) + " " + asString(r["message"]))
				if !j.admin {
					// Plan 276 (F09): hop descriptions name internal nodes / tunnels.
					msg = strings.TrimSpace("备线有一段不通 " + asString(r["message"]))
				}
			}
			continue
		}
		sum += asFloat(r["averageTime"], 0)
		loss = math.Max(loss, asFloat(r["packetLoss"], 0))
	}
	it["success"] = ok
	if ok {
		it["averageTime"], it["packetLoss"] = lkRound(sum), loss
	} else {
		it["message"] = j.redact(msg)
	}
	j.set(idx, it)
}

// run executes every part (in parallel) and returns the final rows + summary.
func (j *lkDiagJob) run(ctx context.Context) ([]lkDiagItem, map[string]interface{}, bool) {
	var wg sync.WaitGroup
	j.runDevice()
	wg.Add(1)
	go func() { defer wg.Done(); j.runHop1(ctx) }()
	if j.iLanding >= 0 {
		wg.Add(1)
		go func() { defer wg.Done(); j.runLanding(ctx) }()
	}
	if j.iExit >= 0 {
		wg.Add(1)
		go func() { defer wg.Done(); j.runExit(ctx, j.iExit) }()
	}
	if j.iBackup >= 0 {
		wg.Add(1)
		go func() { defer wg.Done(); j.runBackup(ctx, j.iBackup) }()
	}
	// The estimate needs 第一跳 / 全程 / the landing hop: composed once every
	// part finished (or at the timeout).
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	timedOut := false
	select {
	case <-done:
	case <-ctx.Done():
		timedOut = true
	}
	j.compose()
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, it := range j.items {
		if it["diagnosing"] == true {
			delete(it, "diagnosing")
			it["message"] = "诊断超时"
		}
	}
	items := make([]lkDiagItem, len(j.items))
	copy(items, j.items)
	// Display order: sections, then their own order.
	order := map[string]int{"device": 0, "hop1": 1, "vendor": 2, "landing": 3, "hop2": 4, "exit": 5, "backup": 6}
	sort.SliceStable(items, func(a, b int) bool {
		return order[fmt.Sprint(items[a]["section"])] < order[fmt.Sprint(items[b]["section"])]
	})
	sum := j.summary
	if sum == nil {
		sum = map[string]interface{}{"hop2Estimate": true, "totalSource": ""}
	}
	return items, sum, timedOut
}

// leikwanRuleDiagnoseStream: NDJSON like /forward/diagnose/stream.
func (h *Handler) leikwanRuleDiagnoseStream(w http.ResponseWriter, r *http.Request) {
	j, err := h.leiKwanDiagPrepare(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	flusher, err := prepareDiagnosisStreamResponse(w)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lkDiagTotalTimeout)
	defer cancel()
	enc := json.NewEncoder(w)
	var wmu sync.Mutex
	closed := false // a probe finishing after the timeout must not write any more
	write := func(t string, data interface{}) {
		wmu.Lock()
		defer wmu.Unlock()
		if closed {
			return
		}
		if err := writeDiagnosisStreamEvent(enc, flusher, t, data); err != nil {
			cancel()
		}
	}
	write("start", j.startPayload())
	j.emit = func(index int, it lkDiagItem) {
		write("item", map[string]interface{}{"index": index, "result": it, "progress": j.progress()})
	}
	items, sum, timedOut := j.run(ctx)
	j.mu.Lock()
	j.emit = nil
	j.mu.Unlock()
	write("done", map[string]interface{}{"progress": j.progress(), "timedOut": timedOut, "summary": sum, "items": items})
	wmu.Lock()
	closed = true
	wmu.Unlock()
}

// leikwanRuleDiagnose: the non-stream fallback.
func (h *Handler) leikwanRuleDiagnose(w http.ResponseWriter, r *http.Request) {
	j, err := h.leiKwanDiagPrepare(r)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lkDiagTotalTimeout)
	defer cancel()
	items, sum, timedOut := j.run(ctx)
	response.WriteJSON(w, response.OK(map[string]interface{}{"ruleName": j.rule.Name, "admin": j.admin, "path": j.path, "items": items, "summary": sum, "timedOut": timedOut}))
}
