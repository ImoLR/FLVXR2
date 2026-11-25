package repo

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

type LandingLink struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	ForwardID   int64  `json:"forwardId,omitempty"`
	EntryNodeID int64  `json:"entryNodeId,omitempty"`
	// Plan 121 client configs for AnyTLS / Snell / Mieru: a Clash (mihomo)
	// proxy entry and, for Snell (which has no share-link standard, so URL
	// is empty), a Surge proxy line.
	Clash string `json:"clash,omitempty"`
	Surge string `json:"surge,omitempty"`
	// SurgeNote: why a Snell node has no Surge line (plan 193).
	SurgeNote string `json:"surgeNote,omitempty"`
	// Blocked (plan 224): the rule's tunnel blocks a protocol feature of the
	// node, so this 经规则转发 link cannot connect.
	Blocked string `json:"blocked,omitempty"`
}

// LandingSS2022Password is the client password of an SS2022 node: "server
// key:user key", or the server key alone on a single-key node (plan 291:
// moved over from mmwx with its credentials unchanged; the agent renders it
// without clients).
func LandingSS2022Password(n model.LandingNode) string {
	if n.UserKey == "" {
		return n.ServerKey
	}
	return n.ServerKey + ":" + n.UserKey
}

func LandingURL(n model.LandingNode, host string, port int) string {
	authority := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	if landingcustom.IsExtraProtocol(n.Protocol) {
		return extraLandingURL(n, host, port)
	}
	if isVLESSEnc(n.Protocol) {
		return vlessEncURL(n, host, port)
	}
	if n.Protocol == "ss2022" {
		userinfo := url.QueryEscape(n.Method) + ":" + url.QueryEscape(LandingSS2022Password(n))
		return "ss://" + strings.ReplaceAll(userinfo, "+", "%20") + "@" + authority + "#" + url.PathEscape(n.Name)
	}
	names := []string{}
	_ = json.Unmarshal([]byte(n.ServerNames), &names)
	sni := ""
	if len(names) > 0 {
		sni = names[0]
	}
	q := url.Values{"encryption": {"none"}, "security": {"reality"}, "sni": {sni}, "fp": {"chrome"}, "pbk": {n.PublicKey}, "sid": {n.ShortID}, "type": {"tcp"}}
	// Plan 291: the node's flow (vision on every panel-made node; "" on a
	// Reality node moved over from mmwx whose clients use no flow).
	if n.Flow != "" {
		q.Set("flow", n.Flow)
	}
	return "vless://" + n.UUID + "@" + authority + "?" + q.Encode() + "#" + url.PathEscape(n.Name)
}
func (r *Repository) LandingNodeLinks(n model.LandingNode) ([]LandingLink, error) {
	s, err := r.GetLandingServer(n.LandingServerID)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("落地服务器不存在")
	}
	udp := landingProtocolUDP(s, n)
	link := func(name, kind, host string, port int) LandingLink {
		l := LandingLink{Name: name, URL: LandingURL(n, host, port), Kind: kind}
		if landingcustom.IsCapProtocol(n.Protocol) {
			l.Clash, l.Surge, l.SurgeNote = LandingClientConfigs(n, host, port, udp)
		}
		return l
	}
	direct := s.PublicHost
	var withheld []UserEntryHost
	out := []LandingLink{}
	if s.NodeID > 0 {
		// Plan 303: a native landing's 直连 uses the owner's 专属域名.
		// Round 3: never the main domain — withheld while the name cannot
		// be handed out.
		names, err := r.LoadUserEntryNames([]int64{n.UserID})
		if err != nil {
			return nil, err
		}
		if uh := names.Resolve(n.UserID, s.NodeID, s.PublicHost); uh.Hidden {
			withheld = append(withheld, uh)
			direct = ""
		} else if uh.Host != "" {
			direct = uh.Host
		}
	}
	if direct != "" {
		out = append(out, link("直连", "direct", direct, n.Port))
	}
	var forwards []model.Forward
	if err := r.db.Where("landing_node_id = ? AND user_id = ?", n.ID, n.UserID).Find(&forwards).Error; err != nil {
		return nil, err
	}
	// Plan 224: one batched read of the rules' tunnel filter flags.
	blockedBy := map[int64]string{}
	if features := LandingNodeFilterFeatures(&n); len(features) > 0 && len(forwards) > 0 {
		ids := make([]int64, 0, len(forwards))
		for _, f := range forwards {
			ids = append(ids, f.TunnelID)
		}
		var tunnels []model.Tunnel
		if err := r.db.Select("id, tls, http").Where("id IN ?", ids).Find(&tunnels).Error; err != nil {
			return nil, err
		}
		for _, t := range tunnels {
			if f := TunnelBlockedFeature(t.TLS, t.HTTP, features); f != "" {
				blockedBy[t.ID] = TunnelBlocksFeatureMsg(f)
			}
		}
	}
	for _, f := range forwards {
		entries, held, err := r.forwardEntriesNamed(f, true)
		if err != nil {
			return nil, err
		}
		withheld = append(withheld, held...)
		seen := map[string]bool{}
		for _, e := range entries {
			l := link(f.Name+" · "+e.NodeName, "forward", e.Host, e.Port)
			key := net.JoinHostPort(strings.Trim(e.Host, "[]"), strconv.Itoa(e.Port))
			if !seen[key] {
				seen[key] = true
				l.ForwardID, l.EntryNodeID = f.ID, e.NodeID
				if !strings.EqualFold(f.Mode, "nftables") && !strings.EqualFold(f.Mode, "wg_path") {
					l.Blocked = blockedBy[f.TunnelID]
				}
				out = append(out, l)
			}
		}
	}
	if err := userEntryWithheldError(len(out), withheld); err != nil {
		return nil, err
	}
	return out, nil
}

type forwardEntry struct {
	Host     string
	Port     int
	NodeID   int64
	NodeName string
}

// forwardEntries lists a rule's entry addresses (tunnel/port overrides first).
func (r *Repository) forwardEntries(f model.Forward, allAddresses ...bool) ([]forwardEntry, error) {
	out, _, err := r.forwardEntriesNamed(f, allAddresses...)
	return out, err
}

// forwardEntriesNamed is forwardEntries plus, for copy links (expand), the
// entries withheld from the owner (plan 303 round 3: his name cannot get a
// record yet, or he has none at this instant — never the main domain).
func (r *Repository) forwardEntriesNamed(f model.Forward, allAddresses ...bool) ([]forwardEntry, []UserEntryHost, error) {
	var ports []model.ForwardPort
	if err := r.db.Where("forward_id = ?", f.ID).Order("id").Find(&ports).Error; err != nil {
		return nil, nil, err
	}
	var t model.Tunnel
	_ = r.db.First(&t, f.TunnelID).Error
	expand := len(allAddresses) > 0 && allAddresses[0]
	var index *entryHostIndex
	if expand && len(ports) > 0 {
		ids := make([]int64, 0, len(ports))
		for _, fp := range ports {
			ids = append(ids, fp.NodeID)
		}
		var err error
		if index, err = r.loadEntryHostIndex(f.TunnelID, t.InIP.String, ids); err != nil {
			return nil, nil, err
		}
	}
	out := []forwardEntry{}
	for _, fp := range ports {
		var entry model.Node
		if expand {
			// Copy links expose every selected entry IP. Other callers retain
			// their existing address selection unless expansion is requested.
			hosts, node, ok := index.hosts(fp.NodeID, fp.InIP.String)
			if !ok {
				continue
			}
			for _, host := range hosts {
				out = append(out, forwardEntry{Host: host, Port: fp.Port, NodeID: node.ID, NodeName: node.Name})
			}
			continue
		}
		if err := r.db.First(&entry, fp.NodeID).Error; err != nil {
			continue
		}
		host := entry.ServerIP
		if t.InIP.Valid && strings.TrimSpace(t.InIP.String) != "" {
			var hosts []string
			if json.Unmarshal([]byte(t.InIP.String), &hosts) == nil && len(hosts) > 0 {
				host = hosts[0]
			} else if !strings.Contains(t.InIP.String, ",") {
				host = t.InIP.String
			}
		}
		if fp.InIP.Valid && strings.TrimSpace(fp.InIP.String) != "" {
			host = strings.TrimSpace(fp.InIP.String)
		}
		out = append(out, forwardEntry{Host: host, Port: fp.Port, NodeID: entry.ID, NodeName: entry.Name})
	}
	var withheld []UserEntryHost
	if expand && len(out) > 0 {
		// Plan 303: copy links show the owner's 专属域名 for that entry.
		// Round 3: never the main domain — an entry whose name cannot be
		// handed out yet is left out (withheld says why).
		names, err := r.LoadUserEntryNames([]int64{f.UserID})
		if err != nil {
			return nil, nil, err
		}
		if !names.Empty() {
			kept := out[:0]
			for _, e := range out {
				uh := names.Resolve(f.UserID, e.NodeID, e.Host)
				if uh.Hidden {
					withheld = append(withheld, uh)
					continue
				}
				if uh.Host != "" {
					e.Host = uh.Host
				}
				kept = append(kept, e)
			}
			out = kept
		}
	}
	return out, withheld, nil
}

// userEntryWithheldError: every entry of a link was withheld → the reason
// (nil when something is left or nothing was withheld).
func userEntryWithheldError(left int, withheld []UserEntryHost) error {
	if left > 0 || len(withheld) == 0 {
		return nil
	}
	tag := withheld[0].Tag
	for _, w := range withheld {
		if w.Tag == UserEntryTagCapped || w.Tag == UserEntryTagFailed {
			tag = w.Tag
			break
		}
	}
	return &UserEntryHiddenError{Tag: tag}
}

// entryHostIndex resolves which tunnel entry addresses belong to which entry
// node: the tunnel's entry list minus addresses owned by another entry (custom
// IPs/domains without a recorded owner stay), a per-rule listen IP override,
// or the node's own address.
type entryHostIndex struct {
	tunnelInIP string
	nodes      map[int64]model.Node
	owners     map[string]map[int64]bool
}

func (r *Repository) loadEntryHostIndex(tunnelID int64, tunnelInIP string, nodeIDs []int64) (*entryHostIndex, error) {
	var nodes []model.Node
	if err := r.db.Where("id IN ? OR id IN (SELECT node_id FROM chain_tunnel WHERE tunnel_id = ? AND chain_type = ?)", nodeIDs, tunnelID, "1").Find(&nodes).Error; err != nil {
		return nil, err
	}
	return newEntryHostIndex(tunnelInIP, nodes), nil
}

// newEntryHostIndex builds the index from already loaded nodes (the rule's
// port nodes plus the tunnel's entry nodes); plan 185 batch-loads them.
func newEntryHostIndex(tunnelInIP string, nodes []model.Node) *entryHostIndex {
	x := &entryHostIndex{tunnelInIP: strings.TrimSpace(tunnelInIP), nodes: map[int64]model.Node{}, owners: map[string]map[int64]bool{}}
	for _, node := range nodes {
		x.nodes[node.ID] = node
		addresses := nodeIngressAddresses(&node)
		for _, address := range append(addresses[:], landingEntryHosts(node.ExtraIPs.String)...) {
			host := normalizeLandingHost(address)
			if host == "" {
				continue
			}
			if x.owners[host] == nil {
				x.owners[host] = map[int64]bool{}
			}
			x.owners[host][node.ID] = true
		}
	}
	return x
}

func (x *entryHostIndex) hosts(nodeID int64, portInIP string) ([]string, model.Node, bool) {
	node, ok := x.nodes[nodeID]
	if !ok {
		return nil, node, false
	}
	if strings.TrimSpace(portInIP) != "" {
		return landingEntryHosts(portInIP), node, true
	}
	if x.tunnelInIP == "" {
		return landingEntryHosts(node.ServerIP), node, true
	}
	matched := []string{}
	for _, host := range landingEntryHosts(x.tunnelInIP) {
		owners := x.owners[normalizeLandingHost(host)]
		if len(owners) == 0 || owners[node.ID] {
			matched = append(matched, host)
		}
	}
	return matched, node, true
}

// EntryHostsForNodes lists, per entry node, the addresses clients use to reach
// a rule on tunnelID (the same selection as the copied 转发版 links).
// portInIPs carries per-node listen IP overrides (forward_port.in_ip).
func (r *Repository) EntryHostsForNodes(tunnelID int64, nodeIDs []int64, portInIPs map[int64]string) (map[int64][]string, map[int64]string, error) {
	var t model.Tunnel
	if err := r.db.First(&t, tunnelID).Error; err != nil {
		return nil, nil, err
	}
	index, err := r.loadEntryHostIndex(tunnelID, t.InIP.String, nodeIDs)
	if err != nil {
		return nil, nil, err
	}
	hosts := make(map[int64][]string, len(nodeIDs))
	names := make(map[int64]string, len(nodeIDs))
	for _, id := range nodeIDs {
		if h, node, ok := index.hosts(id, portInIPs[id]); ok {
			hosts[id] = h
			names[id] = node.Name
		}
	}
	return hosts, names, nil
}

func landingEntryHosts(raw string) []string {
	var hosts []string
	if json.Unmarshal([]byte(raw), &hosts) != nil {
		hosts = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	}
	out := []string{}
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host != "" && !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	return out
}
func normalizeLandingHost(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return strings.ToLower(s)
}
func landingNodeHosts(s model.LandingServer, n model.Node) map[string]bool {
	out := map[string]bool{}
	for _, h := range []string{s.PublicHost, n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String, n.IntranetIP.String} {
		if h != "" {
			out[normalizeLandingHost(h)] = true
		}
	}
	var extra []string
	if json.Unmarshal([]byte(n.ExtraIPs.String), &extra) != nil {
		extra = strings.FieldsFunc(n.ExtraIPs.String, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	}
	for _, h := range extra {
		out[normalizeLandingHost(h)] = true
	}
	return out
}

// MatchLandingTarget rejects mixed targets even when the matched node belongs to
// another user. Only an owner's node is returned for the billing exemption.
func (r *Repository) MatchLandingTarget(userID, tunnelID int64, remoteAddr string) (int64, error) {
	return matchLandingTarget(r.db, userID, tunnelID, remoteAddr)
}
func matchLandingTarget(db *gorm.DB, userID, tunnelID int64, remoteAddr string) (int64, error) {
	targets := strings.FieldsFunc(remoteAddr, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	var nodes []model.LandingNode
	if err := db.Find(&nodes).Error; err != nil {
		return 0, err
	}
	own := int64(0)
	for _, target := range targets {
		host, p, err := net.SplitHostPort(strings.TrimSpace(target))
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		host = normalizeLandingHost(host)
		for _, ln := range nodes {
			if ln.Port != port {
				continue
			}
			var s model.LandingServer
			var node model.Node
			if db.First(&s, ln.LandingServerID).Error != nil || (s.Source == "native" && db.First(&node, s.NodeID).Error != nil) {
				continue
			}
			hosts := landingNodeHosts(s, node)
			if s.Source == "shared" {
				c := SharedLandingCaps(&s)
				for _, h := range []string{c.Hosts.IPv4, c.Hosts.IPv6, c.Hosts.Domain} {
					if h != "" {
						hosts[normalizeLandingHost(h)] = true
					}
				}
			}
			matched := hosts[host]
			if s.Source == "native" && !matched && (host == "127.0.0.1" || host == "::1") {
				var count int64
				err := db.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND node_id = ? AND chain_type = ?", tunnelID, s.NodeID, "3").Count(&count).Error
				if err != nil {
					return 0, err
				}
				matched = count > 0
				// A direct (type 1) tunnel has no relay exit. Its sole entry is
				// also the exit; differing round-robin entries are ambiguous.
				if !matched {
					var tunnel model.Tunnel
					if err := db.Select("type").First(&tunnel, tunnelID).Error; err == nil && tunnel.Type == 1 {
						var entries []int64
						if err := db.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = ?", tunnelID, "1").Distinct().Pluck("node_id", &entries).Error; err != nil {
							return 0, err
						}
						matched = len(entries) == 1 && entries[0] == s.NodeID
					} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return 0, err
					}
				}
			}
			if matched {
				if len(targets) != 1 {
					return 0, errors.New("转发包含面板落地节点时只能设置一个目标")
				}
				if ln.UserID == userID {
					own = ln.ID
				}
			}
		}
	}
	return own, nil
}
func (r *Repository) SetForwardLandingNode(id, landingID int64) error {
	return r.db.Model(&model.Forward{}).Where("id = ?", id).Updates(map[string]interface{}{
		"landing_node_id": landingID,
		// Plan 164: a new landing drops a 「已确认合规」 confirmation.
		"line_compliance_ack_sig": gorm.Expr("CASE WHEN landing_node_id = ? THEN line_compliance_ack_sig ELSE '' END", landingID),
	}).Error
}
func (r *Repository) ClearForwardLandingPause(id int64) error {
	return r.db.Model(&model.Forward{}).Where("id = ?", id).Update("landing_auto_paused", false).Error
}
func (r *Repository) LandingPortInUse(nodeID int64, port int) (bool, error) {
	var count int64
	err := r.db.Model(&model.LandingNode{}).Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").Where("landing_server.node_id = ? AND landing_node.port = ?", nodeID, port).Count(&count).Error
	if err != nil || count > 0 {
		return count > 0, err
	}
	// Loopback chain listeners (plan 108) also occupy the port on the node.
	err = r.db.Model(&model.ForwardChainNode{}).Where("node_id = ? AND port = ?", nodeID, port).Count(&count).Error
	if err != nil || count > 0 {
		return count > 0, err
	}
	// And Reality guard listeners (plan 127).
	guards, err := landingGuardPortsOnNode(r.db, nodeID)
	if err != nil {
		return false, err
	}
	for _, p := range guards {
		if p == port {
			return true, nil
		}
	}
	// So do mmwx-shared landing nodes on the same machine (plan 121).
	shared, err := sharedLandingPortsOnNodeHost(r.db, nodeID)
	for _, p := range shared {
		if p == port {
			return true, err
		}
	}
	return false, err
}

// ValidateNodeLandingPortRange prevents widening the rule range over reserved landing ports.
func (r *Repository) ValidateNodeLandingPortRange(nodeID int64, spec string) error {
	// Plan 139: the whole range of an active native landing is reserved, not
	// only the ports its nodes already use.
	var servers []model.LandingServer
	if err := r.db.Where("source = 'native' AND node_id = ?", nodeID).Find(&servers).Error; err != nil {
		return err
	}
	for i := range servers {
		lo, hi := activeLandingRange(&servers[i])
		if lo <= 0 || len(portSpansOverlap(parsePortSpans(spec), lo, hi)) == 0 {
			continue
		}
		msg := "端口 " + formatPortRange(lo, hi) + " 已留给落地，请从转发端口段里排除"
		if hint, _ := landingRuleSpec(spec, "", 0, 0, lo, hi, nil); hint != "" {
			msg += "（例如改成 " + hint + "）"
		}
		return errors.New(msg)
	}
	var ports []int
	if err := r.db.Model(&model.LandingNode{}).Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").Where("landing_server.node_id = ?", nodeID).Pluck("landing_node.port", &ports).Error; err != nil {
		return err
	}
	var chainPorts []int
	if err := r.db.Model(&model.ForwardChainNode{}).Where("node_id = ?", nodeID).Pluck("port", &chainPorts).Error; err != nil {
		return err
	}
	ports = append(ports, chainPorts...)
	guards, err := landingGuardPortsOnNode(r.db, nodeID)
	if err != nil {
		return err
	}
	ports = append(ports, guards...)
	if len(ports) == 0 {
		return nil
	}
	reserved := map[int]bool{}
	for _, p := range ports {
		reserved[p] = true
	}
	for _, p := range parsePortRangeSpec(spec) {
		if reserved[p] {
			return errors.New("节点转发端口段与已有落地节点端口冲突")
		}
	}
	return nil
}
func landingPortsOnNode(tx *gorm.DB, nodeID int64) ([]int, error) {
	var ports []int
	err := tx.Model(&model.LandingNode{}).Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").Where("landing_server.node_id = ?", nodeID).Pluck("landing_node.port", &ports).Error
	if err != nil {
		return ports, err
	}
	guards, err := landingGuardPortsOnNode(tx, nodeID)
	if err != nil {
		return ports, err
	}
	ports = append(ports, guards...)
	// Dual-installed machines (plan 121): an mmwx-shared landing at the same
	// address owns its node ports on this machine as well.
	shared, err := sharedLandingPortsOnNodeHost(tx, nodeID)
	return append(ports, shared...), err
}

// LandingClientConfigAt: the plan-121 client configs (Clash / Surge) of a
// landing node reached at host:port, as on the landing page (plan 272: 利群
// rules at the 主入口); empty for protocols with a plain share link.
func LandingClientConfigAt(n model.LandingNode, s *model.LandingServer, host string, port int) (clash, surge, surgeNote string) {
	if s == nil || !landingcustom.IsCapProtocol(n.Protocol) {
		return "", "", ""
	}
	return LandingClientConfigs(n, host, port, landingProtocolUDP(s, n))
}
