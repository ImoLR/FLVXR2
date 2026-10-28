package traffic

import (
	"context"
	"testing"

	"github.com/go-gost/core/limiter"
	"github.com/go-gost/core/limiter/traffic"
)

type fixedTL struct{ in, out traffic.Limiter }

func (f *fixedTL) In(context.Context, string, ...limiter.Option) traffic.Limiter  { return f.in }
func (f *fixedTL) Out(context.Context, string, ...limiter.Option) traffic.Limiter { return f.out }

func TestCombineTrafficLimiters(t *testing.T) {
	a, b := NewLimiter(100), NewLimiter(50)
	base, user := &fixedTL{in: a, out: a}, &fixedTL{in: b}
	if CombineTrafficLimiters(nil, nil) != nil {
		t.Fatal("nil + nil must stay nil")
	}
	if CombineTrafficLimiters(base, nil) != base || CombineTrafficLimiters(nil, user) != user {
		t.Fatal("a single limiter must be returned as is")
	}
	c := CombineTrafficLimiters(base, user)
	g := c.In(context.Background(), "$")
	if g == nil || g.Limit() != 50 {
		t.Fatalf("group limit = %v, want the smaller 50", g)
	}
	if again := c.In(context.Background(), "$"); again != g {
		t.Fatal("same pair must reuse the group (no allocation per read)")
	}
	// The user side has no Out limiter: only the base applies.
	if out := c.Out(context.Background(), "$"); out != a {
		t.Fatalf("out = %v, want the base limiter", out)
	}
	// A replaced user limiter object is picked up at once.
	user.in = NewLimiter(10)
	if g2 := c.In(context.Background(), "$"); g2 == g || g2.Limit() != 10 {
		t.Fatalf("replaced user limiter not used: %v", g2)
	}
	// Removed (nil) or zero limiters do not limit.
	user.in = nil
	if in := c.In(context.Background(), "$"); in != a {
		t.Fatalf("removed user limiter: got %v, want base only", in)
	}
	base.in = NewLimiter(0)
	if in := c.In(context.Background(), "$"); in != nil {
		t.Fatalf("zero + nil: got %v, want nil", in)
	}
}
