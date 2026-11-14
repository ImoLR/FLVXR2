package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 256 「分流」 + 外部节点 entry.

func subRoutingPull(h *Handler, token, ua, host string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.registerSubLink(mux)
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	req.RemoteAddr = "203.0.113.30:5555"
	req.Host = host
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func subStoredRouting(t *testing.T, e *flowTestEnv, id int64) *string {
	t.Helper()
	var s model.UserSubscription
	if err := e.r.DB().First(&s, id).Error; err != nil {
		t.Fatal(err)
	}
	return s.Routing
}

func TestSubRoutingSaveAndRender(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	e.h.subState().ruleSets.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	ext := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home.example.net", Port: 1080}
	e.r.DB().Create(&model.LandingExternalNode{ID: 31, UserID: 2, Name: "我的 VPS", Upstream: ext.JSON()})
	items := []any{map[string]any{"kind": "landing_direct", "landingId": 22}, map[string]any{"kind": "external_node", "externalId": 31}}

	// A new subscription without a list (an old editor) gets the default 8.
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": items}, 2, 1))
	id := int64(sub["id"].(float64))
	if r := subStoredRouting(t, e, id); r == nil || *r != sublink.RoutingJSON(sublink.DefaultRouting()) {
		t.Fatalf("default routing %v", r)
	}
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	if len(pool["routing"].([]any)) != 8 || len(pool["routingCatalog"].([]any)) != len(sublink.Catalog) || len(pool["routingDefaults"].([]any)) != 8 {
		t.Fatalf("pool routing %v", pool["routing"])
	}
	if lim := pool["routingLimits"].(map[string]any); lim["entries"] != float64(30) || lim["lines"] != float64(300) {
		t.Fatalf("limits %v", lim)
	}
	if fresh := dataMap(t, subCall(t, e.h, "pool", map[string]any{}, 2, 1)); len(fresh["routing"].([]any)) != 8 {
		t.Fatal("new subscription pool without defaults")
	}
	list := dataMap(t, subCall(t, e.h, "list", map[string]any{}, 2, 1))
	if v := list["subs"].([]any)[0].(map[string]any); v["routing"] != float64(8) || list["externals"] != float64(1) {
		t.Fatalf("list: %v / %v", v["routing"], list["externals"])
	}

	// Clash pull: groups + providers from the origin it was pulled from.
	token := sub["token"].(string)
	body := subRoutingPull(e.h, token, "clash-verge/v2.2.3", "a.example.com").Body.String()
	for _, want := range []string{"rule-providers:", "url: https://a.example.com/s/rs/geosite-ai.mrs", "RULE-SET,geosite-telegram,", "RULE-SET,geoip-telegram,", "format: mrs", "proxy: DIRECT"} {
		if !strings.Contains(body, want) {
			t.Fatalf("clash missing %q:\n%s", want, body)
		}
	}
	if b := subRoutingPull(e.h, token, "clash-verge/v2.2.3", "b.example.com").Body.String(); !strings.Contains(b, "https://b.example.com/s/rs/") {
		t.Fatal("render cache ignores the origin")
	}
	stash := subRoutingPull(e.h, token, "Stash/2.4.6 Clash/1.9.0", "a.example.com").Body.String()
	if !strings.Contains(stash, "format: text") || !strings.Contains(stash, "/s/rs/geosite-ai.txt") || strings.Contains(stash, "proxy: DIRECT") {
		t.Fatalf("stash providers:\n%s", stash)
	}
	sb := subRoutingPull(e.h, token, "SFA/1.12.0 (Android 14; sing-box 1.12.0)", "a.example.com").Body.String()
	if !strings.Contains(sb, `"url": "https://a.example.com/s/rs/geosite-google.srs"`) || strings.Contains(sb, "githubusercontent") {
		t.Fatal("sing-box rule sets not from the panel")
	}
	surge := subRoutingPull(e.h, token, "Surge iOS/3051", "a.example.com").Body.String()
	if !strings.Contains(surge, "RULE-SET,https://a.example.com/s/rs/geosite-youtube.list,📹 YouTube\n") {
		t.Fatal("surge rule sets")
	}
	for _, ua := range []string{"Shadowrocket/2070", "Loon/3.2", "Quantumult%20X/1.4"} {
		if b := subRoutingPull(e.h, token, ua, "a.example.com").Body.String(); strings.Contains(b, "AI") || strings.Contains(b, "/s/rs/") {
			t.Fatalf("%s got routing", ua)
		}
	}

	// Update: custom + node exits; a node not in the subscription → 节点选择.
	routing := []any{
		map[string]any{"type": "builtin", "key": "google", "exit": "node:e:99"},
		map[string]any{"type": "builtin", "key": "ai", "exit": "node:e:31"},
		map[string]any{"type": "custom", "name": "公司", "lines": []string{"mycompany.com", "10.8.0.0/16"}, "exit": "direct"},
	}
	out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": items, "routing": routing}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("update: %+v", out)
	}
	want := `[{"type":"custom","name":"公司","lines":["mycompany.com","10.8.0.0/16"],"exit":"direct"},{"type":"builtin","key":"ai","exit":"node:e:31"},{"type":"builtin","key":"google","exit":"select"}]`
	if r := subStoredRouting(t, e, id); r == nil || *r != want {
		t.Fatalf("stored %v", *r)
	}
	body = subRoutingPull(e.h, token, "mihomo/1.19", "a.example.com").Body.String()
	if !strings.Contains(body, "  - name: \"\\U0001F916 AI\"\n    type: select\n    proxies:\n      - 我的 VPS\n") {
		t.Fatalf("AI exit is not the external node:\n%s", body)
	}
	// An editor from before plan 256 (no routing field) keeps the list.
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s2", "items": items}, 2, 1); out.Code != 0 {
		t.Fatalf("%+v", out)
	}
	if r := subStoredRouting(t, e, id); r == nil || *r != want {
		t.Fatal("old editor wiped the routing")
	}
	// Errors carry the entry and line.
	for _, c := range []struct {
		routing any
		want    string
	}{
		{[]any{map[string]any{"type": "custom", "name": "x", "lines": []string{"ok.com", "not valid"}}}, "「x」第 2 行"},
		{[]any{map[string]any{"type": "custom", "name": "我的 VPS", "lines": []string{"a.com"}}}, "和订阅里的节点同名"},
		{[]any{map[string]any{"type": "builtin", "key": "ai", "exit": "elsewhere"}}, "走哪里"},
	} {
		out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": items, "routing": c.routing}, 2, 1)
		if out.Code == 0 || !strings.Contains(out.Msg, c.want) {
			t.Fatalf("%v → %+v", c.routing, out)
		}
	}
	// The external node is deleted: its subscriptions drop it at once and
	// AI falls back to 节点选择 (the 60 s render cache is purged).
	if del := layerCall(t, e.h, "/api/v1/landing/external/delete", map[string]any{"id": 31}, 2, 1); del.Code != 0 {
		t.Fatalf("delete: %+v", del)
	}
	body = subRoutingPull(e.h, token, "mihomo/1.19", "a.example.com").Body.String()
	if strings.Contains(body, "我的 VPS") || !strings.Contains(body, "  - name: \"\\U0001F916 AI\"\n    type: select\n    proxies:\n      - 节点选择\n") {
		t.Fatalf("no fallback after delete:\n%s", body)
	}
	pool = dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	if pool["routing"].([]any)[1].(map[string]any)["exit"] != "node:e:31" {
		t.Fatal("stored exit should stay (the editor shows it yellow)")
	}

	// 全部删除 = [] → the fork.131 output.
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": items, "routing": []any{}}, 2, 1); out.Code != 0 {
		t.Fatalf("%+v", out)
	}
	if b := subRoutingPull(e.h, token, "mihomo/1.19", "a.example.com").Body.String(); strings.Contains(b, "rule-providers") || strings.Contains(b, "RULE-SET") {
		t.Fatal("empty list still renders routing")
	}
	sub2 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "e", "items": items[:1], "routing": []any{}}, 2, 1))
	if r := subStoredRouting(t, e, int64(sub2["id"].(float64))); r == nil || *r != "[]" {
		t.Fatal("explicit empty list on create")
	}
}

// Subscriptions saved before plan 256 have routing NULL: output unchanged.
func TestSubRoutingNullColumn(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "routing": []any{}, "items": []any{map[string]any{"kind": "landing_direct", "landingId": 22}}}, 2, 1))
	id := int64(sub["id"].(float64))
	before := subRoutingPull(e.h, sub["token"].(string), "mihomo/1.19", "a.example.com").Body.String()
	e.exec("UPDATE user_subscription SET routing = NULL WHERE id = ?", id)
	e.h.subState().cache.purgeSub(id)
	if after := subRoutingPull(e.h, sub["token"].(string), "mihomo/1.19", "a.example.com").Body.String(); after != before {
		t.Fatal("NULL and [] render differently")
	}
	if pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1)); len(pool["routing"].([]any)) != 0 {
		t.Fatal("NULL routing shows entries in the editor")
	}
}

// Every catalog set is served in every format, never 404.
func TestSubRuleSetCatalogServed(t *testing.T) {
	e := subEnv(t)
	var fetches atomic.Int32
	e.h.subState().ruleSets.Fetch = func(context.Context, string) ([]byte, error) {
		fetches.Add(1)
		return nil, errors.New("offline")
	}
	mux := http.NewServeMux()
	e.h.registerSubLink(mux)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "203.0.113.31:5555"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	for _, c := range sublink.Catalog {
		for _, rs := range c.Sets {
			for ext, ctype := range map[string]string{"mrs": "application/octet-stream", "srs": "application/octet-stream", "txt": "text/plain; charset=utf-8", "list": "text/plain; charset=utf-8"} {
				w := get("/s/rs/" + rs.ID + "." + ext)
				if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("Content-Type") != ctype || w.Header().Get("ETag") == "" {
					t.Fatalf("%s.%s: %d %q", rs.ID, ext, w.Code, w.Header().Get("Content-Type"))
				}
			}
		}
	}
	for _, p := range []string{"/s/rs/geosite-ai.yaml", "/s/rs/geosite-cn.mrs", "/s/rs/geosite-ai", "/s/rs/category-ai-!cn.mrs", "/s/rs/x/geosite-ai.mrs"} {
		if w := get(p); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	// One download at a time, at most one per file per day (refresh queue).
	time.Sleep(50 * time.Millisecond)
	if n := fetches.Load(); n == 0 || n > 3*22+2 {
		t.Fatalf("fetches %d", n)
	}
}

// The refresh queue downloads one file at a time and keeps the old copy
// when a download is bad.
func TestRuleSetRefreshOneAtATime(t *testing.T) {
	var rs sublink.RuleSets
	var running, peak, calls atomic.Int32
	rs.Fetch = func(_ context.Context, url string) ([]byte, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		calls.Add(1)
		return []byte("<html>blocked</html>"), nil
	}
	now := time.Unix(1_900_000_000, 0)
	before := map[string]string{}
	for _, id := range []string{"geosite-ai", "geosite-google", "geoip-google", "geosite-apple"} {
		for _, ext := range []string{"mrs", "srs", "txt"} {
			_, etag, ok := rs.Get(id, ext, now)
			if !ok {
				t.Fatal(id, ext)
			}
			before[id+ext] = etag
		}
	}
	for i := 0; i < 200 && calls.Load() < 12; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 12 || peak.Load() != 1 {
		t.Fatalf("calls %d peak %d", calls.Load(), peak.Load())
	}
	for _, id := range []string{"geosite-ai", "geosite-google", "geoip-google", "geosite-apple"} {
		for _, ext := range []string{"mrs", "srs", "txt"} {
			if _, etag, _ := rs.Get(id, ext, now.Add(time.Hour)); etag != before[id+ext] {
				t.Fatalf("%s.%s replaced by a bad download", id, ext)
			}
		}
	}
	if calls.Load() != 12 {
		t.Fatal("refetched within a day")
	}
}

func TestSubExternalUsesAndProbe(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	oldResolver, oldDial := chainResolver, subProbeDial
	chainResolver = fakeChainResolver{"vps.example.net": {"198.51.124.40"}, "inside.example.net": {"10.0.0.7"}}
	var dials atomic.Int32
	subProbeDial = func(ctx context.Context, address string) (net.Conn, error) {
		dials.Add(1)
		if strings.HasSuffix(address, ":9999") {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if strings.HasSuffix(address, ":81") {
			return nil, errors.New("refused")
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	t.Cleanup(func() { chainResolver, subProbeDial = oldResolver, oldDial })
	ups := []linechain.Upstream{
		{Type: "socks", IPVersion: "system", Server: "vps.example.net", Port: 1080},
		{Type: "hysteria2", IPVersion: "system", Server: "vps.example.net", Port: 443, Password: "x"},
		{Type: "socks", IPVersion: "system", Server: "198.51.124.41", Port: 81},
		{Type: "socks", IPVersion: "system", Server: "inside.example.net", Port: 1080},
	}
	for i, up := range ups {
		e.r.DB().Create(&model.LandingExternalNode{ID: int64(41 + i), UserID: 2, Name: up.Server, Upstream: up.JSON()})
	}
	e.r.DB().Create(&model.LandingExternalNode{ID: 50, UserID: 3, Name: "other", Upstream: ups[0].JSON()})
	for i := 0; i < 2; i++ {
		subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{map[string]any{"kind": "external_node", "externalId": 41}, map[string]any{"kind": "external_node", "externalId": 43}}[:2-i]}, 2, 1)
	}
	subCall(t, e.h, "create", map[string]any{"name": "o", "items": []any{map[string]any{"kind": "external_node", "externalId": 50}}}, 3, 1)
	list := dataMap(t, layerCall(t, e.h, "/api/v1/landing/external/list", map[string]any{}, 2, 1))
	uses := map[float64]float64{}
	for _, it := range list["items"].([]any) {
		m := it.(map[string]any)
		uses[m["id"].(float64)] = m["subUses"].(float64)
	}
	if uses[41] != 2 || uses[43] != 1 || uses[42] != 0 || len(uses) != 4 {
		t.Fatalf("subUses %v", uses)
	}

	out := subCall(t, e.h, "external/probe", map[string]any{}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("probe: %+v", out)
	}
	raw, _ := json.Marshal(dataMap(t, out)["items"])
	var res []subProbeResult
	_ = json.Unmarshal(raw, &res)
	got := map[int64]subProbeResult{}
	for _, r := range res {
		got[r.ID] = r
	}
	if len(res) != 4 || !got[41].OK || got[41].Ms < 1 || !got[42].UDP || got[42].OK || got[43].Error != "连接失败" || got[44].OK || got[44].Error == "" {
		t.Fatalf("probe results %+v", res)
	}
	if dials.Load() != 2 {
		t.Fatalf("dials %d (private address must not be dialled)", dials.Load())
	}
	for i := 0; i < 2; i++ {
		subCall(t, e.h, "external/probe", map[string]any{}, 2, 1)
	}
	if out := subCall(t, e.h, "external/probe", map[string]any{}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "太频繁") {
		t.Fatalf("rate limit: %+v", out)
	}
}
