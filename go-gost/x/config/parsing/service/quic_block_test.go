package service_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
	"github.com/go-gost/x/config/parsing/service"
	_ "github.com/go-gost/x/dialer/udp"
	_ "github.com/go-gost/x/listener/udp"
	xlogger "github.com/go-gost/x/logger"
	"github.com/quic-go/quic-go"
)

// Plan 283, end to end through a parsed forward _udp service (same config
// shape the panel sends): with protocolFilter.tls = 1 SS-like datagrams are
// forwarded, a QUIC Initial is not, and that client's following datagrams are
// dropped while its entry lives; other clients and a tls = 0 service are not
// affected.

func udpEcho(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().String()
}

func udpForwardService(t *testing.T, name, target string, tlsBlocked int) string {
	t.Helper()
	if logger.Default() == nil {
		logger.SetDefault(xlogger.Nop())
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	cfg := &config.ServiceConfig{
		Name:      name,
		Addr:      addr,
		Handler:   &config.HandlerConfig{Type: "udp"},
		Listener:  &config.ListenerConfig{Type: "udp", Metadata: map[string]any{"keepAlive": true, "ttl": "300ms"}},
		Forwarder: &config.ForwarderConfig{Nodes: []*config.ForwardNodeConfig{{Name: "node_1", Addr: target}}},
		Metadata: map[string]any{
			"protocolFilter.http": 1, "protocolFilter.tls": tlsBlocked,
			"protocolFilter.socks": 1, "protocolFilter.blockOther": 0, "enableStats": false,
		},
	}
	svc, err := service.ParseService(cfg)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	go svc.Serve()
	t.Cleanup(func() { svc.Close() })
	return addr
}

func quicInitial(t *testing.T) []byte {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go quic.DialAddr(ctx, pc.LocalAddr().String(), &tls.Config{ServerName: "hy2.example", NextProtos: []string{"h3"}}, &quic.Config{})
	buf := make([]byte, 65535)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buf[:n]...)
}

// roundTrip sends b and reports whether the echo came back.
func roundTrip(t *testing.T, c net.Conn, b []byte) bool {
	t.Helper()
	if _, err := c.Write(b); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 65535)
	_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	n, err := c.Read(buf)
	return err == nil && bytes.Equal(buf[:n], b)
}

func randomGram(size int) []byte {
	b := make([]byte, size)
	rand.Read(b)
	b[0] &= 0x3f // never long-header-like: keeps the test deterministic
	return b
}

func TestUDPForwardBlocksQUICWhenTLSBlocked(t *testing.T) {
	target := udpEcho(t)
	initial := quicInitial(t)
	addr := udpForwardService(t, "283_1_1_udp", target, 1)

	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if !roundTrip(t, client, randomGram(1350)) || !roundTrip(t, client, randomGram(80)) {
		t.Fatal("SS-like datagrams not forwarded")
	}
	if roundTrip(t, client, initial) {
		t.Fatal("QUIC Initial was forwarded")
	}
	// Same client right after: dropped (blocked entry), no echo.
	if roundTrip(t, client, randomGram(100)) {
		t.Fatal("blocked client's next datagram was forwarded")
	}
	// Another client on the same forward works.
	other, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if !roundTrip(t, other, randomGram(1300)) {
		t.Fatal("other client blocked too")
	}
	// After the entry's idle lifetime (TTL 300 ms → ≤ 600 ms) the first client
	// is judged again.
	time.Sleep(700 * time.Millisecond)
	if !roundTrip(t, client, randomGram(200)) {
		t.Fatal("blocked client never recovered")
	}
}

func TestUDPForwardPassesQUICWhenTLSAllowed(t *testing.T) {
	target := udpEcho(t)
	initial := quicInitial(t)
	addr := udpForwardService(t, "283_1_2_udp", target, 0)
	client, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if !roundTrip(t, client, initial) || !roundTrip(t, client, randomGram(1300)) {
		t.Fatal("tls=0 forward must pass QUIC")
	}
}
