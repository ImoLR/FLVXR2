package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Helpers shared by the flow accounting tests. They only use APIs that also exist in
// 3.0.27-fork.7, so the regression tests can be run against that release to show the bugs.

type flowTestEnv struct {
	t   *testing.T
	r   *repo.Repository
	h   *Handler
	now int64
}

func newFlowTestEnv(t *testing.T) *flowTestEnv {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return &flowTestEnv{
		t:   t,
		r:   r,
		h:   &Handler{repo: r},
		now: time.Now().UnixMilli(),
	}
}

func (e *flowTestEnv) exec(sql string, args ...interface{}) {
	e.t.Helper()
	if err := e.r.DB().Exec(sql, args...).Error; err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *flowTestEnv) addUser(id int64) {
	e.exec(`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
		VALUES(?, ?, 'x', 1, ?, 99999, 0, 0, 1, 10, ?, ?, 1)`, id, "u"+time.Now().Format("150405.000000000"), e.now+86400000, e.now, e.now)
}

func (e *flowTestEnv) addTunnel(id int64, flowMode int64, ratio float64) {
	e.exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, ?, ?, 1, 'tls', ?, ?, ?, 1, NULL, 0)`, id, "t", ratio, flowMode, e.now, e.now)
}

func (e *flowTestEnv) addUserTunnel(id, userID, tunnelID int64) {
	e.exec(`INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		VALUES(?, ?, ?, NULL, 10, 99999, 0, 0, 1, ?, 1)`, id, userID, tunnelID, e.now+86400000)
}

func (e *flowTestEnv) addForward(id, userID, tunnelID int64) {
	e.exec(`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
		VALUES(?, ?, 'u', 'f', ?, '1.1.1.1:443', 'fifo', 0, 0, ?, ?, 1, 0)`, id, userID, tunnelID, e.now, e.now)
}

func (e *flowTestEnv) addNode(id int64, secret string) {
	e.exec(`INSERT INTO node(id, name, secret, server_ip, port, created_time, status) VALUES(?, 'n', ?, '127.0.0.1', '1000-2000', ?, 1)`, id, secret, e.now)
}

func (e *flowTestEnv) flows(table string, id int64) (int64, int64) {
	e.t.Helper()
	var row struct {
		InFlow  int64
		OutFlow int64
	}
	if err := e.r.DB().Table(table).Select("in_flow, out_flow").Where("id = ?", id).Take(&row).Error; err != nil {
		e.t.Fatalf("read %s %d flows: %v", table, id, err)
	}
	return row.InFlow, row.OutFlow
}

func (e *flowTestEnv) expectFlows(table string, id, wantIn, wantOut int64) {
	e.t.Helper()
	in, out := e.flows(table, id)
	if in != wantIn || out != wantOut {
		e.t.Fatalf("%s %d flows = (%d, %d), want (%d, %d)", table, id, in, out, wantIn, wantOut)
	}
}

func (e *flowTestEnv) monthlyQuotaUsed(userID int64) int64 {
	e.t.Helper()
	var q model.UserQuota
	if err := e.r.DB().Where("user_id = ?", userID).Take(&q).Error; err != nil {
		e.t.Fatalf("read quota of user %d: %v", userID, err)
	}
	return q.MonthlyUsedBytes
}

func (e *flowTestEnv) quotaRows(userID int64) int64 {
	e.t.Helper()
	var n int64
	if err := e.r.DB().Model(&model.UserQuota{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		e.t.Fatalf("count quota rows of user %d: %v", userID, err)
	}
	return n
}

func (e *flowTestEnv) forwardStatus(id int64) int {
	e.t.Helper()
	var row struct{ Status int }
	if err := e.r.DB().Table("forward").Select("status").Where("id = ?", id).Take(&row).Error; err != nil {
		e.t.Fatalf("read forward %d status: %v", id, err)
	}
	return row.Status
}

func flowItemsJSON(t *testing.T, items []flowItem) []byte {
	t.Helper()
	plain, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// encryptFlowUpload builds the body an agent with a node secret sends.
func encryptFlowUpload(t *testing.T, secret string, items []flowItem) []byte {
	t.Helper()
	c, err := security.NewAESCrypto(secret)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Encrypt(flowItemsJSON(t, items))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"encrypted": true, "data": data, "timestamp": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// postFlowUpload posts an agent flow upload and waits for the follow-up enforcement.
func postFlowUpload(h *Handler, secret string, body []byte) (int, string) {
	req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret="+secret, bytes.NewReader(body))
	res := httptest.NewRecorder()
	h.flowUpload(res, req)
	waitFlowEnforcement(h)
	return res.Code, res.Body.String()
}

// upload posts items as an encrypted agent upload and requires an "ok".
func (e *flowTestEnv) upload(secret string, items ...flowItem) {
	e.t.Helper()
	code, text := postFlowUpload(e.h, secret, encryptFlowUpload(e.t, secret, items))
	if code != http.StatusOK || text != "ok" {
		e.t.Fatalf("upload %v: %d %q", items, code, text)
	}
}
