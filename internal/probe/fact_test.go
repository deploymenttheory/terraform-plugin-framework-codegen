package probe

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func validFact() Fact {
	return Fact{
		Resource:   "tag",
		JSONPath:   "colour",
		Field:      FactWritable,
		Value:      BoolValue(true),
		Confidence: ConfidenceObserved,
		Probe:      "write.writable-returned",
		Evidence:   []string{"004-post-tags"},
		Rationale:  "the value sent was read back unchanged",
	}
}

// TestUnit_Probe_FactValidate is the gate on the fact store's trustworthiness.
//
// Validated on load rather than only on write, because the committed facts document is
// hand-editable. A fact with no evidence, or with a field merge does not recognise, would
// otherwise flow into merge and change a schema on the strength of nothing.
func TestUnit_Probe_FactValidate(t *testing.T) {
	t.Parallel()

	if err := validFact().Validate(); err != nil {
		t.Fatalf("a well-formed fact must validate: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Fact)
		want   string
	}{
		{"no resource", func(f *Fact) { f.Resource = "" }, "no resource"},
		{"no field", func(f *Fact) { f.Field = "" }, "no field"},
		{"unknown field", func(f *Fact) { f.Field = "telepathy" }, "unknown fact field"},
		{"no value", func(f *Fact) { f.Value = Value{} }, "no value"},
		{"unknown confidence", func(f *Fact) { f.Confidence = "certain" }, "unknown confidence"},
		// The one that matters most: the whole premise of committing cassettes is that
		// every claim can be checked against the traffic that produced it.
		{"no evidence", func(f *Fact) { f.Evidence = nil }, "no evidence"},
		{"no rationale", func(f *Fact) { f.Rationale = "" }, "no rationale"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := validFact()
			tc.mutate(&f)

			err := f.Validate()
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.want)
			}
			if !errors.Is(err, ErrInvalidFacts) {
				t.Errorf("error = %v, want ErrInvalidFacts", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

// TestUnit_Probe_ConfidenceOrdering pins the rules merge depends on.
//
// Corroborated is required for Immutable=true and Writable=false, so the ordering is not
// cosmetic: getting it backwards would let a single ambiguous observation drive a
// RequiresReplace.
func TestUnit_Probe_ConfidenceOrdering(t *testing.T) {
	t.Parallel()

	if !ConfidenceCorroborated.AtLeast(ConfidenceObserved) {
		t.Error("corroborated must be at least as strong as observed")
	}
	if ConfidenceObserved.AtLeast(ConfidenceCorroborated) {
		t.Error("observed must not satisfy a corroborated floor")
	}
	if !ConfidenceObserved.AtLeast(ConfidenceInferred) {
		t.Error("observed must be stronger than inferred")
	}
	if ConfidenceSuspected.AtLeast(ConfidenceInferred) {
		t.Error("suspected must be the weakest level")
	}
	if Confidence("invented").AtLeast(ConfidenceSuspected) {
		t.Error("an unrecognised confidence must not satisfy any floor")
	}
}

func TestUnit_Probe_ValueRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"bool", BoolValue(false), "false"},
		{"text", TextValue("lowercased"), "lowercased"},
		{"list", ListValue([]string{"a", "b"}), "[a b]"},
		{"literal", LiteralValue(blueprint.Literal{Kind: blueprint.KindString, Raw: `"blue"`}), `"blue"`},
		{"empty", Value{}, "(empty)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.v.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}

	rb := ReadBackValue(blueprint.ReadBack{Enabled: true, MaxRetries: 3, IntervalMS: 250})
	if got := rb.String(); !strings.Contains(got, "retries=3") || !strings.Contains(got, "250ms") {
		t.Errorf("read-back rendering = %q", got)
	}

	// IsZero is what Validate uses to reject a fact with no value, so a false bool and a
	// zero literal must not look empty.
	if BoolValue(false).IsZero() {
		t.Error("a false bool is a value, not an absence")
	}
	if LiteralValue(blueprint.Literal{Raw: "0"}).IsZero() {
		t.Error("a zero literal is a value, not an absence")
	}
	if !(Value{}).IsZero() {
		t.Error("an unset value must be zero")
	}
}

// TestUnit_Probe_SortFactsIsStable: the facts document is committed, so its order must not
// depend on which order probes happened to run in.
func TestUnit_Probe_SortFactsIsStable(t *testing.T) {
	t.Parallel()

	facts := []Fact{
		{Resource: "tag", JSONPath: "value", Field: FactWritable},
		{Resource: "agent", JSONPath: "id", Field: FactVolatile},
		{Resource: "tag", JSONPath: "colour", Field: FactWritable},
		{Resource: "tag", JSONPath: "colour", Field: FactImmutable},
	}

	SortFacts(facts)

	got := make([]string, 0, len(facts))
	for _, f := range facts {
		got = append(got, f.Resource+"."+f.JSONPath+":"+string(f.Field))
	}

	want := []string{
		"agent.id:volatile",
		"tag.colour:immutable",
		"tag.colour:writable",
		"tag.value:writable",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order = %v, want %v", got, want)
			break
		}
	}
}

// TestUnit_Probe_SortFactsOrdersBranchFactsByPrecondition.
//
// The moment one path carries a fact per branch, resource+path+field no longer identifies a
// fact. Without the precondition tiebreak two branch facts have no defined relative order,
// and the committed document would churn between identical runs.
func TestUnit_Probe_SortFactsOrdersBranchFactsByPrecondition(t *testing.T) {
	t.Parallel()

	unconditional := validFact()
	unconditional.Field = FactReturnedOnRead

	static := unconditional
	static.When = []Condition{{JSONPath: "type", Equals: "static"}}

	dynamic := unconditional
	dynamic.When = []Condition{{JSONPath: "type", Equals: "dynamic"}}

	oneOrder := []Fact{static, dynamic, unconditional}
	otherOrder := []Fact{dynamic, unconditional, static}

	SortFacts(oneOrder)
	SortFacts(otherOrder)

	for i := range oneOrder {
		if oneOrder[i].whenKey() != otherOrder[i].whenKey() {
			t.Fatalf("arrival order leaked into the sorted order: %v vs %v",
				oneOrder[i], otherOrder[i])
		}
	}

	if oneOrder[0].Conditional() {
		t.Errorf("the unconditional fact sorts first, got %v", oneOrder[0])
	}

	// The key must not depend on the order a probe appended conditions in.
	a := validFact()
	a.When = []Condition{{JSONPath: "type", Equals: "dynamic"}, {JSONPath: "scope", Equals: "custom"}}
	b := validFact()
	b.When = []Condition{{JSONPath: "scope", Equals: "custom"}, {JSONPath: "type", Equals: "dynamic"}}

	if a.whenKey() != b.whenKey() {
		t.Errorf("condition order changed the key: %q vs %q", a.whenKey(), b.whenKey())
	}
}

// TestUnit_Probe_ValidateShapeChecksPreconditionsAtAnyConfidence.
//
// A Suspected fact is exempt from the strength checks -- it is a prompt for a human, and may
// lack evidence -- but its shape is not negotiable: a malformed precondition on one would
// load unchecked and sit in the store as a claim nothing downstream can interpret.
func TestUnit_Probe_ValidateShapeChecksPreconditionsAtAnyConfidence(t *testing.T) {
	t.Parallel()

	f := validFact()
	f.Confidence = ConfidenceSuspected
	f.Evidence = nil
	f.Rationale = ""

	if err := f.ValidateShape(); err != nil {
		t.Fatalf("a suspected fact may lack evidence and rationale: %v", err)
	}
	if err := f.Validate(); err == nil {
		t.Fatal("the full Validate must still demand evidence")
	}

	f.When = []Condition{{JSONPath: "type", Equals: ""}}
	if err := f.ValidateShape(); err == nil || !strings.Contains(err.Error(), "no value") {
		t.Errorf("a condition with no value must be refused by shape validation: %v", err)
	}
}

func TestUnit_Probe_FactString(t *testing.T) {
	t.Parallel()

	got := validFact().String()
	for _, want := range []string{"tag.colour", "writable", "true", "observed", "write.writable-returned"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}

	// A resource-level fact has no JSON path and must not render a stray dot.
	resourceLevel := validFact()
	resourceLevel.JSONPath = ""
	if strings.Contains(resourceLevel.String(), "tag.:") {
		t.Errorf("a resource-level fact rendered a stray separator: %q", resourceLevel.String())
	}
}

func TestUnit_Probe_NoteString(t *testing.T) {
	t.Parallel()

	full := Note{Resource: "tag", JSONPath: "colour", Probe: "write.server-default", Message: "abandoned"}
	got := full.String()
	for _, want := range []string{"tag.colour", "write.server-default", "abandoned"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}

	bare := Note{Resource: "tag", Message: "no fixtures"}
	if got := bare.String(); got != "tag: no fixtures" {
		t.Errorf("String() = %q", got)
	}
}
