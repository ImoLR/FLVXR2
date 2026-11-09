package repo

import (
	"fmt"
	"sort"
	"time"

	"go-backend/internal/store/model"
)

// Plan 243: a share that silently disappears from 妙妙屋 (its server deleted,
// the share row dropped) is handled like an explicit `status=revoked`, but only
// with evidence that survives a Custom hiccup:
//   - missing from ≥3 consecutive fully successful list rounds spanning ≥3 min,
//     or its status poll answered 404 not_found ≥3 times in a row spanning ≥3 min;
//   - never when more than half of the known shares are suspect at once, or the
//     list came back empty while FLVX knew ≥1 share (that is a Custom-side
//     problem, not N deletions): those are held with an error + admin ⚠.
//
// The marks live in memory only. A panel restart forgets them, which can only
// delay a revocation, never cause one.
const (
	sharedVanishRounds = 3
	sharedVanishSpan   = 3 * time.Minute
)

type sharedVanishMark struct {
	count int
	since time.Time
}

func (m sharedVanishMark) qualified(now time.Time) bool {
	return m.count >= sharedVanishRounds && now.Sub(m.since) >= sharedVanishSpan
}

// SharedVanishTracker is keyed by landing_server id. Not safe for concurrent
// use: the handler keeps it under sharedLandingMu.
type SharedVanishTracker struct {
	missing  map[int64]sharedVanishMark
	notFound map[int64]sharedVanishMark
}

func NewSharedVanishTracker() *SharedVanishTracker {
	return &SharedVanishTracker{missing: map[int64]sharedVanishMark{}, notFound: map[int64]sharedVanishMark{}}
}

// NotFound records one real status answer 404 not_found for a shared server.
func (t *SharedVanishTracker) NotFound(serverID int64, now time.Time) {
	m := t.notFound[serverID]
	if m.count == 0 {
		m.since = time.UnixMilli(now.UnixMilli()) // list rounds use ms
	}
	m.count++
	t.notFound[serverID] = m
}

// StatusOK: a successful status poll ends a not_found streak.
func (t *SharedVanishTracker) StatusOK(serverID int64) { delete(t.notFound, serverID) }

// NotFoundCount is the current not_found streak (tests / messages).
func (t *SharedVanishTracker) NotFoundCount(serverID int64) int { return t.notFound[serverID].count }

// sharedVanishDecision is one list round's verdict over the known shares.
type sharedVanishDecision struct {
	revoke  []int64          // landing_server ids to revoke now
	held    map[int64]string // landing_server id -> hold message (mass guard)
	missing map[int64]string // landing_server id -> "still counting" message
}

// observe runs once per fully successful list fetch. known = shared rows that
// are not revoked; present = remote ids that appeared in the list (valid or
// not: an invalid item is a data problem, not a deletion); empty = the list had
// no items at all.
func (t *SharedVanishTracker) observe(known []model.LandingServer, present map[int64]bool, empty bool, now time.Time) sharedVanishDecision {
	out := sharedVanishDecision{held: map[int64]string{}, missing: map[int64]string{}}
	knownIDs := map[int64]bool{}
	var suspects []model.LandingServer
	missingNow := map[int64]bool{}
	for _, s := range known {
		knownIDs[s.ID] = true
		gone := !present[s.RemoteServerID]
		if gone {
			missingNow[s.ID] = true
		}
		if gone || t.notFound[s.ID].qualified(now) {
			suspects = append(suspects, s)
		}
	}
	// Forget servers that came back or are no longer tracked (revoked, removed).
	for id := range t.missing {
		if !missingNow[id] || !knownIDs[id] {
			delete(t.missing, id)
		}
	}
	for id := range t.notFound {
		if !knownIDs[id] {
			delete(t.notFound, id)
		}
	}
	if len(suspects) == 0 {
		return out
	}
	if empty || 2*len(suspects) > len(known) {
		// Mass guard: keep everything as it is; marks are frozen (not advanced).
		msg := fmt.Sprintf("妙妙屋共享列表中同时缺少 %d/%d 个共享，为防误撤销未自动处理，请检查妙妙屋", len(suspects), len(known))
		if empty {
			msg = fmt.Sprintf("妙妙屋返回的共享列表为空（FLVX 已有 %d 个共享），为防误撤销未自动处理，请检查妙妙屋", len(known))
		}
		for _, s := range suspects {
			out.held[s.ID] = msg
		}
		return out
	}
	for _, s := range suspects {
		if missingNow[s.ID] {
			m := t.missing[s.ID]
			if m.count == 0 {
				m.since = now
			}
			m.count++
			t.missing[s.ID] = m
		}
		if t.missing[s.ID].qualified(now) || t.notFound[s.ID].qualified(now) {
			out.revoke = append(out.revoke, s.ID)
			delete(t.missing, s.ID)
			delete(t.notFound, s.ID)
			continue
		}
		if missingNow[s.ID] {
			out.missing[s.ID] = fmt.Sprintf("妙妙屋共享列表中已没有该共享（可能已删除），连续缺失 %d 次（≥%d 分钟）后按撤销处理", sharedVanishRounds, int(sharedVanishSpan/time.Minute))
		}
	}
	sort.Slice(out.revoke, func(i, j int) bool { return out.revoke[i] < out.revoke[j] })
	return out
}
