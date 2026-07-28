package probe

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// scopeSubject is a subject with one of every shape the narrowing rules key on.
func scopeSubject() Subject {
	subj := quirkSubject()

	subj.Fields = []Field{
		{JSONPath: "id", Attribute: "id", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Computed},
		{JSONPath: "key", Attribute: "key", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Required, Writable: true},
		{JSONPath: "value", Attribute: "value", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true},
		{JSONPath: "mode", Attribute: "mode", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true, Enum: []string{"and", "or"}},
		{JSONPath: "count", Attribute: "count", Kind: blueprint.KindInt64, ComputedOptionalRequired: blueprint.Optional, Writable: true},
		{JSONPath: "webhook", Attribute: "webhook", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true},
		{JSONPath: "nested.inner", Attribute: "inner", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true},
		{JSONPath: "createdAt", Attribute: "created_at", Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Computed},
	}

	return subj
}

func scopePlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "minimal", Body: map[string]any{"key": "x", "value": "v"}},
			{Name: "full", Body: map[string]any{"key": "x", "value": "v", "mode": "and"}},
		},
		Candidates: map[string][]any{
			"value": {"one", "two"},
			// One candidate only, which is not enough for immutability: the fact requires
			// Corroborated, which needs two distinct proven values.
			"mode": {"or"},
		},
		DefaultInfluencers: []string{"mode"},
		Deny:               []string{"webhook"},
	}
}

// TestUnit_Probe_ScopeNarrowsByThePlan.
//
// Each set is derived once and read by both Cost and Exercise. Asserting them here is asserting
// that the budget and the behavior cannot diverge.
func TestUnit_Probe_ScopeNarrowsByThePlan(t *testing.T) {
	t.Parallel()

	sc, err := NewScope(scopeSubject(), scopePlan())
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	if !sc.Planned {
		t.Error("a plan with fixtures is a plan")
	}

	tests := []struct {
		name string
		got  []Field
		want []string
	}{
		{
			// Computed fields are excluded because sending a value for one would produce a
			// fact about a code path the generated provider does not have. The denied field is
			// excluded because that is what denying means.
			name: "sendable",
			got:  sc.Sendable(),
			want: []string{"count", "key", "mode", "nested.inner", "value"},
		},
		{
			// What no fixture sets, which is what a server default is observed on.
			name: "omitted",
			got:  sc.Omitted(),
			want: []string{"count", "nested.inner"},
		},
		{
			// Two or more candidates. "mode" has one, so it is out.
			name: "immutable",
			got:  sc.Immutable(),
			want: []string{"value"},
		},
		{
			name: "enums",
			got:  sc.Enums(),
			want: []string{"mode"},
		},
		{
			// Top-level strings only: a nested field is not reliably present on every object,
			// and normalization is read back from a list response.
			name: "normalizable",
			got:  sc.Normalizable(),
			want: []string{"key", "mode", "value"},
		},
		{
			name: "influencers",
			got:  sc.Influencers(),
			want: []string{"mode"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, f := range tc.got {
				got = append(got, f.JSONPath)
			}

			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
			}
		})
	}

	if !sc.Denied("webhook") {
		t.Error("webhook should be denied")
	}
	if sc.Denied("value") {
		t.Error("value should not be denied")
	}
}

// TestUnit_Probe_UnplannedScopeReportsTheWorstCase.
//
// The figure an operator needs in order to decide whether a plan is worth writing. A scope that
// reported small numbers because nobody supplied a plan would be worse than useless -- it would
// look like the run fits.
func TestUnit_Probe_UnplannedScopeReportsTheWorstCase(t *testing.T) {
	t.Parallel()

	sc := UnplannedScope(scopeSubject())

	if sc.Planned {
		t.Error("no plan is not a plan")
	}
	if len(sc.Sendable()) == 0 {
		t.Error("every writable field is sendable when nothing narrows them")
	}
	// Nothing is "omitted by every fixture" when there are no fixtures. Reporting all of them
	// would claim the server-default protocol covers fields it has no baseline for.
	if len(sc.Omitted()) != 0 {
		t.Errorf("Omitted = %v, want none without a fixture", sc.Omitted())
	}

	// A zero Plan is not a plan either, which is the case `probe -list` with no -plan hits.
	zero, err := NewScope(scopeSubject(), Plan{})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	if zero.Planned {
		t.Error("a plan with no fixtures narrows nothing and must not read as planned")
	}

	if can, why := zero.CanMutate(); can || !strings.Contains(why, "no probe plan") {
		t.Errorf("CanMutate = %v, %q", can, why)
	}
	if !strings.Contains(sc.String(), "unnarrowed worst case") {
		t.Errorf("String should say the costs are unnarrowed: %q", sc.String())
	}
}

// TestUnit_Probe_ScopeRefusesAPlanThatNamesNothing.
//
// A plan naming a field that does not exist would silently produce an empty set, which reads
// exactly like "this API has no writable fields" -- and would then be costed as free.
func TestUnit_Probe_ScopeRefusesAPlanThatNamesNothing(t *testing.T) {
	t.Parallel()

	_, err := NewScope(scopeSubject(), Plan{
		Fixtures: []Fixture{{Name: "typo", Body: map[string]any{"keyy": "x"}}},
	})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("error = %v, want ErrInvalidPlan", err)
	}
}

// TestUnit_Probe_ScopeFixtureBodiesAreCopied.
//
// A probe mutates the body it sends -- omitting a key, substituting a value. One that mutated the
// plan's own map would silently change what every later probe sends, and the resulting facts would
// be about a body nobody declared.
func TestUnit_Probe_ScopeFixtureBodiesAreCopied(t *testing.T) {
	t.Parallel()

	plan := scopePlan()

	sc, err := NewScope(scopeSubject(), plan)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	got, ok := sc.Fixture(0)
	if !ok {
		t.Fatal("fixture 0 should exist")
	}

	delete(got.Body, "key")
	got.Body["value"] = "mutated"

	again, _ := sc.Fixture(0)
	if _, present := again.Body["key"]; !present {
		t.Error("mutating a returned fixture changed the plan's own copy")
	}
	if again.Body["value"] != "v" {
		t.Errorf("value = %v, want the plan's own", again.Body["value"])
	}
	if plan.Fixtures[0].Body["value"] != "v" {
		t.Error("the caller's plan was mutated")
	}

	if _, ok := sc.Fixture(99); ok {
		t.Error("an out-of-range fixture must not be reported as present")
	}

	// Candidates are copied for the same reason.
	cands := sc.Candidates("value")
	cands[0] = "mutated"

	if sc.Candidates("value")[0] == "mutated" {
		t.Error("candidates are shared with the plan")
	}
	if sc.Candidates("absent") != nil {
		t.Error("a path with no candidates should yield nil")
	}
}

// TestUnit_Probe_TheNameFieldIsNotOmittable.
//
// Omitting the name field is not an experiment this tool can run: an object created without the
// stamped prefix could not be found by the sweeper, so the create would be refused before it was
// sent. Excluded from the domain, and the probe is expected to say so in a note rather than leave
// the field looking unexamined.
func TestUnit_Probe_TheNameFieldIsNotOmittable(t *testing.T) {
	t.Parallel()

	sc, err := NewScope(scopeSubject(), scopePlan())
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	fixture, _ := sc.Fixture(0)

	got := sc.Omittable(fixture)
	for _, key := range got {
		if key == sc.Subject.NameField {
			t.Errorf("the name field %q must not be omittable", key)
		}
	}

	if strings.Join(got, ",") != "value" {
		t.Errorf("omittable = %v, want just value", got)
	}
}

// TestUnit_Probe_CostAlwaysCoversCreates is one of the two invariants the cost model has to hold.
//
// Every created object costs at least a create and a delete, so a probe declaring more creates
// than requests has miscounted -- and the request cap would then be the *looser* of the two,
// which is the opposite of the intended relationship.
func TestUnit_Probe_CostAlwaysCoversCreates(t *testing.T) {
	t.Parallel()

	scopes := map[string]Scope{
		"unplanned": UnplannedScope(scopeSubject()),
		"planned":   mustScope(t, scopeSubject(), scopePlan()),
		"no update": func() Scope {
			subj := scopeSubject()
			subj.Update = nil

			return mustScope(t, subj, scopePlan())
		}(),
	}

	for name, sc := range scopes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, e := range Catalogue(sc) {
				if e.Cost < e.Creates {
					t.Errorf("%s: declares %d creates but only %d requests; every created "+
						"object costs at least a create and a delete", e.Name, e.Creates, e.Cost)
				}
				if e.Kind == KindRead && e.Creates != 0 {
					t.Errorf("%s is read-only but claims %d creates", e.Name, e.Creates)
				}
			}
		})
	}
}

// TestUnit_Probe_PlanNarrowingActuallyReducesTheCost.
//
// The whole justification for Scope. If the plan did not move these numbers, the narrowing would
// be decorative and the catalogue would still not fit.
func TestUnit_Probe_PlanNarrowingActuallyReducesTheCost(t *testing.T) {
	t.Parallel()

	unplanned := UnplannedScope(scopeSubject())
	planned := mustScope(t, scopeSubject(), scopePlan())

	_, unplannedCreates := TotalCost(unplanned, "")
	_, plannedCreates := TotalCost(planned, "")

	if plannedCreates >= unplannedCreates {
		t.Errorf("a plan should reduce creates: %d planned vs %d unplanned",
			plannedCreates, unplannedCreates)
	}

	// Specifically, the immutability protocol -- the most expensive in the catalogue -- must
	// narrow from every sendable field to the fields carrying two candidates.
	_, unplannedImm := TotalCost(unplanned, "write.immutability")
	_, plannedImm := TotalCost(planned, "write.immutability")

	if plannedImm != 2 {
		t.Errorf("immutability creates = %d, want 2 (one candidate field, two objects)", plannedImm)
	}
	if unplannedImm <= plannedImm {
		t.Errorf("unplanned immutability (%d) should cost more than planned (%d)",
			unplannedImm, plannedImm)
	}
}

func mustScope(t *testing.T, subj Subject, plan Plan) Scope {
	t.Helper()

	sc, err := NewScope(subj, plan)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	return sc
}
