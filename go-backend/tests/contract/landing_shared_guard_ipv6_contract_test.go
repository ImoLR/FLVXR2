package contract_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
)

// TestSharedLandingRealityGuardIPv6Contract (plan 129) through the real router
// and a strict fake Custom (unknown desired fields → 422):
//   - landing-reality-guard-v1: no reality_guard to a share without the
//     capability; true (default on) once advertised; the Helper's report
//     reaches the admin server view only;
//   - IPv6 online IPs: a "x::/64" entry from Custom is listed exactly as
//     sent, counted as one IP.
func TestSharedLandingRealityGuardIPv6Contract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			id := e.node(t, "vless_reality_vision")
			poll := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }
			poll()
			e.f.mu.Lock()
			if d := e.f.desired[7]; d.RealityGuard != nil || len(d.Nodes) != 1 {
				e.f.mu.Unlock()
				t.Fatalf("older Custom desired %+v", d)
			}
			e.f.shares[0].RealityGuard = &landingcustom.ShareRealityGuard{Versions: []int{1}}
			e.f.mu.Unlock()
			poll()
			e.f.mu.Lock()
			d := e.f.desired[7]
			st := e.f.status[7]
			st.CollectedAt++
			two := 2
			st.Nodes = []landingcustom.NodeStatus{{ID: strconv.FormatInt(id, 10), State: "active", Connections: 4, OnlineIPCount: &two,
				OnlineIPs: []landingcustom.OnlineIP{{IP: "2408:8748:a100:59::/64", Connections: 3}, {IP: "198.51.100.7", Connections: 1}}}}
			st.RealityGuard = &landingcustom.RealityGuardStatus{State: "on", Nodes: 1}
			e.f.status[7] = st
			// The other share keeps the capability absent.
			other := e.f.desired[8]
			e.f.mu.Unlock()
			if d.RealityGuard == nil || !*d.RealityGuard {
				t.Fatalf("advertised share desired %+v", d.RealityGuard)
			}
			if other.RealityGuard != nil {
				t.Fatal("share without the capability got reality_guard")
			}
			poll()

			// Admin view: guard on, 1 node; users never see the guard.
			list := e.ok(t, "server/list", e.admin, map[string]any{})
			raw, _ := json.Marshal(list.Data)
			if !strings.Contains(string(raw), `"realityGuard":{"enabled":true,"nodes":1,"state":"on"}`) {
				t.Fatalf("admin view without the guard report: %s", raw)
			}
			list = e.ok(t, "server/list", e.user, map[string]any{})
			raw, _ = json.Marshal(list.Data)
			if strings.Contains(string(raw), "realityGuard") || strings.Contains(string(raw), "reality_guard") {
				t.Fatalf("user view exposes the guard: %s", raw)
			}

			// Online IPs: /64 listed as sent, the count is Custom's (the monitor
			// needs the admin or a monitor grant; permissions are covered by
			// TestLandingMonitorIPsPermissionsAndUnknown).
			for _, token := range []string{e.admin} {
				ips := e.ok(t, "monitor/ips", token, map[string]any{"nodeId": id})
				raw, _ = json.Marshal(ips.Data)
				var view struct {
					OnlineIPs *int                     `json:"onlineIps"`
					IPs       []landingcustom.OnlineIP `json:"ips"`
				}
				_ = json.Unmarshal(raw, &view)
				if view.OnlineIPs == nil || *view.OnlineIPs != 2 || len(view.IPs) != 2 || view.IPs[0].IP != "2408:8748:a100:59::/64" || view.IPs[0].Connections != 3 || view.IPs[1].IP != "198.51.100.7" {
					t.Fatalf("online IPs %s", raw)
				}
			}
		})
	}
}
