package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "plan245"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"plan245"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func newTestListener(t *testing.T) (*silentTLSListener, string) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := newSilentTLSListener(inner, testTLSConfig(t), nil).(*silentTLSListener)
	t.Cleanup(func() { l.Close() })
	return l, inner.Addr().String()
}

func dialTLS(addr string) (*tls.Conn, error) {
	d := &net.Dialer{Timeout: 3 * time.Second}
	return tls.DialWithDialer(d, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
}

// Plan 245: a client that never sends a ClientHello must not hold up the
// next client's handshake (it used to block Accept for up to 10 s).
func TestSilentTLSSilentClientDoesNotBlockOthers(t *testing.T) {
	l, addr := newTestListener(t)
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	time.Sleep(50 * time.Millisecond) // the silent conn is accepted first

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	start := time.Now()
	c, err := dialTLS(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	select {
	case sc := <-accepted:
		defer sc.Close()
		if _, ok := sc.(*tls.Conn); !ok {
			t.Fatalf("accepted %T, want *tls.Conn", sc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept blocked behind the silent client")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("handshake took %v", d)
	}
	// Data flows both ways on the handed-over conn.
	go func() { _, _ = c.Write([]byte("ping")) }()
}

// Garbage (non-TLS) is closed silently and the next real client is served.
func TestSilentTLSGarbageClosedQuietly(t *testing.T) {
	l, addr := newTestListener(t)
	g, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = g.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	_ = g.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	for {
		if _, err := g.Read(buf); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatal("garbage conn was not closed")
			}
			break
		}
	}
	g.Close()

	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
		done <- err
	}()
	c, err := dialTLS(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no accept after garbage")
	}
}

// Many concurrent clients finish in about one handshake, not N of them.
func TestSilentTLSConcurrentHandshakes(t *testing.T) {
	l, addr := newTestListener(t)
	var served atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			served.Add(1)
			go func() {
				b := make([]byte, 1)
				_, _ = c.Read(b)
				c.Close()
			}()
		}
	}()
	// 8 silent conns first: with the old serial Accept every real client
	// would wait for them.
	for i := 0; i < 8; i++ {
		s, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
	}
	errs := make(chan error, 20)
	start := time.Now()
	for i := 0; i < 20; i++ {
		go func() {
			c, err := dialTLS(addr)
			if err == nil {
				_, err = c.Write([]byte("x"))
				c.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("client: %v", err)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("20 clients took %v", d)
	}
	deadline := time.Now().Add(2 * time.Second)
	for served.Load() < 20 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := served.Load(); n != 20 {
		t.Fatalf("served %d of 20", n)
	}
}

// Close unblocks Accept (net.ErrClosed) and releases pending handshakes.
func TestSilentTLSCloseUnblocks(t *testing.T) {
	l, addr := newTestListener(t)
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	res := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		res <- err
	}()
	time.Sleep(50 * time.Millisecond)
	l.Close()
	select {
	case err := <-res:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("accept after close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept not released by Close")
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second accept: %v", err)
	}
}

type failingListener struct {
	net.Listener
	calls atomic.Int32
	temp  int32 // the first temp calls fail with a temporary error
}

type tempErr struct{}

func (tempErr) Error() string   { return "temporary" }
func (tempErr) Timeout() bool   { return false }
func (tempErr) Temporary() bool { return true }

var errFatal = errors.New("fatal accept")

func (f *failingListener) Accept() (net.Conn, error) {
	if f.calls.Add(1) <= f.temp {
		return nil, tempErr{}
	}
	return nil, errFatal
}

// Temporary errors reach the service loop one by one (it backs off and
// calls again); a fatal error is returned to every later Accept.
func TestSilentTLSAcceptErrors(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	f := &failingListener{Listener: inner, temp: 2}
	l := newSilentTLSListener(f, testTLSConfig(t), nil)
	defer l.Close()
	for i := 0; i < 2; i++ {
		_, err := l.Accept()
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Temporary() {
			t.Fatalf("accept %d: %v, want temporary", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := l.Accept(); !errors.Is(err, errFatal) {
			t.Fatalf("accept after fatal: %v", err)
		}
	}
}

// The in-flight cap holds: with the cap's worth of silent conns, a further
// client waits (kernel backlog) until one of them goes away.
func TestSilentTLSCap(t *testing.T) {
	l, addr := newTestListener(t)
	silents := make([]net.Conn, 0, silentMaxHandshakes)
	for i := 0; i < silentMaxHandshakes; i++ {
		s, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		silents = append(silents, s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(l.sem) < silentMaxHandshakes && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(l.sem); n != silentMaxHandshakes {
		t.Fatalf("in flight %d, want %d", n, silentMaxHandshakes)
	}
	got := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			got <- c
		}
	}()
	go func() {
		c, err := dialTLS(addr)
		if err == nil {
			time.Sleep(3 * time.Second)
			c.Close()
		}
	}()
	select {
	case c := <-got:
		c.Close()
		t.Fatal("accepted beyond the cap")
	case <-time.After(300 * time.Millisecond):
	}
	if n := len(l.sem); n > silentMaxHandshakes {
		t.Fatalf("in flight %d over cap", n)
	}
	silents[0].Close() // a slot frees up → the waiting client is served
	select {
	case c := <-got:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("client not served after a slot freed")
	}
	for _, s := range silents[1:] {
		s.Close()
	}
}
