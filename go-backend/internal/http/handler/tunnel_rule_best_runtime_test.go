package handler

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Plan 180 runtime: tunnel 20 of newBestPathEnv (entry 1 → 中转 {2, 3} 最优 →
// 出口 {4, 5} 最优) with rules:
//
//	31  a.example:443,b.example:443  (two targets)
//	32  b.example:443                (same target as 31)
//	33  c.example:443  nftables      (kernel on the entry: no rule-best)
//	34  d.example:443  paused
func newRuleBestEnv(t *testing.T) (*flowTestEnv, *redeployCommandLog) {
	t.Helper()
	e, commands := newBestPathEnv(t)
	e.h.ruleBest = newRuleBestManager()
	e.h.destOrderCapableFn = func(int64) bool { return true }
	e.addUser(2)
	for id, addr := range map[int64]string{31: "a.example:443,b.example:443", 32: "b.example:443", 33: "c.example:443", 34: "d.example:443"} {
		e.addForward(id, 2, 20)
		e.exec(`UPDATE forward SET remote_addr = ?, mode = 'gost', name = ? WHERE id = ?`, addr, "rule-"+addr, id)
		e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(?, 1, ?)`, id, 30000+id)
	}
	e.exec(`UPDATE forward SET mode = 'nftables' WHERE id = 33`)
	e.exec(`UPDATE forward SET status = 0 WHERE id = 34`)
	return e, commands
}

func rbEnvEdges() map[[2]int64]float64 {
	return map[[2]int64]float64{{1, 2}: 10, {1, 3}: 30, {2, 4}: 100, {2, 5}: 120, {3, 4}: 20, {3, 5}: 25}
}

func rbEnvDests() map[string]map[int64]float64 {
	// a: exit 4 is near; b: exit 5 is near (4 → b 90 ms).
	return map[string]map[int64]float64{"a.example:443": {4: 5, 5: 60}, "b.example:443": {4: 90, 5: 10}}
}

type rbRoundInfo struct {
	plans  []pathLatencyPlan
	pairs  int
	probes int
}

// rbEnvRound runs a full round's decision part (plan 165 then plan 180) on
// real plans with these latencies (missing = failed).
func rbEnvRound(t *testing.T, e *flowTestEnv, edges map[[2]int64]float64, dests map[string]map[int64]float64, at time.Time) rbRoundInfo {
	t.Helper()
	plans, pairs, _, err := e.h.planPathLatencyRound()
	if err != nil {
		t.Fatal(err)
	}
	probes := e.h.attachRuleBestProbes(plans, pairs, at)
	results := rbResults(plans, edges, dests)
	for key := range pairs {
		if _, ok := results[key]; !ok {
			t.Fatalf("pair %+v without a result (planner and test disagree)", key)
		}
	}
	guard := newBestPathPushGuard(time.Now)
	e.h.runBestPathRound(plans, results, guard, at)
	e.h.runRuleBestRound(plans, results, guard, at)
	return rbRoundInfo{plans: plans, pairs: len(pairs), probes: probes}
}

func rbDestOrderOf(t *testing.T, data interface{}) map[string][]string {
	t.Helper()
	raw, _ := json.Marshal(data)
	var payload map[string]interface{}
	_ = json.Unmarshal(raw, &payload)
	if inner, ok := payload["data"].(map[string]interface{}); ok {
		payload = inner
	}
	hop := payload["hops"].([]interface{})[0].(map[string]interface{})
	md, ok := hop["metadata"].(map[string]interface{})
	if !ok {
		return nil
	}
	out := map[string][]string{}
	for k, v := range md[destOrderMetaKey].(map[string]interface{}) {
		for _, a := range v.([]interface{}) {
			out[k] = append(out[k], a.(string))
		}
	}
	return out
}

func rbChainCommands(cmds []redeployCommand) []redeployCommand {
	var out []redeployCommand
	for _, c := range cmds {
		if c.method == "UpdateChains" || c.method == "AddChains" {
			out = append(out, c)
		}
	}
	return out
}

func TestRuleBestRuntimeProbesPushesAndRedeploy(t *testing.T) {
	e, commands := newRuleBestEnv(t)
	now := time.Unix(1_800_000_000, 0)

	info := rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now)
	// 6 edges + one probe per distinct (exit, target): exits {4,5} × {a, b}.
	// nftables rule 33 and paused rule 34 add nothing.
	if info.probes != 4 || info.pairs != 10 {
		t.Fatalf("probes=%d pairs=%d, want 4 and 10", info.probes, info.pairs)
	}
	if rb := info.plans[0].RuleBest; rb == nil || len(rb.Dests) != 2 || len(rb.Forwards[31]) != 2 || len(rb.Forwards[32]) != 1 || rb.Forwards[33] != nil {
		t.Fatalf("plan destinations %+v", info.plans[0].RuleBest)
	}
	if n := len(commands.snapshot()); n != 0 {
		t.Fatalf("round 1 pushed %+v", commands.snapshot())
	}

	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(time.Minute))
	byNode := map[int64][]redeployCommand{}
	for _, c := range commands.snapshot() {
		byNode[c.nodeID] = append(byNode[c.nodeID], c)
	}
	// Plan 165 moves the entry to 中转 3 (1 push, no destOrder: both targets
	// agree with it); plan 180 gives 中转 2 and 3 an entry for b only.
	if len(byNode[1]) != 1 || rbDestOrderOf(t, byNode[1][0].data) != nil {
		t.Fatalf("entry pushes %+v", byNode[1])
	}
	want := map[string][]string{"b.example:443": {"192.0.2.5:1305", "192.0.2.4:1304"}}
	for _, owner := range []int64{2, 3} {
		if len(byNode[owner]) != 1 || byNode[owner][0].method != "UpdateChains" {
			t.Fatalf("owner %d pushes %+v", owner, byNode[owner])
		}
		if got := rbDestOrderOf(t, byNode[owner][0].data); !reflect.DeepEqual(got, want) {
			t.Fatalf("owner %d destOrder %v, want %v", owner, got, want)
		}
		// The hop order itself stays the tunnel's (exit 4 first).
		if addrs, strategy := chainOrder(t, byNode[owner][0].data); addrs[0] != "192.0.2.4:1304" || strategy != "fifo" {
			t.Fatalf("owner %d hop %v %s", owner, addrs, strategy)
		}
	}
	if len(byNode[4])+len(byNode[5]) != 0 {
		t.Fatalf("exits got pushes")
	}

	// Unchanged round: nothing.
	mark := len(commands.snapshot())
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(2*time.Minute))
	if after := commandsSince(commands, mark); len(after) != 0 {
		t.Fatalf("unchanged round pushed %+v", after)
	}

	// Any redeploy of an owner keeps its destOrder (decisions in every build).
	mark = len(commands.snapshot())
	if err := e.h.redeployTunnelRuntimeOnNode(20, 3); err != nil {
		t.Fatal(err)
	}
	after := rbChainCommands(commandsSince(commands, mark))
	if len(after) != 1 || !reflect.DeepEqual(rbDestOrderOf(t, after[0].data), want) {
		t.Fatalf("redeploy lost destOrder: %+v", after)
	}

	// Old agent on owner 3 (no destOrder capability): its chain is the
	// tunnel order only, and the next round keeps it that way.
	e.h.destOrderCapableFn = func(id int64) bool { return id != 3 }
	mark = len(commands.snapshot())
	if err := e.h.redeployTunnelRuntimeOnNode(20, 3); err != nil {
		t.Fatal(err)
	}
	after = rbChainCommands(commandsSince(commands, mark))
	if len(after) != 1 || rbDestOrderOf(t, after[0].data) != nil {
		t.Fatalf("old agent got destOrder: %+v", after)
	}
	mark = len(commands.snapshot())
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(3*time.Minute))
	for _, c := range commandsSince(commands, mark) {
		if c.nodeID == 3 {
			t.Fatalf("old agent got a rule-best push: %+v", c)
		}
	}
	e.h.destOrderCapableFn = func(int64) bool { return true }

	// Target b dies everywhere: entries for b removed once, no switch, 5 min back-off.
	mark = len(commands.snapshot())
	dead := rbEnvDests()
	delete(dead, "b.example:443")
	rbEnvRound(t, e, rbEnvEdges(), dead, now.Add(4*time.Minute))
	cleanup := commandsSince(commands, mark)
	if len(cleanup) != 1 || cleanup[0].nodeID != 2 || rbDestOrderOf(t, cleanup[0].data) != nil {
		t.Fatalf("dead target cleanup %+v", cleanup)
	}
	info = rbEnvRound(t, e, rbEnvEdges(), dead, now.Add(5*time.Minute))
	if info.probes != 2 {
		t.Fatalf("backed-off target still probed: %d probes", info.probes)
	}
}

func TestRuleBestSwitchOffViaTunnelUpdateRemovesDestOrder(t *testing.T) {
	e, commands := newRuleBestEnv(t)
	now := time.Unix(1_800_000_000, 0)
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now)
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(time.Minute))
	mark := len(commands.snapshot())
	body := map[string]interface{}{
		"id": 20, "name": "best-tunnel", "type": 2, "status": 1, "ruleBest": 0,
		"inNodeId":   []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
		"chainNodes": [][]map[string]interface{}{{{"nodeId": 2, "port": 1302, "protocol": "tls", "strategy": "best"}, {"nodeId": 3, "port": 1303, "protocol": "tls", "strategy": "best"}}},
		"outNodeId":  []map[string]interface{}{{"nodeId": 4, "port": 1304, "protocol": "tls", "strategy": "best"}, {"nodeId": 5, "port": 1305, "protocol": "tls", "strategy": "best"}},
	}
	if code, msg := redeployMutationRequest(t, e.h.tunnelUpdate, body); code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	if v := mustQueryInt(t, e.r, `SELECT rule_best FROM tunnel WHERE id = 20`); v != 0 {
		t.Fatalf("rule_best = %d", v)
	}
	chains := 0
	for _, c := range commandsSince(commands, mark) {
		if c.method == "UpdateChains" || c.method == "AddChains" {
			chains++
			if rbDestOrderOf(t, c.data) != nil {
				t.Fatalf("switch off still pushed destOrder to %d: %+v", c.nodeID, c.data)
			}
		}
	}
	if chains < 3 {
		t.Fatalf("update redeployed %d chains", chains)
	}
	// Off: no exit→target probes, no decisions, no pushes from rule-best.
	mark = len(commands.snapshot())
	info := rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(2*time.Minute))
	if info.probes != 0 || info.plans[0].RuleBest != nil {
		t.Fatalf("switch off still probes: %d", info.probes)
	}
	if states, _, _, _ := e.h.ruleBest.size(); states != 0 {
		t.Fatalf("switch off kept %d states", states)
	}
	for _, c := range commandsSince(commands, mark) {
		if rbDestOrderOf(t, c.data) != nil {
			t.Fatalf("off round pushed destOrder %+v", c)
		}
	}
	// Update without the field keeps it off; ruleBest 1 turns it back on.
	delete(body, "ruleBest")
	if code, msg := redeployMutationRequest(t, e.h.tunnelUpdate, body); code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	if v := mustQueryInt(t, e.r, `SELECT rule_best FROM tunnel WHERE id = 20`); v != 0 {
		t.Fatalf("absent field changed rule_best to %d", v)
	}
	body["ruleBest"] = 1
	if code, msg := redeployMutationRequest(t, e.h.tunnelUpdate, body); code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	if v := mustQueryInt(t, e.r, `SELECT rule_best FROM tunnel WHERE id = 20`); v != 1 {
		t.Fatalf("rule_best = %d after turning on", v)
	}
}

func TestRuleBestCreateDefaultsOnAndStoresOff(t *testing.T) {
	e, _ := newBestPathEnv(t)
	e.h.chainSwapCapableFn = nil
	for name, extra := range map[string]interface{}{"rb-default": nil, "rb-off": 0, "rb-on": true} {
		body := map[string]interface{}{
			"name": name, "type": 2, "status": 1,
			"inNodeId":  []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
			"outNodeId": []map[string]interface{}{{"nodeId": 4, "port": 1404, "protocol": "tls"}, {"nodeId": 5, "port": 1405, "protocol": "tls"}},
		}
		if extra != nil {
			body["ruleBest"] = extra
		}
		if code, msg := redeployMutationRequest(t, e.h.tunnelCreate, body); code != 0 {
			t.Fatalf("create %s: %d %s", name, code, msg)
		}
		want := 1
		if name == "rb-off" {
			want = 0
		}
		if v := mustQueryInt(t, e.r, `SELECT rule_best FROM tunnel WHERE name = ?`, name); v != want {
			t.Fatalf("%s: rule_best = %d, want %d", name, v, want)
		}
	}
	// Existing rows (and rows written by an older panel) default to on.
	if v := mustQueryInt(t, e.r, `SELECT rule_best FROM tunnel WHERE id = 20`); v != 1 {
		t.Fatalf("existing tunnel rule_best = %d", v)
	}
	// The admin list carries the switch.
	items, err := e.r.ListTunnels()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if _, ok := it["ruleBest"]; !ok {
			t.Fatalf("tunnel list item without ruleBest: %v", it["name"])
		}
	}
}

func rbItems(ids ...int64) []map[string]interface{} {
	addrs := map[int64]string{31: "a.example:443,b.example:443", 32: "b.example:443", 33: "c.example:443"}
	modes := map[int64]string{33: "nftables"}
	out := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		mode := modes[id]
		if mode == "" {
			mode = "gost"
		}
		out = append(out, map[string]interface{}{"id": id, "tunnelId": int64(20), "remoteAddr": addrs[id], "mode": mode})
	}
	return out
}

func TestRuleBestRuleListPick(t *testing.T) {
	e, _ := newRuleBestEnv(t)
	now := time.Now()
	// Waiting: no round yet ⇒ no view ⇒ no line.
	items := rbItems(31)
	e.h.attachRuleBestPicks(items, map[int64][]int64{31: {1}}, now)
	if _, ok := items[0]["bestPick"]; ok {
		t.Fatalf("pick before any round: %v", items[0]["bestPick"])
	}
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(-time.Minute))
	items = rbItems(31, 32, 33)
	e.h.attachRuleBestPicks(items, map[int64][]int64{31: {1}, 32: {1}, 33: {1}}, now)
	// First round: nothing switched yet (DB order: 中转 2 → 出口 4), shown
	// with the whole path to the target: 10 + 100 + 90.
	p := items[1]["bestPick"].(*rulePick)
	if p.Status != bestPathStatusApplied || p.Summary != "中转 node-2 → 出口 node-4" || !p.WithTarget || p.MinMs != 200 {
		t.Fatalf("rule 32 round 1 %+v", p)
	}
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now)
	items = rbItems(31, 32, 33)
	e.h.attachRuleBestPicks(items, map[int64][]int64{31: {1}, 32: {1}, 33: {1}}, now)
	// Rule 31: two targets, different exits ⇒ 「2 个节点」 + per-target items.
	p = items[0]["bestPick"].(*rulePick)
	if p.Status != bestPathStatusApplied || !p.Multi || p.Summary != "中转 node-3 → 出口 2 个节点" || p.MinMs != 55 || p.MaxMs != 65 || !p.WithTarget || len(p.Items) != 2 {
		t.Fatalf("rule 31 %+v", p)
	}
	byTarget := map[string]rulePickItem{}
	for _, it := range p.Items {
		byTarget[it.Target] = it
	}
	if a := byTarget["a.example:443"]; a.Ms != 55 || a.Hops[1].Name != "node-4" || !a.WithTarget {
		t.Fatalf("rule 31 target a %+v", a)
	}
	if b := byTarget["b.example:443"]; b.Ms != 65 || b.Hops[1].Name != "node-5" {
		t.Fatalf("rule 31 target b %+v", b)
	}
	// Rule 32: single line, no popover items.
	p = items[1]["bestPick"].(*rulePick)
	if p.Multi || p.Summary != "中转 node-3 → 出口 node-5" || p.MinMs != 65 || len(p.Items) != 0 {
		t.Fatalf("rule 32 %+v", p)
	}
	// nftables rule: no line.
	if _, ok := items[2]["bestPick"]; ok {
		t.Fatalf("nft rule got a pick line")
	}
	// Users get names only: no node ids / addresses in the payload.
	raw, _ := json.Marshal(items)
	if s := string(raw); strings.Contains(s, "192.0.2.") || strings.Contains(s, "nodeId") || strings.Contains(s, "ownerNodeId") {
		t.Fatalf("pick payload leaks ids/addresses: %s", s)
	}
	// Owner 3 runs an old agent: its 出口 pick is the tunnel decision (exit 4)
	// and the line drops 含落地 (ms entry→exit only).
	e.h.destOrderCapableFn = func(id int64) bool { return id != 3 }
	items = rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {1}}, now)
	if p = items[0]["bestPick"].(*rulePick); p.Summary != "中转 node-3 → 出口 node-4" || p.WithTarget || p.MinMs != 50 {
		t.Fatalf("old-agent owner pick %+v", p)
	}
	// Owner offline ⇒ 节点离线.
	e.h.destOrderCapableFn = func(int64) bool { return true }
	e.h.nodeOnlineFn = func(id int64) bool { return id != 1 }
	items = rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {1}}, now)
	if p = items[0]["bestPick"].(*rulePick); p.Status != bestPathStatusOffline || p.Summary != "节点离线" {
		t.Fatalf("offline pick %+v", p)
	}
	e.h.nodeOnlineFn = func(int64) bool { return true }
	// Stale data (> 150 s) ⇒ 等待探测.
	items = rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {1}}, now.Add(10*time.Minute))
	if p = items[0]["bestPick"].(*rulePick); p.Status != bestPathStatusWaiting {
		t.Fatalf("stale pick %+v", p)
	}
	// Switch off: the line stays, with the tunnel decision (admin card numbers).
	e.exec(`UPDATE tunnel SET rule_best = 0 WHERE id = 20`)
	e.h.ruleBest.forgetTunnel(20)
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now)
	items = rbItems(31)
	e.h.attachRuleBestPicks(items, map[int64][]int64{31: {1}}, now)
	if p = items[0]["bestPick"].(*rulePick); p.Multi || p.Summary != "中转 node-3 → 出口 node-4" || p.WithTarget || p.MinMs != 50 {
		t.Fatalf("switch-off pick %+v", p)
	}
	// Compare with the admin tunnel card: entry layer 中转 node-3 · 50 ms.
	tunnels, _ := e.r.ListTunnels()
	e.h.attachBestPathStates(tunnels)
	for _, tn := range tunnels {
		if asInt64(tn["id"], 0) != 20 {
			continue
		}
		st := tn["bestPathState"].(*bestPathDisplayState)
		if st.Layers[0].Summary != "node-3" || st.Layers[0].MinMs != 50 || st.Layers[1].Summary != "node-4" {
			t.Fatalf("admin card %+v", st.Layers)
		}
	}
}

// Entry-bound rule (plan 152): only the rule's entries are shown.
func TestRuleBestPickMultiEntryAndBoundEntry(t *testing.T) {
	e, _ := newRuleBestEnv(t)
	e.addNode(6, "n6-secret")
	e.exec(`UPDATE node SET name = 'node-6', server_ip = '192.0.2.6', server_ip_v4 = '192.0.2.6', tcp_listen_addr = '0.0.0.0', udp_listen_addr = '0.0.0.0' WHERE id = 6`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(20, '1', 6, NULL, 'round', 2, 'tls')`)
	edges := rbEnvEdges()
	edges[[2]int64{6, 2}], edges[[2]int64{6, 3}] = 5, 200 // entry 6 prefers 中转 2
	now := time.Now()
	rbEnvRound(t, e, edges, rbEnvDests(), now.Add(-time.Minute))
	rbEnvRound(t, e, edges, rbEnvDests(), now)
	items := rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {1, 6}}, now)
	p := items[0]["bestPick"].(*rulePick)
	if !p.Multi || p.Layers[0].Count != 2 || p.Layers[0].Name != "2 个节点" || len(p.Items) != 2 || p.Items[0].Entry == "" {
		t.Fatalf("multi-entry pick %+v", p)
	}
	// Bound to entry 6 only.
	items = rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {6}}, now)
	p = items[0]["bestPick"].(*rulePick)
	if p.Multi || !strings.HasPrefix(p.Summary, "中转 node-2") {
		t.Fatalf("entry-bound pick %+v", p)
	}
}

// Efficiency: a tunnel whose exit is one fixed node (最优 only on 中转) gets
// no exit→target probes — they could not change any pick.
func TestRuleBestNoProbesWithoutBestExitLayer(t *testing.T) {
	e, _ := newRuleBestEnv(t)
	e.addTunnel(21, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2, name = 'fixed-exit' WHERE id = 21`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(21, '1', 1, NULL, 'round', 1, 'tls'),
		      (21, '2', 2, 1312, 'best', 1, 'tls'),
		      (21, '2', 3, 1313, 'best', 1, 'tls'),
		      (21, '3', 4, 1314, 'round', 1, 'tls')`)
	e.addForward(41, 2, 21)
	e.exec(`UPDATE forward SET remote_addr = 'z.example:443', mode = 'gost' WHERE id = 41`)
	info := rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), time.Now())
	for _, plan := range info.plans {
		if plan.TunnelID == 21 && plan.RuleBest != nil {
			t.Fatalf("fixed-exit tunnel got rule-best probes: %+v", plan.RuleBest)
		}
	}
	if info.probes != 4 { // only tunnel 20's
		t.Fatalf("probes = %d", info.probes)
	}
}
