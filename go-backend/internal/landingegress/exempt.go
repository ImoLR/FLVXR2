package landingegress

import (
	"net/netip"
	"sort"
	"strings"
)

// NodeIPv6ExemptBits: an FLVX node's IPv6 address is exempted as its /64
// (plan 149). Outgoing connections often leave from a temporary (privacy)
// or another SLAAC address of the same /64 than the one the panel knows, and
// a /64 belongs to one machine or customer; a missed node address would block
// forwarded traffic of that node (禁止大陆直连).
const NodeIPv6ExemptBits = 64

// ExemptSources turns FLVX node addresses (IPs or hostnames) plus the admin
// whitelist into a sorted CIDR list. Hostnames go through resolve, which the
// caller caches; unresolvable names are skipped. Node IPv4 addresses are
// exempt as /32, node IPv6 addresses as their /64; whitelist entries as given.
func ExemptSources(hosts []string, whitelist []string, resolve func(string) []netip.Addr) []string {
	set := map[string]bool{}
	add := func(a netip.Addr) {
		a = a.Unmap()
		if !a.IsValid() || a.IsUnspecified() {
			return
		}
		bits := a.BitLen()
		if a.Is6() {
			bits = NodeIPv6ExemptBits
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
	for _, w := range whitelist {
		if p, err := parseIPOrCIDR(w); err == nil {
			set[p.String()] = true
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
