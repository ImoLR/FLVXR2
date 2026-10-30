package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-backend/internal/http/response"
)

// Plan 170: deleting a 限速规则 clears its references and re-pushes the rules
// that used it only where the agent still runs something else (here: a
// pre-plan-170 service that references the limiter). Rules whose agent already
// runs the limiter-free payload are left alone (no service restart).
func TestSpeedLimitDeleteResyncsOnlyDifferingRules(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	e.exec(`INSERT INTO speed_limit(id, name, speed, status, arena_mode, created_time, updated_time) VALUES(5, 'old', 20, 1, 1, 0, 0)`)
	e.exec(`UPDATE forward SET speed_id = 5 WHERE id = 100`)    // gost rule, own 限速规则
	e.exec(`UPDATE user_tunnel SET speed_id = 5 WHERE id = 71`) // nft rule 101 via its user tunnel
	deployAll(e, agent)

	// Node 1 still runs rule 100 with the old limiter reference (as a
	// fork.97 panel left it).
	f100, _ := e.h.getForwardRecord(100)
	in, err := e.h.resolveForwardRuntimeInputs(f100)
	if err != nil {
		t.Fatal(err)
	}
	node, _ := e.h.getNodeRecord(1)
	stale := buildForwardServiceConfigs(in.serviceBase, f100, in.tunnel, node, 1501, "", false)
	for _, s := range stale {
		s["limiter"] = "arena_speed_5_node_1_x"
	}
	if _, err := agent.send(1, "UpdateService", stale, 0); err != nil {
		t.Fatal(err)
	}
	agent.reset()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/delete", bytes.NewBufferString(`{"id":5}`))
	res := httptest.NewRecorder()
	e.h.speedLimitDelete(res, req)
	var out response.R
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Code != 0 {
		t.Fatalf("delete: %+v %v", out, err)
	}

	if svc := lastServicePayload(agent, 1, "100_7_70_tcp"); svc == nil || svc["limiter"] != nil {
		t.Fatalf("rule 100 not re-pushed without the limiter: %v", svc)
	}
	m := strings.Join(agent.mutations(1), " | ")
	if strings.Contains(m, "Nftables") || strings.Contains(m, "Limiters") {
		t.Fatalf("unchanged nft rule re-pushed or limiter pushed: %s", m)
	}
	for _, q := range []string{
		`SELECT COUNT(1) FROM forward WHERE speed_id IS NOT NULL`,
		`SELECT COUNT(1) FROM user_tunnel WHERE speed_id IS NOT NULL`,
		`SELECT COUNT(1) FROM speed_limit`,
	} {
		var n int
		if err := e.r.DB().Raw(q).Row().Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s = %d (%v)", q, n, err)
		}
	}

	// A second delete of a limit nobody runs any more: nothing sent.
	e.exec(`INSERT INTO speed_limit(id, name, speed, status, arena_mode, created_time, updated_time) VALUES(6, 'old2', 20, 1, 0, 0, 0)`)
	e.exec(`UPDATE forward SET speed_id = 6 WHERE id = 100`)
	agent.reset()
	res = httptest.NewRecorder()
	e.h.speedLimitDelete(res, httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/delete", bytes.NewBufferString(`{"id":6}`)))
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("unchanged rule re-pushed: %v", m)
	}
}

// Plan 170: create / update of 限速规则 are refused cleanly.
func TestSpeedLimitWritesRetired(t *testing.T) {
	e, _ := newReconnectDiffEnv(t)
	for _, fn := range []func(http.ResponseWriter, *http.Request){e.h.speedLimitCreate, e.h.speedLimitUpdate} {
		res := httptest.NewRecorder()
		fn(res, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":1,"name":"x","speed":5,"arenaMode":1}`)))
		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Code == 0 || out.Msg != speedLimitRetiredMsg {
			t.Fatalf("expected the retired message, got %+v %v", out, err)
		}
	}
}

// Plan 170: the per-server row (override) and the user default are the only
// entry limits left — both still make the tc user class.
func TestEntryRateTreeFromRowAndDefaultOnly(t *testing.T) {
	e, agent := newEntryRateEnv(t)
	e.exec(`INSERT INTO speed_limit(id, name, speed, status, arena_mode, created_time, updated_time) VALUES(5, 'old', 20, 1, 0, 0, 0)`)
	e.exec(`UPDATE forward SET speed_id = 5, speed_limit_enabled = 1, speed_limit = 3 WHERE id = 100`)
	setUserRate(t, e, 7, 40) // user default
	drainEntryRates(e)
	g, _ := lastEntryRates(agent, 1)
	if c := entryClasses(g); len(c) != 1 || c["u7"] != "@40" {
		t.Fatalf("default: %v", c)
	}
	// per-server row overrides the default on node 1
	before := e.h.userRateBefore(7)
	e.exec(`INSERT INTO user_server_quota(user_id, server_kind, server_ref_id, rate_mbps, rate_override, created_time, updated_time) VALUES(7, 'node', 1, 25, true, 0, 0)`)
	if w := e.h.userRateAfter(7, before); len(w) > 0 {
		t.Fatalf("warnings %v", w)
	}
	drainEntryRates(e)
	g, _ = lastEntryRates(agent, 1)
	if c := entryClasses(g); len(c) != 1 || c["u7"] != "@25" {
		t.Fatalf("row: %v", c)
	}
}
