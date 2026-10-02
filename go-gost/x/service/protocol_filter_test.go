package service

import (
	"io"
	"net"
	"testing"
)

func TestServiceProtocolFiltersAreIsolatedFromEachOtherAndLegacyGlobal(t *testing.T) {
	originalHTTP, originalTLS, originalSOCKS, originalBlockOther := GetProtocolBlockFlags()
	t.Cleanup(func() {
		SetProtocolBlock(originalHTTP, originalTLS, originalSOCKS, originalBlockOther)
	})

	httpService := NewService("http-filter", nil, nil, ProtocolFilterOption(1, 0, 0, 0)).(*defaultService)
	tlsService := NewService("tls-filter", nil, nil, ProtocolFilterOption(0, 1, 0, 0)).(*defaultService)

	// The legacy command remains enabled for services without scoped metadata,
	// but must not override either explicitly configured service.
	SetProtocolBlock(1, 1, 1, 1)

	assertServiceProtocolRead(t, httpService, []byte("GET / HTTP/1.1\r\n\r\n"), true)
	assertServiceProtocolRead(t, httpService, []byte{0x16, 0x03, 0x03, 0x00, 0x01}, false)
	assertServiceProtocolRead(t, tlsService, []byte{0x16, 0x03, 0x03, 0x00, 0x01}, true)
	assertServiceProtocolRead(t, tlsService, []byte("GET / HTTP/1.1\r\n\r\n"), false)
}

func TestServiceProtocolFilterAllZeroDoesNotWrap(t *testing.T) {
	originalHTTP, originalTLS, originalSOCKS, originalBlockOther := GetProtocolBlockFlags()
	t.Cleanup(func() {
		SetProtocolBlock(originalHTTP, originalTLS, originalSOCKS, originalBlockOther)
	})
	SetProtocolBlock(1, 1, 1, 1)

	s := NewService("zero-filter", nil, nil, ProtocolFilterOption(0, 0, 0, 0)).(*defaultService)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	if got := s.wrapProtocolDetection(server); got != server {
		t.Fatal("all-zero service protocol filter should not wrap the connection")
	}
}

func assertServiceProtocolRead(t *testing.T, service *defaultService, payload []byte, wantBlocked bool) {
	t.Helper()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write(payload)
		writeDone <- err
	}()

	conn := service.wrapProtocolDetection(server)
	buf := make([]byte, len(payload))
	n, err := conn.Read(buf)
	blocked := err != nil && n == 0
	if blocked != wantBlocked {
		t.Fatalf("service %s blocked=%v (n=%d err=%v), want %v", service.name, blocked, n, err, wantBlocked)
	}
	if !wantBlocked && (err != nil || n != len(payload)) {
		t.Fatalf("service %s allowed read n=%d err=%v, want n=%d err=nil", service.name, n, err, len(payload))
	}
	if writeErr := <-writeDone; writeErr != nil && writeErr != io.ErrClosedPipe {
		t.Fatalf("write payload: %v", writeErr)
	}
}
