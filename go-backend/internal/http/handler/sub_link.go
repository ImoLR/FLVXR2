package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"go-backend/internal/http/response"
	"go-backend/internal/landingsub"
	"go-backend/internal/linechain"
	"go-backend/internal/ratelimit"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// Plan 185 「订阅」: per-user subscription links (/s/<token>) merging panel
// nodes, external nodes and nodes of external subscriptions (机场). Only
// references are stored; nodes are rendered on every pull. No background
// jobs: external subscriptions refresh lazily on pulls (6 h), the render is
// cached 60 s in a bounded LRU.

const (
	subConfigKey      = "sub_link_enabled"
	subSourceTTL      = 6 * time.Hour
	subSourceRetry    = time.Hour
	subSourceFetchMax = 10 * time.Second
	subPreviewTTL     = 10 * time.Minute
	subPreviewMax     = 64
	subNameMaxRunes   = 40
)

type subPreview struct {
	owner   int64
	url     string
	nodes   []sublink.SourceNode
	expires time.Time
}

type subFlight struct {
	done chan struct{}
	src  model.UserSubSource
	err  error
}

type subLinkState struct {
	once     sync.Once
	cache    *subRenderCache
	tokenRL  *ratelimit.Bucket
	missRL   *ratelimit.Bucket
	refusals ratelimit.Refusals

	// Plan 193: cold-render slots and the number of pulls waiting for one.
	slots   chan struct{}
	waiting atomic.Int32

	mu       sync.Mutex
	previews map[string]*subPreview
	flights  map[int64]*subFlight
	marked   map[int64]time.Time

	cryptoOnce sync.Once
	crypto     *security.AESCrypto

	// Plan 226: geosite-cn / geoip-cn for the sing-box config; plan 256 the
	// 「分流」 catalog sets.
	ruleSets sublink.RuleSets

	// Plan 256: 「检测全部」 of the 外部节点 dialog, per user.
	probeRL *ratelimit.Bucket
}

func (h *Handler) subState() *subLinkState {
	s := &h.subLink
	s.once.Do(func() {
		s.cache = newSubRenderCache(subCacheEntries, subCacheBytes, subRenderTTL)
		s.slots = make(chan struct{}, subRenderSlots)
		// 60 pulls per hour per subscription.
		s.tokenRL = &ratelimit.Bucket{Burst: 60, Every: time.Minute, MaxKeys: 10000}
		// Unknown tokens: 10 misses at once, then one per minute per address.
		s.missRL = &ratelimit.Bucket{Burst: 10, Every: time.Minute, MaxKeys: 10000}
		s.refusals = ratelimit.Refusals{Name: "sub link"}
		s.probeRL = &ratelimit.Bucket{Burst: 3, Every: 20 * time.Second, MaxKeys: 10000}
		s.previews = map[string]*subPreview{}
		s.flights = map[int64]*subFlight{}
		s.marked = map[int64]time.Time{}
	})
	return s
}

// subCrypto seals source URLs with a key derived from the panel secret.
func (h *Handler) subCrypto() *security.AESCrypto {
	s := h.subState()
	s.cryptoOnce.Do(func() {
		s.crypto, _ = security.NewAESCrypto("flvx-sub-source/v1/" + h.jwtSecret)
	})
	return s.crypto
}

func (h *Handler) subLinkEnabled() bool {
	v, err := h.repo.GetViteConfigValue(subConfigKey)
	return err != nil || strings.TrimSpace(v) != "false"
}

func (h *Handler) registerSubLink(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{
		"list":            h.subList,
		"pool":            h.subPool,
		"create":          h.subCreate,
		"update":          h.subUpdate,
		"delete":          h.subDelete,
		"reset":           h.subReset,
		"source/fetch":    h.subSourceFetch,
		"source/create":   h.subSourceCreate,
		"source/refresh":  h.subSourceRefresh,
		"source/delete":   h.subSourceDelete,
		"external/probe":  h.subExternalProbe,
		"ruleset/fetch":   h.subLinkFetch,
		"ruleset/refresh": h.subLinkRefreshNow,
		// Plan 260: the admin's read-only 「全部用户」 view.
		"admin/list":   h.subAdminList,
		"admin/detail": h.subAdminDetail,
	} {
		fn := fn
		list := path == "list" || path == "admin/list"
		adminOnly := strings.HasPrefix(path, "admin/")
		mux.HandleFunc("/api/v1/sub/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			_, role, err := userRoleFromRequest(r)
			if err != nil {
				response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
				return
			}
			if adminOnly && role != 0 {
				response.WriteJSON(w, response.Err(403, subAdminDenied))
				return
			}
			if !list && !h.subLinkEnabled() {
				response.WriteJSON(w, response.ErrDefault("订阅功能已被管理员关闭"))
				return
			}
			fn(w, r)
		})
	}
	mux.HandleFunc("/s/", h.subPublic)
	mux.HandleFunc(sublink.RuleSetPath, h.subRuleSet)
}

func subDecode(r *http.Request, v interface{}) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 256<<10)).Decode(v); err != nil {
		return errors.New("请求参数错误")
	}
	return nil
}

func subFail(w http.ResponseWriter, err error) {
	response.WriteJSON(w, response.ErrDefault(err.Error()))
}

// ---------------------------------------------------------- resolution

// subData is everything one or more subscriptions of a user reference.
type subData struct {
	links     map[string]repo.SubPanelLink
	byEntry   map[string][]repo.SubPanelLink // "forwardID|entryNodeID" → links
	panel     *repo.SubPanelData
	externals map[int64]model.LandingExternalNode
	sources   map[int64]model.UserSubSource
	srcNodes  map[int64][]sublink.SourceNode
	// Plan 272: the owner's 利群 rules as nodes (nil = not referenced).
	leikwan *lkSubData
	// Plan 315: the subscription has fixed node names (entry_names = false):
	// rule nodes are named after the forward only, and a picked entry that no
	// longer serves the forward follows its one remaining entry. Set by the
	// caller per subscription before resolve / panelLink.
	fixedNames bool
	byForward  map[int64][]repo.SubPanelLink // forward id → its rule links
}

// withNames (plan 315): the same data for a subscription's naming mode.
func (d *subData) withNames(s *model.UserSubscription) *subData {
	d.fixedNames = s != nil && s.FixedNames()
	return d
}

// subCounts summarizes a subscription for its card.
type subCounts struct {
	Panel    int              `json:"panel"`
	External int              `json:"external"`
	Missing  int              `json:"missing"`
	LeiKwan  int              `json:"leikwan"`
	Sources  []subSourceCount `json:"sources"`
	Total    int              `json:"total"`
}

type subSourceCount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	All   bool   `json:"all"`
	Count int    `json:"count"`
}

// subLoad batch-loads what refs point at (all = the whole pool of the user,
// for the editor). refresh refetches stale external sources inline.
func (h *Handler) subLoad(ctx context.Context, userID int64, refs []sublink.Ref, all, refresh bool) (*subData, error) {
	var fwd, land, ext, src []int64
	wantLK := all
	for _, ref := range refs {
		switch ref.Kind {
		case sublink.RefLeiKwanRule, sublink.RefLeiKwanAll:
			wantLK = true
		case sublink.RefForwardLink:
			fwd = append(fwd, ref.ForwardID)
		case sublink.RefLandingDirect:
			land = append(land, ref.LandingID)
		case sublink.RefExternalNode:
			ext = append(ext, ref.ExternalID)
		case sublink.RefSourceNode, sublink.RefSourceAll:
			src = append(src, ref.SourceID)
		}
	}
	d := &subData{links: map[string]repo.SubPanelLink{}, byEntry: map[string][]repo.SubPanelLink{}, externals: map[int64]model.LandingExternalNode{},
		sources: map[int64]model.UserSubSource{}, srcNodes: map[int64][]sublink.SourceNode{}, byForward: map[int64][]repo.SubPanelLink{}}
	if wantLK {
		d.leikwan = h.leiKwanSubLoadAt(userID, lkEntryFrom(ctx))
	}
	if all || len(fwd)+len(land) > 0 {
		var fIDs, lIDs []int64
		if !all {
			fIDs, lIDs = append([]int64{}, fwd...), append([]int64{}, land...)
		}
		panel, err := h.repo.SubPanelLinks(userID, fIDs, lIDs, all)
		if err != nil {
			return nil, err
		}
		d.panel = panel
		for _, l := range panel.Links {
			d.links[l.Ref.Key()] = l
			if l.Ref.Kind == sublink.RefForwardLink {
				node, _, _ := strings.Cut(l.Ref.Entry, "|")
				k := strconv.FormatInt(l.Ref.ForwardID, 10) + "|" + node
				d.byEntry[k] = append(d.byEntry[k], l)
				d.byForward[l.Ref.ForwardID] = append(d.byForward[l.Ref.ForwardID], l)
			}
		}
	}
	if all || len(ext) > 0 {
		var ids []int64
		if !all {
			ids = ext
		}
		if d.panel != nil { // plan 284: SubPanelLinks already loaded them all
			want := map[int64]bool{}
			for _, id := range ids {
				want[id] = true
			}
			for _, e := range d.panel.ExternalNodes {
				if ids == nil || want[e.ID] {
					d.externals[e.ID] = e
				}
			}
		} else {
			rows, err := h.repo.ExternalNodesByIDs(userID, ids)
			if err != nil {
				return nil, err
			}
			for _, e := range rows {
				d.externals[e.ID] = e
			}
		}
	}
	if all || len(src) > 0 {
		var rows map[int64]model.UserSubSource
		var err error
		if all {
			list, lerr := h.repo.ListSubSources(userID)
			rows, err = map[int64]model.UserSubSource{}, lerr
			for _, s := range list {
				rows[s.ID] = s
			}
		} else {
			rows, err = h.repo.SubSourcesByIDs(userID, src)
		}
		if err != nil {
			return nil, err
		}
		if refresh {
			rows = h.subRefreshStale(ctx, rows)
		}
		for id, s := range rows {
			d.sources[id] = s
			nodes := sublink.ParseSourceNodes(s.CachedNodes)
			if len(nodes) > model.MaxSubSourceNodes {
				// A cache written before plan 193 may hold more.
				nodes = nodes[:model.MaxSubSourceNodes]
			}
			d.srcNodes[id] = nodes
		}
	}
	return d, nil
}

func externalNode(e model.LandingExternalNode) (sublink.Node, bool) {
	up, err := linechain.FromJSON(e.Upstream)
	if err != nil || up.Type == "" {
		return sublink.Node{}, false
	}
	up.Tag = ""
	return sublink.Node{Name: e.Name, Up: up}, true
}

// panelLink finds a rule link; a rule whose entry address changed (same
// entry node, one address) still resolves.
func (d *subData) panelLink(ref sublink.Ref) (repo.SubPanelLink, bool) {
	if l, ok := d.links[ref.Key()]; ok {
		return l, true
	}
	if ref.Kind == sublink.RefForwardLink {
		node, _, _ := strings.Cut(ref.Entry, "|")
		list := d.byEntry[strconv.FormatInt(ref.ForwardID, 10)+"|"+node]
		if len(list) == 1 {
			return list[0], true
		}
		// Plan 315 (fixed names only): the picked entry no longer serves the
		// forward and the forward has exactly one entry address now → follow
		// it (換入口 keeps the node, and its name).
		if d.fixedNames && len(list) == 0 {
			if all := d.byForward[ref.ForwardID]; len(all) == 1 {
				return all[0], true
			}
		}
	}
	return repo.SubPanelLink{}, false
}

// rulePicks (plan 315): the rule link each forward_link ref resolves to, by
// ref index. Fixed names: a ref that only follows (its entry is gone) onto a
// link another ref of this subscription already names is dropped (never two
// equal nodes); perForward counts the distinct links of each forward, so a
// forward picked through two or more entries keeps 「 · 入口名」. Today's names:
// panelLink as before, perForward unused.
func (d *subData) rulePicks(refs []sublink.Ref) (map[int]repo.SubPanelLink, map[int64]int) {
	picks := map[int]repo.SubPanelLink{}
	perForward := map[int64]int{}
	direct := map[string]bool{}
	if d.fixedNames {
		for _, ref := range refs {
			if ref.Kind == sublink.RefForwardLink {
				if _, ok := d.links[ref.Key()]; ok {
					direct[ref.Key()] = true
				}
			}
		}
	}
	used := map[string]bool{}
	for i, ref := range refs {
		if ref.Kind != sublink.RefForwardLink {
			continue
		}
		l, ok := d.panelLink(ref)
		if !ok {
			continue
		}
		if d.fixedNames {
			k := l.Ref.Key()
			if used[k] || (k != ref.Key() && direct[k]) {
				continue
			}
			used[k] = true
			perForward[l.Ref.ForwardID]++
		}
		picks[i] = l
	}
	return picks, perForward
}

// fixedRuleName (plan 315): the forward name, plus 「 · 入口名」 when this
// subscription holds the forward through two or more entries.
func fixedRuleName(l repo.SubPanelLink, perForward map[int64]int) string {
	if perForward[l.Ref.ForwardID] > 1 && l.EntryName != "" {
		return l.BaseName + " · " + l.EntryName
	}
	return l.BaseName
}

// resolve turns refs into nodes (in order) and card counts. At most max
// nodes are built (plan 193: a render needs only the first 100; the counts
// still cover every node, so an over-limit subscription shows its total).
func (d *subData) resolve(refs []sublink.Ref, max int) ([]sublink.Node, subCounts) {
	nodes := []sublink.Node{}
	c := subCounts{Sources: []subSourceCount{}}
	picks, perForward := d.rulePicks(refs)
	keep := func(n sublink.Node) {
		c.Total++
		if len(nodes) < max {
			nodes = append(nodes, n)
		}
	}
	srcCount := map[int64]*subSourceCount{}
	srcOrder := []int64{}
	countSrc := func(id int64, all bool, n int) {
		sc := srcCount[id]
		if sc == nil {
			sc = &subSourceCount{ID: id, Name: d.sources[id].Name}
			srcCount[id] = sc
			srcOrder = append(srcOrder, id)
		}
		sc.All = sc.All || all
		sc.Count += n
	}
	for i, ref := range refs {
		switch ref.Kind {
		case sublink.RefForwardLink:
			if l, ok := picks[i]; ok {
				n := l.Node
				n.Key = ref.Key()
				if d.fixedNames {
					n.Name = fixedRuleName(l, perForward)
				}
				keep(n)
				c.Panel++
				continue
			}
		case sublink.RefLandingDirect:
			if l, ok := d.panelLink(ref); ok {
				n := l.Node
				n.Key = ref.Key()
				keep(n)
				c.Panel++
				continue
			}
		case sublink.RefExternalNode:
			if e, ok := d.externals[ref.ExternalID]; ok {
				if n, ok := externalNode(e); ok {
					n.Key = ref.Key()
					keep(n)
					c.External++
					continue
				}
			}
		case sublink.RefSourceAll:
			if _, ok := d.sources[ref.SourceID]; ok {
				list := d.srcNodes[ref.SourceID]
				for _, sn := range list {
					n := sn.Node()
					n.Key = sublink.Ref{Kind: sublink.RefSourceNode, SourceID: ref.SourceID, Name: sn.Name}.Key()
					keep(n)
				}
				countSrc(ref.SourceID, true, len(list))
				continue
			}
		case sublink.RefLeiKwanRule:
			if d.leikwan != nil {
				if n, ok := d.leikwan.nodes[ref.LeiKwanRuleID]; ok {
					n.Key = ref.Key()
					keep(n)
					c.LeiKwan++
					continue
				}
			}
		case sublink.RefLeiKwanAll:
			if d.leikwan != nil && d.leikwan.granted {
				for _, id := range d.leikwan.ruleIDs() {
					if n, ok := d.leikwan.nodes[id]; ok {
						n.Key = sublink.Ref{Kind: sublink.RefLeiKwanRule, LeiKwanRuleID: id}.Key()
						keep(n)
						c.LeiKwan++
					}
				}
				continue
			}
		case sublink.RefSourceNode:
			if _, ok := d.sources[ref.SourceID]; ok {
				found := false
				for _, sn := range d.srcNodes[ref.SourceID] {
					if sn.Name == ref.Name {
						n := sn.Node()
						n.Key = ref.Key()
						keep(n)
						countSrc(ref.SourceID, false, 1)
						found = true
						break
					}
				}
				if found {
					continue
				}
			}
		}
		c.Missing++
	}
	for _, id := range srcOrder {
		c.Sources = append(c.Sources, *srcCount[id])
	}
	return nodes, c
}

// normalizeRefs: a followed source (source_all) makes single picks of that
// source redundant.
func normalizeRefs(refs []sublink.Ref) []sublink.Ref {
	follow := map[int64]bool{}
	lkAll := false
	for _, r := range refs {
		if r.Kind == sublink.RefSourceAll {
			follow[r.SourceID] = true
		}
		if r.Kind == sublink.RefLeiKwanAll {
			lkAll = true
		}
	}
	out := refs[:0]
	for _, r := range refs {
		if r.Kind == sublink.RefSourceNode && follow[r.SourceID] {
			continue
		}
		if r.Kind == sublink.RefLeiKwanRule && lkAll {
			continue // plan 272: 「全部利群规则」 already has it
		}
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------- views

func subRefView(ref sublink.Ref) map[string]interface{} {
	v := map[string]interface{}{"kind": ref.Kind}
	switch ref.Kind {
	case sublink.RefForwardLink:
		v["forwardId"], v["entry"] = ref.ForwardID, ref.Entry
	case sublink.RefLandingDirect:
		v["landingId"] = ref.LandingID
	case sublink.RefExternalNode:
		v["externalId"] = ref.ExternalID
	case sublink.RefSourceNode:
		v["sourceId"], v["name"] = ref.SourceID, ref.Name
	case sublink.RefSourceAll:
		v["sourceId"] = ref.SourceID
	case sublink.RefLeiKwanRule:
		v["leikwanRuleId"] = ref.LeiKwanRuleID
	}
	return v
}

func nodeDetail(n sublink.Node) string {
	return net.JoinHostPort(strings.Trim(n.Server(), "[]"), strconv.Itoa(n.Port()))
}

func subSourceView(s model.UserSubSource, usedBy int) map[string]interface{} {
	count := s.NodeCount
	if count > model.MaxSubSourceNodes {
		count = model.MaxSubSourceNodes // pre-plan-193 cache, used as its first 100
	}
	return map[string]interface{}{"id": s.ID, "name": s.Name, "urlHint": s.URLHint, "nodeCount": count, "lastOkAt": s.LastOkAt,
		"lastFetchAt": s.LastFetchAt, "lastError": s.LastError, "usedBy": usedBy}
}

func (h *Handler) subOwner(r *http.Request) (int64, bool, error) {
	uid, role, err := userRoleFromRequest(r)
	return uid, role == 0, err
}

// subList: the user's subscriptions with card counts, and their sources.
func (h *Handler) subList(w http.ResponseWriter, r *http.Request) {
	uid, admin, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	enabled := h.subLinkEnabled()
	out := map[string]interface{}{"enabled": enabled, "subs": []interface{}{}, "sources": []interface{}{}, "cap": model.MaxSubscriptionsPerUser,
		"sourceCap": model.MaxSubSourcesPerUser, "maxNodes": model.MaxSubscriptionNodes, "externals": 0}
	if admin {
		out["cap"] = 0
	}
	if !enabled {
		response.WriteJSON(w, response.OK(out))
		return
	}
	h.subLinkGC(uid) // plan 261: sets nothing references any more
	subs, err := h.repo.ListSubscriptions(uid)
	if err != nil {
		subFail(w, err)
		return
	}
	var union []sublink.Ref
	for _, s := range subs {
		union = append(union, sublink.ParseRefs(s.Items)...)
	}
	d, err := h.subLoad(r.Context(), uid, union, false, false)
	if err != nil {
		subFail(w, err)
		return
	}
	sources, err := h.repo.ListSubSources(uid)
	if err != nil {
		subFail(w, err)
		return
	}
	// Plan 256: the 「外部节点 (N)」 pill.
	if n, err := h.repo.CountLandingExternalNodes(uid); err == nil {
		out["externals"] = n
	}
	usedBy := map[int64]int{}
	views := []interface{}{}
	for _, s := range subs {
		refs := sublink.ParseRefs(s.Items)
		_, counts := d.withNames(&s).resolve(refs, 0)
		seen := map[int64]bool{}
		for _, ref := range refs {
			if (ref.Kind == sublink.RefSourceAll || ref.Kind == sublink.RefSourceNode) && !seen[ref.SourceID] {
				seen[ref.SourceID] = true
				usedBy[ref.SourceID]++
			}
		}
		views = append(views, map[string]interface{}{"id": s.ID, "name": s.Name, "token": s.Token, "format": s.Format, "ruleMode": s.RuleMode,
			"counts": counts, "items": len(refs), "lastPullAt": s.LastPullAt, "lastPullClient": s.LastPullClient, "lastPullNodes": s.LastPullNodes, "createdTime": s.CreatedTime,
			"routing": len(sublink.ParseRouting(s.Routing)), "auto": s.AutoKind, "entryMode": subEntryMode(&s), "entryNames": !s.FixedNames()})
	}
	srcViews := []interface{}{}
	for _, s := range sources {
		srcViews = append(srcViews, subSourceView(s, usedBy[s.ID]))
	}
	out["subs"], out["sources"] = views, srcViews
	response.WriteJSON(w, response.OK(out))
}

// subPool: every node the user can put into a subscription (editor left
// column) and, for an existing subscription, its items (right column).
func (h *Handler) subPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
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
	var refs []sublink.Ref
	routing := sublink.DefaultRouting() // a new subscription starts with the default 分流
	ctx := r.Context()
	var sub *model.UserSubscription
	if req.ID > 0 {
		s, err := h.repo.GetSubscription(req.ID)
		if err != nil || s == nil || s.UserID != uid {
			subFail(w, errors.New("订阅不存在"))
			return
		}
		refs = sublink.ParseRefs(s.Items)
		routing = sublink.ParseRouting(s.Routing)
		ctx = subEntryCtx(ctx, s) // plan 297: a 手机本地 sub shows 127.0.0.1
		sub = s
	}
	d, err := h.subLoad(ctx, uid, refs, true, false)
	if err != nil {
		subFail(w, err)
		return
	}
	d.withNames(sub)
	picks, _ := d.rulePicks(refs)
	item := func(ref sublink.Ref, n sublink.Node, detail string) map[string]interface{} {
		return map[string]interface{}{"ref": subRefView(ref), "key": ref.Key(), "name": n.Name, "protocol": n.ProtocolLabel(), "detail": detail}
	}
	rules, landing, externals := []interface{}{}, []interface{}{}, []interface{}{}
	for _, l := range d.panel.Links {
		it := item(l.Ref, l.Node, l.Detail)
		if l.Rule {
			// Plan 315: the editor shows the fixed name from these.
			it["base"], it["entryName"] = l.BaseName, l.EntryName
			it["chain"] = l.Chain
			if l.External != "" { // plan 284: 端口转发 · 外部节点 <名>
				it["external"], it["externalAuto"] = l.External, l.ExternalAuto
			}
			rules = append(rules, it)
		} else {
			landing = append(landing, it)
		}
	}
	extIDs := make([]int64, 0, len(d.externals))
	for id := range d.externals {
		extIDs = append(extIDs, id)
	}
	sort.Slice(extIDs, func(i, j int) bool { return extIDs[i] < extIDs[j] })
	for _, id := range extIDs {
		if n, ok := externalNode(d.externals[id]); ok {
			externals = append(externals, item(sublink.Ref{Kind: sublink.RefExternalNode, ExternalID: id}, n, nodeDetail(n)))
		}
	}
	srcIDs := make([]int64, 0, len(d.sources))
	for id := range d.sources {
		srcIDs = append(srcIDs, id)
	}
	sort.Slice(srcIDs, func(i, j int) bool { return srcIDs[i] < srcIDs[j] })
	sources := []interface{}{}
	for _, id := range srcIDs {
		s := d.sources[id]
		nodes := []interface{}{}
		seen := map[string]bool{}
		for _, sn := range d.srcNodes[id] {
			if seen[sn.Name] {
				continue
			}
			seen[sn.Name] = true
			n := sn.Node()
			nodes = append(nodes, item(sublink.Ref{Kind: sublink.RefSourceNode, SourceID: id, Name: sn.Name}, n, nodeDetail(n)))
		}
		v := subSourceView(s, 0)
		v["nodes"], v["followKey"] = nodes, sublink.Ref{Kind: sublink.RefSourceAll, SourceID: id}.Key()
		sources = append(sources, v)
	}
	// Plan 272 「利群汇聚」: the user's 利群 rules (manual landings greyed).
	lkItems := []interface{}{}
	lkGranted := d.leikwan != nil && d.leikwan.granted
	if lkGranted {
		for _, x := range d.leikwan.rules {
			ref := sublink.Ref{Kind: sublink.RefLeiKwanRule, LeiKwanRuleID: x.ID}
			if n, ok := d.leikwan.nodes[x.ID]; ok {
				lkItems = append(lkItems, item(ref, n, nodeDetail(n)))
				continue
			}
			lkItems = append(lkItems, map[string]interface{}{"ref": subRefView(ref), "key": ref.Key(), "name": leiKwanSubNodeName(d.leikwan.lines[x.LineID], x),
				"protocol": "", "detail": "", "disabled": true, "reason": d.leikwan.reasons[x.ID]})
		}
	}
	// Selected items, in order, with the missing ones marked. rekey: a rule
	// whose entry address changed is shown under its current key, so a
	// 分流 exit naming the stored key is moved along.
	selected := []interface{}{}
	rekey := map[string]string{}
	for i, ref := range refs {
		v := map[string]interface{}{"ref": subRefView(ref), "key": ref.Key()}
		switch ref.Kind {
		case sublink.RefForwardLink, sublink.RefLandingDirect:
			l, ok := picks[i]
			if ref.Kind == sublink.RefLandingDirect {
				l, ok = d.panelLink(ref)
			}
			if ok {
				v["name"], v["protocol"], v["group"] = l.Node.Name, l.Node.ProtocolLabel(), "landing"
				if l.Rule {
					// Plan 315: the editor shows the fixed name from these.
					v["base"], v["entryName"] = l.BaseName, l.EntryName
				}
				if l.Rule {
					v["group"], v["chain"] = "rule", l.Chain
					if l.External != "" {
						v["external"], v["externalAuto"] = l.External, l.ExternalAuto
					}
				}
				v["key"] = l.Ref.Key()
				v["ref"] = subRefView(l.Ref)
				if k := ref.Key(); k != l.Ref.Key() {
					rekey[k] = l.Ref.Key()
				}
			} else {
				v["missing"], v["group"] = true, "rule"
				if ref.Kind == sublink.RefLandingDirect {
					v["group"] = "landing"
				}
			}
		case sublink.RefExternalNode:
			if e, ok := d.externals[ref.ExternalID]; ok {
				n, _ := externalNode(e)
				v["name"], v["protocol"] = n.Name, n.ProtocolLabel()
			} else {
				v["missing"] = true
			}
			v["group"] = "external"
		case sublink.RefLeiKwanRule:
			v["group"] = "leikwan"
			n, ok := sublink.Node{}, false
			if d.leikwan != nil {
				n, ok = d.leikwan.nodes[ref.LeiKwanRuleID]
			}
			if ok {
				v["name"], v["protocol"] = n.Name, n.ProtocolLabel()
			} else {
				v["missing"] = true
			}
		case sublink.RefLeiKwanAll:
			v["group"], v["name"] = "leikwan_all", "全部利群转发（自动跟随）"
			if d.leikwan == nil || !d.leikwan.granted {
				v["missing"] = true
			} else {
				v["count"] = len(d.leikwan.nodes)
			}
		case sublink.RefSourceAll, sublink.RefSourceNode:
			s, ok := d.sources[ref.SourceID]
			v["group"], v["sourceName"] = "source", s.Name
			if !ok {
				v["missing"] = true
				break
			}
			if ref.Kind == sublink.RefSourceAll {
				v["group"], v["count"] = "source_all", len(d.srcNodes[ref.SourceID])
				break
			}
			v["missing"] = true
			for _, sn := range d.srcNodes[ref.SourceID] {
				if sn.Name == ref.Name {
					n := sn.Node()
					v["name"], v["protocol"], v["missing"] = n.Name, n.ProtocolLabel(), false
					break
				}
			}
			if v["missing"] == true {
				v["name"] = ref.Name
			}
		}
		selected = append(selected, v)
	}
	if routing == nil {
		routing = []sublink.RouteEntry{}
	}
	for i := range routing {
		if k, ok := strings.CutPrefix(routing[i].Exit, sublink.ExitNodePrefix); ok && rekey[k] != "" {
			routing[i].Exit = sublink.ExitNodePrefix + rekey[k]
		}
	}
	// Plan 297: the 主入口 name for the editor's 「入口」 row (跟主入口 …).
	lkMainName := ""
	if lkGranted {
		if e := h.leiKwanUserEntry(uid); e != nil {
			lkMainName = e.Name
		}
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"rules": rules, "landing": landing, "external": externals, "sources": sources, "portForwards": d.panel.PortForwards,
		"leikwan": lkItems, "leikwanFollowKey": sublink.Ref{Kind: sublink.RefLeiKwanAll}.Key(), "leikwanGranted": lkGranted, "leikwanMainEntry": lkMainName,
		"selected": selected, "maxNodes": model.MaxSubscriptionNodes,
		"routing": routing, "routingCatalog": subRoutingCatalog(), "routingDefaults": sublink.DefaultRouteKeys,
		"routingLimits": map[string]int{"entries": sublink.MaxRouteEntries, "lines": sublink.MaxRouteLines, "lineLen": sublink.MaxRouteLineLen, "nameLen": sublink.MaxRouteNameLen,
			"links": sublink.MaxLinkEntries, "linkSets": model.MaxSubRuleSetsPerUser, "linkRules": sublink.MaxLinkRules},
		"routingLinks": h.subLinkViews(uid, routing),
	}))
}

// subRoutingCatalog: the builtin 分流 the editor offers (one row per
// sublink.Catalog entry, so a new category needs no UI work).
func subRoutingCatalog() []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(sublink.Catalog))
	for _, c := range sublink.Catalog {
		out = append(out, map[string]interface{}{"key": c.Key, "icon": c.Icon, "name": c.Name, "desc": c.Desc, "exit": c.Exit})
	}
	return out
}

type subSaveReq struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	Format   string        `json:"format"`
	RuleMode string        `json:"ruleMode"`
	Items    []sublink.Ref `json:"items"`
	// Routing: the 分流 list (plan 256); absent = create with the defaults /
	// update keeps the stored list (an editor from before plan 256).
	Routing *[]sublink.RouteEntry `json:"routing"`
	// EntryMode (plan 297): "main" | "local" (手机本地, admin only); absent =
	// create as main / update keeps the stored mode.
	EntryMode *string `json:"entryMode"`
	// EntryNames (plan 315 「节点名带入口名」): true = today's names, false =
	// fixed names (the editor sends false for a new subscription); absent =
	// create with today's names (an older editor or API client: output as
	// before) / update keeps the stored setting.
	EntryNames *bool `json:"entryNames"`
}

// subReqEntryMode (plan 297): the entry mode a save asks for — "" = keep
// (update) / main (create). 手机本地 is admin-only this round.
func subReqEntryMode(req *subSaveReq, admin bool) (string, error) {
	if req.EntryMode == nil {
		return "", nil
	}
	switch *req.EntryMode {
	case model.SubEntryMain:
		return model.SubEntryMain, nil
	case model.SubEntryLocal:
		if !admin {
			return "", errLKLocalAdminOnly
		}
		return model.SubEntryLocal, nil
	}
	return "", errors.New("入口只能是跟主入口或手机本地")
}

// subValidate checks a save request against the user's own data: only refs
// that resolve are kept (other users' ids never do), at most 100 nodes. The
// 分流 list (when sent) comes back cleaned as its stored JSON.
func (h *Handler) subValidate(ctx context.Context, uid int64, req *subSaveReq, fixed bool) ([]sublink.Ref, *string, error) {
	req.Name = landingsub.CleanName(strings.TrimSpace(req.Name))
	if req.Name == "" {
		req.Name = "我的订阅"
	}
	if req.Format == "" {
		req.Format = sublink.FormatAuto
	}
	if req.RuleMode == "" {
		req.RuleMode = sublink.ModeCNDirect
	}
	if !sublink.ValidFormat(req.Format) || !sublink.ValidMode(req.RuleMode) {
		return nil, nil, errors.New("客户端或分流设置无效")
	}
	refs, err := sublink.CleanRefs(req.Items)
	if err != nil {
		return nil, nil, err
	}
	refs = normalizeRefs(refs)
	d, err := h.subLoad(ctx, uid, refs, false, false)
	if err != nil {
		return nil, nil, err
	}
	d.fixedNames = fixed // plan 315
	kept := make([]sublink.Ref, 0, len(refs))
	keptKeys := map[string]bool{}
	total := 0
	for _, ref := range refs {
		_, c := d.resolve([]sublink.Ref{ref}, 0)
		if c.Missing > 0 {
			continue
		}
		if ref.Kind == sublink.RefForwardLink {
			// Keep the current entry key (an address change resolved by node;
			// plan 315 fixed names: also an entry the forward moved to).
			if l, ok := d.panelLink(ref); ok {
				ref = l.Ref
			}
			if fixed && keptKeys[ref.Key()] {
				continue // plan 315: followed onto a link already in the list
			}
			keptKeys[ref.Key()] = true
		}
		total += c.Total
		kept = append(kept, ref)
	}
	if total > model.MaxSubscriptionNodes {
		return nil, nil, fmt.Errorf("每条订阅最多 %d 个节点，现在是 %d 个，如需更多请联系管理员", model.MaxSubscriptionNodes, total)
	}
	if req.Routing == nil {
		return kept, nil, nil
	}
	// A node: exit must name a node of this subscription: a kept item, or a
	// node of a followed source (it may come and go with the source).
	inItems, follow := map[string]bool{}, map[int64]bool{}
	for _, ref := range kept {
		inItems[ref.Key()] = true
		if ref.Kind == sublink.RefSourceAll {
			follow[ref.SourceID] = true
		}
	}
	inSub := func(k string) bool {
		if inItems[k] {
			return true
		}
		if rest, ok := strings.CutPrefix(k, "s:"); ok {
			id, _, _ := strings.Cut(rest, ":")
			n, err := strconv.ParseInt(id, 10, 64)
			return err == nil && follow[n]
		}
		// Plan 272: a 利群 rule of 「全部利群规则」 (it comes and goes with the rules).
		if strings.HasPrefix(k, "k:") && inItems[sublink.Ref{Kind: sublink.RefLeiKwanAll}.Key()] {
			return true
		}
		return false
	}
	nodes, _ := d.resolve(kept, model.MaxSubscriptionNodes)
	names := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		names[strings.TrimSpace(n.Name)] = true
	}
	// Plan 261: a link entry must name one of the user's own stored sets.
	owned, err := h.repo.SubRuleSetsByIDs(uid, subLinkRefs(*req.Routing))
	if err != nil {
		return nil, nil, err
	}
	linkOK := func(ref int64) bool { _, ok := owned[ref]; return ok }
	routing, err := sublink.CleanRoutingLinks(*req.Routing, inSub, names, linkOK)
	if err != nil {
		return nil, nil, err
	}
	js := sublink.RoutingJSON(routing)
	return kept, &js, nil
}

func subView(s *model.UserSubscription) map[string]interface{} {
	return map[string]interface{}{"id": s.ID, "name": s.Name, "token": s.Token, "format": s.Format, "ruleMode": s.RuleMode, "entryMode": subEntryMode(s),
		"entryNames": !s.FixedNames()}
}

// subEntryMode (plan 297): the subscription's entry mode for views — "main"
// (the owner's 主入口) or "local" (手机本地, 127.0.0.1).
func subEntryMode(s *model.UserSubscription) string {
	if s != nil && s.EntryLocal() {
		return model.SubEntryLocal
	}
	return model.SubEntryMain
}

// subClientTitle (plan 297): the title clients show (profile-title, file
// name, browser page). A 手机本地 subscription gets 「 · 手机本地」, derived
// from entry_mode (never written into the editable name), so switching the
// entry back to the 主入口 drops it at once.
func subClientTitle(s *model.UserSubscription) string {
	if s.EntryLocal() {
		return s.Name + " · " + lkLocalName
	}
	return s.Name
}

// subEntryCtx (plan 297): a 手机本地 subscription renders its 利群 nodes on
// 127.0.0.1 (subLoad reads the entry from the context); main = unchanged.
func subEntryCtx(ctx context.Context, s *model.UserSubscription) context.Context {
	if s != nil && s.EntryLocal() {
		return withLKEntry(ctx, lkLocalEntry())
	}
	return ctx
}

func (h *Handler) subCreate(w http.ResponseWriter, r *http.Request) {
	var req subSaveReq
	if err := subDecode(r, &req); err != nil {
		subFail(w, err)
		return
	}
	uid, admin, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	refs, routing, err := h.subValidate(r.Context(), uid, &req, req.EntryNames != nil && !*req.EntryNames)
	if err != nil {
		subFail(w, err)
		return
	}
	mode, err := subReqEntryMode(&req, admin)
	if err != nil {
		subFail(w, err)
		return
	}
	if mode == "" {
		mode = model.SubEntryMain
	}
	if routing == nil {
		js := sublink.RoutingJSON(sublink.DefaultRouting())
		routing = &js
	}
	token, err := sublink.NewToken()
	if err != nil {
		subFail(w, errors.New("生成订阅链接失败"))
		return
	}
	now := time.Now().UnixMilli()
	s := &model.UserSubscription{UserID: uid, Name: req.Name, Token: token, Format: req.Format, RuleMode: req.RuleMode, Items: sublink.RefsJSON(refs), Routing: routing, CreatedTime: now, UpdatedTime: now,
		EntryMode: mode, EntryNames: req.EntryNames}
	cap := model.MaxSubscriptionsPerUser
	if admin {
		cap = 0
	}
	if err := h.repo.CreateSubscription(s, cap); err != nil {
		if errors.Is(err, repo.ErrSubCap) {
			err = fmt.Errorf("每个账号最多 %d 条订阅", cap)
		}
		subFail(w, err)
		return
	}
	h.subLinkGC(uid)
	response.WriteJSON(w, response.OK(subView(s)))
}

func (h *Handler) subUpdate(w http.ResponseWriter, r *http.Request) {
	var req subSaveReq
	if err := subDecode(r, &req); err != nil {
		subFail(w, err)
		return
	}
	uid, admin, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	s, err := h.repo.GetSubscription(req.ID)
	if err != nil || s == nil || s.UserID != uid {
		subFail(w, errors.New("订阅不存在"))
		return
	}
	fixed := s.FixedNames()
	if req.EntryNames != nil {
		fixed = !*req.EntryNames
	}
	refs, routing, err := h.subValidate(r.Context(), uid, &req, fixed)
	if err != nil {
		subFail(w, err)
		return
	}
	mode, err := subReqEntryMode(&req, admin)
	if err != nil {
		subFail(w, err)
		return
	}
	if ok, err := h.repo.UpdateSubscription(s.ID, uid, req.Name, req.Format, req.RuleMode, sublink.RefsJSON(refs), routing); err != nil || !ok {
		subFail(w, errors.New("保存失败"))
		return
	}
	if mode != "" && mode != subEntryMode(s) {
		// Plan 297: 手机本地 ↔ 跟主入口 (badge + client title follow at once).
		if _, err := h.repo.SetSubscriptionEntryMode(s.ID, uid, mode); err != nil {
			subFail(w, errors.New("保存失败"))
			return
		}
		s.EntryMode = mode
	}
	if req.EntryNames != nil && (s.EntryNames == nil || *s.EntryNames != *req.EntryNames) {
		// Plan 315: 「节点名带入口名」 on / off (an untouched NULL row stays NULL).
		if _, err := h.repo.SetSubscriptionEntryNames(s.ID, uid, *req.EntryNames); err != nil {
			subFail(w, errors.New("保存失败"))
			return
		}
		v := *req.EntryNames
		s.EntryNames = &v
	}
	h.subState().cache.purgeSub(s.ID)
	h.subLinkGC(uid)
	s.Name, s.Format, s.RuleMode = req.Name, req.Format, req.RuleMode
	response.WriteJSON(w, response.OK(subView(s)))
}

func (h *Handler) subOwned(r *http.Request) (*model.UserSubscription, error) {
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
	s, err := h.repo.GetSubscription(req.ID)
	if err != nil || s == nil || s.UserID != uid {
		return nil, errors.New("订阅不存在")
	}
	return s, nil
}

func (h *Handler) subDelete(w http.ResponseWriter, r *http.Request) {
	s, err := h.subOwned(r)
	if err != nil {
		subFail(w, err)
		return
	}
	if _, err := h.repo.DeleteSubscription(s.ID, s.UserID); err != nil {
		subFail(w, errors.New("删除失败"))
		return
	}
	h.subState().cache.purgeSub(s.ID)
	h.subLinkGC(s.UserID)
	response.WriteJSON(w, response.OKEmpty())
}

// subReset gives the subscription a new token; the old link dies at once.
func (h *Handler) subReset(w http.ResponseWriter, r *http.Request) {
	s, err := h.subOwned(r)
	if err != nil {
		subFail(w, err)
		return
	}
	token, err := sublink.NewToken()
	if err != nil {
		subFail(w, errors.New("生成订阅链接失败"))
		return
	}
	if ok, err := h.repo.SetSubscriptionToken(s.ID, s.UserID, token); err != nil || !ok {
		subFail(w, errors.New("重置失败"))
		return
	}
	st := h.subState()
	st.cache.purgeSub(s.ID)
	// The new link's first pull is recorded at once (「最近拉取」 was cleared).
	st.mu.Lock()
	delete(st.marked, s.ID)
	st.mu.Unlock()
	s.Token = token
	response.WriteJSON(w, response.OK(subView(s)))
}

// ---------------------------------------------------------- sources

// maskSourceURL keeps the host and a harmless first path word; tokens in
// the path or query never leave the server.
func maskSourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "…"
	}
	out := u.Hostname()
	if p := u.Port(); p != "" {
		out += ":" + p
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segs) > 0 && segs[0] != "" {
		first := segs[0]
		plain := len(first) <= 12
		for _, c := range first {
			if !unicode.IsLetter(c) && c != '-' && c != '_' {
				plain = false
			}
		}
		if plain {
			out += "/" + first
			if len(segs) > 1 {
				out += "/…"
			}
		} else {
			out += "/…"
		}
	}
	if u.RawQuery != "" {
		key, _, _ := strings.Cut(u.RawQuery, "&")
		key, _, _ = strings.Cut(key, "=")
		if len(key) > 16 {
			key = key[:16]
		}
		out += "?" + key + "=••••"
	}
	return out
}

func subPreviewItems(res *landingsub.Result) ([]sublink.SourceNode, []interface{}) {
	nodes := []sublink.SourceNode{}
	items := []interface{}{}
	for _, e := range res.Entries {
		reason := e.Err
		// Landing-specific advice after 「：」 does not apply here.
		if i := strings.Index(reason, "："); i > 0 {
			reason = reason[:i]
		}
		item := map[string]interface{}{"name": e.Name, "supported": e.Err == "", "reason": reason}
		if e.Err == "" {
			up := e.Upstream
			up.Tag = ""
			nodes = append(nodes, sublink.SourceNode{Name: e.Name, Up: up, Insecure: e.Insecure})
			item["protocol"], item["detail"] = up.ProtocolLabel(), up.Address()
		} else {
			item["protocol"] = strings.ToUpper(e.Upstream.Type)
			item["detail"] = net.JoinHostPort(e.Upstream.Server, strconv.Itoa(e.Upstream.Port))
		}
		items = append(items, item)
	}
	return nodes, items
}

// subFetchURL downloads and parses a source (SSRF guard of plan 177).
func (h *Handler) subFetchURL(ctx context.Context, raw string) (*landingsub.Result, error) {
	body, err := subFetcher(h).Fetch(ctx, raw)
	if err != nil {
		return nil, err
	}
	// Plan 193: only the first 100 usable nodes are parsed and kept.
	return landingsub.ParseWith(body, landingsub.Options{AllowInsecure: true, MaxNodes: model.MaxSubSourceNodes})
}

func (h *Handler) subTakeFetch(uid int64) error {
	h.external.init()
	if !h.external.fetchRL.Take(strconv.FormatInt(uid, 10)) {
		return fmt.Errorf("拉取太频繁，每分钟最多 %d 次，请稍后再试", subFetchesPerMin)
	}
	return nil
}

// subSourceFetch: 获取 — fetch once and show a preview before saving.
func (h *Handler) subSourceFetch(w http.ResponseWriter, r *http.Request) {
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
	if _, err := landingsub.CheckURL(req.URL); err != nil {
		subFail(w, err)
		return
	}
	// Plan 200: at the cap, say so before fetching anything.
	if n, err := h.repo.CountSubSources(uid); err != nil {
		subFail(w, errors.New("获取失败"))
		return
	} else if n >= model.MaxSubSourcesPerUser {
		subFail(w, subSourceCapErr())
		return
	}
	if err := h.subTakeFetch(uid); err != nil {
		subFail(w, err)
		return
	}
	res, err := h.subFetchURL(r.Context(), strings.TrimSpace(req.URL))
	if err != nil {
		subFail(w, err)
		return
	}
	nodes, items := subPreviewItems(res)
	if len(nodes) == 0 {
		subFail(w, errors.New("订阅里没有可用的节点"))
		return
	}
	id := h.subPutPreview(&subPreview{owner: uid, url: strings.TrimSpace(req.URL), nodes: nodes})
	response.WriteJSON(w, response.OK(map[string]interface{}{"fetchId": id, "items": items, "count": len(nodes), "info": res.Info,
		"invalid": res.Invalid, "truncated": res.Truncated, "total": res.Total, "maxNodes": model.MaxSubSourceNodes, "urlHint": maskSourceURL(req.URL)}))
}

func (h *Handler) subPutPreview(p *subPreview) string {
	s := h.subState()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.previews {
		if now.After(v.expires) || v.owner == p.owner {
			delete(s.previews, k)
		}
	}
	for len(s.previews) >= subPreviewMax {
		for k := range s.previews {
			delete(s.previews, k)
			break
		}
	}
	p.expires = now.Add(subPreviewTTL)
	s.previews[id] = p
	return id
}

func (h *Handler) subTakePreview(id string, owner int64) *subPreview {
	s := h.subState()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[id]
	if !ok || p.owner != owner || time.Now().After(p.expires) {
		return nil
	}
	delete(s.previews, id)
	return p
}

// subSourceCapErr: plan 200 wording (no per-user override; ask the admin).
func subSourceCapErr() error {
	return fmt.Errorf("外部订阅最多 %d 个，如需更多请联系管理员", model.MaxSubSourcesPerUser)
}

func (h *Handler) subSourceCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		FetchID string `json:"fetchId"`
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
	p := h.subTakePreview(req.FetchID, uid)
	if p == nil {
		subFail(w, errors.New("获取结果已过期，请重新获取"))
		return
	}
	enc, err := h.subCrypto().Encrypt([]byte(p.url))
	if err != nil {
		subFail(w, errors.New("保存失败"))
		return
	}
	name := landingsub.CleanName(strings.TrimSpace(req.Name))
	if name == "" {
		if u, err := url.Parse(p.url); err == nil {
			name = landingsub.CleanName(u.Hostname())
		}
	}
	nodesJSON, _ := json.Marshal(p.nodes)
	now := time.Now().UnixMilli()
	src := &model.UserSubSource{UserID: uid, Name: name, URLEnc: enc, URLHint: maskSourceURL(p.url), LastOkAt: now, LastFetchAt: now,
		CachedNodes: string(nodesJSON), NodeCount: len(p.nodes), CreatedTime: now, UpdatedTime: now}
	if err := h.repo.CreateSubSource(src, model.MaxSubSourcesPerUser); err != nil {
		if errors.Is(err, repo.ErrSubCap) {
			err = subSourceCapErr()
		}
		subFail(w, err)
		return
	}
	response.WriteJSON(w, response.OK(subSourceView(*src, 0)))
}

func (h *Handler) subOwnedSource(r *http.Request) (*model.UserSubSource, error) {
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
	s, err := h.repo.GetSubSource(req.ID)
	if err != nil || s == nil || s.UserID != uid {
		return nil, errors.New("订阅源不存在")
	}
	return s, nil
}

// subSourceRefresh: 立即更新 (same limit as the landing importer).
func (h *Handler) subSourceRefresh(w http.ResponseWriter, r *http.Request) {
	s, err := h.subOwnedSource(r)
	if err != nil {
		subFail(w, err)
		return
	}
	if err := h.subTakeFetch(s.UserID); err != nil {
		subFail(w, err)
		return
	}
	updated, ferr := h.subFetchSource(r.Context(), *s)
	h.subState().cache.purgeUser(s.UserID)
	view := subSourceView(updated, 0)
	if ferr != nil {
		view["error"] = ferr.Error()
	}
	response.WriteJSON(w, response.OK(view))
}

func (h *Handler) subSourceDelete(w http.ResponseWriter, r *http.Request) {
	s, err := h.subOwnedSource(r)
	if err != nil {
		subFail(w, err)
		return
	}
	_, touched, err := h.repo.DeleteSubSource(s.ID, s.UserID)
	if err != nil {
		subFail(w, errors.New("删除失败"))
		return
	}
	for _, id := range touched {
		h.subState().cache.purgeSub(id)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"removedFrom": len(touched)}))
}

// ---------------------------------------------------------- lazy refresh

func subSourceStale(s model.UserSubSource, now time.Time) bool {
	if s.LastFetchAt == 0 {
		return true
	}
	age := now.Sub(time.UnixMilli(s.LastFetchAt))
	if s.LastError != "" {
		return age >= subSourceRetry
	}
	return age >= subSourceTTL
}

// subFetchSource refetches one source (single flight per source: callers
// for the same source share one download). The fetch runs on its own
// 10 s context so a client that hangs up does not cancel it for the
// others. On failure the cached nodes stay and the error is recorded.
func (h *Handler) subFetchSource(ctx context.Context, src model.UserSubSource) (model.UserSubSource, error) {
	st := h.subState()
	st.mu.Lock()
	f, running := st.flights[src.ID]
	if !running {
		f = &subFlight{done: make(chan struct{})}
		st.flights[src.ID] = f
	}
	st.mu.Unlock()
	if !running {
		go func() {
			defer func() {
				st.mu.Lock()
				delete(st.flights, src.ID)
				st.mu.Unlock()
				close(f.done)
			}()
			f.src, f.err = h.subDoFetch(src)
		}()
	}
	select {
	case <-f.done:
		return f.src, f.err
	case <-ctx.Done():
		return src, ctx.Err()
	}
}

func (h *Handler) subDoFetch(src model.UserSubSource) (model.UserSubSource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subSourceFetchMax)
	defer cancel()
	now := time.Now().UnixMilli()
	fail := func(err error) (model.UserSubSource, error) {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		_ = h.repo.SaveSubSourceFetch(src.ID, false, "", 0, msg, now)
		src.LastFetchAt, src.LastError = now, msg
		return src, err
	}
	plain, err := h.subCrypto().Decrypt(src.URLEnc)
	if err != nil {
		return fail(errors.New("订阅地址无法解密，请删除后重新添加"))
	}
	res, err := h.subFetchURL(ctx, string(plain))
	if err != nil {
		return fail(err)
	}
	nodes, _ := subPreviewItems(res)
	if len(nodes) == 0 {
		return fail(errors.New("订阅里没有可用的节点"))
	}
	b, _ := json.Marshal(nodes)
	if err := h.repo.SaveSubSourceFetch(src.ID, true, string(b), len(nodes), "", now); err != nil {
		return src, err
	}
	src.CachedNodes, src.NodeCount, src.LastOkAt, src.LastFetchAt, src.LastError = string(b), len(nodes), now, now, ""
	return src, nil
}

// subRefreshRefs refetches the stale external sources refs point at (see
// subRefreshStale); nothing to do for subscriptions without sources.
func (h *Handler) subRefreshRefs(ctx context.Context, userID int64, refs []sublink.Ref) error {
	var ids []int64
	for _, ref := range refs {
		if ref.Kind == sublink.RefSourceNode || ref.Kind == sublink.RefSourceAll {
			ids = append(ids, ref.SourceID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.repo.SubSourcesByIDs(userID, ids)
	if err != nil {
		return err
	}
	h.subRefreshStale(ctx, rows)
	return nil
}

// subRefreshStale refetches stale sources concurrently; the whole pull
// waits at most ~10 s and keeps the old cache of any source that fails.
func (h *Handler) subRefreshStale(ctx context.Context, rows map[int64]model.UserSubSource) map[int64]model.UserSubSource {
	now := time.Now()
	var stale []model.UserSubSource
	for _, s := range rows {
		if subSourceStale(s, now) {
			stale = append(stale, s)
		}
	}
	if len(stale) == 0 {
		return rows
	}
	wait, cancel := context.WithTimeout(ctx, subSourceFetchMax+time.Second)
	defer cancel()
	type result struct{ src model.UserSubSource }
	ch := make(chan result, len(stale))
	for _, s := range stale {
		go func(s model.UserSubSource) {
			updated, _ := h.subFetchSource(wait, s)
			ch <- result{updated}
		}(s)
	}
	out := make(map[int64]model.UserSubSource, len(rows))
	for id, s := range rows {
		out[id] = s
	}
	for range stale {
		res := <-ch
		out[res.src.ID] = res.src
	}
	return out
}

// SetSubPullBurst raises the per-subscription pull allowance (contract
// tests that pull every client format many times; production keeps 60/h).
func (h *Handler) SetSubPullBurst(burst int) {
	h.subState().tokenRL = &ratelimit.Bucket{Burst: float64(burst), Every: time.Millisecond, MaxKeys: 10000}
}
