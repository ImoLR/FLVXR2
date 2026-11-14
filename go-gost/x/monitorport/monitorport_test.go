package monitorport

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// loopback listens on 127.0.0.1 only (tests never open a public port).
func loopback(port int) (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
}

func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	var held []net.Listener
	for len(out) < n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, l)
		out = append(out, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range held {
		l.Close()
	}
	return out
}

func quiet(string, ...interface{}) {}

func newTest(t *testing.T, opts Options) *Manager {
	t.Helper()
	if opts.Listen == nil {
		opts.Listen = loopback
	}
	if opts.Logf == nil {
		opts.Logf = quiet
	}
	m := New(opts)
	t.Cleanup(m.CloseAll)
	return m
}

func dial(port int) (net.Conn, error) {
	return net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestOpenCloseListIdempotent(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, 2)
	for i := 0; i < 2; i++ {
		if r := m.Open(p[0]); r.State != StateOpen {
			t.Fatalf("open #%d = %+v", i, r)
		}
	}
	if r := m.Open(p[1]); r.State != StateOpen {
		t.Fatalf("open = %+v", r)
	}
	if got := m.Ports(); len(got) != 2 {
		t.Fatalf("ports %v", got)
	}
	c, err := dial(p[0])
	if err != nil {
		t.Fatalf("dial open port: %v", err)
	}
	c.Close()
	for i := 0; i < 2; i++ {
		if r := m.Close(p[0]); r.State != StateClosed {
			t.Fatalf("close = %+v", r)
		}
	}
	if _, err := dial(p[0]); err == nil {
		t.Fatal("closed port still accepts")
	}
	list := m.List()
	if len(list) != 1 || list[0].Port != p[1] || list[0].Since == 0 {
		t.Fatalf("list %+v", list)
	}
}

func TestOpenRefusesReservedPorts(t *testing.T) {
	p := freePorts(t, 2)
	m := newTest(t, Options{InUse: func(port int) (bool, string) {
		return port == p[0], "gost 服务 rule_1 使用该端口"
	}})
	for _, port := range []int{0, 22, 80, 443, 1023, 65536, 70000} {
		if r := m.Open(port); r.State != StateRefused || r.Error == "" {
			t.Fatalf("port %d = %+v, want refused", port, r)
		}
	}
	if r := m.Open(p[0]); r.State != StateBusy || r.Error == "" {
		t.Fatalf("agent's own port = %+v, want busy", r)
	}
	other, err := loopback(p[1])
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if r := m.Open(p[1]); r.State != StateBusy {
		t.Fatalf("port held by another program = %+v, want busy", r)
	}
	if n := len(m.Ports()); n != 0 {
		t.Fatalf("%d ports open after refusals", n)
	}
	// A non-EADDRINUSE bind failure is an error, not busy.
	m2 := newTest(t, Options{Listen: func(int) (net.Listener, error) { return nil, syscall.EACCES }})
	if r := m2.Open(p[1]); r.State != StateError {
		t.Fatalf("EACCES = %+v", r)
	}
}

func TestCapMaxPorts(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, MaxPorts+1)
	for i := 0; i < MaxPorts; i++ {
		if r := m.Open(p[i]); r.State != StateOpen {
			t.Fatalf("open %d = %+v", i, r)
		}
	}
	if r := m.Open(p[MaxPorts]); r.State != StateRefused {
		t.Fatalf("9th port = %+v, want refused", r)
	}
	// Re-opening an open one is still fine at the cap.
	if r := m.Open(p[0]); r.State != StateOpen {
		t.Fatalf("idempotent at cap = %+v", r)
	}
}

func TestListenerNeverWritesOrReads(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, 1)[0]
	if r := m.Open(p); r.State != StateOpen {
		t.Fatal(r)
	}
	for i := 0; i < 3; i++ {
		c, err := dial(p)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_, _ = c.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		c.Close()
		if n != 0 {
			t.Fatalf("listener wrote %d bytes: %q", n, buf[:n])
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("listener kept the connection open")
		}
		if !(errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)) {
			t.Fatalf("read err %v, want EOF / RST", err)
		}
	}
	if !waitFor(t, time.Second, func() bool { return m.List()[0].Accepted == 3 }) {
		t.Fatalf("accepted %+v", m.List())
	}
}

func TestSyncClosesUnlisted(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, 3)
	m.Open(p[0])
	m.Open(p[1])
	res := m.Sync([]int{p[1], p[2], p[2], 22})
	if len(res) != 3 || res[0].State != StateOpen || res[1].State != StateOpen || res[2].State != StateRefused {
		t.Fatalf("sync results %+v", res)
	}
	got := m.Ports()
	if len(got) != 2 || got[0] != min(p[1], p[2]) {
		t.Fatalf("after sync %v (want %d, %d)", got, p[1], p[2])
	}
	if _, err := dial(p[0]); err == nil {
		t.Fatal("unlisted port still open")
	}
	if res := m.Sync(nil); len(res) != 0 || len(m.Ports()) != 0 {
		t.Fatalf("empty sync left %v", m.Ports())
	}
}

func TestPersistAndRestoreAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateFile)
	p := freePorts(t, 3)
	m := newTest(t, Options{})
	m.Configure(path, nil)
	m.Open(p[0])
	m.Open(p[1])
	m.Close(p[1])
	m.Open(p[2])
	var st persisted
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &st); err != nil || len(st.Ports) != 2 {
		t.Fatalf("persisted %s (%v)", raw, err)
	}
	// Agent shutdown keeps the file; the new process restores both ports.
	m.CloseAll()
	if _, err := dial(p[0]); err == nil {
		t.Fatal("port open after CloseAll")
	}
	busy, _ := loopback(p[2]) // something else took one of them meanwhile
	defer busy.Close()
	m2 := newTest(t, Options{})
	m2.Configure(path, nil)
	res := m2.Restore()
	states := map[int]string{}
	for _, r := range res {
		states[r.Port] = r.State
	}
	if len(res) != 2 || states[p[0]] != StateOpen || states[p[2]] != StateBusy {
		t.Fatalf("restore %+v", res)
	}
	c, err := dial(p[0])
	if err != nil {
		t.Fatalf("restored port: %v", err)
	}
	c.Close()
	raw, _ = os.ReadFile(path)
	_ = json.Unmarshal(raw, &st)
	if len(st.Ports) != 1 || st.Ports[0] != p[0] {
		t.Fatalf("busy port not dropped from the set: %s", raw)
	}
}

func TestIdlePortClosesItself(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateFile)
	p := freePorts(t, 2)
	m := newTest(t, Options{Idle: 400 * time.Millisecond})
	m.Configure(path, nil)
	m.Open(p[0])
	m.Open(p[1])
	// p[0] keeps getting connections, p[1] none.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
				if c, err := dial(p[0]); err == nil {
					c.Close()
				}
			}
		}
	}()
	time.Sleep(900 * time.Millisecond)
	close(stop)
	wg.Wait()
	got := m.Ports()
	if len(got) != 1 || got[0] != p[0] {
		t.Fatalf("after idle window: %v (want only the busy %d)", got, p[0])
	}
	raw, _ := os.ReadFile(path)
	var st persisted
	_ = json.Unmarshal(raw, &st)
	if len(st.Ports) != 1 || st.Ports[0] != p[0] {
		t.Fatalf("expired port still persisted: %s", raw)
	}
	if !waitFor(t, 2*time.Second, func() bool { return len(m.Ports()) == 0 }) {
		t.Fatalf("idle port %d never closed", p[0])
	}
}

func TestPerSourceLimitAndPace(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, 1)[0]
	m.Open(p)
	const n = 120
	start := time.Now()
	for i := 0; i < n; i++ {
		c, err := dial(p)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		c.Close()
	}
	total := func() uint64 { l := m.List()[0]; return l.Accepted + l.Limited }
	// Paced: ≤ 50 / s after the 50-connection burst.
	time.Sleep(300 * time.Millisecond)
	if got, limit := total(), uint64(50+time.Since(start).Seconds()*50+5); got > limit {
		t.Fatalf("%d accepted in %s, pace limit %d", got, time.Since(start), limit)
	}
	if !waitFor(t, 4*time.Second, func() bool { return total() == n }) {
		t.Fatalf("only %d of %d handled", total(), n)
	}
	l := m.List()[0]
	if l.Accepted != sourceMax || l.Limited != n-sourceMax {
		t.Fatalf("one source: accepted %d limited %d, want %d / %d", l.Accepted, l.Limited, sourceMax, n-sourceMax)
	}
}

func TestYieldAndHandle(t *testing.T) {
	m := newTest(t, Options{})
	p := freePorts(t, 2)
	out, err := m.Handle(map[string]interface{}{"action": "sync", "ports": []int{p[0], p[1]}})
	if err != nil || len(out.Results) != 2 || len(out.Open) != 2 || out.Max != MaxPorts {
		t.Fatalf("sync %+v %v", out, err)
	}
	if !m.Yield(p[0]) || m.Yield(p[0]) {
		t.Fatal("yield must close once")
	}
	l, err := loopback(p[0]) // the real service can bind now
	if err != nil {
		t.Fatalf("port not free after yield: %v", err)
	}
	l.Close()
	out, err = m.Handle(map[string]interface{}{"action": "list"})
	if err != nil || len(out.Open) != 1 || out.Open[0].Port != p[1] {
		t.Fatalf("list %+v %v", out, err)
	}
	if _, err := m.Handle(map[string]interface{}{"action": "bogus"}); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := m.Handle(map[string]interface{}{"action": "sync", "ports": make([]int, 100)}); err == nil {
		t.Fatal("oversized list accepted")
	}
}

// The panel's sync is authoritative: a stale persisted port is not restored
// once the panel has spoken (agent restart with a port the panel dropped).
func TestRestoreSkippedAfterPanelSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	p := freePorts(t, 2)
	if err := os.WriteFile(path, []byte(`{"version":1,"ports":[`+strconv.Itoa(p[0])+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTest(t, Options{})
	m.Configure(path, nil)
	m.Sync([]int{p[1]})
	if res := m.Restore(); res != nil {
		t.Fatalf("restored after sync: %+v", res)
	}
	if got := m.Ports(); len(got) != 1 || got[0] != p[1] {
		t.Fatalf("ports %v", got)
	}
	raw, _ := os.ReadFile(path)
	var st persisted
	_ = json.Unmarshal(raw, &st)
	if len(st.Ports) != 1 || st.Ports[0] != p[1] {
		t.Fatalf("persisted %s", raw)
	}
}
