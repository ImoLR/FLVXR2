package landing

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Plan 229: quiet probe sockets — RST close (no TIME_WAIT), ≤ 2 open at once.

func loopbackNet(local string) realityNet {
	var d net.Dialer
	return realityNet{
		lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("203.0.113.2"), net.ParseIP("203.0.113.3")}, nil
		},
		// Every "public" IP is the local listener.
		dial:    func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", local) },
		public:  publicIP,
		sockets: make(chan struct{}, realitySocketCap),
	}
}

func TestQuietCloseSendsRST(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rn := loopbackNet(ln.Addr().String())
	for _, quiet := range []bool{true, false} {
		realityQuietClose = quiet
		got := make(chan error, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				got <- err
				return
			}
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, err = c.Read(make([]byte, 1))
			got <- err
		}()
		if ms := rn.ping(context.Background(), "203.0.113.1", 443); ms < 0 {
			t.Fatal("ping failed")
		}
		err := <-got
		if quiet && !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("quiet close: peer read = %v, want connection reset (RST)", err)
		}
		if !quiet && !errors.Is(err, io.EOF) {
			t.Fatalf("FIN close: peer read = %v, want EOF", err)
		}
	}
	realityQuietClose = true
	if len(rn.sockets) != 0 {
		t.Fatalf("socket slots not given back: %d", len(rn.sockets))
	}
}

// timeWaitTo counts TIME_WAIT sockets in /proc/net/tcp whose remote port is
// port (= our side of connections to the listener).
func timeWaitTo(t *testing.T, port int) int {
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		t.Skip("no /proc/net/tcp")
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "06" {
			continue
		}
		rem := strings.Split(fields[2], ":")
		if len(rem) == 2 {
			if p, err := strconv.ParseUint(rem[1], 16, 32); err == nil && int(p) == port {
				n++
			}
		}
	}
	return n
}

// waitingTLS serves TLS handshakes and keeps the connection until the client
// closes it (so the client is the side that closes first, like a real site).
func waitingTLS(t *testing.T, cfg *tls.Config) (string, int) {
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
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	return ln.Addr().String(), ln.Addr().(*net.TCPAddr).Port
}

func TestProbeLeavesNoTimeWait(t *testing.T) {
	ca := newTestCA(t)
	var names []string
	for i := 0; i < 6; i++ {
		names = append(names, "www.s"+itoa(i)+".test")
	}
	for _, quiet := range []bool{false, true} {
		cfg := &tls.Config{Certificates: []tls.Certificate{ca.leaf(t, names...)}, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS13}
		addr, port := waitingTLS(t, cfg)
		rn := loopbackNet(addr)
		rn.roots = ca.pool
		realityQuietClose = quiet
		res, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: names})
		realityQuietClose = true
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Results {
			if !r.Feasible || len(r.IPs) != 3 {
				t.Fatalf("result = %+v", r)
			}
		}
		// 6 candidates × (3 pings + 1 TLS) = 24 sockets, ≤ 2 open at once.
		if res.Stats == nil || res.Stats.Sockets != 24 || res.Stats.MaxOpen > realitySocketCap || res.Stats.MaxOpen < 1 || res.Stats.Linger0 != quiet {
			t.Fatalf("stats = %+v", res.Stats)
		}
		time.Sleep(100 * time.Millisecond)
		tw := timeWaitTo(t, port)
		if quiet && tw != 0 {
			t.Fatalf("quiet probe left %d TIME_WAIT sockets", tw)
		}
		if !quiet && tw == 0 {
			t.Fatal("FIN probe left no TIME_WAIT: the counter does not see them")
		}
		t.Logf("linger0=%v: %d sockets, max %d open, %d TIME_WAIT", quiet, res.Stats.Sockets, res.Stats.MaxOpen, tw)
	}
}

// countingNet wraps fakeNet dials and tracks how many sockets are open. A
// socket counts as open from the start of its dial until Close, like a real
// fd (plan 248: counting only after the dial returned left a window of a few
// microseconds per socket, so whether two ever overlapped was luck). opened
// (optional) runs right after a socket started to count.
func countingNet(f *fakeNet, sockets chan struct{}, opened ...func(addr string)) (realityNet, func() int) {
	rn := f.rn()
	var mu sync.Mutex
	open, peak := 0, 0
	plain := rn.dial
	closed := func() { mu.Lock(); open--; mu.Unlock() }
	rn.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		open++
		if open > peak {
			peak = open
		}
		mu.Unlock()
		for _, fn := range opened {
			fn(addr)
		}
		c, err := plain(ctx, addr)
		if err != nil {
			closed()
			return nil, err
		}
		return &closeHook{Conn: c, fn: closed}, nil
	}
	rn.sockets = sockets
	return rn, func() int { mu.Lock(); defer mu.Unlock(); return peak }
}

type closeHook struct {
	net.Conn
	once sync.Once
	fn   func()
}

func (c *closeHook) Close() error {
	c.once.Do(c.fn)
	return c.Conn.Close()
}

func TestSocketCapSharedByProbeAndPin(t *testing.T) {
	f := newFakeNet()
	var cands []string
	for i := 0; i < 10; i++ {
		h := "h" + itoa(i) + ".example.com"
		cands = append(cands, h)
		f.set(h, map[string]int{"203.0.113." + itoa(10+i): 8, "203.0.114." + itoa(10+i): 9, "203.0.115." + itoa(10+i): 7})
	}
	f.set("cdn.example.com", map[string]int{"198.51.100.1": 8, "198.51.100.2": 6, "198.51.100.3": 9, "198.51.100.4": 7})
	sockets := make(chan struct{}, realitySocketCap)
	// Deterministic overlap (plan 248): the very first socket stays open
	// until a socket of the other side (probe ↔ pin) is open too, so both
	// provably used the machine-wide slots at the same time. Only that one
	// socket waits; the second slot keeps cycling, so the other side gets it.
	probeOpen, pinOpen := make(chan struct{}), make(chan struct{})
	var probeOnce, pinOnce sync.Once
	var first atomic.Bool
	overlapped := make(chan bool, 1)
	rn, peak := countingNet(f, sockets, func(addr string) {
		mine, other, once := probeOpen, pinOpen, &probeOnce
		if strings.HasPrefix(addr, "198.51.100.") {
			mine, other, once = pinOpen, probeOpen, &pinOnce
		}
		once.Do(func() { close(mine) })
		if first.CompareAndSwap(false, true) {
			select {
			case <-other:
				overlapped <- true
			case <-time.After(5 * time.Second):
				overlapped <- false
			}
		}
	})
	p := &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: rn, now: time.Now}
	var wg sync.WaitGroup
	wg.Add(2)
	var res RealityProbeResponse
	go func() {
		defer wg.Done()
		res, _ = rn.probe(context.Background(), RealityProbeRequest{Candidates: cands, TimeoutMs: 500})
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			p.sync(context.Background(), map[pinKey]bool{{"cdn.example.com", 443}: true})
			p.mu.Lock()
			p.pins = map[pinKey]*RealityPinStatus{}
			p.mu.Unlock()
		}
	}()
	wg.Wait()
	if !<-overlapped {
		t.Fatal("probe and pin never had a socket open at the same time")
	}
	if got := peak(); got > realitySocketCap || got < 2 {
		t.Fatalf("peak open sockets = %d (cap %d)", got, realitySocketCap)
	}
	if res.Stats == nil || res.Stats.MaxOpen > realitySocketCap || res.Stats.Sockets != 40 {
		t.Fatalf("stats = %+v", res.Stats)
	}
	if len(sockets) != 0 {
		t.Fatalf("slots leaked: %d", len(sockets))
	}
}

func TestProbeConcurrencyOneDomainAtATime(t *testing.T) {
	f := newFakeNet()
	var mu sync.Mutex
	cur, peak := 0, 0
	rn := f.rn()
	rn.lookup = func(ctx context.Context, host string) ([]net.IP, error) {
		mu.Lock()
		cur++
		peak = max(peak, cur)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
		return nil, errors.New("no such host")
	}
	c := []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"}
	if _, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: c, Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if peak != 1 {
		t.Fatalf("concurrency 1: peak %d", peak)
	}
	peak = 0
	if _, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: c, Concurrency: 9}); err != nil {
		t.Fatal(err)
	}
	if peak != realityProbeConcurrency {
		t.Fatalf("concurrency 9 → capped: peak %d", peak)
	}
}

func TestSlotWaitNotMeasured(t *testing.T) {
	// A slot held elsewhere for 200 ms must not show up as 200 ms of latency.
	f := newFakeNet()
	f.set("w.example.com", map[string]int{"203.0.113.9": 3})
	sockets := make(chan struct{}, 1)
	rn, _ := countingNet(f, sockets)
	sockets <- struct{}{}
	go func() { time.Sleep(200 * time.Millisecond); <-sockets }()
	ms := rn.ping(context.Background(), "203.0.113.9", 443)
	if ms < 0 || ms > 100 {
		t.Fatalf("ping = %d ms (slot wait counted?)", ms)
	}
}
