package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
)

func adminPost(t *testing.T, fn http.HandlerFunc, body interface{}) (int, string, json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	fn(w, r)
	var payload struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return payload.Code, payload.Msg, payload.Data
}

func storedExtraIPs(t *testing.T, e *flowTestEnv, name string) (string, bool) {
	t.Helper()
	var row struct {
		ExtraIPs *string
	}
	if err := e.r.DB().Raw(`SELECT extra_ips FROM node WHERE name = ?`, name).Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ExtraIPs == nil {
		return "", false
	}
	return *row.ExtraIPs, true
}

func TestNodeCreateValidatesAndNormalizesExtraIPs(t *testing.T) {
	e := newFlowTestEnv(t)
	body := func(name, extra string) map[string]interface{} {
		return map[string]interface{}{"name": name, "serverIp": "2001:db8::1", "serverIpV4": "", "serverIpV6": "2001:db8::1",
			"port": "1000-2000", "extraIPs": extra}
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, body("two-v6", " 2001:DB8::2\n2001:db8::2,203.0.113.9 ")); code != 0 {
		t.Fatalf("create failed: %d %s", code, msg)
	}
	if got, ok := storedExtraIPs(t, e, "two-v6"); !ok || got != `["2001:db8::2","203.0.113.9"]` {
		t.Fatalf("stored extra_ips = %q (%v)", got, ok)
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, body("plain", "")); code != 0 {
		t.Fatalf("create without extras failed: %d %s", code, msg)
	}
	if got, ok := storedExtraIPs(t, e, "plain"); ok {
		t.Fatalf("empty extras must stay NULL, got %q", got)
	}
	for extra, want := range map[string]string{
		"2001:db8:0::1":   "与节点主 IP 重复",
		"hk.example.test": "不是有效的",
		"::1":             "回环",
	} {
		code, msg, _ := adminPost(t, e.h.nodeCreate, body("bad", extra))
		if code == 0 || !strings.Contains(msg, want) {
			t.Fatalf("extra %q: code=%d msg=%q, want %q", extra, code, msg, want)
		}
	}
	var count int64
	e.r.DB().Raw(`SELECT COUNT(*) FROM node WHERE name = 'bad'`).Scan(&count)
	if count != 0 {
		t.Fatalf("rejected nodes were saved: %d", count)
	}
}

func TestNodeUpdateValidatesExtraIPsBeforeSaving(t *testing.T) {
	e := newNodeSyncEnv(t)
	body := nodeSyncBody("entry", "192.0.2.1")
	body["extraIPs"] = "192.0.2.1"
	code, msg, _ := adminPost(t, e.h.nodeUpdate, body)
	if code == 0 || !strings.Contains(msg, "与节点主 IP 重复") {
		t.Fatalf("primary duplicate accepted: %d %q", code, msg)
	}
	body["extraIPs"] = "2001:db8::2, 2001:db8::3"
	body["name"] = "renamed"
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
	if got, _ := storedExtraIPs(t, e, "renamed"); got != `["2001:db8::2","2001:db8::3"]` {
		t.Fatalf("stored extra_ips = %q", got)
	}
	// Adding extra IPs leaves every saved entry address alone.
	if result.EntryAddressesUpdated != 0 || result.TunnelsUpdated != 0 || result.RulesUpdated != 0 || len(result.Failures) != 0 {
		t.Fatalf("adding extras propagated: %+v", result)
	}
}

func TestExtraIPsAreNeverDialedImplicitly(t *testing.T) {
	from := &nodeRecord{Name: "entry", ServerIPv4: "192.0.2.1"}
	to := &nodeRecord{Name: "exit", ServerIP: "192.0.2.2", ServerIPv4: "192.0.2.2", ExtraIPs: `["198.51.100.7"]`}
	for _, tc := range []struct{ preference, connectType string }{{"", ""}, {"", "lan"}, {"auto", ""}, {"v4", ""}} {
		host, _, err := selectTunnelDialHost(from, to, tc.preference, tc.connectType)
		if err != nil || host != "192.0.2.2" {
			t.Fatalf("preference %q/%q dialed %q (%v), want primary", tc.preference, tc.connectType, host, err)
		}
	}
	host, family, err := selectTunnelDialHost(from, to, "", "", "198.51.100.7")
	if err != nil || host != "198.51.100.7" || family != "v4" {
		t.Fatalf("custom connect IP on an extra address = %q %q %v", host, family, err)
	}
	to.IntranetIP = "10.0.0.2"
	if host, family, _ := selectTunnelDialHost(from, to, "", "lan"); host != "10.0.0.2" || family != "lan" {
		t.Fatalf("intranet dial changed: %q %q", host, family)
	}
}

func TestNodeUpdateRemovedExtraIPClearsBindAndRedeploysRule(t *testing.T) {
	e := newNodeSyncEnv(t)
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	e.addUserTunnel(7, 7, 25)
	e.exec(`UPDATE node SET server_ip_v6 = '2001:db8::1', extra_ips = '["2001:db8::2","2001:db8::3"]' WHERE id = 23`)
	e.exec(`UPDATE tunnel SET in_ip = '2001:db8::2,2001:db8::3' WHERE id = 25`)
	e.exec(`UPDATE forward_port SET in_ip = '2001:db8::3' WHERE forward_id = 42`)
	// Node 23 is also the exit of tunnel 26; extra IPs are not dial addresses,
	// so an extra-IP-only edit must not redeploy that tunnel.
	e.addTunnel(26, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2 WHERE id = 26`)
	e.addNode(24, "other-entry")
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(26, '1', 24, 1, 'tls')`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol, port) VALUES(26, '3', 23, 1, 'tls', 1600)`)

	body := nodeSyncBody("entry", "192.0.2.1")
	body["serverIpV6"] = "2001:db8::1"
	body["extraIPs"] = "2001:db8::2"
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
	// TunnelsUpdated counts tunnel 25, whose saved entry address was rewritten.
	if len(result.Failures) != 0 || result.RulesUpdated != 1 || result.TunnelsUpdated != 1 || result.EntryAddressesUpdated != 2 {
		t.Fatalf("sync result = %+v", result)
	}
	var tunnelInIP, bindIP string
	e.r.DB().Raw(`SELECT in_ip FROM tunnel WHERE id = 25`).Scan(&tunnelInIP)
	e.r.DB().Raw(`SELECT in_ip FROM forward_port WHERE forward_id = 42`).Scan(&bindIP)
	if tunnelInIP != "2001:db8::2" || bindIP != "" {
		t.Fatalf("tunnel in_ip = %q, bind = %q", tunnelInIP, bindIP)
	}
	sawDefaultBind := false
	for _, command := range commands.snapshot() {
		raw, _ := json.Marshal(command.data)
		payload := string(raw)
		if strings.Contains(payload, `"26_tls"`) {
			t.Fatalf("exit tunnel redeployed for an extra-IP edit: %s %s", command.method, payload)
		}
		if strings.Contains(payload, "2001:db8::3") {
			t.Fatalf("removed extra IP still deployed: %s %s", command.method, payload)
		}
		if command.nodeID == 23 && strings.Contains(payload, `"addr":"[::]:1234"`) {
			sawDefaultBind = true
		}
	}
	if !sawDefaultBind {
		t.Fatalf("rule was not redeployed on the default listen address: %+v", commands.snapshot())
	}
}

func TestForwardBindsOnExtraIP(t *testing.T) {
	e := newNodeSyncEnv(t)
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	e.addUserTunnel(7, 7, 25)
	e.exec(`UPDATE node SET server_ip_v6 = '2001:db8::1', extra_ips = '["2001:db8::2"]' WHERE id = 23`)
	e.exec(`UPDATE forward_port SET in_ip = '2001:db8::2' WHERE forward_id = 42`)
	forward, err := e.h.getForwardRecord(42)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.h.syncNodeForwardServices(forward); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, command := range commands.snapshot() {
		raw, _ := json.Marshal(command.data)
		found = found || (command.nodeID == 23 && strings.Contains(string(raw), `"addr":"[2001:db8::2]:1234"`))
	}
	if !found {
		t.Fatalf("rule did not bind the extra IP: %+v", commands.snapshot())
	}
	var forwards struct {
		Items []struct {
			InIP string `json:"inIp"`
		} `json:"items"`
	}
	nodeSyncRequest(t, e.h.forwardList, map[string]interface{}{}, &forwards)
	if len(forwards.Items) != 1 || forwards.Items[0].InIP != "[2001:db8::2]:1234" {
		t.Fatalf("rule list = %+v", forwards)
	}
}

func TestForwardListShowsEveryPickedEntryAddress(t *testing.T) {
	e := newNodeSyncEnv(t)
	e.exec(`UPDATE node SET server_ip_v6 = '2001:db8::1', extra_ips = '["2001:db8::2","2001:db8::3"]' WHERE id = 23`)
	var forwards struct {
		Items []struct {
			InIP string `json:"inIp"`
		} `json:"items"`
	}
	for inIP, want := range map[string]string{
		"2001:db8::2":                      "[2001:db8::2]:1234",
		"2001:db8::2,2001:db8::3":          "[2001:db8::2]:1234,[2001:db8::3]:1234",
		"192.0.2.1,2001:db8::2":            "192.0.2.1:1234,[2001:db8::2]:1234",
		"192.0.2.1,198.51.100.4":           "192.0.2.1:1234,198.51.100.4:1234",
		"entry.example.test,[2001:db8::3]": "entry.example.test:1234,[2001:db8::3]:1234",
	} {
		e.exec(`UPDATE tunnel SET in_ip = ? WHERE id = 25`, inIP)
		nodeSyncRequest(t, e.h.forwardList, map[string]interface{}{}, &forwards)
		if len(forwards.Items) != 1 || forwards.Items[0].InIP != want {
			t.Fatalf("tunnel in_ip %q: rule list = %+v, want %q", inIP, forwards, want)
		}
	}
}

func TestPartialEntryPermissionShowsPicksOfAllowedNode(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addTunnel(8, 1, 1)
	for _, id := range []int64{11, 12} {
		e.addNode(id, fmt.Sprint(id))
		e.exec("UPDATE node SET server_ip=?, server_ip_v4=? WHERE id=?", fmt.Sprintf("192.0.2.%d", id), fmt.Sprintf("192.0.2.%d", id), id)
		e.exec("INSERT INTO chain_tunnel(tunnel_id,chain_type,node_id) VALUES(8,'1',?)", id)
	}
	e.exec(`UPDATE node SET extra_ips='["2001:db8::11"]' WHERE id=11`)
	e.addUser(9)
	e.addUserTunnel(9, 9, 8)
	e.addForward(9, 9, 8)
	e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(9,11,1509),(9,12,1509)")
	e.exec("INSERT INTO group_permission_grant(user_group_id,tunnel_group_id,user_tunnel_id,created_by_group,created_time) VALUES(20,30,9,1,1)")
	e.exec("INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id,tunnel_id,node_id,created_time) VALUES(30,8,11,1)")
	inIP := func() string {
		t.Helper()
		list, err := e.r.ListForwards()
		if err != nil || len(list) != 1 {
			t.Fatalf("list = %v (%v)", list, err)
		}
		return asString(list[0]["inIp"])
	}
	e.exec("UPDATE tunnel SET in_ip='2001:db8::11,192.0.2.12' WHERE id=8")
	if got := inIP(); got != "[2001:db8::11]:1509" {
		t.Fatalf("restricted user sees %q, want the allowed node's pick", got)
	}
	// No pick on the allowed node: the node's own address, as before.
	e.exec("UPDATE tunnel SET in_ip='192.0.2.12' WHERE id=8")
	if got := inIP(); got != "192.0.2.11:1509" {
		t.Fatalf("restricted fallback = %q", got)
	}
}

func TestTunnelCreateAndUpdateKeepPickedEntryIPs(t *testing.T) {
	e := newFlowTestEnv(t)
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	e.addNode(11, "entry")
	e.exec(`UPDATE node SET name='two-v6', server_ip='2001:db8::1', server_ip_v6='2001:db8::1', extra_ips='["2001:db8::2","2001:db8::3"]' WHERE id=11`)
	entries := []map[string]interface{}{{"nodeId": 11, "chainType": 1}}
	code, msg, _ := adminPost(t, e.h.tunnelCreate, map[string]interface{}{
		"name": "picked", "type": 1, "flow": 1, "status": 1, "inNodeId": entries, "inIp": "2001:db8::2",
	})
	if code != 0 {
		t.Fatalf("create: %d %s", code, msg)
	}
	var tunnel struct {
		ID   int64
		InIP string
	}
	e.r.DB().Raw(`SELECT id, in_ip FROM tunnel WHERE name = 'picked'`).Scan(&tunnel)
	if tunnel.InIP != "2001:db8::2" {
		t.Fatalf("created in_ip = %q", tunnel.InIP)
	}
	// The UI joins ticked addresses with commas; an unchanged entry set keeps them as sent.
	code, msg, _ = adminPost(t, e.h.tunnelUpdate, map[string]interface{}{
		"id": tunnel.ID, "name": "picked", "type": 1, "flow": 1, "status": 1, "inNodeId": entries, "inIp": "2001:db8::2,2001:db8::3",
	})
	if code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	e.r.DB().Raw(`SELECT in_ip FROM tunnel WHERE id = ?`, tunnel.ID).Scan(&tunnel.InIP)
	if tunnel.InIP != "2001:db8::2,2001:db8::3" {
		t.Fatalf("updated in_ip = %q", tunnel.InIP)
	}
	// Nothing ticked: the backend auto list (primary IPs) as before.
	code, msg, _ = adminPost(t, e.h.tunnelUpdate, map[string]interface{}{
		"id": tunnel.ID, "name": "picked", "type": 1, "flow": 1, "status": 1, "inNodeId": entries, "inIp": "",
	})
	if code != 0 {
		t.Fatalf("clear: %d %s", code, msg)
	}
	e.r.DB().Raw(`SELECT in_ip FROM tunnel WHERE id = ?`, tunnel.ID).Scan(&tunnel.InIP)
	if tunnel.InIP != "2001:db8::1" {
		t.Fatalf("auto in_ip = %q", tunnel.InIP)
	}
}
