package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 193: 100 nodes per subscription and per source, burst guard.

func hostLines(n int) string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("socks5://u:p@n%d.provider.example:1080#N%d", i, i))
	}
	return strings.Join(lines, "\n")
}

func pulledLines(t *testing.T, e *flowTestEnv, token string) []string {
	t.Helper()
	w := subPull(e.h, token, "v2rayN/6.45", "203.0.113.40")
	if w.Code != 200 {
		t.Fatalf("pull %d %s", w.Code, w.Body.String())
	}
	return strings.Split(strings.TrimSpace(decodeB64(t, w.Body.String())), "\n")
}

func TestSubNodeCap100(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	newSubSource(t, hostLines(150))
	pre := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1)
	d := dataMap(t, pre)
	if pre.Code != 0 || d["count"].(float64) != 100 || d["truncated"] != true || d["total"].(float64) != 150 || d["maxNodes"].(float64) != 100 || len(d["items"].([]any)) != 100 {
		t.Fatalf("preview: code %d count %v truncated %v total %v items %d", pre.Code, d["count"], d["truncated"], d["total"], len(d["items"].([]any)))
	}
	out := subCall(t, e.h, "source/create", map[string]any{"name": "机场", "fetchId": d["fetchId"]}, 2, 1)
	src := int64(dataMap(t, out)["id"].(float64))
	var row model.UserSubSource
	e.r.DB().First(&row, src)
	if row.NodeCount != 100 || len(sublink.ParseSourceNodes(row.CachedNodes)) != 100 {
		t.Fatalf("stored %d nodes", row.NodeCount)
	}
	follow := map[string]any{"kind": "source_all", "sourceId": src}
	landing := map[string]any{"kind": "landing_direct", "landingId": 22}
	sub := subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{follow}}, 2, 1)
	if sub.Code != 0 {
		t.Fatalf("100 nodes refused: %+v", sub)
	}
	id := dataMap(t, sub)["id"]
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "items": []any{follow, landing}}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "最多 100 个节点，现在是 101 个") {
		t.Fatalf("101st node: %+v", out)
	}
	if out := subCall(t, e.h, "create", map[string]any{"name": "t", "items": []any{follow, landing}}, 2, 1); out.Code == 0 {
		t.Fatal("create with 101 nodes accepted")
	}
	many := []any{}
	for i := 0; i < 101; i++ {
		many = append(many, map[string]any{"kind": "source_node", "sourceId": src, "name": fmt.Sprintf("N%d", i)})
	}
	if out := subCall(t, e.h, "update", map[string]any{"id": id, "items": many}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "最多 100 个节点") {
		t.Fatalf("101 refs: %+v", out)
	}
	if got := pulledLines(t, e, dataMap(t, sub)["token"].(string)); len(got) != 100 {
		t.Fatalf("rendered %d nodes", len(got))
	}
}

// TestSubOverLimitExistingData: rows from before plan 193 are served capped
// and shown over the limit; nothing is changed or deleted.
func TestSubOverLimitExistingData(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	nodes := []sublink.SourceNode{}
	for i := 0; i < 150; i++ {
		nodes = append(nodes, sublink.SourceNode{Name: fmt.Sprintf("N%d", i), Up: linechain.Upstream{Type: "socks", IPVersion: "system", Server: fmt.Sprintf("n%d.provider.example", i), Port: 1080}})
	}
	cached, _ := json.Marshal(nodes)
	now := time.Now().UnixMilli()
	srcRow := model.UserSubSource{ID: 77, UserID: 2, Name: "旧机场", LastOkAt: now, LastFetchAt: now, CachedNodes: string(cached), NodeCount: 150}
	e.r.DB().Create(&srcRow)
	items := sublink.RefsJSON([]sublink.Ref{{Kind: sublink.RefSourceAll, SourceID: 77}, {Kind: sublink.RefLandingDirect, LandingID: 22}})
	token, _ := sublink.NewToken()
	subRow := model.UserSubscription{ID: 55, UserID: 2, Name: "旧订阅", Token: token, Format: "auto", RuleMode: "cn_direct", Items: items}
	e.r.DB().Create(&subRow)

	if got := pulledLines(t, e, token); len(got) != 100 {
		t.Fatalf("rendered %d nodes", len(got))
	}
	list := dataMap(t, subCall(t, e.h, "list", nil, 2, 1))
	card := list["subs"].([]any)[0].(map[string]any)["counts"].(map[string]any)
	srcView := list["sources"].([]any)[0].(map[string]any)
	if card["total"].(float64) != 101 || srcView["nodeCount"].(float64) != 100 || list["maxNodes"].(float64) != 100 {
		t.Fatalf("list: total %v source %v", card["total"], srcView["nodeCount"])
	}
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": 55}, 2, 1))
	if sel := pool["selected"].([]any); sel[0].(map[string]any)["count"].(float64) != 100 || pool["maxNodes"].(float64) != 100 {
		t.Fatalf("pool selected %v", sel)
	}
	var after model.UserSubscription
	e.r.DB().First(&after, 55)
	var srcAfter model.UserSubSource
	e.r.DB().First(&srcAfter, 77)
	if after.Items != items || srcAfter.CachedNodes != string(cached) || srcAfter.NodeCount != 150 {
		t.Fatal("existing rows were changed")
	}
}

// TestSubColdRenderBusy: with every slot busy a cold render waits briefly
// and gets 503; cached renders are still served; waiters are bounded.
func TestSubColdRenderBusy(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{map[string]any{"kind": "landing_direct", "landingId": 22}}}, 2, 1))
	token := sub["token"].(string)
	if w := subPull(e.h, token, "clash.meta", "203.0.113.50"); w.Code != 200 {
		t.Fatalf("warm %d", w.Code)
	}
	old := subRenderWait
	subRenderWait = 80 * time.Millisecond
	defer func() { subRenderWait = old }()
	st := e.h.subState()
	for i := 0; i < subRenderSlots; i++ {
		st.slots <- struct{}{}
	}
	if w := subPull(e.h, token, "clash.meta", "203.0.113.51"); w.Code != 200 {
		t.Fatalf("cached pull %d", w.Code)
	}
	start := time.Now()
	w := subPull(e.h, token, "sing-box 1.11", "203.0.113.52")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" || time.Since(start) > 2*time.Second {
		t.Fatalf("busy cold render: %d %q after %v", w.Code, w.Header().Get("Retry-After"), time.Since(start))
	}
	st.waiting.Store(subRenderWaiters)
	start = time.Now()
	if w := subPull(e.h, token, "sing-box 1.11", "203.0.113.53"); w.Code != 503 || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("waiters full: %d after %v", w.Code, time.Since(start))
	}
	st.waiting.Store(0)
	for i := 0; i < subRenderSlots; i++ {
		<-st.slots
	}
	if w := subPull(e.h, token, "sing-box 1.11", "203.0.113.54"); w.Code != 200 {
		t.Fatalf("after release %d", w.Code)
	}
	if len(st.slots) != 0 || st.waiting.Load() != 0 {
		t.Fatalf("slots %d waiting %d", len(st.slots), st.waiting.Load())
	}
}
