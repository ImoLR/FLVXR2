// Package nodeaddr collects the node's public interface addresses for the
// panel's node DDNS (plan 142): global unicast IPv6 (no link-local, ULA,
// documentation, temporary/privacy, deprecated, tentative or DAD-failed
// addresses) and public IPv4, skipping container/virtual interfaces. The
// interface carrying the default route comes first.
package nodeaddr

import (
	"net/netip"
	"sort"
	"strings"
)

// Report is what the agent sends ("addrs" in the metric message).
type Report struct {
	V4 []string `json:"v4"`
	V6 []string `json:"v6"`
}

// Equal compares two reports including order.
func (r Report) Equal(o Report) bool {
	return strings.Join(r.V4, ",") == strings.Join(o.V4, ",") && strings.Join(r.V6, ",") == strings.Join(o.V6, ",")
}

// MaxPerFamily caps each family of a report.
const MaxPerFamily = 16

// Addr is one interface address as read from netlink.
type Addr struct {
	IP           netip.Addr
	Iface        string
	DefaultRoute bool // the interface carries a default route of this family
	Temporary    bool // IFA_F_TEMPORARY (privacy extension)
	Deprecated   bool // IFA_F_DEPRECATED or preferred lifetime 0
	Tentative    bool // IFA_F_TENTATIVE / IFA_F_DADFAILED
}

// virtualPrefixes are interfaces whose addresses are never node entries.
var virtualPrefixes = []string{"lo", "docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "kube", "lxc", "lxd", "tailscale", "zt", "wg", "tun", "tap", "dummy", "gost"}

func virtualIface(name string) bool {
	for _, p := range virtualPrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

var nonPublicV6 = []netip.Prefix{
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2002::/16"),
}

var nonPublicV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

func publicV6(ip netip.Addr) bool {
	if !ip.Is6() || ip.Is4In6() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublicV6 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func publicV4(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublicV4 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Filter turns raw interface addresses into a report: public, stable
// addresses only, default-route interface first, then interface name and
// address order; deduplicated and capped.
func Filter(addrs []Addr) Report {
	type item struct {
		Addr
		key string
		i   int
	}
	var v4, v6 []item
	for i, a := range addrs {
		if !a.IP.IsValid() || virtualIface(a.Iface) || a.Tentative || a.Deprecated || a.Temporary {
			continue
		}
		switch {
		case a.IP.Is4() || a.IP.Is4In6():
			if publicV4(a.IP) {
				v4 = append(v4, item{a, a.IP.Unmap().String(), i})
			}
		case publicV6(a.IP):
			v6 = append(v6, item{a, a.IP.String(), i})
		}
	}
	order := func(list []item) []string {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].DefaultRoute != list[j].DefaultRoute {
				return list[i].DefaultRoute
			}
			if list[i].Iface != list[j].Iface {
				return list[i].Iface < list[j].Iface
			}
			return list[i].i < list[j].i
		})
		out := []string{}
		seen := map[string]bool{}
		for _, it := range list {
			if !seen[it.key] && len(out) < MaxPerFamily {
				seen[it.key] = true
				out = append(out, it.key)
			}
		}
		return out
	}
	return Report{V4: order(v4), V6: order(v6)}
}
