// Package linemon is the plan 255 「线路监测」 logic: the 30 s TCP latency
// scheduler (one goroutine, a few probes in flight) and the range maths
// (buckets, stats, 断流 events). It has no database or HTTP code; the handler
// wires it to the agents' TcpPing command and the repository.
package linemon

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// SlotSeconds is the measuring interval per line.
	SlotSeconds = 30
	// RetentionSeconds is how long raw samples are kept (30 days).
	RetentionSeconds = 30 * 24 * 3600
	// MaxLinesPerChart / MaxLinesTotal are the configuration caps.
	MaxLinesPerChart = 8
	MaxLinesTotal    = 40
	// PingTimeoutMS is the agent-side TCP connect timeout of one attempt.
	PingTimeoutMS = 3000
)

// Sample statuses (same values as model.LineSample*).
const (
	StatusOK   = 1
	StatusLoss = 2
	StatusDown = 3
	// StatusClosed (plan 255 part 2): both attempts were refused — the target
	// answers but nothing listens (an expired check port), 「端口未开启」.
	StatusClosed = 4
)

// AttemptKind is what one TcpPing attempt told the panel.
type AttemptKind int

const (
	// NoAnswer: the panel could not ask the agent (offline, command error,
	// channel timeout) — says nothing about the target.
	NoAnswer AttemptKind = iota
	// Connected: the agent's TCP connect succeeded.
	Connected
	// Failed: the agent answered that the TCP connect failed.
	Failed
)

// Attempt is the outcome of one TcpPing command.
type Attempt struct {
	Kind AttemptKind
	Ms   float64
	// Refused (Failed only): the agent reported errorKind "refused" (RST /
	// ICMP port unreachable). Older agents never set it.
	Refused bool
	// ErrKind: the agent's errorKind (refused / timeout / unreach / dns; ""
	// for older agents).
	ErrKind string
	Reason  string
}

// Classify turns the slot's attempts into a sample status. second is nil when
// no retry was made. gap = no sample (the panel could not measure the slot).
//
//	first ok                      → ok   (first ms)
//	first failed, retry ok        → loss (retry ms)
//	first failed, retry failed    → down (both refused → closed 端口未开启)
//	first no answer, retry ok     → ok   (retry ms)
//	anything else                 → gap  (never 断流 / 丢包)
func Classify(first Attempt, second *Attempt) (status int, ms float64, gap bool) {
	if first.Kind == Connected {
		return StatusOK, first.Ms, false
	}
	if second == nil {
		return 0, 0, true
	}
	switch {
	case first.Kind == Failed && second.Kind == Connected:
		return StatusLoss, second.Ms, false
	case first.Kind == Failed && second.Kind == Failed:
		if first.Refused && second.Refused {
			return StatusClosed, 0, false
		}
		return StatusDown, 0, false
	case first.Kind == NoAnswer && second.Kind == Connected:
		return StatusOK, second.Ms, false
	}
	return 0, 0, true
}

// Target is one active line to measure.
type Target struct {
	LineID int64
	NodeID int64 // source node (runs the TcpPing)
	Host   string
	Port   int
}

// Result is the final outcome of one slot.
type Result struct {
	LineID int64
	TS     int64 // unix seconds of the slot's first attempt
	Status int   // 0 when Gap
	Ms     float64
	Gap    bool
	// Reason: why the slot is a gap / failed (e.g. 源节点离线); "" when ok.
	Reason string
}

// Config wires the engine. Zero durations take the defaults.
type Config struct {
	Slot        time.Duration // 30 s
	Retry       time.Duration // 2 s
	MaxInFlight int           // 4
	Reload      time.Duration // re-read the targets (node addresses) every 5 min
	Maintain    time.Duration // hourly maintenance (prune)
	// Load returns the active targets and whether hourly maintenance is still
	// needed (any line or sample exists). An error keeps the previous set.
	Load func() (targets []Target, maintain bool, err error)
	// Online reports whether the source node's agent is connected; offline =
	// gap without sending a command.
	Online func(nodeID int64) bool
	// Probe runs one TcpPing attempt (blocking, bounded by its own timeout).
	Probe func(ctx context.Context, t Target) Attempt
	// OnResult receives every final slot result, on the engine goroutine.
	OnResult func(Result)
	// OnMaintain runs on the engine goroutine (hourly while needed, and once
	// when the engine starts).
	OnMaintain func()
	// OnIdle runs on the engine goroutine before it blocks with no active
	// line and nothing in flight (flush hook).
	OnIdle func()
	Now    func() time.Time
}

// Stats are cumulative engine counters.
type Stats struct {
	Lines       int   `json:"lines"`
	InFlight    int   `json:"inFlight"`
	MaxInFlight int   `json:"maxInFlight"`
	Attempts    int64 `json:"attempts"`
	Retries     int64 `json:"retries"`
	Results     int64 `json:"results"`
	Gaps        int64 `json:"gaps"`
	Skipped     int64 `json:"skipped"`
	Wakeups     int64 `json:"wakeups"`
	Reloads     int64 `json:"reloads"`
}

type job struct {
	lineID  int64
	slotTS  int64
	due     time.Time
	attempt int // 1 or 2
	first   Attempt
	target  Target
}

type lineState struct {
	target Target
	offset time.Duration
	next   time.Time
	busy   bool // a slot (attempt 1 / retry wait / attempt 2) is in progress
}

type probeDone struct {
	job job
	res Attempt
}

// Engine is the one-goroutine 30 s scheduler.
type Engine struct {
	cfg   Config
	kick  chan struct{}
	stats struct {
		attempts, retries, results, gaps, skipped, wakeups, reloads atomic.Int64
		lines, inFlight, maxInFlight                                atomic.Int64
	}
}

// NewEngine fills the defaults.
func NewEngine(cfg Config) *Engine {
	if cfg.Slot <= 0 {
		cfg.Slot = SlotSeconds * time.Second
	}
	if cfg.Retry <= 0 {
		cfg.Retry = 2 * time.Second
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 4
	}
	if cfg.Reload <= 0 {
		cfg.Reload = 5 * time.Minute
	}
	if cfg.Maintain <= 0 {
		cfg.Maintain = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{cfg: cfg, kick: make(chan struct{}, 1)}
}

// Kick asks the engine to reload its targets (non-blocking).
func (e *Engine) Kick() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// Stats returns a snapshot of the counters.
func (e *Engine) Stats() Stats {
	return Stats{
		Lines: int(e.stats.lines.Load()), InFlight: int(e.stats.inFlight.Load()), MaxInFlight: int(e.stats.maxInFlight.Load()),
		Attempts: e.stats.attempts.Load(), Retries: e.stats.retries.Load(), Results: e.stats.results.Load(),
		Gaps: e.stats.gaps.Load(), Skipped: e.stats.skipped.Load(), Wakeups: e.stats.wakeups.Load(), Reloads: e.stats.reloads.Load(),
	}
}

// Offsets spreads n lines evenly over one slot (line i starts i·slot/n after
// the slot boundary), so the probes never come in one burst.
func Offsets(n int, slot time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = time.Duration(int64(slot) * int64(i) / int64(n))
	}
	return out
}

// nextDue is the first time ≥ after that sits offset past a slot boundary.
func nextDue(after time.Time, slot, offset time.Duration) time.Time {
	base := after.Truncate(slot).Add(offset)
	for base.Before(after) {
		base = base.Add(slot)
	}
	return base
}

// Run blocks until ctx ends. With no active lines it waits on Kick / ctx only
// (plus the hourly maintenance while lines or samples exist).
func (e *Engine) Run(ctx context.Context) {
	cfg := e.cfg
	lines := make(map[int64]*lineState)
	var retries []job // waiting for their retry time (sorted by due)
	var queue []job   // due, waiting for a free in-flight slot (FIFO)
	done := make(chan probeDone, cfg.MaxInFlight)
	inFlight := 0
	var wg sync.WaitGroup
	defer wg.Wait()

	maintainOn := false
	var nextMaintain, nextReload time.Time

	reload := func() {
		e.stats.reloads.Add(1)
		targets, maintain, err := cfg.Load()
		now := cfg.Now()
		if err != nil {
			nextReload = now.Add(cfg.Reload)
			return
		}
		maintainOn = maintain
		if maintainOn && nextMaintain.IsZero() {
			nextMaintain = now.Add(cfg.Maintain)
		}
		if !maintainOn {
			nextMaintain = time.Time{}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].LineID < targets[j].LineID })
		offsets := Offsets(len(targets), cfg.Slot)
		fresh := make(map[int64]*lineState, len(targets))
		for i, t := range targets {
			st := lines[t.LineID]
			if st == nil {
				st = &lineState{}
			}
			st.target = t
			if st.offset != offsets[i] || st.next.IsZero() {
				st.offset = offsets[i]
				if !st.busy {
					st.next = nextDue(now, cfg.Slot, st.offset)
				}
			}
			fresh[t.LineID] = st
		}
		lines = fresh
		// Drop queued work of removed lines (in-flight probes finish and are
		// discarded when they report back).
		keepJobs := func(in []job) []job {
			out := in[:0]
			for _, j := range in {
				if _, ok := lines[j.lineID]; ok {
					out = append(out, j)
				}
			}
			return out
		}
		retries, queue = keepJobs(retries), keepJobs(queue)
		e.stats.lines.Store(int64(len(lines)))
		if len(lines) > 0 {
			nextReload = now.Add(cfg.Reload)
		} else {
			nextReload = time.Time{}
		}
	}

	finish := func(st *lineState, r Result) {
		st.busy = false
		e.stats.results.Add(1)
		if r.Gap {
			e.stats.gaps.Add(1)
		}
		if cfg.OnResult != nil {
			cfg.OnResult(r)
		}
	}

	launch := func(j job) {
		inFlight++
		e.stats.inFlight.Store(int64(inFlight))
		if int64(inFlight) > e.stats.maxInFlight.Load() {
			e.stats.maxInFlight.Store(int64(inFlight))
		}
		e.stats.attempts.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := cfg.Probe(ctx, j.target)
			done <- probeDone{job: j, res: res}
		}()
	}

	// start begins attempt j now if the source is online and a slot is free.
	start := func(j job, now time.Time) {
		st := lines[j.lineID]
		if st == nil {
			return
		}
		if cfg.Online != nil && !cfg.Online(j.target.NodeID) {
			if j.attempt == 1 {
				finish(st, Result{LineID: j.lineID, TS: j.slotTS, Gap: true, Reason: "源节点离线"})
				return
			}
			status, ms, gap := Classify(j.first, nil)
			finish(st, Result{LineID: j.lineID, TS: j.slotTS, Status: status, Ms: ms, Gap: gap, Reason: "源节点离线"})
			return
		}
		if inFlight >= cfg.MaxInFlight {
			queue = append(queue, j)
			return
		}
		launch(j)
	}

	if cfg.OnMaintain != nil {
		cfg.OnMaintain()
	}
	reload()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		now := cfg.Now()
		// Due slots of idle lines.
		for _, st := range lines {
			if st.next.IsZero() || st.next.After(now) {
				continue
			}
			due := st.next
			st.next = nextDue(due.Add(time.Nanosecond), cfg.Slot, st.offset)
			if st.next.Before(now) {
				// Far behind (e.g. clock jump): resume from now.
				st.next = nextDue(now, cfg.Slot, st.offset)
			}
			if st.busy {
				// The previous slot is still being measured: this one is a gap.
				e.stats.skipped.Add(1)
				e.stats.gaps.Add(1)
				continue
			}
			st.busy = true
			start(job{lineID: st.target.LineID, slotTS: due.Unix(), due: due, attempt: 1, target: st.target}, now)
		}
		// Retries whose wait is over.
		for len(retries) > 0 && !retries[0].due.After(now) {
			j := retries[0]
			retries = retries[1:]
			if st := lines[j.lineID]; st != nil {
				j.target = st.target
				start(j, now)
			}
		}
		// Queued attempts while slots are free; a first attempt that waited
		// more than half a slot is dropped (gap) instead of drifting.
		for inFlight < cfg.MaxInFlight && len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			st := lines[j.lineID]
			if st == nil {
				continue
			}
			if j.attempt == 1 && now.Sub(j.due) > cfg.Slot/2 {
				e.stats.skipped.Add(1)
				finish(st, Result{LineID: j.lineID, TS: j.slotTS, Gap: true, Reason: "调度繁忙"})
				continue
			}
			launch(j)
		}

		// Next wake-up.
		var wake time.Time
		consider := func(t time.Time) {
			if !t.IsZero() && (wake.IsZero() || t.Before(wake)) {
				wake = t
			}
		}
		for _, st := range lines {
			consider(st.next)
		}
		if len(retries) > 0 {
			consider(retries[0].due)
		}
		consider(nextReload)
		if maintainOn {
			consider(nextMaintain)
		}
		var timerC <-chan time.Time
		if !wake.IsZero() {
			d := wake.Sub(cfg.Now())
			if d < 0 {
				d = 0
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
			timerC = timer.C
		}
		if len(lines) == 0 && inFlight == 0 && cfg.OnIdle != nil {
			cfg.OnIdle()
		}

		select {
		case <-ctx.Done():
			return
		case <-e.kick:
			reload()
		case <-timerC:
			e.stats.wakeups.Add(1)
			now := cfg.Now()
			if !nextReload.IsZero() && !nextReload.After(now) {
				reload()
			}
			if maintainOn && !nextMaintain.After(now) {
				nextMaintain = now.Add(cfg.Maintain)
				if cfg.OnMaintain != nil {
					cfg.OnMaintain()
				}
			}
		case d := <-done:
			inFlight--
			e.stats.inFlight.Store(int64(inFlight))
			st := lines[d.job.lineID]
			if st == nil {
				continue // line removed meanwhile
			}
			j := d.job
			if j.attempt == 1 {
				if d.res.Kind == Connected {
					finish(st, Result{LineID: j.lineID, TS: j.slotTS, Status: StatusOK, Ms: d.res.Ms})
					continue
				}
				e.stats.retries.Add(1)
				j.attempt, j.first = 2, d.res
				j.due = cfg.Now().Add(cfg.Retry)
				retries = append(retries, j)
				sort.SliceStable(retries, func(a, b int) bool { return retries[a].due.Before(retries[b].due) })
				continue
			}
			second := d.res
			status, ms, gap := Classify(j.first, &second)
			reason := ""
			if gap {
				reason = firstNonEmpty(second.Reason, j.first.Reason, "节点未响应")
			} else if status == StatusDown {
				reason = firstNonEmpty(second.Reason, j.first.Reason)
			} else if status == StatusClosed {
				reason = "连接被拒绝：目标端口没有监听"
			}
			finish(st, Result{LineID: j.lineID, TS: j.slotTS, Status: status, Ms: ms, Gap: gap, Reason: reason})
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
