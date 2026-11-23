package handler

import (
	"net/http"
	"testing"
)

// Plan 298: a grant revoke deletes the user_tunnel row before the forwards are
// cleaned; the DeleteService names must still carry that revoked id.
func TestGroupRevokeDeletesRevokedGrantServices(t *testing.T) {
	routes := []struct {
		name string
		fn   func(*Handler) http.HandlerFunc
		body map[string]interface{}
	}{
		{"group/user/assign", func(h *Handler) http.HandlerFunc { return h.groupUserAssign }, map[string]interface{}{"groupId": 30, "userIds": []int64{}}},
		{"group/tunnel/assign", func(h *Handler) http.HandlerFunc { return h.groupTunnelAssign }, map[string]interface{}{"groupId": 20, "tunnelIds": []int64{}}},
		{"group/permission/remove", func(h *Handler) http.HandlerFunc { return h.groupPermissionRemove }, map[string]interface{}{"id": 40}},
		{"group/user/delete", func(h *Handler) http.HandlerFunc { return h.groupUserDelete }, map[string]interface{}{"id": 30}},
		{"group/tunnel/delete", func(h *Handler) http.HandlerFunc { return h.groupTunnelDelete }, map[string]interface{}{"id": 20}},
		{"tunnel/user/remove", func(h *Handler) http.HandlerFunc { return h.userTunnelRemove }, map[string]interface{}{"id": 70}},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			addRedeployForwards(e, "gost") // user 7, user_tunnel 70 (tunnel 10), forward 100 on nodes 1 + 5
			// A second, unrelated grant: its id is a candidate either way.
			e.addTunnel(11, 1, 1)
			e.addUserTunnel(71, 7, 11)
			e.exec(`INSERT INTO user_group(id, name, created_time, updated_time, status) VALUES(30, 'ug', ?, ?, 1)`, e.now, e.now)
			e.exec(`INSERT INTO user_group_user(user_group_id, user_id, created_time) VALUES(30, 7, ?)`, e.now)
			e.exec(`INSERT INTO tunnel_group(id, name, created_time, updated_time, status) VALUES(20, 'tg', ?, ?, 1)`, e.now, e.now)
			e.exec(`INSERT INTO tunnel_group_tunnel(tunnel_group_id, tunnel_id, created_time) VALUES(20, 10, ?)`, e.now)
			e.exec(`INSERT INTO group_permission(id, user_group_id, tunnel_group_id, created_time) VALUES(40, 30, 20, ?)`, e.now)
			e.exec(`INSERT INTO group_permission_grant(user_group_id, tunnel_group_id, user_tunnel_id, created_by_group, created_time) VALUES(30, 20, 70, 1, ?)`, e.now)

			if code, msg, _ := leiKwanHandlerCall(t, route.fn(e.h), route.body); code != 0 {
				t.Fatalf("%s: %d %s", route.name, code, msg)
			}
			var left int64
			if err := e.r.DB().Table("user_tunnel").Where("id = 70").Count(&left).Error; err != nil || left != 0 {
				t.Fatalf("user_tunnel 70 not revoked: %d %v", left, err)
			}
			if e.h.forwardExists(100) {
				t.Fatal("forward 100 survived")
			}
			for _, nodeID := range []int64{1, 5} {
				sent := map[string]bool{}
				for _, c := range commands.snapshot() {
					if c.nodeID != nodeID || c.method != "DeleteService" {
						continue
					}
					for _, name := range c.data.(map[string]interface{})["services"].([]string) {
						sent[name] = true
					}
				}
				for _, want := range []string{"100_7_70_tcp", "100_7_70_udp"} {
					if !sent[want] {
						t.Fatalf("node %d: DeleteService missing %s, sent %v", nodeID, want, sent)
					}
				}
			}
		})
	}
}
