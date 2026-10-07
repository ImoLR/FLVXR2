package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestDetectedEgressIsAdditive(t *testing.T) {
	for _, tc := range []struct{ name, inbound4, detected, manual, preference, want string }{
		{"v6 inbound detects dual", "", "dual", "", "auto", "192.0.2.2"},
		{"manual v6 wins", "", "dual", "v6", "auto", "2001:db8::2"},
		{"detected v4 keeps inbound v6", "192.0.2.1", "v4", "", "v6", "2001:db8::2"},
		{"no detection preserves fork26", "", "", "", "auto", "2001:db8::2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from := &nodeRecord{ServerIPv4: tc.inbound4, ServerIPv6: "2001:db8::1", EgressDetected: tc.detected, EgressIPFamily: tc.manual}
			to := &nodeRecord{ServerIPv4: "192.0.2.2", ServerIPv6: "2001:db8::2"}
			host, _, err := selectTunnelDialHost(from, to, tc.preference, "")
			if err != nil || host != tc.want {
				t.Fatalf("got %q %v, want %q", host, err, tc.want)
			}
		})
	}
}

func TestEgressDetectedPersistence(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, "detected")
	rec, err := e.r.GetNodeRecord(1)
	if err != nil || rec.EgressDetected != "" || rec.EgressDetectedAt != 0 {
		t.Fatalf("defaults: %+v %v", rec, err)
	}
	for i, family := range []string{"dual", "dual", "", "invalid", "v4"} {
		if err := e.r.UpdateNodeEgressDetected(1, family, int64(100+i)); err != nil {
			t.Fatal(err)
		}
		want, at := "dual", int64(100)
		if i == 4 {
			want, at = "v4", 104
		}
		rec, err := e.r.GetNodeRecord(1)
		if err != nil || rec.EgressDetected != want || rec.EgressDetectedAt != at {
			t.Fatalf("report %d: %+v %v", i, rec, err)
		}
	}
	items, err := e.r.ListNodes(nil)
	if err != nil || items[0]["egressDetected"] != "v4" || items[0]["egressDetectedAt"] != int64(104) {
		t.Fatalf("list: %+v %v", items, err)
	}
	backup, err := e.r.ExportPartial([]string{"nodes"})
	if err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE node SET egress_detected='',egress_detected_at=0 WHERE id=1")
	if _, err := e.r.Import(backup, []string{"nodes"}); err != nil {
		t.Fatal(err)
	}
	rec, err = e.r.GetNodeRecord(1)
	if err != nil || rec.EgressDetected != "v4" || rec.EgressDetectedAt != 104 {
		t.Fatalf("backup roundtrip: %+v %v", rec, err)
	}
}

func TestCustomConnectIPOverridesType(t *testing.T) {
	nodes := map[int64]*nodeRecord{1: {ID: 1, ServerIPv6: "2001:db8::1"}, 2: {ID: 2, ServerIPv4: "192.0.2.2", ServerIPv6: "2001:db8::2"}}
	for _, ip := range []string{"198.51.100.22", "2001:db8:99::22"} {
		targets := []tunnelRuntimeNode{{NodeID: 2, Port: 1302, Protocol: "tls", ConnectIPType: "lan", ConnectIP: ip}}
		cfg, err := buildTunnelChainConfig(10, 1, targets, nodes, "v6")
		data, _ := json.Marshal(cfg)
		if err != nil || !strings.Contains(string(data), ip) {
			t.Fatalf("config: %s %v", data, err)
		}
		host, port, err := resolveChainProbeTarget(nodes[1], nodes[2], 1302, "v6", "v4", ip)
		if err != nil || host != ip || port != 1302 {
			t.Fatalf("probe: %s %d %v", host, port, err)
		}
		host, port, err = resolveBestExitProbeTarget(nodes[1], nodes[2], 1302, "v6", "v4", ip)
		if err != nil || host != ip || port != 1302 {
			t.Fatalf("best exit: %s %d %v", host, port, err)
		}
	}
}

func TestCustomConnectIPValidation(t *testing.T) {
	validate := func(req map[string]interface{}) error {
		data, _ := json.Marshal(req)
		var decoded map[string]interface{}
		json.Unmarshal(data, &decoded)
		return validateTunnelConnectIPConstraints(decoded)
	}

	for _, ip := range []string{"192.0.2.2", "2001:db8::2", "", " 192.0.2.2 "} {
		if err := validate(map[string]interface{}{"outNodeId": []map[string]interface{}{{"nodeId": 2, "connectIp": ip}}}); err != nil {
			t.Fatalf("valid %q: %v", ip, err)
		}
	}
	for _, ip := range []string{"example.com", "192.0.2.2:443", "[2001:db8::2]", "fe80::1%eth0", "300.1.1.1"} {
		for _, key := range []string{"outNodeId", "chainNodes"} {
			var nodes interface{} = []map[string]interface{}{{"nodeId": 2, "connectIp": ip}}
			if key == "chainNodes" {
				nodes = []interface{}{nodes}
			}
			if err := validate(map[string]interface{}{key: nodes}); err == nil || !strings.Contains(err.Error(), "IPv4 或 IPv6") {
				t.Fatalf("invalid %q accepted: %v", ip, err)
			}
		}
	}
	for _, key := range []string{"outNodeId", "chainNodes"} {
		var nodes interface{} = []map[string]interface{}{{"nodeId": 2, "connectIp": "192.0.2.2"}, {"nodeId": 3}}
		if key == "chainNodes" {
			nodes = []interface{}{nodes}
		}
		if err := validate(map[string]interface{}{key: nodes}); err == nil {
			t.Fatalf("multiple %s accepted", key)
		}
	}
}

func TestCustomConnectIPSaveUpdateClear(t *testing.T) {
	e, _ := newTunnelRedeployEnv(t)
	hop := map[string]interface{}{"nodeId": 2, "port": 1302, "protocol": "tls", "connectIpType": "v6", "connectIp": "198.51.100.22"}
	exit := map[string]interface{}{"nodeId": 3, "port": 1303, "protocol": "tls", "connectIp": "2001:db8:99::33"}
	body := map[string]interface{}{"name": "custom-save", "type": 2, "status": 1, "inNodeId": []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}}, "chainNodes": [][]map[string]interface{}{{hop}}, "outNodeId": []map[string]interface{}{exit}}
	for step := 0; step < 3; step++ {
		fn := e.h.tunnelCreate
		if step > 0 {
			fn = e.h.tunnelUpdate
		}
		if step == 1 {
			hop["connectIp"] = "2001:db8:99::22"
			exit["connectIp"] = "198.51.100.33"
		}
		if step == 2 {
			hop["connectIp"] = ""
			exit["connectIp"] = ""
		}
		if code, msg := redeployMutationRequest(t, fn, body); code != 0 {
			t.Fatalf("step %d: %d %s", step, code, msg)
		}
		var tunnel model.Tunnel
		if err := e.r.DB().Where("name = ?", "custom-save").First(&tunnel).Error; err != nil {
			t.Fatal(err)
		}
		body["id"] = tunnel.ID
		state, err := e.h.reconstructTunnelState(tunnel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.ChainHops[0][0].ConnectIP != hop["connectIp"] || state.OutNodes[0].ConnectIP != exit["connectIp"] || state.ChainHops[0][0].ConnectIPType != "v6" {
			t.Fatalf("roundtrip step %d: %+v %+v", step, state.ChainHops, state.OutNodes)
		}
	}
}
