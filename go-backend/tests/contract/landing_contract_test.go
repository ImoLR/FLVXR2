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
