package ws

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/auth"
	"go-backend/internal/authstate"
	"go-backend/internal/clientip"
	"go-backend/internal/http/middleware"
)

// Browser sessions on /system-info (plan 135).
//
//   - type=0 logged-in session: admins (role 0) get the full admin view;
//     normal users only nodes of their monitor scope, node-level fields and
//     their own forward metrics.
//   - type=2 public status page: only while 「登录页显示监控链接」 is on, node
//     metrics reduced to the public fields of /api/v1/monitor/public/nodes.
//
// Node command replies are never broadcast; only metric / status /
// upgrade_progress (admins) / panel_upgrade_progress (admins) reach clients.
// Every client has its own small send queue drained by one writer goroutine;
// the node read loop only does non-blocking enqueues, so a client that stops
// reading is dropped instead of stalling node replies.

type clientKind int

const (
	kindNode clientKind = iota
	kindAdmin
	kindUser
	kindPublic
)

const (
	clientQueueSize       = 256 // ~8 s of the busiest admin stream
	publicQueueSize       = 64
	clientReadLimit       = 4096
	publicSwitchCacheTTL  = 10 * time.Second
	maxAnonPerIP          = 4
	maxAnonTotal          = 64
	maxSessionsPerUser    = 8
	refusalReasonOrigin   = "origin"
	refusalReasonSwitch   = "public_off"
	refusalReasonAnonIP   = "anon_per_ip"
	refusalReasonAnonAll  = "anon_total"
	refusalReasonUserCap  = "per_user"
	refusalReasonToken    = "token"
	refusalReasonSlow     = "slow_client"
	refusalReasonRevoked  = "revoked"
	publicSwitchConfigKey = "login_monitor_link"
)

// clientRecheckEvery: how often a long-lived browser session is re-validated
// (var for tests).
var clientRecheckEvery = time.Minute

// NodeScope is the set of nodes a normal user may see (monitor permission).
type NodeScope struct {
	All   bool
	Nodes map[int64]struct{}
}

func (sc *NodeScope) has(nodeID int64) bool {
	if sc == nil {
		return false
	}
	if sc.All {
		return true
	}
	_, ok := sc.Nodes[nodeID]
	return ok
}

// Public status fields: the same set /api/v1/monitor/public/nodes/metrics
// serves (and the /tz page reads).
var publicMetricFields = []string{
	"cpu_usage", "memory_usage", "disk_usage", "net_in_speed", "net_out_speed",
	"bytes_received", "bytes_transmitted", "uptime", "tcp_conns", "load1",
}

// Node-level fields a normal user's monitor view reads; forward_metrics are
// added per user (own rules only).
var userMetricFields = []string{
	"uptime", "bytes_received", "bytes_transmitted", "period_bytes_received",
	"period_bytes_transmitted", "baseline_recorded_at", "next_reset_at", "renewal_cycle",
	"cpu_usage", "memory_usage", "disk_usage", "load1", "load5", "load15",
	"tcp_conns", "udp_conns", "net_in_speed", "net_out_speed",
}

// Fields the admin node list and monitor views read (node.tsx,
// monitor-view.tsx): the user set plus the whole-machine sample. Per-rule
// forward_metrics / serviceConnections / landing capability flags are not
// rendered from the socket and were most of the bytes.
var adminMetricFields = append(append([]string{}, userMetricFields...), "machine_conns", "machine_protect")

type clientStats struct {
	sentMsgs   atomic.Int64
	sentBytes  atomic.Int64
	slowClosed atomic.Int64
}

type clientRegistry struct {
	mu        sync.Mutex
	set       map[*connWrap]struct{}
	list      atomic.Pointer[[]*connWrap]
	anonByIP  map[string]int
	anonTotal int
	byUser    map[int64]int
}

func (s *Server) snapshotClients() []*connWrap {
	if p := s.clients.list.Load(); p != nil {
		return *p
	}
	return nil
}

func (reg *clientRegistry) rebuildLocked() {
	list := make([]*connWrap, 0, len(reg.set))
	for c := range reg.set {
		list = append(list, c)
	}
	reg.list.Store(&list)
}

// acquire reserves a connection slot; it returns "" or the refusal reason.
func (reg *clientRegistry) acquire(kind clientKind, userID int64, ipKey string) string {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.anonByIP == nil {
		reg.anonByIP = make(map[string]int)
		reg.byUser = make(map[int64]int)
	}
	if kind == kindPublic {
		if reg.anonTotal >= maxAnonTotal {
			return refusalReasonAnonAll
		}
		if reg.anonByIP[ipKey] >= maxAnonPerIP {
			return refusalReasonAnonIP
		}
		reg.anonTotal++
		reg.anonByIP[ipKey]++
		return ""
	}
	if reg.byUser[userID] >= maxSessionsPerUser {
		return refusalReasonUserCap
	}
	reg.byUser[userID]++
	return ""
}

func (reg *clientRegistry) release(kind clientKind, userID int64, ipKey string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if kind == kindPublic {
		reg.anonTotal--
		if reg.anonByIP[ipKey] <= 1 {
			delete(reg.anonByIP, ipKey)
		} else {
			reg.anonByIP[ipKey]--
		}
		return
	}
	if reg.byUser[userID] <= 1 {
		delete(reg.byUser, userID)
	} else {
		reg.byUser[userID]--
	}
}

func (reg *clientRegistry) add(c *connWrap) {
	reg.mu.Lock()
	if reg.set == nil {
		reg.set = make(map[*connWrap]struct{})
	}
	reg.set[c] = struct{}{}
	reg.rebuildLocked()
	reg.mu.Unlock()
}

func (reg *clientRegistry) remove(c *connWrap) {
	reg.mu.Lock()
	delete(reg.set, c)
	reg.rebuildLocked()
	reg.mu.Unlock()
}

// count reports registered clients of kind (tests).
func (s *Server) countClients(kind clientKind) int {
	n := 0
	for _, c := range s.snapshotClients() {
		if c.kind == kind {
			n++
		}
	}
	return n
}

// SetUserState wires the shared disabled/deleted-user check.
func (s *Server) SetUserState(c *authstate.Cache) {
	s.mu.Lock()
	s.userState = c
	s.mu.Unlock()
}

// SetUserScopeResolver wires the monitor-scope lookup for normal users. Without
// it normal users see no node data at all.
func (s *Server) SetUserScopeResolver(fn func(userID int64) (NodeScope, error)) {
	s.mu.Lock()
	s.scopeFn = fn
	s.mu.Unlock()
}

func (s *Server) resolveScope(userID int64) *NodeScope {
	s.mu.RLock()
	fn := s.scopeFn
	s.mu.RUnlock()
	if fn == nil {
		return &NodeScope{}
	}
	sc, err := fn(userID)
	if err != nil {
		return nil // keep the previous scope; retried at the next recheck
	}
	return &sc
}

func (s *Server) checkUser(userID int64, role int) error {
	s.mu.RLock()
	us := s.userState
	s.mu.RUnlock()
	return us.Check(userID, role)
}

// publicEnabled reads the 「登录页显示监控链接」 switch (cached 10 s).
func (s *Server) publicEnabled() bool {
	now := time.Now()
	s.publicMu.Lock()
	defer s.publicMu.Unlock()
	if !s.publicAt.IsZero() && now.Sub(s.publicAt) < publicSwitchCacheTTL {
		return s.publicOn
	}
	on := false
	if s.repo != nil {
		if cfg, err := s.repo.GetConfigByName(publicSwitchConfigKey); err == nil && cfg != nil {
			on = strings.TrimSpace(cfg.Value) == "true"
		}
	}
	s.publicOn, s.publicAt = on, now
	return on
}

func (s *Server) resetPublicSwitchCache() {
	s.publicMu.Lock()
	s.publicAt = time.Time{}
	s.publicMu.Unlock()
}

// originAllowed: browsers always send Origin on WebSocket handshakes; it must
// name this panel's host (Host / X-Forwarded-Host as seen through our
// proxies) or one listed in FLVX_WS_ALLOWED_ORIGINS. Non-browser clients send
// no Origin and still need a token (type=0) or the public switch (type=2).
func (s *Server) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	oh := strings.ToLower(u.Hostname())
	if oh == hostOnly(r.Host) {
		return true
	}
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		if oh == hostOnly(strings.Split(xfh, ",")[0]) {
			return true
		}
	}
	for _, h := range s.extraOrigins {
		if oh == h {
			return true
		}
	}
	return false
}

func parseExtraOrigins() []string {
	raw := os.Getenv("FLVX_WS_ALLOWED_ORIGINS")
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if u, err := url.Parse(part); err == nil && u.Host != "" {
			part = u.Hostname()
		}
		out = append(out, strings.ToLower(hostOnly(part)))
	}
	return out
}

func hostOnly(hostport string) string {
	h := strings.TrimSpace(hostport)
	if h == "" {
		return ""
	}
	if u, err := url.Parse("//" + h); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return strings.ToLower(h)
}

// refuse answers a WebSocket handshake without upgrading and without an
// access-log line; refusals are only counted (one summary line per minute).
func (s *Server) refuse(w http.ResponseWriter, code int, reason string) {
	middleware.SuppressRequestLog(w)
	s.refusals.Note(reason)
	http.Error(w, http.StatusText(code), code)
}

func (s *Server) serveBrowser(w http.ResponseWriter, r *http.Request, typeVal, secret string) {
	if !s.originAllowed(r) {
		s.refuse(w, http.StatusForbidden, refusalReasonOrigin)
		return
	}
	kind := kindPublic
	var userID int64
	role := -1
	if typeVal == "0" {
		if secret == "" {
			if cookie, err := r.Cookie(middleware.SessionCookieName); err == nil {
				secret = strings.TrimSpace(cookie.Value)
			}
		}
		if strings.HasPrefix(strings.ToLower(secret), "bearer ") {
			secret = strings.TrimSpace(secret[7:])
		}
		claims, ok := auth.ValidateToken(secret, s.jwtSecret)
		if !ok {
			s.refuse(w, http.StatusForbidden, refusalReasonToken)
			return
		}
		uid, err := strconv.ParseInt(claims.Sub, 10, 64)
		if err != nil || uid <= 0 {
			s.refuse(w, http.StatusForbidden, refusalReasonToken)
			return
		}
		if err := s.checkUser(uid, claims.RoleID); err != nil {
			s.refuse(w, http.StatusForbidden, refusalReasonRevoked)
			return
		}
		userID, role = uid, claims.RoleID
		kind = kindUser
		if claims.RoleID == 0 {
			kind = kindAdmin
		}
	} else if !s.publicEnabled() {
		s.refuse(w, http.StatusForbidden, refusalReasonSwitch)
		return
	}

	ipKey := ""
	if kind == kindPublic {
		ipKey = clientip.LimitKey(r)
	}
	if reason := s.clients.acquire(kind, userID, ipKey); reason != "" {
		s.refuse(w, http.StatusTooManyRequests, reason)
		return
	}
	defer s.clients.release(kind, userID, ipKey)

	var scope *NodeScope
	if kind == kindUser {
		if scope = s.resolveScope(userID); scope == nil {
			scope = &NodeScope{}
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	size := clientQueueSize
	if kind == kindPublic {
		size = publicQueueSize
	}
	cw := &connWrap{
		conn:   conn,
		admin:  kind == kindAdmin,
		kind:   kind,
		userID: userID,
		role:   role,
		send:   make(chan []byte, size),
		done:   make(chan struct{}),
	}
	if scope != nil {
		cw.scope.Store(scope)
	}
	conn.SetReadLimit(clientReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	s.clients.add(cw)
	go s.clientWriter(cw)
	defer func() {
		s.clients.remove(cw)
		cw.close()
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (cw *connWrap) close() {
	cw.closeOnce.Do(func() {
		close(cw.done)
		_ = cw.conn.Close()
	})
}

// enqueue never blocks: a client whose queue is full is dropped (its page
// reconnects and reloads state over REST).
func (s *Server) enqueue(cw *connWrap, msg []byte) {
	select {
	case <-cw.done:
		return
	default:
	}
	select {
	case cw.send <- msg:
	default:
		s.stats.slowClosed.Add(1)
		s.refusals.Note(refusalReasonSlow)
		cw.close()
	}
}

func (s *Server) clientWriter(cw *connWrap) {
	ping := time.NewTicker(wsPingPeriod)
	defer ping.Stop()
	recheck := time.NewTicker(clientRecheckEvery)
	defer recheck.Stop()
	for {
		select {
		case <-cw.done:
			return
		case msg := <-cw.send:
			_ = cw.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := cw.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				cw.close()
				return
			}
			s.stats.sentMsgs.Add(1)
			s.stats.sentBytes.Add(int64(len(msg)))
		case <-ping.C:
			if err := cw.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				cw.close()
				return
			}
		case <-recheck.C:
			if !s.stillAllowed(cw) {
				_ = cw.conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, ""), time.Now().Add(time.Second))
				s.refusals.Note(refusalReasonRevoked)
				cw.close()
				return
			}
		}
	}
}

// stillAllowed re-validates a long-lived session once a minute: disabled or
// deleted users and a switched-off public page lose their stream; a normal
// user's node scope follows permission / tunnel changes.
func (s *Server) stillAllowed(cw *connWrap) bool {
	switch cw.kind {
	case kindPublic:
		return s.publicEnabled()
	case kindAdmin, kindUser:
		if err := s.checkUser(cw.userID, cw.role); err != nil {
			return false
		}
		if cw.kind == kindUser {
			if sc := s.resolveScope(cw.userID); sc != nil {
				cw.scope.Store(sc)
			}
		}
	}
	return true
}

func (cw *connWrap) canSee(nodeID int64) bool {
	switch cw.kind {
	case kindAdmin, kindPublic:
		return true
	case kindUser:
		return cw.scope.Load().has(nodeID)
	}
	return false
}

func marshalBroadcast(nodeID int64, msgType, data string) []byte {
	raw, _ := json.Marshal(broadcastMessage{ID: nodeID, Type: msgType, Data: data})
	return raw
}

// broadcastStatus: node online/offline to everybody allowed to see the node.
func (s *Server) broadcastStatus(nodeID int64, status int) {
	clients := s.snapshotClients()
	if len(clients) == 0 {
		return
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"id":   strconv.FormatInt(nodeID, 10),
		"type": "status",
		"data": status,
	})
	for _, c := range clients {
		if c.canSee(nodeID) {
			s.enqueue(c, raw)
		}
	}
}

// broadcastAdminTyped sends a node message (e.g. upgrade_progress) to admins.
func (s *Server) broadcastAdminTyped(nodeID int64, msgType, data string) {
	var raw []byte
	for _, c := range s.snapshotClients() {
		if c.kind != kindAdmin {
			continue
		}
		if raw == nil {
			raw = marshalBroadcast(nodeID, msgType, data)
		}
		s.enqueue(c, raw)
	}
}

// BroadcastToAdmins sends a panel-level message (panel upgrade progress) to
// admin sessions only.
func (s *Server) BroadcastToAdmins(message string) {
	raw := []byte(message)
	for _, c := range s.snapshotClients() {
		if c.kind == kindAdmin {
			s.enqueue(c, raw)
		}
	}
}

// broadcastMetric builds each variant only if a client needs it, so an idle
// panel (no browser open) does no JSON work for the 1 Hz node metrics.
func (s *Server) broadcastMetric(nodeID int64, data []byte, fwd []ForwardMetric) {
	clients := s.snapshotClients()
	if len(clients) == 0 {
		return
	}
	var (
		fields              map[string]json.RawMessage
		parsed, parseOK     bool
		adminMsg, publicMsg []byte
		userBase            map[string]json.RawMessage
		userBaseMsg         []byte
		ownByUser           map[int64][]ForwardMetric
	)
	parse := func() bool {
		if !parsed {
			parsed = true
			parseOK = json.Unmarshal(data, &fields) == nil
		}
		return parseOK
	}
	for _, c := range clients {
		switch c.kind {
		case kindAdmin:
			if adminMsg == nil {
				if !parse() {
					continue
				}
				adminMsg = marshalBroadcast(nodeID, "metric", pickFields(fields, adminMetricFields, nil))
			}
			s.enqueue(c, adminMsg)
		case kindPublic:
			if publicMsg == nil {
				if !parse() {
					continue
				}
				publicMsg = marshalBroadcast(nodeID, "metric", pickFields(fields, publicMetricFields, nil))
			}
			s.enqueue(c, publicMsg)
		case kindUser:
			if !c.canSee(nodeID) || !parse() {
				continue
			}
			if userBase == nil {
				userBase = make(map[string]json.RawMessage, len(userMetricFields)+1)
				for _, k := range userMetricFields {
					if v, ok := fields[k]; ok {
						userBase[k] = v
					}
				}
				ownByUser = make(map[int64][]ForwardMetric)
				for _, fm := range fwd {
					ownByUser[fm.UserID] = append(ownByUser[fm.UserID], fm)
				}
			}
			own := ownByUser[c.userID]
			if len(own) == 0 {
				if userBaseMsg == nil {
					userBaseMsg = marshalBroadcast(nodeID, "metric", pickFields(userBase, userMetricFields, nil))
				}
				s.enqueue(c, userBaseMsg)
				continue
			}
			ownRaw, _ := json.Marshal(own)
			s.enqueue(c, marshalBroadcast(nodeID, "metric", pickFields(userBase, userMetricFields, map[string]json.RawMessage{"forward_metrics": ownRaw})))
		}
	}
}

func pickFields(fields map[string]json.RawMessage, keys []string, extra map[string]json.RawMessage) string {
	out := make(map[string]json.RawMessage, len(keys)+len(extra))
	for _, k := range keys {
		if v, ok := fields[k]; ok {
			out[k] = v
		}
	}
	for k, v := range extra {
		out[k] = v
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
