package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/ws"
)

func TestTunnelEntryRuleCreateReconcileAndVisibility(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(7)
	e.addTunnel(8, 1, 1)
	e.addUserTunnel(9, 7, 8)
	for _, id := range []int64{11, 12, 13} {
		e.addNode(id, fmt.Sprint(id))
	}
	e.exec("UPDATE node SET server_ip='192.0.2.11' WHERE id=11")
	e.exec("UPDATE node SET server_ip='192.0.2.12' WHERE id=12")
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 11), (8, '1', 12)")
	e.exec("INSERT INTO user_group_user(user_group_id, user_id, created_time) VALUES(20, 7, 1)")
	e.exec("INSERT INTO group_permission(user_group_id, tunnel_group_id, created_time) VALUES(20, 30, 1)")
	e.exec("INSERT INTO group_permission_grant(user_group_id, tunnel_group_id, user_tunnel_id, created_by_group, created_time) VALUES(20, 30, 9, 1, 1)")
	commands := map[int64][]string{}
	e.h.nodeCommandSender = func(id int64, command string, _ interface{}, _ time.Duration) (ws.CommandResult, error) {
		commands[id] = append(commands[id], command)
		return ws.CommandResult{Success: true}, nil
	}
	call := func(fn http.HandlerFunc, body string) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "7", RoleID: 1}))
		w := httptest.NewRecorder()
		fn(w, r)
		var result struct {
			Code int
			Msg  string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code != 0 {
			t.Logf("response=%s", w.Body.String())
		}
		return result.Code
	}
	assign := func(entries string) {
		t.Helper()
		if code := call(e.h.groupTunnelAssign, `{"groupId":30,"tunnelIds":[8],"tunnelEntries":{"8":`+entries+`}}`); code != 0 {
			t.Fatalf("assign failed %d", code)
		}
	}
	assign("[11]")
	if code := call(e.h.forwardCreate, `{"name":"entry-rule","tunnelId":8,"remoteAddr":"1.1.1.1:443","inPort":1501}`); code != 0 {
		t.Fatalf("create failed %d", code)
	}
	var forwardID int64
	e.r.DB().Table("forward").Select("id").Scan(&forwardID)
	check := func(want ...int64) {
		t.Helper()
		ports, err := e.r.ListForwardPorts(forwardID)
		if err != nil || !reflect.DeepEqual(forwardPortNodeIDs(ports), want) {
			t.Fatalf("ports=%v want=%v err=%v", ports, want, err)
		}
	}
	check(11)
	var retainedID int64
	e.r.DB().Table("forward_port").Select("id").Where("node_id=11").Scan(&retainedID)
	assign("[11,12]")
	check(11, 12)
	if len(commands[12]) == 0 {
		t.Fatal("gained entry was not deployed")
	}
	commands = map[int64][]string{}
	assign("[11]")
	check(11)
	if len(commands[11]) != 0 || len(commands[12]) == 0 {
		t.Fatalf("revocation touched retained entry or missed lost entry: %v", commands)
	}
	var afterID int64
	e.r.DB().Table("forward_port").Select("id").Where("node_id=11").Scan(&afterID)
	if retainedID != afterID {
		t.Fatalf("retained port row replaced: %d -> %d", retainedID, afterID)
	}
	// A newly added entry never receives a port through inherited permission.
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 13)")
	e.h.reconcileTunnelEntryPermissions(0, 8)
	check(11)
	// A tunnel-wide address list must not expose the other entry's address.
	e.exec("UPDATE tunnel SET in_ip='192.0.2.11,192.0.2.12' WHERE id=8")
	forwards, err := e.r.ListForwards()
	if err != nil || len(forwards) != 1 || forwards[0]["inIp"] != "192.0.2.11:1501" {
		t.Fatalf("ingress=%v err=%v", forwards, err)
	}
	if code := call(e.h.forwardUpdate, fmt.Sprintf(`{"id":%d,"inPort":1502}`, forwardID)); code != 0 {
		t.Fatalf("update failed %d", code)
	}
	check(11)
	assign("[]")
	var count int64
	e.r.DB().Table("forward").Count(&count)
	if count != 0 {
		t.Fatal("rule with no allowed entry survived")
	}
	if code := call(e.h.forwardCreate, `{"name":"denied","tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); code == 0 {
		t.Fatal("create with no entry succeeded")
	}
}

func TestTunnelEntryAllocationFailureReported(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(7)
	e.addTunnel(8, 1, 1)
	e.addUserTunnel(9, 7, 8)
	e.addNode(11, "a")
	e.addNode(12, "b")
	e.addForward(42, 7, 8)
	e.addForward(43, 7, 8)
	e.exec("UPDATE node SET port='1501' WHERE id=12")
	e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(42,11,1501),(43,12,1501)")
	f, _ := e.h.getForwardRecord(42)
	warnings := e.h.reconcileForwardEntries(f, []int64{11, 12})
	if len(warnings) == 0 {
		t.Fatal("allocation error not reported")
	}
	ports, _ := e.r.ListForwardPorts(42)
	if len(ports) != 1 || ports[0].NodeID != 11 {
		t.Fatalf("failed allocation changed existing entry: %v", ports)
	}
}

func TestTunnelEntryLegacyRuleCompatibility(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(7)
	e.addTunnel(8, 1, 1)
	e.addUserTunnel(9, 7, 8)
	e.exec("INSERT INTO group_permission_grant(user_group_id, tunnel_group_id, user_tunnel_id, created_by_group, created_time) VALUES(20, 30, 9, 1, 1)")
	call := func(fn http.HandlerFunc, user string, role int, body string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: user, RoleID: role}))
		w := httptest.NewRecorder()
		fn(w, r)
		var result struct {
			Code int
			Msg  string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code == 0 {
			return ""
		}
		return result.Msg
	}
	// Group provenance does not replace the legacy runtime validation error.
	if msg := call(e.h.forwardCreate, "7", 1, `{"name":"legacy","tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); msg != "转发入口端口不存在" {
		t.Fatal(msg)
	}
	e.addNode(11, "entry")
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 11)")
	e.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
		return ws.CommandResult{Success: true}, nil
	}
	// With no user row, retain the old caps validation error, not record not found.
	if msg := call(e.h.forwardCreate, "900", 0, `{"name":"admin","tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); msg != "用户不存在" {
		t.Fatal(msg)
	}
	if entries, err := e.h.allowedTunnelEntries(900, 8, true); err != nil || !reflect.DeepEqual(entries, []int64{11}) {
		t.Fatalf("admin without user row: entries=%v err=%v", entries, err)
	}
	e.addUser(900)
	if msg := call(e.h.forwardCreate, "900", 0, `{"name":"admin","tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); msg != "" {
		t.Fatal(msg)
	}
	var forwardID int64
	e.r.DB().Table("forward").Select("id").Where("user_id = 900").Scan(&forwardID)
	if msg := call(e.h.forwardUpdate, "900", 0, fmt.Sprintf(`{"id":%d,"name":"admin updated"}`, forwardID)); msg != "" {
		t.Fatal(msg)
	}
	e.addForward(500, 7, 8)
	e.exec("DELETE FROM user WHERE id = 900")
	e.exec("DELETE FROM chain_tunnel WHERE tunnel_id = 8")
	if warnings := e.h.reconcileTunnelEntryPermissions(0, 0); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	var count int64
	e.r.DB().Table("forward").Count(&count)
	if count != 2 {
		t.Fatalf("reconciliation deleted unrestricted rules: count=%d", count)
	}
	// Empty group access must not replace an earlier validation's error.
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(8, '1', 11)")
	if msg := call(e.h.forwardCreate, "7", 1, `{"tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); msg != "转发名称和目标地址不能为空" {
		t.Fatalf("validation order changed: %s", msg)
	}
	if msg := call(e.h.forwardCreate, "7", 1, `{"name":"denied","tunnelId":8,"remoteAddr":"1.1.1.1:443"}`); msg != "你没有该隧道入口的权限" {
		t.Fatalf("empty group access accepted: %s", msg)
	}
}

func TestTunnelEntryGroupDeleteRevokesLastGrant(t *testing.T) {
	for _, table := range []string{"tunnel_group", "user_group"} {
		t.Run(table, func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addUser(7)
			e.addUser(8)
			e.addTunnel(10, 1, 1)
			e.addNode(11, "entry")
			e.addUserTunnel(20, 7, 10)
			e.addUserTunnel(21, 8, 10)
			e.addForward(30, 7, 10)
			e.addForward(31, 8, 10)
			e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id) VALUES(10, '1', 11)")
			e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(30,11,1501),(31,11,1502)")
			e.exec("INSERT INTO user_group_user(user_group_id,user_id,created_time) VALUES(40,7,1),(40,8,1)")
			e.exec("INSERT INTO group_permission(user_group_id,tunnel_group_id,created_time) VALUES(40,40,1)")
			e.exec("INSERT INTO group_permission_grant(user_group_id,tunnel_group_id,user_tunnel_id,created_by_group,created_time) VALUES(40,40,20,1,1),(40,40,21,0,1)")
			e.exec("INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id,tunnel_id,node_id,created_time) VALUES(40,10,11,1)")
			e.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
				return ws.CommandResult{Success: true}, nil
			}
			w := httptest.NewRecorder()
			e.h.groupDelete(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"id":40}`)), table)
			var result struct{ Code int }
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Code != 0 {
				t.Fatalf("delete response=%s err=%v", w.Body.String(), err)
			}
			var ids []int64
			e.r.DB().Table("forward").Pluck("id", &ids)
			if !reflect.DeepEqual(ids, []int64{31}) {
				t.Fatalf("group rule retained or direct rule removed: %v", ids)
			}
			ports, err := e.r.ListForwardPorts(31)
			if err != nil || len(ports) != 1 || ports[0].Port != 1502 {
				t.Fatalf("direct ports changed: %v %v", ports, err)
			}
		})
	}
}
