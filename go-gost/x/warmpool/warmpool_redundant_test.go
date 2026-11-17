package warmpool

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Plan 264 「冗余」: adaptive size (floor 6 / ceiling 16), parallel refill
// (4 per key), failure waves, agent cap 192, idle sleep 2 h / panel value.

// holdSource is fakeSource whose dials can be held in flight.
type holdSource struct {
	fakeSource
	hmu         sync.Mutex
	held        bool
	wait        chan struct{}
	inflight    atomic.Int64
	maxInflight atomic.Int64
}

func (s *holdSource) hold() {
	s.hmu.Lock()
	if !s.held {
		s.held = true
		s.wait = make(chan struct{})
	}
	s.hmu.Unlock()
}

func (s *holdSource) release() {
	s.hmu.Lock()
	if s.held {
		s.held = false
		close(s.wait)
	}
	s.hmu.Unlock()
}

func (s *holdSource) Dial(ctx context.Context, owner, addr string) (net.Conn, error) {
	n := s.inflight.Add(1)
	defer s.inflight.Add(-1)
	for {
		m := s.maxInflight.Load()
		if n <= m || s.maxInflight.CompareAndSwap(m, n) {
			break
		}
	}
	s.hmu.Lock()
	w, held := s.wait, s.held
	s.hmu.Unlock()
	if held {
		select {
		case <-w:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.fakeSource.Dial(ctx, owner, addr)
}

func newRedundantManager(t *testing.T) (*Manager, *holdSource, *fakeClock) {
	t.Helper()
	m := New(0)
	m.manualTicker = true
	clk := newFakeClock()
	m.now = clk.now
	src := &holdSource{}
	m.SetSource(KindChain, src)
	t.Cleanup(func() {
		src.release()
		m.SetConfig(Config{})
	})
	return m, src, clk
}

// tick advances the fake clock by one 5 s tick and lets refills settle.
func tick(m *Manager, clk *fakeClock) {
	clk.add(tickEvery)
	m.Tick()
	settle(m)
}

func headPool(t *testing.T, m *Manager, owner, addr string) *pool {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.owners[ownerKey{KindChain, owner}]
	if o == nil || o.pools[addr] == nil {
		t.Fatalf("no pool %s → %s", owner, addr)
	}
	return o.pools[addr]
}

func wantOf(m *Manager, p *pool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return p.want
}

// takeN takes n pooled conns (closing them) and returns how many it got;
// every miss is noted like a real stream (cold dial of 30 ms).
func takeN(m *Manager, owner, addr string, n int) (hits int) {
	for i := 0; i < n; i++ {
		if l := m.Take(KindChain, owner, addr); l != nil {
			l.Done()
			l.Conn.Close()
			hits++
			continue
		}
		m.NoteMiss(KindChain, owner, addr, 30*time.Millisecond)
	}
	return hits
}

func TestSizeFormula(t *testing.T) {
	for _, c := range []struct{ peak, floor, ceil, want int }{
		{0, 6, 16, 6}, {1, 6, 16, 6}, {2, 6, 16, 6}, {3, 6, 16, 7}, {4, 6, 16, 8},
		{6, 6, 16, 11}, {9, 6, 16, 16}, {10, 6, 16, 16}, {50, 6, 16, 16}, {1, 2, 16, 4}, {0, 2, 16, 2},
	} {
		if got := sizeFormula(c.peak, c.floor, c.ceil); got != c.want {
			t.Errorf("sizeFormula(peak %d, floor %d, ceil %d) = %d, want %d", c.peak, c.floor, c.ceil, got, c.want)
		}
	}
}

func TestFloorFillsWithFourDialsInFlight(t *testing.T) {
	m, src, _ := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	src.hold()
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "4 dials in flight", func() bool { return src.inflight.Load() == 4 })
	time.Sleep(30 * time.Millisecond)
	if n := src.inflight.Load(); n != 4 || m.Stats().Live != 4 {
		t.Fatalf("in flight %d, live %d — want 4 parallel dials per key", n, m.Stats().Live)
	}
	src.release()
	waitFor(t, "floor 6 filled", func() bool { return idle(m) == 6 })
	settle(m)
	if mx := src.maxInflight.Load(); mx != 4 {
		t.Fatalf("max dials in flight = %d, want 4", mx)
	}
	if d := src.dials.Load(); d != 6 {
		t.Fatalf("dials = %d, want exactly 6", d)
	}
}

func TestBurstPeakRaisesSize(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	src.hold() // refills stay in flight: the burst sees only the 6
	if hits := takeN(m, "chains_1", pe.addr(), 6); hits != 6 {
		t.Fatalf("burst of 6 on a pool of 6: %d hits", hits)
	}
	p := headPool(t, m, "chains_1", pe.addr())
	if w := wantOf(m, p); w != 6 {
		t.Fatalf("want before the tick = %d (6 hits must not grow it)", w)
	}
	src.release()
	settle(m)
	tick(m, clk)
	// peak5s 6 → ⌈9⌉ + 2 = 11.
	if w := wantOf(m, p); w != 11 {
		t.Fatalf("want after a 6-stream tick = %d, want 11", w)
	}
	waitFor(t, "grown to 11", func() bool { return idle(m) == 11 })
	k := onlyKey(t, m.Report())
	if k.Want != 11 || k.Peak5s != 6 || k.WarmN != 6 || k.MissN != 0 || k.WakeN != 0 {
		t.Fatalf("report %+v", k)
	}
}

func TestMissGrowsAtOnceUpToCeiling(t *testing.T) {
	m, src, _ := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	src.hold()
	hits := takeN(m, "chains_1", pe.addr(), 8) // 6 hits + 2 burst misses
	if hits != 6 {
		t.Fatalf("hits = %d", hits)
	}
	if w := wantOf(m, p); w != 10 {
		t.Fatalf("want after 2 burst misses = %d, want 6 + 2×2 = 10", w)
	}
	takeN(m, "chains_1", pe.addr(), 10) // more misses: capped at the ceiling
	if w := wantOf(m, p); w != 16 {
		t.Fatalf("want = %d, want ceiling 16", w)
	}
	if n := src.inflight.Load(); n > maxDialsPerKey {
		t.Fatalf("%d dials in flight on one key", n)
	}
	src.release()
	waitFor(t, "filled to the ceiling", func() bool { return idle(m) == 16 })
	k := onlyKey(t, m.Report())
	if k.MissN != 12 || k.WakeN != 0 || k.Want != 16 {
		t.Fatalf("report %+v", k)
	}
}

func TestSlowShrinkAfterQuietHour(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	src.hold()
	takeN(m, "chains_1", pe.addr(), 10) // peak 10 + 4 misses → 16
	src.release()
	settle(m)
	tick(m, clk)
	if w := wantOf(m, p); w != 16 {
		t.Fatalf("want = %d, want 16", w)
	}
	// Streams every few minutes (1 per tick) keep the key awake; the 1 h
	// peak keeps the formula at 16 for ~55–60 min.
	var trace []int
	for i := 0; i < 12*60*4; i++ { // 4 h of 5 s ticks
		if i%60 == 0 {
			takeN(m, "chains_1", pe.addr(), 1)
		}
		tick(m, clk)
		if i%180 == 179 { // every 15 min
			trace = append(trace, wantOf(m, p))
		}
	}
	// 15 min samples: 16 for the first hour (≥ 3 samples), then −1 per 15 min
	// down to the floor 6, never below.
	if trace[0] != 16 || trace[1] != 16 || trace[2] != 16 {
		t.Fatalf("shrank inside the peak hour: %v", trace)
	}
	for i := 1; i < len(trace); i++ {
		if d := trace[i-1] - trace[i]; d < 0 || d > 1 {
			t.Fatalf("not a slow −1/15 min shrink: %v", trace)
		}
	}
	if last := trace[len(trace)-1]; last != 6 {
		t.Fatalf("after 4 h quiet want = %d, want floor 6 (%v)", last, trace)
	}

	// A burst miss stops the shrink for 15 min.
	src.hold()
	takeN(m, "chains_1", pe.addr(), 7) // 6 hits + 1 miss → 8, peak 7 → 13
	src.release()
	settle(m)
	tick(m, clk)
	if w := wantOf(m, p); w != 13 {
		t.Fatalf("want after a 7-burst = %d, want ⌈10.5⌉+2 = 13", w)
	}
}

func TestShrinkWaitsForMisses(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	src.hold()
	takeN(m, "chains_1", pe.addr(), 7) // 1 miss → 8 (peak lands at the tick)
	src.release()
	settle(m)
	// Formula with peak 7 is 13: the size follows the formula, no shrink.
	tick(m, clk)
	if w := wantOf(m, p); w != 13 {
		t.Fatalf("want = %d", w)
	}
	// Grow above the formula by misses only (peak stays 7 → formula 13).
	m.mu.Lock()
	p.size = 15
	p.lastMissAt = clk.now()
	m.mu.Unlock()
	for i := 0; i < 12*14; i++ { // 14 min: no shrink yet
		tick(m, clk)
	}
	if w := wantOf(m, p); w != 15 {
		t.Fatalf("shrank before 15 min without a miss: %d", w)
	}
	for i := 0; i < 12*2; i++ {
		tick(m, clk)
	}
	if w := wantOf(m, p); w != 14 {
		t.Fatalf("want after 16 min without a miss = %d, want 14", w)
	}
}

func TestCeilingAndFloorFromPanel(t *testing.T) {
	m, src, _ := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 8})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	src.hold()
	takeN(m, "chains_1", pe.addr(), 12)
	if w := wantOf(m, p); w != 8 {
		t.Fatalf("want = %d, want panel ceiling 8", w)
	}
	src.release()
	settle(m)
	// Floor above the ceiling wins; both clamp to MaxSize 16.
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 10}, Ceil: 8})
	if w := wantOf(m, p); w != 10 {
		t.Fatalf("floor 10 / ceil 8 → want %d, want 10", w)
	}
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 40}, Ceil: 99})
	if w := wantOf(m, p); w != MaxSize {
		t.Fatalf("floor 40 → want %d, want %d", w, MaxSize)
	}
	m.mu.Lock()
	ceil := m.ceil
	m.mu.Unlock()
	if ceil != MaxSize {
		t.Fatalf("ceil 99 → %d", ceil)
	}
}

func TestWakeMissesAreNotBurstMisses(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Ceil: 16})
	waitFor(t, "floor filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	clk.add(6*time.Hour + time.Minute)
	m.Tick()
	settle(m)
	if s := m.Stats(); s.Sleeping != 1 || s.Idle != 0 {
		t.Fatalf("not asleep: %+v", s)
	}
	m.Report()
	// A browser wakes it with 5 streams at once: all cold, all wake misses,
	// no growth (the pool was asleep by design).
	src.hold()
	if hits := takeN(m, "chains_1", pe.addr(), 5); hits != 0 {
		t.Fatalf("sleeping key handed out %d conns", hits)
	}
	if w := wantOf(m, p); w != 6 {
		t.Fatalf("want after wake misses = %d, want 6", w)
	}
	src.release()
	waitFor(t, "refilled", func() bool { return idle(m) == 6 })
	k := onlyKey(t, m.Report())
	if k.MissN != 5 || k.WakeN != 5 || k.Sleep {
		t.Fatalf("report %+v", k)
	}
	// Once a conn has landed, a miss is a burst miss again.
	src.hold()
	takeN(m, "chains_1", pe.addr(), 7)
	src.release()
	settle(m)
	k = onlyKey(t, m.Report())
	if k.MissN != 1 || k.WakeN != 0 {
		t.Fatalf("burst after wake %+v", k)
	}
}

func TestNewKeyMissesAreWake(t *testing.T) {
	m, src, _ := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	src.hold()
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}})
	takeN(m, "chains_1", pe.addr(), 3)
	src.release()
	waitFor(t, "filled", func() bool { return idle(m) == 6 })
	k := onlyKey(t, m.Report())
	if k.MissN != 3 || k.WakeN != 3 || k.Want != 6 {
		t.Fatalf("report %+v", k)
	}
}

func TestParallelFailWaveCountsOnce(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	src.fail.Store(true)
	src.set("chains_1", Target{Addr: "127.0.0.1:9"})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}})
	settle(m)
	p := headPool(t, m, "chains_1", "127.0.0.1:9")
	state := func() (int, int, time.Time) {
		m.mu.Lock()
		defer m.mu.Unlock()
		return p.dialFails, p.restLevel, p.restUntil
	}
	if d := src.dials.Load(); d != 4 {
		t.Fatalf("first wave = %d dials, want 4", d)
	}
	if f, _, _ := state(); f != 1 {
		t.Fatalf("a wave of 4 failed dials counted %d failures, want 1", f)
	}
	// From now on one dial at a time: +2 s, +4 s, then rest 60 s.
	clk.add(time.Second)
	m.Tick()
	settle(m)
	if d := src.dials.Load(); d != 4 {
		t.Fatalf("dial before the 2 s backoff: %d", d)
	}
	clk.add(1100 * time.Millisecond)
	m.Tick()
	settle(m)
	if d := src.dials.Load(); d != 5 {
		t.Fatalf("after 2 s: %d dials, want 5 (one retry)", d)
	}
	clk.add(4100 * time.Millisecond)
	m.Tick()
	settle(m)
	if d := src.dials.Load(); d != 6 {
		t.Fatalf("after +4 s: %d dials, want 6", d)
	}
	f, level, until := state()
	if f != 0 || level != 1 || !until.After(clk.now()) {
		t.Fatalf("not resting after 3 failures: fails %d level %d until %v", f, level, until)
	}
	for i := 0; i < 11; i++ { // 55 s resting: nothing
		tick(m, clk)
	}
	if d := src.dials.Load(); d != 6 {
		t.Fatalf("dials while resting: %d", d)
	}
	// Streams during the rest are misses but do not grow the key.
	takeN(m, "chains_1", "127.0.0.1:9", 5)
	if w := wantOf(m, p); w != 6 {
		t.Fatalf("misses during rest grew the key to %d", w)
	}
	tick(m, clk) // rest over: exactly one probe dial
	if d := src.dials.Load(); d != 7 {
		t.Fatalf("after the rest: %d dials, want 7 (one probe, not a wave of 4)", d)
	}
	// The target is back: a success re-opens parallel refill.
	src.fail.Store(false)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.OwnerChanged(KindChain, "chains_1")
	waitFor(t, "new target filled", func() bool { return idle(m) == 6 })
}

func TestAgentCapDefaultAndPanelCap(t *testing.T) {
	if DefaultCap != 192 || MaxSize != 16 || CapVersion != 2 {
		t.Fatalf("DefaultCap %d MaxSize %d CapVersion %d", DefaultCap, MaxSize, CapVersion)
	}
	m, src, _ := newRedundantManager(t)
	if m.cap != 192 {
		t.Fatalf("New(0) cap = %d", m.cap)
	}
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	src.set("chains_2", Target{Addr: pe.addr()})
	src.hold()
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6, "chains_2": 6}, Cap: 9})
	waitFor(t, "cap reached", func() bool { return src.inflight.Load() == 8 })
	src.release()
	waitFor(t, "9 live", func() bool { return idle(m) == 9 })
	time.Sleep(30 * time.Millisecond)
	if s := m.Stats(); s.Live != 9 || pe.accepted.Load() != 9 {
		t.Fatalf("over the panel cap: %+v accepted %d", s, pe.accepted.Load())
	}
	r := m.Report()
	if r.Cap != 9 || !r.CapHit {
		t.Fatalf("report cap %d hit %v", r.Cap, r.CapHit)
	}
	// Panel stops sending a cap: back to the agent default.
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6, "chains_2": 6}})
	waitFor(t, "both filled", func() bool { return idle(m) == 12 })
	if r := m.Report(); r.Cap != DefaultCap {
		t.Fatalf("cap after the panel dropped it = %d", r.Cap)
	}
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, Cap: 1})
	m.mu.Lock()
	c := m.cap
	m.mu.Unlock()
	if c != MinCap {
		t.Fatalf("cap 1 → %d, want %d", c, MinCap)
	}
}

func TestIdleSleepPushedByPanel(t *testing.T) {
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, IdleSleepS: 600})
	waitFor(t, "filled", func() bool { return idle(m) == 6 })
	advance(t, m, clk, 9*time.Minute)
	if s := m.Stats(); s.Sleeping != 0 {
		t.Fatalf("asleep before the panel's 10 min: %+v", s)
	}
	advance(t, m, clk, time.Minute+5*time.Second)
	if s := m.Stats(); s.Sleeping != 1 {
		t.Fatalf("awake after the panel's 10 min: %+v", s)
	}
	// Absent (older panel) → agent default 2 h (plan 271); tiny values clamp to 1 min.
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}})
	m.mu.Lock()
	d := m.idleSleepNowLocked()
	m.mu.Unlock()
	if d != 2*time.Hour {
		t.Fatalf("idle sleep without idleSleepS = %v", d)
	}
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}, IdleSleepS: 3})
	m.mu.Lock()
	d = m.idleSleepNowLocked()
	m.mu.Unlock()
	if d != time.Minute {
		t.Fatalf("idleSleepS 3 → %v", d)
	}
}

func TestOldPanelPayload(t *testing.T) {
	// What fork.135 panels send: sizes only.
	var cfg Config
	if err := json.Unmarshal([]byte(`{"chains":{"chains_1":2},"forwards":{"7":2}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Ceil != 0 || cfg.IdleSleepS != 0 || cfg.Cap != 0 {
		t.Fatalf("old payload decoded %+v", cfg)
	}
	m, src, clk := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(cfg)
	waitFor(t, "floor 2 filled", func() bool { return idle(m) == 2 })
	m.mu.Lock()
	ceil, sleep, capN := m.ceil, m.idleSleepNowLocked(), m.cap
	m.mu.Unlock()
	if ceil != DefaultCeil || sleep != IdleSleepAfter || capN != DefaultCap {
		t.Fatalf("defaults with an old payload: ceil %d sleep %v cap %d", ceil, sleep, capN)
	}
	// Still adaptive above the old floor 2.
	src.hold()
	takeN(m, "chains_1", pe.addr(), 4)
	src.release()
	settle(m)
	tick(m, clk)
	if w := wantOf(m, headPool(t, m, "chains_1", pe.addr())); w != 8 {
		t.Fatalf("want = %d, want ⌈6⌉+2 = 8 (peak 4)", w)
	}
	// Unknown future fields are ignored (no DisallowUnknownFields).
	if err := json.Unmarshal([]byte(`{"chains":{},"forwards":{},"ceil":16,"idleSleepS":21600,"futureKnob":1}`), &cfg); err != nil {
		t.Fatal(err)
	}
}

func TestReportNewFieldsJSON(t *testing.T) {
	b, _ := json.Marshal(KeyReport{Kind: "c", Owner: "chains_1", Addr: "a:1", Target: true, MissN: 3, WakeN: 1, Want: 8, Peak5s: 4})
	for _, f := range []string{`"wakeN":1`, `"want":8`, `"peak5s":4`, `"missN":3`} {
		if !strings.Contains(string(b), f) {
			t.Fatalf("%s missing in %s", f, b)
		}
	}
	b, _ = json.Marshal(KeyReport{Kind: "c", Owner: "chains_1", Addr: "a:1"})
	for _, f := range []string{"wakeN", "want", "peak5s"} {
		if strings.Contains(string(b), f) {
			t.Fatalf("empty %s not omitted: %s", f, b)
		}
	}
}

func TestTickCountsDoNotAllocatePerStream(t *testing.T) {
	m, src, _ := newRedundantManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 6}})
	waitFor(t, "filled", func() bool { return idle(m) == 6 })
	p := headPool(t, m, "chains_1", pe.addr())
	allocs := testing.AllocsPerRun(200, func() {
		m.mu.Lock()
		p.tickN++
		m.adaptLocked(m.owners[ownerKey{KindChain, "chains_1"}], p, m.now())
		m.mu.Unlock()
	})
	if allocs != 0 {
		t.Fatalf("adaptive accounting allocates %.1f per run", allocs)
	}
}
