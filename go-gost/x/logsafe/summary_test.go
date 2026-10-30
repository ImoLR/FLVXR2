package logsafe

import (
	"testing"
	"time"
)

func TestEventSummaryFirstThenPeriodicCount(t *testing.T) {
	var out []string
	now := time.Unix(1_800_000_000, 0)
	s := NewEventSummary(func(line string) { out = append(out, line) })
	s.now = func() time.Time { return now }

	for i := 0; i < 1000; i++ {
		s.Record("http", "🚫 BLOCK HTTP")
	}
	s.Record("tls", "🚫 BLOCK TLS")
	if len(out) != 2 || out[0] != "🚫 BLOCK HTTP（之后同类每 10 分钟汇总一次）" {
		t.Fatalf("want one first line per key, got %q", out)
	}
	now = now.Add(9 * time.Minute)
	s.Record("http", "🚫 BLOCK HTTP")
	if len(out) != 2 {
		t.Fatalf("summary before the interval: %q", out)
	}
	now = now.Add(time.Minute)
	s.Record("http", "🚫 BLOCK HTTP")
	if len(out) != 3 || out[2] != "🚫 BLOCK HTTP ×1001（近 10 分钟）" {
		t.Fatalf("want summary of 1001, got %q", out)
	}
	s.Record("http", "🚫 BLOCK HTTP")
	if len(out) != 3 {
		t.Fatalf("summary right after a summary: %q", out)
	}
}

// Plan 167: own interval, per-call writer, bounded keys.
func TestEventSummaryEveryWithWriterAndKeyBound(t *testing.T) {
	var own, call []string
	now := time.Unix(1_800_000_000, 0)
	s := NewEventSummaryEvery(func(line string) { own = append(own, line) }, time.Hour)
	s.now = func() time.Time { return now }
	writer := func(line string) { call = append(call, line) }

	for i := 0; i < 100; i++ {
		s.RecordWith("svc|readfrom:reset", "readfrom: reset", writer)
	}
	if len(own) != 0 || len(call) != 1 || call[0] != "readfrom: reset（之后同类每 1 小时汇总一次）" {
		t.Fatalf("first line: own=%q call=%q", own, call)
	}
	now = now.Add(59 * time.Minute)
	s.RecordWith("svc|readfrom:reset", "readfrom: reset", writer)
	if len(call) != 1 {
		t.Fatalf("summary before the hour: %q", call)
	}
	now = now.Add(time.Minute)
	s.Record("svc|readfrom:reset", "readfrom: reset") // nil writer → own
	if len(own) != 1 || own[0] != "readfrom: reset ×101（近 60 分钟）" {
		t.Fatalf("hourly summary = %q", own)
	}
	for i := 0; i < maxEventSummaryKeys+10; i++ {
		s.Record(time.Duration(i).String(), "x")
	}
	if len(s.entries) > maxEventSummaryKeys {
		t.Fatalf("keys not bounded: %d", len(s.entries))
	}
}
