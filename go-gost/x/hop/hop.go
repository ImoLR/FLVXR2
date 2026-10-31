package hop

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/bypass"
	"github.com/go-gost/core/chain"
	"github.com/go-gost/core/hop"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/routing"
	"github.com/go-gost/core/selector"
	"github.com/go-gost/x/config"
	node_parser "github.com/go-gost/x/config/parsing/node"
	ctxvalue "github.com/go-gost/x/ctx"
	"github.com/go-gost/x/internal/loader"
)

type options struct {
	name        string
	nodes       []*chain.Node
	bypass      bypass.Bypass
	selector    selector.Selector[*chain.Node]
	fileLoader  loader.Loader
	redisLoader loader.Loader
	httpLoader  loader.Loader
	period      time.Duration
	logger      logger.Logger
	// Plan 180: per-destination preferred node order (see dest_order.go).
	destOrder map[string][]string
}

type Option func(*options)

func NameOption(name string) Option {
	return func(o *options) {
		o.name = name
	}
}

func NodeOption(nodes ...*chain.Node) Option {
	return func(o *options) {
		o.nodes = nodes
	}
}
func BypassOption(bp bypass.Bypass) Option {
	return func(o *options) {
		o.bypass = bp
	}
}

func SelectorOption(s selector.Selector[*chain.Node]) Option {
	return func(o *options) {
		o.selector = s
	}
}

func ReloadPeriodOption(period time.Duration) Option {
	return func(opts *options) {
		opts.period = period
	}
}

func FileLoaderOption(fileLoader loader.Loader) Option {
	return func(opts *options) {
		opts.fileLoader = fileLoader
	}
}

func RedisLoaderOption(redisLoader loader.Loader) Option {
	return func(opts *options) {
		opts.redisLoader = redisLoader
	}
}

func HTTPLoaderOption(httpLoader loader.Loader) Option {
	return func(opts *options) {
		opts.httpLoader = httpLoader
	}
}
func LoggerOption(logger logger.Logger) Option {
	return func(opts *options) {
		opts.logger = logger
	}
}

type chainHop struct {
	nodes      []*chain.Node
	mu         sync.RWMutex
	cancelFunc context.CancelFunc
	options    options
	// chainHop: this hop is a hop of a chain (set by the chain parser), not
	// a forwarder's target list — see Select (plan 180 r2).
	chainHop atomic.Bool
}

// MarkChainHop flags h as a hop of a chain: when every node is fail-marked
// it still selects one (the least-failed) instead of nil, so the chain
// never degrades into a direct dial. Forwarder hops (a rule's targets) are
// not marked and keep returning nil.
func MarkChainHop(h hop.Hop) {
	if p, ok := h.(*chainHop); ok && p != nil {
		p.chainHop.Store(true)
	}
}

// leastFailedNode: the node with the fewest recorded failures, in order on
// ties (fail mark times only have second resolution, the count always moves).
func leastFailedNode(nodes []*chain.Node) *chain.Node {
	var best *chain.Node
	var bestCount int64
	for _, node := range nodes {
		var count int64
		if marker := node.Marker(); marker != nil {
			count = marker.Count()
		}
		if best == nil || count < bestCount {
			best, bestCount = node, count
		}
	}
	return best
}

func NewHop(opts ...Option) hop.Hop {
	var options options
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	ctx, cancel := context.WithCancel(context.TODO())
	p := &chainHop{
		cancelFunc: cancel,
		options:    options,
	}

	if err := p.reload(ctx); err != nil {
		options.logger.Warnf("reload: %v", err)
	}
	if p.options.period > 0 {
		go p.periodReload(ctx)
	}

	return p
}

func (p *chainHop) Nodes() []*chain.Node {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nodes
}

func (p *chainHop) Select(ctx context.Context, opts ...hop.SelectOption) *chain.Node {
	var options hop.SelectOptions
	for _, opt := range opts {
		opt(&options)
	}

	log := p.options.logger

	// hop level bypass
	if p.options.bypass != nil &&
		p.options.bypass.Contains(ctx, options.Network, options.Addr, bypass.WithHostOpton(options.Host)) {
		return nil
	}

	// Get list of nodes to exclude (for failover retry)
	excludeNodes := ctxvalue.ExcludeNodesFromContext(ctx)
	excludeSet := make(map[string]bool)
	for _, addr := range excludeNodes {
		excludeSet[addr] = true
	}

	// Debug logging for failover analysis
	log.Debugf("[hop.Select] excludeNodes=%v, totalNodes=%d", excludeNodes, len(p.Nodes()))

	p.mu.RLock()
	all, destOrder := p.nodes, p.options.destOrder
	p.mu.RUnlock()

	var nodes []*chain.Node
	for _, node := range all {
		if node == nil {
			continue
		}

		// Skip nodes in the exclude list (failover retry)
		if excludeSet[node.Addr] || excludeSet[node.Name] {
			log.Debugf("node %s(%s) excluded for failover retry", node.Name, node.Addr)
			continue
		}

		// node level bypass
		if node.Options().Bypass != nil &&
			node.Options().Bypass.Contains(ctx, options.Network, options.Addr, bypass.WithHostOpton(options.Host)) {
			continue
		}

		if matcher := node.Options().Matcher; matcher != nil {
			req := routing.Request{
				ClientIP: options.ClientIP,
				Host:     options.Host,
				Protocol: options.Protocol,
				Method:   options.Method,
				Path:     options.Path,
				Query:    options.Query,
				Header:   options.Header,
			}
			if !matcher.Match(&req) {
				continue
			}
			log.Debugf("node %s match request %s %s, priority %d", node.Name, req.Protocol, req.Host, node.Options().Priority)
		} else {
			if !p.isEligible(node, &options) {
				continue
			}
		}

		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Options().Priority > nodes[j].Options().Priority
	})

	if nodes[0].Options().Priority > 0 {
		return nodes[0]
	}

	// Plan 180: the panel's preferred order for this destination (规则最优).
	if order := destOrderFor(destOrder, options.Host, options.Addr); order != nil {
		nodes = applyDestOrder(nodes, order)
	}

	// Use selector with FailFilter for proper failover.
	// FailFilter will exclude recently-failed nodes, allowing traffic to
	// be routed to healthy alternatives.
	// Note: FailFilter has a safety guard (len <= 1 returns as-is) to ensure
	// the last remaining node is never permanently blocked.
	if s := p.options.selector; s != nil {
		log.Debugf("[hop.Select] calling selector.Select with %d nodes", len(nodes))
		if node := s.Select(ctx, nodes...); node != nil {
			log.Debugf("[hop.Select] selected node=%s addr=%s", node.Name, node.Addr)
			return node
		}
		// All nodes filtered out by FailFilter - all are marked as failed.
		if p.chainHop.Load() {
			// Plan 180 r2: a chain hop never answers nil here. Chain.Route
			// would then return a route without this hop and the router
			// would dial the destination directly — bypassing the next hop
			// (e.g. a 中转 reaching the landing itself, exposing its own IP).
			// Try them in order, the least-failed first: a failure adds
			// to its count, so the router's retries walk through all of
			// them and the dial fails when they all fail.
			return leastFailedNode(nodes)
		}
		// Return nil to signal "no healthy nodes available" to the caller.
		// The handler's retry loop will handle this appropriately.
		log.Debugf("all %d nodes filtered out by FailFilter, no healthy nodes available", len(nodes))
		return nil
	}

	// Fallback: return first node if no selector configured
	return nodes[0]
}

func (p *chainHop) isEligible(node *chain.Node, opts *hop.SelectOptions) bool {
	if node == nil {
		return false
	}
	if node.Options().Filter == nil {
		return true
	}

	if !p.checkHost(opts.Host, node) || !p.checkProtocol(opts.Protocol, node) || !p.checkPath(opts.Path, node) {
		return false
	}
	return true
}

func (p *chainHop) checkHost(host string, node *chain.Node) bool {
	var vhost string
	if filter := node.Options().Filter; filter != nil {
		vhost = filter.Host
	}
	if vhost == "" { // backup node
		return true
	}

	if host == "" {
		return false
	}

	if v, _, _ := net.SplitHostPort(host); v != "" {
		host = v
	}

	if vhost == host || vhost[0] == '.' && strings.HasSuffix(host, vhost[1:]) {
		return true
	}

	return false
}

func (p *chainHop) checkProtocol(protocol string, node *chain.Node) bool {
	var prot string
	if filter := node.Options().Filter; filter != nil {
		prot = filter.Protocol
	}
	if prot == "" {
		return true
	}
	return prot == protocol
}

func (p *chainHop) checkPath(path string, node *chain.Node) bool {
	var pathFilter string
	if filter := node.Options().Filter; filter != nil {
		pathFilter = filter.Path
	}

	if pathFilter == "" {
		return true
	}

	return strings.HasPrefix(path, pathFilter)
}

func (p *chainHop) periodReload(ctx context.Context) error {
	period := p.options.period
	if period < time.Second {
		period = time.Second
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.reload(ctx); err != nil {
				p.options.logger.Warnf("reload: %v", err)
				// return err
			}
			p.options.logger.Debug("hop reload done")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (p *chainHop) reload(ctx context.Context) (err error) {
	nodes := p.options.nodes

	nl, err := p.load(ctx)

	nodes = append(nodes, nl...)

	p.options.logger.Debugf("load items %d", len(nodes))

	p.mu.Lock()
	defer p.mu.Unlock()

	p.nodes = nodes

	return
}

func (p *chainHop) load(ctx context.Context) (nodes []*chain.Node, err error) {
	if loader := p.options.fileLoader; loader != nil {
		r, er := loader.Load(ctx)
		if er != nil {
			p.options.logger.Warnf("file loader: %v", er)
		}
		nodes, _ = p.parseNode(r)
	}

	if loader := p.options.redisLoader; loader != nil {
		r, er := loader.Load(ctx)
		if er != nil {
			p.options.logger.Warnf("redis loader: %v", er)
		}
		ns, _ := p.parseNode(r)
		nodes = append(nodes, ns...)
	}

	if loader := p.options.httpLoader; loader != nil {
		r, er := loader.Load(ctx)
		if er != nil {
			p.options.logger.Warnf("http loader: %v", er)
		}
		if ns, _ := p.parseNode(r); ns != nil {
			nodes = append(nodes, ns...)
		}
	}

	return
}

func (p *chainHop) parseNode(r io.Reader) ([]*chain.Node, error) {
	if r == nil {
		return nil, nil
	}

	var ncs []*config.NodeConfig
	if err := json.NewDecoder(r).Decode(&ncs); err != nil {
		return nil, err
	}

	var nodes []*chain.Node
	for _, nc := range ncs {
		if nc == nil {
			continue
		}

		node, err := node_parser.ParseNode(p.options.name, nc, logger.Default())
		if err != nil {
			return nodes, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ReorderNodes puts the hop's nodes into the given address order in place
// (plan 165, 最优): the node objects (transports with their mux sessions,
// fail markers) are kept, so a reorder opens no connection and leaves no
// window without nodes. It only applies when addrs is a permutation of the
// current node addresses (unique) of a static hop (no loaders, no periodic
// reload); otherwise it changes nothing and returns false.
func (p *chainHop) ReorderNodes(addrs []string) bool {
	if p == nil || p.options.period > 0 || p.options.fileLoader != nil || p.options.redisLoader != nil || p.options.httpLoader != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(addrs) != len(p.nodes) || len(p.options.nodes) != len(p.nodes) {
		return false
	}
	byAddr := make(map[string]*chain.Node, len(p.nodes))
	for _, node := range p.nodes {
		if node == nil {
			return false
		}
		if _, dup := byAddr[node.Addr]; dup {
			return false
		}
		byAddr[node.Addr] = node
	}
	ordered := make([]*chain.Node, 0, len(addrs))
	for _, addr := range addrs {
		node, ok := byAddr[addr]
		if !ok {
			return false
		}
		delete(byAddr, addr)
		ordered = append(ordered, node)
	}
	p.nodes = ordered
	p.options.nodes = append([]*chain.Node(nil), ordered...)
	return true
}

func (p *chainHop) Close() error {
	p.cancelFunc()
	if p.options.fileLoader != nil {
		p.options.fileLoader.Close()
	}
	if p.options.redisLoader != nil {
		p.options.redisLoader.Close()
	}
	return nil
}
