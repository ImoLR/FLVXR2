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
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 110 over HTTP: direct grants via the user landing settings, group-only,
// none and admin visibility, admin creating a node for a user, and the
// landing-quota auto-grant (the exact prod scenario: user 26 + shared server 3).
func TestLandingDirectGrantContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			future := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
			for _, v := range []interface{}{
				&model.User{ID: 501, User: "group-user", Status: 1, RoleID: 1, ExpTime: future},
				&model.User{ID: 502, User: "direct-user", Status: 1, RoleID: 1, ExpTime: future},
				&model.User{ID: 503, User: "nobody", Status: 1, RoleID: 1, ExpTime: future},
				&model.User{ID: 526, User: "leekao", Status: 1, RoleID: 1, ExpTime: future},
				&model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", Port: "10000-19999", LandingCapable: true},
				&model.UserGroup{ID: 701, Name: "landing-granted", Status: 1},
				&model.UserGroupUser{UserGroupID: 701, UserID: 501},
			} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "landing-grant-contract", "test")
			router := httpserver.NewRouter(h, "landing-grant-contract", r)
			token := func(id int64, name string, role int) string {
				v, _ := auth.GenerateToken(id, name, role, "landing-grant-contract")
				return v
			}
			admin, groupUser, directUser, nobody, leekao := token(1, "admin", 0), token(501, "group-user", 1), token(502, "direct-user", 1), token(503, "nobody", 1), token(526, "leekao", 1)
			call := func(path, tok string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", tok)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			listIDs := func(tok string) []int64 {
				t.Helper()
				out := call("server/list", tok, map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("list %+v", out)
				}
				ids := []int64{}
				for _, item := range out.Data.([]interface{}) {
					ids = append(ids, int64(item.(map[string]interface{})["id"].(float64)))
				}
				return ids
			}
			if out := call("server/enable", admin, map[string]interface{}{"nodeId": 601}); out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			native, _ := r.GetLandingServerForNode(601)
			if err := r.SaveLandingServer(native, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if err := r.SyncSharedLandings([]landingcustom.Share{{ID: 5, Name: "Boil Hinet 马年", Hosts: landingcustom.Hosts{IPv4: "114.41.205.96"}, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022", "vless_reality_vision"}}}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			var shared model.LandingServer
			if err := r.DB().Where("source = 'shared'").First(&shared).Error; err != nil {
				t.Fatal(err)
			}

			// admin sees both; group-only sees native; nobody sees nothing
			if got := listIDs(admin); len(got) != 2 {
				t.Fatalf("admin sees %v", got)
			}
			if got := listIDs(groupUser); len(got) != 1 || got[0] != native.ID {
				t.Fatalf("group user sees %v", got)
			}
			if got := listIDs(nobody); len(got) != 0 {
				t.Fatalf("ungranted user sees %v", got)
			}

			// direct grant via user/save serverIds; user/get returns it
			if out := call("user/save", admin, map[string]interface{}{"userId": 502, "maxNodes": 2, "quotas": []interface{}{}, "serverIds": []int64{shared.ID}}); out.Code != 0 {
				t.Fatalf("grant save %+v", out)
			}
			out := call("user/get", admin, map[string]interface{}{"userId": 502})
			if out.Code != 0 {
				t.Fatalf("user/get %+v", out)
			}
			got := out.Data.(map[string]interface{})
			if ids := got["serverIds"].([]interface{}); len(ids) != 1 || int64(ids[0].(float64)) != shared.ID || got["maxNodes"].(float64) != 2 {
				t.Fatalf("user/get grants %+v", got)
			}
			if _, ok := got["fixedNodeCount"]; !ok {
				t.Fatal("fixedNodeCount missing")
			}
			if ids := listIDs(directUser); len(ids) != 1 || ids[0] != shared.ID {
				t.Fatalf("direct user sees %v", ids)
			}
			if out := call("node/create", directUser, map[string]interface{}{"landingServerId": shared.ID, "name": "mine", "protocol": "ss2022"}); out.Code != 0 {
				t.Fatalf("direct user create %+v", out)
			}
			// omitting serverIds keeps grants (older clients)
			if out := call("user/save", admin, map[string]interface{}{"userId": 502, "maxNodes": 2, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatalf("save without serverIds %+v", out)
			}
			if ids := listIDs(directUser); len(ids) != 1 {
				t.Fatalf("save without serverIds dropped grant %v", ids)
			}

			// prod scenario: admin adds a landing quota for the shared server → leekao sees it
			if out := call("user/save", admin, map[string]interface{}{"userId": 526, "maxNodes": 2, "quotas": []interface{}{map[string]interface{}{"serverKind": "landing", "serverRefId": shared.ID, "limitBytes": int64(1000) << 30}}}); out.Code != 0 {
				t.Fatalf("quota save %+v", out)
			}
			if ids := listIDs(leekao); len(ids) != 1 || ids[0] != shared.ID {
				t.Fatalf("quota did not grant: %v", ids)
			}

			// admin creates for another user: owner access + limits apply
			out = call("node/create", admin, map[string]interface{}{"landingServerId": shared.ID, "userId": 503, "name": "for nobody", "protocol": "ss2022"})
			if out.Code == 0 || !strings.Contains(out.Msg, "可用落地服务器") {
				t.Fatalf("create for ungranted user %+v", out)
			}
			out = call("node/create", admin, map[string]interface{}{"landingServerId": shared.ID, "userId": 526, "name": "keekao ss2022", "protocol": "ss2022"})
			if out.Code != 0 || int64(out.Data.(map[string]interface{})["userId"].(float64)) != 526 {
				t.Fatalf("create for leekao %+v", out)
			}
			if out := call("node/create", admin, map[string]interface{}{"landingServerId": shared.ID, "name": "admin own", "protocol": "ss2022"}); out.Code != 0 || int64(out.Data.(map[string]interface{})["userId"].(float64)) != 1 {
				t.Fatalf("admin own create %+v", out)
			}
			// a non-admin cannot create for someone else (userId ignored)
			out = call("node/create", leekao, map[string]interface{}{"landingServerId": shared.ID, "userId": 502, "name": "spoof", "protocol": "ss2022"})
			if out.Code != 0 || int64(out.Data.(map[string]interface{})["userId"].(float64)) != 526 {
				t.Fatalf("non-admin userId spoof %+v", out)
			}
			// leekao is now at maxNodes 2
			if out := call("node/create", admin, map[string]interface{}{"landingServerId": shared.ID, "userId": 526, "name": "third", "protocol": "ss2022"}); out.Code == 0 || !strings.Contains(out.Msg, "上限") {
				t.Fatalf("owner max nodes bypassed %+v", out)
			}

			// revoke (quota row stays); leekao's list empties
			if out := call("user/save", admin, map[string]interface{}{"userId": 526, "maxNodes": 2, "quotas": []interface{}{map[string]interface{}{"serverKind": "landing", "serverRefId": shared.ID, "limitBytes": int64(1000) << 30}}, "serverIds": []int64{}}); out.Code != 0 {
				t.Fatalf("revoke %+v", out)
			}
			if ids := listIDs(leekao); len(ids) != 0 {
				t.Fatalf("revoked user sees %v", ids)
			}
			if out := call("user/save", admin, map[string]interface{}{"userId": 526, "maxNodes": 2, "quotas": []interface{}{}, "serverIds": []int64{424242}}); out.Code == 0 {
				t.Fatal("unknown server granted")
			}
			// admin server view carries the direct-grant count for the hint
			out = call("server/list", admin, map[string]interface{}{})
			for _, item := range out.Data.([]interface{}) {
				m := item.(map[string]interface{})
				if int64(m["id"].(float64)) == shared.ID && m["directUserCount"].(float64) != 1 {
					t.Fatalf("directUserCount %+v", m["directUserCount"])
				}
			}
			if out := call("user/save", groupUser, map[string]interface{}{"userId": 501, "maxNodes": 2, "quotas": []interface{}{}, "serverIds": []int64{shared.ID}}); out.Code == 0 {
				t.Fatal("non-admin granted itself")
			}
		})
	}
}
