package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/tgbot"
)

// Plan 202 「禁止端口扫描」 (FLVX-native landing nodes; plan 205: also mmwx
// shares advertising landing-scan-guard-v1 §9, same payload and records): the
// 内置 row (admin switch + thresholds, read-only for users), the payload, the
// reported hits (node disabled at once, events, admin notice) and 解除禁用.

// landingScanReport is the agent's Status.scan_guard.
type landingScanReport struct {
	State   string                `json:"state"`
	Error   string                `json:"error"`
	Hits    []repo.LandingScanHit `json:"hits"`
	Blocked []string              `json:"blocked"`
}

// landingScanNotifier rate-limits the admin Telegram notices: at most one
// message per minute; disables arriving meanwhile go into the next message.
type landingScanNotifier struct {
	mu      sync.Mutex
	pending []string
	last    time.Time
	timer   *time.Timer
}

const landingScanNoticeGap = time.Minute

func (h *Handler) registerLandingScan(mux *http.ServeMux) {
	routes := map[string]http.HandlerFunc{
		"scan/get": h.landingScanGet, "scan/save": h.landingScanSave, "scan/events": h.landingScanEvents,
		"node/scan/unblock":  h.limitMutation(mutationLanding, h.landingScanUnblock),
		"scan/event/unblock": h.limitMutation(mutationLanding, h.landingScanEventUnblock),
	}
	for path, fn := range routes {
		fn := fn
		mux.HandleFunc("/api/v1/landing/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if !h.ensureAdminAccess(w, r) {
				return
			}
			fn(w, r)
		})
	}
}

// landingScanPayload is the desired scan_guard of a native server for an
// agent with landing_scan_guard: the thresholds, the guarded node tags (all
// SS2022 / VLESS nodes of the server while the rule is on) and the highest
// hit seq the panel recorded. Always sent to such agents, so a switched-off
// rule still lets the agent hand over and release its pending hits.
func (h *Handler) landingScanPayload(s *model.LandingServer, nodes []model.LandingNode) (map[string]interface{}, error) {
	g, err := h.repo.GetLandingScanGuard()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	if g.On {
		ids = repo.NativeLandingScanTargets(nodes, s.ID, landingcustom.IsExtraProtocol)
	}
	return repo.LandingScanPayload(g, ids, s.ScanSeenSeq), nil
}

// landingScanSig joins the payload into the revision signature.
func landingScanSig(payload map[string]interface{}) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return ";sg=" + hex.EncodeToString(sum[:8])
}

// sharedScanReport turns a share's relayed report (contract §9.3, node ids
// as strings) into the native agent's report shape.
func sharedScanReport(g *landingcustom.ScanGuardReport) *landingScanReport {
	if g == nil {
		return nil
	}
	rep := &landingScanReport{State: g.State, Error: g.Error, Hits: make([]repo.LandingScanHit, 0, len(g.Hits)), Blocked: g.Blocked}
	for _, hit := range g.Hits {
		id, _ := strconv.ParseInt(strings.TrimPrefix(hit.InboundTag, "flvxl-"), 10, 64)
		rep.Hits = append(rep.Hits, repo.LandingScanHit{ID: id, InboundTag: hit.InboundTag, Type: hit.Type, Count: hit.Count, Port: hit.Port, Host: hit.Host, WindowS: hit.WindowS, At: hit.At, Seq: hit.Seq})
	}
	return rep
}

// recordNativeScan stores the reported guard state and the new hits of a
// native agent or (plan 205) a share's status poll; true = the seen mark
// moved (push again so the agent / Helper drops them and gets the disabled
// nodes).
func (h *Handler) recordNativeScan(s *model.LandingServer, rep *landingScanReport) bool {
	if rep == nil {
		return false
	}
	state := rep.State
	if len(state) > 32 {
		state = state[:32]
	}
	notices, advanced, err := h.repo.RecordLandingScanStatus(s.ID, state, rep.Hits, time.Now())
	if err != nil {
		log.Printf("[landing-scan] server=%d hits not recorded: %v", s.ID, err)
		return false
	}
	if len(notices) > 0 {
		h.notifyLandingScan(notices)
		// Forwards using a disabled node pause like for any disabled node
		// (asynchronously: the policy pass syncs servers itself).
		h.egressSyncWG.Add(1)
		go func() {
			defer h.egressSyncWG.Done()
			h.reconcileLandingPolicy(time.Now())
		}()
	}
	return advanced
}

// onLandingScan (ws hook): the agent reports a scan hit the panel has not
// recorded; sync its native landing server now instead of within a minute.
func (h *Handler) onLandingScan(nodeID int64) {
	if h == nil || h.repo == nil {
		return
	}
	serverID, err := h.repo.NativeLandingServerIDForNode(nodeID)
	if err != nil || serverID <= 0 {
		return
	}
	h.egressSyncWG.Add(1)
	go func() {
		defer h.egressSyncWG.Done()
		_, _ = h.syncLandingServer(serverID)
	}()
}

// notifyLandingScan logs one line per disabled node and queues the admin
// Telegram notice (only when a bot token and a chat are set).
func (h *Handler) notifyLandingScan(notices []repo.LandingScanNotice) {
	userIDs := make([]int64, 0, len(notices))
	for _, n := range notices {
		userIDs = append(userIDs, n.UserID)
	}
	names, _ := h.repo.UserNamesByIDs(userIDs)
	lines := []string{}
	for _, n := range notices {
		reason := n.Block.Reason()
		log.Printf("[landing-scan] node=%d server=%d user=%d disabled: %s", n.NodeID, n.ServerID, n.UserID, reason)
		user := names[n.UserID]
		if user == "" {
			user = fmt.Sprintf("#%d", n.UserID)
		}
		lines = append(lines, fmt.Sprintf("· %s（用户 %s，服务器 %s）\n  %s，%s", n.NodeName, user, n.ServerName, reason, landingegress.ScanTime(n.Block.At)))
	}
	st := &h.landingScanNote
	st.mu.Lock()
	defer st.mu.Unlock()
	st.pending = append(st.pending, lines...)
	if st.timer != nil {
		return // a flush is scheduled and takes these too
	}
	wait := time.Until(st.last.Add(landingScanNoticeGap))
	if wait < 0 {
		wait = 0
	}
	// Not tracked by egressSyncWG: a waiting notice must not hold syncs up.
	st.timer = time.AfterFunc(wait, h.flushLandingScanNotice)
}

func (h *Handler) flushLandingScanNotice() {
	st := &h.landingScanNote
	st.mu.Lock()
	lines := st.pending
	st.pending, st.timer, st.last = nil, nil, time.Now()
	st.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	text := landingScanNoticeText(lines)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := h.tgBot().SendNotice(ctx, text); err != nil && !errors.Is(err, tgbot.ErrNoToken) && !errors.Is(err, tgbot.ErrNoChats) {
		log.Printf("[landing-scan] Telegram notice not sent: %v", err)
	}
}

// landingScanNoticeText: at most 10 nodes per message, the rest counted.
func landingScanNoticeText(lines []string) string {
	head := fmt.Sprintf("⚠️ %d 个落地节点因端口扫描已被禁用", len(lines))
	if len(lines) == 1 {
		head = "⚠️ 落地节点因端口扫描已被禁用"
	}
	shown := lines
	if len(shown) > 10 {
		shown = shown[:10]
	}
	text := head + "\n" + strings.Join(shown, "\n")
	if len(lines) > len(shown) {
		text += fmt.Sprintf("\n…另有 %d 个", len(lines)-len(shown))
	}
	return text + "\n在面板「落地」页的节点卡片上点「解除禁用」可恢复。"
}

// landingScanServerState: whether the rule works on a server, as a key and
// the label shown next to the row.
func landingScanServerState(s *model.LandingServer, agent *model.Node, on bool) (string, string) {
	switch {
	case s == nil:
		return "pending", "未生效：落地服务器状态未知"
	case s.Source == "shared":
		return landingScanSharedState(s, on, -1)
	case !on:
		return "off", "未生效：全局已关闭"
	case agent == nil || agent.LandingScanGuard < repo.LandingScanGuardVersion:
		return "agent", "未生效：需要升级节点"
	case s.ScanState == "core_update":
		return "core", "未生效：落地 Core 需要重启一次（服务器卡片上确认升级 Core）"
	case s.ScanState == "error":
		return "error", "检测异常：读取落地 Core 的扫描记录失败"
	case s.ScanState == "on":
		return "on", "生效中"
	}
	return "pending", "未生效：等待同步到落地服务器"
}

// landingScanSharedState (plan 205): a share without landing-scan-guard-v1
// keeps 共享服务器暂不支持; otherwise the Helper's relayed state. nodes = the
// share's node count (-1 unknown): without nodes nothing is guarded yet.
func landingScanSharedState(s *model.LandingServer, on bool, nodes int) (string, string) {
	switch {
	case !repo.SharedScanGuardSupported(s):
		return "shared", "共享服务器暂不支持"
	case !on:
		return "off", "未生效：全局已关闭"
	case s.ScanState == "core_update":
		return "shared_core", "未生效：共享服务器的 FLVX Core 需要升级（妙妙屋端处理）"
	case s.ScanState == "error":
		return "error", "检测异常：读取共享服务器的扫描记录失败"
	case s.ScanState == "on":
		return "on", "生效中"
	case nodes == 0:
		return "idle", "暂无节点：新建节点后自动生效"
	}
	return "pending", "未生效：等待同步到共享服务器"
}

// landingScanSettingsView is the switch + thresholds part of the views.
func landingScanSettingsView(g landingegress.ScanGuard) map[string]any {
	return map[string]any{"on": g.On, "horizontal": g.Horizontal, "horizontalWeb": g.HorizontalWeb, "vertical": g.Vertical, "total": g.Total,
		"windowS": landingegress.ScanWindowS, "totalWindowS": landingegress.ScanTotalWindowS, "webPorts": landingegress.ScanWebPorts}
}

// landingScanAdminView: settings + per-server state + counts (admin 内置).
func (h *Handler) landingScanAdminView() (map[string]any, error) {
	g, err := h.repo.GetLandingScanGuard()
	if err != nil {
		return nil, err
	}
	view := landingScanSettingsView(g)
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		return nil, err
	}
	sharedNodes := map[int64]int{}
	for i := range servers {
		if servers[i].Source == "shared" {
			nodes, err := h.repo.ListLandingNodes(0, true)
			if err != nil {
				return nil, err
			}
			for _, n := range nodes {
				if !n.DeletePending {
					sharedNodes[n.LandingServerID]++
				}
			}
			break
		}
	}
	list := []map[string]any{}
	for i := range servers {
		s := &servers[i]
		var agent *model.Node
		if s.Source == "native" {
			agent, _ = h.repo.GetNodeByID(s.NodeID)
		}
		state, label := landingScanServerState(s, agent, g.On)
		if s.Source == "shared" {
			state, label = landingScanSharedState(s, g.On, sharedNodes[s.ID])
		}
		list = append(list, map[string]any{"id": s.ID, "name": s.Name, "source": s.Source, "state": state, "label": label})
	}
	view["servers"] = list
	blocked, events, err := h.repo.LandingScanCounts()
	if err != nil {
		return nil, err
	}
	view["blockedNodes"], view["events"] = blocked, events
	// Plan 205: blocked nodes deleted before 解除禁用 (owners cannot create).
	deletedOpen, err := h.repo.LandingScanDeletedOpen()
	if err != nil {
		return nil, err
	}
	view["deletedBlocked"] = deletedOpen
	return view, nil
}

// landingScanNodeView: the read-only row of one node's 内置 list.
func (h *Handler) landingScanNodeView(s *model.LandingServer) map[string]any {
	g, err := h.repo.GetLandingScanGuard()
	if err != nil {
		g = landingegress.ScanGuardDefaults()
	}
	var agent *model.Node
	if s != nil && s.Source == "native" {
		agent, _ = h.repo.GetNodeByID(s.NodeID)
	}
	state, label := landingScanServerState(s, agent, g.On)
	view := landingScanSettingsView(g)
	view["state"], view["label"] = state, label
	return view
}

// landingScanBlockView is the reason on a disabled node (node list / card).
func landingScanBlockView(n *model.LandingNode) map[string]any {
	b := repo.LandingScanBlockOf(n)
	if b == nil {
		return nil
	}
	reason := b.Reason()
	return map[string]any{"type": b.Type, "typeLabel": landingegress.ScanTypeLabel(b.Type), "count": b.Count, "port": b.Port, "host": b.Host,
		"windowS": b.WindowS, "at": b.At, "blockedAt": n.ScanBlockedAt, "reason": reason, "message": landingegress.ScanUserMessage(reason)}
}

func (h *Handler) landingScanGet(w http.ResponseWriter, r *http.Request) {
	view, err := h.landingScanAdminView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

// landingScanSave stores the switch and thresholds and pushes the native
// servers once (the same debounced path as other 内置 switches).
func (h *Handler) landingScanSave(w http.ResponseWriter, r *http.Request) {
	var req landingegress.ScanGuard
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := req.Validate(); err != nil {
		landingError(w, err)
		return
	}
	before, err := h.repo.GetLandingScanGuard()
	if err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.SaveLandingScanGuard(req); err != nil {
		landingError(w, err)
		return
	}
	if req != before {
		servers, err := h.repo.ListLandingServers(0, true)
		if err != nil {
			landingError(w, err)
			return
		}
		native, shared := []int64{}, 0
		for _, s := range servers {
			if s.Source == "native" {
				native = append(native, s.ID)
			} else if repo.SharedScanGuardSupported(&s) {
				shared++
			}
		}
		_, by := h.landingActorName(r)
		log.Printf("[landing-scan] settings by=%q on=%t horizontal=%d web=%d vertical=%d total=%d servers=%d shared=%d", by, req.On, req.Horizontal, req.HorizontalWeb, req.Vertical, req.Total, len(native), shared)
		// Shares pick the change up on the shared loop's next pass (≤ 5 s):
		// their desired content (and revision) follows the setting.
		h.scheduleLandingLayerApply(native)
	}
	view, err := h.landingScanAdminView()
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(view))
}

func (h *Handler) landingScanEvents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID int64 `json:"nodeId"`
		Limit  int   `json:"limit"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	events, total, err := h.repo.ListLandingScanEvents(req.NodeID, req.Limit)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"events": events, "total": total}))
}

// landingActorName: the admin's login name for the event log.
func (h *Handler) landingActorName(r *http.Request) (int64, string) {
	uid, _, err := userRoleFromRequest(r)
	if err != nil {
		return 0, ""
	}
	if u, _ := h.repo.GetUserByID(uid); u != nil {
		if u.User != "" {
			return uid, u.User
		}
		return uid, u.Name
	}
	return uid, fmt.Sprintf("#%d", uid)
}

// landingScanUnblock is 「解除禁用」: the node goes back to normal (the policy
// pass then applies quota / expiry as usual), the event records who lifted
// it, and the server is pushed so the agent lifts the Core's block.
func (h *Handler) landingScanUnblock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	_, by := h.landingActorName(r)
	n, err := h.repo.LiftLandingScanBlock(req.ID, by, true, time.Now())
	if err != nil {
		landingError(w, err)
		return
	}
	log.Printf("[landing-scan] node=%d unblocked by=%q", n.ID, by)
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(n.LandingServerID)
	if cur, err := h.repo.GetLandingNode(n.ID); err == nil && cur != nil {
		n = cur
	}
	response.WriteJSON(w, response.OK(h.landingNodeView(*n)))
}

// landingScanEventUnblock is 「解除禁用」 on a row of the event list (plan
// 205): a node still disabled is lifted like its own button; for a node the
// owner deleted meanwhile the record closes, so the owner may create again.
func (h *Handler) landingScanEventUnblock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	_, by := h.landingActorName(r)
	n, err := h.repo.LiftLandingScanEvent(req.ID, by, time.Now())
	if err != nil {
		landingError(w, err)
		return
	}
	if n == nil {
		log.Printf("[landing-scan] event=%d (node deleted) unblocked by=%q", req.ID, by)
		response.WriteJSON(w, response.OK(map[string]any{"nodeId": 0}))
		return
	}
	log.Printf("[landing-scan] node=%d unblocked by=%q (event %d)", n.ID, by, req.ID)
	h.reconcileLandingPolicy(time.Now())
	h.syncLandingServer(n.LandingServerID)
	response.WriteJSON(w, response.OK(map[string]any{"nodeId": n.ID}))
}
