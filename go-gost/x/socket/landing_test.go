package socket

import (
	"strings"
	"testing"
)

func TestLandingRefusesProtectedNode36(t *testing.T) {
	reporter := &WebSocketReporter{nodeID: 36}
	if _, err := reporter.handleApplyLandingDesired(map[string]any{"revision": 1, "enabled": true}); err == nil || !strings.Contains(err.Error(), "node 36") {
		t.Fatalf("protected production host accepted landing state: %v", err)
	}
}
