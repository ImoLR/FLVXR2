package handler

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/ratelimit"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 303 round 2 (user 11-24): on a node with 用户专属域名 every client
// format gets the user's own name — never the node IP, never the node's
// main domain (plan 302's NekoBox / Surge → IPv6 literal split does not
// apply there). A name stays in the subscription while 到期 / 停用 /
// admin-deleted (record gone on purpose). Documents that turn IPv6 off (Clash
// dns.ipv6, Surge ipv6 = false) turn it on only with an AAAA-only server;
// other subscriptions are byte-identical. Item keys keep the main domain.
// Round 3 (user 11-24 evening): the main domain is never a fallback. A fresh
// name being created (正在就绪) is already in the subscription; no name yet,
// or a fresh name that cannot get a record (cap, creating keeps failing) →
// the entry is left out for every client (13 formats).
func TestSubUserSubdomain(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	e.h.subState().tokenRL = &ratelimit.Bucket{Burst: 100000, Every: time.Millisecond, MaxKeys: 100} // many pulls below
	const v6, domain, name = "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", "vmsilo-huri.example.top", "k7x2abc.example.top"
	for _, row := range []any{
		&model.Node{ID: 651, Name: "vmsilo 沪日首发", Secret: "s651", ServerIP: domain, ServerIPV6: sql.NullString{String: domain, Valid: true},
			Port: "20000-65000", DDNSEnabled: true, DDNSProviderID: 1},
		&model.Tunnel{ID: 851, Name: "沪日首发 -> JP", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1, InIP: sql.NullString{String: domain, Valid: true}},
		&model.ChainTunnel{TunnelID: 851, NodeID: 651, ChainType: "1"},
		&model.Forward{ID: 951, UserID: 2, UserName: "a", Name: "test ss", TunnelID: 851, Status: 1, LandingNodeID: 22, RemoteAddr: "203.0.113.21:30002"},
		&model.ForwardPort{ForwardID: 951, NodeID: 651, Port: 26196},
		&model.NodeDDNSRecord{NodeID: 651, Domain: domain, RecordType: "AAAA", Role: model.DDNSRoleMain, Source: "ct", CurrentIP: v6, TTL: 120,
			Status: "synced", ZoneID: "z1", RecordID: "main1", Applied: true},
	} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 951, "entry": "651|" + domain},
		map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"},
	}}, 2, 1))
	id, token := int64(sub["id"].(float64)), sub["token"].(string)
	// A subscription without any AAAA-only entry: byte-identical throughout.
	plain := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "p", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 901, "entry": "602|198.51.100.10"},
	}}, 2, 1))
	plainID, plainToken := int64(plain["id"].(float64)), plain["token"].(string)

	pullTok := func(tok, ua string) string {
		t.Helper()
		w := subPull(e.h, tok, ua, "203.0.113.70")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", ua, w.Code, w.Body.String())
		}
		if strings.HasPrefix(ua, "Shadowrocket") || strings.HasPrefix(ua, "v2rayN") {
			return decodeB64(t, w.Body.String())
		}
		return w.Body.String()
	}
	pull := func(ua string) string { return pullTok(token, ua) }
	purge := func() {
		e.h.subState().cache.purgeSub(id)
		e.h.subState().cache.purgeSub(plainID)
	}
	// The 13 client formats of the round-2 prod comparison.
	uas := []string{"clash-verge/v2.2.3", "Stash/2.7", "sing-box 1.14.4", "NekoBox/Android/1.4.2", "Hiddify/2.5", "v2rayN/7.10",
		"v2rayNG/1.9", "Shadowrocket/2070", "Surge iOS/3100", "Surfboard/2.24", "Loon/3.2", "Quantumult%20X/1.5", "okhttp/4.12"}
	plainBefore := map[string]string{}
	for _, ua := range uas {
		plainBefore[ua] = pullTok(plainToken, ua)
	}

	// Toggle off: plan 302 as on fork.167 (NekoBox / Surge … get the literal).
	off := map[string]string{}
	for _, ua := range uas {
		off[ua] = pull(ua)
	}
	if !strings.Contains(off["NekoBox/Android/1.4.2"], v6) || !strings.Contains(off["clash-verge/v2.2.3"], "server: "+domain) {
		t.Fatalf("plan 302 baseline changed:\n%s", off["NekoBox/Android/1.4.2"])
	}

	check := func(stage, want string) {
		t.Helper()
		for _, ua := range uas {
			body := pull(ua)
			if !strings.Contains(body, want) {
				t.Errorf("%s/%s: %s missing\n%s", stage, ua, want, body)
			}
			if strings.Contains(body, v6) {
				t.Errorf("%s/%s: the entry IP leaked", stage, ua)
			}
			if want == name && strings.Contains(body, domain) {
				t.Errorf("%s/%s: the main domain leaked", stage, ua)
			}
			if !strings.Contains(body, "198.51.100.10") {
				t.Errorf("%s/%s lost the other entry", stage, ua)
			}
			switch ua {
			case "clash-verge/v2.2.3", "Stash/2.7", "NekoBox/Android/1.4.2", "okhttp/4.12":
				if !strings.Contains(body, "  ipv6: true\n") || strings.Contains(body, "ipv6: false") {
					t.Errorf("%s/%s: Clash dns.ipv6 not on for an AAAA-only server", stage, ua)
				}
			case "Surge iOS/3100":
				if !strings.Contains(body, "ipv6 = true\n") || strings.Contains(body, "ipv6 = false") {
					t.Errorf("%s/%s: Surge ipv6 not on", stage, ua)
				}
			}
		}
		for _, ua := range uas {
			if got := pullTok(plainToken, ua); got != plainBefore[ua] {
				t.Errorf("%s/%s: a subscription without AAAA-only entries changed", stage, ua)
			}
		}
	}

	// The entry is left out for every client: no main domain, no IP, no
	// name; the other entry stays, IPv6 is not turned on for it.
	omitted := func(stage string) {
		t.Helper()
		for _, ua := range uas {
			body := pull(ua)
			for _, leak := range []string{domain, v6, name} {
				if strings.Contains(body, leak) {
					t.Errorf("%s/%s: %s in the subscription\n%s", stage, ua, leak, body)
				}
			}
			if !strings.Contains(body, "198.51.100.10") {
				t.Errorf("%s/%s lost the other entry", stage, ua)
			}
			if strings.Contains(body, "ipv6: true") || strings.Contains(body, "ipv6 = true") {
				t.Errorf("%s/%s: IPv6 turned on without an AAAA-only server", stage, ua)
			}
		}
	}

	// Toggle on, no name row yet (the instant before the reservation):
	// round 3 — left out, never the main domain.
	e.r.DB().Model(&model.Node{}).Where("id = ?", 651).Update("user_subdomain_enabled", true)
	purge()
	omitted("no name")

	// Live name → the name for every client.
	row := model.UserSubdomain{UserID: 2, NodeID: 651, OriginNodeID: 651, Label: "k7x2abc", Domain: name, ProviderID: 1, ZoneID: "z1",
		RecordID: "u1", Target: domain, State: model.UserSubdomainActive, Status: model.UserSubdomainSyncSynced}
	if err := e.r.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	purge()
	check("live", name)
	live := map[string]string{}
	for _, ua := range uas {
		live[ua] = pull(ua)
	}
	// The stored key (main domain) still resolves; round 3: the editor shows
	// it under the name (the main domain never reaches the user).
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	sel := pool["selected"].([]any)[0].(map[string]any)
	if want := (sublink.Ref{Kind: sublink.RefForwardLink, ForwardID: 951, Entry: sublink.EntryKey(651, name)}).Key(); sel["missing"] == true || sel["key"] != want {
		t.Fatalf("selected %v, want key %s", sel, want)
	}
	if rawPool, _ := json.Marshal(subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1)); strings.Contains(string(rawPool), domain) || strings.Contains(string(rawPool), v6) {
		t.Fatalf("sub editor pool shows the main domain / IP: %s", rawPool)
	}

	// 到期 / 停用 (record deleted, name kept), a fresh record being created
	// (正在就绪, also after 1–2 failures), a name ready before that waits for
	// room / keeps failing, an admin delete: the same name stays —
	// byte-identical to the live output.
	for _, st := range []map[string]any{
		{"state": "paused", "reason": "expired", "record_id": "", "target": "", "ready_at": 1},
		{"state": "paused", "reason": "disabled", "record_id": "", "target": ""},
		{"state": "active", "reason": "", "record_id": "", "status": "pending", "ready_at": 0},
		{"state": "active", "status": "failed", "fail_count": 2},
		{"state": "active", "status": "capped", "fail_count": 0, "ready_at": 1},
		{"state": "active", "status": "failed", "fail_count": 9, "ready_at": 1},
		{"state": "retired", "reason": "admin", "blocked": true, "record_id": ""},
	} {
		e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(st)
		purge()
		for _, ua := range uas {
			if body := pull(ua); body != live[ua] {
				t.Errorf("%v/%s: output differs from the live name\n%s", st, ua, body)
			}
		}
	}
	// A fresh name (never ready) that waits for room or keeps failing: left
	// out (round 3) — no address at all.
	for _, st := range []map[string]any{
		{"state": "active", "reason": "", "blocked": false, "status": "capped", "fail_count": 0, "ready_at": 0},
		{"state": "active", "status": "failed", "fail_count": 3, "ready_at": 0},
	} {
		e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(st)
		purge()
		omitted(st["status"].(string))
	}
	// Retired for another reason (renamed / user gone) without a new name:
	// left out until the next pass reserves one (round 3: no main domain).
	e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(map[string]any{"state": "retired", "reason": "renamed", "blocked": false,
		"status": "synced", "ready_at": 1})
	purge()
	omitted("retired")

	// Toggle off again → exactly the plan 302 output.
	e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(map[string]any{"state": "paused", "reason": "node"})
	e.r.DB().Model(&model.Node{}).Where("id = ?", 651).Update("user_subdomain_enabled", false)
	purge()
	for _, ua := range uas {
		if body := pull(ua); body != off[ua] {
			t.Errorf("toggle off/%s: differs from plan 302 output", ua)
		}
	}

	// Dual-stack main domain → the name for every client, IPv6 stays off.
	e.r.DB().Model(&model.Node{}).Where("id = ?", 651).Update("user_subdomain_enabled", true)
	e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(map[string]any{"state": "active", "reason": "", "record_id": "u1", "status": "synced", "fail_count": 0})
	e.r.DB().Create(&model.NodeDDNSRecord{NodeID: 651, Domain: domain, RecordType: "A", Role: model.DDNSRoleMain, Source: "panel", CurrentIP: "156.245.192.253", Status: "synced", RecordID: "main2"})
	purge()
	for _, ua := range []string{"NekoBox/Android/1.4.2", "Surge iOS/3100", "Shadowrocket/2070"} {
		body := pull(ua)
		if !strings.Contains(body, name) || strings.Contains(body, domain) {
			t.Errorf("dual-stack/%s: name not served\n%s", ua, body)
		}
		if strings.Contains(body, "ipv6: true") || strings.Contains(body, "ipv6 = true") {
			t.Errorf("dual-stack/%s: IPv6 turned on without an AAAA-only server", ua)
		}
	}
}
