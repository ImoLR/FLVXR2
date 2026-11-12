package landing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Plan 250: AnyTLS chain nodes (ChainVersion 2).

// testAnyTLSCert is a self-signed ECDSA certificate like the panel's
// selfSignedLandingCert; it returns the PEMs and the DER SHA-256.
func testAnyTLSCert(t *testing.T, name string) (string, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), hex.EncodeToString(sum[:])
}

func anytlsChainNode(t *testing.T, id int64, upstream EgressOutbound) Node {
	t.Helper()
	n := chainNode(id, upstream)
	certPEM, keyPEM, _ := testAnyTLSCert(t, "www.lovelive-anime.jp")
	n.Protocol, n.VLESS = ProtocolAnyTLS, nil
	n.AnyTLS = &AnyTLS{Password: "Abcdefghijklmnop12345678", CertificatePEM: certPEM, KeyPEM: keyPEM}
	return n
}

func TestChainAnyTLSValidation(t *testing.T) {
	if ChainVersion < 2 {
		t.Fatal("AnyTLS chain nodes need landing_chain >= 2")
	}
	n := anytlsChainNode(t, 1<<40+99, goldenChainUpstreams()[11])
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{n, testNode(t, 1)}}
	if err := d.validate(); err != nil {
		t.Fatalf("AnyTLS chain node rejected: %v", err)
	}
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	if in["protocol"] != "anytls" || in["listen"] != ChainListen {
		t.Fatalf("inbound = %v", in)
	}
	users := in["settings"].(map[string]any)["users"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["password"] != n.AnyTLS.Password || users[0].(map[string]any)["email"] != n.InboundTag {
		t.Fatalf("users = %v", users)
	}
	stream := in["streamSettings"].(map[string]any)
	tlsSettings := stream["tlsSettings"].(map[string]any)
	if stream["security"] != "tls" || stream["network"] != "tcp" || !reflect.DeepEqual(tlsSettings["alpn"], []string{"h2", "http/1.1"}) {
		t.Fatalf("stream = %v", stream)
	}
	certs := tlsSettings["certificates"].([]any)
	cert := certs[0].(map[string]any)["certificate"].([]string)
	if len(certs) != 1 || cert[0] != "-----BEGIN CERTIFICATE-----" || cert[len(cert)-1] != "-----END CERTIFICATE-----" {
		t.Fatalf("certificate lines = %v", cert)
	}
	if _, ok := in["settings"].(map[string]any)["paddingScheme"]; ok {
		t.Fatal("default padding must not render a scheme")
	}
	// Public ports: a loopback AnyTLS node is never public.
	if p := d.publicPorts(); len(p.TCP) != 1 || p.TCP[0] != 30001 {
		t.Fatalf("public ports = %+v", p)
	}
	otherCert, _, _ := testAnyTLSCert(t, "other.example.com")
	mutate := map[string]func(n *Node){
		"public AnyTLS":        func(n *Node) { n.Listen, n.Chain = "", nil },
		"missing credentials":  func(n *Node) { n.AnyTLS = nil },
		"short password":       func(n *Node) { n.AnyTLS.Password = "short" },
		"password with symbol": func(n *Node) { n.AnyTLS.Password = "Abcdefghijklmnop1234567!" },
		"missing key":          func(n *Node) { n.AnyTLS.KeyPEM = "" },
		"garbage certificate": func(n *Node) {
			n.AnyTLS.CertificatePEM = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
		},
		"key of another cert":     func(n *Node) { n.AnyTLS.CertificatePEM = otherCert },
		"listen on all addresses": func(n *Node) { n.Listen = "0.0.0.0" },
	}
	for name, fn := range mutate {
		m := anytlsChainNode(t, 1<<40+99, goldenChainUpstreams()[11])
		a := *m.AnyTLS
		m.AnyTLS = &a
		fn(&m)
		if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{m}}).validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The Reality guard only ever takes VLESS Reality nodes.
	g := anytlsChainNode(t, 1<<40+99, goldenChainUpstreams()[11])
	g.GuardPort = 45000
	groups, byTag, warnings := Desired{Revision: 1, Enabled: true, Nodes: []Node{g}}.guardGroups()
	if len(groups) != 0 || len(byTag) != 0 || len(warnings) != 1 {
		t.Fatalf("AnyTLS node joined a Reality guard: %v %v %v", groups, byTag, warnings)
	}
}

// The real pinned Core accepts the AnyTLS chain inbound next to Reality
// chain nodes and public SS2022 nodes; a TLS client pinned to the
// certificate completes the handshake on 127.0.0.1 only.
func TestChainAnyTLSRealCore(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	n := anytlsChainNode(t, 1<<40+99, goldenChainUpstreams()[11])
	r := renderedChain(t)
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig(freePort(t))
	cfg["inbounds"] = append(r["inbounds"].([]any), in)
	cfg["outbounds"] = append(cfg["outbounds"].([]any), r["outbounds"].([]any)...)
	cfg["routing"].(map[string]any)["rules"] = r["rules"]
	b, _ := json.MarshalIndent(cfg, "", "  ")
	path := filepath.Join(dir, "chain-anytls-test.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(core, "run", "-test", "-config", path).CombinedOutput(); err != nil {
		t.Fatalf("real Core rejected the AnyTLS chain inbound: %v\n%s", err, out)
	}

	// Hot apply through the manager, then a pinned TLS handshake.
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
	reality := chainNode(2, goldenChainUpstreams()[11])
	reality.Port = freePort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{public, reality}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch
	n.Port = freePort(t)
	d.Revision++
	d.Nodes = append(d.Nodes, n)
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("AnyTLS chain add restarted the Core or failed: %v %s/%s", err, status.Epoch, epoch)
	}
	if !strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("AnyTLS inbound not listed")
	}
	if !listensOnlyOnLoopback(t, n.Port) {
		t.Fatal("AnyTLS chain inbound reachable outside loopback")
	}
	_, _, pin := testAnyTLSCert(t, "x") // unrelated pin must fail
	block, _ := pem.Decode([]byte(n.AnyTLS.CertificatePEM))
	sum := sha256.Sum256(block.Bytes)
	want := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		pin string
		ok  bool
	}{{want, true}, {pin, false}} {
		conn, err := tls.DialWithDialer(&netDialer, "tcp", joinHostPort("127.0.0.1", n.Port), &tls.Config{ServerName: "www.lovelive-anime.jp", InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"},
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				s := sha256.Sum256(raw[0])
				if hex.EncodeToString(s[:]) != tc.pin {
					return errPinMismatch
				}
				return nil
			}})
		if (err == nil) != tc.ok {
			t.Fatalf("pin %s: handshake err=%v", tc.pin[:8], err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}
	// Removing it keeps the Core running.
	d.Revision++
	d.Nodes = d.Nodes[:2]
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("AnyTLS chain removal: %v", err)
	}
	if strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("AnyTLS inbound left after removal")
	}
}

var errPinMismatch = errorString("certificate pin mismatch")

var netDialer = net.Dialer{Timeout: 3 * time.Second}
