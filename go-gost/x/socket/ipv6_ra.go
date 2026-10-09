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
}

func (d *ipv6RADetector) detect() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status, d.detail = nftables.IPv6RAStatus()
}

func (d *ipv6RADetector) run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		d.detect()
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
