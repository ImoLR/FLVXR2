package logsafe

import (
	"fmt"
	"sync"
	"time"
)

const eventSummaryInterval = 10 * time.Minute

type eventCount struct {
	last    time.Time
	pending uint64
}

// EventSummary logs events that can repeat once per connection (plan 137, QA
// F13): the first event of a key is written at once, later ones are counted and
// written as one summary line at most every ten minutes, when the next event of
// that key arrives (no timer, no goroutine). The zero value is ready for
// concurrent use; keys must come from a small fixed set.
type EventSummary struct {
	mu      sync.Mutex
	entries map[string]*eventCount
	now     func() time.Time
	emit    func(string)
}

// NewEventSummary returns an EventSummary writing through emit (stdout when nil).
func NewEventSummary(emit func(string)) *EventSummary {
	return &EventSummary{emit: emit}
}

// Record counts one event of key; message describes it.
func (s *EventSummary) Record(key, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	emit := s.emit
	if emit == nil {
		emit = func(line string) { fmt.Println(line) }
	}
	entry := s.entries[key]
	if entry == nil {
		if s.entries == nil {
			s.entries = make(map[string]*eventCount)
		}
		s.entries[key] = &eventCount{last: now}
		emit(fmt.Sprintf("%s（之后同类每 10 分钟汇总一次）", message))
		return
	}
	entry.pending++
	if now.Sub(entry.last) >= eventSummaryInterval {
		emit(fmt.Sprintf("%s ×%d（近 %d 分钟）", message, entry.pending, int(now.Sub(entry.last).Minutes())))
		entry.last = now
		entry.pending = 0
	}
}
