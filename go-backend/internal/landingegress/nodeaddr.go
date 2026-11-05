package landingegress

import (
	"net/netip"
	"sort"
	"strings"
)

// NodeIPv6Bits: an FLVX node's IPv6 address counts as its /64. Outgoing
// connections often leave from another SLAAC address of the same /64 than
// the one the panel knows, and a /64 belongs to one machine or customer.
const NodeIPv6Bits = 64

// NodeAddressPrefixes turns FLVX node addresses (IPs or hostnames) into a
// sorted CIDR list for the chain-upstream guard (plan 108: a 链式代理 / 外部
// 节点 upstream must never be one of our nodes). Hostnames go through
// resolve, which the caller caches; unresolvable names are skipped. IPv4
// addresses count as /32, IPv6 addresses as their /64.
//
// Plan 219: landing plans no longer carry a source exemption list (the
// former ExemptSources); this helper only serves the guard.
func NodeAddressPrefixes(hosts []string, resolve func(string) []netip.Addr) []string {
	set := map[string]bool{}
	add := func(a netip.Addr) {
		a = a.Unmap()
		if !a.IsValid() || a.IsUnspecified() {
			return
		}
		bits := a.BitLen()
		if a.Is6() {
			bits = NodeIPv6Bits
		}
		p, err := a.Prefix(bits)
		if err == nil {
			set[p.String()] = true
		}
	}
	for _, h := range hosts {
		for _, part := range strings.FieldsFunc(h, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == ';' }) {
			part = strings.Trim(strings.TrimSpace(part), "[]")
			if part == "" {
				continue
			}
			if a, err := netip.ParseAddr(part); err == nil {
				add(a)
				continue
			}
			if p, err := netip.ParsePrefix(part); err == nil {
				set[p.Masked().String()] = true
				continue
			}
			if resolve != nil && hostnameRe.MatchString(strings.ToLower(part)) {
				for _, a := range resolve(strings.ToLower(part)) {
					add(a)
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
