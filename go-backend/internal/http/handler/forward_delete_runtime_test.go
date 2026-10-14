package handler

import (
	"errors"
	"fmt"
	"go-backend/internal/store/model"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCascadeDeleteForwardRuntime(t *testing.T) {
	for _, route := range []string{"tunnel", "tunnel-batch", "tunnel-with-forwards", "tunnel-batch-with-forwards", "user", "user-batch", "forward-batch", "revoke", "remote", "internal-batch"} {
		for _, offline := range []bool{false, true} {
			// Explicit forward-batch deletion keeps its existing failure response.
			if offline && route == "forward-batch" {
				continue
			}
			t.Run(fmt.Sprintf("%s/offline=%v", route, offline), func(t *testing.T) {
				e, commands := newTunnelRedeployEnv(t)
				addRedeployForwards(e, "gost")
				e.addForward(101, 7, 10)
				e.exec("UPDATE forward SET status=0 WHERE id=101")
				e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(101,5,1506)")
				e.addUser(8)
				e.addTunnel(11, 1, 1)
				e.addForward(102, 8, 11)
				e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(102,1,1507)")
				commands.fail = func(nodeID int64, method string) error {
					if method == "DeleteService" && (strings.HasPrefix(route, "tunnel") || strings.HasPrefix(route, "user")) {
						for _, id := range []int64{100, 101} {
							if !e.h.forwardExists(id) {
								t.Errorf("forward %d removed before cleanup", id)
							}
						}
					}
					if offline && nodeID == 1 {
						return errors.New("节点不在线")
					}
					return nil
				}
				var fn http.HandlerFunc
				body := map[string]interface{}{"id": 10, "ids": []int64{10}}
				switch route {
				case "tunnel":
					fn = e.h.tunnelDelete
				case "tunnel-batch":
					fn = e.h.tunnelBatchDelete
				case "tunnel-with-forwards":
					fn = e.h.tunnelDeleteWithForwards
				case "tunnel-batch-with-forwards":
					fn = e.h.tunnelBatchDeleteWithForwards
				case "user":
					fn, body["id"] = e.h.userDelete, 7
				case "user-batch":
					fn, body["ids"] = e.h.userBatchDelete, []int64{7}
				case "forward-batch":
					fn, body["ids"] = e.h.forwardBatchDelete, []int64{100, 101}
				case "revoke":
					e.h.cleanupForwardsForUserTunnel(7, 10)
				case "remote":
					for _, id := range []int64{100, 101} {
						_ = e.h.deleteForwardRuntimeAndRecord(id)
					}
				case "internal-batch":
					if ok, fail := e.h.batchForwardDelete([]int64{100, 101}); ok != 2 || fail != 0 {
						t.Fatalf("batch %d/%d", ok, fail)
					}
				}
				if strings.Contains(route, "batch") {
					delete(body, "id")
				} else {
					delete(body, "ids")
				}
				if fn != nil {
					if code, msg, data := leiKwanHandlerCall(t, fn, body); code != 0 || asInt(data["failCount"], 0) != 0 {
						t.Fatalf("delete: %d %s %v", code, msg, data)
					}
				}
				for _, id := range []int64{100, 101} {
					if e.h.forwardExists(id) {
						t.Fatalf("forward %d survived", id)
					}
					ports, err := e.h.listForwardPorts(id)
					if err != nil || len(ports) != 0 {
						t.Fatalf("ports survived: %v %v", ports, err)
					}
				}
				if !e.h.forwardExists(102) {
					t.Fatal("unrelated forward deleted")
				}
				got := map[int64][]string{}
				for _, c := range commands.snapshot() {
					if c.method != "DeleteService" {
						continue
					}
					for _, name := range c.data.(map[string]interface{})["services"].([]string) {
						if name != "10_tls" {
							got[c.nodeID] = append(got[c.nodeID], name)
						}
					}
				}
				want := map[int64][]string{}
				for _, pair := range [][2]int64{{100, 1}, {100, 5}, {101, 5}} {
					for _, grant := range []int{70, 0} {
						for _, suffix := range []string{"_tcp", "_udp", ""} {
							want[pair[1]] = append(want[pair[1]], fmt.Sprintf("%d_7_%d%s", pair[0], grant, suffix))
						}
					}
				}
				for _, names := range got {
					sort.Strings(names)
				}
				for _, names := range want {
					sort.Strings(names)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("wrong services/nodes: got=%v want=%v", got, want)
				}
			})
		}
	}
}

func TestCascadeDeleteForwardModes(t *testing.T) {
	for _, mode := range []string{"nftables", "wg_path"} {
		for _, route := range []string{"tunnel", "user", "revoke", "forward-batch"} {
			t.Run(mode+"/"+route, func(t *testing.T) {
				e, commands := newTunnelRedeployEnv(t)
				addRedeployForwards(e, mode)
				e.exec("UPDATE forward SET wg_path_id=20,status=0 WHERE id=100")
				for _, row := range []interface{}{&model.PathTunnel{ID: 20, Name: "path"}, &model.PathSegment{PathID: 20, Sequence: 1, FromNodeID: 1, ToNodeID: 5}} {
					if err := e.r.DB().Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if route != "forward-batch" {
					commands.fail = func(nodeID int64, _ string) error {
						if nodeID == 1 {
							return errors.New("节点不在线")
						}
						return nil
					}
				}
				switch route {
				case "tunnel":
					if err := e.h.deleteTunnelAndCleanup(10); err != nil {
						t.Fatal(err)
					}
				case "user":
					if err := e.h.deleteUserAndCleanup(7); err != nil {
						t.Fatal(err)
					}
				case "revoke":
					e.h.cleanupForwardsForUserTunnel(7, 10)
				case "forward-batch":
					if code, msg, data := leiKwanHandlerCall(t, e.h.forwardBatchDelete, map[string]interface{}{"ids": []int64{100}}); code != 0 || asInt(data["failCount"], 0) != 0 {
						t.Fatalf("%d %s %v", code, msg, data)
					}
				}
				method := "DeleteNftablesRules"
				if mode == "wg_path" {
					method = "RemoveWGForwardRule"
				}
				if commands.count(1, method) != 1 || commands.count(5, method) != 1 || e.h.forwardExists(100) {
					t.Fatalf("mode cleanup missing: %+v", commands.snapshot())
				}
			})
		}
	}
}

func TestTunnelDeleteReplaceKeepsMigratedServices(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			addRedeployForwards(e, "gost")
			e.addTunnel(11, 1, 1)
			node := int64(4)
			if shared {
				node = 5
			}
			e.exec("INSERT INTO chain_tunnel(tunnel_id,chain_type,node_id) VALUES(11,'1',?)", node)
			if code, msg, data := leiKwanHandlerCall(t, e.h.tunnelDeleteWithForwards, map[string]interface{}{"id": 10, "action": "replace", "targetTunnelId": 11}); code != 0 || asInt(data["migratedCount"], 0) != 1 {
				t.Fatalf("migration: %d %s %v", code, msg, data)
			}
			f, err := e.h.getForwardRecord(100)
			if err != nil || f.TunnelID != 11 || e.h.tunnelExists(10) {
				t.Fatalf("migration rows: %+v %v", f, err)
			}
			updated := false
			for _, c := range commands.snapshot() {
				if c.nodeID != node {
					continue
				}
				if c.method == "UpdateService" || c.method == "AddService" {
					updated = true
				}
				if c.method == "DeleteService" {
					for _, name := range c.data.(map[string]interface{})["services"].([]string) {
						if strings.HasPrefix(name, "100_") && (!shared || updated) {
							t.Fatalf("migrated runtime deleted: %+v", c)
						}
					}
				}
			}
			if !updated {
				t.Fatal("target services not deployed")
			}
		})
	}
}
