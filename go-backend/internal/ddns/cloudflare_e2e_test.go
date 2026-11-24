package ddns

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Plan 303: one opt-in check against the REAL Cloudflare API (skipped unless
// FLVX_CF_E2E_TOKEN is set). It creates flvxtest-<random>.<zone> as a
// flvx-user CNAME, re-points it, deletes it, and proves DeleteUserRecord
// refuses a record the panel did not write (read-back only, nothing changed).
//
//	FLVX_CF_E2E_TOKEN=… FLVX_CF_E2E_ZONE=imgamer.top FLVX_CF_E2E_TARGET=vmsilo-humei.imgamer.top \
//	FLVX_CF_E2E_FOREIGN=vmsilo-huri.imgamer.top go test ./internal/ddns -run TestCloudflareUserCNAMEE2E -v
func TestCloudflareUserCNAMEE2E(t *testing.T) {
	token := os.Getenv("FLVX_CF_E2E_TOKEN")
	if token == "" {
		t.Skip("set FLVX_CF_E2E_TOKEN to run against the real Cloudflare API")
	}
	zoneName, target, foreign := os.Getenv("FLVX_CF_E2E_ZONE"), os.Getenv("FLVX_CF_E2E_TARGET"), os.Getenv("FLVX_CF_E2E_FOREIGN")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := NewCloudflare(token)
	zones, err := c.ListZones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var zoneID string
	for _, z := range zones {
		if strings.EqualFold(z.Name, zoneName) {
			zoneID = z.ID
		}
	}
	if zoneID == "" {
		t.Fatalf("zone %s not visible", zoneName)
	}
	before, total, err := c.ZoneRecords(ctx, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	wild := false
	for _, r := range before {
		if r.Name == "*."+zoneName {
			wild = true
		}
	}
	usage, uerr := c.ZoneUsage(ctx, zoneID)
	t.Logf("zone %s: %d records listed (total_count %d), wildcard %v, usage %+v (err %v)", zoneName, len(before), total, wild, usage, uerr)

	name := "flvxtest-" + strings.ToLower(time.Now().UTC().Format("0102150405")) + "." + zoneName
	for _, r := range before {
		if r.Name == name {
			t.Fatalf("%s already exists", name)
		}
	}
	rec, err := c.CreateUserCNAME(ctx, zoneID, name, target)
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	defer func() {
		if !deleted {
			_ = c.DeleteRecord(context.Background(), zoneID, rec.ID)
		}
	}()
	got, err := c.GetRecord(ctx, zoneID, rec.ID)
	if err != nil || got.Type != "CNAME" || got.Content != target || got.Proxied || got.TTL != RecordTTL || !IsUserRecord(got) {
		t.Fatalf("created record: %+v %v", got, err)
	}
	t.Logf("created %s CNAME %s (id %s, ttl %d, proxied %v, comment %q)", got.Name, got.Content, got.ID, got.TTL, got.Proxied, got.Comment)
	if _, err := c.UpdateUserCNAME(ctx, zoneID, rec.ID, name, target); err != nil {
		t.Fatalf("update: %v", err)
	}
	if foreign != "" {
		recs, err := c.ListRecords(ctx, zoneID, foreign, "")
		if err != nil || len(recs) == 0 {
			t.Fatalf("foreign %s: %v %v", foreign, recs, err)
		}
		if err := c.DeleteUserRecord(ctx, zoneID, recs[0].ID, foreign); !errors.Is(err, ErrNotUserRecord) {
			t.Fatalf("DeleteUserRecord on a foreign record: %v", err)
		}
		still, err := c.GetRecord(ctx, zoneID, recs[0].ID)
		if err != nil || still.Content != recs[0].Content {
			t.Fatalf("foreign record changed: %+v %v", still, err)
		}
		t.Logf("foreign %s %s refused, untouched", foreign, still.Type)
	}
	if err := c.DeleteUserRecord(ctx, zoneID, rec.ID, name); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deleted = true
	if _, err := c.GetRecord(ctx, zoneID, rec.ID); !IsNotFound(err) {
		t.Fatalf("still there after delete: %v", err)
	}
	after, _, err := c.ZoneRecords(ctx, zoneID)
	if err != nil || len(after) != len(before) {
		t.Fatalf("record count %d → %d (%v)", len(before), len(after), err)
	}
	t.Logf("deleted %s; zone back to %d records", name, len(after))
}
