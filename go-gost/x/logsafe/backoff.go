package logsafe

import (
	"fmt"
	"strings"
	"time"
)

// Backoff is the retry delay of a periodic operation: Min while it succeeds,
// doubled after every consecutive failure, capped at Max. It is not safe for
// concurrent use; keep it in the goroutine (or under the lock) that retries.
type Backoff struct {
	Min, Max time.Duration
	cur      time.Duration
	failures uint64
}

func NewBackoff(min, max time.Duration) *Backoff {
	if min <= 0 {
		min = time.Second
	}
	if max < min {
		max = min
	}
	return &Backoff{Min: min, Max: max, cur: min}
}

// Failure records a failed attempt and returns the delay before the next one.
func (b *Backoff) Failure() time.Duration {
	b.failures++
	if b.cur < b.Min {
		b.cur = b.Min
	}
	if b.cur >= b.Max/2 {
		b.cur = b.Max
	} else {
		b.cur *= 2
	}
	return b.cur
}

// Success resets the delay to Min and returns it.
func (b *Backoff) Success() time.Duration {
	b.failures = 0
	b.cur = b.Min
	return b.cur
}

// Delay is the delay chosen by the last Failure or Success.
func (b *Backoff) Delay() time.Duration {
	if b.cur < b.Min {
		return b.Min
	}
	return b.cur
}

// Failures is the number of consecutive failures.
func (b *Backoff) Failures() uint64 { return b.failures }

// OneLine renders a (possibly joined, multi-line) error as one log line:
// repeated lines are collapsed into "text ×N".
func OneLine(err error) string {
	if err == nil {
		return ""
	}
	var order []string
	counts := make(map[string]int)
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if counts[line] == 0 {
			order = append(order, line)
		}
		counts[line]++
	}
	parts := make([]string, 0, len(order))
	for _, line := range order {
		if counts[line] > 1 {
			line = fmt.Sprintf("%s ×%d", line, counts[line])
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "; ")
}
