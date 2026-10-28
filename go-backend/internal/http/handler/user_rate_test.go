package handler

import (
	"strings"
	"testing"
)

// Plan 159: the user's 速率 on an entry machine = one agent limiter shared by
// the user's forwards there. The reconnect env: user 7, gost forward 100 and
// nftables forward 101, both entering node 1.

// lastServicePayload returns the newest pushed config of a service.
func lastServicePayload(a *fakeRuntimeAgent, nodeID int64, name string) map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.commands) - 1; i >= 0; i-- {
		c := a.commands[i]
		if c.nodeID != nodeID || (c.method != "UpdateService" && c.method != "AddService") {
			continue
		}
		list, _ := genericOf(c.data).([]interface{})
		for _, raw := range list {
			if m, ok := raw.(map[string]interface{}); ok && m["name"] == name {
				return m
			}
		}
	}
	return nil
}

// lastNftSpeed returns the speed_limit of the newest pushed rules of a forward.
func lastNftSpeed(a *fakeRuntimeAgent, nodeID, forwardID int64) (float64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.commands) - 1; i >= 0; i-- {
		c := a.commands[i]
		if c.nodeID != nodeID || (c.method != "AddNftablesRules" && c.method != "UpdateNftablesRules") {
			continue
		}
		g, _ := genericOf(c.data).(map[string]interface{})
		rules, _ := g["rules"].([]interface{})
		for _, raw := range rules {
			if r, ok := raw.(map[string]interface{}); ok && r["forward_id"] == float64(forwardID) {
				v, _ := r["speed_limit"].(float64)
				return v, true
			}
		}
	}
	return 0, false
}

func userLimiterMeta(svc map[string]interface{}) string {
	meta, _ := svc["metadata"].(map[string]interface{})
	s, _ := meta["userLimiter"].(string)
	return s
}

func setUserRate(t *testing.T, e *flowTestEnv, userID int64, rate int) {
	t.Helper()
	before := e.h.userRateBefore(userID)
	if err := e.r.SetUserServerRate(userID, rate); err != nil {
		t.Fatal(err)
	}
	if w := e.h.userRateAfter(userID, before); len(w) > 0 {
		t.Fatalf("warnings %v", w)
	}
}

func TestUserRateCapableAgentIsHot(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	e.h.userRateCapableFn = func(int64) bool { return true }
	deployAll(e, agent)
	svc := lastServicePayload(agent, 1, "100_7_70_tcp")
	if svc != nil {
		t.Fatalf("deployAll was reset; payload leaked: %v", svc)
	}
	// Capable agents always carry the reference (no limiter = no limit).
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("unchanged reconnect sent %v", m)
	}
	if !agent.has(1, "service:100_7_70_tcp") {
		t.Fatal("forward 100 not deployed")
	}

	// Setting a rate only adds the limiter (+ the nft rule's policer).
	setUserRate(t, e, 7, 50)
	m := strings.Join(agent.mutations(1), " | ")
	if !strings.Contains(m, "AddLimiters") || !strings.Contains(m, "flvx_user_7_rate") || strings.Contains(m, "UpdateService") {
		t.Fatalf("capable agent: want the limiter only, got %s", m)
	}
	if v, ok := lastNftSpeed(agent, 1, 101); !ok || v != 50 {
		t.Fatalf("nft forward 101 policer = %v (pushed %v), want 50", v, ok)
	}
	// Same rate again: nothing (no re-created limiter object).
	agent.reset()
	if w := e.h.applyUserRateChanges(7, map[int64]int{1: 50}, map[int64]int{1: 50}); len(w) > 0 || len(agent.mutations(1)) != 0 {
		t.Fatalf("unchanged rate pushed %v %v", agent.mutations(1), w)
	}
	// A reconnect with everything in place sends nothing.
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("reconnect after rate sent %v", m)
	}
	// Changing the rate updates the limiter only.
	agent.reset()
	setUserRate(t, e, 7, 80)
	m = strings.Join(agent.mutations(1), " | ")
	if !strings.Contains(m, "flvx_user_7_rate") || strings.Contains(m, "UpdateService") {
		t.Fatalf("rate change: %s", m)
	}
	// Removing it deletes the limiter (hot) and lifts the nft policer.
	agent.reset()
	setUserRate(t, e, 7, 0)
	m = strings.Join(agent.mutations(1), " | ")
	if !strings.Contains(m, "DeleteLimiters") || strings.Contains(m, "UpdateService") || agent.has(1, "limiter:flvx_user_7_rate") {
		t.Fatalf("rate removal: %s", m)
	}
	if v, _ := lastNftSpeed(agent, 1, 101); v != 0 {
		t.Fatalf("nft policer after removal = %v", v)
	}
}

func TestUserRateReconnectFixesOfflineChanges(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	e.h.userRateCapableFn = func(int64) bool { return true }
	deployAll(e, agent)
	// Rate set while node 1 was away → the reconnect pushes the limiter.
	if err := e.r.SetUserServerRate(7, 40); err != nil {
		t.Fatal(err)
	}
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if !agent.has(1, "limiter:flvx_user_7_rate") {
		t.Fatalf("reconnect did not push the user limiter: %v", agent.mutations(1))
	}
	// Removed while away → the stale limiter is deleted on reconnect.
	if err := e.r.SetUserServerRate(7, 0); err != nil {
		t.Fatal(err)
	}
	agent.reset()
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if agent.has(1, "limiter:flvx_user_7_rate") {
		t.Fatalf("stale user limiter kept: %v", agent.mutations(1))
	}
	// Other limiters (rule / 限速规则) are never touched by this.
	agent.reset()
	forgetPushes(e)
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("clean reconnect sent %v", m)
	}
}

func TestUserRateOlderAgentFallback(t *testing.T) {
	e, agent := newReconnectDiffEnv(t)
	deployAll(e, agent) // agent without the userRate capability
	setUserRate(t, e, 7, 50)
	svc := lastServicePayload(agent, 1, "100_7_70_tcp")
	if svc == nil || svc["limiter"] != "flvx_user_7_rate" || userLimiterMeta(svc) != "" {
		t.Fatalf("older agent: rule without own limit must reference the user limiter: %v", svc)
	}
	if !agent.has(1, "limiter:flvx_user_7_rate") {
		t.Fatal("user limiter missing")
	}
	forgetPushes(e)
	agent.reset()
	e.h.redeployNodeRuntime(1)
	if m := agent.mutations(1); len(m) != 0 {
		t.Fatalf("reconnect after fallback sent %v", m)
	}
	// A rule with its own speed keeps its own limiter on an older agent.
	e.exec(`UPDATE forward SET speed_limit_enabled = 1, speed_limit = 10 WHERE id = 100`)
	f, err := e.h.getForwardRecord(100)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.h.syncForwardServicesOnNode(f, 1); err != nil {
		t.Fatal(err)
	}
	if svc := lastServicePayload(agent, 1, "100_7_70_tcp"); svc == nil || svc["limiter"] != "forward_100_speed" {
		t.Fatalf("own limiter replaced: %v", svc)
	}
	// Removing the rate re-pushes rules that referenced it, then deletes it.
	e.exec(`UPDATE forward SET speed_limit_enabled = 0, speed_limit = 0 WHERE id = 100`)
	f, _ = e.h.getForwardRecord(100)
	_ = e.h.syncForwardServicesOnNode(f, 1)
	agent.reset()
	setUserRate(t, e, 7, 0)
	svc = lastServicePayload(agent, 1, "100_7_70_tcp")
	if svc == nil || svc["limiter"] != nil {
		t.Fatalf("after removal the rule must not reference the limiter: %v", svc)
	}
	if agent.has(1, "limiter:flvx_user_7_rate") {
		t.Fatal("user limiter not deleted")
	}
}

func TestUserRateNoRateNoChange(t *testing.T) {
	// No rate anywhere + older agent = payloads exactly as before plan 159.
	e, agent := newReconnectDiffEnv(t)
	for _, id := range []int64{1} {
		e.h.redeployNodeRuntime(id)
	}
	svc := lastServicePayload(agent, 1, "100_7_70_tcp")
	if svc == nil || svc["limiter"] != nil || userLimiterMeta(svc) != "" {
		t.Fatalf("payload changed without a rate: %v", svc)
	}
	if v, _ := lastNftSpeed(agent, 1, 101); v != 0 {
		t.Fatalf("nft policer without a rate: %v", v)
	}
}

func TestRuleSpeedMustFitRow(t *testing.T) {
	e, _ := newReconnectDiffEnv(t)
	if err := e.r.SetUserServerRate(7, 50); err != nil {
		t.Fatal(err)
	}
	if err := e.h.validateRuleSpeedAgainstRows(7, []int64{1}, 50); err != nil {
		t.Fatalf("equal speed rejected: %v", err)
	}
	if err := e.h.validateRuleSpeedAgainstRows(7, []int64{1}, 60); err == nil || !strings.Contains(err.Error(), "50 Mbps") {
		t.Fatalf("speed above the row accepted: %v", err)
	}
	if err := e.h.validateRuleSpeedAgainstRows(7, []int64{1}, 0); err != nil {
		t.Fatalf("no own speed must pass: %v", err)
	}
	if nftRuleSpeed(0, 50) != 50 || nftRuleSpeed(80, 50) != 50 || nftRuleSpeed(20, 50) != 20 || nftRuleSpeed(20, 0) != 20 {
		t.Fatal("nftRuleSpeed")
	}
	if id, ok := userRateLimiterUser(userRateLimiterName(42)); !ok || id != 42 {
		t.Fatal("limiter name round trip")
	}
	for _, bad := range []string{"flvx_user__rate", "flvx_user_x_rate", "forward_1_speed", "flvx_user_07_rate"} {
		if _, ok := userRateLimiterUser(bad); ok {
			t.Fatalf("%q parsed as a user limiter", bad)
		}
	}
}
