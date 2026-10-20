//go:build !linux

package landing

// Landing runs on Linux only; install refuses elsewhere before this matters.
func checkInstallResources(string) error { return nil }
