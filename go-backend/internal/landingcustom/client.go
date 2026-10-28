// Package landingcustom implements only the Custom FLVX v1 tenant contract.
package landingcustom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-backend/internal/landingegress"
)

const DefaultBaseURL = "https://mmwxc.imgamer.top"
const Prefix = "/api/custom/flvx/v1"

type Hosts struct {
	IPv4   string `json:"ipv4"`
	IPv6   string `json:"ipv6"`
	Domain string `json:"domain"`
}
type Core struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Epoch   string `json:"epoch"`
}
type Share struct {
	ID             int64    `json:"server_id"`
	Name           string   `json:"name"`
	Hosts          Hosts    `json:"hosts"`
	Status         string   `json:"status"`
	PortMin        int      `json:"port_min"`
	PortMax        int      `json:"port_max"`
	TrafficLimit   int64    `json:"traffic_limit_bytes"`
	TrafficUsed    int64    `json:"traffic_used_bytes"`
	ResetDay       int      `json:"reset_day"`
	MaxConnections int      `json:"max_connections"`
	Connections    int      `json:"connections"`
	ShareUntil     *int64   `json:"share_until"`
	Core           Core     `json:"core"`
	Protocols      []string `json:"protocols"`
	DualInstall    bool     `json:"dual_install"`
	// Egress is Custom's landing-egress capability; absent on Custom versions
	// without outbound/routing support (contract landing-egress-v1).
	Egress *ShareEgress `json:"egress,omitempty"`
	// Contract landing-protocols-v1. ProtocolCaps advertises AnyTLS / Snell /
	// Mieru for this share (absent = not supported: FLVX hides them; Protocols
	// above stays the v1 list so older FLVX versions are unaffected).
	// ServerMode is the official server mode, shown as information only.
	// BusyPorts are ports inside the share range already taken on the machine
	// (official inbounds, other panels); FLVX never picks them for new nodes.
	ProtocolCaps *ProtocolCaps `json:"protocol_caps,omitempty"`
	ServerMode   string        `json:"server_mode,omitempty"`
	BusyPorts    []int         `json:"busy_ports,omitempty"`
	// Contract landing-conn-mode-v1: the share's Helper applies a node's
	// 仅入站 limit (Node.ConnMode "inbound" → Core max_inbound_connections)
	// and reports NodeStatus.InboundConnections. Absent = every node limit is
	// 入站+出站 (max_total_connections), as before.
	ConnModes *ShareConnModes `json:"conn_modes,omitempty"`
	// Contract landing-reality-guard-v1 (plan 129): present only while this
	// share's Helper renders the Reality guard; absent = FLVX sends no
	// reality_guard field and the share behaves exactly as before.
	RealityGuard *ShareRealityGuard `json:"reality_guard,omitempty"`
	// Contract landing-user-limits-v1 (plan 158): present only while this
	// share's Helper groups the FLVX Core instance's nodes per user (Core
	// management groups), applies node limits as 入站 (conn_mode "inbound")
	// and reports per-user inbound usage. Absent = FLVX sends nothing new and
	// does not count this share in user totals (today's behaviour).
	UserLimits *ShareUserLimits `json:"user_limits,omitempty"`
}

// UserLimitsVersion is the landing-user-limits contract version FLVX speaks.
const UserLimitsVersion = 1

// ShareUserLimits is the share list's user_limits capability object.
type ShareUserLimits struct {
	Versions []int `json:"versions"`
}

// Supports reports whether the share accepts user-limits contract version v.
func (u *ShareUserLimits) Supports(v int) bool {
	return u != nil && containsInt(u.Versions, v)
}

// RealityGuardVersion is the landing-reality-guard contract version FLVX speaks.
const RealityGuardVersion = 1

// ShareRealityGuard is the share list's reality_guard capability object.
type ShareRealityGuard struct {
	Versions []int `json:"versions"`
}

// Supports reports whether the share accepts guard contract version v.
func (g *ShareRealityGuard) Supports(v int) bool {
	return g != nil && containsInt(g.Versions, v)
}

// RealityGuardStatus is the Helper's guard report relayed by Custom: state
// on | restart_required | error and the number of guarded nodes. Absent =
// the guard is not asked for, or the share has no VLESS Reality node.
type RealityGuardStatus struct {
	State string `json:"state"`
	Nodes int    `json:"nodes"`
}

// ConnModeVersion is the landing-conn-mode contract version FLVX speaks.
const ConnModeVersion = 1

// ConnModeInbound is the only non-default Node.ConnMode value: the node's
// max_connections counts accepted client connections only.
const ConnModeInbound = "inbound"

// ShareConnModes is the share list's conn_modes object.
type ShareConnModes struct {
	Versions []int `json:"versions"`
}

// Supports reports whether the share accepts conn-mode contract version v.
func (c *ShareConnModes) Supports(v int) bool {
	return c != nil && containsInt(c.Versions, v)
}

// Extra landing protocols of contract landing-protocols-v1.
const (
	ProtocolAnyTLS = "anytls"
	ProtocolSnell  = "snell"
	ProtocolMieru  = "mieru"
)

// ExtraProtocols lists them in display order.
var ExtraProtocols = []string{ProtocolAnyTLS, ProtocolSnell, ProtocolMieru}

// IsExtraProtocol reports whether p is one of the contract's extra protocols.
func IsExtraProtocol(p string) bool {
	return p == ProtocolAnyTLS || p == ProtocolSnell || p == ProtocolMieru
}

// ProtocolCaps: Versions are the contract versions the share accepts (FLVX
// requires 1); Protocols holds one entry per protocol the share's Helper and
// FLVX Core can render AND account for.
type ProtocolCaps struct {
	Versions  []int                  `json:"versions"`
	Protocols map[string]ProtocolCap `json:"protocols"`
}

// ProtocolCap: UDP = UDP relay of that protocol works on the share's Core
// (client configs enable UDP only then). Versions / Obfs (Snell) and
// Transports (Mieru) restrict the per-node options; empty = contract default
// (Snell versions 4 and 5, obfs none/http/tls; Mieru TCP and UDP).
type ProtocolCap struct {
	UDP        bool     `json:"udp"`
	Versions   []int    `json:"versions,omitempty"`
	Obfs       []string `json:"obfs,omitempty"`
	Transports []string `json:"transports,omitempty"`
}

// Supports returns the capability of protocol p, if advertised under v1.
func (c *ProtocolCaps) Supports(p string) (ProtocolCap, bool) {
	if c == nil || !IsExtraProtocol(p) {
		return ProtocolCap{}, false
	}
	ok := false
	for _, v := range c.Versions {
		ok = ok || v == 1
	}
	if !ok {
		return ProtocolCap{}, false
	}
	cp, found := c.Protocols[p]
	return cp, found
}

// AllowsSnellVersion / AllowsObfs / AllowsTransport apply the contract
// defaults when the list is empty.
func (c ProtocolCap) AllowsSnellVersion(v int) bool {
	if v != 4 && v != 5 {
		return false
	}
	return len(c.Versions) == 0 || containsInt(c.Versions, v)
}
func (c ProtocolCap) AllowsObfs(mode string) bool {
	if mode != "none" && mode != "http" && mode != "tls" {
		return false
	}
	return len(c.Obfs) == 0 || containsString(c.Obfs, mode)
}
func (c ProtocolCap) AllowsTransport(t string) bool {
	if t != "TCP" && t != "UDP" {
		return false
	}
	return len(c.Transports) == 0 || containsString(c.Transports, t)
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Sanitize bounds the optional landing-protocols-v1 fields of a share read
// from Custom: unknown protocols and option values are dropped, busy ports are
// kept only inside the share's port range (deduplicated, at most 4096), and an
// unexpected server mode is cleared. A malformed optional field never fails
// the share list (the v1 fields keep their strict checks).
func (s *Share) Sanitize() {
	if s.ProtocolCaps != nil {
		caps := &ProtocolCaps{Versions: []int{}, Protocols: map[string]ProtocolCap{}}
		for _, v := range s.ProtocolCaps.Versions {
			if v > 0 && v < 1000 && !containsInt(caps.Versions, v) && len(caps.Versions) < 8 {
				caps.Versions = append(caps.Versions, v)
			}
		}
		for _, p := range ExtraProtocols {
			cp, ok := s.ProtocolCaps.Protocols[p]
			if !ok {
				continue
			}
			clean := ProtocolCap{UDP: cp.UDP}
			if p == ProtocolSnell {
				for _, v := range cp.Versions {
					if (v == 4 || v == 5) && !containsInt(clean.Versions, v) {
						clean.Versions = append(clean.Versions, v)
					}
				}
				for _, o := range cp.Obfs {
					if (o == "none" || o == "http" || o == "tls") && !containsString(clean.Obfs, o) {
						clean.Obfs = append(clean.Obfs, o)
					}
				}
				// An explicit list without any known value supports nothing usable.
				if (len(cp.Versions) > 0 && len(clean.Versions) == 0) || (len(cp.Obfs) > 0 && len(clean.Obfs) == 0) {
					continue
				}
			}
			if p == ProtocolMieru {
				for _, t := range cp.Transports {
					if (t == "TCP" || t == "UDP") && !containsString(clean.Transports, t) {
						clean.Transports = append(clean.Transports, t)
					}
				}
				if len(cp.Transports) > 0 && len(clean.Transports) == 0 {
					continue
				}
			}
			caps.Protocols[p] = clean
		}
		s.ProtocolCaps = caps
		if len(caps.Protocols) == 0 || !containsInt(caps.Versions, 1) {
			s.ProtocolCaps = nil
		}
	}
	if len(s.BusyPorts) > 0 {
		seen := map[int]bool{}
		ports := make([]int, 0, len(s.BusyPorts))
		for _, p := range s.BusyPorts {
			if p >= s.PortMin && p <= s.PortMax && !seen[p] && len(ports) < 4096 {
				seen[p] = true
				ports = append(ports, p)
			}
		}
		s.BusyPorts = ports
		if len(ports) == 0 {
			s.BusyPorts = nil
		}
	}
	switch s.ServerMode {
	case "", "embedded", "external":
	default:
		s.ServerMode = ""
	}
	if s.ConnModes != nil {
		versions := []int{}
		for _, v := range s.ConnModes.Versions {
			if v > 0 && v < 1000 && !containsInt(versions, v) && len(versions) < 8 {
				versions = append(versions, v)
			}
		}
		s.ConnModes = &ShareConnModes{Versions: versions}
		if len(versions) == 0 {
			s.ConnModes = nil
		}
	}
	if s.RealityGuard != nil {
		versions := []int{}
		for _, v := range s.RealityGuard.Versions {
			if v > 0 && v < 1000 && !containsInt(versions, v) && len(versions) < 8 {
				versions = append(versions, v)
			}
		}
		s.RealityGuard = &ShareRealityGuard{Versions: versions}
		if len(versions) == 0 {
			s.RealityGuard = nil
		}
	}
	if s.UserLimits != nil {
		versions := []int{}
		for _, v := range s.UserLimits.Versions {
			if v > 0 && v < 1000 && !containsInt(versions, v) && len(versions) < 8 {
				versions = append(versions, v)
			}
		}
		s.UserLimits = &ShareUserLimits{Versions: versions}
		if len(versions) == 0 {
			s.UserLimits = nil
		}
	}
}

type ShareEgress struct {
	Versions []int `json:"versions"`
	Allowed  bool  `json:"allowed"`
	GeoIP    bool  `json:"geoip"`
	GeoSite  bool  `json:"geosite"`
}

// Supports reports whether the share accepts egress plan version v.
func (e *ShareEgress) Supports(v int) bool {
	if e == nil {
		return false
	}
	for _, x := range e.Versions {
		if x == v {
			return true
		}
	}
	return false
}

type Owner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SS2022 struct {
	Method    string `json:"method"`
	ServerKey string `json:"server_key"`
	UserKey   string `json:"user_key"`
}
type Reality struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"server_names"`
	PrivateKey  string   `json:"private_key"`
	ShortIDs    []string `json:"short_ids"`
}
type VLESS struct {
	UUID    string  `json:"uuid"`
	Flow    string  `json:"flow"`
	Reality Reality `json:"reality"`
}
type Node struct {
	ID             string `json:"id"`
	Owner          Owner  `json:"owner"`
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Port           int    `json:"port"`
	Enabled        bool   `json:"enabled"`
	MaxConnections int    `json:"max_connections"`
	MaxOnlineIPs   int    `json:"max_online_ips"`
	ExpiresAt      *int64 `json:"expires_at"`
	// landing-conn-mode-v1: "inbound" = max_connections counts client
	// connections only; omitted = 入站+出站 (byte-identical for older Custom).
	// Sent only to shares whose ConnModes advertise version 1.
	ConnMode string  `json:"conn_mode,omitempty"`
	SS2022   *SS2022 `json:"ss2022,omitempty"`
	VLESS    *VLESS  `json:"vless,omitempty"`
	// landing-protocols-v1: exactly one object, matching Protocol. Only sent
	// to shares whose ProtocolCaps advertise the protocol.
	AnyTLS *AnyTLS `json:"anytls,omitempty"`
	Snell  *Snell  `json:"snell,omitempty"`
	Mieru  *Mieru  `json:"mieru,omitempty"`
}

// AnyTLS over TLS with an FLVX-generated self-signed ECDSA P-256 certificate
// (clients pin its SHA-256). Padding "default" = the AnyTLS standard scheme.
type AnyTLS struct {
	Password string    `json:"password"`
	Padding  string    `json:"padding"`
	TLS      AnyTLSTLS `json:"tls"`
}
type AnyTLSTLS struct {
	ServerName     string `json:"server_name"`
	CertificatePEM string `json:"certificate_pem"`
	KeyPEM         string `json:"key_pem"`
}

// Snell v4/v5; Obfs none|http|tls (ObfsHost required unless none). TCP
// inbound; UDP is Snell's UDP-over-TCP.
type Snell struct {
	PSK      string `json:"psk"`
	Version  int    `json:"version"`
	Obfs     string `json:"obfs"`
	ObfsHost string `json:"obfs_host"`
}

// Mieru: one inbound serves TCP and UDP underlay clients on the same port.
type Mieru struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type Desired struct {
	Revision int64               `json:"revision"`
	Nodes    []Node              `json:"nodes"`
	Egress   *landingegress.Plan `json:"egress,omitempty"`
	// RealityGuard (landing-reality-guard-v1) is the server's 防盗 switch,
	// sent only to shares advertising the capability; absent = off there.
	RealityGuard *bool `json:"reality_guard,omitempty"`
}
type NodeStatus struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	Error         string     `json:"error"`
	UpBytes       int64      `json:"up_bytes"`
	DownBytes     int64      `json:"down_bytes"`
	Connections   int        `json:"connections"`
	OnlineIPCount *int       `json:"online_ip_count,omitempty"`
	OnlineIPs     []OnlineIP `json:"online_ips,omitempty"`
	// landing-conn-mode-v1: live inbound (client) connections of the node's
	// inbound (Core inbound_current); absent = unknown (older Helper).
	InboundConnections *int `json:"inbound_connections,omitempty"`
}
type OnlineIP struct {
	IP          string `json:"ip"`
	Connections int    `json:"connections"`
}
type Status struct {
	AppliedRevision int64         `json:"applied_revision"`
	Core            Core          `json:"core"`
	Nodes           []NodeStatus  `json:"nodes"`
	TrafficUsed     int64         `json:"share_traffic_used_bytes"`
	CollectedAt     int64         `json:"collected_at"`
	Egress          *EgressStatus `json:"egress,omitempty"`
	// Machine is the shared server's whole-machine connection state (contract
	// machine-protect-v1); absent (older Custom/Helper) = unknown.
	Machine *Machine `json:"machine,omitempty"`
	// RealityGuard (landing-reality-guard-v1); a malformed or unknown report
	// is dropped (Client.Status) instead of failing the poll.
	RealityGuard *RealityGuardStatus `json:"reality_guard,omitempty"`
}

// UnmarshalJSON never fails: a malformed optional guard object must not stop
// the status poll (billing); Valid then rejects it.
func (g *RealityGuardStatus) UnmarshalJSON(b []byte) error {
	type plain RealityGuardStatus
	var v plain
	if json.Unmarshal(b, &v) != nil {
		*g = RealityGuardStatus{}
		return nil
	}
	*g = RealityGuardStatus(v)
	return nil
}

// Valid reports a known state with a bounded node count.
func (g *RealityGuardStatus) Valid() bool {
	return g != nil && (g.State == "on" || g.State == "restart_required" || g.State == "error") && g.Nodes >= 0 && g.Nodes <= 1000
}

// MachineConns: every TCP+UDP socket row of the server (all states), the number
// the mmwx server card shows; same keys as the FLVX agent's machine_conns.
type MachineConns struct {
	Total       int64 `json:"total"`
	TCP         int64 `json:"tcp"`
	UDP         int64 `json:"udp"`
	Established int64 `json:"established"`
	TimeWait    int64 `json:"time_wait"`
	CloseWait   int64 `json:"close_wait"`
	OtherTCP    int64 `json:"other_tcp"`
}

// MachineProtect: the server's machine protection; same keys as the FLVX agent's
// machine_protect. Null numbers = not set.
type MachineProtect struct {
	Owner            string `json:"owner"`
	Enabled          bool   `json:"enabled"`
	MaxTotal         *int64 `json:"max_total"`
	TWBuckets        *int64 `json:"tw_buckets"`
	CloseWaitSeconds *int64 `json:"close_wait_seconds"`
	Blocking         bool   `json:"blocking"`
	DroppedTotal     int64  `json:"dropped_total"`
}

// Machine is {"conns": {...}, "protect": {...}, "updated_at": RFC3339}; the
// spellings "machine_conns" / "machine_protect" are accepted as well.
type Machine struct {
	Conns     *MachineConns   `json:"conns,omitempty"`
	Protect   *MachineProtect `json:"protect,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
}

func (m *Machine) UnmarshalJSON(b []byte) error {
	var raw struct {
		Conns          *MachineConns   `json:"conns"`
		Protect        *MachineProtect `json:"protect"`
		MachineConns   *MachineConns   `json:"machine_conns"`
		MachineProtect *MachineProtect `json:"machine_protect"`
		UpdatedAt      string          `json:"updated_at"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		// A malformed optional object must never fail the status poll (billing).
		*m = Machine{}
		return nil
	}
	*m = Machine{Conns: raw.Conns, Protect: raw.Protect, UpdatedAt: raw.UpdatedAt}
	if m.Conns == nil {
		m.Conns = raw.MachineConns
	}
	if m.Protect == nil {
		m.Protect = raw.MachineProtect
	}
	return nil
}

// EgressStatus is Custom's report of the applied egress plan.
type EgressStatus struct {
	State           string   `json:"state"` // none | applied | error
	Error           string   `json:"error,omitempty"`
	AppliedRevision int64    `json:"applied_revision"`
	Warnings        []string `json:"warnings,omitempty"`
}
type LandingHosts struct {
	Native []string `json:"native_landing_hosts"`
	Nodes  []string `json:"flvx_node_hosts"`
}
type ValidationError struct {
	NodeID  string `json:"node_id"`
	Field   string `json:"field"`
	Message string `json:"message"`
}
type APIError struct {
	Status   int
	Code     string
	Problems []ValidationError
}

// Known Custom error codes map to specific, actionable text. Only the code is
// used (never the remote message), so nothing remote is echoed into the UI.
var apiErrorText = map[string]string{
	"native_landing":         "妙妙屋拒绝：该服务器已启用 FLVX 自有落地，不能同时共享",
	"host_resolution":        "妙妙屋暂时无法解析/核验该服务器或 FLVX 节点地址（DNS），恢复后自动重试",
	"official_ports_unknown": "妙妙屋端暂时读取不到该服务器的官方入站端口（官方面板/Helper 未上报），恢复后自动重试",
	"share_inactive":         "妙妙屋端该共享已暂停或撤销，不接受节点配置",
	"share_revoked":          "妙妙屋端该共享已撤销",
	"revision_conflict":      "妙妙屋拒绝：配置版本冲突（妙妙屋端已有同号或更新的版本），请联系管理员检查",
	"invalid_hosts":          "妙妙屋无法解析 FLVX 节点地址，请检查节点的域名/IP 设置",
	"helper_outdated":        "妙妙屋端尚未就绪：该服务器的 Helper 需升级",
	"unsupported":            "妙妙屋端尚未就绪：该服务器的 Helper 不支持 FLVX 共享，需升级",
	"helper_unsupported":     "妙妙屋端尚未就绪：该服务器的 Helper 不支持 FLVX 共享，需升级",
	"unavailable":            "妙妙屋端存储或服务器清单暂不可用",
	"not_found":              "妙妙屋端找不到该共享（可能已删除）",
	"unauthorized":           "妙妙屋接入令牌无效或已撤销",
}

func (e *APIError) Error() string {
	if text, ok := apiErrorText[e.Code]; ok {
		return text
	}
	code := ""
	if e.Code != "" && len(e.Code) <= 40 && strings.Trim(e.Code, "abcdefghijklmnopqrstuvwxyz0123456789_") == "" {
		code = "，" + e.Code
	}
	switch e.Status {
	case 401, 403:
		return "妙妙屋接入令牌无效或已撤销"
	case 422:
		if code == "" {
			return "妙妙屋拒绝节点配置，请检查节点错误"
		}
		return fmt.Sprintf("妙妙屋拒绝配置（HTTP 422%s）", code)
	case 409:
		if code == "" {
			return "妙妙屋暂未接受配置（共享状态、地址解析或版本冲突）"
		}
		return fmt.Sprintf("妙妙屋暂未接受配置（HTTP 409%s）", code)
	default:
		return fmt.Sprintf("妙妙屋同步异常（HTTP %d%s）", e.Status, code)
	}
}

// Base URLs cannot carry credentials, query strings or fragments. Local HTTP is
// useful for the isolated contract lab; deployments use HTTPS.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = DefaultBaseURL
	}
	raw = strings.TrimSuffix(raw, Prefix)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("妙妙屋地址必须是无凭据的 HTTP(S) 基础地址")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("妙妙屋非本机地址必须使用 HTTPS")
		}
	}
	return u.String(), nil
}

type Client struct {
	base, token string
	http        *http.Client
}

func New(base, token string) (*Client, error) {
	base, err := NormalizeBaseURL(base)
	if err != nil {
		return nil, err
	}
	if token == "" || len(token) > 240 || strings.ContainsAny(token, "\r\n \t") {
		return nil, errors.New("请配置妙妙屋接入令牌")
	}
	return &Client{base: base, token: token, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return errors.New("妙妙屋请求编码失败")
		}
	}
	if len(data) > 2<<20 {
		return errors.New("妙妙屋期望状态超过 2 MiB 上限")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+Prefix+path, bytes.NewReader(data))
	if err != nil {
		return errors.New("妙妙屋请求无效")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	// Never expose URL errors, headers, response bodies or structured credentials.
	if err != nil {
		return errors.New("妙妙屋连接失败，请检查地址或网络")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errors.New("妙妙屋响应读取失败")
	}
	if resp.StatusCode != http.StatusOK {
		e := &APIError{Status: resp.StatusCode}
		var v struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Errors []ValidationError `json:"errors"`
		}
		if json.Unmarshal(raw, &v) == nil {
			e.Code = v.Error.Code
			// Field-specific messages are generated locally: a remote echo must not leak
			// the integration token, SS keys, UUID or Reality private key into the UI/log.
			for _, p := range v.Errors {
				field := validationFieldLabel(p.Field)
				e.Problems = append(e.Problems, ValidationError{NodeID: p.NodeID, Field: field, Message: "妙妙屋校验失败：" + field + "不符合共享约束"})
			}
		}
		return e
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("妙妙屋响应格式错误")
	}
	return nil
}

// validationFieldLabel maps a Custom 422 field path to the local label shown
// in the UI (the remote message itself is never shown).
func validationFieldLabel(name string) string {
	switch name {
	case "port":
		return "端口"
	case "protocol":
		return "协议"
	case "max_connections":
		return "连接上限"
	case "max_online_ips":
		return "在线 IP 上限"
	case "expires_at":
		return "有效期"
	case "name":
		return "名称"
	case "owner":
		return "用户"
	case "ss2022", "vless":
		return "协议凭据"
	}
	// landing-protocols-v1 reports e.g. "snell.version", "anytls.tls".
	for _, proto := range ExtraProtocols {
		if name == proto || strings.HasPrefix(name, proto+".") {
			return "协议凭据"
		}
	}
	return "配置"
}
func (c *Client) Shares(ctx context.Context) ([]Share, error) {
	var out struct {
		Shares *[]Share `json:"shares"`
	}
	err := c.request(ctx, http.MethodGet, "/shares", nil, &out)
	if err == nil && out.Shares == nil {
		err = errors.New("妙妙屋共享列表格式错误")
	}
	if err != nil {
		return nil, err
	}
	return *out.Shares, nil
}
func (c *Client) PutDesired(ctx context.Context, id int64, d Desired) error {
	var out struct {
		Revision int64 `json:"accepted_revision"`
	}
	err := c.request(ctx, http.MethodPut, fmt.Sprintf("/shares/%d/desired", id), d, &out)
	if err == nil && out.Revision != d.Revision {
		return errors.New("妙妙屋未确认期望状态版本")
	}
	return err
}
func (c *Client) Status(ctx context.Context, id int64) (Status, error) {
	var out Status
	err := c.request(ctx, http.MethodGet, fmt.Sprintf("/shares/%d/status", id), nil, &out)
	if err == nil && (out.CollectedAt < 0 || out.AppliedRevision < 0 || out.TrafficUsed < 0) {
		err = errors.New("妙妙屋节点状态格式错误")
	}
	// Custom reports an unknown core as stopped; keep booking counters anyway.
	if err == nil && strings.TrimSpace(out.Core.State) == "" {
		out.Core.State = "stopped"
	}
	if out.RealityGuard != nil && !out.RealityGuard.Valid() {
		out.RealityGuard = nil
	}
	return out, err
}
func (c *Client) PutHosts(ctx context.Context, hosts LandingHosts) error {
	return c.request(ctx, http.MethodPut, "/landing-hosts", hosts, nil)
}
