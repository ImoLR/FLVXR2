package repo

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// MaxNodeExtraIPs caps the 附加 IP list of one node.
const MaxNodeExtraIPs = 16

func splitNodeIPList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if json.Unmarshal([]byte(raw), &list) == nil {
			return list
		}
	}
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '、', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
}

func parseLiteralIP(token string) net.IP {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "[") && strings.HasSuffix(token, "]") {
		token = token[1 : len(token)-1]
	}
	if token == "" || len(token) > 45 {
		return nil
	}
	return net.ParseIP(token)
}

// nodeAddressKey compares addresses independent of brackets, case and IPv6
// spelling. Hostnames compare case-insensitively.
func nodeAddressKey(address string) string {
	address = strings.TrimSpace(address)
	if ip := parseLiteralIP(address); ip != nil {
		return ip.String()
	}
	return strings.ToLower(strings.Trim(address, "[]"))
}

// ParseNodeExtraIPs reads a stored extra_ips value: the canonical JSON array or
// a legacy comma/space/newline separated list. Non-IP tokens are skipped.
func ParseNodeExtraIPs(raw string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, token := range splitNodeIPList(raw) {
		ip := parseLiteralIP(token)
		if ip == nil {
			continue
		}
		key := ip.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// NormalizeNodeExtraIPs validates the 附加 IP input of a node save and returns
// the canonical JSON array text, or "" when the list is empty.
func NormalizeNodeExtraIPs(raw string, primaries ...string) (string, error) {
	primary := make(map[string]struct{}, len(primaries))
	for _, address := range primaries {
		if strings.TrimSpace(address) != "" {
			primary[nodeAddressKey(address)] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, token := range splitNodeIPList(raw) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		ip := parseLiteralIP(token)
		if ip == nil {
			return "", fmt.Errorf("附加 IP「%s」不是有效的 IPv4 或 IPv6 地址", token)
		}
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
			return "", fmt.Errorf("附加 IP「%s」不能是未指定、回环、链路本地或组播地址", token)
		}
		key := ip.String()
		if _, ok := primary[key]; ok {
			return "", fmt.Errorf("附加 IP「%s」与节点主 IP 重复", token)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) > MaxNodeExtraIPs {
		return "", fmt.Errorf("附加 IP 最多 %d 个", MaxNodeExtraIPs)
	}
	if len(out) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
