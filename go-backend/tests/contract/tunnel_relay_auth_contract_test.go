package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/store/model"
)

// Plan 225: tunnel relay auth. Mock agents record every command with its
// payload and a global sequence number.

type relayAuthCmd struct {
	seq  int
	node int64
	typ  string
	data json.RawMessage
}

type relayAuthLab struct {
	*splitLab
	mu   sync.Mutex
	seq  int
	log  []relayAuthCmd
	user string
}

func newRelayAuthLab(t *testing.T, dialect string) *relayAuthLab {
	l := &relayAuthLab{splitLab: newSplitLab(t, dialect)}
	// Second recorder per node: payloads in global order (the split lab only
	// keeps names per node).
	for _, id := range []int64{splitEntryA, splitEntryB, splitHop1, splitHop2a, splitHop2b, splitExit} {
		id := id
		stop := startMockNodeSessionWithCommandRecorder(t, l.srv.URL, fmt.Sprintf("split-node-%d", id), func(cmd string, data json.RawMessage) (bool, string) {
			switch cmd {
			case "ListServices", "GetServiceClientIPs", "TcpPing", "GetCurrentConns", "ListNftablesRules":
				return false, ""
			}
			l.mu.Lock()
			l.seq++
			l.log = append(l.log, relayAuthCmd{seq: l.seq, node: id, typ: cmd, data: append(json.RawMessage(nil), data...)})
			l.mu.Unlock()
			l.record(id, cmd, data)
			return false, ""
		})
		t.Cleanup(stop)
	}
	time.Sleep(300 * time.Millisecond)
	l.quiet()
	l.clear()
	now := time.Now().UnixMilli()
	u := model.User{User: "ra-user", Pwd: "x", RoleID: 1, ExpTime: time.Now().Add(24 * time.Hour).UnixMilli(), Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1}
	if err := l.r.DB().Create(&u).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
	l.user, _ = auth.GenerateToken(u.ID, "ra-user", 1, l.secret)
	return l
}

func (l *relayAuthLab) clear() {
	l.quiet()
	l.mu.Lock()
	l.log = nil
	l.mu.Unlock()
	l.reset()
}

func (l *relayAuthLab) cmds() []relayAuthCmd {
	l.quiet()
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]relayAuthCmd(nil), l.log...)
}

type raAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// chainAuths: connector auths of chains_<tid> in an AddChains/UpdateChains payload.
func chainAuths(c relayAuthCmd, tid int64) []raAuth {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(c.data, &payload)
	raw := c.data
	if d, ok := payload["data"]; ok && c.typ == "UpdateChains" {
		raw = d
	}
	var chain struct {
		Name string `json:"name"`
		Hops []struct {
			Nodes []struct {
				Connector struct {
					Auth *raAuth `json:"auth"`
				} `json:"connector"`
			} `json:"nodes"`
		} `json:"hops"`
	}
	_ = json.Unmarshal(raw, &chain)
	if chain.Name != fmt.Sprintf("chains_%d", tid) {
		return nil
	}
	out := []raAuth{}
	for _, h := range chain.Hops {
		for _, n := range h.Nodes {
			if n.Connector.Auth == nil {
				out = append(out, raAuth{})
			} else {
				out = append(out, *n.Connector.Auth)
			}
		}
	}
	return out
}

type raService struct {
	Name    string `json:"name"`
	Handler struct {
		Type     string                 `json:"type"`
		Auth     *raAuth                `json:"auth"`
		Metadata map[string]interface{} `json:"metadata"`
	} `json:"handler"`
}

func relayServices(c relayAuthCmd, tid int64) []raService {
	if c.typ != "AddService" && c.typ != "UpdateService" {
		return nil
	}
	var list []raService
	if err := json.Unmarshal(c.data, &list); err != nil {
		var wrapped struct {
			Services []raService `json:"services"`
		}
		_ = json.Unmarshal(c.data, &wrapped)
		list = wrapped.Services
	}
	out := []raService{}
	for _, s := range list {
		if s.Name == fmt.Sprintf("%d_tls", tid) {
			out = append(out, s)
		}
	}
	return out
}

func TestTunnelRelayAuthContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			l := newRelayAuthLab(t, dialect)

			// 1. A new tunnel: connectors send, relays require, exit guards.
			l.ok(l.admin, "/api/v1/tunnel/create", multiEntryTunnelPayload("ra-multi", splitEntryA, splitEntryB))
			tid := l.tunnelID("ra-multi")
			if got := l.tunnel(tid).RelayAuth; got != 1 {
				t.Fatalf("new tunnel relay_auth=%d", got)
			}
			var cred raAuth
			chainsSeen, servicesSeen := 0, 0
			for _, c := range l.cmds() {
				for _, a := range chainAuths(c, tid) {
					chainsSeen++
					if a.Username != fmt.Sprintf("flvx-t%d", tid) || len(a.Password) != 32 {
						t.Fatalf("node %d connector auth %+v", c.node, a)
					}
					if cred.Password == "" {
						cred = a
					} else if a != cred {
						t.Fatalf("connector credentials differ inside one tunnel")
					}
				}
			}
			for _, c := range l.cmds() {
				for _, s := range relayServices(c, tid) {
					servicesSeen++
					if s.Handler.Auth == nil || *s.Handler.Auth != cred {
						t.Fatalf("node %d relay without the credential: %+v", c.node, s.Handler.Auth)
					}
					guard, _ := s.Handler.Metadata["flvx.targetGuard"].(bool)
					if guard != (c.node == splitExit) {
						t.Fatalf("node %d targetGuard=%v", c.node, guard)
					}
				}
			}
			if chainsSeen == 0 || servicesSeen != 4 {
				t.Fatalf("chains=%d services=%d", chainsSeen, servicesSeen)
			}

			// 2. Switch off (rollback path): relays lose auth, connectors keep it.
			l.clear()
			res := l.ok(l.admin, "/api/v1/tunnel/relay-auth", map[string]interface{}{"tunnelIds": []int64{tid}, "enforce": false})
			if got := l.tunnel(tid).RelayAuth; got != 0 {
				t.Fatalf("off: relay_auth=%d", got)
			}
			off := 0
			for _, c := range l.cmds() {
				for _, s := range relayServices(c, tid) {
					off++
					if s.Handler.Auth != nil || s.Handler.Metadata["flvx.targetGuard"] != nil {
						t.Fatalf("off: node %d still requires auth", c.node)
					}
				}
				for _, a := range chainAuths(c, tid) {
					if a != cred {
						t.Fatalf("off: connector lost the credential on node %d", c.node)
					}
				}
			}
			if off != 4 {
				t.Fatalf("off: %d relay pushes, want 4 (%v)", off, res.Data)
			}

			// 3. Ordered switch on: every connector push happens before the
			//    first relay that requires the credential.
			l.clear()
			l.ok(l.admin, "/api/v1/tunnel/relay-auth", map[string]interface{}{"tunnelIds": []int64{tid}, "enforce": true})
			if got := l.tunnel(tid).RelayAuth; got != 1 {
				t.Fatalf("on: relay_auth=%d", got)
			}
			firstAuthRelay, lastChain := 1<<30, 0
			upstream := map[int64]bool{}
			for _, c := range l.cmds() {
				if len(chainAuths(c, tid)) > 0 {
					upstream[c.node] = true
					if c.seq > lastChain {
						lastChain = c.seq
					}
				}
				for _, s := range relayServices(c, tid) {
					if s.Handler.Auth != nil && c.seq < firstAuthRelay {
						firstAuthRelay = c.seq
					}
				}
			}
			for _, n := range []int64{splitEntryA, splitEntryB, splitHop1, splitHop2a, splitHop2b} {
				if !upstream[n] {
					t.Fatalf("on: connector of node %d not confirmed before enforcing", n)
				}
			}
			if firstAuthRelay == 1<<30 || firstAuthRelay < lastChain {
				// Middle hops get their (unchanged) chain again inside their own
				// relay push; only the pre-flag connector round must be earlier.
				pre := 0
				for _, c := range l.cmds() {
					if len(chainAuths(c, tid)) > 0 && c.seq < firstAuthRelay {
						pre++
					}
				}
				if firstAuthRelay == 1<<30 || pre < 5 {
					t.Fatalf("on: auth relay at seq %d before connectors (pre=%d)", firstAuthRelay, pre)
				}
			}

			// 4. The credential never appears in an API response.
			for _, call := range []struct {
				token, path string
				body        interface{}
			}{
				{l.admin, "/api/v1/tunnel/list", map[string]interface{}{}},
				{l.admin, "/api/v1/tunnel/get", map[string]interface{}{"id": tid}},
				{l.admin, "/api/v1/tunnel/relay-auth", map[string]interface{}{"tunnelIds": []int64{tid}}},
				{l.admin, "/api/v1/tunnel/relay-auth", map[string]interface{}{"all": true, "enforce": true}},
				{l.admin, "/api/v1/node/list", map[string]interface{}{}},
				{l.admin, "/api/v1/forward/list", map[string]interface{}{}},
				{l.admin, "/api/v1/tunnel/user/tunnel", map[string]interface{}{}},
				{l.user, "/api/v1/tunnel/user/tunnel", map[string]interface{}{}},
				{l.user, "/api/v1/forward/list", map[string]interface{}{}},
			} {
				raw, _ := json.Marshal(call.body)
				req := httptest.NewRequest(http.MethodPost, call.path, bytes.NewReader(raw))
				req.Header.Set("Authorization", call.token)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				l.router.ServeHTTP(rec, req)
				if strings.Contains(rec.Body.String(), cred.Password) {
					t.Fatalf("%s leaks the relay credential", call.path)
				}
			}

			// 5. Non-admins cannot use the rollout API.
			if out := l.call(l.user, "/api/v1/tunnel/relay-auth", map[string]interface{}{"all": true, "enforce": false}); out.Code == 0 {
				t.Fatalf("non-admin relay-auth accepted")
			}
			if got := l.tunnel(tid).RelayAuth; got != 1 {
				t.Fatalf("non-admin changed relay_auth to %d", got)
			}

			// 6. An existing tunnel stays at 0 until the rollout switches it.
			var legacy model.Tunnel
			l.r.DB().Where("id = ?", tid).First(&legacy)
			l.r.DB().Model(&model.Tunnel{}).Where("id = ?", tid).Update("relay_auth", 0)
			l.clear()
			l.ok(l.admin, "/api/v1/tunnel/update", map[string]interface{}{"id": tid, "name": "ra-multi", "type": 2, "flow": 2, "trafficRatio": 1.5, "status": 1,
				"inNodeId": multiEntryTunnelPayload("x", splitEntryA, splitEntryB)["inNodeId"], "chainNodes": multiEntryTunnelPayload("x", splitEntryA)["chainNodes"],
				"outNodeId": multiEntryTunnelPayload("x", splitEntryA)["outNodeId"], "http": 1, "tls": 1, "socks": 1})
			if got := l.tunnel(tid).RelayAuth; got != 0 {
				t.Fatalf("tunnel edit changed relay_auth to %d", got)
			}
			for _, c := range l.cmds() {
				for _, s := range relayServices(c, tid) {
					if s.Handler.Auth != nil {
						t.Fatalf("edit of a not-yet-switched tunnel pushed an auth relay to node %d", c.node)
					}
				}
			}
		})
	}
}
