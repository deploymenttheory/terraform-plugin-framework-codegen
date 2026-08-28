package run

// The adaptive adjustment loop is what makes the executor learn from a refusal
// instead of merely recording it. A create or update the API rejects with a
// 4xx often names, in a machine-legible sentence, exactly what is wrong: a
// field that must be present, a field that is not valid for this variant, a
// field that requires a sibling, or a field that must reference a live object
// in another collection. The loop parses that sentence, adjusts the body
// accordingly, and retries — bounded hard, so a server that keeps refusing can
// never spin the loop forever.
//
// The classification grammar is the test API server's stable 400 vocabulary
// (documented on its validators), parsed defensively so a real API that
// phrases the same fact differently still lands on the right action:
//
//	field X is required[ when G=V]              -> ADD X   (requiredByAPI / requiredWhen)
//	field X is not valid[ when G=V]             -> REMOVE X (validWhen)
//	field X requires field Y to be set          -> ADD Y   (requiresField)
//	field X must reference an existing <coll>   -> BORROW a real id for X
//	anything else                               -> STOP    (inconclusive)
//
// A real API's value-conditional refusals do not always fit that grammar:
// A real API may answer a create with a free-form message such as "field X is not
// supported for the provided object type", which names a field (type) in prose
// no clause matches. When classification stops, the loop falls back on
// generalized field extraction (namedKnownFields) — a refusal that names any
// field the entity declares is a conditional constraint, not an unintelligible
// refusal — and value-cycling (retryAcrossValues), which tries alternate enum
// values for the implicated sibling to find a body the API accepts. See cycle.go.
//
// Every change is recorded as a requestAdjustment so the triangulating
// inference can read what the live API forced, and a required-field add also
// emits the observation the existing vocabulary already carries.

import (
	"bytes"
	"context"
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// maxBodyCorrectionAttempts bounds one create-or-update attempt's adjustments. Combined
// with the no-progress guard and the entity request budget, it guarantees the
// loop terminates. Sized for an API that names one missing field per refusal
// and requires several the document does not declare: each such field is one
// edit, and a body that needs twelve of them is one the document has all but
// left out.
const maxBodyCorrectionAttempts = 12

// maxConditionalProbeAttempts bounds the adjustments of a create pinned to
// one value of a gate field. The body is already the accepted minimal with
// the value swapped in, so what the grammar can still add is a field that
// value alone requires; a refusal that outlasts these attempts is about the
// value's other siblings, which no adjustment supplies, and the full bound
// would spend the same requests on every value of every gate.
const maxConditionalProbeAttempts = 4

// bodyCorrection is the outcome of a bounded adjustment loop.
type bodyCorrection struct {
	// obj is the created object when a create attempt finally succeeded.
	obj *createdObject
	// res is the last response seen — the success or the refusal that ended
	// the loop.
	res *httpResult
	// body is the final body, with every adjustment applied.
	body map[string]any
	// bodyCorrected reports that the loop changed the body at least once. A create
	// that failed with bodyCorrected false was refused as sent, uncorrectably; one
	// that failed with bodyCorrected true was partly built and then stuck, which is
	// a weaker signal about the body it started from.
	bodyCorrected bool
	// addedFields names the fields the additive search added, in the order it added
	// them. Set only where the search ran and failed: it is what the block
	// reason needs to say more than that a status came back.
	addedFields []string
	// unresolved reports that the loop ended without success: the refusal could
	// not be classified into an action, an action made no progress, a borrow
	// found nothing, or the iteration bound was hit.
	unresolved bool
	// freeFormConditional reports that the final refusal named a field the entity
	// declares even though the grammar could not classify it — a free-form
	// freeFormConditional constraint. A caller that would otherwise block treats this
	// as captured edge-evidence and continues instead: value-cycling has
	// already recorded what it could, and blocking would lose the variants and
	// probes the entity can still exercise.
	freeFormConditional bool
}

// refusal action kinds.
type adjustmentKind int

const (
	adjustmentNone adjustmentKind = iota
	adjustmentAdd
	adjustmentRemove
	adjustmentRequires
	adjustmentBorrow
	// adjustmentRevalue keeps the field and replaces its value with one of
	// the shape the refusal asked for.
	adjustmentRevalue
)

// revalueRule names how a refused value is replaced.
type revalueRule int

const (
	// futureValue replaces a timestamp the API wants ahead of now with one
	// a day ahead, in the spelling the body already used.
	futureValue revalueRule = iota + 100
	// positiveValue replaces a number that is not positive with the
	// smallest positive one the document admits.
	positiveValue revalueRule = iota + 1
)

// parsedRefusal is one parsed instruction from a refusal.
type parsedRefusal struct {
	kind       adjustmentKind
	field      string
	collection string
	trigger    string
	condGate   string
	condVal    string
	// candidates lists every field an add may satisfy the refusal with,
	// first choice first, where the sentence offered more than one.
	candidates []string
	// mutuallyExclusiveWith names the field the refusal said this one may
	// not be sent beside, on a removal read out of an exactly-one sentence.
	mutuallyExclusiveWith string
	// revalue names the replacement rule on a revalue.
	revalue revalueRule
	// container is the word the refusal put before the absent field, where
	// it put one: the object whose member is missing when the field is not
	// a top-level one the entity declares.
	container string
	// mustBeDeclared marks an add read out of a looser sentence: the word
	// before an absence complaint, with no "field" marker to vouch for it.
	// Such a field is added only where the entity declares it; the
	// sentence alone does not prove the word is a wire property.
	mustBeDeclared bool
}

// addField settles which field an add puts into the body: the first candidate
// the entity declares and the body lacks, spelt as the entity declares it. A
// refusal that names a field with a marker vouching for it is taken at its
// word even where nothing declared matches, because an API routinely
// requires a property the document omits; one read out of a looser sentence
// is not, and falls back on any declared field the sentence names before
// giving up.
func (r *runner) addField(entity *entityState, body map[string]any, act parsedRefusal, message string) string {
	candidates := act.candidates
	if len(candidates) == 0 {
		candidates = []string{act.field}
	}
	known := r.hints[entity.plan.Entity]
	if known == nil {
		for _, c := range candidates {
			if !strings.Contains(c, ".") && !strings.Contains(c, " ") && !present(body, c) {
				return c
			}
		}
		return ""
	}
	for _, c := range candidates {
		if declared := declaredSpelling(c, known); declared != "" && !present(body, declared) {
			return declared
		}
	}
	if !act.mustBeDeclared {
		if c := candidates[0]; !strings.Contains(c, ".") && !strings.Contains(c, " ") && !present(body, c) {
			return c
		}
		return ""
	}
	for _, named := range declaredFieldsNamedIn(message, known) {
		if !present(body, named) {
			return named
		}
	}
	return ""
}

// leafField is the last segment of a dotted refusal path, which is the name
// the request body spells: an API that validates its own nested request object
// reports "endpoint.url" for a property the document declares as "url".
// correctCreateBody wraps a guarded, ledgered create in the bounded adjustment
// loop. It returns as soon as a create succeeds, and otherwise applies one
// adjustment per iteration until the refusal stops being actionable, the
// bound is hit, or the entity budget is exhausted (surfaced as the error).
// held names the field this create pins and value-cycling must not vary — the
// variant's discriminator for a minimal or maximal create, the value under test
// for a per-enum-value create — so cycling searches the other enum fields for a
// body the API accepts without abandoning what the step is exercising.
func (r *runner) correctCreateBody(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, held string) (bodyCorrection, error) {
	return r.correctCreateBodyRecording(ctx, entity, rec, body, held, true, maxBodyCorrectionAttempts)
}

// correctCreateBodyRecording is adjustCreate, and also says whether the correcting it
// does is a fact about the entity.
//
// Re-creating a parent so a child has something to address is a means to an
// end: the fields that create needs are facts about the parent, and the
// parent's own steps record them against the parent. Recorded here they would
// be attributed to whichever child happened to need the parent first.
//
// attempts bounds the adjustments made before the last refusal is answered
// as the result.
func (r *runner) correctCreateBodyRecording(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, held string, record bool, attempts int) (bodyCorrection, error) {
	applied := map[string]bool{}
	var last *httpResult
	var adds []pendingAdd
	var choice *pendingAdd
	adjusted := false
	// pending is a refusal one of the retries below already answered the
	// current body with, classified on the next pass instead of sending the
	// same body again.
	var pending *httpResult

	// The API took a body carrying every field the grammar added, so each
	// of those fields is a fact about this entity rather than a reading of a
	// sentence.
	accepted := func(obj *createdObject, res *httpResult) bodyCorrection {
		if record {
			for _, add := range adds {
				r.recordAdjustAdd(entity, add.field, add.condGate, add.condVal, add.excerpt)
			}
		}
		return bodyCorrection{obj: obj, res: res, body: body, bodyCorrected: adjusted}
	}

	for i := 0; i < attempts; i++ {
		var obj *createdObject
		var res *httpResult
		if pending != nil {
			res, pending = pending, nil
		} else {
			var err error
			obj, res, err = r.createObject(ctx, entity, rec, body)
			if err != nil {
				return bodyCorrection{}, err
			}
		}
		last = res
		if obj != nil {
			return accepted(obj, res), nil
		}
		if res == nil || !res.refused() {
			return bodyCorrection{res: res, body: body, bodyCorrected: adjusted, unresolved: true}, nil
		}
		// A conflict is about an object that already exists, not about the
		// body: no field edit resolves it, and cycling values against it
		// only spends the budget making the same object again.
		if res.status == http.StatusConflict {
			return bodyCorrection{res: res, body: body, bodyCorrected: adjusted, unresolved: true}, nil
		}
		// A field added from a choice the API then refuses by name gives way
		// to the next field the choice offered.
		if choice != nil && res.mentions(choice.field) {
			if next := r.nextChoice(entity, body, choice); next != "" {
				delete(body, choice.field)
				adds = withoutAdd(adds, choice.field)
				body[next] = r.synthesiseField(entity, next)
				applied["a:"+next] = true
				choice = &pendingAdd{field: next, candidates: choice.candidates, excerpt: res.excerpt}
				adds = append(adds, *choice)
				adjusted = true
				continue
			}
		}
		if added, ok := r.applyAdjustment(ctx, entity, body, res, applied, record); ok {
			adds = append(adds, added...)
			choice = nil
			if len(added) == 1 && len(added[0].candidates) > 1 {
				choice = &added[0]
			}
			adjusted = true
			continue
		}
		// The grammar could not correct the refusal. Before giving up: widen
		// a composite field to the members the document attests, try the
		// entity's own name for a discriminator value the API refused, then
		// value-cycle — a free-form conditional refusal that names an enum
		// field the entity declares is often satisfied by another of that
		// field's values.
		wobj, wres, widened, err := r.retryAttestedComposites(ctx, entity, rec, body, res, applied)
		if err != nil {
			return bodyCorrection{}, err
		}
		if wobj != nil {
			adjusted = true
			return accepted(wobj, wres), nil
		}
		if widened {
			pending = wres
			adjusted = true
			continue
		}
		sobj, sres, progressed, err := r.retryCollectionSegments(ctx, entity, rec, body, held, res, applied)
		if err != nil {
			return bodyCorrection{}, err
		}
		if sobj != nil {
			adjusted = true
			return accepted(sobj, sres), nil
		}
		if progressed {
			pending = sres
			adjusted = true
			continue
		}
		cobj, cres, cycled, err := r.retryAcrossValues(ctx, entity, rec, body, held, res, applied)
		if err != nil {
			return bodyCorrection{}, err
		}
		if cycled {
			adjusted = true
			return accepted(cobj, cres), nil
		}
		return bodyCorrection{res: res, body: body, bodyCorrected: adjusted, unresolved: true,
			freeFormConditional: r.isConditionalRefusal(entity, res)}, nil
	}
	return bodyCorrection{res: last, body: body, bodyCorrected: adjusted, unresolved: true,
		freeFormConditional: r.isConditionalRefusal(entity, last)}, nil
}

// addMember puts one member into an object the body carries, for a refusal
// that named the object and the member together — "repeat type must be
// specified". The member's value is the one the document attests for it,
// read from the object's widened form. Answers the dotted path it set, and
// false where the container is not a declared field, the body holds no
// object under it, or the document attests no such member.
func (r *runner) addMember(entity *entityState, body map[string]any, container, member string, applied map[string]bool) (string, bool) {
	known := r.hints[entity.plan.Entity]
	name := declaredSpelling(container, known)
	if name == "" {
		return "", false
	}
	object, ok := body[name].(map[string]any)
	if !ok {
		return "", false
	}
	synthesis, ok := r.syntheses[entity.plan.Entity]
	if !ok {
		return "", false
	}
	key, value, ok := synthesis.memberValue(name, member)
	if !ok {
		return "", false
	}
	path := name + "." + key
	if _, has := object[key]; has || applied["a:"+path] {
		return "", false
	}
	object[key] = value
	applied["a:"+path] = true
	return path, true
}

// timestampField names the body's top-level timestamp a refusal about time
// is about, when the refusal does not say: the one whose name says it
// starts, or the only one there is. Empty when the choice is not clear.
func timestampField(body map[string]any) string {
	var stamps []string
	for field, value := range body {
		if text, ok := value.(string); ok {
			if _, isStamp := observe.ParseTimestamp(text); isStamp {
				stamps = append(stamps, field)
			}
		}
	}
	sort.Strings(stamps)
	for _, field := range stamps {
		if strings.Contains(strings.ToLower(field), "start") {
			return field
		}
	}
	if len(stamps) == 1 {
		return stamps[0]
	}
	return ""
}

// nextChoice answers the field a refused choice moves on to: the next one
// the refusal offered that the entity declares and the body lacks. Empty
// when the choice is exhausted.
func (r *runner) nextChoice(entity *entityState, body map[string]any, choice *pendingAdd) string {
	after := false
	known := r.hints[entity.plan.Entity]
	for _, candidate := range choice.candidates {
		spelled := candidate
		if known != nil {
			spelled = declaredSpelling(candidate, known)
		} else if strings.ContainsAny(candidate, ". ") {
			spelled = ""
		}
		if spelled == choice.field {
			after = true
			continue
		}
		if after && spelled != "" && !present(body, spelled) {
			return spelled
		}
	}
	return ""
}

// withoutAdd drops one field's pending add: the API refused the field by
// name, so the body no longer carries it and nothing is recorded for it.
func withoutAdd(adds []pendingAdd, field string) []pendingAdd {
	out := adds[:0]
	for _, add := range adds {
		if add.field != field {
			out = append(out, add)
		}
	}
	return out
}

// retryAttestedComposites corrects a refusal of a body whose composite fields
// carry their required members alone by widening one at a time to the
// members the document attests — an element the API refuses as incomplete is
// routinely taken once it carries the documented values. Each field is
// widened once; a widening the API refuses is undone before the next.
//
// A widened field the API answers with a different refusal — one no longer
// naming the field — has moved the create on: the wider value stays in the
// body, the new refusal is answered for the loop to classify, and widened
// reports it. Restoring the smaller value would put the next adjustment on
// a body the API had already refused for the field.
func (r *runner) retryAttestedComposites(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, refusal *httpResult, applied map[string]bool) (*createdObject, *httpResult, bool, error) {
	synthesis, ok := r.syntheses[entity.plan.Entity]
	if !ok {
		return nil, nil, false, nil
	}
	fields := make([]string, 0, len(body))
	for field := range body {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if applied["w:"+field] {
			continue
		}
		wider, has := synthesis.attestedValue(field)
		if !has || reflect.DeepEqual(wider, body[field]) {
			continue
		}
		applied["w:"+field] = true
		smaller := body[field]
		body[field] = wider
		obj, res, err := r.createObject(ctx, entity, rec, body)
		if err != nil {
			return nil, nil, false, err
		}
		if obj != nil {
			return obj, res, false, nil
		}
		if res != nil && res.refused() && !res.mentions(field) && (refusal == nil || !bytes.Equal(res.body, refusal.body)) {
			return nil, res, true, nil
		}
		body[field] = smaller
		if res == nil || !res.refused() {
			break
		}
	}
	return nil, nil, false, nil
}

// pendingAdd is one field the grammar added to a body, held until the API
// takes that body.
//
// A refusal names a field in the API's own vocabulary, which is not always the
// vocabulary of the wire: one pilot answers a create carrying roleName with
// "Error in field roleName : must not be null", and another names
// loginAccountGroup for a property the document spells loginAccountGroupId.
// Asserting a requirement from the sentence alone puts a property the API
// never accepts into the document.
type pendingAdd struct {
	field    string
	condGate string
	condVal  string
	excerpt  observe.Excerpt
	// candidates are the other fields the refusal offered in place of this
	// one, where it offered a choice.
	candidates []string
}

// applyAdjustment classifies one refusal and mutates body toward acceptance,
// reporting the adds it made and whether it made progress. It refuses to loop:
// an add of a field already present, a remove of a field already absent, a
// nested "a.b" target it cannot synthesise, or a borrow that returns the same
// value all stop the loop rather than spin it.
//
// An add is answered rather than recorded: only the caller knows whether the
// API went on to take the body carrying it. Removes, requires and borrows are
// recorded here as before — each is a raw signal for the inference to weigh,
// not a claim about a property's existence.
func (r *runner) applyAdjustment(ctx context.Context, entity *entityState, body map[string]any, res *httpResult, applied map[string]bool, record bool) ([]pendingAdd, bool) {
	act := classifyRefusal(res)
	switch act.kind {
	case adjustmentAdd:
		field := r.addField(entity, body, act, refusalMessage(res.body))
		if field == "" && act.container != "" {
			path, ok := r.addMember(entity, body, act.container, act.field, applied)
			if !ok {
				return nil, false
			}
			return []pendingAdd{{field: path, excerpt: res.excerpt}}, true
		}
		if field == "" || applied["a:"+field] {
			return nil, false
		}
		body[field] = r.synthesiseField(entity, field)
		applied["a:"+field] = true
		return []pendingAdd{{
			field: field, condGate: act.condGate, condVal: act.condVal, excerpt: res.excerpt,
			candidates: act.candidates,
		}}, true
	case adjustmentRequires:
		if strings.Contains(act.field, ".") || applied["a:"+act.field] || present(body, act.field) {
			return nil, false
		}
		body[act.field] = r.synthesiseField(entity, act.field)
		applied["a:"+act.field] = true
		if record {
			r.recordAdjustment(entity, infer.AdjustRequires, act.field, act.trigger, "")
		}
		return nil, true
	case adjustmentRevalue:
		field := act.field
		if field == "" {
			field = timestampField(body)
		}
		current, has := body[field]
		if !has || applied["v:"+field] {
			return nil, false
		}
		replacement, ok := revalued(act.revalue, current, r.hints[entity.plan.Entity][field])
		if !ok {
			return nil, false
		}
		body[field] = replacement
		applied["v:"+field] = true
		if act.revalue == futureValue && entity != nil && entity.ev != nil {
			if entity.ev.futureFields == nil {
				entity.ev.futureFields = map[string]bool{}
			}
			entity.ev.futureFields[field] = true
		}
		return nil, true
	case adjustmentRemove:
		if applied["r:"+act.field] {
			return nil, false
		}
		if act.mutuallyExclusiveWith != "" && !present(body, act.mutuallyExclusiveWith) {
			// Only one of the pair was sent, so it is not the pair the API
			// objects to; removing the one present would leave neither.
			return nil, false
		}
		if strings.ContainsAny(act.field, ".[") {
			// A nested member the API refuses is removed where it sits. That
			// is a fact about the element, not about a top-level property,
			// so nothing is recorded against the entity's attributes.
			if !removePath(body, act.field) {
				return nil, false
			}
			applied["r:"+act.field] = true
			return nil, true
		}
		if !present(body, act.field) {
			return nil, false
		}
		delete(body, act.field)
		applied["r:"+act.field] = true
		if record {
			r.recordAdjustment(entity, infer.AdjustRemove, act.field, act.condGate, act.condVal)
		}
		return nil, true
	case adjustmentBorrow:
		id, ok := r.borrow(ctx, entity, act.collection)
		if !ok || fmt.Sprint(body[act.field]) == id {
			return nil, false
		}
		body[act.field] = id
		applied["b:"+act.field] = true
		if record {
			r.recordAdjustment(entity, infer.AdjustBorrow, act.field, act.collection, "")
		}
		return nil, true
	default:
		return nil, false
	}
}

// recordAdjustAdd records an add and emits the observation the committed
// vocabulary already carries: a plain required field is requiredByAPI, a
// value-conditional one is requiredWhen scoped to the gate the refusal named.
// The triangulating inference also derives these from the raw adjustment; this
// per-probe emission keeps the excerpt proof of the specific refusal.
func (r *runner) recordAdjustAdd(entity *entityState, field, condGate, condVal string, ex observe.Excerpt) {
	r.recordAdjustment(entity, infer.AdjustAdd, field, condGate, condVal)
	if condGate == "" {
		r.record(entity.plan.Entity, field, observe.KindRequiredByAPI, true, nil, observe.OutcomeConfirmed, ex)
		return
	}
	cond := &observe.Condition{Attribute: condGate, Equals: condVal}
	r.record(entity.plan.Entity, field, observe.KindRequiredWhen, true, cond, observe.OutcomeConfirmed, ex)
}

// recordAdjustment appends one raw adjustment signal for the inference to read.
func (r *runner) recordAdjustment(entity *entityState, action infer.AdjustAction, field, gateField, gateValue string) {
	r.adjustments = append(r.adjustments, infer.RequestAdjustment{
		Entity: entity.plan.Entity, Action: action, Field: field,
		GateField: gateField, GateValue: gateValue,
	})
}

// sortedAdjustments deduplicates and orders the raw adjustment signals so the
// summary is stable across runs: the same field added under three variants is
// one signal, and the ordering never depends on which variant ran first.
func sortedAdjustments(in []infer.RequestAdjustment) []infer.RequestAdjustment {
	seen := map[infer.RequestAdjustment]bool{}
	out := make([]infer.RequestAdjustment, 0, len(in))
	for _, a := range in {
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Entity != b.Entity:
			return a.Entity < b.Entity
		case a.Action != b.Action:
			return a.Action < b.Action
		case a.Field != b.Field:
			return a.Field < b.Field
		case a.GateField != b.GateField:
			return a.GateField < b.GateField
		default:
			return a.GateValue < b.GateValue
		}
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// present reports whether a body already carries a top-level field.
func present(body map[string]any, field string) bool {
	_, ok := body[field]
	return ok
}

// cloneAnyMap is a shallow copy the adjustment loop mutates at the top level;
// nested values are shared and never mutated.
func cloneAnyMap(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	return out
}

// removePath deletes one nested member a refusal named by its path —
// "secrets[0].id", "authentication.username" — and reports whether anything
// was there to delete. A dotted segment descends into an object, an index
// into a list.
func removePath(body map[string]any, path string) bool {
	segments := strings.Split(path, ".")
	var current any = body
	for i, segment := range segments {
		name, index, indexed := splitIndex(segment)
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		last := i == len(segments)-1
		if last && !indexed {
			if _, has := object[name]; !has {
				return false
			}
			delete(object, name)
			return true
		}
		next, has := object[name]
		if !has {
			return false
		}
		if indexed {
			list, ok := next.([]any)
			if !ok || index < 0 || index >= len(list) {
				return false
			}
			if last {
				object[name] = append(append([]any{}, list[:index]...), list[index+1:]...)
				return true
			}
			next = list[index]
		}
		current = next
	}
	return false
}

// splitIndex reads "name[3]" as name and 3; a plain segment answers -1 and
// false.
func splitIndex(segment string) (string, int, bool) {
	open := strings.Index(segment, "[")
	if open < 0 || !strings.HasSuffix(segment, "]") {
		return segment, -1, false
	}
	index, err := strconv.Atoi(segment[open+1 : len(segment)-1])
	if err != nil {
		return segment, -1, false
	}
	return segment[:open], index, true
}

// revalued answers the replacement a revalue rule makes for a refused value,
// and false where the rule does not apply to it.
func revalued(rule revalueRule, current any, h strategy.SyntheticValueRules) (any, bool) {
	switch rule {
	case futureValue:
		text, ok := current.(string)
		if !ok {
			return nil, false
		}
		if _, ok := observe.ParseTimestamp(text); !ok {
			return nil, false
		}
		// Whole seconds, so an API storing to the minute is seen to: what
		// it answers for a value with seconds is the normalisation the
		// generated state has to read through.
		ahead := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
		if len(text) == len("2006-01-02") {
			return ahead.Format("2006-01-02"), true
		}
		return ahead.Format(time.RFC3339), true
	case positiveValue:
		n, ok := asFloat(current)
		if !ok || n > 0 {
			return nil, false
		}
		least := 1.0
		if h.Minimum != nil && *h.Minimum > least {
			least = *h.Minimum
		}
		if _, isFloat := current.(float64); isFloat && h.Type != "integer" {
			return least, true
		}
		return int64(least), true
	}
	return nil, false
}
