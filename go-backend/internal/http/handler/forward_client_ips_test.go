package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/ws"
)

func TestQueryForwardClientIPsMergesNodesAndMapsOldAgent(t *testing.T) {
	nodes := []forwardIPNode{{ID: 2, Name: "entry-2"}, {ID: 23, Name: "entry-23"}, {ID: 32, Name: "old"}, {ID: 33, Name: "offline"}}
	send := func(nodeID, forwardID int64) (ws.CommandResult, error) {
		switch nodeID {
		case 2:
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"services": map[string]interface{}{"42_7_8_tcp": map[string]interface{}{"ips": []interface{}{map[string]interface{}{"ip": "192.0.2.1", "connections": 2}}, "ipCount": 1, "connectionCount": 2}}}}, nil
		case 23:
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"services": map[string]interface{}{"42_nft": map[string]interface{}{"ips": []interface{}{map[string]interface{}{"ip": "192.0.2.1", "connections": 1}, map[string]interface{}{"ip": "2001:db8::1", "connections": 3}}, "ipCount": 2, "connectionCount": 4}}}}, nil
		case 32:
			return ws.CommandResult{Type: "UnknownCommandResponse"}, errors.New("未知命令类型: GetServiceClientIPs")
		default:
			return ws.CommandResult{}, errors.New("节点不在线")
		}
	}
	got := queryForwardClientIPs(42, nodes, send)
	if got.IPCount != 2 || got.ConnectionCount != 6 || len(got.IPs) != 2 || got.IPs[0].Connections != 3 || len(got.IPs[0].Nodes) != 2 {
		t.Fatalf("merged result = %+v", got)
	}
	if len(got.NodeErrors) != 2 || got.NodeErrors[1].Reason != "节点 agent 版本过旧，需 fork.13+" {
		t.Fatalf("node errors = %+v", got.NodeErrors)
	}
}

func TestForwardClientIPsAdminOnlyAndOfflineNode(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(7)
	e.addTunnel(8, 1, 1)
	e.addForward(42, 7, 8)
	e.addNode(2, "secret")
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(42, 2, 31001)`)
	request := func(role int) map[string]interface{} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/forward/client-ips", bytes.NewBufferString(`{"id":42}`))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "7", RoleID: role}))
		w := httptest.NewRecorder()
		e.h.forwardClientIPs(w, r)
		var payload map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if got := request(1); got["code"] != float64(403) {
		t.Fatalf("non-admin result = %v", got)
	}
	got := request(0)
	if got["code"] != float64(0) {
		t.Fatalf("admin result = %v", got)
	}
	data := got["data"].(map[string]interface{})
	if len(data["nodeErrors"].([]interface{})) != 1 || data["connectionCount"] != float64(0) {
		t.Fatalf("offline node result = %v", data)
	}
}
