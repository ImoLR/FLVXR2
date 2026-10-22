package contract_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 134 — global rule group, node rules and node-rule caps over HTTP, on
// SQLite and PostgreSQL.

func (l *landingLimitsLab) enableEgress() {
	l.t.Helper()
	if err := l.r.DB().Exec("UPDATE node SET landing_egress = 1, landing_guard = 2 WHERE id = ?", 641).Error; err != nil {
		l.t.Fatal(err)
	}
	if err := l.r.DB().Exec("UPDATE landing_server SET egress_ready = ? WHERE id = ?", true, l.server.ID).Error; err != nil {
		l.t.Fatal(err)
	}
}

func (l *landingLimitsLab) mustCreate(token, name string) int64 {
	l.t.Helper()
	out := l.create(token, name, nil)
	if out.Code != 0 {
		l.t.Fatalf("create %s: %+v", name, out)
	}
	return int64(out.Data.(map[string]interface{})["id"].(float64))
}

func nodeRules(n int, prefix string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for i := 0; i < n; i++ {
		out = append(out, map[string]interface{}{"name": fmt.Sprintf("%s%d", prefix, i), "enabled": true, "ports": fmt.Sprint(20000 + i), "action": "block"})
	}
	return out
}

func (l *landingLimitsLab) saveRules(token string, node int64, rules []map[string]interface{}) response.R {
	l.t.Helper()
	return l.call(token, "node/rules/save", map[string]interface{}{"id": node, "rules": rules})
}

func wantOK(t *testing.T, what string, out response.R) {
	t.Helper()
	if out.Code != 0 {
		t.Fatalf("%s: %+v", what, out)
	}
}

func wantRefused(t *testing.T, what string, out response.R, contains string) {
	t.Helper()
	if out.Code == 0 || !strings.Contains(out.Msg, contains) {
		t.Fatalf("%s: want refusal containing %q, got %+v", what, contains, out)
	}
}

func (l *landingLimitsLab) ruleCount(node int64) int {
	l.t.Helper()
	var n model.LandingNode
	if err := l.r.DB().Select("route_rule_count").First(&n, node).Error; err != nil {
		l.t.Fatal(err)
	}
	return n.RouteRuleCount
}

func TestLandingNodeRuleCapsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			a := &model.User{ID: 561, User: "rules-a", Flow: 100, Status: 1, RoleID: 1}
			b := &model.User{ID: 562, User: "rules-b", Flow: 100, Status: 1, RoleID: 1}
			l := newLandingLimitsLab(t, dialect, a, b)
			l.grant(561, 562)
			l.enableEgress()
			ta, tb := l.token(a), l.token(b)
			n1, n2, n3 := l.mustCreate(ta, "a1"), l.mustCreate(ta, "a2"), l.mustCreate(ta, "a3")
			b1 := l.mustCreate(tb, "b1")

			// Global defaults 5 per node, 10 per user.
			v := l.userView(561)
			if v["ruleMaxNode"].(float64) != 5 || v["ruleMaxTotal"].(float64) != 10 || v["ruleMaxNodeSource"] != "global" {
				t.Fatalf("defaults: %+v", v)
			}
			wantOK(t, "5 rules", l.saveRules(ta, n1, nodeRules(5, "a")))
			wantRefused(t, "6 rules", l.saveRules(ta, n1, nodeRules(6, "a")), "每个节点最多 5 条")
			wantOK(t, "5 more", l.saveRules(ta, n2, nodeRules(5, "b")))
			wantRefused(t, "11th rule", l.saveRules(ta, n3, nodeRules(1, "c")), "节点规则总数已达上限")
			// Another user's node.
			wantRefused(t, "foreign node", l.saveRules(tb, n1, nodeRules(1, "x")), "无权")
			if out := l.call(tb, "node/rules/get", map[string]interface{}{"id": n1}); out.Code == 0 {
				t.Fatal("foreign node readable")
			}
			// Per-user custom caps.
			wantOK(t, "custom caps", l.call(l.admin, "user/save", map[string]interface{}{"userId": 561, "ruleMaxNodeSource": "custom", "ruleMaxNode": 8, "ruleMaxTotalSource": "custom", "ruleMaxTotal": 20}))
			if v := l.userView(561); v["ruleMaxNode"].(float64) != 8 || v["ruleMaxTotalSource"] != "custom" || v["ruleUsed"].(float64) != 10 {
				t.Fatalf("custom view: %+v", v)
			}
			if v := l.userView(561); v["maxNodesSource"] != "global" {
				t.Fatalf("rule caps save changed the node cap: %+v", v)
			}
			wantOK(t, "8 rules custom", l.saveRules(ta, n3, nodeRules(8, "c")))
			// 0 total = not open.
			wantOK(t, "zero", l.call(l.admin, "user/save", map[string]interface{}{"userId": 562, "ruleMaxTotalSource": "custom", "ruleMaxTotal": 0}))
			wantRefused(t, "zero cap", l.saveRules(tb, b1, nodeRules(1, "z")), "管理员未开放")
			view := l.call(tb, "node/rules/get", map[string]interface{}{"id": b1})
			wantOK(t, "view", view)
			if caps := view.Data.(map[string]interface{})["caps"].(map[string]interface{}); caps["total"].(float64) != 0 {
				t.Fatalf("view caps: %+v", caps)
			}
			// Admin is not capped on a user's node.
			wantOK(t, "admin uncapped", l.saveRules(l.admin, b1, nodeRules(12, "adm")))
			// Lowering keeps existing rules; edits that do not add stay possible.
			wantOK(t, "back to global", l.call(l.admin, "user/save", map[string]interface{}{"userId": 561, "ruleMaxNodeSource": "global", "ruleMaxTotalSource": "global"}))
			if l.ruleCount(n3) != 8 {
				t.Fatal("lowering the cap removed rules")
			}
			wantOK(t, "edit over cap", l.saveRules(ta, n3, nodeRules(8, "renamed")))
			wantRefused(t, "add over cap", l.saveRules(ta, n3, nodeRules(9, "c")), "每个节点最多 5 条")
			wantOK(t, "shrink over cap", l.saveRules(ta, n3, nodeRules(7, "c")))
			// Global caps change (users following them pick it up).
			wantOK(t, "global caps", l.call(l.admin, "egress/user-caps/save", map[string]interface{}{"perNode": 3, "total": 4}))
			if v := l.userView(561); v["ruleMaxNode"].(float64) != 3 || v["ruleMaxTotal"].(float64) != 4 || v["globalRuleMaxNode"].(float64) != 3 {
				t.Fatalf("global caps view: %+v", v)
			}
			if out := l.call(ta, "egress/user-caps/save", map[string]interface{}{"perNode": 30, "total": 40}); out.Code != 403 {
				t.Fatalf("user changed global caps: %+v", out)
			}
			// Unpublished outbound refused for users; publishing opens it.
			out := l.call(l.admin, "egress/outbound/save", map[string]interface{}{"name": "v4", "type": "direct", "ipVersion": "force_ipv4"})
			wantOK(t, "outbound", out)
			oid := int64(out.Data.(map[string]interface{})["id"].(float64))
			via := []map[string]interface{}{{"name": "via", "enabled": true, "ports": "443", "action": fmt.Sprintf("outbound:%d", oid)}}
			wantOK(t, "free space", l.saveRules(ta, n1, nodeRules(0, "")))
			wantOK(t, "free space", l.saveRules(ta, n2, nodeRules(0, "")))
			wantOK(t, "free space", l.saveRules(ta, n3, nodeRules(0, "")))
			wantRefused(t, "unpublished", l.saveRules(ta, n1, via), "未开放")
			wantOK(t, "publish", l.call(l.admin, "egress/outbound/save", map[string]interface{}{"id": oid, "name": "v4", "type": "direct", "ipVersion": "force_ipv4", "userAllowed": true}))
			wantOK(t, "published", l.saveRules(ta, n1, via))
			wantRefused(t, "outbound in use", l.call(l.admin, "egress/outbound/delete", map[string]interface{}{"id": oid}), "a1")
			// Un-publishing keeps the user's existing rule; new references refused.
			wantOK(t, "unpublish", l.call(l.admin, "egress/outbound/save", map[string]interface{}{"id": oid, "name": "v4", "type": "direct", "ipVersion": "force_ipv4", "userAllowed": false}))
			var stored model.LandingNode
			if err := l.r.DB().Select("route_rules").First(&stored, n1).Error; err != nil {
				t.Fatal(err)
			}
			var kept []map[string]interface{}
			_ = json.Unmarshal([]byte(stored.RouteRules), &kept)
			kept[0]["name"] = "via renamed"
			wantOK(t, "keep unpublished", l.saveRules(ta, n1, kept))
			wantRefused(t, "new unpublished", l.saveRules(ta, n2, via), "未开放")
			// Delete: pending deletion stops counting, the row takes the rules.
			wantOK(t, "two rules", l.saveRules(ta, n2, nodeRules(2, "d")))
			if v := l.userView(561); v["ruleUsed"].(float64) != 3 {
				t.Fatalf("used before delete: %+v", v["ruleUsed"])
			}
			// Shared delete path: delete_pending until Custom confirms.
			if err := l.r.DB().Exec("UPDATE landing_node SET delete_pending = ? WHERE id = ?", true, n2).Error; err != nil {
				t.Fatal(err)
			}
			if v := l.userView(561); v["ruleUsed"].(float64) != 1 {
				t.Fatalf("delete-pending node still counted: %+v", v["ruleUsed"])
			}
			if err := l.r.DeleteLandingNode(n2); err != nil {
				t.Fatal(err)
			}
			var left int64
			if err := l.r.DB().Model(&model.LandingNode{}).Where("id = ?", n2).Count(&left).Error; err != nil || left != 0 {
				t.Fatalf("node row (and its rules) left: %d %v", left, err)
			}
		})
	}
}

// 20 parallel saves never exceed the caps (one locked transaction each).
func TestLandingNodeRuleCapsParallelContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			c := &model.User{ID: 571, User: "rules-parallel", Flow: 100, Status: 1, RoleID: 1}
			l := newLandingLimitsLab(t, dialect, c)
			l.grant(571)
			l.enableEgress()
			wantOK(t, "node cap", l.call(l.admin, "user/save", map[string]interface{}{"userId": 571, "maxNodesSource": "custom", "maxNodes": 30}))
			tc := l.token(c)
			nodes := []int64{}
			for i := 0; i < 20; i++ {
				nodes = append(nodes, l.mustCreate(tc, fmt.Sprintf("p%d", i)))
			}
			var okCount int32
			var wg sync.WaitGroup
			for i, id := range nodes {
				wg.Add(1)
				go func(i int, id int64) {
					defer wg.Done()
					if out := l.saveRules(tc, id, nodeRules(1, fmt.Sprintf("n%d-", i))); out.Code == 0 {
						atomic.AddInt32(&okCount, 1)
					}
				}(i, id)
			}
			wg.Wait()
			var total int64
			if err := l.r.DB().Model(&model.LandingNode{}).Select("COALESCE(SUM(route_rule_count), 0)").Where("user_id = ?", 571).Scan(&total).Error; err != nil {
				t.Fatal(err)
			}
			if okCount != 10 || total != 10 {
				t.Fatalf("parallel saves: ok=%d stored=%d (cap 10)", okCount, total)
			}
			// Same node, 20 parallel saves over the per-node cap: all refused.
			var target int64
			for _, id := range nodes {
				if l.ruleCount(id) == 1 {
					target = id
					break
				}
			}
			okCount = 0
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if out := l.saveRules(tc, target, nodeRules(6, "x")); out.Code == 0 {
						atomic.AddInt32(&okCount, 1)
					}
				}()
			}
			wg.Wait()
			if okCount != 0 || l.ruleCount(target) != 1 {
				t.Fatalf("over-cap parallel saves on one node: ok=%d count=%d", okCount, l.ruleCount(target))
			}
		})
	}
}

// Owner transfer moves the rules with the node; over the target's caps it
// is refused with a clear message.
func TestLandingNodeRulesTransferContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			a := &model.User{ID: 581, User: "rules-from", Flow: 100, Status: 1, RoleID: 1}
			b := &model.User{ID: 582, User: "rules-to", Flow: 100, Status: 1, RoleID: 1}
			l := newLandingLimitsLab(t, dialect, a, b)
			l.grant(581, 582)
			l.enableEgress()
			wantOK(t, "a caps", l.call(l.admin, "user/save", map[string]interface{}{"userId": 581, "ruleMaxNodeSource": "custom", "ruleMaxNode": 8}))
			ta := l.token(a)
			n := l.mustCreate(ta, "moving")
			wantOK(t, "6 rules", l.saveRules(ta, n, nodeRules(6, "m")))
			wantRefused(t, "transfer over cap", l.call(l.admin, "node/transfer", map[string]interface{}{"id": n, "userId": 582}), "节点规则上限")
			wantOK(t, "shrink", l.saveRules(ta, n, nodeRules(4, "m")))
			wantOK(t, "transfer", l.call(l.admin, "node/transfer", map[string]interface{}{"id": n, "userId": 582}))
			if v := l.userView(582); v["ruleUsed"].(float64) != 4 {
				t.Fatalf("target total: %+v", v["ruleUsed"])
			}
			if v := l.userView(581); v["ruleUsed"].(float64) != 0 {
				t.Fatalf("source total: %+v", v["ruleUsed"])
			}
			view := l.call(l.token(b), "node/rules/get", map[string]interface{}{"id": n})
			wantOK(t, "new owner view", view)
			if rules := view.Data.(map[string]interface{})["rules"].([]interface{}); len(rules) != 4 {
				t.Fatalf("rules after transfer: %d", len(rules))
			}
			if out := l.call(ta, "node/rules/get", map[string]interface{}{"id": n}); out.Code == 0 {
				t.Fatal("old owner still reads the node rules")
			}
		})
	}
}

// Global save: admin only, validated, and every native server is pushed
// once after a burst of saves (debounce), on both dialects.
func TestLandingGlobalRulesContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			u := &model.User{ID: 591, User: "rules-global", Flow: 100, Status: 1, RoleID: 1}
			l := newLandingLimitsLab(t, dialect, u)
			l.grant(591)
			l.enableEgress()
			l.mustCreate(l.token(u), "g1")
			if out := l.call(l.token(u), "egress/global/save", map[string]interface{}{}); out.Code != 403 {
				t.Fatalf("user saved global: %+v", out)
			}
			bad := map[string]interface{}{"forced": []map[string]interface{}{{"name": "x", "enabled": true, "action": "block"}}}
			wantRefused(t, "no matcher", l.call(l.admin, "egress/global/save", bad), "匹配条件")
			// Second native server on its own node; both nodes connect as mock agents.
			if err := l.r.DB().Exec("UPDATE node SET secret = ? WHERE id = ?", "layers-secret-1", 641).Error; err != nil {
				t.Fatal(err)
			}
			if err := l.r.DB().Create(&model.Node{ID: 642, Name: "native-2", Secret: "layers-secret-2", ServerIP: "203.0.113.42", Port: "10000-19999", LandingCapable: true, LandingEgress: 1, LandingGuard: 2}).Error; err != nil {
				t.Fatal(err)
			}
			if out := l.call(l.admin, "server/enable", map[string]interface{}{"nodeId": 642}); out.Code != 0 {
				t.Fatalf("enable 2: %+v", out)
			}
			srv := httptest.NewServer(l.router)
			defer srv.Close()
			var mu sync.Mutex
			applies := map[string]int{}
			for _, sec := range []string{"layers-secret-1", "layers-secret-2"} {
				sec := sec
				stop := startMockNodeSessionWithCommandRecorder(t, srv.URL, sec, func(cmd string, _ json.RawMessage) (bool, string) {
					if cmd == "ApplyLandingDesired" {
						mu.Lock()
						applies[sec]++
						mu.Unlock()
					}
					return false, ""
				})
				defer stop()
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				var online int64
				l.r.DB().Model(&model.Node{}).Where("id IN ? AND status = 1", []int64{641, 642}).Count(&online)
				if online == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("mock agents did not come online")
				}
				time.Sleep(50 * time.Millisecond)
			}
			l.h.WaitEgressSync()
			time.Sleep(300 * time.Millisecond)
			mu.Lock()
			base := map[string]int{"layers-secret-1": applies["layers-secret-1"], "layers-secret-2": applies["layers-secret-2"]}
			mu.Unlock()
			l.h.SetLandingLayerApplyDelay(400 * time.Millisecond)
			for i := 0; i < 5; i++ {
				g := map[string]interface{}{"forced": []map[string]interface{}{{"name": fmt.Sprintf("强制 %d", i), "enabled": true, "domains": []string{"blocked.example"}, "action": "block"}}, "defaultAction": "direct"}
				wantOK(t, "global save", l.call(l.admin, "egress/global/save", g))
			}
			l.h.WaitEgressSync()
			mu.Lock()
			d1, d2 := applies["layers-secret-1"]-base["layers-secret-1"], applies["layers-secret-2"]-base["layers-secret-2"]
			mu.Unlock()
			if d1 != 1 || d2 != 1 {
				t.Fatalf("pushes after 5 rapid saves: server1=%d server2=%d (want 1 each)", d1, d2)
			}
			out := l.call(l.admin, "egress/global/get", map[string]interface{}{})
			wantOK(t, "global get", out)
			g := out.Data.(map[string]interface{})
			if forced := g["forced"].([]interface{}); len(forced) != 1 || forced[0].(map[string]interface{})["name"] != "强制 4" {
				t.Fatalf("stored global: %+v", g["forced"])
			}
			if servers := g["servers"].([]interface{}); len(servers) != 2 {
				t.Fatalf("status rows: %+v", servers)
			}
		})
	}
}
