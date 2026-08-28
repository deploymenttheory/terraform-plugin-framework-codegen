package run

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// evidence accumulates what one entity's responses showed, so per-field
// conclusions can be drawn once, at the end, with everything known — a
// field cannot be written down as carrying a server default before the
// consecutive read has had its chance to show it volatile.
type evidence struct {
	// sent is the resolved createMinimal body; got is what the first
	// successful read of that object answered.
	sent map[string]any
	got  map[string]any
	// maximalSent and maximalGot are the createMaximal pair, covering the
	// optional fields a minimal create never sends.
	maximalSent map[string]any
	maximalGot  map[string]any
	// The status each accepted create answered, kept so the recorded body
	// shows it was an acceptance rather than an assumption.
	sentStatus    int
	maximalStatus int
	// volatile marks fields the consecutive read saw change.
	volatile map[string]bool
	// omitted collects, per field, the value each created object answered
	// for a field its create body did not send — two samples that differ
	// are what tells a derived default from a constant one.
	omitted map[string][]any
	// idField is the identifier key create responses used, excluded from
	// field conclusions.
	idField string
	// values accumulates per-attribute value-set findings across the
	// enum-pinning and undocumented-value steps.
	values        map[string]*observe.Values
	valuesProof   map[string][]observe.Excerpt
	requiredWhens map[string]*requiredWhenPair
	// undeclared collects, per response field the entity's declared schema
	// lacks, the JSON type each read answered — a field seen with one
	// stable type becomes an undocumentedFieldInSpec observation, and a
	// field whose type wobbles between reads claims nothing.
	undeclared      map[string]string
	undeclaredProof map[string][]observe.Excerpt
	// undeclaredUnstable marks fields whose observed JSON type differed
	// between reads.
	undeclaredUnstable map[string]bool
	// update-style tallies across the updateField steps.
	updSucceeded   int
	updRefused     int
	updOmittedKept bool
	updOmittedLost bool
	updateProof    []observe.Excerpt
	// proof excerpts for the finalize-time observations.
	createProof *observe.Excerpt
	readProof   *observe.Excerpt

	// The raw material the triangulating inference reads, gathered across
	// every step. acceptedRequestBodies is every create body the API accepted,
	// resolved as sent — the positive half of variant diffing. listBodies is
	// the collection responses the pre-flight captured, for the list-shape
	// finding. combinedRefusals holds any field pairs a create was refused
	// for carrying together — the mutual-exclusion signal, empty unless the
	// refusal grammar names one.
	acceptedRequestBodies []map[string]any
	// futureFields is every field the adjustment loop moved ahead of now
	// because the API wanted it there: a replay must move it again.
	futureFields map[string]bool
	// references maps each field whose value was borrowed from another
	// collection to that collection's path: a replay must create such an
	// object of its own and refer to it, since the borrowed one is gone.
	references       map[string]string
	listBodies       [][]byte
	combinedRefusals []infer.FieldPair
	// conditionalValues records the value-cycling outcomes the executor
	// gathered correcting free-form conditional refusals — each (discriminator
	// value, sibling field, sibling value) the API accepted or refused — the
	// both-direction signal for a value-conditional validConfiguration.
	conditionalValues []infer.ConditionalValue
}

// requiredWhenPair tracks the with/without halves of one required-when
// check: created with the attribute present under the pinned condition,
// and with it omitted.
type requiredWhenPair struct {
	attribute string
	cond      observe.Condition
	with      *bool
	without   *bool
	proof     []observe.Excerpt
}

func newEvidence() *evidence {
	return &evidence{
		volatile:           map[string]bool{},
		omitted:            map[string][]any{},
		values:             map[string]*observe.Values{},
		valuesProof:        map[string][]observe.Excerpt{},
		requiredWhens:      map[string]*requiredWhenPair{},
		undeclared:         map[string]string{},
		undeclaredProof:    map[string][]observe.Excerpt{},
		undeclaredUnstable: map[string]bool{},
	}
}

// observeUndeclaredFields diffs one read response against the entity's
// declared schema properties, accumulating every field the spec omits with
// the JSON type it was observed carrying. Only the item object itself is
// diffed — collection envelopes never reach here — which is what keeps
// envelope noise out. An empty declared set means the plan carried no
// schema knowledge, and no claim can be made.
func (r *runner) observeUndeclaredFields(entity *entityState, obj map[string]any, excerpt observe.Excerpt) {
	if len(entity.plan.DeclaredProperties) == 0 || obj == nil {
		return
	}
	if entity.declared == nil {
		entity.declared = make(map[string]bool, len(entity.plan.DeclaredProperties))
		for _, name := range entity.plan.DeclaredProperties {
			entity.declared[name] = true
		}
	}
	for k, v := range obj {
		if entity.declared[k] || v == nil {
			continue
		}
		t := jsonTypeOf(v)
		if previous, seen := entity.ev.undeclared[k]; seen && previous != t {
			entity.ev.undeclaredUnstable[k] = true
			continue
		}
		entity.ev.undeclared[k] = t
		entity.ev.undeclaredProof[k] = appendProof(entity.ev.undeclaredProof[k], excerpt)
	}
}

// jsonTypeOf names a decoded JSON value's type the way the observation
// spells it.
func jsonTypeOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, json.Number:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return "unknown"
	}
}

// observeOmittedSamples records what a create's response answered for
// every field its body did not send.
func (r *runner) observeOmittedSamples(entity *entityState, sent, got map[string]any, id string) {
	if entity == nil || got == nil {
		return
	}
	if entity.ev.idField == "" {
		entity.ev.idField = identifyingProperty(got, id)
	}
	for k, v := range got {
		if _, wasSent := sent[k]; wasSent || v == nil {
			continue
		}
		entity.ev.omitted[k] = append(entity.ev.omitted[k], v)
	}
}

// identifyingProperty names the response property carrying the object's id:
// the plain "id" key when the response has one, otherwise the property whose
// value is the id the run already learned. Empty when neither answers.
//
// The run learns an id from a Location header or a self link as readily as
// from a body key, so an API whose path says {id} and whose body says "aid"
// is identified without either name having to match the other.
func identifyingProperty(got map[string]any, id string) string {
	if v, ok := got["id"]; ok && v != nil {
		return "id"
	}
	if id == "" {
		return ""
	}
	// Sorted, so one response always names the same property when two carry
	// the same value.
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if scalarString(got[k]) == id {
			return k
		}
	}
	return ""
}

// valuesFor is the per-attribute accumulator.
func (ev *evidence) valuesFor(attribute string) *observe.Values {
	v, ok := ev.values[attribute]
	if !ok {
		v = &observe.Values{}
		ev.values[attribute] = v
	}
	return v
}

// finalizeEvidence turns an entity's accumulated evidence into
// observations. Per-field conclusions iterate sorted names so two runs
// over the same behaviour emit identically ordered findings.
func (r *runner) finalizeEvidence(entity *entityState) {
	ev := entity.ev
	entityKey := entity.plan.Entity

	if ev.sent != nil && ev.got != nil {
		r.finalizeFields(entityKey, ev, ev.sent, ev.got, true)
	}
	if ev.maximalSent != nil && ev.maximalGot != nil {
		// The maximal pair covers only what the minimal comparison could
		// not: optional fields. Dedup keeps the minimal finding when both
		// speak.
		r.finalizeFields(entityKey, ev, ev.maximalSent, ev.maximalGot, false)
	}

	r.observeUndeclaredSent(entity)
	r.finalizeValues(entityKey, ev)
	r.finalizeRequiredWhens(ev)
	r.finalizeUpdateStyle(entityKey, ev)
	r.finalizeUndeclaredFields(entityKey, ev)
}

// observeUndeclaredSent adds every property an accepted create carried that
// the entityKey's declared schema lacks, with the JSON type it was sent as.
//
// The twin of observeUndeclaredFields, on the request side. An API that
// demands a property the document never declares refuses every create until
// the adjustment loop invents it, and the loop's own finding — requiredByAPI —
// cannot be applied to a property no schema declares. Recording the property
// as well as the requirement is what makes the requirement placeable.
//
// Only an accepted create counts. A field added into a body the API went on to
// refuse is a guess, and a guess is not evidence that the field exists.
func (r *runner) observeUndeclaredSent(entity *entityState) {
	if len(entity.plan.DeclaredProperties) == 0 || entity.ev.createProof == nil {
		return
	}
	if entity.declared == nil {
		entity.declared = make(map[string]bool, len(entity.plan.DeclaredProperties))
		for _, name := range entity.plan.DeclaredProperties {
			entity.declared[name] = true
		}
	}
	for _, body := range entity.ev.acceptedRequestBodies {
		for name, value := range body {
			if entity.declared[name] || value == nil {
				continue
			}
			t := jsonTypeOf(value)
			if previous, seen := entity.ev.undeclared[name]; seen && previous != t {
				entity.ev.undeclaredUnstable[name] = true
				continue
			}
			entity.ev.undeclared[name] = t
			entity.ev.undeclaredProof[name] = appendProof(entity.ev.undeclaredProof[name], *entity.ev.createProof)
		}
	}
}

// finalizeUndeclaredFields emits one undocumentedFieldInSpec observation
// per response field the declared schema lacks, provided every read showed
// it with the same JSON type — the API demonstrably carries the field, and
// the spec omits it.
func (r *runner) finalizeUndeclaredFields(entity string, ev *evidence) {
	fields := make([]string, 0, len(ev.undeclared))
	for f := range ev.undeclared {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		t := ev.undeclared[f]
		if ev.undeclaredUnstable[f] || t == "unknown" {
			continue
		}
		r.record(entity, f, observe.KindUndocumentedFieldInSpec, t, nil,
			observe.OutcomeConfirmed, ev.undeclaredProof[f]...)
	}
}

// finalizeFields draws the per-field conclusions from one sent/got pair:
// writable, normalisation, serverForced, serverDefault, derivedDefault.
func (r *runner) finalizeFields(entity string, ev *evidence, sent, got map[string]any, withDefaults bool) {
	proof := fieldProof(ev)
	for _, f := range sortedFieldUnion(sent, got) {
		if f == ev.idField || ev.volatile[f] {
			continue
		}
		sentV, wasSent := sent[f]
		gotV, present := got[f]

		switch {
		case wasSent && present && gotV == nil:
			// Present-and-null is a different observation from absent, and
			// conflating them is the classic error; neither writable nor
			// discarded can be claimed.
			r.record(entity, f, observe.KindWritable, nil, nil, observe.OutcomeInconclusive, proof...)
		case wasSent && present && equalJSON(sentV, gotV):
			r.record(entity, f, observe.KindWritable, true, nil, observe.OutcomeConfirmed, proof...)
		case wasSent && present && maskedEcho(sentV, gotV):
			// Answered as a run of mask characters: the value was taken and
			// is never given back, which is the same fact as its absence.
			r.record(entity, f, observe.KindWritable, false, nil, observe.OutcomeConfirmed, proof...)
		case wasSent && present:
			if norm, ok := normalisedForm(sentV, gotV); ok {
				r.record(entity, f, observe.KindNormalisation, norm, nil, observe.OutcomeConfirmed, proof...)
			} else if composite(sentV) || composite(gotV) {
				// An object or list answered differently is not the server
				// substituting a value: it is members added, masked or
				// dropped, and which of those it was is not one fact about
				// the field. A member sent and answered masked or not at all
				// is a fact about that member.
				r.record(entity, f, observe.KindServerForced, nil, nil, observe.OutcomeInconclusive, proof...)
				r.finalizeNestedMembers(entity, f, sentV, gotV, proof)
			} else {
				r.record(entity, f, observe.KindServerForced, true, nil, observe.OutcomeConfirmed, proof...)
			}
		case wasSent && !present:
			r.record(entity, f, observe.KindWritable, false, nil, observe.OutcomeConfirmed, proof...)
		case !wasSent && present && withDefaults && gotV != nil:
			r.finalizeDefault(entity, ev, f, gotV, proof)
		}
	}
}

// finalizeNestedMembers records, for the members of an object sent and
// answered, the ones the answer never carries as they were sent: absent, or
// a mask. Each lands on the member's dotted path, so the correction reaches
// the nested property. A list of objects is read through its first element,
// the one the body carried. Members answered as sent record nothing: the
// object as a whole is what the top-level evidence speaks for.
func (r *runner) finalizeNestedMembers(entity, field string, sent, got any, proof []observe.Excerpt) {
	sentObject, gotObject := firstObject(sent), firstObject(got)
	if sentObject == nil || gotObject == nil {
		return
	}
	for _, member := range sortedFieldUnion(sentObject, nil) {
		sentValue := sentObject[member]
		gotValue, present := gotObject[member]
		at := field + "." + member
		switch {
		case !present:
			r.record(entity, at, observe.KindWritable, false, nil, observe.OutcomeConfirmed, proof...)
		case maskedEcho(sentValue, gotValue):
			r.record(entity, at, observe.KindWritable, false, nil, observe.OutcomeConfirmed, proof...)
		case composite(sentValue) && composite(gotValue):
			r.finalizeNestedMembers(entity, at, sentValue, gotValue, proof)
		}
	}
}

// firstObject answers a value as an object: the value itself, or the first
// element of a list of objects; nil for anything else.
func firstObject(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	case []any:
		if len(v) > 0 {
			if first, ok := v[0].(map[string]any); ok {
				return first
			}
		}
	}
	return nil
}

// finalizeDefault decides constant versus derived for one unsent field
// the API populated, across every omitting create the run observed.
func (r *runner) finalizeDefault(entity string, ev *evidence, field string, firstSeen any, proof []observe.Excerpt) {
	distinct := false
	for _, sample := range ev.omitted[field] {
		if !equalJSON(sample, firstSeen) {
			distinct = true
			break
		}
	}
	if distinct {
		// Two omitting creates answered different values: the default is
		// computed, and writing it down as constant would be a permanent
		// lie. derivedDefault also vetoes the serverDefault claim.
		r.record(entity, field, observe.KindDerivedDefault, true, nil, observe.OutcomeConfirmed, proof...)
		return
	}
	if !scalarLike(firstSeen) {
		return
	}
	r.record(entity, field, observe.KindServerDefault, firstSeen, nil, observe.OutcomeConfirmed, proof...)
}

// finalizeValues emits one values observation per attribute that
// accumulated any value-set evidence.
func (r *runner) finalizeValues(entity string, ev *evidence) {
	attributes := make([]string, 0, len(ev.values))
	for a := range ev.values {
		attributes = append(attributes, a)
	}
	sort.Strings(attributes)
	for _, a := range attributes {
		v := ev.values[a]
		sort.Strings(v.Accepted)
		sort.Strings(v.Rejected)
		r.record(entity, a, observe.KindValues, *v, nil, observe.OutcomeConfirmed, ev.valuesProof[a]...)
	}
}

// finalizeRequiredWhens pairs the with/without halves: omission refused
// while presence was accepted is the requirement; both accepted is its
// absence; anything else discriminated nothing.
func (r *runner) finalizeRequiredWhens(ev *evidence) {
	keys := make([]string, 0, len(ev.requiredWhens))
	for k := range ev.requiredWhens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := ev.requiredWhens[k]
		cond := p.cond
		entity := strings.SplitN(k, "\x00", 2)[0]
		switch {
		case p.with != nil && *p.with && p.without != nil && !*p.without:
			r.record(entity, p.attribute, observe.KindRequiredWhen, true, &cond, observe.OutcomeConfirmed, p.proof...)
		case p.with != nil && *p.with && p.without != nil && *p.without:
			r.record(entity, p.attribute, observe.KindRequiredWhen, false, &cond, observe.OutcomeConfirmed, p.proof...)
		default:
			r.record(entity, p.attribute, observe.KindRequiredWhen, nil, &cond, observe.OutcomeInconclusive, p.proof...)
		}
	}
}

// finalizeUpdateStyle reduces the update tallies to one entity-level
// style claim.
func (r *runner) finalizeUpdateStyle(entity string, ev *evidence) {
	switch {
	case ev.updSucceeded == 0 && ev.updRefused > 0:
		r.record(entity, "", observe.KindUpdateStyle, "replace-only", nil, observe.OutcomeConfirmed, ev.updateProof...)
	case ev.updOmittedLost:
		r.record(entity, "", observe.KindUpdateStyle, "put-full", nil, observe.OutcomeConfirmed, ev.updateProof...)
	case ev.updSucceeded > 0 && ev.updOmittedKept:
		r.record(entity, "", observe.KindUpdateStyle, "patch-merge", nil, observe.OutcomeConfirmed, ev.updateProof...)
	case ev.updSucceeded > 0:
		r.record(entity, "", observe.KindUpdateStyle, nil, nil, observe.OutcomeInconclusive, ev.updateProof...)
	}
}

// fieldProof is the excerpt pair behind the per-field conclusions.
func fieldProof(ev *evidence) []observe.Excerpt {
	var out []observe.Excerpt
	if ev.createProof != nil {
		out = append(out, *ev.createProof)
	}
	if ev.readProof != nil {
		out = append(out, *ev.readProof)
	}
	return out
}

func sortedFieldUnion(a, b map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// equalJSON compares decoded JSON values via their encodings, so a Go
// literal int and a decoded float64 spelling the same number compare
// equal.
func equalJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA == nil && errB == nil && string(ja) == string(jb) {
		return true
	}
	return sameNumber(a, b)
}

// sameNumber reports whether two values are the same number wearing different
// JSON types — 120 and "120", or an int against the float64 a decoded body
// yields.
//
// An API that accepts "120" for an integer field and answers 120 has applied
// exactly what it was given. Compared strictly it looks like the server
// substituted a value of its own, which is recorded as serverForced, which
// makes the attribute Computed, which takes a writable field away from the
// practitioner. The strict comparison is right about the bytes and wrong about
// the API.
func sameNumber(a, b any) bool {
	af, okA := numeric(a)
	bf, okB := numeric(b)
	return okA && okB && af == bf
}

// numeric reads a value as a number, including one spelled as a string. It
// deliberately does not accept booleans: true is not 1 to any API worth
// modelling.
func numeric(v any) (float64, bool) {
	if f, ok := asFloat(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return f, err == nil
	}
	return 0, false
}

// normalisedForm reports whether got is a recognisable transform of sent
// and answers the stored form as the string a normalisation observation
// carries; the kind is what the correction compiler reads back from the
// excerpts.
func normalisedForm(sent, got any) (string, bool) {
	_, form, ok := observe.Normalisation(sent, got)
	return form, ok
}

// maskedEcho reports whether a string went in and a mask came back: the
// answer is nothing but asterisks or bullets, and is not what was sent. A
// nested object is masked when every string in it is.
func maskedEcho(sent, got any) bool {
	switch g := got.(type) {
	case string:
		if _, ok := sent.(string); !ok || g == sent {
			return false
		}
		return isMask(g)
	case map[string]any:
		s, ok := sent.(map[string]any)
		if !ok {
			return false
		}
		strings := 0
		for k, v := range g {
			text, isString := v.(string)
			if !isString {
				continue
			}
			strings++
			if !maskedEcho(s[k], text) {
				return false
			}
		}
		return strings > 0
	}
	return false
}

// isMask reports whether a string is made of mask characters alone.
func isMask(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '*' && r != '•' && r != 'x' && r != 'X' {
			return false
		}
	}
	return true
}

// composite reports whether a value is an object or a list.
func composite(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// scalarLike reports whether a value can be a serverDefault observation's
// value.
func scalarLike(v any) bool {
	switch v.(type) {
	case bool, string, float64, int, int64, json.Number:
		return true
	default:
		return false
	}
}
