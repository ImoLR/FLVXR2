package service

import (
	"strconv"
	"testing"
)

func TestServiceClientIPLimit(t *testing.T) {
	s := &defaultService{}
	s.SetMaxClientIPs(2)

	releaseA1 := mustAcquire(t, s, "198.51.100.1")
	releaseA2 := mustAcquire(t, s, "198.51.100.1")
	releaseB := mustAcquire(t, s, "198.51.100.2")

	if release, reason, _, ok := s.acquireConnection("198.51.100.3"); ok {
		release()
		t.Fatal("third distinct client IP was admitted")
	} else if reason != "rule client IPs" {
		t.Fatalf("unexpected rejection reason %q", reason)
	}
	// An already-active IP remains admissible when all distinct-IP slots are in use.
	releaseB2 := mustAcquire(t, s, "198.51.100.2")
	releaseB2()

	releaseA1()
	if release, _, _, ok := s.acquireConnection("198.51.100.3"); ok {
		release()
		t.Fatal("client-IP slot was released before the last connection closed")
	}
	releaseA2()
	releaseC := mustAcquire(t, s, "198.51.100.3")
	releaseC()
	releaseB()
}

func TestServiceClientIPLimitZeroIsUnlimited(t *testing.T) {
	s := &defaultService{}
	s.SetMaxClientIPs(0)
	for i := 0; i < 10; i++ {
		release := mustAcquire(t, s, "203.0.113."+strconv.Itoa(i+1))
		defer release()
	}
}

func TestQuotaGroupSharedAcrossServices(t *testing.T) {
	t.Run("connections", func(t *testing.T) {
		left := &defaultService{}
		right := &defaultService{}
		left.SetQuotaGroup("test-shared-connections", 2, UnlimitedQuota)
		right.SetQuotaGroup("test-shared-connections", 2, UnlimitedQuota)

		releaseLeft := mustAcquire(t, left, "192.0.2.1")
		releaseRight := mustAcquire(t, right, "192.0.2.2")
		if release, reason, _, ok := left.acquireConnection("192.0.2.3"); ok {
			release()
			t.Fatal("shared connection budget admitted a third connection")
		} else if reason != "group connections" {
			t.Fatalf("unexpected rejection reason %q", reason)
		}
		releaseRight()
		releaseThird := mustAcquire(t, left, "192.0.2.3")
		releaseThird()
		releaseLeft()
	})

	t.Run("client IPs", func(t *testing.T) {
		left := &defaultService{}
		right := &defaultService{}
		left.SetQuotaGroup("test-shared-client-ips", UnlimitedQuota, 1)
		right.SetQuotaGroup("test-shared-client-ips", UnlimitedQuota, 1)

		releaseLeft := mustAcquire(t, left, "192.0.2.10")
		releaseRight := mustAcquire(t, right, "192.0.2.10")
		usage := findQuotaUsage(t, "test-shared-client-ips")
		if usage.Connections != 2 || len(usage.ClientIPs) != 1 || usage.ClientIPs[0] != "192.0.2.10" {
			t.Fatalf("shared usage = %+v, want 2 connections and one deduplicated IP", usage)
		}
		if release, reason, _, ok := right.acquireConnection("192.0.2.11"); ok {
			release()
			t.Fatal("shared client-IP budget admitted a second IP")
		} else if reason != "group client IPs" {
			t.Fatalf("unexpected rejection reason %q", reason)
		}
		releaseLeft()
		releaseRight()
		releaseNew := mustAcquire(t, right, "192.0.2.11")
		releaseNew()
	})
}

func TestQuotaGroupBudgetZeroAndUnlimitedEncoding(t *testing.T) {
	s := &defaultService{}
	s.SetQuotaGroup("test-zero-versus-unlimited", UnlimitedQuota, 0)
	if release, reason, _, ok := s.acquireConnection("203.0.113.1"); ok {
		release()
		t.Fatal("zero client-IP budget must mean full")
	} else if reason != "group client IPs" {
		t.Fatalf("unexpected rejection reason %q", reason)
	}

	if err := SetQuotaGroupBudgets([]QuotaGroupBudget{{
		Group:          "test-zero-versus-unlimited",
		MaxConnections: UnlimitedQuota,
		MaxClientIPs:   UnlimitedQuota,
	}}); err != nil {
		t.Fatalf("set unlimited budget: %v", err)
	}
	release := mustAcquire(t, s, "203.0.113.1")
	release()

	if err := SetQuotaGroupBudgets([]QuotaGroupBudget{{
		Group:          "test-zero-versus-unlimited",
		MaxConnections: 0,
		MaxClientIPs:   UnlimitedQuota,
	}}); err != nil {
		t.Fatalf("set full connection budget: %v", err)
	}
	if release, reason, _, ok := s.acquireConnection("203.0.113.2"); ok {
		release()
		t.Fatal("zero connection budget must mean full")
	} else if reason != "group connections" {
		t.Fatalf("unexpected rejection reason %q", reason)
	}
}

func mustAcquire(t *testing.T, s *defaultService, clientIP string) func() {
	t.Helper()
	release, reason, limit, ok := s.acquireConnection(clientIP)
	if !ok {
		t.Fatalf("connection for %s rejected: %s (%d)", clientIP, reason, limit)
	}
	return release
}

func findQuotaUsage(t *testing.T, group string) QuotaGroupUsage {
	t.Helper()
	for _, usage := range QuotaGroupUsages() {
		if usage.Group == group {
			return usage
		}
	}
	t.Fatalf("quota usage for %s not found", group)
	return QuotaGroupUsage{}
}
