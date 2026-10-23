package ws

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/http/middleware"
)

type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *lineSink) add(l string) { s.mu.Lock(); s.lines = append(s.lines, l); s.mu.Unlock() }
func (s *lineSink) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func captureSessionLines(t *testing.T) *lineSink {
	t.Helper()
	sink := &lineSink{}
	orig := nodeSessionLogPrint
	nodeSessionLogPrint = sink.add
	t.Cleanup(func() { nodeSessionLogPrint = orig })
	return sink
}

func waitLine(t *testing.T, sink *lineSink, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range sink.all() {
			if strings.Contains(l, want) {
				return l
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no session line containing %q: %v", want, sink.all())
	return ""
}

func dialNode(t *testing.T, url string, hdr http.Header) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNodeSessionEndReplacedByNewConnection(t *testing.T) {
	sink := captureSessionLines(t)
	s, _, url := panelFamilyServer(t, "")
	old := dialNode(t, url, nil)
	defer old.Close()
	waitFor(t, func() bool { _, ok := s.nodeSessionOf(1); return ok })
	_ = old.WriteMessage(websocket.TextMessage, []byte(`{"type":"metric","data":{"uptime":1}}`))
	time.Sleep(50 * time.Millisecond)
	fresh := dialNode(t, url, nil)
	defer fresh.Close()
	line := waitLine(t, sink, "replaced by a newer connection")
	if !strings.Contains(line, "[ws] node 1 session ended after") || !strings.Contains(line, "via direct client=127.0.0.1 ipv4") ||
		!strings.Contains(line, "old connection silent") || !strings.Contains(line, "ended sessions of this node in the last hour: 1") {
		t.Fatalf("line: %s", line)
	}
}

func TestNodeSessionEndReasons(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		end        func(c *websocket.Conn)
	}{
		{"close frame", `close frame code=1001 "agent stopping"`, func(c *websocket.Conn) {
			_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "agent stopping"), time.Now().Add(time.Second))
		}},
		{"eof", "connection closed without close frame (EOF)", func(c *websocket.Conn) {
			_ = c.UnderlyingConn().(*net.TCPConn).CloseWrite()
		}},
		{"reset", "connection reset", func(c *websocket.Conn) {
			tcp := c.UnderlyingConn().(*net.TCPConn)
			_ = tcp.SetLinger(0)
			_ = tcp.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := captureSessionLines(t)
			s, _, url := panelFamilyServer(t, "")
			c := dialNode(t, url, nil)
			defer c.Close()
			waitFor(t, func() bool { _, ok := s.nodeSessionOf(1); return ok })
			tc.end(c)
			line := waitLine(t, sink, "node 1 session ended")
			if !strings.Contains(line, tc.want) {
				t.Fatalf("want %q in %s", tc.want, line)
			}
		})
	}
}

func TestNodeSessionViaCloudflare(t *testing.T) {
	r := httptest.NewRequest("GET", "/system-info?type=1", nil)
	r.RemoteAddr = "127.0.0.1:50000" // cloudflared on the host
	r.Header.Set("Cf-Ray", "8d3c2b1a0f9e8d7c-NRT")
	r.Header.Set("Cf-Connecting-Ip", "123.204.3.9")
	if got := nodeSessionVia(r); got != "cloudflare colo=NRT client=123.204.3.9 ipv4" {
		t.Fatalf("via = %q", got)
	}
	r.Header.Set("Cf-Connecting-Ip", "2001:b011:b000:c4b4::1")
	if got := nodeSessionVia(r); got != "cloudflare colo=NRT client=2001:b011:b000:c4b4::1 ipv6" {
		t.Fatalf("via = %q", got)
	}
	d := httptest.NewRequest("GET", "/system-info?type=1", nil)
	d.RemoteAddr = "143.20.196.27:41000"
	if got := nodeSessionVia(d); got != "direct client=143.20.196.27 ipv4" {
		t.Fatalf("via = %q", got)
	}
}

func TestClassifyNodeSessionEnd(t *testing.T) {
	timeout := &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}
	reset := &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	for _, tc := range []struct {
		err       error
		replaced  bool
		keepalive string
		want      string
	}{
		{nil, true, "", "replaced by a newer connection"},
		{net.ErrClosed, false, "write: broken pipe", "keepalive ping write failed: write: broken pipe"},
		{&websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: io.ErrUnexpectedEOF.Error()}, false, "", "without close frame"},
		{&websocket.CloseError{Code: 1001}, false, "", "close frame code=1001"},
		{timeout, false, "", "pong timeout"},
		{reset, false, "", "connection reset"},
		{fmt.Errorf("x: %w", net.ErrClosed), false, "", "closed by the panel"},
		{errors.New("boom"), false, "", "read error: boom"},
	} {
		if got := classifyNodeSessionEnd(tc.err, tc.replaced, tc.keepalive); !strings.Contains(got, tc.want) {
			t.Errorf("%v → %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestNodeSessionLogIsRateLimitedPerNode(t *testing.T) {
	var l nodeSessionLogger
	now := time.Unix(1000, 0)
	line := func(ends, skipped int) string { return fmt.Sprintf("ends=%d skipped=%d", ends, skipped) }
	if got := l.record(1, now, line); got != "ends=1 skipped=0" {
		t.Fatalf("first: %q", got)
	}
	if got := l.record(1, now.Add(2*time.Second), line); got != "" {
		t.Fatalf("second within 10 s logged: %q", got)
	}
	if got := l.record(2, now.Add(3*time.Second), line); got != "ends=1 skipped=0" {
		t.Fatalf("other node limited: %q", got)
	}
	if got := l.record(1, now.Add(11*time.Second), line); got != "ends=3 skipped=1" {
		t.Fatalf("after the window: %q", got)
	}
	if got := l.record(1, now.Add(2*time.Hour), line); got != "ends=1 skipped=0" {
		t.Fatalf("hour window: %q", got)
	}
}

// The access-log line of the hijacked agent request is replaced by the
// session line; browser sessions keep theirs.
func TestAgentSessionHasNoAccessLogLine(t *testing.T) {
	_ = captureSessionLines(t)
	var buf bytes.Buffer
	var mu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) }))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s, _, _ := panelFamilyServer(t, "")
	h := httptest.NewServer(middleware.RequestLog(s))
	defer h.Close()
	url := "ws" + strings.TrimPrefix(h.URL, "http") + "/system-info?type=1&secret=test-secret"
	c := dialNode(t, url, nil)
	waitFor(t, func() bool { _, ok := s.nodeSessionOf(1); return ok })
	_ = c.Close()
	waitFor(t, func() bool { _, ok := s.nodeSessionOf(1); return !ok })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(buf.String(), "GET /system-info") {
		t.Fatalf("access log line written: %s", buf.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func (s *Server) nodeSessionOf(id int64) (*nodeSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns, ok := s.nodes[id]
	return ns, ok
}
