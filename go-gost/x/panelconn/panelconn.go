// Package panelconn controls only connections to the panel, never tunnel traffic
// or agent downloads.
package panelconn

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	Auto          = "auto"
	PreferIPv4    = "prefer_ipv4"
	IPv4Only      = "ipv4_only"
	IPv6Only      = "ipv6_only"
	failureWindow = 10 * time.Minute
	healDuration  = time.Hour
)

func Normalize(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", Auto:
		return Auto, nil
	case PreferIPv4, IPv4Only, IPv6Only:
		return strings.TrimSpace(mode), nil
	default:
		return "", fmt.Errorf("invalid panel_ip_family: %q", mode)
	}
}

type Policy struct {
	mu          sync.Mutex
	mode        string
	failures    []time.Time
	preferUntil time.Time
	now         func() time.Time
	logf        func(string, ...interface{})
	transports  map[string]*http.Transport
}

var Default = New()

func New() *Policy {
	p := &Policy{mode: Auto, now: time.Now, logf: func(f string, a ...interface{}) { fmt.Printf(f+"\n", a...) }, transports: make(map[string]*http.Transport)}
	for _, mode := range []string{Auto, PreferIPv4, IPv4Only, IPv6Only} {
		mode := mode
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialMode(ctx, network, address, mode, (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
		}
		p.transports[mode] = tr
	}
	return p
}

func (p *Policy) SetMode(value string) (bool, error) {
	mode, err := Normalize(value)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if mode == p.mode {
		return false, nil
	}
	p.mode = mode
	p.failures = nil
	p.preferUntil = time.Time{}
	p.CloseIdleConnections()
	return true, nil
}

func (p *Policy) Mode() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

func (p *Policy) effectiveLocked() string {
	if !p.preferUntil.IsZero() && !p.now().Before(p.preferUntil) {
		p.preferUntil = time.Time{}
		p.failures = nil
		p.CloseIdleConnections()
		p.logf("panel connection: auto self-heal expired; normal IP family selection restored")
	}
	if p.mode == Auto && !p.preferUntil.IsZero() {
		return PreferIPv4
	}
	return p.mode
}

func (p *Policy) effective() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.effectiveLocked()
}

// IPv6Failure is called once per abnormally ended IPv6 WS session. Local
// restarts and server close frames are excluded by the reporter.
func (p *Policy) IPv6Failure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.effectiveLocked() != Auto {
		return
	}
	now := p.now()
	recent := p.failures[:0]
	for _, at := range p.failures {
		if now.Sub(at) <= failureWindow {
			recent = append(recent, at)
		}
	}
	p.failures = append(recent, now)
	if len(p.failures) < 2 {
		return
	}
	p.preferUntil = now.Add(healDuration)
	p.failures = nil
	p.CloseIdleConnections()
	p.logf("panel connection: 2 abnormal IPv6 sessions within 10m; prefer IPv4 for 1h (auto self-heal)")
}

func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialMode(ctx, network, address, p.effective(), (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
}

type dialFunc func(context.Context, string, string) (net.Conn, error)

func dialMode(ctx context.Context, network, address, mode string, dial dialFunc) (net.Conn, error) {
	switch mode {
	case IPv4Only:
		return dial(ctx, "tcp4", address)
	case IPv6Only:
		return dial(ctx, "tcp6", address)
	case PreferIPv4:
		// Reserve time for IPv6 even when IPv4 SYNs are silently dropped.
		wait := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline)/2 < wait {
			wait = time.Until(deadline) / 2
		}
		first, cancel := context.WithTimeout(ctx, wait)
		conn, err := dial(first, "tcp4", address)
		cancel()
		if err == nil || ctx.Err() != nil {
			return conn, err
		}
		return dial(ctx, "tcp6", address)
	default:
		return dial(ctx, network, address)
	}
}

// Each mode has its own pool, so a setting change cannot reuse an idle
// connection from the previous family. The same policy serves WS and HTTP.
func (p *Policy) RoundTrip(req *http.Request) (*http.Response, error) {
	return p.transports[p.effective()].RoundTrip(req)
}

func (p *Policy) CloseIdleConnections() {
	for _, tr := range p.transports {
		tr.CloseIdleConnections()
	}
}

func Family(addr net.Addr) string {
	if addr == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "unknown"
	}
	if i := strings.LastIndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	if ip.To4() != nil {
		return "IPv4"
	}
	return "IPv6"
}
