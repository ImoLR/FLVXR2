package socket

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

// Plan 122 netns E2E. Run the compiled test in a disposable network namespace
// whose /etc/netns/<ns>/hosts maps panel.test to 127.0.0.2 and 127.0.0.3:
//
//	FLVX_PANEL_FAILOVER_E2E=1 ip netns exec NS ./socket.test -test.run TestPanelFailoverE2E -test.v
//
// (a) WSS works → stays WSS through 3 forced reconnects;
// (b) WSS blocked → falls back to ws, WSS restored → back on WSS within the TTL;
// (c) one of two panel IPs black-holed (SYN) or TLS-stalled → the other one quickly;
// (d) a ws path that only delivers the first packets → abandoned within the hello bound;
// (e) panel restart (wss answers 502 for a while) after a healthy wss session → stays wss.
// It refuses to touch firewall rules in the host namespace.
func TestPanelFailoverE2E(t *testing.T) {
	if os.Getenv("FLVX_PANEL_FAILOVER_E2E") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	self, _ := os.Readlink("/proc/self/ns/net")
	host, _ := os.Readlink("/proc/1/ns/net")
	if self == "" || self == host {
		t.Fatal("refusing firewall changes in the host network namespace")
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("lo up: %s %v", out, err)
	}
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), "panel.test")
	if err != nil || len(addrs) != 2 {
		t.Fatalf("panel.test must resolve to 127.0.0.2 and 127.0.0.3 via /etc/netns/<ns>/hosts: %v %v", addrs, err)
	}

	// Faster clocks, same logic.
	old := []time.Duration{panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout, panelHealthyAfter, panelFailoverTTL, panelDialRoundBudget, wsPingInterval}
	panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout = 4*time.Second, 4*time.Second, 8*time.Second
	panelHealthyAfter, panelFailoverTTL, panelDialRoundBudget, wsPingInterval = 5*time.Second, 8*time.Second, 20*time.Second, 2*time.Second
	defer func() {
		panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout, panelHealthyAfter = old[0], old[1], old[2], old[3]
		panelFailoverTTL, panelDialRoundBudget, wsPingInterval = old[4], old[5], old[6]
	}()

	tlsConf, serverCert := e2eCerts(t)
	oldTLS, oldProbe := panelTLSConfig, panelProbeDo
	panelTLSConfig = tlsConf
	panelProbeDo = func(req *http.Request) (*http.Response, error) {
		tr := &http.Transport{TLSClientConfig: tlsConf.Clone(), DialContext: panelconn.Default.DialContext, DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		return (&http.Client{Transport: tr, Timeout: panelProbeTimeout}).Do(req)
	}
	oldPolicy := panelconn.Default
	panelconn.Default = panelconn.New()
	defer func() {
		panelTLSConfig, panelProbeDo = oldTLS, oldProbe
		panelconn.Default.CloseIdleConnections()
		panelconn.Default = oldPolicy
	}()

	// Panel: TLS on :443 and plain on :80 of both IPs; answers pings while reading.
	var sessions atomic.Int64
	var fail5xx atomic.Bool // (e): wss answers 502 like Cloudflare in front of a restarting panel
	var answered5xx atomic.Int64
	type sess struct{ scheme, ip string }
	accepted := make(chan sess, 64)
	up := websocket.Upgrader{}
	handler := func(scheme string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/flow/test" {
				fmt.Fprint(w, "test")
				return
			}
			if scheme == "wss" && fail5xx.Load() {
				answered5xx.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			sessions.Add(1)
			local := c.LocalAddr().(*net.TCPAddr).IP.String()
			accepted <- sess{scheme, local}
			_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"Hello"}`))
			for {
				_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		})
	}
	var servers []*http.Server
	for _, ip := range []string{"127.0.0.2", "127.0.0.3"} {
		l443, err := tls.Listen("tcp", ip+":443", &tls.Config{Certificates: []tls.Certificate{serverCert}})
		if err != nil {
			t.Fatal(err)
		}
		l80, err := net.Listen("tcp", ip+":80")
		if err != nil {
			t.Fatal(err)
		}
		s443, s80 := &http.Server{Handler: handler("wss")}, &http.Server{Handler: handler("ws")}
		servers = append(servers, s443, s80)
		go s443.Serve(l443)
		go s80.Serve(l80)
	}
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()

	cwd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"addr":"panel.test","secret":"s","panel_ip_family":"auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	oldNft, oldIPs := newNftablesManager, reportPublicIPsOnConnect
	newNftablesManager = func() (NftablesManagerInterface, error) { return nil, nil }
	reportPublicIPsOnConnect = func(*WebSocketReporter) {}
	defer func() { newNftablesManager, reportPublicIPsOnConnect = oldNft, oldIPs }()

	// Firewall helpers (this namespace only).
	var rules [][]string
	addRule := func(rule ...string) {
		t.Helper()
		if out, err := exec.Command("iptables", append([]string{"-w", "-A"}, rule...)...).CombinedOutput(); err != nil {
			t.Fatalf("iptables -A %v: %s %v", rule, out, err)
		}
		rules = append(rules, rule)
	}
	clearRules := func() {
		for _, rule := range rules {
			_ = exec.Command("iptables", append([]string{"-w", "-D"}, rule...)...).Run()
		}
		rules = nil
	}
	defer clearRules()
	kill := func() {
		// Reset the agent side of every panel connection (like `ss -K` on a node).
		_ = exec.Command("ss", "-K", "state", "established", "( dport = :443 or dport = :80 )").Run()
	}

	// One-connection monitor: client-side ESTABLISHED sockets to :80/:443 that
	// live longer than a probe request.
	var maxLive atomic.Int64
	stopMon := make(chan struct{})
	var monWG sync.WaitGroup
	monWG.Add(1)
	go func() {
		defer monWG.Done()
		seen := map[string]time.Time{}
		for {
			select {
			case <-stopMon:
				return
			case <-time.After(100 * time.Millisecond):
			}
			now := time.Now()
			cur := clientPanelSockets()
			live := int64(0)
			for k := range seen {
				if _, ok := cur[k]; !ok {
					delete(seen, k)
				}
			}
			for k := range cur {
				if _, ok := seen[k]; !ok {
					seen[k] = now
				}
				if now.Sub(seen[k]) > 1500*time.Millisecond {
					live++
				}
			}
			if live > maxLive.Load() {
				maxLive.Store(live)
			}
		}
	}()
	defer func() { close(stopMon); monWG.Wait() }()

	w := NewWebSocketReporter("", "s")
	w.addr = "panel.test"
	w.secret = "s"
	w.nodeID = 1
	w.pingInterval = 500 * time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	defer func() {
		w.Stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("reporter did not stop")
		}
	}()

	await := func(label string, within time.Duration, ok func(sess) bool) (sess, time.Duration) {
		t.Helper()
		start := time.Now()
		deadline := time.After(within)
		for {
			select {
			case s := <-accepted:
				if ok(s) {
					return s, time.Since(start)
				}
				t.Logf("%s: passing session %s@%s", label, s.scheme, s.ip)
			case <-deadline:
				t.Fatalf("%s: no matching session within %s", label, within)
			}
		}
	}
	healthyWait := func() { time.Sleep(panelHealthyAfter + 2*time.Second) }

	// (a) healthy WSS stays WSS through 3 forced reconnects.
	first, _ := await("initial", 15*time.Second, func(s sess) bool { return true })
	if first.scheme != "wss" {
		t.Fatalf("(a) initial session %s@%s, want wss", first.scheme, first.ip)
	}
	healthyWait()
	goroutinesBase := runtime.NumGoroutine()
	for i := 1; i <= 3; i++ {
		before := sessions.Load()
		kill()
		s, took := await(fmt.Sprintf("(a) reconnect %d", i), 15*time.Second, func(s sess) bool { return true })
		if s.scheme != "wss" {
			t.Fatalf("(a) reconnect %d went to %s", i, s.scheme)
		}
		if n := sessions.Load() - before; n != 1 {
			t.Fatalf("(a) reconnect %d opened %d sessions", i, n)
		}
		t.Logf("(a) reconnect %d: wss@%s after %s", i, s.ip, took.Round(time.Millisecond))
		healthyWait()
	}

	// (b) WSS blocked (SYN dropped) → ws; WSS back → wss within the TTL.
	addRule("INPUT", "-p", "tcp", "--dport", "443", "-j", "DROP")
	kill()
	s, took := await("(b) fallback", 30*time.Second, func(s sess) bool { return true })
	if s.scheme != "ws" {
		t.Fatalf("(b) expected ws while 443 is blocked, got %s", s.scheme)
	}
	t.Logf("(b) wss blocked → ws@%s after %s", s.ip, took.Round(time.Millisecond))
	time.Sleep(2 * time.Second)
	clearRules()
	restored := time.Now()
	s, _ = await("(b) back to wss", panelFailoverTTL*5/4+15*time.Second, func(s sess) bool { return s.scheme == "wss" })
	backIn := time.Since(restored)
	t.Logf("(b) wss restored → back on wss@%s after %s (TTL %s)", s.ip, backIn.Round(time.Millisecond), panelFailoverTTL)
	if backIn > panelFailoverTTL*5/4+10*time.Second {
		t.Fatalf("(b) switch back took %s", backIn)
	}
	healthyWait()

	// (c1) one panel IP black-holed (no SYN-ACK) → the other one.
	addRule("INPUT", "-d", "127.0.0.2", "-j", "DROP")
	kill()
	s, took = await("(c1) black-holed IP", 20*time.Second, func(s sess) bool { return true })
	if s.scheme != "wss" || s.ip != "127.0.0.3" {
		t.Fatalf("(c1) expected wss@127.0.0.3, got %s@%s", s.scheme, s.ip)
	}
	t.Logf("(c1) 127.0.0.2 black-holed → wss@127.0.0.3 after %s", took.Round(time.Millisecond))
	if took > panelDialAttemptTimeout+3*time.Second {
		t.Fatalf("(c1) too slow: %s", took)
	}
	clearRules()
	healthyWait()

	// (c2) TCP up but TLS stalls on 127.0.0.2 (server's handshake records lost) → pinned 127.0.0.3.
	addRule("OUTPUT", "-s", "127.0.0.2", "-p", "tcp", "--sport", "443", "-m", "length", "--length", "200:65535", "-j", "DROP")
	kill()
	s, took = await("(c2) TLS stall", 30*time.Second, func(s sess) bool { return true })
	if s.scheme != "wss" || s.ip != "127.0.0.3" {
		t.Fatalf("(c2) expected wss@127.0.0.3, got %s@%s", s.scheme, s.ip)
	}
	t.Logf("(c2) TLS stall on 127.0.0.2 → wss@127.0.0.3 after %s", took.Round(time.Millisecond))
	if took > 2*panelDialAttemptTimeout+3*time.Second {
		t.Fatalf("(c2) too slow: %s", took)
	}
	healthyWait()
	kill() // the stalled IP is cooling down: straight to 127.0.0.3
	s, took = await("(c2) next round", 15*time.Second, func(s sess) bool { return true })
	if s.ip != "127.0.0.3" || took > 3*time.Second {
		t.Fatalf("(c2) next round should go straight to 127.0.0.3: %s@%s after %s", s.scheme, s.ip, took)
	}
	t.Logf("(c2) next round: wss@127.0.0.3 after %s (cooled-down IP skipped)", took.Round(time.Millisecond))
	clearRules()
	healthyWait()

	// (d) wss down and ws to 127.0.0.2 only passes the first 3 packets agent→panel:
	// the handshake works, the panel's first message arrives, nothing of ours does.
	addRule("INPUT", "-p", "tcp", "--dport", "443", "-j", "DROP")
	addRule("OUTPUT", "-d", "127.0.0.2", "-p", "tcp", "--dport", "80", "-m", "connbytes", "--connbytes", "4:", "--connbytes-dir", "original", "--connbytes-mode", "packets", "-j", "DROP")
	kill()
	start := time.Now()
	s, _ = await("(d) truncated ws", 30*time.Second, func(s sess) bool { return s.scheme == "ws" && s.ip == "127.0.0.2" })
	truncatedAt := time.Now()
	s, _ = await("(d) good ws", 60*time.Second, func(s sess) bool { return s.scheme == "ws" && s.ip == "127.0.0.3" })
	t.Logf("(d) truncated ws@127.0.0.2 abandoned; ws@127.0.0.3 after %s (total %s)", time.Since(truncatedAt).Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	// Abandon bound: hello timeout + one metric tick, then backoff + a wss round (SYN drop) + the dial.
	if limit := panelHelloTimeout + time.Second + 3*time.Second + panelDialAttemptTimeout + 2*time.Second; time.Since(truncatedAt) > limit {
		t.Fatalf("(d) truncated session not abandoned in time: %s > %s", time.Since(truncatedAt), limit)
	}
	healthyWait()
	clearRules()
	s, _ = await("(d) back to wss", panelFailoverTTL*5/4+15*time.Second, func(s sess) bool { return s.scheme == "wss" })
	t.Logf("(d) back on wss@%s", s.ip)
	healthyWait()

	// (e) panel restart after a healthy wss session: the panel side drops and wss
	// answers 502 for a while; ws on :80 still works. The agent must not fall back
	// to ws (that left nodes 21/53 on ws at the fork.65 restart) and must be back
	// on wss once 502 stops.
	fail5xx.Store(true)
	kill()
	time.Sleep(3 * panelDialAttemptTimeout / 2)
	fail5xx.Store(false)
	restored = time.Now()
	s, _ = await("(e) after 502", 30*time.Second, func(s sess) bool { return true })
	if s.scheme != "wss" {
		t.Fatalf("(e) panel answered 502 %d times → agent fell back to %s@%s", answered5xx.Load(), s.scheme, s.ip)
	}
	t.Logf("(e) panel 502 ×%d during restart → no ws; back on wss@%s %s after the panel was up",
		answered5xx.Load(), s.ip, time.Since(restored).Round(time.Millisecond))
	if answered5xx.Load() == 0 {
		t.Fatal("(e) the agent never hit the 502 window")
	}
	healthyWait()

	if m := maxLive.Load(); m > 1 {
		t.Fatalf("agent held %d panel connections at once", m)
	}
	time.Sleep(time.Second)
	if g := runtime.NumGoroutine(); g > goroutinesBase+6 {
		t.Fatalf("goroutines grew: %d → %d", goroutinesBase, g)
	} else {
		t.Logf("goroutines %d → %d after all scenarios; max live panel connections %d", goroutinesBase, g, maxLive.Load())
	}
}

// clientPanelSockets lists ESTABLISHED sockets whose remote port is 80 or 443.
func clientPanelSockets() map[string]bool {
	out := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 || fields[3] != "01" {
				continue
			}
			rem := fields[2]
			if strings.HasSuffix(rem, ":01BB") || strings.HasSuffix(rem, ":0050") {
				out[fields[1]+"-"+rem] = true
			}
		}
		fh.Close()
	}
	return out
}

func e2eCerts(t *testing.T) (*tls.Config, tls.Certificate) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "flvx e2e ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "panel.test"}, DNSNames: []string{"panel.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &tls.Config{RootCAs: pool}, tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
}
