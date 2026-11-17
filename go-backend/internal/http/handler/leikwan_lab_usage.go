//go:build leikwanlab

package handler

import (
	"os"
	"time"
)

// Plan 273 lab only (never in release builds): FLVX_LEIKWAN_LAB_USAGE_FAST=1
// starts the 利群用量 poller after 3 s with 20 s rounds 1 s apart, so the
// screens get the card / chips / devices without waiting minutes. The read
// intervals and back-off stay as in production.
func init() {
	if os.Getenv("FLVX_LEIKWAN_LAB_USAGE_FAST") == "1" {
		lkUsageFirstDelay, lkUsageRoundEvery, lkUsageGap = 3*time.Second, 20*time.Second, time.Second
	}
}
