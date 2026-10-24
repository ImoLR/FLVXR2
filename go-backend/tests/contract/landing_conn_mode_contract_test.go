package contract_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
)

// Plan 141 / contract landing-conn-mode-v1 end to end through the API: an old
// Custom never receives conn_mode (its strict decoder would reject it), a
// share advertising conn_modes v1 does; inbound_connections from status is
// what the views show for 仅入站 nodes; users only get the label.
func TestSharedLandingConnModeContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			id := e.node(t, "ss2022")
			other := e.node(t, "vless_reality_vision")
			key := strconv.FormatInt(id, 10)
			desiredNode := func(nodeID string) landingcustom.Node {
				t.Helper()
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				for _, n := range e.f.desired[7].Nodes {
					if n.ID == nodeID {
						return n
					}
				}
				t.Fatalf("node %s not in desired", nodeID)
				return landingcustom.Node{}
			}
			adminNode := func(nodeID int64) map[string]any {
				t.Helper()
				list := e.ok(t, "node/list", e.admin, map[string]any{}).Data.([]any)
				for _, item := range list {
					v := item.(map[string]any)
					if int64(v["id"].(float64)) == nodeID {
						return v
					}
				}
				t.Fatalf("node %d missing", nodeID)
				return nil
			}
			poll := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }

			// Only admins set the mode; the share cap (100) still applies.
			if e.call(t, "node/update", e.user, map[string]any{"id": id, "connMode": "inbound"}).Code == 0 {
				t.Fatal("user changed the conn mode")
			}
			if e.call(t, "node/update", e.admin, map[string]any{"id": id, "connMode": "inbound", "maxConnections": 101}).Code == 0 {
				t.Fatal("share cap bypass with 仅入站")
			}
			if e.call(t, "node/update", e.admin, map[string]any{"id": id, "connMode": "both"}).Code == 0 {
				t.Fatal("invalid conn mode accepted")
			}
			e.ok(t, "node/update", e.admin, map[string]any{"id": id, "connMode": "inbound", "maxConnections": 50})
			if n := desiredNode(key); n.ConnMode != "" || n.MaxConnections != 50 {
				t.Fatalf("old Custom received conn_mode: %+v", n)
			}
			v := adminNode(id)
			if v["connModeEffective"] != "total" || v["connModeConfigured"] != "inbound" || v["connModePending"] != true || v["connMode"] != "inbound" {
				t.Fatalf("pending view %+v", v)
			}
			// Custom + Helper upgraded: the share advertises conn_modes v1.
			e.f.mu.Lock()
			e.f.shares[0].ConnModes = &landingcustom.ShareConnModes{Versions: []int{1}}
			e.f.mu.Unlock()
			poll()
			if n := desiredNode(key); n.ConnMode != landingcustom.ConnModeInbound || n.MaxConnections != 50 {
				t.Fatalf("capable Custom desired: %+v", n)
			}
			if n := desiredNode(strconv.FormatInt(other, 10)); n.ConnMode != "" {
				t.Fatalf("total node got conn_mode: %+v", n)
			}
			// Status with inbound_connections → the 仅入站 node shows it.
			e.f.mu.Lock()
			st := e.f.status[7]
			st.CollectedAt = time.Now().UnixMilli() + 1000
			four := 4
			st.Nodes = []landingcustom.NodeStatus{{ID: key, State: "active", Connections: 9, InboundConnections: &four}, {ID: strconv.FormatInt(other, 10), State: "active", Connections: 6}}
			e.f.status[7] = st
			e.f.mu.Unlock()
			poll()
			if n, _ := e.r.GetLandingNode(id); n.Connections != 9 || n.InboundConnections != 4 {
				t.Fatalf("stored counts %d/%d", n.Connections, n.InboundConnections)
			}
			v = adminNode(id)
			if v["connections"].(float64) != 4 || v["connModeEffective"] != "inbound" || v["connModePending"] != false {
				t.Fatalf("capable view %+v", v)
			}
			if w := adminNode(other); w["connections"].(float64) != 6 || w["connModeEffective"] != "total" {
				t.Fatalf("total node view %+v", w)
			}
			servers := e.ok(t, "server/list", e.admin, map[string]any{}).Data.([]any)
			for _, item := range servers {
				sv := item.(map[string]any)
				if int64(sv["id"].(float64)) == s.ID && (sv["connections"].(float64) != 10 || sv["connectionsMode"] != "mixed" || sv["connModeSupported"] != true || sv["maxConnectionsMode"] != "total") {
					t.Fatalf("server view %+v", sv)
				}
			}
			// The user sees counts + label, never the admin internals.
			raw, _ := json.Marshal(e.ok(t, "node/list", e.user, map[string]any{}).Data)
			if strings.Contains(string(raw), "connModePending") || strings.Contains(string(raw), "inboundConnections") || strings.Contains(string(raw), `"connMode"`) || !strings.Contains(string(raw), `"connModeEffective":"inbound"`) {
				t.Fatalf("user node view: %s", raw)
			}
			raw, _ = json.Marshal(e.ok(t, "server/list", e.user, map[string]any{}).Data)
			if strings.Contains(string(raw), "maxConnMode") || strings.Contains(string(raw), `"connMode"`) {
				t.Fatalf("user server view: %s", raw)
			}
			// Server default 仅入站 + 「应用到现有节点」 → every node follows.
			cur, err := e.r.GetLandingServer(s.ID)
			if err != nil || cur == nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(cur)
			body := map[string]any{}
			_ = json.Unmarshal(b, &body)
			body["protocols"], body["realityServerNames"], body["userGroupIds"] = []string{"ss2022", "vless_reality_vision"}, []string{"www.lovelive-anime.jp"}, []int64{701}
			body["connMode"], body["defaultMaxConnections"], body["applyConnModeToNodes"] = "inbound", 40, true
			e.ok(t, "server/save", e.admin, body)
			for _, nodeID := range []int64{id, other} {
				n, _ := e.r.GetLandingNode(nodeID)
				if n.ConnMode != nil || n.MaxConnections != 40 {
					t.Fatalf("apply to existing: %d mode=%v max=%d", nodeID, n.ConnMode, n.MaxConnections)
				}
			}
			if n := desiredNode(strconv.FormatInt(other, 10)); n.ConnMode != landingcustom.ConnModeInbound || n.MaxConnections != 40 {
				t.Fatalf("following node desired %+v", n)
			}
			// A shared total stays 妙妙屋's 入站+出站.
			body["maxConnMode"], body["applyConnModeToNodes"] = "inbound", false
			if e.call(t, "server/save", e.admin, body).Code == 0 {
				t.Fatal("shared 仅入站 total accepted")
			}
			// Capability withdrawn: back to 入站+出站 payload, hint again.
			e.f.mu.Lock()
			e.f.shares[0].ConnModes = nil
			e.f.mu.Unlock()
			poll()
			if n := desiredNode(key); n.ConnMode != "" {
				t.Fatalf("withdrawn capability still sends conn_mode %+v", n)
			}
			if v = adminNode(id); v["connModePending"] != true || v["connModeEffective"] != "total" {
				t.Fatalf("withdrawn view %+v", v)
			}
		})
	}
}
