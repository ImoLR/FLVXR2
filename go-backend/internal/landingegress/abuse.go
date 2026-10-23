package landingegress

// Built-in anti-abuse rules (plan 127). Every landing node blocks outbound
// SMTP relay (TCP 25) and sniffed BitTorrent unless the admin switches the
// defaults off on the node's routing profile. The panel compiles them into
// the plan right after the agent's fixed safety rule, so agents and the mmwx
// Helper apply them as ordinary contract v1 rules (no new agent needed).
// Xray combines the fields of one rule with AND, hence two rules.

// BuiltinAbuse marks the default rules for agents that understand it (node
// capability landing_guard >= 1): they render the rule before per-node chain
// rules and never pause a node because only a builtin rule failed to apply.
// Older agents and the mmwx Custom (strict JSON) never see the field.
const BuiltinAbuse = "abuse"

// AbuseDefaultRules returns fresh copies of the default rules.
func AbuseDefaultRules() []PlanRule {
	return []PlanRule{
		{Name: "默认：屏蔽 SMTP 25", Port: "25", Network: "tcp", Outbound: ActionBlock},
		{Name: "默认：屏蔽 BT", Protocol: []string{"bittorrent"}, Outbound: ActionBlock},
	}
}

// Keys of the default rules, in AbuseDefaultRules order (plan 140 switches).
const (
	AbuseSMTP = "smtp"
	AbuseBT   = "bt"
)

var abuseKeys = []string{AbuseSMTP, AbuseBT}

// AbuseSwitches are the admin's global switches of the default rules (plan
// 140). The zero value keeps both rules on, i.e. the pre-140 behaviour; a
// node blocks only when the global switch AND its profile's toggle are on.
type AbuseSwitches struct {
	SMTPOff bool `json:"-"`
	BTOff   bool `json:"-"`
}

// On reports whether the default rule with the given key is switched on.
func (s AbuseSwitches) On(key string) bool {
	switch key {
	case AbuseSMTP:
		return !s.SMTPOff
	case AbuseBT:
		return !s.BTOff
	}
	return false
}

// AbuseRules returns fresh copies of the default rules that are switched on.
func AbuseRules(s AbuseSwitches) []PlanRule {
	out := []PlanRule{}
	for i, r := range AbuseDefaultRules() {
		if s.On(abuseKeys[i]) {
			out = append(out, r)
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
			return abuseKeys[i]
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
