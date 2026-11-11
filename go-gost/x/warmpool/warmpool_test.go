package warmpool

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ---- test peers ----------------------------------------------------------

type peer struct {
	ln       net.Listener
	accepted atomic.Int64
	open     atomic.Int64
	mu       sync.Mutex
	conns    []net.Conn
	behave   func(c net.Conn)
}

func newPeer(t *testing.T, behave func(c net.Conn)) *peer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{ln: ln, behave: behave}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			p.open.Add(1)
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go func() {
				defer p.open.Add(-1)
				defer c.Close()
				if p.behave != nil {
					p.behave(c)
					return
				}
				io.Copy(io.Discard, c)
			}()
		}
	}()
	t.Cleanup(func() { p.close() })
	return p
}

func (p *peer) addr() string { return p.ln.Addr().String() }

func (p *peer) close() {
	p.ln.Close()
	p.mu.Lock()
	for _, c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
}

// echo answers each byte with the same byte.
func echo(c net.Conn) { io.Copy(c, c) }

type fakeSource struct {
	mu      sync.Mutex
	targets map[string][]Target
	dials   atomic.Int64
	fail    atomic.Bool
	tlsCfg  *tls.Config
}

func (s *fakeSource) set(owner string, ts ...Target) {
	s.mu.Lock()
	if s.targets == nil {
		s.targets = map[string][]Target{}
	}
	if ts == nil {
		delete(s.targets, owner)
	} else {
		s.targets[owner] = ts
	}
	s.mu.Unlock()
}

func (s *fakeSource) Targets(owner string) ([]Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.targets[owner]
	return ts, ok
}

func (s *fakeSource) Dial(ctx context.Context, owner, addr string) (net.Conn, error) {
	s.dials.Add(1)
	if s.fail.Load() {
		return nil, errors.New("dial refused")
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil || s.tlsCfg == nil {
		return c, err
	}
	tc := tls.Client(c, s.tlsCfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return tc, nil
}

func newTestManager(t *testing.T, cap int) (*Manager, *fakeSource) {
	t.Helper()
	m := New(cap)
	m.manualTicker = true
	src := &fakeSource{}
	m.SetSource(KindChain, src)
	t.Cleanup(func() { m.SetConfig(Config{}) })
	return m, src
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func idle(m *Manager) int { return m.Stats().Idle }

// ---- tests ---------------------------------------------------------------

func TestTakeAndRefill(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, echo)
	src.set("chains_1", Target{Addr: pe.addr()})
	if n := m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}}); n != 1 {
		t.Fatalf("owners = %d", n)
	}
	waitFor(t, "pool filled", func() bool { return idle(m) == 2 })

	l := m.Take(KindChain, "chains_1", pe.addr())
	if l == nil {
		t.Fatal("no lease")
	}
	if _, err := l.Conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(l.Conn, b); err != nil || b[0] != 'x' {
		t.Fatalf("echo through pooled conn: %v %q", err, b)
	}
	l.Done()
	l.Conn.Close()
	waitFor(t, "refilled", func() bool { return idle(m) == 2 })

	// A miss on another owner is not recorded; on this one it is.
	m.NoteMiss(KindChain, "other", pe.addr(), time.Millisecond)
	m.NoteMiss(KindChain, "chains_1", pe.addr(), 7*time.Millisecond)
	r := m.Report()
	if r == nil || len(r.Keys) != 1 {
		t.Fatalf("report = %+v", r)
	}
	k := r.Keys[0]
	if k.WarmN != 1 || k.MissN != 1 || k.ColdN != 4 || !k.Target || k.Idle != 2 {
		t.Fatalf("key = %+v", k)
	}
	if k.WarmMs > 5 {
		t.Fatalf("warm extra delay %v ms for a good pooled conn", k.WarmMs)
	}
	// Deltas reset.
	if r2 := m.Report(); r2 == nil || len(r2.Keys) != 1 || r2.Keys[0].WarmN != 0 || r2.Keys[0].ColdN != 0 {
		t.Fatalf("second report = %+v", r2)
	}
}

func TestInactiveOwnerNoTake(t *testing.T) {
	m, _ := newTestManager(t, 64)
	if m.Active(KindChain, "chains_9") {
		t.Fatal("active without config")
	}
	if l := m.Take(KindChain, "chains_9", "1.2.3.4:5"); l != nil {
		t.Fatal("lease without config")
	}
	if m.Report() != nil {
		t.Fatal("report without config")
	}
}

func TestAgeRetireAndReplace(t *testing.T) {
	m, src := newTestManager(t, 64)
	m.ageMin, m.ageMax = 150*time.Millisecond, 200*time.Millisecond
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return idle(m) == 2 })
	first := src.dials.Load()
	time.Sleep(260 * time.Millisecond)
	m.Tick()
	waitFor(t, "replaced", func() bool { return idle(m) == 2 && src.dials.Load() == first+2 })
	waitFor(t, "peer saw old conns closed", func() bool { return pe.open.Load() == 2 })
	// An expired conn is never handed out.
	time.Sleep(260 * time.Millisecond)
	if l := m.Take(KindChain, "chains_1", pe.addr()); l != nil {
		t.Fatal("expired conn handed out")
	}
}

func TestTeardownClosesEverything(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	src.set("chains_2", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2, "chains_2": 2}})
	waitFor(t, "filled", func() bool { return idle(m) == 4 && pe.open.Load() == 4 })
	m.SetConfig(Config{Chains: map[string]int{"chains_2": 2}})
	waitFor(t, "chains_1 gone", func() bool { return pe.open.Load() == 2 })
	if m.Active(KindChain, "chains_1") {
		t.Fatal("still active")
	}
	m.SetConfig(Config{})
	waitFor(t, "all gone", func() bool { return pe.open.Load() == 0 })
	if s := m.Stats(); s.Live != 0 || s.Owners != 0 {
		t.Fatalf("stats after teardown %+v", s)
	}
	// The removed pools still report their last counters once.
	if r := m.Report(); r == nil || len(r.Keys) == 0 {
		t.Fatalf("final report = %+v", r)
	}
	if r := m.Report(); r != nil {
		t.Fatalf("report after final = %+v", r)
	}
}

func TestOwnerAbsentClosesAtOnce(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return pe.open.Load() == 2 })
	src.set("chains_1") // chain deleted
	m.OwnerChanged(KindChain, "chains_1")
	waitFor(t, "closed", func() bool { return pe.open.Load() == 0 })
	time.Sleep(50 * time.Millisecond)
	if d := src.dials.Load(); d != 2 {
		t.Fatalf("dials = %d (no refill for an absent owner)", d)
	}
}

func TestAgentCap(t *testing.T) {
	m, src := newTestManager(t, 3)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	src.set("chains_2", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2, "chains_2": 2}})
	waitFor(t, "cap filled", func() bool { return m.Stats().Live == 3 && idle(m) == 3 })
	time.Sleep(50 * time.Millisecond)
	if s := m.Stats(); s.Live != 3 || pe.accepted.Load() != 3 {
		t.Fatalf("over cap: %+v accepted %d", s, pe.accepted.Load())
	}
	r := m.Report()
	capped := 0
	for _, k := range r.Keys {
		if k.State == "capped" {
			capped++
		}
	}
	if !r.CapHit || capped != 1 {
		t.Fatalf("cap not reported: %+v", r)
	}
}

func TestHeadSkipsFailedAndFollowsRecentUse(t *testing.T) {
	m, src := newTestManager(t, 64)
	a, b, c := newPeer(t, nil), newPeer(t, nil), newPeer(t, nil)
	src.set("chains_1", Target{Addr: a.addr(), Failed: true}, Target{Addr: b.addr()}, Target{Addr: c.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "head warmed", func() bool { return b.open.Load() == 2 })
	if a.accepted.Load() != 0 || c.accepted.Load() != 0 {
		t.Fatal("non-head warmed")
	}
	// A stream went to c (round-robin / 规则最优): c gets one conn.
	m.NoteMiss(KindChain, "chains_1", c.addr(), 5*time.Millisecond)
	waitFor(t, "recent target warmed", func() bool { return c.open.Load() == 1 })
	// 最优 switch: c is now first → c gets 2; b drains by age (no refill).
	src.set("chains_1", Target{Addr: c.addr()}, Target{Addr: b.addr()})
	m.OwnerChanged(KindChain, "chains_1")
	waitFor(t, "new head warmed", func() bool { return c.open.Load() == 2 })
	l := m.Take(KindChain, "chains_1", b.addr())
	if l == nil {
		t.Fatal("old head's conns should still be usable until they age out")
	}
	l.Done()
	l.Conn.Close()
	time.Sleep(50 * time.Millisecond)
	if b.accepted.Load() != 2 {
		t.Fatalf("old head refilled: accepted %d", b.accepted.Load())
	}
}

func TestYoungPeerCloseMarksUnsupported(t *testing.T) {
	m, src := newTestManager(t, 64)
	// The target closes every idle conn after 20 ms (like a 4 s handshake
	// limit): three generations → 「目标不支持预热」, then no more dials.
	pe := newPeer(t, func(c net.Conn) { time.Sleep(20 * time.Millisecond) })
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m.Tick()
		r := m.peek("chains_1", pe.addr())
		if r.unsupported {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	r := m.peek("chains_1", pe.addr())
	if !r.unsupported {
		t.Fatal("not marked unsupported")
	}
	dials := src.dials.Load()
	if dials > 8 {
		t.Fatalf("%d dials before giving up (tight loop?)", dials)
	}
	for i := 0; i < 20; i++ {
		m.Tick()
		time.Sleep(10 * time.Millisecond)
	}
	if d := src.dials.Load(); d != dials {
		t.Fatalf("dialed %d more times while unsupported", d-dials)
	}
	rep := m.Report()
	if len(rep.Keys) != 1 || rep.Keys[0].State != "unsupported" || rep.Keys[0].YoungN == 0 {
		t.Fatalf("report = %+v", rep)
	}
	if l := m.Take(KindChain, "chains_1", pe.addr()); l != nil {
		t.Fatal("lease from an unsupported target")
	}
}

func TestPeerRestartIsNotUnsupported(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return pe.open.Load() == 2 })
	// The exit restarts: every pooled conn dies at once (one generation).
	pe.mu.Lock()
	for _, c := range pe.conns {
		c.Close()
	}
	pe.mu.Unlock()
	waitFor(t, "refilled after restart", func() bool { m.Tick(); return idle(m) == 2 && src.dials.Load() == 4 })
	if p := m.peek("chains_1", pe.addr()); p.unsupported || p.youngStreak > 1 {
		t.Fatalf("restart counted as unsupported: %+v", p)
	}
}

func TestDeadInARowRests(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, nil)
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	for i := 0; i < deadStreakRest; i++ {
		waitFor(t, "conn", func() bool { return idle(m) >= 1 })
		l := m.Take(KindChain, "chains_1", pe.addr())
		if l == nil {
			t.Fatalf("no lease %d", i)
		}
		l.Conn.Close()
		l.Dead()
		time.Sleep(5 * time.Millisecond)
		l.Redialed()
	}
	p := m.peek("chains_1", pe.addr())
	if !time.Now().Before(p.restUntil) {
		t.Fatal("not resting after 3 dead conns")
	}
	waitFor(t, "flushed", func() bool { return idle(m) == 0 })
	d := src.dials.Load()
	m.Tick()
	time.Sleep(30 * time.Millisecond)
	if src.dials.Load() != d {
		t.Fatal("dialed while resting")
	}
	r := m.Report()
	if r.Keys[0].DeadN != 3 || r.Keys[0].WarmN != 3 || r.Keys[0].WarmMs < 10 || r.Keys[0].State != "rest" {
		t.Fatalf("report = %+v", r.Keys[0])
	}
}

func TestDialFailuresBackOff(t *testing.T) {
	m, src := newTestManager(t, 64)
	src.fail.Store(true)
	src.set("chains_1", Target{Addr: "127.0.0.1:9"})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	for i := 0; i < 100; i++ {
		m.Tick()
		time.Sleep(20 * time.Millisecond)
	}
	// 2 s timer granularity: at most the first attempt within 2 s.
	if d := src.dials.Load(); d > 2 {
		t.Fatalf("%d dials in 2 s against a dead target", d)
	}
}

func TestPeerSpeaksFirstKeepsByte(t *testing.T) {
	m, src := newTestManager(t, 64)
	pe := newPeer(t, func(c net.Conn) {
		c.Write([]byte("S"))
		io.Copy(c, c)
	})
	src.set("chains_1", Target{Addr: pe.addr()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 1}})
	waitFor(t, "filled", func() bool { return idle(m) == 1 })
	time.Sleep(30 * time.Millisecond)
	l := m.Take(KindChain, "chains_1", pe.addr())
	if l == nil {
		t.Fatal("no lease")
	}
	defer l.Conn.Close()
	l.Conn.Write([]byte("z"))
	b := make([]byte, 2)
	if _, err := io.ReadFull(l.Conn, b); err != nil || string(b) != "Sz" {
		t.Fatalf("got %q %v", b, err)
	}
}

func TestPooledTLSConnUsableAfterWatch(t *testing.T) {
	cert := selfSigned(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
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
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	m, src := newTestManager(t, 64)
	src.tlsCfg = &tls.Config{InsecureSkipVerify: true}
	src.set("chains_1", Target{Addr: ln.Addr().String()})
	m.SetConfig(Config{Chains: map[string]int{"chains_1": 2}})
	waitFor(t, "filled", func() bool { return idle(m) == 2 })
	time.Sleep(100 * time.Millisecond) // session tickets arrive and are read by the watcher
	for i := 0; i < 4; i++ {
		waitFor(t, "conn", func() bool { return idle(m) >= 1 })
		l := m.Take(KindChain, "chains_1", ln.Addr().String())
		if l == nil {
			t.Fatal("no lease")
		}
		if _, ok := l.Conn.(*tls.Conn); !ok {
			t.Fatalf("lease conn %T", l.Conn)
		}
		msg := []byte("relay-request")
		if _, err := l.Conn.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(l.Conn, got); err != nil || string(got) != string(msg) {
			t.Fatalf("tls echo after watch: %q %v", got, err)
		}
		l.ArmConnect()
		l.Disarm()
		l.Done()
		l.Conn.Close()
	}
}

func TestIsDeadConnErr(t *testing.T) {
	if !IsDeadConnErr(io.EOF) || !IsDeadConnErr(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}) ||
		!IsDeadConnErr(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ETIMEDOUT}) {
		t.Fatal("dead errors not recognised")
	}
	if IsDeadConnErr(errors.New("503 service unavailable")) || IsDeadConnErr(nil) {
		t.Fatal("relay status error treated as dead conn")
	}
}

// ---- helpers --------------------------------------------------------------

func (m *Manager) peek(owner, addr string) pool {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.owners[ownerKey{KindChain, owner}]
	if o == nil || o.pools[addr] == nil {
		return pool{}
	}
	return *o.pools[addr]
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "warm"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type fakeForwarder struct {
	addr  string
	dials atomic.Int64
}

func (f *fakeForwarder) WarmTargets() []Target { return []Target{{Addr: f.addr}} }
func (f *fakeForwarder) WarmDial(ctx context.Context, addr string) (net.Conn, error) {
	f.dials.Add(1)
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func TestRuleServicePoolFollowsTheService(t *testing.T) {
	m, _ := newTestManager(t, 64)
	pe := newPeer(t, nil)
	f := &fakeForwarder{addr: pe.addr()}
	m.RegisterForwarder("12_3_39_tcp", f, 2)
	if !m.Active(KindService, "12_3_39_tcp") {
		t.Fatal("rule service not active")
	}
	waitFor(t, "rule pool filled", func() bool { return pe.open.Load() == 2 })
	// The panel's chain state does not touch rule pools.
	m.SetConfig(Config{})
	time.Sleep(30 * time.Millisecond)
	if pe.open.Load() != 2 {
		t.Fatal("SetConfig removed a rule pool")
	}
	l := m.Take(KindService, "12_3_39_tcp", pe.addr())
	if l == nil {
		t.Fatal("no lease")
	}
	l.Done()
	l.Conn.Close()
	// A stale unregister (old handler of an updated service) is ignored.
	m.UnregisterForwarder("12_3_39_tcp", &fakeForwarder{addr: pe.addr()})
	waitFor(t, "refilled", func() bool { return idle(m) == 2 })
	// Rule paused / deleted: the service closes, the pool goes at once.
	m.UnregisterForwarder("12_3_39_tcp", f)
	waitFor(t, "rule pool closed", func() bool { return pe.open.Load() == 0 })
	if m.Active(KindService, "12_3_39_tcp") || m.Stats().Live != 0 {
		t.Fatalf("still active: %+v", m.Stats())
	}
}
