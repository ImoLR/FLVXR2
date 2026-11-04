package landingcustom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Plan 110: Custom errors must be specific; only known codes or a sanitized
// code are shown, never the remote message.
func TestAPIErrorTextIsSpecific(t *testing.T) {
	for _, tc := range []struct {
		err  APIError
		want string
	}{
		{APIError{Status: 409, Code: "host_resolution"}, "DNS"},
		{APIError{Status: 409, Code: "official_ports_unknown"}, "官方入站端口"},
		{APIError{Status: 422, Code: "invalid_hosts"}, "FLVX 节点地址"},
		{APIError{Status: 409, Code: "helper_outdated"}, "Helper 需升级"},
		{APIError{Status: 409, Code: "share_inactive"}, "暂停或撤销"},
		{APIError{Status: 409, Code: "brand_new_state"}, "brand_new_state"},
		{APIError{Status: 422}, "拒绝节点配置"},
		{APIError{Status: 401}, "令牌"},
		{APIError{Status: 503, Code: "unavailable"}, "暂不可用"},
	} {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) {
			t.Fatalf("%+v → %q, want %q", tc.err, got, tc.want)
		}
	}
	if got := (&APIError{Status: 409, Code: "<script>secret token"}).Error(); strings.Contains(got, "script") || strings.Contains(got, "secret") {
		t.Fatalf("unsanitized code echoed: %q", got)
	}
}

// An empty or unknown core state is accepted (counters still book).
func TestStatusAcceptsUnknownCoreState(t *testing.T) {
	state := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"applied_revision": 0, "collected_at": 0, "core": map[string]any{"state": state}, "nodes": []any{}, "share_traffic_used_bytes": 0})
	}))
	defer srv.Close()
	c, err := New(srv.URL, "flvx_fixture")
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(context.Background(), 5)
	if err != nil || st.Core.State != "stopped" {
		t.Fatalf("empty state: %+v %v", st, err)
	}
	state = "helper_outdated"
	if st, err = c.Status(context.Background(), 5); err != nil || st.Core.State != "helper_outdated" {
		t.Fatalf("unknown state: %+v %v", st, err)
	}
}

// Plan 205 (landing-scan-guard-v1 §9.3): the relayed scan report keeps
// well-formed hits (id as a string or a number, tag derived from id), drops
// bad ones, and a malformed or unknown report never fails the poll.
func TestStatusScanGuardReport(t *testing.T) {
	var guard any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"applied_revision": 0, "collected_at": 0, "core": map[string]any{"state": "running"}, "nodes": []any{}, "share_traffic_used_bytes": 0}
		if guard != nil {
			body["scan_guard"] = guard
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	c, err := New(srv.URL, "flvx_fixture")
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(context.Background(), 5)
	if err != nil || st.ScanGuard != nil {
		t.Fatalf("absent: %+v %v", st.ScanGuard, err)
	}
	guard = map[string]any{"state": "on", "blocked": []string{"flvxl-95"}, "hits": []any{
		map[string]any{"id": "95", "inbound_tag": "flvxl-95", "type": "horizontal", "count": 100, "port": 22, "window_s": 60, "at": "2026-11-04T08:00:00.123456789Z", "seq": 1793709225987084},
		map[string]any{"id": 96, "type": "total", "count": 1000, "window_s": 300, "at": "2026-11-04T08:00:01Z", "seq": 1793709225987085},
		map[string]any{"id": "x", "type": "total", "seq": 7},
		map[string]any{"inbound_tag": "flvxl-07", "type": "total", "seq": 8},
		map[string]any{"id": "97", "type": "total", "seq": 0},
		map[string]any{"id": "98", "type": "total", "seq": 9007199254740992},
		map[string]any{"id": "99", "type": "total", "seq": "12"},
		map[string]any{"id": []int{1}, "type": "total", "seq": 13},
	}}
	st, err = c.Status(context.Background(), 5)
	if err != nil || st.ScanGuard == nil || len(st.ScanGuard.Hits) != 2 || st.ScanGuard.Hits[0].InboundTag != "flvxl-95" || st.ScanGuard.Hits[1].InboundTag != "flvxl-96" ||
		st.ScanGuard.Hits[0].Seq != 1793709225987084 || st.ScanGuard.Hits[0].Port != 22 || st.ScanGuard.Hits[0].At.IsZero() || st.ScanGuard.Blocked[0] != "flvxl-95" {
		t.Fatalf("report: %+v %v", st.ScanGuard, err)
	}
	for _, bad := range []any{"on", map[string]any{"state": "exploded"}, map[string]any{"state": "on", "hits": "x"}, map[string]any{"state": 5}} {
		guard = bad
		if st, err = c.Status(context.Background(), 5); err != nil || st.ScanGuard != nil || st.Core.State != "running" {
			t.Fatalf("bad %v: %+v %v", bad, st.ScanGuard, err)
		}
	}
	// At most 256 hits are kept.
	many := []any{}
	for i := 0; i < 300; i++ {
		many = append(many, map[string]any{"id": "95", "type": "total", "seq": 1000 + i})
	}
	guard = map[string]any{"state": "on", "hits": many}
	if st, err = c.Status(context.Background(), 5); err != nil || len(st.ScanGuard.Hits) != 256 {
		t.Fatalf("cap: %d %v", len(st.ScanGuard.Hits), err)
	}
}

// Plan 205: the share capability is bounded like the other version lists.
func TestShareScanGuardCapability(t *testing.T) {
	s := Share{ScanGuard: &ShareScanGuard{Versions: []int{1, 1, 0, -2, 5000}}}
	s.Sanitize()
	if !s.ScanGuard.Supports(ScanGuardVersion) || len(s.ScanGuard.Versions) != 1 {
		t.Fatalf("%+v", s.ScanGuard)
	}
	s = Share{ScanGuard: &ShareScanGuard{Versions: []int{0}}}
	s.Sanitize()
	if s.ScanGuard != nil || s.ScanGuard.Supports(1) {
		t.Fatal("empty capability kept")
	}
}
