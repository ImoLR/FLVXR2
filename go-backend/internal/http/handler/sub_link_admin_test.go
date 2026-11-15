package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 260: the admin's read-only 「全部用户」 view.

func TestSubAdminView(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	e.addUser(4) // no subscription, no source: not listed
	e.exec("UPDATE user SET user = 'alice' WHERE id = 2")
	e.exec("UPDATE user SET user = 'bob' WHERE id = 3")
	ext := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home.example.net", Port: 1080, Username: "u", Password: "ext-secret-pw"}
	e.r.DB().Create(&model.LandingExternalNode{ID: 31, UserID: 2, Name: "我的 VPS", Upstream: ext.JSON()})

	// A STALE source of alice: a normal pull would refetch it; the admin view
	// must never do that.
	src := newSubSource(t, socksLines("N1", "N2"))
	hy2 := linechain.Upstream{Type: "hysteria2", IPVersion: "system", Server: "hk1.provider.example", Port: 443, Password: "airport-pw",
		Security: "tls", TLS: &linechain.TLS{ServerName: "hk1.provider.example"}}
	cached, _ := json.Marshal([]sublink.SourceNode{{Name: "🇭🇰 香港 01", Up: hy2}, {Name: "🇯🇵 日本 02", Up: hy2}})
	sealed, err := e.h.subCrypto().Encrypt([]byte(subURL))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * time.Hour).UnixMilli()
	source := &model.UserSubSource{UserID: 2, Name: "机场A", URLEnc: sealed, URLHint: "sub.example.net/api/…?token=••••", LastOkAt: old,
		LastFetchAt: old, LastError: `拉取订阅失败 http://sub.example.net/x?token=SECRET123 refused`, CachedNodes: string(cached), NodeCount: 2}
	if err := e.r.DB().Create(source).Error; err != nil {
		t.Fatal(err)
	}

	// alice: rule + 直连 + 外部节点 + whole source, 分流 = one custom row to the
	// 直连 node + AI; then a NULL one. bob: the default 8 (create default).
	landingKey := sublink.Ref{Kind: sublink.RefLandingDirect, LandingID: 22}.Key()
	s1 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "日常", "format": "clash", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"},
		map[string]any{"kind": "landing_direct", "landingId": 22},
		map[string]any{"kind": "external_node", "externalId": 31},
		map[string]any{"kind": "source_all", "sourceId": source.ID},
	}, "routing": []any{
		map[string]any{"type": "custom", "name": "公司", "lines": []string{"example.com", "10.8.0.0/16"}, "exit": "node:" + landingKey},
		map[string]any{"type": "builtin", "key": "ai", "exit": "select"},
	}}, 2, 1))
	s2 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "旧的"}, 2, 1))
	e.exec("UPDATE user_subscription SET routing = NULL WHERE id = ?", s2["id"])
	s3 := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "bob 的", "items": []any{
		map[string]any{"kind": "landing_direct", "landingId": 23},
	}}, 3, 1))
	now := time.Now().UnixMilli()
	e.exec("UPDATE user_subscription SET last_pull_at = ?, last_pull_client = 'mihomo', last_pull_nodes = 5 WHERE id = ?", now-2*3600_000, s3["id"])
	e.exec("UPDATE user_subscription SET last_pull_at = ?, last_pull_client = 'sing-box', last_pull_nodes = 4 WHERE id = ?", now-3*86400_000, s1["id"])
	secrets := []string{s1["token"].(string), s2["token"].(string), s3["token"].(string), "SECRET123", "sub.example.net", "urlHint", "airport-pw",
		"ext-secret-pw", "home.example.net", "hk1.provider.example", "203.0.113.", "198.51.100.", "198.51.124.", "AQEBAQEB", ":30002", "node:", "602|"}
	noSecrets := func(what string, v any) {
		t.Helper()
		raw, _ := json.Marshal(v)
		for _, s := range secrets {
			if strings.Contains(string(raw), s) {
				t.Fatalf("%s leaks %q: %s", what, s, raw)
			}
		}
	}

	// Normal users are refused (list and detail).
	for _, path := range []string{"admin/list", "admin/detail"} {
		if out := subCall(t, e.h, path, map[string]any{"id": s1["id"]}, 2, 1); out.Code != 403 || out.Data != nil {
			t.Fatalf("%s as user: %+v", path, out)
		}
	}

	out := subCall(t, e.h, "admin/list", map[string]any{}, 1, 0)
	noSecrets("list", out)
	list := dataMap(t, out)
	users := list["users"].([]any)
	if list["enabled"] != true || len(users) != 2 {
		t.Fatalf("users %v", users)
	}
	bob, alice := users[0].(map[string]any), users[1].(map[string]any)
	if bob["name"] != "bob" || alice["name"] != "alice" || bob["lastPullAt"].(float64) <= alice["lastPullAt"].(float64) {
		t.Fatalf("order %v / %v", bob["name"], alice["name"])
	}
	subs := alice["subs"].([]any)
	a1, a2 := subs[0].(map[string]any), subs[1].(map[string]any)
	b1 := bob["subs"].([]any)[0].(map[string]any)
	if a1["nodes"] != float64(5) || a1["routing"] != "custom" || a1["routingCount"] != float64(2) || a1["format"] != "clash" || a1["lastPullClient"] != "sing-box" {
		t.Fatalf("alice sub 1 %v", a1)
	}
	if a2["routing"] != "none" || a2["nodes"] != float64(0) || a2["lastPullAt"] != float64(0) {
		t.Fatalf("alice sub 2 %v", a2)
	}
	if b1["routing"] != "default" || b1["routingCount"] != float64(8) || b1["nodes"] != float64(1) {
		t.Fatalf("bob sub %v", b1)
	}
	srcs := alice["sources"].([]any)
	if len(srcs) != 1 || len(bob["sources"].([]any)) != 0 {
		t.Fatalf("sources %v", srcs)
	}
	if sv := srcs[0].(map[string]any); sv["name"] != "机场A" || sv["nodes"] != float64(2) || sv["error"] != "拉取订阅失败 … refused" {
		t.Fatalf("source view %v", sv)
	}

	// Detail: the owner's refs in order, the 直连 node's name as the exit.
	e.exec("UPDATE node SET server_ip = '198.51.100.99' WHERE id = 602") // address change still resolves
	out = subCall(t, e.h, "admin/detail", map[string]any{"id": s1["id"]}, 1, 0)
	noSecrets("detail", out)
	d := dataMap(t, out)
	if d["user"] != "alice" || d["name"] != "日常" || d["routingKind"] != "custom" || d["lastPullNodes"] != float64(4) {
		t.Fatalf("detail head %v", d)
	}
	items := d["items"].([]any)
	groups := []string{}
	for _, it := range items {
		m := it.(map[string]any)
		groups = append(groups, m["group"].(string))
		if m["missing"] == true {
			t.Fatalf("missing item %v", m)
		}
	}
	if strings.Join(groups, ",") != "rule,landing,external,source_all" {
		t.Fatalf("groups %v", groups)
	}
	if all := items[3].(map[string]any); all["sourceName"] != "机场A" || all["count"] != float64(2) {
		t.Fatalf("source_all %v", all)
	}
	if ext := items[2].(map[string]any); ext["name"] != "我的 VPS" || ext["protocol"] == "" {
		t.Fatalf("external %v", ext)
	}
	routing := d["routing"].([]any)
	custom := routing[0].(map[string]any)
	if len(routing) != 2 || custom["exit"] != "node" || custom["exitName"] != "游戏加速（直连）" || len(custom["lines"].([]any)) != 2 {
		t.Fatalf("routing %v", routing)
	}
	if src.hits.Load() != 0 {
		t.Fatalf("admin view fetched the source %d times", src.hits.Load())
	}
	// Deleted refs show as 失效; NULL routing comes back as null.
	e.exec("DELETE FROM landing_external_node WHERE id = 31")
	d = dataMap(t, subCall(t, e.h, "admin/detail", map[string]any{"id": s1["id"]}, 1, 0))
	if m := d["items"].([]any)[2].(map[string]any); m["missing"] != true || m["group"] != "external" {
		t.Fatalf("deleted external %v", m)
	}
	d = dataMap(t, subCall(t, e.h, "admin/detail", map[string]any{"id": s2["id"]}, 1, 0))
	if d["routing"] != nil || d["routingKind"] != "none" || len(d["items"].([]any)) != 0 {
		t.Fatalf("NULL routing %v", d)
	}
	// bob's own permissions: alice's landing never resolves into his sub.
	d = dataMap(t, subCall(t, e.h, "admin/detail", map[string]any{"id": s3["id"]}, 1, 0))
	if m := d["items"].([]any)[0].(map[string]any); m["missing"] == true || m["name"] != "B（直连）" {
		t.Fatalf("bob item %v", m)
	}
	if out := subCall(t, e.h, "admin/detail", map[string]any{"id": 9999}, 1, 0); out.Code == 0 {
		t.Fatal("unknown id")
	}

	// Switched off: the list reports it, the detail refuses.
	e.exec("INSERT INTO vite_config(name, value, time) VALUES(?, ?, ?)", subConfigKey, "false", now)
	if l := dataMap(t, subCall(t, e.h, "admin/list", nil, 1, 0)); l["enabled"] != false || len(l["users"].([]any)) != 0 {
		t.Fatalf("disabled list %v", l)
	}
	if out := subCall(t, e.h, "admin/detail", map[string]any{"id": s1["id"]}, 1, 0); out.Code == 0 {
		t.Fatal("detail while switched off")
	}
	if out := subCall(t, e.h, "admin/list", nil, 3, 1); out.Code != 403 {
		t.Fatalf("user while switched off %+v", out)
	}
}

func TestSubRoutingKind(t *testing.T) {
	def := sublink.DefaultRouting()
	changed := sublink.DefaultRouting()
	changed[0].Exit = sublink.ExitDirect
	cases := []struct {
		list []sublink.RouteEntry
		want string
	}{
		{nil, "none"},
		{[]sublink.RouteEntry{}, "none"},
		{def, "default"},
		{append(append([]sublink.RouteEntry{}, def[1:]...), def[0]), "default"}, // order does not matter
		{changed, "custom"},
		{def[:7], "custom"},
		{append(append([]sublink.RouteEntry{}, def[:7]...), def[0]), "custom"}, // duplicate key
		{[]sublink.RouteEntry{{Type: sublink.RouteCustom, Name: "x", Exit: "select"}}, "custom"},
	}
	for i, c := range cases {
		if got := subRoutingKind(c.list); got != c.want {
			t.Fatalf("case %d: %s, want %s", i, got, c.want)
		}
	}
}
