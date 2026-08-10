package run

import (
	"context"
	"sync"
	"time"
)

// bucket is a token bucket pacing requests at the configured rate. The
// capacity equals the rate, so a run may burst one second's allowance and
// then settles to a steady RateLimitRPS — the shape most API rate limits
// tolerate best.
//
// Hand-rolled rather than a library: the toolkit's dependency set is
// owner-approved, and thirty lines of arithmetic do not justify widening
// it.
type bucket struct {
	mu     sync.Mutex
	rps    int
	tokens float64
	last   time.Time
	// now and sleep are the clock, injectable so tests can run at full
	// speed and still assert the arithmetic.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

func newBucket(rps int) *bucket {
	b := &bucket{
		rps: rps,
		now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
	b.tokens = float64(rps)
	b.last = b.now()
	return b
}

// wait blocks until a token is available, or the context dies.
func (b *bucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		nowT := b.now()
		b.tokens += nowT.Sub(b.last).Seconds() * float64(b.rps)
		if b.tokens > float64(b.rps) {
			b.tokens = float64(b.rps)
		}
		b.last = nowT
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - b.tokens) / float64(b.rps) * float64(time.Second))
		b.mu.Unlock()

		if err := b.sleep(ctx, need); err != nil {
			return err
		}
	}
}
