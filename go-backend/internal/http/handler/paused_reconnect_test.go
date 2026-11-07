package handler

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// pauseAwareAgent adds pause state to the plan-138 fake agent. An old agent
// (reportsPaused=false) forgets it on restart — every service of gost.json
// listens again, the fork.124 bug; a plan-236 agent keeps paused services
// closed, also after UpdateService/AddService with metadata.paused, and
// reports them in ListServices.
type pauseAwareAgent struct {
	*fakeRuntimeAgent
	reportsPaused bool
	pmu           sync.Mutex
	paused        map[int64]map[string]bool
}

func newPauseAwareAgent(inner *fakeRuntimeAgent, reportsPaused bool) *pauseAwareAgent {
	return &pauseAwareAgent{fakeRuntimeAgent: inner, reportsPaused: reportsPaused, paused: map[int64]map[string]bool{}}
}

func (p *pauseAwareAgent) mark(nodeID int64, name string, on bool) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	if p.paused[nodeID] == nil {
		p.paused[nodeID] = map[string]bool{}
	}
	if on {
		p.paused[nodeID][name] = true
	} else {
		delete(p.paused[nodeID], name)
	}
}

func (p *pauseAwareAgent) send(nodeID int64, method string, data interface{}, d time.Duration) (ws.CommandResult, error) {
	g := genericOf(data)
	names := func() []string {
		m, _ := g.(map[string]interface{})
		raw, _ := m["services"].([]interface{})
		out := []string{}
		for _, v := range raw {
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	switch method {
	case "PauseService":
		// Like the agent: every name must exist, else nothing is paused.
		for _, name := range names() {
			if !p.has(nodeID, "service:"+name) {
				p.fakeRuntimeAgent.mu.Lock()
				p.commands = append(p.commands, redeployCommand{nodeID: nodeID, method: method, data: data})
				p.fakeRuntimeAgent.mu.Unlock()
				return ws.CommandResult{}, fmt.Errorf("service %s not found", name)
			}
		}
		for _, name := range names() {
			p.mark(nodeID, name, true)
		}
	case "ResumeService", "DeleteService":
		for _, name := range names() {
			p.mark(nodeID, name, false)
		}
	case "UpdateService", "AddService":
		list, _ := g.([]interface{})
		for _, item := range list {
			svc, _ := item.(map[string]interface{})
			name, _ := svc["name"].(string)
			meta, _ := svc["metadata"].(map[string]interface{})
			paused, _ := meta["paused"].(bool)
			p.mark(nodeID, name, paused && p.reportsPaused)
		}
	}
	res, err := p.fakeRuntimeAgent.send(nodeID, method, data, d)
	if method == "ListServices" && err == nil && p.reportsPaused {
		inv := res.Data
		p.pmu.Lock()
		list := []string{}
		for name := range p.paused[nodeID] {
			list = append(list, name)
		}
		p.pmu.Unlock()
		sort.Strings(list)
		inv["paused"] = list
	}
	return res, err
}

func (p *pauseAwareAgent) restart(nodeID int64) {
	p.fakeRuntimeAgent.restart(nodeID)
	if !p.reportsPaused {
		p.pmu.Lock()
		p.paused[nodeID] = map[string]bool{}
		p.pmu.Unlock()
	}
}

// running: the service exists on the node and is not paused.
func (p *pauseAwareAgent) running(nodeID int64, name string) bool {
	p.pmu.Lock()
	paused := p.paused[nodeID][name]
	p.pmu.Unlock()
	return p.has(nodeID, "service:"+name) && !paused
}

func (p *pauseAwareAgent) payloads(nodeID int64, method string) []interface{} {
	p.fakeRuntimeAgent.mu.Lock()
	defer p.fakeRuntimeAgent.mu.Unlock()
	var out []interface{}
	for _, c := range p.commands {
		if c.nodeID == nodeID && c.method == method {
			out = append(out, genericOf(c.data))
		}
	}
	return out
}

// newPausedReconnectEnv: the reconnect env plus a second gost rule 102 of
// user 7 on entry 1, deployed.
func newPausedReconnectEnv(t *testing.T, reportsPaused bool) (*flowTestEnv, *pauseAwareAgent) {
	t.Helper()
	e, inner := newReconnectDiffEnv(t)
	e.addForward(102, 7, 10)
	e.exec(`UPDATE forward SET mode = 'gost', remote_addr = '8.8.8.8:443' WHERE id = 102`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(102, 1, 1502)`)
	agent := newPauseAwareAgent(inner, reportsPaused)
	e.h.nodeCommandSender = agent.send
	deployAll(e, inner)
	return e, agent
}

var pausedTestServices = []string{"100_7_70_tcp", "100_7_70_udp", "102_7_70_tcp", "102_7_70_udp"}

// pauseRules pauses rules the way forward/pause does (gost: PauseService,
// nftables: rules removed) and stores status 0.
func pauseRules(t *testing.T, e *flowTestEnv, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		f, err := e.h.getForwardRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.EqualFold(f.Mode, "nftables") {
			ports, _ := e.h.listForwardPorts(id)
			if err := e.h.deleteNftablesRules(f, ports); err != nil {
				t.Fatal(err)
			}
		} else if err := e.h.controlForwardServices(f, "PauseService", false); err != nil {
			t.Fatal(err)
		}
		e.exec(`UPDATE forward SET status = 0 WHERE id = ?`, id)
	}
}

func TestRepauseOldAgentAfterRestartSendsOneBatchedPause(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, false)
	for _, name := range pausedTestServices {
		if !agent.running(1, name) {
			t.Fatalf("%s not deployed: %+v", name, agent.objects[1])
		}
	}
	pauseRules(t, e, 100, 102, 101)
	for _, name := range pausedTestServices {
		if agent.running(1, name) {
			t.Fatalf("%s still running after pause", name)
		}
	}
	// OTA / crash: an old agent serves every service of gost.json again.
	agent.restart(1)
	if !agent.running(1, "100_7_70_tcp") {
		t.Fatal("fake old agent did not resurrect the paused rule")
	}
	agent.reset()
	res := e.h.repauseInactiveForwards(1)
	if agent.count(1, "ListServices") != 1 || agent.count(1, "PauseService") != 1 || len(agent.mutations(1)) != 1 {
		t.Fatalf("want one ListServices + one PauseService, got %v", agent.mutations(1))
	}
	if strings.Join(res.services, ",") != strings.Join(pausedTestServices, ",") || res.agentNew {
		t.Fatalf("re-paused %v (agentNew=%v)", res.services, res.agentNew)
	}
	for _, name := range pausedTestServices {
		if agent.running(1, name) {
			t.Fatalf("%s running after the reconnect re-pause", name)
		}
	}
	// Other nodes host no paused rule: no command at all.
	for _, id := range []int64{2, 3, 4} {
		if r := e.h.repauseInactiveForwards(id); r.inactive != 0 || agent.count(id, "ListServices") != 0 {
			t.Fatalf("node %d: %+v / %v", id, r, agent.mutations(id))
		}
	}
	// The reconnect redeploy leaves paused rules alone.
	agent.reset()
	e.h.redeployNodeRuntime(1)
	for _, m := range agent.mutations(1) {
		if strings.Contains(m, "100_7_70") || strings.Contains(m, "102_7_70") || strings.Contains(m, "nft 101") {
			t.Fatalf("redeploy touched a paused rule: %v", agent.mutations(1))
		}
	}
	// Resume still works afterwards.
	f, _ := e.h.getForwardRecord(100)
	f.Status = 1
	e.exec(`UPDATE forward SET status = 1 WHERE id = 100`)
	if err := e.h.controlForwardServices(f, "ResumeService", false); err != nil {
		t.Fatal(err)
	}
	if !agent.running(1, "100_7_70_tcp") || agent.running(1, "102_7_70_tcp") {
		t.Fatal("resume after the re-pause")
	}
}

func TestRepausePlan236AgentSendsNothing(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, true)
	pauseRules(t, e, 100, 102, 101)
	agent.restart(1)
	agent.reset()
	res := e.h.repauseInactiveForwards(1)
	if agent.count(1, "ListServices") != 1 || len(agent.mutations(1)) != 0 || !res.agentNew || res.acted() {
		t.Fatalf("plan-236 agent: %+v, mutations %v", res, agent.mutations(1))
	}
	for _, name := range pausedTestServices {
		if agent.running(1, name) {
			t.Fatalf("%s running", name)
		}
	}
}

func TestRepauseWithoutPausedRulesSendsNoCommand(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, false)
	agent.restart(1)
	agent.reset()
	if res := e.h.repauseInactiveForwards(1); res.inactive != 0 || res.acted() {
		t.Fatalf("%+v", res)
	}
	if n := len(agent.commands); n != 0 {
		t.Fatalf("no paused rule, but %d commands", n)
	}
}

// Every status: user/admin pause, expiry, quota, CN block, landing pause all
// store status 0; any status other than 1 counts.
func TestRepauseCoversEveryNonRunningStatus(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, false)
	pauseRules(t, e, 100)
	e.exec(`UPDATE forward SET status = -1, cn_blocked = ?, cn_blocked_auto_paused = ? WHERE id = 100`, true, true)
	agent.restart(1)
	agent.reset()
	res := e.h.repauseInactiveForwards(1)
	if strings.Join(res.services, ",") != "100_7_70_tcp,100_7_70_udp" || agent.count(1, "PauseService") != 1 {
		t.Fatalf("%+v %v", res, agent.mutations(1))
	}
	if !agent.running(1, "102_7_70_tcp") {
		t.Fatal("running rule paused")
	}
}

// Plan 236: a sync of a paused rule (edit, tunnel/node change, …) carries
// metadata.paused, so a plan-236 agent never opens it; the follow-up
// PauseService stays for older agents.
func TestSyncPausedRuleCarriesPausedMetadata(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, true)
	pauseRules(t, e, 100)
	agent.reset()
	f, _ := e.h.getForwardRecord(100)
	if err := e.h.syncForwardServices(f, "UpdateService", true); err != nil {
		t.Fatal(err)
	}
	ups := agent.payloads(1, "UpdateService")
	if len(ups) != 1 {
		t.Fatalf("UpdateService payloads %v", agent.mutations(1))
	}
	for _, item := range ups[0].([]interface{}) {
		meta, _ := item.(map[string]interface{})["metadata"].(map[string]interface{})
		if p, _ := meta["paused"].(bool); !p {
			t.Fatalf("paused rule pushed without paused metadata: %v", item)
		}
	}
	if agent.count(1, "PauseService") == 0 {
		t.Fatal("no follow-up PauseService for older agents")
	}
	if agent.running(1, "100_7_70_tcp") {
		t.Fatal("paused rule running after sync")
	}
	// A running rule never carries the flag.
	agent.reset()
	f2, _ := e.h.getForwardRecord(102)
	if err := e.h.syncForwardServices(f2, "UpdateService", true); err != nil {
		t.Fatal(err)
	}
	for _, item := range agent.payloads(1, "UpdateService")[0].([]interface{}) {
		meta, _ := item.(map[string]interface{})["metadata"].(map[string]interface{})
		if _, ok := meta["paused"]; ok {
			t.Fatalf("running rule pushed with paused: %v", item)
		}
	}
	if !agent.running(1, "102_7_70_tcp") {
		t.Fatal("running rule not running")
	}
}

// Before plan 236 the nftables branch installed a paused rule's nft rules
// and returned before the keep-paused step.
func TestSyncPausedNftRuleIsRemovedNotInstalled(t *testing.T) {
	e, agent := newPausedReconnectEnv(t, false)
	pauseRules(t, e, 101)
	agent.reset()
	f, _ := e.h.getForwardRecord(101)
	if err := e.h.syncForwardServices(f, "UpdateService", true); err != nil {
		t.Fatal(err)
	}
	if agent.count(1, "AddNftablesRules") != 0 || agent.has(1, "nft:101_nft") {
		t.Fatalf("paused nft rule installed: %v", agent.mutations(1))
	}
	// Quota release resumes it (status 1 before the sync).
	agent.reset()
	e.h.applyUserQuotaRelease(&repo.UserQuotaRelease{UserID: 7, UnblockUser: true, ForwardIDs: []int64{101}}, time.Now().UnixMilli())
	if agent.count(1, "AddNftablesRules") != 1 || !agent.has(1, "nft:101_nft") {
		t.Fatalf("quota release did not reinstall the nft rule: %v", agent.mutations(1))
	}
	if st := mustForwardStatus(t, e, 101); st != 1 {
		t.Fatalf("status %d", st)
	}
}

func mustForwardStatus(t *testing.T, e *flowTestEnv, id int64) int {
	t.Helper()
	var st int
	if err := e.r.DB().Raw(`SELECT status FROM forward WHERE id = ?`, id).Row().Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}
