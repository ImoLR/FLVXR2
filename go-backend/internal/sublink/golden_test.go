package sublink

import (
	"bytes"
	"encoding/base64"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// Plan 191: the phase-1 outputs (Clash / Stash / sing-box / Base64) must stay
// byte-identical to fork.106. The files under testdata/golden were written by
// this test at fork.106 (go test -run TestGoldenV1 -update-golden) before any
// phase-2 change; never regenerate them for a phase-2 change.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden")

// goldenNodes is sampleNodes without randomness plus names that phase-2
// formats have to escape (comma, equals, comment markers, quotes, emoji).
func goldenNodes(t *testing.T) []Node {
	nodes := sampleNodes(t)
	for i := range nodes {
		if nodes[i].Extra != nil && nodes[i].Extra.CertPEM != "" {
			nodes[i].Extra.CertPEM = "-----BEGIN CERTIFICATE-----\nZ29sZGVu\n-----END CERTIFICATE-----\n"
		}
	}
	extra := []Node{
		{Name: "🇺🇸 美国, 洛杉矶 = 1", Up: mustParse(t, "trojan://pw@us.provider.example:443?sni=us.provider.example#a")},
		{Name: "HK #2 ; test // x \"q\"", Up: mustParse(t, "ss://"+b64url("aes-128-gcm:x,y=z")+"@203.0.113.50:5")},
	}
	nodes = append(nodes, extra...)
	FinalizeNames(nodes)
	return nodes
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from the fork.106 golden output (%d vs %d bytes)", name, len(got), len(want))
	}
}

func TestGoldenV1(t *testing.T) {
	nodes := goldenNodes(t)
	for _, c := range []struct {
		name string
		fn   func() ([]byte, int)
	}{
		{"clash-cn.yaml", func() ([]byte, int) { return Clash(nodes, ModeCNDirect, false) }},
		{"clash-global.yaml", func() ([]byte, int) { return Clash(nodes, ModeGlobal, false) }},
		{"stash-cn.yaml", func() ([]byte, int) { return Clash(nodes, ModeCNDirect, true) }},
		{"clash-empty.yaml", func() ([]byte, int) { return Clash(nil, ModeCNDirect, false) }},
		{"singbox-cn.json", func() ([]byte, int) { return SingBox(nodes, ModeCNDirect) }},
		{"singbox-global.json", func() ([]byte, int) { return SingBox(nodes, ModeGlobal) }},
		{"singbox-empty.json", func() ([]byte, int) { return SingBox(nil, ModeCNDirect) }},
		{"base64.txt", func() ([]byte, int) { return Base64(nodes) }},
	} {
		body, _ := c.fn()
		checkGolden(t, c.name, body)
	}
}

func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
