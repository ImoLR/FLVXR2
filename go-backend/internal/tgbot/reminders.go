package tgbot

import (
	"errors"
	"sort"
	"time"

	"go-backend/internal/renewal"
)

// Machine sources.
const (
	SourceFLVX = "flvx" // 转发面板 node (possibly also running in the 救命版)
	SourceOld  = "old"  // machine only in the 救命版
)

// Machine is one machine with an expiry, as the handler resolved it (FLVX
// nodes + 救命版-only machines, deduplicated like the probe).
type Machine struct {
	Key    string // f<id> / o<id>
	Name   string
	Source string
	NodeID int64  // FLVX node id (0 for 救命版-only machines)
	Anchor int64  // expiry_time (ms): rolling anchor (plan 194)
	Cycle  string // raw renewal cycle
	// OldMatch: the same machine's name in the 救命版 (FLVX rows only).
	OldMatch string
}

// Status is the machine's rolling renewal state now.
func (m Machine) Status(now time.Time) (renewal.Status, bool) {
	return renewal.Compute(m.Anchor, m.Cycle, now)
}

// PartialError: the machine list is usable but incomplete (the 救命版 is
// configured but cannot be read right now, or a host name is still being
// resolved). The daily run sends what it has and retries the rest later.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return "partial machine list: " + e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// IsPartial reports whether err is a PartialError.
func IsPartial(err error) bool {
	var pe *PartialError
	return errors.As(err, &pe)
}

// AckKey identifies 「已续费」 for one due date.
type AckKey struct {
	Key string
	Due int64
}

// Reminder is one message to send (to every bound chat).
type Reminder struct {
	Machine Machine
	Due     int64
	Days    int
	Cycle   string // canonical
}

// Select picks today's reminders: due date N days away (N in the settings),
// not muted, not 「已续费」 for that due date. Sorted soonest first, then by
// name. Machines without a cycle can be overdue (negative days) and simply
// stop matching; machines with a cycle roll to the next due date by
// themselves, so there is never an 「已过期」 reminder.
func Select(machines []Machine, st Settings, acked map[AckKey]bool, now time.Time) []Reminder {
	out := make([]Reminder, 0, 4)
	for _, m := range machines {
		if !ValidMachineKey(m.Key) || st.IsMuted(m.Key) {
			continue
		}
		s, ok := m.Status(now)
		if !ok || s.Days < 0 || !st.HasDay(s.Days) {
			continue
		}
		if acked[AckKey{Key: m.Key, Due: s.NextDue}] {
			continue
		}
		out = append(out, Reminder{Machine: m, Due: s.NextDue, Days: s.Days, Cycle: s.Cycle})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due < out[j].Due
		}
		return out[i].Machine.Name < out[j].Machine.Name
	})
	return out
}

// NextAfter is the due date after due (one more cycle, chained like the
// rolling helper); 0 without a known cycle.
func NextAfter(due int64, cycle string) int64 {
	if renewal.CycleMonths(renewal.NormalizeCycle(cycle)) == 0 || due <= 0 {
		return 0
	}
	return renewal.NextDue(due, cycle, time.UnixMilli(due+1))
}
