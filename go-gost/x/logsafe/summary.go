package logsafe

import (
	"fmt"
	"sync"
	"time"
)

const eventSummaryInterval = 10 * time.Minute

// maxEventSummaryKeys bounds an EventSummary whose keys come from a larger
// set (plan 167: service × error class). Past it, new keys share one
// overflow entry (known keys keep their counts).
const maxEventSummaryKeys = 4096

const eventSummaryOverflowKey = "\x00overflow"

type eventCount struct {
	last    time.Time
	pending uint64
}

// EventSummary logs events that can repeat once per connection (plan 137, QA
// F13): the first event of a key is written at once, later ones are counted and
// written as one summary line at most every ten minutes (or the interval given
// to NewEventSummaryEvery), when the next event of that key arrives (no timer,
// no goroutine). The zero value is ready for concurrent use.
type EventSummary struct {
	mu       sync.Mutex
	entries  map[string]*eventCount
	now      func() time.Time
	emit     func(string)
	interval time.Duration
}

// NewEventSummary returns an EventSummary writing through emit (stdout when nil).
func NewEventSummary(emit func(string)) *EventSummary {
	return &EventSummary{emit: emit}
}

// NewEventSummaryEvery is NewEventSummary with its own summary interval.
func NewEventSummaryEvery(emit func(string), interval time.Duration) *EventSummary {
	return &EventSummary{emit: emit, interval: interval}
}

// Record counts one event of key; message describes it.
func (s *EventSummary) Record(key, message string) {
	s.RecordWith(key, message, nil)
}

// RecordWith is Record with a per-call writer (e.g. a connection's logger);
// nil falls back to the summary's own.
func (s *EventSummary) RecordWith(key, message string, emit func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	if emit == nil {
		emit = s.emit
	}
	if emit == nil {
		emit = func(line string) { fmt.Println(line) }
	}
	interval := s.interval
	if interval <= 0 {
		interval = eventSummaryInterval
	}
	entry := s.entries[key]
	if entry == nil && len(s.entries) >= maxEventSummaryKeys {
		key = eventSummaryOverflowKey
		entry = s.entries[key]
	}
	if entry == nil {
		if s.entries == nil {
			s.entries = make(map[string]*eventCount)
		}
		s.entries[key] = &eventCount{last: now}
		emit(fmt.Sprintf("%s（之后同类每%s汇总一次）", message, summaryIntervalText(interval)))
		return
	}
	entry.pending++
	if now.Sub(entry.last) >= interval {
		emit(fmt.Sprintf("%s ×%d（近 %d 分钟）", message, entry.pending, int(now.Sub(entry.last).Minutes())))
		entry.last = now
		entry.pending = 0
	}
}

func summaryIntervalText(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf(" %d 小时", int(d/time.Hour))
	}
	return fmt.Sprintf(" %d 分钟", int(d.Minutes()))
}
