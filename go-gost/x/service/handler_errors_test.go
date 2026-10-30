package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-gost/x/logsafe"
)

func TestHandlerErrorClass(t *testing.T) {
	cases := map[string]string{
		"readfrom tcp 1.2.3.4:5->6.7.8.9:10: read tcp [2408::1]:1->[2001::2]:3: read: connection reset by peer": "readfrom:reset",
		"readfrom tcp 1.2.3.4:5->6.7.8.9:10: read: connection timed out":                                        "readfrom:timeout",
		"dial tcp 9.9.9.9:443: i/o timeout":                                                                     "dial:timeout",
		"relay: unknown command":                                                                                "relay:unknown-command",
		"writeto tcp 1.2.3.4:5->6.7.8.9:10: write: broken pipe":                                                 "writeto:broken-pipe",
		"socks5: 3 Network Unreachable":                                                                         "socks5:unreachable",
		"EOF":                                                                                                   "other:eof",
		"something new":                                                                                         "other:other",
	}
	for msg, want := range cases {
		if got := handlerErrorClass(errors.New(msg)); got != want {
			t.Errorf("%q → %q, want %q", msg, got, want)
		}
	}
	if got := handlerErrorClass(fmt.Errorf("readfrom tcp a->b: %w", errConnectionBlocked)); got != "blocked" {
		t.Errorf("wrapped blocked → %q", got)
	}
	if got := handlerErrorClass(errors.New("readfrom tcp a->b: connection blocked")); got != "blocked" {
		t.Errorf("text blocked → %q", got)
	}
}

// Plan 167: one line per service × class, not one per failed connection;
// blocked connections are left to the BLOCK summary.
func TestRecordHandlerErrorSummarizes(t *testing.T) {
	handlerErrLogs = logsafe.NewEventSummary(nil)
	var lines []string
	emit := func(line string) { lines = append(lines, line) }
	for i := 0; i < 200; i++ {
		recordHandlerError("12_8_3_tcp", fmt.Errorf("readfrom tcp 10.0.0.%d:%d->1.1.1.1:443: read: connection reset by peer", i%250, 40000+i), emit)
		recordHandlerError("12_8_3_tcp", fmt.Errorf("readfrom tcp 10.0.0.1:1->1.1.1.1:443: %w", errConnectionBlocked), emit)
		recordHandlerError("13_8_3_tls", errors.New("relay: unknown command"), emit)
	}
	recordHandlerError("12_8_3_tcp", errors.New("dial tcp 1.1.1.1:443: i/o timeout"), emit)
	// Unknown causes: a new kind is written at once, the same kind with other
	// addresses is not.
	recordHandlerError("12_8_3_tcp", errors.New("dial tcp: lookup a.example on 127.0.0.53:53: server misbehaving"), emit)
	recordHandlerError("12_8_3_tcp", errors.New("dial tcp: lookup b2.example on 127.0.0.53:53: server misbehaving"), emit)
	recordHandlerError("12_8_3_tcp", errors.New("dial tcp 9.9.9.9:443: setsockopt: invalid argument"), emit)
	if len(lines) != 6 || !strings.Contains(lines[0], "connection reset") || !strings.Contains(lines[1], "unknown command") || !strings.Contains(lines[2], "i/o timeout") ||
		!strings.Contains(lines[3], "a.example") || !strings.Contains(lines[4], "b2.example") || !strings.Contains(lines[5], "invalid argument") {
		t.Fatalf("lines = %q", lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "之后同类每 10 分钟汇总一次") {
			t.Fatalf("first line without the summary note: %q", line)
		}
	}
}
