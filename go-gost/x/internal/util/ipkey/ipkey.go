// Package ipkey maps a client address to the unit that one client-IP slot
// counts (plan 231): IPv4 = the address, IPv6 = its /64, the same as the
// 妙妙屋 Core (normalizedSourceIP) and the mmwx Helper nft limiter
// (ip6 saddr & ffff:ffff:ffff:ffff::). A phone rotating IPv6 privacy
// addresses inside one /64 is one IP, not one per address.
package ipkey

import (
	"net"
	"net/netip"
	"strings"
)

// PrefixBits is the IPv6 prefix length one client IP stands for.
const PrefixBits = 64

// Of returns the key of a client address: IPv4 (and IPv4-mapped IPv6) → the
// dotted address, IPv6 → "<network>/64" (e.g. "2409:8a4c:5c4f:26e1::/64"),
// an IPv6 prefix (what the Core reports) → its /64, IPv4 "/32" → the address.
// Brackets and zones are dropped; anything else is returned trimmed, so
// unknown strings still count as one distinct IP each (never zero).
func Of(raw string) string {
	s := strings.TrimSpace(raw)
	if addr, err := netip.ParseAddr(s); err == nil {
		if addr.Is4() {
			return s // already canonical: netip only accepts plain dotted quads
		}
		return ofAddr(addr)
	}
	t := strings.Trim(s, "[]")
	if addr, err := netip.ParseAddr(t); err == nil {
		return ofAddr(addr)
	}
	if p, err := netip.ParsePrefix(t); err == nil {
		addr := p.Addr()
		if addr.Is4In6() && p.Bits() >= 96 {
			return addr.Unmap().String()
		}
		if addr.Is4() {
			if p.Bits() == 32 {
				return addr.String()
			}
			return p.Masked().String()
		}
		if p.Bits() >= PrefixBits {
			return netip.PrefixFrom(addr, PrefixBits).Masked().String()
		}
		return p.Masked().String()
	}
	return s
}

func ofAddr(addr netip.Addr) string {
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, PrefixBits).Masked().String()
}

// OfIP is Of for a net.IP (conntrack flows); nil → "".
func OfIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return ip.String()
	}
	return ofAddr(addr)
}

// IsPrefix reports whether key is an IPv6 /64 key (not a single address).
func IsPrefix(key string) bool { return strings.IndexByte(key, '/') >= 0 }

// Bytes returns the nft set element of a key: 4 bytes for IPv4, the 16-byte
// masked network for an IPv6 key (or an IPv6 address); nil when unparsable.
func Bytes(key string) []byte {
	if p, err := netip.ParsePrefix(key); err == nil {
		b := p.Masked().Addr().As16()
		if p.Addr().Is4() {
			v4 := p.Addr().As4()
			return v4[:]
		}
		return b[:]
	}
	addr, err := netip.ParseAddr(key)
	if err != nil {
		return nil
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		v4 := addr.As4()
		return v4[:]
	}
	b := netip.PrefixFrom(addr, PrefixBits).Masked().Addr().As16()
	return b[:]
}

// Family returns "4" or "6" for a key or address, "" when unparsable.
func Family(key string) string {
	if p, err := netip.ParsePrefix(key); err == nil {
		if p.Addr().Is4() {
			return "4"
		}
		return "6"
	}
	addr, err := netip.ParseAddr(strings.Trim(key, "[]"))
	if err != nil {
		return ""
	}
	if addr.Unmap().Is4() {
		return "4"
	}
	return "6"
}

// Mask6 is the nft bitwise mask applied to an IPv6 source before the set
// lookup (ffff:ffff:ffff:ffff::).
var Mask6 = func() []byte {
	m := make([]byte, 16)
	for i := 0; i < PrefixBits/8; i++ {
		m[i] = 0xff
	}
	return m
}()
