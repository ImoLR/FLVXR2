package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// fakeRegionResolver answers from a map; hosts in `block` wait until released.
type fakeRegionResolver struct {
	mu      sync.Mutex
	answers map[string]string
	block   map[string]chan struct{}
	calls   map[string]int
}

func newFakeRegionResolver(answers map[string]string) *fakeRegionResolver {
	return &fakeRegionResolver{answers: answers, block: map[string]chan struct{}{}, calls: map[string]int{}}
}

func (f *fakeRegionResolver) hold(host string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.block[host] = ch
	return ch
}

func (f *fakeRegionResolver) resolve(host string) string {
	f.mu.Lock()
	f.calls[host]++
	ch := f.block[host]
	f.mu.Unlock()
	if ch != nil {
		<-ch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers[host]
}

func (f *fakeRegionResolver) count(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[host]
}

// waitIdle waits until no lookup of host is running.
func waitRegionIdle(t *testing.T, c *landingRegionCache, host string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		c.mu.Lock()
		e := c.entries[landingRegionHost(host)]
		idle := e != nil && e.inflight == nil
		c.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("lookup of %s still running", host)
}

// TestLandingRegionCache (plan 286): a never-seen host waits at most the
// caller's bound, then comes back ” while its lookup finishes in the
// background; resolved hosts are served from the cache, stale ones are
// served while refreshing, failed ones are retried after landingRegionRetry.
func TestLandingRegionCache(t *testing.T) {
	f := newFakeRegionResolver(map[string]string{"fast.example": "tw", "slow.example": "JP", "203.0.113.5": "us"})
	c := newLandingRegionCache(f.resolve)
	clock := time.Unix(1_800_000_000, 0)
	var clockMu sync.Mutex
	c.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	release := f.hold("slow.example")

	start := time.Now()
	got := c.lookup([]string{"fast.example", "slow.example", "[203.0.113.5]:443", "", "missing.example"}, 80*time.Millisecond)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("lookup blocked %v", took)
	}
	if got["fast.example"] != "TW" || got["slow.example"] != "" || got["[203.0.113.5]:443"] != "US" || got[""] != "" || got["missing.example"] != "" {
		t.Fatalf("first lookup: %v", got)
	}
	// The slow lookup lands in the cache once it finishes.
	close(release)
	waitRegionIdle(t, c, "slow.example")
	if got := c.lookup([]string{"slow.example"}, 0); got["slow.example"] != "JP" {
		t.Fatalf("after background lookup: %v", got)
	}
	// Cached: no new resolver call.
	_ = c.lookup([]string{"fast.example"}, 0)
	if n := f.count("fast.example"); n != 1 {
		t.Fatalf("fast.example resolved %d times", n)
	}
	// Failed ('') host: retried only after landingRegionRetry.
	_ = c.lookup([]string{"missing.example"}, 0)
	if n := f.count("missing.example"); n != 1 {
		t.Fatalf("missing.example retried too early: %d", n)
	}
	advance(landingRegionRetry)
	_ = c.lookup([]string{"missing.example"}, time.Second)
	waitRegionIdle(t, c, "missing.example")
	if n := f.count("missing.example"); n != 2 {
		t.Fatalf("missing.example not retried: %d", n)
	}
	// Stale after the TTL: the old value is served at once, a refresh runs.
	f.mu.Lock()
	f.answers["fast.example"] = "HK"
	f.mu.Unlock()
	refresh := f.hold("fast.example")
	advance(landingRegionTTL)
	start = time.Now()
	if got := c.lookup([]string{"fast.example"}, time.Second); got["fast.example"] != "TW" {
		t.Fatalf("stale value not served: %v", got)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("stale lookup waited %v", took)
	}
	close(refresh)
	waitRegionIdle(t, c, "fast.example")
	if got := c.lookup([]string{"fast.example"}, 0); got["fast.example"] != "HK" || f.count("fast.example") != 2 {
		t.Fatalf("refresh: %v calls %d", got, f.count("fast.example"))
	}
}

func regionSeed(t *testing.T, e *flowTestEnv) {
	t.Helper()
	e.addUser(2)
	e.addNode(2, "landing-secret-2")
	e.addNode(3, "landing-secret-3")
	e.exec("UPDATE node SET region = 'kr', landing_capable = ? WHERE id = 2", true)
	e.exec("UPDATE node SET region = '', landing_capable = ? WHERE id = 3", true)
	base := func(id, node int64, source, host string) *model.LandingServer {
		return &model.LandingServer{ID: id, NodeID: node, RemoteServerID: id * 10, Source: source, Name: fmt.Sprint("s", id), PublicHost: host, PortMin: 30000 + int(id)*100, PortMax: 30050 + int(id)*100, FlowType: 2, TrafficRatio: 1, Status: "active",
			Protocols: `["ss2022"]`, RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CreatedTime: 1, UpdatedTime: 1}
	}
	for _, s := range []*model.LandingServer{
		base(1, 2, "native", "kr-native.example"),       // node region KR wins over GeoIP
		base(2, 3, "native", "native-geo.example"),      // node region unset → GeoIP JP
		base(3, 0, "shared", "shared-tw.example"),       // shared → GeoIP TW
		base(4, 0, "shared", "unknown.example"),         // GeoIP unknown → ''
		base(5, 0, "shared", "shared-override.example"), // override HK wins over GeoIP US
	} {
		if s.Source == "shared" {
			s.NodeID = 0
		}
		if err := e.r.DB().Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	e.exec("UPDATE landing_server SET region_override = 'HK' WHERE id = 5")
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2}).Error; err != nil {
		t.Fatal(err)
	}
}

func regionList(t *testing.T, e *flowTestEnv, uid int64, role int) map[int64]map[string]any {
	t.Helper()
	out := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, uid, role)
	if out.Code != 0 {
		t.Fatalf("list: %+v", out)
	}
	raw, _ := json.Marshal(out.Data)
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	byID := map[int64]map[string]any{}
	for _, v := range list {
		byID[int64(v["id"].(float64))] = v
	}
	return byID
}

// TestLandingServerRegionView (plan 286): region order override > native
// node.region > GeoIP of the public host > ”; admin list only; a host
// that hangs never holds the list longer than landingRegionWait.
func TestLandingServerRegionView(t *testing.T) {
	e := newFlowTestEnv(t)
	regionSeed(t, e)
	f := newFakeRegionResolver(map[string]string{"kr-native.example": "DE", "native-geo.example": "JP", "shared-tw.example": "TW", "shared-override.example": "US"})
	e.h.landingRegionC = newLandingRegionCache(f.resolve)
	release := f.hold("unknown.example")

	start := time.Now()
	list := regionList(t, e, 1, 0)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("list waited %v for a hanging lookup", took)
	}
	want := map[int64][3]string{1: {"KR", "KR", ""}, 2: {"JP", "JP", ""}, 3: {"TW", "TW", ""}, 4: {"", "", ""}, 5: {"HK", "US", "HK"}}
	check := func(list map[int64]map[string]any) {
		t.Helper()
		for id, w := range want {
			v := list[id]
			if v == nil || v["region"] != w[0] || v["regionAuto"] != w[1] || v["regionOverride"] != w[2] {
				t.Fatalf("server %d: region=%v auto=%v override=%v, want %v", id, v["region"], v["regionAuto"], v["regionOverride"], w)
			}
		}
	}
	check(list)
	// The native server with a node region never asks DNS.
	if n := f.count("kr-native.example"); n != 0 {
		t.Fatalf("kr-native.example resolved %d times", n)
	}
	// The hanging host fills in once its lookup returns.
	f.mu.Lock()
	f.answers["unknown.example"] = "SG"
	f.mu.Unlock()
	close(release)
	waitRegionIdle(t, e.h.landingRegions(), "unknown.example")
	want[4] = [3]string{"SG", "SG", ""}
	check(regionList(t, e, 1, 0))

	// Users never get the region fields.
	user := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 2, 1)
	raw, _ := json.Marshal(user.Data)
	if user.Code != 0 || !strings.Contains(string(raw), `"id":1`) {
		t.Fatalf("user list: %s", raw)
	}
	for _, key := range []string{`"region"`, `"regionAuto"`, `"regionOverride"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("user view has %s: %s", key, raw)
		}
	}
}

// TestLandingServerRegionUpdate (plan 286): /landing/server/region sets and
// clears the 「显示地区」 (two-letter code or ”), admin only, one column
// (no revision bump); /landing/server/save keeps it.
func TestLandingServerRegionUpdate(t *testing.T) {
	e := newFlowTestEnv(t)
	regionSeed(t, e)
	f := newFakeRegionResolver(map[string]string{"native-geo.example": "JP"})
	e.h.landingRegionC = newLandingRegionCache(f.resolve)
	stored := func(id int64) (string, int64) {
		t.Helper()
		s, err := e.r.GetLandingServer(id)
		if err != nil || s == nil {
			t.Fatalf("server %d: %v", id, err)
		}
		return s.RegionOverride, s.Revision
	}
	_, rev := stored(2)

	out := layerCall(t, e.h, "/api/v1/landing/server/region", map[string]any{"id": 2, "regionOverride": " tw "}, 1, 0)
	data, _ := out.Data.(map[string]any)
	if out.Code != 0 || data["regionOverride"] != "TW" || data["region"] != "TW" || data["regionAuto"] != "JP" {
		t.Fatalf("set: %+v", out)
	}
	if code, r := stored(2); code != "TW" || r != rev {
		t.Fatalf("stored %q rev %d (was %d)", code, r, rev)
	}
	if v := regionList(t, e, 1, 0)[2]; v["region"] != "TW" || v["regionOverride"] != "TW" {
		t.Fatalf("list after set: %v", v)
	}

	// A full server save (the dialog never sends the field) keeps it.
	srv, _ := e.r.GetLandingServer(2)
	body := map[string]any{"id": 2, "nodeId": 3, "name": "renamed", "publicHost": srv.PublicHost, "portMin": srv.PortMin, "portMax": srv.PortMax, "flowType": 2, "trafficRatio": 1,
		"maxNodesPerUser": 3, "protocols": []string{"ss2022"}, "realityDest": srv.RealityDest, "realityServerNames": []string{"www.lovelive-anime.jp"},
		"coreMemoryMb": 256, "coreCpuPercent": 50, "status": "active", "defaultExpiryMode": "follow_user"}
	if out := layerCall(t, e.h, "/api/v1/landing/server/save", body, 1, 0); out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	if code, _ := stored(2); code != "TW" {
		t.Fatalf("save changed the override to %q", code)
	}

	// Clear → automatic again.
	out = layerCall(t, e.h, "/api/v1/landing/server/region", map[string]any{"id": 2, "regionOverride": ""}, 1, 0)
	data, _ = out.Data.(map[string]any)
	if out.Code != 0 || data["regionOverride"] != "" || data["region"] != "JP" {
		t.Fatalf("clear: %+v", out)
	}
	if code, _ := stored(2); code != "" {
		t.Fatalf("not cleared: %q", code)
	}

	// Validation and access.
	for name, c := range map[string]struct {
		body map[string]any
		uid  int64
		role int
	}{
		"three letters": {map[string]any{"id": 2, "regionOverride": "TWN"}, 1, 0},
		"digits":        {map[string]any{"id": 2, "regionOverride": "1A"}, 1, 0},
		"not a country": {map[string]any{"id": 2, "regionOverride": "ZZ"}, 1, 0},
		"missing field": {map[string]any{"id": 2}, 1, 0},
		"no id":         {map[string]any{"regionOverride": "JP"}, 1, 0},
		"unknown id":    {map[string]any{"id": 99, "regionOverride": "JP"}, 1, 0},
		"normal user":   {map[string]any{"id": 2, "regionOverride": "JP"}, 2, 1},
	} {
		if out := layerCall(t, e.h, "/api/v1/landing/server/region", c.body, c.uid, c.role); out.Code == 0 {
			t.Fatalf("%s accepted: %+v", name, out)
		}
	}
	if code, _ := stored(2); code != "" {
		t.Fatalf("rejected request changed the override to %q", code)
	}
}

// TestLandingNodeListUserAdmin (plan 286): admins see which nodes belong to
// an admin account (全部节点 groups); users never get the field.
func TestLandingNodeListUserAdmin(t *testing.T) {
	e := newFlowTestEnv(t)
	regionSeed(t, e)
	for _, n := range []*model.LandingNode{
		{ID: 1, UserID: 1, LandingServerID: 1, Name: "mine", Protocol: "ss2022", Port: 30101, InboundTag: "flvxl-1", Status: "active"},
		{ID: 2, UserID: 2, LandingServerID: 1, Name: "theirs", Protocol: "ss2022", Port: 30102, InboundTag: "flvxl-2", Status: "active"},
	} {
		if err := e.r.DB().Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	nodes := func(uid int64, role int) map[string]map[string]any {
		t.Helper()
		out := layerCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, uid, role)
		raw, _ := json.Marshal(out.Data)
		var list []map[string]any
		_ = json.Unmarshal(raw, &list)
		byName := map[string]map[string]any{}
		for _, v := range list {
			byName[v["name"].(string)] = v
		}
		return byName
	}
	admin := nodes(1, 0)
	if admin["mine"]["userAdmin"] != true || admin["theirs"]["userAdmin"] != nil {
		t.Fatalf("admin view: mine=%v theirs=%v", admin["mine"]["userAdmin"], admin["theirs"]["userAdmin"])
	}
	user := nodes(2, 1)
	if len(user) != 1 || user["theirs"] == nil {
		t.Fatalf("user nodes: %v", user)
	}
	if _, ok := user["theirs"]["userAdmin"]; ok {
		t.Fatalf("user view has userAdmin: %v", user["theirs"])
	}
}

// TestLandingRegionOverrideMigration (plan 286): a database from before the
// column gets landing_server.region_override (default ”) on SQLite and
// PostgreSQL (FLVX_POSTGRES_TEST_DSN), and the repository methods work.
func TestLandingRegionOverrideMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			db := r.DB()
			if !db.Migrator().HasColumn(&model.LandingServer{}, "region_override") {
				t.Fatal("fresh schema has no region_override")
			}
			// Back to the pre-286 schema with one server row.
			if err := db.Migrator().DropColumn(&model.LandingServer{}, "region_override"); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`INSERT INTO landing_server (id, source, node_id, remote_server_id, name, public_host, port_min, port_max, flow_type, traffic_ratio, status, protocols, reality_dest, reality_server_names, created_time, updated_time)
				VALUES (7, 'shared', 0, 70, 'old', '203.0.113.7', 30000, 30010, 2, 1, 'active', '["ss2022"]', 'a.example:443', '["a.example"]', 1, 1)`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.LandingServer{}); err != nil {
				t.Fatal(err)
			}
			s, err := r.GetLandingServer(7)
			if err != nil || s == nil || s.RegionOverride != "" {
				t.Fatalf("migrated row: %+v %v", s, err)
			}
			if ok, err := r.SetLandingServerRegionOverride(7, "jp"); err != nil || !ok {
				t.Fatalf("set: %v %v", ok, err)
			}
			if s, _ = r.GetLandingServer(7); s.RegionOverride != "JP" {
				t.Fatalf("stored %q", s.RegionOverride)
			}
			if _, err := r.SetLandingServerRegionOverride(7, "J1"); err == nil {
				t.Fatal("bad code accepted")
			}
			if err := db.Create(&model.Node{ID: 8, Name: "n8", Secret: "s8", ServerIP: "127.0.0.1", Port: "1000-2000", CreatedTime: 1, Status: 1, Region: "tw"}).Error; err != nil {
				t.Fatal(err)
			}
			regions, err := r.NodeRegions([]int64{8, 9})
			if err != nil || regions[8] != "TW" || regions[9] != "" {
				t.Fatalf("node regions %v %v", regions, err)
			}
		})
	}
}
