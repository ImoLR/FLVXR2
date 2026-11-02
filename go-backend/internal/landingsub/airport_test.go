package landingsub

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// airportClash builds a Clash config shaped like a typical airport
// (subconverter-style flow proxies, a dozen groups listing every node, a
// rule list): nodes proxies, rules rule lines. Plan 193 measurements.
func airportClash(nodes, rules int) string {
	var b strings.Builder
	b.WriteString("port: 7890\nsocks-port: 7891\nallow-lan: false\nmode: rule\nlog-level: info\nexternal-controller: 127.0.0.1:9090\n")
	b.WriteString("dns:\n  enable: true\n  enhanced-mode: fake-ip\n  nameserver:\n    - 223.5.5.5\n    - 119.29.29.29\n")
	b.WriteString("proxies:\n")
	b.WriteString("  - {name: 剩余流量：512.3 GB, server: 1.1.1.1, port: 443, type: ss, cipher: aes-128-gcm, password: info}\n")
	b.WriteString("  - {name: 套餐到期：2027-01-01, server: 1.1.1.1, port: 443, type: ss, cipher: aes-128-gcm, password: info}\n")
	names := []string{}
	regions := []string{"🇭🇰 香港", "🇯🇵 日本", "🇸🇬 新加坡", "🇺🇸 美国", "🇹🇼 台湾", "🇰🇷 韩国"}
	for i := 0; i < nodes; i++ {
		name := fmt.Sprintf("%s %02d | IPLC x1.5", regions[i%len(regions)], i+1)
		names = append(names, name)
		host := fmt.Sprintf("n%03d.relay-%d.example-airport.net", i, i%7)
		switch i % 5 {
		case 0:
			fmt.Fprintf(&b, "  - {name: %s, server: %s, port: %d, type: ss, cipher: aes-256-gcm, password: 6b0f3c1e-%04d-4c5d-9a8b-2f1e0d3c4b5a, udp: true}\n", name, host, 20000+i, i)
		case 1:
			fmt.Fprintf(&b, "  - {name: %s, server: %s, port: 443, type: vmess, uuid: 6b0f3c1e-%04d-4c5d-9a8b-2f1e0d3c4b5a, alterId: 0, cipher: auto, udp: true, tls: true, skip-cert-verify: false, servername: %s, network: ws, ws-opts: {path: /v2ray, headers: {Host: %s}}}\n", name, host, i, host, host)
		case 2:
			fmt.Fprintf(&b, "  - {name: %s, server: %s, port: 443, type: trojan, password: 6b0f3c1e-%04d-4c5d-9a8b-2f1e0d3c4b5a, udp: true, sni: %s, skip-cert-verify: false}\n", name, host, i, host)
		case 3:
			fmt.Fprintf(&b, "  - {name: %s, server: %s, port: 443, type: vless, uuid: 6b0f3c1e-%04d-4c5d-9a8b-2f1e0d3c4b5a, udp: true, tls: true, flow: xtls-rprx-vision, servername: www.microsoft.com, reality-opts: {public-key: Y5u4vH0Qp6yG9mKZr3QZbqSx2n8Hk8TtZl1u8rH0v0E, short-id: 6ba85179e30d4fc2}, client-fingerprint: chrome, network: tcp}\n", name, host, i)
		default:
			fmt.Fprintf(&b, "  - {name: %s, server: %s, port: 8443, type: hysteria2, password: 6b0f3c1e-%04d-4c5d-9a8b-2f1e0d3c4b5a, sni: %s, skip-cert-verify: false}\n", name, host, i, host)
		}
	}
	b.WriteString("proxy-groups:\n")
	for _, g := range []string{"🚀 节点选择", "♻️ 自动选择", "🎥 奈飞", "📹 油管", "🤖 OpenAI", "🍎 苹果", "Ⓜ️ 微软", "📲 电报", "🎮 游戏", "🌍 国外媒体", "🐟 漏网之鱼", "🎯 全球直连"} {
		fmt.Fprintf(&b, "  - name: %s\n    type: select\n    proxies:\n", g)
		for _, n := range names {
			fmt.Fprintf(&b, "      - %s\n", n)
		}
	}
	b.WriteString("rules:\n")
	for i := 0; i < rules; i++ {
		fmt.Fprintf(&b, "  - DOMAIN-SUFFIX,rule-%05d.example-site.com,🚀 节点选择\n", i)
	}
	b.WriteString("  - MATCH,🐟 漏网之鱼\n")
	return b.String()
}

func TestAirportClashSizes(t *testing.T) {
	for _, c := range []struct{ nodes, rules int }{{100, 0}, {100, 3000}, {100, 8000}, {300, 8000}} {
		body := airportClash(c.nodes, c.rules)
		r, err := ParseWith([]byte(body), Options{AllowInsecure: true})
		if err != nil {
			t.Fatal(err)
		}
		usable := 0
		for _, e := range r.Entries {
			if e.Err == "" {
				usable++
			}
		}
		t.Logf("nodes=%d rules=%d bytes=%d usable=%d info=%d", c.nodes, c.rules, len(body), usable, r.Info)
	}
}

func benchParse(b *testing.B, body []byte, opt Options) {
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		if _, err := ParseWith(body, opt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseAirport: parse cost per body (plan 193 before/after).
func BenchmarkParseAirport(b *testing.B) {
	src := Options{AllowInsecure: true, MaxNodes: 100}
	for _, c := range []struct {
		name         string
		nodes, rules int
	}{{"clash100-rules3k", 100, 3000}, {"clash300-rules8k", 300, 8000}, {"clash500-rules20k", 500, 20000}} {
		body := []byte(airportClash(c.nodes, c.rules))
		b.Run(c.name, func(b *testing.B) { benchParse(b, body, src) })
	}
	var lines strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&lines, "trojan://pw%d@n%d.example-airport.net:443?sni=n%d.example-airport.net#%%F0%%9F%%87%%AD%%F0%%9F%%87%%B0%%20HK%%20%d\n", i, i, i, i)
	}
	b64 := []byte(base64.StdEncoding.EncodeToString([]byte(lines.String())))
	b.Run("base64-500", func(b *testing.B) { benchParse(b, b64, src) })
}

// fullDecode is the pre-plan-193 path (whole document through yaml.v3).
func fullDecode(t *testing.T, text string, opt Options) *Result {
	t.Helper()
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return clashEntries(doc.Proxies, opt)
}

func usableOf(r *Result) int {
	n := 0
	for _, e := range r.Entries {
		if e.Err == "" {
			n++
		}
	}
	return n
}

// TestClashPrefixSameEntries: the proxies-only, early-stopping decode gives
// exactly the entries of the whole-document decode.
func TestClashPrefixSameEntries(t *testing.T) {
	tuic := "  - {name: TUIC %d, server: t%d.example.net, port: 443, type: tuic, uuid: u, password: p}\n"
	var mixed strings.Builder
	mixed.WriteString("mixed-port: 7890\nproxies:\n")
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&mixed, tuic, i, i)
	}
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&mixed, "  - {name: SS %d, server: s%d.example.net, port: 8388, type: ss, cipher: aes-128-gcm, password: p%d}\n", i, i, i)
	}
	mixed.WriteString("rules:\n  - MATCH,DIRECT\n")
	bodies := map[string]string{
		"airport100": airportClash(100, 3000), "airport300": airportClash(300, 50), "airport600": airportClash(600, 10), "mixed": mixed.String(),
		"col0":    "proxies:\n- name: A\n  type: ss\n  server: a.example.net\n  port: 1\n  cipher: aes-128-gcm\n  password: x\n# c\n\n- {name: B, type: trojan, server: b.example.net, port: 443, password: y}\nrules:\n- MATCH,DIRECT\n",
		"anchors": "proxies:\n  - &base {name: A, type: ss, server: a.example.net, port: 8388, cipher: aes-128-gcm, password: x}\n  - <<: *base\n    name: B\n  - name: C\n    <<: *base\n",
		"crlf":    strings.ReplaceAll(airportClash(20, 5), "\n", "\r\n"),
	}
	for label, body := range bodies {
		for _, opt := range []Options{{}, {AllowInsecure: true, MaxNodes: 100}} {
			got, err := ParseWith([]byte(body), opt)
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			want := fullDecode(t, strings.TrimSpace(strings.TrimPrefix(body, "\ufeff")), opt)
			if !reflect.DeepEqual(got.Entries, want.Entries) || got.Truncated != want.Truncated || got.Info != want.Info {
				t.Fatalf("%s %+v: entries differ (%d/%v vs %d/%v)", label, opt, len(got.Entries), got.Truncated, len(want.Entries), want.Truncated)
			}
		}
	}
	r, _ := ParseWith([]byte(bodies["mixed"]), Options{AllowInsecure: true, MaxNodes: 100})
	if usableOf(r) != 100 || !r.Truncated || r.Total != 220 {
		t.Fatalf("mixed: usable %d truncated %v total %d", usableOf(r), r.Truncated, r.Total)
	}
	r, _ = ParseWith([]byte(airportClash(100, 10)), Options{AllowInsecure: true, MaxNodes: 100})
	if usableOf(r) != 100 || r.Truncated || r.Total != 102 {
		t.Fatalf("exactly 100: usable %d truncated %v total %d", usableOf(r), r.Truncated, r.Total)
	}
}

// TestClashOutsideAnchorFallback: an alias to an anchor outside the proxies
// block still parses (whole-document fallback).
func TestClashOutsideAnchorFallback(t *testing.T) {
	body := "x-common: &c {type: ss, cipher: aes-128-gcm, password: x, port: 8388}\nproxies:\n  - <<: *c\n    name: A\n    server: a.example.net\n"
	r, err := ParseWith([]byte(body), Options{})
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Err != "" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := Parse([]byte("proxies: []\nrules: []\n")); err == nil {
		t.Fatal("empty proxies must fail")
	}
	if _, err := Parse([]byte("proxies:\nrules:\n  - MATCH,DIRECT\n")); err == nil {
		t.Fatal("null proxies must fail")
	}
}

// TestLinesMaxNodes: link lists stop parsing at the cap but count the rest.
func TestLinesMaxNodes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&b, "trojan://pw@n%d.example.net:443?sni=n%d.example.net#N%d\n", i, i, i)
	}
	r, err := ParseWith([]byte(b.String()), Options{AllowInsecure: true, MaxNodes: 100})
	if err != nil || len(r.Entries) != 100 || !r.Truncated || r.Total != 250 {
		t.Fatalf("%v entries %d truncated %v total %d", err, len(r.Entries), r.Truncated, r.Total)
	}
	r, _ = Parse([]byte(b.String()))
	if len(r.Entries) != 250 || r.Truncated || r.Total != 250 {
		t.Fatalf("import: entries %d truncated %v", len(r.Entries), r.Truncated)
	}
}
