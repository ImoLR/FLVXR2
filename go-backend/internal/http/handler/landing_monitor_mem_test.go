package handler

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"go-backend/internal/landingcustom"
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

func TestLandingOnlineIPsMemoryFootprint(t *testing.T) {
	const nodes = 200
	counters := make([]repo.LandingCounter, nodes)
	for i := range counters {
		ips := make([]landingcustom.OnlineIP, landingOnlineIPLimit)
		for j := range ips {
			ips[j] = landingcustom.OnlineIP{IP: fmt.Sprintf("2001:db8a:ffff:ffff:ffff:ffff:%04x:%04x", i+4096, j+4096), Connections: 64 - j}
		}
		counters[i] = onlineIPCounter(nativeCounter(int64(i+1), 1, 0, 0, 2080), landingOnlineIPLimit, ips...)
	}
	heap := func() int64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.HeapAlloc)
	}
	before := heap()
	tr := newLandingRateTracker(landingRateMaxEntries)
	base := int64(1_700_000_000_000)
	tr.observe(counters, base)
	tr.observe(counters, base+5000)
	after := heap()
	runtime.KeepAlive(counters)
	runtime.KeepAlive(tr)
	added := after - before
	t.Logf("tracker heap for %d nodes × %d IPv6 IPs: %d bytes (%.2f MiB, %.0f B/node)", nodes, landingOnlineIPLimit, added, float64(added)/(1<<20), float64(added)/nodes)
	if added > 2<<20 {
		t.Fatalf("bounded online IP tracker uses %d bytes", added)
	}
	for _, n := range []int{0, 1, 64} {
		c := counters[0]
		c.OnlineIPs = c.OnlineIPs[:n]
		c.OnlineIPCount = &n
		withIPs, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		c.OnlineIPCount, c.OnlineIPs = nil, nil
		withoutIPs, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("native counter %d IPv6 IPs: JSON %d bytes (additional %d bytes)", n, len(withIPs), len(withIPs)-len(withoutIPs))
	}
}
