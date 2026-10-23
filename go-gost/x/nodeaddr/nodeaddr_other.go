//go:build !linux

package nodeaddr

import (
	"context"
	"time"
)

func Collect() (Report, error) { return Report{V4: []string{}, V6: []string{}}, nil }

func Watch(ctx context.Context, debounce time.Duration, onChange func()) error {
	<-ctx.Done()
	return ctx.Err()
}
