package contract_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

const ruleLimitSecret = "rule-limit-jwt"

// ruleLimitEnv is a dialect-neutral fixture: one entry node (mock agent
// session), one tunnel, normal users granted on it, admin token.
type ruleLimitEnv struct {
	r        *repo.Repository
	h        *handler.Handler
	router   http.Handler
	tunnelID int64
	admin    string
}

func newRuleLimitEnv(t *testing.T, dialect string) *ruleLimitEnv {
	t.Helper()
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	node := model.Node{Name: "rl-node", Secret: "rl-node-secret", ServerIP: "10.60.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	if err := r.DB().Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	tunnel := model.Tunnel{Name: "rl-tunnel", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1}
	if err := r.DB().Create(&tunnel).Error; err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	if err := r.DB().Create(&model.ChainTunnel{TunnelID: tunnel.ID, ChainType: "1", NodeID: node.ID, Port: sql.NullInt64{Int64: 20001, Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
		t.Fatalf("chain_tunnel: %v", err)
	}
	h := handler.New(r, ruleLimitSecret, "test")
	router := httpserver.NewRouter(h, ruleLimitSecret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	stop := startMockNodeSession(t, server.URL, node.Secret)
	t.Cleanup(stop)
	admin, _ := auth.GenerateToken(1, "admin", 0, ruleLimitSecret)
	return &ruleLimitEnv{r: r, h: h, router: router, tunnelID: tunnel.ID, admin: admin}
}

// addUser creates a normal user with rule allowance num, granted on the tunnel
// (per-tunnel num 1: it must not be enforced).
func (e *ruleLimitEnv) addUser(t *testing.T, id int64, num int) string {
	t.Helper()
	now := time.Now().UnixMilli()
	u := model.User{ID: id, User: fmt.Sprintf("rl_user_%d", id), Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: num, CreatedTime: now, Status: 1}
	if err := e.r.DB().Create(&u).Error; err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := e.r.DB().Create(&model.UserTunnel{UserID: id, TunnelID: e.tunnelID, Num: 1, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1}).Error; err != nil {
		t.Fatalf("user_tunnel: %v", err)
	}
	tok, _ := auth.GenerateToken(id, u.User, 1, ruleLimitSecret)
	return tok
}

func (e *ruleLimitEnv) seedRule(t *testing.T, userID int64, name string, status int) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	f := model.Forward{UserID: userID, UserName: fmt.Sprintf("rl_user_%d", userID), Name: name, TunnelID: e.tunnelID, RemoteAddr: "8.8.8.8:53", Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: status, Mode: "gost"}
	if err := e.r.DB().Create(&f).Error; err != nil {
		t.Fatalf("forward: %v", err)
	}
	return f.ID
}

func (e *ruleLimitEnv) call(t *testing.T, token, path string, body interface{}) response.R {
	t.Helper()
	return requestContractEnvelope(t, e.router, token, path, body)
}

func (e *ruleLimitEnv) count(t *testing.T, userID int64) int64 {
	t.Helper()
	var n int64
	if err := e.r.DB().Model(&model.Forward{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *ruleLimitEnv) create(t *testing.T, token, name string, extra map[string]interface{}) response.R {
	t.Helper()
	body := map[string]interface{}{"tunnelId": e.tunnelID, "name": name, "remoteAddr": "1.1.1.1:53", "strategy": "fifo"}
	for k, v := range extra {
		body[k] = v
	}
	return e.call(t, token, "/api/v1/forward/create", body)
}

const ruleLimitMsg2 = "已达到规则数上限（2 条），请删除不用的规则后再新建"

func TestUserRuleLimitRefusesNewRulesKeepsExisting(t *testing.T) {
	e := newRuleLimitEnv(t, "sqlite")
	tok := e.addUser(t, 100, 2)
	ids := []int64{e.seedRule(t, 100, "old-a", 1), e.seedRule(t, 100, "old-b", 0), e.seedRule(t, 100, "old-c", 1)}

	// Over the limit (3 > 2, one paused): every create path is refused, nothing written.
	ports := mustQueryInt64(t, e.r, "SELECT COUNT(*) FROM forward_port")
	if out := e.create(t, tok, "new-1", nil); out.Code == 0 || out.Msg != ruleLimitMsg2 {
		t.Fatalf("create over limit: %+v", out)
	}
	if out := e.create(t, tok, "new-idem", map[string]interface{}{"idempotencyKey": "idem-key-0001"}); out.Code == 0 || out.Msg != ruleLimitMsg2 {
		t.Fatalf("idempotent create over limit: %+v", out)
	}
	if n := e.count(t, 100); n != 3 {
		t.Fatalf("refused creates changed rule count: %d", n)
	}
	if n := mustQueryInt64(t, e.r, "SELECT COUNT(*) FROM forward_port"); n != ports {
		t.Fatalf("refused create wrote ports: %d -> %d", ports, n)
	}

	// Existing over-limit rules keep working: edit, pause, resume.
	if out := e.call(t, tok, "/api/v1/forward/update", map[string]interface{}{"id": ids[0], "name": "old-a-renamed", "tunnelId": e.tunnelID, "remoteAddr": "8.8.4.4:53"}); out.Code != 0 {
		t.Fatalf("update over-limit rule: %+v", out)
	}
	if out := e.call(t, tok, "/api/v1/forward/pause", map[string]interface{}{"id": ids[0]}); out.Code != 0 {
		t.Fatalf("pause over-limit rule: %+v", out)
	}
	if out := e.call(t, tok, "/api/v1/forward/resume", map[string]interface{}{"id": ids[1]}); out.Code != 0 {
		t.Fatalf("resume over-limit rule: %+v", out)
	}

	// Below the limit again: one create fits, the next is refused.
	if out := e.call(t, tok, "/api/v1/forward/delete", map[string]interface{}{"id": ids[2]}); out.Code != 0 {
		t.Fatalf("delete: %+v", out)
	}
	if out := e.call(t, tok, "/api/v1/forward/delete", map[string]interface{}{"id": ids[1]}); out.Code != 0 {
		t.Fatalf("delete: %+v", out)
	}
	if out := e.create(t, tok, "fits", nil); out.Code != 0 {
		t.Fatalf("create under limit: %+v", out)
	}
	if out := e.create(t, tok, "one-too-many", nil); out.Code == 0 || out.Msg != ruleLimitMsg2 {
		t.Fatalf("create at limit: %+v", out)
	}
	if n := e.count(t, 100); n != 2 {
		t.Fatalf("expected 2 rules, got %d", n)
	}

	// Admin creating a rule owned by the user is exempt.
	if out := e.create(t, e.admin, "by-admin", map[string]interface{}{"userId": 100}); out.Code != 0 {
		t.Fatalf("admin create for user: %+v", out)
	}
	if n := e.count(t, 100); n != 3 {
		t.Fatalf("admin create not stored: %d", n)
	}
}

func TestUserRuleLimitUnlimitedValues(t *testing.T) {
	e := newRuleLimitEnv(t, "sqlite")
	for _, tc := range []struct {
		id  int64
		num int
	}{{200, 0}, {201, 99999}} {
		tok := e.addUser(t, tc.id, tc.num)
		for i := 0; i < 3; i++ {
			e.seedRule(t, tc.id, fmt.Sprintf("seed-%d-%d", tc.id, i), 1)
		}
		if out := e.create(t, tok, fmt.Sprintf("unlimited-%d", tc.id), nil); out.Code != 0 {
			t.Fatalf("num=%d must be unlimited: %+v", tc.num, out)
		}
	}
}

// 20 parallel creates of one user never overshoot user.num, on both
// databases. The rate limit is switched off so the database check is what
// is being tested; a second round keeps it on (the shipped configuration).
func TestUserRuleLimitConcurrentCreates(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newRuleLimitEnv(t, dialect)
			const limit = 5
			for round, rateLimited := range []bool{false, true} {
				userID := int64(300 + round)
				tok := e.addUser(t, userID, limit)
				e.h.SetMutationRateLimitEnabled(rateLimited)
				var wg sync.WaitGroup
				results := make([]response.R, 20)
				for i := range results {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						results[i] = e.create(t, tok, fmt.Sprintf("par-%d-%d", userID, i), map[string]interface{}{"inPort": 20100 + round*50 + i})
					}(i)
				}
				wg.Wait()
				ok := 0
				for _, out := range results {
					switch {
					case out.Code == 0:
						ok++
					case strings.Contains(out.Msg, "规则数上限") || out.Msg == "操作太频繁，请稍后再试":
					default:
						t.Fatalf("unexpected refusal: %+v", out)
					}
				}
				if n := e.count(t, userID); n != int64(ok) || n > limit {
					t.Fatalf("rate=%v: %d rows, %d ok, limit %d", rateLimited, n, ok, limit)
				}
				if !rateLimited && ok != limit {
					t.Fatalf("expected exactly %d creates to win, got %d", limit, ok)
				}
			}
			e.h.SetMutationRateLimitEnabled(true)
		})
	}
}

// A normal user hammering pause/resume is cut off after the burst; refusals
// write nothing (DB or access log); admins are never limited; a batch call
// costs one token.
func TestMutationRateLimitRapidFire(t *testing.T) {
	e := newRuleLimitEnv(t, "sqlite")
	tok := e.addUser(t, 500, 100)
	id := e.seedRule(t, 500, "rapid", 1)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	refused, admitted := 0, 0
	for i := 0; i < 60; i++ {
		path := "/api/v1/forward/pause"
		if i%2 == 1 {
			path = "/api/v1/forward/resume"
		}
		out := e.call(t, tok, path, map[string]interface{}{"id": id})
		if out.Msg == "操作太频繁，请稍后再试" {
			if out.Code != 429 {
				t.Fatalf("refusal code %d", out.Code)
			}
			refused++
		} else {
			admitted++
		}
	}
	if refused < 20 || admitted < 30 {
		t.Fatalf("refused=%d admitted=%d", refused, admitted)
	}
	before := e.forward(t, id)
	time.Sleep(5 * time.Millisecond)
	if out := e.call(t, tok, "/api/v1/forward/pause", map[string]interface{}{"id": id}); out.Code != 429 {
		t.Fatalf("expected a refusal, got %+v", out)
	}
	if after := e.forward(t, id); after.UpdatedTime != before.UpdatedTime || after.Status != before.Status {
		t.Fatalf("refused call wrote to the rule: %+v -> %+v", before, after)
	}
	logged := strings.Count(buf.String(), "POST /api/v1/forward/pause") + strings.Count(buf.String(), "POST /api/v1/forward/resume")
	if logged != admitted {
		t.Fatalf("access log lines %d, admitted %d: refusals must not be logged", logged, admitted)
	}
	if n := strings.Count(buf.String(), "mutation rate limit"); n != 1 {
		t.Fatalf("summary lines %d, want 1", n)
	}

	// Admin: never limited.
	for i := 0; i < 60; i++ {
		if out := e.call(t, e.admin, "/api/v1/forward/pause", map[string]interface{}{"id": id}); out.Code == 429 {
			t.Fatalf("admin rate limited at call %d", i)
		}
	}

	// A batch call is one token, whatever its size.
	tok2 := e.addUser(t, 501, 100)
	ids := []int64{e.seedRule(t, 501, "b1", 1), e.seedRule(t, 501, "b2", 1), e.seedRule(t, 501, "b3", 1)}
	for i := 0; i < 30; i++ {
		if out := e.call(t, tok2, "/api/v1/forward/batch-pause", map[string]interface{}{"ids": ids}); out.Code == 429 {
			t.Fatalf("batch call %d refused: batch must cost one token", i)
		}
	}
	if out := e.call(t, tok2, "/api/v1/forward/batch-pause", map[string]interface{}{"ids": ids}); out.Code != 429 {
		t.Fatalf("31st batch call in a burst admitted: %+v", out)
	}
}
