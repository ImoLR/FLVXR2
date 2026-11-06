package relay

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
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
	ap, ok := addrPortOf(remote)
	if !ok || !ap.Addr().IsValid() {
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

// addrPortOf reads the dialed address without formatting and re-parsing it
// (no allocation for the usual *net.TCPAddr / *net.UDPAddr).
func addrPortOf(remote net.Addr) (netip.AddrPort, bool) {
	switch a := remote.(type) {
	case *net.TCPAddr:
		if a == nil {
			return netip.AddrPort{}, false
		}
		return a.AddrPort(), true
	case *net.UDPAddr:
		if a == nil {
			return netip.AddrPort{}, false
		}
		return a.AddrPort(), true
	}
	ap, err := netip.ParseAddrPort(remote.String())
	return ap, err == nil
}

// ownAddrs caches this machine's interface addresses (refreshed every 60 s).
// Readers never lock: the set is an atomic pointer, and a stale set is
// refreshed in the background (one goroutine at a time) while connections
// keep using the previous set. Only the very first check loads it inline.
var ownAddrs = &addrCache{}

const ownAddrsTTL = 60 * time.Second

type addrCache struct {
	set        atomic.Pointer[map[netip.Addr]struct{}]
	at         atomic.Int64 // UnixNano of the last load
	refreshing atomic.Bool
	initMu     sync.Mutex
	load       func() ([]net.Addr, error) // tests
}

func (c *addrCache) has(ip netip.Addr) bool {
	s := c.set.Load()
	if s == nil {
		c.initMu.Lock()
		if s = c.set.Load(); s == nil {
			c.refresh()
			s = c.set.Load()
		}
		c.initMu.Unlock()
	} else if time.Now().UnixNano()-c.at.Load() > int64(ownAddrsTTL) && c.refreshing.CompareAndSwap(false, true) {
		go func() {
			defer c.refreshing.Store(false)
			c.refresh()
		}()
	}
	_, ok := (*s)[ip]
	return ok
}

// refresh reloads the interface addresses. On a load error the previous set
// is kept (an empty set only when there is none yet).
func (c *addrCache) refresh() {
	load := c.load
	if load == nil {
		load = net.InterfaceAddrs
	}
	addrs, err := load()
	if err != nil && c.set.Load() != nil {
		c.at.Store(time.Now().UnixNano())
		return
	}
	set := map[netip.Addr]struct{}{}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			set[p.Addr().Unmap()] = struct{}{}
		}
	}
	c.set.Store(&set)
	c.at.Store(time.Now().UnixNano())
}
