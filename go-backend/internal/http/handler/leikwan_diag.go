package handler

// Plan 272 利群规则「诊断」: 入口机 → 利群专线 → 落地, one-shot on click.
//   - 第一跳: the owner's 主入口 agent times a TCP handshake to each relay
//     ingress the LeiKwan client was offered (agent command LeiKwanDiag) and
//     reads which one the client uses. Users see one row (their 主入口 → 利群
//     专线, no relay IPs); admins every ingress plus the other 利群入口 hosts.
//   - 全程: the LeiKwan client's own rtt on the 主入口 (status file).
//   - 第二跳: 全程 − 第一跳, always labelled 估算 — the landing cannot see the
//     line's exit, so there is no honest measurement of it.
//   - 出口测试: panel landing on an FLVX node → the existing TcpPing rotation.
//   - 备线: the existing rule diagnosis of the 备线 forward, as one row.
// No new pollers; ≤ 5 handshakes per ingress; per-command and total timeouts;
// one diagnose per user per 10 s.

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
)

const (
	lkDiagTotalTimeout = 40 * time.Second
	lkDiagCmdTimeout   = 25 * time.Second
	lkDiagCount        = 3
	lkDiagPingMs       = 1500
	lkDiagGap          = 10 * time.Second
)

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

// ---- running ----

// lkDiagJob: one diagnose run of a rule.
type lkDiagJob struct {
	h        *Handler
	admin    bool
	rule     model.LeiKwanRule
	line     model.LeiKwanLine
	entry    *lkEntry
	others   []leiKwanEntryNode
	landing  string // landing display name
	path     map[string]interface{}
	exitNode int64  // FLVX node of a native landing server (0 = no exit test)
	exitSkip string // why there is no exit test (panel landing only)

	mu      sync.Mutex
	items   []lkDiagItem
	summary map[string]interface{}
	emit    func(index int, item lkDiagItem)
}

func (h *Handler) leiKwanDiagPrepare(r *http.Request) (*lkDiagJob, error) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		return nil, err
	}
	x, admin, err := h.leiKwanRuleAccess(r, req.ID)
	if err != nil {
		return nil, err
	}
	uid, _, _ := userRoleFromRequest(r)
	if !h.lkThrottle.allow("diag:"+strconv.FormatInt(uid, 10), time.Now(), lkDiagGap) {
		return nil, errors.New("诊断太频繁，请 10 秒后再试")
	}
	j := &lkDiagJob{h: h, admin: admin, rule: *x}
	if l, err := h.repo.GetLeiKwanLine(x.LineID); err == nil && l != nil {
		j.line = *l
	}
	j.entry = h.leiKwanUserEntry(x.UserID)
	if j.entry == nil {
		return nil, errors.New("规则主人还没有主入口，诊断不了")
	}
	if setting, _ := h.repo.GetLeiKwanSetting(); setting != nil && admin {
		for _, e := range h.leiKwanEntryNodes(setting.EntryGroupID) {
			if e.ID != j.entry.NodeID {
				j.others = append(j.others, e)
			}
		}
	}
	path := map[string]interface{}{"entryName": j.entry.Name, "entryAddress": net.JoinHostPort(j.entry.Host, strconv.Itoa(x.ListenPort)),
		"lineName": j.line.Name, "routeLabel": j.line.RouteLabel}
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
				} else {
					j.exitSkip = "共享落地机上不测出口（妙妙屋那边的机器）"
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
	j.path = path
	// Placeholders: 0 = 第一跳, 1 = 第二跳, then 出口 / 备线 / other hosts.
	j.items = append(j.items, lkDiagRow("hop1", j.entry.Name+" → 利群专线", ""), lkDiagRow("hop2", "利群专线 → "+j.landingTitle(), ""))
	if x.LandingNodeID > 0 {
		j.items = append(j.items, lkDiagRow("exit", j.landingTitle()+" → 外网", ""))
	}
	if x.BackupForwardID > 0 {
		j.items = append(j.items, lkDiagRow("backup", j.entry.Name+" → 备线 → "+j.landingTitle(), ""))
	}
	for _, e := range j.others {
		j.items = append(j.items, lkDiagRow("other", e.Name+" → 利群专线", ""))
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
	if h.wsServer != nil && !h.wsServer.NodeOnline(nodeID) && h.nodeCommandSender == nil {
		return nil, "入口机不在线"
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
		low := strings.ToLower(msg)
		if strings.Contains(low, "unknown") || strings.Contains(msg, "未知") || strings.Contains(low, "not support") || strings.Contains(msg, "不支持") {
			return nil, "入口机 agent 需要升级才能诊断"
		}
		if strings.Contains(msg, "超时") || strings.Contains(low, "timeout") {
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

// pick: the ingress the client uses (TCP first), else the fastest reachable.
func lkDiagPick(d *lkAgentDiag) int {
	best := -1
	for i, in := range d.Ingresses {
		if strings.Contains(in.InUse, "tcp") {
			return i
		}
	}
	for i, in := range d.Ingresses {
		if in.InUse != "" {
			return i
		}
	}
	for i, in := range d.Ingresses {
		if in.OK && (best < 0 || in.AvgMs < d.Ingresses[best].AvgMs) {
			best = i
		}
	}
	return best
}

func lkRound(v float64) float64 { return math.Round(v*10) / 10 }

func (j *lkDiagJob) runHop1(ctx context.Context) {
	d, errMsg := j.agentDiag(j.entry.NodeID)
	sum := map[string]interface{}{"hop2Estimate": true, "totalSource": ""}
	defer func() {
		j.mu.Lock()
		j.summary = sum
		j.mu.Unlock()
	}()
	if d == nil {
		it := lkDiagRow("hop1", j.entry.Name+" → 利群专线", "")
		it["message"] = errMsg
		j.set(0, it)
		hop2 := lkDiagRow("hop2", "利群专线 → "+j.landingTitle(), "")
		hop2["skipped"], hop2["message"] = true, "第一跳没测到，第二跳估算不了"
		j.set(1, hop2)
		return
	}
	pick := lkDiagPick(d)
	var hop1 float64 = -1
	if pick >= 0 && d.Ingresses[pick].OK {
		hop1 = d.Ingresses[pick].AvgMs
		sum["hop1Ms"] = lkRound(hop1)
	}
	if j.admin {
		if len(d.Ingresses) == 0 {
			it := lkDiagRow("hop1", j.entry.Name+" → 利群专线", "")
			it["message"] = "利群客户端现在没有候选入口（转发状态：" + d.State + "）"
			j.set(0, it)
		}
		for i, in := range d.Ingresses {
			sub := strings.TrimSpace(strings.Trim(lkIngressLabel(in.Endpoint)+" · "+in.Endpoint, " ·"))
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
				idx = 0
			}
			j.set(idx, it)
		}
	} else {
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
		j.set(0, it)
	}
	// 全程 = the client's own measurement (UDP probe preferred, it has loss).
	var total float64 = -1
	switch {
	case d.UDP != nil && d.UDP.RttMs > 0:
		total = d.UDP.RttMs
		sum["totalSource"], sum["totalAgeS"] = "udp", d.UDP.AgeS
		if d.UDP.Sent > 0 {
			sum["totalLoss"] = lkRound(float64(d.UDP.Sent-d.UDP.Recv) * 100 / float64(d.UDP.Sent))
		}
	case d.TCP != nil && d.TCP.RttMs > 0:
		total = d.TCP.RttMs
		sum["totalSource"], sum["totalAgeS"] = "tcp", d.TCP.AgeS
	}
	hop2 := lkDiagRow("hop2", "利群专线 → "+j.landingTitle(), "")
	hop2["estimate"] = true
	switch {
	case total < 0:
		hop2["skipped"], hop2["estimate"], hop2["message"] = true, false, "利群客户端还没有全程数据，第二跳估算不了"
	case hop1 < 0:
		hop2["skipped"], hop2["estimate"], hop2["message"] = true, false, "第一跳没测到，第二跳估算不了"
	default:
		sum["totalMs"] = lkRound(total)
		est := total - hop1
		if est < 0 {
			est = 0
		}
		sum["hop2Ms"] = lkRound(est)
		hop2["success"], hop2["averageTime"], hop2["packetLoss"] = true, lkRound(est), 0.0
		hop2["sub"] = fmt.Sprintf("估算：全程 %s ms − 第一跳 %s ms", lkMs(total), lkMs(hop1))
	}
	if total >= 0 {
		sum["totalMs"] = lkRound(total)
	}
	j.set(1, hop2)
}

func lkMs(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(lkRound(v), 'f', 1, 64)
}

func (j *lkDiagJob) runOther(ctx context.Context, idx int, e leiKwanEntryNode) {
	d, errMsg := j.agentDiag(e.ID)
	it := lkDiagRow("other", e.Name+" → 利群专线", "")
	if d == nil {
		it["message"] = errMsg
		j.set(idx, it)
		return
	}
	best := -1
	for i, in := range d.Ingresses {
		if in.OK && (best < 0 || in.AvgMs < d.Ingresses[best].AvgMs) {
			best = i
		}
	}
	if best < 0 {
		it["message"] = "这台入口机连不上任何利群入口"
		if len(d.Ingresses) == 0 {
			it["message"] = "利群客户端现在没有候选入口"
		}
		j.set(idx, it)
		return
	}
	in := d.Ingresses[best]
	it["title"] = e.Name + " → 利群入口 " + lkCircled(best)
	sub := "最快的入口 · " + strings.Trim(lkIngressLabel(in.Endpoint)+" · "+in.Endpoint, " ·")
	if d.UDP != nil && d.UDP.RttMs > 0 {
		sub += fmt.Sprintf(" · 客户端全程 %s ms", lkMs(d.UDP.RttMs))
	} else if d.TCP != nil && d.TCP.RttMs > 0 {
		sub += fmt.Sprintf(" · 客户端全程 %s ms", lkMs(d.TCP.RttMs))
	}
	it["sub"], it["inUse"], it["success"], it["averageTime"], it["packetLoss"] = sub, in.InUse, true, lkRound(in.AvgMs), in.Loss
	j.set(idx, it)
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

func (j *lkDiagJob) redact(s string) string {
	if j.admin {
		return s
	}
	return diagnosisAddrPattern.ReplaceAllString(s, "***")
}

func (j *lkDiagJob) runBackup(ctx context.Context, idx int) {
	h := j.h
	it := lkDiagRow("backup", j.entry.Name+" → 备线 → "+j.landingTitle(), "")
	fwd, err := h.getForwardRecord(j.rule.BackupForwardID)
	if err != nil || fwd == nil {
		it["message"] = "备线规则不存在"
		j.set(idx, it)
		return
	}
	tunnelName := ""
	if views, err := h.repo.LeiKwanBackupForwards([]int64{j.rule.BackupForwardID}); err == nil {
		tunnelName = views[j.rule.BackupForwardID].TunnelName
	}
	if tunnelName != "" {
		it["title"] = j.entry.Name + " → " + tunnelName + " → " + j.landingTitle()
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
	for _, r := range results {
		if r == nil || asBool(r["exitTest"], false) {
			continue
		}
		if !asBool(r["success"], false) {
			ok = false
			if msg == "" {
				msg = strings.TrimSpace(asString(r["description"]) + " " + asString(r["message"]))
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
	idx := 2
	exitIdx, backupIdx := -1, -1
	if j.rule.LandingNodeID > 0 {
		exitIdx = idx
		idx++
	}
	if j.rule.BackupForwardID > 0 {
		backupIdx = idx
		idx++
	}
	otherIdx := idx
	wg.Add(1)
	go func() { defer wg.Done(); j.runHop1(ctx) }()
	if exitIdx >= 0 {
		wg.Add(1)
		go func() { defer wg.Done(); j.runExit(ctx, exitIdx) }()
	}
	if backupIdx >= 0 {
		wg.Add(1)
		go func() { defer wg.Done(); j.runBackup(ctx, backupIdx) }()
	}
	for i, e := range j.others {
		wg.Add(1)
		go func(i int, e leiKwanEntryNode) { defer wg.Done(); j.runOther(ctx, otherIdx+i, e) }(i, e)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	timedOut := false
	select {
	case <-done:
	case <-ctx.Done():
		timedOut = true
	}
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
	order := map[string]int{"hop1": 0, "hop2": 1, "exit": 2, "backup": 3, "other": 4}
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
