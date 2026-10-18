package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func landingContractRepo(t *testing.T, dialect string) *repo.Repository {
	t.Helper()
	var r *repo.Repository
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("FLVX_POSTGRES_TEST_DSN")
		if dsn == "" {
			t.Skip("set FLVX_POSTGRES_TEST_DSN to run PostgreSQL")
		}
		admin, e := sql.Open("pgx", dsn)
		if e != nil {
			t.Fatal(e)
		}
		schema := fmt.Sprintf("landing_contract_%d", time.Now().UnixNano())
		if _, e := admin.Exec(`CREATE SCHEMA "` + schema + `"`); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
		scoped, e := withSearchPath(dsn, schema)
		if e != nil {
			t.Fatal(e)
		}
		r, err = repo.OpenPostgres(scoped)
	} else {
		r, err = repo.Open(filepath.Join(t.TempDir(), "landing.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func TestLandingPermissionsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			for _, v := range []interface{}{&model.User{ID: 501, User: "landing-a", Status: 1, RoleID: 1}, &model.User{ID: 502, User: "landing-b", Status: 1, RoleID: 1}, &model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", Port: "10000-19999", LandingCapable: true}, &model.UserGroup{ID: 701, Name: "landing-granted", Status: 1}, &model.UserGroupUser{UserGroupID: 701, UserID: 501}} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "landing-contract", "test")
			router := httpserver.NewRouter(h, "landing-contract", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "landing-contract")
			a, _ := auth.GenerateToken(501, "landing-a", 1, "landing-contract")
			b, _ := auth.GenerateToken(502, "landing-b", 1, "landing-contract")
			call := func(path, token string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			for _, path := range []string{"server/enable", "server/save", "node/update", "user/get", "user/save"} {
				if out := call(path, a, map[string]interface{}{}); out.Code == 0 {
					t.Fatalf("non-admin %s succeeded", path)
				}
			}
			if out := call("server/enable", admin, map[string]interface{}{"nodeId": 36}); out.Code == 0 {
				t.Fatal("node36 enabled")
			}
			out := call("server/enable", admin, map[string]interface{}{"nodeId": 601})
			if out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			s, err := r.GetLandingServerForNode(601)
			if err != nil || s == nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(s, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if out := call("server/list", b, map[string]interface{}{}); out.Code != 0 || len(out.Data.([]interface{})) != 0 {
				t.Fatalf("ungranted sees server %+v", out)
			}
			if out := call("node/create", b, map[string]interface{}{"landingServerId": s.ID, "name": "forbidden", "protocol": "ss2022"}); out.Code == 0 {
				t.Fatal("ungranted creation")
			}
			out = call("node/create", a, map[string]interface{}{"landingServerId": s.ID, "name": "own", "protocol": "ss2022"})
			if out.Code != 0 {
				t.Fatalf("create %+v", out)
			}
			id := int64(out.Data.(map[string]interface{})["id"].(float64))
			n, _ := r.GetLandingNode(id)
			raw, _ := json.Marshal(out.Data)
			if strings.Contains(string(raw), n.ServerKey) || strings.Contains(string(raw), n.UserKey) {
				t.Fatal("node list leaked credentials")
			}
			for _, path := range []string{"node/delete", "node/links"} {
				if out := call(path, b, map[string]interface{}{"id": id}); out.Code == 0 {
					t.Fatalf("cross-owner %s", path)
				}
			}
			if out := call("node/list", b, map[string]interface{}{}); out.Code != 0 || len(out.Data.([]interface{})) != 0 {
				t.Fatalf("cross-owner list %+v", out)
			}
			out = call("node/links", a, map[string]interface{}{"id": id})
			if out.Code != 0 || len(out.Data.([]interface{})) != 1 {
				t.Fatalf("own links %+v", out)
			}
			if out := call("node/list", "invalid-token", map[string]interface{}{}); out.Code == 0 {
				t.Fatal("invalid JWT accepted")
			}
			if out := call("node/create", a, map[string]interface{}{"landingServerId": s.ID, "name": "override", "protocol": "ss2022", "maxConnections": 999}); out.Code == 0 {
				t.Fatal("user supplied admin field accepted")
			}
			if out := call("node/delete", a, map[string]interface{}{"id": id}); out.Code != 0 {
				t.Fatalf("own delete %+v", out)
			}
		})
	}
}

func TestLandingBillingHTTPContract(t *testing.T) {
	const gb int64 = 1 << 30
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, tc := range []struct {
				name                      string
				tunnelRatio, landingRatio float64
				via, own                  bool
				want                      int64
			}{{"ratio1-via", 1, 1, true, true, 2 * gb}, {"ratio1-direct", 1, 1, false, true, 2 * gb}, {"premium-tunnel", 3, 1, true, true, 6 * gb}, {"premium-landing", 1, 3, true, true, 6 * gb}, {"foreign-landing", 1, 1, true, false, 2 * gb}} {
				t.Run(tc.name, func(t *testing.T) {
					r := landingContractRepo(t, dialect)
					owner := int64(501)
					landingID := int64(1)
					if !tc.own {
						owner = 502
						landingID = 0
					}
					now := time.Now().UnixMilli()
					rows := []interface{}{&model.User{ID: 501, User: "billing-a", RoleID: 1, Status: 1, Flow: 99999, ExpTime: now + 86400000, FlowResetTime: 1}, &model.User{ID: 502, User: "billing-b", RoleID: 1, Status: 1, Flow: 99999, ExpTime: now + 86400000, FlowResetTime: 1}, &model.Node{ID: 601, Name: "landing", Secret: "landing-contract-secret", ServerIP: "203.0.113.1", Port: "10000-19999"}, &model.Node{ID: 602, Name: "entry", Secret: "entry-contract-secret", ServerIP: "203.0.113.2", Port: "10000-19999"}, &model.Tunnel{ID: 801, Name: "billing", Status: 1, Type: 2, Flow: 2, TrafficRatio: tc.tunnelRatio}, &model.UserTunnel{ID: 701, UserID: 501, TunnelID: 801, Status: 1, Flow: 99999, ExpTime: now + 86400000}, &model.Forward{ID: 901, UserID: 501, UserName: "billing-a", TunnelID: 801, Status: 1, LandingNodeID: landingID, RemoteAddr: "203.0.113.1:30001"}, &model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 12000}, &model.ChainTunnel{TunnelID: 801, NodeID: 602, ChainType: "1"}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"}, &model.LandingServer{ID: 1, Source: "native", NodeID: 601, Name: "landing", PublicHost: "203.0.113.1", FlowType: 2, TrafficRatio: tc.landingRatio, Status: "active"}, &model.LandingNode{ID: 1, UserID: owner, LandingServerID: 1, Name: "landing", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"}, &model.UserGroup{ID: 703, Name: "billing-grant", Status: 1}, &model.UserGroupUser{UserID: owner, UserGroupID: 703}, &model.LandingServerUserGroup{LandingServerID: 1, UserGroupID: 703}}
					for _, row := range rows {
						if err := r.DB().Create(row).Error; err != nil {
							t.Fatal(err)
						}
					}
					h := handler.New(r, "billing-contract", "test")
					router := httpserver.NewRouter(h, "billing-contract", r)
					upload := func(secret string, body interface{}, repeat int) {
						t.Helper()
						plain, _ := json.Marshal(body)
						cipher, err := security.NewAESCrypto(secret)
						if err != nil {
							t.Fatal(err)
						}
						encrypted, err := cipher.Encrypt(plain)
						if err != nil {
							t.Fatal(err)
						}
						raw, _ := json.Marshal(map[string]interface{}{"encrypted": true, "data": encrypted})
						for i := 0; i < repeat; i++ {
							req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret="+secret, bytes.NewReader(raw))
							w := httptest.NewRecorder()
							router.ServeHTTP(w, req)
							if w.Code != 200 || w.Body.String() != "ok" {
								t.Fatalf("flow upload %d %s", w.Code, w.Body.String())
							}
						}
					}
					if tc.via {
						upload("entry-contract-secret", []map[string]interface{}{{"n": "901_501_701_tcp", "d": gb / 4, "u": 3 * gb / 4}}, 2)
					}
					counter := map[string]interface{}{"id": 1, "inbound_tag": "flvxl-1", "epoch": "100", "up_bytes": gb / 4, "down_bytes": 3 * gb / 4}
					upload("landing-contract-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}}, 2)
					// A different encrypted envelope carrying the same cumulative snapshot is also idempotent.
					upload("landing-contract-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}}, 1)
					var user model.User
					if err := r.DB().First(&user, 501).Error; err != nil {
						t.Fatal(err)
					}
					if user.InFlow+user.OutFlow != tc.want {
						t.Fatalf("billed=%d want=%d", user.InFlow+user.OutFlow, tc.want)
					}
					var quota model.UserQuota
					if err := r.DB().First(&quota, "user_id = ?", 501).Error; err != nil {
						t.Fatal(err)
					}
					if quota.MonthlyUsedBytes != tc.want {
						t.Fatalf("monthly=%d want=%d", quota.MonthlyUsedBytes, tc.want)
					}
					if tc.own {
						var q model.UserServerQuota
						if err := r.DB().First(&q, "user_id = ? AND server_kind = ? AND server_ref_id = ?", 501, "node", 601).Error; err != nil {
							t.Fatal(err)
						}
						if q.UsedIn+q.UsedOut != tc.want {
							t.Fatalf("landing-server=%d want=%d", q.UsedIn+q.UsedOut, tc.want)
						}
					}
					if tc.via {
						var q model.UserServerQuota
						if err := r.DB().First(&q, "user_id = ? AND server_kind = ? AND server_ref_id = ?", 501, "node", 602).Error; err != nil {
							t.Fatal(err)
						}
						if q.UsedIn+q.UsedOut != tc.want {
							t.Fatalf("entry-server=%d want=%d", q.UsedIn+q.UsedOut, tc.want)
						}
					}
				})
			}
		})
	}
}

func TestLandingMixedForwardSaveContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			for _, row := range []interface{}{&model.User{ID: 501, User: "mixed-user", RoleID: 1, Status: 1, Flow: 99999, Num: 100, ExpTime: now + 86400000}, &model.Node{ID: 601, Name: "landing", ServerIP: "203.0.113.1", Port: "10000-19999"}, &model.Node{ID: 602, Name: "entry", ServerIP: "203.0.113.2", Port: "10000-19999"}, &model.Tunnel{ID: 801, Name: "mixed-tunnel", Type: 2, Status: 1, Flow: 2, TrafficRatio: 1}, &model.ChainTunnel{TunnelID: 801, NodeID: 602, ChainType: "1"}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"}, &model.UserTunnel{ID: 701, UserID: 501, TunnelID: 801, Status: 1, Flow: 99999, Num: 100, ExpTime: now + 86400000}, &model.LandingServer{ID: 1, NodeID: 601, Source: "native", Name: "landing", PublicHost: "203.0.113.1", Status: "active"}, &model.LandingNode{ID: 1, UserID: 501, LandingServerID: 1, Port: 30001, Protocol: "ss2022", Status: "active"}, &model.Forward{ID: 901, UserID: 501, TunnelID: 801, Name: "existing", RemoteAddr: "1.1.1.1:443", Status: 1}, &model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 12000}} {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "mixed-contract", "test")
			router := httpserver.NewRouter(h, "mixed-contract", r)
			token, _ := auth.GenerateToken(501, "mixed-user", 1, "mixed-contract")
			for _, path := range []string{"create", "update"} {
				raw, _ := json.Marshal(map[string]interface{}{"id": 901, "tunnelId": 801, "name": "mixed", "remoteAddr": "203.0.113.1:30001,1.1.1.1:443", "inPort": 12000})
				req := httptest.NewRequest(http.MethodPost, "/api/v1/forward/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				if out.Code == 0 || !strings.Contains(out.Msg, "只能设置一个目标") {
					t.Fatalf("mixed %s result %+v", path, out)
				}
			}
			var f model.Forward
			r.DB().First(&f, 901)
			if f.RemoteAddr != "1.1.1.1:443" {
				t.Fatal("rejected update changed saved target")
			}
		})
	}
}
