package handler

import (
	"database/sql"
	"strings"
	"testing"

	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 302: an entry switched to an IPv6-only DDNS domain. Stored items keep
// resolving, mihomo / Stash / sing-box get the domain, NekoBox / Hiddify /
// Base64 / Surge / unknown clients keep the record's current IPv6 (as before).
func TestSubEntryIPv6OnlyDomain(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	const v6, domain = "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", "vmsilo-huri.example.top"
	for _, row := range []any{
		&model.Node{ID: 651, Name: "vmsilo 沪日首发", Secret: "s651", ServerIP: v6, ServerIPV6: sql.NullString{String: v6, Valid: true},
			ExtraIPs: sql.NullString{String: `["156.245.192.253"]`, Valid: true}, Port: "20000-65000"},
		&model.Tunnel{ID: 851, Name: "沪日首发 -> JP", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1, InIP: sql.NullString{String: v6, Valid: true}},
		&model.ChainTunnel{TunnelID: 851, NodeID: 651, ChainType: "1"},
		&model.Forward{ID: 951, UserID: 2, UserName: "a", Name: "test ss", TunnelID: 851, Status: 1, LandingNodeID: 22, RemoteAddr: "203.0.113.21:30002"},
		&model.ForwardPort{ForwardID: 951, NodeID: 651, Port: 26196},
	} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{
		map[string]any{"kind": "forward_link", "forwardId": 951, "entry": "651|" + v6},
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
	// Before: a literal entry, same body for every Clash client.
	before := map[string]string{}
	for _, ua := range uas {
		before[ua] = pull(ua)
	}
	if before["clash.meta"] != before["NekoBox/Android/1.4.2"] || before["clash.meta"] != before["okhttp/4.12"] {
		t.Fatal("Clash bodies differ without an entry domain")
	}

	// The DDNS main AAAA record goes live and the node address follows it.
	rec := model.NodeDDNSRecord{NodeID: 651, Domain: domain, RecordType: "AAAA", Role: model.DDNSRoleMain, Source: "ct", CurrentIP: v6, TTL: 120, Status: "synced"}
	if err := e.r.DB().Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	e.h.ddnsApply(rec, v6)
	var tun model.Tunnel
	e.r.DB().First(&tun, 851)
	if tun.InIP.String != domain {
		t.Fatalf("tunnel in_ip %q", tun.InIP.String)
	}
	e.h.subState().cache.purgeSub(id)

	domainUAs := map[string]bool{"clash.meta": true, "sing-box 1.14.4": true, "Stash/2.7": true}
	for _, ua := range uas {
		body := pull(ua)
		hasDomain, hasV6 := strings.Contains(body, domain), strings.Contains(body, v6)
		if domainUAs[ua] != hasDomain || hasDomain == hasV6 {
			t.Errorf("%s: domain %v literal %v\n%s", ua, hasDomain, hasV6, body)
		}
		// The other entry (plain IPv4 literal) is unchanged.
		if !strings.Contains(body, "198.51.100.10") {
			t.Errorf("%s lost the IPv4 entry", ua)
		}
		if !domainUAs[ua] && body != before[ua] {
			t.Errorf("%s: literal body changed after the switch", ua)
		}
	}

	// The stored literal key still resolves and the editor rekeys it.
	pool := dataMap(t, subCall(t, e.h, "pool", map[string]any{"id": id}, 2, 1))
	sel := pool["selected"].([]any)[0].(map[string]any)
	if want := (sublink.Ref{Kind: sublink.RefForwardLink, ForwardID: 951, Entry: sublink.EntryKey(651, domain)}).Key(); sel["missing"] == true || sel["key"] != want {
		t.Fatalf("selected %v, want key %s", sel, want)
	}

	// An A record of the same name makes it dual-stack: the domain for all.
	e.r.DB().Create(&model.NodeDDNSRecord{NodeID: 651, Domain: domain, RecordType: "A", Role: model.DDNSRoleExtra, Source: "panel", CurrentIP: "156.245.192.253", Status: "synced"})
	e.h.subState().cache.purgeSub(id)
	if body := pull("NekoBox/Android/1.4.2"); !strings.Contains(body, "server: "+domain) {
		t.Fatalf("dual-stack domain not served to NekoBox:\n%s", body)
	}
}
