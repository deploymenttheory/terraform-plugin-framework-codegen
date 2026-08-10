package run

import (
	"context"
	"fmt"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
)

// runReadWithRetry polls the item read until it shows the created object,
// within the step's poll bounds. The lag between the create and the first
// successful read is the readAfterWrite finding; the read body is the
// evidence every per-field conclusion draws on.
func (r *runner) runReadWithRetry(ctx context.Context, ent *entityState, step *plan.Step) error {
	interval, timeout := pollBounds(step.Poll)
	deadline := time.Now().Add(timeout)

	var last *httpResult
	for {
		res, err := r.do(ctx, ent, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
		if err != nil {
			return err
		}
		last = res
		if res.ok() && res.object() != nil {
			lag := time.Since(ent.createdAt).Round(10 * time.Millisecond)
			if lag < 0 {
				lag = 0
			}
			r.record(ent.plan.Entity, "", observe.KindReadAfterWrite, lag.String(), nil, observe.OutcomeConfirmed, res.excerpt)
			ent.ev.got = res.object()
			ent.ev.readProof = &res.excerpt
			ent.lastRead = res.object()
			return nil
		}
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
		ent.cause = &last.excerpt
		r.record(ent.plan.Entity, "", observe.KindReadAfterWrite, nil, nil, observe.OutcomeInconclusive, last.excerpt)
	}
	return blockedError{reason: fmt.Sprintf("the created object never became readable within %s", timeout)}
}

// runReadConsecutive reads the same object twice and marks every field
// that differed as volatile — the perpetual-diff class a generated
// provider must exclude from drift comparison.
func (r *runner) runReadConsecutive(ctx context.Context, ent *entityState, step *plan.Step) error {
	first, err := r.do(ctx, ent, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
	if err != nil {
		return err
	}
	second, err := r.do(ctx, ent, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
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
		ent.ev.volatile[f] = true
		r.record(ent.plan.Entity, f, observe.KindVolatile, true, nil, observe.OutcomeConfirmed, first.excerpt, second.excerpt)
	}
	ent.lastRead = b
	if ent.ev.got == nil {
		// A lookup entity has no readWithRetry; its consecutive read is
		// still the evidence a read decodes.
		ent.ev.got = b
	}
	return nil
}

// runLookupRead is a lookup datasource's check: fetch by the operator-
// supplied key and confirm the response decodes. A key that fetches
// nothing blocks the entity — there is no object to say anything about.
func (r *runner) runLookupRead(ctx context.Context, ent *entityState, step *plan.Step) error {
	res, err := r.do(ctx, ent, reqSpec{method: step.Method, path: step.Path, pathValues: step.PathValues})
	if err != nil {
		return err
	}
	if !res.ok() {
		ent.cause = &res.excerpt
		return blockedError{reason: fmt.Sprintf("the lookup read answered %d", res.status)}
	}
	if res.object() == nil {
		ent.cause = &res.excerpt
		return blockedError{reason: "the lookup read did not answer a JSON object"}
	}
	ent.lastRead = res.object()
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
