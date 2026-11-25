package contract_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 305 (user 11-25): 用户专属域名 on a node with several DDNS records
// (Leikwanhost 沪台IPLC: 电信 leikwan-hutai-ct AAAA main, 联通 leikwan-hutai-cu
// AAAA extra, tunnels list both + LeiKwan's own paid-v4.bgp1.leikwanhost.com)
// and 已有转发保持原地址. Only through the API, the kicker and the reconcile
// loop, SQLite + PostgreSQL, fake Cloudflare:
//   - toggle on with 「已有转发保持原地址」 (default): no name is made, every
//     existing forward keeps its address — forward lists, copy links and all
//     13 subscription formats byte-identical to before
//   - a new forward by an existing user → that user gets one name per DDNS
//     record (two CNAMEs, each to its own record); the new forward shows his
//     电信 name for -ct, his 联通 name for -cu, the vendor domain unchanged;
//     nothing of our node domains / IPs in any of his outputs for it; his
//     old (kept) forward and its subscription stay byte-identical
//   - the -ct prefix changes → names unchanged, CNAMEs untouched
//   - delete + recreate a kept forward = a new forward (names)
//   - toggle on again with the box off = full migration.
func TestUserSubdomainMultiRecordContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runUserSubdomainMultiRecord(t, dialect) })
	}
}

func runUserSubdomainMultiRecord(t *testing.T, dialect string) {
	r := landingContractRepo(t, dialect)
	db := r.DB()
	now := time.Now().UnixMilli()
	const ct, cu, vendor = "leikwan-hutai-ct.imgamer.top", "leikwan-hutai-cu.imgamer.top", "paid-v4.bgp1.leikwanhost.com"
	const ctIP, cuIP, ctIP2 = "240e:b8f:b07:4004:ca:8aff:fec0:2813", "2408:820c:7508:34d4:ca:8aff:fec0:2813", "240e:b8f:a30:1904:ca:8aff:fec0:2813"
	fake := newUSFakeCF()
	fake.mu.Lock()
	ctRec := fake.add("AAAA", ct, ctIP, "FLVX DDNS")
	cuRec := fake.add("AAAA", cu, cuIP, "FLVX DDNS")
	fake.mu.Unlock()
	key := base64.StdEncoding.EncodeToString(make([]byte, 16))
	entry := model.Node{ID: 48, Name: "Leikwanhost 沪台IPLC", Secret: "n48-multi-secret", ServerIP: ct, ServerIPV6: sql.NullString{String: ct, Valid: true},
		ExtraIPs: sql.NullString{String: cu, Valid: true}, Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]",
		DDNSEnabled: true, DDNSProviderID: 1}
	mustSeed := func(rows ...interface{}) {
		t.Helper()
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatalf("seed %T: %v", row, err)
			}
		}
	}
	mustSeed(&entry,
		&model.Node{ID: 61, Name: "Bestvm JP B", Secret: "n61-multi", ServerIP: "203.0.113.61", Port: "10000-65535", CreatedTime: now, Status: 1},
		&model.NodeDDNSRecord{NodeID: 48, Domain: ct, RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: ctRec, CurrentIP: ctIP, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 0},
		&model.NodeDDNSRecord{NodeID: 48, Domain: cu, RecordType: "AAAA", Role: "extra", Source: "cu", ZoneID: "z1", RecordID: cuRec, CurrentIP: cuIP, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 1},
	)
	tunnel := model.Tunnel{Name: "拼车hinet3 利群沪台IPLC", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1,
		InIP: sql.NullString{String: ct + "," + cu + "," + vendor, Valid: true}}
	mustSeed(&tunnel)
	mustSeed(&model.ChainTunnel{TunnelID: tunnel.ID, ChainType: "1", NodeID: 48, Port: sql.NullInt64{Int64: 20001, Valid: true},
		Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
	users := map[int64]string{8: "@jiuwei", 60: "usb2", 70: "newbie"}
	for id, name := range users {
		mustSeed(&model.User{ID: id, User: name, Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
			&model.UserTunnel{UserID: id, TunnelID: tunnel.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 30*86400000, Status: 1})
	}
	mustSeed(&model.LandingServer{ID: 11, Source: "native", NodeID: 61, Name: "JP B", PublicHost: "203.0.113.61", FlowType: 2, TrafficRatio: 1, Status: "active"})
	landing := func(id, uid int64) {
		mustSeed(&model.LandingNode{ID: id, UserID: uid, LandingServerID: 11, Name: fmt.Sprintf("落地 %d", id), Protocol: "ss2022", Port: int(30000 + id),
			InboundTag: fmt.Sprintf("flvxl-%d", id), Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key, CreatedTime: now, UpdatedTime: now})
	}
	for _, x := range [][2]int64{{105, 8}, {133, 60}, {134, 60}, {135, 60}, {170, 70}} {
		landing(x[0], x[1])
	}
	for uid := range users {
		mustSeed(&model.LandingServerUser{LandingServerID: 11, UserID: uid, CreatedTime: now})
	}
	// The forwards on 沪台 before the toggle (as on prod: fwd 156 / 347).
	oldFwd := func(uid int64, name string, landingID int64, port int) model.Forward {
		f := model.Forward{UserID: uid, UserName: users[uid], Name: name, TunnelID: tunnel.ID, RemoteAddr: fmt.Sprintf("203.0.113.61:%d", 30000+landingID),
			Strategy: "fifo", CreatedTime: now - 86400000, UpdatedTime: now, Status: 1, Mode: "gost", LandingNodeID: landingID}
		mustSeed(&f)
		mustSeed(&model.ForwardPort{ForwardID: f.ID, NodeID: 48, Port: port})
		return f
	}
	jiuweiTW := oldFwd(8, "TW", 105, 25706)
	usbOld := oldFwd(60, "沪台iplc中转", 133, 40778)

	api := httptest.NewServer(fake)
	defer api.Close()
	const secret = "user-subdomain-multi"
	h := handler.New(r, secret, "test")
	h.SetMutationRateLimitEnabled(false)
	h.SetDDNSAPIBase(api.URL)
	h.SetSubPullBurst(100000)
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	defer server.Close()
	stopNode := startMockNodeSession(t, server.URL, entry.Secret)
	defer stopNode()
	stopLoop := h.StartUserSubdomainLoop(2 * time.Second)
	defer stopLoop()

	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	tok := map[int64]string{}
	for id, name := range users {
		tok[id], _ = auth.GenerateToken(id, name, 1, secret)
	}
	call := func(token, path string, body interface{}) (int, string, string) {
		t.Helper()
		out := requestContractEnvelope(t, router, token, "/api/v1/"+path, body)
		raw, _ := json.Marshal(out)
		data, _ := json.Marshal(out.Data)
		return out.Code, string(raw), string(data)
	}
	names := func(where string, args ...interface{}) []model.UserSubdomain {
		var rows []model.UserSubdomain
		db.Where(where, args...).Order("id").Find(&rows)
		return rows
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		start := time.Now()
		for time.Since(start) < 15*time.Second {
			if cond() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("%s: not within 15 s", what)
	}
	type fwdItem struct {
		ID             int64             `json:"id"`
		Name           string            `json:"name"`
		InIP           string            `json:"inIp"`
		EntryNameTags  map[string]string `json:"entryNameTags"`
		EntryKeptNodes []int64           `json:"entryKeptNodes"`
	}
	forwardItem := func(token string, id int64) fwdItem {
		t.Helper()
		var out struct {
			Data struct {
				Items []fwdItem `json:"items"`
			} `json:"data"`
		}
		_, raw, _ := call(token, "forward/list", map[string]interface{}{})
		_ = json.Unmarshal([]byte(raw), &out)
		for _, it := range out.Data.Items {
			if it.ID == id {
				return it
			}
		}
		t.Fatalf("forward %d not in the list: %.600s", id, raw)
		return fwdItem{}
	}
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
	// makeSub: a subscription of the user's rule links (only those of the
	// given forwards when ids is set).
	makeSub := func(uid int64, label string, ids ...int64) string {
		t.Helper()
		_, raw, _ := call(tok[uid], "sub/pool", map[string]interface{}{})
		var pool struct {
			Data struct {
				Rules []struct {
					Ref map[string]interface{} `json:"ref"`
				} `json:"rules"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(raw), &pool)
		want := map[int64]bool{}
		for _, id := range ids {
			want[id] = true
		}
		items := []interface{}{}
		for _, x := range pool.Data.Rules {
			fid, _ := x.Ref["forwardId"].(float64)
			if len(ids) == 0 || want[int64(fid)] {
				items = append(items, x.Ref)
			}
		}
		if len(items) == 0 {
			t.Fatalf("sub %s: no items in pool %s", label, raw)
		}
		c, raw, _ := call(tok[uid], "sub/create", map[string]interface{}{"name": label, "items": items})
		var created struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(raw), &created)
		if c != 0 || created.Data.Token == "" {
			t.Fatalf("sub/create %s: %s", label, raw)
		}
		return created.Data.Token
	}
	// snapshot: what a user sees of his entries (data only, no envelope ts).
	snapshot := func(uid int64, landingIDs []int64, subs ...string) map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, path := range []string{"forward/list", "user/package", "sub/pool", "landing/node/list", "landing/server/list"} {
			_, _, data := call(tok[uid], path, map[string]interface{}{})
			out[path] = data
		}
		for _, id := range landingIDs {
			_, _, data := call(tok[uid], "landing/node/links", map[string]interface{}{"id": id})
			out[fmt.Sprint("links ", id)] = data
		}
		for _, s := range subs {
			for _, ua := range uas {
				out["/s/"+s[:6]+" "+ua] = pull(s, ua)
			}
		}
		return out
	}
	same := func(stage string, a, b map[string]string) {
		t.Helper()
		keys := make([]string, 0, len(a))
		for k := range a {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if a[k] != b[k] {
				t.Errorf("%s: %s changed\nbefore %.900s\nafter  %.900s", stage, k, a[k], b[k])
			}
		}
	}
	ourHosts := []string{ct, cu, ctIP, cuIP, ctIP2, "240e:b8f:b07:4004:ca:8aff:fec0:2813", "2408:820c:7508:34d4"}
	noLeak := func(stage, where, body string) {
		t.Helper()
		for _, h := range ourHosts {
			if strings.Contains(strings.ToLower(body), h) {
				t.Errorf("%s: %s shows %s: %.700s", stage, where, h, body)
			}
		}
	}

	if c, raw, _ := call(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-305-abcdef"}); c != 0 {
		t.Fatalf("provider: %s", raw)
	}
	subJiuwei := makeSub(8, "jiuwei")
	subUsb := makeSub(60, "usb2")
	baseJiuwei := snapshot(8, []int64{105}, subJiuwei)
	baseUsb := snapshot(60, []int64{133}, subUsb)
	if !strings.Contains(baseUsb["forward/list"], ct+":40778,"+cu+":40778,"+vendor+":40778") {
		t.Fatalf("baseline forward list: %.700s", baseUsb["forward/list"])
	}

	nodeSave := func(on bool, keep interface{}) (int, string) {
		ddns := map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": on, "records": []interface{}{
			map[string]interface{}{"domain": ct, "type": "AAAA", "role": "main", "source": "ct"},
			map[string]interface{}{"domain": cu, "type": "AAAA", "role": "extra", "source": "cu"},
		}}
		if keep != nil {
			ddns["userSubdomainKeep"] = keep
		}
		c, raw, _ := call(admin, "node/update", map[string]interface{}{"id": 48, "name": entry.Name,
			"serverIpV4": "", "serverIpV6": ct, "extraIPs": cu, "port": "10000-65535", "tcpListenAddr": "[::]", "udpListenAddr": "[::]", "ddns": ddns})
		return c, raw
	}
	reconcile := func() { h.WaitDDNS(); h.UserSubdomainReconcileNow(true) }

	// 1. Toggle on, 「已有转发保持原地址」 left at its default (on).
	if c, raw := nodeSave(true, nil); c != 0 {
		t.Fatalf("toggle on: %s", raw)
	}
	reconcile()
	if rows := names("1 = 1"); len(rows) != 0 {
		t.Fatalf("names made for kept forwards: %+v", rows)
	}
	var keep []model.UserSubdomainKeep
	db.Order("ref_id").Find(&keep)
	if len(keep) != 2 || keep[0].RefID != jiuweiTW.ID || keep[1].RefID != usbOld.ID || keep[0].Kind != "forward" || keep[1].RefCreated != usbOld.CreatedTime {
		t.Fatalf("keep set: %+v", keep)
	}
	same("toggle on (kept)", baseJiuwei, snapshot(8, []int64{105}, subJiuwei))
	same("toggle on (kept)", baseUsb, snapshot(60, []int64{133}, subUsb))
	if it := forwardItem(admin, usbOld.ID); len(it.EntryKeptNodes) != 1 || it.EntryKeptNodes[0] != 48 || it.InIP != ct+":40778,"+cu+":40778,"+vendor+":40778" {
		t.Fatalf("admin kept tag: %+v", it)
	}
	if it := forwardItem(tok[60], usbOld.ID); len(it.EntryKeptNodes) != 0 {
		t.Fatalf("user sees the admin tag: %+v", it)
	}
	_, raw, _ := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 48})
	for _, want := range []string{`"label":"电信"`, `"label":"联通"`, `"domain":"` + cu + `"`, `"keptForwards":[{"forwardId":`, `"name":"沪台iplc中转"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("admin node list lacks %s: %.1500s", want, raw)
		}
	}
	_, raw, _ = call(admin, "node/list", map[string]interface{}{})
	if !strings.Contains(raw, `"kept":2`) {
		t.Fatalf("node pill kept count: %.1500s", raw)
	}

	// 2. usb2 (existing user) creates a new forward → one name per record.
	if c, raw, _ := call(tok[60], "forward/create", map[string]interface{}{"tunnelId": tunnel.ID, "name": "沪台新", "strategy": "fifo", "landingNodeId": 134}); c != 0 {
		t.Fatalf("forward/create: %s", raw)
	}
	var usbNew model.Forward
	db.Where("user_id = 60 AND name = ?", "沪台新").First(&usbNew)
	rows := names("user_id = 60 AND state = ?", "active")
	if len(rows) != 2 || rows[0].RecordDomain != "" || rows[1].RecordDomain != cu || rows[0].Domain == rows[1].Domain {
		t.Fatalf("usb2 names: %+v", rows)
	}
	waitFor("usb2 names ready", func() bool {
		rs := names("user_id = 60 AND state = ?", "active")
		return len(rs) == 2 && rs[0].Live() && rs[1].Live() && rs[0].ReadyAt > 0 && rs[1].ReadyAt > 0
	})
	rows = names("user_id = 60 AND state = ?", "active")
	telecom, unicom := rows[0], rows[1]
	cn := fake.userCNAMEs()
	if len(cn) != 2 || cn[telecom.Domain] != ct || cn[unicom.Domain] != cu {
		t.Fatalf("CNAMEs: %+v (电信 %s 联通 %s)", cn, telecom.Domain, unicom.Domain)
	}
	var newPort model.ForwardPort
	db.Where("forward_id = ?", usbNew.ID).First(&newPort)
	p := fmt.Sprint(newPort.Port)
	if it := forwardItem(tok[60], usbNew.ID); it.InIP != telecom.Domain+":"+p+","+unicom.Domain+":"+p+","+vendor+":"+p || len(it.EntryNameTags) != 0 {
		t.Fatalf("new forward address: %+v", it)
	}
	if it := forwardItem(admin, usbNew.ID); len(it.EntryKeptNodes) != 0 || !strings.HasPrefix(it.InIP, telecom.Domain+":") {
		t.Fatalf("admin view of the new forward: %+v", it)
	}
	// The old forward, its links and the old subscription: unchanged.
	if it := forwardItem(tok[60], usbOld.ID); it.InIP != ct+":40778,"+cu+":40778,"+vendor+":40778" {
		t.Fatalf("kept forward moved: %+v", it)
	}
	after := snapshot(60, []int64{133}, subUsb)
	for _, k := range []string{"links 133"} {
		if after[k] != baseUsb[k] {
			t.Errorf("kept forward %s changed:\n%s\n%s", k, baseUsb[k], after[k])
		}
	}
	for _, ua := range uas {
		k := "/s/" + subUsb[:6] + " " + ua
		if after[k] != baseUsb[k] {
			t.Errorf("old subscription %s changed:\n%s\n%s", ua, baseUsb[k], after[k])
		}
	}
	same("usb2 new forward (jiuwei untouched)", baseJiuwei, snapshot(8, []int64{105}, subJiuwei))
	// The new forward everywhere: his names, the vendor domain, none of ours.
	_, _, links134 := call(tok[60], "landing/node/links", map[string]interface{}{"id": 134})
	noLeak("new forward", "links 134", links134)
	if !strings.Contains(links134, telecom.Domain) || !strings.Contains(links134, unicom.Domain) || !strings.Contains(links134, vendor) {
		t.Fatalf("copy links of the new forward: %.900s", links134)
	}
	subNew := makeSub(60, "usb2-new", usbNew.ID)
	for _, ua := range uas {
		body := pull(subNew, ua)
		noLeak("new forward sub", ua, body)
		if !strings.Contains(body, telecom.Domain) || !strings.Contains(body, unicom.Domain) || !strings.Contains(body, vendor) {
			t.Errorf("new forward sub %s lacks a name / the vendor domain:\n%s", ua, body)
		}
	}
	x, _ := r.LoadUserEntryNames([]int64{60})
	if uh := x.ResolveForward(60, 48, usbNew.ID, cu); uh.Host != unicom.Domain || !uh.V6Only {
		t.Fatalf("resolve -cu: %+v", uh)
	}
	if uh := x.ResolveForward(60, 48, usbNew.ID, "["+cuIP+"]"); uh.Host != unicom.Domain {
		t.Fatalf("resolve the -cu IP: %+v", uh)
	}
	if uh := x.ResolveForward(60, 48, usbNew.ID, vendor); uh.Host != "" || uh.Hidden {
		t.Fatalf("vendor domain touched: %+v", uh)
	}
	if uh := x.ResolveForward(60, 48, usbOld.ID, ct); uh.Host != "" || uh.Hidden {
		t.Fatalf("kept forward resolved: %+v", uh)
	}
	_, raw, _ = call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 48})
	if !strings.Contains(raw, `"recordLabel":"联通"`) || !strings.Contains(raw, `"recordLabel":"电信"`) || !strings.Contains(raw, `"targetDomain":"`+cu+`"`) || !strings.Contains(raw, `"kept":true`) {
		t.Fatalf("admin name list labels: %.2000s", raw)
	}

	// 3. A user new on 沪台 gets two names too.
	if c, raw, _ := call(tok[70], "forward/create", map[string]interface{}{"tunnelId": tunnel.ID, "name": "newbie 沪台", "strategy": "fifo", "landingNodeId": 170}); c != 0 {
		t.Fatalf("newbie forward: %s", raw)
	}
	if rows := names("user_id = 70 AND state = ?", "active"); len(rows) != 2 || rows[1].RecordDomain != cu {
		t.Fatalf("newbie names: %+v", rows)
	}
	waitFor("newbie names ready", func() bool {
		rs := names("user_id = 70 AND state = ?", "active")
		return len(rs) == 2 && rs[0].ReadyAt > 0 && rs[1].ReadyAt > 0
	})
	// Every user-facing endpoint + all 13 formats: none of our node domains /
	// IPs (both records), the vendor domain kept.
	subNewbie := makeSub(70, "newbie")
	for _, ep := range []struct {
		path string
		body map[string]interface{}
	}{
		{"forward/list", nil}, {"user/package", nil}, {"tunnel/user/tunnel", nil}, {"user/my-subscription", nil},
		{"sub/list", nil}, {"sub/pool", nil}, {"landing/node/list", nil}, {"landing/server/list", nil},
		{"landing/user/get", nil}, {"landing/external/list", nil}, {"order/list", nil}, {"package/list", nil},
		{"landing/node/links", map[string]interface{}{"id": 170}},
	} {
		body := ep.body
		if body == nil {
			body = map[string]interface{}{}
		}
		_, raw, _ := call(tok[70], ep.path, body)
		noLeak("newbie", ep.path, raw)
	}
	for _, ua := range uas {
		body := pull(subNewbie, ua)
		noLeak("newbie sub", ua, body)
		if !strings.Contains(body, vendor) {
			t.Errorf("newbie sub %s lost the vendor entry:\n%s", ua, body)
		}
	}

	// 4. The 电信 prefix changes (SLAAC): names and CNAMEs stay, the new IP
	// maps to the 电信 name.
	writes := func() int { fake.mu.Lock(); defer fake.mu.Unlock(); return fake.writes }
	before := writes()
	fake.mu.Lock()
	fake.recs[ctRec]["content"] = ctIP2
	fake.mu.Unlock()
	db.Model(&model.NodeDDNSRecord{}).Where("node_id = 48 AND domain = ?", ct).Update("current_ip", ctIP2)
	reconcile()
	rows = names("user_id = 60 AND state = ?", "active")
	if len(rows) != 2 || rows[0].Domain != telecom.Domain || rows[1].Domain != unicom.Domain || rows[0].RecordID != telecom.RecordID || writes() != before {
		t.Fatalf("after prefix change: %+v (writes %d → %d)", rows, before, writes())
	}
	if cn := fake.userCNAMEs(); cn[telecom.Domain] != ct || cn[unicom.Domain] != cu {
		t.Fatalf("CNAMEs after prefix change: %+v", cn)
	}
	x, _ = r.LoadUserEntryNames([]int64{60})
	if uh := x.ResolveForward(60, 48, usbNew.ID, ctIP2); uh.Host != telecom.Domain {
		t.Fatalf("new 电信 IP: %+v", uh)
	}

	// 5. Delete + recreate = a new forward (keep row pruned on the full pass).
	if c, raw, _ := call(tok[60], "forward/delete", map[string]interface{}{"id": usbOld.ID}); c != 0 {
		t.Fatalf("delete kept forward: %s", raw)
	}
	if c, raw, _ := call(tok[60], "forward/create", map[string]interface{}{"tunnelId": tunnel.ID, "name": "沪台iplc中转", "strategy": "fifo", "landingNodeId": 135}); c != 0 {
		t.Fatalf("recreate: %s", raw)
	}
	var recreated model.Forward
	db.Where("user_id = 60 AND name = ? AND landing_node_id = 135", "沪台iplc中转").First(&recreated)
	reconcile()
	if it := forwardItem(tok[60], recreated.ID); !strings.HasPrefix(it.InIP, telecom.Domain+":") {
		t.Fatalf("recreated forward kept the old address: %+v", it)
	}
	var keepLeft int64
	db.Model(&model.UserSubdomainKeep{}).Count(&keepLeft)
	if keepLeft != 1 {
		t.Fatalf("keep rows after delete: %d", keepLeft)
	}

	// 6. Toggle off, then on with the box off = full migration.
	if c, raw := nodeSave(false, nil); c != 0 {
		t.Fatalf("toggle off: %s", raw)
	}
	reconcile()
	if it := forwardItem(tok[8], jiuweiTW.ID); it.InIP != ct+":25706,"+cu+":25706,"+vendor+":25706" {
		t.Fatalf("toggle off: %+v", it)
	}
	if c, raw := nodeSave(true, false); c != 0 {
		t.Fatalf("toggle on (no keep): %s", raw)
	}
	reconcile()
	db.Model(&model.UserSubdomainKeep{}).Count(&keepLeft)
	jw := names("user_id = 8 AND state = ?", "active")
	if keepLeft != 0 || len(jw) != 2 {
		t.Fatalf("full migration: keep %d names %+v", keepLeft, jw)
	}
	waitFor("jiuwei names ready", func() bool {
		rs := names("user_id = 8 AND state = ?", "active")
		return len(rs) == 2 && rs[0].ReadyAt > 0 && rs[1].ReadyAt > 0
	})
	if it := forwardItem(tok[8], jiuweiTW.ID); it.InIP != jw[0].Domain+":25706,"+jw[1].Domain+":25706,"+vendor+":25706" {
		t.Fatalf("migrated forward: %+v", it)
	}
	for _, ua := range uas {
		noLeak("full migration", ua, pull(subJiuwei, ua))
	}
}
