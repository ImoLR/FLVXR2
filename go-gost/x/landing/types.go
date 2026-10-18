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

const CoreVersion = "v1.4.59"
const SS2022Method = "2022-blake3-aes-128-gcm"

// These hashes were verified against both assets and checksums.txt in the pinned release.
var CoreSHA256 = map[string]string{
	"amd64": "ae4b83f0cf77c4aa73857cb1de773e7ec89b1bc5ded272a677b46d417cf40212",
	"arm64": "aa61a9d865c7ccb7d68717abd8d8832a7de4ee0ee73670271097afb69461a2e4",
}

type Desired struct {
	Revision       int64  `json:"revision"`
	Enabled        bool   `json:"enabled"`
	MaxConnections int64  `json:"max_connections"`
	CoreMemoryMB   int    `json:"core_memory_mb"`
	CoreCPUPercent int    `json:"core_cpu_percent"`
	Nodes          []Node `json:"nodes"`
}

type Node struct {
	ID             int64   `json:"id"`
	InboundTag     string  `json:"inbound_tag"`
	Protocol       string  `json:"protocol"`
	Port           int     `json:"port"`
	Enabled        bool    `json:"enabled"`
	MaxConnections int64   `json:"max_connections"`
	MaxOnlineIPs   int     `json:"max_online_ips"`
	SS2022         *SS2022 `json:"ss2022,omitempty"`
	VLESS          *VLESS  `json:"vless,omitempty"`
}

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
	ID          int64  `json:"id"`
	InboundTag  string `json:"inbound_tag"`
	Epoch       string `json:"epoch"`
	UpBytes     int64  `json:"up_bytes"`
	DownBytes   int64  `json:"down_bytes"`
	Connections int64  `json:"connections"`
}
type Status struct {
	Revision    int64     `json:"revision"`
	Epoch       string    `json:"epoch"`
	CoreVersion string    `json:"core_version"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	Nodes       []Counter `json:"nodes"`
}

func (d *Desired) validate() error {
	if d.Revision <= 0 || d.MaxConnections < 0 {
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
	ids, ports := map[int64]bool{}, map[int]bool{}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.ID <= 0 || n.Port < 1024 || n.Port > 65535 || ids[n.ID] || ports[n.Port] || n.MaxConnections < 0 || n.MaxOnlineIPs < 0 {
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
		if _, err := n.inbound(); err != nil {
			return err
		}
	}
	return nil
}

func (n Node) inbound() (map[string]any, error) {
	inbound := map[string]any{"tag": n.InboundTag, "listen": "0.0.0.0", "port": n.Port, "sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true}}
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

func baseConfig(apiPort int) map[string]any {
	return map[string]any{
		"log":   map[string]any{"loglevel": "warning", "access": "none"},
		"api":   map[string]any{"tag": "flvxl-api", "listen": net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)), "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{}, "policy": map[string]any{"system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true}},
		"inbounds":  []any{},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}}, map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch", "rules": []any{
			// The separate loopback API listener needs no routing exception.
			map[string]any{"type": "field", "ip": blockedDestinations, "outboundTag": "block"},
		}},
	}
}

func canonical(v any) string { b, _ := json.Marshal(v); return string(b) }
