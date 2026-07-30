package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// valued builds a string attribute with a documented value set.
func valued(vals ...string) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     "mode",
		GoField:                  "Mode",
		ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind:          blueprint.KindString,
			AllowedValues: vals,
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// TestUnit_Render_OneOfComesFromTheDocumentedSet is the decision this phase turns on.
//
// The validator is built from AttrType.AllowedValues and not from
// Behaviour.AcceptedValues, which reads backwards until you consider which way each errs.
// The documented set is the wider of the two, so building from it errs toward permitting
// and a stale specification surfaces as the API's own error. Building from the observed set
// would err toward blocking a configuration another tenant can use.
func TestUnit_Render_OneOfComesFromTheDocumentedSet(t *testing.T) {
	t.Parallel()

	a := valued("all", "partner", "system")
	// This tenant took only one of the three and refused another outright.
	a.Behaviour.AcceptedValues = []string{"all"}
	a.Behaviour.RejectedValues = []string{"system"}
	a.Behaviour.ValuesClosed = boolPtr(true)

	imports := newImportSet()

	got := validatorsFor(a, imports, newPatternVars())
	if len(got) != 1 {
		t.Fatalf("got %d validators, want 1: %+v", len(got), got)
	}

	want := `stringvalidator.OneOf("all", "partner", "system")`
	if got[0].SchemaDefinition != want {
		t.Errorf("SchemaDefinition = %q, want %q", got[0].SchemaDefinition, want)
	}
	if !strings.Contains(imports.render("irrelevant"), "stringvalidator") {
		t.Error("the stringvalidator package should be registered")
	}
}

// TestUnit_Render_AnOpenValueSetSuppressesTheValidator covers the one case with direct
// evidence that generating it would be harmful.
//
// This path has no coverage from the pilot: every probed attribute there turned out closed,
// so without this test the suppression would ship unexercised.
func TestUnit_Render_AnOpenValueSetSuppressesTheValidator(t *testing.T) {
	t.Parallel()

	a := valued("and", "or")
	// The prober sent a value from outside the documented set and the API took it.
	a.Behaviour.ValuesClosed = boolPtr(false)

	if got := validatorsFor(a, newImportSet(), newPatternVars()); len(got) != 0 {
		t.Errorf("an open value set must generate no validator: %+v", got)
	}

	// Observed closed, and it comes back.
	a.Behaviour.ValuesClosed = boolPtr(true)
	if got := validatorsFor(a, newImportSet(), newPatternVars()); len(got) != 1 {
		t.Errorf("a closed value set should generate one validator: %+v", got)
	}

	// Never probed is not the same as observed open. An unprobed attribute keeps its
	// validator, or ingesting a specification and emitting from it straight away would
	// produce no validators at all.
	a.Behaviour.ValuesClosed = nil
	if got := validatorsFor(a, newImportSet(), newPatternVars()); len(got) != 1 {
		t.Errorf("an unprobed attribute should still get its validator: %+v", got)
	}
}

// TestUnit_Render_ValidatorIsStringAndConfigurableOnly pins the two other suppressions.
func TestUnit_Render_ValidatorIsStringAndConfigurableOnly(t *testing.T) {
	t.Parallel()

	// A purely computed attribute is never configured, so a validator on it cannot run.
	computed := valued("static", "dynamic")
	computed.ComputedOptionalRequired = blueprint.Computed
	if got := validatorsFor(computed, newImportSet(), newPatternVars()); len(got) != 0 {
		t.Errorf("a computed attribute needs no validator: %+v", got)
	}

	// Optional-and-computed can be set, so it does.
	both := valued("static", "dynamic")
	both.ComputedOptionalRequired = blueprint.ComputedOptional
	if got := validatorsFor(both, newImportSet(), newPatternVars()); len(got) != 1 {
		t.Errorf("an optional-and-computed attribute should get one: %+v", got)
	}

	// A collection of strings needs the element-level wrapper, which is a different shape;
	// stringvalidator.OneOf applied to a set does not compile.
	set := valued("a", "b")
	set.Type.Kind = blueprint.KindSet
	set.Type.ElementType = &blueprint.AttrType{Kind: blueprint.KindString}
	if got := validatorsFor(set, newImportSet(), newPatternVars()); len(got) != 0 {
		t.Errorf("a collection should not take a bare string validator: %+v", got)
	}
}

// TestUnit_Render_HandAuthoredValidatorsComeFirst checks the generated one is added to
// rather than replacing what the blueprint declares.
func TestUnit_Render_HandAuthoredValidatorsComeFirst(t *testing.T) {
	t.Parallel()

	a := valued("and", "or")
	a.Validators = []blueprint.CustomCode{{SchemaDefinition: "myvalidator.Whatever()"}}

	got := validatorsFor(a, newImportSet(), newPatternVars())
	if len(got) != 2 {
		t.Fatalf("got %d validators, want the declared one plus the generated one: %+v", len(got), got)
	}
	if got[0].SchemaDefinition != "myvalidator.Whatever()" {
		t.Errorf("the hand-authored validator should come first, got %q", got[0].SchemaDefinition)
	}

	// And validatorsFor must not mutate the attribute's own slice, or a second render would
	// accumulate.
	if len(a.Validators) != 1 {
		t.Errorf("the attribute's validators were mutated: %+v", a.Validators)
	}
}

// TestUnit_Render_ValidatorNoteNamesRejectedValues checks the comment, and that it appears
// only when there is something to say.
func TestUnit_Render_ValidatorNoteNamesRejectedValues(t *testing.T) {
	t.Parallel()

	a := valued("all", "partner", "system")
	a.Behaviour.RejectedValues = []string{"system"}

	note := validatorNote(a)
	if !strings.Contains(note, `"system"`) {
		t.Errorf("the note should name the refused value: %q", note)
	}
	if !strings.HasPrefix(note, "// ") {
		t.Errorf("the note should be a comment: %q", note)
	}
	// Wrapped, because gofumpt does not reflow comments and these embed a value set.
	for _, line := range strings.Split(note, "\n") {
		if len(line) > commentWidth {
			t.Errorf("comment line is %d wide, over %d: %q", len(line), commentWidth, line)
		}
	}

	// Nothing refused, nothing to say.
	a.Behaviour.RejectedValues = nil
	if got := validatorNote(a); got != "" {
		t.Errorf("no refusal means no note, got %q", got)
	}
}
