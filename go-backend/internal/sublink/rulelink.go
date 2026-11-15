package sublink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"
)

// Plan 261 「规则集链接」: a 自己写 分流 entry may point at someone else's rule
// list instead of typed lines. The panel downloads the list, keeps ONLY the
// rules it can validate (domain / domain suffix / keyword / IP range, each
// through the same validators as 自己写), stores them in its own normalized
// form and serves that to the clients at /s/rs/u/<public id>.<ext>. The
// remote content itself is never passed through: whatever the list holds
// besides those rules (regexes, processes, scripts, other rule sets, the
// list's own policies…) is dropped and counted as 忽略.

// Caps of one link set (per-user / per-subscription caps: model + routing).
const (
	MaxLinkBytes   = 4 << 20 // decoded body
	MaxLinkRules   = 50000   // valid rules after dedupe
	MaxLinkEntries = 5       // link entries per subscription

	maxLinkLineBytes = 4096 // a longer raw line is skipped as 忽略
)

// Errors of ParseRuleList (plain words, shown to the user as is).
var (
	ErrLinkBinary  = errors.New("只支持文本规则集（.list / .txt / .yaml），不支持 .mrs / .srs")
	ErrLinkHTML    = errors.New("不是文本规则集（打开的是网页，请用规则文件的原始地址，例如 GitHub 的 Raw 链接）")
	ErrLinkEmpty   = errors.New("没有可用的规则（只认域名、域名后缀、关键词和 IP 段）")
	ErrLinkTooMany = fmt.Errorf("规则太多，最多 %d 条", MaxLinkRules)
	ErrLinkTooBig  = fmt.Errorf("规则集太大，最多 %d MB", MaxLinkBytes>>20)
)

// LinkCounts summarizes a parsed list.
type LinkCounts struct {
	Domain  int `json:"domain"`  // DOMAIN (exact)
	Suffix  int `json:"suffix"`  // DOMAIN-SUFFIX
	Keyword int `json:"keyword"` // DOMAIN-KEYWORD
	IP      int `json:"ip"`      // IP-CIDR / IP-CIDR6
	Ignored int `json:"ignored"` // lines with other rule types or invalid values
}

// Total is the number of kept rules.
func (c LinkCounts) Total() int { return c.Domain + c.Suffix + c.Keyword + c.IP }

// LinkRules is a parsed list: the kept rules in list order, deduplicated.
type LinkRules struct {
	Rules  []RouteRule
	Counts LinkCounts
}

var (
	gzipMagic = []byte{0x1f, 0x8b}
	utf8BOM   = []byte{0xef, 0xbb, 0xbf}
)

// binaryRuleSet reports content that is not a text list: NUL bytes, invalid
// UTF-8, or the magic of a gzip / zstd (.mrs) / sing-box (.srs) file.
func binaryRuleSet(b []byte) bool {
	if bytes.HasPrefix(b, gzipMagic) || bytes.HasPrefix(b, zstdMagic) {
		return true
	}
	if len(b) >= 4 && bytes.HasPrefix(b, []byte("SRS")) && b[3] < 0x20 {
		return true
	}
	return bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b)
}

// linkTypes maps classical rule types (Clash / Surge / Loon / Quantumult X
// spellings) to the kept kinds; every other type is dropped.
var linkTypes = map[string]string{
	"DOMAIN":         "full",
	"HOST":           "full",
	"DOMAIN-SUFFIX":  "suffix",
	"HOST-SUFFIX":    "suffix",
	"DOMAIN-KEYWORD": "keyword",
	"HOST-KEYWORD":   "keyword",
	"IP-CIDR":        "ip",
	"IP-CIDR6":       "ip",
	"IP6-CIDR":       "ip",
}

// yamlKeyLine: a YAML mapping key line such as "payload:" or "behavior: x"
// (a letter first, the colon followed by a blank or the end). IPv6 values
// never match (their first colon is followed by a hex digit or a colon).
func yamlKeyLine(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return false
	}
	c := s[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}
	for j := 1; j < i; j++ {
		c := s[j]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return i == len(s)-1 || s[i+1] == ' ' || s[i+1] == '\t'
}

// stripTrailingComment cuts " #…" / " //…" after a value (outside quotes).
func stripTrailingComment(s string) string {
	q := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case (c == ' ' || c == '\t') && i+1 < len(s) && (s[i+1] == '#' || i+2 < len(s) && s[i+1] == '/' && s[i+2] == '/'):
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// unquote removes one pair of matching YAML quotes.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// linkItem parses one list item into a rule; ok=false = 忽略.
func linkItem(s string) (RouteRule, bool) {
	kind, value := "", ""
	if i := strings.IndexByte(s, ','); i >= 0 {
		// Classical: TYPE,VALUE[,POLICY][,no-resolve…] — the policy and
		// options of the list are never used (the user's exit decides).
		kind = linkTypes[strings.ToUpper(strings.TrimSpace(s[:i]))]
		if kind == "" {
			return RouteRule{}, false
		}
		value = s[i+1:]
		if j := strings.IndexByte(value, ','); j >= 0 {
			value = value[:j]
		}
		value = strings.TrimSpace(value)
	} else {
		// Plain line / Clash domain or ipcidr behavior: "+.x" / ".x" / "*.x"
		// = x and its subdomains, "x" = exactly x (mihomo text semantics),
		// an IP or CIDR = that range; other wildcards are not representable.
		value = s
		switch {
		case strings.HasPrefix(s, "+.") || strings.HasPrefix(s, "*.") || strings.HasPrefix(s, "."):
			kind = "suffix"
		default:
			if _, err := netip.ParsePrefix(s); err == nil {
				kind = "ip"
			} else if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
				kind = "ip"
			} else {
				kind = "full"
			}
		}
	}
	if value == "" || len(value) > MaxRouteLineLen || strings.Contains(value, "*") && kind != "suffix" {
		return RouteRule{}, false
	}
	if kind == "suffix" {
		for _, p := range []string{"+.", "*.", "."} {
			if strings.HasPrefix(value, p) {
				value = value[len(p):]
				break
			}
		}
		if strings.Contains(value, "*") {
			return RouteRule{}, false
		}
	}
	r, why := parseRouteValue(kind, value)
	if why != "" {
		return RouteRule{}, false
	}
	return r, true
}

// ParseRuleList reads a downloaded rule list (decoded body). Accepted
// shapes: Clash classical lines, Surge / Loon / Quantumult X lists, Clash
// YAML `payload:` lists (classical, domain or ipcidr behavior), plain
// one-domain / one-CIDR-per-line lists; # // ; comments, CRLF and a BOM.
func ParseRuleList(b []byte) (*LinkRules, error) {
	if len(b) > MaxLinkBytes {
		return nil, ErrLinkTooBig
	}
	b = bytes.TrimPrefix(b, utf8BOM)
	if binaryRuleSet(b) {
		return nil, ErrLinkBinary
	}
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '<' {
		return nil, ErrLinkHTML
	}
	out := &LinkRules{}
	seen := map[RouteRule]struct{}{}
	for len(b) > 0 {
		var raw []byte
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			raw, b = b[:i], b[i+1:]
		} else {
			raw, b = b, nil
		}
		if len(raw) > maxLinkLineBytes {
			out.Counts.Ignored++
			continue
		}
		s := strings.TrimSpace(string(raw))
		if s == "" || s[0] == '#' || s[0] == ';' || s[0] == '!' || strings.HasPrefix(s, "//") || s == "---" || s == "..." {
			continue
		}
		if s[0] == '[' && s[len(s)-1] == ']' && !strings.Contains(s, ",") {
			continue // [Rule] / [filter_local] section header
		}
		if strings.HasPrefix(s, "- ") || s == "-" {
			s = strings.TrimSpace(s[1:]) // YAML list item
		} else if yamlKeyLine(s) {
			continue // payload: / behavior: …
		}
		s = unquote(stripTrailingComment(s))
		if s == "" {
			continue
		}
		r, ok := linkItem(s)
		if !ok {
			out.Counts.Ignored++
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		if len(out.Rules) >= MaxLinkRules {
			return nil, ErrLinkTooMany
		}
		seen[r] = struct{}{}
		out.Rules = append(out.Rules, r)
		switch r.Kind {
		case "full":
			out.Counts.Domain++
		case "suffix":
			out.Counts.Suffix++
		case "keyword":
			out.Counts.Keyword++
		default:
			out.Counts.IP++
		}
	}
	if len(out.Rules) == 0 {
		return nil, ErrLinkEmpty
	}
	return out, nil
}

// classicalLine is a rule as one classical line without a policy.
func classicalLine(r RouteRule) string {
	switch r.Kind {
	case "full":
		return "DOMAIN," + r.Value
	case "suffix":
		return "DOMAIN-SUFFIX," + r.Value
	case "keyword":
		return "DOMAIN-KEYWORD," + r.Value
	case "ip4":
		return "IP-CIDR," + r.Value
	case "ip6":
		return "IP-CIDR6," + r.Value
	}
	return ""
}

// LinkText is the stored and served form: one classical line per rule
// (mihomo `behavior: classical, format: text`, Stash, Surge / Surfboard
// RULE-SET). IP lines carry no no-resolve; the RULE-SET line does.
func LinkText(rules []RouteRule) []byte {
	var b bytes.Buffer
	b.Grow(len(rules) * 28)
	for _, r := range rules {
		if l := classicalLine(r); l != "" {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// LinkHash is the content hash of a stored set (also its ETag).
func LinkHash(text []byte) string {
	sum := sha256.Sum256(text)
	return hex.EncodeToString(sum[:16])
}

// ParseLinkText reads a stored set back (its own format; a line that does
// not validate is skipped, so a damaged row can never emit anything else).
func ParseLinkText(text string) []RouteRule {
	out := make([]RouteRule, 0, strings.Count(text, "\n")+1)
	for len(text) > 0 {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		t, v, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		kind := linkTypes[t]
		if kind == "" {
			continue
		}
		if r, why := parseRouteValue(kind, v); why == "" {
			out = append(out, r)
		}
	}
	return out
}

// LinkSourceVersion: the sing-box source rule-set version the served JSON
// declares (version 2 = sing-box 1.10+, the generated configs need 1.12+).
const LinkSourceVersion = 2

// LinkSingBoxJSON is a stored set as a sing-box source rule set: one
// headless rule whose domain / suffix / keyword / CIDR lists are ORed.
func LinkSingBoxJSON(rules []RouteRule) []byte {
	var full, suffix, keyword, cidr []string
	for _, r := range rules {
		switch r.Kind {
		case "full":
			full = append(full, r.Value)
		case "suffix":
			suffix = append(suffix, r.Value)
		case "keyword":
			keyword = append(keyword, r.Value)
		case "ip4", "ip6":
			cidr = append(cidr, r.Value)
		}
	}
	rule := jmap{}
	rule.addIf("domain", full)
	rule.addIf("domain_suffix", suffix)
	rule.addIf("domain_keyword", keyword)
	rule.addIf("ip_cidr", cidr)
	rulesOut := []interface{}{}
	if len(rule) > 0 {
		rulesOut = append(rulesOut, rule)
	}
	b, _ := json.Marshal(jmap{{"version", LinkSourceVersion}, {"rules", rulesOut}})
	return append(b, '\n')
}

// ------------------------------------------------------------ public ids

// LinkPath is the public prefix of link sets: /s/rs/u/<public id>.<ext>.
const LinkPath = RuleSetPath + "u/"

// Served extensions of link sets.
const (
	ExtLinkList = "list" // classical text
	ExtLinkJSON = "json" // sing-box source
)

// ValidLinkID: 32 lower-case hex characters (128 random bits).
func ValidLinkID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// LinkFileURL is the panel URL of one served link set file.
func LinkFileURL(origin, publicID, ext string) string {
	return origin + LinkPath + publicID + "." + ext
}

// linkProviderName: the rule-provider / rule-set tag of a link set.
func linkProviderName(publicID string) string { return "link-" + publicID }

// linksFrom drops link groups when there is no panel origin to serve their
// sets from (only tests render without one).
func linksFrom(routes []routeGroup, origin string) []routeGroup {
	if origin != "" {
		return routes
	}
	out := routes[:0]
	for _, g := range routes {
		if g.link == "" {
			out = append(out, g)
		}
	}
	return out
}
