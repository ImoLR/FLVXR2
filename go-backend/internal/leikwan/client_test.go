package leikwan_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
)

func TestOverviewSingleflightAndCache(t *testing.T) {
	f := testapi.New()
	f.SeedSix()
	s := httptest.NewServer(f)
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.Overview(context.Background(), f.Token, 39)
			if err != nil || len(v.Forwards) != 6 || v.Options.ForwardLimit != 8 {
				t.Errorf("overview: %+v %v", v, err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 10; i++ {
		_, _ = c.Overview(context.Background(), f.Token, 39)
	}
	n, w := f.Counts()
	if n != 2 || w != 0 {
		t.Fatalf("cache/singleflight performed %d requests, %d writes", n, w)
	}
	c.Invalidate(f.Token, 39)
	if _, err := c.Overview(context.Background(), f.Token, 39); err != nil {
		t.Fatal(err)
	}
	n, _ = f.Counts()
	if n != 4 {
		t.Fatalf("invalidate requests %d", n)
	}
}

func TestRetryAfterAndSharedTokenRateLimit(t *testing.T) {
	f := testapi.New()
	f.SeedSix()
	f.FailNext = 429
	f.RetryAfter = "1"
	s := httptest.NewServer(f)
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	_, _, err := c.GetForward(context.Background(), f.Token, 263)
	var apiErr *leikwan.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 429 {
		t.Fatalf("want 429, got %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, err = c.GetForward(ctx, f.Token, 264)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rate wait not canceled: %v", err)
	}
	n, _ := f.Counts()
	if n != 1 {
		t.Fatalf("Retry-After ignored: %d", n)
	}
	start := time.Now()
	if _, _, err = c.GetForward(context.Background(), f.Token, 264); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 850*time.Millisecond {
		t.Fatal("Retry-After wait too short")
	}
	start = time.Now()
	if _, _, err = c.GetForward(context.Background(), f.Token, 263); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 550*time.Millisecond {
		t.Fatal("shared account limiter did not space requests")
	}
}

func TestCancelledWaitersDoNotLeakGoroutines(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(started) }); <-r.Context().Done() }))
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = c.Overview(ctx, "secret", 39) }()
	<-started
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, _ = c.Overview(ctx, "secret", 39)
		}()
	}
	wg.Wait()
	cancel()
	<-done
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before+3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > before+3 {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
	t.Logf("100 canceled waiters: goroutines before=%d after=%d", before, after)
}

func TestBoundedResponseAndSecretSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{{"oversized", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 2<<20+1))) }}, {"upstream error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":{"code":"token-secret-value","message":"token-secret-value"}}`))
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(tc.handler)
			defer s.Close()
			c := leikwan.NewClient(s.URL, nil)
			_, _, err := c.GetForward(context.Background(), "token-secret-value", 1)
			if err == nil || strings.Contains(err.Error(), "token-secret-value") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
}

func TestForwardETagIdempotencyAndErrors(t *testing.T) {
	f := testapi.New()
	s := httptest.NewServer(f)
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	body := leikwan.CreateRequest{RouteID: 21, Transport: "tcp", Target: leikwan.Target{Source: "manual", Host: "8.8.8.8", Port: 443}}
	f.FailCreateAfterSave = true
	if _, _, err := c.CreateForward(context.Background(), f.Token, 39, "retry-create-1", body); err == nil {
		t.Fatal("want ambiguous failure")
	}
	forward, etag, err := c.CreateForward(context.Background(), f.Token, 39, "retry-create-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if f.Stored != 1 {
		t.Fatal("idempotent retry created duplicate")
	}
	updated, newTag, err := c.PatchTarget(context.Background(), f.Token, forward.ID, etag, leikwan.Target{Source: "manual", Host: "1.1.1.1", Port: 8443})
	if err != nil || updated.Target.Port != 8443 || newTag == etag {
		t.Fatalf("patch: %+v %v", updated, err)
	}
	if err = c.DeleteForward(context.Background(), f.Token, forward.ID, etag); err == nil {
		t.Fatal("stale delete accepted")
	}
	if err = c.DeleteForward(context.Background(), f.Token, forward.ID, newTag); err != nil {
		t.Fatal(err)
	}
	if f.Stored != 1 {
		t.Fatal("delete reset stored quota")
	}
}
