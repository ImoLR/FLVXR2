package chain

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/hop"
	"github.com/go-gost/core/logger"
	xnet "github.com/go-gost/x/internal/net"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
	"github.com/go-gost/x/warmpool"
)

// chainFailWindow: a chain hop node fail-marked within this window is skipped
// by the selector (the panel's failTimeout, plan 180 r2), so it is not warmed.
const chainFailWindow = 60 * time.Second

func init() {
	warmpool.Default.SetSource(warmpool.KindChain, warmChainSource{})
}

// warmChainSource gives the warm pool (plan 244) the next-hop nodes of a
// registered single-hop chain in their current order (the panel's / 最优
// order) and dials one of them exactly like a stream's connect, without
// touching the node's fail marker.
type warmChainSource struct{}

func warmHop(name string) (hop.Hop, bool) {
	c, ok := registry.LookupChain(name).(*Chain)
	if !ok || c == nil || len(c.hops) != 1 {
		return nil, false
	}
	return c.hops[0], true
}

func (warmChainSource) Targets(name string) ([]warmpool.Target, bool) {
	h, ok := warmHop(name)
	if !ok {
		return nil, false
	}
	nl, ok := h.(hop.NodeList)
	if !ok {
		return nil, false
	}
	nodes := nl.Nodes()
	usable := make([]*chain.Node, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if tr := n.Options().Transport; tr == nil || tr.Multiplex() {
			continue
		}
		usable = append(usable, n)
	}
	return warmpool.NodeTargets(usable, chainFailWindow), true
}

func (warmChainSource) Dial(ctx context.Context, name, addr string) (net.Conn, error) {
	h, ok := warmHop(name)
	if !ok {
		return nil, errors.New("chain not registered")
	}
	nl, ok := h.(hop.NodeList)
	if !ok {
		return nil, errors.New("chain hop has no node list")
	}
	for _, n := range nl.Nodes() {
		if n != nil && n.Addr == addr {
			return dialNodeTransport(ctx, n, warmLogger())
		}
	}
	return nil, errors.New("node not in chain")
}

func warmLogger() logger.Logger {
	if l := logger.Default(); l != nil {
		return l
	}
	return xlogger.Nop()
}

// dialNodeTransport: resolve + Transport.Dial + Handshake (TCP + TLS), the
// first half of connect, with no marker or metric side effects.
func dialNodeTransport(ctx context.Context, node *chain.Node, log logger.Logger) (net.Conn, error) {
	addr, err := xnet.Resolve(ctx, "ip", node.Addr, node.Options().Resolver, node.Options().HostMapper, log)
	if err != nil {
		return nil, err
	}
	tr := node.Options().Transport
	cc, err := tr.Dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	cn, err := tr.Handshake(ctx, cc)
	if err != nil {
		cc.Close()
		return nil, err
	}
	return cn, nil
}
