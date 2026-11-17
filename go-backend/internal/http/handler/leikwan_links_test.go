package handler

import (
	"strings"
	"testing"

	"go-backend/internal/linechain"
	"go-backend/internal/sublink"
)

// Plan 272: a 外部节点 moved to the 利群 entry keeps its domain as SNI / ws Host.
func TestLeiKwanRehost(t *testing.T) {
	cases := []struct {
		up   linechain.Upstream
		want []string
	}{
		{linechain.Upstream{Type: "trojan", Server: "jp.example.com", Port: 443, Password: "pw", Security: "tls"}, []string{"trojan://pw@106.13.17.23:10023", "sni=jp.example.com"}},
		{linechain.Upstream{Type: "vmess", Server: "ws.example.com", Port: 443, UUID: "11111111-2222-3333-4444-555555555555", Security: "tls", Network: "ws", WS: &linechain.WS{Path: "/p"}}, []string{"vmess://"}},
		{linechain.Upstream{Type: "shadowsocks", Server: "203.0.113.9", Port: 8388, Method: "aes-128-gcm", Password: "pw"}, []string{"@106.13.17.23:10023"}},
	}
	for _, c := range cases {
		got := leiKwanRehost(c.up, "106.13.17.23", 10023)
		if got.Server != "106.13.17.23" || got.Port != 10023 {
			t.Fatalf("rehost: %+v", got)
		}
		if c.up.Network == "ws" && (got.WS == nil || got.WS.Host != "ws.example.com" || got.TLS == nil || got.TLS.ServerName != "ws.example.com" || c.up.WS.Host != "") {
			t.Fatalf("ws host / sni (and the original untouched): %+v %+v", got.WS, c.up.WS)
		}
		if c.up.Type == "shadowsocks" && got.TLS != nil {
			t.Fatal("plain ss got TLS")
		}
		uri := sublink.ShareURI(sublink.Node{Name: "n", Up: got})
		for _, w := range c.want {
			if !strings.Contains(uri, w) {
				t.Fatalf("%s missing %q", uri, w)
			}
		}
	}
}

func TestLeiKwanIngressLabel(t *testing.T) {
	if l := lkIngressLabel("121.37.28.219:5438"); !strings.HasPrefix(l, "华为云") {
		t.Fatalf("label: %q", l)
	}
	if l := lkIngressLabel("106.75.132.155:5438"); !strings.HasPrefix(l, "UCloud") {
		t.Fatalf("label: %q", l)
	}
	if l := lkIngressLabel("not-an-ip"); l != "" {
		t.Fatalf("label: %q", l)
	}
}
