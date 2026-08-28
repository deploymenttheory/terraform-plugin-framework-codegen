package run

import (
	"context"
	"fmt"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
)

// runReadWithRetry polls the item read until it shows the created object,
// within the step's poll bounds. The readAfterWrite finding is how long the
// object took to become readable, measured as the number of failed polls times
// the poll interval — a wall-clock duration would fold request latency and
// scheduling noise into the value and never reproduce across runs, so the same
// tenant would revise to a different spec each audit. An object readable on the
// first attempt lagged not at all: zero. The read body is the evidence every
// per-field conclusion draws on.
func (r *runner) runReadWithRetry(ctx context.Context, entity *entityState, step *plan.Step) error {
	if entity.idUnknown {
		// The object was created but its id could not be learned, so there is
		// no item URL to read it back at. The read-after-write claim is
		// inconclusive — recorded, not silently skipped — and the entity keeps
		// going: its create-only findings are still worth gathering.
		r.record(entity.plan.Entity, "", observe.KindReadAfterWrite, nil, nil, observe.OutcomeInconclusive)
		return nil
	}
	interval, timeout := pollBounds(step.Poll)
	deadline := time.Now().Add(timeout)

	var last *httpResult
	polls := 0
	for {
		res, err := r.do(ctx, entity, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
		if err != nil {
			return err
		}
		last = res
		if res.ok() && res.object() != nil {
			lag := time.Duration(polls) * interval
			r.record(entity.plan.Entity, "", observe.KindReadAfterWrite, lag.String(), nil, observe.OutcomeConfirmed, res.excerpt)
			entity.ev.got = res.object()
			entity.ev.readProof = &res.excerpt
			entity.lastRead = res.object()
			r.observeUndeclaredFields(entity, entity.lastRead, res.excerpt)
			return nil
		}
		polls++
		if time.Now().Add(interval).After(deadline) {
			break
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}

	// The object never became readable within the poll window. The claim
	// is inconclusive, and everything downstream of a readable object is
	// blocked.
	if last != nil {
		entity.cause = &last.excerpt
		r.record(entity.plan.Entity, "", observe.KindReadAfterWrite, nil, nil, observe.OutcomeInconclusive, last.excerpt)
	}
	return blockedError{reason: fmt.Sprintf("the created object never became readable within %s", timeout)}
}

// runReadConsecutive reads the same object twice and marks every field
// that differed as volatile — the perpetual-diff class a generated
// provider must exclude from drift comparison.
func (r *runner) runReadConsecutive(ctx context.Context, entity *entityState, step *plan.Step) error {
	if entity.idUnknown {
		// No item URL to read the object at, so volatility cannot be observed.
		return nil
	}
	first, err := r.do(ctx, entity, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
	if err != nil {
		return err
	}
	second, err := r.do(ctx, entity, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
	if err != nil {
		return err
	}
	if !first.ok() || !second.ok() {
		return nil
	}
	a, b := first.object(), second.object()
	if a == nil || b == nil {
		return nil
	}
	for _, f := range sortedFieldUnion(a, b) {
		if equalJSON(a[f], b[f]) {
			continue
		}
		entity.ev.volatile[f] = true
		r.record(entity.plan.Entity, f, observe.KindVolatile, true, nil, observe.OutcomeConfirmed, first.excerpt, second.excerpt)
	}
	r.observeUndeclaredFields(entity, a, first.excerpt)
	r.observeUndeclaredFields(entity, b, second.excerpt)
	entity.lastRead = b
	if entity.ev.got == nil {
		// A lookup entity has no readWithRetry; its consecutive read is
		// still the evidence a read decodes.
		entity.ev.got = b
	}
	return nil
}

// runLookupRead is a lookup datasource's check: fetch by the operator-
// supplied key and confirm the response decodes. A key that fetches
// nothing blocks the entity — there is no object to say anything about.
func (r *runner) runLookupRead(ctx context.Context, entity *entityState, step *plan.Step) error {
	res, err := r.do(ctx, entity, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
	if err != nil {
		return err
	}
	if !res.ok() {
		entity.cause = &res.excerpt
		return blockedError{reason: fmt.Sprintf("the lookup read answered %d", res.status)}
	}
	if res.object() == nil {
		entity.cause = &res.excerpt
		return blockedError{reason: "the lookup read did not answer a JSON object"}
	}
	entity.lastRead = res.object()
	return nil
}

// pollBounds parses a step's poll spec, with the plan's defaults as the
// fallback for anything unparseable.
func pollBounds(p *plan.Poll) (interval, timeout time.Duration) {
	interval, timeout = 2*time.Second, 30*time.Second
	if p == nil {
		return interval, timeout
	}
	if d, err := time.ParseDuration(p.Interval); err == nil && d > 0 {
		interval = d
	}
	if d, err := time.ParseDuration(p.Timeout); err == nil && d > 0 {
		timeout = d
	}
	return interval, timeout
}

// sleepCtx waits, or returns early when the context dies.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
