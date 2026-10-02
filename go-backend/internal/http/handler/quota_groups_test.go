package handler

import (
	"errors"
	"sync"
	"testing"

	"go-backend/internal/store/repo"
)

func quotaUsage(connections int, ips ...string) quotaNodeUsage {
	set := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		set[ip] = struct{}{}
	}
	return quotaNodeUsage{Connections: connections, ClientIps: set}
}

func budgetForUser(t *testing.T, plan quotaBudgetPlan, nodeID, userID int64) quotaGroupBudget {
	t.Helper()
	wantGroup := quotaGroupForUser(userID)
	for _, budget := range plan.Budgets[nodeID] {
		if budget.Group == wantGroup {
			return budget
		}
	}
	t.Fatalf("node %d has no budget for %s: %#v", nodeID, wantGroup, plan.Budgets[nodeID])
	return quotaGroupBudget{}
}

func TestComputeQuotaBudgetPlanAcrossThreeNodes(t *testing.T) {
	targets := []repo.QuotaGroupTarget{
		{UserID: 7, NodeID: 1, MaxConnections: 10, MaxClientIps: 4, NodeStatus: 1, NodeVersion: "3.0.27-fork.13"},
		{UserID: 7, NodeID: 2, MaxConnections: 10, MaxClientIps: 4, NodeStatus: 1, NodeVersion: "v3.0.27-fork.13"},
		{UserID: 7, NodeID: 3, MaxConnections: 10, MaxClientIps: 4, NodeStatus: 1, NodeVersion: "3.0.27-fork.14"},
	}
	usage := map[int64]map[int64]quotaNodeUsage{
		1: {7: quotaUsage(4, "198.51.100.1", "198.51.100.2")},
		2: {7: quotaUsage(3, "198.51.100.2", "198.51.100.3")},
		3: {7: quotaUsage(1, "198.51.100.4")},
	}

	plan := computeQuotaBudgetPlan(targets, usage)
	if got := plan.LiveUsage[7]; got.Connections != 8 || got.ClientIps != 4 {
		t.Fatalf("live usage = %#v, want 8 connections and 4 IPs", got)
	}

	wants := map[int64]quotaGroupBudget{
		1: {MaxConnections: 6, MaxClientIps: 2},
		2: {MaxConnections: 5, MaxClientIps: 2},
		3: {MaxConnections: 3, MaxClientIps: 1},
	}
	for nodeID, want := range wants {
		got := budgetForUser(t, plan, nodeID, 7)
		if got.MaxConnections != want.MaxConnections || got.MaxClientIps != want.MaxClientIps {
			t.Errorf("node %d budget = %#v, want connections=%d clientIps=%d", nodeID, got, want.MaxConnections, want.MaxClientIps)
		}
	}
}

func TestComputeQuotaBudgetPlanUsesZeroForFullAndMinusOneForUnlimited(t *testing.T) {
	targets := []repo.QuotaGroupTarget{
		{UserID: 8, NodeID: 1, MaxConnections: 2, MaxClientIps: 0, NodeStatus: 1, NodeVersion: "3.0.27-fork.13"},
		{UserID: 8, NodeID: 2, MaxConnections: 2, MaxClientIps: 0, NodeStatus: 1, NodeVersion: "3.0.27-fork.13"},
	}
	usage := map[int64]map[int64]quotaNodeUsage{
		1: {8: quotaUsage(1, "203.0.113.1")},
		2: {8: quotaUsage(3, "203.0.113.2")},
	}

	plan := computeQuotaBudgetPlan(targets, usage)
	first := budgetForUser(t, plan, 1, 8)
	second := budgetForUser(t, plan, 2, 8)
	if first.MaxConnections != 0 || second.MaxConnections != 1 {
		t.Fatalf("connection budgets = %d/%d, want 0/1", first.MaxConnections, second.MaxConnections)
	}
	if first.MaxClientIps != -1 || second.MaxClientIps != -1 {
		t.Fatalf("unlimited IP budgets = %d/%d, want -1/-1", first.MaxClientIps, second.MaxClientIps)
	}
}

func TestComputeQuotaBudgetPlanSkipsKnownOldAndOfflineAgents(t *testing.T) {
	targets := []repo.QuotaGroupTarget{
		{UserID: 9, NodeID: 1, MaxConnections: 3, NodeStatus: 1, NodeVersion: "3.0.28"},
		{UserID: 9, NodeID: 2, MaxConnections: 3, NodeStatus: 1, NodeVersion: "3.0.27-fork.12"},
		{UserID: 9, NodeID: 3, MaxConnections: 3, NodeStatus: 0, NodeVersion: "3.0.27-fork.13"},
		{UserID: 9, NodeID: 4, MaxConnections: 3, NodeStatus: 1, NodeVersion: "custom-build"},
	}

	plan := computeQuotaBudgetPlan(targets, nil)
	if len(plan.Budgets) != 1 || len(plan.Budgets[4]) != 1 {
		t.Fatalf("budgets = %#v, want only unknown-version node 4", plan.Budgets)
	}
	if quotaAgentSupport("3.0.28") != quotaAgentUnsupported || quotaAgentSupport("3.0.27-fork.12") != quotaAgentUnsupported {
		t.Fatal("known upstream/fork.12 agents must be skipped")
	}
	if quotaAgentSupport("3.0.27-fork.13") != quotaAgentSupported || quotaAgentSupport("custom-build") != quotaAgentUnknown {
		t.Fatal("fork.13/unknown version classification mismatch")
	}
}

type staticQuotaTargets struct {
	targets []repo.QuotaGroupTarget
}

func (s staticQuotaTargets) ListQuotaGroupTargets() ([]repo.QuotaGroupTarget, error) {
	return append([]repo.QuotaGroupTarget(nil), s.targets...), nil
}

func TestQuotaCoordinatorToleratesUnknownOldAgentOnlyOnce(t *testing.T) {
	source := staticQuotaTargets{targets: []repo.QuotaGroupTarget{{
		UserID: 10, NodeID: 5, MaxConnections: 1, NodeStatus: 1, NodeVersion: "custom-build",
	}}}
	var mu sync.Mutex
	commands := 0
	coordinator := newQuotaCoordinator(source, func(_ int64, _ []quotaGroupBudget) error {
		mu.Lock()
		commands++
		mu.Unlock()
		return errors.New("未知命令类型: SetQuotaGroups")
	})

	coordinator.reconcileOnce()
	coordinator.reconcileOnce()
	mu.Lock()
	defer mu.Unlock()
	if commands != 1 {
		t.Fatalf("unknown old agent received %d commands, want one tolerated probe", commands)
	}
}

func TestQuotaCoordinatorPushesOnlyWhenBudgetChanges(t *testing.T) {
	source := staticQuotaTargets{targets: []repo.QuotaGroupTarget{{
		UserID: 11, NodeID: 6, MaxConnections: 5, MaxClientIps: 2, NodeStatus: 1, NodeVersion: "3.0.27-fork.13",
	}}}
	commands := 0
	coordinator := newQuotaCoordinator(source, func(_ int64, budgets []quotaGroupBudget) error {
		commands++
		if len(budgets) != 1 {
			t.Fatalf("budgets = %#v, want one", budgets)
		}
		return nil
	})

	coordinator.reconcileOnce()
	coordinator.reconcileOnce()
	if commands != 1 {
		t.Fatalf("unchanged budget pushed %d times, want once", commands)
	}
}
