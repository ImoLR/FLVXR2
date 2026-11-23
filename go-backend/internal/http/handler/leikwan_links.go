package handler

// Plan 272: 利群 rules as full node links — in subscriptions (refs
// leikwan_rule / leikwan_all, rendered at fetch time from the owner's current
// 主入口), on the landing page 「复制链接」 and for 复制链接 / 复制订阅. A rule
// has a full link when its landing is a panel landing node or one of the
// owner's 外部节点; a manual host:port only has the entry address.
// Subscriptions are recommended (「生成订阅」: the user's 「利群汇聚」 sub
// follows 主入口 changes), node links stay available to everyone.

import (
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

const (
	lkSubAutoKind = "leikwan"
	lkSubName     = "利群汇聚"
	lkSubNote     = "推荐用订阅：换入口时刷新订阅即可，不用改地址"
	lkManualNoSub = "手动落地没有完整链接，不能进订阅"
)

// lkEntry is a user's 主入口 (the host clients connect to).
type lkEntry struct {
	NodeID int64
	Host   string
	Name   string
}

// leiKwanUserEntry: the 主入口 of a user's grant (nil = no grant / node).
func (h *Handler) leiKwanUserEntry(userID int64) *lkEntry {
	g, err := h.repo.GetLeiKwanGrant(userID)
	if err != nil || g == nil || g.MainEntryNodeID <= 0 {
		return nil
	}
	n, err := h.repo.GetNodeRecord(g.MainEntryNodeID)
	if err != nil || n == nil || strings.TrimSpace(n.ServerIP) == "" {
		return nil
	}
	return &lkEntry{NodeID: n.ID, Host: strings.Trim(strings.TrimSpace(n.ServerIP), "[]"), Name: n.Name}
}

// leiKwanRehost points a copied upstream at host:port. A node that was
// addressed by domain keeps it as TLS SNI / ws Host (the entry is an IP).
// Plan 284: shared with 「我的外部节点」 forwards (linechain.Rehost).
func leiKwanRehost(up linechain.Upstream, host string, port int) linechain.Upstream {
	return linechain.Rehost(up, host, port)
}

// leiKwanExternalSupported: the 外部节点 can be shared as a link / sub node.
func leiKwanExternalSupported(e model.LandingExternalNode) (linechain.Upstream, bool, string) {
	up, err := linechain.FromJSON(e.Upstream)
	if err != nil || up.Type == "" {
		return up, false, "节点数据无效"
	}
	up.Tag = ""
	probe := leiKwanRehost(up, "192.0.2.1", 1)
	if sublink.ShareURI(sublink.Node{Name: "x", Up: probe}) == "" {
		return up, false, "这个协议不能生成订阅链接"
	}
	return up, true, ""
}

// leiKwanRuleNode builds the rule's node at entry:listen port. err = no full
// link (manual landing, landing gone, …).
func (h *Handler) leiKwanRuleNode(x model.LeiKwanRule, entryHost, name string) (sublink.Node, error) {
	if entryHost == "" || x.ListenPort <= 0 {
		return sublink.Node{}, errors.New("主入口未定")
	}
	switch {
	case x.LandingNodeID > 0:
		n, s, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID)
		if err != nil || n == nil {
			return sublink.Node{}, errors.New("落地节点不存在")
		}
		node, ok := repo.SubLandingNode(*n, s, entryHost, x.ListenPort, name)
		if !ok {
			return sublink.Node{}, errors.New("落地节点生成不了链接")
		}
		return node, nil
	case x.ExternalNodeID > 0:
		rows, err := h.repo.ExternalNodesByIDs(x.UserID, []int64{x.ExternalNodeID})
		if err != nil || len(rows) == 0 {
			return sublink.Node{}, errors.New("外部节点不存在")
		}
		up, ok, reason := leiKwanExternalSupported(rows[0])
		if !ok {
			return sublink.Node{}, errors.New(reason)
		}
		return sublink.Node{Name: name, Up: leiKwanRehost(up, entryHost, x.ListenPort)}, nil
	}
	return sublink.Node{}, errors.New(lkManualNoSub)
}

// leiKwanLinkKind: "full" (panel landing / 外部节点) or "address" (manual).
func leiKwanLinkKind(x model.LeiKwanRule) string {
	if x.LandingNodeID > 0 || x.ExternalNodeID > 0 {
		return "full"
	}
	return "address"
}

// leiKwanSubNodeName: 「利群·广港 · 我的规则」 (the line name + rule name).
func leiKwanSubNodeName(line model.LeiKwanLine, x model.LeiKwanRule) string {
	if line.Name == "" {
		return x.Name
	}
	if strings.HasPrefix(x.Name, line.Name) {
		return x.Name
	}
	return line.Name + " · " + x.Name
}

// ---- subscription data ----

// lkSubData: the owner's 利群 rules as subscription nodes (built once per
// render; a user has at most a handful of rules).
type lkSubData struct {
	granted bool
	rules   []model.LeiKwanRule
	nodes   map[int64]sublink.Node
	reasons map[int64]string
	lines   map[int64]model.LeiKwanLine
}

func (h *Handler) leiKwanSubLoad(userID int64) *lkSubData {
	return h.leiKwanSubLoadAt(userID, nil)
}

// leiKwanSubLoadAt: as leiKwanSubLoad, with the 利群 nodes on `at` (plan 294 /
// 297: the subscription's ?lk=local = 手机本地 127.0.0.1, nil = the owner's 主入口).
func (h *Handler) leiKwanSubLoadAt(userID int64, at *lkEntry) *lkSubData {
	d := &lkSubData{nodes: map[int64]sublink.Node{}, reasons: map[int64]string{}, lines: map[int64]model.LeiKwanLine{}}
	g, err := h.repo.GetLeiKwanGrant(userID)
	if err != nil || g == nil || g.Status != 1 {
		return d
	}
	d.granted = true
	d.rules, _ = h.repo.ListLeiKwanRules(userID)
	if len(d.rules) == 0 {
		return d
	}
	if lines, err := h.repo.ListLeiKwanLines(); err == nil {
		for _, l := range lines {
			d.lines[l.ID] = l
		}
	}
	entry := h.leiKwanUserEntry(userID)
	if at != nil {
		entry = at
	}
	for _, x := range d.rules {
		if entry == nil {
			d.reasons[x.ID] = "主入口未定"
			continue
		}
		n, err := h.leiKwanRuleNode(x, entry.Host, leiKwanSubNodeName(d.lines[x.LineID], x))
		if err != nil {
			d.reasons[x.ID] = err.Error()
			continue
		}
		d.nodes[x.ID] = n
	}
	return d
}

func (d *lkSubData) ruleIDs() []int64 {
	out := make([]int64, 0, len(d.rules))
	for _, x := range d.rules {
		out = append(out, x.ID)
	}
	return out
}

// ---- 「生成订阅」: the user's 利群汇聚 subscription ----

var lkSubMu sync.Mutex

// leiKwanEnsureSub returns the user's 利群汇聚 subscription (an ordinary sub
// row, marked auto_kind so a second 生成订阅 reuses it), creating it on first
// use. The user may edit or delete it; 生成订阅 puts 「全部利群规则」 back
// when an edit took it out. Not counted in the 5-sub cap.
func (h *Handler) leiKwanEnsureSub(userID int64) (*model.UserSubscription, error) {
	if !h.subLinkEnabled() {
		return nil, errors.New("订阅功能没开，请联系管理员")
	}
	g, err := h.repo.GetLeiKwanGrant(userID)
	if err != nil {
		return nil, errors.New("读取授权失败")
	}
	if err := leiKwanGrantUsable(g); err != nil {
		return nil, err
	}
	lkSubMu.Lock()
	defer lkSubMu.Unlock()
	s, err := h.repo.GetAutoSubscription(userID, lkSubAutoKind)
	if err != nil {
		return nil, errors.New("读取订阅失败")
	}
	if s != nil {
		refs := sublink.ParseRefs(s.Items)
		for _, ref := range refs {
			if ref.Kind == sublink.RefLeiKwanAll {
				return s, nil
			}
		}
		kept := []sublink.Ref{{Kind: sublink.RefLeiKwanAll}}
		for _, ref := range refs {
			if ref.Kind != sublink.RefLeiKwanRule && len(kept) < sublink.MaxItems {
				kept = append(kept, ref)
			}
		}
		s.Items = sublink.RefsJSON(kept)
		if ok, err := h.repo.UpdateSubscription(s.ID, userID, s.Name, s.Format, s.RuleMode, s.Items, nil); err != nil || !ok {
			return nil, errors.New("更新利群汇聚订阅失败")
		}
		h.subState().cache.purgeSub(s.ID)
		return s, nil
	}
	token, err := sublink.NewToken()
	if err != nil {
		return nil, errors.New("生成订阅链接失败")
	}
	now := time.Now().UnixMilli()
	routing := sublink.RoutingJSON(sublink.DefaultRouting())
	s = &model.UserSubscription{UserID: userID, Name: lkSubName, Token: token, Format: sublink.FormatAuto, RuleMode: sublink.ModeCNDirect,
		Items: sublink.RefsJSON([]sublink.Ref{{Kind: sublink.RefLeiKwanAll}}), Routing: &routing, CreatedTime: now, UpdatedTime: now, AutoKind: lkSubAutoKind}
	if err := h.repo.CreateSubscription(s, 0); err != nil {
		return nil, errors.New("创建利群汇聚订阅失败")
	}
	return s, nil
}

// leikwanRuleSub (POST /api/v1/leikwan-rule/sub {userId?}) — 「生成订阅」: the
// owner's 利群汇聚 subscription (admins may ask for any granted user), with
// what it carries now.
func (h *Handler) leikwanRuleSub(w http.ResponseWriter, r *http.Request) {
	uid, role, _ := userRoleFromRequest(r)
	var req struct {
		UserID int64 `json:"userId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	owner := uid
	if role == 0 && req.UserID > 0 {
		owner = req.UserID
	}
	s, err := h.leiKwanEnsureSub(owner)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	d, err := h.subLoad(r.Context(), owner, sublink.ParseRefs(s.Items), false, false)
	if err != nil {
		leiKwanError(w, errors.New("读取订阅失败"))
		return
	}
	_, counts := d.resolve(sublink.ParseRefs(s.Items), 0)
	rules, manual := 0, 0
	if d.leikwan != nil {
		rules = len(d.leikwan.rules)
		manual = rules - len(d.leikwan.nodes)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": s.ID, "token": s.Token, "name": s.Name, "format": s.Format, "ruleMode": s.RuleMode,
		"leikwanNodes": counts.LeiKwan, "total": counts.Total, "rules": rules, "manual": manual, "lastPullAt": s.LastPullAt, "note": lkSubNote}))
}

// ---- landing page 「复制链接」 ----

// leiKwanLandingLinks: the owner's 利群 rules landing on this panel node, as
// normal node links at the owner's 主入口 (plan 272 item 4).
func (h *Handler) leiKwanLandingLinks(n model.LandingNode) []map[string]interface{} {
	out := []map[string]interface{}{}
	rules, err := h.repo.ListLeiKwanRules(n.UserID)
	if err != nil {
		return out
	}
	var mine []model.LeiKwanRule
	for _, x := range rules {
		if x.LandingNodeID == n.ID {
			mine = append(mine, x)
		}
	}
	if len(mine) == 0 {
		return out
	}
	entry := h.leiKwanUserEntry(n.UserID)
	if entry == nil {
		return out
	}
	lines := map[int64]model.LeiKwanLine{}
	if ls, err := h.repo.ListLeiKwanLines(); err == nil {
		for _, l := range ls {
			lines[l.ID] = l
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].ID < mine[j].ID })
	for _, x := range mine {
		name := lines[x.LineID].Name + " · " + entry.Name
		if lines[x.LineID].Name == "" {
			name = x.Name + " · " + entry.Name
		}
		node, err := h.leiKwanRuleNode(x, entry.Host, x.Name)
		if err != nil {
			continue
		}
		v := map[string]interface{}{"name": name, "kind": "leikwan", "leikwanRuleId": x.ID, "ruleName": x.Name,
			"address": net.JoinHostPort(entry.Host, strconv.Itoa(x.ListenPort)), "url": sublink.ShareURI(node)}
		if ln, s, err := h.repo.LeiKwanLandingTarget(x.LandingNodeID); err == nil && ln != nil {
			cp := *ln
			cp.Name = x.Name
			v["clash"], v["surge"], v["surgeNote"] = repo.LandingClientConfigAt(cp, s, entry.Host, x.ListenPort)
		}
		out = append(out, v)
	}
	return out
}
