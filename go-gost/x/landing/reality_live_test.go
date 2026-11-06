package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRealityLive is a manual, read-only measuring tool (plan 229), skipped
// unless FLVX_REALITY_LIVE is set. Build it with `go test -c` and run the
// binary on a landing machine:
//
//	FLVX_REALITY_LIVE=probe FLVX_REALITY_CANDIDATES=list.txt [FLVX_REALITY_RESAMPLE=10] ./landing.test -test.run TestRealityLive
//	FLVX_REALITY_LIVE=pin   FLVX_REALITY_HOST=www.amd.com ./landing.test -test.run TestRealityLive
//
// FLVX_REALITY_FIN=1 closes sockets the fork.121 way (FIN → TIME_WAIT) for a
// before / after comparison. Output: one JSON document on stdout.
func TestRealityLive(t *testing.T) {
	mode := os.Getenv("FLVX_REALITY_LIVE")
	if mode == "" {
		t.Skip("manual tool: set FLVX_REALITY_LIVE")
	}
	if os.Getenv("FLVX_REALITY_FIN") == "1" {
		realityQuietClose = false
		defer func() { realityQuietClose = true }()
	}
	rn := defaultRealityNet()
	out := map[string]any{"mode": mode, "started": time.Now().Unix(), "linger0": realityQuietClose}
	switch mode {
	case "probe":
		var cands []string
		f, err := os.Open(os.Getenv("FLVX_REALITY_CANDIDATES"))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if c := strings.TrimSpace(sc.Text()); c != "" && !strings.HasPrefix(c, "#") {
				cands = append(cands, c)
			}
		}
		_ = f.Close()
		var passes []any
		var all []RealityProbeResult
		for i := 0; i < len(cands); i += realityProbeMaxCandidates {
			end := min(i+realityProbeMaxCandidates, len(cands))
			res, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: cands[i:end], TimeoutMs: 3000})
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, res.Results...)
			passes = append(passes, res.Stats)
		}
		sortRealityResults(all)
		out["pass1"] = all
		// Re-samples like the panel: the top N feasible, one domain at a
		// time, ≥ 5 s after the previous sample of that domain.
		n, _ := strconv.Atoi(os.Getenv("FLVX_REALITY_RESAMPLE"))
		var top []string
		for _, r := range all {
			if r.Feasible && len(top) < n {
				top = append(top, r.Target)
			}
		}
		var re []any
		for round := 0; round < 2 && len(top) > 0; round++ {
			time.Sleep(5 * time.Second)
			res, err := rn.probe(context.Background(), RealityProbeRequest{Candidates: top, TimeoutMs: 3000, Concurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			re = append(re, res)
		}
		out["passes"] = passes
		out["resample"] = re
	case "pin":
		p := newRealityPinner()
		p.net = rn
		p.logf = nil
		k := pinKey{os.Getenv("FLVX_REALITY_HOST"), 443}
		cnt := &socketCount{}
		p.net.count = cnt
		start := time.Now()
		p.mu.Lock()
		p.running[k] = true
		p.mu.Unlock()
		p.check(context.Background(), k)
		full := time.Since(start).Milliseconds()
		st := p.statuses()
		alive := false
		if len(st) == 1 && st[0].PinnedIP != "" {
			alive = p.alive(context.Background(), st[0].PinnedIP, 443)
		}
		out["pin"] = st
		out["alive"] = alive
		out["full_check_ms"] = full
		out["sockets"] = cnt.total
		out["max_open"] = cnt.peak
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	out["finished"] = time.Now().Unix()
	b, _ := json.Marshal(out)
	_, _ = os.Stdout.Write(append(b, '\n'))
}
