package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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

// Plan 306: 专线出口 → 落地 = the landing's ping to the exit IP; the
// connection rtt − that ping (C − n) is part of the 专线, not added on top.
// Round 2 (user 11-25 night): 「端内」 = the whole 专线入口 → 专线出口 =
// max(全程 − 入口段, C − n), a plain number with the tag 「端内 X ms」 for every
// user and any value (one decimal under 10 ms), never 估算 / ≈ / 含端内;
// 到落地 = 入口段 + 端内 + ping. Shared landings: sources whose C − n is
// longer than this line's 端内 are another line's; several plausible ones →
// a range, never one wrong exit.

type lkEndInLab struct {
	t       *testing.T
	h       *Handler
	mu      sync.Mutex
	total   map[int64]float64 // forward → 全程 (udp)
	hop1    map[int64]float64 // forward → 入口段
	peers   []map[string]interface{}
	ping    map[string]float64 // exit IP → whole ms answered by the agent (absent = timeout)
	pings   int
	unknown bool // ServiceMonitorCheck unknown (never seen, but must not break)
}

func newLKEndInLab(t *testing.T) *lkEndInLab {
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
	for _, n := range []model.Node{{ID: 58, Name: "百度云广州", ServerIP: "106.13.17.23", GroupID: sql.NullInt64{Int64: 5, Valid: true}},
		{ID: 62, Name: "Boil Hinet 马年", ServerIP: "203.0.113.62"}, {ID: 47, Name: "AWS HK", ServerIP: "43.199.143.188"}} {
		n.Secret, n.Port, n.CreatedTime, n.Status = "s", "1-65535", now, 1
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LandingServer{ID: 14, Source: "native", NodeID: 62, Name: "马年", PublicHost: "203.0.113.62", Status: "active", CreatedTime: now, UpdatedTime: now}).Error)
	must(db.Create(&model.LandingNode{ID: 215, UserID: 1, LandingServerID: 14, Name: "SS2022 12311", Protocol: "ss2022", Port: 12311, InboundTag: "flvxl-215", Status: "active"}).Error)
	must(db.Create(&model.LandingNode{ID: 216, UserID: 2, LandingServerID: 14, Name: "利群单独", Protocol: "ss2022", Port: 12312, InboundTag: "flvxl-216", Status: "active"}).Error)
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 3, AccountID: 1, RouteID: 29, Name: "利群·广台", RouteLabel: "CAN → TPE", Status: 1}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 4, AccountID: 1, RouteID: 30, Name: "利群·福台", RouteLabel: "FOC → TPE", Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 1, MainEntryNodeID: 58, Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 2, MainEntryNodeID: 58, Status: 1}).Error)
	// 4 + 6 share landing 215 (like prod 280 + 282 on 马年 :12311); 9 is alone on 216.
	must(db.Create(&model.LeiKwanRule{ID: 4, UserID: 1, LineID: 3, ForwardID: 280, Name: "🇹🇼 利群广台 Hinet", ListenPort: 10020, LandingNodeID: 215, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 6, UserID: 1, LineID: 4, ForwardID: 282, Name: "🇹🇼 利群福台 Hinet", ListenPort: 10017, LandingNodeID: 215, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 9, UserID: 2, LineID: 3, ForwardID: 290, Name: "广台单独", ListenPort: 10030, LandingNodeID: 216, Status: 1}).Error)
	lab := &lkEndInLab{t: t, h: h, total: map[int64]float64{280: 22, 282: 35, 290: 22}, hop1: map[int64]float64{280: 3, 282: 24, 290: 3}}
	h.nodeCommandSender = func(node int64, cmd string, data interface{}, timeout time.Duration) (ws.CommandResult, error) {
		lab.mu.Lock()
		defer lab.mu.Unlock()
		m, _ := data.(map[string]interface{})
		switch cmd {
		case "LeiKwanDiag":
			fid := m["forwardId"].(int64)
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "found": true, "state": "ready", "ingresses": []interface{}{
				map[string]interface{}{"endpoint": "106.75.166.130:5438", "inUse": "tcp+udp", "offered": true, "ok": true, "avgMs": lab.hop1[fid], "loss": 0.0}},
				"udp": map[string]interface{}{"rttMs": lab.total[fid], "sent": 30, "recv": 30, "ageS": 10}}}, nil
		case "LandingPeerRtt":
			if node != 62 {
				t.Errorf("LandingPeerRtt on node %d", node)
			}
			peers := make([]interface{}, len(lab.peers))
			for i, p := range lab.peers {
				peers[i] = p
			}
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "total": 9, "peers": peers}}, nil
		case "TcpPing": // 出口测试 (not under test): answer at once
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"ip": m["ip"], "port": m["port"], "success": true, "averageTime": 3.0, "packetLoss": 0.0}}, nil
		case "ServiceMonitorCheck":
			lab.pings++
			if node != 62 || m["type"] != "icmp" {
				t.Errorf("ping from node %d: %v", node, m)
			}
			if lab.unknown {
				return ws.CommandResult{Success: false, Message: "未知命令: ServiceMonitorCheck"}, nil
			}
			v, ok := lab.ping[m["target"].(string)]
			if !ok {
				return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": false, "latencyMs": 2000.0, "errorMessage": "timeout"}}, nil
			}
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "latencyMs": v}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{}}, nil
	}
	return lab
}

type lkDiagOut struct {
	rows []map[string]interface{}
	by   map[string]map[string]interface{} // first row per section
	sum  map[string]interface{}
	raw  string
}

func (l *lkEndInLab) diag(uid int64, role int, id int64) lkDiagOut {
	l.t.Helper()
	l.h.lkThrottle = lkThrottle{}
	b, _ := json.Marshal(map[string]interface{}{"id": id})
	req := httptest.NewRequest("POST", "/api/v1/leikwan-rule/diagnose", bytes.NewReader(b))
	tok, _ := auth.GenerateToken(uid, "u", role, "s")
	claims, _ := auth.ParseClaims(tok, "s")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, claims))
	w := httptest.NewRecorder()
	l.h.leikwanRuleDiagnose(w, req)
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items   []map[string]interface{} `json:"items"`
			Summary map[string]interface{}   `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 {
		l.t.Fatalf("diagnose: %v %s", err, w.Body.String())
	}
	o := lkDiagOut{rows: out.Data.Items, by: map[string]map[string]interface{}{}, sum: out.Data.Summary, raw: w.Body.String()}
	for _, it := range out.Data.Items {
		if _, ok := o.by[it["section"].(string)]; !ok {
			o.by[it["section"].(string)] = it
		}
	}
	return o
}

func peer(ip string, conns int, rtt, minRtt float64) map[string]interface{} {
	return map[string]interface{}{"ip": ip, "conns": conns, "rttMs": rtt, "minRttMs": minRtt}
}

func endInLabel(it map[string]interface{}) string {
	if e, ok := it["endIn"].(map[string]interface{}); ok {
		return e["label"].(string)
	}
	return ""
}

// noRoughWords: the 端内 / 出口 → 落地 rows never say 估算 / ≈ / 含端内 (round 2).
func noRoughWords(t *testing.T, o lkDiagOut) {
	t.Helper()
	for _, it := range o.rows {
		if it["section"] != "vendor" && it["section"] != "landing" {
			continue
		}
		if it["estimate"] == true {
			t.Fatalf("%s row is 估算: %v", it["section"], it)
		}
		for _, k := range []string{"title", "sub", "message"} {
			v, _ := it[k].(string)
			if strings.Contains(v, "估算") || strings.Contains(v, "≈") || strings.Contains(v, "含端内") {
				t.Fatalf("%s row %s = %q", it["section"], k, v)
			}
		}
		if l := endInLabel(it); strings.Contains(l, "≈") || strings.Contains(l, "含") {
			t.Fatalf("tag %q", l)
		}
	}
}

func TestLeiKwanDiagEndIn(t *testing.T) {
	l := newLKEndInLab(t)

	// 广台-like, landing used by one rule: conn 24 vs ping 6 (whole ms → 6.5):
	// C − n = 17.5 is inside the 专线; 端内 = 22 − 3 = 19, 到落地 = 3 + 19 + 6.5
	// (not 22 + 24). A normal user sees the tag.
	l.peers = []map[string]interface{}{peer("43.199.143.188", 6, 18, 17.5), peer("210.61.104.139", 4, 25, 24)}
	l.ping = map[string]float64{"210.61.104.139": 6}
	o := l.diag(2, 1, 9)
	noRoughWords(t, o)
	if r := o.by["landing"]; r["success"] != true || r["averageTime"] != 6.5 || r["sub"] != "落地机实测（ping 专线出口）" {
		t.Fatalf("landing row: %v", r)
	}
	if v := o.by["vendor"]; v["averageTime"] != 19.0 || v["success"] != true || v["estimate"] != nil || endInLabel(v) != "端内 19 ms" || v["sub"] != "全程 22 ms − 入口段 3 ms" {
		t.Fatalf("vendor row: %v", v)
	}
	if o.sum["landingMs"] != 6.5 || o.sum["endInMs"] != 19.0 || o.sum["vendorMs"] != 19.0 || o.sum["endToEndMs"] != 28.5 || o.sum["landingMaxMs"] != nil || o.sum["landingPing"] != true {
		t.Fatalf("summary: %v", o.sum)
	}
	if e, _ := o.sum["endIn"].(map[string]interface{}); e == nil || e["label"] != "端内 19 ms" {
		t.Fatalf("summary tag: %v", o.sum)
	}
	if strings.Contains(o.raw, "210.61.104.139") || l.pings != 3 {
		t.Fatalf("user sees the exit IP / pings %d: %s", l.pings, o.raw)
	}
	// Admin: exit IP, ping and conn in the sub (no 「含端内」 any more).
	o = l.diag(1, 0, 9)
	noRoughWords(t, o)
	if s := o.by["landing"]["sub"].(string); !strings.Contains(s, "210.61.104.139 · ping 6.5 ms · 连接 24 ms · 4 条连接") {
		t.Fatalf("admin landing sub: %s", s)
	}

	// Tiny 端内 (福台-like: 入口段 21.6 of 全程 22): every user sees 「端内 0.4 ms」.
	l.peers = []map[string]interface{}{peer("202.160.77.45", 4, 11, 10.4)}
	l.ping = map[string]float64{"202.160.77.45": 10}
	l.hop1[290] = 21.6
	for uid, role := range map[int64]int{2: 1, 1: 0} {
		o = l.diag(uid, role, 9)
		noRoughWords(t, o)
		if v := o.by["vendor"]; endInLabel(v) != "端内 0.4 ms" || v["averageTime"] != 0.4 || v["success"] != true {
			t.Fatalf("tiny 端内 role %d: %v", role, v)
		}
		if o.sum["endToEndMs"] != 32.5 || o.by["landing"]["averageTime"] != 10.5 {
			t.Fatalf("tiny 端内 sum (21.6 + 0.4 + 10.5): %v", o.sum)
		}
	}
	// One decimal under 10 ms, whole above; 入口段 longer than 全程 → 0.
	for hop, want := range map[float64]string{17.34: "端内 4.7 ms", 12.04: "端内 10 ms", 23: "端内 0 ms", 13: "端内 9 ms"} {
		l.hop1[290] = hop
		if o = l.diag(2, 1, 9); endInLabel(o.by["vendor"]) != want {
			t.Fatalf("入口段 %v: %q want %q", hop, endInLabel(o.by["vendor"]), want)
		}
	}
	l.hop1[290] = 3

	// C − n longer than the vendor probe's 专线 (40 − 5.5 = 34.5 > 19): the TCP's own number is the 端内.
	l.peers = []map[string]interface{}{peer("103.167.50.69", 6, 41, 40)}
	l.ping = map[string]float64{"103.167.50.69": 5}
	o = l.diag(2, 1, 9)
	noRoughWords(t, o)
	if v := o.by["vendor"]; v["averageTime"] != 34.5 || !strings.Contains(v["sub"].(string), "按 35 ms 算") || endInLabel(v) != "端内 35 ms" || o.sum["endToEndMs"] != 43.0 {
		t.Fatalf("long 端内: %v %v", v, o.sum)
	}

	// The exit does not answer ping → today's numbers (conn rtt, 22 + 24); 端内 still shown.
	l.peers = []map[string]interface{}{peer("210.61.104.139", 4, 25, 24)}
	l.ping = map[string]float64{}
	o = l.diag(2, 1, 9)
	noRoughWords(t, o)
	if r := o.by["landing"]; r["averageTime"] != 24.0 || !strings.Contains(r["sub"].(string), "出口不回 ping") || o.sum["endToEndMs"] != 46.0 || o.sum["endInMs"] != 19.0 || o.sum["vendorMs"] != 19.0 || o.sum["landingPing"] != nil {
		t.Fatalf("no ping: %v %v", r, o.sum)
	}
	if endInLabel(o.by["vendor"]) != "端内 19 ms" {
		t.Fatalf("no ping tag: %v", o.by["vendor"])
	}
	// An agent without the command behaves the same (and is asked once only).
	l.unknown, l.pings = true, 0
	if o = l.diag(2, 1, 9); o.sum["endToEndMs"] != 46.0 || l.pings != 1 {
		t.Fatalf("unknown ping command: %v pings %d", o.sum, l.pings)
	}
	l.unknown = false

	// No 全程 yet: the 端内 row says why, without 估算.
	delete(l.total, 290)
	o = l.diag(2, 1, 9)
	noRoughWords(t, o)
	if v := o.by["vendor"]; v["skipped"] != true || v["message"] != "利群客户端还没有全程数据，算不出端内" || endInLabel(v) != "" {
		t.Fatalf("no 全程: %v", v)
	}
}

func TestLeiKwanDiagSharedLanding(t *testing.T) {
	l := newLKEndInLab(t)
	// 马年 :12311 carries 广台 Hinet (rule 4) and 福台 Hinet (rule 6): a front-type
	// source (conn 23, ping 5.5 → 端内 17.5), a relay-type one (10.4 vs 10.5) and
	// the 备线 tunnel exit (FLVX node, never a candidate).
	l.peers = []map[string]interface{}{peer("43.199.143.188", 6, 18, 17.5), peer("118.194.253.199", 4, 23.5, 23), peer("202.160.77.45", 4, 9, 10.4)}
	l.ping = map[string]float64{"118.194.253.199": 5, "202.160.77.45": 10, "43.199.143.188": 17}

	// 福台 (全程 35 − 入口段 24 = 端内 11): the source with C − n = 17.5 is a longer line's → the relay one.
	o := l.diag(1, 0, 6)
	noRoughWords(t, o)
	if r := o.by["landing"]; r["averageTime"] != 10.5 || r["ambiguous"] != nil || !strings.Contains(r["sub"].(string), "202.160.77.45") || !strings.Contains(r["sub"].(string), "排除了 1 个别的线路的来源") || !strings.Contains(r["sub"].(string), "利群广台 Hinet") {
		t.Fatalf("福台 on the shared landing: %v", r)
	}
	if o.sum["endToEndMs"] != 45.5 || o.sum["landingAmbiguous"] != nil || endInLabel(o.by["vendor"]) != "端内 11 ms" {
		t.Fatalf("福台 summary (24 + 11 + 10.5): %v", o.sum)
	}

	// 广台 (22 − 3 = 19): both sources fit and disagree → a range, admin rows per source.
	l.pings = 0
	o = l.diag(1, 0, 4)
	if l.pings != 6 {
		t.Fatalf("pinged %d times (2 sources × 3)", l.pings)
	}
	noRoughWords(t, o)
	r := o.by["landing"]
	if r["ambiguous"] != true || r["averageTime"] != 5.5 || r["averageMaxMs"] != 10.5 || r["sub"] != "这台落地机还接着「🇹🇼 利群福台 Hinet」，分不清是哪个出口，显示范围" {
		t.Fatalf("广台 range row: %v", r)
	}
	if o.sum["landingMs"] != 5.5 || o.sum["landingMaxMs"] != 10.5 || o.sum["endInMs"] != 19.0 || o.sum["endInMaxMs"] != nil ||
		o.sum["endToEndMs"] != 27.5 || o.sum["endToEndMaxMs"] != 32.5 || o.sum["vendorMs"] != 19.0 || o.sum["vendorMaxMs"] != nil || o.sum["landingAmbiguous"] != true {
		t.Fatalf("广台 range summary: %v", o.sum)
	}
	if endInLabel(o.by["vendor"]) != "端内 19 ms" {
		t.Fatalf("range tag: %v", o.by["vendor"])
	}
	n := 0
	for _, it := range o.rows {
		if title := it["title"].(string); it["section"] == "landing" && (strings.Contains(title, "①") || strings.Contains(title, "②")) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("admin source rows: %v", o.rows)
	}
	// A user: the range, no IPs, no other rule's name.
	o = l.diag(1, 1, 4)
	noRoughWords(t, o)
	if strings.Contains(o.raw, "118.194.253.199") || strings.Contains(o.raw, "福台 Hinet") || o.by["landing"]["sub"] != "这台落地机还接了别的利群线路，分不清是哪个出口，显示范围" {
		t.Fatalf("user view leaks: %s", o.raw)
	}
	if endInLabel(o.by["vendor"]) != "端内 19 ms" {
		t.Fatalf("user range tag: %v", o.by["vendor"])
	}

	// One source only on a shared landing: used, but flagged as maybe the other line's.
	l.peers = []map[string]interface{}{peer("202.160.77.45", 4, 9, 10.4)}
	o = l.diag(1, 1, 4)
	if r := o.by["landing"]; r["averageTime"] != 10.5 || r["sub"] != "落地机 ping 专线出口；这台落地机还接了别的利群线路，不一定是这条线的出口" {
		t.Fatalf("alone: %v", r)
	}

	// No source fits (福台's 端内 11, every source C − n ≥ 17): no wrong exit — the old estimate.
	l.peers = []map[string]interface{}{peer("118.194.253.199", 4, 23.5, 23), peer("210.61.104.176", 4, 23, 22.7)}
	l.ping = map[string]float64{"118.194.253.199": 5, "210.61.104.176": 5}
	o = l.diag(1, 0, 6)
	if r := o.by["landing"]; r["skipped"] != true || !strings.Contains(r["message"].(string), "都不像这条线的出口") {
		t.Fatalf("none fits landing row: %v", o.rows)
	}
	if h2 := o.by["hop2"]; h2 == nil || h2["averageTime"] != 11.0 || o.sum["landingMeasured"] != nil {
		t.Fatalf("none fits → 专线 → 落地 estimate: %v %v", o.rows, o.sum)
	}
}

func TestLeiKwanPickExitFor(t *testing.T) {
	c := func(ip string, conn, ping float64) lkExitCand {
		return lkExitCand{lkLandingPeer: lkLandingPeer{IP: ip, Conns: 1, MinRttMs: conn}, PingMs: ping}
	}
	// Not shared: always the busiest (first), even when another would "fit" better.
	if p := lkPickExitFor([]lkExitCand{c("a", 40, 21.5), c("b", 10, 10)}, false, 19); len(p.sel) != 1 || p.sel[0].IP != "a" || p.noPing {
		t.Fatalf("not shared: %+v", p)
	}
	if p := lkPickExitFor([]lkExitCand{c("a", 40, -1)}, false, 19); !p.noPing {
		t.Fatalf("not shared, no ping: %+v", p)
	}
	// Shared: agreeing sources (within 3 ms) → one answer.
	if p := lkPickExitFor([]lkExitCand{c("a", 10.4, 9.5), c("b", 9.9, 10.5)}, true, 11); len(p.sel) != 1 || p.alone {
		t.Fatalf("agree: %+v", p)
	}
	// Shared, 全程 unknown: nothing can be dropped.
	if p := lkPickExitFor([]lkExitCand{c("a", 23, 5.5), c("b", 10.4, 10.5)}, true, -1); len(p.sel) != 2 || p.dropped != 0 {
		t.Fatalf("unknown 全程: %+v", p)
	}
	// Shared, none answered ping → the old numbers on the busiest.
	if p := lkPickExitFor([]lkExitCand{c("a", 23, -1), c("b", 10.4, -1)}, true, 19); !p.noPing || p.sel[0].IP != "a" {
		t.Fatalf("shared no ping: %+v", p)
	}
	// C − n longer than the line's 端内 + 5 ms → another line's.
	if p := lkPickExitFor([]lkExitCand{c("a", 23, 5.5), c("b", 10.4, 10.5)}, true, 11); len(p.sel) != 1 || p.sel[0].IP != "b" || p.dropped != 1 {
		t.Fatalf("drop: %+v", p)
	}
}
