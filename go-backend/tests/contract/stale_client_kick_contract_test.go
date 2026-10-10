package contract_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/repo"
)

// /api/v1/license/info is only called by pre-fork.16 frontends. It must answer
// HTTP 401 + Clear-Site-Data "storage" through the full router (JWT middleware
// included) for any method and any token, so the old JS logs out and the
// browser drops the stale service worker.
func TestStaleFrontendKickFullRouter(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "kick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	router := httpserver.NewRouter(handler.New(r, "secret", "test"), "secret", r)

	adminToken, err := auth.GenerateToken(1, "admin", 0, "secret")
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := auth.GenerateToken(7, "user", 1, "secret")
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, err := auth.GenerateToken(1, "admin", 0, "other-secret")
	if err != nil {
		t.Fatal(err)
	}

	// Messages the old frontend's isTokenExpired() recognises.
	oldMessages := map[string]bool{
		"未登录或token已过期":      true,
		"无效的token或token已过期": true,
		"无法获取用户权限信息":        true,
	}

	cases := []struct {
		name   string
		method string
		header string
		cookie string
	}{
		{name: "post no token", method: http.MethodPost},
		{name: "get no token", method: http.MethodGet},
		{name: "post invalid token", method: http.MethodPost, header: "not-a-jwt"},
		{name: "post foreign-secret token", method: http.MethodPost, header: foreignToken},
		{name: "post valid admin token", method: http.MethodPost, header: adminToken},
		{name: "post valid user token", method: http.MethodPost, header: userToken},
		{name: "get valid user token", method: http.MethodGet, header: userToken},
		{name: "post session cookie", method: http.MethodPost, cookie: adminToken},
		{name: "put no token", method: http.MethodPut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/v1/license/info", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if got := w.Header().Values("Clear-Site-Data"); len(got) != 1 || got[0] != `"storage"` {
				t.Fatalf("Clear-Site-Data=%q", got)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control=%q", got)
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("Content-Type=%q", got)
			}
			var body struct {
				Code *int    `json:"code"`
				Msg  string  `json:"msg"`
				TS   int64   `json:"ts"`
				Data *string `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", w.Body.String(), err)
			}
			if body.Code == nil || *body.Code != 401 || !oldMessages[body.Msg] || body.TS <= 0 {
				t.Fatalf("envelope=%s", w.Body.String())
			}
		})
	}
}

// The kill switch logs the browser out, so the current frontend must never
// call it (only pre-fork.16 builds do).
func TestCurrentFrontendNeverCallsStaleKickEndpoint(t *testing.T) {
	src := filepath.Join("..", "..", "..", "vite-frontend", "src")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("frontend sources not available: %v", err)
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "license/info") {
			t.Errorf("%s references license/info", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
