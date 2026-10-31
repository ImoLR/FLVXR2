package socket

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-gost/core/chain"
	xchain "github.com/go-gost/x/chain"
	"github.com/go-gost/x/config"
	xhop "github.com/go-gost/x/hop"
	"github.com/go-gost/x/registry"
)

// Plan 180 (规则最优): the panel's hop as built for a rule-best tunnel — the
// plan-165 fifo hop plus metadata.destOrder.
func destOrderChainConfig(name string, destOrder map[string][]string, addrs ...string) config.ChainConfig {
	c := swapTestChainConfig(name, addrs...)
	if destOrder != nil {
		md := map[string]any{}
		raw := map[string]any{}
		for k, v := range destOrder {
			list := make([]any, 0, len(v))
			for _, a := range v {
				list = append(list, a)
			}
			raw[k] = list
		}
		md[xhop.DestOrderMetadataKey] = raw
		c.Hops[0].Metadata = md
	}
	return c
}

func routeFirst(name, network, dest string, opts ...chain.RouteOption) string {
	route := registry.ChainRegistry().Get(name).Route(context.Background(), network, dest, opts...)
	if route == nil || len(route.Nodes()) == 0 {
		return ""
	}
	return route.Nodes()[0].Addr
}

const (
	exitA = "192.0.2.1:1001"
	exitB = "192.0.2.2:1002"
	exitC = "192.0.2.3:1003"
)

func TestDestOrderPicksPerDestinationTCPAndUDP(t *testing.T) {
	name := "chains_180_pick"
	swapTestCleanup(t, name)
	order := map[string][]string{"target-b.example:443": {exitB, exitA, exitC}}
	if err := createChain(createChainRequest{Data: destOrderChainConfig(name, order, exitA, exitB, exitC)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, network := range []string{"tcp", "udp"} {
		if got := routeFirst(name, network, "target-b.example:443"); got != exitB {
			t.Fatalf("%s matching destination first = %q, want %s", network, got, exitB)
		}
		if got := routeFirst(name, network, "target-a.example:443"); got != exitA {
			t.Fatalf("%s other destination first = %q, want the hop order %s", network, got, exitA)
		}
		// Router: the original (unresolved) address arrives as Host.
		if got := routeFirst(name, network, "198.51.100.7:443", chain.WithHostRouteOption("TARGET-B.example:443")); got != exitB {
			t.Fatalf("%s resolved destination with original host first = %q, want %s", network, got, exitB)
		}
		// Same host, other port = other destination.
		if got := routeFirst(name, network, "target-b.example:8443"); got != exitA {
			t.Fatalf("%s other port first = %q, want %s", network, got, exitA)
		}
	}
}

// A dead preferred node is skipped at once for its destination too (the same
// fail marks / maxFails=1 / failTimeout as the hop order), and the order
// continues with the destination's own next node.
func TestDestOrderKeepsInstantFailover(t *testing.T) {
	name := "chains_180_failover"
	swapTestCleanup(t, name)
	order := map[string][]string{"203.0.113.20:443": {exitC, exitB, exitA}}
	if err := createChain(createChainRequest{Data: destOrderChainConfig(name, order, exitA, exitB, exitC)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	nodes := runningHopNodes(t, name)
	var nodeC *chain.Node
	for _, n := range nodes {
		if n.Addr == exitC {
			nodeC = n
		}
	}
	if nodeC == nil || nodeC.Marker() == nil {
		t.Fatalf("node C or its marker missing")
	}
	if got := routeFirst(name, "tcp", "203.0.113.20:443"); got != exitC {
		t.Fatalf("before failure first = %q", got)
	}
	nodeC.Marker().Mark()
	if got := routeFirst(name, "tcp", "203.0.113.20:443"); got != exitB {
		t.Fatalf("after C failed first = %q, want the destination's next %s", got, exitB)
	}
	if got := routeFirst(name, "udp", "203.0.113.20:443"); got != exitB {
		t.Fatalf("udp after C failed first = %q, want %s", got, exitB)
	}
	// Other destinations: hop order (A first) unaffected.
	if got := routeFirst(name, "tcp", "203.0.113.21:443"); got != exitA {
		t.Fatalf("other destination first = %q", got)
	}
	nodeC.Marker().Reset()
	if got := routeFirst(name, "tcp", "203.0.113.20:443"); got != exitC {
		t.Fatalf("after C recovered first = %q", got)
	}
}

// destOrder changes (added, changed, removed) and node reorders are applied
// in place: same chain object, same node objects (mux sessions, fail marks).
func TestUpdateChainDestOrderInPlace(t *testing.T) {
	name := "chains_180_inplace"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: destOrderChainConfig(name, nil, exitA, exitB)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	chainBefore := registry.LookupChain(name)
	before := runningHopNodes(t, name)
	steps := []struct {
		order map[string][]string
		addrs []string
		dest  string
		want  string
	}{
		{map[string][]string{"t1.example:80": {exitB, exitA}}, []string{exitA, exitB}, "t1.example:80", exitB},
		{map[string][]string{"t1.example:80": {exitA, exitB}, "t2.example:80": {exitB, exitA}}, []string{exitB, exitA}, "t1.example:80", exitA},
		{nil, []string{exitB, exitA}, "t1.example:80", exitB},
	}
	for i, st := range steps {
		if err := updateChain(updateChainRequest{Chain: name, Data: destOrderChainConfig(name, st.order, st.addrs...)}); err != nil {
			t.Fatalf("step %d update: %v", i, err)
		}
		if registry.LookupChain(name) != chainBefore {
			t.Fatalf("step %d: destOrder/order-only update must keep the running chain object", i)
		}
		after := runningHopNodes(t, name)
		for _, n := range after {
			if n != before[0] && n != before[1] {
				t.Fatalf("step %d: node objects replaced", i)
			}
		}
		if got := routeFirst(name, "tcp", st.dest); got != st.want {
			t.Fatalf("step %d: first for %s = %q, want %s", i, st.dest, got, st.want)
		}
	}
	// The saved config carries the last destOrder (none) — an agent restart
	// parses exactly what the panel last sent.
	var saved *config.ChainConfig
	config.OnUpdate(func(c *config.Config) error {
		for _, ch := range c.Chains {
			if ch != nil && ch.Name == name {
				saved = ch
			}
		}
		return nil
	})
	if saved == nil || saved.Hops[0].Metadata != nil {
		t.Fatalf("saved config should have no destOrder: %#v", saved.Hops[0].Metadata)
	}
}

// A config with destOrder survives the JSON round trip of gost.json (agent
// restart) and an old config without it parses exactly as before.
func TestDestOrderConfigRoundTripAndOldConfig(t *testing.T) {
	name := "chains_180_roundtrip"
	swapTestCleanup(t, name)
	cfg := destOrderChainConfig(name, map[string][]string{"[2001:DB8::5]:443": {exitB, exitA}}, exitA, exitB)
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var back config.ChainConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := createChain(createChainRequest{Data: back}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := routeFirst(name, "tcp", "[2001:db8::5]:443"); got != exitB {
		t.Fatalf("ipv6 destination first = %q", got)
	}
	c := registry.LookupChain(name).(*xchain.Chain)
	if m := c.Hops()[0].(interface{ DestOrder() map[string][]string }).DestOrder(); len(m["[2001:db8::5]:443"]) != 2 {
		t.Fatalf("normalised key missing: %v", m)
	}

	old := "chains_180_old"
	swapTestCleanup(t, old)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(old, exitA, exitB)}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	oc := registry.LookupChain(old).(*xchain.Chain)
	if m := oc.Hops()[0].(interface{ DestOrder() map[string][]string }).DestOrder(); m != nil {
		t.Fatalf("old config got a destOrder: %v", m)
	}
	if got := routeFirst(old, "tcp", "203.0.113.9:443"); got != exitA {
		t.Fatalf("old config first = %q", got)
	}
}

func TestHandshakeAdvertisesDestOrder(t *testing.T) {
	if q := buildWebSocketQuery("s", "v", 0, 0, 0, 0); !strings.Contains(q, "&destOrder=1") {
		t.Fatalf("handshake lacks destOrder=1: %s", q)
	}
}
