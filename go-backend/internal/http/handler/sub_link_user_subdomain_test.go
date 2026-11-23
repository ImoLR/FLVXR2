package handler

import (
	"database/sql"
	"strings"
	"testing"

	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 303: a live 专属域名 replaces the entry's main domain in the owner's
// subscription. On an IPv6-only main domain the name is IPv6-only too, so
// plan 302's split stays: mihomo / Stash / sing-box get the name, NekoBox /
// Hiddify / Base64 / Surge / unknown keep the literal (byte-identical to
// before). Item keys keep the main domain; a paused name falls back.
func TestSubUserSubdomain(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	const v6, domain, name = "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", "vmsilo-huri.example.top", "k7x2abc.example.top"
	for _, row := range []any{
		&model.Node{ID: 651, Name: "vmsilo 沪日首发", Secret: "s651", ServerIP: domain, ServerIPV6: sql.NullString{String: domain, Valid: true},
			Port: "20000-65000", DDNSEnabled: true, DDNSProviderID: 1, UserSubdomainEnabled: true},
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
	pull := func(ua string) string {
		t.Helper()
		w := subPull(e.h, token, ua, "203.0.113.70")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", ua, w.Code, w.Body.String())
		}
		if strings.HasPrefix(ua, "Shadowrocket") {
			return decodeB64(t, w.Body.String())
		}
		return w.Body.String()
	}
	uas := []string{"clash.meta", "NekoBox/Android/1.4.2", "sing-box 1.14.4", "Hiddify/2.5", "Shadowrocket/2070", "Surge iOS/3100", "Stash/2.7", "okhttp/4.12"}
	before := map[string]string{}
	for _, ua := range uas {
		before[ua] = pull(ua)
	}

	row := model.UserSubdomain{UserID: 2, NodeID: 651, OriginNodeID: 651, Label: "k7x2abc", Domain: name, ProviderID: 1, ZoneID: "z1",
		RecordID: "u1", Target: domain, State: model.UserSubdomainActive, Status: model.UserSubdomainSyncSynced}
	if err := e.r.DB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	e.h.subState().cache.purgeSub(id)
	nameUAs := map[string]bool{"clash.meta": true, "sing-box 1.14.4": true, "Stash/2.7": true}
	for _, ua := range uas {
		body := pull(ua)
		if strings.Contains(body, domain) {
			t.Errorf("%s still has the main domain", ua)
		}
		if nameUAs[ua] != strings.Contains(body, name) {
			t.Errorf("%s: name %v\n%s", ua, strings.Contains(body, name), body)
		}
		if !nameUAs[ua] && body != before[ua] {
			t.Errorf("%s: literal body changed", ua)
		}
		if !strings.Contains(body, "198.51.100.10") {
			t.Errorf("%s lost the other entry", ua)
		}
	}
	// The stored key (main domain) still resolves.
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	sel := pool["selected"].([]any)[0].(map[string]any)
	if want := (sublink.Ref{Kind: sublink.RefForwardLink, ForwardID: 951, Entry: sublink.EntryKey(651, domain)}).Key(); sel["missing"] == true || sel["key"] != want {
		t.Fatalf("selected %v, want key %s", sel, want)
	}

	// Dual-stack main domain → the name for every client.
	e.r.DB().Create(&model.NodeDDNSRecord{NodeID: 651, Domain: domain, RecordType: "A", Role: model.DDNSRoleMain, Source: "panel", CurrentIP: "156.245.192.253", Status: "synced", RecordID: "main2"})
	e.h.subState().cache.purgeSub(id)
	if body := pull("NekoBox/Android/1.4.2"); !strings.Contains(body, "server: "+name) {
		t.Fatalf("dual-stack name not served to NekoBox:\n%s", body)
	}

	// Paused (e.g. 停用 then the admin re-enables the user but the record is
	// not back yet) → the main domain again, never a name without a record.
	e.r.DB().Model(&model.UserSubdomain{}).Where("id = ?", row.ID).Updates(map[string]any{"state": "paused", "record_id": ""})
	e.h.subState().cache.purgeSub(id)
	if body := pull("clash.meta"); strings.Contains(body, name) || !strings.Contains(body, domain) {
		t.Fatalf("paused name still served:\n%s", body)
	}
}
