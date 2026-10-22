package landing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestGuardRealCoreDomainDest runs the pinned Core with a guard whose dest is a
// DOMAIN equal to its serverName (like production: www.lovelive-anime.jp:443):
// only a TLS ClientHello with an allowed SNI may reach the dest; no-SNI and
// non-TLS connections must be closed even though the dokodemo target itself is
// an allowed name (plan 128 fix: sniffed protocol tls required).
func TestGuardRealCoreDomainDest(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)

	// Dest: TLS server on 127.0.0.1 that counts accepted connections and SNIs.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "www.flvx.test"}, DNSNames: []string{"www.flvx.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	accepted, snis := 0, []string{}
	cfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			snis = append(snis, h.ServerName)
			mu.Unlock()
			return nil, nil
		}}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted++
			mu.Unlock()
			go func() {
				defer c.Close()
				s := tls.Server(c, cfg)
				_ = s.SetDeadline(time.Now().Add(5 * time.Second))
				_ = s.Handshake()
			}()
		}
	}()

	gport := freeTCPPort(t)
	g := &guardGroup{Port: gport, Host: "www.flvx.test", DestPort: ln.Addr().(*net.TCPAddr).Port, Names: []string{"www.flvx.test"}}
	conf := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{g.inbound()},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIPv4"}}, map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing":   map[string]any{"rules": g.rules()},
		// The dest name itself resolves to the local dest (Core DNS hosts).
		"dns": map[string]any{"hosts": map[string]any{"www.flvx.test": "127.0.0.1"}},
	}
	b, _ := json.Marshal(conf)
	path := filepath.Join(dir, "guard.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "run", "-config", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	addr := "127.0.0.1:" + strconv.Itoa(gport)
	for i := 0; ; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		if i > 100 {
			t.Fatal("guard did not listen")
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	accepted = 0 // the readiness dial above may have been relayed
	mu.Unlock()

	probe := func(sni string) error {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return err
		}
		defer c.Close()
		s := tls.Client(c, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
		_ = s.SetDeadline(time.Now().Add(5 * time.Second))
		return s.Handshake()
	}
	plain := func() {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: www.flvx.test\r\n\r\n"))
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _ = c.Read(make([]byte, 64))
	}
	if err := probe("www.example.com"); err == nil {
		t.Fatal("wrong SNI got through")
	}
	if err := probe(""); err == nil {
		t.Fatal("no-SNI ClientHello got through")
	}
	plain()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if accepted != 0 {
		t.Fatalf("dest saw %d connections from wrong-SNI / no-SNI / non-TLS probes (SNIs %q)", accepted, snis)
	}
	mu.Unlock()
	if err := probe("www.flvx.test"); err != nil {
		t.Fatal("allowed SNI blocked:", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if accepted != 1 || len(snis) != 1 || snis[0] != "www.flvx.test" {
		t.Fatalf("dest accepted %d, SNIs %q", accepted, snis)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
