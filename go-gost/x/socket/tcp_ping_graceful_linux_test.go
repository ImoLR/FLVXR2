package socket

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Plan 259: graceful TcpPing close for line-monitor custom targets.

// peerName runs getpeername(2) on an accepted connection, the call systemd
// makes for Accept=yes sockets with MaxConnectionsPerSource.
func peerName(t *testing.T, c *net.TCPConn) error {
	t.Helper()
	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var perr error
	if err := rc.Control(func(fd uintptr) { _, perr = syscall.Getpeername(int(fd)) }); err != nil {
		t.Fatal(err)
	}
	return perr
}

func listenLoopback(t *testing.T) (*net.TCPListener, int) {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, l.Addr().(*net.TCPAddr).Port
}

// A systemd-like peer: the connection waits in the accept queue (the probe
// has long returned from connect), then is accepted, getpeername'd and
// closed. RST mode (old behaviour) breaks getpeername; graceful keeps it.
func TestTCPPingGracefulSurvivesLateAccept(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		l, port := listenLoopback(t)
		type res struct {
			peerErr error
			readErr error
		}
		got := make(chan res, 1)
		go func() {
			time.Sleep(300 * time.Millisecond) // the probe's connect finished long ago
			c, err := l.AcceptTCP()
			if err != nil {
				got <- res{peerErr: err}
				return
			}
			r := res{peerErr: peerName(t, c)}
			c.Close() // like /bin/true exiting: the peer closes first
			got <- r
		}()
		start := time.Now()
		ms, loss, kind, err := tcpPingHostClose("127.0.0.1", port, 1, 1000, graceful)
		took := time.Since(start)
		if err != nil || loss != 0 || kind != "" || ms < 0 {
			t.Fatalf("graceful=%v: ping ms=%f loss=%f kind=%q err=%v", graceful, ms, loss, kind, err)
		}
		r := <-got
		switch {
		case graceful && r.peerErr != nil:
			t.Fatalf("graceful close: getpeername after a late accept failed: %v", r.peerErr)
		case !graceful && !errors.Is(r.peerErr, syscall.ENOTCONN):
			t.Fatalf("RST close: expected ENOTCONN on a late accept (the bug this plan works around), got %v", r.peerErr)
		}
		if graceful && (took < 250*time.Millisecond || took > gracefulCloseWait+500*time.Millisecond) {
			t.Fatalf("graceful close should wait for the peer's close (~300 ms), took %v", took)
		}
		if !graceful && took > 200*time.Millisecond {
			t.Fatalf("RST close must not wait, took %v", took)
		}
	}
}

// A peer that never closes (an ordinary service waiting for a request): the
// graceful close is bounded and ends with a FIN, never a RST.
func TestTCPPingGracefulBoundedAgainstSilentPeer(t *testing.T) {
	l, port := listenLoopback(t)
	readErr := make(chan error, 1)
	go func() {
		c, err := l.AcceptTCP()
		if err != nil {
			readErr <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var b [16]byte
		_, err = c.Read(b[:])
		readErr <- err
	}()
	start := time.Now()
	if _, loss, _, err := tcpPingHostClose("127.0.0.1", port, 1, 1000, true); err != nil || loss != 0 {
		t.Fatalf("ping loss=%f err=%v", loss, err)
	}
	took := time.Since(start)
	if took < gracefulCloseWait-100*time.Millisecond || took > gracefulCloseWait+700*time.Millisecond {
		t.Fatalf("silent peer: close should end after ~%v, took %v", gracefulCloseWait, took)
	}
	select {
	case err := <-readErr:
		if err != io.EOF {
			t.Fatalf("silent peer should see a FIN (EOF), got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peer never saw the close")
	}
}

// A peer that sends a banner and then closes: drained, returns at once.
func TestTCPPingGracefulDrainsBanner(t *testing.T) {
	l, port := listenLoopback(t)
	go func() {
		c, err := l.AcceptTCP()
		if err != nil {
			return
		}
		_, _ = c.Write([]byte("220 hello\r\n"))
		c.Close()
	}()
	start := time.Now()
	if _, loss, _, err := tcpPingHostClose("127.0.0.1", port, 1, 1000, true); err != nil || loss != 0 {
		t.Fatalf("ping loss=%f err=%v", loss, err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("banner + close should finish fast, took %v", took)
	}
}

// A peer that keeps streaming: the drain cap ends the wait early.
func TestTCPPingGracefulDrainCap(t *testing.T) {
	l, port := listenLoopback(t)
	go func() {
		c, err := l.AcceptTCP()
		if err != nil {
			return
		}
		defer c.Close()
		chunk := make([]byte, 1024)
		_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		for {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}()
	start := time.Now()
	if _, loss, _, err := tcpPingHostClose("127.0.0.1", port, 1, 1000, true); err != nil || loss != 0 {
		t.Fatalf("ping loss=%f err=%v", loss, err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("drain cap should end the wait early, took %v", took)
	}
}

// After the peer closed first, the graceful close resets: the peer's side
// reads ECONNRESET, not EOF (no TIME_WAIT left on either machine).
func TestTCPPingGracefulResetsAfterPeerClose(t *testing.T) {
	l, port := listenLoopback(t)
	readErr := make(chan error, 1)
	go func() {
		c, err := l.AcceptTCP()
		if err != nil {
			readErr <- err
			return
		}
		defer c.Close()
		_ = c.CloseWrite() // FIN first, keep reading to see how the prober ends
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var b [16]byte
		_, err = c.Read(b[:])
		readErr <- err
	}()
	if _, loss, _, err := tcpPingHostClose("127.0.0.1", port, 1, 1000, true); err != nil || loss != 0 {
		t.Fatalf("ping loss=%f err=%v", loss, err)
	}
	select {
	case err := <-readErr:
		if !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("peer should see the reset after its own close, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out")
	}
}

// The request field reaches the close path; absent = RST as before.
func TestTCPPingRequestGracefulField(t *testing.T) {
	var req TcpPingRequest
	if err := json.Unmarshal([]byte(`{"ip":"127.0.0.1","port":1,"count":1,"timeout":1000,"graceful":true}`), &req); err != nil || !req.Graceful {
		t.Fatalf("graceful=true not decoded: %+v %v", req, err)
	}
	req = TcpPingRequest{}
	if err := json.Unmarshal([]byte(`{"ip":"127.0.0.1","port":1}`), &req); err != nil || req.Graceful {
		t.Fatalf("default must be RST close: %+v %v", req, err)
	}
	for _, graceful := range []bool{false, true} {
		l, port := listenLoopback(t)
		got := make(chan error, 1)
		go func() {
			time.Sleep(200 * time.Millisecond)
			c, err := l.AcceptTCP()
			if err != nil {
				got <- err
				return
			}
			got <- peerName(t, c)
			c.Close()
		}()
		res, err := (&WebSocketReporter{}).handleTcpPing(map[string]interface{}{
			"ip": "127.0.0.1", "port": port, "count": 1, "timeout": 1000, "graceful": graceful})
		if err != nil || !res.Success {
			t.Fatalf("graceful=%v: %+v %v", graceful, res, err)
		}
		perr := <-got
		if graceful != (perr == nil) {
			t.Fatalf("graceful=%v via handleTcpPing: getpeername err %v (port %s)", graceful, perr, strconv.Itoa(port))
		}
	}
}
