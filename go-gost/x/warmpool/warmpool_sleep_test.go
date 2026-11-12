package warmpool

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Plan 249: idle sleep — a key with no new stream for 30 min drains to 0 and
// is not refilled until the next stream (which dials cold and wakes it).

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_800_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newSleepManager(t *testing.T) (*Manager, *fakeSource, *fakeClock) {
	t.Helper()
	m, src := newTestManager(t, 64)
	clk := newFakeClock()
	m.now = clk.now
	if m.idleSleep != IdleSleepAfter || IdleSleepAfter != 30*time.Minute {
		t.Fatalf("idle sleep default = %v / %v, want 30m", m.idleSleep, IdleSleepAfter)
	}
	return m, src, clk
}

// advance moves the fake clock in 5 s ticks (like the real ticker) and lets
// refills settle after every tick.
func advance(t *testing.T, m *Manager, clk *fakeClock, d time.Duration) {
	t.Helper()
	for step := 5 * time.Minute; d > 0; d -= step {
		if d < step {
			step = d
		}
		clk.add(step)
		m.Tick()
		settle(m)
	}
}

// settle waits until no refill dial is in flight.
func settle(m *Manager) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		busy := false
		for _, o := range m.owners {
			for _, p := range o.pools {
				if p.dialing > 0 {
					busy = true
				}
			}
		}
		m.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func onlyKey(t *testing.T, r *Report) KeyReport {
	t.Helper()
	if r == nil || len(r.Keys) != 1 {
		t.Fatalf("report = %+v", r)
	}
	return r.Keys[0]
}

func TestIdleSleepAfter30MinAndWakeOnStream(t *testing.T) {
	m, src, clk := newSleepManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return idle(m) == 2 })

	// 29 min 55 s without a stream: still warm (conns replaced by age).
	advance(t, m, clk, 29*time.Minute+55*time.Second)
	if s := m.Stats(); s.Idle != 2 || s.Sleeping != 0 {
		t.Fatalf("before 30 min: %+v", s)
	}
	if k := onlyKey(t, m.Report()); k.Sleep || !k.Target {
		t.Fatalf("awake key reported %+v", k)
	}
	// 30 min: asleep, drained to 0, peer sees the conns closed.
	advance(t, m, clk, 5*time.Second)
	if s := m.Stats(); s.Idle != 0 || s.Sleeping != 1 || s.Live != 0 {
		t.Fatalf("after 30 min: %+v", s)
	}
	waitFor(t, "peer saw pool drained", func() bool { return pe.open.Load() == 0 })
	k := onlyKey(t, m.Report())
	if !k.Sleep || !k.Target || k.Idle != 0 {
		t.Fatalf("sleeping key reported %+v", k)
	}
	if !m.Active(KindChain, "chains_1") {
		t.Fatal("sleeping owner must stay configured (SetWarmPools set unchanged)")
	}

	// No refill while asleep: another hour of ticks, 0 dials.
	dials := src.dials.Load()
	advance(t, m, clk, time.Hour)
	if got := src.dials.Load(); got != dials {
		t.Fatalf("dials while asleep: %d → %d", dials, got)
	}
	if k := onlyKey(t, m.Report()); !k.Sleep {
		t.Fatalf("still asleep expected, got %+v", k)
	}

	// The next stream finds nothing (cold, same as no pool) and wakes it.
	if l := m.Take(KindChain, "chains_1", pe.addr()); l != nil {
		t.Fatal("a sleeping key handed out a conn")
	}
	m.NoteMiss(KindChain, "chains_1", pe.addr(), 40*time.Millisecond)
	waitFor(t, "refilled at once after the wake", func() bool { return idle(m) == 2 })
	k = onlyKey(t, m.Report())
	if k.Sleep || k.MissN != 1 || k.Idle != 2 {
		t.Fatalf("woken key reported %+v", k)
	}
	if s := m.Stats(); s.Sleeping != 0 {
		t.Fatalf("stats after wake %+v", s)
	}
}

func TestStreamsKeepKeyAwake(t *testing.T) {
	m, src, clk := newSleepManager(t)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return idle(m) == 2 })
	for i := 0; i < 6; i++ { // a stream every 25 min for 2.5 h
		advance(t, m, clk, 25*time.Minute)
		if l := m.Take(KindChain, "chains_1", pe.addr()); l == nil {
			t.Fatalf("round %d: no pooled conn on an active key", i)
		} else {
			l.Done()
			l.Conn.Close()
		}
		settle(m)
	}
	if s := m.Stats(); s.Sleeping != 0 {
		t.Fatalf("key with streams every 25 min fell asleep: %+v", s)
	}
}

func TestSleepingKeyAndReconcile(t *testing.T) {
	m, src, clk := newSleepManager(t)
	pe := newPeer(t, nil)
	pe2 := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()}, Target{Addr: pe2.addr()})
	cfg := Config{Chains: map[string]int{"chains_1": 2}}
	m.SetConfig(cfg)
	waitFor(t, "filled", func() bool { return idle(m) == 2 })
	advance(t, m, clk, 31*time.Minute)
	if s := m.Stats(); s.Sleeping != 1 || s.Idle != 0 {
		t.Fatalf("not asleep: %+v", s)
	}
	dials := src.dials.Load()

	// The panel re-sends the same state (agent reconnect / safety pass) or
	// changes the size: the key stays asleep, nothing dialed.
	m.SetConfig(cfg)
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 3}})
	settle(m)
	// A chain update with the same head: still asleep.
	m.OwnerChanged(KindChain, "chains_1")
	settle(m)
	if got := src.dials.Load(); got != dials || idle(m) != 0 {
		t.Fatalf("reconcile woke a sleeping key: dials %d → %d, idle %d", dials, got, idle(m))
	}

	// 最优 switch to pe2 on the idle owner: the new head starts asleep too.
	src.set("chains_1", Target{Addr: pe2.addr()}, Target{Addr: pe.addr()})
	m.OwnerChanged(KindChain, "chains_1")
	settle(m)
	if got := src.dials.Load(); got != dials || pe2.accepted.Load() != 0 {
		t.Fatalf("new head of an idle owner was warmed: dials %d → %d", dials, got)
	}
	if s := m.Stats(); s.Sleeping != 2 {
		t.Fatalf("sleeping pools = %+v, want both", s)
	}
	// A stream on the new head wakes only that key.
	if l := m.Take(KindChain, "chains_1", pe2.addr()); l != nil {
		t.Fatal("sleeping head handed out a conn")
	}
	waitFor(t, "new head warm", func() bool { return pe2.open.Load() == 3 })
	if pe.open.Load() != 0 {
		t.Fatal("old head woke without a stream")
	}

	// Removed and re-added by the panel (rule paused → resumed): fresh owner,
	// warmed right away.
	m.SetConfig(Config{})
	waitFor(t, "torn down", func() bool { return pe2.open.Load() == 0 })
	m.SetConfig(cfg)
	waitFor(t, "re-added owner is warm", func() bool { return pe2.open.Load() == 2 })
	if s := m.Stats(); s.Sleeping != 0 {
		t.Fatalf("re-added owner asleep: %+v", s)
	}
}

// gatedSource dials only when the gate opens (a refill in flight).
type gatedSource struct {
	fakeSource
	gate chan struct{}
}

func (s *gatedSource) Dial(ctx context.Context, owner, addr string) (net.Conn, error) {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fakeSource.Dial(ctx, owner, addr)
}

func TestDialInFlightWhenFallingAsleepIsDropped(t *testing.T) {
	m := New(64)
	m.manualTicker = true
	clk := newFakeClock()
	m.now = clk.now
	src := &gatedSource{gate: make(chan struct{})}
	m.SetSource(KindChain, src)
	t.Cleanup(func() { m.SetConfig(Config{}) })
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}}) // dial 1 waits at the gate
	clk.add(31 * time.Minute)
	m.Tick() // falls asleep with the dial in flight
	close(src.gate)
	settle(m)
	waitFor(t, "in-flight conn closed", func() bool { return pe.open.Load() == 0 && pe.accepted.Load() == 1 })
	if s := m.Stats(); s.Idle != 0 || s.Live != 0 || s.Sleeping != 1 {
		t.Fatalf("after in-flight dial: %+v", s)
	}
}

func TestRuleServiceKeySleepsAndWakes(t *testing.T) {
	m := New(64)
	m.manualTicker = true
	clk := newFakeClock()
	m.now = clk.now
	t.Cleanup(func() { m.SetConfig(Config{}) })
	pe := newPeer(t, nil)
	f := &fakeForwarder{addr: pe.addr()}
	m.RegisterForwarder("7_2_3_tcp", f)
	m.SetConfig(Config{Forwards: map[string]int{"7": 2}})
	waitFor(t, "filled", func() bool { return pe.open.Load() == 2 })
	clk.add(30 * time.Minute)
	m.Tick()
	waitFor(t, "drained", func() bool { return pe.open.Load() == 0 })
	if l := m.Take(KindService, "7_2_3_tcp", pe.addr()); l != nil {
		t.Fatal("sleeping rule key handed out a conn")
	}
	waitFor(t, "woken", func() bool { return pe.open.Load() == 2 })
}

func TestReportSleepFieldOnlyWhenAsleep(t *testing.T) {
	awake, _ := json.Marshal(KeyReport{Kind: "c", Owner: "chains_1", Addr: "a:1", Target: true})
	if strings.Contains(string(awake), "sleep") {
		t.Fatalf("awake key carries the sleep field: %s", awake)
	}
	asleep, _ := json.Marshal(KeyReport{Kind: "c", Owner: "chains_1", Addr: "a:1", Target: true, Sleep: true})
	if !strings.Contains(string(asleep), `"sleep":true`) {
		t.Fatalf("sleeping key without the field: %s", asleep)
	}
}
