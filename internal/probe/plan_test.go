package probe

import (
	"errors"
	"strings"
	"testing"
)

// TestUnit_Probe_PlanValidate: a plan is hand-authored, so it collects every problem
// rather than stopping at the first, following blueprint.Validate.
func TestUnit_Probe_PlanValidate(t *testing.T) {
	t.Parallel()

	subj := testSubject()

	good := Plan{
		Fixtures: []Fixture{{
			Name: "minimal",
			Body: map[string]any{"key": "probe", "value": "x"},
		}},
		Candidates:         map[string][]any{"value": {"other"}},
		DefaultInfluencers: []string{"key"},
		Deny:               []string{"id"},
	}
	if err := good.Validate(subj); err != nil {
		t.Fatalf("a well-formed plan must validate: %v", err)
	}

	// A fixture key the schema does not know is nearly always a typo -- and a typo here
	// silently omits the field it meant to set, which then looks like a server default.
	// That is a false fact produced by a plan mistake, so it is caught rather than run.
	tests := []struct {
		name string
		plan Plan
		want string
	}{
		{
			name: "fixture with no name",
			plan: Plan{Fixtures: []Fixture{{Body: map[string]any{"key": "x"}}}},
			want: "fixtures[0].name",
		},
		{
			name: "fixture with no body",
			plan: Plan{Fixtures: []Fixture{{Name: "empty"}}},
			want: "body: is required",
		},
		{
			name: "fixture key that is not an attribute",
			plan: Plan{Fixtures: []Fixture{{Name: "typo", Body: map[string]any{"colour": "blue"}}}},
			want: "no attribute has this JSON path",
		},
		{
			name: "candidate for an unknown path",
			plan: Plan{Candidates: map[string][]any{"nope": {1}}},
			want: "candidates[nope]",
		},
		{
			name: "influencer for an unknown path",
			plan: Plan{DefaultInfluencers: []string{"nope"}},
			want: "defaultInfluencers[nope]",
		},
		{
			name: "deny for an unknown path",
			plan: Plan{Deny: []string{"nope"}},
			want: "deny[nope]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.plan.Validate(subj)
			if err == nil {
				t.Fatalf("expected a problem mentioning %q", tc.want)
			}
			if !errors.Is(err, ErrInvalidPlan) {
				t.Errorf("error = %v, want ErrInvalidPlan", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error omits %q:\n%v", tc.want, err)
			}
		})
	}
}

func TestUnit_Probe_PlanValidateCollectsEveryProblem(t *testing.T) {
	t.Parallel()

	plan := Plan{
		Fixtures:           []Fixture{{Body: map[string]any{"nope": 1}}},
		Candidates:         map[string][]any{"also-nope": {1}},
		DefaultInfluencers: []string{"still-nope"},
	}

	err := plan.Validate(testSubject())
	if err == nil {
		t.Fatal("expected problems")
	}

	// Four distinct problems: a missing name, an unknown fixture key, an unknown
	// candidate and an unknown influencer. Being told about one at a time is a poor way
	// to author a plan.
	if !strings.Contains(err.Error(), "4 problem(s)") {
		t.Errorf("expected all four problems to be collected:\n%v", err)
	}
}

// TestUnit_Probe_BudgetDefaults: a plan that omits a cap must not thereby become
// unlimited.
func TestUnit_Probe_BudgetDefaults(t *testing.T) {
	t.Parallel()

	got := Budget{}.WithDefaults()

	if got.MaxRequests != defaultMaxRequests {
		t.Errorf("MaxRequests = %d, want %d", got.MaxRequests, defaultMaxRequests)
	}
	if got.MaxCreates != defaultMaxCreates {
		t.Errorf("MaxCreates = %d, want %d", got.MaxCreates, defaultMaxCreates)
	}
	if got.MaxWallClockSeconds != defaultMaxWallClockSeconds {
		t.Errorf("MaxWallClockSeconds = %d, want %d", got.MaxWallClockSeconds, defaultMaxWallClockSeconds)
	}

	// MaxDeleteFailures is deliberately *not* defaulted. Zero is the intended value --
	// stop creating the moment a delete fails -- and treating zero as "unset" would turn
	// the safest setting into the one you cannot express.
	if got.MaxDeleteFailures != 0 {
		t.Errorf("MaxDeleteFailures = %d; zero must stay zero", got.MaxDeleteFailures)
	}

	// An explicit value is preserved.
	explicit := Budget{MaxRequests: 5, MaxCreates: 1, MaxWallClockSeconds: 30}.WithDefaults()
	if explicit.MaxRequests != 5 || explicit.MaxCreates != 1 || explicit.MaxWallClockSeconds != 30 {
		t.Errorf("explicit caps were overwritten: %+v", explicit)
	}

	// WithDefaults returns a copy, so a plan read from disk is not mutated.
	original := Budget{}
	_ = original.WithDefaults()
	if original.MaxRequests != 0 {
		t.Error("WithDefaults mutated its receiver")
	}
}

func TestUnit_Probe_PlanLookups(t *testing.T) {
	t.Parallel()

	plan := Plan{
		Candidates: map[string][]any{"value": {"a", "b"}},
		Deny:       []string{"webhook_url"},
	}

	if !plan.Denied("webhook_url") {
		t.Error("a denied path should be reported as denied")
	}
	if plan.Denied("value") {
		t.Error("an undenied path must not be denied")
	}
	if got := plan.CandidatesFor("value"); len(got) != 2 {
		t.Errorf("CandidatesFor = %v, want two candidates", got)
	}
	if got := plan.CandidatesFor("absent"); got != nil {
		t.Errorf("CandidatesFor on an unknown path = %v, want nil", got)
	}
}
