package run

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
)

// runCreateMinimal creates the object everything later reads, mutates and
// deletes, correcting the body against any 4xx the API answers — a
// spec-optional-but-really-required field named in a 400 is added and the
// create retried, yielding a requiredByAPI observation rather than a blocked
// entity. A refusal the loop cannot correct blocks the entity only when no prior
// variant has already established the object; a later variant that cannot be
// built is recorded and skipped.
func (r *runner) runCreateMinimal(ctx context.Context, entity *entityState, step *plan.Step) error {
	if entity.plannedMinimal == nil {
		entity.plannedMinimal = cloneAnyMap(step.Body)
	}
	body := cloneAnyMap(step.Body)
	rr, err := r.correctCreateBody(ctx, entity, entity.recipe, body, r.gateFieldFor(entity))
	if err != nil {
		return err
	}
	// The grammar corrects a refusal that names its field. A refusal that says
	// only that the request was bad names nothing to act on, and the document
	// that produced this body is the same document that understated it — so
	// ask the API instead, one field at a time.
	// A failed search still learned something: its last refusal answers a
	// wider body than the one that started it, and the fields it added name
	// what it asked for. Both are carried for the block reason alone — the
	// grammar's own result still decides what happens next.
	// A 5xx counts as a refusal here: an API that answers a body it cannot
	// read with a server error rather than a 400 is still answering the body.
	var searched bodyCorrection
	if rr.obj == nil && rr.res != nil && (rr.res.refused() || rr.res.status >= 500) {
		found, serr := r.addFieldsUntilAccepted(ctx, entity, entity.recipe, rr.body, rr.res)
		if serr != nil {
			return serr
		}
		if found.obj != nil {
			rr = found
		} else {
			searched = found
		}
	}
	if rr.obj != nil {
		sent, err := r.resolveBody(ctx, entity, rr.body)
		if err != nil {
			return err
		}
		// The recipe learns the body that worked. Everything downstream
		// replays it — re-creating this entity as another's parent, narrowing
		// a refused maximal, cleaning up at the end — and the body the plan
		// started from is the document's guess, which is what needed correcting
		// in the first place.
		entity.recipe.minimalBody = cloneAnyMap(rr.body)
		r.createdObjects[entity.plan.Entity] = rr.obj
		entity.createdAt = time.Now()
		entity.ev.sent = sent
		entity.ev.sentStatus = rr.res.status
		entity.ev.createProof = &rr.res.excerpt
		entity.ev.acceptedRequestBodies = append(entity.ev.acceptedRequestBodies, cloneAnyMap(rr.body))
		return nil
	}
	if _, exists := r.createdObjects[entity.plan.Entity]; exists {
		// A prior variant already made the object everything reads; this
		// variant's create could not be built, which is not a reason to
		// abandon the entity.
		return nil
	}
	if rr.freeFormConditional {
		// A free-form conditional refusal the loop could not correct within the
		// cap. Value-cycling has already captured what evidence it could and
		// recorded an inconclusive edge; blocking the whole entity would lose
		// the other variants and probes it can still run, so continue instead.
		return nil
	}
	if refused := lastRefusal(rr, searched); refused != nil {
		entity.cause = &refused.excerpt
		return blockedError{reason: minimalRefusedReason(refused.status, searched.addedFields)}
	}
	return blockedError{reason: "the minimal create produced no object"}
}

// rebased answers a step's body built on the minimal body the API accepted
// rather than the one the plan derived. A per-value or negative probe is
// the planned minimal with one change — a value pinned, a field omitted, a
// field added — and sending it on the planned minimal repeats every
// refusal the correction loop already answered, which the probe then
// records against its own field. The change is carried across: a field the
// step omits stays out, a value the step sets stays set, and everything the
// accepted body learned stays in.
func (r *runner) rebased(entity *entityState, stepBody map[string]any) map[string]any {
	accepted := entity.recipe.minimalBody
	planned := entity.plannedMinimal
	if accepted == nil || planned == nil {
		return cloneAnyMap(stepBody)
	}
	body := cloneAnyMap(accepted)
	for field := range planned {
		if _, kept := stepBody[field]; !kept {
			delete(body, field)
		}
	}
	for field, value := range stepBody {
		if plannedValue, inPlan := planned[field]; !inPlan || !reflect.DeepEqual(plannedValue, value) {
			body[field] = value
		}
	}
	return body
}

// lastRefusal answers the later-informed of the two refusals a minimal create
// can end on: the additive search's, when it ran, and the grammar's otherwise.
func lastRefusal(grammar, searched bodyCorrection) *httpResult {
	if searched.res != nil {
		return searched.res
	}
	return grammar.res
}

// minimalRefusedReason spells why an entity produced no object, naming the
// fields the search asked the API for.
//
// The status alone cannot be acted on. An operator reading a blocked entity
// needs to know whether the document's body was refused as written or whether
// a search widened it and was refused anyway, and which fields it widened it
// with.
func minimalRefusedReason(status int, tried []string) string {
	if len(tried) == 0 {
		return fmt.Sprintf("the minimal create was refused with status %d", status)
	}
	return fmt.Sprintf("the minimal create was refused with status %d, and adding %s did not correct it",
		status, strings.Join(tried, ", "))
}

// runCreateMaximal creates with every writable field populated, correcting the
// body against a 4xx: a field the API says is not valid for this variant is
// removed and the create retried, feeding the validWhen evidence. Accepted, it
// extends the per-field evidence to the optional fields; refused without a
// field the loop can act on, a bounded bisection names the refused field.
func (r *runner) runCreateMaximal(ctx context.Context, entity *entityState, step *plan.Step) error {
	body := cloneAnyMap(step.Body)
	// A composite field takes the form the minimal create got accepted —
	// widened or not — because the maximal claim is about the entity's own
	// fields, and re-learning the element's shape would spend the same
	// requests for the same answer.
	for field, value := range entity.recipe.minimalBody {
		switch value.(type) {
		case map[string]any, []any:
			if _, present := body[field]; present {
				body[field] = cloneAny(value)
			}
		}
	}
	rr, err := r.correctCreateBody(ctx, entity, entity.recipe, body, r.gateFieldFor(entity))
	if err != nil {
		return err
	}
	if rr.obj != nil {
		sent, err := r.resolveBody(ctx, entity, rr.body)
		if err != nil {
			return err
		}
		entity.ev.maximalSent = sent
		entity.ev.maximalGot = rr.res.object()
		entity.ev.maximalStatus = rr.res.status
		entity.ev.acceptedRequestBodies = append(entity.ev.acceptedRequestBodies, cloneAnyMap(rr.body))
		_, _ = r.deleteObject(ctx, entity, entity.recipe, rr.obj)
		return nil
	}
	if rr.res == nil || !rr.res.refused() {
		return nil
	}
	// Narrow first, so a single refused field is named as rejected evidence, then
	// drop what the API will not take until it takes the rest.
	if err := r.narrowRefusedField(ctx, entity, step, rr.res); err != nil {
		return err
	}
	return r.dropFieldsUntilAccepted(ctx, entity, rr.body, rr.res)
}

// narrowRefusedField narrows a refused maximal create to the optional field
// the API objected to, spending at most the plan's bisection allowance in
// extra creates. Single-refused field narrowing by construction: with several
// refused fields it still converges on one of them, which is one true finding
// rather than none.
func (r *runner) narrowRefusedField(ctx context.Context, entity *entityState, step *plan.Step, refusal *httpResult) error {
	minimal := entity.recipe.minimalBody
	var optionalFields []string
	for k := range step.Body {
		if _, inMinimal := minimal[k]; !inMinimal {
			optionalFields = append(optionalFields, k)
		}
	}
	sort.Strings(optionalFields)
	if len(optionalFields) == 0 {
		return nil
	}

	// The refusal often names the refused field outright; believe it when it
	// names exactly one suspect.
	var named []string
	for _, s := range optionalFields {
		if refusal.mentions(s) {
			named = append(named, s)
		}
	}
	if len(named) == 1 {
		r.recordRejectedValue(entity, named[0], step.Body[named[0]], refusal)
		return nil
	}

	spent := 0
	for len(optionalFields) > 1 && spent < step.FieldNarrowingAttemptLimit {
		half := optionalFields[:len(optionalFields)/2]
		body := map[string]any{}
		for k, v := range minimal {
			body[k] = v
		}
		for _, k := range half {
			body[k] = step.Body[k]
		}
		obj, res, err := r.createObject(ctx, entity, entity.recipe, body)
		if err != nil {
			return err
		}
		spent++
		switch {
		case obj != nil:
			_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
			optionalFields = optionalFields[len(optionalFields)/2:]
		case res != nil && res.refused():
			optionalFields = half
		default:
			return nil
		}
	}
	if len(optionalFields) == 1 {
		r.recordRejectedValue(entity, optionalFields[0], step.Body[optionalFields[0]], refusal)
	}
	return nil
}

// recordRejectedValue accumulates one refused create value into the
// attribute's values record.
func (r *runner) recordRejectedValue(entity *entityState, attribute string, value any, res *httpResult) {
	v := entity.ev.valuesFor(attribute)
	v.Rejected = append(v.Rejected, fmt.Sprint(value))
	entity.ev.valuesProof[attribute] = appendProof(entity.ev.valuesProof[attribute], res.excerpt)
}

// runOmitRequired sends the minimal body minus one required field. A
// refusal that names the field is the requirement observed; acceptance is
// its absence — and an object to delete.
func (r *runner) runOmitRequired(ctx context.Context, entity *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, entity, entity.recipe, r.rebased(entity, step.Body))
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	switch {
	case obj != nil:
		r.record(entity.plan.Entity, step.Attribute, observe.KindRequiredByAPI, false, nil, observe.OutcomeConfirmed, res.excerpt)
		_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
	case res.refused() && res.mentions(step.Attribute):
		r.record(entity.plan.Entity, step.Attribute, observe.KindRequiredByAPI, true, nil, observe.OutcomeConfirmed, res.excerpt)
	default:
		// Refused without naming the field: the refusal could have been
		// about anything, and claiming the requirement would be guessing.
		r.record(entity.plan.Entity, step.Attribute, observe.KindRequiredByAPI, nil, nil, observe.OutcomeInconclusive, res.excerpt)
	}
	return nil
}

// runUndocumentedEnumValue sends a value the document does not list.
// Refusal closes the documented set; acceptance opens it.
func (r *runner) runUndocumentedEnumValue(ctx context.Context, entity *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, entity, entity.recipe, r.rebased(entity, step.Body))
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
		_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
	case res.refused():
		*closed = true
	default:
		return nil
	}
	entity.ev.valuesFor(step.Attribute).Closed = closed
	entity.ev.valuesProof[step.Attribute] = appendProof(entity.ev.valuesProof[step.Attribute], res.excerpt)
	return nil
}

// runUndeclaredSpecField sends one field no schema declares and reports
// whether the API rejects unknown body fields. The finding lands on the
// summary as rejectsUnknownFields rather than as an observation because it
// is about how to read the other findings — when true, this entity's
// refusal-based findings need caution — not a finding in its own right.
func (r *runner) runUndeclaredSpecField(ctx context.Context, entity *entityState, step *plan.Step) error {
	obj, res, err := r.createObject(ctx, entity, entity.recipe, r.rebased(entity, step.Body))
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	switch {
	case obj != nil:
		r.summary.RejectsUnknownFields[entity.plan.Entity] = false
		_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
	case res.refused():
		r.summary.RejectsUnknownFields[entity.plan.Entity] = true
	}
	return nil
}

// runCreatePerEnumValue serves two checks that share a shape. With the
// condition on the attribute itself it pins one documented value and
// records acceptance or rejection; with the condition on a sibling it is
// one half of a required-when pair — created with the attribute present
// or omitted under the pinned sibling value.
func (r *runner) runCreatePerEnumValue(ctx context.Context, entity *entityState, step *plan.Step) error {
	body := r.rebased(entity, step.Body)
	held := ""
	if step.Condition != nil {
		held = step.Condition.Attribute
	}
	rr, err := r.correctCreateBody(ctx, entity, entity.recipe, body, held)
	if err != nil {
		return err
	}
	obj, res := rr.obj, rr.res
	if res == nil {
		return nil
	}
	accepted := obj != nil
	if accepted {
		entity.ev.acceptedRequestBodies = append(entity.ev.acceptedRequestBodies, cloneAnyMap(rr.body))
		_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
	} else if !res.refused() {
		return nil
	}

	cond := step.Condition
	if cond == nil {
		return nil
	}
	if cond.Attribute == step.Attribute {
		if !accepted && rr.bodyCorrected {
			// The create was partly built by the adjustment loop and then
			// stuck on a sibling requirement it could not satisfy, so the
			// pinned value itself is not what was refused. Recording it
			// rejected would be a lie; leave the value unclaimed. A value
			// refused as sent, with no adjustment, is a genuine rejection and
			// falls through to be recorded.
			return nil
		}
		v := entity.ev.valuesFor(step.Attribute)
		switch {
		case accepted:
			v.Accepted = append(v.Accepted, fmt.Sprint(cond.Equals))
		case res.mentions(step.Attribute) || res.mentions(fmt.Sprint(cond.Equals)):
			// The refusal names the field or the value: the value is what
			// was refused.
			v.Rejected = append(v.Rejected, fmt.Sprint(cond.Equals))
		default:
			// Refused without naming either — a conflict with an object
			// that already exists, a complaint about a sibling — which says
			// nothing about the value. Claiming a rejection would strip a
			// documented value from the document on no evidence.
			return nil
		}
		entity.ev.valuesProof[step.Attribute] = appendProof(entity.ev.valuesProof[step.Attribute], res.excerpt)
		return nil
	}

	key := entity.plan.Entity + "\x00" + step.Attribute + "\x00" + fmt.Sprint(cond.Attribute, "=", cond.Equals)
	pair, ok := entity.ev.requiredWhens[key]
	if !ok {
		pair = &requiredWhenPair{attribute: step.Attribute, cond: *cond}
		entity.ev.requiredWhens[key] = pair
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

// fieldAdditionAttemptLimit bounds the additive minimal search: how many extra create
// attempts one entity may spend looking for a body the API accepts.
//
// Sized from the candidate count the way bisectionAllowance is sized from the
// optional count, and capped, because a wide entity would otherwise spend the
// whole run's requests on one search. A refused create makes no object, so the
// cost is requests and wall clock, never debris.
func fieldAdditionAttemptLimit(candidates int) int {
	const cap = 24
	if candidates > cap {
		return cap
	}
	return candidates
}

// addFieldsUntilAccepted looks for a create body the API accepts by adding one field at
// a time to a body it refused.
//
// The document is only a claim about what a create needs, and an API that
// declares nothing required leaves the derivation with an empty body that
// cannot make anything. The refusal grammar handles a refusal that names its
// field; this handles the rest, which is every API whose 400 says only that
// the request was bad.
//
// Additive rather than combinatorial: each field that does not provoke a
// refusal naming it stays, so a body needing several fields is found in as
// many attempts rather than exponentially many. What it finds is a viable
// minimal body, not a proof that every field in it is individually necessary.
func (r *runner) addFieldsUntilAccepted(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, refusal *httpResult) (bodyCorrection, error) {
	candidates := r.fieldsToTry(entity, body, refusal)
	allowance := fieldAdditionAttemptLimit(len(candidates))
	last := refusal
	var tried []string

	for i := 0; i < allowance; i++ {
		field := candidates[i]
		tried = append(tried, field)
		body[field] = r.synthesiseField(entity, field)

		obj, res, err := r.createObject(ctx, entity, rec, body)
		if err != nil {
			return bodyCorrection{}, err
		}
		if obj != nil {
			// Every field the search added is part of the smallest body this
			// run could get accepted, which is what the fixture must carry.
			for _, added := range candidates[:i+1] {
				if _, kept := body[added]; kept {
					r.recordAdjustAdd(entity, added, "", "", res.excerpt)
				}
			}
			return bodyCorrection{obj: obj, res: res, body: body, bodyCorrected: true}, nil
		}
		if res == nil || (!res.refused() && res.status < 500) {
			return bodyCorrection{res: res, body: body, bodyCorrected: true, unresolved: true, addedFields: tried}, nil
		}
		// The API now objects to the field just added, so it is not one this
		// create wants; the ones before it stay.
		if res.mentions(field) {
			delete(body, field)
		}
		last = res
	}
	return bodyCorrection{res: last, body: body, bodyCorrected: true, unresolved: true, addedFields: tried}, nil
}

// fieldsToTry orders the fields the search may add, cheapest-signal
// first, so the common case ends in a handful of attempts and a re-run repeats
// the same order.
func (r *runner) fieldsToTry(entity *entityState, body map[string]any, refusal *httpResult) []string {
	hints := r.hints[entity.plan.Entity]
	type candidate struct {
		field string
		rank  int
	}
	var out []candidate
	for field, h := range hints {
		if _, present := body[field]; present {
			continue
		}
		rank := 3
		switch {
		case refusal != nil && refusal.mentions(field):
			// The API has already said this field's name out loud.
			rank = 0
		case len(h.Enum) > 0 || h.Example != nil || h.Default != nil:
			// The document states a value the API is known to accept.
			rank = 1
		case h.Type != "" && h.Type != "object" && h.Type != "array":
			rank = 2
		}
		out = append(out, candidate{field: field, rank: rank})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].field < out[j].field
	})
	fields := make([]string, 0, len(out))
	for _, c := range out {
		fields = append(fields, c.field)
	}
	return fields
}

// dropFieldsUntilAccepted drops the fields the API objects to until it accepts the
// create, and records the body it accepted.
//
// The counterpart to searchMinimal: that one adds until a create works, this
// one removes. What survives is the fullest create this run could get taken,
// which is what a generated maximal configuration has to be — every field in
// it is one the API demonstrably tolerates alongside the others.
func (r *runner) dropFieldsUntilAccepted(ctx context.Context, entity *entityState, body map[string]any, refusal *httpResult) error {
	minimal := entity.recipe.minimalBody
	allowance := fieldAdditionAttemptLimit(len(body))
	last := refusal

	for i := 0; i < allowance; i++ {
		refusedField := r.refusedOptionalField(body, minimal, last)
		if refusedField == "" {
			return nil
		}
		// The evidence for a refusal is bisectMaximal's to record; this only
		// shapes the body, so a field dropped here is not claimed twice.
		delete(body, refusedField)

		obj, res, err := r.createObject(ctx, entity, entity.recipe, body)
		if err != nil {
			return err
		}
		if obj != nil {
			sent, err := r.resolveBody(ctx, entity, body)
			if err != nil {
				return err
			}
			entity.ev.maximalSent = sent
			entity.ev.maximalGot = res.object()
			entity.ev.maximalStatus = res.status
			entity.ev.acceptedRequestBodies = append(entity.ev.acceptedRequestBodies, cloneAnyMap(body))
			_, _ = r.deleteObject(ctx, entity, entity.recipe, obj)
			return nil
		}
		if res == nil || !res.refused() {
			return nil
		}
		last = res
	}
	return nil
}

// refusedOptionalField names the optional field to drop next: the one the refusal
// mentions, else the last in document order, which is deterministic and so
// repeats the same reduction on a re-run.
//
// A field the minimal create needs is never a candidate — removing it would
// trade a refused maximal for a refused minimal.
func (r *runner) refusedOptionalField(body, minimal map[string]any, refusal *httpResult) string {
	var optional []string
	for k := range body {
		if _, needed := minimal[k]; !needed {
			optional = append(optional, k)
		}
	}
	if len(optional) == 0 {
		return ""
	}
	sort.Strings(optional)
	if refusal != nil {
		for _, k := range optional {
			if refusal.mentions(k) {
				return k
			}
		}
	}
	return optional[len(optional)-1]
}
