package contract_test

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/probe"
	"go-backend/internal/store/model"
)

const probeSecret = "probe-contract-secret"

// TestProbeContract: plan 187 public probe on SQLite and PostgreSQL — switch
// off = 404 everywhere, admin-only validated settings, whitelisted snapshot
// (no IP / secret / id / agent version, hidden nodes absent), gzip + ETag,
// series by opaque key only, probe-domain host gate, old public endpoints
// tightened, rate limit.
func TestProbeContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			rows := []interface{}{
				&model.User{ID: 851, User: "probe-user", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.Node{ID: 701, Name: "东京 IIJ", Region: "JP", Secret: "probe-node-secret-701", ServerIP: "203.0.113.71", Port: "10000-19999", Version: sqlNullString("3.0.27-fork.104 (debian/amd64)"), Inx: 1, Status: 1},
				&model.Node{ID: 702, Name: "香港 HKT", Region: "HK", Secret: "probe-node-secret-702", ServerIP: "203.0.113.72", Port: "10000-19999", Version: sqlNullString("3.0.27-fork.104 (ubuntu/arm64)"), Inx: 2, Status: 1},
				&model.Node{ID: 703, Name: "内部节点", Region: "SG", Secret: "probe-node-secret-703", ServerIP: "203.0.113.73", Port: "10000-19999", Version: sqlNullString("3.0.27-fork.104 (debian/amd64)"), Inx: 3, Status: 1},
			}
			for _, row := range rows {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			metrics := make([]*model.NodeMetric, 0)
			for i := 0; i < 600; i++ {
				ts := now - int64(i)*5000
				metrics = append(metrics, &model.NodeMetric{NodeID: 701, Timestamp: ts, CPUUsage: float64(i % 100), MemUsage: 40, DiskUsage: 20, NetInSpeed: 1000, NetOutSpeed: 2000, TCPConns: 7})
			}
			if err := r.InsertNodeMetricBatch(metrics); err != nil {
				t.Fatal(err)
			}

			h := handler.New(r, probeSecret, "test")
			router := httpserver.NewRouter(h, probeSecret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			user, _ := auth.GenerateToken(851, "probe-user", 1, probeSecret)
			keyer := probe.NewKeyer(probeSecret)

			do := func(method, path, host, token string, body interface{}, hdr map[string]string) *httptest.ResponseRecorder {
				t.Helper()
				var rd io.Reader
				if body != nil {
					raw, _ := json.Marshal(body)
					rd = bytes.NewReader(raw)
				}
				req := httptest.NewRequest(method, path, rd)
				if host != "" {
					req.Host = host
				}
				if token != "" {
					req.Header.Set("Authorization", token)
				}
				for k, v := range hdr {
					req.Header.Set(k, v)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w
			}
			api := func(path, token string, body interface{}) response.R {
				t.Helper()
				w := do(http.MethodPost, "/api/v1/"+path, "panel.example.com", token, body, nil)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}

			// 1. Switch off (default): every probe endpoint is a 404.
			for _, p := range []string{"/api/v1/probe/snapshot", "/api/v1/probe/series?k=" + keyer.Key(701), "/api/v1/probe/ws", "/api/v1/probe/host"} {
				if w := do(http.MethodGet, p, "", "", nil, nil); w.Code != http.StatusNotFound {
					t.Fatalf("switch off: %s → %d", p, w.Code)
				}
			}

			// 2. Admin-only settings.
			if out := api("config/probe", "", nil); out.Code != 401 {
				t.Fatalf("anonymous probe settings: %+v", out)
			}
			if out := api("config/probe", user, nil); out.Code != 403 {
				t.Fatalf("user probe settings: %+v", out)
			}
			if out := api("config/probe/save", user, map[string]interface{}{"title": "x"}); out.Code != 403 {
				t.Fatalf("user probe save: %+v", out)
			}
			out := api("config/probe", admin, nil)
			if out.Code != 0 {
				t.Fatalf("admin probe settings: %+v", out)
			}
			raw, _ := json.Marshal(out.Data)
			if !strings.Contains(string(raw), "内部节点") || !strings.Contains(string(raw), `"latency":true`) {
				t.Fatalf("settings: %s", raw)
			}
			for _, bad := range []map[string]interface{}{
				{"title": strings.Repeat("长", 41)},
				{"domain": "not a domain"},
				{"domain": "https://panel.example.com/x"}, // the host the admin is on → lock-out guard
				{"nodes": []map[string]interface{}{{"id": 702, "probeName": strings.Repeat("名", 31)}}},
			} {
				if out := api("config/probe/save", admin, bad); out.Code == 0 {
					t.Fatalf("invalid settings accepted: %v", bad)
				}
			}
			if out := api("config/update", admin, map[string]string{"probe_domain": "panel.example.com"}); out.Code == 0 {
				t.Fatal("generic config API accepted the lock-out domain")
			}
			if out := api("config/update", admin, map[string]string{"probe_nodes": "{broken"}); out.Code == 0 {
				t.Fatal("generic config API accepted broken probe_nodes")
			}
			save := map[string]interface{}{
				"title": "我的探针", "domain": "https://Status.Example.com/", "latency": true,
				"nodes": []map[string]interface{}{{"id": 703, "hidden": true}, {"id": 702, "probeName": "香港 1"}, {"id": 999, "hidden": true}},
			}
			if out := api("config/probe/save", admin, save); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			if v := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_domain"); v != "status.example.com" {
				t.Fatalf("domain stored as %q", v)
			}
			if v := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_nodes"); strings.Contains(v, "999") {
				t.Fatalf("unknown node kept: %s", v)
			}
			if out := api("config/update", admin, map[string]string{"login_monitor_link": "true"}); out.Code != 0 {
				t.Fatalf("switch on: %+v", out)
			}

			// 3. Snapshot: whitelist, no leaks, hidden node absent.
			w := do(http.MethodGet, "/api/v1/probe/snapshot", "", "", nil, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
			}
			body := w.Body.Bytes()
			for _, bad := range []string{"203.0.113.", "probe-node-secret", "fork.104", `"id"`, `"version"`, "内部节点", "10000-19999", keyer.Key(703)} {
				if bytes.Contains(body, []byte(bad)) {
					t.Fatalf("snapshot leaks %q: %s", bad, body)
				}
			}
			var env struct {
				Code int             `json:"code"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(body, &env); err != nil || env.Code != 0 {
				t.Fatalf("snapshot envelope: %v %s", err, body)
			}
			var snap map[string]json.RawMessage
			_ = json.Unmarshal(env.Data, &snap)
			if got := sortedKeys(snap); got != "latency_on,nodes,title,ts" {
				t.Fatalf("snapshot keys %s", got)
			}
			var nodes []map[string]json.RawMessage
			_ = json.Unmarshal(snap["nodes"], &nodes)
			if len(nodes) != 2 {
				t.Fatalf("nodes: %s", snap["nodes"])
			}
			if got := sortedKeys(nodes[0]); got != "arch,cpu,disk,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime" {
				t.Fatalf("node keys %s", got)
			}
			if string(nodes[0]["k"]) != strconv.Quote(keyer.Key(701)) || string(nodes[1]["name"]) != `"香港 1"` || string(nodes[0]["os"]) != `"debian"` {
				t.Fatalf("nodes %s", snap["nodes"])
			}
			if seen, _ := strconv.ParseInt(string(nodes[0]["seen"]), 10, 64); seen != now {
				t.Fatalf("seen %d want %d (newest stored metric)", seen, now)
			}
			if string(snap["title"]) != `"我的探针"` {
				t.Fatalf("title %s", snap["title"])
			}
			// gzip once + ETag.
			wz := do(http.MethodGet, "/api/v1/probe/snapshot", "", "", nil, map[string]string{"Accept-Encoding": "gzip"})
			if wz.Header().Get("Content-Encoding") != "gzip" {
				t.Fatal("no gzip")
			}
			zr, err := gzip.NewReader(bytes.NewReader(wz.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			plain, _ := io.ReadAll(zr)
			if !json.Valid(plain) {
				t.Fatal("gzip body is not the JSON")
			}
			etag := wz.Header().Get("ETag")
			if w := do(http.MethodGet, "/api/v1/probe/snapshot", "", "", nil, map[string]string{"If-None-Match": etag}); etag == "" || w.Code != http.StatusNotModified {
				t.Fatalf("etag %q → %d", etag, w.Code)
			}

			// 4. Series: opaque key only; hidden / unknown / numeric id → 404.
			ws := do(http.MethodGet, "/api/v1/probe/series?k="+keyer.Key(701)+"&range=1h&points=60", "", "", nil, nil)
			if ws.Code != http.StatusOK {
				t.Fatalf("series %d %s", ws.Code, ws.Body.String())
			}
			var series struct {
				Data probe.Series `json:"data"`
			}
			_ = json.Unmarshal(ws.Body.Bytes(), &series)
			if n := len(series.Data.Points); n < 40 || n > 62 || series.Data.Step != 60_000 {
				t.Fatalf("series points %d step %d", n, series.Data.Step)
			}
			if bytes.Contains(ws.Body.Bytes(), []byte("203.0.113.")) || bytes.Contains(ws.Body.Bytes(), []byte(`"id"`)) {
				t.Fatal("series leaks")
			}
			for _, q := range []string{"k=" + keyer.Key(703), "k=nope", "k=701", "k="} {
				if w := do(http.MethodGet, "/api/v1/probe/series?"+q, "", "", nil, nil); w.Code != http.StatusNotFound {
					t.Fatalf("series %s → %d", q, w.Code)
				}
			}
			if w := do(http.MethodGet, "/api/v1/probe/series?k="+keyer.Key(701)+"&range=30d", "", "", nil, nil); w.Code != http.StatusBadRequest {
				t.Fatalf("bad range → %d", w.Code)
			}

			// 5. Probe domain: only the probe routes answer there.
			const probeHost = "status.example.com"
			for _, p := range []string{"/api/v1/config/list", "/api/v1/user/login", "/api/v1/config/probe", "/system-info?type=2", "/flow/upload", "/s/abc", "/api/v1/monitor/public/nodes", "/api/v1/probe/other"} {
				if w := do(http.MethodPost, p, probeHost, admin, nil, nil); w.Code != http.StatusNotFound {
					t.Fatalf("probe host %s → %d", p, w.Code)
				}
			}
			if w := do(http.MethodGet, "/api/v1/config/list", "panel.example.com", "", nil, map[string]string{"X-Forwarded-Host": probeHost}); w.Code != http.StatusNotFound {
				t.Fatalf("X-Forwarded-Host probe host → %d", w.Code)
			}
			if w := do(http.MethodGet, "/api/v1/probe/snapshot", probeHost+":443", "", nil, nil); w.Code != http.StatusOK {
				t.Fatalf("probe host snapshot → %d", w.Code)
			}
			if w := do(http.MethodGet, "/api/v1/probe/host", probeHost, "", nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "probeOnly") {
				t.Fatalf("probe host check → %d %s", w.Code, w.Body.String())
			}
			if w := do(http.MethodGet, "/api/v1/probe/host", "panel.example.com", "", nil, nil); w.Code != http.StatusNotFound {
				t.Fatalf("main host check → %d", w.Code)
			}
			if out := api("config/list", admin, nil); out.Code != 0 {
				t.Fatalf("main host admin API broken: %+v", out)
			}

			// 6. Old public endpoints: hidden node gone, no ids / versions.
			for _, p := range []string{"/api/v1/monitor/public/nodes", "/api/v1/monitor/public/nodes/metrics"} {
				w := do(http.MethodGet, p, "", "", nil, nil)
				b := w.Body.String()
				if strings.Contains(b, "内部节点") || strings.Contains(b, `"id":701`) || strings.Contains(b, `"id":702`) || strings.Contains(b, "fork.104") || !strings.Contains(b, `(debian/amd64)`) {
					t.Fatalf("%s not tightened: %s", p, b)
				}
			}

			// 7. Rate limit: 60 per minute per address.
			limited := false
			for i := 0; i < 70 && !limited; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/probe/snapshot", nil)
				req.RemoteAddr = "198.51.100.9:5000"
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				limited = rec.Code == http.StatusTooManyRequests
			}
			if !limited {
				t.Fatal("snapshot GET not rate limited")
			}

			// 8. Switch off again → 404.
			if out := api("config/update", admin, map[string]string{"login_monitor_link": "false"}); out.Code != 0 {
				t.Fatal(out)
			}
			if w := do(http.MethodGet, "/api/v1/probe/snapshot", "", "", nil, nil); w.Code != http.StatusNotFound {
				t.Fatalf("switched off snapshot → %d", w.Code)
			}
		})
	}
}

// TestProbeWebSocket: origin rule, live frames (gzip binary / text), 5 per
// address cap, switch off closes open streams with 4404.
func TestProbeWebSocket(t *testing.T) {
	r := landingContractRepo(t, "sqlite")
	if err := r.DB().Create(&model.Node{ID: 711, Name: "东京", Secret: "ws-secret-711", ServerIP: "203.0.113.11", Port: "1-2", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertConfig("login_monitor_link", "true", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	h := handler.New(r, probeSecret, "test")
	srv := httptest.NewServer(httpserver.NewRouter(h, probeSecret, r))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/probe/ws"
	origin := http.Header{"Origin": {srv.URL}}

	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin accepted: %v", err)
	}
	c, _, err := websocket.DefaultDialer.Dial(wsURL+"?gzip=1", origin)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	mt, msg, err := c.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage {
		t.Fatalf("first frame: %v %d", err, mt)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(msg))
	plain, _ := io.ReadAll(zr)
	if !bytes.Contains(plain, []byte(`"nodes"`)) || bytes.Contains(plain, []byte("203.0.113.11")) || bytes.Contains(plain, []byte("ws-secret")) {
		t.Fatalf("frame: %s", plain)
	}
	text, _, err := websocket.DefaultDialer.Dial(wsURL, origin)
	if err != nil {
		t.Fatal(err)
	}
	_ = text.SetReadDeadline(time.Now().Add(5 * time.Second))
	if mt, _, err := text.ReadMessage(); err != nil || mt != websocket.TextMessage {
		t.Fatalf("text frame: %v %d", err, mt)
	}
	// Clients send junk: ignored, small frames only (512 B).
	_ = text.WriteMessage(websocket.TextMessage, []byte(`{"type":"cmd","data":"rm -rf /"}`))
	extra := []*websocket.Conn{}
	for i := 0; i < 3; i++ {
		e, _, err := websocket.DefaultDialer.Dial(wsURL, origin)
		if err != nil {
			t.Fatalf("stream %d: %v", i+3, err)
		}
		extra = append(extra, e)
	}
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, origin); err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th stream from one address: %v", err)
	}
	stats, _ := h.ProbeStats()
	if stats.Clients != 5 {
		t.Fatalf("clients %d", stats.Clients)
	}
	// Switch off → open streams closed with 4404.
	admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/config/update", strings.NewReader(`{"login_monitor_link":"false"}`))
	req.Header.Set("Authorization", admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue
		}
		if ce, ok := err.(*websocket.CloseError); !ok || ce.Code != 4404 {
			t.Fatalf("expected close 4404, got %v", err)
		}
		break
	}
	for _, e := range append(extra, text) {
		e.Close()
	}
}

func sqlNullString(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }

func sortedKeys(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// TestProbeGroupsContract: plan 189 custom groups on SQLite and PostgreSQL —
// admin-only validated CRUD (≤ 30, names 1–20 unique, existing nodes), a node
// in several groups, hidden nodes and empty groups never published, deleted
// nodes pruned on read, the snapshot still carries no id / IP / version and
// names groups by index only.
func TestProbeGroupsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			for _, row := range []*model.Node{
				{ID: 721, Name: "香港 IXP", Region: "HK", Secret: "grp-secret-721", ServerIP: "203.0.113.121", Port: "1-2", Version: sqlNullString("3.0.27-fork.104 (debian/amd64)"), Inx: 1, Status: 1},
				{ID: 722, Name: "东京 IXP", Region: "JP", Secret: "grp-secret-722", ServerIP: "203.0.113.122", Port: "1-2", Version: sqlNullString("3.0.27-fork.104 (debian/amd64)"), Inx: 2, Status: 1},
				{ID: 723, Name: "洛杉矶 CN2", Region: "US", Secret: "grp-secret-723", ServerIP: "203.0.113.123", Port: "1-2", Inx: 3, Status: 1},
				{ID: 724, Name: "内部隐藏", Region: "SG", Secret: "grp-secret-724", ServerIP: "203.0.113.124", Port: "1-2", Inx: 4, Status: 1},
				{ID: 725, Name: "待删除", Region: "DE", Secret: "grp-secret-725", ServerIP: "203.0.113.125", Port: "1-2", Inx: 5, Status: 1},
			} {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, probeSecret, "test")
			router := httpserver.NewRouter(h, probeSecret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			keyer := probe.NewKeyer(probeSecret)
			call := func(path, token string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Host = "panel.example.com"
				if token != "" {
					req.Header.Set("Authorization", token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}
			snapshot := func() (map[string]json.RawMessage, []byte) {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/probe/snapshot", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
				}
				var env struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &env)
				return env.Data, w.Body.Bytes()
			}
			type adminView struct {
				Groups []struct {
					ID    int64   `json:"id"`
					Name  string  `json:"name"`
					Nodes []int64 `json:"nodes"`
				} `json:"groups"`
				GroupMode string `json:"groupMode"`
			}
			adminGet := func() adminView {
				t.Helper()
				out := call("config/probe", admin, nil)
				if out.Code != 0 {
					t.Fatalf("admin get %+v", out)
				}
				raw, _ := json.Marshal(out.Data)
				var v adminView
				_ = json.Unmarshal(raw, &v)
				return v
			}
			grp := func(id int64, name string, nodes ...int64) map[string]interface{} {
				return map[string]interface{}{"id": id, "name": name, "nodes": nodes}
			}
			base := func(groups interface{}, mode string) map[string]interface{} {
				return map[string]interface{}{"title": "", "latency": true, "groups": groups, "groupMode": mode,
					"nodes": []map[string]interface{}{{"id": 724, "hidden": true}}}
			}

			// Defaults: no groups, region mode.
			if v := adminGet(); len(v.Groups) != 0 || v.GroupMode != "region" {
				t.Fatalf("defaults %+v", v)
			}
			// Admin only.
			if err := r.DB().Create(&model.User{ID: 852, User: "probe-grp-user", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: time.Now().UnixMilli() + 86400000, FlowResetTime: 1, Num: 10}).Error; err != nil {
				t.Fatal(err)
			}
			user, _ := auth.GenerateToken(852, "probe-grp-user", 1, probeSecret)
			if out := call("config/probe/save", user, base([]interface{}{grp(0, "IXP", 721)}, "group")); out.Code != 403 {
				t.Fatalf("user saved groups: %+v", out)
			}
			// Validation: nothing is written on any error.
			tooMany := make([]interface{}, 31)
			for i := range tooMany {
				tooMany[i] = grp(0, "组"+strconv.Itoa(i))
			}
			for name, bad := range map[string]map[string]interface{}{
				"31 groups":    base(tooMany, "group"),
				"empty name":   base([]interface{}{grp(0, "  ")}, "group"),
				"21 chars":     base([]interface{}{grp(0, strings.Repeat("网", 21))}, "group"),
				"duplicate":    base([]interface{}{grp(0, "IXP"), grp(0, " ixp ")}, "group"),
				"unknown node": base([]interface{}{grp(0, "IXP", 721, 999)}, "group"),
				"bad mode":     base([]interface{}{grp(0, "IXP", 721)}, "tree"),
			} {
				if out := call("config/probe/save", admin, bad); out.Code == 0 {
					t.Fatalf("%s accepted", name)
				}
			}
			if v := adminGet(); len(v.Groups) != 0 {
				t.Fatalf("invalid save wrote groups: %+v", v)
			}
			if out := call("config/update", admin, map[string]string{"probe_groups": "[{\"name\":\"\"}]"}); out.Code == 0 {
				t.Fatal("generic config API accepted an empty group name")
			}
			if out := call("config/update", admin, map[string]string{"probe_group_mode": "tree"}); out.Code == 0 {
				t.Fatal("generic config API accepted a bad group mode")
			}

			// Create: 721 in two groups, 「隐藏组」 has only the hidden node,
			// 「空组」 has nobody; ordered as sent.
			save := base([]interface{}{
				grp(0, " IXP ", 721, 722, 721),
				grp(0, "隐藏组", 724),
				grp(0, "三网优化", 721, 723, 725),
				grp(0, "空组"),
			}, "group")
			if out := call("config/probe/save", admin, save); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			v := adminGet()
			if len(v.Groups) != 4 || v.Groups[0].Name != "IXP" || len(v.Groups[0].Nodes) != 2 || v.Groups[2].Name != "三网优化" || v.GroupMode != "group" {
				t.Fatalf("stored groups %+v", v)
			}
			ids := map[int64]bool{}
			for _, g := range v.Groups {
				if g.ID <= 0 || ids[g.ID] {
					t.Fatalf("group ids %+v", v.Groups)
				}
				ids[g.ID] = true
			}
			// Groups and mode are admin-only config rows.
			anon := call("config/list", "", nil)
			if raw, _ := json.Marshal(anon.Data); strings.Contains(string(raw), "probe_group") || strings.Contains(string(raw), "三网优化") {
				t.Fatalf("anonymous config/list shows groups: %s", raw)
			}

			if out := call("config/update", admin, map[string]string{"login_monitor_link": "true"}); out.Code != 0 {
				t.Fatal(out)
			}
			snap, body := snapshot()
			if got := sortedKeys(snap); got != "group_mode,groups,latency_on,nodes,title,ts" {
				t.Fatalf("snapshot keys %s", got)
			}
			if string(snap["groups"]) != `[{"name":"IXP"},{"name":"三网优化"}]` || string(snap["group_mode"]) != `"group"` {
				t.Fatalf("published groups %s mode %s", snap["groups"], snap["group_mode"])
			}
			for _, bad := range []string{"隐藏组", "空组", "内部隐藏", "203.0.113.", "grp-secret", "fork.104", `"id"`, `"version"`, `"721"`, `"nodes":[7`, keyer.Key(724)} {
				if bytes.Contains(body, []byte(bad)) {
					t.Fatalf("snapshot leaks %q: %s", bad, body)
				}
			}
			var nodes []map[string]json.RawMessage
			_ = json.Unmarshal(snap["nodes"], &nodes)
			if len(nodes) != 4 {
				t.Fatalf("nodes %s", snap["nodes"])
			}
			wantG := map[string]string{keyer.Key(721): "[0,1]", keyer.Key(722): "[0]", keyer.Key(723): "[1]", keyer.Key(725): "[1]"}
			for _, n := range nodes {
				var k string
				_ = json.Unmarshal(n["k"], &k)
				if string(n["g"]) != wantG[k] {
					t.Fatalf("node %s g=%s want %s", n["name"], n["g"], wantG[k])
				}
				if got := sortedKeys(n); got != "arch,cpu,disk,g,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime" {
					t.Fatalf("node keys %s", got)
				}
			}

			// Delete node 725: pruned from the admin view and the snapshot;
			// rename + reorder + delete a group, mode back to region.
			if err := r.DB().Delete(&model.Node{}, 725).Error; err != nil {
				t.Fatal(err)
			}
			v = adminGet()
			if g := v.Groups[2]; g.Name != "三网优化" || len(g.Nodes) != 2 {
				t.Fatalf("deleted node not pruned: %+v", g)
			}
			ixp, sanwang := v.Groups[0], v.Groups[2]
			save = base([]interface{}{grp(sanwang.ID, "三网精品", sanwang.Nodes...), grp(ixp.ID, "IXP", 722)}, "region")
			if out := call("config/probe/save", admin, save); out.Code != 0 {
				t.Fatalf("resave: %+v", out)
			}
			v = adminGet()
			if len(v.Groups) != 2 || v.Groups[0].ID != sanwang.ID || v.Groups[0].Name != "三网精品" || v.Groups[1].ID != ixp.ID || v.GroupMode != "region" {
				t.Fatalf("rename/reorder/delete %+v", v)
			}
			snap, _ = snapshot()
			if string(snap["groups"]) != `[{"name":"三网精品"},{"name":"IXP"}]` || string(snap["group_mode"]) != `"region"` {
				t.Fatalf("groups after edit %s %s", snap["groups"], snap["group_mode"])
			}
			// An older card (no groups field) leaves the groups alone.
			if out := call("config/probe/save", admin, map[string]interface{}{"title": "旧卡片", "latency": true}); out.Code != 0 {
				t.Fatal(out)
			}
			if v := adminGet(); len(v.Groups) != 2 {
				t.Fatalf("groups lost on a save without groups: %+v", v)
			}
			// Clear all → fields gone from the snapshot.
			if out := call("config/probe/save", admin, base([]interface{}{}, "group")); out.Code != 0 {
				t.Fatal(out)
			}
			snap, _ = snapshot()
			if got := sortedKeys(snap); got != "latency_on,nodes,title,ts" {
				t.Fatalf("snapshot keys after clear %s", got)
			}
		})
	}
}
