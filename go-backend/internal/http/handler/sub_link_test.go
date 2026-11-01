package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/landingsub"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"

	"gorm.io/gorm"
)

func subCall(t *testing.T, h *Handler, path string, body any, uid int64, role int) response.R {
	t.Helper()
	mux := http.NewServeMux()
	h.registerSubLink(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/sub/"+path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: strconv.FormatInt(uid, 10), RoleID: role}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	return out
}

func subPull(h *Handler, token, ua, ip string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.registerSubLink(mux)
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	req.RemoteAddr = ip + ":5555"
	req.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// subSource serves a subscription body over a test server and points the
// fetcher at it (the SSRF guard sees a public address).
type subSource struct {
	srv   *httptest.Server
	hits  atomic.Int32
	mu    sync.Mutex
	body  string
	code  int
	block chan struct{}
}

func newSubSource(t *testing.T, body string) *subSource {
	t.Helper()
	s := &subSource{body: body, code: 200}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		block, body, code := s.block, s.body, s.code
		s.mu.Unlock()
		if block != nil {
			<-block
		}
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	old := subFetcher
	subFetcher = func(h *Handler) *landingsub.Fetcher {
		f := &landingsub.Fetcher{Resolver: fakeChainResolver{"sub.example.net": {"198.51.124.30"}}}
		f.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, s.srv.Listener.Addr().String())
		}
		return f
	}
	t.Cleanup(func() { subFetcher = old; s.srv.Close() })
	return s
}

func (s *subSource) set(body string, code int) {
	s.mu.Lock()
	s.body, s.code = body, code
	s.mu.Unlock()
}

func subEnv(t *testing.T) *flowTestEnv {
	t.Helper()
	e := newFlowTestEnv(t)
	e.h.jwtSecret = "sub-link-test"
	e.addUser(2)
	e.addUser(3)
	return e
}

const subURL = "http://sub.example.net/api/v1/client/subscribe?token=SECRET123"

func socksLines(names ...string) string {
	lines := []string{}
	for i, n := range names {
		lines = append(lines, fmt.Sprintf("socks5://u:p@198.51.124.%d:1080#%s", 10+i, n))
	}
	return strings.Join(lines, "\n")
}

// addSource goes through 获取 → 保存 like the UI.
func addSource(t *testing.T, e *flowTestEnv, uid int64, name string) int64 {
	t.Helper()
	pre := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, uid, 1)
	if pre.Code != 0 {
		t.Fatalf("fetch: %+v", pre)
	}
	out := subCall(t, e.h, "source/create", map[string]any{"name": name, "fetchId": dataMap(t, pre)["fetchId"]}, uid, 1)
	if out.Code != 0 {
		t.Fatalf("create source: %+v", out)
	}
	return int64(dataMap(t, out)["id"].(float64))
}

func TestSubSourcePreviewCreateAndSecrets(t *testing.T) {
	e := subEnv(t)
	clash := `proxies:
  - {name: "剩余流量：100G", type: ss, server: 1.1.1.1, port: 1, cipher: aes-128-gcm, password: x}
  - {name: "🇭🇰 香港 01", type: trojan, server: hk1.provider.example, port: 443, password: tpw, sni: hk1.provider.example, skip-cert-verify: true}
  - {name: "🇯🇵 日本 02", type: vmess, server: jp2.provider.example, port: 443, uuid: b831381d-6324-4d53-ad4f-8cda48b30811, alterId: 0, cipher: auto, tls: true, skip-cert-verify: true}
  - {name: "TUIC 节点", type: tuic, server: t.provider.example, port: 443}
`
	src := newSubSource(t, clash)
	pre := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1)
	d := dataMap(t, pre)
	if pre.Code != 0 || d["count"].(float64) != 2 || d["info"].(float64) != 1 || len(d["items"].([]any)) != 3 {
		t.Fatalf("preview: %+v", pre)
	}
	if !strings.Contains(d["urlHint"].(string), "sub.example.net/api/…?token=••••") || strings.Contains(d["urlHint"].(string), "SECRET123") {
		t.Fatalf("hint %v", d["urlHint"])
	}
	out := subCall(t, e.h, "source/create", map[string]any{"name": "机场A", "fetchId": d["fetchId"]}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	id := int64(dataMap(t, out)["id"].(float64))
	var row model.UserSubSource
	e.r.DB().First(&row, id)
	if strings.Contains(row.URLEnc, "SECRET") || row.NodeCount != 2 || !strings.Contains(row.CachedNodes, `"i":true`) {
		t.Fatalf("stored source: %+v", row)
	}
	if plain, err := e.h.subCrypto().Decrypt(row.URLEnc); err != nil || string(plain) != subURL {
		t.Fatalf("decrypt: %v", err)
	}
	// The preview is single-use and per user.
	if out := subCall(t, e.h, "source/create", map[string]any{"fetchId": d["fetchId"]}, 2, 1); out.Code == 0 {
		t.Fatal("preview reused")
	}
	list := subCall(t, e.h, "list", nil, 2, 1)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "SECRET123") || strings.Contains(string(raw), "tpw") {
		t.Fatal("list leaks the URL or node secrets")
	}
	// Cap 3 sources.
	addSource(t, e, 2, "b")
	addSource(t, e, 2, "c")
	pre = subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1)
	if pre.Code != 0 {
		// The landing importer's 5/min limit is shared: 4 fetches so far.
		t.Fatalf("4th fetch: %+v", pre)
	}
	if out := subCall(t, e.h, "source/create", map[string]any{"fetchId": dataMap(t, pre)["fetchId"]}, 2, 1); out.Code == 0 {
		t.Fatal("4th source accepted")
	}
	if out := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1); out.Code != 0 {
		t.Fatalf("5th fetch in a minute: %+v", out)
	}
	if out := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "太频繁") {
		t.Fatalf("6th fetch in a minute: %+v", out)
	}
	if src.hits.Load() != 5 {
		t.Fatalf("hits %d", src.hits.Load())
	}
	// Another user cannot refresh or delete it.
	if out := subCall(t, e.h, "source/delete", map[string]any{"id": id}, 3, 1); out.Code == 0 {
		t.Fatal("user 3 deleted user 2's source")
	}
}

func TestSubSourceLazyRefreshAndFailure(t *testing.T) {
	e := subEnv(t)
	src := newSubSource(t, socksLines("A1", "A2"))
	id := addSource(t, e, 2, "机场A")
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{
		map[string]any{"kind": "source_all", "sourceId": id},
		map[string]any{"kind": "source_node", "sourceId": id, "name": "A1"}, // dropped: the group is followed
	}}, 2, 1))
	token := sub["token"].(string)
	var stored model.UserSubscription
	e.r.DB().First(&stored, int64(sub["id"].(float64)))
	if len(sublink.ParseRefs(stored.Items)) != 1 {
		t.Fatalf("items %s", stored.Items)
	}
	// Fresh cache: no fetch.
	before := src.hits.Load()
	if w := subPull(e.h, token, "clash.meta", "203.0.113.5"); w.Code != 200 || !strings.Contains(w.Body.String(), "A2") {
		t.Fatalf("pull %d", w.Code)
	}
	if src.hits.Load() != before {
		t.Fatal("fresh source refetched")
	}
	// 7 h old → refetched inline, new nodes served.
	src.set(socksLines("B1", "B2", "B3"), 200)
	e.exec("UPDATE user_sub_source SET last_fetch_at = ? WHERE id = ?", time.Now().Add(-7*time.Hour).UnixMilli(), id)
	w := subPull(e.h, token, "sing-box", "203.0.113.5")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "B3") || strings.Contains(w.Body.String(), "A1") {
		t.Fatalf("stale source not refreshed: %s", w.Body.String())
	}
	if src.hits.Load() != before+1 {
		t.Fatalf("hits %d", src.hits.Load())
	}
	// Failure: old nodes stay, error recorded, no retry within the hour.
	src.set("oops", 500)
	e.exec("UPDATE user_sub_source SET last_fetch_at = ? WHERE id = ?", time.Now().Add(-7*time.Hour).UnixMilli(), id)
	w = subPull(e.h, token, "Shadowrocket", "203.0.113.5")
	if w.Code != 200 {
		t.Fatalf("failed source broke the pull: %d", w.Code)
	}
	var row model.UserSubSource
	e.r.DB().First(&row, id)
	if row.LastError == "" || row.NodeCount != 3 || !strings.Contains(row.CachedNodes, "B2") {
		t.Fatalf("after failure: %+v", row)
	}
	hits := src.hits.Load()
	e.h.subState().cache.purgeSub(int64(sub["id"].(float64)))
	subPull(e.h, token, "clash.meta", "203.0.113.5")
	if src.hits.Load() != hits {
		t.Fatal("failed source retried at once")
	}
	if subSourceStale(row, time.Now().Add(61*time.Minute)) != true || subSourceStale(model.UserSubSource{LastFetchAt: time.Now().UnixMilli()}, time.Now().Add(5*time.Hour)) {
		t.Fatal("stale rules")
	}
}

func TestSubSourceSingleFlight(t *testing.T) {
	e := subEnv(t)
	src := newSubSource(t, socksLines("A1"))
	id := addSource(t, e, 2, "机场A")
	var row model.UserSubSource
	e.r.DB().First(&row, id)
	block := make(chan struct{})
	src.mu.Lock()
	src.block = block
	src.mu.Unlock()
	before := src.hits.Load()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.h.subFetchSource(context.Background(), row)
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(block)
	wg.Wait()
	if got := src.hits.Load() - before; got != 1 {
		t.Fatalf("%d downloads for 8 concurrent refreshes", got)
	}
}

// seedSubPanel: user 2 owns a landing node behind rule 901 (entry node
// 602) and its 直连 link; user 3 owns rule 903.
func seedSubPanel(t *testing.T, e *flowTestEnv) {
	t.Helper()
	key := func(b byte) string { return "AQEBAQEBAQEBAQEBAQEBAQ==" }
	for _, row := range []any{
		&model.Node{ID: 601, Name: "东京落地", Secret: "s601", ServerIP: "203.0.113.21", Port: "10000-19999"},
		&model.Node{ID: 602, Name: "上海联通入口", Secret: "s602", ServerIP: "198.51.100.10", Port: "20000-29999"},
		&model.Tunnel{ID: 811, Name: "沪日440", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1},
		&model.ChainTunnel{TunnelID: 811, NodeID: 602, ChainType: "1"},
		&model.LandingServer{ID: 11, Source: "native", NodeID: 601, Name: "东京", PublicHost: "203.0.113.21", FlowType: 2, TrafficRatio: 1, Status: "active"},
		&model.LandingNode{ID: 22, UserID: 2, LandingServerID: 11, Name: "游戏加速", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-22", Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key(1), UserKey: key(2)},
		&model.LandingNode{ID: 23, UserID: 3, LandingServerID: 11, Name: "B", Protocol: "ss2022", Port: 30003, InboundTag: "flvxl-23", Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key(3), UserKey: key(4)},
		&model.Forward{ID: 901, UserID: 2, UserName: "a", Name: "游戏加速", TunnelID: 811, Status: 1, LandingNodeID: 22, RemoteAddr: "203.0.113.21:30002"},
		&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 20009},
		&model.Forward{ID: 903, UserID: 3, UserName: "b", Name: "B", TunnelID: 811, Status: 1, LandingNodeID: 23, RemoteAddr: "203.0.113.21:30003"},
		&model.ForwardPort{ForwardID: 903, NodeID: 602, Port: 20011},
	} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
}

func TestSubResolveMissingAndAddressChange(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	ext := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home.example.net", Port: 1080}
	e.r.DB().Create(&model.LandingExternalNode{ID: 31, UserID: 2, Name: "家", Upstream: ext.JSON()})
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"},
		map[string]any{"kind": "landing_direct", "landingId": 22},
		map[string]any{"kind": "external_node", "externalId": 31},
	}}, 2, 1))
	id := int64(sub["id"].(float64))
	// The entry node's address changes: the rule link still resolves.
	e.exec("UPDATE node SET server_ip = '198.51.100.99' WHERE id = 602")
	// The external node is deleted: counted as 失效.
	e.exec("DELETE FROM landing_external_node WHERE id = 31")
	list := dataMap(t, subCall(t, e.h, "list", nil, 2, 1))
	counts := list["subs"].([]any)[0].(map[string]any)["counts"].(map[string]any)
	if counts["panel"].(float64) != 2 || counts["missing"].(float64) != 1 {
		t.Fatalf("counts %v", counts)
	}
	w := subPull(e.h, sub["token"].(string), "Shadowrocket", "203.0.113.6")
	if !strings.Contains(decodeB64(t, w.Body.String()), "198.51.100.99:20009") {
		t.Fatalf("new entry address not served")
	}
	// Pool shows the missing row; saving drops it.
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	sel := pool["selected"].([]any)
	if len(sel) != 3 || sel[2].(map[string]any)["missing"] != true {
		t.Fatalf("selected %v", sel)
	}
	items := []any{}
	for _, s := range sel {
		items = append(items, s.(map[string]any)["ref"])
	}
	subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": items}, 2, 1)
	var row model.UserSubscription
	e.r.DB().First(&row, id)
	refs := sublink.ParseRefs(row.Items)
	if len(refs) != 2 || refs[0].Entry != "602|198.51.100.99" {
		t.Fatalf("saved items %s", row.Items)
	}
	// 300-node cap on save.
	many := []any{}
	for i := 0; i < 301; i++ {
		many = append(many, map[string]any{"kind": "landing_direct", "landingId": 22})
	}
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "items": many}, 2, 1); out.Code == 0 {
		t.Fatal("301 items accepted")
	}
}

func decodeB64(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSubPullLimitsAndCache(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{map[string]any{"kind": "landing_direct", "landingId": 22}}}, 2, 1))
	token := sub["token"].(string)
	// 60 pulls an hour per subscription.
	for i := 0; i < 60; i++ {
		if w := subPull(e.h, token, "clash.meta", "203.0.113.7"); w.Code != 200 {
			t.Fatalf("pull %d: %d", i, w.Code)
		}
	}
	if w := subPull(e.h, token, "clash.meta", "203.0.113.8"); w.Code != 429 {
		t.Fatalf("61st pull %d", w.Code)
	}
	// Unknown tokens: 10 misses per address, then blocked.
	for i := 0; i < 10; i++ {
		if w := subPull(e.h, strings.Repeat("Z", 22), "clash", "203.0.113.9"); w.Code != 404 {
			t.Fatalf("miss %d: %d", i, w.Code)
		}
	}
	if w := subPull(e.h, strings.Repeat("Z", 22), "clash", "203.0.113.9"); w.Code != 429 {
		t.Fatalf("11th miss %d", w.Code)
	}
	// Cache: bounded by entries and bytes, expires, purges.
	c := newSubRenderCache(4, 100, time.Minute)
	for i := 0; i < 6; i++ {
		c.put(&subRender{key: strconv.Itoa(i), body: make([]byte, 10), subID: int64(i % 2), at: time.Now()})
	}
	if n, b := c.stats(); n != 4 || b != 40 {
		t.Fatalf("cache %d entries %d bytes", n, b)
	}
	c.put(&subRender{key: "big", body: make([]byte, 95), at: time.Now()})
	if n, b := c.stats(); b > 100 || n != 1 {
		t.Fatalf("byte cap: %d entries %d bytes", n, b)
	}
	c.put(&subRender{key: "x", subID: 7, body: []byte("1"), at: time.Now().Add(-2 * time.Minute)})
	if c.get("x") != nil {
		t.Fatal("expired entry served")
	}
	c.put(&subRender{key: "y", subID: 7, body: []byte("1"), at: time.Now()})
	c.purgeSub(7)
	if c.get("y") != nil {
		t.Fatal("purge")
	}
}

// TestSubPullQueries measures the DB statements of one pull (cold render vs
// 60 s cache hit) for the resource report.
func TestSubPullQueries(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	src := newSubSource(t, socksLines("A1", "A2"))
	_ = src
	id := addSource(t, e, 2, "机场A")
	ext := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home.example.net", Port: 1080}
	e.r.DB().Create(&model.LandingExternalNode{ID: 31, UserID: 2, Name: "家", Upstream: ext.JSON()})
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"},
		map[string]any{"kind": "landing_direct", "landingId": 22},
		map[string]any{"kind": "external_node", "externalId": 31},
		map[string]any{"kind": "source_all", "sourceId": id},
	}}, 2, 1))
	var n atomic.Int32
	count := func(*gorm.DB) { n.Add(1) }
	cb := e.r.DB().Callback()
	_ = cb.Query().After("gorm:query").Register("sub:count", count)
	_ = cb.Row().After("gorm:row").Register("sub:count", count)
	_ = cb.Raw().After("gorm:raw").Register("sub:count", count)
	_ = cb.Update().After("gorm:update").Register("sub:count", count)
	token := sub["token"].(string)
	subPull(e.h, token, "clash.meta", "203.0.113.10")
	cold := n.Load()
	n.Store(0)
	subPull(e.h, token, "clash.meta", "203.0.113.10")
	warm := n.Load()
	t.Logf("DB statements per pull: cold render %d, cached %d", cold, warm)
	if cold > 16 || warm > 4 {
		t.Fatalf("too many statements: cold %d, cached %d", cold, warm)
	}
}

func TestMaskSourceURL(t *testing.T) {
	cases := map[string]string{
		"https://sub.provider-a.example/api/v1/client/subscribe?token=abc": "sub.provider-a.example/api/…?token=••••",
		"https://sub.provider-b.example/link/Ab12Cd34?clash=1":             "sub.provider-b.example/link/…?clash=••••",
		"https://x.example/9f8e7d6c5b4a3":                                  "x.example/…",
		"http://1.2.3.4:8080/sub":                                          "1.2.3.4:8080/sub",
	}
	for in, want := range cases {
		if got := maskSourceURL(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}
