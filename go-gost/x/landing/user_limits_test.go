package landing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGroupConnLimitMapping(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	cases := []struct {
		name  string
		b     GroupBudget
		live  groupLive
		limit *int64
		block bool
	}{
		{"unlimited", GroupBudget{MaxConnections: -1, ServerMaxConnections: -1}, groupLive{}, nil, false},
		{"total adds live relays", GroupBudget{MaxConnections: 2, ServerMaxConnections: -1}, groupLive{current: 6, relayConns: 4}, ptr(6), false},
		{"server cap counts relays", GroupBudget{MaxConnections: -1, ServerMaxConnections: 7}, groupLive{current: 6, relayConns: 4}, ptr(7), false},
		{"min of both", GroupBudget{MaxConnections: 5, ServerMaxConnections: 4}, groupLive{relayConns: 3}, ptr(4), false},
		{"full and idle is blocked", GroupBudget{MaxConnections: 0, ServerMaxConnections: -1}, groupLive{}, nil, true},
		{"full with open leases admits nothing new", GroupBudget{MaxConnections: 0, ServerMaxConnections: -1}, groupLive{current: 2}, ptr(1), false},
		{"server cap 0 idle", GroupBudget{MaxConnections: -1, ServerMaxConnections: 0}, groupLive{}, nil, true},
	}
	for _, c := range cases {
		limit, block := groupConnLimit(c.b, c.live)
		if block != c.block || (limit == nil) != (c.limit == nil) || (limit != nil && *limit != *c.limit) {
			t.Errorf("%s: limit=%v block=%v, want %v %v", c.name, limit, block, c.limit, c.block)
		}
	}
}

func TestGroupIPLimitKeepsRelaySlots(t *testing.T) {
	if l, block := groupIPLimit(GroupBudget{MaxClientIPs: -1}, groupLive{}); l != nil || block {
		t.Fatalf("unlimited = %v %v", l, block)
	}
	if l, _ := groupIPLimit(GroupBudget{MaxClientIPs: 1, RelayIPs: 2}, groupLive{relayIPs: 1}); l == nil || *l != 3 {
		t.Fatalf("reserve = %v, want 1 + max(1 seen, 2 announced) = 3", l)
	}
	if l, _ := groupIPLimit(GroupBudget{MaxClientIPs: 0, RelayIPs: 0}, groupLive{relayIPs: 1, seenIPs: 1}); l == nil || *l != 1 {
		t.Fatalf("seen relay keeps its slot = %v, want 1", l)
	}
	if l, block := groupIPLimit(GroupBudget{MaxClientIPs: 0}, groupLive{}); l != nil || !block {
		t.Fatalf("full, no IPs seen = %v %v, want blocked", l, block)
	}
	if l, block := groupIPLimit(GroupBudget{MaxClientIPs: 0}, groupLive{seenIPs: 2}); l == nil || *l != 1 || block {
		t.Fatalf("full with seen IPs = %v %v, want 1 (no new IP)", l, block)
	}
}

func userLimitsDesired(t *testing.T) Desired {
	a, b, c := testNode(t, 1), testNode(t, 2), testNode(t, 3)
	a.QuotaGroup, b.QuotaGroup = "landing-user-7", "landing-user-7"
	return Desired{Revision: 1, Enabled: true, Nodes: []Node{a, b, c}, UserLimits: &UserLimits{
		Version:      UserLimitsVersion,
		RelaySources: []string{"198.51.100.60", "2001:db8::60"},
		Groups:       []UserGroup{{Group: "landing-user-7", MaxConnections: 10, MaxClientIPs: -1, ServerMaxConnections: -1}},
	}}
}

func TestUserLimitsValidation(t *testing.T) {
	d := userLimitsDesired(t)
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Desired){
		func(d *Desired) { d.UserLimits.Version = 2 },
		func(d *Desired) { d.UserLimits.RelaySources = []string{"not-an-ip"} },
		func(d *Desired) { d.UserLimits.Groups[0].Group = "user-7" },
		func(d *Desired) { d.UserLimits.Groups[0].Group = "landing-user-07" },
		func(d *Desired) { d.UserLimits.Groups[0].MaxConnections = -2 },
		func(d *Desired) { d.UserLimits.Groups = append(d.UserLimits.Groups, d.UserLimits.Groups[0]) },
		func(d *Desired) { d.Nodes[2].QuotaGroup = "landing-user-8" },
		func(d *Desired) { d.UserLimits = nil },
	}
	for i, mutate := range bad {
		d := userLimitsDesired(t)
		mutate(&d)
		if err := d.validate(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	// Older panels (no user limits, no quota groups) stay valid.
	plain := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}}
	if err := plain.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManagementConfigAndUsage(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	m.desired = userLimitsDesired(t)
	var snap coreSnapshot
	raw := `{"version":7,"started_at":"2026-10-28T00:00:00Z","management_groups":[{"group":"landing-user-7","inbound_current":6,"inbound_online_ips":[{"ip":"198.51.100.60","connections":4},{"ip":"::ffff:203.0.113.9","connections":2},{"ip":"203.0.113.10","connections":0}]},{"group":"other","inbound_current":9}]}`
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatal(err)
	}
	m.recordGroups(snap)
	usage := m.GroupUsages()
	if len(usage) != 1 || usage[0].Connections != 2 || usage[0].RelayConnections != 4 || len(usage[0].ClientIPs) != 1 || usage[0].ClientIPs[0] != "203.0.113.9" {
		t.Fatalf("usage = %+v, want 2 direct (IPv4-mapped normalised), 4 relayed, grace-only IP not reported", usage)
	}
	if live := m.groupLive["landing-user-7"]; live.current != 6 || live.relayConns != 4 || live.relayIPs != 1 || live.seenIPs != 3 {
		t.Fatalf("live = %+v", live)
	}
	// Static bounds until a dynamic budget: total 10 + 4 live relays.
	mappings, limits, blocked := m.managementConfig()
	if len(mappings) != 2 || len(limits) != 1 || len(blocked) != 0 {
		t.Fatalf("mappings=%v limits=%v blocked=%v", mappings, limits, blocked)
	}
	if got := *(limits[0].(map[string]any)["max_inbound_connections"].(*int64)); got != 14 {
		t.Fatalf("static limit = %d, want 10+4", got)
	}
	if err := m.SetGroupBudgets([]GroupBudget{{Group: "landing-user-7", MaxConnections: 0, MaxClientIPs: -1, ServerMaxConnections: -1}}); err != nil {
		t.Fatal(err)
	}
	_, limits, _ = m.managementConfig()
	if got := *(limits[0].(map[string]any)["max_inbound_connections"].(*int64)); got != 4 {
		t.Fatalf("dynamic full limit = %d, want the 4 live relays only", got)
	}
	// Idle full group → its identities are blocked (the Core reads 0 as unlimited).
	m.recordGroups(coreSnapshot{Version: 7})
	_, limits, blocked = m.managementConfig()
	if len(blocked) != 2 || limits[0].(map[string]any)["max_inbound_connections"].(*int64) != nil {
		t.Fatalf("idle full group: limits=%v blocked=%v, want both nodes blocked", limits, blocked)
	}
	// An authoritative empty list falls back to the static bounds.
	if err := m.SetGroupBudgets(nil); err != nil {
		t.Fatal(err)
	}
	if b := m.effectiveGroupBudgets()["landing-user-7"]; b.MaxConnections != 10 {
		t.Fatalf("after reset = %+v, want static 10", b)
	}
	if err := m.SetGroupBudgets([]GroupBudget{{Group: "user-7"}}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("forward group name accepted by the landing store: %v", err)
	}
}
