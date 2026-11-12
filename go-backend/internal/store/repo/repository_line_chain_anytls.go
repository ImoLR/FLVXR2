package repo

// Plan 250: 「我自己的节点」 chain rules may use an AnyTLS inbound on the exits
// instead of VLESS Reality Vision (Surge has no VLESS). Same per-rule loopback
// model and port as Reality: one inbound per chain rule per exit. The
// credentials (random password + self-signed certificate) are generated once,
// at create or at a protocol switch, never per request.

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChainAnyTLSVersion: exits must report landing_chain ≥ this to run AnyTLS
// chain copies (older agents reject an unknown inbound protocol and with it
// the whole desired state).
const ChainAnyTLSVersion = 2

// ChainInboundMsg is the error for an unsupported chain inbound protocol.
const ChainInboundMsg = "「我自己的节点」只能使用 VLESS Reality Vision 或 AnyTLS"

// NormalizeChainInbound maps a request value to a chain inbound protocol;
// empty = VLESS Reality Vision (the default).
func NormalizeChainInbound(p string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "reality", model.ChainInboundReality:
		return model.ChainInboundReality, nil
	case model.ChainInboundAnyTLS:
		return model.ChainInboundAnyTLS, nil
	}
	return "", errors.New(ChainInboundMsg)
}

// chainInboundValues: stored inbound_protocol values meaning p ("" = Reality
// on rows from before plan 250).
func chainInboundValues(p string) []string {
	if p == model.ChainInboundAnyTLS {
		return []string{model.ChainInboundAnyTLS}
	}
	return []string{"", model.ChainInboundReality}
}

// ChainInboundLabel is the display name of a chain's inbound protocol.
func ChainInboundLabel(c model.ForwardChain) string {
	if c.ChainInbound() == model.ChainInboundAnyTLS {
		return "AnyTLS"
	}
	return "VLESS Reality"
}

// ChainCopySupported: an AnyTLS copy only goes to an agent that applies it.
func ChainCopySupported(c ChainCopy, chainVersion int) bool {
	return c.Chain.ChainInbound() != model.ChainInboundAnyTLS || chainVersion >= ChainAnyTLSVersion
}

// chainSNI: the AnyTLS SNI of a new chain = its first Reality server name (a
// real site that is reachable from the mainland, picked per exit like the
// Reality chain), so the ClientHello on the shared entry looks the same.
func chainSNI(c model.ForwardChain) string {
	for _, name := range ChainServerNames(c) {
		if name = strings.ToLower(strings.TrimSpace(name)); validLandingHostname(name) {
			return name
		}
	}
	return ""
}

// setChainInbound fills the inbound fields of c for protocol p: AnyTLS gets a
// fresh password and certificate; Reality clears the AnyTLS fields (its
// UUID / keys are created with every chain and never change).
func setChainInbound(c *model.ForwardChain, p string, now time.Time) error {
	c.InboundProtocol = p
	if p != model.ChainInboundAnyTLS {
		c.TLSServerName, c.Password, c.TLSCertPEM, c.TLSKeyPEM, c.CertSHA256 = "", "", "", "", ""
		return nil
	}
	pw, err := randomLandingString(24, landingAlnum)
	if err != nil {
		return err
	}
	sni := chainSNI(*c)
	certPEM, keyPEM, sum, err := selfSignedLandingCert(anytlsCertName(sni, ""), now)
	if err != nil {
		return err
	}
	c.TLSServerName, c.Password, c.TLSCertPEM, c.TLSKeyPEM, c.CertSHA256 = sni, pw, certPEM, keyPEM, sum
	return nil
}

// chainAnyTLSNode is the chain's AnyTLS inbound as a landing node, so the
// landing link / client-config helpers render it.
func chainAnyTLSNode(c model.ForwardChain, name string) model.LandingNode {
	opts, _ := json.Marshal(LandingProtocolOpts{SNI: c.TLSServerName})
	return model.LandingNode{Protocol: landingcustom.ProtocolAnyTLS, Name: name, Password: c.Password, TLSCertPEM: c.TLSCertPEM, CertSHA256: c.CertSHA256, ProtocolOpts: string(opts)}
}

// ChainLinkURL is the user's link through one entry: VLESS Reality Vision,
// or (plan 250) anytls://. The anytls URI has no pinning parameter, so the
// 复制链接 dialog adds pinned Clash / Surge configs (ChainClientConfigs).
func ChainLinkURL(c model.ForwardChain, host string, port int, name string) string {
	if c.ChainInbound() == model.ChainInboundAnyTLS {
		return LandingURL(chainAnyTLSNode(c, name), host, port)
	}
	n := model.LandingNode{Protocol: "vless_reality_vision", UUID: c.UUID, PublicKey: c.PublicKey, ShortID: c.ShortID, ServerNames: c.ServerNames, Name: name}
	return LandingURL(n, host, port)
}

// ChainClientConfigs: the pinned Clash (mihomo) entry and Surge line of an
// AnyTLS chain (empty for Reality, whose link carries everything).
func ChainClientConfigs(c model.ForwardChain, host string, port int, name string) (clash, surge string) {
	if c.ChainInbound() != model.ChainInboundAnyTLS {
		return "", ""
	}
	clash, surge, _ = LandingClientConfigs(chainAnyTLSNode(c, name), host, port, true)
	return clash, surge
}

// ChainSubNode is the chain rule's subscription node through one entry.
func ChainSubNode(c model.ForwardChain, host string, port int, name string) (sublink.Node, bool) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || port <= 0 {
		return sublink.Node{}, false
	}
	uri := ChainLinkURL(c, host, port, name)
	if c.ChainInbound() == model.ChainInboundAnyTLS {
		// UDP: AnyTLS carries it over the TCP session (UoT), like Reality's XUDP.
		e := &sublink.Extra{Type: landingcustom.ProtocolAnyTLS, Server: host, Port: port, Password: c.Password, SNI: c.TLSServerName, CertSHA256: c.CertSHA256, CertPEM: c.TLSCertPEM, UDP: true}
		return sublink.Node{Name: name, Extra: e, URI: uri}, true
	}
	up, _, err := linechain.ParseLink(uri)
	if err != nil {
		return sublink.Node{}, false
	}
	return sublink.Node{Name: name, Up: up, URI: uri}, true
}

// chainInboundSnapshot is the rollback point of an inbound switch.
type chainInboundSnapshot struct {
	Protocol   string `json:"protocol"`
	ServerName string `json:"serverName,omitempty"`
	Password   string `json:"password,omitempty"`
	CertPEM    string `json:"certPem,omitempty"`
	KeyPEM     string `json:"keyPem,omitempty"`
	CertSHA256 string `json:"certSha256,omitempty"`
}

func chainInboundFields(s chainInboundSnapshot) map[string]interface{} {
	return map[string]interface{}{"inbound_protocol": s.Protocol, "tls_server_name": s.ServerName, "password": s.Password, "tls_cert_pem": s.CertPEM, "tls_key_pem": s.KeyPEM, "cert_sha256": s.CertSHA256}
}

// BeginChainInboundChange switches the chain's inbound protocol (new
// credentials; the rule's 127.0.0.1 target and port stay) and keeps the old
// inbound as the rollback point until Commit/Revert. The user's link changes.
func (r *Repository) BeginChainInboundChange(id int64, protocol string) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var c model.ForwardChain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, id).Error; err != nil {
			return err
		}
		if c.State != model.ChainStateActive {
			return errors.New("链式规则正在处理中，请稍后再试")
		}
		if c.PrevInbound != "" {
			return ErrChainInFlight
		}
		if c.ChainInbound() == protocol {
			return nil
		}
		var dup int64
		if err := tx.Model(&model.ForwardChain{}).Where("id <> ? AND user_id = ? AND tunnel_id = ? AND upstream_hash = ? AND inbound_protocol IN ? AND state IN ?", id, c.UserID, c.TunnelID, c.UpstreamHash, chainInboundValues(protocol), []string{model.ChainStateCreating, model.ChainStateActive}).Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 {
			return errors.New("这个节点已经在本隧道上建过 " + ChainInboundLabel(model.ForwardChain{InboundProtocol: protocol}) + " 的「我自己的节点」规则")
		}
		prev, _ := json.Marshal(chainInboundSnapshot{Protocol: c.InboundProtocol, ServerName: c.TLSServerName, Password: c.Password, CertPEM: c.TLSCertPEM, KeyPEM: c.TLSKeyPEM, CertSHA256: c.CertSHA256})
		next := c
		if err := setChainInbound(&next, protocol, time.Now()); err != nil {
			return err
		}
		fields := chainInboundFields(chainInboundSnapshot{Protocol: next.InboundProtocol, ServerName: next.TLSServerName, Password: next.Password, CertPEM: next.TLSCertPEM, KeyPEM: next.TLSKeyPEM, CertSHA256: next.CertSHA256})
		fields["prev_inbound"], fields["updated_time"] = string(prev), time.Now().UnixMilli()
		if err := tx.Model(&c).Updates(fields).Error; err != nil {
			return err
		}
		if protocol == model.ChainInboundAnyTLS {
			// The Reality guard port of the copies is not used by AnyTLS.
			if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ? AND guard_port <> 0", id).Update("guard_port", 0).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}

// CommitChainInboundChange drops the rollback point (the old credentials).
func (r *Repository) CommitChainInboundChange(id int64) error {
	return r.db.Model(&model.ForwardChain{}).Where("id = ?", id).Updates(map[string]interface{}{"prev_inbound": "", "updated_time": time.Now().UnixMilli()}).Error
}

// RevertChainInboundChange restores the previous inbound.
func (r *Repository) RevertChainInboundChange(id int64) ([]int64, error) {
	var servers []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var c model.ForwardChain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&c, id).Error; err != nil {
			return err
		}
		if c.PrevInbound == "" {
			return nil
		}
		var prev chainInboundSnapshot
		if err := json.Unmarshal([]byte(c.PrevInbound), &prev); err != nil {
			return err
		}
		fields := chainInboundFields(prev)
		fields["prev_inbound"], fields["updated_time"] = "", time.Now().UnixMilli()
		if err := tx.Model(&c).Updates(fields).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ForwardChainNode{}).Where("chain_id = ?", id).Distinct().Pluck("landing_server_id", &servers).Error; err != nil {
			return err
		}
		return bumpServers(tx, servers)
	})
	return servers, err
}
