package landing

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// EgressVersion is the outbound/routing plan schema shared with the panel and
// the mmwx Helper (contract landing-egress-v1).
const EgressVersion = 1

const (
	maxEgressOutbounds = 8
	maxEgressRules     = 128
	maxEgressItems     = 4000
	maxEgressExempt    = 1024
)

// Egress is a server-level plan compiled by the panel. Every rule is scoped by
// inbound tags; nodes without rules use the Core default outbound ("direct").
type Egress struct {
	Version          int              `json:"version"`
	AllowCoreRestart bool             `json:"allow_core_restart,omitempty"`
	Outbounds        []EgressOutbound `json:"outbounds"`
	Rules            []EgressRule     `json:"rules"`
	ExemptSources    []string         `json:"exempt_sources,omitempty"`
}

type EgressOutbound struct {
	Tag       string           `json:"tag"`
	Type      string           `json:"type"`
	IPVersion string           `json:"ip_version"`
	Server    string           `json:"server,omitempty"`
	Port      int              `json:"port,omitempty"`
	Username  string           `json:"username,omitempty"`
	Password  string           `json:"password,omitempty"`
	Method    string           `json:"method,omitempty"`
	UUID      string           `json:"uuid,omitempty"`
	Flow      string           `json:"flow,omitempty"`
	Reality   *EgressReality   `json:"reality,omitempty"`
	WireGuard *EgressWireGuard `json:"wireguard,omitempty"`
}

type EgressReality struct {
	ServerName  string `json:"server_name"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type EgressWireGuard struct {
	PrivateKey    string `json:"private_key"`
	PeerPublicKey string `json:"peer_public_key"`
	Endpoint      string `json:"endpoint,omitempty"`
	// Registration-provided endpoints; with Endpoint empty the agent picks the
	// family the server can reach.
	EndpointV4 string `json:"endpoint_v4,omitempty"`
	EndpointV6 string `json:"endpoint_v6,omitempty"`
	AddressV4  string `json:"address_v4,omitempty"`
	AddressV6  string `json:"address_v6,omitempty"`
	Reserved   []int  `json:"reserved,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
}

type EgressRule struct {
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
}

func (e *Egress) active() bool { return e != nil && (len(e.Outbounds) > 0 || len(e.Rules) > 0) }

var (
	outboundTagRe = regexp.MustCompile(`^flvxo-[a-z0-9]{1,32}$`)
	geoCodeRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9!_.-]{0,63}$`)
	geoAttrRe     = regexp.MustCompile(`^[a-z0-9!_-]{1,32}$`)
	domainValueRe = regexp.MustCompile(`^[a-z0-9_*.-]{1,253}$`)
	hostnameRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62})(\.[a-z0-9]([a-z0-9-]{0,62}))*\.?$`)
	shortIDRe     = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
)

var egressIPVersions = map[string]bool{"system": true, "prefer_ipv4": true, "prefer_ipv6": true, "force_ipv4": true, "force_ipv6": true}
var egressProtocols = map[string]bool{"http": true, "tls": true, "quic": true, "bittorrent": true}
var ss2022Methods = map[string]int{"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32}
var ssAEADMethods = map[string]bool{"aes-128-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true}
var realityFingerprints = map[string]bool{"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true, "edge": true, "random": true, "randomized": true}

// blockedPrefixes mirrors blockedDestinations for address checks in Go.
var blockedPrefixes = func() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(blockedDestinations))
	for _, s := range blockedDestinations {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// isBlockedAddr reports private, loopback, link-local, CGNAT, benchmark,
// multicast and reserved ranges (plus IPv4-mapped forms of them).
func isBlockedAddr(a netip.Addr) bool {
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

func validHost(h string) bool {
	if a, err := netip.ParseAddr(h); err == nil {
		return !isBlockedAddr(a)
	}
	return len(h) <= 253 && hostnameRe.MatchString(h) && strings.Contains(strings.TrimSuffix(h, "."), ".")
}

func validPortSpec(spec string) error {
	if spec == "" {
		return nil
	}
	parts := strings.Split(spec, ",")
	if len(parts) > 64 {
		return fmt.Errorf("too many port ranges")
	}
	for _, part := range parts {
		lo, hi, found := strings.Cut(strings.TrimSpace(part), "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 || a > 65535 {
			return fmt.Errorf("invalid port %q", part)
		}
		if found {
			b, err := strconv.Atoi(hi)
			if err != nil || b < a || b > 65535 {
				return fmt.Errorf("invalid port range %q", part)
			}
		}
	}
	return nil
}

func validDomainEntry(v string) error {
	kind, value, ok := strings.Cut(v, ":")
	if !ok || value == "" {
		return fmt.Errorf("domain entry %q needs a prefix", v)
	}
	switch kind {
	case "geosite":
		code, attr, hasAttr := strings.Cut(value, "@")
		if !geoCodeRe.MatchString(code) || (hasAttr && !geoAttrRe.MatchString(attr)) {
			return fmt.Errorf("invalid geosite %q", v)
		}
	case "domain", "full":
		if !domainValueRe.MatchString(value) {
			return fmt.Errorf("invalid domain %q", v)
		}
	case "keyword":
		if len(value) > 128 || strings.ContainsAny(value, " \t\r\n\"") {
			return fmt.Errorf("invalid keyword %q", v)
		}
	case "regexp":
		if len(value) > 256 {
			return fmt.Errorf("regexp too long")
		}
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("invalid regexp %q", v)
		}
	default:
		return fmt.Errorf("unsupported domain prefix %q", kind)
	}
	return nil
}

func validIPEntry(v string, allowReverse bool) error {
	if code, ok := strings.CutPrefix(v, "geoip:"); ok {
		if strings.HasPrefix(code, "!") {
			if !allowReverse {
				return fmt.Errorf("reverse geoip not allowed here")
			}
			code = code[1:]
		}
		if !geoCodeRe.MatchString(code) || strings.Contains(code, "!") {
			return fmt.Errorf("invalid geoip %q", v)
		}
		return nil
	}
	if _, err := parsePrefix(v); err != nil {
		return err
	}
	return nil
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

func (o EgressOutbound) validate() error {
	if !outboundTagRe.MatchString(o.Tag) {
		return fmt.Errorf("invalid outbound tag")
	}
	if !egressIPVersions[o.IPVersion] {
		return fmt.Errorf("invalid ip_version for %s", o.Tag)
	}
	proxy := func() error {
		if !validHost(o.Server) || o.Port < 1 || o.Port > 65535 {
			return fmt.Errorf("invalid upstream address for %s", o.Tag)
		}
		if a, err := netip.ParseAddr(o.Server); err == nil {
			if (o.IPVersion == "force_ipv4" && !a.Unmap().Is4()) || (o.IPVersion == "force_ipv6" && a.Unmap().Is4()) {
				return fmt.Errorf("upstream literal of %s conflicts with forced IP version", o.Tag)
			}
		}
		return nil
	}
	switch o.Type {
	case "direct":
		return nil
	case "socks", "http":
		if err := proxy(); err != nil {
			return err
		}
		if len(o.Username) > 255 || len(o.Password) > 255 || strings.ContainsAny(o.Username+o.Password, "\r\n\x00") {
			return fmt.Errorf("invalid proxy credentials for %s", o.Tag)
		}
		return nil
	case "shadowsocks":
		if err := proxy(); err != nil {
			return err
		}
		if size, ok := ss2022Methods[o.Method]; ok {
			parts := strings.Split(o.Password, ":")
			if len(parts) > 2 {
				return fmt.Errorf("invalid SS2022 password for %s", o.Tag)
			}
			for _, p := range parts {
				if !decodeKey(p, false, size) {
					return fmt.Errorf("invalid SS2022 key for %s", o.Tag)
				}
			}
			return nil
		}
		if !ssAEADMethods[o.Method] || o.Password == "" || len(o.Password) > 256 || strings.ContainsAny(o.Password, "\r\n\x00") {
			return fmt.Errorf("invalid Shadowsocks method or password for %s", o.Tag)
		}
		return nil
	case "vless":
		if err := proxy(); err != nil {
			return err
		}
		if _, err := uuid.Parse(o.UUID); err != nil || (o.Flow != "" && o.Flow != "xtls-rprx-vision") {
			return fmt.Errorf("invalid VLESS credentials for %s", o.Tag)
		}
		r := o.Reality
		if r == nil || !hostnameRe.MatchString(r.ServerName) || !decodeKey(r.PublicKey, true, 32) || !shortIDRe.MatchString(r.ShortID) || len(r.ShortID)%2 != 0 || (r.Fingerprint != "" && !realityFingerprints[r.Fingerprint]) {
			return fmt.Errorf("invalid Reality settings for %s", o.Tag)
		}
		return nil
	case "wireguard":
		w := o.WireGuard
		if w == nil || !decodeKey(w.PrivateKey, false, 32) || !decodeKey(w.PeerPublicKey, false, 32) {
			return fmt.Errorf("invalid WireGuard keys for %s", o.Tag)
		}
		v4, v6 := false, false
		if w.AddressV4 != "" {
			p, err := parsePrefix(w.AddressV4)
			if err != nil || !p.Addr().Is4() {
				return fmt.Errorf("invalid WireGuard IPv4 address for %s", o.Tag)
			}
			v4 = true
		}
		if w.AddressV6 != "" {
			p, err := parsePrefix(w.AddressV6)
			if err != nil || p.Addr().Is4() {
				return fmt.Errorf("invalid WireGuard IPv6 address for %s", o.Tag)
			}
			v6 = true
		}
		if !v4 && !v6 || (o.IPVersion == "force_ipv4" && !v4) || (o.IPVersion == "force_ipv6" && !v6) {
			return fmt.Errorf("WireGuard address family missing for %s", o.Tag)
		}
		if len(w.Reserved) != 0 && len(w.Reserved) != 3 {
			return fmt.Errorf("invalid WireGuard reserved for %s", o.Tag)
		}
		for _, b := range w.Reserved {
			if b < 0 || b > 255 {
				return fmt.Errorf("invalid WireGuard reserved for %s", o.Tag)
			}
		}
		if w.MTU != 0 && (w.MTU < 1280 || w.MTU > 1500) {
			return fmt.Errorf("invalid WireGuard MTU for %s", o.Tag)
		}
		for i, ep := range []string{w.Endpoint, w.EndpointV4, w.EndpointV6} {
			if ep == "" {
				continue
			}
			host, port, err := net.SplitHostPort(ep)
			p, perr := strconv.Atoi(port)
			if err != nil || perr != nil || p < 1 || p > 65535 || !validHost(strings.ToLower(host)) {
				return fmt.Errorf("invalid WireGuard endpoint for %s", o.Tag)
			}
			if a, err := netip.ParseAddr(host); i > 0 && (err != nil || a.Unmap().Is4() != (i == 1)) {
				return fmt.Errorf("invalid WireGuard endpoint family for %s", o.Tag)
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported outbound type for %s", o.Tag)
}

func (r EgressRule) items() int {
	return len(r.Domain) + len(r.IP) + len(r.SourceIP) + len(r.Protocol) + strings.Count(r.Port, ",") + strings.Count(r.SourcePort, ",")
}

// validate checks the whole plan. nodeTags limits rule scopes to this server's
// landing nodes; nil skips that check (used for standalone rendering tests).
func (e *Egress) validate(nodeTags map[string]bool) error {
	if e == nil {
		return nil
	}
	if e.Version != EgressVersion {
		return fmt.Errorf("unsupported egress version")
	}
	if len(e.Outbounds) > maxEgressOutbounds || len(e.Rules) > maxEgressRules || len(e.ExemptSources) > maxEgressExempt {
		return fmt.Errorf("egress plan exceeds limits")
	}
	tags := map[string]bool{"direct": true, "block": true}
	for _, o := range e.Outbounds {
		if tags[o.Tag] {
			return fmt.Errorf("duplicate outbound tag")
		}
		if err := o.validate(); err != nil {
			return err
		}
		tags[o.Tag] = true
	}
	items := 0
	for i, r := range e.Rules {
		if len(r.InboundTags) == 0 || !tags[r.Outbound] {
			return fmt.Errorf("rule %d has no scope or an unknown outbound", i+1)
		}
		for _, t := range r.InboundTags {
			if !strings.HasPrefix(t, "flvxl-") || (nodeTags != nil && !nodeTags[t]) {
				return fmt.Errorf("rule %d targets an unknown landing node", i+1)
			}
		}
		for _, d := range r.Domain {
			if err := validDomainEntry(d); err != nil {
				return fmt.Errorf("rule %d: %w", i+1, err)
			}
		}
		for _, v := range r.IP {
			if err := validIPEntry(v, true); err != nil {
				return fmt.Errorf("rule %d: %w", i+1, err)
			}
		}
		for _, v := range r.SourceIP {
			if err := validIPEntry(v, true); err != nil {
				return fmt.Errorf("rule %d: %w", i+1, err)
			}
		}
		if err := validPortSpec(r.Port); err != nil {
			return fmt.Errorf("rule %d: %w", i+1, err)
		}
		if err := validPortSpec(r.SourcePort); err != nil {
			return fmt.Errorf("rule %d: %w", i+1, err)
		}
		switch r.Network {
		case "", "tcp", "udp", "tcp,udp":
		default:
			return fmt.Errorf("rule %d: invalid network", i+1)
		}
		for _, p := range r.Protocol {
			if !egressProtocols[p] {
				return fmt.Errorf("rule %d: invalid protocol", i+1)
			}
		}
		items += r.items()
	}
	if items > maxEgressItems {
		return fmt.Errorf("egress plan exceeds matcher limit")
	}
	for _, s := range e.ExemptSources {
		if _, err := parsePrefix(s); err != nil {
			return fmt.Errorf("invalid exempt source")
		}
	}
	return nil
}

// geoFiles lists the geo data files the rendered rules need.
func (e *Egress) geoFiles() []string {
	ip, site := false, false
	for _, r := range e.Rules {
		for _, d := range r.Domain {
			site = site || strings.HasPrefix(d, "geosite:")
		}
		for _, v := range append(append([]string{}, r.IP...), r.SourceIP...) {
			ip = ip || strings.HasPrefix(v, "geoip:")
		}
	}
	var out []string
	if ip {
		out = append(out, "geoip.dat")
	}
	if site {
		out = append(out, "geosite.dat")
	}
	return out
}

// localAddrs holds the source addresses the kernel would pick for each
// family; forced outbounds bind to them.
type localAddrs struct{ v4, v6 netip.Addr }

const ipBindAddressNoPort = "24" // IP_BIND_ADDRESS_NO_PORT, level SOL_IP (0)

var freedomStrategy = map[string]string{"system": "UseIP", "prefer_ipv4": "UseIPv4v6", "prefer_ipv6": "UseIPv6v4", "force_ipv4": "ForceIPv4", "force_ipv6": "ForceIPv6"}
var dialStrategy = map[string]string{"prefer_ipv4": "UseIPv4v6", "prefer_ipv6": "UseIPv6v4", "force_ipv4": "ForceIPv4", "force_ipv6": "ForceIPv6"}
var wireguardStrategy = map[string]string{"system": "ForceIP", "prefer_ipv4": "ForceIPv4v6", "prefer_ipv6": "ForceIPv6v4", "force_ipv4": "ForceIPv4", "force_ipv6": "ForceIPv6"}

const warpEndpointV4 = "162.159.192.1:2408"
const warpEndpointV6 = "[2606:4700:d0::a29f:c001]:2408"

func blackhole(tag string) map[string]any {
	return map[string]any{"tag": tag, "protocol": "blackhole"}
}

// forceBind returns the sendThrough address for forced modes. Binding the socket
// to an address of the forced family makes the kernel and Go dialer reject the
// other family even for IP-literal destinations, for TCP and UDP alike.
func forceBind(ipVersion string, local localAddrs) (string, bool) {
	switch ipVersion {
	case "force_ipv4":
		return local.v4.String(), local.v4.IsValid()
	case "force_ipv6":
		return local.v6.String(), local.v6.IsValid()
	}
	return "", true
}

// render converts one typed outbound into Core JSON. A forced outbound on a
// server without that address family becomes a blackhole: connections fail,
// and nothing leaves over the other family. For WireGuard the IP version is
// the family inside the tunnel, fixed by its interface addresses instead.
func (o EgressOutbound) render(local localAddrs) (map[string]any, string) {
	bind, ok := "", true
	if o.Type != "wireguard" {
		bind, ok = forceBind(o.IPVersion, local)
	}
	if !ok {
		family := "IPv4"
		if o.IPVersion == "force_ipv6" {
			family = "IPv6"
		}
		return blackhole(o.Tag), fmt.Sprintf("%s: 服务器没有可用的 %s 地址，强制 %s 的连接会失败", o.Tag, family, family)
	}
	out := map[string]any{"tag": o.Tag}
	sockopt := map[string]any{}
	if bind != "" {
		out["sendThrough"] = bind
		sockopt["customSockopt"] = []any{map[string]any{"system": "linux", "network": "tcp", "level": "0", "opt": ipBindAddressNoPort, "value": "1", "type": "int"}}
	}
	if s, ok := dialStrategy[o.IPVersion]; ok && o.Type != "direct" && o.Type != "wireguard" {
		sockopt["domainStrategy"] = s
	}
	stream := map[string]any{}
	switch o.Type {
	case "direct":
		out["protocol"] = "freedom"
		out["settings"] = map[string]any{"domainStrategy": freedomStrategy[o.IPVersion]}
	case "socks", "http":
		out["protocol"] = o.Type
		server := map[string]any{"address": o.Server, "port": o.Port}
		if o.Username != "" || o.Password != "" {
			server["users"] = []any{map[string]any{"user": o.Username, "pass": o.Password}}
		}
		out["settings"] = map[string]any{"servers": []any{server}}
	case "shadowsocks":
		out["protocol"] = "shadowsocks"
		out["settings"] = map[string]any{"servers": []any{map[string]any{"address": o.Server, "port": o.Port, "method": o.Method, "password": o.Password}}}
	case "vless":
		out["protocol"] = "vless"
		user := map[string]any{"id": o.UUID, "encryption": "none"}
		if o.Flow != "" {
			user["flow"] = o.Flow
		}
		out["settings"] = map[string]any{"vnext": []any{map[string]any{"address": o.Server, "port": o.Port, "users": []any{user}}}}
		fp := o.Reality.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		stream["network"] = "tcp"
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{"serverName": o.Reality.ServerName, "publicKey": o.Reality.PublicKey, "shortId": o.Reality.ShortID, "fingerprint": fp}
	case "wireguard":
		w := o.WireGuard
		var addresses []string
		if w.AddressV4 != "" && o.IPVersion != "force_ipv6" {
			p, _ := parsePrefix(w.AddressV4)
			addresses = append(addresses, netip.PrefixFrom(p.Addr(), 32).String())
		}
		if w.AddressV6 != "" && o.IPVersion != "force_ipv4" {
			p, _ := parsePrefix(w.AddressV6)
			addresses = append(addresses, netip.PrefixFrom(p.Addr(), 128).String())
		}
		endpoint := w.Endpoint
		if endpoint == "" {
			v4, v6 := w.EndpointV4, w.EndpointV6
			if v4 == "" {
				v4 = warpEndpointV4
			}
			if v6 == "" {
				v6 = warpEndpointV6
			}
			switch {
			case local.v4.IsValid():
				endpoint = v4
			case local.v6.IsValid():
				endpoint = v6
			default:
				return blackhole(o.Tag), fmt.Sprintf("%s: 服务器没有可用的公网地址连接 WARP", o.Tag)
			}
		}
		mtu := w.MTU
		if mtu == 0 {
			mtu = 1280
		}
		settings := map[string]any{"secretKey": w.PrivateKey, "address": addresses, "peers": []any{map[string]any{"publicKey": w.PeerPublicKey, "endpoint": endpoint, "allowedIPs": []string{"0.0.0.0/0", "::/0"}}}, "mtu": mtu, "domainStrategy": wireguardStrategy[o.IPVersion], "noKernelTun": true}
		if len(w.Reserved) == 3 {
			settings["reserved"] = w.Reserved
		}
		out["protocol"] = "wireguard"
		out["settings"] = settings
	}
	if len(sockopt) > 0 {
		stream["sockopt"] = sockopt
	}
	if len(stream) > 0 {
		out["streamSettings"] = stream
	}
	return out, ""
}

// geoIPReader expands a geoip category; tests replace it.
type geoIPReader func(code string) ([]netip.Prefix, error)

// sourceSet expands a source list (geoip categories, CIDRs, reverse geoip).
func sourceSet(entries []string, read geoIPReader) (dualSet, error) {
	var direct []netip.Prefix
	sets := []dualSet{}
	for _, v := range entries {
		if code, ok := strings.CutPrefix(v, "geoip:"); ok {
			reverse := strings.HasPrefix(code, "!")
			code = strings.TrimPrefix(code, "!")
			prefixes, err := read(code)
			if err != nil {
				return dualSet{}, err
			}
			s := newDualSet(prefixes)
			if reverse {
				s = s.complement()
			}
			sets = append(sets, s)
			continue
		}
		p, err := parsePrefix(v)
		if err != nil {
			return dualSet{}, err
		}
		direct = append(direct, p)
	}
	out := newDualSet(direct)
	for _, s := range sets {
		out = out.union(s)
	}
	return out, nil
}

// sourceMemo keeps expanded source lists between renders. A geoip category
// minus the exemption is ~10k CIDRs; recomputing it on every periodic re-render
// would re-read the 16 MiB geoip.dat. The pinned geo data never changes inside
// one agent process, so (source entries, exemption) fully determine the result.
// Only entries used by the latest render are kept, and their slices are the
// ones the rendered plan already holds.
type sourceMemo struct{ prev, next map[string]sourceResult }

type sourceResult struct {
	list []string
	drop bool
}

func (s *sourceMemo) get(key string) (sourceResult, bool) {
	if s == nil {
		return sourceResult{}, false
	}
	if r, ok := s.next[key]; ok {
		return r, true
	}
	r, ok := s.prev[key]
	return r, ok
}

func (s *sourceMemo) put(key string, r sourceResult) {
	if s == nil {
		return
	}
	if s.next == nil {
		s.next = map[string]sourceResult{}
	}
	s.next[key] = r
}

// rotate keeps only what the finished render used.
func (s *sourceMemo) rotate() {
	if s != nil {
		s.prev, s.next = s.next, nil
	}
}

// renderRules returns the routing rules in order: the immutable safety rule
// first, then the plan. Source lists that overlap exempt sources are rewritten
// to explicit CIDRs minus the exemption; a rule whose whole source set is
// exempt is dropped (an empty list would match every source).
func renderRules(e *Egress, read geoIPReader, memo *sourceMemo) ([]any, error) {
	rules := []any{map[string]any{"type": "field", "ruleTag": "flvx-safety", "ip": blockedDestinations, "outboundTag": "block"}}
	if e == nil {
		return rules, nil
	}
	exemptPrefixes := make([]netip.Prefix, 0, len(e.ExemptSources))
	for _, s := range e.ExemptSources {
		p, err := parsePrefix(s)
		if err != nil {
			return nil, err
		}
		exemptPrefixes = append(exemptPrefixes, p)
	}
	exempt := newDualSet(exemptPrefixes)
	exemptKey := strings.Join(exempt.cidrs(), ",")
	for i, r := range e.Rules {
		rule := map[string]any{"type": "field", "ruleTag": "flvxr-" + strconv.Itoa(i+1), "outboundTag": r.Outbound, "inboundTag": sortedCopy(r.InboundTags)}
		if len(r.Domain) > 0 {
			rule["domain"] = r.Domain
		}
		if len(r.IP) > 0 {
			rule["ip"] = r.IP
		}
		if len(r.SourceIP) > 0 {
			list := r.SourceIP
			if !exempt.empty() {
				key := strings.Join(r.SourceIP, "\n") + "\x00" + exemptKey
				res, ok := memo.get(key)
				if !ok {
					set, err := sourceSet(r.SourceIP, read)
					if err != nil {
						return nil, fmt.Errorf("rule %d: %w", i+1, err)
					}
					res = sourceResult{list: r.SourceIP}
					if set.intersects(exempt) {
						set = set.subtract(exempt)
						if set.empty() {
							res = sourceResult{drop: true}
						} else {
							res = sourceResult{list: set.cidrs()}
						}
					}
				}
				memo.put(key, res)
				if res.drop {
					continue
				}
				list = res.list
			}
			rule["sourceIP"] = list
		}
		if r.Port != "" {
			rule["port"] = r.Port
		}
		if r.SourcePort != "" {
			rule["sourcePort"] = r.SourcePort
		}
		if r.Network != "" {
			rule["network"] = r.Network
		}
		if len(r.Protocol) > 0 {
			rule["protocol"] = r.Protocol
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
