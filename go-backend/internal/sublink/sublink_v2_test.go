package sublink

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Plan 191: Surge / Surfboard / Loon / Quantumult X.

// matrixNodes: one node per protocol variant FLVX can put in a
// subscription, keyed by a short label (same order as the support matrix).
func matrixNodes(t *testing.T) ([]string, map[string]Node) {
	t.Helper()
	k16 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	k32 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	esc := func(s string) string { return strings.ReplaceAll(s, "=", "%3D") }
	vmess := func(net, tls string) string {
		return "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"x","add":"vm.example.com","port":"443","id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":"0","scy":"auto","net":"`+net+`","type":"none","host":"cdn.example.com","path":"/ray","tls":"`+tls+`","sni":"vm.example.com"}`))
	}
	uuid := "8a4f3f6e-1c2b-4d5e-9f00-112233445566"
	rows := []struct {
		key  string
		node Node
	}{
		{"ss", Node{Up: mustParse(t, "ss://"+b64url("aes-256-gcm:pw123")+"@203.0.113.40:8388")}},
		{"ss2022", Node{Up: mustParse(t, "ss://2022-blake3-aes-128-gcm:"+esc(k16)+"@198.51.100.7:20009")}},
		{"ss2022-chacha", Node{Up: mustParse(t, "ss://2022-blake3-chacha20-poly1305:"+esc(k32)+"@198.51.100.8:20010")}},
		{"vless-reality-vision", Node{Up: mustParse(t, "vless://"+uuid+"@203.0.113.21:20008?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk="+realityPBK+"&sid=6ba85179e30d4fc2&type=tcp")}},
		{"vless-reality-grpc", Node{Up: mustParse(t, "vless://"+uuid+"@203.0.113.22:20008?encryption=none&security=reality&sni=www.microsoft.com&pbk="+realityPBK+"&sid=6ba8&type=grpc&serviceName=gun")}},
		{"vless-ws-tls", Node{Up: mustParse(t, "vless://"+uuid+"@ws.example.com:443?security=tls&sni=ws.example.com&type=ws&path=%2Fws&host=cdn.example.com")}},
		{"vless-tcp", Node{Up: mustParse(t, "vless://"+uuid+"@203.0.113.23:8080?security=none&type=tcp")}},
		{"vmess-ws-tls", Node{Up: mustParse(t, vmess("ws", "tls"))}},
		{"vmess-tcp", Node{Up: mustParse(t, vmess("tcp", ""))}},
		{"vmess-grpc", Node{Up: mustParse(t, vmess("grpc", "tls"))}},
		{"trojan", Node{Up: mustParse(t, "trojan://secret@tj.example.com:443?sni=tj.example.com")}},
		{"trojan-ws", Node{Up: mustParse(t, "trojan://secret@tj.example.com:443?sni=tj.example.com&type=ws&path=%2Ftj&host=cdn.example.com")}},
		{"trojan-grpc", Node{Up: mustParse(t, "trojan://secret@tj.example.com:443?sni=tj.example.com&type=grpc&serviceName=gun")}},
		{"hysteria2", Node{Up: mustParse(t, "hysteria2://pass@hy.example.com:8443/?sni=hy.example.com")}},
		{"hysteria2-obfs", Node{Up: mustParse(t, "hysteria2://pass@hy.example.com:8444/?sni=hy.example.com&obfs=salamander&obfs-password=ob"), Insecure: true}},
		{"socks5", Node{Up: mustParse(t, "socks5://user:pw@home.example.net:1080")}},
		{"http", Node{Up: mustParse(t, "http://u:p@proxy.example.net:8080")}},
		{"https", Node{Up: mustParse(t, "https://u:p@proxy.example.net:8443?sni=proxy.example.net")}},
		{"anytls", Node{Extra: &Extra{Type: "anytls", Server: "anytls.example.com", Port: 8443, Password: "p@ss:w/rd", SNI: "anytls.example.com", CertSHA256: strings.Repeat("ab", 32), UDP: true}}},
		{"snell", Node{Extra: &Extra{Type: "snell", Server: "203.0.113.30", Port: 9000, Password: "snellpsk", Version: 4, Obfs: "http", ObfsHost: "bing.com", UDP: true}}},
		{"snell-tls-obfs", Node{Extra: &Extra{Type: "snell", Server: "203.0.113.31", Port: 9001, Password: "snellpsk", Version: 5, Obfs: "tls", ObfsHost: "bing.com"}}},
		{"mieru", Node{Extra: &Extra{Type: "mieru", Server: "203.0.113.32", Port: 9100, Username: "u", Password: "pw", Transport: "TCP"}}},
	}
	keys := make([]string, 0, len(rows))
	m := map[string]Node{}
	for _, r := range rows {
		r.node.Name = r.key
		keys = append(keys, r.key)
		m[r.key] = r.node
	}
	return keys, m
}

// supportMatrix: protocol → supported by Surge, Surfboard, Loon, QX. This is
// the matrix in plans/191-sub-link-v2.md.
var supportMatrix = map[string][4]bool{
	"ss":                   {true, true, true, true},
	"ss2022":               {true, true, true, true},
	"ss2022-chacha":        {false, false, false, false},
	"vless-reality-vision": {false, false, true, true},
	"vless-reality-grpc":   {false, false, false, false},
	"vless-ws-tls":         {false, false, true, true},
	"vless-tcp":            {false, false, true, true},
	"vmess-ws-tls":         {true, true, true, true},
	"vmess-tcp":            {true, true, true, true},
	"vmess-grpc":           {false, false, false, false},
	"trojan":               {true, true, true, true},
	"trojan-ws":            {true, true, true, true},
	"trojan-grpc":          {false, false, false, false},
	"hysteria2":            {true, true, true, false},
	"hysteria2-obfs":       {false, true, true, false},
	"socks5":               {true, true, true, true},
	"http":                 {true, true, true, true},
	"https":                {true, true, true, true},
	"anytls":               {true, true, true, true},
	"snell":                {true, true, false, false},
	"snell-tls-obfs":       {false, true, false, false},
	"mieru":                {false, false, false, false},
}

func TestSupportMatrix(t *testing.T) {
	keys, nodes := matrixNodes(t)
	if len(keys) != len(supportMatrix) {
		t.Fatalf("matrix rows %d, nodes %d", len(supportMatrix), len(keys))
	}
	for _, k := range keys {
		n := nodes[k]
		want := supportMatrix[k]
		_, s := surgeProxy(n, false)
		_, sb := surgeProxy(n, true)
		_, l := loonProxy(n)
		_, q := qxProxy(n)
		if got := [4]bool{s, sb, l, q}; got != want {
			t.Errorf("%s: surge/surfboard/loon/qx = %v, want %v", k, got, want)
		}
	}
}

// proxyLines returns the [Proxy] section lines of a Surge profile.
func surgeSection(profile, section string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(profile, "\n") {
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			in = l == "["+section+"]"
			continue
		}
		if in && strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// splitLine parses "Name = a, b, k=v" into the name, positional values and
// parameters (a test-side parser of the Surge / Loon syntax).
func splitLine(t *testing.T, l, sep string) (string, []string, map[string]string) {
	t.Helper()
	name, rest, ok := strings.Cut(l, " = ")
	if !ok {
		t.Fatalf("no ' = ' in %q", l)
	}
	var pos []string
	kv := map[string]string{}
	var parts []string
	cur, inQ := "", false
	for _, r := range rest {
		switch {
		case r == '"':
			inQ = !inQ
			cur += string(r)
		case !inQ && strings.HasPrefix(sep, string(r)):
			parts = append(parts, strings.TrimSpace(cur))
			cur = ""
		default:
			cur += string(r)
		}
	}
	parts = append(parts, strings.TrimSpace(cur))
	for _, p := range parts {
		if k, v, ok := strings.Cut(p, "="); ok && !strings.HasPrefix(p, `"`) {
			kv[k] = strings.Trim(v, `"`)
		} else {
			pos = append(pos, strings.Trim(p, `"`))
		}
	}
	return name, pos, kv
}

func TestSurgeProfile(t *testing.T) {
	keys, m := matrixNodes(t)
	nodes := make([]Node, 0, len(keys))
	for _, k := range keys {
		nodes = append(nodes, m[k])
	}
	for _, sb := range []bool{false, true} {
		for _, mode := range []string{ModeCNDirect, ModeGlobal} {
			body, count := Surge(nodes, mode, sb)
			s := string(body)
			want := 0
			for _, k := range keys {
				if supportMatrix[k][map[bool]int{false: 0, true: 1}[sb]] {
					want++
				}
			}
			if count != want {
				t.Fatalf("sb=%v %s: %d nodes, want %d", sb, mode, count, want)
			}
			proxies := surgeSection(s, "Proxy")
			if len(proxies) != count {
				t.Fatalf("sb=%v: %d proxy lines for %d nodes\n%s", sb, len(proxies), count, s)
			}
			names := []string{}
			for _, l := range proxies {
				name, pos, kv := splitLine(t, l, ",")
				names = append(names, name)
				if len(pos) < 3 {
					t.Fatalf("short line %q", l)
				}
				switch name {
				case "ss2022":
					if pos[0] != "ss" || kv["encrypt-method"] != "2022-blake3-aes-128-gcm" || kv["password"] == "" || kv["udp-relay"] != "true" {
						t.Errorf("ss2022: %q", l)
					}
				case "vmess-ws-tls":
					if kv["ws"] != "true" || kv["ws-path"] != "/ray" || kv["ws-headers"] != "Host:cdn.example.com" || kv["tls"] != "true" || kv["sni"] != "vm.example.com" || kv["vmess-aead"] != "true" {
						t.Errorf("vmess: %q", l)
					}
				case "trojan-ws":
					if kv["ws"] != "true" || kv["ws-path"] != "/tj" || kv["sni"] != "tj.example.com" {
						t.Errorf("trojan-ws: %q", l)
					}
				case "anytls":
					if pos[0] != "anytls" || kv["password"] != "p@ss:w/rd" || kv["server-cert-fingerprint-sha256"] != strings.Repeat("ab", 32) || kv["skip-cert-verify"] != "" {
						t.Errorf("anytls: %q", l)
					}
				case "snell":
					if kv["psk"] != "snellpsk" || kv["version"] != "4" || kv["obfs"] != "http" || kv["obfs-host"] != "bing.com" {
						t.Errorf("snell: %q", l)
					}
				case "hysteria2-obfs": // Surfboard only
					if kv["salamander-password"] != "ob" || kv["skip-cert-verify"] != "true" {
						t.Errorf("hy2 obfs: %q", l)
					}
				case "https":
					if pos[0] != "https" {
						t.Errorf("https: %q", l)
					}
					if sb && (len(pos) != 5 || pos[3] != "u" || pos[4] != "p") || !sb && (kv["username"] != "u" || kv["password"] != "p") {
						t.Errorf("https credentials (sb=%v): %q", sb, l)
					}
				}
				if sb && (name == "vmess-tcp" || name == "trojan") && kv["udp-relay"] != "true" {
					t.Errorf("surfboard udp-relay missing: %q", l)
				}
				if !sb && name == "trojan" && kv["udp-relay"] != "" {
					t.Errorf("surge trojan needs no udp-relay: %q", l)
				}
			}
			groups := surgeSection(s, "Proxy Group")
			if len(groups) != 2 || groups[0] != GroupSelect+" = select, "+GroupAuto+", "+strings.Join(names, ", ")+", DIRECT" ||
				groups[1] != GroupAuto+" = url-test, "+strings.Join(names, ", ")+", url="+TestURL+", interval=300, tolerance=50" {
				t.Fatalf("groups: %q", groups)
			}
			rules := surgeSection(s, "Rule")
			last := rules[len(rules)-1]
			if sb && last != "FINAL,"+GroupSelect || !sb && last != "FINAL,"+GroupSelect+",dns-failed" {
				t.Errorf("final rule %q", last)
			}
			if hasGeo := strings.Contains(s, "\nGEOIP,CN,DIRECT\n"); hasGeo != (mode == ModeCNDirect) {
				t.Errorf("mode %s GEOIP=%v", mode, hasGeo)
			}
			if strings.Contains(s, "no-resolve") == sb || strings.Contains(s, "skip-proxy") == sb || strings.Contains(s, "loglevel") == sb {
				t.Errorf("surfboard=%v general / no-resolve mismatch", sb)
			}
			if !strings.Contains(s, "IP-CIDR,192.168.0.0/16,DIRECT") || !strings.Contains(s, "proxy-test-url = "+TestURL) ||
				!strings.Contains(s, "dns-server = system, 223.5.5.5, 119.29.29.29") || strings.Contains(s, "RULE-SET") {
				t.Errorf("profile basics missing:\n%s", s)
			}
			if strings.Contains(s, "MANAGED-CONFIG") {
				t.Error("managed line belongs to the handler")
			}
		}
	}
	if l := SurgeManagedLine("https://p.example.com/s/abc?t=surge"); l != "#!MANAGED-CONFIG https://p.example.com/s/abc?t=surge interval=43200 strict=false\n" {
		t.Errorf("managed line %q", l)
	}
}

func TestEmptyOutputs(t *testing.T) {
	only := []Node{{Name: "m", Extra: &Extra{Type: "mieru", Server: "203.0.113.32", Port: 1, Username: "u", Password: "p"}}}
	for _, nodes := range [][]Node{nil, only} {
		for _, sb := range []bool{false, true} {
			body, n := Surge(nodes, ModeCNDirect, sb)
			s := string(body)
			if n != 0 || len(surgeSection(s, "Proxy")) != 0 || strings.Join(surgeSection(s, "Proxy Group"), "\n") != GroupSelect+" = select, DIRECT" ||
				!strings.Contains(s, "FINAL,"+GroupSelect) {
				t.Errorf("empty surge (sb=%v):\n%s", sb, s)
			}
		}
		if b, n := Loon(nodes); n != 0 || string(b) != "# FLVX: 这条订阅里没有 Loon 支持的节点\n" {
			t.Errorf("empty loon %q", b)
		}
		if b, n := QX(nodes); n != 0 || !strings.HasPrefix(string(b), "#") || strings.Count(string(b), "\n") != 1 {
			t.Errorf("empty qx %q", b)
		}
	}
}

func TestLoonLines(t *testing.T) {
	keys, m := matrixNodes(t)
	nodes := make([]Node, 0, len(keys))
	for _, k := range keys {
		nodes = append(nodes, m[k])
	}
	body, n := Loon(nodes)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("%d lines for %d nodes", len(lines), n)
	}
	for _, l := range lines {
		name, pos, kv := splitLine(t, l, ",")
		switch name {
		case "vless-reality-vision":
			if pos[0] != "VLESS" || pos[3] != "8a4f3f6e-1c2b-4d5e-9f00-112233445566" || kv["transport"] != "tcp" || kv["flow"] != "xtls-rprx-vision" ||
				kv["public-key"] != realityPBK || kv["short-id"] != "6ba85179e30d4fc2" || kv["over-tls"] != "true" || kv["sni"] != "www.microsoft.com" {
				t.Errorf("reality: %q", l)
			}
		case "vmess-ws-tls":
			if pos[0] != "VMess" || pos[3] != "auto" || kv["transport"] != "ws" || kv["path"] != "/ray" || kv["host"] != "cdn.example.com" || kv["over-tls"] != "true" || kv["alterId"] != "0" {
				t.Errorf("vmess: %q", l)
			}
		case "ss2022":
			if pos[0] != "Shadowsocks" || pos[3] != "2022-blake3-aes-128-gcm" || !strings.Contains(l, `,"`) || kv["udp"] != "true" {
				t.Errorf("ss: %q", l)
			}
		case "hysteria2-obfs":
			if pos[0] != "Hysteria2" || kv["salamander-password"] != "ob" || kv["skip-cert-verify"] != "true" {
				t.Errorf("hy2: %q", l)
			}
		case "anytls":
			if pos[0] != "AnyTLS" || pos[3] != "p@ss:w/rd" || kv["tls-cert-sha256"] != strings.Repeat("ab", 32) {
				t.Errorf("anytls: %q", l)
			}
		case "https":
			if pos[0] != "https" || pos[3] != "u" || pos[4] != "p" || kv["sni"] != "proxy.example.net" {
				t.Errorf("https: %q", l)
			}
		}
	}
}

func TestQXLines(t *testing.T) {
	keys, m := matrixNodes(t)
	nodes := make([]Node, 0, len(keys))
	for _, k := range keys {
		nodes = append(nodes, m[k])
	}
	body, n := QX(nodes)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("%d lines for %d nodes", len(lines), n)
	}
	for _, l := range lines {
		kv := map[string]string{}
		parts := strings.Split(l, ", ")
		typ, addr, _ := strings.Cut(parts[0], "=")
		for _, p := range parts[1:] {
			k, v, _ := strings.Cut(p, "=")
			kv[k] = v
		}
		switch kv["tag"] {
		case "vless-reality-vision":
			if typ != "vless" || addr != "203.0.113.21:20008" || kv["method"] != "none" || kv["obfs"] != "over-tls" || kv["obfs-host"] != "www.microsoft.com" ||
				kv["reality-base64-pubkey"] != realityPBK || kv["reality-hex-shortid"] != "6ba85179e30d4fc2" || kv["vless-flow"] != "xtls-rprx-vision" {
				t.Errorf("reality: %q", l)
			}
		case "vmess-ws-tls":
			if typ != "vmess" || kv["method"] != "chacha20-poly1305" || kv["obfs"] != "wss" || kv["obfs-host"] != "cdn.example.com" || kv["obfs-uri"] != "/ray" || kv["tls-verification"] != "true" {
				t.Errorf("vmess: %q", l)
			}
		case "trojan":
			if typ != "trojan" || kv["over-tls"] != "true" || kv["tls-host"] != "tj.example.com" || kv["tls-verification"] != "true" {
				t.Errorf("trojan: %q", l)
			}
		case "trojan-ws":
			if kv["obfs"] != "wss" || kv["over-tls"] != "" || kv["obfs-uri"] != "/tj" {
				t.Errorf("trojan ws: %q", l)
			}
		case "anytls":
			if typ != "anytls" || kv["password"] != "p@ss:w/rd" || kv["over-tls"] != "true" || kv["tls-cert-sha256"] != strings.Repeat("ab", 32) {
				t.Errorf("anytls: %q", l)
			}
		case "ss2022":
			if typ != "shadowsocks" || kv["method"] != "2022-blake3-aes-128-gcm" || !strings.HasSuffix(kv["password"], "==") && !strings.Contains(l, "password=") {
				t.Errorf("ss: %q", l)
			}
		case "":
			t.Errorf("no tag: %q", l)
		}
	}
}

func TestLineNamesEscaping(t *testing.T) {
	nodes := []Node{
		{Name: "🇺🇸 美国, 洛杉矶 = 1", Up: mustParse(t, "trojan://pw@us.example.com:443?sni=us.example.com")},
		{Name: "🇺🇸 美国， 洛杉矶 ＝ 1", Up: mustParse(t, "trojan://pw@us2.example.com:443?sni=us2.example.com")},
		{Name: "HK #2 ; a // b \"q\"", Up: mustParse(t, "trojan://pw@hk.example.com:443")},
		{Name: "direct", Up: mustParse(t, "trojan://pw@d.example.com:443")},
	}
	FinalizeNames(nodes)
	surge, _ := Surge(nodes, ModeGlobal, false)
	got := []string{}
	for _, l := range surgeSection(string(surge), "Proxy") {
		name, _, _ := strings.Cut(l, " = ")
		got = append(got, name)
	}
	want := []string{"🇺🇸 美国， 洛杉矶 ＝ 1", "🇺🇸 美国， 洛杉矶 ＝ 1'", "HK ＃2 ； a ／／ b ＂q＂", "direct · 节点"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("surge names %q", got)
	}
	loon, _ := Loon(nodes)
	if !strings.HasPrefix(string(loon), "🇺🇸 美国， 洛杉矶 ＝ 1 = Trojan,") {
		t.Errorf("loon %q", loon)
	}
	qx, _ := QX(nodes)
	if !strings.Contains(string(qx), ", tag=🇺🇸 美国， 洛杉矶 ＝ 1\n") || !strings.Contains(string(qx), `, tag=HK #2 ; a // b "q"`+"\n") {
		t.Errorf("qx %q", qx)
	}
	for _, line := range strings.Split(strings.TrimRight(string(qx), "\n"), "\n") {
		if tag := line[strings.LastIndex(line, "tag=")+4:]; strings.ContainsAny(tag, ",=") {
			t.Errorf("qx tag %q", tag)
		}
	}
}

func TestUnsafeSecretsSkipped(t *testing.T) {
	comma := Node{Name: "c", Up: mustParse(t, "ss://"+b64url("aes-128-gcm:a,b")+"@203.0.113.60:1")}
	quote := Node{Name: "q", Up: mustParse(t, "ss://"+b64url(`aes-128-gcm:a"b`)+"@203.0.113.61:1")}
	space := Node{Name: "s", Up: mustParse(t, "trojan://a%20b@t.example.com:443")}
	if l, ok := surgeProxy(comma, false); !ok || !strings.Contains(l, `password="a,b"`) {
		t.Errorf("surge comma password must be quoted: %q", l)
	}
	if _, ok := surgeProxy(quote, false); ok {
		t.Error("surge: quote in password must skip")
	}
	if _, ok := loonProxy(quote); ok {
		t.Error("loon: quote in password must skip")
	}
	if _, ok := qxProxy(comma); ok {
		t.Error("qx: comma in password must skip")
	}
	if _, ok := surgeProxy(space, true); ok {
		t.Error("surfboard trojan: unquoted password with space must skip")
	}
	if l, ok := surgeProxy(space, false); !ok || !strings.Contains(l, `password="a b"`) {
		t.Errorf("surge trojan quoted %q", l)
	}
}

func TestDetectClientV2(t *testing.T) {
	cases := map[string][2]string{
		"Surge iOS/3051": {FormatSurge, "Surge"},
		"Surge/1419 CFNetwork/1335.0.3 Darwin/21.6.0": {FormatSurge, "Surge"},
		"Surge Mac/2565":                                     {FormatSurge, "Surge"},
		"Surfboard/2.24.4 (Android 14; Pixel 8)":             {FormatSurge, "Surfboard"},
		"Loon/3.2.4 (iPhone; iOS 17.4.1; Scale/3.00)":        {FormatLoon, "Loon"},
		"Loon/773 CFNetwork/1494.0.7 Darwin/23.4.0":          {FormatLoon, "Loon"},
		"Quantumult%20X/1.4.1 (iPhone15,2; iOS 17.4)":        {FormatQX, "Quantumult X"},
		"Quantumult X/1.5.2 (iPad13,4; iOS 18.0)":            {FormatQX, "Quantumult X"},
		"Stash/2.5.0 Clash/1.9.0":                            {FormatClash, "Stash"},
		"Shadowrocket/2070 CFNetwork/1410.0.3 Darwin/22.6.0": {FormatBase64, "Shadowrocket"},
		"clash.meta/1.19.0":                                  {FormatClash, "Clash Meta"},
		"SFI/1.11.4 (Build 1; sing-box 1.11.4)":              {FormatSingBox, "sing-box"},
	}
	for ua, want := range cases {
		c := DetectClient(ua)
		if c.Format != want[0] || c.Label != want[1] || c.Browser {
			t.Errorf("%q → %+v, want %v", ua, c, want)
		}
		if c.Surfboard != (want[1] == "Surfboard") {
			t.Errorf("%q surfboard flag %v", ua, c.Surfboard)
		}
	}
	for _, f := range []string{FormatSurge, FormatLoon, FormatQX} {
		if ValidFormat(f) {
			t.Errorf("%s must not be a stored format", f)
		}
	}
}
