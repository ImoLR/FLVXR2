package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func TestLandingTargetListQueryCount(t *testing.T) {
	for _, count := range []int{1, 6} {
		for _, shared := range []bool{false, true} {
			for _, role := range []int{0, 1} {
				t.Run(fmt.Sprintf("nodes=%d/shared=%v/role=%d", count, shared, role), func(t *testing.T) {
					e := newFlowTestEnv(t)
					e.addUser(2)
					seedHandlerLanding(t, e, 2, 2, 1)
					e.exec("INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (1, 2)")
					if shared {
						e.exec(`UPDATE landing_server SET source='shared', node_id=0, remote_server_id=7, shared_caps='{"status":"active"}' WHERE id=1`)
					}
					for i := 2; i <= count; i++ {
						n := model.LandingNode{ID: int64(i), UserID: 2, LandingServerID: 1, Name: fmt.Sprint("node", i), Protocol: "ss2022", Port: 30000 + i, Status: "active"}
						if err := e.r.DB().Create(&n).Error; err != nil {
							t.Fatal(err)
						}
					}
					e.addForward(20, 2, 0)
					e.addForward(21, 2, 0)
					e.exec("UPDATE forward SET landing_node_id=1 WHERE id IN (20,21)")
					queries := 0
					grouped := 0
					countQuery := func(tx *gorm.DB) {
						queries++
						if strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "group by") {
							grouped++
						}
					}
					if err := e.r.DB().Callback().Query().After("gorm:query").Register("landing-target-count", countQuery); err != nil {
						t.Fatal(err)
					}
					if err := e.r.DB().Callback().Row().After("gorm:row").Register("landing-target-count", countQuery); err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest("POST", "/api/v1/landing/node/list", strings.NewReader("{}"))
					r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "2", RoleID: role}))
					w := httptest.NewRecorder()
					e.h.landingNodeList(w, r)
					var out struct {
						Code int
						Data []map[string]any
					}
					if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 || len(out.Data) != count {
						t.Fatalf("%s %v", w.Body.String(), err)
					}
					wantQueries := 7
					if shared {
						wantQueries++
					}
					if queries != wantQueries || grouped != 1 {
						t.Fatalf("list API: queries=%d grouped=%d, want %d and 1", queries, grouped, wantQueries)
					}
					for i, node := range out.Data {
						want := float64(0)
						if i == 0 {
							want = 2
						}
						if node["forwardCount"] != want || node["serverAvailable"] != true {
							t.Fatalf("incorrect list decoration: %+v", node)
						}
					}
					t.Logf("list API: %d queries", queries)
				})
			}
		}
	}
}

func landingTargetHandlerEnv(t *testing.T) (*flowTestEnv, *model.LandingServer) {
	t.Helper()
	e, _ := newChainEnv(t)
	s, err := e.r.EnableLandingServer(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.SaveLandingServer(s, nil); err != nil {
		t.Fatal(err)
	}
	e.exec("INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (?, 7)", s.ID)
	return e, s
}

func TestLandingTargetConcurrentRulesAndEditingReuse(t *testing.T) {
	e, s := landingTargetHandlerEnv(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, map[string]interface{}{
				"tunnelId": 30, "name": fmt.Sprint("shared-rule-", i), "inPort": 1700 + i,
				"landingServerId": s.ID, "landingProtocol": "ss2022",
			})
			if code != 0 {
				t.Errorf("concurrent rule: %s", msg)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	nodes, err := e.r.ListLandingNodes(7, false)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("concurrent rules created %+v, error=%v", nodes, err)
	}
	var rules []model.Forward
	if err := e.r.DB().Where("user_id = ?", 7).Order("id").Find(&rules).Error; err != nil || len(rules) != 2 {
		t.Fatalf("rules=%+v error=%v", rules, err)
	}
	for _, rule := range rules {
		if rule.LandingNodeID != nodes[0].ID {
			t.Fatalf("rule references another node: %+v", rule)
		}
	}
	for _, target := range []map[string]interface{}{{}, {"landingNodeId": nodes[0].ID}, {"landingServerId": s.ID}} {
		target["id"], target["name"] = rules[0].ID, "renamed"
		code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, target)
		if code != 0 {
			t.Fatalf("editing existing rule: %s", msg)
		}
	}
	after, _ := e.r.ListLandingNodes(7, false)
	if len(after) != 1 || after[0].ID != nodes[0].ID {
		t.Fatalf("editing created another landing node: %+v", after)
	}
}

func TestLandingTargetRollbackOnlyOwnCreation(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("update=%v/existing=%v", update, existing), func(t *testing.T) {
				e, s := landingTargetHandlerEnv(t)
				var old *model.LandingNode
				if existing {
					var err error
					old, err = e.r.CreateLandingNode(7, s.ID, "keep", "ss2022", false)
					if err != nil {
						t.Fatal(err)
					}
				}
				body := map[string]interface{}{"tunnelId": 30, "name": "invalid port", "inPort": 900, "landingServerId": s.ID}
				var action http.HandlerFunc = e.h.forwardCreate
				if update {
					e.addForward(700, 7, 30)
					e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(700, 5, 1800)")
					body["id"], action = 700, e.h.forwardUpdate
				}
				code, msg, _ := callAs(t, action, 7, 1, body)
				if code == 0 || !strings.Contains(msg, "端口") {
					t.Fatalf("expected post-lookup port refusal: %d %s", code, msg)
				}
				nodes, err := e.r.ListLandingNodes(7, false)
				want := 0
				if existing {
					want = 1
				}
				if err != nil || len(nodes) != want || (existing && nodes[0].ID != old.ID) {
					t.Fatalf("rollback nodes=%+v expected=%d err=%v", nodes, want, err)
				}
			})
		}
	}
}

func TestLandingTargetLineMachineProtocolAndPortImmutable(t *testing.T) {
	e, s := landingTargetHandlerEnv(t)
	_, id, created, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID, "landingProtocol": "ss2022"}, 7, 10, "", false)
	if err != nil || id == 0 || created != id {
		t.Fatalf("line-machine creation: id=%d created=%d err=%v", id, created, err)
	}
	n, _ := e.r.GetLandingNode(id)
	if n.Protocol != "vless_reality_vision" {
		t.Fatalf("line-machine created protocol %s", n.Protocol)
	}
	_, reused, created, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingServerId": s.ID}, 7, 10, "", false)
	if err != nil || reused != id || created != 0 {
		t.Fatalf("line-machine reuse: id=%d created=%d err=%v", reused, created, err)
	}
	e.addForward(700, 7, 10)
	e.exec("UPDATE forward SET landing_node_id=? WHERE id=700", id)
	for _, change := range []map[string]interface{}{{"protocol": "ss2022"}, {"port": n.Port + 1}} {
		change["id"] = id
		code, msg, _ := callAs(t, e.h.landingNodeUpdate, 1, 0, change)
		if code == 0 || !strings.Contains(msg, "协议和端口不可修改") {
			t.Fatalf("linked node mutation: %d %s", code, msg)
		}
	}
	after, _ := e.r.GetLandingNode(id)
	if after.Protocol != n.Protocol || after.Port != n.Port {
		t.Fatalf("immutable identity changed: %+v", after)
	}
}
