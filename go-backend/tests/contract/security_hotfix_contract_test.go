package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 135 security hotfix: passwords (MD5 -> bcrypt), login rate limits,
// tokens of disabled/deleted users, anonymous vite_config exposure.

const secHotfixSecret = "sec-hotfix-jwt"

type secEnv struct {
	r      *repo.Repository
	router http.Handler
	admin  string
}

func newSecEnv(t *testing.T) *secEnv {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	h := handler.New(r, secHotfixSecret, "test")
	router := httpserver.NewRouter(h, secHotfixSecret, r)
	admin, _ := auth.GenerateToken(1, "admin_user", 0, secHotfixSecret)
	return &secEnv{r: r, router: router, admin: admin}
}

func (e *secEnv) addUser(t *testing.T, id int64, name, pwdHash string, status int) string {
	t.Helper()
	now := time.Now().UnixMilli()
	u := model.User{ID: id, User: name, Pwd: pwdHash, RoleID: 1, ExpTime: now + 86400000*30, Flow: 100, FlowResetTime: 1, Num: 10, CreatedTime: now, Status: status}
	if err := e.r.DB().Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	tok, _ := auth.GenerateToken(id, name, 1, secHotfixSecret)
	return tok
}

type apiResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (e *secEnv) call(t *testing.T, method, path, token string, body interface{}, hdr map[string]string) apiResp {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	// As seen behind the panel's nginx container (X-Real-IP = client).
	req.RemoteAddr = "172.18.0.5:40000"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	e.router.ServeHTTP(res, req)
	var out apiResp
	if method == http.MethodGet && !strings.HasPrefix(strings.TrimSpace(res.Body.String()), "{") {
		// sub_store answers the subscription header line as plain text.
		return apiResp{Code: 0, Msg: res.Body.String()}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: bad json %q", method, path, res.Body.String())
	}
	return out
}

func (e *secEnv) login(t *testing.T, user, pwd, ip string) apiResp {
	return e.call(t, http.MethodPost, "/api/v1/user/login", "", map[string]string{"username": user, "password": pwd}, map[string]string{"X-Real-IP": ip})
}

func (e *secEnv) storedHash(t *testing.T, id int64) string {
	t.Helper()
	var u model.User
	if err := e.r.DB().Select("pwd").Where("id = ?", id).First(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u.Pwd
}

func TestLegacyMD5LoginUpgradesToBcrypt(t *testing.T) {
	e := newSecEnv(t)
	e.addUser(t, 10, "alice", security.MD5("pw-alice"), 1)

	if res := e.login(t, "alice", "wrong", "203.0.113.1"); res.Code == 0 || res.Msg != "账号或密码错误" {
		t.Fatalf("wrong password: %+v", res)
	}
	if e.storedHash(t, 10) != security.MD5("pw-alice") {
		t.Fatal("hash changed after a failed login")
	}
	if res := e.login(t, "alice", "pw-alice", "203.0.113.1"); res.Code != 0 {
		t.Fatalf("legacy MD5 login: %+v", res)
	}
	upgraded := e.storedHash(t, 10)
	if !security.IsBcryptHash(upgraded) || !strings.HasPrefix(upgraded, "$2a$10$") {
		t.Fatalf("hash not upgraded to bcrypt cost 10: %q", upgraded)
	}
	if res := e.login(t, "alice", "pw-alice", "203.0.113.1"); res.Code != 0 {
		t.Fatalf("login after upgrade: %+v", res)
	}
	if res := e.login(t, "alice", "pw-alicE", "203.0.113.1"); res.Code == 0 {
		t.Fatal("bcrypt accepted a wrong password")
	}
	// Subscription URL (user/pwd query) uses the same verifier.
	if res := e.call(t, http.MethodGet, "/api/v1/open_api/sub_store?user=alice&pwd=pw-alice", "", nil, nil); res.Msg == "鉴权失败" {
		t.Fatalf("subscription with bcrypt password: %+v", res)
	}
	if res := e.call(t, http.MethodGet, "/api/v1/open_api/sub_store?user=alice&pwd=nope", "", nil, nil); res.Msg != "鉴权失败" {
		t.Fatalf("subscription with wrong password: %+v", res)
	}
	// Seeded admin (MD5 of admin_user) still logs in and is upgraded.
	if res := e.login(t, "admin_user", "admin_user", "203.0.113.2"); res.Code != 0 {
		t.Fatalf("seed admin login: %+v", res)
	}
	if !security.IsBcryptHash(e.storedHash(t, 1)) {
		t.Fatal("admin hash not upgraded")
	}
}

func TestNewAndChangedPasswordsAreBcrypt(t *testing.T) {
	e := newSecEnv(t)
	if res := e.call(t, http.MethodPost, "/api/v1/user/create", e.admin, map[string]interface{}{"user": "bob", "pwd": "pw-bob", "status": 1}, nil); res.Code != 0 {
		t.Fatalf("create: %+v", res)
	}
	var bob model.User
	e.r.DB().Where(`"user" = ?`, "bob").First(&bob)
	if !security.IsBcryptHash(bob.Pwd) {
		t.Fatalf("created user hash %q", bob.Pwd)
	}
	if res := e.login(t, "bob", "pw-bob", "203.0.113.3"); res.Code != 0 {
		t.Fatalf("login created user: %+v", res)
	}
	// Admin reset with a long (>72 byte) password.
	long := strings.Repeat("长密码", 30)
	if res := e.call(t, http.MethodPost, "/api/v1/user/update", e.admin, map[string]interface{}{"id": bob.ID, "user": "bob", "pwd": long, "status": 1}, nil); res.Code != 0 {
		t.Fatalf("update: %+v", res)
	}
	if !security.IsBcryptHash(e.storedHash(t, bob.ID)) {
		t.Fatal("admin reset not bcrypt")
	}
	if res := e.login(t, "bob", long, "203.0.113.3"); res.Code != 0 {
		t.Fatalf("long password login: %+v", res)
	}
	if res := e.login(t, "bob", long[:len(long)-3], "203.0.113.3"); res.Code == 0 {
		t.Fatal("prefix of a long password accepted")
	}
	// Own password change: legacy current password, new one bcrypt.
	tok := e.addUser(t, 20, "carol", security.MD5("old-pw"), 1)
	res := e.call(t, http.MethodPost, "/api/v1/user/updatePassword", tok, map[string]string{
		"newUsername": "carol", "currentPassword": "old-pw", "newPassword": "new-pw", "confirmPassword": "new-pw",
	}, nil)
	if res.Code != 0 {
		t.Fatalf("change password: %+v", res)
	}
	if !security.IsBcryptHash(e.storedHash(t, 20)) {
		t.Fatal("changed password not bcrypt")
	}
	if res := e.login(t, "carol", "new-pw", "203.0.113.4"); res.Code != 0 {
		t.Fatalf("login with changed password: %+v", res)
	}
	// Registration path.
	e.r.DB().Model(&model.ViteConfig{}).Where("name = ?", "registration_enabled").Update("value", "true")
	if res := e.call(t, http.MethodPost, "/api/v1/user/register", "", map[string]string{"user": "dave", "password": "pw-dave"}, nil); res.Code != 0 {
		t.Logf("register not available in this config: %+v", res)
	} else {
		var dave model.User
		e.r.DB().Where(`"user" = ?`, "dave").First(&dave)
		if !security.IsBcryptHash(dave.Pwd) {
			t.Fatalf("registered user hash %q", dave.Pwd)
		}
	}
}

func TestLoginRateLimits(t *testing.T) {
	e := newSecEnv(t)
	e.addUser(t, 30, "erin", security.MD5("pw-erin"), 1)

	// Per account: 10 failures, then refused even with the right password
	// from a fresh address.
	for i := 0; i < 10; i++ {
		if res := e.login(t, "erin", "bad", fmt.Sprintf("198.51.100.%d", i+1)); res.Code == 429 {
			t.Fatalf("refused too early at %d", i)
		}
	}
	if res := e.login(t, "erin", "pw-erin", "198.51.100.200"); res.Code != 429 || res.Msg != "登录尝试过于频繁，请稍后再试" {
		t.Fatalf("per-account limit: %+v", res)
	}
	// Successful logins do not use up the budget.
	e.addUser(t, 31, "frank", security.MD5("pw-frank"), 1)
	for i := 0; i < 25; i++ {
		if res := e.login(t, "frank", "pw-frank", "192.0.2.10"); res.Code != 0 {
			t.Fatalf("successful login %d refused: %+v", i, res)
		}
	}
	// Per address: 20 failures across many accounts, then refused.
	for i := 0; i < 20; i++ {
		if res := e.login(t, fmt.Sprintf("ghost%d", i), "x", "192.0.2.77"); res.Code == 429 {
			t.Fatalf("address refused too early at %d", i)
		}
	}
	if res := e.login(t, "frank", "pw-frank", "192.0.2.77"); res.Code != 429 {
		t.Fatalf("per-address limit: %+v", res)
	}
	if res := e.login(t, "frank", "pw-frank", "192.0.2.78"); res.Code != 0 {
		t.Fatalf("other address blocked: %+v", res)
	}
}

func TestDisabledAndDeletedUserTokensRejected(t *testing.T) {
	e := newSecEnv(t)
	tok := e.addUser(t, 40, "gina", security.MD5("pw"), 1)
	if res := e.call(t, http.MethodPost, "/api/v1/user/package", tok, map[string]string{}, nil); res.Code == 401 {
		t.Fatalf("active user rejected: %+v", res)
	}
	// Admin disables the user: the very next read is refused (cache invalidated).
	if res := e.call(t, http.MethodPost, "/api/v1/user/update", e.admin, map[string]interface{}{"id": 40, "user": "gina", "status": 0}, nil); res.Code != 0 {
		t.Fatalf("disable: %+v", res)
	}
	for _, path := range []string{"/api/v1/user/package", "/api/v1/forward/list", "/api/v1/landing/node/list"} {
		if res := e.call(t, http.MethodPost, path, tok, map[string]string{}, nil); res.Code != 401 || res.Msg != "无效的token或token已过期" {
			t.Fatalf("disabled user read %s: %+v", path, res)
		}
	}
	// Re-enabled: token works again.
	if res := e.call(t, http.MethodPost, "/api/v1/user/update", e.admin, map[string]interface{}{"id": 40, "user": "gina", "status": 1}, nil); res.Code != 0 {
		t.Fatalf("enable: %+v", res)
	}
	if res := e.call(t, http.MethodPost, "/api/v1/user/package", tok, map[string]string{}, nil); res.Code == 401 {
		t.Fatalf("re-enabled user rejected: %+v", res)
	}
	// Deleted: refused.
	if res := e.call(t, http.MethodPost, "/api/v1/user/delete", e.admin, map[string]interface{}{"id": 40}, nil); res.Code != 0 {
		t.Fatalf("delete: %+v", res)
	}
	if res := e.call(t, http.MethodPost, "/api/v1/user/package", tok, map[string]string{}, nil); res.Code != 401 {
		t.Fatalf("deleted user read: %+v", res)
	}
	// A token for a user id that never existed.
	ghost, _ := auth.GenerateToken(999, "ghost", 1, secHotfixSecret)
	if res := e.call(t, http.MethodPost, "/api/v1/user/package", ghost, map[string]string{}, nil); res.Code != 401 {
		t.Fatalf("unknown user read: %+v", res)
	}
}

func TestConfigReadsLimitedForAnonymousAndUsers(t *testing.T) {
	e := newSecEnv(t)
	for name, value := range map[string]string{
		"ip": "panel-backend.example:443", "forward_group_order_map": `{"1":{"a":1}}`,
		"forward_compact_mode": "true", "cloudflare_secret_key": "turnstile-secret",
	} {
		if err := e.r.DB().Create(&model.ViteConfig{Name: name, Value: value, Time: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	userTok := e.addUser(t, 50, "hank", security.MD5("pw"), 1)
	keys := func(token string) map[string]string {
		res := e.call(t, http.MethodPost, "/api/v1/config/list", token, map[string]string{}, nil)
		if res.Code != 0 {
			t.Fatalf("config/list: %+v", res)
		}
		var m map[string]string
		_ = json.Unmarshal(res.Data, &m)
		return m
	}
	anon := keys("")
	if _, ok := anon["app_name"]; !ok {
		t.Fatalf("anonymous lost a login-page key: %v", anon)
	}
	for _, secret := range []string{"ip", "forward_group_order_map", "cloudflare_secret_key", "forward_compact_mode"} {
		if _, ok := anon[secret]; ok {
			t.Fatalf("anonymous config/list exposes %s", secret)
		}
	}
	user := keys(userTok)
	if user["forward_compact_mode"] != "true" {
		t.Fatalf("user lost forward_compact_mode: %v", user)
	}
	if _, ok := user["ip"]; ok {
		t.Fatal("normal user sees admin config")
	}
	if admin := keys(e.admin); admin["ip"] != "panel-backend.example:443" || admin["cloudflare_secret_key"] == "" {
		t.Fatalf("admin view changed: %v", admin)
	}
	get := func(token, name string) apiResp {
		return e.call(t, http.MethodPost, "/api/v1/config/get", token, map[string]string{"name": name}, nil)
	}
	if res := get("", "cloudflare_secret_key"); res.Code == 0 {
		t.Fatalf("anonymous config/get secret: %+v", res)
	}
	if res := get("", "registration_enabled"); res.Code != 0 {
		t.Fatalf("anonymous config/get public key: %+v", res)
	}
	if res := get(userTok, "ip"); res.Code == 0 {
		t.Fatalf("user config/get admin key: %+v", res)
	}
	if res := get(e.admin, "ip"); res.Code != 0 {
		t.Fatalf("admin config/get: %+v", res)
	}
}
