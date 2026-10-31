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
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/landingsub"
	"go-backend/internal/linechain"
	"go-backend/internal/ratelimit"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 177 「外部节点」 + 「链式代理」: a landing node's traffic can leave
// through one of its owner's own nodes outside FLVX. External nodes are a
// per-user list (pasted links or a one-shot subscription import); their
// credentials never go back to a browser.

// Subscription fetch cache and limits (memory only, bounded).
const (
	subCacheTTL        = 10 * time.Minute
	subCachePerUser    = 3
	subCacheMax        = 64
	subFetchesPerMin   = 5
	chainProbesPerMin  = 10
	externalNameMaxLen = 40
)

type subFetch struct {
	owner, actor int64
	entries      []landingsub.Entry
	expires      time.Time
}

type externalState struct {
	mu      sync.Mutex
	fetches map[string]*subFetch
	// notes: last compile's chain notes per landing server (plan 177 card
	// state without compiling on every list).
	notes   map[int64]map[int64]string
	fetchRL *ratelimit.Bucket
	probeRL *ratelimit.Bucket
	once    sync.Once
}

func (s *externalState) init() {
	s.once.Do(func() {
		s.fetches = map[string]*subFetch{}
		s.notes = map[int64]map[int64]string{}
		s.fetchRL = &ratelimit.Bucket{Burst: subFetchesPerMin, Every: time.Minute / subFetchesPerMin, MaxKeys: 4096}
		s.probeRL = &ratelimit.Bucket{Burst: chainProbesPerMin, Every: time.Minute / chainProbesPerMin, MaxKeys: 4096}
	})
}

func (s *externalState) setChainNotes(serverID int64, notes map[int64]string, err error) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil || len(notes) == 0 {
		delete(s.notes, serverID)
		return
	}
	s.notes[serverID] = notes
}

func (s *externalState) chainNote(serverID, nodeID int64) string {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes[serverID][nodeID]
}

// putFetch stores a parsed subscription; old entries of the same user and
// expired ones go first, the whole cache stays bounded.
func (s *externalState) putFetch(f *subFetch, now time.Time) string {
	s.init()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	var mine []string
	for k, v := range s.fetches {
		if now.After(v.expires) {
			delete(s.fetches, k)
			continue
		}
		if v.actor == f.actor {
			mine = append(mine, k)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return s.fetches[mine[i]].expires.Before(s.fetches[mine[j]].expires) })
	for len(mine) >= subCachePerUser {
		delete(s.fetches, mine[0])
		mine = mine[1:]
	}
	for len(s.fetches) >= subCacheMax {
		oldest := ""
		for k, v := range s.fetches {
			if oldest == "" || v.expires.Before(s.fetches[oldest].expires) {
				oldest = k
			}
		}
		delete(s.fetches, oldest)
	}
	f.expires = now.Add(subCacheTTL)
	s.fetches[id] = f
	return id
}

func (s *externalState) getFetch(id string, actor, owner int64, now time.Time) *subFetch {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fetches[id]
	if !ok || now.After(f.expires) || f.actor != actor || f.owner != owner {
		return nil
	}
	return f
}

func (s *externalState) dropFetch(id string) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fetches, id)
}

// subFetcher is the SSRF-guarded client; tests replace it.
var subFetcher = func(h *Handler) *landingsub.Fetcher {
	return &landingsub.Fetcher{Resolver: chainResolver, Forbidden: h.chainForbidden()}
}

func (h *Handler) registerLandingExternal(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{
		"external/list":       h.landingExternalList,
		"external/parse":      h.landingExternalParse,
		"external/create":     h.limitMutation(mutationLanding, h.landingExternalCreate),
		"external/rename":     h.limitMutation(mutationLanding, h.landingExternalRename),
		"external/delete":     h.limitMutation(mutationLanding, h.landingExternalDelete),
		"external/sub/fetch":  h.landingExternalSubFetch,
		"external/sub/import": h.limitMutation(mutationLanding, h.landingExternalSubImport),
		"node/chain/save":     h.limitMutation(mutationLanding, h.landingNodeChainSave),
		"node/chain/probe":    h.landingNodeChainProbe,
	} {
		mux.HandleFunc("/api/v1/landing/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if _, _, err := userRoleFromRequest(r); err != nil {
				response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
				return
			}
			fn(w, r)
		})
	}
}

// externalOwner: whose list a request works on. Users always their own;
// admins their own or (userId) a user's, acting for the node owner.
func (h *Handler) externalOwner(r *http.Request, requested int64) (actor, owner int64, cap int, err error) {
	uid, role, err := userRoleFromRequest(r)
	if err != nil {
		return 0, 0, 0, err
	}
	owner = uid
	if role == 0 && requested > 0 {
		owner = requested
	}
	u, err := h.repo.GetUserByID(owner)
	if err != nil || u == nil {
		return 0, 0, 0, errors.New("用户不存在")
	}
	cap = model.MaxExternalNodesPerUser
	if u.RoleID == 0 {
		cap = 0 // admins: unlimited
	}
	return uid, owner, cap, nil
}

// externalForbidden: the plan-108 guard (FLVX nodes, panel) plus every
// landing server's public host, so our own landings never chain to each
// other (shared mmwx servers have no FLVX node row).
func (h *Handler) externalForbidden() linechain.Forbidden {
	base := h.chainForbidden()
	landing := map[netip.Addr]bool{}
	if hosts, err := h.repo.LandingPublicHosts(); err == nil {
		for _, host := range hosts {
			host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
			if a, err := netip.ParseAddr(host); err == nil {
				landing[a.Unmap()] = true
				continue
			}
			for _, a := range h.egressResolve(host) {
				landing[a.Unmap()] = true
			}
		}
	}
	return func(a netip.Addr) (string, bool) {
		if why, bad := base(a); bad {
			return why, true
		}
		if landing[a.Unmap()] {
			return "FLVX 落地服务器", true
		}
		return "", false
	}
}

// validateExternal checks a parsed upstream's address like a pasted plan-108
// node (resolved IPs, FLVX/panel/landing addresses, mainland policy).
func (h *Handler) validateExternal(ctx context.Context, up linechain.Upstream, forbidden linechain.Forbidden) ([]netip.Addr, error) {
	addrs, err := linechain.ValidateAddress(ctx, up.Server, forbidden, chainResolver)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(addrs))
	for _, a := range addrs {
		targets = append(targets, net.JoinHostPort(a.String(), strconv.Itoa(up.Port)))
	}
	if err := h.checkForwardLanding(ctx, strings.Join(targets, ","), "gost", ""); err != nil {
		return nil, err
	}
	return addrs, nil
}

// chainPlanOutbound turns an external node into the plan outbound a server
// can apply, or says why it cannot. Native agents take every plan-108 type
// as is (same JSON); the mmwx Helper only its egress whitelist (SOCKS5 /
// HTTP / Shadowsocks AEAD·2022 / VLESS Reality over TCP), in the plan-107
// field form it decodes strictly.
func chainPlanOutbound(up linechain.Upstream, shared bool) (landingegress.PlanOutbound, string) {
	var po landingegress.PlanOutbound
	b, _ := json.Marshal(up)
	if err := json.Unmarshal(b, &po); err != nil {
		return po, "外部节点数据损坏，请删除后重新添加"
	}
	po.Tag, po.IPVersion = "", "system"
	if !shared {
		return po, ""
	}
	if reason := sharedChainReason(up); reason != "" {
		return po, reason
	}
	plain := landingegress.PlanOutbound{Type: up.Type, IPVersion: "system", Server: up.Server, Port: up.Port}
	switch up.Type {
	case "socks", "http":
		plain.Username, plain.Password = up.Username, up.Password
	case "shadowsocks":
		plain.Method, plain.Password = up.Method, up.Password
	case "vless":
		plain.UUID, plain.Flow = up.UUID, up.Flow
		plain.Reality = &landingegress.PlanReality{ServerName: up.Reality.ServerName, PublicKey: up.Reality.PublicKey, ShortID: up.Reality.ShortID, Fingerprint: up.Reality.Fingerprint}
	}
	return plain, ""
}

// SharedChainProtocols names what shared servers accept, for messages.
const sharedChainProtocols = "SOCKS5 / HTTP / Shadowsocks / VLESS Reality"

// sharedChainReason: "" when the mmwx Helper accepts the upstream.
func sharedChainReason(up linechain.Upstream) string {
	unsupported := "此服务器不支持该协议（共享服务器只支持 " + sharedChainProtocols + "）"
	switch up.Type {
	case "socks":
		if up.Security != "" || up.Network != "" || up.TLS != nil {
			return unsupported
		}
	case "http":
		if up.Security == "tls" || up.TLS != nil || up.Network != "" {
			return "此服务器不支持 HTTPS 代理（共享服务器只支持 " + sharedChainProtocols + "）"
		}
	case "shadowsocks":
		if up.Network != "" || up.Security != "" {
			return unsupported
		}
	case "vless":
		if up.Security != "reality" || up.Reality == nil || (up.Network != "" && up.Network != "tcp") || up.WS != nil || up.GRPC != nil || up.TLS != nil {
			return "此服务器只支持 TCP 的 VLESS Reality（共享服务器只支持 " + sharedChainProtocols + "）"
		}
		if _, err := netip.ParseAddr(up.Reality.ServerName); err == nil || up.Reality.ServerName == "" {
			return "VLESS Reality 的 SNI 必须是域名"
		}
	default:
		return unsupported
	}
	return ""
}

// landingChainSupport: can this server carry 链式代理 at all (button state).
func landingChainSupport(d *repo.LandingEgressData) (bool, string) {
	supported, _, _, reason := landingEgressSupport(d)
	if !supported {
		if d.Server.Source == "native" {
			return false, "节点程序需要升级后才能使用链式代理"
		}
		return false, reason
	}
	if d.Server.Source == "native" && !d.Server.EgressReady && !d.Server.EgressRestartOK {
		return false, "该服务器的落地程序需要先重启一次才能使用链式代理，请联系管理员在服务器设置里确认"
	}
	return true, ""
}

// landingChainOutbound resolves a chained node's external node for the plan.
func landingChainOutbound(d *repo.LandingEgressData, n model.LandingNode) (*landingegress.PlanOutbound, string) {
	ext, ok := d.Externals[n.ChainExternalID]
	if !ok || ext.UserID != n.UserID {
		return nil, "外部节点已删除，链式代理未生效，本节点的流量暂时封锁"
	}
	up, err := linechain.FromJSON(ext.Upstream)
	if err != nil {
		return nil, "外部节点数据损坏，请删除后重新添加"
	}
	po, reason := chainPlanOutbound(up, d.Server.Source == "shared")
	if reason != "" {
		return nil, reason + "，本节点的流量暂时封锁"
	}
	return &po, ""
}

// ---------------------------------------------------------------- views

func externalItemView(n model.LandingExternalNode, uses []repo.LandingExternalUse) map[string]interface{} {
	up, _ := linechain.FromJSON(n.Upstream)
	reason := sharedChainReason(up)
	if uses == nil {
		uses = []repo.LandingExternalUse{}
	}
	return map[string]interface{}{
		"id": n.ID, "name": n.Name, "protocol": n.Protocol, "protocolLabel": up.ProtocolLabel(), "host": n.Host, "port": n.Port,
		"summary": n.Summary, "udpOnly": up.UDPOnly(), "sharedOk": reason == "", "sharedReason": reason, "inUse": uses, "createdTime": n.CreatedTime,
	}
}

func upstreamPreview(up linechain.Upstream, name string) map[string]interface{} {
	reason := sharedChainReason(up)
	return map[string]interface{}{"protocol": up.Type, "protocolLabel": up.ProtocolLabel(), "host": up.Server, "port": up.Port, "summary": up.Summary(), "name": name, "udpOnly": up.UDPOnly(), "sharedOk": reason == "", "sharedReason": reason}
}

func externalName(name string, up linechain.Upstream) (string, error) {
	name = landingsub.CleanName(name)
	if name == "" {
		name = landingsub.CleanName(up.ProtocolLabel() + " " + up.Address())
	}
	if utf8.RuneCountInString(name) > externalNameMaxLen {
		return "", fmt.Errorf("名称最多 %d 个字", externalNameMaxLen)
	}
	return name, nil
}

func externalModel(up linechain.Upstream, name string) model.LandingExternalNode {
	return model.LandingExternalNode{Name: name, Protocol: up.Type, Host: up.Server, Port: up.Port, Summary: up.Summary(), Upstream: up.JSON(), UpstreamHash: up.Hash()}
}

func (h *Handler) landingExternalList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"userId"`
	}
	_ = landingDecode(r, &req)
	_, owner, cap, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	nodes, err := h.repo.ListLandingExternalNodes(owner)
	if err != nil {
		landingError(w, err)
		return
	}
	uses, err := h.repo.LandingExternalUses(owner)
	if err != nil {
		landingError(w, err)
		return
	}
	items := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, externalItemView(n, uses[n.ID]))
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"items": items, "cap": cap, "count": len(nodes), "userId": owner}))
}

func (h *Handler) landingExternalParse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link string `json:"link"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	up, name, err := linechain.ParseLink(req.Link)
	if err != nil {
		landingError(w, err)
		return
	}
	addrs, err := h.validateExternal(r.Context(), up, h.externalForbidden())
	if err != nil {
		landingError(w, err)
		return
	}
	view := upstreamPreview(up, landingsub.CleanName(name))
	if len(addrs) > 0 && addrs[0].String() != up.Server {
		view["resolved"] = addrs[0].String()
	}
	response.WriteJSON(w, response.OK(view))
}

func (h *Handler) landingExternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link   string `json:"link"`
		Name   string `json:"name"`
		UserID int64  `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	_, owner, cap, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	up, linkName, err := linechain.ParseLink(req.Link)
	if err != nil {
		landingError(w, err)
		return
	}
	if _, err := h.validateExternal(r.Context(), up, h.externalForbidden()); err != nil {
		landingError(w, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = linkName
	}
	name, err := externalName(req.Name, up)
	if err != nil {
		landingError(w, err)
		return
	}
	res, err := h.repo.CreateLandingExternalNodes(owner, []model.LandingExternalNode{externalModel(up, name)}, cap)
	if err != nil {
		landingError(w, err)
		return
	}
	switch {
	case len(res.Duplicates) > 0:
		landingError(w, fmt.Errorf("已经添加过这个节点（「%s」）", res.Duplicates[0]))
		return
	case res.OverCap > 0:
		landingError(w, fmt.Errorf("外部节点最多 %d 个，请先删除不用的再添加", cap))
		return
	}
	response.WriteJSON(w, response.OK(externalItemView(res.Created[0], nil)))
}

func (h *Handler) landingExternalRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		UserID int64  `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	_, owner, _, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	name := landingsub.CleanName(req.Name)
	if name == "" {
		landingError(w, errors.New("请填写名称"))
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Name)) > externalNameMaxLen {
		landingError(w, fmt.Errorf("名称最多 %d 个字", externalNameMaxLen))
		return
	}
	if err := h.repo.RenameLandingExternalNode(req.ID, owner, name); err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": req.ID, "name": name}))
}

func (h *Handler) landingExternalDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64 `json:"id"`
		UserID int64 `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	_, owner, _, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	uses, err := h.repo.DeleteLandingExternalNode(req.ID, owner)
	if err != nil {
		landingError(w, err)
		return
	}
	if len(uses) > 0 {
		landingError(w, repo.ExternalInUseError(uses))
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": req.ID}))
}

// ---------------------------------------------------------- subscription

func (h *Handler) landingExternalSubFetch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		UserID int64  `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	actor, owner, cap, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	if _, err := landingsub.CheckURL(req.URL); err != nil {
		landingError(w, err)
		return
	}
	h.external.init()
	if !h.external.fetchRL.Take(strconv.FormatInt(actor, 10)) {
		landingError(w, fmt.Errorf("拉取太频繁，每分钟最多 %d 次，请稍后再试", subFetchesPerMin))
		return
	}
	body, err := subFetcher(h).Fetch(r.Context(), req.URL)
	if err != nil {
		landingError(w, err)
		return
	}
	parsed, err := landingsub.Parse(body)
	if err != nil {
		landingError(w, err)
		return
	}
	existing, err := h.repo.ListLandingExternalNodes(owner)
	if err != nil {
		landingError(w, err)
		return
	}
	have := map[string]string{}
	for _, e := range existing {
		have[e.UpstreamHash] = e.Name
	}
	forbidden := h.externalForbidden()
	items := make([]map[string]interface{}, 0, len(parsed.Entries))
	for i := range parsed.Entries {
		e := &parsed.Entries[i]
		// IP literals are checked now (no DNS); domains on import.
		if e.Err == "" {
			if a, err := netip.ParseAddr(e.Upstream.Server); err == nil {
				if why, bad := forbidden(a.Unmap()); bad || linechain.BlockedAddr(a) {
					if why == "" {
						why = "内网或保留地址"
					}
					e.Err = "节点地址是" + why + "，不能使用"
				}
			}
		}
		item := map[string]interface{}{"index": i, "name": e.Name, "protocol": e.Upstream.Type, "host": e.Upstream.Server, "port": e.Upstream.Port, "supported": e.Err == "", "reason": e.Err}
		if e.Err == "" {
			reason := sharedChainReason(e.Upstream)
			item["protocolLabel"], item["summary"], item["sharedOk"], item["sharedReason"], item["udpOnly"] = e.Upstream.ProtocolLabel(), e.Upstream.Summary(), reason == "", reason, e.Upstream.UDPOnly()
			if name, dup := have[e.Upstream.Hash()]; dup {
				item["duplicate"] = name
			}
		} else {
			item["protocolLabel"] = strings.ToUpper(e.Upstream.Type)
			item["summary"] = strings.ToUpper(e.Upstream.Type) + " · " + net.JoinHostPort(e.Upstream.Server, strconv.Itoa(e.Upstream.Port))
		}
		items = append(items, item)
	}
	id := h.external.putFetch(&subFetch{owner: owner, actor: actor, entries: parsed.Entries}, time.Now())
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"fetchId": id, "items": items, "info": parsed.Info, "invalid": parsed.Invalid, "truncated": parsed.Truncated,
		"cap": cap, "count": len(existing), "expiresIn": int(subCacheTTL / time.Second),
	}))
}

func (h *Handler) landingExternalSubImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FetchID string `json:"fetchId"`
		Indexes []int  `json:"indexes"`
		UserID  int64  `json:"userId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	actor, owner, cap, err := h.externalOwner(r, req.UserID)
	if err != nil {
		landingError(w, err)
		return
	}
	f := h.external.getFetch(req.FetchID, actor, owner, time.Now())
	if f == nil {
		landingError(w, errors.New("订阅结果已过期（10 分钟），请重新获取"))
		return
	}
	seen := map[int]bool{}
	var picked []landingsub.Entry
	for _, i := range req.Indexes {
		if i < 0 || i >= len(f.entries) || seen[i] {
			continue
		}
		seen[i] = true
		if f.entries[i].Err == "" {
			picked = append(picked, f.entries[i])
		}
	}
	if len(picked) == 0 {
		landingError(w, errors.New("请勾选要导入的节点"))
		return
	}
	if cap > 0 && len(picked) > cap {
		picked = picked[:cap+1] // enough to report 已达上限 without resolving everything
	}
	// Domains are resolved and checked now (bounded parallelism, one shared
	// deadline); the panel never stores a node it would refuse as a paste.
	forbidden := h.externalForbidden()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	type outcome struct {
		item model.LandingExternalNode
		err  string
	}
	results := make([]outcome, len(picked))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range picked {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			e := picked[i]
			if _, err := h.validateExternal(ctx, e.Upstream, forbidden); err != nil {
				results[i].err = err.Error()
				return
			}
			name, err := externalName(e.Name, e.Upstream)
			if err != nil {
				results[i].err = err.Error()
				return
			}
			results[i].item = externalModel(e.Upstream, name)
		}(i)
	}
	wg.Wait()
	var items []model.LandingExternalNode
	failed := []map[string]string{}
	for i, res := range results {
		if res.err != "" {
			failed = append(failed, map[string]string{"name": picked[i].Name, "reason": res.err})
			continue
		}
		items = append(items, res.item)
	}
	created := &repo.ExternalCreateResult{}
	if len(items) > 0 {
		if created, err = h.repo.CreateLandingExternalNodes(owner, items, cap); err != nil {
			landingError(w, err)
			return
		}
	}
	if len(created.Created) > 0 {
		h.external.dropFetch(req.FetchID)
	}
	out := make([]map[string]interface{}, 0, len(created.Created))
	for _, n := range created.Created {
		out = append(out, externalItemView(n, nil))
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"imported": len(created.Created), "items": out, "duplicates": created.Duplicates, "overCap": created.OverCap, "failed": failed, "cap": cap,
	}))
}

// ----------------------------------------------------------- 链式代理

// chainTargetFor loads the landing node (owner or admin) and, for id > 0,
// the external node, which must belong to the landing node's owner.
func (h *Handler) chainTargetFor(r *http.Request, nodeID, externalID int64) (*model.LandingNode, *model.LandingExternalNode, *repo.LandingEgressData, error) {
	n, err := h.landingOwnedNode(r, nodeID)
	if err != nil {
		return nil, nil, nil, err
	}
	var ext *model.LandingExternalNode
	if externalID > 0 {
		if ext, err = h.repo.GetLandingExternalNode(externalID, n.UserID); err != nil {
			return nil, nil, nil, err
		}
	}
	d, err := h.repo.LandingEgressData(n.LandingServerID)
	if err != nil || d == nil {
		return nil, nil, nil, errors.New("落地服务器不存在")
	}
	if d.Server.ChainOnly {
		return nil, nil, nil, errors.New("该节点不能使用链式代理")
	}
	return n, ext, d, nil
}

type chainProbeResult struct {
	Probed    bool   `json:"probed"`
	OK        bool   `json:"ok"`
	LatencyMS int    `json:"latencyMs,omitempty"`
	Server    string `json:"server"`
	Target    string `json:"target"`
	Message   string `json:"message,omitempty"`
}

// chainProbeNative tests TCP reachability of the external node from the
// landing server (the plan-108 TcpPing path, two pings, 3 s each).
var chainProbeNative = func(h *Handler, nodeID int64, ip string, port int) (bool, int, string) {
	data, err := h.tcpPingViaNode(nodeID, ip, port, diagnosisExecOptions{commandTimeout: 10 * time.Second, pingTimeoutMS: 3000, pingCount: 2})
	if err != nil {
		return false, 0, err.Error()
	}
	if asBool(data["success"], false) {
		return true, int(asFloat(data["averageTime"], 0) + 0.5), ""
	}
	msg := strings.TrimSpace(asString(data["errorMessage"]))
	if msg == "" {
		msg = "TCP 连接失败"
	}
	return false, 0, msg
}

func (h *Handler) probeChain(ctx context.Context, d *repo.LandingEgressData, ext *model.LandingExternalNode) (chainProbeResult, error) {
	res := chainProbeResult{Server: d.Server.Name, Target: net.JoinHostPort(ext.Host, strconv.Itoa(ext.Port))}
	up, err := linechain.FromJSON(ext.Upstream)
	if err != nil {
		return res, errors.New("外部节点数据损坏，请删除后重新添加")
	}
	if d.Server.Source == "shared" {
		res.Message = "共享服务器上没有 FLVX 节点程序，无法检测；保存后看同步结果"
		return res, nil
	}
	if up.UDPOnly() {
		res.Message = up.ProtocolLabel() + " 走 UDP，无法用 TCP 检测；保存后直接试连"
		return res, nil
	}
	addrs, err := h.validateExternal(ctx, up, h.externalForbidden())
	if err != nil {
		return res, err
	}
	if node, err := h.repo.GetNodeByID(d.Server.NodeID); err != nil || node == nil || node.Status != 1 {
		return res, errors.New("落地服务器离线，暂时无法检测")
	}
	res.Probed = true
	res.OK, res.LatencyMS, res.Message = chainProbeNative(h, d.Server.NodeID, addrs[0].String(), up.Port)
	return res, nil
}

func (h *Handler) landingNodeChainProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int64 `json:"id"`
		ExternalID int64 `json:"externalId"`
	}
	if err := landingDecode(r, &req); err != nil || req.ExternalID <= 0 {
		landingError(w, errors.New("请选择外部节点"))
		return
	}
	uid, _, _ := userRoleFromRequest(r)
	_, ext, d, err := h.chainTargetFor(r, req.ID, req.ExternalID)
	if err != nil {
		landingError(w, err)
		return
	}
	h.external.init()
	if !h.external.probeRL.Take(strconv.FormatInt(uid, 10)) {
		landingError(w, errors.New("检测太频繁，请稍后再试"))
		return
	}
	res, err := h.probeChain(r.Context(), d, ext)
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(res))
}

func (h *Handler) landingNodeChainSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int64 `json:"id"`
		ExternalID int64 `json:"externalId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	n, ext, d, err := h.chainTargetFor(r, req.ID, req.ExternalID)
	if err != nil {
		landingError(w, err)
		return
	}
	var probe *chainProbeResult
	if ext != nil {
		if ok, reason := landingChainSupport(d); !ok {
			landingError(w, errors.New(reason))
			return
		}
		up, err := linechain.FromJSON(ext.Upstream)
		if err != nil {
			landingError(w, errors.New("外部节点数据损坏，请删除后重新添加"))
			return
		}
		if _, reason := chainPlanOutbound(up, d.Server.Source == "shared"); reason != "" {
			landingError(w, errors.New(reason))
			return
		}
		// The server must have room for it without touching anyone else:
		// no node may lose its chain (outbound room) because of this save.
		_, before, err := h.compileLandingReport(d, nil, false)
		if err != nil {
			landingError(w, fmt.Errorf("落地服务器「%s」：%w", d.Server.Name, err))
			return
		}
		_, rep, err := h.compileLandingReport(d, func(d *repo.LandingEgressData) {
			for i := range d.Nodes {
				if d.Nodes[i].ID == n.ID {
					d.Nodes[i].ChainExternalID = ext.ID
				}
			}
			d.Externals[ext.ID] = *ext
		}, false)
		if err != nil {
			landingError(w, fmt.Errorf("落地服务器「%s」：%w", d.Server.Name, err))
			return
		}
		if note := rep.ChainNotes[n.ID]; note != "" {
			landingError(w, errors.New(chainNoteReason(note)))
			return
		}
		if added := chainNotesAdded(before.ChainNotes, rep.ChainNotes, n.ID); len(added) > 0 {
			landingError(w, fmt.Errorf("该服务器的出站已满（每台服务器最多 %d 个不同的出站），请换一台服务器，或选一个这台服务器上已在用的外部节点", landingegress.MaxPlanOutbounds))
			return
		}
		res, err := h.probeChain(r.Context(), d, ext)
		if err != nil {
			landingError(w, err)
			return
		}
		if res.Probed && !res.OK {
			landingError(w, fmt.Errorf("「%s」连不上外部节点 %s：%s（确认外部节点在线后再保存）", res.Server, res.Target, res.Message))
			return
		}
		probe = &res
	}
	if _, err := h.repo.SetLandingNodeChain(n.ID, req.ExternalID); err != nil {
		landingError(w, err)
		return
	}
	_, syncErr := h.syncLandingServer(n.LandingServerID)
	cur, err := h.repo.GetLandingNode(n.ID)
	if err != nil || cur == nil {
		landingError(w, errors.New("落地节点不存在"))
		return
	}
	view := h.landingNodeView(*cur)
	if _, role, _ := userRoleFromRequest(r); role != 0 {
		hideLandingNodeEgressInternals(view)
	}
	out := map[string]interface{}{"node": view, "probe": probe}
	if syncErr != nil {
		out["syncError"] = syncErr.Error()
	}
	response.WriteJSON(w, response.OK(out))
}

// chainNoteReason trims the "traffic blocked" tail of a compile note for a
// save refusal.
func chainNoteReason(note string) string {
	return strings.TrimSuffix(strings.TrimSuffix(note, "，本节点的流量暂时封锁"), "，链式代理未生效")
}

// chainNotesAdded lists nodes (other than skip) whose chain stops working
// after a change: notes that were not there before.
func chainNotesAdded(before, after map[int64]string, skip int64) []int64 {
	var out []int64
	for id := range after {
		if id != skip && before[id] == "" {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ------------------------------------------------------ list decoration

// chainSupportCache memoizes landingChainSupport per server for one list.
type chainSupportCache map[int64][2]string

// landingChainViews adds "chain" (the chosen external node + its state) and
// "chainSupport" to node views. External nodes are batch-loaded once.
func (h *Handler) landingChainViews(views []map[string]interface{}, nodes []model.LandingNode, servers map[int64]*model.LandingServer, catalog landingEgressCatalog) {
	var ids []int64
	for _, n := range nodes {
		if n.ChainExternalID > 0 {
			ids = append(ids, n.ChainExternalID)
		}
	}
	externals, _ := h.repo.LandingExternalNodesByIDs(ids)
	support := chainSupportCache{}
	for i, n := range nodes {
		if i >= len(views) {
			break
		}
		s := servers[n.LandingServerID]
		if s == nil {
			continue
		}
		ok, reason := support.get(s, catalog)
		views[i]["chainSupport"] = map[string]interface{}{"supported": ok, "reason": reason, "shared": s.Source == "shared"}
		if n.ChainExternalID == 0 {
			continue
		}
		ext, found := externals[n.ChainExternalID]
		chain := map[string]interface{}{"externalId": n.ChainExternalID}
		state, why := "applied", ""
		if found && ext.UserID == n.UserID {
			up, _ := linechain.FromJSON(ext.Upstream)
			chain["name"], chain["protocolLabel"], chain["summary"] = ext.Name, up.ProtocolLabel(), ext.Summary
			if s.Source == "shared" {
				if reason := sharedChainReason(up); reason != "" {
					state, why = "error", reason
				}
			}
		} else {
			state, why = "error", "外部节点已删除"
		}
		switch {
		case state == "error":
		case !ok:
			state, why = "error", reason
		case h.external.chainNote(s.ID, n.ID) != "":
			state, why = "error", h.external.chainNote(s.ID, n.ID)
		case s.Source == "shared" && s.SharedPushedRevision != s.SharedAppliedRevision:
			state = "syncing"
		case s.EgressState == "error" && s.EgressError != "":
			state, why = "error", s.EgressError
		}
		chain["state"], chain["reason"] = state, why
		views[i]["chain"] = chain
	}
}

func (c chainSupportCache) get(s *model.LandingServer, catalog landingEgressCatalog) (bool, string) {
	if v, ok := c[s.ID]; ok {
		return v[0] == "1", v[1]
	}
	d := &repo.LandingEgressData{Server: *s}
	if s.Source == "native" && catalog.nodeFacts != nil {
		f := catalog.nodeFacts()[s.NodeID]
		d.NodeEgress, d.NodeGuard = f.LandingEgress, f.LandingGuard
	}
	ok, reason := landingChainSupport(d)
	flag := "0"
	if ok {
		flag = "1"
	}
	c[s.ID] = [2]string{flag, reason}
	return ok, reason
}
