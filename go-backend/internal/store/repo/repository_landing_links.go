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
		return "ss://" + strings.ReplaceAll(url.QueryEscape(n.Method+":"+n.ServerKey+":"+n.UserKey), "+", "%20") + "@" + authority + "#" + url.PathEscape(n.Name)
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
		var ports []model.ForwardPort
		if err := r.db.Where("forward_id = ?", f.ID).Order("id").Find(&ports).Error; err != nil {
			return nil, err
		}
		var t model.Tunnel
		_ = r.db.First(&t, f.TunnelID).Error
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
			out = append(out, LandingLink{Name: f.Name + " · " + entry.Name, URL: LandingURL(n, host, fp.Port), Kind: "forward", ForwardID: f.ID, EntryNodeID: entry.ID})
		}
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
			if db.First(&s, ln.LandingServerID).Error != nil || db.First(&node, s.NodeID).Error != nil {
				continue
			}
			matched := landingNodeHosts(s, node)[host]
			if !matched && (host == "127.0.0.1" || host == "::1") {
				var count int64
				err := db.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND node_id = ? AND chain_type = ?", tunnelID, s.NodeID, "3").Count(&count).Error
				if err != nil {
					return 0, err
				}
				matched = count > 0
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
	return count > 0, err
}
