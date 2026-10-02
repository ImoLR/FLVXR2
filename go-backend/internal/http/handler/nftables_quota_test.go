package handler

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestNftablesPayloadIncludesRuleAndUserQuotas(t *testing.T) {
	forward := &forwardRecord{
		ID: 41, UserID: 7, RemoteAddr: "192.0.2.20:8080",
		MaxConnections: 2, MaxClientIps: 1,
		UserMaxConnections: 5, UserMaxClientIps: 3,
	}
	ports := []forwardPortRecord{{NodeID: 9, Port: 31001}}
	for _, tunnelType := range []int{1, 2} {
		rules := buildNftablesRulePayloads(forward, &tunnelRecord{Type: tunnelType}, ports, nil, 12, nil)
		if len(rules) != 2 {
			t.Fatalf("tunnel type %d: got %d rules", tunnelType, len(rules))
		}
		for _, rule := range rules {
			if rule.NodeID != 9 || rule.MaxConnections != 2 || rule.MaxClientIps != 1 || rule.QuotaGroup != "user-7" || rule.GroupMaxConnections != 5 || rule.GroupMaxClientIps != 3 {
				t.Fatalf("tunnel type %d: unexpected quota payload: %+v", tunnelType, rule)
			}
			encoded, err := json.Marshal(rule)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"max_connections", "max_client_ips", "quota_group", "group_max_connections", "group_max_client_ips"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("missing %s in %s", key, encoded)
				}
			}
		}
	}
	forward.UserMaxConnections = 0
	forward.UserMaxClientIps = 3
	rules := buildNftablesRulePayloads(forward, &tunnelRecord{Type: 1}, ports, nil, 12, nil)
	if rules[0].GroupMaxConnections != -1 || rules[0].GroupMaxClientIps != 3 {
		t.Fatalf("unlimited connection budget encoded as %+v", rules[0])
	}
}

func TestQuotaBudgetPushReachesNftOnlyEntry(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "nft-budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	now := time.Now().UnixMilli()
	userID, err := r.CreateUser("nft-only-budget", "pwd", 1, now+86400000, 100, 1, 10, 2, 1, 1, now, 0, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	node := model.Node{Name: "nft-only", Secret: "secret", ServerIP: "127.0.0.1", Port: "31001-31010", Status: 1, Version: sql.NullString{String: "3.0.27-fork.13", Valid: true}, CreatedTime: now, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"}
	if err := r.DB().Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	forward := model.Forward{UserID: userID, UserName: "nft-only-budget", Name: "nft-forward", TunnelID: 1, RemoteAddr: "192.0.2.2:80", Strategy: "fifo", Status: 1, Mode: "nftables", CreatedTime: now, UpdatedTime: now}
	if err := r.DB().Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Create(&model.ForwardPort{ForwardID: forward.ID, NodeID: node.ID, Port: 31001}).Error; err != nil {
		t.Fatal(err)
	}
	var sentTo int64
	coordinator := newQuotaCoordinator(r, func(nodeID int64, budgets []quotaGroupBudget) error {
		sentTo = nodeID
		if len(budgets) != 1 || budgets[0].Group != quotaGroupForUser(userID) || budgets[0].MaxConnections != 2 || budgets[0].MaxClientIps != 1 {
			t.Fatalf("nft-only budget = %+v", budgets)
		}
		return nil
	})
	coordinator.reconcileOnce()
	if sentTo != node.ID {
		t.Fatalf("nft-only entry %d did not receive budget, sent to %d", node.ID, sentTo)
	}
}
