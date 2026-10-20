package socket

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/core/service"
	"github.com/go-gost/x/machineprotect"
	"github.com/go-gost/x/nftables"
	"github.com/go-gost/x/registry"
	"github.com/gorilla/websocket"
)

type addrService struct{ addr net.Addr }

func (s addrService) Serve() error   { return nil }
func (s addrService) Addr() net.Addr { return s.addr }
func (s addrService) Close() error   { return nil }

var _ service.Service = addrService{}

type portsNftManager struct {
	NftablesManagerInterface
	tcp, udp []int
}

func (m *portsNftManager) ListenPorts() ([]int, []int)               { return m.tcp, m.udp }
func (m *portsNftManager) Close()                                    {}
func (m *portsNftManager) CollectTraffic() []nftables.TrafficDelta   { return nil }
func (m *portsNftManager) GetForwardConnectionCounts() map[int64]int { return nil }
func (m *portsNftManager) ListForwardIDs() []int64                   { return nil }

func testProtector(t *testing.T, w *WebSocketReporter) *machineprotect.Protector {
	t.Helper()
	dir := t.TempDir()
	return machineprotect.New(machineprotect.Options{RunDir: filepath.Join(dir, "run"), StateDir: dir, Ports: w.businessPorts,
		HelperActive: func(context.Context) bool { return false }, Logf: func(string) {}, Interval: 20 * time.Millisecond})
}

func TestBusinessPortsFromServicesNftAndLanding(t *testing.T) {
	reg := registry.ServiceRegistry()
	add := func(name string, addr net.Addr) {
		t.Helper()
		if err := reg.Register(name, addrService{addr: addr}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reg.Unregister(name) })
	}
	add("mp-1_1_1_tcp", &net.TCPAddr{IP: net.IPv6unspecified, Port: 21001})
	add("mp-1_1_1_udp", &net.UDPAddr{IP: net.IPv4zero, Port: 21001})
	add("mp-80_tls", &net.TCPAddr{IP: net.ParseIP("203.0.113.5"), Port: 21002})
	add("mp-loopback", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 21003})
	add("mp-loopback6", &net.UDPAddr{IP: net.IPv6loopback, Port: 21004})
	add("mp-nil", addrService{}.addr)
	w := &WebSocketReporter{}
	w.setNftablesManager(&portsNftManager{tcp: []int{21010, 21001}, udp: []int{21011}})
	got := w.businessPorts()
	if !containsAll(got.TCP, 21001, 21002, 21010) || !containsAll(got.UDP, 21001, 21011) {
		t.Fatalf("ports %+v", got)
	}
	if containsAny(got.TCP, 21003) || containsAny(got.UDP, 21004) {
		t.Fatalf("loopback-only listener listed: %+v", got)
	}
	// After a reconnect the old manager is gone and the new one is used.
	w.setNftablesManager(&portsNftManager{tcp: []int{21020}})
	if got = w.businessPorts(); containsAny(got.TCP, 21010) || !containsAll(got.TCP, 21020) {
		t.Fatalf("stale nft manager ports: %+v", got)
	}
}

func containsAll(list []int, want ...int) bool {
	for _, w := range want {
		if !containsAny(list, w) {
			return false
		}
	}
	return true
}

func containsAny(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestSetMachineProtectionPersistsAndApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"addr":"panel","secret":"psk","panel_ip_family":"prefer_ipv4","http":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	w := NewWebSocketReporter("", "")
	defer w.Stop()
	w.configPath = path
	w.protector = testProtector(t, w)
	if err := w.handleSetMachineProtection(map[string]interface{}{"enabled": true, "maxTotal": 1500, "twBuckets": 500, "closeWaitSeconds": 10}); err != nil {
		t.Fatal(err)
	}
	s, ok := w.protector.Settings()
	if !ok || s != machineprotect.DefaultSettings() {
		t.Fatalf("settings %+v %t", s, ok)
	}
	b, _ := os.ReadFile(path)
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if string(cfg["panel_ip_family"]) != `"prefer_ipv4"` || string(cfg["http"]) != "1" {
		t.Fatalf("other keys lost: %s", b)
	}
	if got, ok := loadMachineProtectionSettings(path); !ok || got != s {
		t.Fatalf("not loaded back: %+v %t (%s)", got, ok, b)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("config mode changed")
	}
	for _, bad := range []map[string]interface{}{
		{"enabled": true, "maxTotal": 50, "twBuckets": 500, "closeWaitSeconds": 10},
		{"enabled": true, "maxTotal": 1500, "twBuckets": 500},
		{"maxTotal": 1500, "twBuckets": 500, "closeWaitSeconds": 10},
	} {
		before, _ := os.ReadFile(path)
		if err := w.handleSetMachineProtection(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("rejected settings changed config.json")
		}
	}
	// An agent restarted with this config applies the persisted settings before the panel connects.
	w2 := &WebSocketReporter{}
	w2.initMachineProtection(t.TempDir(), path)
	if got, ok := w2.protector.Settings(); !ok || got != s {
		t.Fatalf("restart did not load settings: %+v %t", got, ok)
	}
	if _, ok := loadMachineProtectionSettings(filepath.Join(t.TempDir(), "missing.json")); ok {
		t.Fatal("missing config produced settings")
	}
}

func TestHandshakeAnnouncesMachineProtect(t *testing.T) {
	for _, u := range buildWebSocketCandidates("panel.example:443", "s", "v", 0, 0, 0, 0, "") {
		if !strings.Contains(u, "&machineProtect=1") {
			t.Fatalf("capability missing: %s", u)
		}
	}
}

func TestMachineReportOncePerSampleAndConnection(t *testing.T) {
	w := &WebSocketReporter{}
	w.protector = testProtector(t, w)
	if c, p := w.machineReport(); c != nil || p != nil {
		t.Fatal("report before the first sample")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.protector.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for w.protector.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done // no new samples from here on
	if w.protector.Snapshot() == nil {
		t.Fatal("no sample")
	}
	c, p := w.machineReport()
	if c == nil || c.Total <= 0 || p == nil || p.Owner != machineprotect.OwnerFLVX {
		t.Fatalf("first report %+v %+v", c, p)
	}
	if c2, p2 := w.machineReport(); c2 != nil || p2 != nil {
		t.Fatal("same sample sent twice on one connection")
	}
	// A new connection (connect resets the counter) gets the latest sample at once.
	w.machineSentSeq = 0
	if _, p3 := w.machineReport(); p3 == nil {
		t.Fatal("new connection did not get the latest sample")
	}
	// The JSON fields are omitted when there is nothing new.
	info := SystemInfo{}
	b, _ := json.Marshal(info)
	if strings.Contains(string(b), "machine_") {
		t.Fatalf("empty metric carries machine fields: %s", b)
	}
}

// Real reconnects against a fake panel: still exactly one protection loop, and
// every new session's first metric carries the machine sample.
func TestReconnectsKeepOneProtectionLoop(t *testing.T) {
	orig := newNftablesManager
	t.Cleanup(func() { newNftablesManager = orig })
	newNftablesManager = func() (NftablesManagerInterface, error) { return &portsNftManager{}, nil }

	upgrader := websocket.Upgrader{}
	sessions := make(chan bool, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("machineProtect") != "1" {
			http.Error(rw, "no capability", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, msg, err := conn.ReadMessage()
		sessions <- err == nil && strings.Contains(string(msg), `"machine_protect"`)
		_ = conn.Close() // drop the session: the agent reconnects
	}))
	defer srv.Close()

	w := NewWebSocketReporter("", "")
	w.addr = strings.TrimPrefix(srv.URL, "http://")
	w.nodeID = 1 // skip node id discovery
	w.protector = testProtector(t, w)
	go w.protector.Run(w.ctx)
	deadline := time.Now().Add(2 * time.Second)
	for w.protector.Snapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	go w.run()
	defer w.Stop()
	withSample := 0
	for i := 0; i < 6; i++ {
		select {
		case ok := <-sessions:
			if ok {
				withSample++
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d sessions", i)
		}
	}
	if withSample < 5 {
		t.Fatalf("%d of 6 sessions started without the machine sample", withSample)
	}
	if n := countGoroutines("machineprotect.(*Protector).Run"); n != 1 {
		t.Fatalf("%d protection loops after 6 reconnects", n)
	}
	// A second Run (e.g. Start called again) does not add a loop.
	go w.protector.Run(w.ctx)
	time.Sleep(50 * time.Millisecond)
	if n := countGoroutines("machineprotect.(*Protector).Run"); n != 1 {
		t.Fatalf("%d protection loops after a second Run", n)
	}
}

func countGoroutines(fn string) int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, fn) {
			count++
		}
	}
	return count
}

func TestMachineSettingsJSONShape(t *testing.T) {
	b, _ := json.Marshal(machineprotect.DefaultSettings())
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	want := map[string]any{"enabled": true, "max_total": float64(1500), "tw_buckets": float64(500), "close_wait_seconds": float64(10)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config.json shape %s", b)
	}
}
