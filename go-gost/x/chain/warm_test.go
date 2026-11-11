package chain_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/connector"
	"github.com/go-gost/core/logger"
	xchain "github.com/go-gost/x/chain"
	xhop "github.com/go-gost/x/hop"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
	"github.com/go-gost/x/warmpool"
)

// relayish: a next hop that answers a 1-byte request 'C' with 'K' and then
// echoes; the first `dropFirst` requests are dropped (conn closed without an
// answer, like a pooled conn that died), `refuse` answers 'E' (a relay
// status error: the target is unreachable).
type relayish struct {
	ln        net.Listener
	accepted  atomic.Int64
	dropFirst atomic.Int64
	refuse    atomic.Bool
}

func newRelayish(t *testing.T) *relayish {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &relayish{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r.accepted.Add(1)
			go func() {
				defer c.Close()
				b := make([]byte, 1)
				if _, err := io.ReadFull(c, b); err != nil {
					return
				}
				if r.dropFirst.Add(-1) >= 0 {
					return
				}
				if r.refuse.Load() {
					c.Write([]byte("E"))
					return
				}
				c.Write([]byte("K"))
				io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return r
}

type fakeTransport struct {
	opts chain.TransportOptions
}

func (f *fakeTransport) Dial(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}
func (f *fakeTransport) Handshake(ctx context.Context, c net.Conn) (net.Conn, error) { return c, nil }
func (f *fakeTransport) Connect(ctx context.Context, c net.Conn, network, address string) (net.Conn, error) {
	if _, err := c.Write([]byte("C")); err != nil {
		return nil, err
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(c, b); err != nil {
		return nil, err
	}
	if b[0] != 'K' {
		return nil, errors.New("503 target unreachable")
	}
	return c, nil
}
func (f *fakeTransport) Bind(ctx context.Context, c net.Conn, network, address string, opts ...connector.BindOption) (net.Listener, error) {
	return nil, connector.ErrBindUnsupported
}
func (f *fakeTransport) Multiplex() bool                  { return false }
func (f *fakeTransport) Options() *chain.TransportOptions { return &f.opts }
func (f *fakeTransport) Copy() chain.Transporter          { return f }

func TestMain(m *testing.M) {
	logger.SetDefault(xlogger.Nop())
	os.Exit(m.Run())
}

func setupWarmChain(t *testing.T, name string, size int) (*relayish, *chain.Node, *xchain.Chain) {
	t.Helper()
	r := newRelayish(t)
	node := chain.NewNode("node_1", r.ln.Addr().String(), chain.TransportNodeOption(&fakeTransport{}))
	h := xhop.NewHop(xhop.NodeOption(node), xhop.LoggerOption(xlogger.Nop()))
	c := xchain.NewChain(name)
	c.AddHop(h)
	registry.ReplaceChain(name, c)
	warmpool.Default.SetConfig(warmpool.Config{Chains: map[string]int{name: size}})
	t.Cleanup(func() {
		warmpool.Default.SetConfig(warmpool.Config{})
		registry.ChainRegistry().Unregister(name)
	})
	return r, node, c
}

func waitIdle(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if warmpool.Default.Stats().Idle == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("idle = %d, want %d", warmpool.Default.Stats().Idle, n)
}

func dialThrough(t *testing.T, c *xchain.Chain) (net.Conn, error) {
	rt := c.Route(context.Background(), "tcp", "10.0.0.1:443")
	return rt.Dial(context.Background(), "tcp", "10.0.0.1:443", chain.LoggerDialOption(xlogger.Nop()))
}

func TestRouteUsesPooledConn(t *testing.T) {
	r, _, c := setupWarmChain(t, "chains_9001", 2)
	waitIdle(t, 2)
	cc, err := dialThrough(t, c)
	if err != nil {
		t.Fatal(err)
	}
	cc.Write([]byte("hi"))
	b := make([]byte, 2)
	if _, err := io.ReadFull(cc, b); err != nil || string(b) != "hi" {
		t.Fatalf("stream through pooled conn: %q %v", b, err)
	}
	cc.Close()
	waitIdle(t, 2)
	if got := r.accepted.Load(); got != 3 {
		t.Fatalf("accepted %d, want 3 (2 pooled + 1 refill, no cold dial)", got)
	}
	rep := warmpool.Default.Report()
	if rep == nil || len(rep.Keys) != 1 || rep.Keys[0].WarmN != 1 || rep.Keys[0].MissN != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRouteRedialsDeadPooledConn(t *testing.T) {
	r, node, c := setupWarmChain(t, "chains_9002", 1)
	waitIdle(t, 1)
	r.dropFirst.Store(1) // the pooled conn dies when used
	cc, err := dialThrough(t, c)
	if err != nil {
		t.Fatalf("user saw an error for a dead pooled conn: %v", err)
	}
	cc.Write([]byte("ok"))
	b := make([]byte, 2)
	if _, err := io.ReadFull(cc, b); err != nil || string(b) != "ok" {
		t.Fatalf("stream after redial: %q %v", b, err)
	}
	cc.Close()
	if node.Marker().Count() != 0 {
		t.Fatalf("node fail-marked by a pooled conn (count %d)", node.Marker().Count())
	}
	rep := warmpool.Default.Report()
	k := rep.Keys[0]
	if k.DeadN != 1 || k.WarmN != 1 || k.WarmMs <= 0 {
		t.Fatalf("report = %+v", k)
	}
}

func TestRouteRelayErrorIsNotRedialed(t *testing.T) {
	r, _, c := setupWarmChain(t, "chains_9003", 1)
	waitIdle(t, 1)
	r.refuse.Store(true)
	before := r.accepted.Load()
	if _, err := dialThrough(t, c); err == nil {
		t.Fatal("relay error swallowed")
	}
	time.Sleep(50 * time.Millisecond)
	// Only the background refill dials; the stream did not redial cold.
	if got := r.accepted.Load() - before; got != 1 {
		t.Fatalf("%d new conns after a relay error, want 1 (refill)", got)
	}
	k := warmpool.Default.Report().Keys[0]
	if k.DeadN != 0 || k.WarmN != 1 {
		t.Fatalf("report = %+v", k)
	}
}

func TestRouteColdWhenNotConfigured(t *testing.T) {
	r := newRelayish(t)
	node := chain.NewNode("node_1", r.ln.Addr().String(), chain.TransportNodeOption(&fakeTransport{}))
	c := xchain.NewChain("chains_9004")
	c.AddHop(xhop.NewHop(xhop.NodeOption(node), xhop.LoggerOption(xlogger.Nop())))
	registry.ReplaceChain("chains_9004", c)
	defer registry.ChainRegistry().Unregister("chains_9004")
	cc, err := dialThrough(t, c)
	if err != nil {
		t.Fatal(err)
	}
	cc.Close()
	time.Sleep(30 * time.Millisecond)
	if r.accepted.Load() != 1 || warmpool.Default.Stats().Live != 0 {
		t.Fatalf("pool activity without config: accepted %d stats %+v", r.accepted.Load(), warmpool.Default.Stats())
	}
}
