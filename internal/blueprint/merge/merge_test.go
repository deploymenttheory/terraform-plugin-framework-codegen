package merge

import (
	"errors"
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
	before := bp.Resources[0].Attributes[0].ComputedOptionalRequired

	facts := []probe.Fact{
		// A server-default fact with no literal: the probe looked and found nothing.
		fact("colour", probe.FactServerDefault, probe.Value{Text: "none observed"}, probe.Observed),
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
	if bp.Resources[0].Attributes[0].ComputedOptionalRequired != before {
		t.Errorf("presence changed to %q; narrowing must never be automatic",
			bp.Resources[0].Attributes[0].ComputedOptionalRequired)
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
			probe.Corroborated),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, SnapshotID: "1.0-t1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Fatalf("a confirmed default should not conflict: %+v", result.Conflicts)
	}
	if result.Err() != nil {
		t.Errorf("Err() = %v, want nil", result.Err())
	}

	attr := bp.Resources[0].Attributes[0]

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
		fact("colour", probe.FactDefaultIsDerived, probe.BoolValue(true), probe.Observed),
	}

	result, err := Apply(&bp, facts, Options{SnapshotID: "1.0-t1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Errorf("a derived default should not conflict with computed_optional: %+v", result.Conflicts)
	}
	if bp.Resources[0].Attributes[0].ComputedOptionalRequired != blueprint.ComputedOptional {
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
	withStatic.Resources[0].Attributes[0].Default = &blueprint.Default{
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
			fact("key", probe.FactRequiredByAPI, probe.BoolValue(true), probe.Observed),
		}

		result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, SnapshotID: "1.0-t1"})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		if len(result.Conflicts) != 0 {
			t.Errorf("confirming a requirement should not conflict: %+v", result.Conflicts)
		}

		attr := bp.Resources[0].Attributes[1]
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
			fact("key", probe.FactRequiredByAPI, probe.BoolValue(false), probe.Observed),
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
		if annotated.Resources[0].Attributes[1].ComputedOptionalRequired != blueprint.Required {
			t.Error("annotate must not change presence")
		}

		result, err = Apply(&bp, facts, Options{Strategy: StrategyApply})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		if bp.Resources[0].Attributes[1].ComputedOptionalRequired != blueprint.Optional {
			t.Errorf("presence = %q, want optional", bp.Resources[0].Attributes[1].ComputedOptionalRequired)
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
			result, err := Apply(&bp, []probe.Fact{fact("colour", tc.field, tc.value, probe.Observed)},
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
			result, err = Apply(&bp, []probe.Fact{fact("colour", tc.field, tc.value, probe.Corroborated)},
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
		fact("colour", probe.FactImmutable, probe.BoolValue(true), probe.Corroborated),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Attributes[0]

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
		fact("colour", probe.FactWritable, probe.BoolValue(false), probe.Suspected),
		fact("colour", probe.FactVolatile, probe.BoolValue(true), probe.Suspected),
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
	if bp.Resources[0].Attributes[0].Behaviour.Writable != nil {
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
		Resource: "thing", Field: probe.FactReadBack, Confidence: probe.Observed, Probe: "test",
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
		Resource: "thing", Field: probe.FactUpdateStyle, Confidence: probe.Observed, Probe: "test",
		Value:     probe.TextValue(string(blueprint.UpdateMergePatch)),
		Evidence:  []string{"005-put-things-1"},
		Rationale: "a field omitted from an update survived",
	}

	// Unset: filled in.
	bp := testBlueprint()
	if _, err := Apply(&bp, []probe.Fact{observed}, Options{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bp.Resources[0].Policy.UpdateStyle != blueprint.UpdateMergePatch {
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

// TestUnit_Merge_NestedFieldsAreReached: a fact about a field inside an object has to land on
// the right attribute, addressed by its dotted JSON path.
func TestUnit_Merge_NestedFieldsAreReached(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("items.mode", probe.FactVolatile, probe.BoolValue(true), probe.Observed),
	}

	result, err := Apply(&bp, facts, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Conflicts) != 0 {
		t.Fatalf("a nested field should be found: %+v", result.Conflicts)
	}

	nested := bp.Resources[0].Attributes[2].Type.NestedObject.Attributes[0]
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
		fact("nonexistent", probe.FactVolatile, probe.BoolValue(true), probe.Observed),
		{
			Resource: "other", Field: probe.FactVolatile, Value: probe.BoolValue(true),
			Confidence: probe.Observed, Probe: "test",
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

	facts := []probe.Fact{fact("colour", "telepathy", probe.BoolValue(true), probe.Observed)}

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
			probe.Corroborated),
		fact("colour", probe.FactVolatile, probe.BoolValue(false), probe.Observed),
		fact("key", probe.FactRequiredByAPI, probe.BoolValue(true), probe.Observed),
	}

	bp := testBlueprint()
	opts := Options{Strategy: StrategyApply, SnapshotID: "1.0-t1785152261691"}

	first, err := Apply(&bp, facts, opts)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if !first.Changed() {
		t.Fatal("the first merge should change something, or this test is vacuous")
	}

	afterFirst := bp.Resources[0].Attributes[0].MarkdownDescription

	second, err := Apply(&bp, facts, opts)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	if second.Changed() {
		t.Errorf("the second merge changed %d thing(s); merging the same evidence twice must be "+
			"a no-op:\n%+v", len(second.Changes), second.Changes)
	}
	if got := bp.Resources[0].Attributes[0].MarkdownDescription; got != afterFirst {
		t.Errorf("the description drifted on a second merge:\n--- first\n%s\n--- second\n%s",
			afterFirst, got)
	}

	// Newer evidence *should* produce a visible one-line diff, so a reader can see which
	// recording a description came from.
	third, err := Apply(&bp, facts, Options{Strategy: StrategyApply, SnapshotID: "1.1-t9"})
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
	got := replaceBlock("Curated prose.", buildBlock([]string{"Observed: x."}, "s1"))
	if !strings.HasPrefix(got, "Curated prose.") {
		t.Errorf("curated prose should come first: %q", got)
	}
	if !strings.Contains(got, "\n\n<!-- probed:s1 -->") {
		t.Errorf("the block should be a separate paragraph: %q", got)
	}

	// Replacing swaps only the block.
	replaced := replaceBlock(got, buildBlock([]string{"Observed: y."}, "s2"))
	if strings.Contains(replaced, "Observed: x.") {
		t.Errorf("the old block should be gone: %q", replaced)
	}
	if !strings.HasPrefix(replaced, "Curated prose.") {
		t.Errorf("curated prose should survive replacement: %q", replaced)
	}
	if strings.Count(replaced, "<!-- probed:") != 1 {
		t.Errorf("there should be exactly one block: %q", replaced)
	}

	// An empty description gets the block alone, with no leading blank line.
	bare := replaceBlock("", buildBlock([]string{"Observed: x."}, "s1"))
	if strings.HasPrefix(bare, "\n") {
		t.Errorf("no leading newline on an empty description: %q", bare)
	}

	// An unclosed marker is a hand-edit gone wrong. Treated as absent, so the next merge
	// writes a well-formed block rather than nesting inside the broken one.
	broken := "Prose.\n\n<!-- probed:s1 -->\nObserved: x."
	if _, ok := extractBlock(broken); ok {
		t.Error("an unclosed marker must not be treated as a block")
	}

	// StripBlock is what a drift check needs, so newer evidence alone does not read as a change.
	if stripped := StripBlock(got); stripped != "Curated prose." {
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
			Confidence: probe.Observed, Probe: "test",
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

func TestUnit_Merge_EnumFactsDescribeButDoNotValidate(t *testing.T) {
	t.Parallel()

	bp := testBlueprint()

	facts := []probe.Fact{
		fact("colour", probe.FactEnumAccepted,
			probe.ListValue([]string{"blue", "red"}), probe.Observed),
		fact("colour", probe.FactEnumRejectedDocumented,
			probe.ListValue([]string{"deprecated"}), probe.Observed),
	}

	result, err := Apply(&bp, facts, Options{Strategy: StrategyApply, SnapshotID: "s1"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	attr := bp.Resources[0].Attributes[0]

	// **No validator**, ever. An over-tight one rejects configurations the API would have
	// accepted, and the practitioner cannot work around it.
	if len(attr.Validators) != 0 {
		t.Errorf("merge must not generate a validator: %+v", attr.Validators)
	}
	for _, want := range []string{"blue", "red", "deprecated"} {
		if !strings.Contains(attr.MarkdownDescription, want) {
			t.Errorf("the description should mention %q: %q", want, attr.MarkdownDescription)
		}
	}

	var recommended bool
	for _, r := range result.Recommendations {
		if strings.Contains(r, "OneOf") {
			recommended = true
		}
	}
	if !recommended {
		t.Errorf("a OneOf should be recommended, not generated: %v", result.Recommendations)
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
