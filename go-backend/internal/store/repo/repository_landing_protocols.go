package repo

// Plan 121 (contract landing-protocols-v1): AnyTLS / Snell / Mieru landing
// nodes. They exist only on mmwx-shared servers whose share advertises the
// protocol (Share.ProtocolCaps); native FLVX landings keep SS2022 / VLESS.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// LandingProtocolOpts are a node's non-secret protocol options, copied from
// the server defaults when the node is created (stored in ProtocolOpts).
type LandingProtocolOpts struct {
	SNI       string `json:"sni,omitempty"`
	Version   int    `json:"version,omitempty"`
	Obfs      string `json:"obfs,omitempty"`
	ObfsHost  string `json:"obfsHost,omitempty"`
	Transport string `json:"transport,omitempty"`
}

// LandingNodeOpts decodes ProtocolOpts (empty for SS2022 / VLESS nodes).
func LandingNodeOpts(n *model.LandingNode) LandingProtocolOpts {
	var o LandingProtocolOpts
	_ = json.Unmarshal([]byte(n.ProtocolOpts), &o)
	return o
}

func knownLandingProtocol(p string) bool {
	return p == "ss2022" || p == "vless_reality_vision" || landingcustom.IsExtraProtocol(p)
}

// validLandingHostname accepts a DNS name (not an IP literal) for SNI / obfs.
func validLandingHostname(h string) bool {
	if h == "" || len(h) > 253 || net.ParseIP(h) != nil {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(h, "."), ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// normalizeLandingProtocolDefaults fills empty values (older rows/frontends).
func normalizeLandingProtocolDefaults(s *model.LandingServer) {
	s.AnyTLSServerName = strings.ToLower(strings.TrimSpace(s.AnyTLSServerName))
	s.SnellObfs = strings.ToLower(strings.TrimSpace(s.SnellObfs))
	s.SnellObfsHost = strings.ToLower(strings.TrimSpace(s.SnellObfsHost))
	s.MieruTransport = strings.ToUpper(strings.TrimSpace(s.MieruTransport))
	if s.SnellVersion == 0 {
		s.SnellVersion = 4
	}
	if s.SnellObfs == "" {
		s.SnellObfs = "none"
	}
	if s.SnellObfs == "none" {
		s.SnellObfsHost = ""
	}
	if s.MieruTransport == "" {
		s.MieruTransport = "TCP"
	}
}

func validateLandingProtocolDefaults(s *model.LandingServer) error {
	normalizeLandingProtocolDefaults(s)
	if s.AnyTLSServerName != "" && !validLandingHostname(s.AnyTLSServerName) {
		return errors.New("AnyTLS SNI 必须是域名（可留空）")
	}
	if s.SnellVersion != 4 && s.SnellVersion != 5 {
		return errors.New("Snell 版本只能是 v4 或 v5")
	}
	switch s.SnellObfs {
	case "none":
	case "http", "tls":
		if !validLandingHostname(s.SnellObfsHost) {
			return errors.New("Snell 混淆域名无效")
		}
	default:
		return errors.New("Snell 混淆方式无效")
	}
	if s.MieruTransport != "TCP" && s.MieruTransport != "UDP" {
		return errors.New("Mieru 传输方式只能是 TCP 或 UDP")
	}
	return nil
}

// SharedProtocolCap returns the share's capability for an extra protocol.
func SharedProtocolCap(s *model.LandingServer, protocol string) (landingcustom.ProtocolCap, bool) {
	if s == nil || s.Source != "shared" {
		return landingcustom.ProtocolCap{}, false
	}
	caps := SharedLandingCaps(s)
	return caps.ProtocolCaps.Supports(protocol)
}

// SharedAllowedProtocols = Custom's v1 list plus the advertised extras.
func SharedAllowedProtocols(s *model.LandingServer) map[string]bool {
	out := map[string]bool{}
	c := SharedLandingCaps(s)
	for _, p := range c.Protocols {
		out[p] = true
	}
	for _, p := range landingcustom.ExtraProtocols {
		if _, ok := c.ProtocolCaps.Supports(p); ok {
			out[p] = true
		}
	}
	return out
}

// LandingNodeWithheld: a shared node of an extra protocol that the share no
// longer advertises (e.g. its Helper was replaced by an older one). It is left
// out of the desired state (Custom removes the inbound, fail closed) and of
// the egress plan, so the rest of the share keeps syncing; port, credentials
// and counters stay, and it comes back once the capability returns.
func LandingNodeWithheld(s *model.LandingServer, n *model.LandingNode) bool {
	if s == nil || n == nil || !landingcustom.IsExtraProtocol(n.Protocol) {
		return false
	}
	return LandingWithheldFunc(s)(n)
}

// LandingWithheldFunc parses the server's caps once for loops over its nodes.
func LandingWithheldFunc(s *model.LandingServer) func(*model.LandingNode) bool {
	var supported map[string]bool
	return func(n *model.LandingNode) bool {
		if s == nil || n == nil || !landingcustom.IsExtraProtocol(n.Protocol) {
			return false
		}
		if s.Source != "shared" {
			return true // never valid on native servers
		}
		if supported == nil {
			supported = map[string]bool{}
			caps := SharedLandingCaps(s)
			for _, p := range landingcustom.ExtraProtocols {
				_, supported[p] = caps.ProtocolCaps.Supports(p)
			}
		}
		return !supported[n.Protocol]
	}
}

// SharedBusyPortList decodes the stored busy_ports of a shared server.
func SharedBusyPortList(s *model.LandingServer) []int {
	var out []int
	if s != nil && s.SharedBusyPorts != "" {
		_ = json.Unmarshal([]byte(s.SharedBusyPorts), &out)
	}
	return out
}

// LandingWithheldReason is the user-visible explanation.
func LandingWithheldReason(protocol string) string {
	return "妙妙屋端暂不支持 " + LandingProtocolName(protocol) + "（该服务器 Helper 需升级），节点已暂停下发，端口和凭据保留"
}

// LandingProtocolName is the display name used in messages.
func LandingProtocolName(p string) string {
	switch p {
	case "ss2022":
		return "SS2022"
	case "vless_reality_vision":
		return "VLESS Reality Vision"
	case landingcustom.ProtocolAnyTLS:
		return "AnyTLS"
	case landingcustom.ProtocolSnell:
		return "Snell"
	case landingcustom.ProtocolMieru:
		return "Mieru"
	}
	return p
}

// checkExtraProtocolCreate refuses a new node whose protocol or server
// defaults the share does not support right now.
func checkExtraProtocolCreate(s *model.LandingServer, protocol string) error {
	if !landingcustom.IsExtraProtocol(protocol) {
		return nil
	}
	if s.Source != "shared" {
		return errors.New("AnyTLS / Snell / Mieru 目前只支持妙妙屋共享服务器")
	}
	cp, ok := SharedProtocolCap(s, protocol)
	if !ok {
		return errors.New("妙妙屋端暂不支持 " + LandingProtocolName(protocol) + "（该服务器 Helper 需升级）")
	}
	normalizeLandingProtocolDefaults(s)
	if protocol == landingcustom.ProtocolSnell && (!cp.AllowsSnellVersion(s.SnellVersion) || !cp.AllowsObfs(s.SnellObfs)) {
		return errors.New("妙妙屋端不支持该服务器的 Snell 版本或混淆设置，请管理员调整落地服务器设置")
	}
	if protocol == landingcustom.ProtocolMieru && !cp.AllowsTransport(s.MieruTransport) {
		return errors.New("妙妙屋端不支持该服务器的 Mieru 传输方式，请管理员调整落地服务器设置")
	}
	return nil
}

const landingAlnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomLandingString(n int, alphabet string) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

// anytlsCertName: the SNI if set, else the public host (DNS name or IP).
func anytlsCertName(sni, publicHost string) string {
	if sni != "" {
		return sni
	}
	h := strings.Trim(strings.TrimSpace(publicHost), "[]")
	if h != "" {
		return h
	}
	return "flvx-landing"
}

// selfSignedLandingCert makes the AnyTLS server certificate: ECDSA P-256,
// 10 years, SAN = name (DNS or IP). Returns PEMs and the SHA-256 of the DER
// (lowercase hex) that clients pin.
func selfSignedLandingCert(name string, now time.Time) (string, string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", "", "", err
	}
	tpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(name); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		return "", "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256(der)
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, hex.EncodeToString(sum[:]), nil
}

// newExtraLandingCredentials fills an AnyTLS / Snell / Mieru node from the
// server defaults: random credentials, fixed options, AnyTLS certificate.
func newExtraLandingCredentials(n *model.LandingNode, s *model.LandingServer) error {
	normalizeLandingProtocolDefaults(s)
	var opts LandingProtocolOpts
	var err error
	switch n.Protocol {
	case landingcustom.ProtocolAnyTLS:
		if n.Password, err = randomLandingString(24, landingAlnum); err != nil {
			return err
		}
		opts.SNI = s.AnyTLSServerName
		if opts.SNI == "" {
			if host := strings.ToLower(strings.Trim(strings.TrimSpace(s.PublicHost), "[]")); validLandingHostname(host) {
				opts.SNI = host
			}
		}
		if n.TLSCertPEM, n.TLSKeyPEM, n.CertSHA256, err = selfSignedLandingCert(anytlsCertName(opts.SNI, s.PublicHost), time.Now()); err != nil {
			return err
		}
	case landingcustom.ProtocolSnell:
		if n.Password, err = randomLandingString(32, landingAlnum); err != nil {
			return err
		}
		opts.Version, opts.Obfs, opts.ObfsHost = s.SnellVersion, s.SnellObfs, s.SnellObfsHost
	case landingcustom.ProtocolMieru:
		if n.Username, err = randomLandingString(10, "abcdefghijklmnopqrstuvwxyz0123456789"); err != nil {
			return err
		}
		if n.Password, err = randomLandingString(24, landingAlnum); err != nil {
			return err
		}
		opts.Transport = s.MieruTransport
	default:
		return errors.New("协议模板无效")
	}
	raw, _ := json.Marshal(opts)
	n.ProtocolOpts = string(raw)
	return nil
}

// fillSharedExtraProtocol adds the contract object of an extra-protocol node.
func fillSharedExtraProtocol(n model.LandingNode, d *landingcustom.Node) {
	o := LandingNodeOpts(&n)
	switch n.Protocol {
	case landingcustom.ProtocolAnyTLS:
		d.AnyTLS = &landingcustom.AnyTLS{Password: n.Password, Padding: "default", TLS: landingcustom.AnyTLSTLS{ServerName: o.SNI, CertificatePEM: n.TLSCertPEM, KeyPEM: n.TLSKeyPEM}}
	case landingcustom.ProtocolSnell:
		obfs := o.Obfs
		if obfs == "" {
			obfs = "none"
		}
		d.Snell = &landingcustom.Snell{PSK: n.Password, Version: o.Version, Obfs: obfs, ObfsHost: o.ObfsHost}
	case landingcustom.ProtocolMieru:
		d.Mieru = &landingcustom.Mieru{Username: n.Username, Password: n.Password}
	}
}

// sharedLandingHosts: the normalized addresses of a shared server.
func sharedLandingHosts(s model.LandingServer) map[string]bool {
	c := SharedLandingCaps(&s)
	out := map[string]bool{}
	for _, h := range []string{c.Hosts.IPv4, c.Hosts.IPv6, c.Hosts.Domain, s.PublicHost} {
		if h = normalizeLandingHost(h); h != "" {
			out[h] = true
		}
	}
	return out
}

// sharedMachineBusyPorts lists ports a new node on shared server s must avoid
// besides its own nodes: Custom's busy_ports (official inbounds, other panels
// such as x-ui) and ports FLVX itself uses on the same machine through an FLVX
// node with the same address (dual-installed machines). Literal address match
// only, no DNS.
func sharedMachineBusyPorts(tx *gorm.DB, s model.LandingServer) (map[int]bool, error) {
	used := map[int]bool{}
	for _, p := range SharedBusyPortList(&s) {
		used[p] = true
	}
	hosts := sharedLandingHosts(s)
	var nodes []model.Node
	if err := tx.Where("is_remote = 0").Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		match := false
		for h := range landingNodeHosts(model.LandingServer{}, n) {
			match = match || hosts[h]
		}
		if !match {
			continue
		}
		for _, q := range []struct {
			m      interface{}
			column string
			where  string
		}{{&model.ForwardPort{}, "port", "node_id = ?"}, {&model.ChainTunnel{}, "port", "node_id = ? AND port IS NOT NULL"}, {&model.NodeDeployedInbound{}, "listen_port", "node_id = ?"}, {&model.NodeRuntimeResource{}, "port", "node_id = ? AND status <> 'released'"}, {&model.ForwardChainNode{}, "port", "node_id = ?"}} {
			var ports []int
			if err := tx.Model(q.m).Where(q.where, n.ID).Pluck(q.column, &ports).Error; err != nil {
				return nil, err
			}
			for _, p := range ports {
				used[p] = true
			}
		}
	}
	return used, nil
}

// sharedLandingPortsOnNodeHost is the reverse direction: forward ports picked
// on an FLVX node must avoid shared landing nodes on the same machine.
func sharedLandingPortsOnNodeHost(tx *gorm.DB, nodeID int64) ([]int, error) {
	var servers []model.LandingServer
	if err := tx.Where("source = ?", "shared").Find(&servers).Error; err != nil || len(servers) == 0 {
		return nil, err
	}
	var node model.Node
	if err := tx.First(&node, nodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	own := landingNodeHosts(model.LandingServer{}, node)
	var ports []int
	for _, s := range servers {
		match := false
		for h := range sharedLandingHosts(s) {
			match = match || own[h]
		}
		if !match {
			continue
		}
		var p []int
		if err := tx.Model(&model.LandingNode{}).Where("landing_server_id = ?", s.ID).Pluck("port", &p).Error; err != nil {
			return nil, err
		}
		ports = append(ports, p...)
	}
	return ports, nil
}

// extraLandingURL: share links where a de facto standard exists.
//   - AnyTLS: anytls://<password>@host:port/?sni=..&insecure=1#name (the URI
//     scheme has no pinning parameter, so link importers must accept the
//     self-signed certificate; the Clash config pins it instead).
//   - Mieru:  mierus://user:pass@host?multiplexing=..&port=P&profile=..&protocol=TCP#name
//   - Snell:  none (empty string); Surge / Clash configs are given instead.
func extraLandingURL(n model.LandingNode, host string, port int) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	o := LandingNodeOpts(&n)
	switch n.Protocol {
	case landingcustom.ProtocolAnyTLS:
		q := url.Values{"insecure": {"1"}}
		if o.SNI != "" {
			q.Set("sni", o.SNI)
		}
		return "anytls://" + url.PathEscape(n.Password) + "@" + net.JoinHostPort(host, strconv.Itoa(port)) + "/?" + q.Encode() + "#" + url.PathEscape(n.Name)
	case landingcustom.ProtocolMieru:
		transport := o.Transport
		if transport == "" {
			transport = "TCP"
		}
		h := host
		if strings.Contains(h, ":") {
			h = "[" + h + "]"
		}
		q := url.Values{"multiplexing": {"MULTIPLEXING_LOW"}, "port": {strconv.Itoa(port)}, "profile": {n.Name}, "protocol": {transport}}
		return "mierus://" + url.PathEscape(n.Username) + ":" + url.PathEscape(n.Password) + "@" + h + "?" + q.Encode() + "#" + url.PathEscape(n.Name)
	}
	return ""
}

// yamlString quotes a value as a YAML double-quoted scalar (JSON quoting is
// valid YAML), so names with spaces, colons or non-ASCII never break it.
func yamlString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// LandingClientConfigs returns the Clash (mihomo) proxy entry and, for Snell,
// the Surge proxy line. udp follows the share's capability for the protocol.
func LandingClientConfigs(n model.LandingNode, host string, port int, udp bool) (string, string) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	o := LandingNodeOpts(&n)
	var b strings.Builder
	line := func(format string, args ...interface{}) { b.WriteString(fmt.Sprintf(format, args...) + "\n") }
	line("- name: %s", yamlString(n.Name))
	switch n.Protocol {
	case landingcustom.ProtocolAnyTLS:
		line("  type: anytls")
		line("  server: %s", yamlString(host))
		line("  port: %d", port)
		line("  password: %s", yamlString(n.Password))
		if o.SNI != "" {
			line("  sni: %s", yamlString(o.SNI))
		}
		line("  fingerprint: %s", yamlString(n.CertSHA256))
		line("  client-fingerprint: chrome")
		line("  udp: %t", udp)
	case landingcustom.ProtocolSnell:
		version := o.Version
		if version == 0 {
			version = 4
		}
		line("  type: snell")
		line("  server: %s", yamlString(host))
		line("  port: %d", port)
		line("  psk: %s", yamlString(n.Password))
		line("  version: %d", version)
		line("  udp: %t", udp)
		if o.Obfs == "http" || o.Obfs == "tls" {
			line("  obfs-opts:")
			line("    mode: %s", o.Obfs)
			line("    host: %s", yamlString(o.ObfsHost))
		}
		name := strings.NewReplacer(",", " ", "=", " ", "\n", " ", "\r", " ").Replace(n.Name)
		surge := fmt.Sprintf("%s = snell, %s, %d, psk=%s, version=%d", strings.TrimSpace(name), host, port, n.Password, version)
		if o.Obfs == "http" || o.Obfs == "tls" {
			surge += ", obfs=" + o.Obfs + ", obfs-host=" + o.ObfsHost
		}
		return strings.TrimRight(b.String(), "\n"), surge
	case landingcustom.ProtocolMieru:
		transport := o.Transport
		if transport == "" {
			transport = "TCP"
		}
		line("  type: mieru")
		line("  server: %s", yamlString(host))
		line("  port: %d", port)
		line("  transport: %s", transport)
		line("  username: %s", yamlString(n.Username))
		line("  password: %s", yamlString(n.Password))
		line("  multiplexing: MULTIPLEXING_LOW")
	default:
		return "", ""
	}
	return strings.TrimRight(b.String(), "\n"), ""
}
