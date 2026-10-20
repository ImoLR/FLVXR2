package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/cnlanding"
	"go-backend/internal/http/middleware"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// chainAgent fakes every agent of the test panel: landing applies answer like
// the real agent (counters for each desired node, egress applied), TcpPing
// succeeds unless told otherwise, forwarding commands succeed.
type chainAgent struct {
	mu        sync.Mutex
	desired   map[int64][]map[string]interface{} // node -> last desired nodes
	applies   map[int64]int
	commands  []redeployCommand
	egress    func(nodeID int64) map[string]interface{}
	applyErr  func(nodeID int64) error
	pingFail  map[int64]string
	forwardEr func(nodeID int64, method string) error
}

func newChainAgent() *chainAgent {
	return &chainAgent{desired: map[int64][]map[string]interface{}{}, applies: map[int64]int{}, pingFail: map[int64]string{}}
}

func (a *chainAgent) send(nodeID int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
	a.mu.Lock()
	a.commands = append(a.commands, redeployCommand{nodeID: nodeID, method: method, data: data})
	applyErr, egress, forwardErr := a.applyErr, a.egress, a.forwardEr
	pingFail, hasFail := a.pingFail[nodeID]
	a.mu.Unlock()
	switch method {
	case "ApplyLandingDesired":
		if applyErr != nil {
			if err := applyErr(nodeID); err != nil {
				return ws.CommandResult{}, err
			}
		}
		payload := data.(map[string]interface{})
		nodes := payload["nodes"].([]map[string]interface{})
		a.mu.Lock()
		a.desired[nodeID] = nodes
		a.applies[nodeID]++
		a.mu.Unlock()
		counters := []map[string]interface{}{}
		for _, n := range nodes {
			counters = append(counters, map[string]interface{}{"id": n["id"], "inbound_tag": n["inbound_tag"], "epoch": "100"})
		}
		eg := map[string]interface{}{"state": "applied", "ready": true}
		if egress != nil {
			eg = egress(nodeID)
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": "100", "state": "running", "nodes": counters, "egress": eg}}, nil
	case "TcpPing":
		if hasFail {
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": false, "errorMessage": pingFail}}, nil
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "averageTime": 12.5, "packetLoss": 0}}, nil
	}
	if forwardErr != nil {
		if err := forwardErr(nodeID, method); err != nil {
			return ws.CommandResult{}, err
		}
	}
	return ws.CommandResult{Success: true}, nil
}

// chainNodes returns the chain copies the agent of nodeID was last told to run.
func (a *chainAgent) setApplyErr(fn func(int64) error) {
	a.mu.Lock()
	a.applyErr = fn
	a.mu.Unlock()
}

func (a *chainAgent) chainNodes(nodeID int64) []map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []map[string]interface{}{}
	for _, n := range a.desired[nodeID] {
		if _, ok := n["chain"]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (a *chainAgent) count(nodeID int64, method string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.commands {
		if c.nodeID == nodeID && c.method == method {
			n++
		}
	}
	return n
}

type fakeChainResolver map[string][]string

func (f fakeChainResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("nxdomain")
	}
	out := []netip.Addr{}
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

// newChainEnv: tunnel 10 = 线路机 entry 1 → hop 2 → exits 3 and 4 (round);
// tunnel 20 = 线路机 entry 1 → exit 6; tunnel 30 = normal entry 5 → exit 3.
// Exits 3, 4, 6 run chain-capable agents. User 7 may use all tunnels.
func newChainEnv(t *testing.T) (*flowTestEnv, *chainAgent) {
	t.Helper()
	e := newFlowTestEnv(t)
	for id, name := range map[int64]string{1: "DMIT", 2: "hop", 3: "AkariHK", 4: "AkariHK2", 5: "plain-entry", 6: "AkariSG"} {
		e.addNode(id, fmt.Sprintf("chain-secret-%d", id))
		e.exec(`UPDATE node SET name = ?, server_ip = ?, server_ip_v4 = ?, tcp_listen_addr = '0.0.0.0', udp_listen_addr = '0.0.0.0', port = '1000-2000' WHERE id = ?`, name, fmt.Sprintf("192.0.2.%d", id), fmt.Sprintf("192.0.2.%d", id), id)
	}
	e.exec(`UPDATE node SET line_machine = ? WHERE id = 1`, true)
	e.exec(`UPDATE node SET landing_capable = ?, landing_egress = 1, landing_chain = 1 WHERE id IN (3, 4, 6)`, true)
	e.addTunnel(10, 2, 1)
	e.addTunnel(20, 2, 1)
	e.addTunnel(30, 2, 1)
	e.exec(`UPDATE tunnel SET type = 2 WHERE id IN (10, 20, 30)`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES
		(10, '1', 1, NULL, 'round', 1, 'tls'), (10, '2', 2, 1202, 'round', 1, 'tls'), (10, '3', 3, 1203, 'round', 1, 'tls'), (10, '3', 4, 1204, 'round', 2, 'tls'),
		(20, '1', 1, NULL, 'round', 1, 'tls'), (20, '3', 6, 1206, 'round', 1, 'tls'),
		(30, '1', 5, NULL, 'round', 1, 'tls'), (30, '3', 3, 1213, 'round', 1, 'tls')`)
	e.addUser(7)
	e.addUserTunnel(70, 7, 10)
	e.addUserTunnel(71, 7, 20)
	e.addUserTunnel(72, 7, 30)
	agent := newChainAgent()
	e.h.nodeCommandSender = agent.send
	oldResolver, oldStep, oldDeadline := chainResolver, chainWaitStep, chainCreateDeadline
	resolver := fakeChainResolver{"node.example.com": {"8.8.4.4"}, "rebind.example.com": {"8.8.8.8", "10.0.0.1"}}
	chainResolver = resolver
	e.h.cnLandingChecker = cnlanding.New(resolver)
	chainWaitStep = 5 * time.Millisecond
	chainCreateDeadline = 1500 * time.Millisecond
	t.Cleanup(func() {
		e.h.chains.wg.Wait()
		chainResolver, chainWaitStep, chainCreateDeadline = oldResolver, oldStep, oldDeadline
	})
	return e, agent
}

// callAs invokes a handler as a user (role 1) or admin (role 0).
func callAs(t *testing.T, fn http.HandlerFunc, uid int64, role int, body interface{}) (int, string, map[string]interface{}) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: fmt.Sprint(uid), RoleID: role}))
	rec := httptest.NewRecorder()
	fn(rec, req)
	var out struct {
		Code int                    `json:"code"`
		Msg  string                 `json:"msg"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out.Code, out.Msg, out.Data
}

const testSSLink = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@8.8.8.8:8388#home"

func chainCreateBody(tunnelID int64, name, link, key string) map[string]interface{} {
	return map[string]interface{}{"tunnelId": tunnelID, "name": name, "targetMode": "chain", "chainLink": link, "idempotencyKey": key, "mode": "gost"}
}

// createChain runs a user chain create to completion and returns the chain.
func createChain(t *testing.T, e *flowTestEnv, tunnelID int64, name, link, key string) *model.ForwardChain {
	t.Helper()
	code, msg, data := callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(tunnelID, name, link, key))
	if code != 0 {
		t.Fatalf("chain create: %d %s", code, msg)
	}
	e.h.chains.wg.Wait()
	c, err := e.r.GetForwardChain(asInt64(data["chainId"], 0))
	if err != nil || c == nil {
		t.Fatalf("chain row: %+v %v", c, err)
	}
	return c
}

func countRows(t *testing.T, e *flowTestEnv, table string) int {
	t.Helper()
	return mustQueryInt(t, e.r, "SELECT COUNT(*) FROM "+table)
}

func TestChainCreateMultiExitIdenticalCopies(t *testing.T) {
	e, agent := newChainEnv(t)
	c := createChain(t, e, 10, "my-node", testSSLink, "key-00000001")
	if c.State != model.ChainStateActive || c.ForwardID <= 0 || c.LastError != "" {
		t.Fatalf("chain not active: %+v", c)
	}
	f, err := e.h.getForwardRecord(c.ForwardID)
	if err != nil || f.RemoteAddr != chainTarget(c.Port) || f.Mode != "gost" || f.TunnelID != 10 || f.LandingNodeID != 0 {
		t.Fatalf("rule target: %+v %v", f, err)
	}
	var first map[string]interface{}
	for _, nodeID := range []int64{3, 4} {
		nodes := agent.chainNodes(nodeID)
		if len(nodes) != 1 {
			t.Fatalf("exit %d chain nodes = %v", nodeID, nodes)
		}
		n := nodes[0]
		if n["listen"] != "127.0.0.1" || asInt(n["port"], 0) != c.Port || n["protocol"] != "vless_reality_vision" {
			t.Fatalf("exit %d copy = %v", nodeID, n)
		}
		chain := n["chain"].(map[string]interface{})
		if chain["type"] != "shadowsocks" || chain["server"] != "8.8.8.8" || !strings.HasPrefix(asString(chain["tag"]), "flvxc-") {
			t.Fatalf("exit %d upstream = %v", nodeID, chain)
		}
		v := n["vless"].(map[string]interface{})
		if first == nil {
			first = v
			continue
		}
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(v)
		if string(a) != string(b) {
			t.Fatalf("copies differ: %s vs %s", a, b)
		}
	}
	// Chain-only exit landings were created and are invisible to users.
	if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM landing_server WHERE chain_only = ?", true); n != 2 {
		t.Fatalf("chain-only servers = %d", n)
	}
	if list, _ := e.r.ListLandingServers(7, false); len(list) != 0 {
		t.Fatalf("user sees chain-only servers: %+v", list)
	}
	if nodes, _ := e.r.ListLandingNodes(7, false); len(nodes) != 0 {
		t.Fatalf("chain copies appear as landing nodes: %+v", nodes)
	}
	// The user's link: entry address and rule port, the copies' Reality key.
	code, _, data := callAs(t, e.h.forwardChainStatus, 7, 1, map[string]interface{}{"forwardId": c.ForwardID})
	if code != 0 || data["state"] != "active" {
		t.Fatalf("status: %v", data)
	}
	raw, _ := json.Marshal(map[string]interface{}{"forwardId": c.ForwardID})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "7", RoleID: 1}))
	rec := httptest.NewRecorder()
	e.h.forwardChainLinks(rec, req)
	var links struct {
		Code int                      `json:"code"`
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &links)
	port := mustQueryInt(t, e.r, "SELECT port FROM forward_port WHERE forward_id = ?", c.ForwardID)
	if links.Code != 0 || len(links.Data) != 1 || !strings.HasPrefix(asString(links.Data[0]["url"]), fmt.Sprintf("vless://%s@192.0.2.1:%d?", c.UUID, port)) || !strings.Contains(asString(links.Data[0]["url"]), "pbk="+c.PublicKey) {
		t.Fatalf("links = %s", rec.Body.String())
	}
	// Another user cannot see the chain at all.
	e.addUser(8)
	if code, _, _ := callAs(t, e.h.forwardChainStatus, 8, 1, map[string]interface{}{"forwardId": c.ForwardID}); code == 0 {
		t.Fatal("foreign user read chain status")
	}
}

func TestChainCreateRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *flowTestEnv, a *chainAgent)
		body  map[string]interface{}
		want  string
	}{
		{"normal tunnel", nil, chainCreateBody(30, "x", testSSLink, ""), "仅线路机隧道"},
		{"exit is a line machine", func(e *flowTestEnv, a *chainAgent) {
			e.exec("UPDATE node SET line_machine = ? WHERE id = 4", true)
		}, chainCreateBody(10, "x", testSSLink, ""), "是线路机"},
		{"old exit agent", func(e *flowTestEnv, a *chainAgent) { e.exec("UPDATE node SET landing_chain = 0 WHERE id = 4") }, chainCreateBody(10, "x", testSSLink, ""), "版本过低"},
		{"exit offline", func(e *flowTestEnv, a *chainAgent) { e.exec("UPDATE node SET status = 0 WHERE id = 3") }, chainCreateBody(10, "x", testSSLink, ""), "离线"},
		{"unreachable upstream", func(e *flowTestEnv, a *chainAgent) { a.pingFail[4] = "connection refused" }, chainCreateBody(10, "x", testSSLink, ""), "无法连接到你的节点"},
		{"private upstream", nil, chainCreateBody(10, "x", "socks5://10.0.0.8:1080", ""), "内网"},
		{"domain resolving to private", nil, chainCreateBody(10, "x", "socks5://rebind.example.com:1080", ""), "解析到了内网"},
		{"flvx node upstream", nil, chainCreateBody(10, "x", "socks5://192.0.2.5:1080", ""), "FLVX 节点"},
		{"tuic link", nil, chainCreateBody(10, "x", "tuic://u:p@8.8.8.8:443", ""), "TUIC"},
		{"bad link", nil, chainCreateBody(10, "x", "hello", ""), "无法识别"},
		{"nftables mode", nil, func() map[string]interface{} {
			b := chainCreateBody(10, "x", testSSLink, "")
			b["mode"] = "nftables"
			return b
		}(), "gost"},
		{"rule allowance", func(e *flowTestEnv, a *chainAgent) {
			e.exec("UPDATE user SET num = 1 WHERE id = 7")
			e.addForward(500, 7, 30)
		}, chainCreateBody(10, "x", testSSLink, ""), "规则数上限"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, a := newChainEnv(t)
			if tc.setup != nil {
				tc.setup(e, a)
			}
			code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, tc.body)
			e.h.chains.wg.Wait()
			if code == 0 || !strings.Contains(msg, tc.want) {
				t.Fatalf("code=%d msg=%q, want %q", code, msg, tc.want)
			}
			if n := countRows(t, e, "forward_chain") + countRows(t, e, "forward_chain_node"); n != 0 {
				t.Fatalf("refused create left %d chain rows", n)
			}
			if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM forward WHERE tunnel_id = 10"); n != 0 {
				t.Fatalf("refused create left rules: %d", n)
			}
		})
	}
}

// Any failing step rolls back everything this create added.
func TestChainCreateRollbackOnFailures(t *testing.T) {
	cases := map[string]func(e *flowTestEnv, a *chainAgent){
		"exit never applies": func(e *flowTestEnv, a *chainAgent) {
			a.applyErr = func(nodeID int64) error {
				if nodeID == 4 {
					return errors.New("等待节点响应超时")
				}
				return nil
			}
		},
		"core needs restart": func(e *flowTestEnv, a *chainAgent) {
			a.egress = func(nodeID int64) map[string]interface{} {
				return map[string]interface{}{"state": "restart_required", "ready": false, "error": "需要重启"}
			}
		},
		"chain withheld": func(e *flowTestEnv, a *chainAgent) {
			a.egress = func(nodeID int64) map[string]interface{} {
				return map[string]interface{}{"state": "error", "ready": true, "error": "添加落地出站失败"}
			}
		},
		"rule deploy fails": func(e *flowTestEnv, a *chainAgent) {
			a.forwardEr = func(nodeID int64, method string) error {
				if nodeID == 1 && (method == "AddService" || method == "UpdateService") {
					return errors.New("injected entry failure")
				}
				return nil
			}
		},
		"entry port taken": func(e *flowTestEnv, a *chainAgent) {
			e.addForward(600, 7, 30)
			e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(600, 1, 1500)")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e, a := newChainEnv(t)
			setup(e, a)
			body := chainCreateBody(10, "x", testSSLink, "key-rollback1")
			if name == "entry port taken" {
				body["inPort"] = 1500
			}
			code, msg, data := callAs(t, e.h.forwardCreate, 7, 1, body)
			if name == "entry port taken" {
				if code == 0 {
					t.Fatalf("port conflict accepted")
				}
				return
			}
			if code != 0 {
				t.Fatalf("create start: %s", msg)
			}
			if name == "exit never applies" {
				// Shorten the wait: the driver retries until its deadline.
				waitDriverWithDeadline(t, e, asInt64(data["chainId"], 0))
			}
			e.h.chains.wg.Wait()
			c, _ := e.r.GetForwardChain(asInt64(data["chainId"], 0))
			if c == nil || c.State != model.ChainStateFailed || c.LastError == "" || c.Upstream != "" || c.PrivateKey != "" {
				t.Fatalf("failed chain kept secrets or state: %+v", c)
			}
			if n := countRows(t, e, "forward_chain_node"); n != 0 {
				t.Fatalf("copies left: %d", n)
			}
			if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM forward WHERE tunnel_id = 10"); n != 0 {
				t.Fatalf("rule left: %d", n)
			}
			for _, nodeID := range []int64{3, 4} {
				if len(a.chainNodes(nodeID)) != 0 {
					t.Fatalf("exit %d still runs the chain: %v", nodeID, a.chainNodes(nodeID))
				}
			}
			// Same submit key again: the failure is returned, nothing new runs.
			code, _, again := callAs(t, e.h.forwardCreate, 7, 1, body)
			if code != 0 || asInt64(again["chainId"], 0) != c.ID || again["state"] != "failed" {
				t.Fatalf("idempotent failed result: %v", again)
			}
		})
	}
}

// waitDriverWithDeadline expires the running driver's deadline quickly by
// letting it fail fast (the fake apply error never clears).
func waitDriverWithDeadline(t *testing.T, e *flowTestEnv, chainID int64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for e.h.chains.hasDriver(chainID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e.h.chains.hasDriver(chainID) {
		t.Fatal("driver did not finish")
	}
}

func TestChainIdempotencySingleFlightRateLimitDuplicates(t *testing.T) {
	e, a := newChainEnv(t)
	// Hold the first create inside the exit apply.
	release := make(chan struct{})
	var once sync.Once
	a.setApplyErr(func(nodeID int64) error {
		<-release
		return nil
	})
	body := chainCreateBody(10, "first", testSSLink, "key-idem-0001")
	code, msg, first := callAs(t, e.h.forwardCreate, 7, 1, body)
	if code != 0 {
		t.Fatal(msg)
	}
	// Double click: same key returns the same chain.
	code, _, again := callAs(t, e.h.forwardCreate, 7, 1, body)
	if code != 0 || asInt64(again["chainId"], 0) != asInt64(first["chainId"], 0) {
		t.Fatalf("same key: %v %v", first, again)
	}
	// Another submit while the first runs: single flight.
	code, msg, _ = callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "second", "socks5://9.9.9.9:1080", "key-idem-0002"))
	if code == 0 || !strings.Contains(msg, "正在创建") {
		t.Fatalf("concurrent create: %d %s", code, msg)
	}
	once.Do(func() { close(release) })
	e.h.chains.wg.Wait()
	a.setApplyErr(nil)
	e.h.chains.wg.Wait()
	if n := countRows(t, e, "forward_chain"); n != 1 {
		t.Fatalf("chains = %d", n)
	}
	// Same node link on the same tunnel again: refused with the rule name.
	code, msg, _ = callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "dup", "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@8.8.8.8:8388#other-name", "key-idem-0003"))
	e.h.chains.wg.Wait()
	if code == 0 || !strings.Contains(msg, "first") {
		t.Fatalf("duplicate link: %d %s", code, msg)
	}
	// Rate limit: attempts that reached the exits count; refusals do not.
	for i := 0; i < 4; i++ {
		code, msg, _ = callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, fmt.Sprintf("r%d", i), fmt.Sprintf("socks5://9.9.9.%d:1080", i+1), fmt.Sprintf("key-rate-000%d", i)))
		e.h.chains.wg.Wait()
		if code != 0 {
			t.Fatalf("create %d: %s", i, msg)
		}
	}
	code, msg, _ = callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "r9", "socks5://9.9.9.9:1081", "key-rate-0009"))
	if code == 0 || !strings.Contains(msg, "太频繁") {
		t.Fatalf("rate limit: %d %s", code, msg)
	}
	// Admins are not rate limited.
	code, msg, _ = callAs(t, e.h.forwardCreate, 1, 0, func() map[string]interface{} {
		b := chainCreateBody(10, "admin", "socks5://9.9.9.9:1082", "key-rate-0010")
		b["userId"] = 7
		return b
	}())
	e.h.chains.wg.Wait()
	if code != 0 {
		t.Fatalf("admin create: %s", msg)
	}
	// Normal rule creates dedupe by key as well (no second auto-created node).
	normal := map[string]interface{}{"tunnelId": 30, "name": "plain", "remoteAddr": "1.1.1.1:443", "inPort": 1600, "idempotencyKey": "key-normal-01"}
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, normal); code != 0 {
		t.Fatal(msg)
	}
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, normal); code != 0 {
		t.Fatal(msg)
	}
	if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM forward WHERE name = 'plain'"); n != 1 {
		t.Fatalf("double submit created %d rules", n)
	}
}

func chainRowsLeft(t *testing.T, e *flowTestEnv) int {
	return countRows(t, e, "forward_chain") + countRows(t, e, "forward_chain_node")
}

func expectNoChainOnExits(t *testing.T, e *flowTestEnv, a *chainAgent, nodes ...int64) {
	t.Helper()
	e.h.chains.wg.Wait()
	for _, id := range nodes {
		if left := a.chainNodes(id); len(left) != 0 {
			t.Fatalf("exit %d still told to run chain nodes: %v", id, left)
		}
	}
}

// Every delete path removes chain rows and the exit copies.
func TestChainDeletePathsLeaveNothing(t *testing.T) {
	paths := map[string]func(t *testing.T, e *flowTestEnv, c *model.ForwardChain){
		"rule delete": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
				t.Fatal(msg)
			}
		},
		"batch delete": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			if code, msg, data := callAs(t, e.h.forwardBatchDelete, 7, 1, map[string]interface{}{"ids": []int64{c.ForwardID}}); code != 0 || asInt(data["successCount"], 0) != 1 {
				t.Fatal(msg, data)
			}
		},
		"force delete": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			if code, msg, _ := callAs(t, e.h.forwardForceDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
				t.Fatal(msg)
			}
		},
		"user delete": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			if err := e.h.deleteUserAndCleanup(7); err != nil {
				t.Fatal(err)
			}
		},
		"tunnel delete": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			if err := e.h.deleteTunnelByID(10); err != nil {
				t.Fatal(err)
			}
		},
		"orphan sweep": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			// A rule removed behind the panel's back (e.g. manual DB edit).
			e.exec("DELETE FROM forward_port WHERE forward_id = ?", c.ForwardID)
			e.exec("DELETE FROM forward WHERE id = ?", c.ForwardID)
			code, msg, data := callAs(t, e.h.nodeOrphanSweep, 1, 0, map[string]interface{}{"execute": true, "nodeIds": []int64{999}})
			if code != 0 {
				t.Fatal(msg)
			}
			orphans := data["chainOrphans"].([]interface{})
			if len(orphans) != 1 || orphans[0].(map[string]interface{})["deleted"] != true {
				t.Fatalf("orphans = %v", orphans)
			}
		},
		"reconciler": func(t *testing.T, e *flowTestEnv, c *model.ForwardChain) {
			e.exec("DELETE FROM forward_port WHERE forward_id = ?", c.ForwardID)
			e.exec("DELETE FROM forward WHERE id = ?", c.ForwardID)
			e.exec("UPDATE forward_chain SET updated_time = ?", time.Now().Add(-time.Hour).UnixMilli())
			e.h.reconcileChains(time.Now())
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			e, a := newChainEnv(t)
			c := createChain(t, e, 10, "del", testSSLink, "key-delete-01")
			if len(a.chainNodes(3)) != 1 || len(a.chainNodes(4)) != 1 {
				t.Fatal("copies not deployed")
			}
			run(t, e, c)
			if n := chainRowsLeft(t, e); n != 0 {
				t.Fatalf("%s left %d chain rows", name, n)
			}
			expectNoChainOnExits(t, e, a, 3, 4)
		})
	}
}

// Tunnel edits move the copies: a removed exit loses its copy, a new exit
// gets one; node delete then removes the unused chain-only landing.
func TestChainFollowsTunnelExitChanges(t *testing.T) {
	e, a := newChainEnv(t)
	c := createChain(t, e, 10, "move", testSSLink, "key-move-0001")
	// Admin replaces exit 4 by exit 6 (the edit has committed; reconcile).
	e.exec("UPDATE chain_tunnel SET node_id = 6 WHERE tunnel_id = 10 AND node_id = 4")
	warnings := e.h.reconcileTunnelChains(10)
	e.h.chains.wg.Wait()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
	copies, _ := e.r.ListForwardChainNodes(c.ID)
	got := map[int64]string{}
	for _, n := range copies {
		got[n.NodeID] = n.State
	}
	if len(copies) != 2 || got[3] != model.ChainNodeActive || got[6] != model.ChainNodeActive {
		t.Fatalf("copies after move = %+v", copies)
	}
	if len(a.chainNodes(6)) != 1 || len(a.chainNodes(4)) != 0 {
		t.Fatalf("agents after move: 6=%v 4=%v", a.chainNodes(6), a.chainNodes(4))
	}
	after, _ := e.r.GetForwardChain(c.ID)
	if after.OpStartedAt != 0 || after.LastError != "" {
		t.Fatalf("move op state %+v", after)
	}
	// Node 4 left every tunnel: deleting it also removes its chain-only landing.
	e.exec("DELETE FROM chain_tunnel WHERE node_id = 4")
	if err := e.h.deleteNodeByID(4); err != nil {
		t.Fatalf("node delete: %v", err)
	}
	if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM landing_server WHERE node_id = 4"); n != 0 {
		t.Fatal("chain-only landing survived its node")
	}
	// A node that still hosts copies cannot be deleted or made a 线路机.
	e.exec("DELETE FROM chain_tunnel WHERE node_id = 6")
	if err := e.h.deleteNodeByID(6); err == nil || !strings.Contains(err.Error(), "我自己的节点") {
		t.Fatalf("node with chain copies deleted: %v", err)
	}
	code, msg, _ := callAs(t, e.h.nodeUpdate, 1, 0, map[string]interface{}{"id": 3, "name": "AkariHK", "serverIp": "192.0.2.3", "port": "1000-2000", "lineMachine": true})
	if code == 0 || !strings.Contains(msg, "不能设为线路机") {
		t.Fatalf("exit with copies became 线路机: %d %s", code, msg)
	}
	// Exits that are not chain-capable: the move fails visibly, copies stay.
	e.exec("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES (10, '3', 5, 1215, 'round', 3, 'tls')")
	warnings = e.h.reconcileTunnelChains(10)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "无法迁移") {
		t.Fatalf("unsupported exit: %v", warnings)
	}
	after, _ = e.r.GetForwardChain(c.ID)
	if !strings.Contains(after.LastError, "无法迁移") {
		t.Fatalf("error not recorded: %+v", after)
	}
}

func TestChainEditRelinkRetargetAndConvert(t *testing.T) {
	e, a := newChainEnv(t)
	c := createChain(t, e, 10, "edit", testSSLink, "key-edit-0001")
	// Rename only: the normal path keeps the loopback target.
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "name": "renamed", "tunnelId": 10, "targetMode": "chain"}); code != 0 {
		t.Fatal(msg)
	}
	f, _ := e.h.getForwardRecord(c.ForwardID)
	if f.Name != "renamed" || f.RemoteAddr != chainTarget(c.Port) {
		t.Fatalf("rename: %+v", f)
	}
	// New link: hot swap on both exits, same inbound (the user's link stays).
	code, msg, data := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "name": "renamed", "tunnelId": 10, "targetMode": "chain", "chainLink": "trojan://secret@node.example.com:443?sni=node.example.com"})
	if code != 0 || data["op"] != "relink" {
		t.Fatalf("relink start: %d %s %v", code, msg, data)
	}
	e.h.chains.wg.Wait()
	after, _ := e.r.GetForwardChain(c.ID)
	if after.UpstreamProtocol != "Trojan" || after.PrevUpstream != "" || after.OpStartedAt != 0 || after.LastOpAt == 0 || after.LastError != "" || after.UUID != c.UUID || after.Port != c.Port {
		t.Fatalf("relink result %+v", after)
	}
	for _, nodeID := range []int64{3, 4} {
		up := a.chainNodes(nodeID)[0]["chain"].(map[string]interface{})
		if up["type"] != "trojan" {
			t.Fatalf("exit %d upstream = %v", nodeID, up)
		}
	}
	// Failing relink: reverted to the previous upstream.
	a.setApplyErr(func(nodeID int64) error {
		if nodeID == 4 {
			return errors.New("injected")
		}
		return nil
	})
	code, _, data = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "tunnelId": 10, "targetMode": "chain", "chainLink": "socks5://9.9.9.9:1080"})
	if code != 0 {
		t.Fatal("relink 2 start")
	}
	waitDriverWithDeadline(t, e, c.ID)
	a.setApplyErr(nil)
	after, _ = e.r.GetForwardChain(c.ID)
	if after.UpstreamProtocol != "Trojan" || after.LastError == "" || after.OpStartedAt != 0 {
		t.Fatalf("failed relink not reverted: %+v", after)
	}
	// Retarget to tunnel 20 (exit 6): new copy there, old ones gone.
	code, msg, data = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "tunnelId": 20, "targetMode": "chain"})
	if code != 0 || data["op"] != "retarget" {
		t.Fatalf("retarget: %d %s %v", code, msg, data)
	}
	e.h.chains.wg.Wait()
	after, _ = e.r.GetForwardChain(c.ID)
	f, _ = e.h.getForwardRecord(c.ForwardID)
	copies, _ := e.r.ListForwardChainNodes(c.ID)
	if after.TunnelID != 20 || f.TunnelID != 20 || f.RemoteAddr != chainTarget(after.Port) || len(copies) != 1 || copies[0].NodeID != 6 || after.LastError != "" {
		t.Fatalf("retarget result chain=%+v forward=%+v copies=%+v", after, f, copies)
	}
	expectNoChainOnExits(t, e, a, 3, 4)
	if len(a.chainNodes(6)) != 1 {
		t.Fatal("new exit not deployed")
	}
	// Convert away to a Reality landing node: the chain is removed.
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, created_time, updated_time) VALUES (50, 'native', 2, 'land', '192.0.2.2', 'active', 1, 1, 1)`)
	e.exec(`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES (90, 7, 50, 'r', 'vless_reality_vision', 31000, 'flvxl-90', 'active', 1, 1)`)
	e.exec("UPDATE node SET landing_capable = ? WHERE id = 2", true)
	e.exec("INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (50, 7)")
	code, msg, _ = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "tunnelId": 20, "targetMode": "node", "landingNodeId": 90})
	if code != 0 {
		t.Fatalf("convert away: %s", msg)
	}
	e.h.chains.wg.Wait()
	if n := chainRowsLeft(t, e); n != 0 {
		t.Fatalf("chain kept after converting away: %d", n)
	}
	expectNoChainOnExits(t, e, a, 6)
	// Convert a normal rule on a 线路机 tunnel into a chain rule.
	code, msg, data = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "tunnelId": 10, "targetMode": "chain", "chainLink": testSSLink})
	if code != 0 || data["op"] != "attach" {
		t.Fatalf("attach: %d %s %v", code, msg, data)
	}
	e.h.chains.wg.Wait()
	attached, _ := e.r.GetForwardChainByForward(c.ForwardID)
	f, _ = e.h.getForwardRecord(c.ForwardID)
	if attached == nil || attached.State != model.ChainStateActive || f.RemoteAddr != chainTarget(attached.Port) || f.TunnelID != 10 || f.LandingNodeID != 0 {
		t.Fatalf("attach result %+v %+v", attached, f)
	}
	// Batch tunnel change refuses chain rules.
	_, _, res := callAs(t, e.h.forwardBatchChangeTunnel, 7, 1, map[string]interface{}{"forwardIds": []int64{c.ForwardID}, "targetTunnelId": 20})
	if asInt(res["failCount"], 0) != 1 {
		t.Fatalf("batch change of chain rule: %v", res)
	}
}

func TestLineMachineTargetEnforcement(t *testing.T) {
	e, _ := newChainEnv(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, created_time, updated_time, protocols) VALUES (50, 'native', 2, 'land', '192.0.2.2', 'active', 1, 1, 1, '["ss2022","vless_reality_vision"]')`)
	e.exec("UPDATE node SET landing_capable = ? WHERE id = 2", true)
	e.exec("INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (50, 7)")
	e.exec(`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES
		(90, 7, 50, 'reality', 'vless_reality_vision', 31000, 'flvxl-90', 'active', 1, 1), (91, 7, 50, 'ss', 'ss2022', 31001, 'flvxl-91', 'active', 1, 1)`)
	create := func(uid int64, role int, extra map[string]interface{}) (int, string) {
		body := map[string]interface{}{"tunnelId": 10, "name": "r", "inPort": 1700, "remoteAddr": "1.1.1.1:443"}
		if role == 0 {
			body["userId"] = 7
		}
		for k, v := range extra {
			body[k] = v
		}
		code, msg, _ := callAs(t, e.h.forwardCreate, uid, role, body)
		return code, msg
	}
	for name, tc := range map[string]struct {
		uid   int64
		role  int
		extra map[string]interface{}
		ok    bool
		want  string
	}{
		"user manual":            {7, 1, nil, false, "不能手动填写"},
		"admin manual":           {1, 0, nil, false, "不能手动填写"},
		"ss landing node":        {7, 1, map[string]interface{}{"remoteAddr": "", "landingNodeId": 91}, false, "VLESS Reality Vision"},
		"auto node ss2022":       {7, 1, map[string]interface{}{"remoteAddr": "", "landingServerId": 50, "landingProtocol": "ss2022"}, false, "只能使用 VLESS"},
		"reality landing node":   {7, 1, map[string]interface{}{"remoteAddr": "", "landingNodeId": 90, "inPort": 1701}, true, ""},
		"auto node reality":      {7, 1, map[string]interface{}{"remoteAddr": "", "landingServerId": 50, "landingProtocol": "vless_reality_vision", "inPort": 1702}, true, ""},
		"typed own reality node": {7, 1, map[string]interface{}{"remoteAddr": "192.0.2.2:31000", "inPort": 1703}, false, "不能手动填写"},
	} {
		code, msg := create(tc.uid, tc.role, tc.extra)
		if tc.ok != (code == 0) || (!tc.ok && !strings.Contains(msg, tc.want)) {
			t.Errorf("%s: code=%d msg=%q", name, code, msg)
		}
	}
	if n := mustQueryInt(t, e.r, "SELECT COUNT(*) FROM landing_node WHERE user_id = 7"); n != 3 {
		t.Fatalf("refused auto-create left a landing node: %d nodes", n)
	}
	// Loopback typed by a user on a normal tunnel.
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, map[string]interface{}{"tunnelId": 30, "name": "lo", "remoteAddr": "127.0.0.1:22", "inPort": 1710}); code == 0 || !strings.Contains(msg, "回环") {
		t.Fatalf("loopback target: %d %s", code, msg)
	}
	// A rule existing before the tunnel became a 线路机 tunnel keeps working,
	// is marked 不合规, stays editable, but cannot get a new manual target.
	e.addForward(700, 7, 10)
	e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(700, 1, 1800)")
	listItems, err := e.r.ListForwards()
	if err != nil {
		t.Fatal(err)
	}
	if n := e.h.decorateForwardChains(listItems); n != 1 {
		t.Fatalf("non-compliant count: %d", n)
	}
	marked := false
	for _, item := range listItems {
		if asInt64(item["id"], 0) == 700 {
			marked = item["lineNonCompliant"] == true
		}
	}
	if !marked {
		t.Fatal("rule 700 not marked 不合规")
	}
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": 700, "name": "renamed", "tunnelId": 10, "inPort": 1800}); code != 0 {
		t.Fatalf("unchanged non-compliant rule not editable: %s", msg)
	}
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": 700, "tunnelId": 10, "remoteAddr": "9.9.9.9:443", "inPort": 1800}); code == 0 || !strings.Contains(msg, "不能手动填写") {
		t.Fatalf("new manual target accepted: %d %s", code, msg)
	}
	// Batch move of a manual rule into a 线路机 tunnel is refused per item.
	e.addForward(701, 7, 30)
	e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(701, 5, 1801)")
	_, _, res := callAs(t, e.h.forwardBatchChangeTunnel, 7, 1, map[string]interface{}{"forwardIds": []int64{701}, "targetTunnelId": 10})
	if asInt(res["failCount"], 0) != 1 {
		t.Fatalf("batch move into 线路机 tunnel: %v", res)
	}
	// Backup import of a manual rule on a 线路机 tunnel is refused.
	raw := map[string]interface{}{"forward": []interface{}{map[string]interface{}{"name": "imp", "tunnel_id": 10, "remote_addr": "1.1.1.1:443", "mode": "gost"}}}
	if err := e.h.checkForwardImportLineMachine(raw, []string{"forward"}); err == nil || !strings.Contains(err.Error(), "imp") {
		t.Fatalf("import: %v", err)
	}
	// Federation cannot share a 线路机.
	if code, msg, _ := callAs(t, e.h.federationShareCreate, 1, 0, map[string]interface{}{"name": "s", "nodeId": 1}); code == 0 || !strings.Contains(msg, "线路机") {
		t.Fatalf("federation share of 线路机: %d %s", code, msg)
	}
	// Tunnel list mirrors the rules for the dialog.
	items := []map[string]interface{}{{"id": int64(10)}, {"id": int64(30)}}
	e.h.decorateTunnelLineInfo(items)
	if items[0]["lineMachine"] != true || items[0]["chainAvailable"] != true || items[1]["lineMachine"] != false {
		t.Fatalf("tunnel decoration: %v", items)
	}
}

func TestChainDiagnosisAndBilling(t *testing.T) {
	e, _ := newChainEnv(t)
	c := createChain(t, e, 10, "diag", "socks5://node.example.com:1080", "key-diag-0001")
	f, _ := e.h.getForwardRecord(c.ForwardID)
	_, items, err := e.h.prepareForwardDiagnosis(f)
	if err != nil {
		t.Fatal(err)
	}
	exitTargets := 0
	for _, it := range items {
		if asInt(it.metadata["fromChainType"], 0) == 3 && !it.hasChainHop {
			exitTargets++
			if it.targetIP != "node.example.com" || it.targetPort != 1080 || !strings.Contains(it.description, "node.example.com:1080") {
				t.Fatalf("exit diagnosis target %+v", it)
			}
		}
	}
	if exitTargets != 2 {
		t.Fatalf("exit→target items = %d", exitTargets)
	}
	// Chain copy counters never reach landing billing; the rule is billed.
	copies, _ := e.r.ListForwardChainNodes(c.ID)
	counter := repo.LandingCounter{ID: model.ChainAgentIDBase + copies[0].ID, InboundTag: fmt.Sprintf("flvxl-%d", model.ChainAgentIDBase+copies[0].ID), Epoch: "100", UpBytes: 1 << 30, DownBytes: 1 << 30}
	if got := filterChainCounters([]repo.LandingCounter{counter}); len(got) != 0 {
		t.Fatalf("chain counter kept for billing: %+v", got)
	}
	// Even unfiltered, an id without a landing node row is never billed.
	if _, err := e.h.commitFlowWithLanding(3, nil, []repo.LandingCounter{counter}); err != nil {
		t.Fatal(err)
	}
	e.expectFlows("user", 7, 0, 0)
	if n, _ := e.r.ListLandingNodes(7, false); len(n) != 0 {
		t.Fatal("chain counted as landing node")
	}
}

func TestChainReconcilerRecoversInterruptedCreates(t *testing.T) {
	e, a := newChainEnv(t)
	up := mustUpstream(t, testSSLink)
	stale := time.Now().Add(-time.Hour).UnixMilli()
	// 1: interrupted before the rule existed → rolled back.
	notInFlight := time.Now().Add(time.Hour).UnixMilli()
	c1, _, err := e.r.CreateForwardChain(repo.ChainCreateInput{UserID: 7, TunnelID: 10, Upstream: up, ExitNodeIDs: []int64{3, 4}, InFlightAfter: notInFlight})
	if err != nil {
		t.Fatal(err)
	}
	// 2: the rule was created, activation was lost → finished.
	up2 := mustUpstream(t, "socks5://9.9.9.9:1080")
	c2, _, err := e.r.CreateForwardChain(repo.ChainCreateInput{UserID: 7, TunnelID: 10, Upstream: up2, ExitNodeIDs: []int64{3, 4}, InFlightAfter: notInFlight})
	if err != nil {
		t.Fatal(err)
	}
	e.addForward(800, 7, 10)
	e.exec("UPDATE forward SET remote_addr = ? WHERE id = 800", chainTarget(c2.Port))
	e.exec("UPDATE forward_chain SET op_started_at = ?", stale)
	e.h.reconcileChains(time.Now())
	r1, _ := e.r.GetForwardChain(c1.ID)
	r2, _ := e.r.GetForwardChain(c2.ID)
	if r1.State != model.ChainStateFailed || r2.State != model.ChainStateActive || r2.ForwardID != 800 {
		t.Fatalf("reconcile: %+v %+v", r1, r2)
	}
	copies, _ := e.r.ListForwardChainNodes(c1.ID)
	if len(copies) != 0 {
		t.Fatal("rolled back chain kept copies")
	}
	e.h.chains.wg.Wait()
	for _, nodeID := range []int64{3, 4} {
		for _, n := range a.chainNodes(nodeID) {
			if asInt(n["port"], 0) == c1.Port {
				t.Fatalf("exit %d still runs the rolled back chain", nodeID)
			}
		}
	}
	// Failed results are purged after an hour.
	e.exec("UPDATE forward_chain SET updated_time = ? WHERE id = ?", stale, c1.ID)
	e.h.reconcileChains(time.Now())
	if c, _ := e.r.GetForwardChain(c1.ID); c != nil {
		t.Fatal("failed result not purged")
	}
}

func mustUpstream(t *testing.T, link string) linechain.Upstream {
	t.Helper()
	u, _, err := linechain.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// The mainland-China target policy of normal rules applies to the user's
// node, and a tunnel with chain rules must keep its exit tier.
func TestChainCNPolicyAndTunnelGuard(t *testing.T) {
	e, _ := newChainEnv(t)
	code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "cn", "socks5://114.114.114.114:1080", "key-cn-000001"))
	e.h.chains.wg.Wait()
	if code == 0 || !strings.Contains(msg, "中国大陆") {
		t.Fatalf("mainland upstream accepted: %d %s", code, msg)
	}
	c := createChain(t, e, 10, "guard", testSSLink, "key-guard-001")
	f, _ := e.h.getForwardRecord(c.ForwardID)
	if err := e.h.checkForwardRecordLanding(context.Background(), f); err != nil {
		t.Fatalf("policy check of a chain rule must use its upstream: %v", err)
	}
	e.exec("UPDATE forward_chain SET upstream_host = '114.114.114.114', upstream_port = 1080 WHERE id = ?", c.ID)
	if err := e.h.checkForwardRecordLanding(context.Background(), f); err == nil || !strings.Contains(err.Error(), "中国大陆") {
		t.Fatalf("periodic CN check ignores the chain upstream: %v", err)
	}
	code, msg, _ = callAs(t, e.h.tunnelUpdate, 1, 0, map[string]interface{}{"id": 10, "name": "t", "type": 1, "flow": 2, "trafficRatio": 1, "status": 1, "inNodeId": []map[string]interface{}{{"nodeId": 1}}})
	if code == 0 || !strings.Contains(msg, "必须保留出口") {
		t.Fatalf("tunnel lost its exits under chain rules: %d %s", code, msg)
	}
}
