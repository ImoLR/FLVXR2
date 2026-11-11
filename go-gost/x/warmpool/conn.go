package warmpool

import (
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

const connectUserTimeout = 3 * time.Second

const (
	stateIdle = iota
	stateTaken
	stateDead
	stateClosed
)

// pconn is one pooled conn. While idle a watcher goroutine sits in Read (no
// CPU, no traffic): a peer close or reset ends it at once.
type pconn struct {
	net.Conn
	p      *pool
	born   time.Time
	expire time.Time
	state  int // guarded by Manager.mu

	done chan struct{}
	b    [1]byte
	n    int
	err  error
}

func (c *pconn) watch(m *Manager) {
	c.n, c.err = c.Conn.Read(c.b[:])
	close(c.done)
	m.onWatchEnd(c)
}

// stopWatch ends the idle watcher and reports whether the conn is still
// usable; a byte the peer sent first is handed back in front of the stream.
// A read deadline in the past ends the Read with a timeout, which leaves
// both plain TCP and crypto/tls conns usable (a timeout is not sticky).
func (c *pconn) stopWatch() (net.Conn, bool) {
	_ = c.Conn.SetReadDeadline(time.Unix(1, 0))
	<-c.done
	_ = c.Conn.SetReadDeadline(time.Time{})
	if c.n > 0 {
		return &prefixConn{Conn: c.Conn, b: append([]byte(nil), c.b[:c.n]...)}, true
	}
	if isTimeout(c.err) {
		return c.Conn, true
	}
	return nil, false
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// IsDeadConnErr reports whether a per-stream request on a pooled conn failed
// because the conn itself was dead (closed / reset / unacknowledged), as
// opposed to an answer from the peer (e.g. a relay status error) that a cold
// conn would have got too. Only then is the stream redialed cold.
func IsDeadConnErr(err error) bool {
	if err == nil {
		return false
	}
	for _, target := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed,
		syscall.ECONNRESET, syscall.EPIPE, syscall.ETIMEDOUT, syscall.ECONNABORTED} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// prefixConn returns bytes read by the idle watcher before the stream's own.
type prefixConn struct {
	net.Conn
	b []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.b) > 0 {
		n := copy(p, c.b)
		c.b = c.b[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *prefixConn) NetConn() net.Conn { return c.Conn }

// closeQuiet closes a pooled conn we no longer need; nothing is logged.
func closeQuiet(c net.Conn) {
	if c != nil {
		_ = c.Close()
	}
}

// rawConn finds the socket under TLS / wrappers.
func rawConn(c net.Conn) syscall.RawConn {
	for i := 0; i < 8 && c != nil; i++ {
		if sc, ok := c.(syscall.Conn); ok {
			if rc, err := sc.SyscallConn(); err == nil {
				return rc
			}
			return nil
		}
		nc, ok := c.(interface{ NetConn() net.Conn })
		if !ok {
			return nil
		}
		c = nc.NetConn()
	}
	return nil
}
