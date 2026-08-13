package run

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Backoff is how the audit answers a rate-limit refusal: it waits, retries,
// and permanently slows the rest of the run down.
//
// The token bucket in ratelimit.go paces requests at the configured rate, but
// a configured rate is a guess. A tenant's real limit is whatever it is, and
// the audit only learns it by being refused. Without an answer to that
// refusal, a 429 is indistinguishable from a considered "no": one on a
// pre-flight read blocks an entity outright, and one on a delete leaves a live
// object behind in somebody's tenant.
//
// Three parts, and each earns its place:
//
//   - Jitter on every request, so a run's requests do not march in lock-step
//     into whatever window the server meters.
//   - Retry with exponential backoff when a refusal does arrive, honouring
//     Retry-After when the server troubles to send one.
//   - A permanent slowdown once refusals recur, because a rate that is too
//     high stays too high; retrying at the same rate just spends the attempts
//     more slowly.
//
// The rate only ever falls. A run that recovers is not evidence the limit
// lifted, only that the slower rate is working.
const (
	// backoffAttempts is how many attempts one request gets in total: the
	// first, plus four retries.
	backoffAttempts = 5

	// backoffBase is the first retry's delay ceiling. Each further attempt
	// doubles it.
	backoffBase = time.Second

	// backoffCeiling caps a single wait however many attempts have failed, so
	// one unlucky request cannot eat a run's whole time budget.
	backoffCeiling = 32 * time.Second

	// backoffRetryAfterCap bounds what a Retry-After header may ask for. A
	// server is free to name several minutes; a run has a deadline, and
	// waiting past it accomplishes nothing that failing now does not.
	backoffRetryAfterCap = 2 * time.Minute

	// jitterFraction is how much of one request's pacing interval is added, at
	// random, before the request is sent.
	jitterFraction = 0.25

	// slowdownEvery is how many refusals the run absorbs before halving the
	// rate. One 429 is noise — a burst boundary, a neighbour's traffic.
	// Three is a pattern.
	slowdownEvery = 3

	// slowdownFloor is the requests-per-second the run never drops below.
	// Below one the audit is not pacing, it is stopping.
	slowdownFloor = 1
)

// backoff carries one run's refusal history.
type backoff struct {
	mu sync.Mutex
	// seen counts every rate-limit refusal the run has met.
	seen int
	// slowdowns counts how many times the rate has been halved, for the
	// summary a person reads.
	slowdowns int

	// random and sleep are the entropy and the clock, injectable so tests can
	// assert the arithmetic without waiting for it.
	random func() float64
	sleep  func(context.Context, time.Duration) error
}

func newBackoff() *backoff {
	return &backoff{
		random: rand.Float64, //nolint:gosec // jitter timing, not a security decision
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return ctx.Err()
			}
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
}

// rateLimited reports whether a status is the server asking for less traffic.
// Only 429 qualifies: a 503 may mean anything, and retrying every 5xx would
// turn a broken endpoint into five broken requests.
func rateLimited(status int) bool { return status == http.StatusTooManyRequests }

// jitter delays a request by a random slice of its pacing interval. The bucket
// has already granted the token; this is the last thing before the wire.
func (bo *backoff) jitter(ctx context.Context, rps int) error {
	if rps <= 0 {
		return nil
	}
	bo.mu.Lock()
	f := bo.random()
	bo.mu.Unlock()

	interval := float64(time.Second) / float64(rps)
	return bo.sleep(ctx, time.Duration(f*jitterFraction*interval))
}

// pause waits out one rate-limit refusal and reports whether the run should
// try again. attempt is 1-based and counts the attempt that was just refused,
// so the final attempt returns false without sleeping.
//
// The wait is Retry-After when the server sent a usable one, and exponential
// backoff with full jitter otherwise. Full jitter — a uniform draw from zero
// to the ceiling rather than the ceiling itself — is what stops a run's
// refused requests from re-converging on the same instant.
func (bo *backoff) pause(ctx context.Context, attempt int, header http.Header) (bool, error) {
	if attempt >= backoffAttempts {
		return false, nil
	}
	if err := bo.sleep(ctx, bo.delay(attempt, header)); err != nil {
		return false, err
	}
	return true, nil
}

// delay is the wait one refused attempt earns, kept pure so the arithmetic is
// testable without a clock.
func (bo *backoff) delay(attempt int, header http.Header) time.Duration {
	if d, ok := retryAfter(header); ok {
		return d
	}

	ceiling := backoffBase << (attempt - 1)
	if ceiling > backoffCeiling || ceiling <= 0 {
		ceiling = backoffCeiling
	}

	bo.mu.Lock()
	f := bo.random()
	bo.mu.Unlock()
	return time.Duration(f * float64(ceiling))
}

// record notes one refusal and reports the new rate when the run has met
// enough of them to warrant halving, or zero when the rate stands. The caller
// applies it to the bucket, so this stays a decision rather than an effect.
func (bo *backoff) record(current int) int {
	bo.mu.Lock()
	defer bo.mu.Unlock()

	bo.seen++
	if bo.seen%slowdownEvery != 0 {
		return 0
	}
	next := current / 2
	if next < slowdownFloor {
		next = slowdownFloor
	}
	if next >= current {
		return 0
	}
	bo.slowdowns++
	return next
}

// counts reports the refusals met and the slowdowns taken, for the run summary.
func (bo *backoff) counts() (seen, slowdowns int) {
	bo.mu.Lock()
	defer bo.mu.Unlock()
	return bo.seen, bo.slowdowns
}

// retryAfter reads a Retry-After header in either form the RFC allows: a
// count of seconds, or an HTTP date. An unparseable or past value is no
// instruction at all, and the caller falls back to exponential backoff.
func retryAfter(header http.Header) (time.Duration, bool) {
	raw := header.Get("Retry-After")
	if raw == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0, false
		}
		return capWait(time.Duration(secs) * time.Second), true
	}

	when, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	d := time.Until(when)
	if d <= 0 {
		return 0, false
	}
	return capWait(d), true
}

// capWait bounds one wait by what a run can afford to spend on it.
func capWait(d time.Duration) time.Duration {
	if d > backoffRetryAfterCap {
		return backoffRetryAfterCap
	}
	return d
}

// rate reports the bucket's current requests-per-second.
func (b *bucket) rate() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rps
}

// slow lowers the bucket's rate, and never raises it. The accumulated tokens
// are dropped with it: tokens earned at the old rate are exactly the burst the
// server has just refused.
func (b *bucket) slow(rps int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if rps <= 0 || rps >= b.rps {
		return
	}
	b.rps = rps
	if b.tokens > float64(rps) {
		b.tokens = float64(rps)
	}
}
