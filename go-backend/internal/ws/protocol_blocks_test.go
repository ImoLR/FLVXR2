package ws

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/auth"
)

// Plan 182: the agent's protocol-filter counters are kept per session in
// memory, survive metrics without the field, never reach browsers, and vanish
// with the session.
func TestProtocolBlocksKeptInMemoryAdminAPIOnly(t *testing.T) {
	s, _, base := machineServer(t)
	adminToken, _ := auth.GenerateToken(1, "admin", 0, "test-jwt")
	dial := func(q string) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?"+q, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	admin := dial("type=0&secret=" + adminToken)
	node := dial("type=1&secret=test-secret")
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.RLock()
		ready := s.nodes[1] != nil
		s.mu.RUnlock()
		if (ready && s.countClients(kindAdmin) == 1) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	prevInterval := browserMetricInterval.Load()
	browserMetricInterval.Store(0)
	t.Cleanup(func() { browserMetricInterval.Store(prevInterval) })

	readMetric := func() map[string]interface{} {
		t.Helper()
		for {
			_ = admin.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, msg, err := admin.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			var env struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if json.Unmarshal(msg, &env) != nil || env.Type != "metric" {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(env.Data), &data); err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	send := func(metric string) {
		t.Helper()
		if err := node.WriteMessage(websocket.TextMessage, []byte(metric)); err != nil {
			t.Fatal(err)
		}
	}

	// Old agent: no field → nothing.
	send(`{"type":"metric","data":{"uptime":5,"tcp_conns":7}}`)
	readMetric()
	if p := s.ProtocolBlocks(1); p != nil {
		t.Fatalf("old agent counters %+v", p)
	}
	send(`{"type":"metric","data":{"uptime":6,"tcp_conns":7,"protocol_blocks":{"http":1,"tls":2,"socks":205,"other":0}}}`)
	if m := readMetric(); m["protocol_blocks"] != nil {
		t.Fatalf("counters forwarded to the browser: %v", m)
	}
	var p *ProtocolBlocks
	for i := 0; i < 100 && p == nil; i++ {
		p = s.ProtocolBlocks(1)
		time.Sleep(10 * time.Millisecond)
	}
	if p == nil || p.SOCKS != 205 || p.HTTP != 1 || p.TLS != 2 {
		t.Fatalf("stored %+v", p)
	}
	// A metric without the field keeps the latest counters.
	send(`{"type":"metric","data":{"uptime":7,"tcp_conns":7}}`)
	readMetric()
	if p := s.ProtocolBlocks(1); p == nil || p.SOCKS != 205 {
		t.Fatalf("counters dropped by a metric without the field: %+v", p)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"socks":205`) {
		t.Fatalf("json %s", b)
	}
	// Session ends → unknown.
	node.Close()
	for i := 0; i < 200 && s.ProtocolBlocks(1) != nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if p := s.ProtocolBlocks(1); p != nil {
		t.Fatalf("counters outlived the session: %+v", p)
	}
}
