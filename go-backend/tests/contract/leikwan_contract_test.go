package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type leiKwanEnv struct {
	t           *testing.T
	repository  *repo.Repository
	fake        *testapi.Fake
	router      http.Handler
	admin, user string
	accountID   int64
}

func newLeiKwanEnv(t *testing.T) *leiKwanEnv {
	t.Helper()
	repository, err := repo.Open(filepath.Join(t.TempDir(), "leikwan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	fake := testapi.New()
	fake.SeedSix()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	h := handler.New(repository, "lk-contract", "test")
	h.SetLeiKwanClient(leikwan.NewClient(server.URL, nil))
	mux := http.NewServeMux()
	h.Register(mux)
	e := &leiKwanEnv{t: t, repository: repository, fake: fake, router: middleware.JWT(middleware.AuthOptions{JWTSecret: "lk-contract"})(mux)}
	e.admin, _ = auth.GenerateToken(1, "admin", 0, "lk-contract")
	e.user, _ = auth.GenerateToken(2, "user", 1, "lk-contract")
	for _, n := range []model.Node{{ID: 100, Name: "landing", Secret: "lk-node", ServerIP: "8.8.8.8", Port: "20000-30000", Status: 1}, {ID: 101, Name: "other", Secret: "lk-node-other", ServerIP: "1.1.1.1", Port: "20000-30000", Status: 1}, {ID: 102, Name: "private", Secret: "lk-node-private", ServerIP: "127.0.0.1", Port: "20000-30000", Status: 1}} {
		if err := repository.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	return e
}
func (e *leiKwanEnv) call(path, token string, body interface{}) (response.R, *httptest.ResponseRecorder) {
	e.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		e.t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/leikwan/"+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), e.fake.Token) {
		e.t.Fatal("API token leaked in response")
	}
	return out, w
}
func (e *leiKwanEnv) account() int64 {
	e.t.Helper()
	out, _ := e.call("account/save", e.admin, map[string]interface{}{"name": "利群测试", "serviceId": 39, "token": e.fake.Token})
	if out.Code != 0 {
		e.t.Fatalf("account save %+v", out)
	}
	id := int64(out.Data.(map[string]interface{})["id"].(float64))
	e.accountID = id
	return id
}
func TestLeiKwanEveryEndpointAdminOnlyContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	for _, path := range []string{"account/list", "account/save", "account/delete", "overview", "forward/detail", "pipe/takeover", "pipe/create", "pipe/retarget", "pipe/delete", "pipe/check"} {
		out, w := e.call(path, e.user, map[string]interface{}{})
		if out.Code != 403 || w.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d envelope=%+v", path, w.Code, out)
		}
	}
	n, _ := e.fake.Counts()
	if n != 0 {
		t.Fatal("unauthorized call reached LeiKwan")
	}
}
func TestLeiKwanAccountSecretCRUDContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	id := e.account()
	out, _ := e.call("account/list", e.admin, map[string]interface{}{})
	if out.Code != 0 || len(out.Data.([]interface{})) != 1 {
		t.Fatalf("list %+v", out)
	}
	out, _ = e.call("account/save", e.admin, map[string]interface{}{"id": id, "name": "renamed", "serviceId": 39})
	if out.Code != 0 {
		t.Fatalf("update %+v", out)
	}
	saved, err := e.repository.GetLeiKwanAccount(id)
	if err != nil || saved.Token != e.fake.Token {
		t.Fatal("blank token did not preserve secret")
	}
	out, _ = e.call("account/delete", e.admin, map[string]interface{}{"id": id})
	if out.Code != 0 {
		t.Fatal(out)
	}
	saved, _ = e.repository.GetLeiKwanAccount(id)
	if saved != nil {
		t.Fatal("account not deleted")
	}
	n, _ := e.fake.Counts()
	if n != 0 {
		t.Fatal("account CRUD touched upstream")
	}
}
func TestLeiKwanOverviewTakeoverRetargetConflictContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	id := e.account()
	for i := 0; i < 5; i++ {
		out, _ := e.call("overview", e.admin, map[string]interface{}{"accountId": id})
		if out.Code != 0 {
			t.Fatal(out)
		}
		data := out.Data.(map[string]interface{})
		if len(data["forwards"].([]interface{})) != 6 {
			t.Fatal("missing unmanaged forwards")
		}
	}
	n, w := e.fake.Counts()
	if n != 2 || w != 0 {
		t.Fatalf("overview cache requests=%d writes=%d", n, w)
	}
	body := map[string]interface{}{"accountId": id, "forwardId": 263, "landingNodeId": 100, "landingPort": 20015, "expectedTarget": map[string]interface{}{"host": "8.8.8.8", "port": 20015}, "etag": `"forward-263-r1"`, "confirmed": true}
	out, _ := e.call("pipe/takeover", e.admin, body)
	if out.Code != 0 {
		t.Fatal(out)
	}
	pipeID := int64(out.Data.(map[string]interface{})["id"].(float64))
	_, w = e.fake.Counts()
	if w != 0 {
		t.Fatal("same-target takeover rebuilt connection")
	}
	alias := model.LeiKwanAccount{Name: "alias", ServiceID: 39, Token: "rotated-token"}
	if err := e.repository.SaveLeiKwanAccount(&alias); err != nil {
		t.Fatal(err)
	}
	body["accountId"] = alias.ID
	body["landingNodeId"] = 101
	out, _ = e.call("pipe/takeover", e.admin, body)
	if out.Code == 0 {
		t.Fatal("duplicate alias takeover accepted")
	}
	_, w = e.fake.Counts()
	if w != 0 {
		t.Fatal("duplicate alias modified live forward")
	}
	e.fake.Mu.Lock()
	f := e.fake.Forwards[263]
	f.Target.Host = "9.9.9.9"
	f.Revision++
	e.fake.Forwards[263] = f
	e.fake.Mu.Unlock()
	change := map[string]interface{}{"pipeId": pipeID, "landingNodeId": 101, "landingPort": 24443, "expectedTarget": map[string]interface{}{"host": "8.8.8.8", "port": 20015}, "etag": `"forward-263-r1"`, "confirmed": true}
	out, _ = e.call("pipe/retarget", e.admin, change)
	if out.Code != 412 {
		t.Fatalf("stale confirmation %+v", out)
	}
	change["etag"] = `"forward-263-r2"`
	change["expectedTarget"] = map[string]interface{}{"host": "9.9.9.9", "port": 20015}
	out, _ = e.call("pipe/retarget", e.admin, change)
	if out.Code != 0 {
		t.Fatalf("confirmed external change %+v", out)
	}
	p, _ := e.repository.GetLeiKwanPipe(pipeID)
	if p.TargetHost != "1.1.1.1" || p.LandingPort != 24443 {
		t.Fatalf("pipe target %+v", p)
	}
	out, _ = e.call("account/delete", e.admin, map[string]interface{}{"id": id})
	if out.Code == 0 {
		t.Fatal("account with pipe deleted")
	}
}
func TestLeiKwanCreateRetryPersistsIdentityContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	id := e.account()
	e.fake.FailCreateAfterSave = true
	body := map[string]interface{}{"accountId": id, "routeId": 21, "transport": "tcp", "landingNodeId": 100, "landingPort": 24443, "requestId": "persist-create-request"}
	out, _ := e.call("pipe/create", e.admin, body)
	if out.Code == 0 {
		t.Fatal("expected ambiguous upstream failure")
	}
	attempt, err := e.repository.GetLeiKwanCreate(id, "persist-create-request")
	if err != nil || attempt == nil || attempt.Payload == "" {
		t.Fatal("retry identity was not persisted before request")
	}
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 7
	e.fake.Mu.Unlock()
	out, _ = e.call("pipe/create", e.admin, body)
	if out.Code != 0 {
		t.Fatalf("retry after quota filled: %+v", out)
	}
	pipeID := out.Data.(map[string]interface{})["id"].(float64)
	out, _ = e.call("pipe/create", e.admin, body)
	if out.Code != 0 || out.Data.(map[string]interface{})["id"].(float64) != pipeID {
		t.Fatal("retry did not return original pipe")
	}
	if e.fake.Stored != 7 {
		t.Fatalf("created duplicate, stored=%d", e.fake.Stored)
	}
	body["landingPort"] = 24444
	out, _ = e.call("pipe/create", e.admin, body)
	if out.Code == 0 {
		t.Fatal("changed idempotent payload accepted")
	}
	body["requestId"] = "new-private-target"
	body["landingNodeId"] = 102
	out, _ = e.call("pipe/create", e.admin, body)
	if out.Code == 0 {
		t.Fatal("private target accepted")
	}
}
