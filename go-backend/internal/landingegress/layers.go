package landingegress

import (
	"errors"
	"fmt"
)

// Plan 134: layered landing routing. Per node, first match wins:
//
//  1. 系统内置 (safety + default anti-abuse + source exemptions)
//  2. 全局·强制 (Global.Forced): every node, incl. line-chain copies
//  3. 节点规则 (NodeRef.Rules): the node owner's rules, own inbound tag only
//  4. 服务器规则: the server profile or the admin node override
//  5. 全局·默认 (Global.Default): every non-chain node
//  6. 默认出站: node owner > override/server profile default > Global.DefaultAction
//
// Global is the admin's global rule group. The zero value (no rules, direct)
// compiles every server exactly as before plan 134.
type Global struct {
	Forced        []Rule `json:"forced"`
	Default       []Rule `json:"default"`
	DefaultAction string `json:"defaultAction"`
}

// Builtin markers of global layers for agents with landing_guard >= 2:
// "forced" rules are rendered before chain rules like the abuse defaults;
// both never withhold a node when the plan cannot be applied (fail-open).
const (
	BuiltinForced = "forced"
	BuiltinGlobal = "global"
)

// LayersVersion is the landing_guard version that understands BuiltinForced
// and BuiltinGlobal.
const LayersVersion = 2

const (
	MaxGlobalRules = 64 // per list
	// MaxNodeRules is the hard per-node bound (admins are not capped by the
	// user limits, but a node's list stays small).
	MaxNodeRules = 32
)

// Active reports whether the group changes anything.
func (g *Global) Active() bool {
	if g == nil {
		return false
	}
	return len(enabled(g.Forced)) > 0 || len(enabled(g.Default)) > 0 || (g.DefaultAction != "" && g.DefaultAction != ActionDirect)
}

func enabled(rules []Rule) []Rule {
	out := []Rule{}
	for _, r := range rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

// normalizeRuleList validates one rule list with the profile rules' checks.
func normalizeRuleList(label string, rules []Rule, max int, outbounds map[int64]bool) ([]Rule, error) {
	if len(rules) > max {
		return nil, fmt.Errorf("%s最多 %d 条规则", label, max)
	}
	p := Profile{Name: label, DefaultAction: ActionDirect, Rules: append([]Rule(nil), rules...)}
	if err := NormalizeProfile(&p, outbounds); err != nil {
		return nil, fmt.Errorf("%s：%w", label, err)
	}
	for i, r := range p.Rules {
		if len(r.LandingNodeIDs) > 0 {
			return nil, fmt.Errorf("%s：第 %d 条规则不能限定落地节点", label, i+1)
		}
	}
	return p.Rules, nil
}

// NormalizeGlobal validates the global group in place. Rules use every
// profile matcher except 适用落地节点 (they apply to every node).
func NormalizeGlobal(g *Global, outbounds map[int64]bool) error {
	var err error
	if g.Forced, err = normalizeRuleList("全局·强制", g.Forced, MaxGlobalRules, outbounds); err != nil {
		return err
	}
	if g.Default, err = normalizeRuleList("全局·默认", g.Default, MaxGlobalRules, outbounds); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, list := range [][]Rule{g.Forced, g.Default} {
		for i := range list {
			if ids[list[i].ID] {
				list[i].ID = NewRuleID()
			}
			ids[list[i].ID] = true
		}
	}
	if g.DefaultAction == "" {
		g.DefaultAction = ActionDirect
	}
	if !ValidAction(g.DefaultAction, outbounds) {
		return errors.New("全局默认出站无效或已删除")
	}
	items := 0
	for _, list := range [][]Rule{g.Forced, g.Default} {
		for _, r := range list {
			items += RuleItems(r)
		}
	}
	if items > MaxProfileItems {
		return fmt.Errorf("全局规则的匹配条目合计最多 %d 条", MaxProfileItems)
	}
	return nil
}

// NormalizeNodeRules validates a node's own rules and default outbound
// ("" = follow the upper layers). Users get the destination matchers only:
// no 来源 IP / 来源端口 and no node scope. allowed lists the templates the
// caller may use (users: templates marked 允许用户使用).
func NormalizeNodeRules(rules []Rule, def string, allowed map[int64]bool) ([]Rule, string, error) {
	for i, r := range rules {
		if len(r.SourceIPs) > 0 || r.SourcePorts != "" {
			return nil, "", fmt.Errorf("节点规则：第 %d 条规则不能使用来源 IP 或来源端口", i+1)
		}
		if len(r.LandingNodeIDs) > 0 {
			return nil, "", fmt.Errorf("节点规则：第 %d 条规则不能限定落地节点", i+1)
		}
	}
	out, err := normalizeRuleList("节点规则", rules, MaxNodeRules, allowed)
	if err != nil {
		return nil, "", err
	}
	if def != "" && !ValidAction(def, allowed) {
		return nil, "", errors.New("节点默认出站无效或未开放")
	}
	return out, def, nil
}

// RulesUseGeo reports whether any enabled rule references geoip / geosite.
func RulesUseGeo(rules []Rule) (geoIP, geoSite bool) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		ip, site := usesGeo(r.IPs, r.SourceIPs)
		_, site2 := usesGeo(r.Domains)
		geoIP, geoSite = geoIP || ip, geoSite || site || site2
	}
	return geoIP, geoSite
}

// Actions lists the actions a rule list and a default reference.
func Actions(rules []Rule, defaults ...string) []string {
	out := []string{}
	for _, d := range defaults {
		if d != "" {
			out = append(out, d)
		}
	}
	for _, r := range rules {
		if r.Enabled {
			out = append(out, r.Action)
		}
	}
	return out
}
