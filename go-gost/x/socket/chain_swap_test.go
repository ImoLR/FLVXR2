package socket

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-gost/core/chain"
	corelogger "github.com/go-gost/core/logger"
	xchain "github.com/go-gost/x/chain"
	"github.com/go-gost/x/config"
	_ "github.com/go-gost/x/connector/relay"
	_ "github.com/go-gost/x/dialer/tls"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
)

// Plan 165: a 最优 hop on the agent, as the panel builds it (relay connector,
// tls dialer, fifo selector with maxFails/failTimeout).
func swapTestChainConfig(name string, addrs ...string) config.ChainConfig {
	nodes := make([]*config.NodeConfig, 0, len(addrs))
	for i, addr := range addrs {
		nodes = append(nodes, &config.NodeConfig{
			Name:      "node_" + string(rune('1'+i)),
			Addr:      addr,
			Connector: &config.ConnectorConfig{Type: "relay", Metadata: map[string]any{"nodelay": true, "udpTTL": "5s"}},
			Dialer:    &config.DialerConfig{Type: "tls"},
		})
	}
	return config.ChainConfig{
		Name: name,
		Hops: []*config.HopConfig{{
			Name:     "hop_" + name,
			Selector: &config.SelectorConfig{Strategy: "fifo", MaxFails: 1, FailTimeout: 600_000_000_000},
			Nodes:    nodes,
		}},
	}
}

func swapTestCleanup(t *testing.T, name string) {
	t.Helper()
	if corelogger.Default() == nil {
		corelogger.SetDefault(xlogger.Nop())
	}
	registry.ChainRegistry().Unregister(name)
	t.Cleanup(func() {
		registry.ChainRegistry().Unregister(name)
		config.OnUpdate(func(c *config.Config) error {
			kept := c.Chains[:0]
			for _, ch := range c.Chains {
				if ch != nil && ch.Name != name {
					kept = append(kept, ch)
				}
			}
			c.Chains = kept
			return nil
		})
	})
}

func runningHopNodes(t *testing.T, name string) []*chain.Node {
	t.Helper()
	c, ok := registry.LookupChain(name).(*xchain.Chain)
	if !ok || c == nil || len(c.Hops()) != 1 {
		t.Fatalf("chain %s not registered as one-hop chain: %#v", name, registry.LookupChain(name))
	}
	nl, ok := c.Hops()[0].(interface{ Nodes() []*chain.Node })
	if !ok {
		t.Fatalf("hop has no node list")
	}
	return nl.Nodes()
}

func firstRouteAddr(name string) string {
	route := registry.ChainRegistry().Get(name).Route(context.Background(), "tcp", "203.0.113.9:443")
	if route == nil || len(route.Nodes()) == 0 {
		return ""
	}
	return route.Nodes()[0].Addr
}

func TestUpdateChainOrderOnlyReordersSameNodeObjects(t *testing.T) {
	name := "chains_165_reorder"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, "192.0.2.1:1001", "192.0.2.2:1002", "192.0.2.3:1003")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := runningHopNodes(t, name)
	chainBefore := registry.LookupChain(name)
	if got := firstRouteAddr(name); got != "192.0.2.1:1001" {
		t.Fatalf("first before = %q", got)
	}

	if err := updateChain(updateChainRequest{Chain: name, Data: swapTestChainConfig(name, "192.0.2.3:1003", "192.0.2.1:1001", "192.0.2.2:1002")}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if registry.LookupChain(name) != chainBefore {
		t.Fatalf("order-only update must keep the running chain object")
	}
	after := runningHopNodes(t, name)
	if len(after) != 3 || after[0] != before[2] || after[1] != before[0] || after[2] != before[1] {
		t.Fatalf("expected the same node objects in the new order, got %v", after)
	}
	if got := firstRouteAddr(name); got != "192.0.2.3:1003" {
		t.Fatalf("first after reorder = %q, want 192.0.2.3:1003", got)
	}
	// The saved config follows the new order (an agent restart keeps it).
	var saved *config.ChainConfig
	config.OnUpdate(func(c *config.Config) error {
		for _, ch := range c.Chains {
			if ch != nil && ch.Name == name {
				saved = ch
			}
		}
		return nil
	})
	if saved == nil || saved.Hops[0].Nodes[0].Addr != "192.0.2.3:1003" {
		t.Fatalf("saved config not in the new order: %#v", saved)
	}
}

func TestUpdateChainOtherChangeSwapsParsedChainAtomically(t *testing.T) {
	name := "chains_165_replace"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, "192.0.2.1:1001", "192.0.2.2:1002")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	chainBefore := registry.LookupChain(name)
	if err := updateChain(updateChainRequest{Chain: name, Data: swapTestChainConfig(name, "192.0.2.9:1009", "192.0.2.1:1001")}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if registry.LookupChain(name) == chainBefore {
		t.Fatalf("a changed node set must swap in the new chain")
	}
	if got := firstRouteAddr(name); got != "192.0.2.9:1009" {
		t.Fatalf("first after replace = %q", got)
	}
	// Missing chain: UpdateChains registers it (panel redeploy without DeleteChains).
	missing := "chains_165_missing"
	swapTestCleanup(t, missing)
	if err := updateChain(updateChainRequest{Data: swapTestChainConfig(missing, "192.0.2.5:1005")}); err != nil {
		t.Fatalf("update missing: %v", err)
	}
	if got := firstRouteAddr(missing); got != "192.0.2.5:1005" {
		t.Fatalf("missing chain not registered, first = %q", got)
	}
}

// No window without a chain: dials running during many updates (reorders and
// replacements) always find a route with nodes. With the old Unregister +
// Register sequence a dial in between got no route and the router fell back
// to a direct dial (DefaultRoute).
func TestUpdateChainNeverLeavesAWindowWithoutRoute(t *testing.T) {
	name := "chains_165_nogap"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, "192.0.2.1:1001", "192.0.2.2:1002")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var stop atomic.Bool
	var misses, dials atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				dials.Add(1)
				if firstRouteAddr(name) == "" {
					misses.Add(1)
				}
			}
		}()
	}
	orders := [][]string{
		{"192.0.2.1:1001", "192.0.2.2:1002"},
		{"192.0.2.2:1002", "192.0.2.1:1001"},
		{"192.0.2.3:1003", "192.0.2.1:1001"}, // node set change → atomic swap
	}
	for i := 0; i < 600; i++ {
		if err := updateChain(updateChainRequest{Chain: name, Data: swapTestChainConfig(name, orders[i%len(orders)]...)}); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if misses.Load() != 0 {
		t.Fatalf("%d of %d route lookups found no chain during updates", misses.Load(), dials.Load())
	}
	if dials.Load() == 0 {
		t.Fatalf("no concurrent lookups ran")
	}
}

func TestHandleUpdateChainAcceptsPlainChainConfig(t *testing.T) {
	name := "chains_165_plain"
	swapTestCleanup(t, name)
	w := &WebSocketReporter{}
	raw, _ := json.Marshal(swapTestChainConfig(name, "192.0.2.7:1007"))
	var plain map[string]any
	_ = json.Unmarshal(raw, &plain)
	if err := w.handleUpdateChain(plain); err != nil {
		t.Fatalf("plain update: %v", err)
	}
	if got := firstRouteAddr(name); got != "192.0.2.7:1007" {
		t.Fatalf("plain ChainConfig not applied, first = %q", got)
	}
	envelope := map[string]any{"chain": name, "data": swapTestChainConfig(name, "192.0.2.8:1008")}
	if err := w.handleUpdateChain(envelope); err != nil {
		t.Fatalf("envelope update: %v", err)
	}
	if got := firstRouteAddr(name); got != "192.0.2.8:1008" {
		t.Fatalf("envelope not applied, first = %q", got)
	}
}

func TestHandshakeAdvertisesChainSwap(t *testing.T) {
	if q := buildWebSocketQuery("s", "v", 0, 0, 0, 0); !strings.Contains(q, "&chainSwap=1") {
		t.Fatalf("handshake lacks chainSwap=1: %s", q)
	}
}
