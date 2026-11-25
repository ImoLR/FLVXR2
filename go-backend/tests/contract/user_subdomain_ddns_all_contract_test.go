package contract_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 307 (user 11-25): DDNS + 用户专属域名 on every node, node addresses
// kept. Through the API, the kicker and the reconcile loop, SQLite +
// PostgreSQL, fake Cloudflare:
//   - DDNS with 「保持节点地址」 + 「固定 IP」: records written with the typed
//     IP (also for an offline node), node rows / tunnels untouched, no agent
//     command, status 已同步; a name we do not own (dmitnathan, mmwx's)
//     stays untouched
//   - toggle on with 「已有转发保持原地址」: 0 names, everything byte-identical
//   - pause / resume / batch redeploy / batch pause+resume of a kept forward
//     and 停用 / 启用 of a kept landing node: still kept, byte-identical
//   - saving the edit dialog of a kept forward → the owner's name (正在就绪
//     until ready); the other user stays byte-identical
//   - saving the edit dialog of a kept landing node → the owner's name there
//   - a node whose address is a hostname we do not own (Dmit Nathan:
//     dmitnathan.imgamer.top): a new forward shows the user's name, CNAME to
//     our record, never the shared hostname.
func TestUserSubdomainDDNSAllNodesContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runUserSubdomainDDNSAll(t, dialect) })
	}
}

func runUserSubdomainDDNSAll(t *testing.T, dialect string) {
	r := landingContractRepo(t, dialect)
	db := r.DB()
	now := time.Now().UnixMilli()
	const dwIP, natIP, usIP = "103.97.175.50", "103.117.101.148", "155.254.127.160"
	const dwName, natName, usName, natShared = "datawave-hk2.imgamer.top", "dmit-nathan.imgamer.top", "bestvm-us.imgamer.top", "dmitnathan.imgamer.top"
	fake := newUSFakeCF()
	fake.mu.Lock()
	sharedRec := fake.add("A", natShared, natIP, "") // mmwx DDNS owns it
	fake.mu.Unlock()
	key := base64.StdEncoding.EncodeToString(make([]byte, 16))
	mustSeed := func(rows ...interface{}) {
		t.Helper()
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatalf("seed %T: %v", row, err)
			}
		}
	}
	dw := model.Node{ID: 15, Name: "Datawave HK 2", Secret: "n15-307", ServerIP: dwIP, ServerIPV4: sql.NullString{String: dwIP, Valid: true},
		Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	nat := model.Node{ID: 49, Name: "Dmit Nathan", Secret: "n49-307", ServerIP: natShared, ServerIPV4: sql.NullString{String: natShared, Valid: true},
		Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	us := model.Node{ID: 17, Name: "Bestvm US", Secret: "n17-307", ServerIP: usIP, ServerIPV4: sql.NullString{String: usIP, Valid: true},
		Port: "10000-29999,40000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	mustSeed(&dw, &nat, &us)
	mkTunnel := func(name, inIP string, nodeID int64, port int64) model.Tunnel {
		tn := model.Tunnel{Name: name, TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1,
			InIP: sql.NullString{String: inIP, Valid: true}}
		mustSeed(&tn)
		mustSeed(&model.ChainTunnel{TunnelID: tn.ID, ChainType: "1", NodeID: nodeID, Port: sql.NullInt64{Int64: port, Valid: true},
			Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}})
		return tn
	}
	tDW := mkTunnel("港 Datawave", dwIP, 15, 20001)
	tNat := mkTunnel("美 Nathan", natShared, 49, 20002)
	users := map[int64]string{80: "ns-pccly", 81: "ns-hajimi", 90: "newbie"}
	for id, name := range users {
		mustSeed(&model.User{ID: id, User: name, Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1})
		for _, tn := range []model.Tunnel{tDW, tNat} {
			mustSeed(&model.UserTunnel{UserID: id, TunnelID: tn.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 30*86400000, Status: 1})
		}
	}
	mustSeed(&model.LandingServer{ID: 9, Source: "native", NodeID: 17, Name: "US", PublicHost: usIP, FlowType: 2, TrafficRatio: 1, Status: "active"})
	landing := func(id, uid int64) {
		mustSeed(&model.LandingNode{ID: id, UserID: uid, LandingServerID: 9, Name: fmt.Sprintf("落地 %d", id), Protocol: "ss2022", Port: int(30000 + id - 196),
			InboundTag: fmt.Sprintf("flvxl-%d", id), Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key, CreatedTime: now - 86400000, UpdatedTime: now})
	}
	landing(197, 80)
	landing(199, 81)
	landing(230, 90)
	for uid := range users {
		mustSeed(&model.LandingServerUser{LandingServerID: 9, UserID: uid, CreatedTime: now})
	}
	oldFwd := func(uid int64, name string, tn model.Tunnel, nodeID int64, landingID int64, port int) model.Forward {
		f := model.Forward{UserID: uid, UserName: users[uid], Name: name, TunnelID: tn.ID, RemoteAddr: fmt.Sprintf("%s:%d", usIP, 30000+landingID-196),
			Strategy: "fifo", CreatedTime: now - 86400000, UpdatedTime: now, Status: 1, Mode: "gost", LandingNodeID: landingID}
		mustSeed(&f)
		mustSeed(&model.ForwardPort{ForwardID: f.ID, NodeID: nodeID, Port: port})
		return f
	}
	pcclyDW := oldFwd(80, "us vless", tDW, 15, 197, 41001)
	hajimiDW := oldFwd(81, "dmit->bestvm", tDW, 15, 199, 41002)
	hajimiNat := oldFwd(81, "nathan us", tNat, 49, 199, 41003)

	api := httptest.NewServer(fake)
	defer api.Close()
	const secret = "user-subdomain-307"
	h := handler.New(r, secret, "test")
	h.SetMutationRateLimitEnabled(false)
	h.SetDDNSAPIBase(api.URL)
	h.SetSubPullBurst(100000)
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	defer server.Close()
	var cmdMu sync.Mutex
	cmds := []string{}
	record := func(node string) func(string) {
		return func(cmd string) { cmdMu.Lock(); cmds = append(cmds, node+" "+cmd); cmdMu.Unlock() }
	}
	stopDW := startMockNodeSessionWithHook(t, server.URL, dw.Secret, record("15"))
	defer stopDW()
	stopNat := startMockNodeSessionWithHook(t, server.URL, nat.Secret, record("49"))
	defer stopNat()
	// Bestvm US stays offline: its 固定 IP record must sync anyway.
	stopLoop := h.StartUserSubdomainLoop(2 * time.Second)
	defer stopLoop()
	commands := func() []string { cmdMu.Lock(); defer cmdMu.Unlock(); return append([]string(nil), cmds...) }

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
	mustOK := func(token, path string, body interface{}) string {
		t.Helper()
		c, raw, _ := call(token, path, body)
		if c != 0 {
			t.Fatalf("%s: %s", path, raw)
		}
		return raw
	}
	names := func(where string, args ...interface{}) []model.UserSubdomain {
		var rows []model.UserSubdomain
		db.Where(where, args...).Order("id").Find(&rows)
		return rows
	}
	keepRows := func(kind string, ref int64) int64 {
		var n int64
		db.Model(&model.UserSubdomainKeep{}).Where("kind = ? AND ref_id = ?", kind, ref).Count(&n)
		return n
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
		ID            int64             `json:"id"`
		InIP          string            `json:"inIp"`
		RemoteAddr    string            `json:"remoteAddr"`
		EntryNameTags map[string]string `json:"entryNameTags"`
		EntryKept     []int64           `json:"entryKeptNodes"`
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
		return w.Body.String()
	}
	makeSub := func(uid int64, label string) string {
		t.Helper()
		_, raw, _ := call(tok[uid], "sub/pool", map[string]interface{}{})
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
		if len(items) == 0 {
			t.Fatalf("sub %s: empty pool %s", label, raw)
		}
		_, raw, _ = call(tok[uid], "sub/create", map[string]interface{}{"name": label, "items": items})
		var created struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal([]byte(raw), &created)
		if created.Data.Token == "" {
			t.Fatalf("sub/create %s: %s", label, raw)
		}
		return created.Data.Token
	}
	// addrs: what a user is handed (no timestamps / status fields).
	addrs := func(uid int64, landingIDs []int64, subs ...string) map[string]string {
		t.Helper()
		out := map[string]string{}
		var list struct {
			Data struct {
				Items []fwdItem `json:"items"`
			} `json:"data"`
		}
		_, raw, _ := call(tok[uid], "forward/list", map[string]interface{}{})
		_ = json.Unmarshal([]byte(raw), &list)
		for _, it := range list.Data.Items {
			b, _ := json.Marshal(it)
			out[fmt.Sprint("forward ", it.ID)] = string(b)
		}
		for _, path := range []string{"sub/pool", "landing/server/list"} {
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
		if len(a) != len(b) {
			t.Errorf("%s: %d views before, %d after", stage, len(a), len(b))
		}
	}
	nodeRow := func(id int64) string {
		var n model.Node
		db.Where("id = ?", id).First(&n)
		return fmt.Sprintf("%s|%s|%s|%s|%s", n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String, n.ExtraIPs.String, n.PanelIPFamily)
	}
	tunnelRows := func() string {
		var ts []model.Tunnel
		db.Order("id").Find(&ts)
		var b strings.Builder
		for _, x := range ts {
			fmt.Fprintf(&b, "%d=%s;", x.ID, x.InIP.String)
		}
		return b.String()
	}

	mustOK(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-307-abcdef"})
	subPccly, subHajimi := makeSub(80, "pccly"), makeSub(81, "hajimi")
	base80 := addrs(80, []int64{197}, subPccly)
	base81 := addrs(81, []int64{199}, subHajimi)
	if !strings.Contains(base81[fmt.Sprint("forward ", hajimiNat.ID)], natShared+":41003") {
		t.Fatalf("baseline Nathan forward: %v", base81)
	}
	rows0 := map[int64]string{15: nodeRow(15), 49: nodeRow(49), 17: nodeRow(17)}
	tunnels0 := tunnelRows()

	// The node dialog payload (no panelIpFamily: the stored value stays).
	nodeSave := func(n model.Node, recName, ip string, toggle bool, extra map[string]interface{}) (int, string) {
		ddns := map[string]interface{}{"enabled": true, "providerId": 1, "keepAddress": true, "userSubdomain": toggle,
			"records": []interface{}{map[string]interface{}{"domain": recName, "type": "A", "role": "main", "source": "static", "prefix": ip}}}
		for k, v := range extra {
			ddns[k] = v
		}
		c, raw, _ := call(admin, "node/update", map[string]interface{}{"id": n.ID, "name": n.Name, "serverIpV4": n.ServerIPV4.String,
			"serverIpV6": "", "extraIPs": "", "port": n.Port, "tcpListenAddr": "[::]", "udpListenAddr": "[::]", "ddns": ddns})
		return c, raw
	}
	reconcile := func() { h.WaitDDNS(); h.UserSubdomainReconcileNow(true) }

	// 1. DDNS on with 「保持节点地址」 + 「固定 IP」 (toggle still off).
	cmdsBefore := len(commands())
	for _, x := range []struct {
		n        model.Node
		name, ip string
	}{{dw, dwName, dwIP}, {nat, natName, natIP}, {us, usName, usIP}} {
		if c, raw := nodeSave(x.n, x.name, x.ip, false, nil); c != 0 {
			t.Fatalf("DDNS on node %d: %s", x.n.ID, raw)
		}
	}
	reconcile()
	for _, x := range [][2]string{{dwName, dwIP}, {natName, natIP}, {usName, usIP}} {
		recs := fake.byName(x[0])
		if len(recs) != 1 || recs[0]["type"] != "A" || recs[0]["content"] != x[1] || recs[0]["comment"] != "FLVX DDNS" {
			t.Fatalf("record %s: %+v", x[0], recs)
		}
	}
	if recs := fake.byName(natShared); len(recs) != 1 || recs[0]["id"] != sharedRec || recs[0]["content"] != natIP || recs[0]["comment"] != "" {
		t.Fatalf("shared name touched: %+v", recs)
	}
	for id, want := range rows0 {
		if got := nodeRow(id); got != want {
			t.Fatalf("node %d address changed: %s → %s", id, want, got)
		}
	}
	if got := tunnelRows(); got != tunnels0 {
		t.Fatalf("tunnels rewritten: %s → %s", tunnels0, got)
	}
	var recRows []model.NodeDDNSRecord
	db.Order("node_id").Find(&recRows)
	for _, rec := range recRows {
		if rec.Status != model.DDNSStatusSynced || rec.Applied || rec.Source != "static" || rec.CurrentIP == "" {
			t.Fatalf("record row: %+v", rec)
		}
	}
	var nodes []map[string]interface{}
	_, raw, _ := call(admin, "node/list", map[string]interface{}{})
	var nl struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal([]byte(raw), &nl)
	nodes = nl.Data
	for _, n := range nodes {
		d, _ := n["ddns"].(map[string]interface{})
		if d == nil {
			continue
		}
		if d["status"] != "synced" || d["keepAddress"] != true {
			t.Fatalf("node %v DDNS view: %+v", n["id"], d)
		}
	}
	same("DDNS on (toggle off)", base80, addrs(80, []int64{197}, subPccly))
	same("DDNS on (toggle off)", base81, addrs(81, []int64{199}, subHajimi))

	// 2. 用户专属域名 on, 「已有转发保持原地址」 default (on): nothing moves.
	for _, x := range []struct {
		n        model.Node
		name, ip string
	}{{dw, dwName, dwIP}, {nat, natName, natIP}, {us, usName, usIP}} {
		if c, raw := nodeSave(x.n, x.name, x.ip, true, nil); c != 0 {
			t.Fatalf("toggle on node %d: %s", x.n.ID, raw)
		}
	}
	reconcile()
	if rows := names("1 = 1"); len(rows) != 0 {
		t.Fatalf("names made for kept forwards / landings: %+v", rows)
	}
	if keepRows("forward", pcclyDW.ID) != 1 || keepRows("forward", hajimiNat.ID) != 1 || keepRows("landing", 197) != 1 || keepRows("landing", 230) != 1 {
		t.Fatalf("keep set incomplete")
	}
	same("toggle on (kept)", base80, addrs(80, []int64{197}, subPccly))
	same("toggle on (kept)", base81, addrs(81, []int64{199}, subHajimi))
	for id, want := range rows0 {
		if got := nodeRow(id); got != want {
			t.Fatalf("node %d address changed on toggle: %s → %s", id, want, got)
		}
	}
	if got := commands()[cmdsBefore:]; len(got) != 0 {
		t.Fatalf("agent commands during DDNS / toggle: %v", got)
	}

	// 3. Not an edit: pause / resume, batch redeploy, batch pause + resume,
	// landing 停用 / 启用.
	mustOK(tok[80], "forward/pause", map[string]interface{}{"id": pcclyDW.ID})
	mustOK(tok[80], "forward/resume", map[string]interface{}{"id": pcclyDW.ID})
	mustOK(admin, "forward/batch-redeploy", map[string]interface{}{"ids": []int64{pcclyDW.ID, hajimiDW.ID}})
	mustOK(admin, "forward/batch-pause", map[string]interface{}{"ids": []int64{hajimiDW.ID}})
	mustOK(admin, "forward/batch-resume", map[string]interface{}{"ids": []int64{hajimiDW.ID}})
	mustOK(admin, "landing/node/update", map[string]interface{}{"id": 197, "status": "disabled"})
	mustOK(admin, "landing/node/update", map[string]interface{}{"id": 197, "status": "active"})
	reconcile()
	if rows := names("1 = 1"); len(rows) != 0 {
		t.Fatalf("names after pause / redeploy / status ops: %+v", rows)
	}
	if keepRows("forward", pcclyDW.ID) != 1 || keepRows("forward", hajimiDW.ID) != 1 || keepRows("landing", 197) != 1 {
		t.Fatalf("keep rows dropped by a non-edit")
	}
	same("pause / resume / redeploy (kept)", base80, addrs(80, []int64{197}, subPccly))
	same("pause / resume / redeploy (kept)", base81, addrs(81, []int64{199}, subHajimi))

	// 4. ns-pccly saves the edit dialog of his kept forward → his name.
	mustOK(tok[80], "forward/update", map[string]interface{}{"id": pcclyDW.ID, "name": "us vless 改", "tunnelId": tDW.ID, "strategy": "fifo", "landingNodeId": 197})
	if keepRows("forward", pcclyDW.ID) != 0 {
		t.Fatalf("edited forward still kept")
	}
	rows := names("user_id = 80 AND state = ?", "active")
	if len(rows) != 1 || rows[0].NodeID != 15 {
		t.Fatalf("pccly name after the edit: %+v", rows)
	}
	pcName := rows[0].Domain
	it := forwardItem(tok[80], pcclyDW.ID)
	if it.InIP != pcName+":41001" || (it.EntryNameTags[pcName] != "" && it.EntryNameTags[pcName] != "pending") {
		t.Fatalf("edited forward address: %+v (name %s)", it, pcName)
	}
	if it.RemoteAddr != fmt.Sprintf("%s:30001", usIP) {
		t.Fatalf("target of the edited forward (landing still kept): %+v", it)
	}
	waitFor("pccly name ready", func() bool {
		rs := names("user_id = 80 AND state = ?", "active")
		return len(rs) == 1 && rs[0].ReadyAt > 0
	})
	if cn := fake.userCNAMEs(); cn[pcName] != dwName {
		t.Fatalf("CNAME of the edited forward's name: %+v", cn)
	}
	if it := forwardItem(tok[80], pcclyDW.ID); it.InIP != pcName+":41001" || len(it.EntryNameTags) != 0 {
		t.Fatalf("edited forward once ready: %+v", it)
	}
	if it := forwardItem(admin, hajimiDW.ID); len(it.EntryKept) != 1 || it.EntryKept[0] != 15 {
		t.Fatalf("admin tag on the untouched kept forward: %+v", it)
	}
	same("pccly edit (hajimi untouched)", base81, addrs(81, []int64{199}, subHajimi))
	// The landing's own 直连 link stays (landing still kept); its forward
	// link is the edited forward → the name.
	_, _, links := call(tok[80], "landing/node/links", map[string]interface{}{"id": 197})
	if !strings.Contains(links, "@"+usIP+":30001#") || !strings.Contains(links, "@"+pcName+":41001#") || strings.Contains(links, dwIP) {
		t.Fatalf("links 197 after the forward edit: %.900s", links)
	}

	// 5. Admin saves the edit dialog of ns-pccly's kept landing node.
	// The dialog sends the node's fields back (api/landing.ts updateLandingNode).
	var ln model.LandingNode
	db.Where("id = 197").First(&ln)
	mustOK(admin, "landing/node/update", map[string]interface{}{"id": 197, "name": "落地 197 改", "maxConnections": ln.MaxConnections,
		"maxOnlineIps": ln.MaxOnlineIPs, "trafficLimitBytes": ln.TrafficLimitBytes, "flowType": ln.FlowType, "trafficRatio": ln.TrafficRatio,
		"expiryMode": ln.ExpiryMode, "expiresAt": ln.ExpiresAt, "status": "active"})
	if keepRows("landing", 197) != 0 {
		t.Fatalf("edited landing node still kept")
	}
	rows = names("user_id = 80 AND node_id = 17 AND state = ?", "active")
	if len(rows) != 1 {
		t.Fatalf("pccly name on Bestvm US: %+v", names("user_id = 80"))
	}
	usUser := rows[0].Domain
	waitFor("pccly US name ready", func() bool {
		rs := names("user_id = 80 AND node_id = 17 AND state = ?", "active")
		return len(rs) == 1 && rs[0].ReadyAt > 0
	})
	_, _, links = call(tok[80], "landing/node/links", map[string]interface{}{"id": 197})
	if !strings.Contains(links, usUser) || strings.Contains(links, usIP) || strings.Contains(links, usName) {
		t.Fatalf("edited landing links: %.900s", links)
	}
	if it := forwardItem(tok[80], pcclyDW.ID); it.RemoteAddr != usUser+":30001" {
		t.Fatalf("forward target after the landing edit: %+v", it)
	}
	same("pccly landing edit (hajimi untouched)", base81, addrs(81, []int64{199}, subHajimi))

	// 6. A new forward on Dmit Nathan (address = a name we do not own).
	mustOK(tok[90], "forward/create", map[string]interface{}{"tunnelId": tNat.ID, "name": "newbie nathan", "strategy": "fifo", "landingNodeId": 230})
	var nb model.Forward
	db.Where("user_id = 90 AND name = ?", "newbie nathan").First(&nb)
	rows = names("user_id = 90 AND state = ?", "active")
	if len(rows) != 1 || rows[0].NodeID != 49 {
		t.Fatalf("newbie names: %+v", rows)
	}
	nbName := rows[0].Domain
	waitFor("newbie name ready", func() bool {
		rs := names("user_id = 90 AND state = ?", "active")
		return len(rs) == 1 && rs[0].ReadyAt > 0
	})
	var nbPort model.ForwardPort
	db.Where("forward_id = ?", nb.ID).First(&nbPort)
	if it := forwardItem(tok[90], nb.ID); it.InIP != fmt.Sprintf("%s:%d", nbName, nbPort.Port) {
		t.Fatalf("new Nathan forward: %+v", it)
	}
	if cn := fake.userCNAMEs(); cn[nbName] != natName {
		t.Fatalf("newbie CNAME: %+v", cn)
	}
	for _, path := range []string{"forward/list", "user/package", "sub/pool"} {
		_, raw, _ := call(tok[90], path, map[string]interface{}{})
		for _, leak := range []string{natShared, natName, natIP} {
			if strings.Contains(raw, leak) {
				t.Errorf("newbie %s shows %s: %.700s", path, leak, raw)
			}
		}
	}
	if recs := fake.byName(natShared); len(recs) != 1 || recs[0]["content"] != natIP || recs[0]["comment"] != "" {
		t.Fatalf("shared name touched at the end: %+v", recs)
	}
	for id, want := range rows0 {
		if got := nodeRow(id); got != want {
			t.Fatalf("node %d address changed at the end: %s → %s", id, want, got)
		}
	}
	// hajimi's kept Nathan forward still shows the shared name it had.
	if it := forwardItem(tok[81], hajimiNat.ID); it.InIP != natShared+":41003" {
		t.Fatalf("kept Nathan forward: %+v", it)
	}
}
