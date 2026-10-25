package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"

	"gorm.io/gorm"
)

func TestCaptchaVerifyLoginContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)
	verifiedToken := ""

	if err := r.DB().Exec(`
		INSERT INTO vite_config(name, value, time)
		VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value, time = excluded.time
	`, "captcha_enabled", "true", time.Now().UnixMilli()).Error; err != nil {
		t.Fatalf("enable captcha: %v", err)
	}

	t.Run("login allowed when cloudflare keys are missing", func(t *testing.T) {
		body := bytes.NewBufferString(`{"username":"admin_user","password":"admin_user","captchaId":""}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", body)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		assertCode(t, resp, 0)
	})

	t.Run("captcha verify remains compatible without cloudflare secret", func(t *testing.T) {
		verifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/captcha/verify", bytes.NewBufferString(`{"id":"captcha-token-1","data":"ok"}`))
		verifyReq.Header.Set("Content-Type", "application/json")
		verifyResp := httptest.NewRecorder()

		router.ServeHTTP(verifyResp, verifyReq)

		var verifyOut struct {
			Success bool `json:"success"`
			Data    struct {
				ValidToken string `json:"validToken"`
			} `json:"data"`
		}
		if err := json.NewDecoder(verifyResp.Body).Decode(&verifyOut); err != nil {
			t.Fatalf("decode captcha verify response: %v", err)
		}
		if !verifyOut.Success || verifyOut.Data.ValidToken != "captcha-token-1" {
			t.Fatalf("unexpected captcha verify payload: success=%v token=%q", verifyOut.Success, verifyOut.Data.ValidToken)
		}

		verifiedToken = verifyOut.Data.ValidToken
	})

	if err := r.DB().Exec(`
		INSERT INTO vite_config(name, value, time)
		VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value, time = excluded.time
	`, "cloudflare_site_key", "test-site-key", time.Now().UnixMilli()).Error; err != nil {
		t.Fatalf("set cloudflare site key: %v", err)
	}
	if err := r.DB().Exec(`
		INSERT INTO vite_config(name, value, time)
		VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value, time = excluded.time
	`, "cloudflare_secret_key", "test-secret-key", time.Now().UnixMilli()).Error; err != nil {
		t.Fatalf("set cloudflare secret key: %v", err)
	}

	t.Run("login denied without verified captcha token", func(t *testing.T) {
		body := bytes.NewBufferString(`{"username":"admin_user","password":"admin_user","captchaId":""}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", body)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		assertCodeMsg(t, resp, -1, "验证码校验失败")
	})

	t.Run("whmcs api client bypasses captcha", func(t *testing.T) {
		body := bytes.NewBufferString(`{"username":"admin_user","password":"admin_user","captchaId":""}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FLVX-API-Client", "whmcs")
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		assertCode(t, resp, 0)
	})

	t.Run("captcha token is one-time and consumed by login", func(t *testing.T) {
		if strings.TrimSpace(verifiedToken) == "" {
			t.Fatalf("expected verified token from compatibility captcha verify")
		}

		loginBody := bytes.NewBufferString(`{"username":"admin_user","password":"admin_user","captchaId":"` + verifiedToken + `"}`)
		loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", loginBody)
		loginReq.Header.Set("Content-Type", "application/json")
		loginResp := httptest.NewRecorder()
		router.ServeHTTP(loginResp, loginReq)
		assertCode(t, loginResp, 0)

		replayBody := bytes.NewBufferString(`{"username":"admin_user","password":"admin_user","captchaId":"` + verifiedToken + `"}`)
		replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", replayBody)
		replayReq.Header.Set("Content-Type", "application/json")
		replayResp := httptest.NewRecorder()
		router.ServeHTTP(replayResp, replayReq)
		assertCodeMsg(t, replayResp, -1, "验证码校验失败")
	})
}

func TestOpenAPISubStoreContracts(t *testing.T) {
	router, r := setupContractRouter(t, "contract-jwt-secret")

	const tunnelFlowGB = int64(500)
	const tunnelInFlow = int64(123)
	const tunnelOutFlow = int64(456)
	const tunnelExpTimeMs = int64(2727251700000)

	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"contract-tunnel", 1.0, 1, "tls", 1, now, now, 1, nil, 0).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	tunnelID := mustLastInsertID(t, r, "contract-tunnel")
	if err := r.DB().Exec(`INSERT INTO user_tunnel(user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status) VALUES(?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)`,
		1, tunnelID, 99999, tunnelFlowGB, tunnelInFlow, tunnelOutFlow, 1, tunnelExpTimeMs, 1).Error; err != nil {
		t.Fatalf("insert user_tunnel: %v", err)
	}

	t.Run("default user subscription payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/open_api/sub_store?user=admin_user&pwd=admin_user", nil)
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		expected := "upload=0; download=0; total=107373108658176; expire=2727251700"
		if string(body) != expected {
			t.Fatalf("expected body %q, got %q", expected, string(body))
		}
		if got := resp.Header().Get("subscription-userinfo"); got != expected {
			t.Fatalf("expected subscription-userinfo %q, got %q", expected, got)
		}
		if !strings.Contains(resp.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("expected text/plain content type, got %q", resp.Header().Get("Content-Type"))
		}
	})

	t.Run("tunnel scoped subscription payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/open_api/sub_store?user=admin_user&pwd=admin_user&tunnel="+strconv.FormatInt(tunnelID, 10), nil)
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		expected := "upload=123; download=456; total=536870912000; expire=2727251700"
		if string(body) != expected {
			t.Fatalf("expected body %q, got %q", expected, string(body))
		}
		if got := resp.Header().Get("subscription-userinfo"); got != expected {
			t.Fatalf("expected subscription-userinfo %q, got %q", expected, got)
		}
	})

	t.Run("invalid credentials returns contract error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/open_api/sub_store?user=admin_user&pwd=wrong", nil)
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		assertCodeMsg(t, resp, -1, "鉴权失败")
	})

	t.Run("missing tunnel returns contract error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/open_api/sub_store?user=admin_user&pwd=admin_user&tunnel=999999", nil)
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		assertCodeMsg(t, resp, -1, "隧道不存在")
	})
}

func TestSpeedLimitTunnelsRouteRemoved(t *testing.T) {
	secret := "contract-jwt-secret"
	router, _ := setupContractRouter(t, secret)

	token, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/tunnels", nil)
	req.Header.Set("Authorization", token)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 after route removal, got %d", resp.Code)
	}
}

func TestBackupExportImportRestoreContracts(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	ensureContractUser(t, r, 2, 1)
	userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}

	key := "backup_contract_key"
	if err := r.DB().Exec(`
		INSERT INTO vite_config(name, value, time)
		VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value, time = excluded.time
	`, key, "v1", time.Now().UnixMilli()).Error; err != nil {
		t.Fatalf("seed config for backup contract: %v", err)
	}

	t.Run("non-admin is blocked on backup export", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/export", nil)
		req.Header.Set("Authorization", userToken)
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)
		assertCodeMsg(t, resp, 403, "权限不足，仅管理员可操作")
	})

	// Backup format since upstream 1b4e0c0e (2026-04-11, 「修改了很多小bug」):
	// the UI's 全量导出 (types [], mode full/core) is a raw table dump, version
	// "2.0", keyed by table name; /backup/import and /backup/restore only
	// restore that format, adding rows that are missing (existing primary or
	// unique keys are kept, nothing is deleted). Typed exports (types set,
	// version "1.0") are still offered but are not importable; the same commit
	// dropped the duplicate-prefix /api/v1/api/v1/backup/* routes.
	t.Run("typed and full export routes work, duplicate prefix route removed", func(t *testing.T) {
		payloadA := exportBackupPayload(t, router, "/api/v1/backup/export", adminToken)
		if len(payloadA.Configs) == 0 {
			t.Fatalf("expected exported configs, got none")
		}
		if _, ok := payloadA.Configs[key]; !ok {
			t.Fatalf("expected %q in exported configs", key)
		}

		full := exportFullBackup(t, router, adminToken)
		if full["version"] != "2.0" {
			t.Fatalf("expected full backup version 2.0, got %v", full["version"])
		}
		if got := backupRowValue(t, full, "vite_config", "name", key, "value"); got != "v1" {
			t.Fatalf("expected %q=v1 in full backup vite_config, got %q", key, got)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/api/v1/backup/export", bytes.NewBufferString(`{"types":["configs"]}`))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected removed duplicate-prefix export route to return 404, got %d", resp.Code)
		}
	})

	t.Run("backup import restores missing rows from a full export", func(t *testing.T) {
		assertBackupRestoresConfigRow(t, router, r, adminToken, "/api/v1/backup/import", key, "v2")
	})

	t.Run("backup restore alias restores missing rows from a full export", func(t *testing.T) {
		assertBackupRestoresConfigRow(t, router, r, adminToken, "/api/v1/backup/restore", key, "v3")
	})

	t.Run("typed 1.x backup import is refused instead of reporting success", func(t *testing.T) {
		payload := exportBackupPayload(t, router, "/api/v1/backup/export", adminToken)
		before, err := r.GetConfigByName(key)
		if err != nil || before == nil {
			t.Fatalf("query config before typed import: %+v %v", before, err)
		}
		payload.Configs[key] = "typed-should-not-apply"
		raw, err := json.Marshal(backupImportPayload{Types: []string{"configs"}, backupExportPayload: payload})
		if err != nil {
			t.Fatalf("marshal typed import payload: %v", err)
		}
		for _, path := range []string{"/api/v1/backup/import", "/api/v1/backup/restore"} {
			out := postBackupImport(t, router, path, adminToken, raw)
			if out.Code != -1 || !strings.Contains(out.Msg, "旧版（1.x）") {
				t.Fatalf("expected %s to refuse a typed 1.x backup, got code=%d msg=%q", path, out.Code, out.Msg)
			}
		}
		after, err := r.GetConfigByName(key)
		if err != nil || after == nil || after.Value != before.Value {
			t.Fatalf("typed import must not change %q: before %+v after %+v (%v)", key, before, after, err)
		}
	})

	t.Run("backup export and import preserve forward ports", func(t *testing.T) {
		now := time.Now().UnixMilli()

		if err := r.DB().Exec(`
			INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, "backup-forward-tunnel", 1.0, 1, "tls", 0, now, now, 1, "", 88).Error; err != nil {
			t.Fatalf("seed tunnel for forward backup: %v", err)
		}
		tunnelID := mustLastInsertID(t, r, "backup-forward-tunnel")

		if err := r.DB().Exec(`
			INSERT INTO forward(user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, 1, "admin_user", "backup-forward", tunnelID, "127.0.0.1:9000", "fifo", 0, 0, now, now, 1, 88).Error; err != nil {
			t.Fatalf("seed forward for backup: %v", err)
		}
		forwardID := mustLastInsertID(t, r, "backup-forward")

		expected := map[int64]int{
			2001: 21001,
			2002: 21002,
		}
		for nodeID, port := range expected {
			if err := r.DB().Exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(?, ?, ?)`, forwardID, nodeID, port).Error; err != nil {
				t.Fatalf("seed forward_port %d:%d: %v", nodeID, port, err)
			}
		}

		exportReq := httptest.NewRequest(http.MethodPost, "/api/v1/backup/export", bytes.NewBufferString(`{"types":["forwards"]}`))
		exportReq.Header.Set("Authorization", adminToken)
		exportReq.Header.Set("Content-Type", "application/json")
		exportResp := httptest.NewRecorder()
		router.ServeHTTP(exportResp, exportReq)

		if exportResp.Code != http.StatusOK {
			t.Fatalf("expected export status 200, got %d", exportResp.Code)
		}

		exportBody, err := io.ReadAll(exportResp.Body)
		if err != nil {
			t.Fatalf("read forwards backup body: %v", err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(exportBody, &payload); err != nil {
			t.Fatalf("decode forwards backup payload: %v", err)
		}
		version, _ := payload["version"].(string)
		if strings.TrimSpace(version) == "" {
			t.Fatalf("expected backup payload version, body=%s", string(exportBody))
		}

		forwardsRaw, ok := payload["forwards"].([]interface{})
		if !ok {
			t.Fatalf("expected forwards array in payload, body=%s", string(exportBody))
		}

		foundForward := false
		foundPorts := map[int64]int{}
		for _, item := range forwardsRaw {
			forwardMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			idValue, ok := forwardMap["id"].(float64)
			if !ok || int64(idValue) != forwardID {
				continue
			}
			foundForward = true

			portsRaw, ok := forwardMap["forwardPorts"].([]interface{})
			if !ok {
				t.Fatalf("expected forwardPorts for forward %d in payload", forwardID)
			}
			for _, p := range portsRaw {
				portMap, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				nodeID, nodeOK := portMap["nodeId"].(float64)
				port, portOK := portMap["port"].(float64)
				if nodeOK && portOK {
					foundPorts[int64(nodeID)] = int(port)
				}
			}
			break
		}

		if !foundForward {
			t.Fatalf("expected forward %d in exported forwards payload", forwardID)
		}
		if len(foundPorts) != len(expected) {
			t.Fatalf("expected %d exported forward ports, got %d", len(expected), len(foundPorts))
		}
		for nodeID, port := range expected {
			if got, ok := foundPorts[nodeID]; !ok || got != port {
				t.Fatalf("expected exported forward port node=%d port=%d, got %v", nodeID, port, foundPorts)
			}
		}

		// Restore from the full (raw) export: the forward's ports are gone and an
		// unrelated row was added since; import puts the exported rows back and,
		// being additive, keeps the newer row.
		full := exportFullBackup(t, router, adminToken)
		if err := r.DB().Exec(`DELETE FROM forward_port WHERE forward_id = ?`, forwardID).Error; err != nil {
			t.Fatalf("clear forward_port before import: %v", err)
		}
		if err := r.DB().Exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(?, ?, ?)`, forwardID, 9999, 39999).Error; err != nil {
			t.Fatalf("seed newer forward_port before import: %v", err)
		}

		importBody, err := json.Marshal(map[string]interface{}{
			"version":      full["version"],
			"exported_at":  full["exported_at"],
			"types":        []string{"forward_port"},
			"forward_port": full["forward_port"],
		})
		if err != nil {
			t.Fatalf("marshal forwards import payload: %v", err)
		}
		out := postBackupImport(t, router, "/api/v1/backup/import", adminToken, importBody)
		if out.Code != 0 {
			t.Fatalf("expected forwards import code 0, got %d (%s)", out.Code, out.Msg)
		}
		if counts, _ := out.Data.(map[string]interface{}); counts == nil || counts["forward_port"] != float64(len(expected)) {
			t.Fatalf("expected %d restored forward_port rows in import result, got %v", len(expected), out.Data)
		}

		after := mustQueryNodePorts(t, r, `SELECT node_id, port FROM forward_port WHERE forward_id = ? ORDER BY id ASC`, forwardID)
		expected[9999] = 39999
		if len(after) != len(expected) {
			t.Fatalf("expected %d forward ports after import, got %d (%v)", len(expected), len(after), after)
		}
		for nodeID, port := range expected {
			if got, ok := after[nodeID]; !ok || got != port {
				t.Fatalf("expected forward_port node=%d port=%d after import, got %v", nodeID, port, after)
			}
		}
	})

	t.Run("backup export tolerates nullable legacy tunnel chain fields", func(t *testing.T) {
		now := time.Now().UnixMilli()
		if err := r.DB().Exec(`
			INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, "legacy-null-chain", 1.0, 1, "tls", 1000, now, now, 1, nil, 1).Error; err != nil {
			t.Fatalf("seed tunnel for nullable chain export: %v", err)
		}
		tunnelID := mustLastInsertID(t, r, "legacy-null-chain")

		if err := r.DB().Exec(`
			INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
			VALUES(?, ?, ?, ?, ?, ?, ?)
		`, tunnelID, "1", 1, nil, nil, nil, nil).Error; err != nil {
			t.Fatalf("seed nullable chain_tunnel row: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/export", bytes.NewBufferString(`{"types":["tunnels"]}`))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.Code)
		}

		var payload struct {
			Version string `json:"version"`
			Tunnels []struct {
				ID           int64 `json:"id"`
				ChainTunnels []struct {
					Inx      int    `json:"inx"`
					Strategy string `json:"strategy"`
					Protocol string `json:"protocol"`
				} `json:"chainTunnels"`
			} `json:"tunnels"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode tunnels backup payload: %v", err)
		}
		if strings.TrimSpace(payload.Version) == "" {
			t.Fatalf("expected backup payload version, got empty")
		}

		found := false
		for _, tunnel := range payload.Tunnels {
			if tunnel.ID != tunnelID {
				continue
			}
			if len(tunnel.ChainTunnels) != 1 {
				t.Fatalf("expected one chain tunnel for seeded tunnel %d, got %d", tunnelID, len(tunnel.ChainTunnels))
			}
			if tunnel.ChainTunnels[0].Inx != 0 {
				t.Fatalf("expected nullable chain inx to export as 0, got %d", tunnel.ChainTunnels[0].Inx)
			}
			if tunnel.ChainTunnels[0].Strategy != "" {
				t.Fatalf("expected nullable chain strategy to export as empty string, got %q", tunnel.ChainTunnels[0].Strategy)
			}
			if tunnel.ChainTunnels[0].Protocol != "" {
				t.Fatalf("expected nullable chain protocol to export as empty string, got %q", tunnel.ChainTunnels[0].Protocol)
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("expected seeded tunnel %d in backup export", tunnelID)
		}
	})
}

type backupExportPayload struct {
	Version    string            `json:"version"`
	ExportedAt int64             `json:"exportedAt"`
	Configs    map[string]string `json:"configs"`
}

type backupImportPayload struct {
	Types []string `json:"types"`
	backupExportPayload
}

func exportBackupPayload(t *testing.T, router http.Handler, path, token string) backupExportPayload {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"types":["configs"]}`))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200 on %s, got %d", path, resp.Code)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read backup payload from %s: %v", path, err)
	}

	var payload backupExportPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode backup payload from %s: %v", path, err)
	}
	if strings.TrimSpace(payload.Version) == "" {
		var out response.R
		if err := json.Unmarshal(body, &out); err == nil {
			t.Fatalf("expected backup payload on %s, got envelope code=%d msg=%q", path, out.Code, out.Msg)
		}
		t.Fatalf("expected non-empty backup payload version on %s, body=%s", path, string(body))
	}
	if payload.Configs == nil {
		t.Fatalf("expected configs map in backup payload on %s", path)
	}
	return payload
}

// exportFullBackup returns the UI's 全量导出 payload (raw tables, version 2.0).
func exportFullBackup(t *testing.T, router http.Handler, token string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/export", bytes.NewBufferString(`{"types":[],"mode":"full"}`))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected full export status 200, got %d", resp.Code)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode full backup: %v", err)
	}
	return payload
}

// backupRowValue finds the row of table whose matchCol equals matchVal in a
// full backup and returns its col value ("" when absent).
func backupRowValue(t *testing.T, backup map[string]interface{}, table, matchCol, matchVal, col string) string {
	t.Helper()
	rows, _ := backup[table].([]interface{})
	for _, item := range rows {
		row, _ := item.(map[string]interface{})
		if row != nil && fmt.Sprint(row[matchCol]) == matchVal {
			return fmt.Sprint(row[col])
		}
	}
	return ""
}

func postBackupImport(t *testing.T, router http.Handler, path, token string, body []byte) response.R {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	var out response.R
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s response: %v", path, err)
	}
	return out
}

// assertBackupRestoresConfigRow sets key=value, takes a full export, deletes
// the row and imports the vite_config table back through path: exactly that
// row must be restored with the exported value (the others already exist).
func assertBackupRestoresConfigRow(t *testing.T, router http.Handler, r *repo.Repository, token, path, key, value string) {
	t.Helper()
	if err := r.DB().Exec(`UPDATE vite_config SET value = ? WHERE name = ?`, value, key).Error; err != nil {
		t.Fatalf("set config before export: %v", err)
	}
	full := exportFullBackup(t, router, token)
	if got := backupRowValue(t, full, "vite_config", "name", key, "value"); got != value {
		t.Fatalf("expected %q=%s in full backup, got %q", key, value, got)
	}
	if err := r.DB().Exec(`DELETE FROM vite_config WHERE name = ?`, key).Error; err != nil {
		t.Fatalf("delete config before import: %v", err)
	}
	raw, err := json.Marshal(map[string]interface{}{
		"version":     full["version"],
		"exported_at": full["exported_at"],
		"types":       []string{"vite_config"},
		"vite_config": full["vite_config"],
	})
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}
	out := postBackupImport(t, router, path, token, raw)
	if out.Code != 0 {
		t.Fatalf("expected %s code 0, got %d (%s)", path, out.Code, out.Msg)
	}
	if counts, _ := out.Data.(map[string]interface{}); counts == nil || counts["vite_config"] != float64(1) {
		t.Fatalf("expected exactly 1 restored vite_config row from %s, got %v", path, out.Data)
	}
	cfg, err := r.GetConfigByName(key)
	if err != nil {
		t.Fatalf("query restored config: %v", err)
	}
	if cfg == nil || cfg.Value != value {
		t.Fatalf("expected restored config value %s, got %+v", value, cfg)
	}
}

func setupContractRouter(t *testing.T, jwtSecret string) (http.Handler, *repo.Repository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "contract.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
	})

	h := handler.New(r, jwtSecret, "test")
	return httpserver.NewRouter(h, jwtSecret, r), r
}

// The legacy tables below are written the way the old schema.sql files were
// (springboot-backend and go-backend/internal/store/sqlite/sql, both removed):
// two-space indented columns. They used to be indented with Go's tabs, which
// no released schema ever had and which the glebarez/sqlite DDL parser treats
// as quote characters (sqliteSeparator includes \t): its AlterColumn rebuild
// then copied only half the columns into node__temp and failed with
// "NOT NULL constraint failed: node__temp.name".
func TestOpenMigratesLegacyNodeDualStackColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-2.0.7-beta.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}

	t.Cleanup(func() {
		_ = legacyDB.Close()
	})

	if _, err := legacyDB.Exec(`
CREATE TABLE IF NOT EXISTS node (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name VARCHAR(100) NOT NULL,
  secret VARCHAR(100) NOT NULL,
  server_ip VARCHAR(100) NOT NULL,
  port TEXT NOT NULL,
  interface_name VARCHAR(200),
  version VARCHAR(100),
  http INTEGER NOT NULL DEFAULT 0,
  tls INTEGER NOT NULL DEFAULT 0,
  socks INTEGER NOT NULL DEFAULT 0,
  created_time INTEGER NOT NULL,
  updated_time INTEGER,
  status INTEGER NOT NULL,
  tcp_listen_addr VARCHAR(100) NOT NULL DEFAULT '[::]',
  udp_listen_addr VARCHAR(100) NOT NULL DEFAULT '[::]'
)
	`); err != nil {
		t.Fatalf("create legacy node table: %v", err)
	}

	if _, err := legacyDB.Exec(`
CREATE TABLE IF NOT EXISTS tunnel (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name VARCHAR(100) NOT NULL,
  traffic_ratio REAL NOT NULL DEFAULT 1.0,
  type INTEGER NOT NULL,
  protocol VARCHAR(10) NOT NULL DEFAULT 'tls',
  flow INTEGER NOT NULL,
  created_time INTEGER NOT NULL,
  updated_time INTEGER NOT NULL,
  status INTEGER NOT NULL,
  in_ip TEXT
)
	`); err != nil {
		t.Fatalf("create legacy tunnel table: %v", err)
	}

	now := time.Now().UnixMilli()
	if _, err := legacyDB.Exec(`
		INSERT INTO node(name, secret, server_ip, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "legacy-node", "legacy-secret", "10.10.0.1", "10000-10010", "eth0", "v-old", 1, 1, 1, now, now, 1, "[::]", "[::]"); err != nil {
		t.Fatalf("seed legacy node row: %v", err)
	}

	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
	})

	nodes, err := r.ListNodes(nil)
	if err != nil {
		t.Fatalf("list nodes after migration: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after migration, got %d", len(nodes))
	}
	assertLegacyNodeRowSurvived(t, r)

	columns := readTableColumns(t, r.DB(), "node")

	for _, required := range []string{"server_ip_v4", "server_ip_v6", "inx", "extra_ips"} {
		if !columns[required] {
			t.Fatalf("expected node column %q to exist after migration", required)
		}
	}

	tunnelColumns := readTableColumns(t, r.DB(), "tunnel")
	for _, required := range []string{"inx", "ip_preference"} {
		if !tunnelColumns[required] {
			t.Fatalf("expected tunnel column %q to exist after migration", required)
		}
	}
}

func TestOpenMigratesVeryLegacyNodeAndTunnelColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-1.x.db")
	legacyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}

	t.Cleanup(func() {
		_ = legacyDB.Close()
	})

	if _, err := legacyDB.Exec(`
CREATE TABLE IF NOT EXISTS node (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name VARCHAR(100) NOT NULL,
  secret VARCHAR(100) NOT NULL,
  server_ip VARCHAR(100) NOT NULL,
  port TEXT NOT NULL,
  interface_name VARCHAR(200),
  version VARCHAR(100),
  http INTEGER NOT NULL DEFAULT 0,
  tls INTEGER NOT NULL DEFAULT 0,
  socks INTEGER NOT NULL DEFAULT 0,
  created_time INTEGER NOT NULL,
  updated_time INTEGER,
  status INTEGER NOT NULL
)
	`); err != nil {
		t.Fatalf("create very legacy node table: %v", err)
	}

	if _, err := legacyDB.Exec(`
CREATE TABLE IF NOT EXISTS tunnel (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name VARCHAR(100) NOT NULL,
  traffic_ratio REAL NOT NULL DEFAULT 1.0,
  type INTEGER NOT NULL,
  protocol VARCHAR(10) NOT NULL DEFAULT 'tls',
  flow INTEGER NOT NULL,
  created_time INTEGER NOT NULL,
  updated_time INTEGER NOT NULL,
  status INTEGER NOT NULL,
  in_ip TEXT
)
	`); err != nil {
		t.Fatalf("create very legacy tunnel table: %v", err)
	}

	now := time.Now().UnixMilli()
	if _, err := legacyDB.Exec(`
		INSERT INTO node(name, secret, server_ip, port, interface_name, version, http, tls, socks, created_time, updated_time, status)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "legacy-node", "legacy-secret", "10.10.0.1", "10000-10010", "eth0", "v-old", 1, 1, 1, now, now, 1); err != nil {
		t.Fatalf("seed legacy node row: %v", err)
	}

	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
	})

	assertLegacyNodeRowSurvived(t, r)

	columns := readTableColumns(t, r.DB(), "node")
	for _, required := range []string{
		"server_ip_v4",
		"server_ip_v6",
		"extra_ips",
		"tcp_listen_addr",
		"udp_listen_addr",
		"inx",
		"is_remote",
		"remote_url",
		"remote_token",
		"remote_config",
	} {
		if !columns[required] {
			t.Fatalf("expected node column %q to exist after migration", required)
		}
	}

	tunnelColumns := readTableColumns(t, r.DB(), "tunnel")
	for _, required := range []string{"inx", "ip_preference"} {
		if !tunnelColumns[required] {
			t.Fatalf("expected tunnel column %q to exist after migration", required)
		}
	}
}

// assertLegacyNodeRowSurvived checks that the seeded legacy node keeps its
// data through the migration (no column lost in a table rebuild).
func assertLegacyNodeRowSurvived(t *testing.T, r *repo.Repository) {
	t.Helper()
	var row struct {
		Name          string
		Secret        string
		ServerIP      string
		Port          string
		InterfaceName string
		Version       string
		HTTP          int
		Socks         int
		Status        int
	}
	if err := r.DB().Raw(`SELECT name, secret, server_ip, port, interface_name, version, http, socks, status FROM node`).Row().
		Scan(&row.Name, &row.Secret, &row.ServerIP, &row.Port, &row.InterfaceName, &row.Version, &row.HTTP, &row.Socks, &row.Status); err != nil {
		t.Fatalf("read migrated legacy node: %v", err)
	}
	want := "legacy-node legacy-secret 10.10.0.1 10000-10010 eth0 v-old 1 1 1"
	got := fmt.Sprintf("%s %s %s %s %s %s %d %d %d", row.Name, row.Secret, row.ServerIP, row.Port, row.InterfaceName, row.Version, row.HTTP, row.Socks, row.Status)
	if got != want {
		t.Fatalf("legacy node row changed by migration: got %q, want %q", got, want)
	}
}

func readTableColumns(t *testing.T, db *gorm.DB, table string) map[string]bool {
	t.Helper()

	columnTypes, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}

	columns := map[string]bool{}
	for _, col := range columnTypes {
		name := strings.TrimSpace(col.Name())
		if name == "" {
			continue
		}
		columns[name] = true
	}

	return columns
}
