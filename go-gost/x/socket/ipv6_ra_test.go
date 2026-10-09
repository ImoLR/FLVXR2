package socket

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemInfoIPv6RA(t *testing.T) {
	d := ipv6RADetector{}
	status, detail := d.result()
	raw, err := json.Marshal(SystemInfo{IPv6RAStatus: status, IPv6RADetail: detail})
	if err != nil || strings.Contains(string(raw), "ipv6_ra_") {
		t.Fatalf("empty RA must be omitted: %s %v", raw, err)
	}
	d.status, d.detail = "ok", "eth0 accept_ra=2（本次由 agent 1→2）"
	status, detail = d.result()
	raw, err = json.Marshal(SystemInfo{IPv6RAStatus: status, IPv6RADetail: detail})
	if err != nil || !strings.Contains(string(raw), `"ipv6_ra_status":"ok"`) || !strings.Contains(string(raw), detail) {
		t.Fatalf("RA report: %s %v", raw, err)
	}
}
