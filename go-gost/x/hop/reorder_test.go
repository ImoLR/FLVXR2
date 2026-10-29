package hop

import (
	"testing"

	"github.com/go-gost/core/chain"
	xlogger "github.com/go-gost/x/logger"
)

func TestReorderNodesOnlyAcceptsPermutations(t *testing.T) {
	a, b, c := chain.NewNode("node_1", "192.0.2.1:1"), chain.NewNode("node_2", "192.0.2.2:2"), chain.NewNode("node_3", "192.0.2.3:3")
	h := NewHop(NodeOption(a, b, c), LoggerOption(xlogger.Nop())).(*chainHop)

	for _, bad := range [][]string{
		{"192.0.2.3:3", "192.0.2.1:1"},                // missing one
		{"192.0.2.3:3", "192.0.2.1:1", "192.0.2.9:9"}, // unknown addr
		{"192.0.2.3:3", "192.0.2.3:3", "192.0.2.1:1"}, // duplicate
	} {
		if h.ReorderNodes(bad) {
			t.Fatalf("accepted %v", bad)
		}
		if got := h.Nodes(); got[0] != a || got[1] != b || got[2] != c {
			t.Fatalf("rejected order changed nodes: %v", got)
		}
	}
	if !h.ReorderNodes([]string{"192.0.2.3:3", "192.0.2.1:1", "192.0.2.2:2"}) {
		t.Fatalf("permutation rejected")
	}
	if got := h.Nodes(); got[0] != c || got[1] != a || got[2] != b {
		t.Fatalf("not reordered in place: %v", got)
	}
	// A later reload (same static options) keeps the new order.
	if got := h.options.nodes; got[0] != c {
		t.Fatalf("options.nodes not reordered")
	}
}
