package handler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
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
	round.planPing(tunnelQualityPingKey{1, exitTestTargets[0].host, 443}, options)
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
