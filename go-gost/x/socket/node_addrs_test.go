package socket

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-gost/x/nodeaddr"
)

func TestAddrsReportOnlyWhenChangedAndOncePerSession(t *testing.T) {
	current := nodeaddr.Report{V4: []string{}, V6: []string{"240e:b8f:a30:1904:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"}}
	w := &WebSocketReporter{}
	w.addrs.collect = func() (nodeaddr.Report, error) { return current, nil }

	if w.addrsReport() != nil {
		t.Fatal("nothing collected yet: no report")
	}
	w.addrs.refresh()
	first := w.addrsReport()
	if first == nil || len(first.V6) != 2 {
		t.Fatalf("first metric after collect = %+v", first)
	}
	if w.addrsReport() != nil {
		t.Fatal("unchanged set must not be sent again")
	}
	w.addrs.refresh() // e.g. RA lifetime refresh event: same set
	if w.addrsReport() != nil {
		t.Fatal("same set after a refresh must not be sent")
	}
	// New session: the first metric carries the current set again.
	w.addrsSentSeq = 0
	if w.addrsReport() == nil {
		t.Fatal("new session must resend the set")
	}
	// Prefix rotation: sent once.
	current = nodeaddr.Report{V4: []string{}, V6: []string{"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"}}
	w.addrs.refresh()
	next := w.addrsReport()
	if next == nil || next.V6[0] != "240e:b8f:72d:5c04:ca:8aff:fec0:2813" {
		t.Fatalf("changed set = %+v", next)
	}
	if w.addrsReport() != nil {
		t.Fatal("changed set sent twice")
	}
}

func TestMetricAddrsFieldOmittedUnlessChanged(t *testing.T) {
	raw, _ := json.Marshal(metricEnvelope{Type: "metric", Data: SystemInfo{}})
	if strings.Contains(string(raw), `"addrs"`) {
		t.Fatalf("addrs must be omitted when unchanged: %s", raw)
	}
	r := nodeaddr.Report{V4: []string{}, V6: []string{"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"}}
	field, _ := json.Marshal(map[string]interface{}{"addrs": r})
	raw, _ = json.Marshal(metricEnvelope{Type: "metric", Data: SystemInfo{Addrs: &r}})
	if !strings.Contains(string(raw), `"addrs":{"v4":[],"v6":["240e:b8f:72d:5c04:ca:8aff:fec0:2813"`) {
		t.Fatalf("addrs missing: %s", raw)
	}
	t.Logf("addrs field size for node 48 (2 IPv6): %d bytes", len(field)-2)
}

func TestHandshakeAnnouncesAddrReport(t *testing.T) {
	if q := buildWebSocketQuery("s", "v", 0, 0, 0, 0); !strings.Contains(q, "&addrReport=1") {
		t.Fatalf("query %q lacks addrReport", q)
	}
}
