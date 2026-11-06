package handler

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"go-backend/internal/ipkey"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 231: 用户总 IP 数 counts an IPv6 /64 as one IP (like the 妙妙屋 Core).
const (
	p231a1 = "2409:8a4c:5c4f:26e1:1111:2222:3333:4444"
	p231a2 = "2409:8a4c:5c4f:26e1:aaaa:bbbb:cccc:dddd"
	p231a3 = "2409:8a4c:5c4f:26e1::9"
	p231a  = "2409:8a4c:5c4f:26e1::/64"
	p231b1 = "2409:8a4c:5c4f:26e2::1"
	p231b  = "2409:8a4c:5c4f:26e2::/64"
)

func keyedNodes(ids ...int64) func(int64) bool {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(id int64) bool { return set[id] }
}

// jiuwei-like: one phone with 3 privacy addresses in one /64 on the entry,
// the same /64 seen by a native landing Core (key form), one IPv4 each.
func TestQuotaPlanCountsIPv6Slash64(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{8: {MaxClientIps: 5}},
		Caps:    map[repo.ServerQuotaKey]int{},
		Landing: []repo.QuotaLandingSlot{{UserID: 8, ServerID: 50, NodeID: 5, NodeStatus: 1, NodeVersion: "3.0.27-fork.95"}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		1: {{UserID: 8}: quotaUsage(4, p231a1, p231a2, p231a3, "192.0.2.1")},
		5: {{UserID: 8, Landing: true}: quotaUsage(2, p231a, "198.51.100.1")},
	}
	targets := []repo.QuotaGroupTarget{forwardTarget(8, 1), forwardTarget(8, 2), forwardTarget(8, 3)}
	plan := computeQuotaPlanKeyed(targets, extras, usage, nil, keyedNodes(1, 2))
	if got := plan.LiveUsage[8].ClientIps; got != 3 {
		t.Fatalf("live IPs = %d, want 3 (one /64 + 2 IPv4), not 6", got)
	}
	// Entry 1 (ipKey agent): the landing's IPv4 is the only other-only key.
	b1, _ := findBudget(plan, 1, quotaGroupForUser(8))
	if b1.MaxClientIps != 4 || !reflect.DeepEqual(b1.SharedIps, []string{"198.51.100.1"}) {
		t.Fatalf("entry 1 = %+v", b1)
	}
	if u := plan.Usage[1][quotaGroupForUser(8)]; u.ClientIps != 1+1 {
		t.Fatalf("entry 1 push usage = %+v, want 2 keys", u)
	}
	// Entry 2 (ipKey agent, idle): every key is other-only, shared in key form.
	b2, _ := findBudget(plan, 2, quotaGroupForUser(8))
	if b2.MaxClientIps != 2 || !reflect.DeepEqual(b2.SharedIps, []string{"192.0.2.1", "198.51.100.1", p231a}) {
		t.Fatalf("entry 2 = %+v", b2)
	}
	// Entry 3 (old agent, idle): budget in keys (5-3), shared list = today's raw list.
	b3, _ := findBudget(plan, 3, quotaGroupForUser(8))
	wantRaw := []string{"192.0.2.1", "198.51.100.1", p231a3, p231a1, p231a, p231a2}
	sort.Strings(wantRaw)
	if b3.MaxClientIps != 2 || !reflect.DeepEqual(b3.SharedIps, wantRaw) {
		t.Fatalf("old entry 3 = %+v, want budget 2 (was 0) and raw list %v", b3, wantRaw)
	}
	// Native landing (the Core counts keys): 5 - {192.0.2.1} — its /64 is local.
	land, _ := findBudget(plan, 5, landingQuotaGroupForUser(8))
	if land.MaxClientIps != 4 || land.SharedIps != nil {
		t.Fatalf("landing = %+v", land)
	}
	if u := plan.Usage[5][landingQuotaGroupForUser(8)]; u.ClientIps != 2 {
		t.Fatalf("landing push usage = %+v", u)
	}
}

// Without any ipKey agent the shared lists are byte-identical to before (raw).
func TestQuotaPlanOldFleetSharedListsUnchanged(t *testing.T) {
	extras := repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{8: {MaxClientIps: 3}}, Caps: map[repo.ServerQuotaKey]int{}}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		1: {{UserID: 8}: quotaUsage(3, p231a1, p231a2, p231b1)},
		2: {{UserID: 8}: quotaUsage(1, p231a1)},
	}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(8, 1), forwardTarget(8, 2)}, extras, usage, nil)
	b2, _ := findBudget(plan, 2, quotaGroupForUser(8))
	if b2.MaxClientIps != 2 || !reflect.DeepEqual(b2.SharedIps, []string{p231a2, p231b1}) {
		t.Fatalf("old entry 2 = %+v, want budget 3-|{b}| = 2 (was 1), raw shared", b2)
	}
	b1, _ := findBudget(plan, 1, quotaGroupForUser(8))
	if b1.MaxClientIps != 3 || b1.SharedIps != nil {
		t.Fatalf("old entry 1 = %+v, want 3 (its /64 holds entry 2's address)", b1)
	}
}

// legacyBudget is the pre-231 rule (raw strings) for the mixed-fleet property.
func legacyBudget(limit int, slots []map[string]struct{}, i int) (int, []string) {
	other := map[string]struct{}{}
	for j, s := range slots {
		if j == i {
			continue
		}
		for ip := range s {
			if _, local := slots[i][ip]; !local {
				other[ip] = struct{}{}
			}
		}
	}
	shared := make([]string, 0, len(other))
	for ip := range other {
		shared = append(shared, ip)
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		shared = nil
	}
	return max(limit-len(other), 0), shared
}

// Mixed fleet: an old agent never gets a smaller IP budget or a different
// shared list than before plan 231, whatever the other agents are; a new
// agent's budget plus the keys used elsewhere never exceeds the limit.
func TestQuotaPlanMixedFleetNeverStricterForOldAgents(t *testing.T) {
	r := rand.New(rand.NewSource(231))
	pool := []string{"192.0.2.1", "192.0.2.2", "198.51.100.7", p231a1, p231a2, p231a3, p231a, p231b1, p231b, "::ffff:192.0.2.1", "2001:db8::5", "2001:db8::6"}
	for round := 0; round < 400; round++ {
		limit := 1 + r.Intn(6)
		nSlots := 2 + r.Intn(4)
		extras := repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{8: {MaxClientIps: limit}}, Caps: map[repo.ServerQuotaKey]int{}}
		usage := map[int64]map[quotaUsageKey]quotaNodeUsage{}
		targets := []repo.QuotaGroupTarget{}
		raw := make([]map[string]struct{}, nSlots)
		keyed := map[int64]bool{}
		for i := 0; i < nSlots; i++ {
			nodeID := int64(i + 1)
			ips := []string{}
			for k := r.Intn(5); k > 0; k-- {
				ips = append(ips, pool[r.Intn(len(pool))])
			}
			u := quotaUsage(len(ips), ips...)
			raw[i] = u.ClientIps
			usage[nodeID] = map[quotaUsageKey]quotaNodeUsage{{UserID: 8}: u}
			targets = append(targets, forwardTarget(8, nodeID))
			keyed[nodeID] = r.Intn(2) == 0
		}
		plan := computeQuotaPlanKeyed(targets, extras, usage, nil, func(id int64) bool { return keyed[id] })
		for i := 0; i < nSlots; i++ {
			nodeID := int64(i + 1)
			b, ok := findBudget(plan, nodeID, quotaGroupForUser(8))
			if !ok {
				t.Fatalf("round %d: no budget for node %d", round, nodeID)
			}
			oldBudget, oldShared := legacyBudget(limit, raw, i)
			if !keyed[nodeID] {
				if b.MaxClientIps < oldBudget || !reflect.DeepEqual(b.SharedIps, oldShared) {
					t.Fatalf("round %d node %d (old agent): budget %d shared %v, before %d %v", round, nodeID, b.MaxClientIps, b.SharedIps, oldBudget, oldShared)
				}
				continue
			}
			otherKeys := map[string]struct{}{}
			local := ipkey.Set(raw[i])
			for j := range raw {
				if j != i {
					for k := range ipkey.Set(raw[j]) {
						if _, ok := local[k]; !ok {
							otherKeys[k] = struct{}{}
						}
					}
				}
			}
			if b.MaxClientIps+len(otherKeys) > limit && b.MaxClientIps != 0 {
				t.Fatalf("round %d node %d: budget %d + other-only %d > limit %d", round, nodeID, b.MaxClientIps, len(otherKeys), limit)
			}
			for _, ip := range b.SharedIps {
				if ipkey.Of(ip) != ip {
					t.Fatalf("round %d node %d: shared entry %q not in key form", round, nodeID, ip)
				}
			}
		}
	}
}

// The coordinator asks the ws session which agents are ipKey capable.
func TestQuotaCoordinatorUsesIPKeyCapability(t *testing.T) {
	source := &staticQuotaSource{targets: []repo.QuotaGroupTarget{
		{UserID: 8, NodeID: 1, MaxClientIps: 2, NodeStatus: 1, NodeVersion: "3.0.27-fork.95"},
		{UserID: 8, NodeID: 2, MaxClientIps: 2, NodeStatus: 1, NodeVersion: "3.0.27-fork.95"},
	}}
	sent := map[int64][]quotaGroupBudget{}
	c := newQuotaCoordinator(source, func(nodeID int64, budgets []quotaGroupBudget) error {
		sent[nodeID] = budgets
		return nil
	})
	c.ipKeyed = keyedNodes(2)
	c.Observe(1, []ws.QuotaGroupUsage{{Group: "user-8", Connections: 2, ClientIps: []string{p231a1, p231a2}}})
	c.reconcileOnce()
	if b := sent[2]; len(b) != 1 || b[0].MaxClientIps != 1 || !reflect.DeepEqual(b[0].SharedIps, []string{p231a}) {
		t.Fatalf("ipKey node 2 got %+v, want budget 2-1 and the /64 key", sent[2])
	}
	if got := c.LiveUsage()[8].ClientIps; got != 1 {
		t.Fatalf("live IPs = %d, want 1", got)
	}
}

type staticQuotaSource struct{ targets []repo.QuotaGroupTarget }

func (s *staticQuotaSource) ListQuotaGroupTargets() ([]repo.QuotaGroupTarget, error) {
	return s.targets, nil
}

// The admin rule view (当前 N 个 IP) lists one row per key: an old agent's raw
// IPv6 addresses of one /64 merge, a new agent's key rows stay as they are.
func TestQueryForwardClientIPsGroupsBySlash64(t *testing.T) {
	nodes := []forwardIPNode{{ID: 1, Name: "old"}, {ID: 2, Name: "new"}}
	svc := func(name string, ips map[string]int) map[string]interface{} {
		list := []interface{}{}
		total := 0
		for ip, n := range ips {
			list = append(list, map[string]interface{}{"ip": ip, "connections": n})
			total += n
		}
		return map[string]interface{}{"services": map[string]interface{}{name: map[string]interface{}{"ips": list, "ipCount": len(ips), "connectionCount": total}}}
	}
	send := func(nodeID, forwardID int64) (ws.CommandResult, error) {
		if nodeID == 1 {
			return ws.CommandResult{Success: true, Data: svc("42_7_8_tcp", map[string]int{p231a1: 1, p231a2: 2, p231a3: 1, "192.0.2.1": 1})}, nil
		}
		return ws.CommandResult{Success: true, Data: svc("42_nft", map[string]int{p231a: 3, p231b: 1})}, nil
	}
	got := queryForwardClientIPs(42, nodes, send)
	if got.IPCount != 3 || len(got.IPs) != 3 || got.ConnectionCount != 9 {
		t.Fatalf("result = %+v", got)
	}
	byIP := map[string]forwardClientIP{}
	for _, ip := range got.IPs {
		byIP[ip.IP] = ip
	}
	if a := byIP[p231a]; a.Connections != 7 || fmt.Sprint(a.Nodes) != "[new old]" {
		t.Fatalf("/64 row = %+v", a)
	}
}
