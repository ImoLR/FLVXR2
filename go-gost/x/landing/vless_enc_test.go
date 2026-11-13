package landing

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Plan 253: VLESS Encryption nodes (landing_vless_enc 1).

func testVLESSEncKeys(t *testing.T) (decryption, encryption string) {
	t.Helper()
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	k, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return "mlkem768x25519plus.random.600s." + base64.RawURLEncoding.EncodeToString(priv),
		"mlkem768x25519plus.random.0rtt." + base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
}

func vlessEncNode(t *testing.T, id int64) Node {
	t.Helper()
	dec, _ := testVLESSEncKeys(t)
	return Node{ID: id, InboundTag: "flvxl-" + itoa(int(id)), Protocol: ProtocolVLESSEnc, Port: 31000 + int(id), Enabled: true,
		VLESSEnc: &VLESSEnc{UUID: "8f7a1f8e-7c2a-4c1e-9d1b-6c0a5b3c2d10", Flow: "xtls-rprx-vision", Decryption: dec}}
}

func TestVLESSEncInboundRender(t *testing.T) {
	n := vlessEncNode(t, 7)
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(in)
	s := string(b)
	for _, want := range []string{`"protocol":"vless"`, `"decryption":"` + n.VLESSEnc.Decryption + `"`, `"flow":"xtls-rprx-vision"`, `"email":"flvxl-7"`, `"streamSettings":{"network":"tcp"}`, `"listen":"0.0.0.0"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("rendered inbound lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "fallbacks") || strings.Contains(s, "security") {
		t.Fatalf("unexpected fields: %s", s)
	}
	// Empty flow is allowed and rendered without a flow key.
	n.VLESSEnc.Flow = ""
	in, err = n.inbound()
	if err != nil || strings.Contains(canonical(in), "flow") {
		t.Fatalf("empty flow: %v %s", err, canonical(in))
	}
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{vlessEncNode(t, 1), testNode(t, 2)}}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	p := d.publicPorts()
	if len(p.TCP) != 2 || len(p.UDP) != 1 {
		t.Fatalf("public ports: %+v (vless_enc is TCP only)", p)
	}
}

func TestVLESSEncInboundValidation(t *testing.T) {
	_, enc := testVLESSEncKeys(t)
	cases := map[string]func(*Node){
		"missing":       func(n *Node) { n.VLESSEnc = nil },
		"bad uuid":      func(n *Node) { n.VLESSEnc.UUID = "x" },
		"bad flow":      func(n *Node) { n.VLESSEnc.Flow = "xtls-rprx-direct" },
		"none":          func(n *Node) { n.VLESSEnc.Decryption = "none" },
		"client string": func(n *Node) { n.VLESSEnc.Decryption = enc },
		"bad mode":      func(n *Node) { n.VLESSEnc.Decryption = strings.Replace(n.VLESSEnc.Decryption, ".random.", ".weird.", 1) },
		"bad ticket":    func(n *Node) { n.VLESSEnc.Decryption = strings.Replace(n.VLESSEnc.Decryption, ".600s.", ".600.", 1) },
		"short key":     func(n *Node) { n.VLESSEnc.Decryption = "mlkem768x25519plus.random.600s.AAAAAAAAAAAAAAAAAAAAAAAA" },
		"padding after": func(n *Node) { n.VLESSEnc.Decryption += ".100-111-1111" },
		"chain node": func(n *Node) {
			n.Listen = ChainListen
			n.Chain = &EgressOutbound{Tag: chainOutboundTag(n.ID), Type: "socks", IPVersion: "system", Server: "203.0.113.9", Port: 1080}
		},
	}
	for name, mutate := range cases {
		n := vlessEncNode(t, 3)
		mutate(&n)
		d := Desired{Revision: 1, Enabled: true, Nodes: []Node{n}}
		if err := d.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, ok := range []string{"mlkem768x25519plus.native.600-900s.", "mlkem768x25519plus.xorpub.0s.100-111-1111.75-0-111."} {
		n := vlessEncNode(t, 4)
		n.VLESSEnc.Decryption = ok + strings.Split(n.VLESSEnc.Decryption, ".")[3]
		if _, err := n.inbound(); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

func TestVLESSEncOutbound(t *testing.T) {
	_, enc := testVLESSEncKeys(t)
	base := EgressOutbound{Tag: "flvxc-1099511627876", Type: "vless", IPVersion: "system", Server: "203.0.113.9", Port: 443, UUID: "8f7a1f8e-7c2a-4c1e-9d1b-6c0a5b3c2d10", Network: "tcp", Security: "none"}
	good := base
	good.Encryption, good.Flow = enc, "xtls-rprx-vision"
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	r, _ := good.render(goldenLocal)
	if s := canonical(r); !strings.Contains(s, `"encryption":"`+enc+`"`) || !strings.Contains(s, `"flow":"xtls-rprx-vision"`) {
		t.Fatalf("render: %s", s)
	}
	// Without encryption: byte-identical "none" and Vision still needs TLS.
	plain := base
	r, _ = plain.render(goldenLocal)
	if !strings.Contains(canonical(r), `"encryption":"none"`) {
		t.Fatalf("plain render: %s", canonical(r))
	}
	plain.Flow = "xtls-rprx-vision"
	if plain.validate() == nil {
		t.Fatal("Vision without TLS / encryption accepted")
	}
	for name, o := range map[string]EgressOutbound{
		"server string": func() EgressOutbound { o := good; o.Encryption = strings.Replace(enc, ".0rtt.", ".600s.", 1); return o }(),
		"garbage":       func() EgressOutbound { o := good; o.Encryption = "mlkem768x25519plus.random.0rtt.x"; return o }(),
		"on vmess":      func() EgressOutbound { o := good; o.Type, o.Flow, o.Cipher = "vmess", "", "auto"; return o }(),
		"vision on ws": func() EgressOutbound {
			o := good
			o.Network, o.WS = "ws", &EgressWS{Path: "/x"}
			return o
		}(),
	} {
		if o.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestVLESSEncRealCore: the pinned Core accepts the rendered inbound and an
// encrypted outbound, and a vless_enc node is hot-added / removed without a
// Core restart.
func TestVLESSEncRealCore(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	n := vlessEncNode(t, 9)
	n.Port = freePort(t)
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	_, enc := testVLESSEncKeys(t)
	out := EgressOutbound{Tag: "flvxc-1099511627877", Type: "vless", IPVersion: "system", Server: "203.0.113.9", Port: 443, UUID: n.VLESSEnc.UUID, Flow: "xtls-rprx-vision", Network: "tcp", Security: "none", Encryption: enc}
	if err := out.validate(); err != nil {
		t.Fatal(err)
	}
	ro, _ := out.render(goldenLocal)
	cfg := baseConfig(freePort(t))
	cfg["inbounds"] = []any{in}
	cfg["outbounds"] = append(cfg["outbounds"].([]any), ro)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	path := filepath.Join(dir, "vless-enc-test.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(core, "run", "-test", "-config", path).CombinedOutput(); err != nil {
		t.Fatalf("real Core rejected the VLESS Encryption config: %v\n%s", err, out)
	}

	m := NewManager(dir, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	public := testNode(t, 1)
	public.Port = freePort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{public}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch
	d.Revision++
	d.Nodes = append(d.Nodes, n)
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("vless_enc add restarted the Core or failed: %v %s/%s", err, status.Epoch, epoch)
	}
	if !strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("vless_enc inbound not listed")
	}
	d.Revision++
	d.Nodes = d.Nodes[:1]
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("vless_enc removal: %v", err)
	}
	if strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("vless_enc inbound left after removal")
	}
}
