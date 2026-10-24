package landingegress

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Plan 140: the routing-rule preset library (stored in the panel database,
// seeded once with the built-in presets) and the starter rules of 全局·强制
// (plan 149: the three 大陆 rules, enabled).

// ActionAsk is the action placeholder of an AskOutbound preset: the outbound
// is picked when the preset is inserted.
const ActionAsk = "ask"

const (
	MaxPresets         = 64
	MaxPresetRules     = 16
	MaxPresetDescRunes = 300
)

// Preset is one library entry. Rules carry no ids (they get fresh ones when
// inserted) and no 适用落地节点.
type Preset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Rules       []Rule `json:"rules"`
	AskOutbound bool   `json:"askOutbound"`
	InsertTop   bool   `json:"insertTop"`
	SortOrder   int    `json:"sortOrder"`
	SeedKey     string `json:"seedKey"`
}

func presetRule(name, key string, r Rule, action string) Rule {
	r.Name, r.Preset, r.Enabled, r.Action = name, key, true, action
	return FilledRule(r)
}

// FilledRule uses empty lists instead of nil (the normalized form, and what
// the UI expects in stored JSON).
func FilledRule(r Rule) Rule {
	if r.Domains == nil {
		r.Domains = []string{}
	}
	if r.IPs == nil {
		r.IPs = []string{}
	}
	if r.SourceIPs == nil {
		r.SourceIPs = []string{}
	}
	if r.Protocols == nil {
		r.Protocols = []string{}
	}
	if r.LandingNodeIDs == nil {
		r.LandingNodeIDs = []int64{}
	}
	return r
}

// SeedPresets are the presets the library starts with (the pre-140 hard-coded
// frontend list, same names and rules, plus 「禁止访问内网」).
func SeedPresets() []Preset {
	return []Preset{
		{SeedKey: "block-cn", Name: "屏蔽中国大陆", InsertTop: true,
			Description: BlockCNDescription,
			Rules:       BlockCNRules()},
		{SeedKey: "anti-google-cn", Name: "防止送中", AskOutbound: true,
			Description: "Google 走所选出站（默认 WARP），避免落地 IP 被判定在中国",
			Rules:       []Rule{presetRule("防止送中", "anti-google-cn", Rule{Domains: []string{"geosite:google"}}, ActionAsk)}},
		{SeedKey: "speedtest", Name: "测速分流", AskOutbound: true,
			Description: "Speedtest 测速走所选出站",
			Rules:       []Rule{presetRule("测速分流", "speedtest", Rule{Domains: []string{"geosite:speedtest", "geosite:ookla-speedtest"}}, ActionAsk)}},
		{SeedKey: "ai", Name: "AI 解锁", AskOutbound: true,
			Description: "OpenAI、Claude、Gemini 等走所选出站",
			Rules: []Rule{presetRule("AI 解锁", "ai", Rule{Domains: []string{
				"geosite:openai", "geosite:anthropic", "geosite:google-gemini", "geosite:perplexity", "geosite:xai",
			}}, ActionAsk)}},
		{SeedKey: "tiktok", Name: "抖音 / TikTok 解锁", AskOutbound: true,
			Description: "TikTok 走所选出站",
			Rules:       []Rule{presetRule("TikTok 解锁", "tiktok", Rule{Domains: []string{"geosite:tiktok"}}, ActionAsk)}},
		{SeedKey: "streaming", Name: "流媒体解锁", AskOutbound: true,
			Description: "Netflix、Disney+、HBO、Prime Video 等走所选出站",
			Rules: []Rule{presetRule("流媒体解锁", "streaming", Rule{Domains: []string{
				"geosite:netflix", "geosite:disney", "geosite:hbo", "geosite:primevideo", "geosite:hulu", "geosite:dazn", "geosite:abema", "geosite:bahamut",
			}}, ActionAsk)}},
		{SeedKey: "block-bt", Name: "屏蔽 BT",
			Description: "按流量特征识别 BitTorrent 并封锁",
			Rules:       []Rule{presetRule("屏蔽 BT", "block-bt", Rule{Protocols: []string{"bittorrent"}}, ActionBlock)}},
		{SeedKey: "block-private", Name: "禁止访问内网",
			Description: "封锁内网、回环、链路本地等地址（geoip:private）",
			Rules:       []Rule{presetRule("禁止访问内网", "block-private", Rule{IPs: []string{"geoip:private"}}, ActionBlock)}},
		{SeedKey: "anti-abuse", Name: "防滥用",
			Description: "封锁 TCP 465/587/2525（发信）和 23、135-139、445（Telnet/SMB，TCP+UDP）。封 465/587 只影响用邮件客户端通过代理发信，收信（IMAP 993/143、POP3 995/110）和网页邮箱不受影响；不封 22 和 3389",
			Rules: []Rule{
				presetRule("屏蔽发信端口", "anti-abuse", Rule{Ports: "465,587,2525", Network: "tcp"}, ActionBlock),
				presetRule("屏蔽 Telnet/SMB", "anti-abuse", Rule{Ports: "23,135-139,445"}, ActionBlock),
			}},
		{SeedKey: "block-ads", Name: "屏蔽广告",
			Description: "常见广告域名（geosite:category-ads）封锁",
			Rules:       []Rule{presetRule("屏蔽广告", "block-ads", Rule{Domains: []string{"geosite:category-ads"}}, ActionBlock)}},
	}
}

// Plan 149: the three 「屏蔽中国大陆」 rules. 禁止大陆直连 only covers the
// Shadowsocks family: SS nodes are meant to be used through FLVX forwarding
// (a direct SS connection from the mainland burns the landing IP), while
// VLESS Reality and the other protocols are made for direct use.
const BlockCNDescription = "三条规则插在最前，默认开启：屏蔽大陆 IP、屏蔽大陆网站、禁止大陆直连。禁止大陆直连只作用于 SS 类节点（SS2022），VLESS Reality 等节点仍可从大陆直连；经 FLVX 规则转发的连接来自 FLVX 节点（及来源白名单），不受影响"

// BlockCNRules are the rules of the 「屏蔽中国大陆」 preset.
func BlockCNRules() []Rule {
	return []Rule{
		presetRule("屏蔽大陆 IP", "block-cn", Rule{IPs: []string{"geoip:cn"}}, ActionBlock),
		presetRule("屏蔽大陆网站", "block-cn", Rule{Domains: []string{"geosite:cn"}}, ActionBlock),
		presetRule("禁止大陆直连", "block-cn", Rule{SourceIPs: []string{"geoip:cn"}, InboundProtocols: append([]string(nil), SSFamily...)}, ActionBlock),
	}
}

// LegacyBlockCNRules are the preset's rules before plan 149 (禁止大陆直连
// for every protocol); the preset migration replaces exactly these.
func LegacyBlockCNRules() []Rule {
	return []Rule{
		presetRule("屏蔽大陆 IP", "block-cn", Rule{IPs: []string{"geoip:cn"}}, ActionBlock),
		presetRule("屏蔽大陆网站", "block-cn", Rule{Domains: []string{"geosite:cn"}}, ActionBlock),
		presetRule("禁止大陆直连", "block-cn", Rule{SourceIPs: []string{"geoip:cn"}}, ActionBlock),
	}
}

// LegacyBlockCNDescription is the preset description before plan 149.
const LegacyBlockCNDescription = "三条规则插在最前：屏蔽大陆 IP、屏蔽大陆网站、禁止大陆客户端直连（FLVX 节点与来源白名单除外）"

// ForcedSeedRules are the 全局·强制 starter rules. Plan 143 moved the two
// 防滥用 seeds into 内置 (see AbuseSeedRules); plan 149 (user 10-24): the
// three 大陆 rules are ENABLED by default, 禁止大陆直连 scoped to SS nodes.
func ForcedSeedRules() []Rule {
	rules := BlockCNRules()
	ids := []string{SeedCNIP, SeedCNSite, SeedCNDirect}
	for i := range rules {
		rules[i].ID = ids[i]
	}
	return rules
}

// Seed ids of the 大陆 rules in 全局·强制.
const (
	SeedCNIP     = "seed-cn-ip"
	SeedCNSite   = "seed-cn-site"
	SeedCNDirect = "seed-cn-direct"
)

func forcedSeed(id, name, preset string, r Rule) Rule {
	r.ID, r.Name, r.Preset, r.Enabled, r.Action = id, name, preset, false, ActionBlock
	return FilledRule(r)
}

// CNSeedResult is the result of EnableCNSeeds.
type CNSeedResult struct {
	// Enabled: seed ids switched on; Added: seed-cn-direct appended.
	Enabled []string
	Added   bool
	// Edited: seed ids whose matchers the admin changed (left alone);
	// Missing: seed ids deleted by the admin (never re-added); Full: the list
	// was at MaxGlobalRules, nothing appended.
	Edited, Missing []string
	Full            bool
}

// EnableCNSeeds (plan 149 one-time migration of 全局·强制) switches on
// seed-cn-ip / seed-cn-site when they still carry the original matchers and
// adds seed-cn-direct right after them unless a rule with the same signature
// exists or the admin deleted both. Edited seeds stay as they are; deleted
// ones are not re-added.
func EnableCNSeeds(forced []Rule) ([]Rule, CNSeedResult) {
	res := CNSeedResult{}
	seeds := ForcedSeedRules()
	out := append([]Rule(nil), forced...)
	have := map[string]bool{}
	for _, r := range out {
		have[RuleSignature(r)] = true
	}
	last := -1
	for _, seed := range seeds[:2] {
		idx := -1
		for i := range out {
			if out[i].ID == seed.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			res.Missing = append(res.Missing, seed.ID)
			continue
		}
		if idx > last {
			last = idx
		}
		if RuleSignature(out[idx]) != RuleSignature(seed) {
			res.Edited = append(res.Edited, seed.ID)
			continue
		}
		if !out[idx].Enabled {
			out[idx].Enabled = true
			res.Enabled = append(res.Enabled, seed.ID)
		}
	}
	direct := seeds[2]
	if have[RuleSignature(direct)] {
		return out, res
	}
	if len(res.Missing) == len(seeds[:2]) {
		// The admin deleted both 大陆 rules: no 大陆 blocking is wanted.
		return out, res
	}
	if len(out) >= MaxGlobalRules {
		res.Full = true
		return out, res
	}
	for _, r := range out {
		if r.ID == direct.ID {
			direct.ID = NewRuleID()
			break
		}
	}
	pos := last + 1
	if last < 0 {
		pos = len(out)
	}
	out = append(out[:pos], append([]Rule{direct}, out[pos:]...)...)
	res.Added = true
	return out, res
}

// AbuseSeedRules are the plan-140 全局·强制 seeds that plan 143 moved into
// 内置, keyed by their 内置 switch (AbuseMail / AbuseTelnetSMB).
func AbuseSeedRules() map[string]Rule {
	return map[string]Rule{
		AbuseMail:      forcedSeed("seed-mail-ports", "屏蔽发信端口", "anti-abuse", Rule{Ports: MailPorts, Network: "tcp"}),
		AbuseTelnetSMB: forcedSeed("seed-telnet-smb", "屏蔽 Telnet/SMB", "anti-abuse", Rule{Ports: TelnetSMBPorts}),
	}
}

// AbuseSeedMove is the result of MoveAbuseSeeds.
type AbuseSeedMove struct {
	// Found: a 强制 rule matched the 内置 row (key); On: one of them was
	// enabled, i.e. the row was in force before the move.
	Found map[string]bool
	On    map[string]bool
	// Kept: seed ids left in 全局·强制 because their matchers were edited
	// (they are custom rules now; moving them would change what is blocked).
	Kept []string
}

// MoveAbuseSeeds (plan 143 one-time migration) removes from a 全局·强制 list
// every rule that blocks exactly what a moved 防滥用 seed blocks: same
// matchers and action block, no node scope, any id / name / enabled state.
// It reports per 内置 row whether a rule was found and whether one was
// enabled; the caller turns the 内置 switch on exactly then, so the set of
// blocked traffic stays the same.
func MoveAbuseSeeds(forced []Rule) ([]Rule, AbuseSeedMove) {
	res := AbuseSeedMove{Found: map[string]bool{}, On: map[string]bool{}}
	sigs := map[string]string{}
	ids := map[string]string{}
	for key, seed := range AbuseSeedRules() {
		sigs[RuleSignature(seed)] = key
		ids[seed.ID] = key
	}
	rest := []Rule{}
	for _, r := range forced {
		if key, ok := sigs[RuleSignature(r)]; ok && len(r.LandingNodeIDs) == 0 {
			res.Found[key] = true
			res.On[key] = res.On[key] || r.Enabled
			continue
		}
		if _, ok := ids[r.ID]; ok {
			res.Kept = append(res.Kept, r.ID)
		}
		rest = append(rest, r)
	}
	return rest, res
}

// RuleSignature compares rules by matchers and action (not id / name /
// enabled), like the frontend dedupe.
func RuleSignature(r Rule) string {
	r.ID, r.Name, r.Enabled, r.Preset = "", "", false, ""
	if r.LandingNodeIDs == nil {
		r.LandingNodeIDs = []int64{}
	}
	sig := ruleKey(toPlanRule(r)) + "|" + r.Action + "|" + fmt.Sprint(r.LandingNodeIDs)
	if len(r.InboundProtocols) > 0 {
		// Plan 149: unscoped rules keep their pre-149 signature.
		protos := append([]string(nil), r.InboundProtocols...)
		sort.Strings(protos)
		sig += "|p:" + strings.Join(protos, ",")
	}
	return sig
}

// AppendSeedRules appends the seeds that are not in the list yet (same
// matchers and action), keeping the list within MaxGlobalRules.
func AppendSeedRules(list, seeds []Rule) []Rule {
	have := map[string]bool{}
	ids := map[string]bool{}
	for _, r := range list {
		have[RuleSignature(r)] = true
		ids[r.ID] = true
	}
	out := append([]Rule{}, list...)
	for _, s := range seeds {
		if have[RuleSignature(s)] || len(out) >= MaxGlobalRules {
			continue
		}
		if ids[s.ID] {
			s.ID = NewRuleID()
		}
		have[RuleSignature(s)], ids[s.ID] = true, true
		out = append(out, s)
	}
	return out
}

// NormalizePreset validates a library entry in place: the profile rule
// checks (outbounds lists existing templates), no ids, no node scope, and the
// 「ask」 placeholder only (and at least once) in AskOutbound presets.
func NormalizePreset(p *Preset, outbounds map[int64]bool) error {
	name, ok := validName(p.Name, 64)
	if !ok {
		return errors.New("预设名称需为 1–64 个字符")
	}
	p.Name = name
	p.Description = strings.TrimSpace(p.Description)
	if utf8.RuneCountInString(p.Description) > MaxPresetDescRunes {
		return fmt.Errorf("预设说明最多 %d 个字符", MaxPresetDescRunes)
	}
	if len(p.Rules) == 0 {
		return errors.New("预设至少需要一条规则")
	}
	if len(p.Rules) > MaxPresetRules {
		return fmt.Errorf("每个预设最多 %d 条规则", MaxPresetRules)
	}
	asks := map[int]bool{}
	rules := append([]Rule(nil), p.Rules...)
	for i := range rules {
		rules[i].ID, rules[i].LandingNodeIDs = "", nil
		if rules[i].Action == ActionAsk {
			if !p.AskOutbound {
				return fmt.Errorf("第 %d 条规则的出站为「插入时选择」，请开启「插入时选择出站」", i+1)
			}
			asks[i] = true
			rules[i].Action = ActionDirect
		}
	}
	if p.AskOutbound && len(asks) == 0 {
		return errors.New("开启「插入时选择出站」后，至少一条规则的出站应为「插入时选择」")
	}
	prof := Profile{Name: p.Name, DefaultAction: ActionDirect, Rules: rules}
	if err := NormalizeProfile(&prof, outbounds); err != nil {
		return err
	}
	for i := range prof.Rules {
		prof.Rules[i].ID = ""
		prof.Rules[i].LandingNodeIDs = []int64{}
		if asks[i] {
			prof.Rules[i].Action = ActionAsk
		}
	}
	p.Rules = prof.Rules
	return nil
}

// PresetUsableByUser reports whether a normal user may see / insert the
// preset in their node rules: no 来源 IP / 来源端口 matchers, and every fixed
// outbound is a template marked 允许用户使用 (allowed).
func PresetUsableByUser(p Preset, allowed map[int64]bool) bool {
	for _, r := range p.Rules {
		if len(r.SourceIPs) > 0 || r.SourcePorts != "" || len(r.LandingNodeIDs) > 0 || len(r.InboundProtocols) > 0 {
			return false
		}
		if id, ok := ActionOutbound(r.Action); ok && !allowed[id] {
			return false
		}
	}
	return true
}

// PresetMissingOutbound reports a fixed outbound that no longer exists.
func PresetMissingOutbound(p Preset, outbounds map[int64]bool) bool {
	for _, r := range p.Rules {
		if id, ok := ActionOutbound(r.Action); ok && !outbounds[id] {
			return true
		}
	}
	return false
}
