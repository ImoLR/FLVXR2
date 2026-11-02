package sublink

import (
	"strconv"
	"strings"
)

// loonVMessCipher maps FLVX VMess ciphers to Loon's list (none, auto,
// aes-128-gcm, chacha20-ietf-poly1305, aes-128-cfb).
var loonVMessCipher = map[string]string{"": "auto", "auto": "auto", "aes-128-gcm": "aes-128-gcm",
	"chacha20-poly1305": "chacha20-ietf-poly1305", "none": "none", "zero": "none"}

// loonProxy converts one node to the part after "Name = " of a Loon node
// line; ok=false when Loon cannot express it. Supported per nsloon.app:
// Shadowsocks / ss2022 (AES), VMess and VLESS (tcp / ws, TLS, Reality,
// Vision), Trojan (tcp / ws), Hysteria2, HTTP(S), SOCKS5(-TLS), AnyTLS.
// Never: Snell, Mieru, gRPC transports.
func loonProxy(n Node) (string, bool) {
	p := []string{}
	kv := func(k, v string) { p = append(p, k+"="+v) }
	pos := func(v string) bool {
		q, ok := quoted(v)
		if ok {
			p = append(p, q)
		}
		return ok
	}
	tlsParams := func(sni, pin string, insecure bool) {
		if sni != "" {
			kv("sni", sni)
		}
		if h := hexPin(pin); h != "" {
			kv("tls-cert-sha256", h)
		} else if insecure {
			kv("skip-cert-verify", "true")
		}
	}
	head := func(t string) {
		p = append(p, t, strings.Trim(n.Server(), "[]"), strconv.Itoa(n.Port()))
	}
	if e := n.Extra; e != nil {
		if e.Type != "anytls" {
			return "", false
		}
		head("AnyTLS")
		if !pos(e.Password) {
			return "", false
		}
		tlsParams(e.SNI, e.CertSHA256, true)
		kv("udp", "true")
		return strings.Join(p, ","), true
	}
	u := n.Up
	sni, pin := "", ""
	if u.TLS != nil {
		sni, pin = u.TLS.ServerName, u.TLS.PinnedSHA256
	}
	transport := func() bool {
		switch u.Network {
		case "", "tcp":
			kv("transport", "tcp")
			return true
		case "ws":
			kv("transport", "ws")
			path, host := "/", ""
			if u.WS != nil {
				path, host = u.WS.Path, u.WS.Host
			}
			v, ok := pathValue(path)
			if !ok {
				return false
			}
			kv("path", v)
			if host != "" {
				kv("host", host)
			}
			return true
		}
		return false
	}
	switch u.Type {
	case "shadowsocks":
		if !ssLineMethod[u.Method] {
			return "", false
		}
		head("Shadowsocks")
		p = append(p, u.Method)
		if !pos(u.Password) {
			return "", false
		}
		kv("udp", "true")
	case "vmess":
		cipher, ok := loonVMessCipher[u.Cipher]
		if !ok {
			return "", false
		}
		head("VMess")
		p = append(p, cipher)
		if !pos(u.UUID) || !transport() {
			return "", false
		}
		kv("alterId", "0")
		if u.Security == "tls" {
			kv("over-tls", "true")
			tlsParams(sni, pin, n.Insecure)
		}
		kv("udp", "true")
	case "vless":
		head("VLESS")
		if !pos(u.UUID) || !transport() {
			return "", false
		}
		if u.Flow != "" {
			kv("flow", u.Flow)
		}
		switch u.Security {
		case "reality":
			if u.Reality == nil {
				return "", false
			}
			kv("public-key", `"`+u.Reality.PublicKey+`"`)
			if u.Reality.ShortID != "" {
				kv("short-id", u.Reality.ShortID)
			}
			kv("over-tls", "true")
			kv("sni", u.Reality.ServerName)
			kv("tls-profile", "chrome")
		case "tls":
			kv("over-tls", "true")
			tlsParams(sni, pin, n.Insecure)
		}
		kv("udp", "true")
	case "trojan":
		if u.Security == "none" {
			return "", false
		}
		head("Trojan")
		if !pos(u.Password) || !transport() {
			return "", false
		}
		tlsParams(sni, pin, n.Insecure)
		kv("udp", "true")
	case "hysteria2":
		head("Hysteria2")
		if !pos(u.Password) {
			return "", false
		}
		tlsParams(sni, pin, n.Insecure)
		if u.Obfs != "" {
			// Unquoted, as in Loon's documentation.
			if !plain(u.Obfs) {
				return "", false
			}
			kv("salamander-password", u.Obfs)
		}
		kv("udp", "true")
	case "socks", "http":
		t := map[string]string{"socks": "socks5", "http": "http"}[u.Type]
		if u.Type == "http" && u.Security == "tls" {
			t = "https"
		}
		head(t)
		if u.Username != "" || u.Password != "" {
			if !pos(u.Username) || !pos(u.Password) {
				return "", false
			}
		}
		if u.Security == "tls" {
			if u.Type == "socks" {
				kv("over-tls", "true")
			}
			tlsParams(sni, pin, n.Insecure)
		}
		if u.Type == "socks" {
			kv("udp", "true")
		}
	default:
		return "", false
	}
	return strings.Join(p, ","), true
}

// Loon renders a Loon subscription: one Loon node line per node (no
// sections, no rules: Loon subscriptions are node resources, the rules stay
// in the app). It returns the body and how many nodes it contains.
func Loon(nodes []Node) ([]byte, int) {
	kept := make([]Node, 0, len(nodes))
	bodies := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := loonProxy(n); ok {
			kept = append(kept, n)
			bodies = append(bodies, s)
		}
	}
	if len(kept) == 0 {
		return emptyNodeList("Loon"), 0
	}
	names := lineNames(kept, false)
	var b strings.Builder
	for i := range kept {
		b.WriteString(names[i] + " = " + bodies[i] + "\n")
	}
	return []byte(b.String()), len(kept)
}
