package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/core/logger"
)

func lines(buf *bytes.Buffer) []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			panic(err)
		}
		out = append(out, m)
	}
	return out
}

func TestDefaultLevelIsWarn(t *testing.T) {
	for _, level := range []logger.LogLevel{"", "bogus"} {
		var buf bytes.Buffer
		l := NewLogger(OutputOption(&buf), LevelOption(level))
		l.Info("access line")
		l.Infof("%s <> %s", "a", "b")
		l.Debug("debug line")
		l.Warn("warn line")
		l.Error("error line")
		got := lines(&buf)
		if len(got) != 2 || got[0]["msg"] != "warn line" || got[1]["msg"] != "error line" {
			t.Fatalf("level %q: want only warn+error, got %v", level, got)
		}
		if !l.IsLevelEnabled(logger.WarnLevel) || l.IsLevelEnabled(logger.InfoLevel) {
			t.Fatalf("level %q: GetLevel=%s", level, l.GetLevel())
		}
	}
}

func TestExplicitInfoStillLogsAccessLines(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(OutputOption(&buf), LevelOption(logger.InfoLevel))
	conn := l.WithFields(map[string]any{"kind": "handler", "service": "1_tls", "sid": "x"})
	conn.Infof("%s <> %s", "1.2.3.4:5", "10.0.0.1:443")
	conn.WithFields(map[string]any{"duration": time.Second}).Infof("%s >< %s", "1.2.3.4:5", "10.0.0.1:443")
	got := lines(&buf)
	if len(got) != 2 || got[0]["level"] != "info" || got[1]["msg"] != "1.2.3.4:5 >< 10.0.0.1:443" {
		t.Fatalf("want 2 access lines, got %v", got)
	}
}

func fakeClock(l logger.Logger) *time.Time {
	now := time.Unix(1_800_000_000, 0)
	l.(*logrusLogger).conns.now = func() time.Time { return now }
	return &now
}

func TestPerConnectionErrorsAreBudgeted(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(OutputOption(&buf))
	now := fakeClock(l)
	svc := l.WithFields(map[string]any{"kind": "service", "service": "80_tls"})
	for i := 0; i < 50; i++ {
		svc.WithFields(map[string]any{"sid": "s"}).Error("relay: unknown command")
	}
	if n := len(lines(&buf)); n != connLinesPerService {
		t.Fatalf("burst: want %d lines, got %d", connLinesPerService, n)
	}
	// Another service has its own budget; lifecycle lines (no sid) are never dropped.
	buf.Reset()
	l.WithFields(map[string]any{"kind": "handler", "service": "81_tls", "sid": "t"}).Warn("other service")
	for i := 0; i < 50; i++ {
		svc.Warn("lifecycle warning")
	}
	if n := len(lines(&buf)); n != 51 {
		t.Fatalf("want 1 + 50 lines, got %d", n)
	}
	// One token per 3 s; the next line reports the 30 dropped ones.
	buf.Reset()
	*now = now.Add(3 * time.Second)
	svc.WithFields(map[string]any{"sid": "s"}).Errorf("dial %s: refused", "x")
	got := lines(&buf)
	if len(got) != 1 || got[0]["suppressed"] != float64(30) {
		t.Fatalf("want 1 line with suppressed=30, got %v", got)
	}
	// Info is below the default level and never counts.
	buf.Reset()
	svc.WithFields(map[string]any{"sid": "s"}).Info("access")
	if buf.Len() != 0 {
		t.Fatalf("info written at default level: %s", buf.String())
	}
}

func TestListenerLinesAreBudgeted(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(OutputOption(&buf))
	fakeClock(l)
	ln := l.WithFields(map[string]any{"kind": "listener", "service": "5_ws"})
	for i := 0; i < 100; i++ {
		ln.Error("websocket: not a websocket handshake")
	}
	if n := len(lines(&buf)); n != connLinesPerService {
		t.Fatalf("want %d lines, got %d", connLinesPerService, n)
	}
}

func TestGlobalBudget(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(OutputOption(&buf))
	fakeClock(l)
	for s := 0; s < 20; s++ {
		svc := l.WithFields(map[string]any{"service": "svc" + string(rune('a'+s)), "sid": "x"})
		for i := 0; i < connLinesPerService; i++ {
			svc.Error("boom")
		}
	}
	if n := len(lines(&buf)); n != connLinesGlobal {
		t.Fatalf("want global cap %d, got %d", connLinesGlobal, n)
	}
}

func TestRaisedLoggerIsNeverBudgeted(t *testing.T) {
	for _, level := range []logger.LogLevel{logger.InfoLevel, logger.DebugLevel} {
		var buf bytes.Buffer
		l := NewLogger(OutputOption(&buf), LevelOption(level))
		fakeClock(l)
		conn := l.WithFields(map[string]any{"service": "1_tls", "sid": "x"})
		for i := 0; i < 100; i++ {
			conn.Error("boom")
		}
		got := lines(&buf)
		if len(got) != 100 {
			t.Fatalf("%s: want 100 lines, got %d", level, len(got))
		}
		if level == logger.DebugLevel {
			if c, _ := got[0]["caller"].(string); !strings.HasPrefix(c, "logger/logger_test.go:") {
				t.Fatalf("caller must point at the call site, got %q", c)
			}
		}
	}
}

func TestLimiterKeyCountIsBounded(t *testing.T) {
	c := newConnLineLimiter()
	for i := 0; i < connLinesMaxKeys+10; i++ {
		c.allow(strings.Repeat("k", 1) + time.Duration(i).String())
	}
	if len(c.services) > connLinesMaxKeys {
		t.Fatalf("services map grew to %d", len(c.services))
	}
}
