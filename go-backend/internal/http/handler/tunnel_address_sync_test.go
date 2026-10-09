package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
)

func TestTunnelUpdateEntryAddressSync(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addTunnel(8, 1, 1)
	for _, id := range []int64{11, 12} {
		e.addNode(id, fmt.Sprint(id))
		e.exec("UPDATE node SET server_ip=? WHERE id=?", fmt.Sprintf("192.0.2.%d", id), id)
	}
	e.exec("INSERT INTO chain_tunnel(tunnel_id,chain_type,node_id) VALUES(8,'1',11)")
	e.exec("UPDATE tunnel SET in_ip='192.0.2.11' WHERE id=8")
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	for _, uid := range []int64{6, 7, 9} {
		e.addUser(uid)
		e.addUserTunnel(uid, uid, 8)
		e.addForward(uid, uid, 8)
		e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(?,11,?)", uid, 1500+uid)
	}
	e.exec("UPDATE user SET role_id=0 WHERE id=6")
	e.exec("INSERT INTO group_permission_grant(user_group_id,tunnel_group_id,user_tunnel_id,created_by_group,created_time) VALUES(20,30,9,1,1)")
	e.exec("INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id,tunnel_id,node_id,created_time) VALUES(30,8,11,1)")
	// Separate custom bind rule on a retained node, including row identity.
	e.addForward(10, 6, 8)
	e.exec("INSERT INTO forward_port(id,forward_id,node_id,port,in_ip) VALUES(100,10,11,1510,'custom-bind.example')")
	save := func(ids []int64, inIP *string) {
		t.Helper()
		entries := []map[string]interface{}{}
		for _, id := range ids {
			entries = append(entries, map[string]interface{}{"nodeId": id, "chainType": 1})
		}
		body := map[string]interface{}{"id": 8, "name": "autosync", "type": 1, "inNodeId": entries, "status": 1}
		if inIP != nil {
			body["inIp"] = *inIP
		}
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "6", RoleID: 0}))
		w := httptest.NewRecorder()
		e.h.tunnelUpdate(w, r)
		var response struct {
			Code int
			Msg  string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Code != 0 {
			t.Fatalf("save: %s err=%v", w.Body.String(), err)
		}
	}
	value := "192.0.2.11"
	save([]int64{11, 12}, &value)
	check := func(want string, nodes int) {
		t.Helper()
		var tunnel model.Tunnel
		e.r.DB().First(&tunnel, 8)
		if tunnel.InIP.String != want {
			t.Fatalf("in_ip=%q want=%q", tunnel.InIP.String, want)
		}
		list, err := e.r.ListForwards()
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range list {
			id := asInt64(f["id"], 0)
			if id != 6 && id != 7 {
				continue
			}
			ips := asString(f["inIp"])
			if ips != fmt.Sprintf("%s:%d", want, 1500+id) {
				t.Fatalf("rule %d addresses=%s", id, ips)
			}
			ports, _ := e.r.ListForwardPorts(id)
			if len(ports) != nodes {
				t.Fatalf("rule %d ports=%v", id, ports)
			}
		}
	}
	check("192.0.2.11,192.0.2.12", 2)
	if commands.count(12, "UpdateService") == 0 {
		t.Fatal("added entry runtime missing")
	}
	ports, _ := e.r.ListForwardPorts(9)
	if len(ports) != 1 || ports[0].NodeID != 11 {
		t.Fatalf("group restriction changed: %v", ports)
	}
	var custom model.ForwardPort
	e.r.DB().First(&custom, 100)
	if custom.InIP.String != "custom-bind.example" || custom.NodeID != 11 {
		t.Fatalf("custom row changed: %+v", custom)
	}
	// No inIp property: the stored address is the delta's starting point.
	save([]int64{12}, nil)
	check("192.0.2.12", 1)
	if commands.count(11, "DeleteService") == 0 {
		t.Fatal("removed entry runtime was not deleted")
	}
	var count int64
	e.r.DB().Table("forward_port").Where("node_id=11").Count(&count)
	if count != 0 {
		t.Fatal("removed ports remain")
	}
	// Exact old semantics on an unchanged set, then empty rebuild.
	value = "Custom.example, \n192.0.2.12"
	save([]int64{12}, &value)
	var tunnel model.Tunnel
	e.r.DB().First(&tunnel, 8)
	if tunnel.InIP.String != value {
		t.Fatalf("unchanged set normalized %q", tunnel.InIP.String)
	}
	value = ""
	save([]int64{12}, &value)
	check("192.0.2.12", 1)
}

func TestTunnelEntryAddressBuilderPreference(t *testing.T) {
	nodes := map[int64]*nodeRecord{1: {ID: 1, ServerIPv4: "192.0.2.1", ServerIPv6: "2001:db8::1"}, 2: {ID: 2, ServerIP: "fallback.example"}}
	if got := buildTunnelInIP([]tunnelRuntimeNode{{NodeID: 1}, {NodeID: 2}}, nodes, "v6"); got != "2001:db8::1,192.0.2.1,fallback.example" {
		t.Fatal(got)
	}
}
