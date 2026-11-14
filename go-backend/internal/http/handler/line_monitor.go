package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go-backend/internal/http/response"
	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 255 「线路监测」: admin-built comparison charts of source node → target
// TCP latency, measured by the panel every 30 s with the agents' existing
// TcpPing (no agent change). One scheduler goroutine (linemon.Engine), ≤ 4
// probes in flight, samples written in batches, 30 days kept.

const (
	lineMonitorCommandTimeout = 6 * time.Second // 3 s connect timeout + channel margin
	lineMonitorFlushEvery     = 30 * time.Second
	lineMonitorFlushRows      = 100
	lineMonitorBufferCap      = 4000 // samples kept in memory while the DB refuses writes
	lineMonitorPruneBatch     = 2000
	lineMonitorPrunePause     = 20 * time.Millisecond
	lineMonitorCacheTTL       = 60 * time.Second
	lineMonitorCacheMinRange  = 12 * 3600 // ranges ≥ 12 h are cached briefly
	lineMonitorCacheMax       = 8
	lineMonitorMaxEvents      = 200
	lineMonitorSSHHint        = "请勿使用 SSH 端口（22），频繁连接可能被对方 fail2ban 封禁"
)

type lineMonitorLive struct {
	TS     int64         `json:"ts"`
	Status int           `json:"status"` // 0 = 无数据 (gap)
	Ms     linemon.Float `json:"ms"`
	Reason string        `json:"reason,omitempty"`
}

type lineMonitorCacheEntry struct {
	at   time.Time
	data *lineMonitorData
}

// lineMonitorRuntime is the memory-only state of plan 255.
type lineMonitorRuntime struct {
	once   sync.Once
	engine *linemon.Engine
	// Tests shorten the slot / retry before the first use (0 = 30 s / 2 s).
	testSlot, testRetry time.Duration

	mu        sync.Mutex
	buffer    []model.LineMonitorSample
	lastFlush time.Time
	live      map[int64]lineMonitorLive
	version   atomic.Int64 // bumps on every config change (cache key)

	cacheMu sync.Mutex
	cache   map[string]lineMonitorCacheEntry
	readMu  sync.Mutex // one long range read at a time

	cleanupRunning atomic.Bool
	errLogAt       atomic.Int64
	ports          lineMonitorPortRuntime // plan 255 part 2 检测端口
	writes         atomic.Int64
	writeNanos     atomic.Int64
}

func (h *Handler) lineMon() *lineMonitorRuntime {
	rt := &h.lineMonitor
	rt.once.Do(func() {
		rt.live = make(map[int64]lineMonitorLive)
		rt.cache = make(map[string]lineMonitorCacheEntry)
		rt.engine = linemon.NewEngine(linemon.Config{
			Slot:       rt.testSlot,
			Retry:      rt.testRetry,
			Load:       h.lineMonitorTargets,
			Online:     h.lineMonitorSourceOnline,
			Probe:      h.lineMonitorProbe,
			OnResult:   h.lineMonitorResult,
			OnMaintain: h.lineMonitorMaintain,
			OnIdle:     func() { h.lineMonitorFlush(true) },
		})
	})
	return rt
}

// logLineMonitorError logs at most once per 10 minutes (no per-sample logs).
func (h *Handler) logLineMonitorError(format string, args ...interface{}) {
	rt := h.lineMon()
	now := time.Now().Unix()
	if last := rt.errLogAt.Load(); now-last < 600 || !rt.errLogAt.CompareAndSwap(last, now) {
		return
	}
	log.Printf("[line-monitor] "+format+" (logged at most every 10 min)", args...)
}

func (h *Handler) runLineMonitorLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	rt := h.lineMon()
	rt.engine.Run(ctx)
	h.lineMonitorFlush(true)
}

func (h *Handler) lineMonitorKick() {
	rt := h.lineMon()
	rt.version.Add(1)
	rt.cacheMu.Lock()
	rt.cache = make(map[string]lineMonitorCacheEntry)
	rt.cacheMu.Unlock()
	rt.engine.Kick()
}

func (h *Handler) lineMonitorSourceOnline(nodeID int64) bool {
	if h.nodeCommandSender != nil && h.nodeOnlineFn == nil {
		return true // tests: the fake sender answers for every node
	}
	return h.nodeAgentOnline(nodeID)
}

// lineMonitorEffectiveHost: the line's host, or the target node's current
// address as seen from the source (same choice as the tunnel probes).
func lineMonitorEffectiveHost(line model.LineMonitorLine, nodes map[int64]*nodeRecord) string {
	if host := strings.TrimSpace(line.Host); host != "" {
		return host
	}
	if line.TargetNodeID <= 0 {
		return ""
	}
	target := nodes[line.TargetNodeID]
	if target == nil {
		return ""
	}
	host, _, err := resolveChainProbeTarget(nodes[line.SourceNodeID], target, line.Port, "", "")
	if err != nil {
		return ""
	}
	return host
}

func nodeRecordMap(list []*nodeRecord) map[int64]*nodeRecord {
	out := make(map[int64]*nodeRecord, len(list))
	for _, n := range list {
		if n != nil {
			out[n.ID] = n
		}
	}
	return out
}

func (h *Handler) lineMonitorTargets() ([]linemon.Target, bool, error) {
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		h.logLineMonitorError("load lines: %v", err)
		return nil, false, err
	}
	if len(lines) == 0 {
		return nil, h.lineMon().cleanupRunning.Load(), nil
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		h.logLineMonitorError("load nodes: %v", err)
		return nil, true, err
	}
	nodes := nodeRecordMap(list)
	targets := make([]linemon.Target, 0, len(lines))
	auto := map[int64]int64{}
	defer func() {
		pr := h.lineMonPorts()
		pr.mu.Lock()
		pr.autoTarget = auto
		pr.mu.Unlock()
	}()
	for _, line := range lines {
		if line.Paused == 1 {
			continue
		}
		if line.PortMode == model.LinePortAuto && line.TargetNodeID > 0 {
			auto[line.ID] = line.TargetNodeID
		}
		src := nodes[line.SourceNodeID]
		if src == nil || src.IsRemote == 1 {
			continue
		}
		host := lineMonitorEffectiveHost(line, nodes)
		if host == "" || line.Port <= 0 || line.Port > 65535 {
			continue
		}
		targets = append(targets, linemon.Target{LineID: line.ID, NodeID: line.SourceNodeID, Host: host, Port: line.Port})
	}
	return targets, true, nil
}

func shortReason(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 80 {
		r := []rune(s)
		s = string(r[:80]) + "…"
	}
	return s
}

// lineMonitorProbe: one TcpPing (count 1, 3 s) from the source agent.
func (h *Handler) lineMonitorProbe(ctx context.Context, t linemon.Target) linemon.Attempt {
	if ctx.Err() != nil {
		return linemon.Attempt{Kind: linemon.NoAnswer, Reason: "面板停止"}
	}
	payload := map[string]interface{}{"ip": t.Host, "port": t.Port, "count": 1, "timeout": linemon.PingTimeoutMS}
	var (
		res ws.CommandResult
		err error
	)
	if h.nodeCommandSender != nil {
		res, err = h.nodeCommandSender(t.NodeID, "TcpPing", payload, lineMonitorCommandTimeout)
	} else if h.wsServer != nil {
		res, err = h.wsServer.SendCommandContext(ctx, t.NodeID, "TcpPing", payload, lineMonitorCommandTimeout)
	} else {
		err = errors.New("节点不在线")
	}
	if err != nil {
		reason := err.Error()
		switch {
		case strings.Contains(reason, "不在线"):
			reason = "源节点离线"
		case strings.Contains(reason, "超时"):
			reason = "节点响应超时"
		}
		return linemon.Attempt{Kind: linemon.NoAnswer, Reason: shortReason(reason)}
	}
	if res.Data == nil {
		return linemon.Attempt{Kind: linemon.NoAnswer, Reason: "节点未返回数据"}
	}
	ms := asFloat(res.Data["averageTime"], -1)
	if asBool(res.Data["success"], false) && ms >= 0 && !math.IsNaN(ms) && !math.IsInf(ms, 0) {
		return linemon.Attempt{Kind: linemon.Connected, Ms: ms}
	}
	reason := asString(res.Data["errorMessage"])
	if reason == "" {
		reason = "连接失败"
	}
	// Plan 255 part 2: agents with monitorPort say why (refused = the target
	// answers but nothing listens → 端口未开启, not 断流).
	kind := asString(res.Data["errorKind"])
	return linemon.Attempt{Kind: linemon.Failed, Refused: kind == "refused", ErrKind: kind, Reason: shortReason(reason)}
}

func (h *Handler) lineMonitorResult(r linemon.Result) {
	rt := h.lineMon()
	live := lineMonitorLive{TS: r.TS, Status: r.Status, Ms: linemon.Float(math.NaN()), Reason: r.Reason}
	if r.Status == linemon.StatusOK || r.Status == linemon.StatusLoss {
		live.Ms = linemon.Float(r.Ms)
	}
	rt.mu.Lock()
	rt.live[r.LineID] = live
	if !r.Gap {
		ms10 := int(math.Round(r.Ms * 10))
		if r.Status == linemon.StatusDown || ms10 < 0 {
			ms10 = 0
		}
		rt.buffer = append(rt.buffer, model.LineMonitorSample{LineID: r.LineID, TS: r.TS, Status: r.Status, Ms10: ms10})
		if len(rt.buffer) > lineMonitorBufferCap {
			rt.buffer = rt.buffer[len(rt.buffer)-lineMonitorBufferCap:]
		}
	}
	rt.mu.Unlock()
	h.lineMonitorFlush(false)
	if r.Status == linemon.StatusClosed {
		h.lineMonitorHeal(r.LineID)
	}
}

// lineMonitorFlush writes the buffered samples in one statement every 30 s
// (or every 100 rows); force writes whatever is buffered now.
func (h *Handler) lineMonitorFlush(force bool) {
	rt := h.lineMon()
	rt.mu.Lock()
	if len(rt.buffer) == 0 || (!force && len(rt.buffer) < lineMonitorFlushRows && time.Since(rt.lastFlush) < lineMonitorFlushEvery) {
		rt.mu.Unlock()
		return
	}
	batch := rt.buffer
	rt.buffer = nil
	rt.lastFlush = time.Now()
	rt.mu.Unlock()
	start := time.Now()
	err := h.repo.InsertLineMonitorSamples(batch)
	rt.writes.Add(1)
	rt.writeNanos.Add(int64(time.Since(start)))
	if err != nil {
		h.logLineMonitorError("write %d samples: %v", len(batch), err)
		rt.mu.Lock()
		rt.buffer = append(batch, rt.buffer...)
		if len(rt.buffer) > lineMonitorBufferCap {
			rt.buffer = rt.buffer[len(rt.buffer)-lineMonitorBufferCap:]
		}
		rt.mu.Unlock()
	}
}

// lineMonitorPending copies the not-yet-written samples of one line.
func (h *Handler) lineMonitorPending(lineID int64) []model.LineMonitorSample {
	rt := h.lineMon()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var out []model.LineMonitorSample
	for _, s := range rt.buffer {
		if s.LineID == lineID {
			out = append(out, s)
		}
	}
	return out
}

// lineMonitorMaintain (start + hourly): flush, then drop samples older than
// 30 days and samples of deleted lines, in small batches.
func (h *Handler) lineMonitorMaintain() {
	h.lineMonitorFlush(true)
	h.lineMonitorPrune(time.Now())
}

func (h *Handler) lineMonitorPrune(now time.Time) (deleted int64) {
	ids, err := h.repo.LineMonitorSampleLineIDs()
	if err != nil {
		h.logLineMonitorError("prune list: %v", err)
		return 0
	}
	if len(ids) == 0 {
		return 0
	}
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		h.logLineMonitorError("prune lines: %v", err)
		return 0
	}
	exists := make(map[int64]bool, len(lines))
	for _, l := range lines {
		exists[l.ID] = true
	}
	cutoff := now.Unix() - linemon.RetentionSeconds
	for _, id := range ids {
		before := cutoff
		if !exists[id] {
			before = math.MaxInt64 // deleted line: everything goes
		}
		for {
			n, err := h.repo.DeleteLineMonitorSamplesBefore(id, before, lineMonitorPruneBatch)
			if err != nil {
				h.logLineMonitorError("prune line %d: %v", id, err)
				break
			}
			deleted += n
			if n < lineMonitorPruneBatch {
				break
			}
			time.Sleep(lineMonitorPrunePause) // let the other DB work through
		}
	}
	return deleted
}

// lineMonitorCleanupAsync deletes the samples of removed lines in the
// background (batched); one cleanup at a time.
func (h *Handler) lineMonitorCleanupAsync(lineIDs []int64) {
	if len(lineIDs) == 0 {
		return
	}
	rt := h.lineMon()
	rt.mu.Lock()
	removed := make(map[int64]bool, len(lineIDs))
	for _, id := range lineIDs {
		removed[id] = true
		delete(rt.live, id)
	}
	kept := rt.buffer[:0]
	for _, s := range rt.buffer {
		if !removed[s.LineID] {
			kept = append(kept, s)
		}
	}
	rt.buffer = kept
	rt.mu.Unlock()
	if !rt.cleanupRunning.CompareAndSwap(false, true) {
		return // the running pass re-lists the orphans before it ends
	}
	h.lineMonitorCleanupWG.Add(1)
	go func() {
		defer h.lineMonitorCleanupWG.Done()
		defer rt.cleanupRunning.Store(false)
		for i := 0; i < 3; i++ {
			if h.lineMonitorDeleteOrphans() == 0 {
				return
			}
		}
	}()
}

func (h *Handler) lineMonitorDeleteOrphans() (deleted int64) {
	ids, err := h.repo.LineMonitorSampleLineIDs()
	if err != nil || len(ids) == 0 {
		return 0
	}
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		return 0
	}
	exists := make(map[int64]bool, len(lines))
	for _, l := range lines {
		exists[l.ID] = true
	}
	for _, id := range ids {
		if exists[id] {
			continue
		}
		for {
			n, err := h.repo.DeleteLineMonitorSamplesBefore(id, math.MaxInt64, lineMonitorPruneBatch)
			if err != nil {
				h.logLineMonitorError("delete samples of line %d: %v", id, err)
				return deleted
			}
			deleted += n
			if n < lineMonitorPruneBatch {
				break
			}
			time.Sleep(lineMonitorPrunePause)
		}
	}
	return deleted
}

// ─── HTTP ────────────────────────────────────────────────────────────

func lineMonitorAdmin(w http.ResponseWriter, r *http.Request) bool {
	_, roleID, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return false
	}
	if roleID != 0 {
		response.WriteJSON(w, response.Err(403, "权限不足，仅管理员可操作"))
		return false
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return false
	}
	return true
}

func lineMonitorDecode(r *http.Request, out interface{}) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type lineMonitorLineView struct {
	model.LineMonitorLine
	SourceName    string           `json:"sourceName"`
	SourceOnline  bool             `json:"sourceOnline"`
	SourceRemote  bool             `json:"sourceRemote,omitempty"`
	TargetName    string           `json:"targetName,omitempty"`
	EffectiveHost string           `json:"effectiveHost"`
	Live          *lineMonitorLive `json:"live,omitempty"`
	// CheckPort (plan 255 part 2, auto lines): the reserved port as the
	// panel and the target agent see it.
	CheckPort *lineMonitorCheckPortView `json:"checkPort,omitempty"`
}

type lineMonitorCheckPortView struct {
	Port         int    `json:"port"`
	State        string `json:"state"`      // open | closing | "" (not reserved)
	AgentState   string `json:"agentState"` // "" | open | busy | refused | error
	AgentMsg     string `json:"agentMsg,omitempty"`
	TargetOnline bool   `json:"targetOnline"`
	Capable      bool   `json:"capable"`
}

type lineMonitorChartView struct {
	model.LineMonitorChart
	Lines []lineMonitorLineView `json:"lines"`
}

func (h *Handler) lineMonitorList(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	charts, err := h.repo.ListLineMonitorCharts()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	var nodes map[int64]*nodeRecord
	if len(lines) > 0 {
		list, err := h.repo.ListLineMonitorNodeRecords()
		if err != nil {
			response.WriteJSON(w, response.Err(-2, err.Error()))
			return
		}
		nodes = nodeRecordMap(list)
	}
	portRows := map[[2]int64]model.LineMonitorPort{}
	if len(lines) > 0 {
		rows, err := h.repo.ListLineMonitorPorts(0)
		if err != nil {
			response.WriteJSON(w, response.Err(-2, err.Error()))
			return
		}
		for _, r := range rows {
			portRows[[2]int64{r.NodeID, int64(r.Port)}] = r
		}
	}
	rt := h.lineMon()
	rt.mu.Lock()
	live := make(map[int64]lineMonitorLive, len(rt.live))
	for k, v := range rt.live {
		live[k] = v
	}
	rt.mu.Unlock()
	byChart := make(map[int64][]lineMonitorLineView)
	for _, l := range lines {
		v := lineMonitorLineView{LineMonitorLine: l, EffectiveHost: lineMonitorEffectiveHost(l, nodes)}
		if src := nodes[l.SourceNodeID]; src != nil {
			v.SourceName, v.SourceRemote = src.Name, src.IsRemote == 1
			v.SourceOnline = src.IsRemote == 0 && h.lineMonitorSourceOnline(src.ID)
		}
		if l.TargetNodeID > 0 {
			if t := nodes[l.TargetNodeID]; t != nil {
				v.TargetName = t.Name
			}
		}
		if lv, ok := live[l.ID]; ok {
			lv := lv
			v.Live = &lv
		}
		if l.PortMode == model.LinePortAuto && l.TargetNodeID > 0 {
			cp := &lineMonitorCheckPortView{Port: l.Port, TargetOnline: h.lineMonitorSourceOnline(l.TargetNodeID)}
			cp.Capable = cp.TargetOnline && h.monitorPortCapable(l.TargetNodeID)
			if row, ok := portRows[[2]int64{l.TargetNodeID, int64(l.Port)}]; ok {
				cp.State, cp.AgentState, cp.AgentMsg = row.State, row.AgentState, row.AgentMsg
			}
			v.CheckPort = cp
		}
		byChart[l.ChartID] = append(byChart[l.ChartID], v)
	}
	out := make([]lineMonitorChartView, 0, len(charts))
	for _, c := range charts {
		ls := byChart[c.ID]
		if ls == nil {
			ls = []lineMonitorLineView{}
		}
		out = append(out, lineMonitorChartView{LineMonitorChart: c, Lines: ls})
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"charts":       out,
		"totalLines":   len(lines),
		"maxPerChart":  linemon.MaxLinesPerChart,
		"maxTotal":     linemon.MaxLinesTotal,
		"slotSeconds":  linemon.SlotSeconds,
		"retentionDay": linemon.RetentionSeconds / 86400,
		"engine":       rt.engine.Stats(),
	}))
}

type lineMonitorNodeOption struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Online  bool   `json:"online"`
	Remote  bool   `json:"remote,omitempty"`
	// CheckPort (plan 255 part 2): the agent takes MonitorPort (only known
	// while online; an online agent without it needs an OTA).
	CheckPort bool `json:"checkPort"`
}

func (h *Handler) lineMonitorOptions(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	out := make([]lineMonitorNodeOption, 0, len(list))
	for _, n := range list {
		addr := strings.Trim(strings.TrimSpace(n.ServerIP), "[]")
		if addr == "" {
			addr = firstNonBlank(n.ServerIPv4, n.ServerIPv6)
		}
		o := lineMonitorNodeOption{ID: n.ID, Name: n.Name, Address: addr, Remote: n.IsRemote == 1,
			Online: n.IsRemote == 0 && h.lineMonitorSourceOnline(n.ID)}
		o.CheckPort = o.Online && h.monitorPortCapable(n.ID)
		out = append(out, o)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"nodes": out, "sshHint": lineMonitorSSHHint,
		"checkPortUnsupported": lineMonitorUnsupported, "checkWindows": linemon.CheckWindows, "defaultCheckWindow": linemon.DefaultCheckWindow}))
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if v = strings.Trim(strings.TrimSpace(v), "[]"); v != "" {
			return v
		}
	}
	return ""
}

func (h *Handler) lineMonitorPorts(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.NodeID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	hints, err := h.repo.LineMonitorPortHints(req.NodeID, 20)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	filtered := hints[:0]
	for _, p := range hints {
		if p.Port != 22 { // never suggest SSH
			filtered = append(filtered, p)
		}
	}
	response.WriteJSON(w, response.OK(filtered))
}

type lineMonitorLineInput struct {
	ID           int64  `json:"id"`
	SourceNodeID int64  `json:"sourceNodeId"`
	TargetType   string `json:"targetType"` // node | custom
	TargetNodeID int64  `json:"targetNodeId"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Label        string `json:"label"`
	Paused       bool   `json:"paused"`
	// Plan 255 part 2: node targets auto | existing | manual; custom targets
	// keep the 开启命令 options (window hours, only-sources switch).
	PortMode    string `json:"portMode"`
	CheckWindow int    `json:"checkWindow"`
	CheckAllow  bool   `json:"checkAllow"`
}

type lineMonitorSaveInput struct {
	ID    int64                  `json:"id"`
	Name  string                 `json:"name"`
	Lines []lineMonitorLineInput `json:"lines"`
}

// validLineMonitorHost accepts an IP address or a host name (no scheme, port,
// path or spaces).
func validLineMonitorHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

// validateLineMonitorSave checks a chart + lines; returns the rows to store
// and the non-blocking warnings (port 22).
func validateLineMonitorSave(in lineMonitorSaveInput, nodes map[int64]*nodeRecord) (string, []model.LineMonitorLine, []string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", nil, nil, errors.New("请填写监测图名称")
	}
	if utf8.RuneCountInString(name) > 50 {
		return "", nil, nil, errors.New("监测图名称不能超过 50 个字符")
	}
	if len(in.Lines) == 0 {
		return "", nil, nil, errors.New("至少添加一条线路")
	}
	if len(in.Lines) > linemon.MaxLinesPerChart {
		return "", nil, nil, fmt.Errorf("每个监测图最多 %d 条线路", linemon.MaxLinesPerChart)
	}
	var warnings []string
	seen := make(map[string]bool)
	out := make([]model.LineMonitorLine, 0, len(in.Lines))
	for i, l := range in.Lines {
		n := i + 1
		src := nodes[l.SourceNodeID]
		if src == nil {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：请选择源节点", n)
		}
		if src.IsRemote == 1 {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：共享节点不能作为源节点", n)
		}
		host := strings.Trim(strings.TrimSpace(l.Host), "[]")
		targetID := l.TargetNodeID
		if l.TargetType == "custom" {
			targetID = 0
		}
		if targetID > 0 {
			if nodes[targetID] == nil {
				return "", nil, nil, fmt.Errorf("第 %d 条线路：目标节点不存在", n)
			}
			if targetID == l.SourceNodeID {
				return "", nil, nil, fmt.Errorf("第 %d 条线路：源节点和目标节点不能相同", n)
			}
		} else if host == "" {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：请选择目标节点或填写目标地址", n)
		}
		if host != "" && !validLineMonitorHost(host) {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：目标地址格式不正确（只填 IP 或域名，不含端口）", n)
		}
		mode := strings.TrimSpace(l.PortMode)
		window, allow := 0, 0
		if targetID > 0 {
			switch mode {
			case model.LinePortAuto, model.LinePortExisting, model.LinePortManual:
			case "":
				mode = model.LinePortManual
			default:
				return "", nil, nil, fmt.Errorf("第 %d 条线路：端口方式不正确", n)
			}
			if mode == model.LinePortAuto && nodes[targetID].IsRemote == 1 {
				return "", nil, nil, fmt.Errorf("第 %d 条线路：共享节点不能自动开启检测端口", n)
			}
		} else {
			mode = ""
			window = l.CheckWindow
			if window == 0 {
				window = linemon.DefaultCheckWindow
			}
			if !linemon.ValidCheckWindow(window) {
				return "", nil, nil, fmt.Errorf("第 %d 条线路：自动删除时间不正确", n)
			}
			if l.CheckAllow {
				allow = 1
			}
		}
		if mode == model.LinePortAuto {
			if l.Port < 0 || l.Port > 65535 {
				return "", nil, nil, fmt.Errorf("第 %d 条线路：端口范围 1-65535", n)
			}
		} else if l.Port <= 0 || l.Port > 65535 {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：端口范围 1-65535", n)
		}
		if l.Port == 22 && mode != model.LinePortAuto {
			warnings = append(warnings, fmt.Sprintf("第 %d 条线路：%s", n, lineMonitorSSHHint))
		}
		label := strings.TrimSpace(l.Label)
		if utf8.RuneCountInString(label) > 50 {
			return "", nil, nil, fmt.Errorf("第 %d 条线路：备注不能超过 50 个字符", n)
		}
		key := fmt.Sprintf("%d|%d|%s|%d", l.SourceNodeID, targetID, strings.ToLower(host), l.Port)
		if mode == model.LinePortAuto {
			key = fmt.Sprintf("%d|%d|%s|auto", l.SourceNodeID, targetID, strings.ToLower(host))
		}
		if seen[key] {
			return "", nil, nil, fmt.Errorf("第 %d 条线路与前面的线路重复", n)
		}
		seen[key] = true
		paused := 0
		if l.Paused {
			paused = 1
		}
		out = append(out, model.LineMonitorLine{ID: l.ID, SourceNodeID: l.SourceNodeID, TargetNodeID: targetID, Host: host, Port: l.Port, Label: label, Paused: paused,
			PortMode: mode, CheckWindow: window, CheckAllow: allow})
	}
	return name, out, warnings, nil
}

func (h *Handler) lineMonitorChartSave(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var in lineMonitorSaveInput
	if err := lineMonitorDecode(r, &in); err != nil {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	name, lines, warnings, err := validateLineMonitorSave(in, nodeRecordMap(list))
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	// Plan 255 part 2: a line newly set to auto needs a target agent with
	// MonitorPort (an offline target is fine: opened on its reconnect).
	var old map[int64]model.LineMonitorLine
	if in.ID > 0 {
		existing, err := h.repo.ListLineMonitorChartLines(in.ID)
		if err != nil {
			response.WriteJSON(w, response.Err(-2, err.Error()))
			return
		}
		old = make(map[int64]model.LineMonitorLine, len(existing))
		for _, l := range existing {
			old[l.ID] = l
		}
	}
	for i, l := range lines {
		if l.PortMode != model.LinePortAuto {
			continue
		}
		if o, ok := old[l.ID]; ok && o.PortMode == model.LinePortAuto && o.TargetNodeID == l.TargetNodeID {
			continue
		}
		if h.lineMonitorSourceOnline(l.TargetNodeID) && !h.monitorPortCapable(l.TargetNodeID) {
			response.WriteJSON(w, response.ErrDefault(fmt.Sprintf("第 %d 条线路：%s", i+1, lineMonitorUnsupported)))
			return
		}
	}
	chart := &model.LineMonitorChart{ID: in.ID, Name: name}
	res, err := h.repo.SaveLineMonitorChart(chart, lines, linemon.MaxLinesTotal, h.lineMonPorts().busySnapshot())
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.lineMonitorKick()
	h.lineMonitorCleanupAsync(res.Removed)
	// Open / close the check ports and verify the changed lines (bounded by
	// the command timeouts: ≤ 8 s push + ≤ 6 s TcpPing).
	checks := h.lineMonitorApplyPorts(res.Nodes, res.Verify)
	if warnings == nil {
		warnings = []string{}
	}
	if checks == nil {
		checks = []lineMonitorCheck{}
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": chart.ID, "warnings": warnings, "checks": checks}))
}

func (h *Handler) lineMonitorChartDelete(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	res, err := h.repo.DeleteLineMonitorChart(req.ID)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.lineMonitorKick()
	h.lineMonitorCleanupAsync(res.Removed)
	// Close the check ports no line uses any more (待关闭 until the agent acks).
	h.lineMonitorApplyPortsAsync(res.Nodes, nil)
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) lineMonitorChartReorder(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 200 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	if err := h.repo.ReorderLineMonitorCharts(req.IDs); err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) lineMonitorLinePause(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		ID     int64 `json:"id"`
		Paused bool  `json:"paused"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	if err := h.repo.SetLineMonitorLinePaused(req.ID, req.Paused); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.lineMonitorKick()
	// Resume: the check port is kept while paused, but the agent closes it
	// after 24 h without connections — send the list again and verify.
	if !req.Paused {
		if l, err := h.repo.GetLineMonitorLine(req.ID); err == nil && l != nil && l.PortMode == model.LinePortAuto && l.TargetNodeID > 0 {
			h.lineMonitorApplyPortsAsync([]int64{l.TargetNodeID}, []int64{l.ID})
		}
	}
	response.WriteJSON(w, response.OKEmpty())
}

// ─── data ────────────────────────────────────────────────────────────

var lineMonitorRanges = map[string]int64{"1h": 3600, "6h": 6 * 3600, "12h": 12 * 3600, "1d": 86400, "30d": 30 * 86400}

type lineMonitorData struct {
	ChartID int64                     `json:"chartId"`
	From    int64                     `json:"from"`
	To      int64                     `json:"to"`
	Step    int64                     `json:"step"`
	Now     int64                     `json:"now"`
	Slot    int64                     `json:"slot"`
	Lines   []linemon.Series          `json:"lines"`
	Stats   []linemon.LineStats       `json:"stats"`
	Events  []linemon.Event           `json:"events"`
	Live    map[int64]lineMonitorLive `json:"live"`
	QueryMs float64                   `json:"queryMs"`
	Cached  bool                      `json:"cached,omitempty"`
}

// lineMonitorWindow resolves the requested range to an aligned window within
// the retention.
func lineMonitorWindow(rangeKey string, from, to, now int64) (int64, int64, int64, error) {
	oldest := now - linemon.RetentionSeconds - linemon.SlotSeconds
	if d, ok := lineMonitorRanges[rangeKey]; ok {
		to, from = now, now-d
	} else if rangeKey != "" && rangeKey != "custom" {
		return 0, 0, 0, errors.New("时间范围不正确")
	} else {
		if from <= 0 || to <= 0 || to <= from {
			return 0, 0, 0, errors.New("请选择正确的开始和结束时间")
		}
		if to > now {
			to = now
		}
		if from < oldest {
			from = oldest
		}
		if to-from < 60 {
			return 0, 0, 0, errors.New("时间范围至少 1 分钟")
		}
	}
	step := linemon.StepFor(to - from)
	from, to = linemon.AlignWindow(from, to, step)
	return from, to, step, nil
}

func (h *Handler) lineMonitorDataHandler(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		ChartID int64  `json:"chartId"`
		Range   string `json:"range"`
		From    int64  `json:"from"`
		To      int64  `json:"to"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.ChartID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	data, err := h.lineMonitorQuery(req.ChartID, req.Range, req.From, req.To, time.Now())
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(data))
}

func (h *Handler) lineMonitorQuery(chartID int64, rangeKey string, reqFrom, reqTo int64, nowTime time.Time) (*lineMonitorData, error) {
	now := nowTime.Unix()
	from, to, step, err := lineMonitorWindow(rangeKey, reqFrom, reqTo, now)
	if err != nil {
		return nil, err
	}
	chart, err := h.repo.GetLineMonitorChart(chartID)
	if err != nil {
		return nil, err
	}
	if chart == nil {
		return nil, errors.New("监测图不存在")
	}
	rt := h.lineMon()
	long := to-from >= lineMonitorCacheMinRange
	key := fmt.Sprintf("%d|%d|%d|%d|%d", chartID, from, to, step, rt.version.Load())
	if long {
		rt.cacheMu.Lock()
		e, ok := rt.cache[key]
		rt.cacheMu.Unlock()
		if ok && nowTime.Sub(e.at) < lineMonitorCacheTTL {
			cp := *e.data
			cp.Cached = true
			cp.Live = h.lineMonitorLiveFor(cp.Lines)
			return &cp, nil
		}
		rt.readMu.Lock()
		defer rt.readMu.Unlock()
		// Another request may have filled it while this one waited.
		rt.cacheMu.Lock()
		e, ok = rt.cache[key]
		rt.cacheMu.Unlock()
		if ok && nowTime.Sub(e.at) < lineMonitorCacheTTL {
			cp := *e.data
			cp.Cached = true
			cp.Live = h.lineMonitorLiveFor(cp.Lines)
			return &cp, nil
		}
	}
	lines, err := h.repo.ListLineMonitorChartLines(chartID)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	data := &lineMonitorData{ChartID: chartID, From: from, To: to, Step: step, Now: now, Slot: linemon.SlotSeconds,
		Lines: make([]linemon.Series, 0, len(lines)), Stats: make([]linemon.LineStats, 0, len(lines))}
	var eventLists [][]linemon.Event
	for _, l := range lines {
		acc := linemon.NewAccumulator(l.ID, from, to, step, l.CreatedTime/1000, now)
		lastTS := int64(math.MinInt64)
		if err := h.repo.ReadLineMonitorSamples(l.ID, from, to, func(row repo.LineMonitorSampleRow) {
			acc.Add(row.TS, row.Status, row.Ms10)
			lastTS = row.TS
		}); err != nil {
			return nil, err
		}
		for _, s := range h.lineMonitorPending(l.ID) {
			if s.TS > lastTS && s.TS >= from && s.TS < to {
				acc.Add(s.TS, s.Status, s.Ms10)
				lastTS = s.TS
			}
		}
		series, stats, events := acc.Finish()
		data.Lines = append(data.Lines, series)
		data.Stats = append(data.Stats, stats)
		eventLists = append(eventLists, events)
	}
	data.Events = linemon.NewestEvents(eventLists, lineMonitorMaxEvents)
	data.QueryMs = float64(time.Since(start).Microseconds()) / 1000
	if long {
		rt.cacheMu.Lock()
		for k, e := range rt.cache {
			if nowTime.Sub(e.at) >= lineMonitorCacheTTL {
				delete(rt.cache, k)
			}
		}
		if len(rt.cache) >= lineMonitorCacheMax {
			rt.cache = make(map[string]lineMonitorCacheEntry) // ≤ 8 × ~0.4 MB
		}
		rt.cache[key] = lineMonitorCacheEntry{at: nowTime, data: data}
		rt.cacheMu.Unlock()
		cp := *data
		data = &cp
	}
	data.Live = h.lineMonitorLiveFor(data.Lines)
	return data, nil
}

func (h *Handler) lineMonitorLiveFor(series []linemon.Series) map[int64]lineMonitorLive {
	rt := h.lineMon()
	out := make(map[int64]lineMonitorLive, len(series))
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, s := range series {
		if v, ok := rt.live[s.LineID]; ok {
			out[s.LineID] = v
		}
	}
	return out
}
