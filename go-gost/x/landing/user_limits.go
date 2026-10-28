package landing

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// Plan 158: per-user landing limits. The panel maps each node of a limited
// user to the user's Core management group ("landing-user-<id>"); the group's
// inbound limits come from the panel's quota coordinator (SetQuotaGroups,
// dynamic) or, until the first push, from the desired state (static upper
// bounds). Inbound connections whose source is one of our own FLVX nodes are
// forward-rule relays already counted at the rule's entry: they are reported
// separately and do not use the user's total budget.

// UserLimitsVersion is reported as landing_user_limits.
const UserLimitsVersion = 1

// UserGroupPrefix is the only group name prefix this agent maps.
const UserGroupPrefix = "landing-user-"

const (
	maxUserGroups       = 10000
	maxRelaySources     = 4096
	maxUsageIPsPerGroup = 1024
)

// UserLimits is the desired-state part (only sent to agents reporting
// landing_user_limits).
type UserLimits struct {
	Version      int         `json:"version"`
	RelaySources []string    `json:"relay_sources"`
	Groups       []UserGroup `json:"groups"`
}

// UserGroup carries a group's static upper bounds (-1 = unlimited):
// MaxConnections / MaxClientIPs are the user's totals without relays,
// ServerMaxConnections the user's 入站连接数 on this server (relays included).
type UserGroup struct {
	Group                string `json:"group"`
	MaxConnections       int64  `json:"max_connections"`
	MaxClientIPs         int    `json:"max_client_ips"`
	ServerMaxConnections int64  `json:"server_max_connections"`
}

// GroupBudget is one coordinator budget (same meaning as UserGroup, plus the
// relay IP reserve: our nodes that dial this user's nodes here).
type GroupBudget struct {
	Group                string
	MaxConnections       int64
	MaxClientIPs         int
	ServerMaxConnections int64
	RelayIPs             int
}

// GroupUsage is reported to the panel next to the forward quota groups.
type GroupUsage struct {
	Group            string
	Connections      int
	ClientIPs        []string
	RelayConnections int
}

// groupLive is a group's state in the last Core snapshot.
type groupLive struct {
	current    int64 // admitted inbound leases (direct + relay)
	relayConns int64
	relayIPs   int // relay sources among the group's online IPs
	seenIPs    int // all online IPs (incl. the Core's 30 s grace)
}

func (u *UserLimits) validate(nodes []Node) error {
	if u == nil {
		for _, n := range nodes {
			if n.QuotaGroup != "" {
				return fmt.Errorf("landing node quota group without user limits")
			}
		}
		return nil
	}
	if u.Version != UserLimitsVersion || len(u.Groups) > maxUserGroups || len(u.RelaySources) > maxRelaySources {
		return fmt.Errorf("invalid landing user limits")
	}
	for _, ip := range u.RelaySources {
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("invalid landing relay source")
		}
	}
	groups := map[string]bool{}
	for _, g := range u.Groups {
		if !validUserGroupName(g.Group) || groups[g.Group] || g.MaxConnections < -1 || g.MaxClientIPs < -1 || g.ServerMaxConnections < -1 {
			return fmt.Errorf("invalid landing user group")
		}
		groups[g.Group] = true
	}
	for _, n := range nodes {
		if n.QuotaGroup != "" && (!groups[n.QuotaGroup] || n.Chain != nil) {
			return fmt.Errorf("invalid landing node quota group")
		}
	}
	return nil
}

func validUserGroupName(name string) bool {
	id := strings.TrimPrefix(name, UserGroupPrefix)
	if id == name || id == "" || len(id) > 19 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return id[0] != '0'
}

// budgetStore holds the coordinator's dynamic budgets; it has its own lock so
// SetQuotaGroups never waits for a reconcile (which may download for minutes).
type budgetStore struct {
	mu      sync.Mutex
	budgets map[string]GroupBudget
	wake    chan struct{}
}

func (m *Manager) budgetState() *budgetStore {
	m.budgetsOnce.Do(func() { m.budgets = &budgetStore{budgets: map[string]GroupBudget{}, wake: make(chan struct{}, 1)} })
	return m.budgets
}

// SetGroupBudgets replaces the dynamic budgets with the panel's authoritative
// list for this node (groups not listed fall back to the desired state's
// static bounds) and applies them with one control-socket call, coalesced.
func (m *Manager) SetGroupBudgets(budgets []GroupBudget) error {
	next := make(map[string]GroupBudget, len(budgets))
	for _, b := range budgets {
		if !validUserGroupName(b.Group) || b.MaxConnections < -1 || b.MaxClientIPs < -1 || b.ServerMaxConnections < -1 || b.RelayIPs < 0 || b.RelayIPs > maxRelaySources {
			return fmt.Errorf("invalid landing group budget %q", b.Group)
		}
		next[b.Group] = b
	}
	st := m.budgetState()
	st.mu.Lock()
	st.budgets = next
	st.mu.Unlock()
	select {
	case st.wake <- struct{}{}:
	default:
	}
	return nil
}

// runBudgetApplier applies dynamic budgets as they arrive. It waits for a
// running reconcile (which applies the newest budgets itself).
func (m *Manager) runBudgetApplier(ctx context.Context) {
	st := m.budgetState()
	for {
		select {
		case <-ctx.Done():
			return
		case <-st.wake:
		}
		m.mu.Lock()
		if m.loaded && m.apiPort != 0 && m.epoch != "" && m.desired.UserLimits != nil && m.lastSnapshot != nil {
			child, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := m.applyLimits(child, *m.lastSnapshot)
			cancel()
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			m.logger().Record("landing user limits", "apply", msg)
		}
		m.mu.Unlock()
	}
}

// effectiveGroupBudgets merges static bounds and dynamic budgets for the
// groups of the desired state. Caller holds m.mu.
func (m *Manager) effectiveGroupBudgets() map[string]GroupBudget {
	out := map[string]GroupBudget{}
	if m.desired.UserLimits == nil {
		return out
	}
	st := m.budgetState()
	st.mu.Lock()
	dynamic := st.budgets
	st.mu.Unlock()
	for _, g := range m.desired.UserLimits.Groups {
		b := GroupBudget{Group: g.Group, MaxConnections: g.MaxConnections, MaxClientIPs: g.MaxClientIPs, ServerMaxConnections: g.ServerMaxConnections}
		if d, ok := dynamic[g.Group]; ok {
			b = d
		}
		out[g.Group] = b
	}
	return out
}

// groupConnLimit maps a budget to the Core group's max_inbound_connections.
// The Core counts every admitted inbound lease of the group, relays included;
// the user-total budget excludes relays, so the live relay count is added.
// The Core treats 0 as unlimited: a full group with nothing open is blocked
// instead (exact), one with open leases gets 1 (≤ open → nothing new).
func groupConnLimit(b GroupBudget, live groupLive) (limit *int64, block bool) {
	l := int64(-1)
	if b.MaxConnections >= 0 {
		l = b.MaxConnections + live.relayConns
	}
	if b.ServerMaxConnections >= 0 && (l < 0 || b.ServerMaxConnections < l) {
		l = b.ServerMaxConnections
	}
	if l < 0 {
		return nil, false
	}
	if l == 0 {
		if live.current == 0 {
			return nil, true
		}
		l = 1
	}
	return &l, false
}

// groupIPLimit maps a budget to max_inbound_online_ips: relay sources keep
// their slots (the ones seen, at least the reserve the panel announced) so a
// relay is never refused for a new source IP.
func groupIPLimit(b GroupBudget, live groupLive) (limit *int, block bool) {
	if b.MaxClientIPs < 0 {
		return nil, false
	}
	l := b.MaxClientIPs + max(live.relayIPs, b.RelayIPs)
	if l == 0 {
		if live.seenIPs == 0 {
			return nil, true
		}
		l = 1
	}
	return &l, false
}

// managementConfig renders management_mappings / management_limits and the
// identities of full groups to block. Caller holds m.mu.
func (m *Manager) managementConfig() (mappings, limits []any, blocked []map[string]string) {
	mappings, limits = []any{}, []any{}
	if m.desired.UserLimits == nil {
		return mappings, limits, nil
	}
	budgets := m.effectiveGroupBudgets()
	members := map[string][]string{}
	for _, n := range m.desired.Nodes {
		if n.QuotaGroup == "" || n.Chain != nil || n.Listen != "" {
			continue
		}
		if _, ok := budgets[n.QuotaGroup]; !ok {
			continue
		}
		members[n.QuotaGroup] = append(members[n.QuotaGroup], n.InboundTag)
		mappings = append(mappings, map[string]any{"identity": map[string]string{"inbound_tag": n.InboundTag, "user": n.InboundTag}, "group": n.QuotaGroup})
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		live := m.groupLive[name]
		conns, blockConns := groupConnLimit(budgets[name], live)
		ips, blockIPs := groupIPLimit(budgets[name], live)
		if blockConns || blockIPs {
			for _, tag := range members[name] {
				blocked = append(blocked, map[string]string{"inbound_tag": tag, "user": tag})
			}
		}
		limits = append(limits, map[string]any{"group": name, "max_inbound_connections": conns, "max_inbound_online_ips": ips})
	}
	return mappings, limits, blocked
}

// relaySet parses the desired relay sources. Caller holds m.mu.
func (m *Manager) relaySet() map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	if m.desired.UserLimits == nil {
		return out
	}
	for _, raw := range m.desired.UserLimits.RelaySources {
		if ip, err := netip.ParseAddr(raw); err == nil {
			out[ip.Unmap()] = true
		}
	}
	for _, loop := range []string{"127.0.0.1", "::1"} {
		out[netip.MustParseAddr(loop)] = true
	}
	return out
}

// recordGroups derives per-group live state and usage from a snapshot.
// Caller holds m.mu.
func (m *Manager) recordGroups(snapshot coreSnapshot) {
	if m.desired.UserLimits == nil {
		m.groupLive = nil
		m.groupUsage.Store(&[]GroupUsage{})
		return
	}
	relay := m.relaySet()
	live := map[string]groupLive{}
	usage := []GroupUsage{}
	for _, g := range snapshot.ManagementGroups {
		if !validUserGroupName(g.Group) {
			continue
		}
		l := groupLive{current: g.InboundCurrent, seenIPs: len(g.InboundOnlineIPs)}
		direct := []string{}
		for _, ip := range g.InboundOnlineIPs {
			addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(ip.IP), "[]"))
			if err == nil && relay[addr.Unmap()] {
				l.relayConns += ip.Connections
				l.relayIPs++
				continue
			}
			if ip.Connections > 0 && len(direct) < maxUsageIPsPerGroup {
				if err == nil {
					direct = append(direct, addr.Unmap().String())
				} else {
					direct = append(direct, ip.IP)
				}
			}
		}
		if l.relayConns > l.current {
			l.relayConns = l.current
		}
		live[g.Group] = l
		sort.Strings(direct)
		usage = append(usage, GroupUsage{Group: g.Group, Connections: int(l.current - l.relayConns), ClientIPs: direct, RelayConnections: int(l.relayConns)})
	}
	sort.Slice(usage, func(i, j int) bool { return usage[i].Group < usage[j].Group })
	m.groupLive = live
	m.groupUsage.Store(&usage)
}

// GroupUsages returns the last per-user landing usage (no Core call).
func (m *Manager) GroupUsages() []GroupUsage {
	if p := m.groupUsage.Load(); p != nil {
		return append([]GroupUsage(nil), (*p)...)
	}
	return nil
}
