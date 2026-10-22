package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		hdr    map[string]string
		want   string
	}{
		{"direct public peer ignores headers", "198.51.100.7:5000", map[string]string{"Cf-Connecting-Ip": "1.1.1.1", "X-Real-IP": "2.2.2.2"}, "198.51.100.7"},
		{"via nginx direct: X-Real-IP public wins over spoofed CF header", "172.18.0.5:1", map[string]string{"X-Real-IP": "203.0.113.4", "Cf-Connecting-Ip": "1.1.1.1"}, "203.0.113.4"},
		{"via cloudflared+nginx: CF header", "172.18.0.5:1", map[string]string{"X-Real-IP": "172.18.0.1", "Cf-Connecting-Ip": "203.0.113.9", "X-Forwarded-For": "6.6.6.6, 203.0.113.9, 172.18.0.1"}, "203.0.113.9"},
		{"via cloudflared direct to backend", "172.18.0.1:1", map[string]string{"Cf-Connecting-Ip": "2001:db8::5"}, "2001:db8::5"},
		{"xff right-most public", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.10, 10.0.0.2"}, "203.0.113.10"},
		{"loopback only", "127.0.0.1:1", nil, "127.0.0.1"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		for k, v := range tc.hdr {
			r.Header.Set(k, v)
		}
		if got := FromRequest(r); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8:1:2:aaaa::1]:443"
	if got := LimitKey(r); got != "2001:db8:1:2::/64" {
		t.Errorf("v6 limit key %q", got)
	}
}
