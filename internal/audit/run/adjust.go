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
// A real API's value-conditional refusals do not always fit that grammar:
// A real API may answer a create with a free-form message such as "field X is not
// supported for the provided object type", which names a field (type) in prose
// no clause matches. When classification stops, the loop falls back on
// generalized field extraction (namedKnownFields) — a refusal that names any
// field the entity declares is a conditional constraint, not an unintelligible
// refusal — and value-cycling (cycleConditional), which tries alternate enum
// values for the implicated sibling to find a body the API accepts. See cycle.go.
//
// Every change is recorded as a requestAdjustment so the triangulating
// inference can read what the live API forced, and a required-field add also
// emits the observation the existing vocabulary already carries.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// maxAdjustIters bounds one create-or-update attempt's adjustments. Combined
// with the no-progress guard and the entity request budget, it guarantees the
// loop terminates: six sequential edits is already more than any single body
// the compiler emits can legitimately need.
const maxAdjustIters = 6

// adjustResult is the outcome of a bounded adjustment loop.
type adjustResult struct {
	// obj is the created object when a create attempt finally succeeded.
	obj *createdObject
	// res is the last response seen — the success or the refusal that ended
	// the loop.
	res *httpResult
	// body is the final body, with every adjustment applied.
	body map[string]any
	// adjusted reports that the loop changed the body at least once. A create
	// that failed with adjusted false was refused as sent, unhealably; one
	// that failed with adjusted true was partly built and then stuck, which is
	// a weaker signal about the body it started from.
	adjusted bool
	// tried names the fields the additive search added, in the order it added
	// them. Set only where the search ran and failed: it is what the block
	// reason needs to say more than that a status came back.
	tried []string
	// gaveUp reports that the loop ended without success: the refusal could
	// not be classified into an action, an action made no progress, a borrow
	// found nothing, or the iteration bound was hit.
	gaveUp bool
	// conditional reports that the final refusal named a field the entity
	// declares even though the grammar could not classify it — a free-form
	// conditional constraint. A caller that would otherwise block treats this
	// as captured edge-evidence and continues instead: value-cycling has
	// already recorded what it could, and blocking would lose the variants and
	// probes the entity can still exercise.
	conditional bool
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

// refusalAction is one parsed instruction from a refusal.
type refusalAction struct {
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

	// reFieldNamed matches a refusal that names its field mid-sentence and
	// states the complaint after a separator, which is how a framework that
	// wraps its validation errors in prose reports them.
	reFieldNamed = regexp.MustCompile(`(?i)\bfield\s+([\w.\[\]-]+)\s*[:\-]\s*(.+)`)
	// reTheRequired matches the bare English an API writes when it names the
	// field it wanted and nothing else about it.
	reTheRequired = regexp.MustCompile(`(?i)\bthe\s+([\w.]+)\s+(?:is|are)\s+required\b`)
	// reFieldSaid matches the field-prefixed refusal a validation framework
	// emits: the property it rejected, a colon, then its complaint. The field
	// is a dotted path when the API validates a nested request object, and the
	// last segment is the name the request body spells.
	reFieldSaid = regexp.MustCompile(`^\s*([\w.\[\]-]+)\s*:\s*(.+)$`)
	// reAbsent matches the complaints that mean "you sent nothing for this",
	// as distinct from "what you sent is wrong": only absence is healed by
	// adding a value.
	reAbsent = regexp.MustCompile(`(?i)\b(?:is required|is mandatory|must not be (?:null|empty|blank)|may not be (?:null|empty|blank)|cannot be (?:null|empty|blank)|must be (?:provided|specified|present)|missing)\b`)
)

// classifyRefusal reads a 4xx response body and decides what to change. The
// order is deliberate: the two-field grammars (requires, reference) and the
// removal grammar are checked before the bare "is required", because "requires
// field Y" and "is required" share a stem.
func classifyRefusal(res *httpResult) refusalAction {
	msg := refusalMessage(res.body)
	if msg == "" {
		return refusalAction{kind: actStop}
	}
	if m := reRequires.FindStringSubmatch(msg); m != nil {
		return refusalAction{kind: actRequires, field: cleanField(m[2]), trigger: cleanField(m[1])}
	}
	if m := reNotValid.FindStringSubmatch(msg); m != nil {
		return refusalAction{kind: actRemove, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	if m := reReference.FindStringSubmatch(msg); m != nil {
		return refusalAction{kind: actBorrow, field: cleanField(m[1]), collection: strings.ToLower(m[2])}
	}
	if m := reRequired.FindStringSubmatch(msg); m != nil {
		return refusalAction{kind: actAdd, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	if m := reFieldNamed.FindStringSubmatch(msg); m != nil && reAbsent.MatchString(m[2]) {
		return refusalAction{kind: actAdd, field: leafField(m[1])}
	}
	if m := reTheRequired.FindStringSubmatch(msg); m != nil {
		return refusalAction{kind: actAdd, field: leafField(m[1])}
	}
	// Checked last, because it is the loosest: any sentence at all can be read
	// as "<field>: <complaint>", so it must not pre-empt a grammar that names
	// two fields or a gate.
	if m := reFieldSaid.FindStringSubmatch(msg); m != nil && reAbsent.MatchString(m[2]) {
		return refusalAction{kind: actAdd, field: leafField(m[1])}
	}
	return refusalAction{kind: actStop}
}

// leafField is the last segment of a dotted refusal path, which is the name
// the request body spells: an API that validates its own nested request object
// reports "endpoint.url" for a property the document declares as "url".
func leafField(s string) string {
	s = cleanField(s)
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// refusalMessage pulls the human-legible sentence out of whichever error
// envelope the API used — problem+json's detail, an oauth error_description, a
// legacy errorMessage — falling back to the raw body when it is not JSON. It
// deliberately does not join the title: a bare field name in detail beside a
// generic title must not be mistaken for a "field X is required" sentence.
func refusalMessage(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		// The listed complaints come first: an envelope that carries both
		// names the field it rejected in the list and only summarises it in
		// the sentence, and a summary heals nothing.
		if listed := firstListed(m); listed != "" {
			return listed
		}
		for _, k := range []string{"detail", "message", "error_description", "errorMessage", "error", "title"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	return string(raw)
}

// firstListed pulls the first complaint out of an envelope that carries them
// as a list rather than a sentence, which is how an API that validates every
// property before answering reports what it rejected.
//
// Only the first is read: one refusal heals one field, and the next attempt
// re-reads whatever the API then complains about, so taking them one at a time
// converges without assuming the list is ordered or complete.
func firstListed(m map[string]any) string {
	for _, k := range []string{"errors", "messages", "details", "errorMessages", "validationErrors"} {
		listed, ok := m[k].([]any)
		if !ok {
			continue
		}
		for _, entry := range listed {
			switch e := entry.(type) {
			case string:
				if e != "" {
					return e
				}
			case map[string]any:
				// An entry that names the field separately is spelled back
				// into the "<field>: <complaint>" shape the grammar reads.
				field, _ := firstString(e, "field", "name", "property", "path", "pointer", "code")
				complaint, found := firstString(e, "message", "defaultMessage", "detail", "description", "error", "reason")
				if !found {
					continue
				}
				if field != "" {
					return field + ": " + complaint
				}
				return complaint
			}
		}
	}
	return ""
}

// firstString returns the first of the named keys the map carries as a
// non-empty string, and whether it found one.
func firstString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

// cleanField strips the trailing punctuation a refusal sentence might carry
// after a field name.
func cleanField(s string) string {
	return strings.TrimRight(s, ".,;:")
}

// adjustCreate wraps a guarded, ledgered create in the bounded adjustment
// loop. It returns as soon as a create succeeds, and otherwise applies one
// adjustment per iteration until the refusal stops being actionable, the
// bound is hit, or the entity budget is exhausted (surfaced as the error).
// held names the field this create pins and value-cycling must not vary — the
// variant's discriminator for a minimal or maximal create, the value under test
// for a per-enum-value create — so cycling searches the other enum fields for a
// body the API accepts without abandoning what the step is exercising.
func (r *runner) adjustCreate(ctx context.Context, ent *entityState, rec *entityRecipe, body map[string]any, held string) (adjustResult, error) {
	return r.adjustCreateRecording(ctx, ent, rec, body, held, true)
}

// adjustCreateRecording is adjustCreate, and also says whether the healing it
// does is a fact about the entity.
//
// Re-creating a parent so a child has something to address is a means to an
// end: the fields that create needs are facts about the parent, and the
// parent's own steps record them against the parent. Recorded here they would
// be attributed to whichever child happened to need the parent first.
func (r *runner) adjustCreateRecording(ctx context.Context, ent *entityState, rec *entityRecipe, body map[string]any, held string, record bool) (adjustResult, error) {
	applied := map[string]bool{}
	var last *httpResult
	var healed []pendingAdd
	adjusted := false
	for i := 0; i < maxAdjustIters; i++ {
		obj, res, err := r.createObject(ctx, ent, rec, body)
		if err != nil {
			return adjustResult{}, err
		}
		last = res
		if obj != nil {
			// The API took a body carrying every field the grammar added, so
			// each of those fields is a fact about this entity rather than a
			// reading of a sentence.
			if record {
				for _, add := range healed {
					r.recordAdjustAdd(ent, add.field, add.condGate, add.condVal, add.excerpt)
				}
			}
			return adjustResult{obj: obj, res: res, body: body, adjusted: adjusted}, nil
		}
		if res == nil || !res.refused() {
			return adjustResult{res: res, body: body, adjusted: adjusted, gaveUp: true}, nil
		}
		if added, ok := r.applyAdjustment(ctx, ent, body, res, applied, record); ok {
			healed = append(healed, added...)
			adjusted = true
			continue
		}
		// The grammar could not heal the refusal. Before giving up, try
		// value-cycling: a free-form conditional refusal that names an enum
		// field the entity declares is often satisfied by another of that
		// field's values.
		cobj, cres, healed, err := r.cycleConditional(ctx, ent, rec, body, held, res, applied)
		if err != nil {
			return adjustResult{}, err
		}
		if healed {
			return adjustResult{obj: cobj, res: cres, body: body, adjusted: true}, nil
		}
		return adjustResult{res: res, body: body, adjusted: adjusted, gaveUp: true,
			conditional: r.isConditionalRefusal(ent, res)}, nil
	}
	return adjustResult{res: last, body: body, adjusted: adjusted, gaveUp: true,
		conditional: r.isConditionalRefusal(ent, last)}, nil
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
func (r *runner) applyAdjustment(ctx context.Context, ent *entityState, body map[string]any, res *httpResult, applied map[string]bool, record bool) ([]pendingAdd, bool) {
	act := classifyRefusal(res)
	switch act.kind {
	case actAdd:
		if strings.Contains(act.field, ".") || applied["a:"+act.field] || present(body, act.field) {
			return nil, false
		}
		body[act.field] = r.synthField(ent, act.field)
		applied["a:"+act.field] = true
		return []pendingAdd{{
			field: act.field, condGate: act.condGate, condVal: act.condVal, excerpt: res.excerpt,
		}}, true
	case actRequires:
		if strings.Contains(act.field, ".") || applied["a:"+act.field] || present(body, act.field) {
			return nil, false
		}
		body[act.field] = r.synthField(ent, act.field)
		applied["a:"+act.field] = true
		if record {
			r.recordAdjustment(ent, infer.AdjustRequires, act.field, act.trigger, "")
		}
		return nil, true
	case actRemove:
		if !present(body, act.field) || applied["r:"+act.field] {
			return nil, false
		}
		delete(body, act.field)
		applied["r:"+act.field] = true
		if record {
			r.recordAdjustment(ent, infer.AdjustRemove, act.field, act.condGate, act.condVal)
		}
		return nil, true
	case actBorrow:
		id, ok := r.borrow(ctx, ent, act.collection)
		if !ok || fmt.Sprint(body[act.field]) == id {
			return nil, false
		}
		body[act.field] = id
		applied["b:"+act.field] = true
		if record {
			r.recordAdjustment(ent, infer.AdjustBorrow, act.field, act.collection, "")
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
func (r *runner) recordAdjustAdd(ent *entityState, field, condGate, condVal string, ex observe.Excerpt) {
	r.recordAdjustment(ent, infer.AdjustAdd, field, condGate, condVal)
	if condGate == "" {
		r.record(ent.plan.Entity, field, observe.KindRequiredByAPI, true, nil, observe.OutcomeConfirmed, ex)
		return
	}
	cond := &observe.Condition{Attribute: condGate, Equals: condVal}
	r.record(ent.plan.Entity, field, observe.KindRequiredWhen, true, cond, observe.OutcomeConfirmed, ex)
}

// recordAdjustment appends one raw adjustment signal for the inference to read.
func (r *runner) recordAdjustment(ent *entityState, action infer.AdjustAction, field, gateField, gateValue string) {
	r.adjustments = append(r.adjustments, infer.RequestAdjustment{
		Entity: ent.plan.Entity, Action: action, Field: field,
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
