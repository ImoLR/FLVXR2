package handler

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 315 「节点名带入口名」: NULL / true = today's names, false = fixed names.

var updateNamesGolden = flag.Bool("update-names-golden", false, "rewrite testdata/sub-names")

var clashProxyName = regexp.MustCompile(`(?m)^  - name: (.+)$`)

// subClashNames: the proxy names of one Clash pull (in order).
func subClashNames(t *testing.T, e *flowTestEnv, token string) []string {
	t.Helper()
	w := subPull(e.h, token, "clash.meta", "203.0.113.40")
	if w.Code != 200 {
		t.Fatalf("pull %d %s", w.Code, w.Body.String())
	}
	out := []string{}
	body := w.Body.String()
	proxies, _, _ := strings.Cut(body[strings.Index(body, "proxies:"):], "proxy-groups:")
	for _, m := range clashProxyName.FindAllStringSubmatch(proxies, -1) {
		out = append(out, strings.Trim(m[1], `"'`))
	}
	return out
}

// subClashBody: one Clash pull with the token-dependent parts blanked (golden).
func subClashBody(t *testing.T, e *flowTestEnv, token string) string {
	t.Helper()
	w := subPull(e.h, token, "clash.meta", "203.0.113.41")
	if w.Code != 200 {
		t.Fatalf("pull %d", w.Code)
	}
	return strings.ReplaceAll(w.Body.String(), token, "TOKEN")
}

func checkNamesGolden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", "sub-names", name)
	if *updateNamesGolden {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update-names-golden)", name, err)
	}
	if string(want) != got {
		t.Fatalf("golden %s differs:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// seedSecondEntry: rule 901 (游戏加速, landing 22) also listens on a second
// entry node 603 「广州电信入口」.
func seedSecondEntry(t *testing.T, e *flowTestEnv) {
	t.Helper()
	for _, row := range []any{
		&model.Node{ID: 603, Name: "广州电信入口", Secret: "s603", ServerIP: "198.51.100.30", Port: "20000-29999"},
		&model.ChainTunnel{TunnelID: 811, NodeID: 603, ChainType: "1"},
		&model.ForwardPort{ForwardID: 901, NodeID: 603, Port: 20019},
	} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
}

func subCreateItems(t *testing.T, e *flowTestEnv, entryNames *bool, items ...any) (int64, string) {
	t.Helper()
	body := map[string]any{"name": "s", "items": items}
	if entryNames != nil {
		body["entryNames"] = *entryNames
	}
	out := subCall(t, e.h, "create", body, 2, 1)
	if out.Code != 0 {
		t.Fatalf("create: %+v", out)
	}
	sub := dataMap(t, out)
	if want := entryNames == nil || *entryNames; sub["entryNames"] != want {
		t.Fatalf("create view entryNames %v, want %v", sub["entryNames"], want)
	}
	return int64(sub["id"].(float64)), sub["token"].(string)
}

var (
	fwd602   = map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"}
	fwd603   = map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "603|198.51.100.30"}
	direct22 = map[string]any{"kind": "landing_direct", "landingId": 22}
	namesOff = func() *bool { v := false; return &v }()
	namesOn  = func() *bool { v := true; return &v }()
)

func eqNames(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s: names %q, want %q", what, got, want)
	}
}

// Old rows (NULL) and an API create without the field keep today's names.
func TestSubNamesDefaultUnchanged(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	_, token := subCreateItems(t, e, nil, fwd602, direct22)
	eqNames(t, "default", subClashNames(t, e, token), "游戏加速 · 上海联通入口", "游戏加速（直连）")
	var row model.UserSubscription
	e.r.DB().Where("token = ?", token).First(&row)
	if row.EntryNames != nil {
		t.Fatalf("entry_names written: %v", *row.EntryNames)
	}
	// 「节点名带入口名」 on explicitly: the same output, byte for byte.
	_, tokenOn := subCreateItems(t, e, namesOn, fwd602, direct22)
	if a, b := subClashBody(t, e, token), subClashBody(t, e, tokenOn); a != b {
		t.Fatalf("explicit on differs from NULL:\n%s\n---\n%s", a, b)
	}
}

func TestSubNamesFixedSingleEntry(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	_, token := subCreateItems(t, e, namesOff, fwd602, direct22)
	eqNames(t, "fixed", subClashNames(t, e, token), "游戏加速", "游戏加速（直连）")
	checkNamesGolden(t, "fixed-single-clash.yaml", subClashBody(t, e, token))
}

// The forward moves to another entry: the fixed-name node follows it and keeps
// its name; today's names lose the node (as before plan 315).
func TestSubNamesFixedEntrySwitched(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	id, token := subCreateItems(t, e, namesOff, fwd602)
	_, oldToken := subCreateItems(t, e, nil, fwd602)
	before := subClashBody(t, e, token)
	seedSecondEntry(t, e)
	e.exec("DELETE FROM forward_port WHERE forward_id = 901 AND node_id = 602")
	e.exec("DELETE FROM chain_tunnel WHERE tunnel_id = 811 AND node_id = 602")
	e.h.subState().cache.purgeUser(2)
	after := subClashBody(t, e, token)
	eqNames(t, "switched", subClashNames(t, e, token), "游戏加速")
	if !strings.Contains(after, "198.51.100.30") || strings.Contains(after, "198.51.100.10") {
		t.Fatalf("not on the new entry:\n%s", after)
	}
	if strings.ReplaceAll(strings.ReplaceAll(after, "198.51.100.30", "X"), "20019", "P") !=
		strings.ReplaceAll(strings.ReplaceAll(before, "198.51.100.10", "X"), "20009", "P") {
		t.Fatalf("only the address may change:\n%s\n---\n%s", before, after)
	}
	checkNamesGolden(t, "fixed-switched-clash.yaml", after)
	eqNames(t, "switched, today's names", subClashNames(t, e, oldToken))
	// The editor shows the followed item (not 失效) under its current key; a
	// save keeps it there.
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	sel := pool["selected"].([]any)
	if len(sel) != 1 || sel[0].(map[string]any)["missing"] == true || sel[0].(map[string]any)["name"] != "游戏加速" ||
		sel[0].(map[string]any)["key"] != "f:901:603|198.51.100.30" {
		t.Fatalf("selected %v", sel)
	}
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": []any{fwd602}}, 2, 1); out.Code != 0 {
		t.Fatalf("update %+v", out)
	}
	var row model.UserSubscription
	e.r.DB().First(&row, id)
	if !strings.Contains(row.Items, "603|198.51.100.30") || !row.FixedNames() {
		t.Fatalf("saved %s fixed=%v", row.Items, row.FixedNames())
	}
}

// The same forward through two entries keeps 「 · 入口名」 (unique names); if one
// entry goes away the other is not doubled.
func TestSubNamesFixedTwoEntries(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	seedSecondEntry(t, e)
	_, token := subCreateItems(t, e, namesOff, fwd602, fwd603, direct22)
	eqNames(t, "two entries", subClashNames(t, e, token), "游戏加速 · 上海联通入口", "游戏加速 · 广州电信入口", "游戏加速（直连）")
	checkNamesGolden(t, "fixed-two-entries-clash.yaml", subClashBody(t, e, token))
	// Only one of the two entries picked: the plain forward name.
	_, one := subCreateItems(t, e, namesOff, fwd603)
	eqNames(t, "one of two", subClashNames(t, e, one), "游戏加速")
	e.exec("DELETE FROM forward_port WHERE forward_id = 901 AND node_id = 602")
	e.h.subState().cache.purgeUser(2)
	eqNames(t, "one entry left", subClashNames(t, e, token), "游戏加速", "游戏加速（直连）")
}

// 「节点名带入口名」 on / off on an existing subscription (list + render follow).
func TestSubNamesToggle(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	id, token := subCreateItems(t, e, nil, fwd602)
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "name": "s", "items": []any{fwd602}, "entryNames": false}, 2, 1); out.Code != 0 ||
		dataMap(t, out)["entryNames"] != false {
		t.Fatalf("off: %+v", out)
	}
	eqNames(t, "off", subClashNames(t, e, token), "游戏加速")
	list := dataMap(t, subCall(t, e.h, "list", nil, 2, 1))
	if v := list["subs"].([]any)[0].(map[string]any)["entryNames"]; v != false {
		t.Fatalf("list entryNames %v", v)
	}
	// An update without the field keeps the setting.
	subCall(t, e.h, "update", map[string]any{"id": id, "name": "s2", "items": []any{fwd602}}, 2, 1)
	eqNames(t, "kept", subClashNames(t, e, token), "游戏加速")
	subCall(t, e.h, "update", map[string]any{"id": id, "name": "s2", "items": []any{fwd602}, "entryNames": true}, 2, 1)
	eqNames(t, "on", subClashNames(t, e, token), "游戏加速 · 上海联通入口")
}
