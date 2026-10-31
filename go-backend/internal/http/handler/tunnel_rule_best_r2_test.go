package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/ws"
)

// Plan 180 round 2 (user 11-01): a destination unreachable from every exit
// is re-probed only at +1/+3/+10/+60 min; a retry that answers is confirmed
// by the next round; 1 h reachable resets the schedule; all retries failed ⇒
// 「规则最优」 off for its rules (persisted) + admin notice; an admin turns it
// back on.

// rbSchedule drives one destination of a one-tunnel plan through rounds
// every 60 s (the planner's due check, then the round's update) with a fake
// clock; up(minute) says whether the target answers in that round. It
// returns the minutes the target was probed and the minutes it was confirmed
// reachable again, and stops at the first auto-off (minute or -1).
type rbScheduleRun struct {
	probed    []int
	recovered []int
	off       int
}

func rbRunSchedule(t *testing.T, m *ruleBestManager, start time.Time, minutes int, up func(minute int) bool) rbScheduleRun {
	t.Helper()
	key := "dead.example:443"
	edges := map[[2]int64]float64{{1, 4}: 10, {1, 5}: 30}
	run := rbScheduleRun{off: -1}
	for minute := 0; minute <= minutes; minute++ {
		// Rounds drift a little (a few seconds late), as a real ticker does.
		at := start.Add(time.Duration(minute)*time.Minute + time.Duration(minute%3)*time.Second)
		plan := rbPlan(9, [][]int64{{1}, {4, 5}})
		rbDest(&plan, key)
		if !m.probeDue(ruleBestDeadKey{TunnelID: 9, Dest: key}, at) {
			plan.RuleBest.Dests[0].BackedOff = true
			plan.RuleBest.Dests[0].Exits = nil
		} else {
			run.probed = append(run.probed, minute)
		}
		plans := []pathLatencyPlan{plan}
		dests := map[string]map[int64]float64{}
		if up(minute) {
			dests[key] = map[int64]float64{4: 80, 5: 20}
		}
		dead, recovered, offs := m.updateDead(plans, rbResults(plans, edges, dests), at)
		m.evaluate(plans, rbResults(plans, edges, dests), dead, recovered, rbNoTunnelOrder, rbAllCapable, at)
		if recovered[ruleBestDeadKey{TunnelID: 9, Dest: key}] {
			run.recovered = append(run.recovered, minute)
		}
		if len(offs) > 0 {
			if len(offs) != 1 || offs[0].Key != (ruleBestDeadKey{TunnelID: 9, Dest: key}) || offs[0].Label != key {
				t.Fatalf("minute %d: off events %+v", minute, offs)
			}
			run.off = minute
			return run
		}
	}
	return run
}

func TestRuleBestRetryScheduleExactPoints(t *testing.T) {
	m := newRuleBestManager()
	run := rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 90, func(int) bool { return false })
	// Minute 0: the normal round that found it dead; then only the points.
	if want := []int{0, 1, 3, 10, 60}; !reflect.DeepEqual(run.probed, want) {
		t.Fatalf("probed at minutes %v, want %v", run.probed, want)
	}
	if run.off != 60 || len(run.recovered) != 0 {
		t.Fatalf("off at minute %d (want 60), recovered %v", run.off, run.recovered)
	}
	if c := m.takeCounters(); c.Retries != 4 || c.DeadRounds != 1 || c.Dead != 1 {
		t.Fatalf("counters %+v", c)
	}
	// Until the auto-off is written the event repeats (no probe); the write
	// drops the schedule.
	key := ruleBestDeadKey{TunnelID: 9, Dest: "dead.example:443"}
	if m.probeDue(key, time.Unix(1_800_000_000, 0).Add(200*time.Minute)) {
		t.Fatalf("exhausted destination probed again")
	}
	m.forgetDead(key)
	if _, dead, _, _ := m.size(); dead != 0 {
		t.Fatalf("schedule kept after forgetDead")
	}
}

func TestRuleBestRetrySuccessConfirmedThenRuleBestAtOnce(t *testing.T) {
	m := newRuleBestManager()
	// Dead at 0, answers from minute 3 on: the +3 retry answers, minute 4
	// confirms ⇒ reachable again (rule-best at once), no further retries.
	run := rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 30, func(min int) bool { return min >= 3 })
	if want := []int{0, 1, 3, 4, 5, 6}; !reflect.DeepEqual(run.probed[:6], want) {
		t.Fatalf("probed %v, want prefix %v (every round once reachable)", run.probed, want)
	}
	if !reflect.DeepEqual(run.recovered, []int{4}) || run.off != -1 {
		t.Fatalf("recovered %v off %d", run.recovered, run.off)
	}
}

func TestRuleBestRetryConfirmFailsContinuesSchedule(t *testing.T) {
	m := newRuleBestManager()
	// The +1 retry answers, its confirmation (minute 2) fails: the schedule
	// goes on at +3 (not from +1 again), then +10, +60 ⇒ off.
	run := rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 90, func(min int) bool { return min == 1 })
	if want := []int{0, 1, 2, 3, 10, 60}; !reflect.DeepEqual(run.probed, want) {
		t.Fatalf("probed %v, want %v", run.probed, want)
	}
	if run.off != 60 || len(run.recovered) != 0 {
		t.Fatalf("off %d recovered %v", run.off, run.recovered)
	}
	// The last retry answers but its confirmation fails ⇒ off as well.
	m = newRuleBestManager()
	run = rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 90, func(min int) bool { return min == 60 })
	if want := []int{0, 1, 3, 10, 60, 61}; !reflect.DeepEqual(run.probed, want) || run.off != 61 {
		t.Fatalf("probed %v off %d, want %v and off at 61", run.probed, run.off, want)
	}
}

func TestRuleBestFlapWithinHourResumesScheduleAndEndsInOff(t *testing.T) {
	m := newRuleBestManager()
	// Down at 0, back at the +1 retry (confirmed at 2), down again at 20:
	// the old schedule goes on at its next point (+3 counted from 20 = 23),
	// back at 23 (confirmed 24), down at 40 ⇒ +10 from 40 = 50, back at 50
	// (confirmed 51), down at 70 ⇒ +60 from 70 = 130 fails ⇒ off.
	up := func(min int) bool {
		return (min >= 1 && min < 20) || (min >= 23 && min < 40) || (min >= 50 && min < 70)
	}
	run := rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 200, up)
	if !reflect.DeepEqual(run.recovered, []int{2, 24, 51}) {
		t.Fatalf("recovered %v", run.recovered)
	}
	for _, minute := range []int{21, 22, 41, 49, 71, 129} {
		for _, p := range run.probed {
			if p == minute {
				t.Fatalf("probed at %d between retry points (%v)", minute, run.probed)
			}
		}
	}
	if run.off != 130 {
		t.Fatalf("flapping target off at %d, want 130 (probed %v)", run.off, run.probed)
	}
}

func TestRuleBestHealthyHourResetsSchedule(t *testing.T) {
	m := newRuleBestManager()
	// Back at +1 (confirmed 2), reachable until 70 (> 1 h after 2), down at
	// 70: a fresh schedule (+1 = 71), not the old one.
	run := rbRunSchedule(t, m, time.Unix(1_800_000_000, 0), 75, func(min int) bool { return min >= 1 && min < 70 })
	if !reflect.DeepEqual(run.recovered, []int{2}) {
		t.Fatalf("recovered %v", run.recovered)
	}
	got := run.probed[len(run.probed)-3:]
	if !reflect.DeepEqual(got, []int{70, 71, 73}) {
		t.Fatalf("after the healthy hour probed %v, want …70, 71, 73", run.probed)
	}
}

// One exit failing while another reaches the target is normal 最优: no
// schedule, no retry, no notice.
func TestRuleBestOneExitFailingIsNotDead(t *testing.T) {
	key := "half.example:443"
	plan := rbPlan(9, [][]int64{{1}, {4, 5}})
	rbDest(&plan, key)
	plans := []pathLatencyPlan{plan}
	res := rbResults(plans, map[[2]int64]float64{{1, 4}: 10, {1, 5}: 30}, map[string]map[int64]float64{key: {5: 20}})
	m := newRuleBestManager()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 5; i++ {
		dead, _, offs := m.updateDead(plans, res, now.Add(time.Duration(i)*time.Minute))
		if len(dead) != 0 || len(offs) != 0 {
			t.Fatalf("round %d: dead %v offs %v", i, dead, offs)
		}
	}
	if _, dead, _, _ := m.size(); dead != 0 {
		t.Fatalf("schedule for a target one exit reaches")
	}
	// No answer at all (exits offline): nothing changes either.
	plan.RuleBest.Dests[0].Exits = map[int64]pathProbeKey{}
	if dead, _, _ := m.updateDead([]pathLatencyPlan{plan}, nil, now); len(dead) != 0 {
		t.Fatalf("unprobed destination marked dead")
	}
}

// ---- runtime: auto-off persisted, orders removed, probes stop, notice,
// restart, re-enable --------------------------------------------------------

func rbListForwards(t *testing.T, e *flowTestEnv, admin bool) (items []map[string]interface{}, offCount interface{}) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/forward/list", strings.NewReader("{}"))
	claims := auth.Claims{Sub: "1", RoleID: 0}
	if !admin {
		claims = auth.Claims{Sub: "2", RoleID: 1}
	}
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, claims))
	w := httptest.NewRecorder()
	e.h.wsServer = ws.NewServer(e.r, "test") // the rule list reads live metrics
	e.h.forwardList(w, r)
	e.h.wsServer = nil
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items            []map[string]interface{} `json:"items"`
			RuleBestOffCount interface{}              `json:"ruleBestOffCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Code != 0 {
		t.Fatalf("forward list %s: %v", w.Body.String(), err)
	}
	return resp.Data.Items, resp.Data.RuleBestOffCount
}

func TestRuleBestAutoOffRuntime(t *testing.T) {
	e, commands := newRuleBestEnv(t)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	now := time.Unix(1_800_000_000, 0)
	// Healthy: rule-level decisions (b → exit 5 on 中转 3 differs from the
	// tunnel's first; entry 1 picks 中转 3 for b).
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now)
	rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), now.Add(time.Minute))
	if len(e.h.ruleBest.ownerOrders(bestPathKey{20, 3})) == 0 && len(e.h.ruleBest.ownerOrders(bestPathKey{20, 2})) == 0 {
		t.Fatalf("no rule-level decision to remove later")
	}
	// b dies everywhere at minute 2; a (rule 31 only) stays reachable.
	dead := rbEnvDests()
	delete(dead, "b.example:443")
	t0 := now.Add(2 * time.Minute)
	var probedB []int
	offAt := -1
	for minute := 0; minute <= 61; minute++ {
		info := rbEnvRound(t, e, rbEnvEdges(), dead, t0.Add(time.Duration(minute)*time.Minute))
		if info.probes == 4 {
			probedB = append(probedB, minute)
		}
		if mustQueryInt(t, e.r, `SELECT COUNT(*) FROM forward WHERE rule_best_off_sig <> ''`) > 0 {
			offAt = minute
			break
		}
	}
	if want := []int{0, 1, 3, 10, 60}; !reflect.DeepEqual(probedB, want) || offAt != 60 {
		t.Fatalf("b probed at %v (want %v), off at %d (want 60)", probedB, want, offAt)
	}
	// Both rules using b are off (31 also has a: the rule is affected when
	// any of its targets fails all retries); the others are untouched.
	for id, want := range map[int64]int{31: 1, 32: 1, 33: 0, 34: 0} {
		if got := mustQueryInt(t, e.r, `SELECT COUNT(*) FROM forward WHERE id = ? AND rule_best_off_sig <> '' AND rule_best_off_target = 'b.example:443' AND rule_best_off_time > 0`, id); got != want {
			t.Fatalf("rule %d off = %d, want %d", id, got, want)
		}
	}
	if n := strings.Count(logs.String(), "[rule-best] auto-off "); n != 2 {
		t.Fatalf("auto-off log lines = %d, want 2 (one per rule):\n%s", n, logs.String())
	}
	// Next round: 31 and 32 are no longer rule-best — no probes, every
	// per-destination order gone (a's included) by one cleanup push per owner.
	mark := len(commands.snapshot())
	info := rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), t0.Add(61*time.Minute))
	if info.probes != 0 || (info.plans[0].RuleBest != nil && len(info.plans[0].RuleBest.Dests) != 0) {
		t.Fatalf("auto-off rules still probed: %d probes, plan %+v", info.probes, info.plans[0].RuleBest)
	}
	for _, owner := range []int64{1, 2, 3} {
		if o := e.h.ruleBest.ownerOrders(bestPathKey{20, owner}); len(o) != 0 {
			t.Fatalf("owner %d keeps orders %v", owner, o)
		}
	}
	for _, c := range rbChainCommands(commandsSince(commands, mark)) {
		if rbDestOrderOf(t, c.data) != nil {
			t.Fatalf("push after auto-off still carries destOrder: %+v", c)
		}
	}
	if states, deadN, _, _ := e.h.ruleBest.size(); states != 0 || deadN != 0 {
		t.Fatalf("memory after auto-off: states=%d schedules=%d", states, deadN)
	}

	// Admin list: notice on 31 and 32; user list: nothing (tunnel pick only).
	items, count := rbListForwards(t, e, true)
	notices := map[int64]string{}
	for _, item := range items {
		if off, ok := item["ruleBestOff"].(map[string]interface{}); ok {
			notices[asInt64(item["id"], 0)] = asString(off["text"])
		}
	}
	if len(notices) != 2 || asInt64(count, -1) != 2 || !strings.Contains(notices[32], "规则「rule-b.example:443」的落地 b.example:443 从所有出口都连不上（已重试 1/3/10/60 分钟），已自动关闭规则最优，请检查出口或落地") {
		t.Fatalf("admin notices %v count %v", notices, count)
	}
	userItems, userCount := rbListForwards(t, e, false)
	for _, item := range userItems {
		if _, ok := item["ruleBestOff"]; ok {
			t.Fatalf("user sees the notice: %+v", item)
		}
	}
	if userCount != nil {
		t.Fatalf("user got ruleBestOffCount %v", userCount)
	}

	// Panel restart: the OFF state survives (new manager, same DB).
	e.h.ruleBest = newRuleBestManager()
	if info := rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), t0.Add(62*time.Minute)); info.probes != 0 {
		t.Fatalf("after restart the auto-off rules are probed again (%d)", info.probes)
	}

	// Editing the target of 31 drops its auto-off (other target = other
	// signature); re-enabling 32 by the admin API clears it and probing starts.
	if code, msg := redeployMutationRequest(t, e.h.forwardRuleBestReenable, map[string]interface{}{"id": 32}); code != 0 {
		t.Fatalf("re-enable: %d %s", code, msg)
	}
	if mustQueryInt(t, e.r, `SELECT COUNT(*) FROM forward WHERE id = 32 AND rule_best_off_sig = '' AND rule_best_off_target = '' AND rule_best_off_time = 0`) != 1 {
		t.Fatalf("re-enable did not clear rule 32")
	}
	if n := strings.Count(logs.String(), "[rule-best] re-enabled rule=rule-b.example:443(32)"); n != 1 {
		t.Fatalf("re-enable log lines = %d", n)
	}
	info = rbEnvRound(t, e, rbEnvEdges(), rbEnvDests(), t0.Add(63*time.Minute))
	if info.probes != 2 || len(info.plans[0].RuleBest.Dests) != 1 || info.plans[0].RuleBest.Dests[0].Key != "b.example:443" {
		t.Fatalf("re-enabled rule not probed: %d probes", info.probes)
	}
	if _, count := rbListForwards(t, e, true); asInt64(count, -1) != 1 {
		t.Fatalf("notice count after re-enable = %v", count)
	}
	// Non-admins cannot re-enable.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":31}`))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "2", RoleID: 1}))
	w := httptest.NewRecorder()
	e.h.forwardRuleBestReenable(w, r)
	if !strings.Contains(w.Body.String(), `"code":403`) {
		t.Fatalf("user re-enable answered %s", w.Body.String())
	}
	if mustQueryInt(t, e.r, `SELECT COUNT(*) FROM forward WHERE id = 31 AND rule_best_off_sig <> ''`) != 1 {
		t.Fatalf("user request changed rule 31")
	}
	e.exec(`UPDATE forward SET remote_addr = 'a.example:443' WHERE id = 31`)
	if _, count := rbListForwards(t, e, true); asInt64(count, -1) != 0 {
		t.Fatalf("edited rule keeps the notice (count %v)", count)
	}
	if n := strings.Count(logs.String(), "[rule-best]"); n > 2+1+20 {
		t.Fatalf("too many rule-best log lines (%d)", n)
	}
}

// The admin edit path (UpdateForward) drops an auto-off when the tunnel or
// targets change and keeps it otherwise.
func TestRuleBestOffClearedByTargetEdit(t *testing.T) {
	e, _ := newRuleBestEnv(t)
	if ok, err := e.r.SetForwardRuleBestOff(32, 20, "b.example:443", "b.example:443", 1); err != nil || !ok {
		t.Fatalf("set off: %v %v", ok, err)
	}
	if ok, _ := e.r.SetForwardRuleBestOff(32, 20, "b.example:443", "b.example:443", 2); ok {
		t.Fatalf("second set reported a change")
	}
	if ok, _ := e.r.SetForwardRuleBestOff(32, 20, "other.example:1", "x", 3); ok {
		t.Fatalf("set off for a stale target changed the row")
	}
	upd := func(addr string) {
		if err := e.r.UpdateForward(32, "rule-b", 20, addr, "fifo", 5, nil, 0, 0, 0, nil, false, 0, "gost", 0, "port", "", "", true); err != nil {
			t.Fatal(err)
		}
	}
	upd("b.example:443") // same tunnel + target: kept
	if offs, _ := e.r.ForwardRuleBestOffs([]int64{32}); len(offs) != 1 || offs[32].Target != "b.example:443" {
		t.Fatalf("kept off: %+v", offs)
	}
	upd("b2.example:443")
	if offs, _ := e.r.ForwardRuleBestOffs([]int64{32}); len(offs) != 0 {
		t.Fatalf("edit kept the off: %+v", offs)
	}
	upd("b.example:443") // back to the old target: stays on (cleared, not just invalid)
	if offs, _ := e.r.ForwardRuleBestOffs([]int64{32}); len(offs) != 0 {
		t.Fatalf("old off came back: %+v", offs)
	}
}
