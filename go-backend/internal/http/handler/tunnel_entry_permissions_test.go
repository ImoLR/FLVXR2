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
