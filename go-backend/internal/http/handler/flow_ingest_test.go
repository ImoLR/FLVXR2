package handler

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestIngestFlowDeletedForwardOfDeletedUserIsDropped(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addTunnel(1, tunnelFlowTwoWay, 1)

	// Forward 20 and its user 7 are gone; nothing is billed and no quota row is created.
	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_7_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n := e.quotaRows(7); n != 0 {
		t.Fatalf("created %d quota rows for a deleted user", n)
	}
}

func (e *flowTestEnv) addForwardPeerShareRuntime(id, shareID, nodeID int64, serviceName string) {
	e.exec(`INSERT INTO peer_share(id, name, node_id, token, current_flow, is_active, created_time, updated_time)
		VALUES(?, 's', ?, ?, 0, 1, ?, ?)`, shareID, nodeID, "tok"+serviceName, e.now, e.now)
	e.exec(`INSERT INTO peer_share_runtime(id, share_id, node_id, reservation_id, resource_key, role, service_name, applied, status, created_time, updated_time)
		VALUES(?, ?, ?, ?, ?, 'forward', ?, 1, 1, ?, ?)`, id, shareID, nodeID, "res"+serviceName, "key"+serviceName, serviceName, e.now, e.now)
}

func (e *flowTestEnv) peerShareFlow(shareID int64) int64 {
	e.t.Helper()
	var row struct{ CurrentFlow int64 }
	if err := e.r.DB().Table("peer_share").Select("current_flow").Where("id = ?", shareID).Take(&row).Error; err != nil {
		e.t.Fatalf("read peer share %d: %v", shareID, err)
	}
	return row.CurrentFlow
}

func TestIngestFlowFederationRuntimeOnlyCountsPeerShare(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	// Another panel runs its forward 20 of its user 2 on this panel's node 1. This panel has
	// a user 2 and a user_tunnel 10 too, but no forward 20.
	e.addForwardPeerShareRuntime(1, 5, 1, "20_2_10")

	if err := e.h.ingestFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := e.peerShareFlow(5); got != 500 {
		t.Fatalf("peer share flow = %d, want 500", got)
	}
	e.expectFlows("user", 2, 0, 0)
	e.expectFlows("user_tunnel", 10, 0, 0)
	if n := e.quotaRows(2); n != 0 {
		t.Fatalf("federation traffic billed to the local user's quota (%d rows)", n)
	}
}

func TestFlowUploadAnswersBeforeEnforcement(t *testing.T) {
	e := newFlowTestEnv(t)
	const secret = "node-secret-enforcement"
	e.addNode(1, secret)
	e.addUser(2)
	e.addTunnel(1, tunnelFlowTwoWay, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)
	e.exec(`UPDATE forward SET traffic_limit = 1, in_flow = ? WHERE id = 20`, bytesPerGB)

	// Hold the enforcement lock as a slow pause on another upload would.
	e.h.flowEnforceMu.Lock()
	req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret="+secret,
		bytes.NewReader(encryptFlowUpload(t, secret, []flowItem{{N: "20_2_10_tcp", D: 1, U: 1}})))
	res := httptest.NewRecorder()
	answered := make(chan struct{})
	go func() {
		e.h.flowUpload(res, req)
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		e.h.flowEnforceMu.Unlock()
		t.Fatal("upload answer waited for enforcement")
	}
	if res.Code != http.StatusOK || res.Body.String() != "ok" {
		t.Fatalf("upload: %d %q", res.Code, res.Body.String())
	}
	if status := e.forwardStatus(20); status != 1 {
		t.Fatalf("forward paused before enforcement ran (status=%d)", status)
	}

	e.h.flowEnforceMu.Unlock()
	waitFlowEnforcement(e.h)
	if status := e.forwardStatus(20); status != 0 {
		t.Fatalf("forward over its traffic limit not paused (status=%d)", status)
	}
}

// waitFlowEnforcement waits for enforcement started after /flow/upload answers.
func waitFlowEnforcement(h *Handler) {
	h.flowEnforceWG.Wait()
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

func TestFlowUploadResponses(t *testing.T) {
	e := newFlowTestEnv(t)
	e.h.flowUploads = newFlowUploadDeduper(flowUploadDedupeTTL, flowUploadDedupeMaxEntries)
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
