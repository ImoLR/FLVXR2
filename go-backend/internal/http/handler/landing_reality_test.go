package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// realityAgent answers ApplyLandingDesired (with pins) and RealityProbe.
type realityAgent struct {
	mu         sync.Mutex
	candidates [][]string // pass-1 requests
	resamples  [][]string // re-sample requests (concurrency 1)
	results    []map[string]any
	// later maps a target to its re-sample latencies (pass 2, pass 3; 0 =
	// not usable); absent = same as pass 1.
	later   map[string][]int
	stats   map[string]any
	pins    []map[string]any
	applies int
}

func newRealityAgent(t *testing.T, e *flowTestEnv) *realityAgent {
	a := &realityAgent{}
	realityResampleGap = 0
	t.Cleanup(func() { realityResampleGap = 5 * time.Second })
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch method {
		case "RealityProbe":
			req := data.(map[string]any)
			cands := req["candidates"].([]string)
			out := map[string]any{"results": a.results}
			if a.stats != nil {
				out["stats"] = a.stats
			}
			if req["concurrency"] != 1 {
				a.candidates = append(a.candidates, cands)
				return ws.CommandResult{Success: true, Data: out}, nil
			}
			pass := len(a.resamples) % 2
			a.resamples = append(a.resamples, cands)
			var rows []map[string]any
			for _, c := range cands {
				for _, r := range a.results {
					if r["target"] != c {
						continue
					}
					row := map[string]any{}
					for k, v := range r {
						row[k] = v
					}
					if seq, ok := a.later[c]; ok {
						row["latency_ms"] = seq[pass]
						row["feasible"] = seq[pass] > 0
					}
					rows = append(rows, row)
				}
			}
			out["results"] = rows
			return ws.CommandResult{Success: true, Data: out}, nil
		case "ApplyLandingDesired":
			a.applies++
			payload := data.(map[string]interface{})
			out := map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "applied", "ready": true}}
			if a.pins != nil {
				out["reality_pins"] = a.pins
			}
			return ws.CommandResult{Success: true, Data: out}, nil
		}
		t.Fatalf("unexpected command %s", method)
		return ws.CommandResult{}, nil
	}
	return a
}

func probeRow(target string, feasible bool, ms int, bestIP string, sans ...string) map[string]any {
	return map[string]any{"target": target, "feasible": feasible, "latency_ms": ms, "best_ip": bestIP, "ips": []map[string]any{{"ip": bestIP, "tcp_ms": 1}},
		"tls13": feasible, "x25519": feasible, "h2": feasible, "cert_valid": feasible, "server_names": sans}
}

func realitySeed(t *testing.T, e *flowTestEnv) {
	guardSeed(t, e)
	e.exec("UPDATE node SET landing_reality=1, status=1 WHERE id=2")
}

// TestLandingRealityProbeAutoPick: 测速 runs on the machine with the
// non-blocked candidates; 自动（最快） makes the fastest usable non-blocked
// domain the default for NEW nodes; existing nodes keep their dest; a blocked
// winner is never picked; 手动 keeps the admin's pick; admin only.
func TestLandingRealityProbeAutoPick(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	a.results = []map[string]any{
		probeRow("www.amd.com:443", false, 0, "203.0.113.40"),
		probeRow("www.nvidia.com:443", true, 30, "203.0.113.30", "www.nvidia.com", "nvidia.com", "*.nvidia.com", "developer.nvidia.com", "evil.example.com"),
		probeRow("www.tesla.com:443", true, 12, "203.0.113.12", "www.tesla.com", "tesla.com", "*.tesla.com", "other.com"),
		probeRow("www.microsoft.com:443", true, 3, "203.0.113.3", "www.microsoft.com"), // blocked by default; an old/foreign result
	}
	if out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 1); out.Code != 403 {
		t.Fatalf("non-admin probe: %+v", out)
	}
	out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	if out.Code != 0 {
		t.Fatalf("probe: %+v", out)
	}
	if len(a.candidates) != 1 || len(a.resamples) != 2 || strings.Join(a.resamples[0], ",") != "www.tesla.com:443,www.nvidia.com:443" {
		t.Fatalf("probes = %v re-samples = %v", a.candidates, a.resamples)
	}
	sent := strings.Join(a.candidates[0], ",")
	for _, want := range []string{"www.lovelive-anime.jp:443", "www.tesla.com:443", "addons.mozilla.org:443"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("candidate %s missing: %s", want, sent)
		}
	}
	for _, blocked := range []string{"microsoft", "apple", "icloud", "amazon", "cloudflare", "google", "bing"} {
		if strings.Contains(sent, blocked) {
			t.Fatalf("blocked candidate probed: %s", sent)
		}
	}
	view := dataMap(t, out)
	if view["realityPicked"] != "www.tesla.com:443" || view["realityDest"] != "www.tesla.com:443" {
		t.Fatalf("picked = %v / %v", view["realityPicked"], view["realityDest"])
	}
	if names := fmt.Sprint(view["realityServerNames"]); names != "[www.tesla.com tesla.com]" {
		t.Fatalf("server names = %s", names)
	}
	if rows := view["realityProbe"].([]any); len(rows) != 4 {
		t.Fatalf("table rows = %d", len(rows))
	}
	// Existing nodes keep their dest / serverNames.
	for id, dest := range map[int64]string{1: "www.lovelive-anime.jp:443", 2: "www.lovelive-anime.jp:443", 3: "www.example.com:443"} {
		var n model.LandingNode
		if err := e.r.DB().First(&n, id).Error; err != nil || n.Dest != dest {
			t.Fatalf("node %d dest = %q (%v)", id, n.Dest, err)
		}
	}
	// A new node takes the picked dest.
	created, err := e.r.CreateLandingNode(1, 1, "rp-new", "vless_reality_vision", true)
	if err != nil {
		t.Fatal(err)
	}
	if created.Dest != "www.tesla.com:443" || created.ServerNames != `["www.tesla.com","tesla.com"]` {
		t.Fatalf("new node dest = %s %s", created.Dest, created.ServerNames)
	}
	// Block the winner: the next 测速 picks the next fastest, never the blocked one.
	var tesla model.LandingRealityDomain
	if err := e.r.DB().Where("domain = ?", "www.tesla.com").First(&tesla).Error; err != nil {
		t.Fatal(err)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/block", map[string]any{"id": tesla.ID, "blocked": true}, 0); out.Code != 0 {
		t.Fatalf("block: %+v", out)
	}
	out = egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	if view := dataMap(t, out); out.Code != 0 || view["realityDest"] != "www.nvidia.com:443" || fmt.Sprint(view["realityServerNames"]) != "[www.nvidia.com nvidia.com developer.nvidia.com]" {
		t.Fatalf("after block: %+v", view["realityDest"])
	}
	if strings.Contains(strings.Join(a.candidates[1], ","), "www.tesla.com") {
		t.Fatal("blocked domain probed")
	}
	// 手动: the admin's pick stays; a blocked manual pick is refused.
	e.exec("UPDATE landing_server SET reality_mode='manual', reality_dest='www.amd.com:443' WHERE id=1")
	out = egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	if view := dataMap(t, out); out.Code != 0 || view["realityDest"] != "www.amd.com:443" || view["realityMode"] != "manual" {
		t.Fatalf("manual: %v %v", view["realityDest"], view["realityMode"])
	}
	body := serverSaveBody(t, e, 0, false)
	body["realityMode"] = "manual"
	body["realityDest"] = "www.tesla.com:443"
	body["realityServerNames"] = []string{"www.tesla.com"}
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code == 0 || !strings.Contains(out.Msg, "屏蔽") {
		t.Fatalf("blocked manual pick accepted: %+v", out)
	}
	// 自动 ignores a stale form dest.
	body["realityMode"] = "auto"
	body["realityDest"] = "www.typed.example:443"
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatalf("auto save: %+v", out)
	}
	if s, _ := e.r.GetLandingServer(1); s.RealityDest != "www.amd.com:443" || s.RealityMode != "auto" || s.RealityProbedAt == 0 {
		t.Fatalf("auto save kept %s %s %d", s.RealityDest, s.RealityMode, s.RealityProbedAt)
	}
}

// TestLandingRealityPinsFromApply: pins reported with the apply are stored
// and shown to admins only; an agent without the capability stores nothing.
func TestLandingRealityPinsFromApply(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	a.pins = []map[string]any{{"host": "www.lovelive-anime.jp", "port": 443, "pinned_ip": "65.9.180.116", "pinned_ms": 4, "best_ip": "65.9.180.116", "best_ms": 4, "checked_at": 1}}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	s, _ := e.r.GetLandingServer(1)
	pins := repo.LandingRealityPins(s)
	if len(pins) != 1 || pins[0].PinnedIP != "65.9.180.116" || s.RealityPinsAt == 0 {
		t.Fatalf("pins = %+v", pins)
	}
	view := e.h.landingServerView(*s)
	if view["realityPinSupported"] != true || len(view["realityPins"].([]repo.RealityPin)) != 1 {
		t.Fatalf("admin view pins = %v", view["realityPins"])
	}
	user := e.h.landingServerUserView(*s, landingEgressCatalog{}, &repo.LandingUserNodeCounts{})
	for _, k := range []string{"realityPins", "realityProbe", "realityProbeError", "realityProbedAt"} {
		if _, ok := user[k]; ok {
			t.Fatalf("user view has %s", k)
		}
	}
	// No guarded dest any more: the omitted list clears the stored pins.
	a.pins = nil
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if s, _ = e.r.GetLandingServer(1); len(repo.LandingRealityPins(s)) != 0 {
		t.Fatalf("pins kept: %s", s.RealityPins)
	}
	// Older agent: unsupported, nothing recorded.
	e.exec("UPDATE node SET landing_reality=0 WHERE id=2")
	e.exec("UPDATE landing_server SET reality_pins='' WHERE id=1")
	a.pins = []map[string]any{{"host": "x.example.com", "checked_at": 1}}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	s, _ = e.r.GetLandingServer(1)
	if s.RealityPins != "" {
		t.Fatalf("old agent pins stored: %s", s.RealityPins)
	}
	if view := e.h.landingServerView(*s); view["realityProbeSupported"] != false || !strings.Contains(fmt.Sprint(view["realityProbeUnsupportedReason"]), "升级节点") {
		t.Fatalf("old agent view = %v %v", view["realityProbeSupported"], view["realityProbeUnsupportedReason"])
	}
}

// TestLandingRealityDomainList: seeded list, custom add/delete, block.
func TestLandingRealityDomainList(t *testing.T) {
	e := newFlowTestEnv(t)
	out := egressCall(t, e.h, "/api/v1/landing/reality/domains", map[string]any{}, 0)
	if out.Code != 0 {
		t.Fatal(out)
	}
	view := dataMap(t, out)
	domains := view["domains"].([]any)
	blocked, usable := 0, 0
	for _, d := range domains {
		m := d.(map[string]any)
		if m["source"] != "builtin" {
			t.Fatalf("unexpected seed %v", m)
		}
		if m["blocked"] == true {
			blocked++
			if m["note"] == "" {
				t.Fatalf("blocked without reason: %v", m)
			}
		} else {
			usable++
		}
	}
	if usable != 10 || blocked != 8 || view["probeCount"] != float64(10) {
		t.Fatalf("usable=%d blocked=%d probe=%v", usable, blocked, view["probeCount"])
	}
	if first := domains[0].(map[string]any); first["domain"] != "www.lovelive-anime.jp" {
		t.Fatalf("first = %v", first)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domains", map[string]any{}, 1); out.Code != 403 {
		t.Fatalf("non-admin list: %+v", out)
	}
	add := func(d string) (int, string) {
		out := egressCall(t, e.h, "/api/v1/landing/reality/domain/add", map[string]any{"domain": d}, 0)
		return out.Code, out.Msg
	}
	if code, _ := add("https://WWW.Example.org/path?x"); code != 0 {
		t.Fatal("custom add failed")
	}
	for _, bad := range []string{"www.example.org", "1.2.3.4", "example", "www.example.org:8443", "a b.com"} {
		if code, _ := add(bad); code == 0 {
			t.Fatalf("%q accepted", bad)
		}
	}
	rows, _ := e.r.ListLandingRealityDomains()
	var custom, builtin model.LandingRealityDomain
	for _, r := range rows {
		if r.Domain == "www.example.org" {
			custom = r
		}
		if r.Domain == "www.tesla.com" {
			builtin = r
		}
	}
	if custom.Source != "custom" || rows[18].Domain != "www.example.org" {
		t.Fatalf("custom row %+v at %s", custom, rows[18].Domain)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/delete", map[string]any{"id": builtin.ID}, 0); out.Code == 0 {
		t.Fatal("built-in deleted")
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/delete", map[string]any{"id": custom.ID}, 0); out.Code != 0 {
		t.Fatalf("custom delete: %+v", out)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/reality/domain/block", map[string]any{"id": builtin.ID, "blocked": true}, 0); out.Code != 0 {
		t.Fatal(out)
	}
	targets, _ := e.r.RealityProbeTargets()
	if len(targets) != 9 || strings.Contains(strings.Join(targets, ","), "tesla") {
		t.Fatalf("targets = %v", targets)
	}
}

// TestLandingRealityShared: without the share capability 测速 is refused
// with the upgrade hint; with it the probe goes to Custom, pins ride on the
// status poll, and the pool joins the list (risky kinds blocked).
func TestLandingRealityShared(t *testing.T) {
	e := newFlowTestEnv(t)
	var mu sync.Mutex
	var probeBodies []string
	polls := 0
	realitySharedPollEvery = 10 * time.Millisecond
	t.Cleanup(func() { realitySharedPollEvery = 4 * time.Second })
	realityResampleGap = 0
	t.Cleanup(func() { realityResampleGap = 5 * time.Second })
	custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case landingcustom.Prefix + "/shares/7/reality-probe":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b, _ := json.Marshal(body["targets"])
			probeBodies = append(probeBodies, string(b))
			fmt.Fprint(w, `{"probe_id":"p-1","state":"queued","results":[]}`)
		case landingcustom.Prefix + "/shares/7/reality-probe/p-1":
			polls++
			if polls < 2 {
				fmt.Fprint(w, `{"probe_id":"p-1","state":"running","results":[]}`)
				return
			}
			// Custom reports float milliseconds.
			fmt.Fprint(w, `{"probe_id":"p-1","state":"done","results":[`+
				`{"target":"www.sony.com:443","feasible":true,"latency_ms":8.4,"best_ip":"203.0.113.9","ips":[{"ip":"203.0.113.9","tcp_ms":1.2},{"ip":"203.0.113.99","tcp_ms":-1}],"tls13":true,"x25519":true,"h2":true,"cert_valid":true,"server_names":["www.sony.com","sony.com"]},`+
				`{"target":"www.dell.com:443","feasible":true,"latency_ms":20.0,"best_ip":"203.0.113.20","ips":[],"tls13":true,"x25519":true,"h2":true,"cert_valid":true,"server_names":[]}]}`)
		case landingcustom.Prefix + "/shares/7/status":
			fmt.Fprint(w, `{"applied_revision":0,"core":{"state":"running"},"nodes":[],"collected_at":1,"reality_pins":[{"host":"www.lovelive-anime.jp","pinned_ip":"65.9.180.116","pinned_ms":4.6,"best_ip":"65.9.180.116","best_ms":4.6,"checked_at":5,"nodes":["132"]}]}`)
		case landingcustom.Prefix + "/reality-pool":
			fmt.Fprint(w, `{"domains":[{"domain":"www.apple.com"},{"domain":"swdist.apple.com"},{"domain":"www.tesla.com"},{"domain":"www.example.net"},"live.bilibili.com"],"fetched_at":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer custom.Close()
	now := time.Now().UnixMilli()
	e.exec("INSERT INTO landing_integration(id, base_url, token, last_sync_at, sync_error, updated_time) VALUES(1, ?, 'flvx_fixture', 0, '', ?)", custom.URL, now)
	caps := map[string]any{"server_id": 7, "name": "s", "status": "active", "protocols": []string{"ss2022", "vless_reality_vision"}}
	capsJSON, _ := json.Marshal(caps)
	if err := e.r.DB().Create(&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "shared", PublicHost: "203.0.113.70", PortMin: 30000, PortMax: 30999, FlowType: 2, TrafficRatio: 1, Status: "active",
		Protocols: `["ss2022","vless_reality_vision"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, SharedCaps: string(capsJSON)}).Error; err != nil {
		t.Fatal(err)
	}
	out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 5}, 0)
	if out.Code == 0 || out.Msg != realitySharedUnsupported {
		t.Fatalf("old share: %+v", out)
	}
	s, _ := e.r.GetLandingServer(5)
	if v := e.h.landingServerView(*s); v["realityProbeUnsupportedReason"] != realitySharedUnsupported || v["realityPinSupported"] != false {
		t.Fatalf("old share view = %v", v["realityProbeUnsupportedReason"])
	}
	caps["reality_probe"] = map[string]any{"versions": []int{1}}
	caps["reality_pin"] = map[string]any{"versions": []int{1}}
	capsJSON, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=5", string(capsJSON))
	out = egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 5}, 0)
	if view := dataMap(t, out); out.Code != 0 || view["realityDest"] != "www.sony.com:443" || fmt.Sprint(view["realityServerNames"]) != "[www.sony.com sony.com]" {
		t.Fatalf("shared probe: %+v", out)
	}
	// Pass 1 = every candidate; 2 re-sample jobs = the usable subset (plan 229).
	if len(probeBodies) != 3 || strings.Contains(probeBodies[0], "microsoft") || !strings.Contains(probeBodies[0], "www.sony.com:443") || polls != 4 ||
		probeBodies[1] != `["www.sony.com:443","www.dell.com:443"]` || probeBodies[2] != probeBodies[1] {
		t.Fatalf("custom probe bodies = %v polls=%d", probeBodies, polls)
	}
	s, _ = e.r.GetLandingServer(5)
	if res := repo.LandingRealityProbeResults(s); len(res) != 2 || res[0].LatencyMs != 9 || res[0].IPs[1].TCPMs != -1 || fmt.Sprint(res[0].Samples) != "[9 9 9]" || res[0].WorstMs != 9 {
		t.Fatalf("stored shared results = %+v", res)
	}
	if st := repo.LandingRealityHistory(s).Stats; st == nil || st.Passes != 3 || st.Resampled != 2 || st.Measured || st.Linger0 {
		t.Fatalf("shared stats = %+v", st)
	}
	// Pins ride on the status poll (float ms).
	client, _ := landingcustom.New(custom.URL, "flvx_fixture")
	if _, err := e.h.pollSharedLanding(context.Background(), client, *s); err != nil {
		t.Fatal(err)
	}
	s, _ = e.r.GetLandingServer(5)
	if pins := repo.LandingRealityPins(s); len(pins) != 1 || pins[0].PinnedIP != "65.9.180.116" || pins[0].PinnedMs != 5 {
		t.Fatalf("shared pins = %+v", pins)
	}
	// Pool: risky kinds blocked on arrival, built-ins flagged in_pool.
	if out := egressCall(t, e.h, "/api/v1/landing/reality/pool/refresh", map[string]any{}, 0); out.Code != 0 {
		t.Fatalf("pool: %+v", out)
	}
	rows, _ := e.r.ListLandingRealityDomains()
	got := map[string]model.LandingRealityDomain{}
	for _, r := range rows {
		got[r.Domain] = r
	}
	if !got["www.tesla.com"].InPool || got["www.tesla.com"].Source != "builtin" {
		t.Fatalf("tesla = %+v", got["www.tesla.com"])
	}
	if d := got["www.example.net"]; d.Source != "pool" || d.Blocked || !d.InPool {
		t.Fatalf("example.net = %+v", d)
	}
	for _, risky := range []string{"swdist.apple.com", "live.bilibili.com"} {
		if d := got[risky]; !d.Blocked || d.Note == "" {
			t.Fatalf("%s = %+v", risky, d)
		}
	}
	if d := got["www.apple.com"]; d.Source != "builtin" || !d.Blocked || !d.InPool {
		t.Fatalf("apple = %+v", d)
	}
	// Pool shrinks: unblocked pool rows go, blocked ones keep their block.
	if _, err := e.r.ReplaceLandingRealityPool([]string{"www.tesla.com"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = e.r.ListLandingRealityDomains()
	got = map[string]model.LandingRealityDomain{}
	for _, r := range rows {
		got[r.Domain] = r
	}
	if _, ok := got["www.example.net"]; ok {
		t.Fatal("left pool row kept")
	}
	if d, ok := got["swdist.apple.com"]; !ok || !d.Blocked || d.InPool {
		t.Fatalf("blocked pool row = %+v %v", d, ok)
	}
	if got["www.apple.com"].InPool {
		t.Fatal("apple still in pool")
	}
}

func TestRealityServerNamesFor(t *testing.T) {
	cases := map[string][]string{
		"www.lovelive-anime.jp|lovelive-anime.jp,www.lovelive-anime.jp":              {"www.lovelive-anime.jp", "lovelive-anime.jp"},
		"www.amazon.co.jp|amazon.co.jp,*.amazon.co.jp,www.amazon.com,m.amazon.co.jp": {"www.amazon.co.jp", "amazon.co.jp", "m.amazon.co.jp"},
		"addons.mozilla.org|addons.mozilla.org":                                      {"addons.mozilla.org"},
		"www.x.com|a.x.com,b.x.com,c.x.com,d.x.com,e.x.com,f.x.com":                  {"www.x.com", "a.x.com", "b.x.com", "c.x.com", "d.x.com"},
		"www.y.com|": {"www.y.com"},
	}
	for in, want := range cases {
		parts := strings.SplitN(in, "|", 2)
		var sans []string
		if parts[1] != "" {
			sans = strings.Split(parts[1], ",")
		}
		if got := repo.RealityServerNamesFor(parts[0], sans); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: %v want %v", parts[0], got, want)
		}
	}
}

// TestLandingRealityResampleWorstAndTrial (plan 229): the 5 fastest usable
// domains + ≤ 2 试用 contenders get 2 more samples one domain at a time; the
// pick is the lowest WORST sample; a 妙妙屋池 small site is picked only after
// its history is stable (≥ 3 samples over ≥ 6 h, jitter within the limit).
func TestLandingRealityResampleWorstAndTrial(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	if _, err := e.r.ReplaceLandingRealityPool([]string{"portal.citygrainla.com", "www.ebay.com", "download.gfe.nvidia.com"}); err != nil {
		t.Fatal(err)
	}
	a.results = []map[string]any{
		probeRow("portal.citygrainla.com:443", true, 2, "203.0.113.2", "portal.citygrainla.com"),
		probeRow("www.ebay.com:443", true, 3, "203.0.113.3", "www.ebay.com"),
		probeRow("www.tesla.com:443", true, 5, "203.0.113.5", "www.tesla.com"),
		probeRow("download.gfe.nvidia.com:443", true, 8, "203.0.113.8", "download.gfe.nvidia.com"),
		probeRow("www.sony.com:443", true, 9, "203.0.113.9", "www.sony.com"),
		probeRow("www.dell.com:443", true, 10, "203.0.113.10", "www.dell.com"),
		probeRow("www.intel.com:443", true, 11, "203.0.113.11", "www.intel.com"),
		probeRow("www.samsung.com:443", true, 12, "203.0.113.12", "www.samsung.com"),
		probeRow("www.amd.com:443", false, 0, "203.0.113.13"),
	}
	a.later = map[string][]int{
		"portal.citygrainla.com:443":  {40, 3}, // lucky first sample, really jittery
		"www.ebay.com:443":            {3, 4},
		"www.tesla.com:443":           {30, 31}, // fast once, slow twice
		"download.gfe.nvidia.com:443": {8, 9},
		"www.sony.com:443":            {9, 9},
		"www.dell.com:443":            {10, 0}, // fails a re-sample → not usable
	}
	a.stats = map[string]any{"sockets": 20, "max_open": 2, "duration_ms": 900, "linger0": true}
	out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	if out.Code != 0 {
		t.Fatalf("probe: %+v", out)
	}
	want := "www.tesla.com:443,download.gfe.nvidia.com:443,www.sony.com:443,www.dell.com:443,www.intel.com:443,portal.citygrainla.com:443,www.ebay.com:443"
	if len(a.resamples) != 2 || strings.Join(a.resamples[0], ",") != want || strings.Join(a.resamples[1], ",") != want {
		t.Fatalf("re-samples = %v", a.resamples)
	}
	view := dataMap(t, out)
	// nvidia worst 9 (median 8) beats sony worst 9 (median 9); tesla worst 31.
	if view["realityDest"] != "download.gfe.nvidia.com:443" {
		t.Fatalf("picked %v", view["realityDest"])
	}
	rows := map[string]map[string]any{}
	var order []string
	for _, r := range view["realityProbe"].([]any) {
		m := r.(map[string]any)
		rows[m["domain"].(string)] = m
		order = append(order, m["domain"].(string))
	}
	if order[0] != "www.ebay.com" || order[1] != "download.gfe.nvidia.com" || order[2] != "www.sony.com" {
		t.Fatalf("order = %v", order)
	}
	if c := rows["portal.citygrainla.com"]; c["trial"] != true || c["trialOk"] == true || c["trialReason"] != "抖动 38 ms" || fmt.Sprint(c["samples"]) != "[2 40 3]" || c["worstMs"] != float64(40) {
		t.Fatalf("citygrain = %v", c)
	}
	if c := rows["www.ebay.com"]; c["trial"] != true || c["trialOk"] == true || !strings.Contains(c["trialReason"].(string), "6 小时") {
		t.Fatalf("ebay = %v", c)
	}
	if c := rows["download.gfe.nvidia.com"]; c["trial"] == true || c["medianMs"] != float64(8) {
		t.Fatalf("nvidia pool row = %v", c)
	}
	if c := rows["www.dell.com"]; c["feasible"] != false || !strings.HasPrefix(c["reason"].(string), "复测不可用") {
		t.Fatalf("dell = %v", c)
	}
	if c := rows["www.samsung.com"]; fmt.Sprint(c["samples"]) != "[12]" {
		t.Fatalf("samsung = %v", c)
	}
	st := view["realityProbeStats"].(map[string]any)
	if st["passes"] != float64(3) || st["resampled"] != float64(7) || st["sockets"] != float64(60) || st["maxOpen"] != float64(2) || st["linger0"] != true || st["measured"] != true {
		t.Fatalf("stats = %v", st)
	}
	// The domain list marks 试用 rows.
	dom := dataMap(t, egressCall(t, e.h, "/api/v1/landing/reality/domains", map[string]any{}, 0))
	trial := map[string]bool{}
	for _, d := range dom["domains"].([]any) {
		m := d.(map[string]any)
		trial[m["domain"].(string)] = m["trial"] == true
	}
	if !trial["portal.citygrainla.com"] || !trial["www.ebay.com"] || trial["download.gfe.nvidia.com"] || trial["www.tesla.com"] {
		t.Fatalf("trial flags = %v", trial)
	}

	// 7 h later ebay is still 3–4 ms: stable over ≥ 6 h → it may be picked.
	s, _ := e.r.GetLandingServer(1)
	hist := repo.LandingRealityHistory(s)
	for d, list := range hist.D {
		for i := range list {
			list[i].At -= 7 * 3600
		}
		hist.D[d] = list
	}
	raw, _ := json.Marshal(hist)
	e.exec("UPDATE landing_server SET reality_samples=? WHERE id=1", string(raw))
	a.later["www.ebay.com:443"] = []int{4, 3}
	out = egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	if view := dataMap(t, out); out.Code != 0 || view["realityDest"] != "www.ebay.com:443" {
		t.Fatalf("stable small site not picked: %v", view["realityDest"])
	}
	s, _ = e.r.GetLandingServer(1)
	hist = repo.LandingRealityHistory(s)
	if n := len(hist.D["www.ebay.com"]); n != 6 {
		t.Fatalf("ebay history = %d samples", n)
	}
	// citygrain stays manual-only: its history still has the 38 ms jitter.
	for _, r := range repo.LandingRealityProbeResults(s) {
		if r.Target == "portal.citygrainla.com:443" && (r.TrialOK || r.TrialReason == "") {
			t.Fatalf("citygrain = %+v", r)
		}
	}
	// Bounded: ≤ 8 samples per domain after many 测速.
	for i := 0; i < 4; i++ {
		if out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0); out.Code != 0 {
			t.Fatalf("probe %d: %+v", i, out)
		}
	}
	s, _ = e.r.GetLandingServer(1)
	for d, list := range repo.LandingRealityHistory(s).D {
		if len(list) > repo.RealitySampleKeep {
			t.Fatalf("%s: %d samples", d, len(list))
		}
	}
	if len(s.RealitySamples) > 4096 {
		t.Fatalf("history %d bytes", len(s.RealitySamples))
	}
	// A server save keeps the history.
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, 0, false), 0); out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	if s2, _ := e.r.GetLandingServer(1); s2.RealitySamples != s.RealitySamples {
		t.Fatal("history lost on save")
	}
}

// TestLandingRealityOldAgentResampleFails: an agent without stats whose
// re-sample pass fails still records pass 1 and picks the fastest single sample.
func TestLandingRealityOldAgentResampleFails(t *testing.T) {
	e := newFlowTestEnv(t)
	realitySeed(t, e)
	a := newRealityAgent(t, e)
	a.results = []map[string]any{probeRow("www.sony.com:443", true, 9, "203.0.113.9", "www.sony.com"), probeRow("www.dell.com:443", true, 7, "203.0.113.7", "www.dell.com")}
	inner := e.h.nodeCommandSender
	calls := 0
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, d time.Duration) (ws.CommandResult, error) {
		if method == "RealityProbe" {
			calls++
			if calls > 1 {
				return ws.CommandResult{Success: false, Message: "节点离线"}, nil
			}
		}
		return inner(id, method, data, d)
	}
	out := egressCall(t, e.h, "/api/v1/landing/server/reality-probe", map[string]any{"id": 1}, 0)
	view := dataMap(t, out)
	if out.Code != 0 || view["realityDest"] != "www.dell.com:443" || calls != 2 {
		t.Fatalf("picked %v calls=%d", view["realityDest"], calls)
	}
	st := view["realityProbeStats"].(map[string]any)
	if st["passes"] != float64(2) || st["resampled"] != float64(0) || st["measured"] != false {
		t.Fatalf("stats = %v", st)
	}
}

func TestRealityTrialDomainAndVerdict(t *testing.T) {
	for d, want := range map[string]bool{
		"portal.citygrainla.com": true, "www.airbnb.com": true, "ftp.debian.org": true, "www.amazon": true,
		"download.gfe.nvidia.com": false, "download-installer.cdn.mozilla.net": false, "www.sony.co.jp": false, "amd.com": false,
		"learn.microsoft.com": false, "live.bilibili.com": false,
	} {
		if got := repo.RealityTrialDomain(model.LandingRealityDomain{Domain: d, Source: "pool"}); got != want {
			t.Errorf("%s trial = %v", d, got)
		}
	}
	if repo.RealityTrialDomain(model.LandingRealityDomain{Domain: "portal.citygrainla.com", Source: "custom"}) {
		t.Error("custom domain is 试用")
	}
	h := int64(3600)
	s := func(kv ...int64) []repo.RealitySample {
		var out []repo.RealitySample
		for i := 0; i < len(kv); i += 2 {
			out = append(out, repo.RealitySample{At: kv[i], Ms: int(kv[i+1])})
		}
		return out
	}
	for _, c := range []struct {
		hist   []repo.RealitySample
		best   int
		ok     bool
		reason string
	}{
		{s(0, 5, 0, 6, 7*h, 5), 6, true, ""},
		{s(0, 5, 0, 6), 6, false, "还要再测 1 次"},
		{s(0, 5, 0, 6, 0, 5), 6, false, "要隔 6 小时以上再测一次"},
		{s(0, 5, 0, 40, 7*h, 5), 6, false, "抖动 35 ms"},
		{s(0, 5, 0, -1, 7*h, 5), 6, false, "最近 3 次里有 1 次不可用"},
		{s(0, 20, 0, 21, 7*h, 20), 6, false, "比非试用里最快的慢 14 ms"},
		{s(0, 150, 0, 180, 7*h, 160), 0, true, ""}, // far machine: 30 % of 160 = 48 ms allowed
	} {
		ok, reason := repo.RealityTrialVerdict(c.hist, c.best)
		if ok != c.ok || reason != c.reason {
			t.Errorf("%v best=%d → %v %q, want %v %q", c.hist, c.best, ok, reason, c.ok, c.reason)
		}
	}
}
