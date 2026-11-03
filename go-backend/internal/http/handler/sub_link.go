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
		"list":           h.subList,
		"pool":           h.subPool,
		"create":         h.subCreate,
		"update":         h.subUpdate,
		"delete":         h.subDelete,
		"reset":          h.subReset,
		"source/fetch":   h.subSourceFetch,
		"source/create":  h.subSourceCreate,
		"source/refresh": h.subSourceRefresh,
		"source/delete":  h.subSourceDelete,
	} {
		fn := fn
		list := path == "list"
		mux.HandleFunc("/api/v1/sub/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if _, _, err := userRoleFromRequest(r); err != nil {
				response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
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
}

// subCounts summarizes a subscription for its card.
type subCounts struct {
	Panel    int              `json:"panel"`
	External int              `json:"external"`
	Missing  int              `json:"missing"`
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
	for _, ref := range refs {
		switch ref.Kind {
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
		sources: map[int64]model.UserSubSource{}, srcNodes: map[int64][]sublink.SourceNode{}}
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
			}
		}
	}
	if all || len(ext) > 0 {
		var ids []int64
		if !all {
			ids = ext
		}
		rows, err := h.repo.ExternalNodesByIDs(userID, ids)
		if err != nil {
			return nil, err
		}
		for _, e := range rows {
			d.externals[e.ID] = e
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
		if list := d.byEntry[strconv.FormatInt(ref.ForwardID, 10)+"|"+node]; len(list) == 1 {
			return list[0], true
		}
	}
	return repo.SubPanelLink{}, false
}

// resolve turns refs into nodes (in order) and card counts. At most max
// nodes are built (plan 193: a render needs only the first 100; the counts
// still cover every node, so an over-limit subscription shows its total).
func (d *subData) resolve(refs []sublink.Ref, max int) ([]sublink.Node, subCounts) {
	nodes := []sublink.Node{}
	c := subCounts{Sources: []subSourceCount{}}
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
	for _, ref := range refs {
		switch ref.Kind {
		case sublink.RefForwardLink, sublink.RefLandingDirect:
			if l, ok := d.panelLink(ref); ok {
				keep(l.Node)
				c.Panel++
				continue
			}
		case sublink.RefExternalNode:
			if e, ok := d.externals[ref.ExternalID]; ok {
				if n, ok := externalNode(e); ok {
					keep(n)
					c.External++
					continue
				}
			}
		case sublink.RefSourceAll:
			if _, ok := d.sources[ref.SourceID]; ok {
				list := d.srcNodes[ref.SourceID]
				for _, sn := range list {
					keep(sn.Node())
				}
				countSrc(ref.SourceID, true, len(list))
				continue
			}
		case sublink.RefSourceNode:
			if _, ok := d.sources[ref.SourceID]; ok {
				found := false
				for _, sn := range d.srcNodes[ref.SourceID] {
					if sn.Name == ref.Name {
						keep(sn.Node())
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
	for _, r := range refs {
		if r.Kind == sublink.RefSourceAll {
			follow[r.SourceID] = true
		}
	}
	out := refs[:0]
	for _, r := range refs {
		if r.Kind == sublink.RefSourceNode && follow[r.SourceID] {
			continue
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
		"sourceCap": model.MaxSubSourcesPerUser, "maxNodes": model.MaxSubscriptionNodes}
	if admin {
		out["cap"] = 0
	}
	if !enabled {
		response.WriteJSON(w, response.OK(out))
		return
	}
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
	usedBy := map[int64]int{}
	views := []interface{}{}
	for _, s := range subs {
		refs := sublink.ParseRefs(s.Items)
		_, counts := d.resolve(refs, 0)
		seen := map[int64]bool{}
		for _, ref := range refs {
			if (ref.Kind == sublink.RefSourceAll || ref.Kind == sublink.RefSourceNode) && !seen[ref.SourceID] {
				seen[ref.SourceID] = true
				usedBy[ref.SourceID]++
			}
		}
		views = append(views, map[string]interface{}{"id": s.ID, "name": s.Name, "token": s.Token, "format": s.Format, "ruleMode": s.RuleMode,
			"counts": counts, "items": len(refs), "lastPullAt": s.LastPullAt, "lastPullClient": s.LastPullClient, "lastPullNodes": s.LastPullNodes, "createdTime": s.CreatedTime})
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
	if req.ID > 0 {
		s, err := h.repo.GetSubscription(req.ID)
		if err != nil || s == nil || s.UserID != uid {
			subFail(w, errors.New("订阅不存在"))
			return
		}
		refs = sublink.ParseRefs(s.Items)
	}
	d, err := h.subLoad(r.Context(), uid, refs, true, false)
	if err != nil {
		subFail(w, err)
		return
	}
	item := func(ref sublink.Ref, n sublink.Node, detail string) map[string]interface{} {
		return map[string]interface{}{"ref": subRefView(ref), "key": ref.Key(), "name": n.Name, "protocol": n.ProtocolLabel(), "detail": detail}
	}
	rules, landing, externals := []interface{}{}, []interface{}{}, []interface{}{}
	for _, l := range d.panel.Links {
		it := item(l.Ref, l.Node, l.Detail)
		if l.Rule {
			it["chain"] = l.Chain
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
	// Selected items, in order, with the missing ones marked.
	selected := []interface{}{}
	for _, ref := range refs {
		v := map[string]interface{}{"ref": subRefView(ref), "key": ref.Key()}
		switch ref.Kind {
		case sublink.RefForwardLink, sublink.RefLandingDirect:
			if l, ok := d.panelLink(ref); ok {
				v["name"], v["protocol"], v["group"] = l.Node.Name, l.Node.ProtocolLabel(), "landing"
				if l.Rule {
					v["group"], v["chain"] = "rule", l.Chain
				}
				v["key"] = l.Ref.Key()
				v["ref"] = subRefView(l.Ref)
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
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"rules": rules, "landing": landing, "external": externals, "sources": sources, "portForwards": d.panel.PortForwards,
		"selected": selected, "maxNodes": model.MaxSubscriptionNodes,
	}))
}

type subSaveReq struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	Format   string        `json:"format"`
	RuleMode string        `json:"ruleMode"`
	Items    []sublink.Ref `json:"items"`
}

// subValidate checks a save request against the user's own data: only refs
// that resolve are kept (other users' ids never do), at most 100 nodes.
func (h *Handler) subValidate(ctx context.Context, uid int64, req *subSaveReq) ([]sublink.Ref, error) {
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
		return nil, errors.New("客户端或分流设置无效")
	}
	refs, err := sublink.CleanRefs(req.Items)
	if err != nil {
		return nil, err
	}
	refs = normalizeRefs(refs)
	d, err := h.subLoad(ctx, uid, refs, false, false)
	if err != nil {
		return nil, err
	}
	kept := make([]sublink.Ref, 0, len(refs))
	total := 0
	for _, ref := range refs {
		_, c := d.resolve([]sublink.Ref{ref}, 0)
		if c.Missing > 0 {
			continue
		}
		if ref.Kind == sublink.RefForwardLink {
			// Keep the current entry key (an address change resolved by node).
			if l, ok := d.panelLink(ref); ok {
				ref = l.Ref
			}
		}
		total += c.Total
		kept = append(kept, ref)
	}
	if total > model.MaxSubscriptionNodes {
		return nil, fmt.Errorf("每条订阅最多 %d 个节点，现在是 %d 个，如需更多请联系管理员", model.MaxSubscriptionNodes, total)
	}
	return kept, nil
}

func subView(s *model.UserSubscription) map[string]interface{} {
	return map[string]interface{}{"id": s.ID, "name": s.Name, "token": s.Token, "format": s.Format, "ruleMode": s.RuleMode}
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
	refs, err := h.subValidate(r.Context(), uid, &req)
	if err != nil {
		subFail(w, err)
		return
	}
	token, err := sublink.NewToken()
	if err != nil {
		subFail(w, errors.New("生成订阅链接失败"))
		return
	}
	now := time.Now().UnixMilli()
	s := &model.UserSubscription{UserID: uid, Name: req.Name, Token: token, Format: req.Format, RuleMode: req.RuleMode, Items: sublink.RefsJSON(refs), CreatedTime: now, UpdatedTime: now}
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
	response.WriteJSON(w, response.OK(subView(s)))
}

func (h *Handler) subUpdate(w http.ResponseWriter, r *http.Request) {
	var req subSaveReq
	if err := subDecode(r, &req); err != nil {
		subFail(w, err)
		return
	}
	uid, _, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	s, err := h.repo.GetSubscription(req.ID)
	if err != nil || s == nil || s.UserID != uid {
		subFail(w, errors.New("订阅不存在"))
		return
	}
	refs, err := h.subValidate(r.Context(), uid, &req)
	if err != nil {
		subFail(w, err)
		return
	}
	if ok, err := h.repo.UpdateSubscription(s.ID, uid, req.Name, req.Format, req.RuleMode, sublink.RefsJSON(refs)); err != nil || !ok {
		subFail(w, errors.New("保存失败"))
		return
	}
	h.subState().cache.purgeSub(s.ID)
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
