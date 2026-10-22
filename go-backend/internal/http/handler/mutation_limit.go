package handler

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
)

// Per-user rate limit on normal users' mutating calls for rules (forward) and
// landing nodes. It protects nodes from create/delete churn (config pushes,
// logs) without ever biting a human clicking: a token bucket admits the call,
// then calls of one user and one resource type run one at a time (later ones
// queue briefly). Admins are exempt. A refusal does no DB work and writes no
// per-request log line; at most one summary line per minute is logged.

type mutationKind int

const (
	mutationForward mutationKind = iota
	mutationLanding
	mutationKinds
)

type mutationRate struct {
	burst float64       // bucket size
	every time.Duration // one token back per interval
}

var mutationRates = [mutationKinds]mutationRate{
	// Burst 30 covers importing a user's rules in one go; afterwards 1 call/s
	// (3600/h) still far exceeds any manual use.
	mutationForward: {burst: 30, every: time.Second},
	// Landing nodes restart nothing but rewrite the server's Core config.
	mutationLanding: {burst: 10, every: 3 * time.Second},
}

const (
	mutationQueueWait   = 20 * time.Second // wait for the user's previous call of the same type
	mutationIdleEvict   = 10 * time.Minute // drop users idle this long (bucket is full by then)
	mutationSweepEvery  = time.Minute
	mutationLogEvery    = time.Minute
	mutationLogUsers    = 16
	mutationRefusedMsg  = "操作太频繁，请稍后再试"
	mutationRefusedHTTP = 429
)

type mutationUser struct {
	tokens [mutationKinds]float64
	refill [mutationKinds]time.Time
	slot   [mutationKinds]chan struct{} // capacity 1: the running call
	refs   [mutationKinds]int           // admitted calls running or queued
	seen   time.Time
}

type mutationLimiter struct {
	mu           sync.Mutex
	users        map[int64]*mutationUser
	lastSweep    time.Time
	refused      int
	refusedUsers map[int64]struct{}
	lastLog      time.Time
	disabled     bool
	now          func() time.Time
}

func (l *mutationLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// admit takes one token for (user, kind). It returns the user's state with
// refs held, or nil when the bucket is empty.
func (l *mutationLimiter) admit(userID int64, kind mutationKind) *mutationUser {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	if l.users == nil {
		l.users = map[int64]*mutationUser{}
		l.lastSweep = now
	}
	if now.Sub(l.lastSweep) >= mutationSweepEvery {
		l.sweepLocked(now)
	}
	u := l.users[userID]
	if u == nil {
		u = &mutationUser{}
		for k := range u.slot {
			u.slot[k] = make(chan struct{}, 1)
			u.tokens[k] = mutationRates[k].burst
			u.refill[k] = now
		}
		l.users[userID] = u
	}
	u.seen = now
	rate := mutationRates[kind]
	if elapsed := now.Sub(u.refill[kind]); elapsed > 0 {
		u.tokens[kind] += float64(elapsed) / float64(rate.every)
		if u.tokens[kind] > rate.burst {
			u.tokens[kind] = rate.burst
		}
		u.refill[kind] = now
	}
	if u.tokens[kind] < 1 {
		l.noteRefusalLocked(userID, now)
		return nil
	}
	u.tokens[kind]--
	u.refs[kind]++
	return u
}

func (l *mutationLimiter) done(u *mutationUser, kind mutationKind) {
	l.mu.Lock()
	u.refs[kind]--
	u.seen = l.clock()
	l.mu.Unlock()
}

// sweepLocked evicts users with nothing running or queued that have been idle
// long enough for their buckets to be full again, keeping the map bounded by
// the users active in the last few minutes.
func (l *mutationLimiter) sweepLocked(now time.Time) {
	l.lastSweep = now
	for id, u := range l.users {
		busy := false
		for _, n := range u.refs {
			if n > 0 {
				busy = true
				break
			}
		}
		if !busy && now.Sub(u.seen) >= mutationIdleEvict {
			delete(l.users, id)
		}
	}
}

func (l *mutationLimiter) noteRefusalLocked(userID int64, now time.Time) {
	l.refused++
	if l.refusedUsers == nil {
		l.refusedUsers = map[int64]struct{}{}
	}
	if len(l.refusedUsers) < mutationLogUsers {
		l.refusedUsers[userID] = struct{}{}
	}
	if !l.lastLog.IsZero() && now.Sub(l.lastLog) < mutationLogEvery {
		return
	}
	ids := make([]int64, 0, len(l.refusedUsers))
	for id := range l.refusedUsers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	log.Printf("mutation rate limit: refused %d call(s) from user(s) %v (summary, at most one line per minute)", l.refused, ids)
	l.refused = 0
	l.refusedUsers = map[int64]struct{}{}
	l.lastLog = now
}

// acquire admits a call and waits for the user's previous call of the same
// kind to finish. ok=false means refused (bucket empty or queue wait too
// long) or the client went away (ctx done).
func (l *mutationLimiter) acquire(ctx context.Context, userID int64, kind mutationKind) (release func(), ok bool) {
	u := l.admit(userID, kind)
	if u == nil {
		return nil, false
	}
	timer := time.NewTimer(mutationQueueWait)
	defer timer.Stop()
	select {
	case u.slot[kind] <- struct{}{}:
		return func() {
			<-u.slot[kind]
			l.done(u, kind)
		}, true
	case <-timer.C:
		l.mu.Lock()
		l.noteRefusalLocked(userID, l.clock())
		l.mu.Unlock()
	case <-ctx.Done():
	}
	l.done(u, kind)
	return nil, false
}

// size reports how many users the limiter currently tracks (tests/metrics).
func (l *mutationLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.users)
}

// SetMutationRateLimitEnabled turns the per-user mutation rate limit on or off
// (on by default). Tests that drive parallel creates of one user switch it off
// to exercise the database-level limit checks directly.
func (h *Handler) SetMutationRateLimitEnabled(enabled bool) {
	h.mutations.mu.Lock()
	h.mutations.disabled = !enabled
	h.mutations.mu.Unlock()
}

func (h *Handler) mutationLimitDisabled() bool {
	h.mutations.mu.Lock()
	defer h.mutations.mu.Unlock()
	return h.mutations.disabled
}

// limitMutation wraps a mutating route of kind for normal users.
func (h *Handler) limitMutation(kind mutationKind, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, roleID, err := userRoleFromRequest(r)
		if err != nil || roleID == 0 || h.mutationLimitDisabled() {
			next(w, r)
			return
		}
		release, ok := h.mutations.acquire(r.Context(), userID, kind)
		if !ok {
			middleware.SuppressRequestLog(w)
			if r.Context().Err() != nil {
				return
			}
			response.WriteJSON(w, response.Err(mutationRefusedHTTP, mutationRefusedMsg))
			return
		}
		defer release()
		next(w, r)
	}
}
