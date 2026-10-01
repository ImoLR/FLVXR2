package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-gost/core/observer/stats"
	xstats "github.com/go-gost/x/observer/stats"
)

// TestTakeServiceTrafficKeepsConcurrentBytes reports traffic while connections keep adding
// bytes; every byte must be reported exactly once.
func TestTakeServiceTrafficKeepsConcurrentBytes(t *testing.T) {
	st := xstats.NewStats(true)

	const writers = 8
	const perWriter = 200000
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				st.Add(stats.KindInputBytes, int64(1+(i+seed)%7))
				st.Add(stats.KindOutputBytes, int64(1+(i*3+seed)%11))
			}
		}(w)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var reportedIn, reportedOut uint64
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		in, out := takeServiceTraffic(st)
		reportedIn += in
		reportedOut += out
	}
	in, out := takeServiceTraffic(st)
	reportedIn += in
	reportedOut += out

	var wantIn, wantOut uint64
	for w := 0; w < writers; w++ {
		for i := 0; i < perWriter; i++ {
			wantIn += uint64(1 + (i+w)%7)
			wantOut += uint64(1 + (i*3+w)%11)
		}
	}
	if reportedIn != wantIn || reportedOut != wantOut {
		t.Fatalf("reported in=%d out=%d, want in=%d out=%d (lost in=%d out=%d)",
			reportedIn, reportedOut, wantIn, wantOut, int64(wantIn)-int64(reportedIn), int64(wantOut)-int64(reportedOut))
	}
	if st.Get(stats.KindInputBytes) != 0 || st.Get(stats.KindOutputBytes) != 0 {
		t.Fatalf("counters not drained: in=%d out=%d", st.Get(stats.KindInputBytes), st.Get(stats.KindOutputBytes))
	}
}

type fakeTrafficPost struct {
	bodies  [][]byte
	results []bool
	err     error
}

func (f *fakeTrafficPost) post(_ context.Context, body []byte) (bool, error) {
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	if f.err != nil {
		return false, f.err
	}
	ok := true
	if len(f.results) > 0 {
		ok = f.results[0]
		f.results = f.results[1:]
	}
	return ok, nil
}

func newTestTrafficManager(post *fakeTrafficPost, now *time.Time) *GlobalTrafficManager {
	return &GlobalTrafficManager{
		serviceTraffic: map[string]*ServiceTraffic{},
		ctx:            context.Background(),
		now:            func() time.Time { return *now },
		post:           post.post,
	}
}

func decodeReport(t *testing.T, body []byte) map[string]TrafficReportItem {
	t.Helper()
	var items []TrafficReportItem
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode report %q: %v", body, err)
	}
	out := map[string]TrafficReportItem{}
	for _, item := range items {
		out[item.N] = item
	}
	return out
}

func TestTrafficManagerResendsUnacknowledgedReportUnchanged(t *testing.T) {
	now := time.Unix(1000, 0)
	post := &fakeTrafficPost{results: []bool{false, false, true, true}}
	m := newTestTrafficManager(post, &now)

	m.AddTraffic("1_2_3_tcp", 300, 100)
	m.collectAndReport() // not acknowledged
	m.AddTraffic("1_2_3_tcp", 30, 10) // new traffic while the report is pending
	m.AddTraffic("4_2_3_tcp", 5, 6)
	now = now.Add(5 * time.Second)
	m.collectAndReport() // resend: still not acknowledged
	now = now.Add(5 * time.Second)
	m.collectAndReport() // resend: acknowledged

	if len(post.bodies) != 3 {
		t.Fatalf("posted %d reports, want 3", len(post.bodies))
	}
	if !bytes.Equal(post.bodies[0], post.bodies[1]) || !bytes.Equal(post.bodies[0], post.bodies[2]) {
		t.Fatalf("pending report was not resent unchanged:\n%s\n%s\n%s", post.bodies[0], post.bodies[1], post.bodies[2])
	}
	first := decodeReport(t, post.bodies[0])
	if got := first["1_2_3_tcp"]; got.U != 300 || got.D != 100 || len(first) != 1 {
		t.Fatalf("first report = %+v", first)
	}

	// Only the acknowledged bytes were removed; the new traffic is reported next.
	now = now.Add(5 * time.Second)
	m.collectAndReport()
	if len(post.bodies) != 4 {
		t.Fatalf("posted %d reports, want 4", len(post.bodies))
	}
	next := decodeReport(t, post.bodies[3])
	if got := next["1_2_3_tcp"]; got.U != 30 || got.D != 10 {
		t.Fatalf("second report for 1_2_3_tcp = %+v, want U=30 D=10", got)
	}
	if got := next["4_2_3_tcp"]; got.U != 5 || got.D != 6 {
		t.Fatalf("second report for 4_2_3_tcp = %+v, want U=5 D=6", got)
	}
	if up, down := m.GetServiceTraffic("1_2_3_tcp"); up != 0 || down != 0 {
		t.Fatalf("traffic left after acknowledgement: up=%d down=%d", up, down)
	}
}

func TestTrafficManagerRebuildsStalePendingReport(t *testing.T) {
	now := time.Unix(1000, 0)
	post := &fakeTrafficPost{err: errors.New("panel unreachable")}
	m := newTestTrafficManager(post, &now)

	m.AddTraffic("1_2_3_tcp", 300, 100)
	m.collectAndReport()
	m.AddTraffic("1_2_3_tcp", 30, 10)

	now = now.Add(maxPendingReportAge + time.Second)
	post.err = nil
	m.collectAndReport()

	if len(post.bodies) != 2 {
		t.Fatalf("posted %d reports, want 2", len(post.bodies))
	}
	rebuilt := decodeReport(t, post.bodies[1])
	if got := rebuilt["1_2_3_tcp"]; got.U != 330 || got.D != 110 {
		t.Fatalf("rebuilt report = %+v, want all accumulated bytes U=330 D=110", got)
	}
	if up, down := m.GetServiceTraffic("1_2_3_tcp"); up != 0 || down != 0 {
		t.Fatalf("traffic left after acknowledgement: up=%d down=%d", up, down)
	}
}
