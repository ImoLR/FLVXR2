package udp

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/go-gost/core/logger"
)

type nopLogger struct{ logger.Logger }

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Warn(...any)           {}
func (nopLogger) Warnf(string, ...any)  {}

// acceptor runs one Accept loop for the whole test.
func acceptor(ln net.Listener) <-chan net.Conn {
	ch := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(ch)
				return
			}
			ch <- c
		}
	}()
	return ch
}

func acceptWithin(ch <-chan net.Conn, d time.Duration) (net.Conn, bool) {
	select {
	case c, ok := <-ch:
		return c, ok
	case <-time.After(d):
		return nil, false
	}
}

// Plan 283: a blocked client's datagrams are dropped by the listener (no new
// session, no new dial) until the idle check removes the entry; other
// clients are untouched; afterwards the client is judged again.
func TestBlockedSessionDropsUntilIdle(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ttl := 200 * time.Millisecond
	ln := NewListener(pc, &ListenConfig{Backlog: 8, ReadQueueSize: 8, ReadBufferSize: 2048, TTL: ttl, Keepalive: true, Logger: nopLogger{}})
	defer ln.Close()
	accepted := acceptor(ln)

	client, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	other, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	client.Write([]byte("first"))
	c1, ok := acceptWithin(accepted, time.Second)
	if !ok {
		t.Fatal("no session for the first datagram")
	}
	buf := make([]byte, 2048)
	if n, err := c1.Read(buf); err != nil || string(buf[:n]) != "first" {
		t.Fatalf("read %q %v", buf[:n], err)
	}
	c1.(*conn).Block()
	if _, err := c1.Read(buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("blocked session read err=%v, want ErrClosed", err)
	}

	// Keep sending well inside one TTL: all dropped, no new session.
	for i := 0; i < 5; i++ {
		client.Write([]byte("retransmit"))
		time.Sleep(10 * time.Millisecond)
	}
	if c, ok := acceptWithin(accepted, ttl/4); ok {
		t.Fatalf("blocked client got a new session %v", c.RemoteAddr())
	}

	// Another client is not affected.
	other.Write([]byte("other"))
	c2, ok := acceptWithin(accepted, time.Second)
	if !ok || c2.RemoteAddr().String() != other.LocalAddr().String() {
		t.Fatalf("other client: ok=%v", ok)
	}

	// After 1–2 TTLs the entry is gone even though the client kept sending
	// (drops never refresh it) and the next datagram opens a new session.
	deadline := time.Now().Add(5 * ttl)
	var c3 net.Conn
	for time.Now().Before(deadline) && c3 == nil {
		client.Write([]byte("again"))
		c3, _ = acceptWithin(accepted, ttl/4)
	}
	if c3 == nil {
		t.Fatal("blocked entry never expired")
	}
	if c3.RemoteAddr().String() != client.LocalAddr().String() {
		t.Fatalf("new session from %v", c3.RemoteAddr())
	}
	if n, err := c3.Read(buf); err != nil || string(buf[:n]) != "again" {
		t.Fatalf("new session read %q %v", buf[:n], err)
	}
}
