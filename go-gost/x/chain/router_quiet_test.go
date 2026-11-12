package chain_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/logger"
	xchain "github.com/go-gost/x/chain"
	ctxvalue "github.com/go-gost/x/ctx"
)

// recLogger records error/warn vs debug lines (plan 247).
type recLogger struct {
	mu     *sync.Mutex
	errors *[]string
	debugs *[]string
}

func newRecLogger() recLogger {
	return recLogger{mu: &sync.Mutex{}, errors: &[]string{}, debugs: &[]string{}}
}
func (l recLogger) add(dst *[]string, s string)             { l.mu.Lock(); *dst = append(*dst, s); l.mu.Unlock() }
func (l recLogger) WithFields(map[string]any) logger.Logger { return l }
func (l recLogger) Trace(args ...any)                       {}
func (l recLogger) Tracef(string, ...any)                   {}
func (l recLogger) Debug(args ...any)                       { l.add(l.debugs, fmt.Sprint(args...)) }
func (l recLogger) Debugf(f string, args ...any)            { l.add(l.debugs, fmt.Sprintf(f, args...)) }
func (l recLogger) Info(args ...any)                        {}
func (l recLogger) Infof(string, ...any)                    {}
func (l recLogger) Warn(args ...any)                        { l.add(l.errors, fmt.Sprint(args...)) }
func (l recLogger) Warnf(f string, args ...any)             { l.add(l.errors, fmt.Sprintf(f, args...)) }
func (l recLogger) Error(args ...any)                       { l.add(l.errors, fmt.Sprint(args...)) }
func (l recLogger) Errorf(f string, args ...any)            { l.add(l.errors, fmt.Sprintf(f, args...)) }
func (l recLogger) Fatal(args ...any)                       {}
func (l recLogger) Fatalf(string, ...any)                   {}
func (l recLogger) GetLevel() logger.LogLevel               { return logger.DebugLevel }
func (l recLogger) IsLevelEnabled(logger.LogLevel) bool     { return true }

func (l recLogger) count(dst *[]string, sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range *dst {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

// closedAddr: a local port with nothing listening (connection refused).
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// Plan 247: a quiet dial (warm pool refill, no user stream) logs its failure
// at debug level; a normal dial still logs the route error.
func TestRouterQuietDialLogsFailureAtDebug(t *testing.T) {
	addr := closedAddr(t)
	rec := newRecLogger()
	r := xchain.NewRouter(chain.LoggerRouterOption(rec))

	if _, err := r.Dial(ctxvalue.ContextWithQuietDial(context.Background()), "tcp", addr); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if n := rec.count(rec.errors, "route(retry="); n != 0 {
		t.Fatalf("quiet dial wrote %d error lines: %v", n, *rec.errors)
	}
	if n := rec.count(rec.debugs, "route(retry="); n == 0 {
		t.Fatalf("quiet dial failure not kept as a debug line: %v", *rec.debugs)
	}

	if _, err := r.Dial(context.Background(), "tcp", addr); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if n := rec.count(rec.errors, "route(retry="); n != 1 {
		t.Fatalf("normal dial: want 1 route error line, got %d: %v", n, *rec.errors)
	}
}
