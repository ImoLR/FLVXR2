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
)

func TestForwardMainlandLandingRejectionContracts(t *testing.T) {
	const secret = "contract-jwt-secret"
	router, repository := setupContractRouter(t, secret)
	now := time.Now().UnixMilli()

	if err := repository.DB().Exec(`
		INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, 1, 1, 'tls', 99999, ?, ?, 1, NULL, 0)
	`, "cn-landing-contract", now, now).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	tunnelID := mustLastInsertID(t, repository, "cn-landing-contract")
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	assertRejected := func(t *testing.T, path, body string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		request.Header.Set("Authorization", adminToken)
		responseRecorder := httptest.NewRecorder()
		router.ServeHTTP(responseRecorder, request)

		var result response.R
		if err := json.NewDecoder(responseRecorder.Body).Decode(&result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if result.Code == 0 || !strings.Contains(result.Msg, "位于中国大陆，不允许使用") {
			t.Fatalf("expected mainland rejection, got code=%d msg=%q", result.Code, result.Msg)
		}
	}

	t.Run("create", func(t *testing.T) {
		assertRejected(t, "/api/v1/forward/create", `{"tunnelId":`+jsonNumber(tunnelID)+`,"name":"blocked-create","remoteAddr":"1.0.1.1:443"}`)
	})

	if err := repository.DB().Exec(`
		INSERT INTO forward(user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
		VALUES(1, 'admin_user', 'allowed-update', ?, '8.8.8.8:443', 'fifo', 0, 0, ?, ?, 1, 0)
	`, tunnelID, now, now).Error; err != nil {
		t.Fatalf("insert update forward: %v", err)
	}
	updateID := mustLastInsertID(t, repository, "allowed-update")

	t.Run("update", func(t *testing.T) {
		assertRejected(t, "/api/v1/forward/update", `{"id":`+jsonNumber(updateID)+`,"remoteAddr":"1.0.1.1:443"}`)
		var remoteAddr string
		if err := repository.DB().Raw("SELECT remote_addr FROM forward WHERE id = ?", updateID).Scan(&remoteAddr).Error; err != nil {
			t.Fatalf("query update forward: %v", err)
		}
		if remoteAddr != "8.8.8.8:443" {
			t.Fatalf("rejected update changed remote_addr to %q", remoteAddr)
		}
	})

	if err := repository.DB().Exec(`
		INSERT INTO forward(user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
		VALUES(1, 'admin_user', 'blocked-resume', ?, '1.0.1.1:443', 'fifo', 0, 0, ?, ?, 0, 1)
	`, tunnelID, now, now).Error; err != nil {
		t.Fatalf("insert resume forward: %v", err)
	}
	resumeID := mustLastInsertID(t, repository, "blocked-resume")

	t.Run("resume", func(t *testing.T) {
		assertRejected(t, "/api/v1/forward/resume", `{"id":`+jsonNumber(resumeID)+`}`)
		var status int
		if err := repository.DB().Raw("SELECT status FROM forward WHERE id = ?", resumeID).Scan(&status).Error; err != nil {
			t.Fatalf("query resume forward: %v", err)
		}
		if status != 0 {
			t.Fatalf("rejected resume changed status to %d", status)
		}
	})
}
