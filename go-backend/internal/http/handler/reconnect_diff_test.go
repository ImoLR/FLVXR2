package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/ws"
)

// fakeRuntimeAgent is a stateful agent for the plan 138 reconnect tests: it
// keeps services / chains / limiters / nftables rule sets and, like a plan 138
// agent, the revision of every object it applied in its current "process".
type fakeRuntimeAgent struct {
	mu       sync.Mutex
	capable  bool // reports revisions (runtimeRevs=1 agent)
	boot     string
	objects  map[int64]map[string]bool   // node -> "<kind>:<name>"
	revs     map[int64]map[string]string // node -> "<kind>:<name>" -> rev
	commands []redeployCommand
}

func newFakeRuntimeAgent() *fakeRuntimeAgent {
	return &fakeRuntimeAgent{capable: true, boot: "boot-1", objects: map[int64]map[string]bool{}, revs: map[int64]map[string]string{}}
}

func (a *fakeRuntimeAgent) node(id int64) (map[string]bool, map[string]string) {
	if a.objects[id] == nil {
		a.objects[id] = map[string]bool{}
		a.revs[id] = map[string]string{}
	}
	return a.objects[id], a.revs[id]
}

func genericOf(data interface{}) interface{} {
	raw, _ := json.Marshal(data)
	var out interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (a *fakeRuntimeAgent) send(nodeID int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands = append(a.commands, redeployCommand{nodeID: nodeID, method: method, data: data})
	objs, revs := a.node(nodeID)
	set := func(expected map[string]string) {
		for key, rev := range expected {
			objs[key] = true
			if a.capable {
				revs[key] = rev
			}
		}
	}
	drop := func(kind string, names ...string) {
		for _, name := range names {
			delete(objs, kind+":"+name)
			delete(revs, kind+":"+name)
		}
	}
	g, _ := genericOf(data).(map[string]interface{})
	str := func(key string) string { s, _ := g[key].(string); return s }
	switch method {
	case "ListServices":
		inv := map[string]interface{}{"hops": []string{}, "wgPaths": []string{}, "wgForwards": []string{}}
		lists := map[string][]string{"service": {}, "chain": {}, "limiter": {}, "nft": {}}
		for key := range objs {
			kind, name, _ := strings.Cut(key, ":")
			lists[kind] = append(lists[kind], name)
		}
		for kind, field := range map[string]string{"service": "services", "chain": "chains", "limiter": "limiters", "nft": "nftables"} {
			sort.Strings(lists[kind])
			inv[field] = lists[kind]
		}
		if a.capable {
			r := map[string]interface{}{}
			for k, v := range revs {
				r[k] = v
			}
			inv["revs"], inv["boot"] = r, a.boot
		}
		return ws.CommandResult{Success: true, Data: inv}, nil
	case "AddService":
		expected := ws.ExpectedRuntimeRevs(method, data)
		for key := range expected {
			if objs[key] {
				delete(revs, key)
				return ws.CommandResult{}, fmt.Errorf("service %s already exists", strings.TrimPrefix(key, "service:"))
			}
		}
		set(expected)
	case "UpdateService", "AddChains", "UpdateChains", "UpdateLimiters", "AddNftablesRules", "UpdateNftablesRules":
		set(ws.ExpectedRuntimeRevs(method, data))
	case "AddLimiters":
		expected := ws.ExpectedRuntimeRevs(method, data)
		for key := range expected {
			if objs[key] {
				delete(revs, key)
				return ws.CommandResult{}, fmt.Errorf("limiter %s already exists", strings.TrimPrefix(key, "limiter:"))
			}
		}
		set(expected)
	case "DeleteService", "PauseService", "ResumeService":
		raw, _ := g["services"].([]interface{})
		for _, v := range raw {
			name, _ := v.(string)
			if method == "DeleteService" {
				drop("service", name)
			} else {
				delete(revs, "service:"+name)
			}
		}
	case "DeleteChains":
		if !objs["chain:"+str("chain")] {
			return ws.CommandResult{}, errors.New("chain not found")
		}
		drop("chain", str("chain"))
	case "DeleteLimiters":
		drop("limiter", str("limiter"))
	case "DeleteNftablesRules":
		ids, _ := g["forward_ids"].([]interface{})
		for _, v := range ids {
			if f, ok := v.(float64); ok {
				drop("nft", fmt.Sprintf("%d_nft", int64(f)))
			}
		}
	}
	return ws.CommandResult{Success: true}, nil
}

// restart: a new agent process loads its services from gost.json but has no
// revisions; nftables rules are cleared by the new manager.
func (a *fakeRuntimeAgent) restart(nodeID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	objs, _ := a.node(nodeID)
	for key := range objs {
		if strings.HasPrefix(key, "nft:") {
			delete(objs, key)
		}
	}
	a.revs[nodeID] = map[string]string{}
	a.boot += "-restarted"
}

func (a *fakeRuntimeAgent) reset() {
	a.mu.Lock()
	a.commands = nil
	a.mu.Unlock()
}

// mutations returns the state-changing commands sent to a node ("Method name").
func (a *fakeRuntimeAgent) mutations(nodeID int64) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, c := range a.commands {
		if c.nodeID != nodeID || c.method == "ListServices" {
			continue
		}
		out = append(out, c.method+" "+commandObjectNames(c.method, c.data))
	}
	return out
}

func (a *fakeRuntimeAgent) count(nodeID int64, method string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.commands {
		if c.nodeID == nodeID && c.method == method {
			n++
		}
	}
	return n
}

func (a *fakeRuntimeAgent) has(nodeID int64, key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.objects[nodeID][key]
}

func commandObjectNames(method string, data interface{}) string {
	var names []string
	for key := range ws.ExpectedRuntimeRevs(method, data) {
		names = append(names, key)
	}
	g := genericOf(data)
	if m, ok := g.(map[string]interface{}); ok {
		for _, k := range []string{"chain", "limiter"} {
			if s, ok := m[k].(string); ok {
				names = append(names, s)
			}
		}
		if list, ok := m["services"].([]interface{}); ok {
			for _, v := range list {
				names = append(names, fmt.Sprint(v))
			}
		}
		if ids, ok := m["forward_ids"].([]interface{}); ok {
			for _, v := range ids {
				names = append(names, fmt.Sprintf("nft %v", v))
			}
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// newReconnectDiffEnv: tunnel 10 = entry 1 → hop 2 → exits 3/4 (the redeploy
// env), tunnel 11 = entry 1 → hop 2 → exit 4, a gost forward 100 and an
// nftables forward 101 on entry 1, every agent reporting revisions.
func newReconnectDiffEnv(t *testing.T) (*flowTestEnv, *fakeRuntimeAgent) {
	t.Helper()
	e, _ := newTunnelRedeployEnv(t)
	e.addTunnel(11, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2, name = 'second-tunnel' WHERE id = 11`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(11, '1', 1, NULL, 'round', 1, 'tls'),
		      (11, '2', 2, 1302, 'round', 1, 'tls'),
		      (11, '3', 4, 1304, 'round', 1, 'tls')`)
	e.addUser(7)
	e.addUserTunnel(70, 7, 10)
	e.addForward(100, 7, 10)
	e.exec(`UPDATE forward SET mode = 'gost', remote_addr = '1.1.1.1:443' WHERE id = 100`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(100, 1, 1501)`)
	e.addUserTunnel(71, 7, 11)
	e.addForward(101, 7, 11)
	e.exec(`UPDATE forward SET mode = 'nftables', remote_addr = '9.9.9.9:53' WHERE id = 101`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(101, 1, 1601)`)
	agent := newFakeRuntimeAgent()
	e.h.nodeCommandSender = agent.send
	e.h.runtimeRevsCapable = func(int64) bool { return agent.capable }
	return e, agent
}

// deployAll mimics the first deploy after an agent (re)start on every node.
// The pushes happened "long ago" for the following reconnects.
func deployAll(e *flowTestEnv, agent *fakeRuntimeAgent) {
	for _, id := range []int64{1, 2, 3, 4} {
		e.h.redeployNodeRuntime(id)
	}
	agent.reset()
	forgetPushes(e)
}

func forgetPushes(e *flowTestEnv) {
	e.h.recentPushes.mu.Lock()
	e.h.recentPushes.nodes = nil
	e.h.recentPushes.mu.Unlock()
}

func TestReconnectUnchangedSendsNothing(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	for _, id := range []int64{1, 2, 3, 4} {
		if !agent.has(id, "service:10_tls") && id != 1 && id != 3 && id != 4 {
			t.Fatalf("node %d not deployed", id)
		}
	}
	if !agent.has(1, "service:100_7_70_tcp") || !agent.has(1, "nft:101_nft") || !agent.has(1, "chain:chains_10") {
		t.Fatalf("entry not deployed: %+v", agent.objects[1])
	}
	for _, id := range []int64{1, 2, 3, 4} {
		e.h.redeployNodeRuntime(id)
		if m := agent.mutations(id); len(m) != 0 {
			t.Fatalf("unchanged reconnect of node %d sent %v", id, m)
		}
		if agent.count(id, "ListServices") != 1 {
			t.Fatalf("node %d: want exactly one ListServices, got %d", id, agent.count(id, "ListServices"))
		}
	}
}

func TestReconnectTunnelEditedWhileOfflineUpdatesOnlyThatTunnel(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	// Tunnel 11's exit port changed while hop 2 was away.
	e.exec(`UPDATE chain_tunnel SET port = 1314 WHERE tunnel_id = 11 AND node_id = 4`)
	e.h.redeployNodeRuntime(2)
	m := strings.Join(agent.mutations(2), " | ")
	if !strings.Contains(m, "chains_11") || strings.Contains(m, "chains_10") || strings.Contains(m, "10_tls") {
		t.Fatalf("hop reconnect must replace only tunnel 11: %s", m)
	}
	if agent.count(2, "DeleteService") != 0 && !strings.Contains(m, "11_tls") {
		t.Fatalf("unexpected service delete: %s", m)
	}
	// A second reconnect is clean again.
	agent.reset()
	e.h.redeployNodeRuntime(2)
	if m := agent.mutations(2); len(m) != 0 {
		t.Fatalf("second reconnect after the edit sent %v", m)
	}
}

func TestReconnectForwardEditedWhileOfflineUpdatesOnlyThatForward(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	e.exec(`UPDATE forward SET remote_addr = '8.8.8.8:443' WHERE id = 100`)
	e.h.redeployNodeRuntime(1)
	m := agent.mutations(1)
	if len(m) != 1 || !strings.HasPrefix(m[0], "UpdateService ") || !strings.Contains(m[0], "100_7_70_tcp") {
		t.Fatalf("want one UpdateService of forward 100, got %v", m)
	}
}

func TestReconnectRemovesTunnelDeletedWhileOffline(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	e.exec(`DELETE FROM forward_port WHERE forward_id = 101`)
	e.exec(`DELETE FROM forward WHERE id = 101`)
	e.exec(`DELETE FROM user_tunnel WHERE id = 71`)
	e.exec(`DELETE FROM chain_tunnel WHERE tunnel_id = 11`)
	e.exec(`DELETE FROM tunnel WHERE id = 11`)
	e.h.redeployNodeRuntime(2)
	if agent.has(2, "service:11_tls") || agent.has(2, "chain:chains_11") {
		t.Fatalf("deleted tunnel left runtime on the hop: %+v", agent.objects[2])
	}
	if !agent.has(2, "service:10_tls") || agent.count(2, "AddService") != 0 {
		t.Fatalf("other tunnel touched: %v", agent.mutations(2))
	}
	e.h.redeployNodeRuntime(1)
	if agent.has(1, "chain:chains_11") || agent.has(1, "nft:101_nft") {
		t.Fatalf("deleted tunnel/forward left runtime on the entry: %+v", agent.objects[1])
	}
	if !agent.has(1, "chain:chains_10") || !agent.has(1, "service:100_7_70_tcp") {
		t.Fatalf("remaining runtime removed: %+v", agent.objects[1])
	}
}

func TestReconnectRemovesForwardDeletedWhileOffline(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	e.exec(`DELETE FROM forward_port WHERE forward_id = 100`)
	e.exec(`DELETE FROM forward WHERE id = 100`)
	e.h.redeployNodeRuntime(1)
	if agent.has(1, "service:100_7_70_tcp") || agent.has(1, "service:100_7_70_udp") {
		t.Fatalf("deleted forward still runs: %+v", agent.objects[1])
	}
	for _, m := range agent.mutations(1) {
		if !strings.HasPrefix(m, "DeleteService ") {
			t.Fatalf("unexpected command %s", m)
		}
	}
}

func TestReconnectAfterAgentRestartDeploysEverything(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	agent.restart(1)
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "AddChains") != 2 || agent.count(1, "UpdateService") != 1 || agent.count(1, "AddNftablesRules") != 1 {
		t.Fatalf("restarted agent not fully redeployed: %v", agent.mutations(1))
	}
	agent.reset()
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("reconnect after the full deploy sent %v", m)
	}
}

func TestReconnectOldAgentGetsFullDeploy(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	agent.capable = false
	deployAll(e, agent)
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "ListServices") != 0 {
		t.Fatal("an agent without the capability must not be asked for its inventory")
	}
	if agent.count(1, "DeleteChains") != 2 || agent.count(1, "AddChains") != 2 || agent.count(1, "UpdateService") != 1 || agent.count(1, "AddNftablesRules") != 1 {
		t.Fatalf("old agent not fully redeployed: %v", agent.mutations(1))
	}
	// Capable handshake but an inventory without revisions (older agent behind
	// the flag, or a failed listing): full deploy as well.
	agent.reset()
	e.h.runtimeRevsCapable = func(int64) bool { return true }
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "AddChains") != 2 {
		t.Fatalf("inventory without revisions must redeploy fully: %v", agent.mutations(1))
	}
}

func TestReconnectSpeedLimiterChangeResendsLimiterAndService(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	e.exec(`UPDATE forward SET speed_limit_enabled = 1, speed_limit = 80 WHERE id = 100`)
	deployAll(e, agent)
	if !agent.has(1, "limiter:forward_100_speed") {
		t.Fatalf("limiter not deployed: %+v", agent.objects[1])
	}
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("unchanged limiter reconnect sent %v", m)
	}
	e.exec(`UPDATE forward SET speed_limit = 160 WHERE id = 100`)
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "AddLimiters") != 1 || agent.count(1, "UpdateService") != 1 {
		t.Fatalf("speed change not pushed: %v", agent.mutations(1))
	}
}

func TestReconnectNftablesForwardUnchangedAndChanged(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "DeleteNftablesRules")+agent.count(1, "AddNftablesRules") != 0 {
		t.Fatalf("unchanged nft forward re-pushed: %v", agent.mutations(1))
	}
	e.exec(`UPDATE forward SET remote_addr = '9.9.9.10:53' WHERE id = 101`)
	e.h.redeployNodeRuntime(1)
	if agent.count(1, "DeleteNftablesRules") != 1 || agent.count(1, "AddNftablesRules") != 1 {
		t.Fatalf("changed nft forward not re-pushed: %v", agent.mutations(1))
	}
	for _, m := range agent.mutations(1) {
		if strings.Contains(m, "Service") || strings.Contains(m, "Chains") {
			t.Fatalf("nft change touched other runtime: %v", agent.mutations(1))
		}
	}
}

func TestReconnectOrphanFloodIsOnlyReported(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	agent.mu.Lock()
	objs, _ := agent.node(3)
	for i := 0; i < maxReconnectOrphans+1; i++ {
		objs[fmt.Sprintf("service:%d_tls", 900+i)] = true
	}
	agent.mu.Unlock()
	e.h.redeployNodeRuntime(3)
	if agent.count(3, "DeleteService") != 0 {
		t.Fatalf("an orphan flood must not be deleted automatically: %v", agent.mutations(3))
	}
	// A small number is removed.
	agent.mu.Lock()
	for i := 0; i < maxReconnectOrphans+1; i++ {
		if i > 1 {
			delete(objs, fmt.Sprintf("service:%d_tls", 900+i))
		}
	}
	agent.mu.Unlock()
	e.h.redeployNodeRuntime(3)
	if agent.count(3, "DeleteService") != 2 || agent.has(3, "service:900_tls") || !agent.has(3, "service:10_tls") {
		t.Fatalf("2 orphans not removed: %v", agent.mutations(3))
	}
}

func TestReconnectExpectedRevisionsAreStable(t *testing.T) {
	e, _ := newReconnectDiffEnv(t)
	state, err := e.h.reconstructTunnelState(10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.h.buildTunnelRuntimePlan(state, 2)
	if err != nil {
		t.Fatal(err)
	}
	state2, _ := e.h.reconstructTunnelState(10)
	second, _ := e.h.buildTunnelRuntimePlan(state2, 2)
	a, b := first.expectedRevs(), second.expectedRevs()
	if len(a) != 2 || fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("revisions not stable: %v vs %v", a, b)
	}
}

// An object another operation pushed a moment ago may belong to a change that
// is not committed yet (tunnel create, rule update): a reconnect keeps it.
func TestReconnectKeepsRecentlyPushedOrphanCandidates(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent)
	inflight := []map[string]interface{}{{"name": "999_7_70_tcp", "addr": "0.0.0.0:1599", "handler": map[string]interface{}{"type": "tcp"}}}
	if _, err := e.h.sendNodeCommand(1, "AddService", inflight, true, false); err != nil {
		t.Fatal(err)
	}
	agent.reset()
	e.h.redeployNodeRuntime(1)
	if !agent.has(1, "service:999_7_70_tcp") || agent.count(1, "DeleteService") != 0 {
		t.Fatalf("in-flight object removed: %v", agent.mutations(1))
	}
	// Once the push is old, it is an orphan like any other.
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if agent.has(1, "service:999_7_70_tcp") {
		t.Fatalf("stale orphan kept: %v", agent.mutations(1))
	}
}

func TestRecentPushGuardOnlyCoversTheCurrentSession(t *testing.T) {
	var l recentPushLog
	t0 := time.Unix(1000, 0)
	svc := []map[string]interface{}{{"name": "5_tls", "addr": ":1"}}
	l.note(9, "AddService", svc, t0)
	if !l.inFlight(9, "service:5_tls", time.Time{}, t0.Add(time.Minute)) {
		t.Fatal("a push of this session a minute ago is in flight")
	}
	if l.inFlight(9, "service:5_tls", t0.Add(time.Second), t0.Add(time.Minute)) {
		t.Fatal("a push of an earlier session cannot be in flight")
	}
	if l.inFlight(9, "service:5_tls", time.Time{}, t0.Add(3*time.Minute)) {
		t.Fatal("old push treated as in flight")
	}
	l.note(9, "DeleteService", map[string]interface{}{"services": []string{"5_tls"}}, t0)
	if l.inFlight(9, "service:6_tls", time.Time{}, t0) {
		t.Fatal("unknown object")
	}
}
