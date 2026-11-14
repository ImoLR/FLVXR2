package sublink

import (
	"strconv"
	"strings"
)

// surgeProxy converts one node to the part after "Name = " of a Surge
// [Proxy] line; ok=false when Surge (Surfboard with sb) cannot express it.
// Supported per the official manuals (manual.nssurge.com, getsurfboard.com):
// ss / ss2022 (AES), vmess (tcp / ws), trojan (tcp / ws), hysteria2, socks5,
// http(s), AnyTLS, Snell. Never: VLESS, Mieru, gRPC transports.
func surgeProxy(n Node, sb bool) (string, bool) {
	var p []string
	kv := func(k, v string) { p = append(p, k+"="+v) }
	// Secrets are quoted; a comma inside quotes is not documented for Surge,
	// so such a node is left out (Loon documents it, Quantumult X cannot).
	secret := func(k, v string) bool {
		q, ok := quoted(v)
		if ok && !strings.Contains(v, ",") {
			kv(k, q)
			return true
		}
		return false
	}
	tlsParams := func(sni, pin string, insecure bool) {
		if sni != "" {
			kv("sni", sni)
		}
		if h := hexPin(pin); h != "" {
			kv("server-cert-fingerprint-sha256", h)
		} else if insecure {
			kv("skip-cert-verify", "true")
		}
	}
	head := func(t string) {
		p = append(p, t, strings.Trim(n.Server(), "[]"), strconv.Itoa(n.Port()))
	}
	if e := n.Extra; e != nil {
		switch e.Type {
		case "anytls":
			head("anytls")
			if !secret("password", e.Password) {
				return "", false
			}
			tlsParams(e.SNI, e.CertSHA256, true)
		case "snell":
			v := e.Version
			if v == 0 {
				v = 4
			}
			// Surge: Snell v4 / v5 only know HTTP obfuscation.
			if e.Obfs == "tls" && v >= 4 && !sb {
				return "", false
			}
			head("snell")
			if !secret("psk", e.Password) {
				return "", false
			}
			kv("version", strconv.Itoa(v))
			if e.Obfs == "http" || e.Obfs == "tls" {
				kv("obfs", e.Obfs)
				if e.ObfsHost != "" {
					kv("obfs-host", e.ObfsHost)
				}
			}
			if sb && e.UDP {
				kv("udp-relay", "true")
			}
		default:
			return "", false
		}
		return strings.Join(p, ", "), true
	}
	u := n.Up
	sni, pin := "", ""
	if u.TLS != nil {
		sni, pin = u.TLS.ServerName, u.TLS.PinnedSHA256
	}
	ws := func() bool {
		switch u.Network {
		case "", "tcp":
			return true
		case "ws":
			kv("ws", "true")
			path, host := "/", ""
			if u.WS != nil {
				path, host = u.WS.Path, u.WS.Host
			}
			if !plain(path) {
				return false
			}
			kv("ws-path", path)
			if host != "" {
				kv("ws-headers", "Host:"+host)
			}
			return true
		}
		return false
	}
	switch u.Type {
	case "shadowsocks":
		if !ssLineMethod[u.Method] {
			return "", false
		}
		head("ss")
		kv("encrypt-method", u.Method)
		if !secret("password", u.Password) {
			return "", false
		}
		kv("udp-relay", "true")
	case "vmess":
		head("vmess")
		kv("username", u.UUID)
		if !ws() {
			return "", false
		}
		if u.Security == "tls" {
			kv("tls", "true")
			tlsParams(sni, pin, n.Insecure)
		}
		kv("vmess-aead", "true")
		if sb {
			kv("udp-relay", "true")
		}
	case "trojan":
		if u.Security == "none" {
			return "", false
		}
		head("trojan")
		if sb {
			// Surfboard reads the Trojan password unquoted.
			if !plain(u.Password) {
				return "", false
			}
			kv("password", u.Password)
		} else if !secret("password", u.Password) {
			return "", false
		}
		if !ws() {
			return "", false
		}
		tlsParams(sni, pin, n.Insecure)
		if sb {
			kv("udp-relay", "true")
		}
	case "hysteria2":
		// Surge iOS has no Salamander obfuscation (Mac 6.4.3+ only).
		if u.Obfs != "" && !sb {
			return "", false
		}
		head("hysteria2")
		if !secret("password", u.Password) {
			return "", false
		}
		tlsParams(sni, pin, n.Insecure)
		if u.Obfs != "" {
			if !secret("salamander-password", u.Obfs) {
				return "", false
			}
		}
	case "socks", "http":
		t := map[string]string{"socks": "socks5", "http": "http"}[u.Type]
		if u.Security == "tls" {
			t = map[string]string{"socks": "socks5-tls", "http": "https"}[u.Type]
		}
		head(t)
		if u.Username != "" || u.Password != "" {
			if sb {
				// Surfboard: positional username, password.
				if !plain(u.Username) || !plain(u.Password) {
					return "", false
				}
				p = append(p, u.Username, u.Password)
			} else if u.Username != "" && !secret("username", u.Username) || u.Password != "" && !secret("password", u.Password) {
				return "", false
			}
		}
		if u.Security == "tls" {
			tlsParams(sni, pin, n.Insecure)
		}
		if u.Type == "socks" {
			kv("udp-relay", "true")
		}
	default:
		return "", false
	}
	return strings.Join(p, ", "), true
}

// lanRules: LAN / private destinations go DIRECT (the Clash list).
func lanRules(noResolve bool) []string {
	out := make([]string, 0, len(privateCIDRs)+1)
	for _, c := range privateCIDRs {
		if !noResolve {
			c = strings.TrimSuffix(c, ",no-resolve")
		}
		out = append(out, c)
	}
	return append(out, "DOMAIN-SUFFIX,local,DIRECT")
}

// SurgeManagedLine is the first line of a Surge / Surfboard profile: the
// client re-downloads the profile from url (twice a day, keeps the old one
// when an update fails).
func SurgeManagedLine(url string) string {
	return "#!MANAGED-CONFIG " + url + " interval=43200 strict=false\n"
}

// Surge renders a full Surge profile without the #!MANAGED-CONFIG line (the
// handler prepends it, it depends on the request URL). surfboard limits it
// to what Surfboard documents: no loglevel / skip-proxy, no no-resolve /
// dns-failed rule options. Rules are built in only (GEOIP, no rule sets).
// It returns the profile and how many nodes it contains.
func Surge(nodes []Node, mode string, surfboard bool) ([]byte, int) {
	return SurgeRouted(nodes, mode, surfboard, nil, "")
}

// SurgeRouted is Surge with a plan-256 「分流」 list: one select group per
// entry, custom lines inline and the catalog rule sets as RULE-SET lists the
// panel at origin serves (no origin: catalog entries get no rules). An empty
// list renders exactly what Surge renders.
func SurgeRouted(nodes []Node, mode string, surfboard bool, routing []RouteEntry, origin string) ([]byte, int) {
	kept := make([]Node, 0, len(nodes))
	bodies := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := surgeProxy(n, surfboard); ok {
			kept = append(kept, n)
			bodies = append(bodies, s)
		}
	}
	names := lineNames(kept, false)
	var routes []routeGroup
	if len(routing) > 0 {
		taken := map[string]bool{}
		for _, g := range routeGroupNames(routing) {
			taken[lineName(g, false)] = true
		}
		names = avoidLineNames(names, taken)
		routes = planRoutes(routing, routeNodeNames(kept, names), len(kept) > 0)
		for i := range routes {
			routes[i].name = lineName(routes[i].name, false)
		}
	}
	var b strings.Builder
	b.WriteString("[General]\n")
	if !surfboard {
		b.WriteString("loglevel = notify\n")
		b.WriteString("skip-proxy = 127.0.0.1, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, 100.64.0.0/10, localhost, *.local\n")
	}
	b.WriteString("dns-server = system, 223.5.5.5, 119.29.29.29\n")
	if !surfboard {
		// Plan 220 (as mmwx's Surge profile): IPv6 off like the other
		// formats; UDP on a policy without UDP relay (HTTP / no-UDP nodes)
		// is rejected so QUIC falls back to TCP at once instead of going
		// DIRECT from the mainland and stalling (Surge iOS defaults to
		// DIRECT); apps asking 8.8.8.8 / 8.8.4.4 directly get Surge's own
		// DNS; network checks / STUN / consoles keep their real IP.
		b.WriteString("ipv6 = false\n")
		b.WriteString("hijack-dns = 8.8.8.8:53, 8.8.4.4:53\n")
		b.WriteString("udp-policy-not-supported-behaviour = REJECT\n")
		b.WriteString("always-real-ip = *.msftncsi.com, *.msftconnecttest.com, *.srv.nintendo.net, *.stun.playstation.net, xbox.*.microsoft.com, *.xboxlive.com, stun.*\n")
	}
	b.WriteString("proxy-test-url = " + TestURL + "\n")
	b.WriteString("\n[Proxy]\n")
	for i := range kept {
		b.WriteString(names[i] + " = " + bodies[i] + "\n")
	}
	b.WriteString("\n[Proxy Group]\n")
	if len(kept) == 0 {
		b.WriteString(GroupSelect + " = select, DIRECT\n")
	} else {
		list := strings.Join(names, ", ")
		b.WriteString(GroupSelect + " = select, " + GroupAuto + ", " + list + ", DIRECT\n")
		b.WriteString(GroupAuto + " = url-test, " + list + ", url=" + TestURL + ", interval=300, tolerance=50\n")
	}
	auto := ""
	if len(kept) > 0 {
		auto = GroupAuto
	}
	for _, g := range routes {
		b.WriteString(g.name + " = select, " + strings.Join(groupMembers(g, GroupSelect, auto, "DIRECT", "REJECT", names), ", ") + "\n")
	}
	b.WriteString("\n[Rule]\n")
	for _, r := range lanRules(!surfboard) {
		b.WriteString(r + "\n")
	}
	for _, r := range surgeRouteRules(routes, surfboard, origin) {
		b.WriteString(r + "\n")
	}
	if mode != ModeGlobal {
		b.WriteString("GEOIP,CN,DIRECT\n")
	}
	if surfboard {
		b.WriteString("FINAL," + GroupSelect + "\n")
	} else {
		b.WriteString("FINAL," + GroupSelect + ",dns-failed\n")
	}
	return []byte(b.String()), len(kept)
}

// SurgeLine is one node as a complete Surge [Proxy] line ("Name = …"),
// exactly as the Surge profile renders it; ok=false when Surge cannot
// express the node. Used by the 复制链接 dialogs (plan 250).
func SurgeLine(n Node) (string, bool) {
	body, ok := surgeProxy(n, false)
	if !ok {
		return "", false
	}
	name := lineName(n.Name, false)
	if name == "" {
		name = lineName(n.ProtocolLabel()+" "+n.Server(), false)
	}
	return name + " = " + body, true
}

// surgeRouteRules: the 「分流」 rules (Surfboard documents no no-resolve).
func surgeRouteRules(routes []routeGroup, surfboard bool, origin string) []string {
	nr := ",no-resolve"
	if surfboard {
		nr = ""
	}
	var out []string
	for _, g := range routes {
		for _, r := range g.custom {
			switch r.Kind {
			case "suffix":
				out = append(out, "DOMAIN-SUFFIX,"+r.Value+","+g.name)
			case "full":
				out = append(out, "DOMAIN,"+r.Value+","+g.name)
			case "keyword":
				out = append(out, "DOMAIN-KEYWORD,"+r.Value+","+g.name)
			case "ip4":
				out = append(out, "IP-CIDR,"+r.Value+","+g.name+nr)
			case "ip6":
				out = append(out, "IP-CIDR6,"+r.Value+","+g.name+nr)
			}
		}
		if origin == "" {
			continue
		}
		for _, s := range g.sets {
			r := "RULE-SET," + RuleSetFileURL(origin, s.ID, ExtList) + "," + g.name
			if s.IP {
				r += nr
			}
			out = append(out, r)
		}
	}
	return out
}

// avoidLineNames renames node names that are taken by a group name.
func avoidLineNames(names []string, taken map[string]bool) []string {
	hit := false
	for _, n := range names {
		if taken[n] {
			hit = true
			break
		}
	}
	if !hit {
		return names
	}
	out := append([]string(nil), names...)
	used := map[string]bool{}
	for _, n := range out {
		used[n] = true
	}
	for i, n := range out {
		if !taken[n] {
			continue
		}
		name := n + " · 节点"
		for used[name] || taken[name] {
			name += "'"
		}
		used[name] = true
		out[i] = name
	}
	return out
}
