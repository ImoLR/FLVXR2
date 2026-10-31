package hop

import (
	"net"
	"strings"

	"github.com/go-gost/core/chain"
)

// Plan 180 (规则最优): a hop may carry a per-destination preferred node order,
// set by the panel as hop metadata
//
//	"destOrder": {"host:port": ["<node addr>", "<node addr>", …], …}
//
// When the destination being dialed through the hop matches a key, the
// eligible nodes are put into that order before the selector runs (fail
// filter + fifo), so failover is unchanged: a node marked failed is skipped
// for that destination as well. Any other destination keeps the hop's normal
// order. The lookup is one map access per dial; nothing is allocated unless
// the destination matches.

// DestOrderMetadataKey is the hop metadata key of the per-destination order.
const DestOrderMetadataKey = "destOrder"

// NormalizeDestKey normalises a destination like the panel does: lower-case
// host without brackets joined with the port. Inputs without a port are
// returned trimmed and lower-cased.
func NormalizeDestKey(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.ToLower(addr)
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if i := strings.IndexByte(host, '%'); i >= 0 && strings.Contains(host, ":") {
		host = host[:i] // IPv6 zone: not part of the destination identity
	}
	return net.JoinHostPort(host, port)
}

// ParseDestOrder reads the destOrder metadata value (JSON/YAML decoded:
// map[string]any of []any of strings). Invalid entries are dropped; an empty
// result is nil.
func ParseDestOrder(v any) map[string][]string {
	var raw map[string]any
	switch m := v.(type) {
	case map[string]any:
		raw = m
	case map[any]any:
		raw = make(map[string]any, len(m))
		for k, val := range m {
			if s, ok := k.(string); ok {
				raw[s] = val
			}
		}
	case map[string][]string:
		raw = make(map[string]any, len(m))
		for k, val := range m {
			raw[k] = val
		}
	default:
		return nil
	}
	out := make(map[string][]string, len(raw))
	for k, val := range raw {
		key := NormalizeDestKey(k)
		if key == "" {
			continue
		}
		var addrs []string
		switch list := val.(type) {
		case []any:
			for _, item := range list {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					addrs = append(addrs, strings.TrimSpace(s))
				}
			}
		case []string:
			for _, s := range list {
				if strings.TrimSpace(s) != "" {
					addrs = append(addrs, strings.TrimSpace(s))
				}
			}
		}
		if len(addrs) > 0 {
			out[key] = addrs
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DestOrderOption sets the hop's per-destination order (plan 180).
func DestOrderOption(m map[string][]string) Option {
	return func(o *options) {
		o.destOrder = m
	}
}

// SetDestOrder replaces the per-destination order in place (UpdateChains with
// only node order / destOrder changes). Same static-hop rule as ReorderNodes.
func (p *chainHop) SetDestOrder(m map[string][]string) bool {
	if p == nil || p.options.period > 0 || p.options.fileLoader != nil || p.options.redisLoader != nil || p.options.httpLoader != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(m) == 0 {
		m = nil
	}
	p.options.destOrder = m
	return true
}

// DestOrder returns the current per-destination order (read-only).
func (p *chainHop) DestOrder() map[string][]string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.options.destOrder
}

// destOrderFor returns the preferred node order for the dialed destination:
// the original address (SelectOptions.Host, as the router saw it before any
// resolve) first, then the address actually dialed.
func destOrderFor(m map[string][]string, host, addr string) []string {
	if len(m) == 0 {
		return nil
	}
	if host != "" {
		// Fast path: the entry dials exactly the panel's (normalised) string.
		if order, ok := m[host]; ok {
			return order
		}
		if order, ok := m[NormalizeDestKey(host)]; ok {
			return order
		}
	}
	if addr != "" && addr != host {
		if order, ok := m[NormalizeDestKey(addr)]; ok {
			return order
		}
	}
	return nil
}

// applyDestOrder puts the nodes listed in order first (in that order), then
// the others in their current order. nodes is not modified.
func applyDestOrder(nodes []*chain.Node, order []string) []*chain.Node {
	if len(nodes) < 2 || len(order) == 0 {
		return nodes
	}
	out := make([]*chain.Node, 0, len(nodes))
	used := make([]bool, len(nodes))
	for _, addr := range order {
		for i, n := range nodes {
			if !used[i] && n.Addr == addr {
				out = append(out, n)
				used[i] = true
				break
			}
		}
	}
	if len(out) == 0 {
		return nodes
	}
	for i, n := range nodes {
		if !used[i] {
			out = append(out, n)
		}
	}
	return out
}
