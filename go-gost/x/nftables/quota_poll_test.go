//go:build linux

package nftables

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/x/logsafe"
	"golang.org/x/sys/unix"
)

// The node 48 failure: every reconcile fails with ENOENT from a joined netlink batch.
var errNode48 = fmt.Errorf("conn.Receive: %w", errors.Join(
	fmt.Errorf("netlink receive: %w", unix.ENOENT),
	fmt.Errorf("netlink receive: %w", unix.ENOENT),
	fmt.Errorf("netlink receive: %w", unix.ENOENT)))

func TestQuotaPollBacksOffAndLogsOnlyFirstFailureAndRecovery(t *testing.T) {
	var lines []string
	logs := logsafe.NewRoutineLogger(func(s string) { lines = append(lines, s) })
	const failures = 40
	calls := 0
	reconcile := func() error {
		calls++
		if calls <= failures {
			return errNode48
		}
		return nil
	}
	var delays []time.Duration
	wait := func(d time.Duration, stop <-chan struct{}) bool {
		delays = append(delays, d)
		return calls < failures+3
	}
	runQuotaPoll(make(chan struct{}), reconcile, logs, wait)

	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300}
	for i, d := range want {
		if delays[i] != d*time.Second {
			t.Fatalf("delay %d = %v, want %vs (all %v)", i, delays[i], d, delays[:len(want)])
		}
	}
	for _, d := range delays[len(want):failures] {
		if d != quotaPollMaxBackoff {
			t.Fatalf("backoff exceeded the cap: %v", d)
		}
	}
	if delays[failures+1] != time.Second || delays[failures+2] != time.Second {
		t.Fatalf("interval not reset after recovery: %v", delays[failures:])
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "首次失败") || !strings.Contains(lines[0], "IP 限制可能未生效") ||
		!strings.Contains(lines[0], "no such file or directory ×2") || strings.Contains(lines[0], "\n") || !strings.Contains(lines[1], "已恢复") {
		t.Fatalf("logged %d lines: %q", len(lines), lines)
	}
}

func TestQuotaPollStopsWhenStopCloses(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runQuotaPoll(stop, func() error { return errNode48 }, logsafe.NewRoutineLogger(func(string) {}), waitOrStop)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("quota poll did not stop")
	}
}

func TestIsMissingObject(t *testing.T) {
	if !isMissingObject(errNode48) || !isMissingObject(fmt.Errorf("quota gate: %w", errNode48)) {
		t.Fatal("joined ENOENT not recognised")
	}
	if isMissingObject(fmt.Errorf("conn.Receive: %w", unix.EPERM)) || isMissingObject(nil) {
		t.Fatal("unrelated error recognised as missing object")
	}
}

// A manager replaced on reconnect must stop its poll (and never touch the kernel again).
func TestClosedManagerStopsPollAndIgnoresWork(t *testing.T) {
	m := &Manager{
		rules:         map[string]*RuleState{"7_tcp": {ForwardID: 7, Protocol: "tcp", Port: 30007, Quota: RuleQuota{MaxClientIPs: 1}}},
		quotaForwards: map[int64]*quotaForward{7: {id: 7, quota: RuleQuota{MaxClientIPs: 1}, ports: map[string]int{"tcp": 30007}}},
		unrestored:    map[string]*RuleState{},
		healBackoff:   logsafe.NewBackoff(time.Second, healMaxBackoff),
		logs:          logsafe.NewRoutineLogger(func(string) {}),
		now:           time.Now,
	}
	m.mu.Lock()
	m.startQuotaPollLocked()
	done := m.quotaPollDone
	m.mu.Unlock()
	if done == nil {
		t.Fatal("poll not started")
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case <-done:
	default:
		t.Fatal("poll goroutine still running after Close")
	}
	m.Close() // idempotent
	if m.quotaPollStop != nil || len(m.quotaForwards) != 0 {
		t.Fatal("closed manager kept poll state")
	}
	// nil conn: any kernel access would panic.
	if err := m.ReconcileQuota(); err != nil {
		t.Fatal(err)
	}
	if err := m.AddRule(8, 1, 1, 1, "tcp", 30008, "10.0.0.1:80", 0); !errors.Is(err, errManagerClosed) {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.startQuotaPollLocked()
	started := m.quotaPollStop != nil
	m.mu.Unlock()
	if started {
		t.Fatal("closed manager restarted its poll")
	}
}

func TestHealIsSpacedByBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Manager{closed: false, healBackoff: logsafe.NewBackoff(time.Second, healMaxBackoff),
		logs: logsafe.NewRoutineLogger(func(string) {}), now: func() time.Time { return now }}
	// Pretend an attempt just happened: the next one must wait.
	m.healLast, m.healNext = now, now.Add(2*time.Second)
	err := m.healLocked(errNode48)
	if err == nil || !isMissingObject(err) || !strings.Contains(err.Error(), "2s 后重建") {
		t.Fatalf("heal before its backoff elapsed: %v", err)
	}
	m.closed = true
	now = now.Add(time.Hour)
	if err := m.healLocked(errNode48); err != nil {
		t.Fatal("closed manager must not heal:", err)
	}
}
