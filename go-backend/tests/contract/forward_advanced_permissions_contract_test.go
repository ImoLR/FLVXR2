package contract_test

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestNonAdminForwardAdvancedFieldsAreIgnored(t *testing.T) {
	const secret = "forward-advanced-jwt"
	router, repository := setupContractRouter(t, secret)
	userToken, tunnelID := seedForwardAdvancedContract(t, repository, secret)

	create := func(name string, port int) {
		t.Helper()
		out := requestContractEnvelope(t, router, userToken, "/api/v1/forward/create", map[string]interface{}{
			"name":              name,
			"tunnelId":          tunnelID,
			"remoteAddr":        "8.8.8.8:443",
			"inPort":            port,
			"inIp":              "127.0.0.9",
			"maxConnections":    999,
			"maxClientIps":      999,
			"trafficLimit":      99,
			"expiryTime":        time.Now().Add(24 * time.Hour).UnixMilli(),
			"speedId":           11,
			"speedLimitEnabled": true,
			"speedLimit":        123,
		})
		if out.Code != 0 {
			t.Fatalf("non-admin create %q failed: code=%d msg=%q", name, out.Code, out.Msg)
		}
	}

	// The user's totals are both 1. Creating two rules must still be allowed; the
	// totals are a live connection pool and never a rule-creation quota.
	create("advanced-default-a", 33101)
	create("advanced-default-b", 33102)

	var created model.Forward
	if err := repository.DB().Where("name = ?", "advanced-default-a").First(&created).Error; err != nil {
		t.Fatalf("load created forward: %v", err)
	}
	if created.MaxConnections != 0 || created.MaxClientIps != 0 || created.TrafficLimit != 0 {
		t.Fatalf("advanced limits were not reset to defaults: connections=%d clientIps=%d traffic=%d", created.MaxConnections, created.MaxClientIps, created.TrafficLimit)
	}
	if created.ExpiryTime.Valid || created.SpeedLimitEnabled || created.SpeedLimit != 0 {
		t.Fatalf("advanced expiry/speed values were not ignored: expiry=%v enabled=%v speed=%d", created.ExpiryTime, created.SpeedLimitEnabled, created.SpeedLimit)
	}
	if !created.SpeedID.Valid || created.SpeedID.Int64 != 10 {
		t.Fatalf("expected existing user speed fallback 10, got %+v", created.SpeedID)
	}
	var bindIP string
	if err := repository.DB().Raw("SELECT COALESCE(in_ip, '') FROM forward_port WHERE forward_id = ? LIMIT 1", created.ID).Row().Scan(&bindIP); err != nil {
		t.Fatalf("load created bind IP: %v", err)
	}
	if bindIP != "" {
		t.Fatalf("expected default bind IP, got %q", bindIP)
	}

	futureExpiry := time.Now().Add(48 * time.Hour).UnixMilli()
	if err := repository.DB().Model(&model.Forward{}).Where("id = ?", created.ID).Updates(map[string]interface{}{
		"max_connections":     1,
		"max_client_ips":      1,
		"traffic_limit":       7,
		"expiry_time":         futureExpiry,
		"speed_id":            11,
		"speed_limit_enabled": true,
		"speed_limit":         42,
	}).Error; err != nil {
		t.Fatalf("seed stored advanced values: %v", err)
	}
	if err := repository.DB().Model(&model.ForwardPort{}).Where("forward_id = ?", created.ID).Update("in_ip", "127.0.0.2").Error; err != nil {
		t.Fatalf("seed stored bind IP: %v", err)
	}

	out := requestContractEnvelope(t, router, userToken, "/api/v1/forward/update", map[string]interface{}{
		"id":                created.ID,
		"name":              "advanced-updated-name",
		"inIp":              "127.0.0.8",
		"maxConnections":    0,
		"maxClientIps":      0,
		"trafficLimit":      0,
		"expiryTime":        nil,
		"speedId":           nil,
		"speedLimitEnabled": false,
		"speedLimit":        0,
	})
	if out.Code != 0 {
		t.Fatalf("non-admin update failed: code=%d msg=%q", out.Code, out.Msg)
	}

	var updated model.Forward
	if err := repository.DB().First(&updated, created.ID).Error; err != nil {
		t.Fatalf("load updated forward: %v", err)
	}
	if updated.Name != "advanced-updated-name" {
		t.Fatalf("ordinary field was not updated: %q", updated.Name)
	}
	if updated.MaxConnections != 1 || updated.MaxClientIps != 1 || updated.TrafficLimit != 7 || !updated.ExpiryTime.Valid || updated.ExpiryTime.Int64 != futureExpiry {
		t.Fatalf("stored advanced limits changed: %+v", updated)
	}
	if !updated.SpeedID.Valid || updated.SpeedID.Int64 != 11 || !updated.SpeedLimitEnabled || updated.SpeedLimit != 42 {
		t.Fatalf("stored speed fields changed: speedId=%+v enabled=%v speed=%d", updated.SpeedID, updated.SpeedLimitEnabled, updated.SpeedLimit)
	}
	if err := repository.DB().Raw("SELECT COALESCE(in_ip, '') FROM forward_port WHERE forward_id = ? LIMIT 1", created.ID).Row().Scan(&bindIP); err != nil {
		t.Fatalf("load updated bind IP: %v", err)
	}
	if bindIP != "127.0.0.2" {
		t.Fatalf("stored bind IP changed to %q", bindIP)
	}
}

func TestAdminForwardRuleCapCannotExceedUserTotal(t *testing.T) {
	const secret = "forward-cap-jwt"
	router, repository := setupContractRouter(t, secret)
	_, tunnelID := seedForwardAdvancedContract(t, repository, secret)
	adminToken := mustAdminToken(t, secret)

	tests := []struct {
		name        string
		field       string
		wantMessage string
	}{
		{name: "connections", field: "maxConnections", wantMessage: "总连接数限制"},
		{name: "client IPs", field: "maxClientIps", wantMessage: "总接入 IP 数限制"},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"userId":     2,
				"name":       "cap-too-large-" + tc.name,
				"tunnelId":   tunnelID,
				"remoteAddr": "8.8.4.4:443",
				"inPort":     33110 + i,
				tc.field:     2,
			}
			out := requestContractEnvelope(t, router, adminToken, "/api/v1/forward/create", body)
			if out.Code == 0 || !strings.Contains(out.Msg, tc.wantMessage) {
				t.Fatalf("expected clear cap rejection containing %q, got code=%d msg=%q", tc.wantMessage, out.Code, out.Msg)
			}
		})
	}

	valid := requestContractEnvelope(t, router, adminToken, "/api/v1/forward/create", map[string]interface{}{
		"userId":         2,
		"name":           "cap-valid",
		"tunnelId":       tunnelID,
		"remoteAddr":     "8.8.4.4:443",
		"inPort":         33120,
		"maxConnections": 1,
		"maxClientIps":   1,
	})
	if valid.Code != 0 {
		t.Fatalf("create valid capped rule: code=%d msg=%q", valid.Code, valid.Msg)
	}
	var forward model.Forward
	if err := repository.DB().Where("name = ?", "cap-valid").First(&forward).Error; err != nil {
		t.Fatalf("load valid capped rule: %v", err)
	}
	update := requestContractEnvelope(t, router, adminToken, "/api/v1/forward/update", map[string]interface{}{
		"id":           forward.ID,
		"maxClientIps": 2,
	})
	if update.Code == 0 || !strings.Contains(update.Msg, "总接入 IP 数限制") {
		t.Fatalf("expected update cap rejection, got code=%d msg=%q", update.Code, update.Msg)
	}
}

func seedForwardAdvancedContract(t *testing.T, repository *repo.Repository, jwtSecret string) (string, int64) {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := repository.DB().Exec(`
		INSERT INTO speed_limit(id, name, speed, created_time, status)
		VALUES(10, 'user-default', 1024, ?, 1), (11, 'admin-choice', 2048, ?, 1)
	`, now, now).Error; err != nil {
		t.Fatalf("insert speed limits: %v", err)
	}
	if err := repository.DB().Exec(`
		INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status, speed_limit_id, max_connections, max_client_ips)
		VALUES(2, 'advanced_user', 'pwd', 1, ?, 99999, 0, 0, 1, 10, ?, ?, 1, 10, 1, 1)
	`, now+86400000, now, now).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := repository.DB().Exec(`
		INSERT INTO node(id, name, secret, server_ip, server_ip_v4, server_ip_v6, port, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
		VALUES(2, 'offline-entry', 'offline-entry-secret', '127.0.0.1', '127.0.0.1', '', '33100-33199', '3.0.27', 1, 1, 1, ?, ?, 0, '[::]', '[::]', 0)
	`, now, now).Error; err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if err := repository.DB().Exec(`
		INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx)
		VALUES(2, 'advanced-tunnel', 1, 1, 'tls', 99999, ?, ?, 1, 0)
	`, now, now).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	if err := repository.DB().Exec(`
		INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(2, 1, 2, 33100, 'round', 1, 'tls')
	`).Error; err != nil {
		t.Fatalf("insert entry chain: %v", err)
	}
	if err := repository.DB().Exec(`
		INSERT INTO user_tunnel(id, user_id, tunnel_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		VALUES(2, 2, 2, 10, 99999, 0, 0, 1, ?, 1)
	`, now+86400000).Error; err != nil {
		t.Fatalf("insert user tunnel: %v", err)
	}

	token, err := auth.GenerateToken(2, "advanced_user", 1, jwtSecret)
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}
	return token, 2
}
