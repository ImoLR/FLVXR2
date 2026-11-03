package handler

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// TestSubSourceOverCapExisting (plan 200): an account that saved 3 sources
// under the old cap keeps all 3 — they list, refresh and render — but it
// cannot add another until it is below 2.
func TestSubSourceOverCapExisting(t *testing.T) {
	e := subEnv(t)
	src := newSubSource(t, socksLines("新1", "新2"))
	enc, err := e.h.subCrypto().Encrypt([]byte(subURL))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	refs := []sublink.Ref{}
	for i := 1; i <= 3; i++ {
		nodes := []sublink.SourceNode{}
		for j := 0; j < 2; j++ {
			nodes = append(nodes, sublink.SourceNode{Name: fmt.Sprintf("S%d-%d", i, j), Up: linechain.Upstream{Type: "socks", IPVersion: "system", Server: fmt.Sprintf("s%d-%d.provider.example", i, j), Port: 1080}})
		}
		cached, _ := json.Marshal(nodes)
		row := model.UserSubSource{ID: int64(80 + i), UserID: 2, Name: fmt.Sprintf("旧源%d", i), URLEnc: enc, URLHint: "sub.example.net/…",
			LastOkAt: now, LastFetchAt: now, CachedNodes: string(cached), NodeCount: 2, CreatedTime: now, UpdatedTime: now}
		if err := e.r.DB().Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		refs = append(refs, sublink.Ref{Kind: sublink.RefSourceAll, SourceID: row.ID})
	}
	token, _ := sublink.NewToken()
	if err := e.r.DB().Create(&model.UserSubscription{ID: 66, UserID: 2, Name: "旧订阅", Token: token, Format: "auto", RuleMode: "cn_direct", Items: sublink.RefsJSON(refs)}).Error; err != nil {
		t.Fatal(err)
	}

	// All 3 still render and list.
	if got := pulledLines(t, e, token); len(got) != 6 {
		t.Fatalf("rendered %d nodes: %v", len(got), got)
	}
	list := dataMap(t, subCall(t, e.h, "list", nil, 2, 1))
	if list["sourceCap"].(float64) != 2 || len(list["sources"].([]any)) != 3 {
		t.Fatalf("list cap %v sources %d", list["sourceCap"], len(list["sources"].([]any)))
	}
	// Adding is refused before any outbound fetch; refreshing an old one works.
	capMsg := "外部订阅最多 2 个，如需更多请联系管理员"
	if out := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1); out.Code == 0 || out.Msg != capMsg {
		t.Fatalf("fetch over cap: %+v", out)
	}
	if src.hits.Load() != 0 {
		t.Fatalf("fetched at cap: %d", src.hits.Load())
	}
	if out := subCall(t, e.h, "source/refresh", map[string]any{"id": 81}, 2, 1); out.Code != 0 || dataMap(t, out)["nodeCount"].(float64) != 2 {
		t.Fatalf("refresh old source: %+v", out)
	}
	if src.hits.Load() != 1 {
		t.Fatalf("refresh hits %d", src.hits.Load())
	}
	// Down to 2: still at the cap. Down to 1: can add again.
	if out := subCall(t, e.h, "source/delete", map[string]any{"id": 83}, 2, 1); out.Code != 0 {
		t.Fatalf("delete: %+v", out)
	}
	if out := subCall(t, e.h, "source/fetch", map[string]any{"url": subURL}, 2, 1); out.Code == 0 || out.Msg != capMsg {
		t.Fatalf("fetch at cap: %+v", out)
	}
	if out := subCall(t, e.h, "source/delete", map[string]any{"id": 82}, 2, 1); out.Code != 0 {
		t.Fatalf("delete: %+v", out)
	}
	addSource(t, e, 2, "新源")
	var n int64
	e.r.DB().Model(&model.UserSubSource{}).Where("user_id = ?", 2).Count(&n)
	if n != 2 || src.hits.Load() != 2 {
		t.Fatalf("sources %d hits %d", n, src.hits.Load())
	}
}
