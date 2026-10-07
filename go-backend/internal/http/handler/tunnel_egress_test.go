package handler

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestSelectTunnelDialHostEgress(t *testing.T) {
	dual := nodeRecord{ID: 2, ServerIPv4: "192.0.2.2", ServerIPv6: "2001:db8::2"}
	v6 := nodeRecord{ID: 1, ServerIPv6: "2001:db8::1"}
	v4 := nodeRecord{ID: 1, ServerIPv4: "192.0.2.1"}
	lan := dual
	lan.IntranetIP = "10.0.0.2"
	for _, tc := range []struct {
		name                                  string
		from, to                              nodeRecord
		egress, preference, hop, host, family string
		fail                                  bool
	}{
		{"v6 inbound dual egress explicit v4", v6, dual, "dual", "", "v4", "192.0.2.2", "v4", false},
		{"v6 inbound auto egress explicit v4", v6, dual, "", "", "v4", "192.0.2.2", "v4", false},
		{"explicit overrides declared v6 egress", v6, dual, "v6", "v6", "v4", "192.0.2.2", "v4", false},
		{"explicit v6 overrides v4 egress", v4, dual, "v4", "v4", "v6", "2001:db8::2", "v6", false},
		{"auto preserves legacy v6", v6, dual, "", "", "", "2001:db8::2", "v6", false},
		{"auto dual egress chooses v4", v6, dual, "dual", "auto", "", "192.0.2.2", "v4", false},
		{"v4 preference respects auto egress", v6, dual, "", "v4", "", "2001:db8::2", "v6", false},
		{"v4 preference uses dual egress", v6, dual, "dual", "v4", "", "192.0.2.2", "v4", false},
		{"v6 preference uses dual egress", v4, dual, "dual", "v6", "", "2001:db8::2", "v6", false},
		{"v6 preference respects v4 egress", v6, dual, "v4", "v6", "", "192.0.2.2", "v4", false},
		{"v4 preference respects v6 egress", v4, dual, "v6", "v4", "", "2001:db8::2", "v6", false},
		{"missing target v4 falls back", v6, v6, "", "v4", "v4", "2001:db8::1", "v6", false},
		{"missing target v6 falls back", v4, v4, "", "v6", "v6", "192.0.2.1", "v4", false},
		{"missing family uses default lan", v6, nodeRecord{ServerIPv6: "2001:db8::2", IntranetIP: "10.0.0.2"}, "", "", "v4", "10.0.0.2", "lan", false},
		{"fallback still requires compatible auto egress", v4, v6, "", "auto", "v4", "", "", true},
		{"lan independent of egress", v6, lan, "v6", "v6", "lan", "10.0.0.2", "lan", false},
		{"missing lan falls back", v6, dual, "dual", "v6", "lan", "192.0.2.2", "v4", false},
		{"default retains lan priority", v6, lan, "dual", "", "", "10.0.0.2", "lan", false},
		{"auto preference skips lan", v6, lan, "dual", "auto", "", "192.0.2.2", "v4", false},
		{"legacy v4", nodeRecord{ServerIP: "192.0.2.1"}, dual, "", "", "", "192.0.2.2", "v4", false},
		{"legacy v6", nodeRecord{ServerIP: "[2001:db8::1]"}, dual, "", "", "", "2001:db8::2", "v6", false},
		{"legacy hostname remains dual", nodeRecord{ServerIP: "example.test"}, dual, "", "v6", "", "2001:db8::2", "v6", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.from.EgressIPFamily = tc.egress
			host, family, err := selectTunnelDialHost(&tc.from, &tc.to, tc.preference, tc.hop)
			if (err != nil) != tc.fail || host != tc.host || family != tc.family {
				t.Fatalf("got (%q,%q,%v), want (%q,%q,fail=%t)", host, family, err, tc.host, tc.family, tc.fail)
			}
		})
	}
}

func TestTunnelSavePreservesAutomaticConnectIPType(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			e, _ := newTunnelRedeployEnv(t)
			body := map[string]interface{}{
				"name": "egress-save", "type": 2, "status": 1, "ipPreference": "v4",
				"inNodeId":   []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
				"chainNodes": [][]map[string]interface{}{{{"nodeId": 2, "port": 1302, "protocol": "tls", "connectIpType": ""}}},
				"outNodeId":  []map[string]interface{}{{"nodeId": 3, "port": 1303, "protocol": "tls", "connectIpType": "v4"}},
			}
			fn := e.h.tunnelCreate
			if operation == "update" {
				body["id"] = 10
				fn = e.h.tunnelUpdate
			}
			if code, msg := redeployMutationRequest(t, fn, body); code != 0 {
				t.Fatalf("save: %d %s", code, msg)
			}
			var tunnel model.Tunnel
			if err := e.r.DB().Where("name = ?", "egress-save").First(&tunnel).Error; err != nil {
				t.Fatal(err)
			}
			rows, err := e.r.ListChainNodesForTunnel(tunnel.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				want := ""
				if row.ChainType == 3 {
					want = "v4"
				}
				if row.ConnectIPType != want {
					t.Fatalf("node %d stored type %q, want %q", row.NodeID, row.ConnectIPType, want)
				}
			}
			items, err := e.r.ListTunnels()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(items)
			if !strings.Contains(string(data), `"connectIpType":""`) {
				t.Fatalf("list lost automatic choice: %s", data)
			}
		})
	}
}

func TestNodeEgressAPIPersistence(t *testing.T) {
	e := newFlowTestEnv(t)
	body := map[string]interface{}{"name": "egress-node", "serverIpV6": "2001:db8::1", "egressIpFamily": "dual"}
	if code, msg := redeployMutationRequest(t, e.h.nodeCreate, body); code != 0 {
		t.Fatalf("create: %s", msg)
	}
	var node model.Node
	if err := e.r.DB().Where("name = ?", "egress-node").First(&node).Error; err != nil {
		t.Fatal(err)
	}
	body["id"] = node.ID
	for _, family := range []string{"v4", "v6", "dual", ""} {
		body["egressIpFamily"] = family
		if code, msg := redeployMutationRequest(t, e.h.nodeUpdate, body); code != 0 {
			t.Fatalf("update: %s", msg)
		}
		rec, err := e.r.GetNodeRecord(node.ID)
		if err != nil || rec.EgressIPFamily != family {
			t.Fatalf("record: %+v %v", rec, err)
		}
		items, err := e.r.ListNodes(nil)
		if err != nil || len(items) != 1 || items[0]["egressIpFamily"] != family {
			t.Fatalf("list: %+v %v", items, err)
		}
	}
	body["egressIpFamily"] = "invalid"
	if code, _ := redeployMutationRequest(t, e.h.nodeUpdate, body); code == 0 {
		t.Fatal("invalid family accepted")
	}
	e.exec("UPDATE node SET egress_ip_family = 'dual' WHERE id = ?", node.ID)
	delete(body, "egressIpFamily")
	if code, msg := redeployMutationRequest(t, e.h.nodeUpdate, body); code != 0 {
		t.Fatalf("legacy update: %s", msg)
	}
	rec, err := e.r.GetNodeRecord(node.ID)
	if err != nil || rec.EgressIPFamily != "dual" {
		t.Fatalf("legacy update erased egress: %+v %v", rec, err)
	}
	e.addNode(99, "legacy")
	var legacy model.Node
	if err := e.r.DB().First(&legacy, 99).Error; err != nil || legacy.EgressIPFamily != "" {
		t.Fatalf("default: %+v %v", legacy, err)
	}
	var col struct {
		Notnull   int
		DfltValue sql.NullString
	}
	if err := e.r.DB().Raw("SELECT [notnull], dflt_value FROM pragma_table_info('node') WHERE name='egress_ip_family'").Scan(&col).Error; err != nil || col.Notnull != 1 || (col.DfltValue.String != "''" && col.DfltValue.String != `""`) {
		t.Fatalf("column: %+v %v", col, err)
	}
}

func TestBestExitUsesConnectIPType(t *testing.T) {
	from := &nodeRecord{ID: 1, ServerIPv6: "2001:db8::1"}
	to := &nodeRecord{ID: 2, ServerIPv4: "192.0.2.2", ServerIPv6: "2001:db8::2"}
	rows := []chainNodeRecord{{NodeID: 2, Port: 1234, ConnectIP: "198.51.100.99", ConnectIPType: "v4"}}
	targets := chainRecordsToRuntimeTargets(rows)
	if targets[0].ConnectIPType != "v4" {
		t.Fatalf("type = %q", targets[0].ConnectIPType)
	}
	nodes := map[int64]*nodeRecord{1: from, 2: to}
	config, err := buildTunnelChainConfig(1, 1, targets, nodes, "v6")
	data, _ := json.Marshal(config)
	if err != nil || !strings.Contains(string(data), "198.51.100.99:1234") {
		t.Fatalf("chain: %s %v", data, err)
	}
	scores := evaluateBestExitOwner(chainNodeRecord{NodeID: 1}, rows, nodes, "v6", diagnosisExecOptions{}, func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
		if id == 1 && (ip != "198.51.100.99" || port != 1234) {
			t.Fatalf("best exit probed %s:%d", ip, port)
		}
		return 1, 0, nil
	})
	if len(scores) != 1 {
		t.Fatalf("scores = %+v", scores)
	}
	ip, _, err := resolveChainProbeTarget(from, to, 1234, "v6", "v4")
	if err != nil || ip != "192.0.2.2" {
		t.Fatalf("diagnosis probe: %q %v", ip, err)
	}
}
