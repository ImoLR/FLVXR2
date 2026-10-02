package cnlanding

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type fakeResolver struct {
	addresses map[string][]netip.Addr
	errors    map[string]error
}

func (r fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if err := r.errors[host]; err != nil {
		return nil, err
	}
	return r.addresses[host], nil
}

func TestCheckerAddressClassification(t *testing.T) {
	checker := New(fakeResolver{})
	tests := []struct {
		name       string
		remoteAddr string
		wantError  bool
	}{
		{name: "mainland IPv4", remoteAddr: "1.0.1.1:443", wantError: true},
		{name: "mainland IPv6 bracket form", remoteAddr: "[240e::1]:443", wantError: true},
		{name: "Hong Kong", remoteAddr: "1.32.128.1:443"},
		{name: "Taiwan", remoteAddr: "1.34.0.1:443"},
		{name: "Macau", remoteAddr: "27.109.128.1:443"},
		{name: "United States", remoteAddr: "8.8.8.8:443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checker.Check(context.Background(), test.remoteAddr, "")
			if test.wantError && !IsMainland(err) {
				t.Fatalf("Check(%q) error = %v, want mainland error", test.remoteAddr, err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("Check(%q) error = %v, want nil", test.remoteAddr, err)
			}
		})
	}
}

func TestCheckerDomainResolvingToMainland(t *testing.T) {
	checker := New(fakeResolver{addresses: map[string][]netip.Addr{
		"landing.example": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.0.1.1")},
	}})

	err := checker.Check(context.Background(), "landing.example:443", "")
	if !IsMainland(err) {
		t.Fatalf("Check() error = %v, want mainland error", err)
	}
	if !strings.Contains(err.Error(), "landing.example → 1.0.1.1") {
		t.Fatalf("Check() error = %q, want resolved mainland address", err)
	}
}

func TestCheckerDomainResolutionFailure(t *testing.T) {
	t.Run("resolver error", func(t *testing.T) {
		checker := New(fakeResolver{errors: map[string]error{"missing.example": errors.New("no such host")}})

		err := checker.Check(context.Background(), "missing.example:443", "")
		if !IsResolutionFailure(err) {
			t.Fatalf("Check() error = %v, want resolution error", err)
		}
		if !strings.Contains(err.Error(), "域名 missing.example 解析失败") {
			t.Fatalf("Check() error = %q, want clear domain message", err)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		checker := New(fakeResolver{addresses: map[string][]netip.Addr{"empty.example": {}}})
		if err := checker.Check(context.Background(), "empty.example:443", ""); !IsResolutionFailure(err) {
			t.Fatalf("Check() error = %v, want resolution error", err)
		}
	})
}

func TestCheckerMultiTargetList(t *testing.T) {
	checker := New(fakeResolver{})

	err := checker.Check(context.Background(), "8.8.8.8:53, 1.0.1.1:443", "")
	if !IsMainland(err) || !strings.Contains(err.Error(), "1.0.1.1") {
		t.Fatalf("Check() error = %v, want second target mainland error", err)
	}
}

func TestCheckerTargetCIDROverlap(t *testing.T) {
	checker := New(fakeResolver{})

	if err := checker.Check(context.Background(), "8.8.8.8:53", "1.0.0.0/8"); !IsMainland(err) {
		t.Fatalf("overlapping CIDR error = %v, want mainland error", err)
	}
	if err := checker.Check(context.Background(), "8.8.8.8:53", "2001:4860::/32"); err != nil {
		t.Fatalf("non-overlapping CIDR error = %v, want nil", err)
	}
}
