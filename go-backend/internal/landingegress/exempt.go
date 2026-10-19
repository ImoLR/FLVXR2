package landingegress

import (
	"net/netip"
	"sort"
	"strings"
)

// ExemptSources turns FLVX node addresses (IPs or hostnames) plus the admin
// whitelist into a sorted CIDR list. Hostnames go through resolve, which the
// caller caches; unresolvable names are skipped.
func ExemptSources(hosts []string, whitelist []string, resolve func(string) []netip.Addr) []string {
	set := map[string]bool{}
	add := func(a netip.Addr) {
		a = a.Unmap()
		if a.IsValid() && !a.IsUnspecified() {
			set[netip.PrefixFrom(a, a.BitLen()).String()] = true
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
