package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/repo"
)

// guardCustom is a fake Custom for plan 129: the share advertises the guard
// when guard is set, records every desired body raw, and serves status.
type guardCustom struct {
	mu      sync.Mutex
	share   landingcustom.Share
	status  map[string]any
	bodies  []string
	desired landingcustom.Desired
}

func (g *guardCustom) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case landingcustom.Prefix + "/shares":
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{g.share}})
		case landingcustom.Prefix + "/landing-hosts":
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			raw, _ := io.ReadAll(req.Body)
			g.bodies = append(g.bodies, string(raw))
			g.desired = landingcustom.Desired{}
			_ = json.Unmarshal(raw, &g.desired)
			json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": g.desired.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			json.NewEncoder(w).Encode(g.status)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func (g *guardCustom) last(t *testing.T) (string, landingcustom.Desired) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.bodies) == 0 {
		t.Fatal("no desired pushed")
	}
	return g.bodies[len(g.bodies)-1], g.desired
}

// TestSharedLandingRealityGuard (plan 129, landing-reality-guard-v1): the
// switch is sent only to shares that advertise the guard (older Custom keeps
// today's payload and revision), the Helper's report drives the admin view,
// a malformed report never fails the poll, and the admin switch saves for
// supported shares only.
func TestSharedLandingRealityGuard(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := New(r, "shared-guard", "test")
			g := &guardCustom{share: landingcustom.Share{ID: 7, Name: "shared", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30005,
				Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}}}
			g.status = map[string]any{"applied_revision": 0, "core": map[string]any{"state": "running"}, "nodes": []any{}, "share_traffic_used_bytes": 0, "collected_at": time.Now().UnixMilli()}
			custom := g.serve(t)
			defer custom.Close()
			if err := r.SaveLandingIntegration(custom.URL, "flvx_fixture"); err != nil {
				t.Fatal(err)
			}
			h.syncSharedIntegration(context.Background(), true)
			servers, _ := r.ListLandingServers(0, true)
			if len(servers) != 1 {
				t.Fatalf("servers %d", len(servers))
			}
			sid := servers[0].ID
			if _, err := r.CreateLandingNode(1, sid, "reality", "vless_reality_vision", true); err != nil {
				t.Fatal(err)
			}
			if err := h.syncSharedLandingServer(sid); err != nil {
				t.Fatal(err)
			}
			raw, d := g.last(t)
			if strings.Contains(raw, "reality_guard") || d.RealityGuard != nil {
				t.Fatalf("older Custom got the guard field: %s", raw)
			}
			oldRevision := d.Revision
			view := func() map[string]any {
				t.Helper()
				s, _ := r.GetLandingServer(sid)
				return landingGuardView(*s)
			}
			if v := view(); v["state"] != "shared" || v["enabled"] != true {
				t.Fatalf("unsupported share view %v", v)
			}
			// An admin switch for an unsupported share is ignored.
			if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", sharedGuardSaveBody(t, r, sid, false)); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			if s, _ := r.GetLandingServer(sid); s.RealityGuardOff {
				t.Fatal("switch saved for a share without the guard")
			}
			raw, d = g.last(t)
			if strings.Contains(raw, "reality_guard") {
				t.Fatalf("older Custom got the guard field after a save: %s", raw)
			}
			oldRevision = d.Revision

			// Custom v1.4.75 / Helper v0.7.13 advertise the guard: one revision
			// bump, reality_guard true (default on), byte-identical nodes.
			g.mu.Lock()
			g.share.RealityGuard = &landingcustom.ShareRealityGuard{Versions: []int{1, 1, 0, 2000}}
			g.mu.Unlock()
			h.syncSharedIntegration(context.Background(), true)
			raw2, d2 := g.last(t)
			if d2.RealityGuard == nil || !*d2.RealityGuard || d2.Revision != oldRevision+1 || !strings.Contains(raw2, `"reality_guard":true`) {
				t.Fatalf("guard desired %s (old revision %d)", raw2, oldRevision)
			}
			n1, _ := json.Marshal(d.Nodes)
			n2, _ := json.Marshal(d2.Nodes)
			if !bytes.Equal(n1, n2) {
				t.Fatalf("nodes changed:\n%s\n%s", n1, n2)
			}
			if s, _ := r.GetLandingServer(sid); !strings.Contains(s.SharedCaps, `"reality_guard":{"versions":[1]}`) {
				t.Fatalf("caps not sanitized/stored: %s", s.SharedCaps)
			}
			// Unchanged content keeps the revision.
			h.syncSharedIntegration(context.Background(), true)
			if _, d3 := g.last(t); d3.Revision != d2.Revision {
				t.Fatalf("revision moved without a change: %d → %d", d2.Revision, d3.Revision)
			}
			if v := view(); v["state"] != "pending" {
				t.Fatalf("not applied yet: %v", v)
			}

			report := func(guard any) {
				t.Helper()
				g.mu.Lock()
				g.status["applied_revision"] = g.desired.Revision
				g.status["collected_at"] = time.Now().UnixMilli()
				if guard == nil {
					delete(g.status, "reality_guard")
				} else {
					g.status["reality_guard"] = guard
				}
				g.mu.Unlock()
				h.syncSharedIntegration(context.Background(), true)
			}
			report(map[string]any{"state": "on", "nodes": 1})
			if v := view(); v["state"] != "on" || v["nodes"] != 1 || v["enabled"] != true {
				t.Fatalf("on view %v", v)
			}
			report(map[string]any{"state": "restart_required", "nodes": 0})
			if v := view(); v["state"] != "restart" {
				t.Fatalf("restart view %v", v)
			}
			report(map[string]any{"state": "error", "nodes": 0})
			if v := view(); v["state"] != "error" {
				t.Fatalf("error view %v", v)
			}
			// Malformed reports are dropped, the poll and billing go on.
			for _, bad := range []any{"on", map[string]any{"state": "exploded", "nodes": 1}, map[string]any{"state": "on", "nodes": -1}, map[string]any{"state": 5}} {
				report(bad)
				s, _ := r.GetLandingServer(sid)
				if s.GuardState != "" || s.SharedSyncError != "" {
					t.Fatalf("bad report %v stored %q / error %q", bad, s.GuardState, s.SharedSyncError)
				}
				if v := view(); v["state"] != "idle" {
					t.Fatalf("bad report %v view %v", bad, v)
				}
			}
			report(nil)
			if v := view(); v["state"] != "idle" {
				t.Fatalf("absent report view %v", v)
			}

			// Admin switches it off: reality_guard false, new revision.
			if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", sharedGuardSaveBody(t, r, sid, false)); out.Code != 0 {
				t.Fatalf("switch off: %+v", out)
			}
			raw4, d4 := g.last(t)
			if d4.RealityGuard == nil || *d4.RealityGuard || d4.Revision <= d2.Revision || !strings.Contains(raw4, `"reality_guard":false`) {
				t.Fatalf("switch off desired %s", raw4)
			}
			if v := view(); v["state"] != "off" || v["enabled"] != false {
				t.Fatalf("off view %v", v)
			}
			// A save without the field keeps it; back on works.
			body := sharedGuardSaveBody(t, r, sid, true)
			delete(body, "realityGuard")
			if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", body); out.Code != 0 {
				t.Fatal(out)
			}
			if s, _ := r.GetLandingServer(sid); !s.RealityGuardOff {
				t.Fatal("legacy save changed the switch")
			}
			if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", sharedGuardSaveBody(t, r, sid, true)); out.Code != 0 {
				t.Fatal(out)
			}
			if _, d5 := g.last(t); d5.RealityGuard == nil || !*d5.RealityGuard {
				t.Fatal("switch on not sent")
			}

			// Capability withdrawn (Helper downgraded): the field is dropped
			// again and the view falls back to the plan-127 hint.
			g.mu.Lock()
			g.share.RealityGuard = nil
			g.mu.Unlock()
			h.syncSharedIntegration(context.Background(), true)
			if raw6, _ := g.last(t); strings.Contains(raw6, "reality_guard") {
				t.Fatalf("guard sent after the capability was withdrawn: %s", raw6)
			}
			if v := view(); v["state"] != "shared" {
				t.Fatalf("withdrawn view %v", v)
			}
		})
	}
}

func sharedGuardSaveBody(t *testing.T, r *repo.Repository, sid int64, guard bool) map[string]any {
	t.Helper()
	current, _ := r.GetLandingServer(sid)
	raw, _ := json.Marshal(current)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["protocols"] = []string{"ss2022", "vless_reality_vision"}
	out["realityServerNames"] = []string{"www.lovelive-anime.jp"}
	out["userGroupIds"] = []int64{}
	out["realityGuard"] = guard
	return out
}

// TestSharedGuardLabFixtures (plan 129 lab, opt-in with FLVX_GUARD_LAB_OUT):
// writes the exact desired bodies FLVX sends to a share advertising egress +
// reality_guard (one SS2022 node, two Reality nodes in two serverName groups,
// default anti-abuse plan): guard-on.json and guard-off.json. With
// FLVX_GUARD_LAB_REPORT it also feeds the Helper's report (Custom status
// shape, from the mmwx lab) back and prints FLVX's admin guard view.
func TestSharedGuardLabFixtures(t *testing.T) {
	out := os.Getenv("FLVX_GUARD_LAB_OUT")
	if out == "" {
		t.Skip("set FLVX_GUARD_LAB_OUT")
	}
	r := newFlowTestEnv(t).r
	h := New(r, "shared-guard-lab", "test")
	g := &guardCustom{share: landingcustom.Share{ID: 7, Name: "lab", Hosts: landingcustom.Hosts{IPv4: "203.0.113.1"}, Status: "active", PortMin: 31000, PortMax: 31999,
		Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"},
		Egress:       &landingcustom.ShareEgress{Versions: []int{1}, Allowed: true},
		RealityGuard: &landingcustom.ShareRealityGuard{Versions: []int{1}}}}
	g.status = map[string]any{"applied_revision": 0, "core": map[string]any{"state": "running"}, "nodes": []any{}, "share_traffic_used_bytes": 0, "collected_at": time.Now().UnixMilli()}
	custom := g.serve(t)
	defer custom.Close()
	if err := r.SaveLandingIntegration(custom.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	h.syncSharedIntegration(context.Background(), true)
	servers, _ := r.ListLandingServers(0, true)
	sid := servers[0].ID
	setNames := func(dest, name string) {
		t.Helper()
		if err := r.DB().Exec("UPDATE landing_server SET reality_dest = ?, reality_server_names = ? WHERE id = ?", dest, `["`+name+`"]`, sid).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CreateLandingNode(1, sid, "lab-ss", "ss2022", true); err != nil {
		t.Fatal(err)
	}
	setNames("www.flvx.test:8443", "www.flvx.test")
	if _, err := r.CreateLandingNode(1, sid, "lab-reality", "vless_reality_vision", true); err != nil {
		t.Fatal(err)
	}
	setNames("www.flvx.test:8443", "alt.flvx.test")
	if _, err := r.CreateLandingNode(1, sid, "lab-reality-alt", "vless_reality_vision", true); err != nil {
		t.Fatal(err)
	}
	if err := h.syncSharedLandingServer(sid); err != nil {
		t.Fatal(err)
	}
	on, d := g.last(t)
	if d.RealityGuard == nil || !*d.RealityGuard || d.Egress == nil {
		t.Fatalf("lab desired %s", on)
	}
	if err := os.WriteFile(filepath.Join(out, "guard-on.json"), []byte(on), 0600); err != nil {
		t.Fatal(err)
	}
	if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", sharedGuardSaveBody(t, r, sid, false)); out.Code != 0 {
		t.Fatalf("switch off: %+v", out)
	}
	off, _ := g.last(t)
	if err := os.WriteFile(filepath.Join(out, "guard-off.json"), []byte(off), 0600); err != nil {
		t.Fatal(err)
	}
	report := os.Getenv("FLVX_GUARD_LAB_REPORT")
	if report == "" {
		return
	}
	// Back on, then the Helper's real report (Custom /status shape).
	if out := protocolsCall(t, h, 1, 0, "/api/v1/landing/server/save", sharedGuardSaveBody(t, r, sid, true)); out.Code != 0 {
		t.Fatal(out)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var lab map[string]any
	if err := json.Unmarshal(raw, &lab); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	for k, v := range lab {
		g.status[k] = v
	}
	g.status["applied_revision"] = g.desired.Revision
	g.status["collected_at"] = time.Now().UnixMilli()
	g.mu.Unlock()
	h.syncSharedIntegration(context.Background(), true)
	s, _ := r.GetLandingServer(sid)
	view, _ := json.Marshal(landingGuardView(*s))
	t.Logf("FLVX admin guard view from the Helper report: %s", view)
	_ = os.WriteFile(filepath.Join(out, "flvx-view.json"), view, 0600)
}
