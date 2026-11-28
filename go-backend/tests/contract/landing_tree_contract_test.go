package contract_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

// Plan 327: POST /api/v1/landing/tree — the 我的节点 tree's external nodes
// (no credentials) and, per plan-108 线路机 chain forward, the external node
// it was made from. Users see only their own; admins their own, or every
// user's (with owner names) with {"all": true}.
func TestLandingTreeContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runLandingTree(t, dialect) })
	}
}

type treeView struct {
	Externals []struct {
		ID            int64  `json:"id"`
		UserID        int64  `json:"userId"`
		UserName      string `json:"userName"`
		Name          string `json:"name"`
		Protocol      string `json:"protocol"`
		ProtocolLabel string `json:"protocolLabel"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
	} `json:"externals"`
	Chains []struct {
		ForwardID  int64 `json:"forwardId"`
		UserID     int64 `json:"userId"`
		ExternalID int64 `json:"externalId"`
	} `json:"chains"`
	All bool `json:"all"`
}

func runLandingTree(t *testing.T, dialect string) {
	const secret = "landing-tree-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	const uid, other = 120, 121
	for _, u := range []model.User{
		{ID: uid, User: "tree_user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
		{ID: other, User: "tree_other", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*365, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1},
	} {
		u := u
		if err := r.DB().Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	parse := func(link string) linechain.Upstream {
		t.Helper()
		up, _, err := linechain.ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		return up
	}
	ext := func(owner int64, name, link string) int64 {
		t.Helper()
		up := parse(link)
		n := model.LandingExternalNode{UserID: owner, Name: name, Protocol: up.Type, Host: up.Server, Port: up.Port, Summary: up.Summary(), Upstream: up.JSON(), UpstreamHash: up.Hash(), CreatedTime: now, UpdatedTime: now}
		if err := r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	// A chain stores its own copy of the node (IP version set by the chain).
	chain := func(owner, forwardID int64, link string) {
		t.Helper()
		up := parse(link)
		up.IPVersion = "system"
		c := model.ForwardChain{UserID: owner, ForwardID: forwardID, TunnelID: 1, State: model.ChainStateActive, Upstream: up.JSON(), UpstreamProtocol: up.ProtocolLabel(), UpstreamHost: up.Server, UpstreamPort: up.Port, UpstreamHash: up.Hash(), CreatedTime: now, UpdatedTime: now}
		if err := r.DB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	jp := ext(uid, "日本家宽", "trojan://pass@jp.example.com:443?alpn=h2#jp")
	sockA := ext(uid, "同址 A", "socks5://a:a@198.51.100.50:1080#a")
	sockB := ext(uid, "同址 B", "socks5://b:b@198.51.100.50:1080#b")
	otherExt := ext(other, "别人的", "trojan://x@198.51.100.40:443?sni=a.example.com#o")

	chain(uid, 901, "trojan://pass@jp.example.com:443?alpn=h2#jp")      // exact copy → jp
	chain(uid, 902, "trojan://newpass@jp.example.com:443?alpn=h2#jp")   // same protocol + address, only one node there → jp
	chain(uid, 903, "socks5://b:b@198.51.100.50:1080#b")                // exact → B (A shares the address)
	chain(uid, 904, "socks5://c:c@198.51.100.50:1080#c")                // two nodes on the address, no exact → none
	chain(uid, 905, "trojan://y@198.51.100.99:443?sni=z.example.com#p") // pasted, not in the list → none
	chain(uid, 906, "trojan://x@198.51.100.40:443?sni=a.example.com#o") // = the OTHER user's node → never matched across owners
	chain(other, 907, "trojan://x@198.51.100.40:443?sni=a.example.com#o")

	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	admin, _ := auth.GenerateToken(1, "admin", 0, secret)
	user, _ := auth.GenerateToken(uid, "tree_user", 1, secret)
	otherTok, _ := auth.GenerateToken(other, "tree_other", 1, secret)
	tree := func(tok string, body interface{}) treeView {
		t.Helper()
		out := requestContractEnvelope(t, router, tok, "/api/v1/landing/tree", body)
		if out.Code != 0 {
			t.Fatalf("tree: %+v", out)
		}
		raw, _ := json.Marshal(out.Data)
		var v treeView
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	chainMap := func(v treeView) map[int64]int64 {
		m := map[int64]int64{}
		for _, c := range v.Chains {
			m[c.ForwardID] = c.ExternalID
		}
		return m
	}

	// 1. The user: own external nodes and chains only, even when asking for all.
	for _, body := range []interface{}{map[string]interface{}{}, map[string]interface{}{"all": true}} {
		v := tree(user, body)
		if v.All || len(v.Externals) != 3 {
			t.Fatalf("user view %+v", v)
		}
		for _, e := range v.Externals {
			if e.UserID != uid || e.UserName != "" || e.ID == otherExt {
				t.Fatalf("user sees %+v", e)
			}
		}
		got := chainMap(v)
		want := map[int64]int64{901: jp, 902: jp, 903: sockB, 904: 0, 905: 0, 906: 0}
		if len(got) != len(want) {
			t.Fatalf("user chains %v", got)
		}
		for f, e := range want {
			if got[f] != e {
				t.Fatalf("chain %d → %d, want %d (all %v)", f, got[f], e, got)
			}
		}
		_ = sockA
	}
	// The response never carries credentials.
	raw := requestContractEnvelope(t, router, user, "/api/v1/landing/tree", nil)
	b, _ := json.Marshal(raw.Data)
	for _, secretPart := range []string{"pass", "upstream", "uuid", "password"} {
		if strings.Contains(strings.ToLower(string(b)), secretPart) {
			t.Fatalf("tree leaks %q: %s", secretPart, b)
		}
	}

	// 2. The other user: only theirs.
	if v := tree(otherTok, nil); len(v.Externals) != 1 || v.Externals[0].ID != otherExt || len(v.Chains) != 1 || v.Chains[0].ExternalID != otherExt {
		t.Fatalf("other view %+v", v)
	}

	// 3. Admin: own (none) by default; every user's with all, owner names set.
	if v := tree(admin, nil); v.All || len(v.Externals) != 0 || len(v.Chains) != 0 {
		t.Fatalf("admin own view %+v", v)
	}
	v := tree(admin, map[string]interface{}{"all": true})
	if !v.All || len(v.Externals) != 4 || len(v.Chains) != 7 {
		t.Fatalf("admin all view %+v", v)
	}
	for _, e := range v.Externals {
		want := map[int64]string{uid: "tree_user", other: "tree_other"}[e.UserID]
		if e.UserName != want {
			t.Fatalf("owner name %+v", e)
		}
	}
	if got := chainMap(v); got[906] != 0 || got[907] != otherExt || got[901] != jp {
		t.Fatalf("admin chains %v", got)
	}

	// 4. No token → 401.
	if out := requestContractEnvelope(t, router, "", "/api/v1/landing/tree", nil); out.Code != 401 {
		t.Fatalf("anonymous: %+v", out)
	}
}
