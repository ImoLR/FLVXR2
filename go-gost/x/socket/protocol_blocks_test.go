package socket

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-gost/x/service"
)

// Plan 182: the counters ride in the first metric of a session, then only
// when one changed; the JSON field is a few bytes.
func TestProtocolBlocksReportFirstThenOnChange(t *testing.T) {
	h, tl, s, o := service.GetProtocolBlockFlags()
	t.Cleanup(func() { service.SetProtocolBlock(h, tl, s, o) })
	service.SetProtocolBlock(0, 0, 1, 0)

	w := &WebSocketReporter{}
	first := w.protocolBlocksReport()
	if first == nil {
		t.Fatal("first metric of a session must carry the counters")
	}
	if w.protocolBlocksReport() != nil {
		t.Fatal("unchanged counters must not be sent again")
	}
	if !service.IsProtocolBlocked([]byte{0x05, 0x01, 0x00}) {
		t.Fatal("SOCKS5 greeting not blocked with socks=1")
	}
	next := w.protocolBlocksReport()
	if next == nil || next.SOCKS != first.SOCKS+1 || next.HTTP != first.HTTP {
		t.Fatalf("after one SOCKS block: %+v (first %+v)", next, first)
	}
	if w.protocolBlocksReport() != nil {
		t.Fatal("unchanged again")
	}
	w.blocksSent = nil // new session (connect)
	if w.protocolBlocksReport() == nil {
		t.Fatal("a new session must carry the counters again")
	}

	b, _ := json.Marshal(SystemInfo{ProtocolBlocks: next})
	if !strings.Contains(string(b), `"protocol_blocks":{"http":`) {
		t.Fatalf("json: %s", b)
	}
	b, _ = json.Marshal(SystemInfo{})
	if strings.Contains(string(b), "protocol_blocks") {
		t.Fatalf("absent counters must be omitted: %s", b)
	}
}
