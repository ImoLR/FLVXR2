// Package monitorport is the agent side of plan 255 「检测端口」: TCP ports the
// panel opens on this node so the 线路监测 TcpPing from another node has
// something to connect to.
//
// A check port only accepts a connection and closes it at once (inline in the
// accept loop): it never reads, never writes, speaks no protocol and has
// no forward / proxy path. The agent refuses on its own (it does not trust the
// panel blindly): ports < 1024, 22, ports of its own services, busy ports and
// more than MaxPorts ports. The open set is persisted next to config.json and
// restored at start; a port that saw no connection for the idle window (24 h)
// closes itself. The panel re-sends its full list (Sync) after every reconnect,
// which closes every port it does not list.
package monitorport

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-gost/x/atomicfile"
)

const (
	// CapVersion is advertised in the agent handshake (`monitorPort=1`).
	CapVersion = 1
	// MaxPorts is the number of check ports one agent keeps open at most.
	MaxPorts = 8
	// MinPort: privileged ports are never opened.
	MinPort = 1024
	// DefaultIdle closes a port that saw no connection for this long.
	DefaultIdle = 24 * time.Hour
	// StateFile is the persisted open set (in the agent's config directory).
	StateFile = "monitor_ports.json"

	acceptPerSecond = 50               // accept pace per port (token bucket)
	sourceWindow    = 10 * time.Second // per-source counting window
	sourceMax       = 20               // connections per source per window that count as activity
	sourceMapMax    = 256              // sources tracked per window (bounded memory)
	errLogEvery     = 10 * time.Minute
)

// Port states in results.
const (
	StateOpen    = "open"    // listening (now or already)
	StateClosed  = "closed"  // not open (any more)
	StateBusy    = "busy"    // something else uses the port on this machine
	StateRefused = "refused" // not allowed (privileged, 22, range, cap)
	StateError   = "error"   // the bind failed for another reason
)

// Result is the outcome for one port.
type Result struct {
	Port  int    `json:"port"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// Info describes one open check port.
type Info struct {
	Port     int    `json:"port"`
	Since    int64  `json:"since"`              // unix seconds when opened (or restored)
	LastConn int64  `json:"lastConn,omitempty"` // unix seconds of the last counted connection
	Accepted uint64 `json:"accepted"`
	Limited  uint64 `json:"limited,omitempty"` // connections over the per-source cap
}

// Options configure a Manager. Zero values take the defaults.
type Options struct {
	// Path of the persisted set; "" keeps it in memory only.
	Path string
	Idle time.Duration
	// InUse reports ports this agent serves itself (gost services incl.
	// paused ones, nft rule ports, native landing ports) with a reason.
	InUse func(port int) (bool, string)
	// Listen opens the TCP listener (tests replace it).
	Listen func(port int) (net.Listener, error)
	Logf   func(format string, args ...interface{})
}

// Manager owns the check ports of this agent.
type Manager struct {
	mu    sync.Mutex
	opts  Options
	ports map[int]*listener
	errAt map[string]time.Time
	// synced: the panel sent its full list (Sync) — its list is
	// authoritative, the persisted set is not restored any more.
	synced bool
}

type listener struct {
	port     int
	ln       net.Listener
	since    int64
	lastConn atomic.Int64 // unix nanos of the last counted connection (or open time)
	accepted atomic.Uint64
	limited  atomic.Uint64
	closing  atomic.Bool
	done     chan struct{}
}

// New returns a manager with the given options.
func New(opts Options) *Manager {
	m := &Manager{ports: map[int]*listener{}, errAt: map[string]time.Time{}}
	m.setOptions(opts)
	return m
}

func (m *Manager) setOptions(opts Options) {
	if opts.Idle <= 0 {
		opts.Idle = DefaultIdle
	}
	if opts.Listen == nil {
		opts.Listen = func(port int) (net.Listener, error) {
			return net.Listen("tcp", ":"+strconv.Itoa(port))
		}
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	m.opts = opts
}

var std = New(Options{})

// Default is the agent's manager (memory only until Configure).
func Default() *Manager { return std }

// Configure sets the persisted path and the in-use check.
func (m *Manager) Configure(path string, inUse func(port int) (bool, string)) {
	m.mu.Lock()
	opts := m.opts
	opts.Path, opts.InUse = path, inUse
	m.setOptions(opts)
	m.mu.Unlock()
}

func (m *Manager) logf(key, format string, args ...interface{}) {
	if key != "" {
		now := time.Now()
		m.mu.Lock()
		at, seen := m.errAt[key]
		if seen && now.Sub(at) < errLogEvery {
			m.mu.Unlock()
			return
		}
		if len(m.errAt) > 64 {
			m.errAt = map[string]time.Time{}
		}
		m.errAt[key] = now
		m.mu.Unlock()
	}
	m.opts.Logf("[monitor-port] "+format, args...)
}

// Allowed reports whether the agent may open port at all (range / 22).
func Allowed(port int) (bool, string) {
	switch {
	case port < MinPort || port > 65535:
		return false, "只允许 1024-65535 的端口"
	case port == 22:
		return false, "不能使用 SSH 端口 22"
	}
	return true, ""
}

// Open starts a check port (idempotent).
func (m *Manager) Open(port int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.openLocked(port)
	if res.State == StateOpen {
		m.persistLocked()
	}
	return res
}

func (m *Manager) openLocked(port int) Result {
	if ok, why := Allowed(port); !ok {
		return Result{Port: port, State: StateRefused, Error: why}
	}
	if l := m.ports[port]; l != nil {
		return Result{Port: port, State: StateOpen}
	}
	if len(m.ports) >= MaxPorts {
		return Result{Port: port, State: StateRefused, Error: fmt.Sprintf("每个节点最多 %d 个检测端口", MaxPorts)}
	}
	if m.opts.InUse != nil {
		if busy, why := m.opts.InUse(port); busy {
			if why == "" {
				why = "该端口已被本机服务使用"
			}
			return Result{Port: port, State: StateBusy, Error: why}
		}
	}
	ln, err := m.opts.Listen(port)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return Result{Port: port, State: StateBusy, Error: "该端口已被本机其它程序占用"}
		}
		return Result{Port: port, State: StateError, Error: shortErr(err)}
	}
	now := time.Now()
	l := &listener{port: port, ln: ln, since: now.Unix(), done: make(chan struct{})}
	l.lastConn.Store(now.UnixNano())
	m.ports[port] = l
	go m.serve(l)
	m.logf("", "port %d opened (accept and close only)", port)
	return Result{Port: port, State: StateOpen}
}

// Close stops a check port (idempotent).
func (m *Manager) Close(port int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeLocked(port) {
		m.persistLocked()
	}
	return Result{Port: port, State: StateClosed}
}

func (m *Manager) closeLocked(port int) bool {
	l := m.ports[port]
	if l == nil {
		return false
	}
	delete(m.ports, port)
	l.closing.Store(true)
	_ = l.ln.Close() // the port is free once the fd is closed
	m.logf("", "port %d closed", port)
	return true
}

// Yield closes a check port because one of the agent's own services is about
// to listen on it (a real service always wins). It reports whether one was
// open.
func (m *Manager) Yield(port int) bool {
	m.mu.Lock()
	closed := m.closeLocked(port)
	if closed {
		m.persistLocked()
	}
	m.mu.Unlock()
	if closed {
		m.logf("", "port %d yields to a service of this agent", port)
	}
	return closed
}

// Sync makes the open set exactly ports: opens what is missing, closes every
// other check port. Results are in the order of ports.
func (m *Manager) Sync(ports []int) []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.synced = true
	want := make(map[int]bool, len(ports))
	for _, p := range ports {
		want[p] = true
	}
	for p := range m.ports {
		if !want[p] {
			m.closeLocked(p)
		}
	}
	out := make([]Result, 0, len(ports))
	seen := make(map[int]bool, len(ports))
	for _, p := range ports {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, m.openLocked(p))
	}
	m.persistLocked() // always: the panel's list replaces a stale persisted set
	return out
}

// List returns the open check ports (sorted).
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.ports))
	for _, l := range m.ports {
		out = append(out, Info{Port: l.port, Since: l.since, LastConn: l.lastConn.Load() / int64(time.Second),
			Accepted: l.accepted.Load(), Limited: l.limited.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// Ports returns the open port numbers (sorted).
func (m *Manager) Ports() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.portsLocked()
}

func (m *Manager) portsLocked() []int {
	out := make([]int, 0, len(m.ports))
	for p := range m.ports {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// CloseAll stops every check port without touching the persisted set (agent
// shutdown: the ports come back at the next start).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, l := range m.ports {
		delete(m.ports, p)
		l.closing.Store(true)
		_ = l.ln.Close()
	}
}

type persisted struct {
	Version int   `json:"version"`
	Ports   []int `json:"ports"`
}

func (m *Manager) persistLocked() {
	if m.opts.Path == "" {
		return
	}
	data, _ := json.Marshal(persisted{Version: 1, Ports: m.portsLocked()})
	if err := atomicfile.WriteFile(m.opts.Path, append(data, '\n'), 0o600); err != nil {
		go m.logf("persist", "save %s: %v", m.opts.Path, err)
	}
}

// Restore opens the persisted set again (agent start), unless the panel
// already sent its list. Ports that cannot be opened again are dropped.
func (m *Manager) Restore() []Result {
	m.mu.Lock()
	path, synced := m.opts.Path, m.synced
	m.mu.Unlock()
	if path == "" || synced {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st persisted
	if err := json.Unmarshal(raw, &st); err != nil {
		m.logf("", "ignore unreadable %s: %v", path, err)
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.synced {
		return nil
	}
	out := make([]Result, 0, len(st.Ports))
	for _, p := range st.Ports {
		out = append(out, m.openLocked(p))
	}
	m.persistLocked() // drop what could not be opened again
	if n := len(m.ports); n > 0 {
		go m.logf("", "restored %d check port(s): %v", n, m.portsLocked())
	}
	return out
}

// serve is the accept loop of one port: accept → close inline, count
// the connection, pace the loop. The listener's deadline is the idle window.
func (m *Manager) serve(l *listener) {
	defer close(l.done)
	idle := m.opts.Idle
	tl, _ := l.ln.(*net.TCPListener)
	var (
		tokens      = float64(acceptPerSecond)
		lastRefill  = time.Now()
		windowStart = time.Now()
		sources     = make(map[string]int)
		backoff     time.Duration
	)
	for {
		if tl != nil {
			_ = tl.SetDeadline(time.Unix(0, l.lastConn.Load()).Add(idle))
		}
		conn, err := l.ln.Accept()
		if err != nil {
			if l.closing.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if time.Since(time.Unix(0, l.lastConn.Load())) >= idle {
					m.expire(l)
					return
				}
				continue
			}
			m.logf("accept:"+strconv.Itoa(l.port), "port %d accept: %v", l.port, err)
			if backoff = backoff*2 + 5*time.Millisecond; backoff > time.Second {
				backoff = time.Second
			}
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		src := remoteIP(conn.RemoteAddr())
		closeNow(conn)
		now := time.Now()
		if now.Sub(windowStart) >= sourceWindow {
			sources = make(map[string]int)
			windowStart = now
		}
		n, tracked := sources[src]
		if tracked || len(sources) < sourceMapMax {
			n++
			sources[src] = n
		} else {
			n = sourceMax + 1 // map full: unknown sources do not count
		}
		if n > sourceMax {
			l.limited.Add(1)
		} else {
			l.accepted.Add(1)
			l.lastConn.Store(now.UnixNano())
		}
		// Token bucket: ≤ acceptPerSecond per second; the kernel backlog
		// absorbs bursts (a client's connect time is the handshake, so pacing
		// never changes a measured latency).
		tokens += now.Sub(lastRefill).Seconds() * acceptPerSecond
		lastRefill = now
		if tokens > acceptPerSecond {
			tokens = acceptPerSecond
		}
		tokens--
		if tokens < 0 {
			time.Sleep(time.Duration(-tokens / acceptPerSecond * float64(time.Second)))
		}
	}
}

func (m *Manager) expire(l *listener) {
	m.mu.Lock()
	removed := false
	if m.ports[l.port] == l {
		delete(m.ports, l.port)
		removed = true
		m.persistLocked()
	}
	m.mu.Unlock()
	_ = l.ln.Close()
	if removed {
		m.logf("", "port %d: no connection for %s, closed", l.port, m.opts.Idle)
	}
}

// closeNow closes an accepted connection with a normal FIN. Not RST: on a
// sub-millisecond path the RST could reach the client before its connect()
// reported success, and the TcpPing would read the port as refused. The
// panel's TcpPing closes with RST itself, so no TIME_WAIT is left on either
// side for a measurement.
func closeNow(conn net.Conn) {
	_ = conn.Close()
}

func remoteIP(a net.Addr) string {
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.IP.String()
	}
	if a == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
