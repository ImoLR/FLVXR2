package landing

import (
	"fmt"
	"sort"
	"strings"
)

// Plan 316 「屏蔽本机厂商官网」: the panel's rule that blocks the VPS vendor's
// own sites on this landing (builtin "vendor", block only, "domain:" names).
//
// The agent renders it right after the safety rule and, on every inbound it
// covers, lets the Core's sniffer override the routing domain ONLY when the
// sniffed TLS SNI / HTTP Host is one of these names: sniffing.domainsExcluded
// is the complement of the names instead of "regexp:.*". So a client that
// dials the vendor by IP with the vendor's SNI is caught by the same rule,
// while every other sniffed name still never replaces the original target
// (the private-target protection of the safety rule is unchanged: an
// overridden connection is always blocked by the next rule).
//
// Invariant: an inbound's override names ⊆ the vendor names the applied
// routing blocks for it. The routing therefore keeps blocking every name an
// applied inbound still overrides (transition rules), inbounds only get names
// the applied routing blocks, and the routing drops a name one pass after the
// inbounds stopped overriding it.

const builtinVendor = "vendor"

const maxVendorHosts = 64

// vendorHosts returns the host names of a vendor rule ("domain:" entries).
func (r EgressRule) vendorHosts() []string {
	out := make([]string, 0, len(r.Domain))
	for _, d := range r.Domain {
		if h, ok := strings.CutPrefix(d, "domain:"); ok {
			out = append(out, h)
		}
	}
	return out
}

// validateVendorRule: block only, nothing but "domain:<host>" entries.
func (r EgressRule) validateVendorRule() error {
	if r.Outbound != "block" || len(r.Domain) == 0 || len(r.Domain) > maxVendorHosts || len(r.IP)+len(r.SourceIP)+len(r.Protocol) > 0 || r.Port != "" || r.SourcePort != "" || r.Network != "" {
		return fmt.Errorf("invalid builtin rule")
	}
	for _, d := range r.Domain {
		h, ok := strings.CutPrefix(d, "domain:")
		if !ok || len(h) > 253 || !strings.Contains(h, ".") || !hostnameRe.MatchString(h) || strings.HasSuffix(h, ".") {
			return fmt.Errorf("invalid builtin rule")
		}
	}
	return nil
}

// vendorSets maps each inbound tag to the sorted vendor names that cover it.
func vendorSets(e *Egress) map[string][]string {
	if e == nil {
		return nil
	}
	sets := map[string]map[string]bool{}
	for _, r := range e.Rules {
		if r.Builtin != builtinVendor {
			continue
		}
		for _, t := range r.InboundTags {
			if sets[t] == nil {
				sets[t] = map[string]bool{}
			}
			for _, h := range r.vendorHosts() {
				sets[t][h] = true
			}
		}
	}
	if len(sets) == 0 {
		return nil
	}
	out := make(map[string][]string, len(sets))
	for t, s := range sets {
		out[t] = minimalHosts(s)
	}
	return out
}

// minimalHosts sorts names and drops one covered by another (x.a.com under a.com).
func minimalHosts(set map[string]bool) []string {
	out := []string{}
	for h := range set {
		covered := false
		for p := h; ; {
			i := strings.IndexByte(p, '.')
			if i < 0 {
				break
			}
			p = p[i+1:]
			if set[p] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

func hostSet(lists ...[]string) map[string]bool {
	s := map[string]bool{}
	for _, l := range lists {
		for _, h := range l {
			s[h] = true
		}
	}
	return s
}

// coveredBy reports whether every name of a is blocked by "domain:" b.
func coveredBy(a, b []string) bool {
	set := hostSet(b)
	for _, h := range a {
		ok := false
		for p := h; ; {
			if set[p] {
				ok = true
				break
			}
			i := strings.IndexByte(p, '.')
			if i < 0 {
				break
			}
			p = p[i+1:]
		}
		if !ok {
			return false
		}
	}
	return true
}

// withVendorTransition adds, in front, one vendor rule per inbound whose
// applied sniffing still overrides names the plan no longer blocks for it.
// The plan itself is never modified.
func withVendorTransition(plan *Egress, sniff map[string][]string) *Egress {
	if len(sniff) == 0 {
		return plan
	}
	want := vendorSets(plan)
	tags := make([]string, 0, len(sniff))
	for t := range sniff {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	var extra []EgressRule
	for _, t := range tags {
		var missing []string
		for _, h := range sniff[t] {
			if !coveredBy([]string{h}, want[t]) {
				missing = append(missing, h)
			}
		}
		if len(missing) == 0 {
			continue
		}
		r := EgressRule{Name: "vendor-transition", InboundTags: []string{t}, Outbound: "block", Builtin: builtinVendor}
		for _, h := range missing {
			r.Domain = append(r.Domain, "domain:"+h)
		}
		extra = append(extra, r)
	}
	if len(extra) == 0 {
		return plan
	}
	out := &Egress{Version: EgressVersion}
	if plan != nil {
		cp := *plan
		out = &cp
	}
	out.Rules = append(extra, out.Rules...)
	return out
}

// sniffOverride: the names an inbound may override now = desired ∩ blocked
// by the applied routing.
func sniffOverride(desired, applied []string) []string {
	var out []string
	for _, h := range desired {
		if coveredBy([]string{h}, applied) {
			out = append(out, h)
		}
	}
	return out
}

// withVendorSniff replaces the inbound's domain exclusion by the complement
// of the names (nil names: unchanged, i.e. "regexp:.*").
func withVendorSniff(inbound map[string]any, hosts []string) map[string]any {
	if len(hosts) == 0 {
		return inbound
	}
	sniff, ok := inbound["sniffing"].(map[string]any)
	if !ok {
		return inbound
	}
	cp := make(map[string]any, len(sniff))
	for k, v := range sniff {
		cp[k] = v
	}
	cp["domainsExcluded"] = []string{"regexp:" + sniffExcludeRegexp(hosts)}
	out := make(map[string]any, len(inbound))
	for k, v := range inbound {
		out[k] = v
	}
	out["sniffing"] = cp
	return out
}

// sniffExcludeRegexp returns an anchored RE2 expression matching every
// string EXCEPT the names and their subdomains (the Core's "domain:" rule
// semantics), for the lowercase sniffed name the Core checks. Built from a
// trie of the reversed names, so it is linear in their total length.
func sniffExcludeRegexp(hosts []string) string {
	root := &trieNode{}
	for _, h := range minimalHosts(hostSet(hosts)) {
		n := root
		for i := len(h) - 1; i >= 0; i-- {
			n = n.child(h[i])
		}
		n.terminal = true
	}
	// (?s): "." also matches a newline, so no odd sniffed value escapes the
	// exclusion.
	return "(?s)^(?:" + root.exclude() + ")$"
}

type trieNode struct {
	terminal bool
	keys     []byte
	next     map[byte]*trieNode
}

func (n *trieNode) child(c byte) *trieNode {
	if n.next == nil {
		n.next = map[byte]*trieNode{}
	}
	if x, ok := n.next[c]; ok {
		return x
	}
	x := &trieNode{}
	n.next[c] = x
	n.keys = append(n.keys, c)
	sort.Slice(n.keys, func(i, j int) bool { return n.keys[i] < n.keys[j] })
	return x
}

// exclude: the forward regexp of the prefixes p such that p + (this node's
// suffix) is NOT a blocked name. A blocked name ends exactly here (terminal)
// or continues with "." after it.
func (n *trieNode) exclude() string {
	var alts []string
	if !n.terminal {
		// The whole string is a proper suffix of a name: not blocked.
		alts = append(alts, "")
	}
	stop := append([]byte(nil), n.keys...)
	if n.terminal {
		stop = append(stop, '.')
	}
	for _, c := range n.keys {
		if n.terminal && c == '.' {
			continue // "x." + name is a subdomain: blocked
		}
		alts = append(alts, n.next[c].exclude()+reLiteral(c))
	}
	// Any prefix ending in a character that leaves the trie (and, after a
	// whole name, is not a dot) is not blocked.
	alts = append(alts, ".*"+reNotIn(stop))
	return "(?:" + strings.Join(alts, "|") + ")"
}

func reLiteral(c byte) string {
	if c == '.' || c == '-' || c == '*' || c == '+' || c == '?' || c == '(' || c == ')' || c == '[' || c == ']' || c == '{' || c == '}' || c == '|' || c == '^' || c == '$' || c == '\\' {
		return `\` + string(c)
	}
	return string(c)
}

func reNotIn(chars []byte) string {
	if len(chars) == 0 {
		return "."
	}
	var b strings.Builder
	b.WriteString("[^")
	for _, c := range chars {
		switch c {
		case '-', ']', '\\', '^', '[':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteString("]")
	return b.String()
}
