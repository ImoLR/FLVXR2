package sublink

// Plan 302: entries reached by an IPv6-only (AAAA-only) DDNS domain.
//
// The panel builds such nodes on the record's current IPv6 literal (what the
// domain resolves to, so every client keeps working as with a literal entry)
// and remembers the domain in Node.EntryDomain. Clients that resolve
// AAAA-only proxy names get the domain, so a changed IP needs no refresh:
// mihomo (Clash Meta family; dns.ipv6 false does not apply to proxy-server
// lookups), Stash (dns.ipv6 false only strips app answers) and sing-box
// (strategy prefer_ipv4 falls back to AAAA). NekoBox (IPv6 off by default →
// server names resolve ipv4_only), Surge (ipv6 = false → no AAAA queries)
// and the other clients keep the literal.

// UseEntryDomains returns nodes with every EntryDomain node's server set to
// its domain. The input slice is not modified; without EntryDomain nodes it
// is returned as is. A node whose TLS name / Host header would follow the
// server field (no explicit SNI) keeps the literal, so switching the server
// never changes what goes on the wire besides the address lookup.
func UseEntryDomains(nodes []Node) []Node {
	var out []Node
	for i := range nodes {
		n := nodes[i]
		if n.EntryDomain == "" || !entryNameFixed(n) {
			continue
		}
		if out == nil {
			out = append([]Node(nil), nodes...)
		}
		if n.Extra != nil {
			e := *n.Extra
			e.Server = n.EntryDomain
			out[i].Extra = &e
		} else {
			out[i].Up.Server = n.EntryDomain
		}
		out[i].AAAAOnly = true
	}
	if out == nil {
		return nodes
	}
	return out
}

// entryNameFixed: the node's TLS server name and WebSocket / gRPC host do
// not default to the server address.
func entryNameFixed(n Node) bool {
	if e := n.Extra; e != nil {
		switch e.Type {
		case "anytls":
			return e.SNI != ""
		case "snell":
			return e.Obfs == "" || e.ObfsHost != ""
		}
		return true
	}
	u := n.Up
	switch {
	case u.Reality != nil:
		if u.Reality.ServerName == "" {
			return false
		}
	case u.TLS != nil:
		if u.TLS.ServerName == "" {
			return false
		}
	case u.Security == "tls", u.Security == "reality":
		return false
	default:
		switch u.Type {
		case "trojan", "hysteria2", "hysteria", "tuic", "anytls":
			return false // TLS without options: SNI = server
		}
	}
	switch u.Network {
	case "ws":
		return u.WS != nil && u.WS.Host != ""
	case "grpc":
		return u.Reality != nil || u.TLS != nil && u.TLS.ServerName != ""
	}
	return true
}
