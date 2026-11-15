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
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// TestSubAdminViewContract: plan 260 「全部用户」 on SQLite and PostgreSQL —
// admin OK through the full router, a normal user gets 403 from the JWT
// middleware, and neither response carries a token, source URL / hint,
// credential, server address or port.
func TestSubAdminViewContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			hy2 := linechain.Upstream{Type: "hysteria2", IPVersion: "system", Server: "hk9.provider.example", Port: 8443, Password: "airport-secret",
				Security: "tls", TLS: &linechain.TLS{ServerName: "hk9.provider.example"}}
			cached, _ := json.Marshal([]sublink.SourceNode{{Name: "🇭🇰 香港 09", Up: hy2}})
			socks := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home9.example.net", Port: 1080, Username: "u", Password: "socks-secret"}
			routing := `[{"type":"custom","name":"公司","lines":["example.com"],"exit":"node:e:51"},{"type":"builtin","key":"ai","exit":"select"}]`
			rows := []interface{}{
				&model.User{ID: 851, User: "admin-view-a", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.User{ID: 852, User: "admin-view-b", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.LandingExternalNode{ID: 51, UserID: 851, Name: "家宽", Protocol: "socks", Host: socks.Server, Port: socks.Port, Summary: socks.Summary(), Upstream: socks.JSON(), UpstreamHash: socks.Hash()},
				&model.UserSubSource{ID: 61, UserID: 851, Name: "机场Z", URLEnc: "sealed", URLHint: "hint.provider.example/…", LastOkAt: now, LastFetchAt: now, CachedNodes: string(cached), NodeCount: 1},
				&model.UserSubscription{ID: 71, UserID: 851, Name: "日常", Token: "TokAdminViewA1234567890", Format: "auto", RuleMode: "cn_direct",
					Items: `[{"kind":"external_node","externalId":51},{"kind":"source_all","sourceId":61}]`, Routing: &routing, LastPullAt: now - 3600_000, LastPullClient: "mihomo", LastPullNodes: 2, CreatedTime: now},
				&model.UserSubscription{ID: 72, UserID: 852, Name: "B", Token: "TokAdminViewB1234567890", Format: "clash", RuleMode: "global", Items: `[]`, CreatedTime: now},
			}
			for _, row := range rows {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			h := handler.New(r, subSecret, "test")
			router := httpserver.NewRouter(h, subSecret, r)
			user, _ := auth.GenerateToken(851, "admin-view-a", 1, subSecret)
			admin, _ := auth.GenerateToken(1, "admin", 0, subSecret)
			call := func(path, token string, body interface{}) (response.R, string) {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out, w.Body.String()
			}
			secrets := []string{"TokAdminViewA", "TokAdminViewB", "sealed", "hint.provider.example", "urlHint", "airport-secret", "socks-secret",
				"hk9.provider.example", "home9.example.net", ":8443", ":1080", "\"port\"", "node:", "\"token\""}
			clean := func(what, body string) {
				t.Helper()
				for _, s := range secrets {
					if strings.Contains(body, s) {
						t.Fatalf("%s leaks %q: %s", what, s, body)
					}
				}
			}

			for _, path := range []string{"sub/admin/list", "sub/admin/detail"} {
				if out, _ := call(path, user, map[string]interface{}{"id": 71}); out.Code != 403 {
					t.Fatalf("%s as user: %+v", path, out)
				}
			}
			out, body := call("sub/admin/list", admin, map[string]interface{}{})
			if out.Code != 0 {
				t.Fatalf("admin list %+v", out)
			}
			clean("list", body)
			var list struct {
				Enabled bool `json:"enabled"`
				Users   []struct {
					Name string `json:"name"`
					Subs []struct {
						Nodes   int    `json:"nodes"`
						Routing string `json:"routing"`
					} `json:"subs"`
					Sources []struct {
						Nodes int `json:"nodes"`
					} `json:"sources"`
				} `json:"users"`
			}
			b, _ := json.Marshal(out.Data)
			_ = json.Unmarshal(b, &list)
			if !list.Enabled || len(list.Users) != 2 || list.Users[0].Name != "admin-view-a" || list.Users[1].Name != "admin-view-b" {
				t.Fatalf("users %s", b)
			}
			if u := list.Users[0]; len(u.Subs) != 1 || u.Subs[0].Nodes != 2 || u.Subs[0].Routing != "custom" || len(u.Sources) != 1 || u.Sources[0].Nodes != 1 {
				t.Fatalf("user a %s", b)
			}
			if u := list.Users[1]; len(u.Subs) != 1 || u.Subs[0].Routing != "none" {
				t.Fatalf("user b %s", b)
			}

			out, body = call("sub/admin/detail", admin, map[string]interface{}{"id": 71})
			if out.Code != 0 {
				t.Fatalf("admin detail %+v", out)
			}
			clean("detail", body)
			for _, want := range []string{`"user":"admin-view-a"`, `"exitName":"家宽"`, `"sourceName":"机场Z"`, `"group":"source_all"`, `"example.com"`} {
				if !strings.Contains(body, want) {
					t.Fatalf("detail misses %s: %s", want, body)
				}
			}
			// The fresh source was not fetched: its row is unchanged.
			var src model.UserSubSource
			r.DB().First(&src, 61)
			if src.LastFetchAt != now || src.LastError != "" {
				t.Fatalf("source touched %+v", src)
			}
		})
	}
}
