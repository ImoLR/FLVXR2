package contract_test

// Plan 294 (im.gamer App): 复制链接 and the 利群汇聚 subscription follow the
// selected 入口机 — 手机本地 (127.0.0.1, same vendor port) or another front of
// the 「利群入口」 group (admins; users only their own 主入口), with
// 「只开主入口」 respected and the 换主入口 grace untouched by ?lk= pulls.

import (
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestLeiKwanEntryPickContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanEntryPickFlow(t, dialect) })
		t.Run(dialect+"/grace", func(t *testing.T) { leiKwanEntryPickGraceFlow(t, dialect) })
	}
}

func (e *lkLineEnv) pullURL(path string) string {
	e.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", "clash-verge/v2.0.0")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != 200 {
		e.t.Fatalf("pull %s: %d %s", path, w.Code, w.Body.String())
	}
	b, _ := io.ReadAll(w.Body)
	return string(b)
}

func leiKwanEntryPickFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.must(e.r.DB().Model(&model.LandingNode{}).Where("id IN ?", []int64{501, 502}).Updates(map[string]interface{}{"server_key": "MTIzNDU2Nzg5MDEyMzQ1Ng==", "user_key": "YWJjZGVmZ2hpamtsbW5vcA=="}).Error)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	lineID := num(e.lineByName("利群·广港")["id"])
	// alice: 主入口 阿里广州 (60, 8.134.172.154).
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": false})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	r1 := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-pick-1"})["rule"].(map[string]interface{})
	var x1 model.LeiKwanRule
	e.must(e.r.DB().First(&x1, num(r1["id"])).Error)
	port := strconv.Itoa(x1.ListenPort)

	// Default: unchanged (the owner's 主入口).
	if l := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID}); !strings.Contains(l["url"].(string), "@8.134.172.154:"+port) || l["entryName"] != "阿里广州" {
		t.Fatalf("default link: %v", l)
	}
	// 手机本地: 127.0.0.1, same port, same landing params.
	local := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID, "local": true})
	if !strings.Contains(local["url"].(string), "@127.0.0.1:"+port) || local["address"] != "127.0.0.1:"+port || local["entryName"] != "手机本地" {
		t.Fatalf("local link: %v", local)
	}
	if strings.SplitN(local["url"].(string), "@", 2)[0] != strings.SplitN(e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID})["url"].(string), "@", 2)[0] {
		t.Fatal("local link changed the landing credentials")
	}
	// Users: only their own 主入口 by id; another front is refused.
	if l := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID, "entryNodeId": 60}); !strings.Contains(l["url"].(string), "@8.134.172.154:") {
		t.Fatalf("own entry by id: %v", l)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID, "entryNodeId": 58}); out.Code == 0 {
		t.Fatal("user got another entry machine's address")
	}
	// Admins: any node of the 利群入口 group, nothing else.
	if l := e.ok("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": x1.ID, "entryNodeId": 58}); !strings.Contains(l["url"].(string), "@106.13.17.23:"+port) || l["entryName"] != "百度云广州" {
		t.Fatalf("admin pick 58: %v", l)
	}
	for _, bad := range []int64{2, 47, 9999} {
		if out, _ := e.call("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": x1.ID, "entryNodeId": bad}); out.Code == 0 {
			t.Fatalf("admin picked node %d outside the 利群入口 group", bad)
		}
	}

	// Subscription: ?lk=local → 127.0.0.1; plain → 主入口; a user's ?lk=<other
	// node> is ignored (users never get other entry addresses).
	sub := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})
	token := sub["token"].(string)
	if b := e.pull(token); !strings.Contains(b, "8.134.172.154") || strings.Contains(b, "127.0.0.1") {
		t.Fatalf("plain sub:\n%s", b)
	}
	if b := e.pullURL("/s/" + token + "?lk=local"); !strings.Contains(b, "127.0.0.1") || !strings.Contains(b, port) || strings.Contains(b, "8.134.172.154") {
		t.Fatalf("?lk=local sub:\n%s", b)
	}
	if b := e.pullURL("/s/" + token + "?lk=58"); strings.Contains(b, "106.13.17.23") || !strings.Contains(b, "8.134.172.154") {
		t.Fatalf("user ?lk=58 not ignored:\n%s", b)
	}
	// The render cache keeps the variants apart.
	if b := e.pull(token); strings.Contains(b, "127.0.0.1") {
		t.Fatal("cached ?lk=local body served for the plain link")
	}

	// 「只开主入口」: another front refuses the rule, 手机本地 never does.
	global := map[string]interface{}{"holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5, "ioError": "record", "coalesce": "off", "entryLock": true}
	e.ok("/api/v1/leikwan/setting/save", e.admin, map[string]interface{}{"global": global})
	if out, _ := e.call("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": x1.ID, "entryNodeId": 58}); out.Code == 0 || !strings.Contains(out.Msg, "只开主入口") {
		t.Fatalf("locked entry link: %+v", out)
	}
	if l := e.ok("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": x1.ID, "local": true}); !strings.Contains(l["url"].(string), "@127.0.0.1:") {
		t.Fatalf("local link under lock: %v", l)
	}

}

// A fresh env (own pull throttle): a ?lk=local pull keeps a 换主入口 grace, a
// plain pull ends it.
func leiKwanEntryPickGraceFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.must(e.r.DB().Model(&model.LandingNode{}).Where("id IN ?", []int64{501, 502}).Updates(map[string]interface{}{"server_key": "MTIzNDU2Nzg5MDEyMzQ1Ng==", "user_key": "YWJjZGVmZ2hpamtsbW5vcA=="}).Error)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	lineID := num(e.lineByName("利群·广港")["id"])
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": false})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-pick-g"})
	global := map[string]interface{}{"holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5, "ioError": "record", "coalesce": "off", "entryLock": true}
	e.ok("/api/v1/leikwan/setting/save", e.admin, map[string]interface{}{"global": global})
	token := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})["token"].(string)
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": false})
	if aliceGrace(e) == nil {
		t.Fatal("no grace after 换主入口")
	}
	if b := e.pullURL("/s/" + token + "?lk=local"); !strings.Contains(b, "127.0.0.1") {
		t.Fatalf("?lk=local sub:\n%s", b)
	}
	if aliceGrace(e) == nil {
		t.Fatal("?lk=local pull ended the 换主入口 grace")
	}
	if b := e.pull(token); !strings.Contains(b, "101.96.213.164") {
		t.Fatalf("plain sub after 换主入口:\n%s", b)
	}
	if aliceGrace(e) != nil {
		t.Fatal("plain pull did not end the grace")
	}
}
