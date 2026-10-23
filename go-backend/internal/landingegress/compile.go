package landingegress

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// NodeRef is one landing node on the server being compiled.
type NodeRef struct {
	ID       int64
	Tag      string
	Override *int64 // nil follows the server profile
	// Plan 134: the owner's node rules (normalized) and default outbound
	// ("" follows the upper layers); used only when CompileInput.NodeRules.
	Rules   []Rule
	Default string
}

// CompileInput carries everything Compile needs; Resolve turns a template ID
// into a concrete outbound for this server (WARP account, upstream node creds).
type CompileInput struct {
	ServerProfileID  int64
	Nodes            []NodeRef
	Profiles         map[int64]Profile
	Resolve          func(outboundID int64) (PlanOutbound, error)
	Exempt           func() ([]string, error)
	AllowCoreRestart bool
	GeoIP, GeoSite   bool
	// Abuse emits the built-in anti-abuse rules (plan 127) for every node
	// whose profile keeps them. False while the server cannot take plan rules
	// yet (native Core without RoutingService and no restart confirmed): the
	// payload then stays exactly as before instead of pausing nodes.
	Abuse bool
	// Builtin: the agent understands PlanRule.Builtin (renders the rule before
	// chain rules and never pauses nodes for it). Only then are ChainTags
	// (chain copies on this server, plan 108) covered, following the server
	// profile's switch; older agents order chain rules first anyway.
	Builtin   bool
	ChainTags []string
	// AbuseOff: the admin's global switches of the default rules (plan 140);
	// the zero value keeps both on (pre-140 output).
	AbuseOff AbuseSwitches
	// Plan 134 layers. Global layers are sent only when Layers (the same
	// readiness as Abuse); node rules only when NodeRules (otherwise they stay
	// queued until the Core can hot-apply them). Markers: the agent knows
	// BuiltinForced / BuiltinGlobal (native landing_guard >= LayersVersion):
	// 全局·强制 then also covers ChainTags and global layers fail open.
	// Outbounds and NoIPv4 / NoIPv6 drive the IP-version safety of the
	// global and node layers.
	Global    *Global
	Layers    bool
	NodeRules bool
	Markers   bool
	Outbounds map[int64]Outbound
	NoIPv4    bool
	NoIPv6    bool
}

// Report says what of the layered input did not make it into the plan.
type Report struct {
	Warnings []string
	// NodeNotes: per node, why (part of) its own rules are not applied.
	NodeNotes map[int64][]string
	// GlobalNotes: global rules skipped on this server.
	GlobalNotes []string
}

func (r *Report) note(id int64, msg string) {
	for _, m := range r.NodeNotes[id] {
		if m == msg {
			return
		}
	}
	r.NodeNotes[id] = append(r.NodeNotes[id], msg)
}

// OutboundTag is the plan tag of a template.
func OutboundTag(id int64) string { return "flvxo-" + strconv.FormatInt(id, 10) }

// Layer of a plan rule, used for fallbacks only (never sent).
const (
	layerServer = iota // profile rules and profile defaults (pre-134 output)
	layerForced        // 全局·强制
	layerNode          // node rules and node defaults
	layerGlobal        // 全局·默认 and the 全局默认出站
)

type groupRule struct {
	key   string
	rule  PlanRule
	tags  []string
	layer int
}

func ruleKey(r PlanRule) string {
	r.Name, r.InboundTags = "", nil
	b, _ := json.Marshal(r)
	return string(b)
}

// layerKey keeps rules of different layers apart when merging; server rules
// keep the bare key so pre-134 plans merge exactly as before.
func layerKey(layer int, r PlanRule) string {
	switch layer {
	case layerNode:
		return "n:" + ruleKey(r)
	case layerGlobal:
		return "g:" + ruleKey(r)
	case layerForced:
		return "f:" + ruleKey(r)
	}
	return ruleKey(r)
}

func actionTarget(action string) (string, int64) {
	if id, ok := ActionOutbound(action); ok {
		return OutboundTag(id), id
	}
	return action, 0
}

// EffectiveProfile resolves a node's profile (0 = built-in default).
func EffectiveProfile(serverProfile int64, override *int64) int64 {
	if override != nil {
		return *override
	}
	return serverProfile
}

// limitError marks plan-size errors that dropping node rules (and then the
// global layers) can resolve.
type limitError struct{ msg string }

func (e limitError) Error() string { return e.msg }

// Compile builds the server plan; see CompileLayers.
func Compile(in CompileInput) (*Plan, []string, error) {
	plan, rep, err := CompileLayers(in)
	return plan, rep.Warnings, err
}

// CompileLayers builds the server plan. Nodes on the built-in default produce
// only the default anti-abuse rules (when in.Abuse); every rule is scoped by
// inbound tags, so profiles never interfere. Identical leading rules of
// different profiles are merged into one rule; the default rules come first.
// Without global layers and node rules the plan is exactly the pre-134 plan.
// Global and node layers never take a server down: rules the server cannot
// carry (geo data, IP family, plan size) are skipped and reported instead;
// plan-size overflows drop node rules first (highest node ID first), then the
// global layers.
func CompileLayers(in CompileInput) (*Plan, Report, error) {
	rep := Report{NodeNotes: map[int64][]string{}}
	forced, deflt, gdef := in.globalLayer(&rep)
	nodes := make([]NodeRef, len(in.Nodes))
	copy(nodes, in.Nodes)
	for i := range nodes {
		in.nodeLayer(&nodes[i], &rep)
	}
	dropGlobal := false
	for {
		f, d, g := forced, deflt, gdef
		if dropGlobal {
			f, d, g = nil, nil, ActionDirect
		}
		plan, warnings, err := compilePlan(in, nodes, f, d, g)
		var le limitError
		if err == nil || !errors.As(err, &le) {
			rep.Warnings = append(rep.Warnings, warnings...)
			rep.Warnings = append(rep.Warnings, rep.GlobalNotes...)
			if len(rep.NodeNotes) > 0 {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d 个节点的节点规则未完全生效（见节点的路由规则）", len(rep.NodeNotes)))
			}
			return plan, rep, err
		}
		drop := -1
		for i := range nodes {
			if (len(nodes[i].Rules) > 0 || nodes[i].Default != "") && (drop < 0 || nodes[i].ID > nodes[drop].ID) {
				drop = i
			}
		}
		if drop >= 0 {
			rep.note(nodes[drop].ID, "服务器规则容量已满（"+le.msg+"），本节点的节点规则未生效")
			nodes[drop].Rules, nodes[drop].Default = nil, ""
			continue
		}
		if !dropGlobal && (len(forced) > 0 || len(deflt) > 0 || gdef != ActionDirect) {
			dropGlobal = true
			rep.GlobalNotes = append(rep.GlobalNotes, "全局规则未下发："+le.msg)
			continue
		}
		rep.Warnings = append(rep.Warnings, warnings...)
		rep.Warnings = append(rep.Warnings, rep.GlobalNotes...)
		return nil, rep, err
	}
}

// globalLayer: the global rules and the 全局默认出站 this server applies
// (plan 134). Shared by CompileLayers and EffectiveDefault (plan 139), so a
// label never disagrees with the plan.
func (in CompileInput) globalLayer(rep *Report) (forced, deflt []Rule, gdef string) {
	gdef = ActionDirect
	if !in.Layers || !in.Global.Active() {
		return nil, nil, gdef
	}
	forced = rep.filterGlobal(in, enabled(in.Global.Forced))
	deflt = rep.filterGlobal(in, enabled(in.Global.Default))
	gdef = in.Global.DefaultAction
	if gdef == "" {
		gdef = ActionDirect
	}
	if reason := in.familyConflict(gdef); reason != "" {
		rep.GlobalNotes = append(rep.GlobalNotes, "全局默认出站："+reason+"，本服务器改为直连")
		gdef = ActionDirect
	}
	return forced, deflt, gdef
}

// nodeLayer trims a node's own rules and default to what the server applies.
func (in CompileInput) nodeLayer(n *NodeRef, rep *Report) {
	if !in.NodeRules {
		n.Rules, n.Default = nil, ""
		return
	}
	n.Rules = enabled(n.Rules)
	if ip, site := RulesUseGeo(n.Rules); (ip && !in.GeoIP) || (site && !in.GeoSite) {
		rep.note(n.ID, "该服务器没有 geo 数据，使用 geoip / geosite 的节点规则未生效")
		n.Rules = nil
	}
	kept := []Rule{}
	for _, r := range n.Rules {
		if reason := in.familyConflict(r.Action); reason != "" {
			rep.note(n.ID, fmt.Sprintf("规则「%s」：%s，未生效", r.Name, reason))
			continue
		}
		kept = append(kept, r)
	}
	n.Rules = kept
	if reason := in.familyConflict(n.Default); reason != "" {
		rep.note(n.ID, "节点默认出站："+reason+"，改为跟随上级")
		n.Default = ""
	}
}

// nodeDefault: node default > profile default (pid != 0) > global default.
func nodeDefault(n NodeRef, pid int64, profiles map[int64]Profile, gdef string) (string, int) {
	def, layer := ActionDirect, layerServer
	switch {
	case n.Default != "":
		def, layer = n.Default, layerNode
	case pid != 0:
		def = profiles[pid].DefaultAction
	default:
		def, layer = gdef, layerGlobal
	}
	if def == "" {
		def = ActionDirect
	}
	return def, layer
}

// Default sources reported by EffectiveDefault.
const (
	DefaultFromNode   = "node"
	DefaultFromServer = "server"
	DefaultFromGlobal = "global"
)

// EffectiveDefault resolves the default outbound of a node (node == nil: of
// the server, i.e. a node without own rules) exactly like CompileLayers, and
// whether any rule layer (global rules, node rules, profile rules) routes
// traffic to another outbound. Block-only rules and the built-in anti-abuse
// rules (SMTP/BT) do not count.
func (in CompileInput) EffectiveDefault(node *NodeRef) (action, from string, rules bool) {
	rep := Report{NodeNotes: map[int64][]string{}}
	forced, deflt, gdef := in.globalLayer(&rep)
	var n NodeRef
	if node != nil {
		n = *node
		in.nodeLayer(&n, &rep)
	}
	pid := EffectiveProfile(in.ServerProfileID, n.Override)
	rules = routesTraffic(forced) || routesTraffic(deflt) || routesTraffic(n.Rules)
	if p, ok := in.Profiles[pid]; pid != 0 && ok {
		rules = rules || routesTraffic(enabled(p.Rules))
	}
	def, layer := nodeDefault(n, pid, in.Profiles, gdef)
	switch layer {
	case layerNode:
		from = DefaultFromNode
	case layerGlobal:
		from = DefaultFromGlobal
	default:
		from = DefaultFromServer
	}
	return def, from, rules
}

// routesTraffic: some rule sends matching traffic to another outbound. Pure
// block rules (e.g. 屏蔽大陆 IP, 屏蔽发信端口) only filter; allowed traffic still
// leaves through the default outbound, so they do not make a label 「按规则分流」.
func routesTraffic(rules []Rule) bool {
	for _, r := range rules {
		if r.Enabled && r.Action != ActionBlock {
			return true
		}
	}
	return false
}

// EffectiveLabel is the read-only outbound summary users see for a server
// (node == nil) or one node, e.g. 「强制 IPv4」 or 「按规则分流」.
func (in CompileInput) EffectiveLabel(node *NodeRef) string {
	pid := in.ServerProfileID
	if node != nil {
		pid = EffectiveProfile(pid, node.Override)
	}
	if _, ok := in.Profiles[pid]; pid != 0 && !ok {
		return "直连" // unknown profile: as Label always showed
	}
	action, _, rules := in.EffectiveDefault(node)
	if rules {
		return "按规则分流"
	}
	return ActionLabel(action, in.Outbounds)
}

// filterGlobal drops global rules this server cannot carry (geo data, IP
// family of a forced outbound) and records why.
func (r *Report) filterGlobal(in CompileInput, rules []Rule) []Rule {
	out := []Rule{}
	for _, rule := range rules {
		if ip, site := RulesUseGeo([]Rule{rule}); (ip && !in.GeoIP) || (site && !in.GeoSite) {
			r.GlobalNotes = append(r.GlobalNotes, fmt.Sprintf("全局规则「%s」：该服务器没有 geo 数据，未生效", rule.Name))
			continue
		}
		if reason := in.familyConflict(rule.Action); reason != "" {
			r.GlobalNotes = append(r.GlobalNotes, fmt.Sprintf("全局规则「%s」：%s，未生效", rule.Name, reason))
			continue
		}
		out = append(out, rule)
	}
	return out
}

// familyConflict reports a template forcing an IP family the server lacks.
// WARP brings its own addresses (checked when resolving the account).
func (in CompileInput) familyConflict(action string) string {
	id, ok := ActionOutbound(action)
	if !ok {
		return ""
	}
	o, ok := in.Outbounds[id]
	if !ok || o.Type == TypeWarp {
		return ""
	}
	switch {
	case o.IPVersion == "force_ipv4" && in.NoIPv4:
		return fmt.Sprintf("出站「%s」强制 IPv4，但该服务器未检测到 IPv4 地址", o.Name)
	case o.IPVersion == "force_ipv6" && in.NoIPv6:
		return fmt.Sprintf("出站「%s」强制 IPv6，但该服务器未检测到 IPv6 地址", o.Name)
	}
	return ""
}

func toPlanRule(r Rule) PlanRule {
	return PlanRule{Name: r.Name, Domain: r.Domains, IP: r.IPs, Port: r.Ports, SourceIP: r.SourceIPs, SourcePort: r.SourcePorts, Network: r.Network, Protocol: r.Protocols}
}

// mergeLists interleaves rule lists with disjoint scopes. Any interleaving
// that keeps each list's own order is equivalent; heads shared by 2+ lists are
// emitted first and merged into one rule.
func mergeLists(lists [][]groupRule) []groupRule {
	out := []groupRule{}
	heads := make([]int, len(lists))
	for {
		counts := map[string]int{}
		order := []string{}
		for i, list := range lists {
			if heads[i] < len(list) {
				k := list[heads[i]].key
				if counts[k] == 0 {
					order = append(order, k)
				}
				counts[k]++
			}
		}
		if len(order) == 0 {
			return out
		}
		pick := order[0]
		for _, k := range order {
			if counts[k] > 1 {
				pick = k
				break
			}
		}
		var merged *groupRule
		tagSet := map[string]bool{}
		for i, list := range lists {
			if heads[i] < len(list) && list[heads[i]].key == pick {
				g := list[heads[i]]
				if merged == nil {
					merged = &g
				}
				for _, t := range g.tags {
					tagSet[t] = true
				}
				heads[i]++
				if counts[pick] == 1 {
					break
				}
			}
		}
		g := *merged
		g.rule.InboundTags = nil
		for t := range tagSet {
			g.rule.InboundTags = append(g.rule.InboundTags, t)
		}
		sort.Strings(g.rule.InboundTags)
		out = append(out, g)
	}
}

func defaultRuleName(layer int) string {
	switch layer {
	case layerNode:
		return "节点默认出站"
	case layerGlobal:
		return "全局默认出站"
	}
	return "默认出站"
}

func compilePlan(in CompileInput, nodes []NodeRef, forced, deflt []Rule, gdef string) (*Plan, []string, error) {
	plan := &Plan{Version: Version, AllowCoreRestart: in.AllowCoreRestart, Outbounds: []PlanOutbound{}, Rules: []PlanRule{}}
	layers := []int{}
	emit := func(g groupRule) {
		plan.Rules = append(plan.Rules, g.rule)
		layers = append(layers, g.layer)
	}
	type groupKey struct {
		pid   int64
		layer int
		def   string
	}
	groups := map[groupKey][]string{}
	nodeTag := map[int64]string{}
	allTags := []string{}
	for _, n := range nodes {
		nodeTag[n.ID] = n.Tag
		allTags = append(allTags, n.Tag)
		pid := EffectiveProfile(in.ServerProfileID, n.Override)
		def, layer := nodeDefault(n, pid, in.Profiles, gdef)
		if pid == 0 && def == ActionDirect {
			continue
		}
		k := groupKey{pid, layer, def}
		groups[k] = append(groups[k], n.Tag)
	}
	sort.Strings(allTags)
	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.pid != b.pid {
			return a.pid < b.pid
		}
		if a.layer != b.layer {
			return a.layer < b.layer
		}
		return a.def < b.def
	})
	// With 全局·默认 rules the defaults go after them, in their own block.
	separate := len(deflt) > 0
	var lists [][]groupRule
	var defaults []groupRule
	referenced := map[int64]bool{}
	refer := func(action string) string {
		target, id := actionTarget(action)
		if id > 0 {
			referenced[id] = true
		}
		return target
	}
	for _, k := range keys {
		tags := groups[k]
		sort.Strings(tags)
		var list []groupRule
		if k.pid != 0 {
			p, ok := in.Profiles[k.pid]
			if !ok {
				return nil, nil, fmt.Errorf("路由方案 %d 不存在", k.pid)
			}
			abuse := in.Abuse && AbuseOn(k.pid, in.Profiles)
			inGroup := map[string]bool{}
			for _, t := range tags {
				inGroup[t] = true
			}
			for _, r := range p.Rules {
				if !r.Enabled || (abuse && in.AbuseOff.On(AbuseDuplicateOf(r))) {
					// A profile rule that only repeats a default rule (e.g. the
					// 「屏蔽 BT」 preset) is already covered right after safety.
					continue
				}
				scope := tags
				if len(r.LandingNodeIDs) > 0 {
					scope = nil
					for _, id := range r.LandingNodeIDs {
						if t := nodeTag[id]; t != "" && inGroup[t] {
							scope = append(scope, t)
						}
					}
					if len(scope) == 0 {
						continue
					}
				}
				pr := toPlanRule(r)
				pr.Outbound = refer(r.Action)
				list = append(list, groupRule{key: ruleKey(pr), rule: pr, tags: scope, layer: layerServer})
			}
		}
		if k.def != ActionDirect {
			pr := PlanRule{Name: defaultRuleName(k.layer), Outbound: refer(k.def)}
			if k.layer == layerGlobal && in.Markers {
				pr.Builtin = BuiltinGlobal
			}
			g := groupRule{key: layerKey(k.layer, pr), rule: pr, tags: tags, layer: k.layer}
			if separate {
				defaults = append(defaults, g)
			} else {
				list = append(list, g)
			}
		}
		if len(list) > 0 {
			lists = append(lists, list)
		}
	}
	// Layer 2: 全局·强制 over every node, and over chain copies when the agent
	// renders forced rules before the chain rules.
	forcedTags := append([]string(nil), allTags...)
	if in.Markers {
		forcedTags = append(forcedTags, in.ChainTags...)
		sort.Strings(forcedTags)
	}
	if len(forcedTags) > 0 {
		for _, r := range forced {
			pr := toPlanRule(r)
			pr.Outbound = refer(r.Action)
			pr.InboundTags = append([]string(nil), forcedTags...)
			if in.Markers {
				pr.Builtin = BuiltinForced
			}
			emit(groupRule{rule: pr, layer: layerForced})
		}
	}
	// Layer 3: node rules. Identical leading rules of different nodes merge,
	// so a category several owners pick is streamed into the Core once.
	ordered := append([]NodeRef(nil), nodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var nodeLists [][]groupRule
	for _, n := range ordered {
		var list []groupRule
		for _, r := range n.Rules {
			pr := toPlanRule(r)
			pr.Outbound = refer(r.Action)
			list = append(list, groupRule{key: layerKey(layerNode, pr), rule: pr, tags: []string{n.Tag}, layer: layerNode})
		}
		if len(list) > 0 {
			nodeLists = append(nodeLists, list)
		}
	}
	for _, g := range mergeLists(nodeLists) {
		emit(g)
	}
	// Layer 4 (with the defaults when there is no 全局·默认): server profiles.
	for _, g := range mergeLists(lists) {
		emit(g)
	}
	// Layer 5: 全局·默认 over every non-chain node.
	if len(allTags) > 0 {
		for _, r := range deflt {
			pr := toPlanRule(r)
			pr.Outbound = refer(r.Action)
			pr.InboundTags = append([]string(nil), allTags...)
			if in.Markers {
				pr.Builtin = BuiltinGlobal
			}
			emit(groupRule{rule: pr, layer: layerGlobal})
		}
	}
	// Layer 6 after 全局·默认: one rule per distinct default.
	if separate {
		index := map[string]int{}
		var merged []groupRule
		for _, g := range defaults {
			if i, ok := index[g.key]; ok {
				merged[i].tags = append(merged[i].tags, g.tags...)
				continue
			}
			index[g.key] = len(merged)
			g.tags = append([]string(nil), g.tags...)
			merged = append(merged, g)
		}
		// Node defaults, then server profile defaults, then the global one.
		rank := map[int]int{layerNode: 0, layerServer: 1, layerGlobal: 2}
		sort.SliceStable(merged, func(i, j int) bool { return rank[merged[i].layer] < rank[merged[j].layer] })
		for _, g := range merged {
			sort.Strings(g.tags)
			g.rule.InboundTags = g.tags
			emit(g)
		}
	}
	layered := false
	for _, l := range layers {
		layered = layered || l != layerServer
	}
	var warnings []string
	if in.Abuse {
		var abuseTags []string
		for _, n := range nodes {
			if AbuseOn(EffectiveProfile(in.ServerProfileID, n.Override), in.Profiles) {
				abuseTags = append(abuseTags, n.Tag)
			}
		}
		if in.Builtin && AbuseOn(in.ServerProfileID, in.Profiles) {
			abuseTags = append(abuseTags, in.ChainTags...)
		}
		// Only the default rules switched on globally (plan 140).
		defaults := AbuseRules(in.AbuseOff)
		if len(abuseTags) > 0 && len(defaults) > 0 {
			sort.Strings(abuseTags)
			if len(plan.Rules)+len(defaults) > MaxPlanRules {
				if layered {
					// Global and node rules never push the defaults out.
					return nil, nil, limitError{fmt.Sprintf("同一服务器的规则合计最多 %d 条", MaxPlanRules)}
				}
				// Never turn a valid plan into a rejected one (which would
				// block nodes): skip the defaults and say so.
				warnings = append(warnings, "该服务器的规则数已满，默认防滥用规则（屏蔽 SMTP 25 / BT）未下发")
			} else {
				for i := range defaults {
					defaults[i].InboundTags = append([]string(nil), abuseTags...)
					if in.Builtin {
						defaults[i].Builtin = BuiltinAbuse
					}
				}
				plan.Rules = append(defaults, plan.Rules...)
				layers = append(make([]int, len(defaults)), layers...)
			}
		}
	}
	// Resolve templates actually referenced. Failures route to block, except
	// in global layers, which fall back to direct (never block traffic).
	ids := make([]int64, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	failed := map[string]bool{}
	var failures []error
	var failedIDs []int64
	for _, id := range ids {
		o, err := in.Resolve(id)
		if err != nil {
			failed[OutboundTag(id)] = true
			failures = append(failures, err)
			failedIDs = append(failedIDs, id)
			continue
		}
		o.Tag = OutboundTag(id)
		plan.Outbounds = append(plan.Outbounds, o)
	}
	globalRef, otherRef := map[string]bool{}, map[string]bool{}
	for i := range plan.Rules {
		tag := plan.Rules[i].Outbound
		if !failed[tag] {
			continue
		}
		if layers[i] == layerForced || layers[i] == layerGlobal {
			globalRef[tag] = true
			plan.Rules[i].Outbound = ActionDirect
		} else {
			otherRef[tag] = true
			plan.Rules[i].Outbound = ActionBlock
		}
	}
	for i, id := range failedIDs {
		tag := OutboundTag(id)
		if otherRef[tag] || !globalRef[tag] {
			warnings = append(warnings, failures[i].Error())
		}
		if globalRef[tag] {
			reason := strings.TrimSuffix(failures[i].Error(), "，使用它的流量暂时封锁")
			warnings = append(warnings, "全局规则："+reason+"，全局规则中使用它的流量暂时改为直连")
		}
	}
	// Limits and capabilities.
	items, geoBytes := 0, 0
	sources := false
	for _, r := range plan.Rules {
		ip, site := usesGeo(r.IP, r.SourceIP)
		_, site2 := usesGeo(r.Domain)
		if ip && !in.GeoIP {
			return nil, warnings, errors.New("该服务器没有 geoip 数据，不能使用 geoip 规则")
		}
		if (site || site2) && !in.GeoSite {
			return nil, warnings, errors.New("该服务器没有 geosite 数据，不能使用 geosite 规则")
		}
		rr := Rule{Domains: r.Domain, IPs: r.IP, SourceIPs: r.SourceIP, Protocols: r.Protocol, Ports: r.Port, SourcePorts: r.SourcePort}
		items += RuleItems(rr)
		geoBytes += RuleGeoBytes(rr)
		sources = sources || len(r.SourceIP) > 0
	}
	switch {
	case len(plan.Outbounds) > MaxPlanOutbounds:
		return nil, warnings, limitError{fmt.Sprintf("同一服务器最多使用 %d 个出站模板", MaxPlanOutbounds)}
	case len(plan.Rules) > MaxPlanRules:
		return nil, warnings, limitError{fmt.Sprintf("同一服务器的规则合计最多 %d 条", MaxPlanRules)}
	case items > MaxPlanItems:
		return nil, warnings, limitError{fmt.Sprintf("同一服务器的匹配条目合计最多 %d 条", MaxPlanItems)}
	case geoBytes > MaxPlanGeoBytes:
		return nil, warnings, limitError{"同一服务器上的 geo 分类合计过大（超过 3 MiB），请让节点使用同一个方案或改用更小的分类"}
	}
	if sources && in.Exempt != nil {
		exempt, err := in.Exempt()
		if err != nil {
			return nil, warnings, err
		}
		if len(exempt) > MaxExemptSources {
			return nil, warnings, fmt.Errorf("来源豁免地址超过 %d 条", MaxExemptSources)
		}
		plan.ExemptSources = exempt
	}
	return plan, warnings, nil
}

// Label is the read-only summary shown to users, e.g. 「出站：WARP」.
func Label(profileID int64, profiles map[int64]Profile, outbounds map[int64]Outbound) string {
	p, ok := profiles[profileID]
	if profileID == 0 || !ok {
		return "直连"
	}
	for _, r := range p.Rules {
		if r.Enabled {
			return "按规则分流"
		}
	}
	return ActionLabel(p.DefaultAction, outbounds)
}

// ActionLabel names an action without revealing upstream details.
func ActionLabel(action string, outbounds map[int64]Outbound) string {
	switch action {
	case ActionDirect, "":
		return "直连"
	case ActionBlock:
		return "封锁"
	}
	id, _ := ActionOutbound(action)
	o, ok := outbounds[id]
	if !ok {
		return "直连"
	}
	switch o.Type {
	case TypeWarp:
		return "WARP"
	case TypeProxy:
		return "代理"
	case TypeLandingNode:
		return "FLVX 落地"
	}
	switch o.IPVersion {
	case "force_ipv4":
		return "强制 IPv4"
	case "force_ipv6":
		return "强制 IPv6"
	}
	return "直连"
}

// BlockPlan is the fail-closed fallback when a server's plan cannot be
// compiled at sync time: nodes that were meant to follow a profile are blocked
// instead of silently leaving through the default outbound.
func BlockPlan(tags []string, allowRestart bool) *Plan {
	if len(tags) == 0 {
		return nil
	}
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	return &Plan{Version: Version, AllowCoreRestart: allowRestart, Outbounds: []PlanOutbound{}, Rules: []PlanRule{{Name: "出站与路由异常，暂停转发", InboundTags: sorted, Outbound: ActionBlock}}}
}
