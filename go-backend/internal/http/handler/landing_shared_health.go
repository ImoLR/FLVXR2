package handler

// Plan 271 「妙妙屋共享离线闪烁」.
//
// Before: any failed request of a sync round (one status poll, one push, one
// user-limits PUT) wrote the single landing_integration.sync_error, and the
// views copied it onto EVERY shared server (离线) and every shared node
// (「同步异常」) until a fully clean round; the transport breaker then failed
// the other servers' due requests for 15 s, which wrote their own error too.
// Nothing was logged, and every transport error read 「妙妙屋连接失败」.
//
// Now:
//   - each request key (shares / hosts / status:N / push:N / user-limits:N /
//     probe:N) has a failure streak here (in memory, own mutex);
//   - every failure is logged with key, server, class (dns / timeout / refused
//     / reset / tls / http <code> / breaker-open …) and duration — one line per
//     key+class per minute, one line when the key works again, breaker skips
//     aggregated into one line per minute;
//   - the views show a transient (network / 5xx / breaker) problem only after
//     it has lasted sharedOfflineAfter (3 min, the same threshold as the
//     SharedLastSyncAt freshness): a single failed request flips nothing, a
//     server that keeps failing shows 离线 within ~3 min, and one server's
//     problem never marks the others;
//   - the stored errors are written exactly as before (the delete gate in
//     repository_landing_lifecycle.go still blocks on them); a server's
//     transient error is cleared when all its requests work again.

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

const (
	// sharedOfflineAfter: a shared server is 离线 when it had no successful
	// status poll for this long, and a transient sync problem is shown only
	// after it lasted this long.
	sharedOfflineAfter = 3 * time.Minute
	sharedSyncLogEvery = time.Minute
	classBreakerOpen   = "breaker-open"
	sharedStreakMsgCap = 8
)

type sharedKeyHealth struct {
	since     time.Time // first failure (or skip) of the streak
	failures  int       // real requests that failed
	class     string    // class of the last real failure (or breaker-open)
	transient bool      // every failure of the streak may clear by itself
	msgs      map[string]bool
	logged    bool // a failure line was written for this streak
}

type sharedSyncHealth struct {
	mu        sync.Mutex
	now       func() time.Time
	keys      map[string]*sharedKeyHealth
	srvMsgs   map[int64]map[string]bool // transient texts a server may still have stored
	loggedAt  map[string]time.Time      // key|class → last failure line
	skipped   map[string]int            // breaker skips by request kind since the last line
	skipN     int
	skipCause string
	skipLogAt time.Time
}

func newSharedSyncHealth() *sharedSyncHealth {
	return &sharedSyncHealth{now: time.Now, keys: map[string]*sharedKeyHealth{}, srvMsgs: map[int64]map[string]bool{}, loggedAt: map[string]time.Time{}, skipped: map[string]int{}}
}

func (h *Handler) sharedHealth() *sharedSyncHealth {
	h.sharedHealthOnce.Do(func() {
		if h.sharedHealthSt == nil {
			h.sharedHealthSt = newSharedSyncHealth()
		}
	})
	return h.sharedHealthSt
}

// sharedKeyParts: "status:12" → ("status", "12"); "shares" → ("shares", "-").
func sharedKeyParts(key string) (string, string) {
	if kind, id, ok := strings.Cut(key, ":"); ok {
		return kind, id
	}
	return key, "-"
}

func (s *sharedKeyHealth) addMsg(msg string) {
	if msg == "" || s.msgs[msg] {
		return
	}
	if s.msgs == nil {
		s.msgs = map[string]bool{}
	}
	if len(s.msgs) < sharedStreakMsgCap {
		s.msgs[msg] = true
	}
}

// sharedServerKeyID: the landing_server id of a per-server key that decides
// the server's state (status / push / user-limits), else 0.
func sharedServerKeyID(key string) int64 {
	kind, server := sharedKeyParts(key)
	if kind != "status" && kind != "push" && kind != "user-limits" {
		return 0
	}
	id, err := strconv.ParseInt(server, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// noteLocked adds msg to the streak and, while the streak is transient, to the
// texts its server may still have stored.
func (s *sharedSyncHealth) noteLocked(key string, k *sharedKeyHealth, msg string) {
	k.addMsg(msg)
	id := sharedServerKeyID(key)
	if id == 0 || msg == "" || !k.transient {
		return
	}
	m := s.srvMsgs[id]
	if m == nil {
		m = map[string]bool{}
		s.srvMsgs[id] = m
	}
	if len(m) < sharedStreakMsgCap {
		m[msg] = true
	}
}

func (s *sharedSyncHealth) streak(key string, now time.Time) *sharedKeyHealth {
	k := s.keys[key]
	if k == nil {
		k = &sharedKeyHealth{since: now, transient: true}
		s.keys[key] = k
	}
	return k
}

// fail records a real request of key that failed after dur; msg is the text
// the caller stores/shows for it.
func (s *sharedSyncHealth) fail(key string, err error, msg string, dur time.Duration) {
	if s == nil || err == nil {
		return
	}
	class := landingcustom.ErrorClass(err)
	transient := landingcustom.IsTransient(err)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	k := s.streak(key, now)
	k.failures++
	k.class = class
	k.transient = k.transient && transient
	s.noteLocked(key, k, msg)
	lk := key + "|" + class
	if at, ok := s.loggedAt[lk]; ok && now.Sub(at) < sharedSyncLogEvery {
		return
	}
	s.loggedAt[lk] = now
	k.logged = true
	kind, server := sharedKeyParts(key)
	log.Printf("[shared-sync] fail req=%s server=%s class=%s dur=%s streak=%d/%s: %s",
		kind, server, class, dur.Round(time.Millisecond), k.failures, now.Sub(k.since).Round(time.Second), msg)
}

// skip records a request of key that was not sent: the transport breaker is
// open (breaker=true, cause = the class that opened it) or the key backs off
// after its own failure. msg is the text the caller stores for it.
func (s *sharedSyncHealth) skip(key, msg string, breaker bool, cause string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	k := s.keys[key]
	if k == nil {
		if !breaker {
			return // own backoff without a recorded failure (restart): nothing new
		}
		k = s.streak(key, now)
		k.class = classBreakerOpen
	}
	s.noteLocked(key, k, msg)
	if !breaker {
		return
	}
	kind, _ := sharedKeyParts(key)
	s.skipped[kind]++
	s.skipN++
	if cause != "" {
		s.skipCause = cause
	}
	if s.skipLogAt.IsZero() {
		s.skipLogAt = now // the first skip opens the window, logged a minute later
	} else if now.Sub(s.skipLogAt) >= sharedSyncLogEvery {
		s.flushSkipsLocked(now, "")
	}
}

func (s *sharedSyncHealth) flushSkipsLocked(now time.Time, suffix string) {
	if s.skipN == 0 {
		s.skipLogAt = time.Time{}
		return
	}
	parts := make([]string, 0, len(s.skipped))
	for kind, n := range s.skipped {
		parts = append(parts, fmt.Sprintf("%s %d", kind, n))
	}
	sort.Strings(parts)
	window := "since the breaker opened"
	if !s.skipLogAt.IsZero() {
		window = "in " + now.Sub(s.skipLogAt).Round(time.Second).String()
	}
	cause := s.skipCause
	if cause == "" {
		cause = "?"
	}
	log.Printf("[shared-sync] fail req=* server=- class=%s: %d requests skipped %s (%s), cause class=%s%s",
		classBreakerOpen, s.skipN, window, strings.Join(parts, ", "), cause, suffix)
	s.skipped, s.skipN, s.skipLogAt = map[string]int{}, 0, time.Time{}
	if suffix == "" {
		s.skipLogAt = now
	}
}

// ok records a request of key that worked. When that was the server's last
// failing request it returns the transient texts the server may still have
// stored (the caller clears the stored error if it is one of them).
func (s *sharedSyncHealth) ok(key string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.skipN > 0 {
		s.flushSkipsLocked(now, "; breaker closed")
	} else {
		s.skipLogAt = time.Time{}
	}
	k := s.keys[key]
	if k == nil {
		return nil
	}
	delete(s.keys, key)
	for lk := range s.loggedAt {
		if strings.HasPrefix(lk, key+"|") {
			delete(s.loggedAt, lk)
		}
	}
	if k.logged {
		kind, server := sharedKeyParts(key)
		log.Printf("[shared-sync] ok req=%s server=%s after %s (%d failures, last class=%s)",
			kind, server, now.Sub(k.since).Round(time.Second), k.failures, k.class)
	}
	id := sharedServerKeyID(key)
	if id == 0 || s.serverFailingLocked(id) {
		return nil
	}
	out := make([]string, 0, len(s.srvMsgs[id]))
	for m := range s.srvMsgs[id] {
		out = append(out, m)
	}
	delete(s.srvMsgs, id)
	sort.Strings(out)
	return out
}

// serverKeys: the per-server request keys that decide a shared server's state.
func sharedServerKeys(id int64) [3]string {
	n := strconv.FormatInt(id, 10)
	return [3]string{"status:" + n, "push:" + n, "user-limits:" + n}
}

// serverFailing: whether any of the server's requests is failing now.
func (s *sharedSyncHealth) serverFailing(id int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverFailingLocked(id)
}

func (s *sharedSyncHealth) serverFailingLocked(id int64) bool {
	for _, key := range sharedServerKeys(id) {
		if s.keys[key] != nil {
			return true
		}
	}
	return false
}

// hideServerError: msg (the server's stored error) is a transient problem of
// a streak younger than sharedOfflineAfter — not shown yet.
func (s *sharedSyncHealth) hideServerError(id int64, msg string, now time.Time) bool {
	if s == nil || msg == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := s.srvMsgs[id][msg]
	for _, key := range sharedServerKeys(id) {
		k := s.keys[key]
		if k == nil {
			continue
		}
		if !k.transient || now.Sub(k.since) >= sharedOfflineAfter {
			return false // a verdict, or a problem that has lasted: show it
		}
		if k.msgs[msg] {
			known = true
		}
	}
	return known
}

// globalShown: the integration-wide requests (shares / hosts) fail in a way
// every shared server should show: a verdict at once, a transient problem
// after sharedOfflineAfter.
func (s *sharedSyncHealth) globalShown(now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range []string{"shares", "hosts"} {
		if k := s.keys[key]; k != nil && (!k.transient || now.Sub(k.since) >= sharedOfflineAfter) {
			return true
		}
	}
	return false
}

// sharedSyncDisplay is what the views show for a shared server: the error
// text ("" = none) and whether its last successful status poll is too old.
// online = Core running && !stale && text == "".
func (h *Handler) sharedSyncDisplay(s *model.LandingServer, integrationErr string, now time.Time) (string, bool) {
	return sharedSyncDisplayWith(h.sharedHealth(), s, integrationErr, now)
}

// sharedSyncDisplayWith: sharedSyncDisplay with an explicit health (nil = no
// streak known: stored errors show as stored).
func sharedSyncDisplayWith(health *sharedSyncHealth, s *model.LandingServer, integrationErr string, now time.Time) (string, bool) {
	stale := s.SharedLastSyncAt <= now.Add(-sharedOfflineAfter).UnixMilli()
	msg := s.SharedSyncError
	if msg != "" && !stale && health.hideServerError(s.ID, msg, now) {
		msg = ""
	}
	if msg == "" && integrationErr != "" && (stale || health.globalShown(now)) {
		msg = integrationErr
	}
	return msg, stale
}

// sharedRecovered clears a server's stored transient error once none of its
// requests fails any more (texts = what the recovered streak stored).
// Called with sharedLandingMu held.
func (h *Handler) sharedRecovered(key string, texts []string) {
	id := sharedServerKeyID(key)
	if id == 0 || h.repo == nil {
		return
	}
	for _, t := range texts {
		_ = h.repo.ClearSharedSyncErrorIf(id, t)
	}
}

// sharedLayerSyncError: the server's own sync problem for the 路由规则 apply
// state (same 3 min rule; the integration-wide error is not repeated there).
func (h *Handler) sharedLayerSyncError(s *model.LandingServer) string {
	text, _ := h.sharedSyncDisplay(s, "", time.Now())
	return text
}
