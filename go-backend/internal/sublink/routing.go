package sublink

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Plan 256 「分流」: a subscription's routing list. Every entry becomes one
// select group in the client (Clash / Stash / sing-box / Surge) and the rules
// that send matching traffic to it. Builtin entries use rule sets the panel
// serves at /s/rs/ (MetaCubeX meta-rules-dat data, see ruleset.go); custom
// entries carry the user's own domains / IPs as inline rules. An empty list
// renders exactly what fork.131 rendered.

// Entry types and exits.
const (
	RouteBuiltin = "builtin"
	RouteCustom  = "custom"

	ExitSelect     = "select" // 节点选择
	ExitAuto       = "auto"   // 自动选择
	ExitDirect     = "direct"
	ExitReject     = "reject"
	ExitNodePrefix = "node:" // node:<ref key> — a node of this subscription
)

// Caps of one subscription's list.
const (
	MaxRouteEntries = 30
	MaxRouteLines   = 300 // custom lines over all custom entries
	MaxRouteLineLen = 253
	MaxRouteNameLen = 20 // runes
	customIcon      = "📌"
)

// RouteEntry is one stored row of the list.
type RouteEntry struct {
	Type  string   `json:"type"`
	Key   string   `json:"key,omitempty"`   // builtin: catalog key
	Name  string   `json:"name,omitempty"`  // custom: group name without the icon
	Lines []string `json:"lines,omitempty"` // custom: normalized rule lines
	Exit  string   `json:"exit"`
}

// RuleSet is one rule set the panel serves: ID is the public name
// (/s/rs/<ID>.<ext>), Upstream the MetaCubeX file name, IP an ipcidr set.
type RuleSet struct {
	ID       string
	Upstream string
	IP       bool
}

func site(name string) RuleSet { return RuleSet{ID: "geosite-" + name, Upstream: name} }
func ip(name string) RuleSet   { return RuleSet{ID: "geoip-" + name, Upstream: name, IP: true} }

// Category is one builtin entry kind. The table order is the rule order.
type Category struct {
	Key  string
	Icon string
	Name string
	Desc string // short hint under the name in the editor
	Sets []RuleSet
	Exit string // default exit
}

// GroupName is the client-side group name.
func (c Category) GroupName() string { return c.Icon + " " + c.Name }

// Catalog: adding a category = one row here plus its snapshot files under
// ruleset/ (TestRuleSetSnapshot -fetch-rulesets downloads missing ones).
// AI and YouTube come before 谷歌 so Gemini / YouTube are not taken by it;
// GitHub before 微软.
var Catalog = []Category{
	{Key: "ads", Icon: "🛑", Name: "广告拦截", Desc: "常见广告和跟踪域名", Sets: []RuleSet{{ID: "geosite-ads", Upstream: "category-ads-all"}}, Exit: ExitReject},
	{Key: "ai", Icon: "🤖", Name: "AI", Desc: "ChatGPT · Claude · Gemini 等", Sets: []RuleSet{{ID: "geosite-ai", Upstream: "category-ai-!cn"}}, Exit: ExitSelect},
	{Key: "youtube", Icon: "📹", Name: "YouTube", Sets: []RuleSet{site("youtube")}, Exit: ExitSelect},
	{Key: "streaming", Icon: "🎬", Name: "流媒体", Desc: "Netflix · Disney+ · HBO · Prime", Sets: []RuleSet{site("netflix"), site("disney"), site("hbo"), site("primevideo"), ip("netflix")}, Exit: ExitSelect},
	{Key: "tiktok", Icon: "🎵", Name: "TikTok", Sets: []RuleSet{site("tiktok")}, Exit: ExitSelect},
	{Key: "spotify", Icon: "🎧", Name: "Spotify", Sets: []RuleSet{site("spotify")}, Exit: ExitSelect},
	{Key: "discord", Icon: "🕹️", Name: "Discord", Sets: []RuleSet{site("discord")}, Exit: ExitSelect},
	{Key: "social", Icon: "💬", Name: "社交", Desc: "X · Facebook · Instagram", Sets: []RuleSet{site("twitter"), site("facebook"), site("instagram")}, Exit: ExitSelect},
	{Key: "telegram", Icon: "📲", Name: "Telegram", Sets: []RuleSet{site("telegram"), ip("telegram")}, Exit: ExitSelect},
	{Key: "github", Icon: "🐱", Name: "GitHub", Sets: []RuleSet{site("github")}, Exit: ExitSelect},
	{Key: "google", Icon: "🔍", Name: "谷歌", Desc: "搜索 · 地图 · Gmail 等", Sets: []RuleSet{site("google"), ip("google")}, Exit: ExitSelect},
	{Key: "microsoft", Icon: "Ⓜ️", Name: "微软", Sets: []RuleSet{site("microsoft")}, Exit: ExitDirect},
	{Key: "apple", Icon: "🍎", Name: "苹果", Sets: []RuleSet{site("apple")}, Exit: ExitDirect},
	// category-games-!cn holds every steam / epicgames line plus Blizzard,
	// EA, consoles…, without the mainland (@cn) CDN domains.
	{Key: "games", Icon: "🎮", Name: "游戏", Desc: "Steam · Epic · 主机平台等", Sets: []RuleSet{{ID: "geosite-games", Upstream: "category-games-!cn"}}, Exit: ExitSelect},
}

// DefaultRouteKeys: the list of a new subscription (and 「恢复默认」).
var DefaultRouteKeys = []string{"ai", "youtube", "streaming", "telegram", "github", "google", "microsoft", "apple"}

var catalogIndex = func() map[string]int {
	m := map[string]int{}
	for i, c := range Catalog {
		m[c.Key] = i
	}
	return m
}()

// CategoryByKey finds a builtin category.
func CategoryByKey(key string) (Category, bool) {
	i, ok := catalogIndex[key]
	if !ok {
		return Category{}, false
	}
	return Catalog[i], true
}

// catalogSets: every served catalog rule set by id.
var catalogSets = func() map[string]RuleSet {
	m := map[string]RuleSet{}
	for _, c := range Catalog {
		for _, s := range c.Sets {
			m[s.ID] = s
		}
	}
	return m
}()

// DefaultRouting is the default list (builtins with their default exits).
func DefaultRouting() []RouteEntry {
	out := make([]RouteEntry, 0, len(DefaultRouteKeys))
	for _, k := range DefaultRouteKeys {
		c, _ := CategoryByKey(k)
		out = append(out, RouteEntry{Type: RouteBuiltin, Key: k, Exit: c.Exit})
	}
	return out
}

// ParseRouting reads the stored column (NULL / broken JSON → empty list).
func ParseRouting(s *string) []RouteEntry {
	if s == nil || *s == "" {
		return nil
	}
	var out []RouteEntry
	if json.Unmarshal([]byte(*s), &out) != nil {
		return nil
	}
	return out
}

// RoutingJSON is the stored form ("[]" for an empty list).
func RoutingJSON(list []RouteEntry) string {
	if list == nil {
		list = []RouteEntry{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// reservedGroupNames: names a custom entry may not take (also compared with
// the icon prefix stripped from builtin names).
func reservedGroupName(name string) bool {
	if reservedNames[name] || lineReserved[strings.ToUpper(name)] {
		return true
	}
	for _, c := range Catalog {
		if name == c.Name || name == c.GroupName() {
			return true
		}
	}
	return false
}

// validExit checks the syntax of an exit.
func validExit(e string) bool {
	switch e {
	case ExitSelect, ExitAuto, ExitDirect, ExitReject:
		return true
	}
	k, ok := strings.CutPrefix(e, ExitNodePrefix)
	return ok && k != "" && len(k) <= 400 && utf8.ValidString(k) && !strings.ContainsAny(k, "\r\n")
}

// CleanRouteName trims and checks a custom entry name.
func CleanRouteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("请给自己写的分流起个名字")
	}
	if utf8.RuneCountInString(name) > MaxRouteNameLen {
		return "", fmt.Errorf("名字最多 %d 个字", MaxRouteNameLen)
	}
	for _, r := range name {
		if r == ',' || r == '"' || r == '\\' || r == '，' || unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r == 0x85 {
			return "", fmt.Errorf("名字「%s」里不能有逗号、引号、反斜杠或换行", name)
		}
	}
	if reservedGroupName(name) {
		return "", fmt.Errorf("「%s」是内置的名字，请换一个", name)
	}
	return name, nil
}

// RouteRule is one parsed custom line.
type RouteRule struct {
	Kind  string // suffix | full | keyword | ip4 | ip6
	Value string // domain / keyword / CIDR (always with a prefix length)
}

// RouteLineError carries the 1-based line number of a bad custom line.
type RouteLineError struct {
	Line int
	Text string
	Why  string
}

func (e *RouteLineError) Error() string {
	t := e.Text
	if utf8.RuneCountInString(t) > 40 {
		t = string([]rune(t)[:40]) + "…"
	}
	return fmt.Sprintf("第 %d 行「%s」%s", e.Line, t, e.Why)
}

func validDomainChars(s string, dot bool) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || dot && c == '.') {
			return false
		}
	}
	return true
}

func validDomain(d string) bool {
	if len(d) > 253 || !validDomainChars(d, true) || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") || strings.Contains(d, "..") {
		return false
	}
	for _, l := range strings.Split(d, ".") {
		if len(l) > 63 {
			return false
		}
	}
	return true
}

func nonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// ParseRouteLine parses one custom line (already trimmed, not empty, not a
// comment). Plain example.com = the domain and its subdomains; an IP or CIDR =
// that address range; a word without a dot = a keyword; explicit prefixes
// full: / domain: / keyword: / ip: override the guess. A pasted URL counts as
// its host.
func ParseRouteLine(s string) (RouteRule, string) {
	if len(s) > MaxRouteLineLen {
		return RouteRule{}, fmt.Sprintf("太长了（最多 %d 个字符）", MaxRouteLineLen)
	}
	kind := ""
	if i := strings.Index(s, ":"); i > 0 {
		switch strings.ToLower(s[:i]) {
		case "full":
			kind = "full"
		case "domain":
			kind = "suffix"
		case "keyword":
			kind = "keyword"
		case "ip":
			kind = "ip"
		}
		if kind != "" {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	if kind == "" && strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			s = u.Hostname()
		}
	}
	if kind == "" || kind == "ip" {
		if p, err := netip.ParsePrefix(s); err == nil {
			if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
			return ipRule(p.Masked()), ""
		}
		if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil && a.Zone() == "" {
			a = a.Unmap()
			return ipRule(netip.PrefixFrom(a, a.BitLen())), ""
		}
		if kind == "ip" {
			return RouteRule{}, "不是有效的 IP 或 IP 段"
		}
	}
	if nonASCII(s) {
		return RouteRule{}, "里有中文或全角字符（中文域名请写成 xn-- 开头的形式）"
	}
	d := strings.ToLower(s)
	switch kind {
	case "full":
		if !validDomain(d) {
			return RouteRule{}, "不是有效的域名"
		}
		return RouteRule{Kind: "full", Value: d}, ""
	case "keyword":
		if !validDomainChars(d, true) || len(strings.Trim(d, "-_.")) < 2 {
			return RouteRule{}, "关键词至少 2 个字，只能有字母、数字、- _ ."
		}
		return RouteRule{Kind: "keyword", Value: d}, ""
	}
	for _, p := range []string{"+.", "*.", "."} {
		if strings.HasPrefix(d, p) {
			d = d[len(p):]
			kind = "suffix"
			break
		}
	}
	if kind == "" && !strings.Contains(d, ".") {
		if !validDomainChars(d, false) || len(strings.Trim(d, "-_")) < 2 {
			return RouteRule{}, "不认识的写法（一行写一个域名、IP 或关键词）"
		}
		return RouteRule{Kind: "keyword", Value: d}, ""
	}
	if !validDomain(d) {
		return RouteRule{}, "不认识的写法（一行写一个域名、IP 或关键词）"
	}
	return RouteRule{Kind: "suffix", Value: d}, ""
}

func ipRule(p netip.Prefix) RouteRule {
	if p.Addr().Is4() {
		return RouteRule{Kind: "ip4", Value: p.String()}
	}
	return RouteRule{Kind: "ip6", Value: p.String()}
}

// Line is the stored (round-trip stable) form of a rule.
func (r RouteRule) Line() string {
	switch r.Kind {
	case "full":
		return "full:" + r.Value
	case "keyword":
		if strings.Contains(r.Value, ".") {
			return "keyword:" + r.Value
		}
		return r.Value
	case "ip4", "ip6":
		if p, err := netip.ParsePrefix(r.Value); err == nil && p.IsSingleIP() {
			return p.Addr().String()
		}
	}
	return r.Value
}

// ParseRouteLines parses a custom entry's text (one rule per line, empty
// lines and # comments skipped, duplicates dropped). The error names the
// first bad line.
func ParseRouteLines(lines []string) ([]RouteRule, error) {
	out := make([]RouteRule, 0, len(lines))
	seen := map[RouteRule]bool{}
	for i, raw := range lines {
		for _, part := range strings.Split(raw, "\n") {
			s := strings.TrimSpace(strings.TrimSuffix(part, "\r"))
			if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
				continue
			}
			r, why := ParseRouteLine(s)
			if why != "" {
				return nil, &RouteLineError{Line: i + 1, Text: s, Why: why}
			}
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// routeRules parses already cleaned stored lines (bad ones are skipped).
func routeRules(lines []string) []RouteRule {
	out := make([]RouteRule, 0, len(lines))
	for _, l := range lines {
		if r, why := ParseRouteLine(strings.TrimSpace(l)); why == "" {
			out = append(out, r)
		}
	}
	return out
}

// CleanRouting validates a list from the editor and normalizes it: custom
// entries first (in the given order), then builtins in rule order, defaults
// filled in, lines stored in their canonical form. inSub reports whether a
// node: exit names a node of this subscription; one that does not becomes
// 节点选择 (the editor shows such a row in yellow before saving). nodeNames
// are the subscription's node names (custom names may not take one).
func CleanRouting(in []RouteEntry, inSub func(key string) bool, nodeNames map[string]bool) ([]RouteEntry, error) {
	if len(in) > MaxRouteEntries {
		return nil, fmt.Errorf("每条订阅最多 %d 个分流，现在是 %d 个", MaxRouteEntries, len(in))
	}
	var custom []RouteEntry
	builtin := make([]*RouteEntry, len(Catalog))
	names := map[string]bool{}
	total := 0
	for _, e := range in {
		exit := strings.TrimSpace(e.Exit)
		switch e.Type {
		case RouteBuiltin:
			i, ok := catalogIndex[e.Key]
			if !ok {
				return nil, errors.New("分流设置无效，请刷新页面后重试")
			}
			if builtin[i] != nil {
				continue
			}
			if exit == "" {
				exit = Catalog[i].Exit
			}
			builtin[i] = &RouteEntry{Type: RouteBuiltin, Key: e.Key, Exit: exit}
		case RouteCustom:
			name, err := CleanRouteName(e.Name)
			if err != nil {
				return nil, err
			}
			if names[name] {
				return nil, fmt.Errorf("「%s」这个名字已经用过了", name)
			}
			if nodeNames[name] || nodeNames[customIcon+" "+name] {
				return nil, fmt.Errorf("「%s」和订阅里的节点同名，请换一个名字", name)
			}
			names[name] = true
			rules, err := ParseRouteLines(e.Lines)
			if err != nil {
				return nil, fmt.Errorf("「%s」%s", name, err.Error())
			}
			if len(rules) == 0 {
				return nil, fmt.Errorf("「%s」还没有写网站或 IP", name)
			}
			total += len(rules)
			if total > MaxRouteLines {
				return nil, fmt.Errorf("自己写的分流合计最多 %d 行", MaxRouteLines)
			}
			lines := make([]string, len(rules))
			for i, r := range rules {
				lines[i] = r.Line()
			}
			if exit == "" {
				exit = ExitSelect
			}
			custom = append(custom, RouteEntry{Type: RouteCustom, Name: name, Lines: lines, Exit: exit})
		default:
			return nil, errors.New("分流设置无效，请刷新页面后重试")
		}
	}
	out := make([]RouteEntry, 0, len(in))
	out = append(out, custom...)
	for _, b := range builtin {
		if b != nil {
			out = append(out, *b)
		}
	}
	for i := range out {
		e := &out[i]
		if !validExit(e.Exit) {
			return nil, errors.New("分流的「走哪里」无效，请重新选择")
		}
		if k, ok := strings.CutPrefix(e.Exit, ExitNodePrefix); ok && (inSub == nil || !inSub(k)) {
			e.Exit = ExitSelect
		}
	}
	return out, nil
}

// ------------------------------------------------------------ rendering

// routeGroup is one entry as a renderer needs it.
type routeGroup struct {
	name   string // group name (format-safe names are derived per format)
	exit   string // select | auto | direct | reject | node
	node   string // exit node name (exit == "node")
	custom []RouteRule
	sets   []RuleSet
}

// planRoutes resolves a stored list against the nodes one format renders.
// nodeName maps a node key to its rendered name; a node: exit whose node is
// not rendered falls back to 节点选择, an auto exit without nodes too.
// Unknown builtins and custom entries without usable lines are skipped.
func planRoutes(list []RouteEntry, nodeName map[string]string, haveNodes bool) []routeGroup {
	out := make([]routeGroup, 0, len(list))
	// Rule order is fixed: custom entries (list order), then the catalog
	// order, whatever order the stored list has; each builtin once.
	builtin := make([]*routeGroup, len(Catalog))
	for _, e := range list {
		g := routeGroup{exit: e.Exit}
		bi := -1
		switch e.Type {
		case RouteBuiltin:
			i, ok := catalogIndex[e.Key]
			if !ok || builtin[i] != nil {
				continue
			}
			bi = i
			g.name, g.sets = Catalog[i].GroupName(), Catalog[i].Sets
		case RouteCustom:
			// Checked again: the stored JSON is the only input here.
			name, err := CleanRouteName(e.Name)
			if err != nil {
				continue
			}
			g.name, g.custom = customIcon+" "+name, routeRules(e.Lines)
			if len(g.custom) == 0 {
				continue
			}
		default:
			continue
		}
		if k, ok := strings.CutPrefix(e.Exit, ExitNodePrefix); ok {
			if n := nodeName[k]; n != "" {
				g.exit, g.node = "node", n
			} else {
				g.exit = ExitSelect
			}
		}
		switch g.exit {
		case ExitSelect, ExitDirect, ExitReject, "node":
		case ExitAuto:
			if !haveNodes {
				g.exit = ExitSelect
			}
		default:
			g.exit = ExitSelect
		}
		if bi >= 0 {
			builtin[bi] = &g
			continue
		}
		out = append(out, g)
	}
	for _, g := range builtin {
		if g != nil {
			out = append(out, *g)
		}
	}
	return out
}

// routeNodeNames maps node keys to names for the nodes a format kept.
func routeNodeNames(nodes []Node, names []string) map[string]string {
	m := make(map[string]string, len(nodes))
	for i, n := range nodes {
		if n.Key != "" {
			if _, dup := m[n.Key]; !dup {
				m[n.Key] = names[i]
			}
		}
	}
	return m
}

// routeGroupNames: the group names a list renders (cheap: no line parsing).
func routeGroupNames(list []RouteEntry) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		switch e.Type {
		case RouteBuiltin:
			if c, ok := CategoryByKey(e.Key); ok {
				out = append(out, c.GroupName())
			}
		case RouteCustom:
			if name, err := CleanRouteName(e.Name); err == nil {
				out = append(out, customIcon+" "+name)
			}
		}
	}
	return out
}

// avoidGroupNames renames nodes whose name is a routing group name (the
// client would see two things with one name). It returns nodes unchanged
// when nothing collides, else a renamed copy.
func avoidGroupNames(nodes []Node, groups []string) []Node {
	if len(groups) == 0 {
		return nodes
	}
	taken := map[string]bool{}
	for _, g := range groups {
		taken[g] = true
	}
	hit := false
	for _, n := range nodes {
		if taken[n.Name] {
			hit = true
			break
		}
	}
	if !hit {
		return nodes
	}
	out := append([]Node(nil), nodes...)
	used := map[string]bool{}
	for _, n := range out {
		used[n.Name] = true
	}
	for i := range out {
		if !taken[out[i].Name] {
			continue
		}
		name := out[i].Name + " · 节点"
		for used[name] || taken[name] {
			name += "'"
		}
		used[name] = true
		out[i].Name = name
	}
	return out
}

// groupMembers: exit first, then 节点选择, 自动选择, the nodes, DIRECT; REJECT
// only for a reject exit. sel / auto / direct / reject are the format's
// names ("" = absent, e.g. no 自动选择 without nodes).
func groupMembers(g routeGroup, sel, auto, direct, reject string, nodes []string) []string {
	first := sel
	switch g.exit {
	case ExitAuto:
		first = auto
	case ExitDirect:
		first = direct
	case ExitReject:
		first = reject
	case "node":
		first = g.node
	}
	out := make([]string, 0, len(nodes)+4)
	out = append(out, first)
	add := func(s string) {
		if s != "" && s != first {
			out = append(out, s)
		}
	}
	add(sel)
	add(auto)
	for _, n := range nodes {
		add(n)
	}
	add(direct)
	return out
}
