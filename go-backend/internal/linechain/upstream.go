// Package linechain holds the pure parts of 线路机 chain rules (plan 108):
// parsing a user's pasted node link into a typed upstream, validating its
// address and fields, and the summary/hash shown and stored by the panel. It
// performs no database access; DNS lookups go through an injectable resolver.
package linechain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
)

// Upstream is the user's node as a typed outbound. Its JSON matches the
// agent's landing EgressOutbound (go-gost/x/landing), so it is sent as is in
// the chain node's "chain" field. It holds secrets: never return it to clients.
type Upstream struct {
	Tag       string   `json:"tag,omitempty"`
	Type      string   `json:"type"`
	IPVersion string   `json:"ip_version"`
	Server    string   `json:"server"`
	Port      int      `json:"port"`
	Username  string   `json:"username,omitempty"`
	Password  string   `json:"password,omitempty"`
	Method    string   `json:"method,omitempty"`
	UUID      string   `json:"uuid,omitempty"`
	Flow      string   `json:"flow,omitempty"`
	Reality   *Reality `json:"reality,omitempty"`
	Network   string   `json:"network,omitempty"`
	Security  string   `json:"security,omitempty"`
	TLS       *TLS     `json:"tls,omitempty"`
	WS        *WS      `json:"ws,omitempty"`
	GRPC      *GRPC    `json:"grpc,omitempty"`
	Cipher    string   `json:"cipher,omitempty"`
	Obfs      string   `json:"obfs,omitempty"`
}

type Reality struct {
	ServerName  string `json:"server_name"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type TLS struct {
	ServerName   string   `json:"server_name,omitempty"`
	ALPN         []string `json:"alpn,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	PinnedSHA256 string   `json:"pinned_sha256,omitempty"`
}

type WS struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type GRPC struct {
	ServiceName string `json:"service_name,omitempty"`
	MultiMode   bool   `json:"multi_mode,omitempty"`
}

// UDPOnly reports upstreams that cannot be probed with a TCP connect.
func (u Upstream) UDPOnly() bool { return u.Type == "hysteria2" }

// Address is host:port with IPv6 brackets.
func (u Upstream) Address() string { return net.JoinHostPort(u.Server, strconv.Itoa(u.Port)) }

// ProtocolLabel is the short protocol name shown in summaries.
func (u Upstream) ProtocolLabel() string {
	switch u.Type {
	case "shadowsocks":
		if len(u.Method) > 5 && u.Method[:5] == "2022-" {
			return "SS2022"
		}
		return "Shadowsocks"
	case "vless", "vmess", "trojan":
		name := map[string]string{"vless": "VLESS", "vmess": "VMess", "trojan": "Trojan"}[u.Type]
		switch u.Network {
		case "ws":
			name += " WS"
		case "grpc":
			name += " gRPC"
		}
		switch u.Security {
		case "reality":
			name += " Reality"
		case "tls":
			if u.Type != "trojan" {
				name += " TLS"
			}
		}
		return name
	case "socks":
		return "SOCKS5"
	case "http":
		if u.Security == "tls" {
			return "HTTPS"
		}
		return "HTTP"
	case "hysteria2":
		return "Hysteria2"
	}
	return u.Type
}

// Summary is "协议 · 地址:端口" without any credential.
func (u Upstream) Summary() string { return u.ProtocolLabel() + " · " + u.Address() }

// Hash identifies the same node behind two pastes (the link name and field
// order do not matter); it is stored to refuse duplicates.
func (u Upstream) Hash() string {
	u.Tag = ""
	b, _ := json.Marshal(u)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// JSON is the stored form.
func (u Upstream) JSON() string {
	b, _ := json.Marshal(u)
	return string(b)
}

// FromJSON restores a stored upstream.
func FromJSON(s string) (Upstream, error) {
	var u Upstream
	err := json.Unmarshal([]byte(s), &u)
	return u, err
}
