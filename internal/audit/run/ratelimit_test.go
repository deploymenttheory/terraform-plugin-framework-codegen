package run

import (
	"context"
	"testing"
	"time"
)

// TestUnit_RateLimit_BucketArithmetic drives the bucket with an injected
// clock: after the initial burst, every further token costs 1/rps of
// waiting.
func TestUnit_RateLimit_BucketArithmetic(t *testing.T) {
	t.Parallel()
	b := newBucket(2)
	now := time.Unix(0, 0)
	var slept time.Duration
	b.now = func() time.Time { return now }
	b.sleep = func(_ context.Context, d time.Duration) error {
		slept += d
		now = now.Add(d)
		return nil
	}
	b.last = now
	b.tokens = 2

	for range 6 {
		if err := b.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Six requests at rps=2 with a burst of two: four tokens must be
	// waited for, half a second each.
	if want := 2 * time.Second; slept != want {
		t.Fatalf("slept %s, want %s", slept, want)
	}
}

// TestUnit_RateLimit_HonoredOnTheWire is the timing-tolerant real-clock
// check: eight requests at rps=5 with a burst of five cannot finish in
// under roughly three refill intervals.
func TestUnit_RateLimit_HonoredOnTheWire(t *testing.T) {
	t.Parallel()
	b := newBucket(5)
	start := time.Now()
	for range 8 {
		if err := b.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("8 requests at rps=5 took %s, want at least ~600ms of pacing", elapsed)
	}
}

// TestUnit_RateLimit_WaitRespectsTheContext: a dead context ends the
// wait rather than the pacing outliving the run.
func TestUnit_RateLimit_WaitRespectsTheContext(t *testing.T) {
	t.Parallel()
	b := newBucket(1)
	b.tokens = 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.wait(ctx); err == nil {
		t.Fatal("wait on a cancelled context must error")
	}
}
