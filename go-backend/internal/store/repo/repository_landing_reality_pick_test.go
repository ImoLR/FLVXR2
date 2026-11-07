package repo

import "testing"

// Plan 233: the automatic Reality pick keeps the current default unless a
// challenger ranks ≥ RealityPickSwitchMarginMs faster; it switches at once
// when the current one is not pickable any more.

func rs(target string, samples ...int) RealityProbeResult {
	r := RealityProbeResult{Target: target, Feasible: true, LatencyMs: samples[0], Samples: samples}
	if len(samples) > 1 {
		worst := 0
		for _, m := range samples {
			worst = max(worst, m)
		}
		r.WorstMs, r.MedianMs = worst, median(samples)
	}
	return r
}

func pickTarget(d RealityPickDecision) string {
	if d.Pick == nil {
		return ""
	}
	return d.Pick.Target
}

func TestRealityPickSwitchMarginMatchesBestPath(t *testing.T) {
	if RealityPickSwitchMarginMs != 5 {
		t.Fatalf("margin %d, want 5 (same as 最优 bestPathSwitchMarginMS)", RealityPickSwitchMarginMs)
	}
}

func TestDecideRealityPickMargin(t *testing.T) {
	cases := []struct {
		name    string
		rows    []RealityProbeResult
		blocked map[string]bool
		current string
		want    string
		held    bool
		gap     int
	}{
		{"no current yet → fastest", []RealityProbeResult{rs("a.com:443", 7, 7, 7), rs("b.com:443", 6, 6, 6)}, nil, "", "b.com:443", false, 0},
		{"1 ms faster → keep", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 7, 7, 7)}, nil, "a.com:443", "a.com:443", true, 1},
		{"tie → keep", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 6, 6, 6)}, nil, "a.com:443", "a.com:443", true, 0},
		{"4 ms faster → keep", []RealityProbeResult{rs("b.com:443", 3, 3, 3), rs("a.com:443", 7, 7, 7)}, nil, "a.com:443", "a.com:443", true, 4},
		{"exactly 5 ms → switch", []RealityProbeResult{rs("b.com:443", 2, 2, 2), rs("a.com:443", 7, 7, 7)}, nil, "a.com:443", "b.com:443", false, 0},
		{"worst sample decides", []RealityProbeResult{rs("b.com:443", 2, 2, 9), rs("a.com:443", 8, 8, 8)}, nil, "a.com:443", "a.com:443", false, 0},
		{"current is best → no hold", []RealityProbeResult{rs("a.com:443", 5, 5, 5), rs("b.com:443", 6, 6, 6)}, nil, "a.com:443", "a.com:443", false, 0},
		{"current single sample, best re-sampled", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 7)}, nil, "a.com:443", "a.com:443", true, 1},
		{"current single sample faster than best worst", []RealityProbeResult{rs("b.com:443", 6, 6, 9), rs("a.com:443", 7)}, nil, "a.com:443", "a.com:443", true, 0},
		{"dest key: case + default port", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 7, 7, 7)}, nil, "A.com", "a.com:443", true, 1},
		{"other port is another dest", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 7, 7, 7)}, nil, "a.com:8443", "b.com:443", false, 0},
		{"current dropped from candidates → switch", []RealityProbeResult{rs("b.com:443", 6, 6, 6)}, nil, "a.com:443", "b.com:443", false, 0},
		{"current blocked → switch", []RealityProbeResult{rs("b.com:443", 6, 6, 6), rs("a.com:443", 7, 7, 7)}, map[string]bool{"a.com": true}, "a.com:443", "b.com:443", false, 0},
		{"nothing pickable → unchanged", []RealityProbeResult{{Target: "a.com:443", Reason: "握手失败"}}, nil, "a.com:443", "", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DecideRealityPick(c.rows, func(h string) bool { return c.blocked[h] }, c.current)
			if pickTarget(d) != c.want || d.Held != c.held || d.GapMs != c.gap {
				t.Fatalf("pick=%q held=%v gap=%d, want %q %v %d", pickTarget(d), d.Held, d.GapMs, c.want, c.held, c.gap)
			}
			if d.Held && (d.Best == nil || d.Best.Target == d.Pick.Target) {
				t.Fatalf("held without a faster challenger: %+v", d.Best)
			}
		})
	}
}

func TestDecideRealityPickImmediateWhenCurrentInfeasible(t *testing.T) {
	failed := rs("a.com:443", 7, 7, 7)
	failed.Feasible, failed.Reason = false, "复测不可用：握手超时"
	d := DecideRealityPick([]RealityProbeResult{rs("b.com:443", 6, 6, 6), failed}, nil, "a.com:443")
	if pickTarget(d) != "b.com:443" || d.Held {
		t.Fatalf("handshake failed: %+v", d)
	}
	// 试用 site that lost 稳定 → switch at once even though 1 ms apart.
	trial := rs("a.com:443", 7, 7, 7)
	trial.Trial, trial.TrialOK = true, false
	if d := DecideRealityPick([]RealityProbeResult{rs("b.com:443", 6, 6, 6), trial}, nil, "a.com:443"); pickTarget(d) != "b.com:443" || d.Held {
		t.Fatalf("trial lost: %+v", d)
	}
	// Still 稳定 → normal margin applies.
	trial.TrialOK = true
	if d := DecideRealityPick([]RealityProbeResult{rs("b.com:443", 6, 6, 6), trial}, nil, "a.com:443"); pickTarget(d) != "a.com:443" || !d.Held {
		t.Fatalf("trial stable: %+v", d)
	}
}

func TestDecideRealityPickCancelledCurrentUnchanged(t *testing.T) {
	cancelled := RealityProbeResult{Target: "a.com:443", Reason: "已取消"}
	d := DecideRealityPick([]RealityProbeResult{rs("b.com:443", 1, 1, 1), cancelled}, nil, "a.com:443")
	if d.Pick != nil || d.Held {
		t.Fatalf("cancelled current must leave the default alone: %+v", d)
	}
}

// The Bestvm US shape from the fork.123 check: big sites at 6–7 ms, 1 ms
// apart, ranking order changing every day → the pick no longer moves.
func TestDecideRealityPickStopsDailyFlip(t *testing.T) {
	days := [][]RealityProbeResult{
		{rs("www.a.com:443", 6, 6, 6), rs("www.b.com:443", 6, 7, 7), rs("www.c.com:443", 7, 7, 7)},
		{rs("www.b.com:443", 6, 6, 6), rs("www.c.com:443", 6, 6, 7), rs("www.a.com:443", 7, 7, 7)},
		{rs("www.c.com:443", 6, 6, 6), rs("www.a.com:443", 6, 7, 7), rs("www.b.com:443", 7, 7, 7)},
	}
	current := ""
	for i, rows := range days {
		d := DecideRealityPick(rows, nil, current)
		if i > 0 && pickTarget(d) != current {
			t.Fatalf("day %d flipped %s → %s", i, current, pickTarget(d))
		}
		current = pickTarget(d)
	}
}
