package socket

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/go-gost/core/logger"
	xchain "github.com/go-gost/x/chain"
	"github.com/go-gost/x/config"
	parser "github.com/go-gost/x/config/parsing/chain"
	xhop "github.com/go-gost/x/hop"
	"github.com/go-gost/x/registry"
)

func createChain(req createChainRequest) error {

	name := strings.TrimSpace(req.Data.Name)
	if name == "" {
		return errors.New("chain name is required")
	}
	req.Data.Name = name

	if registry.ChainRegistry().IsRegistered(name) {
		return errors.New("chain " + name + " already exists")
	}

	v, err := parser.ParseChain(&req.Data, logger.Default())
	if err != nil {
		return errors.New("create chain " + name + " failed: " + err.Error())
	}

	if err := registry.ChainRegistry().Register(name, v); err != nil {
		return errors.New("chain " + name + " already exists")
	}

	config.OnUpdate(func(c *config.Config) error {
		c.Chains = append(c.Chains, &req.Data)
		return nil
	})

	return nil
}

// chainSwapVersion is advertised in the WebSocket handshake (`chainSwap=1`,
// plan 165): UpdateChains swaps a chain without a window in which dials find
// no chain (and would fall back to a direct dial), so the panel may reorder
// the nodes of a 最优 hop on this agent.
const chainSwapVersion = 1

// destOrderVersion is advertised in the WebSocket handshake (`destOrder=1`,
// plan 180 规则最优): chain hops honour the `destOrder` metadata (a preferred
// node order per dialed destination) and UpdateChains changes it in place.
// The panel only sends destOrder to agents that advertise it.
const destOrderVersion = 1

// updateChain replaces a chain without a gap (plan 165). The new config is
// parsed first. When it differs from the running one only in the order of
// the nodes (the 最优 reorder), the running hop's nodes are reordered in
// place: the same node objects, so no new connection, mux sessions and fail
// marks stay. Any other change swaps in the parsed chain with one atomic
// store. Connections made through the old chain keep running.
func updateChain(req updateChainRequest) error {
	name := strings.TrimSpace(req.Chain)
	if name == "" {
		name = strings.TrimSpace(req.Data.Name)
	}
	if name == "" {
		return errors.New("chain name is required")
	}
	req.Data.Name = name

	var old *config.ChainConfig
	config.OnUpdate(func(c *config.Config) error {
		for _, ch := range c.Chains {
			if ch != nil && ch.Name == name {
				old = ch
				break
			}
		}
		return nil
	})

	// ParseChain fills node defaults in req.Data the same way it did for the
	// stored config, so both compare like for like below.
	v, err := parser.ParseChain(&req.Data, logger.Default())
	if err != nil {
		return errors.New("create chain " + name + " failed: " + err.Error())
	}
	if v == nil {
		return errors.New("create chain " + name + " failed: empty chain")
	}
	if !reorderChainInPlace(name, old, &req.Data) {
		registry.ReplaceChain(name, v)
	}

	config.OnUpdate(func(c *config.Config) error {
		found := false
		for i := range c.Chains {
			if c.Chains[i] != nil && c.Chains[i].Name == name {
				c.Chains[i] = &req.Data
				found = true
				break
			}
		}
		if !found {
			c.Chains = append(c.Chains, &req.Data)
		}
		return nil
	})

	return nil
}

type nodeReorderer interface {
	ReorderNodes(addrs []string) bool
}

type destOrderSetter interface {
	SetDestOrder(m map[string][]string) bool
}

// reorderChainInPlace reorders the registered chain's hop nodes to next's
// order when next equals old except for the node order (and node names,
// which follow the position). It returns false, changing nothing, when that
// is not the case or the running chain cannot be reordered.
func reorderChainInPlace(name string, old, next *config.ChainConfig) bool {
	if old == nil || next == nil || len(next.Hops) == 0 || !sameChainExceptNodeOrder(old, next) {
		return false
	}
	running, ok := registry.LookupChain(name).(*xchain.Chain)
	if !ok || running == nil {
		return false
	}
	hops := running.Hops()
	if len(hops) != len(next.Hops) {
		return false
	}
	orders := make([][]string, len(hops))
	destOrders := make([]map[string][]string, len(hops))
	for i, h := range hops {
		if _, ok := h.(nodeReorderer); !ok || next.Hops[i] == nil {
			return false
		}
		for _, n := range next.Hops[i].Nodes {
			if n == nil {
				return false
			}
			orders[i] = append(orders[i], n.Addr)
		}
		// Plan 180: a destOrder change rides the same in-place path.
		destOrders[i] = hopDestOrder(next.Hops[i])
		if _, ok := h.(destOrderSetter); !ok && (destOrders[i] != nil || hopDestOrder(old.Hops[i]) != nil) {
			return false
		}
	}
	for i, h := range hops {
		// A hop refusing the order (only possible for a hop that is not a plain
		// static node list) leaves the caller to swap in the parsed chain.
		if !h.(nodeReorderer).ReorderNodes(orders[i]) {
			return false
		}
		if setter, ok := h.(destOrderSetter); ok && !setter.SetDestOrder(destOrders[i]) {
			return false
		}
	}
	return true
}

func hopDestOrder(h *config.HopConfig) map[string][]string {
	if h == nil || h.Metadata == nil {
		return nil
	}
	return xhop.ParseDestOrder(h.Metadata[xhop.DestOrderMetadataKey])
}

// sameChainExceptNodeOrder compares two chain configs with each hop's nodes
// sorted by address and node names ignored.
func sameChainExceptNodeOrder(a, b *config.ChainConfig) bool {
	ca, okA := canonicalChainConfig(a)
	cb, okB := canonicalChainConfig(b)
	return okA && okB && bytes.Equal(ca, cb)
}

func canonicalChainConfig(c *config.ChainConfig) ([]byte, bool) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, false
	}
	var cp config.ChainConfig
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, false
	}
	for _, h := range cp.Hops {
		if h == nil {
			continue
		}
		// Plan 180: destOrder is applied in place as well (SetDestOrder).
		if h.Metadata != nil {
			delete(h.Metadata, xhop.DestOrderMetadataKey)
			if len(h.Metadata) == 0 {
				h.Metadata = nil
			}
		}
		seen := make(map[string]struct{}, len(h.Nodes))
		for _, n := range h.Nodes {
			if n == nil {
				return nil, false
			}
			if _, dup := seen[n.Addr]; dup {
				return nil, false
			}
			seen[n.Addr] = struct{}{}
			n.Name = ""
		}
		sort.SliceStable(h.Nodes, func(i, j int) bool { return h.Nodes[i].Addr < h.Nodes[j].Addr })
	}
	out, err := json.Marshal(&cp)
	return out, err == nil
}

func deleteChain(req deleteChainRequest) error {

	name := strings.TrimSpace(req.Chain)

	if registry.ChainRegistry().IsRegistered(name) {
		registry.ChainRegistry().Unregister(name)
	}

	config.OnUpdate(func(c *config.Config) error {
		chains := c.Chains
		c.Chains = nil
		for _, s := range chains {
			if s.Name == name {
				continue
			}
			c.Chains = append(c.Chains, s)
		}
		return nil
	})

	return nil
}

type createChainRequest struct {
	Data config.ChainConfig `json:"data"`
}

type updateChainRequest struct {
	Chain string             `json:"chain"`
	Data  config.ChainConfig `json:"data"`
}

type deleteChainRequest struct {
	Chain string `json:"chain"`
}
