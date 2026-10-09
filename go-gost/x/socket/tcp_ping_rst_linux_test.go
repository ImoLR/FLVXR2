package socket

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestTCPPingClosesWithReset(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	errorsSeen := make(chan error, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := listener.AcceptTCP()
			if err != nil {
				errorsSeen <- err
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var buf [1]byte
			_, err = conn.Read(buf[:])
			conn.Close()
			errorsSeen <- err
		}
	}()
	ms, loss, err := tcpPingHost("127.0.0.1", listener.Addr().(*net.TCPAddr).Port, 2, 1000)
	if err != nil || ms < 0 || loss != 0 {
		t.Fatalf("ping=%f loss=%f err=%v", ms, loss, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errorsSeen:
			if !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("probe %d closed without reset: %v", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("probe close timed out")
		}
	}
}
