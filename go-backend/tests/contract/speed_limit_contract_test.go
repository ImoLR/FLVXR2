package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
)

// Plan 170: 限速规则 (incl. 竞技场) are retired. Create / update are refused
// with a plain message (no 500); list / detail stay read-only.
func TestSpeedLimitRetiredWritesContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	call := func(path, body string) response.R {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: http %d", path, res.Code)
		}
		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		return out
	}

	existing := mustCreateSpeedLimitRepo(t, r, "old-limit")
	for _, c := range []struct{ path, body string }{
		{"/api/v1/speed-limit/create", `{"name":"x","speed":100,"status":1}`},
		{"/api/v1/speed-limit/create", `{"name":"arena","speed":100,"status":1,"arenaMode":1}`},
		{"/api/v1/speed-limit/update", `{"id":` + jsonInt(existing) + `,"name":"old-limit","speed":5,"status":1,"arenaMode":1,"bindings":{"tunnelIds":[1],"forwardIds":[1]}}`},
	} {
		out := call(c.path, c.body)
		if out.Code == 0 || !strings.Contains(out.Msg, "已停用") {
			t.Fatalf("%s %s: expected the retired message, got code=%d msg=%q", c.path, c.body, out.Code, out.Msg)
		}
	}
	if n := mustQueryInt(t, r, `SELECT COUNT(1) FROM speed_limit`); n != 1 {
		t.Fatalf("speed_limit rows changed: %d", n)
	}
	var speed, arena int
	if err := r.DB().Raw(`SELECT speed, arena_mode FROM speed_limit WHERE id = ?`, existing).Row().Scan(&speed, &arena); err != nil || speed != 100 || arena != 0 {
		t.Fatalf("existing limit changed: speed=%d arena=%d err=%v", speed, arena, err)
	}

	out := call("/api/v1/speed-limit/list", `{}`)
	data, ok := out.Data.([]interface{})
	if out.Code != 0 || !ok || len(data) != 1 {
		t.Fatalf("list: code=%d data=%#v", out.Code, out.Data)
	}
	if m, _ := data[0].(map[string]interface{}); m["name"] != "old-limit" || (m["tunnelId"] != nil) {
		t.Fatalf("list item %#v", data[0])
	}
	if out := call("/api/v1/speed-limit/detail", `{"id":`+jsonInt(existing)+`}`); out.Code != 0 {
		t.Fatalf("detail: code=%d msg=%s", out.Code, out.Msg)
	}
}

// Plan 170 bug fix: deleting a 限速规则 clears every reference to it in the same
// transaction (before, forward / tunnel / user_tunnel / user / node kept the
// dangling id).
func TestSpeedLimitDeleteClearsReferencesContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	speedID := mustCreateSpeedLimitRepo(t, r, "bind-limit")
	otherID := mustCreateSpeedLimitRepo(t, r, "other-limit")
	now := time.Now().UnixMilli()
	for _, q := range []struct {
		sql  string
		args []interface{}
	}{
		{`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status, speed_limit_id)
		  VALUES(101, 'bind_user', 'pwd', 1, ?, 100, 0, 0, 1, 10, ?, ?, 1, ?)`, []interface{}{now + 86400000, now, now, speedID}},
		{`INSERT INTO node(id, name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx, speed_id)
		  VALUES(201, 'bind_node', 'secret', '10.0.0.201', '10.0.0.201', '', '20000-30000', 'eth0', 'test', 0, 0, 0, ?, ?, 1, '[::]', '[::]', 1, ?)`, []interface{}{now, now, speedID}},
		{`INSERT INTO tunnel(id, name, type, flow, traffic_ratio, created_time, updated_time, status, inx, speed_id)
		  VALUES(301, 'bind_tunnel', 1, 1, 1.0, ?, ?, 1, 1, ?)`, []interface{}{now, now, speedID}},
		{`INSERT INTO tunnel(id, name, type, flow, traffic_ratio, created_time, updated_time, status, inx, speed_id)
		  VALUES(302, 'other_tunnel', 1, 1, 1.0, ?, ?, 1, 2, ?)`, []interface{}{now, now, otherID}},
		{`INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		  VALUES(501, 101, 301, ?, 10, 100, 0, 0, 1, ?, 1)`, []interface{}{speedID, now + 86400000}},
		{`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx, speed_id)
		  VALUES(401, 101, 'bind_user', 'bind_forward', 301, '127.0.0.1:80', 'fifo', 0, 0, ?, ?, 0, 1, ?)`, []interface{}{now, now, speedID}},
	} {
		if err := r.DB().Exec(q.sql, q.args...).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/delete", bytes.NewBufferString(`{"id":`+jsonInt(speedID)+`}`))
	req.Header.Set("Authorization", adminToken)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	assertCode(t, res, 0)

	for _, c := range []struct {
		table, column string
		id            int64
	}{{"user", "speed_limit_id", 101}, {"node", "speed_id", 201}, {"tunnel", "speed_id", 301}, {"user_tunnel", "speed_id", 501}, {"forward", "speed_id", 401}} {
		var got sql.NullInt64
		if err := r.DB().Raw(`SELECT `+c.column+` FROM "`+c.table+`" WHERE id = ?`, c.id).Row().Scan(&got); err != nil {
			t.Fatalf("query %s.%s: %v", c.table, c.column, err)
		}
		if got.Valid {
			t.Fatalf("%s.%s still references the deleted limit: %d", c.table, c.column, got.Int64)
		}
	}
	if n := mustQueryInt(t, r, `SELECT COUNT(1) FROM speed_limit WHERE id = ?`, speedID); n != 0 {
		t.Fatalf("limit not deleted")
	}
	var other sql.NullInt64
	if err := r.DB().Raw(`SELECT speed_id FROM tunnel WHERE id = 302`).Row().Scan(&other); err != nil || !other.Valid || other.Int64 != otherID {
		t.Fatalf("unrelated reference touched: %v %v", other, err)
	}
}

func TestSpeedLimitDatabaseNullableFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-null.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	id, err := r.CreateSpeedLimit("db-test-limit", 100, 1, 1, 0)
	if err != nil {
		t.Fatalf("CreateSpeedLimit failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected valid id, got %d", id)
	}

	var tunnelID sql.NullInt64
	var tunnelName sql.NullString
	err = r.DB().Raw("SELECT tunnel_id, tunnel_name FROM speed_limit WHERE id = ?", id).Row().Scan(&tunnelID, &tunnelName)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if tunnelID.Valid {
		t.Fatalf("expected TunnelID to be NULL, got %d", tunnelID.Int64)
	}
	if tunnelName.Valid && tunnelName.String != "" {
		t.Fatalf("expected TunnelName to be NULL or empty, got %s", tunnelName.String)
	}
}

func TestSpeedLimitUpdateClearsHistoricalBinding(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-update-clear.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	tunnelID := mustCreateSpeedLimitTunnel(t, r, "speed-limit-update-clear-tunnel")
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO speed_limit(name, speed, tunnel_id, tunnel_name, created_time, updated_time, status)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, "speed-limit-update-clear", 300, tunnelID, "speed-limit-update-clear-tunnel", now, now, 1).Error; err != nil {
		t.Fatalf("insert speed limit with tunnel binding: %v", err)
	}
	speedLimitID := mustLastInsertID(t, r, "speed-limit-update-clear")

	err = r.UpdateSpeedLimit(speedLimitID, "speed-limit-update-clear", 512, 1, 0, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("UpdateSpeedLimit failed: %v", err)
	}

	var dbTunnelID sql.NullInt64
	var dbTunnelName sql.NullString
	err = r.DB().Raw("SELECT tunnel_id, tunnel_name FROM speed_limit WHERE id = ?", speedLimitID).Row().Scan(&dbTunnelID, &dbTunnelName)
	if err != nil {
		t.Fatalf("query updated speed limit failed: %v", err)
	}
	if dbTunnelID.Valid {
		t.Fatalf("expected tunnel_id cleared after update, got %d", dbTunnelID.Int64)
	}
	if dbTunnelName.Valid && dbTunnelName.String != "" {
		t.Fatalf("expected tunnel_name cleared after update, got %q", dbTunnelName.String)
	}
}

func TestSpeedLimitGetSpeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-getspeed.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	speedLimitID, err := r.CreateSpeedLimit("get-speed-test", 500, 1, 1, 0)
	if err != nil {
		t.Fatalf("create speed limit: %v", err)
	}

	t.Run("GetSpeedLimitSpeed returns correct speed", func(t *testing.T) {
		speed, err := r.GetSpeedLimitSpeed(speedLimitID)
		if err != nil {
			t.Fatalf("GetSpeedLimitSpeed failed: %v", err)
		}
		if speed != 500 {
			t.Fatalf("expected speed 500, got %d", speed)
		}
	})

	t.Run("GetSpeedLimitSpeed returns error for non-existent id", func(t *testing.T) {
		_, err := r.GetSpeedLimitSpeed(99999)
		if err == nil {
			t.Fatal("expected error for non-existent speed limit ID")
		}
	})
}

func mustCreateSpeedLimitTunnel(t *testing.T, r *repo.Repository, name string) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, 1.0, 1, 'tls', 99999, ?, ?, 1, NULL, 0)
	`, name, now, now).Error; err != nil {
		t.Fatalf("create tunnel failed: %v", err)
	}
	return mustLastInsertID(t, r, name)
}

func mustCreateSpeedLimitRepo(t *testing.T, r *repo.Repository, name string) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	id, err := r.CreateSpeedLimit(name, 100, now, 1, 0)
	if err != nil {
		t.Fatalf("create speed limit failed: %v", err)
	}
	return id
}
