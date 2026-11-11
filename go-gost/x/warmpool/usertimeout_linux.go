//go:build linux

package warmpool

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// setUserTimeout sets TCP_USER_TIMEOUT on the socket under c (0 = kernel
// default). It reports whether the option was set.
func setUserTimeout(c net.Conn, d time.Duration) bool {
	rc := rawConn(c)
	if rc == nil {
		return false
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d/time.Millisecond))
	}); err != nil {
		return false
	}
	return serr == nil
}
