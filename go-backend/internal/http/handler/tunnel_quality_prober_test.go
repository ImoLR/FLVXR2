package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTunnelQualityRoundPublicFallbackShared(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	options := tunnelQualityProbeOptions()
	round := newTunnelQualityProbeRound(func(id int64, host string, port int, got diagnosisExecOptions) (float64, float64, error) {
		if id != 1 || port != 443 || got != options {
			t.Errorf("probe arguments changed: %d %s %d %+v", id, host, port, got)
		}
		mu.Lock()
		hosts = append(hosts, host)
		mu.Unlock()
		if host != exitTestTargets[2].host {
			return 0, 100, errors.New("target failed")
		}
		return 23, 7, nil
	})
	for i := 0; i < 50; i++ {
		round.planExitTest(1, options)
	}
	// A chain pair which resolves to the same endpoint also shares the ping.
	pathOptions := options
	pathOptions.pingCount = 2
	round.planPing(tunnelQualityPingKey{1, exitTestTargets[0].host, 443}, pathOptions)
	round.run(context.Background())
	latency, loss, err := round.exitTestResult(1)
	wantHosts := []string{exitTestTargets[0].host, exitTestTargets[1].host, exitTestTargets[2].host}
	if latency != 23 || loss != 7 || err != nil || !reflect.DeepEqual(hosts, wantHosts) {
		t.Fatalf("latency=%v loss=%v err=%v hosts=%v", latency, loss, err, hosts)
	}
}

func TestTunnelQualityRoundConcurrencyAndCancellation(t *testing.T) {
	for _, cancelQueued := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelQueued), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			full := make(chan struct{})
			var fullOnce sync.Once
			var mu sync.Mutex
			active, maxActive, calls := 0, 0, 0
			perNode, maxPerNode := map[int64]int{}, map[int64]int{}
			round := newTunnelQualityProbeRound(func(id int64, _ string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
				mu.Lock()
				active++
				calls++
				perNode[id]++
				if active > maxActive {
					maxActive = active
				}
				if perNode[id] > maxPerNode[id] {
					maxPerNode[id] = perNode[id]
				}
				if active == tunnelQualityMaxProbes {
					fullOnce.Do(func() { close(full) })
				}
				mu.Unlock()
				<-release
				mu.Lock()
				active--
				perNode[id]--
				mu.Unlock()
				return 10, 0, nil
			})
			for id := int64(1); id <= 4; id++ {
				for port := 1; port <= 10; port++ {
					round.planPing(tunnelQualityPingKey{id, "192.0.2.1", port}, diagnosisExecOptions{})
				}
				round.planExitTest(id, diagnosisExecOptions{})
			}
			done := make(chan struct{})
			go func() { round.run(ctx); close(done) }()
			select {
			case <-full:
			case <-time.After(3 * time.Second):
				t.Fatal("independent sources did not fill global concurrency")
			}
			if cancelQueued {
				cancel()
			}
			unblock()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("queued or shared probes deadlocked")
			}
			if maxActive != tunnelQualityMaxProbes {
				t.Fatalf("global max=%d", maxActive)
			}
			for id, max := range maxPerNode {
				if max != tunnelQualityMaxNodeProbes {
					t.Fatalf("source %d max=%d", id, max)
				}
			}
			wantCalls := 44
			if cancelQueued {
				wantCalls = tunnelQualityMaxProbes
			}
			if calls != wantCalls {
				t.Fatalf("commands=%d want=%d", calls, wantCalls)
			}
			// Every planned result is completed even when canceled while queued.
			for key := range round.planned {
				_, _, err := round.result(key.nodeID, key.ip, key.port, diagnosisExecOptions{})
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("unfinished result: %v", err)
				}
			}
		})
	}
}

func TestTunnelQualityPlanSkipsOfflineEndpoints(t *testing.T) {
	for _, offlineID := range []int64{1, 2} {
		t.Run(fmt.Sprintf("offline=%d", offlineID), func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addNode(1, "entry")
			e.addNode(2, "exit")
			e.addTunnel(1, 1, 1)
			e.exec(`UPDATE tunnel SET type=2 WHERE id=1`)
			e.exec(`UPDATE node SET status=0 WHERE id=?`, offlineID)
			e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, port) VALUES (1,'1',1,0,1001), (1,'3',2,0,1002)`)
			calls := 0
			round := newTunnelQualityProbeRound(func(id int64, _ string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
				calls++
				if id != 2 {
					t.Errorf("ping from offline entry %d", id)
				}
				return 30, 0, nil
			})
			p := newTunnelQualityProber(e.h)
			defer p.Stop()
			p.demandPath()
			plan := p.planTunnel(1, round, e.h.getNodeRecord)
			if len(round.planned) != 0 {
				t.Fatal("offline pair was scheduled")
			}
			round.run(context.Background())
			snap := plan.snapshot(round)
			wantCalls, wantError := 1, ""
			if offlineID == 2 {
				wantCalls, wantError = 0, "节点不在线"
			}
			if calls != wantCalls || snap.Success || snap.EntryToExitLatency != -1 || snap.EntryToExitLoss != 100 || snap.PathStatus != "timeout" || snap.PathLoss != 100 || snap.ErrorMessage != wantError {
				t.Fatalf("calls=%d snapshot=%+v", calls, snap)
			}
		})
	}
}

func TestTunnelQualityNodeStatusAndWebsocket(t *testing.T) {
	e := newFlowTestEnv(t)
	for id := int64(1); id <= 3; id++ {
		e.addNode(id, fmt.Sprint(id))
	}
	e.exec(`UPDATE node SET is_remote=1 WHERE id IN (2,3)`)
	e.exec(`UPDATE node SET status=0 WHERE id=3`)
	p := newTunnelQualityProber(e.h)
	defer p.Stop()
	lookup := p.roundNodeLookup()
	for id, want := range map[int64]int{1: 0, 2: 1, 3: 0} {
		node, err := lookup(id)
		if err != nil || node.Status != want {
			t.Fatalf("node=%+v err=%v want status=%d", node, err, want)
		}
	}
	// The round normalizes a copy, without writing node status or changing it
	// mid-round when federation or websocket events update the repository.
	node, _ := e.h.getNodeRecord(1)
	if node.Status != 1 {
		t.Fatal("lookup wrote node status")
	}
	e.exec(`UPDATE node SET status=0 WHERE id=2`)
	node, _ = lookup(2)
	if node.Status != 1 {
		t.Fatal("round node snapshot changed")
	}
}

func TestTunnelQualityLowestPathKeepsLegacyMeasurements(t *testing.T) {
	e := newFlowTestEnv(t)
	for id := int64(1); id <= 4; id++ {
		e.addNode(id, fmt.Sprint(id))
	}
	for id := int64(1); id <= 2; id++ {
		e.addTunnel(id, 1, 1)
		e.exec(`UPDATE tunnel SET type=2 WHERE id=?`, id)
		e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, port) VALUES (?,'1',1,0,1001), (?,'1',2,0,1002), (?,'3',3,0,1003), (?,'3',4,0,1004)`, id, id, id, id)
	}
	var mu sync.Mutex
	calls := map[tunnelQualityPingKey]int{}
	round := newTunnelQualityProbeRound(func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		mu.Lock()
		calls[tunnelQualityPingKey{id, ip, port}]++
		mu.Unlock()
		if id == 3 {
			return 99, 100, nil
		} // Preserve the legacy nil-error rule.
		if id == 2 && port == 1004 {
			return 5, 10, nil
		}
		return 40, 20, nil
	})
	p := newTunnelQualityProber(e.h)
	defer p.Stop()
	p.demandPath()
	plans := []tunnelQualityTunnelPlan{p.planTunnel(1, round, e.h.getNodeRecord), p.planTunnel(2, round, e.h.getNodeRecord)}
	if len(round.planned) != 4 || len(round.publicTests) != 1 {
		t.Fatalf("tasks=%v public=%v", round.planned, round.publicTests)
	}
	round.run(context.Background())
	for _, plan := range plans {
		snap := plan.snapshot(round)
		if snap.PathLatency != 5 || math.Abs(snap.PathLoss-10) > 0.0001 || snap.PathStatus != "ok" || snap.EntryToExitLatency != 40 || snap.EntryToExitLoss != 20 || snap.ExitToBingLatency != 99 || snap.ExitToBingLoss != 100 || !snap.Success || snap.ErrorMessage != "" {
			t.Fatalf("legacy or minimum path changed: %+v", snap)
		}
		p.storeResult(snap)
	}
	if len(calls) != 5 {
		t.Fatalf("calls=%v", calls)
	}
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("%v called %d times", key, count)
		}
	}
	rows, err := e.r.GetLatestTunnelQualities()
	if err != nil || len(rows) != 2 {
		t.Fatalf("history=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.EntryToExitLatency != 40 || row.EntryToExitLoss != 20 || row.ExitToBingLatency != 99 || row.ExitToBingLoss != 100 || row.Success != 1 || row.ErrorMessage != "" {
			t.Fatalf("persisted minimum in legacy history: %+v", row)
		}
	}
}

func TestTunnelQualityRoundDestinationDisconnectsAfterPlanning(t *testing.T) {
	round := newTunnelQualityProbeRound(func(int64, string, int, diagnosisExecOptions) (float64, float64, error) {
		t.Error("ping dispatched to a disconnected destination")
		return 10, 0, nil
	})
	online := true
	round.connected = func(id int64) bool { return id == 2 && online }
	key := tunnelQualityPingKey{1, "192.0.2.2", 443}
	round.planNodePing(key, 2, diagnosisExecOptions{})
	online = false
	round.run(context.Background())
	_, loss, err := round.result(key.nodeID, key.ip, key.port, diagnosisExecOptions{})
	if err == nil || err.Error() != "节点不在线" || loss != 100 {
		t.Fatalf("loss=%v err=%v", loss, err)
	}
}

// Three layers of two online nodes, plus a zero-hop tunnel whose legacy key
// shares one of the multi-hop path segments. No real agents or clock waits.
func TestTunnelQualityPathDemandWindow(t *testing.T) {
	e := newFlowTestEnv(t)
	for id := int64(1); id <= 6; id++ {
		e.addNode(id, fmt.Sprint(id))
	}
	for id := int64(1); id <= 2; id++ {
		e.addTunnel(id, 1, 1)
		e.exec(`UPDATE tunnel SET type=2 WHERE id=?`, id)
	}
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, port) VALUES
		(1,'1',1,0,1001),(1,'1',2,0,1002),
		(1,'2',3,1,1003),(1,'2',4,1,1004),
		(1,'3',5,0,1005),(1,'3',6,0,1006),
		(2,'1',1,0,1001),(2,'3',3,0,1003)`)
	p := newTunnelQualityProber(e.h)
	defer p.Stop()
	e.h.qualityProber = p
	now := time.Now()
	p.now = func() time.Time { return now }
	request := func() []userTunnelLatency {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil)
		token, err := auth.GenerateToken(1, "admin", 0, "demand-test")
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		middleware.JWT(middleware.AuthOptions{JWTSecret: "demand-test"})(http.HandlerFunc(e.h.userTunnelLatencyList)).ServeHTTP(res, req)
		var body struct {
			Code int
			Data []userTunnelLatency
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Code != 0 {
			t.Fatalf("response=%s err=%v", res.Body.String(), err)
		}
		if p.lastPathDemand.Load() != now.UnixNano() {
			t.Fatal("endpoint did not record demand")
		}
		return body.Data
	}
	var previousPathAt int64
	for _, stage := range []string{"idle", "demand", "within", "expired"} {
		switch stage {
		case "demand":
			if items := request(); len(items) != 0 {
				t.Fatalf("idle produced fake path results: %+v", items)
			}
		case "within":
			now = now.Add(59 * time.Second)
		case "expired":
			now = now.Add(2 * time.Second)
		}
		var mu sync.Mutex
		calls := map[tunnelQualityPingKey]int{}
		round := newTunnelQualityProbeRound(func(id int64, ip string, port int, options diagnosisExecOptions) (float64, float64, error) {
			mu.Lock()
			calls[tunnelQualityPingKey{id, ip, port}] = options.pingCount
			mu.Unlock()
			return 12, 0, nil
		})
		plans := []tunnelQualityTunnelPlan{p.planTunnel(1, round, e.h.getNodeRecord), p.planTunnel(2, round, e.h.getNodeRecord)}
		p.preparePathRound(round, plans, p.pathSession)
		round.run(context.Background())
		active := stage == "demand" || stage == "within"
		wantCalls := 4 // Two direct keys and two public exit tests.
		if stage == "demand" {
			wantCalls = 11
		} // Eight path keys, one extra direct, two public.
		if len(calls) != wantCalls {
			t.Fatalf("%s: executed=%v want=%d", stage, calls, wantCalls)
		}
		for key, count := range calls {
			wantCount := 4
			if active && key.port != 443 && key != *plans[0].direct && key != *plans[1].direct {
				wantCount = 2
			}
			if count != wantCount {
				t.Fatalf("%s: key=%+v count=%d want=%d", stage, key, count, wantCount)
			}
		}
		for _, plan := range plans {
			p.storeResult(plan.snapshot(round))
		}
		value, _ := p.cache.Load(int64(1))
		snap := value.(*tunnelQualitySnapshot)
		if active {
			if snap.PathStatus != "ok" || snap.PathLatency != 24 {
				t.Fatalf("%s: %+v", stage, snap)
			}
			previousPathAt = snap.PathUpdatedAt
		} else if stage == "idle" {
			if snap.PathStatus != "" || snap.PathUpdatedAt != 0 {
				t.Fatalf("fake timeout: %+v", snap)
			}
		} else if snap.PathUpdatedAt != previousPathAt || snap.PathStatus != "ok" || snap.PathLatency != 24 {
			t.Fatalf("idle round changed previous path: %+v", snap)
		}
		if stage == "demand" {
			items := request()
			if len(items) != 2 || items[0].LatencyMS != 24 || items[1].LatencyMS != 12 {
				t.Fatalf("endpoint=%+v", items)
			}
		}
	}
}

func TestTunnelQualityPlanPingKeepsLargerCount(t *testing.T) {
	for _, counts := range [][]int{{4, 2}, {2, 4}, {0, 2}, {2, 0}} {
		round := newTunnelQualityProbeRound(nil)
		key := tunnelQualityPingKey{1, "192.0.2.2", 443}
		for _, count := range counts {
			options := tunnelQualityProbeOptions()
			options.pingCount = count
			round.planPing(key, options)
		}
		if round.planned[key].pingCount != 4 {
			t.Fatalf("%v downgraded to %d", counts, round.planned[key].pingCount)
		}
	}
}

func TestTunnelQualityDemandWakeSerialRounds(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, "entry")
	e.addNode(2, "exit")
	e.exec(`UPDATE node SET is_remote=1`)
	e.addTunnel(1, 1, 1)
	e.exec(`UPDATE tunnel SET type=2 WHERE id=1`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, port) VALUES (1,'1',1,0,1001),(1,'3',2,0,1002)`)
	p := newTunnelQualityProber(e.h)
	p.interval = time.Hour // A round can only start via demand during this test.
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	p.now = func() time.Time { return time.Unix(0, clock.Load()) }
	started := make(chan struct{}, 20)
	release := make(chan struct{}, 20)
	var active, maxActive atomic.Int64
	p.ping = func(int64, string, int, diagnosisExecOptions) (float64, float64, error) {
		n := active.Add(1)
		for old := maxActive.Load(); n > old; old = maxActive.Load() {
			if maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-p.ctx.Done():
		}
		active.Add(-1)
		return 12, 0, nil
	}
	done := make(chan struct{})
	go func() { p.loop(); close(done) }()
	defer func() { p.Stop(); <-done }()
	await := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("demand did not wake round immediately")
		}
	}
	p.demandPath() // Also interrupts the initial five-second boot delay.
	await(started)
	await(started)
	for i := 0; i < 100; i++ {
		p.demandPath()
	}
	select {
	case <-started:
		t.Fatal("rounds overlapped while blocked")
	case <-time.After(30 * time.Millisecond):
	}
	release <- struct{}{}
	release <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for p.lastPathProbe.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.lastPathProbe.Load() == 0 {
		t.Fatal("path completion not recorded")
	}
	p.demandPath()
	select {
	case <-started:
		t.Fatal("queued/recent demand caused redundant round")
	case <-time.After(30 * time.Millisecond):
	}
	clock.Add(int64(16 * time.Second))
	p.demandPath()
	await(started)
	await(started)
	if maxActive.Load() != 2 {
		t.Fatalf("overlapping rounds: active pings=%d", maxActive.Load())
	}
}
