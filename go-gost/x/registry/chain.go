package registry

import (
	"context"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/metadata"
	"github.com/go-gost/core/selector"
)

type chainRegistry struct {
	registry[chain.Chainer]
}

// ReplaceChain registers v under name in one atomic store (plan 165): a
// dial that looks the chain up meanwhile gets either the old or the new chain,
// never none (Unregister + Register left a window in which the router fell
// back to a direct dial). The old chain is not closed: connections it already
// made keep running.
func ReplaceChain(name string, v chain.Chainer) {
	if name == "" || v == nil {
		return
	}
	if r, ok := chainReg.(*chainRegistry); ok {
		r.m.Store(name, v)
		return
	}
	chainReg.Unregister(name)
	_ = chainReg.Register(name, v)
}

// LookupChain returns the registered chain object itself (not the lazy
// wrapper of Get), or nil.
func LookupChain(name string) chain.Chainer {
	if r, ok := chainReg.(*chainRegistry); ok {
		return r.get(name)
	}
	return nil
}

func (r *chainRegistry) Register(name string, v chain.Chainer) error {
	return r.registry.Register(name, v)
}

func (r *chainRegistry) Get(name string) chain.Chainer {
	if name != "" {
		return &chainWrapper{name: name, r: r}
	}
	return nil
}

func (r *chainRegistry) get(name string) chain.Chainer {
	return r.registry.Get(name)
}

type chainWrapper struct {
	name string
	r    *chainRegistry
}

func (w *chainWrapper) Marker() selector.Marker {
	v := w.r.get(w.name)
	if v == nil {
		return nil
	}
	if mi, ok := v.(selector.Markable); ok {
		return mi.Marker()
	}
	return nil
}

func (w *chainWrapper) Metadata() metadata.Metadata {
	v := w.r.get(w.name)
	if v == nil {
		return nil
	}

	if mi, ok := v.(metadata.Metadatable); ok {
		return mi.Metadata()
	}
	return nil
}

func (w *chainWrapper) Route(ctx context.Context, network, address string, opts ...chain.RouteOption) chain.Route {
	v := w.r.get(w.name)
	if v == nil {
		return nil
	}
	return v.Route(ctx, network, address, opts...)
}
