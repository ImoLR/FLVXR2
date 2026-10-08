package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func sessionPathPlan(id int64, keys ...tunnelQualityPingKey) tunnelQualityTunnelPlan {
	path := tunnelPathPlan{widths: []int{1, len(keys)}, segments: make([][]tunnelPathSegment, 1)}
	for i, key := range keys {
		path.segments[0] = append(path.segments[0], tunnelPathSegment{from: 0, to: i, key: key})
	}
	return tunnelQualityTunnelPlan{base: tunnelQualitySnapshot{TunnelID: id, PathStatus: "timeout", PathUpdatedAt: 1}, path: &path}
}

func sessionRound(p *tunnelQualityProber, plans []tunnelQualityTunnelPlan, ping tunnelQualityPingFunc, legacy ...tunnelQualityPingKey) *tunnelQualityProbeRound {
	r := newTunnelQualityProbeRound(ping)
	options := tunnelQualityProbeOptions()
	options.pingCount = 2
	for _, plan := range plans {
		for _, layer := range plan.path.segments {
			for _, segment := range layer {
				r.planPing(segment.key, options)
			}
		}
	}
	for _, key := range legacy {
		r.planPing(key, tunnelQualityProbeOptions())
	}
	p.preparePathRound(r, plans, p.pathSession)
	return r
}

func sessionSnapshot(t *testing.T, p *tunnelQualityProber, id int64) tunnelQualitySnapshot {
	t.Helper()
	value, ok := p.cache.Load(id)
	if !ok {
		t.Fatal("path snapshot missing")
	}
	return *value.(*tunnelQualitySnapshot)
}

func TestTunnelPathSessionRetryAndRefresh(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fails, calls int
		status       string
	}{{"ok_once", 0, 1, "ok"}, {"two_fail_then_ok", 2, 3, "ok"}, {"dead_after_three", 100, 3, "timeout"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTunnelQualityProber(nil)
			defer p.Stop()
			now := time.Unix(1000, 0)
			p.now = func() time.Time { return now }
			key := tunnelQualityPingKey{1, "192.0.2.2", 1234}
			plans := []tunnelQualityTunnelPlan{sessionPathPlan(1, key)}
			calls := 0
			ping := func(_ int64, _ string, _ int, options diagnosisExecOptions) (float64, float64, error) {
				calls++
				if options.pingCount != 2 {
					t.Errorf("path count=%d", options.pingCount)
				}
				if calls <= tc.fails {
					if calls%2 == 0 {
						return 0, 100, nil
					}
					return 0, 0, errors.New("failed")
				}
				return 12, 20, nil
			}
			for i := 0; i < 10; i++ {
				p.demandPath()
				r := sessionRound(p, plans, ping)
				r.run(context.Background())
				snap := sessionSnapshot(t, p, 1)
				if i < tc.fails && i < 2 {
					if snap.PathStatus != "" {
						t.Fatalf("early timeout: %+v", snap)
					}
				} else if snap.PathStatus != tc.status || snap.PathUpdatedAt != now.UnixMilli() {
					t.Fatalf("round=%d snapshot=%+v", i, snap)
				}
				now = now.Add(10 * time.Second)
			}
			if calls != tc.calls {
				t.Fatalf("calls=%d want=%d", calls, tc.calls)
			}
			// Expiry resets both successful and dead keys, and clears stale values.
			now = now.Add(61 * time.Second)
			p.demandPath()
			if len(p.pathSegments) != 0 || sessionSnapshot(t, p, 1).PathStatus != "" {
				t.Fatal("new session retained state")
			}
			sessionRound(p, plans, ping).run(context.Background())
			if calls != tc.calls+1 {
				t.Fatal("reopened session did not probe")
			}
		})
	}
}

func TestTunnelPathSessionSharedUsersAndSettledTTL(t *testing.T) {
	for _, dead := range []bool{false, true} {
		p := newTunnelQualityProber(nil)
		now := time.Unix(1000, 0)
		p.now = func() time.Time { return now }
		key := tunnelQualityPingKey{1, "192.0.2.2", 1234}
		plans := []tunnelQualityTunnelPlan{sessionPathPlan(1, key)}
		calls := 0
		ping := func(int64, string, int, diagnosisExecOptions) (float64, float64, error) {
			calls++
			if dead {
				return 0, 100, nil
			}
			return 12, 0, nil
		}
		for second := 0; second <= 720; second += 10 {
			// User A polls until minute 7, user B joins at minute 2 and
			// remains through minute 12; identity never partitions the cache.
			if second <= 420 {
				p.demandPath()
			}
			if second >= 120 {
				p.demandPath()
			}
			before := calls
			sessionRound(p, plans, ping).run(context.Background())
			if second == 120 && calls != before {
				t.Fatal("second user reprobed settled key")
			}
			now = now.Add(10 * time.Second)
		}
		want := 3 // t=0, 300, 600, independent of overlapping users.
		if dead {
			want = 9
		} // three attempts after each dead state's TTL.
		if calls != want || p.pathSession != 1 {
			t.Fatalf("dead=%v calls=%d sessions=%d", dead, calls, p.pathSession)
		}
		p.Stop()
	}
}

func TestTunnelPathSessionLegacyAndAlternative(t *testing.T) {
	p := newTunnelQualityProber(nil)
	defer p.Stop()
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	legacy := tunnelQualityPingKey{1, "192.0.2.2", 1002}
	dead := tunnelQualityPingKey{1, "192.0.2.3", 1003}
	ok := tunnelQualityPingKey{1, "192.0.2.4", 1004}
	plans := []tunnelQualityTunnelPlan{sessionPathPlan(1, legacy, dead, ok)}
	var mu sync.Mutex
	calls := map[int]int{}
	ping := func(_ int64, _ string, port int, options diagnosisExecOptions) (float64, float64, error) {
		mu.Lock()
		calls[port]++
		mu.Unlock()
		if port == legacy.port {
			if options.pingCount != 4 {
				t.Error("legacy count changed")
			}
			return 0, 100, errors.New("legacy failure")
		}
		if port == dead.port {
			return 0, 100, nil
		}
		return 7, 0, nil
	}
	for i := 0; i < 8; i++ {
		p.demandPath()
		sessionRound(p, plans, ping, legacy).run(context.Background())
		snap := sessionSnapshot(t, p, 1)
		if snap.PathStatus != "ok" || snap.PathLatency != 7 {
			t.Fatalf("dead alternative poisoned path: %+v", snap)
		}
		now = now.Add(10 * time.Second)
	}
	if calls[legacy.port] != 8 || calls[dead.port] != 3 || calls[ok.port] != 1 {
		t.Fatalf("calls=%v", calls)
	}
	if _, exists := p.pathSegments[legacy]; exists {
		t.Fatal("legacy governed by session cache")
	}
}

func TestTunnelPathSessionProgressivePublish(t *testing.T) {
	p := newTunnelQualityProber(nil)
	defer p.Stop()
	p.demandPath()
	fast := tunnelQualityPingKey{1, "192.0.2.2", 1002}
	better := tunnelQualityPingKey{2, "192.0.2.3", 1003}
	slow := tunnelQualityPingKey{3, "192.0.2.4", 1004}
	plans := []tunnelQualityTunnelPlan{sessionPathPlan(1, fast, better), sessionPathPlan(2, slow)}
	betterRelease, slowRelease := make(chan struct{}), make(chan struct{})
	var betterOnce, slowOnce sync.Once
	unblockBetter := func() { betterOnce.Do(func() { close(betterRelease) }) }
	unblockSlow := func() { slowOnce.Do(func() { close(slowRelease) }) }
	defer unblockBetter()
	defer unblockSlow()
	r := sessionRound(p, plans, func(_ int64, _ string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		if port == better.port {
			<-betterRelease
			return 5, 0, nil
		}
		if port == slow.port {
			<-slowRelease
			return 0, 100, nil
		}
		return 20, 0, nil
	})
	published := make(chan int, 3)
	publish := r.onResult
	r.onResult = func(key tunnelQualityPingKey, result *tunnelQualityPingResult) {
		publish(key, result)
		published <- key.port
	}
	done := make(chan struct{})
	go func() { r.run(context.Background()); close(done) }()
	await := func(want int) {
		t.Helper()
		select {
		case port := <-published:
			if port != want {
				t.Fatalf("port=%d want=%d", port, want)
			}
		case <-time.After(time.Second):
			t.Fatal("publication blocked")
		}
	}
	await(fast.port)
	if snap := sessionSnapshot(t, p, 1); snap.PathLatency != 20 || snap.PathStatus != "ok" {
		t.Fatalf("first path=%+v", snap)
	}
	if snap := sessionSnapshot(t, p, 2); snap.PathStatus != "" {
		t.Fatalf("unfinished path=%+v", snap)
	}
	unblockBetter()
	await(better.port)
	if snap := sessionSnapshot(t, p, 1); snap.PathLatency != 5 {
		t.Fatalf("not refined: %+v", snap)
	}
	// Reset during a still-running round: its late result must not contaminate
	// the new session. Concurrent API reads see immutable snapshot copies.
	p.lastPathDemand.Store(time.Now().Add(-61 * time.Second).UnixNano())
	p.demandPath()
	unblockSlow()
	await(slow.port)
	<-done
	if len(p.pathSegments) != 0 || sessionSnapshot(t, p, 1).PathStatus != "" {
		t.Fatal("old round repopulated new session")
	}
}
