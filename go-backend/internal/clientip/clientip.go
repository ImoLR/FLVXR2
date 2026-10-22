// Package clientip resolves the address of the browser / client behind the
// panel's own proxies, for rate limits and connection caps only (never for
// authorization).
//
// Deployment chain: Cloudflare -> cloudflared (host) -> frontend nginx
// (docker, sets X-Real-IP $remote_addr) -> backend, or cloudflared -> backend
// directly (agent domain). Headers are only trusted when the hop that set
// them is a local/private proxy:
//
//   - RemoteAddr public: the client talked to the backend port directly.
//   - X-Real-IP (overwritten by our nginx) public: the client talked to the
//     nginx port directly; a client-sent Cf-Connecting-Ip is ignored.
//   - otherwise the request came through cloudflared: Cf-Connecting-Ip (set by
//     Cloudflare, which overwrites any client value), then the right-most
//     public X-Forwarded-For entry.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// FromRequest returns the best-effort client IP as a string ("" if unknown).
func FromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	peer := parse(r.RemoteAddr)
	if peer != nil && !trusted(peer) {
		return peer.String()
	}
	if xr := parse(r.Header.Get("X-Real-IP")); xr != nil {
		peer = xr
		if !trusted(xr) {
			return xr.String()
		}
	}
	if cf := parse(r.Header.Get("Cf-Connecting-Ip")); cf != nil {
		return cf.String()
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if ip := parse(parts[i]); ip != nil && !trusted(ip) {
				return ip.String()
			}
		}
	}
	if peer != nil {
		return peer.String()
	}
	return ""
}

// LimitKey is FromRequest grouped for per-address limits: IPv4 as is, IPv6 by
// /64 (one customer usually owns a whole /64, so per-address caps would be
// trivial to dodge otherwise).
func LimitKey(r *http.Request) string {
	s := FromRequest(r)
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() != nil {
		return s
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func trusted(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func parse(raw string) net.IP {
	v := strings.Trim(strings.TrimSpace(raw), "\"")
	if v == "" {
		return nil
	}
	if ip := net.ParseIP(v); ip != nil {
		return norm(ip)
	}
	host, _, err := net.SplitHostPort(v)
	if err != nil {
		return nil
	}
	return norm(net.ParseIP(strings.Trim(host, "[]")))
}

func norm(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}
