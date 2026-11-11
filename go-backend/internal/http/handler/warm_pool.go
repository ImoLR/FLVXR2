package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/ws"
)

// Plan 244 「预热池」 (panel side).
//
// The switch is per tunnel (tunnel.warm_pool, default on). A pool exists only
// while the tunnel has ≥ 1 active rule: the panel computes, per agent, the
// full desired state and sends it with SetWarmPools —
//
//	{"chains": {"chains_<tunnel>": 2}, "forwards": {"<rule id>": 2}}
//
// chains = 隧道转发: the tunnel's chain on every entry that serves an active
// rule and on all its middles (the whole path is warm); forwards = 端口转发:
// each active rule on its entry (one pool per rule target). Only agents with
// the `warmPool` handshake get it; the state is re-sent after every reconnect
// and whenever it changes (rule create / delete / pause / resume / expiry,
// tunnel save / switch / delete), plus a 30 s safety pass that only pushes
// differences. Rule services and chains are not touched by any of this, so
// the switch applies live without re-creating anything.
//
// The agents report counters once a minute (warm_pool on the metric
// message); the panel keeps the last hour in memory (12 × 5 min buckets,
// restart = empty) for the tag on the admin tunnel page.
const (
	warmPoolSize          = 2
	warmRelayReadTimeout  = "130s"
	warmReconcileEvery    = 30 * time.Second
	warmReconcileDebounce = 1500 * time.Millisecond
	warmPushTimeout       = 5 * time.Second
	warmStatBucket        = 5 * time.Minute
	warmStatBuckets       = 12 // = 1 h
	warmMinWarmStreams    = 10
	warmDeadReasonRatio   = 0.05
	warmFailLogEvery      = 10 * time.Minute
)

type warmPoolPayload struct {
	Chains   map[string]int `json:"chains"`
	Forwards map[string]int `json:"forwards"`
}

func (p warmPoolPayload) signature() string {
	b, _ := json.Marshal(p) // map keys are sorted
	return string(b)
}

type warmStatKey struct {
	NodeID int64
	Kind   string
	Owner  string
	Addr   string
}

type warmBucket struct {
	slot                                             int64
	coldN, missN, warmN, deadN, peerN, youngN, failN int64
	coldMs, missMs, warmMs                           float64
}

type warmStatEntry struct {
	buckets [warmStatBuckets]warmBucket
	target  bool
	idle    int
	state   string
	lastAt  time.Time
}

type warmSums struct {
	coldN, missN, warmN, deadN, peerN, youngN, failN int64
	coldMs, missMs, warmMs                           float64
}

type warmPoolManager struct {
	mu       sync.Mutex
	lastSent map[int64]string
	failLog  map[int64]time.Time
	stats    map[warmStatKey]*warmStatEntry
	kick     chan struct{}
	now      func() time.Time
}

func newWarmPoolManager() *warmPoolManager {
	return &warmPoolManager{
		lastSent: map[int64]string{},
		failLog:  map[int64]time.Time{},
		stats:    map[warmStatKey]*warmStatEntry{},
		kick:     make(chan struct{}, 1),
		now:      time.Now,
	}
}

func (h *Handler) warmPools() *warmPoolManager {
	if h == nil {
		return nil
	}
	h.warmOnce.Do(func() {
		if h.warm == nil {
			h.warm = newWarmPoolManager()
		}
	})
	return h.warm
}

// warmPoolsChanged schedules a reconcile (coalesced, ~1.5 s later).
func (h *Handler) warmPoolsChanged() {
	m := h.warmPools()
	if m == nil {
		return
	}
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// warmPoolNodeSession: the node's agent (re)connected or went away — its
// state is unknown, so the next reconcile sends it in full.
func (h *Handler) warmPoolNodeSession(nodeID int64) {
	m := h.warmPools()
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.lastSent, nodeID)
	m.mu.Unlock()
	h.warmPoolsChanged()
}

func (h *Handler) warmPoolCapable(nodeID int64) bool {
	if h.warmPoolCapableFn != nil {
		return h.warmPoolCapableFn(nodeID)
	}
	return h.wsServer != nil && h.wsServer.WarmPoolCapable(nodeID)
}

func (h *Handler) warmPoolNodes() []int64 {
	if h.warmPoolNodesFn != nil {
		return h.warmPoolNodesFn()
	}
	if h.wsServer == nil {
		return nil
	}
	return h.wsServer.WarmPoolNodes()
}

func (h *Handler) runWarmPoolLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	m := h.warmPools()
	ticker := time.NewTicker(warmReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(warmReconcileDebounce):
			}
			select {
			case <-m.kick:
			default:
			}
		}
		h.reconcileWarmPools()
		m.prune()
	}
}

// desiredWarmPools: per node, the SetWarmPools state the DB asks for.
func (h *Handler) desiredWarmPools() (map[int64]warmPoolPayload, error) {
	chains, err := h.repo.ListWarmChainOwners()
	if err != nil {
		return nil, err
	}
	forwards, err := h.repo.ListWarmForwardOwners()
	if err != nil {
		return nil, err
	}
	out := map[int64]warmPoolPayload{}
	get := func(id int64) warmPoolPayload {
		p, ok := out[id]
		if !ok {
			p = warmPoolPayload{Chains: map[string]int{}, Forwards: map[string]int{}}
			out[id] = p
		}
		return p
	}
	for _, o := range chains {
		get(o.NodeID).Chains[fmt.Sprintf("chains_%d", o.TunnelID)] = warmPoolSize
	}
	for _, o := range forwards {
		get(o.NodeID).Forwards[strconv.FormatInt(o.ForwardID, 10)] = warmPoolSize
	}
	return out, nil
}

// reconcileWarmPools pushes SetWarmPools to every capable agent whose state
// differs from the last one it acknowledged. It returns the nodes pushed.
func (h *Handler) reconcileWarmPools() []int64 {
	m := h.warmPools()
	if m == nil || h.repo == nil {
		return nil
	}
	nodes := h.warmPoolNodes()
	if len(nodes) == 0 {
		return nil
	}
	desired, err := h.desiredWarmPools()
	if err != nil {
		log.Printf("[warm-pool] desired state: %v", err)
		return nil
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	var pushed []int64
	for _, id := range nodes {
		p, ok := desired[id]
		if !ok {
			p = warmPoolPayload{Chains: map[string]int{}, Forwards: map[string]int{}}
		}
		sig := p.signature()
		m.mu.Lock()
		last, sent := m.lastSent[id]
		m.mu.Unlock()
		if sent && last == sig {
			continue
		}
		if _, err := h.sendNodeCommandWithTimeout(id, "SetWarmPools", p, warmPushTimeout, false, false); err != nil {
			m.mu.Lock()
			if at := m.failLog[id]; at.IsZero() || m.now().Sub(at) >= warmFailLogEvery {
				m.failLog[id] = m.now()
				log.Printf("[warm-pool] node %d: SetWarmPools failed: %v (retried every 30 s)", id, err)
			}
			m.mu.Unlock()
			continue
		}
		m.mu.Lock()
		m.lastSent[id] = sig
		delete(m.failLog, id)
		m.mu.Unlock()
		pushed = append(pushed, id)
	}
	return pushed
}

// ---- statistics ------------------------------------------------------------

func (h *Handler) onWarmPoolReport(nodeID int64, rep *ws.WarmPoolReport) {
	if rep == nil {
		return
	}
	if m := h.warmPools(); m != nil {
		m.ingest(nodeID, rep)
	}
}

func (m *warmPoolManager) ingest(nodeID int64, rep *ws.WarmPoolReport) {
	now := m.now()
	slot := now.Unix() / int64(warmStatBucket/time.Second)
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[warmStatKey]struct{}{}
	for _, k := range rep.Keys {
		key := warmStatKey{NodeID: nodeID, Kind: k.Kind, Owner: k.Owner, Addr: k.Addr}
		seen[key] = struct{}{}
		e := m.stats[key]
		if e == nil {
			e = &warmStatEntry{}
			m.stats[key] = e
		}
		b := &e.buckets[slot%warmStatBuckets]
		if b.slot != slot {
			*b = warmBucket{slot: slot}
		}
		b.coldN += k.ColdN
		b.coldMs += k.ColdMs
		b.missN += k.MissN
		b.missMs += k.MissMs
		b.warmN += k.WarmN
		b.warmMs += k.WarmMs
		b.deadN += k.DeadN
		b.peerN += k.PeerN
		b.youngN += k.YoungN
		b.failN += k.FailN
		e.target, e.idle, e.state, e.lastAt = k.Target, k.Idle, k.State, now
	}
	// A key the node no longer reports has no pool any more.
	for key, e := range m.stats {
		if key.NodeID != nodeID {
			continue
		}
		if _, ok := seen[key]; !ok {
			e.target, e.idle, e.state = false, 0, ""
		}
	}
}

func (m *warmPoolManager) prune() {
	if m == nil {
		return
	}
	cutoff := m.now().Add(-warmStatBucket * warmStatBuckets)
	m.mu.Lock()
	for key, e := range m.stats {
		if e.lastAt.Before(cutoff) {
			delete(m.stats, key)
		}
	}
	m.mu.Unlock()
}

func (e *warmStatEntry) sums(now time.Time) warmSums {
	cur := now.Unix() / int64(warmStatBucket/time.Second)
	var s warmSums
	for _, b := range e.buckets {
		if b.slot == 0 || cur-b.slot >= warmStatBuckets || b.slot > cur {
			continue
		}
		s.coldN += b.coldN
		s.coldMs += b.coldMs
		s.missN += b.missN
		s.missMs += b.missMs
		s.warmN += b.warmN
		s.warmMs += b.warmMs
		s.deadN += b.deadN
		s.peerN += b.peerN
		s.youngN += b.youngN
		s.failN += b.failN
	}
	return s
}

// warmKeyStat is one pool key's window as the views use it.
type warmKeyStat struct {
	key    warmStatKey
	sums   warmSums
	target bool
	state  string
}

// snapshot copies the window of every key of the given kind.
func (m *warmPoolManager) snapshot(kind string) []warmKeyStat {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]warmKeyStat, 0, len(m.stats))
	for key, e := range m.stats {
		if key.Kind != kind {
			continue
		}
		out = append(out, warmKeyStat{key: key, sums: e.sums(now), target: e.target, state: e.state})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].key, out[j].key
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Addr < b.Addr
	})
	return out
}

// ---- view (admin tunnel page) ----------------------------------------------

// warmPoolHopView: one warmed hop — 隧道转发: from node → next node;
// 端口转发: rule → its target.
type warmPoolHopView struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	FromNodeID int64    `json:"fromNodeId,omitempty"`
	ToNodeID   int64    `json:"toNodeId,omitempty"`
	WarmN      int64    `json:"warmN"`
	HitPct     int      `json:"hitPct"`
	ColdMs     float64  `json:"coldMs"`
	ExtraMs    float64  `json:"extraMs"`
	SavedMs    *float64 `json:"savedMs,omitempty"`
	DeadN      int64    `json:"deadN,omitempty"`
	State      string   `json:"state,omitempty"` // unsupported | rest | capped
	Enough     bool     `json:"enough"`
}

// warmPoolView is item["warmPoolState"]; absent when the switch is off, the
// tunnel has no active rule, or there is nothing to say yet.
type warmPoolView struct {
	Status    string            `json:"status"` // faster | slower | unsupported
	SavedMs   float64           `json:"savedMs"`
	Reason    string            `json:"reason,omitempty"`
	HitPct    int               `json:"hitPct"`
	ColdMs    float64           `json:"coldMs"`
	ExtraMs   float64           `json:"extraMs"`
	WarmN     int64             `json:"warmN"`
	Kind      string            `json:"kind"` // tunnel | forward
	Hops      []warmPoolHopView `json:"hops"`
	WindowMin int               `json:"windowMin"`
}

func hopFromSums(s warmSums, state string) warmPoolHopView {
	v := warmPoolHopView{WarmN: s.warmN, DeadN: s.deadN, State: state}
	if s.coldN > 0 {
		v.ColdMs = round1(s.coldMs / float64(s.coldN))
	}
	if s.warmN > 0 {
		v.ExtraMs = round1(s.warmMs / float64(s.warmN))
	}
	if streams := s.warmN + s.missN; streams > 0 {
		v.HitPct = int(math.Round(float64(s.warmN) * 100 / float64(streams)))
	}
	if s.warmN >= warmMinWarmStreams && s.coldN > 0 {
		saved := round1(v.ColdMs - v.ExtraMs)
		v.SavedMs = &saved
		v.Enough = true
	}
	return v
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// summarize turns per-path hop lists into the tunnel's view. Each path's
// value is the sum of its hops with enough warm streams; paths (one per
// entry, or one per rule target) are averaged weighted by warm streams.
func summarizeWarmPaths(kind string, paths [][]warmPoolHopView, weights []int64) *warmPoolView {
	v := &warmPoolView{Kind: kind, WindowMin: int(warmStatBucket * warmStatBuckets / time.Minute), Hops: []warmPoolHopView{}}
	var total, wsum float64
	var anyEnough, anyUnsupported, allUnsupported bool
	allUnsupported = true
	var coldSum, extraSum float64
	var coldW, extraW, warmN, missEq, deadN int64
	for i, hops := range paths {
		pathSaved := 0.0
		enough := false
		for _, hp := range hops {
			v.Hops = append(v.Hops, hp)
			if hp.State == "unsupported" {
				anyUnsupported = true
			} else {
				allUnsupported = false
			}
			if hp.Enough && hp.SavedMs != nil {
				pathSaved += *hp.SavedMs
				enough = true
			}
			deadN += hp.DeadN
		}
		if !enough {
			continue
		}
		anyEnough = true
		w := float64(weights[i])
		if w <= 0 {
			w = 1
		}
		total += pathSaved * w
		wsum += w
		if len(hops) > 0 {
			first := hops[0]
			coldSum += first.ColdMs * float64(first.WarmN)
			coldW += first.WarmN
			extraSum += first.ExtraMs * float64(first.WarmN)
			extraW += first.WarmN
			warmN += first.WarmN
			if first.HitPct > 0 {
				missEq += int64(math.Round(float64(first.WarmN) * 100 / float64(first.HitPct)))
			}
		}
	}
	if len(v.Hops) == 0 {
		return nil
	}
	if !anyEnough {
		if anyUnsupported && allUnsupported {
			v.Status = "unsupported"
			return v
		}
		return nil
	}
	v.SavedMs = round1(total / wsum)
	v.WarmN = warmN
	if coldW > 0 {
		v.ColdMs = round1(coldSum / float64(coldW))
	}
	if extraW > 0 {
		v.ExtraMs = round1(extraSum / float64(extraW))
	}
	if missEq > 0 {
		v.HitPct = int(math.Round(float64(warmN) * 100 / float64(missEq)))
	}
	if v.SavedMs >= 0 {
		v.Status = "faster"
	} else {
		v.Status = "slower"
		if warmN > 0 && float64(deadN)/float64(warmN) >= warmDeadReasonRatio {
			v.Reason = "预热连接常被对方断开"
		}
	}
	return v
}

// attachWarmPoolStates sets item["warmPoolState"] on the admin tunnel list.
func (h *Handler) attachWarmPoolStates(items []map[string]interface{}) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[warm-pool] attach view failed: %v", recovered)
		}
	}()
	m := h.warmPools()
	if m == nil || len(items) == 0 {
		return
	}
	active, err := h.repo.CountActiveGostForwardsByTunnel()
	if err != nil {
		return
	}
	chainKeys := m.snapshot("c")
	ruleKeys := m.snapshot("s")
	names := map[int64]string{}
	nodeName := func(id int64) string {
		if n, ok := names[id]; ok {
			return n
		}
		n := fmt.Sprintf("节点 %d", id)
		if node, err := h.getNodeRecord(id); err == nil && node != nil {
			n = nodeDisplayName(node)
		}
		names[id] = n
		return n
	}
	for _, item := range items {
		delete(item, "warmPoolState")
		id := asInt64(item["id"], 0)
		if id <= 0 || asInt(item["warmPool"], 1) == 0 || asInt(item["status"], 1) != 1 || active[id] == 0 {
			continue
		}
		var view *warmPoolView
		switch asInt(item["type"], 0) {
		case 2:
			view = h.tunnelWarmView(id, chainKeys, nodeName)
		case 1:
			view = h.forwardWarmView(id, ruleKeys, nodeName)
		}
		if view != nil {
			item["warmPoolState"] = view
		}
	}
}

// tunnelWarmView: 隧道转发 — per entry, follow the current target hop by hop
// (entry → middle → … → exit) and sum the hops' savings.
func (h *Handler) tunnelWarmView(tunnelID int64, keys []warmKeyStat, nodeName func(int64) string) *warmPoolView {
	owner := fmt.Sprintf("chains_%d", tunnelID)
	byNode := map[int64][]warmKeyStat{}
	for _, k := range keys {
		if k.key.Owner == owner {
			byNode[k.key.NodeID] = append(byNode[k.key.NodeID], k)
		}
	}
	if len(byNode) == 0 {
		return nil
	}
	rows, err := h.repo.ListChainNodesForTunnel(tunnelID)
	if err != nil || len(rows) == 0 {
		return nil
	}
	inNodes, chainHops, outNodes := splitChainNodeGroups(rows)
	layers := make([][]chainNodeRecord, 0, len(chainHops)+2)
	layers = append(layers, inNodes)
	layers = append(layers, chainHops...)
	layers = append(layers, outNodes)
	layerOf := map[int64]int{}
	for i, layer := range layers {
		for _, r := range layer {
			if _, ok := layerOf[r.NodeID]; !ok {
				layerOf[r.NodeID] = i
			}
		}
	}
	nextNode := func(from int64, addr string) int64 {
		li, ok := layerOf[from]
		if !ok || li+1 >= len(layers) {
			return 0
		}
		return h.warmAddrNode(layers[li+1], addr)
	}
	// pick: the key the node warms now (target), else the busiest one.
	pick := func(ks []warmKeyStat) *warmKeyStat {
		var best *warmKeyStat
		for i := range ks {
			k := &ks[i]
			switch {
			case best == nil:
				best = k
			case k.target && !best.target:
				best = k
			case k.target == best.target && k.sums.warmN+k.sums.missN > best.sums.warmN+best.sums.missN:
				best = k
			}
		}
		return best
	}
	var paths [][]warmPoolHopView
	var weights []int64
	for _, entry := range inNodes {
		ks := byNode[entry.NodeID]
		if len(ks) == 0 {
			continue
		}
		var hops []warmPoolHopView
		from := entry.NodeID
		visited := map[int64]bool{}
		for len(ks) > 0 && !visited[from] {
			visited[from] = true
			k := pick(ks)
			to := nextNode(from, k.key.Addr)
			hv := hopFromSums(k.sums, k.state)
			hv.FromNodeID, hv.ToNodeID = from, to
			hv.From = nodeName(from)
			if to > 0 {
				hv.To = nodeName(to)
			} else {
				hv.To = k.key.Addr
			}
			hops = append(hops, hv)
			if to == 0 {
				break
			}
			from = to
			ks = byNode[from]
		}
		if len(hops) == 0 {
			continue
		}
		paths = append(paths, hops)
		weights = append(weights, hops[0].WarmN)
	}
	return summarizeWarmPaths("tunnel", paths, weights)
}

// warmAddrNode maps a next-hop address the agent reported ("host:port", as
// the panel configured it) to the node of the next layer: by port (each
// node's relay port of a tunnel is its own), then by host on a tie.
func (h *Handler) warmAddrNode(layer []chainNodeRecord, addr string) int64 {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(portStr)
	var match []chainNodeRecord
	for _, r := range layer {
		if r.Port == port {
			match = append(match, r)
		}
	}
	if len(match) == 1 {
		return match[0].NodeID
	}
	for _, r := range match {
		if strings.TrimSpace(r.ConnectIP) == host {
			return r.NodeID
		}
		node, err := h.getNodeRecord(r.NodeID)
		if err != nil || node == nil {
			continue
		}
		for _, ip := range []string{node.ServerIP, node.ServerIPv4, node.ServerIPv6, node.IntranetIP} {
			if strings.Trim(strings.TrimSpace(ip), "[]") == host {
				return r.NodeID
			}
		}
		if strings.Contains(node.ExtraIPs, host) {
			return r.NodeID
		}
	}
	return 0
}

// forwardWarmView: 端口转发 — one path per rule target.
func (h *Handler) forwardWarmView(tunnelID int64, keys []warmKeyStat, nodeName func(int64) string) *warmPoolView {
	if len(keys) == 0 {
		return nil
	}
	forwards, err := h.repo.ListForwardsByTunnel(tunnelID)
	if err != nil || len(forwards) == 0 {
		return nil
	}
	ruleName := map[int64]string{}
	for _, f := range forwards {
		if f.Status == 1 {
			ruleName[f.ID] = strings.TrimSpace(f.Name)
		}
	}
	var paths [][]warmPoolHopView
	var weights []int64
	for _, k := range keys {
		fid := forwardIDOfService(k.key.Owner)
		name, ok := ruleName[fid]
		if !ok {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("规则 %d", fid)
		}
		hv := hopFromSums(k.sums, k.state)
		hv.FromNodeID = k.key.NodeID
		hv.From = name + " · " + nodeName(k.key.NodeID)
		hv.To = k.key.Addr
		paths = append(paths, []warmPoolHopView{hv})
		weights = append(weights, hv.WarmN)
	}
	return summarizeWarmPaths("forward", paths, weights)
}

func forwardIDOfService(service string) int64 {
	i := strings.IndexByte(service, '_')
	if i <= 0 {
		return 0
	}
	id, _ := strconv.ParseInt(service[:i], 10, 64)
	return id
}

// ---- switch ----------------------------------------------------------------

// tunnelWarmPoolUpdate: POST /api/v1/tunnel/warm-pool {id, warmPool} — admin
// only (middleware). One column write, then the pools follow within seconds
// (SetWarmPools); no tunnel / rule redeploy, nothing re-created.
func (h *Handler) tunnelWarmPoolUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req map[string]interface{}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	id := asInt64(req["id"], 0)
	if id <= 0 {
		response.WriteJSON(w, response.ErrDefault("隧道ID不能为空"))
		return
	}
	if _, ok := req["warmPool"]; !ok {
		response.WriteJSON(w, response.ErrDefault("缺少预热池开关"))
		return
	}
	value := tunnelWarmPoolFromRequest(req, 1)
	if tr, err := h.repo.GetTunnelRecord(id); err != nil || tr == nil {
		response.WriteJSON(w, response.ErrDefault("隧道不存在"))
		return
	}
	if err := h.repo.SetTunnelWarmPoolTx(nil, id, value); err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	h.warmPoolsChanged()
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": id, "warmPool": value}))
}

// tunnelWarmPoolFromRequest reads warmPool / warm_pool (0 = off, else on).
func tunnelWarmPoolFromRequest(req map[string]interface{}, fallback int) int {
	for _, key := range []string{"warmPool", "warm_pool"} {
		if v, ok := req[key]; ok && v != nil {
			switch x := v.(type) {
			case bool:
				if x {
					return 1
				}
				return 0
			default:
				if asInt(v, fallback) == 0 {
					return 0
				}
				return 1
			}
		}
	}
	return fallback
}
