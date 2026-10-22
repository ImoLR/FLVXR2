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
}

func LandingURL(n model.LandingNode, host string, port int) string {
	authority := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	if landingcustom.IsExtraProtocol(n.Protocol) {
		return extraLandingURL(n, host, port)
	}
	if n.Protocol == "ss2022" {
		userinfo := url.QueryEscape(n.Method) + ":" + url.QueryEscape(n.ServerKey+":"+n.UserKey)
		return "ss://" + strings.ReplaceAll(userinfo, "+", "%20") + "@" + authority + "#" + url.PathEscape(n.Name)
	}
	names := []string{}
	_ = json.Unmarshal([]byte(n.ServerNames), &names)
	sni := ""
	if len(names) > 0 {
		sni = names[0]
	}
	q := url.Values{"encryption": {"none"}, "flow": {"xtls-rprx-vision"}, "security": {"reality"}, "sni": {sni}, "fp": {"chrome"}, "pbk": {n.PublicKey}, "sid": {n.ShortID}, "type": {"tcp"}}
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
	udp := true
	if landingcustom.IsExtraProtocol(n.Protocol) {
		cp, _ := SharedProtocolCap(s, n.Protocol)
		udp = cp.UDP
	}
	link := func(name, kind, host string, port int) LandingLink {
		l := LandingLink{Name: name, URL: LandingURL(n, host, port), Kind: kind}
		if landingcustom.IsExtraProtocol(n.Protocol) {
			l.Clash, l.Surge = LandingClientConfigs(n, host, port, udp)
		}
		return l
	}
	out := []LandingLink{link("直连", "direct", s.PublicHost, n.Port)}
	var forwards []model.Forward
	if err := r.db.Where("landing_node_id = ? AND user_id = ?", n.ID, n.UserID).Find(&forwards).Error; err != nil {
		return nil, err
	}
	for _, f := range forwards {
		entries, err := r.forwardEntries(f, true)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, e := range entries {
			l := link(f.Name+" · "+e.NodeName, "forward", e.Host, e.Port)
			key := net.JoinHostPort(strings.Trim(e.Host, "[]"), strconv.Itoa(e.Port))
			if !seen[key] {
				seen[key] = true
				l.ForwardID, l.EntryNodeID = f.ID, e.NodeID
				out = append(out, l)
			}
		}
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
	var ports []model.ForwardPort
	if err := r.db.Where("forward_id = ?", f.ID).Order("id").Find(&ports).Error; err != nil {
		return nil, err
	}
	var t model.Tunnel
	_ = r.db.First(&t, f.TunnelID).Error
	expand := len(allAddresses) > 0 && allAddresses[0]
	entries := map[int64]model.Node{}
	addressOwners := map[string]map[int64]bool{}
	if expand && len(ports) > 0 {
		ids := make([]int64, 0, len(ports))
		for _, fp := range ports {
			ids = append(ids, fp.NodeID)
		}
		var nodes []model.Node
		if err := r.db.Where("id IN ? OR id IN (SELECT node_id FROM chain_tunnel WHERE tunnel_id = ? AND chain_type = ?)", ids, f.TunnelID, "1").Find(&nodes).Error; err != nil {
			return nil, err
		}
		for _, node := range nodes {
			entries[node.ID] = node
			addresses := nodeIngressAddresses(&node)
			for _, address := range append(addresses[:], landingEntryHosts(node.ExtraIPs.String)...) {
				host := normalizeLandingHost(address)
				if host == "" {
					continue
				}
				if addressOwners[host] == nil {
					addressOwners[host] = map[int64]bool{}
				}
				addressOwners[host][node.ID] = true
			}
		}
	}
	out := []forwardEntry{}
	for _, fp := range ports {
		var entry model.Node
		if expand {
			var ok bool
			if entry, ok = entries[fp.NodeID]; !ok {
				continue
			}
		} else {
			if err := r.db.First(&entry, fp.NodeID).Error; err != nil {
				continue
			}
		}
		// Copy links expose every selected entry IP. Other callers retain
		// their existing address selection unless expansion is requested.
		if expand {
			hosts := landingEntryHosts(entry.ServerIP)
			if t.InIP.Valid && strings.TrimSpace(t.InIP.String) != "" {
				hosts = landingEntryHosts(t.InIP.String)
				// Exclude addresses known to belong to another entry, but
				// retain custom IPs/domains whose ownership is not recorded.
				matched := []string{}
				for _, host := range hosts {
					owners := addressOwners[normalizeLandingHost(host)]
					if len(owners) == 0 || owners[entry.ID] {
						matched = append(matched, host)
					}
				}
				hosts = matched
			}
			if fp.InIP.Valid && strings.TrimSpace(fp.InIP.String) != "" {
				hosts = landingEntryHosts(fp.InIP.String)
			}
			for _, host := range hosts {
				out = append(out, forwardEntry{Host: host, Port: fp.Port, NodeID: entry.ID, NodeName: entry.Name})
			}
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
	return out, nil
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
					return 0, errors.New("规则包含面板落地节点时只能设置一个目标")
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
	return r.db.Model(&model.Forward{}).Where("id = ?", id).Update("landing_node_id", landingID).Error
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
			return errors.New("节点规则端口段与已有落地节点端口冲突")
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
