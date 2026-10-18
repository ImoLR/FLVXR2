package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// fakeCustom uses the implemented phase-2A envelopes and rejects unknown desired
// fields. Paused/revoked shares reject PUT, while status retains counters.
type fakeCustom struct {
	mu            sync.Mutex
	shares        []landingcustom.Share
	desired       map[int64]landingcustom.Desired
	status        map[int64]landingcustom.Status
	hosts         landingcustom.LandingHosts
	down          bool
	reject        string
	calls, pushes int
}

func (f *fakeCustom) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if r.Header.Get("Authorization") != "Bearer flvx_fixture" {
		w.WriteHeader(401)
		write(map[string]any{"error": map[string]string{"code": "unauthorized", "message": "bad token"}})
		return
	}
	if f.down {
		w.WriteHeader(503)
		write(map[string]any{"error": map[string]string{"code": "unavailable", "message": "flvx_fixture must not leak"}})
		return
	}
	p := strings.TrimPrefix(r.URL.Path, landingcustom.Prefix)
	if r.Method == "GET" && p == "/shares" {
		write(map[string]any{"shares": f.shares})
		return
	}
	if r.Method == "PUT" && p == "/landing-hosts" {
		if json.NewDecoder(r.Body).Decode(&f.hosts) != nil {
			w.WriteHeader(422)
			return
		}
		write(map[string]any{})
		return
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 {
		w.WriteHeader(404)
		return
	}
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	if parts[2] == "status" && r.Method == "GET" {
		write(f.status[id])
		return
	}
	if parts[2] != "desired" || r.Method != "PUT" {
		w.WriteHeader(405)
		return
	}
	f.pushes++
	var d landingcustom.Desired
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		w.WriteHeader(422)
		write(map[string]any{"errors": []landingcustom.ValidationError{{Field: "desired", Message: "bad shape"}}})
		return
	}
	for _, s := range f.shares {
		if s.ID == id && s.Status != "active" {
			w.WriteHeader(409)
			write(map[string]any{"error": map[string]string{"code": "share_inactive", "message": "inactive"}})
			return
		}
	}
	if f.reject != "" {
		for _, n := range d.Nodes {
			if n.ID == f.reject {
				w.WriteHeader(422)
				write(map[string]any{"errors": []landingcustom.ValidationError{{NodeID: n.ID, Field: "port", Message: "flvx_fixture " + n.SS2022.ServerKey}}})
				return
			}
		}
	}
	old := f.desired[id]
	if d.Revision < old.Revision {
		w.WriteHeader(409)
		write(map[string]any{"error": map[string]string{"code": "revision_conflict"}})
		return
	}
	f.desired[id] = d
	status := f.status[id]
	status.AppliedRevision = d.Revision
	status.CollectedAt = max(time.Now().UnixMilli(), status.CollectedAt+1)
	byID := map[string]landingcustom.NodeStatus{}
	for _, n := range status.Nodes {
		n.State = "removed"
		n.Connections = 0
		byID[n.ID] = n
	}
	for _, n := range d.Nodes {
		sn := byID[n.ID]
		sn.ID = n.ID
		sn.State = "removed"
		if n.Enabled {
			sn.State = "active"
		}
		byID[n.ID] = sn
	}
	status.Nodes = []landingcustom.NodeStatus{}
	for _, n := range byID {
		status.Nodes = append(status.Nodes, n)
	}
	f.status[id] = status
	write(map[string]int64{"accepted_revision": d.Revision})
}

type sharedContract struct {
	r                  *repo.Repository
	f                  *fakeCustom
	router             http.Handler
	admin, user, other string
	servers            []model.LandingServer
}

func newSharedContract(t *testing.T, dialect string) *sharedContract {
	t.Helper()
	f := &fakeCustom{desired: map[int64]landingcustom.Desired{}, status: map[int64]landingcustom.Status{}}
	for _, id := range []int64{7, 8} {
		f.shares = append(f.shares, landingcustom.Share{ID: id, Name: fmt.Sprintf("shared-%d", id), Hosts: landingcustom.Hosts{IPv4: fmt.Sprintf("203.0.113.%d", id)}, Status: "active", PortMin: 30000, PortMax: 30003, TrafficLimit: 10 << 30, MaxConnections: 100, ResetDay: 1, Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}})
		f.status[id] = landingcustom.Status{Core: landingcustom.Core{State: "running", Epoch: "Core-string-epoch"}, CollectedAt: time.Now().UnixMilli()}
	}
	custom := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(custom.Close)
	r := landingContractRepo(t, dialect)
	for _, v := range []any{&model.User{ID: 501, User: "shared-user", RoleID: 1, Status: 1, Flow: 100}, &model.User{ID: 502, User: "other-user", RoleID: 1, Status: 1, Flow: 100}, &model.UserGroup{ID: 701, Name: "shared-group", Status: 1}, &model.UserGroupUser{UserGroupID: 701, UserID: 501}, &model.Node{ID: 601, Name: "node-host", ServerIP: "localhost", Port: "10000-19999", LandingCapable: true}} {
		if err := r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := handler.New(r, "shared-contract", "test")
	e := &sharedContract{r: r, f: f, router: httpserver.NewRouter(h, "shared-contract", r)}
	e.admin, _ = auth.GenerateToken(1, "admin", 0, "shared-contract")
	e.user, _ = auth.GenerateToken(501, "shared-user", 1, "shared-contract")
	e.other, _ = auth.GenerateToken(502, "other-user", 1, "shared-contract")
	e.ok(t, "integration/save", e.admin, map[string]any{"baseUrl": custom.URL, "token": "flvx_fixture"})
	ss, err := r.ListLandingServers(0, true)
	if err != nil || len(ss) != 2 {
		t.Fatalf("sync %d %v", len(ss), err)
	}
	e.servers = ss
	for i := range e.servers {
		if err := r.SaveLandingServer(&e.servers[i], []int64{701}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}
func (e *sharedContract) call(t *testing.T, path, token string, body any) response.R {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/landing/"+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (e *sharedContract) ok(t *testing.T, path, token string, body any) response.R {
	t.Helper()
	out := e.call(t, path, token, body)
	if out.Code != 0 {
		t.Fatalf("%s: %s", path, out.Msg)
	}
	return out
}
func (e *sharedContract) node(t *testing.T, protocol string) int64 {
	t.Helper()
	v := e.ok(t, "node/create", e.user, map[string]any{"landingServerId": e.servers[0].ID, "name": "shared-node", "protocol": protocol})
	return int64(v.Data.(map[string]any)["id"].(float64))
}
func TestSharedLandingCustomContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			for _, path := range []string{"integration/get", "integration/save", "integration/test", "integration/sync"} {
				if e.call(t, path, e.user, map[string]any{}).Code == 0 {
					t.Fatalf("non-admin %s", path)
				}
			}
			cfg := e.ok(t, "integration/get", e.admin, map[string]any{})
			raw, _ := json.Marshal(cfg)
			if strings.Contains(string(raw), "flvx_fixture") || !cfg.Data.(map[string]any)["hasToken"].(bool) {
				t.Fatal("token exposure")
			}
			if v := e.ok(t, "server/list", e.other, map[string]any{}); len(v.Data.([]any)) != 0 {
				t.Fatal("unauthorized shared server")
			}
			if e.call(t, "node/create", e.other, map[string]any{"landingServerId": s.ID, "name": "bad", "protocol": "ss2022"}).Code == 0 {
				t.Fatal("unauthorized create")
			}
			id := e.node(t, "ss2022")
			vless := e.node(t, "vless_reality_vision")
			n, _ := e.r.GetLandingNode(id)
			if n.Port < s.PortMin || n.Port > s.PortMax {
				t.Fatal("port escaped caps")
			}
			d := e.f.desired[7]
			if len(d.Nodes) != 2 || d.Nodes[0].Owner.ID != "501" || d.Nodes[0].SS2022 == nil || d.Nodes[1].VLESS == nil {
				t.Fatal("desired shape")
			}
			if e.call(t, "node/update", e.admin, map[string]any{"id": id, "maxConnections": 101}).Code == 0 {
				t.Fatal("connection cap bypass")
			}
			if e.call(t, "node/update", e.admin, map[string]any{"id": id, "trafficLimitBytes": 11 << 30}).Code == 0 {
				t.Fatal("traffic cap bypass")
			}
			bad := s
			bad.PortMin--
			if e.r.SaveLandingServer(&bad, []int64{701}) == nil {
				t.Fatal("port cap bypass")
			}
			if err := e.r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "landing", ServerRefID: s.ID, LimitBytes: 11 << 30}}, 0); err == nil {
				t.Fatal("server quota cap bypass")
			}
			links := e.ok(t, "node/links", e.user, map[string]any{"id": vless})
			if !strings.Contains(fmt.Sprint(links.Data), "vless://") {
				t.Fatal("shared links")
			}
			if matched, err := e.r.MatchLandingTarget(501, 0, fmt.Sprintf("203.0.113.7:%d", n.Port)); err != nil || matched != id {
				t.Fatalf("manual target %d %v", matched, err)
			}
			if _, err := e.r.MatchLandingTarget(501, 0, fmt.Sprintf("203.0.113.7:%d,example.org:80", n.Port)); err == nil {
				t.Fatal("mixed target")
			}
			if e.call(t, "node/links", e.other, map[string]any{"id": id}).Code == 0 {
				t.Fatal("cross user links")
			}
			found := false
			for _, h := range e.f.hosts.Nodes {
				if h == "127.0.0.1" {
					found = true
				}
			}
			if !found {
				t.Fatal("unresolved host report")
			}
			e.f.mu.Lock()
			e.f.shares[0].Hosts.Domain = "localhost"
			e.f.mu.Unlock()
			if e.call(t, "server/enable", e.admin, map[string]any{"nodeId": 601}).Code == 0 {
				t.Fatal("dual install allowed")
			}
			if e.call(t, "server/enable", e.admin, map[string]any{"nodeId": 36}).Code == 0 {
				t.Fatal("node36")
			}
		})
	}
}
func TestSharedLandingCountersQuotaExpiryAndRevocation(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			id := e.node(t, "ss2022")
			sid := e.servers[0].ID
			setCounter := func(up, down int64, epoch string) {
				e.f.mu.Lock()
				s := e.f.status[7]
				s.CollectedAt++
				s.Core.Epoch = epoch
				s.Nodes = []landingcustom.NodeStatus{{ID: strconv.FormatInt(id, 10), State: "active", UpBytes: up, DownBytes: down}}
				e.f.status[7] = s
				e.f.mu.Unlock()
			}
			poll := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }
			assert := func(want int64) {
				u, _ := e.r.GetUserByID(501)
				if u.InFlow+u.OutFlow != want {
					t.Fatalf("billing %d want %d", u.InFlow+u.OutFlow, want)
				}
			}
			setCounter(100, 200, "a")
			poll()
			assert(600)
			poll()
			assert(600)
			setCounter(150, 300, "new-core")
			poll()
			assert(900)
			setCounter(10, 20, "new-core")
			poll()
			assert(960)
			poll()
			assert(960)
			// Stale collected_at cannot re-open old totals.
			e.f.mu.Lock()
			st := e.f.status[7]
			st.CollectedAt = 1
			st.Nodes[0].UpBytes = 1000
			e.f.status[7] = st
			e.f.mu.Unlock()
			poll()
			assert(960)
			// Restore the current reliable snapshot for subsequent policy checks.
			setCounter(10, 20, "new-core")
			e.f.mu.Lock()
			st = e.f.status[7]
			st.CollectedAt = time.Now().UnixMilli() + 100
			e.f.status[7] = st
			e.f.mu.Unlock()
			if err := e.r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "landing", ServerRefID: sid, LimitBytes: 900}}, 0); err != nil {
				t.Fatal(err)
			}
			poll()
			n, _ := e.r.GetLandingNode(id)
			if n.Status != "paused_server_quota" {
				t.Fatalf("quota state %s", n.Status)
			}
			if d := e.f.desired[7]; len(d.Nodes) != 1 || d.Nodes[0].Enabled {
				t.Fatal("quota not disabled in desired")
			}
			if err := e.r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "landing", ServerRefID: sid, LimitBytes: 2000}}, 0); err != nil {
				t.Fatal(err)
			}
			poll()
			if !e.f.desired[7].Nodes[0].Enabled {
				t.Fatal("quota not resumed")
			}
			oldPort, oldKey := n.Port, n.ServerKey
			exp := time.Now().Add(-time.Hour).UnixMilli()
			if err := e.r.DB().Model(&model.User{}).Where("id=501").Update("exp_time", exp).Error; err != nil {
				t.Fatal(err)
			}
			poll()
			n, _ = e.r.GetLandingNode(id)
			if n.Status != "paused_expired" || e.f.desired[7].Nodes[0].Enabled {
				t.Fatal("expiry not paused")
			}
			e.r.DB().Model(&model.User{}).Where("id=501").Update("exp_time", time.Now().Add(time.Hour).UnixMilli())
			poll()
			n, _ = e.r.GetLandingNode(id)
			if n.Status != "active" || n.Port != oldPort || n.ServerKey != oldKey {
				t.Fatal("renewal changed identity")
			}
			e.f.mu.Lock()
			e.f.shares[0].Status = "revoked"
			e.f.mu.Unlock()
			poll()
			n, _ = e.r.GetLandingNode(id)
			if n == nil || n.Status == "active" {
				t.Fatal("revoked node not retained/stopped")
			}
			if _, err := e.r.ReconcileLandingPolicy(time.Now().Add(30 * 24 * time.Hour)); err != nil {
				t.Fatal(err)
			}
			n, _ = e.r.GetLandingNode(id)
			if n == nil {
				t.Fatal("revocation silently deleted user data")
			}
		})
	}
}
func TestSharedLanding422DownRecoveryAndDelete(t *testing.T) {
	e := newSharedContract(t, "sqlite")
	id := e.node(t, "ss2022")
	e.f.mu.Lock()
	e.f.reject = strconv.FormatInt(id, 10)
	e.f.mu.Unlock()
	e.ok(t, "node/update", e.admin, map[string]any{"id": id, "name": "changed"})
	n, _ := e.r.GetLandingNode(id)
	if n.RemoteState != "error" || !strings.Contains(n.SyncError, "端口") {
		t.Fatal("422 not persisted per node")
	}
	view := e.ok(t, "node/list", e.user, map[string]any{})
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "flvx_fixture") || strings.Contains(string(raw), n.ServerKey) || !strings.Contains(string(raw), `"status":"error"`) {
		t.Fatal("422 redaction/state")
	}
	e.f.mu.Lock()
	e.f.reject = ""
	e.f.down = true
	e.f.mu.Unlock()
	before, _ := e.r.GetLandingIntegration()
	rev := e.f.desired[7].Revision
	e.ok(t, "integration/sync", e.admin, map[string]any{})
	after, _ := e.r.GetLandingIntegration()
	if after.LastSyncAt != before.LastSyncAt || after.SyncError == "" {
		t.Fatal("last good sync lost")
	}
	if n2, _ := e.r.GetLandingNode(id); n2 == nil || n2.Status != "active" || e.f.desired[7].Revision != rev {
		t.Fatal("outage altered runtime")
	}
	calls := e.f.calls
	e.ok(t, "node/update", e.admin, map[string]any{"id": id, "name": "offline edit"})
	if e.f.calls != calls {
		t.Fatal("backoff ignored")
	}
	e.f.mu.Lock()
	e.f.down = false
	e.f.mu.Unlock()
	e.ok(t, "integration/sync", e.admin, map[string]any{})
	e.ok(t, "integration/sync", e.admin, map[string]any{})
	n, _ = e.r.GetLandingNode(id)
	if n.SyncError != "" {
		t.Fatal("recovery retained error")
	}
	e.ok(t, "node/delete", e.user, map[string]any{"id": id})
	if n, _ = e.r.GetLandingNode(id); n != nil {
		t.Fatal("confirmed removal retained node")
	}
	if len(e.f.desired[7].Nodes) != 0 {
		t.Fatal("delete omitted full push")
	}
}

func TestSharedLandingRejectedNeverAppliedDeletion(t *testing.T) {
	e := newSharedContract(t, "sqlite")
	e.f.reject = "1"
	id := e.node(t, "ss2022")
	n, _ := e.r.GetLandingNode(id)
	if n.RemoteState != "error" || n.LastCounterAt != 0 {
		t.Fatal("expected rejected initial node")
	}
	e.ok(t, "node/delete", e.user, map[string]any{"id": id})
	if n, _ = e.r.GetLandingNode(id); n != nil {
		t.Fatal("rejected node cannot be removed")
	}
}
