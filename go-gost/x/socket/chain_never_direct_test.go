package socket

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/core/chain"
	corehop "github.com/go-gost/core/hop"
	xchain "github.com/go-gost/x/chain"
	"github.com/go-gost/x/config"
	hop_parser "github.com/go-gost/x/config/parsing/hop"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
)

// Plan 180 r2 (user 11-01): a 中转 / entry must never dial the target itself
// when every next-hop node of its chain is fail-marked. Before, hop.Select
// returned nil, Chain.Route returned a route without the hop and the router
// dialed the destination directly (bypassing the exit, leaking the 中转's IP).

func closedTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func markAll(nodes []*chain.Node) {
	for _, n := range nodes {
		n.Marker().Mark()
	}
}

func TestChainHopAllFailedStillRoutesThroughHop(t *testing.T) {
	name := "chains_180r2_route"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, exitA, exitB, exitC)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	nodes := runningHopNodes(t, name)
	markAll(nodes)
	// Every node is marked once: in order → A; the dial through A fails →
	// A is marked again → B next, then C: a retry walks through all of them.
	for i, want := range []string{exitA, exitB, exitC} {
		got := firstRouteAddr(name)
		if got != want {
			t.Fatalf("all marked, attempt %d: first = %q, want %s (never an empty route)", i, got, want)
		}
		for _, n := range nodes {
			if n.Addr == got {
				n.Marker().Mark()
			}
		}
	}
	// One node recovers (a success resets its marker) → it is used.
	nodes[1].Marker().Reset()
	if got := firstRouteAddr(name); got != exitB {
		t.Fatalf("after B recovered first = %q, want %s", got, exitB)
	}
}

// destOrder (规则最优) keeps its order on ties when every node is marked.
func TestChainHopAllFailedKeepsDestOrder(t *testing.T) {
	name := "chains_180r2_destorder"
	swapTestCleanup(t, name)
	order := map[string][]string{"203.0.113.30:443": {exitC, exitA, exitB}}
	if err := createChain(createChainRequest{Data: destOrderChainConfig(name, order, exitA, exitB, exitC)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	markAll(runningHopNodes(t, name))
	if got := routeFirst(name, "tcp", "203.0.113.30:443"); got != exitC {
		t.Fatalf("all marked, destination order first = %q, want %s", got, exitC)
	}
	if got := routeFirst(name, "udp", "203.0.113.31:443"); got != exitA {
		t.Fatalf("all marked, other destination first = %q, want %s", got, exitA)
	}
}

// Replaced chains (not in place) and in-place reorders keep the behaviour.
func TestChainHopAllFailedAfterUpdates(t *testing.T) {
	name := "chains_180r2_update"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, exitA, exitB)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// In place (order only).
	if err := updateChain(updateChainRequest{Chain: name, Data: swapTestChainConfig(name, exitB, exitA)}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	markAll(runningHopNodes(t, name))
	if got := firstRouteAddr(name); got != exitB {
		t.Fatalf("in-place reordered, all marked: first = %q, want %s", got, exitB)
	}
	// Replaced (other node set).
	if err := updateChain(updateChainRequest{Chain: name, Data: swapTestChainConfig(name, exitC, exitA)}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	markAll(runningHopNodes(t, name))
	if got := firstRouteAddr(name); got != exitC {
		t.Fatalf("replaced, all marked: first = %q, want %s", got, exitC)
	}
}

// End to end through the router: two dead exits, the target listening
// locally. The dials fail, and the target never sees a connection.
func TestRouterNeverDialsTargetWhenAllExitsFailed(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer target.Close()
	var direct atomic.Int32
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			direct.Add(1)
			c.Close()
		}
	}()
	name := "chains_180r2_router"
	swapTestCleanup(t, name)
	if err := createChain(createChainRequest{Data: swapTestChainConfig(name, closedTCPAddr(t), closedTCPAddr(t))}); err != nil {
		t.Fatalf("create: %v", err)
	}
	r := xchain.NewRouter(
		chain.ChainRouterOption(registry.ChainRegistry().Get(name)),
		chain.RetriesRouterOption(1), // as the panel sets on a 中转 (candidates - 1)
		chain.TimeoutRouterOption(3*time.Second),
	)
	for i := 0; i < 6; i++ {
		if conn, err := r.Dial(context.Background(), "tcp", target.Addr().String()); err == nil {
			conn.Close()
			t.Fatalf("dial %d succeeded with both exits dead", i)
		}
	}
	for _, n := range runningHopNodes(t, name) {
		if n.Marker().Count() == 0 {
			t.Fatalf("node %s never tried", n.Addr)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := direct.Load(); n != 0 {
		t.Fatalf("the target was dialed directly %d times", n)
	}
}

// A rule's target list (forwarder hop, not a chain hop) is unchanged: all
// targets marked ⇒ nil, the handler fails the connection as before.
func TestForwarderHopAllFailedUnchanged(t *testing.T) {
	cfg := swapTestChainConfig("fwd", exitA, exitB).Hops[0]
	h, err := hop_parser.ParseHop(&config.HopConfig{Name: "fwd", Selector: cfg.Selector, Nodes: cfg.Nodes}, xlogger.Nop())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nl := h.(interface{ Nodes() []*chain.Node })
	if got := h.Select(context.Background(), corehop.NetworkSelectOption("tcp")); got == nil || got.Addr != exitA {
		t.Fatalf("healthy forwarder first = %v", got)
	}
	markAll(nl.Nodes())
	if got := h.Select(context.Background(), corehop.NetworkSelectOption("tcp")); got != nil {
		t.Fatalf("forwarder hop with all targets marked = %v, want nil (unchanged)", got)
	}
}
