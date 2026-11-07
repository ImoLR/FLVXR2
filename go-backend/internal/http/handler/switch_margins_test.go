package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 234 「自动切换门槛」: the three margins come from the admin setting
// (defaults 5 / 5 / 20 = the values hard-coded before), 0 = any gain (a tie
// keeps the current one), a failover never waits for a margin, and the
// switches are counted for 「最近 7 天自动切换 N 次」.

func smCall(t *testing.T, h *Handler, path string, body any, role int) response.R {
	t.Helper()
	mux := http.NewServeMux()
	h.registerSwitchMargins(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: role}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	return out
}

func smSet(t *testing.T, r *repo.Repository, best, domain, ip int) {
	t.Helper()
	if err := r.SetSwitchMargins(repo.SwitchMargins{BestMs: best, RealityDomainMs: domain, RealityIPMs: ip}); err != nil {
		t.Fatal(err)
	}
}

// smBestSwitches runs 4 rounds of bpExample with B gain ms faster than A
// through the handler-wired margin.
func smBestSwitches(h *Handler, gain float64) int {
	m := newBestPathManager()
	m.margin = h.bestPath.margin
	n := bpExample()
	n.lat = map[[2]int64]float64{{1, 2}: 10, {2, 9}: 50, {1, 3}: 30, {3, 9}: 30 - gain}
	now := time.Unix(1_800_000_000, 0)
	total := 0
	for i := 0; i < 4; i++ {
		sws := bpRound(m, n, bpAllCapable, now.Add(time.Duration(i)*time.Minute))
		bpApply(m, sws, now)
		total += len(sws)
	}
	return total
}

func TestSwitchMarginBestFollowsSetting(t *testing.T) {
	e := newFlowTestEnv(t)
	e.h = New(e.r, "test-jwt-secret", "3.0.16") // the real wiring
	if e.h.bestPath.margin == nil || e.h.ruleBest.margin == nil {
		t.Fatal("最优 managers not wired to the setting")
	}
	// Default (no key) = 5 ms.
	if got := e.h.bestSwitchMargin(); got != 5 {
		t.Fatalf("default margin %v", got)
	}
	if smBestSwitches(e.h, 4.9) != 0 || smBestSwitches(e.h, 5) != 1 {
		t.Fatal("default 5 ms margin not applied")
	}
	smSet(t, e.r, 20, 5, 20)
	if smBestSwitches(e.h, 19) != 0 || smBestSwitches(e.h, 20) != 1 {
		t.Fatal("20 ms setting not applied")
	}
	smSet(t, e.r, 0, 5, 20)
	if smBestSwitches(e.h, 0.5) != 1 {
		t.Fatal("margin 0: a 0.5 ms gain must switch (after 2 rounds)")
	}

	// Margin 0, tie: B is current (after a real switch), A ties → stays on B
	// (before plan 234 a 0-gap tie would have flipped back to DB order).
	m := newBestPathManager()
	m.margin = e.h.bestPath.margin
	now := time.Unix(1_800_000_000, 0)
	fast := bpExample() // B 50 ms vs A 110 ms
	for i := 0; i < 2; i++ {
		bpApply(m, bpRound(m, fast, bpAllCapable, now.Add(time.Duration(i)*time.Minute)), now)
	}
	if order := m.order(bestPathKey{7, 1}); len(order) == 0 || order[0] != 3 {
		t.Fatalf("setup: B not current: %v", order)
	}
	tie := bpExample()
	tie.lat = map[[2]int64]float64{{1, 2}: 10, {2, 9}: 40, {1, 3}: 30, {3, 9}: 20} // both 50 ms
	for i := 2; i < 6; i++ {
		if sws := bpRound(m, tie, bpAllCapable, now.Add(time.Duration(i)*time.Minute)); len(sws) != 0 {
			t.Fatalf("margin 0 tie switched: %+v", sws)
		}
	}

	// Failover ignores the margin (200 ms): A's path breaks → at once.
	smSet(t, e.r, 200, 5, 20)
	m = newBestPathManager()
	m.margin = e.h.bestPath.margin
	broken := bpExample()
	broken.lat = map[[2]int64]float64{{1, 3}: 30, {3, 9}: 20} // A (current, DB first) unreachable
	sws := bpRound(m, broken, bpAllCapable, now)
	if len(sws) != 1 || !sws[0].Failover || sws[0].ToID != 3 {
		t.Fatalf("failover at 200 ms margin: %+v", sws)
	}
}

func TestSwitchMarginRuleBestFollowsSetting(t *testing.T) {
	e := newFlowTestEnv(t)
	e.h = New(e.r, "test-jwt-secret", "3.0.16")
	key := "t.example:80"
	plan := rbPlan(8, [][]int64{{1}, {4, 5}})
	rbDest(&plan, key)
	plans := []pathLatencyPlan{plan}
	edges := map[[2]int64]float64{{1, 4}: 10, {1, 5}: 10}
	now := time.Unix(1_800_000_000, 0)
	run := func(gap float64) int {
		m := newRuleBestManager()
		m.margin = e.h.ruleBest.margin
		total := 0
		for i := 0; i < 4; i++ {
			res := rbResults(plans, edges, map[string]map[int64]float64{key: {4: 20 + gap, 5: 20}})
			p := rbEval(m, plans, res, now.Add(time.Duration(i)*time.Minute))
			rbApply(m, p, now)
			total += len(p)
		}
		return total
	}
	if run(4) != 0 || run(6) != 1 {
		t.Fatal("default 5 ms not applied to 规则最优")
	}
	smSet(t, e.r, 10, 5, 20)
	if run(6) != 0 || run(10) != 1 {
		t.Fatal("10 ms setting not applied to 规则最优")
	}
	smSet(t, e.r, 0, 5, 20)
	if run(1) != 1 || run(0) != 0 {
		t.Fatal("margin 0: 1 ms gain switches, a tie does not")
	}
}

// Reality 伪装域名: the 测速 pick uses the setting; picks are counted
// (faster vs failover); the 测速 help text shows the margin in force.
func TestSwitchMarginRealityDomainSettingAndCounts(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	probe := func() map[string]any {
		t.Helper()
		out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
		if out.Code != 0 {
			t.Fatalf("probe: %+v", out)
		}
		return dataMap(t, out)
	}
	a.results = []map[string]any{
		probeRow("www.tesla.com:443", true, 12, "203.0.113.12", "www.tesla.com"),
		probeRow("www.nvidia.com:443", true, 30, "203.0.113.30", "www.nvidia.com"),
	}
	// lovelive (stored default) is not among the candidates → forced (failover).
	if view := probe(); view["realityDest"] != "www.tesla.com:443" {
		t.Fatalf("first pick %v", view["realityDest"])
	}
	// 3 ms faster: held at 5, switches at 2.
	a.results[1] = probeRow("www.nvidia.com:443", true, 9, "203.0.113.30", "www.nvidia.com")
	if view := probe(); view["realityDest"] != "www.tesla.com:443" {
		t.Fatalf("held at default 5: %v", view["realityDest"])
	}
	smSet(t, e.r, 5, 2, 20)
	if rule := e.h.landingServerView(mustServer(t, e))["realityTrialRule"].(map[string]any); rule["switchMarginMs"] != 2 {
		t.Fatalf("rule view margin %v", rule["switchMarginMs"])
	}
	if view := probe(); view["realityDest"] != "www.nvidia.com:443" {
		t.Fatalf("2 ms setting kept tesla: %v", view["realityDest"])
	}
	// Margin 0 + tie: stays.
	smSet(t, e.r, 5, 0, 20)
	a.results[0] = probeRow("www.tesla.com:443", true, 9, "203.0.113.12", "www.tesla.com")
	if view := probe(); view["realityDest"] != "www.nvidia.com:443" {
		t.Fatalf("tie at margin 0 switched: %v", view["realityDest"])
	}
	c := e.r.AutoSwitchSummaries(time.Now())[repo.AutoSwitchRealityDomain]
	if c.Total != 2 || c.Failover != 1 {
		t.Fatalf("domain counts %+v, want 2 switches (1 failover)", c)
	}
}

// Reality 目标 IP: the margin goes to agents with landing_reality ≥ 3 only
// when it is not 20 (and joins the revision); pin moves are counted.
func TestSwitchMarginRealityIPPayloadAndCounts(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	inner := e.h.nodeCommandSender
	var mu sync.Mutex
	var last map[string]interface{}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, d time.Duration) (ws.CommandResult, error) {
		if method == "ApplyLandingDesired" {
			mu.Lock()
			last = data.(map[string]interface{})
			mu.Unlock()
		}
		return inner(id, method, data, d)
	}
	sync := func() (map[string]interface{}, int64) {
		t.Helper()
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return last, last["revision"].(int64)
	}
	smSet(t, e.r, 5, 5, 35)
	// landing_reality 1 agent: never gets it.
	if p, _ := sync(); p["reality_pin_margin_ms"] != nil {
		t.Fatalf("old agent got the margin: %v", p["reality_pin_margin_ms"])
	}
	e.exec("UPDATE node SET landing_reality=3 WHERE id=2")
	p, rev := sync()
	if p["reality_pin_margin_ms"] != 35 {
		t.Fatalf("capable agent margin %v", p["reality_pin_margin_ms"])
	}
	if p2, rev2 := sync(); rev2 != rev || p2["reality_pin_margin_ms"] != 35 {
		t.Fatalf("unchanged margin bumped revision %d → %d", rev, rev2)
	}
	smSet(t, e.r, 5, 5, 20) // back to the default: field gone, new revision
	if p3, rev3 := sync(); p3["reality_pin_margin_ms"] != nil || rev3 <= rev {
		t.Fatalf("default 20: field %v rev %d (was %d)", p3["reality_pin_margin_ms"], rev3, rev)
	}

	// Pin moves: first pin no move; same IP re-checked no move; new IP = 1 (failover).
	pin := func(ip, reason string, at int64) {
		a.mu.Lock()
		a.pins = []map[string]any{{"host": "www.lovelive-anime.jp", "port": 443, "pinned_ip": ip, "pinned_ms": 4, "best_ip": ip, "best_ms": 4, "checked_at": at, "move_reason": reason}}
		a.mu.Unlock()
		sync()
	}
	pin("65.9.180.116", "", 1)
	pin("65.9.180.116", "", 2)
	pin("65.9.180.20", "failover", 3)
	pin("65.9.180.21", "faster", 4)
	c := e.r.AutoSwitchSummaries(time.Now())[repo.AutoSwitchRealityIP]
	if c.Total != 2 || c.Failover != 1 {
		t.Fatalf("ip counts %+v, want 2 moves (1 failover)", c)
	}
}

func TestSwitchMarginsAPI(t *testing.T) {
	e := newFlowTestEnv(t)
	if out := smCall(t, e.h, "/api/v1/config/switch-margins", map[string]any{}, 1); out.Code != 403 {
		t.Fatalf("non-admin get: %+v", out)
	}
	if out := smCall(t, e.h, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 1, "realityDomainMs": 1, "realityIpMs": 1}, 1); out.Code != 403 {
		t.Fatalf("non-admin save: %+v", out)
	}
	view := dataMap(t, smCall(t, e.h, "/api/v1/config/switch-margins", map[string]any{}, 0))
	rows := view["rows"].([]any)
	want := map[string]float64{"best": 5, "realityDomain": 5, "realityIp": 20}
	if len(rows) != 3 {
		t.Fatalf("rows %v", rows)
	}
	for _, r := range rows {
		row := r.(map[string]any)
		if row["valueMs"] != want[row["key"].(string)] || row["defaultMs"] != want[row["key"].(string)] {
			t.Fatalf("default row %v", row)
		}
	}
	for _, bad := range []map[string]any{
		{"bestMs": 201, "realityDomainMs": 5, "realityIpMs": 20},
		{"bestMs": -1, "realityDomainMs": 5, "realityIpMs": 20},
		{"bestMs": 5, "realityDomainMs": 5},
	} {
		if out := smCall(t, e.h, "/api/v1/config/switch-margins/save", bad, 0); out.Code == 0 {
			t.Fatalf("accepted %v", bad)
		}
	}
	if m := e.r.SwitchMargins(); m != repo.DefaultSwitchMargins() {
		t.Fatalf("invalid save changed margins: %+v", m)
	}
	out := smCall(t, e.h, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 7, "realityDomainMs": 0, "realityIpMs": 35}, 0)
	if out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	if m := e.r.SwitchMargins(); m.BestMs != 7 || m.RealityDomainMs != 0 || m.RealityIPMs != 35 || e.h.bestSwitchMargin() != 7 {
		t.Fatalf("saved margins %+v", m)
	}

	// Busy hint: one landing server with ≥ 7 non-failover domain switches.
	for i := 0; i < 7; i++ {
		e.r.NoteAutoSwitch(repo.AutoSwitchEvent{Kind: repo.AutoSwitchRealityDomain, Unit: 9})
	}
	e.r.NoteAutoSwitch(repo.AutoSwitchEvent{Kind: repo.AutoSwitchRealityDomain, Unit: 9, Failover: true})
	if err := e.r.DB().Create(&model.LandingServer{ID: 9, NodeID: 0, Source: "native", Name: "Bestvm US", PublicHost: "203.0.113.9", PortMin: 31000, PortMax: 31010, Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	view = dataMap(t, smCall(t, e.h, "/api/v1/config/switch-margins", map[string]any{}, 0))
	for _, r := range view["rows"].([]any) {
		row := r.(map[string]any)
		if row["key"] != "realityDomain" {
			continue
		}
		c := row["counts"].(map[string]any)
		if c["total"] != float64(8) || c["failover"] != float64(1) || c["busyUnit"] != float64(9) || c["busyCount"] != float64(7) || row["busyName"] != "Bestvm US" {
			t.Fatalf("busy row %v", row)
		}
	}
	if s := view["ipSupport"].(map[string]any); s["nativeOld"] == nil {
		t.Fatalf("ipSupport %v", s)
	}
}

// The generic config endpoint validates the margin keys, invalidates the
// cache, refuses to write the counts, and the config list hides them.
func TestSwitchMarginGenericConfigKeys(t *testing.T) {
	if _, err := normalizeAndValidateConfigValue(repo.SwitchMarginBestKey, "201"); err == nil {
		t.Fatal("201 accepted")
	}
	if v, err := normalizeAndValidateConfigValue(repo.SwitchMarginRealityIPKey, " 30 "); err != nil || v != "30" {
		t.Fatalf("30 → %q %v", v, err)
	}
	if _, err := normalizeAndValidateConfigValue(repo.AutoSwitchCountsKey, "{}"); err == nil {
		t.Fatal("counts writable through config/update")
	}
	e := newFlowTestEnv(t)
	e.h = New(e.r, "test-jwt-secret", "3.0.16")
	_ = e.r.SwitchMargins() // warm the cache
	mux := http.NewServeMux()
	e.h.Register(mux)
	raw, _ := json.Marshal(map[string]string{"name": repo.SwitchMarginBestKey, "value": "9"})
	req := httptest.NewRequest("POST", "/api/v1/config/update-single", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if e.r.SwitchMargins().BestMs != 9 {
		t.Fatalf("update-single not seen: %s", w.Body.String())
	}
	e.r.NoteAutoSwitch(repo.AutoSwitchEvent{Kind: repo.AutoSwitchBest, Unit: 1})
	if err := e.r.FlushAutoSwitches(); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("POST", "/api/v1/config/list", bytes.NewReader([]byte(`{}`)))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if _, ok := out.Data[repo.AutoSwitchCountsKey]; ok || out.Data[repo.SwitchMarginBestKey] != "9" {
		t.Fatalf("config list: counts shown or margin missing: %v", out.Data)
	}
}

// Plan 237: saving a changed 伪装域名 margin re-runs the 自动 pick at once on
// the stored 测速 table (no 测速 sent to the machine, no revision bump):
// it switches when the new margin allows, stays when not, the same value
// does nothing, a raised margin never switches back, and a failover-only
// change (current blocked since the 测速) waits for the next 测速 as before.
func TestSwitchMarginRealityDomainSaveAppliesAtOnce(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	probe := func() {
		t.Helper()
		if out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0); out.Code != 0 {
			t.Fatalf("probe: %+v", out)
		}
	}
	save := func(domain int) {
		t.Helper()
		if out := smCall(t, e.h, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 5, "realityDomainMs": domain, "realityIpMs": 20}, 0); out.Code != 0 {
			t.Fatalf("save %d: %+v", domain, out)
		}
	}
	a.results = []map[string]any{
		probeRow("www.tesla.com:443", true, 12, "203.0.113.12", "www.tesla.com"),
		probeRow("www.nvidia.com:443", true, 9, "203.0.113.30", "www.nvidia.com"),
	}
	probe() // lovelive not measured → nvidia (forced, 1 failover)
	a.results[1] = probeRow("www.nvidia.com:443", true, 30, "203.0.113.30", "www.nvidia.com")
	probe() // nvidia 30 vs tesla 12: 18 ms ≥ 5 → tesla
	a.results[1] = probeRow("www.nvidia.com:443", true, 9, "203.0.113.30", "www.nvidia.com")
	probe() // nvidia 9 vs tesla 12: 3 ms < 5 → held on tesla
	before := mustServer(t, e)
	if before.RealityDest != "www.tesla.com:443" {
		t.Fatalf("setup dest %s", before.RealityDest)
	}
	a.mu.Lock()
	probes := len(a.candidates) + len(a.resamples)
	a.mu.Unlock()
	counts := func() repo.AutoSwitchSummary {
		return e.r.AutoSwitchSummaries(time.Now())[repo.AutoSwitchRealityDomain]
	}
	c0 := counts()
	if c0.Total != 2 || c0.Failover != 1 {
		t.Fatalf("setup counts %+v", c0)
	}
	// Only the stored table changes (as if the last 测速 had measured nvidia
	// at 1 ms): a re-evaluation at 5 ms would switch now, so saving the same
	// 5 proves nothing was re-evaluated. 12 ms: the 11 ms gain does not reach it.
	stored := repo.LandingRealityProbeResults(&before)
	for i := range stored {
		if stored[i].Target == "www.nvidia.com:443" {
			stored[i].LatencyMs, stored[i].WorstMs = 1, 1
			for j := range stored[i].Samples {
				stored[i].Samples[j] = 1
			}
		}
	}
	raw, _ := json.Marshal(stored)
	if err := e.r.DB().Model(&model.LandingServer{}).Where("id = 1").Update("reality_probe", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	before = mustServer(t, e)
	save(5)
	save(12)
	if s := mustServer(t, e); s.RealityDest != "www.tesla.com:443" || counts().Total != 2 {
		t.Fatalf("same value re-evaluated or switched below the margin: %s %+v", s.RealityDest, counts())
	}
	// 11 ms: 11 ≥ 11 → nvidia at once (new nodes' default + its names), counted.
	save(11)
	s := mustServer(t, e)
	if s.RealityDest != "www.nvidia.com:443" || s.RealityServerNames != `["www.nvidia.com"]` {
		t.Fatalf("not applied at once: %s %s", s.RealityDest, s.RealityServerNames)
	}
	if c := counts(); c.Total != 3 || c.Failover != 1 {
		t.Fatalf("counts after the re-evaluation %+v", c)
	}
	if s.Revision != before.Revision || s.RealityProbedAt != before.RealityProbedAt || s.RealityProbe != before.RealityProbe {
		t.Fatalf("re-evaluation bumped the revision or rewrote the 测速 (rev %d→%d, at %d→%d)", before.Revision, s.Revision, before.RealityProbedAt, s.RealityProbedAt)
	}
	// Same 11 again, then raised to 20: stays on nvidia (the fastest).
	save(11)
	save(20)
	if s := mustServer(t, e); s.RealityDest != "www.nvidia.com:443" || counts().Total != 3 {
		t.Fatalf("raised margin switched: %s %+v", s.RealityDest, counts())
	}
	// Current blocked since the 测速: a margin change does not fail over
	// (that stays with the next 测速).
	rows, err := e.r.ListLandingRealityDomains()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rows {
		if d.Domain == "www.nvidia.com" {
			if _, err := e.r.SetLandingRealityDomainBlocked(d.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	save(1)
	if s := mustServer(t, e); s.RealityDest != "www.nvidia.com:443" || counts().Total != 3 {
		t.Fatalf("margin change failed over: %s %+v", s.RealityDest, counts())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := len(a.candidates) + len(a.resamples); n != probes {
		t.Fatalf("saving sent %d 测速 requests", n-probes)
	}
}

// The generic config endpoint takes the same path; a 手动 server is never
// touched.
func TestSwitchMarginRealityDomainGenericConfigAppliesAtOnce(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	a.results = []map[string]any{
		probeRow("www.tesla.com:443", true, 12, "203.0.113.12", "www.tesla.com"),
		probeRow("www.nvidia.com:443", true, 30, "203.0.113.30", "www.nvidia.com"),
	}
	probe := func() {
		t.Helper()
		if out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0); out.Code != 0 {
			t.Fatalf("probe: %+v", out)
		}
	}
	probe()
	a.results[1] = probeRow("www.nvidia.com:443", true, 9, "203.0.113.30", "www.nvidia.com")
	probe() // held on tesla (3 ms < 5)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/config/update-single", e.h.updateSingleConfig)
	set := func(v string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"name": repo.SwitchMarginRealityDomainKey, "value": v})
		req := httptest.NewRequest("POST", "/api/v1/config/update-single", bytes.NewReader(raw))
		req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if !bytes.Contains(w.Body.Bytes(), []byte(`"code":0`)) {
			t.Fatalf("update-single %s: %s", v, w.Body.String())
		}
	}
	e.exec("UPDATE landing_server SET reality_mode='manual' WHERE id=1")
	set("2")
	if s := mustServer(t, e); s.RealityDest != "www.tesla.com:443" {
		t.Fatalf("手动 server re-picked: %s", s.RealityDest)
	}
	e.exec("UPDATE landing_server SET reality_mode='auto' WHERE id=1")
	set("1")
	if s := mustServer(t, e); s.RealityDest != "www.nvidia.com:443" {
		t.Fatalf("generic config change not applied: %s", s.RealityDest)
	}
}
