package handler

import (
	"sync"
	"testing"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func landingUsage(direct, relay int, ips ...string) quotaNodeUsage {
	u := quotaUsage(direct, ips...)
	u.Relay = relay
	return u
}

func findBudget(plan quotaBudgetPlan, nodeID int64, group string) (quotaGroupBudget, bool) {
	for _, b := range plan.Budgets[nodeID] {
		if b.Group == group {
			return b, true
		}
	}
	return quotaGroupBudget{}, false
}

func forwardTarget(userID, nodeID int64) repo.QuotaGroupTarget {
	return repo.QuotaGroupTarget{UserID: userID, NodeID: nodeID, NodeStatus: 1, NodeVersion: "3.0.27-fork.95"}
}

// A per-server 入站连接数 on one entry limits only that entry; a user with no
// total gets no budget (and no group) elsewhere.
func TestQuotaPlanPerServerCapOnEntryOnly(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users: map[int64]repo.QuotaUserLimits{7: {}},
		Caps:  map[repo.ServerQuotaKey]int{{UserID: 7, Kind: "node", RefID: 1}: 5},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		1: {{UserID: 7}: quotaUsage(3, "198.51.100.1")},
		2: {{UserID: 7}: quotaUsage(10, "198.51.100.2")},
	}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(7, 1), forwardTarget(7, 2)}, extras, usage, nil)
	b, ok := findBudget(plan, 1, quotaGroupForUser(7))
	if !ok || b.MaxConnections != 5 || b.MaxClientIps != -1 {
		t.Fatalf("capped entry budget = %+v (ok=%v), want 5 / -1", b, ok)
	}
	if _, ok := plan.Budgets[2]; ok {
		t.Fatalf("uncapped entry of a user without totals got budgets %+v", plan.Budgets[2])
	}
	if got := plan.LiveUsage[7]; got.Connections != 13 {
		t.Fatalf("live usage = %+v, want 13 (every slot of a limited user)", got)
	}
}

// 用户总连接数 counts landing inbound once: relayed landing connections are
// already counted at the entry.
func TestQuotaPlanTotalIncludesLandingDirectOnly(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {MaxConnections: 10}},
		Caps:    map[repo.ServerQuotaKey]int{},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 50, NodeID: 5, NodeStatus: 1, NodeVersion: "3.0.27-fork.95", RelayNodes: 1}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		1: {{UserID: 7}: quotaUsage(6, "198.51.100.1")},
		5: {{UserID: 7, Landing: true}: landingUsage(3, 6, "198.51.100.9")},
	}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(7, 1)}, extras, usage, nil)
	if got := plan.LiveUsage[7]; got.Connections != 9 || got.ClientIps != 2 {
		t.Fatalf("live usage = %+v, want 9 connections (6 entry + 3 direct landing) / 2 IPs", got)
	}
	fwd, _ := findBudget(plan, 1, quotaGroupForUser(7))
	if fwd.MaxConnections != 7 {
		t.Fatalf("entry budget = %+v, want 10-3 = 7", fwd)
	}
	land, ok := findBudget(plan, 5, landingQuotaGroupForUser(7))
	if !ok || land.MaxConnections != 4 || land.ServerMaxConnections != nil || land.RelayIps != 1 {
		t.Fatalf("landing budget = %+v (ok=%v), want total 10-6 = 4 without relays, no server cap, 1 relay IP", land, ok)
	}
}

// A landing server's 入站连接数 counts what the Core receives (relays too).
func TestQuotaPlanLandingServerCapCountsRelays(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {}},
		Caps:    map[repo.ServerQuotaKey]int{{UserID: 7, Kind: "node", RefID: 5}: 8},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 50, NodeID: 5, NodeStatus: 1}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		5: {{UserID: 7, Landing: true}: landingUsage(3, 6)},
	}
	plan := computeQuotaPlan(nil, extras, usage, nil)
	land, ok := findBudget(plan, 5, landingQuotaGroupForUser(7))
	if !ok || land.MaxConnections != -1 || land.ServerMaxConnections == nil || *land.ServerMaxConnections != 8 {
		t.Fatalf("landing budget = %+v (ok=%v), want total -1 and server 8", land, ok)
	}
}

// One node that is both the user's entry and native landing shares one cap.
func TestQuotaPlanMixedEntryAndLandingShareOneServerCap(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {}},
		Caps:    map[repo.ServerQuotaKey]int{{UserID: 7, Kind: "node", RefID: 5}: 10},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 50, NodeID: 5, NodeStatus: 1}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		5: {{UserID: 7}: quotaUsage(4), {UserID: 7, Landing: true}: landingUsage(2, 1)},
	}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(7, 5)}, extras, usage, nil)
	fwd, _ := findBudget(plan, 5, quotaGroupForUser(7))
	land, _ := findBudget(plan, 5, landingQuotaGroupForUser(7))
	if fwd.MaxConnections != 7 {
		t.Fatalf("entry budget = %+v, want 10-(2+1) = 7", fwd)
	}
	if land.ServerMaxConnections == nil || *land.ServerMaxConnections != 6 {
		t.Fatalf("landing server budget = %+v, want 10-4 = 6", land)
	}
}

// 用户总 IP 数: distinct across entries + landing direct IPs.
func TestQuotaPlanClientIPsAcrossEntryAndLanding(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {MaxClientIps: 2}},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 50, NodeID: 5, NodeStatus: 1, RelayNodes: 2}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{
		1: {{UserID: 7}: quotaUsage(1, "198.51.100.1")},
		5: {{UserID: 7, Landing: true}: landingUsage(1, 3, "198.51.100.2")},
	}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(7, 1)}, extras, usage, nil)
	fwd, _ := findBudget(plan, 1, quotaGroupForUser(7))
	land, _ := findBudget(plan, 5, landingQuotaGroupForUser(7))
	if fwd.MaxClientIps != 1 || land.MaxClientIps != 1 || land.RelayIps != 2 {
		t.Fatalf("IP budgets entry=%+v landing=%+v, want 1 / 1 (+2 relay slots)", fwd, land)
	}
	if got := plan.LiveUsage[7]; got.ClientIps != 2 {
		t.Fatalf("live IPs = %+v, want 2", got)
	}
}

// Shared servers get per-user budgets for the 妙妙屋 exchange.
func TestQuotaPlanSharedSlotBudgets(t *testing.T) {
	extras := repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {MaxConnections: 5}, 8: {}},
		Caps:    map[repo.ServerQuotaKey]int{{UserID: 8, Kind: "landing", RefID: 70}: 3},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 70, Shared: true}, {UserID: 8, ServerID: 70, Shared: true}},
	}
	usage := map[int64]map[quotaUsageKey]quotaNodeUsage{1: {{UserID: 7}: quotaUsage(1)}}
	shares := map[int64]map[int64]quotaNodeUsage{70: {7: landingUsage(2, 0), 8: landingUsage(1, 1)}}
	plan := computeQuotaPlan([]repo.QuotaGroupTarget{forwardTarget(7, 1)}, extras, usage, shares)
	got := plan.Shares[70]
	if len(got) != 2 || got[0].UserID != 7 || got[0].MaxConnections != 4 || got[0].ServerMaxConnections != nil {
		t.Fatalf("share budgets = %+v, want user 7 total 5-1 = 4", got)
	}
	if got[1].UserID != 8 || got[1].MaxConnections != -1 || got[1].ServerMaxConnections == nil || *got[1].ServerMaxConnections != 3 {
		t.Fatalf("share budgets = %+v, want user 8 server cap 3", got)
	}
	if live := plan.LiveUsage[7]; live.Connections != 3 {
		t.Fatalf("live usage = %+v, want 1 entry + 2 shared", live)
	}
}

type extrasQuotaTargets struct {
	mu      sync.Mutex
	targets []repo.QuotaGroupTarget
	extras  repo.QuotaPlanExtras
	loads   int
}

func (s *extrasQuotaTargets) ListQuotaGroupTargets() ([]repo.QuotaGroupTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]repo.QuotaGroupTarget(nil), s.targets...), nil
}

func (s *extrasQuotaTargets) LoadQuotaPlanExtras() (repo.QuotaPlanExtras, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.extras, nil
}

// Landing group usage is parsed from the agent report, and an agent whose
// landing groups disappear gets one empty list (back to its static desired).
func TestQuotaCoordinatorLandingGroupsObserveAndReset(t *testing.T) {
	source := &extrasQuotaTargets{extras: repo.QuotaPlanExtras{
		Users:   map[int64]repo.QuotaUserLimits{7: {MaxConnections: 10}},
		Landing: []repo.QuotaLandingSlot{{UserID: 7, ServerID: 50, NodeID: 5, NodeStatus: 1}},
	}}
	var mu sync.Mutex
	sent := [][]quotaGroupBudget{}
	coordinator := newQuotaCoordinator(source, func(nodeID int64, budgets []quotaGroupBudget) error {
		mu.Lock()
		defer mu.Unlock()
		if nodeID == 5 {
			sent = append(sent, append([]quotaGroupBudget(nil), budgets...))
		}
		return nil
	})
	coordinator.interval = 0
	coordinator.Observe(5, []ws.QuotaGroupUsage{{Group: "landing-user-7", Connections: 2, RelayConnections: 4, ClientIps: []string{"198.51.100.3"}}, {Group: "landing-bogus", Connections: 9}})
	coordinator.reconcileOnce()
	if live := coordinator.LiveUsage()[7]; live.Connections != 2 || live.ClientIps != 1 {
		t.Fatalf("live usage = %+v, want direct landing only (2 / 1 IP)", live)
	}
	mu.Lock()
	if len(sent) != 1 || len(sent[0]) != 1 || sent[0][0].Group != "landing-user-7" || sent[0][0].MaxConnections != 10 {
		t.Fatalf("pushed = %+v, want one landing-user-7 budget of 10", sent)
	}
	mu.Unlock()

	source.mu.Lock()
	source.extras = repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{}}
	source.mu.Unlock()
	coordinator.InvalidateInputs()
	coordinator.reconcileOnce()
	coordinator.reconcileOnce()
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || len(sent[1]) != 0 {
		t.Fatalf("pushed = %+v, want exactly one empty reset list after the groups vanished", sent)
	}
}

// The static inputs are cached between reconciles and re-read on invalidation.
func TestQuotaCoordinatorCachesExtras(t *testing.T) {
	source := &extrasQuotaTargets{extras: repo.QuotaPlanExtras{Users: map[int64]repo.QuotaUserLimits{}}}
	coordinator := newQuotaCoordinator(source, func(int64, []quotaGroupBudget) error { return nil })
	coordinator.reconcileOnce()
	coordinator.reconcileOnce()
	coordinator.reconcileOnce()
	if source.loads != 1 {
		t.Fatalf("extras loaded %d times in 3 reconciles, want 1 (cached)", source.loads)
	}
	coordinator.InvalidateInputs()
	coordinator.reconcileOnce()
	if source.loads != 2 {
		t.Fatalf("extras loaded %d times after invalidation, want 2", source.loads)
	}
}

func TestForwardQuotaGroupJoinsOnServerCap(t *testing.T) {
	f := &forwardRecord{UserID: 3, UserServerConnCaps: map[int64]int{9: 4}}
	if join, conns, ips := forwardQuotaGroup(f, 9); !join || conns != 4 || ips != -1 {
		t.Fatalf("capped entry = %v %d %d, want join with 4 / -1", join, conns, ips)
	}
	if join, _, _ := forwardQuotaGroup(f, 8); join {
		t.Fatal("uncapped entry of a user without totals must not join")
	}
	f.UserMaxConnections, f.UserMaxClientIps = 10, 2
	if join, conns, ips := forwardQuotaGroup(f, 9); !join || conns != 4 || ips != 2 {
		t.Fatalf("capped entry with totals = %v %d %d, want min(10,4)=4 / 2", join, conns, ips)
	}
	if join, conns, _ := forwardQuotaGroup(f, 8); !join || conns != 10 {
		t.Fatalf("uncapped entry with totals = %v %d, want join with 10", join, conns)
	}
}
