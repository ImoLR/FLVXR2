package landing

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

// Plan 274: AnyTLS as a public landing node (landing_anytls 1).
func publicAnyTLSNode(t *testing.T, id int64) Node {
	t.Helper()
	n := testNode(t, id)
	certPEM, keyPEM, _ := testAnyTLSCert(t, "203.0.113.17")
	n.Protocol, n.SS2022 = ProtocolAnyTLS, nil
	n.AnyTLS = &AnyTLS{Password: "Abcdefghijklmnop12345678", CertificatePEM: certPEM, KeyPEM: keyPEM}
	return n
}

func TestNativeAnyTLSValidation(t *testing.T) {
	if AnyTLSVersion < 1 {
		t.Fatal("public AnyTLS nodes need landing_anytls >= 1")
	}
	n := publicAnyTLSNode(t, 7)
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1), n}}
	if err := d.validate(); err != nil {
		t.Fatalf("public AnyTLS node rejected: %v", err)
	}
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	if in["protocol"] != "anytls" || in["listen"] != "0.0.0.0" || in["port"] != n.Port {
		t.Fatalf("inbound = %v", in)
	}
	tlsSettings := in["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
	if tlsSettings["minVersion"] != "1.2" || len(tlsSettings["certificates"].([]any)) != 1 {
		t.Fatalf("tls settings = %v", tlsSettings)
	}
	// Deferred accept like a guarded Reality node (warm pool hold); the
	// loopback chain AnyTLS inbound has none.
	if b, _ := json.Marshal(in["streamSettings"]); !strings.Contains(string(b), `"opt":"9"`) || !strings.Contains(string(b), `"value":"`+guardDeferAccept+`"`) {
		t.Fatalf("public AnyTLS lacks TCP_DEFER_ACCEPT: %s", b)
	}
	chainIn, _ := anytlsChainNode(t, 1<<40+99, goldenChainUpstreams()[11]).inbound()
	if b, _ := json.Marshal(chainIn["streamSettings"]); strings.Contains(string(b), "sockopt") {
		t.Fatalf("chain AnyTLS inbound changed: %s", b)
	}
	// Public TCP port only (no UDP listener: UDP rides the TCP stream).
	p := d.publicPorts()
	if len(p.TCP) != 2 || p.TCP[1] != n.Port || len(p.UDP) != 1 || p.UDP[0] != 30001 {
		t.Fatalf("public ports = %+v", p)
	}
	otherCert, _, _ := testAnyTLSCert(t, "other.example.com")
	for name, fn := range map[string]func(n *Node){
		"missing credentials":  func(n *Node) { n.AnyTLS = nil },
		"short password":       func(n *Node) { n.AnyTLS.Password = "short" },
		"password with symbol": func(n *Node) { n.AnyTLS.Password = "Abcdefghijklmnop1234567!" },
		"missing key":          func(n *Node) { n.AnyTLS.KeyPEM = "" },
		"key of another cert":  func(n *Node) { n.AnyTLS.CertificatePEM = otherCert },
		"loopback no chain":    func(n *Node) { n.Listen = ChainListen },
	} {
		m := publicAnyTLSNode(t, 7)
		a := *m.AnyTLS
		m.AnyTLS = &a
		fn(&m)
		if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{m}}).validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Never part of a Reality guard group.
	g := publicAnyTLSNode(t, 7)
	g.GuardPort = 45000
	groups, byTag, warnings := Desired{Revision: 1, Enabled: true, Nodes: []Node{g}}.guardGroups()
	if len(groups) != 0 || len(byTag) != 0 || len(warnings) != 1 {
		t.Fatalf("AnyTLS node joined a Reality guard: %v %v %v", groups, byTag, warnings)
	}
}

// The real pinned Core runs the public AnyTLS inbound next to SS2022: hot
// add without a Core restart, a pinned TLS handshake works, a wrong pin
// fails, removal keeps the Core.
func TestNativeAnyTLSRealCore(t *testing.T) {
	dir := t.TempDir()
	_ = realCore(t, dir)
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
	n := publicAnyTLSNode(t, 7)
	n.Port = freePort(t)
	d.Revision++
	d.Nodes = append(d.Nodes, n)
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("AnyTLS add restarted the Core or failed: %v %s/%s", err, status.Epoch, epoch)
	}
	if !strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("AnyTLS inbound not listed")
	}
	block, _ := pem.Decode([]byte(n.AnyTLS.CertificatePEM))
	sum := sha256.Sum256(block.Bytes)
	want := hex.EncodeToString(sum[:])
	_, _, wrong := testAnyTLSCert(t, "x")
	for _, tc := range []struct {
		pin string
		ok  bool
	}{{want, true}, {wrong, false}} {
		var conn *tls.Conn
		for i := 0; i < 30; i++ {
			conn, err = tls.DialWithDialer(&netDialer, "tcp", joinHostPort("127.0.0.1", n.Port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"},
				VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
					s := sha256.Sum256(raw[0])
					if hex.EncodeToString(s[:]) != tc.pin {
						return errPinMismatch
					}
					return nil
				}})
			if err == nil || !tc.ok {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if (err == nil) != tc.ok {
			t.Fatalf("pin %s: handshake err=%v", tc.pin[:8], err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}
	// Public: reachable on every local address, not only loopback.
	if ips := localNonLoopback(); len(ips) > 0 && listensOnlyOnLoopback(t, n.Port) {
		t.Fatal("public AnyTLS inbound listens on loopback only")
	}
	d.Revision++
	d.Nodes = d.Nodes[:1]
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("AnyTLS removal: %v", err)
	}
	if strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("AnyTLS inbound left after removal")
	}
}
