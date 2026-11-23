package contract_test

// Plan 272: 「只开主入口」 (per-host block, 换主入口 grace, exemption),
// 利群 rules landing on a 外部节点, 「生成订阅」 (the user's 利群汇聚
// subscription), 利群 refs in subscriptions and 利群 entries in the landing
// page links — SQLite and PostgreSQL.

import (
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

func TestLeiKwanEntryLockSubContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanEntryLockSubFlow(t, dialect) })
	}
}

func (e *lkLineEnv) blocks(node int64) map[int]bool {
	e.t.Helper()
	ports, err := e.h.LeiKwanHostBlockPorts(node)
	e.must(err)
	out := map[int]bool{}
	for _, p := range ports {
		out[p] = true
	}
	return out
}

func (e *lkLineEnv) pull(token string) string {
	e.t.Helper()
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	req.Header.Set("User-Agent", "clash-verge/v2.0.0")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != 200 {
		e.t.Fatalf("pull %s: %d %s", token, w.Code, w.Body.String())
	}
	b, _ := io.ReadAll(w.Body)
	return string(b)
}

func leiKwanEntryLockSubFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	// Real-length SS2022 keys (the shared fixture's are too short for a
	// parseable subscription node).
	e.must(e.r.DB().Model(&model.LandingNode{}).Where("id IN ?", []int64{501, 502}).Updates(map[string]interface{}{"server_key": "MTIzNDU2Nzg5MDEyMzQ1Ng==", "user_key": "YWJjZGVmZ2hpamtsbW5vcA=="}).Error)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	gg := e.lineByName("利群·广港")
	lineID := num(gg["id"])
	e.ok("/api/v1/leikwan/line/save", e.admin, map[string]interface{}{"id": lineID, "name": "利群·广港", "routeId": 21, "transport": "tcp+udp", "backupTunnelId": 105, "accel": gg["accel"]})
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()

	// alice's 外部节点 (Trojan on a domain) and bob's.
	// (An IP: the automatic 备线 resolves domains, the lab has no DNS; the
	// domain → SNI case is covered by TestLeiKwanRehost.)
	ext := model.LandingExternalNode{UserID: 2, Name: "我的日本", Protocol: "trojan", Host: "203.0.113.50", Port: 8443}
	up := linechain.Upstream{Type: "trojan", Server: "203.0.113.50", Port: 8443, Password: "pw-trojan", Security: "tls", TLS: &linechain.TLS{ServerName: "jp.example.com"}}
	ext.Upstream, ext.UpstreamHash, ext.Summary = up.JSON(), up.Hash(), up.Summary()
	e.must(e.r.DB().Create(&ext).Error)
	bobExt := model.LandingExternalNode{UserID: 3, Name: "bob", Protocol: "trojan", Host: "bob.example.com", Port: 443, Upstream: up.JSON(), UpstreamHash: "bob"}
	e.must(e.r.DB().Create(&bobExt).Error)

	// --- item 5: 外部节点 as 利群 landing ---
	opts := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})
	exts := opts["externals"].([]interface{})
	if len(exts) != 1 || exts[0].(map[string]interface{})["supported"] != true || exts[0].(map[string]interface{})["protocol"] != "Trojan" {
		t.Fatalf("options externals: %v", exts)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "x", "externalNodeId": bobExt.ID, "requestId": "req-alice-bob"}); out.Code == 0 {
		t.Fatal("foreign 外部节点 accepted")
	}
	r1 := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-alice-1"})["rule"].(map[string]interface{})
	r2 := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "广港到日本", "externalNodeId": ext.ID, "requestId": "req-alice-2"})["rule"].(map[string]interface{})
	if r2["landing"].(map[string]interface{})["kind"] != "external" || r2["landing"].(map[string]interface{})["name"] != "我的日本" || r2["linkKind"] != "full" || r2["allEntries"] != nil {
		t.Fatalf("external rule view: %v", r2)
	}
	var x1, x2 model.LeiKwanRule
	e.must(e.r.DB().First(&x1, num(r1["id"])).Error)
	e.must(e.r.DB().First(&x2, num(r2["id"])).Error)
	e.fake.Mu.Lock()
	tgt := e.fake.Forwards[x2.ForwardID].Target
	e.fake.Mu.Unlock()
	var bk model.Forward
	if err := e.r.DB().First(&bk, x2.BackupForwardID).Error; err != nil {
		t.Fatalf("external 备线 missing: %+v / r1 %+v", x2, x1)
	}
	if tgt.Host != "203.0.113.50" || tgt.Port != 8443 || x2.ExternalNodeID != ext.ID || bk.RemoteAddr != "203.0.113.50:8443" {
		t.Fatalf("external target / 备线: %+v %+v %s", tgt, x2, bk.RemoteAddr)
	}
	link := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x2.ID})
	url := link["url"].(string)
	port2 := strconv.Itoa(x2.ListenPort)
	if !strings.HasPrefix(url, "trojan://pw-trojan@8.134.172.154:"+port2) || !strings.Contains(url, "sni=jp.example.com") {
		t.Fatalf("external link (entry + SNI kept): %s", url)
	}
	// Users keep node links (plan 272 item 6 final): the panel landing too.
	if l := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": x1.ID}); !strings.Contains(l["url"].(string), "@8.134.172.154:"+strconv.Itoa(x1.ListenPort)) {
		t.Fatalf("user node link: %v", l)
	}
	// 换落地 back to a panel landing clears the external id.
	e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": x2.ID, "changeTarget": true, "landingNodeId": 502})
	e.must(e.r.DB().First(&x2, x2.ID).Error)
	if x2.ExternalNodeID != 0 || x2.LandingNodeID != 502 {
		t.Fatalf("换落地 to panel: %+v", x2)
	}
	e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": x2.ID, "changeTarget": true, "externalNodeId": ext.ID})
	e.must(e.r.DB().First(&x2, x2.ID).Error)
	if x2.ExternalNodeID != ext.ID || x2.LandingNodeID != 0 || x2.TargetHost != "203.0.113.50" {
		t.Fatalf("换落地 to 外部节点: %+v", x2)
	}

	// --- item 3: 只开主入口 ---
	var r263 model.LeiKwanRule
	e.must(e.r.DB().Where("forward_id = ?", 263).First(&r263).Error)
	p1, p2, p263 := x1.ListenPort, x2.ListenPort, r263.ListenPort
	if b := e.blocks(58); b[p1] || b[p2] || b[p263] {
		t.Fatalf("lock off blocks: %v", b)
	}
	global := map[string]interface{}{"holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5, "ioError": "record", "coalesce": "off", "entryLock": true}
	if out, _ := e.call("/api/v1/leikwan/setting/save", e.user, map[string]interface{}{"global": global}); out.Code == 0 {
		t.Fatal("user changed 利群 settings")
	}
	e.ok("/api/v1/leikwan/setting/save", e.admin, map[string]interface{}{"global": global})
	if gl := e.ok("/api/v1/leikwan/grant/list", e.admin, map[string]interface{}{}); gl["entryLock"] != true {
		t.Fatalf("grant list entryLock: %v", gl["entryLock"])
	}
	// alice: 主入口 阿里广州 (60) open, every other entry blocked; admin's
	// imported rules (主入口 百度云广州 58): plan 297 — an admin is never held
	// to one entry, open on every entry host.
	if b := e.blocks(60); b[p1] || b[p2] || b[p263] {
		t.Fatalf("阿里广州 blocks: %v", b)
	}
	for _, n := range []int64{52, 58, 61} {
		if b := e.blocks(n); !b[p1] || !b[p2] {
			t.Fatalf("node %d must block alice: %v", n, b)
		}
	}
	for _, n := range []int64{52, 58, 60, 61} {
		if b := e.blocks(n); b[p263] {
			t.Fatalf("admin rule blocked on %d: %v", n, b)
		}
	}
	// Exemption 「所有利群入口都可用」 (admin only) on alice's rule.
	if out, _ := e.call("/api/v1/leikwan/rule/all-entries", e.user, map[string]interface{}{"id": x2.ID, "on": true}); out.Code == 0 {
		t.Fatal("user set the exemption")
	}
	e.ok("/api/v1/leikwan/rule/all-entries", e.admin, map[string]interface{}{"id": x2.ID, "on": true})
	for _, n := range []int64{52, 58, 60, 61} {
		if e.blocks(n)[p2] {
			t.Fatalf("exempt rule blocked on %d", n)
		}
	}
	e.ok("/api/v1/leikwan/rule/all-entries", e.admin, map[string]interface{}{"id": x2.ID, "on": false})
	if !e.blocks(58)[p2] {
		t.Fatal("exemption off: alice's rule open on another entry")
	}
	e.ok("/api/v1/leikwan/rule/all-entries", e.admin, map[string]interface{}{"id": r263.ID, "on": true})
	// A rule save keeps the exemption (SaveLeiKwanRule omits it).
	e.ok("/api/v1/leikwan-rule/update", e.admin, map[string]interface{}{"id": r263.ID, "name": "263 改名"})
	if adm := e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{}); !strings.Contains(fmtRules(adm), "263 改名|true") {
		t.Fatalf("exemption lost on save: %s", fmtRules(adm))
	}

	// 换主入口 阿里广州 → 火山广州 (52): the old entry stays open (grace).
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true})
	if b := e.blocks(60); b[p1] || b[p2] {
		t.Fatalf("old entry closed without grace: %v", b)
	}
	if b := e.blocks(52); b[p1] || b[p2] {
		t.Fatalf("new 主入口 blocked: %v", b)
	}
	if b := e.blocks(58); !b[p1] {
		t.Fatalf("other entry open: %v", b)
	}
	grace := aliceGrace(e)
	if grace == nil || grace["prevEntryName"] != "阿里广州" || num(grace["until"])-num(grace["changedAt"]) != 864e5 {
		t.Fatalf("grace view: %v", grace)
	}

	// --- item 6: 生成订阅 ---
	s1 := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})
	s2 := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{"userId": 3}) // userId ignored for users
	if num(s1["id"]) == 0 || s1["id"] != s2["id"] || s1["name"] != "利群汇聚" || num(s1["leikwanNodes"]) != 2 || num(s1["manual"]) != 0 {
		t.Fatalf("生成订阅: %v / %v", s1, s2)
	}
	token := s1["token"].(string)
	subs := e.ok("/api/v1/sub/list", e.user, map[string]interface{}{})["subs"].([]interface{})
	if len(subs) != 1 || subs[0].(map[string]interface{})["auto"] != "leikwan" {
		t.Fatalf("sub list: %v", subs)
	}
	// The pull carries the new 主入口 and ends the grace at once.
	body := e.pull(token)
	if !strings.Contains(body, "101.96.213.164") || !strings.Contains(body, strconv.Itoa(p1)) || !strings.Contains(body, "jp.example.com") || strings.Contains(body, "8.134.172.154") {
		t.Fatalf("sub body after 换主入口:\n%s", body)
	}
	if b := e.blocks(60); !b[p1] || !b[p2] {
		t.Fatalf("grace not ended by the pull: %v", b)
	}
	if aliceGrace(e) != nil {
		t.Fatal("grace still shown")
	}
	// 24 h without a pull also ends a grace.
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true})
	if e.blocks(52)[p1] {
		t.Fatal("grace missing after the second change")
	}
	e.must(e.r.DB().Model(&model.LeiKwanGrant{}).Where("user_id = ?", 2).UpdateColumn("entry_changed_at", time.Now().Add(-25*time.Hour).UnixMilli()).Error)
	if !e.blocks(52)[p1] {
		t.Fatal("grace older than 24 h still open")
	}

	// The 利群汇聚 sub is an ordinary sub: editable, deletable, re-created.
	pool := e.ok("/api/v1/sub/pool", e.user, map[string]interface{}{"id": num(s1["id"])})
	if pool["leikwanGranted"] != true || len(pool["leikwan"].([]interface{})) != 2 || pool["leikwanFollowKey"] != "ka" {
		t.Fatalf("pool: %v %v", pool["leikwanGranted"], pool["leikwan"])
	}
	sel := pool["selected"].([]interface{})
	if len(sel) != 1 || sel[0].(map[string]interface{})["group"] != "leikwan_all" || num(sel[0].(map[string]interface{})["count"]) != 2 {
		t.Fatalf("selected: %v", sel)
	}
	// Edit: keep one single 利群 rule only → 生成订阅 puts 「全部」 back.
	e.ok("/api/v1/sub/update", e.user, map[string]interface{}{"id": num(s1["id"]), "name": "我的利群", "items": []map[string]interface{}{{"kind": "leikwan_rule", "leikwanRuleId": x1.ID}}})
	if b := e.pull(token); strings.Contains(b, "jp.example.com") {
		t.Fatal("edited sub still has every 利群 rule")
	}
	s3 := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})
	if s3["id"] != s1["id"] || num(s3["leikwanNodes"]) != 2 || s3["name"] != "我的利群" {
		t.Fatalf("生成订阅 after edit: %v", s3)
	}
	e.ok("/api/v1/sub/delete", e.user, map[string]interface{}{"id": num(s1["id"])})
	if s4 := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{}); s4["id"] == s1["id"] || s4["token"] == token {
		t.Fatalf("生成订阅 after delete: %v", s4)
	}
	// Admin: for any granted user; bob has no grant.
	if as := e.ok("/api/v1/leikwan-rule/sub", e.admin, map[string]interface{}{"userId": 2}); num(as["leikwanNodes"]) != 2 {
		t.Fatalf("admin 生成订阅 for alice: %v", as)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/sub", e.other, map[string]interface{}{}); out.Code == 0 {
		t.Fatal("bob without grant got a 利群 sub")
	}
	// Admin pool: the imported manual rules are greyed (no full link).
	ap := e.ok("/api/v1/sub/pool", e.admin, map[string]interface{}{})
	greyed := 0
	for _, it := range ap["leikwan"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["disabled"] == true && strings.Contains(m["reason"].(string), "手动落地") {
			greyed++
		}
	}
	if greyed != 7 {
		t.Fatalf("admin pool greyed manual rules: %d", greyed)
	}
	// A sub with a single leikwan_rule pick + other nodes.
	cs := e.ok("/api/v1/sub/create", e.user, map[string]interface{}{"name": "混合", "items": []map[string]interface{}{{"kind": "landing_direct", "landingId": 502}, {"kind": "leikwan_rule", "leikwanRuleId": x2.ID}}})
	if b := e.pull(cs["token"].(string)); !strings.Contains(b, "1.1.1.1") || !strings.Contains(b, "8.134.172.154") || !strings.Contains(b, "jp.example.com") {
		t.Fatalf("mixed sub:\n%s", b)
	}

	// --- item 4: landing page 复制链接 lists the 利群 rule (everyone, node link) ---
	for _, tok := range []string{e.user, e.admin} {
		out, _ := e.call("/api/v1/landing/node/links", tok, map[string]interface{}{"id": 501})
		list, _ := out.Data.([]interface{})
		found := false
		for _, it := range list {
			m := it.(map[string]interface{})
			if m["kind"] == "leikwan" {
				found = strings.HasPrefix(m["url"].(string), "ss://") && strings.Contains(m["url"].(string), "@8.134.172.154:"+strconv.Itoa(p1)) &&
					m["address"] == "8.134.172.154:"+strconv.Itoa(p1) && m["name"] == "利群·广港 · 阿里广州"
			}
		}
		if !found {
			t.Fatalf("landing links: %v", list)
		}
	}
}

func aliceGrace(e *lkLineEnv) map[string]interface{} {
	gl := e.ok("/api/v1/leikwan/grant/list", e.admin, map[string]interface{}{})
	for _, g := range gl["grants"].([]interface{}) {
		m := g.(map[string]interface{})
		if num(m["userId"]) == 2 {
			gr, _ := m["grace"].(map[string]interface{})
			return gr
		}
	}
	return nil
}

func fmtRules(list map[string]interface{}) string {
	var b strings.Builder
	for _, r := range list["rules"].([]interface{}) {
		m := r.(map[string]interface{})
		b.WriteString(m["name"].(string))
		b.WriteString("|")
		if m["allEntries"] == true {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteString(";")
	}
	return b.String()
}
