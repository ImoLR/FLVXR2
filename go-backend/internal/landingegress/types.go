// Package landingegress holds the landing outbound/routing model shared by the
// panel API, the native agent payload and the mmwx Custom contract
// (landing-egress-v1). It performs no database or network access except the
// explicit WARP client in warp.go.
package landingegress

const Version = 1

// Outbound template types.
const (
	TypeDirect      = "direct"
	TypeWarp        = "warp"
	TypeProxy       = "proxy"
	TypeLandingNode = "landing_node"
)

// Built-in rule actions; templates are referenced as "outbound:<id>".
const (
	ActionDirect = "direct"
	ActionBlock  = "block"
)

var IPVersions = map[string]bool{"system": true, "prefer_ipv4": true, "prefer_ipv6": true, "force_ipv4": true, "force_ipv6": true}

// Settings is the type-specific part of an outbound template, stored as JSON.
type Settings struct {
	// WARP
	WarpMode      string `json:"warpMode,omitempty"` // auto | manual
	WarpLicense   string `json:"warpLicense,omitempty"`
	PrivateKey    string `json:"privateKey,omitempty"`
	PeerPublicKey string `json:"peerPublicKey,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	AddressV4     string `json:"addressV4,omitempty"`
	AddressV6     string `json:"addressV6,omitempty"`
	Reserved      []int  `json:"reserved,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
	// Proxy
	Protocol           string `json:"protocol,omitempty"` // socks | http | shadowsocks | vless
	Server             string `json:"server,omitempty"`
	Port               int    `json:"port,omitempty"`
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	Method             string `json:"method,omitempty"`
	UUID               string `json:"uuid,omitempty"`
	Flow               string `json:"flow,omitempty"`
	RealityServerName  string `json:"realityServerName,omitempty"`
	RealityPublicKey   string `json:"realityPublicKey,omitempty"`
	RealityShortID     string `json:"realityShortId,omitempty"`
	RealityFingerprint string `json:"realityFingerprint,omitempty"`
	// FLVX landing node upstream
	LandingNodeID int64 `json:"landingNodeId,omitempty"`
	ViaForwardID  int64 `json:"viaForwardId,omitempty"`
}

type Outbound struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	IPVersion string   `json:"ipVersion"`
	Settings  Settings `json:"settings"`
	Remark    string   `json:"remark"`
	// UserAllowed (plan 134): users may use it in their node rules.
	UserAllowed bool `json:"userAllowed"`
}

type Rule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Domains        []string `json:"domains"`
	IPs            []string `json:"ips"`
	Ports          string   `json:"ports"`
	SourceIPs      []string `json:"sourceIps"`
	SourcePorts    string   `json:"sourcePorts"`
	Network        string   `json:"network"`
	Protocols      []string `json:"protocols"`
	LandingNodeIDs []int64  `json:"landingNodeIds"`
	// InboundProtocols (plan 149, 「节点协议」) limits the rule to landing nodes
	// with one of these protocols (landing node protocol keys, e.g. ss2022);
	// empty = every node. Compiled into the rule's inbound tags only.
	InboundProtocols []string `json:"inboundProtocols,omitempty"`
	Action           string   `json:"action"`
	Preset           string   `json:"preset,omitempty"`
}

type Profile struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DefaultAction string `json:"defaultAction"`
	Rules         []Rule `json:"rules"`
	// AbuseDefaults keeps the built-in anti-abuse rules (plan 127); nil = on
	// (new profiles, and saves from clients that do not send the field keep
	// the stored value).
	AbuseDefaults *bool `json:"abuseDefaults,omitempty"`
}

// Plan is the server-level compiled form sent to agents and Custom.
type Plan struct {
	Version          int            `json:"version"`
	AllowCoreRestart bool           `json:"allow_core_restart,omitempty"`
	Outbounds        []PlanOutbound `json:"outbounds"`
	Rules            []PlanRule     `json:"rules"`
	ExemptSources    []string       `json:"exempt_sources,omitempty"`
}

type PlanOutbound struct {
	Tag       string         `json:"tag"`
	Type      string         `json:"type"` // direct | wireguard | socks | http | shadowsocks | vless
	IPVersion string         `json:"ip_version"`
	Server    string         `json:"server,omitempty"`
	Port      int            `json:"port,omitempty"`
	Username  string         `json:"username,omitempty"`
	Password  string         `json:"password,omitempty"`
	Method    string         `json:"method,omitempty"`
	UUID      string         `json:"uuid,omitempty"`
	Flow      string         `json:"flow,omitempty"`
	Reality   *PlanReality   `json:"reality,omitempty"`
	WireGuard *PlanWireGuard `json:"wireguard,omitempty"`
	// Transport options of chain upstreams (plan 108 / 177 「链式代理」, native
	// agents only; the same JSON as the agent's EgressOutbound). Empty for
	// every template outbound, so plan 107 output is unchanged.
	Network  string    `json:"network,omitempty"`
	Security string    `json:"security,omitempty"`
	TLS      *PlanTLS  `json:"tls,omitempty"`
	WS       *PlanWS   `json:"ws,omitempty"`
	GRPC     *PlanGRPC `json:"grpc,omitempty"`
	Cipher   string    `json:"cipher,omitempty"`
	Obfs     string    `json:"obfs,omitempty"`
}

type PlanTLS struct {
	ServerName   string   `json:"server_name,omitempty"`
	ALPN         []string `json:"alpn,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	PinnedSHA256 string   `json:"pinned_sha256,omitempty"`
}

type PlanWS struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type PlanGRPC struct {
	ServiceName string `json:"service_name,omitempty"`
	MultiMode   bool   `json:"multi_mode,omitempty"`
}

type PlanReality struct {
	ServerName  string `json:"server_name"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type PlanWireGuard struct {
	PrivateKey    string `json:"private_key"`
	PeerPublicKey string `json:"peer_public_key"`
	Endpoint      string `json:"endpoint,omitempty"`
	EndpointV4    string `json:"endpoint_v4,omitempty"`
	EndpointV6    string `json:"endpoint_v6,omitempty"`
	AddressV4     string `json:"address_v4,omitempty"`
	AddressV6     string `json:"address_v6,omitempty"`
	Reserved      []int  `json:"reserved,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
}

type PlanRule struct {
	Name        string   `json:"name,omitempty"`
	InboundTags []string `json:"inbound_tags"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	SourceIP    []string `json:"source_ip,omitempty"`
	SourcePort  string   `json:"source_port,omitempty"`
	Network     string   `json:"network,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
	Outbound    string   `json:"outbound"`
	// Builtin marks a default anti-abuse rule for agents that support it
	// (see BuiltinAbuse); never sent to older agents or the mmwx Custom.
	Builtin string `json:"builtin,omitempty"`
}

// Active reports whether the plan changes anything versus the built-in default.
func (p *Plan) Active() bool { return p != nil && (len(p.Outbounds) > 0 || len(p.Rules) > 0) }

// Plan and profile limits (contract landing-egress-v1).
const (
	MaxProfileRules     = 64
	MaxProfileItems     = 2000
	MaxPlanOutbounds    = 8
	MaxPlanRules        = 128
	MaxPlanItems        = 4000
	MaxExemptSources    = 1024
	MaxRuleGeoBytes     = 5 << 19 // 2.5 MiB
	MaxPlanGeoBytes     = 3 << 20 // 3 MiB; the Core API accepts 4 MiB per call
	MaxWhitelistEntries = 256
)
