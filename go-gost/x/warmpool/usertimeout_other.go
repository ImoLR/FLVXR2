//go:build !linux

package warmpool

import (
	"net"
	"time"
)

func setUserTimeout(c net.Conn, d time.Duration) bool { return false }
