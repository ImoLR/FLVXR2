package socket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/x/dnscache"
)

// Plan 240: the local DNS cache as a standing service. The agent only LOOKS (≤ once per 10 min, local files
// and /proc, no network) and reports dns_cache in the metric; it changes system DNS only when the panel sends
// DNSCacheEnsure (「修复 DNS 缓存」 button, node becoming a native landing).

const dnsCacheInterval = 10 * time.Minute

// dnsCacheMinGap guards against a panel (or a stuck button) re-running ensure back to back.
const dnsCacheMinGap = 20 * time.Second

type dnsCacheDetector struct {
	mu    sync.RWMutex
	st    *dnscache.Status
	check func() dnscache.Status

	busy    atomic.Bool
	lastRun atomic.Int64 // unix ms of the last DNSCacheEnsure start
	ensure  func(ctx context.Context, zones []string) (string, error)
}

func (d *dnsCacheDetector) detect() {
	check := d.check
	if check == nil {
		check = dnscache.Check
	}
	s := check()
	d.mu.Lock()
	d.st = &s
	d.mu.Unlock()
}

func (d *dnsCacheDetector) run(ctx context.Context) {
	ticker := time.NewTicker(dnsCacheInterval)
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

// result returns a copy of the last check (nil before the first one).
func (d *dnsCacheDetector) result() *dnscache.Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.st == nil {
		return nil
	}
	s := *d.st
	return &s
}

type dnsCacheEnsureRequest struct {
	Zones []string `json:"zones"`
}

// handleEnsure runs flvx-dns-cache ensure once (single flight, min gap) and re-checks right after.
func (d *dnsCacheDetector) handleEnsure(parent context.Context, data interface{}) (map[string]interface{}, error) {
	var req dnsCacheEnsureRequest
	if raw, err := json.Marshal(data); err == nil {
		_ = json.Unmarshal(raw, &req)
	}
	if !d.busy.CompareAndSwap(false, true) {
		return nil, dnscache.ErrBusy
	}
	defer d.busy.Store(false)
	now := time.Now().UnixMilli()
	if last := d.lastRun.Load(); last > 0 && now-last < dnsCacheMinGap.Milliseconds() {
		return nil, errors.New("刚刚执行过，请 20 秒后再试")
	}
	d.lastRun.Store(now)
	ensure := d.ensure
	if ensure == nil {
		ensure = dnscache.Ensure
	}
	ctx, cancel := context.WithTimeout(parent, dnscache.EnsureTimeout)
	defer cancel()
	out, err := ensure(ctx, req.Zones)
	d.detect()
	if err != nil {
		return map[string]interface{}{"dns_cache": d.result(), "output": out}, err
	}
	return map[string]interface{}{
		"dns_cache": d.result(),
		"summary":   dnscache.Summary(out),
		"output":    out,
	}, nil
}
