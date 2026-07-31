package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func boolp(b bool) *bool { return &b }

// TestUnit_Render_ImmutableEvidenceBecomesRequiresReplace closes the loop the README
// opens with: "which are immutable, and so need RequiresReplace". The fact was probed,
// corroborated, merged into the blueprint and then never read -- an in-place update the
// API refuses, instead of a planned replacement.
//
// The value is triple-gated before render sees it -- the prober demands corroboration,
// merge only recommends, and a human commits the blueprint diff -- so consuming it here
// is consuming an opt-in, not making one.
func TestUnit_Render_ImmutableEvidenceBecomesRequiresReplace(t *testing.T) {
	t.Parallel()

	imports := newImportSet()

	immutable := attr("object_type", blueprint.KindString, blueprint.Optional)
	immutable.Behaviour.Immutable = boolp(true)

	got := planModifiersFor(testResourceScope, immutable, imports)
	if len(got) != 1 || !strings.Contains(got[0].SchemaDefinition, "stringplanmodifier.RequiresReplace()") {
		t.Fatalf("an immutable writable attribute should force replacement: %+v", got)
	}
	if !strings.Contains(got[0].SchemaDefinition, "prober corroborated") {
		t.Errorf("the provenance should be stated where a schema reader meets it: %s",
			got[0].SchemaDefinition)
	}

	// The modifier follows the attribute's kind.
	immutableBool := attr("enabled", blueprint.KindBool, blueprint.Required)
	immutableBool.Behaviour.Immutable = boolp(true)
	if got := planModifiersFor(testResourceScope, immutableBool, imports); len(got) != 1 ||
		!strings.Contains(got[0].SchemaDefinition, "boolplanmodifier.RequiresReplace()") {
		t.Errorf("the modifier package should follow the kind: %+v", got)
	}

	// A computed attribute cannot be configured, so there is no change to force
	// replacement on.
	computed := attr("created", blueprint.KindString, blueprint.Computed)
	computed.Behaviour.Immutable = boolp(true)
	if got := planModifiersFor(testResourceScope, computed, imports); len(got) != 0 {
		t.Errorf("a computed attribute should get no RequiresReplace: %+v", got)
	}

	// Hand-declared modifiers still win outright -- the per-attribute escape hatch, in
	// both directions.
	declared := attr("object_type", blueprint.KindString, blueprint.Optional)
	declared.Behaviour.Immutable = boolp(true)
	declared.PlanModifiers = []blueprint.CustomCode{{SchemaDefinition: "mine()"}}
	if got := planModifiersFor(testResourceScope, declared, imports); len(got) != 1 ||
		got[0].SchemaDefinition != "mine()" {
		t.Errorf("a declared modifier set should replace the synthesis: %+v", got)
	}
}

// TestUnit_Render_ReplaceOnlyForcesReplacementEverywhere.
//
// The structural tier: an API with no update operation has nowhere for an in-place change
// to go, so a provider without the modifier on every writable attribute is simply broken
// on any change. No evidence is involved -- "there is no update" is a property of the
// binding.
func TestUnit_Render_ReplaceOnlyForcesReplacementEverywhere(t *testing.T) {
	t.Parallel()

	imports := newImportSet()
	sc := testResourceScope
	sc.replaceOnly = true

	writable := attr("value", blueprint.KindString, blueprint.Optional)
	if got := planModifiersFor(sc, writable, imports); len(got) != 1 ||
		!strings.Contains(got[0].SchemaDefinition, "RequiresReplace()") {
		t.Errorf("every writable attribute must force replacement: %+v", got)
	}

	computed := attr("created", blueprint.KindString, blueprint.Computed)
	if got := planModifiersFor(sc, computed, imports); len(got) != 0 {
		t.Errorf("a computed attribute is not writable: %+v", got)
	}
}

// TestUnit_Render_ReplaceOnlyWithAnUpdateBindingIsRefused: the two claims contradict, and
// emitting either reading silently would make the other a lie.
func TestUnit_Render_ReplaceOnlyWithAnUpdateBindingIsRefused(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(attr("value", blueprint.KindString, blueprint.Required))
	r.Policy.UpdateStyle = blueprint.UpdateReplaceOnly
	r.Binding.Update = &blueprint.Operation{
		Style: blueprint.CallStyleMethod, Method: "UpdateThing",
		Return: blueprint.ReturnTransportError,
	}

	_, err := crudView(bp, r)
	if err == nil || !strings.Contains(err.Error(), "replaceOnly") {
		t.Fatalf("the contradiction must be refused naming both halves: %v", err)
	}
}

// TestUnit_Render_TheMaximalFixtureKeepsImmutableValuesOut.
//
// The maximal configuration drives the update step. Introducing an optional immutable
// value there would force a replacement in the middle of a test asserting in-place
// update -- passing while silently testing something else.
func TestUnit_Render_TheMaximalFixtureKeepsImmutableValuesOut(t *testing.T) {
	t.Parallel()

	required := attr("key", blueprint.KindString, blueprint.Required)
	optionalImmutable := attr("region", blueprint.KindString, blueprint.Optional)
	optionalImmutable.Behaviour.Immutable = boolp(true)
	optionalMutable := attr("value", blueprint.KindString, blueprint.Optional)

	bp, r := fixtureResource(required, optionalImmutable, optionalMutable)

	maximal, err := Fixture(bp, r, Options{}, false)
	if err != nil {
		t.Fatalf("Fixture: %v", err)
	}

	for _, v := range maximal.Values {
		if v.Name == "region" {
			t.Error("an optional immutable attribute must stay out of the update configuration")
		}
	}

	var noted bool
	for _, s := range maximal.Skipped {
		if s.Name == "region" && strings.Contains(s.Comment, "force replacement") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the omission must be stated, not silent: %+v", maximal.Skipped)
	}

	// A required immutable attribute stays: create needs it, and its value is identical
	// across fixtures by construction, so the update step never touches it.
	requiredImmutable := attr("key", blueprint.KindString, blueprint.Required)
	requiredImmutable.Behaviour.Immutable = boolp(true)
	bp2, r2 := fixtureResource(requiredImmutable, optionalMutable)

	minimal, err := Fixture(bp2, r2, Options{}, true)
	if err != nil {
		t.Fatalf("minimal: %v", err)
	}
	maximal2, err := Fixture(bp2, r2, Options{}, false)
	if err != nil {
		t.Fatalf("maximal: %v", err)
	}

	find := func(vs []fixtureValue, name string) string {
		for _, v := range vs {
			if v.Name == name {
				return v.HCL
			}
		}
		return ""
	}
	if find(minimal.Values, "key") == "" ||
		find(minimal.Values, "key") != find(maximal2.Values, "key") {
		t.Error("a required immutable value must be pinned identical across both fixtures")
	}
}

// TestUnit_Render_AReplaceStepNeedsASecondUsableValue.
//
// The step exists to prove the generated RequiresReplace live, and it can only run when
// an immutable attribute in the create configuration has another value the API takes.
func TestUnit_Render_AReplaceStepNeedsASecondUsableValue(t *testing.T) {
	t.Parallel()

	// Two observed-accepted values: the step runs, flipping to the other one.
	flippable := attr("object_type", blueprint.KindString, blueprint.Required)
	flippable.Behaviour.Immutable = boolp(true)
	flippable.Behaviour.AcceptedValues = []string{"test", "dashboard"}

	bp, r := fixtureResource(flippable)
	r.Binding.Read = &blueprint.Operation{
		Style: blueprint.CallStyleMethod, Method: "GetThing",
		Return: blueprint.ReturnResultTransportError, ResultType: "things.Thing",
	}

	view, ok, err := ReplaceFixture(bp, r, Options{})
	if err != nil {
		t.Fatalf("ReplaceFixture: %v", err)
	}
	if !ok {
		t.Fatal("two accepted values should make the attribute flippable")
	}

	var flipped bool
	for _, v := range view.Values {
		if v.Name == "object_type" && v.HCL == `"dashboard"` {
			flipped = true
		}
	}
	if !flipped {
		t.Errorf("the fixture should carry the second observed-accepted value: %+v", view.Values)
	}

	// A rejected value is not usable, however documented.
	rejected := flippable
	rejected.Behaviour.AcceptedValues = []string{"test"}
	rejected.Type.AllowedValues = []string{"test", "system"}
	rejected.Behaviour.RejectedValues = []string{"system"}

	_, r2 := fixtureResource(rejected)
	r2.Binding.Read = r.Binding.Read

	if _, ok, _ := ReplaceFixture(bp, r2, Options{}); ok {
		t.Error("a documented-but-refused value must not be flipped to")
	}
}

// TestUnit_Render_ConditionalRequirementsBecomeConfigValidators is the emitter's half of
// the conditional-fact contract: a variant recording requiredByApi under one gate value
// renders as a requiredWhen entry, enforced at plan time where it costs a message rather
// than at apply time where the API's refusal costs a run.
func TestUnit_Render_ConditionalRequirementsBecomeConfigValidators(t *testing.T) {
	t.Parallel()

	gate := attr("type", blueprint.KindString, blueprint.Optional)
	gate.Wire.JSONPath = "type"

	target := attr("match_type", blueprint.KindString, blueprint.Optional)
	target.Wire.JSONPath = "matchType"
	target.Behaviour.Conditional = []blueprint.BehaviourVariant{{
		When:      []blueprint.Condition{{JSONPath: "type", Equals: "dynamic"}},
		Behaviour: blueprint.Behaviour{RequiredByAPI: boolp(true)},
	}}

	_, r := fixtureResource(gate, target)

	rules := requiredWhenRules(r)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v, want exactly one", rules)
	}
	if rules[0].GateAttr != "type" || rules[0].Equals != "dynamic" ||
		rules[0].TargetAttr != "match_type" {
		t.Errorf("rule = %+v", rules[0])
	}

	entry := rules[0].entry()
	for _, want := range []string{
		"requiredWhen{", `path.Root("type")`, `equals: "dynamic"`, `path.Root("match_type")`,
		"specification does not declare it",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("entry missing %q:\n%s", want, entry)
		}
	}

	// The support file exists exactly when the entries do.
	if _, ok := ConditionalValidatorFile(r, Options{}); !ok {
		t.Error("the requiredWhen type must be emitted beside its entries")
	}
	if _, ok := ConditionalValidatorFile(blueprint.Resource{}, Options{}); ok {
		t.Error("no rules, no support file")
	}
}

// TestUnit_Render_ConditionalRequirementsAreRefusedWithoutACarrier.
//
// A rule that cannot be enforced must not be half-enforced: anything the derivation
// cannot carry stays in the description, visible and unenforced, which is the honest
// floor.
func TestUnit_Render_ConditionalRequirementsAreRefusedWithoutACarrier(t *testing.T) {
	t.Parallel()

	variant := func(when ...blueprint.Condition) []blueprint.BehaviourVariant {
		return []blueprint.BehaviourVariant{{
			When:      when,
			Behaviour: blueprint.Behaviour{RequiredByAPI: boolp(true)},
		}}
	}

	gate := attr("type", blueprint.KindString, blueprint.Optional)
	gate.Wire.JSONPath = "type"

	// A conjunctive precondition has no single gate to read.
	twoGates := attr("match_type", blueprint.KindString, blueprint.Optional)
	twoGates.Behaviour.Conditional = variant(
		blueprint.Condition{JSONPath: "type", Equals: "dynamic"},
		blueprint.Condition{JSONPath: "scope", Equals: "custom"},
	)

	// A computed gate is a branch the configuration cannot reach.
	computedGate := attr("kind", blueprint.KindString, blueprint.Computed)
	computedGate.Wire.JSONPath = "kind"
	gatedOnComputed := attr("filters", blueprint.KindString, blueprint.Optional)
	gatedOnComputed.Behaviour.Conditional = variant(
		blueprint.Condition{JSONPath: "kind", Equals: "dynamic"})

	// A required target is already enforced schema-wide.
	requiredTarget := attr("key", blueprint.KindString, blueprint.Required)
	requiredTarget.Behaviour.Conditional = variant(
		blueprint.Condition{JSONPath: "type", Equals: "dynamic"})

	_, r := fixtureResource(gate, twoGates, computedGate, gatedOnComputed, requiredTarget)

	if rules := requiredWhenRules(r); len(rules) != 0 {
		t.Errorf("none of these can carry a rule: %+v", rules)
	}
}
