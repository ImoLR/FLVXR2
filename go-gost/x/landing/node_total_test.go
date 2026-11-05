package landing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Plan 211: 节点总连接数 next to an inbound limit → both Core port fields; a
// total-mode MaxConnections plus a total keeps the smaller; nodes without the
// new field keep their port item byte for byte.
func TestLandingNodeTotalControlPayload(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	bodies := fakeControl(t, m, "")
	apply := func(nodes ...Node) map[string]map[string]json.RawMessage {
		t.Helper()
		m.desired = Desired{Enabled: true, Nodes: nodes}
		if err := m.applyLimits(context.Background(), coreSnapshot{}); err != nil {
			t.Fatal(err)
		}
		ports, _ := portLimitsOf(t, <-bodies)
		return ports
	}
	both, onlyTotal, totalMode, untouched := testNode(t, 1), testNode(t, 2), testNode(t, 3), testNode(t, 4)
	both.MaxConnections, both.ConnMode, both.MaxTotalConnections = 3, ConnModeInbound, 400
	onlyTotal.ConnMode, onlyTotal.MaxTotalConnections = ConnModeInbound, 400
	totalMode.MaxConnections, totalMode.MaxTotalConnections = 300, 400
	untouched.MaxConnections = 100
	ports := apply(both, onlyTotal, totalMode, untouched)
	if string(ports["flvxl-1"]["max_inbound_connections"]) != "3" || string(ports["flvxl-1"]["max_total_connections"]) != "400" {
		t.Fatalf("inbound + total: %v", ports["flvxl-1"])
	}
	if ports["flvxl-2"]["max_inbound_connections"] != nil || string(ports["flvxl-2"]["max_total_connections"]) != "400" {
		t.Fatalf("total only: %v", ports["flvxl-2"])
	}
	if string(ports["flvxl-3"]["max_total_connections"]) != "300" {
		t.Fatalf("total mode 300 + total 400 must keep 300: %v", ports["flvxl-3"])
	}
	lower := totalMode
	lower.MaxTotalConnections = 200
	if got := apply(lower)["flvxl-3"]; string(got["max_total_connections"]) != "200" {
		t.Fatalf("total mode 300 + total 200 must keep 200: %v", got)
	}
	// A node without the new field: same item as before plan 211.
	old := portLimit{InboundTag: "flvxl-4"}
	old.setConnections(100, "")
	want, _ := json.Marshal(old)
	got, _ := json.Marshal(map[string]json.RawMessage{"inbound_tag": ports["flvxl-4"]["inbound_tag"], "max_total_connections": ports["flvxl-4"]["max_total_connections"], "max_inbound_online_ips": ports["flvxl-4"]["max_inbound_online_ips"]})
	var a, b map[string]any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(got, &b)
	if fmt.Sprint(a) != fmt.Sprint(b) || len(ports["flvxl-4"]) != 3 {
		t.Fatalf("untouched node item changed: %v want %s", ports["flvxl-4"], want)
	}
	// The desired of a node without a total carries no new key (the agent's
	// revision-reuse check compares the canonical form).
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}}
	if raw := canonical(d); bytes.Contains([]byte(raw), []byte("max_total_connections")) {
		t.Fatalf("desired without total carries the key: %s", raw)
	}
	bad := testNode(t, 1)
	bad.MaxTotalConnections = -1
	if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{bad}}).validate(); err == nil {
		t.Fatal("negative total accepted")
	}
}

// Counter.TCP sums inbound_tcp / outbound_tcp of every identity of the tag;
// a Core without state counts leaves it nil (the panel shows no breakdown).
func TestLandingCountersTCPBreakdown(t *testing.T) {
	users := `[{"identity":{"inbound_tag":"flvxl-1","user":"flvxl-1"},"current_total":9,"inbound_current":3,` +
		`"inbound_tcp":{"tcp_total":4,"established":3,"time_wait":0,"close_wait":1},"outbound_tcp":{"tcp_total":5,"established":2,"time_wait":3,"close_wait":0}},` +
		`{"identity":{"inbound_tag":"flvxl-1","user":""},"current_total":2,"inbound_current":0,` +
		`"inbound_tcp":{"tcp_total":2,"established":0,"time_wait":2,"close_wait":0},"outbound_tcp":{"tcp_total":0}}]`
	for _, tt := range []struct {
		name  string
		users string
		want  *TCPCounts
	}{
		{"summed per tag", users, &TCPCounts{Inbound: 6, Outbound: 5, Established: 5, TimeWait: 5, CloseWait: 1}},
		{"older snapshot", `[{"identity":{"inbound_tag":"flvxl-1"},"current_total":4,"inbound_current":1}]`, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "landing-total-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			m := NewManager(dir, true)
			m.apiPort = 1
			m.desired = Desired{Nodes: []Node{{ID: 1, InboundTag: "flvxl-1"}}}
			fakeControl(t, m, fmt.Sprintf(`{"version":7,"started_at":"2026-10-20T00:00:00Z","inbounds":[],"proxy_users":%s}`, tt.users))
			if err := os.WriteFile(m.binary(), []byte("#!/bin/sh\nprintf '%s' '{\"stat\":[]}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := m.collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := m.counters[0]
			if (got.TCP == nil) != (tt.want == nil) || (tt.want != nil && *got.TCP != *tt.want) {
				t.Fatalf("tcp %+v, want %+v", got.TCP, tt.want)
			}
			if got.TCP != nil && got.TCP.Inbound+got.TCP.Outbound != got.Connections {
				t.Fatalf("inbound+outbound %d != connections %d", got.TCP.Inbound+got.TCP.Outbound, got.Connections)
			}
			raw, _ := json.Marshal(got)
			t.Logf("per-node upload JSON: %d bytes %s", len(raw), raw)
		})
	}
}
