package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// Plan 249: idle sleep in the views, 「预热中 · 数据不足」 (warming), and the
// 预热 tag per rule on the forward list (admin full, user green only).

func entryKey(addr string, k ws.WarmPoolKeyReport) ws.WarmPoolKeyReport {
	k.Kind, k.Owner, k.Addr, k.Target = "c", "chains_40", addr, true
	return k
}

func TestWarmPoolViewSleepAndWarming(t *testing.T) {
	e := newWarmEnv(t, "sqlite")
	m := e.h.warmPools()
	clock := time.Now()
	m.now = func() time.Time { return clock }
	if code, msg, _ := e.call(e.h.forwardCreate, "1", 0, `{"name":"r40","tunnelId":40,"remoteAddr":"198.51.100.7:443"}`); code != 0 {
		t.Fatalf("create rule: %d %s", code, msg)
	}
	next := func() { clock = clock.Add(2 * time.Hour); m.prune() }

	// Pool warm but no stream yet (refills only): nothing.
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{Idle: 2, ColdN: 4, ColdMs: 200})))
	if v := e.tunnelView(40); v != nil {
		t.Fatalf("view without streams: %+v", v)
	}
	// Streams, < 10 warm: warming with the counts (3 warm + 2 cold streams).
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{Idle: 2, ColdN: 2, ColdMs: 100, MissN: 2, MissMs: 100, WarmN: 3})))
	v := e.tunnelView(40)
	if v == nil || v.Status != "warming" || v.Streams != 5 || v.WarmN != 3 || v.MinWarm != 10 {
		t.Fatalf("warming view = %+v", v)
	}
	if len(v.Hops) != 1 || v.Hops[0].Streams != 5 || v.Hops[0].WarmN != 3 || v.Hops[0].Enough || v.Hops[0].Sleep {
		t.Fatalf("warming hop = %+v", v.Hops)
	}
	// The key falls asleep (no stream for 30 min): nothing.
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{Sleep: true})))
	if v := e.tunnelView(40); v != nil {
		t.Fatalf("view of a sleeping key: %+v", v)
	}
	// Woken (next stream): warming again from the same window.
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{MissN: 1, MissMs: 50})))
	if v := e.tunnelView(40); v == nil || v.Status != "warming" || v.Streams != 6 {
		t.Fatalf("woken view = %+v", v)
	}

	// Enough data, then asleep: nothing; awake: the number as before.
	next()
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{ColdN: 10, ColdMs: 600, WarmN: 40})))
	if v := e.tunnelView(40); v == nil || v.Status != "faster" || v.SavedMs != 60 {
		t.Fatalf("enough data view = %+v", v)
	}
	m.ingest(1, warmReport(entryKey("192.0.2.2:2040", ws.WarmPoolKeyReport{Sleep: true})))
	if v := e.tunnelView(40); v != nil {
		t.Fatalf("sleeping key with enough data still shown: %+v", v)
	}
	// The pool goes (key no longer reported) → not "asleep" any more.
	m.ingest(1, warmReport())
	if st := m.stats[warmStatKey{NodeID: 1, Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040"}]; st == nil || st.sleep || st.target {
		t.Fatalf("unreported key state = %+v", st)
	}

	// Old agent (fork.129): its report has no sleep field → awake.
	next()
	var old ws.WarmPoolReport
	raw := `{"cap":64,"live":2,"keys":[{"kind":"c","owner":"chains_40","addr":"192.0.2.2:2040","target":true,"idle":2,"coldN":3,"coldMs":150,"missN":1,"missMs":50,"warmN":4}]}`
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		t.Fatal(err)
	}
	m.ingest(1, &old)
	if v := e.tunnelView(40); v == nil || v.Status != "warming" || v.Streams != 5 {
		t.Fatalf("old agent view = %+v", v)
	}
	// A middle that sleeps alone does not hide the entry's path.
	m.ingest(2, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.3:3040", Target: true, Sleep: true}))
	if v := e.tunnelView(40); v == nil || v.Status != "warming" || len(v.Hops) != 2 || !v.Hops[1].Sleep {
		t.Fatalf("view with a sleeping middle = %+v", v)
	}
}

func TestSummarizeWarmPathsSkipsSleepingPaths(t *testing.T) {
	saved := 50.0
	enoughAsleep := warmPoolHopView{WarmN: 40, Streams: 40, ColdMs: 50, SavedMs: &saved, Enough: true, Sleep: true}
	fewAwake := warmPoolHopView{WarmN: 2, Streams: 3, ColdMs: 50}
	// One entry slept with enough data, the other is awake with few streams.
	v := summarizeWarmPaths("tunnel", [][]warmPoolHopView{{enoughAsleep}, {fewAwake}}, []int64{40, 2})
	if v == nil || v.Status != "warming" || v.Streams != 3 || v.WarmN != 2 || len(v.Hops) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if v := summarizeWarmPaths("tunnel", [][]warmPoolHopView{{enoughAsleep}}, []int64{40}); v != nil {
		t.Fatalf("all asleep → %+v", v)
	}
	// unsupported stays as before (awake key).
	if v := summarizeWarmPaths("forward", [][]warmPoolHopView{{{State: "unsupported", Streams: 3}}}, []int64{0}); v == nil || v.Status != "unsupported" {
		t.Fatalf("unsupported → %+v", v)
	}
	if v := summarizeWarmPaths("forward", [][]warmPoolHopView{{{State: "unsupported", Sleep: true}}}, []int64{0}); v != nil {
		t.Fatalf("unsupported but asleep → %+v", v)
	}
}

// warmRuleEnv: tunnel 42 = entries 1 「Mkcloud 沪港ixp 新春版」 and 4
// 「Mkcloud 沪日ixp 440」 → middle 2 → exit 3, plus the port tunnel 41.
func warmRuleEnv(t *testing.T, dialect string) *warmEnv {
	e := newWarmEnv(t, dialect)
	e.create(&model.Node{ID: 4, Name: "Mkcloud 沪日ixp 440", Secret: "s4", ServerIP: "192.0.2.4",
		ServerIPV4: sql.NullString{String: "192.0.2.4", Valid: true}, Port: "10000-20000", TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: e.now, Status: 1})
	e.create(&model.Tunnel{ID: 42, Name: "{入口} → 日本", TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 1, CreatedTime: e.now, UpdatedTime: e.now, Status: 1,
		InIP: sql.NullString{String: "192.0.2.1,192.0.2.4", Valid: true}})
	for i, id := range []int64{1, 4} {
		e.create(&model.ChainTunnel{TunnelID: 42, ChainType: "1", NodeID: id, Inx: nullInt(int64(i + 1)), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	}
	e.create(&model.ChainTunnel{TunnelID: 42, ChainType: "2", NodeID: 2, Port: nullInt(2042), Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	e.create(&model.ChainTunnel{TunnelID: 42, ChainType: "3", NodeID: 3, Port: nullInt(3042), Inx: nullInt(1), Strategy: sql.NullString{String: "fifo", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	return e
}

func ruleItem(id, tunnelID int64) map[string]interface{} {
	return map[string]interface{}{"id": id, "tunnelId": tunnelID, "status": 1, "mode": "", "userId": int64(7), "remoteAddr": "198.51.100.7:443"}
}

func TestForwardWarmPoolRuleView(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := warmRuleEnv(t, dialect)
			m := e.h.warmPools()
			clock := time.Now()
			m.now = func() time.Time { return clock }
			// Entry 1 (沪港): fast and busy (−60 ms); entry 4 (440): only 3
			// warm streams; middle → exit −90 ms.
			m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.2:2042", Target: true, Idle: 2, ColdN: 10, ColdMs: 600, WarmN: 50}))
			m.ingest(4, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.2:2042", Target: true, Idle: 2, ColdN: 4, ColdMs: 400, MissN: 1, MissMs: 100, WarmN: 3}))
			m.ingest(2, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.3:3042", Target: true, Idle: 2, ColdN: 10, ColdMs: 900, WarmN: 53}))
			// Port rule 900 on entry 1: target closes idle conns young (grey);
			// port rule 901: −30 ms.
			m.ingest(1, warmReport(
				ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.2:2042", Target: true},
				ws.WarmPoolKeyReport{Kind: "s", Owner: "900_7_70_tcp", Addr: "198.51.100.8:8388", Target: true, ColdN: 6, ColdMs: 180, MissN: 3, MissMs: 90, State: "unsupported"},
				ws.WarmPoolKeyReport{Kind: "s", Owner: "901_7_70_tcp", Addr: "198.51.100.9:443", Target: true, ColdN: 10, ColdMs: 300, WarmN: 20}))

			unbound, bound440, bound1, port900, port901 := int64(800), int64(801), int64(802), int64(900), int64(901)
			build := func() []map[string]interface{} {
				return []map[string]interface{}{ruleItem(unbound, 42), ruleItem(bound440, 42), ruleItem(bound1, 42), ruleItem(port900, 41), ruleItem(port901, 41)}
			}
			entries := map[int64][]int64{unbound: {1, 4}, bound440: {4}, bound1: {1}, port900: {1}, port901: {1}}
			view := func(items []map[string]interface{}, id int64) interface{} {
				for _, it := range items {
					if asInt64(it["id"], 0) == id {
						return it["warmPoolState"]
					}
				}
				return nil
			}

			// Admin: every state, with the per-hop detail.
			items := build()
			e.h.attachForwardWarmPoolStates(items, entries, true)
			v, _ := view(items, unbound).(*warmPoolView)
			// unbound = the tunnel's weighted value over both entries: entry 1
			// path −60 −90 = −150 (50 warm streams); entry 4's own hop has < 10
			// warm streams, so its path counts only the shared middle hop −90
			// (3 warm streams): (150×50 + 90×3) / 53 = 146.6.
			if v == nil || v.Status != "faster" || v.SavedMs != 146.6 || v.Kind != "tunnel" {
				t.Fatalf("unbound rule view = %+v", v)
			}
			v, _ = view(items, bound440).(*warmPoolView)
			// Entry-bound to 440: its own path only — the entry hop has too few
			// samples, the shared middle hop counts (as on the tunnel page).
			if v == nil || v.Status != "faster" || v.SavedMs != 90 || len(v.Hops) != 2 || v.Hops[0].From != "Mkcloud 沪日ixp 440" || v.Hops[0].Enough {
				t.Fatalf("440-bound rule view = %+v", v)
			}
			v, _ = view(items, bound1).(*warmPoolView)
			if v == nil || v.Status != "faster" || v.SavedMs != 150 || v.Hops[0].From != "Mkcloud 沪港ixp 新春版" || v.Hops[1].To != "Bestvm JP B" {
				t.Fatalf("沪港-bound rule view = %+v", v)
			}
			v, _ = view(items, port900).(*warmPoolView)
			if v == nil || v.Status != "unsupported" || v.Kind != "forward" || v.Hops[0].To != "198.51.100.8:8388" {
				t.Fatalf("port rule 900 view = %+v", v)
			}
			v, _ = view(items, port901).(*warmPoolView)
			if v == nil || v.Status != "faster" || v.SavedMs != 30 || len(v.Hops) != 1 {
				t.Fatalf("port rule 901 view = %+v", v)
			}

			// User: only green, and only status + ms (no hops, names, targets, counts).
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, false)
			for _, id := range []int64{port900} {
				if got := view(items, id); got != nil {
					t.Fatalf("user sees non-green tag on %d: %+v", id, got)
				}
			}
			for _, id := range []int64{unbound, bound440, bound1, port901} {
				uv, ok := view(items, id).(warmPoolUserView)
				if !ok || uv.Status != "faster" || len(uv.Hops) != 0 {
					t.Fatalf("user view of %d = %#v", id, view(items, id))
				}
				raw, _ := json.Marshal(uv)
				for _, leak := range []string{"Mkcloud", "Bestvm", "198.51.100", "192.0.2", "warmN", "streams", "coldMs", "hitPct", "NodeId"} {
					if strings.Contains(string(raw), leak) {
						t.Fatalf("user view of %d leaks %q: %s", id, leak, raw)
					}
				}
			}

			// A sleeping entry: nothing for its rules; the other entry's rules unchanged.
			m.ingest(4, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.2:2042", Target: true, Sleep: true}))
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, true)
			if got := view(items, bound440); got != nil {
				t.Fatalf("rule on a sleeping entry: %+v", got)
			}
			if v, _ := view(items, unbound).(*warmPoolView); v == nil || v.SavedMs != 150 { // 440's path asleep → not counted
				t.Fatalf("unbound rule after 440 slept = %+v", v)
			}
			// Paused / nft rules, switch off: nothing.
			items = build()
			items[0]["status"] = 0
			items[2]["mode"] = "nftables"
			e.h.attachForwardWarmPoolStates(items, entries, true)
			if view(items, unbound) != nil || view(items, bound1) != nil {
				t.Fatal("paused / nftables rule got a tag")
			}
			_ = e.r.SetTunnelWarmPoolTx(nil, 42, 0)
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, true)
			if view(items, bound1) != nil || view(items, port901) == nil {
				t.Fatal("switch off must hide only tunnel 42's tags")
			}
			// Entry-bound rule with < 10 warm streams on every hop of its path:
			// admins see 数据不足, the user nothing.
			_ = e.r.SetTunnelWarmPoolTx(nil, 42, 1)
			e.h.warm = newWarmPoolManager()
			m = e.h.warm
			m.ingest(4, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.2:2042", Target: true, ColdN: 4, ColdMs: 400, MissN: 1, MissMs: 100, WarmN: 3}))
			m.ingest(2, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_42", Addr: "192.0.2.3:3042", Target: true, ColdN: 4, ColdMs: 360, WarmN: 3}))
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, true)
			if v, _ := view(items, bound440).(*warmPoolView); v == nil || v.Status != "warming" || v.Streams != 4 || v.WarmN != 3 {
				t.Fatalf("440-bound warming view = %+v", v)
			}
			if got := view(items, bound1); got != nil {
				t.Fatalf("rule on an entry without a pool report: %+v", got)
			}
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, false)
			if got := view(items, bound440); got != nil {
				t.Fatalf("user sees 数据不足: %+v", got)
			}

			// No pool reported at all: no tag, no DB query needed.
			e.h.warm = newWarmPoolManager()
			items = build()
			e.h.attachForwardWarmPoolStates(items, entries, true)
			for _, it := range items {
				if _, ok := it["warmPoolState"]; ok {
					t.Fatalf("tag without any pool report: %+v", it)
				}
			}
		})
	}
}

// The real forward list (SQLite: ListForwards joins the unquoted table
// `user`, which PostgreSQL rejects — pre-existing, see forwardItem).
func TestForwardListCarriesWarmTag(t *testing.T) {
	e := newWarmEnv(t, "sqlite")
	m := e.h.warmPools()
	code, msg, data := e.call(e.h.forwardCreate, "1", 0, `{"name":"日本落地","tunnelId":41,"remoteAddr":"198.51.100.8:8388"}`)
	if code != 0 {
		t.Fatalf("create port rule: %d %s", code, msg)
	}
	var r41 struct{ ID int64 }
	_ = json.Unmarshal(data, &r41)
	owner := fmt.Sprintf("%d_1_0_tcp", r41.ID)
	e.h.wsServer = ws.NewServer(e.r, "test") // the rule list reads live metrics
	defer func() { e.h.wsServer = nil }()
	list := func(role int) map[string]json.RawMessage {
		t.Helper()
		code, msg, data := e.call(e.h.forwardList, "1", role, `{}`)
		if code != 0 {
			t.Fatalf("list: %d %s", code, msg)
		}
		var out struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		for _, it := range out.Items {
			var id int64
			_ = json.Unmarshal(it["id"], &id)
			if id == r41.ID {
				return it
			}
		}
		t.Fatalf("rule %d not listed", r41.ID)
		return nil
	}
	// 数据不足: admins see it, the rule's owner (role 1) does not.
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "s", Owner: owner, Addr: "198.51.100.8:8388", Target: true, ColdN: 3, ColdMs: 90, MissN: 1, MissMs: 30, WarmN: 4}))
	if raw := list(0)["warmPoolState"]; !strings.Contains(string(raw), `"status":"warming"`) {
		t.Fatalf("admin list warm tag = %s", raw)
	}
	if raw, ok := list(1)["warmPoolState"]; ok {
		t.Fatalf("user list shows 数据不足: %s", raw)
	}
	// Green: both, the user without detail.
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "s", Owner: owner, Addr: "198.51.100.8:8388", Target: true, ColdN: 10, ColdMs: 300, WarmN: 20}))
	if raw := list(0)["warmPoolState"]; !strings.Contains(string(raw), `"status":"faster"`) || !strings.Contains(string(raw), "198.51.100.8") {
		t.Fatalf("admin list green tag = %s", raw)
	}
	raw := list(1)["warmPoolState"]
	if !strings.Contains(string(raw), `"status":"faster"`) || strings.Contains(string(raw), "198.51.100.8") || strings.Contains(string(raw), "Mkcloud") {
		t.Fatalf("user list green tag = %s", raw)
	}
}
