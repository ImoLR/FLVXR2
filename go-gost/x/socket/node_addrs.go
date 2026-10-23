package socket

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-gost/x/nodeaddr"
)

// Node DDNS (plan 142): the agent tells the panel its public interface
// addresses. They are read once at start and again only after a kernel
// address event (netlink subscription, 2 s debounce) or on the existing
// 10-minute IPv6 RA tick; the metric message carries them only when the set
// changed and once per panel session.

// addrReportVersion is the handshake capability (?addrReport=1).
const addrReportVersion = 1

const addrWatchDebounce = 2 * time.Second

type addrReporter struct {
	mu        sync.Mutex
	report    nodeaddr.Report
	seq       uint64
	collected bool
	collect   func() (nodeaddr.Report, error) // tests replace it
}

func (a *addrReporter) refresh() {
	collect := a.collect
	if collect == nil {
		collect = nodeaddr.Collect
	}
	r, err := collect()
	if err != nil {
		return
	}
	if r.V4 == nil {
		r.V4 = []string{}
	}
	if r.V6 == nil {
		r.V6 = []string{}
	}
	a.mu.Lock()
	if !a.collected || !r.Equal(a.report) {
		a.report, a.collected = r, true
		a.seq++
	}
	a.mu.Unlock()
}

// snapshot returns the current report and its sequence (0 = not read yet).
func (a *addrReporter) snapshot() (nodeaddr.Report, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.collected {
		return nodeaddr.Report{}, 0
	}
	return a.report, a.seq
}

// run reads the addresses once, then follows kernel address events. If the
// subscription fails it waits 10 minutes before trying again (the RA tick
// still refreshes meanwhile).
func (a *addrReporter) run(ctx context.Context) {
	a.refresh()
	logged := false
	for ctx.Err() == nil {
		err := nodeaddr.Watch(ctx, addrWatchDebounce, a.refresh)
		if ctx.Err() != nil {
			return
		}
		if !logged {
			fmt.Printf("⚠️ 网卡地址监听不可用，改为每 10 分钟检查：%v\n", err)
			logged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
		a.refresh()
	}
}

// addrsReport returns the report for the next metric when it changed since
// the last one sent on this session (connect resets addrsSentSeq).
func (w *WebSocketReporter) addrsReport() *nodeaddr.Report {
	r, seq := w.addrs.snapshot()
	if seq == 0 || seq == w.addrsSentSeq {
		return nil
	}
	w.addrsSentSeq = seq
	return &r
}
