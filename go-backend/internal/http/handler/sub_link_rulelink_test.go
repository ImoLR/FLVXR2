package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/landingsub"
	"go-backend/internal/ratelimit"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 261 「规则集链接」.

const linkHost = "rules.example.net"

const linkClashYAML = `payload:
  - DOMAIN-SUFFIX,netflix.com
  - DOMAIN,fast.com
  - DOMAIN-KEYWORD,nflx
  - IP-CIDR,23.246.0.0/18,no-resolve
  - PROCESS-NAME,Netflix
  - URL-REGEX,^https?://x
`

// linkServer serves rule lists by path; the fetcher reaches it through the
// SSRF guard as the public address 198.51.124.40.
type linkServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string]string
	gzip  map[string]bool
	block chan struct{}
	hits  atomic.Int32
	times []time.Time
}

func newLinkServer(t *testing.T) *linkServer {
	t.Helper()
	s := &linkServer{files: map[string]string{"/netflix.yaml": linkClashYAML}, gzip: map[string]bool{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		s.times = append(s.times, time.Now())
		body, ok := s.files[r.URL.Path]
		gz, block := s.gzip[r.URL.Path], s.block
		s.mu.Unlock()
		if block != nil {
			<-block
		}
		switch {
		case r.URL.Path == "/to-private":
			http.Redirect(w, r, "http://10.0.0.7/x.list", http.StatusFound)
			return
		case r.URL.Path == "/to-loop":
			http.Redirect(w, r, "http://"+linkHost+"/to-loop", http.StatusFound)
			return
		case !ok:
			http.NotFound(w, r)
			return
		}
		if gz {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte(body))
			_ = zw.Close()
			return
		}
		fmt.Fprint(w, body)
	}))
	old := subFetcher
	subFetcher = func(h *Handler) *landingsub.Fetcher {
		f := &landingsub.Fetcher{Resolver: fakeChainResolver{
			linkHost: {"198.51.124.40"}, "rules2.example.net": {"198.51.124.41"},
			"inside.example.net": {"10.1.2.3"}, "meta.example.net": {"169.254.169.254"}, "ours.example.net": {"198.51.124.200"},
		}, Forbidden: h.chainForbidden()}
		f.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, s.srv.Listener.Addr().String())
		}
		return f
	}
	t.Cleanup(func() { subFetcher = old; s.srv.Close() })
	return s
}

func (s *linkServer) set(path, body string) {
	s.mu.Lock()
	s.files[path] = body
	s.mu.Unlock()
}

func linkURL(path string) string { return "http://" + linkHost + path + "?token=TOPSECRET42" }

// linkEnv: subEnv with an unlimited fetch rate (the rate limit has its own test).
func linkEnv(t *testing.T) (*flowTestEnv, *linkServer) {
	e := subEnv(t)
	s := newLinkServer(t)
	e.h.external.init()
	e.h.external.fetchRL = &ratelimit.Bucket{Burst: 1000, Every: time.Millisecond, MaxKeys: 100}
	return e, s
}

func linkFetch(t *testing.T, e *flowTestEnv, uid int64, url string) (map[string]any, string) {
	t.Helper()
	out := subCall(t, e.h, "ruleset/fetch", map[string]any{"url": url}, uid, 1)
	if out.Code != 0 {
		return nil, out.Msg
	}
	return dataMap(t, out), ""
}

func linkSet(t *testing.T, e *flowTestEnv, id int64) model.UserSubRuleSet {
	t.Helper()
	var s model.UserSubRuleSet
	if err := e.r.DB().First(&s, id).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func linkCount(e *flowTestEnv, uid int64) int64 {
	var n int64
	e.r.DB().Model(&model.UserSubRuleSet{}).Where("user_id = ?", uid).Count(&n)
	return n
}

func linkGet(e *flowTestEnv, path string, hdr map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	e.h.registerSubLink(mux)
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "203.0.113.31:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func linkRoute(name string, ref int64, exit string) map[string]any {
	return map[string]any{"type": "link", "name": name, "ref": ref, "exit": exit}
}

func TestSubLinkFetchSaveServe(t *testing.T) {
	e, srv := linkEnv(t)
	data, msg := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	if msg != "" {
		t.Fatal(msg)
	}
	id := int64(data["id"].(float64))
	counts := data["counts"].(map[string]any)
	if data["ruleCount"] != float64(4) || counts["ignored"] != float64(2) || counts["suffix"] != float64(1) || counts["ip"] != float64(1) ||
		len(data["sample"].([]any)) != 4 || data["urlHint"] != linkHost+"/…?token=••••" {
		t.Fatalf("preview %v", data)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "TOPSECRET42") || strings.Contains(string(raw), "netflix.yaml") {
		t.Fatalf("url echoed: %s", raw)
	}
	row := linkSet(t, e, id)
	if row.UserID != 2 || strings.Contains(row.URLEnc, "TOPSECRET42") || row.URLEnc == "" || !sublink.ValidLinkID(row.PublicID) ||
		row.Rules != "DOMAIN-SUFFIX,netflix.com\nDOMAIN,fast.com\nDOMAIN-KEYWORD,nflx\nIP-CIDR,23.246.0.0/18\n" || row.ContentHash != sublink.LinkHash([]byte(row.Rules)) {
		t.Fatalf("row %+v", row)
	}

	// Save a subscription with the link entry and pull it.
	sub := subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{}, "routing": []any{linkRoute("奈飞", id, "direct")}}, 2, 1)
	if sub.Code != 0 {
		t.Fatalf("create: %+v", sub)
	}
	sid := int64(dataMap(t, sub)["id"].(float64))
	if r := subStoredRouting(t, e, sid); r == nil || *r != fmt.Sprintf(`[{"type":"link","name":"奈飞","exit":"direct","ref":%d}]`, id) {
		t.Fatalf("stored routing %v", *r)
	}
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": sid}, 2, 1))
	view := pool["routingLinks"].(map[string]any)[fmt.Sprint(id)].(map[string]any)
	if view["ruleCount"] != float64(4) || view["urlHint"] == nil {
		t.Fatalf("pool view %v", view)
	}
	if raw, _ := json.Marshal(pool); strings.Contains(string(raw), "TOPSECRET42") || strings.Contains(string(raw), row.PublicID) {
		t.Fatalf("pool leaks url / public id: %s", raw)
	}
	token := dataMap(t, sub)["token"].(string)
	file := "https://a.example.com/s/rs/u/" + row.PublicID
	for ua, want := range map[string]string{
		"clash-verge/v2.2.3":                       "url: " + file + ".list",
		"Stash/2.4.6 Clash/1.9.0":                  "url: " + file + ".list",
		"SFA/1.12.0 (Android 14; sing-box 1.12.0)": `"url": "` + file + `.json"`,
		"Surge iOS/3075":                           "RULE-SET," + file + ".list,📌 奈飞,no-resolve",
	} {
		if body := subRoutingPull(e.h, token, ua, "a.example.com").Body.String(); !strings.Contains(body, want) {
			t.Fatalf("%s missing %q:\n%s", ua, want, body)
		}
	}

	// Serving: validated rules, nosniff, ETag / 304, sing-box source JSON.
	w := linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil)
	if w.Code != 200 || w.Body.String() != row.Rules || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("list %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if w := linkGet(e, "/s/rs/u/"+row.PublicID+".list", map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Fatalf("304: %d", w.Code)
	}
	w = linkGet(e, "/s/rs/u/"+row.PublicID+".json", nil)
	var js struct {
		Version int `json:"version"`
		Rules   []map[string][]string
	}
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &js) != nil ||
		js.Version != sublink.LinkSourceVersion || js.Rules[0]["domain_suffix"][0] != "netflix.com" || js.Rules[0]["ip_cidr"][0] != "23.246.0.0/18" ||
		w.Header().Get("ETag") == etag {
		t.Fatalf("json %d %s", w.Code, w.Body.String())
	}
	// Unknown / malformed ids: the plain 404 of any unknown /s/ path.
	notFound := httptest.NewRecorder()
	http.NotFound(notFound, httptest.NewRequest("GET", "/", nil))
	for _, p := range []string{"/s/rs/u/" + strings.Repeat("0", 32) + ".list", "/s/rs/u/" + row.PublicID + ".mrs", "/s/rs/u/" + row.PublicID,
		"/s/rs/u/" + strings.ToUpper(row.PublicID) + ".list", "/s/rs/u/x/" + row.PublicID + ".list"} {
		if w := linkGet(e, p, nil); w.Code != 404 || w.Body.String() != notFound.Body.String() {
			t.Fatalf("%s: %d %q", p, w.Code, w.Body.String())
		}
	}
	// The same URL again (another subscription of the same user) = the same
	// stored set, refreshed.
	again, msg := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	if msg != "" || int64(again["id"].(float64)) != id || linkCount(e, 2) != 1 || srv.hits.Load() != 2 {
		t.Fatalf("dedupe: %v %s n=%d hits=%d", again, msg, linkCount(e, 2), srv.hits.Load())
	}
	// Admin switch off: 403 like every /s/ path.
	e.exec("INSERT INTO vite_config (name, value, time) VALUES ('sub_link_enabled', 'false', 0)")
	if w := linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil); w.Code != http.StatusForbidden {
		t.Fatalf("disabled: %d", w.Code)
	}
}

func TestSubLinkOwnership(t *testing.T) {
	e, _ := linkEnv(t)
	data, msg := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	if msg != "" {
		t.Fatal(msg)
	}
	id := int64(data["id"].(float64))
	// User 3 can neither reference, refresh nor see user 2's set.
	if out := subCall(t, e.h, "create", map[string]any{"name": "x", "items": []any{}, "routing": []any{linkRoute("偷", id, "direct")}}, 3, 1); out.Code == 0 || !strings.Contains(out.Msg, "失效") {
		t.Fatalf("foreign ref saved: %+v", out)
	}
	if out := subCall(t, e.h, "ruleset/refresh", map[string]any{"id": id}, 3, 1); out.Code == 0 || !strings.Contains(out.Msg, "不存在") {
		t.Fatalf("foreign refresh: %+v", out)
	}
	// A stored list naming a foreign ref (written around the API) renders
	// nothing and shows nothing.
	own := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "mine", "items": []any{}, "routing": []any{}}, 3, 1))
	js := fmt.Sprintf(`[{"type":"link","name":"偷","exit":"direct","ref":%d}]`, id)
	e.r.DB().Model(&model.UserSubscription{}).Where("id = ?", own["id"]).Update("routing", js)
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": own["id"]}, 3, 1))
	if len(pool["routingLinks"].(map[string]any)) != 0 {
		t.Fatalf("foreign set visible: %v", pool["routingLinks"])
	}
	body := subRoutingPull(e.h, own["token"].(string), "clash-verge/v2.2.3", "a.example.com").Body.String()
	if strings.Contains(body, "/s/rs/u/") || strings.Contains(body, "偷") {
		t.Fatalf("foreign set rendered:\n%s", body)
	}
	// The same URL fetched by user 3 is user 3's own row.
	data3, msg := linkFetch(t, e, 3, linkURL("/netflix.yaml"))
	if msg != "" || int64(data3["id"].(float64)) == id || linkCount(e, 3) != 1 {
		t.Fatalf("user 3 fetch: %v %s", data3, msg)
	}
	if linkSet(t, e, int64(data3["id"].(float64))).PublicID == linkSet(t, e, id).PublicID {
		t.Fatal("public id shared")
	}
}

func TestSubLinkLimitsAndContent(t *testing.T) {
	e, srv := linkEnv(t)
	big := strings.Repeat("DOMAIN-SUFFIX,a.example\n", (sublink.MaxLinkBytes/24)+10)
	srv.set("/big.list", big)
	srv.set("/bomb.list", big)
	srv.gzip["/bomb.list"] = true
	srv.set("/gz-ok.list", linkClashYAML)
	srv.gzip["/gz-ok.list"] = true
	srv.set("/rules.mrs", "\x28\xb5\x2f\xfd\x00\x00binary")
	srv.set("/rules.srs", "SRS\x03\x00\x01")
	srv.set("/page.html", "<!DOCTYPE html><html>DOMAIN,x.com</html>")
	srv.set("/empty.list", "# nothing\nGEOIP,CN\nPROCESS-NAME,x\n")
	var many strings.Builder
	for i := 0; i <= sublink.MaxLinkRules; i++ {
		fmt.Fprintf(&many, "d%d.example\n", i)
	}
	srv.set("/many.list", many.String())
	for path, want := range map[string]string{
		"/big.list": "太大", "/bomb.list": "太大", "/rules.mrs": "不支持 .mrs / .srs", "/rules.srs": "不支持 .mrs / .srs",
		"/page.html": "网页", "/empty.list": "没有可用的规则", "/many.list": "规则太多", "/missing.list": "HTTP 404",
	} {
		if _, msg := linkFetch(t, e, 2, linkURL(path)); !strings.Contains(msg, want) {
			t.Fatalf("%s: %q want %q", path, msg, want)
		}
	}
	if n := linkCount(e, 2); n != 0 {
		t.Fatalf("stored %d after failures", n)
	}
	// gzip transfer encoding is fine (cap counts decoded bytes).
	if d, msg := linkFetch(t, e, 2, linkURL("/gz-ok.list")); msg != "" || d["ruleCount"] != float64(4) {
		t.Fatalf("gzip ok: %v %s", d, msg)
	}
	// 10 sets per user; the 11th URL is refused before any download.
	for i := 1; linkCount(e, 2) < model.MaxSubRuleSetsPerUser; i++ {
		path := fmt.Sprintf("/n%d.list", i)
		srv.set(path, fmt.Sprintf("DOMAIN-SUFFIX,n%d.example\n", i))
		if _, msg := linkFetch(t, e, 2, linkURL(path)); msg != "" {
			t.Fatal(msg)
		}
	}
	srv.set("/eleven.list", "DOMAIN,x.example\n")
	before := srv.hits.Load()
	if _, msg := linkFetch(t, e, 2, linkURL("/eleven.list")); !strings.Contains(msg, "最多保存 10 个") || srv.hits.Load() != before {
		t.Fatalf("cap: %q hits %d→%d", msg, before, srv.hits.Load())
	}
	// Re-fetching a URL the user already has still works at the cap.
	if _, msg := linkFetch(t, e, 2, linkURL("/gz-ok.list")); msg != "" {
		t.Fatalf("existing at cap: %s", msg)
	}
	// 5 link entries per subscription.
	var sets []model.UserSubRuleSet
	e.r.DB().Where("user_id = ?", 2).Order("id").Find(&sets)
	var routing []any
	for i, s := range sets[:6] {
		routing = append(routing, linkRoute(fmt.Sprintf("l%d", i), s.ID, "select"))
	}
	if out := subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{}, "routing": routing}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "最多 5 个规则集链接") {
		t.Fatalf("6 links: %+v", out)
	}
	if out := subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{}, "routing": routing[:5]}, 2, 1); out.Code != 0 {
		t.Fatalf("5 links: %+v", out)
	}
	// Per-user fetch rate limit (the subscription source limit).
	e.h.external.fetchRL = &ratelimit.Bucket{Burst: 1, Every: time.Hour, MaxKeys: 100}
	linkFetch(t, e, 3, linkURL("/netflix.yaml"))
	if _, msg := linkFetch(t, e, 3, linkURL("/netflix.yaml")); !strings.Contains(msg, "太频繁") {
		t.Fatalf("rate limit: %q", msg)
	}
	subCall(t, e.h, "ruleset/refresh", map[string]any{"id": sets[0].ID}, 2, 1)
	if out := subCall(t, e.h, "ruleset/refresh", map[string]any{"id": sets[0].ID}, 2, 1); !strings.Contains(out.Msg, "太频繁") {
		t.Fatalf("refresh rate limit: %+v", out)
	}
}

func TestSubLinkSSRF(t *testing.T) {
	e, srv := linkEnv(t)
	e.addNode(2, "s")
	e.exec("UPDATE node SET server_ip = '198.51.124.200' WHERE id = 2")
	for _, c := range []struct{ url, want string }{
		{"http://10.0.0.1/x.list", "不能指向"},
		{"http://127.0.0.1:8080/x.list", "不能指向"},
		{"http://[::1]/x.list", "不能指向"},
		{"http://169.254.169.254/latest/meta-data", "不能指向"},
		{"http://[fe80::1]/x.list", "不能指向"},
		{"http://inside.example.net/x.list", "不能指向"},
		{"http://meta.example.net/x.list", "不能指向"},
		{"http://ours.example.net/x.list", "不能指向"},     // an FLVX node
		{"http://198.51.124.200/x.list", "不能指向"},       // the node by IP
		{"http://" + linkHost + "/to-private", "不能指向"}, // redirect into the LAN
		{"http://" + linkHost + "/to-loop", "跳转次数太多"},
		{"http://u:p@" + linkHost + "/x.list", "不能包含用户名或密码"},
		{"file:///etc/passwd", "规则集地址无效"},
		{"ftp://" + linkHost + "/x", "只支持 http"},
		{"gopher://" + linkHost + "/x", "只支持 http"},
		{"http://nxdomain.example.net/x", "无法解析"},
		{"http://" + linkHost + "/" + strings.Repeat("a", 2100), "过长"},
	} {
		before := srv.hits.Load()
		_, msg := linkFetch(t, e, 2, c.url)
		if !strings.Contains(c.url, linkHost) && srv.hits.Load() != before {
			t.Fatalf("%s reached the server", c.url)
		}
		if !strings.Contains(msg, c.want) {
			t.Fatalf("%s: %q want %q", c.url, msg, c.want)
		}
		for _, leak := range []string{"10.0.0", "127.0.0.1", "169.254", "198.51.124", "节点 ", "FLVX"} {
			if strings.Contains(msg, leak) && !strings.Contains(c.url, leak) {
				t.Fatalf("%s: message leaks %q: %s", c.url, leak, msg)
			}
		}
	}
	if linkCount(e, 2) != 0 {
		t.Fatal("stored after refusals")
	}
}

func TestSubLinkRefreshQueue(t *testing.T) {
	old := subLinkHostGap
	subLinkHostGap = 300 * time.Millisecond
	t.Cleanup(func() { subLinkHostGap = old })
	e, srv := linkEnv(t)
	data, _ := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	id := int64(data["id"].(float64))
	row := linkSet(t, e, id)
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{}, "routing": []any{linkRoute("奈飞", id, "direct")}}, 2, 1))
	stale := func(id int64, failCount int, age time.Duration) {
		e.r.DB().Model(&model.UserSubRuleSet{}).Where("id = ?", id).Updates(map[string]any{"last_try_at": time.Now().Add(-age).UnixMilli(), "fail_count": failCount})
	}
	waitIdle := func() {
		t.Helper()
		for i := 0; i < 300; i++ {
			q := e.h.subLinks()
			q.mu.Lock()
			idle := !q.running && len(q.flights) == 0
			q.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("queue never idle")
	}

	// Fresh copy: serving / pulling queues nothing.
	hits := srv.hits.Load()
	linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil)
	subRoutingPull(e.h, sub["token"].(string), "clash-verge/v2.2.3", "a.example.com")
	waitIdle()
	if srv.hits.Load() != hits {
		t.Fatal("fresh set refreshed")
	}

	// Stale + the remote is slow: the pull does not wait for it; serving and
	// pulling the same set again share one download.
	stale(id, 0, 25*time.Hour)
	srv.set("/netflix.yaml", "DOMAIN-SUFFIX,netflix.com\nDOMAIN-SUFFIX,nflxext.com\n")
	srv.mu.Lock()
	srv.block = make(chan struct{})
	srv.mu.Unlock()
	start := time.Now()
	e.h.subState().cache.purgeSub(int64(sub["id"].(float64)))
	if w := subRoutingPull(e.h, sub["token"].(string), "clash-verge/v2.2.3", "a.example.com"); w.Code != 200 || time.Since(start) > 2*time.Second {
		t.Fatalf("pull waited for the refresh: %d %v", w.Code, time.Since(start))
	}
	for i := 0; i < 3; i++ {
		linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil)
	}
	time.Sleep(100 * time.Millisecond)
	srv.mu.Lock()
	close(srv.block)
	srv.block = nil
	srv.mu.Unlock()
	waitIdle()
	if got := srv.hits.Load() - hits; got != 1 {
		t.Fatalf("downloads %d, want 1 (single flight)", got)
	}
	if r := linkSet(t, e, id); r.RuleCount != 2 || r.LastError != "" || r.FailCount != 0 || !strings.Contains(r.Rules, "nflxext.com") {
		t.Fatalf("refreshed row %+v", r)
	}
	if w := linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil); !strings.Contains(w.Body.String(), "nflxext.com") {
		t.Fatalf("served old copy: %s", w.Body.String())
	}

	// Failures keep the last good copy and back off.
	good := linkSet(t, e, id).Rules
	for _, bad := range []string{"\x28\xb5\x2f\xfd\x00binary", "# empty\n", "<html>x</html>"} {
		srv.set("/netflix.yaml", bad)
		stale(id, linkSet(t, e, id).FailCount, 8*24*time.Hour)
		linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil)
		waitIdle()
		if r := linkSet(t, e, id); r.Rules != good || r.LastError == "" {
			t.Fatalf("after %q: %+v", bad, r)
		}
	}
	if r := linkSet(t, e, id); r.FailCount != 3 {
		t.Fatalf("fail count %d", r.FailCount)
	}
	// 3 failures → next try after 96 h, not after 24 h.
	stale(id, 3, 30*time.Hour)
	hits = srv.hits.Load()
	linkGet(e, "/s/rs/u/"+row.PublicID+".list", nil)
	waitIdle()
	if srv.hits.Load() != hits {
		t.Fatal("backoff ignored")
	}
	// 立即更新 reports the failure and keeps the copy.
	out := dataMap(t, subCall(t, e.h, "ruleset/refresh", map[string]any{"id": id}, 2, 1))
	if out["error"] == nil || out["ruleCount"] != float64(2) {
		t.Fatalf("refresh now: %v", out)
	}
	srv.set("/netflix.yaml", linkClashYAML)
	out = dataMap(t, subCall(t, e.h, "ruleset/refresh", map[string]any{"id": id}, 2, 1))
	if out["error"] != nil || out["ruleCount"] != float64(4) || out["failCount"] != float64(0) || len(out["sample"].([]any)) != 4 {
		t.Fatalf("refresh ok: %v", out)
	}

	// Two due sets on one host: one download at a time, ≥ the host gap apart.
	srv.set("/b.list", "DOMAIN,b.example\n")
	d2, _ := linkFetch(t, e, 2, linkURL("/b.list"))
	id2 := int64(d2["id"].(float64))
	stale(id, 0, 25*time.Hour)
	stale(id2, 0, 25*time.Hour)
	srv.mu.Lock()
	srv.times = nil
	srv.mu.Unlock()
	e.h.subLinkEnqueue(linkSet(t, e, id), linkSet(t, e, id2))
	waitIdle()
	srv.mu.Lock()
	times := append([]time.Time(nil), srv.times...)
	srv.mu.Unlock()
	if len(times) != 2 || times[1].Sub(times[0]) < subLinkHostGap {
		t.Fatalf("host gap: %v", times)
	}
}

func TestSubLinkDue(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		fails int
		age   time.Duration
		due   bool
	}{
		{0, 23 * time.Hour, false}, {0, 24 * time.Hour, true},
		{1, 23 * time.Hour, false}, {1, 24 * time.Hour, true},
		{2, 47 * time.Hour, false}, {2, 48 * time.Hour, true},
		{3, 95 * time.Hour, false}, {3, 96 * time.Hour, true},
		{4, 167 * time.Hour, false}, {4, 168 * time.Hour, true},
		{40, 167 * time.Hour, false}, {40, 168 * time.Hour, true},
	} {
		s := model.UserSubRuleSet{FailCount: c.fails, LastTryAt: now.Add(-c.age).UnixMilli()}
		if subLinkDue(s, now) != c.due {
			t.Fatalf("%+v", c)
		}
	}
	if !subLinkDue(model.UserSubRuleSet{}, now) {
		t.Fatal("never tried")
	}
}

func TestSubLinkGC(t *testing.T) {
	e, srv := linkEnv(t)
	srv.set("/b.list", "DOMAIN,b.example\n")
	a, _ := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	b, _ := linkFetch(t, e, 2, linkURL("/b.list"))
	ida, idb := int64(a["id"].(float64)), int64(b["id"].(float64))
	s1 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "1", "items": []any{}, "routing": []any{linkRoute("A", ida, "direct"), linkRoute("B", idb, "direct")}}, 2, 1))
	s2 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "2", "items": []any{}, "routing": []any{linkRoute("A", ida, "select")}}, 2, 1))
	old := time.Now().Add(-2 * subLinkPending).UnixMilli()
	e.r.DB().Model(&model.UserSubRuleSet{}).Where("user_id = ?", 2).Update("created_time", old)
	// B removed from sub 1: deleted; A still used by sub 2: kept.
	if out := subCall(t, e.h, "update", map[string]any{"id": s1["id"], "name": "1", "items": []any{}, "routing": []any{linkRoute("A", ida, "direct")}}, 2, 1); out.Code != 0 {
		t.Fatalf("update: %+v", out)
	}
	if linkCount(e, 2) != 1 {
		t.Fatalf("after update: %d", linkCount(e, 2))
	}
	pidB := linkGet(e, "/s/rs/u/x.list", nil) // (unrelated miss)
	_ = pidB
	// A just-fetched, unsaved set survives a save (pending window).
	srv.set("/c.list", "DOMAIN,c.example\n")
	c, _ := linkFetch(t, e, 2, linkURL("/c.list"))
	subCall(t, e.h, "update", map[string]any{"id": s2["id"], "name": "2", "items": []any{}, "routing": []any{linkRoute("A", ida, "select")}}, 2, 1)
	if linkCount(e, 2) != 2 {
		t.Fatalf("pending set removed: %d", linkCount(e, 2))
	}
	// Deleting the last subscription that uses A deletes A.
	subCall(t, e.h, "delete", map[string]any{"id": s1["id"]}, 2, 1)
	if linkCount(e, 2) != 2 {
		t.Fatal("A deleted while sub 2 uses it")
	}
	subCall(t, e.h, "delete", map[string]any{"id": s2["id"]}, 2, 1)
	var left []model.UserSubRuleSet
	e.r.DB().Where("user_id = ?", 2).Find(&left)
	if len(left) != 1 || left[0].ID != int64(c["id"].(float64)) {
		t.Fatalf("after deletes: %+v", left)
	}
	// User delete removes every set of the user.
	if err := e.r.DeleteUserCascade(2); err != nil {
		t.Fatal(err)
	}
	if linkCount(e, 2) != 0 {
		t.Fatal("sets survive user delete")
	}
}

// The list a client downloads never carries anything from the remote file
// but validated rules, even for hostile lists.
func TestSubLinkServedContentIsNormalized(t *testing.T) {
	e, srv := linkEnv(t)
	hostile := "payload:\n  - 'DOMAIN-SUFFIX,ok.example,DIRECT'\n  - \"DOMAIN,x.com\\\"\\nMATCH,DIRECT\"\n  - 'RULE-SET,https://evil.example/x,REJECT'\n" +
		"proxies:\n  - {name: evil, type: ss}\n  - 'DOMAIN,a.com: b'\n  - '}{]['\n  - 'IP-CIDR,1.2.3.4/24,DIRECT,no-resolve'\n"
	srv.set("/hostile.yaml", hostile)
	d, msg := linkFetch(t, e, 2, linkURL("/hostile.yaml"))
	if msg != "" {
		t.Fatal(msg)
	}
	row := linkSet(t, e, int64(d["id"].(float64)))
	if row.Rules != "DOMAIN-SUFFIX,ok.example\nIP-CIDR,1.2.3.0/24\n" {
		t.Fatalf("stored %q", row.Rules)
	}
	for _, ext := range []string{"list", "json"} {
		body := linkGet(e, "/s/rs/u/"+row.PublicID+"."+ext, nil).Body.Bytes()
		for _, bad := range []string{"MATCH", "evil", "proxies", "REJECT", "DIRECT", "}{"} {
			if bytes.Contains(body, []byte(bad)) {
				t.Fatalf("%s leaks %q: %s", ext, bad, body)
			}
		}
	}
}

// Plan 260 + 261: the admin's read-only detail shows a link entry as name +
// 🔗 + count + status only — never the URL, its hint or the set's ids.
func TestSubLinkAdminDetailNoURL(t *testing.T) {
	e, _ := linkEnv(t)
	data, msg := linkFetch(t, e, 2, linkURL("/netflix.yaml"))
	if msg != "" {
		t.Fatal(msg)
	}
	id := int64(data["id"].(float64))
	row := linkSet(t, e, id)
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{}, "routing": []any{linkRoute("奈飞", id, "direct")}}, 2, 1))
	out := subCall(t, e.h, "admin/detail", map[string]any{"id": sub["id"]}, 1, 0)
	if out.Code != 0 {
		t.Fatalf("admin detail: %+v", out)
	}
	raw, _ := json.Marshal(out.Data)
	for _, leak := range []string{"TOPSECRET42", linkHost, "netflix.yaml", row.PublicID, row.URLHint, `"ref"`, "urlHint", "netflix.com"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("admin detail leaks %q: %s", leak, raw)
		}
	}
	r := dataMap(t, out)["routing"].([]any)[0].(map[string]any)
	if r["type"] != "link" || r["name"] != "奈飞" || r["ruleCount"] != float64(4) || r["fetchedAt"] == nil || r["linkFailed"] != nil {
		t.Fatalf("admin link row %v", r)
	}
	list, _ := json.Marshal(subCall(t, e.h, "admin/list", map[string]any{}, 1, 0).Data)
	if strings.Contains(string(list), "TOPSECRET42") || strings.Contains(string(list), linkHost) || strings.Contains(string(list), row.PublicID) {
		t.Fatalf("admin list leaks: %s", list)
	}
	// A user is still refused.
	if out := subCall(t, e.h, "admin/detail", map[string]any{"id": sub["id"]}, 2, 1); out.Code == 0 {
		t.Fatal("user read admin detail")
	}
}
