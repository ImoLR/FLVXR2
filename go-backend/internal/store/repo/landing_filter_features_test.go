package repo

import (
	"reflect"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 224 lab table: first client packet of each landing protocol (mihomo
// v1.19.15) through go-gost's entry filter (isProtocolBlocked).
func TestLandingFilterFeaturesLabTable(t *testing.T) {
	for _, c := range []struct {
		protocol, opts string
		want           []string
	}{
		{"ss2022", "", nil},
		{"vless_reality_vision", "", []string{"tls"}},
		{"anytls", `{"sni":"a.example"}`, []string{"tls"}},
		{"snell", `{"version":4,"obfs":"none"}`, nil},
		{"snell", `{"version":5}`, nil},
		{"snell", `{"version":4,"obfs":"tls","obfsHost":"bing.com"}`, []string{"tls"}},
		{"snell", `{"version":4,"obfs":"http","obfsHost":"bing.com"}`, []string{"http"}},
		{"mieru", `{"transport":"TCP"}`, nil},
		{"mieru", `{"transport":"UDP"}`, nil},
	} {
		n := model.LandingNode{Protocol: c.protocol, ProtocolOpts: c.opts}
		if got := LandingNodeFilterFeatures(&n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: %v, want %v", c.protocol, c.opts, got, c.want)
		}
	}
	s := &model.LandingServer{SnellObfs: "tls"}
	if got := LandingServerFilterFeatureMap(s, LandingFilterProtocols); !reflect.DeepEqual(got, map[string][]string{"vless_reality_vision": {"tls"}, "anytls": {"tls"}, "snell": {"tls"}}) {
		t.Errorf("server map: %v", got)
	}
	for _, c := range []struct {
		tls, http int
		features  []string
		want      string
	}{
		{1, 1, []string{"tls"}, "tls"},
		{0, 1, []string{"tls"}, ""}, // Dmit Nathan 线路机: VLESS passes
		{1, 1, nil, ""},             // SS2022 on 专线
		{1, 0, []string{"http"}, ""},
		{0, 1, []string{"http"}, "http"},
	} {
		if got := TunnelBlockedFeature(c.tls, c.http, c.features); got != c.want {
			t.Errorf("tls=%d http=%d %v: %q want %q", c.tls, c.http, c.features, got, c.want)
		}
	}
}
