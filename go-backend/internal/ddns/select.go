package ddns

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Address sources of a record.
const (
	SourceAuto   = "auto"   // AAAA: first global IPv6 reported by the agent
	SourceCT     = "ct"     // AAAA: China Telecom 240e::/16
	SourceCU     = "cu"     // AAAA: China Unicom 2408::/16
	SourceCM     = "cm"     // AAAA: China Mobile 2409::/16
	SourcePrefix = "prefix" // AAAA: custom prefix (CIDR or hex prefix)
	SourcePanel  = "panel"  // A: IPv4 the panel sees on the agent connection
	SourceIface  = "iface"  // A: first public IPv4 on a NIC (agent report)
)

// MaxReportedAddrs caps one family of an agent report.
const MaxReportedAddrs = 16

var carrierPrefixes = map[string]netip.Prefix{
	SourceCT: netip.MustParsePrefix("240e::/16"),
	SourceCU: netip.MustParsePrefix("2408::/16"),
	SourceCM: netip.MustParsePrefix("2409::/16"),
}

// AgentAddrs is the address set an agent reported, canonical and ordered
// (addresses of the default-route interface first).
type AgentAddrs struct {
	V4 []string `json:"v4"`
	V6 []string `json:"v6"`
}

// Equal compares two reports including order (order decides 「自动」).
func (a AgentAddrs) Equal(b AgentAddrs) bool {
	return strings.Join(a.V4, ",") == strings.Join(b.V4, ",") && strings.Join(a.V6, ",") == strings.Join(b.V6, ",")
}

// PublicIPv6 is a global unicast IPv6 usable as a public entry.
func PublicIPv6(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.Is6() || ip.Is4In6() {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	for _, p := range nonPublicV6 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// PublicIPv4 is a globally routed unicast IPv4 (no RFC1918, CGNAT, test or
// benchmark ranges).
func PublicIPv4(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.Is4() {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return false
	}
	for _, p := range nonPublicV4 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicV6 = []netip.Prefix{
	netip.MustParsePrefix("2001:db8::/32"), // documentation
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
	netip.MustParsePrefix("100::/64"),      // discard
	netip.MustParsePrefix("2002::/16"),     // 6to4
}

var nonPublicV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// NormalizeReport canonicalizes an agent report: public addresses only,
// deduplicated, original order kept, at most MaxReportedAddrs per family.
func NormalizeReport(v4, v6 []string) AgentAddrs {
	out := AgentAddrs{V4: []string{}, V6: []string{}}
	seen := map[string]bool{}
	for _, raw := range v4 {
		ip, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !PublicIPv4(ip) || len(out.V4) >= MaxReportedAddrs {
			continue
		}
		key := ip.Unmap().String()
		if !seen[key] {
			seen[key] = true
			out.V4 = append(out.V4, key)
		}
	}
	for _, raw := range v6 {
		ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]"))
		if err != nil || !PublicIPv6(ip) || len(out.V6) >= MaxReportedAddrs {
			continue
		}
		key := ip.String()
		if !seen[key] {
			seen[key] = true
			out.V6 = append(out.V6, key)
		}
	}
	return out
}

var hexPrefixRe = regexp.MustCompile(`^[0-9a-f:]{2,39}$`)

// ValidateSource checks a record's type/source/prefix combination and
// returns the canonical prefix text.
func ValidateSource(recordType, source, prefix string) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	switch recordType {
	case "AAAA":
		switch source {
		case SourceAuto, SourceCT, SourceCU, SourceCM:
			return "", nil
		case SourcePrefix:
			if prefix == "" {
				return "", errors.New("自定义前缀不能为空")
			}
			if strings.Contains(prefix, "/") {
				p, err := netip.ParsePrefix(prefix)
				if err != nil || !p.Addr().Is6() {
					return "", fmt.Errorf("自定义前缀「%s」不是有效的 IPv6 前缀", prefix)
				}
				return p.Masked().String(), nil
			}
			if !hexPrefixRe.MatchString(prefix) {
				return "", fmt.Errorf("自定义前缀「%s」只能是 IPv6 前缀，例如 2408:820c 或 2408:820c::/32", prefix)
			}
			return prefix, nil
		}
		return "", fmt.Errorf("AAAA 记录的地址来源无效：%s", source)
	case "A":
		if source == SourcePanel || source == SourceIface {
			return "", nil
		}
		return "", fmt.Errorf("A 记录的地址来源无效：%s", source)
	}
	return "", fmt.Errorf("记录类型只能是 A 或 AAAA")
}

// NeedsAgentReport: sources that read the agent's interface addresses (an
// old agent cannot serve them).
func NeedsAgentReport(recordType, source string) bool {
	return !(recordType == "A" && source == SourcePanel)
}

// SelectAddress picks the address a record should point to. ok=false means
// nothing matches (the caller keeps the record as it is).
func SelectAddress(recordType, source, prefix string, addrs AgentAddrs, panelIP string) (string, bool) {
	if recordType == "A" {
		if source == SourcePanel {
			ip, err := netip.ParseAddr(strings.TrimSpace(panelIP))
			if err == nil && PublicIPv4(ip) {
				return ip.Unmap().String(), true
			}
			return "", false
		}
		if len(addrs.V4) > 0 {
			return addrs.V4[0], true
		}
		return "", false
	}
	for _, raw := range addrs.V6 {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		switch source {
		case SourceAuto:
			return raw, true
		case SourceCT, SourceCU, SourceCM:
			if carrierPrefixes[source].Contains(ip) {
				return raw, true
			}
		case SourcePrefix:
			if matchPrefix(prefix, ip) {
				return raw, true
			}
		}
	}
	return "", false
}

func matchPrefix(prefix string, ip netip.Addr) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return false
	}
	if strings.Contains(prefix, "/") {
		p, err := netip.ParsePrefix(prefix)
		return err == nil && p.Contains(ip)
	}
	return strings.HasPrefix(expandIPv6(ip), expandPrefix(prefix))
}

// expandIPv6 writes all 8 groups without zero compression so that hex
// prefixes like "2408:820c" compare group-wise.
func expandIPv6(ip netip.Addr) string {
	b := ip.As16()
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = fmt.Sprintf("%x", uint16(b[2*i])<<8|uint16(b[2*i+1]))
	}
	return strings.Join(parts, ":")
}

// expandPrefix drops leading zeros of each complete group ("2408:0820" →
// "2408:820"); a trailing partial group is kept as typed.
func expandPrefix(prefix string) string {
	prefix = strings.TrimSuffix(prefix, "::")
	groups := strings.Split(prefix, ":")
	for i := 0; i < len(groups)-1; i++ {
		g := strings.TrimLeft(groups[i], "0")
		if g == "" {
			g = "0"
		}
		groups[i] = g
	}
	return strings.Join(groups, ":")
}

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeDomain validates a record name: lower case, no trailing dot, at
// least two labels, no wildcard, not an IP literal.
func NormalizeDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if domain == "" {
		return "", errors.New("域名不能为空")
	}
	if len(domain) > 253 {
		return "", fmt.Errorf("域名「%s」太长", raw)
	}
	if _, err := netip.ParseAddr(strings.Trim(domain, "[]")); err == nil {
		return "", fmt.Errorf("「%s」是 IP 地址，请填写域名", raw)
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("域名「%s」无效", raw)
	}
	for _, label := range labels {
		if !labelRe.MatchString(label) {
			return "", fmt.Errorf("域名「%s」无效（只能包含字母、数字和 -，不支持通配符）", raw)
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return "", fmt.Errorf("域名「%s」无效", raw)
	}
	return domain, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// protectedLabels are names of the panels themselves (ours = flvxx/flvxxht,
// another panel = flvx/flvxht) and of miaomiaowuX; FLVX never writes them.
var protectedLabels = map[string]bool{"flvx": true, "flvxx": true, "flvxht": true, "flvxxht": true}

// ProtectedDomain reports names FLVX must never touch: panel / agent names,
// mmwx*, the zone apex and any configured panel host.
func ProtectedDomain(domain, zone string, panelHosts []string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	zone = strings.ToLower(strings.TrimSuffix(zone, "."))
	if zone != "" && domain == zone {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(domain, "."+zone), ".") {
		if protectedLabels[label] || strings.HasPrefix(label, "mmwx") {
			return true
		}
	}
	for _, host := range panelHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" && host == domain {
			return true
		}
	}
	return false
}

// Backoff is the retry delay after the n-th consecutive failure: 30 s
// doubling, capped at 10 minutes.
func Backoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	d := 30 * time.Second
	for i := 1; i < failures && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

// DriftCheckEvery is the slow GET-only check of a synced record (someone
// edited it in the dashboard).
const DriftCheckEvery = 60 * time.Minute
