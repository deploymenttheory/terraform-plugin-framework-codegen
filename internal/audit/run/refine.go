package run

// The adaptive refinement loop is what makes the executor learn from a
// refusal instead of merely recording it. A create or update the API rejects
// with a 4xx often names, in a machine-legible sentence, exactly what is
// wrong: a field that must be present, a field that is not valid for this
// variant, a field that requires a sibling, or a field that must reference a
// live object in another collection. The loop parses that sentence, changes
// the body accordingly, and retries — bounded hard, so a server that keeps
// refusing can never spin the loop forever.
//
// The classification grammar is the quirk server's stable 400 vocabulary
// (documented on its validators), parsed defensively so a real API that
// phrases the same fact differently still lands on the right action:
//
//	field X is required[ when G=V]              -> ADD X   (requiredByAPI / requiredWhen)
//	field X is not valid[ when G=V]             -> REMOVE X (validWhen)
//	field X requires field Y to be set          -> ADD Y   (requiresField)
//	field X must reference an existing <coll>   -> BORROW a real id for X
//	anything else                               -> STOP    (inconclusive)
//
// Every change is recorded as a Refinement so a later inference wave can read
// what the live API forced, and a required-field add also emits the
// observation the existing vocabulary already carries.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
)

// maxRefineIters bounds one create-or-update attempt's refinements. Combined
// with the no-progress guard and the entity request budget, it guarantees the
// loop terminates: six sequential edits is already more than any single body
// the compiler emits can legitimately need.
const maxRefineIters = 6

// RefineAction names one kind of change the refinement loop made to a body.
// These are working identifiers for the raw signal a later inference wave
// consumes; the committed observation vocabulary they feed is a separate
// decision.
type RefineAction string

const (
	// RefineAdd: a field was added because the API said it was required.
	RefineAdd RefineAction = "add"
	// RefineRemove: a field was removed because the API said it was not valid
	// for the variant under test.
	RefineRemove RefineAction = "remove"
	// RefineRequires: a field was added because the API said another field
	// requires it.
	RefineRequires RefineAction = "requires"
	// RefineBorrow: a field's value was replaced with a real id borrowed from
	// another collection, because the API said it must reference a live one.
	RefineBorrow RefineAction = "borrow"
)

// Refinement is one change the adaptive loop made, recorded so a later wave
// can infer the conditional edge it implies without re-running the audit. It
// carries no excerpt: the proof lives on the observations the add path emits,
// and keeping the summary excerpt-free keeps redaction trivial.
type Refinement struct {
	Entity    string       `json:"entity"`
	Action    RefineAction `json:"action"`
	Field     string       `json:"field"`
	GateField string       `json:"gateField,omitempty"`
	GateValue string       `json:"gateValue,omitempty"`
}

// refineResult is the outcome of a bounded refinement loop.
type refineResult struct {
	// obj is the created object when a create attempt finally succeeded.
	obj *createdObject
	// res is the last response seen — the success or the refusal that ended
	// the loop.
	res *httpResult
	// body is the final body, with every refinement applied.
	body map[string]any
	// refined reports that the loop changed the body at least once. A create
	// that failed with refined false was refused as sent, unhealably; one
	// that failed with refined true was partly built and then stuck, which is
	// a weaker signal about the body it started from.
	refined bool
	// gaveUp reports that the loop ended without success: the refusal could
	// not be classified into an action, an action made no progress, a borrow
	// found nothing, or the iteration bound was hit.
	gaveUp bool
}

// refusal action kinds.
type actKind int

const (
	actStop actKind = iota
	actAdd
	actRemove
	actRequires
	actBorrow
)

// refineAction is one parsed instruction from a refusal.
type refineAction struct {
	kind       actKind
	field      string
	collection string
	trigger    string
	condGate   string
	condVal    string
}

var (
	reRequires  = regexp.MustCompile(`field (\S+) requires field (\S+) to be set`)
	reNotValid  = regexp.MustCompile(`field (\S+) is not valid(?: when (\w+)=(\S+))?`)
	reReference = regexp.MustCompile(`field (\S+) must reference an existing (\w+)`)
	reRequired  = regexp.MustCompile(`field (\S+) is required(?: when (\w+)=(\S+))?`)
)

// classifyRefusal reads a 4xx response body and decides what to change. The
// order is deliberate: the two-field grammars (requires, reference) and the
// removal grammar are checked before the bare "is required", because "requires
// field Y" and "is required" share a stem.
func classifyRefusal(res *httpResult) refineAction {
	msg := refusalMessage(res.body)
	if msg == "" {
		return refineAction{kind: actStop}
	}
	if m := reRequires.FindStringSubmatch(msg); m != nil {
		return refineAction{kind: actRequires, field: cleanField(m[2]), trigger: cleanField(m[1])}
	}
	if m := reNotValid.FindStringSubmatch(msg); m != nil {
		return refineAction{kind: actRemove, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	if m := reReference.FindStringSubmatch(msg); m != nil {
		return refineAction{kind: actBorrow, field: cleanField(m[1]), collection: strings.ToLower(m[2])}
	}
	if m := reRequired.FindStringSubmatch(msg); m != nil {
		return refineAction{kind: actAdd, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	return refineAction{kind: actStop}
}

// refusalMessage pulls the human-legible sentence out of whichever error
// envelope the API used — problem+json's detail, an oauth error_description, a
// legacy errorMessage — falling back to the raw body when it is not JSON. It
// deliberately does not join the title: a bare field name in detail beside a
// generic title must not be mistaken for a "field X is required" sentence.
func refusalMessage(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		for _, k := range []string{"detail", "message", "error_description", "errorMessage", "error", "title"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	return string(raw)
}

// cleanField strips the trailing punctuation a refusal sentence might carry
// after a field name.
func cleanField(s string) string {
	return strings.TrimRight(s, ".,;:")
}

// refineCreate wraps a guarded, ledgered create in the bounded refinement
// loop. It returns as soon as a create succeeds, and otherwise applies one
// refinement per iteration until the refusal stops being actionable, the
// bound is hit, or the entity budget is exhausted (surfaced as the error).
func (r *runner) refineCreate(ctx context.Context, ent *entityState, rec *entityRecipe, body map[string]any) (refineResult, error) {
	applied := map[string]bool{}
	var last *httpResult
	refined := false
	for i := 0; i < maxRefineIters; i++ {
		obj, res, err := r.createObject(ctx, ent, rec, body)
		if err != nil {
			return refineResult{}, err
		}
		last = res
		if obj != nil {
			return refineResult{obj: obj, res: res, body: body, refined: refined}, nil
		}
		if res == nil || !res.refused() {
			return refineResult{res: res, body: body, refined: refined, gaveUp: true}, nil
		}
		if !r.applyRefinement(ctx, ent, body, res, applied) {
			return refineResult{res: res, body: body, refined: refined, gaveUp: true}, nil
		}
		refined = true
	}
	return refineResult{res: last, body: body, refined: refined, gaveUp: true}, nil
}

// applyRefinement classifies one refusal and mutates body toward acceptance,
// reporting whether it made progress. It refuses to loop: an add of a field
// already present, a remove of a field already absent, a nested "a.b" target
// it cannot synthesise, or a borrow that returns the same value all stop the
// loop rather than spin it.
func (r *runner) applyRefinement(ctx context.Context, ent *entityState, body map[string]any, res *httpResult, applied map[string]bool) bool {
	act := classifyRefusal(res)
	switch act.kind {
	case actAdd:
		if strings.Contains(act.field, ".") || applied["a:"+act.field] || present(body, act.field) {
			return false
		}
		body[act.field] = r.synthField(ent, act.field)
		applied["a:"+act.field] = true
		r.recordRefineAdd(ent, act.field, act.condGate, act.condVal, res.excerpt)
		return true
	case actRequires:
		if strings.Contains(act.field, ".") || applied["a:"+act.field] || present(body, act.field) {
			return false
		}
		body[act.field] = r.synthField(ent, act.field)
		applied["a:"+act.field] = true
		r.recordRefinement(ent, RefineRequires, act.field, act.trigger, "")
		return true
	case actRemove:
		if !present(body, act.field) || applied["r:"+act.field] {
			return false
		}
		delete(body, act.field)
		applied["r:"+act.field] = true
		r.recordRefinement(ent, RefineRemove, act.field, act.condGate, act.condVal)
		return true
	case actBorrow:
		id, ok := r.borrow(ctx, ent, act.collection)
		if !ok || fmt.Sprint(body[act.field]) == id {
			return false
		}
		body[act.field] = id
		applied["b:"+act.field] = true
		r.recordRefinement(ent, RefineBorrow, act.field, act.collection, "")
		return true
	default:
		return false
	}
}

// recordRefineAdd records an add and emits the observation the committed
// vocabulary already carries: a plain required field is requiredByAPI, a
// value-conditional one is requiredWhen scoped to the gate the refusal named.
func (r *runner) recordRefineAdd(ent *entityState, field, condGate, condVal string, ex observe.Excerpt) {
	r.recordRefinement(ent, RefineAdd, field, condGate, condVal)
	if condGate == "" {
		r.record(ent.plan.Entity, field, observe.KindRequiredByAPI, true, nil, observe.OutcomeConfirmed, ex)
		return
	}
	cond := &observe.Condition{Attribute: condGate, Equals: condVal}
	r.record(ent.plan.Entity, field, observe.KindRequiredWhen, true, cond, observe.OutcomeConfirmed, ex)
}

// recordRefinement appends one raw refinement signal for a later wave.
func (r *runner) recordRefinement(ent *entityState, action RefineAction, field, gateField, gateValue string) {
	r.refinements = append(r.refinements, Refinement{
		Entity: ent.plan.Entity, Action: action, Field: field,
		GateField: gateField, GateValue: gateValue,
	})
}

// sortedRefinements deduplicates and orders the raw refinement signals so the
// summary is stable across runs: the same field added under three variants is
// one signal, and the ordering never depends on which variant ran first.
func sortedRefinements(in []Refinement) []Refinement {
	seen := map[Refinement]bool{}
	out := make([]Refinement, 0, len(in))
	for _, r := range in {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
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

// cloneAnyMap is a shallow copy the refinement loop mutates at the top level;
// nested values are shared and never mutated.
func cloneAnyMap(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	return out
}
