package landingegress

// Built-in anti-abuse rules (plan 127). Every landing node blocks outbound
// SMTP relay (TCP 25) and sniffed BitTorrent unless the admin switches the
// defaults off on the node's routing profile. Plan 143 adds the mail
// submission ports and Telnet/SMB (formerly 全局·强制 seeds) to the group. The panel compiles them into
// the plan right after the agent's fixed safety rule, so agents and the mmwx
// Helper apply them as ordinary contract v1 rules (no new agent needed).
// Xray combines the fields of one rule with AND, hence two rules.

// BuiltinAbuse marks the default rules for agents that understand it (node
// capability landing_guard >= 1): they render the rule before per-node chain
// rules and never pause a node because only a builtin rule failed to apply.
// Older agents and the mmwx Custom (strict JSON) never see the field.
const BuiltinAbuse = "abuse"

// AbuseDefaultRules returns fresh copies of the default rules, in the 内置
// order: SMTP 25, mail submission ports, Telnet/SMB (plan 143, formerly the
// 全局·强制 seeds), BT. Their matchers are exactly those of the plan-140
// seeds, so a migrated seed blocks the same traffic.
func AbuseDefaultRules() []PlanRule {
	return []PlanRule{
		{Name: "默认：屏蔽 SMTP 25", Port: "25", Network: "tcp", Outbound: ActionBlock},
		{Name: "默认：屏蔽发信端口", Port: MailPorts, Network: "tcp", Outbound: ActionBlock},
		{Name: "默认：屏蔽 Telnet/SMB", Port: TelnetSMBPorts, Outbound: ActionBlock},
		{Name: "默认：屏蔽 BT", Protocol: []string{"bittorrent"}, Outbound: ActionBlock},
	}
}

// Ports of the two plan-143 rows (TCP only / TCP+UDP).
const (
	MailPorts      = "465,587,2525"
	TelnetSMBPorts = "23,135-139,445"
)

// Keys of the default rules, in AbuseDefaultRules order (plan 140 / 143
// switches).
const (
	AbuseSMTP      = "smtp"
	AbuseMail      = "mail"
	AbuseTelnetSMB = "telnet"
	AbuseBT        = "bt"
)

// AbuseKeys lists the keys in AbuseDefaultRules order.
var AbuseKeys = []string{AbuseSMTP, AbuseMail, AbuseTelnetSMB, AbuseBT}

// AbuseSwitches are the admin's global switches of the default rules (plan
// 140 / 143); a node blocks only when the global switch AND its profile's
// toggle are on. The zero value is the pre-143 output: SMTP and BT on (they
// were always on before plan 140), the plan-143 rows off (they were opt-in
// 全局·强制 seeds, so nothing starts blocking silently).
type AbuseSwitches struct {
	SMTPOff     bool `json:"-"`
	BTOff       bool `json:"-"`
	MailOn      bool `json:"-"`
	TelnetSMBOn bool `json:"-"`
}

// On reports whether the default rule with the given key is switched on.
func (s AbuseSwitches) On(key string) bool {
	switch key {
	case AbuseSMTP:
		return !s.SMTPOff
	case AbuseMail:
		return s.MailOn
	case AbuseTelnetSMB:
		return s.TelnetSMBOn
	case AbuseBT:
		return !s.BTOff
	}
	return false
}

// Set switches the rule with the given key on or off.
func (s *AbuseSwitches) Set(key string, on bool) {
	switch key {
	case AbuseSMTP:
		s.SMTPOff = !on
	case AbuseMail:
		s.MailOn = on
	case AbuseTelnetSMB:
		s.TelnetSMBOn = on
	case AbuseBT:
		s.BTOff = !on
	}
}

// AbuseRules returns fresh copies of the default rules that are switched on.
func AbuseRules(s AbuseSwitches) []PlanRule {
	out := []PlanRule{}
	for i, r := range AbuseDefaultRules() {
		if s.On(AbuseKeys[i]) {
			out = append(out, r)
		}
	}
	return out
}

// AbuseRuleNames: the short names of the rules switched on (warnings,
// server settings), e.g. 「SMTP 25」.
func AbuseRuleNames(s AbuseSwitches) []string {
	short := map[string]string{AbuseSMTP: "SMTP 25", AbuseMail: "发信端口 465/587/2525", AbuseTelnetSMB: "Telnet/SMB", AbuseBT: "BT"}
	out := []string{}
	for _, k := range AbuseKeys {
		if s.On(k) {
			out = append(out, short[k])
		}
	}
	return out
}

// AbuseOn reports whether a profile keeps the default rules; the built-in
// default profile (id 0, or a missing profile) always does.
func AbuseOn(profileID int64, profiles map[int64]Profile) bool {
	if profileID == 0 {
		return true
	}
	p, ok := profiles[profileID]
	return !ok || p.AbuseDefaultsEnabled()
}

// AbuseDefaultsEnabled: nil (older clients, new profiles) means on.
func (p Profile) AbuseDefaultsEnabled() bool { return p.AbuseDefaults == nil || *p.AbuseDefaults }

// IsAbuseDuplicate reports whether a normalized profile rule only repeats a
// default rule (e.g. the 「屏蔽 BT」 preset): same match fields and action
// block. Its node scope does not matter because the default rule covers every
// node of a profile that keeps the defaults.
func IsAbuseDuplicate(r Rule) bool { return AbuseDuplicateOf(r) != "" }

// AbuseDuplicateOf returns the key of the default rule r only repeats ("" =
// none). The compiler drops such a profile rule only while that default rule
// is actually sent (its global switch is on).
func AbuseDuplicateOf(r Rule) string {
	if r.Action != ActionBlock || len(r.Domains)+len(r.IPs)+len(r.SourceIPs) > 0 || r.SourcePorts != "" {
		return ""
	}
	for i, d := range AbuseDefaultRules() {
		if r.Ports == d.Port && r.Network == d.Network && equalStrings(r.Protocols, d.Protocol) {
			return AbuseKeys[i]
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
