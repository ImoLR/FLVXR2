// Package cfnet tells whether an IP belongs to Cloudflare's proxy network
// (plan 290).
//
// A Reality dest on Cloudflare lets anyone who reaches the landing without a
// key be relayed to ANY Cloudflare-hosted site (Cloudflare routes by SNI), which
// is how such landings get abused as botnet C2 relays (DMIT notice 11-22).
//
// The ranges are built in and are the source of truth (cloudflare.com/ips is
// not reachable from every panel host). They equal
// https://api.cloudflare.com/client/v4/ips as checked on 2026-11-22
// (etag 38f79d050aa027e3be3865e495dcc9bc).
package cfnet

import (
	"net/netip"
	"strings"
)

var rangesV4 = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
}

var rangesV6 = []string{
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

var prefixes = func() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(rangesV4)+len(rangesV6))
	for _, s := range append(append([]string{}, rangesV4...), rangesV6...) {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Ranges returns the built-in ranges (copy).
func Ranges() []netip.Prefix { return append([]netip.Prefix(nil), prefixes...) }

// Contains reports whether ip (IPv4, IPv6 or IPv4-mapped IPv6; a zone or
// brackets are tolerated) is a Cloudflare address. Unparseable input is false.
func Contains(ip string) bool {
	s := strings.TrimSpace(ip)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	addr = addr.WithZone("").Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// First returns the first Cloudflare address of ips ("" = none).
func First(ips []string) string {
	for _, ip := range ips {
		if Contains(ip) {
			return strings.TrimSpace(ip)
		}
	}
	return ""
}
