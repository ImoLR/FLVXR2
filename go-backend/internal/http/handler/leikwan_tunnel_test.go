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
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

func newLeiKwanTunnelTest(t *testing.T) (*flowTestEnv, *testapi.Fake, *model.LeiKwanPipe, *redeployCommandLog) {
	t.Helper()
	e := newFlowTestEnv(t)
	fake := testapi.New()
	fake.SeedSix()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	e.h.leikwan = leikwan.NewClient(server.URL, server.Client().Transport)
	for id, ip := range map[int64]string{1: "9.9.9.1", 2: "9.9.9.2", 3: "8.8.8.8", 4: "1.1.1.1", 5: "8.8.4.4"} {
		e.addNode(id, fmt.Sprint(id))
		e.exec("UPDATE node SET server_ip=?,server_ip_v4=?,port='10000-30000',tcp_listen_addr='0.0.0.0',udp_listen_addr='0.0.0.0' WHERE id=?", ip, ip, id)
	}
	account := &model.LeiKwanAccount{Name: "local fake", ServiceID: 39, Token: fake.Token}
	if err := e.r.SaveLeiKwanAccount(account); err != nil {
		t.Fatal(err)
	}
	pipe := &model.LeiKwanPipe{AccountID: account.ID, ForwardID: 263, LandingNodeID: 3, LandingPort: 20015, ListenPort: 10015, TargetHost: "8.8.8.8", Transport: "tcp+udp", RouteID: 21, ETag: `"forward-263-r1"`}
	if err := e.r.SaveLeiKwanPipe(pipe); err != nil {
		t.Fatal(err)
	}
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	return e, fake, pipe, commands
}
func leiKwanTunnelRequest(pipeID int64) map[string]interface{} {
	return map[string]interface{}{"name": "pipe tunnel", "type": 2, "status": 1, "leikwanPipeId": pipeID, "inNodeId": []map[string]interface{}{{"nodeId": 1}, {"nodeId": 2}}, "chainNodes": []interface{}{[]map[string]interface{}{{"nodeId": 3, "port": 20015, "protocol": "tls"}}}, "outNodeId": []map[string]interface{}{{"nodeId": 4, "port": 21000, "protocol": "tls"}}}
}
func leiKwanHandlerCall(t *testing.T, fn http.HandlerFunc, body interface{}) (int, string, map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	var result struct {
		Code int                    `json:"code"`
		Msg  string                 `json:"msg"`
		Data map[string]interface{} `json:"data"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return result.Code, result.Msg, result.Data
}
func leiKwanCreateTunnel(t *testing.T, e *flowTestEnv, p *model.LeiKwanPipe, body map[string]interface{}) int64 {
	t.Helper()
	code, msg, _ := leiKwanHandlerCall(t, e.h.tunnelCreate, body)
	if code != 0 {
		t.Fatalf("create: %d %s", code, msg)
	}
	pipe, err := e.r.GetLeiKwanPipe(p.ID)
	if err != nil || pipe.TunnelID == nil {
		t.Fatalf("pipe link: %+v %v", pipe, err)
	}
	return *pipe.TunnelID
}
func chainAddress(data interface{}) string {
	root := data.(map[string]interface{})
	hops := root["hops"].([]map[string]interface{})
	nodes := hops[0]["nodes"].([]map[string]interface{})
	return nodes[0]["addr"].(string)
}

func TestLeiKwanTunnelMultiEntryPersistenceRedeployAndDiagnosis(t *testing.T) {
	e, fake, pipe, commands := newLeiKwanTunnelTest(t)
	e.exec("UPDATE node SET interface_name='eth0' WHERE id IN (1,2)")
	id := leiKwanCreateTunnel(t, e, pipe, leiKwanTunnelRequest(pipe.ID))
	for _, command := range commands.snapshot() {
		if command.method == "AddChains" && (command.nodeID == 1 || command.nodeID == 2) {
			if got := chainAddress(command.data); got != "127.0.0.1:10015" {
				t.Fatalf("entry %d dial=%s", command.nodeID, got)
			}
			hop := command.data.(map[string]interface{})["hops"].([]map[string]interface{})[0]
			if _, ok := hop["interface"]; ok {
				t.Fatal("loopback must not bind entry's egress interface")
			}
		}
	}
	rows, err := e.r.ListChainNodesForTunnel(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.NodeID == 3 && (row.Port != 20015 || row.ConnectIP != "") {
			t.Fatalf("landing row changed to loopback: %+v", row)
		}
	}
	_, beforeWrites := fake.Counts()
	// A fresh Handler represents a panel restart: no in-memory pipe state survives.
	e.h = &Handler{repo: e.r, nodeCommandSender: commands.send, leikwan: e.h.leikwan}
	for _, entry := range []int64{1, 2} {
		if err := e.h.redeployTunnelRuntimeOnNode(id, entry); err != nil {
			t.Fatalf("reconnect: %v", err)
		}
	}
	if err := e.h.redeployTunnelAndForwards(id); err != nil {
		t.Fatalf("batch redeploy: %v", err)
	}
	for _, command := range commands.snapshot() {
		if command.method == "AddChains" && command.nodeID <= 2 && chainAddress(command.data) != "127.0.0.1:10015" {
			t.Fatal("restart lost override")
		}
	}
	_, afterWrites := fake.Counts()
	if beforeWrites != afterWrites {
		t.Fatal("redeploy called LeiKwan API")
	}
	_, _, work, err := e.h.prepareTunnelDiagnosis(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range work {
		if item.fromNodeID <= 2 && (item.pipeTunnelID != id || item.targetIP != "127.0.0.1" || item.targetPort != 10015 || item.hasChainHop) {
			t.Fatalf("wrong diagnosis target: %+v", item)
		}
	}
	plans, pairs, _, err := e.h.planPathLatencyRound()
	if err != nil || len(plans) != 1 {
		t.Fatalf("latency plans=%v err=%v", plans, err)
	}
	for key := range pairs {
		if key.From <= 2 && (key.Host != "127.0.0.1" || key.Port != 10015 || key.PipeTunnelID != id) {
			t.Fatalf("latency target=%+v", key)
		}
	}
	e.h.nodeCommandSender = func(nodeID int64, method string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
		if method != "PipePing" {
			t.Fatalf("pipe latency used %s", method)
		}
		if payload.(map[string]interface{})["tunnelId"] != id {
			t.Fatal("probe wrong chain")
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "averageTime": 12.5}}, nil
	}
	if got := e.h.probePathPair(pathProbeKey{From: 1, Host: "127.0.0.1", Port: 10015, PipeTunnelID: id}, &nodeRecord{ID: 1}); !got.OK || got.LatencyMS != 12.5 {
		t.Fatalf("probe=%+v", got)
	}
}

func TestLeiKwanTunnelEditRetargetAndConflict(t *testing.T) {
	e, fake, pipe, commands := newLeiKwanTunnelTest(t)
	req := leiKwanTunnelRequest(pipe.ID)
	id := leiKwanCreateTunnel(t, e, pipe, req)
	req["id"] = id
	delete(req, "leikwanPipeId") // Ordinary tunnel editor need not resend the link.
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 3, "port": 20016, "protocol": "tls"}}}
	req["inNodeId"] = []map[string]interface{}{{"nodeId": 2}}
	code, msg, _ := leiKwanHandlerCall(t, e.h.tunnelUpdate, req)
	if code != 0 {
		t.Fatalf("edit: %d %s", code, msg)
	}
	fake.Mu.Lock()
	target := fake.Forwards[263].Target
	fake.Mu.Unlock()
	saved, _ := e.r.GetLeiKwanPipe(pipe.ID)
	if target.Port != 20016 || saved.LandingPort != 20016 || saved.TunnelID == nil || *saved.TunnelID != id {
		t.Fatalf("target=%+v pipe=%+v", target, saved)
	}
	state, err := e.h.reconstructTunnelState(id)
	if err != nil || len(state.InNodes) != 1 || state.InNodes[0].NodeID != 2 {
		t.Fatalf("entry sync state=%+v err=%v", state, err)
	}
	// Moving the first landing through the ordinary editor updates the pipe.
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 5, "port": 20018, "protocol": "tls"}}}
	code, msg, _ = leiKwanHandlerCall(t, e.h.tunnelUpdate, req)
	if code != 0 {
		t.Fatalf("change landing: %d %s", code, msg)
	}
	fake.Mu.Lock()
	target = fake.Forwards[263].Target
	fake.Mu.Unlock()
	saved, _ = e.r.GetLeiKwanPipe(pipe.ID)
	if target.Host != "8.8.4.4" || target.Port != 20018 || saved.LandingNodeID != 5 {
		t.Fatalf("landing target=%+v pipe=%+v", target, saved)
	}
	// An external edit is not overwritten; old deploy remains intact.
	fake.Mu.Lock()
	f := fake.Forwards[263]
	f.Target.Host = "9.9.9.9"
	f.Revision++
	fake.Forwards[263] = f
	fake.Mu.Unlock()
	before := len(commands.snapshot())
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 5, "port": 20019, "protocol": "tls"}}}
	code, msg, _ = leiKwanHandlerCall(t, e.h.tunnelUpdate, req)
	if code == 0 || !strings.Contains(msg, "412") {
		t.Fatalf("external conflict accepted: %d %s", code, msg)
	}
	if len(commands.snapshot()) != before {
		t.Fatal("conflicting edit touched existing node runtime")
	}
	saved, _ = e.r.GetLeiKwanPipe(pipe.ID)
	if saved.LandingPort != 20018 {
		t.Fatal("conflict changed pipe record")
	}
}

func TestLeiKwanTunnelChangeRollsBackRemoteOnLocalFailure(t *testing.T) {
	e, fake, pipe, _ := newLeiKwanTunnelTest(t)
	req := leiKwanTunnelRequest(pipe.ID)
	id := leiKwanCreateTunnel(t, e, pipe, req)
	req["id"] = id
	e.exec(`CREATE TRIGGER fail_pipe_update BEFORE UPDATE ON leikwan_pipe WHEN NEW.landing_port = 20016 BEGIN SELECT RAISE(ABORT, 'injected local failure'); END`)
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 3, "port": 20016, "protocol": "tls"}}}
	code, _, _ := leiKwanHandlerCall(t, e.h.tunnelUpdate, req)
	if code == 0 {
		t.Fatal("local failure unexpectedly succeeded")
	}
	fake.Mu.Lock()
	target := fake.Forwards[263].Target
	fake.Mu.Unlock()
	saved, _ := e.r.GetLeiKwanPipe(pipe.ID)
	if target.Port != 20015 || saved.LandingPort != 20015 {
		t.Fatalf("compensation target=%+v pipe=%+v", target, saved)
	}
	state, err := e.h.reconstructTunnelState(id)
	if err != nil || state.ChainHops[0][0].Port != 20015 {
		t.Fatalf("old tunnel not restored: %+v %v", state, err)
	}
}

func TestLeiKwanTunnelOnePipePerTunnelAndTopologyValidation(t *testing.T) {
	e, _, pipe, _ := newLeiKwanTunnelTest(t)
	req := leiKwanTunnelRequest(pipe.ID)
	leiKwanCreateTunnel(t, e, pipe, req)
	req["name"] = "second"
	if code, _, _ := leiKwanHandlerCall(t, e.h.tunnelCreate, req); code == 0 {
		t.Fatal("pipe attached twice")
	}
	e2, _, p2, _ := newLeiKwanTunnelTest(t)
	req = leiKwanTunnelRequest(p2.ID)
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 3, "port": 20015}, {"nodeId": 5, "port": 20020}}}
	if code, _, _ := leiKwanHandlerCall(t, e2.h.tunnelCreate, req); code == 0 {
		t.Fatal("pipe allowed several first landings")
	}
}

func TestLeiKwanTunnelDeleteKeepsForwardUnlessExplicit(t *testing.T) {
	for _, variant := range []string{"single", "batch", "with-forwards", "batch-with-forwards"} {
		for _, remote := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/remote=%v", variant, remote), func(t *testing.T) {
				e, fake, pipe, _ := newLeiKwanTunnelTest(t)
				id := leiKwanCreateTunnel(t, e, pipe, leiKwanTunnelRequest(pipe.ID))
				_, beforeWrites := fake.Counts()
				payload := map[string]interface{}{"id": id, "ids": []int64{id}, "deleteLeikwanForward": remote, "action": "delete_forwards"}
				fn := e.h.tunnelDelete
				switch variant {
				case "batch":
					fn = e.h.tunnelBatchDelete
				case "with-forwards":
					fn = e.h.tunnelDeleteWithForwards
				case "batch-with-forwards":
					fn = e.h.tunnelBatchDeleteWithForwards
				}
				if strings.HasPrefix(variant, "batch") {
					delete(payload, "id")
				} else {
					delete(payload, "ids")
				}
				if !strings.Contains(variant, "with-forwards") {
					delete(payload, "action")
				}
				code, msg, _ := leiKwanHandlerCall(t, fn, payload)
				if code != 0 {
					t.Fatalf("delete: %d %s", code, msg)
				}
				p, err := e.r.GetLeiKwanPipe(pipe.ID)
				if err != nil {
					t.Fatal(err)
				}
				if remote {
					if p != nil {
						t.Fatal("explicit delete retained pipe")
					}
				} else if p == nil || p.TunnelID != nil {
					t.Fatalf("default delete pipe=%+v", p)
				}
				fake.Mu.Lock()
				_, exists := fake.Forwards[263]
				stored := fake.Stored
				fake.Mu.Unlock()
				if exists == remote || stored != 6 {
					t.Fatalf("exists=%v stored=%d", exists, stored)
				}
				_, writes := fake.Counts()
				if !remote && writes != beforeWrites {
					t.Fatal("default delete wrote LeiKwan")
				}
			})
		}
	}
}

func TestLeiKwanPipeProgressOnlyProbesAgents(t *testing.T) {
	e, fake, pipe, _ := newLeiKwanTunnelTest(t)
	leiKwanCreateTunnel(t, e, pipe, leiKwanTunnelRequest(pipe.ID))
	requests, writes := fake.Counts()
	calls := 0
	e.h.nodeCommandSender = func(nodeID int64, method string, payload interface{}, timeout time.Duration) (ws.CommandResult, error) {
		calls++
		if method != "TcpPing" && method != "PipePing" {
			t.Fatalf("unexpected command %s", method)
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "averageTime": 15.0}}, nil
	}
	code, msg, data := leiKwanHandlerCall(t, e.h.leikwanPipeCheck, map[string]interface{}{"pipeId": pipe.ID})
	if code != 0 || data["endToEnd"].(map[string]interface{})["success"] != true || calls != 4 {
		t.Fatalf("progress: %d %s %+v calls=%d", code, msg, data, calls)
	}
	gotRequests, gotWrites := fake.Counts()
	if gotRequests != requests || gotWrites != writes {
		t.Fatal("browser poll called LeiKwan API")
	}
}

func TestLeiKwanTunnelAmbiguousPatchCompensated(t *testing.T) {
	e, fake, pipe, commands := newLeiKwanTunnelTest(t)
	req := leiKwanTunnelRequest(pipe.ID)
	id := leiKwanCreateTunnel(t, e, pipe, req)
	req["id"] = id
	fake.Mu.Lock()
	fake.FailPatchAfterSave = true
	fake.Mu.Unlock()
	before := len(commands.snapshot())
	req["chainNodes"] = []interface{}{[]map[string]interface{}{{"nodeId": 3, "port": 20016, "protocol": "tls"}}}
	code, _, _ := leiKwanHandlerCall(t, e.h.tunnelUpdate, req)
	if code == 0 {
		t.Fatal("ambiguous patch unexpectedly accepted")
	}
	fake.Mu.Lock()
	target := fake.Forwards[263].Target
	fake.Mu.Unlock()
	saved, _ := e.r.GetLeiKwanPipe(pipe.ID)
	if target.Port != 20015 || saved.LandingPort != 20015 {
		t.Fatalf("ambiguous patch not restored: target=%+v pipe=%+v", target, saved)
	}
	if len(commands.snapshot()) != before {
		t.Fatal("failed edit changed deployed runtime")
	}
}
