package contract_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/usersub"
)

// Plan 303 round 3 (user 11-24 evening): a user is NEVER handed the node's
// main domain. Only through the API, the kicker and the reconcile loop (as
// in production), SQLite + PostgreSQL, fake Cloudflare:
//   - a forward created on a toggled node → his name is reserved in the same
//     request: the very next forward list already shows the final
//     <label>.imgamer.top with 「正在就绪」, the subscription already has it;
//     the record is created and verified by GET → the tag goes away
//   - zone at the record cap → the name gets no record: no address at all
//     (forward list notice 「名额已满」, subscription omits the entry, copy
//     link refuses with the reason), admin ⚠ with the name and the reason;
//     room again → created → ready
//   - Cloudflare create failing → 正在就绪 through the first retries, then
//     「创建失败」 with no address; recovery → ready
//   - at every step no user-facing endpoint and none of the 13 subscription
//     formats contains the main domain or the node IP.
func TestUserSubdomainReadyContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runUserSubdomainReady(t, dialect) })
	}
}

func runUserSubdomainReady(t *testing.T, dialect string) {
	r := landingContractRepo(t, dialect)
	db := r.DB()
	now := time.Now().UnixMilli()
	const mainDomain, v6 = "vmsilo-huri.imgamer.top", "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4"
	key := base64.StdEncoding.EncodeToString(make([]byte, 16))
	entry := model.Node{ID: 51, Name: "vmsilo 沪日首发", Secret: "n51-ready-secret", ServerIP: mainDomain, ServerIPV6: sql.NullString{String: mainDomain, Valid: true},
		Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]", DDNSEnabled: true, DDNSProviderID: 1}
	mustSeed := func(rows ...interface{}) {
		t.Helper()
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatalf("seed %T: %v", row, err)
			}
		}
	}
	mustSeed(&entry,
		&model.Node{ID: 61, Name: "Bestvm JP B", Secret: "n61-ready", ServerIP: "203.0.113.61", Port: "10000-65535", CreatedTime: now, Status: 1},
		&model.NodeDDNSRecord{NodeID: 51, Domain: mainDomain, RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: "rec001", CurrentIP: v6, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now},
	)
	tunnel := model.Tunnel{Name: "沪日首发 -> JP", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1,
		InIP: sql.NullString{String: mainDomain, Valid: true}}
	mustSeed(&tunnel)
	mustSeed(&model.ChainTunnel{TunnelID: tunnel.ID, ChainType: "1", NodeID: 51, Port: sql.NullInt64{Int64: 20001, Valid: true},
		Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	users := map[int64]string{801: "ns-robot", 802: "ns-lingmeng", 803: "liuchen", 804: "ns-saorana"}
	for id, name := range users {
		mustSeed(&model.User{ID: id, User: name, Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
			&model.UserTunnel{UserID: id, TunnelID: tunnel.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 30*86400000, Status: 1})
	}
	// ns-robot also has landings: a native landing on JP B reached through
	// the toggled entry, and a native landing on 沪日首发 itself (直连).
	mustSeed(
		&model.LandingServer{ID: 11, Source: "native", NodeID: 61, Name: "JP B", PublicHost: "203.0.113.61", FlowType: 2, TrafficRatio: 1, Status: "active"},
		&model.LandingServer{ID: 12, Source: "native", NodeID: 51, Name: "沪日首发 落地", PublicHost: mainDomain, FlowType: 2, TrafficRatio: 1, Status: "active"},
		&model.LandingNode{ID: 22, UserID: 801, LandingServerID: 11, Name: "游戏加速", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-22", Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key},
		&model.LandingNode{ID: 23, UserID: 801, LandingServerID: 12, Name: "沪日直连", Protocol: "ss2022", Port: 30010, InboundTag: "flvxl-23", Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key},
	)
	// The others each own a landing node on JP B; their new forwards target
	// it (subscription items are landing / chain / external forwards).
	landingOf := map[int64]int64{802: 32, 803: 33, 804: 34}
	for uid, id := range landingOf {
		mustSeed(&model.LandingNode{ID: id, UserID: uid, LandingServerID: 11, Name: "落地 " + users[uid], Protocol: "ss2022", Port: int(30000 + id),
			InboundTag: "flvxl-" + jsonOf(id), Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key},
			&model.LandingServerUser{LandingServerID: 11, UserID: uid, CreatedTime: now})
	}
	mustSeed(&model.LandingServerUser{LandingServerID: 11, UserID: 801, CreatedTime: now}, &model.LandingServerUser{LandingServerID: 12, UserID: 801, CreatedTime: now})
	viaFwd := model.Forward{UserID: 801, UserName: "ns-robot", Name: "游戏加速", TunnelID: tunnel.ID, RemoteAddr: "203.0.113.61:30002", Strategy: "fifo",
		CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost", LandingNodeID: 22}
	mustSeed(&viaFwd)
	mustSeed(&model.ForwardPort{ForwardID: viaFwd.ID, NodeID: 51, Port: 20500})

	fake := newUSFakeCF()
	api := httptest.NewServer(fake)
	defer api.Close()
	const secret = "user-subdomain-ready"
	h := handler.New(r, secret, "test")
	h.SetMutationRateLimitEnabled(false)
	h.SetDDNSAPIBase(api.URL)
	h.SetSubPullBurst(100000) // 13 formats × many stages
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	defer server.Close()
	stopNode := startMockNodeSession(t, server.URL, entry.Secret)
	defer stopNode()
	// A realistic debounce for ordinary kicks: a reserved name must not wait
	// it out (fast kick).
	stopLoop := h.StartUserSubdomainLoop(2 * time.Second)
	defer stopLoop()

	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	tok := map[int64]string{}
	for id, name := range users {
		tok[id], _ = auth.GenerateToken(id, name, 1, secret)
	}
	call := func(token, path string, body interface{}) (int, string) {
		t.Helper()
		out := requestContractEnvelope(t, router, token, "/api/v1/"+path, body)
		raw, _ := json.Marshal(out)
		return out.Code, string(raw)
	}
	nameOf := func(uid int64) model.UserSubdomain {
		t.Helper()
		var rows []model.UserSubdomain
		db.Where("user_id = ? AND state <> ?", uid, "retired").Order("id").Find(&rows)
		if len(rows) != 1 {
			t.Fatalf("user %d: want one name, got %+v", uid, rows)
		}
		return rows[0]
	}
	waitFor := func(what string, cond func() bool) time.Duration {
		t.Helper()
		start := time.Now()
		for time.Since(start) < 10*time.Second {
			if cond() {
				return time.Since(start)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: not within 10 s", what)
		return 0
	}
	type fwdItem struct {
		ID            int64                  `json:"id"`
		Name          string                 `json:"name"`
		InIP          string                 `json:"inIp"`
		EntryNameTags map[string]string      `json:"entryNameTags"`
		EntryNotices  []repo.UserEntryNotice `json:"entryNotices"`
	}
	forwardItem := func(token, name string) fwdItem {
		t.Helper()
		var out struct {
			Data struct {
				Items []fwdItem `json:"items"`
			} `json:"data"`
		}
		_, raw := call(token, "forward/list", map[string]interface{}{})
		_ = json.Unmarshal([]byte(raw), &out)
		for _, it := range out.Data.Items {
			if it.Name == name {
				return it
			}
		}
		t.Fatalf("forward %q not in the list: %.600s", name, raw)
		return fwdItem{}
	}
	createForward := func(uid int64, name string) {
		t.Helper()
		if c, raw := call(tok[uid], "forward/create", map[string]interface{}{"tunnelId": tunnel.ID, "name": name, "strategy": "fifo", "landingNodeId": landingOf[uid]}); c != 0 {
			t.Fatalf("forward/create %s: %s", name, raw)
		}
	}
	// The 13 client formats (same list as the round-2 prod comparison).
	uas := []string{"clash-verge/v2.2.3", "Stash/2.7", "sing-box 1.14.4", "NekoBox/Android/1.4.2", "Hiddify/2.5", "v2rayN/7.10",
		"v2rayNG/1.9", "Shadowrocket/2070", "Surge iOS/3100", "Surfboard/2.24", "Loon/3.2", "Quantumult%20X/1.5", "okhttp/4.12"}
	pull := func(token, ua string) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/s/"+token, nil)
		req.RemoteAddr = "203.0.113.90:5555"
		req.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("pull %s: %d %s", ua, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.HasPrefix(ua, "Shadowrocket") || strings.HasPrefix(ua, "v2rayN") {
			if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body)); err == nil {
				body = string(dec)
			}
		}
		return body
	}
	subTokens := map[int64]string{}
	makeSub := func(uid int64) {
		t.Helper()
		// Pick every rule link the editor offers (as the UI does).
		_, raw := call(tok[uid], "sub/pool", map[string]interface{}{})
		var pool struct {
			Data struct {
				Rules []struct {
					Ref map[string]interface{} `json:"ref"`
				} `json:"rules"`
				Landings []struct {
					Ref map[string]interface{} `json:"ref"`
				} `json:"landings"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(raw), &pool)
		items := []interface{}{}
		for _, x := range pool.Data.Rules {
			items = append(items, x.Ref)
		}
		for _, x := range pool.Data.Landings {
			items = append(items, x.Ref)
		}
		c, raw := call(tok[uid], "sub/create", map[string]interface{}{"name": users[uid], "items": items})
		var created struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(raw), &created)
		if c != 0 || created.Data.Token == "" {
			t.Fatalf("sub/create %d: %s (pool %s)", uid, raw, raw)
		}
		subTokens[uid] = created.Data.Token
	}
	leaks := []string{mainDomain, v6, "240e:96c:7100:1fe:1c8:c8aa:4ecc:00e4"}
	// sweep: every user-facing endpoint that may carry an entry address, and
	// every subscription format, for one user: no main domain, no node IP.
	sweep := func(stage string, uid int64) {
		t.Helper()
		bodies := map[string]string{}
		for _, ep := range []struct {
			path string
			body map[string]interface{}
		}{
			{"forward/list", nil}, {"user/package", nil}, {"tunnel/user/tunnel", nil}, {"user/my-subscription", nil},
			{"sub/list", nil}, {"sub/pool", nil}, {"landing/node/list", nil}, {"landing/server/list", nil},
			{"landing/user/get", nil}, {"landing/external/list", nil}, {"order/list", nil}, {"package/list", nil},
			{"landing/node/links", map[string]interface{}{"id": 22}}, {"landing/node/links", map[string]interface{}{"id": 23}},
		} {
			body := ep.body
			if body == nil {
				body = map[string]interface{}{}
			}
			_, raw := call(tok[uid], ep.path, body)
			bodies[ep.path+jsonOf(body)] = raw
		}
		if token := subTokens[uid]; token != "" {
			for _, ua := range uas {
				bodies["/s/ "+ua] = pull(token, ua)
			}
		}
		for where, body := range bodies {
			for _, leak := range leaks {
				if strings.Contains(strings.ToLower(body), leak) {
					t.Errorf("%s: user %d %s shows %s: %.700s", stage, uid, where, leak, body)
				}
			}
		}
	}

	// Toggle on through the node editor (the request itself reserves names).
	if c, raw := call(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-303-abcdef"}); c != 0 {
		t.Fatalf("provider: %s", raw)
	}
	if c, raw := call(admin, "node/update", map[string]interface{}{"id": 51, "name": "vmsilo 沪日首发",
		"serverIpV4": "", "serverIpV6": mainDomain, "port": "20000-20999", "tcpListenAddr": "[::]", "udpListenAddr": "[::]",
		"ddns": map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": true, "userSubdomainKeep": false, "records": []interface{}{
			map[string]interface{}{"domain": mainDomain, "type": "AAAA", "role": "main", "source": "ct"},
		}}}); c != 0 {
		t.Fatalf("toggle on: %s", raw)
	}
	robot := nameOf(801) // reserved in the node/update request
	waitFor("ns-robot ready", func() bool { return nameOf(801).Live() })
	makeSub(801)
	sweep("toggle on", 801)
	links := map[string]string{}
	for _, id := range []int{22, 23} {
		_, raw := call(tok[801], "landing/node/links", map[string]interface{}{"id": id})
		links[jsonOf(id)] = raw
	}
	if !strings.Contains(links["22"], robot.Domain) || !strings.Contains(links["23"], robot.Domain) {
		t.Fatalf("landing copy links do not use the name: %v", links)
	}
	// A forward to his native landing on 沪日首发 itself: the target shown
	// to him is his name too.
	// (Seeded: the API refuses a mainland landing target; a toggled node
	// outside the mainland would take it.)
	toSelf := model.Forward{UserID: 801, UserName: "ns-robot", Name: "经沪日首发到沪日直连", TunnelID: tunnel.ID, RemoteAddr: mainDomain + ":30010", Strategy: "fifo",
		CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost", LandingNodeID: 23}
	mustSeed(&toSelf)
	mustSeed(&model.ForwardPort{ForwardID: toSelf.ID, NodeID: 51, Port: 20501})
	if it := forwardItem(tok[801], toSelf.Name); !strings.HasPrefix(it.InIP, robot.Domain+":") {
		t.Fatalf("forward to landing 23: %+v", it)
	}
	sweep("forward to a landing on the toggled node", 801)

	// 1. New forward while Cloudflare is slow: the name is final at once.
	fake.mu.Lock()
	fake.gate = make(chan struct{})
	getsBefore := fake.gets
	fake.mu.Unlock()
	t0 := time.Now()
	createForward(802, "lingmeng-日")
	created := time.Now()
	ling := nameOf(802) // same request
	if ling.RecordID != "" || ling.ReadyAt != 0 || ling.Live() || !strings.HasSuffix(ling.Domain, ".imgamer.top") || ling.Domain == mainDomain {
		t.Fatalf("reserved name: %+v", ling)
	}
	it := forwardItem(tok[802], "lingmeng-日")
	if !strings.HasPrefix(it.InIP, ling.Domain+":") || it.EntryNameTags[ling.Domain] != "pending" || len(it.EntryNotices) != 0 {
		t.Fatalf("正在就绪 not shown: %+v", it)
	}
	if it := forwardItem(admin, "lingmeng-日"); !strings.HasPrefix(it.InIP, ling.Domain+":") || it.EntryNameTags[ling.Domain] != "pending" {
		t.Fatalf("admin view while 正在就绪: %+v", it)
	}
	makeSub(802)
	pendingSubs := map[string]string{}
	for _, ua := range uas {
		body := pull(subTokens[802], ua)
		if !strings.Contains(body, ling.Domain) {
			t.Errorf("正在就绪/%s: the subscription lacks the final name\n%s", ua, body)
		}
		pendingSubs[ua] = body
	}
	sweep("正在就绪", 802)
	// Cloudflare's API answers, but its DNS edge serves the name only a few
	// seconds later (measured ~3 s on imgamer.top): 「正在就绪」 stays until
	// the zone's authoritative servers answer.
	var served atomic.Bool
	h.SetUserSubdomainDNSCheck(func(name string) bool { return name != ling.Domain || served.Load() })
	close(fake.gate)
	fake.mu.Lock()
	fake.gate = nil
	fake.mu.Unlock()
	waitFor("record created", func() bool { return len(fake.byName(ling.Domain)) == 1 && nameOf(802).Live() })
	time.Sleep(300 * time.Millisecond)
	if it := forwardItem(tok[802], "lingmeng-日"); it.EntryNameTags[ling.Domain] != "pending" || nameOf(802).ReadyAt != 0 {
		t.Fatalf("tag gone before the authoritative DNS answered: %+v", it)
	}
	served.Store(true)
	readyIn := waitFor("tag gone", func() bool {
		it := forwardItem(tok[802], "lingmeng-日")
		return it.EntryNameTags[ling.Domain] == "" && strings.HasPrefix(it.InIP, ling.Domain+":")
	})
	got := nameOf(802)
	fake.mu.Lock()
	verified := fake.gets > getsBefore
	fake.mu.Unlock()
	if !got.Live() || got.ReadyAt == 0 || got.Domain != ling.Domain || len(fake.byName(ling.Domain)) != 1 || !verified {
		t.Fatalf("after Cloudflare answered: %+v (record %v, verified %v)", got, fake.byName(ling.Domain), verified)
	}
	for _, ua := range uas {
		if body := pull(subTokens[802], ua); body != pendingSubs[ua] {
			t.Errorf("ready/%s: the subscription changed between 正在就绪 and ready", ua)
		}
	}
	sweep("ready", 802)
	t.Logf("create → name shown %v; authoritative DNS answered → tag gone %v later (fake CF, in process)",
		created.Sub(t0).Round(time.Millisecond), readyIn.Round(time.Millisecond))

	// 2. Zone at the record cap: no record → no address at all.
	fake.mu.Lock()
	fake.quota = len(fake.recs) + usersub.CapReserve
	fake.mu.Unlock()
	if c, raw := call(admin, "node/user-subdomain/sync", map[string]interface{}{}); c != 0 { // full pass: re-reads the quota
		t.Fatalf("sync: %s", raw)
	}
	createForward(803, "沪日")
	waitFor("capped", func() bool { return nameOf(803).Status == "capped" })
	liu := nameOf(803)
	if !usersub.Hidden(liu) || len(fake.byName(liu.Domain)) != 0 {
		t.Fatalf("capped name: %+v", liu)
	}
	it = forwardItem(tok[803], "沪日")
	if it.InIP != "" || len(it.EntryNotices) != 1 || it.EntryNotices[0].Tag != "capped" || it.EntryNotices[0].Domain != "" || it.EntryNotices[0].Reason != "" {
		t.Fatalf("capped for the user: %+v", it)
	}
	if it := forwardItem(admin, "沪日"); it.InIP != "" || len(it.EntryNotices) != 1 || it.EntryNotices[0].Domain != liu.Domain ||
		!strings.Contains(it.EntryNotices[0].Reason, "记录数已到上限") {
		t.Fatalf("capped for the admin: %+v", it)
	}
	if _, raw := call(admin, "node/list", map[string]interface{}{}); !strings.Contains(raw, `"capped":1`) || !strings.Contains(raw, `"attention":true`) {
		t.Fatalf("node pill without the cap ⚠: %.1200s", raw)
	}
	if _, raw := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 51}); !strings.Contains(raw, `"hidden":true`) || !strings.Contains(raw, `"capped":1`) {
		t.Fatalf("node dialog without the capped name: %.1500s", raw)
	}
	var liuFwd model.Forward
	db.Where("user_id = 803").First(&liuFwd)
	if _, err := r.ForwardEntryAddresses(liuFwd.ID); err == nil || err.Error() != "入口域名名额已满，请联系管理员" {
		t.Fatalf("copy link while capped: %v", err)
	}
	makeSub(803)
	for _, ua := range uas {
		if body := pull(subTokens[803], ua); strings.Contains(body, liu.Domain) {
			t.Errorf("capped/%s: the withheld name is in the subscription", ua)
		}
	}
	sweep("capped", 803)
	// Room again → the same name, 正在就绪 → ready.
	fake.mu.Lock()
	fake.quota = 200
	fake.mu.Unlock()
	if c, raw := call(admin, "node/user-subdomain/sync", map[string]interface{}{}); c != 0 {
		t.Fatalf("sync: %s", raw)
	}
	waitFor("capped name created", func() bool { return nameOf(803).Live() })
	makeSub(803) // the editor offers the entry again
	if it := forwardItem(tok[803], "沪日"); !strings.HasPrefix(it.InIP, liu.Domain+":") || len(it.EntryNotices) != 0 || it.EntryNameTags[liu.Domain] != "" {
		t.Fatalf("after the cap freed: %+v", it)
	}
	if body := pull(subTokens[803], "clash-verge/v2.2.3"); !strings.Contains(body, liu.Domain) {
		t.Fatalf("subscription after the cap freed lacks the name:\n%s", body)
	}
	sweep("cap freed", 803)

	// 3. Cloudflare create failing: 正在就绪 through the first retries, then
	// 「创建失败」 without an address; recovery → ready.
	fake.mu.Lock()
	fake.failCreates = 1000
	fake.mu.Unlock()
	// An ordinary mutation first (starts a 2 s debounce), then the forward.
	if c, raw := call(admin, "user/update", map[string]interface{}{"id": 801, "user": "ns-robot", "flow": 99999, "num": 99,
		"expTime": now + 30*86400000, "flowResetTime": 1, "status": 1}); c != 0 {
		t.Fatalf("user/update: %s", raw)
	}
	createForward(804, "日本")
	if took := waitFor("first failure", func() bool { return nameOf(804).FailCount >= 1 }); took > 1500*time.Millisecond {
		t.Fatalf("the reserved name waited out the ordinary debounce: first Cloudflare attempt after %v", took)
	}
	sao := nameOf(804)
	if usersub.Hidden(sao) {
		t.Fatalf("hidden after one failure: %+v", sao)
	}
	if it := forwardItem(tok[804], "日本"); !strings.HasPrefix(it.InIP, sao.Domain+":") || it.EntryNameTags[sao.Domain] != "pending" {
		t.Fatalf("after one failure the user should still see 正在就绪: %+v", it)
	}
	for nameOf(804).FailCount < usersub.FailShow {
		call(admin, "node/user-subdomain/sync", map[string]interface{}{}) // retry now (the admin's 立即同步)
	}
	sao = nameOf(804)
	if !usersub.Hidden(sao) || sao.Status != "failed" {
		t.Fatalf("not withheld after %d failures: %+v", usersub.FailShow, sao)
	}
	it = forwardItem(tok[804], "日本")
	if it.InIP != "" || len(it.EntryNotices) != 1 || it.EntryNotices[0].Tag != "failed" {
		t.Fatalf("创建失败 for the user: %+v", it)
	}
	if it := forwardItem(admin, "日本"); len(it.EntryNotices) != 1 || it.EntryNotices[0].Domain != sao.Domain || it.EntryNotices[0].Reason == "" {
		t.Fatalf("创建失败 for the admin: %+v", it)
	}
	makeSub(804)
	sweep("create failed", 804)
	fake.mu.Lock()
	fake.failCreates = 0
	fake.mu.Unlock()
	call(admin, "node/user-subdomain/sync", map[string]interface{}{})
	waitFor("recovered", func() bool { return nameOf(804).Live() })
	if it := forwardItem(tok[804], "日本"); !strings.HasPrefix(it.InIP, sao.Domain+":") || len(it.EntryNotices) != 0 {
		t.Fatalf("after recovery: %+v", it)
	}
	sweep("recovered", 804)

	// Nothing the panel did not write was touched; every name is unique.
	for _, n := range []string{"vmsilo-huri.imgamer.top", "vmsilo-humei.imgamer.top", "mmwx.imgamer.top", "flvxx.imgamer.top"} {
		if recs := fake.byName(n); len(recs) != 1 || recs[0]["comment"] == "flvx-user" {
			t.Fatalf("foreign record %s touched: %+v", n, recs)
		}
	}
	var labels []string
	db.Model(&model.UserSubdomain{}).Pluck("label", &labels)
	seen := map[string]bool{}
	for _, l := range labels {
		if seen[l] {
			t.Fatalf("label reused: %s", l)
		}
		seen[l] = true
	}
	for _, uid := range []int64{801, 802, 803, 804} {
		sweep("end", uid)
	}
}

func jsonOf(v interface{}) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
