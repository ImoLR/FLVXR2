package socket

import (
	"strings"
	"testing"
)

// Plan 231: the panel learns from the handshake that this agent counts an
// IPv6 /64 as one IP and keys sharedIps entries itself.
func TestHandshakeAnnouncesIPKey(t *testing.T) {
	if q := buildWebSocketQuery("s", "v", 0, 0, 0, 0); !strings.Contains(q, "&ipKey=1") {
		t.Fatalf("query %q lacks ipKey=1", q)
	}
}
