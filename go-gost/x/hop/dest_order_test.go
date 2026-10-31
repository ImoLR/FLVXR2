package hop

import (
	"context"
	"testing"

	"github.com/go-gost/core/chain"
	corehop "github.com/go-gost/core/hop"
	xlogger "github.com/go-gost/x/logger"
)

func TestNormalizeDestKey(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM:443":   "example.com:443",
		" 203.0.113.5:80 ":  "203.0.113.5:80",
		"[2001:DB8::1]:443": "[2001:db8::1]:443",
		"[fe80::1%eth0]:22": "[fe80::1]:22",
		"no-port.example":   "no-port.example",
		"":                  "",
	} {
		if got := NormalizeDestKey(in); got != want {
			t.Fatalf("NormalizeDestKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDestOrderShapes(t *testing.T) {
	if ParseDestOrder(nil) != nil || ParseDestOrder("x") != nil || ParseDestOrder(map[string]any{}) != nil {
		t.Fatalf("empty/invalid must be nil")
	}
	m := ParseDestOrder(map[string]any{
		"A.example:1": []any{"192.0.2.2:2", "192.0.2.1:1", 5, ""},
		"bad":         "not-a-list",
		"empty:1":     []any{},
	})
	if len(m) != 1 || len(m["a.example:1"]) != 2 || m["a.example:1"][0] != "192.0.2.2:2" {
		t.Fatalf("parsed %v", m)
	}
	y := ParseDestOrder(map[any]any{"b.example:2": []string{"192.0.2.1:1"}})
	if len(y["b.example:2"]) != 1 {
		t.Fatalf("yaml shape parsed %v", y)
	}
}

func TestSelectUsesDestOrderOnlyForMatchingDestination(t *testing.T) {
	a, b, c := chain.NewNode("node_1", "192.0.2.1:1"), chain.NewNode("node_2", "192.0.2.2:2"), chain.NewNode("node_3", "192.0.2.3:3")
	h := NewHop(NodeOption(a, b, c), LoggerOption(xlogger.Nop()),
		DestOrderOption(map[string][]string{"t.example:443": {"192.0.2.3:3", "192.0.2.9:9"}})).(*chainHop)
	sel := func(host, addr string) *chain.Node {
		return h.Select(context.Background(), corehop.NetworkSelectOption("tcp"), corehop.AddrSelectOption(addr), corehop.HostSelectOption(host))
	}
	if got := sel("t.example:443", "198.51.100.1:443"); got != c {
		t.Fatalf("matching host: got %v", got)
	}
	if got := sel("", "t.example:443"); got != c {
		t.Fatalf("matching addr: got %v", got)
	}
	if got := sel("u.example:443", "u.example:443"); got != a {
		t.Fatalf("other destination: got %v", got)
	}
	// Unknown addresses in the order are ignored; listed first, then the rest
	// in hop order.
	got := applyDestOrder([]*chain.Node{a, b, c}, []string{"192.0.2.3:3", "192.0.2.9:9"})
	if len(got) != 3 || got[0] != c || got[1] != a || got[2] != b {
		t.Fatalf("applyDestOrder = %v", got)
	}
	// Static-hop rule: in-place set works, and clearing it returns to hop order.
	if !h.SetDestOrder(nil) || h.DestOrder() != nil {
		t.Fatalf("clear failed")
	}
	if got := sel("t.example:443", "t.example:443"); got != a {
		t.Fatalf("after clear: got %v", got)
	}
}

// Per-dial cost of the lookup on a non-matching destination: no allocation
// beyond the existing Select path.
func BenchmarkSelectDestOrder(b *testing.B) {
	n1, n2 := chain.NewNode("node_1", "192.0.2.1:1"), chain.NewNode("node_2", "192.0.2.2:2")
	order := map[string][]string{}
	for i := 0; i < 200; i++ {
		order[NormalizeDestKey("t"+string(rune('a'+i%26))+".example:"+string(rune('0'+i%10))+"443")] = []string{"192.0.2.2:2", "192.0.2.1:1"}
	}
	order["hit.example:443"] = []string{"192.0.2.2:2", "192.0.2.1:1"}
	h := NewHop(NodeOption(n1, n2), LoggerOption(xlogger.Nop()), DestOrderOption(order))
	b.Run("miss", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h.Select(context.Background(), corehop.AddrSelectOption("miss.example:443"), corehop.HostSelectOption("miss.example:443"))
		}
	})
	b.Run("hit", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			h.Select(context.Background(), corehop.AddrSelectOption("hit.example:443"), corehop.HostSelectOption("hit.example:443"))
		}
	})
	b.Run("none", func(b *testing.B) {
		h0 := NewHop(NodeOption(n1, n2), LoggerOption(xlogger.Nop()))
		for i := 0; i < b.N; i++ {
			h0.Select(context.Background(), corehop.AddrSelectOption("hit.example:443"), corehop.HostSelectOption("hit.example:443"))
		}
	})
}
