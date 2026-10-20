//go:build !linux

package machineprotect

import "errors"

var errUnsupported = errors.New("nftables is only available on Linux")

type unsupportedGuard struct{}

func newGuard() guard { return unsupportedGuard{} }

func (unsupportedGuard) Ensure(PortSet, bool) error { return errUnsupported }
func (unsupportedGuard) Remove() error              { return nil }
func (unsupportedGuard) Dropped() (int64, error)    { return 0, nil }
func (unsupportedGuard) Blocking() bool             { return false }
func (unsupportedGuard) Supported() bool            { return false }
