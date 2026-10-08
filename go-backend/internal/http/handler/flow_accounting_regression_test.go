package handler

import (
	"net/http"
	"testing"
)

// Regression tests for the traffic accounting bugs fixed in 3.0.27-fork.8. They drive the
// /flow/upload handler like an agent does.
//
// Billing uses the original panel formula again since 3.0.27-fork.29: each direction x ratio x
// tunnel flow mode (单向 = 1, 双向 = 2).
//
// Agent items: D = bytes received from the client (upload), U = bytes sent to it (download).

const regressionSecret = "regression-node-secret"

func newRegressionEnv(t *testing.T, flowMode int64, ratio float64) *flowTestEnv {
	t.Helper()
	e := newFlowTestEnv(t)
	e.addNode(1, regressionSecret)
	e.addUser(2)
	e.addTunnel(1, flowMode, ratio)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)
	return e
}

func TestRegressionTwoWayTunnelBillsRealTraffic(t *testing.T) {
	e := newRegressionEnv(t, 2, 1)
	e.upload(regressionSecret,
		flowItem{N: "20_2_10_tcp", D: 1000, U: 3000},
		flowItem{N: "20_2_10_udp", D: 5, U: 7},
	)
	// 双向: (up+down) x 2.
	e.expectFlows("forward", 20, 2010, 6014)
	e.expectFlows("user", 2, 2010, 6014)
	e.expectFlows("user_tunnel", 10, 2010, 6014)
	if got := e.monthlyQuotaUsed(2); got != 8024 {
		t.Fatalf("monthly quota used = %d, want 8024", got)
	}
}

func TestRegressionTwoWayTunnelAppliesRatioThenDoubles(t *testing.T) {
	e := newRegressionEnv(t, 2, 0.5)
	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 1000, U: 3000})
	e.expectFlows("forward", 20, 1000, 3000)
}

func TestRegressionOneWayTunnelBillsBothDirectionsOnce(t *testing.T) {
	e := newRegressionEnv(t, 1, 1)
	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 1000, U: 3000})
	// 单向: up + down.
	e.expectFlows("forward", 20, 1000, 3000)
	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 900, U: 100})
	e.expectFlows("forward", 20, 1900, 3100)
	e.expectFlows("user", 2, 1900, 3100)
	e.expectFlows("user_tunnel", 10, 1900, 3100)
	if got := e.monthlyQuotaUsed(2); got != 5000 {
		t.Fatalf("monthly quota used = %d, want 5000", got)
	}
}

func TestRegressionStaleUserTunnelInServiceName(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, regressionSecret)
	e.addUser(2)
	e.addTunnel(1, 2, 1)
	e.addTunnel(2, 2, 1)
	e.addUserTunnel(10, 2, 1) // user_tunnel of the tunnel the forward used to be on
	e.addUserTunnel(11, 2, 2) // user_tunnel of the forward's current tunnel
	e.addForward(20, 2, 2)    // moved from tunnel 1 to tunnel 2; the node was not resynced

	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 100, U: 400})
	e.expectFlows("user_tunnel", 11, 200, 800)
	e.expectFlows("user_tunnel", 10, 0, 0)
}

func TestRegressionFailedWriteIsNotAcknowledged(t *testing.T) {
	e := newRegressionEnv(t, 2, 1)
	e.exec(`CREATE TRIGGER fail_forward_20 BEFORE UPDATE ON forward WHEN NEW.id = 20 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)

	body := encryptFlowUpload(t, regressionSecret, []flowItem{{N: "20_2_10_tcp", D: 100, U: 400}})
	if code, text := postFlowUpload(e.h, regressionSecret, body); code == http.StatusOK || text == "ok" {
		t.Fatalf("failed write acknowledged: %d %q (the agent would drop the bytes)", code, text)
	}
	// Nothing is half written: the user and user_tunnel were not billed either.
	e.expectFlows("user", 2, 0, 0)
	e.expectFlows("user_tunnel", 10, 0, 0)

	e.exec(`DROP TRIGGER fail_forward_20`)
	if code, text := postFlowUpload(e.h, regressionSecret, body); code != http.StatusOK || text != "ok" {
		t.Fatalf("resend after the failure: %d %q", code, text)
	}
	e.expectFlows("forward", 20, 200, 800)
	e.expectFlows("user", 2, 200, 800)
}

func TestRegressionUndecryptableUploadIsNotAcknowledged(t *testing.T) {
	e := newRegressionEnv(t, 2, 1)
	bad := []byte(`{"encrypted":true,"data":"bm90LWEtdmFsaWQtY2lwaGVydGV4dA==","timestamp":1}`)
	if code, text := postFlowUpload(e.h, regressionSecret, bad); code == http.StatusOK || text == "ok" {
		t.Fatalf("undecryptable upload acknowledged: %d %q", code, text)
	}
}

func TestRegressionForwardTrafficLimitCountsUploadOnce(t *testing.T) {
	e := newRegressionEnv(t, 1, 1)
	limit := bytesPerGB
	e.exec(`UPDATE forward SET traffic_limit = 1, in_flow = ? WHERE id = 20`, limit-100)

	// The real total after this upload is limit-40: the forward keeps running.
	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 30, U: 30})
	if status := e.forwardStatus(20); status != 1 {
		t.Fatalf("forward paused below its traffic limit (status=%d)", status)
	}
	in, out := e.flows("forward", 20)
	if in+out != limit-40 {
		t.Fatalf("forward total = %d, want %d", in+out, limit-40)
	}

	// Crossing the limit pauses the forward and resets its counters.
	e.upload(regressionSecret, flowItem{N: "20_2_10_tcp", D: 40, U: 0})
	if status := e.forwardStatus(20); status != 0 {
		t.Fatalf("forward over its limit still running (status=%d)", status)
	}
	e.expectFlows("forward", 20, 0, 0)
}
