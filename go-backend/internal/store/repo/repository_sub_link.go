package repo

import (
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"

	"gorm.io/gorm"
)

// Plan 185 「订阅」: storage of subscription links / sources and the batch
// loading of a user's panel links (rule links, landing 直连, chain links).
// Every load is a fixed number of queries, independent of the item count.

var ErrSubCap = errors.New("cap reached")

// ---------------------------------------------------------- subscriptions

func (r *Repository) ListSubscriptions(userID int64) ([]model.UserSubscription, error) {
	var rows []model.UserSubscription
	err := r.db.Where("user_id = ?", userID).Order("id").Find(&rows).Error
	return rows, err
}

func (r *Repository) GetSubscription(id int64) (*model.UserSubscription, error) {
	var s model.UserSubscription
	err := r.db.First(&s, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

func (r *Repository) GetSubscriptionByToken(token string) (*model.UserSubscription, error) {
	var s model.UserSubscription
	err := r.db.Where("token = ?", token).Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

// CreateSubscription inserts s unless the user already has cap rows
// (cap <= 0: unlimited). The count and insert share one transaction.
func (r *Repository) CreateSubscription(s *model.UserSubscription, cap int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if cap > 0 {
			var n int64
			// Plan 272: the automatic 「利群汇聚」 subscription is not counted.
			if err := tx.Model(&model.UserSubscription{}).Where("user_id = ? AND auto_kind = ?", s.UserID, "").Count(&n).Error; err != nil {
				return err
			}
			if n >= int64(cap) {
				return ErrSubCap
			}
		}
		return tx.Create(s).Error
	})
}

// GetAutoSubscription returns the user's automatic subscription of a kind
// (plan 272 "leikwan"), nil when there is none.
func (r *Repository) GetAutoSubscription(userID int64, kind string) (*model.UserSubscription, error) {
	var s model.UserSubscription
	err := r.db.Where("user_id = ? AND auto_kind = ?", userID, kind).Order("id").Limit(1).Find(&s).Error
	if err != nil || s.ID == 0 {
		return nil, err
	}
	return &s, nil
}

// UpdateSubscription changes name / format / rule mode / items (and, when
// routing is not nil, the 分流 list) of the user's own subscription; false
// when it does not exist.
func (r *Repository) UpdateSubscription(id, userID int64, name, format, mode, items string, routing *string) (bool, error) {
	fields := map[string]interface{}{
		"name": name, "format": format, "rule_mode": mode, "items": items, "updated_time": time.Now().UnixMilli(),
	}
	if routing != nil {
		fields["routing"] = *routing
	}
	res := r.db.Model(&model.UserSubscription{}).Where("id = ? AND user_id = ?", id, userID).Updates(fields)
	return res.RowsAffected > 0, res.Error
}

// ExternalSubscriptionUses counts, per external node of the user, how many
// of the user's subscriptions reference it (plan 256: one query over the
// items column, no per-node query).
func (r *Repository) ExternalSubscriptionUses(userID int64) (map[int64]int, error) {
	var rows []model.UserSubscription
	if err := r.db.Select("id", "items").Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, s := range rows {
		seen := map[int64]bool{}
		for _, ref := range sublink.ParseRefs(s.Items) {
			if ref.Kind == sublink.RefExternalNode && !seen[ref.ExternalID] {
				seen[ref.ExternalID] = true
				out[ref.ExternalID]++
			}
		}
	}
	return out, nil
}

func (r *Repository) SetSubscriptionToken(id, userID int64, token string) (bool, error) {
	res := r.db.Model(&model.UserSubscription{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]interface{}{
		"token": token, "last_pull_at": 0, "last_pull_client": "", "last_pull_nodes": 0, "updated_time": time.Now().UnixMilli(),
	})
	return res.RowsAffected > 0, res.Error
}

// SetSubscriptionEntryMode (plan 297): the entry the subscription's 利群
// nodes use (model.SubEntryMain | model.SubEntryLocal); false when the
// user's subscription does not exist.
func (r *Repository) SetSubscriptionEntryMode(id, userID int64, mode string) (bool, error) {
	res := r.db.Model(&model.UserSubscription{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]interface{}{
		"entry_mode": mode, "updated_time": time.Now().UnixMilli(),
	})
	return res.RowsAffected > 0, res.Error
}

func (r *Repository) DeleteSubscription(id, userID int64) (bool, error) {
	res := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&model.UserSubscription{})
	return res.RowsAffected > 0, res.Error
}

// MarkSubscriptionPulled records the last client pull; the row is written at
// most once per minute (older than minGap), so pulls never storm the DB.
func (r *Repository) MarkSubscriptionPulled(id, at int64, client string, nodes int, minGap time.Duration) error {
	return r.db.Model(&model.UserSubscription{}).Where("id = ? AND last_pull_at < ?", id, at-minGap.Milliseconds()).Updates(map[string]interface{}{
		"last_pull_at": at, "last_pull_client": client, "last_pull_nodes": nodes,
	}).Error
}

// ---------------------------------------------------------- admin overview

// AdminSubscriptionRows: every user's subscriptions for the admin's
// read-only 「全部用户」 view (plan 260) — one query, the token is never
// loaded.
func (r *Repository) AdminSubscriptionRows() ([]model.UserSubscription, error) {
	var rows []model.UserSubscription
	err := r.db.Select("id", "user_id", "name", "format", "rule_mode", "items", "last_pull_at", "last_pull_client", "last_pull_nodes",
		"created_time", "routing", "entry_mode").Order("id").Find(&rows).Error
	return rows, err
}

// AdminSubSourceRows: every external subscription source for the same view —
// one query, without the sealed URL, its hint or the cached nodes.
func (r *Repository) AdminSubSourceRows() ([]model.UserSubSource, error) {
	var rows []model.UserSubSource
	err := r.db.Select("id", "user_id", "name", "last_ok_at", "last_fetch_at", "last_error", "node_count", "created_time").
		Order("id").Find(&rows).Error
	return rows, err
}

// ---------------------------------------------------------- sources

func (r *Repository) ListSubSources(userID int64) ([]model.UserSubSource, error) {
	var rows []model.UserSubSource
	err := r.db.Where("user_id = ?", userID).Order("id").Find(&rows).Error
	return rows, err
}

func (r *Repository) GetSubSource(id int64) (*model.UserSubSource, error) {
	var s model.UserSubSource
	err := r.db.First(&s, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &s, err
}

// SubSourcesByIDs loads the user's own sources among ids.
func (r *Repository) SubSourcesByIDs(userID int64, ids []int64) (map[int64]model.UserSubSource, error) {
	out := map[int64]model.UserSubSource{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.UserSubSource
	if err := r.db.Where("user_id = ? AND id IN ?", userID, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, s := range rows {
		out[s.ID] = s
	}
	return out, nil
}

// CountSubSources: how many external sources the user has (cap pre-check).
func (r *Repository) CountSubSources(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.UserSubSource{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

func (r *Repository) CreateSubSource(s *model.UserSubSource, cap int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if cap > 0 {
			var n int64
			if err := tx.Model(&model.UserSubSource{}).Where("user_id = ?", s.UserID).Count(&n).Error; err != nil {
				return err
			}
			if n >= int64(cap) {
				return ErrSubCap
			}
		}
		return tx.Create(s).Error
	})
}

// SaveSubSourceFetch stores a fetch result: ok replaces the node cache,
// a failure keeps it and only records the error.
func (r *Repository) SaveSubSourceFetch(id int64, ok bool, nodesJSON string, count int, errMsg string, at int64) error {
	fields := map[string]interface{}{"last_fetch_at": at, "last_error": errMsg, "updated_time": at}
	if ok {
		fields["cached_nodes"], fields["node_count"], fields["last_ok_at"], fields["last_error"] = nodesJSON, count, at, ""
	}
	return r.db.Model(&model.UserSubSource{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteSubSource removes the user's source and its references from the
// user's subscriptions (one transaction). It returns the touched
// subscription ids (their render cache must go).
func (r *Repository) DeleteSubSource(id, userID int64) (bool, []int64, error) {
	var touched []int64
	found := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&model.UserSubSource{})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		found = true
		var subs []model.UserSubscription
		if err := tx.Where("user_id = ?", userID).Find(&subs).Error; err != nil {
			return err
		}
		for _, s := range subs {
			refs := sublink.ParseRefs(s.Items)
			kept := refs[:0]
			for _, ref := range refs {
				if (ref.Kind == sublink.RefSourceAll || ref.Kind == sublink.RefSourceNode) && ref.SourceID == id {
					continue
				}
				kept = append(kept, ref)
			}
			if len(kept) == len(sublink.ParseRefs(s.Items)) {
				continue
			}
			touched = append(touched, s.ID)
			if err := tx.Model(&model.UserSubscription{}).Where("id = ?", s.ID).Update("items", sublink.RefsJSON(kept)).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return found, touched, err
}

// ---------------------------------------------------------- panel links

// SubPanelLink is one panel node a subscription can reference.
type SubPanelLink struct {
	Ref  sublink.Ref
	Node sublink.Node
	// Rule: a rule link (else landing 直连); Chain: 「我的外部节点」 chain rule
	// on a 线路机 tunnel.
	Rule  bool
	Chain bool
	// External (plan 284): a port forward to the owner's 外部节点 of that
	// name; ExternalAuto = recognised by its target (no stored link).
	External     string
	ExternalAuto bool
	// Detail: "入口名 :端口" for rule links, "host:port" for 直连.
	Detail string
}

// SubPanelData is everything of a user's panel a subscription can use.
type SubPanelData struct {
	Links []SubPanelLink
	// PortForwards: the user's plain port-forward rules (no proxy link).
	PortForwards int
	// ExternalNodes (plan 284): all of the user's 外部节点 (loaded anyway for
	// the port forwards to them), reused by the 外部节点 items of the render.
	ExternalNodes []model.LandingExternalNode
}

// SubPanelLinks loads the user's panel links: every rule with an own
// landing node or an active 「我自己的节点」 chain (one link per entry
// address, exactly like 复制链接) and every own landing node's 直连 link.
// forwardIDs / landingIDs narrow the load (nil = all); countPlain also
// counts plain port-forward rules. About 8 queries in total.
func (r *Repository) SubPanelLinks(userID int64, forwardIDs, landingIDs []int64, countPlain bool) (*SubPanelData, error) {
	out := &SubPanelData{}
	var landings []model.LandingNode
	if err := r.db.Where("user_id = ? AND delete_pending = ?", userID, false).Order("id").Find(&landings).Error; err != nil {
		return nil, err
	}
	landingByID := map[int64]*model.LandingNode{}
	serverIDs := []int64{}
	for i := range landings {
		landingByID[landings[i].ID] = &landings[i]
		serverIDs = append(serverIDs, landings[i].LandingServerID)
	}
	servers := map[int64]*model.LandingServer{}
	if len(serverIDs) > 0 {
		var rows []model.LandingServer
		if err := r.db.Where("id IN ?", serverIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for i := range rows {
			servers[rows[i].ID] = &rows[i]
		}
	}

	// Rules: own landing node or an active chain.
	var chains []model.ForwardChain
	cq := r.db.Where("user_id = ? AND state = ? AND forward_id > 0", userID, model.ChainStateActive)
	if forwardIDs != nil {
		cq = cq.Where("forward_id IN ?", append(forwardIDs, 0))
	}
	if err := cq.Find(&chains).Error; err != nil {
		return nil, err
	}
	chainByForward := map[int64]*model.ForwardChain{}
	chainForwardIDs := []int64{0}
	for i := range chains {
		chainByForward[chains[i].ForwardID] = &chains[i]
		chainForwardIDs = append(chainForwardIDs, chains[i].ForwardID)
	}
	// Plan 284: with 外部节点, port forwards to them have links too (stored
	// link or read-time match), so every rule of the user is looked at.
	var extNodes []model.LandingExternalNode
	if err := r.db.Where("user_id = ?", userID).Order("id ASC").Find(&extNodes).Error; err != nil {
		return nil, err
	}
	out.ExternalNodes = extNodes
	extCount := len(extNodes)
	var forwards []model.Forward
	fq := r.db.Where("user_id = ? AND (landing_node_id > 0 OR id IN ?)", userID, chainForwardIDs)
	if extCount > 0 {
		fq = r.db.Where("user_id = ?", userID)
	}
	if forwardIDs != nil {
		fq = fq.Where("id IN ?", append(forwardIDs, 0))
	}
	if err := fq.Order("inx, id").Find(&forwards).Error; err != nil {
		return nil, err
	}
	externalOf := map[int64]*ForwardExternal{}
	if extCount > 0 {
		chained := map[int64]bool{}
		for id := range chainByForward {
			chained[id] = true
		}
		got, err := r.forwardExternalsWith(extNodes, forwards, chained)
		if err != nil {
			return nil, err
		}
		externalOf = got
	}
	if countPlain {
		if extCount > 0 && forwardIDs == nil {
			n := 0
			for _, f := range forwards {
				if f.LandingNodeID == 0 && chainByForward[f.ID] == nil && externalOf[f.ID] == nil {
					n++
				}
			}
			out.PortForwards = n
		} else {
			var n int64
			if err := r.db.Model(&model.Forward{}).Where("user_id = ? AND landing_node_id = 0 AND id NOT IN ?", userID, chainForwardIDs).Count(&n).Error; err != nil {
				return nil, err
			}
			out.PortForwards = int(n)
		}
	}
	usable := forwards[:0]
	for _, f := range forwards {
		if chainByForward[f.ID] != nil || landingByID[f.LandingNodeID] != nil || externalOf[f.ID] != nil {
			usable = append(usable, f)
		}
	}
	forwards = usable

	entries, err := r.subForwardEntries(forwards)
	if err != nil {
		return nil, err
	}
	v6only, err := r.subIPv6OnlyEntryDomains(entries)
	if err != nil {
		return nil, err
	}
	// Plan 303: the user's own 专属域名 replace the entry nodes' main domains.
	names, err := r.LoadUserEntryNames([]int64{userID})
	if err != nil {
		return nil, err
	}
	for _, f := range forwards {
		list := entries[f.ID]
		seen := map[string]bool{}
		chain := chainByForward[f.ID]
		hostsPerNode := map[int64]int{}
		for _, e := range list {
			hostsPerNode[e.NodeID]++
		}
		for _, e := range list {
			addr := net.JoinHostPort(strings.Trim(e.Host, "[]"), strconv.Itoa(e.Port))
			if seen[addr] {
				continue
			}
			seen[addr] = true
			link := SubPanelLink{Rule: true, Chain: chain != nil, Ref: sublink.Ref{Kind: sublink.RefForwardLink, ForwardID: f.ID, Entry: sublink.EntryKey(e.NodeID, e.Host)}}
			// Plan 302: an IPv6-only DDNS entry domain is built on its current
			// IPv6 (key and detail keep the domain); the renderer writes the
			// domain only for clients that resolve AAAA-only names.
			host, entryDomain, aaaaOnly := e.Host, "", false
			shown := addr
			if uh := names.ResolveForward(userID, e.NodeID, f.ID, e.Host); uh.Hidden {
				// Plan 303 round 3: his name cannot be handed out yet (zone
				// at the record cap, creating keeps failing, or not reserved
				// this instant): the entry is left out — never the main
				// domain, never the IP.
				continue
			} else if uh.Host != "" {
				// Plan 303 round 2: on a node with 用户专属域名 every client
				// gets the user's own name — never the IP, whatever the
				// client resolves. The key keeps the entry host (stored
				// items stay valid).
				host, aaaaOnly = uh.Host, uh.V6Only
				shown = net.JoinHostPort(uh.Host, strconv.Itoa(e.Port))
				// Round 3: the editor's key carries the name too (never the
				// main domain); items stored under the main domain still
				// resolve (same forward + entry node, see panelLink).
				link.Ref.Entry = sublink.EntryKey(e.NodeID, uh.Host)
			} else if ip := v6only[e.NodeID][strings.ToLower(strings.Trim(e.Host, "[]"))]; ip != "" {
				host, entryDomain = ip, strings.Trim(e.Host, "[]")
			}
			link.Detail = e.NodeName + " :" + strconv.Itoa(e.Port)
			if hostsPerNode[e.NodeID] > 1 {
				link.Detail = e.NodeName + " · " + shown
			}
			if x := externalOf[f.ID]; x != nil && chain == nil {
				node, ok := ExternalForwardNode(x, host, e.Port, ExternalForwardNodeName(f.Name, e.NodeName, len(list)))
				if !ok {
					continue
				}
				link.Node, link.External, link.ExternalAuto = node, x.Node.Name, x.Auto
			} else if chain != nil {
				name := f.Name
				if len(list) > 1 {
					name = f.Name + " · " + e.NodeName
				}
				// Plan 250: Reality (vless://) or AnyTLS (pinned Extra).
				node, ok := ChainSubNode(*chain, host, e.Port, name)
				if !ok {
					continue
				}
				link.Node = node
			} else {
				n := landingByID[f.LandingNodeID]
				node, ok := subLandingNode(*n, servers[n.LandingServerID], host, e.Port, f.Name+" · "+e.NodeName)
				if !ok {
					continue
				}
				link.Node = node
			}
			link.Node.EntryDomain = entryDomain
			link.Node.AAAAOnly = aaaaOnly
			out.Links = append(out.Links, link)
		}
	}

	// Landing 直连.
	want := map[int64]bool{}
	for _, id := range landingIDs {
		want[id] = true
	}
	for i := range landings {
		n := &landings[i]
		if landingIDs != nil && !want[n.ID] {
			continue
		}
		s := servers[n.LandingServerID]
		if s == nil {
			continue
		}
		host, aaaaOnly := s.PublicHost, false
		if s.NodeID > 0 && !names.Empty() {
			if uh := names.ResolveLanding(userID, s.NodeID, n.ID, s.PublicHost); uh.Hidden {
				continue // plan 303 round 3: no address yet → left out
			} else if uh.Host != "" {
				host, aaaaOnly = uh.Host, uh.V6Only // plan 303 round 2: every client
			}
		}
		node, ok := subLandingNode(*n, s, host, n.Port, n.Name+"（直连）")
		if !ok {
			continue
		}
		node.AAAAOnly = aaaaOnly
		out.Links = append(out.Links, SubPanelLink{Ref: sublink.Ref{Kind: sublink.RefLandingDirect, LandingID: n.ID}, Node: node,
			Detail: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(n.Port))})
	}
	return out, nil
}

// subIPv6OnlyEntryDomains maps entry node id → lower-case domain → current
// IPv6 for the entry hosts that are IPv6-only DDNS domains (plan 302): a
// synced AAAA record with a current IP and no A record of the same name. No
// query when every entry host is an IP literal.
func (r *Repository) subIPv6OnlyEntryDomains(entries map[int64][]forwardEntry) (map[int64]map[string]string, error) {
	seen := map[int64]bool{}
	for _, list := range entries {
		for _, e := range list {
			if net.ParseIP(strings.Trim(e.Host, "[]")) == nil {
				seen[e.NodeID] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	// All records (a handful): an A record of the name on any node makes it
	// dual-stack.
	var recs []model.NodeDDNSRecord
	if err := r.db.Select("node_id", "domain", "record_type", "current_ip").Find(&recs).Error; err != nil {
		return nil, err
	}
	hasA := map[string]bool{}
	for _, rec := range recs {
		if strings.EqualFold(rec.RecordType, "A") {
			hasA[strings.ToLower(strings.TrimSpace(rec.Domain))] = true
		}
	}
	out := map[int64]map[string]string{}
	for _, rec := range recs {
		domain := strings.ToLower(strings.TrimSpace(rec.Domain))
		ip := net.ParseIP(strings.TrimSpace(rec.CurrentIP))
		if !seen[rec.NodeID] || !strings.EqualFold(rec.RecordType, "AAAA") || domain == "" || hasA[domain] || ip == nil || ip.To4() != nil {
			continue
		}
		if out[rec.NodeID] == nil {
			out[rec.NodeID] = map[string]string{}
		}
		out[rec.NodeID][domain] = ip.String()
	}
	return out, nil
}

// subLandingNode is a landing node reached at host:port as a subscription
// node (the same credentials as its copied link / client config).
func subLandingNode(n model.LandingNode, s *model.LandingServer, host string, port int, name string) (sublink.Node, bool) {
	if s == nil || strings.TrimSpace(host) == "" || port <= 0 {
		return sublink.Node{}, false
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if landingcustom.IsExtraProtocol(n.Protocol) {
		// Shared: the share's UDP capability; native AnyTLS (plan 274): UDP on.
		o := LandingNodeOpts(&n)
		e := &sublink.Extra{Type: n.Protocol, Server: host, Port: port, Username: n.Username, Password: n.Password, SNI: o.SNI,
			Version: o.Version, Obfs: o.Obfs, ObfsHost: o.ObfsHost, Transport: o.Transport, UDP: landingProtocolUDP(s, n)}
		if n.Protocol == landingcustom.ProtocolAnyTLS {
			e.CertSHA256, e.CertPEM = n.CertSHA256, n.TLSCertPEM
		}
		return sublink.Node{Name: name, Extra: e, URI: extraLandingURL(n, host, port)}, true
	}
	uri := LandingURL(n, host, port)
	up, _, err := linechain.ParseLink(uri)
	if err != nil {
		return sublink.Node{}, false
	}
	return sublink.Node{Name: name, Up: up, URI: uri}, true
}

// subForwardEntries expands the entry addresses of many rules at once (same
// selection as the copied 转发版 links, see forwardEntries) with four
// queries in total: ports, tunnels, tunnel entry nodes, nodes.
func (r *Repository) subForwardEntries(forwards []model.Forward) (map[int64][]forwardEntry, error) {
	out := map[int64][]forwardEntry{}
	if len(forwards) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(forwards))
	tunnelIDs := []int64{}
	seenTunnel := map[int64]bool{}
	for _, f := range forwards {
		ids = append(ids, f.ID)
		if !seenTunnel[f.TunnelID] {
			seenTunnel[f.TunnelID] = true
			tunnelIDs = append(tunnelIDs, f.TunnelID)
		}
	}
	var ports []model.ForwardPort
	if err := r.db.Where("forward_id IN ?", ids).Order("id").Find(&ports).Error; err != nil {
		return nil, err
	}
	var tunnels []model.Tunnel
	if err := r.db.Where("id IN ?", tunnelIDs).Find(&tunnels).Error; err != nil {
		return nil, err
	}
	tunnelInIP := map[int64]string{}
	for _, t := range tunnels {
		tunnelInIP[t.ID] = t.InIP.String
	}
	var entryRows []model.ChainTunnel
	if err := r.db.Where("tunnel_id IN ? AND chain_type = ?", tunnelIDs, "1").Find(&entryRows).Error; err != nil {
		return nil, err
	}
	forwardTunnel := map[int64]int64{}
	for _, f := range forwards {
		forwardTunnel[f.ID] = f.TunnelID
	}
	tunnelNodes := map[int64]map[int64]bool{}
	nodeIDs := []int64{}
	addNode := func(tunnelID, nodeID int64) {
		if tunnelNodes[tunnelID] == nil {
			tunnelNodes[tunnelID] = map[int64]bool{}
		}
		if !tunnelNodes[tunnelID][nodeID] {
			tunnelNodes[tunnelID][nodeID] = true
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	for _, ct := range entryRows {
		addNode(ct.TunnelID, ct.NodeID)
	}
	for _, fp := range ports {
		addNode(forwardTunnel[fp.ForwardID], fp.NodeID)
	}
	nodes := map[int64]model.Node{}
	if len(nodeIDs) > 0 {
		var rows []model.Node
		if err := r.db.Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, n := range rows {
			nodes[n.ID] = n
		}
	}
	index := map[int64]*entryHostIndex{}
	for tunnelID, set := range tunnelNodes {
		list := make([]model.Node, 0, len(set))
		for id := range set {
			if n, ok := nodes[id]; ok {
				list = append(list, n)
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		index[tunnelID] = newEntryHostIndex(tunnelInIP[tunnelID], list)
	}
	for _, fp := range ports {
		x := index[forwardTunnel[fp.ForwardID]]
		if x == nil {
			continue
		}
		hosts, node, ok := x.hosts(fp.NodeID, fp.InIP.String)
		if !ok {
			continue
		}
		for _, host := range hosts {
			out[fp.ForwardID] = append(out[fp.ForwardID], forwardEntry{Host: host, Port: fp.Port, NodeID: node.ID, NodeName: node.Name})
		}
	}
	return out, nil
}

// ExternalNodesByIDs loads the user's own external nodes (nil ids = all).
func (r *Repository) ExternalNodesByIDs(userID int64, ids []int64) ([]model.LandingExternalNode, error) {
	var rows []model.LandingExternalNode
	q := r.db.Where("user_id = ?", userID)
	if ids != nil {
		q = q.Where("id IN ?", append(ids, 0))
	}
	err := q.Order("id").Find(&rows).Error
	return rows, err
}

// SubLandingNode is subLandingNode for the handler (plan 272: a 利群 rule
// whose landing is a panel node, reached at the 主入口:listen port).
func SubLandingNode(n model.LandingNode, s *model.LandingServer, host string, port int, name string) (sublink.Node, bool) {
	return subLandingNode(n, s, host, port, name)
}
