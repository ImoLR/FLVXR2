package handler

import (
	"errors"
	"fmt"
	"testing"
)

func TestForwardCreatePartialDispatchRollback(t *testing.T) {
	for _, mode := range []string{"gost", "nftables"} {
		t.Run(mode, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			e.addUser(7)
			e.addUserTunnel(70, 7, 10)
			e.exec("UPDATE tunnel SET type=1 WHERE id=10")
			e.exec("DELETE FROM chain_tunnel WHERE tunnel_id=10 AND chain_type!='1'")
			e.exec("INSERT INTO chain_tunnel(tunnel_id,chain_type,node_id) VALUES(10,'1',2),(10,'1',5)")
			commands.fail = func(nodeID int64, method string) error {
				if nodeID == 2 && (method == "UpdateService" || method == "AddService" || method == "AddNftablesRules") {
					return errors.New("injected dispatch failure")
				}
				return nil
			}
			code, msg, _ := leiKwanHandlerCall(t, e.h.forwardCreate, map[string]interface{}{"userId": 7, "tunnelId": 10, "name": "failed-create", "remoteAddr": "1.1.1.1:443", "inPort": 1500, "mode": mode})
			if code == 0 {
				t.Fatalf("expected failure: %s", msg)
			}
			method := "DeleteService"
			if mode == "nftables" {
				method = "DeleteNftablesRules"
			}
			if commands.count(1, method) == 0 || commands.count(2, method) == 0 {
				t.Fatalf("missing cleanup of successful/possibly partial nodes: %v; %s", commands.snapshot(), msg)
			}
			var count int64
			e.r.DB().Table("forward").Count(&count)
			if count != 0 {
				t.Fatalf("DB create not rolled back: %d", count)
			}
		})
	}
}

func TestForwardUpdateAndBatchPartialDispatchRollback(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			addRedeployForwards(e, "gost")
			e.addTunnel(11, 1, 1)
			e.addUserTunnel(71, 7, 11)
			e.exec("INSERT INTO chain_tunnel(tunnel_id,chain_type,node_id) VALUES(11,'1',1),(11,'1',2),(11,'1',3)")
			commands.fail = func(nodeID int64, method string) error {
				if nodeID == 2 && (method == "UpdateService" || method == "AddService") {
					return errors.New("injected dispatch failure")
				}
				return nil
			}
			fn := e.h.forwardUpdate
			body := map[string]interface{}{"id": 100, "tunnelId": 11, "inPort": 1500}
			if batch {
				fn = e.h.forwardBatchChangeTunnel
				body = map[string]interface{}{"forwardIds": []int64{100}, "targetTunnelId": 11}
			}
			code, msg, data := leiKwanHandlerCall(t, fn, body)
			if (!batch && code == 0) || (batch && asInt(data["failCount"], 0) != 1) {
				t.Fatalf("expected failure: %d %s %v", code, msg, data)
			}
			deletedNew := false
			for _, c := range commands.snapshot() {
				if c.nodeID != 1 || c.method != "DeleteService" {
					continue
				}
				for _, name := range c.data.(map[string]interface{})["services"].([]string) {
					if name == "100_7_71_tcp" {
						deletedNew = true
					}
				}
			}
			if !deletedNew {
				t.Fatalf("new runtime on node 1 survived: %v; %s", commands.snapshot(), msg)
			}
			f, _ := e.h.getForwardRecord(100)
			if f.TunnelID != 10 || commands.count(5, "UpdateService") == 0 {
				t.Fatalf("old DB/runtime not restored: %+v %v", f, commands.snapshot())
			}
		})
	}
}
