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

// Plan 182 on a real repository (SQLite and PostgreSQL) through the rule API:
// tunnels 30 and 31 are entry 1 → 最优 exits {2, 3} with alternating latency;
// 31 has a rule from the start, 30 none. Only 31 switches. Creating a rule on
// 30 pushes its best order before the rule's own services; deleting it stops
// switching without any extra push.
func newIdleBestEnv(t *testing.T, dialect string) *bindEnv {
	t.Helper()
	r := sharedLandingTestRepo(t, dialect)
	e := &bindEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}
	e.h.nodeCommandSender = func(id int64, command string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		raw, _ := json.Marshal(data)
		e.mu.Lock()
		e.cmds = append(e.cmds, bindCmd{id, command, string(raw)})
		e.mu.Unlock()
		return ws.CommandResult{Success: true}, nil
	}
	e.h.bestPath = newBestPathManager()
	e.h.ruleBest = newRuleBestManager()
	e.h.chainSwapCapableFn = func(int64) bool { return true }
	e.h.nodeOnlineFn = func(int64) bool { return true }
	e.h.destOrderCapableFn = func(int64) bool { return true }
	for id := int64(1); id <= 3; id++ {
		e.create(&model.Node{ID: id, Name: fmt.Sprintf("node-%d", id), Secret: fmt.Sprintf("s%d", id), ServerIP: fmt.Sprintf("192.0.2.%d", id),
			ServerIPV4: sql.NullString{String: fmt.Sprintf("192.0.2.%d", id), Valid: true}, Port: "10000-20000", TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: e.now, Status: 1})
	}
	for _, tid := range []int64{30, 31} {
		e.create(&model.Tunnel{ID: tid, Name: fmt.Sprintf("best-%d", tid), TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 1, CreatedTime: e.now, UpdatedTime: e.now, Status: 1,
			InIP: sql.NullString{String: "192.0.2.1", Valid: true}})
		e.create(&model.ChainTunnel{TunnelID: tid, ChainType: "1", NodeID: 1, Inx: nullInt(1), Strategy: sql.NullString{String: "round", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
		e.create(&model.ChainTunnel{TunnelID: tid, ChainType: "3", NodeID: 2, Port: nullInt(2000 + tid), Inx: nullInt(1), Strategy: sql.NullString{String: "best", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
		e.create(&model.ChainTunnel{TunnelID: tid, ChainType: "3", NodeID: 3, Port: nullInt(3000 + tid), Inx: nullInt(2), Strategy: sql.NullString{String: "best", Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	}
	return e
}

// round runs one path-latency decision step as runPathLatencyRound does after
// the probes (idle marking from the DB, then 最优 and 规则最优).
func (e *bindEnv) bestRound(lat map[[2]int64]float64, at time.Time) {
	e.t.Helper()
	plans, _, _, err := e.h.planPathLatencyRound()
	if err != nil {
		e.t.Fatal(err)
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
	e.h.markIdleTunnels(plans)
	guard := newBestPathPushGuard(time.Now)
	e.h.runBestPathRound(plans, results, guard, at)
	e.h.runRuleBestRound(plans, results, guard, at, at)
}

func idleAlternatingLat(round int) map[[2]int64]float64 {
	if (round/2)%2 == 1 {
		return map[[2]int64]float64{{1, 2}: 32, {1, 3}: 20}
	}
	return map[[2]int64]float64{{1, 2}: 20, {1, 3}: 32}
}

// chainPushes counts UpdateChains per tunnel chain name since index from.
func (e *bindEnv) chainPushes(from int) map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{}
	for _, c := range e.cmds[from:] {
		if c.command != "UpdateChains" {
			continue
		}
		var env struct {
			Chain string `json:"chain"`
		}
		_ = json.Unmarshal([]byte(c.payload), &env)
		out[env.Chain]++
	}
	return out
}

func (e *bindEnv) cmdCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cmds)
}

func TestBestPathIdleTunnelRuleTransitions(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newIdleBestEnv(t, dialect)
			body := func(tid int64) string {
				return fmt.Sprintf(`{"name":"r%d","tunnelId":%d,"remoteAddr":"198.51.100.7:443"}`, tid, tid)
			}
			if code, msg, _ := e.call(e.h.forwardCreate, "1", 0, body(31)); code != 0 {
				t.Fatalf("create rule on 31: %d %s", code, msg)
			}
			now := time.Unix(1_800_000_000, 0)
			mark := e.cmdCount()
			for r := 0; r < 12; r++ {
				e.bestRound(idleAlternatingLat(r), now.Add(time.Duration(r)*time.Minute))
			}
			pushes := e.chainPushes(mark)
			if pushes["chains_30"] != 0 {
				t.Fatalf("tunnel 30 without rules got %d pushes", pushes["chains_30"])
			}
			if pushes["chains_31"] < 4 {
				t.Fatalf("tunnel 31 with a rule switched only %d times: %v", pushes["chains_31"], pushes)
			}
			// Rounds 10/11: exit 2 (20 ms) is best; tunnel 30 still runs DB order
			// 2 first. Make 3 best for one round only — no in-use tunnel would
			// switch on one round — then create the first rule on 30.
			e.bestRound(map[[2]int64]float64{{1, 2}: 23, {1, 3}: 20}, now.Add(12*time.Minute))
			mark = e.cmdCount()
			code, msg, data := e.call(e.h.forwardCreate, "1", 0, body(30))
			if code != 0 {
				t.Fatalf("create rule on 30: %d %s", code, msg)
			}
			var created struct{ ID int64 }
			_ = json.Unmarshal(data, &created)
			e.mu.Lock()
			cmds := append([]bindCmd(nil), e.cmds[mark:]...)
			e.mu.Unlock()
			first := -1
			firstSvc := -1
			for i, c := range cmds {
				if c.command == "UpdateChains" && strings.Contains(c.payload, `"chains_30"`) && first < 0 {
					first = i
					if !strings.Contains(c.payload, "192.0.2.3:3030") || strings.Index(c.payload, "192.0.2.3:3030") > strings.Index(c.payload, "192.0.2.2:2030") {
						t.Fatalf("first-rule push is not best-first: %s", c.payload)
					}
				}
				if strings.Contains(c.payload, fmt.Sprintf(`"%d_`, created.ID)) && firstSvc < 0 {
					firstSvc = i
				}
			}
			if first < 0 || firstSvc < 0 || first > firstSvc {
				t.Fatalf("best order must reach the entry before the rule's services (chain at %d, service at %d): %+v", first, firstSvc, cmds)
			}
			if n := e.chainPushes(mark)["chains_30"]; n != 1 {
				t.Fatalf("first rule pushed chains_30 %d times", n)
			}
			// A second rule on 30: no activation push.
			mark = e.cmdCount()
			if code, msg, _ := e.call(e.h.forwardCreate, "1", 0, `{"name":"r30b","tunnelId":30,"remoteAddr":"198.51.100.8:443"}`); code != 0 {
				t.Fatalf("second rule: %d %s", code, msg)
			}
			if n := e.chainPushes(mark)["chains_30"]; n != 0 {
				t.Fatalf("second rule pushed chains_30 %d times", n)
			}
			// Delete both rules on 30: no push at delete, none in later rounds
			// even when the latency flips.
			mark = e.cmdCount()
			ids, err := e.r.ListTunnelIDsWithForwards()
			if err != nil || len(ids) != 2 {
				t.Fatalf("in-use tunnels %v %v", ids, err)
			}
			fwds, err := e.r.ListForwardsByTunnel(30)
			if err != nil || len(fwds) != 2 {
				t.Fatalf("forwards on 30: %v %v", fwds, err)
			}
			for _, f := range fwds {
				if code, msg, _ := e.call(e.h.forwardDelete, "1", 0, fmt.Sprintf(`{"id":%d}`, f.ID)); code != 0 {
					t.Fatalf("delete %d: %d %s", f.ID, code, msg)
				}
			}
			for r := 13; r < 20; r++ {
				e.bestRound(idleAlternatingLat(r), now.Add(time.Duration(r)*time.Minute))
			}
			if n := e.chainPushes(mark)["chains_30"]; n != 0 {
				t.Fatalf("tunnel 30 after its last rule got %d pushes", n)
			}
			if ids, _ := e.r.ListTunnelIDsWithForwards(); len(ids) != 1 || ids[0] != 31 {
				t.Fatalf("in-use after delete %v", ids)
			}
		})
	}
}
