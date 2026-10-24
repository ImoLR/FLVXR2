package landingcustom

import (
	"encoding/json"
	"strings"
	"testing"
)

// Contract landing-conn-mode-v1: conn_modes is optional and bounded; a node's
// conn_mode is omitted unless 仅入站; inbound_connections absent = unknown.
func TestShareConnModesDecodeSanitizeAndWire(t *testing.T) {
	var s Share
	if err := json.Unmarshal([]byte(`{"server_id":16,"port_min":30000,"port_max":30010,"conn_modes":{"versions":[2,1,1,0,-1,5000]}}`), &s); err != nil {
		t.Fatal(err)
	}
	s.Sanitize()
	if !s.ConnModes.Supports(ConnModeVersion) || !s.ConnModes.Supports(2) || len(s.ConnModes.Versions) != 2 {
		t.Fatalf("sanitized %+v", s.ConnModes)
	}
	var none Share
	_ = json.Unmarshal([]byte(`{"server_id":16,"conn_modes":{"versions":[0]}}`), &none)
	none.Sanitize()
	if none.ConnModes != nil || none.ConnModes.Supports(1) {
		t.Fatal("empty conn_modes kept")
	}
	var old Share
	_ = json.Unmarshal([]byte(`{"server_id":16}`), &old)
	old.Sanitize()
	if old.ConnModes.Supports(1) {
		t.Fatal("old Custom advertises conn modes")
	}
	raw, _ := json.Marshal(Node{ID: "1", Protocol: "ss2022", MaxConnections: 100})
	if strings.Contains(string(raw), "conn_mode") {
		t.Fatalf("total node carries conn_mode: %s", raw)
	}
	raw, _ = json.Marshal(Node{ID: "1", Protocol: "ss2022", MaxConnections: 100, ConnMode: ConnModeInbound})
	if !strings.Contains(string(raw), `"conn_mode":"inbound"`) {
		t.Fatalf("inbound node: %s", raw)
	}
	var st Status
	_ = json.Unmarshal([]byte(`{"nodes":[{"id":"1","state":"active","connections":9,"inbound_connections":4},{"id":"2","state":"active","connections":3}]}`), &st)
	if st.Nodes[0].InboundConnections == nil || *st.Nodes[0].InboundConnections != 4 || st.Nodes[1].InboundConnections != nil {
		t.Fatalf("status %+v", st.Nodes)
	}
}
