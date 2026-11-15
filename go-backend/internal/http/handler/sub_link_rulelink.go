package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/clientip"
	"go-backend/internal/http/response"
	"go-backend/internal/landingsub"
	"go-backend/internal/ratelimit"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// Plan 261 「规则集链接」: a 自己写 分流 entry that points at a rule list on
// the internet. The panel downloads it (the SSRF guard of plan 177: pinned
// public IP per hop, Forbidden list, no proxy / cookies, ≤ 3 redirects),
// keeps only the validated domain / IP rules (sublink.ParseRuleList), stores
// them (user_sub_ruleset) and serves them at /s/rs/u/<public id>.<ext>.
//
// Refresh never runs in a subscription pull's critical path and never on a
// ticker: a pull or a served file whose copy is due (24 h; after failures
// 24 h → 48 h → … ≤ 7 d) enqueues the set; one background worker downloads
// one set at a time panel-wide, ≥ 5 s apart per host, and exits when the
// queue is empty. 获取 / 立即更新 are the user's clicks (per-user fetch limit,
// at most two at a time panel-wide). A failed download keeps the last good
// copy. Logs name the host only (a URL may hold a token).

const (
	subLinkFetchMax    = 20 * time.Second
	subLinkRefresh     = 24 * time.Hour
	subLinkMaxBackoff  = 7 * 24 * time.Hour
	subLinkPending     = 30 * time.Minute // a just-fetched set waits this long for its save
	subLinkInteractive = 2
	subLinkSample      = 5
)

// subLinkHostGap: minimum time between two background downloads from one
// host (var: tests shorten it).
var subLinkHostGap = 5 * time.Second

type subLinkFlight struct {
	done  chan struct{}
	set   model.UserSubRuleSet
	rules *sublink.LinkRules
	err   error
}

// subLinkQueue is the refresh queue and the per-set single flight.
type subLinkQueue struct {
	once        sync.Once
	mu          sync.Mutex
	queue       []int64
	queued      map[int64]bool
	running     bool
	lastHost    map[string]time.Time
	flights     map[int64]*subLinkFlight
	interactive chan struct{}
	serveRL     *ratelimit.Bucket
	keyOnce     sync.Once
	urlKey      []byte
}

func (h *Handler) subLinks() *subLinkQueue {
	q := &h.subLinkQ
	q.once.Do(func() {
		q.queued = map[int64]bool{}
		q.lastHost = map[string]time.Time{}
		q.flights = map[int64]*subLinkFlight{}
		q.interactive = make(chan struct{}, subLinkInteractive)
		// A client downloads a set once a day; 60 per hour per set is plenty.
		q.serveRL = &ratelimit.Bucket{Burst: 60, Every: time.Minute, MaxKeys: 10000}
	})
	return q
}

// subLinkURLHash: keyed hash of a URL (dedupe per user without storing a
// hash anyone could test guesses against).
func (h *Handler) subLinkURLHash(raw string) string {
	q := h.subLinks()
	q.keyOnce.Do(func() {
		sum := sha256.Sum256([]byte("flvx-sub-ruleset-url/v1/" + h.jwtSecret))
		q.urlKey = sum[:]
	})
	m := hmac.New(sha256.New, q.urlKey)
	m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil))
}

func newLinkPublicID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// subLinkDue: whether a set's copy should be refreshed now.
func subLinkDue(s model.UserSubRuleSet, now time.Time) bool {
	if s.LastTryAt == 0 {
		return true
	}
	wait := subLinkRefresh
	for i := 1; i < s.FailCount && wait < subLinkMaxBackoff; i++ {
		wait *= 2
	}
	if wait > subLinkMaxBackoff {
		wait = subLinkMaxBackoff
	}
	return now.Sub(time.UnixMilli(s.LastTryAt)) >= wait
}

// subLinkErr words a fetch failure for 规则集链接 (never the remote body, the
// resolved IP or an internal error string).
func subLinkErr(err error) error {
	var fe *landingsub.FetchError
	if !errors.As(err, &fe) {
		return err // parser errors are already worded
	}
	switch fe.Kind {
	case landingsub.FetchErrURL:
		return errors.New(strings.ReplaceAll(fe.Error(), "订阅地址", "规则集地址"))
	case landingsub.FetchErrBlocked:
		return errors.New("地址无效：不能指向内网、本机、面板或节点的地址")
	case landingsub.FetchErrDNS:
		return errors.New("打不开：无法解析规则集地址的域名")
	case landingsub.FetchErrRedirect:
		return errors.New("打不开：跳转次数太多")
	case landingsub.FetchErrStatus:
		return fmt.Errorf("打不开：对方返回 HTTP %d", fe.Status)
	case landingsub.FetchErrSize:
		return sublink.ErrLinkTooBig
	case landingsub.FetchErrTimeout:
		return fmt.Errorf("打不开：%d 秒内没有下载完，请稍后重试", int(subLinkFetchMax/time.Second))
	}
	return errors.New("打不开：请检查地址能否访问")
}

// subLinkDownload downloads one rule list and parses it.
var subLinkDownload = func(h *Handler, ctx context.Context, raw string) (*sublink.LinkRules, error) {
	f := subFetcher(h)
	f.Timeout = subLinkFetchMax
	f.MaxBytes = sublink.MaxLinkBytes
	body, err := f.Fetch(ctx, raw)
	if err != nil {
		return nil, subLinkErr(err)
	}
	return sublink.ParseRuleList(body)
}

func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "?"
}

func linkContent(r *sublink.LinkRules) repo.SubRuleSetContent {
	text := sublink.LinkText(r.Rules)
	c := r.Counts
	return repo.SubRuleSetContent{Rules: string(text), Total: c.Total(), Domain: c.Domain, Suffix: c.Suffix, Keyword: c.Keyword,
		IP: c.IP, Ignored: c.Ignored, Hash: sublink.LinkHash(text)}
}

func applyLinkContent(s *model.UserSubRuleSet, c repo.SubRuleSetContent, at int64) {
	s.RuleCount, s.CountDomain, s.CountSuffix, s.CountKeyword, s.CountIP, s.Ignored = c.Total, c.Domain, c.Suffix, c.Keyword, c.IP, c.Ignored
	s.ContentHash, s.FetchedAt, s.LastTryAt, s.LastError, s.FailCount, s.UpdatedTime = c.Hash, at, at, "", 0, at
}

// subLinkDoRefresh downloads one stored set (own context: a client that
// hangs up does not cancel it for the others) and records the result.
func (h *Handler) subLinkDoRefresh(s model.UserSubRuleSet) (model.UserSubRuleSet, *sublink.LinkRules, error) {
	now := time.Now().UnixMilli()
	plain, err := h.subCrypto().Decrypt(s.URLEnc)
	var rules *sublink.LinkRules
	if err != nil {
		err = errors.New("规则集地址无法解密，请换链接")
	} else {
		q := h.subLinks()
		host := urlHost(string(plain))
		ctx, cancel := context.WithTimeout(context.Background(), subLinkFetchMax+time.Second)
		rules, err = subLinkDownload(h, ctx, string(plain))
		cancel()
		q.mu.Lock()
		q.lastHost[host] = time.Now()
		q.mu.Unlock()
		if err != nil {
			log.Printf("sub ruleset: set %d refresh from %s failed: %s", s.ID, host, err.Error())
		}
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		_ = h.repo.SaveSubRuleSetFail(s.ID, msg, now)
		s.LastTryAt, s.LastError, s.FailCount = now, msg, s.FailCount+1
		return s, nil, err
	}
	c := linkContent(rules)
	if err := h.repo.SaveSubRuleSetOK(s.ID, c, now); err != nil {
		return s, nil, errors.New("保存失败，请稍后重试")
	}
	applyLinkContent(&s, c, now)
	return s, rules, nil
}

// subLinkRefreshSet refreshes one set, single flight per set (a click and
// the background worker share one download). ctx only bounds the wait.
func (h *Handler) subLinkRefreshSet(ctx context.Context, s model.UserSubRuleSet) (model.UserSubRuleSet, *sublink.LinkRules, error) {
	q := h.subLinks()
	q.mu.Lock()
	f, running := q.flights[s.ID]
	if !running {
		f = &subLinkFlight{done: make(chan struct{})}
		q.flights[s.ID] = f
	}
	q.mu.Unlock()
	if !running {
		go func() {
			defer func() {
				q.mu.Lock()
				delete(q.flights, s.ID)
				q.mu.Unlock()
				close(f.done)
			}()
			f.set, f.rules, f.err = h.subLinkDoRefresh(s)
		}()
	}
	select {
	case <-f.done:
		return f.set, f.rules, f.err
	case <-ctx.Done():
		return s, nil, errors.New("还在更新，请稍后刷新页面查看")
	}
}

// subLinkEnqueue queues due sets for the background worker (deduped).
func (h *Handler) subLinkEnqueue(sets ...model.UserSubRuleSet) {
	now := time.Now()
	q := h.subLinks()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range sets {
		if !subLinkDue(s, now) || q.queued[s.ID] || q.flights[s.ID] != nil {
			continue
		}
		if len(q.queue) >= 4096 {
			return
		}
		q.queued[s.ID] = true
		q.queue = append(q.queue, s.ID)
	}
	if len(q.queue) > 0 && !q.running {
		q.running = true
		go h.subLinkWorker()
	}
}

// subLinkWorker: one background download at a time; exits when idle.
func (h *Handler) subLinkWorker() {
	q := h.subLinks()
	for {
		q.mu.Lock()
		if len(q.queue) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		id := q.queue[0]
		q.queue = q.queue[1:]
		delete(q.queued, id)
		q.mu.Unlock()

		s, err := h.repo.GetSubRuleSet(id)
		if err != nil || s == nil || !subLinkDue(*s, time.Now()) {
			continue // gone, or refreshed meanwhile
		}
		if plain, err := h.subCrypto().Decrypt(s.URLEnc); err == nil {
			host := urlHost(string(plain))
			q.mu.Lock()
			last, seen := q.lastHost[host]
			if len(q.lastHost) > 512 {
				for k, t := range q.lastHost {
					if time.Since(t) > subLinkHostGap {
						delete(q.lastHost, k)
					}
				}
			}
			q.mu.Unlock()
			if wait := subLinkHostGap - time.Since(last); seen && wait > 0 {
				time.Sleep(wait)
			}
		}
		_, _, _ = h.subLinkRefreshSet(context.Background(), *s)
	}
}

// subLinkTakeSlot: one of the two interactive download slots.
func (h *Handler) subLinkTakeSlot(ctx context.Context) (func(), error) {
	q := h.subLinks()
	t := time.NewTimer(subLinkFetchMax)
	defer t.Stop()
	select {
	case q.interactive <- struct{}{}:
		return func() { <-q.interactive }, nil
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, errors.New("服务器正忙，请稍后再试")
}

// subLinkView: what the owner sees of a set (never the URL).
func subLinkView(s model.UserSubRuleSet, rules *sublink.LinkRules) map[string]interface{} {
	v := map[string]interface{}{
		"id": s.ID, "urlHint": s.URLHint, "ruleCount": s.RuleCount,
		"counts":    sublink.LinkCounts{Domain: s.CountDomain, Suffix: s.CountSuffix, Keyword: s.CountKeyword, IP: s.CountIP, Ignored: s.Ignored},
		"fetchedAt": s.FetchedAt, "lastTryAt": s.LastTryAt, "lastError": s.LastError, "failCount": s.FailCount,
	}
	if rules != nil {
		sample := []map[string]string{}
		for i, r := range rules.Rules {
			if i >= subLinkSample {
				break
			}
			kind := r.Kind
			if kind == "ip4" || kind == "ip6" {
				kind = "ip"
			}
			sample = append(sample, map[string]string{"kind": kind, "value": r.Value})
		}
		v["sample"] = sample
	}
	return v
}

// subLinkGC drops the user's sets nothing references (cheap, on writes).
func (h *Handler) subLinkGC(uid int64) {
	if _, err := h.repo.GCSubRuleSets(uid, time.Now().Add(-subLinkPending).UnixMilli()); err != nil {
		log.Printf("sub ruleset: gc user %d: %v", uid, err)
	}
}

func subLinkCapErr() error {
	return fmt.Errorf("每个账号最多保存 %d 个规则集链接，请先删掉不用的", model.MaxSubRuleSetsPerUser)
}

// subLinkFetch: 获取 — download a URL, store its rules (or refresh the
// user's set of that URL) and return the preview.
func (h *Handler) subLinkFetch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := subDecode(r, &req); err != nil {
		subFail(w, err)
		return
	}
	uid, _, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	raw := strings.TrimSpace(req.URL)
	if _, err := landingsub.CheckURL(raw); err != nil {
		subFail(w, subLinkErr(err))
		return
	}
	h.subLinkGC(uid)
	existing, err := h.repo.SubRuleSetByURL(uid, h.subLinkURLHash(raw))
	if err != nil {
		subFail(w, errors.New("获取失败，请稍后重试"))
		return
	}
	if existing == nil {
		if n, err := h.repo.CountSubRuleSets(uid); err != nil {
			subFail(w, errors.New("获取失败，请稍后重试"))
			return
		} else if n >= model.MaxSubRuleSetsPerUser {
			subFail(w, subLinkCapErr())
			return
		}
	}
	if err := h.subTakeFetch(uid); err != nil {
		subFail(w, err)
		return
	}
	release, err := h.subLinkTakeSlot(r.Context())
	if err != nil {
		subFail(w, err)
		return
	}
	defer release()
	if existing != nil {
		s, rules, err := h.subLinkRefreshSet(r.Context(), *existing)
		if err != nil {
			subFail(w, err)
			return
		}
		response.WriteJSON(w, response.OK(subLinkView(s, rules)))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), subLinkFetchMax+time.Second)
	rules, err := subLinkDownload(h, ctx, raw)
	cancel()
	if err != nil {
		subFail(w, err)
		return
	}
	enc, err := h.subCrypto().Encrypt([]byte(raw))
	if err != nil {
		subFail(w, errors.New("保存失败"))
		return
	}
	pid, err := newLinkPublicID()
	if err != nil {
		subFail(w, errors.New("保存失败"))
		return
	}
	now := time.Now().UnixMilli()
	s := model.UserSubRuleSet{UserID: uid, URLEnc: enc, URLHint: maskSourceURL(raw), URLHash: h.subLinkURLHash(raw), PublicID: pid, CreatedTime: now}
	c := linkContent(rules)
	s.Rules = c.Rules
	applyLinkContent(&s, c, now)
	if err := h.repo.CreateSubRuleSet(&s, model.MaxSubRuleSetsPerUser); err != nil {
		if errors.Is(err, repo.ErrSubCap) {
			err = subLinkCapErr()
		} else if again, gerr := h.repo.SubRuleSetByURL(uid, s.URLHash); gerr == nil && again != nil {
			// The same URL was stored by a parallel click: use that row.
			response.WriteJSON(w, response.OK(subLinkView(*again, rules)))
			return
		} else {
			err = errors.New("保存失败")
		}
		subFail(w, err)
		return
	}
	response.WriteJSON(w, response.OK(subLinkView(s, rules)))
}

// subLinkOwned loads the caller's own set (any other id: 不存在).
func (h *Handler) subLinkOwned(r *http.Request) (*model.UserSubRuleSet, error) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := subDecode(r, &req); err != nil {
		return nil, err
	}
	uid, _, err := h.subOwner(r)
	if err != nil {
		return nil, err
	}
	s, err := h.repo.GetSubRuleSet(req.ID)
	if err != nil || s == nil || s.UserID != uid {
		return nil, errors.New("规则集链接不存在，请重新获取")
	}
	return s, nil
}

// subLinkRefreshNow: 立即更新 (per-user fetch limit). A failure keeps the
// last good copy; the view then carries the error.
func (h *Handler) subLinkRefreshNow(w http.ResponseWriter, r *http.Request) {
	s, err := h.subLinkOwned(r)
	if err != nil {
		subFail(w, err)
		return
	}
	if err := h.subTakeFetch(s.UserID); err != nil {
		subFail(w, err)
		return
	}
	release, err := h.subLinkTakeSlot(r.Context())
	if err != nil {
		subFail(w, err)
		return
	}
	defer release()
	updated, rules, ferr := h.subLinkRefreshSet(r.Context(), *s)
	view := subLinkView(updated, rules)
	if ferr != nil {
		view["error"] = ferr.Error()
	}
	response.WriteJSON(w, response.OK(view))
}

// subLinkRefs: the link refs of a list.
func subLinkRefs(list []sublink.RouteEntry) []int64 {
	var ids []int64
	for _, e := range list {
		if e.Type == sublink.RouteLink && e.Ref > 0 {
			ids = append(ids, e.Ref)
		}
	}
	return ids
}

// subLinkResolve fills in the stored sets of a list's link entries (the
// owner's own only) and queues the due ones for a background refresh.
func (h *Handler) subLinkResolve(userID int64, list []sublink.RouteEntry) {
	ids := subLinkRefs(list)
	if len(ids) == 0 {
		return
	}
	sets, err := h.repo.SubRuleSetsByIDs(userID, ids)
	if err != nil {
		return // entries without a set are left out of this render
	}
	due := make([]model.UserSubRuleSet, 0, len(sets))
	for i := range list {
		if list[i].Type != sublink.RouteLink {
			continue
		}
		if s, ok := sets[list[i].Ref]; ok {
			list[i].Link = &sublink.LinkRef{PublicID: s.PublicID}
			due = append(due, s)
		}
	}
	h.subLinkEnqueue(due...)
}

// subLinkViews: the owner's views of the sets a list references.
func (h *Handler) subLinkViews(userID int64, list []sublink.RouteEntry) map[string]interface{} {
	out := map[string]interface{}{}
	ids := subLinkRefs(list)
	if len(ids) == 0 {
		return out
	}
	sets, err := h.repo.SubRuleSetsByIDs(userID, ids)
	if err != nil {
		return out
	}
	for id, s := range sets {
		out[strconv.FormatInt(id, 10)] = subLinkView(s, nil)
	}
	return out
}

// subLinkServe serves GET /s/rs/u/<public id>.<list|json> without login:
// the validated rules in the panel's own format (never the downloaded
// file). Unknown ids get the plain 404 of any unknown /s/ path.
func (h *Handler) subLinkServe(w http.ResponseWriter, r *http.Request, file string) {
	i := strings.LastIndexByte(file, '.')
	if i <= 0 {
		http.NotFound(w, r)
		return
	}
	pid, ext := file[:i], file[i+1:]
	if !sublink.ValidLinkID(pid) || ext != sublink.ExtLinkList && ext != sublink.ExtLinkJSON {
		http.NotFound(w, r)
		return
	}
	if !h.subLinkEnabled() {
		subPlain(w, http.StatusForbidden, "订阅功能已关闭")
		return
	}
	st, q := h.subState(), h.subLinks()
	ipKey := clientip.LimitKey(r)
	if !st.missRL.Take(ipKey) {
		st.refusals.Note("ip")
		subPlain(w, http.StatusTooManyRequests, "请求太频繁，请稍后再试")
		return
	}
	s, err := h.repo.SubRuleSetByPublicID(pid)
	if err != nil {
		st.missRL.Refund(ipKey)
		subPlain(w, http.StatusServiceUnavailable, "暂时不可用，请稍后再试")
		return
	}
	if s == nil {
		st.refusals.Note("unknown_ruleset")
		http.NotFound(w, r)
		return
	}
	st.missRL.Refund(ipKey)
	if !q.serveRL.Take(pid) {
		st.refusals.Note("per_ruleset")
		subPlain(w, http.StatusTooManyRequests, "请求太频繁，请稍后再试")
		return
	}
	h.subLinkEnqueue(*s)
	etag := `"` + s.ContentHash + "-" + ext + `"`
	hd := w.Header()
	hd.Set("ETag", etag)
	hd.Set("Cache-Control", "public, max-age=3600")
	hd.Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	text, err := h.repo.SubRuleSetRules(s.ID)
	if err != nil {
		subPlain(w, http.StatusServiceUnavailable, "暂时不可用，请稍后再试")
		return
	}
	// Re-emitted from the parsed rules: only validated lines ever leave.
	rules := sublink.ParseLinkText(text)
	var body []byte
	if ext == sublink.ExtLinkJSON {
		body = sublink.LinkSingBoxJSON(rules)
		hd.Set("Content-Type", "application/json")
	} else {
		body = sublink.LinkText(rules)
		hd.Set("Content-Type", "text/plain; charset=utf-8")
	}
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
