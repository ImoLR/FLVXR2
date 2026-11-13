package sublink

import (
	"strconv"
	"strings"
)

// qxVMessMethod maps FLVX VMess ciphers to Quantumult X's (none,
// aes-128-gcm, chacha20-poly1305); the client picks the cipher, any value
// works with the server.
var qxVMessMethod = map[string]string{"": "chacha20-poly1305", "auto": "chacha20-poly1305", "aes-128-gcm": "aes-128-gcm",
	"chacha20-poly1305": "chacha20-poly1305", "none": "none", "zero": "none"}

// qxProxy converts one node to a Quantumult X server line without its tag;
// ok=false when Quantumult X cannot express it. Supported per the official
// sample (github.com/crossutility/Quantumult-X sample.conf): shadowsocks /
// ss2022 (AES), vmess and vless (tcp / ws, TLS, Reality, Vision), trojan
// (tcp, wss), http(s), socks5(-tls), anytls. Never: Hysteria2, Snell, Mieru,
// gRPC transports. Values cannot be quoted, so secrets with a comma, quote
// or whitespace make the node unsupported.
func qxProxy(n Node) (string, bool) {
	addr := func(t string) string { return t + "=" + strings.Trim(n.Server(), "[]") + ":" + strconv.Itoa(n.Port()) }
	p := []string{}
	kv := func(k, v string) { p = append(p, k+"="+v) }
	secret := func(k, v string) bool {
		if !plain(v) {
			return false
		}
		kv(k, v)
		return true
	}
	verify := func(pin string, insecure bool) {
		if h := hexPin(pin); h != "" {
			kv("tls-verification", "true")
			kv("tls-cert-sha256", h)
		} else if insecure {
			kv("tls-verification", "false")
		} else {
			kv("tls-verification", "true")
		}
	}
	if e := n.Extra; e != nil {
		if e.Type != "anytls" {
			return "", false
		}
		p = append(p, addr("anytls"))
		if !secret("password", e.Password) {
			return "", false
		}
		kv("over-tls", "true")
		if e.SNI != "" {
			kv("tls-host", e.SNI)
		}
		verify(e.CertSHA256, true)
		kv("udp-relay", "true")
		return strings.Join(p, ", "), true
	}
	u := n.Up
	sni, pin := "", ""
	if u.TLS != nil {
		sni, pin = u.TLS.ServerName, u.TLS.PinnedSHA256
	}
	// obfs: VMess / VLESS transport + TLS in Quantumult X's obfs field
	// (over-tls, ws, wss); obfs-host is the TLS server name and/or the
	// WebSocket Host header.
	obfs := func(tls bool, tlsHost string) bool {
		switch u.Network {
		case "", "tcp":
			if tls {
				kv("obfs", "over-tls")
				if tlsHost != "" {
					kv("obfs-host", tlsHost)
				}
			}
			return true
		case "ws":
			path, host := "/", ""
			if u.WS != nil {
				path, host = u.WS.Path, u.WS.Host
			}
			if !plain(path) {
				return false
			}
			if tls {
				kv("obfs", "wss")
				if host == "" {
					host = tlsHost
				}
			} else {
				kv("obfs", "ws")
			}
			if host != "" {
				kv("obfs-host", host)
			}
			kv("obfs-uri", path)
			return true
		}
		return false
	}
	switch u.Type {
	case "shadowsocks":
		if !ssLineMethod[u.Method] {
			return "", false
		}
		p = append(p, addr("shadowsocks"))
		kv("method", u.Method)
		if !secret("password", u.Password) {
			return "", false
		}
		kv("udp-relay", "true")
	case "vmess":
		method, ok := qxVMessMethod[u.Cipher]
		if !ok {
			return "", false
		}
		p = append(p, addr("vmess"))
		kv("method", method)
		kv("password", u.UUID)
		tls := u.Security == "tls"
		if !obfs(tls, sni) {
			return "", false
		}
		if tls {
			verify(pin, n.Insecure)
		}
		kv("udp-relay", "true")
	case "vless":
		// Plan 253: Quantumult X documents only method=none for VLESS.
		if u.Encrypted() {
			return "", false
		}
		p = append(p, addr("vless"))
		kv("method", "none")
		kv("password", u.UUID)
		switch u.Security {
		case "reality":
			if u.Reality == nil || !obfs(true, u.Reality.ServerName) {
				return "", false
			}
			kv("reality-base64-pubkey", u.Reality.PublicKey)
			if u.Reality.ShortID != "" {
				kv("reality-hex-shortid", u.Reality.ShortID)
			}
		case "tls":
			if !obfs(true, sni) {
				return "", false
			}
			verify(pin, n.Insecure)
		default:
			if !obfs(false, "") {
				return "", false
			}
		}
		if u.Flow != "" {
			kv("vless-flow", u.Flow)
		}
		kv("udp-relay", "true")
	case "trojan":
		if u.Security == "none" {
			return "", false
		}
		p = append(p, addr("trojan"))
		if !secret("password", u.Password) {
			return "", false
		}
		switch u.Network {
		case "", "tcp":
			kv("over-tls", "true")
			if sni != "" {
				kv("tls-host", sni)
			}
		case "ws":
			// Trojan over WebSocket is obfs=wss only (no over-tls / tls-host).
			if !obfs(true, sni) {
				return "", false
			}
		default:
			return "", false
		}
		verify(pin, n.Insecure)
		kv("udp-relay", "true")
	case "socks", "http":
		t := map[string]string{"socks": "socks5", "http": "http"}[u.Type]
		p = append(p, addr(t))
		if u.Username != "" && !secret("username", u.Username) || u.Password != "" && !secret("password", u.Password) {
			return "", false
		}
		if u.Security == "tls" {
			kv("over-tls", "true")
			if sni != "" {
				kv("tls-host", sni)
			}
			verify(pin, n.Insecure)
		}
		if u.Type == "socks" {
			kv("udp-relay", "true")
		}
	default:
		return "", false
	}
	return strings.Join(p, ", "), true
}

// QX renders a Quantumult X server resource (the [server_remote] format):
// one server line per node, no sections or rules (the rules stay in the
// app). It returns the body and how many nodes it contains.
func QX(nodes []Node) ([]byte, int) {
	kept := make([]Node, 0, len(nodes))
	bodies := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := qxProxy(n); ok {
			kept = append(kept, n)
			bodies = append(bodies, s)
		}
	}
	if len(kept) == 0 {
		return emptyNodeList("Quantumult X"), 0
	}
	names := lineNames(kept, true)
	var b strings.Builder
	for i := range kept {
		b.WriteString(bodies[i] + ", tag=" + names[i] + "\n")
	}
	return []byte(b.String()), len(kept)
}
