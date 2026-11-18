package contract_test

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"encoding/base64"
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
	"go-backend/internal/sublink"
)

// Plan 284: a normal forward can target one of the owner's 外部节点. It stays
// a plain port forward to the node's address; the panel builds the node's
// link at the entry address (复制链接 / 订阅), refuses protocols the tunnel
// filter blocks, keeps the node while a forward uses it, and recognises old
// 手动填写 forwards whose target is exactly one node (read time only).
func TestExternalForwardContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runExternalForward(t, dialect) })
	}
}

func runExternalForward(t *testing.T, dialect string) {
	const secret = "ext-fwd-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	entry := model.Node{Name: "沪日入口", Secret: "ef-entry-secret", ServerIP: "10.62.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	if err := r.DB().Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	tunnel := func(name string, tls, http, socks int, inIP string) int64 {
		tu := model.Tunnel{Name: name, TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, HTTP: http, TLS: tls, Socks: socks}
		if inIP != "" {
			tu.InIP = sql.NullString{String: inIP, Valid: true}
		}
		if err := r.DB().Create(&tu).Error; err != nil {
			t.Fatal(err)
		}
		if err := r.DB().Create(&model.ChainTunnel{TunnelID: tu.ID, ChainType: "1", NodeID: entry.ID, Port: sql.NullInt64{Int64: 20001, Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
			t.Fatal(err)
		}
		return tu.ID
	}
	openTunnel := tunnel("端口转发 HK", 0, 0, 0, "203.0.113.5,2001:db8::5")
	lineTunnel := tunnel("沪日 IPLC 专线", 1, 1, 1, "")
	const uid, other = 100, 101
	rows := []interface{}{
		&model.User{ID: uid, User: "ef_user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		&model.User{ID: other, User: "ef_other", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
	}
	for _, tid := range []int64{openTunnel, lineTunnel} {
		for _, u := range []int64{uid, other} {
			rows = append(rows, &model.UserTunnel{UserID: u, TunnelID: tid, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1})
		}
	}
	for _, row := range rows {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	key16 := base64.StdEncoding.EncodeToString(make([]byte, 16))
	ext := func(owner int64, name, link string) int64 {
		t.Helper()
		up, _, err := linechain.ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		n := model.LandingExternalNode{UserID: owner, Name: name, Protocol: up.Type, Host: up.Server, Port: up.Port, Summary: up.Summary(), Upstream: up.JSON(), UpstreamHash: up.Hash(), CreatedTime: now, UpdatedTime: now}
		if err := r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	trojan := ext(uid, "日本家宽", "trojan://pass@jp.example.com:443?alpn=h2#jp")
	ss := ext(uid, "台湾 SS", "ss://2022-blake3-aes-128-gcm:"+strings.ReplaceAll(key16, "=", "%3D")+"@198.51.100.30:8443#tw")
	socks := ext(uid, "公司 SOCKS", "socks5://u:p@198.51.100.31:1080#s")
	hy2 := ext(uid, "HY2", "hysteria2://auth@hy.example.com:8443/?sni=hy.example.com#hy")
	otherExt := ext(other, "别人的", "trojan://x@198.51.100.40:443?sni=a.example.com#o")
	// Two of the user's nodes on one address (ambiguous for auto-match).
	ambA := ext(uid, "同址 A", "socks5://a:a@198.51.100.50:1080#a")
	_ = ext(uid, "同址 B", "socks5://b:b@198.51.100.50:1080#b")

	h := handler.New(r, secret, "test")
	h.SetCNLandingResolver(extFwdResolver{"jp.example.com": "198.51.100.20", "hy.example.com": "198.51.100.21"})
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	stop := startMockNodeSession(t, server.URL, entry.Secret)
	t.Cleanup(stop)
	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	user, _ := auth.GenerateToken(uid, "ef_user", 1, secret)
	call := func(tok, path string, body interface{}) response.R {
		t.Helper()
		return requestContractEnvelope(t, router, tok, path, body)
	}
	create := func(tok string, tunnelID int64, name string, extra map[string]interface{}) response.R {
		t.Helper()
		body := map[string]interface{}{"tunnelId": tunnelID, "name": name, "strategy": "fifo"}
		for k, v := range extra {
			body[k] = v
		}
		return call(tok, "/api/v1/forward/create", body)
	}
	row := func(name string) model.Forward {
		t.Helper()
		var f model.Forward
		if err := r.DB().Where("name = ?", name).First(&f).Error; err != nil {
			t.Fatalf("forward %s: %v", name, err)
		}
		return f
	}
	count := func() int64 {
		var n int64
		r.DB().Model(&model.Forward{}).Count(&n)
		return n
	}

	// 1. Create on an open tunnel: plain port forward to the node's address.
	if out := create(user, openTunnel, "日本家宽转发", map[string]interface{}{"externalNodeId": trojan, "remoteAddr": "9.9.9.9:1"}); out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	f := row("日本家宽转发")
	if f.ExternalNodeID != trojan || f.RemoteAddr != "jp.example.com:443" || f.LandingNodeID != 0 || f.Mode != "gost" {
		t.Fatalf("row %+v", f)
	}

	// 2. Refused, nothing written: another user's node, landing + external,
	// blocked protocols on the 专线 (TLS / SOCKS / QUIC), admin using his own.
	before := count()
	for _, c := range []struct {
		tok    string
		tunnel int64
		extra  map[string]interface{}
		want   string
	}{
		{user, openTunnel, map[string]interface{}{"externalNodeId": otherExt}, "外部节点不存在"},
		{user, openTunnel, map[string]interface{}{"externalNodeId": trojan, "landingNodeId": 5}, "只能选一种"},
		{user, lineTunnel, map[string]interface{}{"externalNodeId": trojan}, "TLS"},
		{user, lineTunnel, map[string]interface{}{"externalNodeId": socks}, "SOCKS"},
		{user, lineTunnel, map[string]interface{}{"externalNodeId": hy2}, "QUIC"},
		{admin, lineTunnel, map[string]interface{}{"externalNodeId": trojan, "userId": uid}, "TLS"},
		{admin, openTunnel, map[string]interface{}{"externalNodeId": otherExt, "userId": uid}, "外部节点不存在"},
	} {
		if out := create(c.tok, c.tunnel, "refused", c.extra); out.Code == 0 || !strings.Contains(out.Msg, c.want) {
			t.Fatalf("%+v: %+v (want %q)", c.extra, out, c.want)
		}
	}
	if count() != before {
		t.Fatal("refused create wrote a row")
	}
	// SS passes the 专线; admin creates for the user with the user's node.
	if out := create(user, lineTunnel, "台湾专线", map[string]interface{}{"externalNodeId": ss}); out.Code != 0 {
		t.Fatalf("ss on 专线: %+v", out)
	}
	if out := create(admin, openTunnel, "管理员代建", map[string]interface{}{"externalNodeId": ss, "userId": uid}); out.Code != 0 || row("管理员代建").UserID != uid || row("管理员代建").ExternalNodeID != ss {
		t.Fatalf("admin create: %+v", out)
	}
	// Old 手动填写 forwards: one matches exactly one node, one is ambiguous.
	if out := create(user, openTunnel, "老转发", map[string]interface{}{"remoteAddr": "198.51.100.31:1080"}); out.Code != 0 {
		t.Fatalf("typed: %+v", out)
	}
	if out := create(user, openTunnel, "老转发同址", map[string]interface{}{"remoteAddr": "198.51.100.50:1080"}); out.Code != 0 {
		t.Fatalf("typed: %+v", out)
	}
	if row("老转发").ExternalNodeID != 0 {
		t.Fatal("auto-match wrote the DB")
	}

	// 3. Forward list: externalNode on linked + auto-matched, not ambiguous.
	list := call(user, "/api/v1/forward/list", map[string]interface{}{})
	raw, _ := json.Marshal(list.Data)
	var payload struct {
		Items []map[string]interface{} `json:"items"`
	}
	_ = json.Unmarshal(raw, &payload)
	views := map[string]map[string]interface{}{}
	for _, it := range payload.Items {
		if v, ok := it["externalNode"].(map[string]interface{}); ok {
			views[it["name"].(string)] = v
		}
	}
	if v := views["日本家宽转发"]; v == nil || v["name"] != "日本家宽" || v["protocolLabel"] != "Trojan" || v["auto"] != false || v["mismatch"] != false {
		t.Fatalf("linked view %+v", views)
	}
	if v := views["老转发"]; v == nil || v["auto"] != true || v["name"] != "公司 SOCKS" {
		t.Fatalf("auto view %+v", views["老转发"])
	}
	if _, ok := views["老转发同址"]; ok {
		t.Fatal("ambiguous target matched")
	}
	if strings.Contains(string(raw), "pass@") || strings.Contains(string(raw), "upstream") {
		t.Fatalf("secret in list: %s", raw)
	}

	// 4. 复制链接: one link per entry address, SNI = the node's domain.
	out := call(user, "/api/v1/forward/external/links", map[string]interface{}{"forwardId": f.ID})
	if out.Code != 0 {
		t.Fatalf("links: %+v", out)
	}
	raw, _ = json.Marshal(out.Data)
	var lres struct {
		Links []struct {
			Name, URL, Kind string
		} `json:"links"`
	}
	_ = json.Unmarshal(raw, &lres)
	if len(lres.Links) != 2 {
		t.Fatalf("links %s", raw)
	}
	var fp model.ForwardPort
	r.DB().Where("forward_id = ?", f.ID).First(&fp)
	port := itoa(int64(fp.Port))
	for i, authority := range []string{"203.0.113.5:" + port, "[2001:db8::5]:" + port} {
		l := lres.Links[i]
		if !strings.HasPrefix(l.URL, "trojan://pass@"+authority+"?") || !strings.Contains(l.URL, "sni=jp.example.com") || !strings.Contains(l.URL, "alpn=h2") || l.Kind != "external" {
			t.Fatalf("link %d %+v", i, l)
		}
		if l.Name != "日本家宽转发 · 沪日入口" {
			t.Fatalf("name %q", l.Name)
		}
	}
	if out := call(user, "/api/v1/forward/external/links", map[string]interface{}{"forwardId": row("老转发同址").ID}); out.Code == 0 {
		t.Fatal("links for a plain forward")
	}
	otherTok, _ := auth.GenerateToken(other, "ef_other", 1, secret)
	if out := call(otherTok, "/api/v1/forward/external/links", map[string]interface{}{"forwardId": f.ID}); out.Code == 0 {
		t.Fatal("another user read the links")
	}

	// 5. 外部节点 list + delete guard; rename stays allowed.
	elist := call(user, "/api/v1/landing/external/list", map[string]interface{}{})
	raw, _ = json.Marshal(elist.Data)
	if !strings.Contains(string(raw), `"kind":"forward"`) || !strings.Contains(string(raw), "日本家宽转发") {
		t.Fatalf("external list uses %s", raw)
	}
	if out := call(user, "/api/v1/landing/external/delete", map[string]interface{}{"id": trojan}); out.Code == 0 || !strings.Contains(out.Msg, "转发 「日本家宽转发」 用作目标") {
		t.Fatalf("delete in use: %+v", out)
	}
	if out := call(user, "/api/v1/landing/external/rename", map[string]interface{}{"id": trojan, "name": "日本家宽2"}); out.Code != 0 {
		t.Fatalf("rename: %+v", out)
	}
	// An auto-matched node is not "in use": deleting it only drops the match.
	if out := call(user, "/api/v1/landing/external/delete", map[string]interface{}{"id": ambA}); out.Code != 0 {
		t.Fatalf("delete unused: %+v", out)
	}

	// 6. Batch change tunnel onto the 专线 is refused for the Trojan forward.
	out = call(user, "/api/v1/forward/batch-change-tunnel", map[string]interface{}{"forwardIds": []int64{f.ID}, "targetTunnelId": lineTunnel})
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), "TLS") || row("日本家宽转发").TunnelID != openTunnel {
		t.Fatalf("batch change tunnel: %s", raw)
	}

	// 7. Subscriptions: the forward's links are panel rule links; the plain
	// port-forward count excludes linked + auto-matched forwards.
	panel, err := r.SubPanelLinks(uid, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var subURIs []string
	externals := map[string]bool{}
	for _, l := range panel.Links {
		if l.External != "" {
			externals[l.External+map[bool]string{true: "/auto", false: ""}[l.ExternalAuto]] = true
			subURIs = append(subURIs, sublink.ShareURI(l.Node))
		}
	}
	if !externals["日本家宽2"] || !externals["台湾 SS"] || !externals["公司 SOCKS/auto"] {
		t.Fatalf("sub externals %v", externals)
	}
	if !strings.Contains(strings.Join(subURIs, "\n"), "trojan://pass@[2001:db8::5]:"+port+"?") {
		t.Fatalf("sub uris %v", subURIs)
	}
	// Deleting 同址 A left B alone on that address: 老转发同址 now matches too.
	if !externals["同址 B/auto"] || panel.PortForwards != 0 {
		t.Fatalf("plain count %d", panel.PortForwards)
	}
	pool := call(user, "/api/v1/sub/pool", map[string]interface{}{})
	raw, _ = json.Marshal(pool.Data)
	if pool.Code != 0 || !strings.Contains(string(raw), `"external":"日本家宽2"`) || !strings.Contains(string(raw), `"externalAuto":true`) {
		t.Fatalf("pool %s", raw)
	}

	// 8. Update: a typed address without externalNodeId drops the link; the
	// node can then be deleted. externalNodeId 0 clears it explicitly too.
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": f.ID, "name": f.Name, "tunnelId": openTunnel, "remoteAddr": "198.51.100.99:443", "strategy": "fifo"}); out.Code != 0 {
		t.Fatalf("update typed: %+v", out)
	}
	if g := row("日本家宽转发"); g.ExternalNodeID != 0 || g.RemoteAddr != "198.51.100.99:443" {
		t.Fatalf("after typed update %+v", g)
	}
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": f.ID, "name": f.Name, "tunnelId": openTunnel, "externalNodeId": hy2, "strategy": "fifo"}); out.Code != 0 {
		t.Fatalf("relink hy2: %+v", out)
	}
	if g := row("日本家宽转发"); g.ExternalNodeID != hy2 || g.RemoteAddr != "hy.example.com:8443" {
		t.Fatalf("after relink %+v", g)
	}
	// Moving the HY2 forward onto the 专线 (屏蔽 TLS ⇒ QUIC) is refused.
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": f.ID, "name": f.Name, "tunnelId": lineTunnel, "strategy": "fifo"}); out.Code == 0 || !strings.Contains(out.Msg, "QUIC") {
		t.Fatalf("move hy2 to 专线: %+v", out)
	}
	if out := call(user, "/api/v1/forward/update", map[string]interface{}{"id": f.ID, "name": f.Name, "tunnelId": openTunnel, "externalNodeId": 0, "remoteAddr": "198.51.100.98:443", "strategy": "fifo"}); out.Code != 0 || row("日本家宽转发").ExternalNodeID != 0 {
		t.Fatalf("clear: %+v", out)
	}
	if out := call(user, "/api/v1/landing/external/delete", map[string]interface{}{"id": trojan}); out.Code != 0 {
		t.Fatalf("delete after unlink: %+v", out)
	}
}

// extFwdResolver: fixed public answers for the test domains.
type extFwdResolver map[string]string

func (m extFwdResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ip, ok := m[host]; ok {
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	return nil, errors.New("no such host")
}
