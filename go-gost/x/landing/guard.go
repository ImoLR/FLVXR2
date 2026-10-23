package landing

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// Reality guard (plan 127). REALITY relays every connection whose ClientHello
// it does not authenticate to the node's dest, so anyone can use a landing as
// a free relay to whatever else the dest's IP serves (a CDN edge) by sending
// another SNI. With the guard, dest points at a loopback helper inbound of the
// same Core whose routing only lets the node's own serverNames through to the
// real dest; everything else (other SNI, no SNI, not TLS, no ClientHello
// within the Core's sniffing window) is closed.
//
// Authenticated user data never passes the guard: REALITY only fetches the
// dest's handshake through it (as it fetches it from dest today) and closes
// that connection once the client is authenticated.
//
// The panel assigns one loopback port per (dest, serverNames) group on a
// server, out of the ports it never gives to anything else. The guard is
// fail-open: without the routing API, with a failed rule update or a helper
// that cannot listen, the node keeps its real dest (as before plan 127).

// GuardVersion is reported as landing_guard: this agent applies Node.GuardPort
// and builtin plan rules (rendered before chain rules, never withholding).
// Version 2 (plan 134) adds the global layer markers "forced" (any outbound,
// rendered before chain rules) and "global" (any outbound, kept in place);
// neither withholds a node when the plan cannot be applied.
const GuardVersion = 2

const guardTagPrefix = "flvxg-"

func guardTag(port int) string { return guardTagPrefix + strconv.Itoa(port) }

type guardGroup struct {
	Port     int
	Host     string
	DestPort int
	Names    []string // sorted, lower-case
}

func (g *guardGroup) key() string {
	return net.JoinHostPort(g.Host, strconv.Itoa(g.DestPort)) + "|" + strings.Join(g.Names, ",")
}

// guardOutboundTag is the freedom outbound the guard's allow rules use instead
// of "direct" (plan 137, QA F14). REALITY closes its guard connection as soon
// as it has the dest's handshake, but the outbound's policy decides when the
// dest leg goes: on "direct" (level 0, downlinkOnly 5 s) every new Reality
// connection kept the leg open ~5 s — 2 extra Core sockets, 3 extra /proc/net
// rows (what machine protection counts) and a spliced pipe pair. Level
// guardUserLevel is deliberately not defined in the base config, so the Core
// uses its built-in session (uplinkOnly/downlinkOnly 1 s). Hot-applied through
// the API like the egress outbounds; the Core never restarts for it.
const (
	guardOutboundTag = "flvxg-direct"
	guardUserLevel   = 9
)

func guardOutbound() map[string]any {
	return map[string]any{"tag": guardOutboundTag, "protocol": "freedom",
		"settings": map[string]any{"domainStrategy": "UseIP", "userLevel": guardUserLevel}}
}

// inbound is the loopback helper: TLS sniffing for routing only, so the
// connection always goes to the configured dest, never to the sniffed name.
func (g *guardGroup) inbound() map[string]any {
	return map[string]any{
		"tag": guardTag(g.Port), "listen": "127.0.0.1", "port": g.Port, "protocol": "dokodemo-door",
		"settings": map[string]any{"address": g.Host, "port": g.DestPort, "network": "tcp"},
		"sniffing": map[string]any{"enabled": true, "destOverride": []string{"tls"}, "routeOnly": true},
	}
}

// rules match only the helper inbound and come before the safety rule: the
// destination is fixed (the dest REALITY already dialed directly before), and
// a domain-only match needs no DNS lookup of the sniffed name.
//
// The allow rule also requires the sniffed protocol "tls": when the dest is a
// domain (every production dest, e.g. www.lovelive-anime.jp:443) and sniffing
// finds no SNI (no-SNI ClientHello, not TLS at all), the Core matches the
// domain rule against the dokodemo target itself, which IS an allowed name —
// without this condition such probes were relayed to the real dest.
func (g *guardGroup) rules() []any {
	tag := guardTag(g.Port)
	domains := make([]string, 0, len(g.Names))
	for _, n := range g.Names {
		domains = append(domains, "full:"+n)
	}
	return []any{
		map[string]any{"type": "field", "ruleTag": tag + "-allow", "inboundTag": []string{tag}, "protocol": []string{"tls"}, "domain": domains, "outboundTag": guardOutboundTag},
		map[string]any{"type": "field", "ruleTag": tag + "-block", "inboundTag": []string{tag}, "outboundTag": "block"},
	}
}

// guardGroups returns the valid guard groups of enabled Reality nodes keyed by
// port, plus the node tag → port map. Invalid or inconsistent guard ports are
// ignored (that node keeps its real dest) and reported as warnings.
func (d Desired) guardGroups() (map[int]*guardGroup, map[string]int, []string) {
	groups, byTag := map[int]*guardGroup{}, map[string]int{}
	var warnings []string
	if !d.Enabled {
		return groups, byTag, nil
	}
	nodePorts := map[int]bool{}
	for _, n := range d.Nodes {
		nodePorts[n.Port] = true
	}
	for _, n := range d.Nodes {
		if n.GuardPort == 0 || !n.Enabled {
			continue
		}
		if n.Protocol != "vless_reality_vision" || n.VLESS == nil || n.GuardPort < 1024 || n.GuardPort > 65535 || nodePorts[n.GuardPort] {
			warnings = append(warnings, n.InboundTag+": Reality 防盗端口无效，已按未开启处理")
			continue
		}
		host, portText, err := net.SplitHostPort(n.VLESS.Reality.Dest)
		port, perr := strconv.Atoi(portText)
		if err != nil || perr != nil || host == "" || port < 1 || port > 65535 {
			warnings = append(warnings, n.InboundTag+": Reality dest 无效，防盗未开启")
			continue
		}
		names := make([]string, 0, len(n.VLESS.Reality.ServerNames))
		for _, name := range n.VLESS.Reality.ServerNames {
			names = append(names, strings.ToLower(strings.TrimSpace(name)))
		}
		sort.Strings(names)
		g := &guardGroup{Port: n.GuardPort, Host: host, DestPort: port, Names: names}
		if old, ok := groups[g.Port]; ok && old.key() != g.key() {
			warnings = append(warnings, n.InboundTag+": Reality 防盗端口与其它目标冲突，防盗未开启")
			continue
		} else if !ok {
			groups[g.Port] = g
		}
		byTag[n.InboundTag] = g.Port
	}
	return groups, byTag, warnings
}

// guardRules renders the rules of the given groups in port order.
func guardRules(groups map[int]*guardGroup) []any {
	ports := make([]int, 0, len(groups))
	for p := range groups {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	var out []any
	for _, p := range ports {
		out = append(out, groups[p].rules()...)
	}
	return out
}

// guardDeferAccept (TCP_DEFER_ACCEPT, seconds) makes the kernel hand a guarded
// Reality connection to the Core only once its first bytes (the ClientHello)
// have arrived. REALITY dials dest — the guard — at accept, and the guard has
// the Core's fixed 200 ms sniffing window to see the ClientHello; deferring the
// accept keeps a ClientHello that arrives late (lost first segment, slow
// client) inside that window. Clients that never send are accepted after the
// timeout as before and then closed by REALITY's own handshake timeout.
const guardDeferAccept = "5"

// withGuardDest points a rendered Reality inbound at its guard.
func withGuardDest(cfg map[string]any, port int) map[string]any {
	stream, _ := cfg["streamSettings"].(map[string]any)
	reality, _ := stream["realitySettings"].(map[string]any)
	if reality == nil {
		return cfg
	}
	copyReality := map[string]any{}
	for k, v := range reality {
		copyReality[k] = v
	}
	copyReality["dest"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	copyStream := map[string]any{}
	for k, v := range stream {
		copyStream[k] = v
	}
	copyStream["realitySettings"] = copyReality
	copyStream["sockopt"] = map[string]any{"customSockopt": []any{map[string]any{"system": "linux", "network": "tcp", "level": "6", "opt": "9", "value": guardDeferAccept, "type": "int"}}}
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	out["streamSettings"] = copyStream
	return out
}
