package logger

import (
	"fmt"
	"sync"
	"time"
)

// Per-connection warn/error lines (plan 137, QA F13). With the default warn
// level a node no longer writes access lines, but every failed connection still
// writes one or two error lines (scanners hitting a relay port, a dead target a
// client keeps retrying, a health probe). Those scale with traffic that the node
// owner does not control, so they get a budget: per service a burst of
// connLinesPerService lines refilled at the same number per minute, and the
// whole process at most connLinesGlobal per minute. The next line written after
// a drop carries "suppressed": <lines dropped for that service since>.
//
// A line is per-connection when its logger carries the "sid" field (set by the
// service for every accepted connection and by the handlers) or belongs to a
// listener (handshake/upgrade errors, full accept queues). Lifecycle lines
// (service start, config, panel session) have neither and are never dropped.
// Loggers raised to info, debug or trace are never limited. No goroutine, no
// timer: buckets refill lazily when the next line arrives.
const (
	connLinesPerService = 20
	connLinesGlobal     = 120
	connLinesMaxKeys    = 4096
)

type lineBucket struct {
	tokens float64
	last   time.Time
}

func (b *lineBucket) take(now time.Time, burst float64, perMinute float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Minutes() * perMinute
		if b.tokens > burst {
			b.tokens = burst
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type serviceLines struct {
	lineBucket
	suppressed uint64
}

type connLineLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	global   lineBucket
	services map[string]*serviceLines
}

func newConnLineLimiter() *connLineLimiter {
	return &connLineLimiter{now: time.Now, services: make(map[string]*serviceLines)}
}

// allow reports whether a per-connection line of service may be written and,
// if so, how many lines of that service were dropped since its last line.
func (c *connLineLimiter) allow(service string) (bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	s := c.services[service]
	if s == nil {
		if len(c.services) >= connLinesMaxKeys {
			c.services = make(map[string]*serviceLines)
		}
		s = &serviceLines{}
		c.services[service] = s
	}
	if !s.take(now, connLinesPerService, connLinesPerService) {
		s.suppressed++
		return false, 0
	}
	if !c.global.take(now, connLinesGlobal, connLinesGlobal) {
		s.suppressed++
		return false, 0
	}
	suppressed := s.suppressed
	s.suppressed = 0
	return true, suppressed
}

func perConnection(fields map[string]any) bool {
	if _, ok := fields["sid"]; ok {
		return true
	}
	return fieldString(fields, "kind") == "listener"
}

func fieldString(fields map[string]any, key string) string {
	v, ok := fields[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
