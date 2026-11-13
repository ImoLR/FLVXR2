package repo

import (
	"strings"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 224: which protocol features of the entry protocol filter (tunnel
// 屏蔽 TLS / 屏蔽 HTTP, go-gost service.isProtocolBlocked) a landing node's
// client traffic shows. Lab-tested with real clients (mihomo) against the
// real filter: VLESS Reality, AnyTLS and Snell obfs=tls start with a TLS
// ClientHello; Snell obfs=http starts with an HTTP request; SS2022, Snell
// without obfs and Mieru look random; UDP is never inspected. This is the
// only place that knows it — the API hands the result to the UI.
const (
	FilterFeatureTLS  = "tls"
	FilterFeatureHTTP = "http"
)

// Rule-target messages (user wording 11-06: no 「只能接 SS」).
const (
	// Backend reject (API / toast); the UI shows 「此隧道屏蔽了 TLS 协议特征」
	// on the tunnel and 「该节点协议带 TLS 特征，此隧道无法使用」 per node.
	LandingTLSBlockedMsg  = "此隧道屏蔽了 TLS 协议特征，该节点协议带 TLS 特征，无法使用"
	LandingHTTPBlockedMsg = "此隧道屏蔽了 HTTP 协议特征，该节点协议带 HTTP 特征，无法使用"
	// 「我自己的节点」 reaches the tunnel exit with VLESS Reality.
	ChainTLSBlockedMsg = "此隧道屏蔽了 TLS 协议特征，「我自己的节点」无法使用"
)

// LandingFilterProtocols: every landing protocol FLVX offers.
// Plan 253: VLESS Encryption (random appearance) has no TLS / HTTP feature,
// like SS2022 — it passes tunnels that block either (lab-tested).
var LandingFilterProtocols = []string{"ss2022", "vless_reality_vision", landingcustom.ProtocolAnyTLS, landingcustom.ProtocolSnell, landingcustom.ProtocolMieru, landingcustom.ProtocolVLESSEnc}

// LandingFilterFeatures returns the filter features of protocol with the
// given Snell obfs mode (ignored for other protocols); nil when none.
func LandingFilterFeatures(protocol, snellObfs string) []string {
	switch protocol {
	case "vless_reality_vision", landingcustom.ProtocolAnyTLS:
		return []string{FilterFeatureTLS}
	case landingcustom.ProtocolSnell:
		switch strings.ToLower(strings.TrimSpace(snellObfs)) {
		case "tls":
			return []string{FilterFeatureTLS}
		case "http":
			return []string{FilterFeatureHTTP}
		}
	}
	return nil
}

// LandingNodeFilterFeatures: the features of an existing node (its own
// stored Snell obfs, copied from the server when it was created).
func LandingNodeFilterFeatures(n *model.LandingNode) []string {
	if n == nil {
		return nil
	}
	obfs := ""
	if n.Protocol == landingcustom.ProtocolSnell {
		obfs = LandingNodeOpts(n).Obfs
	}
	return LandingFilterFeatures(n.Protocol, obfs)
}

// LandingServerProtocolFilterFeatures: the features a node of protocol
// created on s now would have (Snell takes the server's obfs default).
func LandingServerProtocolFilterFeatures(s *model.LandingServer, protocol string) []string {
	obfs := ""
	if s != nil && protocol == landingcustom.ProtocolSnell {
		obfs = s.SnellObfs
	}
	return LandingFilterFeatures(protocol, obfs)
}

// LandingServerFilterFeatureMap lists the features per protocol the server
// offers (protocols without features are left out).
func LandingServerFilterFeatureMap(s *model.LandingServer, protocols []string) map[string][]string {
	out := map[string][]string{}
	for _, p := range protocols {
		if f := LandingServerProtocolFilterFeatures(s, p); len(f) > 0 {
			out[p] = f
		}
	}
	return out
}

// TunnelBlockedFeatures lists the features a tunnel's entry filter blocks.
func TunnelBlockedFeatures(tls, http int) []string {
	out := []string{}
	if tls == 1 {
		out = append(out, FilterFeatureTLS)
	}
	if http == 1 {
		out = append(out, FilterFeatureHTTP)
	}
	return out
}

// TunnelBlockedFeature returns the first feature of features the tunnel
// blocks ("" = the node works through it).
func TunnelBlockedFeature(tls, http int, features []string) string {
	for _, f := range features {
		if (f == FilterFeatureTLS && tls == 1) || (f == FilterFeatureHTTP && http == 1) {
			return f
		}
	}
	return ""
}

// TunnelBlocksFeatureMsg: 「此隧道屏蔽了 TLS 协议特征」 (copy-link warning).
func TunnelBlocksFeatureMsg(feature string) string {
	return "此隧道屏蔽了 " + strings.ToUpper(feature) + " 协议特征"
}

// LandingFeatureBlockedMsg is the reject / disabled reason for feature.
func LandingFeatureBlockedMsg(feature string) string {
	if feature == FilterFeatureHTTP {
		return LandingHTTPBlockedMsg
	}
	return LandingTLSBlockedMsg
}

// TunnelFilterAffectedRule: a rule that would stop working once the tunnel
// blocks Feature (tunnel edit warning).
type TunnelFilterAffectedRule struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	UserName string `json:"userName"`
	Feature  string `json:"feature"`
}

// TunnelFilterAffectedRules lists the gost rules of tunnelID whose landing
// node (or 「我自己的节点」 chain, VLESS Reality) shows a feature the given
// flags block — two queries, nothing per rule.
func (r *Repository) TunnelFilterAffectedRules(tunnelID int64, tls, http int) ([]TunnelFilterAffectedRule, error) {
	out := []TunnelFilterAffectedRule{}
	if r == nil || r.db == nil || tunnelID <= 0 || (tls != 1 && http != 1) {
		return out, nil
	}
	type row struct {
		ID           int64
		Name         string
		UserName     string
		Mode         string
		Protocol     string
		ProtocolOpts string
	}
	var rows []row
	if err := r.db.Table("forward").
		Select("forward.id, forward.name, forward.user_name, forward.mode, landing_node.protocol, landing_node.protocol_opts").
		Joins("JOIN landing_node ON landing_node.id = forward.landing_node_id").
		Where("forward.tunnel_id = ? AND forward.landing_node_id > 0", tunnelID).
		Order("forward.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, rw := range rows {
		if strings.EqualFold(rw.Mode, "nftables") || strings.EqualFold(rw.Mode, "wg_path") {
			continue
		}
		n := model.LandingNode{Protocol: rw.Protocol, ProtocolOpts: rw.ProtocolOpts}
		if f := TunnelBlockedFeature(tls, http, LandingNodeFilterFeatures(&n)); f != "" {
			seen[rw.ID] = true
			out = append(out, TunnelFilterAffectedRule{ID: rw.ID, Name: rw.Name, UserName: rw.UserName, Feature: f})
		}
	}
	if tls == 1 {
		var chains []row
		if err := r.db.Table("forward_chain").
			Select("forward.id, forward.name, forward.user_name").
			Joins("JOIN forward ON forward.id = forward_chain.forward_id").
			Where("forward.tunnel_id = ?", tunnelID).
			Order("forward.id").Scan(&chains).Error; err != nil {
			return nil, err
		}
		for _, c := range chains {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, TunnelFilterAffectedRule{ID: c.ID, Name: c.Name, UserName: c.UserName, Feature: FilterFeatureTLS})
			}
		}
	}
	return out, nil
}
