package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/auth"
	"go-backend/internal/security"
)

// pausedMockAgent is a stateful mock agent for plan 236. Its services survive a
// "restart" (gost.json); an old agent (fork.124 and earlier) forgets which of
// them were paused — they listen again —, a plan-236 agent keeps them closed
// and lists them under "paused".
type pausedMockAgent struct {
	newAgent bool
	mu       sync.Mutex
	services map[string]bool // name -> paused
	log      []string        // "<session>:<command>"
	session  int
}

func (a *pausedMockAgent) handle(cmd string, data json.RawMessage) (bool, string, map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.log = append(a.log, fmt.Sprintf("%d:%s", a.session, cmd))
	var list struct {
		Services []string `json:"services"`
	}
	switch cmd {
	case "ListServices":
		names, paused := []string{}, []string{}
		for name, p := range a.services {
			names = append(names, name)
			if p && a.newAgent {
				paused = append(paused, name)
			}
		}
		sort.Strings(names)
		sort.Strings(paused)
		inv := map[string]interface{}{"services": names, "chains": []string{}, "hops": []string{}, "limiters": []string{}, "nftables": []string{}, "wgPaths": []string{}, "wgForwards": []string{}}
		if a.newAgent {
			inv["paused"] = paused
		}
		return true, "", inv
	case "AddService", "UpdateService":
		var svcs []struct {
			Name     string                 `json:"name"`
			Metadata map[string]interface{} `json:"metadata"`
		}
		_ = json.Unmarshal(data, &svcs)
		for _, s := range svcs {
			p, _ := s.Metadata["paused"].(bool)
			// An old agent serves whatever it gets (the panel pauses after).
			a.services[s.Name] = p && a.newAgent
		}
	case "PauseService", "ResumeService", "DeleteService":
		_ = json.Unmarshal(data, &list)
		for _, name := range list.Services {
			if _, ok := a.services[name]; !ok {
				return false, "service " + name + " not found", nil
			}
		}
		for _, name := range list.Services {
			switch cmd {
			case "PauseService":
				a.services[name] = true
			case "ResumeService":
				a.services[name] = false
			default:
				delete(a.services, name)
			}
		}
	}
	return true, "", nil
}

func (a *pausedMockAgent) restart() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.session++
	if !a.newAgent {
		for name := range a.services {
			a.services[name] = false
		}
	}
}

func (a *pausedMockAgent) running() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for name, p := range a.services {
		if !p {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (a *pausedMockAgent) count(cmd string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, l := range a.log {
		if l == fmt.Sprintf("%d:%s", a.session, cmd) {
			n++
		}
	}
	return n
}

// connect opens one agent session that answers with data (ListServices).
func (a *pausedMockAgent) connect(t *testing.T, baseURL, nodeSecret string) func() {
	t.Helper()
	u, _ := url.Parse(baseURL)
	u.Scheme = "ws"
	u.Path = "/system-info"
	q := u.Query()
	for k, v := range map[string]string{"type": "1", "secret": nodeSecret, "version": "v1", "http": "1", "tls": "1", "socks": "1"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial mock agent: %v", err)
	}
	ponged := installMockNodePongWait(conn)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			plain := raw
			var wrap struct {
				Encrypted bool   `json:"encrypted"`
				Data      string `json:"data"`
			}
			if json.Unmarshal(raw, &wrap) == nil && wrap.Encrypted && wrap.Data != "" {
				if c, err := security.NewAESCrypto(nodeSecret); err == nil {
					if dec, err := c.Decrypt(wrap.Data); err == nil {
						plain = []byte(dec)
					}
				}
			}
			var cmd struct {
				Type      string          `json:"type"`
				RequestID string          `json:"requestId"`
				Data      json.RawMessage `json:"data"`
			}
			if json.Unmarshal(plain, &cmd) != nil || strings.TrimSpace(cmd.RequestID) == "" {
				continue
			}
			ok, msg, data := a.handle(strings.TrimSpace(cmd.Type), cmd.Data)
			resp := map[string]interface{}{"type": cmd.Type + "Response", "success": ok, "message": "OK", "requestId": cmd.RequestID}
			if !ok {
				resp["message"] = msg
			}
			if data != nil {
				resp["data"] = data
			}
			b, _ := json.Marshal(resp)
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = conn.Close()
			wg.Wait()
		})
	}
	awaitMockNodeRegistered(t, conn, ponged, stop)
	return stop
}

// Plan 236 (P0, 11-07): a paused rule must stay paused when its agent
// restarts. Old agents re-open paused services on restart → the panel
// re-pauses them in one batched PauseService right after the reconnect;
// plan-236 agents keep them closed → nothing is sent. Resume still works.
func TestPausedRuleStaysPausedAcrossAgentRestartContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		newAgent bool
	}{{"old-agent", false}, {"plan236-agent", true}} {
		t.Run(tc.name, func(t *testing.T) {
			secret := "contract-jwt-secret"
			router, r := setupContractRouter(t, secret)
			adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UnixMilli()
			if err := r.DB().Exec(`INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "pr-tunnel", 1.0, 1, "tls", 2, now, now, 1, nil, 0).Error; err != nil {
				t.Fatal(err)
			}
			tunnelID := mustLastInsertID(t, r, "pr-tunnel")
			nodeSecret := "pr-node-secret-" + tc.name
			if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "pr-node", nodeSecret, "10.36.0.1", "10.36.0.1", "", "42000-42010", "", "v1", 1, 1, 1, now, now, 1, "[::]", "[::]", 0).Error; err != nil {
				t.Fatal(err)
			}
			nodeID := mustLastInsertID(t, r, "pr-node")
			if err := r.DB().Exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol) VALUES(?, 1, ?, 42001, 'round', 1, 'tls')`, tunnelID, nodeID).Error; err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(router)
			defer server.Close()
			agent := &pausedMockAgent{newAgent: tc.newAgent, services: map[string]bool{}}
			stop := agent.connect(t, server.URL, nodeSecret)

			post := func(path string, body map[string]interface{}) {
				t.Helper()
				b, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
				req.Header.Set("Authorization", adminToken)
				req.Header.Set("Content-Type", "application/json")
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				assertCode(t, res, 0)
			}
			var ids []int64
			for i, target := range []string{"1.1.1.1:443", "8.8.8.8:443"} {
				name := fmt.Sprintf("pr-rule-%d", i)
				post("/api/v1/forward/create", map[string]interface{}{"name": name, "tunnelId": tunnelID, "remoteAddr": target, "strategy": "fifo"})
				ids = append(ids, mustLastInsertID(t, r, name))
			}
			deployed := agent.running()
			if len(deployed) != 4 {
				t.Fatalf("deployed services %v", deployed)
			}
			for _, id := range ids {
				post("/api/v1/forward/pause", map[string]interface{}{"id": id})
				if st := mustQueryInt(t, r, `SELECT status FROM forward WHERE id = ?`, id); st != 0 {
					t.Fatalf("status %d after pause", st)
				}
			}
			if got := agent.running(); len(got) != 0 {
				t.Fatalf("running after pause: %v", got)
			}

			// Agent restart (OTA): the session drops, the process comes back.
			stop()
			agent.restart()
			if !tc.newAgent && len(agent.running()) != 4 {
				t.Fatal("mock old agent did not re-open the paused rules")
			}
			stop = agent.connect(t, server.URL, nodeSecret)
			defer func() { stop() }()

			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) && (len(agent.running()) != 0 || agent.count("ListServices") == 0) {
				time.Sleep(50 * time.Millisecond)
			}
			time.Sleep(500 * time.Millisecond) // no late extra commands
			if got := agent.running(); len(got) != 0 {
				t.Fatalf("paused rules running after the reconnect: %v", got)
			}
			wantPause := 1
			if tc.newAgent {
				wantPause = 0
			}
			if n := agent.count("PauseService"); n != wantPause {
				t.Fatalf("PauseService after reconnect = %d, want %d (batched)", n, wantPause)
			}
			if agent.count("UpdateService")+agent.count("AddService")+agent.count("ResumeService") != 0 {
				t.Fatal("reconnect pushed a paused rule")
			}
			for _, id := range ids {
				if st := mustQueryInt(t, r, `SELECT status FROM forward WHERE id = ?`, id); st != 0 {
					t.Fatalf("status %d after reconnect", st)
				}
			}

			// Resume after the restart works.
			post("/api/v1/forward/resume", map[string]interface{}{"id": ids[0]})
			if got := agent.running(); len(got) != 2 || !strings.HasPrefix(got[0], fmt.Sprintf("%d_", ids[0])) {
				t.Fatalf("running after resume: %v", got)
			}
		})
	}
}
