package socket

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-gost/x/logsafe"
	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

// Close-reason logging (plan 138): one line per ended panel session that says
// what ended it, how long it lived and which address family / remote it used.
// Sessions end rarely, the line is still bounded (≤ 1 per 10 s; skipped ones
// are counted into the next line).

const sessionCloseLogEvery = 10 * time.Second

// Session cause sources.
const (
	causeRead       = "read"        // reading from the panel failed
	causeWrite      = "write"       // writing a metric failed
	causePing       = "ping"        // writing our keepalive ping failed
	causeHello      = "hello-write" // writing the hello ping failed
	causeResponse   = "response"    // writing a command response failed
	causeCloseFrame = "close-frame" // the panel side sent a close frame
	causeNoHello    = "no-hello"    // the hello ping was never answered
	causeStall      = "stall"       // our pings stopped getting pongs
	causeRequested  = "requested"   // reconnect asked for (settings change)
	causeSwitch     = "switch"      // switching back to the primary scheme
	causeStopped    = "stopped"     // agent stopping
)

// sessionCause is the first event that ended the current panel session.
type sessionCause struct {
	source string
	err    error
	at     time.Time
}

// noteSessionCauseLocked records the first cause of the current session
// (connMutex held). Later errors of the same session are consequences.
func (w *WebSocketReporter) noteSessionCauseLocked(conn *websocket.Conn, source string, err error) {
	if conn == nil || conn != w.conn || w.sessionCause.source != "" {
		return
	}
	w.sessionCause = sessionCause{source: source, err: err, at: time.Now()}
}

type sessionCloseLimiter struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

// allow reports whether a line may be written now and how many earlier lines
// were skipped since the last written one.
func (l *sessionCloseLimiter) allow(now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() && now.Sub(l.last) < sessionCloseLogEvery {
		l.suppressed++
		return false, 0
	}
	skipped := l.suppressed
	l.last, l.suppressed = now, 0
	return true, skipped
}

var sessionCloseLog = &sessionCloseLimiter{}

// sessionPrint is the output of the close line (tests replace it).
var sessionPrint = func(line string) { fmt.Println(line) }

func causeSourceText(source string) string {
	switch source {
	case causeRead:
		return "读"
	case causeWrite:
		return "写指标"
	case causePing:
		return "写心跳"
	case causeHello:
		return "写 hello"
	case causeResponse:
		return "写响应"
	}
	return source
}

// describeSessionCause turns the first cause into a short Chinese reason and
// the (redacted) raw error text.
func describeSessionCause(c sessionCause, secret string) (string, string) {
	detail := ""
	if c.err != nil {
		detail = logsafe.Text(logsafe.Error(c.err), secret)
		if len([]rune(detail)) > 200 {
			detail = string([]rune(detail)[:200]) + "…"
		}
	}
	switch c.source {
	case "":
		return "原因未知", detail
	case causeCloseFrame:
		var ce *websocket.CloseError
		if errors.As(c.err, &ce) {
			text := strings.TrimSpace(ce.Text)
			if text != "" {
				return fmt.Sprintf("面板端发来关闭帧（code=%d %s）", ce.Code, text), ""
			}
			return fmt.Sprintf("面板端发来关闭帧（code=%d）", ce.Code), ""
		}
		return "面板端发来关闭帧", detail
	case causeNoHello:
		return fmt.Sprintf("面板 %.0f 秒内没回应 hello", panelHelloTimeout.Seconds()), ""
	case causeStall:
		return fmt.Sprintf("面板 %.0f 秒没回应心跳", panelPongStallTimeout.Seconds()), ""
	case causeRequested:
		return "按面板设置主动重连", ""
	case causeSwitch:
		return "主线路已恢复，主动切回", ""
	case causeStopped:
		return "agent 停止", ""
	}
	src := causeSourceText(c.source)
	err := c.err
	var ce *websocket.CloseError
	var ne net.Error
	switch {
	case err == nil:
		return src + "：原因未知", ""
	case errors.As(err, &ce) && ce.Code == websocket.CloseAbnormalClosure:
		return src + "：连接被断开（对端没发关闭帧，EOF）", detail
	case errors.As(err, &ce):
		text := strings.TrimSpace(ce.Text)
		return fmt.Sprintf("%s：收到关闭帧（code=%d %s）", src, ce.Code, text), ""
	case errors.Is(err, syscall.ECONNRESET):
		return src + "：对端重置连接（RST）", detail
	case errors.Is(err, syscall.EPIPE):
		return src + "：连接已断（broken pipe）", detail
	case errors.Is(err, syscall.ETIMEDOUT):
		return src + "：TCP 超时（对端长时间无确认）", detail
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return src + "：网络不可达", detail
	case errors.As(err, &ne) && ne.Timeout():
		if c.source == causeRead {
			return fmt.Sprintf("%s：%.0f 秒没收到面板任何数据", src, reporterReadWait.Seconds()), detail
		}
		return fmt.Sprintf("%s：%.0f 秒内发不出去（写超时）", src, reporterWriteWait.Seconds()), detail
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return src + "：对端关闭连接（EOF）", detail
	case errors.Is(err, net.ErrClosed):
		return src + "：本地已关闭连接", detail
	}
	return src + "：" + "连接错误", detail
}

func formatSessionDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d 小时 %d 分", int(d.Hours()), int(d.Minutes())%60)
}

// tcpConnOf digs the TCP connection out of a (TLS) WebSocket connection.
func tcpConnOf(conn *websocket.Conn) *net.TCPConn {
	if conn == nil {
		return nil
	}
	c := conn.UnderlyingConn()
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	tcp, _ := c.(*net.TCPConn)
	return tcp
}

// sessionCloseLine builds the close line (without the rate limit).
func (w *WebSocketReporter) sessionCloseLine(out sessionOutcome, cause sessionCause, conn *websocket.Conn, tcpInfo string, lastRecv time.Time, skipped int) string {
	reason, detail := describeSessionCause(cause, w.secret)
	var b strings.Builder
	b.WriteString("🔌 WebSocket连接已关闭：")
	b.WriteString(reason)
	fmt.Fprintf(&b, "；持续 %s", formatSessionDuration(out.duration))
	if conn != nil {
		scheme := strings.ToUpper(out.session.scheme)
		if scheme == "" {
			scheme = "WS"
		}
		fmt.Fprintf(&b, "；%s %s %s", scheme, panelconn.Family(conn.RemoteAddr()), conn.RemoteAddr())
		if la := conn.LocalAddr(); la != nil {
			fmt.Fprintf(&b, "（本地 %s）", la)
		}
	}
	if !lastRecv.IsZero() && !cause.at.IsZero() {
		gap := cause.at.Sub(lastRecv)
		if gap < 0 {
			gap = 0
		}
		fmt.Fprintf(&b, "；断开前 %.1f 秒最后收到面板数据", gap.Seconds())
	}
	if tcpInfo != "" {
		b.WriteString("；")
		b.WriteString(tcpInfo)
	}
	if detail != "" {
		fmt.Fprintf(&b, " [%s]", detail)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "（此前 10 秒内另有 %d 次断开未逐条记录）", skipped)
	}
	return b.String()
}

// logSessionClose writes the close line of the ended session (rate limited).
func (w *WebSocketReporter) logSessionClose(out sessionOutcome, cause sessionCause, conn *websocket.Conn, tcpInfo string, lastRecv time.Time) {
	ok, skipped := sessionCloseLog.allow(time.Now())
	if !ok {
		return
	}
	sessionPrint(w.sessionCloseLine(out, cause, conn, tcpInfo, lastRecv, skipped))
}
