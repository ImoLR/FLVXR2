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
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 250 on SQLite and PostgreSQL, through the real router: a 线路机 tunnel
// accepts exactly VLESS Reality Vision and AnyTLS landing targets (users and
// admins alike); Snell / SS2022 / Mieru landing nodes, typed addresses and
// 「新建/复用」 with another protocol are refused with nothing written; an unset
// protocol still means Reality; AnyTLS rules are compliant in the list.
func TestLineProtocolAnyTLSContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runLineProtocolAnyTLS(t, dialect) })
	}
}

func runLineProtocolAnyTLS(t *testing.T, dialect string) {
	const secret = "line-anytls-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	entry := model.Node{Name: "Dmit Nathan", Secret: "la-entry-secret", ServerIP: "10.62.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]", LineMachine: true}
	if err := r.DB().Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	tu := model.Tunnel{Name: "Dmit Nathan -> 端口转发", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, HTTP: 1, TLS: 0, Socks: 1}
	if err := r.DB().Create(&tu).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Create(&model.ChainTunnel{TunnelID: tu.ID, ChainType: "1", NodeID: entry.ID, Port: sql.NullInt64{Int64: 20001, Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
		t.Fatal(err)
	}
	const uid, uid2 = 110, 111
	caps := `{"server_id":7,"name":"PQS TW","hosts":{"ipv4":"203.0.113.62"},"status":"active","port_min":30000,"port_max":30099,"protocols":["ss2022","vless_reality_vision"],"protocol_caps":{"versions":[1],"protocols":{"anytls":{"udp":true},"snell":{"udp":true},"mieru":{"udp":true}}}}`
	rows := []interface{}{
		&model.User{ID: uid, User: "surge_user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		&model.User{ID: uid2, User: "surge_user2", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		&model.UserTunnel{UserID: uid, TunnelID: tu.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.UserTunnel{UserID: uid2, TunnelID: tu.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "PQS TW", PublicHost: "203.0.113.62", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
			Protocols: `["ss2022","vless_reality_vision","anytls","snell","mieru"]`, SharedCaps: caps, MaxNodesPerUser: 20, Revision: 3, CreatedTime: 1, UpdatedTime: 1, SnellObfs: "none", SnellVersion: 4, MieruTransport: "TCP"},
		&model.LandingServerUser{LandingServerID: 5, UserID: uid, CreatedTime: now},
		&model.LandingServerUser{LandingServerID: 5, UserID: uid2, CreatedTime: now},
	}
	ids := map[string]int64{}
	port := 30001
	for _, p := range []string{"vless_reality_vision", "anytls", "ss2022", "snell", "mieru"} {
		n := model.LandingNode{UserID: uid, LandingServerID: 5, Name: p, Protocol: p, Port: port, Status: "active", InboundTag: "flvxl-c" + p, CreatedTime: now, UpdatedTime: now}
		if p == "anytls" {
			n.Password, n.CertSHA256 = "Abcdefghijklmnop12345678", strings.Repeat("ab", 32)
		}
		rows = append(rows, &n)
		port++
	}
	for _, row := range rows {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
		if n, ok := row.(*model.LandingNode); ok {
			ids[n.Protocol] = n.ID
		}
	}

	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	stop := startMockNodeSession(t, server.URL, entry.Secret)
	t.Cleanup(stop)
	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	user, _ := auth.GenerateToken(uid, "surge_user", 1, secret)
	call := func(tok, path string, body interface{}) response.R {
		t.Helper()
		return requestContractEnvelope(t, router, tok, path, body)
	}
	count := func(m interface{}) int64 {
		var n int64
		r.DB().Model(m).Count(&n)
		return n
	}
	create := func(tok string, name string, extra map[string]interface{}) response.R {
		t.Helper()
		body := map[string]interface{}{"tunnelId": tu.ID, "name": name, "strategy": "fifo"}
		if tok == admin {
			body["userId"] = uid
		}
		for k, v := range extra {
			body[k] = v
		}
		return call(tok, "/api/v1/forward/create", body)
	}
	const refusedMsg = "线路机隧道只能使用 VLESS Reality Vision 或 AnyTLS 落地"

	// 1. Allowed for users and admins: Reality and AnyTLS landing nodes.
	for _, tok := range []string{user, admin} {
		for _, p := range []string{"vless_reality_vision", "anytls"} {
			if out := create(tok, "ok-"+p, map[string]interface{}{"landingNodeId": ids[p]}); out.Code != 0 {
				t.Fatalf("%s target refused: %+v", p, out)
			}
		}
	}
	// 2. Refused, nothing written (users and admins).
	forwards, landings := count(&model.Forward{}), count(&model.LandingNode{})
	for _, tok := range []string{user, admin} {
		for _, p := range []string{"ss2022", "snell", "mieru"} {
			if out := create(tok, "bad-"+p, map[string]interface{}{"landingNodeId": ids[p]}); out.Code == 0 || out.Msg != refusedMsg {
				t.Fatalf("%s target: want %q, got %+v", p, refusedMsg, out)
			}
			if out := create(tok, "bad-server-"+p, map[string]interface{}{"landingServerId": 5, "landingProtocol": p}); out.Code == 0 || out.Msg != refusedMsg {
				t.Fatalf("server + %s: want %q, got %+v", p, refusedMsg, out)
			}
		}
		if out := create(tok, "typed", map[string]interface{}{"remoteAddr": "198.51.100.7:443"}); out.Code == 0 || !strings.Contains(out.Msg, "不能手动填写") || !strings.Contains(out.Msg, "AnyTLS") {
			t.Fatalf("typed target: %+v", out)
		}
	}
	if count(&model.Forward{}) != forwards || count(&model.LandingNode{}) != landings {
		t.Fatal("refused creates wrote rows")
	}
	// 3. 「新建/复用」: AnyTLS reuses the user's AnyTLS node; for a user without
	// one it is created (password + self-signed certificate); unset = Reality.
	if out := create(user, "reuse-anytls", map[string]interface{}{"landingServerId": 5, "landingProtocol": "anytls"}); out.Code != 0 {
		t.Fatalf("server + anytls: %+v", out)
	}
	var reused model.Forward
	r.DB().Where("name = ?", "reuse-anytls").First(&reused)
	if reused.LandingNodeID != ids["anytls"] {
		t.Fatalf("anytls not reused: %+v", reused)
	}
	out := call(admin, "/api/v1/forward/create", map[string]interface{}{"tunnelId": tu.ID, "name": "new-anytls", "strategy": "fifo", "userId": uid2, "landingServerId": 5, "landingProtocol": "anytls"})
	if out.Code != 0 {
		t.Fatalf("server + anytls for a new user: %+v", out)
	}
	var created model.LandingNode
	if err := r.DB().Where("user_id = ? AND protocol = ?", uid2, "anytls").First(&created).Error; err != nil || len(created.Password) != 24 || len(created.CertSHA256) != 64 || !strings.Contains(created.TLSCertPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("created anytls node %+v %v", created, err)
	}
	if out := create(user, "unset", map[string]interface{}{"landingServerId": 5}); out.Code != 0 {
		t.Fatalf("server without protocol: %+v", out)
	}
	var unset model.Forward
	r.DB().Where("name = ?", "unset").First(&unset)
	if unset.LandingNodeID != ids["vless_reality_vision"] {
		t.Fatalf("unset protocol did not use the Reality node: %+v", unset)
	}
	// 4. Moving a rule onto an SS2022 target is refused; AnyTLS → Reality ok.
	var anyRule model.Forward
	r.DB().Where("name = ? AND user_id = ?", "ok-anytls", uid).First(&anyRule)
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": anyRule.ID, "name": "ok-anytls", "tunnelId": tu.ID, "landingNodeId": ids["ss2022"]}); out.Code == 0 || out.Msg != refusedMsg {
		t.Fatalf("edit to ss2022: %+v", out)
	}
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": anyRule.ID, "name": "ok-anytls", "tunnelId": tu.ID, "landingNodeId": ids["vless_reality_vision"]}); out.Code != 0 {
		t.Fatalf("edit to reality: %+v", out)
	}
	// 5. The list: a pre-existing SS2022 rule is 不合规, AnyTLS ones are not.
	// SQLite only: /forward/list fails on PostgreSQL already on the base
	// (fork.129: ListForwards joins the reserved word "user" unquoted; prod
	// runs SQLite) — reported in plan 250, not changed here.
	if dialect != "sqlite" {
		return
	}
	legacy := model.Forward{UserID: uid, UserName: "surge_user", Name: "legacy-ss", TunnelID: tu.ID, RemoteAddr: "203.0.113.62:30003", LandingNodeID: ids["ss2022"], Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
	if err := r.DB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	list := call(user, "/api/v1/forward/list", map[string]interface{}{})
	if list.Code != 0 {
		t.Fatalf("forward list: %+v", list)
	}
	raw, _ := json.Marshal(list.Data)
	var wrapped struct {
		Items []map[string]interface{} `json:"items"`
	}
	_ = json.Unmarshal(raw, &wrapped)
	items := wrapped.Items
	seen := 0
	for _, it := range items {
		name, _ := it["name"].(string)
		bad := it["lineNonCompliant"] == true
		switch {
		case name == "legacy-ss":
			seen++
			if !bad {
				t.Fatalf("legacy SS2022 rule not flagged: %v", it)
			}
		case strings.Contains(name, "anytls") || name == "unset":
			seen++
			if bad {
				t.Fatalf("AnyTLS / Reality rule flagged: %v", it)
			}
		}
	}
	if seen < 4 {
		t.Fatalf("list items %d seen %d: %s", len(items), seen, raw)
	}
}

// Plan 250: the forward_chain AnyTLS columns on both dialects — create,
// protocol switch with its rollback point, revert, and the client outputs.
func TestChainAnyTLSColumnsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			exit := model.Node{Name: "Akari HK", Secret: "ca-exit", ServerIP: "203.0.113.70", Port: "10000-19999", CreatedTime: now, Status: 1, LandingCapable: true, LandingChain: 2}
			if err := r.DB().Create(&exit).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Create(&model.User{ID: 120, User: "chain_user", Pwd: "x", RoleID: 1, Status: 1, Flow: -1, Num: 99, CreatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			up, _, err := linechain.ParseLink("socks5://u:p@198.51.100.20:1080#home")
			if err != nil {
				t.Fatal(err)
			}
			c, _, err := r.CreateForwardChain(repo.ChainCreateInput{UserID: 120, TunnelID: 9, Upstream: up, ExitNodeIDs: []int64{exit.ID}, InFlightAfter: now, InboundProtocol: "anytls"})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := r.GetForwardChain(c.ID)
			if got.InboundProtocol != "anytls" || got.TLSServerName == "" || len(got.Password) != 24 || len(got.CertSHA256) != 64 || !strings.Contains(got.TLSKeyPEM, "PRIVATE KEY") {
				t.Fatalf("stored chain %+v", got)
			}
			// Duplicate with the same protocol refused, other protocol allowed.
			if _, _, err := r.CreateForwardChain(repo.ChainCreateInput{UserID: 120, TunnelID: 9, Upstream: up, ExitNodeIDs: []int64{exit.ID}, InFlightAfter: time.Now().Add(time.Hour).UnixMilli(), InboundProtocol: "anytls"}); err == nil {
				t.Fatal("duplicate AnyTLS chain accepted")
			}
			if err := r.ActivateForwardChain(c.ID, 900); err != nil {
				t.Fatal(err)
			}
			if _, err := r.BeginChainInboundChange(c.ID, model.ChainInboundReality); err != nil {
				t.Fatalf("switch to reality: %v", err)
			}
			mid, _ := r.GetForwardChain(c.ID)
			if mid.ChainInbound() != model.ChainInboundReality || mid.PrevInbound == "" || mid.Password != "" {
				t.Fatalf("mid switch %+v", mid)
			}
			if _, err := r.RevertChainInboundChange(c.ID); err != nil {
				t.Fatal(err)
			}
			back, _ := r.GetForwardChain(c.ID)
			if back.ChainInbound() != model.ChainInboundAnyTLS || back.Password != got.Password || back.CertSHA256 != got.CertSHA256 || back.PrevInbound != "" {
				t.Fatalf("reverted %+v", back)
			}
			// A Reality chain to the same node may exist next to it; then the
			// AnyTLS one cannot switch to Reality (it would duplicate it).
			if _, _, err := r.CreateForwardChain(repo.ChainCreateInput{UserID: 120, TunnelID: 9, Upstream: up, ExitNodeIDs: []int64{exit.ID}, InFlightAfter: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
				t.Fatalf("Reality chain next to AnyTLS: %v", err)
			}
			if _, err := r.BeginChainInboundChange(c.ID, model.ChainInboundReality); err == nil || !strings.Contains(err.Error(), "VLESS Reality") {
				t.Fatalf("duplicate switch: %v", err)
			}
			node, ok := repo.ChainSubNode(*back, "198.51.100.1", 20001, "surge")
			clash, surge := repo.ChainClientConfigs(*back, "198.51.100.1", 20001, "surge")
			if !ok || node.Extra == nil || node.Extra.CertSHA256 != got.CertSHA256 || !strings.Contains(clash, got.CertSHA256) ||
				surge != "surge = anytls, 198.51.100.1, 20001, password=\""+got.Password+"\", sni="+got.TLSServerName+", server-cert-fingerprint-sha256="+got.CertSHA256 {
				t.Fatalf("outputs %+v %q %q", node, clash, surge)
			}
			// A failed create erases the AnyTLS secrets too.
			if _, err := r.FailForwardChain(c.ID, "test"); err != nil {
				t.Fatal(err)
			}
			failed, _ := r.GetForwardChain(c.ID)
			if failed.Password != "" || failed.TLSKeyPEM != "" || failed.PrevInbound != "" {
				t.Fatalf("failed chain kept secrets %+v", failed)
			}
		})
	}
}
