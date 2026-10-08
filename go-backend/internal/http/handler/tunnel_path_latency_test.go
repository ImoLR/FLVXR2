package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
)

func TestTunnelPathLatencyDirectReusesLegacyPing(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, "entry")
	e.addNode(2, "exit")
	e.addTunnel(1, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2 WHERE id = 1`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, port) VALUES (1, '1', 1, 0, 1001), (1, '3', 2, 0, 1002)`)
	calls := map[tunnelQualityPingKey]int{}
	var mu sync.Mutex
	round := newTunnelQualityProbeRound(func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		mu.Lock()
		calls[tunnelQualityPingKey{id, ip, port}]++
		mu.Unlock()
		if id == 1 {
			return 12.5, 3, nil
		}
		return 99, 0, nil
	})
	p := newTunnelQualityProber(e.h)
	defer p.Stop()
	p.demandPath()
	plan := p.planTunnel(1, round, e.h.getNodeRecord)
	round.run(context.Background())
	p.storeResult(plan.snapshot(round))
	snapshot := p.GetAll()[0]
	if snapshot.PathStatus != "ok" || snapshot.PathLatency != 12.5 || snapshot.EntryToExitLatency != 12.5 || snapshot.ExitToBingLatency != 99 || math.Abs(snapshot.PathLoss-3) > 0.0001 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if calls[tunnelQualityPingKey{1, "127.0.0.1", 1002}] != 1 || len(calls) != 2 {
		t.Fatalf("direct ping duplicated or public probe changed: %v", calls)
	}
	// Path fields must not change the monitor payload or database history.
	raw, _ := json.Marshal(snapshot)
	var fields map[string]interface{}
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["pathLatency"]; ok {
		t.Fatal("path leaked into monitor payload")
	}
	rows, err := e.r.GetLatestTunnelQualities()
	if err != nil || len(rows) != 1 || rows[0].EntryToExitLatency != 12.5 || rows[0].ExitToBingLatency != 99 {
		t.Fatalf("legacy history changed: %+v %v", rows, err)
	}
}

func pathLatencyFixture() ([]chainNodeRecord, map[int64]*nodeRecord) {
	// Deliberately put hop 2 first to check ordering by inx.
	rows := []chainNodeRecord{
		{ChainType: 1, NodeID: 1},
		{ChainType: 2, Inx: 2, NodeID: 3, Port: 3003},
		{ChainType: 2, Inx: 1, NodeID: 2, Port: 3002},
		{ChainType: 3, NodeID: 4, Port: 3004},
	}
	nodes := map[int64]*nodeRecord{}
	for id := int64(1); id <= 5; id++ {
		nodes[id] = &nodeRecord{ID: id, Status: 1, ServerIP: fmt.Sprintf("192.0.2.%d", id)}
	}
	return rows, nodes
}

func TestTunnelPathLatencyTwoHops(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	lookup := func(id int64) (*nodeRecord, error) { return nodes[id], nil }
	calls := []tunnelQualityPingKey{}
	ping := func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		calls = append(calls, tunnelQualityPingKey{id, ip, port})
		return float64(id) * 10, 10, nil
	}
	lat, loss, status := probeTunnelPath(rows, "", lookup, ping, diagnosisExecOptions{})
	want := []tunnelQualityPingKey{{1, "192.0.2.2", 3002}, {2, "192.0.2.3", 3003}, {3, "192.0.2.4", 3004}}
	if lat != 60 || math.Abs(loss-27.1) > 0.0001 || status != "ok" || !reflect.DeepEqual(calls, want) {
		t.Fatalf("latency=%v loss=%v status=%s calls=%v", lat, loss, status, calls)
	}
}

func TestTunnelPathLatencyTimeout(t *testing.T) {
	for _, reason := range []string{"offline", "error", "loss"} {
		t.Run(reason, func(t *testing.T) {
			rows, nodes := pathLatencyFixture()
			if reason == "offline" {
				nodes[2].Status = 0
			}
			lookup := func(id int64) (*nodeRecord, error) { return nodes[id], nil }
			ping := func(id int64, _ string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
				if id == 2 && reason == "error" {
					return 0, 0, errors.New("failed")
				}
				if id == 2 && reason == "loss" {
					return 0, 100, nil
				}
				return 10, 0, nil
			}
			lat, loss, status := probeTunnelPath(rows, "", lookup, ping, diagnosisExecOptions{})
			if status != "timeout" || loss != 100 || lat != 0 {
				t.Fatalf("%v %v %s", lat, loss, status)
			}
		})
	}
}

func TestTunnelPathLatencyOfflineMemberSkipped(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	nodes[2].Status = 0
	// First hop has an offline first node followed by two online nodes.
	rows = append(rows, chainNodeRecord{ChainType: 2, Inx: 1, NodeID: 5, Port: 3005}, chainNodeRecord{ChainType: 2, Inx: 1, NodeID: 3, Port: 3003})
	calls := []tunnelQualityPingKey{}
	lookup := func(id int64) (*nodeRecord, error) { return nodes[id], nil }
	ping := func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		calls = append(calls, tunnelQualityPingKey{id, ip, port})
		return 10, 0, nil
	}
	_, _, status := probeTunnelPath(rows, "", lookup, ping, diagnosisExecOptions{})
	want := []tunnelQualityPingKey{{1, "192.0.2.5", 3005}, {1, "192.0.2.3", 3003}, {5, "192.0.2.3", 3003}, {3, "192.0.2.3", 3003}, {3, "192.0.2.4", 3004}}
	if status != "ok" || !reflect.DeepEqual(calls, want) {
		t.Fatalf("%s %v", status, calls)
	}
}

func TestTunnelPathLatencyWholeGroupOffline(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	rows = append(rows, chainNodeRecord{ChainType: 2, Inx: 1, NodeID: 5, Port: 3005})
	nodes[2].Status, nodes[5].Status = 0, 0
	ping := func(id int64, ip string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
		if id == 2 || id == 5 || ip == "192.0.2.2" || ip == "192.0.2.5" {
			t.Fatal("offline endpoint was probed")
		}
		return 10, 0, nil
	}
	lat, loss, status := probeTunnelPath(rows, "", func(id int64) (*nodeRecord, error) { return nodes[id], nil }, ping, diagnosisExecOptions{})
	if lat != 0 || loss != 100 || status != "timeout" {
		t.Fatalf("%v %v %s", lat, loss, status)
	}
}

func TestTunnelPathLatencyMinimumCompletePath(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	nodes[6] = &nodeRecord{ID: 6, Status: 1, ServerIP: "192.0.2.6"}
	rows = append(rows, chainNodeRecord{ChainType: 2, Inx: 1, NodeID: 5, Port: 3005}, chainNodeRecord{ChainType: 2, Inx: 2, NodeID: 6, Port: 3006})
	type sample struct{ latency, loss float64 }
	samples := map[tunnelQualityPingKey]sample{
		{1, "192.0.2.2", 3002}: {1, 0},
		{1, "192.0.2.5", 3005}: {9, 10},
		{2, "192.0.2.3", 3003}: {20, 0},
		{5, "192.0.2.3", 3003}: {2, 0},
		{5, "192.0.2.6", 3006}: {3, 20},
		{3, "192.0.2.4", 3004}: {20, 0},
		{6, "192.0.2.4", 3004}: {1, 30},
	}
	calls := 0
	ping := func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		calls++
		value, ok := samples[tunnelQualityPingKey{id, ip, port}]
		if !ok {
			return 0, 100, errors.New("failed segment")
		}
		return value.latency, value.loss, nil
	}
	lat, loss, status := probeTunnelPath(rows, "", func(id int64) (*nodeRecord, error) { return nodes[id], nil }, ping, diagnosisExecOptions{})
	// Independent layer minima incorrectly produce 1+2+1=4. The real minimum
	// is entry→5→6→exit (9+3+1), carrying this path's 10%,20%,30% loss.
	if status != "ok" || lat != 13 || math.Abs(loss-49.6) > 0.0001 || calls != 8 {
		t.Fatalf("latency=%v loss=%v status=%s calls=%d", lat, loss, status, calls)
	}
}

func TestTunnelPathLatencyMultipleEntriesAndExits(t *testing.T) {
	rows := []chainNodeRecord{
		{ChainType: 1, NodeID: 1}, {ChainType: 1, NodeID: 2},
		{ChainType: 2, Inx: 1, NodeID: 3, Port: 3003}, {ChainType: 2, Inx: 1, NodeID: 4, Port: 3004},
		{ChainType: 3, NodeID: 5, Port: 3005}, {ChainType: 3, NodeID: 6, Port: 3006},
	}
	lookup := func(id int64) (*nodeRecord, error) {
		return &nodeRecord{ID: id, Status: 1, ServerIP: fmt.Sprintf("192.0.2.%d", id)}, nil
	}
	calls := 0
	ping := func(id int64, ip string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
		calls++
		if id == 2 && ip == "192.0.2.4" {
			return 2, 0, nil
		}
		if id == 4 && ip == "192.0.2.6" {
			return 3, 0, nil
		}
		return 10, 0, nil
	}
	lat, _, status := probeTunnelPath(rows, "", lookup, ping, diagnosisExecOptions{})
	if status != "ok" || lat != 5 || calls != 8 {
		t.Fatalf("latency=%v status=%s calls=%d", lat, status, calls)
	}
}

func TestTunnelPathLatencyUnreachableLayerStillSamplesPairs(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	calls := 0
	ping := func(id int64, _ string, _ int, _ diagnosisExecOptions) (float64, float64, error) {
		calls++
		if id == 1 {
			return 0, 100, errors.New("failed")
		}
		return 10, 0, nil
	}
	lat, loss, status := probeTunnelPath(rows, "", func(id int64) (*nodeRecord, error) { return nodes[id], nil }, ping, diagnosisExecOptions{})
	if status != "timeout" || lat != 0 || loss != 100 || calls != 3 {
		t.Fatalf("latency=%v loss=%v status=%s calls=%d", lat, loss, status, calls)
	}
}

func TestTunnelPathLatencyRoundDedupe(t *testing.T) {
	rows, nodes := pathLatencyFixture()
	lookup := func(id int64) (*nodeRecord, error) { return nodes[id], nil }
	var mu sync.Mutex
	calls := map[tunnelQualityPingKey]int{}
	round := newTunnelQualityProbeRound(func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		mu.Lock()
		calls[tunnelQualityPingKey{id, ip, port}]++
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return 10, 0, nil
	})
	second := append([]chainNodeRecord(nil), rows...)
	second[3].NodeID = 5
	var wg sync.WaitGroup
	for _, path := range [][]chainNodeRecord{rows, second} {
		wg.Add(1)
		go func(path []chainNodeRecord) {
			defer wg.Done()
			lat, _, status := probeTunnelPath(path, "", lookup, round.ping, diagnosisExecOptions{})
			if status != "ok" || lat != 30 {
				t.Errorf("%v %s", lat, status)
			}
		}(path)
	}
	wg.Wait()
	if len(calls) != 4 {
		t.Fatalf("calls=%v", calls)
	}
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("%v called %d times", key, count)
		}
	}
	// A new round must sample again, not reuse yesterday's result.
	next := newTunnelQualityProbeRound(round.execute)
	_, _, _ = next.ping(1, "192.0.2.2", 3002, diagnosisExecOptions{})
	if calls[tunnelQualityPingKey{1, "192.0.2.2", 3002}] != 2 {
		t.Fatal("cache survived round")
	}
}

func TestUserTunnelLatencyPermissionsAndFreshness(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(3)
	p := newTunnelQualityProber(e.h)
	defer p.Stop()
	e.h.qualityProber = p
	now := time.Now().UnixMilli()
	for id := int64(1); id <= 7; id++ {
		e.addTunnel(id, 1, 1)
		e.exec(`UPDATE tunnel SET type=2 WHERE id=?`, id)
		if id != 5 {
			p.cache.Store(id, &tunnelQualitySnapshot{TunnelID: id, PathLatency: 46, PathStatus: "ok", PathUpdatedAt: now})
		}
	}
	e.exec(`UPDATE tunnel SET type=1 WHERE id=4`)
	e.exec(`UPDATE tunnel SET status=0 WHERE id=6`)
	p.cache.Store(int64(3), &tunnelQualitySnapshot{TunnelID: 3, PathLatency: 46, PathStatus: "ok", PathUpdatedAt: now - 61000})
	p.cache.Store(int64(7), &tunnelQualitySnapshot{TunnelID: 7, PathStatus: "timeout", PathUpdatedAt: now})
	for _, id := range []int64{1, 3, 4, 5, 6, 7} {
		e.addUserTunnel(id, 3, id)
	}
	mux := http.NewServeMux()
	e.h.Register(mux)
	wrapped := middleware.JWT(middleware.AuthOptions{JWTSecret: "latency-test-secret"})(mux)
	for _, tc := range []struct {
		name string
		role int
		want []int64
	}{{"admin", 0, []int64{1, 2, 6, 7}}, {"user", 1, []int64{1, 7}}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil)
			token, err := auth.GenerateToken(3, "latency-user", tc.role, "latency-test-secret")
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", token)
			res := httptest.NewRecorder()
			wrapped.ServeHTTP(res, req)
			var envelope struct {
				Code int
				Data []map[string]interface{}
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil || envelope.Code != 0 {
				t.Fatalf("%s", res.Body.String())
			}
			ids := []int64{}
			for _, item := range envelope.Data {
				if len(item) != 4 || item["tunnelId"] == nil || item["latencyMs"] == nil || item["status"] == nil || item["updatedAt"] == nil {
					t.Fatalf("unexpected fields (node metadata prohibited): %v", item)
				}
				ids = append(ids, int64(item["tunnelId"].(float64)))
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("got %v want %v", ids, tc.want)
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil)
	res := httptest.NewRecorder()
	wrapped.ServeHTTP(res, req)
	var failure struct{ Code int }
	_ = json.Unmarshal(res.Body.Bytes(), &failure)
	if failure.Code != 401 {
		t.Fatalf("unauthenticated response=%s", res.Body.String())
	}
}
