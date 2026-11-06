// Package ipkey maps a client address to the unit one client-IP slot counts
// (plan 231): IPv4 = the address, IPv6 = its /64 — the same as the 妙妙屋
// Core (normalizedSourceIP), the mmwx Helper nft limiter and the FLVX agent
// (go-gost/x/internal/util/ipkey, identical rules). 用户总 IP 数 / 规则 IP 数
// count distinct keys, so a phone rotating IPv6 privacy addresses inside one
// /64 is one IP.
package ipkey

import (
	"net/netip"
	"strings"
)

// PrefixBits is the IPv6 prefix length one client IP stands for.
const PrefixBits = 64

// Of returns the key of a reported client address: IPv4 (and IPv4-mapped
// IPv6) → the dotted address, IPv6 → "<network>/64" (e.g.
// "2409:8a4c:5c4f:26e1::/64", the Core's own string), an IPv6 prefix → its
// /64, IPv4 "/32" → the address. Brackets and zones are dropped; anything
// else is returned trimmed (still one distinct IP each).
func Of(raw string) string {
	s := strings.TrimSpace(raw)
	if addr, err := netip.ParseAddr(s); err == nil {
		if addr.Is4() {
			return s
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

// Set returns the distinct keys of raw addresses ("" skipped).
func Set(raw map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(raw))
	for ip := range raw {
		if key := Of(ip); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}
