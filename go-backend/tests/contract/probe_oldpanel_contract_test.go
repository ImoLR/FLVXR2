package contract_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/go-sqlite"

	"go-backend/internal/auth"
	"go-backend/internal/geoip"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/probe"
	"go-backend/internal/store/model"
)

// oldPanelFixtureSchema: the 救命版 (flux-panel-backend 3.0.1-alpha6) node /
// node_metric tables and indexes as found in its gost.db.
const oldPanelFixtureSchema = "CREATE TABLE `node` (`id` integer PRIMARY KEY AUTOINCREMENT,`name` varchar(100) NOT NULL,`remark` text,`expiry_time` integer,`renewal_cycle` varchar(20),`secret` varchar(100) NOT NULL,`server_ip` varchar(100) NOT NULL,`server_ip_v4` varchar(100),`server_ip_v6` varchar(100),`extra_ips` text,`port` text NOT NULL,`interface_name` varchar(200),`version` varchar(100),`http` integer NOT NULL DEFAULT 0,`tls` integer NOT NULL DEFAULT 0,`socks` integer NOT NULL DEFAULT 0,`created_time` integer NOT NULL,`updated_time` integer,`status` integer NOT NULL,`tcp_listen_addr` varchar(100) NOT NULL DEFAULT \"[::]\",`udp_listen_addr` varchar(100) NOT NULL DEFAULT \"[::]\",`inx` integer NOT NULL DEFAULT 0,`is_remote` integer DEFAULT 0,`remote_url` text,`remote_token` text,`remote_config` text,`expiry_reminder_dismissed` integer NOT NULL DEFAULT 0, forward_mode varchar(20) NOT NULL DEFAULT 'agent');" +
	"CREATE TABLE `node_metric` (`id` integer PRIMARY KEY AUTOINCREMENT,`node_id` integer NOT NULL,`timestamp` integer NOT NULL,`cpu_usage` real,`mem_usage` real,`disk_usage` real,`net_in_bytes` integer,`net_out_bytes` integer,`net_in_speed` integer,`net_out_speed` integer,`load1` real,`load5` real,`load15` real,`tcp_conns` integer,`udp_conns` integer,`uptime` integer);" +
	"CREATE INDEX `idx_node_metric_time` ON `node_metric`(`timestamp`);" +
	"CREATE INDEX `idx_node_metric_node_time` ON `node_metric`(`node_id`,`timestamp`);"

type oldPanelSeed struct {
	id                 int64
	name, ip, v4, v6   string
	version            string
	expiry             int64
	cycle              string
	remote             int
	lastMetric         int64 // ms, 0 = no rows
	metricsForMinutes  int
	cpu                float64
	netIn, netOut, tcp int64
}

// buildOldPanelDB writes a 救命版-shaped WAL database; the writer connection
// stays open like the running old panel.
func buildOldPanelDB(t *testing.T, seeds []oldPanelSeed) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gost.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Exec(oldPanelFixtureSchema); err != nil {
		t.Fatal(err)
	}
	tx, _ := w.Begin()
	for i, s := range seeds {
		var expiry interface{}
		if s.expiry > 0 {
			expiry = s.expiry
		}
		if _, err := tx.Exec("INSERT INTO node(id,name,expiry_time,renewal_cycle,secret,server_ip,server_ip_v4,server_ip_v6,port,version,created_time,status,inx,is_remote) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			s.id, s.name, expiry, s.cycle, "old-node-secret-"+strconv.FormatInt(s.id, 10), s.ip, s.v4, s.v6, "40000-41000", s.version, s.lastMetric, 1, -i, s.remote); err != nil {
			t.Fatal(err)
		}
		if s.lastMetric == 0 {
			continue
		}
		for ts := s.lastMetric - int64(s.metricsForMinutes)*60_000; ts <= s.lastMetric; ts += 5000 {
			if _, err := tx.Exec("INSERT INTO node_metric(node_id,timestamp,cpu_usage,mem_usage,disk_usage,net_in_bytes,net_out_bytes,net_in_speed,net_out_speed,load1,load5,load15,tcp_conns,udp_conns,uptime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				s.id, ts, s.cpu, 41.5, 23.0, 5<<30, 7<<30, s.netIn, s.netOut, 0.42, 0.3, 0.2, s.tcp, 4, 777777); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestProbeOldPanelContract: plan 195 on SQLite and PostgreSQL (FLVX side;
// the 救命版 side is always its SQLite file) — env unset / missing file → no
// 救命版 machines and no errors, matching by IP / host name / resolved name,
// 救命版-only machines hidden by default, admin picks + names + mixed groups,
// whitelisted output (no IP, host name, 救命版 id, secret, "救命版"/"flux"),
// series by opaque key, old-format settings still load, generic config API
// validation.
func TestProbeOldPanelContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			nowT := time.Now()
			now := nowT.UnixMilli()
			day := int64(24 * time.Hour / time.Millisecond)
			for _, n := range []*model.Node{
				{ID: 761, Name: "Breadcloud JP", Region: "JP", Secret: "flvx-secret-761", ServerIP: "87.76.190.35", Port: "1-2", Version: sqlNullString("3.0.27-fork.104 (debian/amd64)"), Inx: 1, Status: 1},
				{ID: 762, Name: "Dmit Nathan", Region: "HK", Secret: "flvx-secret-762", ServerIP: "dmitnathan.flvx-test.example", Port: "1-2", Inx: 2, Status: 1,
					ExpiryTime: sql.NullInt64{Int64: now + 50*day, Valid: true}, RenewalCycle: sqlNullString("year")},
				{ID: 763, Name: "vmsilo 沪港", Region: "HK", Secret: "flvx-secret-763", ServerIP: "240e:96c:7100:1fe:1c8:c8aa:4ecc:b4", ServerIPV6: sqlNullString("240e:96c:7100:1fe:1c8:c8aa:4ecc:b4"), Port: "1-2", Inx: 3, Status: 1},
			} {
				if err := r.DB().Create(n).Error; err != nil {
					t.Fatal(err)
				}
			}
			old := []oldPanelSeed{
				{id: 13, name: "BREADCLOUD JP", ip: "jp.flvx-test.example", version: "3.0.1-beta13 (debian/amd64)", expiry: now + 20*day, cycle: "year", lastMetric: now - 4000, metricsForMinutes: 2, cpu: 9},
				{id: 72, name: "DMIT NATHAN OLD", ip: "DmitNathan.flvx-test.example", version: "3.0.1-beta13 (debian/amd64)", expiry: now + 9*day, cycle: "month", lastMetric: now - 4000, metricsForMinutes: 2, cpu: 9},
				{id: 40, name: "Vmsilo sh-hk", ip: "10.0.10.38", v4: "10.0.10.38", v6: "240e:96c:7100:1fe:1c8:c8aa:4ecc:b4", version: "3.0.1-rc2 (debian/amd64)", lastMetric: now - 4000, metricsForMinutes: 2},
				{id: 1, name: "PQS TW", ip: "pqstw.flvx-test.example", version: "3.0.1-rc4 (debian/amd64)", expiry: now + 3*day + 3_600_000, cycle: "year", lastMetric: now - 7000, metricsForMinutes: 90, cpu: 33.33, netIn: 123456, netOut: 654321, tcp: 88},
				{id: 35, name: "Vmiss KR", ip: "45.93.28.126", v4: "45.93.28.126", version: "3.0.0-rc6 (ubuntu/arm64)", expiry: now - 5*day, cycle: "month", lastMetric: now - 21000, metricsForMinutes: 10, cpu: 12.5, netIn: 1000, netOut: 2000, tcp: 9},
				{id: 28, name: "VPS.SOY SG", ip: "194.127.192.26", v4: "194.127.192.26", version: "3.0.0-rc6 (debian/amd64)", expiry: now + 30*day, cycle: "month", lastMetric: now - 5000, metricsForMinutes: 2, cpu: 5},
				{id: 41, name: "Offline HK", ip: "103.49.61.123", version: "3.0.0 (debian/amd64)", lastMetric: now - 20*60_000, metricsForMinutes: 3, cpu: 50},
				{id: 99, name: "Remote Thing", ip: "198.51.100.99", remote: 1, lastMetric: now - 3000, metricsForMinutes: 1},
			}
			oldPath := buildOldPanelDB(t, old)
			fakeDNS := func(_ context.Context, host string) ([]netip.Addr, error) {
				switch host {
				case "jp.flvx-test.example":
					return []netip.Addr{netip.MustParseAddr("87.76.190.35")}, nil
				case "pqstw.flvx-test.example":
					return []netip.Addr{netip.MustParseAddr("45.150.1.2")}, nil
				case "dmitnathan.flvx-test.example":
					return []netip.Addr{netip.MustParseAddr("154.17.9.9")}, nil
				}
				return nil, errors.New("nxdomain")
			}
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			keyer := probe.NewKeyer(probeSecret)

			type adminNode struct {
				ID        int64  `json:"id"`
				Name      string `json:"name"`
				ProbeName string `json:"probeName"`
				Hidden    bool   `json:"hidden"`
				Region    string `json:"region"`
				Online    bool   `json:"online"`
				Source    string `json:"source"`
				OldMatch  string `json:"oldMatch"`
				Auto      string `json:"regionAuto"`
				Override  string `json:"regionOverride"`
			}
			type adminView struct {
				Nodes  []adminNode `json:"nodes"`
				Groups []struct {
					ID    int64   `json:"id"`
					Name  string  `json:"name"`
					Nodes []int64 `json:"nodes"`
				} `json:"groups"`
				OldPanel string `json:"oldPanel"`
			}
			type env struct {
				router http.Handler
				h      *handler.Handler
			}
			start := func(path string) env {
				t.Helper()
				t.Setenv("FLVX_OLDPANEL_DB", path)
				h := handler.New(r, probeSecret, "test")
				h.SetProbeOldPanelResolver(fakeDNS)
				return env{router: httpserver.NewRouter(h, probeSecret, r), h: h}
			}
			call := func(e env, path string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Host = "panel.example.com"
				req.Header.Set("Authorization", admin)
				w := httptest.NewRecorder()
				e.router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}
			adminGet := func(e env) (adminView, []byte) {
				t.Helper()
				out := call(e, "config/probe", nil)
				if out.Code != 0 {
					t.Fatalf("admin get %+v", out)
				}
				raw, _ := json.Marshal(out.Data)
				var v adminView
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Fatal(err)
				}
				return v, raw
			}
			get := func(e env, path string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				e.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				return w
			}
			type snap struct {
				Nodes  []map[string]json.RawMessage `json:"nodes"`
				Groups []struct {
					Name string `json:"name"`
				} `json:"groups"`
			}
			snapshot := func(e env) (snap, map[string]map[string]json.RawMessage, []byte) {
				t.Helper()
				w := get(e, "/api/v1/probe/snapshot")
				if w.Code != http.StatusOK {
					t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
				}
				var envl struct {
					Data snap `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envl); err != nil {
					t.Fatal(err)
				}
				byKey := map[string]map[string]json.RawMessage{}
				for _, n := range envl.Data.Nodes {
					var k string
					_ = json.Unmarshal(n["k"], &k)
					byKey[k] = n
				}
				return envl.Data, byKey, w.Body.Bytes()
			}
			str := func(raw json.RawMessage) string {
				var s string
				_ = json.Unmarshal(raw, &s)
				return s
			}

			// 1. Env unset: nothing from the 救命版, card says off.
			e0 := start("")
			if out := call(e0, "config/update", map[string]string{"login_monitor_link": "true"}); out.Code != 0 {
				t.Fatalf("switch on: %+v", out)
			}
			v, raw := adminGet(e0)
			if v.OldPanel != "" || len(v.Nodes) != 3 || strings.Contains(string(raw), `"source":"old"`) {
				t.Fatalf("env unset admin view: %s", raw)
			}
			if _, nodes, _ := snapshot(e0); len(nodes) != 3 {
				t.Fatalf("env unset: %d machines", len(nodes))
			}

			// 2. Missing file: no 救命版 machines, no error for viewers.
			eMiss := start(filepath.Join(t.TempDir(), "absent", "gost.db"))
			v, _ = adminGet(eMiss)
			if v.OldPanel != "error" || len(v.Nodes) != 3 {
				t.Fatalf("missing db admin view %+v", v)
			}
			if _, nodes, _ := snapshot(eMiss); len(nodes) != 3 {
				t.Fatalf("missing db: %d machines", len(nodes))
			}

			// 3. The fixture.
			e := start(oldPath)
			v, raw = adminGet(e)
			if v.OldPanel != "ok" {
				t.Fatalf("old panel status %q", v.OldPanel)
			}
			byID := map[int64]adminNode{}
			for _, n := range v.Nodes {
				byID[n.ID] = n
			}
			// Matched pairs are one FLVX row with the 救命版 name as a hint.
			if byID[761].OldMatch != "BREADCLOUD JP" || byID[762].OldMatch != "DMIT NATHAN OLD" || byID[763].OldMatch != "Vmsilo sh-hk" || byID[761].Source != "flvx" {
				t.Fatalf("matching (host→IP, same host, v6 literal): %s", raw)
			}
			for _, id := range []int64{-13, -72, -40, -99, 13, 1, 35} {
				if _, ok := byID[id]; ok {
					t.Fatalf("machine %d listed (matched / remote / positive old id): %s", id, raw)
				}
			}
			wantOld := []int64{-1, -35, -28, -41}
			for _, id := range wantOld {
				n, ok := byID[id]
				if !ok || n.Source != "old" || !n.Hidden {
					t.Fatalf("救命版-only machine %d: %+v (default hidden) in %s", id, n, raw)
				}
			}
			if !byID[-35].Online || byID[-41].Online || (len(byID[-35].Region) != 0 && len(byID[-35].Region) != 2) {
				t.Fatalf("online / region: %+v %+v", byID[-35], byID[-41])
			}
			// Automatic region = GeoIP of the public address (cached batch).
			for id, ip := range map[int64]string{-28: "194.127.192.26", -41: "103.49.61.123"} {
				want := geoip.Lookup(netip.MustParseAddr(ip))
				if want == "" || byID[id].Auto != want || byID[id].Region != want {
					t.Fatalf("region of %d: auto %q region %q want %q", id, byID[id].Auto, byID[id].Region, want)
				}
			}

			// Default hidden: the public page has only the FLVX machines; the
			// matched FLVX node without its own expiry borrows the 救命版 one.
			_, nodes, body := snapshot(e)
			if len(nodes) != 3 {
				t.Fatalf("default hidden: %d machines: %s", len(nodes), body)
			}
			if d := string(nodes[keyer.Key(761)]["days"]); d == "" {
				t.Fatalf("matched FLVX node did not borrow the 救命版 expiry: %s", body)
			}
			if d := string(nodes[keyer.Key(763)]["days"]); d != "" {
				t.Fatalf("no expiry on either side but days=%s", d)
			}
			if want, _, _ := probe.ExpiryDays(now+50*day, "year", time.Now()); string(nodes[keyer.Key(762)]["days"]) != strconv.Itoa(want) {
				t.Fatalf("FLVX expiry must win: %s want %d", nodes[keyer.Key(762)]["days"], want)
			}

			// 4. Admin picks three, names one, groups mixed sources.
			save := map[string]interface{}{
				"title": "", "latency": true,
				"nodes": []map[string]interface{}{
					{"id": 761, "hidden": false}, {"id": 762, "hidden": false}, {"id": 763, "hidden": false},
					{"id": -1, "hidden": false}, {"id": -35, "hidden": false, "probeName": " 首尔 Vmiss ", "region": "kr"},
					{"id": -28, "hidden": true, "probeName": "隐藏的名字"}, {"id": -41, "hidden": false},
					{"id": -13, "hidden": false},  // matched: not a machine of its own, but a 救命版 id — accepted, never shown
					{"id": -555, "hidden": false}, // unknown: dropped
				},
				"groups": []map[string]interface{}{
					{"id": 1, "name": "混合", "nodes": []int64{761, -35, -28}},
					{"id": 2, "name": "备用组", "nodes": []int64{-41}},
					{"id": 3, "name": "全是隐藏", "nodes": []int64{-28}},
				},
				"groupMode": "group",
			}
			if out := call(e, "config/probe/save", save); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			var stored struct {
				Hidden     []int64           `json:"hidden"`
				OldShown   []int64           `json:"old_shown"`
				OldNames   map[string]string `json:"old_names"`
				OldRegions map[string]string `json:"old_regions"`
			}
			nodesRow := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_nodes")
			if err := json.Unmarshal([]byte(nodesRow), &stored); err != nil {
				t.Fatal(err)
			}
			sort.Slice(stored.OldShown, func(i, j int) bool { return stored.OldShown[i] < stored.OldShown[j] })
			if len(stored.Hidden) != 0 || len(stored.OldShown) != 4 || stored.OldShown[0] != 1 || stored.OldShown[1] != 13 || stored.OldShown[2] != 35 || stored.OldShown[3] != 41 ||
				stored.OldNames["35"] != "首尔 Vmiss" || stored.OldNames["28"] != "隐藏的名字" || strings.Contains(nodesRow, "555") ||
				len(stored.OldRegions) != 1 || stored.OldRegions["35"] != "KR" {
				t.Fatalf("stored probe_nodes %s", nodesRow)
			}
			groupsRow := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_groups")
			if groupsRow != `[{"id":1,"name":"混合","nodes":[761],"old":[35,28]},{"id":2,"name":"备用组","nodes":[],"old":[41]},{"id":3,"name":"全是隐藏","nodes":[],"old":[28]}]` {
				t.Fatalf("stored probe_groups %s", groupsRow)
			}
			if out := call(e, "config/probe/save", map[string]interface{}{"title": "", "latency": true,
				"groups": []map[string]interface{}{{"id": 1, "name": "x", "nodes": []int64{-777}}}}); out.Code == 0 {
				t.Fatal("group with an unknown 救命版 machine accepted")
			}
			if out := call(e, "config/probe/save", map[string]interface{}{"title": "", "latency": true,
				"nodes": []map[string]interface{}{{"id": -35, "hidden": true, "region": "korea"}}}); out.Code == 0 {
				t.Fatal("bad region accepted")
			}
			if got := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_nodes"); got != nodesRow {
				t.Fatalf("rejected save changed probe_nodes: %s", got)
			}

			// Admin view round trip: negative ids in groups, picks kept.
			v, raw = adminGet(e)
			byID = map[int64]adminNode{}
			for _, n := range v.Nodes {
				byID[n.ID] = n
			}
			if byID[-35].Hidden || byID[-35].ProbeName != "首尔 Vmiss" || !byID[-28].Hidden || byID[-1].Hidden || byID[-41].Hidden ||
				byID[-35].Region != "KR" || byID[-35].Override != "KR" || byID[-41].Override != "" || byID[-41].Region != byID[-41].Auto {
				t.Fatalf("picks after save: %s", raw)
			}
			if len(v.Groups) != 3 || len(v.Groups[0].Nodes) != 3 || v.Groups[0].Nodes[1] != -35 || v.Groups[1].Nodes[0] != -41 {
				t.Fatalf("groups in the admin view: %s", raw)
			}

			// 5. Public snapshot: FLVX + 3 picked 救命版 machines, whitelisted.
			s, nodes, body := snapshot(e)
			if len(nodes) != 6 {
				t.Fatalf("want 6 machines, got %d: %s", len(nodes), body)
			}
			base := "arch,cpu,disk,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
			withDays := "arch,cpu,cycle,days,disk,g,k,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
			kr := nodes[keyer.Key(-35)]
			if kr == nil || str(kr["name"]) != "首尔 Vmiss" || str(kr["region"]) != "KR" || string(kr["online"]) != "true" || string(kr["cpu"]) != "12.5" || string(kr["net_in"]) != "1000" ||
				string(kr["tcp"]) != "9" || str(kr["os"]) != "ubuntu" || str(kr["arch"]) != "arm64" || sortedKeys(kr) != withDays || string(kr["cycle"]) != `"month"` {
				t.Fatalf("救命版 machine card %v in %s", kr, body)
			}
			if d := string(kr["days"]); d == "" || d[0] == '-' {
				t.Fatalf("monthly 救命版 expiry did not roll: %s", d)
			}
			pqs := nodes[keyer.Key(-1)]
			if pqs == nil || str(pqs["name"]) != "PQS TW" || string(pqs["online"]) != "true" || string(pqs["cpu"]) != "33.3" {
				t.Fatalf("PQS TW %v", pqs)
			}
			off := nodes[keyer.Key(-41)]
			if off == nil || string(off["online"]) != "false" || string(off["cpu"]) != "0" || string(off["seen"]) == "0" || sortedKeys(off) != strings.Replace(base, "disk,k", "disk,g,k", 1) {
				t.Fatalf("offline 救命版 machine %v", off)
			}
			if seen, _ := strconv.ParseInt(string(off["seen"]), 10, 64); now-seen < 19*60_000 || now-seen > 21*60_000 {
				t.Fatalf("offline seen %d", seen)
			}
			// Keys are source-qualified: not the FLVX key of the same number.
			if k := str(kr["k"]); len(k) != 10 || k == keyer.Key(35) || nodes[keyer.Key(35)] != nil {
				t.Fatalf("key %q", k)
			}
			if nodes[keyer.Key(761)] == nil || nodes[keyer.Key(-13)] != nil || nodes[keyer.Key(-28)] != nil {
				t.Fatal("FLVX key changed / matched or hidden 救命版 machine published")
			}
			// Groups: hidden 救命版 machine never appears; a hidden-only group is not sent.
			if len(s.Groups) != 2 || s.Groups[0].Name != "混合" || s.Groups[1].Name != "备用组" {
				t.Fatalf("groups %+v", s.Groups)
			}
			if string(kr["g"]) != "[0]" || string(off["g"]) != "[1]" || string(nodes[keyer.Key(761)]["g"]) != "[0]" {
				t.Fatalf("membership kr=%s off=%s jp=%s", kr["g"], off["g"], nodes[keyer.Key(761)]["g"])
			}
			for _, bad := range []string{
				"45.93.28.126", "194.127.192.26", "103.49.61.123", "87.76.190.35", "240e:", "10.0.10.38", "198.51.100.99", "45.150.1.2",
				"flvx-test.example", "old-node-secret", "flvx-secret", "40000-41000",
				"救命版", "flux", "Flux", "FLUX", "old:", "oldpanel", "old_",
				"VPS.SOY", "隐藏的名字", "BREADCLOUD JP", "DMIT NATHAN OLD", "Vmsilo sh-hk", "Remote Thing", "Vmiss KR",
				"3.0.1", "3.0.0", "rc6", `"id"`, "全是隐藏",
			} {
				if bytes.Contains(body, []byte(bad)) {
					t.Fatalf("snapshot leaks %q: %s", bad, body)
				}
			}
			for _, sd := range old {
				if sd.expiry != 0 && (bytes.Contains(body, []byte(strconv.FormatInt(sd.expiry, 10))) || bytes.Contains(body, []byte(strconv.FormatInt(sd.expiry/1000, 10)))) {
					t.Fatalf("snapshot leaks the 救命版 expiry of %s", sd.name)
				}
			}

			// GeoIP regions are cached: more admin lists / frames with the same
			// addresses run no new GeoIP batch (the tables are not kept).
			before := e.h.OldPanelStats().RegionLookups
			if before < 1 {
				t.Fatalf("region batches %d", before)
			}
			for i := 0; i < 3; i++ {
				if out := call(e, "config/probe", nil); out.Code != 0 {
					t.Fatal(out)
				}
			}
			if after := e.h.OldPanelStats().RegionLookups; after != before {
				t.Fatalf("region batches %d → %d with unchanged addresses", before, after)
			}

			// 6. Series by opaque key only.
			w := get(e, "/api/v1/probe/series?range=1h&points=60&k="+keyer.Key(-1))
			if w.Code != http.StatusOK {
				t.Fatalf("series %d %s", w.Code, w.Body.String())
			}
			var ser struct {
				Data probe.Series `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &ser); err != nil || len(ser.Data.Points) < 55 || ser.Data.Points[0][1] != 33.3 || ser.Data.Points[0][5] != 88 || len(ser.Data.Latency) != 0 {
				t.Fatalf("series %d points %v", len(ser.Data.Points), err)
			}
			for _, k := range []string{keyer.Key(-28), keyer.Key(-13), keyer.Key(28), "-35", "35"} {
				if w := get(e, "/api/v1/probe/series?range=1h&k="+k); w.Code != http.StatusNotFound {
					t.Fatalf("series for %q → %d", k, w.Code)
				}
			}

			// 7. Probe switch off → 404, 救命版 machines included.
			if out := call(e, "config/update", map[string]string{"login_monitor_link": "false"}); out.Code != 0 {
				t.Fatal(out)
			}
			for _, p := range []string{"/api/v1/probe/snapshot", "/api/v1/probe/series?k=" + keyer.Key(-1)} {
				if w := get(e, p); w.Code != http.StatusNotFound {
					t.Fatalf("switch off %s → %d", p, w.Code)
				}
			}
			if out := call(e, "config/update", map[string]string{"login_monitor_link": "true"}); out.Code != 0 {
				t.Fatal(out)
			}

			// 8. Generic config API validates the new fields and keeps them.
			for _, bad := range []string{`{"hidden":[],"names":{},"old_shown":[-1]}`, `{"hidden":[],"names":{},"old_names":{"x":"a"}}`,
				`{"hidden":[],"names":{},"old_names":{"5":"` + strings.Repeat("长", 31) + `"}}`, `{"hidden":[],"names":{},"old_regions":{"5":"korea"}}`} {
				if out := call(e, "config/update", map[string]string{"probe_nodes": bad}); out.Code == 0 {
					t.Fatalf("generic API accepted %s", bad)
				}
			}
			if out := call(e, "config/update", map[string]string{"probe_nodes": `{"hidden":[763],"names":{},"old_shown":[41],"old_names":{"41":"香港备用"}}`}); out.Code != 0 {
				t.Fatalf("generic API valid value: %+v", out)
			}
			if got := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", "probe_nodes"); !strings.Contains(got, `"old_shown":[41]`) || !strings.Contains(got, "香港备用") {
				t.Fatalf("generic API dropped the new fields: %s", got)
			}
			if out := call(e, "config/update", map[string]string{"probe_groups": `[{"id":1,"name":"a","nodes":[761],"old":[-3]}]`}); out.Code == 0 {
				t.Fatal("generic API accepted a negative old member")
			}
			_, nodes, body = snapshot(e)
			if len(nodes) != 3 || nodes[keyer.Key(-41)] == nil || str(nodes[keyer.Key(-41)]["name"]) != "香港备用" || nodes[keyer.Key(763)] != nil {
				t.Fatalf("after generic update: %s", body)
			}

			// 9. Settings written before plan 195 (no old_* fields) still load:
			// every 救命版 machine hidden, FLVX picks as stored.
			if err := r.UpsertConfig("probe_nodes", `{"hidden":[762],"names":{"761":"东京"}}`, now); err != nil {
				t.Fatal(err)
			}
			if err := r.UpsertConfig("probe_groups", `[{"id":4,"name":"旧格式","nodes":[761,763]}]`, now); err != nil {
				t.Fatal(err)
			}
			if out := call(e, "config/update", map[string]string{"app_name": "FLVX"}); out.Code != 0 { // resets the settings cache
				t.Fatal(out)
			}
			v, raw = adminGet(e)
			for _, n := range v.Nodes {
				if n.Source == "old" && !n.Hidden {
					t.Fatalf("old-format settings: %d shown: %s", n.ID, raw)
				}
			}
			s, nodes, body = snapshot(e)
			if len(nodes) != 2 || str(nodes[keyer.Key(761)]["name"]) != "东京" || len(s.Groups) != 1 || s.Groups[0].Name != "旧格式" {
				t.Fatalf("old-format settings snapshot: %s", body)
			}
		})
	}
}
