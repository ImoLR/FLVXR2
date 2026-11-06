package sublink

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"

	"gopkg.in/yaml.v3"
)

func mustParse(t *testing.T, link string) linechain.Upstream {
	t.Helper()
	up, _, err := linechain.ParseLink(link)
	if err != nil {
		t.Fatalf("parse %s: %v", link, err)
	}
	return up
}

func selfSigned(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "anytls.example.com"}, DNSNames: []string{"anytls.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

const realityPBK = "Y5u4vH0Qp6yG9mKZr3QZbqSx2n8Hk8TtZl1u8rH0v0E"

// sampleNodes covers every protocol a subscription can carry.
func sampleNodes(t *testing.T) []Node {
	vlessURI := "vless://8a4f3f6e-1c2b-4d5e-9f00-112233445566@203.0.113.21:20008?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=" + realityPBK + "&sid=6ba85179e30d4fc2&type=tcp#landing"
	ss22 := "ss://2022-blake3-aes-128-gcm:" + strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")), "=", "%3D") + "%3A" + strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte("fedcba9876543210")), "=", "%3D") + "@198.51.100.7:20009#ss22"
	nodes := []Node{
		{Name: "沪日440 · 我的落地·东京", Up: mustParse(t, vlessURI), URI: vlessURI},
		{Name: "沪日440 · 游戏加速", Up: mustParse(t, ss22), URI: ss22},
		{Name: "东京 AnyTLS", Extra: &Extra{Type: "anytls", Server: "anytls.example.com", Port: 8443, Password: "p@ss:w/rd", SNI: "anytls.example.com", CertSHA256: strings.Repeat("ab", 32), CertPEM: selfSigned(t), UDP: true},
			URI: "anytls://p%40ss%3Aw%2Frd@anytls.example.com:8443/?insecure=1&sni=anytls.example.com#x"},
		{Name: "大阪 Snell", Extra: &Extra{Type: "snell", Server: "203.0.113.30", Port: 9000, Password: "snellpsk", Version: 4, Obfs: "http", ObfsHost: "bing.com", UDP: true}},
		{Name: "首尔 Mieru", Extra: &Extra{Type: "mieru", Server: "203.0.113.31", Port: 9100, Username: "u", Password: "pw", Transport: "TCP"},
			URI: "mierus://u:pw@203.0.113.31?multiplexing=MULTIPLEXING_LOW&port=9100&profile=x&protocol=TCP#x"},
		{Name: "🇭🇰 香港 01 · IPLC", Up: mustParse(t, "trojan://secret@hk1.provider.example:443?sni=hk1.provider.example&type=grpc&serviceName=gun#a")},
		{Name: "🇯🇵 日本 02 · 大阪", Up: mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"jp","add":"jp2.provider.example","port":"443","id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":"0","scy":"auto","net":"ws","type":"none","host":"jp2.provider.example","path":"/ray","tls":"tls","sni":"jp2.provider.example"}`)))},
		{Name: "新加坡 Hy2", Up: mustParse(t, "hysteria2://pass@sg.provider.example:8443/?sni=sg.provider.example&obfs=salamander&obfs-password=ob"), Insecure: true},
		{Name: "我家宽带 Socks", Up: mustParse(t, "socks5://user:pw@home.example.net:1080")},
		{Name: "HTTPS 代理", Up: mustParse(t, "https://u:p@proxy.example.net:8443?sni=proxy.example.net")},
		{Name: "老 SS", Up: mustParse(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pw123"))+"@203.0.113.40:8388")},
		{Name: "VLESS WS TLS", Up: mustParse(t, "vless://b831381d-6324-4d53-ad4f-8cda48b30811@ws.provider.example:443?security=tls&sni=ws.provider.example&type=ws&path=%2Fws&host=ws.provider.example&fp=chrome")},
		{Name: "重名", Up: mustParse(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:a"))+"@203.0.113.41:1")},
		{Name: "重名", Up: mustParse(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:b"))+"@203.0.113.42:2")},
		{Name: "", Up: mustParse(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:c"))+"@203.0.113.43:3")},
		{Name: "节点选择", Up: mustParse(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:d"))+"@203.0.113.44:4")},
	}
	FinalizeNames(nodes)
	return nodes
}

func TestFinalizeNames(t *testing.T) {
	nodes := sampleNodes(t)
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Name == "" || seen[n.Name] {
			t.Fatalf("name %q empty or duplicate", n.Name)
		}
		seen[n.Name] = true
	}
	if !seen["重名 ①"] || !seen["重名 ②"] || !seen["SS 203.0.113.43"] && !seen["Shadowsocks 203.0.113.43"] || !seen["节点选择 · 节点"] {
		t.Fatalf("names: %v", seen)
	}
}

func TestDetectClient(t *testing.T) {
	cases := map[string][2]string{
		"clash-verge/v2.2.3":                                 {FormatClash, "Clash Verge"},
		"ClashMetaForAndroid/2.11.1.Meta":                    {FormatClash, "Clash Meta"},
		"mihomo/1.19.0":                                      {FormatClash, "mihomo"},
		"Stash/2.5.0 Clash/1.9.0":                            {FormatClash, "Stash"},
		"Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0": {FormatBase64, "Shadowrocket"},
		"v2rayNG/1.8.5":                                      {FormatBase64, "v2rayNG"},
		"v2rayN/6.42":                                        {FormatBase64, "v2rayN"},
		"V2Box/1.0":                                          {FormatBase64, "V2Box"},
		"SFA/1.12.0 (Android 14; sing-box 1.12.0)":           {FormatSingBox, "sing-box"},
		"SFI/1.11.4 (Build 1; sing-box 1.11.4; language zh_CN)": {FormatSingBox, "sing-box"},
		"sing-box 1.14.4": {FormatSingBox, "sing-box"},
		"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box": {FormatSingBox, "Hiddify"},
		"Quantumult%20X/1.0.30 (iPhone14,5; iOS 16.4)":              {FormatQX, "Quantumult X"},
		"Loon/3.1.3":     {FormatLoon, "Loon"},
		"Surge Mac/2565": {FormatSurge, "Surge"},
		"Egern/1.0":      {FormatClash, "Egern"},
		"curl/8.0":       {FormatClash, "其它客户端"},
		"":               {FormatClash, "其它客户端"},
	}
	for ua, want := range cases {
		c := DetectClient(ua)
		if c.Format != want[0] || c.Label != want[1] || c.Browser {
			t.Errorf("%q → %+v, want %v", ua, c, want)
		}
	}
	if c := DetectClient("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Safari/604.1"); !c.Browser {
		t.Errorf("browser not detected: %+v", c)
	}
	if c := DetectClient("Mozilla/5.0 clash-verge"); c.Browser || c.Label != "Clash Verge" {
		t.Errorf("client marker must win over Mozilla: %+v", c)
	}
	if !DetectClient("Stash/2.5.0 Clash/1.9.0").Stash {
		t.Error("stash flavour")
	}
}

func TestClashStructure(t *testing.T) {
	nodes := sampleNodes(t)
	out, n := Clash(nodes, ModeCNDirect, false)
	if n != len(nodes) {
		t.Fatalf("clash nodes %d of %d", n, len(nodes))
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
	proxies := doc["proxies"].([]interface{})
	types := map[string]bool{}
	for _, p := range proxies {
		types[p.(map[string]interface{})["type"].(string)] = true
	}
	for _, want := range []string{"vless", "ss", "anytls", "snell", "mieru", "trojan", "vmess", "hysteria2", "socks5", "http"} {
		if !types[want] {
			t.Errorf("missing proxy type %s", want)
		}
	}
	groups := doc["proxy-groups"].([]interface{})
	if len(groups) != 2 || groups[0].(map[string]interface{})["name"] != GroupSelect || groups[1].(map[string]interface{})["type"] != "url-test" {
		t.Fatalf("groups: %v", groups)
	}
	rules := doc["rules"].([]interface{})
	if rules[len(rules)-1] != "MATCH,节点选择" || !strings.Contains(string(out), "GEOSITE,cn,DIRECT") {
		t.Fatalf("rules: %v", rules)
	}
	if !strings.Contains(string(out), "skip-cert-verify: true") {
		t.Fatal("insecure hy2 lost its flag")
	}
	global, _ := Clash(nodes, ModeGlobal, false)
	if strings.Contains(string(global), "GEOSITE") || strings.Contains(string(global), "GEOIP") {
		t.Fatal("global mode must not need geo data")
	}
	stash, sn := Clash(nodes, ModeCNDirect, true)
	if sn != len(nodes)-2 || strings.Contains(string(stash), "type: anytls") || strings.Contains(string(stash), "type: mieru") {
		t.Fatalf("stash should skip anytls + mieru: %d", sn)
	}
	empty, en := Clash(nil, ModeCNDirect, false)
	if en != 0 || !strings.Contains(string(empty), "DIRECT") || strings.Contains(string(empty), GroupAuto) {
		t.Fatalf("empty clash:\n%s", empty)
	}
}

func TestSingBoxStructure(t *testing.T) {
	nodes := sampleNodes(t)
	out, n := SingBox(nodes, ModeCNDirect, testOrigin)
	// snell + mieru are not in sing-box.
	if n != len(nodes)-2 {
		t.Fatalf("sing-box nodes %d", n)
	}
	var doc struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
		Route     map[string]interface{}   `json:"route"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("json: %v", err)
	}
	if doc.Outbounds[0]["tag"] != GroupSelect || doc.Outbounds[1]["type"] != "urltest" || doc.Outbounds[len(doc.Outbounds)-1]["type"] != "direct" {
		t.Fatalf("outbounds head/tail wrong")
	}
	if doc.Route["final"] != GroupSelect || doc.Route["rule_set"] == nil {
		t.Fatalf("route: %v", doc.Route)
	}
	global, _ := SingBox(nodes, ModeGlobal, testOrigin)
	if strings.Contains(string(global), "rule_set") {
		t.Fatal("global mode must not use rule sets")
	}
}

func TestBase64Links(t *testing.T) {
	nodes := sampleNodes(t)
	out, n := Base64(nodes)
	// snell has no link format.
	if n != len(nodes)-1 {
		t.Fatalf("base64 nodes %d", n)
	}
	raw, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	parsed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "anytls://") || strings.HasPrefix(l, "mierus://") {
			continue
		}
		up, name, err := linechain.ParseLink(l)
		if err != nil {
			// The insecure hy2 link is valid for clients, not for landing.
			if strings.HasPrefix(l, "hysteria2://") && strings.Contains(l, "insecure=1") {
				continue
			}
			t.Fatalf("link does not round-trip: %s: %v", l, err)
		}
		if name == "" || up.Server == "" {
			t.Fatalf("link lost name/server: %s", l)
		}
		parsed++
	}
	if !strings.Contains(string(raw), "#"+url.PathEscape(nodes[0].Name)) {
		t.Fatalf("landing link keeps the final name: %s", lines[0])
	}
	if parsed < 10 {
		t.Fatalf("only %d links parsed", parsed)
	}
}

func TestRefs(t *testing.T) {
	refs, err := CleanRefs([]Ref{
		{Kind: RefForwardLink, ForwardID: 1, Entry: "3|1.2.3.4"},
		{Kind: RefForwardLink, ForwardID: 1, Entry: "3|1.2.3.4"},
		{Kind: RefSourceAll, SourceID: 2},
		{Kind: RefSourceNode, SourceID: 2, Name: "a"},
	})
	if err != nil || len(refs) != 3 {
		t.Fatalf("clean: %v %v", refs, err)
	}
	if _, err := CleanRefs([]Ref{{Kind: "bogus"}}); err == nil {
		t.Fatal("bogus kind accepted")
	}
	if _, err := CleanRefs([]Ref{{Kind: RefExternalNode}}); err == nil {
		t.Fatal("empty id accepted")
	}
	back := ParseRefs(RefsJSON(refs))
	if len(back) != 3 || back[0].Entry != "3|1.2.3.4" {
		t.Fatalf("round trip: %v", back)
	}
}

func TestToken(t *testing.T) {
	a, _ := NewToken()
	b, _ := NewToken()
	if a == b || len(a) != TokenLength || !ValidToken(a) {
		t.Fatalf("tokens %q %q", a, b)
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), strings.Repeat("a", 21) + "/", "../../etc/passwdaaaaaaaaaa"} {
		if ValidToken(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// TestRealClients runs the real mihomo / sing-box checkers when their
// binaries are given (SUBLINK_MIHOMO / SUBLINK_SINGBOX); CI skips it.
func TestRealClients(t *testing.T) {
	nodes := sampleNodes(t)
	dir := t.TempDir()
	if bin := os.Getenv("SUBLINK_MIHOMO"); bin != "" {
		home := os.Getenv("SUBLINK_MIHOMO_HOME")
		if home == "" {
			home = dir
		}
		for _, mode := range []string{ModeCNDirect, ModeGlobal} {
			out, _ := Clash(nodes, mode, false)
			f := filepath.Join(dir, "clash-"+mode+".yaml")
			_ = os.WriteFile(f, out, 0o600)
			res, err := exec.Command(bin, "-t", "-d", home, "-f", f).CombinedOutput()
			if err != nil {
				t.Fatalf("mihomo -t %s: %v\n%s", mode, err, res)
			}
			t.Logf("mihomo %s: %s", mode, strings.TrimSpace(lastLine(string(res))))
		}
	}
	if bin := os.Getenv("SUBLINK_SINGBOX"); bin != "" {
		for _, mode := range []string{ModeCNDirect, ModeGlobal} {
			out, _ := SingBox(nodes, mode, testOrigin)
			f := filepath.Join(dir, "sb-"+mode+".json")
			_ = os.WriteFile(f, out, 0o600)
			res, err := exec.Command(bin, "check", "-c", f).CombinedOutput()
			if err != nil {
				t.Fatalf("sing-box check %s: %v\n%s", mode, err, res)
			}
			t.Logf("sing-box check %s: ok %s", mode, strings.TrimSpace(string(res)))
		}
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
