package run

import (
	"context"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
)

// adjustUpdate sends the update and adjusts its body against a 4xx exactly as
// a create is adjusted: a full-replace update that the API rejects for a
// missing required field has that field added and is retried, so an update
// refusal reads as immutability only when the request shape was actually
// right. It returns the final response and the resolved body it was sent with.
func (r *runner) adjustUpdate(ctx context.Context, ent *entityState, step *plan.Step) (*httpResult, map[string]any, error) {
	body := cloneAnyMap(step.Body)
	applied := map[string]bool{}
	var last *httpResult
	var sent map[string]any
	var healed []pendingAdd
	for i := 0; i < maxAdjustIters; i++ {
		resolved, err := r.resolveBody(ctx, ent, body)
		if err != nil {
			return nil, nil, err
		}
		sent = resolved
		res, err := r.do(ctx, ent, reqSpec{
			method: step.Method, path: step.Path, pathValues: step.PathValues, body: resolved,
		})
		if err != nil {
			return nil, nil, err
		}
		last = res
		if res.ok() || !res.refused() {
			// The API took a body carrying every field the grammar added.
			for _, add := range healed {
				r.recordAdjustAdd(ent, add.field, add.condGate, add.condVal, add.excerpt)
			}
			return res, sent, nil
		}
		added, ok := r.applyAdjustment(ctx, ent, body, res, applied, true)
		if !ok {
			return res, sent, nil
		}
		healed = append(healed, added...)
	}
	return last, sent, nil
}

// runUpdateField updates exactly one attribute to a variant value and
// re-reads the object. What the API did with that one field separates
// immutable from silently-ignored from server-forced; what it did with
// every field the update body omitted feeds the entity's update-style
// finding.
func (r *runner) runUpdateField(ctx context.Context, ent *entityState, step *plan.Step) error {
	entity := ent.plan.Entity
	if ent.idUnknown {
		// No item URL to update the object at; whether the field is mutable
		// cannot be told, so the claim is inconclusive rather than skipped.
		r.record(entity, step.Attribute, observe.KindImmutable, nil, nil, observe.OutcomeInconclusive)
		return nil
	}
	before := ent.lastRead

	res, sent, err := r.adjustUpdate(ctx, ent, step)
	if err != nil {
		return err
	}

	if !res.ok() {
		ent.ev.updRefused++
		ent.ev.updProof = appendProof(ent.ev.updProof, res.excerpt)
		switch {
		case res.refused() && res.mentions(step.Attribute):
			// The API said no and named the field: refused-on-update is
			// what immutability is.
			r.record(entity, step.Attribute, observe.KindImmutable, true, nil, observe.OutcomeConfirmed, res.excerpt)
		case res.refused():
			// Refused without naming the field — the request shape could
			// be at fault (an update-only required field, say), and
			// reading that as immutability is the classic mistake.
			r.record(entity, step.Attribute, observe.KindImmutable, nil, nil, observe.OutcomeInconclusive, res.excerpt)
		default:
			r.record(entity, step.Attribute, observe.KindImmutable, nil, nil, observe.OutcomeInconclusive, res.excerpt)
		}
		return nil
	}

	ent.ev.updSucceeded++
	read, err := r.do(ctx, ent, reqSpec{
		method: "GET", path: step.Path, pathValues: step.PathValues,
	})
	if err != nil {
		return err
	}
	after := read.object()
	if !read.ok() || after == nil {
		return nil
	}

	r.compareUpdatedField(ent, step.Attribute, sent[step.Attribute], before, after, res, read)
	r.compareOmittedFields(ent, sent, before, after, read)
	ent.lastRead = after
	return nil
}

// compareUpdatedField draws the per-attribute conclusion from a 2xx
// update: applied, silently ignored, normalised, or server-forced.
func (r *runner) compareUpdatedField(ent *entityState, attr string, sentV any, before, after map[string]any, upd, read *httpResult) {
	entity := ent.plan.Entity
	gotV := after[attr]
	switch {
	case equalJSON(gotV, sentV):
		// Applied as sent: the attribute is demonstrably updatable, which
		// confirms writable if the create could not.
		r.record(entity, attr, observe.KindWritable, true, nil, observe.OutcomeConfirmed, upd.excerpt, read.excerpt)
	case before != nil && equalJSON(gotV, before[attr]):
		// Accepted with a success status and not applied — distinct from
		// immutability, and the only one of the two that produces a
		// perpetual diff.
		r.record(entity, attr, observe.KindIgnoredOnUpdate, true, nil, observe.OutcomeConfirmed, upd.excerpt, read.excerpt)
	default:
		if norm, ok := normalisedForm(sentV, gotV); ok {
			r.record(entity, attr, observe.KindNormalisation, norm, nil, observe.OutcomeConfirmed, upd.excerpt, read.excerpt)
		} else {
			r.record(entity, attr, observe.KindServerForced, true, nil, observe.OutcomeConfirmed, upd.excerpt, read.excerpt)
		}
	}
}

// compareOmittedFields watches what an accepted update did to the stored
// fields the update body did not mention: preserved is merge behaviour,
// cleared or reset is full-replace behaviour.
func (r *runner) compareOmittedFields(ent *entityState, sent, before, after map[string]any, read *httpResult) {
	if before == nil {
		return
	}
	for _, f := range sortedFieldUnion(before, nil) {
		if f == ent.ev.idField || ent.ev.volatile[f] {
			continue
		}
		if _, wasSent := sent[f]; wasSent {
			continue
		}
		gotV, present := after[f]
		switch {
		case present && equalJSON(gotV, before[f]):
			ent.ev.updOmittedKept = true
		default:
			ent.ev.updOmittedLost = true
			ent.ev.updProof = appendProof(ent.ev.updProof, read.excerpt)
		}
	}
}
