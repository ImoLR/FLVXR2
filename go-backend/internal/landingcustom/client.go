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
	ID             string  `json:"id"`
	Owner          Owner   `json:"owner"`
	Name           string  `json:"name"`
	Protocol       string  `json:"protocol"`
	Port           int     `json:"port"`
	Enabled        bool    `json:"enabled"`
	MaxConnections int     `json:"max_connections"`
	MaxOnlineIPs   int     `json:"max_online_ips"`
	ExpiresAt      *int64  `json:"expires_at"`
	SS2022         *SS2022 `json:"ss2022,omitempty"`
	VLESS          *VLESS  `json:"vless,omitempty"`
}
type Desired struct {
	Revision int64               `json:"revision"`
	Nodes    []Node              `json:"nodes"`
	Egress   *landingegress.Plan `json:"egress,omitempty"`
}
type NodeStatus struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Error       string `json:"error"`
	UpBytes     int64  `json:"up_bytes"`
	DownBytes   int64  `json:"down_bytes"`
	Connections int    `json:"connections"`
}
type Status struct {
	AppliedRevision int64         `json:"applied_revision"`
	Core            Core          `json:"core"`
	Nodes           []NodeStatus  `json:"nodes"`
	TrafficUsed     int64         `json:"share_traffic_used_bytes"`
	CollectedAt     int64         `json:"collected_at"`
	Egress          *EgressStatus `json:"egress,omitempty"`
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
				field := "配置"
				switch p.Field {
				case "port":
					field = "端口"
				case "protocol":
					field = "协议"
				case "max_connections":
					field = "连接上限"
				case "max_online_ips":
					field = "在线 IP 上限"
				case "expires_at":
					field = "有效期"
				case "name":
					field = "名称"
				case "owner":
					field = "用户"
				case "ss2022", "vless":
					field = "协议凭据"
				}
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
	return out, err
}
func (c *Client) PutHosts(ctx context.Context, hosts LandingHosts) error {
	return c.request(ctx, http.MethodPut, "/landing-hosts", hosts, nil)
}
