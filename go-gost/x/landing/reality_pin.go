package landing

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Plan 227: fastest-IP pin of a guarded Reality dest (contract reality-pick-v1
// §3). Before, the guard's dokodemo inbound carried the dest host and the
// Core resolved it for every new Reality connection — a DNS lottery between
// near and far CDN edges (+100–150 ms per connection on a far one). Now the
// guard dials the pinned IP: the fastest public IPv4 of the host by TCP
// connect time. It is re-checked every 6 h (and at once when the 10 min
// liveness connect to the pinned IP fails) and moves only when the pinned IP
// fails, leaves the DNS answer, or is ≥ 20 ms slower than the new best. When
// every IP fails the guard falls back to the host (old behaviour). A move
// re-adds only the loopback guard inbound: the Core keeps running and user
// connections (node inbounds) are not touched.
const (
	realityPinRecheck    = 6 * time.Hour
	realityPinHysteresis = 20               // ms
	realityPinRetry      = 10 * time.Minute // after a failed first pin
)

// RealityPinStatus is one pinned dest host (Status.RealityPins).
type RealityPinStatus struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	PinnedIP  string `json:"pinned_ip,omitempty"` // empty = not pinned, the guard dials the host
	PinnedMs  int    `json:"pinned_ms,omitempty"`
	BestIP    string `json:"best_ip,omitempty"`
	BestMs    int    `json:"best_ms,omitempty"`
	IPs       int    `json:"ips"`        // public IPv4 addresses tried
	CheckedAt int64  `json:"checked_at"` // unix ms
	Error     string `json:"error,omitempty"`
}

type pinKey struct {
	host string
	port int
}

type realityPinner struct {
	mu      sync.Mutex
	pins    map[pinKey]*RealityPinStatus
	running map[pinKey]bool
	net     realityNet
	now     func() time.Time
	logf    func(format string, args ...any)
}

func newRealityPinner() *realityPinner {
	return &realityPinner{pins: map[pinKey]*RealityPinStatus{}, running: map[pinKey]bool{}, net: defaultRealityNet(), now: time.Now,
		logf: func(format string, args ...any) { fmt.Printf(format+"\n", args...) }}
}

// address returns the IP the guard of host:port dials, or the host itself
// when there is no usable pin.
func (p *realityPinner) address(host string, port int) string {
	if p == nil || net.ParseIP(host) != nil {
		return host
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pin := p.pins[pinKey{host, port}]; pin != nil && pin.PinnedIP != "" {
		return pin.PinnedIP
	}
	return host
}

// sync drops pins of hosts no guard uses any more and pins new hosts at once
// (bounded: ≤ 2 s DNS + 1 s connect per new host, normally a few ms). Hosts
// whose first pin failed are retried after realityPinRetry; pinned hosts are
// re-checked by recheckDue. Called from reconcile with m.mu held.
func (p *realityPinner) sync(ctx context.Context, want map[pinKey]bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	for k := range p.pins {
		if !want[k] {
			delete(p.pins, k)
		}
	}
	var fresh []pinKey
	now := p.now()
	for k := range want {
		if net.ParseIP(k.host) != nil || p.running[k] {
			continue
		}
		pin := p.pins[k]
		if pin == nil || (pin.PinnedIP == "" && now.Sub(time.UnixMilli(pin.CheckedAt)) >= realityPinRetry) {
			fresh = append(fresh, k)
			p.running[k] = true
		}
	}
	p.mu.Unlock()
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].host < fresh[j].host })
	for _, k := range fresh {
		p.check(ctx, k)
	}
}

// recheckDue re-checks pins older than realityPinRecheck (in the background,
// one host at a time) and reports whether any pin moved. Pins not due get a
// liveness check instead: one TCP connect to the pinned IP (every Reality
// connection goes through it, so a dead pin must not wait 6 h); a failed one
// triggers the full check at once.
func (p *realityPinner) recheckDue(ctx context.Context) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	var due, live []pinKey
	liveIP := map[pinKey]string{}
	now := p.now()
	for k, pin := range p.pins {
		if p.running[k] {
			continue
		}
		age := now.Sub(time.UnixMilli(pin.CheckedAt))
		switch {
		case age >= realityPinRecheck || (pin.PinnedIP == "" && age >= realityPinRetry):
			due = append(due, k)
			p.running[k] = true
		case pin.PinnedIP != "":
			live = append(live, k)
			liveIP[k] = pin.PinnedIP
			p.running[k] = true
		}
	}
	p.mu.Unlock()
	sort.Slice(live, func(i, j int) bool { return live[i].host < live[j].host })
	for _, k := range live {
		if p.alive(ctx, liveIP[k], k.port) {
			p.mu.Lock()
			delete(p.running, k)
			p.mu.Unlock()
			continue
		}
		due = append(due, k) // still marked running
	}
	sort.Slice(due, func(i, j int) bool { return due[i].host < due[j].host })
	moved := false
	for _, k := range due {
		if p.check(ctx, k) {
			moved = true
		}
	}
	return moved
}

// alive is the liveness check: one TCP connect (1 s), closed at once.
func (p *realityPinner) alive(ctx context.Context, ip string, port int) bool {
	s := p.net.connectAll(ctx, []string{ip}, port)
	return len(s) == 1 && s[0].TCPMs >= 0
}

// check measures host:port and updates its pin; true = the dialed address
// changed. The caller marked k running.
func (p *realityPinner) check(ctx context.Context, k pinKey) bool {
	lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	ips, err := p.net.resolvePublic(lctx, k.host)
	cancel()
	var samples []RealityProbeIP
	if err == nil {
		samples = p.net.connectAll(ctx, ips, k.port)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.running, k)
	old := p.pins[k]
	oldIP := ""
	if old != nil {
		oldIP = old.PinnedIP
	}
	next := choosePin(k, old, samples, err)
	next.CheckedAt = p.now().UnixMilli()
	p.pins[k] = next
	if next.PinnedIP != oldIP && p.logf != nil {
		from := oldIP
		if from == "" {
			from = "域名"
		}
		to := next.PinnedIP
		if to == "" {
			to = "域名（" + next.Error + "）"
		}
		p.logf("landing reality pin %s: %s → %s (%d ms, best %d ms, %d IPs)", net.JoinHostPort(k.host, strconv.Itoa(k.port)), from, to, next.PinnedMs, next.BestMs, next.IPs)
	}
	return next.PinnedIP != oldIP
}

// choosePin is the pin decision: the fastest reachable IP, unless the current
// pin is still reachable, still in the DNS answer and less than
// realityPinHysteresis ms slower than the fastest. No reachable IP → no pin
// (the guard dials the host, as before plan 227).
func choosePin(k pinKey, old *RealityPinStatus, samples []RealityProbeIP, lookupErr error) *RealityPinStatus {
	next := &RealityPinStatus{Host: k.host, Port: k.port, IPs: len(samples)}
	if lookupErr != nil {
		next.Error = "解析失败：" + shortErr(lookupErr)
		return next
	}
	best, bestMs := fastestIP(samples)
	if best == "" {
		next.Error = "所有 IP 都连不上"
		return next
	}
	next.BestIP, next.BestMs = best, bestMs
	next.PinnedIP, next.PinnedMs = best, bestMs
	if old != nil && old.PinnedIP != "" {
		for _, s := range samples {
			if s.IP == old.PinnedIP && s.TCPMs >= 0 && s.TCPMs-bestMs < realityPinHysteresis {
				next.PinnedIP, next.PinnedMs = s.IP, s.TCPMs
				break
			}
		}
	}
	return next
}

// statuses returns the pins sorted by host.
func (p *realityPinner) statuses() []RealityPinStatus {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]RealityPinStatus, 0, len(p.pins))
	for _, pin := range p.pins {
		out = append(out, *pin)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Port < out[j].Port
	})
	return out
}

// pinGuards pins the dest hosts of the desired guard groups (first pin at
// once, see sync) and sets what each guard dials. Called by reconcile.
func (m *Manager) pinGuards(ctx context.Context) {
	if m.pins == nil {
		return
	}
	want := map[pinKey]bool{}
	for _, g := range m.guardWant {
		want[pinKey{g.Host, g.DestPort}] = true
	}
	m.pins.sync(ctx, want)
	for _, g := range m.guardWant {
		g.Addr = m.pins.address(g.Host, g.DestPort)
	}
}

// runRealityPins runs every 10 min: a full check per pin every 6 h (a failed
// first pin every 10 min), otherwise one liveness connect to the pinned IP. A
// move is applied by the next reconcile tick (≤ 10 s): only the guard inbound
// is re-added.
func (m *Manager) runRealityPins(ctx context.Context) {
	if m.pins == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.pins.recheckDue(ctx)
		}
	}
}
