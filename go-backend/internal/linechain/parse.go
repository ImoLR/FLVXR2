package linechain

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MaxLinkLength bounds the pasted link; real share links are far shorter.
const MaxLinkLength = 4096

var (
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62})(\.[a-z0-9]([a-z0-9-]{0,62}))*\.?$`)
	wsPathRe   = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,255}$`)
	grpcNameRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)
	shortIDRe  = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
)

var (
	ss2022Keys   = map[string]int{"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32}
	ssAEAD       = map[string]bool{"aes-128-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true}
	ssAliases    = map[string]string{"chacha20-poly1305": "chacha20-ietf-poly1305", "xchacha20-poly1305": "xchacha20-ietf-poly1305"}
	fingerprints = map[string]bool{"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true, "edge": true, "360": true, "qq": true, "random": true, "randomized": true}
	realityFPs   = map[string]bool{"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true, "edge": true, "random": true, "randomized": true}
	alpnValues   = map[string]bool{"h2": true, "http/1.1": true, "h3": true}
	vmessCiphers = map[string]bool{"auto": true, "aes-128-gcm": true, "chacha20-poly1305": true, "none": true, "zero": true}
)

// SupportedText lists the accepted link types for error messages.
const SupportedText = "支持 ss://（含 SS2022）、vless://、vmess://、trojan://、hysteria2://（hy2://）、socks5://、http(s)://"

// ParseLink converts a pasted share link into an upstream and the link's own
// name. Every field is length/format checked; the address itself is checked
// separately (ValidateAddress) because that needs DNS and the panel's lists.
func ParseLink(raw string) (Upstream, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Upstream{}, "", errors.New("请粘贴你的节点链接")
	}
	if len(raw) > MaxLinkLength {
		return Upstream{}, "", errors.New("节点链接过长")
	}
	if strings.ContainsAny(raw, "\r\n\t\x00") {
		return Upstream{}, "", errors.New("一次只能粘贴一条节点链接")
	}
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return Upstream{}, "", errors.New("无法识别的节点链接，" + SupportedText)
	}
	var (
		u    Upstream
		name string
		err  error
	)
	switch strings.ToLower(scheme) {
	case "ss":
		u, name, err = parseSS(raw)
	case "vless":
		u, name, err = parseVLESS(raw)
	case "vmess":
		u, name, err = parseVMess(raw)
	case "trojan":
		u, name, err = parseTrojan(raw)
	case "hysteria2", "hy2":
		u, name, err = parseHysteria2(raw)
	case "socks", "socks5", "socks5h":
		u, name, err = parseProxy(raw, "socks")
	case "http", "https":
		u, name, err = parseProxy(raw, "http")
	case "tuic":
		return Upstream{}, "", errors.New("暂不支持 TUIC 节点：落地程序没有 TUIC 出站，请改用 SS / VLESS / VMess / Trojan / Hysteria2 / SOCKS5 / HTTP 节点")
	case "hysteria":
		return Upstream{}, "", errors.New("不支持 Hysteria 1 节点，请改用 Hysteria2（hysteria2://）")
	case "ssr":
		return Upstream{}, "", errors.New("不支持 ShadowsocksR 节点，请改用 Shadowsocks（AEAD 或 2022）")
	default:
		return Upstream{}, "", errors.New("不支持的节点链接类型 " + scheme + "://，" + SupportedText)
	}
	if err != nil {
		return Upstream{}, "", err
	}
	u.IPVersion = "system"
	u.Server = normalizeHost(u.Server)
	if err := u.validateFields(); err != nil {
		return Upstream{}, "", err
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return u, strings.TrimSpace(name), nil
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Unmap().String()
	}
	return strings.TrimSuffix(h, ".")
}

// fragment splits off and decodes #name.
func fragment(raw string) (string, string) {
	if i := strings.Index(raw, "#"); i >= 0 {
		name, err := url.PathUnescape(raw[i+1:])
		if err != nil {
			name = raw[i+1:]
		}
		return raw[:i], name
	}
	return raw, ""
}

func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func splitHostPort(hostport string) (string, int, error) {
	hostport = strings.TrimSuffix(hostport, "/")
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, errors.New("节点链接缺少地址或端口")
	}
	if strings.ContainsAny(p, ",-") {
		return "", 0, errors.New("暂不支持端口跳跃（多端口）链接，请使用单个端口")
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("节点端口无效（1-65535）")
	}
	if host == "" {
		return "", 0, errors.New("节点链接缺少地址")
	}
	return host, port, nil
}

func insecure(q url.Values) bool {
	for _, k := range []string{"allowInsecure", "allowinsecure", "insecure", "skip-cert-verify"} {
		if v := strings.ToLower(q.Get(k)); v == "1" || v == "true" {
			return true
		}
	}
	return false
}

var errInsecure = errors.New("不支持跳过证书验证（allowInsecure / insecure=1）的链接：落地程序已不再接受这种不安全设置，请给节点换用有效证书，或在链接里提供证书指纹 pinSHA256")

// pinned normalizes certificate SHA-256 pins (hex, optional colons, comma
// separated, at most 4).
func pinned(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	parts := strings.Split(v, ",")
	if len(parts) > 4 {
		return "", errors.New("证书指纹最多 4 个")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), ":", ""))
		if b, err := hex.DecodeString(p); err != nil || len(b) != 32 {
			return "", errors.New("证书指纹 pinSHA256 格式无效（需要 64 位十六进制）")
		}
		out = append(out, p)
	}
	return strings.Join(out, ","), nil
}

func tlsFromQuery(q url.Values, sniKeys ...string) (*TLS, error) {
	if insecure(q) {
		return nil, errInsecure
	}
	t := &TLS{Fingerprint: strings.ToLower(q.Get("fp"))}
	for _, k := range sniKeys {
		if v := q.Get(k); v != "" {
			t.ServerName = strings.ToLower(v)
			break
		}
	}
	if a := q.Get("alpn"); a != "" {
		for _, v := range strings.Split(a, ",") {
			if v = strings.TrimSpace(v); v != "" {
				t.ALPN = append(t.ALPN, v)
			}
		}
	}
	pin := q.Get("pinSHA256")
	if pin == "" {
		pin = q.Get("pcs")
	}
	p, err := pinned(pin)
	if err != nil {
		return nil, err
	}
	t.PinnedSHA256 = p
	if t.ServerName == "" && len(t.ALPN) == 0 && t.Fingerprint == "" && t.PinnedSHA256 == "" {
		return nil, nil
	}
	return t, nil
}

func parseSS(raw string) (Upstream, string, error) {
	raw, name := fragment(raw)
	body := raw[len("ss://"):]
	if i := strings.Index(body, "?"); i >= 0 {
		q, _ := url.ParseQuery(body[i+1:])
		if q.Get("plugin") != "" {
			return Upstream{}, "", errors.New("不支持带插件（plugin）的 Shadowsocks 链接")
		}
		body = body[:i]
	}
	body = strings.TrimSuffix(body, "/")
	var userinfo, hostport string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userinfo, hostport = body[:at], body[at+1:]
		if unescaped, err := url.PathUnescape(userinfo); err == nil && strings.Contains(unescaped, ":") {
			userinfo = unescaped
		} else if decoded, err := decodeBase64Loose(userinfo); err == nil {
			userinfo = string(decoded)
		} else {
			return Upstream{}, "", errors.New("Shadowsocks 链接的加密信息无法解析")
		}
	} else {
		decoded, err := decodeBase64Loose(body)
		if err != nil {
			return Upstream{}, "", errors.New("Shadowsocks 链接无法解析")
		}
		at := strings.LastIndex(string(decoded), "@")
		if at < 0 {
			return Upstream{}, "", errors.New("Shadowsocks 链接缺少服务器地址")
		}
		userinfo, hostport = string(decoded[:at]), string(decoded[at+1:])
	}
	method, password, ok := strings.Cut(userinfo, ":")
	if !ok || method == "" || password == "" {
		return Upstream{}, "", errors.New("Shadowsocks 链接缺少加密方式或密码")
	}
	method = strings.ToLower(method)
	if alias, ok := ssAliases[method]; ok {
		method = alias
	}
	host, port, err := splitHostPort(hostport)
	if err != nil {
		return Upstream{}, "", err
	}
	return Upstream{Type: "shadowsocks", Server: host, Port: port, Method: method, Password: password}, name, nil
}

func parseURL(raw, kind string) (*url.URL, url.Values, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// Port-hopping authorities ("host:443,5000-6000") are not valid URLs.
		authority := raw[strings.Index(raw, "://")+3:]
		if i := strings.IndexAny(authority, "/?"); i >= 0 {
			authority = authority[:i]
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		if i := strings.LastIndex(authority, ":"); i >= 0 && strings.ContainsAny(authority[i+1:], ",-") {
			return nil, nil, "", 0, errors.New("暂不支持端口跳跃（多端口）链接，请使用单个端口")
		}
	}
	if err != nil || u.Host == "" {
		return nil, nil, "", 0, errors.New(kind + " 链接无法解析")
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return nil, nil, "", 0, err
	}
	return u, u.Query(), host, port, nil
}

// transportFromQuery handles type/network, ws and grpc parameters shared by
// VLESS and Trojan links.
func transportFromQuery(q url.Values, up *Upstream, kind string) error {
	network := strings.ToLower(q.Get("type"))
	if network == "" {
		network = strings.ToLower(q.Get("net"))
	}
	switch network {
	case "", "tcp", "raw":
		if h := strings.ToLower(q.Get("headerType")); h != "" && h != "none" {
			return errors.New("不支持 TCP 伪装头（headerType=" + h + "）的 " + kind + " 链接")
		}
		up.Network = "tcp"
	case "ws":
		up.Network = "ws"
		up.WS = &WS{Path: q.Get("path"), Host: strings.ToLower(q.Get("host"))}
		if up.WS.Path == "" {
			up.WS.Path = "/"
		}
	case "grpc":
		up.Network = "grpc"
		mode := strings.ToLower(q.Get("mode"))
		up.GRPC = &GRPC{ServiceName: q.Get("serviceName"), MultiMode: mode == "multi"}
	default:
		return errors.New("暂不支持 " + kind + " 的 " + network + " 传输，只支持 TCP、WebSocket、gRPC")
	}
	return nil
}

func parseVLESS(raw string) (Upstream, string, error) {
	raw, name := fragment(raw)
	u, q, host, port, err := parseURL(raw, "VLESS")
	if err != nil {
		return Upstream{}, "", err
	}
	if u.User == nil || u.User.Username() == "" {
		return Upstream{}, "", errors.New("VLESS 链接缺少 UUID")
	}
	if e := q.Get("encryption"); e != "" && e != "none" {
		return Upstream{}, "", errors.New("不支持 VLESS Encryption 链接（encryption 只能为 none）")
	}
	up := Upstream{Type: "vless", Server: host, Port: port, UUID: strings.ToLower(u.User.Username()), Flow: q.Get("flow")}
	if err := transportFromQuery(q, &up, "VLESS"); err != nil {
		return Upstream{}, "", err
	}
	switch security := strings.ToLower(q.Get("security")); security {
	case "", "none":
		up.Security = "none"
	case "tls":
		up.Security = "tls"
		t, err := tlsFromQuery(q, "sni", "peer")
		if err != nil {
			return Upstream{}, "", err
		}
		up.TLS = t
	case "reality":
		if insecure(q) {
			return Upstream{}, "", errInsecure
		}
		up.Security = "reality"
		up.Reality = &Reality{ServerName: strings.ToLower(q.Get("sni")), PublicKey: q.Get("pbk"), ShortID: strings.ToLower(q.Get("sid")), Fingerprint: strings.ToLower(q.Get("fp"))}
	default:
		return Upstream{}, "", errors.New("不支持 VLESS 的 security=" + security)
	}
	return up, name, nil
}

func parseTrojan(raw string) (Upstream, string, error) {
	raw, name := fragment(raw)
	u, q, host, port, err := parseURL(raw, "Trojan")
	if err != nil {
		return Upstream{}, "", err
	}
	if u.User == nil {
		return Upstream{}, "", errors.New("Trojan 链接缺少密码")
	}
	password := u.User.Username()
	if p, ok := u.User.Password(); ok {
		password += ":" + p
	}
	up := Upstream{Type: "trojan", Server: host, Port: port, Password: password, Security: "tls"}
	if err := transportFromQuery(q, &up, "Trojan"); err != nil {
		return Upstream{}, "", err
	}
	switch security := strings.ToLower(q.Get("security")); security {
	case "", "tls":
	case "none":
		return Upstream{}, "", errors.New("不支持无 TLS 的 Trojan 链接")
	default:
		return Upstream{}, "", errors.New("不支持 Trojan 的 security=" + security)
	}
	t, err := tlsFromQuery(q, "sni", "peer")
	if err != nil {
		return Upstream{}, "", err
	}
	up.TLS = t
	if q.Get("flow") != "" {
		return Upstream{}, "", errors.New("不支持带 flow 的 Trojan 链接")
	}
	return up, name, nil
}

// vmessJSON is the v2rayN share format; numbers may arrive as strings.
type vmessJSON struct {
	V             json.RawMessage `json:"v"`
	PS            string          `json:"ps"`
	Add           string          `json:"add"`
	Port          json.RawMessage `json:"port"`
	ID            string          `json:"id"`
	Aid           json.RawMessage `json:"aid"`
	Scy           string          `json:"scy"`
	Net           string          `json:"net"`
	Type          string          `json:"type"`
	Host          string          `json:"host"`
	Path          string          `json:"path"`
	TLS           string          `json:"tls"`
	SNI           string          `json:"sni"`
	ALPN          string          `json:"alpn"`
	FP            string          `json:"fp"`
	Insecure      json.RawMessage `json:"allowInsecure"`
	InsecureShort json.RawMessage `json:"insecure"`
}

func rawInt(v json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(v)), `"`)
	if s == "" || s == "null" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func parseVMess(raw string) (Upstream, string, error) {
	body, linkName := fragment(raw[len("vmess://"):])
	decoded, err := decodeBase64Loose(body)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(decoded)), "{") {
		return Upstream{}, "", errors.New("VMess 链接无法解析（只支持 v2rayN 格式）")
	}
	var j vmessJSON
	if err := json.Unmarshal(decoded, &j); err != nil {
		return Upstream{}, "", errors.New("VMess 链接内容无法解析")
	}
	port, ok := rawInt(j.Port)
	if !ok || port < 1 || port > 65535 {
		return Upstream{}, "", errors.New("VMess 节点端口无效（1-65535）")
	}
	if aid, ok := rawInt(j.Aid); !ok || aid != 0 {
		return Upstream{}, "", errors.New("不支持 alterId 不为 0 的旧版 VMess，请在服务器上改用 AEAD（alterId=0）")
	}
	for _, v := range []json.RawMessage{j.Insecure, j.InsecureShort} {
		if s := strings.Trim(strings.ToLower(string(v)), `"`); s == "1" || s == "true" {
			return Upstream{}, "", errInsecure
		}
	}
	if j.Add == "" || j.ID == "" {
		return Upstream{}, "", errors.New("VMess 链接缺少地址或 UUID")
	}
	cipher := strings.ToLower(j.Scy)
	if cipher == "" {
		cipher = "auto"
	}
	up := Upstream{Type: "vmess", Server: j.Add, Port: port, UUID: strings.ToLower(j.ID), Cipher: cipher}
	q := url.Values{"type": {strings.ToLower(j.Net)}, "headerType": {j.Type}}
	switch strings.ToLower(j.Net) {
	case "ws":
		q.Set("path", j.Path)
		q.Set("host", j.Host)
	case "grpc":
		q.Set("serviceName", j.Path)
		if strings.ToLower(j.Type) == "multi" {
			q.Set("mode", "multi")
		}
		q.Del("headerType")
	}
	if err := transportFromQuery(q, &up, "VMess"); err != nil {
		return Upstream{}, "", err
	}
	switch strings.ToLower(j.TLS) {
	case "", "none":
		up.Security = "none"
	case "tls":
		up.Security = "tls"
		tq := url.Values{"sni": {j.SNI}, "alpn": {j.ALPN}, "fp": {j.FP}}
		if tq.Get("sni") == "" && j.Host != "" && up.Network == "ws" {
			tq.Set("sni", j.Host)
		}
		t, err := tlsFromQuery(tq, "sni")
		if err != nil {
			return Upstream{}, "", err
		}
		up.TLS = t
	default:
		return Upstream{}, "", errors.New("不支持 VMess 的 tls=" + j.TLS)
	}
	name := j.PS
	if linkName != "" {
		name = linkName
	}
	return up, name, nil
}

func parseHysteria2(raw string) (Upstream, string, error) {
	raw, name := fragment(raw)
	u, q, host, port, err := parseURL(raw, "Hysteria2")
	if err != nil {
		return Upstream{}, "", err
	}
	if q.Get("mport") != "" {
		return Upstream{}, "", errors.New("暂不支持端口跳跃（多端口）链接，请使用单个端口")
	}
	auth := ""
	if u.User != nil {
		auth = u.User.Username()
		if p, ok := u.User.Password(); ok {
			auth += ":" + p
		}
	}
	if auth == "" {
		return Upstream{}, "", errors.New("Hysteria2 链接缺少认证密码")
	}
	up := Upstream{Type: "hysteria2", Server: host, Port: port, Password: auth, Security: "tls"}
	switch obfs := strings.ToLower(q.Get("obfs")); obfs {
	case "", "none":
	case "salamander":
		up.Obfs = q.Get("obfs-password")
		if up.Obfs == "" {
			return Upstream{}, "", errors.New("Hysteria2 链接缺少混淆密码 obfs-password")
		}
	default:
		return Upstream{}, "", errors.New("Hysteria2 只支持 salamander 混淆")
	}
	t, err := tlsFromQuery(q, "sni", "peer")
	if err != nil {
		return Upstream{}, "", err
	}
	up.TLS = t
	return up, name, nil
}

// parseProxy handles socks5:// and http(s):// proxies with optional
// user:pass (plain or base64, as some clients export it).
func parseProxy(raw, kind string) (Upstream, string, error) {
	raw, name := fragment(raw)
	label := map[string]string{"socks": "SOCKS5", "http": "HTTP"}[kind]
	u, _, host, port, err := parseURL(raw, label)
	if err != nil {
		return Upstream{}, "", err
	}
	up := Upstream{Type: kind, Server: host, Port: port}
	if u.User != nil {
		user := u.User.Username()
		pass, hasPass := u.User.Password()
		if !hasPass {
			if decoded, err := decodeBase64Loose(user); err == nil && strings.Contains(string(decoded), ":") {
				user, pass, _ = strings.Cut(string(decoded), ":")
			}
		}
		up.Username, up.Password = user, pass
	}
	if kind == "http" && strings.EqualFold(u.Scheme, "https") {
		up.Security = "tls"
		t, err := tlsFromQuery(u.Query(), "sni", "peer")
		if err != nil {
			return Upstream{}, "", err
		}
		up.TLS = t
	}
	return up, name, nil
}

func validHostname(h string) bool {
	return len(h) <= 253 && hostnameRe.MatchString(h) && strings.Contains(strings.TrimSuffix(h, "."), ".")
}

func validSecret(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\r\n\x00")
}

func (t *TLS) validate() error {
	if t == nil {
		return nil
	}
	if t.ServerName != "" {
		if _, err := netip.ParseAddr(t.ServerName); err != nil && !validHostname(t.ServerName) {
			return errors.New("TLS 的 SNI（sni）格式无效")
		}
	}
	if len(t.ALPN) > 3 {
		return errors.New("TLS 的 ALPN 无效")
	}
	for _, a := range t.ALPN {
		if !alpnValues[a] {
			return errors.New("TLS 的 ALPN 只支持 h2、http/1.1、h3")
		}
	}
	if t.Fingerprint != "" && !fingerprints[t.Fingerprint] {
		return errors.New("TLS 指纹（fp）无效")
	}
	return nil
}

// validateFields applies the same rules as the agent so the panel rejects a
// link before anything is created.
func (u Upstream) validateFields() error {
	if a, err := netip.ParseAddr(u.Server); err == nil {
		if !a.IsValid() {
			return errors.New("节点地址无效")
		}
	} else if !validHostname(u.Server) {
		return errors.New("节点地址无效（需要公网 IP 或带点的域名）")
	}
	if u.Port < 1 || u.Port > 65535 {
		return errors.New("节点端口无效（1-65535）")
	}
	if len(u.Username) > 255 || len(u.Password) > 256 || strings.ContainsAny(u.Username+u.Password+u.Obfs, "\r\n\x00") || len(u.Obfs) > 256 {
		return errors.New("节点用户名或密码过长或包含非法字符")
	}
	transport := func() error {
		if err := u.TLS.validate(); err != nil {
			return err
		}
		switch u.Network {
		case "tcp":
		case "ws":
			if u.WS == nil || !wsPathRe.MatchString(u.WS.Path) {
				return errors.New("WebSocket 路径（path）无效")
			}
			if u.WS.Host != "" && !validHostname(u.WS.Host) {
				return errors.New("WebSocket Host 无效")
			}
		case "grpc":
			if u.GRPC == nil || !grpcNameRe.MatchString(u.GRPC.ServiceName) {
				return errors.New("gRPC 的 serviceName 无效")
			}
		}
		return nil
	}
	switch u.Type {
	case "shadowsocks":
		if size, ok := ss2022Keys[u.Method]; ok {
			parts := strings.Split(u.Password, ":")
			if len(parts) > 2 {
				return errors.New("SS2022 密码格式无效")
			}
			for _, p := range parts {
				b, err := base64.StdEncoding.DecodeString(p)
				if err != nil || len(b) != size {
					return errors.New("SS2022 密钥长度与加密方式不符")
				}
			}
			return nil
		}
		if !ssAEAD[u.Method] {
			return errors.New("不支持该 Shadowsocks 加密方式 " + u.Method + "，只支持 AEAD（aes-128-gcm、aes-256-gcm、chacha20-ietf-poly1305）和 2022 系列")
		}
		if !validSecret(u.Password, 256) {
			return errors.New("Shadowsocks 密码无效")
		}
	case "vless":
		if _, err := uuid.Parse(u.UUID); err != nil {
			return errors.New("VLESS 的 UUID 无效")
		}
		if u.Flow != "" && u.Flow != "xtls-rprx-vision" {
			return errors.New("VLESS 只支持 flow=xtls-rprx-vision")
		}
		if u.Flow != "" && (u.Network != "tcp" || u.Security == "none") {
			return errors.New("xtls-rprx-vision 只能用于 TCP + TLS/Reality")
		}
		if u.Security == "reality" {
			r := u.Reality
			if u.Network != "tcp" && u.Network != "grpc" {
				return errors.New("Reality 只支持 TCP 或 gRPC 传输")
			}
			if r == nil || !validHostname(r.ServerName) {
				return errors.New("Reality 链接缺少有效的 sni")
			}
			if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(r.PublicKey, "=")); err != nil || len(b) != 32 {
				return errors.New("Reality 公钥（pbk）无效")
			}
			r.PublicKey = strings.TrimRight(r.PublicKey, "=")
			if !shortIDRe.MatchString(r.ShortID) || len(r.ShortID)%2 != 0 {
				return errors.New("Reality shortId（sid）无效")
			}
			if r.Fingerprint != "" && !realityFPs[r.Fingerprint] {
				return errors.New("Reality 指纹（fp）无效")
			}
		}
		return transport()
	case "vmess":
		if _, err := uuid.Parse(u.UUID); err != nil {
			return errors.New("VMess 的 UUID 无效")
		}
		if !vmessCiphers[u.Cipher] {
			return errors.New("VMess 加密方式（scy）无效")
		}
		return transport()
	case "trojan":
		if !validSecret(u.Password, 256) {
			return errors.New("Trojan 密码无效")
		}
		return transport()
	case "hysteria2":
		if !validSecret(u.Password, 256) {
			return errors.New("Hysteria2 认证密码无效")
		}
		return u.TLS.validate()
	case "socks", "http":
		return u.TLS.validate()
	default:
		return errors.New("不支持的节点类型")
	}
	return nil
}
