package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func onlineIPCounter(c repo.LandingCounter, count int, ips ...landingcustom.OnlineIP) repo.LandingCounter {
	c.OnlineIPCount, c.OnlineIPs = &count, ips
	return c
}

func TestLandingOnlineIPsLatestBoundedAndStale(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			tr := newLandingRateTracker(2)
			base := int64(1_700_000_000_000)
			counter := func(at int64) repo.LandingCounter {
				if shared {
					return sharedCounter(1, at, 0, 0, 3)
				}
				return nativeCounter(1, 1, 0, 0, 3)
			}
			observe := func(c repo.LandingCounter, at int64) {
				t.Helper()
				tr.observe([]repo.LandingCounter{c}, at)
			}
			observe(counter(base), base)
			if got := tr.onlineIPs(1, base); got.OnlineIPs != nil || len(got.IPs) != 0 || got.SampleAt != base {
				t.Fatalf("old reporter must be unknown: %+v", got)
			}
			observe(onlineIPCounter(counter(base+1), 0), base+1)
			if got := tr.onlineIPs(1, base+1); got.OnlineIPs == nil || *got.OnlineIPs != 0 || len(got.IPs) != 0 {
				t.Fatalf("known zero lost: %+v", got)
			}

			ips := []landingcustom.OnlineIP{
				{IP: "2001:db8::2", Connections: 100},
				{IP: "2001:db8::1", Connections: 100},
				{IP: "198.51.100.1", Connections: 101},
			}
			for i := 70; i > 0; i-- {
				ips = append(ips, landingcustom.OnlineIP{IP: fmt.Sprintf("192.0.2.%d", i), Connections: i})
			}
			observe(onlineIPCounter(counter(base+2), len(ips), ips...), base+2)
			ips[0].IP = "203.0.113.99"
			got := tr.onlineIPs(1, base+2)
			if got.OnlineIPs == nil || *got.OnlineIPs != 73 || len(got.IPs) != 64 || got.IPs[0].IP != "198.51.100.1" || got.IPs[1].IP != "2001:db8::1" || got.IPs[2].IP != "2001:db8::2" {
				t.Fatalf("count/sort/cap/owned copy: %+v", got)
			}
			got.IPs[0].IP = "203.0.113.98"
			*got.OnlineIPs = 1
			// A duplicate/older collection does not replace the latest list.
			observe(onlineIPCounter(counter(base+1), 0), base+1)
			got = tr.onlineIPs(1, base+3)
			if *got.OnlineIPs != 73 || got.IPs[0].IP != "198.51.100.1" {
				t.Fatalf("snapshot mutation or old sample changed tracker: %+v", got)
			}
			atLimit := base + 2 + landingRateStaleAfterMs
			if got = tr.onlineIPs(1, atLimit); got.OnlineIPs == nil {
				t.Fatal("sample expired before the rate's stale threshold")
			}
			if got = tr.onlineIPs(1, atLimit+1); got.OnlineIPs != nil || len(got.IPs) != 0 || got.SampleAt != base+2 {
				t.Fatalf("stale IPs leaked: %+v", got)
			}
			// A newer legacy report clears the previously known count/list.
			observe(counter(base+3), base+3)
			if got = tr.onlineIPs(1, base+3); got.OnlineIPs != nil || len(got.IPs) != 0 {
				t.Fatalf("legacy report reused the old count: %+v", got)
			}
			observe(onlineIPCounter(counter(base+4), 1, landingcustom.OnlineIP{IP: "2001:db8::1", Connections: 2}), base+4)
			tr.forget(1)
			if got = tr.onlineIPs(1, base+4); got.OnlineIPs != nil || len(got.IPs) != 0 || got.SampleAt != 0 {
				t.Fatalf("deleted IPs retained: %+v", got)
			}
			observe(onlineIPCounter(counter(base+5), 1, landingcustom.OnlineIP{IP: "2001:db8::1", Connections: 2}), base+5)
			tr.retain([]int64{1}, base+5+landingRateTTLMs+1)
			if tr.size() != 0 {
				t.Fatal("TTL retained IP entry")
			}
			observe(onlineIPCounter(counter(base+6), 1), base+6)
			tr.retain(nil, base+6)
			if tr.size() != 0 {
				t.Fatal("retain kept deleted IP entry")
			}
			for id := int64(1); id <= 3; id++ {
				c := counter(base + id)
				c.ID = id
				observe(onlineIPCounter(c, 1), base+id)
			}
			if got = tr.onlineIPs(1, base+3); got.OnlineIPs != nil || got.SampleAt != 0 || tr.size() != 2 {
				t.Fatalf("capacity eviction kept old IP entry: %+v", got)
			}
		})
	}
}

func TestLandingOnlineIPsNativeFlowUpload(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	var counters []repo.LandingCounter
	if err := json.Unmarshal([]byte(`[{"id":1,"inbound_tag":"flvxl-1","epoch":"1000","connections":4,"online_ip_count":2,"online_ips":[{"ip":"2001:db8::1","connections":1},{"ip":"192.0.2.1","connections":3}]}]`), &counters); err != nil {
		t.Fatal(err)
	}
	if _, err := e.h.commitFlowWithLanding(2, nil, counters); err != nil {
		t.Fatal(err)
	}
	got := e.h.landingRateTracker().onlineIPs(1, time.Now().UnixMilli())
	if got.OnlineIPs == nil || *got.OnlineIPs != 2 || len(got.IPs) != 2 || got.IPs[0].IP != "192.0.2.1" {
		t.Fatalf("native upload not tracked: %+v", got)
	}
	counters[0].OnlineIPCount = nil
	counters[0].OnlineIPs = nil
	if _, err := e.h.commitFlowWithLanding(99, nil, counters); err == nil {
		t.Fatal("foreign native upload accepted")
	}
	if got = e.h.landingRateTracker().onlineIPs(1, time.Now().UnixMilli()); got.OnlineIPs == nil || *got.OnlineIPs != 2 {
		t.Fatalf("failed flow commit changed tracked IPs: %+v", got)
	}
}

func TestLandingOnlineIPsSharedStatus(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	share := landingcustom.Share{ID: 7, Name: "shared-ips", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}}
	now := time.Now().UnixMilli()
	if err := e.r.SyncSharedLandings([]landingcustom.Share{share}, now); err != nil {
		t.Fatal(err)
	}
	servers, err := e.r.ListLandingServers(0, true)
	if err != nil {
		t.Fatal(err)
	}
	grants := []int64{servers[0].ID}
	if err := e.r.SaveLandingUserSettingWithGrants(2, 5, nil, 0, &grants); err != nil {
		t.Fatal(err)
	}
	n, err := e.r.CreateLandingNode(2, servers[0].ID, "shared-ips", "ss2022", true)
	if err != nil {
		t.Fatal(err)
	}
	body := ""
	requests := 0
	custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != landingcustom.Prefix+"/shares/7/status" {
			t.Errorf("unexpected extra Custom request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, body)
	}))
	defer custom.Close()
	client, err := landingcustom.New(custom.URL, "flvx_fixture")
	if err != nil {
		t.Fatal(err)
	}
	for i, fields := range []string{
		`,"online_ip_count":2,"online_ips":[{"ip":"2001:db8::1","connections":2},{"ip":"192.0.2.1","connections":1}]`,
		`,"online_ip_count":0,"online_ips":[]`,
		``,
	} {
		at := now + int64(i)
		body = fmt.Sprintf(`{"collected_at":%d,"core":{"state":"running"},"nodes":[{"id":"%d","state":"active","connections":3%s}]}`, at, n.ID, fields)
		if _, err = e.h.pollSharedLanding(context.Background(), client, servers[0]); err != nil {
			t.Fatal(err)
		}
		got := e.h.landingRateTracker().onlineIPs(n.ID, at)
		if got.SampleAt != at || (i == 2 && got.OnlineIPs != nil) || (i < 2 && (got.OnlineIPs == nil || *got.OnlineIPs != 2-i*2)) || (i == 0 && (len(got.IPs) != 2 || got.IPs[0].IP != "2001:db8::1")) || (i > 0 && len(got.IPs) != 0) {
			t.Fatalf("shared status fields %q: %+v", fields, got)
		}
	}
	if requests != 3 {
		t.Fatalf("added Custom calls: %d", requests)
	}
}

func callLandingMonitorIPs(t *testing.T, h *Handler, userID int64, role int, nodeID int64) (int, landingMonitorIPsView, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/monitor/ips", strings.NewReader(fmt.Sprintf(`{"nodeId":%d}`, nodeID)))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: fmt.Sprint(userID), RoleID: role}))
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	h.registerLanding(mux)
	mux.ServeHTTP(w, req)
	var env struct {
		Code int                   `json:"code"`
		Data landingMonitorIPsView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return env.Code, env.Data, w.Body.String()
}

func TestLandingMonitorIPsPermissionsAndUnknown(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingMonitorRepo(t, dialect)
			h := &Handler{repo: r}
			now := time.Now().UnixMilli()
			for _, row := range []any{
				&model.User{ID: 21, User: "owner", RoleID: 1, Status: 1},
				&model.User{ID: 22, User: "other", RoleID: 1, Status: 1},
				&model.User{ID: 23, User: "no-monitor", RoleID: 1, Status: 1},
				&model.LandingServer{ID: 41, Source: "native", NodeID: 61, Name: "visible", Status: "active"},
				&model.LandingServerUser{LandingServerID: 41, UserID: 21},
				&model.LandingServerUser{LandingServerID: 41, UserID: 22},
				&model.LandingServerUser{LandingServerID: 41, UserID: 23},
				&model.LandingNode{ID: 51, LandingServerID: 41, UserID: 21, Name: "owner-node", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-51", Status: "active"},
				&model.LandingNode{ID: 52, LandingServerID: 41, UserID: 23, Name: "no-monitor-node", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-52", Status: "active"},
			} {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []int64{21, 22} {
				if err := r.InsertMonitorPermission(id, now); err != nil {
					t.Fatal(err)
				}
			}
			// Full monitor access still never grants another user's landing IPs.
			if err := r.DB().Model(&model.MonitorPermission{}).Where("user_id = ?", 22).Update("full_access", 1).Error; err != nil {
				t.Fatal(err)
			}
			ips := []landingcustom.OnlineIP{{IP: "192.0.2.1", Connections: 3}, {IP: "2001:db8::1", Connections: 1}}
			h.landingRateTracker().observe([]repo.LandingCounter{onlineIPCounter(nativeCounter(51, 1, 0, 0, 4), 2, ips...)}, now)
			for _, access := range []struct {
				name string
				user int64
				role int
				node int64
				code int
			}{
				{"admin", 1, 0, 51, 0},
				{"owner", 21, 1, 51, 0},
				{"other-user-full-monitor", 22, 1, 51, 403},
				{"no-monitor-own-node", 23, 1, 52, 403},
				{"missing-node", 1, 0, 999, 403},
			} {
				code, got, raw := callLandingMonitorIPs(t, h, access.user, access.role, access.node)
				if code != access.code {
					t.Fatalf("%s: %s", access.name, raw)
				}
				if code == 0 && (got.OnlineIPs == nil || *got.OnlineIPs != 2 || !slices.Equal(got.IPs, ips) || got.SampleAt != now) {
					t.Fatalf("%s detail: %s", access.name, raw)
				}
				if code != 0 && (strings.Contains(raw, "192.0.2.1") || strings.Contains(raw, "2001:db8::1")) {
					t.Fatalf("%s leaked IPs: %s", access.name, raw)
				}
			}
			code, view, raw := callLandingMonitor(t, h, 21, 1)
			if code != 0 || len(view.Servers) != 1 || len(view.Servers[0].Nodes) != 1 || view.Servers[0].Nodes[0].OnlineIPs == nil || *view.Servers[0].Nodes[0].OnlineIPs != 2 {
				t.Fatalf("monitor count: %s", raw)
			}
			if strings.Contains(raw, "192.0.2.1") {
				t.Fatalf("regular monitor unnecessarily returned IP list: %s", raw)
			}
			if code, got, raw := callLandingMonitorIPs(t, h, 1, 0, 52); code != 0 || got.OnlineIPs != nil || got.SampleAt != 0 || !strings.Contains(raw, `"ips":[]`) || !strings.Contains(raw, `"onlineIps":null`) {
				t.Fatalf("never reported node: %s", raw)
			}
			tr := h.landingRateTracker()
			tr.forget(51)
			tr.observe([]repo.LandingCounter{onlineIPCounter(nativeCounter(51, 1, 0, 0, 4), 2, ips...)}, now-landingRateStaleAfterMs-1000)
			if code, got, raw := callLandingMonitorIPs(t, h, 21, 1, 51); code != 0 || got.OnlineIPs != nil || len(got.IPs) != 0 || got.SampleAt == 0 {
				t.Fatalf("stale detail: %s", raw)
			}
			if _, view, raw := callLandingMonitor(t, h, 21, 1); view.Servers[0].Nodes[0].OnlineIPs != nil {
				t.Fatalf("stale monitor count: %s", raw)
			}
			if err := r.DB().Model(&model.LandingServer{}).Where("id = ?", 41).Update("status", "paused").Error; err != nil {
				t.Fatal(err)
			}
			if code, _, raw = callLandingMonitorIPs(t, h, 21, 1, 51); code != 403 {
				t.Fatalf("owner on hidden server: %s", raw)
			}
			if err := r.DB().Model(&model.LandingServer{}).Where("id = ?", 41).Update("status", "active").Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Where("landing_server_id = ? AND user_id = ?", 41, 21).Delete(&model.LandingServerUser{}).Error; err != nil {
				t.Fatal(err)
			}
			if code, _, raw = callLandingMonitorIPs(t, h, 21, 1, 51); code != 403 {
				t.Fatalf("owner without server grant: %s", raw)
			}
		})
	}
}
