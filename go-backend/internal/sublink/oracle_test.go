package sublink

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubStoreOracle (plan 191) parses the Surge / Surfboard / Loon / QX
// output back with an independent parser and compares the fields with the
// source nodes. SUBLINK_SUBSTORE_ORACLE points at a node script that reads a
// file and prints one JSON object per proxy line ({line, parser, proxy |
// error}); the plan-191 run bundled Sub-Store's open-source parsers for it
// (used as a test oracle only, nothing of it is in FLVX). Skipped otherwise.
func TestSubStoreOracle(t *testing.T) {
	oracle := os.Getenv("SUBLINK_SUBSTORE_ORACLE")
	if oracle == "" {
		t.Skip("set SUBLINK_SUBSTORE_ORACLE")
	}
	keys, m := matrixNodes(t)
	nodes := []Node{}
	for _, k := range keys {
		nodes = append(nodes, m[k])
	}
	extra := goldenNodes(t)
	nodes = append(nodes, extra[len(extra)-2:]...)
	nodes = append(nodes,
		Node{Name: "IPv6 落地", Up: mustParse(t, "ss://"+b64url("aes-128-gcm:v6pass")+"@[2001:db8::10]:8388")},
		Node{Name: "机场 自签", Up: mustParse(t, "trojan://pw2@tj2.example.com:443?sni=tj2.example.com"), Insecure: true},
		Node{Name: "Pinned", Up: mustParse(t, "trojan://pw3@tj3.example.com:443?sni=tj3.example.com&pinSHA256="+strings.Repeat("cd", 32))},
	)
	FinalizeNames(nodes)
	dir := t.TempDir()
	type conv func(Node) (string, bool)
	formats := []struct {
		name string
		conv conv
		qx   bool
		body func() []byte
	}{
		{"surge", func(n Node) (string, bool) { return surgeProxy(n, false) }, false, func() []byte { b, _ := Surge(nodes, ModeCNDirect, false); return b }},
		{"surfboard", func(n Node) (string, bool) { return surgeProxy(n, true) }, false, func() []byte { b, _ := Surge(nodes, ModeCNDirect, true); return b }},
		{"loon", loonProxy, false, func() []byte { b, _ := Loon(nodes); return b }},
		{"qx", qxProxy, true, func() []byte { b, _ := QX(nodes); return b }},
	}
	for _, f := range formats {
		kept := []Node{}
		for _, n := range nodes {
			if _, ok := f.conv(n); ok {
				kept = append(kept, n)
			}
		}
		names := lineNames(kept, f.qx)
		body := string(f.body())
		if f.name == "surge" || f.name == "surfboard" {
			body = strings.Join(surgeSection(body, "Proxy"), "\n")
		}
		file := filepath.Join(dir, f.name+".txt")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("node", oracle, file).Output()
		if err != nil {
			t.Fatalf("%s: oracle: %v", f.name, err)
		}
		var results []map[string]interface{}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var r map[string]interface{}
			if err := json.Unmarshal([]byte(l), &r); err != nil {
				t.Fatalf("%s: oracle output %q", f.name, l)
			}
			results = append(results, r)
		}
		if len(results) != len(kept) {
			t.Fatalf("%s: oracle parsed %d lines, %d nodes", f.name, len(results), len(kept))
		}
		for i, r := range results {
			if e, ok := r["error"]; ok {
				t.Errorf("%s: %q does not parse: %v", f.name, r["line"], e)
				continue
			}
			p := r["proxy"].(map[string]interface{})
			for _, msg := range compareOracle(f.name, kept[i], names[i], p) {
				t.Errorf("%s %s [%v]: %s", f.name, names[i], r["parser"], msg)
			}
		}
		t.Logf("%s: %d/%d nodes round-trip", f.name, len(results), len(kept))
	}
}

func compareOracle(format string, n Node, name string, p map[string]interface{}) []string {
	var bad []string
	str := func(k string) string {
		switch v := p[k].(type) {
		case string:
			return v
		case float64:
			return fmt.Sprint(v)
		case bool:
			return fmt.Sprint(v)
		}
		return ""
	}
	sub := func(k, kk string) string {
		if o, ok := p[k].(map[string]interface{}); ok {
			if s, ok := o[kk].(string); ok {
				return s
			}
		}
		return ""
	}
	want := func(field, got, exp string) {
		if got != exp {
			bad = append(bad, fmt.Sprintf("%s = %q, want %q", field, got, exp))
		}
	}
	want("name", str("name"), name)
	want("server", str("server"), strings.Trim(n.Server(), "[]"))
	want("port", str("port"), fmt.Sprint(n.Port()))
	typ := n.Type()
	typ = map[string]string{"shadowsocks": "ss", "socks": "socks5"}[typ] + typ
	if strings.HasPrefix(typ, "ss") && typ != "ss" {
		typ = "ss"
	}
	if strings.HasPrefix(typ, "socks5") {
		typ = "socks5"
	}
	want("type", str("type"), typ)
	sni, pin := "", ""
	if e := n.Extra; e != nil {
		sni, pin = e.SNI, e.CertSHA256
		switch e.Type {
		case "snell":
			want("psk", str("psk"), e.Password)
			want("version", str("version"), fmt.Sprint(e.Version))
			want("obfs", sub("obfs-opts", "mode"), e.Obfs)
			want("obfs-host", sub("obfs-opts", "host"), e.ObfsHost)
		default:
			want("password", str("password"), e.Password)
		}
	} else {
		u := n.Up
		if u.TLS != nil {
			sni, pin = u.TLS.ServerName, u.TLS.PinnedSHA256
		}
		switch u.Type {
		case "shadowsocks":
			want("cipher", str("cipher"), u.Method)
			want("password", str("password"), u.Password)
		case "vmess", "vless":
			want("uuid", str("uuid"), u.UUID)
		case "socks", "http":
			want("username", str("username"), u.Username)
			want("password", str("password"), u.Password)
		default:
			want("password", str("password"), u.Password)
		}
		if u.Network == "ws" {
			want("network", str("network"), "ws")
			want("ws path", sub("ws-opts", "path"), u.WS.Path)
			if h, ok := p["ws-opts"].(map[string]interface{})["headers"].(map[string]interface{}); ok || u.WS.Host != "" {
				host, _ := h["Host"].(string)
				want("ws Host", host, u.WS.Host)
			}
		} else if nw := str("network"); nw != "" && nw != "tcp" {
			bad = append(bad, "network "+nw)
		}
		if u.Reality != nil {
			sni = u.Reality.ServerName
			want("reality public-key", sub("reality-opts", "public-key"), u.Reality.PublicKey)
			want("reality short-id", sub("reality-opts", "short-id"), u.Reality.ShortID)
		}
		if u.Flow != "" {
			want("flow", str("flow"), u.Flow)
		}
		if u.Obfs != "" {
			want("obfs-password", str("obfs-password"), u.Obfs)
		}
		if u.Security == "tls" || u.Security == "reality" || u.Type == "trojan" || u.Type == "hysteria2" {
			if str("tls") == "false" {
				bad = append(bad, "tls off")
			}
		}
	}
	gotSNI := str("sni")
	if gotSNI == "" {
		gotSNI = str("servername")
	}
	// Quantumult X has one host for TLS and the WebSocket Host header.
	if sni != "" && !(format == "qx" && str("network") == "ws") {
		want("sni", gotSNI, sni)
	}
	if h := hexPin(pin); h != "" {
		want("tls-fingerprint", str("tls-fingerprint"), h)
	} else if n.Insecure || n.Type() == "anytls" {
		want("skip-cert-verify", str("skip-cert-verify"), "true")
	}
	return bad
}
