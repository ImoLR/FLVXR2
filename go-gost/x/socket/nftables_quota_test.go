//go:build linux

package socket

import (
	"encoding/json"
	"testing"
)

func TestNftablesQuotaPayloadParsesAndOldJSONIgnoresAdditions(t *testing.T) {
	data := []byte(`{"forward_id":41,"node_id":9,"user_id":7,"protocol":"tcp","port":31001,"target":"192.0.2.2:80","max_connections":2,"max_client_ips":1,"quota_group":"user-7","group_max_connections":-1,"group_max_client_ips":0}`)
	var rule NftablesRulePayload
	if err := json.Unmarshal(data, &rule); err != nil {
		t.Fatal(err)
	}
	quota := nftRuleQuota(rule)
	if quota.MaxConnections != 2 || quota.MaxClientIPs != 1 || quota.Group != "user-7" || quota.GroupMaxConnections != -1 || quota.GroupMaxClientIPs != 0 {
		t.Fatalf("parsed quota = %+v", quota)
	}
	var old struct {
		ForwardID int64  `json:"forward_id"`
		Port      int    `json:"port"`
		Target    string `json:"target"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatalf("old agent rejected extra fields: %v", err)
	}
	if old.ForwardID != 41 || old.Port != 31001 || old.Target != "192.0.2.2:80" {
		t.Fatalf("old agent changed forwarding fields: %+v", old)
	}
	rule = NftablesRulePayload{}
	if err := json.Unmarshal([]byte(`{"forward_id":42,"protocol":"udp","port":31002}`), &rule); err != nil {
		t.Fatal(err)
	}
	if nftRuleQuota(rule).Group != "" || nftRuleQuota(rule).MaxConnections != 0 {
		t.Fatalf("old payload gained limits: %+v", rule)
	}
}
