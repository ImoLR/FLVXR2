//go:build warmlab

package warmpool

import (
	"os"
	"time"
)

// Local lab only (plan 249): `go build -tags warmlab` lets
// FLVX_WARM_IDLE_SLEEP (e.g. "60s") shorten the idle sleep so the lab can show
// a pool draining and waking without waiting hours; the panel's idleSleepS
// is then ignored (plan 264). Release builds do not contain this file: there
// the timer is IdleSleepAfter or the panel's value.
func init() {
	v := os.Getenv("FLVX_WARM_IDLE_SLEEP")
	if v == "" {
		return
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		idleSleepDefault = d
		idleSleepLocked = true
		Default.mu.Lock()
		Default.idleSleep = d
		Default.mu.Unlock()
	}
}
