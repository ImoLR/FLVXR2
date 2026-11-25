package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 272 诊断: admin sees every ingress of the 主入口, users one 第一跳 row
// without relay IPs; a manual landing's 专线 → 落地 is an estimate (全程 −
// 第一跳); a dead ingress shows as a failed row; the stream ends with the
// authoritative list; one diagnose per user per 10 s. Plan 301: only the
// chosen entry is diagnosed (no other 利群入口 hosts).
func TestLeiKwanDiagnose(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{repo: r}
	db := r.DB()
	now := time.Now().UnixMilli()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.NodeGroup{ID: 5, Name: "利群入口", CreatedTime: now}).Error)
	for _, n := range []model.Node{{ID: 58, Name: "百度云广州", ServerIP: "106.13.17.23"}, {ID: 60, Name: "阿里广州", ServerIP: "8.134.172.154"}, {ID: 61, Name: "火山上海", ServerIP: "14.103.34.108"}} {
		n.Secret, n.Port, n.CreatedTime, n.Status, n.GroupID = "s", "1-65535", now, 1, sql.NullInt64{Int64: 5, Valid: true}
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 1, AccountID: 1, RouteID: 21, Name: "利群·广港", RouteLabel: "CAN → HKG", Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 2, MainEntryNodeID: 58, BackupLine: 0, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 7, UserID: 2, LineID: 1, ForwardID: 263, Name: "朋友的落地", ListenPort: 10024, TargetHost: "203.0.113.8", TargetPort: 443, Status: 1}).Error)

	var mu sync.Mutex
	calls := map[int64]int{}
	h.nodeCommandSender = func(node int64, cmd string, data interface{}, timeout time.Duration) (ws.CommandResult, error) {
		mu.Lock()
		calls[node]++
		mu.Unlock()
		if cmd != "LeiKwanDiag" {
			t.Errorf("unexpected command %s", cmd)
		}
		switch node {
		case 61:
			return ws.CommandResult{Success: false, Message: "未知命令: LeiKwanDiag"}, nil
		case 60:
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "found": true, "ingresses": []interface{}{
				map[string]interface{}{"endpoint": "121.37.28.219:5438", "offered": true, "ok": true, "avgMs": 9.0, "loss": 0.0}}}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "found": true, "state": "ready", "ingresses": []interface{}{
			map[string]interface{}{"endpoint": "125.94.47.233:5438", "inUse": "tcp", "offered": true, "ok": true, "avgMs": 4.0, "loss": 0.0},
			map[string]interface{}{"endpoint": "106.75.132.155:5438", "offered": true, "ok": true, "avgMs": 6.0, "loss": 0.0},
			map[string]interface{}{"endpoint": "121.37.28.219:5438", "inUse": "udp", "offered": true, "ok": true, "avgMs": 9.0, "loss": 0.0},
			map[string]interface{}{"endpoint": "43.139.9.158:5438", "offered": true, "ok": false, "avgMs": 0.0, "loss": 100.0, "error": "连接超时"},
		}, "udp": map[string]interface{}{"rttMs": 18.0, "sent": 30, "recv": 30, "ageS": 40}}}, nil
	}
	call := func(uid int64, role int, path string, fn func(w *httptest.ResponseRecorder, req *http.Request)) string {
		raw, _ := json.Marshal(map[string]interface{}{"id": 7})
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		tok, _ := auth.GenerateToken(uid, "u", role, "s")
		claims, _ := auth.ParseClaims(tok, "s")
		req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, claims))
		w := httptest.NewRecorder()
		fn(w, req)
		return w.Body.String()
	}

	// User, streamed.
	body := call(2, 1, "/api/v1/leikwan-rule/diagnose/stream", func(w *httptest.ResponseRecorder, req *http.Request) { h.leikwanRuleDiagnoseStream(w, req) })
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var done struct {
		Data struct {
			Items   []map[string]interface{} `json:"items"`
			Summary map[string]interface{}   `json:"summary"`
		} `json:"data"`
		Type string `json:"type"`
	}
	must(json.Unmarshal([]byte(lines[len(lines)-1]), &done))
	if done.Type != "done" || len(lines) < 3 {
		t.Fatalf("stream: %s", body)
	}
	items, sum := done.Data.Items, done.Data.Summary
	if len(items) != 2 || items[0]["section"] != "hop1" || items[0]["title"] != "百度云广州 → 利群专线" || items[0]["inUse"] != "udp" || items[0]["averageTime"] != 9.0 {
		t.Fatalf("user 第一跳: %v", items)
	}
	if strings.Contains(body, "121.37.28.219") || strings.Contains(body, "203.0.113.8") || strings.Contains(body, "华为云") {
		t.Fatalf("user sees relay / manual addresses: %s", body)
	}
	if h2 := items[1]; h2["section"] != "hop2" || h2["estimate"] != true || h2["averageTime"] != 9.0 || !strings.Contains(h2["sub"].(string), "全程 18 ms − 第一跳 9 ms") {
		t.Fatalf("第二跳 estimate: %v", h2)
	}
	if sum["totalMs"] != 18.0 || sum["hop1Ms"] != 9.0 || sum["hop2Ms"] != 9.0 || sum["totalSource"] != "udp" || sum["hop2Estimate"] != true || sum["totalLoss"] != 0.0 {
		t.Fatalf("summary: %v", sum)
	}
	if calls[60] != 0 || calls[61] != 0 {
		t.Fatalf("user run probed other entry hosts: %v", calls)
	}
	// Again within 10 s → refused before any probe.
	if again := call(2, 1, "/x", func(w *httptest.ResponseRecorder, req *http.Request) { h.leikwanRuleDiagnoseStream(w, req) }); !strings.Contains(again, "诊断太频繁") || calls[58] != 1 {
		t.Fatalf("rate limit: %s %v", again, calls)
	}

	// Admin, non-stream: every ingress with labels, other hosts (old agent → upgrade hint).
	body = call(1, 0, "/api/v1/leikwan-rule/diagnose", func(w *httptest.ResponseRecorder, req *http.Request) { h.leikwanRuleDiagnose(w, req) })
	var out struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	must(json.Unmarshal([]byte(body), &out))
	got := map[string][]map[string]interface{}{}
	for _, it := range out.Data.Items {
		got[it["section"].(string)] = append(got[it["section"].(string)], it)
	}
	if len(got["hop1"]) != 4 || got["hop1"][0]["title"] != "百度云广州 → 利群入口 ①" || !strings.Contains(got["hop1"][2]["sub"].(string), "华为云") ||
		!strings.Contains(got["hop1"][2]["sub"].(string), "121.37.28.219") || got["hop1"][3]["success"] != false || got["hop1"][3]["message"] != "连接超时" {
		t.Fatalf("admin 第一跳: %v", got["hop1"])
	}
	// Plan 301: only the chosen entry — no 「其他利群入口机」 rows, no probes there.
	if len(got["other"]) != 0 || calls[60] != 0 || calls[61] != 0 {
		t.Fatalf("other hosts diagnosed: %v %v", got["other"], calls)
	}
	if len(got["exit"]) != 0 || len(got["backup"]) != 0 {
		t.Fatalf("manual rule without 备线: %v", out.Data.Items)
	}
}

// Prod 11-17 (fwd 263): UDP rtt 60000 / 3018 without probe counters while the
// TCP selection said 64 ms → 全程 comes from TCP, never the placeholder value.
func TestLeiKwanDiagSource(t *testing.T) {
	cases := []struct {
		name     string
		tcp, udp *lkAgentClient
		src      string
		rtt      float64
	}{
		{"udp with probes", &lkAgentClient{RttMs: 12}, &lkAgentClient{RttMs: 10, Sent: 30, Recv: 30}, "udp", 10},
		{"udp placeholder 60000", &lkAgentClient{RttMs: 64}, &lkAgentClient{RttMs: 60000}, "tcp", 64},
		{"udp no probes 3018", &lkAgentClient{RttMs: 64}, &lkAgentClient{RttMs: 3018}, "tcp", 64},
		{"udp no probes, no tcp", nil, &lkAgentClient{RttMs: 40}, "udp", 40},
		{"udp probes all lost", &lkAgentClient{RttMs: 64}, &lkAgentClient{RttMs: 50, Sent: 30, Recv: 0}, "tcp", 64},
		{"nothing usable", &lkAgentClient{RttMs: 9000}, &lkAgentClient{RttMs: 60000}, "", 0},
	}
	for _, c := range cases {
		src, cl := lkDiagSource(&lkAgentDiag{TCP: c.tcp, UDP: c.udp})
		got := 0.0
		if cl != nil {
			got = cl.RttMs
		}
		if src != c.src || got != c.rtt {
			t.Errorf("%s: source %q rtt %v, want %q %v", c.name, src, got, c.src, c.rtt)
		}
	}
}

// Plan 301 诊断 v2: a native landing measures 专线出口 → 落地 (kernel rtt of the
// relay exit's connections, FLVX node IPs skipped), 专线入口 → 专线出口 =
// 全程 − 入口段 (plan 306 r2: 「端内」, a plain number; 全程 ends at the exit), whole path ≈ 全程 + 落地段; an old landing agent falls back to the
// old 专线 → 落地 estimate; 手机本地 takes the phone's numbers; an admin
// looking at another user's forward diagnoses it from the admin's own entry.
func TestLeiKwanDiagnoseV2(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{repo: r}
	db := r.DB()
	now := time.Now().UnixMilli()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.NodeGroup{ID: 5, Name: "利群入口", CreatedTime: now}).Error)
	for _, n := range []model.Node{{ID: 58, Name: "百度云广州", ServerIP: "106.13.17.23"}, {ID: 60, Name: "阿里广州", ServerIP: "8.134.172.154"}} {
		n.Secret, n.Port, n.CreatedTime, n.Status, n.GroupID = "s", "1-65535", now, 1, sql.NullInt64{Int64: 5, Valid: true}
		must(db.Create(&n).Error)
	}
	for _, n := range []model.Node{{ID: 62, Name: "Boil Hinet 马年", ServerIP: "203.0.113.62"}, {ID: 90, Name: "深港ixp1", ServerIP: "163.53.52.189"}} {
		n.Secret, n.Port, n.CreatedTime, n.Status = "s", "1-65535", now, 1
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LandingServer{ID: 14, Source: "native", NodeID: 62, Name: "马年", PublicHost: "203.0.113.62", Status: "active", CreatedTime: now, UpdatedTime: now}).Error)
	must(db.Create(&model.LandingNode{ID: 215, UserID: 2, LandingServerID: 14, Name: "利群280", Protocol: "ss2022", Port: 10017, InboundTag: "flvxl-215", Status: "active"}).Error)
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 3, AccountID: 1, RouteID: 29, Name: "利群·广台", RouteLabel: "CAN → TPE", Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 2, MainEntryNodeID: 60, Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 1, MainEntryNodeID: 58, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 8, UserID: 2, LineID: 3, ForwardID: 280, Name: "广台", ListenPort: 10020, LandingNodeID: 215, Status: 1}).Error)

	var mu sync.Mutex
	diagOn := map[int64]int{}
	oldLanding := false
	h.nodeCommandSender = func(node int64, cmd string, data interface{}, timeout time.Duration) (ws.CommandResult, error) {
		mu.Lock()
		defer mu.Unlock()
		switch cmd {
		case "LeiKwanDiag":
			diagOn[node]++
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "found": true, "state": "ready", "ingresses": []interface{}{
				map[string]interface{}{"endpoint": "125.94.47.233:5438", "inUse": "tcp,udp", "offered": true, "ok": true, "avgMs": 5.0, "loss": 0.0}},
				"udp": map[string]interface{}{"rttMs": 40.0, "sent": 30, "recv": 30, "ageS": 10}}}, nil
		case "LandingPeerRtt":
			if node != 62 || data.(map[string]interface{})["port"] != 10017 {
				t.Errorf("LandingPeerRtt on %d %v", node, data)
			}
			if oldLanding {
				return ws.CommandResult{Success: false, Message: "未知命令: LandingPeerRtt"}, nil
			}
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "total": 9, "peers": []interface{}{
				map[string]interface{}{"ip": "163.53.52.189", "conns": 6, "rttMs": 30.0, "minRttMs": 29.0}, // 备线 tunnel exit (FLVX node)
				map[string]interface{}{"ip": "118.194.253.206", "conns": 3, "rttMs": 7.5, "minRttMs": 6.0},
			}}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{}}, nil
	}
	call := func(uid int64, role int, body map[string]interface{}) (items map[string]map[string]interface{}, sum map[string]interface{}, path map[string]interface{}, raw string) {
		h.lkThrottle = lkThrottle{}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/leikwan-rule/diagnose", bytes.NewReader(b))
		tok, _ := auth.GenerateToken(uid, "u", role, "s")
		claims, _ := auth.ParseClaims(tok, "s")
		req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, claims))
		w := httptest.NewRecorder()
		h.leikwanRuleDiagnose(w, req)
		var out struct {
			Code int `json:"code"`
			Data struct {
				Items   []map[string]interface{} `json:"items"`
				Summary map[string]interface{}   `json:"summary"`
				Path    map[string]interface{}   `json:"path"`
			} `json:"data"`
		}
		must(json.Unmarshal(w.Body.Bytes(), &out))
		items = map[string]map[string]interface{}{}
		for _, it := range out.Data.Items {
			items[it["section"].(string)] = it
		}
		return items, out.Data.Summary, out.Data.Path, w.Body.String()
	}

	// User 2 (主入口 阿里广州): 5 + 6 measured, 40 − 5 = 35 = 端内 (plan 306 r2: not 估算), 46 end to end.
	items, sum, path, raw := call(2, 1, map[string]interface{}{"id": 8})
	if diagOn[60] != 1 || diagOn[58] != 0 {
		t.Fatalf("user entry: %v", diagOn)
	}
	if l := items["landing"]; l["success"] != true || l["averageTime"] != 6.0 || l["title"] != "专线出口 → 利群280" {
		t.Fatalf("landing hop: %v", l)
	}
	if v := items["vendor"]; v["estimate"] != nil || v["success"] != true || v["averageTime"] != 35.0 || !strings.Contains(v["sub"].(string), "全程 40 ms") || endInLabel(v) != "端内 35 ms" {
		t.Fatalf("vendor hop: %v", v)
	}
	if _, ok := items["hop2"]; ok {
		t.Fatalf("combined estimate next to a measured landing: %v", items)
	}
	if sum["landingMs"] != 6.0 || sum["vendorMs"] != 35.0 || sum["endToEndMs"] != 46.0 || sum["landingMeasured"] != true || sum["hop2Estimate"] != false || path["landingMeasurable"] != true {
		t.Fatalf("summary: %v %v", sum, path)
	}
	if strings.Contains(raw, "118.194.253.206") || strings.Contains(raw, "125.94.47.233") {
		t.Fatalf("user sees relay addresses: %s", raw)
	}

	// Admin (主入口 百度云广州) on user 2's forward: from the admin's entry; exit IP shown.
	items, _, path, _ = call(1, 0, map[string]interface{}{"id": 8})
	if diagOn[58] != 1 || path["entryName"] != "百度云广州" || !strings.Contains(items["landing"]["sub"].(string), "118.194.253.206") {
		t.Fatalf("admin entry / exit label: %v %v %v", diagOn, path, items["landing"])
	}

	// Old landing agent → the old 专线 → 落地 estimate (40 − 5), landing row 未测.
	oldLanding = true
	items, sum, _, _ = call(2, 1, map[string]interface{}{"id": 8})
	if h2 := items["hop2"]; h2["estimate"] != true || h2["averageTime"] != 35.0 {
		t.Fatalf("fallback estimate: %v", items)
	}
	if l := items["landing"]; l["skipped"] != true || !strings.Contains(l["message"].(string), "还没升级") {
		t.Fatalf("landing fallback row: %v", l)
	}
	if _, ok := items["vendor"]; ok || sum["hop2Ms"] != 35.0 {
		t.Fatalf("vendor row with an unmeasured landing: %v %v", items, sum)
	}
	oldLanding = false

	// 手机本地: users refused; admin gets the phone's numbers (no entry agent call).
	if _, _, _, raw := call(2, 1, map[string]interface{}{"id": 8, "local": true}); !strings.Contains(raw, "手机本地现在只给管理员用") {
		t.Fatalf("user local: %s", raw)
	}
	before := diagOn[58] + diagOn[60]
	items, sum, path, _ = call(1, 0, map[string]interface{}{"id": 8, "local": true, "phone": map[string]interface{}{
		"ingress": map[string]interface{}{"ok": true, "avgMs": 12.0, "loss": 0.0, "inUse": true},
		"total":   map[string]interface{}{"source": "tcp", "rttMs": 50.0, "ageS": 3},
		"device":  map[string]interface{}{"ok": true, "avgMs": 99.0, "count": 3}}})
	if diagOn[58]+diagOn[60] != before {
		t.Fatalf("手机本地 called an entry agent: %v", diagOn)
	}
	if h1 := items["hop1"]; h1["title"] != "手机 → 利群专线" || h1["averageTime"] != 12.0 {
		t.Fatalf("phone hop1: %v", h1)
	}
	if _, ok := items["device"]; ok || path["local"] != true || path["entryName"] != "手机本地" {
		t.Fatalf("手机本地 path: %v %v", items, path)
	}
	if sum["vendorMs"] != 38.0 || sum["totalSource"] != "tcp" {
		t.Fatalf("phone summary (50 − 12): %v", sum)
	}

	// App on a server entry: 本机 → 入口 from the phone.
	items, sum, path, _ = call(1, 0, map[string]interface{}{"id": 8, "phone": map[string]interface{}{"device": map[string]interface{}{"ok": true, "avgMs": 21.0, "loss": 0.0, "count": 3}}})
	if d := items["device"]; d["title"] != "本机 → 百度云广州" || d["averageTime"] != 21.0 || sum["deviceMs"] != 21.0 || path["device"] != true {
		t.Fatalf("device hop: %v %v", items["device"], sum)
	}
}
