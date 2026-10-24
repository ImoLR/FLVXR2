//go:build linux

package udp

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// echoListener serves a gost UDP listener the way the forward handler uses it:
// one net.Conn per client, replies written back through that conn.
func echoListener(t *testing.T, pc net.PacketConn) {
	t.Helper()
	ln := NewListener(pc, &ListenConfig{Backlog: 8, ReadQueueSize: 8, ReadBufferSize: 2048, TTL: 5 * time.Second, Keepalive: true})
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 2048)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(append([]byte("echo:"), buf[:n]...)); err != nil {
						return
					}
				}
			}(c)
		}
	}()
}

// roundTrip sends from a connected client socket (like real UDP clients do);
// a reply from any other source address is dropped by the kernel.
func roundTrip(t *testing.T, network string, server *net.UDPAddr) error {
	t.Helper()
	c, err := net.DialUDP(network, nil, server)
	if err != nil {
		t.Fatalf("dial %s: %v", server, err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	if string(buf[:n]) != "echo:ping" {
		return errors.New("unexpected reply " + string(buf[:n]))
	}
	return nil
}

func listenWildcard(t *testing.T, network, addr string) *net.UDPConn {
	t.Helper()
	uc, err := net.ListenUDP(network, &net.UDPAddr{IP: net.ParseIP(addr)})
	if err != nil {
		t.Skipf("listen %s %s: %v", network, addr, err)
	}
	return uc
}

func portOf(pc net.PacketConn) int { return pc.LocalAddr().(*net.UDPAddr).Port }

// 127.0.0.2 is local on Linux but never the kernel's source for replies to
// 127.0.0.1, exactly like node 48's second (CU) IPv6 address.
func TestStickySourceRepliesFromTheAddressTheClientUsed(t *testing.T) {
	for _, tc := range []struct{ name, network, bind string }{
		{"udp4 socket", "udp4", "0.0.0.0"},
		{"dual-stack socket, IPv4 client", "udp", "::"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := listenWildcard(t, tc.network, tc.bind)
			pc := StickySource(uc)
			if _, ok := pc.(*stickyConn); !ok {
				t.Fatalf("StickySource did not wrap a wildcard %s socket", tc.network)
			}
			echoListener(t, pc)
			for _, ip := range []string{"127.0.0.1", "127.0.0.2"} {
				if err := roundTrip(t, "udp4", &net.UDPAddr{IP: net.ParseIP(ip), Port: portOf(uc)}); err != nil {
					t.Fatalf("client -> %s: %v", ip, err)
				}
			}
		})
	}
}

// The bug this fixes: without StickySource the reply to a client that used
// the secondary address leaves from the primary one and never arrives.
func TestPlainWildcardSocketAnswersFromTheWrongAddress(t *testing.T) {
	uc := listenWildcard(t, "udp4", "0.0.0.0")
	echoListener(t, uc)
	if err := roundTrip(t, "udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: portOf(uc)}); err != nil {
		t.Fatalf("primary address must work without the fix: %v", err)
	}
	if err := roundTrip(t, "udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: portOf(uc)}); err == nil {
		t.Fatal("expected the unfixed socket to answer 127.0.0.2 from 127.0.0.1 (client drops it); the reproduction no longer holds")
	}
}

func TestStickySourceIPv6Loopback(t *testing.T) {
	uc := listenWildcard(t, "udp", "::")
	pc := StickySource(uc)
	echoListener(t, pc)
	if err := roundTrip(t, "udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: portOf(uc)}); err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
}

func TestStickySourceLeavesBoundSocketsAlone(t *testing.T) {
	uc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	if pc := StickySource(uc); pc != net.PacketConn(uc) {
		t.Fatalf("a socket bound to one address must be returned unchanged, got %T", pc)
	}
}

// A vanished local address (rotated SLAAC prefix) must not kill the reply.
func TestStickyWriteFallsBackWhenTheLocalAddressIsGone(t *testing.T) {
	srv := listenWildcard(t, "udp4", "0.0.0.0")
	pc := StickySource(srv).(*stickyConn)
	defer pc.Close()
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	gone := &stickyAddr{UDPAddr: peer.LocalAddr().(*net.UDPAddr), local: netip.MustParseAddr("192.0.2.77")}
	if _, err := pc.WriteTo([]byte("x"), gone); err != nil {
		t.Fatalf("write with a non-local source must fall back, got %v", err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := peer.ReadFrom(make([]byte, 8)); err != nil {
		t.Fatalf("fallback datagram not delivered: %v", err)
	}
}

func TestStickyLocalAddrParsesBothFamilies(t *testing.T) {
	v4 := unix.PktInfo4(&unix.Inet4Pktinfo{Ifindex: 3, Spec_dst: [4]byte{10, 0, 0, 1}, Addr: [4]byte{10, 0, 0, 5}})
	mapped := netip.MustParseAddr("::ffff:10.0.0.5").As16()
	v6mapped := unix.PktInfo6(&unix.Inet6Pktinfo{Addr: mapped, Ifindex: 3})
	cu := netip.MustParseAddr("2408:820c:750b:dc64:ca:8aff:fec0:2813")
	v6 := unix.PktInfo6(&unix.Inet6Pktinfo{Addr: cu.As16(), Ifindex: 2})
	ll := unix.PktInfo6(&unix.Inet6Pktinfo{Addr: netip.MustParseAddr("fe80::1").As16(), Ifindex: 7})
	mc := unix.PktInfo6(&unix.Inet6Pktinfo{Addr: netip.MustParseAddr("ff02::1").As16(), Ifindex: 2})
	cases := []struct {
		name   string
		oob    []byte
		peer4  bool
		want   string
		wantIf uint32
	}{
		{"ipv4 peer, IP_PKTINFO", v4, true, "10.0.0.5", 0},
		{"ipv4 peer, both messages", append(append([]byte{}, v6mapped...), v4...), true, "10.0.0.5", 0},
		{"ipv4 peer, only v4-mapped IPV6_PKTINFO", v6mapped, true, "10.0.0.5", 0},
		{"ipv6 peer", v6, false, cu.String(), 2},
		{"ipv6 link-local keeps ifindex", ll, false, "fe80::1", 7},
		{"multicast destination ignored", mc, false, "invalid IP", 0},
		{"ipv6 peer never takes a v4-mapped source", v6mapped, false, "invalid IP", 0},
		{"no control message", nil, false, "invalid IP", 0},
	}
	for _, tc := range cases {
		got, gotIf := stickyLocalAddr(tc.oob, tc.peer4)
		if got.String() != tc.want || gotIf != tc.wantIf {
			t.Errorf("%s: got %s if=%d, want %s if=%d", tc.name, got, gotIf, tc.want, tc.wantIf)
		}
	}
}

// Netns lab (plan 150), driven by netns_sticky.sh: the node namespace serves a
// wildcard UDP echo (with or without StickySource) and the client namespace
// dials the node's primary and secondary addresses. Skipped otherwise.
func TestStickyNetnsServe(t *testing.T) {
	port := os.Getenv("FLVX_STICKY_NETNS_SERVE")
	if port == "" {
		t.Skip("netns lab only")
	}
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6unspecified, Port: atoi(t, port)})
	if err != nil {
		t.Fatal(err)
	}
	var pc net.PacketConn = uc
	if os.Getenv("FLVX_STICKY_NETNS_PLAIN") != "1" {
		pc = StickySource(uc)
	}
	echoListener(t, pc)
	t.Logf("serving %T on %s", pc, uc.LocalAddr())
	time.Sleep(time.Duration(atoi(t, os.Getenv("FLVX_STICKY_NETNS_SECONDS"))) * time.Second)
}

func TestStickySourceNetnsClient(t *testing.T) {
	target := os.Getenv("FLVX_STICKY_NETNS_TARGET")
	if target == "" {
		t.Skip("netns lab only")
	}
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		t.Fatal(err)
	}
	if err := roundTrip(t, "udp", addr); err != nil {
		t.Fatalf("client -> %s: %v", target, err)
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
