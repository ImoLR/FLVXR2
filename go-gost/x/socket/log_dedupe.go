package socket

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
)

const repeatedRuntimeLogInterval = time.Minute

type dedupeLogEntry struct {
	fingerprint string
	lastEmitted time.Time
	suppressed  uint64
}

// dedupeLogger emits the first failure immediately, suppresses repetitions for
// a bounded interval, and emits state changes immediately. Call Reset when the
// operation recovers so a later regression is visible as a new first failure.
type dedupeLogger struct {
	mu       sync.Mutex
	interval time.Duration
	entries  map[string]*dedupeLogEntry
	now      func() time.Time
	emit     func(string)
}

func newDedupeLogger(interval time.Duration, emit func(string)) *dedupeLogger {
	if interval <= 0 {
		interval = repeatedRuntimeLogInterval
	}
	if emit == nil {
		emit = func(message string) { fmt.Println(message) }
	}
	return &dedupeLogger{
		interval: interval,
		entries:  make(map[string]*dedupeLogEntry),
		now:      time.Now,
		emit:     emit,
	}
}

func (l *dedupeLogger) Logf(key, fingerprint, format string, args ...interface{}) {
	if l == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[key]
	if entry == nil || entry.fingerprint != fingerprint {
		l.emit(message)
		l.entries[key] = &dedupeLogEntry{fingerprint: fingerprint, lastEmitted: now}
		return
	}
	if now.Sub(entry.lastEmitted) < l.interval {
		entry.suppressed++
		return
	}

	if entry.suppressed > 0 {
		message = fmt.Sprintf("%s（已抑制 %d 条相同日志）", message, entry.suppressed)
	}
	l.emit(message)
	entry.lastEmitted = now
	entry.suppressed = 0
}

func (l *dedupeLogger) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// Recoverf emits one recovery message only when the key was previously in a
// failed state. Routine successes stay silent, while a real recovery remains
// visible and resets suppression for any later regression.
func (l *dedupeLogger) Recoverf(key, format string, args ...interface{}) {
	if l == nil {
		return
	}
	message := fmt.Sprintf(format, args...)

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, failed := l.entries[key]; !failed {
		return
	}
	delete(l.entries, key)
	l.emit(message)
}

func networkErrorFingerprint(prefix string, err error) string {
	if err == nil {
		return prefix + ":none"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		switch {
		case dnsErr.IsNotFound:
			return prefix + ":dns-not-found"
		case dnsErr.IsTimeout:
			return prefix + ":dns-timeout"
		case dnsErr.IsTemporary:
			return prefix + ":dns-temporary"
		default:
			return prefix + ":dns:" + dnsErr.Err
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return prefix + ":timeout"
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return prefix + ":connection-refused"
	case errors.Is(err, syscall.ENETUNREACH):
		return prefix + ":network-unreachable"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return prefix + ":host-unreachable"
	default:
		return fmt.Sprintf("%s:%T:%s", prefix, err, err)
	}
}

var runtimeLogs = newDedupeLogger(repeatedRuntimeLogInterval, nil)
