package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// Plan 244 预热池 on a real repository (SQLite; PostgreSQL with
// FLVX_POSTGRES_TEST_DSN): tunnel 40 = 隧道转发 entry 1 → middle 2 → exit 3,
// tunnel 41 = 端口转发 on entry 1. The pools follow the rules through the
// rule API (create / pause / resume / delete), the switch, and agent
// sessions; only warm-capable agents get SetWarmPools.
type warmEnv struct {
	*bindEnv
	capable map[int64]bool
	fail    map[int64]bool
	cmu     sync.Mutex
}

func newWarmEnv(t *testing.T, dialect string) *warmEnv {
	t.Helper()
	r := sharedLandingTestRepo(t, dialect)
	be := &bindEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}
	e := &warmEnv{bindEnv: be, capable: map[int64]bool{1: true, 2: true, 3: true}, fail: map[int64]bool{}}
	be.h.nodeCommandSender = func(id int64, command string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		raw, _ := json.Marshal(data)
		e.cmu.Lock()
		failing := e.fail[id] && command == "SetWarmPools"
		e.cmu.Unlock()
		be.mu.Lock()
		be.cmds = append(be.cmds, bindCmd{id, command, string(raw)})
		be.mu.Unlock()
		if failing {
			return ws.CommandResult{}, errors.New("等待节点响应超时")
		}
		return ws.CommandResult{Success: true}, nil
	}
	be.h.bestPath = newBestPathManager()
	be.h.ruleBest = newRuleBestManager()
	be.h.chainSwapCapableFn = func(int64) bool { return true }
	be.h.nodeOnlineFn = func(int64) bool { return true }
	be.h.destOrderCapableFn = func(int64) bool { return true }
	be.h.warmPoolNodesFn = func() []int64 {
		e.cmu.Lock()
		defer e.cmu.Unlock()
		var out []int64
		for id, ok := range e.capable {
			if ok {
				out = append(out, id)
			}
		}
		return out
	}
	be.h.warmPoolCapableFn = func(id int64) bool { e.cmu.Lock(); defer e.cmu.Unlock(); return e.capable[id] }
	names := map[int64]string{1: "Mkcloud 沪港ixp 新春版", 2: "Bestvm HK 年付", 3: "Bestvm JP B"}
	for id := int64(1); id <= 3; id++ {
		be.create(&model.Node{ID: id, Name: names[id], Secret: fmt.Sprintf("s%d", id), ServerIP: fmt.Sprintf("192.0.2.%d", id),
			ServerIPV4: sql.NullString{String: fmt.Sprintf("192.0.2.%d", id), Valid: true}, Port: "10000-20000", TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: be.now, Status: 1})
	}
	be.create(&model.Tunnel{ID: 40, Name: "沪港ixp 新春版 → 日本", TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 1, CreatedTime: be.now, UpdatedTime: be.now, Status: 1,
		InIP: sql.NullString{String: "192.0.2.1", Valid: true}})
	be.create(&model.ChainTunnel{TunnelID: 40, ChainType: "1", NodeID: 1, Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	be.create(&model.ChainTunnel{TunnelID: 40, ChainType: "2", NodeID: 2, Port: nullInt(2040), Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	be.create(&model.ChainTunnel{TunnelID: 40, ChainType: "3", NodeID: 3, Port: nullInt(3040), Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	be.create(&model.Tunnel{ID: 41, Name: "沪日ixp 440 端口转发", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 1, CreatedTime: be.now, UpdatedTime: be.now, Status: 1,
		InIP: sql.NullString{String: "192.0.2.1", Valid: true}})
	be.create(&model.ChainTunnel{TunnelID: 41, ChainType: "1", NodeID: 1, Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	return e
}

// warmState: the last SetWarmPools payload each node got since mark.
func (e *warmEnv) warmState(mark int) map[int64]warmPoolPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int64]warmPoolPayload{}
	for _, c := range e.cmds[mark:] {
		if c.command != "SetWarmPools" {
			continue
		}
		var p warmPoolPayload
		if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
			e.t.Fatalf("payload %s: %v", c.payload, err)
		}
		out[c.node] = p
	}
	return out
}

func (e *warmEnv) reconcile() (pushed []int64, state map[int64]warmPoolPayload) {
	mark := e.cmdCount()
	pushed = e.h.reconcileWarmPools()
	sort.Slice(pushed, func(i, j int) bool { return pushed[i] < pushed[j] })
	return pushed, e.warmState(mark)
}

func chainsOf(p warmPoolPayload) string {
	keys := make([]string, 0, len(p.Chains)+len(p.Forwards))
	for k, v := range p.Chains {
		keys = append(keys, fmt.Sprintf("%s=%d", k, v))
	}
	for k, v := range p.Forwards {
		keys = append(keys, fmt.Sprintf("f%s=%d", k, v))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestWarmPoolFollowsRules(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newWarmEnv(t, dialect)
			// First pass after start: every capable agent gets its (empty) state once.
			if pushed, st := e.reconcile(); len(pushed) != 3 || chainsOf(st[1]) != "" {
				t.Fatalf("first pass pushed %v %v", pushed, st)
			}
			if pushed, _ := e.reconcile(); len(pushed) != 0 {
				t.Fatalf("unchanged state pushed again to %v", pushed)
			}

			// A rule on the 隧道转发 tunnel: entry + middle warm, exit not.
			code, msg, data := e.call(e.h.forwardCreate, "1", 0, `{"name":"r40","tunnelId":40,"remoteAddr":"198.51.100.7:443"}`)
			if code != 0 {
				t.Fatalf("create rule: %d %s", code, msg)
			}
			var r40 struct{ ID int64 }
			_ = json.Unmarshal(data, &r40)
			pushed, st := e.reconcile()
			if fmt.Sprint(pushed) != "[1 2]" || chainsOf(st[1]) != "chains_40=2" || chainsOf(st[2]) != "chains_40=2" {
				t.Fatalf("after rule create: pushed %v state %v", pushed, st)
			}
			// The rule's own services and the tunnel's chains carry nothing new.
			e.mu.Lock()
			for _, c := range e.cmds {
				if c.command != "SetWarmPools" && strings.Contains(c.payload, "warm") {
					t.Fatalf("%s carries warm keys: %s", c.command, c.payload)
				}
			}
			e.mu.Unlock()

			// A rule on the 端口转发 tunnel: one pool per rule on its entry.
			code, msg, data = e.call(e.h.forwardCreate, "1", 0, `{"name":"r41","tunnelId":41,"remoteAddr":"198.51.100.8:8388"}`)
			if code != 0 {
				t.Fatalf("create port rule: %d %s", code, msg)
			}
			var r41 struct{ ID int64 }
			_ = json.Unmarshal(data, &r41)
			pushed, st = e.reconcile()
			want1 := fmt.Sprintf("chains_40=2,f%d=2", r41.ID)
			if fmt.Sprint(pushed) != "[1]" || chainsOf(st[1]) != want1 {
				t.Fatalf("after port rule: pushed %v state %v", pushed, st)
			}

			// Pause the tunnel rule: the chain pools go, the port rule stays.
			if code, msg, _ := e.call(e.h.forwardPause, "1", 0, fmt.Sprintf(`{"id":%d}`, r40.ID)); code != 0 {
				t.Fatalf("pause: %d %s", code, msg)
			}
			pushed, st = e.reconcile()
			if fmt.Sprint(pushed) != "[1 2]" || chainsOf(st[1]) != fmt.Sprintf("f%d=2", r41.ID) || chainsOf(st[2]) != "" {
				t.Fatalf("after pause: pushed %v state %v", pushed, st)
			}
			if code, msg, _ := e.call(e.h.forwardResume, "1", 0, fmt.Sprintf(`{"id":%d}`, r40.ID)); code != 0 {
				t.Fatalf("resume: %d %s", code, msg)
			}
			if _, st = e.reconcile(); chainsOf(st[2]) != "chains_40=2" {
				t.Fatalf("after resume: %v", st)
			}

			// Switch off (light endpoint): pools gone, nothing re-created.
			mark := e.cmdCount()
			if code, msg, _ := e.call(e.h.tunnelWarmPoolUpdate, "1", 0, `{"id":40,"warmPool":0}`); code != 0 {
				t.Fatalf("switch off: %d %s", code, msg)
			}
			if n := e.cmdCount() - mark; n != 0 {
				t.Fatalf("switch save sent %d commands itself (want 0, the pools follow)", n)
			}
			pushed, st = e.reconcile()
			if fmt.Sprint(pushed) != "[1 2]" || chainsOf(st[1]) != fmt.Sprintf("f%d=2", r41.ID) || chainsOf(st[2]) != "" {
				t.Fatalf("after switch off: pushed %v state %v", pushed, st)
			}
			e.mu.Lock()
			for _, c := range e.cmds[mark:] {
				if c.command != "SetWarmPools" {
					t.Fatalf("switch off re-pushed %s", c.command)
				}
			}
			e.mu.Unlock()
			if tr, _ := e.r.GetTunnelRecord(40); tr.WarmPool != 0 {
				t.Fatalf("switch not stored: %d", tr.WarmPool)
			}
			if code, _, _ := e.call(e.h.tunnelWarmPoolUpdate, "1", 0, `{"id":40,"warmPool":true}`); code != 0 {
				t.Fatal("switch on")
			}
			if _, st = e.reconcile(); chainsOf(st[2]) != "chains_40=2" {
				t.Fatalf("after switch on: %v", st)
			}

			// An agent without the capability never gets SetWarmPools; a
			// reconnect re-sends the full state; a failed push is retried.
			e.cmu.Lock()
			e.capable[2] = false
			e.fail[1] = true
			e.cmu.Unlock()
			e.h.warmPoolNodeSession(1)
			if pushed, st := e.reconcile(); len(pushed) != 0 || st[2].Chains != nil {
				t.Fatalf("pushed %v to a failing / incapable node: %v", pushed, st)
			}
			e.cmu.Lock()
			e.fail[1] = false
			e.cmu.Unlock()
			if pushed, st := e.reconcile(); fmt.Sprint(pushed) != "[1]" || chainsOf(st[1]) != want1 {
				t.Fatalf("retry after failure: %v %v", pushed, st)
			}

			// Delete both rules: everything goes.
			e.cmu.Lock()
			e.capable[2] = true
			e.cmu.Unlock()
			e.h.warmPoolNodeSession(2)
			for _, id := range []int64{r40.ID, r41.ID} {
				if code, msg, _ := e.call(e.h.forwardDelete, "1", 0, fmt.Sprintf(`{"id":%d}`, id)); code != 0 {
					t.Fatalf("delete %d: %d %s", id, code, msg)
				}
			}
			pushed, st = e.reconcile()
			if fmt.Sprint(pushed) != "[1 2]" || chainsOf(st[1]) != "" || chainsOf(st[2]) != "" {
				t.Fatalf("after delete: pushed %v state %v", pushed, st)
			}
		})
	}
}

func TestWarmPoolTunnelSwitchOnCreateAndUpdate(t *testing.T) {
	e := newWarmEnv(t, "sqlite")
	// The tunnel list carries the switch.
	items, err := e.r.ListTunnels()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if asInt(it["warmPool"], -1) != 1 {
			t.Fatalf("tunnel %v warmPool = %v", it["id"], it["warmPool"])
		}
	}
	if got := tunnelWarmPoolFromRequest(map[string]interface{}{}, 1); got != 1 {
		t.Fatal("absent key must keep the fallback")
	}
	if got := tunnelWarmPoolFromRequest(map[string]interface{}{"warmPool": false}, 1); got != 0 {
		t.Fatal("false = off")
	}
	if got := tunnelWarmPoolFromRequest(map[string]interface{}{"warm_pool": float64(0)}, 1); got != 0 {
		t.Fatal("0 = off")
	}
	// Non-admins cannot flip it.
	if code, _, _ := e.call(e.h.tunnelWarmPoolUpdate, "2", 1, `{"id":40,"warmPool":0}`); code == 0 {
		t.Fatal("user changed the switch")
	}
	if code, _, _ := e.call(e.h.tunnelWarmPoolUpdate, "1", 0, `{"id":999,"warmPool":0}`); code == 0 {
		t.Fatal("unknown tunnel accepted")
	}
}

func TestWarmRelayReadTimeout(t *testing.T) {
	node := &nodeRecord{ID: 3, TCPListenAddr: "0.0.0.0"}
	for _, ct := range []int{2, 3} {
		svcs := buildTunnelChainServiceConfig(40, tunnelRuntimeNode{NodeID: 3, Port: 3040, ChainType: ct, Protocol: "tls"}, node, 1, nil)
		md := svcs[0]["handler"].(map[string]interface{})["metadata"].(map[string]interface{})
		if md["readTimeout"] != "130s" || md["nodelay"] != true {
			t.Fatalf("chain type %d relay metadata %v", ct, md)
		}
	}
}

// ---- statistics / view ----------------------------------------------------

func warmReport(keys ...ws.WarmPoolKeyReport) *ws.WarmPoolReport {
	return &ws.WarmPoolReport{Cap: 64, Live: 4, Keys: keys}
}

func (e *warmEnv) tunnelView(id int64) *warmPoolView {
	e.t.Helper()
	items, err := e.r.ListTunnels()
	if err != nil {
		e.t.Fatal(err)
	}
	e.h.attachWarmPoolStates(items)
	for _, it := range items {
		if asInt64(it["id"], 0) == id {
			v, _ := it["warmPoolState"].(*warmPoolView)
			return v
		}
	}
	return nil
}

func TestWarmPoolView(t *testing.T) {
	e := newWarmEnv(t, "sqlite")
	m := e.h.warmPools()
	clock := time.Now()
	m.now = func() time.Time { return clock }
	if code, msg, _ := e.call(e.h.forwardCreate, "1", 0, `{"name":"r40","tunnelId":40,"remoteAddr":"198.51.100.7:443"}`); code != 0 {
		t.Fatalf("create rule: %d %s", code, msg)
	}
	// Entry → middle: cold 62 ms (refills + misses), warm extra 2 ms over
	// 96 streams, 4 misses; middle → exit: cold 92, warm extra 0.
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, Idle: 2,
		ColdN: 20, ColdMs: 1240, MissN: 4, MissMs: 248, WarmN: 96, WarmMs: 192}))
	m.ingest(2, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.3:3040", Target: true, Idle: 2,
		ColdN: 10, ColdMs: 920, MissN: 1, MissMs: 92, WarmN: 99, WarmMs: 0}))
	v := e.tunnelView(40)
	if v == nil || v.Status != "faster" || len(v.Hops) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if v.SavedMs != 152 || v.HitPct != 96 || v.ColdMs != 62 || v.ExtraMs != 2 {
		t.Fatalf("numbers = saved %v hit %v cold %v extra %v", v.SavedMs, v.HitPct, v.ColdMs, v.ExtraMs)
	}
	if v.Hops[0].From != "Mkcloud 沪港ixp 新春版" || v.Hops[0].To != "Bestvm HK 年付" || v.Hops[1].To != "Bestvm JP B" {
		t.Fatalf("hop names %+v", v.Hops)
	}

	// Red: pooled conns dead on use cost more than the cold dial saves.
	clock = clock.Add(2 * time.Hour) // old samples leave the window
	m.prune()
	if v := e.tunnelView(40); v != nil {
		t.Fatalf("view from samples older than 1 h: %+v", v)
	}
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true,
		ColdN: 10, ColdMs: 100, WarmN: 20, WarmMs: 440, DeadN: 6}))
	v = e.tunnelView(40)
	if v == nil || v.Status != "slower" || v.SavedMs != -12 || v.Reason != "预热连接常被对方断开" {
		t.Fatalf("red view = %+v", v)
	}

	// Fewer than 10 warm streams: no number — plan 249: 「预热中 · 数据不足」
	// with the counts so far.
	clock = clock.Add(2 * time.Hour)
	m.prune()
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, ColdN: 4, ColdMs: 200, WarmN: 9}))
	if v := e.tunnelView(40); v == nil || v.Status != "warming" || v.WarmN != 9 || v.Streams != 9 || v.MinWarm != 10 || v.Hops[0].SavedMs != nil {
		t.Fatalf("view with 9 warm streams: %+v", v)
	}
	// Switch off → no tag even with samples.
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, ColdN: 4, ColdMs: 200, WarmN: 30}))
	if v := e.tunnelView(40); v == nil {
		t.Fatal("no view with 39 warm streams")
	}
	_ = e.r.SetTunnelWarmPoolTx(nil, 40, 0)
	if v := e.tunnelView(40); v != nil {
		t.Fatalf("view with the switch off: %+v", v)
	}
	_ = e.r.SetTunnelWarmPoolTx(nil, 40, 1)

	// 端口转发: the rule's target closes idle conns young → grey.
	code, msg, data := e.call(e.h.forwardCreate, "1", 0, `{"name":"日本落地","tunnelId":41,"remoteAddr":"198.51.100.8:8388"}`)
	if code != 0 {
		t.Fatalf("create port rule: %d %s", code, msg)
	}
	var r41 struct{ ID int64 }
	_ = json.Unmarshal(data, &r41)
	owner := fmt.Sprintf("%d_1_0_tcp", r41.ID)
	m.ingest(1, warmReport(
		ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, ColdN: 4, ColdMs: 200, WarmN: 30},
		ws.WarmPoolKeyReport{Kind: "s", Owner: owner, Addr: "198.51.100.8:8388", ColdN: 6, ColdMs: 180, PeerN: 6, YoungN: 6, MissN: 3, MissMs: 90, State: "unsupported"}))
	v = e.tunnelView(41)
	if v == nil || v.Status != "unsupported" || len(v.Hops) != 1 || !strings.HasPrefix(v.Hops[0].From, "日本落地") {
		t.Fatalf("port view = %+v", v)
	}
	// A rule whose pool goes (paused / deleted) leaves no tag.
	if code, _, _ := e.call(e.h.forwardDelete, "1", 0, fmt.Sprintf(`{"id":%d}`, r41.ID)); code != 0 {
		t.Fatal("delete")
	}
	if v := e.tunnelView(41); v != nil {
		t.Fatalf("view without active rules: %+v", v)
	}
}

func TestWarmStatsMemoryBounded(t *testing.T) {
	m := newWarmPoolManager()
	clock := time.Now()
	m.now = func() time.Time { return clock }
	for minute := 0; minute < 180; minute++ {
		clock = clock.Add(time.Minute)
		keys := make([]ws.WarmPoolKeyReport, 0, 50)
		for k := 0; k < 50; k++ {
			keys = append(keys, ws.WarmPoolKeyReport{Kind: "c", Owner: fmt.Sprintf("chains_%d", k), Addr: "192.0.2.9:1", ColdN: 2, ColdMs: 100, WarmN: 3})
		}
		m.ingest(7, warmReport(keys...))
		m.prune()
	}
	if n := len(m.stats); n != 50 {
		t.Fatalf("%d entries, want 50", n)
	}
	s := m.stats[warmStatKey{NodeID: 7, Kind: "c", Owner: "chains_1", Addr: "192.0.2.9:1"}].sums(clock)
	if s.warmN < 55*3 || s.warmN > 60*3 {
		t.Fatalf("window holds %d warm streams, want ~1 h worth", s.warmN)
	}
}
