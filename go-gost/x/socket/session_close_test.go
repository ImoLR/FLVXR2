package socket

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDescribeSessionCause(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	for _, tc := range []struct {
		c    sessionCause
		want string
	}{
		{sessionCause{source: causeRead, err: reset}, "读：对端重置连接（RST）"},
		{sessionCause{source: causeRead, err: &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: io.ErrUnexpectedEOF.Error()}}, "读：连接被断开（对端没发关闭帧，EOF）"},
		{sessionCause{source: causeCloseFrame, err: &websocket.CloseError{Code: 1001, Text: "panel restarting"}}, "面板端发来关闭帧（code=1001 panel restarting）"},
		{sessionCause{source: causeRead, err: timeout}, "读：60 秒没收到面板任何数据"},
		{sessionCause{source: causeWrite, err: timeout}, "写指标：5 秒内发不出去（写超时）"},
		{sessionCause{source: causePing, err: &net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.EPIPE)}}, "写心跳：连接已断（broken pipe）"},
		{sessionCause{source: causeNoHello, err: errPanelNoReply}, "面板 20 秒内没回应 hello"},
		{sessionCause{source: causeStall, err: errPanelNoReply}, "面板 45 秒没回应心跳"},
		{sessionCause{source: causeRequested}, "按面板设置主动重连"},
		{sessionCause{source: causeRead, err: errors.New("x")}, "读：连接错误"},
	} {
		if got, _ := describeSessionCause(tc.c, "psk"); got != tc.want {
			t.Errorf("%+v → %q, want %q", tc.c, got, tc.want)
		}
	}
	// The raw error is attached, secrets redacted.
	_, detail := describeSessionCause(sessionCause{source: causeRead, err: errors.New("dial wss://p/system-info?secret=psk: reset")}, "psk")
	if strings.Contains(detail, "psk") {
		t.Fatalf("secret leaked: %s", detail)
	}
}

func TestSessionCloseLimiter(t *testing.T) {
	l := &sessionCloseLimiter{}
	now := time.Unix(100, 0)
	if ok, skipped := l.allow(now); !ok || skipped != 0 {
		t.Fatal("first line")
	}
	for i := 1; i <= 3; i++ {
		if ok, _ := l.allow(now.Add(time.Duration(i) * time.Second)); ok {
			t.Fatal("line within 10 s")
		}
	}
	if ok, skipped := l.allow(now.Add(11 * time.Second)); !ok || skipped != 3 {
		t.Fatalf("after the window: ok=%v skipped=%d", ok, skipped)
	}
}

// closingPanel upgrades and ends the session its own way once the agent's
// first metric arrived.
func closingPanel(t *testing.T, how string) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, _, err := c.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.TextMessage {
				continue
			}
			time.Sleep(50 * time.Millisecond)
			switch how {
			case "close-frame":
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "panel restarting"), time.Now().Add(time.Second))
				time.Sleep(200 * time.Millisecond)
			case "reset":
				tcp := c.UnderlyingConn().(*net.TCPConn)
				_ = tcp.SetLinger(0)
				_ = tcp.Close()
			case "fin":
				_ = c.UnderlyingConn().Close()
			}
			return
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestSessionCloseLineNamesTheReason(t *testing.T) {
	for how, want := range map[string]string{
		"close-frame": "面板端发来关闭帧（code=1001 panel restarting）",
		"reset":       "对端重置连接（RST）",
		"fin":         "对端没发关闭帧，EOF",
	} {
		t.Run(how, func(t *testing.T) {
			shortTimings(t)
			sessionCloseLog = &sessionCloseLimiter{}
			var mu sync.Mutex
			var lines []string
			old := sessionPrint
			sessionPrint = func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }
			t.Cleanup(func() { sessionPrint = old })
			n := &scriptNet{dns: map[string][]netip.Addr{"panel.test": ips("192.0.2.1")}, wsServer: closingPanel(t, how)}
			n.install(t)
			w := sessionReporter(t, "panel.test")
			defer w.cancel()
			runSession(t, w)
			mu.Lock()
			defer mu.Unlock()
			if len(lines) != 1 {
				t.Fatalf("want one close line, got %q", lines)
			}
			l := lines[0]
			t.Logf("%s", l)
			if !strings.HasPrefix(l, "🔌 WebSocket连接已关闭：") || !strings.Contains(l, want) || !strings.Contains(l, "；持续 ") ||
				!strings.Contains(l, "IPv4 ") || !strings.Contains(l, "（本地 ") {
				t.Fatalf("close line %q, want %q", l, want)
			}
			if how == "reset" && !strings.Contains(l, "TCP rtt=") {
				t.Fatalf("no TCP info on a reset session: %q", l)
			}
		})
	}
}

func TestPublicIPReportIsNotRepeatedOnEveryReconnect(t *testing.T) {
	publicIPReportMu.Lock()
	publicIPLastReport, publicIPLastLocal = time.Time{}, ""
	publicIPReportMu.Unlock()
	local := &net.TCPAddr{IP: net.ParseIP("192.168.11.211"), Port: 40000}
	now := time.Unix(1000, 0)
	if !publicIPReportDue(local, now) {
		t.Fatal("first session must report")
	}
	if publicIPReportDue(&net.TCPAddr{IP: local.IP, Port: 40001}, now.Add(time.Minute)) {
		t.Fatal("reconnect from the same address within 10 min reported again")
	}
	if !publicIPReportDue(&net.TCPAddr{IP: net.ParseIP("192.168.11.212"), Port: 1}, now.Add(2*time.Minute)) {
		t.Fatal("a new local address must report")
	}
	if !publicIPReportDue(&net.TCPAddr{IP: net.ParseIP("192.168.11.212"), Port: 1}, now.Add(13*time.Minute)) {
		t.Fatal("report after 10 minutes")
	}
}
