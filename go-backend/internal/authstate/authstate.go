// Package authstate answers "is the user behind this (validly signed) token
// still allowed in?" for the HTTP API and the browser WebSocket. A JWT lives
// 90 days; without this check a disabled or deleted user kept read access.
//
// Cost: one primary-key lookup (status, role_id) per user per TTL, shared by
// all of that user's requests; admin edits invalidate the entry at once.
package authstate

import (
	"errors"
	"sync"
	"time"
)

// Lookup returns the user's current status and role. found=false means the
// user no longer exists.
type Lookup func(userID int64) (status int, roleID int, found bool, err error)

var (
	ErrUserGone    = errors.New("user deleted")
	ErrUserBlocked = errors.New("user disabled")
	ErrRoleChanged = errors.New("role changed")
)

const (
	defaultTTL = 5 * time.Second
	maxEntries = 4096
)

type entry struct {
	status int
	role   int
	found  bool
	at     time.Time
}

type Cache struct {
	lookup Lookup
	ttl    time.Duration
	now    func() time.Time

	mu        sync.Mutex
	m         map[int64]entry
	lastSweep time.Time
}

func New(lookup Lookup, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Cache{lookup: lookup, ttl: ttl, m: make(map[int64]entry)}
}

func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Check reports whether userID (with the role the token was issued for) may
// still use its token. Database errors fail open (the request itself will hit
// the database next and fail there if it is really down) so a transient
// SQLITE_BUSY never logs everybody out.
func (c *Cache) Check(userID int64, tokenRole int) error {
	if c == nil || c.lookup == nil || userID <= 0 {
		return nil
	}
	now := c.clock()
	c.mu.Lock()
	e, ok := c.m[userID]
	c.mu.Unlock()
	if !ok || now.Sub(e.at) >= c.ttl {
		status, role, found, err := c.lookup(userID)
		if err != nil {
			return nil
		}
		e = entry{status: status, role: role, found: found, at: now}
		c.mu.Lock()
		if len(c.m) >= maxEntries || now.Sub(c.lastSweep) >= time.Minute {
			c.sweepLocked(now)
		}
		c.m[userID] = e
		c.mu.Unlock()
	}
	switch {
	case !e.found:
		return ErrUserGone
	case e.status == 0:
		return ErrUserBlocked
	case e.role != tokenRole:
		return ErrRoleChanged
	}
	return nil
}

// Invalidate drops the cached state of userID (call after admin edits).
func (c *Cache) Invalidate(userID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.m, userID)
	c.mu.Unlock()
}

func (c *Cache) sweepLocked(now time.Time) {
	c.lastSweep = now
	for id, e := range c.m {
		if now.Sub(e.at) >= c.ttl {
			delete(c.m, id)
		}
	}
	if len(c.m) >= maxEntries {
		c.m = make(map[int64]entry)
	}
}
