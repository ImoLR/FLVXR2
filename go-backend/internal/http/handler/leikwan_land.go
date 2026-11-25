package handler

// Plan 310 r3 — two per-minute 利群 values, kept in memory only:
//
//  1. 专线出口 → 落地 for the rule rows' 「到落地」 (= 全程 + this). On a rule's
//     native landing node the exit IP is the 利群 source the landing sees on
//     that rule's landing port (LandingPeerRtt: a passive sock_diag read on
//     the landing, re-read at most every 10 min, picked like 诊断 with
//     lkExitCands); then ONE ICMP echo per minute per (landing node, exit IP)
//     pair through the agents' existing ServiceMonitorCheck (no landing OTA).
//     An exit that does not answer ping → the landing's connection rtt
//     (shown 「≈」, may be high); a single lost echo keeps the last answer
//     ≤ 5 min. Rules sharing a (node, exit) pair share the echo.
//  2. 端内 = 全程 (UDP) − 入口段 per 专线 from the 利群入口 reports (agent
//     plan 310 r3: rttUdp + seg). The 专线 header shows the auto pick and
//     线路监测 「利群专线端内」 lines get one sample a minute from it (no
//     probe at all).
//
// Writes: only the 线路监测 samples of the 端内 lines an admin created
// (batched by the line monitor's flush). Logs: real errors only, ≤ 1 per
// 10 min. Idle panels (no 利群 rule) do one rule query a minute.

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-backend/internal/store/model"
)

const (
	lkLandEvery        = time.Minute
	lkLandPeersEvery   = 10 * time.Minute // LandingPeerRtt per landing port
	lkLandPeersRetry   = 2 * time.Minute  // sooner while no exit is known / the echo fails
	lkLandPeersMaxAge  = 30 * time.Minute // a known exit is dropped after this without a read
	lkLandHoldOK       = 5 * time.Minute  // a lost echo keeps the last answer this long
	lkLandStale        = 3 * time.Minute  // no attempt for this long → nothing shown
	lkLandTargetEvery  = 10 * time.Minute // landing node → (FLVX node, port) cache
	lkLandForget       = 30 * time.Minute // drop state nobody wanted for this long
	lkLandWorkers      = 4
	lkLandPingTimeoutS = 2
)

// Reasons a row has no 出口 → 落地 (API latencyNote; the row shows no tag).
const (
	lkLandManual   = "手动落地，没有落地延迟"
	lkLandExternal = "外部节点，没有落地延迟"
	lkLandShared   = "共享落地机上不测出口"
	lkLandOffline  = "落地机离线"
	lkLandNoExit   = "落地机上现在没有专线出口的连接"
	lkLandPending  = "落地延迟还没测到"
)

type lkLandKey struct {
	Node int64
	Port int
}

type lkPingKey struct {
	Node int64
	IP   string
}

type lkLandPort struct {
	peersAt, tried, seenAt time.Time
	exit                   lkLandingPeer
	haveExit               bool
}

type lkLandPing struct {
	at, okAt, seenAt time.Time
	ms               float64
	noAnswer         bool // the last echo got no answer
}

type lkLandTarget struct {
	key lkLandKey
	why string // "" = measurable
	at  time.Time
}

// lkLandValue: 专线出口 → 落地 of one rule's landing.
type lkLandValue struct {
	Ms     float64 `json:"ms"`
	Approx bool    `json:"approx,omitempty"` // exit does not answer ping: the connection rtt (≈, may be high)
	AgeS   int64   `json:"ageS"`
}

// lkEndInCand: one 端内 reading (a forward of the 专线 on one 利群入口).
type lkEndInCand struct {
	NodeID    int64
	NodeName  string
	ForwardID int64
	RuleName  string
	TotalMs   float64 // 全程 (UDP)
	SegMs     float64 // 入口段
	Ingress   string
	AgeS      int64 // 全程 age as of the tick (report age + client age)
}

func (c lkEndInCand) endIn() float64 { return math.Max(0, c.TotalMs-c.SegMs) }

type lkStickyKey struct{ line, node int64 }

type lkLandRuntime struct {
	mu        sync.Mutex
	ports     map[lkLandKey]*lkLandPort
	pings     map[lkPingKey]*lkLandPing
	targets   map[int64]lkLandTarget
	nodeIPs   map[string]bool
	nodeIPsAt time.Time
	endIn     map[int64][]lkEndInCand // 专线 → readings of the last tick
	sticky    map[lkStickyKey][2]int64
	auto      map[int64]lkEndInCand // 专线 → the auto pick of the last tick
	tickAt    time.Time
	errAt     atomic.Int64
	echoes    atomic.Int64 // ICMP echoes sent (plan doc numbers)
	peerReads atomic.Int64
}

func (rt *lkLandRuntime) init() {
	if rt.ports == nil {
		rt.ports, rt.pings, rt.targets = map[lkLandKey]*lkLandPort{}, map[lkPingKey]*lkLandPing{}, map[int64]lkLandTarget{}
		rt.endIn, rt.sticky, rt.auto = map[int64][]lkEndInCand{}, map[lkStickyKey][2]int64{}, map[int64]lkEndInCand{}
	}
}

func (h *Handler) lkLandErr(format string, args ...interface{}) {
	now := time.Now().Unix()
	if last := h.lkLand.errAt.Load(); now-last < 600 || !h.lkLand.errAt.CompareAndSwap(last, now) {
		return
	}
	log.Printf("[leikwan-land] "+format+" (logged at most every 10 min)", args...)
}

// runLeiKwanLandLoop: one pass a minute, a few seconds after the minute (the
// 线路监测 sample of that minute).
func (h *Handler) runLeiKwanLandLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	for {
		now := time.Now()
		next := now.Truncate(lkLandEvery).Add(5 * time.Second)
		if !next.After(now) {
			next = next.Add(lkLandEvery)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		h.leiKwanLandTick(ctx, time.Now())
	}
}

// leiKwanLandTick is one pass (tests call it directly).
func (h *Handler) leiKwanLandTick(ctx context.Context, now time.Time) {
	if h == nil || h.repo == nil {
		return
	}
	rules, err := h.repo.ListLeiKwanRules(0)
	if err != nil {
		h.lkLandErr("rules: %v", err)
		return
	}
	h.lkLandExits(ctx, rules, now)
	cands := h.lkEndInCands(rules, now)
	rt := &h.lkLand
	rt.mu.Lock()
	rt.init()
	rt.endIn, rt.tickAt = cands, now
	auto := map[int64]lkEndInCand{}
	for line, list := range cands {
		if c, ok := rt.pickLocked(line, 0, list); ok {
			auto[line] = c
		}
	}
	rt.auto = auto
	rt.mu.Unlock()
	h.lkEndInSamples(now)
}

// ---- 专线出口 → 落地 ----

func (h *Handler) lkLandOnline(nodeID int64) bool {
	switch {
	case leiKwanLabCommand != nil:
		return true
	case h.nodeOnlineFn != nil:
		return h.nodeOnlineFn(nodeID)
	case h.nodeCommandSender != nil:
		return true
	case h.wsServer != nil:
		return h.wsServer.NodeOnline(nodeID)
	}
	return false
}

// lkLandTargetFor: the FLVX node + port of a native landing node (cached).
func (h *Handler) lkLandTargetFor(landingNodeID int64, now time.Time) lkLandTarget {
	rt := &h.lkLand
	rt.mu.Lock()
	rt.init()
	t, ok := rt.targets[landingNodeID]
	rt.mu.Unlock()
	if ok && now.Sub(t.at) < lkLandTargetEvery {
		return t
	}
	t = lkLandTarget{at: now, why: lkLandPending}
	if n, s, err := h.repo.LeiKwanLandingTarget(landingNodeID); err == nil && n != nil && s != nil {
		switch {
		case s.Source == "shared" || s.NodeID <= 0:
			t.why = lkLandShared
		case n.Port <= 0:
			t.why = lkLandPending
		default:
			t.key, t.why = lkLandKey{Node: s.NodeID, Port: n.Port}, ""
		}
	}
	rt.mu.Lock()
	rt.targets[landingNodeID] = t
	rt.mu.Unlock()
	return t
}

func (h *Handler) lkLandSkipIPs(now time.Time) map[string]bool {
	rt := &h.lkLand
	rt.mu.Lock()
	if rt.nodeIPs != nil && now.Sub(rt.nodeIPsAt) < lkLandTargetEvery {
		out := rt.nodeIPs
		rt.mu.Unlock()
		return out
	}
	rt.mu.Unlock()
	out := map[string]bool{}
	hosts, err := h.repo.NodeGuardHosts()
	if err != nil {
		h.lkLandErr("node addresses: %v", err)
	}
	for _, raw := range hosts {
		if a, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]")); err == nil {
			out[a.Unmap().String()] = true
		}
	}
	rt.mu.Lock()
	rt.nodeIPs, rt.nodeIPsAt = out, now
	rt.mu.Unlock()
	return out
}

// lkLandPeers: LandingPeerRtt on the landing port (passive read there).
func (h *Handler) lkLandPeers(k lkLandKey) ([]lkLandingPeer, bool) {
	h.lkLand.peerReads.Add(1)
	res, err := h.sendNodeCommandWithTimeout(k.Node, "LandingPeerRtt", map[string]interface{}{"port": k.Port, "max": 16}, 12*time.Second, false, false)
	if err != nil || !res.Success || res.Data == nil {
		return nil, false
	}
	var d struct {
		V     int             `json:"v"`
		Peers []lkLandingPeer `json:"peers"`
	}
	raw, _ := json.Marshal(res.Data)
	if json.Unmarshal(raw, &d) != nil || d.V != 1 {
		return nil, false
	}
	return d.Peers, true
}

// lkLandEcho: one ICMP echo from the landing node to the exit (诊断's
// pingExit with a single echo). ok = answered.
func (h *Handler) lkLandEcho(k lkPingKey) (float64, bool) {
	h.lkLand.echoes.Add(1)
	res, err := h.sendNodeCommandWithTimeout(k.Node, "ServiceMonitorCheck",
		map[string]interface{}{"monitorId": 0, "type": "icmp", "target": k.IP, "timeoutSec": lkLandPingTimeoutS},
		time.Duration(lkLandPingTimeoutS+3)*time.Second, false, false)
	if err != nil || !res.Success || res.Data == nil {
		return 0, false
	}
	ok, _ := res.Data["success"].(bool)
	v, _ := res.Data["latencyMs"].(float64)
	if !ok || v < 0 || v > 2000 {
		return 0, false
	}
	if v == math.Trunc(v) {
		v += 0.5 // the agent reports whole ms (floored)
	}
	return v, true
}

func (h *Handler) lkLandExits(ctx context.Context, rules []model.LeiKwanRule, now time.Time) {
	keys := map[lkLandKey]bool{}
	for _, x := range rules {
		if x.LandingNodeID <= 0 || x.ExternalNodeID > 0 {
			continue
		}
		if t := h.lkLandTargetFor(x.LandingNodeID, now); t.why == "" {
			keys[t.key] = true
		}
	}
	rt := &h.lkLand
	want := map[lkPingKey]bool{}
	if len(keys) > 0 {
		skip := h.lkLandSkipIPs(now)
		ordered := make([]lkLandKey, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Slice(ordered, func(i, j int) bool {
			return ordered[i].Node < ordered[j].Node || ordered[i].Node == ordered[j].Node && ordered[i].Port < ordered[j].Port
		})
		for _, k := range ordered {
			if ctx.Err() != nil || !h.lkLandOnline(k.Node) {
				continue
			}
			rt.mu.Lock()
			rt.init()
			p := rt.ports[k]
			if p == nil {
				p = &lkLandPort{}
				rt.ports[k] = p
			}
			p.seenAt = now
			// Re-read at most every 2 min (a failing read too): every 10 min,
			// while no exit is known, or when an exit that answered ping
			// stopped answering (it may have rotated). An exit that never
			// answers ping is only re-read every 10 min.
			due := false
			if now.Sub(p.tried) >= lkLandPeersRetry {
				switch {
				case now.Sub(p.peersAt) >= lkLandPeersEvery, !p.haveExit:
					due = true
				default:
					pg := rt.pings[lkPingKey{k.Node, p.exit.IP}]
					due = pg != nil && pg.noAnswer && !pg.okAt.IsZero()
				}
			}
			if due {
				p.tried = now
			}
			rt.mu.Unlock()
			if due {
				if peers, ok := h.lkLandPeers(k); ok {
					list := lkExitCands(peers, skip)
					rt.mu.Lock()
					old := p.exit.IP
					p.peersAt, p.haveExit = now, len(list) > 0
					if p.haveExit {
						p.exit = list[0]
						// Same exit, silent past the hold → from now on it is an
						// exit that does not answer ping (≈, 10-min re-reads).
						if pg := rt.pings[lkPingKey{k.Node, old}]; p.exit.IP == old && pg != nil && pg.noAnswer && now.Sub(pg.okAt) > lkLandHoldOK {
							pg.okAt = time.Time{}
						}
					}
					rt.mu.Unlock()
				}
			}
			rt.mu.Lock()
			if p.haveExit && now.Sub(p.peersAt) > lkLandPeersMaxAge {
				p.haveExit = false
			}
			if p.haveExit {
				want[lkPingKey{k.Node, p.exit.IP}] = true
			}
			rt.mu.Unlock()
		}
	}
	// One echo per (landing node, exit IP), ≤ lkLandWorkers at a time.
	var wg sync.WaitGroup
	sem := make(chan struct{}, lkLandWorkers)
	for pk := range want {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(pk lkPingKey) {
			defer wg.Done()
			defer func() { <-sem }()
			ms, ok := h.lkLandEcho(pk)
			rt.mu.Lock()
			pg := rt.pings[pk]
			if pg == nil {
				pg = &lkLandPing{}
				rt.pings[pk] = pg
			}
			pg.at, pg.seenAt, pg.noAnswer = now, now, !ok
			if ok {
				pg.okAt, pg.ms = now, ms
			}
			rt.mu.Unlock()
		}(pk)
	}
	wg.Wait()
	rt.mu.Lock()
	rt.init()
	for k, p := range rt.ports {
		if now.Sub(p.seenAt) > lkLandForget {
			delete(rt.ports, k)
		}
	}
	for k, p := range rt.pings {
		if now.Sub(p.seenAt) > lkLandForget {
			delete(rt.pings, k)
		}
	}
	for k, t := range rt.targets {
		if now.Sub(t.at) > lkLandForget {
			delete(rt.targets, k)
		}
	}
	rt.mu.Unlock()
}

// lkLandFor: 专线出口 → 落地 of a rule now, or why there is none.
func (h *Handler) lkLandFor(x model.LeiKwanRule, now time.Time) (*lkLandValue, string) {
	switch {
	case x.ExternalNodeID > 0:
		return nil, lkLandExternal
	case x.LandingNodeID <= 0:
		return nil, lkLandManual
	}
	rt := &h.lkLand
	rt.mu.Lock()
	rt.init()
	t, ok := rt.targets[x.LandingNodeID]
	if !ok {
		rt.mu.Unlock()
		return nil, lkLandPending
	}
	if t.why != "" {
		rt.mu.Unlock()
		return nil, t.why
	}
	p := rt.ports[t.key]
	var pg *lkLandPing
	var exit lkLandingPeer
	var peersAt time.Time
	have := p != nil && p.haveExit
	if have {
		exit, peersAt = p.exit, p.peersAt
		if v := rt.pings[lkPingKey{t.key.Node, exit.IP}]; v != nil {
			c := *v
			pg = &c
		}
	}
	tried := p != nil && !p.tried.IsZero()
	rt.mu.Unlock()
	if !h.lkLandOnline(t.key.Node) {
		return nil, lkLandOffline
	}
	if !have {
		if tried {
			return nil, lkLandNoExit
		}
		return nil, lkLandPending
	}
	if pg == nil || now.Sub(pg.at) > lkLandStale {
		return nil, lkLandPending
	}
	age := func(t time.Time) int64 {
		if s := int64(now.Sub(t).Seconds()); s > 0 {
			return s
		}
		return 0
	}
	if !pg.okAt.IsZero() && now.Sub(pg.okAt) <= lkLandHoldOK {
		return &lkLandValue{Ms: lkRound(pg.ms), AgeS: age(pg.okAt)}, ""
	}
	conn := exit.MinRttMs
	if conn <= 0 {
		conn = exit.RttMs
	}
	if conn <= 0 {
		return nil, lkLandPending
	}
	return &lkLandValue{Ms: lkRound(conn), Approx: true, AgeS: age(peersAt)}, ""
}

// ---- 端内 ----

// lkEndInCands: every 端内 reading of this minute, per 专线 (only 利群入口
// online, with a fresh plan-310-r3 report, a UDP 全程 and a connection to
// the UDP ingress).
func (h *Handler) lkEndInCands(rules []model.LeiKwanRule, now time.Time) map[int64][]lkEndInCand {
	out := map[int64][]lkEndInCand{}
	if len(rules) == 0 {
		return out
	}
	setting, err := h.repo.GetLeiKwanSetting()
	if err != nil || setting == nil {
		return out
	}
	entries := h.leiKwanEntryNodes(setting.EntryGroupID)
	ids := map[int64]bool{}
	for _, e := range entries {
		if e.Online {
			ids[e.ID] = true
		}
	}
	reps := h.leiKwanHostRtts(ids)
	for _, e := range entries {
		hr := reps[e.ID]
		if !e.Online || hr == nil || hr.rtt < lkHostSegVersion || now.Sub(time.UnixMilli(hr.reportedAt)) > hr.fresh {
			continue
		}
		repAge := int64(now.Sub(time.UnixMilli(hr.reportedAt)).Seconds())
		if repAge < 0 {
			repAge = 0
		}
		for _, x := range rules {
			f := hr.forwards[x.ForwardID]
			if f == nil || f.Seg == nil || f.Seg.Err != "" || f.Seg.Conns <= 0 || f.Seg.Ms <= 0 {
				continue
			}
			src, c := lkClientPick(f.RttUDP, f.RttTCP)
			if src != "udp" || c == nil {
				continue
			}
			age := repAge
			if c.AgeS > 0 {
				age += c.AgeS
			}
			out[x.LineID] = append(out[x.LineID], lkEndInCand{NodeID: e.ID, NodeName: e.Name, ForwardID: x.ForwardID, RuleName: x.Name,
				TotalMs: c.RttMs, SegMs: f.Seg.Ms, Ingress: f.Seg.Endpoint, AgeS: age})
		}
	}
	return out
}

// pickLocked: the reading a 专线 shows (node 0 = any 利群入口): the one of
// the last pick while it is still there (no jumps between hosts / forwards),
// else the freshest 全程.
func (rt *lkLandRuntime) pickLocked(line, node int64, list []lkEndInCand) (lkEndInCand, bool) {
	key := lkStickyKey{line, node}
	cands := list
	if node > 0 {
		cands = nil
		for _, c := range list {
			if c.NodeID == node {
				cands = append(cands, c)
			}
		}
	}
	if len(cands) == 0 {
		delete(rt.sticky, key)
		return lkEndInCand{}, false
	}
	if prev, ok := rt.sticky[key]; ok {
		for _, c := range cands {
			if c.NodeID == prev[0] && c.ForwardID == prev[1] {
				return c, true
			}
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.AgeS < best.AgeS || c.AgeS == best.AgeS && (c.NodeID < best.NodeID || c.NodeID == best.NodeID && c.ForwardID < best.ForwardID) {
			best = c
		}
	}
	rt.sticky[key] = [2]int64{best.NodeID, best.ForwardID}
	return best, true
}

// lkEndInView: the 专线 header tag (admin list).
type lkEndInView struct {
	Ms        float64 `json:"ms"`
	TotalMs   float64 `json:"totalMs"`
	SegMs     float64 `json:"segMs"`
	NodeName  string  `json:"nodeName"`
	RuleName  string  `json:"ruleName,omitempty"`
	ForwardID int64   `json:"forwardId,omitempty"`
	Ingress   string  `json:"ingress,omitempty"` // 「腾讯云 · 广州」 label
	AgeS      int64   `json:"ageS"`
	Readings  int     `json:"readings"` // readings of this 专线 this minute
}

func (h *Handler) lkLineEndIn(lineID int64, admin bool, now time.Time) *lkEndInView {
	rt := &h.lkLand
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.init()
	c, ok := rt.auto[lineID]
	if !ok || now.Sub(rt.tickAt) > lkLandStale {
		return nil
	}
	age := c.AgeS + int64(now.Sub(rt.tickAt).Seconds())
	v := &lkEndInView{Ms: lkRound(c.endIn()), TotalMs: lkRound(c.TotalMs), SegMs: lkRound(c.SegMs), NodeName: c.NodeName,
		AgeS: age, Readings: len(rt.endIn[lineID])}
	if admin {
		v.RuleName, v.ForwardID = c.RuleName, c.ForwardID
		v.Ingress = strings.Trim(lkIngressLabel(c.Ingress)+" · "+lkEndpointHost(c.Ingress), " ·")
	}
	return v
}

// lkEndInSamples: one 线路监测 sample a minute per 「利群专线端内」 line.
func (h *Handler) lkEndInSamples(now time.Time) {
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		h.lkLandErr("line monitor lines: %v", err)
		return
	}
	ts := now.Truncate(time.Minute).Unix()
	found := false
	for _, l := range lines {
		if l.Kind != model.LineKindLeiKwanEndIn || l.Paused == 1 {
			continue
		}
		found = true
		rt := &h.lkLand
		rt.mu.Lock()
		c, ok := rt.pickLocked(l.LeiKwanLineID, l.SourceNodeID, rt.endIn[l.LeiKwanLineID])
		rt.mu.Unlock()
		if !ok {
			why := "没有可用的利群入口读数"
			if l.SourceNodeID > 0 {
				why = "这台利群入口现在没有读数"
			}
			h.lineMonitorPassiveGap(l.ID, ts, why)
			continue
		}
		h.lineMonitorPassive(l.ID, ts, c)
	}
	if found {
		h.lineMonitorFlush(false)
	}
}

// RunLeiKwanLandPass runs one per-minute 利群 pass now (contract tests).
func (h *Handler) RunLeiKwanLandPass() { h.leiKwanLandTick(context.Background(), time.Now()) }
