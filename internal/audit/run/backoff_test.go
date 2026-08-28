package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// fixedBackoff is a backoff whose entropy and clock are pinned, so the
// arithmetic can be asserted without waiting for any of it.
func fixedBackoff(random float64) (*backoff, *[]time.Duration) {
	var slept []time.Duration
	bo := newBackoff()
	bo.random = func() float64 { return random }
	bo.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	return bo, &slept
}

func TestUnit_Backoff_RateLimitedRecognisesOnly429(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		status int
		want   bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusForbidden, false},
		// A 503 may mean anything; retrying every 5xx turns one broken
		// endpoint into five broken requests.
		{http.StatusServiceUnavailable, false},
		{http.StatusInternalServerError, false},
	} {
		if got := rateLimited(testCase.status); got != testCase.want {
			t.Errorf("rateLimited(%d) = %v, want %v", testCase.status, got, testCase.want)
		}
	}
}

func TestUnit_Backoff_DelayDoublesUnderFullJitter(t *testing.T) {
	t.Parallel()
	// random() == 1 draws the top of the range, which is the ceiling itself,
	// so the doubling is visible.
	bo, _ := fixedBackoff(1)

	for _, testCase := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
	} {
		if got := bo.delay(testCase.attempt, http.Header{}); got != testCase.want {
			t.Errorf("delay(attempt %d) = %s, want %s", testCase.attempt, got, testCase.want)
		}
	}
}

func TestUnit_Backoff_DelayIsCappedAtTheCeiling(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	// An attempt far beyond the retry cap must not overflow the shift into a
	// negative or absurd duration.
	for _, attempt := range []int{8, 40, 64, 100} {
		got := bo.delay(attempt, http.Header{})
		if got <= 0 || got > backoffCeiling {
			t.Errorf("delay(attempt %d) = %s, want a positive duration no greater than %s",
				attempt, got, backoffCeiling)
		}
	}
}

func TestUnit_Backoff_DelayDrawsBelowTheCeiling(t *testing.T) {
	t.Parallel()
	// Full jitter is a uniform draw from zero to the ceiling, not the ceiling
	// itself: that is what stops refused requests re-converging on one instant.
	bo, _ := fixedBackoff(0.5)
	if got, want := bo.delay(3, http.Header{}), 2*time.Second; got != want {
		t.Errorf("delay with random 0.5 at attempt 3 = %s, want %s (half of the 4s ceiling)", got, want)
	}
}

func TestUnit_Backoff_RetryAfterSecondsWinsOverBackoff(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)
	h := http.Header{}
	h.Set("Retry-After", "7")

	if got, want := bo.delay(1, h), 7*time.Second; got != want {
		t.Errorf("delay with Retry-After 7 = %s, want %s", got, want)
	}
}

func TestUnit_Backoff_RetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(20*time.Second).UTC().Format(http.TimeFormat))

	got := bo.delay(1, h)
	// The header has second granularity and time passes during the test, so
	// the window is generous but still excludes the backoff value of 1s.
	if got < 15*time.Second || got > 21*time.Second {
		t.Errorf("delay with an HTTP-date Retry-After = %s, want roughly 20s", got)
	}
}

func TestUnit_Backoff_RetryAfterIsCapped(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)
	h := http.Header{}
	h.Set("Retry-After", "86400")

	if got := bo.delay(1, h); got != backoffRetryAfterCap {
		t.Errorf("delay with a day-long Retry-After = %s, want the cap %s", got, backoffRetryAfterCap)
	}
}

func TestUnit_Backoff_UnusableRetryAfterFallsBackToBackoff(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	for name, value := range map[string]string{
		"garbage":  "soon",
		"negative": "-5",
		"past date": time.Now().Add(-time.Hour).UTC().
			Format(http.TimeFormat),
	} {
		h := http.Header{}
		h.Set("Retry-After", value)
		if got, want := bo.delay(2, h), 2*time.Second; got != want {
			t.Errorf("%s Retry-After: delay = %s, want the backoff value %s", name, got, want)
		}
	}
}

func TestUnit_Backoff_MissingRetryAfterFallsBackToBackoff(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)
	if got, want := bo.delay(1, http.Header{}), 1*time.Second; got != want {
		t.Errorf("delay with no Retry-After = %s, want %s", got, want)
	}
}

func TestUnit_Backoff_PauseStopsAtTheAttemptCap(t *testing.T) {
	t.Parallel()
	bo, slept := fixedBackoff(1)
	ctx := context.Background()

	for attempt := 1; attempt < backoffAttempts; attempt++ {
		retry, err := bo.pause(ctx, attempt, http.Header{})
		if err != nil {
			t.Fatalf("pause(attempt %d): %v", attempt, err)
		}
		if !retry {
			t.Fatalf("pause(attempt %d) = false, want another attempt", attempt)
		}
	}

	retry, err := bo.pause(ctx, backoffAttempts, http.Header{})
	if err != nil {
		t.Fatalf("pause at the cap: %v", err)
	}
	if retry {
		t.Error("pause at the attempt cap allowed another attempt; the cap is not a cap")
	}
	if len(*slept) != backoffAttempts-1 {
		t.Errorf("slept %d times, want %d — the refused final attempt must not wait for nothing",
			len(*slept), backoffAttempts-1)
	}
}

func TestUnit_Backoff_PauseCarriesTheContextError(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	retry, err := bo.pause(ctx, 1, http.Header{})
	if err == nil {
		t.Fatal("pause on a cancelled context returned no error")
	}
	if retry {
		t.Error("pause on a cancelled context asked for another attempt")
	}
}

func TestUnit_Backoff_RecordHalvesEveryNthRefusal(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	// The first refusals below the threshold are absorbed: one 429 is a burst
	// boundary or a neighbour's traffic, not a verdict on the rate.
	for i := 1; i < slowdownEvery; i++ {
		if got := bo.record(8); got != 0 {
			t.Errorf("record #%d returned %d, want 0 — a lone refusal must not move the rate", i, got)
		}
	}
	if got, want := bo.record(8), 4; got != want {
		t.Errorf("record #%d returned %d, want %d", slowdownEvery, got, want)
	}
}

func TestUnit_Backoff_RecordNeverFallsBelowTheFloor(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	rate := 8
	for range 40 {
		if next := bo.record(rate); next > 0 {
			rate = next
		}
	}
	if rate != slowdownFloor {
		t.Errorf("rate settled at %d, want the floor %d", rate, slowdownFloor)
	}
	if rate < slowdownFloor {
		t.Error("the rate fell below the floor; below one the audit is not pacing, it is stopping")
	}
}

func TestUnit_Backoff_RecordDoesNotRaiseTheRate(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	// Already at the floor: halving would round to the floor again, which is
	// not a reduction, so no slowdown is claimed.
	for range slowdownEvery {
		_ = bo.record(slowdownFloor)
	}
	_, slowdowns := bo.counts()
	if slowdowns != 0 {
		t.Errorf("counted %d slowdowns at the floor, want 0 — a no-op is not a slowdown", slowdowns)
	}
}

func TestUnit_Backoff_CountsReportWhatHappened(t *testing.T) {
	t.Parallel()
	bo, _ := fixedBackoff(1)

	rate := 8
	for range slowdownEvery * 2 {
		if next := bo.record(rate); next > 0 {
			rate = next
		}
	}
	seen, slowdowns := bo.counts()
	if seen != slowdownEvery*2 {
		t.Errorf("counted %d refusals, want %d", seen, slowdownEvery*2)
	}
	if slowdowns != 2 {
		t.Errorf("counted %d slowdowns, want 2", slowdowns)
	}
}

func TestUnit_Backoff_JitterStaysWithinItsFractionOfTheInterval(t *testing.T) {
	t.Parallel()
	bo, slept := fixedBackoff(1)

	if err := bo.jitter(context.Background(), 4); err != nil {
		t.Fatalf("jitter: %v", err)
	}
	if len(*slept) != 1 {
		t.Fatalf("jitter slept %d times, want 1", len(*slept))
	}
	// One quarter of a 250ms interval, at the top of the draw.
	if got, want := (*slept)[0], 62500*time.Microsecond; got != want {
		t.Errorf("jitter slept %s, want %s", got, want)
	}
}

func TestUnit_Backoff_JitterIsSkippedWithoutARate(t *testing.T) {
	t.Parallel()
	bo, slept := fixedBackoff(1)

	if err := bo.jitter(context.Background(), 0); err != nil {
		t.Fatalf("jitter: %v", err)
	}
	if len(*slept) != 0 {
		t.Errorf("jitter slept %d times at rate 0, want 0", len(*slept))
	}
}

func TestUnit_Bucket_SlowLowersTheRateAndNeverRaisesIt(t *testing.T) {
	t.Parallel()
	b := newBucket(8)

	if got := b.rate(); got != 8 {
		t.Fatalf("rate() = %d, want 8", got)
	}

	b.slow(4)
	if got := b.rate(); got != 4 {
		t.Errorf("after slow(4), rate() = %d, want 4", got)
	}

	// A run that recovers is not evidence the limit lifted.
	b.slow(6)
	if got := b.rate(); got != 4 {
		t.Errorf("after slow(6) on a rate of 4, rate() = %d, want 4 — the rate only ever falls", got)
	}
	b.slow(0)
	if got := b.rate(); got != 4 {
		t.Errorf("after slow(0), rate() = %d, want 4", got)
	}
	b.slow(-1)
	if got := b.rate(); got != 4 {
		t.Errorf("after slow(-1), rate() = %d, want 4", got)
	}
}

func TestUnit_Bucket_SlowDropsTheBurstItEarned(t *testing.T) {
	t.Parallel()
	b := newBucket(8)
	// A fresh bucket holds a full second's allowance; tokens earned at the old
	// rate are exactly the burst the server has just refused.
	b.slow(2)

	b.mu.Lock()
	tokens := b.tokens
	b.mu.Unlock()

	if tokens > 2 {
		t.Errorf("after slow(2) the bucket still holds %v tokens, want no more than 2", tokens)
	}
}

// throttlingServer refuses the first refusals requests to any path with 429
// and serves a normal widget lifecycle thereafter. Retry-After is zero so the
// test exercises the real retry path without waiting out a real backoff.
type throttlingServer struct {
	mu       sync.Mutex
	refusals int
	refused  int
	seq      int
	objects  map[string]map[string]any
}

func newThrottlingServer(refusals int) *throttlingServer {
	return &throttlingServer{refusals: refusals, objects: map[string]map[string]any{}}
}

func (s *throttlingServer) refusedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}

func (s *throttlingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.refused < s.refusals {
		s.refused++
		s.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/widgets" && r.Method == http.MethodPost:
		s.mu.Lock()
		s.seq++
		id := fmt.Sprintf("w%d", s.seq)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body == nil {
			body = map[string]any{}
		}
		body["id"] = id
		s.objects[id] = body
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	case r.URL.Path == "/widgets":
		s.mu.Lock()
		out := make([]map[string]any, 0, len(s.objects))
		for _, o := range s.objects {
			out = append(out, o)
		}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, "/widgets/"):
		id := strings.TrimPrefix(r.URL.Path, "/widgets/")
		s.mu.Lock()
		obj, ok := s.objects[id]
		if r.Method == http.MethodDelete {
			delete(s.objects, id)
		}
		s.mu.Unlock()
		switch {
		case r.Method == http.MethodDelete && ok:
			w.WriteHeader(http.StatusNoContent)
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(w).Encode(obj)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// TestUnit_Run_RateLimitedRequestsAreRetriedNotRecorded is the whole point of
// backoff.go: before it, one 429 on a pre-flight read blocked an entity
// outright and one on a delete orphaned a live object. The entity must now
// finish, and the run must say it was throttled.
func TestUnit_Run_RateLimitedRequestsAreRetriedNotRecorded(t *testing.T) {
	t.Parallel()
	srv := newThrottlingServer(4)
	s := httptest.NewServer(srv)
	t.Cleanup(s.Close)

	item := map[string]string{"widgetId": "$created:widget"}
	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity:     "widget",
			AuditShape: "resource",
			Budget:     plan.Budget{Requests: 50},
			Steps: []plan.Step{
				{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/widgets",
					Body: map[string]any{"name": "tfpfgen-<runid>-widget-name"}},
				{Kind: plan.StepReadWithRetry, Method: "GET", Path: "/widgets/{widgetId}", PathValues: item,
					Poll: &plan.Poll{Interval: "10ms", Timeout: "200ms"}},
				{Kind: plan.StepDeleteWithConfirmation, Method: "DELETE", Path: "/widgets/{widgetId}", PathValues: item},
			},
		}},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}

	opts := Options{
		Plan:         p,
		BaseURL:      s.URL,
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(nil).Level(zerolog.Disabled),
		Lookup:       lookupOf(map[string]string{config.SecretToken: testToken}),
		RunID:        "testrun1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if srv.refusedCount() != 4 {
		t.Fatalf("the server refused %d requests, want 4 — the test proves nothing otherwise", srv.refusedCount())
	}

	got := entityStatus(t, summary, "widget")
	if got.Outcome != observe.OutcomeConfirmed {
		t.Errorf("widget = %+v, want audited — a rate-limit refusal must not block an entity", got)
	}

	if summary.RateLimited != 4 {
		t.Errorf("summary counted %d rate-limit refusals, want 4", summary.RateLimited)
	}
	// Four refusals, halving every slowdownEvery: the rate must have fallen.
	if summary.Slowdowns < 1 {
		t.Errorf("summary counted %d slowdowns after 4 refusals, want at least 1", summary.Slowdowns)
	}
	if summary.RateLimitRPS >= opts.RateLimitRPS {
		t.Errorf("run finished at %d rps, want below the configured %d", summary.RateLimitRPS, opts.RateLimitRPS)
	}

	// Every attempt is charged: a retried request is real load on the tenant.
	if summary.Requests <= 3 {
		t.Errorf("run charged %d requests; the retries are not being counted", summary.Requests)
	}
}

// TestUnit_Run_RelentlessRateLimitingGivesUpWithoutOrphaning holds the other
// edge: an API that never stops refusing must exhaust the attempts and report,
// not spin forever.
func TestUnit_Run_RelentlessRateLimitingGivesUpWithoutOrphaning(t *testing.T) {
	t.Parallel()
	srv := newThrottlingServer(1 << 30)
	s := httptest.NewServer(srv)
	t.Cleanup(s.Close)

	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity:     "widget",
			AuditShape: "resource",
			Budget:     plan.Budget{Requests: 50},
			Steps: []plan.Step{
				{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/widgets",
					Body: map[string]any{"name": "tfpfgen-<runid>-widget-name"}},
			},
		}},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}

	opts := Options{
		Plan:         p,
		BaseURL:      s.URL,
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(nil).Level(zerolog.Disabled),
		Lookup:       lookupOf(map[string]string{config.SecretToken: testToken}),
		RunID:        "testrun1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if summary.RateLimited < backoffAttempts {
		t.Errorf("summary counted %d refusals, want at least the %d attempts one request gets",
			summary.RateLimited, backoffAttempts)
	}
	// One logical request earns backoffAttempts refusals, which is one
	// halving, not a descent to the floor — the floor is reached by a run that
	// keeps going, and this plan has nowhere left to go.
	if summary.RateLimitRPS >= opts.RateLimitRPS {
		t.Errorf("run finished at %d rps, want below the configured %d", summary.RateLimitRPS, opts.RateLimitRPS)
	}
	if summary.Slowdowns < 1 {
		t.Errorf("summary counted %d slowdowns under relentless refusal, want at least 1", summary.Slowdowns)
	}
	// The attempts are capped, so a server that never relents costs a bounded
	// number of requests rather than spinning until the run's deadline.
	if summary.Requests > backoffAttempts*2 {
		t.Errorf("run charged %d requests for a single refused step; the attempt cap is not holding",
			summary.Requests)
	}
}
