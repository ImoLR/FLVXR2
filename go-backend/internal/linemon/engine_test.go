package linemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	ok := Attempt{Kind: Connected, Ms: 12.3}
	ok2 := Attempt{Kind: Connected, Ms: 45.6}
	fail := Attempt{Kind: Failed, Reason: "connection refused"}
	none := Attempt{Kind: NoAnswer, Reason: "等待节点响应超时"}
	refused := Attempt{Kind: Failed, Refused: true, Reason: "连接被拒绝"}
	cases := []struct {
		name   string
		first  Attempt
		second *Attempt
		status int
		ms     float64
		gap    bool
	}{
		{"first ok", ok, nil, StatusOK, 12.3, false},
		{"first ok ignores retry", ok, &fail, StatusOK, 12.3, false},
		{"fail then ok = loss with retry ms", fail, &ok2, StatusLoss, 45.6, false},
		{"fail fail = down", fail, &fail, StatusDown, 0, false},
		{"no answer then ok = ok", none, &ok2, StatusOK, 45.6, false},
		{"fail then no answer = gap", fail, &none, 0, 0, true},
		{"no answer then fail = gap", none, &fail, 0, 0, true},
		{"no answer twice = gap", none, &none, 0, 0, true},
		{"fail, no retry = gap", fail, nil, 0, 0, true},
		{"no answer, no retry = gap", none, nil, 0, 0, true},
		// plan 255 part 2: refused twice = 端口未开启 (not 断流); mixed = down
		{"refused refused = closed", refused, &refused, StatusClosed, 0, false},
		{"refused then timeout = down", refused, &fail, StatusDown, 0, false},
		{"timeout then refused = down", fail, &refused, StatusDown, 0, false},
		{"refused then ok = loss", refused, &ok2, StatusLoss, 45.6, false},
		{"no answer then refused = gap", none, &refused, 0, 0, true},
	}
	for _, c := range cases {
		status, ms, gap := Classify(c.first, c.second)
		if status != c.status || ms != c.ms || gap != c.gap {
			t.Errorf("%s: got (%d, %v, %v) want (%d, %v, %v)", c.name, status, ms, gap, c.status, c.ms, c.gap)
		}
	}
}

func TestOffsetsSpreadEvenly(t *testing.T) {
	slot := 30 * time.Second
	got := Offsets(4, slot)
	want := []time.Duration{0, 7500 * time.Millisecond, 15 * time.Second, 22500 * time.Millisecond}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("offset %d = %v want %v", i, got[i], want[i])
		}
	}
	for _, n := range []int{1, 7, 40} {
		o := Offsets(n, slot)
		for i := 1; i < n; i++ {
			if o[i] <= o[i-1] || o[i] >= slot {
				t.Fatalf("n=%d offsets not increasing within the slot: %v", n, o)
			}
		}
	}
	if d := nextDue(time.Unix(990, 0), slot, 5*time.Second); d.Unix() != 995 {
		t.Fatalf("nextDue = %d", d.Unix())
	}
	if d := nextDue(time.Unix(996, 0), slot, 5*time.Second); d.Unix() != 1025 {
		t.Fatalf("nextDue after = %d", d.Unix())
	}
}

type fakeAgent struct {
	mu       sync.Mutex
	calls    map[int64]int
	behave   func(t Target, call int) Attempt
	delay    time.Duration
	active   atomic.Int64
	peak     atomic.Int64
	starts   []time.Time
	startsMu sync.Mutex
}

func (f *fakeAgent) probe(ctx context.Context, t Target) Attempt {
	n := f.active.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer f.active.Add(-1)
	f.startsMu.Lock()
	f.starts = append(f.starts, time.Now())
	f.startsMu.Unlock()
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[int64]int{}
	}
	f.calls[t.LineID]++
	call := f.calls[t.LineID]
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
		}
	}
	return f.behave(t, call)
}

type resultLog struct {
	mu  sync.Mutex
	all []Result
}

func (r *resultLog) add(x Result) { r.mu.Lock(); r.all = append(r.all, x); r.mu.Unlock() }
func (r *resultLog) list() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.all...)
}

func runEngine(t *testing.T, cfg Config, d time.Duration) *Engine {
	t.Helper()
	e := NewEngine(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	e.Run(ctx)
	return e
}

func TestEngineClassifiesWithRetry(t *testing.T) {
	agent := &fakeAgent{behave: func(tg Target, call int) Attempt {
		switch tg.LineID {
		case 1: // always ok
			return Attempt{Kind: Connected, Ms: 10}
		case 2: // first attempt of every slot fails, retry ok → loss
			if call%2 == 1 {
				return Attempt{Kind: Failed}
			}
			return Attempt{Kind: Connected, Ms: 20}
		case 3: // dead target → down
			return Attempt{Kind: Failed, Reason: "connection refused"}
		case 4: // command timeouts → gap
			return Attempt{Kind: NoAnswer, Reason: "等待节点响应超时"}
		}
		return Attempt{}
	}}
	var log resultLog
	targets := []Target{{LineID: 1, NodeID: 10}, {LineID: 2, NodeID: 10}, {LineID: 3, NodeID: 10}, {LineID: 4, NodeID: 10}, {LineID: 5, NodeID: 99}}
	e := runEngine(t, Config{
		Slot: 400 * time.Millisecond, Retry: 50 * time.Millisecond,
		Load:     func() ([]Target, bool, error) { return targets, false, nil },
		Online:   func(id int64) bool { return id != 99 },
		Probe:    agent.probe,
		OnResult: log.add,
	}, 1300*time.Millisecond)
	by := map[int64][]Result{}
	for _, r := range log.list() {
		by[r.LineID] = append(by[r.LineID], r)
	}
	for id := int64(1); id <= 5; id++ {
		if len(by[id]) < 2 {
			t.Fatalf("line %d: %d results, want ≥ 2 (%+v)", id, len(by[id]), by[id])
		}
	}
	for _, r := range by[1] {
		if r.Gap || r.Status != StatusOK || r.Ms != 10 {
			t.Fatalf("line 1 result %+v", r)
		}
	}
	for _, r := range by[2] {
		if r.Gap || r.Status != StatusLoss || r.Ms != 20 {
			t.Fatalf("line 2 result %+v", r)
		}
	}
	for _, r := range by[3] {
		if r.Gap || r.Status != StatusDown || r.Reason != "connection refused" {
			t.Fatalf("line 3 result %+v", r)
		}
	}
	for _, r := range by[4] {
		if !r.Gap || r.Status != 0 {
			t.Fatalf("line 4 (timeout) must be a gap: %+v", r)
		}
	}
	for _, r := range by[5] {
		if !r.Gap || r.Reason != "源节点离线" {
			t.Fatalf("line 5 (offline source) must be a gap: %+v", r)
		}
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.calls[5] != 0 {
		t.Fatalf("offline source got %d commands, want 0", agent.calls[5])
	}
	// ok lines: one attempt per slot; failing lines: two (the run may end
	// with one slot still in flight).
	if c := agent.calls[1]; c < len(by[1]) || c > len(by[1])+1 {
		t.Fatalf("ok line attempts %d vs slots %d", c, len(by[1]))
	}
	if c := agent.calls[3]; c < 2*len(by[3]) || c > 2*len(by[3])+2 {
		t.Fatalf("down line attempts %d vs 2×slots %d", c, len(by[3]))
	}
	if s := e.Stats(); s.Retries == 0 || s.Gaps == 0 {
		t.Fatalf("stats %+v", s)
	}
}

func TestEngineRetryWaitsRetryDelay(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	e := NewEngine(Config{
		Slot: 2 * time.Second, Retry: 200 * time.Millisecond,
		Load: func() ([]Target, bool, error) { return []Target{{LineID: 1, NodeID: 1}}, false, nil },
		Probe: func(ctx context.Context, tg Target) Attempt {
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
			return Attempt{Kind: Failed}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	e.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(times) < 2 {
		t.Fatalf("attempts = %d", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 180*time.Millisecond || gap > 600*time.Millisecond {
		t.Fatalf("retry after %v, want ~200ms", gap)
	}
}

func TestEngineConcurrencyLimitAndSpread(t *testing.T) {
	agent := &fakeAgent{delay: 120 * time.Millisecond, behave: func(Target, int) Attempt { return Attempt{Kind: Connected, Ms: 1} }}
	var targets []Target
	for i := int64(1); i <= 12; i++ {
		targets = append(targets, Target{LineID: i, NodeID: 1})
	}
	var log resultLog
	slot := 600 * time.Millisecond
	runEngine(t, Config{
		Slot: slot, Retry: 50 * time.Millisecond, MaxInFlight: 4,
		Load:     func() ([]Target, bool, error) { return targets, false, nil },
		Probe:    agent.probe,
		OnResult: log.add,
	}, 1900*time.Millisecond)
	if p := agent.peak.Load(); p > 4 {
		t.Fatalf("peak in flight %d > 4", p)
	}
	if p := agent.peak.Load(); p < 2 {
		t.Fatalf("peak in flight %d — lines never overlapped?", p)
	}
	// Spread: the probe starts of one slot are not one burst (12 lines over
	// 600 ms ≈ one start per 50 ms, never all within 100 ms).
	agent.startsMu.Lock()
	starts := append([]time.Time(nil), agent.starts...)
	agent.startsMu.Unlock()
	if len(starts) < 12 {
		t.Fatalf("starts %d", len(starts))
	}
	if span := starts[11].Sub(starts[0]); span < 300*time.Millisecond {
		t.Fatalf("first 12 starts within %v: not spread over the slot", span)
	}
	for _, r := range log.list() {
		if r.Gap {
			t.Fatalf("unexpected gap %+v", r)
		}
	}
}

func TestEngineIdleWithoutLines(t *testing.T) {
	var probes, maint atomic.Int64
	var idle atomic.Int64
	e := runEngine(t, Config{
		Slot: 50 * time.Millisecond, Reload: 50 * time.Millisecond, Maintain: 50 * time.Millisecond,
		Load:       func() ([]Target, bool, error) { return nil, false, nil },
		Probe:      func(context.Context, Target) Attempt { probes.Add(1); return Attempt{} },
		OnMaintain: func() { maint.Add(1) },
		OnIdle:     func() { idle.Add(1) },
	}, 400*time.Millisecond)
	s := e.Stats()
	if probes.Load() != 0 || s.Wakeups != 0 || s.Reloads != 1 {
		t.Fatalf("idle engine woke up: probes=%d stats=%+v", probes.Load(), s)
	}
	if maint.Load() != 1 {
		t.Fatalf("maintenance %d, want only the start one", maint.Load())
	}
	if idle.Load() == 0 {
		t.Fatal("OnIdle never ran")
	}
}

func TestEngineKickReloadsAndPauses(t *testing.T) {
	var mu sync.Mutex
	active := []Target{{LineID: 1, NodeID: 1}}
	var log resultLog
	e := NewEngine(Config{
		Slot: 100 * time.Millisecond,
		Load: func() ([]Target, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]Target(nil), active...), true, nil
		},
		Probe:    func(context.Context, Target) Attempt { return Attempt{Kind: Connected, Ms: 1} },
		OnResult: log.add,
	})
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() { e.Run(ctx); close(doneCh) }()
	time.Sleep(350 * time.Millisecond)
	mu.Lock()
	active = nil // pause the only line
	mu.Unlock()
	e.Kick()
	time.Sleep(50 * time.Millisecond)
	n := len(log.list())
	time.Sleep(350 * time.Millisecond)
	cancel()
	<-doneCh
	if n < 2 {
		t.Fatalf("results before pause %d", n)
	}
	if after := len(log.list()); after != n {
		t.Fatalf("paused line still measured: %d → %d", n, after)
	}
	if s := e.Stats(); s.Lines != 0 {
		t.Fatalf("lines after pause %d", s.Lines)
	}
}

func TestEngineHourlyMaintenanceWhileLinesExist(t *testing.T) {
	var maint atomic.Int64
	runEngine(t, Config{
		Maintain:   80 * time.Millisecond,
		Load:       func() ([]Target, bool, error) { return nil, true, nil }, // paused lines only
		Probe:      func(context.Context, Target) Attempt { return Attempt{} },
		OnMaintain: func() { maint.Add(1) },
	}, 300*time.Millisecond)
	if m := maint.Load(); m < 3 {
		t.Fatalf("maintenance ran %d times, want start + hourly", m)
	}
}
