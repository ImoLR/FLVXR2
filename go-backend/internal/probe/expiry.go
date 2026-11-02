package probe

import (
	"strings"
	"time"
)

// Plan 194 「剩余天数」: the public page gets a whole number of days and a
// cycle token, never the stored timestamp.

// Public renewal cycle tokens (the only values Node.Cycle can carry).
const (
	CycleMonth    = "month"
	CycleQuarter  = "quarter"
	CycleHalfYear = "halfyear"
	CycleYear     = "year"
)

// expiryZone: days are counted in Taiwan / China time (UTC+8, no DST), so the
// count changes at local midnight for the operators of this panel.
var expiryZone = time.FixedZone("UTC+8", 8*3600)

const (
	// maxCycleSteps bounds the roll-forward loop (100 years of monthly
	// cycles); the renewal job keeps stored values current anyway.
	maxCycleSteps = 1200
	// maxExpiryDays clamps nonsense dates (year 9999) to a sane number.
	maxExpiryDays = 99999
)

// NormalizeCycle maps a stored renewal_cycle to its public token ("" when
// unknown), case-insensitive like every other reader (halfYear / halfyear).
func NormalizeCycle(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "month":
		return CycleMonth
	case "quarter":
		return CycleQuarter
	case "halfyear":
		return CycleHalfYear
	case "year":
		return CycleYear
	}
	return ""
}

func cycleMonths(token string) int {
	switch token {
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

// ExpiryDays returns the calendar days (UTC+8) from now until the node's next
// due date and the public cycle token. ok is false when no expiry is set.
//
// Same rule as the admin 节点 page and the panel's renewal job: with a known
// cycle the due date rolls forward by whole cycles while it is in the past,
// so only nodes without a cycle can come out negative (「已过期 N 天」).
func ExpiryDays(expiryMs int64, rawCycle string, now time.Time) (days int, cycle string, ok bool) {
	if expiryMs <= 0 {
		return 0, "", false
	}
	cycle = NormalizeCycle(rawCycle)
	due := time.UnixMilli(expiryMs).In(expiryZone)
	nowLocal := now.In(expiryZone)
	if months := cycleMonths(cycle); months > 0 {
		for i := 0; i < maxCycleSteps && due.Before(nowLocal); i++ {
			due = due.AddDate(0, months, 0)
		}
	}
	return calendarDays(nowLocal, due), cycle, true
}

// calendarDays counts date changes between a and b (both already in
// expiryZone): same day = 0, tomorrow = 1, yesterday = -1.
func calendarDays(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	da := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	db := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	d := db.Sub(da).Hours() / 24
	// Durations cap at ~292 years; clamp both ways before converting.
	if d > maxExpiryDays {
		return maxExpiryDays
	}
	if d < -maxExpiryDays {
		return -maxExpiryDays
	}
	if d < 0 {
		return int(d - 0.5)
	}
	return int(d + 0.5)
}
