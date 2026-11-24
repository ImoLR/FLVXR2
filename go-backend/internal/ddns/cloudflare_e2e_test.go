package ddns

import (
	"context"
	"errors"
	"net"
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

// Plan 303 round 2: the 到期 → 续期 path against the REAL Cloudflare API and
// public resolvers (opt-in like the test above): flvxtest-r<time>.<zone> is
// created, deleted (到期), created again under the SAME name (续期), then
// resolved through 1.1.1.1 and 8.8.8.8, and deleted. The name is never looked
// up while it does not exist (that would cache NXDOMAIN for the zone's SOA
// minimum). FLVX_CF_E2E_NXDEMO=1 also shows that cache: a second name is
// looked up while deleted (as an expired user's client keeps doing), created
// again, and each resolver is asked again (NXDOMAIN + remaining negative TTL
// where it kept the cache).
func TestCloudflareUserCNAMERenewE2E(t *testing.T) {
	token := os.Getenv("FLVX_CF_E2E_TOKEN")
	if token == "" {
		t.Skip("set FLVX_CF_E2E_TOKEN to run against the real Cloudflare API")
	}
	zoneName, target := os.Getenv("FLVX_CF_E2E_ZONE"), os.Getenv("FLVX_CF_E2E_TARGET")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	before, _, err := c.ZoneRecords(ctx, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]string{} // name → record id still to delete
	defer func() {
		for name, id := range live {
			if err := c.DeleteUserRecord(context.Background(), zoneID, id, name); err != nil {
				t.Errorf("cleanup %s: %v", name, err)
			}
		}
		after, _, err := c.ZoneRecords(context.Background(), zoneID)
		if err != nil || len(after) != len(before) {
			t.Errorf("record count %d → %d (%v)", len(before), len(after), err)
		} else {
			t.Logf("cleanup done; zone back to %d records", len(after))
		}
	}()
	stamp := strings.ToLower(time.Now().UTC().Format("0102150405"))
	create := func(name string) string {
		t.Helper()
		for _, r := range before {
			if r.Name == name {
				t.Fatalf("%s already exists", name)
			}
		}
		rec, err := c.CreateUserCNAME(ctx, zoneID, name, target)
		if err != nil {
			t.Fatal(err)
		}
		live[name] = rec.ID
		return rec.ID
	}
	remove := func(name string) {
		t.Helper()
		if err := c.DeleteUserRecord(ctx, zoneID, live[name], name); err != nil {
			t.Fatal(err)
		}
		delete(live, name)
	}

	name := "flvxtest-r" + stamp + "." + zoneName
	id1 := create(name)
	time.Sleep(3 * time.Second)
	remove(name) // 到期
	time.Sleep(3 * time.Second)
	id2 := create(name) // 续期: same name, new record
	t.Logf("%s: created %s, deleted, created again %s (no lookup in between)", name, id1, id2)
	time.Sleep(12 * time.Second) // let every Cloudflare edge have it before the first lookup
	for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		ans, err := dnsQueryAAAA(server, name)
		if err != nil {
			t.Fatalf("%s: %v", server, err)
		}
		if ans.rcode != 0 || ans.answers < 2 {
			t.Fatalf("%s: %s did not resolve after 续期: rcode %d answers %d", server, name, ans.rcode, ans.answers)
		}
		t.Logf("%s: %s → rcode %d, %d answers (CNAME → %s + AAAA), ttl %d", server, name, ans.rcode, ans.answers, target, ans.ttl)
	}
	remove(name)

	if os.Getenv("FLVX_CF_E2E_NXDEMO") != "1" {
		return
	}
	nx := "flvxtest-nx" + stamp + "." + zoneName
	servers := []string{"1.1.1.1:53", "8.8.8.8:53", "223.5.5.5:53", "119.29.29.29:53"}
	create(nx)
	time.Sleep(3 * time.Second)
	remove(nx) // 到期
	time.Sleep(10 * time.Second)
	probe := func(when string) {
		for _, server := range servers { // an expired user's client still trying / the renewed client
			ans, err := dnsQueryAAAA(server, nx)
			if err != nil {
				t.Logf("NX demo %s %s: %v", when, server, err)
				continue
			}
			t.Logf("NX demo %s %s: rcode %d, ttl %d s", when, server, ans.rcode, ans.ttl)
		}
	}
	probe("while deleted")
	create(nx) // 续期
	time.Sleep(12 * time.Second)
	probe("12 s after 续期")
	time.Sleep(80 * time.Second)
	probe("92 s after 续期")
	remove(nx)
}

type dnsAnswer struct {
	rcode, answers int
	ttl            uint32 // first answer's TTL, or the SOA TTL (negative caching) on NXDOMAIN
}

// dnsQueryAAAA sends one recursive AAAA query over UDP (test helper, no deps).
func dnsQueryAAAA(server, name string) (dnsAnswer, error) {
	q := []byte{0x30, 0x33, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 28, 0, 1)
	conn, err := net.DialTimeout("udp", server, 5*time.Second)
	if err != nil {
		return dnsAnswer{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	if _, err := conn.Write(q); err != nil {
		return dnsAnswer{}, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return dnsAnswer{}, err
	}
	m := buf[:n]
	if n < 12 || m[0] != 0x30 || m[1] != 0x33 {
		return dnsAnswer{}, errors.New("bad reply")
	}
	out := dnsAnswer{rcode: int(m[3] & 0x0f), answers: int(m[6])<<8 | int(m[7])}
	nsCount := int(m[8])<<8 | int(m[9])
	off := skipDNSName(m, 12) + 4
	rrTTL := func(off int) (uint32, int) {
		off = skipDNSName(m, off)
		if off+10 > len(m) {
			return 0, len(m)
		}
		ttl := uint32(m[off+4])<<24 | uint32(m[off+5])<<16 | uint32(m[off+6])<<8 | uint32(m[off+7])
		rdlen := int(m[off+8])<<8 | int(m[off+9])
		return ttl, off + 10 + rdlen
	}
	if out.answers > 0 {
		out.ttl, _ = rrTTL(off)
	} else if nsCount > 0 {
		out.ttl, _ = rrTTL(off)
	}
	return out, nil
}

func skipDNSName(m []byte, off int) int {
	for off < len(m) {
		l := int(m[off])
		switch {
		case l == 0:
			return off + 1
		case l&0xc0 == 0xc0:
			return off + 2
		default:
			off += 1 + l
		}
	}
	return len(m)
}
