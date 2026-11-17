package contract_test

// Plan 270 利群汇聚 page redesign: the 「利群汇聚」 nav flag
// (/leikwan-rule/access) and the grant gate on the user-facing 利群 rule
// endpoints — a user without an active grant gets no rules, no lines and no
// vendor quota read; a granted user sees only their own rules, without the
// admin fields (利群转发号, owner, 预热, 备线 forward ids / error text).

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestLeiKwanRedesignGrantFlagContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanRedesignGrantFlagFlow(t, dialect) })
	}
}

func (e *lkLineEnv) granted(token string) bool {
	e.t.Helper()
	v, ok := e.ok("/api/v1/leikwan-rule/access", token, map[string]interface{}{})["granted"].(bool)
	if !ok {
		e.t.Fatal("access: granted flag missing")
	}
	return v
}

func (e *lkLineEnv) ruleList(token string) map[string]interface{} {
	e.t.Helper()
	return e.ok("/api/v1/leikwan-rule/list", token, map[string]interface{}{})
}

func leiKwanRedesignGrantFlagFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})

	// Admin: always open, every rule and line.
	if !e.granted(e.admin) {
		t.Fatal("admin access flag false")
	}
	adm := e.ruleList(e.admin)
	if len(adm["rules"].([]interface{})) != 7 || len(adm["lines"].([]interface{})) != 4 {
		t.Fatalf("admin list: rules=%d lines=%v", len(adm["rules"].([]interface{})), adm["lines"])
	}
	if l := adm["lines"].([]interface{})[0].(map[string]interface{}); l["transport"] == nil || l["routeLabel"] == nil {
		t.Fatalf("admin line view: %v", l)
	}
	// 「旧管道」 card: the pipe row carries its id / etag and the tunnel's rule count.
	pipes := e.ok("/api/v1/leikwan/line/list", e.admin, map[string]interface{}{})["pipes"].([]interface{})
	if p := pipes[0].(map[string]interface{}); len(pipes) != 1 || num(p["id"]) == 0 || p["etag"] == nil || num(p["forwardId"]) != 318 || p["tunnelRules"] == nil || num(p["tunnelRules"]) != 0 {
		t.Fatalf("pipe view: %v", pipes)
	}

	// No grant: no nav flag, no rules, no lines, no landings, no vendor read.
	reads, _ := e.fake.Counts()
	if e.granted(e.user) {
		t.Fatal("user without grant: access flag true")
	}
	none := e.ruleList(e.user)
	if len(none["rules"].([]interface{})) != 0 || none["lines"] != nil || none["grant"] != nil || none["granted"] != false {
		t.Fatalf("user without grant list: %v", none)
	}
	opts := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})
	if len(opts["lines"].([]interface{})) != 0 || len(opts["landings"].([]interface{})) != 0 || opts["grantError"] == nil {
		t.Fatalf("user without grant options: %v", opts)
	}
	if r2, _ := e.fake.Counts(); r2 != reads {
		t.Fatalf("no-grant calls hit the LeiKwan API: %d -> %d", reads, r2)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": 1, "name": "x", "landingNodeId": 501, "requestId": "req-redesign-0"}); out.Code == 0 {
		t.Fatal("create without grant accepted")
	}

	// Grant alice: flag on, the active lines (name/route/transport) for 「可用专线」.
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true})
	if !e.granted(e.user) {
		t.Fatal("granted user: access flag false")
	}
	if e.granted(e.other) {
		t.Fatal("bob has no grant but the flag is on")
	}
	mine := e.ruleList(e.user)
	lines := mine["lines"].([]interface{})
	if len(mine["rules"].([]interface{})) != 0 || len(lines) != 4 || mine["grant"] == nil {
		t.Fatalf("granted user list before any rule: %v", mine)
	}
	for _, l := range lines {
		m := l.(map[string]interface{})
		if m["name"] == nil || m["transport"] != "tcp+udp" || m["routeLabel"] == nil {
			t.Fatalf("user line view: %v", m)
		}
	}

	// One rule for alice (quota raised on the fake account).
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 10
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	lineID := num(lines[0].(map[string]interface{})["id"])
	created := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-redesign-1"})
	ruleID := num(created["rule"].(map[string]interface{})["id"])

	// alice sees exactly her rule, without admin fields.
	mineRules := e.ruleList(e.user)["rules"].([]interface{})
	if len(mineRules) != 1 || num(mineRules[0].(map[string]interface{})["id"]) != ruleID {
		t.Fatalf("granted user rules: %v", mineRules)
	}
	for _, k := range []string{"forwardId", "userId", "userName", "accel", "backup", "source", "hostValues", "landingHint"} {
		if v, ok := mineRules[0].(map[string]interface{})[k]; ok {
			t.Fatalf("user view leaks %s: %v", k, v)
		}
	}
	// The internal 备线 error text stays with the admin.
	e.must(e.r.DB().Model(&model.LeiKwanRule{}).Where("id = ?", ruleID).Update("backup_error", "forward 777 create failed").Error)
	if msg := e.ruleList(e.user)["rules"].([]interface{})[0].(map[string]interface{})["backupError"]; msg != "备线还没建好，管理员处理中" {
		t.Fatalf("user backupError: %v", msg)
	}
	found := false
	for _, r := range e.ruleList(e.admin)["rules"].([]interface{}) {
		if m := r.(map[string]interface{}); num(m["id"]) == ruleID {
			found = m["backupError"] == "forward 777 create failed"
		}
	}
	if !found {
		t.Fatal("admin lost the raw 备线 error")
	}
	// bob (no grant) still sees nothing, although 利群 rules exist.
	if bob := e.ruleList(e.other); len(bob["rules"].([]interface{})) != 0 || bob["lines"] != nil {
		t.Fatalf("bob list: %v", bob)
	}

	// Disabled grant = no grant: flag off, list empty, options without lines.
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true, "status": 0})
	if e.granted(e.user) {
		t.Fatal("disabled grant: access flag true")
	}
	if off := e.ruleList(e.user); len(off["rules"].([]interface{})) != 0 {
		t.Fatalf("disabled grant list: %v", off)
	}
	if o := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{}); len(o["lines"].([]interface{})) != 0 || o["grantError"] != "利群汇聚授权已停用" {
		t.Fatalf("disabled grant options: %v", o)
	}
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true, "status": 1})
	if !e.granted(e.user) || len(e.ruleList(e.user)["rules"].([]interface{})) != 1 {
		t.Fatal("re-enabled grant: rule not visible again")
	}
}
