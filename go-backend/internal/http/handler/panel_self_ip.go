package handler

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// Plan 225 (P2-2): the panel host's own public addresses.
//
// The panel runs in a bridge-networked container behind Cloudflare: neither
// its interfaces (container addresses) nor its configured domain (Cloudflare
// addresses) show the host's public IPs, so a chain upstream pointing at the
// panel host itself was accepted. The panel learns its public egress
// addresses (IPv4 and IPv6) from Cloudflare's trace endpoint: one request per
// family at start and every 6 h, one at a time, results cached in memory.
// Lookups never wait for the network.

const panelSelfIPRefresh = 6 * time.Hour

var panelSelfIPURLs = []string{
	"https://1.1.1.1/cdn-cgi/trace",
	"https://[2606:4700:4700::1111]/cdn-cgi/trace",
}

type panelSelfIPCache struct {
	mu      sync.Mutex
	addrs   []netip.Addr
	at      time.Time
	running bool
	// fetch is replaced in tests.
	fetch func(ctx context.Context) []netip.Addr
}

var panelSelfIPs = &panelSelfIPCache{}

// get returns the cached addresses and starts a background refresh when the
// cache is empty or older than panelSelfIPRefresh.
func (c *panelSelfIPCache) get() []netip.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running && (c.at.IsZero() || time.Since(c.at) > panelSelfIPRefresh) {
		c.running = true
		go c.refresh()
	}
	return append([]netip.Addr(nil), c.addrs...)
}

func (c *panelSelfIPCache) refresh() {
	fetch := c.fetch
	if fetch == nil {
		fetch = fetchPanelSelfIPs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	addrs := fetch(ctx)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	c.at = time.Now()
	if len(addrs) > 0 {
		c.addrs = addrs
	} else if len(c.addrs) == 0 {
		// Nothing learned: retry in 10 minutes instead of 6 hours.
		c.at = time.Now().Add(-panelSelfIPRefresh + 10*time.Minute)
	}
}

func fetchPanelSelfIPs(ctx context.Context) []netip.Addr {
	if testing.Testing() {
		return nil // no outbound requests from unit/contract tests
	}
	client := &http.Client{Timeout: 8 * time.Second}
	var out []netip.Addr
	for _, u := range panelSelfIPURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		if a, ok := parseTraceIP(io.LimitReader(resp.Body, 4096)); ok {
			out = append(out, a)
		}
		resp.Body.Close()
	}
	return out
}

// parseTraceIP reads the "ip=" line of a cdn-cgi/trace body.
func parseTraceIP(r io.Reader) (netip.Addr, bool) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if v, ok := strings.CutPrefix(line, "ip="); ok {
			a, err := netip.ParseAddr(strings.TrimSpace(v))
			if err == nil && a.IsGlobalUnicast() && !a.IsPrivate() {
				return a.Unmap(), true
			}
		}
	}
	return netip.Addr{}, false
}
