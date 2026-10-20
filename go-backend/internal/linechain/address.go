package linechain

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// blockedPrefixes: private, loopback, link-local (incl. cloud metadata
// 169.254.169.254), CGNAT (incl. 100.100.100.200), benchmark, multicast,
// reserved, ULA (incl. fd00:ec2::254), NAT64 and 6to4/Teredo forms that can
// embed any of those. Unspecified addresses are rejected separately.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2002::/16",
		"fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// BlockedAddr reports addresses a chain upstream may never use.
func BlockedAddr(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	if a.Is4In6() {
		return BlockedAddr(a.Unmap())
	}
	if a.IsUnspecified() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolver is the subset of net.Resolver used here (tests inject a fake).
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Forbidden reports panel and FLVX node addresses with a short reason.
type Forbidden func(netip.Addr) (string, bool)

// ValidateAddress checks the upstream host: IP literals directly, domains by
// resolving every A/AAAA record. Any blocked or forbidden answer rejects the
// node (a domain that also resolves to an internal address is not allowed).
// It returns the usable addresses, IPv4 first.
func ValidateAddress(ctx context.Context, host string, forbidden Forbidden, resolver Resolver) ([]netip.Addr, error) {
	host = normalizeHost(host)
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a.Unmap()}
	} else {
		if !validHostname(host) {
			return nil, errors.New("节点地址无效")
		}
		if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".localhost") || host == "localhost" {
			return nil, errors.New("节点地址不能是内网域名")
		}
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		child, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		got, err := resolver.LookupNetIP(child, "ip", host)
		if err != nil || len(got) == 0 {
			return nil, errors.New("无法解析节点域名 " + host + "，请检查链接或稍后重试")
		}
		if len(got) > 64 {
			got = got[:64]
		}
		for _, a := range got {
			addrs = append(addrs, a.Unmap())
		}
	}
	for _, a := range addrs {
		if BlockedAddr(a) {
			if net.ParseIP(host) != nil {
				return nil, errors.New("节点地址不能是内网、回环、链路本地、组播或保留地址")
			}
			return nil, errors.New("节点域名解析到了内网、回环或保留地址（" + a.String() + "），不允许使用")
		}
		if forbidden != nil {
			if reason, bad := forbidden(a); bad {
				return nil, errors.New("节点地址不能指向" + reason + "（" + a.String() + "）")
			}
		}
	}
	sort.SliceStable(addrs, func(i, j int) bool { return addrs[i].Is4() && !addrs[j].Is4() })
	return addrs, nil
}
