// Package ratelimit holds small in-memory helpers for refusing abusive
// traffic cheaply: a keyed token bucket with a hard size cap and idle
// eviction, and a refusal counter that writes at most one summary log line
// per minute (refusals never log per event, so a flood cannot grow the log).
package ratelimit

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bucket is a token bucket per key. Memory is bounded by MaxKeys; a key whose
// bucket has refilled completely is forgotten on the next sweep.
type Bucket struct {
	Burst   float64
	Every   time.Duration // one token back per interval
	MaxKeys int
	Now     func() time.Time

	mu        sync.Mutex
	m         map[string]*bucketState
	lastSweep time.Time
}

type bucketState struct {
	tokens float64
	at     time.Time
}

func (b *Bucket) clock() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bucket) refillLocked(s *bucketState, now time.Time) {
	if elapsed := now.Sub(s.at); elapsed > 0 {
		s.tokens += float64(elapsed) / float64(b.Every)
		if s.tokens > b.Burst {
			s.tokens = b.Burst
		}
	}
	s.at = now
}

// Take consumes one token of key; false means the key is over its limit.
func (b *Bucket) Take(key string) bool {
	if b == nil || key == "" {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	if b.m == nil {
		b.m = make(map[string]*bucketState)
		b.lastSweep = now
	}
	s := b.m[key]
	if s == nil {
		if now.Sub(b.lastSweep) >= time.Minute || len(b.m) >= b.maxKeys() {
			b.sweepLocked(now)
		}
		s = &bucketState{tokens: b.Burst, at: now}
		b.m[key] = s
	} else {
		b.refillLocked(s, now)
	}
	if s.tokens < 1 {
		return false
	}
	s.tokens--
	return true
}

// Refund gives back one token of key (e.g. after a successful login).
func (b *Bucket) Refund(key string) {
	if b == nil || key == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.m[key]; s != nil {
		b.refillLocked(s, b.clock())
		s.tokens++
		if s.tokens > b.Burst {
			s.tokens = b.Burst
		}
	}
}

// Len reports the number of tracked keys (tests).
func (b *Bucket) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.m)
}

func (b *Bucket) maxKeys() int {
	if b.MaxKeys > 0 {
		return b.MaxKeys
	}
	return 10000
}

// sweepLocked forgets keys whose bucket is full again; if the map is still at
// its cap, it forgets the key that was idle longest.
func (b *Bucket) sweepLocked(now time.Time) {
	b.lastSweep = now
	fullAfter := time.Duration(b.Burst * float64(b.Every))
	for k, s := range b.m {
		if now.Sub(s.at) >= fullAfter {
			delete(b.m, k)
		}
	}
	for len(b.m) >= b.maxKeys() {
		var oldestKey string
		var oldest time.Time
		for k, s := range b.m {
			if oldestKey == "" || s.at.Before(oldest) {
				oldestKey, oldest = k, s.at
			}
		}
		delete(b.m, oldestKey)
	}
}

// Refusals counts refused events by reason and logs one summary line at most
// once per Every (default one minute). Nothing is logged while quiet.
type Refusals struct {
	Name  string
	Every time.Duration
	Now   func() time.Time

	mu      sync.Mutex
	counts  map[string]int
	lastLog time.Time
}

func (r *Refusals) Note(reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	if r.counts == nil {
		r.counts = make(map[string]int)
	}
	if len(r.counts) < 32 {
		r.counts[reason]++
	} else {
		r.counts["other"]++
	}
	every := r.Every
	if every <= 0 {
		every = time.Minute
	}
	if !r.lastLog.IsZero() && now.Sub(r.lastLog) < every {
		return
	}
	keys := make([]string, 0, len(r.counts))
	for k := range r.counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, r.counts[k]))
	}
	log.Printf("%s refused: %s (summary, at most one line per minute)", r.Name, strings.Join(parts, " "))
	r.counts = make(map[string]int)
	r.lastLog = now
}
