package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestBillTunnelFlow(t *testing.T) {
	tests := []struct {
		name             string
		mode             int64
		ratio            float64
		upload, download int64
		wantIn, wantOut  int64
	}{
		{name: "two-way bills both directions once", mode: 2, ratio: 1, upload: 1000, download: 3000, wantIn: 1000, wantOut: 3000},
		{name: "two-way applies ratio", mode: 2, ratio: 0.5, upload: 1000, download: 3000, wantIn: 500, wantOut: 1500},
		{name: "two-way fractional ratio truncates", mode: 2, ratio: 1.5, upload: 3, download: 5, wantIn: 4, wantOut: 7},
		{name: "one-way bills larger download only", mode: 1, ratio: 1, upload: 1000, download: 3000, wantIn: 0, wantOut: 3000},
		{name: "one-way bills larger upload only", mode: 1, ratio: 1, upload: 5000, download: 200, wantIn: 5000, wantOut: 0},
		{name: "one-way tie goes to upload", mode: 1, ratio: 1, upload: 700, download: 700, wantIn: 700, wantOut: 0},
		{name: "one-way applies ratio", mode: 1, ratio: 2, upload: 10, download: 40, wantIn: 0, wantOut: 80},
		{name: "unknown mode is two-way", mode: 3, ratio: 1, upload: 10, download: 40, wantIn: 10, wantOut: 40},
		{name: "zero ratio treated as one", mode: 2, ratio: 0, upload: 10, download: 40, wantIn: 10, wantOut: 40},
		{name: "negative bytes ignored", mode: 2, ratio: 1, upload: -10, download: 40, wantIn: 0, wantOut: 40},
		{name: "no traffic", mode: 1, ratio: 1, upload: 0, download: 0, wantIn: 0, wantOut: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, out := billTunnelFlow(tc.mode, tc.ratio, tc.upload, tc.download)
			if in != tc.wantIn || out != tc.wantOut {
				t.Fatalf("billTunnelFlow(%d, %v, %d, %d) = (%d, %d), want (%d, %d)",
					tc.mode, tc.ratio, tc.upload, tc.download, in, out, tc.wantIn, tc.wantOut)
			}
		})
	}
}

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
		h:   &Handler{repo: r, flowUploads: newFlowUploadDeduper(flowUploadDedupeTTL, flowUploadDedupeMaxEntries)},
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

func TestIngestFlowTwoWayTunnelCountsRealTraffic(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)

	// D = upload (from client), U = download (to client).
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 1000, U: 3000}, {N: "20_2_10_udp", D: 5, U: 7}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("forward", 20, 1005, 3007)
	e.expectFlows("user", 2, 1005, 3007)
	e.expectFlows("user_tunnel", 10, 1005, 3007)
	if got := e.monthlyQuotaUsed(2); got != 4012 {
		t.Fatalf("monthly quota used = %d, want 4012", got)
	}
}

func TestIngestFlowOneWayTunnelBillsLargerDirection(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowOneWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)

	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 1000, U: 3000}}); err != nil {
		t.Fatalf("ingest download-heavy: %v", err)
	}
	e.expectFlows("forward", 20, 0, 3000)
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 900, U: 100}}); err != nil {
		t.Fatalf("ingest upload-heavy: %v", err)
	}
	e.expectFlows("forward", 20, 900, 3000)
	e.expectFlows("user", 2, 900, 3000)
	e.expectFlows("user_tunnel", 10, 900, 3000)
	if got := e.monthlyQuotaUsed(2); got != 3900 {
		t.Fatalf("monthly quota used = %d, want 3900", got)
	}
}

func TestIngestFlowAppliesTunnelRatio(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 0.5)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)

	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 1000, U: 3000}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("forward", 20, 500, 1500)
}

func TestIngestFlowResolvesCurrentUserTunnelInsteadOfStaleServiceName(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addTunnel(2, tunnelFlowOneWay, 1)
	e.addUserTunnel(10, 2, 1) // the user_tunnel of the tunnel the forward used to be on
	e.addUserTunnel(11, 2, 2) // the user_tunnel of the forward's current tunnel
	e.addForward(20, 2, 2)    // forward moved from tunnel 1 to tunnel 2

	// The node was not resynced and still reports the old user_tunnel id 10.
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("user_tunnel", 11, 0, 400) // billed with tunnel 2 (one-way)
	e.expectFlows("user_tunnel", 10, 0, 0)
	e.expectFlows("forward", 20, 0, 400)
	e.expectFlows("user", 2, 0, 400)
}

func TestIngestFlowUsesRecreatedUserTunnelID(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(15, 2, 1) // re-created after a group revoke/re-grant; old id 10 is gone
	e.addForward(20, 2, 1)

	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("user_tunnel", 15, 100, 400)
}

func TestIngestFlowAdminForwardWithoutUserTunnel(t *testing.T) {
	e := newFlowTestEnv(t) // user 1 is the seeded admin
	e.exec(`UPDATE user SET in_flow = 0, out_flow = 0 WHERE id = 1`)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addForward(20, 1, 1)

	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_1_0_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("forward", 20, 100, 400)
	e.expectFlows("user", 1, 100, 400)
}

func TestIngestFlowDeletedForwardFallsBackToParsedIDs(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowOneWay, 1)
	e.addUserTunnel(10, 2, 1)

	// Forward 20 was deleted; the node still reports its last bytes.
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("user", 2, 0, 400) // billed with the user_tunnel's tunnel (one-way)
	e.expectFlows("user_tunnel", 10, 0, 400)
}

func TestIngestFlowSkipsLocalCountersWhenServiceOwnerDiffers(t *testing.T) {
	e := newFlowTestEnv(t) // user 1 is the seeded admin
	e.exec(`UPDATE user SET in_flow = 0, out_flow = 0 WHERE id = 1`)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 1, 1) // local forward 20 belongs to user 1

	// A service of another owner with the same forward id (federation runtime).
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	e.expectFlows("forward", 20, 0, 0)
	e.expectFlows("user", 1, 0, 0)
	e.expectFlows("user", 2, 0, 0)
	e.expectFlows("user_tunnel", 10, 0, 0)
}

func TestIngestFlowBatchIsAtomic(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addUser(3)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addUserTunnel(11, 3, 1)
	e.addForward(20, 2, 1)
	e.addForward(21, 3, 1)
	e.exec(`CREATE TRIGGER fail_user_tunnel_11 BEFORE UPDATE ON user_tunnel WHEN NEW.id = 11 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)

	items := []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}, {N: "21_3_11_tcp", D: 7, U: 9}}
	if err := e.h.ingestFlowItems(1, items); err == nil {
		t.Fatal("expected the batch to fail")
	}
	e.expectFlows("forward", 20, 0, 0)
	e.expectFlows("user", 2, 0, 0)
	e.expectFlows("user_tunnel", 10, 0, 0)

	e.exec(`DROP TRIGGER fail_user_tunnel_11`)
	if err := e.h.ingestFlowItems(1, items); err != nil {
		t.Fatalf("retry: %v", err)
	}
	e.expectFlows("forward", 20, 100, 400)
	e.expectFlows("forward", 21, 7, 9)
	e.expectFlows("user_tunnel", 11, 7, 9)
}

func TestForwardTrafficLimitDoesNotCountUploadTwice(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)
	limit := bytesPerGB
	e.exec(`UPDATE forward SET traffic_limit = 1, in_flow = ? WHERE id = 20`, limit-100)

	// Real total after this upload is limit-40: the forward must keep running.
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 30, U: 30}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	fr, err := e.r.GetForwardRecord(20)
	if err != nil || fr == nil {
		t.Fatalf("reload forward: %v", err)
	}
	if fr.Status != 1 {
		t.Fatalf("forward paused below its traffic limit (status=%d)", fr.Status)
	}
	if fr.InFlow+fr.OutFlow != limit-40 {
		t.Fatalf("forward total = %d, want %d", fr.InFlow+fr.OutFlow, limit-40)
	}

	// Crossing the limit pauses the forward and resets its counters.
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 40, U: 0}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	fr, err = e.r.GetForwardRecord(20)
	if err != nil || fr == nil {
		t.Fatalf("reload forward: %v", err)
	}
	if fr.Status != 0 || fr.InFlow != 0 || fr.OutFlow != 0 {
		t.Fatalf("forward over its limit: status=%d in=%d out=%d, want paused and reset", fr.Status, fr.InFlow, fr.OutFlow)
	}
}

func encryptFlowUpload(t *testing.T, secret string, items []flowItem) []byte {
	t.Helper()
	plain, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	c, err := security.NewAESCrypto(secret)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"encrypted": true, "data": data, "timestamp": time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postFlowUpload(h *Handler, secret string, body []byte) (int, string) {
	req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret="+secret, bytes.NewReader(body))
	res := httptest.NewRecorder()
	h.flowUpload(res, req)
	return res.Code, res.Body.String()
}

func TestFlowUploadResponses(t *testing.T) {
	e := newFlowTestEnv(t)
	const secret = "node-secret-for-flow-upload"
	e.addNode(1, secret)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)

	body := encryptFlowUpload(t, secret, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}})
	if code, text := postFlowUpload(e.h, secret, body); code != http.StatusOK || text != "ok" {
		t.Fatalf("upload: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 100, 400)

	// The agent resends the same upload when it missed the "ok": not counted again.
	if code, text := postFlowUpload(e.h, secret, body); code != http.StatusOK || text != "ok" {
		t.Fatalf("resend: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 100, 400)

	// A plain JSON body (agent without cipher) is still accepted.
	if code, text := postFlowUpload(e.h, secret, []byte(`[{"n":"20_2_10_tcp","u":1,"d":2}]`)); code != http.StatusOK || text != "ok" {
		t.Fatalf("plain upload: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 102, 401)

	// Undecryptable or malformed bodies are rejected so the agent keeps the bytes.
	bad := []byte(`{"encrypted":true,"data":"bm90LWEtdmFsaWQtY2lwaGVydGV4dA==","timestamp":1}`)
	if code, text := postFlowUpload(e.h, secret, bad); code == http.StatusOK || text == "ok" {
		t.Fatalf("undecryptable upload accepted: %d %q", code, text)
	}
	if code, text := postFlowUpload(e.h, secret, []byte(`[{"n":`)); code == http.StatusOK || text == "ok" {
		t.Fatalf("malformed upload accepted: %d %q", code, text)
	}

	// Unknown secrets are acknowledged (deleted node) without touching anything.
	if code, text := postFlowUpload(e.h, "unknown-secret", body); code != http.StatusOK || text != "ok" {
		t.Fatalf("unknown secret: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 102, 401)

	// A failed write is rejected, nothing is stored, and the same upload is processed once
	// when it is resent after the failure is gone.
	e.exec(`CREATE TRIGGER fail_forward_20 BEFORE UPDATE ON forward WHEN NEW.id = 20 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	body2 := encryptFlowUpload(t, secret, []flowItem{{N: "20_2_10_tcp", D: 1, U: 1}})
	if code, text := postFlowUpload(e.h, secret, body2); code == http.StatusOK || text == "ok" {
		t.Fatalf("failed write acknowledged: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 102, 401)
	e.exec(`DROP TRIGGER fail_forward_20`)
	if code, text := postFlowUpload(e.h, secret, body2); code != http.StatusOK || text != "ok" {
		t.Fatalf("resend after failure: %d %q", code, text)
	}
	if code, text := postFlowUpload(e.h, secret, body2); code != http.StatusOK || text != "ok" {
		t.Fatalf("second resend: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 103, 402)
}

func TestFlowUploadDeduperRunsConcurrentDuplicatesOnce(t *testing.T) {
	d := newFlowUploadDeduper(time.Minute, 100)
	var calls int32
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	fn := func() error {
		atomic.AddInt32(&calls, 1)
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	wg.Add(1)
	go func() { defer wg.Done(); errs[0] = d.run("k", fn) }()
	<-started
	for i := 1; i < len(errs); i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = d.run("k", fn) }(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if err := d.run("k", fn); err != nil || calls != 1 {
		t.Fatalf("completed key ran again: err=%v calls=%d", err, calls)
	}
}

func TestFlowUploadDeduperRetriesFailuresAndExpires(t *testing.T) {
	d := newFlowUploadDeduper(time.Minute, 2)
	now := time.Unix(1000, 0)
	d.now = func() time.Time { return now }

	failed := errors.New("boom")
	calls := 0
	if err := d.run("a", func() error { calls++; return failed }); !errors.Is(err, failed) {
		t.Fatalf("expected failure, got %v", err)
	}
	if err := d.run("a", func() error { calls++; return nil }); err != nil || calls != 2 {
		t.Fatalf("failed key not retried: err=%v calls=%d", err, calls)
	}
	if err := d.run("a", func() error { calls++; return nil }); err != nil || calls != 2 {
		t.Fatalf("succeeded key ran again: calls=%d", calls)
	}

	now = now.Add(2 * time.Minute)
	if err := d.run("a", func() error { calls++; return nil }); err != nil || calls != 3 {
		t.Fatalf("expired key not processed again: calls=%d", calls)
	}

	// The entry cap evicts the oldest keys first.
	_ = d.run("b", func() error { return nil })
	_ = d.run("c", func() error { return nil })
	if _, ok := d.done["a"]; ok {
		t.Fatal("oldest key kept beyond the entry cap")
	}
	if len(d.done) != 2 {
		t.Fatalf("remembered %d keys, want 2", len(d.done))
	}
}
