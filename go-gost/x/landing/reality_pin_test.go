package landing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func samples(kv ...any) []RealityProbeIP {
	var out []RealityProbeIP
	for i := 0; i < len(kv); i += 2 {
		out = append(out, RealityProbeIP{IP: kv[i].(string), TCPMs: kv[i+1].(int)})
	}
	return out
}

func TestChoosePinFastestAndHysteresis(t *testing.T) {
	k := pinKey{"www.example.com", 443}
	// First pin: the fastest reachable IP.
	p := choosePin(k, nil, samples("54.230.70.30", 55, "65.9.180.116", 4, "65.9.180.9", -1), nil, realityPinHysteresis)
	if p.PinnedIP != "65.9.180.116" || p.PinnedMs != 4 || p.BestIP != "65.9.180.116" || p.IPs != 3 {
		t.Fatalf("first pin = %+v", p)
	}
	old := &RealityPinStatus{PinnedIP: "65.9.180.116", PinnedMs: 4}
	// Pinned IP 19 ms slower than the new best: keep (hysteresis 20 ms).
	p = choosePin(k, old, samples("65.9.180.116", 25, "65.9.180.20", 6), nil, realityPinHysteresis)
	if p.PinnedIP != "65.9.180.116" || p.PinnedMs != 25 || p.BestIP != "65.9.180.20" || p.BestMs != 6 || p.MoveReason != "" {
		t.Fatalf("within hysteresis = %+v", p)
	}
	// Exactly 20 ms slower: move.
	p = choosePin(k, old, samples("65.9.180.116", 26, "65.9.180.20", 6), nil, realityPinHysteresis)
	if p.PinnedIP != "65.9.180.20" || p.MoveReason != realityPinMoveFaster {
		t.Fatalf("at hysteresis = %+v", p)
	}
	// Pinned IP fails: move.
	p = choosePin(k, old, samples("65.9.180.116", -1, "54.230.70.30", 55), nil, realityPinHysteresis)
	if p.PinnedIP != "54.230.70.30" || p.PinnedMs != 55 || p.MoveReason != realityPinMoveFailover {
		t.Fatalf("pinned failed = %+v", p)
	}
	// Pinned IP left the DNS answer: move even if the others are slower.
	p = choosePin(k, old, samples("54.230.70.30", 55), nil, realityPinHysteresis)
	if p.PinnedIP != "54.230.70.30" || p.MoveReason != realityPinMoveFailover {
		t.Fatalf("pinned gone from DNS = %+v", p)
	}
	// Every IP fails: no pin (the guard dials the host as before).
	p = choosePin(k, old, samples("65.9.180.116", -1), nil, realityPinHysteresis)
	if p.PinnedIP != "" || p.Error == "" {
		t.Fatalf("all failed = %+v", p)
	}
	p = choosePin(k, old, nil, errors.New("no such host"), realityPinHysteresis)
	if p.PinnedIP != "" || !strings.Contains(p.Error, "解析失败") {
		t.Fatalf("lookup failed = %+v", p)
	}
}

// Plan 234: the margin comes from the panel (Desired.reality_pin_margin_ms);
// absent = 20 ms; 0 = any gain (a tie keeps the pin); a failed / vanished
// pin moves whatever the margin.
func TestChoosePinMarginSetting(t *testing.T) {
	k := pinKey{"www.example.com", 443}
	old := &RealityPinStatus{PinnedIP: "65.9.180.116", PinnedMs: 10}
	cases := []struct {
		margin int
		want   string
	}{{0, "65.9.180.20"}, {3, "65.9.180.20"}, {4, "65.9.180.116"}, {20, "65.9.180.116"}, {200, "65.9.180.116"}}
	for _, c := range cases { // pinned 13 ms, best 10 ms → 3 ms faster
		p := choosePin(k, old, samples("65.9.180.116", 13, "65.9.180.20", 10), nil, c.margin)
		if p.PinnedIP != c.want {
			t.Fatalf("margin %d: pinned %s, want %s", c.margin, p.PinnedIP, c.want)
		}
		if (p.PinnedIP != old.PinnedIP) != (p.MoveReason == realityPinMoveFaster) {
			t.Fatalf("margin %d: move reason %q", c.margin, p.MoveReason)
		}
	}
	if p := choosePin(k, old, samples("65.9.180.116", 10, "65.9.180.20", 10), nil, 0); p.PinnedIP != old.PinnedIP || p.MoveReason != "" {
		t.Fatalf("margin 0 tie moved: %+v", p)
	}
	if p := choosePin(k, old, samples("65.9.180.116", -1, "65.9.180.20", 90), nil, 200); p.PinnedIP != "65.9.180.20" || p.MoveReason != realityPinMoveFailover {
		t.Fatalf("failover at 200 ms: %+v", p)
	}
	if p := choosePin(k, nil, samples("65.9.180.20", 10), nil, 0); p.MoveReason != "" {
		t.Fatalf("first pin has a move reason: %+v", p)
	}

	var pin *realityPinner = newRealityPinner()
	if pin.marginMs() != realityPinHysteresis {
		t.Fatalf("default margin %d", pin.marginMs())
	}
	for _, v := range []int{0, 7, 200} {
		v := v
		pin.setMargin(&v)
		if pin.marginMs() != v {
			t.Fatalf("pushed %d, in force %d", v, pin.marginMs())
		}
	}
	pin.setMargin(nil) // panel back to the default (field absent)
	if pin.marginMs() != realityPinHysteresis {
		t.Fatalf("absent → %d, want 20", pin.marginMs())
	}
	bad := 201
	pin.setMargin(&bad)
	if pin.marginMs() != realityPinHysteresis {
		t.Fatalf("out of range → %d", pin.marginMs())
	}
}

func TestDesiredRealityPinMargin(t *testing.T) {
	for _, c := range []struct {
		v  *int
		ok bool
	}{{nil, true}, {intp(0), true}, {intp(35), true}, {intp(200), true}, {intp(-1), false}, {intp(201), false}} {
		d := Desired{Revision: 1, RealityPinMarginMs: c.v}
		if err := d.validate(); (err == nil) != c.ok {
			t.Fatalf("margin %v: err=%v", c.v, err)
		}
	}
	// Older panels never send the field: the JSON stays decodable and absent.
	var d Desired
	if err := json.Unmarshal([]byte(`{"revision":1,"enabled":true,"nodes":[]}`), &d); err != nil || d.RealityPinMarginMs != nil {
		t.Fatalf("absent field: %v %+v", err, d.RealityPinMarginMs)
	}
	if err := json.Unmarshal([]byte(`{"revision":1,"enabled":true,"nodes":[],"reality_pin_margin_ms":0}`), &d); err != nil || d.RealityPinMarginMs == nil || *d.RealityPinMarginMs != 0 {
		t.Fatalf("zero margin: %v %+v", err, d.RealityPinMarginMs)
	}
}

func intp(v int) *int { return &v }

func TestPublicIPFilter(t *testing.T) {
	for _, ip := range []string{"10.1.2.3", "127.0.0.1", "100.64.1.1", "192.168.1.1", "172.16.5.5", "169.254.169.254", "0.0.0.0", "224.0.0.1", "198.18.0.5", "240.0.0.1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatalf("%s accepted as public", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "65.9.180.116", "203.0.113.10", "1.1.1.1"} {
		if !publicIP(net.ParseIP(ip)) {
			t.Fatalf("%s refused", ip)
		}
	}
}

// fakeNet answers lookups from a table and "connects" with a per-IP latency
// (-1 = refused), counting dials.
type fakeNet struct {
	mu      sync.Mutex
	answers map[string][]string
	latency map[string]int
	dials   map[string]int
}

func (f *fakeNet) set(host string, ips map[string]int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[host] = nil
	for ip, ms := range ips {
		f.answers[host] = append(f.answers[host], ip)
		f.latency[ip] = ms
	}
}

func (f *fakeNet) rn() realityNet {
	return realityNet{
		lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			ips, ok := f.answers[host]
			if !ok {
				return nil, errors.New("no such host")
			}
			var out []net.IP
			for _, ip := range ips {
				out = append(out, net.ParseIP(ip))
			}
			return out, nil
		},
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			f.mu.Lock()
			ms, ok := f.latency[host]
			f.dials[host]++
			f.mu.Unlock()
			if !ok || ms < 0 {
				return nil, errors.New("refused")
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		},
		public: publicIP,
	}
}

func newFakeNet() *fakeNet {
	return &fakeNet{answers: map[string][]string{}, latency: map[string]int{}, dials: map[string]int{}}
}

func TestPinnerSyncRecheckAndDrop(t *testing.T) {
	f := newFakeNet()
	f.set("cdn.example.com", map[string]int{"203.0.113.1": 60, "203.0.113.2": 5, "10.0.0.9": 0, "203.0.113.3": -1})
	now := time.Unix(1_800_000_000, 0)
	var logs []string
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: f.rn(), now: func() time.Time { return now },
		logf: func(format string, args ...any) { logs = append(logs, format) }}
	k := pinKey{"cdn.example.com", 443}
	p.sync(context.Background(), map[pinKey]bool{k: true, {"1.2.3.4", 443}: true})
	if got := p.address("cdn.example.com", 443); got != "203.0.113.2" {
		t.Fatalf("address = %s", got)
	}
	if got := p.address("1.2.3.4", 443); got != "1.2.3.4" {
		t.Fatalf("ip literal = %s", got)
	}
	if f.dials["10.0.0.9"] != 0 {
		t.Fatal("private IP dialed")
	}
	st := p.statuses()
	if len(st) != 1 || st[0].PinnedIP != "203.0.113.2" || st[0].IPs != 3 || st[0].CheckedAt != now.UnixMilli() || len(logs) != 1 {
		t.Fatalf("status = %+v logs=%d", st, len(logs))
	}
	// A second sync of the same host does not measure again.
	dials := f.dials["203.0.113.2"]
	p.sync(context.Background(), map[pinKey]bool{k: true})
	if f.dials["203.0.113.2"] != dials {
		t.Fatal("sync re-measured a pinned host")
	}
	// Not due yet: only the liveness connect to the pinned IP (no DNS, no
	// other IPs), and it stays.
	now = now.Add(5 * time.Hour)
	other := f.dials["203.0.113.1"]
	if p.recheckDue(context.Background()) || f.dials["203.0.113.2"] != dials+1 || f.dials["203.0.113.1"] != other {
		t.Fatalf("recheck before 6 h: dials %v", f.dials)
	}
	if st := p.statuses(); st[0].CheckedAt != time.Unix(1_800_000_000, 0).UnixMilli() {
		t.Fatal("liveness rewrote the full-check time")
	}
	// 6 h later the pinned IP got 15 ms slower than a new best: stays.
	now = now.Add(time.Hour)
	f.set("cdn.example.com", map[string]int{"203.0.113.2": 20, "203.0.113.4": 5})
	if p.recheckDue(context.Background()) || p.address("cdn.example.com", 443) != "203.0.113.2" {
		t.Fatalf("moved inside hysteresis: %+v", p.statuses())
	}
	// Next round the pinned IP is unreachable: moves.
	now = now.Add(6 * time.Hour)
	f.set("cdn.example.com", map[string]int{"203.0.113.2": -1, "203.0.113.4": 5})
	if !p.recheckDue(context.Background()) || p.address("cdn.example.com", 443) != "203.0.113.4" {
		t.Fatalf("did not move: %+v", p.statuses())
	}
	// Host no longer used by any guard: pin dropped.
	p.sync(context.Background(), map[pinKey]bool{})
	if len(p.statuses()) != 0 || p.address("cdn.example.com", 443) != "cdn.example.com" {
		t.Fatal("pin of an unused host kept")
	}
}

func TestPinnerFailedFirstPinFallsBackAndRetries(t *testing.T) {
	f := newFakeNet()
	f.set("only-private.example.com", map[string]int{"192.168.1.10": 1})
	now := time.Unix(1_800_000_000, 0)
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: f.rn(), now: func() time.Time { return now }}
	k := pinKey{"only-private.example.com", 443}
	p.sync(context.Background(), map[pinKey]bool{k: true})
	if p.address(k.host, 443) != k.host {
		t.Fatal("private-only host pinned")
	}
	if st := p.statuses(); len(st) != 1 || st[0].Error == "" {
		t.Fatalf("status = %+v", st)
	}
	f.set("only-private.example.com", map[string]int{"203.0.113.7": 3})
	p.sync(context.Background(), map[pinKey]bool{k: true}) // retry not due yet
	if p.address(k.host, 443) != k.host {
		t.Fatal("retried too early")
	}
	now = now.Add(realityPinRetry)
	p.sync(context.Background(), map[pinKey]bool{k: true})
	if p.address(k.host, 443) != "203.0.113.7" {
		t.Fatalf("retry did not pin: %+v", p.statuses())
	}
}

func TestResolvePublicCapsAndFilters(t *testing.T) {
	f := newFakeNet()
	ips := map[string]int{"::1": 1, "10.0.0.1": 1}
	for i := 1; i <= 12; i++ {
		ips["203.0.113."+itoa(i)] = 1
	}
	f.set("many.example.com", ips)
	got, err := f.rn().resolvePublic(context.Background(), "many.example.com")
	if err != nil || len(got) != realityMaxIPs {
		t.Fatalf("got %v %v", got, err)
	}
	for _, ip := range got {
		if !strings.HasPrefix(ip, "203.0.113.") {
			t.Fatalf("non-public %s", ip)
		}
	}
}

func itoa(i int) string { return big.NewInt(int64(i)).String() }

func TestGuardInboundDialsPinnedAddress(t *testing.T) {
	g := &guardGroup{Port: 20001, Host: "www.example.com", DestPort: 443, Names: []string{"www.example.com"}}
	settings := g.inbound()["settings"].(map[string]any)
	if settings["address"] != "www.example.com" {
		t.Fatalf("unpinned address = %v", settings["address"])
	}
	keyBefore := g.key()
	g.Addr = "203.0.113.2"
	settings = g.inbound()["settings"].(map[string]any)
	if settings["address"] != "203.0.113.2" || settings["port"] != 443 {
		t.Fatalf("pinned settings = %v", settings)
	}
	if g.key() != keyBefore {
		t.Fatal("pin changed the guard group key")
	}
	// The allow rule still matches the node's serverNames (sniffed SNI).
	rules := g.rules()
	allow := rules[0].(map[string]any)
	if d := allow["domain"].([]string); len(d) != 1 || d[0] != "full:www.example.com" {
		t.Fatalf("allow rule = %v", allow)
	}
}

// --- probe ---

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func (ca *testCA) leaf(t *testing.T, names ...string) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsTarget serves TLS handshakes on a local port; dials to the "public" IPs
// in ips are redirected there.
func tlsTarget(t *testing.T, cfg *tls.Config) string {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.(*tls.Conn).Handshake()
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func probeNet(f *fakeNet, local string, roots *x509.CertPool) realityNet {
	rn := f.rn()
	plain := rn.dial
	rn.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		f.mu.Lock()
		ms, ok := f.latency[host]
		f.mu.Unlock()
		if ok && ms == 0 { // 0 = the real local TLS target
			var d net.Dialer
			return d.DialContext(ctx, "tcp", local)
		}
		return plain(ctx, addr)
	}
	rn.roots = roots
	return rn
}

func TestProbeFeasibleAndReasons(t *testing.T) {
	ca := newTestCA(t)
	good := tlsTarget(t, &tls.Config{Certificates: []tls.Certificate{ca.leaf(t, "www.good.test", "good.test", "*.good.test", "cdn.good.test")}, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS13})
	f := newFakeNet()
	// 203.0.113.1 = the real target (0 ms marker), .2 slow fake, .3 refused.
	f.set("www.good.test", map[string]int{"203.0.113.1": 0, "203.0.113.2": 40, "203.0.113.3": -1})
	rn := probeNet(f, good, ca.pool)
	res, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: []string{"www.good.test", "www.good.test:443", "1.2.3.4", "missing.test", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 3 {
		t.Fatalf("results = %+v", res.Results)
	}
	r := res.Results[0]
	if !r.Feasible || r.Target != "www.good.test:443" || r.BestIP != "203.0.113.1" || !r.TLS13 || !r.H2 || !r.X25519 || !r.CertValid || r.LatencyMs <= 0 || len(r.IPs) != 3 {
		t.Fatalf("good = %+v", r)
	}
	if strings.Join(r.ServerNames, ",") != "www.good.test,good.test,cdn.good.test" {
		t.Fatalf("server names = %v", r.ServerNames)
	}
	reasons := res.Results[1].Reason + "|" + res.Results[2].Reason
	if !strings.Contains(reasons, "需要域名") || !strings.Contains(reasons, "解析失败") {
		t.Fatalf("reasons = %s", reasons)
	}

	// Wrong certificate name, no h2, TLS 1.2 only.
	cases := map[string]struct {
		cfg    *tls.Config
		reason string
	}{
		"www.badname.test": {&tls.Config{Certificates: []tls.Certificate{ca.leaf(t, "other.test")}, NextProtos: []string{"h2"}}, "证书不可信"},
		"www.noh2.test":    {&tls.Config{Certificates: []tls.Certificate{ca.leaf(t, "www.noh2.test")}, NextProtos: []string{"http/1.1"}}, "HTTP/2"},
		"www.tls12.test":   {&tls.Config{Certificates: []tls.Certificate{ca.leaf(t, "www.tls12.test")}, NextProtos: []string{"h2"}, MaxVersion: tls.VersionTLS12}, "TLS 1.3"},
	}
	for host, c := range cases {
		addr := tlsTarget(t, c.cfg)
		f := newFakeNet()
		f.set(host, map[string]int{"203.0.113.9": 0})
		res, err := probeNet(f, addr, ca.pool).probe(context.Background(), RealityProbeRequest{Candidates: []string{host}})
		if err != nil || len(res.Results) != 1 {
			t.Fatalf("%s: %v %+v", host, err, res)
		}
		if r := res.Results[0]; r.Feasible || !strings.Contains(r.Reason, c.reason) {
			t.Fatalf("%s: %+v", host, r)
		}
	}
}

func TestProbeLimitsAndOrder(t *testing.T) {
	var many []string
	for i := 0; i <= realityProbeMaxCandidates; i++ {
		many = append(many, "h"+itoa(i)+".example.com")
	}
	if _, err := newFakeNet().rn().probe(context.Background(), RealityProbeRequest{Candidates: many}); err == nil {
		t.Fatal("65 candidates accepted")
	}
	results := []RealityProbeResult{{Target: "a", LatencyMs: 0}, {Target: "b", LatencyMs: 30}, {Target: "c", Feasible: true, LatencyMs: 50}, {Target: "d", Feasible: true, LatencyMs: 10}}
	sortRealityResults(results)
	var got []string
	for _, r := range results {
		got = append(got, r.Target)
	}
	if strings.Join(got, "") != "dcba" {
		t.Fatalf("order = %v", got)
	}
}

func TestProbeConcurrencyAtMostTwo(t *testing.T) {
	f := newFakeNet()
	var mu sync.Mutex
	cur, peak := 0, 0
	rn := f.rn()
	rn.lookup = func(ctx context.Context, host string) ([]net.IP, error) {
		mu.Lock()
		cur++
		if cur > peak {
			peak = cur
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
		return nil, errors.New("no such host")
	}
	var c []string
	for i := 0; i < 12; i++ {
		c = append(c, "h"+itoa(i)+".example.com")
	}
	if _, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: c}); err != nil {
		t.Fatal(err)
	}
	if peak > realityProbeConcurrency || peak < 2 {
		t.Fatalf("peak concurrency %d", peak)
	}
}

func TestPinnerLivenessMovesDeadPinAtOnce(t *testing.T) {
	f := newFakeNet()
	f.set("cdn.example.com", map[string]int{"203.0.113.1": 30, "203.0.113.2": 4})
	now := time.Unix(1_800_000_000, 0)
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: f.rn(), now: func() time.Time { return now }}
	k := pinKey{"cdn.example.com", 443}
	p.sync(context.Background(), map[pinKey]bool{k: true})
	if p.address(k.host, 443) != "203.0.113.2" {
		t.Fatalf("first pin: %+v", p.statuses())
	}
	// 10 min later the pinned IP is dead: the liveness connect fails and the
	// full check moves the pin without waiting for the 6 h re-check.
	now = now.Add(10 * time.Minute)
	f.mu.Lock()
	f.latency["203.0.113.2"] = -1
	f.mu.Unlock()
	if !p.recheckDue(context.Background()) || p.address(k.host, 443) != "203.0.113.1" {
		t.Fatalf("dead pin kept: %+v", p.statuses())
	}
	if st := p.statuses(); st[0].CheckedAt != now.UnixMilli() || p.running[k] {
		t.Fatalf("status after move = %+v running=%v", st, p.running)
	}
	// Liveness OK → nothing else measured.
	before := f.dials["203.0.113.2"]
	now = now.Add(10 * time.Minute)
	if p.recheckDue(context.Background()) || f.dials["203.0.113.2"] != before || p.running[k] {
		t.Fatal("healthy pin re-measured")
	}
}

// Plan 234: Apply hands the desired margin to the pinner; a later desired
// without it (panel back to 20 ms, or an older panel) restores the default.
func TestApplySetsRealityPinMargin(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	ctx := context.Background()
	if _, err := m.Apply(ctx, Desired{Revision: 1, Nodes: []Node{}, RealityPinMarginMs: intp(7)}); err != nil {
		t.Fatal(err)
	}
	if got := m.pins.marginMs(); got != 7 {
		t.Fatalf("margin after apply = %d, want 7", got)
	}
	if _, err := m.Apply(ctx, Desired{Revision: 2, Nodes: []Node{}}); err != nil {
		t.Fatal(err)
	}
	if got := m.pins.marginMs(); got != realityPinHysteresis {
		t.Fatalf("margin without the field = %d, want 20", got)
	}
	if _, err := m.Apply(ctx, Desired{Revision: 3, Nodes: []Node{}, RealityPinMarginMs: intp(500)}); err == nil {
		t.Fatal("out-of-range margin accepted")
	}
}

// meteredNet wraps fakeNet and counts every lookup and dial.
func meteredNet(f *fakeNet, lookups, dials *int) realityNet {
	rn := f.rn()
	lookup, dial := rn.lookup, rn.dial
	rn.lookup = func(ctx context.Context, host string) ([]net.IP, error) {
		*lookups++
		return lookup(ctx, host)
	}
	rn.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		*dials++
		return dial(ctx, addr)
	}
	return rn
}

// Plan 237: a changed 「Reality 目标 IP」 margin re-evaluates the pins once
// with the samples of their last check (no DNS, no connect): it moves when
// the new margin allows, stays when not, and the same value does nothing.
func TestPinnerMarginChangeReevaluatesHeldSamples(t *testing.T) {
	var lookups, dials int
	t0 := time.Unix(1_800_000_000, 0)
	var logs []string
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: meteredNet(newFakeNet(), &lookups, &dials),
		now: func() time.Time { return t0.Add(time.Hour) }, logf: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }}
	k := pinKey{"cdn.example.com", 443}
	held := samples("203.0.113.1", 30, "203.0.113.2", 18, "203.0.113.3", -1)
	// The last check kept .1 (30 ms) although .2 is 12 ms faster: < 20 ms.
	p.pins[k] = choosePin(k, &RealityPinStatus{Host: k.host, Port: k.port, PinnedIP: "203.0.113.1"}, held, nil, p.marginMs())
	p.pins[k].CheckedAt = t0.UnixMilli()
	p.held = map[pinKey][]RealityProbeIP{k: held}
	if p.address(k.host, k.port) != "203.0.113.1" {
		t.Fatalf("setup: %+v", p.pins[k])
	}
	// Same value (explicit 20 = the default) and absent: nothing.
	if p.setMargin(intp(20)) || p.setMargin(nil) || p.address(k.host, k.port) != "203.0.113.1" || len(logs) != 0 {
		t.Fatalf("same margin re-evaluated: %+v logs=%v", p.pins[k], logs)
	}
	// 15 ms: the 12 ms gain still does not reach it → stays.
	if p.setMargin(intp(15)) || p.address(k.host, k.port) != "203.0.113.1" || len(logs) != 0 {
		t.Fatalf("moved below the new margin: %+v", p.pins[k])
	}
	// 10 ms: 12 ≥ 10 → moves at once to the faster IP, reason faster,
	// checked_at unchanged (the 6 h re-check schedule does not shift).
	if !p.setMargin(intp(10)) || p.address(k.host, k.port) != "203.0.113.2" {
		t.Fatalf("did not move with margin 10: %+v", p.pins[k])
	}
	st := p.statuses()[0]
	if st.MoveReason != realityPinMoveFaster || st.PinnedMs != 18 || st.BestIP != "203.0.113.2" || st.CheckedAt != t0.UnixMilli() || st.IPs != 3 {
		t.Fatalf("status after the move = %+v", st)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "margin 15 → 10 ms") {
		t.Fatalf("logs = %v", logs)
	}
	// Same 10 again (the 1-minute re-push): nothing.
	if p.setMargin(intp(10)) || len(logs) != 1 {
		t.Fatal("same value re-evaluated")
	}
	// Back to 20 (raised): the pin is the fastest, never moves back.
	if p.setMargin(nil) || p.address(k.host, k.port) != "203.0.113.2" || len(logs) != 1 {
		t.Fatalf("raised margin moved: %+v", p.pins[k])
	}
	if lookups != 0 || dials != 0 {
		t.Fatalf("margin change measured: %d lookups, %d dials", lookups, dials)
	}
}

// A host in a running check, a host without a pin and a host without held
// samples are not touched by a margin change.
func TestPinnerMarginChangeSkipsRunningAndUnpinned(t *testing.T) {
	var lookups, dials int
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: meteredNet(newFakeNet(), &lookups, &dials), now: time.Now}
	busy, unpinned, noSamples := pinKey{"a.example.com", 443}, pinKey{"b.example.com", 443}, pinKey{"c.example.com", 443}
	held := samples("203.0.113.1", 40, "203.0.113.2", 5)
	p.pins[busy] = &RealityPinStatus{Host: busy.host, Port: 443, PinnedIP: "203.0.113.1", PinnedMs: 40, BestIP: "203.0.113.2", BestMs: 5}
	p.pins[unpinned] = &RealityPinStatus{Host: unpinned.host, Port: 443, Error: "所有 IP 都连不上"}
	p.pins[noSamples] = &RealityPinStatus{Host: noSamples.host, Port: 443, PinnedIP: "203.0.113.1", PinnedMs: 40, BestIP: "203.0.113.2", BestMs: 5}
	p.held = map[pinKey][]RealityProbeIP{busy: held, unpinned: held}
	p.running[busy] = true
	if p.setMargin(intp(0)) {
		t.Fatal("a skipped host moved")
	}
	if p.address(busy.host, 443) != "203.0.113.1" || p.address(unpinned.host, 443) != unpinned.host || p.address(noSamples.host, 443) != "203.0.113.1" {
		t.Fatalf("pins = %+v", p.statuses())
	}
	if lookups != 0 || dials != 0 {
		t.Fatalf("measured: %d lookups, %d dials", lookups, dials)
	}
}

// A full check keeps its samples for a later margin change; a dropped host
// or a failed check drops them.
func TestPinnerCheckHoldsSamples(t *testing.T) {
	f := newFakeNet()
	f.set("cdn.example.com", map[string]int{"203.0.113.1": 3, "203.0.113.2": -1})
	f.set("dead.example.com", map[string]int{"203.0.113.9": -1})
	p := newRealityPinner()
	p.net, p.logf = f.rn(), nil
	k, dead := pinKey{"cdn.example.com", 443}, pinKey{"dead.example.com", 443}
	p.sync(context.Background(), map[pinKey]bool{k: true, dead: true})
	if got := p.held[k]; len(got) != 2 {
		t.Fatalf("held = %+v", got)
	}
	if _, ok := p.held[dead]; ok {
		t.Fatal("samples held for a host without a pin")
	}
	p.sync(context.Background(), map[pinKey]bool{})
	if len(p.held) != 0 {
		t.Fatalf("samples of a dropped host kept: %+v", p.held)
	}
}

// Plan 237 end to end on the agent: the 1-minute push that carries a lower
// margin moves the pin in the same Apply (the reconcile right after it sets
// the guard's address), and the status of that apply already reports the
// move; the unchanged re-push does nothing.
func TestApplyMarginChangeMovesPinInSameApply(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	var lookups, dials int
	m.pins.net, m.pins.logf = meteredNet(newFakeNet(), &lookups, &dials), nil
	ctx := context.Background()
	k := pinKey{"cdn.example.com", 443}
	held := samples("203.0.113.1", 30, "203.0.113.2", 18)
	m.pins.pins[k] = &RealityPinStatus{Host: k.host, Port: 443, PinnedIP: "203.0.113.1", PinnedMs: 30, BestIP: "203.0.113.2", BestMs: 18, IPs: 2, CheckedAt: 1}
	m.pins.held[k] = held
	if _, err := m.Apply(ctx, Desired{Revision: 1, Nodes: []Node{}}); err != nil {
		t.Fatal(err)
	}
	if m.pins.address(k.host, 443) != "203.0.113.1" {
		t.Fatal("default margin moved the pin")
	}
	st, err := m.Apply(ctx, Desired{Revision: 2, Nodes: []Node{}, RealityPinMarginMs: intp(10)})
	if err != nil {
		t.Fatal(err)
	}
	if m.pins.address(k.host, 443) != "203.0.113.2" {
		t.Fatalf("pin not moved by the pushed margin: %+v", m.pins.statuses())
	}
	if len(st.RealityPins) != 1 || st.RealityPins[0].PinnedIP != "203.0.113.2" || st.RealityPins[0].MoveReason != realityPinMoveFaster {
		t.Fatalf("apply status pins = %+v", st.RealityPins)
	}
	// Re-push of the same revision: no re-evaluation.
	m.pins.pins[k].PinnedIP = "203.0.113.1" // would move again if re-evaluated
	if _, err := m.Apply(ctx, Desired{Revision: 2, Nodes: []Node{}, RealityPinMarginMs: intp(10)}); err != nil {
		t.Fatal(err)
	}
	if m.pins.address(k.host, 443) != "203.0.113.1" {
		t.Fatal("unchanged margin re-evaluated")
	}
	if lookups != 0 || dials != 0 {
		t.Fatalf("measured: %d lookups, %d dials", lookups, dials)
	}
}
