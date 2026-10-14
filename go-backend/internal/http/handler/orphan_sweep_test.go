package handler

import (
	"encoding/json"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func TestOrphanClassification(t *testing.T) {
	s := &repo.RuntimeOwnership{
		Forwards: []model.Forward{{ID: 100, UserID: 7, TunnelID: 80}, {ID: 101, Mode: "wg_path", WGPathID: 3}},
		Ports:    []model.ForwardPort{{ForwardID: 100, NodeID: 52}},
		Tunnels:  []model.Tunnel{{ID: 80, Type: 2, Status: 1}},
		Chains:   []model.ChainTunnel{{TunnelID: 80, NodeID: 52, ChainType: "1"}, {TunnelID: 80, NodeID: 53, ChainType: "3"}},
		Paths:    []model.PathTunnel{{ID: 3}},
		Segments: []model.PathSegment{{PathID: 3, FromNodeID: 52, ToNodeID: 54}},
	}
	for _, tc := range []struct {
		node             int64
		kind, name, want string
	}{
		{52, "service", "100_7_999_tcp", "owned"}, // historical grant, paused owner
		{52, "service", "100_7_0", "owned"}, {53, "service", "100_7_0_udp", "orphan"},
		{52, "service", "999_7_0_tcp", "orphan"}, {52, "nft", "100_nft", "owned"}, {52, "nft", "999_nft", "orphan"},
		{52, "chain", "chains_80", "owned"}, {53, "service", "80_tls", "owned"},
		{54, "service", "80_tls", "orphan"}, {52, "chain", "chains_999", "orphan"},
		{52, "wgForward", "forward-101", "owned"}, {53, "wgForward", "forward-101", "orphan"},
		{52, "wgPath", "wg-flvx-3", "owned"}, {52, "wgPath", "wg-flvx-999", "orphan"},
		{52, "limiter", "forward_100_speed", "owned"}, {53, "limiter", "forward_100_speed", "orphan"},
		{53, "service", "flux_agent", "unknown"}, {53, "service", "mmw-agent", "unknown"}, {53, "wgPath", "wg0", "unknown"},
		{52, "service", "999_7_0_tcp_custom", "unknown"}, {52, "service", "999_tls_extra", "unknown"},
		{52, "service", "0999_7_0_tcp", "unknown"}, {52, "service", "999_7_-1_tcp", "unknown"},
		{52, "chain", "custom_999", "unknown"}, {52, "hop", "hop_999", "unknown"}, {52, "limiter", "42", "unknown"},
	} {
		t.Run(tc.kind+"/"+tc.name+"/"+tc.want, func(t *testing.T) {
			_, got := classifyRuntime(s, tc.node, runtimeObject{Kind: tc.kind, Name: tc.name})
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOrphanSweepSafetyAndSelection(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	addRedeployForwards(e, "gost")
	e.exec("UPDATE node SET status=0 WHERE id=3")
	e.exec("INSERT INTO peer_share(name,node_id,token,created_time,updated_time,is_active) VALUES('disabled share',4,'test',0,0,0)")
	e.exec("UPDATE node SET is_remote=1 WHERE id=5")
	e.h.nodeCommandSender = func(id int64, method string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
		if method == "ListServices" {
			if id == 3 || id == 4 || id == 5 {
				t.Fatalf("probed offline/shared node %d", id)
			}
			i := runtimeInventory{Services: []string{"100_7_70_tcp", "100_7_70_udp", "999_7_0_tcp", "998_7_0_udp", "custom"}}
			if id == 2 {
				i.Services = append(i.Services, "fed_svc_999")
			}
			raw, _ := json.Marshal(i)
			var data map[string]interface{}
			_ = json.Unmarshal(raw, &data)
			return ws.CommandResult{Success: true, Data: data}, nil
		}
		return commands.send(id, method, payload, timeout)
	}
	for _, execute := range []bool{false, true} {
		code, msg, data := leiKwanHandlerCall(t, e.h.nodeOrphanSweep, map[string]interface{}{
			"execute": execute, "targets": []map[string]interface{}{{"nodeId": 1, "kind": "service", "name": "999_7_0_tcp"}},
		})
		if code != 0 {
			t.Fatalf("sweep %d %s", code, msg)
		}
		raw, _ := json.Marshal(data["nodes"])
		var nodes []orphanNodeReport
		_ = json.Unmarshal(raw, &nodes)
		if len(nodes) != 5 || len(nodes[0].Orphans) != 2 || len(nodes[0].Unknown) != 1 || nodes[0].MissingCount != 1 {
			t.Fatalf("report: %s", raw)
		}
		for _, n := range nodes[1:] {
			if n.Skipped == "" {
				t.Fatalf("node %d not protected", n.NodeID)
			}
		}
		if !execute && len(commands.snapshot()) != 0 {
			t.Fatal("dry run mutated runtime")
		}
	}
	cmds := commands.snapshot()
	if len(cmds) != 1 || cmds[0].nodeID != 1 || cmds[0].method != "DeleteService" {
		t.Fatalf("unexpected cleanup: %v", cmds)
	}
	names := cmds[0].data.(map[string]interface{})["services"].([]string)
	if len(names) != 1 || names[0] != "999_7_0_tcp" {
		t.Fatalf("wrong target: %v", names)
	}
}

func TestOrphanSweepRechecksOwnership(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	e.h.nodeCommandSender = func(id int64, method string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
		if method == "ListServices" {
			// The rule is created after the scan's DB snapshot, before execution.
			e.addForward(999, 7, 10)
			e.exec("INSERT INTO forward_port(forward_id,node_id,port) VALUES(999,1,1500)")
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"services": []string{"999_7_0_tcp"}}}, nil
		}
		return commands.send(id, method, payload, timeout)
	}
	code, msg, _ := leiKwanHandlerCall(t, e.h.nodeOrphanSweep, map[string]interface{}{"execute": true, "nodeIds": []int64{1}})
	if code != 0 || len(commands.snapshot()) != 0 {
		t.Fatalf("deleted newly owned runtime: %d %s %v", code, msg, commands.snapshot())
	}
}
