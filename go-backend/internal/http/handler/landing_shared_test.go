package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestSharedLandingCadenceAndRevisionRetry(t *testing.T) {
	e := newFlowTestEnv(t)
	pushes, polls, lists, hosts := 0, 0, 0, 0
	down := false
	var last landingcustom.Desired
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if down {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case landingcustom.Prefix + "/shares":
			lists++
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{{ID: 7, Name: "shared", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}, Core: landingcustom.Core{State: "running"}}}})
		case landingcustom.Prefix + "/landing-hosts":
			hosts++
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			pushes++
			json.NewDecoder(r.Body).Decode(&last)
			json.NewEncoder(w).Encode(map[string]any{"accepted_revision": last.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			polls++
			json.NewEncoder(w).Encode(landingcustom.Status{Core: landingcustom.Core{State: "running"}, CollectedAt: time.Now().UnixMilli()})
		default:
			t.Errorf("unexpected Custom path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	e.h.syncSharedIntegration(context.Background(), false)
	if pushes != 1 || polls != 1 || hosts != 1 || lists != 1 {
		t.Fatalf("initial cadence %d %d %d %d", pushes, polls, hosts, lists)
	}
	original := last.Revision
	for i := 0; i < 4; i++ {
		e.h.syncSharedIntegration(context.Background(), false)
	}
	if pushes != 1 || polls != 1 || hosts != 1 || lists != 1 {
		t.Fatal("unchanged state polled/pushed too often")
	}
	e.r.DB().Model(&model.LandingServer{}).Where("source='shared'").Update("shared_last_sync_at", time.Now().Add(-61*time.Second).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if polls != 2 || pushes != 1 {
		t.Fatal("60 second poll")
	}
	e.r.DB().Model(&model.LandingServer{}).Where("source='shared'").Update("shared_last_push_at", time.Now().Add(-11*time.Minute).UnixMilli())
	e.h.syncSharedIntegration(context.Background(), false)
	if pushes != 2 || last.Revision != original {
		t.Fatal("10 minute identical full re-push changed revision")
	}
	down = true
	e.h.sharedLanding.lastList = time.Time{}
	e.h.syncSharedIntegration(context.Background(), false)
	retry := e.h.sharedLanding.offline
	if retry.failures != 1 || time.Until(retry.next) > 15*time.Second {
		t.Fatal("first retry backoff")
	}
	e.h.syncSharedIntegration(context.Background(), false)
	if e.h.sharedLanding.offline.failures != 1 {
		t.Fatal("backoff did not suppress network retry")
	}
	if sharedBackoff(20) != 10*time.Minute {
		t.Fatal("unbounded retry")
	}
	down = false
	e.h.syncSharedIntegration(context.Background(), true)
	if e.h.sharedLanding.offline.failures != 0 {
		t.Fatal("recovery did not reset backoff")
	}
}

func TestSharedLandingRuleAutoCreation(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	share := landingcustom.Share{ID: 7, Name: "shared-rule", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022", "vless_reality_vision"}}
	if err := e.r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	servers, _ := e.r.ListLandingServers(0, true)
	// Plan 110: an admin creating for user 2 needs user 2 to have access.
	if _, _, _, err := e.h.resolveNativeLandingTarget(map[string]any{"landingServerId": servers[0].ID, "landingProtocol": "vless_reality_vision", "name": "rule landing"}, 2, 1, "", true); err == nil || !strings.Contains(err.Error(), "该用户无权使用此落地服务器") {
		t.Fatalf("ungranted owner auto-creation: %v", err)
	}
	grant := []int64{servers[0].ID}
	if err := e.r.SaveLandingUserSettingWithGrants(2, 5, nil, 0, &grant); err != nil {
		t.Fatal(err)
	}
	target, id, created, err := e.h.resolveNativeLandingTarget(map[string]any{"landingServerId": servers[0].ID, "landingProtocol": "vless_reality_vision", "name": "rule landing"}, 2, 1, "", true)
	if err != nil || id == 0 || created != id || target != "203.0.113.7:30000" {
		t.Fatalf("rule target %s %d %d %v", target, id, created, err)
	}
	n, _ := e.r.GetLandingNode(id)
	if n.Protocol != "vless_reality_vision" {
		t.Fatal("auto-created wrong protocol")
	}
	target2, id2, created2, err := e.h.resolveNativeLandingTarget(map[string]any{"landingNodeId": id}, 2, 1, "", true)
	if err != nil || target2 != target || id2 != id || created2 != 0 {
		t.Fatal("reuse own shared node")
	}
}

func sharedLandingTestRepo(t *testing.T, dialect string) *repo.Repository {
	t.Helper()
	if dialect == "sqlite" {
		return newFlowTestEnv(t).r
	}
	dsn := os.Getenv("FLVX_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run PostgreSQL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("landing_shared_handler_%d", time.Now().UnixNano())
	if _, err = admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	r, err := repo.OpenPostgres(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestSharedLandingBackgroundDeletion(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := New(r, "shared-background", "test")
			share := landingcustom.Share{ID: 7, Name: "shared", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30000, Protocols: []string{"ss2022"}}
			status := landingcustom.Status{CollectedAt: time.Now().UnixMilli(), Core: landingcustom.Core{State: "running"}}
			var desired landingcustom.Desired
			pushes := 0
			// holdApply: accepting desired never updates the published Helper status.
			custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case landingcustom.Prefix + "/shares":
					json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{share}})
				case landingcustom.Prefix + "/landing-hosts":
					w.Write([]byte("{}"))
				case landingcustom.Prefix + "/shares/7/desired":
					pushes++
					json.NewDecoder(req.Body).Decode(&desired)
					json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": desired.Revision})
				case landingcustom.Prefix + "/shares/7/status":
					json.NewEncoder(w).Encode(status)
				default:
					t.Errorf("unexpected path %s", req.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer custom.Close()
			if err := r.SaveLandingIntegration(custom.URL, "flvx_fixture"); err != nil {
				t.Fatal(err)
			}
			h.syncSharedIntegration(context.Background(), false)
			servers, _ := r.ListLandingServers(0, true)
			sid := servers[0].ID
			n, err := r.CreateLandingNode(1, sid, "async", "ss2022", true)
			if err != nil {
				t.Fatal(err)
			}
			if err = h.syncSharedLandingServer(sid); err != nil {
				t.Fatal(err)
			}
			status.AppliedRevision = desired.Revision
			status.Nodes = []landingcustom.NodeStatus{{ID: strconv.FormatInt(n.ID, 10), State: "active", Connections: 1}}
			status.CollectedAt++
			deleteNode := func() {
				t.Helper()
				w := httptest.NewRecorder()
				h.deleteSharedLandingNode(w, httptest.NewRequest("POST", "/", nil), n)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 || out.Data.(map[string]any)["pending"] != true {
					t.Fatalf("pending delete: %s", w.Body.String())
				}
			}
			deleteNode()
			revision := desired.Revision
			pushCount := pushes
			// Retrying is optional, idempotent, and cannot postpone the Helper ACK.
			deleteNode()
			if desired.Revision != revision || pushes != pushCount {
				t.Fatal("retry advanced/pushed revision")
			}
			if _, err := r.CreateLandingNode(1, sid, "reserved", "ss2022", true); err == nil {
				t.Fatal("pending deletion released capacity/port")
			}
			tick := func() {
				t.Helper()
				if err := r.DB().Model(&model.LandingServer{}).Where("id = ?", sid).Update("shared_last_sync_at", 0).Error; err != nil {
					t.Fatal(err)
				}
				h.syncSharedIntegration(context.Background(), false)
			}
			retained := func() {
				t.Helper()
				current, _ := r.GetLandingNode(n.ID)
				if current == nil || !current.DeletePending || current.Port != n.Port {
					t.Fatal("unconfirmed deletion lost identity/port")
				}
			}
			status.Nodes[0].State, status.Nodes[0].Connections = "removed", 0
			status.Nodes[0].UpBytes, status.Nodes[0].DownBytes = 10, 20
			status.CollectedAt++
			tick() // removed but applied revision is still old
			retained()
			status.AppliedRevision = revision
			status.Nodes[0].Connections = 1
			status.CollectedAt++
			tick() // applied, but a connection remains
			retained()
			status.Nodes[0].State, status.Nodes[0].Connections = "active", 0
			status.CollectedAt++
			tick() // no connections, but not removed
			retained()
			status.Nodes[0].State = "removed"
			status.Nodes[0].UpBytes, status.Nodes[0].DownBytes = 15, 25
			status.CollectedAt++
			tick() // one background pass books final counters and releases the node
			if current, _ := r.GetLandingNode(n.ID); current != nil {
				t.Fatal("background did not finalize confirmed deletion")
			}
			tick() // replay final snapshot
			u, _ := r.GetUserByID(1)
			s, _ := r.GetLandingServer(sid)
			if u.InFlow+u.OutFlow != 80 || s.Revision != revision || desired.Revision != revision || pushes != pushCount {
				t.Fatalf("billing/revision changed: user=%d revision=%d desired=%d pushes=%d", u.InFlow+u.OutFlow, s.Revision, desired.Revision, pushes)
			}
			if replacement, err := r.CreateLandingNode(1, sid, "released", "ss2022", true); err != nil || replacement.Port != n.Port {
				t.Fatal("confirmed deletion did not release port/capacity", err)
			}
		})
	}
}

func TestSharedLandingHostReportDNSFailureDoesNotBackoff(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := New(r, "shared-hosts", "test")
			for _, row := range []any{
				&model.Node{ID: 601, Name: "dead-domain", ServerIP: "Lost.INVALID"},
				&model.Node{ID: 602, Name: "resolvable", ServerIP: "LOCALHOST"},
				&model.Node{ID: 603, Name: "literal", ServerIP: "203.0.113.99"},
				&model.LandingServer{ID: 601, NodeID: 601, Source: "native", PublicHost: "Lost.INVALID", Status: "disabled"},
			} {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			shares := []landingcustom.Share{}
			for _, id := range []int64{7, 8} {
				shares = append(shares, landingcustom.Share{ID: id, Name: "shared", Hosts: landingcustom.Hosts{IPv4: fmt.Sprintf("203.0.113.%d", id)}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}})
			}
			var report landingcustom.LandingHosts
			hosts := 0
			polls, pushes := map[string]int{}, map[string]int{}
			custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case req.URL.Path == landingcustom.Prefix+"/shares":
					json.NewEncoder(w).Encode(map[string]any{"shares": shares})
				case req.URL.Path == landingcustom.Prefix+"/landing-hosts":
					if req.Method != http.MethodPut {
						t.Error("host report must be PUT")
					}
					hosts++
					json.NewDecoder(req.Body).Decode(&report)
					w.Write([]byte("{}"))
				case strings.HasSuffix(req.URL.Path, "/status"):
					polls[req.URL.Path]++
					json.NewEncoder(w).Encode(landingcustom.Status{CollectedAt: time.Now().UnixMilli(), Core: landingcustom.Core{State: "running"}})
				case strings.HasSuffix(req.URL.Path, "/desired"):
					pushes[req.URL.Path]++
					var desired landingcustom.Desired
					json.NewDecoder(req.Body).Decode(&desired)
					json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": desired.Revision})
				default:
					t.Errorf("unexpected path %s", req.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer custom.Close()
			if err := r.SaveLandingIntegration(custom.URL, "flvx_fixture"); err != nil {
				t.Fatal(err)
			}
			h.syncSharedIntegration(context.Background(), false)
			if hosts != 1 || !slices.Contains(report.Nodes, "lost.invalid") || !slices.Contains(report.Native, "lost.invalid") || !slices.Contains(report.Nodes, "localhost") || !slices.Contains(report.Nodes, "127.0.0.1") || !slices.Contains(report.Nodes, "203.0.113.99") {
				t.Fatalf("DNS failure blocked/incompletely reported hosts: %+v", report)
			}
			for _, id := range []int{7, 8} {
				prefix := fmt.Sprintf("%s/shares/%d", landingcustom.Prefix, id)
				if polls[prefix+"/status"] != 1 || pushes[prefix+"/desired"] != 1 {
					t.Fatal("DNS failure blocked same-tick shared status/push")
				}
			}
			st := h.sharedState()
			if len(st.hostAddresses["localhost"]) == 0 {
				t.Fatal("successful resolution was not cached")
			}
			// Model addresses learned during a previous successful resolution.
			st.hostAddresses["lost.invalid"] = []string{"203.0.113.60", "2001:db8::60"}
			st.lastHosts = time.Time{}
			h.syncSharedIntegration(context.Background(), false)
			if hosts != 2 || !slices.Contains(report.Nodes, "203.0.113.60") || !slices.Contains(report.Native, "2001:db8::60") || !slices.Contains(report.Nodes, "lost.invalid") {
				t.Fatal("DNS failure dropped previously resolved addresses", report)
			}
			cfg, _ := r.GetLandingIntegration()
			if st.offline.failures != 0 || !st.offline.next.IsZero() || len(st.retries) != 0 || cfg.SyncError != "" {
				t.Fatal("DNS failure became a transport failure or persistent sync error")
			}
			// Native enablement must ignore the best-effort report cache.
			if err := h.checkSharedNativeConflict(context.Background(), 601); err == nil {
				t.Fatal("unresolvable own native address was accepted")
			}
			shares[0].Hosts.Domain = "unresolvable-share.invalid"
			if err := h.checkSharedNativeConflict(context.Background(), 603); err == nil {
				t.Fatal("unresolvable share address was accepted")
			}
		})
	}
}
