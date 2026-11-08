//go:build lab

package dnscache

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Lab only (go test -c -tags lab, run inside a systemd container as root): the agent's Ensure path
// (systemd-run --wait, output file, re-check) and the check cost.
func TestLabEnsure(t *testing.T) {
	zones := strings.Fields(os.Getenv("LAB_ZONES"))
	t0 := time.Now()
	out, err := Ensure(context.Background(), zones)
	fmt.Printf("ensure took %v err=%v\n--- output tail ---\n%s\n--- summary: %s\n", time.Since(t0).Round(time.Millisecond), err, out, Summary(out))
	fmt.Printf("check: %+v\n", Check())
}

func BenchmarkCheck(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Check()
	}
}

// Worst case of one check: resolv.conf points at 127.0.0.1 → one pass over /proc/*/comm.
func BenchmarkProcScan(b *testing.B) {
	c := defaultChecker
	for i := 0; i < b.N; i++ {
		_ = c.running("dnsmasq", c.conf)
	}
}
