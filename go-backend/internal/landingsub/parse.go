// Package landingsub turns a user's subscription (plan 177 「从订阅导入」) into
// candidate external nodes: base64 or plain share-link lists and Clash /
// Mihomo YAML `proxies:`. Every entry goes through the plan-108 link parser
// (linechain.ParseLink), so a subscription can never add a node a pasted link
// could not. Info pseudo-nodes (剩余流量 / 到期 / 官网 …, placeholder
// addresses) are dropped. It performs no network access; fetch.go holds the
// SSRF-guarded one-shot download.
package landingsub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-backend/internal/linechain"

	"gopkg.in/yaml.v3"
)

// MaxEntries bounds one subscription (lines or proxies considered).
const MaxEntries = 500

// Entry is one candidate node of a subscription.
type Entry struct {
	Name     string
	Upstream linechain.Upstream
	// Err: the node cannot be used (unsupported protocol / field), shown
	// greyed with this reason.
	Err string
}

// Result is a parsed subscription.
type Result struct {
	Entries []Entry
	// Info: pseudo-nodes dropped (剩余流量 / 到期 / 官网 / placeholder hosts).
	Info int
	// Invalid: lines that were not node links at all (or broken YAML items).
	Invalid int
	// Truncated: more than MaxEntries items; the rest were ignored.
	Truncated bool
}

// infoName matches provider info entries; the words come from mmwx's default
// node-name filter (剩余|流量|到期|订阅|时间|重置) plus 官网 / 套餐 and friends.
var infoName = regexp.MustCompile(`(?i)剩余|流量|到期|过期|有效期|订阅|时间|重置|官网|网址|套餐|客服|公告|频道|发布|更新|expire|remaining|traffic|reset|官方`)

// placeholderHosts are addresses providers use for info entries.
var placeholderHosts = map[string]bool{
	"localhost": true, "example.com": true, "www.example.com": true, "google.com": true, "www.google.com": true,
	"baidu.com": true, "www.baidu.com": true, "1.1.1.1": true, "1.0.0.1": true, "8.8.8.8": true, "8.8.4.4": true,
	"0.0.0.0": true, "127.0.0.1": true, "::1": true, "::": true,
}

// IsInfoNode reports provider pseudo-nodes (names like 剩余流量 / 到期时间,
// or placeholder / unusable addresses).
func IsInfoNode(name, host string, port int) bool {
	if infoName.MatchString(name) {
		return true
	}
	h := strings.ToLower(strings.Trim(host, "[]"))
	if placeholderHosts[h] || port <= 1 {
		return true
	}
	if a, err := netip.ParseAddr(h); err == nil && linechain.BlockedAddr(a) {
		return true
	}
	return false
}

// Parse reads a subscription body (any of the three formats).
func Parse(body []byte) (*Result, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("订阅内容为空")
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}
	if looksLikeClash(text) {
		return parseClash(text)
	}
	if !strings.Contains(text, "://") {
		if decoded, ok := decodeBase64Body(text); ok {
			text = decoded
		}
	}
	if !strings.Contains(text, "://") {
		if strings.HasPrefix(strings.ToLower(text), "<!doctype") || strings.HasPrefix(strings.ToLower(text), "<html") {
			return nil, errors.New("订阅地址返回的是网页，不是节点列表，请检查订阅地址")
		}
		return nil, errors.New("无法识别订阅内容（支持 Base64 / 节点链接列表 / Clash YAML）")
	}
	return parseLines(text), nil
}

func looksLikeClash(text string) bool {
	for _, line := range strings.SplitN(text, "\n", 400) {
		if strings.HasPrefix(strings.TrimRight(line, "\r \t"), "proxies:") {
			return true
		}
	}
	return false
}

func decodeBase64Body(text string) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(compact); err == nil && strings.Contains(string(b), "://") {
			return strings.ToValidUTF8(string(b), ""), true
		}
	}
	return "", false
}

func parseLines(text string) *Result {
	res := &Result{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.Contains(line, "://") {
			res.Invalid++
			continue
		}
		if len(res.Entries) >= MaxEntries {
			res.Truncated = true
			break
		}
		res.add(line, "")
	}
	return res
}

// add parses one share link; fallbackName names entries whose link has none.
func (res *Result) add(link, fallbackName string) {
	up, name, err := linechain.ParseLink(link)
	if name == "" {
		name = fallbackName
	}
	if err != nil {
		host, port := roughHostPort(link)
		if name == "" {
			name = linkName(link)
		}
		if IsInfoNode(name, host, port) {
			res.Info++
			return
		}
		if name == "" {
			name = schemeOf(link) + " 节点"
		}
		res.Entries = append(res.Entries, Entry{Name: CleanName(name), Upstream: linechain.Upstream{Type: schemeOf(link), Server: host, Port: port}, Err: err.Error()})
		return
	}
	if IsInfoNode(name, up.Server, up.Port) {
		res.Info++
		return
	}
	if name == "" {
		name = up.ProtocolLabel() + " " + up.Address()
	}
	res.Entries = append(res.Entries, Entry{Name: CleanName(name), Upstream: up})
}

// CleanName makes a display name: valid UTF-8, no control characters, at most
// 40 runes.
func CleanName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 40 {
		name = string([]rune(name)[:40])
	}
	return name
}

func schemeOf(link string) string {
	s, _, _ := strings.Cut(link, "://")
	return strings.ToLower(s)
}

func linkName(link string) string {
	if i := strings.LastIndex(link, "#"); i >= 0 {
		if n, err := url.PathUnescape(link[i+1:]); err == nil {
			return n
		}
		return link[i+1:]
	}
	return ""
}

// roughHostPort extracts host:port of a link the parser refused (for the
// info filter and the greyed row only).
func roughHostPort(link string) (string, int) {
	_, rest, ok := strings.Cut(link, "://")
	if !ok {
		return "", 0
	}
	if i := strings.IndexAny(rest, "#?/"); i >= 0 {
		rest = rest[:i]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return strings.Trim(rest, "[]"), 0
	}
	port, _ := strconv.Atoi(rest[i+1:])
	host := strings.Trim(rest[:i], "[]")
	if len(host) > 253 {
		host = host[:253]
	}
	return host, port
}

// ---------------------------------------------------------------- Clash

type clashDoc struct {
	Proxies []map[string]interface{} `yaml:"proxies"`
}

func parseClash(text string) (*Result, error) {
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, errors.New("Clash 订阅内容格式错误，无法解析")
	}
	if len(doc.Proxies) == 0 {
		return nil, errors.New("Clash 订阅里没有节点（proxies 为空）")
	}
	res := &Result{}
	for _, p := range doc.Proxies {
		if len(res.Entries) >= MaxEntries {
			res.Truncated = true
			break
		}
		name := CleanName(str(p["name"]))
		host, port := str(p["server"]), num(p["port"])
		if IsInfoNode(name, host, port) {
			res.Info++
			continue
		}
		link, err := clashLink(p)
		if err != nil {
			if name == "" {
				name = str(p["type"]) + " 节点"
			}
			res.Entries = append(res.Entries, Entry{Name: name, Upstream: linechain.Upstream{Type: strings.ToLower(str(p["type"])), Server: host, Port: port}, Err: err.Error()})
			continue
		}
		res.add(link, name)
	}
	return res, nil
}

func str(v interface{}) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func num(v interface{}) int {
	n, _ := strconv.Atoi(str(v))
	return n
}

func flag(v interface{}) bool {
	s := strings.ToLower(str(v))
	return s == "true" || s == "1"
}

func sub(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func hostPort(p map[string]interface{}) string {
	host := str(p["server"])
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(num(p["port"]))
}

func alpnOf(p map[string]interface{}) string {
	if list, ok := p["alpn"].([]interface{}); ok {
		var out []string
		for _, a := range list {
			out = append(out, str(a))
		}
		return strings.Join(out, ",")
	}
	return str(p["alpn"])
}

// transport writes Clash network options as share-link query parameters.
func transport(p map[string]interface{}, q url.Values) error {
	network := strings.ToLower(str(p["network"]))
	switch network {
	case "", "tcp":
		q.Set("type", "tcp")
	case "ws":
		q.Set("type", "ws")
		ws := sub(p["ws-opts"])
		if path := str(ws["path"]); path != "" {
			q.Set("path", path)
		}
		if h := str(sub(ws["headers"])["Host"]); h != "" {
			q.Set("host", h)
		}
	case "grpc":
		q.Set("type", "grpc")
		q.Set("serviceName", str(sub(p["grpc-opts"])["grpc-service-name"]))
	default:
		return fmt.Errorf("暂不支持 %s 传输，只支持 TCP、WebSocket、gRPC", network)
	}
	return nil
}

// clashLink converts a Clash proxy into the equivalent share link, so the
// one link parser validates both formats the same way.
func clashLink(p map[string]interface{}) (string, error) {
	typ := strings.ToLower(str(p["type"]))
	name := url.PathEscape(str(p["name"]))
	q := url.Values{}
	insecure := flag(p["skip-cert-verify"])
	if insecure {
		q.Set("allowInsecure", "1")
	}
	if fp := str(p["client-fingerprint"]); fp != "" {
		q.Set("fp", fp)
	}
	if a := alpnOf(p); a != "" {
		q.Set("alpn", a)
	}
	switch typ {
	case "ss":
		if str(p["plugin"]) != "" {
			return "", errors.New("不支持带插件（plugin）的 Shadowsocks 节点")
		}
		cred := base64.RawURLEncoding.EncodeToString([]byte(str(p["cipher"]) + ":" + str(p["password"])))
		return "ss://" + cred + "@" + hostPort(p) + "#" + name, nil
	case "vless":
		if err := transport(p, q); err != nil {
			return "", err
		}
		if f := str(p["flow"]); f != "" {
			q.Set("flow", f)
		}
		reality := sub(p["reality-opts"])
		switch {
		case str(reality["public-key"]) != "":
			q.Set("security", "reality")
			q.Set("pbk", str(reality["public-key"]))
			q.Set("sid", str(reality["short-id"]))
			q.Set("sni", str(p["servername"]))
		case flag(p["tls"]):
			q.Set("security", "tls")
			if sni := str(p["servername"]); sni != "" {
				q.Set("sni", sni)
			}
		default:
			q.Set("security", "none")
		}
		return "vless://" + url.PathEscape(str(p["uuid"])) + "@" + hostPort(p) + "?" + q.Encode() + "#" + name, nil
	case "trojan":
		if err := transport(p, q); err != nil {
			return "", err
		}
		if sni := str(p["sni"]); sni != "" {
			q.Set("sni", sni)
		}
		return "trojan://" + url.PathEscape(str(p["password"])) + "@" + hostPort(p) + "?" + q.Encode() + "#" + name, nil
	case "vmess":
		if insecure {
			return "", errors.New("不支持跳过证书验证（skip-cert-verify）的节点")
		}
		j := map[string]interface{}{"v": "2", "ps": str(p["name"]), "add": str(p["server"]), "port": num(p["port"]), "id": str(p["uuid"]), "aid": num(p["alterId"]), "scy": str(p["cipher"]), "net": strings.ToLower(str(p["network"])), "type": "none"}
		if flag(p["tls"]) {
			j["tls"] = "tls"
			j["sni"] = str(p["servername"])
			j["alpn"] = alpnOf(p)
			j["fp"] = str(p["client-fingerprint"])
		}
		switch j["net"] {
		case "ws":
			ws := sub(p["ws-opts"])
			j["path"] = str(ws["path"])
			j["host"] = str(sub(ws["headers"])["Host"])
		case "grpc":
			j["path"] = str(sub(p["grpc-opts"])["grpc-service-name"])
		case "", "tcp":
			j["net"] = "tcp"
		default:
			return "", fmt.Errorf("暂不支持 %s 传输，只支持 TCP、WebSocket、gRPC", j["net"])
		}
		b, _ := json.Marshal(j)
		return "vmess://" + base64.StdEncoding.EncodeToString(b), nil
	case "hysteria2", "hy2":
		if str(p["ports"]) != "" {
			return "", errors.New("暂不支持端口跳跃（多端口）节点，请使用单个端口")
		}
		if sni := str(p["sni"]); sni != "" {
			q.Set("sni", sni)
		}
		if o := strings.ToLower(str(p["obfs"])); o != "" {
			q.Set("obfs", o)
			q.Set("obfs-password", str(p["obfs-password"]))
		}
		auth := str(p["password"])
		if auth == "" {
			auth = str(p["auth"])
		}
		return "hysteria2://" + url.PathEscape(auth) + "@" + hostPort(p) + "?" + q.Encode() + "#" + name, nil
	case "socks5", "socks":
		if flag(p["tls"]) {
			return "", errors.New("暂不支持 TLS 加密的 SOCKS5 节点")
		}
		return "socks5://" + userinfo(p) + hostPort(p) + "#" + name, nil
	case "http":
		scheme := "http"
		if flag(p["tls"]) {
			scheme = "https"
			if sni := str(p["sni"]); sni != "" {
				q.Set("sni", sni)
			}
		}
		link := scheme + "://" + userinfo(p) + hostPort(p)
		if scheme == "https" && len(q) > 0 {
			link += "/?" + q.Encode()
		}
		return link + "#" + name, nil
	case "tuic":
		return "", errors.New("暂不支持 TUIC 节点：落地程序没有 TUIC 出站")
	case "hysteria":
		return "", errors.New("不支持 Hysteria 1 节点，请改用 Hysteria2")
	case "ssr":
		return "", errors.New("不支持 ShadowsocksR 节点")
	case "":
		return "", errors.New("节点缺少类型")
	}
	return "", fmt.Errorf("暂不支持 %s 节点", typ)
}

func userinfo(p map[string]interface{}) string {
	user, pass := str(p["username"]), str(p["password"])
	if user == "" && pass == "" {
		return ""
	}
	return url.UserPassword(user, pass).String() + "@"
}
