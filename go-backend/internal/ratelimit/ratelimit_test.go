package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestBucketRefillRefundAndBound(t *testing.T) {
	now := time.Unix(1000, 0)
	b := &Bucket{Burst: 3, Every: time.Minute, MaxKeys: 100, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if !b.Take("a") {
			t.Fatalf("take %d refused", i)
		}
	}
	if b.Take("a") {
		t.Fatal("4th take allowed")
	}
	b.Refund("a")
	if !b.Take("a") {
		t.Fatal("refund not applied")
	}
	now = now.Add(time.Minute)
	if !b.Take("a") || b.Take("a") {
		t.Fatal("refill should give exactly one token per minute")
	}
	// Bounded: a flood of keys never grows the map past MaxKeys.
	for i := 0; i < 1000; i++ {
		b.Take(fmt.Sprintf("k%d", i))
	}
	if n := b.Len(); n > 100 {
		t.Fatalf("map grew to %d", n)
	}
	// Idle keys are forgotten once their bucket is full again.
	now = now.Add(10 * time.Minute)
	b.Take("fresh")
	if n := b.Len(); n != 1 {
		t.Fatalf("idle keys kept: %d", n)
	}
}

func TestRefusalsSummarizeAtMostOncePerMinute(t *testing.T) {
	now := time.Unix(0, 0)
	r := &Refusals{Name: "t", Now: func() time.Time { return now }}
	r.Note("x") // logs (first)
	for i := 0; i < 1000; i++ {
		r.Note("x")
	}
	if !r.lastLog.Equal(time.Unix(0, 0)) || r.counts["x"] != 1000 {
		t.Fatalf("logged more than once in a minute: %v %v", r.lastLog, r.counts)
	}
	now = now.Add(time.Minute)
	r.Note("y")
	if len(r.counts) != 0 {
		t.Fatal("summary not flushed after a minute")
	}
}
