package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-backend/internal/store/model"
)

// Plan 229: Reality 测速 more than once without bursts, and 妙妙屋池 small
// sites 「试用」 until they prove stable on that machine.
//
//   - Pass 1 probes every candidate once (≤ 64). The 5 fastest usable ones
//     (+ ≤ 2 试用 sites that could beat them) get 2 more samples each, one
//     domain at a time, ≥ 5 s apart. The pick ranks the re-sampled ones by the
//     WORST of their 3 samples (tie → median): one lucky fast sample can no
//     longer win.
//   - Every sample goes into a small per-server history (last 8 per domain,
//     ≤ 7 days). A 试用 site (pool domain that is not a built-in vendor or a
//     known big kind) is picked automatically only when its history is stable:
//     ≥ 3 samples spanning ≥ 6 h, all usable, jitter (max − min) within the
//     limit, and not much slower than the fastest non-试用 domain.
const (
	RealityResampleTop      = 5
	RealityResampleTrialMax = 2
	RealityResampleRounds   = 2

	RealitySampleKeep   = 8
	RealitySampleMaxAge = 7 * 24 * time.Hour

	RealityTrialMinSamples = 3
	RealityTrialMinSpan    = 6 * time.Hour
	// Jitter limit = max(RealityTrialJitterMs, RealityTrialJitterPct % of the
	// median); margin = how much slower than the fastest non-试用 domain.
	RealityTrialJitterMs  = 10
	RealityTrialJitterPct = 30
	RealityTrialMarginMs  = 5
)

// RealitySample is one measurement: unix seconds + TLS handshake ms (-1 =
// not usable that time). JSON: [at, ms].
type RealitySample struct {
	At int64
	Ms int
}

func (s RealitySample) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("[%d,%d]", s.At, s.Ms)), nil
}

func (s *RealitySample) UnmarshalJSON(b []byte) error {
	var v [2]int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.At, s.Ms = v[0], int(v[1])
	return nil
}

// RealityProbeStats is what the last 测速 cost on the machine.
type RealityProbeStats struct {
	At         int64 `json:"at"`                // unix ms
	Passes     int   `json:"passes"`            // 1 + re-sample passes
	Resampled  int   `json:"resampled"`         // domains re-sampled
	Sockets    int   `json:"sockets,omitempty"` // TCP connects (agent ≥ landing_reality 2)
	MaxOpen    int   `json:"maxOpen,omitempty"` // most open at once (ditto)
	DurationMs int64 `json:"durationMs"`        // whole 测速 incl. the 5 s gaps
	Linger0    bool  `json:"linger0"`           // RST close, no TIME_WAIT (ditto)
	Measured   bool  `json:"measured"`          // sockets / maxOpen reported by the machine
}

// RealitySampleHistory is landing_server.reality_samples.
type RealitySampleHistory struct {
	V     int                        `json:"v"`
	Stats *RealityProbeStats         `json:"stats,omitempty"`
	D     map[string][]RealitySample `json:"d"`
}

func LandingRealityHistory(s *model.LandingServer) RealitySampleHistory {
	h := RealitySampleHistory{V: 1, D: map[string][]RealitySample{}}
	if s == nil || s.RealitySamples == "" {
		return h
	}
	if json.Unmarshal([]byte(s.RealitySamples), &h) != nil || h.D == nil {
		return RealitySampleHistory{V: 1, D: map[string][]RealitySample{}}
	}
	return h
}

// add appends one probe's samples of a domain (all at the probe time) and
// keeps the last RealitySampleKeep.
func (h *RealitySampleHistory) add(domain string, at int64, ms []int) {
	if len(ms) == 0 {
		return
	}
	list := h.D[domain]
	for _, m := range ms {
		list = append(list, RealitySample{At: at, Ms: m})
	}
	if len(list) > RealitySampleKeep {
		list = list[len(list)-RealitySampleKeep:]
	}
	h.D[domain] = list
}

// prune drops samples older than RealitySampleMaxAge (and empty domains).
func (h *RealitySampleHistory) prune(now time.Time) {
	cutoff := now.Add(-RealitySampleMaxAge).Unix()
	for d, list := range h.D {
		i := 0
		for i < len(list) && list[i].At < cutoff {
			i++
		}
		if i == len(list) {
			delete(h.D, d)
		} else if i > 0 {
			h.D[d] = append([]RealitySample(nil), list[i:]...)
		}
	}
}

// with = the history of domain plus extra samples at `at` (not stored).
func (h RealitySampleHistory) with(domain string, at int64, ms []int) []RealitySample {
	out := append([]RealitySample(nil), h.D[domain]...)
	for _, m := range ms {
		out = append(out, RealitySample{At: at, Ms: m})
	}
	if len(out) > RealitySampleKeep {
		out = out[len(out)-RealitySampleKeep:]
	}
	return out
}

// realityBrand is the name part of the site (amd for www.amd.com,
// mozilla for download-installer.cdn.mozilla.net, sony for www.sony.co.jp).
func realityBrand(host string) string {
	reg := registrableDomain(strings.ToLower(host))
	if i := strings.IndexByte(reg, '.'); i > 0 {
		return reg[:i]
	}
	return reg
}

var realityBuiltinBrands = func() map[string]bool {
	out := map[string]bool{}
	for _, s := range realityBuiltinSeeds {
		out[realityBrand(s.domain)] = true
	}
	return out
}()

// RealityTrialDomain: a 妙妙屋池 domain that is not of a built-in vendor
// (same brand as a built-in candidate) nor of a known big kind (the
// auto-block rules) = 「小网站」, 试用 until stable. Built-in and admin custom
// domains are never 试用.
func RealityTrialDomain(d model.LandingRealityDomain) bool {
	if d.Source != "pool" {
		return false
	}
	return !realityBuiltinBrands[realityBrand(d.Domain)] && realityRiskNote(d.Domain) == ""
}

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[(len(s)-1)/2] // lower median: 3 samples → the middle one
}

// RealityJitterLimit is the allowed max − min for a median.
func RealityJitterLimit(med int) int {
	return max(RealityTrialJitterMs, med*RealityTrialJitterPct/100)
}

// RealityTrialVerdict decides whether a 试用 site is stable enough on this
// machine. bestOther = the fastest non-试用 median of this 测速 (0 = none).
func RealityTrialVerdict(hist []RealitySample, bestOther int) (ok bool, reason string) {
	if len(hist) == 0 {
		return false, "还没有测速记录"
	}
	var ms []int
	failed := 0
	for _, s := range hist {
		if s.Ms < 0 {
			failed++
			continue
		}
		ms = append(ms, s.Ms)
	}
	if failed > 0 {
		return false, fmt.Sprintf("最近 %d 次里有 %d 次不可用", len(hist), failed)
	}
	lo, hi := ms[0], ms[0]
	for _, m := range ms {
		lo, hi = min(lo, m), max(hi, m)
	}
	med := median(ms)
	if len(ms) >= 2 && hi-lo > RealityJitterLimit(med) {
		return false, fmt.Sprintf("抖动 %d ms", hi-lo)
	}
	if len(hist) < RealityTrialMinSamples {
		return false, fmt.Sprintf("还要再测 %d 次", RealityTrialMinSamples-len(hist))
	}
	if time.Duration(hist[len(hist)-1].At-hist[0].At)*time.Second < RealityTrialMinSpan {
		return false, "要隔 6 小时以上再测一次"
	}
	if bestOther > 0 && med > bestOther+RealityTrialMarginMs {
		return false, fmt.Sprintf("比非试用里最快的慢 %d ms", med-bestOther)
	}
	return true, ""
}

// RealityPickInfo is what choosing needs besides the results.
type RealityPickInfo struct {
	Blocked map[string]bool
	Trial   map[string]bool
	History RealitySampleHistory
	Now     time.Time
}

func (r *Repository) RealityPickInfo(serverID int64) (RealityPickInfo, error) {
	info, err := r.realityDomainSets()
	if err != nil {
		return info, err
	}
	s, err := r.GetLandingServer(serverID)
	if err != nil {
		return info, err
	}
	info.History = LandingRealityHistory(s)
	return info, nil
}

// realityDomainSets = the blocked and 试用 domains (history left empty).
func (r *Repository) realityDomainSets() (RealityPickInfo, error) {
	info := RealityPickInfo{Blocked: map[string]bool{}, Trial: map[string]bool{}, Now: time.Now()}
	rows, err := r.ListLandingRealityDomains()
	if err != nil {
		return info, err
	}
	for _, d := range rows {
		if d.Blocked {
			info.Blocked[d.Domain] = true
		}
		if RealityTrialDomain(d) {
			info.Trial[d.Domain] = true
		}
	}
	return info, nil
}

// rowSamples are this 测速's samples of a result (pass 1 only when not merged).
func rowSamples(res RealityProbeResult) []int {
	if len(res.Samples) > 0 {
		return res.Samples
	}
	if res.Feasible && res.LatencyMs > 0 {
		return []int{res.LatencyMs}
	}
	return []int{-1}
}

// bestOtherMedian = the fastest median among usable non-试用, non-blocked rows.
func bestOtherMedian(results []RealityProbeResult, info RealityPickInfo) int {
	best := 0
	for _, res := range results {
		host := realityHost(res.Target)
		if !res.Feasible || info.Blocked[host] || info.Trial[host] {
			continue
		}
		if m := median(rowSamples(res)); m > 0 && (best == 0 || m < best) {
			best = m
		}
	}
	return best
}

// RealityResampleSet picks what gets 2 more samples after pass 1: the 5
// fastest usable domains that may be picked automatically, plus up to 2 试用
// sites not yet proven stable that are at least as fast as the slowest of
// those 5 (so they collect history while they matter).
func RealityResampleSet(pass1 []RealityProbeResult, info RealityPickInfo) []string {
	rows := append([]RealityProbeResult(nil), pass1...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].LatencyMs < rows[j].LatencyMs })
	bestOther := bestOtherMedian(rows, info)
	var set, trial []string
	limit := 0
	for _, res := range rows {
		host := realityHost(res.Target)
		if !res.Feasible || res.LatencyMs <= 0 || info.Blocked[host] {
			continue
		}
		qualified := true
		if info.Trial[host] {
			qualified, _ = RealityTrialVerdict(info.History.with(host, info.Now.Unix(), []int{res.LatencyMs}), bestOther)
		}
		switch {
		case qualified && len(set) < RealityResampleTop:
			set = append(set, res.Target)
			limit = res.LatencyMs
		case !qualified && len(trial) < RealityResampleTrialMax && (len(set) < RealityResampleTop || res.LatencyMs <= limit):
			trial = append(trial, res.Target)
		}
	}
	return append(set, trial...)
}

// MergeRealitySamples folds the re-sample passes into the pass-1 rows:
// samples, worst, median; a row is usable only when every sample was.
// Rows come back ranked: usable first, re-sampled (by worst, then median)
// before single samples (by latency), failures last.
func MergeRealitySamples(pass1 []RealityProbeResult, passes [][]RealityProbeResult) []RealityProbeResult {
	out := make([]RealityProbeResult, len(pass1))
	index := map[string]int{}
	for i, res := range pass1 {
		res.Samples = rowSamples(res)
		out[i] = res
		index[res.Target] = i
	}
	for _, pass := range passes {
		for _, re := range pass {
			i, ok := index[re.Target]
			if !ok {
				continue
			}
			ms := -1
			if re.Feasible && re.LatencyMs > 0 {
				ms = re.LatencyMs
			} else if out[i].Feasible {
				out[i].Feasible = false
				out[i].Reason = "复测不可用：" + re.Reason
			}
			out[i].Samples = append(out[i].Samples, ms)
		}
	}
	for i := range out {
		var ok []int
		worst := 0
		for _, m := range out[i].Samples {
			if m > 0 {
				ok = append(ok, m)
				worst = max(worst, m)
			}
		}
		if out[i].Feasible && len(ok) > 0 {
			out[i].WorstMs, out[i].MedianMs = worst, median(ok)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return realityRankLess(out[i], out[j]) })
	return out
}

func realityRankLess(a, b RealityProbeResult) bool {
	if a.Feasible != b.Feasible {
		return a.Feasible
	}
	if !a.Feasible {
		return false
	}
	ra, rb := len(a.Samples) > 1, len(b.Samples) > 1
	if ra != rb {
		return ra
	}
	if ra {
		if a.WorstMs != b.WorstMs {
			return a.WorstMs < b.WorstMs
		}
		return a.MedianMs < b.MedianMs
	}
	return a.LatencyMs < b.LatencyMs
}

// annotateTrial sets Trial / TrialOK / TrialReason on 试用 rows (history must
// already contain this 测速's samples).
func annotateTrial(results []RealityProbeResult, info RealityPickInfo) {
	bestOther := bestOtherMedian(results, info)
	for i := range results {
		host := realityHost(results[i].Target)
		if !info.Trial[host] {
			continue
		}
		results[i].Trial = true
		results[i].TrialOK, results[i].TrialReason = RealityTrialVerdict(info.History.D[host], bestOther)
	}
}

// PickRealityResult is the automatic choice: usable, not blocked, not an
// unproven 试用 site; re-sampled rows first by worst sample (tie → median),
// else the fastest single sample (old behaviour when nothing was re-sampled).
func PickRealityResult(results []RealityProbeResult, blocked func(host string) bool) *RealityProbeResult {
	var best *RealityProbeResult
	for i := range results {
		res := &results[i]
		if !res.Feasible || res.LatencyMs <= 0 || (blocked != nil && blocked(realityHost(res.Target))) || (res.Trial && !res.TrialOK) {
			continue
		}
		if best == nil || realityRankLess(*res, *best) {
			best = res
		}
	}
	return best
}

// RealityHistoryJitter = max − min of the usable samples (0 with < 2).
func RealityHistoryJitter(hist []RealitySample) int {
	lo, hi, n := 0, 0, 0
	for _, s := range hist {
		if s.Ms < 0 {
			continue
		}
		if n == 0 {
			lo, hi = s.Ms, s.Ms
		}
		lo, hi = min(lo, s.Ms), max(hi, s.Ms)
		n++
	}
	if n < 2 {
		return 0
	}
	return hi - lo
}
