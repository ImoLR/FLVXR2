package service

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/go-gost/x/internal/util/ipkey"
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
	// Plan 231: each entry is normalised to its key (an IPv6 address or
	// "<net>/64" covers its whole /64), so raw lists from older panels work.
	SharedIPs []string `json:"sharedIps,omitempty"`
}

// maxSharedIPs bounds one group's shared-IP list.
const maxSharedIPs = 256

type QuotaGroupUsage struct {
	Group       string `json:"group"`
	Connections int    `json:"connections"`
	// ClientIPs are the raw client addresses (wire unchanged by plan 231): the
	// panel keys them itself and builds raw shared lists for older agents.
	ClientIPs []string `json:"clientIps"`
	// RelayConnections (plan 158, landing-user-* groups only): inbound on the
	// landing Core from our own FLVX nodes (forward-rule relays), excluded
	// from Connections / ClientIPs.
	RelayConnections int `json:"relayConnections,omitempty"`
}

// Plan 231: client IPs are counted by key (ipkey.Of: IPv4 = the address,
// IPv6 = its /64, like the 妙妙屋 Core). clientIPs / nftClientIPs / sharedIPs
// hold keys; addrs / nftAddrs keep the raw IPv6 addresses (flat, one map
// increment per IPv6 connection) only for the usage report — IPv4 keys are
// their own address.
type quotaGroup struct {
	mu             sync.Mutex
	maxConnections int
	maxClientIPs   int
	connections    int
	clientIPs      map[string]int
	addrs          map[string]int // raw IPv6 address → gost connections
	nftConnections int
	nftClientIPs   map[string]int
	nftAddrs       map[string]int // raw IPv6 address → conntrack flows
	services       int
	sharedIPs      map[string]struct{} // plan 158: counted elsewhere, never a new IP here
}

// NftQuotaGroup keeps a quota group present while an nftables entry rule uses it.
type NftQuotaGroup struct{ group *quotaGroup }

type QuotaGroupState struct {
	MaxConnections int
	MaxClientIPs   int
	Connections    int
	// ClientIPs = IP keys (plan 231: IPv6 = "<net>/64") that may connect
	// while the IP budget is full: the group's local keys plus its shared
	// keys (plan 158).
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

// SetNftQuotaGroupUsage replaces the conntrack snapshot for one node-local
// group: ips by key (ipkey.Of), addrs = the raw IPv6 addresses behind the
// /64 keys (report only; nil is fine).
func SetNftQuotaGroupUsage(group string, connections int, ips map[string]int, addrs map[string]int) {
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
	var copyAddrs map[string]int
	for addr, count := range addrs {
		if count > 0 {
			if copyAddrs == nil {
				copyAddrs = make(map[string]int, len(addrs))
			}
			copyAddrs[addr] = count
		}
	}
	g.mu.Lock()
	g.nftConnections = connections
	g.nftClientIPs = copyIPs
	g.nftAddrs = copyAddrs
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

// countedIPsLocked counts the distinct local IP keys (gost + nftables) that
// are not shared, i.e. that use this node's IP budget. Caller holds g.mu.
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

// acquire admits one connection of the client with key (ipkey.Of) and raw
// address addr (kept for the report when it differs from key, i.e. IPv6).
func (g *quotaGroup) acquire(key, addr string) (reason string, limit int, ok bool) {
	if g == nil {
		return "", 0, true
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.maxConnections >= 0 && g.connections+g.nftConnections >= g.maxConnections {
		return "group connections", g.maxConnections, false
	}
	_, shared := g.sharedIPs[key]
	if g.maxClientIPs >= 0 && !shared && g.clientIPs[key] == 0 && g.nftClientIPs[key] == 0 && g.countedIPsLocked() >= g.maxClientIPs {
		return "group client IPs", g.maxClientIPs, false
	}

	g.connections++
	g.clientIPs[key]++
	if addr != key && ipkey.IsPrefix(key) {
		if g.addrs == nil {
			g.addrs = make(map[string]int)
		}
		g.addrs[addr]++
	}
	return "", 0, true
}

func (g *quotaGroup) release(key, addr string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.connections > 0 {
		g.connections--
	}
	if count := g.clientIPs[key]; count <= 1 {
		delete(g.clientIPs, key)
	} else {
		g.clientIPs[key] = count - 1
	}
	if addr != key {
		if count := g.addrs[addr]; count <= 1 {
			delete(g.addrs, addr)
		} else {
			g.addrs[addr] = count - 1
		}
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
			if key := ipkey.Of(ip); key != "" {
				g.sharedIPs[key] = struct{}{}
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

// reportIPsLocked lists the group's raw client addresses (gost + nftables,
// deduplicated, sorted): the raw IPv6 addresses, the IPv4 keys, and a /64
// key itself only if none of its addresses is known. Caller holds g.mu.
func (g *quotaGroup) reportIPsLocked() []string {
	seen := make(map[string]struct{}, len(g.clientIPs)+len(g.nftClientIPs)+len(g.addrs)+len(g.nftAddrs))
	out := make([]string, 0, len(g.clientIPs)+len(g.addrs))
	add := func(ip string) {
		if _, ok := seen[ip]; !ok {
			seen[ip] = struct{}{}
			out = append(out, ip)
		}
	}
	var covered map[string]struct{}
	for _, addrs := range []map[string]int{g.addrs, g.nftAddrs} {
		for addr := range addrs {
			add(addr)
			if covered == nil {
				covered = make(map[string]struct{}, len(addrs))
			}
			covered[ipkey.Of(addr)] = struct{}{}
		}
	}
	for _, keys := range []map[string]int{g.clientIPs, g.nftClientIPs} {
		for key := range keys {
			if ipkey.IsPrefix(key) {
				if _, ok := covered[key]; ok {
					continue
				}
			}
			add(key)
		}
	}
	sort.Strings(out)
	return out
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
			ClientIPs:   item.group.reportIPsLocked(),
		}
		item.group.mu.Unlock()
		usages = append(usages, usage)
	}
	return usages
}
