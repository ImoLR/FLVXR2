package handler

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/repo"
)

// Plan 180 resource numbers (not run in CI): probes per round and the CPU of
// the decision step for the 31 prod-shaped tunnels with prod-like rules and
// with 200 seeded rules. FLVX_RULE_BEST_SCALE=1 runs it;
// FLVX_RULE_BEST_SCALE_DB=<copy of a panel DB> also counts on that data.
func TestRuleBestScaleNumbers(t *testing.T) {
	if os.Getenv("FLVX_RULE_BEST_SCALE") == "" {
		t.Skip("set FLVX_RULE_BEST_SCALE=1")
	}
	if path := os.Getenv("FLVX_RULE_BEST_SCALE_DB"); path != "" {
		r, err := repo.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		h := &Handler{repo: r, bestPath: newBestPathManager(), ruleBest: newRuleBestManager()}
		rbScaleReport(t, "db-copy", h)
	}
	for _, rules := range []int{17, 200} {
		e := newFlowTestEnv(t)
		for id := int64(1); id <= 60; id++ {
			e.addNode(id, fmt.Sprintf("s%d", id))
			e.exec(`UPDATE node SET server_ip = ?, server_ip_v4 = ? WHERE id = ?`, fmt.Sprintf("198.51.100.%d", id), fmt.Sprintf("198.51.100.%d", id), id)
		}
		tunnelIDs := []int64{}
		for i, shape := range rbProdShapes {
			tid := int64(100 + i)
			tunnelIDs = append(tunnelIDs, tid)
			e.addTunnel(tid, 1, 1)
			e.exec(`UPDATE tunnel SET type = 2 WHERE id = ?`, tid)
			for g, group := range rbParseShape(shape) {
				chainType := 2
				if g == 0 {
					chainType = 1
				} else if g == len(strings.Split(shape, "|"))-1 {
					chainType = 3
				}
				for _, n := range group {
					e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(?, ?, ?, ?, ?, ?, 'tls')`, tid, fmt.Sprint(chainType), n.NodeID, n.Port, n.Strategy, g)
				}
			}
		}
		e.addUser(2)
		for i := 0; i < rules; i++ {
			id := int64(1000 + i)
			e.addForward(id, 2, tunnelIDs[i%len(tunnelIDs)])
			// Prod: 17 rules, 14 distinct targets; seeded: 200 rules, 120 targets.
			e.exec(`UPDATE forward SET remote_addr = ?, mode = 'gost' WHERE id = ?`, fmt.Sprintf("t%d.example:%d", i%(rules*7/10+1), 10000+i%(rules*7/10+1)), id)
		}
		e.h.bestPath, e.h.ruleBest = newBestPathManager(), newRuleBestManager()
		e.h.chainSwapCapableFn = func(int64) bool { return true }
		e.h.destOrderCapableFn = func(int64) bool { return true }
		e.h.nodeOnlineFn = func(int64) bool { return true }
		rbScaleReport(t, fmt.Sprintf("seeded-%d-rules", rules), e.h)
	}
}

func rbScaleReport(t *testing.T, name string, h *Handler) {
	t.Helper()
	plans, pairs, edges, err := h.planPathLatencyRound()
	if err != nil {
		t.Fatal(err)
	}
	edgePairs := len(pairs)
	now := time.Now()
	probes := h.attachRuleBestProbes(plans, pairs, now)
	dests := 0
	ruleTunnels := 0
	for _, p := range plans {
		if p.RuleBest != nil {
			ruleTunnels++
			dests += len(p.RuleBest.Dests)
		}
	}
	// Every probe answers (synthetic latency by key) — worst case for the
	// decision step (every owner × destination evaluated).
	results := make(map[pathProbeKey]pathProbeResult, len(pairs))
	for k := range pairs {
		results[k] = pathProbeResult{OK: true, LatencyMS: float64(5 + (len(k.Host)*7+k.Port+int(k.From)*13)%90)}
	}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.HeapAlloc
	start := time.Now()
	const rounds = 20
	for i := 0; i < rounds; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		dead, recovered, _ := h.ruleBest.updateDead(plans, results, at)
		h.ruleBest.storeViews(plans, results, dead, at)
		h.ruleBest.evaluate(plans, results, dead, recovered, func(k bestPathKey) []int64 { return h.bestPath.order(k) }, func(int64) (bool, bool) { return true, true }, at)
	}
	per := time.Since(start) / rounds
	runtime.GC()
	runtime.ReadMemStats(&ms)
	states, _, _, views := h.ruleBest.size()
	t.Logf("%s: tunnels=%d edges=%d edge_pairs=%d rule_best_tunnels=%d dests=%d rule_best_probes=%d (pairs %d→%d, +%.0f%%) decision_per_round=%s states=%d views=%d heap_delta=%dKiB",
		name, len(plans), edges, edgePairs, ruleTunnels, dests, probes, edgePairs, len(pairs), 100*float64(probes)/float64(max(edgePairs, 1)), per.Round(time.Microsecond), states, views, (int64(ms.HeapAlloc)-int64(before))/1024)
}
