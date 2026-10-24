package handler

import (
	"net/netip"
	"sort"
	"strconv"
	"sync"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/repo"
)

// The 监控 「落地」 tab shows live rates without any new polling: every cumulative
// landing counter the panel already commits (native agent flow uploads every 5 s,
// shared Custom status once a minute per server) is also kept here, two samples
// per landing node, in memory only. Nothing in this file touches the database.
const (
	// Samples closer than this keep the older base sample, so a near-duplicate
	// report cannot turn a few bytes over a few milliseconds into a spike.
	landingRateMinGapMs = int64(1000)
	// Rates whose newest sample is older than this are not reported.
	landingRateStaleAfterMs = int64(5 * 60 * 1000)
	// Entries without a sample for this long are dropped by the minute sweep.
	landingRateTTLMs = int64(24 * 60 * 60 * 1000)
	// Hard bound on the map; the entry with the oldest sample makes room.
	landingRateMaxEntries = 50000
	landingOnlineIPLimit  = 64
)

type landingRateSample struct {
	at    int64 // unix ms: panel receive time (native) or Helper collection time (shared)
	epoch int64 // native Core start; 0 for shared lifetime counters
	up    int64
	down  int64
	conns int
}

type landingRateEntry struct {
	shared  bool
	base    landingRateSample
	last    landingRateSample
	hasBase bool
	// Only the latest IP list is retained; the rate's base sample has no IPs.
	onlineIPCount int
	onlineIPKnown bool
	onlineIPs     []landingcustom.OnlineIP
}

// landingRate is what the monitor shows for one landing node. SampleAt is zero
// when no sample has arrived since the panel started.
type landingRate struct {
	SampleAt    int64
	Connections int
	UpRate      float64 // bytes per second, client upload
	DownRate    float64 // bytes per second, client download
	RateOK      bool
	OnlineIPs   *int
}

type landingRateTracker struct {
	mu         sync.Mutex
	entries    map[int64]*landingRateEntry
	maxEntries int
}

func newLandingRateTracker(maxEntries int) *landingRateTracker {
	if maxEntries <= 0 {
		maxEntries = landingRateMaxEntries
	}
	return &landingRateTracker{entries: map[int64]*landingRateEntry{}, maxEntries: maxEntries}
}

func (h *Handler) landingRateTracker() *landingRateTracker {
	h.landingRatesOnce.Do(func() { h.landingRates = newLandingRateTracker(landingRateMaxEntries) })
	return h.landingRates
}

// observe records committed counters. Shared counters (ServerID set) are timed by
// the Helper's collection time, native ones by nowMs. Duplicates, older samples
// and counters of an older native Core epoch are ignored.
func (t *landingRateTracker) observe(counters []repo.LandingCounter, nowMs int64) {
	if t == nil || len(counters) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range counters {
		if c.ID <= 0 || c.UpBytes < 0 || c.DownBytes < 0 || c.Connections < 0 {
			continue
		}
		shared := c.ServerID > 0
		s := landingRateSample{at: nowMs, up: c.UpBytes, down: c.DownBytes, conns: c.Connections}
		if shared {
			s.at = c.CollectedAt
		} else {
			epoch, err := strconv.ParseInt(c.Epoch, 10, 64)
			if err != nil || epoch <= 0 {
				continue
			}
			s.epoch = epoch
		}
		if s.at <= 0 {
			continue
		}
		e := t.entries[c.ID]
		if e == nil || e.shared != shared {
			if e == nil && len(t.entries) >= t.maxEntries {
				t.evictOldestLocked()
			}
			e = &landingRateEntry{shared: shared, last: s}
			e.setOnlineIPs(c)
			t.entries[c.ID] = e
			continue
		}
		if s.at <= e.last.at || (!shared && s.epoch < e.last.epoch) {
			continue
		}
		if s.at-e.last.at >= landingRateMinGapMs {
			e.base, e.hasBase = e.last, true
		}
		e.last = s
		e.setOnlineIPs(c)
	}
}

func (e *landingRateEntry) setOnlineIPs(c repo.LandingCounter) {
	e.onlineIPKnown = c.OnlineIPCount != nil && *c.OnlineIPCount >= 0
	e.onlineIPCount = 0
	e.onlineIPs = nil
	if !e.onlineIPKnown {
		return
	}
	e.onlineIPCount = *c.OnlineIPCount
	if e.onlineIPCount == 0 {
		return
	}
	// Copy valid IPs so request buffers and arbitrarily long strings cannot be
	// retained by the tracker. A truncated list never changes the real count.
	ips := make([]landingcustom.OnlineIP, 0, len(c.OnlineIPs))
	for _, ip := range c.OnlineIPs {
		if text, ok := onlineIPText(ip.IP); ok && ip.Connections >= 0 {
			ips = append(ips, landingcustom.OnlineIP{IP: text, Connections: ip.Connections})
		}
	}
	sort.Slice(ips, func(i, j int) bool {
		if ips[i].Connections != ips[j].Connections {
			return ips[i].Connections > ips[j].Connections
		}
		return ips[i].IP < ips[j].IP
	})
	e.onlineIPs = make([]landingcustom.OnlineIP, min(len(ips), landingOnlineIPLimit))
	copy(e.onlineIPs, ips)
}

// onlineIPText canonicalises one online-IP entry: an address, or (plan 129) an
// IPv6 network such as "2408:8748:a100:59::/64" — the Core counts IPv6 clients
// per /64, so one entry is one online IP. Zones, IPv4 prefixes and unmasked
// prefixes are rejected.
func onlineIPText(raw string) (string, bool) {
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.String(), addr.Zone() == ""
	}
	p, err := netip.ParsePrefix(raw)
	if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() || p.Bits() < 1 || p != p.Masked() {
		return "", false
	}
	return p.String(), true
}

func (t *landingRateTracker) evictOldestLocked() {
	var oldestID int64
	oldestAt := int64(-1)
	for id, e := range t.entries {
		if oldestAt < 0 || e.last.at < oldestAt || (e.last.at == oldestAt && id < oldestID) {
			oldestID, oldestAt = id, e.last.at
		}
	}
	if oldestAt >= 0 {
		delete(t.entries, oldestID)
	}
}

// landingCounterDelta never returns a negative value. A native Core restart (new
// epoch) or a decrease of a shared lifetime counter is a reset: the bytes since
// the reset are the new value itself. A native decrease within one epoch is not
// trusted at all.
func landingCounterDelta(base, last int64, shared, newEpoch bool) (int64, bool) {
	if newEpoch {
		return last, true
	}
	if last >= base {
		return last - base, true
	}
	if shared {
		return last, true
	}
	return 0, false
}

func (t *landingRateTracker) snapshot(id, nowMs int64) landingRate {
	if t == nil {
		return landingRate{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked(id, nowMs)
}

// snapshotMany reads many nodes under one lock (one monitor response).
func (t *landingRateTracker) snapshotMany(ids []int64, nowMs int64) map[int64]landingRate {
	out := make(map[int64]landingRate, len(ids))
	if t == nil {
		return out
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		if r := t.snapshotLocked(id, nowMs); r.SampleAt > 0 {
			out[id] = r
		}
	}
	return out
}

func (t *landingRateTracker) snapshotLocked(id, nowMs int64) landingRate {
	e := t.entries[id]
	if e == nil {
		return landingRate{}
	}
	out := landingRate{SampleAt: e.last.at, Connections: e.last.conns}
	if nowMs-e.last.at > landingRateStaleAfterMs {
		return out
	}
	if e.onlineIPKnown {
		count := e.onlineIPCount
		out.OnlineIPs = &count
	}
	if !e.hasBase {
		return out
	}
	dt := e.last.at - e.base.at
	if dt <= 0 {
		return out
	}
	newEpoch := !e.shared && e.last.epoch > e.base.epoch
	up, okUp := landingCounterDelta(e.base.up, e.last.up, e.shared, newEpoch)
	down, okDown := landingCounterDelta(e.base.down, e.last.down, e.shared, newEpoch)
	if !okUp || !okDown {
		return out
	}
	out.UpRate = float64(up) * 1000 / float64(dt)
	out.DownRate = float64(down) * 1000 / float64(dt)
	out.RateOK = true
	return out
}

// onlineIPs copies the bounded list only when the IP detail is opened/refreshed;
// the regular monitor response only reads the count.
func (t *landingRateTracker) onlineIPs(id, nowMs int64) landingMonitorIPsView {
	out := landingMonitorIPsView{IPs: []landingcustom.OnlineIP{}}
	if t == nil {
		return out
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	sample := t.snapshotLocked(id, nowMs)
	out.OnlineIPs, out.SampleAt = sample.OnlineIPs, sample.SampleAt
	if sample.OnlineIPs != nil {
		out.IPs = append(out.IPs, t.entries[id].onlineIPs...)
	}
	return out
}

func (t *landingRateTracker) forget(ids ...int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		delete(t.entries, id)
	}
}

// retain drops entries of deleted landing nodes and entries without a sample for
// landingRateTTLMs. live is the full current id list.
func (t *landingRateTracker) retain(live []int64, nowMs int64) {
	if t == nil {
		return
	}
	keep := make(map[int64]struct{}, len(live))
	for _, id := range live {
		keep[id] = struct{}{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, e := range t.entries {
		if _, ok := keep[id]; !ok || nowMs-e.last.at > landingRateTTLMs {
			delete(t.entries, id)
		}
	}
}

func (t *landingRateTracker) size() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}
