package contract_test

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 321 (user 11-28, wings): removing a user's last grant on a node left
// his per-server row (用户设置 → 服务器) and his 专属域名 on that node behind.
// Through the router (real JWTs), the kicker and the reconcile loop, SQLite +
// PostgreSQL, fake Cloudflare:
//   - forward deleted, grants left → names + row stay, the admin list says
//     which grants they come from
//   - one of two grants on the node removed → still kept (the other one)
//   - the last grant removed → names retired (no_access) before the response,
//     CF records deleted by the pass, the row gone; other nodes untouched
//   - tunnel delete and user-group removal do the same; 停用 grants keep them
//   - dry run / apply endpoint, second pass: nothing more (idempotent).
func TestUserAccessCleanupContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runUserAccessCleanup(t, dialect) })
	}
}

func runUserAccessCleanup(t *testing.T, dialect string) {
	r := landingContractRepo(t, dialect)
	db := r.DB()
	now := time.Now().UnixMilli()
	const ct, cu, vendor, nathan = "leikwan-hutai-ct.imgamer.top", "leikwan-hutai-cu.imgamer.top", "paid-v4.bgp1.leikwanhost.com", "dmit-nathan.imgamer.top"
	const ctIP, cuIP = "240e:b8f:b07:4004:ca:8aff:fec0:2813", "2408:820c:7508:34d4:ca:8aff:fec0:2813"
	fake := newUSFakeCF()
	fake.mu.Lock()
	ctRec := fake.add("AAAA", ct, ctIP, "FLVX DDNS")
	cuRec := fake.add("AAAA", cu, cuIP, "FLVX DDNS")
	nRec := fake.add("A", nathan, "45.32.10.49", "FLVX DDNS")
	fake.mu.Unlock()
	seed := func(rows ...interface{}) {
		t.Helper()
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatalf("seed %T: %v", row, err)
			}
		}
	}
	n48 := model.Node{ID: 48, Name: "Leikwanhost 沪台IPLC", Secret: "n48-access", ServerIP: ct, ServerIPV6: sql.NullString{String: ct, Valid: true},
		ExtraIPs: sql.NullString{String: cu, Valid: true}, Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]",
		DDNSEnabled: true, DDNSProviderID: 1}
	n49 := model.Node{ID: 49, Name: "Dmit Nathan", Secret: "n49-access", ServerIP: "45.32.10.49", Port: "10000-65535", CreatedTime: now, Status: 1,
		DDNSEnabled: true, DDNSProviderID: 1}
	seed(&n48, &n49,
		&model.Node{ID: 47, Name: "AWS HK", Secret: "n47-access", ServerIP: "203.0.113.47", Port: "10000-65535", CreatedTime: now, Status: 1},
		&model.NodeDDNSRecord{NodeID: 48, Domain: ct, RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: ctRec, CurrentIP: ctIP, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 0},
		&model.NodeDDNSRecord{NodeID: 48, Domain: cu, RecordType: "AAAA", Role: "extra", Source: "cu", ZoneID: "z1", RecordID: cuRec, CurrentIP: cuIP, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 1},
		&model.NodeDDNSRecord{NodeID: 49, Domain: nathan, RecordType: "A", Role: "main", Source: "static", SourcePrefix: "45.32.10.49", ZoneID: "z1", RecordID: nRec, CurrentIP: "45.32.10.49", TTL: 120, Applied: false, Status: "synced", LastSyncAt: now},
	)
	tunnel := func(name string, entry int64, exit int64, inIP string) model.Tunnel {
		t.Helper()
		typ := 1
		if exit > 0 {
			typ = 2
		}
		tn := model.Tunnel{Name: name, TrafficRatio: 1, Type: typ, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1,
			InIP: sql.NullString{String: inIP, Valid: true}}
		seed(&tn)
		seed(&model.ChainTunnel{TunnelID: tn.ID, ChainType: "1", NodeID: entry, Port: sql.NullInt64{Int64: 20001, Valid: true},
			Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
		if exit > 0 {
			seed(&model.ChainTunnel{TunnelID: tn.ID, ChainType: "3", NodeID: exit, Port: sql.NullInt64{Int64: 20002, Valid: true},
				Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
		}
		return tn
	}
	hutai := ct + "," + cu + "," + vendor
	tA := tunnel("拼车Hinet/Seednet 利群沪台IPLC", 48, 0, hutai)
	tB := tunnel("拼车hinet1 利群沪台IPLC", 48, 0, hutai)
	tC := tunnel("Dmit Nathan -> TW", 49, 47, nathan)
	tD := tunnel("拼车hinet3 利群沪台IPLC", 48, 0, hutai)
	tE := tunnel("沪台 组授权", 48, 0, hutai)
	users := map[int64]string{90: "wings", 91: "hutai-only", 92: "group-user"}
	for id, name := range users {
		seed(&model.User{ID: id, User: name, Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1})
	}
	grant := func(uid, tid int64) model.UserTunnel {
		ut := model.UserTunnel{UserID: uid, TunnelID: tid, Num: 99, Flow: 200, FlowResetTime: 1, ExpTime: now + 30*86400000, Status: 1}
		seed(&ut)
		return ut
	}
	utA, utB, utC := grant(90, tA.ID), grant(90, tB.ID), grant(90, tC.ID)
	grant(91, tD.ID)
	// User group 10 → tunnel group 9 (tE); user 92 joins through the API.
	seed(&model.TunnelGroup{ID: 9, Name: "沪台组", CreatedTime: now, UpdatedTime: now},
		&model.TunnelGroupTunnel{TunnelGroupID: 9, TunnelID: tE.ID, CreatedTime: now},
		&model.UserGroup{ID: 10, Name: "沪台用户", CreatedTime: now, UpdatedTime: now, Status: 1},
		&model.GroupPermission{UserGroupID: 10, TunnelGroupID: 9, CreatedTime: now})
	fwd := func(uid, tid, node int64, port int) model.Forward {
		f := model.Forward{UserID: uid, UserName: users[uid], Name: users[uid] + "-fwd", TunnelID: tid, RemoteAddr: "198.51.100.10:443",
			Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
		seed(&f)
		seed(&model.ForwardPort{ForwardID: f.ID, NodeID: node, Port: port})
		return f
	}
	f90a := fwd(90, tA.ID, 48, 24189)
	fwd(90, tC.ID, 49, 30001)
	fwd(91, tD.ID, 48, 25000)
	quota := func(uid, node int64, used int64) {
		seed(&model.UserServerQuota{UserID: uid, ServerKind: "node", ServerRefID: node, UsedIn: used, UsedOut: used, PeriodKey: 20261125, CreatedTime: now, UpdatedTime: now})
	}
	quota(90, 48, 13594)
	quota(90, 49, 19490)
	quota(90, 47, 9745)
	quota(91, 48, 100)
	quota(92, 48, 100)

	api := httptest.NewServer(fake)
	defer api.Close()
	const secret = "user-access-321"
	h := handler.New(r, secret, "test")
	h.SetMutationRateLimitEnabled(false)
	h.SetDDNSAPIBase(api.URL)
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	defer server.Close()
	stop48 := startMockNodeSession(t, server.URL, n48.Secret)
	defer stop48()
	stop49 := startMockNodeSession(t, server.URL, n49.Secret)
	defer stop49()
	stopLoop := h.StartUserSubdomainLoop(time.Second)
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
	mustOK := func(token, path string, body interface{}) string {
		t.Helper()
		c, raw := call(token, path, body)
		if c != 0 {
			t.Fatalf("%s: %s", path, raw)
		}
		return raw
	}
	names := func(uid, node int64) []model.UserSubdomain {
		var rows []model.UserSubdomain
		db.Where("user_id = ? AND node_id = ?", uid, node).Order("id").Find(&rows)
		return rows
	}
	states := func(rows []model.UserSubdomain) string {
		parts := []string{}
		for _, row := range rows {
			parts = append(parts, row.State+"/"+row.Reason)
		}
		return strings.Join(parts, ",")
	}
	quotaRow := func(uid, node int64) bool {
		var n int64
		db.Model(&model.UserServerQuota{}).Where("user_id = ? AND server_kind = 'node' AND server_ref_id = ?", uid, node).Count(&n)
		return n > 0
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
	reconcile := func() { h.WaitDDNS(); h.UserSubdomainReconcileNow(true) }

	mustOK(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-321-abcdef"})
	mustOK(admin, "node/update", map[string]interface{}{"id": 48, "name": n48.Name, "serverIpV4": "", "serverIpV6": ct, "extraIPs": cu, "port": "10000-65535",
		"tcpListenAddr": "[::]", "udpListenAddr": "[::]", "ddns": map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": true, "userSubdomainKeep": false,
			"records": []interface{}{map[string]interface{}{"domain": ct, "type": "AAAA", "role": "main", "source": "ct"}, map[string]interface{}{"domain": cu, "type": "AAAA", "role": "extra", "source": "cu"}}}})
	mustOK(admin, "node/update", map[string]interface{}{"id": 49, "name": n49.Name, "serverIpV4": "45.32.10.49", "port": "10000-65535",
		"ddns": map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": true, "userSubdomainKeep": false,
			"records": []interface{}{map[string]interface{}{"domain": nathan, "type": "A", "role": "main", "source": "static", "prefix": "45.32.10.49"}}}})
	// User 92 joins the group (grant created by the group) and gets a forward.
	mustOK(admin, "group/user/assign", map[string]interface{}{"groupId": 10, "userIds": []int64{92}})
	var ut92 model.UserTunnel
	if err := db.Where("user_id = ? AND tunnel_id = ?", 92, tE.ID).First(&ut92).Error; err != nil {
		t.Fatalf("group grant for 92: %v", err)
	}
	fwd(92, tE.ID, 48, 26000)
	reconcile()
	waitFor("names ready", func() bool {
		return len(fake.userCNAMEs()) == 7 && states(names(90, 48)) == "active/,active/" && states(names(90, 49)) == "active/"
	})
	wingsCT, wingsCU := names(90, 48)[0].Domain, names(90, 48)[1].Domain
	wingsNathan := names(90, 49)[0].Domain

	// B. Entry tags (plan 321) on the forward's names (admin and owner), none
	// on the vendor IPv4 name.
	labelsOf := func(token string, id int64) map[string]map[string]interface{} {
		t.Helper()
		var out struct {
			Data struct {
				Items []struct {
					ID          int64                             `json:"id"`
					InIP        string                            `json:"inIp"`
					EntryLabels map[string]map[string]interface{} `json:"entryLabels"`
				} `json:"items"`
			} `json:"data"`
		}
		raw := mustOK(token, "forward/list", map[string]interface{}{})
		_ = json.Unmarshal([]byte(raw), &out)
		for _, it := range out.Data.Items {
			if it.ID == id {
				return it.EntryLabels
			}
		}
		t.Fatalf("forward %d not listed: %.800s", id, raw)
		return nil
	}
	// (the admin's list shows the owner's names too, plan 303)
	if l := labelsOf(admin, f90a.ID); len(l) != 2 || l[wingsCT]["carrier"] != "电信" || l[wingsCT]["ipv6"] != true || l[wingsCU]["carrier"] != "联通" || l[vendor] != nil {
		t.Fatalf("admin entry labels: %+v", l)
	}
	if l := labelsOf(tok[90], f90a.ID); len(l) != 2 || l[wingsCT]["carrier"] != "电信" || l[wingsCU]["carrier"] != "联通" || l[wingsCU]["ipv6"] != true {
		t.Fatalf("user entry labels: %+v", l)
	}

	// 1. His forward on 沪台 deleted, both grants left: names + row stay, the
	// admin list names both grants.
	mustOK(tok[90], "forward/delete", map[string]interface{}{"id": f90a.ID})
	reconcile()
	if s := states(names(90, 48)); s != "active/,active/" || !quotaRow(90, 48) {
		t.Fatalf("after forward delete: names %s row %v", s, quotaRow(90, 48))
	}
	raw := mustOK(admin, "node/user-subdomain/list", map[string]interface{}{"userId": 90})
	for _, want := range []string{`"tunnelName":"` + tA.Name + `"`, `"tunnelName":"` + tB.Name + `"`, `"userTunnelId":` + strconv.FormatInt(utB.ID, 10),
		`"recordLabel":"联通"`, `"entryLabel":{"carrier":"电信","ipv6":true}`, `"entryLabel":{"carrier":"联通","ipv6":true}`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("admin list lacks %s: %.1500s", want, raw)
		}
	}
	raw = mustOK(admin, "landing/user/get", map[string]interface{}{"userId": 90})
	var got struct {
		Data struct {
			ServerGrants map[string][]struct {
				UserTunnelID int64 `json:"userTunnelId"`
				Entry        bool  `json:"entry"`
			} `json:"serverGrants"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(raw), &got)
	if g := got.Data.ServerGrants; len(g["node:48"]) != 2 || len(g["node:47"]) != 1 || g["node:47"][0].UserTunnelID != utC.ID || g["node:47"][0].Entry {
		t.Fatalf("serverGrants: %.2000s", raw)
	}

	// 2. One of the two 沪台 grants removed (the user's case at 01:20): kept.
	mustOK(admin, "tunnel/user/remove", map[string]interface{}{"id": utA.ID})
	if s := states(names(90, 48)); s != "active/,active/" || !quotaRow(90, 48) {
		t.Fatalf("after removing one grant: names %s row %v", s, quotaRow(90, 48))
	}
	// 停用 (batch) keeps them too: the grant stays, renewable.
	mustOK(admin, "tunnel/user/batch-update-status", map[string]interface{}{"ids": []int64{utB.ID}, "status": 2})
	if s := states(names(90, 48)); s != "active/,active/" || !quotaRow(90, 48) {
		t.Fatalf("after 停用: names %s row %v", s, quotaRow(90, 48))
	}

	// 3. The last 沪台 grant removed (01:30): retired before the response
	// returns, the row gone; Nathan (node 49) and the relay row stay.
	mustOK(admin, "tunnel/user/remove", map[string]interface{}{"id": utB.ID})
	if s := states(names(90, 48)); s != "retired/no_access,retired/no_access" {
		t.Fatalf("after the last grant: names %s", s)
	}
	if quotaRow(90, 48) || !quotaRow(90, 49) || !quotaRow(90, 47) {
		t.Fatalf("rows after the last grant: 48 %v 49 %v 47 %v", quotaRow(90, 48), quotaRow(90, 49), quotaRow(90, 47))
	}
	if s := states(names(90, 49)); s != "active/" {
		t.Fatalf("Nathan name: %s", s)
	}
	waitFor("CF records of the retired names deleted", func() bool {
		c := fake.userCNAMEs()
		_, a := c[wingsCT]
		_, b := c[wingsCU]
		_, n := c[wingsNathan]
		return !a && !b && n
	})
	raw = mustOK(admin, "landing/user/get", map[string]interface{}{"userId": 90})
	if strings.Contains(raw, `"serverRefId":48`) || strings.Contains(raw, `"node:48"`) {
		t.Fatalf("node 48 still in his settings: %.2000s", raw)
	}
	raw = mustOK(admin, "node/user-subdomain/list", map[string]interface{}{"userId": 90})
	if strings.Contains(raw, wingsCT) {
		t.Fatalf("retired name still listed: %.1500s", raw)
	}
	raw = mustOK(admin, "node/user-subdomain/list", map[string]interface{}{"userId": 90, "retired": true})
	if !strings.Contains(raw, `"reason":"no_access"`) {
		t.Fatalf("history lacks the reason: %.1500s", raw)
	}
	// Only our own records were touched (node DDNS records stay).
	if len(fake.byName(ct)) != 1 || len(fake.byName(cu)) != 1 || len(fake.byName(nathan)) != 1 {
		t.Fatalf("node DDNS records touched")
	}

	// 4. Tunnel delete: user 91's only access to 沪台.
	mustOK(admin, "tunnel/delete", map[string]interface{}{"id": tD.ID})
	if s := states(names(91, 48)); s != "retired/no_access,retired/no_access" || quotaRow(91, 48) {
		t.Fatalf("after tunnel delete: names %s row %v", s, quotaRow(91, 48))
	}

	// 5. User-group removal (grant created by the group).
	mustOK(admin, "group/user/assign", map[string]interface{}{"groupId": 10, "userIds": []int64{}})
	if s := states(names(92, 48)); s != "retired/no_access,retired/no_access" || quotaRow(92, 48) {
		t.Fatalf("after group removal: names %s row %v", s, quotaRow(92, 48))
	}
	waitFor("all retired records deleted", func() bool { return len(fake.userCNAMEs()) == 1 })

	// 6. Idempotent: dry run lists nothing, apply does nothing, a full pass
	// changes nothing.
	raw = mustOK(admin, "node/user-access/orphans", map[string]interface{}{})
	if !strings.Contains(raw, `"names":[]`) || !strings.Contains(raw, `"serverRows":[]`) {
		t.Fatalf("dry run after cleanup: %s", raw)
	}
	before := states(append(append(names(90, 48), names(91, 48)...), names(92, 48)...))
	reconcile()
	raw = mustOK(admin, "node/user-access/orphans", map[string]interface{}{"apply": true})
	if !strings.Contains(raw, `"retired":0`) || !strings.Contains(raw, `"pruned":0`) {
		t.Fatalf("apply after cleanup: %s", raw)
	}
	if after := states(append(append(names(90, 48), names(91, 48)...), names(92, 48)...)); after != before {
		t.Fatalf("second pass changed names: %s → %s", before, after)
	}

	// 7. An orphan left by an older panel (row without any need, name of a
	// user without access) shows in the dry run and goes with apply.
	quota(91, 47, 494)
	raw = mustOK(admin, "node/user-access/orphans", map[string]interface{}{})
	if !strings.Contains(raw, `"serverRefId":47`) || !strings.Contains(raw, `"userName":"hutai-only"`) {
		t.Fatalf("dry run misses the orphan row: %s", raw)
	}
	if !quotaRow(91, 47) {
		t.Fatalf("dry run deleted the row")
	}
	mustOK(admin, "node/user-access/orphans", map[string]interface{}{"apply": true})
	if quotaRow(91, 47) {
		t.Fatalf("apply left the orphan row")
	}
	// Users cannot call it.
	if c, _ := call(tok[90], "node/user-access/orphans", map[string]interface{}{}); c != 403 {
		t.Fatalf("user reached the admin endpoint: %d", c)
	}
}
