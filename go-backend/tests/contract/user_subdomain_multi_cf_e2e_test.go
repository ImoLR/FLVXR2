package contract_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 305: one opt-in run against the REAL Cloudflare API (skipped unless
// FLVX_CF_E2E_TOKEN is set). Two throwaway DDNS records stand in for a
// 电信 / 联通 node (flvxtest-<t>-ct / -cu AAAA → 2001:db8:: documentation
// addresses); the panel's own reconcile pass gives a user with a forward
// on that node one CNAME per record, verifies each through the API and the
// zone's authoritative servers (never public resolvers), then the user is
// deleted → both CNAMEs deleted; the AAAA records are deleted last and the
// zone record count must be back to where it was.
//
//	FLVX_CF_E2E_TOKEN=… FLVX_CF_E2E_ZONE=imgamer.top go test ./tests/contract -run TestUserSubdomainMultiRecordCloudflareE2E -v
func TestUserSubdomainMultiRecordCloudflareE2E(t *testing.T) {
	token := os.Getenv("FLVX_CF_E2E_TOKEN")
	zoneName := os.Getenv("FLVX_CF_E2E_ZONE")
	if token == "" || zoneName == "" {
		t.Skip("set FLVX_CF_E2E_TOKEN and FLVX_CF_E2E_ZONE to run against the real Cloudflare API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := ddns.NewCloudflare(token)
	zones, err := c.ListZones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	zoneID := ""
	for _, z := range zones {
		if strings.EqualFold(z.Name, zoneName) {
			zoneID = z.ID
		}
	}
	if zoneID == "" {
		t.Fatalf("zone %s not visible", zoneName)
	}
	before, _, err := c.ZoneRecords(ctx, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	stamp := strings.ToLower(time.Now().UTC().Format("0102150405"))
	ct, cu := "flvxtest-"+stamp+"-ct."+zoneName, "flvxtest-"+stamp+"-cu."+zoneName
	for _, r := range before {
		if r.Name == ct || r.Name == cu {
			t.Fatalf("%s already exists", r.Name)
		}
	}
	created := []string{}
	defer func() {
		// Whatever happened: delete what this test created (by id), names first.
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		for i := len(created) - 1; i >= 0; i-- {
			if err := c.DeleteRecord(cctx, zoneID, created[i]); err != nil && !ddns.IsNotFound(err) {
				t.Errorf("cleanup %s: %v", created[i], err)
			}
		}
		after, _, err := c.ZoneRecords(cctx, zoneID)
		if err != nil || len(after) != len(before) {
			t.Errorf("zone record count %d → %d (%v)", len(before), len(after), err)
		} else {
			t.Logf("zone back to %d records", len(after))
		}
	}()
	recCT, err := c.CreateRecord(ctx, zoneID, "AAAA", ct, "2001:db8::c7")
	if err != nil {
		t.Fatal(err)
	}
	created = append(created, recCT.ID)
	recCU, err := c.CreateRecord(ctx, zoneID, "AAAA", cu, "2001:db8::c0")
	if err != nil {
		t.Fatal(err)
	}
	created = append(created, recCU.ID)
	t.Logf("test records %s (%s) and %s (%s)", ct, recCT.ID, cu, recCU.ID)

	r, err := repo.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := r.DB()
	now := time.Now().UnixMilli()
	creds, _ := json.Marshal(map[string]string{"api_token": token})
	for _, row := range []interface{}{
		&model.DNSProvider{ID: 1, Name: "Cloudflare " + zoneName, ProviderType: "cloudflare", Credentials: string(creds), TokenStatus: "active", Zones: `["` + zoneName + `"]`, CreatedTime: now, UpdatedTime: now},
		&model.Node{ID: 48, Name: "flvxtest 沪台", Secret: "e2e-48", ServerIP: ct, ServerIPV6: sql.NullString{String: ct, Valid: true}, Port: "10000-65535", CreatedTime: now, Status: 1, DDNSEnabled: true, DDNSProviderID: 1},
		&model.NodeDDNSRecord{NodeID: 48, Domain: ct, RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: zoneID, RecordID: recCT.ID, CurrentIP: "2001:db8::c7", TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 0},
		&model.NodeDDNSRecord{NodeID: 48, Domain: cu, RecordType: "AAAA", Role: "extra", Source: "cu", ZoneID: zoneID, RecordID: recCU.ID, CurrentIP: "2001:db8::c0", TTL: 120, Applied: true, Status: "synced", LastSyncAt: now, Inx: 1},
		&model.User{ID: 60, User: "flvxtest-user", Pwd: "x", RoleID: 1, ExpTime: now + 86400000, Flow: 1, FlowResetTime: 1, Num: 1, CreatedTime: now, Status: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	tunnel := model.Tunnel{Name: "e2e", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 1, CreatedTime: now, UpdatedTime: now, Status: 1,
		InIP: sql.NullString{String: ct + "," + cu + ",paid-v4.bgp1.leikwanhost.com", Valid: true}}
	db.Create(&tunnel)
	f := model.Forward{UserID: 60, UserName: "flvxtest-user", Name: "e2e", TunnelID: tunnel.ID, RemoteAddr: "127.0.0.1:1", Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
	db.Create(&f)
	db.Create(&model.ForwardPort{ForwardID: f.ID, NodeID: 48, Port: 40778})
	if _, _, err := r.EnableNodeUserSubdomain(48, false); err != nil {
		t.Fatal(err)
	}
	h := handler.New(r, "e2e-secret", "test")
	var names []model.UserSubdomain
	deadline := time.Now().Add(90 * time.Second)
	for {
		h.UserSubdomainReconcileNow(true)
		names = nil
		db.Where("user_id = 60 AND state = ?", "active").Order("id").Find(&names)
		for _, n := range names {
			if n.RecordID != "" && !usE2EContains(created, n.RecordID) {
				created = append(created, n.RecordID)
			}
		}
		if len(names) == 2 && names[0].ReadyAt > 0 && names[1].ReadyAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("names not ready: %+v", names)
		}
		time.Sleep(2 * time.Second)
	}
	want := map[string]string{names[0].Domain: ct, names[1].Domain: cu}
	if names[0].RecordDomain != "" || names[1].RecordDomain != cu {
		t.Fatalf("record keys: %+v", names)
	}
	for _, n := range names {
		got, err := c.GetRecord(ctx, zoneID, n.RecordID)
		if err != nil || got.Type != "CNAME" || !strings.EqualFold(got.Content, want[n.Domain]) || got.Proxied || got.TTL != ddns.RecordTTL || !ddns.IsUserRecord(got) {
			t.Fatalf("CNAME %s: %+v %v", n.Domain, got, err)
		}
		t.Logf("%s CNAME %s (ttl %d, comment %q), ready after %d ms", got.Name, got.Content, got.TTL, got.Comment, n.ReadyAt-n.CreatedTime)
	}
	names2, _ := r.LoadUserEntryNames([]int64{60})
	if uh := names2.ResolveForward(60, 48, f.ID, cu); uh.Host != names[1].Domain {
		t.Fatalf("resolve -cu: %+v", uh)
	}
	// The user is deleted → both names retired, both CNAMEs deleted.
	db.Where("id = 60").Delete(&model.User{})
	h.UserSubdomainReconcileNow(true)
	for _, n := range names {
		if _, err := c.GetRecord(ctx, zoneID, n.RecordID); !ddns.IsNotFound(err) {
			t.Fatalf("CNAME %s still there: %v", n.Domain, err)
		}
	}
	t.Logf("user deleted → %s and %s deleted", names[0].Domain, names[1].Domain)
	fmt.Fprintln(os.Stderr, "e2e names:", names[0].Domain, names[1].Domain)
}

func usE2EContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
