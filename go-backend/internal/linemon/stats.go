package linemon

import (
	"math"
	"sort"
	"strconv"
)

// MaxPoints is the most buckets one line returns for any range.
const MaxPoints = 1500

// adjacentGap: two samples of a line at most this far apart are neighbours
// (one 30 s slot + scheduling jitter). Further apart = a gap in between.
const adjacentGap = 50

// liveEdge: the newest slot is still being measured (attempt + retry), so it
// is not expected yet.
const liveEdge = SlotSeconds + 5

var niceSteps = []int64{30, 60, 120, 180, 300, 600, 900, 1200, 1800, 3600, 7200, 14400, 21600, 43200, 86400}

// StepFor picks the bucket width for a window of rangeSec seconds: the
// smallest step of the nice list that keeps the window within MaxPoints.
func StepFor(rangeSec int64) int64 {
	for _, s := range niceSteps {
		if rangeSec <= s*MaxPoints {
			return s
		}
	}
	return niceSteps[len(niceSteps)-1]
}

// AlignWindow widens [from, to) to whole steps.
func AlignWindow(from, to, step int64) (int64, int64) {
	if step <= 0 {
		step = SlotSeconds
	}
	f := floorDiv(from, step) * step
	t := floorDiv(to+step-1, step) * step
	if t <= f {
		t = f + step
	}
	return f, t
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Floats marshals NaN as null and every value with one decimal.
type Floats []float64

func (f Floats) MarshalJSON() ([]byte, error) {
	buf := make([]byte, 0, 2+len(f)*6)
	buf = append(buf, '[')
	for i, v := range f {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendFloat(buf, v)
	}
	return append(buf, ']'), nil
}

// Float is a scalar with NaN → null, one decimal.
type Float float64

func (f Float) MarshalJSON() ([]byte, error) { return appendFloat(nil, float64(f)), nil }

func appendFloat(buf []byte, v float64) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(buf, "null"...)
	}
	return strconv.AppendFloat(buf, math.Round(v*10)/10, 'f', -1, 64)
}

// Series is one line's bucketed range. Bucket i covers [From+i·Step, +Step).
type Series struct {
	LineID int64   `json:"id"`
	Avg    Floats  `json:"avg"` // mean latency of the measured ok/loss slots (null = none)
	Max    Floats  `json:"max"`
	OK     []int32 `json:"ok"`
	Loss   []int32 `json:"loss"`
	Down   []int32 `json:"down"`
	Exp    []int32 `json:"exp"` // slots expected (≥ measured); exp − ok − loss − down = 无数据
}

// LineStats are the 统计 numbers of one line over the range.
type LineStats struct {
	LineID     int64 `json:"lineId"`
	LastTS     int64 `json:"lastTs"`
	LastStatus int   `json:"lastStatus"`
	LastMs     Float `json:"lastMs"`
	Avg        Float `json:"avg"`
	P95        Float `json:"p95"`
	// Jitter = mean |Δ| of consecutive measured latencies (adjacent slots).
	Jitter Float `json:"jitter"`
	// Availability % = (ok + loss) / measured slots; gaps excluded.
	Availability Float `json:"availability"`
	NoDataPct    Float `json:"noDataPct"`
	Measured     int64 `json:"measured"`
	Expected     int64 `json:"expected"`
	OK           int64 `json:"ok"`
	Loss         int64 `json:"loss"`
	Down         int64 `json:"down"`
	DownEvents   int   `json:"downEvents"`
	// LongestDown in seconds (one down slot = 30 s).
	LongestDown int64 `json:"longestDown"`
}

// Event is one 断流: consecutive down slots of a line.
type Event struct {
	LineID  int64 `json:"lineId"`
	Start   int64 `json:"start"`
	End     int64 `json:"end"` // last down slot + 30 s
	Slots   int   `json:"slots"`
	Ongoing bool  `json:"ongoing,omitempty"`
}

// Duration of the event in seconds.
func (e Event) Duration() int64 { return e.End - e.Start }

const maxEventsPerLine = 500

// Accumulator folds one line's samples (in ts order) into buckets, stats and
// events in a single pass; nothing but the latency values (for p95) is kept.
type Accumulator struct {
	lineID, from, to, step, created, now int64

	series Series
	sums   []float64
	cnt    []int32
	lat    []int32

	measured, ok, loss, down int64
	sum                      float64

	prevTS     int64
	prevLat    int32
	havePrev   bool
	jitterSum  float64
	jitterN    int64
	run        *Event
	lastDownTS int64
	events     []Event
	longest    int64
	eventCount int

	lastTS     int64
	lastStatus int
	lastMs     float64
}

// NewAccumulator: window [from, to) in steps of step seconds; created = the
// line's creation (unix s, nothing expected before); now = unix s.
func NewAccumulator(lineID, from, to, step, created, now int64) *Accumulator {
	if step <= 0 {
		step = SlotSeconds
	}
	n := int((to - from + step - 1) / step)
	if n < 0 {
		n = 0
	}
	a := &Accumulator{lineID: lineID, from: from, to: to, step: step, created: created, now: now, lastMs: math.NaN()}
	a.series = Series{LineID: lineID, Avg: make(Floats, n), Max: make(Floats, n), OK: make([]int32, n), Loss: make([]int32, n), Down: make([]int32, n), Exp: make([]int32, n)}
	a.sums = make([]float64, n)
	a.cnt = make([]int32, n)
	return a
}

// Add one sample (ts ascending; samples outside the window are ignored).
func (a *Accumulator) Add(ts int64, status int, ms10 int) {
	if ts < a.from || ts >= a.to {
		return
	}
	i := int((ts - a.from) / a.step)
	if i < 0 || i >= len(a.cnt) {
		return
	}
	a.measured++
	a.lastTS, a.lastStatus = ts, status
	switch status {
	case StatusOK, StatusLoss:
		if status == StatusOK {
			a.ok++
			a.series.OK[i]++
		} else {
			a.loss++
			a.series.Loss[i]++
		}
		ms := float64(ms10) / 10
		a.lastMs = ms
		a.sums[i] += ms
		a.cnt[i]++
		if a.cnt[i] == 1 || ms > a.series.Max[i] {
			a.series.Max[i] = ms
		}
		a.sum += ms
		a.lat = append(a.lat, int32(ms10))
		if a.havePrev && ts-a.prevTS <= adjacentGap {
			a.jitterSum += math.Abs(float64(int32(ms10)-a.prevLat)) / 10
			a.jitterN++
		}
		a.prevTS, a.prevLat, a.havePrev = ts, int32(ms10), true
		a.closeRun()
	case StatusDown:
		a.down++
		a.series.Down[i]++
		a.lastMs = math.NaN()
		a.havePrev = false
		if a.run != nil && ts-a.lastDownTS <= adjacentGap {
			a.run.Slots++
			a.run.End = ts + SlotSeconds
		} else {
			a.closeRun()
			a.run = &Event{LineID: a.lineID, Start: ts, End: ts + SlotSeconds, Slots: 1}
		}
		a.lastDownTS = ts
	default:
		a.measured--
	}
}

func (a *Accumulator) closeRun() {
	if a.run == nil {
		return
	}
	ev := *a.run
	a.run = nil
	a.eventCount++
	if d := ev.Duration(); d > a.longest {
		a.longest = d
	}
	a.events = append(a.events, ev)
	if len(a.events) > maxEventsPerLine {
		a.events = a.events[len(a.events)-maxEventsPerLine:]
	}
}

// expected slots of [start, end) for this line (rounded; never in the future
// or before the line existed).
func (a *Accumulator) expected(start, end int64) int32 {
	if start < a.created {
		start = a.created
	}
	if limit := a.now - liveEdge; end > limit {
		end = limit
	}
	if end <= start {
		return 0
	}
	return int32(math.Round(float64(end-start) / SlotSeconds))
}

// Finish returns the bucket series, the range stats and the 断流 events
// (oldest first).
func (a *Accumulator) Finish() (Series, LineStats, []Event) {
	if a.run != nil {
		ongoing := a.lastTS == a.lastDownTS && a.now-a.lastDownTS <= SlotSeconds+adjacentGap
		a.closeRun()
		if ongoing && len(a.events) > 0 {
			a.events[len(a.events)-1].Ongoing = true
		}
	}
	var expTotal int64
	for i := range a.cnt {
		if a.cnt[i] > 0 {
			a.series.Avg[i] = a.sums[i] / float64(a.cnt[i])
		} else {
			a.series.Avg[i] = math.NaN()
			a.series.Max[i] = math.NaN()
		}
		start := a.from + int64(i)*a.step
		exp := a.expected(start, start+a.step)
		if got := a.series.OK[i] + a.series.Loss[i] + a.series.Down[i]; got > exp {
			exp = got
		}
		a.series.Exp[i] = exp
		expTotal += int64(exp)
	}
	st := LineStats{
		LineID: a.lineID, LastTS: a.lastTS, LastStatus: a.lastStatus, LastMs: Float(a.lastMs),
		Avg: Float(math.NaN()), P95: Float(math.NaN()), Jitter: Float(math.NaN()), Availability: Float(math.NaN()), NoDataPct: Float(math.NaN()),
		Measured: a.measured, Expected: expTotal, OK: a.ok, Loss: a.loss, Down: a.down,
		DownEvents: a.eventCount, LongestDown: a.longest,
	}
	if n := len(a.lat); n > 0 {
		st.Avg = Float(a.sum / float64(n))
		st.P95 = Float(float64(Percentile(a.lat, 95)) / 10)
	}
	if a.jitterN > 0 {
		st.Jitter = Float(a.jitterSum / float64(a.jitterN))
	}
	if a.measured > 0 {
		st.Availability = Float(float64(a.ok+a.loss) * 100 / float64(a.measured))
	}
	if expTotal > 0 {
		st.NoDataPct = Float(float64(expTotal-a.measured) * 100 / float64(expTotal))
	}
	return a.series, st, a.events
}

// Percentile (nearest rank) of values; sorts values in place.
func Percentile(values []int32, p float64) int32 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := int(math.Ceil(p/100*float64(len(values)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(values) {
		rank = len(values) - 1
	}
	return values[rank]
}

// NewestEvents merges per-line events newest first, at most limit.
func NewestEvents(lists [][]Event, limit int) []Event {
	var all []Event
	for _, l := range lists {
		all = append(all, l...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start > all[j].Start
		}
		return all[i].LineID < all[j].LineID
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []Event{}
	}
	return all
}
