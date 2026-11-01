package sublink

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// kv / omap: an ordered YAML mapping (yaml.v3 sorts plain maps).
type kv struct {
	k string
	v interface{}
}

type omap []kv

func (m omap) MarshalYAML() (interface{}, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range m {
		var v yaml.Node
		if err := v.Encode(e.v); err != nil {
			return nil, err
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.k}, &v)
	}
	return n, nil
}

func (m *omap) add(k string, v interface{}) { *m = append(*m, kv{k, v}) }

// addIf adds non-empty strings / non-nil values only.
func (m *omap) addIf(k string, v interface{}) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return
		}
	case []string:
		if len(x) == 0 {
			return
		}
	case nil:
		return
	}
	m.add(k, v)
}

func firstPin(p string) string {
	if i := strings.Index(p, ","); i >= 0 {
		return p[:i]
	}
	return p
}

// clashProxy converts one node; ok=false when Clash cannot express it.
func clashProxy(n Node, stash bool) (omap, bool) {
	p := omap{{"name", n.Name}}
	if e := n.Extra; e != nil {
		switch e.Type {
		case "anytls":
			if stash {
				return nil, false
			}
			p.add("type", "anytls")
			p.add("server", e.Server)
			p.add("port", e.Port)
			p.add("password", e.Password)
			p.addIf("sni", e.SNI)
			if e.CertSHA256 != "" {
				p.add("fingerprint", e.CertSHA256)
			} else {
				p.add("skip-cert-verify", true)
			}
			p.add("client-fingerprint", "chrome")
			p.add("udp", e.UDP)
		case "snell":
			p.add("type", "snell")
			p.add("server", e.Server)
			p.add("port", e.Port)
			p.add("psk", e.Password)
			v := e.Version
			if v == 0 {
				v = 4
			}
			p.add("version", v)
			p.add("udp", e.UDP)
			if e.Obfs == "http" || e.Obfs == "tls" {
				p.add("obfs-opts", omap{{"mode", e.Obfs}, {"host", e.ObfsHost}})
			}
		case "mieru":
			if stash {
				return nil, false
			}
			t := e.Transport
			if t == "" {
				t = "TCP"
			}
			p.add("type", "mieru")
			p.add("server", e.Server)
			p.add("port", e.Port)
			p.add("transport", t)
			p.add("username", e.Username)
			p.add("password", e.Password)
			p.add("multiplexing", "MULTIPLEXING_LOW")
		default:
			return nil, false
		}
		return p, true
	}
	u := n.Up
	tlsOpts := func(sniKey string) {
		if u.TLS != nil {
			p.addIf(sniKey, u.TLS.ServerName)
			p.addIf("alpn", u.TLS.ALPN)
			p.addIf("client-fingerprint", u.TLS.Fingerprint)
			p.addIf("fingerprint", firstPin(u.TLS.PinnedSHA256))
		}
		if n.Insecure {
			p.add("skip-cert-verify", true)
		}
	}
	transport := func() {
		switch u.Network {
		case "ws":
			p.add("network", "ws")
			ws := omap{}
			if u.WS != nil {
				ws.add("path", u.WS.Path)
				if u.WS.Host != "" {
					ws.add("headers", omap{{"Host", u.WS.Host}})
				}
			}
			p.add("ws-opts", ws)
		case "grpc":
			p.add("network", "grpc")
			name := ""
			if u.GRPC != nil {
				name = u.GRPC.ServiceName
			}
			p.add("grpc-opts", omap{{"grpc-service-name", name}})
		default:
			p.add("network", "tcp")
		}
	}
	server := func(t string) {
		p.add("type", t)
		p.add("server", u.Server)
		p.add("port", u.Port)
	}
	switch u.Type {
	case "shadowsocks":
		server("ss")
		p.add("cipher", u.Method)
		p.add("password", u.Password)
		p.add("udp", true)
	case "vless":
		server("vless")
		p.add("uuid", u.UUID)
		p.add("udp", true)
		transport()
		switch u.Security {
		case "reality":
			if u.Reality == nil {
				return nil, false
			}
			p.add("tls", true)
			p.addIf("flow", u.Flow)
			p.add("servername", u.Reality.ServerName)
			p.add("reality-opts", omap{{"public-key", u.Reality.PublicKey}, {"short-id", u.Reality.ShortID}})
			fp := u.Reality.Fingerprint
			if fp == "" {
				fp = "chrome"
			}
			p.add("client-fingerprint", fp)
		case "tls":
			p.add("tls", true)
			p.addIf("flow", u.Flow)
			tlsOpts("servername")
		default:
			p.add("tls", false)
		}
	case "vmess":
		server("vmess")
		p.add("uuid", u.UUID)
		p.add("alterId", 0)
		cipher := u.Cipher
		if cipher == "" {
			cipher = "auto"
		}
		p.add("cipher", cipher)
		p.add("udp", true)
		transport()
		if u.Security == "tls" {
			p.add("tls", true)
			tlsOpts("servername")
		}
	case "trojan":
		server("trojan")
		p.add("password", u.Password)
		p.add("udp", true)
		transport()
		tlsOpts("sni")
	case "hysteria2":
		server("hysteria2")
		p.add("password", u.Password)
		if u.Obfs != "" {
			p.add("obfs", "salamander")
			p.add("obfs-password", u.Obfs)
		}
		tlsOpts("sni")
	case "socks":
		server("socks5")
		p.addIf("username", u.Username)
		p.addIf("password", u.Password)
		p.add("udp", true)
	case "http":
		server("http")
		p.addIf("username", u.Username)
		p.addIf("password", u.Password)
		if u.Security == "tls" {
			p.add("tls", true)
			tlsOpts("sni")
		}
	default:
		return nil, false
	}
	return p, true
}

var privateCIDRs = []string{
	"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
	"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
	"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
	"IP-CIDR6,::1/128,DIRECT,no-resolve",
	"IP-CIDR6,fc00::/7,DIRECT,no-resolve",
	"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
}

// Clash renders a mihomo (Clash.Meta) config. 国内直连 is a trimmed mmwx
// fake-ip template using the client's built-in GEOSITE / GEOIP data (no rule
// lists downloaded from FLVX); 全部走代理 needs no geo data at all. It returns
// the YAML and how many nodes it contains.
func Clash(nodes []Node, mode string, stash bool) ([]byte, int) {
	proxies := []omap{}
	names := []string{}
	for _, n := range nodes {
		if p, ok := clashProxy(n, stash); ok {
			proxies = append(proxies, p)
			names = append(names, n.Name)
		}
	}
	dns := omap{
		{"enable", true},
		{"ipv6", false},
		{"enhanced-mode", "fake-ip"},
		{"fake-ip-range", "198.18.0.1/16"},
		{"fake-ip-filter", []string{"+.lan", "+.local", "localhost.ptlogin2.qq.com"}},
		{"default-nameserver", []string{"223.5.5.5", "119.29.29.29"}},
		{"nameserver", []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}},
		{"proxy-server-nameserver", []string{"https://223.5.5.5/dns-query", "https://120.53.53.53/dns-query"}},
	}
	rules := []string{}
	if mode == ModeGlobal {
		rules = append(rules, privateCIDRs...)
	} else {
		dns.add("nameserver-policy", omap{{"geosite:cn,private", []string{"https://223.5.5.5/dns-query", "https://120.53.53.53/dns-query"}}})
		rules = append(rules, "GEOSITE,private,DIRECT", "GEOIP,private,DIRECT,no-resolve", "GEOSITE,cn,DIRECT", "GEOIP,CN,DIRECT")
	}
	rules = append(rules, "MATCH,"+GroupSelect)
	groups := []omap{}
	if len(names) == 0 {
		groups = append(groups, omap{{"name", GroupSelect}, {"type", "select"}, {"proxies", []string{"DIRECT"}}})
	} else {
		sel := append([]string{GroupAuto}, names...)
		sel = append(sel, "DIRECT")
		groups = append(groups,
			omap{{"name", GroupSelect}, {"type", "select"}, {"proxies", sel}},
			omap{{"name", GroupAuto}, {"type", "url-test"}, {"proxies", names}, {"url", TestURL}, {"interval", 300}, {"tolerance", 50}},
		)
	}
	doc := omap{
		{"mixed-port", 7890},
		{"allow-lan", false},
		{"mode", "rule"},
		{"log-level", "warning"},
		{"unified-delay", true},
		{"dns", dns},
		{"proxies", proxies},
		{"proxy-groups", groups},
		{"rules", rules},
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(doc)
	_ = enc.Close()
	return []byte(b.String()), len(names)
}
