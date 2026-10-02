package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/cnlanding"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type cnLandingJobResolver struct {
	addresses map[string][]netip.Addr
	errors    map[string]error
}

func (r cnLandingJobResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if err := r.errors[host]; err != nil {
		return nil, err
	}
	return r.addresses[host], nil
}

func TestRunCNLandingCheckJobPausesPositiveMatchButNotDNSFailure(t *testing.T) {
	r := openCNLandingJobRepo(t)
	h := New(r, "secret", "test")
	h.cnLandingChecker = cnlanding.New(cnLandingJobResolver{
		errors: map[string]error{
			"temporary.example": errors.New("temporary DNS failure"),
			"flagged.example":   errors.New("temporary DNS failure"),
		},
	})
	now := time.Now().UnixMilli()

	if err := r.DB().Exec(`
		INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
		VALUES
			(101, 1, 'admin_user', 'mainland-active', 0, '1.0.1.1:443', 'fifo', 0, 0, ?, ?, 1, 0),
			(102, 1, 'admin_user', 'dns-failure-active', 0, 'temporary.example:443', 'fifo', 0, 0, ?, ?, 1, 1),
			(103, 1, 'admin_user', 'dns-failure-flagged', 0, 'flagged.example:443', 'fifo', 0, 0, ?, ?, 0, 2),
			(104, 1, 'admin_user', 'wg-mainland-cidr', 0, '1.0.0.0/8', 'fifo', 0, 0, ?, ?, 1, 3)
	`, now, now, now, now, now, now, now, now).Error; err != nil {
		t.Fatalf("insert forwards: %v", err)
	}
	if err := r.DB().Exec(`UPDATE forward SET cn_blocked = 1, cn_blocked_reason = 'existing reason', cn_blocked_auto_paused = 1 WHERE id = 103`).Error; err != nil {
		t.Fatalf("flag DNS failure forward: %v", err)
	}
	if err := r.DB().Exec(`UPDATE forward SET mode = 'wg_path', target_cidr = '1.0.0.0/8' WHERE id = 104`).Error; err != nil {
		t.Fatalf("configure WG CIDR forward: %v", err)
	}

	h.runCNLandingCheckJob()

	var mainland model.Forward
	if err := r.DB().First(&mainland, 101).Error; err != nil {
		t.Fatalf("load mainland forward: %v", err)
	}
	if mainland.Status != 0 || !mainland.CNBlocked || !mainland.CNBlockedAutoPaused {
		t.Fatalf("mainland forward state = status:%d blocked:%v auto:%v, want paused and flagged", mainland.Status, mainland.CNBlocked, mainland.CNBlockedAutoPaused)
	}
	if mainland.CNBlockedReason == "" {
		t.Fatal("mainland forward missing block reason")
	}

	var dnsFailure model.Forward
	if err := r.DB().First(&dnsFailure, 102).Error; err != nil {
		t.Fatalf("load DNS failure forward: %v", err)
	}
	if dnsFailure.Status != 1 || dnsFailure.CNBlocked || dnsFailure.CNBlockedAutoPaused || dnsFailure.CNBlockedReason != "" {
		t.Fatalf("DNS failure changed state: status:%d blocked:%v auto:%v reason:%q", dnsFailure.Status, dnsFailure.CNBlocked, dnsFailure.CNBlockedAutoPaused, dnsFailure.CNBlockedReason)
	}

	var flaggedDNSFailure model.Forward
	if err := r.DB().First(&flaggedDNSFailure, 103).Error; err != nil {
		t.Fatalf("load flagged DNS failure forward: %v", err)
	}
	if flaggedDNSFailure.Status != 0 || !flaggedDNSFailure.CNBlocked || !flaggedDNSFailure.CNBlockedAutoPaused || flaggedDNSFailure.CNBlockedReason != "existing reason" {
		t.Fatalf("flagged DNS failure changed state: status:%d blocked:%v auto:%v reason:%q", flaggedDNSFailure.Status, flaggedDNSFailure.CNBlocked, flaggedDNSFailure.CNBlockedAutoPaused, flaggedDNSFailure.CNBlockedReason)
	}

	var wgCIDR model.Forward
	if err := r.DB().First(&wgCIDR, 104).Error; err != nil {
		t.Fatalf("load WG CIDR forward: %v", err)
	}
	if wgCIDR.Status != 0 || !wgCIDR.CNBlocked || !wgCIDR.CNBlockedAutoPaused {
		t.Fatalf("WG CIDR state = status:%d blocked:%v auto:%v, want paused and flagged", wgCIDR.Status, wgCIDR.CNBlocked, wgCIDR.CNBlockedAutoPaused)
	}
}

func TestRedeployTunnelSkipsPausedCNBlockedForward(t *testing.T) {
	r := openCNLandingJobRepo(t)
	h := New(r, "secret", "test")
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(301, 'paused-redeploy', 1, 1, 'tls', 99999, ?, ?, 1, NULL, 0)
	`, now, now).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	if err := r.DB().Exec(`
		INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx, cn_blocked, cn_blocked_reason, cn_blocked_auto_paused)
		VALUES(301, 1, 'admin_user', 'paused-cn-forward', 301, '1.0.1.1:443', 'fifo', 0, 0, ?, ?, 0, 0, 1, 'blocked', 1)
	`, now, now).Error; err != nil {
		t.Fatalf("insert forward: %v", err)
	}

	if err := h.redeployTunnelAndForwards(301); err != nil {
		t.Fatalf("paused forward should be skipped during redeploy: %v", err)
	}
}

func TestForwardUpdateClearsCNBlockAndResumesAutoPausedRule(t *testing.T) {
	r := openCNLandingJobRepo(t)
	h := New(r, "secret", "test")
	now := time.Now().UnixMilli()

	if err := r.DB().Exec(`
		INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(201, 'cn-edit-tunnel', 1, 1, 'tls', 99999, ?, ?, 1, NULL, 0)
	`, now, now).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	if err := r.DB().Exec(`
		INSERT INTO node(id, name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
		VALUES(201, 'offline-entry', 'secret', '10.0.0.10', '10.0.0.10', '', '20000-20010', '', 'v1', 1, 1, 1, ?, ?, 1, '[::]', '[::]', 0)
	`, now, now).Error; err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if err := r.DB().Exec(`
		INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(201, 1, 201, 20001, 'round', 1, 'tls')
	`).Error; err != nil {
		t.Fatalf("insert chain tunnel: %v", err)
	}
	if err := r.DB().Exec(`
		INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx, cn_blocked, cn_blocked_reason, cn_blocked_auto_paused)
		VALUES(201, 1, 'admin_user', 'auto-paused', 201, '1.0.1.1:443', 'fifo', 0, 0, ?, ?, 0, 0, 1, 'old reason', 1)
	`, now, now).Error; err != nil {
		t.Fatalf("insert forward: %v", err)
	}
	if err := r.DB().Exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(201, 201, 20001)`).Error; err != nil {
		t.Fatalf("insert forward port: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/forward/update", bytes.NewBufferString(`{"id":201,"remoteAddr":"8.8.8.8:443"}`))
	request = request.WithContext(context.WithValue(request.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", User: "admin_user", RoleID: 0}))
	recorder := httptest.NewRecorder()
	h.forwardUpdate(recorder, request)

	var result response.R
	if err := json.NewDecoder(recorder.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Code != 0 {
		t.Fatalf("update failed: code=%d msg=%q", result.Code, result.Msg)
	}

	var forward model.Forward
	if err := r.DB().First(&forward, 201).Error; err != nil {
		t.Fatalf("load updated forward: %v", err)
	}
	if forward.RemoteAddr != "8.8.8.8:443" || forward.Status != 1 || forward.CNBlocked || forward.CNBlockedAutoPaused || forward.CNBlockedReason != "" {
		t.Fatalf("updated state = remote:%q status:%d blocked:%v auto:%v reason:%q", forward.RemoteAddr, forward.Status, forward.CNBlocked, forward.CNBlockedAutoPaused, forward.CNBlockedReason)
	}
}

func openCNLandingJobRepo(t *testing.T) *repo.Repository {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "cn-landing.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}
