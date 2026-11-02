package probe

import (
	"time"

	"go-backend/internal/renewal"
)

// Plan 194 「剩余天数」: the public page gets a whole number of days and a
// cycle token, never the stored timestamp. The rolling rule itself lives in
// internal/renewal (shared with the 节点 page's TS helper via fixtures).

// Public renewal cycle tokens (the only values Node.Cycle can carry).
const (
	CycleMonth    = renewal.CycleMonth
	CycleQuarter  = renewal.CycleQuarter
	CycleHalfYear = renewal.CycleHalfYear
	CycleYear     = renewal.CycleYear
)

// ExpiryDays returns the calendar days (UTC+8) from now until the node's next
// due date and the public cycle token. ok is false when no expiry is set.
//
// Same rule as the admin 节点 page: with a known cycle the due date rolls
// forward by whole cycles while it is in the past, so only nodes without a
// cycle can come out negative (「已过期 N 天」).
func ExpiryDays(expiryMs int64, rawCycle string, now time.Time) (days int, cycle string, ok bool) {
	st, ok := renewal.Compute(expiryMs, rawCycle, now)
	if !ok {
		return 0, "", false
	}
	return st.Days, st.Cycle, true
}
