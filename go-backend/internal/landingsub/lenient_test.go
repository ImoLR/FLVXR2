package landingsub

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Plan 185: subscription sources keep nodes with self-signed certificates
// (flagged Insecure); the landing 链式代理 import still refuses them.
func TestAllowInsecureOnlyWithOption(t *testing.T) {
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"jp","add":"jp.example.net","port":"443","id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":"0","net":"ws","path":"/r","tls":"tls","sni":"jp.example.net","allowInsecure":"1"}`))
	lines := strings.Join([]string{
		"trojan://pw@hk.example.net:443?sni=hk.example.net&allowInsecure=1#HK",
		"hysteria2://pw@sg.example.net:8443/?sni=sg.example.net&insecure=1#SG",
		vmess,
		"trojan://pw@ok.example.net:443?sni=ok.example.net#OK",
	}, "\n")
	strict, err := Parse([]byte(lines))
	if err != nil {
		t.Fatal(err)
	}
	// Strict: only the node with a valid certificate is usable (the
	// unparsable vmess line is dropped like an info entry, as before).
	for _, e := range strict.Entries {
		if e.Err == "" && e.Name != "OK" || e.Insecure {
			t.Fatalf("strict parse accepted %s", e.Name)
		}
	}
	lenient, err := ParseWith([]byte(lines), Options{AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	insecure := 0
	for _, e := range lenient.Entries {
		if e.Err != "" {
			t.Fatalf("%s: %s", e.Name, e.Err)
		}
		if e.Insecure {
			insecure++
		}
	}
	if insecure != 3 || len(lenient.Entries) != 4 {
		t.Fatalf("lenient: %d insecure of %d", insecure, len(lenient.Entries))
	}
	clash := "proxies:\n  - {name: v, type: vmess, server: v.example.net, port: 443, uuid: b831381d-6324-4d53-ad4f-8cda48b30811, alterId: 0, cipher: auto, tls: true, skip-cert-verify: true}\n"
	if r, _ := Parse([]byte(clash)); r.Entries[0].Err == "" {
		t.Fatal("strict clash vmess insecure accepted")
	}
	if r, _ := ParseWith([]byte(clash), Options{AllowInsecure: true}); r.Entries[0].Err != "" || !r.Entries[0].Insecure {
		t.Fatalf("lenient clash vmess: %+v", r.Entries[0])
	}
}
