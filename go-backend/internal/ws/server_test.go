package ws

import (
	"encoding/json"
	"testing"
)

func TestMetricDataForBroadcastRemovesQuotaGroupClientIPs(t *testing.T) {
	raw := []byte(`{"uptime":1,"quotaGroups":[{"group":"user-1","connections":2,"clientIps":["198.51.100.1"]}]}`)
	filtered := metricDataForBroadcast(raw)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(filtered), &fields); err != nil {
		t.Fatalf("filtered metric is invalid JSON: %v", err)
	}
	if _, ok := fields["quotaGroups"]; ok {
		t.Fatalf("quotaGroups leaked into broadcast: %s", filtered)
	}
	if string(fields["uptime"]) != "1" {
		t.Fatalf("unrelated metric field changed: %s", filtered)
	}
}
