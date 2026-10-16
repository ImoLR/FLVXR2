package handler

import (
	"net/http"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestGroupOrphanDeletePaths(t *testing.T) {
	for _, path := range []string{"tunnel", "tunnel-batch", "tunnel-with-forwards", "tunnel-batch-with-forwards", "federation-cleanup", "federation-node", "user", "user-batch", "revoke", "forward-batch"} {
		t.Run(path, func(t *testing.T) {
			e, _ := newTunnelRedeployEnv(t)
			addRedeployForwards(e, "gost")
			e.addUser(8)
			e.addTunnel(11, 1, 1)
			e.addUserTunnel(71, 8, 11)
			for _, row := range []interface{}{
				&model.TunnelGroup{ID: 20, Name: "delete-group"}, &model.UserGroup{ID: 30, Name: "delete-users"},
				&model.TunnelGroupNew{ID: 21, Name: "display-group"}, &model.TunnelList{ID: 22, Name: "list"},
				&model.TunnelGroupTunnel{TunnelGroupID: 20, TunnelID: 10}, &model.TunnelGroupTunnel{TunnelGroupID: 20, TunnelID: 11},
				&model.TunnelGroupTunnelEntry{TunnelGroupID: 20, TunnelID: 10, NodeID: 1}, &model.TunnelGroupTunnelEntry{TunnelGroupID: 20, TunnelID: 11, NodeID: 5},
				&model.TunnelGroupTunnelNew{TunnelGroupID: 21, TunnelID: 10}, &model.TunnelGroupTunnelNew{TunnelGroupID: 21, TunnelID: 11},
				&model.TunnelListTunnel{TunnelListID: 22, TunnelID: 10}, &model.TunnelListTunnel{TunnelListID: 22, TunnelID: 11},
				&model.GroupPermissionGrant{UserGroupID: 30, TunnelGroupID: 20, UserTunnelID: 70, CreatedByGroup: 1},
				&model.GroupPermissionGrant{UserGroupID: 30, TunnelGroupID: 20, UserTunnelID: 71, CreatedByGroup: 1},
			} {
				if err := e.r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			var fn http.HandlerFunc
			body := map[string]interface{}{"id": 10}
			switch path {
			case "tunnel":
				fn = e.h.tunnelDelete
			case "tunnel-batch":
				fn = e.h.tunnelBatchDelete
				body["ids"] = []int64{10}
			case "tunnel-with-forwards":
				fn = e.h.tunnelDeleteWithForwards
			case "tunnel-batch-with-forwards":
				fn = e.h.tunnelBatchDeleteWithForwards
				body["ids"] = []int64{10}
			case "federation-cleanup":
				e.exec("UPDATE tunnel SET name = 'Share-99-Port-1501' WHERE id = 10")
				e.h.cleanupFederationTunnels(99)
			case "federation-node":
				e.exec("UPDATE node SET is_remote = 1 WHERE id = 1")
				if out := deleteRemoteNodeForTest(t, e.h, 1); out.Code != 0 {
					t.Fatalf("remote delete: %+v", out)
				}
			case "user":
				fn = e.h.userDelete
				body["id"] = 7
			case "user-batch":
				fn = e.h.userBatchDelete
				body["ids"] = []int64{7}
			case "revoke":
				fn = e.h.userTunnelRemove
				body["id"] = 70
			case "forward-batch":
				fn = e.h.forwardBatchDelete
				body["ids"] = []int64{100}
			}
			if strings.Contains(path, "with-forwards") {
				body["action"] = "delete_forwards"
			}
			if fn != nil {
				if strings.Contains(path, "batch") {
					delete(body, "id")
				}
				if code, msg, data := leiKwanHandlerCall(t, fn, body); code != 0 || asInt(data["failCount"], 0) != 0 {
					t.Fatalf("delete: %d %s %v", code, msg, data)
				}
			}
			count := func(table, where string, want int64) {
				t.Helper()
				var got int64
				if err := e.r.DB().Table(table).Where(where).Count(&got).Error; err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("%s %s got %d want %d", table, where, got, want)
				}
			}
			wantGrant := int64(0)
			if path == "forward-batch" {
				wantGrant = 1
			}
			count("group_permission_grant", "user_tunnel_id = 70", wantGrant)
			count("user_tunnel", "id = 70", wantGrant)
			count("group_permission_grant", "user_tunnel_id = 71", 1)
			wantTunnel := int64(1)
			if strings.HasPrefix(path, "tunnel") || strings.HasPrefix(path, "federation") {
				wantTunnel = 0
			}
			for _, table := range []string{"tunnel_group_tunnel", "tunnel_group_tunnel_entry", "tunnel_group_tunnel_new", "tunnel_list_tunnel"} {
				count(table, "tunnel_id = 10", wantTunnel)
				count(table, "tunnel_id = 11", 1)
			}
		})
	}
}

func TestGroupOrphanNodeDeleteHandlers(t *testing.T) {
	for _, batch := range []bool{false, true} {
		e, _ := newTunnelRedeployEnv(t)
		fn := e.h.nodeDelete
		body := map[string]interface{}{"id": 1}
		if batch {
			fn = e.h.nodeBatchDelete
			body = map[string]interface{}{"ids": []int64{1}}
		}
		if code, _, _ := leiKwanHandlerCall(t, fn, body); code == 0 {
			t.Fatal("in-use node deletion reported success")
		}
		var count int64
		if err := e.r.DB().Model(&model.ChainTunnel{}).Where("node_id = 1").Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("chain changed: %d %v", count, err)
		}
		e.addNode(99, "unused-node")
		e.exec("INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id,tunnel_id,node_id,created_time) VALUES(20,10,99,1)")
		if batch {
			body["ids"] = []int64{99}
		} else {
			body["id"] = 99
		}
		if code, msg, _ := leiKwanHandlerCall(t, fn, body); code != 0 {
			t.Fatalf("unused node: %d %s", code, msg)
		}
		if err := e.r.DB().Model(&model.TunnelGroupTunnelEntry{}).Where("node_id = 99").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("entry survived: %d %v", count, err)
		}
	}
}
