// Package renewal is the one Go copy of the 节点 page's rolling renewal rule
// (vite-frontend/src/pages/node/renewal.ts getNodeRenewalSnapshot).
//
// A node's expiry_time is an anchor: once the due date has passed, a node
// with a renewal cycle simply counts down to the next cycle (month / quarter
// / half year / year), nobody has to edit the node. Only a node with an
// expiry but no (known) cycle can be overdue.
//
// Pure functions, no I/O: the probe (plan 194), the 救命版 machines in the
// probe (plan 195) and renewal reminders (plan 196) share them.
// testdata/rolling.json holds the fixtures the TS helper is checked against.
package renewal

import (
	"strings"
	"time"
)

// Canonical cycle tokens (lower case, as stored by the node form and as
// published on the probe).
const (
	CycleMonth    = "month"
	CycleQuarter  = "quarter"
	CycleHalfYear = "halfyear"
	CycleYear     = "year"
)

// Zone is the operators' clock: Taiwan / China time, UTC+8, no DST. Month
// steps keep the wall-clock time in this zone (JS setMonth in a UTC+8
// browser) and days change at local midnight.
var Zone = time.FixedZone("UTC+8", 8*3600)

const (
	// maxSteps bounds the roll-forward loop (100 years of monthly cycles);
	// the 6-hourly renewal job keeps stored anchors current anyway.
	maxSteps = 1200
	// MaxDays clamps nonsense dates (year 9999) to a sane number.
	MaxDays = 99999
)

// NormalizeCycle maps a stored renewal_cycle to its canonical token, "" when
// unknown; case-insensitive and trimmed like every other reader (halfYear).
func NormalizeCycle(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CycleMonth:
		return CycleMonth
	case CycleQuarter:
		return CycleQuarter
	case CycleHalfYear:
		return CycleHalfYear
	case CycleYear:
		return CycleYear
	}
	return ""
}

// CycleMonths is the length of a canonical cycle in months (0 = none).
func CycleMonths(cycle string) int {
	switch cycle {
	case CycleMonth:
		return 1
	case CycleQuarter:
		return 3
	case CycleHalfYear:
		return 6
	case CycleYear:
		return 12
	}
	return 0
}

// NextDue rolls anchorMs forward by whole cycles while it is before now
// (strictly, in ms, like `while (nextDueTime < Date.now())`). Each step adds
// the months to the previous step's result in Zone, so month-end overflow
// chains exactly like JS setMonth (Jan 31 → Mar 3 → Apr 3). Without a known
// cycle the anchor is returned unchanged.
func NextDue(anchorMs int64, cycle string, now time.Time) int64 {
	months := CycleMonths(NormalizeCycle(cycle))
	if anchorMs <= 0 || months == 0 {
		return anchorMs
	}
	nowMs := now.UnixMilli()
	due := anchorMs
	for i := 0; i < maxSteps && due < nowMs; i++ {
		next := time.UnixMilli(due).In(Zone).AddDate(0, months, 0).UnixMilli()
		if next <= due {
			break
		}
		due = next
	}
	return due
}

// DaysUntil counts calendar days in Zone from now to dueMs: same local date
// = 0, tomorrow = 1, yesterday = -1. A node due at 00:00 TW tomorrow is 1 at
// 23:30 TW today (never 0). Clamped to ±MaxDays.
func DaysUntil(dueMs int64, now time.Time) int {
	ay, am, ad := now.In(Zone).Date()
	by, bm, bd := time.UnixMilli(dueMs).In(Zone).Date()
	a := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	b := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	// Durations cap at ~292 years: compare years first so far dates clamp.
	if by-ay > 300 {
		return MaxDays
	}
	if ay-by > 300 {
		return -MaxDays
	}
	d := int(b.Sub(a).Hours()/24 + 0.5)
	if b.Before(a) {
		d = -int(a.Sub(b).Hours()/24 + 0.5)
	}
	if d > MaxDays {
		return MaxDays
	}
	if d < -MaxDays {
		return -MaxDays
	}
	return d
}

// Status is a node's renewal state at one moment.
type Status struct {
	Cycle   string // canonical token, "" = no (known) cycle
	NextDue int64  // ms: the rolled due date (the anchor itself without a cycle)
	Days    int    // DaysUntil(NextDue); < 0 only without a cycle
}

// Compute returns the node's renewal status; ok is false when no expiry is
// set (anchorMs ≤ 0).
func Compute(anchorMs int64, rawCycle string, now time.Time) (Status, bool) {
	if anchorMs <= 0 {
		return Status{}, false
	}
	cycle := NormalizeCycle(rawCycle)
	due := NextDue(anchorMs, cycle, now)
	return Status{Cycle: cycle, NextDue: due, Days: DaysUntil(due, now)}, true
}
