package handler

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/repo"
)

// Plan 180 「规则最优」. On a tunnel with the switch on (tunnel.rule_best, default
// 1) and ≥ 1 最优 layer, every distinct target of its rules (a destination) is
// also probed from every exit in the same 60 s path-latency round, and every
// 最优 owner (entry / 中转) gets a decision per destination:
//
//	cost(candidate) = edge(owner→candidate) + best downstream to an exit + exit→destination
//
// with the plan 165/179 switch rules (first place changes when ≥ 5 ms faster
// in 2 consecutive rounds, at once when the current path fails). A
// destination whose decision differs from the tunnel's first place is written
// into the owner's chain hop as metadata.destOrder (the agent then orders the
// hop for dials to that destination); equal ⇒ no entry. Only agents with the
// destOrder handshake capability get it; others stay on the tunnel order. A
// destination unreachable from every exit is probed only every 5 min and
// uses the tunnel order meanwhile. Everything lives in memory.
const (
	ruleBestDeadBackoff = 5 * time.Minute
	destOrderMetaKey    = "destOrder"
)

// ruleBestTarget is what the exits probe for a destination.
type ruleBestTarget struct {
	Host string
	Port int
}

func (t ruleBestTarget) String() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

type ruleBestPlanDest struct {
	Key       string // destOrder key: the target as the entry dials it, normalised
	Label     string // display: the target (chain rules: their upstream)
	Probe     ruleBestTarget
	Exits     map[int64]pathProbeKey // exit node → probe (empty when backed off)
	BackedOff bool
}

type ruleBestPlan struct {
	Dests    []ruleBestPlanDest
	Forwards map[int64][]string // eligible rule → its destination keys
}

// normalizeDestKey matches the agent's hop.NormalizeDestKey: lower-case host
// without brackets joined with the port.
func normalizeDestKey(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.ToLower(addr)
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if i := strings.IndexByte(host, '%'); i >= 0 && strings.Contains(host, ":") {
		host = host[:i]
	}
	return net.JoinHostPort(host, port)
}

func splitDestTarget(addr string) (ruleBestTarget, bool) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return ruleBestTarget{}, false
	}
	p, err := strconv.Atoi(port)
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if err != nil || p <= 0 || p > 65535 || host == "" {
		return ruleBestTarget{}, false
	}
	return ruleBestTarget{Host: host, Port: p}, true
}

func isLoopbackDestHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// ruleBestForwardEligible: gost rules only (nftables / wg_path rules bypass
// the chain on the entry), running rules only.
func ruleBestForwardEligible(f repo.RuleBestForward) bool {
	if f.Status != 1 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(f.Mode)) {
	case "", "gost":
		return true
	default:
		return false
	}
}

// planHasBestLayer: the tunnel has a 最优 layer that rule-best can decide.
func planHasBestLayer(plan pathLatencyPlan) bool {
	for g := 1; g < len(plan.Groups); g++ {
		if isBestLayer(plan.Groups[g]) && !(plan.Pipe && g == 1) {
			return true
		}
	}
	return false
}

// planRuleBestUseful: exit→target can only change a pick when the exit layer
// itself is 最优 (with one fixed exit, or a non-最优 exit layer, it adds the
// same amount to every candidate), so only such tunnels get the probes.
func planRuleBestUseful(plan pathLatencyPlan) bool {
	last := len(plan.Groups) - 1
	return plan.RuleBestOn && plan.Active && last >= 1 && isBestLayer(plan.Groups[last]) && !(plan.Pipe && last == 1)
}

// ruleBestDestinations collects the distinct destinations of a tunnel's
// eligible rules. Chain rules (plan 108) dial an exit-local copy of the
// user's node (loopback target, a copy on every exit); their exit→target leg
// is exit→upstream, so that is what gets probed. Other loopback targets are
// skipped (they would only measure the exit itself).
func ruleBestDestinations(forwards []repo.RuleBestForward, chainUpstreams map[int64]string) ([]ruleBestPlanDest, map[int64][]string) {
	byKey := make(map[string]int)
	var dests []ruleBestPlanDest
	perForward := make(map[int64][]string)
	for _, f := range forwards {
		if !ruleBestForwardEligible(f) {
			continue
		}
		for _, raw := range splitRemoteTargets(f.RemoteAddr) {
			key := normalizeDestKey(raw)
			target, ok := splitDestTarget(raw)
			if key == "" || !ok {
				continue
			}
			label := raw
			if up, chained := chainUpstreams[f.ID]; chained {
				if t, ok := splitDestTarget(up); ok {
					target, label = t, up
				} else {
					continue
				}
			} else if isLoopbackDestHost(target.Host) {
				continue
			}
			if _, seen := byKey[key]; !seen {
				byKey[key] = len(dests)
				dests = append(dests, ruleBestPlanDest{Key: key, Label: label, Probe: target})
			}
			perForward[f.ID] = appendUniqueString(perForward[f.ID], key)
		}
	}
	sort.SliceStable(dests, func(i, j int) bool { return dests[i].Key < dests[j].Key })
	return dests, perForward
}

// attachRuleBestProbes adds the rule-best destinations and their exit probes
// to the round's plans. One probe per distinct (exit, probe host, port): the
// pairs map dedupes across tunnels and with the edges.
func (h *Handler) attachRuleBestProbes(plans []pathLatencyPlan, pairs map[pathProbeKey]*nodeRecord, now time.Time) int {
	if h == nil || h.ruleBest == nil {
		return 0
	}
	need := false
	for i := range plans {
		if planRuleBestUseful(plans[i]) {
			need = true
			break
		}
	}
	if !need || h.repo == nil {
		return 0
	}
	forwards, err := h.repo.ListRuleBestForwards()
	if err != nil {
		return 0
	}
	upstreams, err := h.repo.ListActiveChainUpstreams()
	if err != nil {
		upstreams = nil
	}
	nodes := make(map[int64]*nodeRecord)
	load := func(id int64) *nodeRecord {
		if n, ok := nodes[id]; ok {
			return n
		}
		n, _ := h.getNodeRecord(id)
		nodes[id] = n
		return n
	}
	byTunnel := make(map[int64][]repo.RuleBestForward)
	for _, f := range forwards {
		byTunnel[f.TunnelID] = append(byTunnel[f.TunnelID], f)
	}
	probes := 0
	for i := range plans {
		plan := &plans[i]
		if !planRuleBestUseful(*plan) {
			continue
		}
		dests, perForward := ruleBestDestinations(byTunnel[plan.TunnelID], upstreams)
		if len(dests) == 0 {
			continue
		}
		exits := plan.Groups[len(plan.Groups)-1]
		for d := range dests {
			dest := &dests[d]
			if !h.ruleBest.probeDue(dest.Probe, now) {
				dest.BackedOff = true
				continue
			}
			dest.Exits = make(map[int64]pathProbeKey, len(exits))
			for _, exit := range exits {
				node := load(exit.NodeID)
				if node == nil || node.Status != 1 {
					continue
				}
				key := pathProbeKey{From: exit.NodeID, Host: dest.Probe.Host, Port: dest.Probe.Port}
				dest.Exits[exit.NodeID] = key
				if _, dup := pairs[key]; !dup {
					pairs[key] = node
					probes++
				}
			}
		}
		plan.RuleBest = &ruleBestPlan{Dests: dests, Forwards: perForward}
	}
	return probes
}

// ---- decisions ------------------------------------------------------------

type ruleBestKey struct {
	TunnelID    int64
	OwnerNodeID int64
	Dest        string
}

type ruleBestState struct {
	Layer       int
	Order       []int64 // decided order; nil = follows the tunnel order (no decision yet)
	PendingN    int
	Ranked      []bestPathCandidate
	Candidates  []int64 // the layer in DB order at the latest evaluation
	EvaluatedAt int64
	SwitchedAt  int64
}

type ruleBestSwitch struct {
	Key        ruleBestKey
	Layer      int
	FromID     int64
	ToID       int64
	FromCost   float64 // -1 = unreachable
	ToCost     float64
	Order      []int64
	Candidates []int64
	Failover   bool
	Gain       float64
}

// ruleBestPush is one UpdateChains to one owner: its destination changes of
// this round (none for a cleanup push).
type ruleBestPush struct {
	Key      bestPathKey
	Switches []ruleBestSwitch
	Cleanup  bool
	Failover bool
	Gain     float64
}

type ruleBestDeadState struct {
	NextProbe time.Time
}

// ruleBestView is the latest round's data of one tunnel for the rule list.
type ruleBestView struct {
	On        bool // switch on and the tunnel can use it
	Groups    [][]chainNodeRecord
	Pipe      bool
	Remote    map[int64]bool
	Edges     map[bestPathEdge]float64 // answered edges only
	Dests     map[string]ruleBestViewDest
	UpdatedAt int64
}

type ruleBestViewDest struct {
	Label     string
	Exit      map[int64]float64 // exit → destination, answered only
	BackedOff bool
	Dead      bool
}

type ruleBestManager struct {
	mu     sync.Mutex
	states map[ruleBestKey]*ruleBestState
	dead   map[ruleBestTarget]*ruleBestDeadState
	// Owners whose agent may still hold a destOrder entry memory no longer
	// has (rule deleted, destination dead): one cleanup push is due.
	dirty map[bestPathKey]bool
	views map[int64]*ruleBestView
	// Since the last hourly summary.
	switches, deferred, pushFailures, probes, deadRounds int64
	lastDests                                            int
}

func newRuleBestManager() *ruleBestManager {
	return &ruleBestManager{
		states: make(map[ruleBestKey]*ruleBestState),
		dead:   make(map[ruleBestTarget]*ruleBestDeadState),
		dirty:  make(map[bestPathKey]bool),
		views:  make(map[int64]*ruleBestView),
	}
}

func (m *ruleBestManager) probeDue(t ruleBestTarget, now time.Time) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.dead[t]
	return st == nil || !now.Before(st.NextProbe)
}

// updateDead records, per probe target probed this round, whether any exit
// reached it. Unreachable from every exit ⇒ next probe in 5 min. Targets no
// longer used are dropped.
func (m *ruleBestManager) updateDead(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, now time.Time) map[ruleBestTarget]bool {
	probed := make(map[ruleBestTarget]bool) // target → any exit OK
	used := make(map[ruleBestTarget]bool)
	for _, plan := range plans {
		if plan.RuleBest == nil {
			continue
		}
		for _, d := range plan.RuleBest.Dests {
			used[d.Probe] = true
			for _, key := range d.Exits {
				res, ok := results[key]
				if !ok {
					continue
				}
				probed[d.Probe] = probed[d.Probe] || res.OK
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for t, ok := range probed {
		if ok {
			delete(m.dead, t)
			continue
		}
		m.dead[t] = &ruleBestDeadState{NextProbe: now.Add(ruleBestDeadBackoff)}
		m.deadRounds++
	}
	for t := range m.dead {
		if !used[t] {
			delete(m.dead, t)
		}
	}
	dead := make(map[ruleBestTarget]bool, len(m.dead))
	for t := range m.dead {
		dead[t] = true
	}
	return dead
}

func (m *ruleBestManager) snapshot(key ruleBestKey) (ruleBestState, bool) {
	if m == nil {
		return ruleBestState{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[key]
	if st == nil {
		return ruleBestState{}, false
	}
	out := *st
	out.Order = append([]int64(nil), st.Order...)
	out.Ranked = append([]bestPathCandidate(nil), st.Ranked...)
	out.Candidates = append([]int64(nil), st.Candidates...)
	return out, true
}

func (m *ruleBestManager) view(tunnelID int64) *ruleBestView {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.views[tunnelID]
}

func (m *ruleBestManager) size() (states, dead, dirty, views int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.states), len(m.dead), len(m.dirty), len(m.views)
}

// ownerOrders returns the decided orders of one (tunnel, owner) by destination.
func (m *ruleBestManager) ownerOrders(key bestPathKey) map[string][]int64 {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out map[string][]int64
	for k, st := range m.states {
		if k.TunnelID != key.TunnelID || k.OwnerNodeID != key.OwnerNodeID || len(st.Order) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string][]int64)
		}
		out[k.Dest] = append([]int64(nil), st.Order...)
	}
	return out
}

// forgetTunnel drops a tunnel's decisions without a cleanup push (the caller
// redeploys the tunnel, or it is gone).
func (m *ruleBestManager) forgetTunnel(tunnelID int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.states {
		if k.TunnelID == tunnelID {
			delete(m.states, k)
		}
	}
	for k := range m.dirty {
		if k.TunnelID == tunnelID {
			delete(m.dirty, k)
		}
	}
	delete(m.views, tunnelID)
}

// evaluate runs one decision round. tunnelOrder is the applied plan-165 order
// of an owner (nil = DB order); owner reports a live agent session and the
// destOrder capability. It returns the confirmed first-place changes (not yet
// applied) grouped per owner, plus the owners due for a cleanup push, and
// drops every state that is no longer a rule-best (tunnel, owner, destination).
func (m *ruleBestManager) evaluate(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, dead map[ruleBestTarget]bool, tunnelOrder func(bestPathKey) []int64, owner func(int64) (online, capable bool), now time.Time) []ruleBestPush {
	m.mu.Lock()
	defer m.mu.Unlock()
	visited := make(map[ruleBestKey]bool)
	inPlan := make(map[int64]bool)
	var switches []ruleBestSwitch
	destCount := 0
	for _, plan := range plans {
		inPlan[plan.TunnelID] = true
		if plan.RuleBest == nil || !plan.Active || len(plan.Groups) < 2 {
			continue
		}
		seen := make(map[int64]int)
		for _, group := range plan.Groups {
			for _, row := range group {
				seen[row.NodeID]++
			}
		}
		last := len(plan.Groups) - 1
		for _, dest := range plan.RuleBest.Dests {
			if dest.BackedOff || dead[dest.Probe] {
				continue // tunnel order meanwhile (落地自己挂了 ≠ reason to switch)
			}
			destCount++
			exitCost := make(map[int64]float64, len(dest.Exits))
			for exitID, key := range dest.Exits {
				if res, ok := results[key]; ok && res.OK {
					exitCost[exitID] = res.LatencyMS
				}
			}
			costs, edges := bestPathCostsWith(plan, results, exitCost)
			for g := 1; g <= last; g++ {
				layer := plan.Groups[g]
				if !isBestLayer(layer) || (plan.Pipe && g == 1) {
					continue
				}
				dbOrder := make([]int64, 0, len(layer))
				for _, row := range layer {
					dbOrder = append(dbOrder, row.NodeID)
				}
				for _, ownerRow := range plan.Groups[g-1] {
					ownerID := ownerRow.NodeID
					if plan.Remote[ownerID] || seen[ownerID] > 1 {
						continue
					}
					online, capable := owner(ownerID)
					if online && !capable {
						continue // agent without destOrder: tunnel order, state dropped below
					}
					key := ruleBestKey{TunnelID: plan.TunnelID, OwnerNodeID: ownerID, Dest: dest.Key}
					visited[key] = true
					st := m.states[key]
					if st == nil || st.Layer != g || !sameInt64Set(st.Candidates, dbOrder) {
						st = &ruleBestState{Layer: g}
						m.states[key] = st
					}
					st.Candidates = dbOrder
					if !online {
						continue // keep the decision; the reconnect redeploy carries it
					}
					ranked := rankBestCandidates(ownerID, layer, edges[g-1], costs[g])
					st.Ranked = ranked
					st.EvaluatedAt = now.UnixMilli()
					tunnelFirst := effectiveOrder(dbOrder, tunnelOrder(bestPathKey{TunnelID: plan.TunnelID, OwnerNodeID: ownerID}))
					current := tunnelFirst
					if len(st.Order) > 0 {
						current = effectiveOrder(dbOrder, st.Order)
					}
					curID := current[0]
					var cur bestPathCandidate
					for _, c := range ranked {
						if c.NodeID == curID {
							cur = c
							break
						}
					}
					best := ranked[0]
					sw := ruleBestSwitch{Key: key, Layer: g, FromID: curID, ToID: best.NodeID, FromCost: -1, ToCost: best.CostMS, Candidates: dbOrder}
					if cur.Reachable {
						sw.FromCost = cur.CostMS
					}
					switch {
					case !best.Reachable || best.NodeID == curID:
						st.PendingN = 0
						continue
					case !cur.Reachable:
						sw.Failover = true
					case cur.CostMS-best.CostMS >= bestPathSwitchMarginMS:
						st.PendingN++
						if st.PendingN < bestPathConfirmRounds {
							continue
						}
						sw.Gain = cur.CostMS - best.CostMS
					default:
						st.PendingN = 0
						continue
					}
					sw.Order = make([]int64, 0, len(ranked))
					for _, c := range ranked {
						sw.Order = append(sw.Order, c.NodeID)
					}
					switches = append(switches, sw)
				}
			}
		}
	}
	m.lastDests = destCount
	// Dropped decisions: a cleanup push removes their entry from the agent
	// when one was written (decided first ≠ the tunnel's first).
	for key, st := range m.states {
		if visited[key] {
			continue
		}
		bk := bestPathKey{TunnelID: key.TunnelID, OwnerNodeID: key.OwnerNodeID}
		if inPlan[key.TunnelID] && len(st.Order) > 0 && len(st.Candidates) > 0 {
			if effectiveOrder(st.Candidates, st.Order)[0] != effectiveOrder(st.Candidates, tunnelOrder(bk))[0] {
				m.dirty[bk] = true
			}
		}
		delete(m.states, key)
	}
	for bk := range m.dirty {
		if !inPlan[bk.TunnelID] {
			delete(m.dirty, bk)
		}
	}
	// One push per owner: its switches, failovers first, then the biggest gains.
	byOwner := make(map[bestPathKey]*ruleBestPush)
	var order []bestPathKey
	for _, sw := range switches {
		bk := bestPathKey{TunnelID: sw.Key.TunnelID, OwnerNodeID: sw.Key.OwnerNodeID}
		p := byOwner[bk]
		if p == nil {
			p = &ruleBestPush{Key: bk}
			byOwner[bk] = p
			order = append(order, bk)
		}
		p.Switches = append(p.Switches, sw)
		p.Failover = p.Failover || sw.Failover
		if sw.Gain > p.Gain {
			p.Gain = sw.Gain
		}
	}
	for bk := range m.dirty {
		if byOwner[bk] == nil {
			byOwner[bk] = &ruleBestPush{Key: bk, Cleanup: true}
			order = append(order, bk)
		}
	}
	pushes := make([]ruleBestPush, 0, len(order))
	for _, bk := range order {
		pushes = append(pushes, *byOwner[bk])
	}
	sort.SliceStable(pushes, func(i, j int) bool {
		if pushes[i].Failover != pushes[j].Failover {
			return pushes[i].Failover
		}
		if pushes[i].Cleanup != pushes[j].Cleanup {
			return !pushes[i].Cleanup
		}
		if pushes[i].Gain != pushes[j].Gain {
			return pushes[i].Gain > pushes[j].Gain
		}
		if pushes[i].Key.TunnelID != pushes[j].Key.TunnelID {
			return pushes[i].Key.TunnelID < pushes[j].Key.TunnelID
		}
		return pushes[i].Key.OwnerNodeID < pushes[j].Key.OwnerNodeID
	})
	return pushes
}

// setOrders applies a push's switches in memory and returns the previous
// orders for restoreOrders.
func (m *ruleBestManager) setOrders(p ruleBestPush) map[ruleBestKey][]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := make(map[ruleBestKey][]int64, len(p.Switches))
	for _, sw := range p.Switches {
		st := m.states[sw.Key]
		if st == nil {
			continue
		}
		prev[sw.Key] = st.Order
		st.Order = append([]int64(nil), sw.Order...)
	}
	return prev
}

func (m *ruleBestManager) restoreOrders(prev map[ruleBestKey][]int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, order := range prev {
		if st := m.states[k]; st != nil {
			st.Order = order
		}
	}
}

func (m *ruleBestManager) markPushed(p ruleBestPush, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dirty, p.Key)
	for _, sw := range p.Switches {
		if st := m.states[sw.Key]; st != nil {
			st.PendingN = 0
			st.SwitchedAt = at.UnixMilli()
		}
		m.switches++
	}
}

func (m *ruleBestManager) clearDirty(key bestPathKey) {
	m.mu.Lock()
	delete(m.dirty, key)
	m.mu.Unlock()
}

func (m *ruleBestManager) count(deferred, failures, probes int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.deferred += int64(deferred)
	m.pushFailures += int64(failures)
	m.probes += int64(probes)
	m.mu.Unlock()
}

// takeCounters returns and resets the counters of the hourly summary.
func (m *ruleBestManager) takeCounters() (states, dests, dead int, switches, deferred, failures, probes, deadRounds int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states, dests, dead = len(m.states), m.lastDests, len(m.dead)
	switches, deferred, failures, probes, deadRounds = m.switches, m.deferred, m.pushFailures, m.probes, m.deadRounds
	m.switches, m.deferred, m.pushFailures, m.probes, m.deadRounds = 0, 0, 0, 0, 0
	return
}

// storeViews keeps the latest round's data per tunnel for the rule list.
func (m *ruleBestManager) storeViews(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, dead map[ruleBestTarget]bool, now time.Time) {
	views := make(map[int64]*ruleBestView)
	for _, plan := range plans {
		if !plan.Active || !planHasBestLayer(plan) {
			continue
		}
		v := &ruleBestView{On: plan.RuleBestOn, Groups: plan.Groups, Pipe: plan.Pipe, Remote: plan.Remote, Edges: make(map[bestPathEdge]float64), UpdatedAt: now.UnixMilli()}
		for _, layer := range plan.Layers {
			for _, e := range layer {
				if res, ok := results[e.Key]; ok && res.OK {
					v.Edges[bestPathEdge{e.From, e.To}] = res.LatencyMS
				}
			}
		}
		if plan.RuleBest != nil {
			v.Dests = make(map[string]ruleBestViewDest, len(plan.RuleBest.Dests))
			for _, d := range plan.RuleBest.Dests {
				vd := ruleBestViewDest{Label: d.Label, BackedOff: d.BackedOff, Dead: dead[d.Probe], Exit: make(map[int64]float64, len(d.Exits))}
				for exitID, key := range d.Exits {
					if res, ok := results[key]; ok && res.OK {
						vd.Exit[exitID] = res.LatencyMS
					}
				}
				v.Dests[d.Key] = vd
			}
		}
		views[plan.TunnelID] = v
	}
	m.mu.Lock()
	m.views = views
	m.mu.Unlock()
}

// ---- runtime ----------------------------------------------------------------

func (h *Handler) destOrderCapable(nodeID int64) bool {
	if h.destOrderCapableFn != nil {
		return h.destOrderCapableFn(nodeID)
	}
	return h.wsServer.DestOrderCapable(nodeID)
}

// ruleBestDestOrders is the destOrder metadata of an owner's chain hop: the
// decided order (as node addresses of this hop) of every destination whose
// first place differs from the hop's first. nil when there is none, the
// tunnel's switch is off, the layer is not 最优 or the agent lacks destOrder.
func (h *Handler) ruleBestDestOrders(state *tunnelCreateState, ownerID int64, ordered []tunnelRuntimeNode, addrs []string) map[string][]string {
	if h == nil || h.ruleBest == nil || state == nil || !state.RuleBest || len(ordered) != len(addrs) || !isBestRuntimeLayer(ordered) {
		return nil
	}
	decided := h.ruleBest.ownerOrders(bestPathKey{TunnelID: state.TunnelID, OwnerNodeID: ownerID})
	if len(decided) == 0 || !h.destOrderCapable(ownerID) {
		return nil
	}
	ids := make([]int64, len(ordered))
	addrByID := make(map[int64]string, len(ordered))
	for i, t := range ordered {
		ids[i] = t.NodeID
		addrByID[t.NodeID] = addrs[i]
	}
	var out map[string][]string
	for dest, order := range decided {
		eff := effectiveOrder(ids, order)
		if len(eff) == 0 || eff[0] == ids[0] {
			continue // same first place as the hop: no entry
		}
		list := make([]string, 0, len(eff))
		for _, id := range eff {
			list = append(list, addrByID[id])
		}
		if out == nil {
			out = make(map[string][]string)
		}
		out[dest] = list
	}
	return out
}

// attachDestOrder writes destOrder into the (single) hop of a chain config.
func attachDestOrder(chain map[string]interface{}, destOrder map[string][]string) {
	if len(destOrder) == 0 || chain == nil {
		return
	}
	hops, ok := chain["hops"].([]map[string]interface{})
	if !ok || len(hops) != 1 {
		return
	}
	md, _ := hops[0]["metadata"].(map[string]interface{})
	if md == nil {
		md = make(map[string]interface{})
	}
	md[destOrderMetaKey] = destOrder
	hops[0]["metadata"] = md
}

func chainHopAddrs(chain map[string]interface{}) []string {
	hops, ok := chain["hops"].([]map[string]interface{})
	if !ok || len(hops) != 1 {
		return nil
	}
	nodes, ok := hops[0]["nodes"].([]map[string]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, asString(n["addr"]))
	}
	return out
}

// runRuleBestRound updates the back-off, decides and applies after the
// plan-165 round, under the same storm guard.
func (h *Handler) runRuleBestRound(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, guard *bestPathPushGuard, now time.Time) {
	if h == nil || h.ruleBest == nil {
		return
	}
	dead := h.ruleBest.updateDead(plans, results, now)
	h.ruleBest.storeViews(plans, results, dead, now)
	pushes := h.ruleBest.evaluate(plans, results, dead, func(k bestPathKey) []int64 {
		return h.bestPath.order(k)
	}, func(id int64) (bool, bool) {
		if !h.nodeAgentOnline(id) {
			return false, false
		}
		return true, h.destOrderCapable(id)
	}, now)
	if len(pushes) == 0 {
		return
	}
	deferred, failed := 0, 0
	for _, p := range pushes {
		owner := p.Key.OwnerNodeID
		if !guard.allow(owner) {
			deferred++
			continue
		}
		_, wasDeferred, wasFailed := guard.done(owner, h.applyRuleBestPush(p))
		if wasDeferred {
			deferred++
		}
		if wasFailed {
			failed++
		}
	}
	h.ruleBest.count(deferred, failed, 0)
}

// applyRuleBestPush pushes one owner's chain with its new destination orders
// (or a cleanup) through the gap-free UpdateChains, like applyBestPathSwitch:
// memory first (under the tunnel lock), rolled back when the push fails.
func (h *Handler) applyRuleBestPush(p ruleBestPush) error {
	unlock, ok := h.tryLockTunnelRuntime(p.Key.TunnelID)
	if !ok {
		return errBestPathBusy
	}
	defer unlock()
	owner := p.Key.OwnerNodeID
	if !h.destOrderCapable(owner) {
		h.ruleBest.clearDirty(p.Key)
		return errBestPathStale
	}
	tunnel, err := h.getTunnelRecord(p.Key.TunnelID)
	if err != nil || tunnel == nil || tunnel.Type != 2 || tunnel.Status != 1 {
		h.ruleBest.clearDirty(p.Key)
		return errBestPathStale
	}
	state, err := h.reconstructTunnelState(p.Key.TunnelID)
	if err != nil {
		return err
	}
	if node := state.Nodes[owner]; node == nil || node.IsRemote == 1 {
		h.ruleBest.clearDirty(p.Key)
		return errBestPathStale
	}
	if !state.RuleBest && !p.Cleanup {
		return errBestPathStale
	}
	if err := h.restoreFederationRuntimePorts(state); err != nil {
		return err
	}
	prev := h.ruleBest.setOrders(p)
	plan, err := h.buildTunnelRuntimePlan(state, owner)
	if err == nil && (plan.chainData == nil || plan.federation || !isBestRuntimeLayer(plan.targets)) {
		err = errBestPathStale
	}
	if err == nil && len(p.Switches) > 0 {
		ids := make([]int64, 0, len(plan.targets))
		for _, t := range plan.targets {
			ids = append(ids, t.NodeID)
		}
		if !sameInt64s(ids, p.Switches[0].Candidates) {
			err = errBestPathStale
		}
	}
	if err == nil {
		_, err = h.sendNodeCommandWithTimeout(owner, "UpdateChains", map[string]interface{}{
			"chain": fmt.Sprintf("chains_%d", p.Key.TunnelID),
			"data":  plan.chainData,
		}, bestPathPushTimeout, false, false)
	}
	if err != nil {
		h.ruleBest.restoreOrders(prev)
		if errors.Is(err, errBestPathStale) {
			h.ruleBest.clearDirty(p.Key)
		}
		return err
	}
	h.ruleBest.markPushed(p, time.Now())
	for _, sw := range p.Switches {
		reason := "faster"
		from := fmt.Sprintf("%.0fms", sw.FromCost)
		if sw.Failover {
			reason, from = "failover", "unreachable"
		}
		log.Printf("[rule-best] switch tunnel=%d owner=%s(%d) dest=%s layer=%d %s(%d) %s -> %s(%d) %.0fms reason=%s",
			sw.Key.TunnelID, nodeDisplayName(state.Nodes[owner]), owner, sw.Key.Dest, sw.Layer,
			nodeDisplayName(state.Nodes[sw.FromID]), sw.FromID, from,
			nodeDisplayName(state.Nodes[sw.ToID]), sw.ToID, sw.ToCost, reason)
	}
	return nil
}
