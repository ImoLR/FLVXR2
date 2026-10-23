package ws

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-backend/internal/auth"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func machineServer(t *testing.T) (*Server, *repo.Repository, string) {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.DB().Create(&model.Node{ID: 1, Name: "n1", Secret: "test-secret", ServerIP: "127.0.0.1"}).Error; err != nil {
		t.Fatal(err)
	}
	s := NewServer(r, "test-jwt")
	h := httptest.NewServer(s)
	t.Cleanup(h.Close)
	return s, r, "ws" + strings.TrimPrefix(h.URL, "http")
}

func readCommand(t *testing.T, c *websocket.Conn) (string, string, map[string]interface{}) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	aes, _ := security.NewAESCrypto("test-secret")
	var cmd struct {
		Type      string                 `json:"type"`
		RequestID string                 `json:"requestId"`
		Data      map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal([]byte(decryptIfNeeded(payload, aes, "test-secret")), &cmd); err != nil {
		t.Fatal(err)
	}
	return cmd.Type, cmd.RequestID, cmd.Data
}

func TestMachineProtectionPushOnlyToCapableAgents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		push  bool
	}{{"capable", "&machineProtect=1", true}, {"old agent", "", false}, {"bad value", "&machineProtect=x", false}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, base := machineServer(t)
			done := make(chan error, 1)
			want := repo.MachineProtection{Enabled: true, MaxTotal: 1000, TWBuckets: 500, CloseWaitSeconds: 10}
			s.SetNodeOnlineHook(func(id int64) { done <- s.SyncMachineProtection(id, want) })
			c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?type=1&secret=test-secret"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if tc.push {
				typ, reqID, data := readCommand(t, c)
				if typ != "SetMachineProtection" || data["enabled"] != true || data["maxTotal"] != float64(1000) ||
					data["twBuckets"] != float64(500) || data["closeWaitSeconds"] != float64(10) {
					t.Fatalf("command %s %v", typ, data)
				}
				if err := c.WriteJSON(map[string]interface{}{"type": "SetMachineProtectionResponse", "requestId": reqID, "success": true}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("sync blocked")
			}
			if s.MachineProtectCapable(1) != tc.push {
				t.Fatal("capability")
			}
			if !tc.push {
				_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
				if _, _, err := c.ReadMessage(); err == nil {
					t.Fatal("old agent received a command")
				}
			}
		})
	}
}

func TestMachineReportStoredAndBroadcastToAdminsOnly(t *testing.T) {
	s, r, base := machineServer(t)
	// Public status stream only exists while the login-page monitor link is on;
	// the normal user needs a monitor scope that covers the node.
	if err := r.DB().Exec(`UPDATE vite_config SET value = 'true' WHERE name = 'login_monitor_link'`).Error; err != nil {
		t.Fatal(err)
	}
	s.SetUserScopeResolver(func(int64) (NodeScope, error) { return NodeScope{All: true}, nil })
	adminToken, _ := auth.GenerateToken(1, "admin", 0, "test-jwt")
	userToken, _ := auth.GenerateToken(2, "user", 1, "test-jwt")
	dial := func(q string) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?"+q, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	admin := dial("type=0&secret=" + adminToken)
	user := dial("type=0&secret=" + userToken)
	public := dial("type=2")
	node := dial("type=1&secret=test-secret&machineProtect=1")
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.RLock()
		ready := s.nodes[1] != nil
		s.mu.RUnlock()
		ready = ready && s.countClients(kindAdmin) == 1 && s.countClients(kindUser) == 1 && s.countClients(kindPublic) == 1
		if ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	metric := `{"type":"metric","data":{"uptime":5,"cpu_usage":1,"memory_usage":2,"tcp_conns":7,"ipv6_ra_status":"ok",` +
		`"machine_conns":{"total":823,"tcp":700,"udp":123,"established":300,"time_wait":350,"close_wait":20,"other_tcp":30},` +
		`"machine_protect":{"owner":"flvx","enabled":true,"max_total":1000,"tw_buckets":500,"close_wait_seconds":10,"blocking":false,"dropped_total":4,"ports":6,"error":""}}}`
	if err := node.WriteMessage(websocket.TextMessage, []byte(metric)); err != nil {
		t.Fatal(err)
	}
	readMetric := func(c *websocket.Conn) map[string]interface{} {
		t.Helper()
		for {
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, msg, err := c.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if json.Unmarshal(msg, &env) != nil || env.Type != "metric" {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(env.Data), &data); err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	a, u, p := readMetric(admin), readMetric(user), readMetric(public)
	if a["machine_conns"] == nil || a["machine_protect"] == nil || a["tcp_conns"] != float64(7) {
		t.Fatalf("admin metric %v", a)
	}
	for name, m := range map[string]map[string]interface{}{"user": u, "public": p} {
		if m["machine_conns"] != nil || m["machine_protect"] != nil || m["tcp_conns"] != float64(7) {
			t.Fatalf("%s metric %v", name, m)
		}
	}
	if a["ipv6_ra_status"] != nil || u["ipv6_ra_status"] != nil {
		t.Fatal("RA status leaked")
	}
	var sample *MachineSample
	for i := 0; i < 100 && sample == nil; i++ {
		sample = s.MachineStatus(1)
		time.Sleep(10 * time.Millisecond)
	}
	if sample == nil || sample.Conns.Total != 823 || sample.Protect.DroppedTotal != 4 || *sample.Protect.MaxTotal != 1000 {
		t.Fatalf("stored %+v", sample)
	}
	// The next 1 s metric without the fields keeps the sample (the browser
	// throttle is lifted so the immediate second report is forwarded too).
	prevInterval := browserMetricInterval.Load()
	browserMetricInterval.Store(0)
	t.Cleanup(func() { browserMetricInterval.Store(prevInterval) })
	if err := node.WriteMessage(websocket.TextMessage, []byte(`{"type":"metric","data":{"uptime":6,"cpu_usage":1,"tcp_conns":7}}`)); err != nil {
		t.Fatal(err)
	}
	if u2 := readMetric(admin); u2["machine_conns"] != nil {
		t.Fatal("fields invented")
	}
	if s.MachineStatus(1) == nil {
		t.Fatal("sample dropped by a metric without fields")
	}
}

func TestMachineSampleViewAndBounds(t *testing.T) {
	maxTotal, tw, cw := int64(1000), int64(500), int64(10)
	conns, prot := SanitizeMachine(
		&MachineConns{Total: 1, TCP: 900, UDP: 30, Established: -5, TimeWait: 600, CloseWait: 40, OtherTCP: 260},
		&MachineProtect{Owner: "flvx", Enabled: true, MaxTotal: &maxTotal, TWBuckets: &tw, CloseWaitSeconds: &cw, Blocking: true, DroppedTotal: -1, Ports: 4, Error: strings.Repeat("错", 400), MmwxStatus: "weird"})
	if conns.Total != 930 || conns.Established != 0 || prot.DroppedTotal != 0 || len([]rune(prot.Error)) != 300 || prot.MmwxStatus != "" {
		t.Fatalf("sanitize %+v %+v", conns, prot)
	}
	if _, p := SanitizeMachine(nil, &MachineProtect{Owner: "evil"}); p != nil {
		t.Fatal("unknown owner accepted")
	}
	sample := &MachineSample{Conns: conns, Protect: prot, At: time.Now()}
	admin, _ := json.Marshal(sample.View(true))
	user, _ := json.Marshal(sample.View(false))
	for _, want := range []string{`"maxTotal":1000`, `"twBuckets":500`, `"closeWaitSeconds":10`, `"ports":4`, `"error":`, `"timeWait":600`} {
		if !strings.Contains(string(admin), want) {
			t.Fatalf("admin view lacks %s: %s", want, admin)
		}
	}
	for _, leak := range []string{"twBuckets", "closeWaitSeconds", "ports", "error", "mmwxStatus"} {
		if strings.Contains(string(user), `"`+leak+`"`) {
			t.Fatalf("user view leaks %s: %s", leak, user)
		}
	}
	if !strings.Contains(string(user), `"maxTotal":1000`) || !strings.Contains(string(user), `"blocking":true`) || !strings.Contains(string(user), `"total":930`) {
		t.Fatalf("user view lacks chip fields: %s", user)
	}
	off := &MachineSample{Protect: &MachineProtect{Owner: "flvx", Enabled: false, MaxTotal: &maxTotal}, At: time.Now()}
	if v, _ := json.Marshal(off.View(false)); !strings.Contains(string(v), `"maxTotal":null`) {
		t.Fatalf("disabled protection shows a cap to users: %s", v)
	}
	if (*MachineSample)(nil).View(true) != nil {
		t.Fatal("nil sample view")
	}

	s := &Server{machineStats: map[int64]*MachineSample{}}
	now := time.Now()
	for id := int64(1); id <= 200; id++ {
		s.recordMachine(id, SystemInfo{MachineConns: &MachineConns{TCP: 800, UDP: 20}, MachineProtect: &MachineProtect{Owner: "flvx", Enabled: true, MaxTotal: &maxTotal}}, now)
	}
	s.machineStats[7].At = now.Add(-machineFreshFor - time.Second)
	if s.MachineStatus(7) != nil || s.MachineStatus(8) == nil {
		t.Fatal("freshness")
	}
	s.machineStats[9].At = now.Add(-machineRetainFor - time.Second)
	s.pruneMachineStats(now)
	s.ForgetMachine(10)
	if _, ok := s.machineStats[9]; ok || s.machineStats[10] != nil || len(s.machineStats) != 198 {
		t.Fatalf("bounds: %d entries", len(s.machineStats))
	}
	s.recordMachine(11, SystemInfo{}, now) // a metric without the fields stores nothing
	if s.machineStats[11].Conns.TCP != 800 {
		t.Fatal("empty metric overwrote the sample")
	}
}

// Memory of the in-memory store for 200 nodes (resource note in the plan).
func TestMachineStoreMemoryFor200Nodes(t *testing.T) {
	maxTotal := int64(1000)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	s := &Server{machineStats: map[int64]*MachineSample{}}
	for id := int64(1); id <= 200; id++ {
		s.recordMachine(id, SystemInfo{MachineConns: &MachineConns{TCP: 800, UDP: 20, TimeWait: 300}, MachineProtect: &MachineProtect{Owner: "flvx", Enabled: true, MaxTotal: &maxTotal, Error: ""}}, time.Now())
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	used := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("200 node samples: %d bytes heap", used)
	fmt.Println("machine-store-200-nodes-bytes", used)
	if used > 256<<10 {
		t.Fatalf("store uses %d bytes for 200 nodes", used)
	}
	if len(s.machineStats) != 200 {
		t.Fatal("entries")
	}
}

func TestMetricBroadcastVariants(t *testing.T) {
	plain := `{"uptime":1,"tcp_conns":3}`
	if a, o := metricBroadcastVariants([]byte(plain)); a != plain || o != plain {
		t.Fatal("plain metric changed")
	}
	withRA := `{"uptime":1,"ipv6_ra_status":"ok","quotaGroups":[]}`
	if a, o := metricBroadcastVariants([]byte(withRA)); a != o || strings.Contains(a, "ipv6") || strings.Contains(a, "quota") {
		t.Fatalf("RA metric %s / %s", a, o)
	}
	withMachine := `{"uptime":1,"machine_conns":{"total":1},"machine_protect":{"owner":"flvx"}}`
	a, o := metricBroadcastVariants([]byte(withMachine))
	if !strings.Contains(a, "machine_conns") || strings.Contains(o, "machine_") || !strings.Contains(o, "uptime") {
		t.Fatalf("machine metric %s / %s", a, o)
	}
	if metricDataForBroadcast([]byte(withMachine)) != o {
		t.Fatal("legacy helper must return the non-admin variant")
	}
}
