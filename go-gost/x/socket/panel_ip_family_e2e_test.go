package socket

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

// Run the compiled test in a disposable network namespace with dual-stack
// localhost, e.g. FLVX_PANEL_BLACKHOLE_E2E=1 ip netns exec NAME ./socket.test
// -test.run TestPanelIPv6BlackholeRecovery -test.v. It never changes host rules.
func TestPanelIPv6BlackholeRecovery(t *testing.T) {
	if os.Getenv("FLVX_PANEL_BLACKHOLE_E2E") != "1" {
		t.Skip("requires disposable network namespace")
	}
	self, _ := os.Readlink("/proc/self/ns/net")
	host, _ := os.Readlink("/proc/1/ns/net")
	if self == "" || self == host {
		t.Fatal("refusing firewall changes in the host network namespace")
	}
	oldPolicy := panelconn.Default
	panelconn.Default = panelconn.New()
	defer func() { panelconn.Default.CloseIdleConnections(); panelconn.Default = oldPolicy }()
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"addr":"localhost","secret":"","panel_ip_family":"auto"}`), 0600); err != nil {
		t.Fatal(err)
	}
	v4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	port := v4.Addr().(*net.TCPAddr).Port
	v6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	accepted := make(chan string, 16)
	up := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system-info" {
			fmt.Fprint(w, "ok")
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		accepted <- panelconn.Family(c.LocalAddr())
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	s4 := &http.Server{Handler: handler}
	s6 := &http.Server{Handler: handler}
	defer s4.Close()
	defer s6.Close()
	go s4.Serve(v4)
	go s6.Serve(v6)
	w := NewWebSocketReporter("", "")
	w.addr = fmt.Sprintf("localhost:%d", port)
	w.nodeID = 1 // skip identity/bootstrap HTTP
	w.preferredWSScheme = "ws"
	w.pingInterval = 200 * time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	defer func() { w.Stop(); <-done }()
	awaitFamily := func(want string) {
		t.Helper()
		select {
		case got := <-accepted:
			if got != want {
				t.Fatalf("connected over %s, want %s", got, want)
			}
			t.Logf("connected over %s", got)
		case <-time.After(35 * time.Second):
			t.Fatal("no reconnect")
		}
	}
	rules := [][]string{
		{"OUTPUT", "-d", "::1", "-p", "tcp", "--dport", strconv.Itoa(port), "-j", "DROP"},
		{"OUTPUT", "-s", "::1", "-p", "tcp", "--sport", strconv.Itoa(port), "-j", "DROP"},
	}
	blocked := false
	unblock := func() {
		if !blocked {
			return
		}
		for _, rule := range rules {
			_ = exec.Command("ip6tables", append([]string{"-w", "-D"}, rule...)...).Run()
		}
		blocked = false
	}
	defer unblock()
	start := time.Now()
	for i := 0; i < 2; i++ {
		awaitFamily("IPv6")
		// Wait until connect() has installed its reader deadline.
		w.connMutex.Lock()
		session := w.conn
		connected := w.connected
		localPort := session.LocalAddr().(*net.TCPAddr).Port
		w.connMutex.Unlock()
		rules[0] = []string{"OUTPUT", "-d", "::1", "-p", "tcp", "--dport", strconv.Itoa(port), "--sport", strconv.Itoa(localPort), "-j", "DROP"}
		rules[1] = []string{"OUTPUT", "-s", "::1", "-p", "tcp", "--sport", strconv.Itoa(port), "--dport", strconv.Itoa(localPort), "-j", "DROP"}
		if !connected {
			t.Fatal("handshake did not establish a session")
		}
		blocked = true
		for _, rule := range rules {
			if out, err := exec.Command("ip6tables", append([]string{"-w", "-A"}, rule...)...).CombinedOutput(); err != nil {
				t.Fatalf("drop: %s %v", out, err)
			}
		}
		dropAt := time.Now()
		for {
			w.connMutex.Lock()
			connected = w.connected && w.conn == session
			w.connMutex.Unlock()
			if !connected {
				break
			}
			if time.Since(dropAt) > 75*time.Second {
				t.Fatal("IPv6 blackhole did not end session")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("IPv6 session %d failed after %s", i+1, time.Since(dropAt))
		if i == 0 {
			unblock()
		}
	}
	awaitFamily("IPv4")
	t.Logf("auto recovered over IPv4 after %s", time.Since(start))
	if time.Since(start) > 3*time.Minute {
		t.Fatal("recovery too slow")
	}
	// With IPv6 still blackholed, panel HTTP must recover with the same policy.
	client := &http.Client{Transport: panelconn.Default, Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + w.addr + "/flow/config")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	unblock()
	changed, err := w.handleSetPanelIPFamily(map[string]interface{}{"panelIpFamily": "prefer_ipv4"})
	if err != nil || !changed {
		t.Fatalf("prefer setting: %t %v", changed, err)
	}
	for i := 0; i < 3; i++ {
		w.reconnectPanel()
		awaitFamily("IPv4")
	}
	t.Log("prefer_ipv4: 3/3 reconnects over IPv4 with healthy IPv6 available")
}
