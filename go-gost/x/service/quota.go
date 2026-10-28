package service

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// UnlimitedQuota is the wire/config encoding for an unlimited quota-group
// dimension. Zero is intentionally distinct and means that the group is full.
const UnlimitedQuota = -1

type QuotaGroupBudget struct {
	Group          string `json:"group"`
	MaxConnections int    `json:"maxConnections"`
	MaxClientIPs   int    `json:"maxClientIps"`
	// SharedIPs (plan 158): the user's client IPs already counted on other
	// nodes / landing servers. They may connect here without using one of
	// this node's MaxClientIPs (用户总 IP 数 counts distinct IPs everywhere).
	SharedIPs []string `json:"sharedIps,omitempty"`
}

// maxSharedIPs bounds one group's shared-IP list.
const maxSharedIPs = 256

type QuotaGroupUsage struct {
	Group       string   `json:"group"`
	Connections int      `json:"connections"`
	ClientIPs   []string `json:"clientIps"`
	// RelayConnections (plan 158, landing-user-* groups only): inbound on the
	// landing Core from our own FLVX nodes (forward-rule relays), excluded
	// from Connections / ClientIPs.
	RelayConnections int `json:"relayConnections,omitempty"`
}

type quotaGroup struct {
	mu             sync.Mutex
	maxConnections int
	maxClientIPs   int
	connections    int
	clientIPs      map[string]int
	nftConnections int
	nftClientIPs   map[string]int
	services       int
	sharedIPs      map[string]struct{} // plan 158: counted elsewhere, never a new IP here
}

// NftQuotaGroup keeps a quota group present while an nftables entry rule uses it.
type NftQuotaGroup struct{ group *quotaGroup }

type QuotaGroupState struct {
	MaxConnections int
	MaxClientIPs   int
	Connections    int
	// ClientIPs = IPs that may connect while the IP budget is full: the
	// group's local IPs plus its shared IPs (plan 158).
	ClientIPs map[string]struct{}
	// CountedIPs = local IPs that use this node's budget (shared ones don't).
	CountedIPs int
}

func AttachNftQuotaGroup(group string, maxConnections, maxClientIPs int) *NftQuotaGroup {
	if g := attachQuotaGroup(group, maxConnections, maxClientIPs); g != nil {
		return &NftQuotaGroup{group: g}
	}
	return nil
}

func (h *NftQuotaGroup) Detach() {
	if h != nil {
		detachQuotaGroup(h.group)
	}
}

// SetNftQuotaGroupUsage replaces the conntrack snapshot for one node-local group.
func SetNftQuotaGroupUsage(group string, connections int, ips map[string]int) {
	quotaGroupRegistry.Lock()
	g := quotaGroupRegistry.groups[group]
	quotaGroupRegistry.Unlock()
	if g == nil {
		return
	}
	copyIPs := make(map[string]int, len(ips))
	for ip, count := range ips {
		if count > 0 {
			copyIPs[ip] = count
		}
	}
	g.mu.Lock()
	g.nftConnections = connections
	g.nftClientIPs = copyIPs
	g.mu.Unlock()
}

func GetQuotaGroupState(group string) (QuotaGroupState, bool) {
	quotaGroupRegistry.Lock()
	g := quotaGroupRegistry.groups[group]
	quotaGroupRegistry.Unlock()
	if g == nil {
		return QuotaGroupState{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	state := QuotaGroupState{
		MaxConnections: g.maxConnections,
		MaxClientIPs:   g.maxClientIPs,
		Connections:    g.connections + g.nftConnections,
		ClientIPs:      make(map[string]struct{}, len(g.clientIPs)+len(g.nftClientIPs)+len(g.sharedIPs)),
		CountedIPs:     g.countedIPsLocked(),
	}
	for ip := range g.clientIPs {
		state.ClientIPs[ip] = struct{}{}
	}
	for ip := range g.nftClientIPs {
		state.ClientIPs[ip] = struct{}{}
	}
	for ip := range g.sharedIPs {
		state.ClientIPs[ip] = struct{}{}
	}
	return state, true
}

// countedIPsLocked counts the distinct local IPs (gost + nftables) that are
// not shared, i.e. that use this node's IP budget. Caller holds g.mu.
func (g *quotaGroup) countedIPsLocked() int {
	n := 0
	for ip := range g.clientIPs {
		if _, shared := g.sharedIPs[ip]; !shared {
			n++
		}
	}
	for ip := range g.nftClientIPs {
		if _, shared := g.sharedIPs[ip]; !shared && g.clientIPs[ip] == 0 {
			n++
		}
	}
	return n
}

var quotaGroupRegistry = struct {
	sync.Mutex
	groups map[string]*quotaGroup
}{
	groups: make(map[string]*quotaGroup),
}

func attachQuotaGroup(group string, maxConnections, maxClientIPs int) *quotaGroup {
	group = strings.TrimSpace(group)
	if group == "" || (maxConnections < 0 && maxClientIPs < 0) {
		return nil
	}

	quotaGroupRegistry.Lock()
	g := quotaGroupRegistry.groups[group]
	if g == nil {
		g = &quotaGroup{
			maxConnections: maxConnections,
			maxClientIPs:   maxClientIPs,
			clientIPs:      make(map[string]int),
		}
		quotaGroupRegistry.groups[group] = g
	}
	g.mu.Lock()
	g.services++
	g.mu.Unlock()
	quotaGroupRegistry.Unlock()
	return g
}

func detachQuotaGroup(g *quotaGroup) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.services > 0 {
		g.services--
	}
	g.mu.Unlock()
}

func (g *quotaGroup) acquire(clientIP string) (reason string, limit int, ok bool) {
	if g == nil {
		return "", 0, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.maxConnections >= 0 && g.connections+g.nftConnections >= g.maxConnections {
		return "group connections", g.maxConnections, false
	}
	_, shared := g.sharedIPs[clientIP]
	if g.maxClientIPs >= 0 && !shared && g.clientIPs[clientIP] == 0 && g.nftClientIPs[clientIP] == 0 && g.countedIPsLocked() >= g.maxClientIPs {
		return "group client IPs", g.maxClientIPs, false
	}

	g.connections++
	g.clientIPs[clientIP]++
	return "", 0, true
}

func (g *quotaGroup) release(clientIP string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.connections > 0 {
		g.connections--
	}
	if count := g.clientIPs[clientIP]; count <= 1 {
		delete(g.clientIPs, clientIP)
	} else {
		g.clientIPs[clientIP] = count - 1
	}
	g.mu.Unlock()
}

func (g *quotaGroup) setBudgets(maxConnections, maxClientIPs int, shared []string) {
	g.mu.Lock()
	g.maxConnections = maxConnections
	g.maxClientIPs = maxClientIPs
	g.sharedIPs = nil
	if len(shared) > 0 {
		g.sharedIPs = make(map[string]struct{}, len(shared))
		for _, ip := range shared {
			if ip = strings.TrimSpace(ip); ip != "" {
				g.sharedIPs[ip] = struct{}{}
			}
		}
	}
	g.mu.Unlock()
}

// SetQuotaGroupBudgets applies panel-coordinated node budgets without
// restarting services. Values below -1 are invalid; -1 is unlimited and 0 is
// a full pool that rejects new usage.
func SetQuotaGroupBudgets(budgets []QuotaGroupBudget) error {
	normalized := make([]QuotaGroupBudget, len(budgets))
	for i, budget := range budgets {
		budget.Group = strings.TrimSpace(budget.Group)
		if budget.Group == "" {
			return fmt.Errorf("quota group is required")
		}
		if budget.MaxConnections < UnlimitedQuota || budget.MaxClientIPs < UnlimitedQuota || len(budget.SharedIPs) > maxSharedIPs {
			return fmt.Errorf("quota group %s has invalid budget", budget.Group)
		}
		normalized[i] = budget
	}

	for _, budget := range normalized {
		quotaGroupRegistry.Lock()
		g := quotaGroupRegistry.groups[budget.Group]
		if g == nil {
			g = &quotaGroup{
				maxConnections: UnlimitedQuota,
				maxClientIPs:   UnlimitedQuota,
				clientIPs:      make(map[string]int),
			}
			quotaGroupRegistry.groups[budget.Group] = g
		}
		quotaGroupRegistry.Unlock()
		g.setBudgets(budget.MaxConnections, budget.MaxClientIPs, budget.SharedIPs)
	}
	return nil
}

// QuotaGroupUsages returns a stable snapshot for the panel telemetry report.
func QuotaGroupUsages() []QuotaGroupUsage {
	quotaGroupRegistry.Lock()
	type namedGroup struct {
		name  string
		group *quotaGroup
	}
	groups := make([]namedGroup, 0, len(quotaGroupRegistry.groups))
	for name, group := range quotaGroupRegistry.groups {
		groups = append(groups, namedGroup{name: name, group: group})
	}
	quotaGroupRegistry.Unlock()
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })

	usages := make([]QuotaGroupUsage, 0, len(groups))
	for _, item := range groups {
		item.group.mu.Lock()
		if item.group.services == 0 && item.group.connections == 0 && item.group.nftConnections == 0 {
			item.group.mu.Unlock()
			continue
		}
		usage := QuotaGroupUsage{
			Group:       item.name,
			Connections: item.group.connections + item.group.nftConnections,
			ClientIPs:   make([]string, 0, len(item.group.clientIPs)+len(item.group.nftClientIPs)),
		}
		for clientIP := range item.group.clientIPs {
			usage.ClientIPs = append(usage.ClientIPs, clientIP)
		}
		for clientIP := range item.group.nftClientIPs {
			if item.group.clientIPs[clientIP] == 0 {
				usage.ClientIPs = append(usage.ClientIPs, clientIP)
			}
		}
		item.group.mu.Unlock()
		sort.Strings(usage.ClientIPs)
		usages = append(usages, usage)
	}
	return usages
}
