package landingegress

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func splitHostPortLink(hostport string) (string, int, error) {
	hostport = strings.TrimSuffix(hostport, "/")
	i := strings.LastIndex(hostport, ":")
	if i <= 0 {
		return "", 0, errors.New("缺少端口")
	}
	port, err := strconv.Atoi(hostport[i+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("端口无效")
	}
	return NormalizeHost(hostport[:i]), port, nil
}

// ParseLink converts an ss:// (SIP002/SIP022 or legacy) or vless:// (Reality)
// share link into proxy settings. The result still goes through
// NormalizeOutbound before it is saved.
func ParseLink(raw string) (Settings, string, error) {
	raw = strings.TrimSpace(raw)
	name := ""
	if i := strings.Index(raw, "#"); i >= 0 {
		name, _ = url.PathUnescape(raw[i+1:])
		raw = raw[:i]
	}
	switch {
	case strings.HasPrefix(raw, "ss://"):
		body := strings.TrimPrefix(raw, "ss://")
		if i := strings.IndexAny(body, "?"); i >= 0 {
			query, _ := url.ParseQuery(body[i+1:])
			if query.Get("plugin") != "" {
				return Settings{}, "", errors.New("不支持带插件的 Shadowsocks 链接")
			}
			body = body[:i]
		}
		var userinfo, hostport string
		if at := strings.LastIndex(body, "@"); at >= 0 {
			userinfo, hostport = body[:at], body[at+1:]
			if unescaped, err := url.PathUnescape(userinfo); err == nil && strings.Contains(unescaped, ":") {
				userinfo = unescaped
			} else if decoded, err := decodeBase64Loose(userinfo); err == nil {
				userinfo = string(decoded)
			} else {
				return Settings{}, "", errors.New("Shadowsocks 链接的加密信息无法解析")
			}
		} else {
			decoded, err := decodeBase64Loose(body)
			if err != nil {
				return Settings{}, "", errors.New("Shadowsocks 链接无法解析")
			}
			at := strings.LastIndex(string(decoded), "@")
			if at < 0 {
				return Settings{}, "", errors.New("Shadowsocks 链接缺少服务器地址")
			}
			userinfo, hostport = string(decoded[:at]), string(decoded[at+1:])
		}
		method, password, ok := strings.Cut(userinfo, ":")
		if !ok || method == "" || password == "" {
			return Settings{}, "", errors.New("Shadowsocks 链接缺少加密方式或密码")
		}
		host, port, err := splitHostPortLink(hostport)
		if err != nil {
			return Settings{}, "", err
		}
		return Settings{Protocol: "shadowsocks", Server: host, Port: port, Method: strings.ToLower(method), Password: password}, name, nil
	case strings.HasPrefix(raw, "vless://"):
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			return Settings{}, "", errors.New("VLESS 链接无法解析")
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return Settings{}, "", errors.New("VLESS 链接端口无效")
		}
		q := u.Query()
		if q.Get("security") != "reality" {
			return Settings{}, "", errors.New("只支持 VLESS TCP + Reality 链接")
		}
		if t := q.Get("type"); t != "" && t != "tcp" && t != "raw" {
			return Settings{}, "", errors.New("只支持 VLESS TCP + Reality 链接")
		}
		if e := q.Get("encryption"); e != "" && e != "none" {
			return Settings{}, "", errors.New("不支持 VLESS Encryption 链接")
		}
		return Settings{Protocol: "vless", Server: NormalizeHost(u.Hostname()), Port: port, UUID: strings.ToLower(u.User.Username()), Flow: q.Get("flow"), RealityServerName: q.Get("sni"), RealityPublicKey: q.Get("pbk"), RealityShortID: q.Get("sid"), RealityFingerprint: q.Get("fp")}, name, nil
	}
	return Settings{}, "", errors.New("只支持 ss:// 和 vless:// 链接")
}
