package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// Plan 264 「冗余」 (panel side): the payload by agent version, the machine
// budget, and the new hop-view fields.

// lastWarmRaw: the raw JSON of the last SetWarmPools each node got since mark.
func (e *warmEnv) lastWarmRaw(mark int) map[int64]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int64]string{}
	for _, c := range e.cmds[mark:] {
		if c.command == "SetWarmPools" {
			out[c.node] = c.payload
		}
	}
	return out
}

func TestWarmPoolPayloadByAgentVersion(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newWarmEnv(t, dialect)
			e.cmu.Lock()
			e.versions[1] = 1 // entry still on fork.135 (warmPool=1)
			e.versions[2] = 2 // middle already on the plan-264 agent
			e.cmu.Unlock()
			e.reconcile()
			code, msg, data := e.call(e.h.forwardCreate, "1", 0, `{"name":"r40","tunnelId":40,"remoteAddr":"198.51.100.7:443"}`)
			if code != 0 {
				t.Fatalf("create rule: %d %s", code, msg)
			}
			code, msg, data = e.call(e.h.forwardCreate, "1", 0, `{"name":"r41","tunnelId":41,"remoteAddr":"198.51.100.8:8388"}`)
			if code != 0 {
				t.Fatalf("create port rule: %d %s", code, msg)
			}
			var r41 struct{ ID int64 }
			_ = json.Unmarshal(data, &r41)
			mark := e.cmdCount()
			pushed, st := e.reconcile()
			if fmt.Sprint(pushed) != "[1 2]" {
				t.Fatalf("pushed %v", pushed)
			}
			raw := e.lastWarmRaw(mark)
			// Old agent: floor clamped to its max 4, no new fields at all.
			if got := chainsOf(st[1]); got != fmt.Sprintf("chains_40=4,f%d=4", r41.ID) {
				t.Fatalf("v1 payload sizes %s", got)
			}
			for _, f := range []string{"ceil", "idleSleepS", "cap"} {
				if strings.Contains(raw[1], f) {
					t.Fatalf("v1 agent got %q: %s", f, raw[1])
				}
			}
			// New agent: floor 6, ceiling 16, idle sleep 6 h, no cap (1000/5 ≥ 192).
			if got := chainsOf(st[2]); got != "chains_40=6" || st[2].Ceil != 16 || st[2].IdleSleepS != 21600 || st[2].Cap != 0 {
				t.Fatalf("v2 payload %s / %+v", got, st[2])
			}
			if strings.Contains(raw[2], `"cap"`) {
				t.Fatalf("cap sent without a low machine limit: %s", raw[2])
			}

			// The entry is upgraded (reconnects with warmPool=2): full state in
			// the new shape at once.
			e.cmu.Lock()
			e.versions[1] = 2
			e.cmu.Unlock()
			e.h.warmPoolNodeSession(1)
			pushed, st = e.reconcile()
			if fmt.Sprint(pushed) != "[1]" || chainsOf(st[1]) != fmt.Sprintf("chains_40=6,f%d=6", r41.ID) || st[1].Ceil != 16 || st[1].IdleSleepS != 21600 {
				t.Fatalf("after the entry upgrade: pushed %v %+v", pushed, st[1])
			}

			// A node with a low 整机连接上限 (300) gets a pooled-conn budget of
			// 1/5 (60); protection off → none.
			if err := e.r.DB().Model(&model.Node{}).Where("id = ?", 2).Update("machine_max_total", 300).Error; err != nil {
				t.Fatal(err)
			}
			pushed, st = e.reconcile()
			if fmt.Sprint(pushed) != "[2]" || st[2].Cap != 60 {
				t.Fatalf("budget push: %v %+v", pushed, st[2])
			}
			if err := e.r.DB().Model(&model.Node{}).Where("id = ?", 2).Update("machine_protect_enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			if pushed, st = e.reconcile(); fmt.Sprint(pushed) != "[2]" || st[2].Cap != 0 {
				t.Fatalf("protection off: %v %+v", pushed, st[2])
			}
			// Nothing changed → nothing pushed.
			if pushed, _ = e.reconcile(); len(pushed) != 0 {
				t.Fatalf("unchanged state pushed to %v", pushed)
			}
		})
	}
}

func TestWarmPoolMachineCap(t *testing.T) {
	for _, c := range []struct {
		max  int64
		want int
	}{{0, 0}, {1000, 0}, {960, 0}, {955, 191}, {500, 100}, {300, 60}, {100, 20}, {20, 8}} {
		if got := warmPoolMachineCap(c.max); got != c.want {
			t.Errorf("warmPoolMachineCap(%d) = %d, want %d", c.max, got, c.want)
		}
	}
}

func TestWarmPoolViewBurstAndWakeFields(t *testing.T) {
	e := newWarmEnv(t, "sqlite")
	m := e.h.warmPools()
	clock := time.Now()
	m.now = func() time.Time { return clock }
	if code, msg, _ := e.call(e.h.forwardCreate, "1", 0, `{"name":"r40","tunnelId":40,"remoteAddr":"198.51.100.7:443"}`); code != 0 {
		t.Fatalf("create rule: %d %s", code, msg)
	}
	// Entry on a plan-264 agent: 10 misses of which 6 right after a wake.
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, Idle: 9,
		ColdN: 30, ColdMs: 1800, MissN: 10, MissMs: 600, WarmN: 90, WakeN: 6, Want: 11, Peak5s: 6}))
	// Middle on an old agent: no new fields.
	m.ingest(2, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.3:3040", Target: true, Idle: 2,
		ColdN: 10, ColdMs: 920, MissN: 3, MissMs: 276, WarmN: 97}))
	// A minute later, more from the entry (buckets add up; want/peak = latest).
	clock = clock.Add(time.Minute)
	m.ingest(1, warmReport(ws.WarmPoolKeyReport{Kind: "c", Owner: "chains_40", Addr: "192.0.2.2:2040", Target: true, Idle: 12,
		ColdN: 2, ColdMs: 120, MissN: 2, MissMs: 120, WarmN: 40, Want: 12, Peak5s: 7}))
	v := e.tunnelView(40)
	if v == nil || len(v.Hops) != 2 {
		t.Fatalf("view = %+v", v)
	}
	h0, h1 := v.Hops[0], v.Hops[1]
	if h0.Streams != 142 || h0.WakeN != 6 || h0.BurstMissN != 6 || h0.Want != 12 || h0.Peak5s != 7 {
		t.Fatalf("entry hop %+v", h0)
	}
	if h1.WakeN != 0 || h1.BurstMissN != 3 || h1.Want != 0 || h1.Peak5s != 0 {
		t.Fatalf("old-agent hop %+v", h1)
	}
	raw, _ := json.Marshal(h1)
	if !strings.Contains(string(raw), `"burstMissN":3`) || !strings.Contains(string(raw), `"wakeN":0`) || strings.Contains(string(raw), "want") {
		t.Fatalf("old-agent hop JSON %s", raw)
	}
	// hitPct stays "all misses".
	if h0.HitPct != 92 { // 130 / 142
		t.Fatalf("hitPct %d", h0.HitPct)
	}
	// The key goes away (rule paused): want / peak reset with the pool.
	m.ingest(1, warmReport())
	if v := e.tunnelView(40); v != nil && v.Hops[0].Want != 0 {
		t.Fatalf("want kept after the key vanished: %+v", v.Hops[0])
	}
}
