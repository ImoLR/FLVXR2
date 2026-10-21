package landingcustom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Plan 121: the share example of contract landing-protocols-v1 §2 decodes
// into exactly the advertised capability, and the v1 list stays untouched.
func TestShareProtocolCapsDecodeAndSanitize(t *testing.T) {
	raw := `{"server_id":10,"name":"Dmit T1","hosts":{"ipv4":"154.3.39.138","ipv6":"","domain":""},"status":"active",
"port_min":30000,"port_max":30999,"traffic_limit_bytes":0,"traffic_used_bytes":0,"reset_day":1,"max_connections":1000,
"connections":0,"share_until":null,"core":{"state":"stopped","version":"","epoch":""},
"protocols":["ss2022","vless_reality_vision"],"dual_install":true,
"protocol_caps":{"versions":[1],"protocols":{"anytls":{"udp":true},"snell":{"udp":true,"versions":[4,5],"obfs":["none","http","tls"]},"mieru":{"udp":true,"transports":["TCP","UDP"]}}},
"server_mode":"embedded","busy_ports":[30005,10015,30005,51501,30999,31000]}`
	var s Share
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	s.Sanitize()
	if !reflect.DeepEqual(s.Protocols, []string{"ss2022", "vless_reality_vision"}) {
		t.Fatalf("v1 list changed: %v", s.Protocols)
	}
	for _, p := range ExtraProtocols {
		if _, ok := s.ProtocolCaps.Supports(p); !ok {
			t.Fatalf("%s not advertised", p)
		}
	}
	if cp, _ := s.ProtocolCaps.Supports(ProtocolSnell); !cp.AllowsSnellVersion(5) || cp.AllowsSnellVersion(6) || !cp.AllowsObfs("tls") || cp.AllowsObfs("shadow-tls") {
		t.Fatalf("snell options: %+v", cp)
	}
	// Busy ports: deduplicated, only inside the share range.
	if !reflect.DeepEqual(s.BusyPorts, []int{30005, 30999}) {
		t.Fatalf("busy ports %v", s.BusyPorts)
	}
	if s.ServerMode != "embedded" {
		t.Fatalf("mode %q", s.ServerMode)
	}
}

func TestProtocolCapsGatingAndBounds(t *testing.T) {
	var nilCaps *ProtocolCaps
	if _, ok := nilCaps.Supports(ProtocolAnyTLS); ok {
		t.Fatal("absent capability advertised")
	}
	for name, tc := range map[string]struct {
		caps string
		want []string
	}{
		"no version 1":          {`{"versions":[2],"protocols":{"anytls":{"udp":true}}}`, nil},
		"unknown protocol only": {`{"versions":[1],"protocols":{"hysteria2":{"udp":true}}}`, nil},
		"snell bad versions":    {`{"versions":[1],"protocols":{"snell":{"versions":[3,6]},"mieru":{}}}`, []string{"mieru"}},
		"mieru bad transports":  {`{"versions":[1],"protocols":{"mieru":{"transports":["QUIC"]},"anytls":{"udp":false}}}`, []string{"anytls"}},
		"ss2022 ignored here":   {`{"versions":[1,2],"protocols":{"ss2022":{},"snell":{"udp":false}}}`, []string{"snell"}},
	} {
		s := Share{PortMin: 1024, PortMax: 2048, ServerMode: "turbo"}
		if err := json.Unmarshal([]byte(tc.caps), &s.ProtocolCaps); err != nil {
			t.Fatal(err)
		}
		s.Sanitize()
		got := []string{}
		for _, p := range ExtraProtocols {
			if _, ok := s.ProtocolCaps.Supports(p); ok {
				got = append(got, p)
			}
		}
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %v want %v", name, got, tc.want)
		}
		if s.ServerMode != "" {
			t.Fatalf("%s: unknown mode kept", name)
		}
	}
	// At most 4096 busy ports are kept.
	s := Share{PortMin: 1024, PortMax: 65535}
	for p := 1024; p < 1024+5000; p++ {
		s.BusyPorts = append(s.BusyPorts, p)
	}
	s.Sanitize()
	if len(s.BusyPorts) != 4096 {
		t.Fatalf("busy ports not capped: %d", len(s.BusyPorts))
	}
}

// v1 node payloads must stay byte-identical (no new keys) and the new objects
// use exactly the contract field names.
func TestNodeEncodingExtraProtocols(t *testing.T) {
	v1, _ := json.Marshal(Node{ID: "1", Owner: Owner{ID: "2", Name: "u"}, Name: "n", Protocol: "ss2022", Port: 30000, Enabled: true, SS2022: &SS2022{Method: "m", ServerKey: "a", UserKey: "b"}})
	want := `{"id":"1","owner":{"id":"2","name":"u"},"name":"n","protocol":"ss2022","port":30000,"enabled":true,"max_connections":0,"max_online_ips":0,"expires_at":null,"ss2022":{"method":"m","server_key":"a","user_key":"b"}}`
	if string(v1) != want {
		t.Fatalf("v1 node payload changed:\n%s", v1)
	}
	for _, tc := range []struct {
		node Node
		want string
	}{
		{Node{Protocol: ProtocolAnyTLS, AnyTLS: &AnyTLS{Password: "p", Padding: "default", TLS: AnyTLSTLS{ServerName: "a.example.com", CertificatePEM: "C", KeyPEM: "K"}}}, `"anytls":{"password":"p","padding":"default","tls":{"server_name":"a.example.com","certificate_pem":"C","key_pem":"K"}}`},
		{Node{Protocol: ProtocolSnell, Snell: &Snell{PSK: "k", Version: 4, Obfs: "http", ObfsHost: "www.bing.com"}}, `"snell":{"psk":"k","version":4,"obfs":"http","obfs_host":"www.bing.com"}`},
		{Node{Protocol: ProtocolMieru, Mieru: &Mieru{Username: "u", Password: "p"}}, `"mieru":{"username":"u","password":"p"}`},
	} {
		raw, _ := json.Marshal(tc.node)
		if !strings.Contains(string(raw), tc.want) || strings.Contains(string(raw), `"ss2022"`) || strings.Contains(string(raw), `"vless"`) {
			t.Fatalf("%s payload: %s", tc.node.Protocol, raw)
		}
	}
}

// 422 field paths of the new objects map to the generic credential label;
// the remote message is never echoed.
func TestValidationFieldMappingExtraProtocols(t *testing.T) {
	for field, want := range map[string]string{"snell.version": "协议凭据", "anytls.tls": "协议凭据", "mieru": "协议凭据", "port": "端口", "snellx": "配置", "ss2022.method": "配置"} {
		if got := validationFieldLabel(field); got != want {
			t.Fatalf("%s → %s want %s", field, got, want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"error":{"code":"invalid_desired"},"errors":[{"node_id":"9","field":"snell.version","message":"secret-echo psk=abc"}]}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "flvx_fixture")
	if err != nil {
		t.Fatal(err)
	}
	err = c.PutDesired(context.Background(), 10, Desired{Revision: 2, Nodes: []Node{}})
	e, ok := err.(*APIError)
	if !ok || len(e.Problems) != 1 || e.Problems[0].Field != "协议凭据" || strings.Contains(e.Problems[0].Message, "secret") || e.Problems[0].NodeID != "9" {
		t.Fatalf("422 mapping: %#v", err)
	}
}
