package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/authstate"
	"go-backend/internal/clientip"
	"go-backend/internal/http/middleware"
	"go-backend/internal/ratelimit"
	"go-backend/internal/security"
	"go-backend/internal/store/repo"
)

type encryptedMessage struct {
	Encrypted bool   `json:"encrypted"`
	Data      string `json:"data"`
	Timestamp int64  `json:"timestamp"`
}

type broadcastMessage struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Data string `json:"data"`
}

type connWrap struct {
	conn  *websocket.Conn
	mu    sync.Mutex // node sessions: serializes command writes and pings
	admin bool       // role 0: receives the machine protection fields of node metrics
	// keepaliveErr: the panel's keepalive ping could not be written and the
	// keepalive closed the connection (node sessions, plan 138).
	keepaliveErr atomic.Pointer[string]

	// Browser sessions (clients.go): one writer goroutine drains send.
	kind      clientKind
	userID    int64
	role      int
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
	scope     atomic.Pointer[NodeScope]
}

type nodeSession struct {
	panelIPFamily   string // empty for agents without panel IP family support
	machineProtect  int    // handshake capability; 0 = agent without machine protection
	runtimeRevs     int    // handshake capability; >0 = agent reports runtime revisions (plan 138)
	userRate        int    // handshake capability; >0 = agent applies metadata.userLimiter (plan 159)
	landingRate     int    // handshake capability; >0 = agent shapes user_rates on its landing nodes (plan 161)
	startedAt       time.Time
	via             string       // how the session reached the panel (Cloudflare colo / direct)
	lastMsgAt       atomic.Int64 // unix nanos of the last data message (read loop)
	replacedAt      atomic.Int64 // unix nanos when a newer connection of the node replaced this one
	leikwanStatus   string
	leikwanForwards string
	egressDetected  string
	ipv6RAStatus    string
	ipv6RARecorded  bool
	ipv6RADetail    string
	nodeID          int64
	secret          string
	conn            *connWrap
	crypto          *security.AESCrypto // 缓存的 AES 加密器，避免每条消息重建
	// Last metric forwarded to browser sessions (read loop only, no lock).
	lastBrowserMetric time.Time
	serviceName       string // last stored service_name (read loop only)
	// Node DDNS (plan 142).
	addrReport int    // handshake capability; >0 = agent reports its interface addresses
	clientIP   string // address the panel sees for this session (Cloudflare client IP)
	addrsKey   string // last address report handed to the hook (read loop only)
}

// browserMetricInterval: browser views (node list, monitor, status page) get
// at most one metric per node per interval; agents report every second. The
// panel-side recording (machine sample, metrics ingestion) still sees every
// report. Tests may lower it.
var browserMetricInterval atomic.Int64

func init() { browserMetricInterval.Store(int64(1500 * time.Millisecond)) }

// browserMetricDue is called from the node's read loop only.
func (ns *nodeSession) browserMetricDue(now time.Time) bool {
	if !ns.lastBrowserMetric.IsZero() && now.Sub(ns.lastBrowserMetric) < time.Duration(browserMetricInterval.Load()) {
		return false
	}
	ns.lastBrowserMetric = now
	return true
}

type commandResponse struct {
	Type      string          `json:"type"`
	Success   bool            `json:"success"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
}

type pendingRequest struct {
	nodeID int64
	ch     chan CommandResult
}

const (
	wsPingPeriod = 15 * time.Second
	wsPongWait   = 45 * time.Second
	wsWriteWait  = 5 * time.Second
)

type CommandResult struct {
	Type    string                 `json:"type"`
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

type Server struct {
	repo          *repo.Repository
	jwtSecret     string
	upgrader      websocket.Upgrader
	onNodeOnline  func(nodeID int64)
	onNodeOffline func(nodeID int64)
	onNodeMetric  func(nodeID int64, info SystemInfo)
	onNodeAddrs   func(nodeID int64, addrs NodeAddrs) // plan 142

	mu                    sync.RWMutex
	clients               clientRegistry
	userState             *authstate.Cache
	scopeFn               func(userID int64) (NodeScope, error)
	extraOrigins          []string
	refusals              ratelimit.Refusals
	stats                 clientStats
	publicMu              sync.Mutex
	publicOn              bool
	publicAt              time.Time
	nodes                 map[int64]*nodeSession
	byConn                map[*websocket.Conn]*nodeSession
	pending               map[string]pendingRequest
	serviceConnections    map[int64]map[string]int           // nodeID -> serviceName -> connections
	serviceConnUpdateTime map[int64]int64                    // nodeID -> last update time
	forwardMetrics        map[int64]map[int64]*ForwardMetric // forwardID -> nodeID -> metric
	forwardMetricsMu      sync.RWMutex
	nodeOfflineTime       map[int64]int64 // nodeID -> offline timestamp (seconds)
	machineMu             sync.RWMutex
	machineStats          map[int64]*MachineSample // nodeID -> latest machine sample (memory only)
	sessionLog            nodeSessionLogger        // node session close lines (plan 138)
	commandsSent          sync.Map                 // nodeID -> *atomic.Int64 commands written (reconnect cost)
}

type SystemInfo struct {
	LandingCapable         bool              `json:"landing_capable,omitempty"`
	LandingEgress          int               `json:"landing_egress,omitempty"`
	LandingChain           int               `json:"landing_chain,omitempty"`
	LandingGuard           int               `json:"landing_guard,omitempty"`
	LandingConnMode        int               `json:"landing_conn_mode,omitempty"` // plan 141: applies 仅入站 limits + reports inbound counts
	LandingUserLimits      int               `json:"landing_user_limits,omitempty"` // plan 158: per-user landing Core groups + landing-user-* usage
	LeiKwan                *leiKwanReport    `json:"leikwan,omitempty"`
	EgressIPFamily         string            `json:"egress_ip_family,omitempty"`
	IPv6RAStatus           *string           `json:"ipv6_ra_status,omitempty"`
	IPv6RADetail           string            `json:"ipv6_ra_detail,omitempty"`
	Uptime                 uint64            `json:"uptime"`
	BytesReceived          uint64            `json:"bytes_received"`
	BytesTransmitted       uint64            `json:"bytes_transmitted"`
	PeriodBytesReceived    uint64            `json:"period_bytes_received"`
	PeriodBytesTransmitted uint64            `json:"period_bytes_transmitted"`
	BaselineRecordedAt     int64             `json:"baseline_recorded_at"`
	NextResetAt            int64             `json:"next_reset_at"`
	RenewalCycle           string            `json:"renewal_cycle,omitempty"`
	CPUUsage               float64           `json:"cpu_usage"`
	MemoryUsage            float64           `json:"memory_usage"`
	DiskUsage              float64           `json:"disk_usage"`
	Load1                  float64           `json:"load1"`
	Load5                  float64           `json:"load5"`
	Load15                 float64           `json:"load15"`
	TCPConns               int64             `json:"tcp_conns"`
	UDPConns               int64             `json:"udp_conns"`
	MachineConns           *MachineConns     `json:"machine_conns,omitempty"`
	MachineProtect         *MachineProtect   `json:"machine_protect,omitempty"`
	Addrs                  *NodeAddrs        `json:"addrs,omitempty"` // only when changed (plan 142)
	NetInSpeed             int64             `json:"net_in_speed"`
	NetOutSpeed            int64             `json:"net_out_speed"`
	ServiceName            string            `json:"service_name,omitempty"`
	ServiceConnections     map[string]int    `json:"serviceConnections"`
	ForwardMetrics         []ForwardMetric   `json:"forward_metrics,omitempty"`
	QuotaGroups            []QuotaGroupUsage `json:"quotaGroups,omitempty"`
}

type QuotaGroupUsage struct {
	Group       string   `json:"group"`
	Connections int      `json:"connections"`
	ClientIps   []string `json:"clientIps"`
	// RelayConnections (plan 158, landing-user-* groups only): inbound
	// connections on the landing Core whose source is one of our FLVX nodes
	// (relayed by a forward rule). Connections / ClientIps exclude them.
	RelayConnections int `json:"relayConnections,omitempty"`
}

// ForwardMetric 转发规则指标
type ForwardMetric struct {
	ForwardID   int64  `json:"forward_id"`
	UserID      int64  `json:"user_id"`
	TunnelID    int64  `json:"tunnel_id"`
	NodeID      int64  `json:"node_id"` // 新增：节点 ID
	Port        int    `json:"port"`    // 新增：入口端口
	ServiceName string `json:"service_name"`
	InSpeed     uint64 `json:"in_speed"`
	OutSpeed    uint64 `json:"out_speed"`
	Connections int    `json:"connections"`
}

func (s *Server) SetNodeOnlineHook(fn func(nodeID int64)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onNodeOnline = fn
	s.mu.Unlock()
}

func (s *Server) SetNodeOfflineHook(fn func(nodeID int64)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onNodeOffline = fn
	s.mu.Unlock()
}

func (s *Server) SetNodeMetricHook(fn func(nodeID int64, info SystemInfo)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onNodeMetric = fn
	s.mu.Unlock()
}

// GetServiceConnections 获取指定节点上所有服务的连接数
func (s *Server) GetServiceConnections(nodeID int64) map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if conns, ok := s.serviceConnections[nodeID]; ok {
		// 返回副本避免外部修改
		result := make(map[string]int, len(conns))
		for k, v := range conns {
			result[k] = v
		}
		return result
	}
	return nil
}

// GetForwardCurrentConnections 获取指定转发的当前连接数
// 服务名格式为 "{forwardID}_{userID}_{userTunnelID}_tcp" 或 "{forwardID}_{userID}_{userTunnelID}_udp"
func (s *Server) GetForwardCurrentConnections(nodeID int64, forwardID int64) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conns := s.serviceConnections[nodeID]
	if conns == nil {
		return 0
	}

	total := 0
	// 服务名格式：{forwardID}_{userID}_{userTunnelID}_tcp 或 _udp
	// 遍历所有连接数，匹配以 "{forwardID}_" 开头的服务
	prefix := fmt.Sprintf("%d_", forwardID)
	for serviceName, count := range conns {
		if strings.HasPrefix(serviceName, prefix) {
			total += count
		}
	}
	return total
}

// GetForwardMetric 获取指定 forward 的实时指标（汇总所有节点）
func (s *Server) GetForwardMetric(forwardID int64) *ForwardMetric {
	s.forwardMetricsMu.RLock()
	defer s.forwardMetricsMu.RUnlock()

	nodeMetrics, ok := s.forwardMetrics[forwardID]
	if !ok || len(nodeMetrics) == 0 {
		return nil
	}

	// 汇总所有节点的带宽
	var totalInSpeed, totalOutSpeed, totalConnections uint64
	var firstMetric *ForwardMetric
	for _, fm := range nodeMetrics {
		if firstMetric == nil {
			// 保存第一个指标的元数据
			firstMetric = &ForwardMetric{
				ForwardID:   fm.ForwardID,
				UserID:      fm.UserID,
				TunnelID:    fm.TunnelID,
				ServiceName: fm.ServiceName,
			}
		}
		totalInSpeed += fm.InSpeed
		totalOutSpeed += fm.OutSpeed
		totalConnections += uint64(fm.Connections)
	}

	if firstMetric == nil {
		return nil
	}

	return &ForwardMetric{
		ForwardID:   firstMetric.ForwardID,
		UserID:      firstMetric.UserID,
		TunnelID:    firstMetric.TunnelID,
		ServiceName: firstMetric.ServiceName,
		InSpeed:     totalInSpeed,
		OutSpeed:    totalOutSpeed,
		Connections: int(totalConnections),
	}
}

func NewServer(repo *repo.Repository, jwtSecret string) *Server {
	s := &Server{
		repo:      repo,
		jwtSecret: jwtSecret,
		upgrader: websocket.Upgrader{
			// Browser origins are checked in serveBrowser before upgrading;
			// agents (type=1) send no Origin.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		extraOrigins:          parseExtraOrigins(),
		refusals:              ratelimit.Refusals{Name: "websocket"},
		nodes:                 make(map[int64]*nodeSession),
		byConn:                make(map[*websocket.Conn]*nodeSession),
		pending:               make(map[string]pendingRequest),
		serviceConnections:    make(map[int64]map[string]int),
		serviceConnUpdateTime: make(map[int64]int64),
		forwardMetrics:        make(map[int64]map[int64]*ForwardMetric), // forwardID -> nodeID -> metric
		nodeOfflineTime:       make(map[int64]int64),                    // nodeID -> offline timestamp
		machineStats:          make(map[int64]*MachineSample),
	}
	// 启动后台清理任务（每 2 分钟清理一次过期数据）
	go s.cleanupStaleMetrics(2 * time.Minute)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	typeVal := query.Get("type")
	secret := query.Get("secret")

	if typeVal == "1" {
		node, err := s.repo.GetNodeBySecret(secret)
		if err != nil || node == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.handleNode(w, r, node.ID, secret)
		return
	}

	if typeVal == "0" || typeVal == "2" {
		s.serveBrowser(w, r, typeVal, secret)
		return
	}

	http.Error(w, "bad request", http.StatusBadRequest)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request, nodeID int64, secret string) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// The session close line below replaces the access-log line of the
	// hijacked request ("GET /system-info -> 200 (<lifetime>)").
	middleware.SuppressRequestLog(w)
	cw := &connWrap{conn: conn}
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	done := make(chan struct{})
	go startKeepalive(cw, done)

	version := r.URL.Query().Get("version")
	httpVal := parseIntDefault(r.URL.Query().Get("http"), 0)
	tlsVal := parseIntDefault(r.URL.Query().Get("tls"), 0)
	socksVal := parseIntDefault(r.URL.Query().Get("socks"), 0)
	blockOtherVal := parseIntDefault(r.URL.Query().Get("blockOther"), 0)

	s.mu.Lock()
	if old, ok := s.nodes[nodeID]; ok {
		old.replacedAt.Store(time.Now().UnixNano())
		_ = old.conn.conn.Close()
		delete(s.byConn, old.conn.conn)
	}
	// 初始化 AES 加密器并缓存（仅创建一次）
	var nodeCrypto *security.AESCrypto
	if strings.TrimSpace(secret) != "" {
		nodeCrypto, _ = security.NewAESCrypto(secret)
	}
	ns := &nodeSession{nodeID: nodeID, secret: secret, conn: cw, crypto: nodeCrypto, startedAt: time.Now(), via: nodeSessionVia(r)}
	if v, err := strconv.Atoi(r.URL.Query().Get("runtimeRevs")); err == nil && v > 0 {
		ns.runtimeRevs = v
	}
	if value := r.URL.Query().Get("panelIpFamily"); value != "" {
		ns.panelIPFamily, _ = repo.NormalizePanelIPFamily(value)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("machineProtect")); err == nil && v > 0 {
		ns.machineProtect = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("addrReport")); err == nil && v > 0 {
		ns.addrReport = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("userRate")); err == nil && v > 0 {
		ns.userRate = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("landingRate")); err == nil && v > 0 {
		ns.landingRate = v
	}
	ns.clientIP = clientip.FromRequest(r)
	s.nodes[nodeID] = ns
	delete(s.serviceConnections, nodeID)
	delete(s.serviceConnUpdateTime, nodeID)
	s.byConn[conn] = ns
	// 节点重新上线，清除离线时间记录
	delete(s.nodeOfflineTime, nodeID)
	s.mu.Unlock()

	_ = s.repo.UpdateNodeOnline(nodeID, 1, version, httpVal, tlsVal, socksVal, blockOtherVal)
	// Plan 159: remembered for the admin view (offline nodes); one write per
	// session, only when the capability changed.
	_ = s.repo.SetNodeUserRateCapability(nodeID, ns.userRate)
	// Plan 161: read by the native landing sync (user_rates only for >= 1).
	_ = s.repo.SetNodeLandingRateCapability(nodeID, ns.landingRate)
	s.broadcastStatus(nodeID, 1)

	s.mu.RLock()
	onlineHook := s.onNodeOnline
	s.mu.RUnlock()
	if onlineHook != nil {
		go onlineHook(nodeID)
	}

	defer func() {
		close(done)
		needOfflineBroadcast := false
		s.mu.Lock()
		current, ok := s.nodes[nodeID]
		if ok && current.conn.conn == conn {
			delete(s.nodes, nodeID)
			// 记录节点离线时间
			s.nodeOfflineTime[nodeID] = time.Now().Unix()
			needOfflineBroadcast = true
		}
		delete(s.byConn, conn)
		s.mu.Unlock()
		if needOfflineBroadcast {
			s.failPendingForNode(nodeID, "节点连接已断开")
			_ = s.repo.UpdateNodeStatus(nodeID, 0)
			s.broadcastStatus(nodeID, 0)
			s.mu.RLock()
			offlineHook := s.onNodeOffline
			s.mu.RUnlock()
			if offlineHook != nil {
				go offlineHook(nodeID)
			}
		}
		_ = conn.Close()
	}()

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			s.logNodeSessionEnd(ns, err)
			return
		}
		ns.lastMsgAt.Store(time.Now().UnixNano())

		msg := decryptIfNeeded(payload, ns.crypto, secret)
		if s.tryResolvePending(nodeID, msg) {
			// Command replies (TcpPing, landing sync, client IPs, ...) belong
			// to the waiting caller only; never broadcast them.
			continue
		}

		var parsed struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(msg), &parsed) == nil && parsed.Type != "" {
			switch parsed.Type {
			case "metric":
				// Agent 新版指标消息：{type:"metric", data:{...}}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal([]byte(msg), &envelope); err == nil && len(envelope.Data) > 0 {
					// 解析 SystemInfo 并调用 hook
					var sysInfo SystemInfo
					sysOK := json.Unmarshal(envelope.Data, &sysInfo) == nil
					if sysOK {
						s.recordEgressDetection(ns, sysInfo.EgressIPFamily)
						s.recordIPv6RA(ns, sysInfo.IPv6RAStatus, sysInfo.IPv6RADetail)
						s.recordLeiKwan(ns, sysInfo.LeiKwan)
						s.recordMachine(nodeID, sysInfo, time.Now())
						s.recordAddrs(ns, sysInfo.Addrs)
						_ = s.repo.SetNodeLandingCapabilities(nodeID, sysInfo.LandingCapable, sysInfo.LandingEgress, sysInfo.LandingChain, sysInfo.LandingGuard, sysInfo.LandingConnMode, sysInfo.LandingUserLimits)
						// 缓存服务连接数
						s.mu.Lock()
						s.serviceConnections[nodeID] = sysInfo.ServiceConnections
						s.serviceConnUpdateTime[nodeID] = time.Now().Unix()
						// 更新 service_name
						// Once per session/change, not on every 1 s report.
						if sysInfo.ServiceName != "" && sysInfo.ServiceName != ns.serviceName {
							if s.repo.UpdateNodeServiceName(nodeID, sysInfo.ServiceName) == nil {
								ns.serviceName = sysInfo.ServiceName
							}
						}
						// 缓存 forward 指标
						if len(sysInfo.ForwardMetrics) > 0 {
							s.forwardMetricsMu.Lock()
							for _, fm := range sysInfo.ForwardMetrics {
								// 初始化 forwardID 的 map（如果不存在）
								if s.forwardMetrics[fm.ForwardID] == nil {
									s.forwardMetrics[fm.ForwardID] = make(map[int64]*ForwardMetric)
								}
								// 按 nodeID 存储
								s.forwardMetrics[fm.ForwardID][fm.NodeID] = &fm
							}
							s.forwardMetricsMu.Unlock()
						}
						s.mu.Unlock()

						s.mu.RLock()
						onMetric := s.onNodeMetric
						s.mu.RUnlock()
						if onMetric != nil {
							go onMetric(nodeID, sysInfo)
						}
					}
					// 配额组包含活跃客户端 IP，仅供面板内存聚合，不广播给前端。
					var fwd []ForwardMetric
					if sysOK {
						fwd = sysInfo.ForwardMetrics
					}
					if ns.browserMetricDue(time.Now()) {
						s.broadcastMetric(nodeID, envelope.Data, fwd)
					}
				}
				continue
			case "ReportPublicIP":
				// Plan 147: new agents report their public addresses here (the
				// encrypted session) instead of plain HTTP. Same fill-empty rule
				// as /api/v1/node/report-ip: never overwrites an address.
				var envelope struct {
					Data struct {
						PublicIP   string `json:"public_ip"`
						PublicIPV4 string `json:"public_ip_v4"`
						PublicIPV6 string `json:"public_ip_v6"`
					} `json:"data"`
				}
				if json.Unmarshal([]byte(msg), &envelope) == nil {
					v4, v6 := envelope.Data.PublicIPV4, envelope.Data.PublicIPV6
					if v4 == "" && v6 == "" {
						v4 = envelope.Data.PublicIP
						if repo.PublicIPReportAddr(v4, "v6") != "" {
							v4, v6 = "", envelope.Data.PublicIP
						}
					}
					s.applyPublicIPReport(nodeID, v4, v6)
				}
				continue
			case "UpgradeProgress":
				s.broadcastAdminTyped(nodeID, "upgrade_progress", msg)
				continue
			default:
				// Broadcast is a whitelist (metric / status / upgrade
				// progress). Late command replies and unknown agent
				// messages are dropped.
				continue
			}
		}

		// 兼容旧版 Agent：无 type 字段的系统信息消息
		if looksLikeSystemInfoMessage(msg) {
			var sysInfo SystemInfo
			if err := json.Unmarshal([]byte(msg), &sysInfo); err == nil {
				s.recordEgressDetection(ns, sysInfo.EgressIPFamily)
				s.recordIPv6RA(ns, sysInfo.IPv6RAStatus, sysInfo.IPv6RADetail)
				s.recordLeiKwan(ns, sysInfo.LeiKwan)
				s.recordMachine(nodeID, sysInfo, time.Now())
				_ = s.repo.SetNodeLandingCapabilities(nodeID, sysInfo.LandingCapable, sysInfo.LandingEgress, sysInfo.LandingChain, sysInfo.LandingGuard, sysInfo.LandingConnMode, sysInfo.LandingUserLimits)
				// 缓存服务连接数
				s.mu.Lock()
				s.serviceConnections[nodeID] = sysInfo.ServiceConnections
				s.serviceConnUpdateTime[nodeID] = time.Now().Unix()
				s.mu.Unlock()

				s.mu.RLock()
				onMetric := s.onNodeMetric
				s.mu.RUnlock()
				if onMetric != nil {
					go onMetric(nodeID, sysInfo)
				}
				if ns.browserMetricDue(time.Now()) {
					s.broadcastMetric(nodeID, []byte(msg), sysInfo.ForwardMetrics)
				}
				continue
			}
		}
		// Anything else (untyped, unknown) is dropped: never broadcast.
	}
}

// metricDataForBroadcast is the metric data for non-admin and public sessions.
func metricDataForBroadcast(raw []byte) string {
	_, other := metricBroadcastVariants(raw)
	return other
}

func looksLikeSystemInfoMessage(msg string) bool {
	// Keep this as a cheap heuristic so that arbitrary JSON objects don't get
	// misclassified as metrics (SystemInfo unmarshal would otherwise succeed with
	// all-zero values).
	if strings.TrimSpace(msg) == "" {
		return false
	}
	if !strings.Contains(msg, "{") {
		return false
	}

	keys := []string{
		"\"uptime\"",
		"\"cpu_usage\"",
		"\"memory_usage\"",
		"\"disk_usage\"",
		"\"bytes_received\"",
		"\"bytes_transmitted\"",
		"\"net_in_speed\"",
		"\"net_out_speed\"",
		"\"tcp_conns\"",
		"\"udp_conns\"",
		"\"load1\"",
		"\"load5\"",
		"\"load15\"",
	}
	matched := 0
	for _, k := range keys {
		if strings.Contains(msg, k) {
			matched++
			if matched >= 3 {
				return true
			}
		}
	}
	return false
}

func (s *Server) SendCommand(nodeID int64, cmdType string, data interface{}, timeout time.Duration) (CommandResult, error) {
	if s == nil {
		return CommandResult{}, errors.New("server not initialized")
	}
	if strings.TrimSpace(cmdType) == "" {
		return CommandResult{}, errors.New("command type is empty")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	s.mu.RLock()
	ns, ok := s.nodes[nodeID]
	s.mu.RUnlock()
	if !ok || ns == nil || ns.conn == nil || ns.conn.conn == nil {
		return CommandResult{}, errors.New("节点不在线")
	}

	requestID := fmt.Sprintf("%d_%d", nodeID, time.Now().UnixNano())
	ch := make(chan CommandResult, 1)

	s.mu.Lock()
	s.pending[requestID] = pendingRequest{nodeID: nodeID, ch: ch}
	s.mu.Unlock()

	cleanup := func() {
		s.mu.Lock()
		if p, exists := s.pending[requestID]; exists {
			delete(s.pending, requestID)
			close(p.ch)
		}
		s.mu.Unlock()
	}

	cmdPayload := map[string]interface{}{
		"type":      cmdType,
		"data":      annotateRuntimeRevs(cmdType, data),
		"requestId": requestID,
	}
	rawCmd, err := json.Marshal(cmdPayload)
	if err != nil {
		cleanup()
		return CommandResult{}, err
	}

	messageData := rawCmd
	if ns.crypto != nil {
		encrypted, err := ns.crypto.Encrypt(rawCmd)
		if err != nil {
			cleanup()
			return CommandResult{}, err
		}
		wrapper := map[string]interface{}{
			"encrypted": true,
			"data":      encrypted,
			"timestamp": time.Now().UnixMilli(),
		}
		messageData, err = json.Marshal(wrapper)
		if err != nil {
			cleanup()
			return CommandResult{}, err
		}
	}

	counter, ok := s.commandsSent.Load(nodeID)
	if !ok {
		counter, _ = s.commandsSent.LoadOrStore(nodeID, new(atomic.Int64))
	}
	counter.(*atomic.Int64).Add(1)
	ns.conn.mu.Lock()
	_ = ns.conn.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	err = ns.conn.conn.WriteMessage(websocket.TextMessage, messageData)
	_ = ns.conn.conn.SetWriteDeadline(time.Time{})
	ns.conn.mu.Unlock()
	if err != nil {
		cleanup()
		return CommandResult{}, err
	}

	select {
	case result, ok := <-ch:
		if !ok {
			return CommandResult{}, errors.New("命令通道已关闭")
		}
		if !result.Success {
			if strings.TrimSpace(result.Message) == "" {
				result.Message = "命令执行失败"
			}
			return result, errors.New(result.Message)
		}
		return result, nil
	case <-time.After(timeout):
		cleanup()
		return CommandResult{}, errors.New("等待节点响应超时")
	}
}

// tryResolvePending hands a command reply to its waiter. It returns true for
// every message carrying a requestId (answered, late or mismatched): such a
// message is a reply and must not be processed or broadcast any further.
func (s *Server) tryResolvePending(nodeID int64, message string) bool {
	if s == nil || strings.TrimSpace(message) == "" {
		return false
	}

	// 快速短路：指标消息永远不含 requestId，跳过完整 JSON 解析
	if !strings.Contains(message, "\"requestId\"") {
		return false
	}

	var resp commandResponse
	if err := json.Unmarshal([]byte(message), &resp); err != nil {
		return false
	}
	if strings.TrimSpace(resp.RequestID) == "" {
		return false
	}

	s.mu.Lock()
	p, ok := s.pending[resp.RequestID]
	if ok {
		delete(s.pending, resp.RequestID)
	}
	s.mu.Unlock()
	if !ok {
		return true
	}
	if p.nodeID != nodeID {
		select {
		case p.ch <- CommandResult{Type: resp.Type, Success: false, Message: "节点响应与请求不匹配"}:
		default:
		}
		close(p.ch)
		return true
	}

	result := CommandResult{
		Type:    resp.Type,
		Success: resp.Success,
		Message: resp.Message,
	}
	if len(resp.Data) > 0 {
		var data map[string]interface{}
		if err := json.Unmarshal(resp.Data, &data); err == nil {
			result.Data = data
		}
	}

	select {
	case p.ch <- result:
	default:
	}
	close(p.ch)
	return true
}

func (s *Server) failPendingForNode(nodeID int64, message string) {
	if s == nil {
		return
	}

	type pair struct {
		id string
		pr pendingRequest
	}
	items := make([]pair, 0)

	s.mu.Lock()
	for id, pr := range s.pending {
		if pr.nodeID != nodeID {
			continue
		}
		items = append(items, pair{id: id, pr: pr})
		delete(s.pending, id)
	}
	s.mu.Unlock()

	for _, item := range items {
		select {
		case item.pr.ch <- CommandResult{Success: false, Message: message}:
		default:
		}
		close(item.pr.ch)
	}
}

func decryptIfNeeded(payload []byte, crypto *security.AESCrypto, secret string) string {
	text := string(payload)
	var wrap encryptedMessage
	if err := json.Unmarshal(payload, &wrap); err != nil || !wrap.Encrypted || strings.TrimSpace(wrap.Data) == "" {
		return text
	}

	// 优先使用缓存的 crypto 实例
	c := crypto
	if c == nil && strings.TrimSpace(secret) != "" {
		c, _ = security.NewAESCrypto(secret)
	}
	if c == nil {
		return text
	}
	plain, err := c.Decrypt(wrap.Data)
	if err != nil {
		return text
	}
	return string(plain)
}

func parseIntDefault(v string, fallback int) int {
	x, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return x
}

func startKeepalive(cw *connWrap, done <-chan struct{}) {
	if cw == nil || cw.conn == nil {
		return
	}
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			cw.mu.Lock()
			_ = cw.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			err := cw.conn.WriteMessage(websocket.PingMessage, nil)
			_ = cw.conn.SetWriteDeadline(time.Time{})
			cw.mu.Unlock()
			if err != nil {
				msg := err.Error()
				cw.keepaliveErr.Store(&msg)
				_ = cw.conn.Close()
				return
			}
		}
	}
}

// cleanupStaleMetrics 定期清理过期节点的指标数据（离线超过 10 分钟）
func (s *Server) cleanupStaleMetrics(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		s.pruneMachineStats(time.Now())
		s.mu.Lock()
		now := time.Now().Unix()
		offlineTimeout := int64(10 * 60) // 10 分钟

		// 清理离线超过 10 分钟的节点的 forwardMetrics
		for nodeID, offlineTime := range s.nodeOfflineTime {
			if now-offlineTime > offlineTimeout {
				// 清理该节点的所有 forward 指标
				s.forwardMetricsMu.Lock()
				for forwardID, nodeMetrics := range s.forwardMetrics {
					if _, exists := nodeMetrics[nodeID]; exists {
						delete(nodeMetrics, nodeID)
						// 如果该 forward 没有其他节点的指标了，删除整个 entry
						if len(nodeMetrics) == 0 {
							delete(s.forwardMetrics, forwardID)
						}
					}
				}
				s.forwardMetricsMu.Unlock()

				// 清理离线时间记录
				delete(s.nodeOfflineTime, nodeID)
			}
		}
		s.mu.Unlock()
	}
}

// Called by the session read loop; unchanged telemetry never issues a DB update.
func (s *Server) recordEgressDetection(ns *nodeSession, family string) {
	if family != "v4" && family != "v6" && family != "dual" {
		return
	}
	if ns.egressDetected == family {
		return
	}
	if err := s.repo.UpdateNodeEgressDetected(ns.nodeID, family, time.Now().UnixMilli()); err != nil {
		log.Printf("node %d egress detection: %v", ns.nodeID, err)
		return
	}
	ns.egressDetected = family
}

func (s *Server) recordIPv6RA(ns *nodeSession, reported *string, detail string) {
	// Missing means an older agent; explicit empty clears a previous warning.
	if reported == nil {
		return
	}
	status := *reported
	if status != "" && status != "ok" && status != "warn" && status != "error" {
		return
	}
	if runes := []rune(detail); len(runes) > 300 {
		detail = string(runes[:300])
	}
	if ns.ipv6RARecorded && ns.ipv6RAStatus == status && ns.ipv6RADetail == detail {
		return
	}
	if err := s.repo.UpdateNodeIPv6RA(ns.nodeID, status, detail, time.Now().UnixMilli()); err != nil {
		log.Printf("node %d IPv6 RA detection: %v", ns.nodeID, err)
		return
	}
	ns.ipv6RAStatus, ns.ipv6RADetail = status, detail
	ns.ipv6RARecorded = true
}

// RuntimeRevsCapable reports whether the node's current agent session reports
// runtime revisions (ListServices `revs`), i.e. a reconnect may skip unchanged
// objects (plan 138).
func (s *Server) RuntimeRevsCapable(nodeID int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	return ns != nil && ns.runtimeRevs > 0
}

// UserRateCapable reports whether the node's current agent session applies
// a forward service's metadata.userLimiter together with its own limiter
// (plan 159).
func (s *Server) UserRateCapable(nodeID int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	return ns != nil && ns.userRate > 0
}

// CommandsSent is the number of commands written to a node since the panel
// started (memory only; used to report the cost of a reconnect redeploy).
func (s *Server) CommandsSent(nodeID int64) int64 {
	if s == nil {
		return 0
	}
	if v, ok := s.commandsSent.Load(nodeID); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

// SessionStartedAt is when the node's current agent session began (zero when
// the node is not connected).
func (s *Server) SessionStartedAt(nodeID int64) time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ns := s.nodes[nodeID]; ns != nil {
		return ns.startedAt
	}
	return time.Time{}
}
