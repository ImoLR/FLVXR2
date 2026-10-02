package socket

import (
	"testing"

	"github.com/go-gost/x/service"
)

func TestHandleSetQuotaGroups(t *testing.T) {
	reporter := &WebSocketReporter{}
	err := reporter.handleSetQuotaGroups([]map[string]interface{}{
		{
			"group":          "socket-command-user-7",
			"maxConnections": 0,
			"maxClientIps":   service.UnlimitedQuota,
		},
	})
	if err != nil {
		t.Fatalf("handle SetQuotaGroups: %v", err)
	}
}

func TestHandleSetQuotaGroupsRejectsInvalidBudget(t *testing.T) {
	reporter := &WebSocketReporter{}
	err := reporter.handleSetQuotaGroups([]map[string]interface{}{
		{
			"group":          "socket-command-invalid",
			"maxConnections": -2,
			"maxClientIps":   service.UnlimitedQuota,
		},
	})
	if err == nil {
		t.Fatal("expected invalid budget error")
	}
}
