package contract_test

import (
	"strconv"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
)

// Plan 147 item 4: 妙妙屋 keeps reporting the last connection count of a node
// it already removed (node 103: 29 for hours). That stale count must not reach
// any FLVX total or live row: admin server list, node list, monitor server
// sum and node rows (the owner's views use the same numbers).
func TestSharedRemovedNodeStaleConnectionsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			live := e.node(t, "ss2022")
			gone := e.node(t, "ss2022")
			e.f.mu.Lock()
			st := e.f.status[7]
			st.CollectedAt = time.Now().UnixMilli() + 1000
			st.Nodes = []landingcustom.NodeStatus{
				{ID: strconv.FormatInt(live, 10), State: "active", Connections: 9},
				{ID: strconv.FormatInt(gone, 10), State: "removed", Connections: 29},
			}
			e.f.status[7] = st
			e.f.mu.Unlock()
			e.ok(t, "integration/sync", e.admin, map[string]any{})
			if n, _ := e.r.GetLandingNode(gone); n.RemoteState != "removed" {
				t.Fatalf("remote state %q", n.RemoteState)
			}
			for _, item := range e.ok(t, "server/list", e.admin, map[string]any{}).Data.([]any) {
				if sv := item.(map[string]any); int64(sv["id"].(float64)) == s.ID && sv["connections"].(float64) != 9 {
					t.Fatalf("server total counts the removed node: %v", sv["connections"])
				}
			}
			for _, item := range e.ok(t, "node/list", e.admin, map[string]any{}).Data.([]any) {
				v := item.(map[string]any)
				id := int64(v["id"].(float64))
				if (id == gone && v["connections"].(float64) != 0) || (id == live && v["connections"].(float64) != 9) {
					t.Fatalf("node %d row shows %v", id, v["connections"])
				}
			}
			mon := e.ok(t, "monitor", e.admin, map[string]any{}).Data.(map[string]any)
			for _, item := range mon["servers"].([]any) {
				sv := item.(map[string]any)
				if int64(sv["id"].(float64)) != s.ID {
					continue
				}
				if sv["connections"].(float64) != 9 {
					t.Fatalf("monitor server sum counts the removed node: %v", sv["connections"])
				}
				for _, raw := range sv["nodes"].([]any) {
					n := raw.(map[string]any)
					if int64(n["id"].(float64)) == gone && n["connections"].(float64) != 0 {
						t.Fatalf("monitor row of the removed node shows %v", n["connections"])
					}
				}
			}
		})
	}
}
