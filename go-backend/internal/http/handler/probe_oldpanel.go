package handler

// Plan 195: machines that only run the 救命版 (old Flux panel) agent appear on
// the probe, read straight from the 救命版 SQLite file (FLVX_OLDPANEL_DB),
// strictly read-only (see internal/oldpanel). Inside FLVX a 救命版 machine is
// the negative id -<old id>; it never leaves the panel (public key = HMAC of
// "old:<id>", see probe.Keyer).
//
// All reads happen on the probe's own build path (so only while somebody
// looks): node list at most every 60 s, newest metrics at most every 5 s
// (incremental by row id), host names resolved in the background.

import (
	"context"
	"log"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/geoip"
	"go-backend/internal/oldpanel"
	"go-backend/internal/probe"
	"go-backend/internal/store/repo"
)

const (
	oldPanelEnv          = "FLVX_OLDPANEL_DB"
	oldPanelNodesTTL     = 60 * time.Second
	oldPanelNodesRetry   = 15 * time.Second // after a failed node list read
	oldPanelPollEvery    = 5 * time.Second
	oldPanelBootstrap    = 60 * time.Second // window read on start / after a pause
	oldPanelPauseAfter   = 30 * time.Second // no poll for this long → bootstrap again
	oldPanelStaleRows    = 90 * time.Second // no new row for this long → bootstrap (file replaced?)
	oldPanelLogEvery     = 10 * time.Minute
	oldPanelQueryTimeout = 3 * time.Second
	oldPanelSeriesTime   = 8 * time.Second
	// The 救命版 writes its 5 s rows in batches every 10–20 s, so the newest row
	// of a healthy machine is up to ~25 s old (+ our 5 s poll): 45 s, not 30 s.
	oldPanelOnlineAge = 45 * time.Second
)

// oldPanelStats: resource numbers for the demo / tests.
type oldPanelStats struct {
	Polls       int64
	Rows        int64
	LastPollMs  float64
	LastRows    int
	Bootstraps  int64
	NodeReads   int64
	LastNodesMs float64
	SeenSeeks   int64
	SeriesReads int64
}

type oldPanelState struct {
	reader *oldpanel.Reader // nil = feature off
	dns    *oldpanel.DNSCache

	tickMu sync.Mutex // one tick at a time (DB reads)

	mu        sync.Mutex
	nodes     []oldpanel.Node
	addrs     map[int64]oldpanel.Addrs
	nodesOK   bool
	nodesTry  time.Time
	nodesRead time.Time
	samples   map[int64]probe.Sample // old id → newest row
	lastSeen  map[int64]int64
	cursor    int64
	pollTry   time.Time
	pollOK    time.Time
	rowsAt    time.Time
	errClass  string
	logAt     time.Time
	logMuted  int
	stats     oldPanelStats
}

// oldPanelView is what one build needs, copied under the lock.
type oldPanelView struct {
	on      bool
	ok      bool // node list readable
	nodes   []oldpanel.Node
	addrs   map[int64]oldpanel.Addrs
	samples map[int64]probe.Sample
	seen    map[int64]int64
}

func (o *oldPanelState) init() {
	if path := strings.TrimSpace(os.Getenv(oldPanelEnv)); path != "" {
		o.reader = oldpanel.New(path)
		o.dns = oldpanel.NewDNSCache()
	}
	o.addrs = map[int64]oldpanel.Addrs{}
	o.samples = map[int64]probe.Sample{}
	o.lastSeen = map[int64]int64{}
}

// note records a read result; at most one summary line per 10 minutes.
func (o *oldPanelState) note(err error, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	class := oldpanel.Classify(err)
	if class == "" {
		if o.errClass != "" && now.Sub(o.logAt) >= oldPanelLogEvery {
			log.Printf("[probe] old panel data readable again")
			o.logAt = now
		}
		o.errClass = ""
		return
	}
	o.errClass = class
	if now.Sub(o.logAt) < oldPanelLogEvery {
		o.logMuted++
		return
	}
	log.Printf("[probe] old panel data unavailable (%s): its machines are left out of the probe (%d similar in the last 10 min)", class, o.logMuted)
	o.logAt, o.logMuted = now, 0
}

// oldPanelTick refreshes the 救命版 view when due. Runs on the probe build /
// series / admin path only.
func (h *Handler) oldPanelTick(now time.Time) {
	o := &h.probeInit().old
	if o.reader == nil {
		return
	}
	o.tickMu.Lock()
	defer o.tickMu.Unlock()

	o.mu.Lock()
	nodesDue := o.nodesTry.IsZero() || now.Sub(o.nodesTry) >= oldPanelNodesTTL || (!o.nodesOK && now.Sub(o.nodesTry) >= oldPanelNodesRetry)
	o.mu.Unlock()
	if nodesDue {
		h.oldPanelReadNodes(now)
	}
	o.mu.Lock()
	pollDue := o.nodesOK && (o.pollTry.IsZero() || now.Sub(o.pollTry) >= oldPanelPollEvery)
	o.mu.Unlock()
	if pollDue {
		h.oldPanelPoll(now)
	}
}

func (h *Handler) oldPanelReadNodes(now time.Time) {
	o := &h.probeInit().old
	ctx, cancel := context.WithTimeout(context.Background(), oldPanelQueryTimeout)
	defer cancel()
	start := time.Now()
	nodes, err := o.reader.Nodes(ctx)
	ms := float64(time.Since(start).Microseconds()) / 1000
	o.note(err, now)
	o.mu.Lock()
	o.nodesTry = now
	o.stats.NodeReads++
	o.stats.LastNodesMs = ms
	if err != nil {
		// Keep showing nothing rather than stale machines of a broken source.
		o.nodesOK = false
		o.nodes = nil
		o.mu.Unlock()
		return
	}
	o.nodesOK, o.nodes, o.nodesRead = true, nodes, now
	alive := make(map[int64]bool, len(nodes))
	addrs := make(map[int64]oldpanel.Addrs, len(nodes))
	var missing []int64
	for _, n := range nodes {
		alive[n.ID] = true
		addrs[n.ID] = oldpanel.ParseAddrs(n.ServerIP, n.ServerIPV4, n.ServerIPV6, n.ExtraIPs)
		if _, ok := o.samples[n.ID]; !ok {
			if _, ok := o.lastSeen[n.ID]; !ok {
				missing = append(missing, n.ID)
			}
		}
	}
	o.addrs = addrs
	for id := range o.samples {
		if !alive[id] {
			delete(o.samples, id)
		}
	}
	for id := range o.lastSeen {
		if !alive[id] {
			delete(o.lastSeen, id)
		}
	}
	o.mu.Unlock()
	// 「最后在线」 of machines with no recent row: one index seek each, once.
	for _, id := range missing {
		ts, err := o.reader.LatestMetricTime(ctx, id)
		if err != nil {
			break
		}
		o.mu.Lock()
		o.lastSeen[id] = ts
		o.stats.SeenSeeks++
		o.mu.Unlock()
	}
}

func (h *Handler) oldPanelPoll(now time.Time) {
	o := &h.probeInit().old
	o.mu.Lock()
	bootstrap := o.cursor == 0 || now.Sub(o.pollOK) > oldPanelPauseAfter || (!o.rowsAt.IsZero() && now.Sub(o.rowsAt) > oldPanelStaleRows)
	cursor := o.cursor
	o.pollTry = now
	o.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), oldPanelQueryTimeout)
	defer cancel()
	start := time.Now()
	var rows []oldpanel.Metric
	var err error
	if bootstrap {
		rows, err = o.reader.MetricsSince(ctx, now.Add(-oldPanelBootstrap).UnixMilli())
	} else {
		rows, err = o.reader.MetricsAfter(ctx, cursor)
	}
	ms := float64(time.Since(start).Microseconds()) / 1000
	o.note(err, now)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pollOK = now
	o.stats.Polls++
	o.stats.Rows += int64(len(rows))
	o.stats.LastPollMs, o.stats.LastRows = ms, len(rows)
	if bootstrap {
		o.stats.Bootstraps++
	}
	var maxID int64
	for _, m := range rows {
		if m.ID > maxID {
			maxID = m.ID
		}
		if cur, ok := o.samples[m.NodeID]; ok && cur.At >= m.At {
			continue
		}
		o.samples[m.NodeID] = probe.Sample{
			At: m.At, CPU: m.CPU, Mem: m.Mem, Disk: m.Disk, Load1: m.Load1,
			NetIn: m.NetInSpeed, NetOut: m.NetOutSpeed, RxTotal: m.NetInBytes, TxTotal: m.NetOutBytes,
			Uptime: m.Uptime, TCP: m.TCP,
		}
		if m.At > o.lastSeen[m.NodeID] {
			o.lastSeen[m.NodeID] = m.At
		}
	}
	if len(rows) > 0 {
		o.rowsAt = now
		if bootstrap || maxID > o.cursor {
			o.cursor = maxID
		}
	}
	if len(o.samples) > 4096 { // node ids only; bound anyway
		o.samples = map[int64]probe.Sample{}
	}
}

func (h *Handler) oldPanelView() oldPanelView {
	o := &h.probeInit().old
	if o.reader == nil {
		return oldPanelView{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v := oldPanelView{on: true, ok: o.nodesOK, nodes: o.nodes, addrs: o.addrs, samples: make(map[int64]probe.Sample, len(o.samples)), seen: make(map[int64]int64, len(o.lastSeen))}
	for id, s := range o.samples {
		v.samples[id] = s
	}
	for id, ts := range o.lastSeen {
		v.seen[id] = ts
	}
	return v
}

// oldPanelOnline: newest row of the 救命版 machine is fresh enough.
func (h *Handler) oldPanelOnline(oldID int64, now time.Time) bool {
	o := &h.probeInit().old
	if o.reader == nil {
		return false
	}
	o.mu.Lock()
	s, ok := o.samples[oldID]
	o.mu.Unlock()
	return ok && now.UnixMilli()-s.At <= oldPanelOnlineAge.Milliseconds()
}

// oldPanelMatch is the merge of both panels for one build.
type oldPanelMatch struct {
	view    oldPanelView
	matched map[int64]int64 // old id → FLVX id
	pending map[int64]bool
	byFLVX  map[int64][]int64 // FLVX id → matched old ids (old order)
}

// oldPanelMerge matches the 救命版 nodes against the FLVX rows (host names
// from the background DNS cache; never blocks on DNS).
func (h *Handler) oldPanelMerge(rows []repo.ProbeNodeRow, flvxAddrs map[int64]oldpanel.Addrs) oldPanelMatch {
	m := oldPanelMatch{view: h.oldPanelView()}
	if !m.view.on || !m.view.ok {
		return m
	}
	o := &h.probeInit().old
	flvx := make([]oldpanel.Side, 0, len(rows))
	hosts := make([]string, 0, 16)
	for _, r := range rows {
		a := flvxAddrs[r.ID]
		flvx = append(flvx, oldpanel.Side{ID: r.ID, Addrs: a})
		hosts = append(hosts, a.Hosts...)
	}
	old := make([]oldpanel.Side, 0, len(m.view.nodes))
	for _, n := range m.view.nodes {
		a := m.view.addrs[n.ID]
		old = append(old, oldpanel.Side{ID: n.ID, Addrs: a})
		hosts = append(hosts, a.Hosts...)
	}
	o.dns.Want(hosts)
	m.matched, m.pending = oldpanel.Match(flvx, old, o.dns.Lookup)
	m.byFLVX = make(map[int64][]int64, len(m.matched))
	for _, n := range m.view.nodes {
		if fid, ok := m.matched[n.ID]; ok {
			m.byFLVX[fid] = append(m.byFLVX[fid], n.ID)
		}
	}
	return m
}

// oldPanelAdminMerge: a fresh 救命版 list for the admin card (node list
// re-read, host names get up to 2.5 s to resolve). knownOld = every 救命版
// node id, nil while the 救命版 cannot be read (stored picks are then kept).
func (h *Handler) oldPanelAdminMerge(rows []repo.ProbeNodeRow) (oldPanelMatch, map[int64]bool) {
	o := &h.probeInit().old
	if o.reader == nil {
		return oldPanelMatch{}, nil
	}
	o.mu.Lock()
	o.nodesTry = time.Time{}
	o.mu.Unlock()
	h.oldPanelTick(time.Now())
	addrs := make(map[int64]oldpanel.Addrs, len(rows))
	for _, n := range rows {
		addrs[n.ID] = oldpanel.ParseAddrs(n.ServerIP, n.ServerIPV4, n.ServerIPV6, n.ExtraIPs)
	}
	m := h.oldPanelMerge(rows, addrs)
	if len(m.pending) > 0 {
		o.dns.Wait(2500 * time.Millisecond)
		m = h.oldPanelMerge(rows, addrs)
	}
	if !m.view.ok {
		return m, nil
	}
	known := make(map[int64]bool, len(m.view.nodes))
	for _, n := range m.view.nodes {
		known[n.ID] = true
	}
	return m, known
}

// oldPanelKnownIDs: every 救命版 node id right now, nil when the source is
// off or cannot be read (callers then keep stored picks unchanged).
func (h *Handler) oldPanelKnownIDs() map[int64]bool {
	o := &h.probeInit().old
	if o.reader == nil {
		return nil
	}
	h.oldPanelTick(time.Now())
	v := h.oldPanelView()
	if !v.ok {
		return nil
	}
	known := make(map[int64]bool, len(v.nodes))
	for _, n := range v.nodes {
		known[n.ID] = true
	}
	return known
}

// oldPanelRegion: GeoIP of the machine's public address (IPv4 first, literal
// before resolved), like the 节点 page's auto-detection.
func (h *Handler) oldPanelRegion(a oldpanel.Addrs) string {
	o := &h.probeInit().old
	var cand []netip.Addr
	for _, want4 := range []bool{true, false} {
		for _, ip := range a.IPs {
			if ip.Is4() == want4 {
				cand = append(cand, ip)
			}
		}
		for _, host := range a.Hosts {
			ips, _ := o.dns.Lookup(host)
			for _, ip := range ips {
				if ip.Is4() == want4 {
					cand = append(cand, ip)
				}
			}
		}
	}
	for _, ip := range cand {
		if r := geoip.Lookup(ip); r != "" {
			return r
		}
	}
	return ""
}

// oldPanelInputs: the shown 救命版-only machines (default hidden: only the
// ones the admin ticked), with the FLVX-side expiry fallback for matched pairs.
func (h *Handler) oldPanelInputs(m oldPanelMatch, st probeSettings) []probe.NodeInput {
	if !m.view.on || !m.view.ok {
		return nil
	}
	out := make([]probe.NodeInput, 0, len(st.OldShown))
	for _, n := range m.view.nodes {
		if _, ok := m.matched[n.ID]; ok || m.pending[n.ID] || !st.OldShown[n.ID] {
			continue
		}
		name := strings.TrimSpace(n.Name)
		if v := st.OldNames[n.ID]; v != "" {
			name = v
		}
		seen := m.view.seen[n.ID]
		if s, ok := m.view.samples[n.ID]; ok && s.At > seen {
			seen = s.At
		}
		region := st.OldRegions[n.ID]
		if region == "" {
			region = h.oldPanelRegion(m.view.addrs[n.ID])
		}
		out = append(out, probe.NodeInput{
			ID: -n.ID, Name: name, Region: region, Version: n.Version, LastSeen: seen,
			ExpiryTime: n.ExpiryTime, RenewalCycle: n.RenewalCycle, SampleMaxAge: oldPanelOnlineAge,
		})
	}
	return out
}

// oldPanelExpiry: a matched FLVX node without its own expiry borrows the
// 救命版's (first matched old node that has one).
func oldPanelExpiry(m oldPanelMatch, flvxID int64) (int64, string, bool) {
	for _, oid := range m.byFLVX[flvxID] {
		for _, n := range m.view.nodes {
			if n.ID == oid && n.ExpiryTime > 0 {
				return n.ExpiryTime, n.RenewalCycle, true
			}
		}
	}
	return 0, "", false
}

// oldPanelSeries reads the detail chart of a 救命版 machine.
func (h *Handler) oldPanelSeries(oldID, startMs, endMs, stepMs int64, points int) ([]oldpanel.Bucket, error) {
	o := &h.probeInit().old
	if o.reader == nil {
		return nil, oldpanel.ErrMissing
	}
	ctx, cancel := context.WithTimeout(context.Background(), oldPanelSeriesTime)
	defer cancel()
	rows, err := o.reader.Buckets(ctx, oldID, startMs, endMs, stepMs, points)
	o.mu.Lock()
	o.stats.SeriesReads++
	o.mu.Unlock()
	if err != nil {
		o.note(err, time.Now())
	}
	return rows, err
}

// oldPanelStatus for the admin card: "" off, "ok", "error".
func (h *Handler) oldPanelStatus() string {
	o := &h.probeInit().old
	if o.reader == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.nodesOK {
		return "ok"
	}
	return "error"
}

// OldPanelStats is for the demo / tests (resource numbers).
func (h *Handler) OldPanelStats() oldPanelStats {
	o := &h.probeInit().old
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

// sortedOldIDs: stable order for stored id lists.
func sortedOldIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id, on := range set {
		if on {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SetProbeOldPanelResolver replaces the host name resolver of the 救命版
// matching (tests and the offline demo only; production uses the system DNS).
func (h *Handler) SetProbeOldPanelResolver(fn oldpanel.Resolver) {
	o := &h.probeInit().old
	if o.dns != nil && fn != nil {
		o.dns.Resolve = fn
	}
}
