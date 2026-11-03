package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Plan 202 「禁止端口扫描」: the pinned Core (Custom v1.4.83, feature
// scan_guard.v1, contract landing-scan-guard-v1 §8) counts every outbound TCP
// dial of the listed inbound tags and blocks a tag by itself the moment it
// crosses a threshold. The agent only passes the panel's settings through,
// reads the Core's hits once per reconcile tick (no timer of its own, no Core
// API CLI call) and keeps a hit's node blocked until the panel has recorded
// it; from then on the panel decides (node disabled = stays blocked, enabled
// again = 解除禁用). Nothing is logged per connection or per hit.

// ScanGuardVersion is reported as landing_scan_guard: this agent applies
// Desired.ScanGuard and reports Status.ScanGuard. Older agents never get it.
const ScanGuardVersion = 1

// scanGuardFeature is the Core capability listed on /v1/features. An older
// Core rejects a config carrying scan_guard / scan_unblock (strict JSON), so
// neither key is sent to it.
const scanGuardFeature = "scan_guard.v1"

// maxScanPending bounds the hits kept for the panel (= the Core's ring).
const maxScanPending = 256

// Default thresholds (the panel always sends its own values; zero fields of an
// enabled guard fall back to these).
const (
	scanDefaultHorizontal    = 100
	scanDefaultHorizontalWeb = 500
	scanDefaultWindowS       = 60
	scanDefaultVertical      = 30
	scanDefaultTotal         = 1000
	scanDefaultTotalWindowS  = 300
)

var scanDefaultWebPorts = []int{80, 443}

// ScanGuard is the panel's 禁止端口扫描 setting for this server.
type ScanGuard struct {
	Enabled bool `json:"enabled"`
	// Identities are the inbound tags to guard (FLVX-native landing nodes).
	Identities    []string `json:"identities"`
	Horizontal    int      `json:"horizontal,omitempty"`
	HorizontalWeb int      `json:"horizontal_web,omitempty"`
	WebPorts      []int    `json:"web_ports,omitempty"`
	WindowS       int      `json:"window_s,omitempty"`
	Vertical      int      `json:"vertical,omitempty"`
	Total         int      `json:"total,omitempty"`
	TotalWindowS  int      `json:"total_window_s,omitempty"`
	// Seen is the highest hit seq the panel has recorded for this server (it
	// disabled the node in the same step). A hit above it is still the
	// agent's to hold; a Core block at or below it follows the node's
	// enabled state.
	Seen int64 `json:"seen,omitempty"`
}

// ScanHit is one Core hit as reported to the panel.
type ScanHit struct {
	ID         int64     `json:"id"` // landing node id
	InboundTag string    `json:"inbound_tag"`
	Type       string    `json:"type"` // horizontal | vertical | total
	Count      int       `json:"count"`
	Port       int       `json:"port,omitempty"`
	Host       string    `json:"host,omitempty"`
	WindowS    int       `json:"window_s"`
	At         time.Time `json:"at"`
	Seq        int64     `json:"seq"`
}

// ScanStatus is the guard state of this server.
type ScanStatus struct {
	// State: off | on | core_update (the running Core has no scan_guard.v1:
	// one Core restart after the agent upgrade) | error.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Hits the panel has not recorded yet (seq > Seen), oldest first.
	Hits []ScanHit `json:"hits"`
	// Blocked are the inbound tags the Core blocks right now.
	Blocked []string `json:"blocked,omitempty"`
}

// coreScanHit / coreScanReport are the Core's GET /v1/scan wire shape.
type coreScanHit struct {
	Identity string    `json:"identity"`
	Type     string    `json:"type"`
	Count    int       `json:"count"`
	Port     int       `json:"port"`
	Host     string    `json:"host"`
	WindowS  int       `json:"window_s"`
	At       time.Time `json:"at"`
	Seq      int64     `json:"seq"`
}

type coreScanReport struct {
	Version int           `json:"version"`
	Hits    []coreScanHit `json:"hits"`
	Blocked []coreScanHit `json:"blocked"`
}

// scanState is the agent's guard state, guarded by m.mu (scanSeq is the
// lock-free copy for the 1 s system info).
type scanState struct {
	featureEpoch string // Core epoch whose /v1/features answer is cached
	capable      bool   // that Core lists scan_guard.v1
	after        int64  // highest seq read from /v1/scan
	loaded       bool   // scan.json read
	pending      []ScanHit
	blocked      map[string]int64 // Core-blocked tag -> seq of its hit
	err          string
}

func (g *ScanGuard) validate(nodes []Node) error {
	if g == nil {
		return nil
	}
	if g.Seen < 0 || len(g.Identities) > len(nodes) {
		return fmt.Errorf("invalid landing scan guard")
	}
	tags := map[string]bool{}
	for _, n := range nodes {
		tags[n.InboundTag] = true
	}
	seen := map[string]bool{}
	for _, id := range g.Identities {
		if !tags[id] || seen[id] {
			return fmt.Errorf("invalid landing scan guard identity")
		}
		seen[id] = true
	}
	if !g.Enabled {
		return nil
	}
	for _, v := range []int{g.Horizontal, g.HorizontalWeb, g.Vertical, g.Total} {
		if v < 0 || v > 100000 {
			return fmt.Errorf("invalid landing scan guard threshold")
		}
	}
	for _, v := range []int{g.WindowS, g.TotalWindowS} {
		if v != 0 && (v < 10 || v > 3600) {
			return fmt.Errorf("invalid landing scan guard window")
		}
	}
	if len(g.WebPorts) > 16 {
		return fmt.Errorf("invalid landing scan guard web ports")
	}
	ports := map[int]bool{}
	for _, p := range g.WebPorts {
		if p < 1 || p > 65535 || ports[p] {
			return fmt.Errorf("invalid landing scan guard web ports")
		}
		ports[p] = true
	}
	return nil
}

// coreConfig is the Core's scan_guard object for the given tags.
func (g *ScanGuard) coreConfig(identities []string) map[string]any {
	or := func(v, d int) int {
		if v > 0 {
			return v
		}
		return d
	}
	ports := g.WebPorts
	if ports == nil {
		ports = scanDefaultWebPorts
	}
	return map[string]any{
		"enabled": true, "identities": identities,
		"horizontal": or(g.Horizontal, scanDefaultHorizontal), "horizontal_web": or(g.HorizontalWeb, scanDefaultHorizontalWeb),
		"web_ports": ports, "window_s": or(g.WindowS, scanDefaultWindowS),
		"vertical": or(g.Vertical, scanDefaultVertical),
		"total":    or(g.Total, scanDefaultTotal), "total_window_s": or(g.TotalWindowS, scanDefaultTotalWindowS),
	}
}

// scanFeature asks the Core once per epoch whether it has the guard. A
// failed request is retried on the next tick; an answer is kept.
func (m *Manager) scanFeature(ctx context.Context) {
	if m.scan.featureEpoch == m.epoch && m.epoch != "" {
		return
	}
	var features struct {
		Features []string `json:"features"`
	}
	if err := m.control(ctx, http.MethodGet, "/v1/features", nil, &features); err != nil {
		m.scan.capable = false
		return
	}
	m.scan.featureEpoch = m.epoch
	m.scan.capable = false
	for _, f := range features.Features {
		if f == scanGuardFeature {
			m.scan.capable = true
		}
	}
}

// readScan runs once per reconcile, before the limits are pushed: the
// Core's new hits and current blocks (one control-socket GET, empty lists in
// the steady state).
func (m *Manager) readScan(ctx context.Context) {
	if !m.scan.loaded {
		m.scan.loaded = true
		if b, err := os.ReadFile(filepath.Join(m.dir, "scan.json")); err == nil {
			var saved []ScanHit
			if json.Unmarshal(b, &saved) == nil && len(saved) <= maxScanPending {
				m.scan.pending = saved
			}
		}
	}
	m.scanFeature(ctx)
	before := canonical(m.scan.pending)
	m.scan.err = ""
	if m.scan.capable {
		var report coreScanReport
		path := "/v1/scan"
		if m.scan.after > 0 {
			path += "?after=" + strconv.FormatInt(m.scan.after, 10)
		}
		if err := m.control(ctx, http.MethodGet, path, nil, &report); err != nil || report.Version != 1 {
			m.scan.err = "landing Core scan report unavailable"
		} else {
			m.scan.blocked = map[string]int64{}
			for _, b := range report.Blocked {
				if b.Identity != "" && b.Seq > 0 {
					m.scan.blocked[b.Identity] = b.Seq
				}
			}
			for _, h := range append(report.Hits, report.Blocked...) {
				if h.Seq > m.scan.after && h.Seq < 1<<53 {
					m.scan.after = h.Seq
				}
				m.addPending(h)
			}
		}
	} else {
		m.scan.blocked = nil
	}
	m.prunePending()
	if canonical(m.scan.pending) != before {
		if len(m.scan.pending) == 0 {
			_ = os.Remove(filepath.Join(m.dir, "scan.json"))
		} else {
			_ = m.writeJSON("scan.json", m.scan.pending)
		}
	}
}

// addPending keeps a hit the panel has not recorded yet (once per seq).
func (m *Manager) addPending(h coreScanHit) {
	g := m.desired.ScanGuard
	if g == nil || h.Identity == "" || h.Seq <= g.Seen {
		return
	}
	for _, p := range m.scan.pending {
		if p.Seq == h.Seq {
			return
		}
	}
	m.scan.pending = append(m.scan.pending, ScanHit{InboundTag: h.Identity, Type: h.Type, Count: h.Count, Port: h.Port, Host: h.Host, WindowS: h.WindowS, At: h.At, Seq: h.Seq})
}

// prunePending drops hits the panel has recorded (seq <= Seen) and hits of
// tags no longer in the desired state, fills in node ids, and caps the list.
func (m *Manager) prunePending() {
	g := m.desired.ScanGuard
	ids := map[string]int64{}
	for _, n := range m.desired.Nodes {
		ids[n.InboundTag] = n.ID
	}
	kept := m.scan.pending[:0]
	for _, p := range m.scan.pending {
		if g == nil || p.Seq <= g.Seen || ids[p.InboundTag] == 0 {
			continue
		}
		p.ID = ids[p.InboundTag]
		kept = append(kept, p)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Seq < kept[j].Seq })
	if len(kept) > maxScanPending {
		kept = append([]ScanHit(nil), kept[len(kept)-maxScanPending:]...)
	}
	m.scan.pending = kept
	var top int64
	for _, p := range kept {
		top = p.Seq
	}
	m.scanSeq.Store(top)
}

// scanHeld: tags with a hit the panel has not recorded yet. The agent blocks
// them itself (blocked_identities), so a Core restart before the panel saw
// the hit cannot lift the block.
func (m *Manager) scanHeld() map[string]bool {
	held := map[string]bool{}
	for _, p := range m.scan.pending {
		held[p.InboundTag] = true
	}
	return held
}

// scanControl returns the Core's scan_guard (nil = omit) and scan_unblock
// entries for the next /v1/config. Nothing is returned for a Core without
// the feature. A Core block is lifted when the panel has recorded its hit
// and the node is enabled again (解除禁用), when the node is gone, or when
// the panel does not run the guard at all (older panel after a rollback).
func (m *Manager) scanControl(active map[string]bool) (map[string]any, []map[string]any) {
	if !m.scan.capable {
		return nil, nil
	}
	g := m.desired.ScanGuard
	enabled := map[string]bool{}
	public := map[string]bool{}
	for _, n := range m.desired.Nodes {
		enabled[n.InboundTag] = m.desired.Enabled && n.Enabled
		public[n.InboundTag] = n.Listen == ""
	}
	unblock := []map[string]any{}
	tags := make([]string, 0, len(m.scan.blocked))
	for tag := range m.scan.blocked {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		seq := m.scan.blocked[tag]
		_, known := enabled[tag]
		if g == nil || !known || (seq <= g.Seen && enabled[tag]) {
			unblock = append(unblock, map[string]any{"identity": tag, "seq": seq})
		}
	}
	if len(unblock) == 0 {
		unblock = nil
	}
	if g == nil || !g.Enabled {
		return nil, unblock
	}
	ids := []string{}
	for _, id := range g.Identities {
		if active[id] && public[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, unblock
	}
	return g.coreConfig(ids), unblock
}

// scanStatus is the Status.ScanGuard report (m.mu held).
func (m *Manager) scanStatus() *ScanStatus {
	st := &ScanStatus{State: "off", Hits: append([]ScanHit{}, m.scan.pending...)}
	g := m.desired.ScanGuard
	switch {
	case g == nil || !g.Enabled:
	case m.epoch == "":
	case !m.scan.capable:
		st.State = "core_update"
	case m.scan.err != "":
		st.State, st.Error = "error", m.scan.err
	default:
		st.State = "on"
	}
	for tag := range m.scan.blocked {
		st.Blocked = append(st.Blocked, tag)
	}
	sort.Strings(st.Blocked)
	return st
}

// ScanSeq is the highest hit seq the panel has not recorded yet (0 = none);
// the 1 s system info carries it so the panel syncs at once.
func (m *Manager) ScanSeq() int64 { return m.scanSeq.Load() }
