package handler

import (
	"runtime"
	"testing"

	"go-backend/internal/store/repo"
)

// Resource check for plan 111: the in-memory sample map for 10 000 landing
// nodes (50 servers × 200) stays small, and a full snapshot is cheap.
func TestLandingRateTrackerMemoryFootprint(t *testing.T) {
	const nodes = 10000
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	counters := make([]repo.LandingCounter, nodes)
	ids := make([]int64, nodes)
	for i := range counters {
		counters[i] = nativeCounter(int64(i+1), 1, int64(i)*mb, int64(i)*mb, 3)
		ids[i] = int64(i + 1)
	}
	before := heap()
	tr := newLandingRateTracker(landingRateMaxEntries)
	base := int64(1_700_000_000_000)
	tr.observe(counters, base)
	for i := range counters {
		counters[i].UpBytes += 5 * mb
		counters[i].DownBytes += 9 * mb
	}
	tr.observe(counters, base+5000)
	after := heap()
	// The input slices are live on both sides of the measurement.
	runtime.KeepAlive(counters)
	rates := tr.snapshotMany(ids, base+5000)
	runtime.KeepAlive(tr)
	if len(rates) != nodes || tr.size() != nodes || !rates[nodes].RateOK {
		t.Fatalf("tracker lost entries: %d %d", len(rates), tr.size())
	}
	perEntry := float64(after-before) / nodes
	t.Logf("tracker heap for %d nodes: %.2f MiB (%.0f B/entry)", nodes, float64(after-before)/(1<<20), perEntry)
	if after-before > 8<<20 {
		t.Fatalf("tracker uses %d bytes for %d nodes", after-before, nodes)
	}
}
