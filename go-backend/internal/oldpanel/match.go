package oldpanel

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Matching a 救命版 node with an FLVX node (the same machine runs both
// agents): a shared public IP literal, a shared host name, or an IP literal on
// one side that a host name on the other side resolves to. Two host names that
// merely resolve to the same address do NOT match (Cloudflare-proxied names
// share anycast addresses). Private, loopback, link-local and CGNAT addresses
// never match (two providers may hand out the same 10.x address).

// Addrs is the parsed address set of one node.
type Addrs struct {
	IPs   []netip.Addr // public literals
	Hosts []string     // lower-case host names
}

// ParseAddrs reads server_ip / server_ip_v4 / server_ip_v6 / extra_ips values
// (extra_ips: JSON string array or a comma / space separated list).
func ParseAddrs(values ...string) Addrs {
	var out Addrs
	seenIP := map[netip.Addr]bool{}
	seenHost := map[string]bool{}
	add := func(tok string) {
		tok = strings.Trim(strings.TrimSpace(tok), `"'`)
		if tok == "" {
			return
		}
		if ip, ok := parseIP(tok); ok {
			if PublicIP(ip) && !seenIP[ip] {
				seenIP[ip] = true
				out.IPs = append(out.IPs, ip)
			}
			return
		}
		if h := hostName(tok); h != "" && !seenHost[h] {
			seenHost[h] = true
			out.Hosts = append(out.Hosts, h)
		}
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") && strings.Contains(v, `"`) {
			var list []string
			if json.Unmarshal([]byte(v), &list) == nil {
				for _, t := range list {
					add(t)
				}
				continue
			}
		}
		for _, t := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
			add(t)
		}
	}
	return out
}

func parseIP(tok string) (netip.Addr, bool) {
	t := strings.TrimSuffix(strings.TrimPrefix(tok, "["), "]")
	if ip, err := netip.ParseAddr(t); err == nil {
		return ip.Unmap().WithZone(""), true
	}
	if ap, err := netip.ParseAddrPort(tok); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}

// hostName keeps a plausible DNS name (letters, digits, '-', '.', at least
// one dot), lower case, without a port or a trailing dot.
func hostName(tok string) string {
	h := strings.ToLower(strings.TrimSpace(tok))
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[:i], ":") {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	if len(h) < 3 || len(h) > 253 || !strings.Contains(h, ".") {
		return ""
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return ""
		}
	}
	return h
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PublicIP: an address that can identify one machine.
func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}

// Side is one node of either panel for Match.
type Side struct {
	ID    int64
	Addrs Addrs
}

// Lookup returns the cached addresses of a host name; done is false while
// the name has not been resolved yet (a failed lookup is done with no IPs).
type Lookup func(host string) (ips []netip.Addr, done bool)

// Match pairs old nodes with FLVX nodes. matched: old id → FLVX id (the
// first FLVX node in list order when several fit). pending: old ids that are
// unmatched while some host name involved is still being resolved.
func Match(flvx, old []Side, lookup Lookup) (matched map[int64]int64, pending map[int64]bool) {
	matched = map[int64]int64{}
	pending = map[int64]bool{}
	order := make(map[int64]int, len(flvx))
	byIP := map[netip.Addr][]int64{}       // FLVX literals
	byHost := map[string][]int64{}         // FLVX host names
	byResolved := map[netip.Addr][]int64{} // what FLVX host names resolve to
	flvxPending := false
	for i, f := range flvx {
		order[f.ID] = i
		for _, ip := range f.Addrs.IPs {
			byIP[ip] = append(byIP[ip], f.ID)
		}
		for _, h := range f.Addrs.Hosts {
			byHost[h] = append(byHost[h], f.ID)
			ips, done := lookup(h)
			if !done {
				flvxPending = true
			}
			for _, ip := range ips {
				if PublicIP(ip) {
					byResolved[ip.Unmap()] = append(byResolved[ip.Unmap()], f.ID)
				}
			}
		}
	}
	for _, o := range old {
		best, found := int64(0), false
		take := func(ids []int64) {
			for _, id := range ids {
				if !found || order[id] < order[best] {
					best, found = id, true
				}
			}
		}
		oldPending := false
		for _, ip := range o.Addrs.IPs {
			take(byIP[ip])
			take(byResolved[ip])
		}
		for _, h := range o.Addrs.Hosts {
			take(byHost[h])
			ips, done := lookup(h)
			if !done {
				oldPending = true
			}
			for _, ip := range ips {
				take(byIP[ip.Unmap()])
			}
		}
		switch {
		case found:
			matched[o.ID] = best
		case oldPending || (flvxPending && (len(o.Addrs.IPs) > 0 || len(o.Addrs.Hosts) > 0)):
			pending[o.ID] = true
		}
	}
	return matched, pending
}

// ─── DNS cache ──────────────────────────────────────────────────────────

// Resolver is net.Resolver.LookupNetIP (injected in tests).
type Resolver func(ctx context.Context, host string) ([]netip.Addr, error)

type dnsEntry struct {
	ips []netip.Addr
	at  time.Time
	ok  bool
}

// DNSCache resolves host names off the request path: Want starts one batch
// in the background (single flight, 4 lookups at a time, 2 s each) for names
// that are missing or expired; Lookup only reads the cache (an expired entry
// keeps serving its old answer until the refresh lands).
type DNSCache struct {
	Resolve  Resolver
	TTL      time.Duration // success
	FailTTL  time.Duration // failure
	Timeout  time.Duration
	Parallel int
	Now      func() time.Time

	mu      sync.Mutex
	entries map[string]dnsEntry
	done    chan struct{} // open while a batch runs
}

func NewDNSCache() *DNSCache {
	return &DNSCache{
		Resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		TTL: 10 * time.Minute, FailTTL: 2 * time.Minute, Timeout: 2 * time.Second, Parallel: 4,
	}
}

func (c *DNSCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Lookup reads the cache.
func (c *DNSCache) Lookup(host string) ([]netip.Addr, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[host]
	if !ok {
		return nil, false
	}
	return e.ips, true
}

// Want makes sure hosts get (re)resolved; names no longer wanted are
// forgotten. It never blocks.
func (c *DNSCache) Want(hosts []string) {
	now := c.now()
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]dnsEntry{}
	}
	wanted := make(map[string]bool, len(hosts))
	var todo []string
	for _, h := range hosts {
		if wanted[h] {
			continue
		}
		wanted[h] = true
		e, ok := c.entries[h]
		ttl := c.TTL
		if ok && !e.ok {
			ttl = c.FailTTL
		}
		if !ok || now.Sub(e.at) >= ttl {
			todo = append(todo, h)
		}
	}
	for h := range c.entries {
		if !wanted[h] {
			delete(c.entries, h)
		}
	}
	if len(todo) == 0 || c.done != nil {
		c.mu.Unlock()
		return
	}
	if len(todo) > 256 {
		todo = todo[:256]
	}
	done := make(chan struct{})
	c.done = done
	c.mu.Unlock()
	go c.run(todo, done)
}

func (c *DNSCache) run(todo []string, done chan struct{}) {
	defer func() {
		c.mu.Lock()
		c.done = nil
		c.mu.Unlock()
		close(done)
	}()
	par := c.Parallel
	if par <= 0 {
		par = 4
	}
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for _, h := range todo {
		sem <- struct{}{}
		wg.Add(1)
		go func(h string) {
			defer func() { <-sem; wg.Done() }()
			ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
			ips, err := c.Resolve(ctx, h)
			cancel()
			e := dnsEntry{at: c.now(), ok: err == nil}
			for _, ip := range ips {
				if ip = ip.Unmap().WithZone(""); PublicIP(ip) {
					e.ips = append(e.ips, ip)
				}
			}
			c.mu.Lock()
			if c.entries != nil {
				c.entries[h] = e
			}
			c.mu.Unlock()
		}(h)
	}
	wg.Wait()
}

// Wait blocks until the running batch (if any) is done or d passes.
func (c *DNSCache) Wait(d time.Duration) {
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	if done == nil {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}
