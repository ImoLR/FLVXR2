package handler

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestLimiter() (*mutationLimiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	return &mutationLimiter{now: c.now}, c
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func mustAcquire(t *testing.T, l *mutationLimiter, user int64, kind mutationKind) {
	t.Helper()
	release, ok := l.acquire(context.Background(), user, kind)
	if !ok {
		t.Fatalf("user %d kind %d refused", user, kind)
	}
	release()
}

func TestMutationLimiterBucket(t *testing.T) {
	captureLog(t)
	l, clock := newTestLimiter()
	for i := 0; i < 30; i++ {
		mustAcquire(t, l, 7, mutationForward)
	}
	if _, ok := l.acquire(context.Background(), 7, mutationForward); ok {
		t.Fatal("31st forward call in the same instant was admitted")
	}
	// Other users and the other resource type have their own buckets.
	mustAcquire(t, l, 8, mutationForward)
	for i := 0; i < 10; i++ {
		mustAcquire(t, l, 7, mutationLanding)
	}
	if _, ok := l.acquire(context.Background(), 7, mutationLanding); ok {
		t.Fatal("11th landing call admitted")
	}
	// Refill: 1 forward token per second, 1 landing token per 3 s.
	clock.add(time.Second)
	mustAcquire(t, l, 7, mutationForward)
	if _, ok := l.acquire(context.Background(), 7, mutationForward); ok {
		t.Fatal("refill gave more than one token per second")
	}
	clock.add(3 * time.Second)
	mustAcquire(t, l, 7, mutationLanding)
	// A long pause refills to the burst, never beyond.
	clock.add(time.Hour)
	for i := 0; i < 30; i++ {
		mustAcquire(t, l, 7, mutationForward)
	}
	if _, ok := l.acquire(context.Background(), 7, mutationForward); ok {
		t.Fatal("bucket exceeded its burst after a long idle")
	}
}

// Calls of one user and one type run one at a time; the next waits.
func TestMutationLimiterOneInFlight(t *testing.T) {
	captureLog(t)
	l, _ := newTestLimiter()
	release, ok := l.acquire(context.Background(), 9, mutationForward)
	if !ok {
		t.Fatal("first call refused")
	}
	// A landing call of the same user is independent.
	mustAcquire(t, l, 9, mutationLanding)
	got := make(chan bool, 1)
	go func() {
		r2, ok := l.acquire(context.Background(), 9, mutationForward)
		if ok {
			r2()
		}
		got <- ok
	}()
	select {
	case <-got:
		t.Fatal("second call ran while the first was in flight")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("queued call was refused after the first finished")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued call never ran")
	}
	// A queued call whose client goes away gives up without running.
	release, _ = l.acquire(context.Background(), 9, mutationForward)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := l.acquire(ctx, 9, mutationForward)
		done <- ok
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if ok := <-done; ok {
		t.Fatal("cancelled queued call was admitted")
	}
	release()
	l.mu.Lock()
	refs := l.users[9].refs[mutationForward]
	l.mu.Unlock()
	if refs != 0 {
		t.Fatalf("refs leaked: %d", refs)
	}
}

// Idle users are evicted, busy ones kept: memory stays bounded by the users
// active in the last minutes.
func TestMutationLimiterEvictsIdleUsers(t *testing.T) {
	captureLog(t)
	l, clock := newTestLimiter()
	for u := int64(1); u <= 1000; u++ {
		mustAcquire(t, l, u, mutationForward)
	}
	busy, ok := l.acquire(context.Background(), 5000, mutationLanding)
	if !ok {
		t.Fatal("busy call refused")
	}
	if n := l.size(); n != 1001 {
		t.Fatalf("tracked %d users", n)
	}
	clock.add(mutationIdleEvict + time.Second)
	mustAcquire(t, l, 1, mutationForward) // triggers the sweep
	if n := l.size(); n != 2 {
		t.Fatalf("after sweep tracked %d users, want 2 (the caller and the busy user)", n)
	}
	busy()
}

// Refusals log one summary line per minute at most.
func TestMutationLimiterLogsSummaryOnly(t *testing.T) {
	buf := captureLog(t)
	l, clock := newTestLimiter()
	for i := 0; i < 30; i++ {
		mustAcquire(t, l, 3, mutationForward)
	}
	try := func() {
		if release, ok := l.acquire(context.Background(), 3, mutationForward); ok {
			release()
		}
	}
	for i := 0; i < 500; i++ {
		try()
		clock.add(10 * time.Millisecond) // 5 s in total, ~5 tokens back
	}
	if n := strings.Count(buf.String(), "mutation rate limit"); n != 1 {
		t.Fatalf("expected one summary line, got %d:\n%s", n, buf.String())
	}
	clock.add(time.Minute)
	for i := 0; i < 100; i++ {
		try()
	}
	if n := strings.Count(buf.String(), "mutation rate limit"); n != 2 {
		t.Fatalf("expected a second summary after a minute, got %d", n)
	}
}

// The wrapper exempts admins, answers refusals in the normal envelope and
// keeps them out of the access log.
func TestLimitMutationWrapper(t *testing.T) {
	buf := captureLog(t)
	h := &Handler{}
	calls := 0
	wrapped := middleware.RequestLog(withClaims(h.limitMutation(mutationLanding, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"code":0}`))
	})))
	do := func(role int) string {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/node/create", strings.NewReader(`{}`))
		req = req.WithContext(context.WithValue(req.Context(), testRoleKey{}, role))
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		return w.Body.String()
	}
	for i := 0; i < 50; i++ {
		if body := do(0); strings.Contains(body, "操作太频繁") {
			t.Fatal("admin was rate limited")
		}
	}
	refused := 0
	for i := 0; i < 20; i++ {
		if body := do(1); strings.Contains(body, "操作太频繁") {
			refused++
			if !strings.Contains(body, `"code":429`) {
				t.Fatalf("refusal not in the envelope: %s", body)
			}
		}
	}
	if refused != 10 || calls != 60 {
		t.Fatalf("refused=%d calls=%d, want 10 refused and 60 handler calls", refused, calls)
	}
	if n := strings.Count(buf.String(), "POST /api/v1/landing/node/create"); n != 60 {
		t.Fatalf("access log has %d lines, want 60 (refusals must not log)", n)
	}
	if n := strings.Count(buf.String(), "mutation rate limit"); n != 1 {
		t.Fatalf("summary lines: %d", n)
	}
	h.SetMutationRateLimitEnabled(false)
	if body := do(1); strings.Contains(body, "操作太频繁") {
		t.Fatal("disabled limiter still refused")
	}
}

type testRoleKey struct{}

// withClaims injects JWT claims the way middleware.JWT does (role from ctx).
func withClaims(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(testRoleKey{}).(int)
		sub := "42"
		if role == 0 {
			sub = "1"
		}
		claims := auth.Claims{Sub: sub, RoleID: role}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, claims)))
	})
}
