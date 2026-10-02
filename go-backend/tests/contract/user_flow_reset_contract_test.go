package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
)

func seedUserFlowResetContractData(t *testing.T, r *repo.Repository, nowMs int64) {
	t.Helper()
	stmts := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
			VALUES(1, 't1', 1.0, 1, 'tls', 2, ?, ?, 1, NULL, 0)`, []interface{}{nowMs, nowMs}},
		{`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
			VALUES(2, 'reset_user', 'pwd', 1, 2727251700000, 99999, 1000, 2000, 0, 99, ?, ?, 1)`, []interface{}{nowMs, nowMs}},
		{`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
			VALUES(3, 'other_user', 'pwd', 1, 2727251700000, 99999, 5000, 6000, 0, 99, ?, ?, 1)`, []interface{}{nowMs, nowMs}},
		{`INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
			VALUES(10, 2, 1, NULL, 99, 99999, 300, 400, 0, 2727251700000, 1)`, nil},
		{`INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
			VALUES(11, 3, 1, NULL, 99, 99999, 700, 800, 0, 2727251700000, 1)`, nil},
		{`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
			VALUES(20, 2, 'reset_user', 'rule-a', 1, '1.1.1.1:443', 'fifo', 100, 200, ?, ?, 1, 0)`, []interface{}{nowMs, nowMs}},
		{`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
			VALUES(21, 2, 'reset_user', 'rule-idle', 1, '1.1.1.1:443', 'fifo', 0, 0, ?, ?, 1, 1)`, []interface{}{nowMs, nowMs}},
		{`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
			VALUES(30, 3, 'other_user', 'rule-other', 1, '1.1.1.1:443', 'fifo', 500, 600, ?, ?, 1, 2)`, []interface{}{nowMs, nowMs}},
	}
	for _, s := range stmts {
		if err := r.DB().Exec(s.query, s.args...).Error; err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}
}

func postUserFlowReset(t *testing.T, router http.Handler, token, path, body string) response.R {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	var out response.R
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s response: %v", path, err)
	}
	return out
}

func TestUserResetFlowAlsoResetsUserForwards(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)
	seedUserFlowResetContractData(t, r, time.Now().UnixMilli())
	token, err := auth.GenerateToken(1, "admin", 0, secret)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	// A single user_tunnel reset keeps the rules.
	if out := postUserFlowReset(t, router, token, "/api/v1/user/reset", `{"id":10,"type":2}`); out.Code != 0 {
		t.Fatalf("user_tunnel reset failed: code=%d msg=%q", out.Code, out.Msg)
	}
	if in, out := mustQueryInt64Int(t, r, `SELECT in_flow, out_flow FROM forward WHERE id = 20`); in != 100 || out != 200 {
		t.Fatalf("user_tunnel reset changed forward 20: in=%d out=%d", in, out)
	}

	// The user reset clears the user, its user_tunnels and its rules.
	if out := postUserFlowReset(t, router, token, "/api/v1/user/reset", `{"id":2,"type":1}`); out.Code != 0 {
		t.Fatalf("user reset failed: code=%d msg=%q", out.Code, out.Msg)
	}
	if got := mustQueryInt64(t, r, `SELECT in_flow + out_flow FROM user WHERE id = 2`); got != 0 {
		t.Fatalf("user total not cleared: %d", got)
	}
	if got := mustQueryInt64(t, r, `SELECT SUM(in_flow + out_flow) FROM user_tunnel WHERE user_id = 2`); got != 0 {
		t.Fatalf("user_tunnel traffic not cleared: %d", got)
	}
	if got := mustQueryInt64(t, r, `SELECT SUM(in_flow + out_flow) FROM forward WHERE user_id = 2`); got != 0 {
		t.Fatalf("forward traffic not cleared: %d", got)
	}
	if got := mustQueryInt(t, r, `SELECT COUNT(*) FROM forward_traffic_reset_log WHERE forward_id = 21`); got != 0 {
		t.Fatalf("idle forward got %d reset logs", got)
	}
	inBefore := mustQueryInt64(t, r, `SELECT in_flow_before FROM forward_traffic_reset_log WHERE forward_id = 20`)
	outBefore := mustQueryInt64(t, r, `SELECT out_flow_before FROM forward_traffic_reset_log WHERE forward_id = 20`)
	reason := mustQueryString(t, r, `SELECT reason FROM forward_traffic_reset_log WHERE forward_id = 20`)
	operator := mustQueryString(t, r, `SELECT operator_name FROM forward_traffic_reset_log WHERE forward_id = 20`)
	if inBefore != 100 || outBefore != 200 || !strings.HasPrefix(reason, "用户流量归零联动") || operator != "admin_user" {
		t.Fatalf("unexpected forward reset log: in=%d out=%d reason=%q operator=%q", inBefore, outBefore, reason, operator)
	}

	// Other users are untouched.
	if got := mustQueryInt64(t, r, `SELECT in_flow + out_flow FROM forward WHERE id = 30`); got != 1100 {
		t.Fatalf("other user's forward changed: %d", got)
	}
	if got := mustQueryInt64(t, r, `SELECT in_flow + out_flow FROM user_tunnel WHERE id = 11`); got != 1500 {
		t.Fatalf("other user's user_tunnel changed: %d", got)
	}

	// Batch reset: the existing user is reset with its rules, the missing one fails.
	out := postUserFlowReset(t, router, token, "/api/v1/user/batch-reset", `{"ids":[3,999]}`)
	if out.Code != 0 {
		t.Fatalf("batch reset failed: code=%d msg=%q", out.Code, out.Msg)
	}
	data, _ := out.Data.(map[string]interface{})
	if valueAsInt(data["successCount"]) != 1 || valueAsInt(data["failCount"]) != 1 {
		t.Fatalf("unexpected batch reset result: %#v", out.Data)
	}
	if got := mustQueryInt64(t, r, `SELECT in_flow + out_flow FROM forward WHERE id = 30`); got != 0 {
		t.Fatalf("batch reset did not clear forward 30: %d", got)
	}
	if got := mustQueryInt(t, r, `SELECT COUNT(*) FROM forward_traffic_reset_log WHERE forward_id = 30`); got != 1 {
		t.Fatalf("expected 1 reset log for forward 30, got %d", got)
	}

	// Resetting a missing user reports an error.
	if out := postUserFlowReset(t, router, token, "/api/v1/user/reset", `{"id":999,"type":1}`); out.Code == 0 {
		t.Fatalf("expected an error for a missing user")
	}
}
