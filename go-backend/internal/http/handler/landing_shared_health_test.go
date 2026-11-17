package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// flapCustom: share list + status / desired; `fail` makes one share's status
// answer that HTTP code, `listFail` the share list.
type flapCustom struct {
	mu       sync.Mutex
	shares   []landingcustom.Share
	fail     map[int64]int
	listFail int
}

func (f *flapCustom) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(req.URL.Path, landingcustom.Prefix)
	switch {
	case path == "/shares":
		if f.listFail != 0 {
			w.WriteHeader(f.listFail)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"shares": f.shares})
	case path == "/landing-hosts":
		w.Write([]byte("{}"))
	case strings.HasPrefix(path, "/shares/"):
		var id int64
		var tail string
		fmt.Sscanf(strings.TrimPrefix(path, "/shares/"), "%d/%s", &id, &tail)
		if code := f.fail[id]; code != 0 && tail == "status" {
			w.WriteHeader(code)
			w.Write([]byte(`{}`))
			return
		}
		switch tail {
		case "status":
			json.NewEncoder(w).Encode(landingcustom.Status{CollectedAt: time.Now().UnixMilli(), Core: landingcustom.Core{State: "running"}})
		case "desired":
			var d landingcustom.Desired
			json.NewDecoder(req.Body).Decode(&d)
			json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": d.Revision})
		default:
			w.WriteHeader(404)
		}
	default:
		w.WriteHeader(404)
	}
}

func (f *flapCustom) set(fn func()) {
	f.mu.Lock()
	fn()
	f.mu.Unlock()
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureSyncLog(t *testing.T) *lockedBuf {
	t.Helper()
	buf := &lockedBuf{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buf
}

// Plan 271 end to end, both dialects: 5 shares; one status poll answers 503.
//   - one failure: every server stays online, no node/server shows 同步异常,
//     although the stored errors are written as before (delete gate input);
//   - the same server failing ≥ 3 min (no successful poll): only it is 离线
//     with its own text; the others stay online;
//   - recovery clears the stored error, the delete gate opens again;
//   - the log has one fail line (key, server, class, duration) and one ok line.
func TestSharedSyncFlapNeedsThreeMinutes(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := &Handler{repo: r}
			logs := captureSyncLog(t)
			f := &flapCustom{fail: map[int64]int{}}
			for _, id := range []int64{5, 7, 10, 12, 16} {
				f.shares = append(f.shares, tolerantShare(id))
			}
			srv := httptest.NewServer(f)
			defer srv.Close()
			if err := r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			h.syncSharedIntegration(ctx, true)
			byRemote := map[int64]model.LandingServer{}
			reload := func() {
				t.Helper()
				ss, err := r.ListLandingServers(0, true)
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range ss {
					byRemote[s.RemoteServerID] = s
				}
			}
			reload()
			if len(byRemote) != 5 {
				t.Fatalf("servers %d", len(byRemote))
			}
			view := func(remote int64) (bool, string) {
				t.Helper()
				reload()
				v := h.landingServerView(byRemote[remote])
				msg, _ := v["sharedSyncError"].(string)
				return v["online"] == true, msg
			}
			for remote := range byRemote {
				if on, msg := view(remote); !on || msg != "" {
					t.Fatalf("share %d after the first round: online=%v %q", remote, on, msg)
				}
			}
			// A user node on the failing server: its 同步异常 follows the server.
			bad := byRemote[7]
			now := time.Now().UnixMilli()
			if err := r.DB().Create(&model.User{ID: 30, User: "flap-user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Create(&model.LandingNode{ID: 70, UserID: 30, LandingServerID: bad.ID, Name: "n70", Protocol: "ss2022", Port: 30010, InboundTag: "flvxl-70", Status: "active", RemoteState: "active", CreatedTime: now, UpdatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			nodeErr := func() string {
				n, err := r.GetLandingNode(70)
				if err != nil || n == nil {
					t.Fatal(err)
				}
				v := h.landingNodeView(*n)
				msg, _ := v["sharedSyncError"].(string)
				return msg
			}

			// The background round a minute later: every status is due, share 7's
			// answer is 503 → breaker opens, the polls after it are skipped.
			f.set(func() { f.fail[7] = 503 })
			if err := r.DB().Model(&model.LandingServer{}).Where("source = ?", "shared").
				Update("shared_last_sync_at", time.Now().Add(-61*time.Second).UnixMilli()).Error; err != nil {
				t.Fatal(err)
			}
			h.syncSharedIntegration(ctx, false)
			reload()
			if byRemote[7].SharedSyncError == "" {
				t.Fatal("the failure must still be stored (delete gate input)")
			}
			if cfg, _ := r.GetLandingIntegration(); cfg.SyncError == "" {
				t.Fatal("the 接入设置 card still reports the last round")
			}
			for remote := range byRemote {
				if on, msg := view(remote); !on || msg != "" {
					t.Fatalf("one failed request flipped share %d: online=%v %q", remote, on, msg)
				}
			}
			if msg := nodeErr(); msg != "" {
				t.Fatalf("node shows 同步异常 after one failure: %q", msg)
			}
			if !strings.Contains(logs.String(), fmt.Sprintf("[shared-sync] fail req=status server=%d class=http 503", bad.ID)) {
				t.Fatalf("fail line missing:\n%s", logs.String())
			}

			// Delete gate: an expired, settled node on the failing server stays.
			past := time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
			if err := r.DB().Create(&model.User{ID: 31, User: "gone-user", Pwd: "x", RoleID: 1, ExpTime: past, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Create(&model.LandingNode{ID: 71, UserID: 31, LandingServerID: bad.ID, Name: "n71", Protocol: "ss2022", Port: 30011, InboundTag: "flvxl-71", Status: "expired", RemoteState: "removed", ExpiredAt: &past, LastCounterAt: time.Now().UnixMilli(), CreatedTime: past, UpdatedTime: past}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			if n, _ := r.GetLandingNode(71); n == nil {
				t.Fatal("a still-failing sync must block the delete")
			}

			// ≥ 3 min without a successful poll of share 7.
			health := h.sharedHealth()
			health.mu.Lock()
			for _, key := range sharedServerKeys(bad.ID) {
				if k := health.keys[key]; k != nil {
					k.since = k.since.Add(-4 * time.Minute)
				}
			}
			health.mu.Unlock()
			if err := r.DB().Model(&model.LandingServer{}).Where("id = ?", bad.ID).
				Update("shared_last_sync_at", time.Now().Add(-4*time.Minute).UnixMilli()).Error; err != nil {
				t.Fatal(err)
			}
			if on, msg := view(7); on || !strings.Contains(msg, "HTTP 503") {
				t.Fatalf("share 7 failing for 4 min: online=%v %q", on, msg)
			}
			if msg := nodeErr(); !strings.Contains(msg, "HTTP 503") {
				t.Fatalf("node of the failing share: %q", msg)
			}
			for _, remote := range []int64{5, 10, 12, 16} {
				if on, msg := view(remote); !on || msg != "" {
					t.Fatalf("share 7's problem marked share %d: online=%v %q", remote, on, msg)
				}
			}

			// Recovery: one clean round clears share 7, the gate opens.
			f.set(func() { delete(f.fail, 7) })
			h.syncSharedIntegration(ctx, true)
			for remote := range byRemote {
				if on, msg := view(remote); !on || msg != "" {
					t.Fatalf("share %d after recovery: online=%v %q", remote, on, msg)
				}
			}
			reload()
			for remote, s := range byRemote {
				if s.SharedSyncError != "" {
					t.Fatalf("share %d keeps a stored error after recovery: %q", remote, s.SharedSyncError)
				}
			}
			if msg := nodeErr(); msg != "" {
				t.Fatalf("node after recovery: %q", msg)
			}
			if !strings.Contains(logs.String(), fmt.Sprintf("[shared-sync] ok req=status server=%d after", bad.ID)) {
				t.Fatalf("ok line missing:\n%s", logs.String())
			}
			if _, err := r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			if n, _ := r.GetLandingNode(71); n != nil {
				t.Fatal("after recovery the settled node is released")
			}
			if strings.Count(logs.String(), "[shared-sync] fail req=status server=") > 1 {
				t.Fatalf("one failure, more than one status fail line:\n%s", logs.String())
			}
		})
	}
}

// The share list failing: nothing changes for 3 min (servers keep their last
// state), then every server shows the integration error; recovery clears it.
func TestSharedSyncListOutageShowsAfterThreeMinutes(t *testing.T) {
	r := sharedLandingTestRepo(t, "sqlite")
	h := &Handler{repo: r}
	_ = captureSyncLog(t)
	f := &flapCustom{fail: map[int64]int{}}
	for _, id := range []int64{5, 7} {
		f.shares = append(f.shares, tolerantShare(id))
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if err := r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h.syncSharedIntegration(ctx, true)
	f.set(func() { f.listFail = 502 })
	h.syncSharedIntegration(ctx, true)
	servers := func() []model.LandingServer {
		ss, err := r.ListLandingServers(0, true)
		if err != nil || len(ss) != 2 {
			t.Fatalf("servers %d %v", len(ss), err)
		}
		return ss
	}
	for _, s := range servers() {
		if v := h.landingServerView(s); v["online"] != true || v["sharedSyncError"] != "" {
			t.Fatalf("list failed once: %v %q", v["online"], v["sharedSyncError"])
		}
	}
	health := h.sharedHealth()
	health.mu.Lock()
	health.keys["shares"].since = time.Now().Add(-4 * time.Minute)
	health.mu.Unlock()
	for _, s := range servers() {
		if v := h.landingServerView(s); v["online"] == true || !strings.Contains(fmt.Sprint(v["sharedSyncError"]), "HTTP 502") {
			t.Fatalf("list failing 4 min: %v %q", v["online"], v["sharedSyncError"])
		}
	}
	f.set(func() { f.listFail = 0 })
	h.syncSharedIntegration(ctx, true)
	for _, s := range servers() {
		if v := h.landingServerView(s); v["online"] != true || v["sharedSyncError"] != "" {
			t.Fatalf("after recovery: %v %q", v["online"], v["sharedSyncError"])
		}
	}
}

// Log rate limit: one line per key+class per minute, one line when it clears,
// breaker skips folded into one line per minute.
func TestSharedSyncHealthLogRateLimit(t *testing.T) {
	logs := captureSyncLog(t)
	s := newSharedSyncHealth()
	clk := time.Date(2026, 11, 17, 7, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clk }
	timeout := &landingcustom.TransportError{Class: landingcustom.ClassTimeout}
	for i := 0; i < 10; i++ {
		s.fail("status:3", timeout, timeout.Error(), 15*time.Second)
		clk = clk.Add(5 * time.Second)
	}
	if n := strings.Count(logs.String(), "fail req=status server=3 class=timeout"); n != 1 {
		t.Fatalf("10 failures in 50 s → %d lines", n)
	}
	s.fail("status:3", &landingcustom.TransportError{Class: landingcustom.ClassDNS}, "dns", time.Millisecond)
	if !strings.Contains(logs.String(), "class=dns") {
		t.Fatal("a new class logs at once")
	}
	clk = clk.Add(time.Minute)
	s.fail("status:3", timeout, timeout.Error(), 15*time.Second)
	if n := strings.Count(logs.String(), "class=timeout"); n != 2 {
		t.Fatalf("after a minute the class logs again: %d", n)
	}
	for i := 0; i < 30; i++ {
		s.skip(fmt.Sprintf("user-limits:%d", i%3), "x（自动重试中）", true, landingcustom.ClassTimeout)
		clk = clk.Add(3 * time.Second)
	}
	if n := strings.Count(logs.String(), "class=breaker-open"); n != 1 {
		t.Fatalf("30 skips in 90 s → %d breaker lines", n)
	}
	s.ok("status:3")
	if !strings.Contains(logs.String(), "ok req=status server=3 after") || !strings.Contains(logs.String(), "breaker closed") {
		t.Fatalf("clear lines:\n%s", logs.String())
	}
	s.ok("status:3")
	if n := strings.Count(logs.String(), "ok req=status server=3"); n != 1 {
		t.Fatalf("a second ok logs nothing: %d", n)
	}
	// verdicts are never hidden
	s.fail("push:4", &landingcustom.APIError{Status: 422}, "拒绝", time.Millisecond)
	if s.hideServerError(4, "拒绝", clk) {
		t.Fatal("a 422 shows at once")
	}
}
