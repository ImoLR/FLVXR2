// Package warmpool keeps a few pre-dialed upstream connections (预热池, plan
// 244) so that the first bytes of a new stream skip the dial.
//
// Two kinds of owners, both configured by the panel with SetWarmPools (the
// full desired state of this agent):
//
//   - a chain (隧道转发, type 2): conns are TCP + TLS (Transport.Dial +
//     Handshake) to the chain hop's next node. The relay CONNECT (credential +
//     target) still runs per stream, so a pooled conn carries neither.
//   - a rule service (端口转发, type 1): conns are plain TCP to the rule target.
//
// Per owner only the node a stream would use now is warmed (the first node
// that is not fail-marked, i.e. the 最优 / fifo head) with `size` conns, plus
// 1 conn for up to 2 other nodes that real streams used in the last 5 min
// (round-robin, per-destination 规则最优 order). Conns live 90–110 s (the
// panel gives tunnel relays a 130 s request timeout), are watched while idle
// (a peer close is seen at once), are refilled in the background with at most
// one dial per key, and never count as a node failure. Peers that close idle
// conns young (< 40 s, three generations in a row) are not warmed for 30 min
// (「目标不支持预热」). The agent holds at most Cap pooled conns.
//
// Only counters are kept: they ride the existing metric message once a
// minute (Report). Nothing is logged per conn or per stream.
package warmpool

import (
	"context"
	"math/rand"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"
)

// CapVersion is advertised in the agent handshake (`warmPool=1`): the agent
// takes SetWarmPools and reports `warm_pool`. The panel sends neither to
// agents without it.
const CapVersion = 1

const (
	DefaultSize = 2
	MaxSize     = 4
	DefaultCap  = 64

	ageMin            = 90 * time.Second
	ageMax            = 110 * time.Second
	youngAge          = 40 * time.Second
	youngGenerations  = 3
	unsupportedFor    = 30 * time.Minute
	deadStreakRest    = 3
	restFor           = 60 * time.Second
	maxRest           = 5 * time.Minute
	failsBeforeRest   = 3
	recentWindow      = 5 * time.Minute
	maxRecentTargets  = 2
	tickEvery         = 5 * time.Second
	dialTimeout       = 10 * time.Second
	capLogEvery       = 10 * time.Minute
	poolDropAfter     = 10 * time.Minute
	minAdaptiveMaxAge = 30 * time.Second
)

// Kind of an owner.
type Kind byte

const (
	KindChain   Kind = 'c'
	KindService Kind = 's'
)

func (k Kind) String() string { return string(rune(k)) }

// Config is the full desired state from the panel: owner name → pool size.
type Config struct {
	Chains   map[string]int `json:"chains"`
	Services map[string]int `json:"services"`
}

// Target is one dial target of an owner, in the owner's current order.
type Target struct {
	Addr   string
	Failed bool // fail-marked recently: a stream would not pick it, not warmed
}

// Source gives the targets of an owner and dials one of them cold.
// Targets returns ok=false when the owner does not exist on this agent (chain
// not registered, rule service not running): its pools are closed at once.
type Source interface {
	Targets(owner string) ([]Target, bool)
	Dial(ctx context.Context, owner, addr string) (net.Conn, error)
}

type ownerKey struct {
	kind Kind
	name string
}

type poolKey struct {
	owner ownerKey
	addr  string
}

type owner struct {
	key    ownerKey
	size   int
	pools  map[string]*pool
	recent map[string]time.Time
	absent bool
}

type counters struct {
	coldN, missN, warmN, deadN, peerN, youngN, failN int64
	coldMs, missMs, warmMs                           float64
}

func (c *counters) empty() bool { return *c == counters{} }

type pool struct {
	key     poolKey
	idle    []*pconn // oldest first
	dialing int
	want    int
	removed bool

	restUntil        time.Time
	restLevel        int
	nextDialAt       time.Time
	dialFails        int
	deadStreak       int
	youngStreak      int
	lastYoungAt      time.Time
	unsupportedUntil time.Time
	unsupported      bool
	ageCap           time.Duration
	capped           bool
	lastUse          time.Time
	created          time.Time

	stats counters
}

// Manager holds every pool of this agent.
type Manager struct {
	mu      sync.Mutex
	sources map[Kind]Source
	owners  map[ownerKey]*owner
	retired []*pool
	live    int // idle pooled conns + dials in flight
	cap     int
	capHit  bool
	capLog  time.Time
	active  atomic.Pointer[map[ownerKey]struct{}]
	stop    chan struct{}
	rnd     *rand.Rand
	log     logger.Logger

	// test knobs
	now          func() time.Time
	tick         time.Duration
	ageMin       time.Duration
	ageMax       time.Duration
	manualTicker bool
}

// Default is the agent's manager.
var Default = New(DefaultCap)

// New returns an empty manager holding at most cap pooled conns.
func New(cap int) *Manager {
	if cap <= 0 {
		cap = DefaultCap
	}
	m := &Manager{
		sources: map[Kind]Source{KindService: &serviceSource{}},
		owners:  map[ownerKey]*owner{},
		cap:     cap,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
		now:     time.Now,
		tick:    tickEvery,
		ageMin:  ageMin,
		ageMax:  ageMax,
	}
	empty := map[ownerKey]struct{}{}
	m.active.Store(&empty)
	return m
}

func (m *Manager) logger() logger.Logger {
	if m.log != nil {
		return m.log
	}
	if l := logger.Default(); l != nil {
		return l.WithFields(map[string]any{"kind": "warmpool"})
	}
	return xlogger.Nop()
}

// SetSource installs the source of a kind (package chain installs KindChain).
func (m *Manager) SetSource(k Kind, s Source) {
	m.mu.Lock()
	m.sources[k] = s
	m.mu.Unlock()
}

// Active reports whether the owner has a pool configured (lock-free; called
// for every stream).
func (m *Manager) Active(k Kind, name string) bool {
	if name == "" {
		return false
	}
	_, ok := (*m.active.Load())[ownerKey{k, name}]
	return ok
}

// SetConfig applies the panel's full desired state. Owners not listed lose
// their pools at once (idle conns closed); new owners are warmed right away.
// It returns the number of owners configured.
func (m *Manager) SetConfig(cfg Config) int {
	want := map[ownerKey]int{}
	add := func(k Kind, items map[string]int) {
		for name, size := range items {
			if name == "" || size <= 0 {
				continue
			}
			if size > MaxSize {
				size = MaxSize
			}
			want[ownerKey{k, name}] = size
		}
	}
	add(KindChain, cfg.Chains)
	add(KindService, cfg.Services)

	m.mu.Lock()
	var closing []*pconn
	for key, o := range m.owners {
		if _, ok := want[key]; !ok {
			closing = append(closing, m.removeOwnerLocked(o)...)
		}
	}
	for key, size := range want {
		o := m.owners[key]
		if o == nil {
			o = &owner{key: key, pools: map[string]*pool{}, recent: map[string]time.Time{}}
			m.owners[key] = o
		}
		o.size = size
	}
	m.publishActiveLocked()
	for _, o := range m.owners {
		closing = append(closing, m.evaluateLocked(o)...)
	}
	m.ensureTickerLocked()
	n := len(m.owners)
	m.mu.Unlock()
	closeAll(closing)
	return n
}

// OwnerChanged re-reads an owner's targets now (chain updated / deleted,
// rule service started / closed).
func (m *Manager) OwnerChanged(k Kind, name string) {
	m.mu.Lock()
	o := m.owners[ownerKey{k, name}]
	var closing []*pconn
	if o != nil {
		if k == KindChain {
			// 最优 reorder / chain swap: only the new head is warmed from now
			// on; nodes used before the change drain by age.
			o.recent = map[string]time.Time{}
		}
		closing = m.evaluateLocked(o)
	}
	m.mu.Unlock()
	closeAll(closing)
}

func (m *Manager) publishActiveLocked() {
	next := make(map[ownerKey]struct{}, len(m.owners))
	for k := range m.owners {
		next[k] = struct{}{}
	}
	m.active.Store(&next)
}

func (m *Manager) removeOwnerLocked(o *owner) []*pconn {
	var closing []*pconn
	for _, p := range o.pools {
		closing = append(closing, m.retirePoolLocked(p)...)
	}
	delete(m.owners, o.key)
	return closing
}

// retirePoolLocked closes a pool's idle conns and keeps its counters until
// the next report.
func (m *Manager) retirePoolLocked(p *pool) []*pconn {
	closing := m.drainIdleLocked(p)
	p.removed = true
	p.want = 0
	m.retired = append(m.retired, p)
	return closing
}

func (m *Manager) drainIdleLocked(p *pool) []*pconn {
	closing := p.idle
	for _, c := range closing {
		c.state = stateClosed
		m.live--
	}
	p.idle = nil
	return closing
}

func (m *Manager) ensureTickerLocked() {
	if m.manualTicker {
		return
	}
	if len(m.owners) > 0 && m.stop == nil {
		m.stop = make(chan struct{})
		go m.runTicker(m.stop, m.tick)
	}
	if len(m.owners) == 0 && m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
}

func (m *Manager) runTicker(stop chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m.Tick()
		}
	}
}

// Tick retires conns past their age, re-reads targets and refills. It runs
// every 5 s while any owner is configured.
func (m *Manager) Tick() {
	m.mu.Lock()
	now := m.now()
	var closing []*pconn
	for _, o := range m.owners {
		for addr, ts := range o.recent {
			if now.Sub(ts) > recentWindow {
				delete(o.recent, addr)
			}
		}
		for _, p := range o.pools {
			keep := p.idle[:0]
			for _, c := range p.idle {
				if !now.Before(c.expire) {
					c.state = stateClosed
					m.live--
					closing = append(closing, c)
					continue
				}
				if now.Sub(c.born) >= youngAge {
					p.youngStreak = 0
				}
				keep = append(keep, c)
			}
			for i := len(keep); i < len(p.idle); i++ {
				p.idle[i] = nil
			}
			p.idle = keep
			if p.unsupported && !now.Before(p.unsupportedUntil) {
				p.unsupported = false
				p.youngStreak = 0
			}
		}
		closing = append(closing, m.evaluateLocked(o)...)
		for addr, p := range o.pools {
			if p.want == 0 && len(p.idle) == 0 && p.dialing == 0 && p.stats.empty() &&
				now.Sub(p.lastUse) > poolDropAfter && now.Sub(p.created) > poolDropAfter {
				delete(o.pools, addr)
			}
		}
	}
	m.mu.Unlock()
	closeAll(closing)
}

// evaluateLocked recomputes how many conns each target of o should hold and
// starts refills. It returns conns to close (owner absent).
func (m *Manager) evaluateLocked(o *owner) []*pconn {
	src := m.sources[o.key.kind]
	var targets []Target
	ok := false
	if src != nil {
		targets, ok = src.Targets(o.key.name)
	}
	now := m.now()
	want := map[string]int{}
	if ok {
		head := ""
		for _, t := range targets {
			if !t.Failed && t.Addr != "" {
				head = t.Addr
				break
			}
		}
		if head != "" {
			want[head] = o.size
		}
		extra := 0
		for _, t := range targets {
			if extra >= maxRecentTargets {
				break
			}
			if t.Failed || t.Addr == "" || t.Addr == head {
				continue
			}
			if ts, used := o.recent[t.Addr]; used && now.Sub(ts) <= recentWindow {
				want[t.Addr] = 1
				extra++
			}
		}
	}
	var closing []*pconn
	o.absent = !ok
	for addr, p := range o.pools {
		p.want = want[addr]
		if !ok {
			closing = append(closing, m.drainIdleLocked(p)...)
		}
	}
	for addr, n := range want {
		p := o.pools[addr]
		if p == nil {
			p = &pool{key: poolKey{o.key, addr}, created: now, lastUse: now}
			o.pools[addr] = p
		}
		p.want = n
	}
	for _, p := range o.pools {
		m.refillLocked(p)
	}
	return closing
}

func (m *Manager) refillLocked(p *pool) {
	if p.removed || p.dialing > 0 || len(p.idle) >= p.want {
		return
	}
	now := m.now()
	if now.Before(p.restUntil) || p.unsupported || now.Before(p.nextDialAt) {
		return
	}
	if m.live >= m.cap {
		m.noteCapLocked(p, now)
		return
	}
	p.capped = false
	src := m.sources[p.key.owner.kind]
	if src == nil {
		return
	}
	p.dialing = 1
	m.live++
	go m.dialLoop(p, src)
}

// noteCapLocked: p wanted a conn but the agent cap is reached (one log line
// per 10 min at most).
func (m *Manager) noteCapLocked(p *pool, now time.Time) {
	p.capped = true
	m.capHit = true
	if m.capLog.IsZero() || now.Sub(m.capLog) >= capLogEvery {
		m.capLog = now
		m.logger().Warnf("warm pool: agent cap of %d pooled connections reached; other pools are not refilled until conns free up", m.cap)
	}
}

func (m *Manager) dialLoop(p *pool, src Source) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		t0 := time.Now()
		conn, err := src.Dial(ctx, p.key.owner.name, p.key.addr)
		d := time.Since(t0)
		cancel()

		m.mu.Lock()
		now := m.now()
		if err != nil || conn == nil {
			p.stats.failN++
			p.dialFails++
			if p.dialFails >= failsBeforeRest {
				rest := restFor << uint(p.restLevel)
				if rest > maxRest {
					rest = maxRest
				}
				if p.restLevel == 0 {
					m.logger().Infof("warm pool %s %s → %s: %d dials failed, resting %s", p.key.owner.kind, p.key.owner.name, p.key.addr, p.dialFails, rest)
				}
				p.restUntil = now.Add(rest)
				p.restLevel++
				p.dialFails = 0
			} else {
				p.nextDialAt = now.Add(time.Duration(1<<uint(p.dialFails)) * time.Second)
			}
			p.dialing = 0
			m.live--
			m.mu.Unlock()
			return
		}
		p.dialFails = 0
		p.restLevel = 0
		p.stats.coldN++
		p.stats.coldMs += ms(d)
		o := m.owners[p.key.owner]
		if p.removed || o == nil || o.absent || len(p.idle) >= p.want || p.unsupported || now.Before(p.restUntil) {
			p.dialing = 0
			m.live--
			m.mu.Unlock()
			closeQuiet(conn)
			return
		}
		c := &pconn{Conn: conn, p: p, born: now, expire: now.Add(m.ageLocked(p)), done: make(chan struct{})}
		p.idle = append(p.idle, c)
		go c.watch(m)
		// The dial slot became an idle slot (live unchanged).
		if len(p.idle) < p.want && !now.Before(p.nextDialAt) {
			if m.live < m.cap {
				m.live++
				m.mu.Unlock()
				continue
			}
			m.noteCapLocked(p, now)
		}
		p.dialing = 0
		m.mu.Unlock()
		return
	}
}

func (m *Manager) ageLocked(p *pool) time.Duration {
	age := m.ageMin
	if span := m.ageMax - m.ageMin; span > 0 {
		age += time.Duration(m.rnd.Int63n(int64(span)))
	}
	if p.ageCap > 0 && p.ageCap < age {
		age = p.ageCap - time.Duration(m.rnd.Int63n(int64(p.ageCap/10)+1))
	}
	return age
}

// onWatchEnd: the idle watcher of c returned (peer closed, error or data).
func (m *Manager) onWatchEnd(c *pconn) {
	m.mu.Lock()
	if c.state != stateIdle {
		m.mu.Unlock()
		return
	}
	if c.n > 0 && c.err == nil {
		// The peer spoke first (e.g. an SSH banner): alive, the byte is kept.
		m.mu.Unlock()
		return
	}
	p := c.p
	now := m.now()
	for i, x := range p.idle {
		if x == c {
			p.idle = append(p.idle[:i], p.idle[i+1:]...)
			break
		}
	}
	c.state = stateDead
	m.live--
	m.peerClosedLocked(p, c, now)
	m.refillLocked(p)
	m.mu.Unlock()
	closeQuiet(c.Conn)
}

// peerClosedLocked: an idle pooled conn was closed by the peer at its age.
func (m *Manager) peerClosedLocked(p *pool, c *pconn, now time.Time) {
	p.stats.peerN++
	age := now.Sub(c.born)
	if age >= youngAge {
		p.youngStreak = 0
		// The peer has an idle limit above the young mark but below our age:
		// replace conns before it (≥ 30 s, else not worth warming).
		capAge := age * 3 / 4
		if capAge < minAdaptiveMaxAge {
			capAge = minAdaptiveMaxAge
		}
		if p.ageCap == 0 || capAge < p.ageCap {
			p.ageCap = capAge
		}
		return
	}
	p.stats.youngN++
	if c.born.After(p.lastYoungAt) {
		p.youngStreak++
	}
	p.lastYoungAt = now
	if p.youngStreak >= youngGenerations {
		p.youngStreak = 0
		p.unsupported = true
		p.unsupportedUntil = now.Add(unsupportedFor)
		closeLater(m.drainIdleLocked(p))
		m.logger().Infof("warm pool %s %s → %s: the peer closes idle connections after ~%.0fs; not warming it for %s",
			p.key.owner.kind, p.key.owner.name, p.key.addr, age.Seconds(), unsupportedFor)
		return
	}
	next := now.Add(time.Duration(p.youngStreak) * 2 * time.Second)
	if next.After(p.nextDialAt) {
		p.nextDialAt = next
	}
}

// Lease is a pooled conn handed to one stream.
type Lease struct {
	Conn  net.Conn
	m     *Manager
	p     *pool
	start time.Time
	took  time.Duration
	armed bool
	dead  bool
	done  bool
}

// Take hands out the oldest live pooled conn of (owner, addr), or nil.
func (m *Manager) Take(k Kind, name, addr string) *Lease {
	if !m.Active(k, name) {
		return nil
	}
	start := time.Now()
	m.mu.Lock()
	o := m.owners[ownerKey{k, name}]
	if o == nil {
		m.mu.Unlock()
		return nil
	}
	now := m.now()
	m.noteUseLocked(o, addr, now)
	p := o.pools[addr]
	for p != nil && len(p.idle) > 0 {
		c := p.idle[0]
		p.idle[0] = nil
		p.idle = p.idle[1:]
		c.state = stateTaken
		m.live--
		expired := !now.Before(c.expire)
		m.refillLocked(p)
		m.mu.Unlock()
		if !expired {
			if conn, ok := c.stopWatch(); ok {
				return &Lease{Conn: conn, m: m, p: p, start: start, took: time.Since(start)}
			}
			// The peer closed it just now (raced with the watcher).
			m.mu.Lock()
			m.peerClosedLocked(p, c, m.now())
			m.refillLocked(p)
			m.mu.Unlock()
		}
		closeQuiet(c.Conn)
		m.mu.Lock()
		now = m.now()
	}
	if p != nil {
		m.refillLocked(p)
	}
	m.mu.Unlock()
	return nil
}

// NoteMiss records a stream of (owner, addr) that found no pooled conn and
// dialed cold in d (d < 0: the cold dial failed, only the use is noted).
func (m *Manager) NoteMiss(k Kind, name, addr string, d time.Duration) {
	if !m.Active(k, name) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.owners[ownerKey{k, name}]
	if o == nil {
		return
	}
	m.noteUseLocked(o, addr, m.now())
	if p := o.pools[addr]; p != nil && d >= 0 {
		p.stats.missN++
		p.stats.missMs += ms(d)
	}
}

func (m *Manager) noteUseLocked(o *owner, addr string, now time.Time) {
	_, known := o.recent[addr]
	o.recent[addr] = now
	if p := o.pools[addr]; p != nil {
		p.lastUse = now
	}
	if !known && o.pools[addr] == nil {
		closeLater(m.evaluateLocked(o))
	}
}

// ArmConnect bounds the per-stream request on a pooled conn: unacknowledged
// data for 3 s means the conn is dead (TCP_USER_TIMEOUT), not a slow target.
func (l *Lease) ArmConnect() {
	if l != nil && !l.armed {
		l.armed = setUserTimeout(l.Conn, connectUserTimeout)
	}
}

// Disarm restores the conn's default TCP user timeout.
func (l *Lease) Disarm() {
	if l != nil && l.armed {
		setUserTimeout(l.Conn, 0)
		l.armed = false
	}
}

// Done records a stream that used the pooled conn (extra delay = the take).
func (l *Lease) Done() {
	if l == nil || l.done {
		return
	}
	l.done = true
	l.m.mu.Lock()
	l.p.stats.warmN++
	l.p.stats.warmMs += ms(l.took)
	l.p.deadStreak = 0
	l.m.mu.Unlock()
}

// Dead records that the pooled conn failed before the stream could use it;
// the caller closes it and dials cold, then calls Redialed. Three in a row
// rest the key for 60 s.
func (l *Lease) Dead() {
	if l == nil || l.dead {
		return
	}
	l.dead = true
	l.m.mu.Lock()
	p := l.p
	p.stats.deadN++
	p.deadStreak++
	var closing []*pconn
	if p.deadStreak >= deadStreakRest {
		p.deadStreak = 0
		closing = l.m.drainIdleLocked(p)
		p.restUntil = l.m.now().Add(restFor)
		l.m.logger().Infof("warm pool %s %s → %s: %d pooled conns dead in a row, resting %s",
			p.key.owner.kind, p.key.owner.name, p.key.addr, deadStreakRest, restFor)
	}
	l.m.mu.Unlock()
	closeAll(closing)
}

// Redialed records the stream after a dead pooled conn: its extra delay is
// everything from the take until the cold redial finished.
func (l *Lease) Redialed() {
	if l == nil || l.done {
		return
	}
	l.done = true
	d := time.Since(l.start)
	l.m.mu.Lock()
	l.p.stats.warmN++
	l.p.stats.warmMs += ms(d)
	l.m.mu.Unlock()
}

// Stats is a read-only view for tests and the debug log.
type Stats struct {
	Owners int
	Pools  int
	Live   int
	Idle   int
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Stats{Owners: len(m.owners), Live: m.live}
	for _, o := range m.owners {
		s.Pools += len(o.pools)
		for _, p := range o.pools {
			s.Idle += len(p.idle)
		}
	}
	return s
}

func (m *Manager) sortedOwnersLocked() []*owner {
	out := make([]*owner, 0, len(m.owners))
	for _, o := range m.owners {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].key.kind != out[j].key.kind {
			return out[i].key.kind < out[j].key.kind
		}
		return out[i].key.name < out[j].key.name
	})
	return out
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func closeAll(cs []*pconn) {
	for _, c := range cs {
		closeQuiet(c.Conn)
	}
}

// closeLater closes conns off the caller's lock.
func closeLater(cs []*pconn) {
	if len(cs) > 0 {
		go closeAll(cs)
	}
}
