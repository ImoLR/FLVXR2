package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 255 part 2 「检测端口」 (panel side).
//
// Node targets in auto mode: the save transaction reserves a check port
// (line_monitor_port) and this file sends the target agent its full list
// (MonitorPort sync — idempotent; the agent closes every port not listed),
// stores the answer, moves lines off ports the agent reported busy, and
// verifies each changed line with one TcpPing from its source. The list is
// re-sent after every reconnect of a capable agent (that is also what closes
// ports marked 待关闭 while the agent was offline, and stale ports an agent
// restored from its own file). 换一个端口 tries ≤ 3 new ports on demand.
// A 端口未开启 slot of an auto line re-sends the list at most once per
// 10 min per node. No other loop or probe.

const (
	lineMonitorPortPushTimeout = 8 * time.Second
	lineMonitorPortBusyTTL     = time.Hour
	lineMonitorPortHealEvery   = 10 * time.Minute
	lineMonitorRotateTries     = 3
	lineMonitorPushRounds      = 3
	lineMonitorVerifyParallel  = 8
	lineMonitorUnsupported     = "需升级节点 agent（OTA）后才可自动开启检测端口"
	lineMonitorRotateGaveUp    = "试了 3 个端口都连不上：可能商家防火墙 / 安全组只放行了部分端口。请检查商家后台的端口放行，或改用「使用已在监听的端口」"
)

// lineMonitorPortRuntime is the memory-only state of the check ports.
type lineMonitorPortRuntime struct {
	mu         sync.Mutex
	nodeLocks  map[int64]*sync.Mutex
	busy       map[int64]map[int]time.Time // ports agents reported busy / a rotate found unreachable (avoided 1 h)
	healAt     map[int64]time.Time
	autoTarget map[int64]int64 // auto line → target node (from the engine's last load)
}

func (h *Handler) lineMonPorts() *lineMonitorPortRuntime {
	rt := h.lineMon()
	p := &rt.ports
	p.mu.Lock()
	if p.nodeLocks == nil {
		p.nodeLocks = map[int64]*sync.Mutex{}
		p.busy = map[int64]map[int]time.Time{}
		p.healAt = map[int64]time.Time{}
		p.autoTarget = map[int64]int64{}
	}
	p.mu.Unlock()
	return p
}

func (p *lineMonitorPortRuntime) nodeLock(nodeID int64) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.nodeLocks[nodeID]
	if l == nil {
		l = &sync.Mutex{}
		p.nodeLocks[nodeID] = l
	}
	return l
}

func (p *lineMonitorPortRuntime) markBusy(nodeID int64, port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy[nodeID] == nil {
		p.busy[nodeID] = map[int]time.Time{}
	}
	p.busy[nodeID][port] = time.Now()
}

// busySnapshot: ports reported busy in the last hour (per node).
func (p *lineMonitorPortRuntime) busySnapshot() map[int64]map[int]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[int64]map[int]bool{}
	for node, ports := range p.busy {
		for port, at := range ports {
			if time.Since(at) > lineMonitorPortBusyTTL {
				delete(ports, port)
				continue
			}
			if out[node] == nil {
				out[node] = map[int]bool{}
			}
			out[node][port] = true
		}
		if len(ports) == 0 {
			delete(p.busy, node)
		}
	}
	return out
}

func lineMonitorPortTakenError(nodeName string, port int) error {
	return fmt.Errorf("节点 %s 端口 %d 已被「线路监测」的检测端口占用（可在线路监测里给那条线路换一个端口）", nodeName, port)
}

func (h *Handler) monitorPortCapable(nodeID int64) bool {
	if h.monitorPortCapableFn != nil {
		return h.monitorPortCapableFn(nodeID)
	}
	return h.wsServer != nil && h.wsServer.MonitorPortCapable(nodeID)
}

func (h *Handler) lineMonitorCommand(nodeID int64, cmd string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
	if h.nodeCommandSender != nil {
		return h.nodeCommandSender(nodeID, cmd, payload, timeout)
	}
	if h.wsServer != nil {
		return h.wsServer.SendCommandContext(context.Background(), nodeID, cmd, payload, timeout)
	}
	return ws.CommandResult{}, errors.New("节点不在线")
}

type monitorPortReply struct {
	Results []struct {
		Port  int    `json:"port"`
		State string `json:"state"`
		Error string `json:"error"`
	} `json:"results"`
}

// lineMonitorPushNode sends the node's full check-port list and stores the
// answer. Ports the agent reports busy move their lines to another port and
// the list goes again (≤ 3 rounds). answered = the agent took the list;
// moved = lines whose port changed (verify them).
func (h *Handler) lineMonitorPushNode(nodeID int64) (answered bool, moved []int64, err error) {
	if nodeID <= 0 || h.repo == nil {
		return false, nil, nil
	}
	pr := h.lineMonPorts()
	lock := pr.nodeLock(nodeID)
	lock.Lock()
	defer lock.Unlock()
	if !h.lineMonitorSourceOnline(nodeID) {
		return false, nil, nil
	}
	if !h.monitorPortCapable(nodeID) {
		return false, nil, errors.New(lineMonitorUnsupported)
	}
	for round := 0; round < lineMonitorPushRounds; round++ {
		rows, err := h.repo.ListLineMonitorPorts(nodeID)
		if err != nil {
			return answered, moved, err
		}
		open := []int{}
		var closing []int64
		for _, r := range rows {
			if r.State == model.LineMonitorPortOpen {
				open = append(open, r.Port)
			} else {
				closing = append(closing, r.ID)
			}
		}
		sort.Ints(open)
		res, err := h.lineMonitorCommand(nodeID, "MonitorPort", map[string]interface{}{"action": "sync", "ports": open}, lineMonitorPortPushTimeout)
		if err != nil {
			h.logLineMonitorError("node %d MonitorPort sync: %v", nodeID, err)
			return answered, moved, err
		}
		answered = true
		var reply monitorPortReply
		if raw, err := json.Marshal(res.Data); err == nil {
			_ = json.Unmarshal(raw, &reply)
		}
		results := make([]repo.LineMonitorPortResult, 0, len(reply.Results))
		var busy []int
		for _, r := range reply.Results {
			results = append(results, repo.LineMonitorPortResult{Port: r.Port, State: r.State, Error: r.Error})
			if r.State == "busy" {
				busy = append(busy, r.Port)
			}
		}
		if err := h.repo.ApplyLineMonitorPortSync(nodeID, closing, results); err != nil {
			return answered, moved, err
		}
		if len(busy) == 0 {
			return answered, moved, nil
		}
		for _, port := range busy {
			pr.markBusy(nodeID, port)
			ids, _, err := h.repo.ReplaceLineMonitorPort(nodeID, port, nil, pr.busySnapshot()[nodeID], true)
			if err != nil {
				// No free port left: the lines say so; the row stays reserved.
				h.lineMonitorMarkLines(nodeID, port, model.LineCheckBusy, err.Error())
				continue
			}
			moved = append(moved, ids...)
		}
	}
	return answered, moved, nil
}

func (h *Handler) lineMonitorMarkLines(nodeID int64, port int, state, msg string) {
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		return
	}
	for _, l := range lines {
		if l.PortMode == model.LinePortAuto && l.TargetNodeID == nodeID && l.Port == port {
			_ = h.repo.SetLineMonitorLineCheck(l.ID, state, msg)
		}
	}
}

// lineMonitorCheck is one line's verify result.
type lineMonitorCheck struct {
	LineID int64  `json:"lineId"`
	Port   int    `json:"port"`
	State  string `json:"state"`
	Msg    string `json:"msg,omitempty"`
}

// lineMonitorVerify checks the auto lines' check ports from their sources
// (one TcpPing each, ≤ 8 at once) and stores the result on the lines.
func (h *Handler) lineMonitorVerify(lineIDs []int64) []lineMonitorCheck {
	ids := uniqueLineIDs(lineIDs)
	if len(ids) == 0 {
		return nil
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		return nil
	}
	nodes := nodeRecordMap(list)
	rows, err := h.repo.ListLineMonitorPorts(0)
	if err != nil {
		return nil
	}
	rowByKey := map[[2]int64]model.LineMonitorPort{}
	for _, r := range rows {
		rowByKey[[2]int64{r.NodeID, int64(r.Port)}] = r
	}
	out := make([]lineMonitorCheck, len(ids))
	sem := make(chan struct{}, lineMonitorVerifyParallel)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = h.lineMonitorVerifyOne(id, nodes, rowByKey)
		}(i, id)
	}
	wg.Wait()
	res := out[:0]
	for _, c := range out {
		if c.LineID > 0 {
			res = append(res, c)
		}
	}
	return res
}

func uniqueLineIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (h *Handler) lineMonitorVerifyOne(id int64, nodes map[int64]*nodeRecord, rows map[[2]int64]model.LineMonitorPort) lineMonitorCheck {
	line, err := h.repo.GetLineMonitorLine(id)
	if err != nil || line == nil || line.PortMode != model.LinePortAuto || line.TargetNodeID <= 0 {
		return lineMonitorCheck{}
	}
	c := lineMonitorCheck{LineID: id, Port: line.Port}
	set := func(state, msg string) lineMonitorCheck {
		c.State, c.Msg = state, msg
		_ = h.repo.SetLineMonitorLineCheck(id, state, msg)
		return c
	}
	row, reserved := rows[[2]int64{line.TargetNodeID, int64(line.Port)}]
	switch {
	case !h.lineMonitorSourceOnline(line.TargetNodeID):
		return set(model.LineCheckPending, "目标节点离线：端口已保留，节点上线后自动开启")
	case !h.monitorPortCapable(line.TargetNodeID):
		return set(model.LineCheckUnsupported, lineMonitorUnsupported)
	case !reserved:
		return set(model.LineCheckFailed, "检测端口没有保留，请重新保存这条线路")
	case row.AgentState == "busy":
		return set(model.LineCheckBusy, firstNonBlankMsg(row.AgentMsg, "该端口在目标节点上被占用"))
	case row.AgentState == "refused" || row.AgentState == "error":
		return set(model.LineCheckFailed, firstNonBlankMsg(row.AgentMsg, "目标节点没能开启该端口"))
	case row.AgentState != "open":
		return set(model.LineCheckPending, "等待目标节点开启")
	}
	if !h.lineMonitorSourceOnline(line.SourceNodeID) {
		return set(model.LineCheckUnverified, "源节点离线，暂时无法验证")
	}
	host := lineMonitorEffectiveHost(*line, nodes)
	if host == "" {
		return set(model.LineCheckFailed, "目标节点没有可用地址")
	}
	a := h.lineMonitorProbe(context.Background(), linemon.Target{LineID: id, NodeID: line.SourceNodeID, Host: host, Port: line.Port})
	switch {
	case a.Kind == linemon.Connected:
		return set(model.LineCheckOK, fmt.Sprintf("%.1f ms", a.Ms))
	case a.Kind == linemon.Failed && a.Refused:
		return set(model.LineCheckRefused, "连接被拒绝：该地址上这个端口没有监听（可能 NAT 转发到了别的机器）")
	case a.Kind == linemon.Failed:
		return set(model.LineCheckUnreachable, shortReason(a.Reason))
	}
	return set(model.LineCheckUnverified, firstNonBlankMsg(a.Reason, "源节点未响应，暂时无法验证"))
}

func firstNonBlankMsg(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// lineMonitorApplyPorts pushes the touched nodes (≤ 4 at once) and verifies
// the changed lines plus every line moved off a busy port.
func (h *Handler) lineMonitorApplyPorts(nodes []int64, verify []int64) []lineMonitorCheck {
	var mu sync.Mutex
	all := append([]int64(nil), verify...)
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(n int64) {
			defer wg.Done()
			defer func() { <-sem }()
			_, moved, _ := h.lineMonitorPushNode(n)
			mu.Lock()
			all = append(all, moved...)
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	return h.lineMonitorVerify(all)
}

// lineMonitorApplyPortsAsync: same in the background (delete / pause / node
// events), one at a time per call site, bounded by the command timeouts.
func (h *Handler) lineMonitorApplyPortsAsync(nodes []int64, verify []int64) {
	if len(nodes) == 0 && len(verify) == 0 {
		return
	}
	h.lineMonitorCleanupWG.Add(1)
	go func() {
		defer h.lineMonitorCleanupWG.Done()
		h.lineMonitorApplyPorts(nodes, verify)
	}()
}

// lineMonitorPortsNodeOnline (reconnect): send the full list to a capable
// agent — even an empty one, which closes ports it restored from its own
// file — then verify the lines that were waiting for it.
func (h *Handler) lineMonitorPortsNodeOnline(nodeID int64) {
	if h.repo == nil || !h.monitorPortCapable(nodeID) {
		return
	}
	h.lineMonitorCleanupWG.Add(1)
	go func() {
		defer h.lineMonitorCleanupWG.Done()
		_, moved, err := h.lineMonitorPushNode(nodeID)
		if err != nil {
			return
		}
		lines, err := h.repo.ListLineMonitorLines()
		if err != nil {
			return
		}
		verify := moved
		for _, l := range lines {
			if l.PortMode == model.LinePortAuto && l.TargetNodeID == nodeID && l.CheckState != model.LineCheckOK {
				verify = append(verify, l.ID)
			}
		}
		h.lineMonitorVerify(verify)
	}()
}

// lineMonitorHeal: a 端口未开启 slot on an auto line → re-send the target's
// list (≤ 1 / 10 min per node; the agent may have expired the port while the
// source was offline for a day).
func (h *Handler) lineMonitorHeal(lineID int64) {
	pr := h.lineMonPorts()
	pr.mu.Lock()
	node := pr.autoTarget[lineID]
	if node <= 0 || time.Since(pr.healAt[node]) < lineMonitorPortHealEvery {
		pr.mu.Unlock()
		return
	}
	pr.healAt[node] = time.Now()
	pr.mu.Unlock()
	h.lineMonitorApplyPortsAsync([]int64{node}, []int64{lineID})
}

// lineMonitorNodeDeleted: the node's lines are gone (node delete
// transaction); drop their samples and close the check ports other targets
// kept for them. The deleted node's own reservations went with it (nothing
// to close there).
func (h *Handler) lineMonitorNodeDeleted(nodeID int64, lines []repo.LineMonitorLineRef) {
	if len(lines) == 0 {
		return
	}
	ids := make([]int64, 0, len(lines))
	var targets []int64
	for _, l := range lines {
		ids = append(ids, l.ID)
		if l.PortMode == model.LinePortAuto && l.TargetNodeID != nodeID {
			targets = append(targets, l.TargetNodeID)
		}
	}
	h.lineMonitorKick()
	h.lineMonitorCleanupAsync(ids)
	h.lineMonitorApplyPortsAsync(uniqueLineIDs(targets), nil)
}

// ─── HTTP ────────────────────────────────────────────────────────────

func (h *Handler) lineMonitorCheckPortVerify(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		LineID int64 `json:"lineId"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.LineID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	line, err := h.repo.GetLineMonitorLine(req.LineID)
	if err != nil || line == nil {
		response.WriteJSON(w, response.ErrDefault("线路不存在"))
		return
	}
	if line.PortMode != model.LinePortAuto || line.TargetNodeID <= 0 {
		response.WriteJSON(w, response.ErrDefault("这条线路没有使用自动检测端口"))
		return
	}
	checks := h.lineMonitorApplyPorts([]int64{line.TargetNodeID}, []int64{line.ID})
	h.lineMonitorKick()
	response.WriteJSON(w, response.OK(map[string]interface{}{"checks": checks}))
}

type lineMonitorRotateTry struct {
	Port  int    `json:"port"`
	State string `json:"state"`
	Msg   string `json:"msg,omitempty"`
}

func (h *Handler) lineMonitorCheckPortRotate(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		LineID int64 `json:"lineId"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.LineID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	tries, final, err := h.lineMonitorRotate(req.LineID)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.lineMonitorKick()
	response.WriteJSON(w, response.OK(map[string]interface{}{"tries": tries, "ok": final == "", "msg": final}))
}

// lineMonitorRotate moves one line to a new check port and verifies it; up
// to 3 ports, stopping at the first that the source reaches.
func (h *Handler) lineMonitorRotate(lineID int64) ([]lineMonitorRotateTry, string, error) {
	line, err := h.repo.GetLineMonitorLine(lineID)
	if err != nil || line == nil {
		return nil, "", errors.New("线路不存在")
	}
	if line.PortMode != model.LinePortAuto || line.TargetNodeID <= 0 {
		return nil, "", errors.New("这条线路没有使用自动检测端口")
	}
	if !h.lineMonitorSourceOnline(line.TargetNodeID) {
		return nil, "", errors.New("目标节点离线，暂时不能换端口")
	}
	if !h.monitorPortCapable(line.TargetNodeID) {
		return nil, "", errors.New(lineMonitorUnsupported)
	}
	pr := h.lineMonPorts()
	exclude := map[int]bool{line.Port: true}
	current := line.Port
	// A port that just failed is not picked again for an hour (any rotate).
	pr.markBusy(line.TargetNodeID, line.Port)
	var tries []lineMonitorRotateTry
	for i := 0; i < lineMonitorRotateTries; i++ {
		ex := map[int]bool{}
		for p := range exclude {
			ex[p] = true
		}
		for p := range pr.busySnapshot()[line.TargetNodeID] {
			ex[p] = true
		}
		_, newPort, err := h.repo.ReplaceLineMonitorPort(line.TargetNodeID, current, []int64{lineID}, ex, false)
		if err != nil {
			if len(tries) == 0 {
				return nil, "", err
			}
			return tries, err.Error(), nil
		}
		exclude[newPort] = true
		if _, _, err := h.lineMonitorPushNode(line.TargetNodeID); err != nil {
			tries = append(tries, lineMonitorRotateTry{Port: newPort, State: model.LineCheckFailed, Msg: err.Error()})
			return tries, "目标节点没有响应，请稍后再试", nil
		}
		checks := h.lineMonitorVerify([]int64{lineID})
		if len(checks) == 0 {
			return tries, "线路已不存在", nil
		}
		c := checks[0]
		current = c.Port
		exclude[current] = true
		tries = append(tries, lineMonitorRotateTry{Port: c.Port, State: c.State, Msg: c.Msg})
		switch c.State {
		case model.LineCheckOK:
			return tries, "", nil
		case model.LineCheckUnreachable, model.LineCheckRefused, model.LineCheckBusy:
			pr.markBusy(line.TargetNodeID, c.Port)
			continue // a port problem: try the next one
		default:
			return tries, c.Msg, nil // not about the port (offline / no answer)
		}
	}
	return tries, lineMonitorRotateGaveUp, nil
}

// lineMonitorSourceAddrs: the literal IPs a source node is known by, and
// whether its address is dynamic (DDNS record or a host name).
func lineMonitorSourceAddrs(n *nodeRecord, ddns bool) ([]netip.Addr, bool) {
	var out []netip.Addr
	dynamic := ddns
	for _, v := range []string{n.ServerIP, n.ServerIPv4, n.ServerIPv6} {
		v = strings.Trim(strings.TrimSpace(v), "[]")
		if v == "" {
			continue
		}
		a, err := netip.ParseAddr(v)
		if err != nil {
			if v == strings.Trim(strings.TrimSpace(n.ServerIP), "[]") {
				dynamic = true // the server address is a host name
			}
			continue
		}
		a = a.Unmap()
		dup := false
		for _, x := range out {
			dup = dup || x == a
		}
		if !dup && a.IsValid() && !a.IsUnspecified() {
			out = append(out, a)
		}
	}
	return out, dynamic
}

func (h *Handler) lineMonitorCheckPortCommand(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		Host      string  `json:"host"`
		Port      int     `json:"port"`
		Window    int     `json:"window"`
		Allow     *bool   `json:"allow"`
		SourceIDs []int64 `json:"sourceIds"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || len(req.SourceIDs) > linemon.MaxLinesPerChart {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	host := strings.Trim(strings.TrimSpace(req.Host), "[]")
	if host != "" && !validLineMonitorHost(host) {
		response.WriteJSON(w, response.ErrDefault("目标地址格式不正确"))
		return
	}
	if req.Window == 0 {
		req.Window = linemon.DefaultCheckWindow
	}
	if !linemon.ValidCheckWindow(req.Window) {
		response.WriteJSON(w, response.ErrDefault("自动删除时间不正确"))
		return
	}
	suggested := false
	if req.Port == 0 {
		used := map[int]bool{}
		if host != "" {
			if u, err := h.repo.LineMonitorCustomPorts(host); err == nil {
				used = u
			}
		}
		p, err := linemon.SuggestCustomPort(used)
		if err != nil {
			response.WriteJSON(w, response.ErrDefault(err.Error()))
			return
		}
		req.Port, suggested = p, true
	}
	if !linemon.ValidCheckPort(req.Port) {
		response.WriteJSON(w, response.ErrDefault("检测端口必须在 1024-65535 之间，且不能是 22"))
		return
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	nodes := nodeRecordMap(list)
	ids := uniqueLineIDs(req.SourceIDs)
	ddns, _ := h.repo.LineMonitorNodeHasDDNS(ids)
	var allowIPs []netip.Addr
	var dynamicNames, noIPNames []string
	for _, id := range ids {
		n := nodes[id]
		if n == nil {
			continue
		}
		addrs, dynamic := lineMonitorSourceAddrs(n, ddns[id])
		if len(addrs) == 0 {
			noIPNames = append(noIPNames, n.Name)
		}
		if dynamic {
			dynamicNames = append(dynamicNames, n.Name)
		}
		allowIPs = append(allowIPs, addrs...)
	}
	available := len(ids) > 0 && len(noIPNames) == 0 && len(allowIPs) <= linemon.MaxCheckAllow
	def := available && len(dynamicNames) == 0
	allow := def
	if req.Allow != nil {
		allow = *req.Allow && available
	}
	note := ""
	switch {
	case len(ids) == 0:
		note = "先选源节点，才能限制只允许源节点连接"
	case len(noIPNames) > 0:
		note = "「" + strings.Join(noIPNames, "」「") + "」没有固定 IP 地址，不能限制来源"
	case len(dynamicNames) > 0:
		note = "「" + strings.Join(dynamicNames, "」「") + "」是动态 IP（DDNS / 域名地址）：IP 一变就会被挡住、显示成断流，所以默认不限制来源"
	case len(allowIPs) > linemon.MaxCheckAllow:
		note = "来源 IP 太多，不能限制来源"
	default:
		note = "只有源节点的 IP 能连上，其它人扫描看到的是过滤端口；若源节点的出口 IP 和面板里的地址不同（NAT），请关掉并用「测试连通」检查"
	}
	var use []netip.Addr
	if allow {
		use = allowIPs
	}
	install, undo, err := linemon.CheckPortScript(req.Port, req.Window, use)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	ips := make([]string, 0, len(use))
	for _, a := range use {
		ips = append(ips, a.String())
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"port": req.Port, "suggested": suggested, "window": req.Window, "windows": linemon.CheckWindows,
		"install": install, "undo": undo, "allow": allow, "allowAvailable": available, "allowDefault": def,
		"allowIps": ips, "allowNote": note,
	}))
}

type lineMonitorTestResult struct {
	NodeID int64         `json:"nodeId"`
	Name   string        `json:"name"`
	OK     bool          `json:"ok"`
	Ms     linemon.Float `json:"ms"`
	Kind   string        `json:"kind"` // ok | refused | timeout | unreach | dns | failed | offline | noanswer
	Msg    string        `json:"msg,omitempty"`
}

func (h *Handler) lineMonitorCheckPortTest(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		Host      string  `json:"host"`
		Port      int     `json:"port"`
		SourceIDs []int64 `json:"sourceIds"`
	}
	if err := lineMonitorDecode(r, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	host := strings.Trim(strings.TrimSpace(req.Host), "[]")
	ids := uniqueLineIDs(req.SourceIDs)
	if !validLineMonitorHost(host) || req.Port <= 0 || req.Port > 65535 || len(ids) == 0 || len(ids) > linemon.MaxLinesPerChart {
		response.WriteJSON(w, response.ErrDefault("请填写目标地址、端口并选择源节点"))
		return
	}
	if net.ParseIP(host) == nil && strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	list, err := h.repo.ListLineMonitorNodeRecords()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	nodes := nodeRecordMap(list)
	out := make([]lineMonitorTestResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		n := nodes[id]
		if n == nil || n.IsRemote == 1 {
			out[i] = lineMonitorTestResult{NodeID: id, Kind: "failed", Msg: "源节点不存在", Ms: linemon.Float(-1)}
			continue
		}
		out[i] = lineMonitorTestResult{NodeID: id, Name: n.Name, Ms: linemon.Float(-1)}
		if !h.lineMonitorSourceOnline(id) {
			out[i].Kind, out[i].Msg = "offline", "源节点离线"
			continue
		}
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			a := h.lineMonitorProbe(context.Background(), linemon.Target{NodeID: id, Host: host, Port: req.Port})
			switch {
			case a.Kind == linemon.Connected:
				out[i].OK, out[i].Kind, out[i].Ms = true, "ok", linemon.Float(a.Ms)
			case a.Kind == linemon.Failed:
				out[i].Kind = firstNonBlankMsg(a.ErrKind, "failed")
				out[i].Msg = a.Reason
				if a.Refused {
					out[i].Msg = "连接被拒绝：端口没有监听（开启命令还没执行，或已自动删除）"
				}
			default:
				out[i].Kind, out[i].Msg = "noanswer", a.Reason
			}
		}(i, id)
	}
	wg.Wait()
	response.WriteJSON(w, response.OK(map[string]interface{}{"results": out}))
}
