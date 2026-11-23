//go:build linux

package socket

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const tcpEstablished = 1 // TCP_ESTABLISHED

// landingPeerSamples dumps the TCP sockets of both families (sock_diag with
// INET_DIAG_INFO) and keeps the ESTABLISHED ones on the local port.
func landingPeerSamples(port int) ([]landingPeerSample, error) {
	var out []landingPeerSample
	var lastErr error
	ok := 0
	for _, fam := range []uint8{unix.AF_INET, unix.AF_INET6} {
		res, err := netlink.SocketDiagTCPInfo(fam)
		if err != nil {
			lastErr = err
			continue
		}
		ok++
		for _, r := range res {
			if r == nil || r.InetDiagMsg == nil {
				continue
			}
			s := r.InetDiagMsg
			if s.State != tcpEstablished || int(s.ID.SourcePort) != port {
				continue
			}
			sample := landingPeerSample{LocalPort: port, Remote: peerIP(s.ID.Destination)}
			if r.TCPInfo != nil {
				sample.RttUs, sample.MinRttUs = r.TCPInfo.Rtt, r.TCPInfo.Min_rtt
			}
			out = append(out, sample)
		}
	}
	if ok == 0 {
		return nil, fmt.Errorf("读不到连接表：%v", lastErr)
	}
	return out, nil
}

func peerIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}
