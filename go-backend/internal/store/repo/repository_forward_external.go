package repo

// Plan 284 「我的外部节点」 port forwards: a forward whose target is one of
// its owner's 外部节点 (forward.external_node_id, or — read time only — a
// plain forward whose single target is exactly one of them). The forward
// itself stays a plain port forward; this file only builds its links.

import (
	"net"
	"net/netip"
	"strconv"
	"strings"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// LandingExternalUseForward marks a forward use of an external node
// (ID = forward.id); it keeps the node from being deleted.
const LandingExternalUseForward = "forward"

// ForwardExternal is how a forward reaches one of its owner's 外部节点.
type ForwardExternal struct {
	Node model.LandingExternalNode
	Up   linechain.Upstream
	// Auto: no stored link, the forward's target equals exactly this node
	// (shown as 「自动识别」).
	Auto bool
	// Mismatch: linked, but the forward's target is no longer the node's
	// address (an admin / DB edit); links still use the node, a warning is
	// shown, the forward is never rewritten.
	Mismatch bool
	// Blocked: the forward's tunnel filter blocks the node's protocol.
	Blocked string
}

// ExternalAddrKey is the canonical host:port of an external node / target.
func ExternalAddrKey(host string, port int) string {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if a, err := netip.ParseAddr(h); err == nil {
		h = a.Unmap().String()
	}
	return net.JoinHostPort(strings.TrimSuffix(h, "."), strconv.Itoa(port))
}

// forwardTargetKey: the canonical single target of a forward ("" when it
// has several targets or cannot be parsed).
func forwardTargetKey(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" || strings.ContainsAny(remote, ",\n\r") {
		return ""
	}
	host, port, err := net.SplitHostPort(remote)
	if err != nil {
		return ""
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return ""
	}
	return ExternalAddrKey(host, p)
}

// ExternalUpstream decodes a stored external node (nil = unusable row).
func ExternalUpstream(n model.LandingExternalNode) (linechain.Upstream, bool) {
	up, err := linechain.FromJSON(n.Upstream)
	if err != nil || up.Type == "" {
		return up, false
	}
	up.Tag = ""
	return up, true
}

// externalMatcher resolves forwards to their owners' external nodes.
type externalMatcher struct {
	byID    map[int64]model.LandingExternalNode
	ups     map[int64]linechain.Upstream
	byAddr  map[int64]map[string][]int64 // owner → addr → node ids
	tunnels map[int64]model.Tunnel
}

// newExternalMatcher indexes already loaded external nodes.
func newExternalMatcher(nodes []model.LandingExternalNode) *externalMatcher {
	m := &externalMatcher{byID: map[int64]model.LandingExternalNode{}, ups: map[int64]linechain.Upstream{}, byAddr: map[int64]map[string][]int64{}, tunnels: map[int64]model.Tunnel{}}
	for _, n := range nodes {
		up, ok := ExternalUpstream(n)
		if !ok {
			continue
		}
		m.byID[n.ID], m.ups[n.ID] = n, up
		if m.byAddr[n.UserID] == nil {
			m.byAddr[n.UserID] = map[string][]int64{}
		}
		k := ExternalAddrKey(up.Server, up.Port)
		m.byAddr[n.UserID][k] = append(m.byAddr[n.UserID][k], n.ID)
	}
	return m
}

// candidate: f may resolve to an external node (stored link or a single
// address match); only candidates need their tunnel's filter flags.
func (m *externalMatcher) candidate(f model.Forward, chained bool) bool {
	if chained || f.LandingNodeID > 0 || strings.EqualFold(f.Mode, "wg_path") {
		return false
	}
	if f.ExternalNodeID > 0 {
		n, ok := m.byID[f.ExternalNodeID]
		return ok && n.UserID == f.UserID
	}
	key := forwardTargetKey(f.RemoteAddr)
	return key != "" && len(m.byAddr[f.UserID][key]) == 1
}

// loadTunnels reads the filter flags of the candidates' tunnels (one query,
// none when nothing can match).
func (r *Repository) loadMatcherTunnels(m *externalMatcher, forwards []model.Forward, chained map[int64]bool) error {
	ids, seen := []int64{}, map[int64]bool{}
	for _, f := range forwards {
		if m.candidate(f, chained[f.ID]) && !seen[f.TunnelID] {
			seen[f.TunnelID] = true
			ids = append(ids, f.TunnelID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var tunnels []model.Tunnel
	if err := r.db.Select("id, tls, http, socks, block_other").Where("id IN ?", ids).Find(&tunnels).Error; err != nil {
		return err
	}
	for _, t := range tunnels {
		m.tunnels[t.ID] = t
	}
	return nil
}

func (m *externalMatcher) blocked(f model.Forward, up linechain.Upstream) string {
	if strings.EqualFold(f.Mode, "nftables") || strings.EqualFold(f.Mode, "wg_path") {
		return "" // kernel forwarding: no entry filter
	}
	t, ok := m.tunnels[f.TunnelID]
	if !ok {
		return ""
	}
	return ExternalFilterBlockedMsg(t.TLS, t.HTTP, t.Socks, t.BlockOther, up)
}

// match: the external node of f (nil = none). chained = f is a
// 「我的外部节点」 chain rule on a 线路机 tunnel (never matched here).
func (m *externalMatcher) match(f model.Forward, chained bool) *ForwardExternal {
	if m == nil || chained || f.LandingNodeID > 0 || strings.EqualFold(f.Mode, "wg_path") {
		return nil
	}
	if f.ExternalNodeID > 0 {
		n, ok := m.byID[f.ExternalNodeID]
		if !ok || n.UserID != f.UserID {
			return nil // gone or not the owner's (restored backup): plain forward
		}
		up := m.ups[n.ID]
		return &ForwardExternal{Node: n, Up: up, Mismatch: forwardTargetKey(f.RemoteAddr) != ExternalAddrKey(up.Server, up.Port), Blocked: m.blocked(f, up)}
	}
	key := forwardTargetKey(f.RemoteAddr)
	if key == "" {
		return nil
	}
	ids := m.byAddr[f.UserID][key]
	if len(ids) != 1 {
		return nil // no match or ambiguous: stays a plain port forward
	}
	n, up := m.byID[ids[0]], m.ups[ids[0]]
	if m.blocked(f, up) != "" {
		return nil // could never connect through this tunnel
	}
	return &ForwardExternal{Node: n, Up: up, Auto: true}
}

// ForwardExternals resolves many forwards at once (forward list, admin
// views); chained marks 线路机 chain rules. At most two queries.
func (r *Repository) ForwardExternals(forwards []model.Forward, chained map[int64]bool) (map[int64]*ForwardExternal, error) {
	if len(forwards) == 0 {
		return map[int64]*ForwardExternal{}, nil
	}
	owners, seen := []int64{}, map[int64]bool{}
	for _, f := range forwards {
		if !seen[f.UserID] {
			seen[f.UserID] = true
			owners = append(owners, f.UserID)
		}
	}
	var nodes []model.LandingExternalNode
	if err := r.db.Where("user_id IN ?", owners).Order("id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	return r.forwardExternalsWith(nodes, forwards, chained)
}

// forwardExternalsWith resolves forwards against preloaded external nodes.
func (r *Repository) forwardExternalsWith(nodes []model.LandingExternalNode, forwards []model.Forward, chained map[int64]bool) (map[int64]*ForwardExternal, error) {
	out := map[int64]*ForwardExternal{}
	if len(nodes) == 0 || len(forwards) == 0 {
		return out, nil
	}
	m := newExternalMatcher(nodes)
	if err := r.loadMatcherTunnels(m, forwards, chained); err != nil {
		return nil, err
	}
	for _, f := range forwards {
		if x := m.match(f, chained[f.ID]); x != nil {
			out[f.ID] = x
		}
	}
	return out, nil
}

// ForwardExternalOf resolves one forward.
func (r *Repository) ForwardExternalOf(f model.Forward) (*ForwardExternal, error) {
	chain, err := r.GetForwardChainByForward(f.ID)
	if err != nil {
		return nil, err
	}
	all, err := r.ForwardExternals([]model.Forward{f}, map[int64]bool{f.ID: chain != nil})
	if err != nil {
		return nil, err
	}
	return all[f.ID], nil
}

// ExternalForwardNodeName: the forward name, plus the entry name when the
// forward has several entry addresses (same rule as chain links).
func ExternalForwardNodeName(forwardName, entryName string, entries int) string {
	if entries > 1 {
		return forwardName + " · " + entryName
	}
	return forwardName
}

// ExternalForwardNode is the external node reached through one entry
// address of the forward (ok = the protocol has a share link).
func ExternalForwardNode(x *ForwardExternal, host string, port int, name string) (sublink.Node, bool) {
	if x == nil || strings.TrimSpace(host) == "" || port <= 0 {
		return sublink.Node{}, false
	}
	node := sublink.Node{Name: name, Up: linechain.Rehost(x.Up, host, port)}
	if sublink.ShareURI(node) == "" {
		return sublink.Node{}, false
	}
	return node, true
}

// ForwardExternalLinks: one link per entry address (exactly like the
// 复制链接 of 面板节点 forwards), the node's own link with server/port set to
// the entry. Blocked is set when the tunnel filter blocks the protocol.
func (r *Repository) ForwardExternalLinks(f model.Forward, x *ForwardExternal) ([]LandingLink, error) {
	entries, withheld, err := r.forwardEntriesNamed(f, true)
	if err != nil {
		return nil, err
	}
	if err := userEntryWithheldError(len(entries), withheld); err != nil {
		return nil, err // plan 303 round 3: no address yet → say why
	}
	out := []LandingLink{}
	seen := map[string]bool{}
	for _, e := range entries {
		key := net.JoinHostPort(strings.Trim(e.Host, "[]"), strconv.Itoa(e.Port))
		if seen[key] {
			continue
		}
		seen[key] = true
		node, ok := ExternalForwardNode(x, e.Host, e.Port, ExternalForwardNodeName(f.Name, e.NodeName, len(entries)))
		if !ok {
			continue
		}
		out = append(out, LandingLink{Name: node.Name, URL: sublink.ShareURI(node), Kind: "external", ForwardID: f.ID, EntryNodeID: e.NodeID, Blocked: x.Blocked})
	}
	return out, nil
}

// ForwardExternalUses: per external node id, the forwards linked to it
// (stored links only; the 外部节点 list shows 「N 条转发在用」).
func (r *Repository) ForwardExternalUses(userID int64) (map[int64][]LandingExternalUse, error) {
	var rows []model.Forward
	if err := r.db.Select("id", "name", "external_node_id").Where("user_id = ? AND external_node_id > 0 AND landing_node_id = 0", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64][]LandingExternalUse{}
	for _, f := range rows {
		out[f.ExternalNodeID] = append(out[f.ExternalNodeID], LandingExternalUse{ID: f.ID, Name: f.Name, ExternalID: f.ExternalNodeID, Kind: LandingExternalUseForward})
	}
	return out, nil
}

// ForwardExternalsByIDs resolves the forwards with the given ids (forward
// list decoration; three queries).
func (r *Repository) ForwardExternalsByIDs(ids []int64, chained map[int64]bool) (map[int64]*ForwardExternal, error) {
	if len(ids) == 0 {
		return map[int64]*ForwardExternal{}, nil
	}
	var forwards []model.Forward
	if err := r.db.Select("id", "name", "user_id", "tunnel_id", "remote_addr", "landing_node_id", "mode", "external_node_id").Where("id IN ?", ids).Find(&forwards).Error; err != nil {
		return nil, err
	}
	return r.ForwardExternals(forwards, chained)
}
