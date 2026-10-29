package handler

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 161: per-user 速率 on FLVX-native landing servers. The native payload
// carries user_rates + rate_group only for agents advertising landingRate;
// older agents (and capable ones while nobody has a rate) get today's payload
// byte for byte. Each content change advances the revision exactly once; the
// agent's kernel error is kept for the user's row and cleared when applied.

type rateAgent struct {
	mu       sync.Mutex
	payloads []map[string]interface{}
	report   map[string]interface{} // user_rate answered by the agent; nil = older agent
}

func (a *rateAgent) last(t *testing.T) (map[string]interface{}, []byte) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.payloads) == 0 {
		t.Fatal("no ApplyLandingDesired sent")
	}
	raw, _ := json.Marshal(a.payloads[len(a.payloads)-1])
	p := map[string]interface{}{}
	_ = json.Unmarshal(raw, &p)
	return p, raw
}

func (a *rateAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.payloads)
}

func newRateAgent(t *testing.T, e *flowTestEnv) *rateAgent {
	a := &rateAgent{}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		if method != "ApplyLandingDesired" {
			return ws.CommandResult{Success: true}, nil
		}
		payload := data.(map[string]interface{})
		a.mu.Lock()
		a.payloads = append(a.payloads, payload)
		status := map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "none"}}
		if a.report != nil {
			status["user_rate"] = a.report
		}
		a.mu.Unlock()
		return ws.CommandResult{Success: true, Data: status}, nil
	}
	return a
}

func rateNodes(p map[string]interface{}) map[float64]map[string]interface{} {
	return connModeNodes(p)
}

func TestNativeLandingPayloadUserRate(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addUser(3)
	seedHandlerLanding(t, e, 2, 2, 1) // server 1 on node 2, node 1 of user 2
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0 WHERE id=2", true)
	for _, n := range []model.LandingNode{
		{ID: 2, UserID: 2, LandingServerID: 1, Name: "two", Protocol: "vless_reality_vision", Port: 30002, InboundTag: "flvxl-2", Status: "active"},
		{ID: 3, UserID: 3, LandingServerID: 1, Name: "three", Protocol: "ss2022", Port: 30003, InboundTag: "flvxl-3", Status: "active"},
	} {
		if err := e.r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	a := newRateAgent(t, e)
	sync := func() (map[string]interface{}, []byte) {
		t.Helper()
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
		return a.last(t)
	}
	base, baseRaw := sync()

	// Rates set, older agent: byte-identical payload, same revision.
	if err := e.r.SetUserServerRate(2, 20); err != nil {
		t.Fatal(err)
	}
	if err := e.r.DB().Create(&model.UserServerQuota{UserID: 3, ServerKind: "node", ServerRefID: 2, FollowDefault: true, RateOverride: true, RateMbps: 50, CreatedTime: 1, UpdatedTime: 1}).Error; err != nil {
		t.Fatal(err)
	}
	old, oldRaw := sync()
	if string(oldRaw) != string(baseRaw) || old["revision"] != base["revision"] {
		t.Fatalf("older agent payload changed:\n%s\n%s", baseRaw, oldRaw)
	}

	// Capable agent: one group per owner, every node of the owner tagged.
	e.exec("UPDATE node SET landing_rate = 1 WHERE id = 2")
	rated, ratedRaw := sync()
	if rated["revision"].(float64) <= old["revision"].(float64) {
		t.Fatalf("revision not advanced: %v → %v", old["revision"], rated["revision"])
	}
	nodes := rateNodes(rated)
	if nodes[1]["rate_group"] != "landing-user-2" || nodes[2]["rate_group"] != "landing-user-2" || nodes[3]["rate_group"] != "landing-user-3" {
		t.Fatalf("rate groups: %s", ratedRaw)
	}
	ur, _ := rated["user_rates"].(map[string]interface{})
	groups, _ := ur["groups"].([]interface{})
	if ur["version"].(float64) != 1 || len(groups) != 2 {
		t.Fatalf("user_rates: %s", ratedRaw)
	}
	g0, g1 := groups[0].(map[string]interface{}), groups[1].(map[string]interface{})
	if g0["group"] != "landing-user-2" || g0["rate_mbps"].(float64) != 20 || g1["group"] != "landing-user-3" || g1["rate_mbps"].(float64) != 50 {
		t.Fatalf("groups: %v", groups)
	}
	t.Logf("native payload, 3 nodes: %d bytes, with rates %d (+%d)", len(baseRaw), len(ratedRaw), len(ratedRaw)-len(baseRaw))
	if again, _ := sync(); again["revision"] != rated["revision"] {
		t.Fatal("identical rate content advanced the revision")
	}

	// A rate change is a new revision with the new value.
	if err := e.r.SetUserServerRate(2, 30); err != nil {
		t.Fatal(err)
	}
	changed, changedRaw := sync()
	if changed["revision"] == rated["revision"] || !strings.Contains(string(changedRaw), `"group":"landing-user-2","rate_mbps":30`) {
		t.Fatalf("change: %s", changedRaw)
	}

	// The agent reports a kernel error: kept for the row, cleared when on.
	a.report = map[string]interface{}{"state": "error", "error": "内核限速失败：eth0 添加过滤器失败"}
	sync()
	if s, _ := e.r.GetLandingServer(1); s == nil || s.RateError != "内核限速失败：eth0 添加过滤器失败" {
		t.Fatalf("rate error not recorded: %+v", s)
	}
	a.report = map[string]interface{}{"state": "on"}
	sync()
	if s, _ := e.r.GetLandingServer(1); s == nil || s.RateError != "" {
		t.Fatalf("rate error not cleared: %q", s.RateError)
	}

	// All rates removed: the fields disappear (payload = an older agent's
	// with the same server state), the revision advances once.
	if err := e.r.SetUserServerRate(2, 0); err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE user_server_quota SET rate_mbps = 0 WHERE user_id = 3")
	free, freeRaw := sync()
	if strings.Contains(string(freeRaw), "rate_group") || strings.Contains(string(freeRaw), "user_rates") || free["revision"] == changed["revision"] {
		t.Fatalf("no rates: %s", freeRaw)
	}
	if again, _ := sync(); again["revision"] != free["revision"] {
		t.Fatal("identical content advanced the revision")
	}
}

// The user's 默认速率 saved in the user dialog reaches the user's native
// landing servers right away (rows without an own value follow it).
func TestUserDefaultRateSyncsNativeLanding(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0, landing_rate=1 WHERE id=2", true)
	a := newRateAgent(t, e)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	before := a.count()
	u, err := e.r.GetUserByID(2)
	if err != nil || u == nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]interface{}{"id": 2, "user": u.User, "flow": 99999, "num": 10, "expTime": u.ExpTime, "flowResetTime": 1, "status": 1, "serverRateMbps": 25})
	w := httptest.NewRecorder()
	e.h.userUpdate(w, httptest.NewRequest("POST", "/api/v1/user/update", bytes.NewReader(body)))
	if !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("user update: %s", w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if a.count() > before {
			p, raw := a.last(t)
			if rateNodes(p)[1]["rate_group"] == "landing-user-2" && strings.Contains(string(raw), `"rate_mbps":25`) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("default rate not pushed (%d → %d payloads)", before, a.count())
}

// A capable agent that (re)connects gets its native desired state at once
// (e.g. right after its OTA); an older agent keeps the minute loop.
func TestNativeLandingSyncOnReconnect(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0 WHERE id=2", true)
	if err := e.r.SetUserServerRate(2, 15); err != nil {
		t.Fatal(err)
	}
	a := newRateAgent(t, e)
	e.h.syncNativeLandingOnReconnect(2)
	time.Sleep(300 * time.Millisecond)
	if a.count() != 0 {
		t.Fatal("older agent synced on reconnect")
	}
	e.exec("UPDATE node SET landing_rate = 1 WHERE id = 2")
	e.h.syncNativeLandingOnReconnect(2)
	deadline := time.Now().Add(3 * time.Second)
	for a.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if a.count() == 0 {
		t.Fatal("capable agent not synced on reconnect")
	}
	if _, raw := a.last(t); !strings.Contains(string(raw), `"rate_mbps":15`) {
		t.Fatalf("payload without the rate: %s", raw)
	}
	e.h.syncNativeLandingOnReconnect(99) // unknown node: nothing
}
