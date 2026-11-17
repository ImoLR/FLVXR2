package leikwan_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
)

// Plan 273: the 2026-11-16 API shapes decode without strictness — the forward
// object's new `ingress {host,port}`, `native_direction`, endpoint counts,
// the devices report's `clients[]` and null platform / build fields.
func TestUsageDevicesServicesDecodeNewShapes(t *testing.T) {
	const forward = `{"ok":true,"forward":{"id":263,"service_id":39,"route":{"id":21,"label":"CAN -> HKG"},"listen_port":10018,"transport":"tcp+udp","native_outbound":false,"native_direction":null,"target":{"source":"manual","host":"boilhkt99u.imgamer.top","port":10016,"service_id":null},"ingress":{"host":null,"port":null},"state":"active","endpoint_count":0,"active_endpoint_count":0,"revision":10,"created_at":"2026-10-04 15:05:51","updated_at":"2026-11-17 11:47:19"}}`
	const usage = `{"ok":true,"service_id":39,"status":"active","period":{"name":"current","cycle_started_at":"2026-11-04 12:08:03","cycle_ends_at":"2026-12-04 12:08:03","counted_since":"2026-11-04 12:08:03","traffic_reset_at":null},"usage":{"billing_mode":"monthly_bundle","used_bytes":6543178770,"included_bytes":1073741824000,"remaining_bytes":1067198645230,"used_percent":0.61,"billable_bytes":0,"lifetime_used_bytes":16473512155},"forwards":[{"forward_id":263,"state":"active","listen_port":10018,"transport":"tcp+udp","native_outbound":false,"route":{"id":21,"label":"CAN -> HKG"},"target":{"source":"manual","host":"boilhkt99u.imgamer.top","port":10016,"service_id":null},"bytes_in":2966876432,"bytes_out":650666153,"extra_billed_bytes":0,"total_bytes":3617542585,"share_percent":55.29},{"forward_id":318,"state":"deleted","listen_port":10044,"transport":"tcp","native_outbound":false,"route":{"id":21,"label":"CAN -> HKG"},"target":{"source":"manual","host":"43.199.143.188","port":10044,"service_id":null},"bytes_in":44656093,"bytes_out":34422448,"extra_billed_bytes":0,"total_bytes":79078541,"share_percent":1.21}],"attributed_bytes":6543178770,"unattributed_bytes":0,"daily":[{"date":"2026-11-04","bytes_in":44754864,"bytes_out":13511288,"extra_billed_bytes":0,"total_bytes":58266152}]}`
	const prev = `{"ok":true,"service_id":39,"status":"active","period":{"name":"previous","cycle_started_at":"2026-10-04 12:08:03","cycle_ends_at":"2026-11-04 12:08:03","counted_since":"2026-10-04 12:08:03","traffic_reset_at":null},"usage":{"billing_mode":"monthly_bundle","used_bytes":9930333385,"included_bytes":1073741824000,"remaining_bytes":1063811490615,"used_percent":0.92,"billable_bytes":0},"forwards":[],"attributed_bytes":9930333385,"unattributed_bytes":0,"daily":[]}`
	const devices = `{"ok":true,"online_device_limit":5,"online_device_count":5,"available_device_slots":0,"limit_source":"plan","online_window_seconds":180,"clients":[{"client_id":44,"name":"Default Client","online_device_limit":5,"online_device_count":5,"available_device_slots":0}],"devices":[{"device_id":"linux-x","client_id":44,"device_name":"Leikwan-Client-Agent/2026.11.15.1","platform":null,"os_version":"6.1.0-33-amd64","client_build":"2026.11.15.1","user_agent":"Leikwan-Client-Agent/2026.11.15.1","ip":"8.134.172.154","online":true,"state":"online","can_connect":true,"counts_toward_limit":true,"blocked_until":null,"restorable":false,"first_seen_at":"2026-11-16 10:26:32","last_seen_at":"2026-11-17 11:46:29","last_heartbeat_at":"2026-11-17 11:46:00"},{"device_id":"android-client-44-y","client_id":44,"device_name":"Xiaomi","platform":"android-host","os_version":"16","client_build":null,"ip":"2001:db8::1","online":false,"state":"offline","can_connect":true,"counts_toward_limit":true,"blocked_until":null,"restorable":false,"first_seen_at":"2026-10-31 11:53:06","last_seen_at":"2026-10-31 11:54:34","last_heartbeat_at":"2026-10-31 11:54:34"}]}`
	const services = `{"ok":true,"services":[{"id":39,"name":"1000M 滬日·廣港·京德 | LeiKwan Bridge #947","status":"active","plan":{"id":12,"name":"1000M 滬日·廣港·京德·廣台·福台 1TB"},"capabilities":{"allow_manage_forwards":true,"allow_delete_forwards":true},"active_forward_count":7,"forward_limit":8,"expires_at":"2026-12-07 23:59:59","usage":{"billing_mode":"monthly_bundle","used_bytes":6543178770,"included_bytes":1073741824000,"remaining_bytes":1067198645230,"used_percent":0.61,"billable_bytes":0}}]}`
	var auth []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if r.Method != http.MethodGet {
			t.Errorf("write %s %s", r.Method, r.URL)
		}
		body := map[string]string{"/api/v1/user/forwards/263": forward, "/api/v1/user/devices": devices, "/api/v1/user/services": services}[r.URL.Path]
		if r.URL.Path == "/api/v1/user/services/39/usage" {
			body = usage
			if r.URL.Query().Get("period") == "previous" {
				body = prev
			}
		}
		if body == "" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"not_found"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	ctx := context.Background()

	f, etag, err := c.GetForward(ctx, "tok", 263)
	if err != nil || f.ID != 263 || f.ListenPort != 10018 || f.Target.Host != "boilhkt99u.imgamer.top" || f.Revision != 10 {
		t.Fatalf("forward with ingress {host,port}: %+v %q %v", f, etag, err)
	}
	u, err := c.ServiceUsage(ctx, "tok", 39, false)
	if err != nil || u.Usage.UsedBytes != 6543178770 || u.Usage.RemainingBytes == nil || *u.Usage.RemainingBytes != 1067198645230 ||
		u.Usage.UsedPercent == nil || *u.Usage.UsedPercent != 0.61 || u.Usage.LifetimeUsedBytes == nil || len(u.Forwards) != 2 ||
		u.Forwards[0].ForwardID != 263 || u.Forwards[0].TotalBytes != 3617542585 || u.Forwards[1].State != "deleted" ||
		u.Period.CycleStartedAt != "2026-11-04 12:08:03" || u.Period.TrafficResetAt != nil || len(u.Daily) != 1 {
		t.Fatalf("usage: %+v %v", u, err)
	}
	if got := leikwan.ParseTime(u.Period.CycleEndsAt); got.Format("2006-01-02T15:04:05Z07:00") != "2026-12-04T12:08:03Z" {
		t.Fatalf("UTC parse: %v", got)
	}
	p, err := c.ServiceUsage(ctx, "tok", 39, true)
	if err != nil || p.Period.Name != "previous" || p.Usage.UsedBytes != 9930333385 || p.Usage.LifetimeUsedBytes != nil {
		t.Fatalf("previous: %+v %v", p, err)
	}
	d, err := c.Devices(ctx, "tok")
	if err != nil || d.OnlineDeviceLimit != 5 || d.AvailableDeviceSlots != 0 || len(d.Clients) != 1 || len(d.Devices) != 2 ||
		d.Devices[0].Platform != nil || *d.Devices[0].IP != "8.134.172.154" || d.Devices[1].ClientBuild != nil || d.Devices[1].Online {
		t.Fatalf("devices: %+v %v", d, err)
	}
	sv, err := c.Services(ctx, "tok")
	if err != nil || len(sv) != 1 || sv[0].ExpiresAt == nil || *sv[0].ExpiresAt != "2026-12-07 23:59:59" || sv[0].Plan.Name == "" ||
		sv[0].Usage == nil || sv[0].Usage.IncludedBytes != 1073741824000 || sv[0].ForwardLimit != 8 {
		t.Fatalf("services: %+v %v", sv, err)
	}
	for _, a := range auth {
		if a != "Bearer tok" {
			t.Fatalf("authorization header %q", a)
		}
	}
}

// Plan 273: no closed cycle yet = 404 usage_period_not_found (typed), and a
// failing read never echoes the token.
func TestUsageErrorsTypedAndTokenFree(t *testing.T) {
	f := testapi.New()
	f.NoPrevious = true
	s := httptest.NewServer(f)
	defer s.Close()
	c := leikwan.NewClient(s.URL, nil)
	_, err := c.ServiceUsage(context.Background(), f.Token, 39, true)
	var apiErr *leikwan.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Code != "usage_period_not_found" {
		t.Fatalf("previous without a closed cycle: %v", err)
	}
	if _, err := c.ServiceUsage(context.Background(), "secret-token-xyz", 39, false); err == nil || strings.Contains(err.Error(), "secret-token-xyz") {
		t.Fatalf("bad token: %v", err)
	}
	f.FailReads = 503
	if _, err := c.Devices(context.Background(), f.Token); !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("503: %v", err)
	}
	f.FailReads = 0
	u, err := c.ServiceUsage(context.Background(), f.Token, 39, false)
	if err != nil || u.Usage.IncludedBytes != 1000<<30 || u.Forwards == nil {
		t.Fatalf("fake usage: %+v %v", u, err)
	}
	if req, writes := f.Counts(); writes != 0 || req != 4 {
		t.Fatalf("requests %d writes %d", req, writes)
	}
}
