package probe

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tw builds a UTC+8 wall-clock time (tests read like the operators' clock).
func tw(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, expiryZone)
}

func TestExpiryDays(t *testing.T) {
	now := tw(2026, 11, 2, 12, 0)
	cases := []struct {
		name   string
		expiry time.Time
		cycle  string
		days   int
		token  string
	}{
		{"later today", tw(2026, 11, 2, 23, 59), "", 0, ""},
		{"earlier today, no cycle", tw(2026, 11, 2, 0, 1), "", 0, ""},
		{"tomorrow just after midnight", tw(2026, 11, 3, 0, 1), "", 1, ""},
		{"yesterday", tw(2026, 11, 1, 23, 59), "", -1, ""},
		{"expired 3 days", tw(2026, 10, 30, 8, 0), "", -3, ""},
		{"12 days", tw(2026, 11, 14, 9, 0), "month", 12, CycleMonth},
		{"month rolls forward", tw(2026, 10, 20, 9, 0), "month", 18, CycleMonth},
		{"month due earlier today rolls a month", tw(2026, 11, 2, 11, 0), "month", 30, CycleMonth},
		{"year rolls twice", tw(2025, 3, 1, 10, 0), "year", 119, CycleYear},
		{"quarter", tw(2026, 9, 1, 10, 0), "quarter", 29, CycleQuarter},
		{"halfYear mixed case", tw(2026, 5, 10, 10, 0), " halfYear ", 8, CycleHalfYear},
		{"unknown cycle never rolls", tw(2026, 10, 30, 8, 0), "weekly", -3, ""},
		{"future with year cycle", tw(2027, 2, 14, 0, 0), "YEAR", 104, CycleYear},
	}
	for _, c := range cases {
		days, token, ok := ExpiryDays(c.expiry.UnixMilli(), c.cycle, now)
		if !ok || days != c.days || token != c.token {
			t.Errorf("%s: got %d %q %v, want %d %q", c.name, days, token, ok, c.days, c.token)
		}
	}
	// Month end: Jan 31 + 1 month = Mar 3 (Go AddDate, JS setMonth and the
	// renewal job all normalise the same way).
	if days, _, _ := ExpiryDays(tw(2026, 1, 31, 10, 0).UnixMilli(), "month", tw(2026, 2, 15, 10, 0)); days != 16 {
		t.Errorf("month end: %d", days)
	}
	// Exactly now: 今天到期, no roll-forward.
	if days, _, _ := ExpiryDays(now.UnixMilli(), "month", now); days != 0 {
		t.Errorf("exactly now: %d", days)
	}
	// Calendar days in UTC+8, not UTC: 16:30 UTC is already the next day.
	utcEvening := time.Date(2026, 11, 2, 16, 30, 0, 0, time.UTC) // 11-03 00:30 UTC+8
	if days, _, _ := ExpiryDays(tw(2026, 11, 3, 23, 0).UnixMilli(), "", utcEvening); days != 0 {
		t.Errorf("UTC+8 midnight: %d", days)
	}
	// Not set / nonsense.
	for _, ms := range []int64{0, -1} {
		if _, _, ok := ExpiryDays(ms, "month", now); ok {
			t.Errorf("expiry %d accepted", ms)
		}
	}
	if days, _, _ := ExpiryDays(tw(9999, 12, 31, 0, 0).UnixMilli(), "", now); days != maxExpiryDays {
		t.Errorf("far future not clamped: %d", days)
	}
	if days, _, _ := ExpiryDays(1, "", now); days >= 0 || days < -maxExpiryDays {
		t.Errorf("1970 without cycle: %d", days)
	}
	if days, _, _ := ExpiryDays(1, "month", now); days < 0 || days > 31 {
		t.Errorf("1970 monthly should roll to the next due date: %d", days)
	}
}

// Plan 194 whitelist: days + cycle only, never the stored timestamp; switch
// off or no expiry → both keys absent; hidden nodes are not passed in at all.
func TestBuildSnapshotExpiry(t *testing.T) {
	now := tw(2026, 11, 2, 12, 0)
	in := demoInput(now)
	yearly := tw(2027, 2, 14, 9, 30).UnixMilli()
	expired := tw(2026, 10, 30, 8, 0).UnixMilli()
	in.Nodes[0].ExpiryTime, in.Nodes[0].RenewalCycle = yearly, "year"
	in.Nodes[1].ExpiryTime, in.Nodes[1].RenewalCycle = expired, "bogus"
	in.ExpiryOn = true
	snap := BuildSnapshot(in)
	if got := strings.Join(keysOf(t, snap), ","); got != "latency_on,nodes,title,ts" {
		t.Fatalf("snapshot keys %s", got)
	}
	want := "arch,cpu,cycle,days,disk,k,latency,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
	if got := strings.Join(keysOf(t, snap.Nodes[0]), ","); got != want {
		t.Fatalf("node keys\n got %s\nwant %s", got, want)
	}
	// Unknown cycle: days only.
	if got := strings.Join(keysOf(t, snap.Nodes[1]), ","); strings.Contains(got, "cycle") || !strings.Contains(got, "days") {
		t.Fatalf("expired node keys %s", got)
	}
	// No expiry: neither key.
	if got := strings.Join(keysOf(t, snap.Nodes[2]), ","); strings.Contains(got, "days") || strings.Contains(got, "cycle") {
		t.Fatalf("no-expiry node keys %s", got)
	}
	if n := snap.Nodes[0]; n.Days == nil || *n.Days != 104 || n.Cycle != CycleYear {
		t.Fatalf("yearly node %+v", n)
	}
	if n := snap.Nodes[1]; n.Days == nil || *n.Days != -3 || n.Cycle != "" {
		t.Fatalf("expired node %+v", n)
	}
	raw, _ := json.Marshal(snap)
	for _, ms := range []int64{yearly, expired} {
		for _, bad := range []string{strconv.FormatInt(ms, 10), strconv.FormatInt(ms/1000, 10), "bogus", "2027-02-14"} {
			if bytes.Contains(raw, []byte(bad)) {
				t.Fatalf("snapshot leaks %q: %s", bad, raw)
			}
		}
	}
	// Day 0 must survive omitempty (pointer).
	in.Nodes[2].ExpiryTime = tw(2026, 11, 2, 20, 0).UnixMilli()
	if n := BuildSnapshot(in).Nodes[2]; n.Days == nil || *n.Days != 0 {
		t.Fatalf("today lost: %+v", n)
	}
	in.ExpiryOn = false
	off := BuildSnapshot(in)
	raw, _ = json.Marshal(off)
	if bytes.Contains(raw, []byte(`"days"`)) || bytes.Contains(raw, []byte(`"cycle"`)) {
		t.Fatalf("switch off still publishes: %s", raw)
	}
}
