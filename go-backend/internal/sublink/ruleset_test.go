package sublink

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Plan 226: embedded copies are served at once; a day-old copy starts one
// background refresh, and only a valid download replaces it.
func TestRuleSetsServeAndRefresh(t *testing.T) {
	var calls atomic.Int32
	next := make(chan []byte, 4)
	var s RuleSets
	s.Fetch = func(_ context.Context, url string) ([]byte, error) {
		if url != geositeCN {
			return nil, errors.New("not under test")
		}
		calls.Add(1)
		b := <-next
		if b == nil {
			return nil, errors.New("down")
		}
		return b, nil
	}
	wait := func(n int32) {
		t.Helper()
		for i := 0; i < 200; i++ {
			s.mu.Lock()
			busy := s.sets["geosite-cn"].busy
			s.mu.Unlock()
			if calls.Load() == n && !busy {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("calls %d, want %d", calls.Load(), n)
	}
	now := time.Unix(1_800_000_000, 0)
	b0, e0, ok := s.Get("geosite-cn", now)
	if !ok || !validRuleSet(b0) || e0 == "" {
		t.Fatalf("embedded copy: ok=%v len=%d", ok, len(b0))
	}
	if _, _, ok := s.Get("geosite-us", now); ok {
		t.Fatal("unknown rule set served")
	}
	next <- nil // the first refresh fails: the embedded copy stays
	wait(1)
	if b, e, _ := s.Get("geosite-cn", now.Add(time.Hour)); !bytes.Equal(b, b0) || e != e0 {
		t.Fatal("failed refresh replaced the copy")
	}
	if calls.Load() != 1 {
		t.Fatalf("refetched within a day: %d", calls.Load())
	}
	next <- []byte("<html>blocked</html>") // invalid body: ignored
	s.Get("geosite-cn", now.Add(25*time.Hour))
	wait(2)
	if b, _, _ := s.Get("geosite-cn", now.Add(26*time.Hour)); !bytes.Equal(b, b0) {
		t.Fatal("invalid download replaced the copy")
	}
	fresh := append([]byte("SRS\x01"), bytes.Repeat([]byte{7}, 100)...)
	next <- fresh
	s.Get("geosite-cn", now.Add(50*time.Hour))
	wait(3)
	if b, e, _ := s.Get("geosite-cn", now.Add(51*time.Hour)); !bytes.Equal(b, fresh) || e == e0 {
		t.Fatal("valid download not served")
	}
	if b, _, ok := s.Get("geoip-cn", now); !ok || !validRuleSet(b) {
		t.Fatal("geoip-cn embedded copy")
	}
}
