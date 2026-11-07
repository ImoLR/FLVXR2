package repo

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-backend/internal/store/model"
)

// Plan 234 「自动切换门槛」: the three automatic-switch margins an admin sets on
// 网站配置, kept in vite_config (missing key = default = the value hard-coded
// before, so behaviour is unchanged until edited), and a bounded count of the
// automatic switches per UTC+8 day for 「最近 7 天自动切换 N 次」.
//
//   - best          = 「最优」 tunnel layers and 「规则最优」 per destination (panel,
//     2 consecutive 60 s rounds stay fixed)
//   - realityDomain = Reality 伪装域名 auto pick (panel, plan 233)
//   - realityIp     = Reality 目标 IP pin (native agent / mmwx Helper)
//
// A margin of 0 means any gain switches (a tie keeps the current one). A
// failover (current unreachable / blocked / gone) never waits for a margin.
const (
	SwitchMarginBestKey          = "switch_margin_best_ms"
	SwitchMarginRealityDomainKey = "switch_margin_reality_domain_ms"
	SwitchMarginRealityIPKey     = "switch_margin_reality_ip_ms"
	AutoSwitchCountsKey          = "auto_switch_counts"

	DefaultSwitchMarginBestMs          = 5
	DefaultSwitchMarginRealityDomainMs = RealityPickSwitchMarginMs
	DefaultSwitchMarginRealityIPMs     = 20
	SwitchMarginMaxMs                  = 200

	// switchMarginsTTL bounds how long a value written behind the cache's
	// back (backup import, direct DB edit) can stay unseen; the card and the
	// generic config endpoint invalidate at once.
	switchMarginsTTL = 5 * time.Minute

	AutoSwitchBest          = "best"
	AutoSwitchRealityDomain = "realityDomain"
	AutoSwitchRealityIP     = "realityIp"

	// AutoSwitchWindowDays is the 「最近 7 天」 window (today + 6 days before);
	// autoSwitchKeepDays the buckets kept (one spare day).
	AutoSwitchWindowDays = 7
	autoSwitchKeepDays   = 8
	// AutoSwitchBusyPerWindow: one tunnel / server with this many margin
	// switches in the window (≥ 1 a day) gets the 「切换较频繁」 hint.
	AutoSwitchBusyPerWindow = 7
)

// LandingRealityPinMarginVersion: native agents reporting landing_reality ≥ 3
// take Desired.reality_pin_margin_ms and report pin move_reason.
const LandingRealityPinMarginVersion = 3

var errSwitchMargin = errors.New("请输入 0–200 的整数（毫秒）")

// SwitchMargins are the three margins in whole ms.
type SwitchMargins struct {
	BestMs          int `json:"bestMs"`
	RealityDomainMs int `json:"realityDomainMs"`
	RealityIPMs     int `json:"realityIpMs"`
}

func DefaultSwitchMargins() SwitchMargins {
	return SwitchMargins{BestMs: DefaultSwitchMarginBestMs, RealityDomainMs: DefaultSwitchMarginRealityDomainMs, RealityIPMs: DefaultSwitchMarginRealityIPMs}
}

// SwitchMarginBeats is the one switch rule of all three: the challenger is
// actually faster (gap > 0, so a tie keeps the current one even at margin 0)
// and by at least margin ms.
func SwitchMarginBeats(gapMs, marginMs float64) bool {
	return gapMs > 0 && gapMs >= marginMs
}

// IsSwitchMarginKey reports the vite_config keys of the three margins.
func IsSwitchMarginKey(name string) bool {
	switch strings.TrimSpace(name) {
	case SwitchMarginBestKey, SwitchMarginRealityDomainKey, SwitchMarginRealityIPKey:
		return true
	}
	return false
}

// ParseSwitchMargin validates one stored / submitted margin (0–200 ms).
func ParseSwitchMargin(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 || n > SwitchMarginMaxMs {
		return 0, errSwitchMargin
	}
	return n, nil
}

// Validate checks all three margins.
func (m SwitchMargins) Validate() error {
	for _, v := range []int{m.BestMs, m.RealityDomainMs, m.RealityIPMs} {
		if v < 0 || v > SwitchMarginMaxMs {
			return errSwitchMargin
		}
	}
	return nil
}

type switchMarginsCache struct {
	m  SwitchMargins
	at time.Time
}

// SwitchMargins returns the margins from memory; the DB is read at most once
// per switchMarginsTTL (or after a save). A missing or malformed key is its
// default; a DB error keeps the last known values (defaults before any).
func (r *Repository) SwitchMargins() SwitchMargins {
	if r == nil || r.db == nil {
		return DefaultSwitchMargins()
	}
	now := time.Now()
	c := r.margins.Load()
	if c != nil && now.Sub(c.at) < switchMarginsTTL && !now.Before(c.at) {
		return c.m
	}
	m, err := r.loadSwitchMargins()
	if err != nil {
		if c != nil {
			return c.m
		}
		return DefaultSwitchMargins()
	}
	r.margins.Store(&switchMarginsCache{m: m, at: now})
	return m
}

func (r *Repository) loadSwitchMargins() (SwitchMargins, error) {
	m := DefaultSwitchMargins()
	values, err := r.GetConfigsByNames([]string{SwitchMarginBestKey, SwitchMarginRealityDomainKey, SwitchMarginRealityIPKey})
	if err != nil {
		return m, err
	}
	read := func(key string, dst *int) {
		if raw, ok := values[key]; ok {
			if n, err := ParseSwitchMargin(raw); err == nil {
				*dst = n
			}
		}
	}
	read(SwitchMarginBestKey, &m.BestMs)
	read(SwitchMarginRealityDomainKey, &m.RealityDomainMs)
	read(SwitchMarginRealityIPKey, &m.RealityIPMs)
	return m, nil
}

// InvalidateSwitchMargins drops the cache (a margin key was written).
func (r *Repository) InvalidateSwitchMargins() {
	if r != nil {
		r.margins.Store(nil)
	}
}

// SetSwitchMargins stores all three margins in one transaction.
func (r *Repository) SetSwitchMargins(m SwitchMargins) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for key, v := range map[string]int{SwitchMarginBestKey: m.BestMs, SwitchMarginRealityDomainKey: m.RealityDomainMs, SwitchMarginRealityIPKey: m.RealityIPMs} {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "name"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "time"}),
			}).Create(&model.ViteConfig{Name: key, Value: strconv.Itoa(v), Time: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	r.InvalidateSwitchMargins()
	return err
}

// ---- switch counts ----------------------------------------------------------

// autoSwitchBucket is one kind on one day. N = every automatic switch, F = the
// failovers among them, U = per unit (tunnel id / landing server id) the
// switches that were not failovers (faster, or reason unknown: older agents
// and Helpers do not say) — what the 「切换较频繁」 hint looks at.
type autoSwitchBucket struct {
	N int            `json:"n"`
	F int            `json:"f,omitempty"`
	U map[string]int `json:"u,omitempty"`
}

type autoSwitchCounts struct {
	V     int                                     `json:"v"`
	Start int64                                   `json:"start"` // unix ms counting began (first switch-counting panel start)
	Days  map[string]map[string]*autoSwitchBucket `json:"days"`  // "2006-01-02" (UTC+8) → kind → bucket
}

type autoSwitchStore struct {
	mu     sync.Mutex
	counts *autoSwitchCounts // nil = not loaded yet
	dirty  bool
}

// AutoSwitchEvent is one applied automatic switch.
type AutoSwitchEvent struct {
	Kind     string
	Unit     int64
	Failover bool
	At       time.Time
}

func autoSwitchDay(t time.Time) string { return t.In(TrafficDayZone).Format("2006-01-02") }

// loadLocked reads the stored counts once (missing / malformed = empty, starting now).
func (r *Repository) autoSwitchLoadLocked(now time.Time) {
	st := &r.switches
	if st.counts != nil {
		return
	}
	c := &autoSwitchCounts{V: 1, Days: map[string]map[string]*autoSwitchBucket{}}
	var rows []model.ViteConfig
	if r.db != nil && r.db.Select("value").Where("name = ?", AutoSwitchCountsKey).Limit(1).Find(&rows).Error == nil && len(rows) == 1 {
		var stored autoSwitchCounts
		if json.Unmarshal([]byte(rows[0].Value), &stored) == nil && stored.Days != nil {
			c = &stored
			c.V = 1
		}
	}
	if c.Start <= 0 {
		c.Start = now.UnixMilli()
		st.dirty = true
	}
	st.counts = c
}

// pruneLocked keeps the newest autoSwitchKeepDays days.
func (c *autoSwitchCounts) pruneLocked(now time.Time) {
	oldest := autoSwitchDay(now.AddDate(0, 0, -(autoSwitchKeepDays - 1)))
	for day := range c.Days {
		if day < oldest {
			delete(c.Days, day)
		}
	}
}

// StartAutoSwitchCounts loads the stored counts at panel start; the first
// start of a switch-counting panel records when counting began.
func (r *Repository) StartAutoSwitchCounts() error {
	if r == nil || r.db == nil {
		return nil
	}
	r.switches.mu.Lock()
	r.autoSwitchLoadLocked(time.Now())
	r.switches.mu.Unlock()
	return r.FlushAutoSwitches()
}

// NoteAutoSwitch counts switches in memory; FlushAutoSwitches persists them.
func (r *Repository) NoteAutoSwitch(events ...AutoSwitchEvent) {
	if r == nil || len(events) == 0 {
		return
	}
	st := &r.switches
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	r.autoSwitchLoadLocked(now)
	for _, ev := range events {
		if ev.Kind == "" {
			continue
		}
		at := ev.At
		if at.IsZero() {
			at = now
		}
		day := autoSwitchDay(at)
		kinds := st.counts.Days[day]
		if kinds == nil {
			kinds = map[string]*autoSwitchBucket{}
			st.counts.Days[day] = kinds
		}
		b := kinds[ev.Kind]
		if b == nil {
			b = &autoSwitchBucket{}
			kinds[ev.Kind] = b
		}
		b.N++
		if ev.Failover {
			b.F++
		} else if ev.Unit > 0 {
			if b.U == nil {
				b.U = map[string]int{}
			}
			b.U[strconv.FormatInt(ev.Unit, 10)]++
		}
		st.dirty = true
	}
	st.counts.pruneLocked(now)
}

// FlushAutoSwitches writes the counts when they changed (one small row).
func (r *Repository) FlushAutoSwitches() error {
	if r == nil || r.db == nil {
		return nil
	}
	st := &r.switches
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.counts == nil || !st.dirty {
		return nil
	}
	st.counts.pruneLocked(time.Now())
	raw, err := json.Marshal(st.counts)
	if err != nil {
		return err
	}
	if err := r.UpsertConfig(AutoSwitchCountsKey, string(raw), time.Now().UnixMilli()); err != nil {
		return err
	}
	st.dirty = false
	return nil
}

// RecordAutoSwitch counts and persists at once (rare events: 测速, pins).
func (r *Repository) RecordAutoSwitch(events ...AutoSwitchEvent) error {
	if len(events) == 0 {
		return nil
	}
	r.NoteAutoSwitch(events...)
	return r.FlushAutoSwitches()
}

// AutoSwitchSummary is one kind over the 7-day window.
type AutoSwitchSummary struct {
	Total    int `json:"total"`
	Failover int `json:"failover"`
	// Since = window start, or when counting began if later (unix ms);
	// Partial = counting began inside the window (fewer than 7 days counted).
	Since   int64 `json:"since"`
	Partial bool  `json:"partial"`
	// BusyUnit / BusyCount: the tunnel / server with the most non-failover
	// switches when it has ≥ AutoSwitchBusyPerWindow (else 0).
	BusyUnit  int64 `json:"busyUnit,omitempty"`
	BusyCount int   `json:"busyCount,omitempty"`
}

// AutoSwitchSummaries sums the window per kind (all three kinds present).
func (r *Repository) AutoSwitchSummaries(now time.Time) map[string]AutoSwitchSummary {
	out := map[string]AutoSwitchSummary{AutoSwitchBest: {}, AutoSwitchRealityDomain: {}, AutoSwitchRealityIP: {}}
	if r == nil {
		return out
	}
	st := &r.switches
	st.mu.Lock()
	defer st.mu.Unlock()
	r.autoSwitchLoadLocked(now)
	local := now.In(TrafficDayZone)
	windowStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, TrafficDayZone).AddDate(0, 0, -(AutoSwitchWindowDays - 1))
	since, partial := windowStart.UnixMilli(), false
	if st.counts.Start > since {
		since, partial = st.counts.Start, true
	}
	first := autoSwitchDay(windowStart)
	last := autoSwitchDay(now)
	days := make([]string, 0, len(st.counts.Days))
	for day := range st.counts.Days {
		if day >= first && day <= last {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	for kind := range out {
		s := AutoSwitchSummary{Since: since, Partial: partial}
		units := map[string]int{}
		for _, day := range days {
			b := st.counts.Days[day][kind]
			if b == nil {
				continue
			}
			s.Total += b.N
			s.Failover += b.F
			for u, n := range b.U {
				units[u] += n
			}
		}
		keys := make([]string, 0, len(units))
		for u := range units {
			keys = append(keys, u)
		}
		sort.Strings(keys)
		for _, u := range keys {
			if n := units[u]; n >= AutoSwitchBusyPerWindow && n > s.BusyCount {
				id, _ := strconv.ParseInt(u, 10, 64)
				s.BusyUnit, s.BusyCount = id, n
			}
		}
		out[kind] = s
	}
	return out
}
