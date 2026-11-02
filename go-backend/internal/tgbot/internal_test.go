package tgbot

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackoffSequence(t *testing.T) {
	var got []time.Duration
	var cur time.Duration
	for i := 0; i < 8; i++ {
		cur = nextBackoff(cur, 30*time.Second, 10*time.Minute)
		got = append(got, cur)
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d: %v want %v (all %v)", i, got[i], want[i], got)
		}
	}
}

func TestLogThrottleAndRedaction(t *testing.T) {
	token := "123456789:AAH-secret-token-value-ABCDEFGHIJKLMNOP"
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	var lines []string
	m := New(nil, Hooks{}, Options{Now: func() time.Time { return now }, Logf: func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) }})
	m.token = token
	for i := 0; i < 50; i++ {
		m.noteErr("poll", fmt.Errorf(`Post "https://api.telegram.org/bot%s/getUpdates": dial tcp: i/o timeout`, token))
		now = now.Add(5 * time.Second) // 50 errors over ~4 min
	}
	if len(lines) != 1 {
		t.Fatalf("want 1 line in 10 min, got %d: %v", len(lines), lines)
	}
	now = now.Add(10 * time.Minute)
	m.noteErr("poll", fmt.Errorf("telegram getUpdates: 409 Conflict %s", token))
	if len(lines) != 2 || !strings.Contains(lines[1], "+49 more") {
		t.Fatalf("summary line missing: %v", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, token) || strings.Contains(l, "AAH-secret") {
			t.Fatalf("token logged: %s", l)
		}
	}
	if strings.Contains(m.lastErr, token) {
		t.Fatalf("token in last error: %s", m.lastErr)
	}
}

func TestPollTimeoutHitsSendTime(t *testing.T) {
	// 09:59:40 TW, send at 10:00, a chat bound → poll at most ~21 s.
	now := time.Date(2026, 11, 3, 1, 59, 40, 0, time.UTC)
	m := New(nil, Hooks{}, Options{Now: func() time.Time { return now }})
	m.chats = map[int64]bool{1: true}
	if got := m.pollTimeout(now); got > 21*time.Second || got < 20*time.Second {
		t.Fatalf("timeout %v", got)
	}
	// Already sent today → full 50 s.
	m.lastRun = dayKey(now)
	if got := m.pollTimeout(now); got != 50*time.Second {
		t.Fatalf("timeout after run %v", got)
	}
	// No chat → nothing pending → full 50 s.
	m.lastRun = ""
	m.chats = map[int64]bool{}
	if got := m.pollTimeout(now); got != 50*time.Second {
		t.Fatalf("timeout without chats %v", got)
	}
}

func TestNextDailyMissedSameDay(t *testing.T) {
	m := New(nil, Hooks{}, Options{})
	m.chats = map[int64]bool{1: true}
	m.lastRun = "2026-11-02"
	// 14:00 TW on 11-03: the 10:00 run was missed → due now (in the past).
	now := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	next := m.nextDailyLocked(now)
	if !next.Before(now) || dayKey(next) != "2026-11-03" {
		t.Fatalf("next %v", next)
	}
	m.lastRun = "2026-11-03"
	next = m.nextDailyLocked(now)
	if dayKey(next) != "2026-11-04" || minuteOfDay(next) != 600 {
		t.Fatalf("next after run %v", next.In(time.FixedZone("TW", 8*3600)))
	}
}
