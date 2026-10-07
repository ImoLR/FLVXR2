package ws

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
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

func TestIsNodeConnected(t *testing.T) {
	var absent *Server
	if absent.IsNodeConnected(1) {
		t.Fatal("nil server is connected")
	}
	s := &Server{nodes: map[int64]*nodeSession{
		1: nil,
		2: {},
		3: {conn: &connWrap{}},
		4: {conn: &connWrap{conn: &websocket.Conn{}}},
	}}
	for id := int64(0); id <= 4; id++ {
		if got := s.IsNodeConnected(id); got != (id == 4) {
			t.Fatalf("node %d connected=%v", id, got)
		}
	}
}
