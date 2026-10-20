package linechain

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

const (
	uid = "d77c8b43-40ed-42cd-9150-f3f47d0f534d"
	pbk = "Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws"
)

var ss2022Key = base64.StdEncoding.EncodeToString(make([]byte, 16))

func vmessLink(json string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(json))
}

func TestParseLinkProtocols(t *testing.T) {
	cases := []struct {
		link, summary string
		check         func(t *testing.T, u Upstream)
	}{
		{"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@1.2.3.4:8388#my%20node", "Shadowsocks · 1.2.3.4:8388", func(t *testing.T, u Upstream) {
			if u.Method != "aes-256-gcm" || u.Password != "pass" {
				t.Fatalf("%+v", u)
			}
		}},
		{"ss://2022-blake3-aes-128-gcm:" + strings.ReplaceAll(ss2022Key, "=", "%3D") + "@[2606:4700::1]:443", "SS2022 · [2606:4700::1]:443", nil},
		{"ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-poly1305:p@ss@ss.example.com:8389")), "Shadowsocks · ss.example.com:8389", func(t *testing.T, u Upstream) {
			if u.Method != "chacha20-ietf-poly1305" || u.Password != "p@ss" {
				t.Fatalf("legacy base64 form: %+v", u)
			}
		}},
		{"vless://" + uid + "@Example.COM:443?security=reality&sni=www.example.com&pbk=" + pbk + "&sid=0a1b&fp=chrome&flow=xtls-rprx-vision&type=tcp#r", "VLESS Reality · example.com:443", func(t *testing.T, u Upstream) {
			if u.Reality == nil || u.Reality.PublicKey != pbk || u.Flow != "xtls-rprx-vision" || u.Network != "tcp" {
				t.Fatalf("%+v", u)
			}
		}},
		{"vless://" + uid + "@ws.example.com:443?type=ws&security=tls&path=%2Fray%3Fed%3D2048&host=cdn.example.com&sni=cdn.example.com&alpn=h2,http/1.1", "VLESS WS TLS · ws.example.com:443", func(t *testing.T, u Upstream) {
			if u.WS == nil || u.WS.Path != "/ray?ed=2048" || u.WS.Host != "cdn.example.com" || u.TLS == nil || len(u.TLS.ALPN) != 2 {
				t.Fatalf("%+v %+v", u.WS, u.TLS)
			}
		}},
		{"vless://" + uid + "@g.example.com:443?type=grpc&security=tls&serviceName=svc&mode=multi", "VLESS gRPC TLS · g.example.com:443", func(t *testing.T, u Upstream) {
			if u.GRPC == nil || !u.GRPC.MultiMode || u.GRPC.ServiceName != "svc" {
				t.Fatalf("%+v", u.GRPC)
			}
		}},
		{"vless://" + uid + "@8.8.4.4:80?type=tcp", "VLESS · 8.8.4.4:80", nil},
		{vmessLink(`{"v":"2","ps":"vm","add":"vm.example.com","port":"443","id":"` + uid + `","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/vm","tls":"tls","sni":"","alpn":""}`), "VMess WS TLS · vm.example.com:443", func(t *testing.T, u Upstream) {
			if u.TLS == nil || u.TLS.ServerName != "cdn.example.com" || u.WS.Path != "/vm" {
				t.Fatalf("%+v %+v", u.TLS, u.WS)
			}
		}},
		{vmessLink(`{"add":"9.9.9.9","port":10086,"id":"` + uid + `","aid":0,"net":"tcp","tls":""}`), "VMess · 9.9.9.9:10086", nil},
		{vmessLink(`{"add":"g.example.com","port":443,"id":"` + uid + `","aid":0,"net":"grpc","path":"gsvc","type":"multi","tls":"tls"}`), "VMess gRPC TLS · g.example.com:443", func(t *testing.T, u Upstream) {
			if u.GRPC == nil || u.GRPC.ServiceName != "gsvc" || !u.GRPC.MultiMode {
				t.Fatalf("%+v", u.GRPC)
			}
		}},
		{"trojan://p%40ss@tj.example.com:443?sni=tj.example.com#tj", "Trojan · tj.example.com:443", func(t *testing.T, u Upstream) {
			if u.Password != "p@ss" || u.Security != "tls" {
				t.Fatalf("%+v", u)
			}
		}},
		{"trojan://pass@tj.example.com:443?type=ws&path=/tj&host=tj.example.com", "Trojan WS · tj.example.com:443", nil},
		{"hysteria2://auth%3Apart@hy.example.com:443/?sni=hy.example.com&obfs=salamander&obfs-password=obfs&pinSHA256=AA:" + strings.Repeat("bb", 31), "Hysteria2 · hy.example.com:443", func(t *testing.T, u Upstream) {
			if u.Password != "auth:part" || u.Obfs != "obfs" || u.TLS == nil || u.TLS.PinnedSHA256 != "aa"+strings.Repeat("bb", 31) || !u.UDPOnly() {
				t.Fatalf("%+v %+v", u, u.TLS)
			}
		}},
		{"hy2://pw@1.1.1.1:8443", "Hysteria2 · 1.1.1.1:8443", nil},
		{"socks5://user:pass@1.0.0.1:1080", "SOCKS5 · 1.0.0.1:1080", func(t *testing.T, u Upstream) {
			if u.Username != "user" || u.Password != "pass" {
				t.Fatalf("%+v", u)
			}
		}},
		{"socks://" + base64.StdEncoding.EncodeToString([]byte("u:p")) + "@1.0.0.1:1080", "SOCKS5 · 1.0.0.1:1080", func(t *testing.T, u Upstream) {
			if u.Username != "u" || u.Password != "p" {
				t.Fatalf("base64 userinfo: %+v", u)
			}
		}},
		{"http://1.0.0.2:3128", "HTTP · 1.0.0.2:3128", nil},
		{"https://u:p@proxy.example.com:443", "HTTPS · proxy.example.com:443", nil},
	}
	for _, c := range cases {
		u, _, err := ParseLink(c.link)
		if err != nil {
			t.Errorf("%s: %v", c.link, err)
			continue
		}
		if got := u.Summary(); got != c.summary {
			t.Errorf("%s: summary %q, want %q", c.link, got, c.summary)
		}
		if u.IPVersion != "system" {
			t.Errorf("%s: ip_version %q", c.link, u.IPVersion)
		}
		if c.check != nil {
			c.check(t, u)
		}
	}
	_, name, _ := ParseLink("trojan://pass@tj.example.com:443#%E6%88%91%E7%9A%84%E8%8A%82%E7%82%B9")
	if name != "我的节点" {
		t.Fatalf("name = %q", name)
	}
}

func TestParseLinkRejects(t *testing.T) {
	cases := map[string]string{
		"":                                 "请粘贴",
		"tuic://u:p@1.2.3.4:443":           "TUIC",
		"hysteria://a@1.2.3.4:443":         "Hysteria 1",
		"wireguard://x@1.2.3.4:51820":      "不支持的节点链接类型",
		"just text":                        "无法识别",
		"ss://aes-256-cfb:pw@1.2.3.4:8388": "加密方式",
		"ss://YWVzLTI1Ni1nY206cGFzcw@1.2.3.4:8388?plugin=obfs-local":                                          "插件",
		"ss://2022-blake3-aes-256-gcm:" + ss2022Key + "@1.2.3.4:8388":                                         "密钥长度",
		"vless://" + uid + "@1.2.3.4:443?security=tls&allowInsecure=1":                                        "跳过证书验证",
		"vless://" + uid + "@1.2.3.4:443?security=reality&sni=a.example.com&pbk=bad":                          "公钥",
		"vless://" + uid + "@1.2.3.4:443?type=xhttp&security=tls":                                             "xhttp",
		"vless://" + uid + "@1.2.3.4:443?type=tcp&headerType=http":                                            "伪装头",
		"vless://" + uid + "@1.2.3.4:443?flow=xtls-rprx-vision":                                               "xtls-rprx-vision",
		"vless://" + uid + "@1.2.3.4:443?encryption=mlkem768x25519plus":                                       "Encryption",
		"vless://not-a-uuid@1.2.3.4:443":                                                                      "UUID",
		"vless://" + uid + "@1.2.3.4:443?type=grpc&security=tls":                                              "serviceName",
		"vless://" + uid + "@1.2.3.4:70000":                                                                   "端口",
		"vless://" + uid + "@1.2.3.4":                                                                         "端口",
		"vless://" + uid + "@1.2.3.4:443?type=ws&security=tls&path=no-slash":                                  "路径",
		"vless://" + uid + "@1.2.3.4:443?security=tls&fp=netscape":                                            "指纹",
		"vless://" + uid + "@1.2.3.4:443?security=tls&alpn=spdy":                                              "ALPN",
		"vless://" + uid + "@nodot:443":                                                                       "节点地址无效",
		vmessLink(`{"add":"1.2.3.4","port":443,"id":"` + uid + `","aid":64,"net":"tcp"}`):                     "alterId",
		vmessLink(`{"add":"1.2.3.4","port":443,"id":"` + uid + `","aid":0,"net":"kcp"}`):                      "kcp",
		vmessLink(`{"add":"1.2.3.4","port":443,"id":"` + uid + `","aid":0,"tls":"tls","allowInsecure":true}`): "跳过证书验证",
		"vmess://not-base64!!":                                             "VMess",
		"trojan://pass@1.2.3.4:443?security=none":                          "无 TLS",
		"trojan://@1.2.3.4:443":                                            "密码",
		"hysteria2://pw@1.2.3.4:443,5000-6000":                             "端口跳跃",
		"hysteria2://pw@1.2.3.4:443?mport=5000-6000":                       "端口跳跃",
		"hysteria2://pw@1.2.3.4:443?obfs=gecko":                            "salamander",
		"hysteria2://pw@1.2.3.4:443?insecure=1":                            "跳过证书验证",
		"hysteria2://pw@1.2.3.4:443?pinSHA256=12":                          "pinSHA256",
		"hysteria2://@1.2.3.4:443":                                         "认证密码",
		"socks5://1.2.3.4":                                                 "端口",
		"ss://" + strings.Repeat("a", MaxLinkLength):                       "过长",
		"vless://" + uid + "@1.2.3.4:443\nvless://" + uid + "@1.2.3.5:443": "一次只能",
	}
	for link, want := range cases {
		_, _, err := ParseLink(link)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.60q: err=%v, want it to mention %q", link, err, want)
		}
	}
}

func TestUpstreamHashIgnoresNameAndOrder(t *testing.T) {
	a, _, _ := ParseLink("trojan://pass@tj.example.com:443?sni=tj.example.com&alpn=h2#one")
	b, _, _ := ParseLink("trojan://pass@TJ.example.com:443?alpn=h2&sni=tj.example.com#two")
	c, _, _ := ParseLink("trojan://other@tj.example.com:443?sni=tj.example.com&alpn=h2")
	if a.Hash() != b.Hash() || a.Hash() == c.Hash() {
		t.Fatal("hash must identify the node, not the link text")
	}
	stored, err := FromJSON(a.JSON())
	if err != nil || stored.Hash() != a.Hash() {
		t.Fatal("stored upstream does not round-trip")
	}
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("nxdomain")
	}
	var out []netip.Addr
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func TestValidateAddress(t *testing.T) {
	res := fakeResolver{
		"good.example.com":   {"2606:4700::1", "1.1.1.1"},
		"rebind.example.com": {"1.1.1.1", "10.0.0.7"},
		"meta.example.com":   {"169.254.169.254"},
		"node.example.com":   {"5.6.7.8"},
	}
	forbidden := func(a netip.Addr) (string, bool) {
		if a.String() == "5.6.7.8" {
			return "FLVX 节点", true
		}
		if a.String() == "9.9.9.10" {
			return "面板服务器", true
		}
		return "", false
	}
	ok := map[string]string{"1.1.1.1": "1.1.1.1", "good.example.com": "1.1.1.1", "2606:4700::1111": "2606:4700::1111", "::ffff:8.8.8.8": "8.8.8.8"}
	for host, first := range ok {
		addrs, err := ValidateAddress(context.Background(), host, forbidden, res)
		if err != nil || len(addrs) == 0 || addrs[0].String() != first {
			t.Errorf("%s: %v %v", host, addrs, err)
		}
	}
	bad := map[string]string{
		"10.1.2.3": "内网", "127.0.0.1": "内网", "0.0.0.0": "内网", "169.254.169.254": "内网", "100.100.100.200": "内网",
		"192.168.1.1": "内网", "172.16.0.1": "内网", "224.0.0.1": "内网", "255.255.255.255": "内网", "198.18.0.1": "内网",
		"::1": "内网", "::": "内网", "fd00:ec2::254": "内网", "fe80::1": "内网", "ff02::1": "内网", "::ffff:10.0.0.1": "内网",
		"64:ff9b::a00:1": "内网", "2002:a00:1::1": "内网",
		"rebind.example.com": "解析到了内网", "meta.example.com": "解析到了内网", "missing.example.com": "无法解析",
		"node.example.com": "FLVX 节点", "9.9.9.10": "面板服务器", "printer.local": "内网域名", "localhost": "节点地址无效",
	}
	for host, want := range bad {
		if _, err := ValidateAddress(context.Background(), host, forbidden, res); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err=%v, want %q", host, err, want)
		}
	}
}
