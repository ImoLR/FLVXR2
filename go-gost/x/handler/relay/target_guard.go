package relay

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

// FLVX exit target guard (plan 225).
//
// A tunnel exit's relay ({tid}_tls) dials whatever target the entry asks for.
// With metadata "flvx.targetGuard" the relay refuses targets on this machine
// or its private networks: loopback, unspecified, private (RFC 1918 / ULA),
// link-local (incl. 169.254.169.254), CGNAT, multicast and this machine's own
// interface addresses. The one exception is a port that a landing (Core)
// inbound of this machine listens on (native landing nodes and the
// 127.0.0.1-only line-chain copies), reported by LocalTargetPortAllowed.
// The check runs on the address actually dialed (after DNS), so a domain that
// resolves to 127.0.0.1 is refused too. `unix` / `serial` targets are refused.

const targetGuardKey = "flvx.targetGuard"

var errTargetGuard = errors.New("relay: target refused by exit target guard")

// LocalTargetPortAllowed reports whether a local target port belongs to a
// landing inbound of this machine. Set by the agent (socket package); nil =
// no local target allowed.
var LocalTargetPortAllowed func(port int) bool

// guardedNetwork: unix/serial targets are never allowed through a guarded relay.
func guardedNetwork(network string) bool {
	return network == "unix" || network == "serial"
}

// checkTargetGuard returns errTargetGuard when the dialed remote address is
// refused.
func checkTargetGuard(remote net.Addr) error {
	if remote == nil {
		return errTargetGuard
	}
	ap, err := netip.ParseAddrPort(remote.String())
	if err != nil {
		return errTargetGuard
	}
	ip := ap.Addr().Unmap()
	local := ip.IsLoopback() || ip.IsUnspecified() || ownAddrs.has(ip)
	if !local && !privateAddr(ip) {
		return nil
	}
	if local && LocalTargetPortAllowed != nil && LocalTargetPortAllowed(int(ap.Port())) {
		return nil
	}
	return errTargetGuard
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func privateAddr(ip netip.Addr) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() || (ip.Is4() && cgnat.Contains(ip))
}

// ownAddrs caches this machine's interface addresses (refreshed every 60 s).
var ownAddrs = &addrCache{}

type addrCache struct {
	mu   sync.Mutex
	at   time.Time
	set  map[netip.Addr]struct{}
	load func() ([]net.Addr, error) // tests
}

func (c *addrCache) has(ip netip.Addr) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set == nil || time.Since(c.at) > 60*time.Second {
		load := c.load
		if load == nil {
			load = net.InterfaceAddrs
		}
		set := map[netip.Addr]struct{}{}
		if addrs, err := load(); err == nil {
			for _, a := range addrs {
				if p, err := netip.ParsePrefix(a.String()); err == nil {
					set[p.Addr().Unmap()] = struct{}{}
				}
			}
		}
		c.set, c.at = set, time.Now()
	}
	_, ok := c.set[ip]
	return ok
}
