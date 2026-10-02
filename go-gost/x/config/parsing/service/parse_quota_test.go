package service

import (
	"testing"

	xservice "github.com/go-gost/x/service"
)

type quotaMetadataTarget struct {
	maxConnections      int
	maxClientIPs        int
	group               string
	groupMaxConnections int
	groupMaxClientIPs   int
}

func (t *quotaMetadataTarget) SetMaxConns(value int) {
	t.maxConnections = value
}

func (t *quotaMetadataTarget) SetMaxClientIPs(value int) {
	t.maxClientIPs = value
}

func (t *quotaMetadataTarget) SetQuotaGroup(group string, maxConnections, maxClientIPs int) {
	t.group = group
	t.groupMaxConnections = maxConnections
	t.groupMaxClientIPs = maxClientIPs
}

func TestApplyQuotaMetadata(t *testing.T) {
	target := &quotaMetadataTarget{}
	applyQuotaMetadata(target, map[string]any{
		"maxConnections":      float64(7),
		"maxClientIps":        3,
		"quotaGroup":          "user-42",
		"groupMaxConnections": 11,
		"groupMaxClientIps":   float64(5),
	})

	if target.maxConnections != 7 || target.maxClientIPs != 3 {
		t.Fatalf("rule limits = %d/%d, want 7/3", target.maxConnections, target.maxClientIPs)
	}
	if target.group != "user-42" || target.groupMaxConnections != 11 || target.groupMaxClientIPs != 5 {
		t.Fatalf("group metadata = %q/%d/%d", target.group, target.groupMaxConnections, target.groupMaxClientIPs)
	}
}

func TestApplyQuotaMetadataMissingBudgetsMeansUnlimited(t *testing.T) {
	target := &quotaMetadataTarget{}
	applyQuotaMetadata(target, map[string]any{"quotaGroup": "user-43"})
	if target.groupMaxConnections != xservice.UnlimitedQuota || target.groupMaxClientIPs != xservice.UnlimitedQuota {
		t.Fatalf("missing group budgets = %d/%d, want -1/-1", target.groupMaxConnections, target.groupMaxClientIPs)
	}
}
