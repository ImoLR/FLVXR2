//go:build !linux

package udp

import "net"

// StickySource is a no-op outside Linux: replies keep the kernel-chosen source.
func StickySource(pc *net.UDPConn) net.PacketConn {
	if pc == nil {
		return nil
	}
	return pc
}
