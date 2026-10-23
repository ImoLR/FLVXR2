package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/auth"
	"go-backend/internal/authstate"
)

// Plan 135: the /system-info WebSocket must never leak node command replies,
// other users' rule data or anything to anonymous clients while the public
// status page is switched off.

type wsMsg struct {
	ID   json.RawMessage `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	raw  string
}

func dialWS(t *testing.T, url string, hdr http.Header) (*websocket.Conn, int) {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		return nil, code
	}
	t.Cleanup(func() { c.Close() })
	return c, http.StatusSwitchingProtocols
}

func mustDial(t *testing.T, url string, hdr http.Header) *websocket.Conn {
	t.Helper()
	c, code := dialWS(t, url, hdr)
	if c == nil {
		t.Fatalf("dial %s: status %d", url, code)
	}
	return c
}

// A gorilla connection is unusable after a read deadline expires, so each
// test client gets one reader goroutine; drain collects what arrives in d.
var collectors sync.Map // *websocket.Conn -> chan wsMsg

func collector(c *websocket.Conn) chan wsMsg {
	if ch, ok := collectors.Load(c); ok {
		return ch.(chan wsMsg)
	}
	ch := make(chan wsMsg, 1024)
	collectors.Store(c, ch)
	go func() {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				close(ch)
				return
			}
			var m wsMsg
			_ = json.Unmarshal(raw, &m)
			m.raw = string(raw)
			ch <- m
		}
	}()
	return ch
}

func drain(c *websocket.Conn, d time.Duration) []wsMsg {
	ch := collector(c)
	var out []wsMsg
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, m)
		case <-timer.C:
			return out
		}
	}
}

func metricData(t *testing.T, m wsMsg) map[string]interface{} {
	t.Helper()
	var s string
	if err := json.Unmarshal(m.Data, &s); err != nil {
		t.Fatalf("metric data not a string: %s", m.raw)
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		t.Fatalf("metric data: %v", err)
	}
	return data
}

func tokenFor(t *testing.T, id int64, role int) string {
	t.Helper()
	tok, err := auth.GenerateToken(id, fmt.Sprintf("u%d", id), role, "test-jwt")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type nodeConn struct {
	c  *websocket.Conn
	mu sync.Mutex
}

// sendQuiet is for goroutines that may outlive the test body.
func (n *nodeConn) sendQuiet(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	_ = n.c.WriteMessage(websocket.TextMessage, []byte(msg))
}

func (n *nodeConn) send(t *testing.T, msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Errorf("node write: %v", err)
	}
}

const leakyMetric = `{"type":"metric","data":{"uptime":5,"cpu_usage":1.5,"memory_usage":2,"disk_usage":3,` +
	`"net_in_speed":10,"net_out_speed":20,"bytes_received":100,"bytes_transmitted":200,"tcp_conns":7,"udp_conns":1,"load1":0.5,` +
	`"service_name":"flvx_agent","serviceConnections":{"11_2_5_tcp":3,"12_3_6_tcp":4},` +
	`"egress_ip_family":"dual","landing_capable":true,` +
	`"forward_metrics":[` +
	`{"forward_id":11,"user_id":2,"tunnel_id":5,"node_id":1,"port":20011,"service_name":"11_2_5_tcp","in_speed":1,"out_speed":2,"connections":3},` +
	`{"forward_id":12,"user_id":3,"tunnel_id":6,"node_id":1,"port":20012,"service_name":"12_3_6_tcp","in_speed":4,"out_speed":5,"connections":4}]}}`

func TestPublicStreamOnlyWithSwitchAndNodeFieldsOnly(t *testing.T) {
	s, r, base := machineServer(t)
	node := &nodeConn{c: mustDial(t, base+"/system-info?type=1&secret=test-secret", nil)}
	waitFor(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.nodes[1] != nil })

	// Switch off (default): refused before upgrade.
	if c, code := dialWS(t, base+"/system-info?type=2", nil); c != nil || code != http.StatusForbidden {
		t.Fatalf("anonymous stream with switch off: conn=%v status=%d", c != nil, code)
	}
	if err := r.DB().Exec(`UPDATE vite_config SET value = 'true' WHERE name = 'login_monitor_link'`).Error; err != nil {
		t.Fatal(err)
	}
	s.resetPublicSwitchCache()
	pub := mustDial(t, base+"/system-info?type=2", nil)
	waitFor(t, func() bool { return s.countClients(kindPublic) == 1 })

	node.send(t, leakyMetric)
	// Late command replies, unknown types and untyped junk are never broadcast.
	node.send(t, `{"type":"TcpPingResponse","success":true,"message":"OK","requestId":"1_999","data":{"ip":"203.0.113.9","port":4433}}`)
	node.send(t, `{"type":"ApplyLandingDesiredResponse","success":true,"data":{"nodes":[{"online_ips":[{"ip":"198.51.100.7"}]}]}}`)
	node.send(t, `{"hello":"world","ip":"203.0.113.10"}`)

	msgs := drain(pub, 600*time.Millisecond)
	if len(msgs) != 1 || msgs[0].Type != "metric" {
		t.Fatalf("public got %d messages: %+v", len(msgs), msgs)
	}
	data := metricData(t, msgs[0])
	allowed := map[string]bool{}
	for _, k := range publicMetricFields {
		allowed[k] = true
	}
	for k := range data {
		if !allowed[k] {
			t.Fatalf("public metric leaks field %q: %v", k, data)
		}
	}
	if data["cpu_usage"] != 1.5 || data["tcp_conns"] != float64(7) {
		t.Fatalf("public metric lost node fields: %v", data)
	}
	for _, bad := range []string{"203.0.113", "198.51.100", "forward", "20011", "service"} {
		if strings.Contains(msgs[0].raw, bad) {
			t.Fatalf("public stream leaked %q: %s", bad, msgs[0].raw)
		}
	}

	// Switching it off drops the open stream at the next re-check.
	if err := r.DB().Exec(`UPDATE vite_config SET value = 'false' WHERE name = 'login_monitor_link'`).Error; err != nil {
		t.Fatal(err)
	}
	s.resetPublicSwitchCache()
	cw := s.snapshotClients()[0]
	if s.stillAllowed(cw) {
		t.Fatal("public stream still allowed after switch off")
	}
}

func TestUsersOnlySeeOwnRulesAndNoReplies(t *testing.T) {
	s, _, base := machineServer(t)
	s.SetUserScopeResolver(func(uid int64) (NodeScope, error) {
		if uid == 4 {
			return NodeScope{}, nil // no monitor grant
		}
		return NodeScope{Nodes: map[int64]struct{}{1: {}}}, nil
	})
	node := &nodeConn{c: mustDial(t, base+"/system-info?type=1&secret=test-secret", nil)}
	waitFor(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.nodes[1] != nil })
	admin := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 1, 0), nil)
	userA := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 2, 1), nil)
	userB := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 3, 1), nil)
	noScope := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 4, 1), nil)
	waitFor(t, func() bool { return s.countClients(kindUser) == 3 && s.countClients(kindAdmin) == 1 })
	for _, c := range []*websocket.Conn{admin, userA, userB, noScope} {
		collector(c)
	}

	// A real command round trip: the reply reaches SendCommand only.
	go func() {
		_, payload, err := node.c.ReadMessage()
		if err != nil {
			return
		}
		var cmd struct {
			RequestID string `json:"requestId"`
		}
		_ = json.Unmarshal([]byte(decryptIfNeeded(payload, nil, "test-secret")), &cmd)
		node.sendQuiet(fmt.Sprintf(`{"type":"TcpPingResponse","success":true,"message":"OK","requestId":%q,"data":{"ip":"203.0.113.9","port":4433,"averageTime":12}}`, cmd.RequestID))
	}()
	res, err := s.SendCommand(1, "TcpPing", map[string]interface{}{"ip": "203.0.113.9", "port": 4433}, 3*time.Second)
	if err != nil || res.Data["ip"] != "203.0.113.9" {
		t.Fatalf("command reply not delivered to waiter: %v %+v", err, res)
	}
	node.send(t, leakyMetric)

	check := func(name string, c *websocket.Conn, wantFwdUser int64) {
		msgs := drain(c, 600*time.Millisecond)
		metrics := 0
		for _, m := range msgs {
			if strings.Contains(m.raw, "203.0.113.9") || strings.Contains(m.raw, "TcpPing") {
				t.Fatalf("%s received a command reply: %s", name, m.raw)
			}
			if m.Type != "metric" {
				continue
			}
			metrics++
			data := metricData(t, m)
			if _, ok := data["serviceConnections"]; ok && wantFwdUser != 0 {
				t.Fatalf("%s got serviceConnections (other users' rule ids): %v", name, data)
			}
			fwd, _ := data["forward_metrics"].([]interface{})
			if wantFwdUser == 0 {
				// Plan 136: admins get only the fields their views render.
				_, svc := data["serviceConnections"]
				if len(fwd) != 0 || svc || data["service_name"] != nil || data["cpu_usage"] != 1.5 || data["tcp_conns"] != float64(7) {
					t.Fatalf("admin view: %v", data)
				}
				continue
			}
			if len(fwd) != 1 || fwd[0].(map[string]interface{})["user_id"] != float64(wantFwdUser) {
				t.Fatalf("%s forward metrics %v", name, fwd)
			}
			if data["cpu_usage"] != 1.5 {
				t.Fatalf("%s lost node fields: %v", name, data)
			}
		}
		if metrics != 1 {
			t.Fatalf("%s got %d metric messages: %+v", name, metrics, msgs)
		}
	}
	check("admin", admin, 0)
	check("userA", userA, 2)
	check("userB", userB, 3)
	if msgs := drain(noScope, 300*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("user without monitor grant received %+v", msgs)
	}
	// Agent upgrade progress is admin-only.
	node.send(t, `{"type":"UpgradeProgress","success":true,"message":"x","data":{"stage":"download","percent":5}}`)
	if msgs := drain(userA, 300*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("user got upgrade progress: %+v", msgs)
	}
	if msgs := drain(admin, 300*time.Millisecond); len(msgs) != 1 || msgs[0].Type != "upgrade_progress" {
		t.Fatalf("admin upgrade progress: %+v", msgs)
	}
}

func TestSlowClientDoesNotDelayNodeReplies(t *testing.T) {
	// Forward every flooded metric so the stuck client's queue really fills.
	prevInterval := browserMetricInterval.Load()
	browserMetricInterval.Store(0)
	t.Cleanup(func() { browserMetricInterval.Store(prevInterval) })
	s, _, base := machineServer(t)
	node := &nodeConn{c: mustDial(t, base+"/system-info?type=1&secret=test-secret", nil)}
	waitFor(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.nodes[1] != nil })
	// An admin session that never reads.
	_ = mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 1, 0), nil)
	waitFor(t, func() bool { return s.countClients(kindAdmin) == 1 })

	big := strings.Repeat("x", 32<<10)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// The padding rides in a field admins receive (unknown fields are dropped).
			node.sendQuiet(fmt.Sprintf(`{"type":"metric","data":{"uptime":%d,"cpu_usage":1,"renewal_cycle":%q}}`, i, big))
		}
	}()
	// Command replies keep flowing while the flood fills the stuck client.
	go func() {
		for {
			_, payload, err := node.c.ReadMessage()
			if err != nil {
				return
			}
			var cmd struct {
				RequestID string `json:"requestId"`
			}
			_ = json.Unmarshal([]byte(decryptIfNeeded(payload, nil, "test-secret")), &cmd)
			node.sendQuiet(fmt.Sprintf(`{"type":"PingResponse","success":true,"message":"OK","requestId":%q}`, cmd.RequestID))
		}
	}()
	time.Sleep(300 * time.Millisecond)
	var worst time.Duration
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, err := s.SendCommand(1, "Ping", nil, 3*time.Second); err != nil {
			t.Fatalf("command %d during flood: %v", i, err)
		}
		if d := time.Since(start); d > worst {
			worst = d
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("worst command round trip with a stuck client: %s; stuck clients dropped: %d", worst, s.stats.slowClosed.Load())
	if worst > time.Second {
		t.Fatalf("stuck client delayed node replies: %s", worst)
	}
	waitFor(t, func() bool { return s.countClients(kindAdmin) == 0 })
}

func TestConnectionCapsAndOrigin(t *testing.T) {
	_, r, base := machineServer(t)
	if err := r.DB().Exec(`UPDATE vite_config SET value = 'true' WHERE name = 'login_monitor_link'`).Error; err != nil {
		t.Fatal(err)
	}
	ip := func(v string) http.Header { return http.Header{"X-Real-Ip": {v}} }
	for i := 0; i < maxAnonPerIP; i++ {
		mustDial(t, base+"/system-info?type=2", ip("203.0.113.1"))
	}
	if c, code := dialWS(t, base+"/system-info?type=2", ip("203.0.113.1")); c != nil || code != http.StatusTooManyRequests {
		t.Fatalf("anon per-IP cap: status %d", code)
	}
	for i := 0; i < maxAnonTotal-maxAnonPerIP; i++ {
		mustDial(t, base+"/system-info?type=2", ip(fmt.Sprintf("198.51.100.%d", i/maxAnonPerIP+1)))
	}
	if c, code := dialWS(t, base+"/system-info?type=2", ip("192.0.2.200")); c != nil || code != http.StatusTooManyRequests {
		t.Fatalf("anon total cap: status %d", code)
	}

	tok := tokenFor(t, 5, 1)
	for i := 0; i < maxSessionsPerUser; i++ {
		mustDial(t, base+"/system-info?type=0&secret="+tok, nil)
	}
	if c, code := dialWS(t, base+"/system-info?type=0&secret="+tok, nil); c != nil || code != http.StatusTooManyRequests {
		t.Fatalf("per-user cap: status %d", code)
	}
	if c, code := dialWS(t, base+"/system-info?type=0&secret=garbage", nil); c != nil || code != http.StatusForbidden {
		t.Fatalf("bad token: status %d", code)
	}

	host := strings.TrimPrefix(base, "ws://")
	admin := tokenFor(t, 1, 0)
	if c, code := dialWS(t, base+"/system-info?type=0&secret="+admin, http.Header{"Origin": {"https://evil.example"}}); c != nil || code != http.StatusForbidden {
		t.Fatalf("foreign origin: status %d", code)
	}
	mustDial(t, base+"/system-info?type=0&secret="+admin, http.Header{"Origin": {"http://" + host}})
	mustDial(t, base+"/system-info?type=0&secret="+admin, http.Header{"Origin": {"https://panel.example"}, "X-Forwarded-Host": {"panel.example"}})
	// Agents never send a browser origin check.
	mustDial(t, base+"/system-info?type=1&secret=test-secret", http.Header{"Origin": {"https://evil.example"}})
}

func TestDisabledUserLosesStream(t *testing.T) {
	old := clientRecheckEvery
	clientRecheckEvery = 150 * time.Millisecond
	t.Cleanup(func() { clientRecheckEvery = old })

	s, _, base := machineServer(t)
	var mu sync.Mutex
	status := map[int64]int{2: 1, 3: 0}
	cache := authstate.New(func(id int64) (int, int, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		st, ok := status[id]
		return st, 1, ok, nil
	}, 50*time.Millisecond)
	s.SetUserState(cache)
	s.SetUserScopeResolver(func(int64) (NodeScope, error) { return NodeScope{All: true}, nil })

	if c, code := dialWS(t, base+"/system-info?type=0&secret="+tokenFor(t, 3, 1), nil); c != nil || code != http.StatusForbidden {
		t.Fatalf("disabled user connected: status %d", code)
	}
	if c, code := dialWS(t, base+"/system-info?type=0&secret="+tokenFor(t, 9, 1), nil); c != nil || code != http.StatusForbidden {
		t.Fatalf("deleted user connected: status %d", code)
	}
	if c, code := dialWS(t, base+"/system-info?type=0&secret="+tokenFor(t, 2, 0), nil); c != nil || code != http.StatusForbidden {
		t.Fatalf("token with a stale admin role connected: status %d", code)
	}
	c := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 2, 1), nil)
	mu.Lock()
	status[2] = 0
	mu.Unlock()
	cache.Invalidate(2)
	ch := collector(c)
	deadline := time.After(3 * time.Second)
	for closed := false; !closed; {
		select {
		case _, ok := <-ch:
			closed = !ok
		case <-deadline:
			t.Fatal("disabled user's stream stayed open")
		}
	}
	waitFor(t, func() bool { return s.countClients(kindUser) == 0 })
}

// Plan 136 (F17): browsers get at most one metric per node per interval;
// the panel still records every report.
func TestBrowserMetricThrottle(t *testing.T) {
	s, _, base := machineServer(t)
	node := &nodeConn{c: mustDial(t, base+"/system-info?type=1&secret=test-secret", nil)}
	waitFor(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.nodes[1] != nil })
	admin := mustDial(t, base+"/system-info?type=0&secret="+tokenFor(t, 1, 0), nil)
	waitFor(t, func() bool { return s.countClients(kindAdmin) == 1 })
	for i := 0; i < 5; i++ {
		node.send(t, fmt.Sprintf(`{"type":"metric","data":{"uptime":%d,"cpu_usage":1,"tcp_conns":7}}`, i+1))
	}
	got := 0
	for _, m := range drain(admin, 600*time.Millisecond) {
		if m.Type == "metric" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("burst of 5 reports within the interval gave %d browser metrics", got)
	}
	time.Sleep(time.Duration(browserMetricInterval.Load()))
	node.send(t, `{"type":"metric","data":{"uptime":9,"cpu_usage":1,"tcp_conns":7}}`)
	got = 0
	for _, m := range drain(admin, 600*time.Millisecond) {
		if m.Type == "metric" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("report after the interval gave %d browser metrics", got)
	}
}
