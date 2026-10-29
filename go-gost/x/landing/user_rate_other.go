//go:build !linux

package landing

import "time"

// The landing Core runs on Linux only; this keeps the package buildable.
func newRateEngine() rateEngine { return stubRateEngine{} }

type stubRateEngine struct{}

func (stubRateEngine) release() RateStatus { return RateStatus{State: "off"} }

func (stubRateEngine) ensure(want rateWant, _ time.Time) RateStatus {
	if !want.active() {
		return RateStatus{State: "off"}
	}
	return RateStatus{State: "error", Error: "按用户限速只支持 Linux", Groups: len(want.groups), Ports: want.ports()}
}
