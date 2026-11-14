package linemon

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestStepFor(t *testing.T) {
	cases := map[int64]int64{
		3600:            30,   // 1小时 → 120 raw slots
		6 * 3600:        30,   // 720
		12 * 3600:       30,   // 1440
		86400:           60,   // 1天 → 1440
		7 * 86400:       600,  // 1008
		30 * 86400:      1800, // 30天 → 1440
		3 * 86400:       180,  // 1440
		3*86400 + 12345: 300,  // 1508 at 180 s → next step
	}
	for r, want := range cases {
		got := StepFor(r)
		if got != want {
			t.Errorf("StepFor(%d) = %d want %d", r, got, want)
		}
		if r/got > MaxPoints {
			t.Errorf("StepFor(%d) gives %d points", r, r/got)
		}
	}
	f, to := AlignWindow(1015, 1100, 30)
	if f != 990 || to != 1110 {
		t.Fatalf("AlignWindow = %d %d", f, to)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	var v []int32
	for i := int32(1); i <= 100; i++ {
		v = append(v, 101-i) // unsorted on purpose
	}
	if p := Percentile(v, 95); p != 95 {
		t.Fatalf("p95 of 1..100 = %d", p)
	}
	if p := Percentile([]int32{7}, 95); p != 7 {
		t.Fatalf("p95 single = %d", p)
	}
	if p := Percentile([]int32{10, 20, 30, 40}, 95); p != 40 {
		t.Fatalf("p95 of 4 = %d", p)
	}
}

// Fixed fixture: 20 slots of 30 s starting at t0 for one line.
//
//	slot: 0  1  2  3  4   5  6  7  8  9  10 11 12 13 14 15 16 17 18 19
//	      ok ok ok L  ok  D  D  D  ok -  -  ok D  ok ok ok -  D  D  ok
//
// ok latencies 10,12,14 / loss 30 / ok 10 / … (see below); "-" = no sample.
func fixture() (int64, []struct {
	slot   int
	status int
	ms10   int
}) {
	return 1_700_000_000, []struct {
		slot   int
		status int
		ms10   int
	}{
		{0, StatusOK, 100}, {1, StatusOK, 120}, {2, StatusOK, 140}, {3, StatusLoss, 300}, {4, StatusOK, 100},
		{5, StatusDown, 0}, {6, StatusDown, 0}, {7, StatusDown, 0}, {8, StatusOK, 110},
		{11, StatusOK, 130}, {12, StatusDown, 0}, {13, StatusOK, 120}, {14, StatusOK, 120}, {15, StatusOK, 160},
		{17, StatusDown, 0}, {18, StatusDown, 0}, {19, StatusOK, 100},
	}
}

func TestAccumulatorStatsFixture(t *testing.T) {
	t0, rows := fixture()
	from, to := t0, t0+20*SlotSeconds
	now := to + liveEdge // every slot of the window is expected
	a := NewAccumulator(7, from, to, SlotSeconds, 0, now)
	for _, r := range rows {
		a.Add(t0+int64(r.slot)*SlotSeconds+3, r.status, r.ms10) // +3 s line offset
	}
	series, st, events := a.Finish()

	if st.Measured != 17 || st.Expected != 20 || st.OK != 10 || st.Loss != 1 || st.Down != 6 {
		t.Fatalf("counts %+v", st)
	}
	// Availability = (ok + loss) / measured = 11 / 17.
	if got, want := float64(st.Availability), 11.0*100/17; math.Abs(got-want) > 1e-9 {
		t.Fatalf("availability %v want %v", got, want)
	}
	// 无数据 = 3 of 20 expected.
	if got := float64(st.NoDataPct); math.Abs(got-15) > 1e-9 {
		t.Fatalf("noData %v", got)
	}
	// Latencies (ms): 10 12 14 30 10 11 13 12 12 16 10 → avg 150/11.
	if got, want := float64(st.Avg), 150.0/11; math.Abs(got-want) > 1e-9 {
		t.Fatalf("avg %v want %v", got, want)
	}
	// p95 nearest rank of 11 values = ceil(10.45) = 11th smallest = 30.
	if st.P95 != 30 {
		t.Fatalf("p95 %v", st.P95)
	}
	// Jitter: adjacent measured pairs only (a down or a gap breaks the chain):
	// 0-1 2, 1-2 2, 2-3 16, 3-4 20 | 8 … gap … 11 (90 s apart: no) | 13-14 0,
	// 14-15 4 → (2+2+16+20+0+4)/6.
	if got, want := float64(st.Jitter), 44.0/6; math.Abs(got-want) > 1e-9 {
		t.Fatalf("jitter %v want %v", got, want)
	}
	// 断流: slots 5–7 (90 s), 12 (30 s), 17–18 (60 s) → 3 events, longest 90 s.
	if st.DownEvents != 3 || st.LongestDown != 90 || len(events) != 3 {
		t.Fatalf("events %d longest %d list %+v", st.DownEvents, st.LongestDown, events)
	}
	if events[0].Start != t0+5*SlotSeconds+3 || events[0].Slots != 3 || events[0].Duration() != 90 {
		t.Fatalf("first event %+v", events[0])
	}
	if events[2].Ongoing {
		t.Fatal("last event ended (slot 19 ok) but marked ongoing")
	}
	if st.LastStatus != StatusOK || st.LastMs != 10 {
		t.Fatalf("last %+v", st)
	}
	// Raw buckets (step = slot): slot 9 has no sample, slot 5 shows down.
	if series.Down[5] != 1 || !math.IsNaN(series.Avg[5]) || series.Exp[9] != 1 || series.OK[9]+series.Loss[9]+series.Down[9] != 0 {
		t.Fatalf("raw buckets wrong: down5=%d avg5=%v exp9=%d", series.Down[5], series.Avg[5], series.Exp[9])
	}
}

func TestAccumulatorBucketKeepsDown(t *testing.T) {
	t0, rows := fixture()
	from, to := t0, t0+20*SlotSeconds
	// 10 slots per bucket: bucket 0 = slots 0–9, bucket 1 = slots 10–19.
	a := NewAccumulator(7, from, to, 10*SlotSeconds, 0, to+liveEdge)
	for _, r := range rows {
		a.Add(t0+int64(r.slot)*SlotSeconds, r.status, r.ms10)
	}
	series, _, _ := a.Finish()
	if len(series.Avg) != 2 {
		t.Fatalf("buckets %d", len(series.Avg))
	}
	// Bucket 0: avg of 10 12 14 30 10 11 = 87/6, max 30, 3 down — the down
	// slots are counted, never averaged away.
	if math.Abs(series.Avg[0]-87.0/6) > 1e-9 || series.Max[0] != 30 || series.Down[0] != 3 || series.Loss[0] != 1 || series.OK[0] != 5 || series.Exp[0] != 10 {
		t.Fatalf("bucket 0: avg %v max %v ok %d loss %d down %d exp %d", series.Avg[0], series.Max[0], series.OK[0], series.Loss[0], series.Down[0], series.Exp[0])
	}
	if series.Down[1] != 3 || series.Exp[1] != 10 || series.OK[1] != 5 {
		t.Fatalf("bucket 1: ok %d down %d exp %d", series.OK[1], series.Down[1], series.Exp[1])
	}
	// A bucket that is all down still reports the down (avg null).
	b := NewAccumulator(1, 0, 300, 300, 0, 10_000)
	for s := int64(0); s < 10; s++ {
		b.Add(s*SlotSeconds, StatusDown, 0)
	}
	sb, st, ev := b.Finish()
	if sb.Down[0] != 10 || !math.IsNaN(sb.Avg[0]) || st.DownEvents != 1 || st.LongestDown != 300 || len(ev) != 1 {
		t.Fatalf("all-down bucket: %+v %+v %+v", sb, st, ev)
	}
}

func TestAccumulatorExpectedRespectsCreationAndLiveEdge(t *testing.T) {
	// Line created 10 min into a 1 h window that ends now: 50 min expected,
	// minus the slot still being measured.
	now := int64(1_700_003_600)
	from := now - 3600
	a := NewAccumulator(1, from, now, SlotSeconds, from+600, now)
	_, st, _ := a.Finish()
	want := int64(math.Round(float64(3000-liveEdge) / SlotSeconds))
	if st.Expected < want-2 || st.Expected > want+2 || st.Measured != 0 {
		t.Fatalf("expected %d want ~%d", st.Expected, want)
	}
	if st.NoDataPct != 100 || !math.IsNaN(float64(st.Availability)) || !math.IsNaN(float64(st.Avg)) {
		t.Fatalf("empty line stats %+v", st)
	}
}

func TestAccumulatorOngoingAndGapSplit(t *testing.T) {
	now := int64(10_000)
	a := NewAccumulator(1, 9000, 10_020, SlotSeconds, 0, now)
	a.Add(9000, StatusDown, 0)
	a.Add(9030, StatusDown, 0)
	// 120 s without samples (panel restart): a new event, not one long outage.
	a.Add(9180, StatusDown, 0)
	a.Add(9960, StatusDown, 0)
	_, st, ev := a.Finish()
	if st.DownEvents != 3 || len(ev) != 3 {
		t.Fatalf("events %+v", ev)
	}
	if ev[0].Slots != 2 || ev[0].Duration() != 60 || ev[1].Slots != 1 {
		t.Fatalf("split wrong %+v", ev)
	}
	if !ev[2].Ongoing {
		t.Fatalf("latest down slot 40 s ago must be ongoing: %+v", ev[2])
	}
	merged := NewestEvents([][]Event{ev, {{LineID: 2, Start: 9500, End: 9530, Slots: 1}}}, 3)
	if len(merged) != 3 || merged[0].Start != 9960 || merged[1].LineID != 2 {
		t.Fatalf("newest first merge %+v", merged)
	}
}

func TestFloatsJSONNull(t *testing.T) {
	b, err := json.Marshal(struct {
		A Floats `json:"a"`
		B Float  `json:"b"`
		C Float  `json:"c"`
	}{Floats{1.25, math.NaN(), 30}, Float(math.NaN()), 12.345})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"a":[1.3,null,30]`) || !strings.Contains(s, `"b":null`) || !strings.Contains(s, `"c":12.3`) {
		t.Fatalf("json %s", s)
	}
}

// Plan 255 part 2: 端口未开启 slots are neither 断流 nor measured for 可用率,
// end a 断流 run and are not 无数据.
func TestAccumulatorClosedSlots(t *testing.T) {
	const from = int64(1_700_000_000)
	a := NewAccumulator(9, from, from+10*30, 30, 0, from+20*30)
	statuses := []int{StatusOK, StatusDown, StatusClosed, StatusClosed, StatusDown, StatusOK, StatusClosed, StatusOK, StatusOK, StatusLoss}
	for i, st := range statuses {
		ms := 0
		if st == StatusOK || st == StatusLoss {
			ms = 100
		}
		a.Add(from+int64(i)*30, st, ms)
	}
	series, st, events := a.Finish()
	if st.Closed != 3 || st.Measured != 7 || st.Down != 2 {
		t.Fatalf("closed %d measured %d down %d", st.Closed, st.Measured, st.Down)
	}
	if got := float64(st.Availability); math.Abs(got-500.0/7) > 1e-9 {
		t.Fatalf("availability %v, want 5/7", got)
	}
	if got := float64(st.NoDataPct); got != 0 {
		t.Fatalf("closed counted as 无数据: %v", got)
	}
	if st.DownEvents != 2 || len(events) != 2 {
		t.Fatalf("closed must split 断流 runs: %d events %+v", st.DownEvents, events)
	}
	if series.Closed[2] != 1 || series.Closed[6] != 1 || series.Exp[2] != 1 {
		t.Fatalf("series closed %v exp %v", series.Closed, series.Exp)
	}
	a2 := NewAccumulator(9, from, from+60, 30, 0, from+600)
	a2.Add(from, StatusClosed, 0)
	_, st2, _ := a2.Finish()
	if st2.LastStatus != StatusClosed || st2.Measured != 0 || !math.IsNaN(float64(st2.Availability)) {
		t.Fatalf("closed only: %+v", st2)
	}
}
