package contract_test

// Plan 276: fixes for the 利群 QA findings (plan 275) — user delete cascade
// (F01), 备线 retarget / 重试备线 (F02), grace ends only on a sub with 利群
// nodes (F03), 外部节点 / landing delete refused while a 利群 rule uses it
// (F05), user changes re-block at once (F06), 备线 pause/resume retried
// (F07), name / requestId messages (F10), vendor quota text (F11) and a
// stopped grant blocking its rules (F12) — SQLite and PostgreSQL.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

func TestLeiKwanQAFixContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanQAFixFlow(t, dialect) })
	}
}

func (e *lkLineEnv) rule(id int64) model.LeiKwanRule {
	e.t.Helper()
	var x model.LeiKwanRule
	e.must(e.r.DB().First(&x, id).Error)
	return x
}

// waitBlock polls the rule's block reason (user changes recompute it async).
func (e *lkLineEnv) waitBlock(id int64, want string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		x := e.rule(id)
		if x.BlockReason == want {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("rule %d block reason %q, want %q", id, x.BlockReason, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *lkLineEnv) adminRuleView(id int64, token string) map[string]interface{} {
	e.t.Helper()
	body := map[string]interface{}{}
	if token == e.admin {
		body["userId"] = 2
	}
	for _, r := range e.ok("/api/v1/leikwan-rule/list", token, body)["rules"].([]interface{}) {
		if m := r.(map[string]interface{}); num(m["id"]) == id {
			return m
		}
	}
	e.t.Fatalf("rule %d not listed", id)
	return nil
}

func leiKwanQAFixFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	db := e.r.DB()
	e.must(db.Model(&model.LandingNode{}).Where("id IN ?", []int64{501, 502}).Updates(map[string]interface{}{"server_key": "MTIzNDU2Nzg5MDEyMzQ1Ng==", "user_key": "YWJjZGVmZ2hpamtsbW5vcA=="}).Error)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	gg := e.lineByName("利群·广港")
	lineID := num(gg["id"])
	e.ok("/api/v1/leikwan/line/save", e.admin, map[string]interface{}{"id": lineID, "name": "利群·广港", "routeId": 21, "transport": "tcp+udp", "backupTunnelId": 105, "accel": gg["accel"]})
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	ext := model.LandingExternalNode{UserID: 2, Name: "我的日本", Protocol: "trojan", Host: "203.0.113.50", Port: 8443}
	up := linechain.Upstream{Type: "trojan", Server: "203.0.113.50", Port: 8443, Password: "pw-trojan", Security: "tls", TLS: &linechain.TLS{ServerName: "jp.example.com"}}
	ext.Upstream, ext.UpstreamHash, ext.Summary = up.JSON(), up.Hash(), up.Summary()
	e.must(db.Create(&ext).Error)
	r1 := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-qafix-1"})["rule"].(map[string]interface{})
	r2 := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "广港到日本", "externalNodeId": ext.ID, "requestId": "req-qafix-2"})["rule"].(map[string]interface{})
	x1, x2 := e.rule(num(r1["id"])), e.rule(num(r2["id"]))
	if x1.BackupForwardID <= 0 || x2.BackupForwardID <= 0 || x1.BackupError != "" {
		t.Fatalf("setup 备线: %+v %+v", x1, x2)
	}

	// --- F10: name / requestId messages ---
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": strings.Repeat("利", 34), "landingNodeId": 501, "requestId": "req-qafix-long"}); out.Code == 0 || !strings.Contains(out.Msg, "规则名称太长") {
		t.Fatalf("long name: %+v", out)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "ok", "landingNodeId": 501, "requestId": "x"}); out.Code == 0 || !strings.Contains(out.Msg, "请求标识无效") {
		t.Fatalf("bad requestId: %+v", out)
	}

	// --- F05: 外部节点 / panel landing in use by a 利群 rule ---
	if out, _ := e.call("/api/v1/landing/external/delete", e.user, map[string]interface{}{"id": ext.ID}); out.Code == 0 || !strings.Contains(out.Msg, "利群规则") || !strings.Contains(out.Msg, "广港到日本") {
		t.Fatalf("external delete in use: %+v", out)
	}
	items := e.ok("/api/v1/landing/external/list", e.user, map[string]interface{}{})["items"].([]interface{})
	uses := items[0].(map[string]interface{})["inUse"].([]interface{})
	if len(uses) != 1 || uses[0].(map[string]interface{})["kind"] != "leikwan" || uses[0].(map[string]interface{})["name"] != "广港到日本" {
		t.Fatalf("external inUse: %v", uses)
	}
	if err := e.r.DeleteLandingNode(501); err == nil || !strings.Contains(err.Error(), "我的广港") {
		t.Fatalf("landing delete in use: %v", err)
	}
	if _, err := e.r.PrepareLandingNodeDeletion(501); err == nil || !strings.Contains(err.Error(), "利群规则") {
		t.Fatalf("landing prepare delete in use: %v", err)
	}
	if _, err := e.r.PrepareSharedLandingDeletion(501); err == nil || !strings.Contains(err.Error(), "利群规则") {
		t.Fatalf("shared landing delete in use: %v", err)
	}

	// --- F02: a failed 备线 retarget is marked, a later success clears it ---
	setTunnel := func(status int) {
		e.must(db.Model(&model.Tunnel{}).Where("id = ?", 105).UpdateColumn("status", status).Error)
	}
	remote := func(id int64) string {
		var f model.Forward
		e.must(db.First(&f, id).Error)
		return f.RemoteAddr
	}
	setTunnel(0)
	res := e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": x1.ID, "changeTarget": true, "landingNodeId": 502})
	if w, _ := res["warnings"].([]interface{}); len(w) != 1 || !strings.Contains(w[0].(string), "备线目标未改好") {
		t.Fatalf("retarget warning: %v", res["warnings"])
	}
	if x := e.rule(x1.ID); !strings.HasPrefix(x.BackupError, "备线还指向旧落地") || remote(x.BackupForwardID) != "8.8.8.8:30001" {
		t.Fatalf("stale 备线: %+v %s", x, remote(x.BackupForwardID))
	}
	if v := e.adminRuleView(x1.ID, e.admin); v["backupStale"] != true {
		t.Fatalf("admin view backupStale: %v", v)
	}
	if v := e.adminRuleView(x1.ID, e.user); v["backupError"] != "备线还指向旧落地，管理员处理中" || v["backupStale"] != true {
		t.Fatalf("user view stale: %v", v)
	}
	setTunnel(1)
	e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": x1.ID, "changeTarget": true, "landingNodeId": 501})
	if x := e.rule(x1.ID); x.BackupError != "" || remote(x.BackupForwardID) != "8.8.8.8:30001" {
		t.Fatalf("successful retarget kept the error: %+v", x)
	}
	setTunnel(0)
	e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": x1.ID, "changeTarget": true, "landingNodeId": 502})
	setTunnel(1)
	e.ok("/api/v1/leikwan/rule/backup-retry", e.admin, map[string]interface{}{"id": x1.ID})
	if x := e.rule(x1.ID); x.BackupError != "" || remote(x.BackupForwardID) != "1.1.1.1:30002" || x.BackupForwardID != x1.BackupForwardID {
		t.Fatalf("重试备线 retarget: %+v %s", x, remote(x.BackupForwardID))
	}
	// A 备线 that cannot be changed (its tunnel is gone) is deleted and created again.
	e.must(db.Model(&model.Forward{}).Where("id = ?", x1.BackupForwardID).UpdateColumn("tunnel_id", 999).Error)
	e.ok("/api/v1/leikwan/rule/backup-retry", e.admin, map[string]interface{}{"id": x1.ID})
	x := e.rule(x1.ID)
	if x.BackupError != "" || x.BackupForwardID == x1.BackupForwardID || x.BackupMode != "auto" || remote(x.BackupForwardID) != "1.1.1.1:30002" {
		t.Fatalf("重试备线 recreate: %+v", x)
	}
	if e.forwardCount("id = ?", x1.BackupForwardID) != 0 {
		t.Fatal("old 备线 left behind")
	}
	x1 = x

	// --- F03: only a pull carrying 利群 nodes ends the 换主入口 grace ---
	global := map[string]interface{}{"holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5, "ioError": "record", "coalesce": "off", "entryLock": true}
	e.ok("/api/v1/leikwan/setting/save", e.admin, map[string]interface{}{"global": global})
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true})
	p1 := x1.ListenPort
	if e.blocks(60)[p1] || aliceGrace(e) == nil {
		t.Fatal("grace missing after 换主入口")
	}
	plain := e.ok("/api/v1/sub/create", e.user, map[string]interface{}{"name": "我的普通订阅", "format": "auto", "ruleMode": "cn_direct", "items": []map[string]interface{}{{"kind": "landing_direct", "landingId": 502}}})
	if body := e.pull(plain["token"].(string)); strings.Contains(body, "101.96.213.164") {
		t.Fatalf("plain sub has 利群 nodes:\n%s", body)
	}
	if e.blocks(60)[p1] || aliceGrace(e) == nil {
		t.Fatal("a sub without 利群 nodes ended the grace")
	}
	lk := e.ok("/api/v1/leikwan-rule/sub", e.user, map[string]interface{}{})
	if body := e.pull(lk["token"].(string)); !strings.Contains(body, "101.96.213.164") || !strings.Contains(body, strconv.Itoa(p1)) {
		t.Fatalf("利群 sub body:\n%s", body)
	}
	if !e.blocks(60)[p1] || aliceGrace(e) != nil {
		t.Fatal("the 利群 sub pull did not end the grace")
	}

	// --- F06: user changes re-block at once (no 5 min wait) ---
	userUpdate := func(status int, exp int64) {
		e.ok("/api/v1/user/update", e.admin, map[string]interface{}{"id": 2, "user": "alice", "flow": 999, "num": 10, "expTime": exp, "status": status, "flowResetTime": 1})
	}
	future := time.Now().Add(48 * time.Hour).UnixMilli()
	userUpdate(0, future)
	e.waitBlock(x1.ID, "账号已禁用")
	userUpdate(1, time.Now().Add(-time.Minute).UnixMilli())
	e.waitBlock(x1.ID, "账号已到期")
	userUpdate(1, future)
	e.waitBlock(x1.ID, "")

	// --- F07: an automatic 备线 out of step with its rule is fixed by the next pass ---
	e.must(db.Model(&model.Forward{}).Where("id = ?", x1.BackupForwardID).UpdateColumn("status", 0).Error)
	e.h.ReconcileLeiKwanBlocks()
	var bk model.Forward
	e.must(db.First(&bk, x1.BackupForwardID).Error)
	if bk.Status != 1 {
		t.Fatalf("paused 备线 of an open rule not resumed: %d", bk.Status)
	}

	// --- F12: a stopped grant blocks its rules and refuses links ---
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true, "status": 0})
	e.h.ReconcileLeiKwanBlocks()
	if x := e.rule(x1.ID); x.BlockReason != "授权已停用" {
		t.Fatalf("stopped grant: %q", x.BlockReason)
	}
	if !e.blocks(52)[p1] || !e.blocks(58)[p1] {
		t.Fatal("stopped grant: ports open")
	}
	if out, _ := e.call("/api/v1/leikwan-rule/link", e.admin, map[string]interface{}{"id": x1.ID}); out.Code == 0 || !strings.Contains(out.Msg, "授权已停用") {
		t.Fatalf("link with a stopped grant: %+v", out)
	}
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true, "status": 1})
	e.h.ReconcileLeiKwanBlocks()
	if x := e.rule(x1.ID); x.BlockReason != "" {
		t.Fatalf("grant back on: %q", x.BlockReason)
	}

	// --- F11: the vendor's quota refusal in plain words ---
	e.ok("/api/v1/leikwan-rule/options", e.admin, map[string]interface{}{"userId": 2}) // caches the overview
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = len(e.fake.Forwards)
	e.fake.Mu.Unlock()
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "满了", "externalNodeId": ext.ID, "requestId": "req-qafix-full"}); out.Code == 0 || out.Msg != "利群名额已满，请联系管理员" {
		t.Fatalf("quota exceeded text: %+v", out)
	}
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 12
	e.fake.Mu.Unlock()

	// --- F01: deleting the user deletes the 利群 rules (forward + 备线 + grant) ---
	var listed bool
	for _, u := range e.okList("/api/v1/user/list") {
		if num(u["id"]) == 2 {
			listed = num(u["leikwanRuleCount"]) == 2
		}
	}
	if !listed {
		t.Fatal("user list: leikwanRuleCount missing")
	}
	backups := []int64{x1.BackupForwardID, x2.BackupForwardID}
	e.fake.Mu.Lock()
	e.fake.FailNext = 422
	e.fake.Mu.Unlock()
	if out, _ := e.call("/api/v1/user/delete", e.admin, map[string]interface{}{"id": 2}); out.Code == 0 || !strings.Contains(out.Msg, "用户未删除") {
		t.Fatalf("user delete with a LeiKwan failure: %+v", out)
	}
	var n int64
	e.must(db.Model(&model.User{}).Where("id = ?", 2).Count(&n).Error)
	if n != 1 {
		t.Fatal("user deleted although the LeiKwan delete failed")
	}
	e.ok("/api/v1/user/delete", e.admin, map[string]interface{}{"id": 2})
	e.must(db.Model(&model.LeiKwanRule{}).Where("user_id = ?", 2).Count(&n).Error)
	var g int64
	e.must(db.Model(&model.LeiKwanGrant{}).Where("user_id = ?", 2).Count(&g).Error)
	if n != 0 || g != 0 || e.forwardCount("id IN ?", backups) != 0 {
		t.Fatalf("after user delete: rules %d grant %d 备线 %d", n, g, e.forwardCount("id IN ?", backups))
	}
	e.fake.Mu.Lock()
	_, f1 := e.fake.Forwards[x1.ForwardID]
	_, f2 := e.fake.Forwards[x2.ForwardID]
	e.fake.Mu.Unlock()
	if f1 || f2 {
		t.Fatal("LeiKwan forwards left after the user delete")
	}
	// Fallback: a rule whose owner is gone is blocked, never open.
	e.must(db.Create(&model.LeiKwanRule{UserID: 77, LineID: lineID, AccountID: 1, ForwardID: 9077, Name: "孤儿", ListenPort: 10077, Status: 1, Source: "panel"}).Error)
	e.h.ReconcileLeiKwanBlocks()
	var orphan model.LeiKwanRule
	e.must(db.Where("forward_id = ?", 9077).First(&orphan).Error)
	if orphan.BlockReason != "用户已删除" || !e.blocks(58)[10077] {
		t.Fatalf("orphan rule: %q", orphan.BlockReason)
	}
}

// okList: a list endpoint answering {code, data: [ ... ]}.
func (e *lkLineEnv) okList(path string) []map[string]interface{} {
	e.t.Helper()
	out, _ := e.call(path, e.admin, map[string]interface{}{})
	if out.Code != 0 {
		e.t.Fatalf("%s: %+v", path, out)
	}
	rows := []map[string]interface{}{}
	list, _ := out.Data.([]interface{})
	for _, it := range list {
		if m, ok := it.(map[string]interface{}); ok {
			rows = append(rows, m)
		}
	}
	return rows
}
