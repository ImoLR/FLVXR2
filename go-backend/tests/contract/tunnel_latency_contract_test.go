package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Real JWTs through NewRouter, including its global admin route guard. Offline
// nodes produce immediate timeout snapshots without external network traffic.
func TestTunnelLatencyFullRouterJWTEntryPermissions(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "latency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, row := range []interface{}{
		&model.User{ID: 7, User: "limited", RoleID: 1},
		&model.Node{ID: 11, Name: "private-entry-a", ServerIP: "192.0.2.11"},
		&model.Node{ID: 12, Name: "private-entry-b", ServerIP: "192.0.2.12"},
		&model.Node{ID: 13, Name: "private-exit", ServerIP: "192.0.2.13"},
		&model.Tunnel{ID: 8, Name: "visible", Type: 2, Status: 1},
		&model.Tunnel{ID: 9, Name: "hidden", Type: 2, Status: 1},
		&model.ChainTunnel{TunnelID: 8, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 8, ChainType: "1", NodeID: 12},
		&model.ChainTunnel{TunnelID: 8, ChainType: "3", NodeID: 13},
		&model.ChainTunnel{TunnelID: 9, ChainType: "1", NodeID: 11},
		&model.ChainTunnel{TunnelID: 9, ChainType: "3", NodeID: 13},
		&model.UserTunnel{ID: 10, UserID: 7, TunnelID: 8},
		&model.UserGroupUser{UserGroupID: 20, UserID: 7},
		&model.GroupPermission{UserGroupID: 20, TunnelGroupID: 30},
		&model.GroupPermissionGrant{UserGroupID: 20, TunnelGroupID: 30, UserTunnelID: 10, CreatedByGroup: 1},
		&model.TunnelGroupTunnelEntry{TunnelGroupID: 30, TunnelID: 8, NodeID: 12},
	} {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := handler.New(r, "secret", "test")
	h.StartBackgroundJobs()
	defer h.StopBackgroundJobs()
	router := httpserver.NewRouter(h, "secret", r)
	request := func(user int64, role int) (string, []map[string]interface{}) {
		t.Helper()
		token, err := auth.GenerateToken(user, "test", role, "secret")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil)
		req.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var body struct {
			Code int
			Data []map[string]interface{}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != 0 {
			t.Fatalf("full router response=%s err=%v", w.Body.String(), err)
		}
		return w.Body.String(), body.Data
	}
	deadline := time.Now().Add(3 * time.Second)
	var admin []map[string]interface{}
	for time.Now().Before(deadline) {
		_, admin = request(1, 0)
		if len(admin) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(admin) != 2 || len(admin[0]["entries"].([]interface{})) != 2 {
		t.Fatalf("admin=%v", admin)
	}
	raw, user := request(7, 1)
	if len(user) != 1 || user[0]["tunnelId"] != float64(8) || len(user[0]["entries"].([]interface{})) != 1 {
		t.Fatalf("limited=%s", raw)
	}
	for _, forbidden := range []string{"nodeId", "entryName", "private-", "192.0.2", "port", "host"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("leaked %q: %s", forbidden, raw)
		}
	}
	entry := user[0]["entries"].([]interface{})[0].(map[string]interface{})
	if len(entry) != 2 || entry["status"] != "timeout" {
		t.Fatalf("entry fields=%v", entry)
	}
	// Unauthenticated requests still require a session.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tunnel/user/latency", nil))
	assertCode(t, w, 401)
}
