package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

type lineMonEnv struct {
	*flowTestEnv
}

func newLineMonEnv(t *testing.T) *lineMonEnv { return newLineMonEnvDialect(t, "sqlite") }

// newLineMonEnvDialect: SQLite, or PostgreSQL with FLVX_POSTGRES_TEST_DSN
// (fresh schema; skipped without the DSN).
func newLineMonEnvDialect(t *testing.T, dialect string) *lineMonEnv {
	t.Helper()
	r := landingMonitorRepo(t, dialect)
	e := &lineMonEnv{flowTestEnv: &flowTestEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}}
	for _, n := range []struct {
		id         int64
		name, addr string
		remote     int
	}{
		{1, "AWS HK", "198.51.100.1", 0},
		{2, "Fachost seednet 黑五", "198.51.100.2", 0},
		{3, "Tcscloud Seednet", "seednet.example.test", 0},
		{4, "Shared Remote", "198.51.100.4", 1},
	} {
		e.exec(`INSERT INTO node(id, name, secret, server_ip, server_ip_v4, port, created_time, status, is_remote) VALUES(?, ?, ?, ?, ?, '1000-2000', ?, 1, ?)`,
			n.id, n.name, "s"+n.name, n.addr, n.addr, e.now, n.remote)
	}
	return e
}

// lineMonDialects runs fn on SQLite and (with the DSN) PostgreSQL.
func lineMonDialects(t *testing.T, fn func(t *testing.T, e *lineMonEnv)) {
	for _, d := range []string{"sqlite", "postgres"} {
		t.Run(d, func(t *testing.T) { fn(t, newLineMonEnvDialect(t, d)) })
	}
}

func lineMonCall(t *testing.T, fn http.HandlerFunc, role int, body interface{}) (int, string, json.RawMessage) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/node/line-monitor/x", bytes.NewReader(raw))
	if role >= 0 {
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: role}))
	}
	w := httptest.NewRecorder()
	fn(w, r)
	var out struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return out.Code, out.Msg, out.Data
}

func (e *lineMonEnv) save(t *testing.T, body map[string]interface{}) (int64, []string) {
	t.Helper()
	code, msg, data := lineMonCall(t, e.h.lineMonitorChartSave, 0, body)
	if code != 0 {
		t.Fatalf("save %v: %d %s", body, code, msg)
	}
	var out struct {
		ID       int64    `json:"id"`
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(data, &out)
	return out.ID, out.Warnings
}

func lmLine(src int64, target int64, host string, port int) map[string]interface{} {
	m := map[string]interface{}{"sourceNodeId": src, "port": port}
	if target > 0 {
		m["targetType"], m["targetNodeId"] = "node", target
	} else {
		m["targetType"] = "custom"
	}
	if host != "" {
		m["host"] = host
	}
	return m
}

func TestLineMonitorAdminOnly(t *testing.T) {
	e := newLineMonEnv(t)
	for name, fn := range map[string]http.HandlerFunc{
		"list": e.h.lineMonitorList, "options": e.h.lineMonitorOptions, "ports": e.h.lineMonitorPorts,
		"save": e.h.lineMonitorChartSave, "delete": e.h.lineMonitorChartDelete, "reorder": e.h.lineMonitorChartReorder,
		"pause": e.h.lineMonitorLinePause, "data": e.h.lineMonitorDataHandler,
	} {
		if code, _, _ := lineMonCall(t, fn, 1, map[string]interface{}{"id": 1, "chartId": 1}); code != 403 {
			t.Fatalf("%s as user: code %d, want 403", name, code)
		}
		if code, _, _ := lineMonCall(t, fn, -1, map[string]interface{}{}); code != 401 {
			t.Fatalf("%s anonymous: code %d, want 401", name, code)
		}
	}
	// The JWT middleware also guards the whole prefix.
	if !strings.HasPrefix("/api/v1/node/line-monitor/data", "/api/v1/node/") {
		t.Fatal("routes must stay under the admin-only /api/v1/node/ prefix")
	}
}

func TestLineMonitorChartCRUDCapsAndPortHint(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		id, warn := e.save(t, map[string]interface{}{"name": "AWS HK → Seednet", "lines": []interface{}{
			lmLine(1, 2, "", 30001), lmLine(1, 3, "", 30002),
		}})
		if id <= 0 || len(warn) != 0 {
			t.Fatalf("create id %d warnings %v", id, warn)
		}
		_, _, data := lineMonCall(t, e.h.lineMonitorList, 0, map[string]interface{}{})
		var list struct {
			Charts []struct {
				ID    int64  `json:"id"`
				Name  string `json:"name"`
				Lines []struct {
					ID            int64  `json:"id"`
					Color         int    `json:"color"`
					SourceName    string `json:"sourceName"`
					TargetName    string `json:"targetName"`
					EffectiveHost string `json:"effectiveHost"`
					Host          string `json:"host"`
				} `json:"lines"`
			} `json:"charts"`
			TotalLines int `json:"totalLines"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Charts) != 1 || len(list.Charts[0].Lines) != 2 || list.TotalLines != 2 {
			t.Fatalf("list %+v", list)
		}
		l0, l1 := list.Charts[0].Lines[0], list.Charts[0].Lines[1]
		if l0.SourceName != "AWS HK" || l0.TargetName != "Fachost seednet 黑五" || l0.Host != "" || l0.EffectiveHost != "198.51.100.2" || l0.Color != 0 || l1.Color != 1 {
			t.Fatalf("line views %+v %+v", l0, l1)
		}
		if l1.EffectiveHost != "seednet.example.test" {
			t.Fatalf("follow-node host %q", l1.EffectiveHost)
		}

		// Edit: rename, drop line 0, keep line 1 (colour 1 stays), add a custom
		// HiNet target on port 22 → saved with the SSH hint, not refused.
		if err := e.r.InsertLineMonitorSamples([]model.LineMonitorSample{{LineID: l0.ID, TS: 100, Status: 1, Ms10: 50}, {LineID: l0.ID, TS: 130, Status: 3}}); err != nil {
			t.Fatal(err)
		}
		_, warn = e.save(t, map[string]interface{}{"id": id, "name": "AWS HK ⇄ Seednet", "lines": []interface{}{
			map[string]interface{}{"id": l1.ID, "sourceNodeId": 1, "targetType": "node", "targetNodeId": 3, "port": 30002},
			lmLine(2, 0, "hinet.example.test", 22),
		}})
		if len(warn) != 1 || !strings.Contains(warn[0], "fail2ban") {
			t.Fatalf("port 22 warnings %v", warn)
		}
		lines, _ := e.r.ListLineMonitorChartLines(id)
		if len(lines) != 2 || lines[0].ID != l1.ID || lines[0].Color != 1 || lines[1].Color != 0 || lines[1].Port != 22 || lines[1].TargetNodeID != 0 {
			t.Fatalf("after edit %+v", lines)
		}
		e.h.lineMonitorCleanupWG.Wait()
		var left int64
		e.r.DB().Model(&model.LineMonitorSample{}).Where("line_id = ?", l0.ID).Count(&left)
		if left != 0 {
			t.Fatalf("samples of the removed line left: %d", left)
		}

		// Caps: 9 lines in one chart, 41 in total.
		nine := make([]interface{}, 0, 9)
		for i := 0; i < 9; i++ {
			nine = append(nine, lmLine(1, 0, "203.0.113.9", 40000+i))
		}
		if code, msg, _ := lineMonCall(t, e.h.lineMonitorChartSave, 0, map[string]interface{}{"name": "x", "lines": nine}); code == 0 || !strings.Contains(msg, "最多 8") {
			t.Fatalf("9 lines: %d %s", code, msg)
		}
		for c := 0; c < 4; c++ { // 2 + 4×8 = 34
			ls := make([]interface{}, 0, 8)
			for i := 0; i < 8; i++ {
				ls = append(ls, lmLine(1, 0, "203.0.113.9", 41000+c*10+i))
			}
			e.save(t, map[string]interface{}{"name": "fill", "lines": ls})
		}
		six := make([]interface{}, 0, 6)
		for i := 0; i < 6; i++ {
			six = append(six, lmLine(2, 0, "203.0.113.10", 42000+i))
		}
		e.save(t, map[string]interface{}{"name": "forty", "lines": six}) // = 40
		if code, msg, _ := lineMonCall(t, e.h.lineMonitorChartSave, 0, map[string]interface{}{"name": "41st", "lines": []interface{}{lmLine(2, 0, "203.0.113.11", 1)}}); code == 0 || !strings.Contains(msg, "40") {
			t.Fatalf("41st line: %d %s", code, msg)
		}
		var total int64
		e.r.DB().Model(&model.LineMonitorLine{}).Count(&total)
		if total != 40 {
			t.Fatalf("total lines %d after the refused save (transaction must roll back)", total)
		}

		// Delete the chart: its lines go.
		if code, msg, _ := lineMonCall(t, e.h.lineMonitorChartDelete, 0, map[string]interface{}{"id": id}); code != 0 {
			t.Fatalf("delete %d %s", code, msg)
		}
		if ls, _ := e.r.ListLineMonitorChartLines(id); len(ls) != 0 {
			t.Fatalf("lines left %v", ls)
		}
	})
}

func TestLineMonitorSaveValidation(t *testing.T) {
	e := newLineMonEnv(t)
	for _, c := range []struct {
		lines []interface{}
		want  string
	}{
		{[]interface{}{}, "至少"},
		{[]interface{}{lmLine(9, 2, "", 443)}, "源节点"},
		{[]interface{}{lmLine(4, 2, "", 443)}, "共享节点"},
		{[]interface{}{lmLine(1, 1, "", 443)}, "不能相同"},
		{[]interface{}{lmLine(1, 0, "", 443)}, "目标"},
		{[]interface{}{lmLine(1, 0, "http://x.test/", 443)}, "格式"},
		{[]interface{}{lmLine(1, 0, "1.2.3.4:80", 443)}, "格式"},
		{[]interface{}{lmLine(1, 0, "1.2.3.4", 0)}, "端口"},
		{[]interface{}{lmLine(1, 0, "1.2.3.4", 70000)}, "端口"},
		{[]interface{}{lmLine(1, 0, "1.2.3.4", 80), lmLine(1, 0, "1.2.3.4", 80)}, "重复"},
	} {
		code, msg, _ := lineMonCall(t, e.h.lineMonitorChartSave, 0, map[string]interface{}{"name": "v", "lines": c.lines})
		if code == 0 || !strings.Contains(msg, c.want) {
			t.Fatalf("%v: code %d msg %q, want %q", c.lines, code, msg, c.want)
		}
	}
	if code, msg, _ := lineMonCall(t, e.h.lineMonitorChartSave, 0, map[string]interface{}{"name": "", "lines": []interface{}{lmLine(1, 2, "", 443)}}); code == 0 || !strings.Contains(msg, "名称") {
		t.Fatalf("empty name: %d %s", code, msg)
	}
	// IPv6 literal and names are fine.
	e.save(t, map[string]interface{}{"name": "ok", "lines": []interface{}{lmLine(1, 0, "[2001:db8::1]", 443), lmLine(1, 0, "hinet-box.example.test", 8443)}})
}

func TestLineMonitorPortHintsNeverSSH(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		e.addTunnel(30, 1, 1)
		e.exec(`UPDATE tunnel SET name = 'AWS HK → 黑五' WHERE id = 30`)
		e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol, port) VALUES(30, '1', 1, 1, 'tls', NULL), (30, '3', 2, 1, 'tls', 10044), (30, '3', 2, 2, 'tls', 22)`)
		e.addForward(50, 7, 30) // user row not needed (no FK)
		e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(50, 2, 12345)`)
		code, _, data := lineMonCall(t, e.h.lineMonitorPorts, 0, map[string]interface{}{"nodeId": 2})
		var hints []struct {
			Port  int    `json:"port"`
			Kind  string `json:"kind"`
			Label string `json:"label"`
		}
		_ = json.Unmarshal(data, &hints)
		if code != 0 || len(hints) != 2 || hints[0].Port != 10044 || hints[0].Kind != "tunnel" || hints[1].Port != 12345 || hints[1].Kind != "rule" {
			t.Fatalf("hints %d %+v", code, hints)
		}
	})
}

// fakeTcpPing answers TcpPing per target port: 1 ok, 2 first attempt of each
// slot fails, 3 refused, 4 channel timeout.
type fakeTcpPing struct {
	mu    sync.Mutex
	calls map[int]int
	sent  map[int64]int
}

func (f *fakeTcpPing) send(nodeID int64, typ string, data interface{}, timeout time.Duration) (ws.CommandResult, error) {
	m := data.(map[string]interface{})
	port := m["port"].(int)
	f.mu.Lock()
	if f.calls == nil {
		f.calls, f.sent = map[int]int{}, map[int64]int{}
	}
	f.calls[port]++
	f.sent[nodeID]++
	n := f.calls[port]
	f.mu.Unlock()
	if typ != "TcpPing" || m["count"] != 1 || m["timeout"] != linemon.PingTimeoutMS || timeout != lineMonitorCommandTimeout {
		return ws.CommandResult{}, nil
	}
	ok := func(ms float64) (ws.CommandResult, error) {
		return ws.CommandResult{Data: map[string]interface{}{"success": true, "averageTime": ms}}, nil
	}
	fail := ws.CommandResult{Data: map[string]interface{}{"success": false, "averageTime": float64(0), "errorMessage": "所有TCP连接尝试都失败"}}
	switch port {
	case 1001:
		return ok(21.34)
	case 1002:
		if n%2 == 1 {
			return fail, nil
		}
		return ok(48.0)
	case 1003:
		return fail, nil
	case 1004:
		return ws.CommandResult{}, context.DeadlineExceeded
	}
	return ws.CommandResult{}, nil
}

func TestLineMonitorSchedulerWritesSamples(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		fake := &fakeTcpPing{}
		e.h.nodeCommandSender = fake.send
		offline := map[int64]bool{3: true}
		e.h.nodeOnlineFn = func(id int64) bool { return !offline[id] }
		e.h.lineMonitor.testSlot, e.h.lineMonitor.testRetry = 1*time.Second, 100*time.Millisecond
		chart, _ := e.save(t, map[string]interface{}{"name": "lab", "lines": []interface{}{
			lmLine(1, 0, "203.0.113.1", 1001), lmLine(1, 0, "203.0.113.1", 1002), lmLine(1, 0, "203.0.113.1", 1003),
			lmLine(1, 0, "203.0.113.1", 1004), lmLine(3, 0, "203.0.113.1", 1001),
		}})
		paused, _ := e.save(t, map[string]interface{}{"name": "paused", "lines": []interface{}{lmLine(2, 0, "203.0.113.2", 1001)}})
		pl, _ := e.r.ListLineMonitorChartLines(paused)
		if code, _, _ := lineMonCall(t, e.h.lineMonitorLinePause, 0, map[string]interface{}{"id": pl[0].ID, "paused": true}); code != 0 {
			t.Fatal("pause")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3200*time.Millisecond)
		defer cancel()
		e.h.jobsWG.Add(1)
		e.h.runLineMonitorLoop(ctx) // returns at ctx end after the final flush
		lines, _ := e.r.ListLineMonitorChartLines(chart)
		statuses := func(id int64) map[int]int {
			var rows []model.LineMonitorSample
			e.r.DB().Where("line_id = ?", id).Find(&rows)
			out := map[int]int{}
			for _, r := range rows {
				out[r.Status]++
				if r.Status == linemon.StatusOK && r.Ms10 != 213 {
					t.Errorf("ok ms10 %d", r.Ms10)
				}
				if r.Status == linemon.StatusLoss && r.Ms10 != 480 {
					t.Errorf("loss ms10 %d", r.Ms10)
				}
			}
			return out
		}
		if s := statuses(lines[0].ID); s[linemon.StatusOK] < 2 || len(s) != 1 {
			t.Fatalf("ok line %v", s)
		}
		if s := statuses(lines[1].ID); s[linemon.StatusLoss] < 2 || len(s) != 1 {
			t.Fatalf("loss line %v", s)
		}
		if s := statuses(lines[2].ID); s[linemon.StatusDown] < 2 || len(s) != 1 {
			t.Fatalf("down line %v", s)
		}
		if s := statuses(lines[3].ID); len(s) != 0 {
			t.Fatalf("command-timeout line must have no samples (gap): %v", s)
		}
		if s := statuses(lines[4].ID); len(s) != 0 {
			t.Fatalf("offline-source line must have no samples (gap): %v", s)
		}
		if s := statuses(pl[0].ID); len(s) != 0 {
			t.Fatalf("paused line measured: %v", s)
		}
		fake.mu.Lock()
		sentOffline, sentPaused := fake.sent[3], fake.sent[2]
		fake.mu.Unlock()
		if sentOffline != 0 || sentPaused != 0 {
			t.Fatalf("commands to offline source %d / paused line source %d, want 0", sentOffline, sentPaused)
		}
		rt := e.h.lineMon()
		if lv := rt.live[lines[4].ID]; lv.Status != 0 || lv.Reason != "源节点离线" {
			t.Fatalf("offline live %+v", lv)
		}
		if lv := rt.live[lines[3].ID]; lv.Status != 0 || lv.Reason == "" {
			t.Fatalf("timeout live %+v", lv)
		}
		if w := rt.writes.Load(); w == 0 || w > 4 {
			t.Fatalf("writes %d: samples must be batched", w)
		}
	})
}

func TestLineMonitorPruneAndOrphans(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		id, _ := e.save(t, map[string]interface{}{"name": "p", "lines": []interface{}{lmLine(1, 2, "", 443)}})
		lines, _ := e.r.ListLineMonitorChartLines(id)
		lid := lines[0].ID
		now := time.Unix(1_800_000_000, 0)
		old := now.Unix() - linemon.RetentionSeconds
		var rows []model.LineMonitorSample
		for i := int64(0); i < 4500; i++ { // 4500 old rows → 3 batches
			rows = append(rows, model.LineMonitorSample{LineID: lid, TS: old - 30*(i+1), Status: 1, Ms10: 10})
		}
		for i := int64(0); i < 10; i++ {
			rows = append(rows, model.LineMonitorSample{LineID: lid, TS: old + 30*i, Status: 1, Ms10: 10})
			rows = append(rows, model.LineMonitorSample{LineID: 999, TS: now.Unix() - 30*i, Status: 3}) // orphan
		}
		if err := e.r.InsertLineMonitorSamples(rows); err != nil {
			t.Fatal(err)
		}
		if n := e.h.lineMonitorPrune(now); n != 4510 {
			t.Fatalf("pruned %d, want 4500 old + 10 orphan", n)
		}
		var kept int64
		e.r.DB().Model(&model.LineMonitorSample{}).Count(&kept)
		if kept != 10 {
			t.Fatalf("kept %d", kept)
		}
		var minTS int64
		e.r.DB().Model(&model.LineMonitorSample{}).Select("MIN(ts)").Scan(&minTS)
		if minTS < old {
			t.Fatalf("a sample older than 30 days survived: %d < %d", minTS, old)
		}
		// Duplicate slot writes are ignored (restart / retry safety).
		if err := e.r.InsertLineMonitorSamples([]model.LineMonitorSample{{LineID: lid, TS: old, Status: 3}}); err != nil {
			t.Fatal(err)
		}
		var st int
		e.r.DB().Model(&model.LineMonitorSample{}).Select("status").Where("line_id = ? AND ts = ?", lid, old).Scan(&st)
		if st != 1 {
			t.Fatalf("existing slot overwritten: %d", st)
		}
	})
}

func TestLineMonitorDataBucketsAndStats(t *testing.T) {
	lineMonDialects(t, func(t *testing.T, e *lineMonEnv) {
		id, _ := e.save(t, map[string]interface{}{"name": "d", "lines": []interface{}{lmLine(1, 2, "", 443), lmLine(1, 3, "", 443)}})
		lines, _ := e.r.ListLineMonitorChartLines(id)
		now := time.Now()
		nowS := now.Unix() / 30 * 30
		e.exec(`UPDATE line_monitor_line SET created_time = ?`, (nowS-31*86400)*1000)
		var rows []model.LineMonitorSample
		// Line A: 30 days of ok samples, one 3-slot 断流 10 days ago, one loss.
		downAt := nowS - 10*86400
		for ts := nowS - 30*86400 + 60; ts < nowS-60; ts += 30 {
			s := model.LineMonitorSample{LineID: lines[0].ID, TS: ts + 7, Status: 1, Ms10: 200 + int(ts%7)}
			if ts >= downAt && ts < downAt+90 {
				s.Status, s.Ms10 = 3, 0
			}
			if ts == downAt+3600 {
				s.Status, s.Ms10 = 2, 900
			}
			rows = append(rows, s)
			if len(rows) >= 2000 {
				if err := e.r.InsertLineMonitorSamples(rows); err != nil {
					t.Fatal(err)
				}
				rows = rows[:0]
			}
		}
		// Line B: only the last hour, clean.
		for ts := nowS - 3600; ts < nowS-60; ts += 30 {
			rows = append(rows, model.LineMonitorSample{LineID: lines[1].ID, TS: ts + 20, Status: 1, Ms10: 500})
		}
		if err := e.r.InsertLineMonitorSamples(rows); err != nil {
			t.Fatal(err)
		}
		q := func(rng string, from, to int64) *lineMonitorData {
			d, err := e.h.lineMonitorQuery(id, rng, from, to, now)
			if err != nil {
				t.Fatal(err)
			}
			return d
		}
		d := q("30d", 0, 0)
		if d.Step != 1800 || len(d.Lines) != 2 || len(d.Lines[0].Avg) > linemon.MaxPoints {
			t.Fatalf("30d step %d points %d", d.Step, len(d.Lines[0].Avg))
		}
		var downBuckets int32
		for _, v := range d.Lines[0].Down {
			downBuckets += v
		}
		if downBuckets != 3 {
			t.Fatalf("30d buckets lost the 断流: %d down slots", downBuckets)
		}
		sa := d.Stats[0]
		if sa.DownEvents != 1 || sa.LongestDown != 90 || sa.Loss != 1 || sa.Down != 3 {
			t.Fatalf("30d stats A %+v", sa)
		}
		if len(d.Events) != 1 || d.Events[0].LineID != lines[0].ID || d.Events[0].Start != downAt+7 || d.Events[0].Duration() != 90 {
			t.Fatalf("events %+v", d.Events)
		}
		if a := float64(sa.Availability); math.Abs(a-100*float64(sa.OK+sa.Loss)/float64(sa.Measured)) > 1e-9 || a >= 100 {
			t.Fatalf("availability %v", a)
		}
		sb := d.Stats[1]
		if sb.DownEvents != 0 || float64(sb.Availability) != 100 || float64(sb.NoDataPct) < 90 {
			t.Fatalf("30d stats B (1 h of data in 30 d → mostly 无数据) %+v", sb)
		}
		// Cached second read (same window).
		if d2 := q("30d", 0, 0); !d2.Cached {
			t.Fatal("30d read not cached")
		}
		// 1 h raw: step 30, line B clean with ~0 无数据.
		h1 := q("1h", 0, 0)
		if h1.Step != 30 || h1.Cached {
			t.Fatalf("1h step %d cached %v", h1.Step, h1.Cached)
		}
		if s := h1.Stats[1]; s.DownEvents != 0 || float64(s.NoDataPct) > 5 || s.P95 != 50 {
			t.Fatalf("1h stats B %+v", s)
		}
		// Custom window around the 断流 (zoom) with raw slots.
		z := q("custom", downAt-600, downAt+600)
		if z.Step != 30 || z.Stats[0].DownEvents != 1 || z.Stats[0].Down != 3 {
			t.Fatalf("zoom %+v", z.Stats[0])
		}
		// Custom window older than 30 days is clamped; bad windows refused.
		if _, err := e.h.lineMonitorQuery(id, "custom", nowS-40*86400, nowS-20*86400, now); err != nil {
			t.Fatalf("clamped window: %v", err)
		}
		for _, bad := range [][2]int64{{0, 0}, {nowS, nowS - 10}, {nowS - 30, nowS}} {
			if _, err := e.h.lineMonitorQuery(id, "custom", bad[0], bad[1], now); err == nil {
				t.Fatalf("window %v accepted", bad)
			}
		}
		if _, err := e.h.lineMonitorQuery(id, "2y", 0, 0, now); err == nil {
			t.Fatal("unknown range accepted")
		}
	})
}
