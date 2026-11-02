package handler

// Plan 187 公开「探针」页: the public, read-only server status page at /tz.
//
// Safe by construction: every probe route only reads the in-memory snapshot
// (latest agent reports + path-latency rounds) or averaged node_metric rows,
// through the whitelist structs of internal/probe. Nothing here sends node
// commands, runs diagnostics, touches files or writes the database. Requests
// are not logged one by one; refusals are counted (one summary line a minute).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"go-backend/internal/clientip"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/probe"
	"go-backend/internal/ratelimit"
	"go-backend/internal/store/repo"
)

const (
	probeSwitchKey    = "login_monitor_link" // 公开探针页 (was 登录页显示监控链接)
	probeTitleKey     = "probe_title"
	probeDomainKey    = "probe_domain"
	probeLatencyKey   = "probe_latency"
	probeNodesKey     = "probe_nodes"
	probeGroupsKey    = "probe_groups"     // plan 189: [{id,name,nodes:[ids]}] in order
	probeGroupModeKey = "probe_group_mode" // plan 189: region (default) | group
	probeExpiryKey    = "probe_expiry"     // plan 194: 显示到期天数, default on
	probeAppNameKey   = "app_name"
	probeGroupsMax    = 30
	probeGroupNameMax = 20
	probeTitleMax     = 40
	probeNameMax      = 30
	probeSettingsTTL  = 5 * time.Second
	probeNodesTTL     = 15 * time.Second
	probeSeriesTTL    = 30 * time.Second
	probeEdgeMaxAge   = 3 * time.Minute
	probeWSReadLimit  = 512
	probeWSPing       = 25 * time.Second
	probeWSPongWait   = 60 * time.Second
	probeWSWriteWait  = 10 * time.Second
	// Close code sent when the admin switches the probe off.
	probeCloseOff = 4404
)

// Probe paths: the only ones that answer on the 探针专用域名.
const (
	probePathSnapshot = "/api/v1/probe/snapshot"
	probePathWS       = "/api/v1/probe/ws"
	probePathSeries   = "/api/v1/probe/series"
	probePathHost     = "/api/v1/probe/host"
)

var probeHostPaths = map[string]bool{probePathSnapshot: true, probePathWS: true, probePathSeries: true, probePathHost: true}

// ProbePublicPath reports whether path is one of the anonymous probe routes
// (the auth middleware skips them).
func ProbePublicPath(path string) bool { return probeHostPaths[path] }

type probeSettings struct {
	Enabled bool
	Title   string
	Domain  string
	Latency bool
	Hidden  map[int64]bool
	Names   map[int64]string
	// Plan 189 custom groups (member ids as stored; the snapshot and the
	// admin view drop deleted / hidden ones).
	Groups    []probeGroup
	GroupMode string
	// Plan 194: publish 「剩余天数」 (default on).
	Expiry bool
}

type probeNodesConfig struct {
	Hidden []int64           `json:"hidden"`
	Names  map[string]string `json:"names"`
}

// probeGroup is one stored custom group (probe_groups row, admin only).
type probeGroup struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Nodes []int64 `json:"nodes"`
}

type probeSeriesKey struct {
	id     int64
	rng    string
	points int
}

type probeSeriesEntry struct {
	at   time.Time
	body []byte
}

type probeState struct {
	once    sync.Once
	keyer   *probe.Keyer
	hub     *probe.Hub
	latency probe.LatencyBook

	samplesMu sync.RWMutex
	samples   map[int64]probe.Sample

	settingsMu sync.Mutex
	settings   atomic.Pointer[probeSettings]
	settingsAt atomic.Int64 // unix nano

	nodesMu  sync.Mutex
	nodes    []repo.ProbeNodeRow
	lastSeen map[int64]int64
	nodesAt  time.Time

	seriesMu    sync.Mutex
	seriesCache map[probeSeriesKey]probeSeriesEntry

	getRL    *ratelimit.Bucket
	seriesRL *ratelimit.Bucket
	// seriesSem: at most 2 history queries at a time, whatever the number
	// of client addresses (protects the SQLite writer).
	seriesSem chan struct{}
	wsRL      *ratelimit.Bucket
	refusals  ratelimit.Refusals
	upgrader  websocket.Upgrader
	// online overrides the agent session check (tests only).
	online func(nodeID int64) bool
}

func (h *Handler) probeInit() *probeState {
	p := &h.probe
	p.once.Do(func() {
		p.keyer = probe.NewKeyer(h.jwtSecret)
		p.samples = make(map[int64]probe.Sample)
		p.lastSeen = make(map[int64]int64)
		p.seriesCache = make(map[probeSeriesKey]probeSeriesEntry)
		p.seriesSem = make(chan struct{}, 2)
		// 60 per minute per address (burst 60): the page needs one GET on load,
		// one per tab return, and 12/min only while the stream is down.
		p.getRL = &ratelimit.Bucket{Burst: 60, Every: time.Second, MaxKeys: 4096}
		p.seriesRL = &ratelimit.Bucket{Burst: 30, Every: 2 * time.Second, MaxKeys: 4096}
		p.wsRL = &ratelimit.Bucket{Burst: 20, Every: 3 * time.Second, MaxKeys: 4096}
		p.refusals = ratelimit.Refusals{Name: "probe"}
		p.upgrader = websocket.Upgrader{
			ReadBufferSize:  probeWSReadLimit,
			WriteBufferSize: 4096,
			// Origin is checked before the upgrade with the /system-info rule.
			CheckOrigin: func(*http.Request) bool { return true },
		}
		p.hub = probe.NewHub(probe.HubConfig{
			Interval: 2 * time.Second,
			MaxPerIP: 5,
			MaxTotal: 200,
			Queue:    4,
			Build:    h.buildProbeFrame,
			Enabled:  func() bool { return h.probeSettings().Enabled },
		})
	})
	return p
}

// probeObserve keeps the newest report of a node (metric hook, 1 s): memory
// only, one small struct per node.
func (h *Handler) probeObserve(nodeID int64, s probe.Sample) {
	p := h.probeInit()
	p.samplesMu.Lock()
	p.samples[nodeID] = s
	p.samplesMu.Unlock()
}

// probeRecordLatency takes the node→node edges of a finished path round.
func (h *Handler) probeRecordLatency(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, now time.Time) {
	edges := make([]probe.Edge, 0)
	for _, plan := range plans {
		for _, layer := range plan.Layers {
			for _, e := range layer {
				res, ok := results[e.Key]
				if !ok {
					continue
				}
				edges = append(edges, probe.Edge{From: e.From, To: e.To, MS: res.LatencyMS, OK: res.OK})
			}
		}
	}
	h.probeInit().latency.Record(edges, now)
}

func (h *Handler) probeSettings() probeSettings {
	p := h.probeInit()
	now := time.Now().UnixNano()
	if cur := p.settings.Load(); cur != nil && now-p.settingsAt.Load() < probeSettingsTTL.Nanoseconds() {
		return *cur
	}
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	if cur := p.settings.Load(); cur != nil && time.Now().UnixNano()-p.settingsAt.Load() < probeSettingsTTL.Nanoseconds() {
		return *cur
	}
	st := h.loadProbeSettings()
	p.settings.Store(&st)
	p.settingsAt.Store(time.Now().UnixNano())
	return st
}

func (h *Handler) resetProbeSettings() {
	p := h.probeInit()
	p.settingsAt.Store(0)
	p.nodesMu.Lock()
	p.nodesAt = time.Time{}
	p.nodesMu.Unlock()
	p.seriesMu.Lock()
	p.seriesCache = make(map[probeSeriesKey]probeSeriesEntry)
	p.seriesMu.Unlock()
	p.hub.Invalidate()
}

// noteProbeConfigChange: the probe follows a saved switch / app name at once
// (and the old public stream within its own 10 s cache).
func (h *Handler) noteProbeConfigChange(key string) {
	switch key {
	case probeSwitchKey, probeAppNameKey, probeTitleKey, probeDomainKey, probeLatencyKey, probeNodesKey, probeGroupsKey, probeGroupModeKey, probeExpiryKey:
		h.resetProbeSettings()
		if key == probeSwitchKey {
			h.wsServer.ResetPublicSwitch()
		}
	}
}

func (h *Handler) loadProbeSettings() probeSettings {
	st := probeSettings{Latency: true, Expiry: true, Hidden: map[int64]bool{}, Names: map[int64]string{}, GroupMode: probe.GroupModeRegion}
	if h.repo == nil {
		return st
	}
	cfg, err := h.repo.GetConfigsByNames([]string{probeSwitchKey, probeTitleKey, probeDomainKey, probeLatencyKey, probeNodesKey, probeAppNameKey, probeGroupsKey, probeGroupModeKey, probeExpiryKey})
	if err != nil {
		// Fail closed: a DB hiccup must not open the page.
		return st
	}
	st.Enabled = cfg[probeSwitchKey] == "true"
	st.Title = strings.TrimSpace(cfg[probeTitleKey])
	if st.Title == "" {
		if app := strings.TrimSpace(cfg[probeAppNameKey]); app != "" {
			st.Title = truncateRunes(app+" 探针", probeTitleMax)
		} else {
			st.Title = "FLVX 探针"
		}
	}
	st.Domain = strings.TrimSpace(cfg[probeDomainKey])
	st.Latency = cfg[probeLatencyKey] != "false"
	st.Expiry = cfg[probeExpiryKey] != "false"
	if raw := strings.TrimSpace(cfg[probeNodesKey]); raw != "" {
		var nc probeNodesConfig
		if json.Unmarshal([]byte(raw), &nc) == nil {
			for _, id := range nc.Hidden {
				st.Hidden[id] = true
			}
			for k, v := range nc.Names {
				if id, err := strconv.ParseInt(k, 10, 64); err == nil && strings.TrimSpace(v) != "" {
					st.Names[id] = strings.TrimSpace(v)
				}
			}
		}
	}
	if raw := strings.TrimSpace(cfg[probeGroupsKey]); raw != "" {
		// A stored row that no longer validates (hand-edited) publishes no
		// groups rather than something half-checked.
		if groups, err := parseProbeGroups(raw); err == nil {
			st.Groups = groups
		}
	}
	if cfg[probeGroupModeKey] == probe.GroupModeGroup {
		st.GroupMode = probe.GroupModeGroup
	}
	return st
}

// parseProbeGroups decodes and validates a probe_groups value (no node
// existence check: deleted nodes are dropped where the groups are read).
func parseProbeGroups(raw string) ([]probeGroup, error) {
	var groups []probeGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil, errors.New("探针分组格式不正确")
	}
	return normalizeProbeGroups(groups, nil)
}

// normalizeProbeGroups validates custom groups: ≤ 30, names trimmed 1–20
// characters without control characters and unique (case-insensitive),
// member ids deduplicated (and, when known is given, existing nodes only).
// Ids stay stable when valid; new / clashing ones get the next free number.
func normalizeProbeGroups(groups []probeGroup, known map[int64]bool) ([]probeGroup, error) {
	if len(groups) > probeGroupsMax {
		return nil, errors.New("探针分组最多 30 个")
	}
	out := make([]probeGroup, 0, len(groups))
	names := make(map[string]bool, len(groups))
	ids := make(map[int64]bool, len(groups))
	var maxID int64
	for _, g := range groups {
		if g.ID > maxID && g.ID < 1<<40 {
			maxID = g.ID
		}
	}
	for _, g := range groups {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			return nil, errors.New("分组名不能为空")
		}
		if n := utf8.RuneCountInString(name); n > probeGroupNameMax || !utf8.ValidString(name) {
			return nil, errors.New("分组名最多 20 个字")
		}
		for _, r := range name {
			if unicode.IsControl(r) || r == ' ' || r == ' ' {
				return nil, errors.New("分组名不能包含控制字符")
			}
		}
		lower := strings.ToLower(name)
		if names[lower] {
			return nil, errors.New("分组名「" + name + "」重复了")
		}
		names[lower] = true
		id := g.ID
		if id <= 0 || id >= 1<<40 || ids[id] {
			maxID++
			id = maxID
		}
		ids[id] = true
		if len(g.Nodes) > 4096 {
			return nil, errors.New("分组里的服务器过多")
		}
		members := make([]int64, 0, len(g.Nodes))
		seen := make(map[int64]bool, len(g.Nodes))
		for _, nid := range g.Nodes {
			if seen[nid] {
				continue
			}
			if nid <= 0 || (known != nil && !known[nid]) {
				return nil, errors.New("分组「" + name + "」里有不存在的服务器，请刷新后再试")
			}
			seen[nid] = true
			members = append(members, nid)
		}
		out = append(out, probeGroup{ID: id, Name: name, Nodes: members})
	}
	return out, nil
}

// probeGroupInputs: the stored groups as the snapshot builder takes them.
func probeGroupInputs(groups []probeGroup) []probe.GroupInput {
	if len(groups) == 0 {
		return nil
	}
	out := make([]probe.GroupInput, 0, len(groups))
	for _, g := range groups {
		out = append(out, probe.GroupInput{Name: g.Name, Nodes: g.Nodes})
	}
	return out
}

// probeNodeRows: local nodes, cached 15 s (only refreshed while somebody
// looks). Offline nodes without a report since the panel started get their
// 「最后在线」 from one index seek each.
func (h *Handler) probeNodeRows(now time.Time) ([]repo.ProbeNodeRow, map[int64]int64) {
	p := h.probeInit()
	p.nodesMu.Lock()
	defer p.nodesMu.Unlock()
	if !p.nodesAt.IsZero() && now.Sub(p.nodesAt) < probeNodesTTL {
		return p.nodes, p.lastSeen
	}
	rows, err := h.repo.ListProbeNodes()
	if err != nil {
		return p.nodes, p.lastSeen
	}
	// Deleted nodes leave the in-memory sample map with the next refresh.
	alive := make(map[int64]bool, len(rows))
	for _, n := range rows {
		alive[n.ID] = true
	}
	p.samplesMu.Lock()
	for id := range p.samples {
		if !alive[id] {
			delete(p.samples, id)
		}
	}
	p.samplesMu.Unlock()
	p.samplesMu.RLock()
	seen := make(map[int64]int64, len(rows))
	missing := make([]int64, 0)
	for _, n := range rows {
		if s, ok := p.samples[n.ID]; ok {
			seen[n.ID] = s.At
		} else if old, ok := p.lastSeen[n.ID]; ok {
			seen[n.ID] = old
		} else {
			missing = append(missing, n.ID)
		}
	}
	p.samplesMu.RUnlock()
	for _, id := range missing {
		ts, _ := h.repo.LatestNodeMetricTime(id)
		seen[id] = ts
	}
	p.nodes, p.lastSeen, p.nodesAt = rows, seen, now
	return rows, seen
}

// probeShown resolves the shown nodes with their display names.
func (h *Handler) probeShown(now time.Time, st probeSettings) []probe.NodeInput {
	rows, seen := h.probeNodeRows(now)
	out := make([]probe.NodeInput, 0, len(rows))
	for _, n := range rows {
		if st.Hidden[n.ID] {
			continue
		}
		name := n.Name
		if v := st.Names[n.ID]; v != "" {
			name = v
		}
		out = append(out, probe.NodeInput{ID: n.ID, Name: name, Region: n.Region, Version: n.Version, LastSeen: seen[n.ID], ExpiryTime: n.ExpiryTime, RenewalCycle: n.RenewalCycle})
	}
	return out
}

func (h *Handler) buildProbeFrame(now time.Time) ([]byte, error) {
	p := h.probeInit()
	st := h.probeSettings()
	nodes := h.probeShown(now, st)
	p.samplesMu.RLock()
	samples := make(map[int64]probe.Sample, len(nodes))
	for _, n := range nodes {
		if s, ok := p.samples[n.ID]; ok {
			samples[n.ID] = s
		}
	}
	p.samplesMu.RUnlock()
	var edges map[int64][]probe.Edge
	if st.Latency {
		edges = p.latency.Latest(now, probeEdgeMaxAge)
	}
	snap := probe.BuildSnapshot(probe.BuildInput{
		Title:     st.Title,
		LatencyOn: st.Latency,
		Now:       now,
		Nodes:     nodes,
		Samples:   samples,
		Online:    h.probeOnline,
		Edges:     edges,
		Key:       p.keyer.Key,
		Groups:    probeGroupInputs(st.Groups),
		GroupMode: st.GroupMode,
		ExpiryOn:  st.Expiry,
	})
	return json.Marshal(response.R{Code: 0, Msg: "ok", TS: now.UnixMilli(), Data: snap})
}

func (h *Handler) probeOnline(nodeID int64) bool {
	if fn := h.probe.online; fn != nil {
		return fn(nodeID)
	}
	return h.wsServer.NodeOnline(nodeID)
}

// probePublicNode is the old public stream / endpoints view of a node:
// opaque number, hidden nodes dropped.
func (h *Handler) probePublicNode(nodeID int64) (int64, bool) {
	if h.probeSettings().Hidden[nodeID] {
		return 0, false
	}
	return h.probeInit().keyer.PublicID(nodeID), true
}

// publicOSArch is the old endpoints' version field: only 「(distro/arch)」,
// which their distro icon parser reads; the agent version is not shown.
func publicOSArch(version string) string {
	osName, arch := probe.ParseOSArch(version)
	if osName == "" && arch == "" {
		return ""
	}
	return "(" + osName + "/" + arch + ")"
}

// ─── host gate ──────────────────────────────────────────────────────

func requestHost(raw string) string {
	h := strings.TrimSpace(raw)
	if h == "" {
		return ""
	}
	if u, err := url.Parse("//" + h); err == nil && u.Hostname() != "" {
		return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	}
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

func (h *Handler) isProbeHost(r *http.Request, st probeSettings) bool {
	if st.Domain == "" {
		return false
	}
	if requestHost(r.Host) == st.Domain {
		return true
	}
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" && requestHost(strings.Split(xfh, ",")[0]) == st.Domain {
		return true
	}
	return false
}

// ProbeHostGate: on the 探针专用域名 only the probe routes exist; every other
// path (admin/user API, /system-info, /flow, /s) is a plain 404 there.
func (h *Handler) ProbeHostGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := h.probeSettings()
		if st.Domain != "" && h.isProbeHost(r, st) && !probeHostPaths[r.URL.Path] {
			h.probeInit().refusals.Note("probe_host_other_path")
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── public endpoints ───────────────────────────────────────────────

func probeJSON(w http.ResponseWriter, status int, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	raw, _ := json.Marshal(response.R{Code: code, Msg: msg, TS: time.Now().UnixMilli()})
	_, _ = w.Write(raw)
}

func probeOff(w http.ResponseWriter) {
	probeJSON(w, http.StatusNotFound, 404, "探针未开启")
}

func (h *Handler) probeAllowed(w http.ResponseWriter, r *http.Request, rl *ratelimit.Bucket, reason string) bool {
	middleware.SuppressRequestLog(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		probeJSON(w, http.StatusMethodNotAllowed, 405, "请求失败")
		return false
	}
	if !h.probeSettings().Enabled {
		probeOff(w)
		return false
	}
	if rl != nil && !rl.Take(clientip.LimitKey(r)) {
		h.probeInit().refusals.Note(reason)
		w.Header().Set("Retry-After", "5")
		probeJSON(w, http.StatusTooManyRequests, 429, "请求太频繁，请稍后再试")
		return false
	}
	return true
}

func (h *Handler) probeSnapshot(w http.ResponseWriter, r *http.Request) {
	p := h.probeInit()
	if !h.probeAllowed(w, r, p.getRL, "get_rate") {
		return
	}
	f, err := p.hub.Frame()
	if err != nil {
		probeJSON(w, http.StatusServiceUnavailable, -1, "暂时不可用")
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "application/json; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("ETag", f.ETag)
	hdr.Add("Vary", "Accept-Encoding")
	hdr.Set("X-Content-Type-Options", "nosniff")
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == f.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.Raw
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		hdr.Set("Content-Encoding", "gzip")
		body = f.Gzip
	}
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

func (h *Handler) probeHost(w http.ResponseWriter, r *http.Request) {
	middleware.SuppressRequestLog(w)
	if !h.isProbeHost(r, h.probeSettings()) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"probeOnly":true}}`))
}

func (h *Handler) probeWS(w http.ResponseWriter, r *http.Request) {
	middleware.SuppressRequestLog(w)
	p := h.probeInit()
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !h.probeSettings().Enabled {
		http.NotFound(w, r)
		return
	}
	if !h.wsServer.OriginAllowed(r) {
		p.refusals.Note("ws_origin")
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	ip := clientip.LimitKey(r)
	if !p.wsRL.Take(ip) {
		p.refusals.Note("ws_rate")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	gz := r.URL.Query().Get("gzip") == "1"
	client, err := p.hub.Join(ip, gz)
	if err != nil {
		if errors.Is(err, probe.ErrTotal) {
			p.refusals.Note("ws_total")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		p.refusals.Note("ws_per_ip")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	defer p.hub.Leave(client)
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(probeWSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(probeWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(probeWSPongWait))
	})
	// Reader: anything a client sends is read and thrown away (512 B max).
	go func() {
		defer p.hub.Leave(client)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	if f, err := p.hub.Frame(); err == nil {
		p.hub.Offer(client, f)
	}
	ping := time.NewTicker(probeWSPing)
	defer ping.Stop()
	for {
		select {
		case <-client.Done:
			if client.Reason() == probe.KickOff {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(probeCloseOff, "off"), time.Now().Add(time.Second))
			}
			return
		case f := <-client.Send:
			_ = conn.SetWriteDeadline(time.Now().Add(probeWSWriteWait))
			var werr error
			if client.Gzip {
				werr = conn.WriteMessage(websocket.BinaryMessage, f.Gzip)
			} else {
				werr = conn.WriteMessage(websocket.TextMessage, f.Raw)
			}
			if werr != nil {
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(probeWSWriteWait)); err != nil {
				return
			}
		}
	}
}

func (h *Handler) probeSeries(w http.ResponseWriter, r *http.Request) {
	p := h.probeInit()
	if !h.probeAllowed(w, r, p.seriesRL, "series_rate") {
		return
	}
	q := r.URL.Query()
	rng := q.Get("range")
	if rng == "" {
		rng = "1h"
	}
	dur, ok := probe.ParseRange(rng)
	if !ok {
		probeJSON(w, http.StatusBadRequest, 400, "参数错误")
		return
	}
	now := time.Now()
	st := h.probeSettings()
	key := strings.TrimSpace(q.Get("k"))
	var id int64
	shown := h.probeShown(now, st)
	keys := make(map[int64]string, len(shown))
	for _, n := range shown {
		k := p.keyer.Key(n.ID)
		keys[n.ID] = k
		if key != "" && k == key {
			id = n.ID
		}
	}
	if id == 0 {
		probeJSON(w, http.StatusNotFound, 404, "未找到")
		return
	}
	points, _ := strconv.Atoi(q.Get("points"))
	points = probe.ClampPoints(points)
	ck := probeSeriesKey{id: id, rng: rng, points: points}
	p.seriesMu.Lock()
	if e, ok := p.seriesCache[ck]; ok && now.Sub(e.at) < probeSeriesTTL {
		p.seriesMu.Unlock()
		writeProbeBody(w, e.body)
		return
	}
	p.seriesMu.Unlock()

	select {
	case p.seriesSem <- struct{}{}:
	case <-time.After(2 * time.Second):
		p.refusals.Note("series_busy")
		probeJSON(w, http.StatusServiceUnavailable, -1, "暂时不可用")
		return
	}
	step := probe.StepFor(dur, points)
	rows, err := func() ([]repo.NodeMetricBucket, error) {
		defer func() { <-p.seriesSem }()
		return h.repo.NodeMetricBuckets(id, now.Add(-dur).UnixMilli(), now.UnixMilli(), step, points)
	}()
	if err != nil {
		probeJSON(w, http.StatusServiceUnavailable, -1, "暂时不可用")
		return
	}
	series := probe.Series{K: key, Range: rng, Step: step, Points: make([][6]float64, 0, len(rows))}
	for _, b := range rows {
		series.Points = append(series.Points, [6]float64{
			float64(b.Timestamp), probe.Round1(b.CPUUsage), probe.Round1(b.MemUsage),
			float64(int64(b.NetInSpeed)), float64(int64(b.NetOutSpeed)), float64(int64(b.TCPConns + 0.5)),
		})
	}
	if st.Latency {
		for to, pts := range p.latency.History(id) {
			k, ok := keys[to]
			if !ok || to == id {
				continue
			}
			series.Latency = append(series.Latency, probe.LatencySeries{To: k, Points: pts})
		}
		sortLatencySeries(series.Latency, shown, keys)
	}
	body, err := json.Marshal(response.R{Code: 0, Msg: "ok", TS: now.UnixMilli(), Data: series})
	if err != nil {
		probeJSON(w, http.StatusServiceUnavailable, -1, "暂时不可用")
		return
	}
	p.seriesMu.Lock()
	if len(p.seriesCache) >= 512 {
		for k, e := range p.seriesCache {
			if now.Sub(e.at) >= probeSeriesTTL {
				delete(p.seriesCache, k)
			}
		}
		if len(p.seriesCache) >= 512 {
			p.seriesCache = make(map[probeSeriesKey]probeSeriesEntry)
		}
	}
	p.seriesCache[ck] = probeSeriesEntry{at: now, body: body}
	p.seriesMu.Unlock()
	writeProbeBody(w, body)
}

func sortLatencySeries(list []probe.LatencySeries, shown []probe.NodeInput, keys map[int64]string) {
	order := make(map[string]int, len(shown))
	for i, n := range shown {
		order[keys[n.ID]] = i
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && order[list[j].To] < order[list[j-1].To]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func writeProbeBody(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// ─── admin settings (设置 → 公开探针页) ─────────────────────────────

type probeAdminNode struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ProbeName string `json:"probeName"`
	Hidden    bool   `json:"hidden"`
	Region    string `json:"region"`
	Online    bool   `json:"online"`
}

type probeAdminSettings struct {
	Enabled bool             `json:"enabled"`
	Title   string           `json:"title"`
	Domain  string           `json:"domain"`
	Latency bool             `json:"latency"`
	Nodes   []probeAdminNode `json:"nodes"`
	// Shown in the card: what anonymous viewers get.
	DefaultTitle string `json:"defaultTitle"`
	// Plan 189: custom groups (deleted nodes pruned) + default mode.
	Groups    []probeGroup `json:"groups"`
	GroupMode string       `json:"groupMode"`
	// Plan 194: 显示到期天数.
	Expiry bool `json:"expiry"`
}

type probeAdminSave struct {
	Title   string `json:"title"`
	Domain  string `json:"domain"`
	Latency *bool  `json:"latency"`
	Nodes   []struct {
		ID        int64  `json:"id"`
		ProbeName string `json:"probeName"`
		Hidden    bool   `json:"hidden"`
	} `json:"nodes"`
	// Plan 189. Groups nil = leave the stored groups as they are (an older
	// open card); an empty list clears them.
	Groups    *[]probeGroup `json:"groups"`
	GroupMode string        `json:"groupMode"`
	// Plan 194. nil = leave the stored switch as it is (an older open card).
	Expiry *bool `json:"expiry"`
}

func (h *Handler) probeAdminGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	cfg, err := h.repo.GetConfigsByNames([]string{probeTitleKey, probeAppNameKey})
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取设置失败"))
		return
	}
	h.resetProbeSettings()
	st := h.probeSettings()
	rows, err := h.repo.ListProbeNodes()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取节点失败"))
		return
	}
	out := probeAdminSettings{Enabled: st.Enabled, Title: strings.TrimSpace(cfg[probeTitleKey]), Domain: st.Domain, Latency: st.Latency, Nodes: make([]probeAdminNode, 0, len(rows))}
	out.DefaultTitle = "FLVX 探针"
	if app := strings.TrimSpace(cfg[probeAppNameKey]); app != "" {
		out.DefaultTitle = truncateRunes(app+" 探针", probeTitleMax)
	}
	known := make(map[int64]bool, len(rows))
	for _, n := range rows {
		known[n.ID] = true
		out.Nodes = append(out.Nodes, probeAdminNode{ID: n.ID, Name: n.Name, ProbeName: st.Names[n.ID], Hidden: st.Hidden[n.ID], Region: probe.CleanRegion(n.Region), Online: h.probeOnline(n.ID)})
	}
	out.Groups = make([]probeGroup, 0, len(st.Groups))
	for _, g := range st.Groups {
		members := make([]int64, 0, len(g.Nodes))
		for _, id := range g.Nodes {
			if known[id] {
				members = append(members, id)
			}
		}
		out.Groups = append(out.Groups, probeGroup{ID: g.ID, Name: g.Name, Nodes: members})
	}
	out.GroupMode = st.GroupMode
	out.Expiry = st.Expiry
	response.WriteJSON(w, response.OK(out))
}

var probeDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// normalizeProbeDomain: a bare host name (scheme / path / port stripped).
func normalizeProbeDomain(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			return "", errors.New("探针专用域名格式不正确")
		}
		v = u.Hostname()
	}
	v = strings.TrimSuffix(strings.SplitN(v, "/", 2)[0], ".")
	if len(v) > 253 || !probeDomainRe.MatchString(v) {
		return "", errors.New("探针专用域名格式不正确（例如 status.example.com）")
	}
	return v, nil
}

// normalizeProbeConfigValue validates probe_* rows written through the
// generic config API (the 设置 card uses /config/probe/save).
func normalizeProbeConfigValue(key, value string) (string, error) {
	switch key {
	case probeTitleKey:
		v := strings.TrimSpace(value)
		if utf8.RuneCountInString(v) > probeTitleMax {
			return "", errors.New("探针标题最多 40 个字")
		}
		return v, nil
	case probeDomainKey:
		return normalizeProbeDomain(value)
	case probeLatencyKey:
		if v := strings.TrimSpace(value); v == "true" || v == "false" {
			return v, nil
		}
		return "", errors.New("显示延迟只能是 true 或 false")
	case probeExpiryKey:
		if v := strings.TrimSpace(value); v == "true" || v == "false" {
			return v, nil
		}
		return "", errors.New("显示到期天数只能是 true 或 false")
	case probeNodesKey:
		var nc probeNodesConfig
		if err := json.Unmarshal([]byte(value), &nc); err != nil {
			return "", errors.New("探针节点设置格式不正确")
		}
		if len(nc.Hidden) > 4096 || len(nc.Names) > 4096 {
			return "", errors.New("探针节点设置过多")
		}
		for k, v := range nc.Names {
			if _, err := strconv.ParseInt(k, 10, 64); err != nil || utf8.RuneCountInString(strings.TrimSpace(v)) > probeNameMax {
				return "", errors.New("探针显示名最多 30 个字")
			}
		}
		if nc.Hidden == nil {
			nc.Hidden = []int64{}
		}
		if nc.Names == nil {
			nc.Names = map[string]string{}
		}
		raw, _ := json.Marshal(nc)
		return string(raw), nil
	case probeGroupsKey:
		groups, err := parseProbeGroups(value)
		if err != nil {
			return "", err
		}
		raw, _ := json.Marshal(groups)
		return string(raw), nil
	case probeGroupModeKey:
		if v := strings.TrimSpace(value); v == probe.GroupModeRegion || v == probe.GroupModeGroup {
			return v, nil
		}
		return "", errors.New("默认分组方式只能是 region 或 group")
	}
	return value, nil
}

// probeDomainLockout refuses the domain the admin is using right now.
func probeDomainLockout(r *http.Request, key, domain string) error {
	if key != probeDomainKey || domain == "" {
		return nil
	}
	if requestHost(r.Host) == domain || requestHost(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]) == domain {
		return errors.New("不能设为当前正在使用的面板域名（设置后这个域名只剩探针页）")
	}
	return nil
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func (h *Handler) probeAdminSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req probeAdminSave
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	title := strings.TrimSpace(req.Title)
	if utf8.RuneCountInString(title) > probeTitleMax {
		response.WriteJSON(w, response.ErrDefault("探针标题最多 40 个字"))
		return
	}
	domain, err := normalizeProbeDomain(req.Domain)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	// Lock-out guard: the probe domain answers only probe routes, so it can
	// never be the address the admin is using right now.
	if err := probeDomainLockout(r, probeDomainKey, domain); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	rows, err := h.repo.ListProbeNodes()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "读取节点失败"))
		return
	}
	known := make(map[int64]bool, len(rows))
	for _, n := range rows {
		known[n.ID] = true
	}
	nc := probeNodesConfig{Hidden: []int64{}, Names: map[string]string{}}
	for _, n := range req.Nodes {
		if !known[n.ID] {
			continue
		}
		name := strings.TrimSpace(n.ProbeName)
		if utf8.RuneCountInString(name) > probeNameMax {
			response.WriteJSON(w, response.ErrDefault("探针显示名最多 30 个字"))
			return
		}
		if n.Hidden {
			nc.Hidden = append(nc.Hidden, n.ID)
		}
		if name != "" {
			nc.Names[strconv.FormatInt(n.ID, 10)] = name
		}
	}
	nodesJSON, _ := json.Marshal(nc)
	latency := "true"
	if req.Latency != nil && !*req.Latency {
		latency = "false"
	}
	writes := [][2]string{{probeTitleKey, title}, {probeDomainKey, domain}, {probeLatencyKey, latency}, {probeNodesKey, string(nodesJSON)}}
	// Plan 189 custom groups: validated in full before anything is written.
	if req.Groups != nil {
		groups, err := normalizeProbeGroups(*req.Groups, known)
		if err != nil {
			response.WriteJSON(w, response.ErrDefault(err.Error()))
			return
		}
		groupsJSON, _ := json.Marshal(groups)
		writes = append(writes, [2]string{probeGroupsKey, string(groupsJSON)})
	}
	if req.Expiry != nil {
		expiry := "true"
		if !*req.Expiry {
			expiry = "false"
		}
		writes = append(writes, [2]string{probeExpiryKey, expiry})
	}
	switch req.GroupMode {
	case "":
	case probe.GroupModeRegion, probe.GroupModeGroup:
		writes = append(writes, [2]string{probeGroupModeKey, req.GroupMode})
	default:
		response.WriteJSON(w, response.ErrDefault("默认分组方式只能是 按地区 或 按自定义分组"))
		return
	}
	now := time.Now().UnixMilli()
	for _, kv := range writes {
		if err := h.repo.UpsertConfig(kv[0], kv[1], now); err != nil {
			response.WriteJSON(w, response.Err(-2, "保存失败"))
			return
		}
	}
	h.resetProbeSettings()
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) registerProbe(mux *http.ServeMux) {
	mux.HandleFunc(probePathSnapshot, h.probeSnapshot)
	mux.HandleFunc(probePathWS, h.probeWS)
	mux.HandleFunc(probePathSeries, h.probeSeries)
	mux.HandleFunc(probePathHost, h.probeHost)
	mux.HandleFunc("/api/v1/config/probe", h.probeAdminGet)
	mux.HandleFunc("/api/v1/config/probe/save", h.probeAdminSave)
	h.wsServer.SetPublicNodeView(h.probePublicNode)
}

// ProbeStats is for the demo / tests (resource numbers).
func (h *Handler) ProbeStats() (probe.HubStats, int) {
	p := h.probeInit()
	return p.hub.Stats(), p.latency.Pairs()
}
