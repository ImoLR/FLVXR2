package socket

import (
	"context"
	"net"
	"sync"
	"time"
)

type egressDialer func(context.Context, string, string) (net.Conn, error)

type egressDetector struct {
	mu   sync.RWMutex
	last string
}

func detectEgress(ctx context.Context, dial egressDialer) string {
	check := func(network string, targets []string) bool {
		for _, target := range targets {
			if ctx.Err() != nil {
				return false
			}
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			conn, err := dial(probeCtx, network, target)
			cancel()
			if err == nil {
				conn.Close()
				return true
			}
		}
		return false
	}
	v4 := check("tcp4", []string{"223.5.5.5:443", "1.1.1.1:443", "8.8.8.8:443"})
	v6 := check("tcp6", []string{"[2400:3200::1]:443", "[2606:4700:4700::1111]:443", "[2001:4860:4860::8888]:443"})
	switch {
	case v4 && v6:
		return "dual"
	case v4:
		return "v4"
	case v6:
		return "v6"
	default:
		return ""
	}
}

// Detection has its own loop so unreachable targets never delay telemetry.
func (d *egressDetector) run(ctx context.Context, dial egressDialer) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		family := detectEgress(ctx, dial)
		if ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		d.last = family
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *egressDetector) family() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.last
}
