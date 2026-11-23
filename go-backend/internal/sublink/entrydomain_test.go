package sublink

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestUseEntryDomains(t *testing.T) {
	// Without EntryDomain nodes the very same slice comes back, so every
	// existing subscription renders byte-identically.
	nodes := goldenNodes(t)
	if got := UseEntryDomains(nodes); &got[0] != &nodes[0] {
		t.Fatal("copied without entry domains")
	}
	before, _ := Clash(nodes, ModeCNDirect, false)
	after, _ := Clash(UseEntryDomains(nodes), ModeCNDirect, false)
	if !bytes.Equal(before, after) {
		t.Fatal("output changed without entry domains")
	}

	ss := "ss://" + b64url("aes-128-gcm:pw") + "@[240e::e4]:26196#a"
	trojanNoSNI := "trojan://pw@[240e::e4]:443#b"
	trojanSNI := "trojan://pw@[240e::e4]:443?sni=landing.example.com#c"
	wsNoHost := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@[240e::e4]:443?security=tls&sni=x.example.com&type=ws&path=%2Fws#d"
	in := []Node{
		{Name: "ss", Up: mustParse(t, ss), URI: ss, EntryDomain: "hr.example.top"},
		{Name: "trojan no sni", Up: mustParse(t, trojanNoSNI), EntryDomain: "hr.example.top"},
		{Name: "trojan sni", Up: mustParse(t, trojanSNI), EntryDomain: "hr.example.top"},
		{Name: "ws no host", Up: mustParse(t, wsNoHost), EntryDomain: "hr.example.top"},
		{Name: "anytls no sni", Extra: &Extra{Type: "anytls", Server: "240e::e4", Port: 1, Password: "p"}, EntryDomain: "hr.example.top"},
		{Name: "anytls sni", Extra: &Extra{Type: "anytls", Server: "240e::e4", Port: 2, Password: "p", SNI: "a.example.com"}, EntryDomain: "hr.example.top"},
		{Name: "plain", Up: mustParse(t, "ss://"+b64url("aes-128-gcm:x")+"@203.0.113.9:1#e")},
	}
	out := UseEntryDomains(in)
	want := []string{"hr.example.top", "240e::e4", "hr.example.top", "240e::e4", "240e::e4", "hr.example.top", "203.0.113.9"}
	for i, n := range out {
		if n.Server() != want[i] {
			t.Errorf("%s: server %q, want %q", n.Name, n.Server(), want[i])
		}
	}
	// The input is untouched (Extra copied, not shared).
	if in[0].Up.Server != "240e::e4" || in[5].Extra.Server != "240e::e4" || out[5].Extra == in[5].Extra {
		t.Fatal("input modified")
	}
	// Base64 keeps the share link (literal); Clash / sing-box use the domain.
	if b, _ := Base64(out[:1]); !strings.Contains(decodeAll(t, b), "@[240e::e4]:26196") {
		t.Fatalf("base64 changed: %s", decodeAll(t, b))
	}
	if c, _ := Clash(out[:1], ModeCNDirect, false); !strings.Contains(string(c), "server: hr.example.top") {
		t.Fatalf("clash: %s", c)
	}
	if s, _ := SingBox(out[:1], ModeCNDirect, ""); !strings.Contains(string(s), `"server": "hr.example.top"`) {
		t.Fatalf("sing-box: %s", s)
	}
}

func decodeAll(t *testing.T, b []byte) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
