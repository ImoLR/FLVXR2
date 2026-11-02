package sublink

import (
	"encoding/hex"
	"strings"
)

// Plan 191 helpers shared by the line-based formats (Surge / Surfboard, Loon,
// Quantumult X): one proxy per line, comma separated parameters.

// lineReserved are built-in policy names of these clients (compared upper
// case) and the two group names; a node may not take them.
var lineReserved = map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-TINYGIF": true, "REJECT-DROP": true,
	"REJECT-NO-DROP": true, "PROXY": true, GroupSelect: true, GroupAuto: true}

// lineName makes a node name safe for a line format: "," and "=" end a field
// in every format and become their full-width forms; for Surge / Loon (INI
// style) "#", ";", "//" (inline comments) and '"' (quoted values) are
// replaced too. Control characters become spaces.
func lineName(name string, qx bool) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteRune(' ')
		case r == ',':
			b.WriteRune('，')
		case r == '=':
			b.WriteRune('＝')
		case !qx && r == '#':
			b.WriteRune('＃')
		case !qx && r == ';':
			b.WriteRune('；')
		case !qx && r == '"':
			b.WriteRune('＂')
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if !qx {
		s = strings.ReplaceAll(s, "//", "／／")
	}
	return strings.TrimSpace(s)
}

// lineNames returns the line-safe names of nodes (already unique from
// FinalizeNames), unique again after the replacement and never a built-in
// policy name.
func lineNames(nodes []Node, qx bool) []string {
	out := make([]string, len(nodes))
	used := map[string]bool{}
	for i, n := range nodes {
		name := lineName(n.Name, qx)
		if name == "" {
			name = lineName(n.ProtocolLabel()+" "+n.Server(), qx)
		}
		if lineReserved[strings.ToUpper(name)] {
			name += " · 节点"
		}
		for used[name] {
			name += "'"
		}
		used[name] = true
		out[i] = name
	}
	return out
}

// quoted wraps a value in double quotes; ok=false when it cannot be written
// safely in any client version (quote, backslash, control characters).
func quoted(s string) (string, bool) {
	for _, r := range s {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return `"` + s + `"`, true
}

// plain accepts a value written without quotes: non-empty, no separators,
// quotes, backslashes, whitespace or control characters.
func plain(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == ',' || r == '"' || r == '\\' || r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// hexPin is the first certificate pin as 64 lower-case hex digits ("" when
// there is none or it is malformed).
func hexPin(p string) string {
	p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(firstPin(p)), ":", ""))
	if b, err := hex.DecodeString(p); err != nil || len(b) != 32 {
		return ""
	}
	return p
}

// pathValue is a WebSocket path for a comma separated line: quoted when it
// contains a comma (ok=false when even that is impossible).
func pathValue(p string) (string, bool) {
	if p == "" {
		return "/", true
	}
	if strings.Contains(p, ",") {
		return quoted(p)
	}
	if strings.ContainsAny(p, "\"\\ \t\r\n") {
		return "", false
	}
	return p, true
}

// ssLineMethod: Shadowsocks ciphers all four clients document (the 2022
// ChaCha20 variant is not supported by any of them).
var ssLineMethod = map[string]bool{"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true}

// emptyNodeList is the body of a Loon / Quantumult X node list without a
// single node the client supports: one comment line (ignored by both).
func emptyNodeList(client string) []byte {
	return []byte("# FLVX: 这条订阅里没有 " + client + " 支持的节点\n")
}
