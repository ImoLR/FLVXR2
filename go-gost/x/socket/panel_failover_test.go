package socket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
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

// ---- pure ordering / classification -------------------------------------------------

func TestParsePanelTarget(t *testing.T) {
	cases := []struct{ in, hostPort, host, primary string }{
		{"flvxxht.imgamer.top", "flvxxht.imgamer.top", "flvxxht.imgamer.top", "wss"},
		{"104.145.236.29:63665", "104.145.236.29:63665", "104.145.236.29", "wss"},
		{"[2602:ffb0::a]:63665", "[2602:ffb0::a]:63665", "2602:ffb0::a", "wss"},
		{"https://panel.example.com/path?q=1", "panel.example.com", "panel.example.com", "wss"},
		{"http://panel.example.com:6365", "panel.example.com:6365", "panel.example.com", "ws"},
		{"ws://10.0.0.1:80", "10.0.0.1:80", "10.0.0.1", "ws"},
	}
	for _, c := range cases {
		got := parsePanelTarget(c.in)
		if got.hostPort != c.hostPort || got.host != c.host || got.primary != c.primary {
			t.Fatalf("%s => %+v", c.in, got)
		}
		if (got.primary == "wss") != (got.fallback == "ws") {
			t.Fatalf("%s fallback %s", c.in, got.fallback)
		}
	}
}

func TestClassifyDialFailure(t *testing.T) {
	if classifyDialFailure(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, nil, true) != failNoTLS {
		t.Fatal("record header error must mean no TLS on that port")
	}
	if classifyDialFailure(websocket.ErrBadHandshake, &http.Response{StatusCode: 502}, true) != failHTTPStatus {
		t.Fatal("status answer must be failHTTPStatus")
	}
	if classifyDialFailure(context.DeadlineExceeded, nil, true) != failAfterTCP {
		t.Fatal("stall after TCP must be failAfterTCP")
	}
	if classifyDialFailure(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout", IsTimeout: true}}, nil, false) != failDNS {
		t.Fatal("lookup failure must be failDNS")
	}
	if classifyDialFailure(&net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, nil, false) != failTCP {
		t.Fatal("connect failure must be failTCP")
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }
func ip(s string) netip.Addr             { return netip.MustParseAddr(s) }
func ips(s ...string) (out []netip.Addr) {
	for _, x := range s {
		out = append(out, ip(x))
	}
	return
}
func keys(a []netip.Addr) (out []string) {
	for _, x := range a {
		out = append(out, x.String())
	}
	return
}

func TestFailoverSchemesNeverStickAfterTransientFailure(t *testing.T) {
	f := newPanelFailover()
	tg := parsePanelTarget("panel.test")
	f.fail("wss", tg.hostPort, ip("192.0.2.1"))
	f.fail("wss", tg.hostPort, ip("192.0.2.2"))
	if got := f.schemes(tg); got[0] != "wss" {
		t.Fatalf("transient wss failures must not change the scheme order: %v", got)
	}
	f.markNoTLS(tg.hostPort)
	if got := f.schemes(tg); got[0] != "ws" || got[1] != "wss" {
		t.Fatalf("a port that does not speak TLS keeps ws first: %v", got)
	}
	if f.effectivePrimary(tg) != "ws" {
		t.Fatal("effective primary on a plain port is ws")
	}
	if got := f.schemes(parsePanelTarget("other.test")); got[0] != "wss" {
		t.Fatal("no-TLS memory is per address")
	}
}

func TestFailoverOrderCooldownLastGoodAndTTL(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	f := newPanelFailover()
	f.now = clk.now
	hp := "panel.test"
	all := ips("192.0.2.1", "192.0.2.2", "192.0.2.3")
	if got := keys(f.order("wss", hp, all)); strings.Join(got, ",") != "192.0.2.1,192.0.2.2,192.0.2.3" {
		t.Fatalf("no state keeps resolver order: %v", got)
	}
	if f.hasCooldown("wss", hp) {
		t.Fatal("no cooldown yet")
	}
	f.fail("wss", hp, ip("192.0.2.1"))
	clk.add(time.Second)
	f.fail("wss", hp, ip("192.0.2.2"))
	if !f.hasCooldown("wss", hp) || f.hasCooldown("ws", hp) {
		t.Fatal("cooldown is per scheme")
	}
	if got := keys(f.order("wss", hp, all)); strings.Join(got, ",") != "192.0.2.3,192.0.2.1,192.0.2.2" {
		t.Fatalf("healthy first, then the oldest failure: %v", got)
	}
	f.healthy("wss", hp, "panel.test", ip("192.0.2.2"))
	if got := keys(f.order("wss", hp, all)); strings.Join(got, ",") != "192.0.2.2,192.0.2.3,192.0.2.1" {
		t.Fatalf("last healthy IP leads: %v", got)
	}
	clk.add(panelFailoverTTL)
	if f.hasCooldown("wss", hp) {
		t.Fatal("cooldown must expire after the TTL")
	}
	f.fail("wss", hp, ip("192.0.2.3"))
	f.clearScheme("wss", hp)
	if f.hasCooldown("wss", hp) {
		t.Fatal("clearScheme lifts cooldowns")
	}
}

func TestOrderFamiliesAndFilter(t *testing.T) {
	mixed := ips("2001:db8::1", "192.0.2.1", "2001:db8::2", "192.0.2.2")
	if got := strings.Join(keys(orderFamilies(mixed, panelconn.Auto)), ","); got != "192.0.2.1,2001:db8::1,192.0.2.2,2001:db8::2" {
		t.Fatalf("auto interleaves starting with IPv4: %s", got)
	}
	if got := strings.Join(keys(orderFamilies(mixed, panelconn.PreferIPv4)), ","); got != "192.0.2.1,192.0.2.2,2001:db8::1,2001:db8::2" {
		t.Fatalf("prefer_ipv4: %s", got)
	}
	if got := filterFamily(mixed, panelconn.IPv4Only); len(got) != 2 || !got[0].Is4() || !got[1].Is4() {
		t.Fatalf("ipv4_only: %v", got)
	}
	if got := filterFamily(mixed, panelconn.IPv6Only); len(got) != 2 || got[0].Is4() {
		t.Fatalf("ipv6_only: %v", got)
	}
	if panelconn.LookupNetwork(panelconn.IPv4Only) != "ip4" || panelconn.LookupNetwork(panelconn.IPv6Only) != "ip6" || panelconn.LookupNetwork(panelconn.Auto) != "ip" {
		t.Fatal("lookup network mapping")
	}
}

func TestResolveFallsBackToKnownIPsAndHonoursFamily(t *testing.T) {
	old := panelLookupNetIP
	defer func() { panelLookupNetIP = old }()
	var fail atomic.Bool
	var network atomic.Value
	panelLookupNetIP = func(_ context.Context, nw, host string) ([]netip.Addr, error) {
		network.Store(nw)
		if fail.Load() {
			return nil, &net.DNSError{Err: "i/o timeout", Name: host, IsTimeout: true}
		}
		return ips("192.0.2.1", "2001:db8::1", "192.0.2.2"), nil
	}
	f := newPanelFailover()
	got, err := f.resolve(context.Background(), "panel.test", panelconn.IPv4Only)
	if err != nil || strings.Join(keys(got), ",") != "192.0.2.1,192.0.2.2" || network.Load() != "ip4" {
		t.Fatalf("ipv4_only resolve: %v %v %v", got, err, network.Load())
	}
	fail.Store(true)
	got, err = f.resolve(context.Background(), "panel.test", panelconn.Auto)
	if err != nil || len(got) != 3 {
		t.Fatalf("DNS failure must fall back to the IPs seen before: %v %v", got, err)
	}
	if _, err := newPanelFailover().resolve(context.Background(), "panel.test", panelconn.Auto); err == nil {
		t.Fatal("no known IPs and no DNS must be an error")
	}
	lit, err := f.resolve(context.Background(), "203.0.113.9", panelconn.Auto)
	if err != nil || len(lit) != 1 || lit[0] != ip("203.0.113.9") {
		t.Fatalf("literal: %v %v", lit, err)
	}
}

// ---- dial rounds over a scripted network ------------------------------------------

// scriptNet scripts what each ip:port does. TCP refusal/timeout happens in the
// dialers; everything after TCP happens in the fake wsDial.
type scriptNet struct {
	mu       sync.Mutex
	dns      map[string][]netip.Addr // host → ips (missing = NXDOMAIN)
	dnsDown  bool
	tcpFail  map[string]bool   // ip:port → SYN never answered
	after    map[string]string // ip:port → "stall" | "502" | "notls" | "" (ok)
	wsServer string            // real ws server (host:port) a successful attempt connects to
	attempts []string          // scheme@ip:port of every TCP connection
}

type fakeConn struct {
	net.Conn
	remote net.Addr
}

func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c *fakeConn) Close() error         { return nil }

func (n *scriptNet) dialAddr(address string) (net.Conn, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tcpFail[address] {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	}
	ta, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	return &fakeConn{remote: ta}, nil
}

func (n *scriptNet) install(t *testing.T) {
	t.Helper()
	oldNative, oldPinned, oldDial, oldLookup := panelDialNative, panelDialPinned, wsDial, panelLookupNetIP
	t.Cleanup(func() {
		panelDialNative, panelDialPinned, wsDial, panelLookupNetIP = oldNative, oldPinned, oldDial, oldLookup
	})
	panelLookupNetIP = func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.dnsDown {
			return nil, &net.DNSError{Err: "i/o timeout", Name: host, IsTimeout: true}
		}
		if a, ok := n.dns[host]; ok {
			return append([]netip.Addr(nil), a...), nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	// Native = Go dialer: resolve, then every address in order until one connects.
	panelDialNative = func(ctx context.Context, _ string, address string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(address)
		if lit, err := netip.ParseAddr(host); err == nil {
			return n.dialAddr(net.JoinHostPort(lit.String(), port))
		}
		addrs, err := panelLookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: err}
		}
		var first error
		for _, a := range addrs {
			c, err := n.dialAddr(net.JoinHostPort(a.String(), port))
			if err == nil {
				return c, nil
			}
			if first == nil {
				first = err
			}
		}
		return nil, first
	}
	panelDialPinned = func(_ context.Context, address string) (net.Conn, error) { return n.dialAddr(address) }
	wsDial = func(ctx context.Context, d *websocket.Dialer, raw string) (*websocket.Conn, *http.Response, error) {
		u, _ := url.Parse(raw)
		port := u.Port()
		if port == "" {
			port = map[string]string{"wss": "443", "ws": "80"}[u.Scheme]
		}
		c, err := d.NetDialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
		if err != nil {
			return nil, nil, err
		}
		where := c.RemoteAddr().String()
		n.mu.Lock()
		n.attempts = append(n.attempts, u.Scheme+"@"+where)
		behaviour := n.after[where]
		srv := n.wsServer
		n.mu.Unlock()
		switch behaviour {
		case "stall":
			<-ctx.Done()
			return nil, nil, ctx.Err()
		case "502", "404":
			code := 502
			if behaviour == "404" {
				code = 404
			}
			return nil, &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, websocket.ErrBadHandshake
		case "notls":
			return nil, nil, tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}
		}
		real, resp, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+srv+"/system-info", nil)
		return real, resp, err
	}
}

func (n *scriptNet) take() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.attempts
	n.attempts = nil
	return out
}

// wsTestServer answers pings while serving; behaviour can switch at runtime.
type wsTestServer struct {
	*httptest.Server
	mode     atomic.Value // "serve" | "first" (one message, then never reads → no pongs)
	stall    atomic.Bool  // serving sessions stop reading (no more pongs), TCP stays up
	accepted atomic.Int64
	open     atomic.Int64
	mu       sync.Mutex
	drop     chan struct{} // closed by dropAll: every session is closed by the "panel"
	stopped  chan struct{}
}

func newWSTestServer(t *testing.T) *wsTestServer {
	s := &wsTestServer{drop: make(chan struct{}), stopped: make(chan struct{})}
	s.mode.Store("serve")
	up := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flow/test" {
			fmt.Fprint(w, "test")
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.accepted.Add(1)
		s.open.Add(1)
		defer s.open.Add(-1)
		defer c.Close()
		s.mu.Lock()
		drop := s.drop
		s.mu.Unlock()
		wait := func() {
			select {
			case <-drop:
			case <-s.stopped:
			case <-time.After(30 * time.Second):
			}
		}
		if s.mode.Load().(string) == "first" {
			_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello"}`))
			wait()
			return
		}
		go func() {
			select {
			case <-drop:
				c.Close()
			case <-s.stopped:
				c.Close()
			}
		}()
		for {
			if s.stall.Load() {
				wait()
				return
			}
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(func() { close(s.stopped); s.Server.Close() })
	return s
}

// dropAll closes every open session from the panel side.
func (s *wsTestServer) dropAll() {
	s.mu.Lock()
	close(s.drop)
	s.drop = make(chan struct{})
	s.mu.Unlock()
}

func (s *wsTestServer) hostPort() string { return strings.TrimPrefix(s.URL, "http://") }

func shortTimings(t *testing.T) {
	t.Helper()
	old := []time.Duration{panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout, panelHealthyAfter, panelFailoverTTL, panelDialRoundBudget, wsPingInterval}
	panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout = 300*time.Millisecond, 400*time.Millisecond, 900*time.Millisecond
	panelHealthyAfter, panelFailoverTTL, panelDialRoundBudget, wsPingInterval = 600*time.Millisecond, 1500*time.Millisecond, 2*time.Second, 200*time.Millisecond
	t.Cleanup(func() {
		panelDialAttemptTimeout, panelHelloTimeout, panelPongStallTimeout, panelHealthyAfter = old[0], old[1], old[2], old[3]
		panelFailoverTTL, panelDialRoundBudget, wsPingInterval = old[4], old[5], old[6]
	})
}

func newRoundReporter() *WebSocketReporter {
	ctx, cancel := context.WithCancel(context.Background())
	return &WebSocketReporter{ctx: ctx, cancel: cancel, failover: newPanelFailover(), curBackoff: initialBackoff}
}

func dialOnce(t *testing.T, w *WebSocketReporter, addr string) (*panelDialResult, error) {
	t.Helper()
	res, err := w.dialPanelRound(w.ctx, *websocket.DefaultDialer, parsePanelTarget(addr), "/system-info?type=1&secret=s")
	if res != nil {
		res.conn.Close()
	}
	return res, err
}

func TestDialRoundHealthyPrimaryIsOneNativeAttempt(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")}, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	for i := 0; i < 3; i++ {
		res, err := dialOnce(t, w, "panel.test")
		if err != nil || res.scheme != "wss" || !res.native {
			t.Fatalf("round %d: %+v %v", i, res, err)
		}
		if got := n.take(); len(got) != 1 || got[0] != "wss@192.0.2.1:443" {
			t.Fatalf("round %d attempts: %v", i, got)
		}
	}
}

func TestDialRoundTLSStallOnOneIPUsesTheOther(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")},
		after: map[string]string{"192.0.2.1:443": "stall"}, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	start := time.Now()
	res, err := dialOnce(t, w, "panel.test")
	if err != nil || res.scheme != "wss" || res.ip != ip("192.0.2.2") || res.native {
		t.Fatalf("expected pinned wss on the second IP: %+v %v", res, err)
	}
	if got := n.take(); strings.Join(got, ",") != "wss@192.0.2.1:443,wss@192.0.2.2:443" {
		t.Fatalf("attempts: %v", got)
	}
	if time.Since(start) > 2*panelDialAttemptTimeout {
		t.Fatalf("too slow: %s", time.Since(start))
	}
	// Next round: the stalled IP cools down, so the round goes straight to the good one.
	res, err = dialOnce(t, w, "panel.test")
	if err != nil || res.ip != ip("192.0.2.2") {
		t.Fatalf("second round: %+v %v", res, err)
	}
	if got := n.take(); strings.Join(got, ",") != "wss@192.0.2.2:443" {
		t.Fatalf("second round must skip the cooled-down IP: %v", got)
	}
}

func TestDialRoundFallsBackToWSOnlyAfterEveryWSSAddress(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")},
		after: map[string]string{"192.0.2.1:443": "stall", "192.0.2.2:443": "stall"}, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	res, err := dialOnce(t, w, "panel.test")
	if err != nil || res.scheme != "ws" {
		t.Fatalf("expected ws fallback: %+v %v", res, err)
	}
	if got := strings.Join(n.take(), ","); got != "wss@192.0.2.1:443,wss@192.0.2.2:443,ws@192.0.2.1:80" {
		t.Fatalf("attempt order: %s", got)
	}
	// wss is still first next round (no sticky scheme), on the oldest failure first.
	n.mu.Lock()
	n.after = map[string]string{}
	n.mu.Unlock()
	res, err = dialOnce(t, w, "panel.test")
	if err != nil || res.scheme != "wss" {
		t.Fatalf("next round must try wss first again: %+v %v", res, err)
	}
}

func TestDialRoundHTTPStatusDoesNotWalkIPs(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")},
		after:    map[string]string{"192.0.2.1:443": "502", "192.0.2.2:443": "502", "192.0.2.1:80": "502", "192.0.2.2:80": "502"},
		wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	if _, err := dialOnce(t, w, "panel.test"); err == nil {
		t.Fatal("502 everywhere must fail")
	}
	if got := strings.Join(n.take(), ","); got != "wss@192.0.2.1:443,ws@192.0.2.1:80" {
		t.Fatalf("panel down = same two attempts as before plan 122: %s", got)
	}
	if w.failover.hasCooldown("wss", "panel.test") {
		t.Fatal("a status answer must not cool the address down")
	}
}

func TestDialRoundPlainPortKeepsWSFirst(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{after: map[string]string{"203.0.113.9:63665": ""}, wsServer: srv.hostPort()}
	n.install(t)
	// wss to the plain port answers with an HTTP record → no TLS there.
	n.after["203.0.113.9:63665"] = "notls"
	w := newRoundReporter()
	// The script keys only by ip:port, so let ws succeed by switching after the wss attempt.
	oldDial := wsDial
	wsDial = func(ctx context.Context, d *websocket.Dialer, raw string) (*websocket.Conn, *http.Response, error) {
		if strings.HasPrefix(raw, "ws://") {
			n.mu.Lock()
			n.after["203.0.113.9:63665"] = ""
			n.mu.Unlock()
		}
		return oldDial(ctx, d, raw)
	}
	res, err := dialOnce(t, w, "203.0.113.9:63665")
	if err != nil || res.scheme != "ws" {
		t.Fatalf("plain port: %+v %v", res, err)
	}
	n.take()
	res, err = dialOnce(t, w, "203.0.113.9:63665")
	if err != nil || res.scheme != "ws" {
		t.Fatalf("second round: %+v %v", res, err)
	}
	if got := n.take(); len(got) != 1 || !strings.HasPrefix(got[0], "ws@") {
		t.Fatalf("a port without TLS must dial ws first like before: %v", got)
	}
}

func TestDialRoundDNSFailureUsesKnownIPs(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")}, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	res, err := dialOnce(t, w, "panel.test")
	if err != nil {
		t.Fatal(err)
	}
	w.failover.healthy(res.scheme, "panel.test", "panel.test", res.ip)
	n.take()
	n.mu.Lock()
	n.dnsDown = true
	n.mu.Unlock()
	res, err = dialOnce(t, w, "panel.test")
	if err != nil || res.scheme != "wss" || res.ip != ip("192.0.2.1") {
		t.Fatalf("DNS down must fall back to the last healthy IP: %+v %v", res, err)
	}
}

func TestDialRoundBudgetBoundsAWorstCaseRound(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	many := ips("192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.6", "192.0.2.7", "192.0.2.8")
	after := map[string]string{}
	for _, a := range many {
		after[a.String()+":443"] = "stall"
		after[a.String()+":80"] = "stall"
	}
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": many}, after: after, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	start := time.Now()
	if _, err := dialOnce(t, w, "panel.test"); err == nil {
		t.Fatal("everything stalls")
	}
	took := time.Since(start)
	if limit := panelDialRoundBudget + 3*panelDialAttemptTimeout; took > limit {
		t.Fatalf("round took %s > %s", took, limit)
	}
	got := n.take()
	ws := 0
	for _, a := range got {
		if strings.HasPrefix(a, "ws@") {
			ws++
		}
	}
	if ws == 0 {
		t.Fatalf("the fallback scheme must still get its first attempt: %v", got)
	}
	if len(got) >= 16 {
		t.Fatalf("budget did not stop the round: %d attempts", len(got))
	}
}

// ---- sessions --------------------------------------------------------------------

func sessionReporter(t *testing.T, addr string) *WebSocketReporter {
	t.Helper()
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"addr":"`+addr+`","secret":"s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	oldNft, oldIPs := newNftablesManager, reportPublicIPsOnConnect
	newNftablesManager = func() (NftablesManagerInterface, error) { return nil, nil }
	reportPublicIPsOnConnect = func(*WebSocketReporter) {}
	t.Cleanup(func() { os.Chdir(cwd); newNftablesManager, reportPublicIPsOnConnect = oldNft, oldIPs })
	w := NewWebSocketReporter("", "s")
	w.addr = addr
	w.secret = "s"
	w.nodeID = 1
	w.pingInterval = 100 * time.Millisecond
	return w
}

func runSession(t *testing.T, w *WebSocketReporter) sessionOutcome {
	t.Helper()
	if err := w.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return w.handleConnection()
}

func TestSessionWithoutHelloReplyIsAbandonedQuickly(t *testing.T) {
	shortTimings(t)
	panelFailoverTTL = time.Minute // outlive the backoff sleep inside afterSession
	srv := newWSTestServer(t)
	srv.mode.Store("first") // one message, then nothing (no pongs) = node 48's truncated ws:80
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1")}, wsServer: srv.hostPort()}
	n.install(t)
	w := sessionReporter(t, "panel.test")
	defer w.cancel()
	start := time.Now()
	out := runSession(t, w)
	if out.reason != sessionNoHello || out.healthy || out.helloOK {
		t.Fatalf("outcome %+v", out)
	}
	if took := time.Since(start); took > panelHelloTimeout+time.Second {
		t.Fatalf("abandoned after %s, bound %s", took, panelHelloTimeout)
	}
	if !w.afterSession(out) {
		t.Fatal("afterSession stopped")
	}
	if !w.failover.hasCooldown("wss", "panel.test") {
		t.Fatal("the truncated candidate must cool down")
	}
	if w.curBackoff <= initialBackoff {
		t.Fatal("a failed session must grow the backoff")
	}
}

func TestSessionStallAfterHelloEndsAsFailure(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1")}, wsServer: srv.hostPort()}
	n.install(t)
	w := sessionReporter(t, "panel.test")
	defer w.cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		srv.stall.Store(true) // the panel stops answering; the TCP connection stays up
	}()
	start := time.Now()
	out := runSession(t, w)
	if out.reason != sessionStalled || !out.helloOK || out.healthy {
		t.Fatalf("expected a stall after a good hello: %+v", out)
	}
	if took := time.Since(start); took > 300*time.Millisecond+panelPongStallTimeout+wsPingInterval+500*time.Millisecond {
		t.Fatalf("stall detected after %s", took)
	}
}

func TestHealthySessionRecordsLastGoodAndResetsBackoff(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")}, wsServer: srv.hostPort()}
	n.install(t)
	w := sessionReporter(t, "panel.test")
	defer w.cancel()
	w.curBackoff = 32 * time.Second
	go func() {
		time.Sleep(panelHealthyAfter + 300*time.Millisecond)
		w.reconnectPanelForTest()
	}()
	out := runSession(t, w)
	if out.reason != sessionRequested || !out.helloOK {
		t.Fatalf("outcome %+v", out)
	}
	out.reason = sessionEnded // treat as an ordinary end to check the healthy path
	if !out.healthy {
		t.Fatalf("should be healthy: %+v", out)
	}
	w.afterSession(out)
	if w.curBackoff != initialBackoff || w.failover.lastGood["wss|panel.test"] != ip("192.0.2.1") {
		t.Fatalf("healthy bookkeeping: backoff %s lastGood %v", w.curBackoff, w.failover.lastGood)
	}
}

func TestDegradedSessionSwitchesBackWhenPrimaryAnswers(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1")},
		after: map[string]string{"192.0.2.1:443": "stall"}, wsServer: srv.hostPort()}
	n.install(t)
	var probes atomic.Int64
	var primaryUp atomic.Bool
	oldProbe := panelProbeDo
	panelProbeDo = func(req *http.Request) (*http.Response, error) {
		probes.Add(1)
		if req.URL.Scheme != "https" || req.URL.Path != "/flow/test" {
			return nil, fmt.Errorf("unexpected probe %s", req.URL)
		}
		if !primaryUp.Load() {
			return nil, errors.New("tls handshake timeout")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("test"))}, nil
	}
	t.Cleanup(func() { panelProbeDo = oldProbe })
	w := sessionReporter(t, "panel.test")
	defer w.cancel()
	go func() {
		time.Sleep(3 * panelFailoverTTL / 2)
		primaryUp.Store(true)
		n.mu.Lock()
		n.after = map[string]string{}
		n.mu.Unlock()
	}()
	start := time.Now()
	out := runSession(t, w)
	if w.session.scheme != "ws" {
		t.Fatalf("expected a ws session first, got %s", w.session.scheme)
	}
	if out.reason != sessionSwitch {
		t.Fatalf("expected a switch back, got %+v", out)
	}
	if took := time.Since(start); took > 4*panelFailoverTTL {
		t.Fatalf("switch back took %s (TTL %s)", took, panelFailoverTTL)
	}
	if probes.Load() < 1 {
		t.Fatal("no probe ran")
	}
	if w.failover.hasCooldown("wss", "panel.test") {
		t.Fatal("primary cooldown must be lifted after a good probe")
	}
	w.afterSession(out)
	if err := w.connect(); err != nil {
		t.Fatal(err)
	}
	if w.session.scheme != "wss" {
		t.Fatalf("reconnect after the switch must be wss, got %s", w.session.scheme)
	}
	w.reconnectPanelForTest()
}

func TestPrimarySessionNeverProbes(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1")}, wsServer: srv.hostPort()}
	n.install(t)
	var probes atomic.Int64
	oldProbe := panelProbeDo
	panelProbeDo = func(*http.Request) (*http.Response, error) { probes.Add(1); return nil, errors.New("no") }
	t.Cleanup(func() { panelProbeDo = oldProbe })
	w := sessionReporter(t, "panel.test")
	defer w.cancel()
	go func() { time.Sleep(3 * panelFailoverTTL); w.reconnectPanelForTest() }()
	out := runSession(t, w)
	if out.reason != sessionRequested || probes.Load() != 0 {
		t.Fatalf("primary session probed %d times (%+v)", probes.Load(), out)
	}
}

// run() over many forced disconnects: one session at a time, no goroutine growth.
func TestRunForcedDisconnectsKeepOneSessionAndNoLeaks(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")}, wsServer: srv.hostPort()}
	n.install(t)
	w := sessionReporter(t, "panel.test")
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	waitAccepted := func(want int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for srv.accepted.Load() < want {
			if time.Now().After(deadline) {
				t.Fatalf("accepted %d, want %d", srv.accepted.Load(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitAccepted(1)
	time.Sleep(panelHealthyAfter + 200*time.Millisecond)
	base := runtime.NumGoroutine()
	for i := int64(2); i <= 6; i++ {
		srv.dropAll() // panel side drops every session
		waitAccepted(i)
		if open := srv.open.Load(); open > 1 {
			t.Fatalf("%d sessions open at once", open)
		}
		time.Sleep(panelHealthyAfter + 200*time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if g := runtime.NumGoroutine(); g > base+3 {
		t.Fatalf("goroutines %d after 5 reconnects (base %d)", g, base)
	}
	w.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
	if got := n.take(); len(got) == 0 || strings.Contains(strings.Join(got, ","), "ws@") {
		t.Fatalf("healthy wss line must never touch ws: %v", got)
	}
}

func (w *WebSocketReporter) reconnectPanelForTest() { w.reconnectPanel() }

// A real gorilla dial of wss:// against a plain HTTP port must classify as
// "no TLS here" (that is what keeps directly connected nodes on ws first).
func TestRealPlainPortClassifiesAsNoTLS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 400) }))
	defer srv.Close()
	w := newRoundReporter()
	defer w.cancel()
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	conn, resp, tcpIP, err := w.panelAttempt(context.Background(), *websocket.DefaultDialer, "wss://"+hostPort+"/system-info", netip.Addr{})
	if conn != nil {
		conn.Close()
	}
	if err == nil || !tcpIP.IsValid() {
		t.Fatalf("expected a TLS failure after TCP: %v %v", err, tcpIP)
	}
	if got := classifyDialFailure(err, resp, tcpIP.IsValid()); got != failNoTLS {
		t.Fatalf("classified %v (%v)", got, err)
	}
}

// A panel restart answers wss with 502 for a moment. Once wss has carried a healthy
// session, the round must not fall back to ws (that raced the restart and left
// nodes on ws); a fresh process keeps the old fallback.
func TestDialRoundNoWSFallbackOn5xxAfterHealthyWSS(t *testing.T) {
	shortTimings(t)
	srv := newWSTestServer(t)
	n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1", "192.0.2.2")},
		after: map[string]string{"192.0.2.1:443": "502", "192.0.2.2:443": "502"}, wsServer: srv.hostPort()}
	n.install(t)
	w := newRoundReporter()
	res, err := dialOnce(t, w, "panel.test")
	if err != nil || res.scheme != "ws" {
		t.Fatalf("fresh process must still fall back to ws on 502: %+v %v", res, err)
	}
	n.take()
	w.failover.healthy("wss", "panel.test", "panel.test", ip("192.0.2.1"))
	if _, err := dialOnce(t, w, "panel.test"); err == nil || !strings.Contains(err.Error(), "面板暂不可用") {
		t.Fatalf("after a healthy wss session a 502 must end the round: %v", err)
	}
	if got := n.take(); strings.Join(got, ",") != "wss@192.0.2.1:443" {
		t.Fatalf("only the wss attempt, no ws: %v", got)
	}
	if w.failover.hasCooldown("wss", "panel.test") {
		t.Fatal("a 502 must not cool the address down")
	}
	n.mu.Lock()
	n.after = map[string]string{"192.0.2.1:443": "404", "192.0.2.2:443": "404"}
	n.mu.Unlock()
	// Non-5xx answers keep the old behaviour (try the other scheme).
	if res, err := dialOnce(t, w, "panel.test"); err != nil || res.scheme != "ws" {
		t.Fatalf("4xx must still fall back to ws: %+v %v", res, err)
	}
}
