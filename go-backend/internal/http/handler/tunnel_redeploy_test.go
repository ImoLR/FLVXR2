package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

type redeployCommand struct {
	nodeID int64
	method string
	data   interface{}
}

type redeployCommandLog struct {
	mu       sync.Mutex
	commands []redeployCommand
	fail     func(int64, string) error
}

func (l *redeployCommandLog) send(nodeID int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
	l.mu.Lock()
	l.commands = append(l.commands, redeployCommand{nodeID: nodeID, method: method, data: data})
	l.mu.Unlock()
	if l.fail != nil {
		if err := l.fail(nodeID, method); err != nil {
			return ws.CommandResult{}, err
		}
	}
	return ws.CommandResult{Success: true}, nil
}

func (l *redeployCommandLog) snapshot() []redeployCommand {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]redeployCommand(nil), l.commands...)
}

func (l *redeployCommandLog) count(nodeID int64, method string) int {
	n := 0
	for _, command := range l.snapshot() {
		if command.nodeID == nodeID && command.method == method {
			n++
		}
	}
	return n
}

func newTunnelRedeployEnv(t *testing.T) (*flowTestEnv, *redeployCommandLog) {
	t.Helper()
	e := newFlowTestEnv(t)
	for i, name := range []string{"entry", "hop", "exit-offline", "exit-healthy", "entry-other"} {
		id := int64(i + 1)
		e.addNode(id, name+"-secret")
		e.exec(`UPDATE node SET name = ?, server_ip = ?, server_ip_v4 = ?, tcp_listen_addr = '0.0.0.0', udp_listen_addr = '0.0.0.0' WHERE id = ?`,
			name, fmt.Sprintf("192.0.2.%d", id), fmt.Sprintf("192.0.2.%d", id), id)
	}
	e.addTunnel(10, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2, name = 'repair-tunnel' WHERE id = 10`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(10, '1', 1, NULL, 'round', 1, 'tls'),
		      (10, '2', 2, 1202, 'round', 1, 'tls'),
		      (10, '3', 3, 1203, 'round', 1, 'tls'),
		      (10, '3', 4, 1204, 'round', 2, 'tls')`)
	commands := &redeployCommandLog{}
	e.h.nodeCommandSender = commands.send
	return e, commands
}

func addRedeployForwards(e *flowTestEnv, mode string) {
	e.addUser(7)
	e.addUserTunnel(70, 7, 10)
	e.addForward(100, 7, 10)
	e.exec(`UPDATE forward SET mode = ?, remote_addr = '1.1.1.1:443' WHERE id = 100`, mode)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(10, '1', 5, NULL, 'round', 2, 'tls')`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(100, 1, 1501), (100, 5, 1505)`)
}

func TestTunnelRedeployReconnectFailureNeverTouchesOtherNodes(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	commands.fail = func(nodeID int64, method string) error {
		if nodeID == 1 && method == "AddChains" {
			return errors.New("等待节点响应超时")
		}
		return nil
	}
	e.h.redeployNodeRuntime(1)
	if commands.count(1, "AddChains") != 1 {
		t.Fatalf("entry replacement missing: %+v", commands.snapshot())
	}
	for _, command := range commands.snapshot() {
		if command.nodeID != 1 {
			t.Fatalf("entry reconnect sent %s to healthy node %d", command.method, command.nodeID)
		}
	}
}

func TestTunnelRedeployReconnectFiltersMultiEntryForwardServices(t *testing.T) {
	for _, mode := range []string{"gost", "nftables"} {
		t.Run(mode, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			addRedeployForwards(e, mode)
			e.h.redeployNodeRuntime(1)
			for _, command := range commands.snapshot() {
				if command.nodeID != 1 {
					t.Fatalf("reconnect %s sent %s to node %d", mode, command.method, command.nodeID)
				}
			}
			method := "UpdateService"
			if mode == "nftables" {
				method = "AddNftablesRules"
			}
			if commands.count(1, method) != 1 {
				t.Fatalf("forward not restored on reconnecting entry: %+v", commands.snapshot())
			}
		})
	}
}

func TestTunnelRedeployReconnectKeepsFederationBindings(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected federation request", http.StatusInternalServerError)
	}))
	defer remote.Close()
	e.exec(`UPDATE node SET is_remote = 1, remote_url = ?, remote_token = 'remote-token' WHERE id = 2`, remote.URL)
	binding := model.FederationTunnelBinding{TunnelID: 10, NodeID: 2, ChainType: 2, HopInx: 1, RemoteURL: remote.URL,
		ResourceKey: "existing-resource", RemoteBindingID: "existing-binding", AllocatedPort: 1299,
		Status: 1, CreatedTime: e.now, UpdatedTime: e.now}
	if err := e.r.DB().Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	e.h.redeployNodeRuntime(1)
	bindings, err := e.r.ListActiveFederationTunnelBindingsByTunnel(10)
	if err != nil || len(bindings) != 1 || bindings[0].ID != binding.ID || bindings[0].RemoteBindingID != binding.RemoteBindingID {
		t.Fatalf("reconnect rewrote federation bindings: %+v (%v)", bindings, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("local reconnect made %d federation requests", requests.Load())
	}
	if commands.count(1, "AddChains") != 1 {
		t.Fatalf("local entry was not restored with its remote peer: %+v", commands.snapshot())
	}
	for _, command := range commands.snapshot() {
		if command.nodeID != 1 {
			t.Fatalf("local reconnect contacted node %d", command.nodeID)
		}
		if command.method == "AddChains" {
			raw, err := json.Marshal(command.data)
			if err != nil || !strings.Contains(string(raw), "192.0.2.2:1299") || strings.Contains(string(raw), "192.0.2.2:1202") {
				t.Fatalf("local reconnect ignored the latest federation allocated port: %s (%v)", raw, err)
			}
		}
	}
}

func TestTunnelRedeployFullContinuesAfterOfflineExit(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	e.addNode(6, "last-hop-secret")
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(10, '2', 6, 1206, 'round', 2, 'tls')`)
	e.exec(`UPDATE node SET status = 0 WHERE id = 3`)
	commands.fail = func(nodeID int64, _ string) error {
		if nodeID == 3 {
			return errors.New("节点不在线")
		}
		return nil
	}
	err := e.h.redeployTunnelAndForwards(10)
	if err == nil || !strings.Contains(err.Error(), "exit-offline") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("offline exit missing from result: %v", err)
	}
	for _, expected := range []struct {
		nodeID int64
		method string
	}{{4, "AddService"}, {6, "AddChains"}, {6, "AddService"}, {2, "AddChains"}, {2, "AddService"}, {1, "AddChains"}} {
		if commands.count(expected.nodeID, expected.method) != 1 {
			t.Fatalf("healthy node %d lost %s: %+v", expected.nodeID, expected.method, commands.snapshot())
		}
	}
	// A healthy node is completely replaced before another node is torn down.
	lastRank := -1
	for _, command := range commands.snapshot() {
		rank := map[int64]int{3: 0, 4: 1, 6: 2, 2: 3, 1: 4}[command.nodeID]
		if rank < lastRank {
			t.Fatalf("replacement did not finish downstream first: %+v", commands.snapshot())
		}
		lastRank = rank
	}
}

func TestTunnelRedeployFullReportsEveryFailedNode(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	commands.fail = func(nodeID int64, method string) error {
		if (nodeID == 3 && method == "AddService") || (nodeID == 1 && method == "AddChains") {
			return errors.New("injected push failure")
		}
		return nil
	}
	err := e.h.redeployTunnelAndForwards(10)
	if err == nil || !strings.Contains(err.Error(), "exit-offline") || !strings.Contains(err.Error(), "entry") {
		t.Fatalf("expected both failed nodes, got %v", err)
	}
	if commands.count(4, "AddService") != 1 || commands.count(2, "AddService") != 1 {
		t.Fatalf("healthy replacements missing: %+v", commands.snapshot())
	}
}

func TestTunnelRedeployReadsFreshStateAndSkipsInactive(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	e.exec(`UPDATE node SET server_ip = '192.0.2.99', server_ip_v4 = '192.0.2.99' WHERE id = 2`)
	if err := e.h.redeployTunnelRuntimeOnNode(10, 1); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands.snapshot() {
		if command.method == "AddChains" {
			raw, err := json.Marshal(command.data)
			if err != nil || !strings.Contains(string(raw), "192.0.2.99") {
				t.Fatalf("replacement did not use latest next-hop address: %s (%v)", raw, err)
			}
		}
	}
	before := len(commands.snapshot())
	e.exec(`UPDATE tunnel SET status = 0 WHERE id = 10`)
	_ = e.h.redeployTunnelRuntimeOnNode(10, 1)
	if len(commands.snapshot()) != before {
		t.Fatal("disabled tunnel sent runtime commands")
	}
}

func TestTunnelRedeploySchedulerCoalescesRapidReconnects(t *testing.T) {
	const delay = 25 * time.Millisecond
	runs := make(chan int64, 10)
	scheduler := newNodeRedeployScheduler(delay, func(nodeID int64) { runs <- nodeID }, func(int64) bool { return true })
	defer scheduler.Stop()
	for i := 0; i < 5; i++ {
		scheduler.Offline(1)
		scheduler.Online(1)
	}
	select {
	case nodeID := <-runs:
		if nodeID != 1 {
			t.Fatalf("wrong node %d", nodeID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced reconnect never ran")
	}
	select {
	case <-runs:
		t.Fatal("rapid reconnects triggered multiple redeploys")
	case <-time.After(4 * delay):
	}
}

func TestTunnelRedeploySchedulerReconnectDuringRunHasOneFollowup(t *testing.T) {
	const delay = 25 * time.Millisecond
	started := make(chan int, 10)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var count, active, peak atomic.Int32
	scheduler := newNodeRedeployScheduler(delay, func(int64) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer active.Add(-1)
		run := count.Add(1)
		started <- int(run)
		if run == 1 {
			<-release
		}
	}, func(int64) bool { return true })
	defer scheduler.Stop()
	defer releaseOnce.Do(func() { close(release) })
	scheduler.Online(1)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("initial run never started")
	}
	for i := 0; i < 5; i++ {
		scheduler.Offline(1)
		scheduler.Online(1)
	}
	select {
	case <-started:
		t.Fatal("node redeploys overlapped")
	case <-time.After(3 * delay):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case run := <-started:
		if run != 2 {
			t.Fatalf("unexpected followup %d", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect during run lost its followup")
	}
	select {
	case <-started:
		t.Fatal("reconnects during run scheduled more than one followup")
	case <-time.After(4 * delay):
	}
	if peak.Load() != 1 {
		t.Fatalf("same-node peak concurrency = %d", peak.Load())
	}
}

func TestTunnelRedeploySchedulerRequiresContinuousOnlineWindow(t *testing.T) {
	const delay = 40 * time.Millisecond
	runs := make(chan struct{}, 10)
	var online atomic.Bool
	online.Store(true)
	scheduler := newNodeRedeployScheduler(delay, func(int64) { runs <- struct{}{} }, func(int64) bool { return online.Load() })
	defer scheduler.Stop()
	scheduler.Online(1)
	online.Store(false)
	scheduler.Offline(1)
	select {
	case <-runs:
		t.Fatal("offline node was redeployed")
	case <-time.After(2 * delay):
	}
	online.Store(true)
	scheduler.Online(1)
	select {
	case <-runs:
	case <-time.After(2 * time.Second):
		t.Fatal("stable online node never redeployed")
	}
}

func TestTunnelRedeployRetryHealsOnlineNodeAndDropsSuccess(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	var fail atomic.Bool
	fail.Store(true)
	commands.fail = func(nodeID int64, method string) error {
		if fail.Load() && nodeID == 1 && method == "AddChains" {
			return errors.New("等待节点响应超时")
		}
		return nil
	}
	e.h.redeployNodeRuntime(1)
	if commands.count(1, "AddChains") != 1 {
		t.Fatal("initial failure not exercised")
	}
	e.exec(`UPDATE node SET status = 0 WHERE id = 1`)
	now := time.Now().Add(2 * time.Minute)
	e.h.retryPendingTunnelNodes(now)
	if commands.count(1, "AddChains") != 1 {
		t.Fatal("retry attempted an offline node")
	}
	fail.Store(false)
	e.exec(`UPDATE node SET status = 1 WHERE id = 1`)
	e.h.retryPendingTunnelNodes(now.Add(time.Minute))
	if commands.count(1, "AddChains") != 2 {
		t.Fatalf("online node did not heal: %+v", commands.snapshot())
	}
	e.h.retryPendingTunnelNodes(now.Add(time.Hour))
	if commands.count(1, "AddChains") != 2 {
		t.Fatal("successful retry remained pending")
	}
	for _, command := range commands.snapshot() {
		if command.nodeID != 1 {
			t.Fatalf("retry touched healthy node %d", command.nodeID)
		}
	}
}

func TestTunnelRedeployRetryDropsStaleMembership(t *testing.T) {
	for _, change := range []string{
		`DELETE FROM tunnel WHERE id = 10`,
		`UPDATE tunnel SET status = 0 WHERE id = 10`,
		`DELETE FROM chain_tunnel WHERE tunnel_id = 10 AND node_id = 1`,
	} {
		t.Run(change, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			e.h.recordTunnelRuntimeResult(10, 1, errors.New("push failed"))
			e.exec(change)
			e.h.retryPendingTunnelNodes(time.Now().Add(2 * time.Minute))
			if len(commands.snapshot()) != 0 {
				t.Fatalf("stale retry pushed commands: %+v", commands.snapshot())
			}
			e.h.redeployRetryMu.Lock()
			pending := len(e.h.redeployPending)
			e.h.redeployRetryMu.Unlock()
			if pending != 0 {
				t.Fatalf("stale retry was retained: %d", pending)
			}
		})
	}
}

func TestTunnelRedeployRetryBackoffIsBounded(t *testing.T) {
	e, commands := newTunnelRedeployEnv(t)
	commands.fail = func(_ int64, method string) error {
		if method == "AddChains" {
			return errors.New("retry push rejected")
		}
		return nil
	}
	e.h.recordTunnelRuntimeResult(10, 1, errors.New("initial push rejected"))
	key := tunnelNodeKey{tunnelID: 10, nodeID: 1}
	for attempt, wantDelay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		e.h.redeployRetryMu.Lock()
		pending := *e.h.redeployPending[key]
		e.h.redeployRetryMu.Unlock()
		if pending.delay != wantDelay {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, pending.delay, wantDelay)
		}
		before := commands.count(1, "AddChains")
		e.h.retryPendingTunnelNodes(pending.next.Add(-time.Millisecond))
		if commands.count(1, "AddChains") != before {
			t.Fatalf("attempt %d ignored its backoff", attempt)
		}
		e.h.retryPendingTunnelNodes(pending.next.Add(time.Millisecond))
		if commands.count(1, "AddChains") != before+1 {
			t.Fatalf("attempt %d did not retry after its backoff", attempt)
		}
	}
}

func redeployMutationRequest(t *testing.T, handler http.HandlerFunc, body interface{}) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	handler(w, r)
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %s: %v", w.Body.String(), err)
	}
	return response.Code, response.Msg
}

func TestTunnelRedeployCreateUpdateKeepStrictRollback(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, failure := range []struct {
			name     string
			message  string
			wantFail bool
		}{{"rejected", "injected apply failure", true}, {"transient", "等待节点响应超时", false}} {
			t.Run(operation+"/"+failure.name, func(t *testing.T) {
				e, commands := newTunnelRedeployEnv(t)
				commands.fail = func(nodeID int64, method string) error {
					if nodeID == 3 && method == "AddService" {
						return errors.New(failure.message)
					}
					return nil
				}
				body := map[string]interface{}{
					"name": "strict-tunnel", "type": 2, "status": 1,
					"inNodeId":   []map[string]interface{}{{"nodeId": 1, "protocol": "tls"}},
					"chainNodes": [][]map[string]interface{}{{{"nodeId": 2, "port": 1302, "protocol": "tls"}}},
					"outNodeId":  []map[string]interface{}{{"nodeId": 3, "port": 1303, "protocol": "tls"}, {"nodeId": 4, "port": 1304, "protocol": "tls"}},
				}
				fn := e.h.tunnelCreate
				if operation == "update" {
					body["id"] = 10
					fn = e.h.tunnelUpdate
				}
				code, message := redeployMutationRequest(t, fn, body)
				if (code != 0) != failure.wantFail {
					t.Fatalf("existing strict/deferred response changed: code %d message %q", code, message)
				}
				if failure.wantFail && !strings.Contains(message, failure.message) {
					t.Fatalf("apply failure not exposed to user: %q", message)
				}
				if commands.count(3, "AddService") != 1 || commands.count(4, "AddService") != 0 {
					t.Fatalf("strict apply did not stop at first failed exit: %+v", commands.snapshot())
				}
				// Ignore update's pre-apply cleanup; inspect only commands after the failed add.
				failedAt := -1
				all := commands.snapshot()
				for i, command := range all {
					if command.nodeID == 3 && command.method == "AddService" {
						failedAt = i
						break
					}
				}
				var rollbackEntry, rollbackHopChain, rollbackHopService bool
				for _, command := range all[failedAt+1:] {
					rollbackEntry = rollbackEntry || command.nodeID == 1 && command.method == "DeleteChains"
					rollbackHopChain = rollbackHopChain || command.nodeID == 2 && command.method == "DeleteChains"
					rollbackHopService = rollbackHopService || command.nodeID == 2 && command.method == "DeleteService"
				}
				if !rollbackEntry || !rollbackHopChain || !rollbackHopService {
					t.Fatalf("strict apply did not rollback successful pushes: %+v", all)
				}
				if operation == "create" {
					wantRows := 1
					if failure.wantFail {
						wantRows = 0
					}
					if rows := mustQueryInt(t, e.r, `SELECT count(*) FROM tunnel WHERE name = 'strict-tunnel'`); rows != wantRows {
						t.Fatalf("create persistence semantics changed: rows = %d, want %d", rows, wantRows)
					}
				}
			})
		}
	}
}

func TestTunnelRedeploySerializesSameTunnelRuntime(t *testing.T) {
	for _, secondPath := range []string{"node", "full"} {
		t.Run(secondPath, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			blocked := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			commands.fail = func(_ int64, _ string) error {
				once.Do(func() {
					close(blocked)
					<-release
				})
				return nil
			}
			firstDone := make(chan error, 1)
			go func() { firstDone <- e.h.redeployTunnelAndForwards(10) }()
			select {
			case <-blocked:
			case <-time.After(2 * time.Second):
				t.Fatal("first runtime apply never reached command sender")
			}
			secondStarted := make(chan struct{})
			secondDone := make(chan error, 1)
			go func() {
				close(secondStarted)
				if secondPath == "node" {
					secondDone <- e.h.redeployTunnelRuntimeOnNode(10, 1)
				} else {
					secondDone <- e.h.redeployTunnelAndForwards(10)
				}
			}()
			<-secondStarted
			select {
			case err := <-secondDone:
				t.Fatalf("second runtime escaped serialization: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if len(commands.snapshot()) != 1 {
				t.Fatalf("same-tunnel commands interleaved: %+v", commands.snapshot())
			}
			releaseOnce.Do(func() { close(release) })
			for _, done := range []<-chan error{firstDone, secondDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("runtime apply deadlocked")
				}
			}
		})
	}
}

func TestTunnelRedeployRetryHealsFailedForwardOnItsEntryOnly(t *testing.T) {
	for _, mode := range []string{"gost", "nftables"} {
		t.Run(mode, func(t *testing.T) {
			e, commands := newTunnelRedeployEnv(t)
			addRedeployForwards(e, mode)
			var fail atomic.Bool
			fail.Store(true)
			commands.fail = func(nodeID int64, method string) error {
				if nodeID == 1 && fail.Load() && (method == "UpdateService" || method == "AddService" || method == "AddNftablesRules") {
					return errors.New("等待节点响应超时")
				}
				return nil
			}
			e.h.redeployNodeRuntime(1)
			method := "UpdateService"
			if mode == "nftables" {
				method = "AddNftablesRules"
			}
			if commands.count(1, method) != 1 {
				t.Fatalf("forward failure not exercised: %+v", commands.snapshot())
			}
			fail.Store(false)
			e.h.retryPendingTunnelNodes(time.Now().Add(2 * time.Minute))
			if commands.count(1, method) != 2 {
				t.Fatalf("failed forward did not self-heal: %+v", commands.snapshot())
			}
			e.h.retryPendingTunnelNodes(time.Now().Add(time.Hour))
			if commands.count(1, method) != 2 {
				t.Fatal("successful forward retry remained pending")
			}
			for _, command := range commands.snapshot() {
				if command.nodeID != 1 {
					t.Fatalf("forward retry touched unrelated node %d", command.nodeID)
				}
			}
		})
	}
}

func TestTunnelRedeploySchedulerIgnoresStaleOfflineNotification(t *testing.T) {
	runs := make(chan int64, 1)
	scheduler := newNodeRedeployScheduler(25*time.Millisecond, func(nodeID int64) { runs <- nodeID }, func(int64) bool { return true })
	defer scheduler.Stop()
	// A delayed old-session disconnect arrives after the replacement session is online.
	scheduler.Online(1)
	scheduler.Offline(1)
	select {
	case nodeID := <-runs:
		if nodeID != 1 {
			t.Fatalf("redeployed wrong node %d", nodeID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale disconnect cancelled the new session's redeploy")
	}
}
