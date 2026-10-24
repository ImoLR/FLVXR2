package landing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// fakeControl serves the Core control socket of m and returns every PUT body.
func fakeControl(t *testing.T, m *Manager, snapshot string) <-chan []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(m.socket()), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", m.socket())
	if err != nil {
		t.Fatal(err)
	}
	bodies := make(chan []byte, 8)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(snapshot))
			return
		}
		b, _ := io.ReadAll(r.Body)
		bodies <- b
		_, _ = w.Write([]byte(`{"success":true}`))
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return bodies
}

func portLimitsOf(t *testing.T, body []byte) (map[string]map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	var ports []map[string]json.RawMessage
	if err := json.Unmarshal(cfg["port_limits"], &ports); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]json.RawMessage{}
	for _, p := range ports {
		var tag string
		_ = json.Unmarshal(p["inbound_tag"], &tag)
		out[tag] = p
	}
	return out, cfg
}

// Plan 141: a 仅入站 node's limit goes to the Core's max_inbound_connections
// (total null); 入站+出站 nodes keep today's port item byte for byte.
func TestLandingConnModeControlPayload(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	bodies := fakeControl(t, m, "")
	apply := func(d Desired) []byte {
		t.Helper()
		m.desired = d
		if err := m.applyLimits(context.Background(), coreSnapshot{}); err != nil {
			t.Fatal(err)
		}
		return <-bodies
	}
	total, inbound, unlimited := testNode(t, 1), testNode(t, 2), testNode(t, 3)
	total.MaxConnections, total.MaxOnlineIPs = 100, 5
	inbound.MaxConnections, inbound.ConnMode = 3, ConnModeInbound
	unlimited.ConnMode = ConnModeInbound // 0 = unlimited in either mode

	before := apply(Desired{Enabled: true, MaxConnections: 500, Nodes: []Node{total}})
	ports, cfg := portLimitsOf(t, before)
	if string(ports["flvxl-1"]["max_total_connections"]) != "100" || ports["flvxl-1"]["max_inbound_connections"] != nil {
		t.Fatalf("total node item changed: %s", before)
	}
	if string(cfg["max_global_total_connections"]) != "500" || string(cfg["max_global_inbound_connections"]) != "null" {
		t.Fatalf("total server limit changed: %s", before)
	}
	// Byte-identical to the pre-141 port item for 入站+出站 nodes.
	type oldPortLimit struct {
		InboundTag          string `json:"inbound_tag"`
		MaxTotalConnections *int64 `json:"max_total_connections"`
		MaxInboundOnlineIPs *int   `json:"max_inbound_online_ips"`
	}
	v, ips := int64(100), 5
	oldItem, _ := json.Marshal(oldPortLimit{InboundTag: "flvxl-1", MaxTotalConnections: &v, MaxInboundOnlineIPs: &ips})
	newItem := portLimit{InboundTag: "flvxl-1", MaxInboundOnlineIPs: &ips}
	newItem.setConnections(100, "")
	if raw, _ := json.Marshal(newItem); !bytes.Equal(raw, oldItem) {
		t.Fatalf("total port item %s != pre-141 %s", raw, oldItem)
	}

	after := apply(Desired{Enabled: true, MaxConnections: 500, MaxConnMode: ConnModeInbound, Nodes: []Node{total, inbound, unlimited}})
	ports, cfg = portLimitsOf(t, after)
	if string(ports["flvxl-2"]["max_inbound_connections"]) != "3" || string(ports["flvxl-2"]["max_total_connections"]) != "null" {
		t.Fatalf("inbound node item: %s", after)
	}
	if string(ports["flvxl-1"]["max_total_connections"]) != "100" || ports["flvxl-1"]["max_inbound_connections"] != nil {
		t.Fatalf("total node beside an inbound node: %s", after)
	}
	if string(ports["flvxl-3"]["max_total_connections"]) != "null" || ports["flvxl-3"]["max_inbound_connections"] != nil {
		t.Fatalf("unlimited inbound node: %s", after)
	}
	if string(cfg["max_global_inbound_connections"]) != "500" || string(cfg["max_global_total_connections"]) != "null" {
		t.Fatalf("inbound server limit: %s", after)
	}
	one := apply(Desired{Enabled: true, MaxConnections: 500, Nodes: []Node{total}})
	oneInbound := apply(Desired{Enabled: true, MaxConnections: 500, Nodes: []Node{func() Node { n := total; n.ConnMode = ConnModeInbound; return n }()}})
	t.Logf("control payload, 1 node: 入站+出站 %d bytes, 仅入站 %d bytes (+%d)", len(one), len(oneInbound), len(oneInbound)-len(one))
}

func TestLandingConnModeValidate(t *testing.T) {
	for _, tt := range []struct {
		node, server string
		ok           bool
	}{{"", "", true}, {"total", "total", true}, {ConnModeInbound, ConnModeInbound, true}, {"outbound", "", false}, {"", "INBOUND", false}} {
		n := testNode(t, 1)
		n.ConnMode = tt.node
		d := Desired{Revision: 1, Enabled: true, MaxConnMode: tt.server, Nodes: []Node{n}}
		if err := d.validate(); (err == nil) != tt.ok {
			t.Fatalf("node %q server %q: err=%v", tt.node, tt.server, err)
		}
	}
	// A total desired keeps the canonical form older agents stored (revision
	// reuse check): no conn_mode keys at all.
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1)}}
	if raw := canonical(d); bytes.Contains([]byte(raw), []byte("conn_mode")) {
		t.Fatalf("total desired carries conn_mode: %s", raw)
	}
}

// Counter.InboundConnections = Σ proxy_users inbound_current of the tag, next
// to Connections (Σ current_total), from the same snapshot (no extra call).
func TestLandingCountersInboundConnections(t *testing.T) {
	for _, tt := range []struct {
		name    string
		users   string
		total   int64
		inbound *int64
	}{
		{"summed per tag", `[{"identity":{"inbound_tag":"flvxl-1","user":"flvxl-1"},"current_total":9,"inbound_current":3},{"identity":{"inbound_tag":"flvxl-1","user":""},"current_total":2,"inbound_current":1},{"identity":{"inbound_tag":"flvxl-2"},"current_total":7,"inbound_current":7}]`, 11, int64Pointer(4)},
		{"present zero", `[{"identity":{"inbound_tag":"flvxl-1"},"current_total":4,"inbound_current":0}]`, 4, int64Pointer(0)},
		{"older snapshot unknown", `[{"identity":{"inbound_tag":"flvxl-1"},"current_total":4}]`, 4, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "landing-conn-")
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
			if got.Connections != tt.total || (got.InboundConnections == nil) != (tt.inbound == nil) || (tt.inbound != nil && *got.InboundConnections != *tt.inbound) {
				t.Fatalf("counter %+v inbound=%v, want total %d inbound %v", got, got.InboundConnections, tt.total, tt.inbound)
			}
			raw, _ := json.Marshal(got)
			t.Logf("per-node upload JSON: %d bytes %s", len(raw), raw)
		})
	}
}

func int64Pointer(v int64) *int64 { return &v }
