package repo

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

type LandingLink struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	ForwardID   int64  `json:"forwardId,omitempty"`
	EntryNodeID int64  `json:"entryNodeId,omitempty"`
}

func LandingURL(n model.LandingNode, host string, port int) string {
	authority := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
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
	out := []LandingLink{{Name: "直连", URL: LandingURL(n, s.PublicHost, n.Port), Kind: "direct"}}
	var forwards []model.Forward
	if err := r.db.Where("landing_node_id = ? AND user_id = ?", n.ID, n.UserID).Find(&forwards).Error; err != nil {
		return nil, err
	}
	for _, f := range forwards {
		entries, err := r.forwardEntries(f)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, LandingLink{Name: f.Name + " · " + e.NodeName, URL: LandingURL(n, e.Host, e.Port), Kind: "forward", ForwardID: f.ID, EntryNodeID: e.NodeID})
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
func (r *Repository) forwardEntries(f model.Forward) ([]forwardEntry, error) {
	var ports []model.ForwardPort
	if err := r.db.Where("forward_id = ?", f.ID).Order("id").Find(&ports).Error; err != nil {
		return nil, err
	}
	var t model.Tunnel
	_ = r.db.First(&t, f.TunnelID).Error
	out := []forwardEntry{}
	for _, fp := range ports {
		var entry model.Node
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
	return out, nil
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
	return count > 0, err
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
	return ports, err
}
