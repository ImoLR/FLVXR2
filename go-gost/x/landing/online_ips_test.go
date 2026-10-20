package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLandingCollectOnlineIPs(t *testing.T) {
	many := make([]OnlineIP, 70)
	for i := range many {
		many[i] = OnlineIP{IP: fmt.Sprintf("2001:db8::%x", i+1), Connections: int64(i + 1)}
	}
	manyJSON, err := json.Marshal(many)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		inbounds string
		users    string
		count    *int
		ips      []OnlineIP
	}{
		{
			name:     "inbound preferred with IPv6 and sort ties",
			inbounds: `[{"inbound_tag":"flvxl-2","online_ip_count":9},{"inbound_tag":"flvxl-1","online_ip_count":3,"online_ips":[{"ip":"2001:db8::2","connections":1},{"ip":"203.0.113.4","connections":4},{"ip":"2001:db8::1","connections":4}]}]`,
			users:    `[{"identity":{"inbound_tag":"flvxl-1"},"current_total":9,"inbound_online_ips":[{"ip":"192.0.2.1","connections":99}]}]`,
			count:    intPointer(3),
			ips:      []OnlineIP{{"2001:db8::1", 4}, {"203.0.113.4", 4}, {"2001:db8::2", 1}},
		},
		{
			name:     "present zero",
			inbounds: `[{"inbound_tag":"flvxl-1","online_ip_count":0,"online_ips":[]}]`,
			count:    intPointer(0),
		},
		{
			name:     "old snapshot is unknown",
			inbounds: `[{"inbound_tag":"flvxl-1"}]`,
			users:    `[{"identity":{"inbound_tag":"flvxl-1"},"current_total":9}]`,
		},
		{
			name:     "unmatched inbound is unknown",
			inbounds: `[{"inbound_tag":"flvxl-2","online_ip_count":2,"online_ips":[{"ip":"192.0.2.2","connections":2}]}]`,
		},
		{
			name:     "inbound cap preserves count",
			inbounds: fmt.Sprintf(`[{"inbound_tag":"flvxl-1","online_ip_count":70,"online_ips":%s}]`, manyJSON),
			count:    intPointer(70),
			ips:      reversedTopIPs(many),
		},
		{
			name:     "Core user null is known empty",
			inbounds: `[{"inbound_tag":"flvxl-1"}]`,
			users:    `[{"identity":{"inbound_tag":"flvxl-1"},"inbound_online_ips":null}]`,
			count:    intPointer(0),
		},
		{
			name:     "Core users deduplicate IPv6 and sum connections",
			inbounds: `[{"inbound_tag":"flvxl-1"}]`,
			users:    `[{"identity":{"inbound_tag":"flvxl-1","user":""},"inbound_online_ips":[{"ip":"2001:db8::1","connections":2},{"ip":"192.0.2.1","connections":1}]},{"identity":{"inbound_tag":"flvxl-1","user":"flvxl-1"},"inbound_online_ips":[{"ip":"2001:db8::1","connections":3}]},{"identity":{"inbound_tag":"flvxl-2"},"inbound_online_ips":[{"ip":"192.0.2.2","connections":100}]}]`,
			count:    intPointer(2),
			ips:      []OnlineIP{{"2001:db8::1", 5}, {"192.0.2.1", 1}},
		},
		{
			name:  "any absent Core user field makes node unknown",
			users: `[{"identity":{"inbound_tag":"flvxl-1","user":""},"inbound_online_ips":null},{"identity":{"inbound_tag":"flvxl-1","user":"flvxl-1"}}]`,
		},
		{
			name:  "Core user cap preserves count",
			users: fmt.Sprintf(`[{"identity":{"inbound_tag":"flvxl-1"},"inbound_online_ips":%s}]`, manyJSON),
			count: intPointer(70),
			ips:   reversedTopIPs(many),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "landing-ips-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			m := NewManager(dir, true)
			m.apiPort = 1
			m.desired = Desired{Nodes: []Node{{ID: 1, InboundTag: "flvxl-1"}}}
			if err := os.MkdirAll(filepath.Dir(m.socket()), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", m.socket())
			if err != nil {
				t.Fatal(err)
			}
			inbounds, users := tt.inbounds, tt.users
			if inbounds == "" {
				inbounds = "[]"
			}
			if users == "" {
				users = "[]"
			}
			snapshot := fmt.Sprintf(`{"version":7,"started_at":"2026-10-20T00:00:00Z","inbounds":%s,"proxy_users":%s}`, inbounds, users)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/snapshot" {
					t.Errorf("unexpected control request %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(snapshot))
			})}
			go server.Serve(listener)
			defer server.Close()
			if err := os.WriteFile(m.binary(), []byte("#!/bin/sh\nprintf '%s' '{\"stat\":[]}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			// The latest snapshot must replace old known values, including when
			// the replacement no longer contains online-IP fields.
			m.counters = []Counter{{ID: 1, InboundTag: "flvxl-1", OnlineIPCount: intPointer(1), OnlineIPs: []OnlineIP{{"192.0.2.99", 1}}}}
			if err := m.collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := m.counters[0]
			if !reflect.DeepEqual(got.OnlineIPCount, tt.count) || len(got.OnlineIPs) != len(tt.ips) || (len(tt.ips) > 0 && !reflect.DeepEqual(got.OnlineIPs, tt.ips)) {
				t.Fatalf("online IPs = %+v/%+v, want %+v/%+v", got.OnlineIPCount, got.OnlineIPs, tt.count, tt.ips)
			}
			if len(got.OnlineIPs) > 64 || cap(got.OnlineIPs) > 64 {
				t.Fatalf("online IP list retains too many entries: len=%d cap=%d", len(got.OnlineIPs), cap(got.OnlineIPs))
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if (fields["online_ip_count"] != nil) != (tt.count != nil) {
				t.Fatalf("unknown/zero count JSON presence incorrect: %s", raw)
			}
			if tt.count == nil && fields["online_ips"] != nil {
				t.Fatalf("unknown IP list was encoded: %s", raw)
			}
			t.Logf("per-node upload JSON: %d bytes", len(raw))
		})
	}
}

func intPointer(v int) *int { return &v }

func reversedTopIPs(ips []OnlineIP) []OnlineIP {
	result := make([]OnlineIP, 64)
	for i := range result {
		result[i] = ips[len(ips)-1-i]
	}
	return result
}
