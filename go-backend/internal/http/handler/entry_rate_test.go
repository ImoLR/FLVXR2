package handler

import (
	"fmt"
	"strings"
	"testing"

	"go-backend/internal/store/repo"
)

// Plan 168: entry limits in the agent's kernel tree.

func TestEntryRatePlanBuild(t *testing.T) {
	rows := []repo.EntryRateRuleRow{
		{ForwardID: 1, UserID: 7, Port: 1001},                                         // only the user's row
		{ForwardID: 2, UserID: 7, Port: 1002, SpeedLimitEnabled: true, SpeedLimit: 5}, // own 5 under the row
		{ForwardID: 3, UserID: 8, Port: 1003, LimitID: 4, LimitSpeed: 12},             // non-arena 限速规则 = per rule
		{ForwardID: 4, UserID: 8, Port: 1004},                                         // nothing: not filtered
		{ForwardID: 5, UserID: 9, Port: 1005, LimitID: 6, LimitSpeed: 30, LimitArena: true},
		{ForwardID: 6, UserID: 10, Port: 1006, LimitID: 6, LimitSpeed: 30, LimitArena: true}, // arena 6: users 9+10, no row → top
		{ForwardID: 7, UserID: 7, Port: 1007, LimitID: 11, LimitSpeed: 40, LimitArena: true}, // arena 11: only user 7 (row) → under u7
		{ForwardID: 8, UserID: 7, Port: 1008, LimitID: 12, LimitSpeed: 25, LimitArena: true},
		{ForwardID: 9, UserID: 8, Port: 1009, LimitID: 12, LimitSpeed: 25, LimitArena: true}, // arena 12: users 7 (row) + 8 → conflict
		{ForwardID: 10, UserID: 7, Port: 1001},                                               // duplicate port: skipped
		{ForwardID: 11, UserID: 7, Port: 1011, Mode: "wg_path", SpeedLimitEnabled: true, SpeedLimit: 3},
		{ForwardID: 12, UserID: 8, Port: 1012, SpeedLimitEnabled: true, SpeedLimit: 6, LimitID: 6, LimitSpeed: 30, LimitArena: true}, // dynamic wins over the arena
	}
	plan := buildEntryRatePlan(rows, map[int64]int{7: 20})
	classes := map[string]entryRateClassPayload{}
	for _, c := range plan.payload.Classes {
		classes[c.ID] = c
	}
	want := map[string]entryRateClassPayload{
		"u7":  {ID: "u7", Group: true, RateMbps: 20},
		"r2":  {ID: "r2", Parent: "u7", RateMbps: 5},
		"r3":  {ID: "r3", RateMbps: 12},
		"a6":  {ID: "a6", RateMbps: 30},
		"a11": {ID: "a11", Parent: "u7", RateMbps: 40},
		"r12": {ID: "r12", RateMbps: 6},
	}
	if len(classes) != len(want) {
		t.Fatalf("classes %v", plan.payload.Classes)
	}
	for id, c := range want {
		if classes[id] != c {
			t.Errorf("class %s = %+v, want %+v", id, classes[id], c)
		}
	}
	ports := map[int]string{}
	for _, p := range plan.payload.Ports {
		if !p.TCP || !p.UDP {
			t.Fatalf("port %d not tcp+udp", p.Port)
		}
		ports[p.Port] = p.Class
	}
	wantPorts := map[int]string{1001: "u7", 1002: "r2", 1003: "r3", 1005: "a6", 1006: "a6", 1007: "a11", 1008: "u7", 1012: "r12"}
	if len(ports) != len(wantPorts) {
		t.Fatalf("ports %v", ports)
	}
	for p, c := range wantPorts {
		if ports[p] != c {
			t.Errorf("port %d → %q, want %q", p, ports[p], c)
		}
	}
	if !plan.agentArena[8] || !plan.agentArena[9] || len(plan.agentArena) != 2 {
		t.Fatalf("agent arenas %v", plan.agentArena)
	}
	if plan.tcCovers(9) || !plan.tcCovers(3) || (*entryRatePlan)(nil).tcCovers(3) {
		t.Fatal("tcCovers")
	}
	if plan.rev == "" {
		t.Fatal("no revision")
	}
	if again := buildEntryRatePlan(rows, map[int64]int{7: 20}); again.rev != plan.rev {
		t.Fatal("revision not stable")
	}
	if empty := buildEntryRatePlan(nil, nil); empty.rev != "" || len(empty.payload.Classes) != 0 {
		t.Fatalf("empty plan %+v", empty)
	}
}

func lastEntryRates(a *fakeRuntimeAgent, nodeID int64) (map[string]interface{}, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.commands) - 1; i >= 0; i-- {
		c := a.commands[i]
		if c.nodeID == nodeID && c.method == "SetEntryRates" {
			g, _ := genericOf(c.data).(map[string]interface{})
			return g, true
		}
	}
	return nil, false
}

func entryClasses(g map[string]interface{}) map[string]string {
	out := map[string]string{}
	list, _ := g["classes"].([]interface{})
	for _, raw := range list {
		c, _ := raw.(map[string]interface{})
		id, _ := c["id"].(string)
		parent, _ := c["parent"].(string)
		out[id] = fmt.Sprintf("%s@%v", parent, c["rate_mbps"])
	}
	return out
}

// drainEntryRates runs a pending debounced flush now (deterministic tests).
func drainEntryRates(e *flowTestEnv) {
	d := &e.h.entryRateDirty
	d.mu.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	nodes := d.nodes
	d.nodes = nil
	d.mu.Unlock()
	for id := range nodes {
		_ = e.h.syncEntryRates(id)
	}
}

func newEntryRateEnv(t *testing.T) (*flowTestEnv, *fakeRuntimeAgent) {
	e, agent := newReconnectDiffEnv(t)
	e.h.userRateCapableFn = func(int64) bool { return true }
	e.h.entryRateTcFn = func(int64) bool { return true }
	t.Cleanup(func() { drainEntryRates(e) })
	deployAll(e, agent)
	drainEntryRates(e)
	agent.reset()
	return e, agent
}

func TestEntryRateTcNodeHotAndQuiet(t *testing.T) {
	e, agent := newEntryRateEnv(t)
	// No limits anywhere: nothing sent, a reconnect stays quiet.
	if _, sent := lastEntryRates(agent, 1); sent {
		t.Fatal("empty tree pushed")
	}
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("unchanged reconnect sent %v", m)
	}
	// The user's 速率: only the tree changes (no limiter, no service / rule push).
	setUserRate(t, e, 7, 50)
	m := strings.Join(agent.mutations(1), " | ")
	if !strings.Contains(m, "SetEntryRates") || strings.Contains(m, "Limiters") || strings.Contains(m, "UpdateService") || strings.Contains(m, "Nftables") {
		t.Fatalf("tc node rate set: %s", m)
	}
	g, _ := lastEntryRates(agent, 1)
	if c := entryClasses(g); len(c) != 1 || c["u7"] != "@50" {
		t.Fatalf("tree %v", g)
	}
	if ports, _ := g["ports"].([]interface{}); len(ports) != 2 {
		t.Fatalf("both rules (gost 1501 + nft 1601) must be in the user's class: %v", g["ports"])
	}
	// gost service: no limiter, no userLimiter; nft rule: no policer.
	agent.reset()
	f100, _ := e.h.getForwardRecord(100)
	f101, _ := e.h.getForwardRecord(101)
	if err := e.h.syncForwardServicesOnNode(f100, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.h.syncForwardServicesOnNode(f101, 1); err != nil {
		t.Fatal(err)
	}
	svc := lastServicePayload(agent, 1, "100_7_70_tcp")
	if svc == nil || svc["limiter"] != nil || userLimiterMeta(svc) != "" {
		t.Fatalf("tc node gost payload %v", svc)
	}
	if v, ok := lastNftSpeed(agent, 1, 101); !ok || v != 0 {
		t.Fatalf("tc node nft policer %v %v", v, ok)
	}
	// Plan 170: a stale rule speed (dynamic / 限速规则) is ignored — no
	// limiter, no rule class: the tree keeps only the user's class.
	agent.reset()
	e.exec(`INSERT INTO speed_limit(id, name, speed, status, arena_mode, created_time, updated_time) VALUES(5, 'old', 20, 1, 0, 0, 0)`)
	e.exec(`UPDATE forward SET speed_limit_enabled = 1, speed_limit = 10, speed_id = 5 WHERE id = 100`)
	f100, _ = e.h.getForwardRecord(100)
	if err := e.h.syncForwardServicesOnNode(f100, 1); err != nil {
		t.Fatal(err)
	}
	drainEntryRates(e)
	m = strings.Join(agent.mutations(1), " | ")
	if strings.Contains(m, "Limiters") || strings.Contains(m, "SetEntryRates") {
		t.Fatalf("stale rule speed pushed a limiter or changed the tree: %s", m)
	}
	if svc := lastServicePayload(agent, 1, "100_7_70_tcp"); svc == nil || svc["limiter"] != nil {
		t.Fatalf("stale rule speed referenced: %v", svc)
	}
	if plan := e.h.entryRatePlanFor(1); plan == nil || len(plan.agentArena) != 0 {
		t.Fatalf("plan %+v", plan)
	} else if c := entryClasses(genericOf(plan.payload).(map[string]interface{})); len(c) != 1 || c["u7"] != "@50" {
		t.Fatalf("tree with a stale rule speed %v", c)
	}
	// A rule edit (rule dialog → syncForwardServices) on a tc node whose agent
	// runs the same services already → no UpdateService (no restart).
	agent.reset()
	if _, err := e.h.syncForwardServicesWithWarnings(f100, "UpdateService", true); err != nil {
		t.Fatal(err)
	}
	drainEntryRates(e)
	if m := strings.Join(agent.mutations(1), " | "); strings.Contains(m, "UpdateService") || strings.Contains(m, "SetEntryRates") {
		t.Fatalf("unchanged rule edit restarted the service or pushed the tree: %s", m)
	}
	// Reconnect with everything in place: quiet; agent restart (tree file
	// kept): quiet for the tree.
	agent.reset()
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("reconnect sent %v", m)
	}
	agent.restart(1)
	agent.reset()
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if strings.Contains(strings.Join(agent.mutations(1), " "), "SetEntryRates") {
		t.Fatalf("restart re-pushed an unchanged tree: %v", agent.mutations(1))
	}
	// Row changed while away → the reconnect pushes the tree.
	if err := e.r.SetUserServerRate(7, 30); err != nil {
		t.Fatal(err)
	}
	agent.reset()
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	g, sent := lastEntryRates(agent, 1)
	if c := entryClasses(g); !sent || c["u7"] != "@30" {
		t.Fatalf("reconnect tree %v", g)
	}
	// Everything removed: an empty tree clears the agent.
	agent.reset()
	setUserRate(t, e, 7, 0)
	drainEntryRates(e)
	g, _ = lastEntryRates(agent, 1)
	if c := entryClasses(g); len(c) != 0 || agent.has(1, "entryrate:entry") {
		t.Fatalf("tree not cleared: %v", g)
	}
}

func TestEntryRateFallbackNodeUnchanged(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	e.h.userRateCapableFn = func(int64) bool { return true }
	e.h.entryRateTcFn = func(int64) bool { return false } // old agent / kernel without tc
	deployAll(e, agent)
	setUserRate(t, e, 7, 50)
	m := strings.Join(agent.mutations(1), " | ")
	if strings.Contains(m, "SetEntryRates") || !strings.Contains(m, "flvx_user_7_rate") {
		t.Fatalf("fallback node: %s", m)
	}
	f100, _ := e.h.getForwardRecord(100)
	_ = e.h.syncForwardServicesOnNode(f100, 1)
	if svc := lastServicePayload(agent, 1, "100_7_70_tcp"); userLimiterMeta(svc) != "flvx_user_7_rate" {
		t.Fatalf("fallback payload %v", svc)
	}
	if v, _ := lastNftSpeed(agent, 1, 101); v != 50 {
		t.Fatalf("fallback nft policer %v", v)
	}
}

// Plan 170: 竞技场 / 限速规则 are retired. An arena referenced by two users'
// rules (forward.speed_id and user_tunnel.speed_id) never reaches the tree or
// the agent, with or without a user 速率.
func TestEntryRateRetiredArenaIgnored(t *testing.T) {
	e, agent := newEntryRateEnv(t)
	e.exec(`INSERT INTO speed_limit(id, name, speed, status, arena_mode, created_time, updated_time) VALUES(5, 'arena', 30, 1, 1, 0, 0)`)
	e.addUser(8)
	e.addUserTunnel(80, 8, 10)
	e.addForward(102, 8, 10)
	e.exec(`UPDATE forward SET mode = 'gost', remote_addr = '1.1.1.1:443' WHERE id = 102`)
	e.exec(`UPDATE user_tunnel SET speed_id = 5 WHERE id = 80`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(102, 1, 1502)`)
	e.exec(`UPDATE forward SET speed_id = 5 WHERE id = 100`)
	for _, id := range []int64{100, 102} {
		f, _ := e.h.getForwardRecord(id)
		if err := e.h.syncForwardServicesOnNode(f, 1); err != nil {
			t.Fatal(err)
		}
	}
	drainEntryRates(e)
	if _, sent := lastEntryRates(agent, 1); sent {
		t.Fatal("tree pushed for a retired arena")
	}
	for _, name := range []string{"100_7_70_tcp", "102_8_80_tcp"} {
		if svc := lastServicePayload(agent, 1, name); svc == nil || svc["limiter"] != nil {
			t.Fatalf("%s references a retired limiter: %v", name, svc)
		}
	}
	// user 7 gets a 速率: only u7 (rules 100 + 101), user 8's rule stays out,
	// the cross-user-arena fallback never triggers.
	agent.reset()
	setUserRate(t, e, 7, 50)
	drainEntryRates(e)
	g, _ := lastEntryRates(agent, 1)
	if c := entryClasses(g); len(c) != 1 || c["u7"] != "@50" {
		t.Fatalf("tree %v", c)
	}
	if ports, _ := g["ports"].([]interface{}); len(ports) != 2 {
		t.Fatalf("ports %v", g["ports"])
	}
	if plan := e.h.entryRatePlanFor(1); plan == nil || len(plan.agentArena) != 0 {
		t.Fatalf("agent arena %+v", plan)
	}
	if m := strings.Join(agent.mutations(1), " "); strings.Contains(m, "arena_speed") || strings.Contains(m, "Limiters") {
		t.Fatalf("arena limiter pushed: %s", m)
	}
}
