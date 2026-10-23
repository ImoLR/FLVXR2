//go:build linux

package socket

import (
	"fmt"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

// tcpInfoSummary reads the kernel's view of the panel TCP connection just
// before it is closed: smoothed RTT, retransmissions, lost segments and the
// time since the last received segment. One getsockopt per ended session.
func tcpInfoSummary(conn *websocket.Conn) string {
	tcp := tcpConnOf(conn)
	if tcp == nil {
		return ""
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return ""
	}
	var info *unix.TCPInfo
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		info, gerr = unix.GetsockoptTCPInfo(int(fd), unix.SOL_TCP, unix.TCP_INFO)
	}); err != nil || gerr != nil || info == nil {
		return ""
	}
	return fmt.Sprintf("TCP rtt=%.0fms 累计重传=%d 丢失=%d 最后收包 %.1f 秒前",
		float64(info.Rtt)/1000, info.Total_retrans, info.Lost, float64(info.Last_data_recv)/1000)
}
