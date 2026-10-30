package landing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Plan 168: every speed limit FLVX enforces on an ENTRY machine — the rule's
// own speed (dynamic / 限速规则), the per-user per-server 速率 (plan 159) and
// 竞技场 (arena) limits — is shaped by the same kernel objects as the
// per-user landing 速率 (plan 161): one IFB pair, one HTB tree 7f1a:, flower
// filters on the rule's listen port (ingress = upload, egress = download).
//
// The panel sends the complete tree of this machine (EntryRates, command
// SetEntryRates): classes (a user's 速率 is a group whose default leaf carries
// the user's rules without an own limit; rule / arena limits are leaves under
// the group or at the top) and the listen ports of every limited rule. The
// agent keeps it in entry_rates.json, applies it at once (rate changes are
// class changes: no service restart, no connection drop) and on the existing
// 10 s landing tick. Landing classes keep their plan-161 slots; entry classes
// take the free rest.

// EntryRateVersion is advertised in the WebSocket handshake (entryRateTc=1).
// Agents that do not advertise it (or report entryRateTcErr) keep the gost
// limiter / nftables policer path.
const EntryRateVersion = 1

const (
	maxEntryClasses = 3000
	maxEntryPorts   = 4000
	// Entry classes take slots from here up (landing classes use the user
	// id first, see rateSlots), then wrap around.
	entrySlotBase = 2048
	// A freed entry slot is not handed out again for this long, so a class
	// still draining is never reused for another one.
	entrySlotReuse = 2 * time.Minute
	entryRatesFile = "entry_rates.json"
)

// EntryRates is the complete entry-limit tree of this machine.
type EntryRates struct {
	Version int              `json:"version"`
	Classes []EntryRateClass `json:"classes"`
	Ports   []EntryRatePort  `json:"ports"`
	// Rev is the panel's runtime revision of this payload (reported back in
	// the service inventory, so a reconnect pushes only what differs).
	Rev string `json:"_flvxRev,omitempty"`
}

// EntryRateClass is one limit. Group = a user's 速率 (may have children; ports
// pointing at the group itself share its default leaf). A child names its
// group in Parent. RateMbps is FLVX Mbps (131072 B/s), both directions.
type EntryRateClass struct {
	ID       string `json:"id"`
	Parent   string `json:"parent,omitempty"`
	Group    bool   `json:"group,omitempty"`
	RateMbps int    `json:"rate_mbps"`
}

// EntryRatePort is the listen port of a limited rule on this machine.
type EntryRatePort struct {
	Port  int    `json:"port"`
	TCP   bool   `json:"tcp,omitempty"`
	UDP   bool   `json:"udp,omitempty"`
	Class string `json:"class"`
}

func (r *EntryRates) empty() bool {
	return r == nil || (len(r.Classes) == 0 && len(r.Ports) == 0)
}

func validEntryClassID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

// Validate checks the payload before anything is stored or applied.
func (r EntryRates) Validate() error {
	if r.empty() {
		return nil
	}
	if r.Version != EntryRateVersion {
		return fmt.Errorf("unsupported entry rate version %d", r.Version)
	}
	if len(r.Classes) > maxEntryClasses || len(r.Ports) > maxEntryPorts {
		return fmt.Errorf("too many entry rate classes / ports")
	}
	byID := map[string]EntryRateClass{}
	for _, c := range r.Classes {
		if !validEntryClassID(c.ID) || c.RateMbps < 1 || c.RateMbps > maxRateMbps {
			return fmt.Errorf("invalid entry rate class %q", c.ID)
		}
		if _, dup := byID[c.ID]; dup {
			return fmt.Errorf("duplicate entry rate class %q", c.ID)
		}
		if c.Group && c.Parent != "" {
			return fmt.Errorf("entry rate group %q must be top-level", c.ID)
		}
		byID[c.ID] = c
	}
	for _, c := range r.Classes {
		if c.Parent == "" {
			continue
		}
		if p, ok := byID[c.Parent]; !ok || !p.Group {
			return fmt.Errorf("entry rate class %q: parent %q is not a group", c.ID, c.Parent)
		}
	}
	ports := map[int]bool{}
	for _, p := range r.Ports {
		if p.Port < 1 || p.Port > 65535 || (!p.TCP && !p.UDP) || ports[p.Port] {
			return fmt.Errorf("invalid entry rate port %d", p.Port)
		}
		if _, ok := byID[p.Class]; !ok {
			return fmt.Errorf("entry rate port %d: unknown class %q", p.Port, p.Class)
		}
		ports[p.Port] = true
	}
	return nil
}

// entryShape is the entry part of the kernel state (built by buildEntryShape).
type entryShape struct {
	classes  []shapeClass
	filters  map[rateKey]uint16
	nClasses int // limits (wire classes) applied
	nPorts   int // ports filtered
	errs     []string
}

func (s *entryShape) active() bool {
	return s != nil && (len(s.classes) > 0 || len(s.filters) > 0)
}

// entrySlots hands out stable HTB slots to entry classes (by identity: the
// parent and the id, so moving a class is a new class and the old one drains).
type entrySlots struct {
	byKey map[string]uint16
	freed map[uint16]time.Time
}

// assign returns the slot of every key; reserved slots (landing) are never
// used. A key without a free slot gets 0.
func (a *entrySlots) assign(keys []string, reserved map[uint16]bool, now time.Time) map[string]uint16 {
	if a.byKey == nil {
		a.byKey = map[string]uint16{}
		a.freed = map[uint16]time.Time{}
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	for k, slot := range a.byKey {
		if !want[k] || reserved[slot] {
			delete(a.byKey, k)
			a.freed[slot] = now
		}
	}
	for slot, at := range a.freed {
		if now.Sub(at) >= entrySlotReuse {
			delete(a.freed, slot)
		}
	}
	used := map[uint16]bool{}
	for _, slot := range a.byKey {
		used[slot] = true
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	next := func() uint16 {
		for i := 0; i < maxRateSlots; i++ {
			slot := uint16((entrySlotBase-1+i)%maxRateSlots + 1)
			if !used[slot] && !reserved[slot] {
				if _, draining := a.freed[slot]; !draining {
					return slot
				}
			}
		}
		return 0
	}
	out := map[string]uint16{}
	for _, k := range sorted {
		slot, ok := a.byKey[k]
		if !ok {
			if slot = next(); slot == 0 {
				continue
			}
			a.byKey[k] = slot
			used[slot] = true
		}
		out[k] = slot
	}
	return out
}

func entryClassKey(c EntryRateClass) string { return c.Parent + ">" + c.ID }
func entryDefaultKey(group string) string   { return group + ">/" }

func mbpsWire(mbps int) uint64 { return rateWire(uint64(mbps) * RateBytesPerMbps) }

// buildEntryShape maps the entry tree onto HTB classes and filters next to
// the landing state (whose slots and ports win; a clash is an entry error).
func buildEntryShape(r *EntryRates, landing rateWant, slots *entrySlots, now time.Time) *entryShape {
	if r.empty() {
		slots.assign(nil, nil, now)
		return nil
	}
	reserved := map[uint16]bool{}
	landingFilters, _ := rateWant{groups: landing.groups}.shape()
	for _, g := range landing.groups {
		reserved[g.slot] = true
	}
	byID := map[string]EntryRateClass{}
	children := map[string][]EntryRateClass{}
	var keys []string
	for _, c := range r.Classes {
		byID[c.ID] = c
		keys = append(keys, entryClassKey(c))
		if c.Group {
			keys = append(keys, entryDefaultKey(c.ID))
		}
		if c.Parent != "" {
			children[c.Parent] = append(children[c.Parent], c)
		}
	}
	assigned := slots.assign(keys, reserved, now)
	s := &entryShape{filters: map[rateKey]uint16{}}
	leafOf := map[string]uint16{} // class id → slot packets of its ports go to
	noSlot := false
	top := make([]EntryRateClass, 0, len(r.Classes))
	for _, c := range r.Classes {
		if c.Parent == "" {
			top = append(top, c)
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].ID < top[j].ID })
	for _, c := range top {
		slot := assigned[entryClassKey(c)]
		if slot == 0 {
			noSlot = true
			continue
		}
		wire := mbpsWire(c.RateMbps)
		if !c.Group {
			s.classes = append(s.classes, shapeClass{slot: slot, rate: wire, ceil: wire, leaf: true})
			leafOf[c.ID] = slot
			s.nClasses++
			continue
		}
		def := assigned[entryDefaultKey(c.ID)]
		if def == 0 {
			noSlot = true
			continue
		}
		kids := children[c.ID]
		sort.Slice(kids, func(i, j int) bool { return kids[i].ID < kids[j].ID })
		// Every child is guaranteed an equal share (the sum never exceeds
		// the group, so the group's limit always holds); what an idle child
		// leaves is borrowed by the busy ones (equal quantum = equal split).
		share := wire / uint64(len(kids)+1)
		if share < rateMinimum {
			share = rateMinimum
		}
		s.classes = append(s.classes, shapeClass{slot: slot, rate: wire, ceil: wire})
		s.classes = append(s.classes, shapeClass{slot: def, parent: slot, rate: minU64(share, wire), ceil: wire, leaf: true})
		leafOf[c.ID] = def
		s.nClasses++
		for _, k := range kids {
			ks := assigned[entryClassKey(k)]
			if ks == 0 {
				noSlot = true
				continue
			}
			ceil := mbpsWire(minInt(k.RateMbps, c.RateMbps))
			s.classes = append(s.classes, shapeClass{slot: ks, parent: slot, rate: minU64(share, ceil), ceil: ceil, leaf: true})
			leafOf[k.ID] = ks
			s.nClasses++
		}
	}
	if noSlot {
		s.errs = append(s.errs, "限速类太多，部分限速未生效")
	}
	ports := append([]EntryRatePort(nil), r.Ports...)
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	var clash []string
	for _, p := range ports {
		slot, ok := leafOf[p.Class]
		if !ok {
			continue
		}
		var keysOf []rateKey
		for _, proto := range []struct {
			on, udp bool
		}{{p.TCP, false}, {p.UDP, true}} {
			if !proto.on {
				continue
			}
			for _, egress := range []bool{false, true} {
				for _, v6 := range []bool{false, true} {
					keysOf = append(keysOf, rateKey{egress: egress, v6: v6, udp: proto.udp, port: uint16(p.Port)})
				}
			}
		}
		taken := false
		for _, k := range keysOf {
			if _, ok := landingFilters[k]; ok {
				taken = true
			}
		}
		if taken {
			clash = append(clash, fmt.Sprint(p.Port))
			continue
		}
		for _, k := range keysOf {
			s.filters[k] = slot
		}
		s.nPorts++
	}
	if len(clash) > 0 {
		if len(clash) > 5 {
			clash = append(clash[:5], "…")
		}
		s.errs = append(s.errs, "端口 "+strings.Join(clash, ", ")+" 已被落地节点限速占用")
	}
	return s
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EntryRateStatus is reported to the panel (only when it changed): "off" =
// nothing to enforce, "on" = applied, "error" = (partly) not applied.
type EntryRateStatus struct {
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	Classes int    `json:"classes"`
	Ports   int    `json:"ports"`
}

type entryReport struct {
	status EntryRateStatus
	seq    uint64
	rev    string
}

// DisableEntryRates is called at start when this machine must not shape
// entry limits (kernel probe failed or FLVX_ENTRY_RATE_TC=off): a tree kept
// from an earlier run is dropped (file deleted, objects removed on the next
// tick) and SetEntryRates is refused; the panel then uses the old path.
func (m *Manager) DisableEntryRates() {
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	m.entryDisabled = true
	m.entryLoaded = true
	m.entry = nil
	_ = os.Remove(filepath.Join(m.dir, entryRatesFile))
}

// loadEntryLocked reads entry_rates.json once per process. Caller holds m.rateMu.
func (m *Manager) loadEntryLocked() {
	if m.entryLoaded {
		return
	}
	m.entryLoaded = true
	b, err := os.ReadFile(filepath.Join(m.dir, entryRatesFile))
	if err != nil {
		return
	}
	var r EntryRates
	if json.Unmarshal(b, &r) == nil && r.Validate() == nil && !r.empty() {
		m.entry = &r
	}
}

// ApplyEntryRates stores and applies the complete entry-limit tree of this
// machine (empty = remove every entry limit). Rate changes are class changes:
// no service restart, no connection drop.
func (m *Manager) ApplyEntryRates(r EntryRates) (EntryRateStatus, error) {
	if err := r.Validate(); err != nil {
		return EntryRateStatus{State: "error", Error: err.Error()}, err
	}
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	if m.ratesReleased {
		return m.entryStatus, errors.New("agent is stopping")
	}
	if m.entryDisabled {
		return EntryRateStatus{State: "off"}, errors.New("entry rates are disabled on this machine")
	}
	m.loadEntryLocked()
	if r.empty() {
		if err := os.Remove(filepath.Join(m.dir, entryRatesFile)); err != nil && !os.IsNotExist(err) {
			return m.entryStatus, fmt.Errorf("remove entry rates: %w", err)
		}
		m.entry = nil
	} else {
		if err := os.MkdirAll(m.dir, 0700); err != nil {
			return m.entryStatus, fmt.Errorf("create landing directory: %w", err)
		}
		if err := m.writeJSON(entryRatesFile, r); err != nil {
			return m.entryStatus, err
		}
		cp := r
		m.entry = &cp
	}
	m.applyRatesLocked()
	return m.entryStatus, nil
}

// EntryRatesRev is the revision of the applied entry tree ("" = none);
// lock-free for the service inventory.
func (m *Manager) EntryRatesRev() string {
	if r := m.entryRep.Load(); r != nil {
		return r.rev
	}
	return ""
}

// EntryRateReport returns the entry status and a sequence number that grows
// whenever the status changes (the 1 s report sends it only then); lock-free.
func (m *Manager) EntryRateReport() (EntryRateStatus, uint64) {
	if r := m.entryRep.Load(); r != nil {
		return r.status, r.seq
	}
	return EntryRateStatus{State: "off"}, 0
}

// publishEntryLocked updates the lock-free report. Caller holds m.rateMu.
func (m *Manager) publishEntryLocked() {
	rev := ""
	if m.entry != nil {
		rev = m.entry.Rev
	}
	old := m.entryRep.Load()
	if old != nil && old.status == m.entryStatus && old.rev == rev {
		return
	}
	var seq uint64 = 1
	if old != nil {
		seq = old.seq
		if old.status != m.entryStatus {
			seq++
		}
	}
	m.entryRep.Store(&entryReport{status: m.entryStatus, seq: seq, rev: rev})
}

// entryStatusOf derives the entry status from the shared engine result.
func entryStatusOf(shape *entryShape, st RateStatus) EntryRateStatus {
	if shape == nil {
		return EntryRateStatus{State: "off"}
	}
	out := EntryRateStatus{State: "on", Classes: shape.nClasses, Ports: shape.nPorts}
	var errs []string
	if st.State == "error" && st.Error != "" {
		errs = append(errs, st.Error)
	}
	errs = append(errs, shape.errs...)
	if len(errs) > 0 {
		out.State = "error"
		out.Error = strings.Join(errs, "；")
		if r := []rune(out.Error); len(r) > 300 {
			out.Error = string(r[:300])
		}
	}
	if !shape.active() && len(errs) == 0 {
		out.State = "off"
	}
	return out
}
