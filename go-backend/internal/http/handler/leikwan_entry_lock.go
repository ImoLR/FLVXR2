package handler

// Plan 272 「只开主入口」: with the global switch on, a 利群 rule's port is
// blocked (nft, by the entry agent's existing block list) on the 利群入口
// hosts that are not the owner's 主入口. After a 主入口 change the old entry
// stays open until the owner pulls any subscription, or 24 h. Rules with
// 「所有利群入口都可用」 (admin only) stay open on every entry host.

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go-backend/internal/http/response"
)

// leikwanRuleAllEntries: admin switch 「所有利群入口都可用」 of one rule.
func (h *Handler) leikwanRuleAllEntries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
		On bool  `json:"on"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if req.ID <= 0 {
		leiKwanError(w, errors.New("利群规则不存在"))
		return
	}
	if err := h.repo.SetLeiKwanRuleAllEntries(req.ID, req.On); err != nil {
		leiKwanError(w, err)
		return
	}
	h.leiKwanHostKick()
	response.WriteJSON(w, response.OKEmpty())
}

// lkThrottle: per-user "at most once per gap" gates of this panel.
type lkThrottle struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// allow: true (and remembered) when key was not allowed within gap.
func (t *lkThrottle) allow(key string, now time.Time, gap time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = map[string]time.Time{}
	}
	if at, ok := t.seen[key]; ok && now.Sub(at) < gap {
		return false
	}
	if len(t.seen) >= 4096 {
		for k, at := range t.seen {
			if now.Sub(at) >= time.Minute {
				delete(t.seen, k)
			}
		}
	}
	t.seen[key] = now
	return true
}

// leiKwanSubPulled runs after a subscription body with at least one of the
// owner's 利群 nodes went out (plan 276: a sub without 利群 nodes does not
// count): a running 换主入口 grace of the owner ends (the client now has the
// new address). At most one conditional UPDATE per user per minute.
func (h *Handler) leiKwanSubPulled(userID int64, now time.Time) {
	if h == nil || h.repo == nil || userID <= 0 {
		return
	}
	if !h.lkThrottle.allow("pull:"+strconv.FormatInt(userID, 10), now, time.Minute) {
		return
	}
	if ended, err := h.repo.EndLeiKwanGrantGrace(userID, now.UnixMilli()); err == nil && ended {
		h.leiKwanHostKick()
	}
}

// leiKwanSubsChanged: the user's 利群 links changed (主入口, rules, landing):
// rendered subscription bodies of that user are dropped so the next pull
// (which ends a grace) carries the new addresses.
func (h *Handler) leiKwanSubsChanged(userID int64) {
	if h == nil || userID <= 0 {
		return
	}
	h.subState().cache.purgeUser(userID)
}
