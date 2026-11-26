//go:build linux

package socket

// Plan 310 r3 「端内」 on the 专线 header and in 线路监测: 端内 = 全程 (the
// LeiKwan client's UDP probe, entry → 专线出口) − 入口段 (entry → the ingress
// that UDP selection uses). 入口段 comes from the kernel: the client keeps
// long-lived TCP connections to its ingresses (keepalive every few seconds),
// so the smoothed rtt (tcp_info) of those connections is the entry segment,
// read with ONE sock_diag dump per tick — filtered in the kernel to the
// ingress port(s) and ESTABLISHED sockets. No probe, no new connection, no
// subprocess, no goroutine; runs inside the existing 1-minute host tick.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

// lkSegSock: one ESTABLISHED TCP socket of this host (remote end + kernel rtt).
type lkSegSock struct {
	Dst   netip.AddrPort
	RttUs uint32 // tcpi_rtt (smoothed)
	MinUs uint32 // tcpi_min_rtt (0 = kernel too old)
}

const (
	lkSegMaxSocks     = 4096 // cap per tick (an entry host has ~100)
	lkSegTimeout      = 2 * time.Second
	lkInetDiagReqBC   = 1 // INET_DIAG_REQ_BYTECODE
	lkInetDiagInfo    = 2 // INET_DIAG_INFO
	lkInetDiagBCDGE   = 4 // INET_DIAG_BC_D_GE
	lkInetDiagBCDLE   = 5 // INET_DIAG_BC_D_LE
	lkTCPEstablished  = 1
	lkInetDiagMsgSize = 72
	lkTCPInfoRttOff   = 68  // struct tcp_info.tcpi_rtt
	lkTCPInfoMinOff   = 148 // struct tcp_info.tcpi_min_rtt
)

// ingressFor maps a selection_ref of status.json to the ingress endpoint,
// like 诊断: the current offer first, then the earlier offers (lines.json).
func (m *lkHostManager) ingressFor(id int64, ref string) string {
	if raw, ok := m.readSmall(fmt.Sprintf("run/leikwan-fast-failover/forward-%d.offered.json", id)); ok {
		offers, _ := lkDiagParseOffered(raw)
		for _, o := range offers {
			if o.Ref == ref {
				return o.Endpoint
			}
		}
	}
	if raw, ok := m.readSmall(fmt.Sprintf("run/leikwan-fast-failover/forward-%d.lines.json", id)); ok {
		return lkDiagParseLines(raw)[ref]
	}
	return ""
}

// fillEntrySegs reads the connection table once for every forward's ingress.
func (m *lkHostManager) fillEntrySegs(forwards []LKForward) {
	want := map[netip.AddrPort][]*LKEntrySeg{}
	portSet := map[int]bool{}
	for i := range forwards {
		s := forwards[i].Seg
		if s == nil {
			continue
		}
		ap, err := netip.ParseAddrPort(s.Endpoint)
		if err != nil {
			forwards[i].Seg = nil
			continue
		}
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
		want[ap] = append(want[ap], s)
		portSet[int(ap.Port())] = true
	}
	if len(want) == 0 || m.segDump == nil {
		return
	}
	ports := make([]int, 0, len(portSet))
	for p := range portSet {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	socks, err := m.segDump(ports)
	if err != nil {
		for _, list := range want {
			for _, s := range list {
				s.Err = "读不到连接表"
			}
		}
		return
	}
	for ap, g := range lkSegGroup(socks, want) {
		ms, minMs := lkSegStats(g.rtts)
		for _, s := range want[ap] {
			s.Conns, s.Ms, s.MinMs = g.conns, ms, minMs
		}
	}
}

type lkSegAcc struct {
	conns int
	rtts  []uint32 // µs, > 0
}

// lkSegGroup: the sockets to each wanted endpoint.
func lkSegGroup(socks []lkSegSock, want map[netip.AddrPort][]*LKEntrySeg) map[netip.AddrPort]*lkSegAcc {
	out := map[netip.AddrPort]*lkSegAcc{}
	for _, s := range socks {
		ap := netip.AddrPortFrom(s.Dst.Addr().Unmap(), s.Dst.Port())
		if _, ok := want[ap]; !ok {
			continue
		}
		g := out[ap]
		if g == nil {
			g = &lkSegAcc{}
			out[ap] = g
		}
		g.conns++
		if s.RttUs > 0 {
			g.rtts = append(g.rtts, s.RttUs)
		}
	}
	return out
}

// lkSegStats: the lower median and the lowest srtt in ms (0.01 ms steps).
func lkSegStats(rtts []uint32) (float64, float64) {
	if len(rtts) == 0 {
		return 0, 0
	}
	sorted := append([]uint32(nil), rtts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	ms := func(us uint32) float64 { return float64(us/10) / 100 }
	return ms(sorted[(len(sorted)-1)/2]), ms(sorted[0])
}

// lkSegDump: one netlink socket, per port and family one filtered dump.
func lkSegDump(ports []int) ([]lkSegSock, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_INET_DIAG)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	tv := unix.NsecToTimeval(lkSegTimeout.Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
	kernel := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	buf := make([]byte, 64<<10)
	var out []lkSegSock
	var lastErr error
	ok := 0
	seq := uint32(time.Now().UnixNano())
	for _, port := range ports {
		if port <= 0 || port > 65535 {
			continue
		}
		for _, fam := range []uint8{unix.AF_INET, unix.AF_INET6} {
			seq++
			if err := unix.Sendto(fd, lkSegRequest(seq, fam, uint16(port)), 0, kernel); err != nil {
				lastErr = err
				continue
			}
			got, err := lkSegRecv(fd, buf, seq, lkSegMaxSocks-len(out))
			if err != nil {
				lastErr = err // e.g. no inet6_diag: the other family still counts
				continue
			}
			ok++
			out = append(out, got...)
		}
	}
	if ok == 0 {
		if lastErr == nil {
			lastErr = errors.New("no query")
		}
		return nil, lastErr
	}
	return out, nil
}

// lkSegRecv reads one dump answer up to NLMSG_DONE.
func lkSegRecv(fd int, buf []byte, seq uint32, room int) ([]lkSegSock, error) {
	var out []lkSegSock
	for rounds := 0; rounds < 256; rounds++ {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		got, done, err := lkSegParse(buf[:n], seq)
		if err != nil {
			return nil, err
		}
		if room > 0 {
			if len(got) > room {
				got = got[:room]
			}
			room -= len(got)
			out = append(out, got...)
		}
		if done {
			return out, nil
		}
	}
	return nil, errors.New("sock_diag: answer too long")
}

// lkSegRequest builds SOCK_DIAG_BY_FAMILY (dump) for ESTABLISHED TCP sockets
// of one family with INET_DIAG_INFO, filtered in the kernel by bytecode
// 「dport ≥ port && dport ≤ port」.
func lkSegRequest(seq uint32, family uint8, port uint16) []byte {
	const hdr, req, attr = 16, 56, 4 + 16
	ne := binary.NativeEndian
	b := make([]byte, hdr+req+attr)
	ne.PutUint32(b[0:], uint32(len(b)))
	ne.PutUint16(b[4:], unix.SOCK_DIAG_BY_FAMILY)
	ne.PutUint16(b[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	ne.PutUint32(b[8:], seq)
	r := b[hdr:]
	r[0], r[1], r[2] = family, unix.IPPROTO_TCP, 1<<(lkInetDiagInfo-1)
	ne.PutUint32(r[4:], 1<<lkTCPEstablished)
	a := b[hdr+req:]
	ne.PutUint16(a[0:], attr)
	ne.PutUint16(a[2:], lkInetDiagReqBC)
	bc := a[4:]
	// op 0: D_GE, true → op 2 (+8), false → past the end (+20 = reject).
	bc[0], bc[1] = lkInetDiagBCDGE, 8
	ne.PutUint16(bc[2:], 20)
	ne.PutUint16(bc[6:], port)
	// op 2: D_LE, true → end (accept), false → past the end (+12 = reject).
	bc[8], bc[9] = lkInetDiagBCDLE, 8
	ne.PutUint16(bc[10:], 12)
	ne.PutUint16(bc[14:], port)
	return b
}

// lkSegParse reads the netlink messages of one recv; done at NLMSG_DONE.
func lkSegParse(buf []byte, seq uint32) ([]lkSegSock, bool, error) {
	ne := binary.NativeEndian
	var out []lkSegSock
	for len(buf) >= 16 {
		l := int(ne.Uint32(buf[0:]))
		typ := ne.Uint16(buf[4:])
		s := ne.Uint32(buf[8:])
		if l < 16 || l > len(buf) {
			return out, false, errors.New("sock_diag: bad message")
		}
		msg := buf[16:l]
		if s == seq {
			switch typ {
			case unix.NLMSG_DONE:
				return out, true, nil
			case unix.NLMSG_ERROR:
				if len(msg) >= 4 {
					if e := int32(ne.Uint32(msg)); e != 0 {
						return out, false, unix.Errno(-e)
					}
				}
				return out, true, nil
			case unix.SOCK_DIAG_BY_FAMILY:
				if sk, ok := lkSegParseMsg(msg); ok {
					out = append(out, sk)
				}
			}
		}
		adv := (l + 3) &^ 3
		if adv >= len(buf) {
			break
		}
		buf = buf[adv:]
	}
	return out, false, nil
}

// lkSegParseMsg: inet_diag_msg + its INET_DIAG_INFO (struct tcp_info).
func lkSegParseMsg(msg []byte) (lkSegSock, bool) {
	if len(msg) < lkInetDiagMsgSize || msg[1] != lkTCPEstablished {
		return lkSegSock{}, false
	}
	port := binary.BigEndian.Uint16(msg[6:8])
	var addr netip.Addr
	switch msg[0] {
	case unix.AF_INET:
		addr = netip.AddrFrom4([4]byte(msg[24:28]))
	case unix.AF_INET6:
		addr = netip.AddrFrom16([16]byte(msg[24:40])).Unmap()
	default:
		return lkSegSock{}, false
	}
	sk := lkSegSock{Dst: netip.AddrPortFrom(addr, port)}
	ne := binary.NativeEndian
	for off := lkInetDiagMsgSize; off+4 <= len(msg); {
		alen := int(ne.Uint16(msg[off:]))
		atype := ne.Uint16(msg[off+2:]) & 0x3fff // NLA_TYPE_MASK
		if alen < 4 || off+alen > len(msg) {
			break
		}
		if atype == lkInetDiagInfo {
			info := msg[off+4 : off+alen]
			if len(info) >= lkTCPInfoRttOff+4 {
				sk.RttUs = ne.Uint32(info[lkTCPInfoRttOff:])
			}
			if len(info) >= lkTCPInfoMinOff+4 {
				sk.MinUs = ne.Uint32(info[lkTCPInfoMinOff:])
			}
		}
		off += (alen + 3) &^ 3
	}
	return sk, true
}
