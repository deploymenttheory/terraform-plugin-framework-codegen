package run

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
)

// runCreateMinimal creates the object everything later reads, mutates and
// deletes, refining the body against any 4xx the API answers — a
// spec-optional-but-really-required field named in a 400 is added and the
// create retried, yielding a requiredByAPI observation rather than a blocked
// entity. A refusal the loop cannot heal blocks the entity only when no prior
// variant has already established the object; a later variant that cannot be
// built is recorded and skipped.
func (r *runner) runCreateMinimal(ctx context.Context, ent *entityState, step *plan.Step) error {
	body := cloneAnyMap(step.Body)
	rr, err := r.adjustCreate(ctx, ent, ent.recipe, body)
	if err != nil {
		return err
	}
	if rr.obj != nil {
		sent, err := r.resolveBody(ctx, ent, rr.body)
		if err != nil {
			return err
		}
		r.registry[ent.plan.Entity] = rr.obj
		ent.createdAt = time.Now()
		ent.ev.sent = sent
		ent.ev.createProof = &rr.res.excerpt
		ent.ev.acceptedBodies = append(ent.ev.acceptedBodies, cloneAnyMap(rr.body))
		return nil
	}
	if _, exists := r.registry[ent.plan.Entity]; exists {
		// A prior variant already made the object everything reads; this
		// variant's create could not be built, which is not a reason to
		// abandon the entity.
		return nil
	}
	if rr.res != nil {
		ent.cause = &rr.res.excerpt
		return blockedError{reason: fmt.Sprintf("the minimal create was refused with status %d", rr.res.status)}
	}
	return blockedError{reason: "the minimal create produced no object"}
}

// runCreateMaximal creates with every writable field populated, refining the
// body against a 4xx: a field the API says is not valid for this variant is
// removed and the create retried, feeding the validWhen evidence. Accepted, it
// extends the per-field evidence to the optional fields; refused without a
// field the loop can act on, a bounded bisection names the culprit.
func (r *runner) runCreateMaximal(ctx context.Context, ent *entityState, step *plan.Step) error {
	body := cloneAnyMap(step.Body)
	rr, err := r.adjustCreate(ctx, ent, ent.recipe, body)
	if err != nil {
		return err
	}
	if rr.obj != nil {
		sent, err := r.resolveBody(ctx, ent, rr.body)
		if err != nil {
			return err
		}
		ent.ev.maximalSent = sent
		ent.ev.maximalGot = rr.res.object()
		ent.ev.acceptedBodies = append(ent.ev.acceptedBodies, cloneAnyMap(rr.body))
		_, _ = r.deleteObject(ctx, ent, ent.recipe, rr.obj)
		return nil
	}
	if rr.res == nil || !rr.res.refused() {
		return nil
	}
	return r.bisectMaximal(ctx, ent, step, rr.res)
}

// bisectMaximal narrows a refused maximal create to the optional field
// the API objected to, spending at most the plan's bisection allowance in
// extra creates. Single-culprit narrowing by construction: with several
// culprits it still converges on one of them, which is one true finding
// rather than none.
func (r *runner) bisectMaximal(ctx context.Context, ent *entityState, step *plan.Step, refusal *httpResult) error {
	minimal := ent.recipe.minimalBody
	var suspects []string
	for k := range step.Body {
		if _, inMinimal := minimal[k]; !inMinimal {
			suspects = append(suspects, k)
		}
	}
	sort.Strings(suspects)
	if len(suspects) == 0 {
		return nil
	}

	// The refusal often names the culprit outright; believe it when it
	// names exactly one suspect.
	var named []string
	for _, s := range suspects {
		if refusal.mentions(s) {
			named = append(named, s)
		}
	}
	if len(named) == 1 {
		r.recordRejectedValue(ent, named[0], step.Body[named[0]], refusal)
		return nil
	}

	spent := 0
	for len(suspects) > 1 && spent < step.BisectionAllowance {
		half := suspects[:len(suspects)/2]
		body := map[string]any{}
		for k, v := range minimal {
			body[k] = v
		}
		for _, k := range half {
			body[k] = step.Body[k]
		}
		obj, res, err := r.createObject(ctx, ent, ent.recipe, body)
		if err != nil {
			return err
		}
		spent++
		switch {
		case obj != nil:
			_, _ = r.deleteObject(ctx, ent, ent.recipe, obj)
			suspects = suspects[len(suspects)/2:]
		case res != nil && res.refused():
			suspects = half
		default:
			return nil
		}
	}
	if len(suspects) == 1 {
		r.recordRejectedValue(ent, suspects[0], step.Body[suspects[0]], refusal)
	}
	return nil
}

// recordRejectedValue accumulates one refused create value into the
// attribute's values record.
func (r *runner) recordRejectedValue(ent *entityState, attr string, value any, res *httpResult) {
	v := ent.ev.valuesFor(attr)
	v.Rejected = append(v.Rejected, fmt.Sprint(value))
	ent.ev.valuesProof[attr] = appendProof(ent.ev.valuesProof[attr], res.excerpt)
}

// runOmitRequired sends the minimal body minus one required field. A
// refusal that names the field is the requirement observed; acceptance is
// its absence — and an object to delete.
func (r *runner) runOmitRequired(ctx context.Context, ent *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, ent, ent.recipe, step.Body)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	switch {
	case obj != nil:
		r.record(ent.plan.Entity, step.Attribute, observe.KindRequiredByAPI, false, nil, observe.OutcomeConfirmed, res.excerpt)
		_, _ = r.deleteObject(ctx, ent, ent.recipe, obj)
	case res.refused() && res.mentions(step.Attribute):
		r.record(ent.plan.Entity, step.Attribute, observe.KindRequiredByAPI, true, nil, observe.OutcomeConfirmed, res.excerpt)
	default:
		// Refused without naming the field: the refusal could have been
		// about anything, and claiming the requirement would be guessing.
		r.record(ent.plan.Entity, step.Attribute, observe.KindRequiredByAPI, nil, nil, observe.OutcomeInconclusive, res.excerpt)
	}
	return nil
}

// runUndocumentedEnumValue sends a value the document does not list.
// Refusal closes the documented set; acceptance opens it.
func (r *runner) runUndocumentedEnumValue(ctx context.Context, ent *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, ent, ent.recipe, step.Body)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	closed := new(bool)
	switch {
	case obj != nil:
		*closed = false
		_, _ = r.deleteObject(ctx, ent, ent.recipe, obj)
	case res.refused():
		*closed = true
	default:
		return nil
	}
	ent.ev.valuesFor(step.Attribute).Closed = closed
	ent.ev.valuesProof[step.Attribute] = appendProof(ent.ev.valuesProof[step.Attribute], res.excerpt)
	return nil
}

// runUndeclaredSpecField sends one field no schema declares and reports
// whether the API rejects unknown body fields. The finding lands on the
// summary as rejectsUnknownFields rather than as an observation because it
// is about how to read the other findings — when true, this entity's
// refusal-based findings need caution — not a finding in its own right.
func (r *runner) runUndeclaredSpecField(ctx context.Context, ent *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, ent, ent.recipe, step.Body)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	switch {
	case obj != nil:
		r.summary.RejectsUnknownFields[ent.plan.Entity] = false
		_, _ = r.deleteObject(ctx, ent, ent.recipe, obj)
	case res.refused():
		r.summary.RejectsUnknownFields[ent.plan.Entity] = true
	}
	return nil
}

// runCreatePerEnumValue serves two checks that share a shape. With the
// condition on the attribute itself it pins one documented value and
// records acceptance or rejection; with the condition on a sibling it is
// one half of a required-when pair — created with the attribute present
// or omitted under the pinned sibling value.
func (r *runner) runCreatePerEnumValue(ctx context.Context, ent *entityState, step *plan.Step) error {
	body := cloneAnyMap(step.Body)
	rr, err := r.adjustCreate(ctx, ent, ent.recipe, body)
	if err != nil {
		return err
	}
	obj, res := rr.obj, rr.res
	if res == nil {
		return nil
	}
	accepted := obj != nil
	if accepted {
		ent.ev.acceptedBodies = append(ent.ev.acceptedBodies, cloneAnyMap(rr.body))
		_, _ = r.deleteObject(ctx, ent, ent.recipe, obj)
	} else if !res.refused() {
		return nil
	}

	cond := step.Condition
	if cond == nil {
		return nil
	}
	if cond.Attribute == step.Attribute {
		if !accepted && rr.adjusted {
			// The create was partly built by the adjustment loop and then
			// stuck on a sibling requirement it could not satisfy, so the
			// pinned value itself is not what was refused. Recording it
			// rejected would be a lie; leave the value unclaimed. A value
			// refused as sent, with no adjustment, is a genuine rejection and
			// falls through to be recorded.
			return nil
		}
		v := ent.ev.valuesFor(step.Attribute)
		if accepted {
			v.Accepted = append(v.Accepted, fmt.Sprint(cond.Equals))
		} else {
			v.Rejected = append(v.Rejected, fmt.Sprint(cond.Equals))
		}
		ent.ev.valuesProof[step.Attribute] = appendProof(ent.ev.valuesProof[step.Attribute], res.excerpt)
		return nil
	}

	key := ent.plan.Entity + "\x00" + step.Attribute + "\x00" + fmt.Sprint(cond.Attribute, "=", cond.Equals)
	pair, ok := ent.ev.requiredWhens[key]
	if !ok {
		pair = &requiredWhenPair{attr: step.Attribute, cond: *cond}
		ent.ev.requiredWhens[key] = pair
	}
	if _, withAttr := step.Body[step.Attribute]; withAttr {
		pair.with = &accepted
	} else {
		pair.without = &accepted
	}
	pair.proof = appendProof(pair.proof, res.excerpt)
	return nil
}

// appendProof keeps at most two excerpts per accumulated finding: enough
// for a reviewer, bounded for a committed file.
func appendProof(proof []observe.Excerpt, e observe.Excerpt) []observe.Excerpt {
	if len(proof) >= 2 {
		return proof
	}
	return append(proof, e)
}
