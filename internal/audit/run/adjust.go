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
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// maxBodyCorrectionAttempts bounds one create-or-update attempt's adjustments. Combined
// with the no-progress guard and the entity request budget, it guarantees the
// loop terminates: six sequential edits is already more than any single body
// the compiler emits can legitimately need.
const maxBodyCorrectionAttempts = 6

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
	// mustBeDeclared marks an add read out of a looser sentence: the word
	// before an absence complaint, with no "field" marker to vouch for it.
	// Such a field is added only where the entity declares it; the
	// sentence alone does not prove the word is a wire property.
	mustBeDeclared bool
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
	// as distinct from "what you sent is wrong": only absence is corrected by
	// adding a value.
	reAbsent = regexp.MustCompile(`(?i)\b(?:is required|is mandatory|must not be (?:null|empty|blank)|may not be (?:null|empty|blank)|cannot be (?:null|empty|blank)|must be (?:provided|specified|present)|missing)\b`)
	// reMissingProperty matches a deserialiser naming the property it could
	// not find, quoted — the shape a polymorphic body's discriminator is
	// reported missing in.
	reMissingProperty = regexp.MustCompile(`(?i)\bmissing (?:[\w-]+ )*property '([\w.]+)'`)
	// reAtLeastOne matches a refusal offering a choice of fields, one of
	// which has to be present; the list after the colon is the candidates.
	reAtLeastOne = regexp.MustCompile(`(?i)\bat least one of (?:the following )?(?:is |are )?(?:required|mandatory)\s*[:\-]?\s*(.+)`)
	// reBareAbsent matches the word before an absence complaint, in a
	// sentence carrying no "field" marker: "endRepeat must be specified".
	reBareAbsent = regexp.MustCompile(`(?i)\b([A-Za-z][\w.]*)\s+(?:must be (?:specified|provided|present|set)|is required|is mandatory|cannot be (?:null|empty|blank)|must not be (?:null|empty|blank)|may not be (?:null|empty|blank))\b`)
)

// classifyRefusal reads a 4xx response body and decides what to change. The
// order is deliberate: the two-field grammars (requires, reference) and the
// removal grammar are checked before the bare "is required", because "requires
// field Y" and "is required" share a stem.
func classifyRefusal(res *httpResult) parsedRefusal {
	message := refusalMessage(res.body)
	if message == "" {
		return parsedRefusal{kind: adjustmentNone}
	}
	if m := reRequires.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentRequires, field: cleanField(m[2]), trigger: cleanField(m[1])}
	}
	if m := reNotValid.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentRemove, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	if m := reReference.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentBorrow, field: cleanField(m[1]), collection: strings.ToLower(m[2])}
	}
	if m := reRequired.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	// A choice of fields is read before the bare "the X is required", which
	// would otherwise take "the following" for a field name.
	if m := reAtLeastOne.FindStringSubmatch(message); m != nil {
		if candidates := listedCandidates(m[1]); len(candidates) > 0 {
			return parsedRefusal{kind: adjustmentAdd, field: candidates[0], candidates: candidates, mustBeDeclared: true}
		}
	}
	if m := reMissingProperty.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reFieldNamed.FindStringSubmatch(message); m != nil && reAbsent.MatchString(m[2]) {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reTheRequired.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	// Checked after every marked grammar, because it is loose: any sentence
	// at all can be read as "<field>: <complaint>", so it must not pre-empt a
	// grammar that names two fields or a gate.
	if m := reFieldSaid.FindStringSubmatch(message); m != nil && reAbsent.MatchString(m[2]) {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reBareAbsent.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1]), mustBeDeclared: true}
	}
	return parsedRefusal{kind: adjustmentNone}
}

// listedCandidates splits the field list a choice refusal ends with — "a, b
// or c" — into its members, in the order offered.
func listedCandidates(list string) []string {
	list = strings.TrimRight(strings.TrimSpace(list), ".")
	list = strings.ReplaceAll(list, " or ", ",")
	list = strings.ReplaceAll(list, " and ", ",")
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// declaredSpelling answers the wire name an entity declares for a field a
// refusal spelt in its own words — "query params" for queryParams — comparing
// with case and punctuation removed. Empty when nothing declared matches.
func declaredSpelling(candidate string, known map[string]strategy.SyntheticValueRules) string {
	wanted := lettersOf(candidate)
	if wanted == "" {
		return ""
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if lettersOf(name) == wanted {
			return name
		}
	}
	return ""
}

// lettersOf lower-cases a name and drops everything but letters and digits.
func lettersOf(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
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
		// the sentence, and a summary corrects nothing.
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
// Only the first is read: one refusal corrects one field, and the next attempt
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

// correctCreateBody wraps a guarded, ledgered create in the bounded adjustment
// loop. It returns as soon as a create succeeds, and otherwise applies one
// adjustment per iteration until the refusal stops being actionable, the
// bound is hit, or the entity budget is exhausted (surfaced as the error).
// held names the field this create pins and value-cycling must not vary — the
// variant's discriminator for a minimal or maximal create, the value under test
// for a per-enum-value create — so cycling searches the other enum fields for a
// body the API accepts without abandoning what the step is exercising.
func (r *runner) correctCreateBody(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, held string) (bodyCorrection, error) {
	return r.correctCreateBodyRecording(ctx, entity, rec, body, held, true)
}

// correctCreateBodyRecording is adjustCreate, and also says whether the correcting it
// does is a fact about the entity.
//
// Re-creating a parent so a child has something to address is a means to an
// end: the fields that create needs are facts about the parent, and the
// parent's own steps record them against the parent. Recorded here they would
// be attributed to whichever child happened to need the parent first.
func (r *runner) correctCreateBodyRecording(ctx context.Context, entity *entityState, rec *entityLifecycle, body map[string]any, held string, record bool) (bodyCorrection, error) {
	applied := map[string]bool{}
	var last *httpResult
	var corrected []pendingAdd
	adjusted := false
	for i := 0; i < maxBodyCorrectionAttempts; i++ {
		obj, res, err := r.createObject(ctx, entity, rec, body)
		if err != nil {
			return bodyCorrection{}, err
		}
		last = res
		if obj != nil {
			// The API took a body carrying every field the grammar added, so
			// each of those fields is a fact about this entity rather than a
			// reading of a sentence.
			if record {
				for _, add := range corrected {
					r.recordAdjustAdd(entity, add.field, add.condGate, add.condVal, add.excerpt)
				}
			}
			return bodyCorrection{obj: obj, res: res, body: body, bodyCorrected: adjusted}, nil
		}
		if res == nil || !res.refused() {
			return bodyCorrection{res: res, body: body, bodyCorrected: adjusted, unresolved: true}, nil
		}
		if added, ok := r.applyAdjustment(ctx, entity, body, res, applied, record); ok {
			corrected = append(corrected, added...)
			adjusted = true
			continue
		}
		// The grammar could not correct the refusal. Before giving up, try
		// the entity's own name for a discriminator value the API refused,
		// then value-cycling: a free-form conditional refusal that names an
		// enum field the entity declares is often satisfied by another of
		// that field's values.
		sobj, sres, corrected, err := r.retryCollectionSegments(ctx, entity, rec, body, held, res, applied)
		if err != nil {
			return bodyCorrection{}, err
		}
		if corrected {
			return bodyCorrection{obj: sobj, res: sres, body: body, bodyCorrected: true}, nil
		}
		cobj, cres, corrected, err := r.retryAcrossValues(ctx, entity, rec, body, held, res, applied)
		if err != nil {
			return bodyCorrection{}, err
		}
		if corrected {
			return bodyCorrection{obj: cobj, res: cres, body: body, bodyCorrected: true}, nil
		}
		return bodyCorrection{res: res, body: body, bodyCorrected: adjusted, unresolved: true,
			freeFormConditional: r.isConditionalRefusal(entity, res)}, nil
	}
	return bodyCorrection{res: last, body: body, bodyCorrected: adjusted, unresolved: true,
		freeFormConditional: r.isConditionalRefusal(entity, last)}, nil
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
func (r *runner) applyAdjustment(ctx context.Context, entity *entityState, body map[string]any, res *httpResult, applied map[string]bool, record bool) ([]pendingAdd, bool) {
	act := classifyRefusal(res)
	switch act.kind {
	case adjustmentAdd:
		field := r.addField(entity, body, act, refusalMessage(res.body))
		if field == "" || applied["a:"+field] {
			return nil, false
		}
		body[field] = r.synthesiseField(entity, field)
		applied["a:"+field] = true
		return []pendingAdd{{
			field: field, condGate: act.condGate, condVal: act.condVal, excerpt: res.excerpt,
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
	case adjustmentRemove:
		if !present(body, act.field) || applied["r:"+act.field] {
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
