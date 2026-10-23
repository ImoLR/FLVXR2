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
