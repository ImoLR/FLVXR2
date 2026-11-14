package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// fakeCheckAgents simulates the agents of plan 255 part 2: MonitorPort sync
// (open set per node, ports "another program" holds → busy), TcpPing from a
// source to host:port (open → ok, vendor-blocked port → timeout, nothing
// listening → refused with errorKind).
type fakeCheckAgents struct {
	mu        sync.Mutex
	open      map[int64]map[int]bool
	busy      map[int64]map[int]bool // fixed busy ports
	busyFirst int                    // the next N new ports report busy
	blocked   map[int]bool           // ports a vendor firewall drops
	online    map[int64]bool
	capable   map[int64]bool
	hosts     map[string]int64 // address → node
	syncs     map[int64][][]int
	pings     int
}

func newFakeCheckAgents() *fakeCheckAgents {
	f := &fakeCheckAgents{open: map[int64]map[int]bool{}, busy: map[int64]map[int]bool{}, blocked: map[int]bool{},
		online: map[int64]bool{1: true, 2: true, 3: true}, capable: map[int64]bool{1: true, 2: true, 3: true},
		hosts: map[string]int64{"198.51.100.1": 1, "198.51.100.2": 2, "seednet.example.test": 3}, syncs: map[int64][][]int{}}
	for id := int64(1); id <= 3; id++ {
		f.open[id] = map[int]bool{}
	}
	return f
}

func (f *fakeCheckAgents) install(e *lineMonEnv) {
	e.h.nodeCommandSender = f.send
	e.h.nodeOnlineFn = func(id int64) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.online[id] }
	e.h.monitorPortCapableFn = func(id int64) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.capable[id] }
}

func (f *fakeCheckAgents) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func (f *fakeCheckAgents) openPorts(node int64) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for p := range f.open[node] {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func (f *fakeCheckAgents) syncCount(node int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.syncs[node])
}

func (f *fakeCheckAgents) send(node int64, cmd string, payload interface{}, _ time.Duration) (ws.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.online[node] {
		return ws.CommandResult{}, errors.New("节点不在线")
	}
	raw, _ := json.Marshal(payload)
	switch cmd {
	case "MonitorPort":
		if !f.capable[node] {
			return ws.CommandResult{}, errors.New("未知命令")
		}
		var req struct {
			Action string `json:"action"`
			Ports  []int  `json:"ports"`
		}
		_ = json.Unmarshal(raw, &req)
		f.syncs[node] = append(f.syncs[node], append([]int(nil), req.Ports...))
		want := map[int]bool{}
		var results []interface{}
		for _, p := range req.Ports {
			want[p] = true
			state := "open"
			switch {
			case p < 1024 || p == 22:
				state = "refused"
			case f.busy[node][p]:
				state = "busy"
			case !f.open[node][p] && f.busyFirst > 0:
				f.busyFirst--
				if f.busy[node] == nil {
					f.busy[node] = map[int]bool{}
				}
				f.busy[node][p] = true
				state = "busy"
			}
			if state == "open" {
				f.open[node][p] = true
			}
			results = append(results, map[string]interface{}{"port": p, "state": state})
		}
		for p := range f.open[node] {
			if !want[p] {
				delete(f.open[node], p)
			}
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"results": results}}, nil
	case "TcpPing":
		f.pings++
		var req struct {
			IP   string `json:"ip"`
			Port int    `json:"port"`
		}
		_ = json.Unmarshal(raw, &req)
		target, known := f.hosts[req.IP]
		switch {
		case f.blocked[req.Port]:
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": false, "errorMessage": "所有TCP连接尝试都失败", "errorKind": "timeout"}}, nil
		case known && f.open[target][req.Port]:
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "averageTime": 12.3}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": false, "errorMessage": "所有TCP连接尝试都失败", "errorKind": "refused"}}, nil
	}
	return ws.CommandResult{}, fmt.Errorf("unexpected %s", cmd)
}

func lmAuto(src, target int64, port int) map[string]interface{} {
	m := lmLine(src, target, "", port)
	m["portMode"] = "auto"
	return m
}

type lmSaveOut struct {
	ID     int64              `json:"id"`
	Checks []lineMonitorCheck `json:"checks"`
}

func (e *lineMonEnv) saveChecks(t *testing.T, body map[string]interface{}) lmSaveOut {
	t.Helper()
	code, msg, data := lineMonCall(t, e.h.lineMonitorChartSave, 0, body)
	if code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	var out lmSaveOut
	_ = json.Unmarshal(data, &out)
	return out
}

func (e *lineMonEnv) portRows(t *testing.T, node int64) map[int]string {
	t.Helper()
	rows, err := e.r.ListLineMonitorPorts(node)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	for _, r := range rows {
		out[r.Port] = r.State
	}
	return out
}

func (e *lineMonEnv) line(t *testing.T, id int64) model.LineMonitorLine {
	t.Helper()
	l, err := e.r.GetLineMonitorLine(id)
	if err != nil || l == nil {
		t.Fatalf("line %d: %v", id, err)
	}
	return *l
}

func (e *lineMonEnv) chartLines(t *testing.T, chart int64) []model.LineMonitorLine {
	t.Helper()
	ls, err := e.r.ListLineMonitorChartLines(chart)
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func TestCheckPortAdminOnly(t *testing.T) {
	e := newLineMonEnv(t)
	for name, fn := range map[string]http.HandlerFunc{
		"verify": e.h.lineMonitorCheckPortVerify, "rotate": e.h.lineMonitorCheckPortRotate,
		"command": e.h.lineMonitorCheckPortCommand, "test": e.h.lineMonitorCheckPortTest,
	} {
		if code, _, _ := lineMonCall(t, fn, 1, map[string]interface{}{"lineId": 1}); code != 403 {
			t.Fatalf("%s as user: %d", name, code)
		}
		if code, _, _ := lineMonCall(t, fn, -1, map[string]interface{}{}); code != 401 {
			t.Fatalf("%s anonymous: %d", name, code)
		}
	}
}

// Auto pick = a free port of the node's rule range; every port the panel
// knows is in use is skipped; the reservation then blocks rule / tunnel
// allocation and manual rule ports.
func TestCheckPortAutoPickExcludesUsedAndReserves(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		f := newFakeCheckAgents()
		f.install(e)
		e.exec(`UPDATE node SET port = '1020-1030' WHERE id = 2`)
		for i, p := range []int{1024, 1025, 1026} {
			e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(?, 2, ?)`, 100+i, p)
		}
		e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(9, '3', 2, 1027, 'round', 1, 'tls')`)
		e.exec(`INSERT INTO nftables_rule(forward_id, node_id, protocol, port, "table", chain, status) VALUES(7, 2, 'tcp', 1028, 'flvx', 'c', 1)`)
		out := e.saveChecks(t, map[string]interface{}{"name": "pick", "lines": []interface{}{lmAuto(1, 2, 0)}})
		l := e.chartLines(t, out.ID)[0]
		if l.Port != 1029 && l.Port != 1030 {
			t.Fatalf("picked %d, want 1029 or 1030 (1020-1023 privileged, 1024-1028 used)", l.Port)
		}
		if rows := e.portRows(t, 2); rows[l.Port] != model.LineMonitorPortOpen || len(rows) != 1 {
			t.Fatalf("reservation %v", rows)
		}
		for i := 0; i < 40; i++ {
			p, err := e.r.PickRandomNodePortTx(e.r.DB(), 2, map[int64]int{}, 0)
			if err != nil || p == l.Port || (p >= 1024 && p <= 1027) { // (nft rule ports were never excluded by the rule picker)
				t.Fatalf("rule pick %d (%v) took a reserved / used port (check port %d)", p, err, l.Port)
			}
		}
		used, _ := e.r.GetUsedPortsOnNodeAsMap(2)
		fed, _ := e.r.ListUsedPortsOnNode(2)
		inFed := false
		for _, p := range fed {
			inFed = inFed || p == l.Port
		}
		if !used[l.Port] || !inFed {
			t.Fatalf("reserved port missing from used ports (%v / %v)", used[l.Port], fed)
		}
		if taken, _ := e.r.HasOtherForwardOnNodePort(2, l.Port, 0); !taken {
			t.Fatal("manual rule port check ignores the check port")
		}
		node := &nodeRecord{ID: 2, Name: "Fachost seednet 黑五"}
		err := e.h.validateForwardPortAvailabilityTx(e.r.DB(), node, l.Port, 0)
		if err == nil || !strings.Contains(err.Error(), "线路监测") {
			t.Fatalf("manual rule on the check port: %v", err)
		}
		if f.openPorts(2)[0] != l.Port || out.Checks[0].State != model.LineCheckOK {
			t.Fatalf("agent %v checks %+v", f.openPorts(2), out.Checks)
		}
	})
}

// Save opens the port through the agent and verifies from the source; two
// lines to the same node share it; a vendor-blocked port reads
// 「连不上」 and 换一个端口 tries ≤ 3 ports.
func TestCheckPortVerifyRotateAndShare(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		f := newFakeCheckAgents()
		f.install(e)
		out := e.saveChecks(t, map[string]interface{}{"name": "AWS HK → Seednet", "lines": []interface{}{lmAuto(1, 2, 0), lmAuto(3, 2, 0)}})
		ls := e.chartLines(t, out.ID)
		if ls[0].Port != ls[1].Port || ls[0].Port < 1024 || ls[0].Port > 2000 {
			t.Fatalf("ports %d / %d must be shared and in 1024-2000", ls[0].Port, ls[1].Port)
		}
		if len(out.Checks) != 2 || out.Checks[0].State != model.LineCheckOK || out.Checks[1].State != model.LineCheckOK {
			t.Fatalf("checks %+v", out.Checks)
		}
		if got := f.openPorts(2); len(got) != 1 || got[0] != ls[0].Port {
			t.Fatalf("agent open %v", got)
		}
		// List shows the agent's state.
		_, _, data := lineMonCall(t, e.h.lineMonitorList, 0, map[string]interface{}{})
		if !strings.Contains(string(data), `"agentState":"open"`) || !strings.Contains(string(data), `"checkState":"ok"`) {
			t.Fatalf("list lacks check port state: %s", data)
		}
		// The vendor drops the port → 连不上; rotate the second line.
		p0 := ls[0].Port
		f.set(func() { f.blocked[p0] = true })
		code, _, vdata := lineMonCall(t, e.h.lineMonitorCheckPortVerify, 0, map[string]interface{}{"lineId": ls[1].ID})
		if code != 0 || !strings.Contains(string(vdata), model.LineCheckUnreachable) {
			t.Fatalf("verify blocked: %d %s", code, vdata)
		}
		code, msg, rdata := lineMonCall(t, e.h.lineMonitorCheckPortRotate, 0, map[string]interface{}{"lineId": ls[1].ID})
		var rot struct {
			Tries []lineMonitorRotateTry `json:"tries"`
			OK    bool                   `json:"ok"`
		}
		_ = json.Unmarshal(rdata, &rot)
		if code != 0 || !rot.OK || len(rot.Tries) != 1 {
			t.Fatalf("rotate: %d %s %s", code, msg, rdata)
		}
		l1 := e.line(t, ls[1].ID)
		if l1.Port == p0 || l1.CheckState != model.LineCheckOK {
			t.Fatalf("rotated line %+v", l1)
		}
		// The first line still uses p0 (ref-count): both ports open.
		if got := f.openPorts(2); len(got) != 2 {
			t.Fatalf("agent open %v, want both ports", got)
		}
		// Everything blocked: 3 tries, then stop and say so.
		f.set(func() {
			for p := 1024; p <= 2000; p++ {
				f.blocked[p] = true
			}
		})
		pingsBefore := f.pings
		_, _, rdata = lineMonCall(t, e.h.lineMonitorCheckPortRotate, 0, map[string]interface{}{"lineId": ls[1].ID})
		var rot2 struct {
			Tries []lineMonitorRotateTry `json:"tries"`
			OK    bool                   `json:"ok"`
			Msg   string                 `json:"msg"`
		}
		_ = json.Unmarshal(rdata, &rot2)
		if rot2.OK || len(rot2.Tries) != 3 || !strings.Contains(rot2.Msg, "3 个端口") || f.pings-pingsBefore != 3 {
			t.Fatalf("give up after 3: %s (pings %d)", rdata, f.pings-pingsBefore)
		}
		// Only the last tried port + the first line's port stay open.
		if got := f.openPorts(2); len(got) != 2 {
			t.Fatalf("tried ports not closed: %v", got)
		}
		if rows := e.portRows(t, 2); len(rows) != 2 {
			t.Fatalf("rows %v", rows)
		}
	})
}

// A port the agent reports busy moves the line to another port.
func TestCheckPortBusyMovesLine(t *testing.T) {
	e := newLineMonEnv(t)
	f := newFakeCheckAgents()
	f.install(e)
	f.set(func() { f.busyFirst = 1 })
	out := e.saveChecks(t, map[string]interface{}{"name": "busy", "lines": []interface{}{lmAuto(1, 2, 0)}})
	l := e.chartLines(t, out.ID)[0]
	f.mu.Lock()
	busy := f.busy[2]
	f.mu.Unlock()
	if len(busy) != 1 || busy[l.Port] {
		t.Fatalf("line on %d, busy %v", l.Port, busy)
	}
	if l.CheckState != model.LineCheckOK || len(e.portRows(t, 2)) != 1 {
		t.Fatalf("line %+v rows %v", l, e.portRows(t, 2))
	}
}

// Old agent online → refused with the OTA hint; offline target → reserved,
// pending, opened on reconnect.
func TestCheckPortOldAgentAndOfflineTarget(t *testing.T) {
	e := newLineMonEnv(t)
	f := newFakeCheckAgents()
	f.install(e)
	f.set(func() { f.capable[2] = false })
	code, msg, _ := lineMonCall(t, e.h.lineMonitorChartSave, 0, map[string]interface{}{"name": "old", "lines": []interface{}{lmAuto(1, 2, 0)}})
	if code == 0 || !strings.Contains(msg, "OTA") {
		t.Fatalf("old agent: %d %s", code, msg)
	}
	_, _, opts := lineMonCall(t, e.h.lineMonitorOptions, 0, map[string]interface{}{})
	var o struct {
		Nodes []lineMonitorNodeOption `json:"nodes"`
		Hint  string                  `json:"checkPortUnsupported"`
	}
	_ = json.Unmarshal(opts, &o)
	for _, n := range o.Nodes {
		if n.ID <= 3 && (n.ID == 2) == n.CheckPort {
			t.Fatalf("option %+v", n)
		}
	}
	if !strings.Contains(o.Hint, "OTA") {
		t.Fatalf("hint %q", o.Hint)
	}
	f.set(func() { f.capable[2], f.online[2] = true, false })
	out := e.saveChecks(t, map[string]interface{}{"name": "offline", "lines": []interface{}{lmAuto(1, 2, 0)}})
	l := e.chartLines(t, out.ID)[0]
	if l.CheckState != model.LineCheckPending || f.syncCount(2) != 0 || e.portRows(t, 2)[l.Port] != model.LineMonitorPortOpen {
		t.Fatalf("offline target: %+v syncs %d rows %v", l, f.syncCount(2), e.portRows(t, 2))
	}
	f.set(func() { f.online[2] = true })
	e.h.lineMonitorPortsNodeOnline(2)
	e.h.lineMonitorCleanupWG.Wait()
	if got := f.openPorts(2); len(got) != 1 || got[0] != l.Port || e.line(t, l.ID).CheckState != model.LineCheckOK {
		t.Fatalf("after reconnect: open %v line %+v", got, e.line(t, l.ID))
	}
}

// Every path that stops a check port from being needed closes it on the
// agent and frees the reservation (plan 255 part 2 D).
func TestCheckPortLifecycle(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		f := newFakeCheckAgents()
		f.install(e)
		wait := e.h.lineMonitorCleanupWG.Wait
		a := e.saveChecks(t, map[string]interface{}{"name": "A", "lines": []interface{}{lmAuto(1, 2, 0), lmLine(1, 3, "", 1500)}})
		b := e.saveChecks(t, map[string]interface{}{"name": "B", "lines": []interface{}{lmAuto(3, 2, 0)}})
		la, lb := e.chartLines(t, a.ID), e.chartLines(t, b.ID)
		p := la[0].Port
		if lb[0].Port != p {
			t.Fatalf("second chart must share the node's port: %d vs %d", lb[0].Port, p)
		}
		// 1. delete chart B: chart A still uses the port (ref-count) → kept.
		if code, _, _ := lineMonCall(t, e.h.lineMonitorChartDelete, 0, map[string]interface{}{"id": b.ID}); code != 0 {
			t.Fatal("delete B")
		}
		wait()
		if got := f.openPorts(2); len(got) != 1 || got[0] != p || e.portRows(t, 2)[p] != model.LineMonitorPortOpen {
			t.Fatalf("shared port closed: %v %v", got, e.portRows(t, 2))
		}
		// 2. pause keeps it; resume re-sends the list.
		n := f.syncCount(2)
		lineMonCall(t, e.h.lineMonitorLinePause, 0, map[string]interface{}{"id": la[0].ID, "paused": true})
		wait()
		if f.syncCount(2) != n || len(f.openPorts(2)) != 1 {
			t.Fatal("pause must keep the port without commands")
		}
		f.set(func() { delete(f.open[2], p) }) // the agent's 24 h net closed it meanwhile
		lineMonCall(t, e.h.lineMonitorLinePause, 0, map[string]interface{}{"id": la[0].ID, "paused": false})
		wait()
		if got := f.openPorts(2); len(got) != 1 || got[0] != p {
			t.Fatalf("resume did not re-open: %v", got)
		}
		// 3. change target node 2 → 3: node 2's port closes, node 3 gets one.
		mv := lmAuto(1, 3, 0)
		mv["id"] = la[0].ID
		keep := lmLine(1, 3, "", 1500)
		keep["id"] = la[1].ID
		e.saveChecks(t, map[string]interface{}{"id": a.ID, "name": "A", "lines": []interface{}{mv, keep}})
		if got := f.openPorts(2); len(got) != 0 || len(e.portRows(t, 2)) != 0 {
			t.Fatalf("old target still open: %v rows %v", got, e.portRows(t, 2))
		}
		p3 := e.line(t, la[0].ID).Port
		if got := f.openPorts(3); len(got) != 1 || got[0] != p3 || p3 == 1500 {
			t.Fatalf("new target %v (line port %d)", got, p3)
		}
		// 4. mode auto → manual closes it.
		man := lmLine(1, 3, "", 1600)
		man["id"], man["portMode"] = la[0].ID, "manual"
		e.saveChecks(t, map[string]interface{}{"id": a.ID, "name": "A", "lines": []interface{}{man, keep}})
		if len(f.openPorts(3)) != 0 || len(e.portRows(t, 3)) != 0 {
			t.Fatalf("manual mode left %v", f.openPorts(3))
		}
		// 5. offline at delete: 待关闭 kept (still reserved) → closed after reconnect.
		auto := lmAuto(1, 3, 0)
		auto["id"] = la[0].ID
		e.saveChecks(t, map[string]interface{}{"id": a.ID, "name": "A", "lines": []interface{}{auto, keep}})
		p3 = e.line(t, la[0].ID).Port
		f.set(func() { f.online[3] = false })
		if code, _, _ := lineMonCall(t, e.h.lineMonitorChartDelete, 0, map[string]interface{}{"id": a.ID}); code != 0 {
			t.Fatal("delete A")
		}
		wait()
		if rows := e.portRows(t, 3); rows[p3] != model.LineMonitorPortClosing {
			t.Fatalf("offline delete: rows %v", rows)
		}
		if taken, _ := e.r.HasOtherForwardOnNodePort(3, p3, 0); !taken {
			t.Fatal("a 待关闭 port must stay reserved until closed")
		}
		if got := f.openPorts(3); len(got) != 1 {
			t.Fatalf("agent offline, port still there: %v", got)
		}
		f.set(func() { f.online[3] = true })
		e.h.lineMonitorPortsNodeOnline(3)
		wait()
		if got := f.openPorts(3); len(got) != 0 || len(e.portRows(t, 3)) != 0 {
			t.Fatalf("after reconnect: open %v rows %v", got, e.portRows(t, 3))
		}
		// 6. agent restart with a stale persisted port → reconcile closes it.
		f.set(func() { f.open[2][1999] = true })
		e.h.lineMonitorPortsNodeOnline(2)
		wait()
		if got := f.openPorts(2); len(got) != 0 {
			t.Fatalf("stale port survived reconnect: %v", got)
		}
	})
}

// Node delete: lines from a deleted source go and their target's port
// closes; a deleted target drops its reservation (nothing to close).
func TestCheckPortNodeDelete(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		f := newFakeCheckAgents()
		f.install(e)
		c1 := e.saveChecks(t, map[string]interface{}{"name": "from 1", "lines": []interface{}{lmAuto(1, 2, 0)}})
		c2 := e.saveChecks(t, map[string]interface{}{"name": "to 3", "lines": []interface{}{lmAuto(2, 3, 0)}})
		if len(f.openPorts(2)) != 1 || len(f.openPorts(3)) != 1 {
			t.Fatal("setup")
		}
		if err := e.h.deleteNodeByID(1); err != nil {
			t.Fatalf("delete source node: %v", err)
		}
		e.h.lineMonitorCleanupWG.Wait()
		if ls := e.chartLines(t, c1.ID); len(ls) != 0 {
			t.Fatalf("lines of the deleted source stayed: %+v", ls)
		}
		if got := f.openPorts(2); len(got) != 0 || len(e.portRows(t, 2)) != 0 {
			t.Fatalf("target port of a deleted source: %v %v", got, e.portRows(t, 2))
		}
		n3 := f.syncCount(3)
		if err := e.h.deleteNodeByID(3); err != nil {
			t.Fatalf("delete target node: %v", err)
		}
		e.h.lineMonitorCleanupWG.Wait()
		if ls := e.chartLines(t, c2.ID); len(ls) != 0 || len(e.portRows(t, 3)) != 0 || f.syncCount(3) != n3 {
			t.Fatalf("target delete: lines %+v rows %v syncs %d→%d", ls, e.portRows(t, 3), n3, f.syncCount(3))
		}
	})
}

// A refused slot (both attempts) is 端口未开启, not 断流, and re-sends the
// target's list (≤ 1 / 10 min).
func TestCheckPortClosedSlotAndHeal(t *testing.T) {
	e := newLineMonEnv(t)
	f := newFakeCheckAgents()
	f.install(e)
	e.h.lineMonitor.testSlot, e.h.lineMonitor.testRetry = time.Second, 100*time.Millisecond // before the engine exists
	out := e.saveChecks(t, map[string]interface{}{"name": "heal", "lines": []interface{}{lmAuto(1, 2, 0), lmLine(1, 0, "203.0.113.9", 25931)}})
	ls := e.chartLines(t, out.ID)
	f.set(func() { delete(f.open[2], ls[0].Port) }) // expired on the agent
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	before := f.syncCount(2)
	e.h.jobsWG.Add(1)
	e.h.runLineMonitorLoop(ctx)
	e.h.lineMonitorCleanupWG.Wait()
	var rows []model.LineMonitorSample
	e.r.DB().Where("line_id = ?", ls[1].ID).Find(&rows)
	if len(rows) == 0 {
		t.Fatal("custom line not measured")
	}
	for _, r := range rows {
		if r.Status != model.LineSampleClosed {
			t.Fatalf("refused custom target stored status %d, want 4 (端口未开启)", r.Status)
		}
	}
	var autoRows []model.LineMonitorSample
	e.r.DB().Where("line_id = ?", ls[0].ID).Order("ts").Find(&autoRows)
	if len(autoRows) < 2 || autoRows[0].Status != model.LineSampleClosed || autoRows[len(autoRows)-1].Status != model.LineSampleOK {
		t.Fatalf("auto line: expired port must read 端口未开启, then OK after the heal: %+v", autoRows)
	}
	if got := f.syncCount(2) - before; got != 1 {
		t.Fatalf("heal syncs %d, want exactly 1 within 10 min", got)
	}
	if got := f.openPorts(2); len(got) != 1 {
		t.Fatalf("heal did not re-open: %v", got)
	}
	// Old source agents (no errorKind) keep reading as 断流.
	a := linemon.Attempt{Kind: linemon.Failed}
	if st, _, _ := linemon.Classify(a, &a); st != linemon.StatusDown {
		t.Fatalf("old agent refusal = %d", st)
	}
}

func TestCheckPortCommandAndConnectivityTest(t *testing.T) {
	e := newLineMonEnv(t)
	f := newFakeCheckAgents()
	f.install(e)
	call := func(body map[string]interface{}) (int, string, map[string]interface{}) {
		code, msg, data := lineMonCall(t, e.h.lineMonitorCheckPortCommand, 0, body)
		var m map[string]interface{}
		_ = json.Unmarshal(data, &m)
		return code, msg, m
	}
	code, _, m := call(map[string]interface{}{"host": "hinet.example.test", "port": 0, "sourceIds": []int64{1, 2}})
	port := int(m["port"].(float64))
	if code != 0 || port < 20000 || port > 29999 || m["suggested"] != true || m["allowDefault"] != true || m["allow"] != true {
		t.Fatalf("suggest: %d %v", code, m)
	}
	install := m["install"].(string)
	if !strings.Contains(install, fmt.Sprintf("P=%d\n", port)) || !strings.Contains(install, "IPAddressAllow=198.51.100.1 198.51.100.2\n") ||
		!strings.Contains(install, "W=${FLVX_LINEMON_TEST_IDLE:-86400}") || strings.Contains(install, "hinet") {
		t.Fatalf("install:\n%s", install)
	}
	// A dynamic-IP source (host name address) → off by default, with the reason.
	code, _, m = call(map[string]interface{}{"host": "hinet.example.test", "port": 25931, "window": 6, "sourceIds": []int64{1, 3}})
	if code != 0 || m["allowDefault"] != false || m["allow"] != false || !strings.Contains(m["allowNote"].(string), "Tcscloud Seednet") ||
		strings.Contains(m["install"].(string), "IPAddressAllow") || !strings.Contains(m["install"].(string), "-21600}") {
		t.Fatalf("dynamic source: %v", m)
	}
	for _, bad := range []map[string]interface{}{{"port": 22}, {"port": 80}, {"port": 25931, "window": 5}, {"port": 25931, "host": "bad host;rm"}} {
		if code, _, _ := call(bad); code == 0 {
			t.Fatalf("accepted %v", bad)
		}
	}
	// 测试连通: per source ✓ / ✗ with the kind.
	f.set(func() { f.hosts["hinet.example.test"] = 99; f.open[99] = map[int]bool{25931: true} })
	code, _, data := lineMonCall(t, e.h.lineMonitorCheckPortTest, 0, map[string]interface{}{"host": "hinet.example.test", "port": 25931, "sourceIds": []int64{1, 2}})
	var res struct {
		Results []lineMonitorTestResult `json:"results"`
	}
	_ = json.Unmarshal(data, &res)
	if code != 0 || len(res.Results) != 2 || !res.Results[0].OK || res.Results[0].Name != "AWS HK" {
		t.Fatalf("test ok: %s", data)
	}
	f.set(func() { f.open[99] = map[int]bool{}; f.online[2] = false })
	_, _, data = lineMonCall(t, e.h.lineMonitorCheckPortTest, 0, map[string]interface{}{"host": "hinet.example.test", "port": 25931, "sourceIds": []int64{1, 2}})
	_ = json.Unmarshal(data, &res)
	if res.Results[0].OK || res.Results[0].Kind != "refused" || res.Results[1].Kind != "offline" {
		t.Fatalf("test refused / offline: %s", data)
	}
}
