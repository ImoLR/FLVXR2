package socket

import (
	relayhandler "github.com/go-gost/x/handler/relay"
	"github.com/go-gost/x/landing"
)

// Plan 225: the exit relay target guard lets a local target through only when
// a landing (Core) inbound of this machine listens on its port (native landing
// nodes and 127.0.0.1 line-chain copies); the Core API port is never one.
func init() {
	relayhandler.LocalTargetPortAllowed = func(port int) bool {
		return landing.Default().InboundPort(port)
	}
}
