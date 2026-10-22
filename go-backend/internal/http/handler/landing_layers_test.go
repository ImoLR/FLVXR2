package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 134 handler tests: global group, node rules, debounce, statuses.

func layerCall(t *testing.T, h *Handler, path string, body any, uid int64, role int) response.R {
	t.Helper()
	mux := http.NewServeMux()
	h.registerLanding(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: strconv.FormatInt(uid, 10), RoleID: role}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	return out
}

type layerAgent struct {
	mu       sync.Mutex
	perNode  map[int64]int
	lastFor  map[int64]map[string]interface{}
	payloads []map[string]interface{}
	notReady bool // report a Core without the routing API
}

func (a *layerAgent) lastOf(node int64) *landingegress.Plan {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _ := a.lastFor[node]["egress"].(*landingegress.Plan)
	return p
}

func (a *layerAgent) count(id int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.perNode[id]
}

func (a *layerAgent) last() *landingegress.Plan {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.payloads) == 0 {
		return nil
	}
	p, _ := a.payloads[len(a.payloads)-1]["egress"].(*landingegress.Plan)
	return p
}

// layersEnv: native server 1 (node 2, guard 2, ready) with node 1 of user 2
// and node 2 of user 3; agent answers like an egress-capable agent.
func layersEnv(t *testing.T) (*flowTestEnv, *layerAgent) {
	t.Helper()
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addUser(3)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=2 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	if err := e.r.DB().Create(&model.LandingNode{ID: 2, UserID: 3, LandingServerID: 1, Name: "b-node", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	a := &layerAgent{perNode: map[int64]int{}, lastFor: map[int64]map[string]interface{}{}}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		payload := data.(map[string]interface{})
		a.mu.Lock()
		a.perNode[id]++
		a.lastFor[id] = payload
		a.payloads = append(a.payloads, payload)
		ready := !a.notReady
		a.mu.Unlock()
		state := "none"
		if _, ok := payload["egress"]; ok {
			state = "applied"
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": state, "ready": ready}}}, nil
	}
	e.h.SetLandingLayerApplyDelay(30 * time.Millisecond)
	return e, a
}

func compiledHash(t *testing.T, h *Handler, serverID int64) (string, *landingegress.Plan) {
	t.Helper()
	plan, _, err := h.compileLandingEgress(serverID)
	if err != nil {
		t.Fatal(err)
	}
	return planHash(plan), plan
}

func findRule(p *landingegress.Plan, name string) *landingegress.PlanRule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		if p.Rules[i].Name == name {
			return &p.Rules[i]
		}
	}
	return nil
}

// Saving an empty global group (the upgrade state) changes no plan.
func TestLandingGlobalEmptyKeepsPlans(t *testing.T) {
	e, _ := layersEnv(t)
	out := egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"name": "AI", "defaultAction": "block", "rules": []any{map[string]any{"name": "AI", "enabled": true, "domains": []string{"geosite:openai"}, "action": "direct"}}}, 0)
	if out.Code != 0 {
		t.Fatal(out)
	}
	pid := int64(dataMap(t, out)["id"].(float64))
	for _, profile := range []int64{0, pid} {
		if err := e.r.SetLandingServerEgress(1, profile, false); err != nil {
			t.Fatal(err)
		}
		before, _ := compiledHash(t, e.h, 1)
		for _, g := range []map[string]any{
			{"forced": []any{}, "default": []any{}, "defaultAction": "direct"},
			{"forced": []any{map[string]any{"name": "off", "enabled": false, "ports": "1", "action": "block"}}, "default": []any{}, "defaultAction": "direct"},
		} {
			if out := egressCall(t, e.h, "/api/v1/landing/egress/global/save", g, 0); out.Code != 0 {
				t.Fatal(out)
			}
			e.h.WaitEgressSync()
			if after, _ := compiledHash(t, e.h, 1); after != before {
				t.Fatalf("profile %d: empty global changed the plan", profile)
			}
		}
	}
}

func TestLandingGlobalLayersCompileAndMarkers(t *testing.T) {
	e, agent := layersEnv(t)
	if out := layerCall(t, e.h, "/api/v1/landing/egress/global/save", map[string]any{"forced": []any{}}, 2, 1); out.Code != 403 {
		t.Fatalf("user saved global rules: %+v", out)
	}
	global := map[string]any{
		"forced":        []any{map[string]any{"name": "强制屏蔽", "enabled": true, "domains": []string{"blocked.example"}, "action": "block"}},
		"default":       []any{map[string]any{"name": "默认 9999", "enabled": true, "ports": "9999", "action": "block"}},
		"defaultAction": "block",
	}
	out := egressCall(t, e.h, "/api/v1/landing/egress/global/save", global, 0)
	if out.Code != 0 {
		t.Fatal(out)
	}
	e.h.WaitEgressSync()
	plan := agent.last()
	forced := findRule(plan, "强制屏蔽")
	if forced == nil || forced.Builtin != landingegress.BuiltinForced || strings.Join(forced.InboundTags, ",") != "flvxl-1,flvxl-2" {
		t.Fatalf("forced rule on a guard-2 agent: %+v", forced)
	}
	if d := findRule(plan, "全局默认出站"); d == nil || d.Outbound != "block" || d.Builtin != landingegress.BuiltinGlobal {
		t.Fatalf("global default outbound: %+v", d)
	}
	// Older agent (guard 1): plain rules, no markers.
	e.exec("UPDATE node SET landing_guard=1 WHERE id=2")
	_, plan = compiledHash(t, e.h, 1)
	if f := findRule(plan, "强制屏蔽"); f == nil || f.Builtin != "" {
		t.Fatalf("marker sent to an old agent: %+v", f)
	}
	// Core not ready, no restart confirmed: global layers wait (like the
	// abuse defaults), no plan at all.
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", false)
	if _, plan = compiledHash(t, e.h, 1); plan != nil {
		t.Fatalf("plan sent to a Core that cannot take it: %+v", plan)
	}
	view := dataMap(t, egressCall(t, e.h, "/api/v1/landing/egress/global/get", map[string]any{}, 0))
	servers := view["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["state"] != "restart" {
		t.Fatalf("status: %+v", servers)
	}
	// Outbound used by the global group cannot be deleted.
	out = egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"name": "v4", "type": "direct", "ipVersion": "force_ipv4"}, 0)
	oid := int64(dataMap(t, out)["id"].(float64))
	global["defaultAction"] = "outbound:" + strconv.FormatInt(oid, 10)
	if out := egressCall(t, e.h, "/api/v1/landing/egress/global/save", global, 0); out.Code != 0 {
		t.Fatal(out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/delete", map[string]any{"id": oid}, 0); out.Code == 0 || !strings.Contains(out.Msg, "全局规则") {
		t.Fatalf("outbound used by global deleted: %+v", out)
	}
	e.h.WaitEgressSync()
}

// Rapid global saves push every server exactly once (debounce).
func TestLandingGlobalSaveDebounced(t *testing.T) {
	e, agent := layersEnv(t)
	e.addNode(3, "second")
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=2 WHERE id=3", true)
	if err := e.r.DB().Create(&model.LandingServer{ID: 2, NodeID: 3, Source: "native", Name: "second", PublicHost: "203.0.113.3", Status: "active", EgressReady: true}).Error; err != nil {
		t.Fatal(err)
	}
	e.h.SetLandingLayerApplyDelay(300 * time.Millisecond)
	for i := 0; i < 5; i++ {
		g := map[string]any{"forced": []any{map[string]any{"name": "强制 " + strconv.Itoa(i), "enabled": true, "ports": strconv.Itoa(7000 + i), "action": "block"}}}
		if out := egressCall(t, e.h, "/api/v1/landing/egress/global/save", g, 0); out.Code != 0 {
			t.Fatal(out)
		}
	}
	if agent.count(2) != 0 || agent.count(3) != 0 {
		t.Fatal("global save pushed synchronously")
	}
	e.h.WaitEgressSync()
	if agent.count(2) != 1 || agent.count(3) != 1 {
		t.Fatalf("pushes per server: node2=%d node3=%d (want 1 each)", agent.count(2), agent.count(3))
	}
	if r := findRule(agent.lastOf(2), "强制 4"); r == nil {
		t.Fatal("last global save not applied")
	}
}

func TestLandingNodeRulesHandler(t *testing.T) {
	e, agent := layersEnv(t)
	rules := []any{
		map[string]any{"name": "我的封锁", "enabled": true, "domains": []string{"mine.example"}, "action": "block"},
		map[string]any{"name": "内网直连", "enabled": true, "ips": []string{"10.0.0.0/8"}, "action": "direct"},
	}
	out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": rules}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("owner save: %+v", out)
	}
	if st := dataMap(t, out)["state"]; st != "syncing" {
		t.Fatalf("state after save = %v", st)
	}
	e.h.WaitEgressSync()
	plan := agent.last()
	mine := findRule(plan, "我的封锁")
	if mine == nil || strings.Join(mine.InboundTags, ",") != "flvxl-1" {
		t.Fatalf("node rule scope: %+v", mine)
	}
	// The private-range rule is compiled after the agent's fixed safety rule
	// (the Core keeps blocking 10.0.0.0/8; acceptance test proves it live).
	if r := findRule(plan, "内网直连"); r == nil || r.Builtin != "" {
		t.Fatalf("private rule: %+v", r)
	}
	view := dataMap(t, layerCall(t, e.h, "/api/v1/landing/node/rules/get", map[string]any{"id": 1}, 2, 1))
	if view["state"] != "applied" || len(view["rules"].([]any)) != 2 {
		t.Fatalf("view after sync: %+v", view)
	}
	// Another user's node.
	if out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": []any{}}, 3, 1); out.Code == 0 {
		t.Fatal("user 3 edited user 2's node rules")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/rules/get", map[string]any{"id": 1}, 3, 1); out.Code == 0 {
		t.Fatal("user 3 read user 2's node rules")
	}
	// Source IP is admin-only matcher; users never get it.
	bad := []any{map[string]any{"name": "x", "enabled": true, "sourceIps": []string{"1.2.3.4"}, "action": "block"}}
	if out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": bad}, 2, 1); out.Code == 0 {
		t.Fatal("source IP accepted in node rules")
	}
	// Unpublished outbound refused, published accepted.
	out = egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"name": "v4", "type": "direct", "ipVersion": "force_ipv4"}, 0)
	oid := int64(dataMap(t, out)["id"].(float64))
	via := []any{map[string]any{"name": "走 v4", "enabled": true, "ports": "443", "action": "outbound:" + strconv.FormatInt(oid, 10)}}
	if out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": via}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "未开放") {
		t.Fatalf("unpublished outbound accepted: %+v", out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"id": oid, "name": "v4", "type": "direct", "ipVersion": "force_ipv4", "userAllowed": true}, 0); out.Code != 0 || dataMap(t, out)["userAllowed"] != true {
		t.Fatalf("publish: %+v", out)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": via, "defaultAction": "outbound:" + strconv.FormatInt(oid, 10)}, 2, 1); out.Code != 0 {
		t.Fatalf("published outbound refused: %+v", out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/delete", map[string]any{"id": oid}, 0); out.Code == 0 || !strings.Contains(out.Msg, "节点") {
		t.Fatalf("outbound used by node rules deleted: %+v", out)
	}
	e.h.WaitEgressSync()
	plan = agent.last()
	if d := findRule(plan, "节点默认出站"); d == nil || strings.Join(d.InboundTags, ",") != "flvxl-1" || d.Outbound != landingegress.OutboundTag(oid) {
		t.Fatalf("node default outbound: %+v", d)
	}
	if findRule(plan, "我的封锁") != nil {
		t.Fatal("replaced rules still in the plan")
	}
	// User 3's node is untouched by user 2's rules.
	for _, r := range plan.Rules {
		if strings.Contains(strings.Join(r.InboundTags, ","), "flvxl-2") && r.Builtin == "" {
			t.Fatalf("user 2's rule reached user 3's node: %+v", r)
		}
	}
}

// A Core that cannot hot-apply rules keeps node rules queued; nothing
// restarts it.
func TestLandingNodeRulesQueuedWhenCoreNotReady(t *testing.T) {
	e, agent := layersEnv(t)
	agent.notReady = true
	e.exec("UPDATE landing_server SET egress_ready=?, egress_restart_ok=? WHERE id=1", false, false)
	before, _ := compiledHash(t, e.h, 1)
	out := layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": []any{map[string]any{"name": "x", "enabled": true, "ports": "7777", "action": "block"}}}, 2, 1)
	if out.Code != 0 || dataMap(t, out)["state"] != "queued" {
		t.Fatalf("queued save: %+v", out)
	}
	e.h.WaitEgressSync()
	if after, _ := compiledHash(t, e.h, 1); after != before {
		t.Fatal("queued node rules changed the plan")
	}
	for _, p := range agent.payloads {
		if plan, ok := p["egress"].(*landingegress.Plan); ok && plan.AllowCoreRestart {
			t.Fatal("node rule save allowed a Core restart")
		}
	}
	// Unsupported (old agent): refused with the existing reason.
	e.exec("UPDATE node SET landing_egress=0 WHERE id=2")
	out = layerCall(t, e.h, "/api/v1/landing/node/rules/save", map[string]any{"id": 1, "rules": []any{map[string]any{"name": "y", "enabled": true, "ports": "1", "action": "block"}}}, 2, 1)
	if out.Code == 0 || !strings.Contains(out.Msg, "节点程序版本过旧") {
		t.Fatalf("unsupported server: %+v", out)
	}
}
