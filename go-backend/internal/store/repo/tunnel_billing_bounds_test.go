package repo

import (
	"math"
	"testing"
)

func TestValidateTunnelBillingBounds(t *testing.T) {
	for _, f := range []int64{1, 2} {
		if err := ValidateTunnelFlowMode(f); err != nil {
			t.Fatalf("flow %d rejected: %v", f, err)
		}
	}
	for _, f := range []int64{0, -1, 3, 99999} {
		if ValidateTunnelFlowMode(f) == nil {
			t.Fatalf("flow %d accepted", f)
		}
	}
	for _, r := range []float64{0.01, 1, 2.5, 100} {
		if err := ValidateTunnelTrafficRatio(r); err != nil {
			t.Fatalf("ratio %v rejected: %v", r, err)
		}
	}
	for _, r := range []float64{0, -1, 0.0099, 100.0001, 99999, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if ValidateTunnelTrafficRatio(r) == nil {
			t.Fatalf("ratio %v accepted", r)
		}
	}
}

func TestValidateImportedTunnelBilling(t *testing.T) {
	// 0 = legacy "unset" (billed as 1) stays importable.
	for _, c := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {2, 100}, {1, 0.01}} {
		if err := validateImportedTunnelBilling(int64(c[0]), c[1]); err != nil {
			t.Fatalf("import flow=%v ratio=%v rejected: %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]float64{{99999, 1}, {3, 1}, {-1, 1}, {1, 99999}, {1, -1}, {1, 0.001}} {
		if validateImportedTunnelBilling(int64(c[0]), c[1]) == nil {
			t.Fatalf("import flow=%v ratio=%v accepted", c[0], c[1])
		}
	}
}
