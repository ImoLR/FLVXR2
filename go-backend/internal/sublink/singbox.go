package sublink

import (
	"bytes"
	"encoding/json"
)

// jmap: an ordered JSON object (readable output, stable for tests).
type jmap []kv

func (m jmap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(e.k)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (m *jmap) add(k string, v interface{}) { *m = append(*m, kv{k, v}) }

func (m *jmap) addIf(k string, v interface{}) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return
		}
	case []string:
		if len(x) == 0 {
			return
		}
	case nil:
		return
	}
	m.add(k, v)
}

// singBoxOutbound converts one node; ok=false when sing-box cannot express it
// (Snell, Mieru, certificate-hash pins).
func singBoxOutbound(n Node) (jmap, bool) {
	o := jmap{}
	if e := n.Extra; e != nil {
		if e.Type != "anytls" {
			return nil, false
		}
		o.add("type", "anytls")
		o.add("tag", n.Name)
		o.add("server", e.Server)
		o.add("server_port", e.Port)
		o.add("password", e.Password)
		t := jmap{{"enabled", true}}
		t.addIf("server_name", e.SNI)
		if e.CertPEM != "" {
			t.add("certificate", e.CertPEM)
		} else {
			t.add("insecure", true)
		}
		t.add("utls", jmap{{"enabled", true}, {"fingerprint", "chrome"}})
		o.add("tls", t)
		return o, true
	}
	u := n.Up
	if u.TLS != nil && u.TLS.PinnedSHA256 != "" {
		// sing-box pins public keys, not certificate hashes.
		return nil, false
	}
	base := func(t string) {
		o.add("type", t)
		o.add("tag", n.Name)
		o.add("server", u.Server)
		o.add("server_port", u.Port)
	}
	tlsObj := func() jmap {
		t := jmap{{"enabled", true}}
		if u.TLS != nil {
			t.addIf("server_name", u.TLS.ServerName)
			t.addIf("alpn", u.TLS.ALPN)
		}
		if n.Insecure {
			t.add("insecure", true)
		}
		if u.TLS != nil && u.TLS.Fingerprint != "" {
			t.add("utls", jmap{{"enabled", true}, {"fingerprint", u.TLS.Fingerprint}})
		}
		return t
	}
	transport := func() {
		switch u.Network {
		case "ws":
			t := jmap{{"type", "ws"}}
			if u.WS != nil {
				t.addIf("path", u.WS.Path)
				if u.WS.Host != "" {
					t.add("headers", jmap{{"Host", u.WS.Host}})
				}
			}
			o.add("transport", t)
		case "grpc":
			t := jmap{{"type", "grpc"}}
			if u.GRPC != nil {
				t.addIf("service_name", u.GRPC.ServiceName)
			}
			o.add("transport", t)
		}
	}
	switch u.Type {
	case "shadowsocks":
		base("shadowsocks")
		o.add("method", u.Method)
		o.add("password", u.Password)
	case "vless":
		// Plan 253: sing-box has no VLESS Encryption (1.14.3: unknown field
		// "encryption"), so such nodes are left out.
		if u.Encrypted() {
			return nil, false
		}
		base("vless")
		o.add("uuid", u.UUID)
		o.addIf("flow", u.Flow)
		switch u.Security {
		case "reality":
			if u.Reality == nil {
				return nil, false
			}
			fp := u.Reality.Fingerprint
			if fp == "" {
				fp = "chrome"
			}
			o.add("tls", jmap{
				{"enabled", true},
				{"server_name", u.Reality.ServerName},
				{"utls", jmap{{"enabled", true}, {"fingerprint", fp}}},
				{"reality", jmap{{"enabled", true}, {"public_key", u.Reality.PublicKey}, {"short_id", u.Reality.ShortID}}},
			})
		case "tls":
			o.add("tls", tlsObj())
		}
		transport()
	case "vmess":
		base("vmess")
		o.add("uuid", u.UUID)
		cipher := u.Cipher
		if cipher == "" {
			cipher = "auto"
		}
		o.add("security", cipher)
		o.add("alter_id", 0)
		if u.Security == "tls" {
			o.add("tls", tlsObj())
		}
		transport()
	case "trojan":
		base("trojan")
		o.add("password", u.Password)
		o.add("tls", tlsObj())
		transport()
	case "hysteria2":
		base("hysteria2")
		o.add("password", u.Password)
		if u.Obfs != "" {
			o.add("obfs", jmap{{"type", "salamander"}, {"password", u.Obfs}})
		}
		o.add("tls", tlsObj())
	case "socks":
		base("socks")
		o.add("version", "5")
		o.addIf("username", u.Username)
		o.addIf("password", u.Password)
	case "http":
		base("http")
		o.addIf("username", u.Username)
		o.addIf("password", u.Password)
		if u.Security == "tls" {
			o.add("tls", tlsObj())
		}
	default:
		return nil, false
	}
	return o, true
}

// ncsiDomains: the Windows network check (plan 226).
var ncsiDomains = []string{"msftconnecttest.com", "msftncsi.com"}

const (
	geositeCN = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs"
	geoipCN   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"
)

// SingBox renders a sing-box (1.12+ schema) client config with a TUN and a
// local mixed inbound. 国内直连 uses SagerNet's geosite-cn / geoip-cn rule
// sets; plan 226: the client downloads them DIRECT from the panel at origin
// (scheme://host the subscription was pulled from, see RuleSets), so a dead
// node or 节点选择 = direct no longer stops sing-box from starting. origin ""
// keeps the fork.119 download (GitHub through 节点选择). It returns the JSON
// and how many nodes it contains.
func SingBox(nodes []Node, mode, origin string) ([]byte, int) {
	return SingBoxRouted(nodes, mode, origin, nil)
}

// SingBoxRouted is SingBox with a plan-256 「分流」 list: one selector per
// entry and route rules after the private rules (the rule sets download
// DIRECT from the panel at origin). sing-box 1.13 has no block outbound, so a
// 拒绝 entry is a reject rule without a selector. An empty list renders
// exactly what SingBox renders.
func SingBoxRouted(nodes []Node, mode, origin string, routing []RouteEntry) ([]byte, int) {
	if len(routing) > 0 {
		nodes = avoidGroupNames(nodes, planRoutes(routing, nil, true))
	}
	outbounds := []interface{}{}
	tags := []string{}
	var kept []Node
	for _, n := range nodes {
		if o, ok := singBoxOutbound(n); ok {
			outbounds = append(outbounds, o)
			tags = append(tags, n.Name)
			if len(routing) > 0 {
				kept = append(kept, n)
			}
		}
	}
	var routes []routeGroup
	if len(routing) > 0 {
		routes = planRoutes(routing, routeNodeNames(kept, tags), len(tags) > 0)
	}
	sel := []string{}
	if len(tags) > 0 {
		sel = append(sel, GroupAuto)
	}
	sel = append(sel, tags...)
	sel = append(sel, "direct")
	groups := []interface{}{jmap{{"type", "selector"}, {"tag", GroupSelect}, {"outbounds", sel}, {"default", sel[0]}}}
	if len(tags) > 0 {
		groups = append(groups, jmap{{"type", "urltest"}, {"tag", GroupAuto}, {"outbounds", tags}, {"url", TestURL}, {"interval", "5m"}, {"tolerance", 50}})
	}
	auto := ""
	if len(tags) > 0 {
		auto = GroupAuto
	}
	for _, g := range routes {
		if g.exit == ExitReject {
			continue
		}
		members := groupMembers(g, GroupSelect, auto, "direct", "", tags)
		groups = append(groups, jmap{{"type", "selector"}, {"tag", g.name}, {"outbounds", members}, {"default", members[0]}})
	}
	all := append(groups, outbounds...)
	all = append(all, jmap{{"type", "direct"}, {"tag", "direct"}})

	// Plan 220: without nodes the select group is only "direct", so a remote
	// resolver behind it would dial 1.1.1.1 straight from the mainland (times
	// out) and the rule sets would download from GitHub directly; everything
	// goes direct anyway, so such a config resolves with CN DoH only.
	remoteDNS := "dns-remote"
	dnsServers := []interface{}{
		jmap{{"type", "https"}, {"tag", "dns-remote"}, {"server", "1.1.1.1"}, {"detour", GroupSelect}},
		jmap{{"type", "https"}, {"tag", "dns-local"}, {"server", "223.5.5.5"}},
	}
	if len(tags) == 0 {
		remoteDNS = "dns-local"
		dnsServers = dnsServers[1:]
	}
	cn := mode != ModeGlobal && len(tags) > 0
	dnsRules := []interface{}{}
	routeRules := []interface{}{
		jmap{{"action", "sniff"}},
		jmap{{"protocol", "dns"}, {"action", "hijack-dns"}},
		jmap{{"ip_is_private", true}, {"outbound", "direct"}},
	}
	route := jmap{}
	if cn {
		// Plan 226: the Windows network check resolves with CN DoH and goes
		// DIRECT (through a dead node Windows shows 「无 Internet」).
		dnsRules = append(dnsRules,
			jmap{{"domain_suffix", ncsiDomains}, {"server", "dns-local"}},
			jmap{{"rule_set", "geosite-cn"}, {"server", "dns-local"}})
		routeRules = append(routeRules, jmap{{"domain_suffix", ncsiDomains}, {"outbound", "direct"}})
	}
	routeRules = append(routeRules, singBoxRouteRules(routes)...)
	if cn {
		routeRules = append(routeRules, jmap{{"rule_set", []string{"geosite-cn", "geoip-cn"}}, {"outbound", "direct"}})
	}
	route.add("rules", routeRules)
	detour := GroupSelect
	if origin != "" {
		detour = "direct"
	}
	ruleSets := []interface{}{}
	if cn {
		site, ip := geositeCN, geoipCN
		if origin != "" {
			site, ip = RuleSetURL(origin, "geosite-cn"), RuleSetURL(origin, "geoip-cn")
		}
		ruleSets = append(ruleSets,
			jmap{{"type", "remote"}, {"tag", "geosite-cn"}, {"format", "binary"}, {"url", site}, {"download_detour", detour}},
			jmap{{"type", "remote"}, {"tag", "geoip-cn"}, {"format", "binary"}, {"url", ip}, {"download_detour", detour}},
		)
	}
	seen := map[string]bool{}
	for _, g := range routes {
		for _, rs := range g.sets {
			if seen[rs.ID] {
				continue
			}
			seen[rs.ID] = true
			url := upstreamURL(rs.ID, ExtSRS)
			if origin != "" {
				url = RuleSetURL(origin, rs.ID)
			}
			ruleSets = append(ruleSets, jmap{{"type", "remote"}, {"tag", rs.ID}, {"format", "binary"}, {"url", url}, {"download_detour", detour}})
		}
	}
	if len(ruleSets) > 0 {
		route.add("rule_set", ruleSets)
	}
	route.add("final", GroupSelect)
	route.add("auto_detect_interface", true)
	route.add("default_domain_resolver", "dns-local")

	dns := jmap{{"servers", dnsServers}}
	if len(dnsRules) > 0 {
		dns.add("rules", dnsRules)
	}
	dns.add("final", remoteDNS)
	dns.add("strategy", "prefer_ipv4")

	doc := jmap{
		{"log", jmap{{"level", "warn"}}},
		{"dns", dns},
		{"inbounds", []interface{}{
			jmap{{"type", "tun"}, {"tag", "tun-in"}, {"address", []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}}, {"auto_route", true}, {"strict_route", true}},
			jmap{{"type", "mixed"}, {"tag", "mixed-in"}, {"listen", "127.0.0.1"}, {"listen_port", 2080}},
		}},
		{"outbounds", all},
		{"route", route},
		{"experimental", jmap{{"cache_file", jmap{{"enabled", true}}}}},
	}
	raw, _ := json.Marshal(doc)
	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") != nil {
		return raw, len(tags)
	}
	out.WriteByte('\n')
	return out.Bytes(), len(tags)
}

// singBoxRouteRules: the 「分流」 route rules. Domain and IP fields of one
// rule are ORed by sing-box; IP rules never resolve a domain first.
func singBoxRouteRules(routes []routeGroup) []interface{} {
	out := []interface{}{}
	for _, g := range routes {
		var rules []jmap
		if len(g.custom) > 0 {
			var suffix, full, keyword, cidr []string
			for _, r := range g.custom {
				switch r.Kind {
				case "suffix":
					suffix = append(suffix, r.Value)
				case "full":
					full = append(full, r.Value)
				case "keyword":
					keyword = append(keyword, r.Value)
				case "ip4", "ip6":
					cidr = append(cidr, r.Value)
				}
			}
			m := jmap{}
			m.addIf("domain", full)
			m.addIf("domain_suffix", suffix)
			m.addIf("domain_keyword", keyword)
			m.addIf("ip_cidr", cidr)
			rules = append(rules, m)
		}
		if len(g.sets) > 0 {
			ids := make([]string, 0, len(g.sets))
			for _, rs := range g.sets {
				ids = append(ids, rs.ID)
			}
			rules = append(rules, jmap{{"rule_set", ids}})
		}
		for _, m := range rules {
			if g.exit == ExitReject {
				m.add("action", "reject")
			} else {
				m.add("outbound", g.name)
			}
			out = append(out, m)
		}
	}
	return out
}
