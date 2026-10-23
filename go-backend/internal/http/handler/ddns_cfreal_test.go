//go:build cfreal

package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// One real Cloudflare round trip through the panel's updater (plan 142):
//
//	FLVX_CF_TOKEN_FILE=/root/flvx-workers/secrets/cf-dns-token go test -tags cfreal -run TestRealCloudflareRoundTrip -v ./internal/http/handler/
//
// A throwaway record flvx-ddns-test-<ts>.imgamer.top is created, updated,
// read back and DELETED again by the test (the panel code never deletes).
func TestRealCloudflareRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("FLVX_CF_TOKEN_FILE"))
	if err != nil {
		t.Skip("set FLVX_CF_TOKEN_FILE")
	}
	token := strings.TrimSpace(string(raw))
	domain := fmt.Sprintf("flvx-ddns-test-%d.imgamer.top", time.Now().Unix())
	const first, second = "240e:b8f:72d:5c04::dead", "240e:b8f:a30:1904::dead"

	l := newDDNSLab(t)
	m := l.h.ddnsState()
	calls := 0
	m.newClient = func(tok string) *ddns.Client {
		c := ddns.NewCloudflare(tok)
		c.Calls = func(method, path string) { calls++; t.Logf("CF %s %s", method, path) }
		return c
	}
	m.now = time.Now
	l.exec(`UPDATE dns_provider SET credentials = ? WHERE id = 1`, dnsProviderCredentials(token))
	real := ddns.NewCloudflare(token)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	zones, err := real.ListZones(ctx)
	if err != nil {
		t.Fatalf("zones: %v", err)
	}
	zone, ok := ddns.ZoneFor(domain, zones)
	if !ok {
		t.Fatal("imgamer.top not visible")
	}
	if got, _ := real.ListRecords(ctx, zone.ID, domain, ""); len(got) != 0 {
		t.Fatalf("test name already exists: %+v", got)
	}
	// Always remove the throwaway record, also when the test fails.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recs, _ := real.ListRecords(ctx, zone.ID, domain, "")
		for _, rec := range recs {
			req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, "https://api.cloudflare.com/client/v4/zones/"+zone.ID+"/dns_records/"+rec.ID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				t.Logf("cleanup DELETE %s -> HTTP %d", rec.ID, resp.StatusCode)
			}
		}
		left, err := real.ListRecords(ctx, zone.ID, domain, "")
		if err != nil || len(left) != 0 {
			t.Errorf("throwaway record still present: %+v %v", left, err)
		} else {
			t.Logf("verified gone: %s has 0 records", domain)
		}
	})

	cfg := map[string]interface{}{"enabled": true, "providerId": 1, "records": []interface{}{
		map[string]interface{}{"domain": domain, "type": "AAAA", "role": "main", "source": "ct"}}}
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(cfg)); code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	l.wait()
	l.h.onNodeAddrs(23, ws.NodeAddrs{V6: []string{first, cuIP}})
	l.wait()
	recs, err := real.ListRecords(ctx, zone.ID, domain, "AAAA")
	if err != nil || len(recs) != 1 || recs[0].Content != first || recs[0].Proxied || recs[0].TTL != 120 || recs[0].Comment != ddns.RecordComment {
		t.Fatalf("created record = %+v %v", recs, err)
	}
	t.Logf("created %s AAAA %s ttl=%d proxied=%v", domain, recs[0].Content, recs[0].TTL, recs[0].Proxied)
	if n := l.node(); n.ServerIPV6.String != domain {
		t.Fatalf("node field = %q", n.ServerIPV6.String)
	}

	before := calls
	l.h.onNodeAddrs(23, ws.NodeAddrs{V6: []string{second, cuIP}})
	l.wait()
	got, err := real.GetRecord(ctx, zone.ID, recs[0].ID)
	if err != nil || got.Content != second || got.Proxied || got.TTL != 120 {
		t.Fatalf("updated record = %+v %v", got, err)
	}
	t.Logf("updated with %d API call(s); read back %s", calls-before, got.Content)
	rec := l.records()[domain]
	if rec.Status != model.DDNSStatusSynced || rec.CurrentIP != second {
		t.Fatalf("state = %+v", rec)
	}
}
