package handler

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 260 「全部用户」: the admin's read-only view of every user's
// subscriptions on the 订阅 page. Typed views on purpose — only names, kinds,
// protocol names, counts, timestamps, the client name and the 分流 list leave
// the server; never a token / link, a source URL (not even its hint), node
// credentials, server addresses / ports or ref keys (a rule link key holds
// the entry address).

const subAdminDenied = "权限不足，仅管理员可操作"

type subAdminSub struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Format         string `json:"format"`
	RuleMode       string `json:"ruleMode"`
	Nodes          int    `json:"nodes"`
	Routing        string `json:"routing"` // none | default | custom
	RoutingCount   int    `json:"routingCount"`
	LastPullAt     int64  `json:"lastPullAt"`
	LastPullClient string `json:"lastPullClient"`
	LastPullNodes  int    `json:"lastPullNodes"`
	CreatedTime    int64  `json:"createdTime"`
}

type subAdminSource struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Nodes       int    `json:"nodes"`
	LastOkAt    int64  `json:"lastOkAt"`
	LastFetchAt int64  `json:"lastFetchAt"`
	Error       string `json:"error"`
}

type subAdminUser struct {
	ID         int64            `json:"id"`
	Name       string           `json:"name"`
	LastPullAt int64            `json:"lastPullAt"`
	Subs       []subAdminSub    `json:"subs"`
	Sources    []subAdminSource `json:"sources"`
}

type subAdminItem struct {
	// Group: rule | landing | external | source | source_all.
	Group      string `json:"group"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol,omitempty"`
	Chain      bool   `json:"chain,omitempty"`
	SourceName string `json:"sourceName,omitempty"`
	Count      int    `json:"count,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
}

// subAdminRoute is one 分流 row; a node exit is "node" + the node's name.
type subAdminRoute struct {
	Type        string   `json:"type"`
	Key         string   `json:"key,omitempty"`
	Name        string   `json:"name,omitempty"`
	Lines       []string `json:"lines,omitempty"`
	Exit        string   `json:"exit"`
	ExitName    string   `json:"exitName,omitempty"`
	ExitMissing bool     `json:"exitMissing,omitempty"`
	// Plan 261 link entry: count + status only (never the URL, its hint,
	// the stored set's id or public id).
	RuleCount   int   `json:"ruleCount,omitempty"`
	FetchedAt   int64 `json:"fetchedAt,omitempty"`
	LinkFailed  bool  `json:"linkFailed,omitempty"`
	LinkMissing bool  `json:"linkMissing,omitempty"`
}

func subIsAdmin(r *http.Request) bool {
	_, role, err := userRoleFromRequest(r)
	return err == nil && role == 0
}

// subRoutingKind is the short 分流 word of a subscription: none (NULL or [],
// both render the output from before 分流), default (exactly the default
// builtins with their default exits) or custom.
func subRoutingKind(list []sublink.RouteEntry) string {
	if len(list) == 0 {
		return "none"
	}
	def := sublink.DefaultRouting()
	if len(list) != len(def) {
		return "custom"
	}
	want := make(map[string]string, len(def))
	for _, e := range def {
		want[e.Key] = e.Exit
	}
	for _, e := range list {
		exit, ok := want[e.Key]
		if e.Type != sublink.RouteBuiltin || !ok || e.Exit != exit {
			return "custom"
		}
		delete(want, e.Key)
	}
	return "default"
}

var subURLText = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://\S+`)

// subAdminError: a source's last error without anything URL-like (fetch
// errors are already friendly; this is a second guard).
func subAdminError(msg string) string {
	return strings.TrimSpace(subURLText.ReplaceAllString(msg, "…"))
}

func subSourceNodes(s model.UserSubSource) int {
	if s.NodeCount > model.MaxSubSourceNodes {
		return model.MaxSubSourceNodes
	}
	return s.NodeCount
}

// subAdminList: users with ≥1 subscription or source, latest pull first.
// One pass, three queries; nothing is resolved or rendered.
func (h *Handler) subAdminList(w http.ResponseWriter, r *http.Request) {
	if !subIsAdmin(r) {
		response.WriteJSON(w, response.Err(403, subAdminDenied))
		return
	}
	enabled := h.subLinkEnabled()
	out := map[string]interface{}{"enabled": enabled, "users": []subAdminUser{}}
	if !enabled {
		response.WriteJSON(w, response.OK(out))
		return
	}
	subs, err := h.repo.AdminSubscriptionRows()
	if err != nil {
		subFail(w, err)
		return
	}
	sources, err := h.repo.AdminSubSourceRows()
	if err != nil {
		subFail(w, err)
		return
	}
	users := map[int64]*subAdminUser{}
	user := func(id int64) *subAdminUser {
		u := users[id]
		if u == nil {
			u = &subAdminUser{ID: id, Subs: []subAdminSub{}, Sources: []subAdminSource{}}
			users[id] = u
		}
		return u
	}
	srcNodes := make(map[int64]int, len(sources))
	for _, s := range sources {
		srcNodes[s.ID] = subSourceNodes(s)
		u := user(s.UserID)
		u.Sources = append(u.Sources, subAdminSource{ID: s.ID, Name: s.Name, Nodes: srcNodes[s.ID], LastOkAt: s.LastOkAt,
			LastFetchAt: s.LastFetchAt, Error: subAdminError(s.LastError)})
	}
	for _, s := range subs {
		nodes := 0
		for _, ref := range sublink.ParseRefs(s.Items) {
			if ref.Kind == sublink.RefSourceAll {
				nodes += srcNodes[ref.SourceID]
				continue
			}
			nodes++
		}
		routing := sublink.ParseRouting(s.Routing)
		u := user(s.UserID)
		u.Subs = append(u.Subs, subAdminSub{ID: s.ID, Name: s.Name, Format: s.Format, RuleMode: s.RuleMode, Nodes: nodes,
			Routing: subRoutingKind(routing), RoutingCount: len(routing), LastPullAt: s.LastPullAt, LastPullClient: s.LastPullClient,
			LastPullNodes: s.LastPullNodes, CreatedTime: s.CreatedTime})
		if s.LastPullAt > u.LastPullAt {
			u.LastPullAt = s.LastPullAt
		}
	}
	ids := make([]int64, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	names, err := h.repo.UserNamesByIDs(ids)
	if err != nil {
		subFail(w, err)
		return
	}
	list := make([]subAdminUser, 0, len(users))
	for _, u := range users {
		u.Name = names[u.ID]
		if u.Name == "" {
			u.Name = "#" + strconv.FormatInt(u.ID, 10)
		}
		list = append(list, *u)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].LastPullAt != list[j].LastPullAt {
			return list[i].LastPullAt > list[j].LastPullAt
		}
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	out["users"] = list
	response.WriteJSON(w, response.OK(out))
}

// subAdminDetail: one subscription, its items resolved with the OWNER's
// permissions (never a source fetch) and its 分流 list.
func (h *Handler) subAdminDetail(w http.ResponseWriter, r *http.Request) {
	if !subIsAdmin(r) {
		response.WriteJSON(w, response.Err(403, subAdminDenied))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := subDecode(r, &req); err != nil {
		subFail(w, err)
		return
	}
	s, err := h.repo.GetSubscription(req.ID)
	if err != nil || s == nil || req.ID <= 0 {
		subFail(w, errors.New("订阅不存在"))
		return
	}
	refs := sublink.ParseRefs(s.Items)
	d, err := h.subLoad(r.Context(), s.UserID, refs, false, false)
	if err != nil {
		subFail(w, err)
		return
	}
	names, err := h.repo.UserNamesByIDs([]int64{s.UserID})
	if err != nil {
		subFail(w, err)
		return
	}
	owner := names[s.UserID]
	if owner == "" {
		owner = "#" + strconv.FormatInt(s.UserID, 10)
	}
	// nodeName: ref key → node name, for 分流 rows that send to one node
	// (the current key of a rule whose entry address changed, too).
	nodeName := map[string]string{}
	items := make([]subAdminItem, 0, len(refs))
	for _, ref := range refs {
		it := subAdminItem{}
		switch ref.Kind {
		case sublink.RefForwardLink, sublink.RefLandingDirect:
			it.Group = "landing"
			if ref.Kind == sublink.RefForwardLink {
				it.Group = "rule"
			}
			if l, ok := d.panelLink(ref); ok {
				it.Name, it.Protocol = l.Node.Name, l.Node.ProtocolLabel()
				if l.Rule {
					it.Group, it.Chain = "rule", l.Chain
				}
				nodeName[ref.Key()], nodeName[l.Ref.Key()] = l.Node.Name, l.Node.Name
			} else {
				it.Missing = true
			}
		case sublink.RefExternalNode:
			it.Group = "external"
			if e, ok := d.externals[ref.ExternalID]; ok {
				it.Name = e.Name
				if n, ok := externalNode(e); ok {
					it.Protocol = n.ProtocolLabel()
					nodeName[ref.Key()] = e.Name
				} else {
					it.Missing = true
				}
			} else {
				it.Missing = true
			}
		case sublink.RefSourceAll:
			it.Group = "source_all"
			src, ok := d.sources[ref.SourceID]
			if !ok {
				it.Missing = true
				break
			}
			it.SourceName, it.Count = src.Name, len(d.srcNodes[ref.SourceID])
			for _, sn := range d.srcNodes[ref.SourceID] {
				nodeName[sublink.Ref{Kind: sublink.RefSourceNode, SourceID: ref.SourceID, Name: sn.Name}.Key()] = sn.Name
			}
		case sublink.RefLeiKwanRule: // plan 272
			it.Group, it.Missing = "leikwan", true
			if d.leikwan != nil {
				if n, ok := d.leikwan.nodes[ref.LeiKwanRuleID]; ok {
					it.Name, it.Protocol, it.Missing = n.Name, n.ProtocolLabel(), false
					nodeName[ref.Key()] = n.Name
				}
			}
		case sublink.RefLeiKwanAll:
			it.Group = "leikwan_all"
			if d.leikwan == nil || !d.leikwan.granted {
				it.Missing = true
				break
			}
			it.Count = len(d.leikwan.nodes)
			for id, n := range d.leikwan.nodes {
				nodeName[sublink.Ref{Kind: sublink.RefLeiKwanRule, LeiKwanRuleID: id}.Key()] = n.Name
			}
		case sublink.RefSourceNode:
			it.Group, it.Name, it.Missing = "source", ref.Name, true
			src, ok := d.sources[ref.SourceID]
			if !ok {
				break
			}
			it.SourceName = src.Name
			for _, sn := range d.srcNodes[ref.SourceID] {
				if sn.Name == ref.Name {
					it.Protocol, it.Missing = sn.Node().ProtocolLabel(), false
					nodeName[ref.Key()] = sn.Name
					break
				}
			}
		default:
			continue
		}
		items = append(items, it)
	}
	var routing []subAdminRoute
	list := sublink.ParseRouting(s.Routing)
	if len(list) > 0 {
		sets, err := h.repo.SubRuleSetsByIDs(s.UserID, subLinkRefs(list))
		if err != nil {
			subFail(w, err)
			return
		}
		routing = make([]subAdminRoute, 0, len(list))
		for _, e := range list {
			v := subAdminRoute{Type: e.Type, Key: e.Key, Name: e.Name, Lines: e.Lines, Exit: e.Exit}
			if e.Type == sublink.RouteLink {
				v.Lines = nil
				if set, ok := sets[e.Ref]; ok {
					v.RuleCount, v.FetchedAt, v.LinkFailed = set.RuleCount, set.FetchedAt, set.FailCount > 0 && set.LastError != ""
				} else {
					v.LinkMissing = true
				}
			}
			if k, ok := strings.CutPrefix(e.Exit, sublink.ExitNodePrefix); ok {
				v.Exit = "node"
				v.ExitName, v.ExitMissing = nodeName[k], nodeName[k] == ""
			}
			routing = append(routing, v)
		}
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"id": s.ID, "name": s.Name, "userId": s.UserID, "user": owner, "format": s.Format, "ruleMode": s.RuleMode,
		"createdTime": s.CreatedTime, "lastPullAt": s.LastPullAt, "lastPullClient": s.LastPullClient, "lastPullNodes": s.LastPullNodes,
		"items": items, "routing": routing, "routingKind": subRoutingKind(list), "routingCatalog": subRoutingCatalog(),
	}))
}
