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

// Plan 272 诊断: admin sees every ingress of the 主入口 (+ the other 利群入口
// hosts), users one 第一跳 row without relay IPs; 第二跳 is always an
// estimate (全程 − 第一跳); an old agent / a dead ingress show as failed rows;
// the stream ends with the authoritative list; one diagnose per user per 10 s.
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
	if len(got["other"]) != 2 {
		t.Fatalf("other hosts: %v", got["other"])
	}
	for _, o := range got["other"] {
		switch {
		case strings.HasPrefix(o["title"].(string), "阿里广州"):
			if o["success"] != true || o["averageTime"] != 9.0 {
				t.Fatalf("阿里广州 row: %v", o)
			}
		case strings.HasPrefix(o["title"].(string), "火山上海"):
			if o["success"] != false || o["message"] != "入口机 agent 需要升级才能诊断" {
				t.Fatalf("火山上海 row: %v", o)
			}
		default:
			t.Fatalf("other row: %v", o)
		}
	}
	if len(got["exit"]) != 0 || len(got["backup"]) != 0 {
		t.Fatalf("manual rule without 备线: %v", out.Data.Items)
	}
}
