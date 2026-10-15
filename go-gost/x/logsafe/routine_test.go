package logsafe

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoutineLoggerFailureSummaryRecovery(t *testing.T) {
	now := time.Unix(1000, 0)
	var messages []string
	l := RoutineLogger{now: func() time.Time { return now }, emit: func(s string) { messages = append(messages, s) }}
	for i := 0; i < 100; i++ {
		l.Record("TcpPing", "host:443", "")
	}
	if len(messages) != 0 {
		t.Fatal(messages)
	}
	l.Record("TcpPing", "host:443", "refused")
	for i := 0; i < 100; i++ {
		l.Record("TcpPing", "host:443", fmt.Sprintf("timeout source-port=%d", i))
	}
	now = now.Add(10*time.Minute - time.Nanosecond)
	l.Record("TcpPing", "host:443", "DNS failed")
	if len(messages) != 1 {
		t.Fatalf("changing failures must remain limited: %v", messages)
	}
	now = now.Add(time.Nanosecond)
	l.Record("TcpPing", "host:443", "still failed")
	if len(messages) != 2 || !strings.Contains(messages[1], "102 次（累计 103 次）") {
		t.Fatal(messages)
	}
	l.Record("TcpPing", "host:443", "again")
	l.Record("TcpPing", "host:443", "")
	l.Record("TcpPing", "host:443", "")
	if len(messages) != 3 || !strings.Contains(messages[2], "累计失败 104 次，未汇总 1 次") || len(l.entries) != 0 {
		t.Fatal(messages)
	}
	l.Record("TcpPing", "host:443", "regression")
	if len(messages) != 4 || !strings.Contains(messages[3], "首次失败") {
		t.Fatal(messages)
	}
}

func TestRoutineLoggerConcurrentTargetsAndRedaction(t *testing.T) {
	var messages []string
	l := RoutineLogger{emit: func(s string) { messages = append(messages, s) }}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Record("TcpPing", "host:443", "failed") }()
	}
	wg.Wait()
	l.Record("PipePing", "host:443", "failed")
	l.Record("TcpPing", "other:443", "failed")
	l.Record("ConfigReport", "https://panel/config?secret=private-psk", "https://panel/config?token=private-token\nsecond line")
	if len(messages) != 4 {
		t.Fatal(messages)
	}
	if strings.Contains(strings.Join(messages, ""), "private-") || strings.Contains(messages[3], "\n") {
		t.Fatal(messages)
	}
}
