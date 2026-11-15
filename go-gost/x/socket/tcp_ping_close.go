package socket

import (
	"errors"
	"io"
	"net"
	"time"
)

// Plan 259: how a connected TcpPing attempt is closed.
const (
	// gracefulCloseWait bounds how long a graceful close waits for the peer
	// to close first (systemd Accept=yes: accept + spawn /bin/true + close is
	// one round trip plus a few ms).
	gracefulCloseWait = time.Second
	// gracefulCloseDrain bounds the bytes read and dropped while waiting (a
	// banner from an ordinary service).
	gracefulCloseDrain = 4096
)

// closeProbeConn ends one connected TcpPing attempt. The measured latency is
// the connect time, so the close never changes a result.
//
// Default: SO_LINGER 0 → RST and no TIME_WAIT on this machine (plan 082, the
// path-latency prober pings many hop pairs every minute).
//
// graceful (line-monitor custom targets): the 「复制开启命令」 check port is a
// systemd Accept=yes socket. A RST that arrives while the connection still
// waits in its accept queue makes systemd's getpeername() fail: the
// connection is dropped, logged as an error and never counted in NAccepted,
// so the port's idle watchdog removes it. Instead wait, bounded by
// gracefulCloseWait / gracefulCloseDrain, for the peer to close first:
//   - peer closed (EOF): it has accepted and finished → reset now; no
//     TIME_WAIT on either side (same footprint as the agent's own check
//     ports, which close with FIN and see the prober's RST).
//   - otherwise (an ordinary service that waits for a request, or a slow
//     accept): close with a normal FIN, never a RST before the peer had the
//     chance to accept. Costs at most one TIME_WAIT here for such targets.
//
// No goroutine; at most one blocking read loop on the caller's goroutine.
func closeProbeConn(conn net.Conn, graceful bool) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return
	}
	if !graceful || peerClosedWithin(tc, gracefulCloseWait, gracefulCloseDrain) {
		_ = tc.SetLinger(0)
	}
	_ = tc.Close()
}

// peerClosedWithin reads and drops what the peer sends until it closes the
// connection (EOF → true), the wait ends, max bytes were read or the read
// fails otherwise (→ false).
func peerClosedWithin(c *net.TCPConn, wait time.Duration, max int) bool {
	if err := c.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return false
	}
	var buf [512]byte
	for read := 0; read < max; {
		n, err := c.Read(buf[:])
		read += n
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
	return false
}
