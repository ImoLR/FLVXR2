package landing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGuardDestLegReleaseRealCore (plan 147 item 7, re-measuring the plan-137
// F14 fix): after REALITY has fetched the dest's handshake through the guard
// it closes its side; the guard's dest leg must then go within ~1 s. With the
// shared "direct" outbound (level 0: downlinkOnly 5 s) every new Reality
// connection kept 2 extra Core sockets for ~5 s; the dedicated flvxg-direct
// (undefined level → Core default 1 s) releases them in ~1 s. Both variants
// run in one pinned Core against a dest that, like a real web server, keeps
// the connection open after the handshake.
func TestGuardDestLegReleaseRealCore(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "www.flvx.test"}, DNSNames: []string{"www.flvx.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	closedAt := map[int][]time.Time{} // dest leg EOF per guard group (by SNI)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				s := tls.Server(c, cfg)
				_ = s.SetDeadline(time.Now().Add(30 * time.Second))
				if s.Handshake() != nil {
					return
				}
				// Like a web server: wait for a request until the peer goes.
				_, _ = io.Copy(io.Discard, s)
				group := 0
				if strings.HasPrefix(s.ConnectionState().ServerName, "b.") {
					group = 1
				}
				mu.Lock()
				closedAt[group] = append(closedAt[group], time.Now())
				mu.Unlock()
			}()
		}
	}()
	destPort := ln.Addr().(*net.TCPAddr).Port

	before := &guardGroup{Port: freeTCPPort(t), Host: "127.0.0.1", DestPort: destPort, Names: []string{"a.flvx.test"}}
	after := &guardGroup{Port: freeTCPPort(t), Host: "127.0.0.1", DestPort: destPort, Names: []string{"b.flvx.test"}}
	conf := baseConfig(freeTCPPort(t))
	rulesBefore := before.rules()
	rulesBefore[0].(map[string]any)["outboundTag"] = "direct" // fork.69–76 rendering
	conf["inbounds"] = []any{before.inbound(), after.inbound()}
	conf["outbounds"] = append(conf["outbounds"].([]any), guardOutbound())
	routing := conf["routing"].(map[string]any)
	routing["rules"] = append(append(rulesBefore, after.rules()...), routing["rules"].([]any)...)
	b, _ := json.Marshal(conf)
	path := filepath.Join(dir, "guard-release.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "run", "-config", path)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	for _, g := range []*guardGroup{before, after} {
		for i := 0; ; i++ {
			if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(g.Port)); err == nil {
				c.Close()
				break
			}
			if i > 100 {
				t.Fatal("guard did not listen")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	time.Sleep(6 * time.Second) // readiness probes are blocked; let them go
	sockets := func() int {
		entries, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
		n := 0
		for _, e := range entries {
			if target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", cmd.Process.Pid, e.Name())); err == nil && strings.HasPrefix(target, "socket:") {
				n++
			}
		}
		return n
	}
	const burst = 20
	measure := func(g *guardGroup, group int) (time.Duration, time.Duration, []int) {
		idle := sockets()
		var wg sync.WaitGroup
		closes := make([]time.Time, burst)
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(g.Port), 5*time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				s := tls.Client(c, &tls.Config{ServerName: g.Names[0], InsecureSkipVerify: true})
				_ = s.SetDeadline(time.Now().Add(5 * time.Second))
				if err := s.Handshake(); err != nil {
					t.Error("handshake via guard:", err)
				}
				_ = c.Close() // what REALITY does once it has the handshake
				closes[i] = time.Now()
			}(i)
		}
		wg.Wait()
		end := time.Now()
		var extra []int
		for _, at := range []time.Duration{300 * time.Millisecond, 1500 * time.Millisecond, 3 * time.Second, 6 * time.Second} {
			time.Sleep(time.Until(end.Add(at)))
			extra = append(extra, sockets()-idle)
		}
		mu.Lock()
		got := append([]time.Time(nil), closedAt[group]...)
		mu.Unlock()
		if len(got) != burst {
			t.Fatalf("group %d: dest saw %d of %d legs end", group, len(got), burst)
		}
		sort.Slice(got, func(i, j int) bool { return got[i].Before(got[j]) })
		sort.Slice(closes, func(i, j int) bool { return closes[i].Before(closes[j]) })
		var releases []time.Duration
		for i := range got {
			releases = append(releases, got[i].Sub(closes[i]))
		}
		sort.Slice(releases, func(i, j int) bool { return releases[i] < releases[j] })
		return releases[len(releases)/2], releases[len(releases)-1], extra
	}
	medA, maxA, extraA := measure(before, 0)
	medB, maxB, extraB := measure(after, 1)
	t.Logf("shared direct (before): release median %v max %v; extra Core sockets for %d new conns at +0.3/1.5/3/6 s = %v", medA.Round(10*time.Millisecond), maxA.Round(10*time.Millisecond), burst, extraA)
	t.Logf("flvxg-direct (after):  release median %v max %v; extra Core sockets for %d new conns at +0.3/1.5/3/6 s = %v", medB.Round(10*time.Millisecond), maxB.Round(10*time.Millisecond), burst, extraB)
	if maxB > 2500*time.Millisecond || extraB[2] > 2 {
		t.Fatalf("guard dest leg not released within ~1 s: max %v, sockets at +3 s %d", maxB, extraB[2])
	}
}
