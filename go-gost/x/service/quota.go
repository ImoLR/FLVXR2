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
}

type QuotaGroupUsage struct {
	Group       string   `json:"group"`
	Connections int      `json:"connections"`
	ClientIPs   []string `json:"clientIps"`
}

type quotaGroup struct {
	mu             sync.Mutex
	maxConnections int
	maxClientIPs   int
	connections    int
	clientIPs      map[string]int
	services       int
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

	if g.maxConnections >= 0 && g.connections >= g.maxConnections {
		return "group connections", g.maxConnections, false
	}
	if g.maxClientIPs >= 0 && g.clientIPs[clientIP] == 0 && len(g.clientIPs) >= g.maxClientIPs {
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

func (g *quotaGroup) setBudgets(maxConnections, maxClientIPs int) {
	g.mu.Lock()
	g.maxConnections = maxConnections
	g.maxClientIPs = maxClientIPs
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
		if budget.MaxConnections < UnlimitedQuota || budget.MaxClientIPs < UnlimitedQuota {
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
		g.setBudgets(budget.MaxConnections, budget.MaxClientIPs)
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
		if item.group.services == 0 && item.group.connections == 0 {
			item.group.mu.Unlock()
			continue
		}
		usage := QuotaGroupUsage{
			Group:       item.name,
			Connections: item.group.connections,
			ClientIPs:   make([]string, 0, len(item.group.clientIPs)),
		}
		for clientIP := range item.group.clientIPs {
			usage.ClientIPs = append(usage.ClientIPs, clientIP)
		}
		item.group.mu.Unlock()
		sort.Strings(usage.ClientIPs)
		usages = append(usages, usage)
	}
	return usages
}
