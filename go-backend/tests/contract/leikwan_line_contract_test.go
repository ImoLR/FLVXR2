package contract_test

// Plan 266 利群专线入面板 step 1: import, grant (主入口 + 备线), user rules with
// automatic 备线, entry change, landing change, delete — driven by the local
// fake LeiKwan API on SQLite and PostgreSQL.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type lkLineEnv struct {
	t                  *testing.T
	r                  *repo.Repository
	fake               *testapi.Fake
	router             http.Handler
	admin, user, other string
}

func newLKLineEnv(t *testing.T, dialect string) *lkLineEnv {
	t.Helper()
	r := landingContractRepo(t, dialect)
	fake := testapi.New()
	fake.SeedReal8()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	h := handler.New(r, "lk-line", "test")
	h.SetLeiKwanClient(leikwan.NewClient(server.URL, nil))
	mux := http.NewServeMux()
	h.Register(mux)
	e := &lkLineEnv{t: t, r: r, fake: fake, router: middleware.JWT(middleware.AuthOptions{JWTSecret: "lk-line"})(mux)}
	e.admin, _ = auth.GenerateToken(1, "imolr", 0, "lk-line")
	e.user, _ = auth.GenerateToken(2, "alice", 1, "lk-line")
	e.other, _ = auth.GenerateToken(3, "bob", 1, "lk-line")
	e.seed()
	return e
}

func (e *lkLineEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *lkLineEnv) seed() {
	db := e.r.DB()
	now := time.Now().UnixMilli()
	for _, u := range []model.User{{ID: 2, User: "alice", Pwd: "x", RoleID: 1, Num: 10, CreatedTime: now, Status: 1, ExpTime: now + 864e5, Flow: 999, FlowResetTime: 1},
		{ID: 3, User: "bob", Pwd: "x", RoleID: 1, Num: 10, CreatedTime: now, Status: 1, ExpTime: now + 864e5, Flow: 999, FlowResetTime: 1}} {
		e.must(db.Create(&u).Error)
	}
	for _, n := range []model.Node{
		{ID: 2, Name: "Mkcloud 深港ixp 1", Secret: "s2", ServerIP: "163.53.52.189", Port: "30000-60000", Status: 0},
		{ID: 47, Name: "AWS HK", Secret: "s47", ServerIP: "43.199.143.188", Port: "10000-65535", Status: 0},
		{ID: 52, Name: "火山广州", Secret: "s52", ServerIP: "101.96.213.164", Port: "10000-65535", Status: 1, LeiKwanStatus: "running"},
		{ID: 58, Name: "百度云广州", Secret: "s58", ServerIP: "106.13.17.23", Port: "10000-65535", Status: 1, LeiKwanStatus: "running"},
		{ID: 60, Name: "阿里广州", Secret: "s60", ServerIP: "8.134.172.154", Port: "10000-65535", Status: 1, LeiKwanStatus: "running"},
		{ID: 61, Name: "火山上海", Secret: "s61", ServerIP: "14.103.34.108", Port: "10000-65535", Status: 1, LeiKwanStatus: "running"},
	} {
		n.CreatedTime, n.UpdatedTime, n.TCPListenAddr, n.UDPListenAddr = now, sql.NullInt64{Int64: now, Valid: true}, "[::]", "[::]"
		e.must(db.Create(&n).Error)
	}
	for _, tn := range []model.Tunnel{
		{ID: 90, Name: "测试·263备线·深港ixp1→99U（端口转发）", Type: 1, Protocol: "tls", Flow: 1, Status: 1},
		{ID: 105, Name: "mk深港ixp1 -> HK(端口转发，Mk出口，接自己落地)", Type: 1, Protocol: "tls", Flow: 1, Status: 1},
		{ID: 4, Name: "mk深港ixp1 -> TW(走HKIX直达)", Type: 2, Protocol: "tls", Flow: 1, Status: 1},
		{ID: 80, Name: "利群·广港·AWS HK", Type: 2, Protocol: "tls", Flow: 1, Status: 1},
	} {
		tn.CreatedTime, tn.UpdatedTime, tn.TrafficRatio = now, now, 1
		e.must(db.Create(&tn).Error)
	}
	for _, c := range []model.ChainTunnel{{TunnelID: 90, ChainType: "1", NodeID: 2}, {TunnelID: 105, ChainType: "1", NodeID: 2}, {TunnelID: 4, ChainType: "1", NodeID: 2}, {TunnelID: 4, ChainType: "3", NodeID: 47}} {
		e.must(db.Create(&c).Error)
	}
	for _, f := range []struct {
		id, tunnel int64
		port       int
		target     string
	}{{148, 90, 35046, "boilhkt99u.imgamer.top:10017"}, {619, 90, 59349, "boilhkt99u.imgamer.top:10019"}, {595, 105, 40304, "188.253.118.132:60061"},
		{601, 105, 52914, "43.199.143.188:10022"}, {587, 4, 31070, "boilhinet2.imgamer.top:12311"}, {599, 4, 52405, "facseednet.imgamer.top:10015"}} {
		fw := model.Forward{ID: f.id, UserID: 1, UserName: "imolr", Name: "備線·" + f.target, TunnelID: f.tunnel, RemoteAddr: f.target, CreatedTime: now, UpdatedTime: now, Status: 1}
		e.must(db.Create(&fw).Error)
		e.must(db.Create(&model.ForwardPort{ForwardID: f.id, NodeID: 2, Port: f.port}).Error)
	}
	acct := model.LeiKwanAccount{ID: 1, Name: "利群汇聚 · 服务 39", ServiceID: 39, Token: e.fake.Token}
	e.must(db.Create(&acct).Error)
	tid := int64(80)
	e.must(db.Create(&model.LeiKwanPipe{AccountID: 1, ForwardID: 318, TunnelID: &tid, LandingNodeID: 47, LandingPort: 10044, ListenPort: 10044, RouteID: 21, RouteLabel: "CAN → HKG 广港", Transport: "tcp", TargetHost: "43.199.143.188"}).Error)
	srv := model.LandingServer{ID: 11, Source: "native", Name: "Boil HKT 99u", PublicHost: "8.8.8.8", Status: "active"}
	e.must(db.Create(&srv).Error)
	e.must(db.Create(&model.LandingServer{ID: 12, Source: "native", Name: "Bestvm JP B", PublicHost: "1.1.1.1", Status: "active"}).Error)
	for _, ln := range []model.LandingNode{
		{ID: 501, UserID: 2, LandingServerID: 11, Name: "alice-99u", Protocol: "ss2022", Port: 30001, InboundTag: "a1", Method: "2022-blake3-aes-128-gcm", ServerKey: "c2VydmVy", UserKey: "dXNlcg==", Status: "active"},
		{ID: 502, UserID: 2, LandingServerID: 12, Name: "alice-jp", Protocol: "ss2022", Port: 30002, InboundTag: "a2", Method: "2022-blake3-aes-128-gcm", ServerKey: "c2VydmVy", UserKey: "dXNlcg==", Status: "active"},
		{ID: 503, UserID: 3, LandingServerID: 11, Name: "bob-99u", Protocol: "ss2022", Port: 30003, InboundTag: "b1", Method: "2022-blake3-aes-128-gcm", ServerKey: "c2VydmVy", UserKey: "dXNlcg==", Status: "active"},
	} {
		e.must(db.Create(&ln).Error)
	}
	// Read-only host snapshots (secret-free), as imported from the hosts.
	baidu := map[string]interface{}{
		"host": "instance-nbieyy68", "agentVersion": "2026.11.15.1", "coreVersion": "2026.11.13.1", "updateLock": true, "nativeUdp": true, "frontChannel": "test",
		"frontBuild": "test-ssfilter-coalesce-warmflush-paced", "fastFailover": "test", "logswitch": "v11", "holdS": 30, "leaseSettleS": 30, "grace": "switch", "returnCheckS": 2.5,
		"coalesce":    "on budget_ms=2 min=59",
		"warm":        map[string]int{"263": 10, "264": 4, "280": 4, "282": 4, "540": 10, "backup:263": 4, "backup:264": 4, "backup:280": 4, "backup:282": 4, "backup:540": 4, "backup_standby": 2, "pace_ms": 100},
		"ssFilter":    map[string]string{"263": "enforce", "264": "enforce", "279": "enforce", "280": "enforce", "281": "enforce", "282": "enforce", "540": "enforce"},
		"triggers":    map[string]string{"timeout": "switch", "warm_death": "switch", "io_error": "record", "return_check": "off", "263:return_check": "on", "264:return_check": "on", "280:return_check": "on", "282:return_check": "on", "540:return_check": "on"},
		"lines":       []map[string]interface{}{{"forwardId": 263, "listenPort": 10018, "backup": "163.53.52.189:35046"}, {"forwardId": 264, "listenPort": 10015, "backup": "163.53.52.189:40304"}, {"forwardId": 279, "listenPort": 10019, "backup": "163.53.52.189:52914"}, {"forwardId": 280, "listenPort": 10020, "backup": "163.53.52.189:31070"}, {"forwardId": 281, "listenPort": 10016, "backup": "163.53.52.189:52405"}, {"forwardId": 282, "listenPort": 10017, "backup": "163.53.52.189:31070"}, {"forwardId": 540, "listenPort": 10021, "backup": "163.53.52.189:59349"}},
		"phoneBackup": []map[string]interface{}{{"port": 19263, "target": "163.53.52.189:35046", "note": "263 广港"}},
	}
	gz := map[string]interface{}{"host": "iv-yemha2nk74f8b8xih1dh", "agentVersion": "2026.10.12.1", "warm": map[string]int{"263": 10, "264": 4, "280": 4, "282": 4, "backup:263": 4, "backup_standby": 2, "pace_ms": 100},
		"lines": []map[string]interface{}{{"forwardId": 263, "listenPort": 10018, "backup": "163.53.52.189:35046"}}}
	for node, snap := range map[int64]map[string]interface{}{58: baidu, 52: gz} {
		if out, _ := e.call("/api/v1/leikwan/host/import", e.admin, map[string]interface{}{"nodeId": node, "snapshot": snap}); out.Code != 0 {
			e.t.Fatalf("host import %d: %+v", node, out)
		}
	}
}

func (e *lkLineEnv) call(path, token string, body interface{}) (response.R, map[string]interface{}) {
	e.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	if strings.Contains(w.Body.String(), e.fake.Token) {
		e.t.Fatal("LeiKwan token leaked")
	}
	data, _ := out.Data.(map[string]interface{})
	return out, data
}

func (e *lkLineEnv) ok(path, token string, body interface{}) map[string]interface{} {
	e.t.Helper()
	out, data := e.call(path, token, body)
	if out.Code != 0 {
		e.t.Fatalf("%s: code=%d msg=%s", path, out.Code, out.Msg)
	}
	return data
}

func num(v interface{}) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func (e *lkLineEnv) forwardCount(where string, args ...interface{}) int64 {
	var n int64
	e.must(e.r.DB().Model(&model.Forward{}).Where(where, args...).Count(&n).Error)
	return n
}

func TestLeiKwanLinePanelContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanLinePanelFlow(t, dialect) })
	}
}

func leiKwanLinePanelFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)

	// Admin-only endpoints (users get 403 + envelope).
	for _, p := range []string{"line/list", "line/save", "grant/save", "grant/list", "import/preview", "import/apply", "host/list", "host/import", "setting/save", "rule/backup-retry"} {
		if out, _ := e.call("/api/v1/leikwan/"+p, e.user, map[string]interface{}{}); out.Code != 403 {
			t.Fatalf("%s user access: %+v", p, out)
		}
	}
	// Snapshots that look like they carry a key are refused.
	if out, _ := e.call("/api/v1/leikwan/host/import", e.admin, map[string]interface{}{"nodeId": 60, "snapshot": map[string]interface{}{"extra": []string{"LEIKWAN_FRONT_SSKEYS=263:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}}); out.Code == 0 {
		t.Fatal("secret-like snapshot accepted")
	}

	// --- import: dry run first, no LeiKwan write at any point ---
	_, writesBefore := e.fake.Counts()
	plan := e.ok("/api/v1/leikwan/import/preview", e.admin, map[string]interface{}{})
	items := plan["items"].([]interface{})
	byID := map[int64]map[string]interface{}{}
	for _, it := range items {
		m := it.(map[string]interface{})
		byID[num(m["forwardId"])] = m
	}
	if len(items) != 8 || byID[318]["action"] != "skip" {
		t.Fatalf("preview items: %v", items)
	}
	want := map[int64]struct {
		line   string
		backup int64
	}{263: {"利群·广港", 148}, 540: {"利群·广港", 619}, 279: {"利群·广港", 601}, 264: {"利群·沪日", 595}, 280: {"利群·广台", 587}, 281: {"利群·广台", 599}, 282: {"利群·福台", 587}}
	for id, w := range want {
		it := byID[id]
		if it["action"] != "import" || it["lineName"] != w.line || num(it["backupForwardId"]) != w.backup {
			t.Fatalf("preview %d: %v", id, it)
		}
	}
	if shared := byID[280]["backupSharedWith"].([]interface{}); len(shared) != 1 || num(shared[0]) != 282 {
		t.Fatalf("587 should be shared by 280 + 282: %v", byID[280])
	}
	// 540 is the hand-made test rule: registered without a warm pool.
	if acc := byID[540]["accel"].(map[string]interface{}); num(acc["warmMain"]) != 0 || !strings.Contains(acc["note"].(string), "测试") {
		t.Fatalf("540 accel: %v", acc)
	}
	if acc := byID[279]["accel"].(map[string]interface{}); num(acc["warmMain"]) != 0 || acc["returnCheck"] != false {
		t.Fatalf("279 accel: %v", acc)
	}
	grant := plan["adminGrant"].(map[string]interface{})
	if grant["exists"] != false || num(grant["mainEntryNodeId"]) != 58 {
		t.Fatalf("admin grant proposal: %v", grant)
	}
	if out, _ := e.call("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{}); out.Code == 0 {
		t.Fatal("apply without confirmation accepted")
	}
	done := e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	if len(done["rulesImported"].([]interface{})) != 7 || len(done["linesCreated"].([]interface{})) != 4 || done["groupCreated"] != true {
		t.Fatalf("apply: %v", done)
	}
	if _, writes := e.fake.Counts(); writes != writesBefore {
		t.Fatalf("import wrote to LeiKwan: %d -> %d", writesBefore, writes)
	}
	again := e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	if len(again["rulesImported"].([]interface{})) != 0 || len(again["linesCreated"].([]interface{})) != 0 {
		t.Fatalf("import not idempotent: %v", again)
	}
	if n := e.forwardCount("1 = 1"); n != 6 {
		t.Fatalf("import must link backups, not create forwards: %d", n)
	}
	var groupID sql.NullInt64
	e.must(e.r.DB().Raw("SELECT group_id FROM node WHERE id = 61").Scan(&groupID).Error)
	if !groupID.Valid {
		t.Fatal("火山上海 not in 利群入口")
	}

	lines := e.ok("/api/v1/leikwan/line/list", e.admin, map[string]interface{}{})
	var ggLine map[string]interface{}
	for _, l := range lines["lines"].([]interface{}) {
		m := l.(map[string]interface{})
		if m["name"] == "利群·广港" {
			ggLine = m
		}
	}
	if ggLine == nil || num(ggLine["ruleCount"]) != 3 || num(ggLine["backupTunnelId"]) != 90 {
		t.Fatalf("广港 line: %v", ggLine)
	}
	if acc := ggLine["accel"].(map[string]interface{}); num(acc["warm"].(map[string]interface{})["main"]) != 10 {
		t.Fatalf("广港 warm default: %v", acc)
	}
	if len(lines["pipes"].([]interface{})) != 1 || num(lines["quota"].(map[string]interface{})["active"]) != 8 {
		t.Fatalf("pipes/quota: %v %v", lines["pipes"], lines["quota"])
	}
	lineID := num(ggLine["id"])
	// Switch 广港's 备线隧道 to the general ixp1 port-forward tunnel.
	e.ok("/api/v1/leikwan/line/save", e.admin, map[string]interface{}{"id": lineID, "name": "利群·广港", "routeId": 21, "transport": "tcp+udp", "backupTunnelId": 105, "accel": ggLine["accel"]})

	// Admin rules show the admin grant's entry (百度云广州).
	admRules := e.ok("/api/v1/leikwan-rule/list", e.admin, map[string]interface{}{})["rules"].([]interface{})
	if len(admRules) != 7 {
		t.Fatalf("admin rules: %d", len(admRules))
	}
	for _, r := range admRules {
		m := r.(map[string]interface{})
		if num(m["forwardId"]) == 263 {
			if m["entry"].(map[string]interface{})["address"] != "106.13.17.23:10018" || m["backup"].(map[string]interface{})["mode"] != "linked" {
				t.Fatalf("263 view: %v", m)
			}
		}
	}

	// --- user: no grant yet ---
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-alice-1"}); out.Code == 0 || !strings.Contains(out.Msg, "授权") {
		t.Fatalf("create without grant: %+v", out)
	}
	if out, _ := e.call("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 47, "backupLine": true}); out.Code == 0 {
		t.Fatal("main entry outside 利群入口 accepted")
	}
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true, "num": 2, "flowGb": 100})

	// Quota is 8 / 8: a new rule is refused before any LeiKwan write.
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-alice-1"}); out.Code == 0 || !strings.Contains(out.Msg, "名额") {
		t.Fatalf("quota full: %+v", out)
	}
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 10
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh() // the 8 / 8 read is cached 15 s; an account save drops it
	opts := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})
	if len(opts["landings"].([]interface{})) != 2 || opts["grant"].(map[string]interface{})["mainEntryName"] != "阿里广州" {
		t.Fatalf("user options: %v", opts)
	}
	// Other users' landing nodes and manual targets are not allowed for users.
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "x", "landingNodeId": 503, "requestId": "req-alice-x"}); out.Code == 0 {
		t.Fatal("foreign landing node accepted")
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "x", "targetHost": "9.9.9.9", "targetPort": 443, "requestId": "req-alice-y"}); out.Code == 0 {
		t.Fatal("user manual target accepted")
	}
	created := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-alice-1"})
	rule := created["rule"].(map[string]interface{})
	if !rule["backupOn"].(bool) || rule["entry"].(map[string]interface{})["nodeName"] != "阿里广州" || rule["forwardId"] != nil {
		t.Fatalf("user rule view: %v", rule)
	}
	ruleID := num(rule["id"])
	var x model.LeiKwanRule
	e.must(e.r.DB().First(&x, ruleID).Error)
	var backup model.Forward
	e.must(e.r.DB().First(&backup, x.BackupForwardID).Error)
	if backup.UserID != 1 || backup.TunnelID != 105 || backup.RemoteAddr != "8.8.8.8:30001" || x.BackupMode != "auto" {
		t.Fatalf("auto backup: %+v / %+v", backup, x)
	}
	// Same request id again: idempotent (no second LeiKwan forward / 备线).
	_, w1 := e.fake.Counts()
	e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": lineID, "name": "我的广港", "landingNodeId": 501, "requestId": "req-alice-1"})
	if _, w2 := e.fake.Counts(); w2 != w1 || e.forwardCount("1 = 1") != 7 {
		t.Fatal("retried create duplicated something")
	}
	// The user sees only their own rule, without admin fields.
	mine := e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})
	if len(mine) != 1 || mine[0].(map[string]interface{})["accel"] != nil {
		t.Fatalf("user list: %v", mine)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/delete", e.other, map[string]interface{}{"id": ruleID, "confirmed": true}); out.Code == 0 {
		t.Fatal("other user deleted the rule")
	}
	link := e.ok("/api/v1/leikwan-rule/link", e.user, map[string]interface{}{"id": ruleID})
	if !strings.HasPrefix(link["url"].(string), "ss://") || !strings.Contains(link["url"].(string), "@8.134.172.154:") {
		t.Fatalf("link: %v", link)
	}

	// Change the grant's 主入口: every rule address follows.
	moved := e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true, "num": 2, "flowGb": 100})
	if num(moved["movedRules"]) != 1 {
		t.Fatalf("moved: %v", moved)
	}
	mine = e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})
	if addr := mine[0].(map[string]interface{})["entry"].(map[string]interface{})["address"].(string); !strings.HasPrefix(addr, "101.96.213.164:") {
		t.Fatalf("address after entry change: %s", addr)
	}

	// Change the landing: LeiKwan target patched, the 备线 follows.
	e.ok("/api/v1/leikwan-rule/update", e.user, map[string]interface{}{"id": ruleID, "changeTarget": true, "landingNodeId": 502})
	e.fake.Mu.Lock()
	tgt := e.fake.Forwards[x.ForwardID].Target
	e.fake.Mu.Unlock()
	e.must(e.r.DB().First(&backup, x.BackupForwardID).Error)
	if tgt.Host != "1.1.1.1" || tgt.Port != 30002 || backup.RemoteAddr != "1.1.1.1:30002" {
		t.Fatalf("retarget: %+v %s", tgt, backup.RemoteAddr)
	}

	// 备线 off removes the automatic backup; on creates it again.
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": false, "num": 2})
	if e.forwardCount("id = ?", x.BackupForwardID) != 0 {
		t.Fatal("备线 off kept the auto backup")
	}
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 52, "backupLine": true, "num": 2})
	e.must(e.r.DB().First(&x, ruleID).Error)
	if x.BackupForwardID == 0 || e.forwardCount("id = ?", x.BackupForwardID) != 1 {
		t.Fatal("备线 on did not recreate the backup")
	}

	// Delete: LeiKwan forward deleted, auto backup gone.
	e.ok("/api/v1/leikwan-rule/delete", e.user, map[string]interface{}{"id": ruleID, "confirmed": true})
	e.fake.Mu.Lock()
	_, still := e.fake.Forwards[x.ForwardID]
	e.fake.Mu.Unlock()
	if still || e.forwardCount("id = ?", x.BackupForwardID) != 0 {
		t.Fatal("delete left the LeiKwan forward or the backup")
	}

	// An imported rule deleted with 「只解除登记」 keeps LeiKwan and its linked 备线.
	var r263 model.LeiKwanRule
	e.must(e.r.DB().Where("forward_id = ?", 263).First(&r263).Error)
	e.ok("/api/v1/leikwan-rule/delete", e.admin, map[string]interface{}{"id": r263.ID, "confirmed": true, "keepLeiKwan": true})
	e.fake.Mu.Lock()
	_, kept := e.fake.Forwards[263]
	e.fake.Mu.Unlock()
	if !kept || e.forwardCount("id = 148") != 1 {
		t.Fatal("unregister removed the LeiKwan forward or the linked backup")
	}
	if out, _ := e.call("/api/v1/leikwan/grant/delete", e.admin, map[string]interface{}{"userId": 1}); out.Code == 0 {
		t.Fatal("grant with rules deleted")
	}
}

// fakeLimitRefresh waits out the client's 15 s overview cache cheaply by
// saving the account again (which invalidates the cache like an admin edit).
func (e *lkLineEnv) fakeLimitRefresh() bool {
	e.ok("/api/v1/leikwan/account/save", e.admin, map[string]interface{}{"id": 1, "name": "利群汇聚 · 服务 39", "serviceId": 39, "token": ""})
	return true
}

// After everything is imported the preview still returns arrays (the UI maps them).
func TestLeiKwanImportPreviewEmptyListsContract(t *testing.T) {
	e := newLKLineEnv(t, "sqlite")
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	out, w := e.call("/api/v1/leikwan/import/preview", e.admin, map[string]interface{}{})
	_ = w
	data := out.Data.(map[string]interface{})
	if _, ok := data["lines"].([]interface{}); !ok {
		t.Fatalf("lines must be an array: %v", data["lines"])
	}
}
