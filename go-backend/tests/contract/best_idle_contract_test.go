package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 182: a 最优 tunnel is "in use" (may switch) while at least one rule —
// active or paused — is on it. The path round reads that with one query
// (ListTunnelIDsWithForwards); rule create / pause / delete through the API
// move a tunnel in and out of it, on SQLite and PostgreSQL.
func TestBestPathTunnelInUseFollowsRulesContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			secret := "contract-jwt-secret"
			r := landingContractRepo(t, dialect)
			router := httpserver.NewRouter(handler.New(r, secret, "test"), secret, r)
			adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UnixMilli()
			create := func(v interface{}) {
				t.Helper()
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatalf("create %T: %v", v, err)
				}
			}
			str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
			num := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
			create(&model.Node{ID: 61, Name: "idle-entry", Secret: "idle-entry-secret", ServerIP: "10.61.0.1", ServerIPV4: str("10.61.0.1"), Port: "41000-41010", TCPListenAddr: "[::]", UDPListenAddr: "[::]", CreatedTime: now, Status: 1})
			create(&model.Node{ID: 62, Name: "idle-exit-a", Secret: "idle-exit-a", ServerIP: "10.61.0.2", ServerIPV4: str("10.61.0.2"), Port: "42000-42010", CreatedTime: now, Status: 1})
			create(&model.Node{ID: 63, Name: "idle-exit-b", Secret: "idle-exit-b", ServerIP: "10.61.0.3", ServerIPV4: str("10.61.0.3"), Port: "43000-43010", CreatedTime: now, Status: 1})
			for _, tid := range []int64{70, 71} {
				create(&model.Tunnel{ID: tid, Name: "idle-best", TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, InIP: str("10.61.0.1")})
				create(&model.ChainTunnel{TunnelID: tid, ChainType: "1", NodeID: 61, Inx: num(1), Strategy: str("round"), Protocol: str("tls")})
				create(&model.ChainTunnel{TunnelID: tid, ChainType: "3", NodeID: 62, Port: num(42001 + tid - 70), Inx: num(1), Strategy: str("best"), Protocol: str("tls")})
				create(&model.ChainTunnel{TunnelID: tid, ChainType: "3", NodeID: 63, Port: num(43001 + tid - 70), Inx: num(2), Strategy: str("best"), Protocol: str("tls")})
			}

			server := httptest.NewServer(router)
			defer server.Close()
			stop := startMockNodeSession(t, server.URL, "idle-entry-secret")
			defer stop()

			post := func(path string, payload map[string]interface{}) {
				t.Helper()
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				req.Header.Set("Authorization", adminToken)
				req.Header.Set("Content-Type", "application/json")
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				assertCode(t, res, 0)
			}
			inUse := func() map[int64]bool {
				t.Helper()
				ids, err := r.ListTunnelIDsWithForwards()
				if err != nil {
					t.Fatal(err)
				}
				out := map[int64]bool{}
				for _, id := range ids {
					if out[id] {
						t.Fatalf("duplicate tunnel id %d in %v", id, ids)
					}
					out[id] = true
				}
				return out
			}

			if got := inUse(); len(got) != 0 {
				t.Fatalf("fresh tunnels in use: %v", got)
			}
			post("/api/v1/forward/create", map[string]interface{}{"name": "idle-r1", "tunnelId": 70, "remoteAddr": "1.1.1.1:443"})
			post("/api/v1/forward/create", map[string]interface{}{"name": "idle-r2", "tunnelId": 70, "remoteAddr": "1.0.0.1:443"})
			if got := inUse(); !got[70] || got[71] || len(got) != 1 {
				t.Fatalf("after two rules on 70: %v", got)
			}
			var ids []int64
			if err := r.DB().Model(&model.Forward{}).Where("tunnel_id = ?", 70).Order("id").Pluck("id", &ids).Error; err != nil || len(ids) != 2 {
				t.Fatalf("forward ids %v %v", ids, err)
			}
			// A paused rule keeps its entry services: still in use.
			post("/api/v1/forward/pause", map[string]interface{}{"id": ids[0]})
			post("/api/v1/forward/delete", map[string]interface{}{"id": ids[1]})
			if got := inUse(); !got[70] {
				t.Fatalf("paused rule must keep 70 in use: %v", got)
			}
			post("/api/v1/forward/delete", map[string]interface{}{"id": ids[0]})
			if got := inUse(); len(got) != 0 {
				t.Fatalf("after the last rule: %v", got)
			}
		})
	}
}
