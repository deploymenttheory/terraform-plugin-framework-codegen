package infer

import (
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/strategy"
)

// EdgeKinds is the set of conditional-edge observation kinds the inference
// asserts globally — the ones no single probe justifies. The run summary
// counts these as "edges" separately from the scalar and requirement findings
// the executor also reads per probe.
var EdgeKinds = map[observe.Kind]bool{
	observe.KindValidConfiguration: true,
	observe.KindValidWhen:          true,
	observe.KindDependsOn:          true,
	observe.KindMutuallyExclusive:  true,
}

// Infer turns one entity's evidence, read against its compiled strategy, into
// the conditional-edge and list-shape observations the evidence supports. It
// is pure and deterministic; the caller stamps the run identity onto what it
// returns.
//
// The rules, evidence pattern -> kind -> corroboration required:
//
//	field accepted under exactly one gate value, removed under >=1 other
//	    -> validWhen             (both directions: one positive, one negative)
//	>=2 gate values each create, and >=1 validWhen edge among them
//	    -> validConfiguration    (multiple variants, genuinely distinct)
//	"X requires Y" adjustment the API forced and the retry accepted
//	    -> dependsOn             (a completed refuse->add->accept cycle)
//	each of A,B accepted alone but A+B refused together
//	    -> mutuallyExclusive     (both valid alone, the pair refused)
//	an add the API forced with no gate
//	    -> requiredByAPI         (a completed refuse->add->accept cycle)
//	an add the API forced under one gate value
//	    -> requiredWhen          (the same, value-conditioned)
//	a collection response's structure
//	    -> listResponseShape     (read from the body, not the document)
//
// A strategy hypothesis the evidence does not confirm becomes an inconclusive
// observation, never an assertion — so a planted or mistaken edge is reported
// as untested rather than asserted.
func Infer(ev Evidence, compiled *strategy.Strategy) []observe.Observation {
	m := newModel(ev, compiled)

	var out []observe.Observation
	confirmed := map[string]bool{}

	valid := m.validWhenEdges()
	for _, o := range valid {
		confirmed[keyOf(o)] = true
	}
	out = append(out, valid...)
	out = append(out, m.validConfiguration(valid)...)

	for _, o := range m.dependsOn() {
		confirmed[keyOf(o)] = true
		out = append(out, o)
	}
	for _, o := range m.mutuallyExclusive() {
		confirmed[keyOf(o)] = true
		out = append(out, o)
	}
	for _, o := range m.requiredEdges() {
		confirmed[keyOf(o)] = true
		out = append(out, o)
	}
	out = append(out, m.listShape()...)
	out = append(out, m.hypothesisGaps(confirmed)...)

	return dedup(out)
}

// model is the per-entity picture inference reasons over, precomputed once so
// each rule reads it rather than re-walking the evidence.
type model struct {
	ev       Evidence
	compiled *strategy.Strategy
	gate     gate
	accepted map[string]map[string]bool // gate value -> accepted field set
	removed  map[string]map[string]bool // gate value -> removed field set
	created  []string                   // gate values a create succeeded under
}

func newModel(ev Evidence, compiled *strategy.Strategy) *model {
	g := gateOf(compiled)
	accepted := acceptedUnder(ev.AcceptedBodies, g.field)
	created := make([]string, 0, len(accepted))
	for v := range accepted {
		if v != "" {
			created = append(created, v)
		}
	}
	sort.Strings(created)
	return &model{
		ev:       ev,
		compiled: compiled,
		gate:     g,
		accepted: accepted,
		removed:  removedUnder(ev.Adjustments, g.field),
		created:  created,
	}
}

// candidateFields is the sorted union of every field that appeared in an
// accepted body or a removal — the fields variant diffing has anything to say
// about.
func (m *model) candidateFields() []string {
	seen := map[string]bool{}
	for _, set := range m.accepted {
		for f := range set {
			seen[f] = true
		}
	}
	for _, set := range m.removed {
		for f := range set {
			seen[f] = true
		}
	}
	delete(seen, m.gate.field)
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// validWhenEdges asserts, for each candidate field, that it is valid only when
// the gate holds one value: accepted under exactly that value and removed
// under at least one other. Both directions must be present — a positive and a
// negative — so a bare removal with no matching acceptance asserts nothing.
func (m *model) validWhenEdges() []observe.Observation {
	if m.gate.field == "" {
		return nil
	}
	var out []observe.Observation
	for _, f := range m.candidateFields() {
		home := m.acceptedValues(f)
		rem := m.removedValues(f)
		if len(home) != 1 || len(rem) == 0 {
			continue
		}
		v := home[0]
		if contains(rem, v) {
			// Accepted and removed under the same value: conflicting, not an
			// edge.
			continue
		}
		cond := &observe.Condition{Attribute: m.gate.field, Equals: v}
		prov := m.provenanceFor(f, v)
		out = append(out, m.edge(f, observe.KindValidWhen, true, cond, prov, observe.OutcomeConfirmed))
	}
	return out
}

// validConfiguration asserts the entity has several distinct valid
// configurations selected by the gate: two or more gate values each created,
// and at least one field genuinely valid under one and not another (a
// confirmed validWhen edge). Without a distinguishing field the "variants"
// would be the same object created twice, which is no configuration at all.
func (m *model) validConfiguration(validEdges []observe.Observation) []observe.Observation {
	if m.gate.field == "" || len(m.created) < 2 || len(validEdges) == 0 {
		return nil
	}
	values := make([]string, len(m.created))
	copy(values, m.created)
	sort.Strings(values)
	prov := observe.ProvenanceDerived
	if p, ok := m.variantProvenance(); ok {
		prov = p
	}
	return []observe.Observation{
		m.edgeAttr(m.gate.field, observe.KindValidConfiguration, values, nil, prov, observe.OutcomeConfirmed),
	}
}

// dependsOn asserts a co-requirement the API forced: "X requires Y", where the
// executor was refused, added Y, and the retry succeeded. Each distinct
// (trigger, required) pair yields one confirmed edge.
func (m *model) dependsOn() []observe.Observation {
	seen := map[[2]string]bool{}
	var out []observe.Observation
	for _, a := range m.ev.Adjustments {
		if a.Action != AdjustRequires || a.GateField == "" || a.Field == "" {
			continue
		}
		pair := [2]string{a.GateField, a.Field}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		prov := m.requiresProvenance(a.GateField, a.Field)
		out = append(out, m.edge(a.GateField, observe.KindDependsOn, a.Field, nil, prov, observe.OutcomeConfirmed))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attribute < out[j].Attribute })
	return out
}

// mutuallyExclusive asserts that at most one of a pair may be set: each field
// accepted alone, the pair refused together. Both directions again — a refusal
// of the pair with no evidence either is valid alone asserts nothing.
func (m *model) mutuallyExclusive() []observe.Observation {
	seen := map[[2]string]bool{}
	var out []observe.Observation
	for _, p := range m.ev.CombinedRefusals {
		a, b := p.A, p.B
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		key := [2]string{a, b}
		if seen[key] {
			continue
		}
		if !m.acceptedAlone(a, b) || !m.acceptedAlone(b, a) {
			continue
		}
		seen[key] = true
		set := []string{a, b}
		prov := m.provenanceForSet(set)
		out = append(out, m.edgeAttr("", observe.KindMutuallyExclusive, set, nil, prov, observe.OutcomeConfirmed))
	}
	return out
}

// requiredEdges asserts the requirements the adaptive loop was forced into:
// an add with no gate is unconditionally required (requiredByAPI); an add
// under one gate value is required only then (requiredWhen). Each is a
// completed refuse->add->accept cycle, corroboration enough on its own.
func (m *model) requiredEdges() []observe.Observation {
	byAPI := map[string]bool{}
	when := map[[2]string]bool{}
	for _, a := range m.ev.Adjustments {
		if a.Action != AdjustAdd || a.Field == "" {
			continue
		}
		if a.GateField == "" || a.GateValue == "" {
			byAPI[a.Field] = true
			continue
		}
		when[[2]string{a.Field, a.GateValue}] = true
	}

	var out []observe.Observation
	fields := sortedKeys(byAPI)
	for _, f := range fields {
		out = append(out, m.edge(f, observe.KindRequiredByAPI, true, nil, "", observe.OutcomeConfirmed))
	}
	pairs := make([][2]string, 0, len(when))
	for p := range when {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	for _, p := range pairs {
		gateField := m.gate.field
		if gateField == "" {
			// An add named a gate value the strategy had no gate for; keep the
			// value as the condition attribute is unknown-free by trusting the
			// executor's gate field is the model's, so skip when we cannot name
			// the gate field.
			continue
		}
		cond := &observe.Condition{Attribute: gateField, Equals: p[1]}
		out = append(out, m.edge(p[0], observe.KindRequiredWhen, true, cond, "", observe.OutcomeConfirmed))
	}
	return out
}

// listShape reports the structure of the collection responses the executor
// captured — wrapped versus bare, the key, the pagination style — read from
// the body, never from the document.
func (m *model) listShape() []observe.Observation {
	shape := listShapeOf(m.ev.ListBodies)
	if shape == nil {
		return nil
	}
	return []observe.Observation{
		m.edgeAttr("", observe.KindListResponseShape, *shape, nil, observe.ProvenanceDerived, observe.OutcomeConfirmed),
	}
}

// hypothesisGaps turns every strategy hypothesis the evidence did not confirm
// into an inconclusive observation. This is the false-edge guard: a hypothesis
// the run could not corroborate is recorded as tested-and-unconfirmed, so it
// is visible without ever becoming an assertion.
func (m *model) hypothesisGaps(confirmed map[string]bool) []observe.Observation {
	if m.compiled == nil {
		return nil
	}
	var out []observe.Observation
	seen := map[string]bool{}
	for _, h := range m.compiled.Hypotheses {
		for _, o := range m.hypothesisObservations(h) {
			k := keyOf(o)
			if confirmed[k] || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, o)
		}
	}
	return out
}

// hypothesisObservations maps one hypothesis to the inconclusive observation(s)
// it would become if unconfirmed.
func (m *model) hypothesisObservations(h strategy.Hypothesis) []observe.Observation {
	prov := observe.Provenance(h.Provenance)
	switch h.Kind {
	case strategy.HypothesisVariant, strategy.HypothesisValidWhen:
		cond := &observe.Condition{Attribute: h.GateField, Equals: h.GateValue}
		if h.GateField == "" {
			return nil
		}
		var out []observe.Observation
		for _, f := range h.Subjects {
			out = append(out, m.edge(f, observe.KindValidWhen, nil, cond, prov, observe.OutcomeInconclusive))
		}
		return out
	case strategy.HypothesisRequiredWhen:
		if h.GateField == "" || len(h.Subjects) == 0 {
			return nil
		}
		cond := &observe.Condition{Attribute: h.GateField, Equals: h.GateValue}
		return []observe.Observation{m.edge(h.Subjects[0], observe.KindRequiredWhen, nil, cond, prov, observe.OutcomeInconclusive)}
	case strategy.HypothesisRequiresField:
		subjects := append([]string(nil), h.Subjects...)
		sort.Strings(subjects)
		if len(subjects) < 2 {
			return nil
		}
		// A requiresField hypothesis names the whole co-requirement set; the
		// subject is the trigger the check field records, or the first member.
		trigger := h.Check.Field
		if trigger == "" {
			trigger = subjects[0]
		}
		return []observe.Observation{m.edge(trigger, observe.KindDependsOn, nil, nil, prov, observe.OutcomeInconclusive)}
	case strategy.HypothesisMutuallyExclusive:
		set := append([]string(nil), h.Subjects...)
		sort.Strings(set)
		if len(set) < 2 {
			return nil
		}
		return []observe.Observation{m.edgeAttr("", observe.KindMutuallyExclusive, nil, nil, prov, observe.OutcomeInconclusive)}
	default:
		return nil
	}
}

// acceptedValues lists the gate values under which field f appeared in an
// accepted body.
func (m *model) acceptedValues(f string) []string {
	var out []string
	for _, v := range m.gate.values {
		if m.accepted[v][f] {
			out = append(out, v)
		}
	}
	return out
}

// removedValues lists the gate values under which field f was removed.
func (m *model) removedValues(f string) []string {
	var out []string
	for _, v := range m.gate.values {
		if m.removed[v][f] {
			out = append(out, v)
		}
	}
	return out
}

// acceptedAlone reports whether field a appeared in an accepted body that did
// not also carry field b.
func (m *model) acceptedAlone(a, b string) bool {
	for _, body := range m.ev.AcceptedBodies {
		if _, hasA := body[a]; !hasA {
			continue
		}
		if _, hasB := body[b]; !hasB {
			return true
		}
	}
	return false
}

// provenanceFor finds the provenance of a confirmed validWhen edge from a
// matching strategy hypothesis, defaulting to derived.
func (m *model) provenanceFor(field, gateValue string) observe.Provenance {
	if m.compiled == nil {
		return observe.ProvenanceDerived
	}
	for _, h := range m.compiled.Hypotheses {
		if h.Kind != strategy.HypothesisValidWhen && h.Kind != strategy.HypothesisVariant {
			continue
		}
		if h.GateField != m.gate.field || h.GateValue != gateValue {
			continue
		}
		if contains(h.Subjects, field) {
			return observe.Provenance(h.Provenance)
		}
	}
	return observe.ProvenanceDerived
}

// variantProvenance finds the provenance of a structural variant hypothesis on
// the gate, for the validConfiguration finding.
func (m *model) variantProvenance() (observe.Provenance, bool) {
	if m.compiled == nil {
		return "", false
	}
	for _, h := range m.compiled.Hypotheses {
		if h.Kind == strategy.HypothesisVariant && h.GateField == m.gate.field {
			return observe.Provenance(h.Provenance), true
		}
	}
	return "", false
}

// requiresProvenance finds the provenance of a requiresField hypothesis
// covering the co-requirement, defaulting to derived.
func (m *model) requiresProvenance(trigger, required string) observe.Provenance {
	if m.compiled == nil {
		return observe.ProvenanceDerived
	}
	for _, h := range m.compiled.Hypotheses {
		if h.Kind != strategy.HypothesisRequiresField {
			continue
		}
		if contains(h.Subjects, trigger) && contains(h.Subjects, required) {
			return observe.Provenance(h.Provenance)
		}
	}
	return observe.ProvenanceDerived
}

// provenanceForSet finds the provenance of a hypothesis whose subjects equal
// the given set, defaulting to derived.
func (m *model) provenanceForSet(set []string) observe.Provenance {
	if m.compiled == nil {
		return observe.ProvenanceDerived
	}
	want := joinSorted(set)
	for _, h := range m.compiled.Hypotheses {
		if h.Kind == strategy.HypothesisMutuallyExclusive && joinSorted(h.Subjects) == want {
			return observe.Provenance(h.Provenance)
		}
	}
	return observe.ProvenanceDerived
}
