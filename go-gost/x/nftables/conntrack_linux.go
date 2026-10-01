//go:build linux

package nftables

import (
	"github.com/vishvananda/netlink"
)

// dnatPortFilter matches conntrack entries of connections that were DNATed from a listening
// port: the client connected to that port and the reply comes from another address or port.
// Connections this host opened itself to a remote port with the same number are not DNATed
// and do not match.
type dnatPortFilter struct {
	proto uint8
	port  uint16
}

func (f dnatPortFilter) MatchConntrackFlow(flow *netlink.ConntrackFlow) bool {
	if flow == nil || flow.Forward.Protocol != f.proto || flow.Forward.DstPort != f.port {
		return false
	}
	return !flow.Reverse.SrcIP.Equal(flow.Forward.DstIP) || flow.Reverse.SrcPort != flow.Forward.DstPort
}

// deleteDNATConntrackEntries removes the IPv4 and IPv6 conntrack entries of connections
// DNATed from port. Their packets then no longer match a NAT binding and the connections end.
func deleteDNATConntrackEntries(proto uint8, port uint16) (uint, error) {
	var total uint
	var firstErr error
	for _, family := range []netlink.InetFamily{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		n, err := netlink.ConntrackDeleteFilters(netlink.ConntrackTable, family, dnatPortFilter{proto: proto, port: port})
		total += n
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return total, firstErr
}
