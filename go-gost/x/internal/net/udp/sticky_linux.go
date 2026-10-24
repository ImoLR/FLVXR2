//go:build linux

package udp

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// StickySource makes a UDP socket that is bound to a wildcard address answer
// every peer from the local address that peer sent its datagram to.
//
// Without it the kernel chooses the reply source by routing alone. On a host
// with several addresses of one family (node 48: China Telecom + China Unicom
// IPv6 on one interface) a client that sent to the second address gets the
// reply from the first one and drops it, so UDP through that entry is dead
// while TCP (whose accepted socket keeps the right address) works.
//
// The returned PacketConn is pc itself when the socket is bound to a specific
// address (the source is fixed anyway) or when the destination-address socket
// options cannot be enabled, so behaviour is never worse than before.
func StickySource(pc *net.UDPConn) net.PacketConn {
	if pc == nil {
		return nil
	}
	la, _ := pc.LocalAddr().(*net.UDPAddr)
	if la == nil || (la.IP != nil && !la.IP.IsUnspecified()) {
		return pc
	}
	raw, err := pc.SyscallConn()
	if err != nil {
		return pc
	}
	ok := false
	ctrlErr := raw.Control(func(fd uintptr) {
		domain, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_DOMAIN)
		if err != nil {
			return
		}
		switch domain {
		case unix.AF_INET6:
			if unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1) != nil {
				return
			}
			// IPv4 datagrams on a dual-stack socket: older kernels only report
			// their destination through IP_PKTINFO. Best effort.
			_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_PKTINFO, 1)
			ok = true
		case unix.AF_INET:
			ok = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_PKTINFO, 1) == nil
		}
	})
	if ctrlErr != nil || !ok {
		return pc
	}
	return &stickyConn{UDPConn: pc}
}

const stickyOOBSize = 128 // room for IPV6_PKTINFO + IP_PKTINFO control messages

var stickyOOBPool = sync.Pool{New: func() any { b := make([]byte, stickyOOBSize); return &b }}

type stickyConn struct {
	*net.UDPConn
}

// stickyAddr is the peer address of one received datagram plus the local
// address it was sent to. String/Network are the embedded UDPAddr's, so every
// layer above (session keys, logs, admission, client IP lists) sees exactly
// what a plain ReadFrom would have returned.
type stickyAddr struct {
	*net.UDPAddr
	local   netip.Addr
	ifIndex uint32
	oob     atomic.Pointer[[]byte]
}

func (c *stickyConn) ReadFrom(b []byte) (int, net.Addr, error) {
	oobp := stickyOOBPool.Get().(*[]byte)
	n, oobn, _, raddr, err := c.UDPConn.ReadMsgUDP(b, *oobp)
	var local netip.Addr
	var ifIndex uint32
	if err == nil && oobn > 0 && raddr != nil {
		local, ifIndex = stickyLocalAddr((*oobp)[:oobn], raddr.IP.To4() != nil)
	}
	stickyOOBPool.Put(oobp)
	if raddr == nil {
		return n, nil, err
	}
	if !local.IsValid() {
		return n, raddr, err
	}
	return n, &stickyAddr{UDPAddr: raddr, local: local, ifIndex: ifIndex}, err
}

func (c *stickyConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	sa, ok := addr.(*stickyAddr)
	if !ok {
		return c.UDPConn.WriteTo(b, addr)
	}
	n, _, err := c.UDPConn.WriteMsgUDP(b, sa.controlMessage(), sa.UDPAddr)
	if err != nil && (errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EADDRNOTAVAIL) || errors.Is(err, unix.ENETUNREACH)) {
		// The address the peer used is gone (e.g. a rotated SLAAC prefix:
		// EINVAL for IPv6, ENETUNREACH for IPv4) or cannot be a source:
		// answer the way we did before this fix.
		return c.UDPConn.WriteTo(b, sa.UDPAddr)
	}
	return n, err
}

// controlMessage is the IP_PKTINFO / IPV6_PKTINFO message that pins the reply
// source; built once per peer session and shared by its writers.
func (a *stickyAddr) controlMessage() []byte {
	if p := a.oob.Load(); p != nil {
		return *p
	}
	var oob []byte
	if a.local.Is4() {
		// IPv4 peer, also on a dual-stack socket: the kernel hands v4-mapped
		// destinations to udp_sendmsg, which honours IP_PKTINFO on every version.
		oob = unix.PktInfo4(&unix.Inet4Pktinfo{Spec_dst: a.local.As4()})
	} else {
		info := &unix.Inet6Pktinfo{Addr: a.local.As16()}
		if a.local.IsLinkLocalUnicast() {
			info.Ifindex = a.ifIndex
		}
		oob = unix.PktInfo6(info)
	}
	a.oob.Store(&oob)
	return oob
}

// stickyLocalAddr extracts the destination address of a received datagram from
// its control messages. peerIs4 selects the IPv4 view for IPv4 peers on a
// dual-stack socket. Non-unicast destinations are ignored (zero Addr).
func stickyLocalAddr(oob []byte, peerIs4 bool) (netip.Addr, uint32) {
	var v4, v6 netip.Addr
	var v6If uint32
	for len(oob) > 0 {
		hdr, data, rest, err := unix.ParseOneSocketControlMessage(oob)
		if err != nil {
			break
		}
		oob = rest
		switch {
		case hdr.Level == unix.IPPROTO_IP && hdr.Type == unix.IP_PKTINFO && len(data) >= unix.SizeofInet4Pktinfo:
			// struct in_pktinfo { int ifindex; in_addr spec_dst; in_addr addr; }
			v4 = netip.AddrFrom4([4]byte(data[8:12]))
		case hdr.Level == unix.IPPROTO_IPV6 && hdr.Type == unix.IPV6_PKTINFO && len(data) >= unix.SizeofInet6Pktinfo:
			// struct in6_pktinfo { in6_addr addr; unsigned int ifindex; }
			v6 = netip.AddrFrom16([16]byte(data[:16]))
			v6If = binary.NativeEndian.Uint32(data[16:20])
		}
	}
	local, ifIndex := v6, v6If
	if peerIs4 {
		local, ifIndex = v4, 0
		if !local.IsValid() && v6.Is4In6() {
			local = v6.Unmap()
		}
	} else if v6.Is4In6() {
		return netip.Addr{}, 0
	}
	if !local.IsValid() || local.IsUnspecified() || local.IsMulticast() ||
		(local.Is4() && local == netip.AddrFrom4([4]byte{255, 255, 255, 255})) {
		return netip.Addr{}, 0
	}
	return local, ifIndex
}
