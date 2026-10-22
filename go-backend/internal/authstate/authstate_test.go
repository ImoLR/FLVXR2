package authstate

import (
	"errors"
	"testing"
	"time"
)

func TestCacheTTLInvalidateAndFailOpen(t *testing.T) {
	calls := 0
	status, role, found := 1, 1, true
	var dbErr error
	c := New(func(int64) (int, int, bool, error) {
		calls++
		return status, role, found, dbErr
	}, time.Hour)
	if err := c.Check(7, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		_ = c.Check(7, 1)
	}
	if calls != 1 {
		t.Fatalf("lookups %d, want 1 (cached)", calls)
	}
	status = 0
	if err := c.Check(7, 1); err != nil {
		t.Fatal("cached entry should still allow until invalidated")
	}
	c.Invalidate(7)
	if err := c.Check(7, 1); !errors.Is(err, ErrUserBlocked) {
		t.Fatalf("disabled: %v", err)
	}
	status, found = 1, false
	c.Invalidate(7)
	if err := c.Check(7, 1); !errors.Is(err, ErrUserGone) {
		t.Fatalf("deleted: %v", err)
	}
	found = true
	c.Invalidate(7)
	if err := c.Check(7, 0); !errors.Is(err, ErrRoleChanged) {
		t.Fatalf("role changed: %v", err)
	}
	dbErr = errors.New("busy")
	c.Invalidate(7)
	if err := c.Check(7, 1); err != nil {
		t.Fatalf("db error must fail open: %v", err)
	}
}
