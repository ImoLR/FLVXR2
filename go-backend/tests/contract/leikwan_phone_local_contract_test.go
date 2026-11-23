package contract_test

// Plan 297 (im.gamer App = web + 手机本地):
//   - an admin is never held to one entry by 「只开主入口」 (normal users are);
//   - a subscription follows the entry it was made / updated under
//     (user_subscription.entry_mode): main (default) = unchanged output,
//     local = the 利群 nodes on 127.0.0.1 + a derived 「手机本地」 client title;
//   - 手机本地 (link / sub / preview / rule list) is admin-only.

import (
	"encoding/base64"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestLeiKwanPhoneLocalContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanPhoneLocalFlow(t, dialect) })
	}
}

// pullTitle: body + the decoded profile-title of one subscription pull.
func (e *lkLineEnv) pullTitle(token string) (string, string) {
	e.t.Helper()
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	req.Header.Set("User-Agent", "clash-verge/v2.0.0")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != 200 {
		e.t.Fatalf("pull %s: %d %s", token, w.Code, w.Body.String())
	}
	b, _ := io.ReadAll(w.Body)
	raw := strings.TrimPrefix(w.Header().Get("profile-title"), "base64:")
	title, _ := base64.StdEncoding.DecodeString(raw)
	return string(b), string(title)
}

func subEntryOf(e *lkLineEnv, token string, id int64) string {
	e.t.Helper()
	for _, it := range e.ok("/api/v1/sub/list", token, map[string]interface{}{})["subs"].([]interface{}) {
		m := it.(map[string]interface{})
		if num(m["id"]) == id {
			return m["entryMode"].(string)
		}
	}
	e.t.Fatalf("sub %d not listed", id)
	return ""
}

func leiKwanPhoneLocalFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.must(e.r.DB().Model(&model.LandingNode{}).Where("id IN ?", []int64{501, 502}).Updates(map[string]interface{}{"server_key": "MTIzNDU2Nzg5MDEyMzQ1Ng==", "user_key": "YWJjZGVmZ2hpamtsbW5vcA=="}).Error)
	// The admin's own panel landing (a copy of alice's, owned by imolr).
	var l501 model.LandingNode
	e.must(e.r.DB().First(&l501, 501).Error)
	adm := l501
	adm.ID, adm.UserID, adm.Name, adm.Port, adm.InboundTag = 599, 1, "imolr-99u", 30099, "a99"
	e.must(e.r.DB().Create(&adm).Error)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	lineID := num(e.lineByName("利群·广港")["id"])
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": false})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	ra := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-pl-a"})["rule"].(map[string]interface{})
	rm := e.ok("/api/v1/leikwan-rule/create", e.admin, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 599, "requestId": "req-pl-m"})["rule"].(map[string]interface{})
	var xa, xm model.LeiKwanRule
	e.must(e.r.DB().First(&xa, num(ra["id"])).Error)
	e.must(e.r.DB().First(&xm, num(rm["id"])).Error)
	if xm.UserID != 1 {
		t.Fatalf("admin rule owner: %+v", xm)
	}
	pa, pm := xa.ListenPort, xm.ListenPort
	var grant model.LeiKwanGrant
	e.must(e.r.DB().Where("user_id = ?", 1).First(&grant).Error)
	mainNode := model.Node{}
	e.must(e.r.DB().First(&mainNode, grant.MainEntryNodeID).Error)

	// --- 只开主入口: admin open everywhere, normal user still held ---
	global := map[string]interface{}{"holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5, "ioError": "record", "coalesce": "off", "entryLock": true}
	e.ok("/api/v1/leikwan/setting/save", e.admin, map[string]interface{}{"global": global})
	for _, n := range []int64{52, 58, 60, 61} {
		b := e.blocks(n)
		if b[pm] {
			t.Fatalf("admin rule blocked on entry %d by 只开主入口: %v", n, b)
		}
		if want := n != 60; b[pa] != want {
			t.Fatalf("alice on entry %d: blocked=%v, want %v", n, b[pa], want)
		}
	}
	owner := map[int64]interface{}{}
	for _, it := range e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{})["rules"].([]interface{}) {
		m := it.(map[string]interface{})
		owner[num(m["id"])] = m["ownerAdmin"]
	}
	if owner[xm.ID] != true || owner[xa.ID] != nil {
		t.Fatalf("ownerAdmin: admin %v alice %v", owner[xm.ID], owner[xa.ID])
	}

	// --- subscriptions: default main = unchanged ---
	s1 := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{})
	tok1, id1 := s1["token"].(string), num(s1["id"])
	if s1["entryMode"] != "main" || s1["name"] != "利群汇聚" || subEntryOf(e, e.admin, id1) != "main" {
		t.Fatalf("default sub: %v", s1)
	}
	var row model.UserSubscription
	e.must(e.r.DB().First(&row, id1).Error)
	if row.EntryMode != "main" {
		t.Fatalf("stored default entry_mode %q", row.EntryMode)
	}
	mainBody, mainTitle := e.pullTitle(tok1)
	if !strings.Contains(mainBody, mainNode.ServerIP) || strings.Contains(mainBody, "127.0.0.1") || mainTitle != "利群汇聚" {
		t.Fatalf("main sub (%s):\n%s", mainTitle, mainBody)
	}

	// 手机本地 is admin-only: users can't ask for it anywhere.
	if out, _ := e.call("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{"entry": "local"}); out.Code == 0 {
		t.Fatal("user got a 手机本地 subscription")
	}
	if out, _ := e.call("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"userId": 2, "entry": "local", "action": "create"}); out.Code == 0 {
		t.Fatal("admin made a 手机本地 sub for another user")
	}
	if out, _ := e.call("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": xa.ID, "local": true}); out.Code == 0 {
		t.Fatal("user got a 手机本地 link")
	}
	us := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})
	if out, _ := e.call("/api/v1/sub/update", e.user, map[string]interface{}{"id": num(us["id"]), "name": "x", "format": "auto", "ruleMode": "cn_direct",
		"items": []map[string]interface{}{{"kind": "leikwan_all"}}, "entryMode": "local"}); out.Code == 0 {
		t.Fatal("user switched a sub to 手机本地")
	}

	// App with 手机本地 and no local sub yet: the 利群汇聚 one comes back.
	if got := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"entry": "local"}); num(got["id"]) != id1 || got["entryMode"] != "main" {
		t.Fatalf("local entry, no local sub: %v", got)
	}
	// 新建 under 手机本地: a plain name, 127.0.0.1 nodes, derived title.
	s2 := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"entry": "local", "action": "create"})
	tok2, id2 := s2["token"].(string), num(s2["id"])
	if s2["entryMode"] != "local" || s2["name"] != "利群汇聚 2" || strings.Contains(s2["name"].(string), "手机本地") || len(s2["subs"].([]interface{})) != 2 {
		t.Fatalf("created local sub: %v", s2)
	}
	localBody, localTitle := e.pullTitle(tok2)
	if !strings.Contains(localBody, "127.0.0.1") || !strings.Contains(localBody, strconv.Itoa(pm)) || strings.Contains(localBody, mainNode.ServerIP) || localTitle != "利群汇聚 2 · 手机本地" {
		t.Fatalf("local sub (%s):\n%s", localTitle, localBody)
	}
	if strings.Contains(localBody, "8.134.172.154") || strings.Contains(localBody, strconv.Itoa(pa)) {
		t.Fatal("local sub carries another user's rule")
	}
	if strings.SplitN(localBody, "127.0.0.1", 2)[0] == "" {
		t.Fatal("empty local body")
	}
	// The main sub is untouched (same body).
	if b, title := e.pullTitle(tok1); b != mainBody || title != "利群汇聚" {
		t.Fatalf("main sub changed by the local one (%s):\n%s", title, b)
	}
	if subEntryOf(e, e.admin, id2) != "local" || subEntryOf(e, e.admin, id1) != "main" {
		t.Fatal("sub list entry modes")
	}
	// App with 手机本地 again: the newest local sub.
	if got := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"entry": "local"}); num(got["id"]) != id2 {
		t.Fatalf("local entry pick: %v", got)
	}
	// Browser (no entry): still the 利群汇聚 one.
	if got := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{}); num(got["id"]) != id1 {
		t.Fatalf("browser pick: %v", got)
	}

	// 改成跟主入口 (browser / server entry): badge + title gone, nodes back on
	// the 主入口; 改成手机本地 again adds them back.
	if got := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"id": id2, "action": "convert"}); got["entryMode"] != "main" {
		t.Fatalf("convert to main: %v", got)
	}
	if b, title := e.pullTitle(tok2); strings.Contains(b, "127.0.0.1") || !strings.Contains(b, mainNode.ServerIP) || title != "利群汇聚 2" {
		t.Fatalf("converted sub (%s):\n%s", title, b)
	}
	e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"id": id2, "entry": "local", "action": "convert"})
	if b, title := e.pullTitle(tok2); b != localBody || title != "利群汇聚 2 · 手机本地" {
		t.Fatalf("back to local (%s):\n%s", title, b)
	}
	// The 订阅 editor: entryMode on save (absent = kept).
	save := func(mode interface{}) {
		body := map[string]interface{}{"id": id2, "name": "手机用", "format": "auto", "ruleMode": "cn_direct", "items": []map[string]interface{}{{"kind": "leikwan_all"}}}
		if mode != nil {
			body["entryMode"] = mode
		}
		e.ok("/api/v1/sub/update", e.admin, body)
	}
	save(nil)
	if subEntryOf(e, e.admin, id2) != "local" {
		t.Fatal("editor save without entryMode changed the entry")
	}
	if _, title := e.pullTitle(tok2); title != "手机用 · 手机本地" {
		t.Fatalf("renamed local title %q", title)
	}
	save("main")
	if b, title := e.pullTitle(tok2); subEntryOf(e, e.admin, id2) != "main" || title != "手机用" || strings.Contains(b, "127.0.0.1") {
		t.Fatalf("editor → main (%s):\n%s", title, b)
	}
	if out, _ := e.call("/api/v1/sub/update", e.admin, map[string]interface{}{"id": id2, "name": "手机用", "format": "auto", "ruleMode": "cn_direct",
		"items": []map[string]interface{}{{"kind": "leikwan_all"}}, "entryMode": "phone"}); out.Code == 0 {
		t.Fatal("bad entryMode accepted")
	}
	save("local")
	if subEntryOf(e, e.admin, id2) != "local" {
		t.Fatal("editor → local")
	}

	// A 手机本地 pull does not end the admin's 换主入口 grace.
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 1, "mainEntryNodeId": 52, "backupLine": false})
	e.must(e.r.DB().Where("user_id = ?", 1).First(&grant).Error)
	if grant.PrevEntryNodeID == 0 {
		t.Fatal("no grace after the admin's 换主入口")
	}
	time1 := grant.EntryChangedAt
	e.pullTitle(tok2)
	e.must(e.r.DB().Where("user_id = ?", 1).First(&grant).Error)
	if grant.PrevEntryNodeID == 0 || grant.EntryChangedAt != time1 {
		t.Fatal("手机本地 pull ended the grace")
	}
	if b, _ := e.pullTitle(tok1); !strings.Contains(b, "101.96.213.164") {
		t.Fatalf("main sub after 换主入口:\n%s", b)
	}

	// --- link / preview / rule list (admin, App) ---
	l := e.ok("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": xm.ID, "local": true})
	if l["address"] != "127.0.0.1:"+strconv.Itoa(pm) || !strings.Contains(l["url"].(string), "@127.0.0.1:"+strconv.Itoa(pm)) || l["entryName"] != "手机本地" {
		t.Fatalf("local link: %v", l)
	}
	pv := e.ok("/api/v1/leikwan/grant/preview", e.admin, map[string]interface{}{"userId": 1, "local": true, "phone": map[string]interface{}{"warm": true, "app": "1.0.8-panel"}})
	if pv["local"] != true || len(pv["rules"].([]interface{})) == 0 || pv["to"].(map[string]interface{})["name"] != "手机本地" {
		t.Fatalf("local preview: %v", pv)
	}
	if out, _ := e.call("/api/v1/leikwan/grant/preview", e.user, map[string]interface{}{"userId": 2, "local": true}); out.Code == 0 {
		t.Fatal("user previewed 手机本地")
	}
	limits := map[int64]bool{}
	for _, it := range e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{"phone": map[string]interface{}{"warm": false}})["rules"].([]interface{}) {
		m := it.(map[string]interface{})
		_, has := m["phoneLimits"]
		limits[num(m["id"])] = has
	}
	if !limits[xm.ID] || limits[xa.ID] {
		t.Fatalf("phoneLimits only on the admin's own rules: %v", limits)
	}
	for _, it := range e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{"phone": map[string]interface{}{"warm": true}})["rules"].([]interface{}) {
		if _, has := it.(map[string]interface{})["phoneLimits"]; has {
			t.Fatal("user list carries phoneLimits")
		}
	}
}
