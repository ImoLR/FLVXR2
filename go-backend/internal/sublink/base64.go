package sublink

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func hostPort(host string, port int) string {
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}

func frag(name string) string { return "#" + url.PathEscape(name) }

// withName replaces the fragment (node name) of an existing share link.
func withName(uri, name string) string {
	if i := strings.Index(uri, "#"); i >= 0 {
		uri = uri[:i]
	}
	return uri + frag(name)
}

// ShareURI is the node as a share link ("" when no link format exists, e.g.
// Snell). Links FLVX already builds (landing / rule links) are reused.
func ShareURI(n Node) string {
	if n.URI != "" {
		return withName(n.URI, n.Name)
	}
	if n.Extra != nil {
		return ""
	}
	u := n.Up
	addr := hostPort(u.Server, u.Port)
	q := url.Values{}
	tlsQuery := func() {
		if u.TLS != nil {
			if u.TLS.ServerName != "" {
				q.Set("sni", u.TLS.ServerName)
			}
			if len(u.TLS.ALPN) > 0 {
				q.Set("alpn", strings.Join(u.TLS.ALPN, ","))
			}
			if u.TLS.Fingerprint != "" {
				q.Set("fp", u.TLS.Fingerprint)
			}
			if u.TLS.PinnedSHA256 != "" {
				q.Set("pinSHA256", u.TLS.PinnedSHA256)
			}
		}
		if n.Insecure {
			q.Set("allowInsecure", "1")
		}
	}
	transportQuery := func() {
		switch u.Network {
		case "ws":
			q.Set("type", "ws")
			if u.WS != nil {
				q.Set("path", u.WS.Path)
				if u.WS.Host != "" {
					q.Set("host", u.WS.Host)
				}
			}
		case "grpc":
			q.Set("type", "grpc")
			if u.GRPC != nil {
				q.Set("serviceName", u.GRPC.ServiceName)
				if u.GRPC.MultiMode {
					q.Set("mode", "multi")
				}
			}
		default:
			q.Set("type", "tcp")
		}
	}
	switch u.Type {
	case "shadowsocks":
		if strings.HasPrefix(u.Method, "2022-") {
			userinfo := url.QueryEscape(u.Method) + ":" + url.QueryEscape(u.Password)
			return "ss://" + strings.ReplaceAll(userinfo, "+", "%20") + "@" + addr + frag(n.Name)
		}
		return "ss://" + base64.RawURLEncoding.EncodeToString([]byte(u.Method+":"+u.Password)) + "@" + addr + frag(n.Name)
	case "vless":
		q.Set("encryption", "none")
		transportQuery()
		if u.Flow != "" {
			q.Set("flow", u.Flow)
		}
		switch u.Security {
		case "reality":
			if u.Reality == nil {
				return ""
			}
			q.Set("security", "reality")
			q.Set("sni", u.Reality.ServerName)
			q.Set("pbk", u.Reality.PublicKey)
			q.Set("sid", u.Reality.ShortID)
			fp := u.Reality.Fingerprint
			if fp == "" {
				fp = "chrome"
			}
			q.Set("fp", fp)
		case "tls":
			q.Set("security", "tls")
			tlsQuery()
		default:
			q.Set("security", "none")
		}
		return "vless://" + url.PathEscape(u.UUID) + "@" + addr + "?" + q.Encode() + frag(n.Name)
	case "vmess":
		j := map[string]interface{}{"v": "2", "ps": n.Name, "add": strings.Trim(u.Server, "[]"), "port": strconv.Itoa(u.Port), "id": u.UUID, "aid": "0", "scy": u.Cipher, "net": "tcp", "type": "none", "host": "", "path": "", "tls": ""}
		switch u.Network {
		case "ws":
			j["net"] = "ws"
			if u.WS != nil {
				j["path"], j["host"] = u.WS.Path, u.WS.Host
			}
		case "grpc":
			j["net"] = "grpc"
			if u.GRPC != nil {
				j["path"] = u.GRPC.ServiceName
				if u.GRPC.MultiMode {
					j["type"] = "multi"
				}
			}
		}
		if u.Security == "tls" {
			j["tls"] = "tls"
			if u.TLS != nil {
				j["sni"], j["fp"] = u.TLS.ServerName, u.TLS.Fingerprint
				if len(u.TLS.ALPN) > 0 {
					j["alpn"] = strings.Join(u.TLS.ALPN, ",")
				}
			}
			if n.Insecure {
				j["allowInsecure"] = true
			}
		}
		b, _ := json.Marshal(j)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "trojan":
		q.Set("security", "tls")
		transportQuery()
		tlsQuery()
		return "trojan://" + url.PathEscape(u.Password) + "@" + addr + "?" + q.Encode() + frag(n.Name)
	case "hysteria2":
		if u.Obfs != "" {
			q.Set("obfs", "salamander")
			q.Set("obfs-password", u.Obfs)
		}
		tlsQuery()
		if n.Insecure {
			q.Del("allowInsecure")
			q.Set("insecure", "1")
		}
		s := "hysteria2://" + url.PathEscape(u.Password) + "@" + addr + "/"
		if len(q) > 0 {
			s += "?" + q.Encode()
		}
		return s + frag(n.Name)
	case "socks":
		s := "socks://"
		if u.Username != "" || u.Password != "" {
			s += base64.StdEncoding.EncodeToString([]byte(u.Username+":"+u.Password)) + "@"
		}
		return s + addr + frag(n.Name)
	case "http":
		scheme := "http"
		if u.Security == "tls" {
			scheme = "https"
			tlsQuery()
		}
		s := scheme + "://"
		if u.Username != "" || u.Password != "" {
			s += url.UserPassword(u.Username, u.Password).String() + "@"
		}
		s += addr
		if len(q) > 0 {
			s += "/?" + q.Encode()
		}
		return s + frag(n.Name)
	}
	return ""
}

// Base64 renders the generic subscription: one share link per line, the
// whole list Base64 encoded (Shadowrocket / v2rayN / v2rayNG / V2Box).
func Base64(nodes []Node) ([]byte, int) {
	lines := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s := ShareURI(n); s != "" {
			lines = append(lines, s)
		}
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))), len(lines)
}
