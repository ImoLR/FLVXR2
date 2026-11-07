package repo

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Plan 234: margins (defaults, parse, cache) and the bounded switch counts.

func openSwitchRepo(t *testing.T, path string) *Repository {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestSwitchMarginsDefaultsParseAndCache(t *testing.T) {
	r := openSwitchRepo(t, filepath.Join(t.TempDir(), "m.db"))
	if m := r.SwitchMargins(); m != (SwitchMargins{BestMs: 5, RealityDomainMs: 5, RealityIPMs: 20}) {
		t.Fatalf("defaults %+v", m)
	}
	for _, c := range []struct {
		in string
		ok bool
	}{{"0", true}, {"200", true}, {" 7 ", true}, {"201", false}, {"-1", false}, {"5.5", false}, {"", false}, {"abc", false}} {
		if _, err := ParseSwitchMargin(c.in); (err == nil) != c.ok {
			t.Fatalf("parse %q err=%v", c.in, err)
		}
	}
	if !SwitchMarginBeats(5, 5) || SwitchMarginBeats(4.9, 5) || SwitchMarginBeats(0, 0) || !SwitchMarginBeats(0.1, 0) {
		t.Fatal("SwitchMarginBeats rule")
	}
	if err := r.SetSwitchMargins(SwitchMargins{BestMs: 201}); err == nil {
		t.Fatal("201 saved")
	}
	if err := r.SetSwitchMargins(SwitchMargins{BestMs: 8, RealityDomainMs: 0, RealityIPMs: 40}); err != nil {
		t.Fatal(err)
	}
	if m := r.SwitchMargins(); m.BestMs != 8 || m.RealityDomainMs != 0 || m.RealityIPMs != 40 {
		t.Fatalf("saved %+v", m)
	}
	// A write behind the cache's back is seen after invalidation (or the TTL);
	// a malformed value is its default.
	if err := r.UpsertConfig(SwitchMarginRealityIPKey, "garbage", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if r.SwitchMargins().RealityIPMs != 40 {
		t.Fatal("cache not used")
	}
	r.InvalidateSwitchMargins()
	if m := r.SwitchMargins(); m.RealityIPMs != 20 || m.BestMs != 8 {
		t.Fatalf("after invalidate %+v", m)
	}
	r.margins.Store(&switchMarginsCache{m: SwitchMargins{BestMs: 99}, at: time.Now().Add(-switchMarginsTTL - time.Second)})
	if r.SwitchMargins().BestMs != 8 {
		t.Fatal("expired cache served")
	}
}

func TestAutoSwitchCountsBoundedAndPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	r := openSwitchRepo(t, path)
	if err := r.StartAutoSwitchCounts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// 40 days of history: only the newest autoSwitchKeepDays days are kept.
	r.switches.mu.Lock()
	r.switches.counts.Start = now.AddDate(0, 0, -60).UnixMilli()
	r.switches.mu.Unlock()
	for d := 0; d < 40; d++ {
		r.NoteAutoSwitch(AutoSwitchEvent{Kind: AutoSwitchBest, Unit: 3, At: now.AddDate(0, 0, -d)})
	}
	r.NoteAutoSwitch(AutoSwitchEvent{Kind: AutoSwitchBest, Unit: 3, Failover: true, At: now})
	r.NoteAutoSwitch(AutoSwitchEvent{Kind: AutoSwitchRealityIP, Unit: 5, At: now})
	if err := r.FlushAutoSwitches(); err != nil {
		t.Fatal(err)
	}
	raw, err := r.GetViteConfigValue(AutoSwitchCountsKey)
	if err != nil {
		t.Fatal(err)
	}
	var stored autoSwitchCounts
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || len(stored.Days) > autoSwitchKeepDays {
		t.Fatalf("stored %d days (%v): %s", len(stored.Days), err, raw)
	}
	s := r.AutoSwitchSummaries(now)
	// 7 days in the window, one switch each + 1 failover today.
	if b := s[AutoSwitchBest]; b.Total != 8 || b.Failover != 1 || b.BusyUnit != 3 || b.BusyCount != 7 || b.Partial {
		t.Fatalf("best summary %+v", b)
	}
	if ip := s[AutoSwitchRealityIP]; ip.Total != 1 || ip.BusyUnit != 0 {
		t.Fatalf("ip summary %+v", ip)
	}
	if d := s[AutoSwitchRealityDomain]; d.Total != 0 {
		t.Fatalf("domain summary %+v", d)
	}
	// A new panel process reads the same counts back.
	_ = r.Close()
	r2 := openSwitchRepo(t, path)
	if err := r2.StartAutoSwitchCounts(); err != nil {
		t.Fatal(err)
	}
	if b := r2.AutoSwitchSummaries(now)[AutoSwitchBest]; b.Total != 8 || b.BusyCount != 7 {
		t.Fatalf("after reopen %+v", b)
	}
	// Nothing new → no write.
	r2.switches.mu.Lock()
	dirty := r2.switches.dirty
	r2.switches.mu.Unlock()
	if dirty {
		t.Fatal("reopen marked dirty")
	}
}

func TestAutoSwitchCountsPartialWindow(t *testing.T) {
	r := openSwitchRepo(t, filepath.Join(t.TempDir(), "p.db"))
	if err := r.StartAutoSwitchCounts(); err != nil {
		t.Fatal(err)
	}
	s := r.AutoSwitchSummaries(time.Now())[AutoSwitchBest]
	if !s.Partial || s.Since <= 0 || s.Total != 0 {
		t.Fatalf("fresh counts %+v", s)
	}
	// Fewer than 7 margin switches of one unit: no busy hint.
	for i := 0; i < 6; i++ {
		r.NoteAutoSwitch(AutoSwitchEvent{Kind: AutoSwitchRealityDomain, Unit: 2})
	}
	if d := r.AutoSwitchSummaries(time.Now())[AutoSwitchRealityDomain]; d.Total != 6 || d.BusyUnit != 0 {
		t.Fatalf("6 switches %+v", d)
	}
}

func TestRealityPinMoves(t *testing.T) {
	at := time.Now()
	old := []RealityPin{{Host: "a.com", Port: 443, PinnedIP: "1.1.1.1"}, {Host: "b.com", Port: 443}}
	cur := []RealityPin{
		{Host: "A.com", Port: 443, PinnedIP: "1.1.1.2", MoveReason: RealityPinMoveFaster}, // moved (host case)
		{Host: "a.com", Port: 443, PinnedIP: "1.1.1.2"},                                   // same IP twice (Helper nodes)
		{Host: "a.com", Port: 443, PinnedIP: "1.1.1.1"},                                   // nodes still on the old IP
		{Host: "b.com", Port: 443, PinnedIP: "2.2.2.2"},                                   // first pin
		{Host: "c.com", Port: 443, PinnedIP: "3.3.3.3"},                                   // new dest
	}
	moves := RealityPinMoves(4, old, cur, at)
	if len(moves) != 1 || moves[0].Unit != 4 || moves[0].Failover || moves[0].Kind != AutoSwitchRealityIP {
		t.Fatalf("moves %+v", moves)
	}
	// Fallback to the host (no IP answered) is not a switch; the way back is not either.
	if m := RealityPinMoves(4, cur, []RealityPin{{Host: "a.com", Port: 443}}, at); len(m) != 0 {
		t.Fatalf("fallback counted %+v", m)
	}
	if m := RealityPinMoves(4, []RealityPin{{Host: "a.com", Port: 443}}, []RealityPin{{Host: "a.com", Port: 443, PinnedIP: "9.9.9.9"}}, at); len(m) != 0 {
		t.Fatalf("re-pin after fallback counted %+v", m)
	}
}

// Unknown move reasons (a Helper / agent bug) are dropped before storing.
func TestRecordRealityPinsMoveReason(t *testing.T) {
	r := openSwitchRepo(t, filepath.Join(t.TempDir(), "pins.db"))
	if err := r.db.Exec("INSERT INTO landing_server (id, node_id, source, name, public_host, port_min, port_max, status, created_time, updated_time) VALUES (1, 0, 'native', 's', '203.0.113.1', 30000, 30010, 'active', 1, 1)").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordLandingRealityPins(1, []RealityPin{{Host: "a.com", Port: 443, PinnedIP: "1.1.1.1", CheckedAt: 1}}); err != nil {
		t.Fatal(err)
	}
	moves, err := r.RecordLandingRealityPins(1, []RealityPin{{Host: "a.com", Port: 443, PinnedIP: "1.1.1.2", CheckedAt: 2, MoveReason: "<script>"}})
	if err != nil || len(moves) != 1 || moves[0].Failover {
		t.Fatalf("moves %+v %v", moves, err)
	}
	var raw string
	r.db.Raw("SELECT reality_pins FROM landing_server WHERE id = 1").Scan(&raw)
	var pins []RealityPin
	if json.Unmarshal([]byte(raw), &pins) != nil || len(pins) != 1 || pins[0].MoveReason != "" {
		t.Fatalf("stored %s", raw)
	}
}
