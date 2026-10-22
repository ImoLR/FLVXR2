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
func IsAbuseDuplicate(r Rule) bool {
	if r.Action != ActionBlock || len(r.Domains)+len(r.IPs)+len(r.SourceIPs) > 0 || r.SourcePorts != "" {
		return false
	}
	for _, d := range AbuseDefaultRules() {
		if r.Ports == d.Port && r.Network == d.Network && equalStrings(r.Protocols, d.Protocol) {
			return true
		}
	}
	return false
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
