package linechain

import (
	"net"
	"strings"
)

// Rehost points a copy of up at host:port (a panel entry address). A node
// that was addressed by a domain keeps that domain as the TLS SNI and the
// WebSocket Host when the link had none, otherwise the client would send the
// entry IP and fail the certificate / vhost check. Reality keeps its own
// server name (it is always explicit) and is not touched; SS / SOCKS / HTTP /
// plain VMess only get the address swapped.
func Rehost(up Upstream, host string, port int) Upstream {
	orig := strings.Trim(strings.TrimSpace(up.Server), "[]")
	if orig != "" && net.ParseIP(orig) == nil {
		if up.Security == "tls" || up.TLS != nil {
			t := TLS{}
			if up.TLS != nil {
				t = *up.TLS
			}
			if t.ServerName == "" {
				t.ServerName = orig
			}
			up.TLS = &t
		}
		if up.WS != nil && up.WS.Host == "" {
			ws := *up.WS
			ws.Host = orig
			up.WS = &ws
		}
	}
	up.Server, up.Port = strings.Trim(strings.TrimSpace(host), "[]"), port
	return up
}

// Entry protocol filter features (go-gost service.isProtocolBlocked looks
// at the first bytes of every gost TCP connection; plan 283 drops QUIC
// Initial packets on UDP when 屏蔽 TLS is on).
const (
	FilterTLS   = "tls"   // TLS ClientHello (TLS, Reality, Trojan, HTTPS proxy)
	FilterHTTP  = "http"  // HTTP request (HTTP proxy, WebSocket without TLS)
	FilterSOCKS = "socks" // SOCKS greeting
	FilterQUIC  = "quic"  // QUIC on UDP (Hysteria2), blocked with 屏蔽 TLS
	FilterOther = "other" // random-looking (SS, VLESS Encryption, plain VMess/VLESS)
)

// FilterFeature is what the entry filter sees from a client of up.
func (u Upstream) FilterFeature() string {
	switch u.Type {
	case "trojan":
		return FilterTLS
	case "hysteria2":
		return FilterQUIC
	case "socks":
		return FilterSOCKS
	case "http":
		if u.Security == "tls" {
			return FilterTLS
		}
		return FilterHTTP
	case "vless", "vmess":
		if u.Security == "tls" || u.Security == "reality" {
			return FilterTLS
		}
		if u.Network == "ws" {
			return FilterHTTP // the WebSocket upgrade is a plain GET
		}
		return FilterOther // gRPC h2c preface and raw VLESS/VMess are not matched
	}
	return FilterOther
}
