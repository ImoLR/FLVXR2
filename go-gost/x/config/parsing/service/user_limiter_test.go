package service_test

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
	limiterparser "github.com/go-gost/x/config/parsing/limiter"
	"github.com/go-gost/x/config/parsing/service"
	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
	_ "github.com/go-gost/x/handler/forward/local"
	_ "github.com/go-gost/x/listener/tcp"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
)

// Plan 159: two forward services of one user on one machine reference the
// same user limiter (metadata.userLimiter); together they get its rate, a
// service's own limiter still applies, and replacing / removing the user
// limiter takes effect at once (no per-service cache of the old object).

func sinkServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 16<<10)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					if _, err := c.Write(buf); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func forwardService(t *testing.T, name, target, own, user string) string {
	t.Helper()
	addr := freeAddr(t)
	cfg := &config.ServiceConfig{
		Name:      name,
		Addr:      addr,
		Limiter:   own,
		Handler:   &config.HandlerConfig{Type: "tcp"},
		Listener:  &config.ListenerConfig{Type: "tcp"},
		Forwarder: &config.ForwarderConfig{Nodes: []*config.ForwardNodeConfig{{Name: "target", Addr: target}}},
		Metadata:  map[string]any{"userLimiter": user, "enableStats": false},
	}
	svc, err := service.ParseService(cfg)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	go svc.Serve()
	t.Cleanup(func() { svc.Close() })
	return addr
}

// download reads through addr for d and returns the bytes per second.
func download(t *testing.T, addr string, d time.Duration) float64 {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Errorf("dial %s: %v", addr, err)
		return 0
	}
	defer c.Close()
	var n atomic.Int64
	deadline := time.Now().Add(d)
	_ = c.SetReadDeadline(deadline)
	buf := make([]byte, 32<<10)
	for {
		k, err := c.Read(buf)
		n.Add(int64(k))
		if err != nil {
			if err != io.EOF && !isTimeout(err) {
				t.Errorf("read %s: %v", addr, err)
			}
			break
		}
	}
	return float64(n.Load()) / d.Seconds()
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func registerLimiter(t *testing.T, name, limit string) {
	t.Helper()
	registry.TrafficLimiterRegistry().Unregister(name)
	// The agent's AddLimiters path (socket/limiter.go createLimiter).
	lim := limiterparser.ParseTrafficLimiter(&config.LimiterConfig{Name: name, Limits: []string{"$ " + limit + " " + limit}})
	if err := registry.TrafficLimiterRegistry().Register(name, lim); err != nil {
		t.Fatal(err)
	}
}

func TestUserLimiterSharedAcrossServices(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	if logger.Default() == nil {
		logger.SetDefault(xlogger.Nop())
	}
	sink := sinkServer(t)
	defer sink.Close()
	const user, own = "flvx_user_7_rate", "test_own_rule_limit"
	registerLimiter(t, user, "256KB")
	registerLimiter(t, own, "64KB")
	t.Cleanup(func() {
		registry.TrafficLimiterRegistry().Unregister(user)
		registry.TrafficLimiterRegistry().Unregister(own)
	})
	a := forwardService(t, "svc-a_tcp", sink.Addr().String(), "", user)
	b := forwardService(t, "svc-b_tcp", sink.Addr().String(), own, user)
	c := forwardService(t, "svc-c_tcp", sink.Addr().String(), "", user)

	// Phase 1: A and C (no own limit) share the 256 KiB/s bucket: together
	// ≈ 256 KiB/s (+ one second of burst spread over the window), not 2×.
	const d = 4 * time.Second
	var wg sync.WaitGroup
	var ra, rc float64
	wg.Add(2)
	go func() { defer wg.Done(); ra = download(t, a, d) }()
	go func() { defer wg.Done(); rc = download(t, c, d) }()
	wg.Wait()
	total := ra + rc
	if total < 0.8*256*1024 || total > 1.35*256*1024 {
		t.Fatalf("shared rate: A %.0f + C %.0f = %.0f B/s, want ≈ 262144 (one bucket)", ra, rc, total)
	}
	t.Logf("shared 256 KiB/s: A %.0f B/s + C %.0f B/s = %.0f B/s", ra, rc, total)

	// Phase 1b: B's own 64 KiB/s limit still applies under the user limiter.
	if rb := download(t, b, 2*time.Second); rb > 1.6*64*1024 {
		t.Fatalf("B's own limit not applied: %.0f B/s, want ≈ 65536", rb)
	} else {
		t.Logf("own 64 KiB/s under the user limiter: B %.0f B/s", rb)
	}

	// Phase 2: the user limiter is removed → A is unlimited at once.
	registry.TrafficLimiterRegistry().Unregister(user)
	if r := download(t, a, time.Second); r < 4*256*1024 {
		t.Fatalf("after removal A = %.0f B/s, want unlimited", r)
	}

	// Phase 3: re-created with another rate → applies at once.
	registerLimiter(t, user, "128KB")
	if r := download(t, a, 2*time.Second); r < 0.7*128*1024 || r > 1.6*128*1024 {
		t.Fatalf("re-created 128 KiB/s: A = %.0f B/s", r)
	}
}
