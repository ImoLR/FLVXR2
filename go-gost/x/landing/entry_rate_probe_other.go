//go:build !linux

package landing

// ProbeEntryRate: the kernel shaper is Linux only (plan 168).
func ProbeEntryRate() string { return "只支持 Linux" }
