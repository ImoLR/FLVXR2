package probe

import "time"

// Chart ranges of the detail view. Anything else is rejected.
var ranges = map[string]time.Duration{"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour}

func ParseRange(s string) (time.Duration, bool) {
	d, ok := ranges[s]
	return d, ok
}

const (
	MinPoints     = 60
	MaxPoints     = 720
	DefaultPoints = 360
	pointsStep    = 60
)

// ClampPoints bounds the client's point count and rounds it up to a multiple of
// 60, so cache keys stay few (12 values) whatever a client sends.
func ClampPoints(n int) int {
	if n <= 0 {
		n = DefaultPoints
	}
	if n < MinPoints {
		n = MinPoints
	}
	if n > MaxPoints {
		n = MaxPoints
	}
	return (n + pointsStep - 1) / pointsStep * pointsStep
}

// StepFor is the bucket width for a range drawn with points points: never
// below the 1 s raw rows, never below 30 s where rows are already 30 s means.
func StepFor(d time.Duration, points int) int64 {
	step := d.Milliseconds() / int64(points)
	if step < 1000 {
		step = 1000
	}
	if d > time.Hour && step < 30_000 {
		step = 30_000
	}
	return step
}
