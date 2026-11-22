package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 290: no real DNS in handler tests — every lookup fails (an unknown
// verdict never changes anything) unless a test installs answers.
func init() {
	realityCFLookup = func(context.Context, string) ([]string, error) { return nil, errors.New("no dns in tests") }
	realityCFLookupGap = 0
}

// fakeRealityDNS installs lookup answers (host → IPs; absent = failure) and
// clears the plan-290 caches; restored after the test.
func fakeRealityDNS(t *testing.T, answers map[string][]string) *sync.Mutex {
	t.Helper()
	var mu sync.Mutex
	resetRealityCFState()
	old := realityCFLookup
	realityCFLookup = func(_ context.Context, host string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if ips, ok := answers[host]; ok {
			return ips, nil
		}
		return nil, errors.New("nxdomain")
	}
	t.Cleanup(func() {
		realityCFLookup = old
		resetRealityCFState()
	})
	return &mu
}

func resetRealityCFState() {
	realityCFCacheMu.Lock()
	realityCFCache = map[string]realityCFCacheEntry{}
	realityCFCacheMu.Unlock()
	realityCFVerdicts.Range(func(k, _ any) bool { realityCFVerdicts.Delete(k); return true })
	realityCFStateMu.Lock()
	realityCFLastRun, realityCFDue = time.Time{}, false
	realityCFStateMu.Unlock()
}

func domainView(t *testing.T, view map[string]any, domain string) map[string]any {
	t.Helper()
	for _, d := range view["domains"].([]any) {
		if m := d.(map[string]any); m["domain"] == domain {
			return m
		}
	}
	t.Fatalf("no domain %s", domain)
	return nil
}

// TestRealityCFDomainPool: an added domain on Cloudflare is blocked at once
// with the reason, never probed, cannot be unblocked; a non-CF one is
// untouched; the 6 h check lifts it when it leaves Cloudflare (the admin's
// own block stays) and marks the built-in www.cloudflare.com too.
func TestRealityCFDomainPool(t *testing.T) {
	e := newFlowTestEnv(t)
	mu := fakeRealityDNS(t, nil)
	answers := map[string][]string{
		"cf-lab.example.org":  {"104.21.3.4", "2606:4700:3030::6815:304"},
		"cdn-lab.example.org": {"23.45.67.89"},
		"www.cloudflare.com":  {"104.16.123.96"},
		"www.tesla.com":       {"23.1.2.3"},
	}
	realityCFLookup = func(_ context.Context, host string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if ips, ok := answers[host]; ok {
			return ips, nil
		}
		return nil, errors.New("nxdomain")
	}
	add := func(d string) map[string]any {
		t.Helper()
		out := egressCall(t, e.h, "/api/v1/landing/reality/domain/add", map[string]any{"domain": d}, 0)
		if out.Code != 0 {
			t.Fatalf("add %s: %+v", d, out)
		}
		return dataMap(t, out)
	}
	view := add("cf-lab.example.org")
	cf := domainView(t, view, "cf-lab.example.org")
	if cf["cfHosted"] != true || cf["blocked"] != true || cf["manualBlocked"] != false || !strings.Contains(fmt.Sprint(cf["cfReason"]), "Cloudflare 托管：未认证流量可被借道到任意 CF 网站（DMIT 11-22 公告）") || !strings.Contains(fmt.Sprint(cf["cfEvidence"]), "104.21.3.4（面板解析）") {
		t.Fatalf("cf row %v", cf)
	}
	view = add("cdn-lab.example.org")
	if d := domainView(t, view, "cdn-lab.example.org"); d["cfHosted"] != false || d["blocked"] != false {
		t.Fatalf("non-CF row %v", d)
	}
	if view["probeCount"] != float64(11) { // 10 usable built-ins + cdn-lab
		t.Fatalf("probeCount %v", view["probeCount"])
	}
	targets, _ := e.r.RealityProbeTargets()
	if joined := strings.Join(targets, ","); strings.Contains(joined, "cf-lab") || !strings.Contains(joined, "cdn-lab.example.org:443") {
		t.Fatalf("targets %s", joined)
	}
	id := int64(cf["id"].(float64))
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/block", map[string]any{"id": id, "blocked": false}, 0); out.Code == 0 || !strings.Contains(out.Msg, "Cloudflare") {
		t.Fatalf("CF unblock accepted: %+v", out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/block", map[string]any{"id": id, "blocked": true}, 0); out.Code != 0 {
		t.Fatalf("manual block: %+v", out)
	}
	// Periodic check (first run = fresh lookups): built-in www.cloudflare.com
	// gets the mark next to its name block; the rest unchanged.
	e.h.realityCFCheckIfDue(context.Background())
	if d := cfRowH(t, e, "www.cloudflare.com"); !d.CFHosted || !d.Blocked {
		t.Fatalf("www.cloudflare.com %+v", d)
	}
	if d := cfRowH(t, e, "www.tesla.com"); d.CFHosted || d.Blocked {
		t.Fatalf("tesla %+v", d)
	}
	// Not due again for 6 h (no lookups).
	calls := 0
	realityCFLookup = func(context.Context, string) ([]string, error) { calls++; return nil, errors.New("x") }
	e.h.realityCFCheckIfDue(context.Background())
	if calls != 0 {
		t.Fatalf("check not paced: %d lookups", calls)
	}
	// A failing lookup keeps the mark.
	e.h.realityCFCheckAll(context.Background(), true)
	if d := cfRowH(t, e, "cf-lab.example.org"); !d.CFHosted {
		t.Fatal("lookup failure lifted the block")
	}
	// Leaves Cloudflare → lifted; the admin's block stays. (Failures are
	// cached for 30 min; start from an empty cache.)
	realityCFCacheMu.Lock()
	realityCFCache = map[string]realityCFCacheEntry{}
	realityCFCacheMu.Unlock()
	answers["cf-lab.example.org"] = []string{"23.9.9.9"}
	realityCFLookup = func(_ context.Context, host string) ([]string, error) {
		if ips, ok := answers[host]; ok {
			return ips, nil
		}
		return nil, errors.New("nxdomain")
	}
	e.h.realityCFCheckAll(context.Background(), true)
	if d := cfRowH(t, e, "cf-lab.example.org"); d.CFHosted || !d.Blocked {
		t.Fatalf("after leaving CF %+v", d)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/block", map[string]any{"id": id, "blocked": false}, 0); out.Code != 0 {
		t.Fatalf("unblock after lift: %+v", out)
	}
}

func cfRowH(t *testing.T, e *flowTestEnv, domain string) model.LandingRealityDomain {
	t.Helper()
	var d model.LandingRealityDomain
	if err := e.r.DB().Where("domain = ?", domain).First(&d).Error; err != nil {
		t.Fatalf("row %s: %v", domain, err)
	}
	return d
}

// TestRealityCFProbeAndManualPick: a candidate the landing resolves to a
// Cloudflare address is never picked (marked, shown as such in the table),
// even when fastest; a manual pick of a Cloudflare dest is refused.
func TestRealityCFProbeAndManualPick(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	fakeRealityDNS(t, map[string][]string{"cf-manual.example.org": {"172.67.10.10"}})
	a := newRealityAgent(t, e)
	a.results = []map[string]any{
		probeRow("www.tesla.com:443", true, 3, "104.18.20.20", "www.tesla.com"),
		probeRow("www.nvidia.com:443", true, 30, "203.0.113.30", "www.nvidia.com"),
	}
	out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	view := dataMap(t, out)
	if out.Code != 0 || view["realityDest"] != "www.nvidia.com:443" {
		t.Fatalf("picked %v (%+v)", view["realityDest"], out)
	}
	if row := heldRow(t, view, "www.tesla.com"); row["cfHosted"] != true || row["blocked"] != true {
		t.Fatalf("table row %v", row)
	}
	if !cfRowH(t, e, "www.tesla.com").CFHosted {
		t.Fatal("row not marked")
	}
	// Existing nodes untouched.
	var n model.LandingNode
	if err := e.r.DB().First(&n, 1).Error; err != nil || n.Dest != "www.lovelive-anime.jp:443" {
		t.Fatalf("node 1 dest %q", n.Dest)
	}
	body := serverSaveBody(t, e, 0, false)
	body["realityMode"] = "manual"
	body["realityDest"] = "cf-manual.example.org:443"
	body["realityServerNames"] = []string{"cf-manual.example.org"}
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code == 0 || !strings.Contains(out.Msg, "Cloudflare") {
		t.Fatalf("CF manual pick accepted: %+v", out)
	}
	body["realityDest"] = "www.tesla.com:443"
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code == 0 || !strings.Contains(out.Msg, "Cloudflare") {
		t.Fatalf("marked manual pick accepted: %+v", out)
	}
	body["realityDest"] = "www.nvidia.com:443"
	body["realityServerNames"] = []string{"www.nvidia.com"}
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatalf("non-CF manual pick: %+v", out)
	}
}

func riskOf(t *testing.T, e *flowTestEnv) map[string]any {
	t.Helper()
	out := egressCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 0)
	if out.Code != 0 {
		t.Fatalf("list %+v", out)
	}
	raw, _ := json.Marshal(out.Data)
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	for _, m := range list {
		if m["id"] == float64(1) {
			r, _ := m["realityRisk"].(map[string]any)
			return r
		}
	}
	t.Fatal("server 1 missing")
	return nil
}

// TestRealityCFServerRisk: the admin ⚠ data — Reality listeners without
// 防盗 in effect and dests on Cloudflare; users never get it.
func TestRealityCFServerRisk(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	fakeRealityDNS(t, map[string][]string{"www.example.com": {"104.16.9.9"}, "www.lovelive-anime.jp": {"13.32.1.1"}})
	// 3 Reality nodes + 1 Reality chain copy; guard on but not reported yet.
	risk := riskOf(t, e)
	if risk["listeners"] != float64(4) || risk["guardState"] != "idle" || risk["guardExposed"] != true || len(risk["cfDests"].([]any)) != 0 {
		t.Fatalf("idle risk %v", risk)
	}
	e.exec("UPDATE landing_server SET guard_state='on', guard_nodes=4 WHERE id=1")
	if risk := riskOf(t, e); risk["guardExposed"] != false || risk["guardState"] != "on" {
		t.Fatalf("on risk %v", risk)
	}
	e.exec("UPDATE landing_server SET reality_guard_off=? WHERE id=1", true)
	if risk := riskOf(t, e); risk["guardExposed"] != true || risk["guardState"] != "off" {
		t.Fatalf("off risk %v", risk)
	}
	e.exec("UPDATE landing_server SET reality_guard_off=? WHERE id=1", false)
	// A live node's dest (not a candidate) on Cloudflare, seen by the check.
	e.h.realityCFCheckAll(context.Background(), true)
	risk = riskOf(t, e)
	dests := risk["cfDests"].([]any)
	if len(dests) != 1 {
		t.Fatalf("cf dests %v", dests)
	}
	if d := dests[0].(map[string]any); d["host"] != "www.example.com" || d["nodes"] != float64(1) || d["default"] != false || !strings.Contains(fmt.Sprint(d["evidence"]), "104.16.9.9") {
		t.Fatalf("cf dest %v", d)
	}
	// The server default dest on Cloudflare (pins of this machine).
	e.exec("UPDATE landing_server SET reality_pins=? WHERE id=1", `[{"host":"www.lovelive-anime.jp","port":443,"pinned_ip":"104.17.1.1","checked_at":1}]`)
	risk = riskOf(t, e)
	if dests := risk["cfDests"].([]any); len(dests) != 2 || dests[0].(map[string]any)["host"] != "www.lovelive-anime.jp" || dests[0].(map[string]any)["default"] != true || dests[0].(map[string]any)["nodes"] != float64(3) {
		t.Fatalf("default cf %v", risk["cfDests"])
	}
	// Shared server whose Helper has no guard: exposed.
	s := mustServer(t, e)
	s.Source = "shared"
	if v := e.h.landingRealityRiskView(s); v["guardState"] != "shared" || v["guardExposed"] != true {
		t.Fatalf("shared risk %v", v)
	}
	// No Reality listeners → nothing exposed.
	e.exec("UPDATE landing_node SET status='disabled' WHERE protocol='vless_reality_vision'")
	e.exec("UPDATE forward_chain_node SET state='pending'")
	e.exec("UPDATE landing_server SET guard_state='', reality_pins='' WHERE id=1")
	if risk := riskOf(t, e); risk["listeners"] != float64(0) || risk["guardExposed"] != false {
		t.Fatalf("no listeners %v", risk)
	}
	// Users never get the block.
	user := e.h.landingServerUserView(mustServer(t, e), landingEgressCatalog{}, &repo.LandingUserNodeCounts{})
	if _, ok := user["realityRisk"]; ok {
		t.Fatal("user view has realityRisk")
	}
}
