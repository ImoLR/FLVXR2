package handler

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
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
// destOrder handshake capability get it; others stay on the tunnel order.
// Decisions live in memory.
//
// Round 2 (user 11-01): a destination unreachable from every exit of its
// tunnel uses the tunnel order at once and is re-probed only at fixed points
// after that failure (+1, +3, +10 min, +1 h). A retry that answers is
// confirmed by the next round (1 min later): OK ⇒ rule-best again at once;
// not OK ⇒ the same schedule goes on at its next point. The schedule only
// starts over once the destination stayed reachable for 1 h (a flapping
// landing ends in the notice instead of looping). When the last point fails,
// 「规则最优」 is turned off for every rule of that tunnel using it (persisted
// per rule, admin notice) and it is no longer probed until an admin turns it
// back on. Schedules are memory only (a panel restart starts them over).
const destOrderMetaKey = "destOrder"

var (
	// ruleBestRetryOffsets: the retry points after a destination was found
	// unreachable from every exit.
	ruleBestRetryOffsets = []time.Duration{time.Minute, 3 * time.Minute, 10 * time.Minute, time.Hour}
	// ruleBestRetryEarly: a 60 s round may come a little before a point; it
	// takes the point when it is at most this early (the round nearest to it).
	ruleBestRetryEarly = pathLatencyInterval / 2
	// ruleBestConfirmDelay: a retry that answered is checked again this much
	// later (= the next round).
	ruleBestConfirmDelay = pathLatencyInterval
	// ruleBestHealthyReset: reachable this long without a break ⇒ the
	// schedule is forgotten (the next failure starts at +1 min again).
	ruleBestHealthyReset = time.Hour
)

// ruleBestMaxProbes caps the new exit→target probes of one round (prod
// 2026-10-31: 14; 200 rules: ~210). Destinations beyond it keep the tunnel
// order this round — a safety net so the round stays well under 60 s.
var ruleBestMaxProbes = 300

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
	Rules     []repo.RuleBestForward // the tunnel's eligible rules using it
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
// the chain on the entry), running rules only, not auto-off (round 2).
func ruleBestForwardEligible(f repo.RuleBestForward) bool {
	if f.Status != 1 || f.RuleBestOff() {
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
			i, seen := byKey[key]
			if !seen {
				i = len(dests)
				byKey[key] = i
				dests = append(dests, ruleBestPlanDest{Key: key, Label: label, Probe: target})
			}
			if n := len(dests[i].Rules); n == 0 || dests[i].Rules[n-1].ID != f.ID {
				dests[i].Rules = append(dests[i].Rules, f)
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
			if !h.ruleBest.probeDue(ruleBestDeadKey{TunnelID: plan.TunnelID, Dest: dest.Key}, now) {
				dest.BackedOff = true
				continue
			}
			if probes+len(exits) > ruleBestMaxProbes {
				dest.BackedOff = true // over the cap: tunnel order this round
				h.ruleBest.countCapped()
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
	Recovered  bool // destination reachable again: best at once (round 2)
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

// ruleBestDeadKey: the retry schedule is per tunnel (its own exits) and
// destination.
type ruleBestDeadKey struct {
	TunnelID int64
	Dest     string
}

// ruleBestDeadState is the retry schedule of one destination (round 2).
type ruleBestDeadState struct {
	Since       time.Time // the failure the pending points count from
	Next        int       // index of the next pending point in ruleBestRetryOffsets
	Dead        bool      // tunnel order; probed only at the points
	ConfirmAt   time.Time // a retry answered: confirm at the round ≥ this
	RecoveredAt time.Time // reachable again since (forgotten 1 h later)
	OffLogged   bool      // a failed auto-off write was logged once
}

func (st *ruleBestDeadState) exhausted() bool {
	return st.Dead && st.ConfirmAt.IsZero() && st.Next >= len(ruleBestRetryOffsets)
}

// due: probe this round? Reachable destinations every round; dead ones only
// at the confirmation or the next pending point.
func (st *ruleBestDeadState) due(now time.Time) bool {
	switch {
	case st == nil || !st.Dead:
		return true
	case !st.ConfirmAt.IsZero():
		return !now.Before(st.ConfirmAt.Add(-ruleBestRetryEarly))
	case st.Next >= len(ruleBestRetryOffsets):
		return false
	default:
		return !now.Before(st.Since.Add(ruleBestRetryOffsets[st.Next] - ruleBestRetryEarly))
	}
}

// ruleBestOffEvent: every retry failed — 「规则最优」 goes off for these rules.
type ruleBestOffEvent struct {
	Key   ruleBestDeadKey
	Label string
	Rules []repo.RuleBestForward
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
	dead   map[ruleBestDeadKey]*ruleBestDeadState
	// Owners whose agent may still hold a destOrder entry memory no longer
	// has (rule deleted, destination dead): one cleanup push is due.
	dirty map[bestPathKey]bool
	views map[int64]*ruleBestView
	// Since the last hourly summary.
	switches, deferred, pushFailures, probes, deadRounds, capped int64
	retries, autoOff                                             int64
	lastDests                                                    int
}

func newRuleBestManager() *ruleBestManager {
	return &ruleBestManager{
		states: make(map[ruleBestKey]*ruleBestState),
		dead:   make(map[ruleBestDeadKey]*ruleBestDeadState),
		dirty:  make(map[bestPathKey]bool),
		views:  make(map[int64]*ruleBestView),
	}
}

func (m *ruleBestManager) probeDue(key ruleBestDeadKey, now time.Time) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dead[key].due(now)
}

// updateDead moves every destination probed this round through its retry
// schedule (see the top of the file). It returns the destinations on the
// tunnel order (dead or confirming), those reachable again this round
// (rule-best at once, no 2-round wait) and the auto-off events (all retries
// failed; repeated each round until forgetDead after the write).
// Destinations no longer used are dropped.
func (m *ruleBestManager) updateDead(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, now time.Time) (dead, recovered map[ruleBestDeadKey]bool, offs []ruleBestOffEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	used := make(map[ruleBestDeadKey]bool)
	recovered = make(map[ruleBestDeadKey]bool)
	last := len(ruleBestRetryOffsets)
	for _, plan := range plans {
		if plan.RuleBest == nil {
			continue
		}
		for _, d := range plan.RuleBest.Dests {
			key := ruleBestDeadKey{TunnelID: plan.TunnelID, Dest: d.Key}
			used[key] = true
			answered, ok := false, false
			for _, pk := range d.Exits {
				if res, found := results[pk]; found {
					answered = true
					ok = ok || res.OK
				}
			}
			st := m.dead[key]
			off := false
			switch {
			case !answered:
				off = st != nil && st.exhausted() // auto-off write still pending
			case st == nil:
				if !ok {
					m.dead[key] = &ruleBestDeadState{Since: now, Dead: true}
					m.deadRounds++
				}
			case !st.Dead: // reachable again, within the hour
				if ok {
					if !now.Before(st.RecoveredAt.Add(ruleBestHealthyReset)) {
						delete(m.dead, key)
					}
					break
				}
				// Died again: the old schedule goes on (its next point, counted
				// from now); nothing left ⇒ off.
				st.Dead, st.Since, st.ConfirmAt = true, now, time.Time{}
				m.deadRounds++
				off = st.Next >= last
			case !st.ConfirmAt.IsZero(): // the confirmation round
				st.ConfirmAt = time.Time{}
				if ok {
					st.Dead, st.RecoveredAt = false, now
					recovered[key] = true
					break
				}
				off = st.Next >= last
			default: // a retry point
				st.Next++
				m.retries++
				if ok {
					st.ConfirmAt = now.Add(ruleBestConfirmDelay)
					break
				}
				off = st.Next >= last
			}
			if off {
				offs = append(offs, ruleBestOffEvent{Key: key, Label: d.Label, Rules: d.Rules})
			}
		}
	}
	for key := range m.dead {
		if !used[key] {
			delete(m.dead, key)
		}
	}
	dead = make(map[ruleBestDeadKey]bool, len(m.dead))
	for key, st := range m.dead {
		if st.Dead {
			dead[key] = true
		}
	}
	return dead, recovered, offs
}

// forgetDead drops a destination's schedule (auto-off written, or an admin
// turned 「规则最优」 back on).
func (m *ruleBestManager) forgetDead(keys ...ruleBestDeadKey) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		delete(m.dead, key)
	}
}

// offWriteFailed reports whether this is the first failed auto-off write of
// the destination (logged once, retried every round).
func (m *ruleBestManager) offWriteFailed(key ruleBestDeadKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.dead[key]
	if st == nil || st.OffLogged {
		return false
	}
	st.OffLogged = true
	return true
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
func (m *ruleBestManager) evaluate(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, dead, recovered map[ruleBestDeadKey]bool, tunnelOrder func(bestPathKey) []int64, owner func(int64) (online, capable bool), now time.Time) []ruleBestPush {
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
			deadKey := ruleBestDeadKey{TunnelID: plan.TunnelID, Dest: dest.Key}
			if dest.BackedOff || dead[deadKey] {
				continue // tunnel order meanwhile (落地自己挂了 ≠ reason to switch)
			}
			// Reachable again (confirmed): the best decision at once.
			again := recovered[deadKey]
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
						if st.PendingN < bestPathConfirmRounds && !again {
							continue
						}
						sw.Gain = cur.CostMS - best.CostMS
						sw.Recovered = again && st.PendingN < bestPathConfirmRounds
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

// ruleBestCounters is the hourly summary.
type ruleBestCounters struct {
	States, Dests, Dead                                                        int
	Switches, Deferred, Failures, Probes, DeadRounds, Capped, Retries, AutoOff int64
}

// takeCounters returns and resets the counters of the hourly summary.
func (m *ruleBestManager) takeCounters() (c ruleBestCounters) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.dead {
		if st.Dead {
			c.Dead++
		}
	}
	c.States, c.Dests = len(m.states), m.lastDests
	c.Switches, c.Deferred, c.Failures, c.Probes, c.DeadRounds, c.Capped, c.Retries, c.AutoOff = m.switches, m.deferred, m.pushFailures, m.probes, m.deadRounds, m.capped, m.retries, m.autoOff
	m.switches, m.deferred, m.pushFailures, m.probes, m.deadRounds, m.capped, m.retries, m.autoOff = 0, 0, 0, 0, 0, 0, 0, 0
	return
}

func (m *ruleBestManager) countAutoOff() {
	m.mu.Lock()
	m.autoOff++
	m.mu.Unlock()
}

func (m *ruleBestManager) countCapped() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.capped++
	m.mu.Unlock()
}

// storeViews keeps the latest round's data per tunnel for the rule list.
func (m *ruleBestManager) storeViews(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, dead map[ruleBestDeadKey]bool, now time.Time) {
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
				vd := ruleBestViewDest{Label: d.Label, BackedOff: d.BackedOff, Dead: dead[ruleBestDeadKey{TunnelID: plan.TunnelID, Dest: d.Key}], Exit: make(map[int64]float64, len(d.Exits))}
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

// runRuleBestRound moves the retry schedules (clocked by the round's start,
// the time attachRuleBestProbes checked them with), decides and applies after
// the plan-165 round, under the same storm guard.
func (h *Handler) runRuleBestRound(plans []pathLatencyPlan, results map[pathProbeKey]pathProbeResult, guard *bestPathPushGuard, roundStart, now time.Time) {
	if h == nil || h.ruleBest == nil {
		return
	}
	dead, recovered, offs := h.ruleBest.updateDead(plans, results, roundStart)
	h.applyRuleBestOffs(offs, now)
	h.ruleBest.storeViews(plans, results, dead, now)
	pushes := h.ruleBest.evaluate(plans, results, dead, recovered, func(k bestPathKey) []int64 {
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
		switch {
		case sw.Failover:
			reason, from = "failover", "unreachable"
		case sw.Recovered:
			reason = "recovered"
		}
		log.Printf("[rule-best] switch tunnel=%d owner=%s(%d) dest=%s layer=%d %s(%d) %s -> %s(%d) %.0fms reason=%s",
			sw.Key.TunnelID, nodeDisplayName(state.Nodes[owner]), owner, sw.Key.Dest, sw.Layer,
			nodeDisplayName(state.Nodes[sw.FromID]), sw.FromID, from,
			nodeDisplayName(state.Nodes[sw.ToID]), sw.ToID, sw.ToCost, reason)
	}
	return nil
}

// ---- round 2: auto-off and re-enable --------------------------------------

// applyRuleBestOffs turns 「规则最优」 off for the rules of every destination
// whose retries all failed: one DB write and one log line per rule, then the
// schedule is dropped (the rules are no longer eligible, so the destination
// is not probed again and its decisions are gone — the cleanup push of the
// dead destination already ran when it first failed). A failed write is
// retried the next round (logged once).
func (h *Handler) applyRuleBestOffs(offs []ruleBestOffEvent, now time.Time) {
	if h == nil || h.repo == nil || len(offs) == 0 {
		return
	}
	for _, off := range offs {
		var writeErr error
		for _, f := range off.Rules {
			changed, err := h.repo.SetForwardRuleBestOff(f.ID, f.TunnelID, f.RemoteAddr, off.Label, now.UnixMilli())
			if err != nil {
				writeErr = err
				continue
			}
			if changed {
				h.ruleBest.countAutoOff()
				log.Printf("[rule-best] auto-off rule=%s(%d) tunnel=%d target=%s: unreachable from every exit, retries at 1/3/10/60 min failed; 规则最优 off until an admin turns it back on",
					f.Name, f.ID, f.TunnelID, off.Label)
			}
		}
		if writeErr != nil {
			if h.ruleBest.offWriteFailed(off.Key) {
				log.Printf("[rule-best] auto-off write failed tunnel=%d target=%s (retried each round): %v", off.Key.TunnelID, off.Label, writeErr)
			}
			continue
		}
		h.ruleBest.forgetDead(off.Key)
	}
}

// ruleBestOffNotice is the admin notice text of an auto-off rule.
func ruleBestOffNotice(name, target string) string {
	return fmt.Sprintf("规则「%s」的落地 %s 从所有出口都连不上（已重试 1/3/10/60 分钟），已自动关闭规则最优，请检查出口或落地", name, target)
}

// decorateRuleBestOff marks the auto-off rules of an admin's rule list
// (ruleBestOff: target, time, notice) with one query and returns how many.
// Users never get it: their rule line just shows the tunnel pick.
func (h *Handler) decorateRuleBestOff(items []map[string]interface{}) int {
	if h == nil || h.repo == nil || len(items) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, asInt64(item["id"], 0))
	}
	offs, err := h.repo.ForwardRuleBestOffs(ids)
	if err != nil {
		log.Printf("[rule-best] read auto-off marks: %v", err)
		return 0
	}
	count := 0
	for _, item := range items {
		off, ok := offs[asInt64(item["id"], 0)]
		if !ok {
			continue
		}
		item["ruleBestOff"] = map[string]interface{}{
			"target": off.Target,
			"at":     off.Time,
			"text":   ruleBestOffNotice(asString(item["name"]), off.Target),
		}
		count++
	}
	return count
}

// reenableRuleBest turns 「规则最优」 back on for a rule (admin): the mark is
// cleared and the schedules of its destinations on its tunnel are dropped,
// so the next round probes them as new.
func (h *Handler) reenableRuleBest(forwardID int64) error {
	f, err := h.getForwardRecord(forwardID)
	if err != nil {
		if errors.Is(err, errForwardNotFound) {
			return errors.New("转发不存在")
		}
		return err
	}
	cleared, err := h.repo.ClearForwardRuleBestOff(forwardID)
	if err != nil {
		return err
	}
	var keys []ruleBestDeadKey
	for _, raw := range splitRemoteTargets(f.RemoteAddr) {
		if key := normalizeDestKey(raw); key != "" {
			keys = append(keys, ruleBestDeadKey{TunnelID: f.TunnelID, Dest: key})
		}
	}
	h.ruleBest.forgetDead(keys...)
	if cleared {
		log.Printf("[rule-best] re-enabled rule=%s(%d) tunnel=%d by an admin", f.Name, f.ID, f.TunnelID)
	}
	return nil
}

// forwardRuleBestReenable: POST /api/v1/forward/rule-best-reenable {id} —
// admin only; panel-side, the next 60 s round picks the rule up again.
func (h *Handler) forwardRuleBestReenable(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if err := h.reenableRuleBest(req.ID); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}
