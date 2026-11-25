package contract_test

// Plan 310: the 利群 rule latency tag on SQLite and PostgreSQL — the entry's
// agent report (real fixtures + the plan-310 rtt fields) goes through the
// normal ingest; the admin list and the owner's list show the 全程 of the
// row's entry (UDP with probes, else TCP; bogus UDP 60000 dropped like 诊断),
// old agents / offline entries give a reason, users see only their own rules
// and never the forward number. Round 3: the tag is 到落地 (UDP 全程 + 专线出口
// → 落地); the imported rules have manual landings → no tag, the reason only
// (the landing part is covered by the handler tests: contract handlers have
// no agents to ping from).

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestLeiKwanRuleLatencyContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanRuleLatencyFlow(t, dialect) })
	}
}

func lkWithRtt(rtts map[int64]map[string]interface{}) func(map[string]interface{}) {
	return func(m map[string]interface{}) {
		st := m["state"].(map[string]interface{})
		st["rtt"] = 1
		for _, it := range st["forwards"].([]interface{}) {
			f := it.(map[string]interface{})
			for k, v := range rtts[num(f["forwardId"])] {
				f[k] = v
			}
		}
	}
}

func lkRuleByForward(t *testing.T, rules []interface{}, name string) map[string]interface{} {
	t.Helper()
	for _, it := range rules {
		m := it.(map[string]interface{})
		if m["name"] == name {
			return m
		}
	}
	t.Fatalf("rule %q not listed", name)
	return nil
}

func leiKwanRuleLatencyFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	var r263, r264 model.LeiKwanRule
	e.must(e.r.DB().Where("forward_id = ?", 263).First(&r263).Error)
	e.must(e.r.DB().Where("forward_id = ?", 264).First(&r264).Error)
	// Old agents (fixtures as captured): every row says the agent needs an update.
	for node, host := range lkHostFixtures {
		e.h.IngestLeiKwanHostReport(node, lkFixtureReport(t, host, nil))
	}
	list := e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{})["rules"].([]interface{})
	if m := lkRuleByForward(t, list, r263.Name); m["latency"] != nil || m["latencyNote"] != "入口机 agent 需要升级" {
		t.Fatalf("old agent row: %v %v", m["latency"], m["latencyNote"])
	}
	// 百度云广州 (the admin grant's 主入口) on a plan-310 agent.
	e.h.IngestLeiKwanHostReport(58, lkFixtureReport(t, "baidu", lkWithRtt(map[int64]map[string]interface{}{
		263: {"rttUdp": map[string]interface{}{"rttMs": 11, "p95Ms": 12, "sent": 30, "recv": 30, "ageS": 6}, "rttTcp": map[string]interface{}{"rttMs": 107, "ageS": 30}},
		264: {"rttUdp": map[string]interface{}{"rttMs": 60000, "ageS": 2}, "rttTcp": map[string]interface{}{"rttMs": 64, "ageS": 9}},
	})))
	e.h.RunLeiKwanLandPass()
	list = e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{})["rules"].([]interface{})
	for _, c := range []struct {
		rule model.LeiKwanRule
		note string
	}{{r263, "手动落地，没有落地延迟"}, {r264, "利群客户端没有 UDP 全程"}} {
		m := lkRuleByForward(t, list, c.rule.Name)
		if m["latency"] != nil || m["landLatency"] != nil || m["latencyNote"] != c.note {
			t.Fatalf("%s: %v %v", c.rule.Name, m["latency"], m["latencyNote"])
		}
	}
	// alice owns 264 on 阿里广州 (old agent there) and sees only that rule,
	// with a reason and no forward number; bob (no grant) sees nothing.
	e.must(e.r.DB().Model(&model.LeiKwanRule{}).Where("id = ?", r264.ID).Update("user_id", 2).Error)
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true, "num": 2, "flowGb": 100})
	mine := e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})
	if len(mine) != 1 {
		t.Fatalf("alice rules: %d", len(mine))
	}
	m := mine[0].(map[string]interface{})
	if m["latency"] != nil || m["latencyNote"] != "入口机 agent 需要升级" || m["forwardId"] != nil {
		t.Fatalf("alice old agent: %v", m)
	}
	e.h.IngestLeiKwanHostReport(60, lkFixtureReport(t, "aliyun-gz", lkWithRtt(map[int64]map[string]interface{}{
		264: {"rttUdp": map[string]interface{}{"rttMs": 60, "sent": 30, "recv": 30, "ageS": 3}},
	})))
	m = e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})[0].(map[string]interface{})
	if m["latency"] != nil || m["latencyNote"] != "手动落地，没有落地延迟" || m["forwardId"] != nil {
		t.Fatalf("alice latency: %v", m)
	}
	if out, data := e.call("/api/v1/leikwan-rule/list", e.other, map[string]interface{}{}); out.Code != 0 || len(data["rules"].([]interface{})) != 0 {
		t.Fatalf("bob: %+v %v", out, data)
	}
	// 主入口 offline → 入口机离线 (no stale number).
	e.must(e.r.DB().Model(&model.Node{}).Where("id = ?", 60).Update("status", 0).Error)
	m = e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})[0].(map[string]interface{})
	if m["latency"] != nil || m["latencyNote"] != "入口机离线" {
		t.Fatalf("alice offline: %v %v", m["latency"], m["latencyNote"])
	}
}
