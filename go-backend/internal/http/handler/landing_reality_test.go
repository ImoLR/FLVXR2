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
	candidates [][]string
	results    []map[string]any
	pins       []map[string]any
	applies    int
}

func newRealityAgent(t *testing.T, e *flowTestEnv) *realityAgent {
	a := &realityAgent{}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch method {
		case "RealityProbe":
			req := data.(map[string]any)
			a.candidates = append(a.candidates, req["candidates"].([]string))
			return ws.CommandResult{Success: true, Data: map[string]any{"results": a.results}}, nil
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
	if len(a.candidates) != 1 {
		t.Fatalf("probes = %d", len(a.candidates))
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
	if len(probeBodies) != 1 || strings.Contains(probeBodies[0], "microsoft") || !strings.Contains(probeBodies[0], "www.sony.com:443") || polls != 2 {
		t.Fatalf("custom probe bodies = %v polls=%d", probeBodies, polls)
	}
	s, _ = e.r.GetLandingServer(5)
	if res := repo.LandingRealityProbeResults(s); len(res) != 2 || res[0].LatencyMs != 9 || res[0].IPs[1].TCPMs != -1 {
		t.Fatalf("stored shared results = %+v", res)
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
