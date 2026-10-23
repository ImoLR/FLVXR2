package socket

import (
	"context"
	"sync"
	"time"

	"github.com/go-gost/x/nftables"
)

type ipv6RADetector struct {
	mu             sync.RWMutex
	status, detail string
	onTick         func() // runs after each 10-minute check (node DDNS address refresh)
}

func (d *ipv6RADetector) detect() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status, d.detail = nftables.IPv6RAStatus()
}

func (d *ipv6RADetector) run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	first := true
	for {
		d.detect()
		if d.onTick != nil && !first {
			d.onTick()
		}
		first = false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *ipv6RADetector) result() (string, string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status, d.detail
}
