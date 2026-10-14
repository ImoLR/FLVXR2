package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
)

func TestMonitorNodeScope(t *testing.T) {
	for _, tc := range []struct {
		name                                                   string
		chain, ownRule, restricted, allowA, otherRule, removed bool
		want                                                   []int64
	}{
		{name: "port forward without rules", want: []int64{11}},
		{name: "two entries hop exit and one rule", chain: true, ownRule: true, want: []int64{11, 12, 13, 14}},
		{name: "only entry A permitted", chain: true, ownRule: true, restricted: true, allowA: true, want: []int64{11, 13, 14}},
		{name: "no entry permitted skips entire tunnel including own rule", chain: true, ownRule: true, restricted: true, want: []int64{}},
		{name: "other users rule cannot expose entry B", chain: true, restricted: true, allowA: true, otherRule: true, want: []int64{11, 13, 14}},
		{name: "own historical entry retained", chain: true, restricted: true, allowA: true, removed: true, want: []int64{11, 13, 14, 15}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addUser(7)
			e.addUser(8)
			e.addTunnel(8, 1, 1)
			e.addUserTunnel(9, 7, 8)
			e.exec("INSERT INTO monitor_permission(user_id, full_access, created_time) VALUES(7, 0, 1)")
			for id := int64(11); id <= 16; id++ {
				e.addNode(id, fmt.Sprint(id))
			}
			e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 11)")
			if tc.chain {
				e.exec("UPDATE tunnel SET type=2 WHERE id=8")
				e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 12), (8, '2', 13), (8, '3', 14)")
			}
			if tc.restricted {
				e.exec("INSERT INTO group_permission_grant(user_group_id, tunnel_group_id, user_tunnel_id, created_by_group, created_time) VALUES(20, 30, 9, 1, 1)")
				if tc.allowA {
					e.exec("INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id, tunnel_id, node_id, created_time) VALUES(30, 8, 11, 1)")
				}
			}
			if tc.ownRule || tc.removed {
				e.addForward(42, 7, 8)
				e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(42, 11, 1501)")
				if tc.removed {
					e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(42, 15, 1501)")
				}
			}
			if tc.otherRule {
				e.addForward(43, 8, 8)
				e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(43, 12, 1501), (43, 15, 1501)")
			}
			// Inactive tunnel grants cannot expose even the user's own rule entries.
			e.addTunnel(18, 1, 1)
			e.addUserTunnel(19, 7, 18)
			e.exec("UPDATE user_tunnel SET status=0 WHERE id=19")
			e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(18, '1', 16)")
			e.addForward(44, 7, 18)
			e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(44, 16, 1501)")
			scope := &monitorScope{userID: 7, roleID: 1}
			ids, err := e.h.getAccessibleNodeIDs(scope)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("accessible nodes=%v want=%v", ids, tc.want)
			}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/monitor/nodes", nil)
			r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "7", RoleID: 1}))
			w := httptest.NewRecorder()
			e.h.monitorNodeListHandler(w, r)
			var result struct {
				Code int
				Data []monitorNodeListItem
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Code != 0 {
				t.Fatalf("list: %s err=%v", w.Body.String(), err)
			}
			var listed []int64
			for _, node := range result.Data {
				listed = append(listed, node.ID)
			}
			slices.Sort(listed)
			if !slices.Equal(listed, tc.want) {
				t.Fatalf("listed=%v want=%v", listed, tc.want)
			}
			for id := int64(11); id <= 16; id++ {
				wantAccess := slices.Contains(tc.want, id)
				if got := e.h.checkNodeAccess(scope, id); got != wantAccess {
					t.Fatalf("node %d access=%v want=%v", id, got, wantAccess)
				}
				w := httptest.NewRecorder()
				e.h.handleNodeMetricsLatest(w, r, fmt.Sprint(id), scope)
				var metrics struct{ Code int }
				if err := json.Unmarshal(w.Body.Bytes(), &metrics); err != nil {
					t.Fatal(err)
				}
				if wantAccess && metrics.Code != 0 || !wantAccess && metrics.Code != 403 {
					t.Fatalf("node %d metrics: %s", id, w.Body.String())
				}
			}
		})
	}
}
