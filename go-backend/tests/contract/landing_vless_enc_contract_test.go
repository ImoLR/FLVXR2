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
	"go-backend/internal/vlessenc"
)

// Plan 253 on SQLite and PostgreSQL, through the real router: a shared server
// advertising vless_enc creates VLESS Encryption nodes (keys made by the
// panel), the share link parses back, a normal tunnel that blocks TLS and HTTP
// accepts them as targets, a 线路机 tunnel refuses them (users and admins)
// with nothing written, and 「我自己的节点」 parses a VLESS Encryption link.
func TestLandingVLESSEncContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runLandingVLESSEnc(t, dialect) })
	}
}

func runLandingVLESSEnc(t *testing.T, dialect string) {
	const secret = "landing-vless-enc-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	line := model.Node{Name: "Dmit Nathan", Secret: "ve-line-secret", ServerIP: "10.63.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]", LineMachine: true}
	plain := model.Node{Name: "火山广州", Secret: "ve-plain-secret", ServerIP: "10.63.0.2", Port: "21000-21999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	for _, n := range []*model.Node{&line, &plain} {
		if err := r.DB().Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	lineTunnel := model.Tunnel{Name: "Dmit Nathan -> 端口转发", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1}
	// 专线: blocks both TLS and HTTP (plan 224 entry filter).
	blockTunnel := model.Tunnel{Name: "广州专线 屏蔽TLS", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, TLS: 1, HTTP: 1}
	for i, tu := range []*model.Tunnel{&lineTunnel, &blockTunnel} {
		if err := r.DB().Create(tu).Error; err != nil {
			t.Fatal(err)
		}
		entry := []model.Node{line, plain}[i]
		if err := r.DB().Create(&model.ChainTunnel{TunnelID: tu.ID, ChainType: "1", NodeID: entry.ID, Port: sql.NullInt64{Int64: int64(20001 + i*1000), Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	const uid = 120
	caps := `{"server_id":8,"name":"PQS TW","hosts":{"ipv4":"203.0.113.63"},"status":"active","port_min":30000,"port_max":30099,"protocols":["ss2022","vless_reality_vision"],"protocol_caps":{"versions":[1],"protocols":{"anytls":{"udp":true},"vless_enc":{"udp":true,"versions":[1]}}}}`
	rows := []interface{}{
		&model.User{ID: uid, User: "ve_user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		&model.UserTunnel{UserID: uid, TunnelID: lineTunnel.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.UserTunnel{UserID: uid, TunnelID: blockTunnel.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1},
		&model.LandingServer{ID: 6, Source: "shared", RemoteServerID: 8, Name: "PQS TW", PublicHost: "203.0.113.63", PortMin: 30000, PortMax: 30099, FlowType: 2, TrafficRatio: 1, Status: "active",
			Protocols: `["ss2022","vless_reality_vision","vless_enc"]`, SharedCaps: caps, MaxNodesPerUser: 20, Revision: 3, CreatedTime: 1, UpdatedTime: 1, SnellObfs: "none", SnellVersion: 4, MieruTransport: "TCP"},
		&model.LandingServerUser{LandingServerID: 6, UserID: uid, CreatedTime: now},
	}
	for _, row := range rows {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	for _, n := range []model.Node{line, plain} {
		stop := startMockNodeSession(t, server.URL, n.Secret)
		t.Cleanup(stop)
	}
	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	user, _ := auth.GenerateToken(uid, "ve_user", 1, secret)
	call := func(tok, path string, body interface{}) response.R {
		t.Helper()
		return requestContractEnvelope(t, router, tok, path, body)
	}
	data := func(out response.R) map[string]interface{} {
		raw, _ := json.Marshal(out.Data)
		m := map[string]interface{}{}
		_ = json.Unmarshal(raw, &m)
		return m
	}

	// 1. The user creates a VLESS Encryption node; the panel made the keys.
	out := call(user, "/api/v1/landing/node/create", map[string]interface{}{"landingServerId": 6, "name": "我的 VE", "protocol": "vless_enc"})
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	var n model.LandingNode
	if err := r.DB().Where("user_id = ? AND protocol = ?", uid, "vless_enc").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if pub, err := vlessenc.PublicFromPrivate(n.PrivateKey); err != nil || pub != n.PublicKey || n.Flow != "xtls-rprx-vision" || vlessenc.ValidateDecryption(repo.VLESSEncDecryption(n)) != nil {
		t.Fatalf("node credentials %+v %v", n, err)
	}
	if v := data(out); strings.Contains(string(mustMarshal(v)), n.PrivateKey) {
		t.Fatal("private key leaked in the create response")
	}
	// 2. 复制链接 = the standard link, parsed back to the same encryption.
	links := call(user, "/api/v1/landing/node/links", map[string]interface{}{"id": n.ID})
	raw := mustMarshal(links.Data)
	if links.Code != 0 || strings.Contains(raw, n.PrivateKey) {
		t.Fatalf("links: %+v", links)
	}
	var list []repo.LandingLink
	_ = json.Unmarshal([]byte(raw), &list)
	if len(list) == 0 || !strings.HasPrefix(list[0].URL, "vless://"+n.UUID+"@203.0.113.63:") || !strings.Contains(list[0].Clash, "encryption:") {
		t.Fatalf("link list %s", raw)
	}
	up, _, err := linechain.ParseLink(list[0].URL)
	if err != nil || up.Encryption != repo.VLESSEncEncryption(n) {
		t.Fatalf("parse back %+v %v", up, err)
	}
	// 3. Server list: available, no filter feature for vless_enc.
	sl := call(user, "/api/v1/landing/server/list", map[string]interface{}{})
	sraw := mustMarshal(sl.Data)
	if sl.Code != 0 || !strings.Contains(sraw, `"vlessEncAvailable":true`) || strings.Contains(sraw, `"vless_enc":["tls"]`) {
		t.Fatalf("server list %s", sraw)
	}
	count := func(m interface{}) int64 {
		var c int64
		r.DB().Model(m).Count(&c)
		return c
	}
	create := func(tok string, tunnelID int64, name string, extra map[string]interface{}) response.R {
		t.Helper()
		body := map[string]interface{}{"tunnelId": tunnelID, "name": name, "strategy": "fifo"}
		if tok == admin {
			body["userId"] = uid
		}
		for k, v := range extra {
			body[k] = v
		}
		return call(tok, "/api/v1/forward/create", body)
	}
	// 4. 线路机: refused for users and admins, nothing written.
	const refusedMsg = "线路机隧道只能使用 VLESS Reality Vision 或 AnyTLS 落地"
	forwards, landings := count(&model.Forward{}), count(&model.LandingNode{})
	for _, tok := range []string{user, admin} {
		if out := create(tok, lineTunnel.ID, "line-ve", map[string]interface{}{"landingNodeId": n.ID}); out.Code == 0 || out.Msg != refusedMsg {
			t.Fatalf("线路机 vless_enc node: %+v", out)
		}
		if out := create(tok, lineTunnel.ID, "line-ve-new", map[string]interface{}{"landingServerId": 6, "landingProtocol": "vless_enc"}); out.Code == 0 || out.Msg != refusedMsg {
			t.Fatalf("线路机 new vless_enc: %+v", out)
		}
	}
	if count(&model.Forward{}) != forwards || count(&model.LandingNode{}) != landings {
		t.Fatal("refused creates wrote rows")
	}
	// 5. 屏蔽 TLS + HTTP tunnel: allowed (existing node and 新建/复用).
	for _, tok := range []string{user, admin} {
		if out := create(tok, blockTunnel.ID, "block-ve", map[string]interface{}{"landingNodeId": n.ID}); out.Code != 0 {
			t.Fatalf("blocking tunnel refused vless_enc: %+v", out)
		}
	}
	if out := create(user, blockTunnel.ID, "block-ve-reuse", map[string]interface{}{"landingServerId": 6, "landingProtocol": "vless_enc"}); out.Code != 0 {
		t.Fatalf("blocking tunnel 新建/复用: %+v", out)
	}
	var reused model.Forward
	r.DB().Where("name = ?", "block-ve-reuse").First(&reused)
	if reused.LandingNodeID != n.ID {
		t.Fatalf("not reused: %+v", reused)
	}
	// Reality is still refused there (unchanged behaviour).
	if out := create(user, blockTunnel.ID, "block-rv", map[string]interface{}{"landingServerId": 6, "landingProtocol": "vless_reality_vision"}); out.Code == 0 || !strings.Contains(out.Msg, "TLS") {
		t.Fatalf("Reality on a TLS-blocking tunnel: %+v", out)
	}
	// 6. 「我自己的节点」 parse accepts a VLESS Encryption link.
	p := call(user, "/api/v1/forward/chain/parse", map[string]interface{}{"link": "vless://" + n.UUID + "@8.8.8.8:443?encryption=" + repo.VLESSEncEncryption(n) + "&flow=xtls-rprx-vision&security=none&type=tcp#x"})
	if pd := data(p); p.Code != 0 || pd["protocolLabel"] != "VLESS Encryption" {
		t.Fatalf("chain parse %+v", p)
	}
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
