package landingegress

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BlockedCIDRs is the private/loopback/link-local/metadata list the landing
// safety rule blocks; outbound upstreams must not point into it either.
var BlockedCIDRs = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8"}

var blockedPrefixes = func() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(BlockedCIDRs))
	for _, s := range BlockedCIDRs {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsBlockedAddr reports addresses the landing must never connect to.
func IsBlockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var (
	hostnameRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62})(\.[a-z0-9]([a-z0-9-]{0,62}))*\.?$`)
	geoCodeRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9!_.-]{0,63}$`)
	geoAttrRe     = regexp.MustCompile(`^[a-z0-9!_-]{1,32}$`)
	domainValueRe = regexp.MustCompile(`^[a-z0-9_*.-]{1,253}$`)
	shortIDRe     = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
	ruleIDRe      = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	warpLicenseRe = regexp.MustCompile(`^[A-Za-z0-9]{8}-[A-Za-z0-9]{8}-[A-Za-z0-9]{8}$`)
	uuidRe        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

var ss2022Methods = map[string]int{"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32}
var ssAEADMethods = map[string]bool{"aes-128-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true}
var realityFingerprints = map[string]bool{"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true, "edge": true, "random": true, "randomized": true}
var ruleProtocols = map[string]bool{"http": true, "tls": true, "quic": true, "bittorrent": true}

// InboundProtocolKeys are the landing node protocols the 「节点协议」 matcher
// (plan 149) accepts, in display order. SSFamily is the Shadowsocks part of
// it: every SS variant the panel offers (only SS2022 today).
var InboundProtocolKeys = []string{"ss2022", "vless_reality_vision", "anytls", "snell", "mieru"}

var SSFamily = []string{"ss2022"}

var inboundProtocolSet = func() map[string]bool {
	out := map[string]bool{}
	for _, k := range InboundProtocolKeys {
		out[k] = true
	}
	return out
}()

// RuleAppliesTo reports whether a rule covers a node of this protocol (""
// = unknown: covered).
func RuleAppliesTo(r Rule, protocol string) bool {
	if len(r.InboundProtocols) == 0 || protocol == "" {
		return true
	}
	for _, p := range r.InboundProtocols {
		if p == protocol {
			return true
		}
	}
	return false
}

// SSMethods lists accepted Shadowsocks methods (2022 + AEAD).
func SSMethods() []string {
	out := []string{}
	for m := range ss2022Methods {
		out = append(out, m)
	}
	for m := range ssAEADMethods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func validName(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > max {
		return s, false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return s, false
		}
	}
	return s, true
}

// NormalizeHost lowercases a host and strips IPv6 brackets.
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap().String()
	}
	return h
}

// ValidHost accepts public IP literals and dotted hostnames.
func ValidHost(h string) bool {
	if a, err := netip.ParseAddr(h); err == nil {
		return !IsBlockedAddr(a)
	}
	return len(h) <= 253 && hostnameRe.MatchString(h) && strings.Contains(strings.TrimSuffix(h, "."), ".")
}

func decodeKey(s string, raw bool, n int) bool {
	var b []byte
	var err error
	if raw {
		b, err = base64.RawURLEncoding.Strict().DecodeString(s)
	} else {
		b, err = base64.StdEncoding.Strict().DecodeString(s)
	}
	return err == nil && len(b) == n
}

func forcedConflict(ipVersion, host string) bool {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return (ipVersion == "force_ipv4" && !a.Is4()) || (ipVersion == "force_ipv6" && a.Is4())
}

// NormalizeOutbound validates a template in place and clears fields that do
// not belong to its type.
func NormalizeOutbound(o *Outbound) error {
	name, ok := validName(o.Name, 64)
	if !ok {
		return errors.New("出站名称需为 1–64 个字符")
	}
	o.Name = name
	o.Remark = strings.TrimSpace(o.Remark)
	if utf8.RuneCountInString(o.Remark) > 200 {
		return errors.New("备注最多 200 个字符")
	}
	if o.IPVersion == "" {
		o.IPVersion = "system"
	}
	if !IPVersions[o.IPVersion] {
		return errors.New("IP 版本无效")
	}
	s := o.Settings
	switch o.Type {
	case TypeDirect:
		o.Settings = Settings{}
		return nil
	case TypeWarp:
		if s.WarpMode == "" {
			s.WarpMode = "auto"
		}
		s.WarpLicense = strings.TrimSpace(s.WarpLicense)
		if s.WarpLicense != "" && !warpLicenseRe.MatchString(s.WarpLicense) {
			return errors.New("WARP+ 许可证格式应为 xxxxxxxx-xxxxxxxx-xxxxxxxx")
		}
		switch s.WarpMode {
		case "auto":
			o.Settings = Settings{WarpMode: "auto", WarpLicense: s.WarpLicense}
			return nil
		case "manual":
		default:
			return errors.New("WARP 模式无效")
		}
		clean := Settings{WarpMode: "manual", PrivateKey: strings.TrimSpace(s.PrivateKey), PeerPublicKey: strings.TrimSpace(s.PeerPublicKey), Endpoint: strings.TrimSpace(s.Endpoint), Reserved: s.Reserved, MTU: s.MTU}
		if !decodeKey(clean.PrivateKey, false, 32) || !decodeKey(clean.PeerPublicKey, false, 32) {
			return errors.New("WARP 私钥或对端公钥无效（需为 32 字节 Base64）")
		}
		v4, v6 := false, false
		if a := strings.TrimSpace(s.AddressV4); a != "" {
			p, err := parseIPOrCIDR(a)
			if err != nil || !p.Addr().Is4() {
				return errors.New("WARP IPv4 地址无效")
			}
			clean.AddressV4, v4 = p.Addr().String(), true
		}
		if a := strings.TrimSpace(s.AddressV6); a != "" {
			p, err := parseIPOrCIDR(a)
			if err != nil || p.Addr().Is4() {
				return errors.New("WARP IPv6 地址无效")
			}
			clean.AddressV6, v6 = p.Addr().String(), true
		}
		if !v4 && !v6 {
			return errors.New("请至少填写一个 WARP 接口地址")
		}
		if (o.IPVersion == "force_ipv4" && !v4) || (o.IPVersion == "force_ipv6" && !v6) {
			return errors.New("强制 IP 版本需要对应的 WARP 接口地址")
		}
		if len(clean.Reserved) != 0 && len(clean.Reserved) != 3 {
			return errors.New("reserved 需为 3 个 0–255 的数字")
		}
		for _, b := range clean.Reserved {
			if b < 0 || b > 255 {
				return errors.New("reserved 需为 3 个 0–255 的数字")
			}
		}
		if clean.MTU != 0 && (clean.MTU < 1280 || clean.MTU > 1500) {
			return errors.New("MTU 需在 1280–1500 之间")
		}
		if clean.Endpoint != "" {
			host, port, err := splitHostPort(clean.Endpoint)
			if err != nil || !ValidHost(host) {
				return errors.New("WARP 端点需为公网 host:port")
			}
			clean.Endpoint = joinHostPort(host, port)
		}
		o.Settings = clean
		return nil
	case TypeProxy:
		clean := Settings{Protocol: s.Protocol, Server: NormalizeHost(s.Server), Port: s.Port}
		if !ValidHost(clean.Server) || clean.Port < 1 || clean.Port > 65535 {
			return errors.New("上游地址无效：不能使用内网、回环或保留地址")
		}
		if forcedConflict(o.IPVersion, clean.Server) {
			return errors.New("上游 IP 与强制 IP 版本不一致")
		}
		switch s.Protocol {
		case "socks", "http":
			clean.Username, clean.Password = s.Username, s.Password
			if len(clean.Username) > 255 || len(clean.Password) > 255 || strings.ContainsAny(clean.Username+clean.Password, "\r\n\x00") {
				return errors.New("代理用户名或密码无效")
			}
		case "shadowsocks":
			clean.Method, clean.Password = s.Method, strings.TrimSpace(s.Password)
			if size, ok := ss2022Methods[clean.Method]; ok {
				parts := strings.Split(clean.Password, ":")
				if len(parts) > 2 {
					return errors.New("Shadowsocks 2022 密码无效")
				}
				for _, p := range parts {
					if !decodeKey(p, false, size) {
						return fmt.Errorf("Shadowsocks 2022 密钥需为 %d 字节 Base64", size)
					}
				}
			} else if !ssAEADMethods[clean.Method] {
				return errors.New("Shadowsocks 加密方式不受支持")
			} else if clean.Password == "" || len(clean.Password) > 256 || strings.ContainsAny(clean.Password, "\r\n\x00") {
				return errors.New("Shadowsocks 密码无效")
			}
		case "vless":
			clean.UUID, clean.Flow = strings.ToLower(strings.TrimSpace(s.UUID)), s.Flow
			if !uuidRe.MatchString(clean.UUID) {
				return errors.New("VLESS UUID 无效")
			}
			if clean.Flow != "" && clean.Flow != "xtls-rprx-vision" {
				return errors.New("VLESS flow 只支持 xtls-rprx-vision")
			}
			clean.RealityServerName = strings.ToLower(strings.TrimSpace(s.RealityServerName))
			clean.RealityPublicKey = strings.TrimSpace(s.RealityPublicKey)
			clean.RealityShortID = strings.ToLower(strings.TrimSpace(s.RealityShortID))
			clean.RealityFingerprint = strings.TrimSpace(s.RealityFingerprint)
			if clean.RealityFingerprint == "" {
				clean.RealityFingerprint = "chrome"
			}
			if !hostnameRe.MatchString(clean.RealityServerName) || !decodeKey(clean.RealityPublicKey, true, 32) || !shortIDRe.MatchString(clean.RealityShortID) || len(clean.RealityShortID)%2 != 0 || !realityFingerprints[clean.RealityFingerprint] {
				return errors.New("Reality 参数无效（SNI、公钥 pbk、shortId、指纹）")
			}
		default:
			return errors.New("代理协议只支持 SOCKS5、HTTP、Shadowsocks、VLESS Reality")
		}
		o.Settings = clean
		return nil
	case TypeLandingNode:
		if s.LandingNodeID <= 0 || s.ViaForwardID < 0 {
			return errors.New("请选择上游 FLVX 落地节点")
		}
		o.Settings = Settings{LandingNodeID: s.LandingNodeID, ViaForwardID: s.ViaForwardID}
		return nil
	}
	return errors.New("出站类型无效")
}

func parseIPOrCIDR(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func splitHostPort(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 {
		return "", 0, errors.New("missing port")
	}
	host, portText := NormalizeHost(s[:i]), s[i+1:]
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("invalid port")
	}
	return host, port, nil
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

// NormalizePorts canonicalizes "443, 8000-9000" to "443,8000-9000".
func NormalizePorts(spec string) (string, error) {
	spec = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(spec), " ", ""), "，", ",")
	if spec == "" {
		return "", nil
	}
	parts := strings.Split(spec, ",")
	if len(parts) > 64 {
		return "", errors.New("端口最多 64 段")
	}
	for _, part := range parts {
		lo, hi, ranged := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 || a > 65535 {
			return "", fmt.Errorf("端口 %q 无效", part)
		}
		if ranged {
			b, err := strconv.Atoi(hi)
			if err != nil || b < a || b > 65535 {
				return "", fmt.Errorf("端口范围 %q 无效", part)
			}
		}
	}
	return strings.Join(parts, ","), nil
}

func normalizeDomain(v string) (string, error) {
	v = strings.TrimSpace(v)
	kind, value, ok := strings.Cut(v, ":")
	if !ok {
		kind, value = "domain", v
	}
	switch kind {
	case "geosite":
		value = strings.ToLower(value)
		code, attr, hasAttr := strings.Cut(value, "@")
		if !geoCodeRe.MatchString(code) || (hasAttr && !geoAttrRe.MatchString(attr)) {
			return "", fmt.Errorf("geosite %q 无效", v)
		}
		if known, _, _ := GeoEntry("geosite:" + code); !known {
			return "", fmt.Errorf("geosite:%s 不存在（geo 数据版本 %s）", code, GeoRelease)
		}
	case "domain", "full":
		value = strings.ToLower(strings.TrimSuffix(value, "."))
		if kind == "domain" {
			// "*.example.com" means the domain and its subdomains, which is
			// exactly what the Core's domain: matcher does.
			value = strings.TrimPrefix(value, "*.")
		}
		if !domainValueRe.MatchString(value) || strings.Contains(value, "*") {
			return "", fmt.Errorf("域名 %q 无效（通配请写成 *.example.com 或 domain:example.com）", v)
		}
	case "keyword":
		value = strings.ToLower(value)
		if value == "" || len(value) > 128 || strings.ContainsAny(value, " \t\r\n\"") {
			return "", fmt.Errorf("关键字 %q 无效", v)
		}
	case "regexp":
		if value == "" || len(value) > 256 {
			return "", errors.New("正则最长 256 个字符")
		}
		if _, err := regexp.Compile(value); err != nil {
			return "", fmt.Errorf("正则 %q 无效", v)
		}
	default:
		return "", fmt.Errorf("域名条目 %q 只支持 geosite:/domain:/full:/keyword:/regexp:", v)
	}
	return kind + ":" + value, nil
}

func normalizeIP(v string) (string, error) {
	v = strings.TrimSpace(v)
	if code, ok := strings.CutPrefix(strings.ToLower(v), "geoip:"); ok {
		bare := strings.TrimPrefix(code, "!")
		if !geoCodeRe.MatchString(bare) || strings.Contains(bare, "!") {
			return "", fmt.Errorf("geoip %q 无效", v)
		}
		if known, _, _ := GeoEntry("geoip:" + bare); !known {
			return "", fmt.Errorf("geoip:%s 不存在（geo 数据版本 %s）", bare, GeoRelease)
		}
		return "geoip:" + code, nil
	}
	p, err := parseIPOrCIDR(v)
	if err != nil {
		return "", fmt.Errorf("IP/CIDR %q 无效", v)
	}
	if p.Bits() == p.Addr().BitLen() {
		return p.Addr().String(), nil
	}
	return p.String(), nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// NewRuleID returns a short random identifier for UI keys.
func NewRuleID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "r-" + hex.EncodeToString(b)
}

// ValidAction reports built-in actions and existing template references.
func ValidAction(action string, outbounds map[int64]bool) bool {
	if action == ActionDirect || action == ActionBlock {
		return true
	}
	id, ok := ActionOutbound(action)
	return ok && outbounds[id]
}

// ActionOutbound parses "outbound:<id>".
func ActionOutbound(action string) (int64, bool) {
	text, ok := strings.CutPrefix(action, "outbound:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil && id > 0
}

// RuleItems counts matcher entries towards the item limits.
func RuleItems(r Rule) int {
	n := len(r.Domains) + len(r.IPs) + len(r.SourceIPs) + len(r.Protocols) + len(r.LandingNodeIDs)
	if r.Ports != "" {
		n += strings.Count(r.Ports, ",") + 1
	}
	if r.SourcePorts != "" {
		n += strings.Count(r.SourcePorts, ",") + 1
	}
	return n
}

// RuleGeoBytes estimates the Core API payload of one rule's geo categories.
func RuleGeoBytes(r Rule) int {
	total := 0
	for _, list := range [][]string{r.Domains, r.IPs, r.SourceIPs} {
		for _, e := range list {
			_, b, _ := GeoEntry(e)
			total += b
		}
	}
	return total
}

// NormalizeProfile validates a routing profile in place. outbounds lists
// existing template IDs.
func NormalizeProfile(p *Profile, outbounds map[int64]bool) error {
	name, ok := validName(p.Name, 64)
	if !ok {
		return errors.New("方案名称需为 1–64 个字符")
	}
	p.Name = name
	if p.DefaultAction == "" {
		p.DefaultAction = ActionDirect
	}
	if !ValidAction(p.DefaultAction, outbounds) {
		return errors.New("默认出站无效或已删除")
	}
	if len(p.Rules) > MaxProfileRules {
		return fmt.Errorf("每个方案最多 %d 条规则", MaxProfileRules)
	}
	items := 0
	ids := map[string]bool{}
	for i := range p.Rules {
		r := &p.Rules[i]
		label := fmt.Sprintf("第 %d 条规则", i+1)
		if r.ID == "" {
			r.ID = NewRuleID()
		}
		if !ruleIDRe.MatchString(r.ID) || ids[r.ID] {
			return fmt.Errorf("%s 标识无效", label)
		}
		ids[r.ID] = true
		if strings.TrimSpace(r.Name) == "" {
			r.Name = fmt.Sprintf("规则 %d", i+1)
		}
		if n, ok := validName(r.Name, 64); !ok {
			return fmt.Errorf("%s 名称需为 1–64 个字符", label)
		} else {
			r.Name = n
		}
		var err error
		for j, d := range r.Domains {
			if r.Domains[j], err = normalizeDomain(d); err != nil {
				return fmt.Errorf("%s：%w", label, err)
			}
		}
		for j, v := range r.IPs {
			if r.IPs[j], err = normalizeIP(v); err != nil {
				return fmt.Errorf("%s：%w", label, err)
			}
		}
		for j, v := range r.SourceIPs {
			if r.SourceIPs[j], err = normalizeIP(v); err != nil {
				return fmt.Errorf("%s 来源 IP：%w", label, err)
			}
		}
		r.Domains, r.IPs, r.SourceIPs = dedupe(r.Domains), dedupe(r.IPs), dedupe(r.SourceIPs)
		if r.Ports, err = NormalizePorts(r.Ports); err != nil {
			return fmt.Errorf("%s 目标端口：%w", label, err)
		}
		if r.SourcePorts, err = NormalizePorts(r.SourcePorts); err != nil {
			return fmt.Errorf("%s 来源端口：%w", label, err)
		}
		switch r.Network {
		case "", "tcp", "udp":
		case "tcp,udp":
			r.Network = ""
		default:
			return fmt.Errorf("%s 网络只能是 TCP、UDP 或全部", label)
		}
		protocols := dedupe(r.Protocols)
		for _, proto := range protocols {
			if !ruleProtocols[proto] {
				return fmt.Errorf("%s 协议只支持 http、tls、quic、bittorrent", label)
			}
		}
		sort.Strings(protocols)
		r.Protocols = protocols
		nodes := []int64{}
		seen := map[int64]bool{}
		for _, id := range r.LandingNodeIDs {
			if id <= 0 {
				return fmt.Errorf("%s 适用落地节点无效", label)
			}
			if !seen[id] {
				seen[id] = true
				nodes = append(nodes, id)
			}
		}
		sort.Slice(nodes, func(a, b int) bool { return nodes[a] < nodes[b] })
		r.LandingNodeIDs = nodes
		if len(r.InboundProtocols) > 0 {
			inbound := dedupe(r.InboundProtocols)
			for _, proto := range inbound {
				if !inboundProtocolSet[proto] {
					return fmt.Errorf("%s 节点协议无效", label)
				}
			}
			sort.Strings(inbound)
			r.InboundProtocols = inbound
			if len(inbound) == 0 {
				r.InboundProtocols = nil
			}
		} else {
			r.InboundProtocols = nil
		}
		if !ValidAction(r.Action, outbounds) {
			return fmt.Errorf("%s 的出站无效或已删除", label)
		}
		if len(r.Domains)+len(r.IPs)+len(r.SourceIPs)+len(r.Protocols)+len(r.LandingNodeIDs)+len(r.InboundProtocols) == 0 && r.Ports == "" && r.SourcePorts == "" && r.Network == "" {
			return fmt.Errorf("%s 至少需要一个匹配条件", label)
		}
		if RuleGeoBytes(*r) > MaxRuleGeoBytes {
			return fmt.Errorf("%s 引用的 geo 分类过大（超过 2.5 MiB），请改用更小的分类，例如 geosite:category-ads", label)
		}
		items += RuleItems(*r)
	}
	if items > MaxProfileItems {
		return fmt.Errorf("每个方案的匹配条目合计最多 %d 条", MaxProfileItems)
	}
	return nil
}

// NormalizeWhitelist canonicalizes the admin source whitelist.
func NormalizeWhitelist(in []string) ([]string, error) {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, err := parseIPOrCIDR(v)
		if err != nil {
			return nil, fmt.Errorf("来源白名单 %q 不是 IP 或 CIDR", v)
		}
		if p.Bits() == 0 {
			return nil, errors.New("来源白名单不能包含 0.0.0.0/0 或 ::/0")
		}
		out = append(out, p.String())
	}
	out = dedupe(out)
	if len(out) > MaxWhitelistEntries {
		return nil, fmt.Errorf("来源白名单最多 %d 条", MaxWhitelistEntries)
	}
	sort.Strings(out)
	return out, nil
}
