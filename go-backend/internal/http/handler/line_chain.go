package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"go-backend/internal/landingegress"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// 「我自己的节点」chain rules (plan 108): the user's own node behind a 线路机
// tunnel. Creating one installs (if needed) the landing Core on every exit of
// the tunnel and adds an identical loopback VLESS Reality Vision node there
// whose outbound is the user's node; the rule targets 127.0.0.1:<port>.

const (
	chainRateWindow     = 10 * time.Minute
	chainRateMax        = 5 // non-admin chain creates / link changes per user per window
	chainMaxRunning     = 3 // background chain operations panel-wide
	chainStaleAfter     = 10 * time.Minute
	chainFailedKeep     = time.Hour
	chainReconcileEvery = 2 * time.Minute
)

// chainCreateDeadline bounds one operation (first Core install included);
// tests shorten it.
var chainCreateDeadline = 5 * time.Minute

var idemKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// chainResolver resolves upstream domains; tests replace it to stay offline.
var chainResolver linechain.Resolver = net.DefaultResolver

// chainProbe checks TCP reachability from an exit; tests replace it.
var chainProbe = func(h *Handler, nodeID int64, ip string, port int) (bool, string) {
	data, err := h.tcpPingViaNode(nodeID, ip, port, diagnosisExecOptions{commandTimeout: 10 * time.Second, pingTimeoutMS: 3000, pingCount: 2})
	if err != nil {
		return false, err.Error()
	}
	if asBool(data["success"], false) {
		return true, ""
	}
	msg := strings.TrimSpace(asString(data["errorMessage"]))
	if msg == "" {
		msg = strings.TrimSpace(asString(data["message"]))
	}
	if msg == "" {
		msg = "TCP 连接失败"
	}
	return false, msg
}

// chainWaitStep is the pause between desired-state pushes while an exit is
// still installing or applying; tests shorten it.
var chainWaitStep = 3 * time.Second

// chainGuard: per-user single flight, a sliding-window rate limit and a
// global cap on background operations. In memory; a restart rolls back
// stale operations through the reconciler.
type chainGuard struct {
	mu       sync.Mutex
	busy     map[int64]bool
	attempts map[int64][]time.Time
	running  int
	drivers  map[int64]bool
	wg       sync.WaitGroup
}

func (g *chainGuard) begin(userID int64, rateLimited bool, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy == nil {
		g.busy, g.attempts, g.drivers = map[int64]bool{}, map[int64][]time.Time{}, map[int64]bool{}
	}
	if g.busy[userID] {
		return repo.ErrChainInFlight
	}
	if rateLimited {
		recent := g.attempts[userID][:0]
		for _, t := range g.attempts[userID] {
			if now.Sub(t) < chainRateWindow {
				recent = append(recent, t)
			}
		}
		g.attempts[userID] = recent
		if len(recent) >= chainRateMax {
			wait := chainRateWindow - now.Sub(recent[0])
			return fmt.Errorf("操作太频繁：10 分钟内最多 %d 次创建或修改「我自己的节点」，请 %d 分钟后再试", chainRateMax, int(wait.Minutes())+1)
		}
	}
	if g.running >= chainMaxRunning {
		return errors.New("系统正在处理其他「我自己的节点」任务，请稍后再试")
	}
	g.busy[userID] = true
	if rateLimited {
		g.attempts[userID] = append(g.attempts[userID], now)
	}
	return nil
}

// refund forgets the last attempt when the request failed validation before
// anything was created (typos must not use up the rate limit).
func (g *chainGuard) refund(userID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n := len(g.attempts[userID]); n > 0 {
		g.attempts[userID] = g.attempts[userID][:n-1]
	}
}

func (g *chainGuard) end(userID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.busy, userID)
}

// startDriver reserves a background slot for one chain.
func (g *chainGuard) startDriver(chainID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.drivers == nil {
		g.busy, g.attempts, g.drivers = map[int64]bool{}, map[int64][]time.Time{}, map[int64]bool{}
	}
	if g.drivers[chainID] {
		return false
	}
	g.drivers[chainID] = true
	g.running++
	g.wg.Add(1)
	return true
}

func (g *chainGuard) stopDriver(chainID int64) {
	g.mu.Lock()
	delete(g.drivers, chainID)
	g.running--
	g.mu.Unlock()
	g.wg.Done()
}

func (g *chainGuard) hasDriver(chainID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.drivers[chainID]
}

// chainReplayKey marks a forward create/update replayed by a chain driver:
// only then may a rule target the chain's 127.0.0.1 port (and skip the
// 线路机 manual-target rule). Clients cannot set context values.
type chainReplayKey struct{}

type chainReplay struct {
	target string
}

func chainReplayFrom(r *http.Request) (chainReplay, bool) {
	v, ok := r.Context().Value(chainReplayKey{}).(chainReplay)
	return v, ok
}

func chainTarget(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// captureWriter records a handler's JSON envelope for in-process replays.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}
func (c *captureWriter) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *captureWriter) WriteHeader(s int)           { c.status = s }

func (c *captureWriter) envelope() (int, string, json.RawMessage) {
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(c.body.Bytes(), &env); err != nil {
		return -2, "规则保存结果无法解析", nil
	}
	return env.Code, env.Msg, env.Data
}

func (c *captureWriter) flushTo(w http.ResponseWriter) {
	for k, v := range c.header {
		w.Header()[k] = v
	}
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
	_, _ = w.Write(c.body.Bytes())
}

func replayRequest(claims auth.Claims, path, target string) *http.Request {
	ctx := context.WithValue(context.Background(), middleware.ClaimsContextKey, claims)
	ctx = context.WithValue(ctx, chainReplayKey{}, chainReplay{target: target})
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, path, http.NoBody)
	return r
}

func cloneReq(req map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(req))
	for k, v := range req {
		out[k] = v
	}
	for _, k := range []string{"targetMode", "chainLink", "idempotencyKey", "landingNodeId", "landingServerId", "landingProtocol"} {
		delete(out, k)
	}
	return out
}

// chainForbidden: addresses a chain upstream must never be — the panel's own
// address and every FLVX node address (resolved hostnames included).
func (h *Handler) chainForbidden() linechain.Forbidden {
	var nodes []netip.Prefix
	if hosts, err := h.repo.LandingExemptHosts(); err == nil {
		for _, cidr := range landingegress.ExemptSources(hosts, nil, h.egressResolve) {
			if p, err := netip.ParsePrefix(cidr); err == nil {
				nodes = append(nodes, p)
			}
		}
	}
	panel := map[netip.Addr]bool{}
	if v, err := h.repo.GetViteConfigValue("ip"); err == nil && strings.TrimSpace(v) != "" {
		host := strings.TrimSpace(v)
		if u, err := splitPanelHost(host); err == nil {
			host = u
		}
		if a, err := netip.ParseAddr(host); err == nil {
			panel[a.Unmap()] = true
		} else {
			for _, a := range h.egressResolve(strings.ToLower(host)) {
				panel[a.Unmap()] = true
			}
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipNet.IP); ok {
					panel[ip.Unmap()] = true
				}
			}
		}
	}
	return func(a netip.Addr) (string, bool) {
		for _, p := range nodes {
			if p.Contains(a) {
				return "FLVX 节点", true
			}
		}
		if panel[a] {
			return "面板服务器", true
		}
		return "", false
	}
}

// splitPanelHost turns "host:port", "[v6]:port" or a URL into its host.
func splitPanelHost(v string) (string, error) {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
	if i := strings.IndexAny(v, "/?"); i >= 0 {
		v = v[:i]
	}
	if h, _, err := net.SplitHostPort(v); err == nil {
		return strings.Trim(h, "[]"), nil
	}
	return strings.Trim(v, "[]"), nil
}

// parseChainLink parses and validates a pasted link (fields + address).
func (h *Handler) parseChainLink(ctx context.Context, link string) (linechain.Upstream, string, []netip.Addr, error) {
	up, name, err := linechain.ParseLink(link)
	if err != nil {
		return up, "", nil, err
	}
	addrs, err := linechain.ValidateAddress(ctx, up.Server, h.chainForbidden(), chainResolver)
	if err != nil {
		return up, "", nil, err
	}
	// The mainland-China target policy of normal rules applies to the
	// user's node; the already validated addresses are checked (no 2nd DNS).
	targets := make([]string, 0, len(addrs))
	for _, a := range addrs {
		targets = append(targets, net.JoinHostPort(a.String(), strconv.Itoa(up.Port)))
	}
	if err := h.checkForwardLanding(ctx, strings.Join(targets, ","), "gost", ""); err != nil {
		return up, "", nil, err
	}
	return up, name, addrs, nil
}

// checkChainExits verifies the tunnel can host chain nodes right now.
func (h *Handler) checkChainExits(tunnelID int64) (*repo.TunnelLineInfo, error) {
	info, err := h.repo.TunnelLineInfo(tunnelID)
	if err != nil {
		return nil, err
	}
	if ok, reason := tunnelChainAvailability(info); !ok {
		return nil, errors.New(reason)
	}
	for _, e := range info.Exits {
		if !e.Online {
			return nil, errors.New("出口节点「" + e.Name + "」离线，暂时无法使用「我自己的节点」，请稍后重试")
		}
	}
	return info, nil
}

// probeChainUpstream: every exit must reach the user's node (any exit may be
// picked per connection). UDP-only protocols cannot be probed with TCP.
func (h *Handler) probeChainUpstream(info *repo.TunnelLineInfo, up linechain.Upstream, addrs []netip.Addr) error {
	if up.UDPOnly() || len(addrs) == 0 {
		return nil
	}
	var mu sync.Mutex
	var failures []string
	var wg sync.WaitGroup
	for _, e := range info.Exits {
		wg.Add(1)
		go func(e repo.ChainExit) {
			defer wg.Done()
			ok, msg := chainProbe(h, e.NodeID, addrs[0].String(), up.Port)
			if !ok {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("出口节点「%s」无法连接到你的节点 %s：%s", e.Name, up.Address(), msg))
				mu.Unlock()
			}
		}(e)
	}
	wg.Wait()
	if len(failures) > 0 {
		sort.Strings(failures)
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}

func exitNodeIDs(info *repo.TunnelLineInfo) []int64 {
	ids := make([]int64, 0, len(info.Exits))
	for _, e := range info.Exits {
		ids = append(ids, e.NodeID)
	}
	return ids
}

func chainStatusView(c *model.ForwardChain) map[string]interface{} {
	return map[string]interface{}{
		"chainId": c.ID, "state": c.State, "op": c.Op, "progress": c.Progress, "opStartedAt": c.OpStartedAt,
		"lastOpAt": c.LastOpAt, "error": c.LastError, "forwardId": c.ForwardID,
		"summary": strings.TrimSpace(c.UpstreamProtocol + " · " + net.JoinHostPort(c.UpstreamHost, strconv.Itoa(c.UpstreamPort))),
	}
}

func chainActorClaims(r *http.Request) (auth.Claims, bool) {
	claims, ok := r.Context().Value(middleware.ClaimsContextKey).(auth.Claims)
	return claims, ok
}

// chainRuleLimit: chain rules count as rules; their hidden exit nodes make
// them the one place where the user's rule allowance (num) is enforced.
func (h *Handler) chainRuleLimit(owner *model.User, adding int64) error {
	if owner == nil || owner.RoleID == 0 || owner.Num <= 0 || owner.Num >= 99999 {
		return nil
	}
	n, err := h.repo.CountUserForwards(owner.ID)
	if err != nil {
		return err
	}
	pending, _ := h.repo.CountCreatingChains(owner.ID)
	if n+pending+adding > int64(owner.Num) {
		return fmt.Errorf("已达到规则数上限（%d 条），不能再创建「我自己的节点」规则", owner.Num)
	}
	return nil
}

// forwardCreateChain validates a chain create synchronously, persists the
// pending chain and returns; a background driver finishes it.
func (h *Handler) forwardCreateChain(w http.ResponseWriter, r *http.Request, req map[string]interface{}) {
	actorID, roleID, err := userRoleFromRequest(r)
	claims, ok := chainActorClaims(r)
	if err != nil || !ok {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return
	}
	ownerID := actorID
	if roleID == 0 {
		if id := asInt64(req["userId"], 0); id > 0 {
			ownerID = id
		}
	}
	fail := func(msg string) { response.WriteJSON(w, response.ErrDefault(msg)) }
	if mode := defaultString(asString(req["mode"]), "gost"); mode != "gost" {
		fail("「我自己的节点」只支持 gost 转发模式")
		return
	}
	idem := strings.TrimSpace(asString(req["idempotencyKey"]))
	if idem != "" && !idemKeyRe.MatchString(idem) {
		fail("请求参数错误")
		return
	}
	if idem != "" {
		if existing, err := h.repo.FindForwardChainByIdem(ownerID, idem); err == nil && existing != nil {
			response.WriteJSON(w, response.OK(chainStatusView(existing)))
			return
		}
	}
	tunnelID := asInt64(req["tunnelId"], 0)
	if tunnelID <= 0 {
		fail("隧道ID不能为空")
		return
	}
	if err := h.ensureTunnelPermission(ownerID, roleID, tunnelID); err != nil {
		fail(err.Error())
		return
	}
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		fail("隧道不存在")
		return
	}
	if tunnel.Status != 1 {
		fail("隧道已禁用，无法创建转发")
		return
	}
	now := time.Now()
	if err := h.ensureUserTunnelForwardAllowed(ownerID, tunnelID, now.UnixMilli()); err != nil {
		fail(err.Error())
		return
	}
	name := strings.TrimSpace(asString(req["name"]))
	if name == "" {
		fail("请填写规则名称")
		return
	}
	info, err := h.checkChainExits(tunnelID)
	if err != nil {
		fail(err.Error())
		return
	}
	owner, err := h.repo.GetUserByID(ownerID)
	if err != nil || owner == nil {
		fail("用户不存在")
		return
	}
	if err := h.chainRuleLimit(owner, 1); err != nil {
		fail(err.Error())
		return
	}
	entries, entryErr := h.allowedTunnelEntries(ownerID, tunnelID, roleID == 0 && actorID == ownerID)
	if entryErr != nil {
		fail(entryErr.Error())
		return
	}
	if port := asInt(req["inPort"], 0); port > 0 {
		for _, nodeID := range entries {
			node, nodeErr := h.getNodeRecord(nodeID)
			if nodeErr != nil {
				continue
			}
			for _, check := range []error{validateRemoteNodePort(node, port), validateLocalNodePort(node, port), h.validateForwardPortAvailability(node, port, 0)} {
				if check != nil {
					fail(check.Error())
					return
				}
			}
		}
	}
	up, _, addrs, err := h.parseChainLink(r.Context(), asString(req["chainLink"]))
	if err != nil {
		fail(err.Error())
		return
	}
	if err := h.chains.begin(ownerID, roleID != 0, now); err != nil {
		fail(err.Error())
		return
	}
	started := false
	defer func() {
		if !started {
			h.chains.end(ownerID)
		}
	}()
	if err := h.probeChainUpstream(info, up, addrs); err != nil {
		h.chains.refund(ownerID)
		fail(err.Error())
		return
	}
	chain, _, err := h.repo.CreateForwardChain(repo.ChainCreateInput{UserID: ownerID, TunnelID: tunnelID, IdemKey: idem, Upstream: up, ExitNodeIDs: exitNodeIDs(info), InFlightAfter: now.Add(-chainStaleAfter).UnixMilli()})
	if errors.Is(err, repo.ErrChainIdempotent) && chain != nil {
		h.chains.refund(ownerID)
		response.WriteJSON(w, response.OK(chainStatusView(chain)))
		return
	}
	if err != nil {
		h.chains.refund(ownerID)
		fail(err.Error())
		return
	}
	if !h.chains.startDriver(chain.ID) {
		servers, _ := h.repo.FailForwardChain(chain.ID, "系统繁忙")
		h.syncChainServers(servers)
		fail("系统正在处理其他「我自己的节点」任务，请稍后再试")
		return
	}
	started = true
	op := chainOp{kind: "create", chainID: chain.ID, ownerID: ownerID, claims: claims, req: cloneReq(req), deadline: now.Add(chainCreateDeadline), holdsUser: true}
	go h.runChainOp(op)
	response.WriteJSON(w, response.OK(chainStatusView(chain)))
}

type chainOp struct {
	kind      string // create | attach | relink | retarget | move
	chainID   int64
	ownerID   int64
	forwardID int64
	claims    auth.Claims
	req       map[string]interface{}
	deadline  time.Time
	holdsUser bool
	relink    bool // an upstream change is part of the operation
	plan      *repo.ChainRetarget
	tunnelID  int64
}

// runChainOp is the background driver. Each step persists progress; any
// failure rolls back what this operation added.
func (h *Handler) runChainOp(op chainOp) {
	defer h.chains.stopDriver(op.chainID)
	if op.holdsUser {
		defer h.chains.end(op.ownerID)
	}
	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("chain driver chain=%d panicked: %v", op.chainID, rec)
				err = errors.New("内部错误，已回滚")
			}
		}()
		switch op.kind {
		case "create", "attach":
			err = h.chainCreateSteps(op)
		case "relink", "retarget", "move":
			err = h.chainChangeSteps(op)
		default:
			err = errors.New("unknown chain operation")
		}
	}()
	if err == nil {
		return
	}
	log.Printf("chain operation %s chain=%d failed: %s", op.kind, op.chainID, compactErrorMessage(err.Error()))
	switch op.kind {
	case "create", "attach":
		servers, ferr := h.repo.FailForwardChain(op.chainID, err.Error())
		if ferr != nil {
			log.Printf("chain rollback chain=%d: %v", op.chainID, ferr)
		}
		h.syncChainServers(servers)
	default:
		h.rollbackChainChange(op, err.Error())
	}
}

func (h *Handler) rollbackChainChange(op chainOp, reason string) {
	var servers []int64
	if op.plan != nil {
		if s, err := h.repo.AbortChainRetarget(op.chainID); err == nil {
			servers = append(servers, s...)
		}
	}
	if op.relink {
		if s, err := h.repo.RevertChainUpstreamChange(op.chainID); err == nil {
			servers = append(servers, s...)
		}
	}
	_ = h.repo.FinishChainOp(op.chainID, reason)
	h.syncChainServers(servers)
}

// syncChainServers pushes desired state (best effort; the minute loop and a
// reconnecting agent converge anything that fails here).
func (h *Handler) syncChainServers(ids []int64) {
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := h.syncLandingServer(id); err != nil {
			log.Printf("chain sync server=%d pending: %s", id, compactErrorMessage(err.Error()))
		}
	}
}

// waitChainServers pushes each server's desired state until the chain's
// copies there are applied (Core installed, routing ready, not withheld).
func (h *Handler) waitChainServers(op chainOp, serverIDs []int64) error {
	for i, sid := range serverIDs {
		s, _ := h.repo.GetLandingServer(sid)
		name := fmt.Sprintf("%d", sid)
		if s != nil {
			name = s.Name
		}
		progress := fmt.Sprintf("正在准备出口「%s」（%d/%d）：首次使用需要安装落地程序，可能需要几十秒…", name, i+1, len(serverIDs))
		_ = h.repo.SetForwardChainProgress(op.chainID, progress)
		step := chainWaitStep
		last := "节点未响应"
		for {
			if _, err := h.repo.GetForwardChain(op.chainID); err != nil {
				return err
			}
			snapshot, err := h.syncLandingServer(sid)
			if err == nil {
				ok, reason := h.chainCopiesApplied(op.chainID, sid, snapshot)
				if ok {
					break
				}
				last = reason
				if strings.Contains(reason, "重启") {
					return fmt.Errorf("出口「%s」：%s", name, reason)
				}
			} else {
				last = err.Error()
			}
			if time.Now().Add(step).After(op.deadline) {
				return fmt.Errorf("出口「%s」准备超时：%s", name, last)
			}
			time.Sleep(step)
			if step < 10*time.Second {
				step *= 2
			}
		}
	}
	return nil
}

// chainCopiesApplied checks the agent's report for this chain's copies.
func (h *Handler) chainCopiesApplied(chainID, serverID int64, snapshot *landingSyncSnapshot) (bool, string) {
	if snapshot == nil {
		return false, "节点未返回落地状态"
	}
	if snapshot.Egress == nil {
		return false, "出口节点程序不支持「我自己的节点」，请先升级"
	}
	switch snapshot.Egress.State {
	case "applied":
	case "restart_required":
		return false, "出口的落地程序需要重启一次才能启用「我自己的节点」，请管理员在落地页该服务器的「出站与路由」中确认重启后再试"
	default:
		reason := strings.TrimSpace(snapshot.Egress.Error)
		if reason == "" {
			reason = "出站与路由尚未生效"
		}
		return false, reason
	}
	copies, err := h.repo.ListForwardChainNodes(chainID)
	if err != nil {
		return false, err.Error()
	}
	withheld := map[string]bool{}
	for _, t := range snapshot.Egress.Withheld {
		withheld[t] = true
	}
	present := map[int64]bool{}
	for _, c := range snapshot.Nodes {
		if c.Epoch != "" {
			present[c.ID] = true
		}
	}
	for _, c := range copies {
		if c.LandingServerID != serverID {
			continue
		}
		agentID := model.ChainAgentIDBase + c.ID
		if !present[agentID] || withheld[fmt.Sprintf("flvxl-%d", agentID)] {
			return false, "链式节点尚未生效"
		}
	}
	return true, ""
}

func (h *Handler) chainCreateSteps(op chainOp) error {
	chain, err := h.repo.GetForwardChain(op.chainID)
	if err != nil || chain == nil {
		return errors.New("链式规则已被删除")
	}
	servers, err := h.repo.ChainServerIDs([]int64{op.chainID})
	if err != nil {
		return err
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i] < servers[j] })
	if err := h.waitChainServers(op, servers); err != nil {
		return err
	}
	_ = h.repo.SetForwardChainProgress(op.chainID, "正在部署规则…")
	target := chainTarget(chain.Port)
	body := cloneReq(op.req)
	body["remoteAddr"] = target
	body["mode"] = "gost"
	body["strategy"] = "fifo"
	cw := &captureWriter{}
	forwardID := op.forwardID
	if op.kind == "attach" {
		body["id"] = op.forwardID
		h.forwardUpdateWithReq(cw, replayRequest(op.claims, "/api/v1/forward/update", target), body)
	} else {
		h.forwardCreateWithReq(cw, replayRequest(op.claims, "/api/v1/forward/create", target), body)
	}
	code, msg, data := cw.envelope()
	if code != 0 {
		if msg == "" {
			msg = "规则保存失败"
		}
		return errors.New(msg)
	}
	if op.kind == "create" {
		var created struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(data, &created)
		forwardID = created.ID
		if forwardID <= 0 {
			forwardID, _ = h.repo.FindChainTargetForward(op.ownerID, chain.TunnelID, target)
		}
		if forwardID <= 0 {
			return errors.New("规则已创建但未找到，请刷新后检查")
		}
	}
	if err := h.repo.ActivateForwardChain(op.chainID, forwardID); err != nil {
		if op.kind == "create" {
			h.deleteChainForwardBestEffort(forwardID)
		}
		return err
	}
	return nil
}

// deleteChainForwardBestEffort removes a rule created by a failed driver.
func (h *Handler) deleteChainForwardBestEffort(forwardID int64) {
	f, err := h.getForwardRecord(forwardID)
	if err != nil || f == nil {
		return
	}
	h.cleanupForwardRuntimeBestEffort(f)
	_ = h.deleteForwardByID(forwardID)
}

// chainChangeSteps: relink (new upstream), retarget (rule moves to another
// tunnel) and move (the tunnel's exits changed) all wait for the new copies
// or upstream, then switch the rule, then commit.
func (h *Handler) chainChangeSteps(op chainOp) error {
	chain, err := h.repo.GetForwardChain(op.chainID)
	if err != nil || chain == nil {
		return errors.New("链式规则已被删除")
	}
	var wait []int64
	if op.plan != nil {
		wait = append(wait, op.plan.AddedServerIDs...)
	}
	if op.relink {
		servers, err := h.repo.ChainServerIDs([]int64{op.chainID})
		if err != nil {
			return err
		}
		wait = append(wait, servers...)
	}
	wait = uniqueInt64s(wait)
	sort.Slice(wait, func(i, j int) bool { return wait[i] < wait[j] })
	if err := h.waitChainServers(op, wait); err != nil {
		return err
	}
	_ = h.repo.SetForwardChainProgress(op.chainID, "正在更新规则…")
	port := chain.Port
	if op.plan != nil {
		port = op.plan.NewPort
	}
	target := chainTarget(port)
	switch {
	case op.req != nil:
		body := cloneReq(op.req)
		body["id"] = chain.ForwardID
		body["remoteAddr"] = target
		body["mode"] = "gost"
		body["strategy"] = "fifo"
		if op.tunnelID > 0 {
			body["tunnelId"] = op.tunnelID
		}
		cw := &captureWriter{}
		h.forwardUpdateWithReq(cw, replayRequest(op.claims, "/api/v1/forward/update", target), body)
		if code, msg, _ := cw.envelope(); code != 0 {
			if msg == "" {
				msg = "规则保存失败"
			}
			return errors.New(msg)
		}
	case op.plan != nil && op.plan.NewPort != op.plan.OldPort:
		// Tunnel edit moved the exits to a new port: only the target changes.
		if err := h.repo.SetForwardRemoteAddr(chain.ForwardID, target); err != nil {
			return err
		}
		f, err := h.getForwardRecord(chain.ForwardID)
		if err != nil {
			return err
		}
		if f.Status == 1 {
			if err := h.syncForwardServices(f, "UpdateService", true); err != nil {
				_ = h.repo.SetForwardRemoteAddr(chain.ForwardID, chainTarget(op.plan.OldPort))
				if old, gerr := h.getForwardRecord(chain.ForwardID); gerr == nil {
					_ = h.syncForwardServices(old, "UpdateService", true)
				}
				return err
			}
		}
	}
	var servers []int64
	if op.plan != nil {
		tunnelID := chain.TunnelID
		if op.tunnelID > 0 {
			tunnelID = op.tunnelID
		}
		if err := h.repo.CommitChainRetarget(op.plan, tunnelID); err != nil {
			return err
		}
		servers = append(servers, op.plan.RemovedServerIDs...)
		servers = append(servers, op.plan.AddedServerIDs...)
	}
	if op.relink {
		if err := h.repo.CommitChainUpstreamChange(op.chainID); err != nil {
			return err
		}
	}
	_ = h.repo.FinishChainOp(op.chainID, "")
	h.syncChainServers(servers)
	return nil
}

// forwardUpdateChainHook routes rule edits that involve a chain. It returns
// false when the normal update path should handle the request.
func (h *Handler) forwardUpdateChainHook(w http.ResponseWriter, r *http.Request, req map[string]interface{}) bool {
	if _, replay := chainReplayFrom(r); replay {
		return false
	}
	id := asInt64(req["id"], 0)
	if id <= 0 {
		return false
	}
	forward, actorID, roleID, err := h.resolveForwardAccess(r, id)
	if err != nil || forward == nil {
		return false
	}
	chain, err := h.repo.GetForwardChainByForward(id)
	if err != nil {
		return false
	}
	targetMode := asString(req["targetMode"])
	wantChain := targetMode == "chain"
	fail := func(msg string) bool { response.WriteJSON(w, response.ErrDefault(msg)); return true }
	if chain == nil && !wantChain {
		return false
	}
	if chain != nil && chain.OpStartedAt > 0 {
		return fail("该规则正在处理中，请稍候再试")
	}
	if chain != nil && chain.State != model.ChainStateActive {
		return fail("该规则的「我自己的节点」正在处理中，请稍候再试")
	}
	claims, ok := chainActorClaims(r)
	if !ok {
		return fail("无效的token或token已过期")
	}
	tunnelID := asInt64(req["tunnelId"], forward.TunnelID)
	link := strings.TrimSpace(asString(req["chainLink"]))
	now := time.Now()

	if chain != nil && !wantChain && targetMode != "" {
		// Switching to another target type: normal update, then remove the chain.
		cw := &captureWriter{}
		h.forwardUpdateWithReq(cw, r, req)
		if code, _, _ := cw.envelope(); code == 0 {
			servers, derr := h.repo.DeleteForwardChain(chain.ID)
			if derr != nil {
				log.Printf("chain detach forward=%d: %v", id, derr)
			}
			h.syncChainServers(servers)
		}
		cw.flushTo(w)
		return true
	}
	if chain != nil && link == "" && tunnelID == forward.TunnelID {
		// Name, port, limits…: the normal path with the chain target pinned.
		body := cloneReq(req)
		body["remoteAddr"] = chainTarget(chain.Port)
		body["mode"] = "gost"
		body["strategy"] = "fifo"
		h.forwardUpdateWithReq(w, replayWithRequest(r, chainTarget(chain.Port)), body)
		return true
	}
	if mode := defaultString(asString(req["mode"]), forward.Mode); mode != "gost" {
		return fail("「我自己的节点」只支持 gost 转发模式")
	}
	if tunnelID != forward.TunnelID {
		if err := h.ensureTunnelPermission(actorID, roleID, tunnelID); err != nil {
			return fail(err.Error())
		}
		if t, err := h.getTunnelRecord(tunnelID); err != nil || t.Status != 1 {
			return fail("目标隧道不存在或已禁用")
		}
	}
	info, err := h.checkChainExits(tunnelID)
	if err != nil {
		return fail(err.Error())
	}
	var up linechain.Upstream
	var addrs []netip.Addr
	if link != "" {
		if up, _, addrs, err = h.parseChainLink(r.Context(), link); err != nil {
			return fail(err.Error())
		}
	} else if chain == nil {
		return fail("请粘贴你的节点链接")
	}
	if chain == nil {
		owner, _ := h.repo.GetUserByID(forward.UserID)
		if err := h.chainRuleLimit(owner, 0); err != nil {
			return fail(err.Error())
		}
	}
	if err := h.chains.begin(forward.UserID, roleID != 0 && link != "", now); err != nil {
		return fail(err.Error())
	}
	started := false
	defer func() {
		if !started {
			h.chains.end(forward.UserID)
		}
	}()
	if link != "" {
		if err := h.probeChainUpstream(info, up, addrs); err != nil {
			h.chains.refund(forward.UserID)
			return fail(err.Error())
		}
	}
	op := chainOp{ownerID: forward.UserID, forwardID: id, claims: claims, req: cloneReq(req), deadline: now.Add(chainCreateDeadline), holdsUser: true}
	if chain == nil {
		// Convert an existing rule into a chain rule.
		created, _, err := h.repo.CreateForwardChain(repo.ChainCreateInput{UserID: forward.UserID, TunnelID: tunnelID, Upstream: up, ExitNodeIDs: exitNodeIDs(info), InFlightAfter: now.Add(-chainStaleAfter).UnixMilli(), AttachForwardID: id})
		if err != nil {
			h.chains.refund(forward.UserID)
			return fail(err.Error())
		}
		op.kind, op.chainID = "attach", created.ID
		op.req["tunnelId"] = tunnelID
		if !h.chains.startDriver(created.ID) {
			_, _ = h.repo.FailForwardChain(created.ID, "系统繁忙")
			return fail("系统正在处理其他「我自己的节点」任务，请稍后再试")
		}
		started = true
		go h.runChainOp(op)
		response.WriteJSON(w, response.OK(chainStatusView(created)))
		return true
	}
	op.chainID = chain.ID
	kind := "relink"
	if tunnelID != forward.TunnelID {
		kind = "retarget"
	}
	if _, err := h.repo.StartChainOp(chain.ID, kind); err != nil {
		return fail(err.Error())
	}
	op.kind = kind
	finishOnError := func(msg string) bool {
		h.rollbackChainChange(op, msg)
		return fail(msg)
	}
	if link != "" {
		servers, err := h.repo.BeginChainUpstreamChange(chain.ID, up)
		if err != nil {
			_ = h.repo.FinishChainOp(chain.ID, "")
			return fail(err.Error())
		}
		op.relink = true
		_ = servers
	}
	if tunnelID != forward.TunnelID {
		plan, err := h.repo.PrepareChainRetarget(chain.ID, exitNodeIDs(info))
		if err != nil {
			return finishOnError(err.Error())
		}
		op.plan, op.tunnelID = plan, tunnelID
	}
	if !h.chains.startDriver(chain.ID) {
		return finishOnError("系统正在处理其他「我自己的节点」任务，请稍后再试")
	}
	started = true
	go h.runChainOp(op)
	latest, _ := h.repo.GetForwardChain(chain.ID)
	if latest == nil {
		latest = chain
	}
	response.WriteJSON(w, response.OK(chainStatusView(latest)))
	return true
}

func replayWithRequest(r *http.Request, target string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), chainReplayKey{}, chainReplay{target: target}))
}

// reconcileTunnelChains moves chain copies after a tunnel edit changed the
// exit tier (new exits get copies, removed exits lose them). Runs in the
// background; failures stay visible on the rule and are retried.
func (h *Handler) reconcileTunnelChains(tunnelID int64, known ...[]model.ForwardChain) []string {
	var chains []model.ForwardChain
	if len(known) > 0 {
		chains = known[0]
	} else {
		var err error
		if chains, err = h.repo.ListForwardChains(model.ChainStateActive); err != nil {
			return nil
		}
	}
	info, infoErr := h.repo.TunnelLineInfo(tunnelID)
	var warnings []string
	for _, c := range chains {
		if c.TunnelID != tunnelID || c.OpStartedAt > 0 {
			continue
		}
		copies, err := h.repo.ListForwardChainNodes(c.ID)
		if err != nil {
			continue
		}
		have := map[int64]bool{}
		for _, n := range copies {
			have[n.NodeID] = true
		}
		want := map[int64]bool{}
		if infoErr == nil {
			for _, e := range info.Exits {
				want[e.NodeID] = true
			}
		}
		same := len(have) == len(want)
		for id := range want {
			same = same && have[id]
		}
		if same {
			continue
		}
		if msg := h.startChainMove(c, info, infoErr); msg != "" {
			warnings = append(warnings, msg)
		}
	}
	return warnings
}

func (h *Handler) startChainMove(c model.ForwardChain, info *repo.TunnelLineInfo, infoErr error) string {
	reason := ""
	if infoErr != nil {
		reason = infoErr.Error()
	} else if ok, why := tunnelChainAvailability(info); !ok {
		reason = why
	}
	if reason != "" {
		msg := "隧道出口已变化，「我自己的节点」无法迁移：" + reason
		if c.LastError != msg {
			_ = h.repo.SetForwardChainError(c.ID, msg)
		}
		return "「我自己的节点」规则无法迁移到新的出口：" + reason
	}
	if _, err := h.repo.StartChainOp(c.ID, "move"); err != nil {
		return ""
	}
	op := chainOp{kind: "move", chainID: c.ID, ownerID: c.UserID, forwardID: c.ForwardID, deadline: time.Now().Add(chainCreateDeadline)}
	plan, err := h.repo.PrepareChainRetarget(c.ID, exitNodeIDs(info))
	if err != nil {
		_ = h.repo.FinishChainOp(c.ID, "隧道出口已变化，「我自己的节点」无法迁移："+err.Error())
		return "「我自己的节点」规则无法迁移到新的出口：" + err.Error()
	}
	op.plan = plan
	if !h.chains.startDriver(c.ID) {
		h.rollbackChainChange(op, "系统繁忙，稍后自动重试")
		return ""
	}
	go h.runChainOp(op)
	return "「我自己的节点」规则正在迁移到新的出口"
}

// runChainReconcileLoop: startup + every 2 minutes, one batched pass.
func (h *Handler) runChainReconcileLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			h.reconcileChains(time.Now())
			timer.Reset(chainReconcileEvery)
		}
	}
}

// reconcileChains finishes or rolls back operations left by a crash, purges
// old failed results, removes orphans and retries exit moves.
func (h *Handler) reconcileChains(now time.Time) {
	if h == nil || h.repo == nil {
		return
	}
	chains, err := h.repo.ListForwardChains()
	if err != nil {
		return
	}
	staleBefore := now.Add(-chainStaleAfter).UnixMilli()
	var resync []int64
	for _, c := range chains {
		if c.OpStartedAt == 0 || c.OpStartedAt > staleBefore || h.chains.hasDriver(c.ID) {
			continue
		}
		switch c.Op {
		case "create", "attach":
			target := chainTarget(c.Port)
			id := c.ForwardID
			if c.Op == "create" {
				id, _ = h.repo.FindChainTargetForward(c.UserID, c.TunnelID, target)
			} else if f, err := h.getForwardRecord(id); err != nil || f == nil || f.RemoteAddr != target {
				id = 0
			}
			if id > 0 && h.repo.ActivateForwardChain(c.ID, id) == nil {
				log.Printf("chain reconcile: finished %s chain=%d forward=%d", c.Op, c.ID, id)
				continue
			}
			servers, _ := h.repo.FailForwardChain(c.ID, "操作中断（面板重启或超时），已自动回滚，请重新创建")
			resync = append(resync, servers...)
			log.Printf("chain reconcile: rolled back %s chain=%d", c.Op, c.ID)
		default:
			op := chainOp{kind: c.Op, chainID: c.ID, relink: c.PrevUpstream != ""}
			if f, err := h.getForwardRecord(c.ForwardID); err == nil && f != nil && f.RemoteAddr != chainTarget(c.Port) {
				// The rule already switched to the pending copies: commit.
				if p, ok := parseLoopbackPort(f.RemoteAddr); ok {
					plan := &repo.ChainRetarget{ChainID: c.ID, OldPort: c.Port, NewPort: p}
					if err := h.repo.CommitChainRetarget(plan, f.TunnelID); err == nil {
						if op.relink {
							_ = h.repo.CommitChainUpstreamChange(c.ID)
						}
						_ = h.repo.FinishChainOp(c.ID, "")
						servers, _ := h.repo.ChainServerIDs([]int64{c.ID})
						resync = append(resync, servers...)
						continue
					}
				}
			}
			op.plan = &repo.ChainRetarget{ChainID: c.ID}
			h.rollbackChainChange(op, "操作中断（面板重启或超时），已恢复原设置")
		}
	}
	if n, err := h.repo.PurgeFailedForwardChains(now.Add(-chainFailedKeep).UnixMilli()); err == nil && n > 0 {
		log.Printf("chain reconcile: purged %d failed results", n)
	}
	if orphans, err := h.repo.FindChainOrphans(); err == nil {
		for _, o := range orphans {
			if o.Kind == "chain" {
				if c, err := h.repo.GetForwardChain(o.ID); err != nil || c == nil || c.UpdatedTime > staleBefore {
					continue
				}
			}
			servers, err := h.repo.DeleteChainOrphan(o)
			if err == nil {
				log.Printf("chain reconcile: removed orphan %s %d (%s)", o.Kind, o.ID, o.Reason)
				resync = append(resync, servers...)
			}
		}
	}
	h.syncChainServers(resync)
	// Retry exit moves of chains whose tunnel exits no longer match: one
	// pass, grouped by tunnel.
	byTunnel := map[int64][]model.ForwardChain{}
	for _, c := range chains {
		if c.State == model.ChainStateActive && c.OpStartedAt == 0 {
			byTunnel[c.TunnelID] = append(byTunnel[c.TunnelID], c)
		}
	}
	for id, list := range byTunnel {
		h.reconcileTunnelChains(id, list)
	}
}

// chainTunnelEditGuard: a tunnel carrying chain rules must keep an exit tier;
// without one the entry itself would dial the rule's 127.0.0.1 target.
func (h *Handler) chainTunnelEditGuard(tunnelID int64, req map[string]interface{}) error {
	if h == nil || h.repo == nil {
		return nil
	}
	chains, err := h.repo.ListForwardChains(model.ChainStateActive, model.ChainStateCreating)
	if err != nil {
		return nil
	}
	n := 0
	for _, c := range chains {
		if c.TunnelID == tunnelID {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	if asInt(req["type"], 1) != 2 || len(asMapSlice(req["outNodeId"])) == 0 {
		return fmt.Errorf("该隧道上有 %d 条「我自己的节点」规则，隧道必须保留出口节点；请先删除这些规则或把它们换到其他隧道", n)
	}
	return nil
}

func parseLoopbackPort(addr string) (int, bool) {
	host, p, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || host != "127.0.0.1" {
		return 0, false
	}
	port, err := strconv.Atoi(p)
	return port, err == nil && port > 0
}

// Routes ------------------------------------------------------------------

func (h *Handler) registerLineChain(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{"parse": h.forwardChainParse, "status": h.forwardChainStatus, "links": h.forwardChainLinks} {
		mux.HandleFunc("/api/v1/forward/chain/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if _, _, err := userRoleFromRequest(r); err != nil {
				response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
				return
			}
			fn(w, r)
		})
	}
}

// forwardChainParse shows the parsed summary before confirm. Credentials are
// never echoed back.
func (h *Handler) forwardChainParse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link string `json:"link"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	up, name, addrs, err := h.parseChainLink(r.Context(), req.Link)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	resolved := ""
	if len(addrs) > 0 && addrs[0].String() != up.Server {
		resolved = addrs[0].String()
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"protocol": up.Type, "protocolLabel": up.ProtocolLabel(), "host": up.Server, "port": up.Port, "summary": up.Summary(), "name": name, "udp": up.UDPOnly(), "resolved": resolved}))
}

func (h *Handler) ownedChain(r *http.Request, chain *model.ForwardChain) bool {
	uid, role, err := userRoleFromRequest(r)
	return err == nil && chain != nil && (role == 0 || chain.UserID == uid)
}

func (h *Handler) forwardChainStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        int64 `json:"id"`
		ForwardID int64 `json:"forwardId"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	var chain *model.ForwardChain
	var err error
	if req.ID > 0 {
		chain, err = h.repo.GetForwardChain(req.ID)
	} else {
		chain, err = h.repo.GetForwardChainByForward(req.ForwardID)
	}
	if err != nil || chain == nil || !h.ownedChain(r, chain) {
		response.WriteJSON(w, response.ErrDefault("「我自己的节点」规则不存在"))
		return
	}
	response.WriteJSON(w, response.OK(chainStatusView(chain)))
}

// forwardChainLinks returns the user's link per entry of the rule: VLESS
// Reality Vision to the tunnel entry address and the rule's port.
func (h *Handler) forwardChainLinks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ForwardID int64 `json:"forwardId"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	chain, err := h.repo.GetForwardChainByForward(req.ForwardID)
	if err != nil || chain == nil || !h.ownedChain(r, chain) || chain.State != model.ChainStateActive {
		response.WriteJSON(w, response.ErrDefault("「我自己的节点」规则不存在"))
		return
	}
	f, err := h.getForwardRecord(req.ForwardID)
	if err != nil || f == nil {
		response.WriteJSON(w, response.ErrDefault("规则不存在"))
		return
	}
	entries, err := h.repo.ForwardEntryAddresses(req.ForwardID)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	links := []map[string]interface{}{}
	for _, e := range entries {
		name := f.Name
		if len(entries) > 1 {
			name = f.Name + " · " + e.NodeName
		}
		links = append(links, map[string]interface{}{"name": name, "url": repo.ChainLinkURL(*chain, e.Host, e.Port, name), "entryNodeId": e.NodeID, "entryNodeName": e.NodeName})
	}
	response.WriteJSON(w, response.OK(links))
}

// idemCache dedupes normal rule creates by submit key for 10 minutes: a
// double click or a client retry returns the first answer instead of
// creating a second rule (or a second auto-created landing node).
type idemCache struct {
	mu      sync.Mutex
	entries map[string]*idemEntry
}

type idemEntry struct {
	at   time.Time
	done bool
	body []byte
}

const idemTTL = 10 * time.Minute

func (c *idemCache) start(key string, now time.Time) (*idemEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*idemEntry{}
	}
	if len(c.entries) > 2000 {
		for k, e := range c.entries {
			if now.Sub(e.at) > idemTTL {
				delete(c.entries, k)
			}
		}
	}
	if e, ok := c.entries[key]; ok && now.Sub(e.at) <= idemTTL {
		return e, false
	}
	e := &idemEntry{at: now}
	c.entries[key] = e
	return e, true
}

func (c *idemCache) finish(key string, body []byte, keep bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		if !keep {
			delete(c.entries, key)
			return
		}
		e.done, e.body = true, body
	}
}

func (c *idemCache) result(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && e.done {
		return e.body, true
	}
	return nil, false
}

func (h *Handler) forwardCreateIdempotent(w http.ResponseWriter, r *http.Request, req map[string]interface{}, key string) {
	uid, _, err := userRoleFromRequest(r)
	if err != nil || !idemKeyRe.MatchString(key) {
		h.forwardCreateWithReq(w, r, req)
		return
	}
	full := strconv.FormatInt(uid, 10) + ":" + key
	if _, fresh := h.createIdem.start(full, time.Now()); !fresh {
		if body, ok := h.createIdem.result(full); ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(body)
			return
		}
		response.WriteJSON(w, response.ErrDefault("正在创建，请勿重复提交"))
		return
	}
	cw := &captureWriter{}
	h.forwardCreateWithReq(cw, r, req)
	code, _, _ := cw.envelope()
	// Only successful creates are replayed; a failed submit may be retried.
	h.createIdem.finish(full, cw.body.Bytes(), code == 0)
	cw.flushTo(w)
}

// chainDiagnosisTargets swaps a chain rule's loopback target for the user's
// node, so the final step reads 出口(X)->目标(用户节点).
func (h *Handler) chainDiagnosisTargets(forward *forwardRecord) ([]diagnosisTarget, bool) {
	if forward == nil || h.repo == nil {
		return nil, false
	}
	chain, err := h.repo.GetForwardChainByForward(forward.ID)
	if err != nil || chain == nil || chain.State != model.ChainStateActive || chain.UpstreamHost == "" || chain.UpstreamPort <= 0 {
		return nil, false
	}
	addr := net.JoinHostPort(chain.UpstreamHost, strconv.Itoa(chain.UpstreamPort))
	targets, err := resolveDiagnosisTargets(addr)
	if err != nil {
		return nil, false
	}
	return targets, true
}

// markChainUDPDiagnosis: UDP-only upstreams (Hysteria2) cannot be probed with
// TCP; their exit→target step is reported as skipped instead of failed.
func (h *Handler) markChainUDPDiagnosis(forward *forwardRecord, items []diagnosisWorkItem) {
	if forward == nil || h.repo == nil {
		return
	}
	chain, err := h.repo.GetForwardChainByForward(forward.ID)
	if err != nil || chain == nil || chain.Upstream == "" {
		return
	}
	up, err := linechain.FromJSON(chain.Upstream)
	if err != nil || !up.UDPOnly() {
		return
	}
	for i := range items {
		if !items[i].hasChainHop && items[i].targetPort > 0 && asInt(items[i].metadata["fromChainType"], 0) == 3 {
			if items[i].metadata == nil {
				items[i].metadata = map[string]interface{}{}
			}
			items[i].metadata["chainUdpOnly"] = up.ProtocolLabel()
		}
	}
}

// chainDeleteGuard refuses a normal delete while a chain operation of the
// rule is running (force-delete still works; the driver then rolls back).
func (h *Handler) chainDeleteGuard(forwardID int64) error {
	if h == nil || h.repo == nil {
		return nil
	}
	if c, err := h.repo.GetForwardChainByForward(forwardID); err == nil && c != nil && c.OpStartedAt > 0 {
		return errors.New("该规则的「我自己的节点」正在处理中，请稍候再删除")
	}
	return nil
}

// syncChainServersAsync pushes exits after a delete without delaying the
// answer; failures converge through the minute loop.
func (h *Handler) syncChainServersAsync(ids []int64) {
	if len(ids) == 0 || h == nil || h.repo == nil {
		return
	}
	h.chains.wg.Add(1)
	go func() {
		defer h.chains.wg.Done()
		h.syncChainServers(ids)
	}()
}
