package merge

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// testBlueprint mirrors the shape of the committed pilot: an attribute assumed to have a server
// default, and one marked required although the request schema declares none.
func testBlueprint() blueprint.Blueprint {
	return blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider:      blueprint.Provider{Name: "example"},
		Resources: []blueprint.Resource{{
			Key: "thing",
			Schema: blueprint.Schema{
				Attributes: []blueprint.Attribute{
					{
						Name: "colour", ComputedOptionalRequired: blueprint.ComputedOptional,
						Type:                blueprint.AttrType{Kind: blueprint.KindString},
						Wire:                blueprint.WireBinding{JSONPath: "colour"},
						MarkdownDescription: "The thing's colour.",
					},
					{
						Name: "key", ComputedOptionalRequired: blueprint.Required,
						Type: blueprint.AttrType{Kind: blueprint.KindString},
						Wire: blueprint.WireBinding{JSONPath: "key"},
					},
					{
						Name: "items", ComputedOptionalRequired: blueprint.Optional,
						Type: blueprint.AttrType{
							Kind: blueprint.KindSetNested,
							NestedObject: &blueprint.NestedAttributeObject{
								GoTypeName: "ItemModel",
								Attributes: []blueprint.Attribute{{
									Name: "mode", ComputedOptionalRequired: blueprint.Optional,
									Type: blueprint.AttrType{Kind: blueprint.KindString},
									Wire: blueprint.WireBinding{JSONPath: "mode"},
								}},
							},
						},
						Wire: blueprint.WireBinding{JSONPath: "items"},
					},
				},
			},
		}},
	}
}

func fact(path string, field probe.FactField, value probe.Value, conf probe.Confidence) probe.Fact {
	return probe.Fact{
		Resource: "thing", JSONPath: path, Field: field, Value: value,
		Confidence: conf, Probe: "test",
		Evidence:  []string{"004-post-things"},
		Rationale: "observed during a test",
	}
}

// TestUnit_Merge_NoServerDefaultConflictsWithComputedOptional is the Phase 4.5 milestone, and
// the case the whole precedence design exists for.
//
// A curated computed_optional plus **no** observed server default is wrong: the attribute reads
// "(known after apply)" forever and never becomes known. But narrowing presence can break
// existing state, so merge must not do it silently -- it conflicts, changes nothing, and exits
// non-zero.
//
// This is a realistic outcome for the committed pilot blueprint, where three attributes are
// computed_optional on an explicitly unprobed assumption.
func TestUnit_Merge_NoServerDefaultConflictsWithComputedOptional(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	before := bp.Resources[0].Schema.Attributes[0].ComputedOptionalRequired

	facts := []probe.Fact{
		// A server-default fact with no literal: the probe looked and found nothing.
		fact("colour", probe.FactServerDefault, probe.Value{Text: "none observed"}, probe.ConfidenceObserved),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(result.Conflicts), result.Conflicts)
	}

	c := result.Conflicts[0]
	for _, want := range []string{"computed_optional", "no server default", "optional"} {
		if !strings.Contains(c.String(), want) {
			t.Errorf("the conflict should mention %q:\n%s", want, c)
		}
	}
	// It has to say why, or a reader cannot tell a refusal from a bug.
	if !strings.Contains(c.Why, "break existing state") {
		t.Errorf("the conflict must explain the refusal: %q", c.Why)
	}
	if len(c.Evidence) == 0 {
		t.Error("the conflict must cite its evidence")
	}

	// Nothing changed, even under apply.
	if bp.Resources[0].Schema.Attributes[0].ComputedOptionalRequired != before {
		t.Errorf("presence changed to %q; narrowing must never be automatic",
			bp.Resources[0].Schema.Attributes[0].ComputedOptionalRequired)
	}

	if !errors.Is(result.Err(), ErrConflicts) {
		t.Errorf("Err() = %v, want ErrConflicts", result.Err())
	}
}

// TestUnit_Merge_ConstantDefaultIsRecordedAndDescribed is the other half of the milestone.
//
// A constant default confirms the guess, so it applies cleanly: behaviour is written, the
// description gains a probed block, and the exit is zero. Adding a *static* default is only
// recommended, because it changes plan output for every existing configuration.
func TestUnit_Merge_ConstantDefaultIsRecordedAndDescribed(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactServerDefault,
			probe.LiteralValue(blueprint.Literal{Kind: blueprint.KindString, Raw: `"blue"`}),
			probe.ConfidenceCorroborated),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, RecordingID: "1.0-t1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Fatalf("a confirmed default should not conflict: %+v", result.Conflicts)
	}
	if result.Err() != nil {
		t.Errorf("Err() = %v, want nil", result.Err())
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if attr.Behaviour.ServerDefault == nil || attr.Behaviour.ServerDefault.Raw != `"blue"` {
		t.Errorf("the server default was not recorded: %+v", attr.Behaviour.ServerDefault)
	}
	// The curated prose survives, because a human wrote it.
	if !strings.Contains(attr.MarkdownDescription, "The thing's colour.") {
		t.Errorf("the curated description was lost: %q", attr.MarkdownDescription)
	}
	if !strings.Contains(attr.MarkdownDescription, "blue") {
		t.Errorf("the observation was not written: %q", attr.MarkdownDescription)
	}

	// A static default is recommended, never applied.
	var recommended bool
	for _, r := range result.Recommendations {
		if strings.Contains(r, "default.static") {
			recommended = true
		}
	}
	if !recommended {
		t.Errorf("a static default should be recommended: %v", result.Recommendations)
	}
	if attr.Default != nil {
		t.Error("merge must not add a static default itself: it changes plan output for every " +
			"existing configuration")
	}
}

// TestUnit_Merge_DerivedDefaultConfirmsTheGuess.
//
// computed_optional is exactly right for a derived default, so the blueprint's assumption turns
// out correct -- for a reason nobody had established. Worth saying so rather than silently
// agreeing, which is why this records a change even though nothing structural moves.
func TestUnit_Merge_DerivedDefaultConfirmsTheGuess(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactDefaultIsDerived, probe.BoolValue(true), probe.ConfidenceObserved),
	}

	result, err := Apply(&bp, facts, Options{RecordingID: "1.0-t1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Errorf("a derived default should not conflict with computed_optional: %+v", result.Conflicts)
	}
	if bp.Resources[0].Schema.Attributes[0].ComputedOptionalRequired != blueprint.ComputedOptional {
		t.Error("presence should be left alone")
	}

	var confirmed bool
	for _, c := range result.Changes {
		if strings.Contains(c.What, "confirmed") {
			confirmed = true
			if c.Warning == "" {
				t.Error("a confirmed guess should say the reason was not previously established")
			}
		}
	}
	if !confirmed {
		t.Errorf("the confirmation should be recorded: %+v", result.Changes)
	}

	// And a static default on a derived value is a conflict, because it is a permanent lie.
	withStatic := testBlueprint()
	withStatic.Resources[0].Schema.Attributes[0].Default = &blueprint.Default{
		Static: &blueprint.Literal{Kind: blueprint.KindString, Raw: `"blue"`},
	}

	result, err = Apply(&withStatic, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("a static default for a derived value must conflict: %+v", result.Conflicts)
	}
	if !strings.Contains(result.Conflicts[0].Why, "wrong at plan time") {
		t.Errorf("the conflict should explain the cost: %q", result.Conflicts[0].Why)
	}
}

// TestUnit_Merge_RequiredByAPI covers both directions of the pilot's other guess.
func TestUnit_Merge_RequiredByAPI(t *testing.T) {
	t.Parallel()

	t.Run("enforced although undeclared", func(t *testing.T) {
		t.Parallel()

		bp := testBlueprint()

		facts := []probe.Fact{
			fact("key", probe.FactRequiredByAPI, probe.BoolValue(true), probe.ConfidenceObserved),
		}

		result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, RecordingID: "1.0-t1"})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		if len(result.Conflicts) != 0 {
			t.Errorf("confirming a requirement should not conflict: %+v", result.Conflicts)
		}

		attr := bp.Resources[0].Schema.Attributes[1]
		if attr.ComputedOptionalRequired != blueprint.Required {
			t.Errorf("presence = %q, want it left required", attr.ComputedOptionalRequired)
		}
		if attr.Behaviour.RequiredByAPI == nil || !*attr.Behaviour.RequiredByAPI {
			t.Error("the behaviour should record that the API enforces it")
		}
		// The description tells whoever regenerates from a newer specification not to "fix" it.
		if !strings.Contains(attr.MarkdownDescription, "does not declare") {
			t.Errorf("the description should say the specification does not declare it: %q",
				attr.MarkdownDescription)
		}
	})

	t.Run("not enforced widens, loudly", func(t *testing.T) {
		t.Parallel()

		bp := testBlueprint()

		facts := []probe.Fact{
			fact("key", probe.FactRequiredByAPI, probe.BoolValue(false), probe.ConfidenceObserved),
		}

		// Widening needs apply; annotate reports it instead.
		annotated := testBlueprint()
		result, err := Apply(&annotated, facts, Options{Strategy: StrategyAnnotate})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(result.Conflicts) != 1 {
			t.Fatalf("annotate should report rather than change presence: %+v", result.Conflicts)
		}
		if annotated.Resources[0].Schema.Attributes[1].ComputedOptionalRequired != blueprint.Required {
			t.Error("annotate must not change presence")
		}

		result, err = Apply(&bp, facts, Options{Strategy: StrategyApply})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		if bp.Resources[0].Schema.Attributes[1].ComputedOptionalRequired != blueprint.Optional {
			t.Errorf("presence = %q, want optional", bp.Resources[0].Schema.Attributes[1].ComputedOptionalRequired)
		}

		// Widening is safe but surprising, so it has to be said out loud.
		var warned bool
		for _, c := range result.Changes {
			if c.What == "presence" && c.Warning != "" {
				warned = true
			}
		}
		if !warned {
			t.Errorf("widening a required attribute should carry a warning: %+v", result.Changes)
		}
	})
}

// TestUnit_Merge_DangerousClaimsNeedCorroboration.
//
// writable=false and immutable=true both change what a practitioner may write, with no
// workaround. One observation is not enough.
func TestUnit_Merge_DangerousClaimsNeedCorroboration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		field probe.FactField
		value probe.Value
	}{
		"writable=false": {probe.FactWritable, probe.BoolValue(false)},
		"immutable=true": {probe.FactImmutable, probe.BoolValue(true)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bp := testBlueprint()

			// Observed is not enough.
			result, err := Apply(&bp, []probe.Fact{fact("colour", tc.field, tc.value, probe.ConfidenceObserved)},
				Options{Strategy: StrategyApply})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(result.Conflicts) != 1 {
				t.Fatalf("%s at Observed should be refused: %+v", name, result.Conflicts)
			}
			if !strings.Contains(result.Conflicts[0].Why, "corroboration") {
				t.Errorf("the refusal should say why: %q", result.Conflicts[0].Why)
			}
			if len(result.Changes) != 0 {
				t.Errorf("nothing should change: %+v", result.Changes)
			}

			// Corroborated is.
			bp = testBlueprint()
			result, err = Apply(&bp, []probe.Fact{fact("colour", tc.field, tc.value, probe.ConfidenceCorroborated)},
				Options{Strategy: StrategyApply})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(result.Conflicts) != 0 {
				t.Errorf("%s at Corroborated should be accepted: %+v", name, result.Conflicts)
			}
			if len(result.Changes) == 0 {
				t.Error("the behaviour should have been written")
			}
		})
	}
}

// TestUnit_Merge_ImmutableNeverSetsAPlanModifier.
//
// Whether Terraform should destroy and recreate is a decision about somebody's infrastructure.
// The toolkit's job is to put the evidence in front of the person making it.
func TestUnit_Merge_ImmutableNeverSetsAPlanModifier(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactImmutable, probe.BoolValue(true), probe.ConfidenceCorroborated),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if len(attr.PlanModifiers) != 0 {
		t.Errorf("merge must never add a plan modifier: %+v", attr.PlanModifiers)
	}
	if attr.Behaviour.Immutable == nil || !*attr.Behaviour.Immutable {
		t.Error("the behaviour should be recorded")
	}

	var recommended bool
	for _, r := range result.Recommendations {
		if strings.Contains(r, "RequiresReplace") {
			recommended = true
			if !strings.Contains(r, "destroys and recreates") {
				t.Errorf("the recommendation should state the cost: %q", r)
			}
		}
	}
	if !recommended {
		t.Errorf("RequiresReplace should be recommended: %v", result.Recommendations)
	}
}

// TestUnit_Merge_SuspectedFactsAreNeverApplied.
//
// A suspected fact is a prompt for somebody to look, not a conclusion, and letting one into the
// blueprint would put a claim there that no sequence supports.
func TestUnit_Merge_SuspectedFactsAreNeverApplied(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactWritable, probe.BoolValue(false), probe.ConfidenceSuspected),
		fact("colour", probe.FactVolatile, probe.BoolValue(true), probe.ConfidenceSuspected),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Changes) != 0 {
		t.Errorf("a suspected fact must change nothing: %+v", result.Changes)
	}
	if result.Ignored != 2 {
		t.Errorf("Ignored = %d, want 2 -- a run that ignored facts must say so", result.Ignored)
	}
	if bp.Resources[0].Schema.Attributes[0].Behaviour.Writable != nil {
		t.Error("behaviour was written from a suspected fact")
	}
}

// TestUnit_Merge_ReadBackTurnsOnNeverOff.
//
// Asymmetric on purpose: seeing a stale read proves inconsistency, one fast success proves
// nothing. A needless re-read costs a request; a missing one costs a failed apply.
func TestUnit_Merge_ReadBackTurnsOnNeverOff(t *testing.T) {
	t.Parallel()

	on := probe.Fact{
		Resource: "thing", Field: probe.FactReadBack, Confidence: probe.ConfidenceObserved, Probe: "test",
		Value:     probe.ReadBackValue(blueprint.ReadBack{Enabled: true, MaxRetries: 3, IntervalMS: 250}),
		Evidence:  []string{"002-get-things-1"},
		Rationale: "the first read after create answered 404",
	}

	bp := testBlueprint()
	result, err := Apply(&bp, []probe.Fact{on}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bp.Resources[0].Policy.ReadBack.Enabled {
		t.Error("a read-back should be enabled")
	}
	// A retry loop with no stated reason is indistinguishable from cargo cult.
	if bp.Resources[0].Policy.ReadBack.Reason == "" {
		t.Error("the read-back should carry the reason it exists")
	}
	if len(result.Changes) == 0 {
		t.Error("the change should be recorded")
	}

	// Turning it off is refused.
	off := on
	off.Value = probe.ReadBackValue(blueprint.ReadBack{Enabled: false})

	enabled := testBlueprint()
	enabled.Resources[0].Policy.ReadBack = blueprint.ReadBack{Enabled: true, MaxRetries: 5}

	result, err = Apply(&enabled, []probe.Fact{off}, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !enabled.Resources[0].Policy.ReadBack.Enabled {
		t.Error("a read-back must never be removed automatically")
	}
	if len(result.Conflicts) != 1 {
		t.Errorf("removing a read-back should conflict: %+v", result.Conflicts)
	}

	// And retries only increase.
	lower := on
	lower.Value = probe.ReadBackValue(blueprint.ReadBack{Enabled: true, MaxRetries: 1})

	high := testBlueprint()
	high.Resources[0].Policy.ReadBack = blueprint.ReadBack{Enabled: true, MaxRetries: 5}

	if _, err := Apply(&high, []probe.Fact{lower}, Options{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if high.Resources[0].Policy.ReadBack.MaxRetries != 5 {
		t.Errorf("retries = %d, want them not lowered", high.Resources[0].Policy.ReadBack.MaxRetries)
	}
}

// TestUnit_Merge_UpdateStyleIsNotOverwritten.
//
// Getting this wrong silently erases attributes the practitioner never mentioned, so it is not
// changed on one observation against a human's stated choice.
func TestUnit_Merge_UpdateStyleIsNotOverwritten(t *testing.T) {
	t.Parallel()

	observed := probe.Fact{
		Resource: "thing", Field: probe.FactUpdateStyle, Confidence: probe.ConfidenceObserved, Probe: "test",
		Value:     probe.TextValue(string(blueprint.UpdatePatchMerge)),
		Evidence:  []string{"005-put-things-1"},
		Rationale: "a field omitted from an update survived",
	}

	// Unset: filled in.
	bp := testBlueprint()
	if _, err := Apply(&bp, []probe.Fact{observed}, Options{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bp.Resources[0].Policy.UpdateStyle != blueprint.UpdatePatchMerge {
		t.Errorf("updateStyle = %q, want it filled in", bp.Resources[0].Policy.UpdateStyle)
	}

	// Set and disagreeing: conflicts, unchanged.
	curated := testBlueprint()
	curated.Resources[0].Policy.UpdateStyle = blueprint.UpdatePutFull

	result, err := Apply(&curated, []probe.Fact{observed}, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if curated.Resources[0].Policy.UpdateStyle != blueprint.UpdatePutFull {
		t.Error("a curated update style must not be overwritten")
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("the disagreement should conflict: %+v", result.Conflicts)
	}
	if !strings.Contains(result.Conflicts[0].Why, "silently erases") {
		t.Errorf("the conflict should state the cost: %q", result.Conflicts[0].Why)
	}
}

// TestUnit_Merge_AConditionalResourceLevelFactIsNotApplied.
//
// Policy is unconditional -- one update style, one delete semantics per resource -- so a fact
// that holds only under a precondition must not be written into it. The same guard the
// attribute path has, because the bug class is identical: an observation about one branch
// silently applied to every branch.
func TestUnit_Merge_AConditionalResourceLevelFactIsNotApplied(t *testing.T) {
	t.Parallel()

	conditional := probe.Fact{
		Resource: "thing", Field: probe.FactUpdateStyle, Confidence: probe.ConfidenceObserved, Probe: "test",
		Value:     probe.TextValue(string(blueprint.UpdatePatchMerge)),
		Evidence:  []string{"005-put-things-1"},
		Rationale: "a field omitted from an update survived",
		When:      []probe.Condition{{JSONPath: "type", Equals: "dynamic"}},
	}

	bp := testBlueprint()

	result, err := Apply(&bp, []probe.Fact{conditional}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if bp.Resources[0].Policy.UpdateStyle != "" {
		t.Errorf("updateStyle = %q, a conditional fact must not set policy",
			bp.Resources[0].Policy.UpdateStyle)
	}
	if result.Conditional != 1 {
		t.Errorf("Conditional = %d, want the held-back fact counted", result.Conditional)
	}
	if len(result.Recommendations) != 1 ||
		!strings.Contains(result.Recommendations[0], `type is "dynamic"`) {
		t.Errorf("the held-back fact should surface as a recommendation naming its "+
			"condition: %v", result.Recommendations)
	}
}

// TestUnit_Merge_AnIntegralFactIsARecommendationOnly.
//
// Changing an attribute's type breaks state compatibility, which is a human decision, like
// RequiresReplace -- and the fact is capped at Inferred anyway, because JSON cannot
// distinguish 5 from 5.0. So the blueprint must not move, and the evidence must not vanish.
func TestUnit_Merge_AnIntegralFactIsARecommendationOnly(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	before, err := blueprint.Marshal(bp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	integral := fact("colour", probe.FactIntegral, probe.BoolValue(true), probe.ConfidenceInferred)

	res, err := Apply(&bp, []probe.Fact{integral}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, err := blueprint.Marshal(bp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Error("an integral fact must not change the blueprint")
	}

	if len(res.Recommendations) != 1 ||
		!strings.Contains(res.Recommendations[0], "whole number") {
		t.Errorf("the evidence should surface as a recommendation: %v", res.Recommendations)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("a recognised fact must not fall into the unknown-field conflict: %+v",
			res.Conflicts)
	}
}

// TestUnit_Merge_NestedFieldsAreReached: a fact about a field inside an object has to land on
// the right attribute, addressed by its dotted JSON path.
func TestUnit_Merge_NestedFieldsAreReached(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("items.mode", probe.FactVolatile, probe.BoolValue(true), probe.ConfidenceObserved),
	}

	result, err := Apply(&bp, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Fatalf("a nested field should be found: %+v", result.Conflicts)
	}

	nested := bp.Resources[0].Schema.Attributes[2].Type.NestedObject.Attributes[0]
	if nested.Behaviour.Volatile == nil || !*nested.Behaviour.Volatile {
		t.Errorf("the nested attribute's behaviour was not written: %+v", nested.Behaviour)
	}
}

// TestUnit_Merge_UnmatchedFactsAreReported.
//
// A fact merge cannot place is either a schema that has moved on or a probe addressing the wrong
// thing. Both matter, and neither should be silently dropped.
func TestUnit_Merge_UnmatchedFactsAreReported(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("nonexistent", probe.FactVolatile, probe.BoolValue(true), probe.ConfidenceObserved),
		{
			Resource: "other", Field: probe.FactVolatile, Value: probe.BoolValue(true),
			Confidence: probe.ConfidenceObserved, Probe: "test",
			Evidence: []string{"x"}, Rationale: "y",
		},
	}

	result, err := Apply(&bp, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 2 {
		t.Fatalf("both should be reported: %+v", result.Conflicts)
	}

	var sawField, sawResource bool
	for _, c := range result.Conflicts {
		if strings.Contains(c.Curated, "no attribute") {
			sawField = true
		}
		if strings.Contains(c.Curated, "no such resource") {
			sawResource = true
		}
	}
	if !sawField || !sawResource {
		t.Errorf("both kinds of drift should be reported: %+v", result.Conflicts)
	}
}

// TestUnit_Merge_UnrecognisedFactFieldIsReported.
//
// Silently ignoring a fact is the one failure mode a fact store must not have: it would look
// merged and would not be.
func TestUnit_Merge_UnrecognisedFactFieldIsReported(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{fact("colour", "telepathy", probe.BoolValue(true), probe.ConfidenceObserved)}

	result, err := Apply(&bp, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 1 {
		t.Fatalf("an unrecognised field must be reported: %+v", result.Conflicts)
	}
	if !strings.Contains(result.Conflicts[0].Observed, "telepathy") {
		t.Errorf("the conflict should name the field: %+v", result.Conflicts[0])
	}
}

// TestUnit_Merge_IsIdempotent is what the description marker exists for.
//
// Merging twice must change nothing the second time. Without the marker, the observations append
// on every run: the blueprint drifts, the drift gate fires on a no-op, and a reviewer stops
// trusting it.
func TestUnit_Merge_IsIdempotent(t *testing.T) {
	t.Parallel()

	facts := []probe.Fact{
		fact("colour", probe.FactServerDefault,
			probe.LiteralValue(blueprint.Literal{Kind: blueprint.KindString, Raw: `"blue"`}),
			probe.ConfidenceCorroborated),
		fact("colour", probe.FactVolatile, probe.BoolValue(false), probe.ConfidenceObserved),
		fact("key", probe.FactRequiredByAPI, probe.BoolValue(true), probe.ConfidenceObserved),
	}

	bp := testBlueprint()
	opts := Options{Strategy: StrategyApply, RecordingID: "1.0-t1785152261691"}

	first, err := Apply(&bp, facts, opts)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if !first.Changed() {
		t.Fatal("the first merge should change something, or this test is vacuous")
	}

	afterFirst := bp.Resources[0].Schema.Attributes[0].MarkdownDescription

	second, err := Apply(&bp, facts, opts)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if second.Changed() {
		t.Errorf("the second merge changed %d thing(s); merging the same evidence twice must be "+
			"a no-op:\n%+v", len(second.Changes), second.Changes)
	}
	if got := bp.Resources[0].Schema.Attributes[0].MarkdownDescription; got != afterFirst {
		t.Errorf("the description drifted on a second merge:\n--- first\n%s\n--- second\n%s",
			afterFirst, got)
	}

	// Newer evidence *should* produce a visible one-line diff, so a reader can see which
	// recording a description came from.
	third, err := Apply(&bp, facts, Options{Strategy: StrategyApply, RecordingID: "1.1-t9"})
	if err != nil {
		t.Fatalf("third Apply: %v", err)
	}
	if !third.Changed() {
		t.Error("a newer snapshot id should update the marker")
	}
}

func TestUnit_Merge_DescriptionBlockHandling(t *testing.T) {
	t.Parallel()

	// Curated prose is preserved and separated from the generated block.
	got := appendBlock("Curated prose.", buildBlock([]string{"Observed: x."}, "s1"))
	if !strings.HasPrefix(got, "Curated prose.") {
		t.Errorf("curated prose should come first: %q", got)
	}
	if !strings.Contains(got, "\n\n<!-- probed:s1 -->") {
		t.Errorf("the block should be a separate paragraph: %q", got)
	}

	// A newer recording replaces the live block: the id changes, blocks do not accumulate.
	live, ok := channelBlock(got, false)
	if !ok {
		t.Fatalf("the live block should be found: %q", got)
	}
	replaced := strings.Replace(got, live, buildBlock([]string{"Observed: y."}, "s2"), 1)
	if strings.Contains(replaced, "Observed: x.") {
		t.Errorf("the old block should be gone: %q", replaced)
	}
	if !strings.HasPrefix(replaced, "Curated prose.") {
		t.Errorf("curated prose should survive replacement: %q", replaced)
	}
	if strings.Count(replaced, "<!-- probed:") != 1 {
		t.Errorf("there should be exactly one live block: %q", replaced)
	}

	// The static channel owns its own block beside the live one: the SDK-type facts and a
	// live recording are different evidence, and one overwriting the other is how re-merging
	// a snapshot came to read as drift.
	both := appendBlock(replaced, buildBlock([]string{"Static: z."}, StaticRecordingID))
	if s, ok := channelBlock(both, true); !ok || !strings.Contains(s, "Static: z.") {
		t.Errorf("the static block should be found beside the live one: %q", both)
	}
	if l, ok := channelBlock(both, false); !ok || !strings.Contains(l, "Observed: y.") {
		t.Errorf("the live block should survive the static one: %q", both)
	}

	// An empty description gets the block alone, with no leading blank line.
	bare := appendBlock("", buildBlock([]string{"Observed: x."}, "s1"))
	if strings.HasPrefix(bare, "\n") {
		t.Errorf("no leading newline on an empty description: %q", bare)
	}

	// An unclosed marker is a hand-edit gone wrong. Treated as absent, so the next merge
	// writes a well-formed block rather than nesting inside the broken one.
	broken := "Prose.\n\n<!-- probed:s1 -->\nObserved: x."
	if _, ok := channelBlock(broken, false); ok {
		t.Error("an unclosed marker must not be treated as a block")
	}

	// StripBlock is what a drift check needs, so newer evidence alone does not read as a
	// change -- and it removes every channel's block, not just the first.
	if stripped := StripBlock(both); stripped != "Curated prose." {
		t.Errorf("StripBlock = %q, want the curated prose alone", stripped)
	}
	if stripped := StripBlock("no block here"); stripped != "no block here" {
		t.Errorf("StripBlock on unmarked text = %q", stripped)
	}

	// A block with no snapshot id still has one, or the idempotence check would pass for
	// evidence that had changed.
	if !strings.Contains(buildBlock([]string{"x"}, ""), "unknown") {
		t.Error("a block with no snapshot id should say unknown rather than nothing")
	}
}

func TestUnit_Merge_NotFoundIsSuccessMovesEitherWay(t *testing.T) {
	t.Parallel()

	// Error handling rather than schema, and a wrong answer is recoverable, so this is the one
	// policy field merge changes freely.
	for _, want := range []bool{true, false} {
		bp := testBlueprint()
		bp.Resources[0].Policy.Delete.NotFoundIsSuccess = !want

		f := probe.Fact{
			Resource: "thing", Field: probe.FactNotFoundIsSuccess, Value: probe.BoolValue(want),
			Confidence: probe.ConfidenceObserved, Probe: "test",
			Evidence: []string{"001-get-things-999"}, Rationale: "observed",
		}

		if _, err := Apply(&bp, []probe.Fact{f}, Options{}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if bp.Resources[0].Policy.Delete.NotFoundIsSuccess != want {
			t.Errorf("notFoundIsSuccess = %v, want %v",
				bp.Resources[0].Policy.Delete.NotFoundIsSuccess, want)
		}
	}
}

func TestUnit_Merge_ObservedValueSetsAreStoredAsData(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactAcceptedValues,
			probe.ListValue([]string{"blue", "red"}), probe.ConfidenceObserved),
		fact("colour", probe.FactRejectedValues,
			probe.ListValue([]string{"deprecated"}), probe.ConfidenceObserved),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, RecordingID: "s1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	// Merge still puts no validator in the IR: it records what was observed, and render
	// decides what to generate from it. Attribute.Validators stays for hand-authored ones.
	if len(attr.Validators) != 0 {
		t.Errorf("merge must not write a validator into the IR: %+v", attr.Validators)
	}

	// Stored as data, which is what lets render name the rejected value beside the
	// validator. Previously these reached the description and nothing else, so the
	// evidence was there to read and not to act on.
	if got := attr.Behaviour.AcceptedValues; !slices.Equal(got, []string{"blue", "red"}) {
		t.Errorf("acceptedValues = %v, want [blue red]", got)
	}
	if got := attr.Behaviour.RejectedValues; !slices.Equal(got, []string{"deprecated"}) {
		t.Errorf("rejectedValues = %v, want [deprecated]", got)
	}

	// And still in the description, because that is what a practitioner reads.
	for _, want := range []string{"blue", "red", "deprecated"} {
		if !strings.Contains(attr.MarkdownDescription, want) {
			t.Errorf("the description should mention %q: %q", want, attr.MarkdownDescription)
		}
	}

	// No recommendation to add a OneOf by hand: render generates it.
	for _, r := range result.Recommendations {
		if strings.Contains(r, "OneOf") {
			t.Errorf("a OneOf is generated, so it should not be recommended: %q", r)
		}
	}
}

// TestUnit_Merge_ValuesClosedLandsOnTheAttribute is the fact merge used to discard.
//
// It was grouped with the resource-level facts and counted as ignored, even though it
// carries a JSON path. It is the one observation with direct evidence that a generated
// OneOf would be harmful, so render needs it beside the attribute.
func TestUnit_Merge_ValuesClosedLandsOnTheAttribute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		closed bool
	}{
		{"an API that enforces its documented set", true},
		{"an API that takes values outside it", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bp := testBlueprint()
			facts := []probe.Fact{
				fact("colour", probe.FactValuesClosed, probe.BoolValue(tc.closed), probe.ConfidenceObserved),
			}

			result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, RecordingID: "s1"})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if result.Ignored != 0 {
				t.Errorf("valuesClosed must not be counted as ignored: %+v", result)
			}

			got := bp.Resources[0].Schema.Attributes[0].Behaviour.ValuesClosed
			if got == nil {
				t.Fatal("valuesClosed did not reach the attribute")
			}
			if *got != tc.closed {
				t.Errorf("valuesClosed = %v, want %v", *got, tc.closed)
			}
		})
	}
}

func TestUnit_Merge_ResultRendering(t *testing.T) {
	t.Parallel()

	c := Conflict{
		Resource: "thing", JSONPath: "colour",
		Curated: "computed_optional", Observed: "no default", Suggested: "optional",
		Why: "narrowing breaks state", Evidence: []string{"004-post"}, Fix: "edit it",
	}
	for _, want := range []string{
		"thing.colour", "computed_optional", "no default", "optional",
		"narrowing breaks state", "004-post", "edit it",
	} {
		if !strings.Contains(c.String(), want) {
			t.Errorf("Conflict.String() missing %q:\n%s", want, c)
		}
	}

	ch := Change{
		Resource: "thing", JSONPath: "key", What: "presence", From: "required", To: "optional",
		Warning: "surprising",
	}
	got := ch.String()
	for _, want := range []string{"thing.key", "presence", "required", "optional", "surprising"} {
		if !strings.Contains(got, want) {
			t.Errorf("Change.String() = %q, missing %q", got, want)
		}
	}

	// A resource-level entry must not render a stray separator.
	bare := Change{Resource: "thing", What: "policy.updateStyle", To: "putFull"}
	if strings.Contains(bare.String(), "thing.:") {
		t.Errorf("stray separator: %q", bare.String())
	}

	var empty Result
	if empty.Err() != nil || empty.Changed() {
		t.Error("an empty result has no error and no changes")
	}
}

// conditional returns a fact that holds only when gate equals value.
func conditional(f probe.Fact, gate, value string) probe.Fact {
	f.When = []probe.Condition{{JSONPath: gate, Equals: value}}
	return f
}

// TestUnit_Merge_AConditionalFactBecomesABehaviourVariant is step two of the fix for the
// class of bug that prompted the whole phase.
//
// Behaviour's own fields are unconditional: one Writable, one ReturnedOnRead per attribute.
// Writing a fact that holds only under a precondition into one of those makes it a claim
// about every case -- which is exactly how the pilot came to suppress matchType's read-back
// for every tag on the strength of an observation about static ones. Step one held the fact
// back as description prose; now it lands as a variant, structure emission can act on, and
// the unconditional field still must not move.
func TestUnit_Merge_AConditionalFactBecomesABehaviourVariant(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	returned := conditional(
		fact("colour", probe.FactReturnedOnRead, probe.BoolValue(false), probe.ConfidenceCorroborated),
		"objectType", "static",
	)

	res, err := Apply(&bp, []probe.Fact{returned}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	// The claim must not reach the unconditional field, where nothing could tell it was
	// conditional.
	if attr.Behaviour.ReturnedOnRead != nil {
		t.Errorf("a conditional fact must not touch the unconditional field: %v",
			*attr.Behaviour.ReturnedOnRead)
	}

	// It lands as a variant instead.
	if len(attr.Behaviour.Conditional) != 1 {
		t.Fatalf("variants = %+v, want exactly one", attr.Behaviour.Conditional)
	}
	v := attr.Behaviour.Conditional[0]
	if v.WhenKey() != "objectType=static" {
		t.Errorf("WhenKey = %q, want objectType=static", v.WhenKey())
	}
	if v.Behaviour.ReturnedOnRead == nil || *v.Behaviour.ReturnedOnRead {
		t.Errorf("the variant should observe returnedOnRead=false: %+v", v.Behaviour)
	}

	// Visible in the report as a change naming the branch. The description rewrite adds
	// its own change beside it, so this looks for the variant's rather than counting.
	var named bool
	for _, c := range res.Changes {
		if strings.Contains(c.What, "conditional[objectType=static].returnedOnRead") {
			named = true
		}
	}
	if !named {
		t.Errorf("a change should name the variant dimension: %+v", res.Changes)
	}

	// ...and in the description, condition first, so a reader meets the branch before the
	// claim.
	if !strings.Contains(attr.MarkdownDescription, `objectType is "static"`) {
		t.Errorf("the condition should be stated:\n%s", attr.MarkdownDescription)
	}
	if !strings.Contains(attr.MarkdownDescription, "behaviour variant") {
		t.Errorf("the description should say the fact was applied:\n%s", attr.MarkdownDescription)
	}

	// Nothing was held back and nothing was too weak.
	if res.Conditional != 0 {
		t.Errorf("Conditional = %d, want 0 -- the fact found a structural home", res.Conditional)
	}
	if res.Ignored != 0 {
		t.Errorf("Ignored = %d, want 0", res.Ignored)
	}
}

// TestUnit_Merge_AConditionalFactWithNoVariantHomeIsHeldBackAsProse.
//
// The variant only has fields for the dimensions a conditional derivation exists for. A
// conditional fact about any other dimension keeps step one's behaviour: the condition is
// stated in the description, counted, and nothing is generated from it -- because applying
// it is the bug, and dropping it silently loses evidence somebody paid a live run for.
func TestUnit_Merge_AConditionalFactWithNoVariantHomeIsHeldBackAsProse(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	volatile := conditional(
		fact("colour", probe.FactVolatile, probe.BoolValue(true), probe.ConfidenceCorroborated),
		"objectType", "static",
	)

	res, err := Apply(&bp, []probe.Fact{volatile}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if attr.Behaviour.Volatile != nil {
		t.Errorf("a conditional fact must not be applied unconditionally: %v",
			*attr.Behaviour.Volatile)
	}
	if len(attr.Behaviour.Conditional) != 0 {
		t.Errorf("volatile has no variant home yet: %+v", attr.Behaviour.Conditional)
	}
	if !strings.Contains(attr.MarkdownDescription, "Conditionally") ||
		!strings.Contains(attr.MarkdownDescription, "Not applied") {
		t.Errorf("the fact should be held back in the description:\n%s", attr.MarkdownDescription)
	}
	if res.Conditional != 1 {
		t.Errorf("Conditional = %d, want the held-back fact counted", res.Conditional)
	}
}

// TestUnit_Merge_AConditionalFactContradictingTheBaseConflicts.
//
// The set base is nearly always a stale unconditional half-truth -- returnedOnRead false,
// measured on a static tag before the probe learned to attribute branches. The re-record
// replaces it; until then the disagreement must be visible rather than resolved by whichever
// fact merged last.
func TestUnit_Merge_AConditionalFactContradictingTheBaseConflicts(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	staleFalse := false
	bp.Resources[0].Schema.Attributes[0].Behaviour.ReturnedOnRead = &staleFalse

	branchTrue := conditional(
		fact("colour", probe.FactReturnedOnRead, probe.BoolValue(true), probe.ConfidenceCorroborated),
		"objectType", "dynamic",
	)

	res, err := Apply(&bp, []probe.Fact{branchTrue}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if len(attr.Behaviour.Conditional) != 0 {
		t.Errorf("a contradicted branch fact must not be written: %+v", attr.Behaviour.Conditional)
	}
	if attr.Behaviour.ReturnedOnRead == nil || *attr.Behaviour.ReturnedOnRead {
		t.Error("the base must not move either; the conflict is the output")
	}
	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0].Fix, "re-record") {
		t.Errorf("the disagreement should conflict with the fix named: %+v", res.Conflicts)
	}
}

// TestUnit_Merge_AConditionalWritableFalseStillNeedsCorroboration.
//
// The corroboration floor does not soften because the claim is scoped to a branch: a branch
// observation still turns an attribute a practitioner can set into one they cannot.
func TestUnit_Merge_AConditionalWritableFalseStillNeedsCorroboration(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	weak := conditional(
		fact("colour", probe.FactWritable, probe.BoolValue(false), probe.ConfidenceObserved),
		"objectType", "static",
	)

	res, err := Apply(&bp, []probe.Fact{weak}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(bp.Resources[0].Schema.Attributes[0].Behaviour.Conditional) != 0 {
		t.Error("an uncorroborated writable=false must not reach a variant")
	}
	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0].Why, "corroboration") {
		t.Errorf("the refusal should be a conflict naming the missing corroboration: %+v",
			res.Conflicts)
	}
}

// TestUnit_Merge_VariantOrderDoesNotDependOnFactOrder.
//
// The committed blueprint is diffed, so two merges over the same facts in any arrival order
// must produce byte-identical variants.
func TestUnit_Merge_VariantOrderDoesNotDependOnFactOrder(t *testing.T) {
	t.Parallel()

	forward := []probe.Fact{
		conditional(fact("colour", probe.FactRequiredByAPI, probe.BoolValue(true), probe.ConfidenceObserved),
			"objectType", "dynamic"),
		conditional(fact("colour", probe.FactRequiredByAPI, probe.BoolValue(false), probe.ConfidenceObserved),
			"objectType", "static"),
	}
	backward := []probe.Fact{forward[1], forward[0]}

	one := testBlueprint()
	if _, err := Apply(&one, forward, Options{RecordingID: "snap-1"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	other := testBlueprint()
	if _, err := Apply(&other, backward, Options{RecordingID: "snap-1"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	a, err := blueprint.Marshal(one)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, err := blueprint.Marshal(other)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(a) != string(b) {
		t.Error("fact arrival order leaked into the committed bytes")
	}
}

// TestUnit_Merge_TheSameFactWithoutAConditionIsApplied.
//
// The control. Without it the test above would pass if merge had simply stopped applying
// returnedOnRead altogether, which would be a different bug wearing the same green tick.
func TestUnit_Merge_TheSameFactWithoutAConditionIsApplied(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	unconditional := fact(
		"colour", probe.FactReturnedOnRead, probe.BoolValue(false), probe.ConfidenceCorroborated,
	)

	res, err := Apply(&bp, []probe.Fact{unconditional}, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if attr.Behaviour.ReturnedOnRead == nil {
		t.Fatal("an unconditional fact should reach Behaviour")
	}
	if *attr.Behaviour.ReturnedOnRead {
		t.Error("returnedOnRead should be false, as observed")
	}
	if res.Conditional != 0 {
		t.Errorf("Conditional = %d, want 0", res.Conditional)
	}
}

// TestUnit_Merge_ConditionalFactsFromBothBranchesBecomeSeparateVariants.
//
// Two branches of one gate produce two facts that disagree, which is the whole point of
// measuring them separately. Neither may win the unconditional field, neither may be
// dropped, and each lands in its own variant -- an attribute recording only one branch
// reads as though that branch were the whole truth, which is where this started.
func TestUnit_Merge_ConditionalFactsFromBothBranchesBecomeSeparateVariants(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		conditional(
			fact("colour", probe.FactRequiredByAPI, probe.BoolValue(true), probe.ConfidenceObserved),
			"objectType", "dynamic",
		),
		conditional(
			fact("colour", probe.FactRequiredByAPI, probe.BoolValue(false), probe.ConfidenceObserved),
			"objectType", "static",
		),
	}

	res, err := Apply(&bp, facts, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Schema.Attributes[0]

	if attr.Behaviour.RequiredByAPI != nil {
		t.Errorf("neither branch may win the unconditional field: %v",
			*attr.Behaviour.RequiredByAPI)
	}

	if len(attr.Behaviour.Conditional) != 2 {
		t.Fatalf("variants = %+v, want one per branch", attr.Behaviour.Conditional)
	}
	// Sorted by canonical key, not arrival order.
	if attr.Behaviour.Conditional[0].WhenKey() != "objectType=dynamic" ||
		attr.Behaviour.Conditional[1].WhenKey() != "objectType=static" {
		t.Errorf("variants should sort by canonical key: %q, %q",
			attr.Behaviour.Conditional[0].WhenKey(), attr.Behaviour.Conditional[1].WhenKey())
	}

	dynamic := attr.Behaviour.Conditional[0].Behaviour.RequiredByAPI
	static := attr.Behaviour.Conditional[1].Behaviour.RequiredByAPI
	if dynamic == nil || !*dynamic || static == nil || *static {
		t.Errorf("each branch should carry its own observation: dynamic=%v static=%v",
			dynamic, static)
	}

	for _, want := range []string{`objectType is "dynamic"`, `objectType is "static"`} {
		if !strings.Contains(attr.MarkdownDescription, want) {
			t.Errorf("both branches should be recorded, missing %s:\n%s",
				want, attr.MarkdownDescription)
		}
	}
	if res.Conditional != 0 {
		t.Errorf("Conditional = %d, want 0 -- both facts found a structural home", res.Conditional)
	}
}

// TestUnit_Merge_AConditionalFactIsIdempotentOnASecondMerge.
//
// Idempotence, which merge -check depends on. A second merge over the same facts must find
// the existing variant, see the same value, and report no further change -- otherwise CI
// would fail on every run against unchanged evidence.
func TestUnit_Merge_AConditionalFactIsIdempotentOnASecondMerge(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{conditional(
		fact("colour", probe.FactReturnedOnRead, probe.BoolValue(false), probe.ConfidenceCorroborated),
		"objectType", "static",
	)}

	if _, err := Apply(&bp, facts, Options{RecordingID: "snap-1"}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	second, err := Apply(&bp, facts, Options{RecordingID: "snap-1"})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if len(second.Changes) != 0 {
		t.Errorf("a second merge over the same facts should change nothing: %+v", second.Changes)
	}
}

// TestUnit_Merge_RehearsalEchoFactsLandOnTheAttribute covers the per-operation echo
// observations the rehearsal probe produces: the write response answering null for a
// field it was sent is a different document from a later GET, and the generator needs
// both recorded to route assertions and carry-through correctly.
func TestUnit_Merge_RehearsalEchoFactsLandOnTheAttribute(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	facts := []probe.Fact{
		fact("colour", probe.FactReturnedOnCreate, probe.BoolValue(false), probe.ConfidenceObserved),
		fact("colour", probe.FactReturnedOnRead, probe.BoolValue(false), probe.ConfidenceObserved),
		fact("colour", probe.FactReturnedOnUpdate, probe.BoolValue(false), probe.ConfidenceObserved),
	}

	result, err := Apply(&bp, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", result.Conflicts)
	}

	b := bp.Resources[0].Schema.Attributes[0].Behaviour
	if b.ReturnedOnCreate == nil || *b.ReturnedOnCreate {
		t.Error("returnedOnCreate=false must land on the attribute")
	}
	if b.ReturnedOnUpdate == nil || *b.ReturnedOnUpdate {
		t.Error("returnedOnUpdate=false must land on the attribute")
	}

	// All three false together is a write-only field, and the description must say so:
	// prose is where a schema reader learns why state carries the configured value.
	desc := bp.Resources[0].Schema.Attributes[0].MarkdownDescription
	if !strings.Contains(desc, "write-only in practice") {
		t.Errorf("description should name the write-only conclusion:\n%s", desc)
	}
}

// TestUnit_Merge_AForcedValueNeedsCorroboration holds serverForced to the writable=false
// bar: acting on it tells a practitioner their value can never take effect.
func TestUnit_Merge_AForcedValueNeedsCorroboration(t *testing.T) {
	t.Parallel()

	lit := probe.LiteralValue(blueprint.Literal{Kind: blueprint.KindBool, Raw: "true"})

	bp := testBlueprint()
	result, err := Apply(&bp, []probe.Fact{
		fact("colour", probe.FactServerForced, lit, probe.ConfidenceObserved),
	}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Conflicts) != 1 {
		t.Fatalf("an Observed forced value must conflict, not apply: %+v", result.Conflicts)
	}
	if bp.Resources[0].Schema.Attributes[0].Behaviour.ForcedValue != nil {
		t.Error("forcedValue must not be written without corroboration")
	}

	bp = testBlueprint()
	result, err = Apply(&bp, []probe.Fact{
		fact("colour", probe.FactServerForced, lit, probe.ConfidenceCorroborated),
	}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", result.Conflicts)
	}

	fv := bp.Resources[0].Schema.Attributes[0].Behaviour.ForcedValue
	if fv == nil || fv.Raw != "true" {
		t.Fatalf("corroborated forced value must be recorded, got %+v", fv)
	}
	if len(result.Recommendations) == 0 ||
		!strings.Contains(result.Recommendations[0], "Computed") {
		t.Errorf("a forced value should recommend Computed: %v", result.Recommendations)
	}
}

// TestUnit_Merge_AnUpdateDefaultDisagreeingWithTheCreateDefaultConflicts is the
// create-vs-update asymmetry: both defaults are real at once, so both are recorded,
// and the disagreement is surfaced because a static Default would lie on one path.
func TestUnit_Merge_AnUpdateDefaultDisagreeingWithTheCreateDefaultConflicts(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	bp.Resources[0].Schema.Attributes[0].Behaviour.ServerDefault = &blueprint.Literal{Kind: blueprint.KindString, Raw: `"blue"`}

	result, err := Apply(&bp, []probe.Fact{
		fact("colour", probe.FactUpdateDefault,
			probe.LiteralValue(blueprint.Literal{Kind: blueprint.KindString, Raw: `"red"`}),
			probe.ConfidenceObserved),
	}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(result.Conflicts), result.Conflicts)
	}
	if !strings.Contains(result.Conflicts[0].Why, "create and update paths") {
		t.Errorf("the conflict must explain the asymmetry: %q", result.Conflicts[0].Why)
	}

	ud := bp.Resources[0].Schema.Attributes[0].Behaviour.UpdateDefault
	if ud == nil || ud.Raw != `"red"` {
		t.Fatalf("the update default is a real observation and must still be recorded, got %+v", ud)
	}
}

// TestUnit_Merge_UpdateResetsIsARecommendationAndProse: nothing structural changes, but
// the finding must reach both the description and the recommendations.
func TestUnit_Merge_UpdateResetsIsARecommendationAndProse(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	result, err := Apply(&bp, []probe.Fact{
		fact("colour", probe.FactUpdateResets, probe.BoolValue(true), probe.ConfidenceObserved),
	}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Recommendations) == 0 ||
		!strings.Contains(result.Recommendations[0], "always send") {
		t.Errorf("updateResets should recommend always sending the field: %v",
			result.Recommendations)
	}
	if !strings.Contains(bp.Resources[0].Schema.Attributes[0].MarkdownDescription, "resets") {
		t.Error("the description should record the reset behaviour")
	}
}

// TestUnit_Merge_ZeroValueUnsendableIsStaticEvidence: the sdkbind scan's fact lands
// structurally, because the generator must know an optional bool whose only expressible
// value is the server default is not genuinely configurable.
func TestUnit_Merge_ZeroValueUnsendableIsStaticEvidence(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()
	f := fact("colour", probe.FactZeroValueUnsendable, probe.BoolValue(true), probe.ConfidenceCorroborated)
	f.Evidence = []string{"static:things.CreateThingRequest.Colour json:\"colour,omitempty\""}

	result, err := Apply(&bp, []probe.Fact{f}, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", result.Conflicts)
	}

	z := bp.Resources[0].Schema.Attributes[0].Behaviour.ZeroValueUnsendable
	if z == nil || !*z {
		t.Fatal("zeroValueUnsendable must land on the attribute")
	}
	if !strings.Contains(bp.Resources[0].Schema.Attributes[0].MarkdownDescription, "omitempty") {
		t.Error("the description should explain the encoding limit")
	}
}
