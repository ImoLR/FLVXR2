package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/ws"
)

func TestPathLatencyCompleteMinimumPerEntry(t *testing.T) {
	edge := func(from, to int64) pathLatencyEdge {
		return pathLatencyEdge{From: from, To: to, Key: pathProbeKey{From: from, Host: string(rune(to)), Port: 443}}
	}
	for _, tc := range []struct {
		name   string
		layers [][]pathLatencyEdge
		costs  []float64
		want   float64
	}{
		{"zero hop is entry to exit", [][]pathLatencyEdge{{edge(1, 4)}}, []float64{10}, 10},
		{"two hops exclude public internet", [][]pathLatencyEdge{{edge(1, 2)}, {edge(2, 3)}, {edge(3, 4)}}, []float64{10, 20, 30}, 60},
		{"minimum complete route", [][]pathLatencyEdge{{edge(1, 2), edge(1, 3)}, {edge(2, 4), edge(3, 4)}}, []float64{1, 20, 100, 5}, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := map[pathProbeKey]pathProbeResult{}
			i := 0
			for _, layer := range tc.layers {
				for _, edge := range layer {
					results[edge.Key] = pathProbeResult{LatencyMS: tc.costs[i], OK: true}
					i++
				}
			}
			plan := pathLatencyPlan{Entries: []chainNodeRecord{{NodeID: 1}, {NodeID: 9}}, Layers: tc.layers}
			got := calculatePathLatency(plan, results, time.Now())
			if got.Entries[0].Status != "ok" || got.Entries[0].LatencyMS != tc.want || got.Entries[1].Status != "timeout" {
				t.Fatalf("path=%+v", got)
			}
			for _, edge := range tc.layers[len(tc.layers)-1] {
				delete(results, edge.Key)
			}
			got = calculatePathLatency(plan, results, time.Now())
			if got.Entries[0].Status != "timeout" || got.Entries[0].LatencyMS != 0 {
				t.Fatalf("partial sum leaked: %+v", got)
			}
		})
	}
}

func TestPathLatencyDedupeOfflineAndProbeOptions(t *testing.T) {
	e := newFlowTestEnv(t)
	for _, id := range []int64{1, 2, 3} {
		e.addNode(id, "secret")
	}
	for _, id := range []int64{10, 11} {
		e.addTunnel(id, 1, 1)
		e.exec("UPDATE tunnel SET type=2 WHERE id=?", id)
		e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port) VALUES(?, '1', 1, NULL), (?, '3', 2, 1500)", id, id)
	}
	e.h.nodeCommandSender = func(id int64, command string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
		p := payload.(map[string]interface{})
		if command != "TcpPing" || id != 1 || p["count"] != 2 || p["timeout"] != 1000 || timeout != 4*time.Second {
			t.Errorf("unexpected probe %d %s %v %v", id, command, p, timeout)
		}
		return ws.CommandResult{Data: map[string]interface{}{"success": true, "averageTime": 46.0}}, nil
	}
	plans, pairs, edges, err := e.h.planPathLatencyRound()
	if err != nil || len(plans) != 2 || len(pairs) != 1 || edges != 2 {
		t.Fatalf("plan=%+v pairs=%v edges=%d err=%v", plans, pairs, edges, err)
	}
	calls := 0
	results, retries := probePathPairs(context.Background(), pairs, func(k pathProbeKey, n *nodeRecord) pathProbeResult { calls++; return e.h.probePathPair(k, n) }, waitPathRetry)
	if calls != 1 || retries != 0 || len(results) != 1 {
		t.Fatalf("calls=%d retries=%d results=%v", calls, retries, results)
	}
	e.exec("UPDATE node SET status=0 WHERE id=2")
	plans, pairs, _, err = e.h.planPathLatencyRound()
	if err != nil || len(pairs) != 0 {
		t.Fatalf("offline pairs=%v err=%v", pairs, err)
	}
	if got := calculatePathLatency(plans[0], nil, time.Now()); got.Entries[0].Status != "timeout" {
		t.Fatalf("offline result=%+v", got)
	}
	// Same target host/port, different source, is a different pair.
	e.exec("UPDATE node SET status=1 WHERE id=2")
	e.exec("UPDATE chain_tunnel SET node_id=3 WHERE tunnel_id=11 AND chain_type='1'")
	_, pairs, _, _ = e.h.planPathLatencyRound()
	if len(pairs) != 2 {
		t.Fatalf("source was omitted from key: %v", pairs)
	}
}

func TestPathLatencyRetryOnlyFailedPairOnceAfterTenSeconds(t *testing.T) {
	pairs := map[pathProbeKey]*nodeRecord{{From: 1}: {}, {From: 2}: {}, {From: 3}: {}}
	var mu sync.Mutex
	calls, waits := map[int64]int{}, 0
	probe := func(key pathProbeKey, _ *nodeRecord) pathProbeResult {
		mu.Lock()
		defer mu.Unlock()
		calls[key.From]++
		return pathProbeResult{OK: key.From == 1 || key.From == 3 && calls[3] == 2, LatencyMS: 1}
	}
	wait := func(_ context.Context, delay time.Duration) bool {
		if delay != 10*time.Second {
			t.Errorf("retry delay=%v", delay)
		}
		mu.Lock()
		waits++
		mu.Unlock()
		return true
	}
	results, retries := probePathPairs(context.Background(), pairs, probe, wait)
	if retries != 2 || waits != 2 || calls[1] != 1 || calls[2] != 2 || calls[3] != 2 || results[pathProbeKey{From: 2}].OK || !results[pathProbeKey{From: 3}].OK {
		t.Fatalf("calls=%v waits=%d retries=%d results=%v", calls, waits, retries, results)
	}
	// The next round has a fresh attempt budget.
	probePathPairs(context.Background(), pairs, probe, wait)
	if calls[1] != 2 || calls[2] != 4 {
		t.Fatalf("new round calls=%v", calls)
	}
}

func TestPathLatencyOmitsStaleSnapshot(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addTunnel(10, 1, 1)
	e.addNode(1, "secret")
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(10, '1', 1)")
	e.h.pathLatency = map[int64]tunnelPathLatency{10: {TunnelID: 10, UpdatedAt: time.Now().Add(-151 * time.Second).UnixMilli(), Entries: []pathEntryLatency{{NodeID: 1, Status: "ok", LatencyMS: 10}}}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil)
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	e.h.userTunnelLatency(w, r)
	var body struct {
		Code int
		Data []tunnelPathLatency
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != 0 || len(body.Data) != 0 {
		t.Fatalf("stale response=%s err=%v", w.Body.String(), err)
	}
}
