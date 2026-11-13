package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 252: idle chain-only exit landings are removed with their Core.

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) lines(substr string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range strings.Split(s.b.String(), "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

func captureChainHostLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
}

// newChainHostEnv: the chain env with exits 3, 4, 6 on agents that know
// RemoveLandingCore (landing_chain 3).
func newChainHostEnv(t *testing.T) (*flowTestEnv, *chainAgent) {
	t.Helper()
	e, a := newChainEnv(t)
	e.exec("UPDATE node SET landing_chain = 3 WHERE id IN (3, 4, 6)")
	return e, a
}

func chainHostServer(t *testing.T, e *flowTestEnv, nodeID int64) *model.LandingServer {
	t.Helper()
	s, err := e.r.GetLandingServerForNode(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *chainAgent) removedCount(nodeID int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.removed[nodeID]
}

func (a *chainAgent) setRemoveFn(fn func(nodeID, revision int64) (ws.CommandResult, error, bool)) {
	a.mu.Lock()
	a.removeFn = fn
	a.mu.Unlock()
}

// sweepGrace runs the two sweeps a removal needs: one notes the server idle,
// one after the grace period acts.
func sweepGrace(e *flowTestEnv, t0 time.Time) {
	e.h.sweepChainHosts(context.Background(), t0)
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace))
}

func TestChainHostCleanupAfterRuleDeleteAndRecreate(t *testing.T) {
	e, a := newChainHostEnv(t)
	logs := captureChainHostLog(t)
	c := createChain(t, e, 20, "own", testSSLink, "key-host-0001")
	first := chainHostServer(t, e, 6)
	if first == nil || !first.ChainOnly {
		t.Fatalf("chain-only server not created: %+v", first)
	}
	if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	expectNoChainOnExits(t, e, a, 6)
	t0 := time.Now()
	e.h.sweepChainHosts(context.Background(), t0)
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace-time.Second))
	if a.count(6, "RemoveLandingCore") != 0 || chainHostServer(t, e, 6) == nil {
		t.Fatal("removed before the grace period")
	}
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace))
	if a.removedCount(6) != 1 || chainHostServer(t, e, 6) != nil {
		t.Fatalf("not removed: commands=%d server=%+v", a.count(6, "RemoveLandingCore"), chainHostServer(t, e, 6))
	}
	if got := logs.lines("[chain-host]"); len(got) != 1 || !strings.Contains(got[0], "removed idle chain-only landing server") || !strings.Contains(got[0], "AkariSG") {
		t.Fatalf("log lines = %q", got)
	}
	// Later sweeps: nothing left, nothing sent, nothing logged.
	e.h.sweepChainHosts(context.Background(), t0.Add(3*chainHostGrace))
	if a.count(6, "RemoveLandingCore") != 1 || len(logs.lines("[chain-host]")) != 1 {
		t.Fatal("repeat sweep acted again")
	}
	// The next 「我自己的节点」 rule on that exit recreates everything.
	again := createChain(t, e, 20, "own-again", testSSLink, "key-host-0002")
	second := chainHostServer(t, e, 6)
	if again.State != model.ChainStateActive || second == nil || !second.ChainOnly || second.ID == first.ID || second.Status != "active" || second.CoreCPUPercent != repo.ChainOnlyCoreCPUPercent {
		t.Fatalf("recreate: chain=%+v server=%+v", again, second)
	}
	if nodes := a.chainNodes(6); len(nodes) != 1 {
		t.Fatalf("recreated exit runs %d chain nodes", len(nodes))
	}
}

// Every path that removes the last chain copy ends in the same idle state.
func TestChainHostCleanupEveryRemovalPath(t *testing.T) {
	type path struct {
		run     func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain)
		removed []int64 // exits whose chain-only server goes
		kept    []int64
	}
	paths := map[string]path{
		"rule delete": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
				t.Fatal(msg)
			}
		}, removed: []int64{3, 4}},
		"batch delete": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			if code, msg, _ := callAs(t, e.h.forwardBatchDelete, 1, 0, map[string]interface{}{"ids": []int64{c.ForwardID}}); code != 0 {
				t.Fatal(msg)
			}
		}, removed: []int64{3, 4}},
		"force delete": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			if code, msg, _ := callAs(t, e.h.forwardForceDelete, 1, 0, map[string]interface{}{"id": c.ForwardID}); code != 0 {
				t.Fatal(msg)
			}
		}, removed: []int64{3, 4}},
		"user delete": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			if err := e.h.deleteUserAndCleanup(7); err != nil {
				t.Fatal(err)
			}
		}, removed: []int64{3, 4}},
		"tunnel delete": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			if err := e.h.deleteTunnelByID(10); err != nil {
				t.Fatal(err)
			}
		}, removed: []int64{3, 4}},
		"switch to a landing node": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, created_time, updated_time) VALUES (50, 'native', 2, 'land', '192.0.2.2', 'active', 1, 1, 1)`)
			e.exec(`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES (90, 7, 50, 'r', 'vless_reality_vision', 31000, 'flvxl-90', 'active', 1, 1)`)
			e.exec("UPDATE node SET landing_capable = ? WHERE id = 2", true)
			e.exec("INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (50, 7)")
			if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": c.ForwardID, "tunnelId": 10, "targetMode": "node", "landingNodeId": 90}); code != 0 {
				t.Fatal(msg)
			}
			e.h.chains.wg.Wait()
		}, removed: []int64{3, 4}},
		"exit removed from the tunnel": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			e.exec("DELETE FROM chain_tunnel WHERE tunnel_id = 10 AND node_id = 4")
			e.h.reconcileTunnelChains(10)
			e.h.chains.wg.Wait()
		}, removed: []int64{4}, kept: []int64{3}},
		"orphan reconciler": {run: func(t *testing.T, e *flowTestEnv, a *chainAgent, c *model.ForwardChain) {
			e.exec("DELETE FROM forward_port WHERE forward_id = ?", c.ForwardID)
			e.exec("DELETE FROM forward WHERE id = ?", c.ForwardID)
			e.exec("UPDATE forward_chain SET updated_time = ?", time.Now().Add(-time.Hour).UnixMilli())
			e.h.reconcileChains(time.Now())
		}, removed: []int64{3, 4}},
	}
	for name, p := range paths {
		t.Run(name, func(t *testing.T) {
			e, a := newChainHostEnv(t)
			c := createChain(t, e, 10, "own", testSSLink, "key-path-0001")
			p.run(t, e, a, c)
			e.h.chains.wg.Wait()
			sweepGrace(e, time.Now())
			for _, id := range p.removed {
				if chainHostServer(t, e, id) != nil || a.removedCount(id) != 1 {
					t.Fatalf("exit %d: server kept / %d removals", id, a.removedCount(id))
				}
			}
			for _, id := range p.kept {
				if s := chainHostServer(t, e, id); s == nil || a.count(id, "RemoveLandingCore") != 0 {
					t.Fatalf("exit %d still used but cleaned (%+v)", id, s)
				}
			}
		})
	}
}

// A create that failed after its copies were made leaves idle servers too.
func TestChainHostCleanupAfterFailedCreate(t *testing.T) {
	e, a := newChainHostEnv(t)
	a.setApplyErr(func(nodeID int64) error {
		if nodeID == 4 {
			return errors.New("agent down")
		}
		return nil
	})
	code, _, data := callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "fails", testSSLink, "key-fail-0001"))
	e.h.chains.wg.Wait()
	if code == 0 {
		if c, _ := e.r.GetForwardChain(asInt64(data["chainId"], 0)); c != nil && c.State == model.ChainStateActive {
			t.Fatal("create did not fail")
		}
	}
	a.setApplyErr(nil)
	if chainHostServer(t, e, 3) == nil || chainHostServer(t, e, 4) == nil {
		t.Fatal("chain-only servers were not created by the attempt")
	}
	sweepGrace(e, time.Now())
	if chainHostServer(t, e, 3) != nil || chainHostServer(t, e, 4) != nil {
		t.Fatal("servers left by a failed create were not cleaned")
	}
}

// Prod today: Bestvm US (server 9) carries sunflorian's active 「ATTVPS」 on
// 「Dmit Echo -> US(接入Akari/GSL， 接自己落地)」; it and paused rules stay.
func TestChainHostKeepsServersInUse(t *testing.T) {
	e, a := newChainHostEnv(t)
	logs := captureChainHostLog(t)
	active := createChain(t, e, 20, "ATTVPS", testSSLink, "key-keep-0001")
	paused := createChain(t, e, 10, "paused", testSSLink, "key-keep-0002")
	if code, msg, _ := callAs(t, e.h.forwardPause, 7, 1, map[string]interface{}{"id": paused.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	e.h.chains.wg.Wait()
	before := map[int64]model.LandingServer{}
	for _, id := range []int64{3, 4, 6} {
		before[id] = *chainHostServer(t, e, id)
	}
	t0 := time.Now()
	for _, d := range []time.Duration{0, chainHostGrace, 2 * time.Hour, 48 * time.Hour} {
		e.h.sweepChainHosts(context.Background(), t0.Add(d))
	}
	for _, id := range []int64{3, 4, 6} {
		s := chainHostServer(t, e, id)
		if s == nil || s.Revision != before[id].Revision || a.count(id, "RemoveLandingCore") != 0 {
			t.Fatalf("exit %d touched: before=%+v after=%+v", id, before[id], s)
		}
	}
	if got := logs.lines("[chain-host]"); len(got) != 0 {
		t.Fatalf("logged for used servers: %q", got)
	}
	if c, _ := e.r.GetForwardChain(active.ID); c == nil || c.State != model.ChainStateActive {
		t.Fatal("active chain changed")
	}
	// The paused rule comes back with its link unchanged.
	if code, msg, _ := callAs(t, e.h.forwardResume, 7, 1, map[string]interface{}{"id": paused.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	if c, _ := e.r.GetForwardChain(paused.ID); c == nil || c.UUID != paused.UUID || c.Port != paused.Port {
		t.Fatal("resume changed the paused chain")
	}
}

// An idle chain-only server that anything else still references stays; the
// reason is logged once.
func TestChainHostKeepReasons(t *testing.T) {
	cases := map[string]struct {
		setup  string
		reason string
	}{
		"admin disabled":       {"UPDATE landing_server SET status = 'disabled' WHERE id = 60", "已停用"},
		"route profile":        {"UPDATE landing_server SET route_profile_id = 5 WHERE id = 60", "出站路由方案"},
		"landing node":         {`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES (91, 7, 60, 'n', 'ss2022', 35000, 'flvxl-91', 'disabled', 1, 1)`, "落地节点"},
		"user grant":           {"INSERT INTO landing_server_user(landing_server_id, user_id) VALUES (60, 7)", "用户授权"},
		"group grant":          {"INSERT INTO landing_server_user_group(landing_server_id, user_group_id) VALUES (60, 3)", "用户组授权"},
		"warp account":         {"INSERT INTO landing_warp_account(landing_server_id, outbound_id, created_time, updated_time) VALUES (60, 1, 1, 1)", "WARP"},
		"scan event":           {"INSERT INTO landing_scan_event(landing_server_id, landing_node_id, seq, created_time) VALUES (60, 91, 1, 1)", "扫描记录"},
		"user quota":           {"INSERT INTO user_server_quota(user_id, server_kind, server_ref_id, created_time, updated_time) VALUES (7, 'landing', 60, 1, 1)", "用户限额"},
		"traffic pool":         {"INSERT INTO user_traffic_pool_member(pool_id, user_id, server_kind, server_ref_id) VALUES (1, 7, 'landing', 60)", "流量池"},
		"pending chain copy":   {"INSERT INTO forward_chain_node(chain_id, landing_server_id, node_id, port, state, created_time) VALUES (999, 60, 6, 2000, 'pending', 1)", ""},
		"public landing (not chain-only)": {"UPDATE landing_server SET chain_only = ? WHERE id = 60", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, a := newChainHostEnv(t)
			logs := captureChainHostLog(t)
			e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, chain_only, created_time, updated_time) VALUES (60, 'native', 6, 'AkariSG', '192.0.2.6', 'active', 4, ?, 1, 1)`, true)
			if strings.Contains(tc.setup, "chain_only = ?") {
				e.exec(tc.setup, false)
			} else {
				e.exec(tc.setup)
			}
			t0 := time.Now()
			for _, d := range []time.Duration{0, chainHostGrace, chainHostGrace + chainHostSweepEvery, 6 * time.Hour} {
				e.h.sweepChainHosts(context.Background(), t0.Add(d))
			}
			if chainHostServer(t, e, 6) == nil || a.count(6, "RemoveLandingCore") != 0 {
				t.Fatal("used server removed")
			}
			got := logs.lines("[chain-host]")
			if tc.reason == "" {
				if len(got) != 0 {
					t.Fatalf("not a candidate, yet logged: %q", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.reason) {
				t.Fatalf("log = %q, want one line with %q", got, tc.reason)
			}
		})
	}
}

// Exit agents without the command (fork.130 and older: landing_chain ≤ 2)
// get no command, no retry storm, one log line; after their OTA the next
// sweep cleans up.
func TestChainHostOldAgentWaitsForUpgrade(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("landing_chain=%d", version), func(t *testing.T) {
			e, a := newChainHostEnv(t)
			logs := captureChainHostLog(t)
			c := createChain(t, e, 20, "own", testSSLink, "key-old-0001")
			if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
				t.Fatal(msg)
			}
			e.h.chains.wg.Wait()
			e.exec("UPDATE node SET landing_chain = ? WHERE id = 6", version)
			applies := a.count(6, "ApplyLandingDesired")
			t0 := time.Now()
			for i := 0; i <= 12; i++ { // two hours of sweeps
				e.h.sweepChainHosts(context.Background(), t0.Add(time.Duration(i)*chainHostSweepEvery))
			}
			if chainHostServer(t, e, 6) == nil || a.count(6, "RemoveLandingCore") != 0 || a.count(6, "ApplyLandingDesired") != applies {
				t.Fatal("old agent got commands")
			}
			if got := logs.lines("[chain-host]"); len(got) != 1 || !strings.Contains(got[0], "版本过低") {
				t.Fatalf("log = %q", got)
			}
			// OTA: the agent reports landing_chain 3.
			e.exec("UPDATE node SET landing_chain = 3 WHERE id = 6")
			e.h.sweepChainHosts(context.Background(), t0.Add(13*chainHostSweepEvery))
			if chainHostServer(t, e, 6) != nil || a.removedCount(6) != 1 {
				t.Fatal("not cleaned after the upgrade")
			}
		})
	}
}

func TestChainHostOfflineExitWaits(t *testing.T) {
	e, a := newChainHostEnv(t)
	logs := captureChainHostLog(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, chain_only, created_time, updated_time) VALUES (60, 'native', 6, 'AkariSG', '192.0.2.6', 'active', 4, ?, 1, 1)`, true)
	e.exec("UPDATE node SET status = 0 WHERE id = 6")
	t0 := time.Now()
	for i := 0; i < 5; i++ {
		e.h.sweepChainHosts(context.Background(), t0.Add(time.Duration(i)*chainHostGrace))
	}
	if chainHostServer(t, e, 6) == nil || a.count(6, "RemoveLandingCore") != 0 || len(logs.lines("离线")) != 1 {
		t.Fatalf("offline: commands=%d log=%q", a.count(6, "RemoveLandingCore"), logs.lines("[chain-host]"))
	}
	e.exec("UPDATE node SET status = 1 WHERE id = 6")
	e.h.sweepChainHosts(context.Background(), t0.Add(5*chainHostGrace))
	if chainHostServer(t, e, 6) != nil {
		t.Fatal("not cleaned once online")
	}
}

// The exit missed the push after the delete (still runs the copy): the
// agent refuses, the panel pushes the empty state and retries once.
func TestChainHostAgentStillHoldsNodes(t *testing.T) {
	e, a := newChainHostEnv(t)
	c := createChain(t, e, 20, "own", testSSLink, "key-held-0001")
	a.setApplyErr(func(nodeID int64) error {
		if nodeID == 6 {
			return errors.New("link down")
		}
		return nil
	})
	if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	e.h.chains.wg.Wait()
	if len(a.chainNodes(6)) != 1 {
		t.Fatal("test setup: the exit should still hold the copy")
	}
	a.setApplyErr(nil)
	applies := a.count(6, "ApplyLandingDesired")
	sweepGrace(e, time.Now())
	if chainHostServer(t, e, 6) != nil || a.removedCount(6) != 1 || a.count(6, "RemoveLandingCore") != 2 || a.count(6, "ApplyLandingDesired") != applies+1 {
		t.Fatalf("refusal path: removals=%d commands=%d applies=%d→%d", a.removedCount(6), a.count(6, "RemoveLandingCore"), applies, a.count(6, "ApplyLandingDesired"))
	}
}

// No answer (the command may still have run): no push — that would
// reinstall the Core — but a 30 min backoff and an idempotent repeat.
func TestChainHostNoAnswerBacksOff(t *testing.T) {
	e, a := newChainHostEnv(t)
	logs := captureChainHostLog(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, chain_only, created_time, updated_time) VALUES (60, 'native', 6, 'AkariSG', '192.0.2.6', 'active', 4, ?, 1, 1)`, true)
	calls := 0
	a.setRemoveFn(func(nodeID, revision int64) (ws.CommandResult, error, bool) {
		calls++
		if calls == 1 {
			return ws.CommandResult{}, errors.New("等待节点响应超时"), true
		}
		return ws.CommandResult{}, nil, false
	})
	t0 := time.Now()
	sweepGrace(e, t0)
	if chainHostServer(t, e, 6) == nil || calls != 1 || a.count(6, "ApplyLandingDesired") != 0 {
		t.Fatalf("after timeout: calls=%d applies=%d", calls, a.count(6, "ApplyLandingDesired"))
	}
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace+chainHostSweepEvery))
	if calls != 1 {
		t.Fatal("retried inside the backoff")
	}
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace+chainHostRetryAfter))
	if calls != 2 || chainHostServer(t, e, 6) != nil {
		t.Fatalf("not retried after the backoff: calls=%d", calls)
	}
	if got := logs.lines("[chain-host]"); len(got) != 2 || !strings.Contains(got[0], "30 分钟后重试") || !strings.Contains(got[1], "removed") {
		t.Fatalf("log = %q", got)
	}
}

// A chain rule created between the Core removal and the row delete keeps the
// server; its Core is pushed again right away.
func TestChainHostRaceWithNewRule(t *testing.T) {
	e, a := newChainHostEnv(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, chain_only, created_time, updated_time) VALUES (60, 'native', 6, 'AkariSG', '192.0.2.6', 'active', 4, ?, 1, 1)`, true)
	a.setRemoveFn(func(nodeID, revision int64) (ws.CommandResult, error, bool) {
		e.exec("INSERT INTO forward_chain_node(chain_id, landing_server_id, node_id, port, state, created_time) VALUES (999, 60, 6, 2000, 'pending', 1)")
		return ws.CommandResult{}, nil, false
	})
	sweepGrace(e, time.Now())
	if chainHostServer(t, e, 6) == nil || a.removedCount(6) != 1 || a.count(6, "ApplyLandingDesired") != 1 {
		t.Fatalf("race: server=%+v removals=%d applies=%d", chainHostServer(t, e, 6), a.removedCount(6), a.count(6, "ApplyLandingDesired"))
	}
}

// Delete + recreate within the grace period never removes anything.
func TestChainHostGraceAvoidsFlapping(t *testing.T) {
	e, a := newChainHostEnv(t)
	c := createChain(t, e, 20, "own", testSSLink, "key-flap-0001")
	first := chainHostServer(t, e, 6)
	if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	e.h.chains.wg.Wait()
	t0 := time.Now()
	e.h.sweepChainHosts(context.Background(), t0)
	createChain(t, e, 20, "own2", testSSLink, "key-flap-0002")
	e.h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace))
	e.h.sweepChainHosts(context.Background(), t0.Add(3*chainHostGrace))
	if s := chainHostServer(t, e, 6); s == nil || s.ID != first.ID || a.count(6, "RemoveLandingCore") != 0 {
		t.Fatalf("flapped: %+v", s)
	}
}

// The loop: a chain change kicks a sweep soon; with no grace the idle server
// goes at once. No sweep runs before the first period otherwise.
func TestChainHostLoopKick(t *testing.T) {
	e, a := newChainHostEnv(t)
	oldFirst, oldKick, oldGrace := chainHostFirstSweep, chainHostKickDelay, chainHostGrace
	chainHostFirstSweep, chainHostKickDelay, chainHostGrace = time.Hour, 20*time.Millisecond, 0
	defer func() { chainHostFirstSweep, chainHostKickDelay, chainHostGrace = oldFirst, oldKick, oldGrace }()
	ctx, cancel := context.WithCancel(context.Background())
	e.h.jobsWG.Add(1)
	go e.h.runChainHostCleanupLoop(ctx)
	defer func() { cancel(); e.h.jobsWG.Wait() }()
	c := createChain(t, e, 20, "own", testSSLink, "key-loop-0001")
	if code, msg, _ := callAs(t, e.h.forwardDelete, 7, 1, map[string]interface{}{"id": c.ForwardID}); code != 0 {
		t.Fatal(msg)
	}
	e.h.chains.wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for chainHostServer(t, e, 6) != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if chainHostServer(t, e, 6) != nil || a.removedCount(6) != 1 {
		t.Fatal("kick did not clean the idle server")
	}
}

// Repository + sweep on SQLite and PostgreSQL with prod-shaped rows.
func TestChainHostCleanupDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			nodes := []model.Node{
				{ID: 17, Name: "Bestvm US", LandingChain: 3},
				{ID: 18, Name: "Bestvm JP B", LandingChain: 3},
				{ID: 19, Name: "granted", LandingChain: 3},
				{ID: 20, Name: "Nobrand IPLC 沪港", LandingChain: 1},
				{ID: 21, Name: "NexKR", LandingChain: 3},
			}
			for _, n := range nodes {
				n.Secret, n.ServerIP, n.Port, n.CreatedTime, n.Status, n.LandingCapable = fmt.Sprintf("s%d", n.ID), "192.0.2.1", "10000-65535", now, 1, true
				if err := r.DB().Create(&n).Error; err != nil {
					t.Fatal(err)
				}
			}
			server := func(id, node int64, chainOnly bool) *model.LandingServer {
				return &model.LandingServer{ID: id, Source: "native", NodeID: node, Name: "s", PublicHost: "192.0.2.1", PortMin: 30000, PortMax: 39999, FlowType: 2, TrafficRatio: 1, Status: "active", Revision: 69, ChainOnly: chainOnly, CoreCPUPercent: 1000, CreatedTime: now, UpdatedTime: now}
			}
			for _, v := range []interface{}{
				server(9, 17, true), server(10, 18, true), server(11, 19, true), server(12, 20, true), server(5, 21, false),
				&model.User{ID: 74, User: "sunflorian", Pwd: "x", RoleID: 1, ExpTime: now + 86400000, Flow: 99, FlowResetTime: 1, Num: 9, CreatedTime: now, Status: 1},
				&model.Forward{ID: 602, UserID: 74, UserName: "sunflorian", Name: "ATTVPS", TunnelID: 116, RemoteAddr: "127.0.0.1:1024", Strategy: "fifo", Status: 1, CreatedTime: now, UpdatedTime: now},
				&model.ForwardChain{ID: 6, UserID: 74, TunnelID: 116, ForwardID: 602, State: model.ChainStateActive, Port: 1024, Upstream: `{"type":"socks"}`, CreatedTime: now, UpdatedTime: now},
				&model.ForwardChainNode{ID: 7, ChainID: 6, LandingServerID: 9, NodeID: 17, Port: 1024, State: model.ChainNodeActive, CreatedTime: now},
				&model.LandingServerUser{LandingServerID: 11, UserID: 74, CreatedTime: now},
			} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			idle, err := r.IdleChainOnlyServers()
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, s := range idle {
				ids = append(ids, s.ID)
			}
			if fmt.Sprint(ids) != "[10 11 12]" {
				t.Fatalf("idle = %v", ids)
			}
			for id, want := range map[int64]string{9: "仍有「我自己的节点」规则", 10: "", 11: "有用户授权", 5: "不是仅链式落地服务器", 404: "落地服务器已不存在"} {
				if got, err := r.ChainHostKeepReason(id); err != nil || got != want {
					t.Fatalf("keep reason %d = %q %v, want %q", id, got, err, want)
				}
			}
			h := &Handler{repo: r}
			a := newChainAgent()
			h.nodeCommandSender = a.send
			t0 := time.Now()
			h.sweepChainHosts(context.Background(), t0)
			h.sweepChainHosts(context.Background(), t0.Add(chainHostGrace))
			left := map[int64]bool{}
			var all []model.LandingServer
			if err := r.DB().Find(&all).Error; err != nil {
				t.Fatal(err)
			}
			for _, s := range all {
				left[s.ID] = true
			}
			if !left[9] || left[10] || !left[11] || !left[12] || !left[5] {
				t.Fatalf("servers left = %v", left)
			}
			if a.count(18, "RemoveLandingCore") != 1 || a.count(17, "RemoveLandingCore")+a.count(19, "RemoveLandingCore")+a.count(20, "RemoveLandingCore")+a.count(21, "RemoveLandingCore") != 0 {
				t.Fatal("commands went to the wrong exits")
			}
			s9, _ := r.GetLandingServer(9)
			if s9.Revision != 69 || mustCount(t, r, "forward_chain_node") != 1 {
				t.Fatalf("Bestvm US changed: %+v", s9)
			}
			if removed, why, err := r.DeleteIdleChainOnlyServer(9); err != nil || removed || why == "" {
				t.Fatalf("delete of a used server: %v %q %v", removed, why, err)
			}
			if removed, _, err := r.DeleteIdleChainOnlyServer(10); err != nil || !removed {
				t.Fatalf("repeat delete: %v %v", removed, err)
			}
		})
	}
}

func mustCount(t *testing.T, r *repo.Repository, table string) int64 {
	t.Helper()
	var n int64
	if err := r.DB().Table(table).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}
