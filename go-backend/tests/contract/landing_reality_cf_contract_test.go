package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 290: the 域名池 rows carry the Cloudflare state next to the admin's own
// block (blocked = effective, manualBlocked = the admin's / risk rule's,
// cfHosted + cfEvidence + cfReason), a Cloudflare row cannot be unblocked,
// and the admin server list carries realityRisk (users never do).
func TestLandingRealityCFContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			for _, v := range []interface{}{
				&model.User{ID: 541, User: "cf-user", Status: 1, RoleID: 1},
				&model.Node{ID: 641, Name: "native", ServerIP: "203.0.113.41", Port: "10000-19999", LandingCapable: true},
			} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "landing-cf", "test")
			router := httpserver.NewRouter(h, "landing-cf", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "landing-cf")
			user, _ := auth.GenerateToken(541, "cf-user", 1, "landing-cf")
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
			rows := func() map[string]map[string]interface{} {
				t.Helper()
				out := call("reality/domains", admin, map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("domains %+v", out)
				}
				m := map[string]map[string]interface{}{}
				for _, d := range out.Data.(map[string]interface{})["domains"].([]interface{}) {
					v := d.(map[string]interface{})
					m[v["domain"].(string)] = v
				}
				return m
			}
			if out := call("reality/domains", user, map[string]interface{}{}); out.Code != 403 {
				t.Fatalf("user domains %+v", out)
			}
			// Mark one built-in as hosted on Cloudflare (the check's verdict).
			if _, err := r.ApplyRealityCFVerdicts(map[string]repo.RealityCFVerdict{
				"www.tesla.com":      {Hosted: true, Known: true, Evidence: "104.18.1.1（面板解析）"},
				"www.cloudflare.com": {Hosted: true, Known: true, Evidence: "104.16.123.96（面板解析）"},
			}); err != nil {
				t.Fatal(err)
			}
			m := rows()
			tesla, cfcom, amd := m["www.tesla.com"], m["www.cloudflare.com"], m["www.amd.com"]
			keys := []string{}
			for k := range tesla {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if got := strings.Join(keys, ","); got != "blocked,cfEvidence,cfHosted,cfReason,createdTime,domain,id,inPool,manualBlocked,note,source,sortOrder,trial,updatedTime" {
				t.Fatalf("row fields %s", got)
			}
			if tesla["blocked"] != true || tesla["manualBlocked"] != false || tesla["cfHosted"] != true || !strings.Contains(tesla["cfReason"].(string), "Cloudflare 托管") || tesla["cfEvidence"] != "104.18.1.1（面板解析）" {
				t.Fatalf("tesla %v", tesla)
			}
			if cfcom["blocked"] != true || cfcom["manualBlocked"] != true || cfcom["cfHosted"] != true {
				t.Fatalf("www.cloudflare.com %v", cfcom)
			}
			if amd["blocked"] != false || amd["manualBlocked"] != false || amd["cfHosted"] != false {
				t.Fatalf("amd %v", amd)
			}
			if _, ok := amd["cfReason"]; ok {
				t.Fatalf("cfReason on a non-CF row %v", amd)
			}
			if out := call("reality/domain/block", admin, map[string]interface{}{"id": tesla["id"], "blocked": false}); out.Code == 0 || !strings.Contains(out.Msg, "Cloudflare") {
				t.Fatalf("CF unblock %+v", out)
			}
			// realityRisk: admin only.
			if out := call("server/enable", admin, map[string]interface{}{"nodeId": 641}); out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			out := call("server/list", admin, map[string]interface{}{})
			if out.Code != 0 {
				t.Fatalf("list %+v", out)
			}
			for _, item := range out.Data.([]interface{}) {
				risk, ok := item.(map[string]interface{})["realityRisk"].(map[string]interface{})
				if !ok {
					t.Fatalf("admin view without realityRisk %v", item)
				}
				for _, k := range []string{"listeners", "guardState", "guardExposed", "cfDests"} {
					if _, ok := risk[k]; !ok {
						t.Fatalf("realityRisk misses %s: %v", k, risk)
					}
				}
			}
		})
	}
}
