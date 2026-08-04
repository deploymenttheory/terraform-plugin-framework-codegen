package probe

import (
	"strings"
	"testing"
)

// att builds one omission attempt: a status and the gate values that held for it.
func att(status int, gates map[string]string) omission {
	var cs []Condition
	for path, v := range gates {
		cs = append(cs, Condition{JSONPath: path, Equals: v})
	}

	return omission{
		fixture:  "f",
		omitted:  "matchType",
		status:   status,
		named:    status >= 400,
		evidence: "interaction-1",
		gates:    cs,
	}
}

// TestUnit_Probe_ADisagreementAttributedToOneGateBecomesTwoFacts.
//
// This is what phase 7.1 exists for. requiredByAPI already detected the shape -- a field one
// fixture may omit and another may not -- and deliberately downgraded it to a note, because
// "conditional on something else in the body" was all a fact could say. With a precondition it can
// name the something.
//
// Two facts rather than one, each under its own condition, because two branches producing different
// observations were separately measured and collapsing them would hide that.
func TestUnit_Probe_ADisagreementAttributedToOneGateBecomesTwoFacts(t *testing.T) {
	t.Parallel()

	sc := UnplannedScope(Subject{Resource: "tag"})

	rounds := []omission{
		att(201, map[string]string{"objectType": "test"}),
		att(400, map[string]string{"objectType": "endpoint-agent"}),
	}

	facts := requiredByAPI{}.conditionalRequiredFacts(sc, "matchType", rounds)
	if len(facts) != 2 {
		t.Fatalf("want one fact per branch, got %d", len(facts))
	}

	byCondition := map[string]Fact{}
	for _, f := range facts {
		if len(f.When) != 1 {
			t.Fatalf("each fact needs exactly one condition, got %v", f.When)
		}
		byCondition[f.When[0].Equals] = f
	}

	// Omitting it succeeded on a static tag, so it is not required there.
	static, ok := byCondition["test"]
	if !ok {
		t.Fatal("no fact conditioned on objectType=test")
	}
	if static.Value.Bool == nil || *static.Value.Bool {
		t.Errorf("omission succeeded, so requiredByApi should be false: %v", static.Value)
	}

	// And it was refused on an endpoint-agent tag, so it is required there.
	dynamic, ok := byCondition["endpoint-agent"]
	if !ok {
		t.Fatal("no fact conditioned on objectType=endpoint-agent")
	}
	if dynamic.Value.Bool == nil || !*dynamic.Value.Bool {
		t.Errorf("omission was refused, so requiredByApi should be true: %v", dynamic.Value)
	}

	// The condition has to survive into the rationale, which is what merge writes into the
	// attribute's description. A conditional fact whose prose reads as unconditional is the
	// original bug wearing a new coat.
	if !strings.Contains(dynamic.Rationale, "objectType") {
		t.Errorf("the rationale should name the condition: %q", dynamic.Rationale)
	}
	if !strings.Contains(dynamic.Because(), `objectType is "endpoint-agent"`) {
		t.Errorf("Because() should render the condition: %q", dynamic.Because())
	}
}

// TestUnit_Probe_ADisagreementNothingExplainsStaysANote.
//
// The important half. "Conditional on something" is not a fact, and inventing a condition to
// promote it would be worse than the note ever was -- a precondition nobody measured, carrying the
// authority of one that was.
func TestUnit_Probe_ADisagreementNothingExplainsStaysANote(t *testing.T) {
	t.Parallel()

	sc := UnplannedScope(Subject{Resource: "tag"})

	for _, tc := range []struct {
		name   string
		rounds []omission
	}{
		{
			// No gate was recorded at all, so nothing can be attributed.
			name: "no candidate gate",
			rounds: []omission{
				att(201, nil),
				att(400, nil),
			},
		},
		{
			// The gate held the same value on both sides, so it explains nothing.
			name: "a gate that does not vary",
			rounds: []omission{
				att(201, map[string]string{"objectType": "test"}),
				att(400, map[string]string{"objectType": "test"}),
			},
		},
		{
			// The gate varies, but one value appears on both sides -- so it cannot be what
			// decides, and correlating on it would be coincidence dressed as evidence.
			name: "a gate whose values overlap the outcomes",
			rounds: []omission{
				att(201, map[string]string{"objectType": "test"}),
				att(400, map[string]string{"objectType": "test"}),
				att(400, map[string]string{"objectType": "endpoint-agent"}),
			},
		},
		{
			// Two gates both separate the outcomes perfectly, so the evidence cannot say which
			// one decides. Picking either would be a guess presented as a measurement.
			name: "two gates both separate the outcomes",
			rounds: []omission{
				att(201, map[string]string{"objectType": "test", "accessType": "all"}),
				att(400, map[string]string{"objectType": "endpoint-agent", "accessType": "partner"}),
			},
		},
		{
			// A gate absent from one attempt cannot partition the set.
			name: "a gate missing from one attempt",
			rounds: []omission{
				att(201, map[string]string{"objectType": "test"}),
				att(400, nil),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := (requiredByAPI{}).conditionalRequiredFacts(sc, "matchType", tc.rounds); got != nil {
				t.Errorf("should not have attributed this; got %d fact(s): %+v", len(got), got)
			}
		})
	}
}

// TestUnit_Probe_ABranchThatDisagreesWithItselfIsNotAttributed.
//
// Two attempts under the *same* gate value, one accepted and one refused, means the gate does not
// explain that branch -- something else in the body does. Emitting a fact for it would attribute
// the outcome to a value that demonstrably does not decide it.
func TestUnit_Probe_ABranchThatDisagreesWithItselfIsNotAttributed(t *testing.T) {
	t.Parallel()

	sc := UnplannedScope(Subject{Resource: "tag"})

	rounds := []omission{
		att(201, map[string]string{"objectType": "test"}),
		att(400, map[string]string{"objectType": "test"}),
		att(400, map[string]string{"objectType": "endpoint-agent"}),
	}

	if got := (requiredByAPI{}).conditionalRequiredFacts(sc, "matchType", rounds); got != nil {
		t.Errorf("a self-disagreeing branch should not be attributed: %+v", got)
	}
}

// TestUnit_Probe_AConditionalFactValidatesLikeAnyOther, and a malformed condition does not.
//
// Facts are loaded from a hand-editable document, so a condition that constrains nothing has to be
// refused on the way in. A fact that reads as conditional and applies always is the worst of both
// and is undetectable downstream.
func TestUnit_Probe_AConditionalFactValidatesLikeAnyOther(t *testing.T) {
	t.Parallel()

	sound := Fact{
		Resource: "tag", JSONPath: "matchType", Field: FactRequiredByAPI,
		Value: BoolValue(true), Confidence: ConfidenceObserved, Probe: "write.required",
		Evidence: []string{"i-1"}, Rationale: "refused",
		When: []Condition{{JSONPath: "objectType", Equals: "endpoint-agent"}},
	}
	if err := sound.Validate(); err != nil {
		t.Fatalf("a well-formed conditional fact should validate: %v", err)
	}

	for _, tc := range []struct {
		name string
		when []Condition
		want string
	}{
		{
			name: "no path",
			when: []Condition{{Equals: "endpoint-agent"}},
			want: "no jsonPath",
		},
		{
			// Absence of a gate value and a gate holding "" are different observations, and
			// nothing distinguishes them once written.
			name: "no value",
			when: []Condition{{JSONPath: "objectType"}},
			want: "no value",
		},
		{
			// Two values for one path cannot both have held, so the fact describes a state
			// that never existed.
			name: "the same path twice",
			when: []Condition{
				{JSONPath: "objectType", Equals: "test"},
				{JSONPath: "objectType", Equals: "endpoint-agent"},
			},
			want: "twice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := sound
			f.When = tc.when

			err := f.Validate()
			if err == nil {
				t.Fatal("expected this condition to be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q: %v", tc.want, err)
			}
		})
	}
}

// TestUnit_Probe_AnUnconditionalFactIsUnchanged.
//
// The whole feature has to be inert for every fact that existed before it. Because() empty,
// Conditional() false, and String() identical -- otherwise adding a precondition would rewrite
// every committed fact's rendering and the cassette gate would fail for no reason.
func TestUnit_Probe_AnUnconditionalFactIsUnchanged(t *testing.T) {
	t.Parallel()

	f := Fact{
		Resource: "tag", JSONPath: "colour", Field: FactServerDefault,
		Value: TextValue("#A7EB10"), Confidence: ConfidenceCorroborated, Probe: "write.default",
		Evidence: []string{"i-1"}, Rationale: "observed",
	}

	if f.Conditional() {
		t.Error("a fact with no When is not conditional")
	}
	if f.Because() != "" {
		t.Errorf("Because() should be empty: %q", f.Because())
	}
	if got := f.String(); !strings.HasSuffix(got, "(corroborated, write.default)") {
		t.Errorf("rendering should be unchanged: %q", got)
	}
}
