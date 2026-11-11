// Package landing manages the isolated FLVX landing Core. It never changes the
// forwarding agent's existing services or the legacy deployment Core.
package landing

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Plan 202: Custom v1.4.83 = Core f6f58447 (v1.4.77 + port-scan guard
// scan_guard.v1: /v1/config scan_guard + scan_unblock, GET /v1/scan).
const CoreVersion = "v1.4.83"
const SS2022Method = "2022-blake3-aes-128-gcm"

// These hashes were verified against both assets and checksums.txt in the pinned release.
var CoreSHA256 = map[string]string{
	"amd64": "1929c07329f1c257b55c2adb20e54b2f4d3a7e6afa6254b22339d8eae9d943b9",
	"arm64": "6c6403e8a4179d6d296323d3c1749b60097c5e7876cabae42abee56b7718a679",
}

// CorePreviousSHA256 is the pin before CoreVersion (Custom v1.4.77 = Core
// 2bf03c9a, plan 148). A node upgraded from it keeps running and reconciling
// on that verified binary while the new pin cannot be downloaded yet (no disk
// / memory headroom, mirrors down); the download retries with its backoff.
var CorePreviousSHA256 = map[string]string{
	"amd64": "a175bd06addc3afe9a89af43b1ce319bf5a118e93bcc812f4490d9f258dd764a",
	"arm64": "faa5932f6c5febc56bd6ce656f1c60e128aeff18b122e569d0fdd3963c93caab",
}

type Desired struct {
	Revision       int64 `json:"revision"`
	Enabled        bool  `json:"enabled"`
	MaxConnections int64 `json:"max_connections"`
	// MaxConnMode (plan 141): "inbound" = MaxConnections counts the Core's
	// accepted client connections (max_global_inbound_connections); empty =
	// every TCP socket (max_global_total_connections), as before.
	MaxConnMode    string  `json:"max_conn_mode,omitempty"`
	CoreMemoryMB   int     `json:"core_memory_mb"`
	CoreCPUPercent int     `json:"core_cpu_percent"`
	Nodes          []Node  `json:"nodes"`
	Egress         *Egress `json:"egress,omitempty"`
	// UpgradeCoreEpoch (plan 129): the admin confirmed one restart of the
	// Core with this epoch, reported as running an older binary than the
	// pinned one (Status.CoreUpdate). Any other epoch (the restarted Core, a
	// later Core) never matches, so a persisted value cannot restart again.
	UpgradeCoreEpoch string `json:"upgrade_core_epoch,omitempty"`
	// UserLimits (plan 158): per-user Core groups (see user_limits.go). Only
	// sent to agents reporting landing_user_limits.
	UserLimits *UserLimits `json:"user_limits,omitempty"`
	// UserRates (plan 161): per-user 速率, enforced in the kernel (see
	// user_rate.go). Only sent to agents advertising landingRate.
	UserRates *UserRates `json:"user_rates,omitempty"`
	// ScanGuard (plan 202): 禁止端口扫描, see scan_guard.go. Only sent to
	// agents reporting landing_scan_guard.
	ScanGuard *ScanGuard `json:"scan_guard,omitempty"`
	// RealityPinMarginMs (plan 234 「自动切换门槛」): a Reality dest's pin moves
	// to a faster IP only when it is ≥ this many ms faster (0–200; 0 = any
	// gain). Absent = realityPinHysteresis (20). Only sent to agents
	// reporting landing_reality ≥ 3, and only when not 20.
	RealityPinMarginMs *int `json:"reality_pin_margin_ms,omitempty"`
}

type Node struct {
	ID             int64  `json:"id"`
	InboundTag     string `json:"inbound_tag"`
	Protocol       string `json:"protocol"`
	Port           int    `json:"port"`
	Enabled        bool   `json:"enabled"`
	MaxConnections int64  `json:"max_connections"`
	MaxOnlineIPs   int    `json:"max_online_ips"`
	// ConnMode (plan 141): "inbound" = MaxConnections counts accepted client
	// connections of this inbound (Core max_inbound_connections, like a rule's
	// 连接数); empty = all TCP sockets of the port (max_total_connections).
	ConnMode string `json:"conn_mode,omitempty"`
	// MaxTotalConnections (plan 211, landing_conn_mode 2): the node's 节点总连接数
	// = every TCP socket of the port in any state (Core port
	// max_total_connections), applied next to an inbound MaxConnections. 0 =
	// none; with a total-mode MaxConnections as well, the smaller one wins.
	MaxTotalConnections int64   `json:"max_total_connections,omitempty"`
	SS2022              *SS2022 `json:"ss2022,omitempty"`
	VLESS               *VLESS  `json:"vless,omitempty"`
	// Listen is empty (all addresses) or the loopback address of a chain node.
	Listen string `json:"listen,omitempty"`
	// Chain is the private upstream of a chain node (plan 108): everything the
	// inbound accepts leaves through it, right after the safety rule.
	Chain *EgressOutbound `json:"chain,omitempty"`
	// GuardPort is the loopback port of the Reality guard (plan 127) of a
	// VLESS Reality node; 0 = no guard. Nodes sharing dest and serverNames
	// share the port. See guard.go.
	GuardPort int `json:"guard_port,omitempty"`
	// QuotaGroup (plan 158): the owner's landing group ("landing-user-<id>")
	// when the owner is limited on this server; "" = no per-user group.
	QuotaGroup string `json:"quota_group,omitempty"`
	// RateGroup (plan 161): the owner's rate group ("landing-user-<id>") when
	// the owner has a 速率 on this server; "" = not shaped.
	RateGroup string `json:"rate_group,omitempty"`
}

// ConnModeVersion is reported as landing_conn_mode: this agent applies
// conn_mode / max_conn_mode "inbound" and reports Counter.InboundConnections.
// Older agents ignore both fields, so the panel only sends them here.
// Version 2 (plan 211) also applies nodes[].max_total_connections next to an
// inbound limit and reports Counter.TCP (the socket breakdown).
const ConnModeVersion = 2

// ConnModeInbound is the only non-default conn_mode / max_conn_mode value.
const ConnModeInbound = "inbound"

func validConnMode(mode string) bool { return mode == "" || mode == "total" || mode == ConnModeInbound }

// ChainVersion is the chain-node schema (listen + chain) this agent applies.
// Older agents ignore both fields, so the panel only sends chain nodes to
// agents that report it.
const ChainVersion = 1

// ChainListen is the only listen address a chain node may use: the tunnel's
// exit relay dials it locally, nothing outside the server can reach it.
const ChainListen = "127.0.0.1"

const maxChainNodes = 1000

func chainOutboundTag(id int64) string { return "flvxc-" + strconv.FormatInt(id, 10) }

type SS2022 struct {
	Method    string `json:"method"`
	ServerKey string `json:"server_key"`
	UserKey   string `json:"user_key"`
}
type VLESS struct {
	UUID    string  `json:"uuid"`
	Flow    string  `json:"flow"`
	Reality Reality `json:"reality"`
}
type Reality struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"server_names"`
	PrivateKey  string   `json:"private_key"`
	ShortIDs    []string `json:"short_ids"`
}
type Counter struct {
	ID            int64      `json:"id"`
	InboundTag    string     `json:"inbound_tag"`
	Epoch         string     `json:"epoch"`
	UpBytes       int64      `json:"up_bytes"`
	DownBytes     int64      `json:"down_bytes"`
	Connections   int64      `json:"connections"`
	OnlineIPCount *int       `json:"online_ip_count,omitempty"`
	OnlineIPs     []OnlineIP `json:"online_ips,omitempty"`
	// InboundConnections (plan 141) = the Core's live inbound client
	// connections of this tag (sum of proxy_users inbound_current); nil while
	// the Core snapshot has none (never for the pinned Core).
	InboundConnections *int64 `json:"inbound_connections,omitempty"`
	// TCP (plan 211): the port's sockets by direction and state, summed from
	// the same Core snapshot (Inbound + Outbound = Connections); nil while the
	// Core reports no TCP state counts.
	TCP *TCPCounts `json:"tcp,omitempty"`
}

// TCPCounts is a node's socket breakdown: inbound (client side) and outbound
// sockets in any state, and how many of all of them are ESTABLISHED /
// TIME_WAIT / CLOSE_WAIT (zeros omitted: the object is sent every 5 s).
type TCPCounts struct {
	Inbound     int64 `json:"inbound,omitempty"`
	Outbound    int64 `json:"outbound,omitempty"`
	Established int64 `json:"established,omitempty"`
	TimeWait    int64 `json:"time_wait,omitempty"`
	CloseWait   int64 `json:"close_wait,omitempty"`
}
type OnlineIP struct {
	IP          string `json:"ip"`
	Connections int64  `json:"connections"`
}
type Status struct {
	Revision    int64         `json:"revision"`
	Epoch       string        `json:"epoch"`
	CoreVersion string        `json:"core_version"`
	State       string        `json:"state"`
	Error       string        `json:"error,omitempty"`
	Nodes       []Counter     `json:"nodes"`
	Egress      *EgressStatus `json:"egress,omitempty"`
	// CoreUpdate (plan 129): "restart_required" while the running Core was
	// started before the pinned binary on disk was written (agent OTA with a
	// new pin): the new Core only takes over after one restart.
	CoreUpdate string `json:"core_update,omitempty"`
	// NodeErrors (plan 147): nodes whose inbound could not be added, with port
	// and reason. Always present on agents that report them (possibly empty);
	// older agents omit it and fail the whole reconcile instead.
	NodeErrors []NodeError `json:"node_errors"`
	// UserRate (plan 161): the kernel state of the per-user 速率; present on
	// agents advertising landingRate.
	UserRate *RateStatus `json:"user_rate,omitempty"`
	// ScanGuard (plan 202): the 禁止端口扫描 state and the hits the panel has
	// not recorded yet; present on agents reporting landing_scan_guard.
	ScanGuard *ScanStatus `json:"scan_guard,omitempty"`
	// RealityPins (plan 227): the pinned IP of every guarded Reality dest;
	// present on agents reporting landing_reality.
	RealityPins []RealityPinStatus `json:"reality_pins,omitempty"`
}

func (d *Desired) validate() error {
	if d.Revision <= 0 || d.MaxConnections < 0 || !validConnMode(d.MaxConnMode) {
		return fmt.Errorf("invalid landing revision or limit")
	}
	if d.CoreMemoryMB == 0 {
		d.CoreMemoryMB = 256
	}
	if d.CoreCPUPercent == 0 {
		d.CoreCPUPercent = 50
	}
	if d.CoreMemoryMB < 32 || d.CoreMemoryMB > 65536 || d.CoreCPUPercent < 1 || d.CoreCPUPercent > 10000 {
		return fmt.Errorf("invalid landing resource limits")
	}
	if m := d.RealityPinMarginMs; m != nil && (*m < 0 || *m > realityPinMarginMax) {
		return fmt.Errorf("invalid reality pin margin")
	}
	ids, ports := map[int64]bool{}, map[int]bool{}
	chains := 0
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.ID <= 0 || n.Port < 1024 || n.Port > 65535 || ids[n.ID] || ports[n.Port] || n.MaxConnections < 0 || n.MaxTotalConnections < 0 || n.MaxOnlineIPs < 0 || !validConnMode(n.ConnMode) {
			return fmt.Errorf("invalid landing node identity, port or limit")
		}
		ids[n.ID], ports[n.Port] = true, true
		tag := "flvxl-" + strconv.FormatInt(n.ID, 10)
		if n.InboundTag == "" {
			n.InboundTag = tag
		}
		if n.InboundTag != tag {
			return fmt.Errorf("invalid landing inbound tag")
		}
		if n.Chain != nil && n.Chain.Tag == "" {
			n.Chain.Tag = chainOutboundTag(n.ID)
		}
		if err := n.validateChain(); err != nil {
			return err
		}
		if n.Chain != nil {
			chains++
		}
		if _, err := n.inbound(); err != nil {
			return err
		}
	}
	if chains > maxChainNodes {
		return fmt.Errorf("too many chain nodes")
	}
	if err := d.UserLimits.validate(d.Nodes); err != nil {
		return err
	}
	if err := d.UserRates.validate(d.Nodes); err != nil {
		return err
	}
	if err := d.ScanGuard.validate(d.Nodes); err != nil {
		return err
	}
	if d.Egress != nil {
		tags := map[string]bool{}
		for _, n := range d.Nodes {
			tags[n.InboundTag] = true
		}
		if err := d.Egress.validate(tags); err != nil {
			return fmt.Errorf("invalid landing egress: %w", err)
		}
	}
	return nil
}

// validateChain: a chain node listens on loopback only and is always VLESS
// Reality Vision; a loopback listener is never a public landing node.
func (n Node) validateChain() error {
	if n.Listen != "" && n.Listen != ChainListen {
		return fmt.Errorf("invalid landing listen address")
	}
	if n.Chain == nil {
		if n.Listen != "" {
			return fmt.Errorf("loopback landing node without chain upstream")
		}
		return nil
	}
	if n.Listen != ChainListen || n.Protocol != "vless_reality_vision" {
		return fmt.Errorf("chain node must be loopback VLESS Reality Vision")
	}
	o := *n.Chain
	if o.Tag != chainOutboundTag(n.ID) || o.Type == "direct" || o.Type == "wireguard" {
		return fmt.Errorf("invalid chain upstream for node %d", n.ID)
	}
	if err := o.validate(); err != nil {
		return fmt.Errorf("chain upstream: %w", err)
	}
	return nil
}

// publicPorts lists the inbound ports reachable from outside: SS2022 serves TCP
// and UDP, VLESS Reality TCP; loopback chain nodes are not public.
func (d Desired) publicPorts() *PortsInUse {
	p := &PortsInUse{}
	if !d.Enabled {
		return p
	}
	for _, n := range d.Nodes {
		if !n.Enabled || n.Listen != "" || n.Port <= 0 {
			continue
		}
		p.TCP = append(p.TCP, n.Port)
		if n.Protocol == "ss2022" {
			p.UDP = append(p.UDP, n.Port)
		}
	}
	return p
}

// inboundPorts: every enabled node port (public and chain copies).
func (d Desired) inboundPorts() *map[int]struct{} {
	p := map[int]struct{}{}
	if d.Enabled {
		for _, n := range d.Nodes {
			if n.Enabled && n.Port > 0 {
				p[n.Port] = struct{}{}
			}
		}
	}
	return &p
}

func (n Node) inbound() (map[string]any, error) {
	listen := "0.0.0.0"
	if n.Listen != "" {
		listen = n.Listen
	}
	// A sniffed public Host/SNI must not replace a private original domain for
	// routing: retain protocol sniffing, but exclude all domain overrides.
	inbound := map[string]any{"tag": n.InboundTag, "listen": listen, "port": n.Port, "sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true, "domainsExcluded": []string{"regexp:.*"}}}
	switch n.Protocol {
	case "ss2022":
		if n.SS2022 == nil || n.SS2022.Method != SS2022Method {
			return nil, fmt.Errorf("invalid SS2022 method")
		}
		for _, key := range []string{n.SS2022.ServerKey, n.SS2022.UserKey} {
			b, err := base64.StdEncoding.Strict().DecodeString(key)
			if err != nil || len(b) != 16 {
				return nil, fmt.Errorf("SS2022 keys must encode exactly 16 bytes")
			}
		}
		inbound["protocol"] = "shadowsocks"
		inbound["settings"] = map[string]any{"method": SS2022Method, "password": n.SS2022.ServerKey, "network": "tcp,udp", "clients": []any{map[string]any{"password": n.SS2022.UserKey, "email": n.InboundTag}}}
		inbound["streamSettings"] = map[string]any{"network": "tcp"}
	case "vless_reality_vision":
		if n.VLESS == nil {
			return nil, fmt.Errorf("missing VLESS credentials")
		}
		v := n.VLESS
		if _, err := uuid.Parse(v.UUID); err != nil || v.Flow != "xtls-rprx-vision" {
			return nil, fmt.Errorf("invalid VLESS credentials")
		}
		key, err := base64.RawURLEncoding.Strict().DecodeString(v.Reality.PrivateKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid Reality private key")
		}
		if _, _, err := net.SplitHostPort(v.Reality.Dest); err != nil || len(v.Reality.ServerNames) == 0 || len(v.Reality.ShortIDs) == 0 {
			return nil, fmt.Errorf("invalid Reality destination")
		}
		for _, sid := range v.Reality.ShortIDs {
			if b, err := hex.DecodeString(sid); err != nil || len(b) > 8 {
				return nil, fmt.Errorf("invalid Reality short id")
			}
		}
		for _, name := range v.Reality.ServerNames {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("empty Reality server name")
			}
		}
		inbound["protocol"] = "vless"
		inbound["settings"] = map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": v.UUID, "email": n.InboundTag, "flow": "xtls-rprx-vision"}}}
		inbound["streamSettings"] = map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"dest": v.Reality.Dest, "serverNames": v.Reality.ServerNames, "privateKey": v.Reality.PrivateKey, "shortIds": v.Reality.ShortIDs}}
	default:
		return nil, fmt.Errorf("unsupported landing protocol")
	}
	return inbound, nil
}

var blockedDestinations = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8"}

// landingHandshakeSeconds: how long the Core keeps a connection that has not
// finished its protocol handshake. Plan 244: 4 → 120 s so that an entry's
// warm pool (端口转发 to this landing) can hold idle pre-dialed conns (the pool
// replaces them after 90–110 s). The Core reads it only at start, so it takes
// effect at the next Core start; there is no hot path and no automatic
// restart for it. connIdle (300 s, after the handshake) is unchanged.
const landingHandshakeSeconds = 120

// baseConfig is read only when the Core starts. RoutingService enables hot
// rule replacement; IPOnDemand resolves a domain at the first IP condition (the
// safety rule), so no later domain rule can route a name that resolves to a
// private address. Policy timeouts are the Core defaults, stated explicitly so
// half-open connections are reaped on every level.
func baseConfig(apiPort int) map[string]any {
	return map[string]any{
		"log":   map[string]any{"loglevel": "warning", "access": "none"},
		"api":   map[string]any{"tag": "flvxl-api", "listen": net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)), "services": []string{"HandlerService", "StatsService", "RoutingService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{"handshake": landingHandshakeSeconds, "connIdle": 300, "uplinkOnly": 2, "downlinkOnly": 5}},
			"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"inbounds":  []any{},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}}, map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing": map[string]any{"domainStrategy": "IPOnDemand", "rules": []any{
			// The separate loopback API listener needs no routing exception.
			map[string]any{"type": "field", "ruleTag": "flvx-safety", "ip": blockedDestinations, "outboundTag": "block"},
		}},
	}
}

func canonical(v any) string { b, _ := json.Marshal(v); return string(b) }
