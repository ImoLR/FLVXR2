package renewal

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type fixture struct {
	Name   string  `json:"name"`
	Anchor *string `json:"anchor"`
	Cycle  string  `json:"cycle"`
	Now    string  `json:"now"`
	Next   string  `json:"next"`
	Days   int     `json:"days"`
	TSDays *int    `json:"tsDays"`
}

func parse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// The same fixtures are replayed against the TS helper (see plan 194):
// both must roll to the same next due date.
func TestComputeFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/rolling.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []fixture `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 15 {
		t.Fatalf("only %d fixtures", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		now := parse(t, c.Now)
		if c.Anchor == nil {
			if _, ok := Compute(0, c.Cycle, now); ok {
				t.Errorf("%s: no expiry accepted", c.Name)
			}
			continue
		}
		st, ok := Compute(parse(t, *c.Anchor).UnixMilli(), c.Cycle, now)
		if !ok {
			t.Errorf("%s: not ok", c.Name)
			continue
		}
		if want := parse(t, c.Next).UnixMilli(); st.NextDue != want {
			t.Errorf("%s: next %s, want %s", c.Name, time.UnixMilli(st.NextDue).In(Zone).Format(time.RFC3339Nano), c.Next)
		}
		if st.Days != c.Days {
			t.Errorf("%s: days %d, want %d", c.Name, st.Days, c.Days)
		}
		if st.Cycle != NormalizeCycle(c.Cycle) {
			t.Errorf("%s: cycle %q", c.Name, st.Cycle)
		}
		if st.Cycle != "" && st.Days < 0 {
			t.Errorf("%s: a cycle node can never be overdue (%d)", c.Name, st.Days)
		}
	}
}

func TestNormalizeCycle(t *testing.T) {
	for raw, want := range map[string]string{
		"month": CycleMonth, " Quarter ": CycleQuarter, "halfYear": CycleHalfYear, "HALFYEAR": CycleHalfYear,
		"year": CycleYear, "": "", "weekly": "", "half_year": "", "monthly": "",
	} {
		if got := NormalizeCycle(raw); got != want {
			t.Errorf("NormalizeCycle(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDaysUntilClampAndSign(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, Zone)
	far := time.Date(9999, 12, 31, 0, 0, 0, 0, Zone).UnixMilli()
	if d := DaysUntil(far, now); d != MaxDays {
		t.Errorf("far future %d", d)
	}
	if d := DaysUntil(-62135596800000, now); d != -MaxDays { // year 1
		t.Errorf("far past %d", d)
	}
	if d := DaysUntil(1, now); d >= 0 || d < -MaxDays {
		t.Errorf("1970 %d", d)
	}
	// Late evening vs early morning of the next local day.
	if d := DaysUntil(time.Date(2026, 11, 3, 0, 1, 0, 0, Zone).UnixMilli(), time.Date(2026, 11, 2, 23, 59, 0, 0, Zone)); d != 1 {
		t.Errorf("tomorrow %d", d)
	}
	if d := DaysUntil(time.Date(2026, 11, 1, 23, 59, 0, 0, Zone).UnixMilli(), now); d != -1 {
		t.Errorf("yesterday %d", d)
	}
}

func TestNextDueBounded(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, Zone)
	// 1970 → 2200 monthly needs > maxSteps steps: the loop stops (no hang)
	// and the result simply stays in the past.
	if due := NextDue(1, CycleMonth, time.Date(2200, 1, 1, 0, 0, 0, 0, Zone)); due != time.Date(2070, 1, 1, 8, 0, 0, 1e6, Zone).UnixMilli() {
		t.Errorf("roll not bounded at %d steps: %s", maxSteps, time.UnixMilli(due).In(Zone))
	}
	if due := NextDue(-5, CycleMonth, now); due != -5 {
		t.Errorf("negative anchor rolled: %d", due)
	}
	if due := NextDue(0, CycleMonth, now); due != 0 {
		t.Errorf("zero anchor rolled: %d", due)
	}
	anchor := time.Date(2026, 10, 1, 0, 0, 0, 0, Zone).UnixMilli()
	if due := NextDue(anchor, "", now); due != anchor {
		t.Errorf("no cycle rolled: %d", due)
	}
}
