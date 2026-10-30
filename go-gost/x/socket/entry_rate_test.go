package socket

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-gost/x/landing"
)

// The socket tests never probe the host kernel (the probe creates a
// throw-away IFB); plan 168.
func TestMain(m *testing.M) {
	entryRateProbe = func() string { return "" }
	os.Exit(m.Run())
}

func resetEntryProbe(reason string) {
	entryRateProbeOnce = sync.Once{}
	entryRateProbe = func() string { return reason }
}

func TestEntryRateHandshake(t *testing.T) {
	defer resetEntryProbe("")
	resetEntryProbe("")
	if q := entryRateQuery(5); q != "&entryRateTc=1" {
		t.Fatalf("capable: %q", q)
	}
	if q := entryRateQuery(36); q != "" {
		t.Fatalf("node 36 must not advertise: %q", q)
	}
	resetEntryProbe("内核不支持 ifb：operation not permitted")
	q := entryRateQuery(5)
	if !strings.HasPrefix(q, "&entryRateTc=1&entryRateTcErr=") || !strings.Contains(q, "%E5%86%85") {
		t.Fatalf("probe failure not reported: %q", q)
	}
	t.Setenv("FLVX_ENTRY_RATE_TC", "off")
	resetEntryProbe("")
	if q := entryRateQuery(5); !strings.Contains(q, "entryRateTcErr=") {
		t.Fatalf("kill switch not reported: %q", q)
	}
	w := &WebSocketReporter{nodeID: 5}
	if _, err := w.handleSetEntryRates(map[string]interface{}{"version": 1}); err == nil {
		t.Fatal("kernel without tc accepted SetEntryRates")
	}
	w36 := &WebSocketReporter{nodeID: 36}
	if _, err := w36.handleSetEntryRates(map[string]interface{}{}); err == nil {
		t.Fatal("node 36 accepted SetEntryRates")
	}
	if w36.entryRateReport() != nil {
		t.Fatal("node 36 reports entry rates")
	}
}

func TestEntryRateInvalidPayload(t *testing.T) {
	resetEntryProbe("")
	w := &WebSocketReporter{nodeID: 5}
	if _, err := w.handleSetEntryRates(map[string]interface{}{"version": 1, "classes": []interface{}{map[string]interface{}{"id": "u1", "rate_mbps": 0}}}); err == nil {
		t.Fatal("invalid tree accepted")
	}
	if _, err := w.handleSetEntryRates("not an object"); err == nil {
		t.Fatal("garbage accepted")
	}
	var _ = landing.EntryRateVersion
}
