package handler

import (
	"encoding/json"
	"testing"
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
