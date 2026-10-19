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
