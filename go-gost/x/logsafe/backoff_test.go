package logsafe

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackoffDoublesToCapAndResets(t *testing.T) {
	b := NewBackoff(time.Second, 5*time.Minute)
	if b.Delay() != time.Second {
		t.Fatal(b.Delay())
	}
	var got []time.Duration
	for i := 0; i < 11; i++ {
		got = append(got, b.Failure())
	}
	want := []time.Duration{2, 4, 8, 16, 32, 64, 128, 256, 300, 300, 300}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("failure %d: delay %v, want %vs (all %v)", i+1, got[i], want[i], got)
		}
	}
	if b.Failures() != 11 {
		t.Fatal(b.Failures())
	}
	if d := b.Success(); d != time.Second || b.Delay() != time.Second || b.Failures() != 0 {
		t.Fatal(d, b.Delay(), b.Failures())
	}
	if d := b.Failure(); d != 2*time.Second {
		t.Fatal(d)
	}
}

func TestOneLineCollapsesJoinedNetlinkErrors(t *testing.T) {
	enoent := errors.New("netlink receive: no such file or directory")
	err := fmt.Errorf("conn.Receive: %w", errors.Join(enoent, enoent, enoent))
	got := OneLine(err)
	if strings.Contains(got, "\n") || got != "conn.Receive: netlink receive: no such file or directory; netlink receive: no such file or directory ×2" {
		t.Fatalf("%q", got)
	}
	if OneLine(nil) != "" {
		t.Fatal("nil error")
	}
}

// A failure every second for a whole day logs the first failure and one summary per ten
// minutes: 1 + 144 lines instead of 86 400 (or 3 × that with joined netlink errors).
func TestRoutineLoggerBoundsPerSecondFailuresForADay(t *testing.T) {
	now := time.Unix(0, 0)
	lines := 0
	l := RoutineLogger{now: func() time.Time { return now }, emit: func(string) { lines++ }}
	for i := 0; i < 86400; i++ {
		l.Record("nft quota reconcile", "node", "conn.Receive: netlink receive: no such file or directory")
		now = now.Add(time.Second)
	}
	if lines > 1+144 {
		t.Fatalf("%d lines for a day of per-second failures", lines)
	}
	l.Record("nft quota reconcile", "node", "")
	if lines > 2+144 {
		t.Fatalf("recovery logged more than one line: %d", lines)
	}
}
