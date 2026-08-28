package infer

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// monitorStrategy is a discriminated resource: a kind gate over ping/web/dns,
// no declared hypotheses (the flat quirk-server monitor), so every edge it
// yields is derived from probing.
func monitorStrategy() *strategy.Strategy {
	return &strategy.Strategy{
		Entity: "monitor",
		Variants: []strategy.Variant{
			{},
			{GateField: "kind", GateValue: "ping"},
			{GateField: "kind", GateValue: "web"},
			{GateField: "kind", GateValue: "dns"},
		},
	}
}

// monitorEvidence is a complete, convergent picture: each variant created, the
// wrong-variant fields removed under the others, the interval add forced, the
// dnssec/domain co-requirement forced, and a wrapped list response.
func monitorEvidence() Evidence {
	adj := func(a AdjustAction, field, gf, gv string) RequestAdjustment {
		return RequestAdjustment{Entity: "monitor", Action: a, Field: field, GateField: gf, GateValue: gv}
	}
	return Evidence{
		Entity: "monitor",
		AcceptedRequestBodies: []map[string]any{
			{"kind": "ping", "interval": 5.0, "target_host": "h", "name": "n"},
			{"kind": "web", "interval": 5.0, "web": map[string]any{"url": "u"}, "name": "n"},
			{"kind": "dns", "interval": 5.0, "domain": "d", "dnssec": true, "name": "n"},
		},
		Adjustments: []RequestAdjustment{
			adj(AdjustAdd, "interval", "", ""),
			adj(AdjustAdd, "target_host", "kind", "ping"),
			adj(AdjustRemove, "target_host", "kind", "web"),
			adj(AdjustRemove, "target_host", "kind", "dns"),
			adj(AdjustRemove, "domain", "kind", "ping"),
			adj(AdjustRemove, "domain", "kind", "web"),
			adj(AdjustRemove, "web", "kind", "ping"),
			adj(AdjustRemove, "web", "kind", "dns"),
			adj(AdjustRemove, "dnssec", "kind", "ping"),
			adj(AdjustRemove, "dnssec", "kind", "web"),
			adj(AdjustRequires, "domain", "dnssec", ""),
		},
		ListBodies: [][]byte{[]byte(`{"monitors":[{"id":"1"}]}`)},
	}
}

func find(obs []observe.Observation, attribute string, kind observe.Kind) *observe.Observation {
	for i := range obs {
		if obs[i].Attribute == attribute && obs[i].Kind == kind {
			return &obs[i]
		}
	}
	return nil
}

func findWhen(obs []observe.Observation, attribute string, kind observe.Kind, gateVal string) *observe.Observation {
	for i := range obs {
		o := &obs[i]
		if o.Attribute == attribute && o.Kind == kind && o.Condition != nil && o.Condition.Equals == gateVal {
			return o
		}
	}
	return nil
}

// TestUnit_Infer_MonitorConvergentEdges asserts the whole monitor picture:
// validConfiguration over the gate, a validWhen per gated field, the
// dnssec->domain dependsOn, the interval requiredByAPI, and the list shape —
// every one confirmed, and every one passing observe.Validate.
func TestUnit_Infer_MonitorConvergentEdges(t *testing.T) {
	t.Parallel()
	obs := Infer(monitorEvidence(), monitorStrategy())

	for i := range obs {
		if err := obs[i].Validate(); err != nil {
			t.Fatalf("inferred observation is invalid: %v (%+v)", err, obs[i])
		}
	}

	vc := find(obs, "kind", observe.KindValidConfiguration)
	if vc == nil || vc.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("validConfiguration(kind) = %+v, want confirmed", vc)
	}
	got, _ := json.Marshal(vc.Value)
	if string(got) != `["dns","ping","web"]` {
		t.Errorf("validConfiguration values = %s, want the three gate values", got)
	}

	for _, testCase := range []struct{ field, gate string }{
		{"target_host", "ping"}, {"web", "web"}, {"domain", "dns"}, {"dnssec", "dns"},
	} {
		o := findWhen(obs, testCase.field, observe.KindValidWhen, testCase.gate)
		if o == nil || o.Outcome != observe.OutcomeConfirmed || o.Value != true {
			t.Errorf("validWhen(%s, kind=%s) = %+v, want confirmed true", testCase.field, testCase.gate, o)
		}
		if o != nil && o.Condition.Attribute != "kind" {
			t.Errorf("validWhen(%s) condition attribute = %q, want kind", testCase.field, o.Condition.Attribute)
		}
		if o != nil && o.Provenance != observe.ProvenanceDerived {
			t.Errorf("validWhen(%s) provenance = %q, want derived", testCase.field, o.Provenance)
		}
	}

	dep := find(obs, "dnssec", observe.KindDependsOn)
	if dep == nil || dep.Outcome != observe.OutcomeConfirmed || dep.Value != "domain" {
		t.Fatalf("dependsOn(dnssec) = %+v, want confirmed value domain", dep)
	}

	request := find(obs, "interval", observe.KindRequiredByAPI)
	if request == nil || request.Value != true {
		t.Errorf("requiredByAPI(interval) = %+v, want true", request)
	}
	rw := findWhen(obs, "target_host", observe.KindRequiredWhen, "ping")
	if rw == nil || rw.Value != true {
		t.Errorf("requiredWhen(target_host, kind=ping) = %+v, want true", rw)
	}

	ls := find(obs, "", observe.KindListResponseShape)
	if ls == nil || ls.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("listResponseShape = %+v, want confirmed", ls)
	}
}

// TestUnit_Infer_Deterministic: the same evidence yields DeepEqual
// observations and byte-identical JSON, twice.
func TestUnit_Infer_Deterministic(t *testing.T) {
	t.Parallel()
	a := Infer(monitorEvidence(), monitorStrategy())
	b := Infer(monitorEvidence(), monitorStrategy())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two inferences over the same evidence differ")
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("two inferences produced different JSON")
	}
}

// TestUnit_Infer_NoExcerpts: an inferred edge carries no excerpt — its proof
// is convergence, not one request — so redaction has nothing to remove.
func TestUnit_Infer_NoExcerpts(t *testing.T) {
	t.Parallel()
	for _, o := range Infer(monitorEvidence(), monitorStrategy()) {
		if len(o.Excerpts) != 0 {
			t.Errorf("%s.%s carries %d excerpt(s); inferred edges must carry none", o.Entity, o.Kind, len(o.Excerpts))
		}
	}
}

// TestUnit_Infer_PlantedFalseEdgeIsInconclusive: a prose hypothesis the live
// evidence contradicts (the field is valid under two gate values, not one)
// comes out inconclusive, never confirmed.
func TestUnit_Infer_PlantedFalseEdgeIsInconclusive(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity: "monitor",
		Variants: []strategy.Variant{
			{}, {GateField: "kind", GateValue: "ping"}, {GateField: "kind", GateValue: "dns"},
		},
		Hypotheses: []strategy.Hypothesis{{
			Kind: strategy.HypothesisValidWhen, Subjects: []string{"foo"},
			GateField: "kind", GateValue: "ping", Provenance: strategy.ProvenanceProse,
		}},
	}
	ev := Evidence{
		Entity: "monitor",
		AcceptedRequestBodies: []map[string]any{
			{"kind": "ping", "foo": "x"},
			{"kind": "dns", "foo": "y"}, // foo accepted under BOTH values: not gated
		},
	}
	obs := Infer(ev, compiled)
	o := findWhen(obs, "foo", observe.KindValidWhen, "ping")
	if o == nil {
		t.Fatal("the unconfirmed hypothesis produced no observation; it must surface as inconclusive")
	}
	if o.Outcome != observe.OutcomeInconclusive {
		t.Fatalf("planted false edge outcome = %s, want inconclusive (never confirmed)", o.Outcome)
	}
	if o.Provenance != observe.ProvenanceProse {
		t.Errorf("provenance = %q, want prose (carried from the hypothesis)", o.Provenance)
	}
}

// TestUnit_Infer_LoneAmbiguousRemovalAssertsNothing: a single removal with no
// matching acceptance and no hypothesis creates no edge at all.
func TestUnit_Infer_LoneAmbiguousRemovalAssertsNothing(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "monitor",
		Variants: []strategy.Variant{{}, {GateField: "kind", GateValue: "ping"}, {GateField: "kind", GateValue: "dns"}},
	}
	ev := Evidence{
		Entity:                "monitor",
		AcceptedRequestBodies: []map[string]any{{"kind": "ping"}},
		Adjustments: []RequestAdjustment{
			{Entity: "monitor", Action: AdjustRemove, Field: "bar", GateField: "kind", GateValue: "dns"},
		},
	}
	if o := find(Infer(ev, compiled), "bar", observe.KindValidWhen); o != nil {
		t.Fatalf("a lone ambiguous removal asserted %+v; it must assert nothing", o)
	}
}

// TestUnit_Infer_ConflictingAcceptanceAndRemovalIsNoEdge: a field accepted and
// removed under the same gate value is contradictory and asserts nothing.
func TestUnit_Infer_ConflictingAcceptanceAndRemovalIsNoEdge(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "e",
		Variants: []strategy.Variant{{}, {GateField: "kind", GateValue: "a"}, {GateField: "kind", GateValue: "b"}},
	}
	ev := Evidence{
		Entity:                "e",
		AcceptedRequestBodies: []map[string]any{{"kind": "a", "foo": 1.0}},
		Adjustments: []RequestAdjustment{
			{Entity: "e", Action: AdjustRemove, Field: "foo", GateField: "kind", GateValue: "a"},
		},
	}
	if o := find(Infer(ev, compiled), "foo", observe.KindValidWhen); o != nil {
		t.Fatalf("conflicting evidence asserted %+v; it must not", o)
	}
}

// TestUnit_Infer_MutuallyExclusive: each field valid alone, the pair refused
// together, yields a confirmed mutuallyExclusive over the set.
func TestUnit_Infer_MutuallyExclusive(t *testing.T) {
	t.Parallel()
	ev := Evidence{
		Entity: "widget",
		AcceptedRequestBodies: []map[string]any{
			{"a": 1.0, "name": "n"},
			{"b": 2.0, "name": "n"},
		},
		CombinedRefusals: []FieldPair{{A: "b", B: "a"}},
	}
	obs := Infer(ev, &strategy.Strategy{Entity: "widget"})
	o := find(obs, "", observe.KindMutuallyExclusive)
	if o == nil || o.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("mutuallyExclusive = %+v, want confirmed", o)
	}
	got, _ := json.Marshal(o.Value)
	if string(got) != `["a","b"]` {
		t.Errorf("mutuallyExclusive set = %s, want sorted [a b]", got)
	}
}

// TestUnit_Infer_MutuallyExclusiveNeedsBothAlone: the pair refused together but
// one field never seen alone asserts nothing.
func TestUnit_Infer_MutuallyExclusiveNeedsBothAlone(t *testing.T) {
	t.Parallel()
	ev := Evidence{
		Entity:                "widget",
		AcceptedRequestBodies: []map[string]any{{"a": 1.0, "b": 2.0}}, // never a without b
		CombinedRefusals:      []FieldPair{{A: "a", B: "b"}},
	}
	if o := find(Infer(ev, &strategy.Strategy{Entity: "widget"}), "", observe.KindMutuallyExclusive); o != nil {
		t.Fatalf("asserted %+v without evidence each is valid alone", o)
	}
}

// TestUnit_Infer_FlatResourceHasNoVariantEdges: a strategy with no gate yields
// no validConfiguration or validWhen — the assignment case, no spurious edges.
func TestUnit_Infer_FlatResourceHasNoVariantEdges(t *testing.T) {
	t.Parallel()
	ev := Evidence{
		Entity:                "assignment",
		AcceptedRequestBodies: []map[string]any{{"name": "n", "agent_id": "agent-1"}},
		Adjustments: []RequestAdjustment{
			{Entity: "assignment", Action: AdjustBorrow, Field: "agent_id", GateField: "agent"},
		},
		ListBodies: [][]byte{[]byte(`{"assignments":[]}`)},
	}
	obs := Infer(ev, &strategy.Strategy{Entity: "assignment"})
	for _, o := range obs {
		if EdgeKinds[o.Kind] {
			t.Errorf("flat resource produced a spurious edge: %s.%s", o.Attribute, o.Kind)
		}
	}
	if find(obs, "", observe.KindListResponseShape) == nil {
		t.Error("the list shape should still be inferred for a flat resource")
	}
}

// TestUnit_Infer_ValidConfigurationNeedsADistinguishingField: two variants that
// created but with no field valid under one and not another are the same object
// twice, not a configuration.
func TestUnit_Infer_ValidConfigurationNeedsADistinguishingField(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "e",
		Variants: []strategy.Variant{{}, {GateField: "kind", GateValue: "a"}, {GateField: "kind", GateValue: "b"}},
	}
	ev := Evidence{
		Entity: "e",
		AcceptedRequestBodies: []map[string]any{
			{"kind": "a", "name": "n"},
			{"kind": "b", "name": "n"},
		},
	}
	if o := find(Infer(ev, compiled), "kind", observe.KindValidConfiguration); o != nil {
		t.Fatalf("validConfiguration asserted %+v with no distinguishing field", o)
	}
}

// TestUnit_Infer_HypothesisProvenanceIsCarried: a structural variant hypothesis
// that the evidence confirms lends its provenance to the validWhen edge.
func TestUnit_Infer_HypothesisProvenanceIsCarried(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "e",
		Variants: []strategy.Variant{{}, {GateField: "kind", GateValue: "a"}, {GateField: "kind", GateValue: "b"}},
		Hypotheses: []strategy.Hypothesis{
			{Kind: strategy.HypothesisVariant, Subjects: []string{"foo"}, GateField: "kind", GateValue: "a", Provenance: strategy.ProvenanceStructural},
		},
	}
	ev := Evidence{
		Entity:                "e",
		AcceptedRequestBodies: []map[string]any{{"kind": "a", "foo": 1.0}, {"kind": "b"}},
		Adjustments: []RequestAdjustment{
			{Entity: "e", Action: AdjustRemove, Field: "foo", GateField: "kind", GateValue: "b"},
		},
	}
	o := findWhen(Infer(ev, compiled), "foo", observe.KindValidWhen, "a")
	if o == nil || o.Provenance != observe.ProvenanceStructural {
		t.Fatalf("validWhen(foo) = %+v, want structural provenance from the hypothesis", o)
	}
	vc := find(Infer(ev, compiled), "kind", observe.KindValidConfiguration)
	if vc == nil || vc.Provenance != observe.ProvenanceStructural {
		t.Errorf("validConfiguration provenance = %v, want structural from the variant hypothesis", vc)
	}
}

// TestUnit_Infer_DependsOnProvenanceFromHypothesis: a requiresField hypothesis
// covering the pair lends its provenance to the dependsOn edge.
func TestUnit_Infer_DependsOnProvenanceFromHypothesis(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity: "e",
		Hypotheses: []strategy.Hypothesis{{
			Kind: strategy.HypothesisRequiresField, Subjects: []string{"x", "y"},
			Provenance: strategy.ProvenanceStructural,
		}},
	}
	ev := Evidence{
		Entity:      "e",
		Adjustments: []RequestAdjustment{{Entity: "e", Action: AdjustRequires, Field: "y", GateField: "x"}},
	}
	o := find(Infer(ev, compiled), "x", observe.KindDependsOn)
	if o == nil || o.Value != "y" || o.Provenance != observe.ProvenanceStructural {
		t.Fatalf("dependsOn(x) = %+v, want value y with structural provenance", o)
	}
}

// TestUnit_Infer_UnconfirmedHypothesesSurfaceInconclusive covers each
// hypothesis kind's gap mapping: none is confirmed by the empty evidence, so
// each becomes exactly one inconclusive observation of the right kind.
func TestUnit_Infer_UnconfirmedHypothesesSurfaceInconclusive(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity: "e",
		Variants: []strategy.Variant{
			{}, {GateField: "g", GateValue: "v"},
		},
		Hypotheses: []strategy.Hypothesis{
			{Kind: strategy.HypothesisRequiredWhen, Subjects: []string{"a"}, GateField: "g", GateValue: "v", Provenance: strategy.ProvenanceProse},
			{Kind: strategy.HypothesisRequiresField, Subjects: []string{"c", "d"}, Provenance: strategy.ProvenanceStructural, Check: strategy.Check{Field: "c"}},
			{Kind: strategy.HypothesisMutuallyExclusive, Subjects: []string{"e", "f"}, Provenance: strategy.ProvenanceProse},
			{Kind: strategy.HypothesisValidWhen, Subjects: []string{"h"}, GateField: "g", GateValue: "v", Provenance: strategy.ProvenanceProse},
		},
	}
	obs := Infer(Evidence{Entity: "e"}, compiled)
	want := []struct {
		attribute string
		kind      observe.Kind
	}{
		{"a", observe.KindRequiredWhen},
		{"c", observe.KindDependsOn},
		{"", observe.KindMutuallyExclusive},
		{"h", observe.KindValidWhen},
	}
	for _, w := range want {
		o := find(obs, w.attribute, w.kind)
		if o == nil || o.Outcome != observe.OutcomeInconclusive {
			t.Errorf("%s.%s = %+v, want inconclusive", w.attribute, w.kind, o)
		}
	}
}

// TestUnit_Infer_ListShapeVariants covers every list-shape reading: bare array,
// each pagination style, and an ambiguous envelope that yields nothing.
func TestUnit_Infer_ListShapeVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want *observe.ListResponseShape
	}{
		{"bare", `[{"id":"1"}]`, &observe.ListResponseShape{Envelope: "bare", Pagination: "none"}},
		{"wrapped-preferred", `{"data":[],"total":3}`, &observe.ListResponseShape{Envelope: "wrapped", Key: "data", Pagination: "none"}},
		{"cursor", `{"items":[],"next_cursor":"z"}`, &observe.ListResponseShape{Envelope: "wrapped", Key: "items", Pagination: "cursor"}},
		{"offset", `{"results":[],"offset":10}`, &observe.ListResponseShape{Envelope: "wrapped", Key: "results", Pagination: "offset"}},
		{"page", `{"monitors":[],"page":2}`, &observe.ListResponseShape{Envelope: "wrapped", Key: "monitors", Pagination: "page"}},
		{"sole-key", `{"widgets":[{"id":"1"}]}`, &observe.ListResponseShape{Envelope: "wrapped", Key: "widgets", Pagination: "none"}},
		{"ambiguous", `{"a":[],"b":[]}`, nil},
		{"not-a-list", `{"id":"1"}`, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := listShapeOf([][]byte{[]byte(testCase.body)})
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("listShapeOf(%s) = %+v, want %+v", testCase.body, got, testCase.want)
			}
		})
	}
}

// TestUnit_Infer_NilStrategyIsSafe: inference over a nil strategy makes no
// variant edges and does not panic, still reporting the list shape.
func TestUnit_Infer_NilStrategyIsSafe(t *testing.T) {
	t.Parallel()
	ev := Evidence{Entity: "e", ListBodies: [][]byte{[]byte(`[]`)}}
	obs := Infer(ev, nil)
	for _, o := range obs {
		if EdgeKinds[o.Kind] {
			t.Errorf("nil strategy produced an edge: %s", o.Kind)
		}
	}
}

// TestUnit_Infer_ValueConditionalConfiguration confirms a validConfiguration
// learned by value-cycling: a sibling value (mode=batch) accepted under one gate
// value and refused under another, with two gate values each created. The
// distinguishing signal is a value difference, not a field-presence difference,
// which is what makes this rule distinct from the field-set validConfiguration.
func TestUnit_Infer_ValueConditionalConfiguration(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "stream",
		Gates:    []strategy.Gate{{Field: "format", Kind: strategy.GateRequiredEnum, Values: []any{"avro", "json"}}},
		Variants: []strategy.Variant{{}, {GateField: "format", GateValue: "avro"}, {GateField: "format", GateValue: "json"}},
	}
	ev := Evidence{
		Entity: "stream",
		AcceptedRequestBodies: []map[string]any{
			{"format": "avro", "mode": "streaming", "name": "n"},
			{"format": "json", "mode": "batch", "name": "n"},
		},
		ConditionalValues: []ConditionalValue{
			{GateField: "format", GateValue: "avro", Field: "mode", Value: "batch", Accepted: false},
			{GateField: "format", GateValue: "avro", Field: "mode", Value: "streaming", Accepted: true},
			{GateField: "format", GateValue: "json", Field: "mode", Value: "batch", Accepted: true},
		},
	}
	o := find(Infer(ev, compiled), "format", observe.KindValidConfiguration)
	if o == nil || o.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("validConfiguration(format) = %+v, want confirmed", o)
	}
	got, _ := json.Marshal(o.Value)
	if string(got) != `["avro","json"]` {
		t.Errorf("validConfiguration values = %s, want [avro json]", got)
	}
}

// TestUnit_Infer_ValueConditionalNeedsBothDirections: a sibling value only ever
// refused is one-directional and asserts nothing, even with two gate values
// created — the conservative discipline the whole package holds to.
func TestUnit_Infer_ValueConditionalNeedsBothDirections(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "stream",
		Gates:    []strategy.Gate{{Field: "format", Values: []any{"avro", "json"}}},
		Variants: []strategy.Variant{{}, {GateField: "format", GateValue: "avro"}, {GateField: "format", GateValue: "json"}},
	}
	ev := Evidence{
		Entity: "stream",
		AcceptedRequestBodies: []map[string]any{
			{"format": "avro", "mode": "streaming", "name": "n"},
			{"format": "json", "mode": "streaming", "name": "n"},
		},
		ConditionalValues: []ConditionalValue{
			{GateField: "format", GateValue: "avro", Field: "mode", Value: "batch", Accepted: false},
			{GateField: "format", GateValue: "json", Field: "mode", Value: "batch", Accepted: false},
		},
	}
	if o := find(Infer(ev, compiled), "format", observe.KindValidConfiguration); o != nil {
		t.Fatalf("validConfiguration asserted %+v from one-directional evidence", o)
	}
}

// TestUnit_Infer_ValueConditionalNeedsTwoCreatedValues: both-direction evidence
// for a sibling value but only one gate value ever created is not two distinct
// configurations, so nothing is asserted.
func TestUnit_Infer_ValueConditionalNeedsTwoCreatedValues(t *testing.T) {
	t.Parallel()
	compiled := &strategy.Strategy{
		Entity:   "stream",
		Gates:    []strategy.Gate{{Field: "format", Values: []any{"avro", "json"}}},
		Variants: []strategy.Variant{{}, {GateField: "format", GateValue: "avro"}, {GateField: "format", GateValue: "json"}},
	}
	ev := Evidence{
		Entity:                "stream",
		AcceptedRequestBodies: []map[string]any{{"format": "avro", "mode": "streaming", "name": "n"}},
		ConditionalValues: []ConditionalValue{
			{GateField: "format", GateValue: "avro", Field: "mode", Value: "batch", Accepted: false},
			{GateField: "format", GateValue: "json", Field: "mode", Value: "batch", Accepted: true},
		},
	}
	if o := find(Infer(ev, compiled), "format", observe.KindValidConfiguration); o != nil {
		t.Fatalf("validConfiguration asserted %+v from a single created gate value", o)
	}
}
