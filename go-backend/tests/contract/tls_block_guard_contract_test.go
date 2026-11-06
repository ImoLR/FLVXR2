package contract_test

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 224: a tunnel with 屏蔽 TLS (and 屏蔽 HTTP) refuses rules to landing
// nodes whose client traffic shows that feature; SS-type rules on it (prod
// forwards 156 「TW」 / 347 「沪台iplc中转」) and VLESS on tls=0 tunnels
// (Dmit Nathan 线路机) keep working; existing rules are never touched.
func TestTLSBlockGuardContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runTLSBlockGuard(t, dialect) })
	}
}

func runTLSBlockGuard(t *testing.T, dialect string) {
	const secret = "tls-guard-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	entry := model.Node{Name: "tg-entry", Secret: "tg-entry-secret", ServerIP: "10.61.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	landing := model.Node{Name: "tg-landing", Secret: "tg-landing-secret", ServerIP: "203.0.113.61", Port: "10000-19999", CreatedTime: now, LandingCapable: true}
	for _, n := range []*model.Node{&entry, &landing} {
		if err := r.DB().Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Prod shapes: 专线 tunnels have tls=1,http=1,socks=1; 线路机 tunnels
	// (Dmit Nathan) tls=0,http=1,socks=1.
	tunnel := func(name string, tls int) int64 {
		tu := model.Tunnel{Name: name, TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, HTTP: 1, TLS: tls, Socks: 1}
		if err := r.DB().Create(&tu).Error; err != nil {
			t.Fatal(err)
		}
		if err := r.DB().Create(&model.ChainTunnel{TunnelID: tu.ID, ChainType: "1", NodeID: entry.ID, Port: sql.NullInt64{Int64: 20001, Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
			t.Fatal(err)
		}
		return tu.ID
	}
	tlsTunnel, plainTunnel := tunnel("利群沪台IPLC", 1), tunnel("Dmit Nathan -> TW", 0)
	const uid = 100
	rows := []interface{}{
		&model.User{ID: uid, User: "tg_user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		&model.UserTunnel{UserID: uid, TunnelID: tlsTunnel, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.UserTunnel{UserID: uid, TunnelID: plainTunnel, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.LandingServer{ID: 1, Source: "native", NodeID: landing.ID, Name: "TW", PublicHost: "203.0.113.61", PortMin: 30000, PortMax: 39999, FlowType: 2, TrafficRatio: 1, Status: "active", Revision: 1, Protocols: `["ss2022","vless_reality_vision"]`, SnellObfs: "none", SnellVersion: 4, MieruTransport: "TCP"},
		&model.LandingServerUser{LandingServerID: 1, UserID: uid, CreatedTime: now},
	}
	nodes := map[string]model.LandingNode{
		"ss":         {Protocol: "ss2022"},
		"vless":      {Protocol: "vless_reality_vision"},
		"anytls":     {Protocol: "anytls"},
		"snell":      {Protocol: "snell", ProtocolOpts: `{"version":4,"obfs":"none"}`},
		"snell-tls":  {Protocol: "snell", ProtocolOpts: `{"version":4,"obfs":"tls","obfsHost":"bing.com"}`},
		"snell-http": {Protocol: "snell", ProtocolOpts: `{"version":4,"obfs":"http","obfsHost":"bing.com"}`},
		"mieru":      {Protocol: "mieru", ProtocolOpts: `{"transport":"TCP"}`},
	}
	ids := map[string]int64{}
	port := 30001
	for _, name := range []string{"ss", "vless", "anytls", "snell", "snell-tls", "snell-http", "mieru"} {
		n := nodes[name]
		n.UserID, n.LandingServerID, n.Name, n.Port, n.Status = uid, 1, name, port, "active"
		n.InboundTag = "flvxl-" + name
		rows = append(rows, &n)
		port++
	}
	for _, row := range rows {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range rows {
		if n, ok := row.(*model.LandingNode); ok {
			ids[n.Name] = n.ID
		}
	}

	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	stop := startMockNodeSession(t, server.URL, entry.Secret)
	t.Cleanup(stop)
	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	user, _ := auth.GenerateToken(uid, "tg_user", 1, secret)
	call := func(tok, path string, body interface{}) response.R {
		t.Helper()
		return requestContractEnvelope(t, router, tok, path, body)
	}
	forwards := func() int64 {
		var n int64
		r.DB().Model(&model.Forward{}).Count(&n)
		return n
	}
	create := func(tok string, tunnelID int64, name string, extra map[string]interface{}) response.R {
		t.Helper()
		body := map[string]interface{}{"tunnelId": tunnelID, "name": name, "strategy": "fifo"}
		for k, v := range extra {
			body[k] = v
		}
		return call(tok, "/api/v1/forward/create", body)
	}
	forwardID := func(name string) int64 {
		t.Helper()
		var f model.Forward
		if err := r.DB().Where("name = ?", name).First(&f).Error; err != nil {
			t.Fatalf("forward %s: %v", name, err)
		}
		return f.ID
	}

	// 1. Allowed on the 屏蔽 TLS tunnel: SS2022 (prod 156 / 347), Snell
	// without obfs, Mieru, and a typed address.
	for _, name := range []string{"ss", "snell", "mieru"} {
		if out := create(user, tlsTunnel, "ok-"+name, map[string]interface{}{"landingNodeId": ids[name]}); out.Code != 0 {
			t.Fatalf("%s on tls=1 tunnel refused: %+v", name, out)
		}
	}
	if out := create(user, tlsTunnel, "ok-typed", map[string]interface{}{"remoteAddr": "198.51.100.7:443"}); out.Code != 0 {
		t.Fatalf("typed target refused: %+v", out)
	}

	// 2. Refused, nothing written: TLS-feature nodes (user and admin), and
	// Snell obfs=http on a tunnel with 屏蔽 HTTP.
	before := forwards()
	for name, msg := range map[string]string{"vless": repo.LandingTLSBlockedMsg, "anytls": repo.LandingTLSBlockedMsg, "snell-tls": repo.LandingTLSBlockedMsg, "snell-http": repo.LandingHTTPBlockedMsg} {
		for _, tok := range []string{user, admin} {
			extra := map[string]interface{}{"landingNodeId": ids[name]}
			if tok == admin {
				extra["userId"] = uid
			}
			if out := create(tok, tlsTunnel, "bad-"+name, extra); out.Code == 0 || out.Msg != msg {
				t.Fatalf("%s on tls=1 tunnel: want %q, got %+v", name, msg, out)
			}
		}
	}
	// 「新建/复用」 target (landingServerId + protocol): refused before a node exists.
	var landingCount int64
	r.DB().Model(&model.LandingNode{}).Count(&landingCount)
	if out := create(user, tlsTunnel, "bad-server", map[string]interface{}{"landingServerId": 1, "landingProtocol": "vless_reality_vision"}); out.Code == 0 || out.Msg != repo.LandingTLSBlockedMsg {
		t.Fatalf("server+vless on tls=1 tunnel: %+v", out)
	}
	var landingAfter int64
	r.DB().Model(&model.LandingNode{}).Count(&landingAfter)
	if forwards() != before || landingAfter != landingCount {
		t.Fatalf("refused creates wrote rows: forwards %d→%d landing %d→%d", before, forwards(), landingCount, landingAfter)
	}

	// 3. Dmit Nathan case: VLESS on a tls=0 tunnel (屏蔽 HTTP on) works.
	if out := create(user, plainTunnel, "nathan-vless", map[string]interface{}{"landingNodeId": ids["vless"]}); out.Code != 0 {
		t.Fatalf("vless on tls=0 tunnel refused: %+v", out)
	}
	nathan := forwardID("nathan-vless")

	// 4. Moving it onto the 屏蔽 TLS tunnel is refused (edit and batch).
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": nathan, "name": "nathan-vless", "tunnelId": tlsTunnel, "landingNodeId": ids["vless"]}); out.Code == 0 || out.Msg != repo.LandingTLSBlockedMsg {
		t.Fatalf("move vless to tls=1 tunnel: %+v", out)
	}
	out := call(admin, "/api/v1/forward/batch-change-tunnel", map[string]interface{}{"forwardIds": []int64{nathan}, "targetTunnelId": tlsTunnel})
	raw, _ := json.Marshal(out.Data)
	if !strings.Contains(string(raw), repo.LandingTLSBlockedMsg) {
		t.Fatalf("batch change tunnel: %+v", out)
	}
	var moved model.Forward
	r.DB().First(&moved, nathan)
	if moved.TunnelID != plainTunnel {
		t.Fatalf("refused move changed the tunnel: %d", moved.TunnelID)
	}

	// 5. A pre-existing VLESS rule on the 屏蔽 TLS tunnel (made before the
	// guard / before 屏蔽 TLS was turned on) stays editable and untouched.
	legacy := model.Forward{UserID: uid, UserName: "tg_user", Name: "legacy-vless", TunnelID: tlsTunnel, RemoteAddr: "203.0.113.61:30002", LandingNodeID: ids["vless"], Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
	if err := r.DB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Create(&model.ForwardPort{ForwardID: legacy.ID, NodeID: entry.ID, Port: 20050}).Error; err != nil {
		t.Fatal(err)
	}
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": legacy.ID, "name": "legacy-renamed", "tunnelId": tlsTunnel, "landingNodeId": ids["vless"]}); out.Code != 0 {
		t.Fatalf("rename of existing rule refused: %+v", out)
	}
	var kept model.Forward
	r.DB().First(&kept, legacy.ID)
	if kept.LandingNodeID != ids["vless"] || kept.TunnelID != tlsTunnel || kept.Name != "legacy-renamed" {
		t.Fatalf("existing rule changed: %+v", kept)
	}

	// 6. The API hands the UI the feature sets (one shared source).
	list := call(user, "/api/v1/landing/node/list", map[string]interface{}{})
	raw, _ = json.Marshal(list.Data)
	var nodeViews []map[string]interface{}
	_ = json.Unmarshal(raw, &nodeViews)
	features := map[string]string{}
	for _, v := range nodeViews {
		b, _ := json.Marshal(v["filterFeatures"])
		features[v["name"].(string)] = string(b)
	}
	for name, want := range map[string]string{"ss": "null", "vless": `["tls"]`, "anytls": `["tls"]`, "snell": "null", "snell-tls": `["tls"]`, "snell-http": `["http"]`, "mieru": "null"} {
		if features[name] != want {
			t.Fatalf("filterFeatures %s = %s, want %s", name, features[name], want)
		}
	}
	tunnels := call(user, "/api/v1/tunnel/user/tunnel", map[string]interface{}{})
	raw, _ = json.Marshal(tunnels.Data)
	if !strings.Contains(string(raw), `"blockedFeatures":["tls","http"]`) || !strings.Contains(string(raw), `"blockedFeatures":["http"]`) {
		t.Fatalf("tunnel blockedFeatures missing: %s", raw)
	}
	servers := call(user, "/api/v1/landing/server/list", map[string]interface{}{})
	raw, _ = json.Marshal(servers.Data)
	if !strings.Contains(string(raw), `"protocolFilterFeatures":{"anytls":["tls"],"vless_reality_vision":["tls"]}`) {
		t.Fatalf("server protocolFilterFeatures: %s", raw)
	}
	links := call(user, "/api/v1/landing/node/links", map[string]interface{}{"id": ids["vless"]})
	raw, _ = json.Marshal(links.Data)
	if !strings.Contains(string(raw), `"blocked":"此隧道屏蔽了 TLS 协议特征"`) || !strings.Contains(string(raw), `"kind":"direct"`) {
		t.Fatalf("links of legacy rule not marked: %s", raw)
	}

	// 7. Tunnel edit warning: turning 屏蔽 TLS on for the Dmit-like tunnel
	// lists its VLESS rule; the save itself is not blocked by it.
	warn := call(admin, "/api/v1/tunnel/filter-affected", map[string]interface{}{"id": plainTunnel, "tls": 1, "http": 1})
	raw, _ = json.Marshal(warn.Data)
	if warn.Code != 0 || !strings.Contains(string(raw), `"count":1`) || !strings.Contains(string(raw), "nathan-vless") {
		t.Fatalf("filter-affected: %+v %s", warn, raw)
	}
	if out := call(user, "/api/v1/tunnel/filter-affected", map[string]interface{}{"id": plainTunnel, "tls": 1}); out.Code == 0 {
		t.Fatalf("filter-affected must be admin only: %+v", out)
	}
	off := call(admin, "/api/v1/tunnel/filter-affected", map[string]interface{}{"id": tlsTunnel, "tls": 0, "http": 0})
	raw, _ = json.Marshal(off.Data)
	if !strings.Contains(string(raw), `"count":0`) {
		t.Fatalf("turning off must warn nothing: %s", raw)
	}
}
