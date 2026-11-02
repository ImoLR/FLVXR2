package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/probe"
	"go-backend/internal/store/model"
)

// TestProbeExpiryContract: plan 194 「剩余天数」 on SQLite and PostgreSQL —
// per node only an integer `days` + a cycle token (never the stored ms
// timestamp), exact key sets, no expiry → keys absent, hidden node absent,
// 显示到期天数 default on / admin-only / validated, switch off → absent at once.
func TestProbeExpiryContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			nowT := time.Now()
			day := int64(24 * time.Hour / time.Millisecond)
			now := nowT.UnixMilli()
			type seed struct {
				id     int64
				name   string
				expiry int64 // 0 = NULL
				cycle  string
			}
			seeds := []seed{
				{741, "东京 年付", now + 104*day + 3_600_000, "year"},
				{742, "香港 月付", now - 5*day, "month"}, // rolls forward like the 节点 page
				{743, "无周期", now - 3*day, ""},        // 已过期 3 天
				{744, "无到期", 0, "month"},
				{745, "内部隐藏", now + 9*day, "year"},
				{746, "半年付", now + 40*day, "halfYear"},
				{747, "怪周期", now + 2*day, "weekly"},
			}
			for i, s := range seeds {
				n := &model.Node{ID: s.id, Name: s.name, Region: "JP", Secret: "exp-secret-" + strconv.FormatInt(s.id, 10), ServerIP: "203.0.113.4" + strconv.Itoa(i), Port: "1-2", Version: sqlNullString("3.0.27-fork.108 (debian/amd64)"), Inx: i + 1, Status: 1}
				if s.expiry != 0 {
					n.ExpiryTime = sql.NullInt64{Int64: s.expiry, Valid: true}
				}
				if s.cycle != "" {
					n.RenewalCycle = sqlNullString(s.cycle)
				}
				if err := r.DB().Create(n).Error; err != nil {
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
			snapshot := func() (map[string]map[string]json.RawMessage, []byte) {
				t.Helper()
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/probe/snapshot", nil))
				if w.Code != http.StatusOK {
					t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
				}
				var env struct {
					Data struct {
						Nodes []map[string]json.RawMessage `json:"nodes"`
					} `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
					t.Fatal(err)
				}
				byKey := map[string]map[string]json.RawMessage{}
				for _, n := range env.Data.Nodes {
					var k string
					_ = json.Unmarshal(n["k"], &k)
					byKey[k] = n
				}
				return byKey, w.Body.Bytes()
			}
			adminExpiry := func() bool {
				t.Helper()
				out := call("config/probe", admin, nil)
				if out.Code != 0 {
					t.Fatalf("admin get %+v", out)
				}
				raw, _ := json.Marshal(out.Data)
				var v struct {
					Expiry *bool `json:"expiry"`
				}
				_ = json.Unmarshal(raw, &v)
				if v.Expiry == nil {
					t.Fatalf("admin view has no expiry: %s", raw)
				}
				return *v.Expiry
			}

			// Default: on, nothing stored yet.
			if !adminExpiry() {
				t.Fatal("显示到期天数 should default to on")
			}
			// Admin only + validation (generic config API too).
			if err := r.DB().Create(&model.User{ID: 853, User: "probe-exp-user", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + day, FlowResetTime: 1, Num: 10}).Error; err != nil {
				t.Fatal(err)
			}
			user, _ := auth.GenerateToken(853, "probe-exp-user", 1, probeSecret)
			if out := call("config/probe/save", user, map[string]interface{}{"expiry": false}); out.Code != 403 {
				t.Fatalf("user saved expiry: %+v", out)
			}
			for _, bad := range []string{"yes", "1", ""} {
				if out := call("config/update", admin, map[string]string{"probe_expiry": bad}); out.Code == 0 {
					t.Fatalf("generic config API accepted probe_expiry=%q", bad)
				}
			}
			// Old settings card (no expiry field): switch untouched.
			if out := call("config/probe/save", admin, map[string]interface{}{"title": "", "latency": true,
				"nodes": []map[string]interface{}{{"id": 745, "hidden": true}}}); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			if v := mustQueryString(t, r, "SELECT COUNT(*) FROM vite_config WHERE name = ?", "probe_expiry"); v != "0" {
				t.Fatalf("save without expiry wrote the switch (%s rows)", v)
			}
			if out := call("config/update", admin, map[string]string{"login_monitor_link": "true"}); out.Code != 0 {
				t.Fatalf("switch on: %+v", out)
			}

			base := "arch,cpu,disk,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
			full := "arch,cpu,cycle,days,disk,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
			daysOnly := "arch,cpu,days,disk,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
			nodes, body := snapshot()
			if len(nodes) != 6 {
				t.Fatalf("want 6 shown nodes, got %d: %s", len(nodes), body)
			}
			if _, ok := nodes[keyer.Key(745)]; ok || bytes.Contains(body, []byte("内部隐藏")) {
				t.Fatal("hidden node published")
			}
			wantCycle := map[int64]string{741: "year", 742: "month", 746: "halfyear"}
			for _, s := range seeds {
				if s.id == 745 {
					continue
				}
				n := nodes[keyer.Key(s.id)]
				if n == nil {
					t.Fatalf("node %d missing", s.id)
				}
				keys := sortedKeys(n)
				switch {
				case s.expiry == 0:
					if keys != base {
						t.Fatalf("%s keys %s", s.name, keys)
					}
					continue
				case wantCycle[s.id] != "":
					if keys != full || string(n["cycle"]) != strconv.Quote(wantCycle[s.id]) {
						t.Fatalf("%s keys %s cycle %s", s.name, keys, n["cycle"])
					}
				default:
					if keys != daysOnly {
						t.Fatalf("%s keys %s", s.name, keys)
					}
				}
				want, _, _ := probe.ExpiryDays(s.expiry, s.cycle, time.Now())
				if string(n["days"]) != strconv.Itoa(want) {
					t.Fatalf("%s days %s want %d", s.name, n["days"], want)
				}
			}
			if d := string(nodes[keyer.Key(742)]["days"]); d == "" || d[0] == '-' {
				t.Fatalf("monthly node did not roll forward: %s", d)
			}
			if d := string(nodes[keyer.Key(743)]["days"]); d != "-3" && d != "-2" && d != "-4" {
				t.Fatalf("expired no-cycle node: %s", d)
			}
			// Never the stored timestamp (ms or s), not even of the hidden node.
			for _, s := range seeds {
				if s.expiry == 0 {
					continue
				}
				for _, bad := range []string{strconv.FormatInt(s.expiry, 10), strconv.FormatInt(s.expiry/1000, 10)} {
					if bytes.Contains(body, []byte(bad)) {
						t.Fatalf("snapshot leaks expiry %s: %s", bad, body)
					}
				}
			}
			for _, bad := range []string{"expiry", "renewal", "halfYear", "weekly", "203.0.113."} {
				if bytes.Contains(body, []byte(bad)) {
					t.Fatalf("snapshot leaks %q: %s", bad, body)
				}
			}

			// Switch off in the card → absent at once (cache reset on save).
			if out := call("config/probe/save", admin, map[string]interface{}{"title": "", "latency": true, "expiry": false,
				"nodes": []map[string]interface{}{{"id": 745, "hidden": true}}}); out.Code != 0 {
				t.Fatalf("save off: %+v", out)
			}
			if v := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_expiry"); v != "false" {
				t.Fatalf("stored %q", v)
			}
			if adminExpiry() {
				t.Fatal("admin view still on")
			}
			nodes, body = snapshot()
			if bytes.Contains(body, []byte(`"days"`)) || bytes.Contains(body, []byte(`"cycle"`)) {
				t.Fatalf("switch off still publishes: %s", body)
			}
			for k, n := range nodes {
				if got := sortedKeys(n); got != base {
					t.Fatalf("node %s keys while off: %s", k, got)
				}
			}
			// Old card again: stays off.
			if out := call("config/probe/save", admin, map[string]interface{}{"title": "", "latency": true}); out.Code != 0 || adminExpiry() {
				t.Fatalf("old card flipped the switch: %+v", out)
			}
			// Generic config API on → back at once.
			if out := call("config/update", admin, map[string]string{"probe_expiry": "true"}); out.Code != 0 {
				t.Fatalf("generic on: %+v", out)
			}
			if _, body = snapshot(); !strings.Contains(string(body), `"days":`) {
				t.Fatalf("switch on again: no days: %s", body)
			}
		})
	}
}
