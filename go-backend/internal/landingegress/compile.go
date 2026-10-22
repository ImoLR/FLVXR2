package landingegress

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// NodeRef is one landing node on the server being compiled.
type NodeRef struct {
	ID       int64
	Tag      string
	Override *int64 // nil follows the server profile
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
}

// OutboundTag is the plan tag of a template.
func OutboundTag(id int64) string { return "flvxo-" + strconv.FormatInt(id, 10) }

type groupRule struct {
	key  string
	rule PlanRule
	tags []string
}

func ruleKey(r PlanRule) string {
	r.Name, r.InboundTags = "", nil
	b, _ := json.Marshal(r)
	return string(b)
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

// Compile builds the server plan. Nodes on the built-in default produce only
// the default anti-abuse rules (when in.Abuse); every rule is scoped by
// inbound tags, so profiles never interfere. Identical leading rules of
// different profiles are merged into one rule; the default rules come first.
func Compile(in CompileInput) (*Plan, []string, error) {
	plan := &Plan{Version: Version, AllowCoreRestart: in.AllowCoreRestart, Outbounds: []PlanOutbound{}, Rules: []PlanRule{}}
	groups := map[int64][]string{}
	nodeTag := map[int64]string{}
	for _, n := range in.Nodes {
		nodeTag[n.ID] = n.Tag
		if pid := EffectiveProfile(in.ServerProfileID, n.Override); pid != 0 {
			groups[pid] = append(groups[pid], n.Tag)
		}
	}
	pids := make([]int64, 0, len(groups))
	for pid := range groups {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	var lists [][]groupRule
	referenced := map[int64]bool{}
	for _, pid := range pids {
		p, ok := in.Profiles[pid]
		if !ok {
			return nil, nil, fmt.Errorf("路由方案 %d 不存在", pid)
		}
		tags := groups[pid]
		sort.Strings(tags)
		abuse := in.Abuse && AbuseOn(pid, in.Profiles)
		inGroup := map[string]bool{}
		for _, t := range tags {
			inGroup[t] = true
		}
		var list []groupRule
		for _, r := range p.Rules {
			if !r.Enabled || (abuse && IsAbuseDuplicate(r)) {
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
			target, id := actionTarget(r.Action)
			if id > 0 {
				referenced[id] = true
			}
			pr := PlanRule{Name: r.Name, Domain: r.Domains, IP: r.IPs, Port: r.Ports, SourceIP: r.SourceIPs, SourcePort: r.SourcePorts, Network: r.Network, Protocol: r.Protocols, Outbound: target}
			list = append(list, groupRule{key: ruleKey(pr), rule: pr, tags: scope})
		}
		if p.DefaultAction != ActionDirect {
			target, id := actionTarget(p.DefaultAction)
			if id > 0 {
				referenced[id] = true
			}
			pr := PlanRule{Name: "默认出站", Outbound: target}
			list = append(list, groupRule{key: ruleKey(pr), rule: pr, tags: tags})
		}
		if len(list) > 0 {
			lists = append(lists, list)
		}
	}
	// Merge: any interleaving that keeps each profile's own order is equivalent
	// because scopes are disjoint; prefer emitting heads shared by 2+ profiles.
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
			break
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
		rule := merged.rule
		for t := range tagSet {
			rule.InboundTags = append(rule.InboundTags, t)
		}
		sort.Strings(rule.InboundTags)
		plan.Rules = append(plan.Rules, rule)
	}
	var warnings []string
	if in.Abuse {
		var abuseTags []string
		for _, n := range in.Nodes {
			if AbuseOn(EffectiveProfile(in.ServerProfileID, n.Override), in.Profiles) {
				abuseTags = append(abuseTags, n.Tag)
			}
		}
		if in.Builtin && AbuseOn(in.ServerProfileID, in.Profiles) {
			abuseTags = append(abuseTags, in.ChainTags...)
		}
		if len(abuseTags) > 0 {
			sort.Strings(abuseTags)
			defaults := AbuseDefaultRules()
			if len(plan.Rules)+len(defaults) > MaxPlanRules {
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
			}
		}
	}
	// Resolve templates actually referenced; failures route to block.
	ids := make([]int64, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	failed := map[string]bool{}
	for _, id := range ids {
		o, err := in.Resolve(id)
		if err != nil {
			warnings = append(warnings, err.Error())
			failed[OutboundTag(id)] = true
			continue
		}
		o.Tag = OutboundTag(id)
		plan.Outbounds = append(plan.Outbounds, o)
	}
	for i := range plan.Rules {
		if failed[plan.Rules[i].Outbound] {
			plan.Rules[i].Outbound = ActionBlock
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
		return nil, warnings, fmt.Errorf("同一服务器最多使用 %d 个出站模板", MaxPlanOutbounds)
	case len(plan.Rules) > MaxPlanRules:
		return nil, warnings, fmt.Errorf("同一服务器的规则合计最多 %d 条", MaxPlanRules)
	case items > MaxPlanItems:
		return nil, warnings, fmt.Errorf("同一服务器的匹配条目合计最多 %d 条", MaxPlanItems)
	case geoBytes > MaxPlanGeoBytes:
		return nil, warnings, errors.New("同一服务器上的 geo 分类合计过大（超过 3 MiB），请让节点使用同一个方案或改用更小的分类")
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
		return "直连（仅 IPv4）"
	case "force_ipv6":
		return "直连（仅 IPv6）"
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
