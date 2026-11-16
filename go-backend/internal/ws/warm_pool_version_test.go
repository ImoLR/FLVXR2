package ws

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Plan 264: the panel needs the agent's warmPool handshake value (1 = fixed
// sizes ≤ 4, 2 = adaptive floor / ceiling) to shape SetWarmPools.
func TestWarmPoolVersionFromHandshake(t *testing.T) {
	for _, tc := range []struct {
		query   string
		version int
	}{{"&warmPool=2", 2}, {"&warmPool=1", 1}, {"", 0}, {"&warmPool=x", 0}} {
		t.Run("q"+tc.query, func(t *testing.T) {
			s, _, base := machineServer(t)
			online := make(chan struct{}, 1)
			s.SetNodeOnlineHook(func(int64) { online <- struct{}{} })
			c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?type=1&secret=test-secret"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			select {
			case <-online:
			case <-time.After(3 * time.Second):
				t.Fatal("node not online")
			}
			if v := s.WarmPoolVersion(1); v != tc.version {
				t.Fatalf("WarmPoolVersion = %d, want %d", v, tc.version)
			}
			if s.WarmPoolCapable(1) != (tc.version > 0) {
				t.Fatal("capability disagrees with the version")
			}
			if s.WarmPoolVersion(99) != 0 {
				t.Fatal("unknown node has a version")
			}
		})
	}
	var nilServer *Server
	if nilServer.WarmPoolVersion(1) != 0 {
		t.Fatal("nil server")
	}
}
