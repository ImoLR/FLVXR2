package ws

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"
)

func TestMetricDataForBroadcast(t *testing.T) {
	for _, key := range []string{"quotaGroups", "forward_metrics", "serviceConnections"} {
		t.Run(key, func(t *testing.T) {
			raw := []byte(`{"uptime":1,"cpu_usage":12.5,"` + key + `":[{"clientIps":["198.51.100.1"]}]}`)
			original := string(raw)
			filtered := metricDataForBroadcast(raw)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(filtered), &fields); err != nil {
				t.Fatalf("filtered metric is invalid JSON: %v", err)
			}
			if _, ok := fields[key]; ok {
				t.Fatalf("%s leaked into broadcast: %s", key, filtered)
			}
			if string(fields["uptime"]) != "1" || string(fields["cpu_usage"]) != "12.5" {
				t.Fatalf("unrelated metric field changed: %s", filtered)
			}
			if string(raw) != original {
				t.Fatal("raw metric was mutated")
			}
		})
	}
	t.Run("all fields", func(t *testing.T) {
		raw := []byte(`{"quotaGroups":[],"forward_metrics":[],"serviceConnections":{},"uptime":1}`)
		if got := metricDataForBroadcast(raw); got != `{"uptime":1}` {
			t.Fatalf("unexpected filtered metric: %s", got)
		}
	})
	for _, raw := range []string{` { "uptime" : 1 } `, `{broken`, `null`, `[]`} {
		if got := metricDataForBroadcast([]byte(raw)); got != raw {
			t.Fatalf("fast path changed %q to %q", raw, got)
		}
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
