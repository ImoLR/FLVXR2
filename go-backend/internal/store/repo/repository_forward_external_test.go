package repo

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
	"go-backend/internal/vlessenc"
)

// Plan 284: a 「我的外部节点」 forward's link = the node's own link with
// server:port set to the entry address; a domain node without SNI / ws Host
// gets them filled with that domain; everything else is kept.

const extTestUUID = "d77c8b43-40ed-42cd-9150-f3f47d0f534d"

func extTestLinks(t *testing.T) map[string]string {
	t.Helper()
	keys, err := vlessenc.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	key16 := base64.StdEncoding.EncodeToString(make([]byte, 16))
	vm := func(j string) string { return "vmess://" + base64.StdEncoding.EncodeToString([]byte(j)) }
	return map[string]string{
		"ss":            "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@198.51.100.10:8388#aead",
		"ss2022":        "ss://2022-blake3-aes-128-gcm:" + url.QueryEscape(key16) + "@ss.example.com:443#ss2022",
		"vless-reality": "vless://" + extTestUUID + "@vl.example.com:443?security=reality&sni=www.microsoft.com&pbk=Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws&sid=0a1b&fp=chrome&flow=xtls-rprx-vision&type=tcp#reality",
		"vless-tls-ws":  "vless://" + extTestUUID + "@ws.example.com:443?type=ws&security=tls&path=%2Fray&fp=chrome&alpn=h2,http/1.1#ws",
		"vless-ws-host": "vless://" + extTestUUID + "@ws.example.com:443?type=ws&security=tls&path=%2Fray&host=cdn.example.com&sni=cdn.example.com#ws-cdn",
		"vless-enc":     "vless://" + extTestUUID + "@enc.example.com:443?encryption=" + url.QueryEscape(vlessenc.Encryption("random", keys.PublicKey)) + "&flow=xtls-rprx-vision&security=none&type=tcp#enc",
		"vmess-tls-ws":  vm(`{"v":"2","ps":"vm","add":"vm.example.com","port":"443","id":"` + extTestUUID + `","aid":"0","scy":"auto","net":"ws","type":"none","path":"/vm","tls":"tls"}`),
		"vmess-plain":   vm(`{"add":"vm.example.com","port":10086,"id":"` + extTestUUID + `","aid":0,"net":"tcp","scy":"aes-128-gcm","tls":""}`),
		"trojan":        "trojan://pass@tj.example.com:443?alpn=h2#tj",
		"trojan-ip":     "trojan://pass@198.51.100.11:443?pinSHA256=" + strings.Repeat("ab", 32) + "#tj-ip",
		"hysteria2":     "hysteria2://auth@hy.example.com:443/?obfs=salamander&obfs-password=obfs#hy2",
		"socks5":        "socks5://user:pass@198.51.100.12:1080#socks",
		"http":          "http://u:p@198.51.100.13:3128#http",
		"https":         "https://u:p@proxy.example.com:443#https",
	}
}

func extTestNode(t *testing.T, link string) *ForwardExternal {
	t.Helper()
	up, _, err := linechain.ParseLink(link)
	if err != nil {
		t.Fatalf("parse %s: %v", link, err)
	}
	return &ForwardExternal{Node: model.LandingExternalNode{ID: 1, Name: "n"}, Up: up}
}

func TestExternalForwardLinkPerProtocol(t *testing.T) {
	links := extTestLinks(t)
	type want struct {
		sni, wsHost string // expected after rehost ("" = none)
		contains    []string
		absent      []string
	}
	cases := map[string]want{
		"ss":            {absent: []string{"sni=", "host="}},
		"ss2022":        {absent: []string{"sni=", "host="}},
		"vless-reality": {contains: []string{"security=reality", "sni=www.microsoft.com", "pbk=Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws", "sid=0a1b", "flow=xtls-rprx-vision"}, absent: []string{"sni=vl.example.com", "host="}},
		"vless-tls-ws":  {sni: "ws.example.com", wsHost: "ws.example.com", contains: []string{"sni=ws.example.com", "host=ws.example.com", "path=%2Fray", "alpn=h2%2Chttp%2F1.1", "fp=chrome"}},
		"vless-ws-host": {sni: "cdn.example.com", wsHost: "cdn.example.com", contains: []string{"sni=cdn.example.com", "host=cdn.example.com"}, absent: []string{"ws.example.com"}},
		"vless-enc":     {contains: []string{"encryption=mlkem768x25519plus", "flow=xtls-rprx-vision", "security=none"}, absent: []string{"sni=", "host="}},
		"vmess-tls-ws":  {sni: "vm.example.com", wsHost: "vm.example.com"},
		"vmess-plain":   {},
		"trojan":        {sni: "tj.example.com", contains: []string{"sni=tj.example.com", "alpn=h2", "pass@"}},
		"trojan-ip":     {contains: []string{"pinSHA256="}, absent: []string{"sni="}},
		"hysteria2":     {sni: "hy.example.com", contains: []string{"sni=hy.example.com", "obfs=salamander", "obfs-password=obfs"}},
		"socks5":        {absent: []string{"sni="}},
		"http":          {absent: []string{"sni="}},
		"https":         {sni: "proxy.example.com", contains: []string{"https://", "sni=proxy.example.com"}},
	}
	for _, entry := range []struct{ host, authority string }{{"203.0.113.5", "203.0.113.5:20001"}, {"2001:db8::7", "[2001:db8::7]:20001"}, {"[2001:db8::8]", "[2001:db8::8]:20001"}} {
		for name, w := range cases {
			x := extTestNode(t, links[name])
			node, ok := ExternalForwardNode(x, entry.host, 20001, "我的转发")
			if !ok {
				t.Fatalf("%s: no link", name)
			}
			uri := sublink.ShareURI(node)
			got, gotName, err := linechain.ParseLink(uri)
			if err != nil {
				t.Fatalf("%s: rewritten link does not parse: %v\n%s", name, err, uri)
			}
			if gotName != "我的转发" && !strings.HasPrefix(uri, "vmess://") {
				t.Fatalf("%s: name %q", name, gotName)
			}
			wantHost := strings.Trim(entry.host, "[]")
			if got.Server != wantHost || got.Port != 20001 {
				t.Fatalf("%s: server %s:%d, want %s", name, got.Server, got.Port, entry.authority)
			}
			if !strings.HasPrefix(uri, "vmess://") && !strings.Contains(uri, "@"+entry.authority) && !strings.Contains(uri, "//"+entry.authority) {
				t.Fatalf("%s: authority %s missing in %s", name, entry.authority, uri)
			}
			sni := ""
			if got.TLS != nil {
				sni = got.TLS.ServerName
			}
			if got.Reality != nil {
				sni = "" // Reality keeps its own server name (checked below)
			}
			if sni != w.sni {
				t.Fatalf("%s: sni %q, want %q (%s)", name, sni, w.sni, uri)
			}
			wsHost := ""
			if got.WS != nil {
				wsHost = got.WS.Host
			}
			if wsHost != w.wsHost {
				t.Fatalf("%s: ws host %q, want %q", name, wsHost, w.wsHost)
			}
			for _, s := range w.contains {
				if !strings.Contains(uri, s) {
					t.Fatalf("%s: %q missing in %s", name, s, uri)
				}
			}
			for _, s := range w.absent {
				if strings.Contains(uri, s) {
					t.Fatalf("%s: %q unexpected in %s", name, s, uri)
				}
			}
			// Everything but the address (and the filled SNI / Host) is kept.
			orig := x.Up
			cmp := got
			cmp.Server, cmp.Port, cmp.TLS, cmp.WS = orig.Server, orig.Port, orig.TLS, orig.WS
			if !reflect.DeepEqual(stripEmpty(cmp), stripEmpty(orig)) {
				a, _ := json.Marshal(cmp)
				b, _ := json.Marshal(orig)
				t.Fatalf("%s: fields changed\n got %s\nwant %s", name, a, b)
			}
			if orig.TLS != nil && got.TLS != nil {
				o, g := *orig.TLS, *got.TLS
				g.ServerName, o.ServerName = "", ""
				if !reflect.DeepEqual(o, g) {
					t.Fatalf("%s: TLS fields changed %+v vs %+v", name, g, o)
				}
			}
		}
	}
	// The stored upstream itself is never modified.
	x := extTestNode(t, links["trojan"])
	_, _ = ExternalForwardNode(x, "203.0.113.5", 1, "a")
	if x.Up.Server != "tj.example.com" || (x.Up.TLS != nil && x.Up.TLS.ServerName != "") {
		t.Fatalf("stored upstream changed: %+v", x.Up)
	}
}

func stripEmpty(u linechain.Upstream) linechain.Upstream {
	u.Tag = ""
	return u
}

func TestExternalForwardVMessJSON(t *testing.T) {
	x := extTestNode(t, extTestLinks(t)["vmess-tls-ws"])
	node, _ := ExternalForwardNode(x, "2001:db8::9", 30001, "vm")
	uri := sublink.ShareURI(node)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var j map[string]any
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	if j["add"] != "2001:db8::9" || j["port"] != "30001" || j["sni"] != "vm.example.com" || j["host"] != "vm.example.com" || j["tls"] != "tls" || j["path"] != "/vm" || j["ps"] != "vm" {
		t.Fatalf("vmess json %v", j)
	}
}

func TestExternalFilterFeatures(t *testing.T) {
	links := extTestLinks(t)
	want := map[string]string{
		"ss": "other", "ss2022": "other", "vless-reality": "tls", "vless-tls-ws": "tls", "vless-enc": "other",
		"vmess-tls-ws": "tls", "vmess-plain": "other", "trojan": "tls", "hysteria2": "quic", "socks5": "socks",
		"http": "http", "https": "tls",
	}
	for name, f := range want {
		if got := extTestNode(t, links[name]).Up.FilterFeature(); got != f {
			t.Fatalf("%s: feature %q, want %q", name, got, f)
		}
	}
	plainWS, _, _ := linechain.ParseLink("vless://" + extTestUUID + "@198.51.100.20:80?type=ws&path=%2F&security=none#pws")
	if plainWS.FilterFeature() != "http" {
		t.Fatalf("plain ws = %q", plainWS.FilterFeature())
	}
	// 专线 (tls+http+socks): only SS / VLESS Encryption / plain VMess pass.
	for name := range want {
		up := extTestNode(t, links[name]).Up
		msg := ExternalFilterBlockedMsg(1, 1, 1, 0, up)
		pass := name == "ss" || name == "ss2022" || name == "vless-enc" || name == "vmess-plain"
		if (msg == "") != pass {
			t.Fatalf("%s on 专线: %q", name, msg)
		}
	}
	if m := ExternalFilterBlockedMsg(1, 0, 0, 0, extTestNode(t, links["hysteria2"]).Up); !strings.Contains(m, "QUIC") {
		t.Fatalf("hy2 msg %q", m)
	}
	if m := ExternalFilterBlockedMsg(0, 0, 0, 1, extTestNode(t, links["ss"]).Up); !strings.Contains(m, "其它协议") {
		t.Fatalf("block other %q", m)
	}
	if m := ExternalFilterBlockedMsg(0, 0, 0, 0, extTestNode(t, links["trojan"]).Up); m != "" {
		t.Fatalf("open tunnel blocks %q", m)
	}
}

func TestForwardTargetKey(t *testing.T) {
	for in, want := range map[string]string{
		"1.2.3.4:443":           "1.2.3.4:443",
		" Node.Example.COM.:80": "node.example.com:80",
		"[2001:DB8::1]:443":     "[2001:db8::1]:443",
		"[::ffff:1.2.3.4]:5":    "1.2.3.4:5",
		"1.2.3.4:443,5.6.7.8:1": "",
		"1.2.3.4":               "",
		"1.2.3.4:0":             "",
	} {
		if got := forwardTargetKey(in); got != want {
			t.Fatalf("%q → %q, want %q", in, got, want)
		}
	}
}
