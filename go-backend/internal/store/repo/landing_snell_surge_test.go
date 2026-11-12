package repo

import (
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 193: the 复制链接 Surge line only uses what Surge supports for Snell
// (v4 / v5: obfs=http only); TLS obfuscation gives no Surge line but a note,
// while the Clash (mihomo) entry keeps obfs-opts tls.
func TestSnellSurgeLine(t *testing.T) {
	node := func(opts string) model.LandingNode {
		return model.LandingNode{Name: "我的, Snell=1", Protocol: "snell", Password: "PSK0123456789abcdef", ProtocolOpts: opts}
	}
	for _, c := range []struct {
		opts, surge, note, clash string
	}{
		{`{"version":4}`, "我的  Snell 1 = snell, 203.0.113.9, 30004, psk=PSK0123456789abcdef, version=4", "", "version: 4"},
		{`{"version":5,"obfs":"none"}`, "我的  Snell 1 = snell, 203.0.113.9, 30004, psk=PSK0123456789abcdef, version=5", "", "version: 5"},
		{`{"version":5,"obfs":"http","obfsHost":"www.bing.com"}`, "我的  Snell 1 = snell, 203.0.113.9, 30004, psk=PSK0123456789abcdef, version=5, obfs=http, obfs-host=www.bing.com", "", "mode: http"},
		{`{"version":4,"obfs":"tls","obfsHost":"www.bing.com"}`, "", SnellSurgeNoTLSObfs, "mode: tls"},
		{`{"version":5,"obfs":"tls","obfsHost":"www.bing.com"}`, "", SnellSurgeNoTLSObfs, "mode: tls"},
		{`{}`, "我的  Snell 1 = snell, 203.0.113.9, 30004, psk=PSK0123456789abcdef, version=4", "", "version: 4"},
	} {
		clash, surge, note := LandingClientConfigs(node(c.opts), "203.0.113.9", 30004, true)
		if surge != c.surge || note != c.note || !strings.Contains(clash, c.clash) || strings.Contains(surge, "obfs=tls") {
			t.Fatalf("%s: surge %q note %q clash %q", c.opts, surge, note, clash)
		}
	}
	// Plan 250: AnyTLS has a Surge line (pinned, like the Surge subscription).
	pin := strings.Repeat("ab", 32)
	if _, surge, note := LandingClientConfigs(model.LandingNode{Protocol: "anytls", Name: "a", Password: "Abcdefghijklmnop12345678", CertSHA256: pin, ProtocolOpts: `{"sni":"www.example.com"}`}, "203.0.113.9", 1, true); surge != `a = anytls, 203.0.113.9, 1, password="Abcdefghijklmnop12345678", sni=www.example.com, server-cert-fingerprint-sha256=`+pin || note != "" {
		t.Fatalf("anytls Surge line %q %q", surge, note)
	}
}
