package run

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
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
	// update-style tallies across the updateField steps.
	updSucceeded   int
	updRefused     int
	updOmittedKept bool
	updOmittedLost bool
	updProof       []observe.Excerpt
	// proof excerpts for the finalize-time observations.
	createProof *observe.Excerpt
	readProof   *observe.Excerpt
}

// requiredWhenPair tracks the with/without halves of one required-when
// check: created with the attribute present under the pinned condition,
// and with it omitted.
type requiredWhenPair struct {
	attr    string
	cond    observe.Condition
	with    *bool
	without *bool
	proof   []observe.Excerpt
}

func newEvidence() *evidence {
	return &evidence{
		volatile:      map[string]bool{},
		omitted:       map[string][]any{},
		values:        map[string]*observe.Values{},
		valuesProof:   map[string][]observe.Excerpt{},
		requiredWhens: map[string]*requiredWhenPair{},
	}
}

// observeOmittedSamples records what a create's response answered for
// every field its body did not send.
func (r *runner) observeOmittedSamples(ent *entityState, sent, got map[string]any) {
	if ent == nil || got == nil {
		return
	}
	if ent.ev.idField == "" {
		if v, ok := got["id"]; ok && v != nil {
			ent.ev.idField = "id"
		}
	}
	for k, v := range got {
		if _, wasSent := sent[k]; wasSent || v == nil {
			continue
		}
		ent.ev.omitted[k] = append(ent.ev.omitted[k], v)
	}
}

// valuesFor is the per-attribute accumulator.
func (ev *evidence) valuesFor(attr string) *observe.Values {
	v, ok := ev.values[attr]
	if !ok {
		v = &observe.Values{}
		ev.values[attr] = v
	}
	return v
}

// finalizeEvidence turns an entity's accumulated evidence into
// observations. Per-field conclusions iterate sorted names so two runs
// over the same behaviour emit identically ordered findings.
func (r *runner) finalizeEvidence(ent *entityState) {
	ev := ent.ev
	entity := ent.plan.Entity

	if ev.sent != nil && ev.got != nil {
		r.finalizeFields(entity, ev, ev.sent, ev.got, true)
	}
	if ev.maximalSent != nil && ev.maximalGot != nil {
		// The maximal pair covers only what the minimal comparison could
		// not: optional fields. Dedup keeps the minimal finding when both
		// speak.
		r.finalizeFields(entity, ev, ev.maximalSent, ev.maximalGot, false)
	}

	r.finalizeValues(entity, ev)
	r.finalizeRequiredWhens(ev)
	r.finalizeUpdateStyle(entity, ev)
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
		case wasSent && present:
			if norm, ok := normalisedForm(sentV, gotV); ok {
				r.record(entity, f, observe.KindNormalisation, norm, nil, observe.OutcomeConfirmed, proof...)
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
	attrs := make([]string, 0, len(ev.values))
	for a := range ev.values {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)
	for _, a := range attrs {
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
			r.record(entity, p.attr, observe.KindRequiredWhen, true, &cond, observe.OutcomeConfirmed, p.proof...)
		case p.with != nil && *p.with && p.without != nil && *p.without:
			r.record(entity, p.attr, observe.KindRequiredWhen, false, &cond, observe.OutcomeConfirmed, p.proof...)
		default:
			r.record(entity, p.attr, observe.KindRequiredWhen, nil, &cond, observe.OutcomeInconclusive, p.proof...)
		}
	}
}

// finalizeUpdateStyle reduces the update tallies to one entity-level
// style claim.
func (r *runner) finalizeUpdateStyle(entity string, ev *evidence) {
	switch {
	case ev.updSucceeded == 0 && ev.updRefused > 0:
		r.record(entity, "", observe.KindUpdateStyle, "replace-only", nil, observe.OutcomeConfirmed, ev.updProof...)
	case ev.updOmittedLost:
		r.record(entity, "", observe.KindUpdateStyle, "put-full", nil, observe.OutcomeConfirmed, ev.updProof...)
	case ev.updSucceeded > 0 && ev.updOmittedKept:
		r.record(entity, "", observe.KindUpdateStyle, "patch-merge", nil, observe.OutcomeConfirmed, ev.updProof...)
	case ev.updSucceeded > 0:
		r.record(entity, "", observe.KindUpdateStyle, nil, nil, observe.OutcomeInconclusive, ev.updProof...)
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
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// normalisedForm reports whether got is a recognisable transform of sent
// — case-folded, trimmed, or a re-sorted list — and returns the stored
// form as the string a normalisation observation carries.
func normalisedForm(sent, got any) (string, bool) {
	if ss, ok := sent.(string); ok {
		gs, ok := got.(string)
		if !ok {
			return "", false
		}
		for _, candidate := range []string{
			strings.ToLower(ss), strings.TrimSpace(ss), strings.ToLower(strings.TrimSpace(ss)), strings.ToUpper(ss),
		} {
			if gs == candidate && gs != ss {
				return gs, true
			}
		}
		return "", false
	}
	sentList, okS := sent.([]any)
	gotList, okG := got.([]any)
	if okS && okG && len(sentList) == len(gotList) {
		sorted := make([]any, len(sentList))
		copy(sorted, sentList)
		sort.Slice(sorted, func(i, j int) bool { return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j]) })
		if equalJSON(sorted, gotList) && !equalJSON(sentList, gotList) {
			raw, _ := json.Marshal(gotList)
			return string(raw), true
		}
	}
	return "", false
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
