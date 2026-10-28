package traffic

import (
	"context"
	"sync/atomic"

	"github.com/go-gost/core/limiter"
	"github.com/go-gost/core/limiter/traffic"
)

// Plan 159 (FLVX per-user 速率): a forward service applies its own limiter
// (base, cached as before) AND the user's shared limiter on this machine. The
// user limiter is the registry wrapper of its name, resolved on every call:
// all services of the user share the one registered object (its service-scope
// "$" bucket), a re-created limiter takes over at once (no per-service cache
// keeping an old object) and a deleted one stops limiting at once.

type combinedTrafficLimiter struct {
	base    traffic.TrafficLimiter
	user    traffic.TrafficLimiter
	lastIn  atomic.Pointer[limiterPair]
	lastOut atomic.Pointer[limiterPair]
}

// limiterPair caches the group of the last (base, user) limiter objects so a
// read/write does not allocate while both stay the same.
type limiterPair struct {
	a, b  traffic.Limiter
	group traffic.Limiter
}

// CombineTrafficLimiters applies base and user together (either may be nil).
func CombineTrafficLimiters(base, user traffic.TrafficLimiter) traffic.TrafficLimiter {
	if user == nil {
		return base
	}
	if base == nil {
		return user
	}
	return &combinedTrafficLimiter{base: base, user: user}
}

func (c *combinedTrafficLimiter) In(ctx context.Context, key string, opts ...limiter.Option) traffic.Limiter {
	return pickLimiter(&c.lastIn, c.base.In(ctx, key, opts...), c.user.In(ctx, key, opts...))
}

func (c *combinedTrafficLimiter) Out(ctx context.Context, key string, opts ...limiter.Option) traffic.Limiter {
	return pickLimiter(&c.lastOut, c.base.Out(ctx, key, opts...), c.user.Out(ctx, key, opts...))
}

func active(l traffic.Limiter) bool {
	return l != nil && l.Limit() > 0
}

func pickLimiter(last *atomic.Pointer[limiterPair], a, b traffic.Limiter) traffic.Limiter {
	switch {
	case !active(a) && !active(b):
		return nil
	case !active(a):
		return b
	case !active(b):
		return a
	}
	if p := last.Load(); p != nil && p.a == a && p.b == b {
		return p.group
	}
	g := newLimiterGroup(a, b)
	last.Store(&limiterPair{a: a, b: b, group: g})
	return g
}
