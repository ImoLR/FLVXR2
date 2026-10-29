package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/ws"
)

// Plan 165 runtime: a real SQLite panel with the command log. Tunnel 20:
// entry 1 → 中转 {2, 3} 最优 → 出口 {4, 5} 最优.
func newBestPathEnv(t *testing.T) (*flowTestEnv, *redeployCommandLog) {
	t.Helper()
	e := newFlowTestEnv(t)
	for id := int64(1); id <= 5; id++ {
		e.addNode(id, fmt.Sprintf("n%d-secret", id))
		e.exec(`UPDATE node SET name = ?, server_ip = ?, server_ip_v4 = ?, tcp_listen_addr = '0.0.0.0', udp_listen_addr = '0.0.0.0' WHERE id = ?`,
			fmt.Sprintf("node-%d", id), fmt.Sprintf("192.0.2.%d", id), fmt.Sprintf("192.0.2.%d", id), id)
	}
	e.addTunnel(20, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2, name = 'best-tunnel' WHERE id = 20`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(20, '1', 1, NULL, 'round', 1, 'tls'),
		      (20, '2', 2, 1302, 'best', 1, 'tls'),
		      (20, '2', 3, 1303, 'best', 1, 'tls'),
		      (20, '3', 4, 1304, 'best', 1, 'tls'),
		      (20, '3', 5, 1305, 'best', 2, 'tls')`)
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	e.h.bestPath = newBestPathManager()
	e.h.chainSwapCapableFn = func(int64) bool { return true }
	e.h.nodeOnlineFn = func(int64) bool { return true }
	return e, commands
}

// bestPathEnvRound runs the 最优 step on a real plan with these edge latencies
// (missing = failed in the round).
func bestPathEnvRound(t *testing.T, e *flowTestEnv, lat map[[2]int64]float64, at time.Time) {
	t.Helper()
	plans, _, _, err := e.h.planPathLatencyRound()
	if err != nil {
		t.Fatal(err)
	}
	results := map[pathProbeKey]pathProbeResult{}
	for _, plan := range plans {
		for _, layer := range plan.Layers {
			for _, edge := range layer {
				if v, ok := lat[[2]int64{edge.From, edge.To}]; ok {
					results[edge.Key] = pathProbeResult{LatencyMS: v, OK: true}
				} else {
					results[edge.Key] = pathProbeResult{}
				}
			}
		}
	}
	e.h.runBestPathRound(plans, results, at)
}

func bpExampleLat() map[[2]int64]float64 {
	// via 2: 10 + min(100, 120) = 110; via 3: 30 + min(20, 25) = 50.
	return map[[2]int64]float64{{1, 2}: 10, {1, 3}: 30, {2, 4}: 100, {2, 5}: 120, {3, 4}: 20, {3, 5}: 25}
}

// chainOrder reads node addresses and the selector strategy of a pushed chain
// (AddChains config or UpdateChains envelope).
func chainOrder(t *testing.T, data interface{}) ([]string, string) {
	t.Helper()
	raw, _ := json.Marshal(data)
	var payload map[string]interface{}
	_ = json.Unmarshal(raw, &payload)
	if inner, ok := payload["data"].(map[string]interface{}); ok {
		payload = inner
	}
	hops, _ := payload["hops"].([]interface{})
	if len(hops) != 1 {
		t.Fatalf("chain without one hop: %s", raw)
	}
	hop := hops[0].(map[string]interface{})
	var addrs []string
	for _, n := range hop["nodes"].([]interface{}) {
		addrs = append(addrs, n.(map[string]interface{})["addr"].(string))
	}
	return addrs, hop["selector"].(map[string]interface{})["strategy"].(string)
}

func commandsSince(log *redeployCommandLog, from int) []redeployCommand {
	all := log.snapshot()
	if from > len(all) {
		return nil
	}
	return all[from:]
}

func TestBestPathRuntimeSwitchPushesOnlyOnChangeAndSticks(t *testing.T) {
	e, commands := newBestPathEnv(t)
	now := time.Unix(1_800_000_000, 0)

	bestPathEnvRound(t, e, bpExampleLat(), now)
	if n := len(commands.snapshot()); n != 0 {
		t.Fatalf("round 1 pushed %d commands: %+v", n, commands.snapshot())
	}
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(time.Minute))
	got := commands.snapshot()
	if len(got) != 1 || got[0].nodeID != 1 || got[0].method != "UpdateChains" {
		t.Fatalf("round 2 must push one UpdateChains to the entry only: %+v", got)
	}
	if env := got[0].data.(map[string]interface{}); env["chain"] != "chains_20" {
		t.Fatalf("envelope chain = %v", env["chain"])
	}
	addrs, strategy := chainOrder(t, got[0].data)
	if strings.Join(addrs, ",") != "192.0.2.3:1303,192.0.2.2:1302" || strategy != "fifo" {
		t.Fatalf("pushed order %v strategy %s, want node 3 first, fifo", addrs, strategy)
	}
	switchData := got[0].data

	// Same results: nothing to push.
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(2*time.Minute))
	if n := len(commands.snapshot()); n != 1 {
		t.Fatalf("unchanged round pushed again: %+v", commandsSince(commands, 1))
	}

	// Resync / edit redeploy of the entry keeps the decided order (one
	// UpdateChains, no DeleteChains window).
	mark := len(commands.snapshot())
	if err := e.h.redeployTunnelRuntimeOnNode(20, 1); err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	after := commandsSince(commands, mark)
	if len(after) != 1 || after[0].method != "UpdateChains" {
		t.Fatalf("capable redeploy commands: %+v", after)
	}
	if addrs, _ := chainOrder(t, after[0].data); addrs[0] != "192.0.2.3:1303" {
		t.Fatalf("redeploy reverted to DB order: %v", addrs)
	}

	// Reconnect diff: the agent already runs the switched chain → nothing sent.
	revs := ws.ExpectedRuntimeRevs("UpdateChains", switchData)
	inv := newAgentRuntimeInventory(runtimeInventory{Chains: []string{"chains_20"}, Revs: revs})
	unlock := e.h.lockTunnelRuntime(20)
	changed, err := e.h.redeployTunnelRuntimeOnNodeDiffLocked(20, 1, inv)
	unlock()
	if err != nil || changed {
		t.Fatalf("reconnect diff: changed=%v err=%v (revs %v)", changed, err, revs)
	}

	// Old agent: static DB order through DeleteChains + AddChains, as before.
	e.h.chainSwapCapableFn = func(int64) bool { return false }
	mark = len(commands.snapshot())
	if err := e.h.redeployTunnelRuntimeOnNode(20, 1); err != nil {
		t.Fatalf("old-agent redeploy: %v", err)
	}
	after = commandsSince(commands, mark)
	if len(after) != 2 || after[0].method != "DeleteChains" || after[1].method != "AddChains" {
		t.Fatalf("old agent commands: %+v", after)
	}
	if addrs, strategy := chainOrder(t, after[1].data); addrs[0] != "192.0.2.2:1302" || strategy != "fifo" {
		t.Fatalf("old agent order %v strategy %s, want DB order fifo", addrs, strategy)
	}
}

func TestBestPathRuntimeFailedPushRollsBackAndRetries(t *testing.T) {
	e, commands := newBestPathEnv(t)
	now := time.Unix(1_800_000_000, 0)
	commands.fail = func(nodeID int64, method string) error {
		if method == "UpdateChains" {
			return errors.New("等待节点响应超时")
		}
		return nil
	}
	bestPathEnvRound(t, e, bpExampleLat(), now)
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(time.Minute))
	if commands.count(1, "UpdateChains") != 1 {
		t.Fatalf("push not attempted: %+v", commands.snapshot())
	}
	if order := e.h.bestPath.order(bestPathKey{20, 1}); len(order) != 0 {
		t.Fatalf("failed push must restore the previous (DB) order, got %v", order)
	}
	commands.fail = nil
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(2*time.Minute))
	if commands.count(1, "UpdateChains") != 2 || fmt.Sprint(e.h.bestPath.order(bestPathKey{20, 1})) != "[3 2]" {
		t.Fatalf("retry next round: %+v order %v", commands.snapshot(), e.h.bestPath.order(bestPathKey{20, 1}))
	}
}

func TestBestPathRuntimeDeadNodeFailoverAndCleanup(t *testing.T) {
	e, commands := newBestPathEnv(t)
	now := time.Unix(1_800_000_000, 0)
	bestPathEnvRound(t, e, bpExampleLat(), now)
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(time.Minute))
	// Node 3 dies: its edges fail → the entry goes back to node 2 in the same
	// round; owner 3 itself (exit layer) has nothing reachable → no push.
	lat := bpExampleLat()
	delete(lat, [2]int64{1, 3})
	delete(lat, [2]int64{3, 4})
	delete(lat, [2]int64{3, 5})
	mark := len(commands.snapshot())
	bestPathEnvRound(t, e, lat, now.Add(2*time.Minute))
	after := commandsSince(commands, mark)
	if len(after) != 1 || after[0].nodeID != 1 {
		t.Fatalf("failover pushes: %+v", after)
	}
	if addrs, _ := chainOrder(t, after[0].data); addrs[0] != "192.0.2.2:1302" {
		t.Fatalf("failover order %v", addrs)
	}
	if e.h.bestPath.size() != 3 {
		t.Fatalf("states = %d, want 3 owners", e.h.bestPath.size())
	}
	// Tunnel disabled, then deleted: states go with the next round.
	e.exec(`UPDATE tunnel SET status = 0 WHERE id = 20`)
	bestPathEnvRound(t, e, lat, now.Add(3*time.Minute))
	if e.h.bestPath.size() != 0 {
		t.Fatalf("disabled tunnel kept %d states", e.h.bestPath.size())
	}
	e.exec(`UPDATE tunnel SET status = 1 WHERE id = 20`)
	bestPathEnvRound(t, e, lat, now.Add(4*time.Minute))
	e.exec(`DELETE FROM chain_tunnel WHERE tunnel_id = 20`)
	e.exec(`DELETE FROM tunnel WHERE id = 20`)
	bestPathEnvRound(t, e, lat, now.Add(5*time.Minute))
	if e.h.bestPath.size() != 0 {
		t.Fatalf("deleted tunnel kept %d states", e.h.bestPath.size())
	}
}

func TestBestPathAdminDisplay(t *testing.T) {
	e, _ := newBestPathEnv(t)
	list := func() map[string]interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		e.h.tunnelList(w, httptest.NewRequest(http.MethodPost, "/api/v1/tunnel/list", nil))
		var payload struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, item := range payload.Data {
			if item["id"] == float64(20) {
				if _, old := item["bestExitState"]; old {
					t.Fatalf("legacy bestExitState still sent")
				}
				state, _ := item["bestPathState"].(map[string]interface{})
				return state
			}
		}
		t.Fatalf("tunnel 20 missing")
		return nil
	}
	layer := func(state map[string]interface{}, i int) map[string]interface{} {
		return state["layers"].([]interface{})[i].(map[string]interface{})
	}
	state := list()
	if len(state["layers"].([]interface{})) != 2 || layer(state, 0)["label"] != "中转" || layer(state, 1)["label"] != "出口" {
		t.Fatalf("layers: %+v", state)
	}
	if layer(state, 0)["status"] != "waiting" || layer(state, 0)["summary"] != "等待探测" {
		t.Fatalf("before the first round: %+v", layer(state, 0))
	}
	now := time.Now()
	bestPathEnvRound(t, e, bpExampleLat(), now.Add(-time.Minute))
	bestPathEnvRound(t, e, bpExampleLat(), now)
	state = list()
	hop, exit := layer(state, 0), layer(state, 1)
	if hop["status"] != "applied" || hop["summary"] != "node-3" || hop["minMs"] != float64(50) {
		t.Fatalf("hop layer: %+v", hop)
	}
	// Exit layer: owners 2 and 3 both use node 4 (100 ms / 20 ms).
	if exit["summary"] != "node-4" || exit["minMs"] != float64(20) || exit["maxMs"] != float64(100) || len(exit["items"].([]interface{})) != 2 {
		t.Fatalf("exit layer: %+v", exit)
	}
	item := hop["items"].([]interface{})[0].(map[string]interface{})
	cands := item["candidates"].([]interface{})
	if item["ownerName"] != "node-1" || len(cands) != 2 || cands[0].(map[string]interface{})["name"] != "node-3" {
		t.Fatalf("hop item: %+v", item)
	}
	// Old agent: 节点需升级 (static order).
	e.h.chainSwapCapableFn = func(int64) bool { return false }
	if hop := layer(list(), 0); hop["status"] != "upgrade" || hop["summary"] != "节点需升级" {
		t.Fatalf("old agent display: %+v", hop)
	}
	// Owner offline.
	e.h.chainSwapCapableFn = func(int64) bool { return true }
	e.h.nodeOnlineFn = func(id int64) bool { return id != 1 }
	if hop := layer(list(), 0); hop["status"] != "offline" {
		t.Fatalf("offline display: %+v", hop)
	}
	// Not 最优 any more → no state at all.
	e.exec(`UPDATE chain_tunnel SET strategy = 'fifo' WHERE tunnel_id = 20`)
	if state := list(); state != nil {
		t.Fatalf("fifo tunnel has a 最优 state: %+v", state)
	}
}

func TestBestPathCreateDefaultsAndRuntimeMapping(t *testing.T) {
	e, commands := newBestPathEnv(t)
	e.h.chainSwapCapableFn = nil // no sessions: old-agent path on create
	code, msg := redeployMutationRequest(t, e.h.tunnelCreate, map[string]interface{}{
		"name": "default-best", "type": 2, "status": 1,
		"inNodeId":   []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
		"chainNodes": [][]map[string]interface{}{{{"nodeId": 2, "port": 1402, "protocol": "tls"}, {"nodeId": 3, "port": 1403, "protocol": "tls"}}},
		"outNodeId":  []map[string]interface{}{{"nodeId": 4, "port": 1404, "protocol": "tls"}, {"nodeId": 5, "port": 1405, "protocol": "tls", "strategy": "round"}},
	})
	if code != 0 {
		t.Fatalf("create: %d %s", code, msg)
	}
	var rows []struct {
		ChainType string
		NodeID    int64
		Strategy  string
	}
	e.r.DB().Raw(`SELECT chain_type, node_id, strategy FROM chain_tunnel WHERE tunnel_id = (SELECT id FROM tunnel WHERE name = 'default-best') ORDER BY chain_type, inx, node_id`).Scan(&rows)
	want := map[int64]string{1: "round", 2: "best", 3: "best", 4: "best", 5: "round"}
	for _, row := range rows {
		if want[row.NodeID] != row.Strategy {
			t.Fatalf("row %+v, want strategy %s (all %+v)", row, want[row.NodeID], rows)
		}
	}
	// The entry's chain to the 最优 hop runs fifo (never "best", which agents read as round-robin).
	for _, c := range commands.snapshot() {
		if c.nodeID == 1 && c.method == "AddChains" {
			if _, strategy := chainOrder(t, c.data); strategy != "fifo" {
				t.Fatalf("entry chain strategy %s", strategy)
			}
		}
	}
	// Update with an empty strategy keeps the old default (existing tunnels untouched).
	code, msg = redeployMutationRequest(t, e.h.tunnelUpdate, map[string]interface{}{
		"id": 20, "name": "best-tunnel", "type": 2, "status": 1,
		"inNodeId":  []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
		"outNodeId": []map[string]interface{}{{"nodeId": 4, "port": 1304, "protocol": "tls"}, {"nodeId": 5, "port": 1305, "protocol": "tls"}},
	})
	if code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	if n := mustQueryInt(t, e.r, `SELECT count(*) FROM chain_tunnel WHERE tunnel_id = 20 AND chain_type = '3' AND strategy = 'round'`); n != 2 {
		t.Fatalf("update default changed: %d round exits", n)
	}
}

func TestChainRevMatchesAcrossAddAndUpdate(t *testing.T) {
	nodes := map[int64]*nodeRecord{1: {ID: 1, ServerIPv4: "192.0.2.1"}, 2: {ID: 2, ServerIPv4: "192.0.2.2"}, 3: {ID: 3, ServerIPv4: "192.0.2.3"}}
	data, err := buildTunnelChainConfig(30, 1, []tunnelRuntimeNode{{NodeID: 3, Port: 3, Strategy: "best"}, {NodeID: 2, Port: 2, Strategy: "best"}}, nodes, "")
	if err != nil {
		t.Fatal(err)
	}
	add := ws.ExpectedRuntimeRevs("AddChains", data)
	upd := ws.ExpectedRuntimeRevs("UpdateChains", map[string]interface{}{"chain": "chains_30", "data": data})
	if len(add) != 1 || add["chain:chains_30"] == "" || add["chain:chains_30"] != upd["chain:chains_30"] {
		t.Fatalf("revs differ: add %v update %v", add, upd)
	}
}
