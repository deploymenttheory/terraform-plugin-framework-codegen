package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
)

// entityState is one entity's execution: its recipe, its spend, its
// accumulated evidence, and how far it got.
type entityState struct {
	plan   *plan.EntityPlan
	recipe *entityRecipe
	ev     *evidence

	requests int
	status   string
	reason   string
	// cause is the excerpt behind a blocked status, attached to the
	// blocked claims as proof of why they never ran.
	cause *observe.Excerpt
	// createdAt is when the minimal create succeeded — the readWithRetry
	// clock starts here, not at the read.
	createdAt time.Time
	// lastRead is the most recent full read of the minimal object, the
	// baseline updates compare against.
	lastRead map[string]any
	// preflighted marks the foreign-object pre-flight as done.
	preflighted bool
	// idUnknown records that a create succeeded but no id could be learned
	// from its response. The object still exists — the prefix pass cleans it —
	// but it cannot be addressed by id, so the id-derived steps (read, update,
	// delete-with-confirmation) record inconclusive instead of driving a
	// request at an empty id.
	idUnknown bool
	// declared is the plan's DeclaredProperties as a set, built on first
	// use by observeUndeclaredFields.
	declared map[string]bool
}

// runEntity executes one entity's steps in order, stopping at the first
// blocked precondition or exhausted budget and recording the remaining
// steps' claims under that outcome.
func (r *runner) runEntity(ctx context.Context, ep *plan.EntityPlan) {
	ent := &entityState{plan: ep, ev: newEvidence(), status: StatusAudited}
	ent.recipe = recipeOf(ep)
	r.recipes[ep.Entity] = ent.recipe

	for i := range ep.Steps {
		step := &ep.Steps[i]
		if err := r.runStep(ctx, ent, step); err != nil {
			r.halt(ent, err)
		}
		if ent.status != StatusAudited {
			r.emitHaltedClaims(ent, ep.Steps[i:])
			break
		}
	}

	r.finalizeEvidence(ent)
	r.evidence[ep.Entity] = &infer.Evidence{
		Entity:                ep.Entity,
		AcceptedRequestBodies: ent.ev.acceptedRequestBodies,
		ListBodies:            ent.ev.listBodies,
		CombinedRefusals:      ent.ev.combinedRefusals,
		ConditionalValues:     ent.ev.conditionalValues,
		IdentifierProperty:    ent.ev.idField,
	}
	r.summary.RequestBodies = append(r.summary.RequestBodies, recordedRequestBodies(ep.Entity, ent))
	r.summary.Entities = append(r.summary.Entities, EntityResult{
		Entity: ep.Entity, Status: ent.status, Reason: ent.reason, Refusal: ent.cause,
	})
	r.log.Info().Str("entity", ep.Entity).Str("status", ent.status).Str("reason", ent.reason).Int("requests", ent.requests).Msg("entity finished")
}

// recordedRequestBodies is what this entity's accepted creates looked like,
// for the generated acceptance tests to be built from.
//
// A create the API refused is not here: the point of the record is that these
// are requests it took, so a configuration replaying one is a configuration
// known to apply.
func recordedRequestBodies(entity string, ent *entityState) observe.RequestBodies {
	out := observe.RequestBodies{Entity: entity}
	if ent.ev.sent != nil {
		out.Minimal = &observe.AcceptedRequestBody{
			Status: ent.ev.sentStatus, Request: ent.ev.sent, Response: ent.ev.got,
		}
	}
	if ent.ev.maximalSent != nil {
		out.Maximal = &observe.AcceptedRequestBody{
			Status: ent.ev.maximalStatus, Request: ent.ev.maximalSent, Response: ent.ev.maximalGot,
		}
	}
	return out
}

// halt classifies a step failure onto the entity.
func (r *runner) halt(ent *entityState, err error) {
	var blocked blockedError
	var budget budgetError
	switch {
	case errors.As(err, &budget):
		ent.status = StatusTimeoutExhausted
		ent.reason = budget.reason
	case errors.As(err, &blocked):
		ent.status = StatusBlocked
		ent.reason = blocked.reason
	default:
		// A network failure or a context death: the steps cannot
		// discriminate anything further, which is a blocked entity.
		ent.status = StatusBlocked
		ent.reason = err.Error()
	}
}

// runStep dispatches one step. It returns an error only when the entity
// cannot continue; a response that merely disappoints becomes an
// observation instead.
func (r *runner) runStep(ctx context.Context, ent *entityState, step *plan.Step) error {
	if mutatingMethod(step.Method) && !ent.preflighted {
		if err := r.preflight(ctx, ent, step); err != nil {
			return err
		}
	}

	switch step.Kind {
	case plan.StepCreateMinimal:
		return r.runCreateMinimal(ctx, ent, step)
	case plan.StepReadWithRetry:
		return r.runReadWithRetry(ctx, ent, step)
	case plan.StepReadConsecutive:
		return r.runReadConsecutive(ctx, ent, step)
	case plan.StepUpdateField:
		return r.runUpdateField(ctx, ent, step)
	case plan.StepDeleteWithConfirmation:
		return r.runDeleteWithConfirmation(ctx, ent, step)
	case plan.StepCreateMaximal:
		return r.runCreateMaximal(ctx, ent, step)
	case plan.StepOmitRequired:
		return r.runOmitRequired(ctx, ent, step)
	case plan.StepUndocumentedEnumValue:
		return r.runUndocumentedEnumValue(ctx, ent, step)
	case plan.StepUndeclaredSpecField:
		return r.runUndeclaredSpecField(ctx, ent, step)
	case plan.StepCreatePerEnumValue:
		return r.runCreatePerEnumValue(ctx, ent, step)
	case plan.StepRead:
		return r.runLookupRead(ctx, ent, step)
	case plan.StepCleanupDelete:
		return r.runCleanupDelete(ctx, ent, step)
	default:
		return blockedError{reason: fmt.Sprintf("unknown step kind %q", step.Kind)}
	}
}

// preflight is the foreign-object check: a read of the entity's
// collection before its first mutating request. A collection already
// holding more foreign objects than the run may itself create does not
// look like a sandbox, and mutating it needs the explicit ForceAPIAudit.
func (r *runner) preflight(ctx context.Context, ent *entityState, step *plan.Step) error {
	ent.preflighted = true
	if ent.recipe.collectionPath == "" {
		return nil
	}
	res, err := r.do(ctx, ent, reqSpec{
		method: "GET", path: ent.recipe.collectionPath, pathValues: ent.recipe.collectionValues,
	})
	if err != nil {
		return err
	}
	if !res.ok() {
		return blockedError{reason: fmt.Sprintf("the pre-flight read of %s answered %d, so the tenant's size is unknown", ent.recipe.collectionPath, res.status)}
	}
	// The collection response is the evidence the list-response-shape finding
	// is read from — its structure only, never a value from it.
	if len(res.body) > 0 && len(ent.ev.listBodies) == 0 {
		ent.ev.listBodies = append(ent.ev.listBodies, append([]byte(nil), res.body...))
	}
	foreign := 0
	for _, item := range items(res.body) {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if nameOf(obj, r.opts.NamePrefix) == "" {
			foreign++
		}
	}
	if foreign > r.budget.Objects && !r.opts.ForceAPIAudit {
		return blockedError{reason: fmt.Sprintf(
			"%s holds %d objects that do not carry the %q prefix, over the %d-object ceiling; a tenant this full does not look like a sandbox, and mutating it needs --force-api-audit",
			ent.recipe.collectionPath, foreign, r.opts.NamePrefix, r.budget.Objects)}
	}
	_ = step
	return nil
}

// recipeOf distils an entity plan into what re-creation and deletion need.
func recipeOf(ep *plan.EntityPlan) *entityRecipe {
	rec := &entityRecipe{entity: ep.Entity}
	for i := range ep.Steps {
		s := &ep.Steps[i]
		switch s.Kind {
		case plan.StepCreateMinimal:
			// First wins: a strategy program carries one createMinimal per
			// variant, and the baseline (first) is the simplest body to
			// re-create a parent or seed the cleanup recipe from.
			if rec.collectionPath == "" {
				rec.collectionPath = s.Path
				rec.collectionValues = s.PathValues
				rec.createMethod = s.Method
				rec.minimalBody = s.Body
			}
		case plan.StepReadWithRetry, plan.StepRead:
			if rec.itemPath == "" {
				rec.itemPath = s.Path
				rec.itemValues = s.PathValues
			}
		case plan.StepDeleteWithConfirmation, plan.StepCleanupDelete:
			rec.deleteMethod = s.Method
			if rec.itemPath == "" {
				rec.itemPath = s.Path
				rec.itemValues = s.PathValues
			}
		}
	}
	if rec.deleteMethod == "" {
		rec.deleteMethod = "DELETE"
	}
	return rec
}

// selfParam is the trailing path parameter an item path addresses the
// object itself by.
func selfParam(itemPath string) string {
	if i := strings.LastIndex(itemPath, "{"); i >= 0 && strings.HasSuffix(itemPath, "}") {
		return itemPath[i+1 : len(itemPath)-1]
	}
	return ""
}

// itemValuesFor is the item path values with the object's own parameter
// pinned to a concrete id.
func itemValuesFor(rec *entityRecipe, id string) map[string]string {
	out := make(map[string]string, len(rec.itemValues)+1)
	for k, v := range rec.itemValues {
		out[k] = v
	}
	if p := selfParam(rec.itemPath); p != "" {
		out[p] = id
	}
	return out
}

// resolveCreated answers $created:<entity>: the live minimal object's id,
// re-creating it from the entity's recipe when a later step already
// deleted it — a parent entity ends at zero live objects before its
// children run, so re-creation is the only way a child can exist.
func (r *runner) resolveCreated(ctx context.Context, ent *entityState, entity string) (string, error) {
	if obj, ok := r.registry[entity]; ok {
		return obj.id, nil
	}
	rec, ok := r.recipes[entity]
	if !ok || rec.minimalBody == nil {
		return "", blockedError{reason: fmt.Sprintf("no created %s object exists and its entity has no create recipe", entity)}
	}
	// Healed like any other create: a parent the document understates is
	// refused the same way its own create was, and without the loop every
	// child of it blocks on a refusal the loop can read.
	rr, err := r.adjustCreateRecording(ctx, ent, rec, cloneAnyMap(rec.minimalBody), "", false)
	if err != nil {
		return "", err
	}
	obj := rr.obj
	if obj == nil {
		return "", blockedError{reason: fmt.Sprintf("re-creating the %s parent object was refused", entity)}
	}
	r.registry[entity] = obj
	return obj.id, nil
}

// createObject performs one guarded, ledgered create against an entity's
// collection. It returns the created object (nil when the API refused)
// and the response. The ledger intent is written and fsynced before the
// request is sent — the whole point of having a ledger.
func (r *runner) createObject(ctx context.Context, ent *entityState, rec *entityRecipe, body map[string]any) (*createdObject, *httpResult, error) {
	if !r.inCleanup && len(r.ledger.unresolved()) >= r.budget.Objects {
		return nil, nil, budgetError{reason: fmt.Sprintf("the run's live-object budget (%d) is exhausted", r.budget.Objects)}
	}

	resolved, err := r.resolveBody(ctx, ent, body)
	if err != nil {
		return nil, nil, err
	}
	itemPath, err := r.partialItemPath(ctx, ent, rec)
	if err != nil {
		return nil, nil, err
	}

	seq, err := r.ledger.intent(rec.entity, nameOf(resolved, r.opts.NamePrefix), itemPath)
	if err != nil {
		return nil, nil, blockedError{reason: "the create could not be recorded in the ledger first: " + err.Error()}
	}

	res, err := r.do(ctx, ent, reqSpec{
		method: rec.createMethod, path: rec.collectionPath,
		pathValues: rec.collectionValues, body: resolved,
	})
	if err != nil {
		// The intent stays outstanding: a request whose outcome was never
		// seen may have created an object, and cleanup must look for it.
		return nil, nil, err
	}

	switch {
	case res.ok():
		id := learnID(rec.entity, res)
		r.ledger.resolve(seq, activityCreated, id, res.status)
		r.summary.ObjectsCreated++
		r.observeOmittedSamples(ent, resolved, res.object(), id)
		if id == "" {
			// The object exists but its id could not be learned from any known
			// response shape. The prefix pass still deletes it, but nothing can
			// address it by id, so its read/update/delete probing is impaired —
			// recorded, never silent.
			r.log.Warn().Str("entity", rec.entity).Int("status", res.status).Msg("a created object's id was never learned; it is addressable only by the cleanup prefix pass and its id-derived observations are inconclusive")
			if ent != nil {
				ent.idUnknown = true
			}
		}
		return &createdObject{entity: rec.entity, id: id, seq: seq}, res, nil
	case res.refused():
		r.ledger.resolve(seq, activityRejected, "", res.status)
		return nil, res, nil
	default:
		// A 5xx discriminates nothing and may have created the object
		// anyway; the intent stays outstanding for cleanup.
		return nil, res, nil
	}
}

// partialItemPath resolves an entity's item path down to the object's own
// parameter: parents substituted, self still templated. This is what the
// ledger stores, so a crashed run's objects remain addressable.
func (r *runner) partialItemPath(ctx context.Context, ent *entityState, rec *entityRecipe) (string, error) {
	if rec.itemPath == "" {
		return "", nil
	}
	self := selfParam(rec.itemPath)
	out := rec.itemPath
	for param, v := range rec.itemValues {
		if param == self {
			continue
		}
		resolved, err := r.resolveValue(ctx, ent, v)
		if err != nil {
			return "", err
		}
		out = strings.ReplaceAll(out, "{"+param+"}", resolved)
	}
	return out, nil
}

// deleteObject deletes one created object and resolves its ledger intent
// when the API confirms it gone.
func (r *runner) deleteObject(ctx context.Context, ent *entityState, rec *entityRecipe, obj *createdObject) (*httpResult, error) {
	if obj.id == "" {
		return nil, nil
	}
	res, err := r.do(ctx, ent, reqSpec{
		method: rec.deleteMethod, path: rec.itemPath, pathValues: itemValuesFor(rec, obj.id),
	})
	if err != nil {
		return nil, err
	}
	if res.ok() || res.status == 404 {
		r.ledger.resolve(obj.seq, activityDeleted, obj.id, res.status)
		if live, ok := r.registry[obj.entity]; ok && live.seq == obj.seq {
			delete(r.registry, obj.entity)
		}
	}
	return res, nil
}

// claim is one observation a halted step would have earned.
type claim struct {
	attr string
	kind observe.Kind
	cond *observe.Condition
}

// stepClaims maps a pending step to the primary claims it would have
// produced — what a blocked or exhausted entity records instead of
// silence.
func stepClaims(s *plan.Step) []claim {
	switch s.Kind {
	case plan.StepReadWithRetry:
		return []claim{{kind: observe.KindReadAfterWrite}}
	case plan.StepUpdateField:
		return []claim{{attr: s.Attribute, kind: observe.KindImmutable}}
	case plan.StepDeleteWithConfirmation:
		return []claim{{kind: observe.KindDeleteNotFoundOK}}
	case plan.StepOmitRequired:
		return []claim{{attr: s.Attribute, kind: observe.KindRequiredByAPI}}
	case plan.StepUndocumentedEnumValue:
		return []claim{{attr: s.Attribute, kind: observe.KindValues}}
	case plan.StepCreatePerEnumValue:
		if s.Condition != nil && s.Condition.Attribute != s.Attribute {
			return []claim{{attr: s.Attribute, kind: observe.KindRequiredWhen, cond: s.Condition}}
		}
		return []claim{{attr: s.Attribute, kind: observe.KindValues}}
	default:
		return nil
	}
}

// emitHaltedClaims records every remaining step's claims under the
// entity's halt outcome, with the halting excerpt as proof of why.
func (r *runner) emitHaltedClaims(ent *entityState, remaining []plan.Step) {
	outcome := observe.OutcomeBlocked
	if ent.status == StatusTimeoutExhausted {
		outcome = observe.OutcomeTimeoutExhausted
	}
	var proof []observe.Excerpt
	if ent.cause != nil {
		proof = []observe.Excerpt{*ent.cause}
	}
	seen := map[string]bool{}
	sawUpdate := false
	for i := range remaining {
		s := &remaining[i]
		if s.Kind == plan.StepUpdateField {
			sawUpdate = true
		}
		for _, c := range stepClaims(s) {
			id := observe.ComputeID(ent.plan.Entity, c.attr, c.kind, c.cond)
			if seen[id] {
				continue
			}
			seen[id] = true
			r.record(ent.plan.Entity, c.attr, c.kind, nil, c.cond, outcome, proof...)
		}
	}
	if sawUpdate {
		r.record(ent.plan.Entity, "", observe.KindUpdateStyle, nil, nil, outcome, proof...)
	}
}
