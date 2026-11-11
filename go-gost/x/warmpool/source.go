package warmpool

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/selector"
)

// Forwarder is a running rule service (端口转发) that can be warmed: its
// targets in the forwarder's order and a cold dial through its own router
// (interface binding, DNS) exactly like a stream's.
type Forwarder interface {
	WarmTargets() []Target
	WarmDial(ctx context.Context, addr string) (net.Conn, error)
}

// serviceSource maps rule service names to their running forwarder.
type serviceSource struct {
	mu sync.RWMutex
	m  map[string]Forwarder
}

func (s *serviceSource) Targets(name string) ([]Target, bool) {
	s.mu.RLock()
	f := s.m[name]
	s.mu.RUnlock()
	if f == nil {
		return nil, false
	}
	return f.WarmTargets(), true
}

func (s *serviceSource) Dial(ctx context.Context, name, addr string) (net.Conn, error) {
	s.mu.RLock()
	f := s.m[name]
	s.mu.RUnlock()
	if f == nil {
		return nil, errors.New("service not running")
	}
	return f.WarmDial(ctx, addr)
}

// RegisterForwarder: a rule service with `warmPool` started (or was
// re-created). Its pool lives exactly as long as the service runs: a paused,
// deleted or updated service unregisters (the panel sends the size with the
// rule's own service config, so no separate command is needed).
func (m *Manager) RegisterForwarder(service string, f Forwarder, size int) {
	if service == "" || f == nil || size <= 0 {
		return
	}
	if size > MaxSize {
		size = MaxSize
	}
	m.mu.Lock()
	src, _ := m.sources[KindService].(*serviceSource)
	if src == nil {
		m.mu.Unlock()
		return
	}
	src.mu.Lock()
	if src.m == nil {
		src.m = map[string]Forwarder{}
	}
	src.m[service] = f
	src.mu.Unlock()
	key := ownerKey{KindService, service}
	o := m.owners[key]
	if o == nil {
		o = &owner{key: key, pools: map[string]*pool{}, recent: map[string]time.Time{}}
		m.owners[key] = o
		m.publishActiveLocked()
	}
	o.size = size
	closing := m.evaluateLocked(o)
	m.ensureTickerLocked()
	m.mu.Unlock()
	closeAll(closing)
}

// UnregisterForwarder: the rule service closed. Its pooled conns are closed
// at once.
func (m *Manager) UnregisterForwarder(service string, f Forwarder) {
	if service == "" {
		return
	}
	m.mu.Lock()
	src, _ := m.sources[KindService].(*serviceSource)
	if src == nil {
		m.mu.Unlock()
		return
	}
	src.mu.Lock()
	cur := src.m[service]
	if cur == f {
		delete(src.m, service)
	}
	src.mu.Unlock()
	var closing []*pconn
	if cur == f {
		if o := m.owners[ownerKey{KindService, service}]; o != nil {
			closing = m.removeOwnerLocked(o)
			m.publishActiveLocked()
			m.ensureTickerLocked()
		}
	}
	m.mu.Unlock()
	closeAll(closing)
}

// NodeTargets turns a node list into targets; a node fail-marked within
// window is skipped by the selector, so it is not warmed either.
func NodeTargets(nodes []*chain.Node, window time.Duration) []Target {
	out := make([]Target, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		out = append(out, Target{Addr: n.Addr, Failed: RecentlyFailed(n.Marker(), window)})
	}
	return out
}

// RecentlyFailed: the marker holds a failure younger than window.
func RecentlyFailed(mk selector.Marker, window time.Duration) bool {
	if mk == nil || mk.Count() <= 0 {
		return false
	}
	return time.Since(mk.Time()) < window
}
